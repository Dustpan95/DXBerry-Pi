package wsjtx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestReleaseRPCPausesAndRequiresAuthorityBeforeExactReceiptResume(t *testing.T) {
	f := programService(t)
	f.approve(t)
	_, runtime, _ := hookServer(t, f.service)
	state, err := CallProgramRelease(context.Background(), runtime, f.path, f.store, "release-block", "")
	if err != nil || !state.Paused {
		t.Fatal(state, err)
	}
	if _, err := CallProgramRelease(context.Background(), runtime, f.path, f.store, "release-resume", state.Receipt); err == nil {
		t.Fatal("missing authority resumed collection")
	}
	newer, err := CallProgramRelease(context.Background(), runtime, f.path, f.store, "release-block", "")
	if err != nil || newer.Receipt == state.Receipt {
		t.Fatal(newer, err)
	}
	if err := f.service.acquire(context.Background()); err != nil {
		t.Fatal(err)
	}
	f.service.resumeLease = func(context.Context) (func(), error) { return func() {}, nil }
	<-f.service.lock
	if _, err := CallProgramRelease(context.Background(), runtime, f.path, f.store, "release-resume", state.Receipt); err == nil {
		t.Fatal("old RPC receipt cleared new transition")
	}
	resumed, err := CallProgramRelease(context.Background(), runtime, f.path, f.store, "release-resume", newer.Receipt)
	if err != nil || resumed.Paused {
		t.Fatal(resumed, err)
	}
	status, err := CallProgramRelease(context.Background(), runtime, f.path, f.store, "release-status", "")
	if err != nil || status != resumed {
		t.Fatal(status, err)
	}
	f.p.gate.set(programStartPre, programInvocationOne)
	if err := f.service.hook(context.Background(), "start", "radio1", programInvocationOne); err != nil {
		t.Fatal(err)
	}
	if _, err := CallProgramRelease(context.Background(), runtime, f.path, f.store, "release-resume", newer.Receipt); err != nil {
		t.Fatal("completed receipt retry was not idempotent", err)
	}
}

func TestReleaseOfflinePauseCannotAuditThroughLiveInventoryOwner(t *testing.T) {
	f := programService(t)
	f.approve(t)
	state, err := CallProgramRelease(context.Background(), filepath.Join(t.TempDir(), "missing"), f.path, f.store, "release-block", "")
	if err == nil || !state.Paused {
		t.Fatal("offline audit bypassed live owner", state, err)
	}
	if err := CheckProgramAcquisition(f.path); !errors.Is(err, ErrProgramReleasePaused) {
		t.Fatal("failed offline audit forgot its pause", err)
	}
	if _, err := CallProgramRelease(context.Background(), filepath.Join(t.TempDir(), "missing"), f.path, f.store, "release-resume", state.Receipt); err == nil {
		t.Fatal("offline resume accepted")
	}
}

func TestReleaseOfflineAuditRetainsPauseAcrossControllerAndServiceRestart(t *testing.T) {
	f := programService(t)
	f.approve(t)
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	// Opening a controller for preview took ownership; close it before the
	// offline audit, exactly as a stopped daemon would release its inventory.
	state, err := CallProgramRelease(context.Background(), filepath.Join(t.TempDir(), "missing"), f.path, f.store, "release-block", "")
	if err != nil || !state.Paused {
		t.Fatal(state, err)
	}
	f.p.reopen(t)
	f.service = newProgramService(f.path, f.store, func() (*ProgramController, error) { return f.p.c, nil })
	f.p.gate.set(programStartPre, programInvocationOne)
	if err := f.service.hook(context.Background(), "start", "radio1", programInvocationOne); !errors.Is(err, ErrProgramReleasePaused) {
		t.Fatal("restart cleared release pause", err)
	}
	public, err := f.service.CollectionState(context.Background(), "radio1")
	if err != nil || !public.ReleasePaused {
		t.Fatal(public, err)
	}
	raw, _ := json.Marshal(public)
	if strings.Contains(string(raw), state.Receipt) || strings.Contains(string(raw), "receipt") {
		t.Fatal("public observation exposed root receipt")
	}
}

func TestReleaseAuditSerializesAfterAnAlreadyStartingProgram(t *testing.T) {
	f := programService(t)
	f.approve(t)
	f.p.gate.set(programStartPre, programInvocationOne)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	f.p.c.eligible = func(ctx context.Context, _ string) error {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	started := make(chan error, 1)
	go func() { started <- f.service.hook(context.Background(), "start", "radio1", programInvocationOne) }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("start not entered")
	}
	blocked := make(chan error, 1)
	go func() { _, err := f.service.blockRelease(context.Background()); blocked <- err }()
	unblock()
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	if err := <-blocked; !errors.Is(err, ErrRoutingJournalRecovery) {
		t.Fatal("audit missed an in-flight acquisition", err)
	}
	f.p.pending(t, RoutingInstalled)
}

func TestReleaseGatePersistsAfterUncleanProcessExit(t *testing.T) {
	path := releaseFixture(t)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(executable, "-test.run=^TestReleaseGateChildExit$")
	cmd.Env = append(os.Environ(), "DXB_RELEASE_EXIT_CHILD=1", "DXB_RELEASE_EXIT_PATH="+path)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("release exit: %v %s", err, output)
	}
	if err := permitReleaseAcquisition(path); !errors.Is(err, ErrProgramReleasePaused) {
		t.Fatal("process exit lost committed pause", err)
	}
}

func TestReleaseGateChildExit(t *testing.T) {
	if os.Getenv("DXB_RELEASE_EXIT_CHILD") != "1" {
		return
	}
	if _, err := PauseRelease(os.Getenv("DXB_RELEASE_EXIT_PATH")); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}
