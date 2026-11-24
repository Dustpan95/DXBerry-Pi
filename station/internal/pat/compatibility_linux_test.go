//go:build patcompat

package pat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"debug/elf"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const compatibilityPackageSize = 4940336
const compatibilityPackageHash = "21b5c7cbf96375d10da977a014c83d5bd11b2119d72b303db75b4ff168e163c0"

// Only the application fixture explicitly reads these bounded diagnostics.
// Keep a general String formatter absent from production evidence types, and
// serialize reads with output still arriving from the synthetic application.
type compatibilityOutput struct {
	mu     sync.Mutex
	output boundedOutput
}

func (b *compatibilityOutput) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.output.Write(p)
}

func (b *compatibilityOutput) text() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.output.buffer.String()
}

// This fixture is newly created data, not a copy of station settings. Explicit
// empty backends/listeners/schedule and disabled reporting supplement Docker's
// mandatory network/device isolation. Only version and http commands are used.
const compatibilityConfig = `{
 "mycall":"N0CALL","locator":"AA00aa","secure_login_password":"",
 "auxiliary_addresses":[],"auto_download_size_limit":-1,"service_codes":[],
 "http_addr":"127.0.0.1:8180","motd":[],"connect_aliases":{},"listen":[],
 "hamlib_rigs":{},"ax25":{"engine":"agwpe","beacon":{"every":0}},
 "ax25_linux":{"port":""},"agwpe":{"addr":""},"serial-tnc":{"path":""},
 "ardop":{"addr":"","ptt_ctrl":false,"cwid_enabled":false,"beacon_interval":0},
 "pactor":{"path":""},"telnet":{"listen_addr":"","password":""},
 "varahf":{"addr":""},"varafm":{"addr":""},
 "gpsd":{"addr":"","enable_http":false,"allow_forms":false,"update_locator":false},
 "prediction":{"engine":"disabled"},"schedule":{},"version_reporting_disabled":true
}`

func compatibilityPackage(path string) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, errors.New("verified Pat package unavailable")
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != compatibilityPackageSize {
		return nil, errors.New("Pat package type or size mismatch")
	}
	raw, err := io.ReadAll(io.LimitReader(f, compatibilityPackageSize+1))
	if err != nil || len(raw) != compatibilityPackageSize {
		return nil, errors.New("Pat package read failed")
	}
	digest := sha256.Sum256(raw)
	if hex.EncodeToString(digest[:]) != compatibilityPackageHash {
		return nil, errors.New("Pat package checksum mismatch")
	}
	return raw, nil
}

func compatibilityEnvironment() error {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 0 {
		return errors.New("fixture requires Linux/amd64 Docker root driver; Pat runs unprivileged")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		return errors.New("application checks require Docker")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		return err
	}
	for _, item := range interfaces {
		if item.Flags&net.FlagLoopback == 0 {
			return errors.New("fixture requires --network none")
		}
	}
	for _, pattern := range []string{"/dev/snd", "/dev/ttyUSB*", "/dev/ttyACM*", "/dev/gpiochip*", "/dev/bus/usb"} {
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) != 0 {
			return errors.New("radio/USB/GPIO devices must not be passed to the fixture")
		}
	}
	return nil
}

func compatibilityCommand(ctx context.Context, binary, home string, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Dir = home
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C", "LC_ALL=C", "GOMAXPROCS=2", "HOME=" + home, "XDG_CONFIG_HOME=" + home + "/config", "XDG_DATA_HOME=" + home + "/data", "XDG_STATE_HOME=" + home + "/state", "XDG_CACHE_HOME=" + home + "/cache"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Credential: &syscall.Credential{Uid: 65534, Gid: 65534, Groups: []uint32{}}}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 200 * time.Millisecond
	return cmd
}

func compatibilityChildUnprivileged(pid int) bool {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", pid))
	if err != nil {
		return false
	}
	uid, caps := false, false
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "Uid:") {
			f := strings.Fields(line)
			uid = len(f) == 5 && f[1] == "65534" && f[2] == "65534" && f[3] == "65534" && f[4] == "65534"
		}
		if strings.HasPrefix(line, "CapEff:") {
			f := strings.Fields(line)
			caps = len(f) == 2 && strings.Trim(f[1], "0") == ""
		}
	}
	return uid && caps
}

func publishCompatibilityReport(path string, report any) error {
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".pat-application-*")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(append(raw, '\n')); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	// Same-directory linking publishes a complete report without replacing old
	// evidence. Failure leaves no success record for the current invocation.
	return os.Link(f.Name(), path)
}

func TestPatApplicationCompatibility(t *testing.T) {
	if err := compatibilityEnvironment(); err != nil {
		t.Fatal("application acceptance not run:", err)
	}
	packagePath, reportPath := os.Getenv("DXB_PAT_COMPAT_PACKAGE"), os.Getenv("DXB_PAT_COMPAT_REPORT")
	if packagePath == "" || reportPath == "" {
		t.Fatal("application acceptance not run: verified package and report destination are required")
	}
	if !filepath.IsAbs(reportPath) {
		t.Fatal("absolute evidence destination required")
	}
	if info, err := os.Stat(filepath.Dir(reportPath)); err != nil || !info.IsDir() {
		t.Fatal("evidence directory unavailable")
	}
	if _, err := os.Lstat(reportPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("refusing an existing or unavailable report destination")
	}
	raw, err := compatibilityPackage(packagePath)
	if err != nil {
		t.Fatal("application acceptance not run:", err)
	}
	work, err := os.MkdirTemp("/tmp", "dxberry-pat-compat-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(work)
	if err := os.Chmod(work, 0755); err != nil {
		t.Fatal(err)
	}
	archive := filepath.Join(work, "package.deb")
	if err := os.WriteFile(archive, raw, 0600); err != nil {
		t.Fatal(err)
	}
	raw = nil
	extracted := filepath.Join(work, "extracted")
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	_, err = metadataCommand(ctx, "/usr/bin/dpkg-deb", "--extract", archive, extracted)
	cancel()
	if err != nil {
		t.Fatal("verified package extraction failed:", err)
	}
	binary := filepath.Join(extracted, "usr", "bin", "pat")
	info, err := os.Lstat(binary)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0 {
		t.Fatal("package does not provide the expected ordinary Pat executable")
	}
	image, err := elf.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	validELF := image.Machine == elf.EM_X86_64 && image.Class == elf.ELFCLASS64
	image.Close()
	if !validELF {
		t.Fatal("package executable architecture mismatch")
	}
	home := filepath.Join(work, "home")
	for _, dir := range []string{home, home + "/config", home + "/data", home + "/state", home + "/cache", home + "/mailbox", home + "/forms", home + "/prehooks"} {
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(dir, 65534, 65534); err != nil {
			t.Fatal(err)
		}
	}
	config := filepath.Join(home, "config.json")
	if err := os.WriteFile(config, []byte(compatibilityConfig), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(config, 65534, 65534); err != nil {
		t.Fatal(err)
	}
	ctx, cancel = context.WithTimeout(context.Background(), 5*time.Second)
	versionCmd := compatibilityCommand(ctx, binary, home, "version")
	var versionLog compatibilityOutput
	versionCmd.Stdout, versionCmd.Stderr = &versionLog, &versionLog
	err = versionCmd.Run()
	cancel()
	version := strings.TrimSpace(versionLog.text())
	if err != nil || !strings.HasPrefix(version, "Pat v1.0.1 (") || !strings.Contains(version, ") linux/amd64 - go") {
		t.Fatal("application-reported version check failed:", err, versionLog.text())
	}
	// Refuse a pre-existing listener; this namespace contains only the driver
	// and its spawned fixture process, never a host/shared program server.
	reservation, err := net.Listen("tcp4", "127.0.0.1:8180")
	if err != nil {
		t.Fatal("fixture status port already occupied")
	}
	reservation.Close()
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	app := compatibilityCommand(ctx, binary, home, "--config", config, "--mbox", home+"/mailbox", "--forms", home+"/forms", "--prehooks", home+"/prehooks", "--log", home+"/state/pat.log", "--event-log", home+"/state/events.json", "http", "--addr", "127.0.0.1:8180")
	var appLog compatibilityOutput
	app.Stdout, app.Stderr = &appLog, &appLog
	if err := app.Start(); err != nil {
		t.Fatal("application start failed:", err)
	}
	done := make(chan error, 1)
	go func() { done <- app.Wait() }()
	stopped := false
	defer func() {
		if !stopped {
			_ = syscall.Kill(-app.Process.Pid, syscall.SIGKILL)
			<-done
		}
	}()
	client := newClient()
	var samples []Status
	deadline := time.Now().Add(10 * time.Second)
	for len(samples) == 0 && time.Now().Before(deadline) {
		select {
		case err := <-done:
			stopped = true
			t.Fatal("application exited before HTTP readiness:", err, appLog.text())
		default:
		}
		status, err := client.read(ctx)
		if err == nil {
			samples = append(samples, status)
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if len(samples) == 0 {
		t.Fatal("actual application did not produce a compatible idle status:", appLog.text())
	}
	if !compatibilityChildUnprivileged(app.Process.Pid) {
		t.Fatal("application is not running as the unprivileged fixture identity")
	}
	for i := 0; i < 2; i++ {
		status, err := client.read(ctx)
		if err != nil {
			t.Fatal("repeated actual status read failed", err)
		}
		samples = append(samples, status)
	}
	for _, status := range samples {
		if status.Connected || status.Dialing || status.ActiveListeners == nil || len(status.ActiveListeners) != 0 || status.ObservedAt.IsZero() {
			t.Fatal("fixture was not in the expected idle state", status)
		}
	}
	if err := syscall.Kill(-app.Process.Pid, syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		stopped = true
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGTERM {
				t.Fatal("unexpected application termination", err)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("application did not stop after TERM")
	}
	if status, err := client.read(ctx); err != ErrUnavailable || !reflect.DeepEqual(status, Status{}) {
		t.Fatal("stopped application supplied a current observation", status, err)
	}
	report := map[string]any{"version": 1, "kind": "pat-application-compatibility", "result": "partial", "package_sha256": compatibilityPackageHash, "application_version": version, "samples": samples,
		"passed":   []string{"package integrity", "reported version and architecture", "unprivileged execution", "actual idle HTTP status", "repeated idle reads", "TERM stop and unavailable status"},
		"untested": []string{"dialing/connected transitions", "managed reader with real systemd", "active Winlink shutdown and message persistence", "capture workload", "physical radio/RF"}}
	if err := publishCompatibilityReport(reportPath, report); err != nil {
		t.Fatal("report publication failed:", err)
	}
}

func TestPatCompatibilityPackageRejectsInvalidInput(t *testing.T) {
	root := t.TempDir()
	file := filepath.Join(root, "package.deb")
	if _, err := compatibilityPackage(file); err == nil {
		t.Fatal("missing package accepted")
	}
	if err := os.WriteFile(file, []byte("not a package"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := compatibilityPackage(file); err == nil {
		t.Fatal("wrong size accepted")
	}
	if err := os.WriteFile(file, bytes.Repeat([]byte{0}, compatibilityPackageSize), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := compatibilityPackage(file); err == nil {
		t.Fatal("wrong checksum accepted")
	}
	link := filepath.Join(root, "link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	if _, err := compatibilityPackage(link); err == nil {
		t.Fatal("symlink accepted")
	}
	fifo := filepath.Join(root, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := compatibilityPackage(fifo); err == nil {
		t.Fatal("non-regular input accepted")
	}
}

func TestPatCompatibilityReportNeverReplacesExistingEvidence(t *testing.T) {
	path := filepath.Join(t.TempDir(), "report.json")
	if err := publishCompatibilityReport(path, map[string]string{"result": "partial"}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(path)
	if err := publishCompatibilityReport(path, map[string]string{"result": "new"}); err == nil {
		t.Fatal("existing evidence overwritten")
	}
	after, _ := os.ReadFile(path)
	if !bytes.Equal(before, after) {
		t.Fatal("previous report changed")
	}
}
