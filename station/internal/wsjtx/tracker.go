package wsjtx

import (
	"errors"
	"math"
	"regexp"
	"sort"
	"sync"
	"time"
	"unicode/utf8"
)

const (
	HeartbeatPeriod    = 15 * time.Second
	FreshFor           = 3 * HeartbeatPeriod
	DecodeHistoryLimit = 200
	MaxSessions        = 32
)

var (
	ErrInvalidSession      = errors.New("invalid WSJT-X observation session")
	ErrSessionLimit        = errors.New("WSJT-X observation session limit reached")
	ErrGenerationExhausted = errors.New("WSJT-X session generations exhausted")
	ErrRetired             = errors.New("WSJT-X observation session retired")
	ErrIdentity            = errors.New("unexpected WSJT-X instance ID")
	ErrLateDatagram        = errors.New("stale or out-of-order WSJT-X observation")
	radioName              = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

// Clock.Now must be safe for concurrent use and preserve monotonic time for
// expiry. The default uses time.Now; tests advance their clock without waiting.
type Clock interface{ Now() time.Time }

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

type Availability string

const (
	Waiting Availability = "waiting"
	Live    Availability = "live"
	Stale   Availability = "stale"
	Closed  Availability = "closed"
)

// Token identifies a receiver generation. Its fields are private so callers
// cannot accidentally use a browser-supplied generation or another tracker.
// A future UDP receiver must capture this token when creating its socket; the
// wire protocol itself contains no generation number or sender authentication.
type Token struct {
	owner      *Tracker
	radio      string
	generation uint64
}

type Counters struct {
	Accepted          uint64
	Malformed         uint64
	Oversized         uint64
	UnsupportedSchema uint64
	IgnoredType       uint64
	WrongIdentity     uint64
	Late              uint64
	Duplicates        uint64
	Evicted           uint64
	Clears            uint64
	Expirations       uint64
}

// ReceivedDecode keeps arrival times separate from the program's UTC time of
// day. Data retains the first packet's New flag; SeenLive/SeenReplay include
// subsequent duplicate observations without adding a second decode row.
type ReceivedDecode struct {
	FirstSeen  time.Time
	LastSeen   time.Time
	Data       Decode
	SeenLive   bool
	SeenReplay bool
}

// Snapshot is detached from the tracker's mutable state. Epoch changes on a
// timeout even when the managed program generation has not changed, exposing
// the loss of continuity instead of silently retaining pre-gap status/history.
type Snapshot struct {
	Radio        string
	Generation   uint64
	Epoch        uint64
	InstanceID   string
	Availability Availability
	LastSeen     *time.Time
	StatusSeenAt *time.Time
	Schema       uint32
	Heartbeat    *Heartbeat
	Status       *Status
	Decodes      []ReceivedDecode
	Counters     Counters
}

type observedSession struct {
	generation   uint64
	epoch        uint64
	id           string
	state        Availability
	hasSeen      bool
	lastSeen     time.Time
	schema       uint32
	heartbeat    *Heartbeat
	status       *Status
	statusSeenAt time.Time
	decodes      []ReceivedDecode
	counters     Counters
}

// Tracker holds only program observations. It does not claim a transmitter,
// schedule radio work, open a socket, or write program settings.
type Tracker struct {
	mu       sync.Mutex
	clock    Clock
	next     uint64
	sessions map[string]*observedSession
}

func NewTracker(clock Clock) *Tracker {
	if clock == nil {
		clock = wallClock{}
	}
	return &Tracker{clock: clock, sessions: make(map[string]*observedSession)}
}

func validSession(radio, instanceID string) bool {
	return radioName.MatchString(radio) && instanceID != "" && len(instanceID) <= MaxIDSize && utf8.ValidString(instanceID)
}

// Start replaces any observation for radio, including a same-ID process restart.
// Invalid requests leave the existing generation unchanged. A receiver must
// also retire its old socket and avoid reusing its endpoint for the new token.
func (t *Tracker) Start(radio, instanceID string) (Token, error) {
	if !validSession(radio, instanceID) {
		return Token{}, ErrInvalidSession
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if _, exists := t.sessions[radio]; !exists && len(t.sessions) >= MaxSessions {
		return Token{}, ErrSessionLimit
	}
	if t.next == math.MaxUint64 {
		return Token{}, ErrGenerationExhausted
	}
	t.next++
	t.sessions[radio] = &observedSession{generation: t.next, epoch: 1, id: instanceID, state: Waiting}
	return Token{owner: t, radio: radio, generation: t.next}, nil
}

func (t *Tracker) session(token Token) *observedSession {
	if token.owner != t {
		return nil
	}
	s := t.sessions[token.radio]
	if s == nil || s.generation != token.generation {
		return nil
	}
	return s
}

// Remove invalidates one exact generation. A delayed stop notification cannot
// delete the replacement session, and removed radios leave no growing tombstone.
func (t *Tracker) Remove(token Token) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.session(token) == nil {
		return false
	}
	delete(t.sessions, token.radio)
	return true
}

// Accept parses one datagram for a receiver's captured token. Rejected input
// never refreshes liveness. Close is terminal until Start creates a new token.
func (t *Tracker) Accept(token Token, datagram []byte) error {
	if token.owner != t {
		return ErrRetired
	}
	at := t.clock.Now()
	m, parseErr := Parse(datagram)
	return t.accept(token, m, parseErr, at)
}

// accept also serves the receiver, which parses once before pinning its peer.
func (t *Tracker) accept(token Token, m Message, parseErr error, at time.Time) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.session(token)
	if s == nil || s.state == Closed {
		return ErrRetired
	}
	now := t.clock.Now()
	s.expire(now)
	if parseErr != nil {
		switch {
		case errors.Is(parseErr, ErrTooLarge):
			s.counters.Oversized++
		case errors.Is(parseErr, ErrUnsupportedSchema):
			s.counters.UnsupportedSchema++
		case errors.Is(parseErr, ErrIgnoredType):
			s.counters.IgnoredType++
		default:
			s.counters.Malformed++
		}
		return parseErr
	}
	if m.ID != s.id {
		s.counters.WrongIdentity++
		return ErrIdentity
	}
	if at.After(now) || now.Sub(at) >= FreshFor || s.hasSeen && at.Before(s.lastSeen) {
		s.counters.Late++
		return ErrLateDatagram
	}
	s.counters.Accepted++
	s.hasSeen, s.lastSeen, s.schema, s.state = true, at, m.Schema, Live
	switch m.Type {
	case HeartbeatMessage:
		s.heartbeat = m.Heartbeat
	case StatusMessage:
		s.status = m.Status
		s.statusSeenAt = at
	case DecodeMessage:
		s.addDecode(*m.Decode, at)
	case ClearMessage:
		s.decodes = nil
		s.counters.Clears++
	case CloseMessage:
		s.clearReadings()
		s.state = Closed
	}
	return nil
}

// retire ends a failed/closed transport without inventing a program message or
// refreshing its last arrival time. A replacement token is never affected.
func (t *Tracker) retire(token Token) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if s := t.session(token); s != nil {
		s.clearReadings()
		s.state = Closed
	}
}

func (s *observedSession) clearReadings() {
	s.schema, s.heartbeat, s.status, s.decodes = 0, nil, nil, nil
	s.statusSeenAt = time.Time{}
}

func (s *observedSession) expire(now time.Time) {
	if s.state == Live && now.Sub(s.lastSeen) >= FreshFor {
		s.clearReadings()
		s.state = Stale
		s.epoch++
		s.counters.Expirations++
	}
}

type optionalValue[T comparable] struct {
	present bool
	value   T
}

func optional[T comparable](p *T) optionalValue[T] {
	if p == nil {
		return optionalValue[T]{}
	}
	return optionalValue[T]{present: true, value: *p}
}

type decodeKey struct {
	when       optionalValue[uint32]
	snr        int32
	dt         float64
	df         uint32
	mode, text optionalValue[string]
	low, off   optionalValue[bool]
}

func key(d Decode) decodeKey {
	return decodeKey{optional(d.TimeMS), d.SNR, d.DeltaTime, d.DeltaFrequency,
		optional(d.Mode), optional(d.Text), optional(d.LowConfidence), optional(d.OffAir)}
}

func (s *observedSession) addDecode(d Decode, now time.Time) {
	k := key(d)
	for i := len(s.decodes) - 1; i >= 0; i-- {
		r := &s.decodes[i]
		// Replays merge with a retained matching event. Live duplicates merge
		// only within FreshFor of first arrival, so the same time-of-day on a
		// later day is never silently treated as yesterday's event.
		if key(r.Data) == k && (!d.New || now.Sub(r.FirstSeen) < FreshFor) {
			r.LastSeen = now
			r.SeenLive = r.SeenLive || d.New
			r.SeenReplay = r.SeenReplay || !d.New
			s.counters.Duplicates++
			return
		}
	}
	r := ReceivedDecode{FirstSeen: now, LastSeen: now, Data: d, SeenLive: d.New, SeenReplay: !d.New}
	if len(s.decodes) == DecodeHistoryLimit {
		copy(s.decodes, s.decodes[1:])
		s.decodes[len(s.decodes)-1] = r
		s.counters.Evicted++
	} else {
		s.decodes = append(s.decodes, r)
	}
}

// Snapshot expires stale observations on read; no background timer is needed.
func (t *Tracker) Snapshot(radio string) (Snapshot, bool) {
	t.mu.Lock()
	defer t.mu.Unlock()
	s := t.sessions[radio]
	if s == nil {
		return Snapshot{}, false
	}
	s.expire(t.clock.Now())
	return s.snapshot(radio), true
}

// Snapshots returns a stable radio order and one consistent expiry instant.
func (t *Tracker) Snapshots() []Snapshot {
	t.mu.Lock()
	defer t.mu.Unlock()
	now := t.clock.Now()
	out := make([]Snapshot, 0, len(t.sessions))
	for radio, s := range t.sessions {
		s.expire(now)
		out = append(out, s.snapshot(radio))
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Radio < out[j].Radio })
	return out
}

func (s *observedSession) snapshot(radio string) Snapshot {
	out := Snapshot{Radio: radio, Generation: s.generation, Epoch: s.epoch, InstanceID: s.id,
		Availability: s.state, Schema: s.schema, Counters: s.counters,
		Heartbeat: cloneHeartbeat(s.heartbeat), Status: cloneStatus(s.status),
		Decodes: make([]ReceivedDecode, len(s.decodes))}
	if s.hasSeen {
		out.LastSeen = clone(&s.lastSeen)
	}
	if s.status != nil {
		out.StatusSeenAt = clone(&s.statusSeenAt)
	}
	for i, r := range s.decodes {
		r.Data = cloneDecode(r.Data)
		out.Decodes[i] = r
	}
	return out
}

func clone[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}

func cloneHeartbeat(h *Heartbeat) *Heartbeat {
	if h == nil {
		return nil
	}
	return &Heartbeat{MaxSchema: h.MaxSchema, Version: clone(h.Version), Revision: clone(h.Revision)}
}

func cloneStatus(s *Status) *Status {
	if s == nil {
		return nil
	}
	v := *s
	v.Mode, v.DXCall, v.Report, v.TXMode = clone(s.Mode), clone(s.DXCall), clone(s.Report), clone(s.TXMode)
	v.DECall, v.DEGrid, v.DXGrid = clone(s.DECall), clone(s.DEGrid), clone(s.DXGrid)
	v.TXWatchdog, v.SubMode, v.FastMode = clone(s.TXWatchdog), clone(s.SubMode), clone(s.FastMode)
	v.SpecialOperation, v.FrequencyTolerance, v.TRPeriod = clone(s.SpecialOperation), clone(s.FrequencyTolerance), clone(s.TRPeriod)
	v.ConfigurationName, v.TXMessage = clone(s.ConfigurationName), clone(s.TXMessage)
	return &v
}

func cloneDecode(d Decode) Decode {
	d.TimeMS, d.Mode, d.Text = clone(d.TimeMS), clone(d.Mode), clone(d.Text)
	d.LowConfidence, d.OffAir = clone(d.LowConfidence), clone(d.OffAir)
	return d
}
