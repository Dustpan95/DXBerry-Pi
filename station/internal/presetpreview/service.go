// Package presetpreview produces read-only frequency comparisons. It has no
// radio write, owner acquisition, program lifecycle or recall operation.
package presetpreview

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/presets"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/rig"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/session"
)

const (
	Lifetime    = 2 * time.Second
	MaxTokens   = 256
	MaxInFlight = 16
)

var (
	ErrInvalid        = errors.New("invalid preset preview request")
	ErrUnavailable    = errors.New("preset preview evidence is unavailable")
	ErrConflict       = errors.New("preset preview evidence changed; preview again")
	ErrNotFound       = errors.New("preset or registered radio not found")
	ErrNotOwner       = errors.New("this tab does not hold the radio")
	ErrProgramRunning = errors.New("a program holds the radio")
	ErrTransmitting   = errors.New("the radio is transmitting")
	ErrUnsupported    = errors.New("preset frequency or radio observation is unsupported")
	ErrBusy           = errors.New("preset preview is busy; retry shortly")
	ErrExpired        = errors.New("preset preview expired; preview again")
	radioName         = regexp.MustCompile(`^[a-z][a-z0-9]{0,11}$`)
	loginName         = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)
)

// Principal comes from trusted authenticated server code, never a request body.
// The HTTP adapter must still verify the live login on every call.
type Principal struct{ Login, Tab string }
type Request struct{ Radio, ID, Revision string }

// TargetSource must freshly verify the exact registration's effective radio
// route/poller identity and authoritative program-free state on EACH call.
// Identity must change across route or program ownership lifecycle changes,
// including a change and return. Unknown evidence is an error, never "free".
// Cached Watcher.Get, Programs.Holder or reduced Radio equality is insufficient.
// Implementations must obey context cancellation and issue no control writes.
// No production implementation or fallback is supplied by this package.
type TargetSource interface {
	Inspect(context.Context, radios.Registration) (Target, error)
}

// Target is private server evidence. Identity may include sensitive metadata;
// it and Caps are never copied into Preview or logged by this service.
type Target struct {
	Poller   *rig.Poller
	Caps     rig.Caps
	Identity string
}

type Config struct {
	Definitions   *presets.Store
	Registrations *radios.RegistrationReader
	Owners        *session.Owners
	Targets       TargetSource
	Now           func() time.Time
}

// Preview is the explicit public field set. Its token is advisory read evidence,
// not a reservation, ownership grant or authorization for a future CAT write.
type Preview struct {
	Radio                string `json:"radio"`
	PresetID             string `json:"preset_id"`
	Revision             string `json:"revision"`
	Label                string `json:"label"`
	VFO                  string `json:"vfo"`
	CurrentFrequencyHz   int64  `json:"current_frequency_hz"`
	RequestedFrequencyHz int64  `json:"requested_frequency_hz"`
	DeltaHz              int64  `json:"delta_hz"`
	Token                string `json:"token"`
	ExpiresInMS          int64  `json:"expires_in_ms"`
}

type key struct {
	principal Principal
	radio     string
}
type evidence struct {
	key          key
	request      Request
	definition   presets.Definition
	registration radios.Registration
	claim        session.TabClaimObservation
	target       Target
	reading      rig.Observation
	started      time.Time
}

type Service struct {
	initialized bool
	config      Config
	random      io.Reader
	mu          sync.Mutex
	tokens      map[string]evidence
	active      map[key]bool
}

func New(config Config) (*Service, error) {
	if config.Definitions == nil || config.Registrations == nil || config.Owners == nil || missingSource(config.Targets) {
		return nil, ErrUnavailable
	}
	if config.Now == nil {
		config.Now = time.Now
	}
	return &Service{initialized: true, config: config, random: rand.Reader, tokens: map[string]evidence{}, active: map[key]bool{}}, nil
}

func missingSource(source TargetSource) bool {
	if source == nil {
		return true
	}
	v := reflect.ValueOf(source)
	switch v.Kind() {
	case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Func, reflect.Chan, reflect.Interface:
		return v.IsNil()
	}
	return false
}

func validPrincipal(p Principal) bool {
	if !loginName.MatchString(p.Login) || p.Tab == "" || len(p.Tab) > 256 || !utf8.ValidString(p.Tab) {
		return false
	}
	for _, c := range p.Tab {
		if unicode.IsControl(c) {
			return false
		}
	}
	return true
}
func hexID(s string, n int) bool { return len(s) == n && strings.Trim(s, "0123456789abcdef") == "" }
func validRequest(r Request) bool {
	return radioName.MatchString(r.Radio) && hexID(r.ID, 32) && hexID(r.Revision, 64)
}

// Preview loads the selected server-side definition; callers cannot supply a
// frequency, executable, selector, process identity or file path.
func (s *Service) Preview(ctx context.Context, principal Principal, request Request) (Preview, error) {
	if s == nil || !s.initialized {
		return Preview{}, ErrUnavailable
	}
	if !validPrincipal(principal) || !validRequest(request) {
		return Preview{}, ErrInvalid
	}
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	k := key{principal, request.Radio}
	if !s.begin(k, true) {
		return Preview{}, ErrBusy
	}
	defer s.end(k)
	// Obtain entropy before reading the radio, so it cannot delay publication
	// of already collected evidence. A collision refuses rather than overwrites.
	var bytes [32]byte
	if _, err := io.ReadFull(s.random, bytes[:]); err != nil {
		return Preview{}, ErrUnavailable
	}
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	token := hex.EncodeToString(bytes[:])
	e, err := s.inspect(ctx, principal, request)
	if err != nil {
		return Preview{}, err
	}
	s.mu.Lock()
	now := s.config.Now()
	s.pruneLocked(now)
	if ctx.Err() != nil {
		s.mu.Unlock()
		return Preview{}, ctx.Err()
	}
	if !fresh(now, e.started) {
		s.mu.Unlock()
		return Preview{}, ErrExpired
	}
	if !s.localCurrent(e) {
		s.mu.Unlock()
		return Preview{}, ErrConflict
	}
	// Local mutex acquisition can itself take time. Sample the deadline after
	// those checks too, rather than publishing with a pre-wait timestamp.
	now = s.config.Now()
	if ctx.Err() != nil {
		s.mu.Unlock()
		return Preview{}, ctx.Err()
	}
	if !fresh(now, e.started) {
		s.mu.Unlock()
		return Preview{}, ErrExpired
	}
	if len(s.tokens) >= MaxTokens {
		s.mu.Unlock()
		return Preview{}, ErrBusy
	}
	if _, exists := s.tokens[token]; !exists {
		s.tokens[token] = e
		s.mu.Unlock()
		return view(e, token, now), nil
	}
	s.mu.Unlock()
	return Preview{}, ErrUnavailable
}

// Recheck performs fresh reads again and rejects changes since the original
// preview. It never extends the original lifetime or consumes a write permit:
// there is no write permit here. Recall needs separate guarded implementation.
func (s *Service) Recheck(ctx context.Context, principal Principal, radio, token string) (Preview, error) {
	if s == nil || !s.initialized {
		return Preview{}, ErrUnavailable
	}
	if !validPrincipal(principal) || !radioName.MatchString(radio) || !hexID(token, 64) {
		return Preview{}, ErrExpired
	}
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	s.mu.Lock()
	s.pruneLocked(s.config.Now())
	old, exists := s.tokens[token]
	s.mu.Unlock()
	if !exists || old.key.principal != principal || old.key.radio != radio {
		return Preview{}, ErrExpired
	}
	if !s.begin(old.key, false) {
		return Preview{}, ErrBusy
	}
	defer s.end(old.key)
	ctx, cancel := context.WithTimeout(ctx, Lifetime-s.config.Now().Sub(old.started))
	defer cancel()
	current, err := s.inspect(ctx, principal, old.request)
	if err == nil && (!old.registration.Same(current.registration) || old.claim != current.claim ||
		!sameTarget(old.target, current.target) || old.definition != current.definition ||
		old.reading.FrequencyHz() != current.reading.FrequencyHz() || old.reading.VFO() != current.reading.VFO() ||
		!old.target.Poller.ObservationCurrent(old.reading)) {
		err = ErrConflict
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	now := s.config.Now()
	_, exists = s.tokens[token]
	if err != nil {
		delete(s.tokens, token)
		return Preview{}, err
	}
	if ctx.Err() != nil {
		delete(s.tokens, token)
		return Preview{}, ctx.Err()
	}
	if !exists || !fresh(now, old.started) {
		delete(s.tokens, token)
		return Preview{}, ErrExpired
	}
	if !s.localCurrent(old) {
		delete(s.tokens, token)
		return Preview{}, ErrConflict
	}
	now = s.config.Now()
	if ctx.Err() != nil {
		delete(s.tokens, token)
		return Preview{}, ctx.Err()
	}
	if !fresh(now, old.started) {
		delete(s.tokens, token)
		return Preview{}, ErrExpired
	}
	return view(old, token, now), nil
}

func (s *Service) inspect(ctx context.Context, principal Principal, request Request) (evidence, error) {
	e := evidence{key: key{principal, request.Radio}, request: request, started: s.config.Now()}
	ctx, cancel := context.WithTimeout(ctx, Lifetime)
	defer cancel()
	fail := func(err error) (evidence, error) {
		if ctx.Err() != nil {
			return evidence{}, ctx.Err()
		}
		return evidence{}, classify(err)
	}
	definition, err := s.definition(ctx, request)
	if err != nil {
		return fail(err)
	}
	e.definition = definition
	e.registration, err = s.config.Registrations.Read(ctx, request.Radio)
	if err != nil {
		return fail(err)
	}
	var ok bool
	e.claim, ok = s.config.Owners.ObserveTabClaim(request.Radio, session.TabID(principal.Login, principal.Tab))
	if !ok {
		return fail(ErrNotOwner)
	}
	e.target, err = s.target(ctx, e.registration, definition.FrequencyHz)
	if err != nil {
		return fail(err)
	}
	e.reading, err = e.target.Poller.Observe(ctx)
	if err != nil {
		return fail(err)
	}
	after, err := s.target(ctx, e.registration, definition.FrequencyHz)
	if err != nil {
		return fail(err)
	}
	if !sameTarget(e.target, after) {
		return fail(ErrConflict)
	}
	registration, err := s.config.Registrations.Read(ctx, request.Radio)
	if err != nil {
		return fail(err)
	}
	if !e.registration.Same(registration) {
		return fail(ErrConflict)
	}
	latest, err := s.definition(ctx, request)
	if err != nil {
		return fail(err)
	}
	if latest != definition || !s.config.Owners.TabClaimCurrent(e.claim) || !e.target.Poller.ObservationCurrent(e.reading) || !capsMatch(e.target) {
		return fail(ErrConflict)
	}
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	if !fresh(s.config.Now(), e.started) {
		return fail(ErrExpired)
	}
	return e, nil
}

func (s *Service) definition(ctx context.Context, request Request) (presets.Definition, error) {
	l, err := s.config.Definitions.List(ctx, request.Radio)
	if err != nil {
		return presets.Definition{}, err
	}
	if l.Revision != request.Revision {
		return presets.Definition{}, ErrConflict
	}
	for _, d := range l.Presets {
		if d.ID == request.ID {
			return d, nil
		}
	}
	return presets.Definition{}, ErrNotFound
}

func (s *Service) target(ctx context.Context, registration radios.Registration, hz int64) (Target, error) {
	t, err := s.config.Targets.Inspect(ctx, registration)
	if err != nil {
		return Target{}, err
	}
	if t.Poller == nil || t.Identity == "" || len(t.Identity) > 4096 || !capsMatch(t) {
		return Target{}, ErrUnavailable
	}
	t.Caps = cloneCaps(t.Caps)
	if !t.Caps.CanGetPTT || !t.Caps.CanGetVFO || !t.Caps.InRange(hz) {
		return Target{}, ErrUnsupported
	}
	return t, nil
}

func capsMatch(t Target) bool {
	if t.Poller == nil {
		return false
	}
	caps := t.Poller.State().Caps
	return caps != nil && reflect.DeepEqual(*caps, t.Caps)
}
func sameTarget(a, b Target) bool {
	return a.Poller == b.Poller && a.Identity == b.Identity && reflect.DeepEqual(a.Caps, b.Caps)
}
func (s *Service) localCurrent(e evidence) bool {
	return s.config.Owners.TabClaimCurrent(e.claim) && e.target.Poller.ObservationCurrent(e.reading) && capsMatch(e.target)
}
func cloneCaps(c rig.Caps) rig.Caps {
	c.Modes = slices.Clone(c.Modes)
	c.VFOs = slices.Clone(c.VFOs)
	c.SettableLevels = slices.Clone(c.SettableLevels)
	c.SettableFuncs = slices.Clone(c.SettableFuncs)
	c.ReadableMeters = slices.Clone(c.ReadableMeters)
	c.Hidden = slices.Clone(c.Hidden)
	c.FreqRanges = slices.Clone(c.FreqRanges)
	return c
}
func fresh(now, started time.Time) bool {
	age := now.Sub(started)
	return !started.IsZero() && age >= 0 && age < Lifetime
}
func view(e evidence, token string, now time.Time) Preview {
	return Preview{
		Radio: e.request.Radio, PresetID: e.definition.ID, Revision: e.request.Revision, Label: e.definition.Label, VFO: e.reading.VFO(),
		CurrentFrequencyHz: e.reading.FrequencyHz(), RequestedFrequencyHz: e.definition.FrequencyHz, DeltaHz: e.definition.FrequencyHz - e.reading.FrequencyHz(),
		Token: token, ExpiresInMS: Lifetime.Milliseconds() - now.Sub(e.started).Milliseconds(),
	}
}

func (s *Service) begin(k key, invalidate bool) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pruneLocked(s.config.Now())
	if s.active[k] || len(s.active) >= MaxInFlight {
		return false
	}
	s.active[k] = true
	if invalidate {
		for token, e := range s.tokens {
			if e.key == k {
				delete(s.tokens, token)
			}
		}
	}
	return true
}
func (s *Service) end(k key) { s.mu.Lock(); delete(s.active, k); s.mu.Unlock() }
func (s *Service) pruneLocked(now time.Time) {
	for token, e := range s.tokens {
		if !fresh(now, e.started) {
			delete(s.tokens, token)
		}
	}
}

func classify(err error) error {
	for _, known := range []error{ErrInvalid, ErrUnavailable, ErrConflict, ErrNotFound, ErrNotOwner, ErrProgramRunning, ErrTransmitting, ErrUnsupported, ErrBusy, ErrExpired, context.Canceled, context.DeadlineExceeded} {
		if errors.Is(err, known) {
			return known
		}
	}
	switch {
	case errors.Is(err, radios.ErrRegistrationNotFound), errors.Is(err, presets.ErrNotFound):
		return ErrNotFound
	case errors.Is(err, rig.ErrObservationTransmitting):
		return ErrTransmitting
	case errors.Is(err, rig.ErrObservationChanged):
		return ErrConflict
	case errors.Is(err, rig.ErrObservationUnsupported):
		return ErrUnsupported
	case errors.Is(err, rig.ErrObservationBusy):
		return ErrBusy
	default:
		return ErrUnavailable
	}
}
