package rig

import (
	"context"
	"io"
	"log/slog"
	"strings"
	"sync"
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
	return fn(f)
}

func (f *ixRig) Do(ctx context.Context, cmd string, args ...string) (Reply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.TrimSpace(cmd+" "+strings.Join(args, " ")))
	if f.fail != nil {
		return Reply{}, f.fail
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
	f := &ixRig{st: State{Connected: true, Freq: 14074000, Mode: "USB", VFO: "VFOA",
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
	for _, m := range []string{"PKTUSB", "PKTLSB", "PKTFM", "RTTY", "RTTYR"} {
		h.rig.st.Mode = m
		r := h.in.Tune(context.Background(), "a", true)
		ixRefused(t, r, "a data mode would key the sound card with silence; switch to USB or CW to tune", "switch to USB or CW to tune")
	}
	if h.rig.has("set_ptt 1") {
		t.Fatal("keyed in a data mode")
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
