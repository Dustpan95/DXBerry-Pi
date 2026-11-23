package radios

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

const registrationFixture = `{"version":1,"radios":{"radio1":{"label":"Fixture radio","rigctld_port":4532,"owner":"","ptt":{"method":"rigctld"},"rig":{"model":3073,"control":"hamlib"},"cat":{"path":"usb-0:1.3:1.0","serial":"example"},"audio":null}},"gps":{"device":"auto"}}`

func registrationReaderFixture(t *testing.T) (*RegistrationReader, string) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "radios.json")
	if err := os.WriteFile(p, []byte(registrationFixture), 0600); err != nil {
		t.Fatal(err)
	}
	r, err := NewRegistrationReader(p)
	if err != nil {
		t.Fatal(err)
	}
	// The same descriptor/trust code runs for ordinary CI users. The public
	// constructor's root-only ownership policy has a separate explicit check.
	r.owner = uint32(os.Geteuid())
	return r, p
}

func TestFreshRegistrationReadsWithoutChangingWatcherOrFiles(t *testing.T) {
	r, p := registrationReaderFixture(t)
	w := New(p, 0)
	before, err := os.ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	a, err := r.Read(context.Background(), "radio1")
	if err != nil {
		t.Fatal(err)
	}
	b, err := r.Read(context.Background(), "radio1")
	if err != nil || !a.Same(b) {
		t.Fatal(b, err)
	}
	want, ok := w.Get("radio1")
	if !ok || a.Radio() != want {
		t.Fatal(a.Radio(), want)
	}
	copy := a.Radio()
	copy.Port = 9999
	if a.Radio().Port != 4532 {
		t.Fatal("mutable registration")
	}
	after, err := os.ReadFile(p)
	if err != nil || string(after) != string(before) {
		t.Fatal("reader changed registration contents", err)
	}
	write(t, p, `{"version":1,"radios":{}}`)
	if _, err := r.Read(context.Background(), "radio1"); !errors.Is(err, ErrRegistrationNotFound) {
		t.Fatal(err)
	}
	if cached, ok := w.Get("radio1"); !ok || cached != want {
		t.Fatal("fresh reader altered watcher", cached)
	}
	write(t, p, `{"version":1,"radios":{`)
	if got, err := r.Read(context.Background(), "radio1"); !errors.Is(err, ErrRegistrationUnavailable) || got != (Registration{}) {
		t.Fatal("used last-good evidence", got, err)
	}
}

func TestRegistrationIdentityChangesInvalidateOldEvidence(t *testing.T) {
	for _, kind := range []string{"selected metadata", "unrelated extension", "same bytes replacement", "parent replacement", "mode", "write and restore"} {
		t.Run(kind, func(t *testing.T) {
			r, p := registrationReaderFixture(t)
			a, err := r.Read(context.Background(), "radio1")
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "selected metadata":
				write(t, p, strings.Replace(registrationFixture, "example", "different", 1))
			case "unrelated extension":
				write(t, p, strings.Replace(registrationFixture, `"device":"auto"`, `"device":"none"`, 1))
			case "same bytes replacement":
				q := p + ".new"
				write(t, q, registrationFixture)
				if err := os.Rename(q, p); err != nil {
					t.Fatal(err)
				}
			case "parent replacement":
				old := filepath.Dir(p)
				if err := os.Rename(old, old+"-old"); err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { os.RemoveAll(old + "-old") })
				if err := os.Mkdir(old, 0700); err != nil {
					t.Fatal(err)
				}
				// Same file inode moved under a newly created parent must differ.
				if err := os.Rename(filepath.Join(old+"-old", "radios.json"), p); err != nil {
					t.Fatal(err)
				}
			case "mode":
				if err := os.Chmod(p, 0400); err != nil {
					t.Fatal(err)
				}
			case "write and restore":
				write(t, p, strings.Replace(registrationFixture, "example", "changed", 1))
				write(t, p, registrationFixture)
			}
			b, err := r.Read(context.Background(), "radio1")
			if err != nil {
				t.Fatal(err)
			}
			if a.Same(b) {
				t.Fatal("stale identity accepted")
			}
		})
	}
	r, p := registrationReaderFixture(t)
	a, err := r.Read(context.Background(), "radio1")
	if err != nil {
		t.Fatal(err)
	}
	other, _ := NewRegistrationReader(p)
	other.owner = r.owner
	b, err := other.Read(context.Background(), "radio1")
	if err != nil || a.Same(b) || (Registration{}).Same(Registration{}) {
		t.Fatal("foreign/zero identity accepted", err)
	}
}

func TestRegistrationRefusesUntrustedFilesystemInputsWithoutBlocking(t *testing.T) {
	tests := map[string]func(*RegistrationReader, string){
		"missing": func(r *RegistrationReader, p string) {
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
		},
		"bad owner": func(r *RegistrationReader, p string) { r.owner++ },
		"group writable": func(r *RegistrationReader, p string) {
			if err := os.Chmod(p, 0620); err != nil {
				t.Fatal(err)
			}
		},
		"world writable": func(r *RegistrationReader, p string) {
			if err := os.Chmod(p, 0602); err != nil {
				t.Fatal(err)
			}
		},
		"parent writable": func(r *RegistrationReader, p string) {
			if err := os.Chmod(filepath.Dir(p), 0777); err != nil {
				t.Fatal(err)
			}
		},
		"leaf symlink": func(r *RegistrationReader, p string) {
			q := p + ".real"
			if err := os.Rename(p, q); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(q, p); err != nil {
				t.Fatal(err)
			}
		},
		"ancestor symlink": func(r *RegistrationReader, p string) {
			q := filepath.Dir(p) + "-alias"
			if err := os.Symlink(filepath.Dir(p), q); err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { os.Remove(q) })
			r.path = filepath.Join(q, "radios.json")
		},
		"hard link": func(r *RegistrationReader, p string) {
			if err := os.Link(p, p+".link"); err != nil {
				t.Fatal(err)
			}
		},
		"directory": func(r *RegistrationReader, p string) {
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(p, 0700); err != nil {
				t.Fatal(err)
			}
		},
		"FIFO": func(r *RegistrationReader, p string) {
			if err := os.Remove(p); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(p, 0600); err != nil {
				t.Fatal(err)
			}
		},
		"oversize": func(r *RegistrationReader, p string) { write(t, p, strings.Repeat(" ", registrationLimit+1)) },
	}
	for name, change := range tests {
		t.Run(name, func(t *testing.T) {
			r, p := registrationReaderFixture(t)
			change(r, p)
			done := make(chan error, 1)
			go func() {
				got, err := r.Read(context.Background(), "radio1")
				if got != (Registration{}) {
					done <- errors.New("failure returned evidence")
				} else {
					done <- err
				}
			}()
			select {
			case err := <-done:
				if !errors.Is(err, ErrRegistrationUnavailable) {
					t.Fatal(err)
				}
			case <-time.After(time.Second):
				t.Fatal("non-regular registration blocked")
			}
		})
	}
}

func TestRegistrationReaderConfigurationCancellationAndRootPolicy(t *testing.T) {
	for _, p := range []string{"", "relative", "/", "/tmp/../tmp/radios.json"} {
		if _, err := NewRegistrationReader(p); !errors.Is(err, ErrRegistrationUnavailable) {
			t.Fatal(p, err)
		}
	}
	r, p := registrationReaderFixture(t)
	production, err := NewRegistrationReader(p)
	if err != nil || production.owner != 0 {
		t.Fatal("public reader is not root-owned", err)
	}
	_, err = production.Read(context.Background(), "radio1")
	if os.Geteuid() == 0 && err != nil || os.Geteuid() != 0 && !errors.Is(err, ErrRegistrationUnavailable) {
		t.Fatal("production ownership policy", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := r.Read(ctx, "radio1"); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, name := range []string{"", "../radio1", "RADIO1", "radio-name", "radio12345678"} {
		if _, err := r.Read(context.Background(), name); !errors.Is(err, ErrRegistrationNotFound) {
			t.Fatal(name, err)
		}
	}
	for _, reader := range []*RegistrationReader{nil, {}} {
		if _, err := reader.Read(context.Background(), "radio1"); !errors.Is(err, ErrRegistrationUnavailable) {
			t.Fatal(err)
		}
	}
	empty := filepath.Join(t.TempDir(), "not-created", "radios.json")
	unused, err := NewRegistrationReader(empty)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := unused.Read(context.Background(), "radio1"); !errors.Is(err, ErrRegistrationUnavailable) {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Dir(empty)); !os.IsNotExist(err) {
		t.Fatal("reader created state", err)
	}
}

func TestRegistrationParserRejectsAmbiguousAndInvalidRecords(t *testing.T) {
	bad := []string{
		`null`, `[]`, registrationFixture + `{}`, strings.Replace(registrationFixture, `"version":1`, `"version":2`, 1),
		strings.Replace(registrationFixture, `"version":1`, `"Version":1`, 1),
		strings.Replace(registrationFixture, `"radios":`, `"radios":{},"radios":`, 1),
		strings.Replace(registrationFixture, `"owner":""`, `"owner":"","OWNER":"pat"`, 1),
		strings.Replace(registrationFixture, `"owner":""`, `"owner":null`, 1),
		strings.Replace(registrationFixture, `"model":3073`, `"model":null`, 1),
		strings.Replace(registrationFixture, `"model":3073`, `"model":1.5`, 1),
		strings.Replace(registrationFixture, `"rigctld_port":4532`, `"rigctld_port":4533`, 1),
		strings.Replace(registrationFixture, `"rigctld_port":4532`, `"rigctld_port":65536`, 1),
		strings.Replace(registrationFixture, `"rigctld_port":4532`, `"rigctld_port":null`, 1),
		strings.Replace(registrationFixture, `"method":"rigctld"`, `"method":"bogus"`, 1),
		strings.Replace(registrationFixture, `"control":"hamlib"`, `"control":null`, 1),
		strings.Replace(registrationFixture, `"control":"hamlib"`, `"control":""`, 1),
		strings.Replace(registrationFixture, `"path":"usb-0:1.3:1.0"`, `"path":null`, 1),
		strings.Replace(registrationFixture, `"serial":"example"`, `"serial":"\ud800"`, 1),
		strings.Replace(registrationFixture, `"serial":"example"`, `"serial":"\udc00"`, 1),
		strings.Replace(registrationFixture, `"serial":"example"`, `"serial":"`+string([]byte{0xff})+`"`, 1),
		strings.Replace(registrationFixture, `"Fixture radio"`, `"`+strings.Repeat("a", 41)+`"`, 1),
		strings.Replace(registrationFixture, `"Fixture radio"`, `"new\nline"`, 1),
		strings.Replace(registrationFixture, `"gps":{"device":"auto"}`, `"gps":{"device":"auto","device":"auto"}`, 1),
		strings.Replace(registrationFixture, `"gps":{"device":"auto"}`, `"gps":`+strings.Repeat("[", 34)+`0`+strings.Repeat("]", 34), 1),
	}
	var root map[string]any
	if err := json.Unmarshal([]byte(registrationFixture), &root); err != nil {
		t.Fatal(err)
	}
	radios := root["radios"].(map[string]any)
	radios["radio2"] = radios["radio1"]
	duplicatePort, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	bad = append(bad, string(duplicatePort))
	for i, raw := range bad {
		if got, err := parseRegistration([]byte(raw), "radio1"); !errors.Is(err, ErrRegistrationUnavailable) || got != (Radio{}) {
			t.Fatalf("case %d: %+v %v", i, got, err)
		}
	}
	for _, raw := range []string{registrationFixture, strings.Replace(registrationFixture, `,"control":"hamlib"`, "", 1), strings.Replace(registrationFixture, `"serial":"example"`, `"serial":"\ud83d\ude80"`, 1)} {
		if _, err := parseRegistration([]byte(raw), "radio1"); err != nil {
			t.Fatal("valid record rejected", err)
		}
	}
}

func FuzzRegistrationParser(f *testing.F) {
	f.Add([]byte(registrationFixture))
	f.Add([]byte(`{"version":1,"radios":{}}`))
	f.Add([]byte(`{"radios":{"radio1":null}}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		r, err := parseRegistration(raw, "radio1")
		if err != nil {
			if r != (Radio{}) {
				t.Fatal("error returned partial evidence")
			}
			return
		}
		if r.Name != "radio1" || r.Port < 4532 || r.Port > 65534 || r.Port%2 != 0 || r.Model < 1 {
			t.Fatal("invalid successful identity", r)
		}
	})
}
