package rig

import (
	"fmt"
	"slices"
	"sync"
	"time"
)

// The SWR guard (design §9.4). SWRGuard is the latch itself: it sees every SWR read taken while
// the radio is keyed, and latches on the third one in a row over the limit. Guard (below) is
// the radio's wiring of it: the poller's reads in, PTT dropped and the radio unkeyed on a trip,
// the transmit intents refused while latched, notices to every tab.

const (
	// GuardFrames is how many consecutive over-limit reads trip the guard (200 ms apart).
	GuardFrames = 3
	// ATUWindow is how long SWR reads are ignored after an ATU intent: the tuner is mismatched
	// on purpose while it works.
	ATUWindow = 30 * time.Second
	// GuardFix is the fix named by every refusal while the guard is latched.
	GuardFix = "tune the antenna, change band, or override"
	// NoSWRGuard is what the Radios page says of a radio whose SWR cannot be read over CAT.
	NoSWRGuard = "no SWR guard: this radio does not report SWR over CAT"
)

// Verdict is what the guard made of one read.
type Verdict int

// The verdicts of SWRGuard.Observe.
const (
	Quiet Verdict = iota
	Tripped
)

// amateurBands is the band table of the VFO page (web/vfo.js BANDS), edges in Hz, US allocations.
var amateurBands = []struct {
	Name   string
	Lo, Hi int64
}{
	{"160 m", 1800000, 2000000}, {"80 m", 3500000, 4000000}, {"60 m", 5330500, 5406400},
	{"40 m", 7000000, 7300000}, {"30 m", 10100000, 10150000}, {"20 m", 14000000, 14350000},
	{"17 m", 18068000, 18168000}, {"15 m", 21000000, 21450000}, {"12 m", 24890000, 24990000},
	{"10 m", 28000000, 29700000}, {"6 m", 50000000, 54000000}, {"2 m", 144000000, 148000000},
	{"1.25 m", 222000000, 225000000}, {"70 cm", 420000000, 450000000}, {"33 cm", 902000000, 928000000},
	{"23 cm", 1240000000, 1300000000},
}

// BandOf is the amateur band hz lies in, or "" outside them all.
func BandOf(hz int64) string {
	for _, b := range amateurBands {
		if hz >= b.Lo && hz <= b.Hi {
			return b.Name
		}
	}
	return ""
}

// SWRGuard is one radio's latch. All methods are safe for concurrent use.
type SWRGuard struct {
	limit func() float64
	clock Clock

	mu         sync.Mutex
	over       int // consecutive over-limit reads
	latched    bool
	tripSWR    float64
	tripLimit  float64
	band       string
	bandKnown  bool
	latchBand  string
	overridden bool   // the latch was overridden on overBand: no trips there
	overBand   string //
	until      time.Time
	atuArmed   bool // an ATU was started since the latch: its first read after the window decides
	lastIgn    float64
	haveIgn    bool
}

// NewSWRGuard makes a guard whose limit is read on every use (the radio's setting may change).
func NewSWRGuard(limit func() float64, clock Clock) *SWRGuard {
	if clock == nil {
		clock = realClock{}
	}
	return &SWRGuard{limit: limit, clock: clock}
}

// Latched reports whether transmitting is refused.
func (g *SWRGuard) Latched() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.latched
}

// Reason is the words for the latch, or "" when it never tripped.
func (g *SWRGuard) Reason() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.reasonLocked()
}

func (g *SWRGuard) reasonLocked() string {
	if g.tripLimit == 0 {
		return ""
	}
	return fmt.Sprintf("SWR guard: %.1f:1 against %.1f:1; %s", g.tripSWR, g.tripLimit, GuardFix)
}

// Observe takes one SWR read. pttOn says the radio was keyed when it was read; reads taken
// while it receives only reset the count. Inside the ATU window reads are ignored. It returns
// Tripped on the third consecutive over-limit read (again on later ones while latched, so a
// program that keys the radio again is unkeyed again).
func (g *SWRGuard) Observe(swr float64, pttOn bool) Verdict {
	lim := g.limit()
	g.mu.Lock()
	defer g.mu.Unlock()
	if !pttOn {
		g.over = 0
		return Quiet
	}
	if !g.until.IsZero() && g.clock.Now().Before(g.until) {
		g.over = 0
		g.lastIgn, g.haveIgn = swr, true
		return Quiet
	}
	if g.overridden && g.overBand == g.band {
		g.over = 0
		return Quiet
	}
	if swr <= lim {
		g.over = 0
		return Quiet
	}
	if g.over++; g.over < GuardFrames {
		return Quiet
	}
	g.over = 0
	g.latched, g.atuArmed = true, false
	g.tripSWR, g.tripLimit, g.latchBand = swr, lim, g.band
	return Tripped
}

// NoteFreq tells the guard the radio's frequency. A move into a different band clears the latch
// and re-arms an override; it reports whether the latch was cleared.
func (g *SWRGuard) NoteFreq(hz int64) bool {
	band := BandOf(hz)
	g.mu.Lock()
	moved := g.bandKnown && band != g.band
	g.band, g.bandKnown = band, true
	if g.overridden && g.overBand != band {
		g.overridden = false
	}
	latched, from := g.latched, g.latchBand
	g.mu.Unlock()
	if moved && latched && band != from {
		return g.ClearForBandChange()
	}
	return false
}

// ClearForBandChange clears the latch because the radio moved to another band. It reports
// whether there was a latch to clear.
func (g *SWRGuard) ClearForBandChange() bool { return g.clear() }

func (g *SWRGuard) clear() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	was := g.latched
	g.latched, g.atuArmed, g.over = false, false, 0
	return was
}

// SuppressAroundATU ignores SWR reads for d from now (an ATU intent was sent: the tuner is
// mismatched while it works) and arms ClearAfterATU.
func (g *SWRGuard) SuppressAroundATU(d time.Duration) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.until = g.clock.Now().Add(d)
	g.atuArmed, g.haveIgn, g.over = true, false, 0
}

// ATUPending reports whether a latch waits for the read that follows an ATU window.
func (g *SWRGuard) ATUPending() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.latched && g.atuArmed
}

// Settled returns the last read taken inside the ATU window: the tuner has stopped keying by the
// time the window is over, so no later read may ever come, and the last one is the tuned match.
func (g *SWRGuard) Settled() (swr float64, ok bool) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.lastIgn, g.haveIgn
}

// ClearAfterATU clears the latch when the window is over and nextSWR, the first read after an
// ATU, is under the limit. A read still over the limit ends the wait (the ATU did not fix it).
func (g *SWRGuard) ClearAfterATU(nextSWR float64) bool {
	lim := g.limit()
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.latched || !g.atuArmed || g.clock.Now().Before(g.until) {
		return false
	}
	g.atuArmed = false
	if nextSWR > lim {
		return false
	}
	g.latched, g.over = false, 0
	return true
}

// Override clears the latch for the band the radio is on now; no trip happens there until the
// band changes.
func (g *SWRGuard) Override() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.latched, g.atuArmed, g.over = false, false, 0
	g.overridden, g.overBand = true, g.band
}

// GuardNotice is sent to every tab when the guard latches or clears.
type GuardNotice struct {
	Type    string `json:"type"`
	Radio   string `json:"radio"`
	Latched bool   `json:"latched"`
	Reason  string `json:"reason"`
	Note    string `json:"note,omitempty"` // what to do when a program may key the radio again
}

// Guard is one radio's SWR guard as wired into its intents and poller.
type Guard struct {
	in     *Intents
	g      *SWRGuard
	notify Notifier

	// Drop ends a PTT window for a reason (PTT.Drop); nil: no window to end.
	Drop func(reason string)
}

// NewGuard makes in's SWR guard: limit is the radio's SWR limit setting (read on every use).
func NewGuard(in *Intents, limit func() float64, notify Notifier) *Guard {
	gd := &Guard{in: in, g: NewSWRGuard(limit, in.clock()), notify: notify}
	in.mu.Lock()
	in.guard = gd
	in.mu.Unlock()
	return gd
}

// Active reports whether the radio has a guard: it reports SWR over CAT.
func (gd *Guard) Active() bool {
	caps, ok := gd.in.Caps()
	return ok && slices.Contains(caps.ReadableMeters, "SWR")
}

// View is the guard as the page shows it.
func (gd *Guard) View() (active, latched bool, reason string) {
	active = gd.Active()
	if active && gd.g.Latched() {
		return true, true, gd.g.Reason()
	}
	return active, false, ""
}

// Latch exposes the latch itself.
func (gd *Guard) Latch() *SWRGuard { return gd.g }

// refusal is the answer to a transmit intent while latched, or nil.
func (gd *Guard) refusal(intent string) *Result {
	if !gd.Active() || !gd.g.Latched() {
		return nil
	}
	r := gd.in.refuse(intent, gd.g.Reason(), GuardFix)
	return &r
}

func (gd *Guard) send(latched bool, reason, note string) {
	if gd.notify != nil {
		gd.notify(gd.in.Radio, GuardNotice{Type: "guard", Radio: gd.in.Radio, Latched: latched, Reason: reason, Note: note})
	}
}

func (gd *Guard) cleared(cause string) {
	gd.in.log().Info(fmt.Sprintf("radio %s: SWR guard cleared (%s)", gd.in.Radio, cause))
	gd.send(false, cause, "")
}

// Read is the poller's hook: one SWR read taken while pttOn (false: the radio is receiving).
func (gd *Guard) Read(swr float64, pttOn bool) {
	if !gd.Active() {
		return
	}
	if pttOn {
		gd.NoteFreq(gd.in.Poller.State().Freq)
		if gd.g.ATUPending() && gd.g.ClearAfterATU(swr) {
			gd.cleared(fmt.Sprintf("ATU tuned to %.1f:1", swr))
			return
		}
	}
	was := gd.g.Latched()
	if gd.g.Observe(swr, pttOn) == Tripped {
		gd.tripped(was)
	}
}

// NoteFreq is told the radio's frequency (an intent, or a read of the radio's dial).
func (gd *Guard) NoteFreq(hz int64) {
	if gd.g.NoteFreq(hz) {
		gd.cleared("band changed")
	}
}

func (gd *Guard) tripped(was bool) {
	gd.g.mu.Lock()
	swr, lim, reason := gd.g.tripSWR, gd.g.tripLimit, gd.g.reasonLocked()
	gd.g.mu.Unlock()
	gd.in.log().Warn(fmt.Sprintf("radio %s: SWR guard tripped %.1f:1 (limit %.1f)", gd.in.Radio, swr, lim))
	note := ""
	if h, ok := gd.in.Owners.Holder(gd.in.Radio); ok && h.Kind == "program" {
		note = fmt.Sprintf("%s may key again; Stop %s to end it", h.Label, h.Label)
	}
	if !was || note != "" {
		gd.send(true, reason, note)
	}
	// the poller's goroutine goes on reading: the radio is unkeyed on its own
	go func() {
		if gd.Drop != nil {
			gd.Drop("SWR guard")
		}
		// whoever keyed it: a Tune carrier, PTT, or a program through rigctld
		gd.in.unkey("swr guard")
	}()
}

// ATUStarted is told an ATU intent was sent: reads are ignored for ATUWindow, and a latch is
// cleared at the end of it when the match the tuner reached was under the limit.
func (gd *Guard) ATUStarted() {
	gd.g.SuppressAroundATU(ATUWindow)
	end := gd.in.clock().At(gd.in.clock().Now().Add(ATUWindow))
	done := gd.in.closedCh()
	var ctxDone <-chan struct{}
	if gd.in.Ctx != nil {
		ctxDone = gd.in.Ctx.Done()
	}
	go func() {
		select {
		case <-end:
		case <-done:
			return
		case <-ctxDone:
			return
		}
		if !gd.g.ATUPending() {
			return
		}
		if swr, ok := gd.g.Settled(); ok && gd.g.ClearAfterATU(swr) {
			gd.cleared(fmt.Sprintf("ATU tuned to %.1f:1", swr))
		}
	}()
}

// Override is the owner's confirmed override: the latch clears for the band the radio is on.
func (gd *Guard) Override(device string) {
	gd.g.Override()
	gd.in.log().Warn(fmt.Sprintf("radio %s: SWR guard overridden by %s", gd.in.Radio, device))
	gd.cleared("override by " + device)
}

// Hooks are the callbacks a poller makes on reads.
type Hooks struct {
	// SWR gets every SWR read taken while the radio is keyed, and (0, false) when a transmit
	// cycle finds it receiving.
	SWR func(swr float64, pttOn bool)
	// Freq gets each frequency a read reports.
	Freq func(hz int64)
}

// Hooks is the poller's side of the guard.
func (gd *Guard) Hooks() Hooks {
	return Hooks{SWR: gd.Read, Freq: gd.NoteFreq}
}
