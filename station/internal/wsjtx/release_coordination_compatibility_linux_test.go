//go:build wsjtxcompat && wsjtxmanager && wsjtxscreen

package wsjtx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

type releaseCoordinationFixture struct {
	first   ReleaseState
	history []byte
}

func fixtureRequireExit(t *testing.T, err error, expected int) {
	t.Helper()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != expected {
		t.Fatal("actual maintenance command exit differs:", err)
	}
}

func fixtureReleaseCLI(ctx context.Context, action, receipt string) (ReleaseState, error) {
	callCtx, cancel := context.WithTimeout(ctx, 35*time.Second)
	defer cancel()
	args := []string{"-state", filepath.Dir(routingFixtureState), "-program-run", routingFixtureRuntime,
		"-program-release-run", "/run/dxb-console", "-wsjtx-release", action}
	if receipt != "" {
		args = append(args, "-wsjtx-release-receipt", receipt)
	}
	command := exec.CommandContext(callCtx, "/opt/dxberry/bin/dxberryd", args...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "GOMAXPROCS=2"}
	command.WaitDelay = 200 * time.Millisecond
	var output, diagnostic applicationOutput
	command.Stdout, command.Stderr = &output, &diagnostic
	if err := command.Run(); err != nil {
		return ReleaseState{}, err
	}
	var state ReleaseState
	if action == "status" {
		if err := json.Unmarshal([]byte(output.text()), &state); err != nil {
			return state, err
		}
	} else if output.text() != "" {
		return state, ErrProgramReleaseState
	}
	return state, nil
}

func fixtureReleaseState(t *testing.T, ctx context.Context) ReleaseState {
	t.Helper()
	state, err := fixtureReleaseCLI(ctx, "status", "")
	actual, exists, readErr := ReadReleaseState(releasePath(routingFixtureState))
	if err != nil || readErr != nil || !exists || state != actual || state.Version != 1 || !journalID(state.Receipt) || state.Revision == 0 {
		t.Fatal("actual release CLI and checksum-verified durable state differ:", err, readErr)
	}
	return state
}

func fixtureReleaseGuard(ctx context.Context, mode string) *exec.Cmd {
	command := exec.CommandContext(ctx, "/bin/bash", "/fixture/release-guard.sh", mode)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "GOMAXPROCS=2"}
	command.WaitDelay = 200 * time.Millisecond
	return command
}

func (f *routingScreenFixture) pauseForRelease(t *testing.T, ctx context.Context) *releaseCoordinationFixture {
	t.Helper()
	compatible, err := ProgramBundleCompatible("/opt/dxberry")
	if err != nil || !compatible || f.console == nil {
		t.Fatal("real compatible installed console bundle required:", err)
	}
	if err := os.Mkdir("/run/dxb-incompatible", 0755); err != nil {
		t.Fatal("fresh incompatible target required:", err)
	}
	if state, err := fixtureReleaseCLI(ctx, "status", ""); err != nil || state != (ReleaseState{}) {
		t.Fatal("unexpected existing guest release transition")
	}
	before, err := showProgramUnit(ctx, "dxberry-screen@wsjtx-fixture1.service")
	if err != nil {
		t.Fatal(err)
	}
	view, err := f.bridge.Inspect(ctx, "fixture1")
	if err != nil {
		t.Fatal(err)
	}
	command := fixtureReleaseGuard(ctx, "once")
	var output applicationOutput
	command.Stdout, command.Stderr = &output, &output
	fixtureRequireExit(t, command.Run(), 6)
	first := fixtureReleaseState(t, ctx)
	if !first.Paused || !errors.Is(CheckProgramAcquisition(routingFixtureState), ErrProgramReleasePaused) {
		t.Fatal("failed actual update audit did not retain acquisition pause")
	}
	state, err := f.service.CollectionState(ctx, "fixture1")
	if err != nil || !state.ReleasePaused || !state.RecoveryRequired || state.Phase != RoutingInstalled || !state.Enabled || state.Observation != nil || state.Transport != nil {
		t.Fatal("actual public status lost the paused recovery obligation:", err)
	}
	body, err := json.Marshal(map[string]string{"fingerprint": f.approval})
	if err != nil {
		t.Fatal(err)
	}
	status, raw, _, err := f.console.request(ctx, "POST", "/api/radio/fixture1/wsjtx/enable", string(body), f.console.cookie, "same-origin")
	var refusal struct{ Code string }
	if err != nil || status != http.StatusConflict || json.Unmarshal(raw, &refusal) != nil || refusal.Code != "release-paused" {
		t.Fatal("real authenticated acquisition intent bypassed the retained release pause:", status, err)
	}
	if fixtureReleaseState(t, ctx) != first {
		t.Fatal("refused actual acquisition intent changed the release receipt")
	}
	if after, err := f.bridge.Inspect(ctx, "fixture1"); err != nil || after.fingerprint() != view.fingerprint() {
		t.Fatal("failed actual update audit changed live managed routing")
	}
	if after, err := showProgramUnit(ctx, "dxberry-screen@wsjtx-fixture1.service"); err != nil || !bytes.Equal(before, after) {
		t.Fatal("failed actual update audit changed the running application")
	}
	current, err := f.generation.Read(ctx)
	if err != nil || current.Same(f.active) {
		t.Fatal("actual blocked maintenance/acquisition did not invalidate prior evidence")
	}
	f.active = current // owed stop-post cleanup must preserve this invalidation
	f.checkPreferences(t)
	history, err := os.ReadFile(filepath.Join(routingFixtureState, "ports", historyName))
	if err != nil {
		t.Fatal(err)
	}
	return &releaseCoordinationFixture{first: first, history: history}
}

func (r *releaseCoordinationFixture) reconcile(t *testing.T, ctx context.Context, f *routingScreenFixture) {
	t.Helper()
	if fixtureReleaseState(t, ctx) != r.first {
		t.Fatal("normal owed restoration changed the retained release pause")
	}
	state, err := f.service.CollectionState(ctx, "fixture1")
	if err != nil || !state.ReleasePaused || state.Phase != "" || state.RecoveryRequired {
		t.Fatal("normal actual stop-post failed to restore through release pause:", err)
	}
	daemon, err := showProgramUnit(ctx, "dxberryd.service")
	if err != nil {
		t.Fatal(err)
	}
	// This subprocess invokes the unchanged production updater guard. Its real
	// descriptor 5 remains held after the successful guard returns. The pipe
	// only coordinates lifetime; it supplies no CLI/manager/authority response.
	guard := fixtureReleaseGuard(ctx, "hold")
	input, err := guard.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	reader, writer, err := os.Pipe()
	if err != nil {
		_ = input.Close()
		t.Fatal(err)
	}
	guard.Stdout = writer
	var diagnostic applicationOutput
	guard.Stderr = &diagnostic
	if err := guard.Start(); err != nil {
		_ = input.Close()
		_ = reader.Close()
		_ = writer.Close()
		t.Fatal(err)
	}
	_ = writer.Close()
	defer reader.Close()
	waited := false
	defer func() {
		_ = input.Close()
		if !waited {
			_ = guard.Process.Kill()
			_ = guard.Wait()
		}
	}()
	if err := reader.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	line, err := bufio.NewReader(reader).ReadSlice('\n')
	if err != nil || string(line) != "DXB_RELEASE_GUARD_READY\n" {
		t.Fatal("actual stopped updater guard did not complete while retaining its lock:", err, diagnostic.text())
	}
	second := fixtureReleaseState(t, ctx)
	if !second.Paused || second.Receipt == r.first.Receipt || second.Revision <= r.first.Revision {
		t.Fatal("new actual update audit did not retire the previous receipt")
	}
	_, err = fixtureReleaseCLI(ctx, "resume", second.Receipt)
	fixtureRequireExit(t, err, 7)
	if fixtureReleaseState(t, ctx) != second {
		t.Fatal("held actual transition lock did not preserve the release pause")
	}
	if _, err := io.WriteString(input, "finish\n"); err != nil {
		t.Fatal(err)
	}
	_ = input.Close()
	err = guard.Wait()
	waited = true
	if err != nil {
		t.Fatal("actual updater guard did not finish cleanly:", err, diagnostic.text())
	}
	_, err = fixtureReleaseCLI(ctx, "resume", r.first.Receipt)
	fixtureRequireExit(t, err, 7)
	if fixtureReleaseState(t, ctx) != second {
		t.Fatal("actual stale receipt changed the current pause after lock release")
	}
	// A real manager unit supplies active job evidence. It performs no update;
	// the test establishes actual job-state refusal, not updater execution.
	const job = "dxberry-job-update-fixture"
	start := exec.CommandContext(ctx, "/usr/bin/systemd-run", "--unit="+job, "--property=Type=exec", "/usr/bin/sleep", "60")
	start.Stdout, start.Stderr = &diagnostic, &diagnostic
	if err := start.Run(); err != nil {
		t.Fatal("real fixture maintenance job failed to start:", err)
	}
	defer func() { _, _ = fixtureManagerCommand(context.Background(), "stop", job+".service") }()
	if _, err := fixtureManagerCommand(ctx, "is-active", "--quiet", job+".service"); err != nil {
		t.Fatal("actual maintenance job is not active")
	}
	if err := os.WriteFile("/run/dxb-console/update-job.json", []byte(`{"unit":"`+job+`"}`+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	_, err = fixtureReleaseCLI(ctx, "resume", second.Receipt)
	fixtureRequireExit(t, err, 7)
	if fixtureReleaseState(t, ctx) != second {
		t.Fatal("actual active maintenance job did not preserve the release pause")
	}
	if _, err := fixtureManagerCommand(ctx, "stop", job+".service"); err != nil {
		t.Fatal(err)
	}
	if _, err := fixtureReleaseCLI(ctx, "resume", second.Receipt); err != nil {
		t.Fatal("real compatible running daemon could not reconcile after job/lock completion:", err)
	}
	resumed := fixtureReleaseState(t, ctx)
	if resumed.Paused || resumed.Receipt != second.Receipt || resumed.Revision != second.Revision+1 || CheckProgramAcquisition(routingFixtureState) != nil {
		t.Fatal("actual exact receipt did not commit reconciliation")
	}
	if _, err := fixtureReleaseCLI(ctx, "resume", second.Receipt); err != nil || fixtureReleaseState(t, ctx) != resumed {
		t.Fatal("completed actual receipt retry changed the committed revision:", err)
	}
	state, err = f.service.CollectionState(ctx, "fixture1")
	if err != nil || state.ReleasePaused || state.Phase != "" || state.RecoveryRequired || !state.Configured || !state.Enabled || state.Observation != nil || state.Transport != nil {
		t.Fatal("actual authenticated status differs after reconciliation:", err)
	}
	if after, err := os.ReadFile(filepath.Join(routingFixtureState, "ports", historyName)); err != nil || !bytes.Equal(r.history, after) {
		t.Fatal("maintenance lost or changed the latest endpoint retirement inventory")
	}
	if after, err := showProgramUnit(ctx, "dxberryd.service"); err != nil || !bytes.Equal(daemon, after) {
		t.Fatal("release reconciliation unexpectedly replaced the actual daemon")
	}
	if after, err := f.generation.Read(ctx); err != nil || after.Same(f.active) {
		t.Fatal("actual reconciliation did not invalidate earlier acquisition evidence")
	}
	f.checkPreferences(t)
}
