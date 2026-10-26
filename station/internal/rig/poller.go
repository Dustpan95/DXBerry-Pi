package rig

import (
	"context"
	"errors"
	"log/slog"
	"math"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// Clock is the poller's and manager's source of time. It has the same shape
// as status.Clock; tests drive a fake one.
type Clock interface {
	Now() time.Time
	At(t time.Time) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) At(t time.Time) <-chan time.Time {
	return time.After(time.Until(t))
}

// Interest is how closely somebody is watching a radio.
type Interest int

// The values are part of the events protocol's contract with package status
// (it passes them as plain ints): 0 none, 1 dashboard, 2 VFO page.
const (
	None      Interest = iota // 10 s
	Dashboard                 // 2 s
	Page                      // 250 ms, plus the meters
)

const txEvery = 200 * time.Millisecond // SWR/ALC cadence while transmitting

// missesToQuiet is how many poll cycles in a row with no successful read make a radio "not answering".
const missesToQuiet = 2

func (i Interest) period() time.Duration {
	switch i {
	case Page:
		return 250 * time.Millisecond
	case Dashboard:
		return 2 * time.Second
	}
	return 10 * time.Second
}

// State is everything the station knows about one radio's front panel.
// Optimistic names the fields a write changed that no read has confirmed yet
// ("freq", "mode", "ptt", "vfo", "split", or a level name). Answering is false once
// missesToQuiet poll cycles in a row got no successful read: rigctld is up but the radio
// behind it does not answer (switched off, flrig not running).
type State struct {
	Connected  bool               `json:"connected"`
	Answering  bool               `json:"answering"`
	Freq       int64              `json:"freq"`
	Mode       string             `json:"mode"`
	Passband   string             `json:"passband"`
	PTT        bool               `json:"ptt"`
	VFO        string             `json:"vfo"`
	Split      bool               `json:"split"`
	Levels     map[string]float64 `json:"levels"`
	Meters     map[string]float64 `json:"meters"`
	Caps       *Caps              `json:"caps,omitempty"`
	UpdatedAt  time.Time          `json:"updated_at"`
	Optimistic map[string]bool    `json:"optimistic"`
}

func (s State) clone() State {
	s.Levels = cloneMap(s.Levels)
	s.Meters = cloneMap(s.Meters)
	s.Optimistic = cloneMap(s.Optimistic)
	if s.Caps != nil {
		c := *s.Caps
		s.Caps = &c
	}
	return s
}

func cloneMap[V any](m map[string]V) map[string]V {
	out := make(map[string]V, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

// Link is the part of a rigctld connection the poller uses. *Client is one.
type Link interface {
	Commander
	Connected() bool
	OnState(func(connected bool))
}

// Poller reads one radio at the cadence its watchers ask for and keeps State.
type Poller struct {
	link   Link
	client *Client // nil when link is not a *Client (tests)
	clock  Clock
	log    *slog.Logger

	pending atomic.Int32 // writes in progress: no read starts while it is non-zero
	gen     atomic.Int64 // bumped by every write; a reply that began earlier is stale
	force   atomic.Bool  // run a full cycle at once
	wake    chan struct{}

	wmu sync.Mutex // serializes writes against each other

	mu       sync.Mutex
	readPTTv bool // the last PTT value a READ reported (st.PTT may hold an optimistic one)
	misses   int  // poll cycles in a row that tried the radio and got no successful read
	st       State
	caps     Caps
	interest Interest
	subs     []chan State
}

// NewPoller returns a poller for c. Run starts it.
func NewPoller(c *Client, caps Caps, clock Clock, log *slog.Logger) *Poller {
	p := newPoller(c, caps, clock, log)
	p.client = c
	return p
}

func newPoller(l Link, caps Caps, clock Clock, log *slog.Logger) *Poller {
	if clock == nil {
		clock = realClock{}
	}
	if log == nil {
		log = slog.Default()
	}
	p := &Poller{link: l, clock: clock, log: log, wake: make(chan struct{}, 1)}
	p.st = State{Answering: true, Levels: map[string]float64{}, Meters: map[string]float64{}, Optimistic: map[string]bool{}}
	p.setCapsLocked(caps)
	l.OnState(p.onState) // register first, then read the state
	p.st.Connected = l.Connected()
	return p
}

func (p *Poller) setCapsLocked(c Caps) {
	p.caps = c
	if c.ModelName == "" && c.Probed.IsZero() {
		p.st.Caps = nil
		return
	}
	cc := c
	p.st.Caps = &cc
}

// SetCaps replaces the capabilities (the manager learns them after the
// connection comes up).
func (p *Poller) SetCaps(c Caps) {
	p.mu.Lock()
	p.setCapsLocked(c)
	p.publishLocked()
	p.mu.Unlock()
	p.nudge()
}

func (p *Poller) nudge() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *Poller) onState(up bool) {
	p.mu.Lock()
	if p.st.Connected != up {
		p.st.Connected = up
		p.publishLocked()
	}
	p.mu.Unlock()
	if up {
		p.force.Store(true)
	}
	p.nudge()
}

// SetInterest changes the cadence; the next read is scheduled from the last.
func (p *Poller) SetInterest(i Interest) {
	p.mu.Lock()
	p.interest = i
	p.mu.Unlock()
	p.nudge()
}

// State returns a copy of the current state.
func (p *Poller) State() State {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.st.clone()
}

// Subscribe returns a channel that holds the latest state after each change
// (a slow reader sees the newest, not a backlog).
func (p *Poller) Subscribe() <-chan State {
	ch := make(chan State, 1)
	p.mu.Lock()
	p.subs = append(p.subs, ch)
	p.mu.Unlock()
	return ch
}

// Unsubscribe stops deliveries to ch.
func (p *Poller) Unsubscribe(ch <-chan State) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for i, c := range p.subs {
		if (<-chan State)(c) == ch {
			p.subs = append(p.subs[:i], p.subs[i+1:]...)
			return
		}
	}
}

func (p *Poller) publishLocked() {
	for _, ch := range p.subs {
		s := p.st.clone()
		select {
		case ch <- s:
		default:
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- s:
			default:
			}
		}
	}
}

// Run polls until ctx ends.
func (p *Poller) Run(ctx context.Context) {
	var lastStart, lastTx time.Time
	txActive := false
	for ctx.Err() == nil {
		p.mu.Lock()
		in, up := p.interest, p.st.Connected
		p.mu.Unlock()
		if !up {
			lastStart, txActive = time.Time{}, false
			select {
			case <-ctx.Done():
				return
			case <-p.wake:
			}
			continue
		}
		if p.force.Swap(false) {
			lastStart = time.Time{}
		}
		if txActive && in != Page {
			txActive = false
		}
		now := p.clock.Now()
		mainDue := lastStart.Add(in.period())
		next, isTx := mainDue, false
		if txActive && lastTx.Add(txEvery).Before(next) {
			next, isTx = lastTx.Add(txEvery), true
		}
		if next.After(now) {
			select {
			case <-ctx.Done():
				return
			case <-p.wake:
			case <-p.clock.At(next):
			}
			continue
		}
		if p.pending.Load() > 0 {
			select {
			case <-ctx.Done():
				return
			case <-p.wake:
			case <-time.After(5 * time.Millisecond):
			}
			continue
		}
		if isTx {
			lastTx = now
			if !p.txCycle(ctx) {
				txActive = false
			}
			continue
		}
		lastStart = now
		if p.mainCycle(ctx, in) && in == Page && p.lastReadPTT() && !txActive {
			txActive, lastTx = true, now
		}
	}
}

// read runs one command unless a write is waiting. ok is false when the cycle
// should stop (write pending, transport failure); a rigctld error just means
// this field has nothing to report.
func (p *Poller) read(ctx context.Context, c *cycle, cmd string, args ...string) (rep Reply, got, ok bool) {
	g := p.gen.Load()
	if p.pending.Load() > 0 || ctx.Err() != nil {
		return Reply{}, false, false
	}
	r, err := p.link.Do(ctx, cmd, args...)
	if p.gen.Load() != g {
		return Reply{}, false, false // a write began meanwhile: this answer may predate it
	}
	if err != nil {
		var re *RigError
		if errors.As(err, &re) {
			c.missed = true
			return Reply{}, false, true
		}
		if !errors.Is(err, ErrNotConnected) && ctx.Err() == nil {
			c.missed = true
			p.log.Debug("rig read failed", "cmd", cmd, "error", err)
		}
		return Reply{}, false, false
	}
	return r, true, true
}

// field finds the value of a "Key: value" line, or the idx-th line when the
// reply carries no labels.
func field(r Reply, key string, idx int) (string, bool) {
	for _, l := range r.Lines {
		if k, v, ok := strings.Cut(l, ":"); ok && strings.EqualFold(strings.TrimSpace(k), key) {
			return strings.TrimSpace(v), true
		}
	}
	if idx < len(r.Lines) && !strings.Contains(r.Lines[idx], ":") {
		return strings.TrimSpace(r.Lines[idx]), true
	}
	return "", false
}

func number(r Reply) (float64, bool) {
	for _, l := range r.Lines {
		if i := strings.LastIndex(l, ":"); i >= 0 {
			l = l[i+1:]
		}
		if f, err := strconv.ParseFloat(strings.TrimSpace(l), 64); err == nil && !math.IsNaN(f) && !math.IsInf(f, 0) {
			return f, true
		}
	}
	return 0, false
}

// cycle collects one round of reads and publishes once at the end.
type cycle struct {
	p      *Poller
	dirty  bool // a value or an Optimistic flag changed
	gotAny bool // at least one read succeeded
	missed bool // a read reached the link and failed (the radio's error or no reply)
}

func (c *cycle) set(fn func(s *State) bool) {
	c.gotAny = true
	c.p.mu.Lock()
	if fn(&c.p.st) {
		c.dirty = true
	}
	c.p.mu.Unlock()
}

// confirm clears the optimistic flag of a field a read just reported.
func confirm(s *State, name string) bool {
	if s.Optimistic[name] {
		delete(s.Optimistic, name)
		return true
	}
	return false
}

func (c *cycle) done() {
	p := c.p
	p.mu.Lock()
	switch {
	case c.gotAny:
		p.st.UpdatedAt = p.clock.Now()
		p.misses = 0
		if !p.st.Answering {
			p.st.Answering, c.dirty = true, true
			p.log.Info("the radio is answering again")
		}
	case c.missed:
		p.misses++
		if p.misses >= missesToQuiet && p.st.Answering {
			p.st.Answering, c.dirty = false, true
			p.log.Warn("the radio is not answering behind rigctld", "cycles", p.misses)
		}
	}
	if c.dirty {
		p.publishLocked()
	}
	p.mu.Unlock()
}

func isTxMeter(n string) bool { return n == "SWR" || n == "ALC" }

func (p *Poller) lastReadPTT() bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.readPTTv
}

// readPTT reads the key state. Whenever a read reports PTT off, SWR and ALC
// are zeroed in the same update, at any interest and whatever raced it: the
// zero depends on the reading, not on the cadence that happened to run.
func (p *Poller) readPTT(ctx context.Context, c *cycle) (on, ok bool) {
	r, got, ok := p.read(ctx, c, "get_ptt")
	if !got {
		return false, ok
	}
	v, found := field(r, "PTT", 0)
	if !found {
		return false, true
	}
	on = v != "0"
	c.set(func(s *State) bool {
		p.readPTTv = on
		d := confirm(s, "ptt")
		if s.PTT != on {
			s.PTT, d = on, true
		}
		if !on {
			for _, n := range p.caps.ReadableMeters {
				if isTxMeter(n) {
					if old, has := s.Meters[n]; has && old != 0 {
						s.Meters[n], d = 0, true
					}
				}
			}
		}
		return d
	})
	return on, true
}

// store files a level reading under Levels (settable) or Meters (read-only).
func (p *Poller) store(c *cycle, name string, v float64) {
	p.mu.Lock()
	lvl := false
	for _, n := range p.caps.SettableLevels {
		if n == name {
			lvl = true
		}
	}
	p.mu.Unlock()
	c.set(func(s *State) bool {
		d := confirm(s, name)
		m := s.Meters
		if lvl {
			m = s.Levels
		}
		if old, ok := m[name]; !ok || old != v {
			m[name], d = v, true
		}
		return d
	})
}

func (p *Poller) mainCycle(ctx context.Context, in Interest) bool {
	c := &cycle{p: p}
	defer c.done()
	p.mu.Lock()
	caps := p.caps
	known := p.st.Caps != nil
	p.mu.Unlock()

	if r, got, ok := p.read(ctx, c, "get_freq"); !ok {
		return false
	} else if got {
		if v, found := field(r, "Frequency", 0); found {
			if f, err := strconv.ParseInt(v, 10, 64); err == nil {
				c.set(func(s *State) bool {
					d := confirm(s, "freq")
					if s.Freq != f {
						s.Freq, d = f, true
					}
					return d
				})
			}
		}
	}
	if r, got, ok := p.read(ctx, c, "get_mode"); !ok {
		return false
	} else if got {
		mode, found := field(r, "Mode", 0)
		pb, _ := field(r, "Passband", 1)
		if found {
			c.set(func(s *State) bool {
				d := confirm(s, "mode")
				if s.Mode != mode || s.Passband != pb {
					s.Mode, s.Passband, d = mode, pb, true
				}
				return d
			})
		}
	}
	if !known || caps.CanGetVFO {
		if r, got, ok := p.read(ctx, c, "get_vfo"); !ok {
			return false
		} else if got {
			if v, found := field(r, "VFO", 0); found {
				c.set(func(s *State) bool {
					d := confirm(s, "vfo")
					if s.VFO != v {
						s.VFO, d = v, true
					}
					return d
				})
			}
		}
	}
	if !known || caps.CanGetPTT {
		if _, ok := p.readPTT(ctx, c); !ok {
			return false
		}
	}
	if !known || caps.CanGetSplit {
		if r, got, ok := p.read(ctx, c, "get_split_vfo"); !ok {
			return false
		} else if got {
			if v, found := field(r, "Split", 0); found {
				on := v != "0"
				c.set(func(s *State) bool {
					d := confirm(s, "split")
					if s.Split != on {
						s.Split, d = on, true
					}
					return d
				})
			}
		}
	}
	if in == Page {
		for _, n := range caps.ReadableMeters {
			if isTxMeter(n) {
				continue
			}
			r, got, ok := p.read(ctx, c, "get_level", n)
			if !ok {
				return false
			}
			if got {
				if v, found := number(r); found {
					p.store(c, n, v)
				}
			}
		}
	}
	return true
}

// txCycle reads PTT and, while it is on, SWR and ALC. It reports whether the
// transmit cadence should go on.
func (p *Poller) txCycle(ctx context.Context) bool {
	c := &cycle{p: p}
	defer c.done()
	p.mu.Lock()
	caps := p.caps
	p.mu.Unlock()
	on, ok := p.readPTT(ctx, c)
	if !ok || !on {
		return false
	}
	for _, n := range caps.ReadableMeters {
		if !isTxMeter(n) {
			continue
		}
		r, got, ok := p.read(ctx, c, "get_level", n)
		if !ok {
			return false
		}
		if got {
			if v, found := number(r); found {
				p.store(c, n, v)
			}
		}
	}
	return true
}

// Write applies optimistic to the state, runs fn ahead of any read, and
// leaves the next read of each changed field to confirm or correct it. The
// optimistic callback runs under the poller's lock: it must only edit the
// State it is given. Writes run one at a time. When fn fails the optimistic
// change is undone; when it succeeds, fields no read can ever confirm (the
// capabilities say the radio cannot report them) lose their flag at once.
func (p *Poller) Write(ctx context.Context, fn func(*Client) error, optimistic func(*State)) error {
	p.wmu.Lock()
	defer p.wmu.Unlock()
	p.pending.Add(1)
	defer func() {
		p.force.Store(true)
		p.pending.Add(-1)
		p.nudge()
	}()
	p.gen.Add(1)
	var before State
	var fields []string
	p.mu.Lock()
	if optimistic != nil {
		before = p.st.clone()
		optimistic(&p.st)
		fields = diffFields(before, p.st)
		for _, f := range fields {
			p.st.Optimistic[f] = true
		}
		p.publishLocked()
	}
	p.mu.Unlock()

	err := fn(p.client)
	p.gen.Add(1)
	if len(fields) == 0 {
		return err
	}
	p.mu.Lock()
	if err != nil {
		revert(&p.st, before, fields)
	} else {
		for _, f := range fields {
			if !p.readableLocked(f) {
				delete(p.st.Optimistic, f)
			}
		}
	}
	p.publishLocked()
	p.mu.Unlock()
	return err
}

// readableLocked reports whether the poller's reads can ever report field f.
func (p *Poller) readableLocked(f string) bool {
	if p.st.Caps == nil {
		return true // capabilities unknown: every read is tried
	}
	c := p.caps
	switch f {
	case "freq", "mode":
		return true
	case "ptt":
		return c.CanGetPTT
	case "vfo":
		return c.CanGetVFO
	case "split":
		return c.CanGetSplit
	}
	for _, n := range c.ReadableMeters {
		if n == f {
			return true
		}
	}
	return false
}

func diffFields(a, b State) []string {
	var f []string
	if a.Freq != b.Freq {
		f = append(f, "freq")
	}
	if a.Mode != b.Mode || a.Passband != b.Passband {
		f = append(f, "mode")
	}
	if a.PTT != b.PTT {
		f = append(f, "ptt")
	}
	if a.VFO != b.VFO {
		f = append(f, "vfo")
	}
	if a.Split != b.Split {
		f = append(f, "split")
	}
	seen := map[string]bool{}
	for k, v := range b.Levels {
		seen[k] = true
		if old, ok := a.Levels[k]; !ok || old != v {
			f = append(f, k)
		}
	}
	for k := range a.Levels {
		if !seen[k] {
			f = append(f, k)
		}
	}
	return f
}

func revert(s *State, before State, fields []string) {
	for _, f := range fields {
		delete(s.Optimistic, f)
		switch f {
		case "freq":
			s.Freq = before.Freq
		case "mode":
			s.Mode, s.Passband = before.Mode, before.Passband
		case "ptt":
			s.PTT = before.PTT
		case "vfo":
			s.VFO = before.VFO
		case "split":
			s.Split = before.Split
		default:
			if v, ok := before.Levels[f]; ok {
				s.Levels[f] = v
			} else {
				delete(s.Levels, f)
			}
		}
	}
}
