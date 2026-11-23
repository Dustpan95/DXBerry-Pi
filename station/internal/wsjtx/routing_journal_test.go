package wsjtx

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"testing"
)

func routeJournal(t *testing.T) (*RoutingJournal, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "routes")
	if err := CreateRoutingJournal(path); err != nil {
		t.Fatal(err)
	}
	j := reopenJournal(t, path)
	return j, path
}

func reopenJournal(t *testing.T, path string) *RoutingJournal {
	t.Helper()
	j, err := OpenRoutingJournal(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = j.Close() })
	return j
}

func journalLease(t *testing.T, o *Observer, radio string, original RoutingSettings) (Binding, *RoutingLease, RoutingPatch, RoutingSettings) {
	t.Helper()
	b, err := o.Start(radio, fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	l, p, managed := routingClaim(t, b, original)
	return b, l, p, managed
}

func journalRecovery(t *testing.T, j *RoutingJournal, h *SettingsFile, radio string) {
	t.Helper()
	token, _, exists, err := j.Lookup(radio)
	if err != nil {
		t.Fatal(err)
	}
	if !exists {
		return
	}
	d, err := h.Read()
	if err != nil {
		t.Fatal(err)
	}
	p, err := j.PrepareRestore(token, d.Routing())
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Apply(p, func() error { return j.PermitRestore(token) }); err != nil {
		t.Fatal(err)
	}
	d, err = h.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := j.CompleteRestore(token, d.Routing()); err != nil {
		t.Fatal(err)
	}
}

func TestRoutingJournalRequiresExplicitInitializationAndExclusiveOwner(t *testing.T) {
	path := filepath.Join(t.TempDir(), "routes")
	if _, err := OpenRoutingJournal(path); !errors.Is(err, ErrRoutingJournal) {
		t.Fatal("missing journal accepted", err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("open created state", err)
	}
	if err := CreateRoutingJournal(path); err != nil {
		t.Fatal(err)
	}
	j := reopenJournal(t, path)
	before := fileBytes(t, filepath.Join(path, routingJournalName))
	if err := CreateRoutingJournal(path); !errors.Is(err, ErrRoutingJournal) {
		t.Fatal("existing journal reset", err)
	}
	if other, err := OpenRoutingJournal(path); !errors.Is(err, ErrRoutingJournal) {
		if other != nil {
			_ = other.Close()
		}
		t.Fatal("second owner admitted", err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	reopenJournal(t, path)
	if !bytes.Equal(before, fileBytes(t, filepath.Join(path, routingJournalName))) {
		t.Fatal("open/close changed state")
	}
}

func TestRoutingJournalGatesRealFileReplacementAndRestoration(t *testing.T) {
	j, _ := routeJournal(t)
	o, _ := persistentObserver(t)
	h, path := settingsFile(t)
	d, err := h.Read()
	if err != nil {
		t.Fatal(err)
	}
	b, l, p, managed := journalLease(t, o, "radio1", d.Routing())
	token, err := j.Prepare(l, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.PermitRestore(token); !errors.Is(err, ErrRoutingJournalPhase) {
		t.Fatal("restore allowed before durable intent", err)
	}
	if err := j.CompleteRestore(token, d.Routing()); !errors.Is(err, ErrRoutingJournalPhase) {
		t.Fatal("pending ownership forgotten", err)
	}
	if err := h.Apply(p, func() error { return j.PermitInstall(token) }); err != nil {
		t.Fatal(err)
	}
	if err := j.MarkInstalled(token, d.Routing()); !errors.Is(err, ErrRoutingConflict) {
		t.Fatal("unverified installation accepted", err)
	}
	if err := j.MarkInstalled(token, managed); err != nil {
		t.Fatal(err)
	}
	revision := j.data.Revision
	if err := j.MarkInstalled(token, managed); err != nil || j.data.Revision != revision {
		t.Fatal("installation confirmation not idempotent", err)
	}
	if err := j.PermitInstall(token); !errors.Is(err, ErrRoutingJournalPhase) {
		t.Fatal("installed record permitted a new install", err)
	}
	if _, err := j.Prepare(l, b); !errors.Is(err, ErrRoutingJournalBusy) {
		t.Fatal("outstanding ownership replaced", err)
	}
	journalRecovery(t, j, h, "radio1")
	if string(fileBytes(t, path)) != settingsExample {
		t.Fatal("original file not restored")
	}
	if _, _, exists, err := j.Lookup("radio1"); err != nil || exists {
		t.Fatal("restored ownership not released", err)
	}
	if err := j.PermitRestore(token); !errors.Is(err, ErrRoutingJournalToken) {
		t.Fatal("completed token remained active", err)
	}
}

func TestRoutingJournalRejectsEphemeralOrRetiredBindings(t *testing.T) {
	j, _ := routeJournal(t)
	o, _, b := observer(t)
	l, _, _ := routingClaim(t, b, routingSettings())
	if _, err := j.Prepare(l, b); !errors.Is(err, ErrRoutingBinding) {
		t.Fatal("memory-only endpoint journaled", err)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	po, _ := persistentObserver(t)
	b, l, _, _ = journalLease(t, po, "radio1", routingSettings())
	if !po.Stop(b) {
		t.Fatal("stop failed")
	}
	if _, err := j.Prepare(l, b); !errors.Is(err, ErrRoutingBinding) {
		t.Fatal("retired binding journaled", err)
	}
	if _, err := j.Prepare(nil, b); !errors.Is(err, ErrRoutingLease) {
		t.Fatal("nil lease accepted", err)
	}
}

func TestRoutingJournalBindsApprovalToTheExactObserver(t *testing.T) {
	j, _ := routeJournal(t)
	old, _, before := observer(t)
	lease, _, _ := routingClaim(t, before, routingSettings())
	if err := old.Close(); err != nil {
		t.Fatal(err)
	}
	o, _ := persistentObserver(t)
	o.listen = func(string, *net.UDPAddr) (*net.UDPConn, error) {
		return net.ListenUDP("udp4", net.UDPAddrFromAddrPort(before.Address()))
	}
	after, err := o.Start(before.Radio(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if before.Address() != after.Address() || before.Generation() != after.Generation() {
		t.Fatal("test did not reproduce the same address and generation")
	}
	if _, err := j.Prepare(lease, after); !errors.Is(err, ErrRoutingLease) {
		t.Fatal("approval laundered through another observer", err)
	}
	fresh, _, _ := routingClaim(t, after, routingSettings())
	if _, err := j.Prepare(fresh, after); err != nil {
		t.Fatal(err)
	}
}

func TestRoutingJournalRestorationConflictsRetainOwnership(t *testing.T) {
	j, path := routeJournal(t)
	o, _ := persistentObserver(t)
	b, l, _, managed := journalLease(t, o, "radio1", routingSettings())
	token, err := j.Prepare(l, b)
	if err != nil {
		t.Fatal(err)
	}
	before := fileBytes(t, filepath.Join(path, routingJournalName))
	for name, mutate := range routingMutations() {
		t.Run(name, func(t *testing.T) {
			view := cloneRouting(managed)
			mutate(&view)
			if _, err := j.PrepareRestore(token, view); !errors.Is(err, ErrRoutingConflict) {
				t.Fatal("ambiguous recovery accepted", err)
			}
			if !bytes.Equal(before, fileBytes(t, filepath.Join(path, routingJournalName))) {
				t.Fatal("conflict changed recovery evidence")
			}
		})
	}
	if _, err := j.PrepareRestore(token, managed); err != nil {
		t.Fatal(err)
	}
	if err := j.CompleteRestore(token, managed); !errors.Is(err, ErrRoutingConflict) {
		t.Fatal("managed settings reported as restored", err)
	}
	if err := j.MarkInstalled(token, managed); !errors.Is(err, ErrRoutingJournalPhase) {
		t.Fatal("late installation reversed restoration intent", err)
	}
}

func TestRoutingJournalReopenRequiresRecoveryAndRejectsOldCallbacks(t *testing.T) {
	j, path := routeJournal(t)
	o, _ := persistentObserver(t)
	b, l, _, managed := journalLease(t, o, "radio1", routingSettings())
	old, err := j.Prepare(l, b)
	if err != nil {
		t.Fatal(err)
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	j = reopenJournal(t, path)
	token, s, exists, err := j.Lookup("radio1")
	if err != nil || !exists || !s.RecoveryRequired || s.Phase != RoutingPrepared {
		t.Fatal("reopened ownership treated as active", err)
	}
	if err := j.PermitInstall(token); !errors.Is(err, ErrRoutingJournalRecovery) {
		t.Fatal("old endpoint permitted after reopen", err)
	}
	if err := j.MarkInstalled(token, managed); !errors.Is(err, ErrRoutingJournalRecovery) {
		t.Fatal("recovery bypassed", err)
	}
	if _, err := j.PrepareRestore(old, managed); !errors.Is(err, ErrRoutingJournalToken) {
		t.Fatal("old-owner callback accepted", err)
	}
	if _, err := j.PrepareRestore(token, routingSettings()); err != nil {
		t.Fatal(err)
	}
	if err := j.CompleteRestore(token, routingSettings()); err != nil {
		t.Fatal(err)
	}
	b, l, _, managed = journalLease(t, o, "radio1", routingSettings())
	newToken, err := j.Prepare(l, b)
	if err != nil {
		t.Fatal(err)
	}
	if newToken.transaction == token.transaction {
		t.Fatal("transaction identity reused")
	}
	if err := j.MarkInstalled(token, managed); !errors.Is(err, ErrRoutingJournalToken) {
		t.Fatal("old transaction adopted replacement", err)
	}
}

func TestRoutingJournalSnapshotsDeepCopyAndShowRetiredBindings(t *testing.T) {
	j, _ := routeJournal(t)
	o, _ := persistentObserver(t)
	for _, radio := range []string{"radio2", "radio1"} {
		b, l, _, _ := journalLease(t, o, radio, routingSettings())
		if _, err := j.Prepare(l, b); err != nil {
			t.Fatal(err)
		}
	}
	s, err := j.Snapshots()
	if err != nil {
		t.Fatal(err)
	}
	if len(s) != 2 || s[0].Radio != "radio1" || s[1].Radio != "radio2" || s[0].RecoveryRequired {
		t.Fatal("bad ordered snapshot")
	}
	s[0].Original.Saved["Other"] = UDPSettings{}
	_, again, _, err := j.Lookup("radio1")
	if err != nil {
		t.Fatal(err)
	}
	if reflect.DeepEqual(s[0].Original, again.Original) {
		t.Fatal("snapshot mutated journal")
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	_, again, _, err = j.Lookup("radio1")
	if err != nil || !again.RecoveryRequired {
		t.Fatal("retired listener shown as current", err)
	}
}

func TestRoutingJournalUncertainCommitsRecoverAtEveryPhase(t *testing.T) {
	for _, step := range []string{"prepare", "install", "restore", "complete"} {
		for _, committed := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s-%t", step, committed), func(t *testing.T) {
				j, jpath := routeJournal(t)
				o, _ := persistentObserver(t)
				h, path := settingsFile(t)
				d, err := h.Read()
				if err != nil {
					t.Fatal(err)
				}
				b, l, p, managed := journalLease(t, o, "radio1", d.Routing())
				failure := errors.New("injected uncertain journal write")
				inject := func() {
					j.write = func(data []byte) error {
						if committed {
							if err := j.state.replace(routingJournalName, routingJournalPending, data); err != nil {
								return err
							}
						}
						return failure
					}
				}
				if step == "prepare" {
					inject()
				}
				token, err := j.Prepare(l, b)
				if step != "prepare" {
					if err != nil {
						t.Fatal(err)
					}
					if err := h.Apply(p, func() error { return j.PermitInstall(token) }); err != nil {
						t.Fatal(err)
					}
					if step == "install" {
						inject()
					}
					err = j.MarkInstalled(token, managed)
					if step != "install" {
						if err != nil {
							t.Fatal(err)
						}
						if step == "restore" {
							inject()
						}
						var restore RoutingPatch
						restore, err = j.PrepareRestore(token, managed)
						if step == "complete" {
							if err != nil {
								t.Fatal(err)
							}
							if err := h.Apply(restore, func() error { return j.PermitRestore(token) }); err != nil {
								t.Fatal(err)
							}
							inject()
							err = j.CompleteRestore(token, d.Routing())
						}
					}
				}
				if !errors.Is(err, ErrRoutingJournalUncertain) || !errors.Is(err, failure) {
					t.Fatal("uncertain storage outcome hidden", err)
				}
				if _, err := j.Snapshots(); !errors.Is(err, ErrRoutingJournalUncertain) {
					t.Fatal("poisoned owner reported a definite state", err)
				}
				if err := j.Close(); err != nil {
					t.Fatal(err)
				}
				j = reopenJournal(t, jpath)
				journalRecovery(t, j, h, "radio1")
				if string(fileBytes(t, path)) != settingsExample {
					t.Fatal("uncertain phase recovery lost original settings")
				}
			})
		}
	}
}

func TestRoutingJournalDetectsExternalChangesAndRefusesReset(t *testing.T) {
	for _, kind := range []string{"missing", "changed", "lock"} {
		t.Run(kind, func(t *testing.T) {
			j, path := routeJournal(t)
			switch kind {
			case "missing":
				if err := os.Remove(filepath.Join(path, routingJournalName)); err != nil {
					t.Fatal(err)
				}
			case "changed":
				d := cloneJournal(j.data)
				d.Revision++
				raw, err := encodeRoutingJournal(d)
				if err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(filepath.Join(path, routingJournalName), raw, 0600); err != nil {
					t.Fatal(err)
				}
			case "lock":
				lock := filepath.Join(path, historyLock)
				if err := os.Rename(lock, lock+".old"); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(lock, nil, 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := j.Snapshots(); !errors.Is(err, ErrRoutingJournal) {
				t.Fatal("external change accepted", err)
			}
			if err := CreateRoutingJournal(path); !errors.Is(err, ErrRoutingJournal) {
				t.Fatal("damaged journal reset", err)
			}
		})
	}
}

func TestRoutingJournalRejectsCorruptAndNoncanonicalRecords(t *testing.T) {
	j, path := routeJournal(t)
	o, _ := persistentObserver(t)
	b, l, _, _ := journalLease(t, o, "radio1", routingSettings())
	if _, err := j.Prepare(l, b); err != nil {
		t.Fatal(err)
	}
	raw := fileBytes(t, filepath.Join(path, routingJournalName))
	mutations := map[string]func([]byte) []byte{
		"truncated": func(b []byte) []byte { return b[:len(b)-5] },
		"checksum": func(b []byte) []byte {
			return bytes.Replace(b, []byte("private-destination"), []byte("changed-destination"), 1)
		},
		"extra-json":  func(b []byte) []byte { return append(b, []byte("{}")...) },
		"extra-field": func(b []byte) []byte { return bytes.Replace(b, []byte(`"data":`), []byte(`"extra":true,"data":`), 1) },
		"duplicate-field": func(b []byte) []byte {
			return bytes.Replace(b, []byte(`"version":1`), []byte(`"version":0,"version":1`), 1)
		},
		"case-alias": func(b []byte) []byte { return bytes.Replace(b, []byte(`"version":`), []byte(`"VERSION":`), 1) },
		"whitespace": func(b []byte) []byte { return append([]byte(" "), b...) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeRoutingJournal(mutate(bytes.Clone(raw))); !errors.Is(err, ErrRoutingJournalLayout) {
				t.Fatal("bad journal accepted", err)
			}
		})
	}
	for _, change := range []string{"version", "phase", "port", "host", "generation", "radio", "nil-profiles"} {
		t.Run(change, func(t *testing.T) {
			d := cloneJournal(j.data)
			r := d.Records["radio1"]
			switch change {
			case "version":
				d.Version = 2
			case "phase":
				r.Phase = "released"
			case "port":
				r.Managed.Port.Value = "0"
			case "host":
				r.Managed.Server.Value = "192.0.2.1"
			case "generation":
				r.Generation = 0
			case "radio":
				delete(d.Records, "radio1")
				d.Records["../radio"] = r
			case "nil-profiles":
				r.Original.Saved = nil
			}
			if change != "radio" {
				d.Records["radio1"] = r
			}
			encoded, err := encodeRoutingJournal(d)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := decodeRoutingJournal(encoded); !errors.Is(err, ErrRoutingJournalLayout) {
				t.Fatal("invalid signed schema accepted", err)
			}
		})
	}
}

func TestRoutingJournalByteAndRevisionLimitsPreserveState(t *testing.T) {
	j, path := routeJournal(t)
	o, _ := persistentObserver(t)
	view := routingSettings()
	view.Current.Server.Value = strings.Repeat("<", MaxRoutingValue)
	view.Saved = make(map[string]UDPSettings)
	for i := 0; i < MaxSavedProfiles; i++ {
		view.Saved[fmt.Sprint(i)] = view.Current
	}
	b, l, _, _ := journalLease(t, o, "radio1", view)
	if _, err := j.Prepare(l, b); err != nil {
		t.Fatal(err)
	}
	before := fileBytes(t, filepath.Join(path, routingJournalName))
	b, l, _, _ = journalLease(t, o, "radio2", view)
	if _, err := j.Prepare(l, b); !errors.Is(err, ErrRoutingJournalLimit) {
		t.Fatal("journal byte bound not enforced", err)
	}
	if !bytes.Equal(before, fileBytes(t, filepath.Join(path, routingJournalName))) {
		t.Fatal("capacity failure changed state")
	}
	if err := j.Close(); err != nil {
		t.Fatal(err)
	}
	d, err := decodeRoutingJournal(before)
	if err != nil {
		t.Fatal(err)
	}
	d.Revision = math.MaxUint64
	raw, err := encodeRoutingJournal(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, routingJournalName), raw, 0600); err != nil {
		t.Fatal(err)
	}
	j = reopenJournal(t, path)
	token, _, _, err := j.Lookup("radio1")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.PrepareRestore(token, view); !errors.Is(err, ErrRoutingJournalLimit) {
		t.Fatal("revision wrapped", err)
	}
	if !bytes.Equal(raw, fileBytes(t, filepath.Join(path, routingJournalName))) {
		t.Fatal("revision failure changed state")
	}
}

func TestRoutingJournalConcurrentCapacityAndReleasedSlots(t *testing.T) {
	j, _ := routeJournal(t)
	o, _ := persistentObserver(t)
	bindings := make([]Binding, MaxSessions)
	leases := make([]*RoutingLease, MaxSessions)
	for i := 0; i < MaxSessions; i++ {
		bindings[i], leases[i], _, _ = journalLease(t, o, fmt.Sprintf("r%d", i), routingSettings())
	}
	var wg sync.WaitGroup
	for i := range bindings {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if _, err := j.Prepare(leases[i], bindings[i]); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	s, err := j.Snapshots()
	if err != nil || len(s) != MaxSessions {
		t.Fatal("concurrent records lost", err)
	}
	extra, _ := persistentObserver(t)
	b, l, _, _ := journalLease(t, extra, "extra", routingSettings())
	if _, err := j.Prepare(l, b); !errors.Is(err, ErrRoutingJournalLimit) {
		t.Fatal("record capacity exceeded", err)
	}
	token, _, _, err := j.Lookup("r0")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := j.PrepareRestore(token, routingSettings()); err != nil {
		t.Fatal(err)
	}
	if err := j.CompleteRestore(token, routingSettings()); err != nil {
		t.Fatal(err)
	}
	if _, err := j.Prepare(l, b); err != nil {
		t.Fatal("released slot was not reusable", err)
	}
}

func TestRoutingJournalRefusesMissingOrUnsafeFilesOnOpen(t *testing.T) {
	for _, kind := range []string{"missing", "corrupt", "public", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			j, path := routeJournal(t)
			if err := j.Close(); err != nil {
				t.Fatal(err)
			}
			file := filepath.Join(path, routingJournalName)
			switch kind {
			case "missing":
				if err := os.Remove(file); err != nil {
					t.Fatal(err)
				}
			case "corrupt":
				if err := os.WriteFile(file, []byte("invalid"), 0600); err != nil {
					t.Fatal(err)
				}
			case "public":
				if err := os.Chmod(file, 0644); err != nil {
					t.Fatal(err)
				}
			case "symlink":
				if err := os.Rename(file, file+".real"); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(file+".real", file); err != nil {
					t.Fatal(err)
				}
			}
			if other, err := OpenRoutingJournal(path); !errors.Is(err, ErrRoutingJournal) {
				if other != nil {
					_ = other.Close()
				}
				t.Fatal("unsafe journal opened", err)
			}
			if err := CreateRoutingJournal(path); !errors.Is(err, ErrRoutingJournal) {
				t.Fatal("unsafe journal reset automatically", err)
			}
		})
	}
}

func TestRoutingJournalUncleanExitAtEachTransactionBoundary(t *testing.T) {
	for _, boundary := range []string{"prepared", "written", "installed", "restoring", "restored", "complete"} {
		t.Run(boundary, func(t *testing.T) {
			base := t.TempDir()
			jpath, hpath, path := filepath.Join(base, "routes"), filepath.Join(base, "history"), filepath.Join(base, "WSJT-X.ini")
			if err := CreateRoutingJournal(jpath); err != nil {
				t.Fatal(err)
			}
			if err := CreateEndpointHistory(hpath); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(path, []byte(settingsExample), 0600); err != nil {
				t.Fatal(err)
			}
			cmd := exec.Command(os.Args[0], "-test.run=^TestRoutingJournalExitHelper$")
			cmd.Env = append(os.Environ(), "DXB_JOURNAL_EXIT_BASE="+base, "DXB_JOURNAL_EXIT_BOUNDARY="+boundary)
			if data, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("child: %v %s", err, data)
			}
			j := reopenJournal(t, jpath)
			h, err := OpenSettingsFile(path)
			if err != nil {
				t.Fatal(err)
			}
			defer h.Close()
			o := reopenObserver(t, hpath)
			portText := fileBytes(t, filepath.Join(base, "port"))
			port, err := strconv.ParseUint(string(portText), 10, 16)
			if err != nil {
				t.Fatal(err)
			}
			if o.used[port/64]&(uint64(1)<<(port%64)) == 0 {
				t.Fatal("unclean exit forgot endpoint")
			}
			_, snapshot, exists, err := j.Lookup("radio1")
			if err != nil {
				t.Fatal(err)
			}
			if boundary == "complete" {
				if exists {
					t.Fatal("completed journal resurrected record")
				}
			} else if !exists || !snapshot.RecoveryRequired {
				t.Fatal("unclean ownership treated as active")
			}
			journalRecovery(t, j, h, "radio1")
			if string(fileBytes(t, path)) != settingsExample {
				t.Fatal("unclean recovery did not restore original")
			}
		})
	}
}

func TestRoutingJournalExitHelper(t *testing.T) {
	base := os.Getenv("DXB_JOURNAL_EXIT_BASE")
	if base == "" {
		return
	}
	boundary := os.Getenv("DXB_JOURNAL_EXIT_BOUNDARY")
	j, err := OpenRoutingJournal(filepath.Join(base, "routes"))
	if err != nil {
		t.Fatal(err)
	}
	o, err := NewPersistentObserver(nil, filepath.Join(base, "history"))
	if err != nil {
		t.Fatal(err)
	}
	h, err := OpenSettingsFile(filepath.Join(base, "WSJT-X.ini"))
	if err != nil {
		t.Fatal(err)
	}
	d, err := h.Read()
	if err != nil {
		t.Fatal(err)
	}
	b, l, p, managed := journalLease(t, o, "radio1", d.Routing())
	if err := os.WriteFile(filepath.Join(base, "port"), []byte(strconv.Itoa(int(b.Address().Port()))), 0600); err != nil {
		t.Fatal(err)
	}
	exitAt := func(at string) {
		if at == boundary {
			os.Exit(0)
		}
	}
	token, err := j.Prepare(l, b)
	if err != nil {
		t.Fatal(err)
	}
	exitAt("prepared")
	if err := h.Apply(p, func() error { return j.PermitInstall(token) }); err != nil {
		t.Fatal(err)
	}
	exitAt("written")
	if err := j.MarkInstalled(token, managed); err != nil {
		t.Fatal(err)
	}
	exitAt("installed")
	restore, err := j.PrepareRestore(token, managed)
	if err != nil {
		t.Fatal(err)
	}
	exitAt("restoring")
	if err := h.Apply(restore, func() error { return j.PermitRestore(token) }); err != nil {
		t.Fatal(err)
	}
	exitAt("restored")
	if err := j.CompleteRestore(token, d.Routing()); err != nil {
		t.Fatal(err)
	}
	exitAt("complete")
	t.Fatal("unknown fixture exit boundary")
}

func FuzzRoutingJournalDecode(f *testing.F) {
	seed, _ := encodeRoutingJournal(routingJournalData{Version: 1, StoreID: "ABCDEFGHIJKLMNOPQRSTUVWXYZ", Records: map[string]routingRecord{}})
	f.Add(seed)
	populated, _ := encodeRoutingJournal(routingJournalData{Version: 1, StoreID: "ABCDEFGHIJKLMNOPQRSTUVWXYZ", Revision: 1, Records: map[string]routingRecord{
		"radio1": {Transaction: "ZYXWVUTSRQPONMLKJIHGFEDCBA", Generation: 1, Phase: RoutingPrepared, Original: routingSettings(), Managed: UDPSettings{RawSetting{true, "127.0.0.1"}, RawSetting{true, "65000"}}},
	}})
	f.Add(populated)
	f.Add([]byte(`{"data":{"version":1},"sha256":"invalid"}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		d, err := decodeRoutingJournal(raw)
		if err != nil {
			return
		}
		out, err := encodeRoutingJournal(d)
		if err != nil || !bytes.Equal(raw, out) {
			t.Fatal("accepted journal was not canonical", err)
		}
		var valid json.RawMessage
		if err := json.Unmarshal(out, &valid); err != nil {
			t.Fatal(err)
		}
	})
}
