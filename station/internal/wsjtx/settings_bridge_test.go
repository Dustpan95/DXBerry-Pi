package wsjtx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type bridgeFixture struct {
	bridge                  *SettingsBridge
	home, path, bin, canary string
}

func appBridge(t *testing.T) bridgeFixture {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("cross-user bridge requires isolated root container")
	}
	base, err := os.MkdirTemp("/tmp", "wsjtx-bridge-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(base) })
	if err := os.Chmod(base, 0755); err != nil {
		t.Fatal(err)
	}
	f := bridgeFixture{home: filepath.Join(base, "apps"), bin: filepath.Join(base, "bin"), canary: filepath.Join(base, "private", "record")}
	for _, dir := range []string{f.home, filepath.Join(f.home, ".config"), f.bin, filepath.Dir(f.canary)} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{f.home, filepath.Join(f.home, ".config")} {
		if err := os.Chown(dir, 1000, 1000); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(filepath.Join(f.home, ".config"), 0770); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(f.bin, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(f.canary, []byte("private recovery fixture"), 0600); err != nil {
		t.Fatal(err)
	}
	f.path = filepath.Join(f.home, ".config", "WSJT-X - radio1.ini")
	if err := os.WriteFile(f.path, []byte(settingsExample), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(f.path, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(executable)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.bin, "test-helper"), data, 0755); err != nil {
		t.Fatal(err)
	}
	f.bridge = f.mode(t, "normal")
	return f
}

func (f bridgeFixture) mode(t *testing.T, mode string) *SettingsBridge {
	t.Helper()
	quote := func(s string) string { return "'" + strings.ReplaceAll(s, "'", "'\\''") + "'" }
	helper := filepath.Join(f.bin, "helper-"+mode)
	// Each exchange launches this fixture afresh. Disable only the race
	// runtime's default one-second exit sleep, not race reporting or checks.
	script := "#!/bin/sh\nexport GORACE=atexit_sleep_ms=0\nexport DXB_WSJTX_BRIDGE_CHILD=1\nexport DXB_WSJTX_CHILD_MODE=" + quote(mode) + "\nexport DXB_WSJTX_CANARY=" + quote(f.canary) + "\nexec " + quote(filepath.Join(f.bin, "test-helper")) + " -test.run='^TestSettingsBridgeChildHelper$' -- \"$@\"\n"
	if err := os.WriteFile(helper, []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	b, err := newSettingsBridge(helper, f.home, 1000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func bridgePlan(t *testing.T, f bridgeFixture) (*RoutingJournal, JournalToken, RoutingPatch, RoutingSettings) {
	t.Helper()
	view, err := f.bridge.Inspect(context.Background(), "radio1")
	if err != nil {
		t.Fatal(err)
	}
	j, _ := routeJournal(t)
	o, _ := persistentObserver(t)
	b, l, p, _ := journalLease(t, o, "radio1", view)
	token, err := j.Prepare(l, b)
	if err != nil {
		t.Fatal(err)
	}
	return j, token, p, view
}

func bridgeRecover(t *testing.T, b *SettingsBridge, j *RoutingJournal, token JournalToken, original RoutingSettings) {
	t.Helper()
	view, err := b.Inspect(context.Background(), "radio1")
	if err != nil {
		t.Fatal(err)
	}
	p, err := j.PrepareRestore(token, view)
	if err != nil {
		t.Fatal(err)
	}
	restored, err := b.Restore(context.Background(), p, func(context.Context) error { return j.PermitRestore(token) })
	if err != nil {
		t.Fatal(err)
	}
	if restored.fingerprint() != original.fingerprint() {
		t.Fatal("bridge restored another namespace")
	}
	if err := j.CompleteRestore(token, restored); err != nil {
		t.Fatal(err)
	}
}

func TestSettingsBridgeCrossUserJournalTransaction(t *testing.T) {
	f := appBridge(t)
	t.Setenv("DXB_BRIDGE_PARENT_SECRET", "not inherited")
	if h, err := OpenSettingsFile(f.path); !errors.Is(err, ErrSettingsFile) {
		if h != nil {
			_ = h.Close()
		}
		t.Fatal("root bypassed application ownership", err)
	}
	j, token, p, original := bridgePlan(t, f)
	calls := 0
	managed, err := f.bridge.Install(context.Background(), p, func(ctx context.Context) error {
		calls++
		if err := ctx.Err(); err != nil {
			return err
		}
		return j.PermitInstall(token)
	})
	if err != nil || calls != 2 || managed.Current != p.Replacement() {
		t.Fatal("cross-user installation failed", err, calls)
	}
	if err := j.MarkInstalled(token, managed); err != nil {
		t.Fatal(err)
	}
	bridgeRecover(t, f.bridge, j, token, original)
	if string(fileBytes(t, f.path)) != settingsExample {
		t.Fatal("bridge lost original bytes")
	}
	info, err := os.Stat(f.path)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0640 {
		t.Fatal("bridge changed settings mode")
	}
}

func TestSettingsBridgeStandaloneHelperBinary(t *testing.T) {
	path := os.Getenv("DXB_SETTINGS_HELPER_BINARY")
	if path == "" {
		t.Skip("real helper supplied by Docker integration build")
	}
	f := appBridge(t)
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	helper := filepath.Join(f.bin, "standalone-helper")
	if err := os.WriteFile(helper, data, 0755); err != nil {
		t.Fatal(err)
	}
	b, err := newSettingsBridge(helper, f.home, 1000, 1000)
	if err != nil {
		t.Fatal(err)
	}
	f.bridge = b
	j, token, p, original := bridgePlan(t, f)
	managed, err := b.Install(context.Background(), p, func(context.Context) error { return j.PermitInstall(token) })
	if err != nil {
		t.Fatal(err)
	}
	if err := j.MarkInstalled(token, managed); err != nil {
		t.Fatal(err)
	}
	bridgeRecover(t, b, j, token, original)
	if string(fileBytes(t, f.path)) != settingsExample {
		t.Fatal("standalone binary changed original preferences")
	}
}

func TestSettingsBridgeIdempotentRestoreStillChecksBothBoundaries(t *testing.T) {
	f := appBridge(t)
	j, token, _, original := bridgePlan(t, f)
	p, err := j.PrepareRestore(token, original)
	if err != nil {
		t.Fatal(err)
	}
	calls := 0
	view, err := f.bridge.Restore(context.Background(), p, func(context.Context) error { calls++; return j.PermitRestore(token) })
	if err != nil || calls != 2 || view.fingerprint() != original.fingerprint() {
		t.Fatal("idempotent bridge restore failed", err, calls)
	}
	if string(fileBytes(t, f.path)) != settingsExample {
		t.Fatal("idempotent restore changed file")
	}
}

func TestSettingsBridgeDeniedChecksPreserveFileAndJournal(t *testing.T) {
	for _, deny := range []int{1, 2} {
		t.Run(fmt.Sprint(deny), func(t *testing.T) {
			f := appBridge(t)
			j, token, p, original := bridgePlan(t, f)
			calls := 0
			denied := errors.New("fixture lifecycle lost")
			_, err := f.bridge.Install(context.Background(), p, func(context.Context) error {
				calls++
				if calls == deny {
					return denied
				}
				return j.PermitInstall(token)
			})
			if !errors.Is(err, ErrSettingsBridgeUncertain) || !errors.Is(err, denied) {
				t.Fatal("denied check hidden", err)
			}
			if string(fileBytes(t, f.path)) != settingsExample {
				t.Fatal("denied check committed settings")
			}
			_, _, exists, err := j.Lookup("radio1")
			if err != nil || !exists {
				t.Fatal("failure discarded recovery record", err)
			}
			bridgeRecover(t, f.bridge, j, token, original)
		})
	}
}

func TestSettingsBridgeFailedOrExtraOutputRequiresRecovery(t *testing.T) {
	for _, mode := range []string{"exit", "oversized", "extra", "wrong-result"} {
		t.Run(mode, func(t *testing.T) {
			f := appBridge(t)
			j, token, p, original := bridgePlan(t, f)
			b := f.mode(t, mode)
			_, err := b.Install(context.Background(), p, func(context.Context) error { return j.PermitInstall(token) })
			if !errors.Is(err, ErrSettingsBridgeUncertain) {
				t.Fatal("failed helper reported success", err)
			}
			_, _, exists, err := j.Lookup("radio1")
			if err != nil || !exists {
				t.Fatal("failed helper lost ownership", err)
			}
			bridgeRecover(t, f.bridge, j, token, original)
		})
	}
}

func TestSettingsBridgeCancellationAfterWriteRetainsRecovery(t *testing.T) {
	f := appBridge(t)
	j, token, p, original := bridgePlan(t, f)
	b := f.mode(t, "hang-after")
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	var resultErr error
	go func() {
		defer close(done)
		_, resultErr = b.Install(ctx, p, func(context.Context) error { return j.PermitInstall(token) })
	}()
	defer func() { cancel(); <-done }()
	eventually(t, func() bool {
		data, err := os.ReadFile(f.path)
		return err == nil && bytes.Contains(data, []byte("UDPServerPort="+p.Replacement().Port.Value+"\n"))
	})
	cancel()
	<-done
	if !errors.Is(resultErr, ErrSettingsBridgeUncertain) || !errors.Is(resultErr, context.Canceled) {
		t.Fatal("post-write cancellation was not honored", resultErr)
	}
	_, _, exists, err := j.Lookup("radio1")
	if err != nil || !exists {
		t.Fatal("timeout discarded ownership", err)
	}
	bridgeRecover(t, f.bridge, j, token, original)
}

func TestSettingsBridgeEarlierDeadlineStopsBeforeCommit(t *testing.T) {
	f := appBridge(t)
	j, token, p, original := bridgePlan(t, f)
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	_, err := f.bridge.Install(ctx, p, func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() })
	if !errors.Is(err, ErrSettingsBridgeUncertain) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal("earlier caller deadline not honored", err)
	}
	if string(fileBytes(t, f.path)) != settingsExample {
		t.Fatal("deadline allowed a commit")
	}
	bridgeRecover(t, f.bridge, j, token, original)
}

func TestSettingsBridgeRefusesUnsafeIdentityAndRequests(t *testing.T) {
	if os.Geteuid() == 0 {
		if err := ServeSettingsBridge(strings.NewReader(""), io.Discard, "/unopened"); !errors.Is(err, ErrSettingsBridgeIdentity) {
			t.Fatal("helper allowed root", err)
		}
	}
	f := appBridge(t)
	if _, err := newSettingsBridge(f.bridge.helper, f.home, 0, 0); !errors.Is(err, ErrSettingsBridgeIdentity) {
		t.Fatal("root helper credentials accepted", err)
	}
	if _, err := newSettingsBridge(f.bridge.helper, f.home, 1000, 0); !errors.Is(err, ErrSettingsBridgeIdentity) {
		t.Fatal("privileged primary group accepted", err)
	}
	if err := os.Chmod(f.bridge.helper, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := f.bridge.Inspect(context.Background(), "radio1"); !errors.Is(err, ErrSettingsBridgeIdentity) {
		t.Fatal("changed helper permissions accepted", err)
	}
	for _, radio := range []string{"../radio1", "radio1/file", "radio1\n", ";id"} {
		if _, err := f.bridge.Inspect(context.Background(), radio); !errors.Is(err, ErrSettingsBridgeProtocol) {
			t.Fatal("invalid radio reached helper", err)
		}
	}
}

func TestSettingsBridgeFrameAndActionValidation(t *testing.T) {
	valid := bridgeRequest{Version: 1, Action: "inspect", Radio: "radio1"}
	var encoded bytes.Buffer
	if err := writeBridgeFrame(&encoded, valid); err != nil {
		t.Fatal(err)
	}
	for _, bad := range [][]byte{
		bytes.Replace(encoded.Bytes(), []byte(`"version":1`), []byte(`"version":0,"version":1`), 1),
		bytes.Replace(encoded.Bytes(), []byte(`"version":`), []byte(`"VERSION":`), 1),
		append([]byte(" "), encoded.Bytes()...),
		bytes.TrimSuffix(encoded.Bytes(), []byte("\n")),
		[]byte(strings.Repeat("x", MaxSettingsBridgeFrame+1) + "\n"),
	} {
		var request bridgeRequest
		if err := readBridgeFrame(bufio.NewReader(bytes.NewReader(bad)), &request); !errors.Is(err, ErrSettingsBridgeProtocol) {
			t.Fatal("noncanonical/oversized frame accepted", err)
		}
	}
	for _, r := range []bridgeRequest{
		{Version: 2, Action: "inspect", Radio: "radio1"},
		{Version: 1, Action: "command", Radio: "radio1"},
		{Version: 1, Action: "restore", Radio: "radio1"},
		{Version: 1, Action: "inspect", Radio: "radio1", Patch: &bridgePatch{}},
		{Version: 1, Action: "install", Radio: "radio1", Patch: &bridgePatch{Profile: "Default", Generation: 1, Before: strings.Repeat("a", 64), After: UDPSettings{RawSetting{true, "192.0.2.1"}, RawSetting{true, "12345"}}}},
	} {
		if _, err := bridgeRequestPatch(r); !errors.Is(err, ErrSettingsBridgeProtocol) {
			t.Fatal("unknown/unbounded action accepted", err)
		}
	}
}

type bridgeTestWriter func([]byte) (int, error)

func (w bridgeTestWriter) Write(data []byte) (int, error) { return w(data) }

func TestSettingsBridgeChildHelper(t *testing.T) {
	if os.Getenv("DXB_WSJTX_BRIDGE_CHILD") != "1" {
		return
	}
	if os.Geteuid() != 1000 || os.Getegid() != 1000 {
		t.Fatal("helper did not drop to application identity")
	}
	groups, err := os.Getgroups()
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range groups {
		if group == 0 {
			t.Fatal("helper inherited privileged group")
		}
	}
	if _, err := os.ReadFile(os.Getenv("DXB_WSJTX_CANARY")); !errors.Is(err, os.ErrPermission) {
		t.Fatal("helper could access private root recovery state", err)
	}
	if os.Getenv("DXB_BRIDGE_PARENT_SECRET") != "" {
		t.Fatal("helper inherited parent environment")
	}
	index := -1
	for i, arg := range os.Args {
		if arg == "--" {
			index = i
			break
		}
	}
	if index < 0 || len(os.Args) != index+3 || os.Args[index+1] != "--home" {
		t.Fatal("invalid fixture arguments")
	}
	mode := os.Getenv("DXB_WSJTX_CHILD_MODE")
	if mode == "exit" {
		os.Exit(0)
	}
	if mode == "oversized" {
		_, _ = os.Stdout.WriteString(strings.Repeat("x", MaxSettingsBridgeFrame+1) + "\n")
		os.Exit(0)
	}
	var output io.Writer = os.Stdout
	if mode == "wrong-result" {
		output = bridgeTestWriter(func(raw []byte) (int, error) {
			var reply bridgeResponse
			if err := json.Unmarshal(raw, &reply); err != nil {
				return 0, err
			}
			if reply.Kind == "done" {
				reply.Settings.Current.Server = RawSetting{true, "unexpected-destination"}
			}
			if err := writeBridgeFrame(os.Stdout, reply); err != nil {
				return 0, err
			}
			return len(raw), nil
		})
	}
	if err := ServeSettingsBridge(os.Stdin, output, os.Args[index+2]); err != nil {
		os.Exit(1)
	}
	if mode == "extra" {
		_, _ = os.Stdout.WriteString("{}\n")
	}
	if mode == "hang-after" {
		for {
			time.Sleep(time.Hour)
		}
	}
	os.Exit(0)
}

func FuzzSettingsBridgeFrame(f *testing.F) {
	var seed bytes.Buffer
	_ = writeBridgeFrame(&seed, bridgeRequest{Version: 1, Action: "inspect", Radio: "radio1"})
	f.Add(seed.Bytes())
	f.Add([]byte("{\"version\":1,\"version\":0}\n"))
	f.Fuzz(func(t *testing.T, raw []byte) {
		var request bridgeRequest
		if err := readBridgeFrame(bufio.NewReader(bytes.NewReader(raw)), &request); err != nil {
			return
		}
		var out bytes.Buffer
		if err := writeBridgeFrame(&out, request); err != nil {
			t.Fatal(err)
		}
		// A reader consumes one frame only; later frames are validated by the
		// protocol state machine. The accepted prefix must be canonical.
		if !bytes.HasPrefix(raw, out.Bytes()) {
			t.Fatal("accepted frame changed on encoding")
		}
		_, _ = bridgeRequestPatch(request)
	})
}
