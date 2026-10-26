package rig

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/session"
)

// Result is the answer to every intent: done, or why not and what to do about it. The same
// words are logged.
type Result struct {
	OK     bool   `json:"ok"`
	Reason string `json:"reason,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

// Writer is a radio's poller as the intents use it: its state, and writes that show their
// effect at once and are confirmed by the next read.
type Writer interface {
	State() State
	Write(ctx context.Context, fn func(Commander) error, optimistic func(*State)) error
}

// Urgent is a Writer whose rigctld link can be told an unkey is owed (its reconnect backoff is
// then capped; see Client.SetUrgent).
type Urgent interface {
	SetUrgent(on bool)
}

// PollerWriter adapts a Poller (whose Write hands out a *Client) to Writer.
func PollerWriter(p *Poller) Writer { return pollerWriter{p} }

type pollerWriter struct{ p *Poller }

func (w pollerWriter) State() State { return w.p.State() }

func (w pollerWriter) SetUrgent(on bool) {
	if w.p.client != nil {
		w.p.client.SetUrgent(on)
	}
}

func (w pollerWriter) Write(ctx context.Context, fn func(Commander) error, opt func(*State)) error {
	return w.p.Write(ctx, func(c *Client) error { return fn(c) }, opt)
}

// OwnerLock is the transmitter lock (*session.Owners).
type OwnerLock interface {
	Claim(radio string, h session.Holder) error
	TakeOver(radio string, h session.Holder) error
	Release(radio, id string)
	Holder(radio string) (session.Holder, bool)
	Touch(radio, id string)
}

const (
	// CarrierLimit is the longest a tune carrier stays up.
	CarrierLimit = 10 * time.Second

	reasonUnreachable = "rig control unreachable; the radio's rigctld is restarting"
	fixUnreachable    = "wait a few seconds"
	reasonDataMode    = "a data mode would key the sound card with silence"
	fixTuneMode       = "switch to FM or AM to tune"
)

var (
	dataModes = []string{"PKTUSB", "PKTLSB", "PKTFM", "PKTAM", "RTTY", "RTTYR", "FM-D", "AM-D"}
	// carrierModes put a carrier on the air from PTT alone, with no audio: the only modes Tune
	// keys in (owner's ruling 9; SSB has no carrier and CW keys from the key line, not PTT)
	carrierModes = []string{"AM", "AMS", "FM", "FMN", "WFM"}
)

// tuneModeRefusal says why Tune cannot key in mode, or "" when it can.
func tuneModeRefusal(mode string) string {
	switch {
	case slices.Contains(carrierModes, mode):
		return ""
	case slices.Contains(dataModes, mode):
		return reasonDataMode
	case mode == "":
		return "the radio's mode is not known yet"
	}
	return mode + " sends no carrier from PTT alone"
}

// Intents is everything a browser may ask of one radio.
type Intents struct {
	Radio  string
	Caps   func() (Caps, bool) // false until the radio's capabilities are known
	Poller Writer
	Owners OwnerLock
	Clock  Clock
	Log    *slog.Logger
	Ctx    context.Context // ends the unkey retry loop (nil: never)
	Gone   func() bool     // true once the radio no longer exists; ends the retry loop
	Marks  *KeyedMarks     // the keyed markers that outlive a crash (nil: none)

	mu       sync.Mutex
	carrier  chan struct{} // closed to cancel the running carrier timer
	keyed    bool          // a tune carrier was keyed and not yet confirmed off
	pending  bool          // set_ptt 0 is owed to the radio
	debt     uint64        // bumped whenever keyed or pending is set: an unkey clears only the debt it was sent for
	keying   int           // key-ons between their marker write and their answer: the marker is theirs
	retrying bool          // a retry loop is running
	closed   chan struct{} // closed by Close

	// set by NewPTT: whether a PTT window is open, and what to tell it after an unkey that was
	// not its own (the carrier machinery dropped the radio's PTT line, so the window ends too)
	pttActive  func() bool
	afterUnkey func(intent string)
}

// Close stops the unkey retry loop (the radio left the records or the service is ending).
func (in *Intents) Close() {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.closed == nil {
		in.closed = make(chan struct{})
	}
	select {
	case <-in.closed:
	default:
		close(in.closed)
	}
}

func (in *Intents) closedCh() <-chan struct{} {
	in.mu.Lock()
	defer in.mu.Unlock()
	if in.closed == nil {
		in.closed = make(chan struct{})
	}
	return in.closed
}

// RetryEvery is how often an owed unkey is retried.
const RetryEvery = time.Second

func (in *Intents) log() *slog.Logger {
	if in.Log != nil {
		return in.Log
	}
	return slog.Default()
}

func (in *Intents) clock() Clock {
	if in.Clock != nil {
		return in.Clock
	}
	return realClock{}
}

func (in *Intents) refuse(intent, reason, fix string) Result {
	in.log().Info("intent refused", "radio", in.Radio, "intent", intent, "reason", reason, "fix", fix)
	return Result{Reason: reason, Fix: fix}
}

// check is the rig-link guard plus the capability lookup shared by every command intent.
func (in *Intents) check(intent string) (State, Caps, *Result) {
	st := in.Poller.State()
	if !st.Connected {
		r := in.refuse(intent, reasonUnreachable, fixUnreachable)
		return st, Caps{}, &r
	}
	caps, ok := in.Caps()
	if !ok {
		r := in.refuse(intent, "this radio's capabilities are not known yet", fixUnreachable)
		return st, Caps{}, &r
	}
	return st, caps, nil
}

func (in *Intents) send(ctx context.Context, intent string, opt func(*State), cmd string, args ...string) Result {
	err := in.Poller.Write(ctx, func(c Commander) error {
		_, err := c.Do(ctx, cmd, args...)
		return err
	}, opt)
	if err != nil {
		return in.refuse(intent, rigFailure(err), "check the radio and try again")
	}
	return Result{OK: true}
}

func rigFailure(err error) string {
	var re *RigError
	switch {
	case errors.As(err, &re):
		return fmt.Sprintf("the radio refused the command (rigctld error %d)", re.Code)
	case errors.Is(err, ErrDeadline):
		return "the radio did not answer in time"
	case errors.Is(err, ErrNotConnected):
		return reasonUnreachable
	}
	return "the command could not be sent to the radio"
}

// Freq tunes the radio.
func (in *Intents) Freq(ctx context.Context, hz int64) Result {
	_, caps, bad := in.check("freq")
	if bad != nil {
		return *bad
	}
	if !caps.InRange(hz) {
		return in.refuse("freq", fmt.Sprintf("%d is outside this radio's range", hz), rangeFix(caps))
	}
	return in.send(ctx, "freq", func(s *State) { s.Freq = hz }, "set_freq", strconv.FormatInt(hz, 10))
}

func rangeFix(c Caps) string {
	if len(c.FreqRanges) == 0 {
		return "this radio's frequency range is not known"
	}
	lo, hi := c.FreqRanges[0].Lo, c.FreqRanges[0].Hi
	for _, r := range c.FreqRanges[1:] {
		lo, hi = min(lo, r.Lo), max(hi, r.Hi)
	}
	return fmt.Sprintf("this radio tunes %d–%d Hz", lo, hi)
}

// Mode sets the mode; passband 0 leaves the radio's default.
func (in *Intents) Mode(ctx context.Context, mode string, passband int) Result {
	_, caps, bad := in.check("mode")
	if bad != nil {
		return *bad
	}
	if !caps.Allows(mode) {
		return in.refuse("mode", fmt.Sprintf("this radio has no mode %s", mode), "modes: "+strings.Join(caps.Modes, " "))
	}
	if passband < 0 {
		return in.refuse("mode", fmt.Sprintf("%d is outside this radio's range", passband), "a passband is 0 or more Hz")
	}
	pb := strconv.Itoa(passband)
	return in.send(ctx, "mode", func(s *State) { s.Mode, s.Passband = mode, pb }, "set_mode", mode, pb)
}

// VFO selects a VFO.
func (in *Intents) VFO(ctx context.Context, which string) Result {
	_, caps, bad := in.check("vfo")
	if bad != nil {
		return *bad
	}
	if !caps.CanSetVFO || !slices.Contains(caps.VFOs, which) {
		return in.refuse("vfo", fmt.Sprintf("this radio has no VFO %s", which), "VFOs: "+strings.Join(caps.VFOs, " "))
	}
	return in.send(ctx, "vfo", func(s *State) { s.VFO = which }, "set_vfo", which)
}

// Split turns split on or off.
func (in *Intents) Split(ctx context.Context, on bool) Result {
	_, caps, bad := in.check("split")
	if bad != nil {
		return *bad
	}
	if !caps.CanSetSplit {
		return in.refuse("split", "this radio's split cannot be set over CAT", "set split on the radio itself")
	}
	n, tx := "0", "VFOA"
	if on {
		n, tx = "1", "VFOB"
	}
	return in.send(ctx, "split", func(s *State) { s.Split = on }, "set_split_vfo", n, tx)
}

// Level sets one of the radio's settable levels, a float from 0 to 1.
func (in *Intents) Level(ctx context.Context, name string, value float64) Result {
	_, caps, bad := in.check("level")
	if bad != nil {
		return *bad
	}
	if !slices.Contains(caps.SettableLevels, name) {
		return in.refuse("level", fmt.Sprintf("%s is not a level this radio sets", name), "levels: "+strings.Join(caps.SettableLevels, " "))
	}
	if math.IsNaN(value) || value < 0 || value > 1 {
		return in.refuse("level", fmt.Sprintf("%g is outside this radio's range", value), "levels run from 0 to 1")
	}
	v := strconv.FormatFloat(value, 'f', -1, 64)
	return in.send(ctx, "level", func(s *State) { s.Levels[name] = value }, "set_level", name, v)
}

func (in *Intents) owns(intent, tab string) *Result {
	if h, ok := in.Owners.Holder(in.Radio); ok && h.Kind == "tab" && h.ID == tab {
		return nil
	}
	r := in.refuse(intent, "this tab does not hold the transmitter", "claim the radio first")
	return &r
}

// Tune keys a carrier for at most CarrierLimit (on) or ends it (off). Only the owner may.
func (in *Intents) Tune(ctx context.Context, tab string, on bool) Result {
	if !on {
		if bad := in.owns("tune", tab); bad != nil {
			return *bad
		}
		return in.Stop(ctx)
	}
	st, _, bad := in.check("tune")
	if bad != nil {
		return *bad
	}
	if bad := in.owns("tune", tab); bad != nil {
		return *bad
	}
	if in.pttOn() {
		return in.refuse("tune", "PTT is on", "release PTT first")
	}
	if why := tuneModeRefusal(st.Mode); why != "" {
		return in.refuse("tune", why, fixTuneMode)
	}
	in.Owners.Touch(in.Radio, tab)
	var cancel chan struct{}
	res := in.keyOn(ctx, "tune", func() {
		if in.carrier != nil {
			close(in.carrier)
		}
		cancel = make(chan struct{})
		in.carrier = cancel
	})
	if !res.OK {
		return res
	}
	fire := in.clock().At(in.clock().Now().Add(CarrierLimit)) // taken now, not in the goroutine
	go func() {
		select {
		case <-cancel:
		case <-fire:
			in.mu.Lock()
			current := in.carrier == cancel
			if current {
				in.carrier = nil
			}
			in.mu.Unlock()
			if current {
				in.log().Info("tune carrier ended at its limit", "radio", in.Radio)
				in.unkey("tune")
			}
		}
	}()
	return res
}

// keyOn is the one key-on path, Tune's and PTT's: the keyed marker first, then set_ptt 1. A
// key-on whose fate is unknown owes the radio an unkey (retried until it answers); one the radio
// refused owes nothing. keyed runs with in.mu held at the moment the carrier becomes ours.
func (in *Intents) keyOn(ctx context.Context, intent string, keyed func()) Result {
	if err := in.mark(); err != nil {
		in.log().Error("could not write the keyed marker; not keying", "radio", in.Radio, "error", err)
		return in.refuse(intent, "the station could not note the carrier before keying", "check the station's storage")
	}
	err := in.Poller.Write(ctx, func(c Commander) error {
		_, err := c.Do(ctx, "set_ptt", "1")
		return err
	}, func(s *State) { s.PTT = true })
	if err != nil {
		res := in.refuse(intent, rigFailure(err), "check the radio and try again")
		var re *RigError
		if !errors.As(err, &re) {
			// the key-on may have reached the radio: owe the unkey
			in.mu.Lock()
			in.keyed, in.pending = true, true
			in.debt++
			in.keying--
			in.mu.Unlock()
			in.startRetry(intent)
		} else {
			// the radio refused: nothing was keyed by this one
			in.mu.Lock()
			in.keying--
			if !in.keyed && !in.pending {
				in.clearMarkLocked()
			}
			in.mu.Unlock()
		}
		return res
	}
	in.mu.Lock()
	if keyed != nil {
		keyed()
	}
	in.keyed, in.pending = true, false
	in.debt++
	in.keying--
	in.mu.Unlock()
	return Result{OK: true}
}

// pttOn reports whether a PTT window is open on this radio.
func (in *Intents) pttOn() bool {
	in.mu.Lock()
	fn := in.pttActive
	in.mu.Unlock()
	return fn != nil && fn()
}

// carrierUp reports whether a tune carrier's timer is running.
func (in *Intents) carrierUp() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.carrier != nil
}

func (in *Intents) endCarrier() {
	in.mu.Lock()
	if in.carrier != nil {
		close(in.carrier)
		in.carrier = nil
	}
	in.mu.Unlock()
}

// unkey owes the radio a set_ptt 0, tries it now and, if the radio cannot take it, keeps trying
// every RetryEvery until it answers. A carrier is a safety matter: nothing cancels this.
func (in *Intents) unkey(intent string) Result {
	in.endCarrier()
	in.mu.Lock()
	owed := in.keyed || in.pending
	in.mu.Unlock()
	if !owed && !in.Poller.State().Connected {
		// nothing of ours is keyed and the radio cannot be reached: owe nothing, or a
		// set_ptt 0 would fire whenever the link returns, maybe in the middle of a program's turn
		return in.refuse(intent, reasonUnreachable, fixUnreachable)
	}
	in.mu.Lock()
	in.pending = true
	in.debt++
	in.mu.Unlock()
	res := in.attemptUnkey(context.Background(), intent, false)
	if !res.OK || in.Pending() {
		// failed, or a new debt arrived while this one was on the wire
		in.startRetry(intent)
	}
	in.mu.Lock()
	after := in.afterUnkey
	in.mu.Unlock()
	if after != nil {
		after(intent)
	}
	return res
}

// attemptUnkey sends set_ptt 0 once. quiet keeps a failure out of the Info log (the retry loop).
// A success settles only the debt that stood when it was sent: a key-on that came after keeps
// its own.
func (in *Intents) attemptUnkey(ctx context.Context, intent string, quiet bool) Result {
	if st := in.Poller.State(); !st.Connected {
		if quiet {
			return Result{Reason: reasonUnreachable, Fix: fixUnreachable}
		}
		return in.refuse(intent, reasonUnreachable, fixUnreachable)
	}
	in.mu.Lock()
	debt := in.debt
	in.mu.Unlock()
	var res Result
	err := in.Poller.Write(ctx, func(c Commander) error {
		_, err := c.Do(ctx, "set_ptt", "0")
		return err
	}, func(s *State) { s.PTT = false })
	switch {
	case err == nil:
		res = Result{OK: true}
	case quiet:
		res = Result{Reason: rigFailure(err), Fix: "check the radio and try again"}
	default:
		res = in.refuse(intent, rigFailure(err), "check the radio and try again")
	}
	if res.OK {
		in.mu.Lock()
		if in.debt == debt {
			in.pending, in.keyed = false, false
			in.clearMarkLocked()
		}
		in.mu.Unlock()
	}
	return res
}

// mark writes the keyed marker before a key-on. It counts as a new debt, so an unkey already on
// the wire cannot clear the flags of the carrier that follows it, and the key-on owns the marker
// until it has its answer (keying): an unkey that starts later but reaches the radio first must
// not remove it either. On success the key-on returns with keying raised; Tune lowers it with
// the key-on's answer.
func (in *Intents) mark() error {
	in.mu.Lock()
	defer in.mu.Unlock()
	in.debt++
	if in.Marks != nil {
		if err := in.Marks.Mark(in.Radio); err != nil {
			return err
		}
	}
	in.keying++
	return nil
}

// clearMarkLocked removes the keyed marker, unless a key-on still waits for its answer: that
// key-on's own path settles the marker (kept if it keyed, removed once nothing is owed).
func (in *Intents) clearMarkLocked() {
	if in.Marks == nil || in.keying > 0 {
		return
	}
	if err := in.Marks.Clear(in.Radio); err != nil {
		in.log().Warn("could not remove the keyed marker", "radio", in.Radio, "error", err)
	}
}

func (in *Intents) startRetry(intent string) {
	in.mu.Lock()
	if in.retrying {
		in.mu.Unlock()
		return
	}
	in.retrying = true
	in.mu.Unlock()
	in.setUrgent(true)
	done := in.closedCh()
	var ctxDone <-chan struct{}
	if in.Ctx != nil {
		ctxDone = in.Ctx.Done()
	}
	tick := in.clock().At(in.clock().Now().Add(RetryEvery))
	go func() {
		// stopRetry ends the loop; it is called with in.mu held, the same hold that read pending,
		// so a debt that arrives afterwards finds retrying false and starts a loop of its own
		// (whose own urgency, re-asserted every round, wins over this loop's late "not urgent")
		stopRetry := func() { in.retrying = false; in.mu.Unlock(); in.setUrgent(false) }
		for n := 1; ; n++ {
			select {
			case <-tick:
			case <-done:
				in.mu.Lock()
				stopRetry()
				return
			case <-ctxDone:
				in.mu.Lock()
				stopRetry()
				return
			}
			if in.Gone != nil && in.Gone() {
				in.log().Info("radio is gone; giving up the owed unkey", "radio", in.Radio)
				in.mu.Lock()
				stopRetry()
				return
			}
			in.mu.Lock()
			if !in.pending {
				stopRetry()
				return
			}
			in.mu.Unlock()
			in.setUrgent(true) // again each round: the radio's poller and client may have been replaced
			res := in.attemptUnkey(context.Background(), intent, true)
			switch {
			case res.OK:
				in.log().Info("owed unkey sent", "radio", in.Radio, "attempts", n)
			case n%60 == 0:
				in.log().Info("owed unkey still failing", "radio", in.Radio, "attempts", n, "reason", res.Reason)
			default:
				in.log().Debug("owed unkey failed; retrying", "radio", in.Radio, "reason", res.Reason)
			}
			if res.OK {
				in.mu.Lock()
				if !in.pending {
					stopRetry()
					return
				}
				in.mu.Unlock() // a new debt came while this one was on the wire: go round again
			}
			tick = in.clock().At(in.clock().Now().Add(RetryEvery))
		}
	}()
}

// Unkey ends a running carrier (the owner released the radio, its socket closed, or it idled
// out). Nothing happens when no carrier is up.
func (in *Intents) Unkey() {
	in.mu.Lock()
	need := in.keyed || in.pending
	in.mu.Unlock()
	if need {
		in.unkey("release")
	}
}

// setUrgent tells the radio's link whether an unkey is owed (a capped reconnect backoff).
func (in *Intents) setUrgent(on bool) {
	if u, ok := in.Poller.(Urgent); ok {
		u.SetUrgent(on)
	}
}

// Owe owes the radio an unkey and retries it until the radio takes it: a keyed marker from
// before the service restarted says a carrier of ours may still be up.
func (in *Intents) Owe() {
	in.mu.Lock()
	in.keyed, in.pending = true, true
	in.debt++
	in.mu.Unlock()
	in.log().Warn("the service stopped while this radio may have been keyed; unkeying it", "radio", in.Radio)
	in.startRetry("restart")
}

// Owed reports whether a carrier of ours may be up: keyed and not confirmed off, or an unkey
// still owed.
func (in *Intents) Owed() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.keyed || in.pending
}

// UnkeyNow sends one set_ptt 0 within ctx when a carrier of ours may be up (the service is
// stopping: no retry loop is left behind). It reports whether nothing is owed any more.
func (in *Intents) UnkeyNow(ctx context.Context) bool {
	in.endCarrier()
	in.mu.Lock()
	if !in.keyed && !in.pending {
		in.mu.Unlock()
		return true
	}
	in.pending = true
	in.debt++
	in.mu.Unlock()
	res := in.attemptUnkey(ctx, "shutdown", true)
	if res.OK && !in.Pending() {
		in.log().Info("carrier unkeyed as the service stops", "radio", in.Radio)
		return true
	}
	in.log().Warn("could not unkey as the service stops", "radio", in.Radio, "reason", res.Reason)
	return false
}

// Pending reports whether an unkey is still owed to the radio.
func (in *Intents) Pending() bool {
	in.mu.Lock()
	defer in.mu.Unlock()
	return in.pending
}

// Stop drops PTT and ends any carrier. Any tab may send it. While the link is down it answers
// the refusal but the unkey stays owed and is retried until the radio takes it.
func (in *Intents) Stop(ctx context.Context) Result {
	return in.unkey("stop")
}

// ATU starts the radio's antenna tuner when the radio has one that CAT controls.
func (in *Intents) ATU(ctx context.Context, tab string) Result {
	_, caps, bad := in.check("atu")
	if bad != nil {
		return *bad
	}
	if bad := in.owns("atu", tab); bad != nil {
		return *bad
	}
	if caps.ModelName == "Dummy" || !slices.Contains(caps.SettableFuncs, "TUNER") {
		return in.refuse("atu", "this radio has no antenna tuner control", "tune with the radio's own tuner button")
	}
	in.Owners.Touch(in.Radio, tab)
	return in.send(ctx, "atu", nil, "set_func", "TUNER", "1")
}

func (in *Intents) lockRefusal(intent string, err error) Result {
	switch {
	case errors.Is(err, session.ErrProgramOwns):
		return in.refuse(intent, "a program holds this radio's transmitter", "stop the program, or use its own window")
	case errors.Is(err, session.ErrHeld):
		return in.refuse(intent, "another tab holds this radio's transmitter", "take over from that tab")
	case errors.Is(err, session.ErrTransmitting):
		return in.refuse(intent, "the radio is transmitting", "wait until it stops, or press stop")
	}
	return in.refuse(intent, err.Error(), "")
}

// Claim makes a tab the radio's transmitter owner.
func (in *Intents) Claim(h session.Holder) Result {
	if err := in.Owners.Claim(in.Radio, h); err != nil {
		return in.lockRefusal("claim", err)
	}
	return Result{OK: true}
}

// Release gives the transmitter back.
func (in *Intents) Release(id string) Result {
	in.Owners.Release(in.Radio, id)
	return Result{OK: true}
}

// TakeOver moves the claim to a tab unless the radio is transmitting.
func (in *Intents) TakeOver(h session.Holder) Result {
	if in.Poller.State().PTT || in.pttOn() {
		if cur, ok := in.Owners.Holder(in.Radio); ok && cur.ID != h.ID {
			return in.lockRefusal("takeover", session.ErrTransmitting)
		}
	}
	if err := in.Owners.TakeOver(in.Radio, h); err != nil {
		return in.lockRefusal("takeover", err)
	}
	return Result{OK: true}
}
