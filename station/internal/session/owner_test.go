package session

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
)

type fakeClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(d)
	c.mu.Unlock()
}

func newClock() *fakeClock { return &fakeClock{now: time.Unix(1_700_000_000, 0)} }

func tab(id string) Holder { return Holder{Kind: "tab", ID: id, Label: "iPad"} }

func TestOwnerClaimAndRelease(t *testing.T) {
	o := NewOwners(newClock())
	if err := o.Claim("radio1", tab("a")); err != nil {
		t.Fatal(err)
	}
	if err := o.Claim("radio1", tab("a")); err != nil {
		t.Fatalf("the holder claiming again: %v", err)
	}
	if err := o.Claim("radio1", tab("b")); !errors.Is(err, ErrHeld) {
		t.Fatalf("second tab: got %v, want ErrHeld", err)
	}
	if h, ok := o.Holder("radio1"); !ok || h.ID != "a" {
		t.Fatalf("holder: %+v %v", h, ok)
	}
	o.Release("radio1", "b") // not the holder: no effect
	if _, ok := o.Holder("radio1"); !ok {
		t.Fatal("a stranger's release dropped the claim")
	}
	o.Release("radio1", "a")
	if _, ok := o.Holder("radio1"); ok {
		t.Fatal("still held after release")
	}
	if err := o.Claim("radio1", tab("b")); err != nil {
		t.Fatalf("claim after release: %v", err)
	}
}

func TestOwnerTakeOverRefusedWhileTransmitting(t *testing.T) {
	o := NewOwners(newClock())
	tx := true
	o.SetTransmitting(func(string) bool { return tx })
	_ = o.Claim("radio1", tab("a"))
	if err := o.TakeOver("radio1", tab("b")); !errors.Is(err, ErrTransmitting) {
		t.Fatalf("got %v, want ErrTransmitting", err)
	}
	if h, _ := o.Holder("radio1"); h.ID != "a" {
		t.Fatalf("holder changed to %q", h.ID)
	}
	tx = false
	if err := o.TakeOver("radio1", tab("b")); err != nil {
		t.Fatal(err)
	}
	if h, _ := o.Holder("radio1"); h.ID != "b" {
		t.Fatalf("holder %q, want b", h.ID)
	}
}

func TestOwnerReleasedOnSocketClose(t *testing.T) {
	o := NewOwners(newClock())
	_ = o.Claim("radio1", tab("a"))
	_ = o.Claim("radio2", tab("a"))
	_ = o.Claim("radio3", tab("b"))
	o.ReleaseAll("a")
	for _, r := range []string{"radio1", "radio2"} {
		if _, ok := o.Holder(r); ok {
			t.Fatalf("%s still held", r)
		}
	}
	if _, ok := o.Holder("radio3"); !ok {
		t.Fatal("another tab's claim was dropped")
	}
}

func TestOwnerReleasedAfterIdle10m(t *testing.T) {
	c := newClock()
	o := NewOwners(c)
	_ = o.Claim("radio1", tab("a"))
	c.Advance(9*time.Minute + 59*time.Second)
	if _, ok := o.Holder("radio1"); !ok {
		t.Fatal("released early")
	}
	o.Touch("radio1", "a") // a transmit intent
	c.Advance(9 * time.Minute)
	if _, ok := o.Holder("radio1"); !ok {
		t.Fatal("touch did not restart the idle time")
	}
	c.Advance(time.Minute + time.Second)
	if _, ok := o.Holder("radio1"); ok {
		t.Fatal("still held after 10 minutes idle")
	}
	if err := o.Claim("radio1", tab("b")); err != nil {
		t.Fatalf("claim after idle: %v", err)
	}
}

func TestProgramHolderBlocksTabs(t *testing.T) {
	list := []radios.Radio{{Name: "radio1", Owner: "wsjtx"}, {Name: "radio2"}}
	active := map[string]bool{"wsjtx-radio1": true}
	probe := func(ctx context.Context, s string) (bool, string, error) { return active[s], "WSJT-X", nil }
	p := NewPrograms(func() []radios.Radio { return list }, probe)
	o := NewOwners(newClock())
	o.SetPrograms(p)

	if h, ok := o.Holder("radio1"); !ok || h.Kind != "program" || h.ID != "wsjtx" {
		t.Fatal("unverified initial assignment did not hold the radio")
	}
	p.Refresh(context.Background())
	h, ok := o.Holder("radio1")
	if !ok || h.Kind != "program" || h.ID != "wsjtx" || h.Label != "WSJT-X" {
		t.Fatalf("holder: %+v %v", h, ok)
	}
	if err := o.Claim("radio1", tab("a")); !errors.Is(err, ErrProgramOwns) {
		t.Fatalf("claim: got %v", err)
	}
	if err := o.TakeOver("radio1", tab("a")); !errors.Is(err, ErrProgramOwns) {
		t.Fatalf("take over: got %v", err)
	}
	if err := o.Claim("radio2", tab("a")); err != nil {
		t.Fatalf("an unowned radio: %v", err)
	}
	active["wsjtx-radio1"] = false
	p.Refresh(context.Background())
	if err := o.Claim("radio1", tab("a")); err != nil {
		t.Fatalf("program stopped: %v", err)
	}
}

func TestScreenProbeReadsState(t *testing.T) {
	run := func(out string, exit int) func(context.Context, []string) (string, int, error) {
		return func(_ context.Context, args []string) (string, int, error) {
			if len(args) != 3 || args[0] != "screen" || args[1] != "wsjtx-radio1" || args[2] != "--json" {
				t.Fatalf("args %v", args)
			}
			return out, exit, nil
		}
	}
	ok, label, _ := ScreenProbe(run(`{"session":"wsjtx-radio1","access":true,"state":"running","label":"WSJT-X"}`, 0))(context.Background(), "wsjtx-radio1")
	if !ok || label != "WSJT-X" {
		t.Fatalf("running: %v %q", ok, label)
	}
	if ok, _, err := ScreenProbe(run(`{"session":"wsjtx-radio1","access":true,"state":"stopped"}`, 0))(context.Background(), "wsjtx-radio1"); ok || err != nil {
		t.Fatalf("stopped: %v %v", ok, err)
	}
	if ok, _, err := ScreenProbe(run(`{"session":"wsjtx-radio1","access":true,"state":"starting"}`, 0))(context.Background(), "wsjtx-radio1"); !ok || err != nil {
		t.Fatalf("starting must hold the radio: %v %v", ok, err)
	}
	if ok, label, err := ScreenProbe(run(`{"session":"wsjtx-radio1","app":"wsjtx","radio":"radio1","label":"WSJT-X","radio_label":"Radio","state":"failed","socket":null,"open":{"kind":"screen"},"access":true}`, 0))(context.Background(), "wsjtx-radio1"); ok || label != "WSJT-X" || err != nil {
		t.Fatalf("full production response: %v %q %v", ok, label, err)
	}
	if _, _, err := ScreenProbe(run(`{"state":"running"}`, 3))(context.Background(), "wsjtx-radio1"); err == nil {
		t.Fatal("a failed command is not an answer")
	}
	if _, _, err := ScreenProbe(run(`not json`, 0))(context.Background(), "wsjtx-radio1"); err == nil {
		t.Fatal("unreadable output is not an answer")
	}
	if _, _, err := ScreenProbe(run(`{"session":"wsjtx-radio1","access":false}`, 0))(context.Background(), "wsjtx-radio1"); err == nil {
		t.Fatal("access:false is not an answer")
	}
}

func TestScreenProbeRejectsMissingAmbiguousOrMismatchedEvidence(t *testing.T) {
	for _, raw := range []string{
		`{}`, `null`, `[]`,
		`{"session":"wsjtx-radio1","access":true}`,
		`{"session":"wsjtx-radio1","access":true,"state":null}`,
		`{"session":"wsjtx-radio1","access":true,"state":"unknown"}`,
		`{"session":"wsjtx-radio1","access":true,"state":"deactivating"}`,
		`{"session":"wsjtx-radio1","access":true,"state":"running","state":"stopped"}`,
		`{"session":"wsjtx-radio1","access":true,"state":"running","State":"stopped"}`,
		`{"session":"wsjtx-radio2","access":true,"state":"stopped"}`,
		`{"session":"pat-radio1","access":true,"state":"stopped"}`,
		`{"session":"wsjtx-radio1","state":"stopped"}`,
		`{"session":"wsjtx-radio1","access":null,"state":"stopped"}`,
		`{"session":"wsjtx-radio1","access":false,"state":"stopped"}`,
		`{"session":"wsjtx-radio1","access":true,"state":"stopped","app":"pat"}`,
		`{"session":"wsjtx-radio1","access":true,"state":"stopped","radio":"radio2"}`,
		`{"session":"wsjtx-radio1","access":true,"state":"stopped","app":null}`,
		`{"session":"wsjtx-radio1","access":true,"state":"stopped","label":null}`,
	} {
		run := func(context.Context, []string) (string, int, error) { return raw, 0, nil }
		probe := ScreenProbe(run)
		if _, _, err := probe(context.Background(), "wsjtx-radio1"); err == nil {
			t.Errorf("incomplete or ambiguous state accepted: %s", raw)
		}
		// Verify the error reaches the ownership guard and cannot free a
		// previously running program's transmitter.
		p := NewPrograms(func() []radios.Radio { return []radios.Radio{{Name: "radio1", Owner: "wsjtx"}} }, func(context.Context, string) (bool, string, error) { return true, "WSJT-X", nil })
		p.Refresh(context.Background())
		p.probe = probe
		p.Refresh(context.Background())
		o := NewOwners(newClock())
		o.SetPrograms(p)
		if err := o.Claim("radio1", tab("a")); !errors.Is(err, ErrProgramOwns) {
			t.Errorf("unverified evidence freed a transmitter: %s: %v", raw, err)
		}
	}
}

func TestProgramOwnerChangeCannotReuseAnotherProgramsAnswer(t *testing.T) {
	for _, previouslyHeld := range []bool{false, true} {
		list := []radios.Radio{{Name: "radio1", Owner: "wsjtx"}}
		fail := false
		p := NewPrograms(func() []radios.Radio { return list }, func(context.Context, string) (bool, string, error) {
			if fail {
				return false, "", errors.New("unavailable")
			}
			return previouslyHeld, "WSJT-X", nil
		})
		p.Refresh(context.Background())
		list[0].Owner, fail = "pat", true
		p.Refresh(context.Background())
		p.Refresh(context.Background())
		o := NewOwners(newClock())
		o.SetPrograms(p)
		if holder, ok := o.Holder("radio1"); !ok || holder.ID != "pat" || holder.Label != "pat" {
			t.Errorf("answer from previous owner survived the switch: held=%v: %+v %v", previouslyHeld, holder, ok)
		}
		if err := o.Claim("radio1", tab("a")); !errors.Is(err, ErrProgramOwns) {
			t.Errorf("new unverified owner allowed a browser claim: %v", err)
		}
		if err := o.TakeOver("radio1", tab("a")); !errors.Is(err, ErrProgramOwns) {
			t.Errorf("new unverified owner allowed takeover: %v", err)
		}
		p.probe = func(context.Context, string) (bool, string, error) { return false, "Pat", nil }
		p.Refresh(context.Background())
		if err := o.Claim("radio1", tab("a")); err != nil {
			t.Errorf("verified stopped replacement did not release the radio: %v", err)
		}
	}
}

func TestProgramHolderKeptWhenProbeFails(t *testing.T) {
	list := []radios.Radio{{Name: "radio1", Owner: "wsjtx"}}
	fail := false
	probe := func(ctx context.Context, s string) (bool, string, error) {
		if fail {
			return false, "", errors.New("timeout")
		}
		return true, "WSJT-X", nil
	}
	p := NewPrograms(func() []radios.Radio { return list }, probe)
	o := NewOwners(newClock())
	o.SetPrograms(p)
	p.Refresh(context.Background())
	fail = true
	p.Refresh(context.Background())
	p.Refresh(context.Background())
	if err := o.Claim("radio1", tab("a")); !errors.Is(err, ErrProgramOwns) {
		t.Fatalf("a failed probe freed the radio: %v", err)
	}
}

func TestSocketRefcountReleasesOnLastClose(t *testing.T) {
	o := NewOwners(newClock())
	var dropped []string
	o.OnDrop(func(r string) { dropped = append(dropped, r) })
	_ = o.Claim("radio1", tab("a"))
	o.SocketOpened("a") // the old page's socket
	o.SocketOpened("a") // the reloaded page's
	o.SocketClosed("a")
	if _, ok := o.Holder("radio1"); !ok || len(dropped) != 0 {
		t.Fatal("claim dropped while a socket is still open")
	}
	o.SocketClosed("a")
	if _, ok := o.Holder("radio1"); ok {
		t.Fatal("claim kept after the last socket closed")
	}
	if len(dropped) != 1 || dropped[0] != "radio1" {
		t.Fatalf("OnDrop calls: %v", dropped)
	}
}

// With no answer yet (the first probe after a start fails), the radio's owner program is taken
// to hold it until a probe succeeds; a later failure also invalidates a free answer.
func TestProgramHolderHeldWhenNoProbeHasAnswered(t *testing.T) {
	list := []radios.Radio{{Name: "radio1", Owner: "wsjtx"}}
	fail, held := true, false
	probe := func(ctx context.Context, s string) (bool, string, error) {
		if fail {
			return false, "", errors.New("timeout")
		}
		return held, "WSJT-X", nil
	}
	p := NewPrograms(func() []radios.Radio { return list }, probe)
	o := NewOwners(newClock())
	o.SetPrograms(p)
	p.Refresh(context.Background())
	h, ok := o.Holder("radio1")
	if !ok || h != (Holder{Kind: "program", ID: "wsjtx", Label: "wsjtx"}) {
		t.Fatalf("holder with no answer: %+v %v", h, ok)
	}
	if err := o.Claim("radio1", tab("a")); !errors.Is(err, ErrProgramOwns) {
		t.Fatalf("claimed with no answer: %v", err)
	}
	p.Refresh(context.Background()) // still failing
	if err := o.Claim("radio1", tab("a")); !errors.Is(err, ErrProgramOwns) {
		t.Fatalf("claimed on the second failure: %v", err)
	}
	fail = false // the program is not running
	p.Refresh(context.Background())
	if err := o.Claim("radio1", tab("a")); err != nil {
		t.Fatalf("free after an answer: %v", err)
	}
	o.Release("radio1", "a")
	fail = true // the previous free answer cannot resolve current uncertainty
	p.Refresh(context.Background())
	if h, ok := o.Holder("radio1"); !ok || h.Kind != "program" || h.ID != "wsjtx" {
		t.Fatal("a failure after a free answer did not block ownership")
	}
}
