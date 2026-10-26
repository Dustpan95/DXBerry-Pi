package rig

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/session"
)

// ixRig is a Writer that records the rigctld commands it is given.
type ixRig struct {
	mu    sync.Mutex
	st    State
	calls []string
	fail  error
	// failCmd, when set, fails only that command ("set_ptt 1") with failErr
	failCmd string
	failErr error
	// afterWrite, when set, runs once a write's command has answered, before the intent sees
	// the answer (outside the lock, so it may send intents of its own)
	afterWrite func(call string)
	// beforeDo, when set, runs as a command reaches the rig and before it is recorded or
	// answered (outside the lock): a key-on that is slow to reach the radio
	beforeDo func(call string)
	// urgent records SetUrgent calls
	urgent []bool
}

func (f *ixRig) SetUrgent(on bool) { f.mu.Lock(); f.urgent = append(f.urgent, on); f.mu.Unlock() }

func (f *ixRig) urgentLog() []bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]bool{}, f.urgent...)
}

func (f *ixRig) setUp(up bool) { f.mu.Lock(); f.st.Connected = up; f.mu.Unlock() }

func (f *ixRig) State() State {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.st.clone()
}

func (f *ixRig) Write(ctx context.Context, fn func(Commander) error, opt func(*State)) error {
	f.mu.Lock()
	if opt != nil {
		opt(&f.st)
	}
	f.mu.Unlock()
	err := fn(f)
	f.mu.Lock()
	hook, last := f.afterWrite, ""
	if len(f.calls) > 0 {
		last = f.calls[len(f.calls)-1]
	}
	f.mu.Unlock()
	if hook != nil {
		hook(last)
	}
	return err
}

func (f *ixRig) Do(ctx context.Context, cmd string, args ...string) (Reply, error) {
	key := strings.TrimSpace(cmd + " " + strings.Join(args, " "))
	f.mu.Lock()
	hook := f.beforeDo
	f.mu.Unlock()
	if hook != nil {
		hook(key)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, key)
	if f.fail != nil {
		return Reply{}, f.fail
	}
	if f.failCmd != "" && f.calls[len(f.calls)-1] == f.failCmd {
		return Reply{}, f.failErr
	}
	if cmd == "set_ptt" {
		f.st.PTT = args[0] == "1"
	}
	return Reply{}, nil
}

func (f *ixRig) log() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.calls...)
}

func (f *ixRig) has(c string) bool {
	for _, x := range f.log() {
		if x == c {
			return true
		}
	}
	return false
}

func ixCaps() Caps {
	return Caps{
		ModelName:      "IC-7300",
		Modes:          []string{"USB", "LSB", "CW", "PKTUSB"},
		VFOs:           []string{"VFOA", "VFOB"},
		CanSetSplit:    true,
		CanSetVFO:      true,
		SettableLevels: []string{"RFPOWER", "AF"},
		SettableFuncs:  []string{"TUNER"},
		FreqRanges:     []FreqRange{{Lo: 30000, Hi: 74800000}},
	}
}

type ixHarness struct {
	in    *Intents
	rig   *ixRig
	clock *plClock
	own   *session.Owners
}

func newIxHarness(caps Caps) *ixHarness {
	clock := newPlClock()
	f := &ixRig{st: State{Connected: true, Freq: 146520000, Mode: "FM", VFO: "VFOA",
		Levels: map[string]float64{}, Meters: map[string]float64{}, Optimistic: map[string]bool{}}}
	own := session.NewOwners(clock)
	h := &ixHarness{rig: f, clock: clock, own: own}
	h.in = &Intents{
		Radio:  "radio1",
		Caps:   func() (Caps, bool) { return caps, true },
		Poller: f,
		Owners: own,
		Clock:  clock,
		Log:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	own.OnDrop(func(string) { h.in.Unkey() })
	return h
}

func (h *ixHarness) count(c string) int {
	n := 0
	for _, x := range h.rig.log() {
		if x == c {
			n++
		}
	}
	return n
}

// tick advances the fake clock a second at a time until cond holds.
func (h *ixHarness) tick(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s (calls %v)", what, h.rig.log())
		}
		h.clock.Advance(time.Second)
		time.Sleep(5 * time.Millisecond)
	}
}

func ixHolder(id string) session.Holder { return session.Holder{Kind: "tab", ID: id, Label: "iPad"} }

func ixRefused(t *testing.T, r Result, reasonHas, fixHas string) {
	t.Helper()
	if r.OK || !strings.Contains(r.Reason, reasonHas) || !strings.Contains(r.Fix, fixHas) {
		t.Fatalf("got %+v, want refusal with reason %q and fix %q", r, reasonHas, fixHas)
	}
}

func TestIntentFreqModeOK(t *testing.T) {
	h := newIxHarness(ixCaps())
	ctx := context.Background()
	if r := h.in.Freq(ctx, 7074000); !r.OK {
		t.Fatalf("freq: %+v", r)
	}
	if r := h.in.Mode(ctx, "CW", 500); !r.OK {
		t.Fatalf("mode: %+v", r)
	}
	if !h.rig.has("set_freq 7074000") || !h.rig.has("set_mode CW 500") {
		t.Fatalf("calls: %v", h.rig.log())
	}
	if st := h.rig.State(); st.Freq != 7074000 || st.Mode != "CW" {
		t.Fatalf("optimistic state: %+v", st)
	}
}

func TestIntentRefusesOutOfRangeFreq(t *testing.T) {
	h := newIxHarness(ixCaps())
	r := h.in.Freq(context.Background(), 99999999)
	ixRefused(t, r, "99999999 is outside this radio's range", "this radio tunes 30000–74800000 Hz")
	if len(h.rig.log()) != 0 {
		t.Fatalf("a refused intent reached the radio: %v", h.rig.log())
	}
}

func TestIntentRefusesUnknownMode(t *testing.T) {
	h := newIxHarness(ixCaps())
	r := h.in.Mode(context.Background(), "FM", 0)
	ixRefused(t, r, "this radio has no mode FM", "modes: USB LSB CW PKTUSB")
}

func TestIntentRefusesUnknownLevel(t *testing.T) {
	h := newIxHarness(ixCaps())
	ctx := context.Background()
	ixRefused(t, h.in.Level(ctx, "KEYSPD", 0.5), "KEYSPD is not a level this radio sets", "levels: RFPOWER AF")
	ixRefused(t, h.in.Level(ctx, "RFPOWER", 1.5), "1.5 is outside this radio's range", "0 to 1")
	if r := h.in.Level(ctx, "RFPOWER", 0.25); !r.OK || !h.rig.has("set_level RFPOWER 0.25") {
		t.Fatalf("level: %+v %v", r, h.rig.log())
	}
}

func TestIntentRefusesWhenDisconnected(t *testing.T) {
	h := newIxHarness(ixCaps())
	h.rig.st.Connected = false
	ctx := context.Background()
	for name, r := range map[string]Result{
		"freq":  h.in.Freq(ctx, 7074000),
		"mode":  h.in.Mode(ctx, "USB", 0),
		"vfo":   h.in.VFO(ctx, "VFOB"),
		"split": h.in.Split(ctx, true),
		"level": h.in.Level(ctx, "AF", 0.5),
		"tune":  h.in.Tune(ctx, "a", true),
		"atu":   h.in.ATU(ctx, "a"),
		"stop":  h.in.Stop(ctx),
	} {
		if r.OK || r.Reason != "rig control unreachable; the radio's rigctld is restarting" || r.Fix != "wait a few seconds" {
			t.Fatalf("%s: got %+v", name, r)
		}
	}
	if len(h.rig.log()) != 0 {
		t.Fatalf("commands sent while disconnected: %v", h.rig.log())
	}
}

func TestTuneCarrierCappedAt10s(t *testing.T) {
	h := newIxHarness(ixCaps())
	ctx := context.Background()
	_ = h.own.Claim("radio1", ixHolder("a"))
	if r := h.in.Tune(ctx, "a", true); !r.OK {
		t.Fatalf("tune: %+v", r)
	}
	if !h.rig.has("set_ptt 1") || !h.rig.State().PTT {
		t.Fatalf("no carrier: %v", h.rig.log())
	}
	h.clock.Advance(9 * time.Second)
	time.Sleep(20 * time.Millisecond)
	if h.rig.has("set_ptt 0") {
		t.Fatal("carrier dropped early")
	}
	h.clock.Advance(time.Second)
	deadline := time.Now().Add(2 * time.Second)
	for !h.rig.has("set_ptt 0") && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	if !h.rig.has("set_ptt 0") || h.rig.State().PTT {
		t.Fatalf("carrier not ended at 10 s: %v", h.rig.log())
	}
}

func TestTuneRefusedInDataMode(t *testing.T) {
	h := newIxHarness(ixCaps())
	_ = h.own.Claim("radio1", ixHolder("a"))
	for _, m := range []string{"PKTUSB", "PKTLSB", "PKTFM", "PKTAM", "RTTY", "RTTYR", "FM-D", "AM-D"} {
		h.rig.st.Mode = m
		r := h.in.Tune(context.Background(), "a", true)
		if r.OK || r.Reason != "a data mode would key the sound card with silence" || r.Fix != "switch to FM or AM to tune" {
			t.Fatalf("%s: got %+v", m, r)
		}
	}
	if h.rig.has("set_ptt 1") {
		t.Fatal("keyed in a data mode")
	}
}

// Tune is PTT with no audio: only AM and FM put a carrier on the air that way.
func TestTuneRefusedInModesWithoutACarrier(t *testing.T) {
	h := newIxHarness(ixCaps())
	_ = h.own.Claim("radio1", ixHolder("a"))
	for _, m := range []string{"USB", "LSB", "CW", "CWR"} {
		h.rig.st.Mode = m
		r := h.in.Tune(context.Background(), "a", true)
		if r.OK || r.Reason != m+" sends no carrier from PTT alone" || r.Fix != "switch to FM or AM to tune" {
			t.Fatalf("%s: got %+v", m, r)
		}
	}
	if h.rig.has("set_ptt 1") {
		t.Fatal("keyed in a mode with no carrier")
	}
}

func TestTuneKeysInCarrierModes(t *testing.T) {
	for _, m := range []string{"AM", "AMS", "FM", "FMN", "WFM"} {
		h := newIxHarness(ixCaps())
		_ = h.own.Claim("radio1", ixHolder("a"))
		h.rig.st.Mode = m
		if r := h.in.Tune(context.Background(), "a", true); !r.OK || !h.rig.has("set_ptt 1") {
			t.Fatalf("%s: got %+v %v", m, r, h.rig.log())
		}
		h.in.Stop(context.Background())
	}
}

func TestTuneRequiresOwner(t *testing.T) {
	h := newIxHarness(ixCaps())
	ctx := context.Background()
	ixRefused(t, h.in.Tune(ctx, "a", true), "does not hold", "")
	_ = h.own.Claim("radio1", ixHolder("b"))
	ixRefused(t, h.in.Tune(ctx, "a", true), "does not hold", "")
	ixRefused(t, h.in.ATU(ctx, "a"), "does not hold", "")
	if len(h.rig.log()) != 0 {
		t.Fatalf("commands sent: %v", h.rig.log())
	}
	_ = h.own.Claim("radio1", ixHolder("b"))
	if r := h.in.ATU(ctx, "b"); !r.OK || !h.rig.has("set_func TUNER 1") {
		t.Fatalf("atu: %+v %v", r, h.rig.log())
	}
}

func TestATUHiddenWithoutTunerOrOnDummy(t *testing.T) {
	c := ixCaps()
	c.SettableFuncs = nil
	h := newIxHarness(c)
	_ = h.own.Claim("radio1", ixHolder("a"))
	ixRefused(t, h.in.ATU(context.Background(), "a"), "no antenna tuner", "")
	c = ixCaps()
	c.ModelName = "Dummy"
	h = newIxHarness(c)
	_ = h.own.Claim("radio1", ixHolder("a"))
	ixRefused(t, h.in.ATU(context.Background(), "a"), "no antenna tuner", "")
}

func TestStopFromAnyTab(t *testing.T) {
	h := newIxHarness(ixCaps())
	ctx := context.Background()
	_ = h.own.Claim("radio1", ixHolder("a"))
	h.in.Tune(ctx, "a", true)
	// tab "b" never claimed anything
	if r := h.in.Stop(ctx); !r.OK {
		t.Fatalf("stop: %+v", r)
	}
	if !h.rig.has("set_ptt 0") || h.rig.State().PTT {
		t.Fatalf("not stopped: %v", h.rig.log())
	}
	n := len(h.rig.log())
	h.clock.Advance(11 * time.Second) // the cancelled timer must not fire a second set_ptt 0
	time.Sleep(30 * time.Millisecond)
	if len(h.rig.log()) != n {
		t.Fatalf("carrier timer still ran: %v", h.rig.log())
	}
}

func TestClaimIntents(t *testing.T) {
	h := newIxHarness(ixCaps())
	if r := h.in.Claim(ixHolder("a")); !r.OK {
		t.Fatalf("claim: %+v", r)
	}
	ixRefused(t, h.in.Claim(ixHolder("b")), "another tab", "take over")
	h.rig.st.PTT = true
	ixRefused(t, h.in.TakeOver(ixHolder("b")), "transmitting", "")
	h.rig.st.PTT = false
	if r := h.in.TakeOver(ixHolder("b")); !r.OK {
		t.Fatalf("take over: %+v", r)
	}
	if r := h.in.Release("b"); !r.OK {
		t.Fatalf("release: %+v", r)
	}
	if _, ok := h.own.Holder("radio1"); ok {
		t.Fatal("still held")
	}
}

func TestStopWhileLinkDownIsRetriedUntilTheRadioAnswers(t *testing.T) {
	h := newIxHarness(ixCaps())
	ctx := context.Background()
	_ = h.own.Claim("radio1", ixHolder("a"))
	h.in.Tune(ctx, "a", true)
	h.rig.setUp(false)
	r := h.in.Stop(ctx)
	if r.OK || r.Reason != "rig control unreachable; the radio's rigctld is restarting" {
		t.Fatalf("stop while down: %+v", r)
	}
	if !h.in.Pending() {
		t.Fatal("the owed unkey was forgotten")
	}
	h.clock.Advance(3 * time.Second)
	time.Sleep(20 * time.Millisecond)
	if h.count("set_ptt 0") != 0 {
		t.Fatal("sent while down")
	}
	h.rig.setUp(true)
	h.tick(t, "set_ptt 0 after the link returns", func() bool { return h.count("set_ptt 0") > 0 })
	h.tick(t, "pending cleared", func() bool { return !h.in.Pending() })
	if h.rig.State().PTT {
		t.Fatal("still keyed")
	}
}

func TestCarrierCapFiresWhileLinkDown(t *testing.T) {
	h := newIxHarness(ixCaps())
	_ = h.own.Claim("radio1", ixHolder("a"))
	h.in.Tune(context.Background(), "a", true)
	h.rig.setUp(false)
	h.clock.Advance(10 * time.Second)
	h.tick(t, "cap owes an unkey", func() bool { return h.in.Pending() })
	if h.count("set_ptt 0") != 0 {
		t.Fatal("sent while down")
	}
	h.rig.setUp(true)
	h.tick(t, "set_ptt 0 after the link returns", func() bool { return h.count("set_ptt 0") > 0 })
	h.tick(t, "pending cleared", func() bool { return !h.in.Pending() })
}

func TestReleaseAndSocketCloseEndTheCarrier(t *testing.T) {
	h := newIxHarness(ixCaps())
	ctx := context.Background()
	_ = h.own.Claim("radio1", ixHolder("a"))
	h.in.Tune(ctx, "a", true)
	h.in.Release("a")
	if h.count("set_ptt 0") != 1 || h.rig.State().PTT {
		t.Fatalf("release left the carrier: %v", h.rig.log())
	}
	_ = h.own.Claim("radio1", ixHolder("a"))
	h.in.Tune(ctx, "a", true)
	h.own.SocketOpened("a")
	h.own.SocketClosed("a")
	if h.count("set_ptt 0") != 2 {
		t.Fatalf("socket close left the carrier: %v", h.rig.log())
	}
}

func TestIdleExpiryEndsTheCarrier(t *testing.T) {
	h := newIxHarness(ixCaps())
	_ = h.own.Claim("radio1", ixHolder("a"))
	h.in.Tune(context.Background(), "a", true)
	h.rig.setUp(false) // the carrier is still owed after the expiry
	h.clock.Advance(11 * time.Minute)
	if _, ok := h.own.Holder("radio1"); ok {
		t.Fatal("still held")
	}
	if !h.in.Pending() {
		t.Fatal("expiry did not owe an unkey")
	}
}

func TestIntentAnswersWhenTheWriteFails(t *testing.T) {
	h := newIxHarness(ixCaps())
	h.rig.fail = io.ErrUnexpectedEOF
	r := h.in.Freq(context.Background(), 7074000)
	if r.OK || r.Reason == "" || r.Fix == "" {
		t.Fatalf("got %+v", r)
	}
}

func TestTuneKeyOnErrorOwesAnUnkey(t *testing.T) {
	h := newIxHarness(ixCaps())
	_ = h.own.Claim("radio1", ixHolder("a"))
	h.rig.failCmd, h.rig.failErr = "set_ptt 1", ErrDeadline
	r := h.in.Tune(context.Background(), "a", true)
	if r.OK || r.Reason == "" {
		t.Fatalf("got %+v", r)
	}
	if !h.in.Pending() {
		t.Fatal("a key-on that may have reached the radio owes an unkey")
	}
	h.rig.mu.Lock()
	h.rig.failCmd = ""
	h.rig.mu.Unlock()
	h.tick(t, "set_ptt 0", func() bool { return h.count("set_ptt 0") > 0 })
	h.tick(t, "pending cleared", func() bool { return !h.in.Pending() })
}

func TestTuneKeyOnRigErrorOwesNothing(t *testing.T) {
	h := newIxHarness(ixCaps())
	_ = h.own.Claim("radio1", ixHolder("a"))
	h.rig.failCmd, h.rig.failErr = "set_ptt 1", &RigError{Code: -1}
	if r := h.in.Tune(context.Background(), "a", true); r.OK || h.in.Pending() {
		t.Fatalf("got %+v pending %v", r, h.in.Pending())
	}
}

func (h *ixHarness) retrying() bool {
	h.in.mu.Lock()
	defer h.in.mu.Unlock()
	return h.in.retrying
}

func TestRetryLoopEndsOnCloseContextAndGone(t *testing.T) {
	for name, stop := range map[string]func(h *ixHarness, cancel context.CancelFunc){
		"close":   func(h *ixHarness, _ context.CancelFunc) { h.in.Close() },
		"context": func(h *ixHarness, cancel context.CancelFunc) { cancel() },
		"gone":    nil, // set below: it needs the case's own flag
	} {
		h := newIxHarness(ixCaps())
		ctx, cancel := context.WithCancel(context.Background())
		h.in.Ctx = ctx
		var gone atomic.Bool
		h.in.Gone = gone.Load
		if name == "gone" {
			stop = func(*ixHarness, context.CancelFunc) { gone.Store(true) }
		}
		_ = h.own.Claim("radio1", ixHolder("a"))
		h.in.Tune(context.Background(), "a", true)
		h.rig.setUp(false)
		h.in.Stop(context.Background())
		if !h.retrying() {
			t.Fatalf("%s: no retry loop", name)
		}
		stop(h, cancel)
		deadline := time.Now().Add(3 * time.Second)
		for h.retrying() {
			if time.Now().After(deadline) {
				t.Fatalf("%s: retry loop still running", name)
			}
			h.clock.Advance(time.Second)
			time.Sleep(5 * time.Millisecond)
		}
		cancel()
	}
}

func TestStopOwesNothingWhenNothingIsKeyed(t *testing.T) {
	h := newIxHarness(ixCaps())
	h.rig.setUp(false)
	r := h.in.Stop(context.Background())
	if r.OK || r.Reason != "rig control unreachable; the radio's rigctld is restarting" {
		t.Fatalf("got %+v", r)
	}
	if h.in.Pending() {
		t.Fatal("owed an unkey for a carrier that was never keyed")
	}
	h.rig.setUp(true)
	h.clock.Advance(5 * time.Second)
	time.Sleep(30 * time.Millisecond)
	if h.count("set_ptt 0") != 0 {
		t.Fatalf("a stray unkey was sent: %v", h.rig.log())
	}
}

// A key-on that fails (its fate unknown) while an unkey is on the wire owes a new unkey; the
// unkey's success must not erase it, and a retry loop must be left to send it.
func TestNewDebtDuringAnUnkeyIsNotLost(t *testing.T) {
	for _, path := range []string{"stop", "retry"} {
		h := newIxHarness(ixCaps())
		ctx := context.Background()
		_ = h.own.Claim("radio1", ixHolder("a"))
		if r := h.in.Tune(ctx, "a", true); !r.OK {
			t.Fatalf("%s: tune: %+v", path, r)
		}
		armed := atomic.Bool{}
		armed.Store(true)
		h.rig.mu.Lock()
		h.rig.afterWrite = func(call string) {
			if call != "set_ptt 0" || !armed.Swap(false) {
				return
			}
			h.rig.mu.Lock()
			h.rig.failCmd, h.rig.failErr = "set_ptt 1", ErrDeadline
			h.rig.mu.Unlock()
			h.in.Tune(ctx, "a", true) // its key-on times out: a new unkey is owed
			h.rig.mu.Lock()
			h.rig.failCmd = ""
			h.rig.mu.Unlock()
		}
		h.rig.mu.Unlock()
		if path == "retry" {
			h.rig.setUp(false)
			h.in.Stop(ctx) // owed, a retry loop runs
			h.rig.setUp(true)
		} else {
			h.in.Stop(ctx)
		}
		h.tick(t, path+": the second debt is sent", func() bool { return h.count("set_ptt 0") >= 2 })
		h.tick(t, path+": nothing owed", func() bool { return !h.in.Pending() && !h.retrying() })
	}
}

func markerAt(t *testing.T, dir string) bool {
	t.Helper()
	fi, err := os.Stat(filepath.Join(dir, "radio1"))
	if err != nil {
		return false
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("marker mode %v", fi.Mode().Perm())
	}
	return true
}

// The keyed marker is on disk from before set_ptt 1 until a set_ptt 0 is confirmed, so a crash
// in between leaves it for the next start.
func TestTuneMarksTheCarrierUntilConfirmedOff(t *testing.T) {
	h := newIxHarness(ixCaps())
	dir := filepath.Join(t.TempDir(), "keyed")
	h.in.Marks = &KeyedMarks{Dir: dir}
	ctx := context.Background()
	_ = h.own.Claim("radio1", ixHolder("a"))
	var markedAtKeyOn bool
	h.rig.mu.Lock()
	h.rig.afterWrite = func(call string) {
		if call == "set_ptt 1" {
			markedAtKeyOn = markerAt(t, dir)
		}
	}
	h.rig.mu.Unlock()
	if r := h.in.Tune(ctx, "a", true); !r.OK {
		t.Fatalf("tune: %+v", r)
	}
	if !markedAtKeyOn || !markerAt(t, dir) {
		t.Fatal("no marker while keyed")
	}
	if fi, err := os.Stat(dir); err != nil || fi.Mode().Perm() != 0o700 {
		t.Fatalf("marker dir: %v %v", fi, err)
	}
	h.in.Stop(ctx)
	if markerAt(t, dir) {
		t.Fatal("marker kept after a confirmed unkey")
	}
	// a stop while the link is down keeps the marker until the retry is confirmed
	h.in.Tune(ctx, "a", true)
	h.rig.setUp(false)
	h.in.Stop(ctx)
	if !markerAt(t, dir) {
		t.Fatal("marker dropped while the unkey is still owed")
	}
	h.rig.setUp(true)
	h.tick(t, "marker removed after the owed unkey", func() bool { return !h.in.Pending() && !markerAt(t, dir) })
	// a key-on the radio refused keys nothing and leaves no marker
	h.rig.mu.Lock()
	h.rig.failCmd, h.rig.failErr = "set_ptt 1", &RigError{Code: -1}
	h.rig.mu.Unlock()
	h.in.Tune(ctx, "a", true)
	if markerAt(t, dir) {
		t.Fatal("marker left by a refused key-on")
	}
}

func TestTuneRefusedWhenTheMarkerCannotBeWritten(t *testing.T) {
	h := newIxHarness(ixCaps())
	file := filepath.Join(t.TempDir(), "keyed")
	if err := os.WriteFile(file, nil, 0o600); err != nil { // a file where the directory should be
		t.Fatal(err)
	}
	h.in.Marks = &KeyedMarks{Dir: file}
	_ = h.own.Claim("radio1", ixHolder("a"))
	r := h.in.Tune(context.Background(), "a", true)
	ixRefused(t, r, "the station could not note the carrier", "check the station's storage")
	if h.rig.has("set_ptt 1") {
		t.Fatal("keyed without a marker")
	}
}

func TestOweUnkeysAMarkedRadio(t *testing.T) {
	h := newIxHarness(ixCaps())
	dir := t.TempDir()
	marks := &KeyedMarks{Dir: dir}
	if err := marks.Mark("radio1"); err != nil {
		t.Fatal(err)
	}
	if got := marks.List(); len(got) != 1 || got[0] != "radio1" {
		t.Fatalf("list %v", got)
	}
	h.in.Marks = marks
	h.rig.setUp(false)
	h.in.Owe()
	h.clock.Advance(3 * time.Second)
	time.Sleep(20 * time.Millisecond)
	if h.count("set_ptt 0") != 0 || !markerAt(t, dir) {
		t.Fatal("sent while down, or the marker went early")
	}
	h.rig.setUp(true)
	h.tick(t, "set_ptt 0 once connected", func() bool { return h.count("set_ptt 0") == 1 })
	h.tick(t, "marker removed", func() bool { return !markerAt(t, dir) && !h.in.Pending() })
	if len(marks.List()) != 0 {
		t.Fatalf("list %v", marks.List())
	}
}

// A Stop whose set_ptt 0 reaches the radio after Tune wrote its marker but before Tune's own
// set_ptt 1 must not remove that marker: the carrier that follows still needs it.
func TestStopRacingAKeyOnKeepsItsMarker(t *testing.T) {
	h := newIxHarness(ixCaps())
	dir := t.TempDir()
	h.in.Marks = &KeyedMarks{Dir: dir}
	ctx := context.Background()
	_ = h.own.Claim("radio1", ixHolder("a"))
	var once sync.Once
	h.rig.mu.Lock()
	h.rig.beforeDo = func(call string) {
		if call == "set_ptt 1" {
			once.Do(func() { h.in.Stop(ctx) }) // lands while the key-on is on its way
		}
	}
	h.rig.mu.Unlock()
	if r := h.in.Tune(ctx, "a", true); !r.OK {
		t.Fatalf("tune: %+v", r)
	}
	if got := h.rig.log(); len(got) != 2 || got[0] != "set_ptt 0" || got[1] != "set_ptt 1" {
		t.Fatalf("calls %v, want the stop's unkey before the key-on", got)
	}
	if !markerAt(t, dir) {
		t.Fatal("the carrier is up with no marker")
	}
	h.in.Stop(ctx)
	if markerAt(t, dir) {
		t.Fatal("marker kept after the next confirmed unkey")
	}
	// the same race with a key-on the radio refuses: nothing is keyed, the marker goes with it
	once = sync.Once{}
	h.rig.mu.Lock()
	h.rig.failCmd, h.rig.failErr = "set_ptt 1", &RigError{Code: -1}
	h.rig.mu.Unlock()
	h.in.Tune(ctx, "a", true)
	if markerAt(t, dir) || h.in.Owed() {
		t.Fatalf("refused key-on left marker %v owed %v", markerAt(t, dir), h.in.Owed())
	}
}

// An owed unkey makes the radio's link urgent (a capped reconnect backoff) until it is paid.
func TestOwedUnkeyMakesTheLinkUrgent(t *testing.T) {
	h := newIxHarness(ixCaps())
	ctx := context.Background()
	_ = h.own.Claim("radio1", ixHolder("a"))
	h.in.Tune(ctx, "a", true)
	h.in.Stop(ctx) // paid at once: no urgency
	if u := h.rig.urgentLog(); len(u) != 0 {
		t.Fatalf("urgent with nothing owed: %v", u)
	}
	h.in.Tune(ctx, "a", true)
	h.rig.setUp(false)
	h.in.Stop(ctx)
	h.tick(t, "urgent while owed", func() bool { u := h.rig.urgentLog(); return len(u) > 0 && u[len(u)-1] })
	h.rig.setUp(true)
	h.tick(t, "paid", func() bool { return !h.in.Pending() && !h.retrying() })
	if u := h.rig.urgentLog(); u[len(u)-1] {
		t.Fatalf("still urgent after the unkey: %v", u)
	}
}

// The poller hands urgency to its rigctld client.
func TestPollerWriterPassesUrgencyToTheClient(t *testing.T) {
	c := dial("127.0.0.1:1", nil, options{hold: true})
	defer c.Close()
	w := PollerWriter(NewPoller(c, Caps{}, nil, nil))
	u, ok := w.(Urgent)
	if !ok {
		t.Fatal("a poller writer cannot be made urgent")
	}
	u.SetUrgent(true)
	c.mu.Lock()
	on := c.urgent
	c.mu.Unlock()
	if !on {
		t.Fatal("the client did not become urgent")
	}
}
