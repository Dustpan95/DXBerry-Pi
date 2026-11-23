package presets

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func testStore(t *testing.T) *Store {
	t.Helper()
	s, err := NewStore(filepath.Join(t.TempDir(), "presets"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func createRequest(rev, label string) Request {
	return Request{Version: 1, Operation: "create", Radio: "radio1", ExpectedRevision: rev, Label: label, FrequencyHz: 14074000}
}
func seed(t *testing.T, s *Store) Library {
	t.Helper()
	l, err := s.Edit(context.Background(), createRequest(Absent, "original"))
	if err != nil {
		t.Fatal(err)
	}
	return l
}
func savedBytes(t *testing.T, s *Store) []byte {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(s.dir, "radio1.json"))
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestStoreCRUDAndRevisionConflicts(t *testing.T) {
	s := testStore(t)
	ctx := context.Background()
	l, err := s.List(ctx, "radio1")
	if err != nil || l.Revision != Absent || l.Presets == nil || len(l.Presets) != 0 {
		t.Fatalf("absent: %+v %v", l, err)
	}
	if _, err := os.Stat(s.dir); !os.IsNotExist(err) {
		t.Fatal("read created state")
	}
	l = seed(t, s)
	original := savedBytes(t, s)
	if !validHex(l.Revision, 64) || !validHex(l.Presets[0].ID, 32) {
		t.Fatal("missing generated identities")
	}
	for path, mode := range map[string]os.FileMode{s.dir: 0700, filepath.Join(s.dir, "radio1.json"): 0600} {
		info, err := os.Stat(path)
		if err != nil || info.Mode().Perm() != mode {
			t.Fatalf("mode %s: %v %v", path, info, err)
		}
	}
	if _, err := s.Edit(ctx, createRequest(Absent, "stale")); err != ErrConflict || !bytes.Equal(savedBytes(t, s), original) {
		t.Fatal("absent CAS overwrote an existing document", err)
	}
	update := Request{Version: 1, Operation: "update", Radio: "radio1", ExpectedRevision: l.Revision, ID: l.Presets[0].ID, Label: "<receive> \"label\"", FrequencyHz: 7100000}
	next, err := s.Edit(ctx, update)
	if err != nil || next.Revision == l.Revision || next.Presets[0].ID != l.Presets[0].ID || next.Presets[0].Label != update.Label {
		t.Fatalf("update: %+v %v", next, err)
	}
	if _, err := s.Edit(ctx, update); err != ErrConflict {
		t.Fatal("stale update accepted", err)
	}
	missing := update
	missing.ExpectedRevision = next.Revision
	missing.ID = strings.Repeat("f", 32)
	before := savedBytes(t, s)
	if _, err := s.Edit(ctx, missing); err != ErrNotFound || !bytes.Equal(before, savedBytes(t, s)) {
		t.Fatal("missing ID edited state", err)
	}
	deleted, err := s.Edit(ctx, Request{Version: 1, Operation: "delete", Radio: "radio1", ExpectedRevision: next.Revision, ID: next.Presets[0].ID})
	if err != nil || len(deleted.Presets) != 0 || deleted.Presets == nil || deleted.Revision == next.Revision {
		t.Fatalf("delete: %+v %v", deleted, err)
	}
	if _, err := os.Stat(filepath.Join(s.dir, "radio1.json")); err != nil {
		t.Fatal("empty library was unlinked", err)
	}
	if _, err := s.Edit(ctx, createRequest(l.Revision, "ABA")); err != ErrConflict {
		t.Fatal("earlier revision revived", err)
	}
	if _, err := s.List(ctx, "../outside"); err != ErrInvalid {
		t.Fatal("path accepted", err)
	}
}

func TestStorePreservesUnsafeOrDamagedExistingFiles(t *testing.T) {
	cases := map[string]func(*testing.T, *Store, string){
		"corrupt": func(t *testing.T, s *Store, p string) {
			if err := os.WriteFile(p, []byte(`{"version":`), 0600); err != nil {
				t.Fatal(err)
			}
		},
		"oversized": func(t *testing.T, s *Store, p string) {
			if err := os.WriteFile(p, bytes.Repeat([]byte{' '}, MaxBytes+1), 0600); err != nil {
				t.Fatal(err)
			}
		},
		"public permissions": func(t *testing.T, s *Store, p string) {
			if err := os.Chmod(p, 0644); err != nil {
				t.Fatal(err)
			}
		},
		"wrong radio": func(t *testing.T, s *Store, p string) {
			raw := bytes.Replace(savedBytes(t, s), []byte(`"radio1"`), []byte(`"radio2"`), 1)
			if err := os.WriteFile(p, raw, 0600); err != nil {
				t.Fatal(err)
			}
		},
		"hardlink": func(t *testing.T, s *Store, p string) {
			if err := os.Link(p, p+".link"); err != nil {
				t.Fatal(err)
			}
		},
		"symlink": func(t *testing.T, s *Store, p string) {
			if err := os.Rename(p, p+".target"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(p+".target", p); err != nil {
				t.Fatal(err)
			}
		},
		"dangling symlink": func(t *testing.T, s *Store, p string) {
			os.Remove(p)
			if err := os.Symlink(p+".missing", p); err != nil {
				t.Fatal(err)
			}
		},
		"fifo": func(t *testing.T, s *Store, p string) {
			os.Remove(p)
			if err := syscall.Mkfifo(p, 0600); err != nil {
				t.Fatal(err)
			}
		},
		"directory": func(t *testing.T, s *Store, p string) {
			os.Remove(p)
			if err := os.Mkdir(p, 0700); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, damage := range cases {
		t.Run(name, func(t *testing.T) {
			s := testStore(t)
			l := seed(t, s)
			p := filepath.Join(s.dir, "radio1.json")
			damage(t, s, p)
			info, err := os.Lstat(p)
			if err != nil {
				t.Fatal(err)
			}
			var before []byte
			if info.Mode().IsRegular() {
				before = savedBytes(t, s)
			}
			if l, err := s.List(context.Background(), "radio1"); err != ErrUnavailable || l.Version != 0 {
				t.Fatalf("damaged read supplied state: %+v %v", l, err)
			}
			if _, err := s.Edit(context.Background(), createRequest(l.Revision, "must not replace")); err != ErrUnavailable {
				t.Fatal("damaged write not refused", err)
			}
			after, err := os.Lstat(p)
			if err != nil || !os.SameFile(info, after) || after.Mode() != info.Mode() {
				t.Fatal("unsafe existing entry replaced")
			}
			if before != nil && !bytes.Equal(before, savedBytes(t, s)) {
				t.Fatal("damaged bytes changed")
			}
		})
	}
}

func TestStoreRefusesDirectoryReplacementAndOwnership(t *testing.T) {
	for _, kind := range []string{"permissions", "owner", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			l := seed(t, s)
			switch kind {
			case "permissions":
				if err := os.Chmod(s.dir, 0755); err != nil {
					t.Fatal(err)
				}
			case "owner":
				s.owner++
			case "symlink":
				if err := os.Rename(s.dir, s.dir+".moved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(s.dir+".moved", s.dir); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.List(context.Background(), "radio1"); err != ErrUnavailable {
				t.Fatal("unsafe directory read", err)
			}
			if _, err := s.Edit(context.Background(), createRequest(l.Revision, "new")); err != ErrUnavailable {
				t.Fatal("unsafe directory write", err)
			}
		})
	}
	for _, path := range []string{"relative", "/", "/tmp/../tmp/presets"} {
		if _, err := NewStore(path); err != ErrInvalid {
			t.Fatal("unsafe store path accepted", path, err)
		}
	}
}

func TestStoreRefusesUntrustedOrMissingParent(t *testing.T) {
	for _, kind := range []string{"missing", "symlink", "writable", "foreign owner"} {
		t.Run(kind, func(t *testing.T) {
			parent := filepath.Join(t.TempDir(), "state")
			if kind != "missing" {
				if err := os.Mkdir(parent, 0700); err != nil {
					t.Fatal(err)
				}
			}
			s, err := NewStore(filepath.Join(parent, "presets"))
			if err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "symlink":
				if err := os.Rename(parent, parent+".moved"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(parent+".moved", parent); err != nil {
					t.Fatal(err)
				}
			case "writable":
				if err := os.Chmod(parent, 0770); err != nil {
					t.Fatal(err)
				}
			case "foreign owner":
				s.owner++
			}
			if _, err := s.List(context.Background(), "radio1"); err != ErrUnavailable {
				t.Fatal("unsafe parent became an empty successful library", err)
			}
			if _, err := s.Edit(context.Background(), createRequest(Absent, "new")); err != ErrUnavailable {
				t.Fatal("unsafe parent accepted a mutation", err)
			}
		})
	}
}

func TestStoreFileOwnerIsVerifiedIndependently(t *testing.T) {
	s := testStore(t)
	seed(t, s)
	dir, err := s.directory(false)
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	s.owner++
	if _, err := s.load(dir, "radio1"); err != ErrUnavailable {
		t.Fatal("foreign file owner accepted", err)
	}
}

func TestStorePrePublicationFailuresPreservePreviousRevision(t *testing.T) {
	for _, kind := range []string{"file sync", "parent sync", "rename", "entropy", "cancel", "outside replacement", "directory replacement"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			l := seed(t, s)
			before := savedBytes(t, s)
			realSync := s.sync
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch kind {
			case "file sync":
				s.sync = func(f *os.File) error {
					info, _ := f.Stat()
					if !info.IsDir() {
						return syscall.EIO
					}
					return realSync(f)
				}
			case "parent sync":
				s.sync = func(f *os.File) error {
					if f.Name() == "preset parent" {
						return syscall.EIO
					}
					return realSync(f)
				}
			case "rename":
				s.rename = func(int, string, int, string) error { return syscall.EIO }
			case "entropy":
				s.random = bytes.NewReader(nil)
			case "cancel":
				s.sync = func(f *os.File) error {
					err := realSync(f)
					info, _ := f.Stat()
					if !info.IsDir() {
						cancel()
					}
					return err
				}
			case "outside replacement":
				s.sync = func(f *os.File) error {
					err := realSync(f)
					info, _ := f.Stat()
					if !info.IsDir() {
						p := filepath.Join(s.dir, "outside.json")
						if e := os.WriteFile(p, before, 0600); e != nil {
							return e
						}
						if e := os.Rename(p, filepath.Join(s.dir, "radio1.json")); e != nil {
							return e
						}
					}
					return err
				}
			case "directory replacement":
				s.sync = func(f *os.File) error {
					err := realSync(f)
					info, _ := f.Stat()
					if !info.IsDir() {
						if e := os.Rename(s.dir, s.dir+".moved"); e != nil {
							return e
						}
						if e := os.Mkdir(s.dir, 0700); e != nil {
							return e
						}
					}
					return err
				}
			}
			got, err := s.Edit(ctx, createRequest(l.Revision, "not published"))
			want := ErrUnavailable
			if kind == "outside replacement" {
				want = ErrConflict
			}
			if err != want || got.Version != 0 {
				t.Fatalf("failure supplied success or wrong error: %+v %v, want %v", got, err, want)
			}
			p := filepath.Join(s.dir, "radio1.json")
			if kind == "directory replacement" {
				p = filepath.Join(s.dir+".moved", "radio1.json")
			}
			after, e := os.ReadFile(p)
			if e != nil || !bytes.Equal(after, before) {
				t.Fatal("previous revision changed", e)
			}
		})
	}
}

func TestStorePostPublicationFailureIsUnknown(t *testing.T) {
	for _, kind := range []string{"directory sync", "readback", "cancellation"} {
		t.Run(kind, func(t *testing.T) {
			s := testStore(t)
			l := seed(t, s)
			realSync := s.sync
			realRename := s.rename
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			if kind == "directory sync" {
				s.sync = func(f *os.File) error {
					if f.Name() == s.dir {
						return syscall.EIO
					}
					return realSync(f)
				}
			}
			if kind == "readback" {
				s.rename = func(a int, b string, c int, d string) error {
					if err := realRename(a, b, c, d); err != nil {
						return err
					}
					return os.WriteFile(filepath.Join(s.dir, d), []byte("damaged after publication"), 0600)
				}
			}
			if kind == "cancellation" {
				s.rename = func(a int, b string, c int, d string) error { err := realRename(a, b, c, d); cancel(); return err }
			}
			if got, err := s.Edit(ctx, createRequest(l.Revision, "published")); err != ErrUnknown || got.Version != 0 {
				t.Fatalf("uncertain write claimed success/old state: %+v %v", got, err)
			}
			s.sync = realSync
			s.rename = realRename
			current, err := s.List(context.Background(), "radio1")
			if kind == "readback" {
				if err != ErrUnavailable {
					t.Fatal("corrupt readback became success", err)
				}
			} else if err != nil || current.Revision == l.Revision || len(current.Presets) != 2 {
				t.Fatalf("new publication silently rolled back: %+v %v", current, err)
			}
		})
	}
}

func TestStoreLimitsAndCancellableProcessLock(t *testing.T) {
	s := testStore(t)
	l := seed(t, s)
	for len(l.Presets) < MaxPresets {
		var err error
		l, err = s.Edit(context.Background(), createRequest(l.Revision, "receive"))
		if err != nil {
			t.Fatal(err)
		}
	}
	before := savedBytes(t, s)
	if _, err := s.Edit(context.Background(), createRequest(l.Revision, "too many")); err != ErrLimit || !bytes.Equal(before, savedBytes(t, s)) {
		t.Fatal("limit changed state", err)
	}
	f, err := os.Open(s.dir)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	for _, write := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 70*time.Millisecond)
		start := time.Now()
		if write {
			_, err = s.Edit(ctx, createRequest(l.Revision, "blocked"))
		} else {
			_, err = s.List(ctx, "radio1")
		}
		cancel()
		if err != ErrUnavailable || time.Since(start) > time.Second {
			t.Fatal("lock ignored deadline", write, err)
		}
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if _, err := s.List(context.Background(), "radio1"); err != nil {
		t.Fatal("lock not released", err)
	}
	if leftovers, _ := filepath.Glob(filepath.Join(s.dir, "*.tmp")); len(leftovers) != 0 {
		t.Fatal("ordinary writes leaked temporaries", leftovers)
	}
}

func TestStoreConcurrentEditsHaveOneWinner(t *testing.T) {
	s := testStore(t)
	l := seed(t, s)
	start := make(chan struct{})
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			other, e := NewStore(s.dir)
			if e != nil {
				errs <- e
				return
			}
			<-start
			_, e = other.Edit(context.Background(), createRequest(l.Revision, "parallel"))
			errs <- e
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	wins, conflicts := 0, 0
	for err := range errs {
		if err == nil {
			wins++
		} else if err == ErrConflict {
			conflicts++
		} else {
			t.Fatal(err)
		}
	}
	if wins != 1 || conflicts != 7 {
		t.Fatalf("winners=%d conflicts=%d", wins, conflicts)
	}
	current, err := s.List(context.Background(), "radio1")
	if err != nil || len(current.Presets) != 2 {
		t.Fatal("lost update", current, err)
	}
}

// This helper executes only in the subprocesses created by the tests below.
func TestPresetStoreHelperProcess(t *testing.T) {
	mode := os.Getenv("DXB_PRESET_CHILD")
	if mode == "" {
		return
	}
	if mode == "zero" {
		var uninitialized Store
		if _, err := uninitialized.List(context.Background(), "radio1"); err != ErrUnavailable {
			os.Exit(25)
		}
		if _, err := uninitialized.Edit(context.Background(), createRequest(Absent, "must not write cwd")); err != ErrUnavailable {
			os.Exit(26)
		}
		os.Exit(0)
	}
	dir := os.Getenv("DXB_PRESET_DIR")
	s, err := NewStore(dir)
	if err != nil {
		os.Exit(20)
	}
	ready := os.Getenv("DXB_PRESET_READY")
	if mode == "crash" {
		s.rename = func(a int, b string, c int, d string) error {
			if os.WriteFile(ready, []byte("synced temporary ready"), 0600) != nil {
				os.Exit(21)
			}
			select {}
		}
	} else {
		if os.WriteFile(ready, []byte("ready"), 0600) != nil {
			os.Exit(22)
		}
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, e := os.Stat(os.Getenv("DXB_PRESET_GO")); e == nil {
				break
			}
			if time.Now().After(deadline) {
				os.Exit(23)
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
	_, err = s.Edit(context.Background(), createRequest(os.Getenv("DXB_PRESET_REV"), "child"))
	if err == nil {
		os.Exit(0)
	}
	if errors.Is(err, ErrConflict) {
		os.Exit(10)
	}
	os.Exit(24)
}

func waitFile(t *testing.T, path string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("helper did not become ready")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
func helper(t *testing.T, s *Store, rev, mode, ready, goFile string) *exec.Cmd {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestPresetStoreHelperProcess$")
	cmd.Dir = filepath.Dir(s.dir)
	cmd.Env = append(os.Environ(), "DXB_PRESET_CHILD="+mode, "DXB_PRESET_DIR="+s.dir, "DXB_PRESET_REV="+rev, "DXB_PRESET_READY="+ready, "DXB_PRESET_GO="+goFile)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	return cmd
}

func TestStoreCrossProcessCASAndInterruptedWriter(t *testing.T) {
	t.Run("uninitialized store cannot adopt cwd", func(t *testing.T) {
		s := testStore(t)
		cmd := helper(t, s, "", "zero", "", "")
		if err := cmd.Wait(); err != nil {
			t.Fatal("uninitialized store used cwd", err)
		}
		entries, err := os.ReadDir(filepath.Dir(s.dir))
		if err != nil || len(entries) != 0 {
			t.Fatal("uninitialized store changed cwd", entries, err)
		}
	})
	t.Run("process CAS", func(t *testing.T) {
		s := testStore(t)
		l := seed(t, s)
		parent := filepath.Dir(s.dir)
		goFile := filepath.Join(parent, "go")
		var cmds []*exec.Cmd
		for i := 0; i < 3; i++ {
			ready := filepath.Join(parent, fmt.Sprint("ready", i))
			cmds = append(cmds, helper(t, s, l.Revision, "race", ready, goFile))
			waitFile(t, ready)
		}
		if err := os.WriteFile(goFile, []byte("go"), 0600); err != nil {
			t.Fatal(err)
		}
		wins, conflicts := 0, 0
		for _, cmd := range cmds {
			err := cmd.Wait()
			if err == nil {
				wins++
			} else if e, ok := err.(*exec.ExitError); ok && e.ExitCode() == 10 {
				conflicts++
			} else {
				t.Fatal("unexpected child result", err)
			}
		}
		if wins != 1 || conflicts != 2 {
			t.Fatalf("cross-process winners=%d conflicts=%d", wins, conflicts)
		}
	})
	t.Run("killed before publication", func(t *testing.T) {
		s := testStore(t)
		l := seed(t, s)
		before := savedBytes(t, s)
		ready := filepath.Join(filepath.Dir(s.dir), "ready")
		cmd := helper(t, s, l.Revision, "crash", ready, "")
		waitFile(t, ready)
		if err := cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		_ = cmd.Wait()
		if !bytes.Equal(before, savedBytes(t, s)) {
			t.Fatal("killed writer changed active bytes")
		}
		if leftovers, _ := filepath.Glob(filepath.Join(s.dir, "*.tmp")); len(leftovers) != 1 {
			t.Fatalf("expected the killed writer's private temporary: %v", leftovers)
		}
		if _, err := s.Edit(context.Background(), createRequest(l.Revision, "after crash")); err != nil {
			t.Fatal("crashed writer retained process lock", err)
		}
	})
}
