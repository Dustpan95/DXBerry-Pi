// Package settings keeps the per-radio station settings: the SWR limit and the PTT timeout
// that the transmit guards read, and when the radio's capabilities were last probed.
package settings

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"sync"
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
	SWRLimit    float64   `json:"swr_limit"`
	PTTTimeoutS int       `json:"ptt_timeout_s"`
	Probed      time.Time `json:"probed"`
}

// Defaults is what a radio without a file gets.
func Defaults() RadioSettings {
	return RadioSettings{SWRLimit: DefaultSWRLimit, PTTTimeoutS: DefaultPTTTimeoutS}
}

// Validate says what is wrong with s, naming the valid range, or returns nil.
func (s RadioSettings) Validate() error {
	if !(s.SWRLimit >= MinSWR && s.SWRLimit <= MaxSWR) {
		return fmt.Errorf("the SWR limit must be between 1.5 and 10")
	}
	if s.PTTTimeoutS < MinPTT || s.PTTTimeoutS > MaxPTT {
		return fmt.Errorf("the PTT timeout must be between 30 and 600 seconds")
	}
	return nil
}

// Store is the settings directory (<state>/radios).
type Store struct {
	Dir string

	mu     sync.Mutex // writers
	logMu  sync.Mutex
	logged map[string]bool
}

// Get returns the radio's settings, or the defaults when the file is absent or unreadable
// (an unreadable one is logged once per radio).
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
	if err := json.Unmarshal(b, &s); err != nil {
		st.warn(name, err)
		return Defaults()
	}
	if err := s.Validate(); err != nil {
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
	s := st.read(name)
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
		return os.Rename(tmp, filepath.Join(st.Dir, name+".json"))
	}()
	if err != nil {
		os.Remove(tmp)
	}
	return err
}
