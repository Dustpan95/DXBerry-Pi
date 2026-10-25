package rig

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"
)

// plClock only moves when a test advances it; At fires when the clock reaches the deadline.
type plClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []plWaiter
}

type plWaiter struct {
	at time.Time
	ch chan time.Time
}

func newPlClock() *plClock { return &plClock{now: time.Unix(1_700_000_000, 0)} }

func (c *plClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *plClock) At(t time.Time) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if !t.After(c.now) {
		ch <- c.now
		return ch
	}
	c.waiters = append(c.waiters, plWaiter{t, ch})
	return ch
}

func (c *plClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	keep := c.waiters[:0]
	for _, w := range c.waiters {
		if w.at.After(c.now) {
			keep = append(keep, w)
		} else {
			w.ch <- c.now
		}
	}
	c.waiters = keep
}

// plLink is a scripted rig: replies by "cmd arg", every call recorded.
type plLink struct {
	mu    sync.Mutex
	up    bool
	cbs   []func(bool)
	calls []string
	reply map[string][]string
	errs  map[string]error
}

func newPlLink() *plLink {
	l := &plLink{up: true, reply: map[string][]string{}, errs: map[string]error{}}
	l.reply["get_freq"] = []string{"Frequency: 14074000"}
	l.reply["get_mode"] = []string{"Mode: USB", "Passband: 2400"}
	l.reply["get_vfo"] = []string{"VFO: VFOA"}
	l.reply["get_ptt"] = []string{"PTT: 0"}
	l.reply["get_split_vfo"] = []string{"Split: 0", "TX VFO: VFOB"}
	return l
}

func (l *plLink) Do(ctx context.Context, cmd string, args ...string) (Reply, error) {
	key := strings.TrimSpace(cmd + " " + strings.Join(args, " "))
	l.mu.Lock()
	defer l.mu.Unlock()
	l.calls = append(l.calls, key)
	if !l.up {
		return Reply{}, ErrNotConnected
	}
	if err := l.errs[key]; err != nil {
		return Reply{}, err
	}
	r, ok := l.reply[key]
	if !ok {
		return Reply{}, &RigError{Code: -1}
	}
	return Reply{Lines: r}, nil
}

func (l *plLink) Connected() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.up
}

func (l *plLink) OnState(fn func(bool)) {
	l.mu.Lock()
	l.cbs = append(l.cbs, fn)
	l.mu.Unlock()
}

func (l *plLink) Close() {}

func (l *plLink) set(key string, lines ...string) {
	l.mu.Lock()
	l.reply[key] = lines
	l.mu.Unlock()
}

func (l *plLink) setUp(up bool) {
	l.mu.Lock()
	l.up = up
	cbs := append([]func(bool){}, l.cbs...)
	l.mu.Unlock()
	for _, fn := range cbs {
		fn(up)
	}
}

func (l *plLink) count(key string) int {
	l.mu.Lock()
	defer l.mu.Unlock()
	n := 0
	for _, c := range l.calls {
		if c == key {
			n++
		}
	}
	return n
}

func plEventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func settle() { time.Sleep(40 * time.Millisecond) }

func startPoller(t *testing.T, l *plLink, caps Caps) (*Poller, *plClock) {
	t.Helper()
	clk := newPlClock()
	p := newPoller(l, caps, clk, slog.New(slog.NewTextHandler(io.Discard, nil)))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { p.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return p, clk
}

func TestPollerRatesFollowInterest(t *testing.T) {
	l := newPlLink()
	p, clk := startPoller(t, l, Caps{})
	plEventually(t, "first read", func() bool { return l.count("get_freq") == 1 })
	clk.Advance(9900 * time.Millisecond)
	settle()
	if n := l.count("get_freq"); n != 1 {
		t.Fatalf("None interest read again before 10 s: %d", n)
	}
	clk.Advance(100 * time.Millisecond)
	plEventually(t, "10 s read", func() bool { return l.count("get_freq") == 2 })

	p.SetInterest(Dashboard)
	clk.Advance(1900 * time.Millisecond)
	settle()
	if n := l.count("get_freq"); n != 2 {
		t.Fatalf("Dashboard read before 2 s: %d", n)
	}
	clk.Advance(100 * time.Millisecond)
	plEventually(t, "2 s read", func() bool { return l.count("get_freq") == 3 })

	p.SetInterest(Page)
	clk.Advance(250 * time.Millisecond)
	plEventually(t, "250 ms read", func() bool { return l.count("get_freq") == 4 })
	clk.Advance(100 * time.Millisecond)
	settle()
	if n := l.count("get_freq"); n != 4 {
		t.Fatalf("Page read before 250 ms: %d", n)
	}
	st := p.State()
	if st.Freq != 14074000 || st.Mode != "USB" || st.Passband != "2400" || st.VFO != "VFOA" || !st.Connected {
		t.Fatalf("state: %+v", st)
	}
}

func meterCaps() Caps {
	return Caps{
		ReadableMeters: []string{"STRENGTH", "SWR", "ALC", "RFPOWER"},
		SettableLevels: []string{"RFPOWER"},
	}
}

func TestPollerMetersOnlyOnPage(t *testing.T) {
	l := newPlLink()
	l.set("get_level STRENGTH", "Level Value: -12")
	l.set("get_level RFPOWER", "Level Value: 0.5")
	l.set("get_level SWR", "Level Value: 1.5")
	l.set("get_level ALC", "Level Value: 0.2")
	p, clk := startPoller(t, l, meterCaps())
	p.SetInterest(Dashboard)
	plEventually(t, "first read", func() bool { return l.count("get_freq") >= 1 })
	clk.Advance(2 * time.Second)
	plEventually(t, "second read", func() bool { return l.count("get_freq") >= 2 })
	if n := l.count("get_level STRENGTH"); n != 0 {
		t.Fatalf("meter read off the page: %d", n)
	}
	p.SetInterest(Page)
	clk.Advance(250 * time.Millisecond)
	plEventually(t, "meter read", func() bool { return l.count("get_level STRENGTH") == 1 })
	st := p.State()
	if st.Meters["STRENGTH"] != -12 || st.Levels["RFPOWER"] != 0.5 {
		t.Fatalf("meters %v levels %v", st.Meters, st.Levels)
	}
	if l.count("get_level SWR") != 0 || l.count("get_level ALC") != 0 {
		t.Fatal("SWR/ALC read while PTT is off")
	}
}

func TestPollerSWRAndALCOnlyWhilePTT(t *testing.T) {
	l := newPlLink()
	l.set("get_level STRENGTH", "Level Value: -12")
	l.set("get_level RFPOWER", "Level Value: 0.5")
	l.set("get_level SWR", "Level Value: 1.5")
	l.set("get_level ALC", "Level Value: 0.2")
	p, clk := startPoller(t, l, meterCaps())
	p.SetInterest(Page)
	plEventually(t, "first read", func() bool { return l.count("get_freq") == 1 })
	clk.Advance(250 * time.Millisecond)
	plEventually(t, "second read", func() bool { return l.count("get_freq") == 2 })
	if l.count("get_level SWR") != 0 {
		t.Fatal("SWR read with PTT off")
	}

	l.set("get_ptt", "PTT: 1")
	clk.Advance(250 * time.Millisecond)
	plEventually(t, "PTT seen", func() bool { return p.State().PTT })
	clk.Advance(200 * time.Millisecond)
	plEventually(t, "SWR and ALC read", func() bool {
		return l.count("get_level SWR") >= 1 && l.count("get_level ALC") >= 1
	})
	if st := p.State(); st.Meters["SWR"] != 1.5 || st.Meters["ALC"] != 0.2 {
		t.Fatalf("meters %v", st.Meters)
	}

	l.set("get_ptt", "PTT: 0")
	clk.Advance(250 * time.Millisecond)
	plEventually(t, "zero frame", func() bool {
		st := p.State()
		v, ok := st.Meters["SWR"]
		return !st.PTT && ok && v == 0 && st.Meters["ALC"] == 0
	})
	n := l.count("get_level SWR")
	clk.Advance(time.Second)
	settle()
	clk.Advance(time.Second)
	settle()
	if l.count("get_level SWR") != n {
		t.Fatal("SWR kept being read after PTT dropped")
	}
}

func TestPollerCorrectsOptimisticValue(t *testing.T) {
	l := newPlLink()
	l.set("get_freq", "Frequency: 7074100")
	p, clk := startPoller(t, l, Caps{})
	p.SetInterest(Page)
	plEventually(t, "first read", func() bool { return p.State().Freq == 7074100 })
	l.set("get_freq", "Frequency: 7074100")
	var seen State
	err := p.Write(context.Background(), func(*Client) error {
		seen = p.State()
		return nil
	}, func(s *State) { s.Freq = 7074000 })
	if err != nil {
		t.Fatal(err)
	}
	if seen.Freq != 7074000 || !seen.Optimistic["freq"] {
		t.Fatalf("optimistic state not applied before the write: %+v", seen)
	}
	clk.Advance(250 * time.Millisecond)
	plEventually(t, "correction", func() bool {
		st := p.State()
		return st.Freq == 7074100 && !st.Optimistic["freq"]
	})
}

func TestPollerWriteRevertsOnFailure(t *testing.T) {
	l := newPlLink()
	p, _ := startPoller(t, l, Caps{})
	plEventually(t, "first read", func() bool { return p.State().Freq == 14074000 })
	boom := errors.New("boom")
	err := p.Write(context.Background(), func(*Client) error { return boom }, func(s *State) { s.Freq = 1 })
	if !errors.Is(err, boom) {
		t.Fatalf("err %v", err)
	}
	if st := p.State(); st.Freq != 14074000 || len(st.Optimistic) != 0 {
		t.Fatalf("not reverted: %+v", st)
	}
}

func TestPollerNoReadWhileWritePending(t *testing.T) {
	l := newPlLink()
	p, clk := startPoller(t, l, Caps{})
	p.SetInterest(Page)
	plEventually(t, "first read", func() bool { return l.count("get_freq") >= 1 })
	settle()
	inWrite, release, finished := make(chan int), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		_ = p.Write(context.Background(), func(*Client) error {
			inWrite <- l.count("get_freq")
			<-release
			return nil
		}, nil)
	}()
	before := <-inWrite
	clk.Advance(time.Second)
	settle()
	if n := l.count("get_freq"); n != before {
		t.Fatalf("a read started during a pending write: %d -> %d", before, n)
	}
	close(release)
	<-finished
	plEventually(t, "read after the write", func() bool { return l.count("get_freq") > before })
}

func TestPollerDisconnectedState(t *testing.T) {
	l := newPlLink()
	p, clk := startPoller(t, l, Caps{})
	plEventually(t, "first read", func() bool { return p.State().Freq == 14074000 })
	frozen := p.State().UpdatedAt
	if frozen.IsZero() {
		t.Fatal("UpdatedAt not set")
	}
	clk.Advance(time.Second)
	l.setUp(false)
	plEventually(t, "disconnected", func() bool { return !p.State().Connected })
	n := len(l.calls)
	clk.Advance(30 * time.Second)
	settle()
	st := p.State()
	if st.Freq != 14074000 || st.Mode != "USB" || !st.UpdatedAt.Equal(frozen) {
		t.Fatalf("values or UpdatedAt changed while down: %+v", st)
	}
	l.mu.Lock()
	got := len(l.calls)
	l.mu.Unlock()
	if got != n {
		t.Fatalf("reads attempted while disconnected: %d -> %d", n, got)
	}
	l.setUp(true)
	plEventually(t, "reads resume", func() bool { return p.State().Connected && l.count("get_freq") >= 2 })
}

func TestPollerSubscribeGetsChanges(t *testing.T) {
	l := newPlLink()
	p, _ := startPoller(t, l, Caps{})
	ch := p.Subscribe()
	select {
	case st := <-ch:
		if st.Freq != 14074000 {
			t.Fatalf("freq %d", st.Freq)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no state published")
	}
	p.Unsubscribe(ch)
}
