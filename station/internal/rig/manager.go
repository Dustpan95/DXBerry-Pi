package rig

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
)

// RadioLister is the radio records as the manager needs them (radios.Watcher).
type RadioLister interface {
	List() []radios.Radio
	Subscribe() <-chan []radios.Radio
	Unsubscribe(<-chan []radios.Radio)
}

// Conn is a rigctld connection the manager owns (*Client is one).
type Conn interface {
	Link
	Close()
}

// Update is one rig delta for the events socket. Package status declares the
// same unnamed struct type, so the two meet without importing each other.
// Link is true when the connection to the radio went up or down.
type Update = struct {
	Radio string
	Data  []byte
	Link  bool
}

const (
	discoverBackoffStart = time.Second
	discoverBackoffMax   = 30 * time.Second
	discoverTimeout      = 30 * time.Second
	updateBuffer         = 256
)

// Manager owns one Client, Caps and Poller per radio record.
type Manager struct {
	// Dialer opens the connection to a rigctld; tests replace it. The
	// default dials TCP with Dial.
	Dialer func(addr string) Conn

	w     RadioLister
	cache CapsCache
	clock Clock
	log   *slog.Logger

	mu       sync.Mutex
	entries  map[string]*entry
	interest map[string]Interest
	subs     map[chan Update]struct{}
}

type entry struct {
	radio  radios.Radio // guarded by Manager.mu
	name   string
	conn   Conn
	poller *Poller
	cancel context.CancelFunc
	done   chan struct{}

	mu   sync.Mutex
	caps Caps
	have bool
}

// NewManager returns a manager; Run starts it.
func NewManager(w RadioLister, cache CapsCache, clock Clock, log *slog.Logger) *Manager {
	if clock == nil {
		clock = realClock{}
	}
	if log == nil {
		log = slog.Default()
	}
	m := &Manager{w: w, cache: cache, clock: clock, log: log,
		entries: map[string]*entry{}, interest: map[string]Interest{}, subs: map[chan Update]struct{}{}}
	m.Dialer = func(addr string) Conn { return Dial(addr, log) }
	return m
}

// Run follows the radio records until ctx ends, then closes every poller and
// connection.
func (m *Manager) Run(ctx context.Context) {
	ch := m.w.Subscribe()
	defer m.w.Unsubscribe(ch)
	m.reconcile(ctx, m.w.List())
	for {
		select {
		case <-ctx.Done():
			m.stopAll()
			return
		case list := <-ch:
			m.reconcile(ctx, list)
		}
	}
}

func (m *Manager) reconcile(ctx context.Context, list []radios.Radio) {
	want := map[string]radios.Radio{}
	for _, r := range list {
		if r.Port <= 0 {
			m.log.Warn("radio has no rigctld port; not polled", "radio", r.Name)
			continue
		}
		want[r.Name] = r
	}
	m.mu.Lock()
	var gone []*entry
	for name, e := range m.entries {
		r, ok := want[name]
		if !ok || r.Port != e.radio.Port || r.Model != e.radio.Model {
			gone = append(gone, e)
			delete(m.entries, name)
			continue
		}
		e.radio = r // a label or owner change needs no new connection
	}
	m.mu.Unlock()
	for _, e := range gone {
		e.stop()
	}
	for name, r := range want {
		m.mu.Lock()
		_, have := m.entries[name]
		m.mu.Unlock()
		if !have {
			m.start(ctx, r)
		}
	}
}

func (e *entry) stop() {
	e.cancel()
	<-e.done
	e.conn.Close()
}

func (m *Manager) stopAll() {
	m.mu.Lock()
	all := make([]*entry, 0, len(m.entries))
	for _, e := range m.entries {
		all = append(all, e)
	}
	m.entries = map[string]*entry{}
	m.mu.Unlock()
	for _, e := range all {
		e.stop()
	}
}

func (m *Manager) start(parent context.Context, r radios.Radio) {
	ctx, cancel := context.WithCancel(parent)
	conn := m.Dialer("127.0.0.1:" + strconv.Itoa(r.Port))
	p := newPoller(conn, Caps{}, m.clock, m.log.With("radio", r.Name))
	if c, ok := conn.(*Client); ok {
		p.client = c
	}
	e := &entry{radio: r, name: r.Name, conn: conn, poller: p, cancel: cancel, done: make(chan struct{})}
	model := strconv.Itoa(r.Model)
	if c, ok := m.cache.Load(model); ok {
		e.caps, e.have = c, true
		p.SetCaps(c)
	}
	m.mu.Lock()
	m.entries[r.Name] = e
	p.SetInterest(m.interest[r.Name])
	m.mu.Unlock()

	var wg sync.WaitGroup
	wg.Add(3)
	go func() { defer wg.Done(); p.Run(ctx) }()
	go func() { defer wg.Done(); m.forward(ctx, r.Name, p) }()
	go func() { defer wg.Done(); m.discover(ctx, e, model) }()
	go func() { wg.Wait(); close(e.done) }()
}

// discover learns the radio's capabilities once the connection is up, retrying
// with backoff, and caches them by model number.
func (m *Manager) discover(ctx context.Context, e *entry, model string) {
	e.mu.Lock()
	have := e.have
	e.mu.Unlock()
	if have {
		return
	}
	up := make(chan struct{}, 1)
	e.conn.OnState(func(c bool) {
		if c {
			select {
			case up <- struct{}{}:
			default:
			}
		}
	})
	backoff := discoverBackoffStart
	for ctx.Err() == nil {
		if !e.conn.Connected() {
			select {
			case <-ctx.Done():
				return
			case <-up:
			}
			continue
		}
		dctx, cancel := context.WithTimeout(ctx, discoverTimeout)
		caps, err := Discover(dctx, e.conn)
		cancel()
		if err == nil {
			if err := m.cache.Store(model, caps); err != nil {
				m.log.Warn("could not cache radio capabilities", "radio", e.name, "error", err)
			}
			e.mu.Lock()
			e.caps, e.have = caps, true
			e.mu.Unlock()
			e.poller.SetCaps(caps)
			return
		}
		m.log.Warn("radio capability discovery failed; will retry", "radio", e.name, "error", err)
		select {
		case <-ctx.Done():
			return
		case <-m.clock.At(m.clock.Now().Add(backoff)):
		}
		if backoff *= 2; backoff > discoverBackoffMax {
			backoff = discoverBackoffMax
		}
	}
}

// forward turns the poller's states into events for the sockets.
func (m *Manager) forward(ctx context.Context, name string, p *Poller) {
	ch := p.Subscribe()
	defer p.Unsubscribe(ch)
	linked := p.State().Connected
	for {
		select {
		case <-ctx.Done():
			return
		case st := <-ch:
			u := Update{Radio: name, Data: encodeState(st), Link: st.Connected != linked}
			linked = st.Connected
			m.broadcast(u)
		}
	}
}

// encodeState is the wire form: the capabilities travel separately.
func encodeState(st State) []byte {
	st.Caps = nil
	b, _ := json.Marshal(st)
	return b
}

func (m *Manager) broadcast(u Update) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for ch := range m.subs {
		select {
		case ch <- u:
		default: // a stalled socket misses this one; the next change corrects it
		}
	}
}

// Subscribe returns a channel of rig deltas and a cancel function.
func (m *Manager) Subscribe() (<-chan Update, func()) {
	ch := make(chan Update, updateBuffer)
	m.mu.Lock()
	m.subs[ch] = struct{}{}
	m.mu.Unlock()
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			m.mu.Lock()
			delete(m.subs, ch)
			m.mu.Unlock()
		})
	}
}

// Snapshot is the current State of a radio in wire form.
func (m *Manager) Snapshot(name string) ([]byte, bool) {
	p, ok := m.Radio(name)
	if !ok {
		return nil, false
	}
	return encodeState(p.State()), true
}

// Radio returns the poller of a radio.
func (m *Manager) Radio(name string) (*Poller, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[name]; ok {
		return e.poller, true
	}
	return nil, false
}

// Caps returns the radio's capabilities once they are known.
func (m *Manager) Caps(name string) (Caps, bool) {
	m.mu.Lock()
	e, ok := m.entries[name]
	m.mu.Unlock()
	if !ok {
		return Caps{}, false
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.caps, e.have
}

// Client returns the radio's rigctld client (false when the connection is not
// a *Client, as in tests).
func (m *Manager) Client(name string) (*Client, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if e, ok := m.entries[name]; ok {
		c, isClient := e.conn.(*Client)
		return c, isClient
	}
	return nil, false
}

// SetInterest sets how closely a radio is polled; it is remembered for a
// radio whose poller does not exist yet.
func (m *Manager) SetInterest(name string, level Interest) {
	m.mu.Lock()
	m.interest[name] = level
	e := m.entries[name]
	m.mu.Unlock()
	if e != nil {
		e.poller.SetInterest(level)
	}
}

// SetWatch is SetInterest with the level as the plain int package status
// passes: 0 none, 1 dashboard, 2 page.
func (m *Manager) SetWatch(name string, level int) {
	if level < int(None) || level > int(Page) {
		level = int(None)
	}
	m.SetInterest(name, Interest(level))
}
