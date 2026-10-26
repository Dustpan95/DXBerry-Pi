package rig

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"time"
)

var markNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// KeyedMarks keeps one file per radio whose carrier the station may have up: written before
// set_ptt 1, removed once a set_ptt 0 is confirmed. The files live under the state directory
// (<state>/keyed), so they outlive a crash of the service and its Restart=always: at the next
// start every marker owes its radio an unkey.
type KeyedMarks struct{ Dir string }

func (k *KeyedMarks) path(radio string) (string, error) {
	if !markNameRe.MatchString(radio) {
		return "", errors.New("rig: bad radio name for a keyed marker")
	}
	return filepath.Join(k.Dir, radio), nil
}

// Mark notes that radio may be keyed (directory 0700, file 0600).
func (k *KeyedMarks) Mark(radio string) error {
	p, err := k.path(radio)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(k.Dir, 0o700); err != nil {
		return err
	}
	return os.WriteFile(p, []byte(strconv.FormatInt(time.Now().Unix(), 10)+"\n"), 0o600)
}

// Clear removes radio's marker; a missing one is not an error.
func (k *KeyedMarks) Clear(radio string) error {
	p, err := k.path(radio)
	if err != nil {
		return err
	}
	if err := os.Remove(p); err != nil && !errors.Is(err, fs.ErrNotExist) {
		return err
	}
	return nil
}

// List names the radios that have a marker, sorted.
func (k *KeyedMarks) List() []string {
	ents, err := os.ReadDir(k.Dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range ents {
		if e.Type().IsRegular() && markNameRe.MatchString(e.Name()) {
			out = append(out, e.Name())
		}
	}
	sort.Strings(out)
	return out
}
