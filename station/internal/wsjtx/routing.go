package wsjtx

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	MaxSavedProfiles = 32
	MaxRoutingValue  = 4096
)

var (
	ErrRoutingLayout   = errors.New("unsupported WSJT-X routing settings")
	ErrRoutingApproval = errors.New("WSJT-X routing preview must be approved")
	ErrRoutingChanged  = errors.New("WSJT-X routing changed since preview")
	ErrRoutingConflict = errors.New("WSJT-X routing changed during collection; restoration needs review")
	ErrRoutingBinding  = errors.New("WSJT-X routing binding is no longer current")
	ErrRoutingEndpoint = errors.New("WSJT-X receiver port is already configured; allocate a fresh binding")
	ErrRoutingLease    = errors.New("invalid WSJT-X routing lease")
)

// RawSetting keeps absence distinct from an explicitly empty value. Value is
// the raw single-line Qt representation, so restoration retains its spelling.
type RawSetting struct {
	Present bool
	Value   string
}

type UDPSettings struct {
	Server RawSetting
	Port   RawSetting
}

// RoutingSettings is the complete logical routing namespace, after a settings
// reader has checked Qt aliases/duplicates and identified the active profile.
// Saved profiles must all be included, even when their UDP keys are absent.
// This type alone does not prove that a raw INI file was interpreted correctly.
type RoutingSettings struct {
	Profile string
	Current UDPSettings
	Saved   map[string]UDPSettings
}

// RoutingPreview describes the existing destination without claiming that a
// default or absent destination is unused by another integration. Approval is
// always required, and its fingerprint is bound to this receiver generation.
type RoutingPreview struct {
	Fingerprint string
	Radio       string
	Generation  uint64
	Profile     string
	Existing    UDPSettings
}

// RoutingPatch is a compare-and-swap plan for the two current UDP keys only.
// It performs no I/O. Its future writer must run as the application user while
// the program is stopped, recheck the complete settings fingerprint under its
// lifecycle/configuration lock, and replace both keys in one file transaction.
// An acquiring writer must also recheck that the binding is still current.
type RoutingPatch struct {
	radio      string
	generation uint64
	profile    string
	before     string
	after      UDPSettings
}

func (p RoutingPatch) Radio() string               { return p.radio }
func (p RoutingPatch) Generation() uint64          { return p.generation }
func (p RoutingPatch) Profile() string             { return p.profile }
func (p RoutingPatch) ExpectedFingerprint() string { return p.before }
func (p RoutingPatch) Replacement() UDPSettings    { return p.after }

// RoutingLease is an immutable in-memory restoration plan. It is not a durable
// lease journal and cannot establish that any settings have been written.
type RoutingLease struct {
	radio      string
	generation uint64
	original   RoutingSettings
	managed    UDPSettings
	valid      bool
	binding    Binding
}

func routingName(s string) bool {
	return len(s) > 0 && len(s) <= MaxIDSize && utf8.ValidString(s) && !strings.ContainsAny(s, "\x00\r\n/\\")
}

func (s RoutingSettings) validate() error {
	if !routingName(s.Profile) || len(s.Saved) > MaxSavedProfiles {
		return ErrRoutingLayout
	}
	valid := func(v RawSetting) bool {
		return (v.Present || v.Value == "") && len(v.Value) <= MaxRoutingValue && utf8.ValidString(v.Value) && !strings.ContainsAny(v.Value, "\x00\r\n")
	}
	validPair := func(p UDPSettings) bool {
		if !valid(p.Server) || !valid(p.Port) {
			return false
		}
		_, portOK := configuredPort(p.Port)
		return portOK
	}
	if !validPair(s.Current) {
		return ErrRoutingLayout
	}
	for name, pair := range s.Saved {
		if !routingName(name) || name == s.Profile || !validPair(pair) {
			return ErrRoutingLayout
		}
	}
	return nil
}

// Qt's absent-port default is verified against 3.0.2. Only plain decimal
// ports (or an explicit empty/zero value, which disables sending) are accepted
// in this first settings slice. Unknown encodings are refused, never guessed.
func configuredPort(v RawSetting) (uint16, bool) {
	if len(v.Value) > MaxRoutingValue {
		return 0, false
	}
	if !v.Present {
		return 2237, v.Value == ""
	}
	if v.Value == "" {
		return 0, true
	}
	for _, c := range v.Value {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	port, err := strconv.ParseUint(v.Value, 10, 16)
	return uint16(port), err == nil
}

func cloneRouting(s RoutingSettings) RoutingSettings {
	out := s
	// Normalize nil and empty maps to the same complete, empty namespace.
	out.Saved = make(map[string]UDPSettings, len(s.Saved))
	for name, pair := range s.Saved {
		out.Saved[name] = pair
	}
	return out
}

func routingDigest(value any) string {
	// The only callers pass bounded structs/maps of strings, booleans and
	// integers; encoding cannot fail. encoding/json sorts map keys.
	data, _ := json.Marshal(value)
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func (s RoutingSettings) fingerprint() string { return routingDigest(cloneRouting(s)) }

func currentRoutingBinding(b Binding) bool {
	if b.owner == nil {
		return false
	}
	o := b.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	r := o.receivers[b.token.radio]
	if o.closed || r == nil || r.token != b.token || r.addr != b.addr {
		return false
	}
	select {
	case <-r.stopCh:
		return false
	default:
		return true
	}
}

func PreviewRouting(b Binding, settings RoutingSettings) (RoutingPreview, error) {
	if err := settings.validate(); err != nil {
		return RoutingPreview{}, err
	}
	if !currentRoutingBinding(b) {
		return RoutingPreview{}, ErrRoutingBinding
	}
	// A never-published port can still be an existing program destination.
	// Exclude it independently of host spelling or current profile activity.
	port, _ := configuredPort(settings.Current.Port)
	if port == b.Address().Port() {
		return RoutingPreview{}, ErrRoutingEndpoint
	}
	for _, saved := range settings.Saved {
		port, _ := configuredPort(saved.Port)
		if port == b.Address().Port() {
			return RoutingPreview{}, ErrRoutingEndpoint
		}
	}
	fingerprint := routingDigest(struct {
		Instance   string
		Radio      string
		Generation uint64
		Address    string
		Settings   RoutingSettings
	}{b.owner.instance, b.Radio(), b.Generation(), b.Address().String(), cloneRouting(settings)})
	return RoutingPreview{fingerprint, b.Radio(), b.Generation(), settings.Profile, settings.Current}, nil
}

// ClaimRouting requires an explicit approval of this exact preview, including
// all saved-profile routes. It plans only a loopback destination derived from
// the observer binding; callers cannot supply a host or port. It does not edit
// a program, acquire a transmitter, or mark a lease as applied.
func ClaimRouting(b Binding, settings RoutingSettings, approved string) (*RoutingLease, RoutingPatch, error) {
	preview, err := PreviewRouting(b, settings)
	if err != nil {
		return nil, RoutingPatch{}, err
	}
	if approved == "" {
		return nil, RoutingPatch{}, ErrRoutingApproval
	}
	if approved != preview.Fingerprint {
		return nil, RoutingPatch{}, ErrRoutingChanged
	}
	managed := UDPSettings{
		Server: RawSetting{true, "127.0.0.1"},
		Port:   RawSetting{true, strconv.Itoa(int(b.Address().Port()))},
	}
	lease := &RoutingLease{radio: b.Radio(), generation: b.Generation(), original: cloneRouting(settings), managed: managed, valid: true, binding: b}
	return lease, RoutingPatch{b.Radio(), b.Generation(), settings.Profile, settings.fingerprint(), managed}, nil
}

// Apply computes a replacement view only if the entire routing namespace
// still matches the plan. A filesystem writer must provide the same check at
// its atomic write boundary; this method does not make disk operations atomic.
func (p RoutingPatch) Apply(current RoutingSettings) (RoutingSettings, error) {
	if err := current.validate(); err != nil {
		return RoutingSettings{}, err
	}
	if p.before == "" || p.profile != current.Profile || p.before != current.fingerprint() {
		return RoutingSettings{}, ErrRoutingChanged
	}
	out := cloneRouting(current)
	out.Current = p.after
	if err := out.validate(); err != nil {
		return RoutingSettings{}, err
	}
	return out, nil
}

// Restore refuses profile switches, clones, renames, deletions and route edits
// instead of guessing where leased settings moved. It restores both keys as a
// pair, preserving original absence/empty values and leaving saved profiles
// alone. An already-original view is an idempotent no-change plan. Unrelated
// settings are outside the view and must be preserved by the future writer.
func (l *RoutingLease) Restore(current RoutingSettings) (RoutingPatch, error) {
	if l == nil || !l.valid {
		return RoutingPatch{}, ErrRoutingLease
	}
	if err := current.validate(); err != nil {
		return RoutingPatch{}, err
	}
	expected := cloneRouting(l.original)
	expected.Current = l.managed
	fingerprint := current.fingerprint()
	if fingerprint != expected.fingerprint() && fingerprint != l.original.fingerprint() {
		return RoutingPatch{}, ErrRoutingConflict
	}
	return RoutingPatch{l.radio, l.generation, current.Profile, fingerprint, l.original.Current}, nil
}
