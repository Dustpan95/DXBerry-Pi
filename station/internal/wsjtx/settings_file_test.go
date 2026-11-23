package wsjtx

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
)

func settingsFile(t *testing.T) (*SettingsFile, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "WSJT-X - test.ini")
	if err := os.WriteFile(path, []byte(settingsExample), 0640); err != nil {
		t.Fatal(err)
	}
	h, err := OpenSettingsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = h.Close() })
	return h, path
}

func fileBytes(t *testing.T, path string) []byte {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

func testLifecyclePermit() error { return nil } // Isolated fixture; no program exists.

func filePlan(t *testing.T, h *SettingsFile) (*RoutingLease, RoutingPatch) {
	t.Helper()
	d, err := h.Read()
	if err != nil {
		t.Fatal(err)
	}
	_, _, b := observer(t)
	l, p, _ := routingClaim(t, b, d.Routing())
	return l, p
}

func TestSettingsFileAtomicallyAppliesAndRestoresWithMetadata(t *testing.T) {
	h, path := settingsFile(t)
	before, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	l, p := filePlan(t, h)
	if err := h.Apply(p, testLifecyclePermit); err != nil {
		t.Fatal(err)
	}
	managed, err := h.Read()
	if err != nil {
		t.Fatal(err)
	}
	after, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if os.SameFile(before, after) || after.Mode().Perm() != before.Mode().Perm() || after.Sys().(*syscall.Stat_t).Gid != before.Sys().(*syscall.Stat_t).Gid || after.Sys().(*syscall.Stat_t).Uid != before.Sys().(*syscall.Stat_t).Uid {
		t.Fatal("replacement was not atomic or changed ownership/mode")
	}
	restore, err := l.Restore(managed.Routing())
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Apply(restore, testLifecyclePermit); err != nil {
		t.Fatal(err)
	}
	if string(fileBytes(t, path)) != settingsExample {
		t.Fatal("restoration lost original bytes")
	}
	before, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Apply(restore, testLifecyclePermit); !errors.Is(err, ErrRoutingChanged) {
		t.Fatal("stale restore patch was reused", err)
	}
	d, err := h.Read()
	if err != nil {
		t.Fatal(err)
	}
	idempotent, err := l.Restore(d.Routing())
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Apply(idempotent, testLifecyclePermit); err != nil {
		t.Fatal(err)
	}
	after, err = os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if !os.SameFile(before, after) {
		t.Fatal("no-change restoration rewrote file")
	}
}

func TestSettingsFileHasExclusiveOwnership(t *testing.T) {
	h, path := settingsFile(t)
	if other, err := OpenSettingsFile(path); !errors.Is(err, ErrSettingsFile) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatal("two settings owners admitted", err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	other, err := OpenSettingsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	_ = other.Close()
	if _, err := h.Read(); !errors.Is(err, os.ErrClosed) {
		t.Fatal("closed handle readable", err)
	}
}

func TestSettingsFileRequiresLifecycleChecksAtBothBoundaries(t *testing.T) {
	for _, when := range []string{"missing", "initial", "final"} {
		t.Run(when, func(t *testing.T) {
			h, path := settingsFile(t)
			_, p := filePlan(t, h)
			var permit func() error
			calls := 0
			if when != "missing" {
				permit = func() error {
					calls++
					if when == "initial" || calls == 2 {
						return errors.New("fixture program no longer quiescent")
					}
					return nil
				}
			}
			if err := h.Apply(p, permit); !errors.Is(err, ErrSettingsLifecycle) {
				t.Fatal("unverified lifecycle allowed write", err)
			}
			if string(fileBytes(t, path)) != settingsExample {
				t.Fatal("lifecycle refusal changed settings")
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), h.pending)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("refused stage leaked", err)
			}
		})
	}
}

func TestSettingsFilePreservesEditsBeforeApply(t *testing.T) {
	h, path := settingsFile(t)
	_, p := filePlan(t, h)
	operator := strings.Replace(settingsExample, "keep=yes", "keep=operator-choice", 1)
	if err := os.WriteFile(path, []byte(operator), 0640); err != nil {
		t.Fatal(err)
	}
	if err := h.Apply(p, testLifecyclePermit); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(fileBytes(t, path), []byte("keep=operator-choice")) {
		t.Fatal("unrelated operator edit overwritten")
	}
}

func TestSettingsFileDetectsConcurrentFileOrMetadataChanges(t *testing.T) {
	for _, change := range []string{"content", "inode", "mode", "lock", "directory", "staging-symlink"} {
		t.Run(change, func(t *testing.T) {
			h, path := settingsFile(t)
			_, p := filePlan(t, h)
			calls := 0
			permit := func() error {
				calls++
				if calls != 2 {
					return nil
				}
				switch change {
				case "staging-symlink":
					pending := filepath.Join(filepath.Dir(path), h.pending)
					if err := os.Remove(pending); err != nil {
						return err
					}
					return os.Symlink(path, pending)
				case "content":
					return os.WriteFile(path, []byte(strings.Replace(settingsExample, "keep=yes", "keep=new", 1)), 0640)
				case "inode":
					if err := os.Rename(path, path+".old"); err != nil {
						return err
					}
					return os.WriteFile(path, []byte(settingsExample), 0640)
				case "mode":
					return os.Chmod(path, 0600)
				case "lock":
					lock := filepath.Join(filepath.Dir(path), h.lockName)
					if err := os.Rename(lock, lock+".old"); err != nil {
						return err
					}
					return os.WriteFile(lock, nil, 0600)
				case "directory":
					dir := filepath.Dir(path)
					if err := os.Rename(dir, dir+".old"); err != nil {
						return err
					}
					if err := os.Mkdir(dir, 0700); err != nil {
						return err
					}
					return os.WriteFile(path, []byte(settingsExample), 0640)
				}
				return nil
			}
			err := h.Apply(p, permit)
			if !errors.Is(err, ErrSettingsChanged) && !errors.Is(err, ErrSettingsFile) {
				t.Fatal("concurrent change not detected", err)
			}
			if bytes.Contains(fileBytes(t, path), []byte("UDPServer=127.0.0.1\n")) {
				t.Fatal("raced preferences were replaced")
			}
		})
	}
}

func TestSettingsFileStorageFailuresKeepPairAndRequireReconciliation(t *testing.T) {
	for _, boundary := range []string{"file-sync", "rename", "directory-sync"} {
		t.Run(boundary, func(t *testing.T) {
			h, path := settingsFile(t)
			_, p := filePlan(t, h)
			failure := errors.New("injected storage fault")
			switch boundary {
			case "file-sync":
				h.syncFile = func(*os.File) error { return failure }
			case "rename":
				h.rename = func(int, string, int, string) error { return failure }
			case "directory-sync":
				h.syncDir = func() error { return failure }
			}
			err := h.Apply(p, testLifecyclePermit)
			if !errors.Is(err, failure) {
				t.Fatal("storage error hidden", err)
			}
			current := settingsDoc(t, fileBytes(t, path)).Routing().Current
			if boundary == "directory-sync" {
				if current != p.Replacement() {
					t.Fatal("post-rename pair incomplete")
				}
			} else if string(fileBytes(t, path)) != settingsExample {
				t.Fatal("pre-rename fault changed settings")
			}
			if boundary != "file-sync" {
				if !errors.Is(err, ErrSettingsUncertain) {
					t.Fatal("uncertain outcome reported as certain", err)
				}
				if err := h.Apply(p, testLifecyclePermit); !errors.Is(err, ErrSettingsUncertain) {
					t.Fatal("uncertain owner retried without reconciliation", err)
				}
			}
			if _, err := os.Stat(filepath.Join(filepath.Dir(path), h.pending)); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("failed stage leaked", err)
			}
		})
	}
}

func TestSettingsFileIgnoresCrashStagingAndDoesNotFollowItsLink(t *testing.T) {
	h, path := settingsFile(t)
	_, p := filePlan(t, h)
	canary := filepath.Join(filepath.Dir(path), "canary")
	if err := os.WriteFile(canary, []byte("untouched"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(canary, filepath.Join(filepath.Dir(path), h.pending)); err != nil {
		t.Fatal(err)
	}
	if err := h.Apply(p, testLifecyclePermit); err != nil {
		t.Fatal(err)
	}
	if string(fileBytes(t, canary)) != "untouched" {
		t.Fatal("staging symlink was followed")
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(path), h.pending)); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("stage not reclaimed", err)
	}
}

func TestSettingsFileRejectsUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"missing", "symlink", "hardlink", "fifo", "read-only", "world-writable", "foreign-owner"} {
		t.Run(kind, func(t *testing.T) {
			h, path := settingsFile(t)
			if err := h.Close(); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "missing":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(path, path+".real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path+".real", path); err != nil {
					t.Fatal(err)
				}
			case "hardlink":
				if err := os.Link(path, path+".link"); err != nil {
					t.Fatal(err)
				}
			case "fifo":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := syscall.Mkfifo(path, 0600); err != nil {
					t.Fatal(err)
				}
			case "read-only":
				if err := os.Chmod(path, 0400); err != nil {
					t.Fatal(err)
				}
			case "world-writable":
				if err := os.Chmod(path, 0666); err != nil {
					t.Fatal(err)
				}
			case "foreign-owner":
				if os.Geteuid() != 0 {
					t.Skip("ownership mutation needs isolated root fixture")
				}
				if err := os.Chown(path, 1234, -1); err != nil {
					t.Fatal(err)
				}
			}
			if other, err := OpenSettingsFile(path); !errors.Is(err, ErrSettingsFile) {
				if other != nil {
					_ = other.Close()
				}
				t.Fatal("unsafe file admitted", err)
			}
		})
	}
}

func TestSettingsFileOutsideReadersNeverSeeAHalfUpdatedPair(t *testing.T) {
	h, path := settingsFile(t)
	l, p := filePlan(t, h)
	d, err := h.Read()
	if err != nil {
		t.Fatal(err)
	}
	managed, err := d.WithPatch(p)
	if err != nil {
		t.Fatal(err)
	}
	restore, err := l.Restore(managed.Routing())
	if err != nil {
		t.Fatal(err)
	}
	stop, finished := make(chan struct{}), make(chan struct{})
	faults := make(chan error, 1)
	var reads atomic.Uint64
	go func() {
		defer close(finished)
		for {
			select {
			case <-stop:
				return
			default:
			}
			data, err := os.ReadFile(path)
			if err != nil {
				faults <- err
				return
			}
			reads.Add(1)
			if !bytes.Equal(data, d.data) && !bytes.Equal(data, managed.data) {
				faults <- errors.New("outside reader saw an incomplete settings pair")
				return
			}
		}
	}()
	defer func() { close(stop); <-finished }()
	for i := 0; i < 8; i++ {
		if err := h.Apply(p, testLifecyclePermit); err != nil {
			t.Fatal(err)
		}
		if err := h.Apply(restore, testLifecyclePermit); err != nil {
			t.Fatal(err)
		}
	}
	select {
	case err := <-faults:
		t.Fatal(err)
	default:
	}
	if reads.Load() == 0 {
		t.Fatal("outside reader did not run")
	}
}

func TestSettingsFileRejectsUnsafeDirectoriesAndLocks(t *testing.T) {
	for _, kind := range []string{"directory-mode", "directory-symlink", "lock-symlink", "lock-mode"} {
		t.Run(kind, func(t *testing.T) {
			h, path := settingsFile(t)
			if err := h.Close(); err != nil {
				t.Fatal(err)
			}
			switch kind {
			case "directory-mode":
				if err := os.Chmod(filepath.Dir(path), 0777); err != nil {
					t.Fatal(err)
				}
			case "directory-symlink":
				alias := filepath.Dir(path) + ".alias"
				if err := os.Symlink(filepath.Dir(path), alias); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(alias, filepath.Base(path))
			case "lock-symlink":
				lock := filepath.Join(filepath.Dir(path), h.lockName)
				if err := os.Remove(lock); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(path, lock); err != nil {
					t.Fatal(err)
				}
			case "lock-mode":
				if err := os.Chmod(filepath.Join(filepath.Dir(path), h.lockName), 0644); err != nil {
					t.Fatal(err)
				}
			}
			if other, err := OpenSettingsFile(path); !errors.Is(err, ErrSettingsFile) {
				if other != nil {
					_ = other.Close()
				}
				t.Fatal("unsafe settings location admitted", err)
			}
		})
	}
}

func TestSettingsFilePreservesPrimaryServiceGroupWritePermissions(t *testing.T) {
	h, path := settingsFile(t)
	if err := h.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Dir(path), 0770); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0660); err != nil {
		t.Fatal(err)
	}
	h, err := OpenSettingsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	l, p := filePlan(t, h)
	if err := h.Apply(p, testLifecyclePermit); err != nil {
		t.Fatal(err)
	}
	view, err := h.Read()
	if err != nil {
		t.Fatal(err)
	}
	restore, err := l.Restore(view.Routing())
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Apply(restore, testLifecyclePermit); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0660 || string(fileBytes(t, path)) != settingsExample {
		t.Fatal("service-group policy or original bytes changed")
	}
}

func TestSettingsFileRefusesForeignGroupWritePermissions(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("ownership mutation needs isolated root fixture")
	}
	for _, directory := range []bool{false, true} {
		t.Run(fmt.Sprint(directory), func(t *testing.T) {
			h, path := settingsFile(t)
			if err := h.Close(); err != nil {
				t.Fatal(err)
			}
			target, mode := path, os.FileMode(0660)
			if directory {
				target, mode = filepath.Dir(path), 0770
			}
			if err := os.Chown(target, -1, 1234); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(target, mode); err != nil {
				t.Fatal(err)
			}
			if other, err := OpenSettingsFile(path); !errors.Is(err, ErrSettingsFile) {
				if other != nil {
					_ = other.Close()
				}
				t.Fatal("foreign writable group accepted", err)
			}
		})
	}
}
