//go:build patmanager && patcompat

package pat

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"syscall"
	"testing"
	"time"
)

// This driver runs only in the disposable QEMU guest, never on a station. It
// verifies the production reader against real manager/process/socket metadata;
// the synthetic unit does not claim full DXBerry session-hook acceptance.
func TestPatRealManagerCompatibility(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 || os.Getenv("DXB_PAT_MANAGER_FIXTURE") != "1" {
		t.Fatal("isolated Linux/amd64 root manager fixture required")
	}
	pid1, err := os.Readlink("/proc/1/exe")
	if err != nil || filepath.Base(pid1) != "systemd" {
		t.Fatal("real guest systemd PID 1 required")
	}
	marker, err := os.ReadFile("/fixture/pat-manager-guest")
	if err != nil || string(marker) != "isolated-pat-manager\n" {
		t.Fatal("guest identity unavailable")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range interfaces {
		if item.Flags&net.FlagLoopback == 0 {
			t.Fatal("only guest loopback is permitted")
		}
	}
	for _, pattern := range []string{"/dev/snd", "/dev/ttyUSB*", "/dev/ttyACM*", "/dev/gpiochip*", "/dev/bus/usb"} {
		paths, err := filepath.Glob(pattern)
		if err != nil || len(paths) != 0 {
			t.Fatalf("radio devices must not enter the fixture: pattern %s, matches %v, error %v", pattern, paths, err)
		}
	}
	if _, err := compatibilityPackage("/fixture/pat_1.0.1_linux_amd64.deb"); err != nil {
		t.Fatal(err)
	}
	uid, err := localAppsUID()
	if err != nil || uid == 0 {
		t.Fatal("fixture application identity unavailable")
	}
	root := "/var/lib/dxb-pat-manager"
	if err := os.Mkdir(root, 0700); err != nil {
		t.Fatal("fresh fixture state required:", err)
	}
	home := "/var/lib/dxb-pat-home"
	for _, dir := range []string{home, home + "/config", home + "/config/pat", home + "/data", home + "/state", home + "/cache"} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(dir, int(uid), -1); err != nil {
			t.Fatal(err)
		}
	}
	config := home + "/config/pat/config.json"
	if err := os.WriteFile(config, []byte(compatibilityConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(config, int(uid), -1); err != nil {
		t.Fatal(err)
	}
	records := root + "/radios.json"
	oneOwner := []byte(`{"version":1,"radios":{"fixture1":{"owner":"pat"}}}` + "\n")
	writeRecords := func(data []byte) {
		t.Helper()
		f, err := os.CreateTemp(root, ".records-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.Remove(f.Name())
		if _, err := f.Write(data); err != nil {
			t.Fatal(err)
		}
		if err := f.Sync(); err != nil {
			t.Fatal(err)
		}
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
		if err := os.Rename(f.Name(), records); err != nil {
			t.Fatal(err)
		}
	}
	writeRecords(oneOwner)
	const unit = "dxberry-screen@pat-fixture1.service"
	unitText := fmt.Sprintf(`[Unit]
Description=Isolated actual Pat reader fixture
[Service]
Type=simple
User=dxbapps
Group=dxbapps
Environment=HOME=%[1]s GOMAXPROCS=2 XDG_CONFIG_HOME=%[1]s/config XDG_DATA_HOME=%[1]s/data XDG_STATE_HOME=%[1]s/state XDG_CACHE_HOME=%[1]s/cache
ExecStart=/usr/bin/pat http --addr 127.0.0.1:8180
KillMode=mixed
TimeoutStopSec=5
NoNewPrivileges=yes
PrivateDevices=yes
`, home)
	if err := os.WriteFile("/etc/systemd/system/"+unit, []byte(unitText), 0644); err != nil {
		t.Fatal(err)
	}
	manager := func(ctx context.Context, args ...string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", args...)
		var output compatibilityOutput
		cmd.Stdout, cmd.Stderr = &output, &output
		if err := cmd.Run(); err != nil {
			t.Fatal("fixture manager operation failed:", err, output.text())
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	manager(ctx, "daemon-reload")
	t.Cleanup(func() {
		stop, done := context.WithTimeout(context.Background(), 8*time.Second)
		defer done()
		if err := exec.CommandContext(stop, "/usr/bin/systemctl", "stop", unit).Run(); err != nil {
			t.Error("fixture service cleanup failed:", err)
		}
	})
	r, err := NewReader(records)
	if err != nil {
		t.Fatal(err)
	}
	waitRead := func() Observation {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for time.Now().Before(deadline) {
			value, err := r.Read(ctx, "fixture1")
			if err == nil {
				if value.Radio != "fixture1" || value.Status.Connected || value.Status.Dialing || value.Status.ActiveListeners == nil || len(value.Status.ActiveListeners) != 0 || value.Status.ObservedAt.IsZero() {
					t.Fatal("actual managed status was not idle")
				}
				return value
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("actual manager reader never accepted the fixture")
		return Observation{}
	}
	manager(ctx, "start", unit)
	waitRead()
	owned, err := r.probe(ctx, "fixture1")
	if err != nil {
		t.Fatal("managed application identity unavailable")
	}
	deviceRoot := filepath.Join("/proc", owned.pid, "root/dev")
	if info, err := os.Stat(filepath.Join(deviceRoot, "null")); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		t.Fatal("managed application device view inaccessible")
	}
	for _, pattern := range []string{"snd", "ttyUSB*", "ttyACM*", "gpiochip*", "bus/usb"} {
		paths, err := filepath.Glob(filepath.Join(deviceRoot, pattern))
		if err != nil || len(paths) != 0 {
			t.Fatal("managed application must have a private device view")
		}
	}
	probe, read := r.probe, r.read
	var probes []instance
	r.probe = func(ctx context.Context, radio string) (instance, error) {
		value, err := probe(ctx, radio)
		if err == nil {
			probes = append(probes, value)
		}
		return value, err
	}
	r.read = func(ctx context.Context) (Status, error) {
		value, err := read(ctx)
		if err != nil {
			return value, err
		}
		manager(ctx, "restart", unit)
		// Type=simple can return before Pat has bound its new listener. Wait
		// within the unchanged production read deadline so both metadata
		// probes actually succeed and expose the replacement identity.
		for {
			if _, err := read(ctx); err == nil {
				return value, nil
			}
			select {
			case <-ctx.Done():
				return Status{}, ctx.Err()
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	value, err := r.Read(ctx, "fixture1")
	if err != ErrUnavailable || !reflect.DeepEqual(value, Observation{}) || len(probes) != 2 || probes[0] == probes[1] {
		t.Fatal("real replacement was not rejected between successful metadata probes")
	}
	r.probe, r.read = probe, read
	waitRead()
	probes = nil
	r.probe = func(ctx context.Context, radio string) (instance, error) {
		value, err := probe(ctx, radio)
		if err == nil {
			probes = append(probes, value)
		}
		return value, err
	}
	r.read = func(ctx context.Context) (Status, error) {
		value, err := read(ctx)
		if err == nil {
			writeRecords(oneOwner)
		}
		return value, err
	}
	value, err = r.Read(ctx, "fixture1")
	if err != ErrUnavailable || !reflect.DeepEqual(value, Observation{}) || len(probes) != 2 || probes[0] == probes[1] {
		t.Fatal("real assignment ABA was not rejected")
	}
	r.probe, r.read = probe, read
	writeRecords([]byte(`{"version":1,"radios":{"fixture1":{"owner":"pat"},"fixture2":{"owner":"pat"}}}` + "\n"))
	if value, err := r.Read(ctx, "fixture1"); err != ErrUnavailable || !reflect.DeepEqual(value, Observation{}) {
		t.Fatal("multiple real assignments supplied a current observation")
	}
	writeRecords(oneOwner)
	waitRead()
	queueFixtureMessage(t, ctx)
	messageID := readFixtureMessage(t, ctx)
	manager(ctx, "restart", unit)
	waitRead()
	if readFixtureMessage(t, ctx) != messageID {
		t.Fatal("managed restart replaced the queued message")
	}
	manager(ctx, "stop", unit)
	if value, err := r.Read(ctx, "fixture1"); err != ErrUnavailable || !reflect.DeepEqual(value, Observation{}) {
		t.Fatal("stopped actual unit supplied a current observation")
	}
	var files syscall.Statfs_t
	if err := syscall.Statfs("/sys/fs/cgroup", &files); err != nil || uint64(files.Type) != 0x63677270 {
		t.Fatal("private guest cgroup v2 required")
	}
	report := map[string]any{"kind": "pat-real-manager-reader", "result": "partial", "package_sha256": compatibilityPackageHash,
		"passed":   []string{"actual owned idle read", "real systemd/proc/cgroup/socket metadata", "restart replacement refusal", "assignment ABA refusal", "multiple owner refusal", "queued P2P message preserved across managed restart", "stopped unit refusal"},
		"untested": []string{"public DXBerry prepare/session/stopped hooks", "physical radio eligibility", "active Winlink session persistence", "ARM64 station workload", "physical radio/RF"}}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(string(raw))
}
