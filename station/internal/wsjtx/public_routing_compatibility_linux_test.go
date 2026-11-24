//go:build wsjtxcompat && wsjtxmanager && wsjtxscreen

package wsjtx

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/programstate"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/settings"
)

const routingFixtureState = "/var/lib/dxb-native-state/wsjtx"
const routingFixturePreferences = "/var/lib/dxb-native-state/preferences"
const routingFixtureRuntime = "/run/dxb-native-hooks"

func TestWSJTXPublicRoutingCompatibility(t *testing.T) {
	runWSJTXScreenFixture(t, true, false)
}

func TestWSJTXPublicCLICompatibility(t *testing.T) {
	runWSJTXScreenFixture(t, true, true)
}

// Only the guest manager invokes this process at its actual unit boundaries.
// The production client authenticates the root peer and talks to the real
// ProgramService over its private socket; no lifecycle or settings stand-in.
func TestWSJTXPublicRoutingBoundary(t *testing.T) {
	runWSJTXPublicBoundary(t, false)
}

func TestWSJTXPublicCLIBoundary(t *testing.T) {
	runWSJTXPublicBoundary(t, true)
}

func runWSJTXPublicBoundary(t *testing.T, consoleCLI bool) {
	t.Helper()
	requireWSJTXManagerGuest(t)
	phase := os.Getenv("DXB_WSJTX_MANAGER_BOUNDARY")
	action := ""
	switch phase {
	case "pre":
		action = "start"
	case "post":
		action = "stopped"
	default:
		t.Fatal("explicit real manager boundary required")
	}
	var err error
	if consoleCLI {
		err = fixtureConsoleCommand(context.Background(), "-state", filepath.Dir(routingFixtureState), "-program-run", routingFixtureRuntime,
			"-wsjtx-hook", action, "-wsjtx-radio", "fixture1", "-wsjtx-invocation", os.Getenv("INVOCATION_ID"))
	} else {
		err = CallProgramHook(context.Background(), routingFixtureRuntime, routingFixtureState,
			&settings.Store{Dir: routingFixturePreferences}, action, "fixture1", os.Getenv("INVOCATION_ID"))
	}
	result := "pass"
	if err != nil {
		result = "refused"
	}
	f, writeErr := os.OpenFile("/run/dxb-wsjtx/phase-events", os.O_WRONLY|os.O_CREATE|os.O_APPEND, 0600)
	if writeErr != nil {
		t.Fatal(writeErr)
	}
	if _, writeErr := fmt.Fprintln(f, phase+":"+result); writeErr != nil {
		t.Fatal(writeErr)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatal("actual public lifecycle hook refused:", err)
	}
}

type routingScreenFixture struct {
	service    *ProgramService
	store      *settings.Store
	bridge     *SettingsBridge
	generation *programstate.Store
	before     programstate.Generation
	active     programstate.Generation
	original   RoutingSettings
	approval   string
	server     *ProgramHookServer
	cancel     context.CancelFunc
	done       chan error
}

func fixtureConsoleCommand(ctx context.Context, args ...string) error {
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/opt/dxberry/bin/dxberryd", args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "GOMAXPROCS=2"}
	cmd.WaitDelay = 200 * time.Millisecond
	var output applicationOutput
	cmd.Stdout, cmd.Stderr = &output, &output
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("actual console CLI failed: %w: %s", err, output.text())
	}
	return nil
}

func newRoutingScreenFixture(t *testing.T, ctx context.Context, consoleCLI bool) *routingScreenFixture {
	t.Helper()
	generation, err := programstate.NewStore(filepath.Dir(routingFixtureState))
	if err != nil {
		t.Fatal("fresh native generation parent unavailable")
	}
	preferences := routingFixturePreferences
	if consoleCLI {
		preferences = filepath.Join(filepath.Dir(routingFixtureState), "radios")
		if err := fixtureConsoleCommand(ctx, "-state", filepath.Dir(routingFixtureState), "-program-release-run", "/run/dxb-cli-initialize", "-wsjtx-initialize"); err != nil {
			t.Fatal(err)
		}
		if _, err := generation.Read(ctx); err != nil {
			t.Fatal("actual console initializer did not establish generation state:", err)
		}
	} else {
		if err := generation.PrepareParent(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := generation.Advance(ctx); err != nil {
			t.Fatal(err)
		}
		if err := InitializeProgramState(routingFixtureState); err != nil {
			t.Fatal(err)
		}
	}
	store := &settings.Store{Dir: preferences}
	if err := store.Set("fixture1", settings.RadioSettings{SWRLimit: 2.5, PTTTimeoutS: 90}); err != nil {
		t.Fatal(err)
	}
	bridge, err := NewSettingsBridge()
	if err != nil {
		t.Fatal("actual application-user helper unavailable:", err)
	}
	f := &routingScreenFixture{store: store, bridge: bridge, generation: generation}
	t.Cleanup(func() { f.closeServer(t) })
	f.openServer(t, ctx)
	return f
}

func (f *routingScreenFixture) openServer(t *testing.T, ctx context.Context) {
	t.Helper()
	service, err := NewProgramService(ProgramServiceConfig{StatePath: routingFixtureState, Approvals: f.store,
		Eligible: func(ctx context.Context, radio string) error {
			// This guest has no physical radio. Only this synthetic assignment
			// is eligible; physical eligibility is explicitly outside this gate.
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if radio != "fixture1" {
				return ErrInvalidSession
			}
			return nil
		}})
	if err != nil {
		t.Fatal(err)
	}
	server, err := ListenProgramHooks(routingFixtureRuntime, service)
	if err != nil {
		_ = service.Close()
		t.Fatal(err)
	}
	serveCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	f.service, f.server, f.cancel, f.done = service, server, cancel, done
	go func() { done <- server.Serve(serveCtx) }()
	if info, err := os.Lstat(filepath.Join(routingFixtureRuntime, "wsjtx.sock")); err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
		t.Fatal("private actual root hook socket unavailable")
	}
}

func (f *routingScreenFixture) closeServer(t *testing.T) {
	t.Helper()
	if f.server == nil {
		return
	}
	f.cancel()
	_ = f.server.Close()
	if err := <-f.done; err != nil {
		t.Error("public hook server failed:", err)
	}
	if err := f.service.Close(); err != nil {
		t.Error(err)
	}
	f.server, f.service, f.cancel, f.done = nil, nil, nil, nil
}

func (f *routingScreenFixture) restartWhileRunning(t *testing.T, ctx context.Context) {
	t.Helper()
	const unit = "dxberry-screen@wsjtx-fixture1.service"
	before, err := showProgramUnit(ctx, unit)
	if err != nil {
		t.Fatal(err)
	}
	view, err := f.bridge.Inspect(ctx, "fixture1")
	if err != nil {
		t.Fatal(err)
	}
	f.closeServer(t)
	f.openServer(t, ctx)
	state, err := f.service.CollectionState(ctx, "fixture1")
	if err != nil || !state.Configured || !state.Enabled || state.Phase != RoutingInstalled || !state.RecoveryRequired || state.Observation != nil || state.Transport != nil {
		t.Fatal("reopened actual service lost its owed route or adopted an old observation:", err)
	}
	if _, err := f.service.Preview(ctx, "fixture1"); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("reopened service accepted a running routing preview:", err)
	}
	if err := f.service.Recover(ctx, "fixture1"); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("reopened service restored while the actual application was running:", err)
	}
	if err := f.service.Disable(ctx, "fixture1"); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("reopened service accepted a running disable:", err)
	}
	if restored, err := f.bridge.Inspect(ctx, "fixture1"); err != nil || restored.fingerprint() != view.fingerprint() {
		t.Fatal("daemon replacement or refused recovery changed the live managed routing")
	}
	if after, err := f.generation.Read(ctx); err != nil || !after.Same(f.active) {
		t.Fatal("daemon replacement or refused recovery advanced acquisition state")
	}
	if after, err := showProgramUnit(ctx, unit); err != nil || !bytes.Equal(before, after) {
		t.Fatal("actual application invocation changed during daemon replacement")
	}
	f.checkPreferences(t)
}

func (f *routingScreenFixture) prepare(t *testing.T, ctx context.Context, cycle int) {
	t.Helper()
	view, err := f.bridge.Inspect(ctx, "fixture1")
	if err != nil {
		t.Fatal("actual settings helper inspection failed:", err)
	}
	if cycle == 0 {
		if view.Current.Server != (RawSetting{true, `"localhost"`}) || view.Current.Port != (RawSetting{true, "02237"}) || len(view.Saved) != 2 || view.Saved["Other Profile"].Port != (RawSetting{true, "2238"}) || view.Saved["Blank"] != (UDPSettings{}) {
			t.Fatal("synthetic original routing namespace incomplete")
		}
		f.original = cloneRouting(view)
		preview, err := f.service.Preview(ctx, "fixture1")
		if err != nil || preview.Settings.fingerprint() != view.fingerprint() {
			t.Fatal("actual native preview failed:", err)
		}
		if err := f.service.Enable(ctx, "fixture1", preview.Fingerprint); err != nil {
			t.Fatal("explicit synthetic approval failed:", err)
		}
		f.approval = preview.Fingerprint
	} else if view.fingerprint() != f.original.fingerprint() {
		t.Fatal("restored route was not retained for the next session")
	}
	if after, err := f.bridge.Inspect(ctx, "fixture1"); err != nil || after.fingerprint() != f.original.fingerprint() {
		t.Fatal("preview/approval changed the stopped profile")
	}
	f.before, err = f.generation.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	f.checkPreferences(t)
}

func (f *routingScreenFixture) started(t *testing.T, ctx context.Context) (*Observer, Binding) {
	t.Helper()
	state, err := f.service.CollectionState(ctx, "fixture1")
	if err != nil || !state.Configured || !state.Enabled || state.Phase != RoutingInstalled || state.RecoveryRequired {
		t.Fatal("actual public start did not install a tracked route:", err)
	}
	var session programSession
	var observer *Observer
	var ok bool
	func() {
		ctx, finish, err := f.service.begin(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer finish()
		c, err := f.service.get()
		if err != nil {
			t.Fatal(err)
		}
		if err := c.acquire(ctx); err != nil {
			t.Fatal(err)
		}
		defer c.release()
		session, ok = c.sessions["fixture1"]
		observer = c.observer
	}()
	if !ok {
		t.Fatal("actual installed session missing")
	}
	managed, err := f.bridge.Inspect(ctx, "fixture1")
	expected := cloneRouting(f.original)
	expected.Current = UDPSettings{RawSetting{true, "127.0.0.1"}, RawSetting{true, fmt.Sprint(session.binding.Address().Port())}}
	if err != nil || managed.fingerprint() != expected.fingerprint() {
		t.Fatal("actual helper did not install only the current routing destination")
	}
	f.active, err = f.generation.Read(ctx)
	if err != nil || f.active.Same(f.before) {
		t.Fatal("actual start client/server did not invalidate the previous generation")
	}
	if err := f.service.Disable(ctx, "fixture1"); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("running actual application accepted a settings mutation:", err)
	}
	if err := CallProgramHook(ctx, routingFixtureRuntime, routingFixtureState, f.store, "stopped", "fixture1", strings.Repeat("a", 32)); err == nil {
		t.Fatal("outdated stop callback accepted while the actual session was running")
	}
	if after, err := f.bridge.Inspect(ctx, "fixture1"); err != nil || after.fingerprint() != managed.fingerprint() {
		t.Fatal("refused running operations changed the actual routing namespace")
	}
	f.checkPreferences(t)
	return observer, session.binding
}

func (f *routingScreenFixture) stopped(t *testing.T, ctx context.Context) {
	t.Helper()
	restored, err := f.bridge.Inspect(ctx, "fixture1")
	if err != nil || restored.fingerprint() != f.original.fingerprint() {
		t.Fatal("actual stop-post did not restore the complete original routing namespace:", err)
	}
	state, err := f.service.CollectionState(ctx, "fixture1")
	if err != nil || !state.Configured || !state.Enabled || state.Phase != "" || state.RecoveryRequired || state.Observation != nil || state.Transport != nil {
		t.Fatal("actual stop-post left a pending route or observation:", err)
	}
	if after, err := f.generation.Read(ctx); err != nil || !after.Same(f.active) {
		t.Fatal("restoration unexpectedly established a new acquisition generation")
	}
	f.checkPreferences(t)
	raw, err := os.ReadFile(filepath.Join(settingsAppHome, ".config", "WSJT-X - fixture1.ini"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range []string{"AcceptUDPRequests=false", "BroadcastToN1MM=true", "N1MMServer=127.0.0.2", "N1MMServerPort=23333"} {
		if !bytes.Contains(raw, []byte("\n"+line+"\n")) {
			t.Fatal("actual application/route restoration changed an independent request/logging setting")
		}
	}
}

func (f *routingScreenFixture) checkPreferences(t *testing.T) {
	t.Helper()
	value, err := f.store.ReadStrict("fixture1")
	if err != nil || value.SWRLimit != 2.5 || value.PTTTimeoutS != 90 || value.WSJTXRoutingApproval != f.approval {
		t.Fatal("actual native operation changed consent or transmit guard preferences")
	}
}
