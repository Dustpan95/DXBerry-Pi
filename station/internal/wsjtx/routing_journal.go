package wsjtx

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
)

const (
	MaxRoutingJournalBytes = 1024 * 1024
	routingJournalName     = "routes.v1"
	routingJournalPending  = "routes.pending"
)

type RoutingPhase string

const (
	RoutingPrepared  RoutingPhase = "prepared"
	RoutingInstalled RoutingPhase = "installed"
	RoutingRestoring RoutingPhase = "restoring"
)

var (
	ErrRoutingJournal          = errors.New("WSJT-X route journal unavailable")
	ErrRoutingJournalLayout    = errors.New("invalid WSJT-X route journal")
	ErrRoutingJournalLimit     = errors.New("WSJT-X route journal capacity exhausted")
	ErrRoutingJournalUncertain = errors.New("WSJT-X route journal outcome requires reconciliation")
	ErrRoutingJournalToken     = errors.New("retired WSJT-X route journal transaction")
	ErrRoutingJournalPhase     = errors.New("WSJT-X route journal transition refused")
	ErrRoutingJournalRecovery  = errors.New("WSJT-X route requires restoration after ownership changed")
	ErrRoutingJournalBusy      = errors.New("WSJT-X route already has an outstanding recovery record")
)

// JournalToken identifies an exact transaction in one open journal owner.
// Reopen, completion and replacement invalidate old callbacks.
type JournalToken struct {
	owner              *RoutingJournal
	radio, transaction string
}

// RoutingJournalSnapshot is private control-plane evidence, not program
// liveness or proof that settings were freshly read. Original destinations
// must not be logged or copied into unauthenticated status responses.
type RoutingJournalSnapshot struct {
	Radio, Transaction string
	Generation         uint64
	Phase              RoutingPhase
	RecoveryRequired   bool
	Original           RoutingSettings
	Managed            UDPSettings
}

type routingRecord struct {
	Transaction string          `json:"transaction"`
	Generation  uint64          `json:"generation"`
	Phase       RoutingPhase    `json:"phase"`
	Original    RoutingSettings `json:"original"`
	Managed     UDPSettings     `json:"managed"`
}

type routingJournalData struct {
	Version  int                      `json:"version"`
	StoreID  string                   `json:"store_id"`
	Revision uint64                   `json:"revision"`
	Records  map[string]routingRecord `json:"records"`
}

type routingJournalFile struct {
	Data     routingJournalData `json:"data"`
	Checksum string             `json:"sha256"`
}

// RoutingJournal retains settings ownership until exact restoration is
// observed. It performs no program, radio or INI operations. Callers must hold
// the program's lifecycle lock and supply fresh verified settings observations.
type RoutingJournal struct {
	mu       sync.Mutex
	state    *privateStateDir
	data     routingJournalData
	encoded  []byte
	bindings map[string]Binding // not persisted; reopened records require recovery
	closed   bool
	poisoned error
	write    func([]byte) error
}

func journalError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrRoutingJournal, err)
}

// CreateRoutingJournal initializes only a new private directory. Existing,
// missing or corrupt state is never replaced as automatic repair.
func CreateRoutingJournal(path string) error {
	s, err := openPrivateStateDir(path, true)
	if err != nil {
		return journalError(err)
	}
	defer s.close()
	data, err := encodeRoutingJournal(routingJournalData{Version: 1, StoreID: rand.Text(), Records: map[string]routingRecord{}})
	if err == nil {
		err = s.replace(routingJournalName, routingJournalPending, data)
	}
	if err == nil {
		err = s.syncParent()
	}
	return journalError(err)
}

func OpenRoutingJournal(path string) (*RoutingJournal, error) {
	s, err := openPrivateStateDir(path, false)
	if err != nil {
		return nil, journalError(err)
	}
	raw, err := readRoutingJournal(s)
	var data routingJournalData
	if err == nil {
		data, err = decodeRoutingJournal(raw)
	}
	if err != nil {
		_ = s.close()
		return nil, journalError(err)
	}
	j := &RoutingJournal{state: s, data: data, encoded: raw, bindings: make(map[string]Binding)}
	j.write = func(data []byte) error { return s.replace(routingJournalName, routingJournalPending, data) }
	return j, nil
}

func journalID(s string) bool {
	return len(s) >= 26 && len(s) <= 64 && strings.Trim(s, "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567") == ""
}

func validRoutingRecord(radio string, r routingRecord) bool {
	if !radioName.MatchString(radio) || !journalID(r.Transaction) || r.Generation == 0 || r.Original.Saved == nil || r.Original.validate() != nil {
		return false
	}
	if r.Phase != RoutingPrepared && r.Phase != RoutingInstalled && r.Phase != RoutingRestoring {
		return false
	}
	port, ok := configuredPort(r.Managed.Port)
	if !ok || port == 0 || !r.Managed.Port.Present || r.Managed.Port.Value != strconv.Itoa(int(port)) || r.Managed.Server != (RawSetting{true, "127.0.0.1"}) {
		return false
	}
	old, _ := configuredPort(r.Original.Current.Port)
	if old == port {
		return false
	}
	for _, saved := range r.Original.Saved {
		old, _ = configuredPort(saved.Port)
		if old == port {
			return false
		}
	}
	return true
}

func encodeRoutingJournal(data routingJournalData) ([]byte, error) {
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, ErrRoutingJournalLayout
	}
	sum := sha256.Sum256(payload)
	encoded, err := json.Marshal(routingJournalFile{data, hex.EncodeToString(sum[:])})
	if err != nil {
		return nil, ErrRoutingJournalLayout
	}
	if len(encoded)+1 > MaxRoutingJournalBytes {
		return nil, ErrRoutingJournalLimit
	}
	return append(encoded, '\n'), nil
}

func decodeRoutingJournal(raw []byte) (routingJournalData, error) {
	var file routingJournalFile
	if len(raw) == 0 || len(raw) > MaxRoutingJournalBytes {
		return file.Data, ErrRoutingJournalLayout
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&file); err != nil {
		return routingJournalData{}, ErrRoutingJournalLayout
	}
	d := file.Data
	if d.Version != 1 || !journalID(d.StoreID) || d.Records == nil || len(d.Records) > MaxSessions || d.Revision == 0 && len(d.Records) != 0 {
		return routingJournalData{}, ErrRoutingJournalLayout
	}
	seen := make(map[string]bool)
	for radio, r := range d.Records {
		if !validRoutingRecord(radio, r) || seen[r.Transaction] {
			return routingJournalData{}, ErrRoutingJournalLayout
		}
		seen[r.Transaction] = true
	}
	canonical, err := encodeRoutingJournal(d)
	if err != nil || !bytes.Equal(canonical, raw) {
		return routingJournalData{}, ErrRoutingJournalLayout
	}
	// Re-encoding also verifies the checksum and rejects duplicate fields,
	// case aliases, reordered/extra JSON values and noncanonical spelling.
	return d, nil
}

func readRoutingJournal(s *privateStateDir) ([]byte, error) {
	if err := s.checkIdentity(); err != nil {
		return nil, err
	}
	f, err := s.openFile(routingJournalName, syscall.O_RDONLY)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	return io.ReadAll(io.LimitReader(f, MaxRoutingJournalBytes+1))
}

func cloneJournal(d routingJournalData) routingJournalData {
	out := d
	out.Records = make(map[string]routingRecord, len(d.Records))
	for radio, r := range d.Records {
		r.Original = cloneRouting(r.Original)
		out.Records[radio] = r
	}
	return out
}

func (j *RoutingJournal) check() error {
	if j.closed {
		return journalError(os.ErrClosed)
	}
	if j.poisoned != nil {
		return j.poisoned
	}
	raw, err := readRoutingJournal(j.state)
	if err == nil && !bytes.Equal(raw, j.encoded) {
		err = errors.New("route journal changed outside its owner")
	}
	if err != nil {
		j.poisoned = journalError(err)
		return j.poisoned
	}
	return nil
}

func (j *RoutingJournal) commit(next routingJournalData) error {
	if err := j.check(); err != nil {
		return err
	}
	if j.data.Revision == math.MaxUint64 {
		return ErrRoutingJournalLimit
	}
	next.Revision = j.data.Revision + 1
	raw, err := encodeRoutingJournal(next)
	if err != nil {
		return err
	}
	if err = j.write(raw); err == nil {
		var verified []byte
		verified, err = readRoutingJournal(j.state)
		if err == nil && !bytes.Equal(raw, verified) {
			err = errors.New("route journal read-back differs")
		}
	}
	if err != nil {
		j.poisoned = fmt.Errorf("%w: %w", ErrRoutingJournalUncertain, err)
		return j.poisoned
	}
	j.data, j.encoded = next, raw
	return nil
}

func persistentRoutingBinding(b Binding) error {
	if b.owner == nil {
		return ErrRoutingBinding
	}
	o := b.owner
	o.mu.Lock()
	defer o.mu.Unlock()
	r := o.receivers[b.Radio()]
	if o.closed || r == nil || r.token != b.token || r.addr != b.addr || o.history == nil {
		return ErrRoutingBinding
	}
	select {
	case <-r.stopCh:
		return ErrRoutingBinding
	default:
	}
	if err := o.history.check(); err != nil {
		return err
	}
	port := b.Address().Port()
	if o.history.bits[port/64]&(uint64(1)<<(port%64)) == 0 {
		return ErrRoutingBinding
	}
	return nil
}

func (j *RoutingJournal) Prepare(lease *RoutingLease, b Binding) (JournalToken, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.check(); err != nil {
		return JournalToken{}, err
	}
	if lease == nil || !lease.valid || lease.binding != b || lease.radio != b.Radio() || lease.generation != b.Generation() || lease.managed.Port.Value != strconv.Itoa(int(b.Address().Port())) {
		return JournalToken{}, ErrRoutingLease
	}
	if _, exists := j.data.Records[lease.radio]; exists {
		return JournalToken{}, ErrRoutingJournalBusy
	}
	if len(j.data.Records) >= MaxSessions {
		return JournalToken{}, ErrRoutingJournalLimit
	}
	if err := persistentRoutingBinding(b); err != nil {
		return JournalToken{}, err
	}
	r := routingRecord{rand.Text(), lease.generation, RoutingPrepared, cloneRouting(lease.original), lease.managed}
	if !validRoutingRecord(lease.radio, r) {
		return JournalToken{}, ErrRoutingLease
	}
	next := cloneJournal(j.data)
	next.Records[lease.radio] = r
	if err := j.commit(next); err != nil {
		return JournalToken{}, err
	}
	j.bindings[lease.radio] = b
	return JournalToken{j, lease.radio, r.Transaction}, nil
}

func (j *RoutingJournal) record(token JournalToken) (routingRecord, error) {
	if err := j.check(); err != nil {
		return routingRecord{}, err
	}
	r, ok := j.data.Records[token.radio]
	if token.owner != j || !ok || token.transaction != r.Transaction {
		return routingRecord{}, ErrRoutingJournalToken
	}
	return r, nil
}

// PermitInstall/PermitRestore prove the journal phase only. The caller still
// must enforce program quiescence and lifecycle serialization; these methods
// alone do not authorize a station settings write.
func (j *RoutingJournal) permitInstall(token JournalToken) error {
	r, err := j.record(token)
	if err != nil {
		return err
	}
	if r.Phase != RoutingPrepared {
		return ErrRoutingJournalPhase
	}
	b, ok := j.bindings[token.radio]
	if !ok {
		return ErrRoutingJournalRecovery
	}
	return persistentRoutingBinding(b)
}

func (j *RoutingJournal) PermitInstall(token JournalToken) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.permitInstall(token)
}

func (j *RoutingJournal) PermitRestore(token JournalToken) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	r, err := j.record(token)
	if err != nil {
		return err
	}
	if r.Phase != RoutingRestoring {
		return ErrRoutingJournalPhase
	}
	return nil
}

func managedSettings(r routingRecord) RoutingSettings {
	out := cloneRouting(r.Original)
	out.Current = r.Managed
	return out
}

func (j *RoutingJournal) MarkInstalled(token JournalToken, observed RoutingSettings) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	r, err := j.record(token)
	if err != nil {
		return err
	}
	if r.Phase != RoutingPrepared && r.Phase != RoutingInstalled {
		return ErrRoutingJournalPhase
	}
	b, ok := j.bindings[token.radio]
	if !ok {
		return ErrRoutingJournalRecovery
	}
	if err := persistentRoutingBinding(b); err != nil {
		return err
	}
	if observed.validate() != nil || observed.fingerprint() != managedSettings(r).fingerprint() {
		return ErrRoutingConflict
	}
	if r.Phase == RoutingInstalled {
		return nil
	}
	r.Phase = RoutingInstalled
	next := cloneJournal(j.data)
	next.Records[token.radio] = r
	return j.commit(next)
}

func (j *RoutingJournal) PrepareRestore(token JournalToken, observed RoutingSettings) (RoutingPatch, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	r, err := j.record(token)
	if err != nil {
		return RoutingPatch{}, err
	}
	lease := &RoutingLease{radio: token.radio, generation: r.Generation, original: r.Original, managed: r.Managed, valid: true}
	patch, err := lease.Restore(observed)
	if err != nil {
		return RoutingPatch{}, err
	}
	if r.Phase != RoutingRestoring {
		r.Phase = RoutingRestoring
		next := cloneJournal(j.data)
		next.Records[token.radio] = r
		if err := j.commit(next); err != nil {
			return RoutingPatch{}, err
		}
	}
	return patch, nil
}

func (j *RoutingJournal) CompleteRestore(token JournalToken, observed RoutingSettings) error {
	j.mu.Lock()
	defer j.mu.Unlock()
	r, err := j.record(token)
	if err != nil {
		return err
	}
	if r.Phase != RoutingRestoring {
		return ErrRoutingJournalPhase
	}
	if observed.validate() != nil || observed.fingerprint() != r.Original.fingerprint() {
		return ErrRoutingConflict
	}
	next := cloneJournal(j.data)
	delete(next.Records, token.radio)
	if err := j.commit(next); err != nil {
		return err
	}
	delete(j.bindings, token.radio)
	return nil
}

func (j *RoutingJournal) snapshot(radio string, r routingRecord) RoutingJournalSnapshot {
	b, fresh := j.bindings[radio]
	return RoutingJournalSnapshot{radio, r.Transaction, r.Generation, r.Phase, !fresh || !currentRoutingBinding(b), cloneRouting(r.Original), r.Managed}
}

func (j *RoutingJournal) Lookup(radio string) (JournalToken, RoutingJournalSnapshot, bool, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.check(); err != nil {
		return JournalToken{}, RoutingJournalSnapshot{}, false, err
	}
	r, ok := j.data.Records[radio]
	if !ok {
		return JournalToken{}, RoutingJournalSnapshot{}, false, nil
	}
	return JournalToken{j, radio, r.Transaction}, j.snapshot(radio, r), true, nil
}

func (j *RoutingJournal) Snapshots() ([]RoutingJournalSnapshot, error) {
	j.mu.Lock()
	defer j.mu.Unlock()
	if err := j.check(); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(j.data.Records))
	for radio := range j.data.Records {
		names = append(names, radio)
	}
	sort.Strings(names)
	out := make([]RoutingJournalSnapshot, 0, len(names))
	for _, radio := range names {
		out = append(out, j.snapshot(radio, j.data.Records[radio]))
	}
	return out, nil
}

func (j *RoutingJournal) Close() error {
	j.mu.Lock()
	defer j.mu.Unlock()
	if j.closed {
		return nil
	}
	j.closed = true
	return j.state.close()
}
