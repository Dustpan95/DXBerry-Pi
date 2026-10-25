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

	if _, ok := o.Holder("radio1"); ok {
		t.Fatal("holder before any refresh")
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
	ok, label, _ := ScreenProbe(run(`{"state":"running","label":"WSJT-X"}`, 0))(context.Background(), "wsjtx-radio1")
	if !ok || label != "WSJT-X" {
		t.Fatalf("running: %v %q", ok, label)
	}
	if ok, _, err := ScreenProbe(run(`{"state":"stopped"}`, 0))(context.Background(), "wsjtx-radio1"); ok || err != nil {
		t.Fatalf("stopped: %v %v", ok, err)
	}
	if ok, _, err := ScreenProbe(run(`{"state":"starting"}`, 0))(context.Background(), "wsjtx-radio1"); !ok || err != nil {
		t.Fatalf("starting must hold the radio: %v %v", ok, err)
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
