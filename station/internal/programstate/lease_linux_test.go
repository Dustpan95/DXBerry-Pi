package programstate

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func leaseWriter(t *testing.T, s *Store, blocked bool) {
	leaseProcess(t, s, false, blocked)
}

func leaseProcess(t *testing.T, s *Store, reader, blocked bool) {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestReadLeaseWriterProcess$")
	cmd.Env = append(os.Environ(), "DXB_LEASE_WRITER="+s.state)
	if reader {
		cmd.Env = append(cmd.Env, "DXB_LEASE_READER=1")
	}
	err = cmd.Run()
	if blocked {
		var exit *exec.ExitError
		if !errors.As(err, &exit) || exit.ExitCode() != 11 {
			t.Fatalf("process escaped held lease: %v", err)
		}
	} else if err != nil {
		t.Fatalf("writer did not recover: %v", err)
	}
}

func TestReadLeaseWriterProcess(t *testing.T) {
	root := os.Getenv("DXB_LEASE_WRITER")
	if root == "" {
		return
	}
	s, err := NewStore(root)
	if err != nil {
		os.Exit(3)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if os.Getenv("DXB_LEASE_READER") == "1" {
		err = s.WithReadLease(ctx, func(context.Context, Generation) error { return nil })
	} else {
		_, err = s.Advance(ctx)
	}
	if errors.Is(err, context.DeadlineExceeded) {
		os.Exit(11)
	}
	if err != nil {
		os.Exit(12)
	}
	os.Exit(0)
}

func TestReadLeaseExcludesProcessWriterAndAllowsReaders(t *testing.T) {
	s := rootStore(t)
	before := mustAdvance(t, s)
	err := s.WithReadLease(context.Background(), func(ctx context.Context, g Generation) error {
		if !g.Same(before) {
			t.Fatal("wrong generation")
		}
		leaseWriter(t, s, true)
		return s.WithReadLease(ctx, func(_ context.Context, other Generation) error {
			if !g.Same(other) {
				t.Fatal("concurrent readers disagree")
			}
			return nil
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	leaseWriter(t, s, false)
	after, err := s.Read(context.Background())
	if err != nil || before.Same(after) {
		t.Fatalf("writer failed to advance after lease: %v", err)
	}
}

func TestReadLeaseCancellationDoesNotReleaseRunningCallback(t *testing.T) {
	s := rootStore(t)
	mustAdvance(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := s.WithReadLease(ctx, func(callbackCtx context.Context, _ Generation) error {
		cancel()
		if !errors.Is(callbackCtx.Err(), context.Canceled) {
			t.Fatal("callback did not receive caller cancellation")
		}
		leaseWriter(t, s, true)
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	leaseWriter(t, s, false)
}

func TestReadLeaseCallbackUsesCallerDeadline(t *testing.T) {
	s := rootStore(t)
	mustAdvance(t, s)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.WithReadLease(ctx, func(callbackCtx context.Context, _ Generation) error {
		if callbackCtx != ctx {
			t.Fatal("callback context was replaced")
		}
		timer := time.NewTimer(operationLimit + 25*time.Millisecond)
		defer timer.Stop()
		select {
		case <-callbackCtx.Done():
			return callbackCtx.Err()
		case <-timer.C:
			return nil
		}
	})
	if err != nil {
		t.Fatalf("preflight deadline leaked into callback/postflight: %v", err)
	}
}

func TestReadLeaseRejectsReplacedLockAfterWaiting(t *testing.T) {
	s := rootStore(t)
	mustAdvance(t, s)
	path := filepath.Join(s.state, "program-state", "lock")
	lock, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result := make(chan error, 1)
	go func() {
		result <- s.WithReadLease(ctx, func(context.Context, Generation) error {
			return errors.New("callback ran with replaced lock")
		})
	}()
	// Observe a second descriptor for the actual inode, without opening or
	// duplicating it. Replacement then happens after the reader opened the lock.
	var held unix.Stat_t
	if err := unix.Fstat(int(lock.Fd()), &held); err != nil {
		t.Fatal(err)
	}
	for {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		matches := 0
		for _, entry := range entries {
			var st unix.Stat_t
			if unix.Stat("/proc/self/fd/"+entry.Name(), &st) == nil && st.Dev == held.Dev && st.Ino == held.Ino {
				matches++
			}
		}
		if matches >= 2 {
			break
		}
		select {
		case err := <-result:
			t.Fatalf("reader returned before contention: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	if err := os.WriteFile(path+".new", nil, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(path+".new", path); err != nil {
		t.Fatal(err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err := <-result; !errors.Is(err, ErrUnavailable) {
		t.Fatalf("replaced lock accepted: %v", err)
	}
}

func TestReadLeaseRejectsNilCallbackAndUnprivilegedUse(t *testing.T) {
	s, _ := NewStore(filepath.Join(t.TempDir(), "absent"))
	if err := s.WithReadLease(context.Background(), nil); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if err := (*Store)(nil).WithReadLease(context.Background(), func(context.Context, Generation) error { return nil }); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		if err := s.WithAdvanceLease(context.Background(), func(context.Context, Generation) error {
			t.Fatal("unprivileged mutation ran")
			return nil
		}); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		if err := s.WithReadLease(context.Background(), func(context.Context, Generation) error {
			t.Fatal("unprivileged callback ran")
			return nil
		}); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
	if _, err := os.Lstat(s.state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("refusal initialized state: %v", err)
	}
}

func TestReadLeaseAcquisitionRefusesCanceledOrExclusiveLock(t *testing.T) {
	s := rootStore(t)
	mustAdvance(t, s)
	lock, err := os.Open(filepath.Join(s.state, "program-state", "lock"))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	for _, canceled := range []bool{false, true} {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		want := context.DeadlineExceeded
		if canceled {
			cancel()
			want = context.Canceled
		}
		err := s.WithReadLease(ctx, func(context.Context, Generation) error {
			t.Fatal("callback ran without lease")
			return nil
		})
		cancel()
		if !errors.Is(err, want) {
			t.Fatal(err)
		}
	}
}

func TestReadLeaseRefusesMissingOrUnsafeMetadataWithoutRepair(t *testing.T) {
	for _, fault := range []string{"absent", "missing-lock", "mode", "content", "symlink", "hardlink", "fifo", "epoch"} {
		t.Run(fault, func(t *testing.T) {
			s := rootStore(t)
			if fault != "absent" {
				mustAdvance(t, s)
			}
			lock := filepath.Join(s.state, "program-state", "lock")
			var err error
			switch fault {
			case "missing-lock":
				err = os.Remove(lock)
			case "mode":
				err = os.Chmod(lock, 0644)
			case "content":
				err = os.WriteFile(lock, []byte("damaged"), 0600)
			case "symlink", "fifo":
				if err = os.Remove(lock); err == nil {
					if fault == "symlink" {
						err = os.Symlink(epochPath(s), lock)
					} else {
						err = unix.Mkfifo(lock, 0600)
					}
				}
			case "hardlink":
				err = os.Link(lock, lock+".other")
			case "epoch":
				err = os.WriteFile(epochPath(s), []byte("damaged"), 0600)
			}
			if err != nil {
				t.Fatal(err)
			}
			err = s.WithReadLease(context.Background(), func(context.Context, Generation) error {
				t.Fatal("callback ran with invalid state")
				return nil
			})
			if !errors.Is(err, ErrUnavailable) {
				t.Fatal(err)
			}
			if fault == "absent" || fault == "missing-lock" {
				if _, err := os.Lstat(lock); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("lease created missing state: %v", err)
				}
			}
		})
	}
}

func TestReadLeasePostcheckAndCallbackErrors(t *testing.T) {
	callbackErr := errors.New("callback failed after possible effects")
	for _, replacement := range []string{"none", "lock", "epoch", "directory", "parent"} {
		t.Run(replacement, func(t *testing.T) {
			s := rootStore(t)
			before := mustAdvance(t, s)
			err := s.WithReadLease(context.Background(), func(context.Context, Generation) error {
				path := filepath.Join(s.state, "program-state", replacement)
				switch replacement {
				case "lock", "epoch":
					raw, err := os.ReadFile(path)
					if err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(path+".new", raw, 0600); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(path+".new", path); err != nil {
						t.Fatal(err)
					}
				case "directory", "parent":
					path = filepath.Join(s.state, "program-state")
					if replacement == "parent" {
						path = s.state
					}
					if err := os.Rename(path, path+".old"); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(path, 0700); err != nil {
						t.Fatal(err)
					}
				}
				return callbackErr
			})
			if !errors.Is(err, callbackErr) {
				t.Fatalf("lost callback error: %v", err)
			}
			if replacement != "none" && (!errors.Is(err, ErrUnavailable) || !errors.Is(err, ErrUncertain)) {
				t.Fatalf("lost post-callback uncertainty: %v", err)
			}
			if replacement == "none" {
				after, err := s.Read(context.Background())
				if err != nil || !before.Same(after) {
					t.Fatalf("read lease rotated generation: %v", err)
				}
				leaseWriter(t, s, false)
			}
		})
	}
}
