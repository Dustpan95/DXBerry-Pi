//go:build wsjtxcompat && wsjtxmanager && wsjtxscreen

package wsjtx

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// The guest supplies synthetic session data instead of preparing a physical
// radio. It uses the production screen runner, module loader and lifecycle guard.
func TestWSJTXScreenSessionCompatibility(t *testing.T) {
	runWSJTXScreenFixture(t, false)
}

func runWSJTXScreenFixture(t *testing.T, nativeRouting bool) {
	t.Helper()
	requireWSJTXManagerGuest(t)
	requireWSJTXPrivateDevices(t, "/dev")
	f, err := os.Open("/fixture/wsjtx.deb")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	hash := sha256.New()
	if n, err := io.Copy(hash, io.LimitReader(f, 46499355)); err != nil || n != 46499354 || hex.EncodeToString(hash.Sum(nil)) != applicationPackageHash {
		t.Fatal("official package integrity mismatch")
	}
	account, err := user.Lookup("dxbapps")
	if err != nil {
		t.Fatal(err)
	}
	uid, uidErr := strconv.Atoi(account.Uid)
	gid, gidErr := strconv.Atoi(account.Gid)
	if uidErr != nil || gidErr != nil || uid == 0 {
		t.Fatal("fixture application account unavailable")
	}
	home := "/var/lib/dxb-wsjtx-home"
	configDir := home + "/config"
	boundaryTest := "TestWSJTXManagerBoundary"
	if nativeRouting {
		home, configDir = settingsAppHome, settingsAppHome+"/.config"
		boundaryTest = "TestWSJTXPublicRoutingBoundary"
	}
	const run = "/run/dxb-screen"
	const session = "wsjtx-fixture1"
	const unit = "dxberry-screen@" + session + ".service"
	for _, dir := range []string{home, configDir, home + "/data", home + "/cache", home + "/state", home + "/runtime", home + "/audio", home + "/audio/fixture1"} {
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chmod(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(dir, uid, gid); err != nil {
			t.Fatal(err)
		}
	}
	for _, dir := range []string{"/run/dxb-wsjtx", run} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal("fresh fixture required:", err)
		}
	}
	for _, dir := range []string{run + "/sessions", run + "/screens"} {
		if err := os.Mkdir(dir, 0750); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(dir, 0, gid); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.Chmod(run+"/screens", os.ModeSticky|os.ModeSetgid|0770); err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "HOME=" + home, "GOMAXPROCS=2",
		"XDG_CONFIG_HOME=" + configDir, "XDG_DATA_HOME=" + home + "/data", "XDG_CACHE_HOME=" + home + "/cache",
		"XDG_STATE_HOME=" + home + "/state", "XDG_RUNTIME_DIR=" + home + "/runtime", "PULSE_SERVER=unix:" + home + "/audio/fixture1/native"}
	ctx, cancel := context.WithTimeout(context.Background(), 160*time.Second)
	t.Cleanup(cancel)
	var routing *routingScreenFixture
	if nativeRouting {
		routing = newRoutingScreenFixture(t, ctx)
	}
	ctl := func(args ...string) error {
		cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", args...)
		var output applicationOutput
		cmd.Stdout, cmd.Stderr = &output, &output
		if err := cmd.Run(); err != nil {
			t.Log("systemctl", args, output.text())
			return err
		}
		return nil
	}
	pulseConfig := home + "/pulse.pa"
	if err := os.WriteFile(pulseConfig, []byte("load-module module-native-protocol-unix socket="+home+"/audio/fixture1/native auth-anonymous=1\nload-module module-null-sink sink_name=fixture_null sink_properties=device.description=DXBerryFixture\nset-default-sink fixture_null\nset-default-source fixture_null.monitor\n"), 0644); err != nil {
		t.Fatal(err)
	}
	unitEnvironment := "Environment=" + strings.Join(env, " ") + "\n"
	units := map[string]string{
		"dxb-wsjtx-pulse.service": "[Service]\nUser=dxbapps\nGroup=dxbapps\nPrivateDevices=yes\nNoNewPrivileges=yes\n" + unitEnvironment + "ExecStart=/usr/bin/pulseaudio --daemonize=no --exit-idle-time=-1 --disallow-exit -n --file=" + pulseConfig + "\nTimeoutStopSec=5\n",
		unit: "[Service]\nType=simple\nUser=dxbapps\nGroup=dxbapps\nUMask=0007\nPrivateDevices=yes\nNoNewPrivileges=yes\nKillMode=mixed\nTimeoutStartSec=40\nTimeoutStopSec=20\n" + unitEnvironment +
			"Environment=DXB_WSJTX_MANAGER_FIXTURE=1 QT_QPA_PLATFORM=xcb DXB_LIB=/fixture/lib DXB_APPS_HOME=" + home + " DXB_RUN_DIR=" + run + " DXB_AUDIO_DIR=" + home + "/audio\n" +
			"ExecStartPre=+/usr/bin/env DXB_WSJTX_MANAGER_BOUNDARY=pre /fixture/wsjtx-manager.test -test.run=^" + boundaryTest + "$\nExecStart=/fixture/run-session\nExecStopPost=+/usr/bin/env DXB_WSJTX_MANAGER_BOUNDARY=post /fixture/wsjtx-manager.test -test.run=^" + boundaryTest + "$\n",
	}
	for name, contents := range units {
		if err := os.WriteFile("/etc/systemd/system/"+name, []byte(contents), 0644); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() {
		stop, done := context.WithTimeout(context.Background(), 25*time.Second)
		defer done()
		_ = exec.CommandContext(stop, "/usr/bin/systemctl", "stop", unit, "dxb-wsjtx-pulse.service").Run()
	})
	if ctl("daemon-reload") != nil || ctl("start", "dxb-wsjtx-pulse.service") != nil {
		t.Fatal("fixture manager setup failed")
	}
	var audio struct{ Input, Output string }
	for deadline := time.Now().Add(12 * time.Second); time.Now().Before(deadline); {
		cmd := exec.CommandContext(ctx, "/usr/bin/python3", "/fixture/audio-info.py")
		cmd.Env = append(append([]string{}, env...), "QT_QPA_PLATFORM=offscreen")
		cmd.Dir = home
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{}}}
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
	var previousInvocation string
	var previousBinding Binding
	for generation := 0; generation < 2; generation++ {
		var binding Binding
		port := 2237
		if routing == nil {
			binding, err = observer.Start("fixture1", "WSJT-X - fixture1")
			if err != nil {
				t.Fatal(err)
			}
			port = int(binding.Address().Port())
		}
		path := configDir + "/WSJT-X - fixture1.ini"
		if routing == nil || generation == 0 {
			profile := fmt.Sprintf("[Configuration]\nMyCall=N0CALL\nMyGrid=AA00aa\nRig=None\nSoundInName=%q\nSoundOutName=%q\nUDPServer=127.0.0.1\nUDPServerPort=%d\nAcceptUDPRequests=false\nBroadcastToN1MM=true\nN1MMServer=127.0.0.2\nN1MMServerPort=23333\n[Common]\nMode=FT8\n[MainWindow]\nAutoSeq=false\n", audio.Input, audio.Output, port)
			if routing != nil {
				profile = strings.Replace(profile, "UDPServer=127.0.0.1\nUDPServerPort=2237\n", "UDPServer=\"localhost\"\nUDPServerPort=02237\n", 1)
				profile += "[MultiSettings]\nCurrentName=Default\nOther%20Profile\\Configuration\\UDPServer=127.0.0.2\nOther%20Profile\\Configuration\\UDPServerPort=2238\nOther%20Profile\\OtherKey=keep\nBlank\\MultiSettingsPlaceHolder=@Invalid()\n"
			}
			if err := os.WriteFile(path, []byte(profile), 0600); err != nil {
				t.Fatal(err)
			}
			if err := os.Chown(path, uid, gid); err != nil {
				t.Fatal(err)
			}
		}
		if routing != nil {
			routing.prepare(t, ctx, generation)
		}
		dir := run + "/sessions/" + session + ".d"
		if err := os.Mkdir(dir, 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(dir, uid, gid); err != nil {
			t.Fatal(err)
		}
		envf := run + "/sessions/" + session + ".env"
		if err := os.WriteFile(envf, []byte("APP=wsjtx\nRADIO=fixture1\nLABEL=WSJT-X\nRADIO_LABEL=fixture1\nINDEX=1\nDEVICE=''\nCARD=''\nRIGCTLD=''\nAUDIO=alsa\nSCREEN=yes\nGEOMETRY=1280x800\nDISPLAY_NUM=102\n"), 0640); err != nil {
			t.Fatal(err)
		}
		if err := os.Chown(envf, 0, gid); err != nil {
			t.Fatal(err)
		}
		if ctl("start", unit) != nil {
			t.Fatal("real screen-session start failed")
		}
		if routing != nil {
			observer, binding = routing.started(t, ctx)
		}
		if generation > 0 && (binding.Generation() <= previousBinding.Generation() || binding.Address() == previousBinding.Address()) {
			t.Fatal("fresh observation generation and endpoint required")
		}
		previousBinding = binding
		observed := false
		for deadline := time.Now().Add(35 * time.Second); time.Now().Before(deadline); {
			s, ok := observer.Snapshot("fixture1")
			if ok && s.Observation.Availability == Live && s.Observation.Status != nil && s.Observation.Heartbeat != nil {
				o := s.Observation
				if o.Heartbeat.Version == nil || *o.Heartbeat.Version != "3.0.2" || o.Status.TXEnabled || o.Status.Transmitting || o.Status.DECall == nil || *o.Status.DECall != "N0CALL" || o.Status.DEGrid == nil || *o.Status.DEGrid != "AA00aa" || o.Counters.Malformed != 0 || o.Counters.WrongIdentity != 0 || s.Transport.QueueDrops != 0 || s.Transport.WrongPeer != 0 {
					t.Fatal("actual screen-session application evidence mismatch")
				}
				observed = true
				break
			}
			time.Sleep(100 * time.Millisecond)
		}
		if !observed {
			t.Fatal("actual screen-session protocol evidence unavailable")
		}
		rawUnit, err := showProgramUnit(ctx, unit)
		actualUnit, parseErr := parseProgramUnit(rawUnit)
		if err != nil || parseErr != nil || !processID(actualUnit.pid) || !programInvocation(actualUnit.invocation) || actualUnit.invocation == previousInvocation {
			t.Fatal("fresh real manager invocation required")
		}
		previousInvocation = actualUnit.invocation
		requireWSJTXPrivateDevices(t, filepath.Join("/proc", actualUnit.pid, "root/dev"))
		members, err := os.ReadFile(filepath.Join("/sys/fs/cgroup", actualUnit.cgroup, "cgroup.procs"))
		if err != nil {
			t.Fatal("actual session cgroup unavailable")
		}
		for _, name := range []string{"wsjtx", "Xvnc", "openbox"} {
			reference, err := os.Stat("/usr/bin/" + name)
			if err != nil {
				t.Fatal("actual session executable unavailable:", name)
			}
			found := false
			for _, pid := range strings.Fields(string(members)) {
				if !processID(pid) {
					t.Fatal("invalid actual session member")
				}
				actual, err := os.Stat(filepath.Join("/proc", pid, "exe"))
				if err == nil && os.SameFile(actual, reference) {
					found = true
				}
			}
			if !found {
				t.Fatal("actual session executable not running in its unit:", name)
			}
		}
		for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal("guest listener inventory unavailable")
			}
			for _, line := range strings.Split(string(raw), "\n")[1:] {
				if fields := strings.Fields(line); len(fields) > 3 && fields[3] == "0A" {
					t.Fatal("TCP listeners must not enter the private screen fixture")
				}
			}
		}
		if info, err := os.Stat(run + "/screens/" + session + ".sock"); err != nil || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0660 || info.Sys().(*syscall.Stat_t).Uid != uint32(uid) {
			t.Fatal("actual private VNC UNIX socket unavailable")
		}
		if info, err := os.Stat(dir + "/Xauthority"); err != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Sys().(*syscall.Stat_t).Uid != uint32(uid) {
			t.Fatal("actual private X cookie unavailable")
		}
		if ctl("stop", unit) != nil || g.check(ctx, "fixture1", "", programStopped) != nil {
			t.Fatal("normal screen-session stop did not establish a safe restoration boundary")
		}
		if routing != nil {
			routing.stopped(t, ctx)
		}
		phases, err := os.ReadFile("/run/dxb-wsjtx/phase-events")
		if err != nil || string(phases) != strings.Repeat("pre:pass\npost:pass\n", generation+1) {
			t.Fatalf("normal screen-session boundary phase mismatch: %q", phases)
		}
		if _, err := os.Lstat(run + "/screens/" + session + ".sock"); !os.IsNotExist(err) {
			t.Fatal("screen socket remained after normal stop")
		}
		// Synthetic preparation only, after the unchanged guard has proved
		// the real processes gone; this is not public stopped IPC acceptance.
		if err := os.RemoveAll(dir); err != nil {
			t.Fatal(err)
		}
	}
	phases, err := os.ReadFile("/run/dxb-wsjtx/phase-events")
	if err != nil || string(phases) != "pre:pass\npost:pass\npre:pass\npost:pass\n" {
		t.Fatal("normal screen-session start/stop phase evidence incomplete")
	}
	report := map[string]any{"kind": "wsjtx-real-screen-session", "result": "partial", "package_sha256": applicationPackageHash,
		"passed":   []string{"production screen runner and WSJT-X module", "actual Xvnc/Openbox with private UNIX VNC and X cookie", "two fresh actual application observations and manager invocations", "normal mixed-mode stop and real stop-post guard", "fresh stopped checks before synthetic reprepare"},
		"untested": []string{"physical eligibility and public prepare/session/stopped IPC", "native route restoration and update/rollback", "GUI change and Decode/Clear input", "ARM64 capture workload", "physical radio/RF"}}
	if routing != nil {
		report["kind"] = "wsjtx-public-hook-routing"
		report["passed"] = append(report["passed"].([]string), "actual public root hook IPC", "actual application-user settings helper", "durable approval/generation/journal", "managed route installation and restoration on two normal stops", "running mutation refusal and routing/logging preservation")
		report["untested"] = []string{"physical eligibility and public prepare/session CLI", "daemon restart and interrupted restoration", "update/rollback", "GUI change and Decode/Clear input", "ARM64 capture workload", "physical radio/RF"}
	}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(string(raw))
}
