// Package settings keeps the per-radio station settings: the SWR limit and the PTT timeout
// that the transmit guards read, and when the radio's capabilities were last probed.
package settings

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"
)

// Defaults and limits.
const (
	DefaultSWRLimit    = 3.0
	DefaultPTTTimeoutS = 180
	MinSWR, MaxSWR     = 1.5, 10.0
	MinPTT, MaxPTT     = 30, 600
)

var nameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// RadioSettings is one radio's file, <Dir>/<name>.json.
type RadioSettings struct {
	SWRLimit             float64   `json:"swr_limit"`
	PTTTimeoutS          int       `json:"ptt_timeout_s"`
	Probed               time.Time `json:"probed"`
	WSJTXRoutingApproval string    `json:"wsjtx_routing_approval,omitempty"`
}

// Defaults is what a radio without a file gets.
func Defaults() RadioSettings {
	return RadioSettings{SWRLimit: DefaultSWRLimit, PTTTimeoutS: DefaultPTTTimeoutS}
}

// Validate says what is wrong with s, naming the valid range, or returns nil.
func (s RadioSettings) Validate() error {
	if err := s.validateGuards(); err != nil {
		return err
	}
	if a := s.WSJTXRoutingApproval; a != "" && (len(a) != 64 || strings.Trim(a, "0123456789abcdef") != "") {
		return fmt.Errorf("invalid WSJT-X routing approval")
	}
	return nil
}

func (s RadioSettings) validateGuards() error {
	if !(s.SWRLimit >= MinSWR && s.SWRLimit <= MaxSWR) {
		return fmt.Errorf("the SWR limit must be between 1.5 and 10")
	}
	if s.PTTTimeoutS < MinPTT || s.PTTTimeoutS > MaxPTT {
		return fmt.Errorf("the PTT timeout must be between 30 and 600 seconds")
	}
	return nil
}

// ReadStrict preserves absence as defaults but refuses damaged, ambiguous or
// unsafe existing files. Configuration ownership must never infer disabled
// collection from the fallback values used by the ordinary guard reader.
func (st *Store) ReadStrict(name string) (RadioSettings, error) {
	if !nameRe.MatchString(name) {
		return RadioSettings{}, fmt.Errorf("no such radio")
	}
	fd, err := syscall.Open(filepath.Join(st.Dir, name+".json"), syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if os.IsNotExist(err) {
		return Defaults(), nil
	}
	if err != nil {
		return RadioSettings{}, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return RadioSettings{}, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Uid != uint32(os.Geteuid()) || stat.Nlink != 1 {
		return RadioSettings{}, fmt.Errorf("unsafe radio settings file")
	}
	b, err := io.ReadAll(io.LimitReader(f, 16*1024+1))
	if err != nil || len(b) > 16*1024 {
		return RadioSettings{}, fmt.Errorf("radio settings unreadable or too large")
	}
	decoder := json.NewDecoder(bytes.NewReader(b))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return RadioSettings{}, fmt.Errorf("invalid radio settings")
	}
	s := Defaults()
	fields := map[string]any{"swr_limit": &s.SWRLimit, "ptt_timeout_s": &s.PTTTimeoutS, "probed": &s.Probed, "wsjtx_routing_approval": &s.WSJTXRoutingApproval}
	seen := map[string]bool{}
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		field, known := fields[key]
		if err != nil || !ok || !known || seen[key] {
			return RadioSettings{}, fmt.Errorf("ambiguous radio settings")
		}
		seen[key] = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
			return RadioSettings{}, fmt.Errorf("invalid radio settings field")
		}
		if err := json.Unmarshal(raw, field); err != nil {
			return RadioSettings{}, fmt.Errorf("invalid radio settings field")
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return RadioSettings{}, fmt.Errorf("invalid radio settings")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return RadioSettings{}, fmt.Errorf("extra radio settings data")
	}
	return s, s.Validate()
}

func (st *Store) RoutingApproval(name string) (string, error) {
	s, err := st.ReadStrict(name)
	return s.WSJTXRoutingApproval, err
}

// HasRoutingApprovals is used only when release audit finds no inventory. A
// retained consent or unreadable preference must not hide lost configured state.
func (st *Store) HasRoutingApprovals() (bool, error) {
	f, err := os.Open(st.Dir)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	defer f.Close()
	entries, err := f.ReadDir(1025)
	if err != nil && err != io.EOF || len(entries) > 1024 {
		return false, fmt.Errorf("radio settings inventory unavailable")
	}
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		approval, err := st.RoutingApproval(name)
		if err != nil {
			return false, err
		}
		if approval != "" {
			return true, nil
		}
	}
	return false, nil
}

// SaveRoutingApproval compares only the old approval, while preserving the
// latest transmit limits/probe time under the same lock as ordinary writers.
func (st *Store) SaveRoutingApproval(name, expected, approved string) error {
	st.mu.Lock()
	defer st.mu.Unlock()
	s, err := st.ReadStrict(name)
	if err != nil {
		return err
	}
	if s.WSJTXRoutingApproval != expected {
		return fmt.Errorf("WSJT-X routing approval changed")
	}
	s.WSJTXRoutingApproval = approved
	if err := s.Validate(); err != nil {
		return err
	}
	return st.write(name, s)
}

// Store is the settings directory (<state>/radios).
type Store struct {
	Dir string

	mu     sync.Mutex // writers
	logMu  sync.Mutex
	logged map[string]bool
}

// Get returns the radio's guard settings, or the defaults when the file is absent
// or unreadable (logged once). Approval callers must use RoutingApproval or
// ReadStrict; this fallback reader never supplies collection consent.
func (st *Store) Get(name string) RadioSettings {
	if !nameRe.MatchString(name) {
		return Defaults()
	}
	return st.read(name)
}

// read is Get for a validated name; it takes no lock the writers hold.
func (st *Store) read(name string) RadioSettings {
	b, err := os.ReadFile(filepath.Join(st.Dir, name+".json"))
	if err != nil {
		if !os.IsNotExist(err) {
			st.warn(name, err)
		}
		return Defaults()
	}
	s := Defaults()
	guards := struct {
		SWRLimit    float64   `json:"swr_limit"`
		PTTTimeoutS int       `json:"ptt_timeout_s"`
		Probed      time.Time `json:"probed"`
	}{s.SWRLimit, s.PTTTimeoutS, s.Probed}
	if err := json.Unmarshal(b, &guards); err != nil {
		st.warn(name, err)
		return Defaults()
	}
	s.SWRLimit, s.PTTTimeoutS, s.Probed = guards.SWRLimit, guards.PTTTimeoutS, guards.Probed
	if err := s.validateGuards(); err != nil {
		st.warn(name, err)
		d := Defaults()
		d.Probed = s.Probed
		return d
	}
	return s
}

func (st *Store) warn(name string, err error) {
	st.logMu.Lock()
	defer st.logMu.Unlock()
	if st.logged == nil {
		st.logged = map[string]bool{}
	}
	if st.logged[name] {
		return
	}
	st.logged[name] = true
	log.Printf("settings: radio %s: using the defaults: %v", name, err)
}

// Set validates s and writes it atomically, mode 0600.
func (st *Store) Set(name string, s RadioSettings) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("no such radio")
	}
	if err := s.Validate(); err != nil {
		return err
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	return st.write(name, s)
}

// Modify reads the radio's settings, lets fn change them, validates and writes the result, all
// under the store's lock, so two writers never overwrite each other's field.
func (st *Store) Modify(name string, fn func(*RadioSettings)) error {
	if !nameRe.MatchString(name) {
		return fmt.Errorf("no such radio")
	}
	st.mu.Lock()
	defer st.mu.Unlock()
	s, err := st.ReadStrict(name)
	if err != nil {
		return err
	}
	fn(&s)
	if err := s.Validate(); err != nil {
		return err
	}
	return st.write(name, s)
}

// SetProbed records when the radio's capabilities were probed, keeping its limits.
func (st *Store) SetProbed(name string, at time.Time) error {
	return st.Modify(name, func(s *RadioSettings) { s.Probed = at })
}

// write needs st.mu. The temp file is synced before the rename; stale temp files from an
// earlier crash are removed first.
func (st *Store) write(name string, s RadioSettings) error {
	if err := os.MkdirAll(st.Dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if stale, _ := filepath.Glob(filepath.Join(st.Dir, "."+name+"-*.tmp")); len(stale) > 0 {
		for _, p := range stale {
			os.Remove(p)
		}
	}
	f, err := os.CreateTemp(st.Dir, "."+name+"-*.tmp") // mode 0600
	if err != nil {
		return err
	}
	tmp := f.Name()
	err = func() error {
		if _, err := f.Write(append(b, '\n')); err != nil {
			f.Close()
			return err
		}
		if err := f.Sync(); err != nil {
			f.Close()
			return err
		}
		if err := f.Close(); err != nil {
			return err
		}
		if err := os.Rename(tmp, filepath.Join(st.Dir, name+".json")); err != nil {
			return err
		}
		dir, err := os.Open(st.Dir)
		if err != nil {
			return err
		}
		defer dir.Close()
		if err := dir.Sync(); err != nil {
			return err
		}
		parent, err := os.Open(filepath.Dir(st.Dir))
		if err != nil {
			return err
		}
		defer parent.Close()
		return parent.Sync()
	}()
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
