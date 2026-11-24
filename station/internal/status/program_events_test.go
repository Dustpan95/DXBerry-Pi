package status

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
)

type fakeProgramSource struct {
	mu             sync.Mutex
	sets           map[string]int
	state          map[string][]byte
	subs           map[chan struct{}]struct{}
	beforeSnapshot func(string)
}

func newFakePrograms() *fakeProgramSource {
	return &fakeProgramSource{sets: map[string]int{}, state: map[string][]byte{"a": []byte(`{"value":1}`), "b": []byte(`{"value":9}`)}, subs: map[chan struct{}]struct{}{}}
}

func (p *fakeProgramSource) SetWatch(radio string, level int) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.sets[radio] = level
}

func (p *fakeProgramSource) Snapshot(radio string) ([]byte, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.beforeSnapshot != nil {
		p.beforeSnapshot(radio)
	}
	raw, ok := p.state[radio]
	return append([]byte(nil), raw...), ok
}

func (p *fakeProgramSource) Subscribe() (<-chan struct{}, func()) {
	p.mu.Lock()
	defer p.mu.Unlock()
	ch := make(chan struct{}, 1)
	p.subs[ch] = struct{}{}
	return ch, func() { p.mu.Lock(); delete(p.subs, ch); p.mu.Unlock() }
}

func (p *fakeProgramSource) push(radio, raw string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.state[radio] = []byte(raw)
	for ch := range p.subs {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

func (p *fakeProgramSource) interest(radio string) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.sets[radio]
}

func TestProgramEventsUseWatchInterestWithoutARigLayer(t *testing.T) {
	r := newRig(t)
	p := newFakePrograms()
	r.st.SetRadios(fakeRadios{names: []string{"a", "b"}})
	r.st.SetPrograms(p)
	ts := serve(t, r.st, true)
	watcher, bystander := openSock(t, ts), openSock(t, ts)
	watchMsg(t, watcher, "a", "page")
	m := readMsg(t, watcher)
	if string(m["type"]) != `"program"` || string(m["program"]) != `"wsjtx"` || string(m["radio"]) != `"a"` || string(m["data"]) != `{"value":1}` {
		t.Fatalf("invalid initial program event: %v", m)
	}
	p.push("b", `{"value":10}`)
	p.push("a", `{"value":2}`)
	m = readMsg(t, watcher)
	if string(m["radio"]) != `"a"` || string(m["data"]) != `{"value":2}` {
		t.Fatalf("program event crossed radio watches: %v", m)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, _, err := bystander.Read(ctx); err == nil {
		t.Fatal("unwatched socket received program data")
	}
	watchMsg(t, watcher, "a", "none")
	eventually(t, "program watch removed", func() bool { return p.interest("a") == 0 })
	_ = watcher.CloseNow()
	eventually(t, "program subscribers removed", func() bool { p.mu.Lock(); defer p.mu.Unlock(); return len(p.subs) == 0 })
}

func TestProgramEventsAggregateAcrossSocketsAndRejectUnknownWatches(t *testing.T) {
	r := newRig(t)
	p := newFakePrograms()
	r.st.SetRadios(fakeRadios{names: []string{"a"}})
	r.st.SetPrograms(p)
	ts := serve(t, r.st, true)
	one, two := openSock(t, ts), openSock(t, ts)
	watchMsg(t, one, "missing", "page")
	watchMsg(t, one, "a", "dashboard")
	readMsg(t, one)
	watchMsg(t, two, "a", "page")
	readMsg(t, two)
	eventually(t, "program page interest", func() bool { return p.interest("a") == 2 })
	_ = two.CloseNow()
	eventually(t, "remaining program interest", func() bool { return p.interest("a") == 1 })
	p.mu.Lock()
	_, unknown := p.sets["missing"]
	p.mu.Unlock()
	if unknown {
		t.Fatal("unknown radio reached program source")
	}
	_ = one.CloseNow()
	eventually(t, "program interest released", func() bool { return p.interest("a") == 0 })
}

func TestProgramEventEnvelopeRejectsInvalidAndOversizedData(t *testing.T) {
	for _, raw := range []string{"{invalid", strings.Repeat(" ", (256<<10)+1)} {
		if programMessage("a", []byte(raw)) != nil {
			t.Fatal("invalid or oversized event accepted")
		}
	}
	raw := programMessage("a", []byte(`{"status":null}`))
	var m map[string]json.RawMessage
	if json.Unmarshal(raw, &m) != nil || string(m["data"]) != `{"status":null}` {
		t.Fatal("nullable program state changed", string(raw))
	}
	var sent time.Time
	if json.Unmarshal(m["sent_at"], &sent) != nil || sent.IsZero() {
		t.Fatal("program event omitted its server time")
	}
}

func TestProgramEventsCoalesceWithoutRepeatingUnchangedSnapshots(t *testing.T) {
	r := newRig(t)
	p := newFakePrograms()
	r.st.SetRadios(fakeRadios{names: []string{"a", "b"}})
	r.st.SetPrograms(p)
	ts := serve(t, r.st, true)
	c := openSock(t, ts)
	watchMsg(t, c, "a", "page")
	readMsg(t, c)
	p.push("b", `{"value":11}`)
	p.push("a", `{"value":1}`)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if _, _, err := c.Read(ctx); err == nil {
		t.Fatal("unchanged or unwatched data generated a frame")
	}
}

func TestProgramEventBatchRechecksLoginBetweenFrames(t *testing.T) {
	r := newRig(t)
	p := newFakePrograms()
	var alive, armed atomic.Bool
	alive.Store(true)
	p.beforeSnapshot = func(radio string) {
		if radio == "b" && armed.Load() {
			alive.Store(false)
		}
	}
	r.st.SetRadios(fakeRadios{names: []string{"a", "b"}})
	r.st.SetPrograms(p)
	ts := serveHandler(t, r.st.eventsHandler(func(*http.Request) bool { return alive.Load() }, time.Hour))
	c := openSock(t, ts)
	for _, name := range []string{"a", "b"} {
		watchMsg(t, c, name, "page")
		readMsg(t, c)
	}
	armed.Store(true)
	p.mu.Lock()
	p.state["b"] = []byte(`{"value":12}`)
	p.mu.Unlock()
	p.push("a", `{"value":2}`)
	if m := readMsg(t, c); string(m["radio"]) != `"a"` {
		t.Fatal("expected first permitted frame", m)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, _, err := c.Read(ctx)
	if websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatal("later batch frame passed a revoked login", err)
	}
}
