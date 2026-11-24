package wsjtx

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func releaseFixture(t *testing.T) string {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("root release state")
	}
	return filepath.Join(t.TempDir(), "release")
}

func TestReleaseGatePersistsPauseAndRetiresOldReceipts(t *testing.T) {
	path := releaseFixture(t)
	if _, exists, err := ReadReleaseState(path); err != nil || exists {
		t.Fatal(exists, err)
	}
	one, err := PauseRelease(path)
	if err != nil || !one.Paused {
		t.Fatal(one, err)
	}
	if err := permitReleaseAcquisition(path); !errors.Is(err, ErrProgramReleasePaused) {
		t.Fatal(err)
	}
	two, err := PauseRelease(path)
	if err != nil || two.Receipt == one.Receipt || two.Revision <= one.Revision {
		t.Fatal(two, err)
	}
	if err := ResumeRelease(path, one.Receipt); !errors.Is(err, ErrProgramReleaseReceipt) {
		t.Fatal("late receipt unpaused replacement", err)
	}
	if err := ResumeRelease(path, two.Receipt); err != nil {
		t.Fatal(err)
	}
	if err := ResumeRelease(path, two.Receipt); err != nil {
		t.Fatal("lost-response retry failed", err)
	}
	if err := permitReleaseAcquisition(path); err != nil {
		t.Fatal(err)
	}
	state, exists, err := ReadReleaseState(path)
	if err != nil || !exists || state.Paused || state.Revision != two.Revision+1 {
		t.Fatal(state, exists, err)
	}
}

func TestReleaseGateRefusesDamagePartialStateAndUnsafeFiles(t *testing.T) {
	for _, kind := range []string{"missing", "checksum", "duplicate", "case", "extra", "trailing", "large", "symlink", "hardlink", "permissions"} {
		t.Run(kind, func(t *testing.T) {
			path := releaseFixture(t)
			value, err := PauseRelease(path)
			if err != nil {
				t.Fatal(err)
			}
			name := filepath.Join(path, "release.v1")
			before := fileBytes(t, name)
			switch kind {
			case "missing":
				err = os.Remove(name)
			case "checksum":
				err = os.WriteFile(name, bytes.Replace(before, []byte(`"paused":true`), []byte(`"paused":false`), 1), 0600)
			case "duplicate":
				err = os.WriteFile(name, bytes.Replace(before, []byte(`"version":1`), []byte(`"version":1,"version":1`), 1), 0600)
			case "case":
				err = os.WriteFile(name, bytes.Replace(before, []byte(`"paused"`), []byte(`"Paused"`), 1), 0600)
			case "extra":
				err = os.WriteFile(name, bytes.Replace(before, []byte(`"version":1`), []byte(`"version":1,"other":0`), 1), 0600)
			case "trailing":
				err = os.WriteFile(name, append(before, []byte("{}\n")...), 0600)
			case "large":
				err = os.WriteFile(name, []byte(strings.Repeat("x", 1025)), 0600)
			case "symlink":
				err = os.Rename(name, name+".original")
				if err == nil {
					err = os.Symlink(name+".original", name)
				}
			case "hardlink":
				err = os.Link(name, name+".link")
			case "permissions":
				err = os.Chmod(name, 0644)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := ReadReleaseState(path); err == nil {
				t.Fatal("damaged state became unpaused")
			}
			if _, err := PauseRelease(path); err == nil {
				t.Fatal("pause silently repaired damaged state")
			}
			if err := ResumeRelease(path, value.Receipt); err == nil {
				t.Fatal("resume ignored damage")
			}
		})
	}
	path := releaseFixture(t)
	if err := os.Mkdir(path, 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := PauseRelease(path); err == nil {
		t.Fatal("partial state initialized silently")
	}
}

func TestReleaseGatePreparedPendingDataNeverReconstructsState(t *testing.T) {
	path := releaseFixture(t)
	value, err := PauseRelease(path)
	if err != nil {
		t.Fatal(err)
	}
	unpaused := value
	unpaused.Paused = false
	unpaused.Revision++
	if err := os.WriteFile(filepath.Join(path, "release.pending"), encodeReleaseState(unpaused), 0600); err != nil {
		t.Fatal(err)
	}
	if err := permitReleaseAcquisition(path); !errors.Is(err, ErrProgramReleasePaused) {
		t.Fatal("pending file unpaused committed state", err)
	}
	if _, err := PauseRelease(path); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseServiceBlocksAcquisitionAndKeepsRestorationAvailable(t *testing.T) {
	f := programService(t)
	preview := f.approve(t)
	f.p.gate.set(programStartPre, programInvocationOne)
	if err := f.service.hook(context.Background(), "start", "radio1", programInvocationOne); err != nil {
		t.Fatal(err)
	}
	value, err := f.service.blockRelease(context.Background())
	if !errors.Is(err, ErrRoutingJournalRecovery) || !value.Paused {
		t.Fatal("owned route passed release audit", value, err)
	}
	if !currentRoutingBinding(f.p.c.sessions["radio1"].binding) {
		t.Fatal("failed release stopped a running program's receiver")
	}
	state, err := f.service.CollectionState(context.Background(), "radio1")
	if err != nil || !state.ReleasePaused || !state.Enabled || state.Observation == nil {
		t.Fatal(state, err)
	}
	f.p.gate.set(programStopPost, programInvocationOne)
	if err := f.service.hook(context.Background(), "stopped", "radio1", programInvocationOne); err != nil {
		t.Fatal("pause blocked restoration", err)
	}
	f.p.empty(t)
	f.p.gate.set(programStartPre, programInvocationTwo)
	if err := f.service.hook(context.Background(), "start", "radio1", programInvocationTwo); !errors.Is(err, ErrProgramReleasePaused) {
		t.Fatal("new acquisition ignored pause", err)
	}
	f.p.gate.set(programStopped, "")
	if err := f.service.Enable(context.Background(), "radio1", preview.Fingerprint); !errors.Is(err, ErrProgramReleasePaused) {
		t.Fatal("consent ignored release pause", err)
	}
	f.service.resumeLease = func(context.Context) (func(), error) { return func() {}, nil } // isolated authority fixture
	if _, err := f.service.resumeRelease(context.Background(), value.Receipt); err != nil {
		t.Fatal(err)
	}
	f.p.start(t, preview.Fingerprint, programInvocationTwo)
}

func TestReleaseServiceFencesUnconfiguredStationBeforeInitialization(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root release state")
	}
	root := filepath.Join(t.TempDir(), "native")
	f := programService(t)
	s := newProgramService(root, f.store, func() (*ProgramController, error) {
		t.Fatal("unconfigured release created a controller")
		return nil, ErrProgramController
	})
	t.Cleanup(func() { _ = s.Close() })
	state, err := s.blockRelease(context.Background())
	if err != nil || !state.Paused {
		t.Fatal(state, err)
	}
	if err := CheckProgramAcquisition(root); !errors.Is(err, ErrProgramReleasePaused) {
		t.Fatal(err)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("release initialized endpoint history")
	}
	if err := s.hook(context.Background(), "start", "radio1", ""); err != nil {
		t.Fatal("pause broke uncollected legacy start", err)
	}
	s.resumeLease = func(context.Context) (func(), error) { return func() {}, nil }
	if _, err := s.resumeRelease(context.Background(), state.Receipt); err != nil {
		t.Fatal(err)
	}
}

func TestReleaseServiceLostInventoryWithRetainedConsentFailsClosed(t *testing.T) {
	f := programService(t)
	f.approve(t)
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(f.path, f.path+".retained"); err != nil {
		t.Fatal(err)
	}
	s := newProgramService(f.path, f.store, func() (*ProgramController, error) { return nil, ErrProgramController })
	defer s.Close()
	state, err := s.blockRelease(context.Background())
	if !errors.Is(err, ErrRoutingJournalRecovery) || !state.Paused {
		t.Fatal("missing inventory became safe", state, err)
	}
}

func FuzzReleaseState(f *testing.F) {
	f.Add(encodeReleaseState(ReleaseState{Version: 1, Revision: 1, Receipt: "ABCDEFGHIJKLMNOPQRSTUVWXYZ", Paused: true}))
	f.Fuzz(func(t *testing.T, raw []byte) {
		state, err := decodeReleaseState(raw)
		if err == nil && !bytes.Equal(raw, encodeReleaseState(state)) {
			t.Fatal("noncanonical release accepted")
		}
	})
}
