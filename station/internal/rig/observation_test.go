package rig

import (
	"context"
	"errors"
	"fmt"
	"net"
	"reflect"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type observationLink struct {
	*plLink
	generation atomic.Uint64
	after      func(string, int)
	count      int
	status     int
}

func (l *observationLink) ConnectionGeneration() (uint64, bool) {
	return l.generation.Load(), l.Connected()
}

func (l *observationLink) Do(ctx context.Context, cmd string, args ...string) (Reply, error) {
	r, err := l.plLink.Do(ctx, cmd, args...)
	r.RPRT = l.status
	l.count++
	if l.after != nil {
		l.after(cmd, l.count)
	}
	return r, err
}

func observationCaps() Caps {
	return Caps{ModelName: "fixture", Probed: time.Unix(1, 0), CanGetPTT: true, CanGetVFO: true, VFOs: []string{"VFOA", "VFOB", "MEM", "currVFO"}}
}

func observationFixture() (*Poller, *observationLink, *plClock) {
	l := &observationLink{plLink: newPlLink()}
	l.generation.Store(1)
	c := newPlClock()
	return newPoller(l, observationCaps(), c, nil), l, c
}

func TestObservationFreshReadIgnoresDisplayCache(t *testing.T) {
	p, l, c := observationFixture()
	p.mu.Lock()
	p.st.Freq = 1
	p.st.VFO = "VFOB"
	p.st.PTT = true
	p.st.Answering = false
	p.st.Optimistic = map[string]bool{"freq": true, "ptt": true, "vfo": true}
	p.mu.Unlock()
	before := p.State()
	ch := p.Subscribe()
	defer p.Unsubscribe(ch)
	o, err := p.Observe(context.Background())
	if err != nil || o.FrequencyHz() != 14074000 || o.VFO() != "VFOA" || !o.StartedAt().Equal(c.Now()) || !p.ObservationCurrent(o) {
		t.Fatalf("%+v: %v", o, err)
	}
	if !reflect.DeepEqual(before, p.State()) {
		t.Fatal("on-demand read changed the display cache")
	}
	select {
	case <-ch:
		t.Fatal("on-demand read published a display update")
	default:
	}
	if !slices.Equal(l.callLog(), []string{"get_ptt", "get_vfo", "get_freq", "get_freq", "get_vfo", "get_ptt"}) {
		t.Fatal(l.callLog())
	}
}

func TestObservationUnavailableInputs(t *testing.T) {
	tests := []struct {
		name  string
		setup func(*Poller, *observationLink)
		want  error
	}{
		{"unknown caps", func(p *Poller, l *observationLink) { p.SetCaps(Caps{}) }, ErrObservationUnsupported},
		{"PTT unsupported", func(p *Poller, l *observationLink) { c := observationCaps(); c.CanGetPTT = false; p.SetCaps(c) }, ErrObservationUnsupported},
		{"VFO unsupported", func(p *Poller, l *observationLink) { c := observationCaps(); c.CanGetVFO = false; p.SetCaps(c) }, ErrObservationUnsupported},
		{"no VFO list", func(p *Poller, l *observationLink) { c := observationCaps(); c.VFOs = nil; p.SetCaps(c) }, ErrObservationUnsupported},
		{"disconnected", func(p *Poller, l *observationLink) { l.setUp(false) }, ErrObservationUnavailable},
		{"station keyed", func(p *Poller, l *observationLink) { p.SetTransmitting(true) }, ErrObservationTransmitting},
		{"write pending", func(p *Poller, l *observationLink) { p.pending.Add(1) }, ErrObservationChanged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, l, _ := observationFixture()
			tt.setup(p, l)
			o, err := p.Observe(context.Background())
			if !errors.Is(err, tt.want) || o != (Observation{}) || len(l.callLog()) != 0 {
				t.Fatal(o, err, l.callLog())
			}
		})
	}
	p := newPoller(newPlLink(), observationCaps(), newPlClock(), nil)
	if _, err := p.Observe(context.Background()); !errors.Is(err, ErrObservationUnsupported) {
		t.Fatal("link without synchronous generation accepted", err)
	}
}

func TestObservationMalformedOrTransmitReplies(t *testing.T) {
	tests := []struct {
		cmd   string
		lines []string
		want  error
	}{
		{"get_ptt", nil, ErrObservationUnavailable},
		{"get_ptt", []string{"PTT: 0", "PTT: 1"}, ErrObservationUnavailable},
		{"get_ptt", []string{"PTT: false"}, ErrObservationUnavailable},
		{"get_ptt", []string{"PTT: -1"}, ErrObservationUnavailable},
		{"get_ptt", []string{"PTT: 1"}, ErrObservationTransmitting},
		{"get_ptt", []string{"PTT: 2"}, ErrObservationTransmitting},
		{"get_ptt", []string{"PTT: 3"}, ErrObservationTransmitting},
		{"get_freq", []string{"Other: 14074000"}, ErrObservationUnavailable},
		{"get_freq", []string{"Frequency: 14074000", "Frequency: 1"}, ErrObservationUnavailable},
		{"get_freq", []string{"Frequency: NaN"}, ErrObservationUnavailable},
		{"get_freq", []string{"Frequency: 14e6"}, ErrObservationUnavailable},
		{"get_freq", []string{"Frequency: 0"}, ErrObservationUnavailable},
		{"get_freq", []string{"Frequency: -1"}, ErrObservationUnavailable},
		{"get_freq", []string{"Frequency: 9007199254740992"}, ErrObservationUnavailable},
		{"get_freq", []string{"Frequency: 99999999999999999999999"}, ErrObservationUnavailable},
		{"get_vfo", []string{"VFO: UNKNOWN"}, ErrObservationUnavailable},
		{"get_vfo", []string{"VFO: MEM"}, ErrObservationUnsupported},
		{"get_vfo", []string{"VFO: currVFO"}, ErrObservationUnsupported},
	}
	for i, tt := range tests {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			p, l, _ := observationFixture()
			l.set(tt.cmd, tt.lines...)
			o, err := p.Observe(context.Background())
			if !errors.Is(err, tt.want) || o != (Observation{}) {
				t.Fatal(o, err)
			}
		})
	}
	p, l, _ := observationFixture()
	l.errs["get_freq"] = &RigError{Code: -1}
	if _, err := p.Observe(context.Background()); !errors.Is(err, ErrObservationUnavailable) {
		t.Fatal(err)
	}
	p, l, _ = observationFixture()
	l.set("get_ptt", "0")
	l.set("get_vfo", "VFOA")
	l.set("get_freq", "9007199254740991")
	if o, err := p.Observe(context.Background()); err != nil || o.FrequencyHz() != 1<<53-1 {
		t.Fatal(o, err)
	}
}

func TestObservationRejectsTransitionsDuringReads(t *testing.T) {
	tests := []struct {
		name   string
		change func(*Poller, *observationLink, *plClock)
		want   error
	}{
		{"frequency drift", func(p *Poller, l *observationLink, c *plClock) { l.set("get_freq", "Frequency: 14075000") }, ErrObservationChanged},
		{"VFO drift", func(p *Poller, l *observationLink, c *plClock) { l.set("get_vfo", "VFO: VFOB") }, ErrObservationChanged},
		{"PTT on", func(p *Poller, l *observationLink, c *plClock) { l.set("get_ptt", "PTT: 1") }, ErrObservationTransmitting},
		{"write", func(p *Poller, l *observationLink, c *plClock) {
			_ = p.Write(context.Background(), func(*Client) error { return nil }, nil)
		}, ErrObservationChanged},
		{"failed write", func(p *Poller, l *observationLink, c *plClock) {
			_ = p.Write(context.Background(), func(*Client) error { return errors.New("write failed") }, nil)
		}, ErrObservationChanged},
		{"reconnect", func(p *Poller, l *observationLink, c *plClock) { l.setUp(false); l.generation.Add(1); l.setUp(true) }, ErrObservationChanged},
		{"silent reconnect generation", func(p *Poller, l *observationLink, c *plClock) { l.generation.Add(1) }, ErrObservationChanged},
		{"caps replaced", func(p *Poller, l *observationLink, c *plClock) { p.SetCaps(observationCaps()) }, ErrObservationChanged},
		{"station PTT ABA", func(p *Poller, l *observationLink, c *plClock) { p.SetTransmitting(true); p.SetTransmitting(false) }, ErrObservationChanged},
		{"expired", func(p *Poller, l *observationLink, c *plClock) { c.Advance(ObservationLifetime) }, ErrObservationChanged},
		{"clock reversed", func(p *Poller, l *observationLink, c *plClock) { c.Advance(-time.Nanosecond) }, ErrObservationChanged},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, l, c := observationFixture()
			l.after = func(cmd string, n int) {
				if n == 3 {
					tt.change(p, l, c)
				}
			}
			o, err := p.Observe(context.Background())
			if !errors.Is(err, tt.want) || o != (Observation{}) {
				t.Fatal(o, err, l.callLog())
			}
		})
	}
}

func TestObservationCurrentRejectsLocalInvalidation(t *testing.T) {
	p, l, c := observationFixture()
	o, err := p.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	other, _, _ := observationFixture()
	if other.ObservationCurrent(o) || p.ObservationCurrent(Observation{}) {
		t.Fatal("foreign/empty observation accepted")
	}
	c.Advance(ObservationLifetime - time.Nanosecond)
	if !p.ObservationCurrent(o) {
		t.Fatal("expired early")
	}
	c.Advance(time.Nanosecond)
	if p.ObservationCurrent(o) {
		t.Fatal("expired observation accepted")
	}
	o, err = p.Observe(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	l.generation.Add(1)
	if p.ObservationCurrent(o) {
		t.Fatal("stale connection accepted")
	}
}

func TestObservationDoesNotHoldWriteMutexAndBoundsConcurrency(t *testing.T) {
	p, l, _ := observationFixture()
	release := l.blockOn("get_freq")
	defer release()
	done := make(chan error, 1)
	go func() { _, err := p.Observe(context.Background()); done <- err }()
	l.waitEntered(t, "get_freq")
	if _, err := p.Observe(context.Background()); !errors.Is(err, ErrObservationBusy) {
		t.Fatal(err)
	}
	written := make(chan error, 1)
	go func() { written <- p.Write(context.Background(), func(*Client) error { return nil }, nil) }()
	select {
	case err := <-written:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("observation blocked the write mutex")
	}
	release()
	if err := <-done; !errors.Is(err, ErrObservationChanged) {
		t.Fatal(err)
	}
}

func TestObservationCanceledSequenceStopsBeforeNextRead(t *testing.T) {
	p, l, _ := observationFixture()
	ctx, cancel := context.WithCancel(context.Background())
	l.after = func(cmd string, n int) {
		if n == 2 {
			cancel()
		}
	}
	if o, err := p.Observe(ctx); !errors.Is(err, context.Canceled) || o != (Observation{}) || len(l.callLog()) != 2 {
		t.Fatal(o, err, l.callLog())
	}
	before := len(l.callLog())
	if _, err := p.Observe(ctx); !errors.Is(err, context.Canceled) || len(l.callLog()) != before {
		t.Fatal(err)
	}
}

func TestObservationRejectsNonzeroStatusAndReleasesAdmission(t *testing.T) {
	p, l, _ := observationFixture()
	l.status = -1
	if _, err := p.Observe(context.Background()); !errors.Is(err, ErrObservationUnavailable) {
		t.Fatal(err)
	}
	l.status = 0
	if _, err := p.Observe(context.Background()); err != nil {
		t.Fatal("failed read retained admission", err)
	}
}

func TestClientConnectionGenerationPrecedesEventDelivery(t *testing.T) {
	// No dispatch goroutine: both state callbacks are still queued.
	c := &Client{connected: true, epoch: 7, wake: make(chan struct{}, 1)}
	c.setState(false)
	c.setState(true)
	generation, up := c.ConnectionGeneration()
	if !up || generation != 8 || len(c.events) != 2 {
		t.Fatal(generation, up, c.events)
	}
}

func TestObservationActualClientDeadlineStopsWithoutRetry(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	f := newFake(t, func(n int, line string, w net.Conn) bool {
		if line != `+\get_ptt` {
			t.Errorf("unexpected command %q", line)
		}
		close(entered)
		<-release
		return false
	})
	c := fastClient(t, f.addr())
	waitConnected(t, c)
	p := NewPoller(c, observationCaps(), nil, nil)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := p.Observe(ctx); done <- err }()
	select {
	case <-entered:
	case <-time.After(time.Second):
		t.Fatal("read never reached protocol fixture")
	}
	select {
	case err := <-done:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("observation ignored caller deadline")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.cmds) != 1 {
		t.Fatal("canceled observation retried", f.cmds)
	}
}

func TestObservationActualExtendedProtocol(t *testing.T) {
	f := newFake(t, func(n int, line string, w net.Conn) bool {
		cmd := strings.TrimPrefix(line, `+\`)
		value := map[string]string{"get_ptt": "PTT: 0", "get_vfo": "VFO: VFOA", "get_freq": "Frequency: 14074000"}[cmd]
		if value == "" {
			t.Errorf("unexpected wire command %q", line)
			return false
		}
		fmt.Fprintf(w, "%s:\n%s\nRPRT 0\n", cmd, value)
		return true
	})
	c := fastClient(t, f.addr())
	waitConnected(t, c)
	p := NewPoller(c, observationCaps(), nil, nil)
	o, err := p.Observe(ctx5(t))
	if err != nil || o.FrequencyHz() != 14074000 || !p.ObservationCurrent(o) {
		t.Fatal(o, err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.cmds) != 6 {
		t.Fatal(f.cmds)
	}
	for _, cmd := range f.cmds {
		if !strings.HasPrefix(cmd, `+\get_`) {
			t.Fatal("write sent", cmd)
		}
	}
}
