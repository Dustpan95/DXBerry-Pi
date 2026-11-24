package wsjtx

import (
	"bytes"
	"errors"
	"fmt"
	"math/bits"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
)

func persistentObserver(t *testing.T) (*Observer, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "history")
	if err := CreateEndpointHistory(path); err != nil {
		t.Fatal(err)
	}
	o := reopenObserver(t, path)
	return o, path
}

func reopenObserver(t *testing.T, path string) *Observer {
	t.Helper()
	o, err := NewPersistentObserver(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = o.Close() })
	return o
}

func historyBytes(t *testing.T, path string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(path, historyName))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func TestEndpointHistoryRequiresExplicitNewDirectory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	if _, err := NewPersistentObserver(nil, path); !errors.Is(err, ErrEndpointHistory) {
		t.Fatal("missing history was not refused", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("opening missing history created state", err)
	}
	if err := CreateEndpointHistory(path); err != nil {
		t.Fatal(err)
	}
	before := historyBytes(t, path)
	if err := CreateEndpointHistory(path); !errors.Is(err, ErrEndpointHistory) {
		t.Fatal("existing history reset", err)
	}
	if !bytes.Equal(before, historyBytes(t, path)) {
		t.Fatal("initialization modified existing history")
	}
	if err := os.Remove(filepath.Join(path, historyName)); err != nil {
		t.Fatal(err)
	}
	if err := CreateEndpointHistory(path); !errors.Is(err, ErrEndpointHistory) {
		t.Fatal("missing journal in existing directory was reinitialized", err)
	}
}

func TestEndpointHistorySurvivesObserverRestart(t *testing.T) {
	o, path := persistentObserver(t)
	b, err := o.Start("radio1", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	o = reopenObserver(t, path)
	// Force the kernel candidate to the previous generation's now-free port.
	attempts := 0
	o.listen = func(string, *net.UDPAddr) (*net.UDPConn, error) {
		attempts++
		return net.ListenUDP("udp4", net.UDPAddrFromAddrPort(b.Address()))
	}
	if _, err := o.Start("radio1", fixtureID); !errors.Is(err, ErrNoFreshEndpoint) || attempts != freshBindAttempts {
		t.Fatalf("restarted observer reused retired endpoint: %v (%d)", err, attempts)
	}
	o.listen = net.ListenUDP
	fresh, err := o.Start("radio1", fixtureID)
	if err != nil || fresh.Address() == b.Address() {
		t.Fatal("fresh endpoint unavailable or reused", fresh.Address(), err)
	}
	late := udpSender(t, b.Address())
	send(t, late, fixture(t, "status-full-s3"))
	s, _ := o.Snapshot("radio1")
	if s.Observation.Counters.Accepted != 0 || s.Observation.LastSeen != nil {
		t.Fatal("old endpoint traffic reached replacement")
	}
	send(t, udpSender(t, fresh.Address()), fixture(t, "heartbeat-s3"))
	if received(t, o, "radio1", 1).Observation.Status != nil {
		t.Fatal("retired Status adopted")
	}
}

func TestEndpointHistoryHasOneOwnerUntilObserverClose(t *testing.T) {
	o, path := persistentObserver(t)
	for i := 0; i < 2; i++ {
		if other, err := NewPersistentObserver(nil, path); !errors.Is(err, ErrEndpointHistory) {
			if other != nil {
				_ = other.Close()
			}
			t.Fatal("concurrent history owner admitted", err)
		}
	}
	b, err := o.Start("radio1", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if !o.Stop(b) {
		t.Fatal("stop failed")
	}
	if other, err := NewPersistentObserver(nil, path); !errors.Is(err, ErrEndpointHistory) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatal("stopping one receiver released observer ownership", err)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if err := o.Close(); err != nil {
		t.Fatal("close was not idempotent", err)
	}
	reopenObserver(t, path)
}

func TestEndpointHistoryReservationPrecedesPublication(t *testing.T) {
	o, path := persistentObserver(t)
	old, err := o.Start("radio1", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	before := historyBytes(t, path)
	commits := 0
	o.history.write = func(data []byte) error {
		commits++
		prior, ok := o.tracker.Snapshot("radio1")
		if !ok || prior.Generation != old.Generation() || o.receivers["radio1"].addr != old.Address() {
			t.Error("replacement published before durable reservation")
		}
		if !bytes.Equal(historyBytes(t, path), before) {
			t.Error("history changed before transaction")
		}
		if err := o.history.replace(data); err != nil {
			return err
		}
		if !bytes.Equal(historyBytes(t, path), data) {
			t.Error("committed reservation not readable")
		}
		return nil
	}
	fresh, err := o.Start("radio1", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if commits != 1 || fresh.Address() == old.Address() || fresh.Generation() == old.Generation() {
		t.Fatal("replacement was not committed exactly once")
	}
	if o.history.bits != o.used {
		t.Fatal("memory and durable port inventories differ")
	}
}

func TestEndpointHistoryRejectsInvalidAndUnsafeState(t *testing.T) {
	mutations := map[string]func(string) error{
		"missing":   func(p string) error { return os.Remove(filepath.Join(p, historyName)) },
		"truncated": func(p string) error { return os.Truncate(filepath.Join(p, historyName), int64(historySize-1)) },
		"oversized": func(p string) error { return os.Truncate(filepath.Join(p, historyName), int64(historySize+1)) },
		"checksum": func(p string) error {
			data := historyBytes(t, p)
			data[20] ^= 1
			return os.WriteFile(filepath.Join(p, historyName), data, 0600)
		},
		"version": func(p string) error {
			data := historyBytes(t, p)
			data[7] = '2'
			return os.WriteFile(filepath.Join(p, historyName), data, 0600)
		},
		"public-file":      func(p string) error { return os.Chmod(filepath.Join(p, historyName), 0644) },
		"public-directory": func(p string) error { return os.Chmod(p, 0755) },
		"missing-lock":     func(p string) error { return os.Remove(filepath.Join(p, historyLock)) },
		"public-lock":      func(p string) error { return os.Chmod(filepath.Join(p, historyLock), 0644) },
		"hard-link":        func(p string) error { return os.Link(filepath.Join(p, historyName), filepath.Join(p, "copy")) },
		"symlink": func(p string) error {
			if err := os.Rename(filepath.Join(p, historyName), filepath.Join(p, "copy")); err != nil {
				return err
			}
			return os.Symlink("copy", filepath.Join(p, historyName))
		},
		"fifo": func(p string) error {
			if err := os.Remove(filepath.Join(p, historyName)); err != nil {
				return err
			}
			return syscall.Mkfifo(filepath.Join(p, historyName), 0600)
		},
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "history")
			if err := CreateEndpointHistory(path); err != nil {
				t.Fatal(err)
			}
			if err := mutate(path); err != nil {
				t.Fatal(err)
			}
			if o, err := NewPersistentObserver(nil, path); !errors.Is(err, ErrEndpointHistory) {
				if o != nil {
					_ = o.Close()
				}
				t.Fatal("invalid history accepted", err)
			}
		})
	}
}

func TestEndpointHistoryRejectsDirectorySymlink(t *testing.T) {
	o, path := persistentObserver(t)
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(filepath.Dir(path), "alias")
	if err := os.Symlink(path, link); err != nil {
		t.Fatal(err)
	}
	if other, err := NewPersistentObserver(nil, link); !errors.Is(err, ErrEndpointHistory) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatal("directory symlink accepted", err)
	}
}

func TestEndpointHistoryFailedWritePreservesCurrentReceiver(t *testing.T) {
	for _, afterRename := range []bool{false, true} {
		t.Run(fmt.Sprint(afterRename), func(t *testing.T) {
			o, path := persistentObserver(t)
			b, err := o.Start("radio1", fixtureID)
			if err != nil {
				t.Fatal(err)
			}
			client := udpSender(t, b.Address())
			send(t, client, fixture(t, "status-full-s3"))
			received(t, o, "radio1", 1)
			before := historyBytes(t, path)
			failure := errors.New("injected uncertain storage failure")
			var attempted []byte
			var rejected *net.UDPAddr
			o.listen = func(network string, addr *net.UDPAddr) (*net.UDPConn, error) {
				c, err := net.ListenUDP(network, addr)
				if err == nil {
					rejected = c.LocalAddr().(*net.UDPAddr)
				}
				return c, err
			}
			o.history.write = func(data []byte) error {
				attempted = append([]byte(nil), data...)
				if afterRename {
					if err := o.history.replace(data); err != nil {
						return err
					}
				}
				return failure
			}
			if _, err := o.Start("radio1", fixtureID); !errors.Is(err, failure) || !errors.Is(err, ErrEndpointHistory) {
				t.Fatal("storage failure hidden", err)
			}
			if len(attempted) != historySize {
				t.Fatal("no reserve was attempted")
			}
			if rejected == nil {
				t.Fatal("no candidate socket")
			}
			probe, err := net.ListenUDP("udp4", rejected)
			if err != nil {
				t.Fatal("failed reservation leaked socket", err)
			}
			_ = probe.Close()
			if afterRename == bytes.Equal(before, historyBytes(t, path)) {
				t.Fatal("unexpected injected commit outcome")
			}
			send(t, client, fixture(t, "heartbeat-s3"))
			s := received(t, o, "radio1", 2)
			if s.Address != b.Address() || s.Observation.Generation != b.Generation() || s.Observation.Status == nil {
				t.Fatal("storage failure retired working receiver")
			}
			o.listen = func(string, *net.UDPAddr) (*net.UDPConn, error) {
				t.Error("poisoned owner bound a socket")
				return nil, failure
			}
			if _, err := o.Start("radio2", fixtureID); !errors.Is(err, failure) {
				t.Fatal("uncertain owner retried", err)
			}
			if err := o.Close(); err != nil {
				t.Fatal(err)
			}
			restarted := reopenObserver(t, path)
			if afterRename && !bytes.Equal(encodeHistory(restarted.used), attempted) {
				t.Fatal("uncertain committed reservation lost on reopen")
			}
			if !afterRename && !bytes.Equal(encodeHistory(restarted.used), before) {
				t.Fatal("failed reservation changed prior state")
			}
		})
	}
}

func TestEndpointHistoryChangedStateStopsNewBindings(t *testing.T) {
	for _, change := range []string{"bits", "missing", "lock", "directory"} {
		t.Run(change, func(t *testing.T) {
			o, path := persistentObserver(t)
			b, err := o.Start("radio1", fixtureID)
			if err != nil {
				t.Fatal(err)
			}
			switch change {
			case "bits":
				other := o.used
				other[0] ^= 2
				err = os.WriteFile(filepath.Join(path, historyName), encodeHistory(other), 0600)
			case "missing":
				err = os.Remove(filepath.Join(path, historyName))
			case "lock":
				err = os.Rename(filepath.Join(path, historyLock), filepath.Join(path, "old-lock"))
				if err == nil {
					err = os.WriteFile(filepath.Join(path, historyLock), nil, 0600)
				}
			case "directory":
				err = os.Rename(path, path+"-old")
				if err == nil {
					err = os.Mkdir(path, 0700)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			o.listen = func(string, *net.UDPAddr) (*net.UDPConn, error) {
				t.Error("changed history reached bind")
				return nil, errors.New("bind")
			}
			if _, err := o.Start("radio1", fixtureID); !errors.Is(err, ErrEndpointHistory) {
				t.Fatal("changed history accepted", err)
			}
			client := udpSender(t, b.Address())
			send(t, client, fixture(t, "heartbeat-s3"))
			if received(t, o, "radio1", 1).Observation.Generation != b.Generation() {
				t.Fatal("history change retired existing receiver")
			}
		})
	}
}

func TestEndpointHistoryConcurrentStartsAreRetained(t *testing.T) {
	o, path := persistentObserver(t)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 8; j++ {
				b, err := o.Start(fmt.Sprintf("radio%d", i), fixtureID)
				if err != nil {
					t.Error(err)
					return
				}
				if !o.Stop(b) {
					t.Error("stop failed")
				}
			}
		}(i)
	}
	wg.Wait()
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	o = reopenObserver(t, path)
	count := 0
	for _, word := range o.used {
		count += bits.OnesCount64(word)
	}
	if count != 32 {
		t.Fatalf("lost concurrent reservation: %d", count)
	}
	entries, err := os.ReadDir(path)
	if err != nil || len(entries) != 2 {
		t.Fatal("temporary files leaked", entries, err)
	}
}

func TestEndpointHistoryExhaustionDoesNotReset(t *testing.T) {
	o, path := persistentObserver(t)
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	var full [1024]uint64
	for i := range full {
		full[i] = ^uint64(0)
	}
	if err := os.WriteFile(filepath.Join(path, historyName), encodeHistory(full), 0600); err != nil {
		t.Fatal(err)
	}
	o = reopenObserver(t, path)
	if _, err := o.Start("radio1", fixtureID); !errors.Is(err, ErrNoFreshEndpoint) {
		t.Fatal("exhausted history reused a port", err)
	}
	if !bytes.Equal(historyBytes(t, path), encodeHistory(full)) {
		t.Fatal("exhaustion reset history")
	}
}

func TestEndpointHistoryIgnoresAndReplacesCrashStaging(t *testing.T) {
	o, path := persistentObserver(t)
	b, err := o.Start("radio1", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	committed := o.used
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	// Staging may be partial, or a complete but never-published transaction.
	for _, pending := range [][]byte{[]byte("partial"), encodeHistory([1024]uint64{})} {
		if err := os.WriteFile(filepath.Join(path, historyPending), pending, 0600); err != nil {
			t.Fatal(err)
		}
		o = reopenObserver(t, path)
		if o.used != committed {
			t.Fatal("uncommitted staging replaced actual history")
		}
		fresh, err := o.Start("radio1", fixtureID)
		if err != nil || fresh.Address() == b.Address() {
			t.Fatal("crash staging blocked valid start or lost retired port", err)
		}
		committed = o.used
		if err := o.Close(); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(path)
		if err != nil || len(entries) != 2 {
			t.Fatal("crash staging not reclaimed", err)
		}
	}
}

func TestEndpointHistoryUncleanExit(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history")
	if err := CreateEndpointHistory(path); err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(t.TempDir(), "port")
	cmd := exec.Command(os.Args[0], "-test.run=^TestEndpointHistoryExitHelper$")
	cmd.Env = append(os.Environ(), "DXB_HISTORY_EXIT_TEST="+path, "DXB_HISTORY_EXIT_PORT="+output)
	if data, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("helper: %v: %s", err, data)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	port, err := strconv.ParseUint(string(data), 10, 16)
	if err != nil {
		t.Fatal(err)
	}
	o := reopenObserver(t, path)
	if o.used[port/64]&(uint64(1)<<(port%64)) == 0 {
		t.Fatal("endpoint lost when process exited without Close")
	}
}

func TestEndpointHistoryExitHelper(t *testing.T) {
	path := os.Getenv("DXB_HISTORY_EXIT_TEST")
	if path == "" {
		return
	}
	o, err := NewPersistentObserver(nil, path)
	if err != nil {
		t.Fatal(err)
	}
	b, err := o.Start("radio1", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(os.Getenv("DXB_HISTORY_EXIT_PORT"), []byte(strconv.Itoa(int(b.Address().Port()))), 0600); err != nil {
		t.Fatal(err)
	}
	os.Exit(0) // Deliberately skip Observer.Close and all Go cleanup.
}
