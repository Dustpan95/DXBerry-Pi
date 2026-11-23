package programstate

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func rootStore(t *testing.T) *Store {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("root-owned storage positive fixture requires root")
	}
	s, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func mustAdvance(t *testing.T, s *Store) Generation {
	t.Helper()
	g, err := s.Advance(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func epochPath(s *Store) string { return filepath.Join(s.state, "program-state", "epoch") }

func epochBytes(t *testing.T, s *Store) []byte {
	t.Helper()
	b, err := os.ReadFile(epochPath(s))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestConstructionAndReadNeverInitialize(t *testing.T) {
	state := filepath.Join(t.TempDir(), "absent")
	s, err := NewStore(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.Read(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := os.Lstat(state); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("read created state: %v", err)
	}
	for _, name := range []string{"", ".", "/", "/tmp/../tmp", "/tmp/", "/tmp/a\x00b"} {
		if _, err := NewStore(name); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("accepted %q: %v", name, err)
		}
	}
	var empty Store
	if _, err := empty.Read(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	if _, err := (*Store)(nil).Advance(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestGenerationPersistenceRotationAndPrivacy(t *testing.T) {
	s := rootStore(t)
	if _, err := s.Read(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(s.state)
	if len(entries) != 0 {
		t.Fatal("read created private directory")
	}
	first := mustAdvance(t, s)
	read, err := s.Read(context.Background())
	if err != nil || !first.Same(read) {
		t.Fatalf("read: %v", err)
	}
	raw := epochBytes(t, s)
	if len(raw) != 65 || string(raw) != hex.EncodeToString(first.token[:])+"\n" {
		t.Fatal("noncanonical epoch")
	}
	for path, mode := range map[string]os.FileMode{"program-state": 0700, "program-state/epoch": 0600, "program-state/lock": 0600} {
		st, err := os.Stat(filepath.Join(s.state, path))
		if err != nil || st.Mode().Perm() != mode {
			t.Fatalf("%s: %v", path, err)
		}
	}
	other, _ := NewStore(s.state)
	reopened, err := other.Read(context.Background())
	if err != nil || reopened.token != first.token || first.Same(reopened) {
		t.Fatalf("new reader identity: %v", err)
	}
	next := mustAdvance(t, s)
	if first.Same(next) || first.token == next.token {
		t.Fatal("rotation reused generation")
	}
	for _, g := range []Generation{first, next, {}} {
		//lint:ignore SA9005 This regression requires private evidence to serialize as an empty object.
		b, err := json.Marshal(g)
		if err != nil || string(b) != "{}" {
			t.Fatalf("private evidence serialized: %s %v", b, err)
		}
	}
	if (Generation{}).Same(Generation{}) {
		t.Fatal("zero receipt valid")
	}
	entries, _ = os.ReadDir(filepath.Join(s.state, "program-state"))
	if len(entries) != 2 {
		t.Fatalf("temporary files leaked: %v", entries)
	}
}

func TestGenerationChangesEvenWhenOtherStateReturns(t *testing.T) {
	s := rootStore(t)
	before := mustAdvance(t, s)
	// No unit state is stored here. A writer can rotate before an activation,
	// then discard its simulated unit metadata entirely: the epoch still changes.
	unit := filepath.Join(t.TempDir(), "unit-state")
	if err := os.WriteFile(unit, []byte("inactive"), 0600); err != nil {
		t.Fatal(err)
	}
	mustAdvance(t, s)
	if err := os.WriteFile(unit, []byte("running"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(unit); err != nil {
		t.Fatal(err)
	}
	after, err := s.Read(context.Background())
	if err != nil || before.Same(after) {
		t.Fatalf("intervening rotation lost: %v", err)
	}
	// This is a writer-protocol fixture, not actual systemd lifecycle acceptance.
}

func TestRestrictiveUmaskCannotPublishAnInvalidEpoch(t *testing.T) {
	s := rootStore(t)
	before := mustAdvance(t, s)
	previous := unix.Umask(0777)
	defer unix.Umask(previous)
	after, err := s.Advance(context.Background())
	if err != nil {
		t.Fatalf("private new epoch must retain mode 0600 despite umask: %v", err)
	}
	if before.Same(after) {
		t.Fatal("generation did not advance")
	}
	st, err := os.Stat(epochPath(s))
	if err != nil || st.Mode().Perm() != 0600 {
		t.Fatalf("invalid published mode: %v", err)
	}
}

func TestIdenticalReplacementIsAChangedReceipt(t *testing.T) {
	s := rootStore(t)
	before := mustAdvance(t, s)
	temp := epochPath(s) + ".replacement"
	if err := os.WriteFile(temp, epochBytes(t, s), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(temp, epochPath(s)); err != nil {
		t.Fatal(err)
	}
	after, err := s.Read(context.Background())
	if err != nil || before.token != after.token || before.Same(after) {
		t.Fatalf("file replacement hidden: %v", err)
	}
}

func TestUntrustedOrDamagedStateIsNeverRepaired(t *testing.T) {
	cases := []string{"bad-bytes", "oversize", "zero", "upper", "mode", "hardlink", "symlink", "fifo", "directory", "child-mode", "child-symlink", "parent-mode", "ancestor-symlink", "owner"}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			s := rootStore(t)
			mustAdvance(t, s)
			p := epochPath(s)
			original := epochBytes(t, s)
			switch name {
			case "bad-bytes":
				os.WriteFile(p, []byte("damaged\n"), 0600)
			case "oversize":
				os.WriteFile(p, bytes.Repeat([]byte("a"), 128<<10), 0600)
			case "zero":
				os.WriteFile(p, []byte(strings.Repeat("0", 64)+"\n"), 0600)
			case "upper":
				os.WriteFile(p, []byte(strings.Repeat("A", 64)+"\n"), 0600)
			case "mode":
				os.Chmod(p, 0644)
			case "hardlink":
				if err := os.Link(p, p+".alias"); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				os.Rename(p, p+".kept")
				os.Symlink(p+".kept", p)
			case "fifo":
				os.Remove(p)
				if err := unix.Mkfifo(p, 0600); err != nil {
					t.Fatal(err)
				}
			case "directory":
				os.Remove(p)
				os.Mkdir(p, 0600)
			case "child-mode":
				os.Chmod(filepath.Dir(p), 0755)
			case "child-symlink":
				os.Rename(filepath.Dir(p), filepath.Dir(p)+".kept")
				os.Symlink(filepath.Dir(p)+".kept", filepath.Dir(p))
			case "parent-mode":
				os.Chmod(s.state, 0777)
			case "ancestor-symlink":
				os.Symlink(s.state, s.state+".alias")
				s.state = filepath.Join(s.state+".alias", "unused")
				os.Mkdir(filepath.Join(filepath.Dir(p), "..", "unused"), 0700)
			case "owner":
				if err := os.Chown(p, 65534, 65534); err != nil {
					t.Fatal(err)
				}
			}
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			if _, err := s.Read(ctx); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("read accepted %s: %v", name, err)
			}
			if _, err := s.Advance(ctx); !errors.Is(err, ErrUnavailable) {
				t.Fatalf("advance repaired %s: %v", name, err)
			}
			if name == "mode" || name == "hardlink" || name == "owner" {
				if !bytes.Equal(original, epochBytes(t, s)) {
					t.Fatal("changed damaged state")
				}
			}
		})
	}
}

func TestUntrustedLockRefusesRotation(t *testing.T) {
	for _, name := range []string{"mode", "symlink", "hardlink", "fifo", "bytes", "owner"} {
		t.Run(name, func(t *testing.T) {
			s := rootStore(t)
			before := mustAdvance(t, s)
			p := filepath.Join(s.state, "program-state", "lock")
			switch name {
			case "mode":
				os.Chmod(p, 0666)
			case "symlink":
				os.Rename(p, p+".old")
				os.Symlink(p+".old", p)
			case "hardlink":
				os.Link(p, p+".alias")
			case "fifo":
				os.Remove(p)
				unix.Mkfifo(p, 0600)
			case "bytes":
				os.WriteFile(p, []byte("x"), 0600)
			case "owner":
				os.Chown(p, 65534, 65534)
			}
			if _, err := s.Advance(context.Background()); !errors.Is(err, ErrUnavailable) {
				t.Fatal(err)
			}
			after, err := s.Read(context.Background())
			if err != nil || !before.Same(after) {
				t.Fatalf("epoch changed: %v", err)
			}
		})
	}
}

func TestPublicationFailuresPreserveOrReportUncertain(t *testing.T) {
	for failAt := 1; failAt <= 3; failAt++ {
		t.Run(string(rune('0'+failAt)), func(t *testing.T) {
			s := rootStore(t)
			before := mustAdvance(t, s)
			calls := 0
			s.sync = func(f *os.File) error {
				calls++
				if calls == failAt {
					return io.ErrUnexpectedEOF
				}
				return f.Sync()
			}
			g, err := s.Advance(context.Background())
			if g.store != nil || err == nil {
				t.Fatal("failed publication returned evidence")
			}
			if errors.Is(err, ErrUncertain) != (failAt == 3) {
				t.Fatalf("wrong uncertainty: %v", err)
			}
			after, readErr := s.Read(context.Background())
			if readErr != nil {
				t.Fatal(readErr)
			}
			if before.Same(after) != (failAt != 3) {
				t.Fatalf("publication/rollback state mismatch at sync %d", failAt)
			}
		})
	}
}

func TestEntropyFailureAndCollisionPreserveEpoch(t *testing.T) {
	for _, name := range []string{"failed", "zeros", "same", "temp-exists", "cancel"} {
		t.Run(name, func(t *testing.T) {
			s := rootStore(t)
			before := mustAdvance(t, s)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			switch name {
			case "failed":
				s.random = bytes.NewReader(nil)
			case "zeros":
				s.random = bytes.NewReader(make([]byte, 128))
			case "same":
				s.random = bytes.NewReader(bytes.Repeat(before.token[:], 4))
			case "temp-exists":
				token := bytes.Repeat([]byte{0xa5}, 32)
				s.random = bytes.NewReader(token)
				p := filepath.Join(s.state, "program-state", ".epoch-"+hex.EncodeToString(token))
				if err := os.WriteFile(p, []byte("retained"), 0600); err != nil {
					t.Fatal(err)
				}
			case "cancel":
				s.random = &cancelEntropy{cancel: cancel}
			}
			_, err := s.Advance(ctx)
			if err == nil || errors.Is(err, ErrUncertain) {
				t.Fatalf("bad prepublication outcome: %v", err)
			}
			after, readErr := s.Read(context.Background())
			if readErr != nil || !before.Same(after) {
				t.Fatalf("old generation lost: %v", readErr)
			}
		})
	}
}

type cancelEntropy struct{ cancel context.CancelFunc }

func (r *cancelEntropy) Read(p []byte) (int, error) {
	for i := range p {
		p[i] = 0xa5
	}
	r.cancel()
	return len(p), nil
}

func TestPathAndLockReplacementDuringWrite(t *testing.T) {
	for _, where := range []string{"lock-before-rename", "directory-before-rename", "directory-after-rename"} {
		t.Run(where, func(t *testing.T) {
			s := rootStore(t)
			mustAdvance(t, s)
			calls := 0
			s.sync = func(f *os.File) error {
				calls++
				at := 2
				if where == "directory-after-rename" {
					at = 3
				}
				if calls == at {
					p := filepath.Join(s.state, "program-state")
					if where == "lock-before-rename" {
						if err := os.Rename(filepath.Join(p, "lock"), filepath.Join(p, "old-lock")); err != nil {
							return err
						}
						return os.WriteFile(filepath.Join(p, "lock"), nil, 0600)
					}
					if err := os.Rename(p, p+".kept"); err != nil {
						return err
					}
					if err := os.Mkdir(p, 0700); err != nil {
						return err
					}
				}
				return f.Sync()
			}
			_, err := s.Advance(context.Background())
			if err == nil || errors.Is(err, ErrUncertain) != (where == "directory-after-rename") {
				t.Fatalf("%s: %v", where, err)
			}
		})
	}
}

func TestConcurrentIndependentWritersAndFreshReaders(t *testing.T) {
	s := rootStore(t)
	mustAdvance(t, s)
	var wg sync.WaitGroup
	var mu sync.Mutex
	seen := map[[32]byte]bool{}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			writer, _ := NewStore(s.state)
			for j := 0; j < 5; j++ {
				g, err := writer.Advance(context.Background())
				if err != nil {
					t.Error(err)
					return
				}
				mu.Lock()
				if seen[g.token] {
					t.Error("generation reused")
				}
				seen[g.token] = true
				mu.Unlock()
			}
		}()
	}
	for i := 0; i < 20; i++ {
		g, err := s.Read(context.Background())
		if err == nil && g.token == [32]byte{} {
			t.Fatal("empty generation")
		}
	}
	wg.Wait()
	if len(seen) != 20 {
		t.Fatalf("only %d successful generations", len(seen))
	}
}

func TestSeparateProcessRespectsHeldLockAndRecovers(t *testing.T) {
	if root := os.Getenv("DXB_PROGRAMSTATE_CHILD"); root != "" {
		s, err := NewStore(root)
		if err != nil {
			os.Exit(3)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
		defer cancel()
		_, err = s.Advance(ctx)
		if errors.Is(err, context.DeadlineExceeded) {
			os.Exit(11)
		}
		if err != nil {
			os.Exit(12)
		}
		os.Exit(0)
	}
	s := rootStore(t)
	before := mustAdvance(t, s)
	f, err := os.OpenFile(filepath.Join(s.state, "program-state", "lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	run := func() error {
		cmd := exec.Command(exe, "-test.run=^TestSeparateProcessRespectsHeldLockAndRecovers$")
		cmd.Env = append(os.Environ(), "DXB_PROGRAMSTATE_CHILD="+s.state)
		return cmd.Run()
	}
	err = run()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 11 {
		t.Fatalf("child did not refuse contended lock: %v", err)
	}
	after, err := s.Read(context.Background())
	if err != nil || !before.Same(after) {
		t.Fatalf("child changed held generation: %v", err)
	}
	if err := unix.Flock(int(f.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	if err := run(); err != nil {
		t.Fatal(err)
	}
	after, err = s.Read(context.Background())
	if err != nil || before.Same(after) {
		t.Fatalf("child failed recovery: %v", err)
	}
}

func TestCanceledAndUnprivilegedOperationsAreClosed(t *testing.T) {
	state := t.TempDir()
	s, _ := NewStore(state)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.Read(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := s.Advance(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if os.Geteuid() != 0 {
		if _, err := s.Read(context.Background()); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
		if _, err := s.Advance(context.Background()); !errors.Is(err, ErrUnavailable) {
			t.Fatal(err)
		}
	}
	entries, _ := os.ReadDir(state)
	if len(entries) != 0 {
		t.Fatal("refused operation wrote files")
	}
}

func TestWriterDeathPreservesActiveEpochAndReleasesLock(t *testing.T) {
	if root := os.Getenv("DXB_PROGRAMSTATE_CRASH"); root != "" {
		s, err := NewStore(root)
		if err != nil {
			os.Exit(3)
		}
		calls := 0
		s.sync = func(f *os.File) error {
			if err := f.Sync(); err != nil {
				return err
			}
			calls++
			if calls == 2 {
				fmt.Fprintln(os.Stdout, "ready")
				<-time.After(time.Hour)
			}
			return nil
		}
		_, err = s.Advance(context.Background())
		if err != nil {
			os.Exit(4)
		}
		os.Exit(5)
	}
	s := rootStore(t)
	before := mustAdvance(t, s)
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestWriterDeathPreservesActiveEpochAndReleasesLock$")
	cmd.Env = append(os.Environ(), "DXB_PROGRAMSTATE_CRASH="+s.state)
	output, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	t.Cleanup(func() {
		if !waited {
			cmd.Process.Kill()
			cmd.Wait()
		}
	})
	line, err := bufio.NewReader(output).ReadString('\n')
	if err != nil || line != "ready\n" {
		t.Fatalf("child did not reach prepublication sync: %q %v", line, err)
	}
	read, err := s.Read(context.Background())
	if err != nil || !before.Same(read) {
		t.Fatalf("unpublished epoch became active: %v", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	if err := cmd.Wait(); err == nil {
		t.Fatal("killed child reported success")
	}
	waited = true
	after := mustAdvance(t, s)
	if before.Same(after) {
		t.Fatal("lock was not released or generation was reused")
	}
	// The orphan's private .epoch-* name is not a readable active generation.
	entries, err := os.ReadDir(filepath.Dir(epochPath(s)))
	if err != nil {
		t.Fatal(err)
	}
	orphans := 0
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".epoch-") {
			orphans++
		}
	}
	if orphans != 1 {
		t.Fatalf("expected retained orphan, got %d", orphans)
	}
}

func FuzzDecode(f *testing.F) {
	for _, s := range []string{"", strings.Repeat("a", 64) + "\n", strings.Repeat("0", 64) + "\n", strings.Repeat("A", 64) + "\n", strings.Repeat("a", 65) + "\n"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, raw []byte) {
		token, ok := decode(raw)
		if ok && (token == [32]byte{} || hex.EncodeToString(token[:])+"\n" != string(raw)) {
			t.Fatal("accepted noncanonical token")
		}
	})
}
