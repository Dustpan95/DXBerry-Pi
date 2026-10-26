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
	block map[string]chan struct{} // a call with this key waits for the channel to close
	enter chan string              // receives the key of a blocked call when it starts waiting
}

func newPlLink() *plLink {
	l := &plLink{up: true, reply: map[string][]string{}, errs: map[string]error{},
		block: map[string]chan struct{}{}, enter: make(chan string, 64)}
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
	l.calls = append(l.calls, key)
	gate := l.block[key]
	l.mu.Unlock()
	if gate != nil {
		l.enter <- key
		<-gate
	}
	l.mu.Lock()
	defer l.mu.Unlock()
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

// blockOn makes calls with key wait until the returned func is called.
func (l *plLink) blockOn(key string) (release func()) {
	g := make(chan struct{})
	l.mu.Lock()
	l.block[key] = g
	l.mu.Unlock()
	var once sync.Once
	return func() {
		l.mu.Lock()
		delete(l.block, key)
		l.mu.Unlock()
		once.Do(func() { close(g) })
	}
}

func (l *plLink) waitEntered(t *testing.T, key string) {
	t.Helper()
	select {
	case k := <-l.enter:
		if k != key {
			t.Fatalf("blocked on %q, want %q", k, key)
		}
	case <-time.After(3 * time.Second):
		t.Fatalf("no call to %q", key)
	}
}

func (l *plLink) callLog() []string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]string{}, l.calls...)
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
	plEventually(t, "state", func() bool {
		st := p.State()
		return st.Freq == 14074000 && st.Mode == "USB" && st.Passband == "2400" && st.VFO == "VFOA" && st.Connected
	})
}

func meterCaps() Caps {
	return Caps{
		ModelName: "Test", Probed: time.Now(), CanGetVFO: true, CanGetPTT: true, CanGetSplit: true,
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
	plEventually(t, "meter state", func() bool {
		st := p.State()
		return st.Meters["STRENGTH"] == -12 && st.Levels["RFPOWER"] == 0.5
	})
	if l.count("get_level SWR") != 0 || l.count("get_level ALC") != 0 {
		t.Fatal("SWR/ALC read while PTT is off")
	}
}

func txLink() *plLink {
	l := newPlLink()
	l.set("get_level STRENGTH", "Level Value: -12")
	l.set("get_level RFPOWER", "Level Value: 0.5")
	l.set("get_level SWR", "Level Value: 1.5")
	l.set("get_level ALC", "Level Value: 0.2")
	return l
}

// transmitting brings a Page poller to the state of a key-down with SWR/ALC read once.
func transmitting(t *testing.T, l *plLink) (*Poller, *plClock) {
	t.Helper()
	p, clk := startPoller(t, l, meterCaps())
	p.SetInterest(Page)
	plEventually(t, "first read", func() bool { return p.State().Freq == 14074000 })
	l.set("get_ptt", "PTT: 1")
	clk.Advance(250 * time.Millisecond)
	plEventually(t, "PTT seen", func() bool { return p.State().PTT })
	clk.Advance(200 * time.Millisecond)
	plEventually(t, "SWR and ALC", func() bool {
		st := p.State()
		return st.Meters["SWR"] == 1.5 && st.Meters["ALC"] == 0.2
	})
	return p, clk
}

func TestPollerSWRAndALCOnlyWhilePTT(t *testing.T) {
	l := txLink()
	p, clk := startPoller(t, l, meterCaps())
	p.SetInterest(Page)
	var hist struct {
		sync.Mutex
		states []State
	}
	ch := p.Subscribe()
	go func() {
		for st := range ch {
			hist.Lock()
			hist.states = append(hist.states, st)
			hist.Unlock()
		}
	}()
	plEventually(t, "first read", func() bool { return p.State().Freq == 14074000 })
	clk.Advance(250 * time.Millisecond)
	settle()
	if l.count("get_level SWR") != 0 {
		t.Fatal("SWR read with PTT off")
	}

	l.set("get_ptt", "PTT: 1")
	clk.Advance(250 * time.Millisecond)
	plEventually(t, "PTT seen", func() bool { return p.State().PTT })
	base := l.count("get_level SWR")
	for i := 1; i <= 5; i++ { // one simulated second at the 200 ms cadence
		clk.Advance(200 * time.Millisecond)
		want := base + i
		plEventually(t, "SWR read", func() bool { return l.count("get_level SWR") >= want })
	}
	settle()
	if n := l.count("get_level SWR"); n != base+5 {
		t.Fatalf("SWR read %d times in 1 s, want 5", n-base)
	}
	if l.count("get_level ALC") != l.count("get_level SWR") {
		t.Fatal("ALC and SWR counts differ")
	}
	plEventually(t, "meters", func() bool { st := p.State(); return st.Meters["SWR"] == 1.5 && st.Meters["ALC"] == 0.2 })

	l.set("get_ptt", "PTT: 0")
	clk.Advance(200 * time.Millisecond)
	plEventually(t, "zero state", func() bool {
		st := p.State()
		return !st.PTT && st.Meters["SWR"] == 0 && st.Meters["ALC"] == 0
	})
	// the zero frame was published as a State, after a transmitting one
	hist.Lock()
	sawTx, sawZero := false, false
	for _, st := range hist.states {
		if st.Meters["SWR"] == 1.5 {
			sawTx = true
		}
		if sawTx && !st.PTT && st.Meters["SWR"] == 0 && st.Meters["ALC"] == 0 {
			sawZero = true
		}
	}
	hist.Unlock()
	if !sawTx || !sawZero {
		t.Fatalf("published states lack the transmit frame or the zero frame (tx %v zero %v)", sawTx, sawZero)
	}
	n := l.count("get_level SWR")
	for i := 0; i < 3; i++ {
		clk.Advance(time.Second)
		settle()
	}
	if l.count("get_level SWR") != n {
		t.Fatal("SWR kept being read after PTT dropped")
	}
}

// An unkey from the page races an in-flight SWR read: the read is discarded,
// yet the next PTT reading must still zero the meters.
func TestPollerZeroesMetersAfterUnkeyRacingARead(t *testing.T) {
	l := txLink()
	p, clk := transmitting(t, l)
	release := l.blockOn("get_level SWR")
	defer release()
	clk.Advance(200 * time.Millisecond)
	l.waitEntered(t, "get_level SWR")
	l.set("get_ptt", "PTT: 0")
	wrote := make(chan error, 1)
	go func() {
		wrote <- p.Write(context.Background(), func(*Client) error { return nil }, func(s *State) { s.PTT = false })
	}()
	plEventually(t, "write started", func() bool { return !p.State().PTT })
	release()
	if err := <-wrote; err != nil {
		t.Fatal(err)
	}
	plEventually(t, "meters zeroed", func() bool {
		st := p.State()
		return st.Meters["SWR"] == 0 && st.Meters["ALC"] == 0 && !st.PTT && !st.Optimistic["ptt"]
	})
}

func TestPollerZeroesMetersWhenDashboardReadsPTTOff(t *testing.T) {
	l := txLink()
	p, clk := transmitting(t, l)
	p.SetInterest(Dashboard) // the page closed while transmitting
	l.set("get_ptt", "PTT: 0")
	clk.Advance(2 * time.Second)
	plEventually(t, "meters zeroed", func() bool {
		st := p.State()
		return !st.PTT && st.Meters["SWR"] == 0 && st.Meters["ALC"] == 0
	})
}

func TestPollerSkipsNonFiniteMeters(t *testing.T) {
	l := txLink()
	l.set("get_level STRENGTH", "Level Value: nan")
	l.set("get_level RFPOWER", "Level Value: +Inf")
	p, _ := startPoller(t, l, meterCaps())
	p.SetInterest(Page)
	plEventually(t, "meters read", func() bool { return l.count("get_level RFPOWER") >= 1 && p.State().Freq != 0 })
	settle()
	st := p.State()
	if _, ok := st.Meters["STRENGTH"]; ok {
		t.Fatal("NaN stored")
	}
	if _, ok := st.Levels["RFPOWER"]; ok {
		t.Fatal("Inf stored")
	}
	if b := encodeState(st); !strings.Contains(string(b), `"freq":14074000`) {
		t.Fatalf("frame blanked: %s", b)
	}
}

func TestPollerGatesReadsOnKnownCaps(t *testing.T) {
	l := newPlLink()
	startPoller(t, l, Caps{ModelName: "Test", Probed: time.Now()}) // can get nothing optional
	plEventually(t, "first read", func() bool { return l.count("get_freq") == 1 && l.count("get_mode") == 1 })
	settle()
	for _, c := range []string{"get_vfo", "get_ptt", "get_split_vfo"} {
		if l.count(c) != 0 {
			t.Errorf("%s read though the caps say it cannot be", c)
		}
	}
	l2 := newPlLink()
	startPoller(t, l2, meterCaps()) // can get all
	plEventually(t, "all reads", func() bool {
		return l2.count("get_vfo") == 1 && l2.count("get_ptt") == 1 && l2.count("get_split_vfo") == 1
	})
	l3 := newPlLink()
	startPoller(t, l3, Caps{}) // unknown: everything is tried
	plEventually(t, "unknown caps", func() bool {
		return l3.count("get_vfo") == 1 && l3.count("get_ptt") == 1 && l3.count("get_split_vfo") == 1
	})
}

func TestPollerClearsOptimisticFlagOfUnreadableField(t *testing.T) {
	l := newPlLink()
	p, _ := startPoller(t, l, Caps{ModelName: "Test", Probed: time.Now()})
	plEventually(t, "first read", func() bool { return p.State().Freq == 14074000 })
	if err := p.Write(context.Background(), func(*Client) error { return nil }, func(s *State) { s.PTT, s.Freq = true, 1 }); err != nil {
		t.Fatal(err)
	}
	st := p.State()
	if st.Optimistic["ptt"] {
		t.Fatal("flag kept for a field no read can confirm")
	}
	if !st.PTT {
		t.Fatal("value dropped")
	}
	if !st.Optimistic["freq"] && st.Freq == 1 {
		t.Fatal("freq is readable: its flag stays until a read")
	}
}

func TestPollerCorrectsOptimisticValue(t *testing.T) {
	l := newPlLink() // the rig starts at 14074000
	p, clk := startPoller(t, l, Caps{})
	p.SetInterest(Page)
	plEventually(t, "first read", func() bool { return p.State().Freq == 14074000 })
	var seen State
	err := p.Write(context.Background(), func(*Client) error {
		seen = p.State()
		l.set("get_freq", "Frequency: 7074100") // the rig lands slightly off
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

// A write issued while a read is in flight runs before the poller's next read.
func TestPollerWriteGoesAheadOfQueuedReads(t *testing.T) {
	l := newPlLink()
	p, clk := startPoller(t, l, Caps{})
	p.SetInterest(Page)
	plEventually(t, "first read", func() bool { return p.State().Freq == 14074000 })
	settle()
	release := l.blockOn("get_mode")
	defer release()
	clk.Advance(250 * time.Millisecond)
	l.waitEntered(t, "get_mode")
	blocked := len(l.callLog()) // index the next call will take
	wrote := make(chan error, 1)
	go func() {
		wrote <- p.Write(context.Background(), func(*Client) error {
			l.mu.Lock()
			l.calls = append(l.calls, "WRITE")
			l.mu.Unlock()
			return nil
		}, nil)
	}()
	plEventually(t, "write ran", func() bool { return l.count("WRITE") == 1 })
	release()
	if err := <-wrote; err != nil {
		t.Fatal(err)
	}
	plEventually(t, "reads resume", func() bool { return l.count("get_freq") >= 3 })
	log := l.callLog()
	if log[blocked-1] != "get_mode" || log[blocked] != "WRITE" {
		t.Fatalf("a read ran between the in-flight read and the write: %v", log[blocked-1:])
	}
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
	n := len(l.callLog())
	clk.Advance(30 * time.Second)
	settle()
	st := p.State()
	if st.Freq != 14074000 || st.Mode != "USB" || !st.UpdatedAt.Equal(frozen) {
		t.Fatalf("values or UpdatedAt changed while down: %+v", st)
	}
	if got := len(l.callLog()); got != n {
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

// A radio that does not answer behind a running rigctld (every read an error) is "not answering"
// after two cycles with no successful read, and answering again at the first success.
func TestPollerAnsweringFollowsReads(t *testing.T) {
	l := newPlLink()
	saved := l.reply
	l.reply = map[string][]string{} // every read: RPRT -1, quickly
	p, clk := startPoller(t, l, Caps{})
	if !p.State().Answering {
		t.Fatal("a new poller starts as answering")
	}
	ch := p.Subscribe()
	plEventually(t, "first cycle", func() bool { return l.count("get_split_vfo") == 1 })
	settle()
	if !p.State().Answering {
		t.Fatal("not answering after one cycle")
	}
	clk.Advance(10 * time.Second)
	plEventually(t, "second cycle: not answering", func() bool { return !p.State().Answering })
	select {
	case st := <-ch:
		if st.Answering {
			t.Fatal("the change was not published")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("nothing published")
	}
	if b := encodeState(p.State()); !strings.Contains(string(b), `"answering":false`) {
		t.Fatalf("wire form %s", b)
	}
	l.mu.Lock()
	l.reply = saved
	l.mu.Unlock()
	clk.Advance(10 * time.Second)
	plEventually(t, "answering again", func() bool { return p.State().Answering })
	// transport failures count too (a reply that never comes)
	l.mu.Lock()
	l.errs["get_freq"] = ErrDeadline
	l.mu.Unlock()
	clk.Advance(10 * time.Second)
	plEventually(t, "third failed get_freq", func() bool { return l.count("get_freq") == 4 })
	settle()
	if !p.State().Answering {
		t.Fatal("not answering after one failed cycle")
	}
	clk.Advance(10 * time.Second)
	plEventually(t, "not answering on deadlines", func() bool { return !p.State().Answering })
}
