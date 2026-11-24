//go:build wsjtxcompat && wsjtxmanager

package wsjtx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func requireWSJTXManagerGuest(t *testing.T) {
	t.Helper()
	pid1, err := os.Readlink("/proc/1/exe")
	marker, markerErr := os.ReadFile("/fixture/wsjtx-manager-guest")
	if runtime.GOARCH != "amd64" || os.Geteuid() != 0 || os.Getenv("DXB_WSJTX_MANAGER_FIXTURE") != "1" || err != nil || filepath.Base(pid1) != "systemd" || markerErr != nil || string(marker) != "isolated-wsjtx-manager\n" {
		t.Fatal("private Linux/amd64 real-manager guest required")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range interfaces {
		if item.Flags&net.FlagLoopback == 0 {
			t.Fatal("guest loopback only required")
		}
	}
}

func requireWSJTXPrivateDevices(t *testing.T, root string) {
	t.Helper()
	if info, err := os.Stat(filepath.Join(root, "null")); err != nil || info.Mode()&os.ModeCharDevice == 0 {
		t.Fatal("fixture device view inaccessible")
	}
	for _, pattern := range []string{"snd", "ttyUSB*", "ttyACM*", "gpiochip*", "bus/usb"} {
		paths, err := filepath.Glob(filepath.Join(root, pattern))
		if err != nil || len(paths) != 0 {
			t.Fatal("private radio-free device view required")
		}
	}
}

// Invoked only by the fixture unit's real root start-pre/stop-post processes.
// INVOCATION_ID comes from the manager, never from the parent test driver.
func TestWSJTXManagerBoundary(t *testing.T) {
	requireWSJTXManagerGuest(t)
	phase := os.Getenv("DXB_WSJTX_MANAGER_BOUNDARY")
	var boundary programBoundary
	switch phase {
	case "pre":
		boundary = programStartPre
	case "post":
		boundary = programStopPost
	default:
		t.Fatal("explicit guest unit boundary required")
	}
	g, err := newSystemdProgramLifecycle()
	if err != nil {
		t.Fatal(err)
	}
	err = g.check(context.Background(), "fixture1", os.Getenv("INVOCATION_ID"), boundary)
	result := "pass"
	if err != nil {
		result = "refused"
		// Classify only an independently observed, real application-owned
		// child at the otherwise valid stop-post boundary. No identity
		// evidence is serialized into the result trace.
		if phase == "post" {
			ctx, done := context.WithTimeout(context.Background(), time.Second)
			raw, showErr := showProgramUnit(ctx, "dxberry-screen@wsjtx-fixture1.service")
			done()
			unit, parseErr := parseProgramUnit(raw)
			pidRaw, pidErr := os.ReadFile("/var/lib/dxb-wsjtx-home/runtime/held-child.pid")
			pid := strings.TrimSpace(string(pidRaw))
			if showErr == nil && parseErr == nil && unit.matches("dxberry-screen@wsjtx-fixture1.service", os.Getenv("INVOCATION_ID"), boundary) && pidErr == nil && processID(pid) {
				info, statErr := os.Stat(filepath.Join("/proc", pid))
				group, groupErr := os.ReadFile(filepath.Join("/proc", pid, "cgroup"))
				if statErr == nil && groupErr == nil && uint64(info.Sys().(*syscall.Stat_t).Uid) == g.uid && strings.Contains(string(group), "0::"+unit.cgroup+"\n") {
					result = "refused-owned-child"
				}
			}
		}
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
		t.Fatal("actual lifecycle boundary refused")
	}
}

func TestWSJTXRealManagerCompatibility(t *testing.T) {
	requireWSJTXManagerGuest(t)
	requireWSJTXPrivateDevices(t, "/dev")
	packageFile, err := os.Open("/fixture/wsjtx.deb")
	if err != nil {
		t.Fatal(err)
	}
	defer packageFile.Close()
	hash := sha256.New()
	if n, err := io.Copy(hash, io.LimitReader(packageFile, 46499355)); err != nil || n != 46499354 || hex.EncodeToString(hash.Sum(nil)) != applicationPackageHash {
		t.Fatal("official package integrity mismatch")
	}
	account, err := user.Lookup("dxbapps")
	if err != nil {
		t.Fatal(err)
	}
	uid, uidErr := strconv.ParseUint(account.Uid, 10, 32)
	gid, gidErr := strconv.ParseUint(account.Gid, 10, 32)
	if uidErr != nil || gidErr != nil || uid == 0 {
		t.Fatal("fixture application account unavailable")
	}
	const home = "/var/lib/dxb-wsjtx-home"
	for _, dir := range []string{home, home + "/config", home + "/data", home + "/cache", home + "/state", home + "/runtime"} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(dir, int(uid), int(gid)); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Mkdir("/run/dxb-wsjtx", 0700); err != nil {
		t.Fatal("fresh private fixture state required:", err)
	}
	env := []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "HOME=" + home, "QT_QPA_PLATFORM=offscreen", "GOMAXPROCS=2",
		"XDG_CONFIG_HOME=" + home + "/config", "XDG_DATA_HOME=" + home + "/data", "XDG_CACHE_HOME=" + home + "/cache",
		"XDG_STATE_HOME=" + home + "/state", "XDG_RUNTIME_DIR=" + home + "/runtime", "PULSE_SERVER=unix:" + home + "/runtime/pulse.sock"}
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Second)
	defer cancel()
	ctl := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", args...)
		var output applicationOutput
		cmd.Stdout, cmd.Stderr = &output, &output
		return cmd.Run()
	}
	pulseConfig := home + "/pulse.pa"
	if err := os.WriteFile(pulseConfig, []byte("load-module module-native-protocol-unix socket="+home+"/runtime/pulse.sock auth-anonymous=1\nload-module module-null-sink sink_name=fixture_null sink_properties=device.description=DXBerryFixture\nset-default-sink fixture_null\nset-default-source fixture_null.monitor\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(pulseConfig, int(uid), int(gid)); err != nil {
		t.Fatal(err)
	}
	const unit = "dxberry-screen@wsjtx-fixture1.service"
	unitEnvironment := "Environment=" + strings.Join(env, " ") + "\n"
	units := map[string]string{
		"dxb-wsjtx-pulse.service": "[Service]\nUser=dxbapps\nGroup=dxbapps\nPrivateDevices=yes\nNoNewPrivileges=yes\n" + unitEnvironment + "ExecStart=/usr/bin/pulseaudio --daemonize=no --exit-idle-time=-1 --disallow-exit -n --file=" + pulseConfig + "\nTimeoutStopSec=5\n",
		unit:                      "[Unit]\nStartLimitIntervalSec=60\nStartLimitBurst=3\n[Service]\nType=simple\nUser=dxbapps\nGroup=dxbapps\nUMask=0007\nPrivateDevices=yes\nNoNewPrivileges=yes\nKillMode=mixed\nTimeoutStartSec=20\nTimeoutStopSec=20\n" + unitEnvironment + "Environment=DXB_WSJTX_MANAGER_FIXTURE=1 DXB_WSJTX_RETAIN_CHILD=1\nExecStartPre=+/usr/bin/env DXB_WSJTX_MANAGER_BOUNDARY=pre /fixture/wsjtx-manager.test -test.run=^TestWSJTXManagerBoundary$\nExecStart=/fixture/run-app\nExecStopPost=+/usr/bin/env DXB_WSJTX_MANAGER_BOUNDARY=post /fixture/wsjtx-manager.test -test.run=^TestWSJTXManagerBoundary$\n",
	}
	for name, contents := range units {
		if err := os.WriteFile("/etc/systemd/system/"+name, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		stop, done := context.WithTimeout(context.Background(), 20*time.Second)
		defer done()
		_ = exec.CommandContext(stop, "/usr/bin/systemctl", "stop", unit, "dxb-wsjtx-pulse.service").Run()
	})
	if ctl("daemon-reload") != nil || ctl("start", "dxb-wsjtx-pulse.service") != nil {
		t.Fatal("fixture manager setup failed")
	}
	credential := &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{}}
	var audio struct{ Qt, Input, Output string }
	for deadline := time.Now().Add(12 * time.Second); time.Now().Before(deadline); {
		cmd := exec.CommandContext(ctx, "/usr/bin/python3", "/fixture/audio-info.py")
		cmd.Env, cmd.Dir = env, home
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: credential}
		var stdout, stderr applicationOutput
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if cmd.Run() == nil && json.Unmarshal([]byte(stdout.text()), &audio) == nil && audio.Input != "" && audio.Output != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if audio.Input == "" || audio.Output == "" {
		t.Fatal("private Qt dummy audio unavailable")
	}
	g, err := newSystemdProgramLifecycle()
	if err != nil || g.check(ctx, "fixture1", "", programStopped) != nil {
		t.Fatal("initial actual stopped boundary refused")
	}
	observer := NewObserver(nil)
	defer observer.Close()
	prepare := func() {
		t.Helper()
		binding, err := observer.Start("fixture1", "WSJT-X - fixture1")
		if err != nil {
			t.Fatal(err)
		}
		contents := fmt.Sprintf("[Configuration]\nMyCall=N0CALL\nMyGrid=AA00aa\nRig=None\nSoundInName=%q\nSoundOutName=%q\nUDPServer=127.0.0.1\nUDPServerPort=%d\nAcceptUDPRequests=false\n[Common]\nMode=FT8\n[MainWindow]\nAutoSeq=false\n", audio.Input, audio.Output, binding.Address().Port())
		path := home + "/config/WSJT-X - fixture1.ini"
		if err := os.WriteFile(path, []byte(contents), 0600); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(path, int(uid), int(gid)); err != nil {
			t.Fatal(err)
		}
	}
	waitActual := func() {
		t.Helper()
		for deadline := time.Now().Add(35 * time.Second); time.Now().Before(deadline); {
			s, ok := observer.Snapshot("fixture1")
			if ok && s.Observation.Availability == Live && s.Observation.Status != nil && s.Observation.Heartbeat != nil {
				if s.Observation.Heartbeat.Version == nil || *s.Observation.Heartbeat.Version != "3.0.2" || s.Observation.Status.TXEnabled || s.Observation.Status.Transmitting || s.Observation.Status.DECall == nil || *s.Observation.Status.DECall != "N0CALL" || s.Observation.Status.DEGrid == nil || *s.Observation.Status.DEGrid != "AA00aa" {
					t.Fatal("actual managed application status mismatch")
				}
				return
			}
			time.Sleep(100 * time.Millisecond)
		}
		t.Fatal("actual managed application observation unavailable")
	}
	prepare()
	if ctl("start", unit) != nil {
		t.Fatal("actual start-pre boundary failed")
	}
	waitActual()
	rawUnit, err := showProgramUnit(ctx, unit)
	actualUnit, parseErr := parseProgramUnit(rawUnit)
	if err != nil || parseErr != nil || !processID(actualUnit.pid) {
		t.Fatal("actual managed application identity unavailable")
	}
	if app, err := g.applicationPID(actualUnit.pid); err != nil || !app {
		t.Fatal("actual application account mismatch")
	}
	requireWSJTXPrivateDevices(t, filepath.Join("/proc", actualUnit.pid, "root/dev"))
	if g.check(ctx, "fixture1", "", programStopped) == nil {
		t.Fatal("active actual unit accepted as stopped")
	}
	_ = ctl("stop", unit) // the deliberately unsafe stop-post check must fail
	if g.check(ctx, "fixture1", "", programStopped) != nil {
		t.Fatal("manager final cleanup did not establish an actual stopped boundary")
	}
	firstPhases, err := os.ReadFile("/run/dxb-wsjtx/phase-events")
	if err != nil || string(firstPhases) != "pre:pass\npost:refused-owned-child\n" {
		t.Fatal("actual retained owned child did not cause the expected stop-post refusal")
	}
	prepare()
	manual := exec.CommandContext(ctx, "/usr/bin/wsjtx", "--rig-name=fixture1")
	manual.Env, manual.Dir = env, home
	manual.SysProcAttr = &syscall.SysProcAttr{Credential: credential, Setpgid: true}
	var output applicationOutput
	manual.Stdout, manual.Stderr = &output, &output
	if err := manual.Start(); err != nil {
		t.Fatal(err)
	}
	var once sync.Once
	stopManual := func() { once.Do(func() { _ = syscall.Kill(-manual.Process.Pid, syscall.SIGKILL); _ = manual.Wait() }) }
	t.Cleanup(stopManual)
	waitActual()
	if g.check(ctx, "fixture1", "", programStopped) == nil || ctl("start", unit) == nil {
		t.Fatal("actual same-profile manual process failed to block stopped/start-pre checks")
	}
	stopManual()
	// An orphaned decoder can need a brief PID-1 reap after group termination.
	// Wait for an actually accepted boundary without changing its deadline.
	recovered := false
	for deadline := time.Now().Add(6 * time.Second); time.Now().Before(deadline); {
		if g.check(ctx, "fixture1", "", programStopped) == nil {
			recovered = true
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if !recovered {
		t.Fatal("actual manual process cleanup did not recover stopped checks")
	}
	if err := os.Remove(home + "/runtime/held-child.pid"); err != nil {
		t.Fatal(err)
	}
	// The positive case explicitly stops all unit members through the real
	// manager. This is not a claim about the station's mixed-stop wrapper.
	if err := os.WriteFile("/etc/systemd/system/"+unit, []byte(strings.Replace(units[unit], "DXB_WSJTX_RETAIN_CHILD=1", "DXB_WSJTX_RETAIN_CHILD=0", 1)), 0644); err != nil || ctl("daemon-reload") != nil {
		t.Fatal("positive fixture setup failed")
	}
	prepare()
	if ctl("start", unit) != nil {
		t.Fatal("actual managed restart failed")
	}
	waitActual()
	if ctl("kill", "--kill-whom=all", "--signal=TERM", unit) != nil {
		t.Fatal("explicit whole-unit fixture stop failed")
	}
	if ctl("stop", unit) != nil || g.check(ctx, "fixture1", "", programStopped) != nil {
		t.Fatal("actual recovered stop failed")
	}
	phases, err := os.ReadFile("/run/dxb-wsjtx/phase-events")
	if err != nil || string(phases) != "pre:pass\npost:refused-owned-child\npre:refused\npost:refused\npre:pass\npost:pass\n" {
		t.Fatal("real manager phase evidence incomplete")
	}
	report := map[string]any{"kind": "wsjtx-real-manager-boundaries", "result": "partial", "package_sha256": applicationPackageHash,
		"passed":   []string{"real start-pre/stopped guard", "positive stop-post after explicit whole-unit stop", "actual managed idle WSJT-X", "active-unit refusal", "mixed-stop retained owned-child refusal", "actual same-profile manual process refusal", "fresh observed recovery"},
		"untested": []string{"normal station mixed-stop wrapper and automatic restoration", "public session IPC and settings restoration", "update/rollback lifecycle", "physical-radio eligibility", "GUI change and Decode/Clear input", "ARM64 capture workload", "physical radio/RF"}}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(string(raw))
}
