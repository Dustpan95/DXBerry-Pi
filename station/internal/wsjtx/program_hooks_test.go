package wsjtx

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/settings"
)

func hookServer(t *testing.T, service *ProgramService) (*ProgramHookServer, string, <-chan struct{}) {
	t.Helper()
	base, err := os.MkdirTemp("/tmp", "dxb-hooks-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	runtime := filepath.Join(base, "run")
	server, err := ListenProgramHooks(runtime, service)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		if err := server.Serve(context.Background()); err != nil {
			t.Error(err)
		}
	}()
	t.Cleanup(func() {
		_ = server.Close()
		select {
		case <-done:
		case <-time.After(3 * time.Second):
			t.Error("program hook handlers did not exit")
		}
	})
	return server, runtime, done
}

func TestProgramHooksRealRootSocketDrivesApplicationUserRouting(t *testing.T) {
	f := programService(t)
	f.approve(t)
	_, runtime, _ := hookServer(t, f.service)
	call := func(action string) error {
		return CallProgramHook(context.Background(), runtime, f.path, f.store, action, "radio1", programInvocationOne)
	}
	f.p.gate.set(programStartPre, programInvocationOne)
	if err := call("start"); err != nil {
		t.Fatal(err)
	}
	f.p.pending(t, RoutingInstalled)
	f.p.gate.set(programStopPost, programInvocationOne)
	if err := call("stopped"); err != nil {
		t.Fatal(err)
	}
	f.p.empty(t)
	if string(fileBytes(t, f.p.f.path)) != settingsExample {
		t.Fatal("root unit callbacks did not restore app preferences")
	}
}

func TestProgramHooksUnavailableDaemonRequiresProofOfNoOwnedRoute(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root command fallback")
	}
	base := t.TempDir()
	root := filepath.Join(base, "native")
	store := &settings.Store{Dir: filepath.Join(base, "radios")}
	runtime := filepath.Join(base, "missing-run")
	call := func() error {
		return CallProgramHook(context.Background(), runtime, root, store, "start", "radio1", "")
	}
	if err := call(); err != nil {
		t.Fatal("legacy default-off startup failed", err)
	}
	if err := store.SaveRoutingApproval("radio1", "", strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := call(); err == nil {
		t.Fatal("enabled startup skipped its missing daemon")
	}
	if err := store.SaveRoutingApproval("radio1", strings.Repeat("a", 64), ""); err != nil {
		t.Fatal(err)
	}
	if err := InitializeProgramState(root); err != nil {
		t.Fatal(err)
	}
	if err := call(); err != nil {
		t.Fatal("verified empty journal blocked uncollected startup", err)
	}
	if err := os.Remove(filepath.Join(root, "routes", routingJournalName)); err != nil {
		t.Fatal(err)
	}
	if err := call(); err == nil {
		t.Fatal("missing journal became an empty inventory")
	}
	if err := os.WriteFile(filepath.Join(store.Dir, "radio1.json"), []byte(`{broken`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := call(); err == nil {
		t.Fatal("damaged approval became disabled")
	}
}

func TestProgramHooksDaemonLossKeepsPendingRouteBlockedAcrossRestart(t *testing.T) {
	f := programService(t)
	preview := f.approve(t)
	server, runtime, done := hookServer(t, f.service)
	f.p.gate.set(programStartPre, programInvocationOne)
	if err := CallProgramHook(context.Background(), runtime, f.path, f.store, "start", "radio1", programInvocationOne); err != nil {
		t.Fatal(err)
	}
	if err := f.store.SaveRoutingApproval("radio1", preview.Fingerprint, ""); err != nil {
		t.Fatal(err)
	}
	_ = server.Close()
	<-done
	if err := f.service.Close(); err != nil {
		t.Fatal(err)
	}
	if err := CallProgramHook(context.Background(), runtime, f.path, f.store, "start", "radio1", programInvocationTwo); !errors.Is(err, ErrRoutingJournalRecovery) {
		t.Fatal("pending route bypassed absent daemon", err)
	}
	if string(fileBytes(t, f.p.f.path)) == settingsExample {
		t.Fatal("daemon shutdown edited a potentially running program")
	}
	f.p.reopen(t)
	f.service = newProgramService(f.path, f.store, func() (*ProgramController, error) { return f.p.c, nil })
	f.p.gate.set(programStartPre, programInvocationTwo)
	_, runtime2, _ := hookServer(t, f.service)
	if err := CallProgramHook(context.Background(), runtime2, f.path, f.store, "start", "radio1", programInvocationTwo); err != nil {
		t.Fatal(err)
	}
	f.p.empty(t)
	if string(fileBytes(t, f.p.f.path)) != settingsExample {
		t.Fatal("uncollected restart did not recover original route")
	}
}

func TestProgramHooksRejectMalformedExtraAndUnboundedRequests(t *testing.T) {
	f := programService(t)
	_, runtime, _ := hookServer(t, f.service)
	valid := `{"version":1,"action":"start","radio":"radio1","invocation":""}` + "\n"
	for _, raw := range []string{
		strings.Replace(valid, `"start"`, `"transmit"`, 1), strings.Replace(valid, `"radio1"`, `"../radio1"`, 1),
		strings.Replace(valid, `"version":1`, `"version":1,"version":1`, 1), strings.Replace(valid, `"action"`, `"Action"`, 1),
		strings.Replace(valid, `"invocation":""`, `"invocation":"","server":"127.0.0.1"`, 1), valid + "{}\n", strings.Repeat("x", 5000) + "\n",
	} {
		conn, err := net.DialUnix("unix", nil, &net.UnixAddr{Name: filepath.Join(runtime, "wsjtx.sock"), Net: "unix"})
		if err != nil {
			t.Fatal(err)
		}
		_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
		_, _ = conn.Write([]byte(raw))
		_ = conn.CloseWrite()
		var reply programHookReply
		err = readBridgeFrame(bufio.NewReader(conn), &reply)
		_ = conn.Close()
		if err == nil && reply.OK {
			t.Fatal("invalid root request accepted")
		}
	}
	if err := CallProgramHook(context.Background(), runtime, f.path, f.store, "start", "radio1", ""); err != nil {
		t.Fatal("invalid frames broke listener", err)
	}
	f.p.empty(t)
	if f.p.settings.installs != 0 {
		t.Fatal("invalid root input changed routing")
	}
}

func TestProgramHooksRequireRootPeerEvenWhenSocketAccessIsBroadened(t *testing.T) {
	f := programService(t)
	_, runtime, _ := hookServer(t, f.service)
	for _, path := range []string{filepath.Dir(runtime), runtime} {
		if err := os.Chmod(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(runtime, "wsjtx.sock"), 0666); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(filepath.Join(f.p.f.bin, "test-helper"), "-test.run=^TestProgramHooksChildHelper$", "--", runtime)
	cmd.Env = []string{"DXB_PROGRAM_HOOK_CHILD=1", "PATH=/usr/bin:/bin"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 1000, Gid: 1000, Groups: []uint32{1000}}}
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("cross-user peer was not refused: %v %s", err, output)
	}
	if err := os.Chmod(runtime, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(runtime, "wsjtx.sock"), 0600); err != nil {
		t.Fatal(err)
	}
	f.p.empty(t)
}

func TestProgramHooksChildHelper(t *testing.T) {
	if os.Getenv("DXB_PROGRAM_HOOK_CHILD") != "1" {
		return
	}
	conn, err := net.Dial("unix", filepath.Join(os.Args[len(os.Args)-1], "wsjtx.sock"))
	if err != nil {
		os.Exit(2)
	}
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	var reply programHookReply
	err = readBridgeFrame(bufio.NewReader(conn), &reply)
	_ = conn.Close()
	if err != nil || reply.Code != "denied" || reply.OK {
		os.Exit(3)
	}
	os.Exit(0)
}

func TestProgramHooksExclusiveRuntimeOwnershipAndUnsafeSocketRefusal(t *testing.T) {
	f := programService(t)
	server, runtime, done := hookServer(t, f.service)
	path := filepath.Join(runtime, "wsjtx.sock")
	before, err := os.Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	if second, err := ListenProgramHooks(runtime, f.service); err == nil {
		_ = second.Close()
		t.Fatal("second daemon acquired the runtime")
	}
	after, err := os.Lstat(path)
	if err != nil || !os.SameFile(before, after) {
		t.Fatal("failed ownership replaced working socket", err)
	}
	_ = server.Close()
	<-done
	if err := os.WriteFile(path, []byte("retain"), 0600); err != nil {
		t.Fatal(err)
	}
	if second, err := ListenProgramHooks(runtime, f.service); err == nil {
		_ = second.Close()
		t.Fatal("regular file removed as a stale socket")
	}
	if string(fileBytes(t, path)) != "retain" {
		t.Fatal("unsafe runtime refusal changed existing file")
	}
}

func TestProgramHooksShutdownCancelsInFlightAcquisition(t *testing.T) {
	f := programService(t)
	f.approve(t)
	server, runtime, done := hookServer(t, f.service)
	entered := make(chan struct{})
	f.p.c.eligible = func(ctx context.Context, _ string) error { close(entered); <-ctx.Done(); return ctx.Err() }
	f.p.gate.set(programStartPre, programInvocationOne)
	result := make(chan error, 1)
	go func() {
		result <- CallProgramHook(context.Background(), runtime, f.path, f.store, "start", "radio1", programInvocationOne)
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("hook did not enter acquisition")
	}
	_ = server.Close()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("shutdown left an in-flight hook")
	}
	if err := <-result; err == nil {
		t.Fatal("cancelled hook returned success")
	}
	f.p.empty(t)
}
