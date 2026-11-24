package programstate

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func preparingStore(t *testing.T) *Store {
	t.Helper()
	base := rootStore(t)
	s, err := NewStore(filepath.Join(base.state, "station"))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestPrepareParentIsExplicitAndDoesNotInitializeOrRepairEpoch(t *testing.T) {
	s := preparingStore(t)
	if _, err := s.Advance(context.Background()); err == nil {
		t.Fatal("advance created its missing parent")
	}
	if _, err := os.Lstat(s.state); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("implicit parent creation")
	}
	if err := s.PrepareParent(context.Background()); err != nil {
		t.Fatal(err)
	}
	var st unix.Stat_t
	if unix.Lstat(s.state, &st) != nil || st.Uid != 0 || st.Gid != 0 || st.Mode&07777 != 0700 {
		t.Fatal("new parent ownership/mode")
	}
	if _, err := os.Lstat(filepath.Join(s.state, "program-state")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("prepare initialized private state")
	}
	marker := filepath.Join(s.state, "unrelated")
	if err := os.WriteFile(marker, []byte("retained"), 0600); err != nil {
		t.Fatal(err)
	}
	first := mustAdvance(t, s)
	before := epochBytes(t, s)
	if err := s.PrepareParent(context.Background()); err != nil {
		t.Fatal(err)
	}
	after, err := s.Read(context.Background())
	if err != nil || !first.Same(after) {
		t.Fatal("prepare rotated an existing epoch", err)
	}
	if err := os.WriteFile(epochPath(s), []byte("damaged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := s.PrepareParent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Advance(context.Background()); err == nil {
		t.Fatal("damaged epoch repaired")
	}
	damaged, _ := os.ReadFile(epochPath(s))
	retained, _ := os.ReadFile(marker)
	if string(damaged) != "damaged\n" || string(retained) != "retained" || bytes.Equal(before, damaged) {
		t.Fatal("existing data changed")
	}
}

func TestPrepareParentRejectsUnsafeExistingLeavesAndAncestors(t *testing.T) {
	for _, kind := range []string{"symlink", "file", "fifo", "writable", "foreign", "missing-ancestor", "writable-ancestor"} {
		t.Run(kind, func(t *testing.T) {
			s := preparingStore(t)
			switch kind {
			case "symlink":
				if err := os.Symlink(filepath.Dir(s.state), s.state); err != nil {
					t.Fatal(err)
				}
			case "file":
				if err := os.WriteFile(s.state, []byte("retained"), 0600); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := unix.Mkfifo(s.state, 0600); err != nil {
					t.Fatal(err)
				}
			case "writable", "foreign":
				if err := os.Mkdir(s.state, 0700); err != nil {
					t.Fatal(err)
				}
				if kind == "writable" {
					if err := os.Chmod(s.state, 0777); err != nil {
						t.Fatal(err)
					}
				} else if err := os.Chown(s.state, 65534, 65534); err != nil {
					t.Fatal(err)
				}
			case "missing-ancestor":
				s.state = filepath.Join(filepath.Dir(s.state), "missing", "station")
			case "writable-ancestor":
				if err := os.Chmod(filepath.Dir(s.state), 0777); err != nil {
					t.Fatal(err)
				}
			}
			var before unix.Stat_t
			existed := unix.Lstat(s.state, &before) == nil
			if err := s.PrepareParent(context.Background()); err == nil {
				t.Fatal("unsafe prepare accepted")
			}
			var after unix.Stat_t
			if existed {
				if unix.Lstat(s.state, &after) != nil || before.Mode != after.Mode || before.Uid != after.Uid || before.Ino != after.Ino {
					t.Fatal("unsafe leaf repaired/replaced")
				}
			} else if _, err := os.Lstat(s.state); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("unsafe path created")
			}
			if kind == "missing-ancestor" {
				if _, err := os.Lstat(filepath.Dir(s.state)); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("recursively created ancestor")
				}
			}
		})
	}
}

func TestPrepareParentCancellationSyncFailureAndPathReplacement(t *testing.T) {
	s := preparingStore(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := s.PrepareParent(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation", err)
	}
	if _, err := os.Lstat(s.state); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("canceled prepare wrote")
	}
	s.sync = func(*os.File) error { return io.ErrClosedPipe }
	if err := s.PrepareParent(context.Background()); err == nil {
		t.Fatal("sync failure ignored")
	}
	if _, err := os.Lstat(filepath.Join(s.state, "program-state")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("failed prepare initialized epoch")
	}
	s.sync = func(f *os.File) error { return f.Sync() }
	first := mustAdvance(t, s)
	changed := false
	s.sync = func(f *os.File) error {
		if !changed {
			changed = true
			if err := os.Rename(s.state, s.state+".retained"); err != nil {
				t.Fatal(err)
			}
			if err := os.Mkdir(s.state, 0700); err != nil {
				t.Fatal(err)
			}
		}
		return f.Sync()
	}
	if err := s.PrepareParent(context.Background()); err == nil {
		t.Fatal("path replacement accepted")
	}
	if next, err := s.Read(context.Background()); err == nil || first.Same(next) {
		t.Fatal("replaced parent retained authority")
	}
	if _, err := os.Stat(filepath.Join(s.state+".retained", "program-state", "epoch")); err != nil {
		t.Fatal("original evidence lost", err)
	}
}
