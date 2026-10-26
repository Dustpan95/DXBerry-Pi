package rig

import (
	"context"
	"fmt"
	"log/slog"
	"math"
	"sync"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/settings"
)

// PTT is press-and-hold transmit from a browser tab (design §9.3). It keys the radio through the
// same carrier machinery as Tune (Intents.keyOn: the keyed marker, set_ptt 1, the owed-unkey
// retry loop), so it is never a second state machine for the radio's PTT line. What is PTT's own
// is the audio side of the window: the TX loopback, the bridge's TX switch for the tab, the
// keying tone, and the window's timers (heartbeat and timeout).

// HeartbeatLost is how long a PTT window lasts without a heartbeat from its tab.
const HeartbeatLost = 7 * time.Second

// AudioFix is the fix named when a radio has no sound server (the signalling socket's words).
const AudioFix = "start the radio's sound server or stop the program that holds the sound card"

// The keying tone of digirig_tone: a sine on the right channel of <CARD>.tx for the PTT window
// (the DigiRig Lite keys on audio in its right channel; the plumbing spec names the channel, not
// a frequency or level, so these are this station's choice).
const (
	ToneHz    = 1000
	ToneLevel = 0.5 // of full scale (-6 dBFS)
)

const (
	audioWithin = 5 * time.Second // a pactl call made for PTT
	toneFrame   = 960             // stereo pairs per 20 ms frame at 48 kHz
	toneRate    = 48000
	toneAhead   = 3 // frames written before the pacing starts
)

const (
	reasonNotHolder = "you do not hold the transmitter"
	reasonMicClosed = "the microphone is not open yet"
	fixMicClosed    = "press PTT again once the browser has asked for the microphone"
	reasonStopping  = "the station is stopping"
)

// Notifier sends a transient notice to every tab (status.Status.Notify).
type Notifier func(radio string, msg any)

// TXBridge is the radio's WebRTC bridge as PTT uses it (*audio.Bridge).
type TXBridge interface {
	SetTXAllowed(tab string, on bool)
	// HasInbound reports whether the tab's connection carries its microphone yet.
	HasInbound(tab string) bool
	OnLost(fn func(tab, reason string)) (cancel func())
}

// SoundServer is the radios' sound server control as PTT uses it (*audio.ServerControl).
type SoundServer interface {
	Available(radio string) (bool, string)
	LoadTXLoopback(ctx context.Context, radio string) (int, error)
	UnloadTXLoopback(ctx context.Context, radio string, moduleID int) error
}

// TonePlayer plays stereo s16 frames into <CARD>.tx (*audio.Playback with two channels).
type TonePlayer interface {
	Start(ctx context.Context) error
	Write(frame []int16) error
	Close() error
}

// PTTNotice is sent to every tab when PTT drops.
type PTTNotice struct {
	Type   string `json:"type"`
	Radio  string `json:"radio"`
	On     bool   `json:"on"`
	Reason string `json:"reason"`
}

type pttPhase int

const (
	pttOff pttPhase = iota
	pttStarting
	pttOn
)

// window is what one PTT window opened, for its teardown.
type window struct {
	tab    string
	method string
	module int
	tone   *toneRun
}

// PTT is one radio's push-to-talk.
type PTT struct {
	in       *Intents
	bridge   TXBridge
	server   SoundServer
	settings func() settings.RadioSettings
	notify   Notifier
	clock    Clock
	log      *slog.Logger
	stopLost func()

	// Tone makes the player of the keying tone (digirig_tone); nil refuses PTT on such a radio.
	Tone func() TonePlayer

	mu       sync.Mutex
	method   string
	phase    pttPhase
	gen      uint64 // bumped whenever a window opens or ends
	ended    bool   // the window still starting was ended meanwhile; its On undoes it
	cancel   string // why
	w        window
	lastHB   time.Time
	timeout  time.Duration
	done     chan struct{} // closed when the open window ends (stops its timer)
	shut     bool
	detached bool
}

// NewPTT makes radio in.Radio's PTT. bridge and server are its audio side (nil: PTT is refused,
// the station has no browser audio); settings gives the radio's PTT timeout; method is the
// radio record's ptt.method; notify tells every tab when PTT drops.
func NewPTT(in *Intents, bridge TXBridge, server SoundServer, settings func() settings.RadioSettings, method string,
	notify Notifier, clock Clock, log *slog.Logger) *PTT {
	if clock == nil {
		clock = in.clock()
	}
	if log == nil {
		log = in.log()
	}
	p := &PTT{in: in, bridge: bridge, server: server, settings: settings, method: method, notify: notify, clock: clock, log: log}
	in.mu.Lock()
	in.pttActive = p.Active
	in.afterUnkey = p.carrierDropped
	in.mu.Unlock()
	if bridge != nil {
		p.stopLost = bridge.OnLost(func(tab, reason string) {
			p.mu.Lock()
			ours := p.phase == pttOn && p.w.tab == tab
			p.mu.Unlock()
			if ours {
				p.log.Info("PTT dropped: the browser's audio was lost", "radio", in.Radio, "why", reason)
				p.drop("audio lost", true)
			}
		})
	}
	return p
}

// SetMethod follows a change of the radio record's ptt.method; a window already open keeps the
// method it was opened with.
func (p *PTT) SetMethod(m string) {
	p.mu.Lock()
	p.method = m
	p.mu.Unlock()
}

// Active reports whether a PTT window is open (or opening).
func (p *PTT) Active() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.phase != pttOff
}

// State is the window as the page sees it: on, which tab holds it, its timeout and method.
func (p *PTT) State() (on bool, tab string, timeout time.Duration, method string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.phase == pttOn, p.w.tab, p.configuredTimeout(), p.method
}

// keysRig: the methods whose PTT goes through the radio's rigctld (cm108 and gpio too: rigctld
// carries their PTT arguments).
func keysRig(method string) bool { return method == "rigctld" || method == "cm108" || method == "gpio" }

func audioOnly(method string) bool {
	return method == "vox" || method == "none" || method == "digirig_tone"
}

// configuredTimeout is the radio's PTT timeout from its settings; p.mu held or not, it reads only
// the settings.
func (p *PTT) configuredTimeout() time.Duration {
	s := settings.DefaultPTTTimeoutS
	if p.settings != nil {
		if v := p.settings().PTTTimeoutS; v >= settings.MinPTT && v <= settings.MaxPTT {
			s = v
		}
	}
	return time.Duration(s) * time.Second
}

// minSec is d as M:SS.
func minSec(d time.Duration) string {
	s := int(d.Round(time.Second) / time.Second)
	return fmt.Sprintf("%d:%02d", s/60, s%60)
}

func (p *PTT) refuse(reason, fix string) Result { return p.in.refuse("ptt", reason, fix) }

// On opens a PTT window for tab: the owner only; the radio keyed the way its method says, the TX
// loopback loaded, the tab's microphone let through, the heartbeat and timeout timers running.
// The same tab's "on" while its window is open is a heartbeat.
func (p *PTT) On(ctx context.Context, tab string) Result {
	if bad := p.in.owns("ptt", tab); bad != nil {
		return *bad
	}
	if bad := p.in.guardRefusal("ptt"); bad != nil {
		return *bad
	}
	p.mu.Lock()
	switch {
	case p.shut:
		p.mu.Unlock()
		return p.refuse(reasonStopping, "wait for the station to start again")
	case p.phase == pttOn && p.w.tab == tab:
		p.lastHB = p.clock.Now()
		p.mu.Unlock()
		p.in.Owners.Touch(p.in.Radio, tab)
		return Result{OK: true}
	case p.phase == pttOn:
		p.mu.Unlock()
		return p.refuse("PTT is already on", "wait until it drops")
	case p.phase != pttOff:
		p.mu.Unlock()
		return p.refuse("PTT is already starting", "wait a moment")
	}
	if p.in.carrierUp() {
		p.mu.Unlock()
		return p.refuse("a tune carrier is up", "wait for it to end, or press STOP")
	}
	method := p.method
	p.phase, p.ended, p.cancel = pttStarting, false, ""
	p.gen++
	g := p.gen
	p.w = window{tab: tab, method: method}
	p.mu.Unlock()

	// abort ends a window that never opened (nothing of the audio side is up yet). Until then the
	// window stays "starting", even when a drop ended it meanwhile, so no second press can open
	// another window over this one's half-built audio side.
	abort := func(res Result) Result {
		p.mu.Lock()
		if p.gen == g && p.phase == pttStarting {
			p.phase, p.w = pttOff, window{}
			p.gen++
		}
		p.mu.Unlock()
		return res
	}
	switch {
	case !keysRig(method) && !audioOnly(method):
		return abort(p.refuse(fmt.Sprintf("this radio's PTT method %q is not one the station knows", method), "set the radio's PTT method in its settings"))
	case p.bridge == nil || p.server == nil:
		return abort(p.refuse("this station has no browser audio", "update the station"))
	case keysRig(method) && !p.in.Poller.State().Connected:
		return abort(p.refuse(reasonUnreachable, fixUnreachable))
	case method == "digirig_tone" && p.Tone == nil:
		return abort(p.refuse("this station cannot play the keying tone", "set the radio's PTT method in its settings"))
	}
	if ok, why := p.server.Available(p.in.Radio); !ok {
		return abort(p.refuse(why, AudioFix))
	}
	if !p.bridge.HasInbound(tab) {
		return abort(p.refuse(reasonMicClosed, fixMicClosed))
	}
	p.in.Owners.Touch(p.in.Radio, tab)
	if keysRig(method) {
		if res := p.in.keyOn(ctx, "ptt", nil); !res.OK {
			return abort(res)
		}
	}
	// the audio side: pactl is not tied to the request (a cancelled request must not leave a
	// half-loaded module behind)
	actx, cancel := context.WithTimeout(context.WithoutCancel(ctx), audioWithin)
	module, err := p.server.LoadTXLoopback(actx, p.in.Radio)
	cancel()
	if err != nil {
		if keysRig(method) {
			p.in.unkey("ptt")
		}
		return abort(p.refuse("the transmit audio path could not be opened: "+err.Error(), AudioFix))
	}
	w := window{tab: tab, method: method, module: module}
	if method == "digirig_tone" {
		t, err := startTone(p.Tone(), func() {
			p.log.Warn("the keying tone stopped; dropping PTT", "radio", p.in.Radio)
			p.dropGen(g, "the keying tone stopped")
		}, p.log)
		if err != nil {
			p.closeAudio(w)
			return abort(p.refuse("the keying tone could not be played: "+err.Error(), AudioFix))
		}
		w.tone = t
	}
	p.bridge.SetTXAllowed(tab, true)

	p.mu.Lock()
	if p.ended {
		// ended while it was opening (STOP, the owner's socket, shutdown): undo it all
		reason := p.cancel
		p.phase, p.w = pttOff, window{}
		p.gen++
		p.mu.Unlock()
		p.release(w, keysRig(method), reason)
		return p.refuse("PTT was stopped: "+reason, "press PTT again")
	}
	p.phase, p.w = pttOn, w
	p.lastHB = p.clock.Now()
	p.timeout = p.configuredTimeout()
	done := make(chan struct{})
	p.done = done
	since, limit := p.lastHB, p.timeout
	p.mu.Unlock()
	p.in.refreshTx()
	go p.watch(g, done, since, limit)
	p.log.Info("PTT on", "radio", p.in.Radio, "method", method, "timeout", minSec(limit))
	return Result{OK: true}
}

// watch ends window g when its heartbeat lapses or its time is up.
func (p *PTT) watch(g uint64, done <-chan struct{}, since time.Time, limit time.Duration) {
	timeUp := p.clock.At(since.Add(limit))
	for {
		p.mu.Lock()
		if p.gen != g {
			p.mu.Unlock()
			return
		}
		hb := p.lastHB
		p.mu.Unlock()
		select {
		case <-done:
			return
		case <-timeUp:
			p.log.Info("PTT timed out", "radio", p.in.Radio, "after", minSec(limit))
			p.dropGen(g, "PTT timed out at "+minSec(limit))
			return
		case <-p.clock.At(hb.Add(HeartbeatLost)):
			p.mu.Lock()
			lapsed := p.gen == g && !p.clock.Now().Before(p.lastHB.Add(HeartbeatLost))
			p.mu.Unlock()
			if lapsed {
				p.log.Info("PTT dropped: no heartbeat", "radio", p.in.Radio)
				p.dropGen(g, "heartbeat lost")
				return
			}
		}
	}
}

// Heartbeat keeps the owner's window open. Anyone else's is ignored.
func (p *PTT) Heartbeat(tab string) Result {
	if bad := p.in.owns("ptt", tab); bad != nil {
		return Result{Reason: reasonNotHolder}
	}
	p.mu.Lock()
	if p.phase != pttOn || p.w.tab != tab {
		p.mu.Unlock()
		return Result{Reason: "PTT is not on", Fix: "press PTT again"}
	}
	p.lastHB = p.clock.Now()
	p.mu.Unlock()
	p.in.Owners.Touch(p.in.Radio, tab)
	return Result{OK: true}
}

// Off ends the window tab opened, for reason (the page's release says "released"). It is
// idempotent. An Off from a tab that neither opened the window nor holds the transmitter is
// ignored: a reconnecting old tab's late release must not unkey the new owner (the heartbeat
// rule ends the old owner's window). While the rig link is down the unkey stays owed and is
// retried every second; the audio side closes at once either way.
func (p *PTT) Off(tab, reason string) Result {
	p.mu.Lock()
	ours := p.phase != pttOff && p.w.tab == tab
	p.mu.Unlock()
	if !ours {
		if h, ok := p.in.Owners.Holder(p.in.Radio); ok && h.Kind == "tab" && h.ID == tab {
			return Result{OK: true} // the owner, with nothing on
		}
		p.log.Info("PTT off from a tab that does not hold the transmitter; ignored", "radio", p.in.Radio)
		return Result{Reason: reasonNotHolder}
	}
	_, res := p.drop(reason, true)
	return res
}

// Drop ends whatever window is open, for reason, from the station's side (the SWR guard).
func (p *PTT) Drop(reason string) Result {
	_, res := p.drop(reason, true)
	return res
}

// Stop is STOP from any tab: the window ends with "STOP from <device>" and the radio gets its
// set_ptt 0 whatever keyed it.
func (p *PTT) Stop(ctx context.Context, device string) Result {
	p.mu.Lock()
	method := p.w.method
	p.mu.Unlock()
	if was, res := p.drop("STOP from "+device, true); was && keysRig(method) {
		return res // its unkey was the STOP's set_ptt 0
	}
	return p.in.Stop(ctx)
}

// OwnerGone is told when a tab's claim on the radio ends (its socket closed, or it released):
// a window whose tab no longer holds the radio ends.
func (p *PTT) OwnerGone() {
	p.mu.Lock()
	tab, open := p.w.tab, p.phase != pttOff
	p.mu.Unlock()
	if !open {
		return
	}
	if h, ok := p.in.Owners.Holder(p.in.Radio); ok && h.Kind == "tab" && h.ID == tab {
		return
	}
	p.drop("owner disconnected", true)
}

// carrierDropped is told after the carrier machinery sent an unkey that was not PTT's own (STOP,
// Tune off, a release): the radio's PTT line is down, so the window ends too.
func (p *PTT) carrierDropped(intent string) {
	if intent == "ptt" {
		return
	}
	reason := "the transmitter was unkeyed"
	if intent == "stop" {
		reason = "STOP"
	}
	p.drop(reason, false)
}

// Shutdown ends the window as the station stops and refuses new ones.
func (p *PTT) Shutdown() {
	p.mu.Lock()
	p.shut = true
	p.mu.Unlock()
	p.drop(reasonStopping, true)
}

// Close detaches the PTT from its radio (the radio left the records): any window ends.
func (p *PTT) Close() {
	p.mu.Lock()
	if p.detached {
		p.mu.Unlock()
		return
	}
	p.detached, p.shut = true, true
	p.mu.Unlock()
	p.drop("the radio was removed", true)
	if p.stopLost != nil {
		p.stopLost()
	}
	p.in.mu.Lock()
	p.in.pttActive, p.in.afterUnkey = nil, nil
	p.in.mu.Unlock()
}

// drop ends the open window: unkey (when it keyed through rigctld and unkey is asked), close the
// audio side, tell every tab. A window still opening is marked ended; On undoes it. It reports
// whether an open window was ended, with the unkey's answer.
func (p *PTT) drop(reason string, unkey bool) (bool, Result) {
	return p.dropAt(0, reason, unkey)
}

// dropGen is drop for window g only (a timer of a window that has already ended does nothing).
func (p *PTT) dropGen(g uint64, reason string) { p.dropAt(g, reason, true) }

// dropAt is drop; g is the window it is for, 0 for whichever is open.
func (p *PTT) dropAt(g uint64, reason string, unkey bool) (bool, Result) {
	p.mu.Lock()
	if g != 0 && p.gen != g {
		p.mu.Unlock()
		return false, Result{OK: true}
	}
	switch p.phase {
	case pttOff:
		p.mu.Unlock()
		return false, Result{OK: true}
	case pttStarting:
		if g == 0 && !p.ended {
			p.ended, p.cancel = true, reason
		}
		p.mu.Unlock()
		return false, Result{OK: true}
	}
	w := p.w
	p.phase, p.w = pttOff, window{}
	p.gen++
	if p.done != nil {
		close(p.done)
		p.done = nil
	}
	p.mu.Unlock()
	return true, p.release(w, unkey && keysRig(w.method), reason)
}

// release takes a window down in the safe order: the radio unkeyed first, then the microphone
// stopped and the loopback unloaded, then every tab told.
func (p *PTT) release(w window, unkey bool, reason string) Result {
	res := Result{OK: true}
	if unkey {
		res = p.in.unkey("ptt")
	}
	p.bridge.SetTXAllowed(w.tab, false)
	p.closeAudio(w)
	p.in.refreshTx()
	p.log.Info("PTT off", "radio", p.in.Radio, "reason", reason)
	if p.notify != nil {
		p.notify(p.in.Radio, PTTNotice{Type: "ptt", Radio: p.in.Radio, On: false, Reason: reason})
	}
	return res
}

// closeAudio stops the keying tone and unloads the window's loopback.
func (p *PTT) closeAudio(w window) {
	if w.tone != nil {
		w.tone.end()
	}
	ctx, cancel := context.WithTimeout(context.Background(), audioWithin)
	defer cancel()
	if err := p.server.UnloadTXLoopback(ctx, p.in.Radio, w.module); err != nil {
		p.log.Warn("could not unload the TX loopback", "radio", p.in.Radio, "module", w.module, "error", err)
	}
}

// toneRun plays the keying tone until end.
type toneRun struct {
	pl     TonePlayer
	cancel context.CancelFunc
	stop   chan struct{}
	done   chan struct{}
	once   sync.Once
}

// startTone starts pl and keeps it fed with the tone, paced in real time; failed is called (in
// its own goroutine) if the player stops taking frames.
func startTone(pl TonePlayer, failed func(), log *slog.Logger) (*toneRun, error) {
	ctx, cancel := context.WithCancel(context.Background())
	if err := pl.Start(ctx); err != nil {
		cancel()
		return nil, err
	}
	t := &toneRun{pl: pl, cancel: cancel, stop: make(chan struct{}), done: make(chan struct{})}
	go func() {
		defer close(t.done)
		tick := time.NewTicker(20 * time.Millisecond)
		defer tick.Stop()
		var n int64 // samples played, for the phase
		frame := make([]int16, 2*toneFrame)
		for i := 0; ; i++ {
			n = fillTone(frame, n)
			if err := pl.Write(frame); err != nil {
				select {
				case <-t.stop: // ended by us
				default:
					log.Debug("keying tone write failed", "error", err)
					go failed()
				}
				return
			}
			if i < toneAhead-1 {
				continue
			}
			select {
			case <-t.stop:
				return
			case <-tick.C:
			}
		}
	}()
	return t, nil
}

// end stops the tone: the player is closed first, so a write blocked on it returns at once.
func (t *toneRun) end() {
	t.once.Do(func() {
		close(t.stop)
		_ = t.pl.Close()
		<-t.done
		t.cancel()
	})
}

// fillTone writes one stereo frame of the keying tone (left silent, right the sine) starting at
// sample n and returns the next sample number.
func fillTone(frame []int16, n int64) int64 {
	amp := ToneLevel * 32767
	for i := 0; i < len(frame)/2; i++ {
		ph := 2 * math.Pi * ToneHz * float64(n%toneRate) / toneRate
		frame[2*i] = 0
		frame[2*i+1] = int16(math.Round(amp * math.Sin(ph)))
		n++
	}
	return n
}
