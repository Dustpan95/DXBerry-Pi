package rig

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
)

type mgLister struct {
	mu   sync.Mutex
	list []radios.Radio
	ch   chan []radios.Radio
}

func newMgLister(l ...radios.Radio) *mgLister {
	return &mgLister{list: l, ch: make(chan []radios.Radio, 4)}
}

func (l *mgLister) List() []radios.Radio {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]radios.Radio{}, l.list...)
}
func (l *mgLister) Subscribe() <-chan []radios.Radio  { return l.ch }
func (l *mgLister) Unsubscribe(<-chan []radios.Radio) {}
func (l *mgLister) push(list ...radios.Radio) {
	l.mu.Lock()
	l.list = list
	l.mu.Unlock()
	l.ch <- list
}

// mgConn is a plLink that remembers it was closed.
type mgConn struct {
	*plLink
	addr   string
	closed chan struct{}
	once   sync.Once
}

func (c *mgConn) Close() { c.once.Do(func() { close(c.closed) }) }

func (c *mgConn) isClosed() bool {
	select {
	case <-c.closed:
		return true
	default:
		return false
	}
}

type mgRig struct {
	t     *testing.T
	m     *Manager
	clk   *plClock
	lst   *mgLister
	cache CapsCache
	mu    sync.Mutex
	conns []*mgConn
	tune  func(*plLink)
}

func (r *mgRig) all() []*mgConn {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]*mgConn{}, r.conns...)
}

func newMgRig(t *testing.T, radiosIn ...radios.Radio) *mgRig {
	t.Helper()
	r := &mgRig{t: t, clk: newPlClock(), lst: newMgLister(radiosIn...), cache: CapsCache{Dir: filepath.Join(t.TempDir(), "rigcaps")}}
	r.m = NewManager(r.lst, r.cache, r.clk, slog.New(slog.NewTextHandler(io.Discard, nil)))
	r.m.Dialer = func(addr string) Conn {
		l := newPlLink()
		l.reply["dump_caps"] = fixture(t, "3073")
		if r.tune != nil {
			r.tune(l)
		}
		c := &mgConn{plLink: l, addr: addr, closed: make(chan struct{})}
		r.mu.Lock()
		r.conns = append(r.conns, c)
		r.mu.Unlock()
		return c
	}
	return r
}

func (r *mgRig) run() {
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.m.Run(ctx); close(done) }()
	r.t.Cleanup(func() { cancel(); <-done })
}

func radio(name string, port, model int) radios.Radio {
	return radios.Radio{Name: name, Label: name, Port: port, Model: model}
}

func TestManagerStartsOnePollerPerRadio(t *testing.T) {
	r := newMgRig(t, radio("hf", 4532, 3073), radio("vhf", 4533, 1))
	r.run()
	plEventually(t, "both pollers", func() bool {
		_, a := r.m.Radio("hf")
		_, b := r.m.Radio("vhf")
		return a && b
	})
	addrs := map[string]bool{}
	for _, c := range r.all() {
		addrs[c.addr] = true
	}
	if !addrs["127.0.0.1:4532"] || !addrs["127.0.0.1:4533"] || len(addrs) != 2 {
		t.Fatalf("dialled %v", addrs)
	}
	plEventually(t, "caps for hf", func() bool { _, ok := r.m.Caps("hf"); return ok })
	if _, ok := r.cache.Load("3073"); !ok {
		t.Fatal("caps not stored under the model number")
	}
	if _, ok := r.m.Client("hf"); ok {
		t.Fatal("a fake connection is not a *Client")
	}
	p, _ := r.m.Radio("hf")
	plEventually(t, "poller has caps", func() bool { return p.State().Caps != nil })
	plEventually(t, "poller reads", func() bool { return p.State().Freq == 14074000 })
}

func TestManagerUsesCachedCaps(t *testing.T) {
	r := newMgRig(t, radio("hf", 4532, 3073))
	if err := r.cache.Store("3073", Caps{ModelName: "Cached", Probed: time.Now()}); err != nil {
		t.Fatal(err)
	}
	r.run()
	plEventually(t, "cached caps", func() bool { c, ok := r.m.Caps("hf"); return ok && c.ModelName == "Cached" })
	settle()
	for _, c := range r.all() {
		if c.count("dump_caps") != 0 {
			t.Fatal("discovered although the cache had the model")
		}
	}
}

func TestManagerRetriesDiscoveryWithBackoff(t *testing.T) {
	r := newMgRig(t, radio("hf", 4532, 3073))
	r.tune = func(l *plLink) { l.errs["dump_caps"] = ErrDeadline }
	r.run()
	plEventually(t, "first attempt", func() bool { c := r.all(); return len(c) == 1 && c[0].count("dump_caps") == 1 })
	c := r.all()[0]
	if _, ok := r.m.Caps("hf"); ok {
		t.Fatal("caps from a failed discovery")
	}
	c.mu.Lock()
	delete(c.errs, "dump_caps")
	c.mu.Unlock()
	r.clk.Advance(time.Second)
	plEventually(t, "retry succeeds", func() bool { _, ok := r.m.Caps("hf"); return ok })
	if n := c.count("dump_caps"); n != 2 {
		t.Fatalf("dump_caps called %d times", n)
	}
}

func TestManagerRemovalClosesPollerAndClient(t *testing.T) {
	r := newMgRig(t, radio("hf", 4532, 3073))
	r.run()
	plEventually(t, "poller", func() bool { _, ok := r.m.Radio("hf"); return ok })
	r.lst.push()
	plEventually(t, "removed", func() bool { _, ok := r.m.Radio("hf"); return !ok })
	plEventually(t, "client closed", func() bool { return r.all()[0].isClosed() })
}

func TestManagerRecreatesOnPortOrModelChange(t *testing.T) {
	r := newMgRig(t, radio("hf", 4532, 3073))
	r.run()
	plEventually(t, "poller", func() bool { _, ok := r.m.Radio("hf"); return ok })
	first, _ := r.m.Radio("hf")

	relabelled := radio("hf", 4532, 3073)
	relabelled.Label = "new label"
	r.lst.push(relabelled)
	settle()
	if p, _ := r.m.Radio("hf"); p != first || len(r.all()) != 1 {
		t.Fatal("a label change recreated the poller")
	}

	r.lst.push(radio("hf", 4600, 3073))
	plEventually(t, "new port", func() bool { c := r.all(); return len(c) == 2 && c[1].addr == "127.0.0.1:4600" })
	plEventually(t, "old client closed", func() bool { return r.all()[0].isClosed() })
	r.lst.push(radio("hf", 4600, 1036))
	plEventually(t, "new model", func() bool { return len(r.all()) == 3 })
	if p, _ := r.m.Radio("hf"); p == first {
		t.Fatal("same poller after changes")
	}
	plEventually(t, "caps for the new model", func() bool { _, ok := r.cache.Load("1036"); return ok })
}

func TestManagerInterestBeforeAndAfterStart(t *testing.T) {
	r := newMgRig(t, radio("hf", 4532, 3073))
	r.m.SetInterest("hf", Page) // before the poller exists
	r.run()
	plEventually(t, "first read", func() bool { c := r.all(); return len(c) == 1 && c[0].count("get_freq") == 1 })
	c := r.all()[0]
	r.clk.Advance(250 * time.Millisecond)
	plEventually(t, "page cadence", func() bool { return c.count("get_freq") == 2 })
	r.m.SetWatch("hf", 0)
	r.clk.Advance(250 * time.Millisecond)
	settle()
	if c.count("get_freq") != 2 {
		t.Fatal("still polling at the page rate after SetWatch(0)")
	}
}

func TestManagerBroadcastsDeltas(t *testing.T) {
	r := newMgRig(t, radio("hf", 4532, 3073))
	r.run()
	ups, cancel := r.m.Subscribe()
	defer cancel()
	var got Update
	select {
	case got = <-ups:
	case <-time.After(3 * time.Second):
		t.Fatal("no update")
	}
	var st struct {
		Freq int64
		Caps *Caps
	}
	if err := json.Unmarshal(got.Data, &st); err != nil || got.Radio != "hf" {
		t.Fatalf("update %+v err %v", got, err)
	}
	if st.Freq != 14074000 || st.Caps != nil {
		t.Fatalf("data %s", got.Data)
	}
	if snap, ok := r.m.Snapshot("hf"); !ok || len(snap) == 0 {
		t.Fatal("no snapshot")
	}
	if _, ok := r.m.Snapshot("nope"); ok {
		t.Fatal("snapshot of an unknown radio")
	}
	r.all()[0].setUp(false)
	deadline := time.After(3 * time.Second)
	for {
		select {
		case u := <-ups:
			if u.Link {
				return
			}
		case <-deadline:
			t.Fatal("no link-change update")
		}
	}
}
