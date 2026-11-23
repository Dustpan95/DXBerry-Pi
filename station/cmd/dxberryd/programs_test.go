package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/wsjtx"
)

func programEligibilityFixture(t *testing.T) (string, string) {
	t.Helper()
	root := t.TempDir()
	records := filepath.Join(root, "radios.json")
	if err := os.WriteFile(records, []byte(`{"radios":{"radio1":{"owner":"wsjtx"}}}`), 0600); err != nil {
		t.Fatal(err)
	}
	return records, root
}

func TestProgramReleaseModesKeepCompatibleBundlesAndRetainIncompatiblePause(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root release command")
	}
	bundle := t.TempDir()
	for _, dir := range []string{"bin", "lib", "share"} {
		if err := os.Mkdir(filepath.Join(bundle, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"bin/dxberryd", "bin/dxberry-app", "bin/dxberry-wsjtx-settings", "lib/wsjtx.sh"} {
		if err := os.WriteFile(filepath.Join(bundle, name), []byte("inert fixture"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	marker := filepath.Join(bundle, "share/wsjtx-native-protocol")
	if err := os.WriteFile(marker, []byte("1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "station")
	runtime := filepath.Join(t.TempDir(), "missing")
	if _, err := runProgramReleaseMode(context.Background(), state, runtime, "check", bundle, ""); err != nil {
		t.Fatal("compatible bundle required downtime", err)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("compatible check created paused inventory")
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	paused, err := runProgramReleaseMode(context.Background(), state, runtime, "check", bundle, "")
	if err != nil || !paused.Paused {
		t.Fatal(paused, err)
	}
	if _, err := os.Stat(filepath.Join(state, "wsjtx")); !os.IsNotExist(err) {
		t.Fatal("release preflight initialized collection")
	}
	if err := runProgramMode(context.Background(), state, runtime, filepath.Dir(runtime), "", "", "", true); !errors.Is(err, wsjtx.ErrProgramReleasePaused) {
		t.Fatal("paused release allowed initialization", err)
	}
	value, err := runProgramReleaseMode(context.Background(), state, runtime, "status", "", "")
	if err != nil || value != paused {
		t.Fatal(value, err)
	}
	if _, err := runProgramReleaseMode(context.Background(), state, runtime, "resume", "", paused.Receipt); err == nil {
		t.Fatal("offline release resume accepted")
	}
}

func TestProgramInitializationUsesConfiguredReleaseDirectoryIndependentlyOfSocket(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root release coordination")
	}
	releaseRun := t.TempDir()
	runtime := filepath.Join(t.TempDir(), "elsewhere")
	f, err := os.OpenFile(filepath.Join(releaseRun, "native-release.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	state := filepath.Join(t.TempDir(), "station")
	if err := runProgramMode(context.Background(), state, runtime, releaseRun, "", "", "", true); !errors.Is(err, wsjtx.ErrProgramReleasePaused) {
		t.Fatal("socket override bypassed release lock", err)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("blocked initialization changed station state")
	}
}

func TestProgramEligibilityUsesFreshAssignmentAndAuthoritativePresence(t *testing.T) {
	records, bin := programEligibilityFixture(t)
	app := filepath.Join(bin, "dxberry-app")
	if err := os.WriteFile(app, []byte("#!/bin/sh\n[ \"$1\" = verify-present ] && [ \"$2\" = wsjtx-radio1 ] && [ -z \"${PARENT_PROGRAM_SECRET:-}\" ]\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PARENT_PROGRAM_SECRET", "must not be inherited")
	check := programEligibility(records, bin)
	if err := check(context.Background(), "radio1"); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"radios":{"radio1":{"owner":"station"}}}`, `{"radios":{"radio1":{"Owner":"wsjtx"}}}`, `{"RADIOS":{"radio1":{"owner":"wsjtx"}}}`, `{broken`, strings.Repeat(" ", 2*1024*1024+1)} {
		if err := os.WriteFile(records, []byte(raw), 0600); err != nil {
			t.Fatal(err)
		}
		if err := check(context.Background(), "radio1"); !errors.Is(err, wsjtx.ErrInvalidSession) {
			t.Fatal("invalid fresh assignment accepted", err)
		}
	}
}

func TestProgramEligibilityRejectsUnpluggedOrChangedRecordDuringPresenceCheck(t *testing.T) {
	records, bin := programEligibilityFixture(t)
	app := filepath.Join(bin, "dxberry-app")
	if err := os.WriteFile(app, []byte("#!/bin/sh\nexit 4\n"), 0755); err != nil {
		t.Fatal(err)
	}
	check := programEligibility(records, bin)
	if err := check(context.Background(), "radio1"); !errors.Is(err, wsjtx.ErrInvalidSession) {
		t.Fatal("absent radio accepted", err)
	}
	if err := os.WriteFile(app, []byte("#!/bin/sh\nprintf '%s\\n' '{\"radios\":{\"radio1\":{\"owner\":\"station\"}}}' > \"$DXB_RADIOS_FILE\"\n"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := check(context.Background(), "radio1"); !errors.Is(err, wsjtx.ErrInvalidSession) {
		t.Fatal("ownership change during presence check accepted", err)
	}
}

func TestProgramEligibilityHonorsEarlierDeadline(t *testing.T) {
	records, bin := programEligibilityFixture(t)
	if err := os.WriteFile(filepath.Join(bin, "dxberry-app"), []byte("#!/bin/sh\nsleep 60\n"), 0755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	start := time.Now()
	if err := programEligibility(records, bin)(ctx, "radio1"); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("deadline lost", err)
	}
	if time.Since(start) > time.Second {
		t.Fatal("presence command outlived caller deadline")
	}
}

func TestProgramCommandModesKeepDefaultOffAndInitializationExplicit(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root program command")
	}
	state := filepath.Join(t.TempDir(), "station")
	runtime := filepath.Join(t.TempDir(), "run")
	if err := runProgramMode(context.Background(), state, runtime, filepath.Dir(runtime), "start", "radio1", "", false); err != nil {
		t.Fatal("legacy hook needs no controller", err)
	}
	if _, err := os.Stat(state); !os.IsNotExist(err) {
		t.Fatal("default off created station state")
	}
	if err := runProgramMode(context.Background(), state, runtime, filepath.Dir(runtime), "", "", "", true); err != nil {
		t.Fatal(err)
	}
	if err := runProgramMode(context.Background(), state, runtime, filepath.Dir(runtime), "", "", "", true); err == nil {
		t.Fatal("existing inventory reset")
	}
	if err := runProgramMode(context.Background(), state, runtime, filepath.Dir(runtime), "transmit", "radio1", "", false); err == nil {
		t.Fatal("unknown root action accepted")
	}
	if err := runProgramMode(context.Background(), state, runtime, filepath.Dir(runtime), "start", "radio1", "", true); err == nil {
		t.Fatal("ambiguous command mode accepted")
	}
	missingParent := filepath.Join(t.TempDir(), "missing", "station")
	if err := runProgramMode(context.Background(), missingParent, runtime, filepath.Dir(runtime), "", "", "", true); err == nil {
		t.Fatal("initialization fabricated uncommitted ancestors")
	}
	if _, err := os.Stat(filepath.Dir(missingParent)); !os.IsNotExist(err) {
		t.Fatal("failed initialization created an ancestor")
	}
	unsafe := t.TempDir()
	if err := os.Chmod(unsafe, 0777); err != nil {
		t.Fatal(err)
	}
	if err := runProgramMode(context.Background(), unsafe, runtime, filepath.Dir(runtime), "", "", "", true); err == nil {
		t.Fatal("writable station directory accepted")
	}
	if _, err := os.Stat(filepath.Join(unsafe, "wsjtx")); !os.IsNotExist(err) {
		t.Fatal("unsafe initialization changed state")
	}
}
