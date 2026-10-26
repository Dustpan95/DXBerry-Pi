package rig

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/session"
)

const guardReason = "SWR guard: 5.8:1 against 3.0:1; tune the antenna, change band, or override"

func newLatch() (*SWRGuard, *plClock) {
	clk := newPlClock()
	return NewSWRGuard(func() float64 { return 3.0 }, clk), clk
}

func trip(t *testing.T, g *SWRGuard, swr float64) {
	t.Helper()
	g.Observe(swr, true, false)
	g.Observe(swr, true, false)
	if g.Observe(swr, true, false) != Tripped {
		t.Fatalf("no trip at %.1f", swr)
	}
}

func TestSWRGuardNeedsThreeFrames(t *testing.T) {
	g, _ := newLatch()
	if g.Observe(5.8, true, false) != Quiet || g.Observe(5.8, true, false) != Quiet || g.Latched() {
		t.Fatal("tripped before three reads")
	}
	if g.Observe(2.0, true, false) != Quiet { // a good read starts the count again
		t.Fatal("a good read tripped")
	}
	g.Observe(5.8, true, false)
	g.Observe(5.8, true, false)
	if g.Latched() {
		t.Fatal("the count was not reset by a good read")
	}
	if g.Observe(5.8, true, false) != Tripped || !g.Latched() {
		t.Fatal("the third read in a row did not trip")
	}
	if got := g.Reason(); got != guardReason {
		t.Fatalf("reason %q", got)
	}
	// exactly at the limit is not over it
	g2, _ := newLatch()
	for i := 0; i < 6; i++ {
		g2.Observe(3.0, true, false)
	}
	if g2.Latched() {
		t.Fatal("a read at the limit tripped")
	}
}

func TestSWRGuardIgnoresWhileRX(t *testing.T) {
	g, _ := newLatch()
	for i := 0; i < 10; i++ {
		if g.Observe(9, false, false) != Quiet {
			t.Fatal("tripped while receiving")
		}
	}
	g.Observe(9, true, false)
	g.Observe(9, true, false)
	g.Observe(9, false, false) // receiving: the run is broken
	g.Observe(9, true, false)
	if g.Latched() {
		t.Fatal("reads either side of a receive period counted as consecutive")
	}
}

func TestSWRGuardSuppressedAroundATU(t *testing.T) {
	g, clk := newLatch()
	g.SuppressAroundATU(30 * time.Second)
	for i := 0; i < 9; i++ {
		if g.Observe(8, true, false) != Quiet {
			t.Fatal("tripped inside the ATU window")
		}
	}
	clk.Advance(29 * time.Second)
	g.Observe(8, true, false)
	if g.Latched() {
		t.Fatal("tripped at 29 s")
	}
	clk.Advance(time.Second)
	trip(t, g, 8)
}

func TestSWRGuardClearsOnBandChange(t *testing.T) {
	g, _ := newLatch()
	g.NoteFreq(14074000)
	trip(t, g, 5.8)
	if g.NoteFreq(14200000) || !g.Latched() {
		t.Fatal("a move inside the band cleared the latch")
	}
	if !g.NoteFreq(7074000) || g.Latched() {
		t.Fatal("a move to 40 m did not clear the latch")
	}
	if BandOf(14074000) != "20 m" || BandOf(99) != "" || BandOf(144390000) != "2 m" {
		t.Fatal("band table")
	}
}

func TestSWRGuardClearsAfterGoodATU(t *testing.T) {
	g, clk := newLatch()
	trip(t, g, 5.8)
	g.SuppressAroundATU(30 * time.Second)
	if g.ClearAfterATU(1.5) {
		t.Fatal("cleared inside the window")
	}
	clk.Advance(30 * time.Second)
	if g.ClearAfterATU(4.0) || !g.Latched() {
		t.Fatal("a read over the limit cleared the latch")
	}
	if g.ClearAfterATU(1.5) {
		t.Fatal("cleared without a new ATU")
	}
	g.SuppressAroundATU(30 * time.Second)
	clk.Advance(30 * time.Second)
	if !g.ClearAfterATU(1.5) || g.Latched() {
		t.Fatal("a good read after the ATU did not clear the latch")
	}
}

func TestSWRGuardOverride(t *testing.T) {
	g, _ := newLatch()
	g.NoteFreq(14074000)
	trip(t, g, 5.8)
	g.Override()
	if g.Latched() {
		t.Fatal("still latched after the override")
	}
	for i := 0; i < 9; i++ {
		if g.Observe(6, true, false) != Quiet {
			t.Fatal("tripped on the overridden band")
		}
	}
	g.NoteFreq(14100000) // same band: still overridden
	if g.Observe(6, true, false) != Quiet {
		t.Fatal("tripped inside the overridden band")
	}
	g.NoteFreq(7074000) // another band re-arms
	trip(t, g, 6)
	g.NoteFreq(14074000) // back to 20 m: the old override is gone too
	trip(t, g, 6)
}

// ---- the guard as wired to a radio -----------------------------------------------------------

// progOwners reports a program as the radio's holder.
type progOwners struct {
	OwnerLock
	h session.Holder
}

func (p progOwners) Holder(string) (session.Holder, bool) { return p.h, true }

type guardHarness struct {
	*ixHarness
	gd    *Guard
	mu    sync.Mutex
	notes []string
	drops []string
}

func swrCaps() Caps {
	c := ixCaps()
	c.ReadableMeters = []string{"SWR"}
	return c
}

func newGuardHarness(t *testing.T, caps Caps) *guardHarness {
	t.Helper()
	h := &guardHarness{ixHarness: newIxHarness(caps)}
	h.gd = NewGuard(h.in, func() float64 { return 3.0 }, func(radio string, msg any) {
		b, err := json.Marshal(msg)
		if err != nil {
			t.Errorf("notice: %v", err)
		}
		h.mu.Lock()
		h.notes = append(h.notes, string(b))
		h.mu.Unlock()
	})
	h.gd.Drop = func(reason string) { h.mu.Lock(); h.drops = append(h.drops, reason); h.mu.Unlock() }
	return h
}

func (h *guardHarness) noteLog() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string{}, h.notes...)
}

func (h *guardHarness) read(swr float64, n int) {
	for i := 0; i < n; i++ {
		h.gd.Read(swr, true)
	}
}

func TestSWRGuardUnkeysProgramKeyedRadio(t *testing.T) {
	h := newGuardHarness(t, swrCaps())
	h.in.Owners = progOwners{h.in.Owners, session.Holder{Kind: "program", ID: "wsjtx", Label: "WSJT-X"}}
	h.rig.mu.Lock()
	h.rig.st.PTT = true // a program keyed the radio through rigctld; the station keyed nothing
	h.rig.mu.Unlock()
	h.read(5.8, 3)
	waitFor(t, "set_ptt 0", func() bool { return h.rig.has("set_ptt 0") })
	notes := h.noteLog()
	if len(notes) != 1 {
		t.Fatalf("notices %v", notes)
	}
	var n map[string]any
	if err := json.Unmarshal([]byte(notes[0]), &n); err != nil {
		t.Fatal(err)
	}
	if n["type"] != "guard" || n["radio"] != "radio1" || n["latched"] != true || n["reason"] != guardReason ||
		!strings.Contains(n["note"].(string), "Stop WSJT-X") {
		t.Fatalf("notice %v", n)
	}
	waitFor(t, "PTT window dropped", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.drops) == 1 && h.drops[0] == "SWR guard"
	})
	if h.rig.State().PTT {
		t.Fatal("the radio is still keyed")
	}
}

func TestSWRGuardEndsTuneCarrierAndRefusesTransmit(t *testing.T) {
	h := newGuardHarness(t, swrCaps())
	ctx := context.Background()
	if r := h.in.Claim(ixHolder("a")); !r.OK {
		t.Fatal(r)
	}
	if r := h.in.Freq(ctx, 14074000); !r.OK {
		t.Fatalf("freq: %+v", r)
	}
	if r := h.in.Tune(ctx, "a", true); !r.OK {
		t.Fatalf("tune: %+v", r)
	}
	h.read(5.8, 3)
	waitFor(t, "carrier ended", func() bool { return h.rig.has("set_ptt 0") && !h.in.carrierUp() })
	ixRefused(t, h.in.Tune(ctx, "a", true), guardReason, "tune the antenna, change band, or override")
	p := NewPTT(h.in, nil, nil, nil, "rigctld", nil, h.clock, nil)
	ixRefused(t, p.On(ctx, "a"), guardReason, "tune the antenna, change band, or override")
	// the ATU is how the latch is cleared: not refused
	if r := h.in.ATU(ctx, "a"); !r.OK {
		t.Fatalf("atu while latched: %+v", r)
	}
	// receive-side intents stay open
	if r := h.in.Mode(ctx, "CW", 0); !r.OK {
		t.Fatalf("mode while latched: %+v", r)
	}
	if r := h.in.Freq(ctx, 14100000); !r.OK {
		t.Fatalf("freq in the same band: %+v", r)
	}
	if r := h.in.Level(ctx, "AF", 0.5); !r.OK {
		t.Fatalf("level while latched: %+v", r)
	}
	if _, latched, _ := h.gd.View(); !latched {
		t.Fatal("a same-band move cleared the latch")
	}
	if r := h.in.Freq(ctx, 7074000); !r.OK {
		t.Fatalf("freq to 40 m: %+v", r)
	}
	if _, latched, _ := h.gd.View(); latched {
		t.Fatal("a band change did not clear the latch")
	}
	notes := h.noteLog()
	if last := notes[len(notes)-1]; last != `{"type":"guard","radio":"radio1","latched":false,"reason":"band changed"}` {
		t.Fatalf("clear notice %s", last)
	}
	h.rig.mu.Lock()
	h.rig.st.Mode = "FM" // the mode intent above left CW, which sends no carrier to tune with
	h.rig.mu.Unlock()
	if r := h.in.Tune(ctx, "a", true); !r.OK {
		t.Fatalf("tune after the band change: %+v", r)
	}
}

func TestSWRGuardOverrideIsOwnerOnly(t *testing.T) {
	h := newGuardHarness(t, swrCaps())
	h.in.Claim(ixHolder("a"))
	h.read(5.8, 3)
	ixRefused(t, h.in.Override("b"), "does not hold the transmitter", "claim")
	if _, latched, _ := h.gd.View(); !latched {
		t.Fatal("a non-owner cleared the latch")
	}
	if r := h.in.Override("a"); !r.OK {
		t.Fatalf("override: %+v", r)
	}
	if _, latched, _ := h.gd.View(); latched {
		t.Fatal("still latched")
	}
	notes := h.noteLog()
	if last := notes[len(notes)-1]; last != `{"type":"guard","radio":"radio1","latched":false,"reason":"override by iPad"}` {
		t.Fatalf("notice %s", last)
	}
}

func TestSWRGuardClearsAfterATUWindowWithGoodMatch(t *testing.T) {
	h := newGuardHarness(t, swrCaps())
	ctx := context.Background()
	h.in.Claim(ixHolder("a"))
	h.read(5.8, 3)
	if r := h.in.ATU(ctx, "a"); !r.OK {
		t.Fatal(r)
	}
	h.gd.Read(1.4, true) // the tuner's carrier, inside the window: ignored, but remembered
	if _, latched, _ := h.gd.View(); !latched {
		t.Fatal("cleared inside the window")
	}
	waitFor(t, "ATU timer", func() bool { return len(h.clock.waiters) > 0 })
	h.clock.Advance(ATUWindow)
	waitFor(t, "cleared", func() bool { _, l, _ := h.gd.View(); return !l })
	// the latch clears first and the notice follows (a tab hears it a moment late, never early)
	waitFor(t, "clear notice", func() bool { return len(h.noteLog()) >= 2 })
	notes := h.noteLog()
	if last := notes[len(notes)-1]; last != `{"type":"guard","radio":"radio1","latched":false,"reason":"ATU tuned to 1.4:1"}` {
		t.Fatalf("notice %s", last)
	}
}

func TestNoSWRGuardWithoutReadableSWR(t *testing.T) {
	h := newGuardHarness(t, ixCaps())
	h.read(9, 5)
	active, latched, _ := h.gd.View()
	if active || latched || h.rig.has("set_ptt 0") {
		t.Fatal("a radio with no SWR meter got a guard")
	}
}

// ---- fix round 1 -------------------------------------------------------------------------------

// Out-of-band frequencies (and 0, before the first read) are not band changes.
func TestSWRGuardOnlyAnotherAmateurBandClears(t *testing.T) {
	g, _ := newLatch()
	g.NoteFreq(14074000)
	trip(t, g, 5.8)
	for _, hz := range []int64{14350001, 13999999, 0, 14074000, 14000000, 14350000} {
		if g.NoteFreq(hz) || !g.Latched() {
			t.Fatalf("%d cleared a 20 m latch", hz)
		}
	}
	if !g.NoteFreq(7000000) || g.Latched() {
		t.Fatal("the 40 m band edge did not clear it")
	}
	// the same through the guard's own state: freq 0 before any read
	g2, _ := newLatch()
	g2.NoteFreq(0)
	g2.NoteFreq(14074000)
	trip(t, g2, 5.8)
	g2.NoteFreq(0)
	if !g2.Latched() {
		t.Fatal("freq 0 cleared the latch")
	}
}

// The ATU window ignores the tuner's own carrier, not a transmission the station makes meanwhile.
func TestSWRGuardATUWindowDoesNotBlindStationTransmit(t *testing.T) {
	g, _ := newLatch()
	g.SuppressAroundATU(30 * time.Second)
	for i := 0; i < 5; i++ {
		if g.Observe(8, true, false) != Quiet {
			t.Fatal("the tuner's carrier tripped")
		}
	}
	g.Observe(8, true, true)
	g.Observe(8, true, true)
	if g.Observe(8, true, true) != Tripped {
		t.Fatal("a PTT inside the window was not guarded")
	}
}

func TestSWRGuardOverrideRearmsOnGoodRead(t *testing.T) {
	g, _ := newLatch()
	g.NoteFreq(14074000)
	trip(t, g, 5.8)
	g.Override()
	for i := 0; i < 5; i++ {
		g.Observe(6, true, false)
	}
	if g.Latched() {
		t.Fatal("tripped while overridden")
	}
	g.Observe(1.5, true, false) // the match is good: the override is spent
	trip(t, g, 6)
}

func TestSWRGuardOverrideRearmsOnReleaseOrNewHolder(t *testing.T) {
	for _, how := range []string{"release", "takeover"} {
		h := newGuardHarness(t, swrCaps())
		h.in.Claim(ixHolder("a"))
		h.read(5.8, 3)
		if r := h.in.Override("a"); !r.OK {
			t.Fatal(r)
		}
		h.read(6, 3)
		if _, l, _ := h.gd.View(); l {
			t.Fatalf("%s: tripped while overridden", how)
		}
		if how == "release" {
			h.in.Release("a")
		} else if r := h.in.TakeOver(ixHolder("b")); !r.OK {
			t.Fatal(r)
		}
		h.read(6, 3)
		if _, l, _ := h.gd.View(); !l {
			t.Fatalf("%s: the override survived", how)
		}
	}
}

// A last in-window read over the limit leaves the latch set; two ATUs in a row forget the first's
// reads.
func TestSWRGuardATUSettledReads(t *testing.T) {
	h := newGuardHarness(t, swrCaps())
	h.in.Claim(ixHolder("a"))
	h.read(5.8, 3)
	if r := h.in.ATU(context.Background(), "a"); !r.OK {
		t.Fatal(r)
	}
	h.gd.Read(4.0, true)
	waitFor(t, "ATU timer", func() bool { return len(h.clock.waiters) > 0 })
	h.clock.Advance(ATUWindow)
	settle()
	if _, l, _ := h.gd.View(); !l {
		t.Fatal("a tuner that left SWR at 4.0 cleared the latch")
	}
	g, _ := newLatch()
	g.SuppressAroundATU(time.Second)
	g.Observe(1.4, true, false)
	if _, ok := g.Settled(); !ok {
		t.Fatal("no settled read")
	}
	g.SuppressAroundATU(time.Second)
	if _, ok := g.Settled(); ok {
		t.Fatal("the first ATU's read leaked into the second")
	}
}

type lockedBuf struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuf) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}
func (l *lockedBuf) String() string { l.mu.Lock(); defer l.mu.Unlock(); return l.b.String() }

func TestSWRGuardLogLines(t *testing.T) {
	h := newGuardHarness(t, swrCaps())
	buf := &lockedBuf{}
	h.in.Log = slog.New(slog.NewTextHandler(buf, nil))
	h.in.Claim(ixHolder("a"))
	h.read(5.8, 3)
	h.in.Override("a")
	out := buf.String()
	for _, want := range []string{
		`msg="radio radio1: SWR guard tripped 5.8:1 (limit 3.0)"`,
		`msg="radio radio1: SWR guard overridden by iPad"`,
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("log lacks %s:\n%s", want, out)
		}
	}
}

// An override made with nobody holding the radio forgets an earlier override's holder.
func TestSWRGuardOverrideWithoutHolderClearsStaleHolder(t *testing.T) {
	h := newGuardHarness(t, swrCaps())
	h.in.Claim(ixHolder("a"))
	h.read(5.8, 3)
	h.in.Override("a")
	h.in.Release("a") // the stale holder "a" is now gone
	h.read(5.8, 3)    // re-armed by the release, trips again
	if _, l, _ := h.gd.View(); !l {
		t.Fatal("no trip after the release")
	}
	h.gd.Override("somebody") // no holder now
	h.read(6, 3)
	if _, l, _ := h.gd.View(); l {
		t.Fatal("a stale holder re-armed the override at once")
	}
}
