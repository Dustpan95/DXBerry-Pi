// Package status keeps the station's last dxberry-status answer fresh and streams it to the
// browsers that are watching. One loop runs the command at startup, every 30 s while an events
// socket is open, on an aged one-shot read, and soon after a call that changed something.
package status

import (
	"context"
	"encoding/json"
	"sync"
	"time"
)

const (
	busyEvery = 30 * time.Second // while somebody listens
	pokeGap   = time.Second      // at most one poked refresh per second
	runLimit  = 90 * time.Second
)

// Clock is the time source; tests move it by hand.
type Clock interface {
	Now() time.Time
	// At returns a channel that receives once the clock reaches t (at once if it already has).
	At(t time.Time) <-chan time.Time
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }
func (realClock) At(t time.Time) <-chan time.Time {
	return time.After(time.Until(t))
}

// Status is the cache and the refresh loop.
type Status struct {
	run      func(ctx context.Context) (string, error)
	clock    Clock
	radios   RadioSource   // nil until SetRadios
	hub      RigHub        // nil until SetRig
	programs ProgramSource // nil until SetPrograms
	onSock   SocketHook    // nil until OnSocket

	smu   sync.Mutex // guards socks and the order of hub interest updates
	socks map[*sock]struct{}

	nmu   sync.Mutex // guards notes
	notes map[chan []byte]struct{}

	mu      sync.Mutex
	raw     json.RawMessage // the last answer that parsed; nil before the first
	genAt   time.Time       // when raw was produced
	failed  bool            // the most recent run failed
	lastRun time.Time       // when the most recent run ended (zero before the first)
	demand  bool            // an aged one-shot read requested a report
	subs    map[chan struct{}]struct{}

	poke chan struct{}
	wake chan struct{}
}

// New returns a Status that gets its answers from run (dxberry-status --json, through the CLI
// package). A nil clock is the real one.
func New(run func(ctx context.Context) (string, error), clock Clock) *Status {
	if clock == nil {
		clock = realClock{}
	}
	return &Status{run: run, clock: clock, subs: map[chan struct{}]struct{}{},
		poke: make(chan struct{}, 1), wake: make(chan struct{}, 1)}
}

// ChangesState says whether a finished CLI call may have changed what dxberry-status reports:
// everything except reads (first argument get, list, check, job, recent, screen, status, models
// or scan), service-activity queries, log reading and the status command itself.
func ChangesState(cmd string, args []string) bool {
	// Preset definitions are not part of the full-station report. Their own
	// command response supplies the new revision without waking collectors.
	if cmd == "journalctl" || cmd == "dxberry-status" || cmd == "dxberry-preset" || len(args) == 0 {
		return false
	}
	if cmd == "systemctl" && args[0] == "is-active" {
		return false
	}
	switch args[0] {
	case "get", "list", "check", "job", "recent", "screen", "status", "models", "scan":
		return false
	}
	return true
}

func nudge(c chan struct{}) {
	select {
	case c <- struct{}{}:
	default:
	}
}

// Poke asks for a refresh soon (within a second of the previous run).
func (s *Status) Poke() { nudge(s.poke) }

// Request asks for an aged cache to be refreshed without waiting for the result.
// Unlike mutation pokes, read demand is satisfied by any in-flight collection.
// Both successful and failed attempts limit repeated reads to the usual cadence.
func (s *Status) Request() {
	s.mu.Lock()
	if s.lastRun.IsZero() || !s.clock.Now().Before(s.lastRun.Add(busyEvery)) {
		s.demand = true
	}
	s.mu.Unlock()
	nudge(s.wake)
}

// Listeners is the number of open events sockets.
func (s *Status) Listeners() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.subs)
}

// Subscribe registers a listener. The channel gets a value after every refresh (values merge when
// the listener is slow); call cancel when done.
func (s *Status) Subscribe() (updates <-chan struct{}, cancel func()) {
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	nudge(s.wake)
	var once sync.Once
	return ch, func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.subs, ch)
			s.mu.Unlock()
			nudge(s.wake)
		})
	}
}

// Snapshot is {"generated": unix ms, "stale": bool, "status": <last answer or null>}. The answer
// is stale when the last run failed, when nothing has been read yet, or when it is older than
// three times the normal observed interval, regardless of whether a browser is connected.
func (s *Status) Snapshot() []byte {
	s.mu.Lock()
	raw, gen, failed := s.raw, s.genAt, s.failed
	s.mu.Unlock()
	out := struct {
		Generated int64           `json:"generated"`
		Stale     bool            `json:"stale"`
		Status    json.RawMessage `json:"status"`
	}{Status: json.RawMessage("null")}
	out.Stale = true
	if raw != nil {
		out.Generated = gen.UnixMilli()
		out.Status = raw
		out.Stale = failed || s.clock.Now().Sub(gen) > 3*busyEvery
	}
	b, err := json.Marshal(out)
	if err != nil { // raw was validated before it was kept
		return []byte(`{"generated":0,"stale":true,"status":null}`)
	}
	return b
}

// Run refreshes until ctx ends.
func (s *Status) Run(ctx context.Context) {
	for ctx.Err() == nil {
		s.mu.Lock()
		last := s.lastRun
		wanted := len(s.subs) > 0 || s.demand
		due := last.Add(busyEvery)
		s.mu.Unlock()
		if last.IsZero() || (wanted && !s.clock.Now().Before(due)) {
			s.refresh(ctx)
			continue
		}
		var deadline <-chan time.Time
		if wanted {
			deadline = s.clock.At(due)
		}
		select {
		case <-ctx.Done():
			return
		case <-deadline:
		case <-s.wake: // the listener count changed: work the deadline out again
		case <-s.poke:
			if t := last.Add(pokeGap); s.clock.Now().Before(t) {
				select {
				case <-ctx.Done():
					return
				case <-s.clock.At(t):
				}
			}
			s.refresh(ctx)
		}
	}
}

func (s *Status) refresh(ctx context.Context) {
	rctx, cancel := context.WithTimeout(ctx, runLimit)
	out, err := s.run(rctx)
	cancel()
	if ctx.Err() != nil {
		return
	}
	now := s.clock.Now()
	s.mu.Lock()
	s.lastRun = now
	s.demand = false
	if err == nil && json.Valid([]byte(out)) {
		s.raw, s.genAt, s.failed = json.RawMessage(out), now, false
	} else {
		s.failed = true
	}
	for ch := range s.subs {
		nudge(ch)
	}
	s.mu.Unlock()
}
