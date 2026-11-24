//go:build wsjtxcompat

package wsjtx

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

const applicationPackageHash = "a0a00ebb74f2159b45930be1b2694b22f1a83a802622cd1d2edacb7c11e80e15"

// This output contains only new synthetic profiles. It is not station evidence.
type applicationOutput struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (o *applicationOutput) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.b.Len()+len(p) > 64<<10 {
		return 0, fmt.Errorf("fixture diagnostic limit exceeded")
	}
	return o.b.Write(p)
}

func (o *applicationOutput) text() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.b.String()
}

func TestWSJTXApplicationCompatibility(t *testing.T) {
	if runtime.GOOS != "linux" || runtime.GOARCH != "amd64" || os.Geteuid() != 65534 || os.Getegid() != 65534 {
		t.Fatal("unprivileged Linux/amd64 fixture identity required")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("Docker isolation required")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, item := range interfaces {
		if item.Flags&net.FlagLoopback == 0 {
			t.Fatal("external networking must be disabled")
		}
	}
	for _, pattern := range []string{"/dev/snd", "/dev/ttyUSB*", "/dev/ttyACM*", "/dev/gpiochip*", "/dev/bus/usb"} {
		paths, err := filepath.Glob(pattern)
		if err != nil || len(paths) != 0 {
			t.Fatal("radio devices must not enter the fixture")
		}
	}
	process, err := os.ReadFile("/proc/self/status")
	if err != nil || !bytes.Contains(process, []byte("CapEff:\t0000000000000000\n")) {
		t.Fatal("zero effective capabilities required")
	}
	f, err := os.Open("/fixture/wsjtx.deb")
	if err != nil {
		t.Fatal("official package unavailable")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || info.Size() != 46499354 {
		t.Fatal("official package type or size mismatch")
	}
	hash := sha256.New()
	if n, err := io.Copy(hash, io.LimitReader(f, 46499355)); err != nil || n != 46499354 || hex.EncodeToString(hash.Sum(nil)) != applicationPackageHash {
		t.Fatal("official package integrity mismatch")
	}
	root := t.TempDir()
	for _, dir := range []string{"config", "data", "cache", "state", "runtime"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0700); err != nil {
			t.Fatal(err)
		}
	}
	env := []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "LC_ALL=C.UTF-8", "HOME=" + root,
		"XDG_CONFIG_HOME=" + root + "/config", "XDG_DATA_HOME=" + root + "/data", "XDG_CACHE_HOME=" + root + "/cache",
		"XDG_STATE_HOME=" + root + "/state", "XDG_RUNTIME_DIR=" + root + "/runtime", "QT_QPA_PLATFORM=offscreen",
		"PULSE_SERVER=unix:" + root + "/runtime/pulse.sock"}
	ctx, cancel := context.WithTimeout(context.Background(), 125*time.Second)
	defer cancel()
	start := func(name string, args ...string) *exec.Cmd {
		t.Helper()
		cmd := exec.CommandContext(ctx, name, args...)
		cmd.Env, cmd.Dir = env, root
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
		cmd.WaitDelay = 200 * time.Millisecond
		var output applicationOutput
		cmd.Stdout, cmd.Stderr = &output, &output
		if err := cmd.Start(); err != nil {
			t.Fatal("fixture process start failed:", err)
		}
		t.Cleanup(func() {
			_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
			_ = cmd.Wait()
			if t.Failed() {
				t.Log(filepath.Base(name), output.text())
			}
		})
		return cmd
	}
	pulseConfig := root + "/pulse.pa"
	if err := os.WriteFile(pulseConfig, []byte("load-module module-native-protocol-unix socket="+root+"/runtime/pulse.sock auth-anonymous=1\nload-module module-null-sink sink_name=fixture_null sink_properties=device.description=DXBerryFixture\nset-default-sink fixture_null\nset-default-source fixture_null.monitor\n"), 0600); err != nil {
		t.Fatal(err)
	}
	start("/usr/bin/pulseaudio", "--daemonize=no", "--exit-idle-time=-1", "--disallow-exit", "-n", "--file="+pulseConfig)
	var audio struct {
		Qt, Input, Output string
		QtCore            string `json:"qt_core"`
	}
	for deadline := time.Now().Add(8 * time.Second); time.Now().Before(deadline); {
		cmd := exec.CommandContext(ctx, "/usr/bin/python3", "/fixture/audio-info.py")
		cmd.Env, cmd.Dir = env, root
		var stdout, stderr applicationOutput
		cmd.Stdout, cmd.Stderr = &stdout, &stderr
		if cmd.Run() == nil && json.Unmarshal([]byte(stdout.text()), &audio) == nil && audio.Qt != "" && audio.Input != "" && audio.Output != "" {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}
	if audio.Qt == "" || audio.Input == "" || audio.Output == "" {
		t.Fatal("actual Qt dummy audio unavailable")
	}
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	observer := NewObserver(nil)
	defer observer.Close()
	binding, err := observer.Start("fixture2", "WSJT-X - fixture2")
	if err != nil {
		t.Fatal(err)
	}
	ports := []int{conn.LocalAddr().(*net.UDPAddr).Port, int(binding.Address().Port())}
	var applications []*exec.Cmd
	for i, port := range ports {
		profile := fmt.Sprintf("fixture%d", i+1)
		settings := fmt.Sprintf("[Configuration]\nMyCall=N0CALL\nMyGrid=AA00aa\nRig=None\nSoundInName=%q\nSoundOutName=%q\nAudioInputChannel=Mono\nAudioOutputChannel=Mono\nUDPServer=127.0.0.1\nUDPServerPort=%d\nAcceptUDPRequests=false\nBroadcastToN1MM=true\nN1MMServer=127.0.0.2\nN1MMServerPort=23333\n[Common]\nMode=FT8\n[MainWindow]\nRxFreq=1500\nAutoSeq=false\n", audio.Input, audio.Output, port)
		if err := os.WriteFile(filepath.Join(root, "config", "WSJT-X - "+profile+".ini"), []byte(settings), 0600); err != nil {
			t.Fatal(err)
		}
		applications = append(applications, start("/usr/bin/wsjtx", "--rig-name="+profile))
	}
	var heartbeats []time.Time
	var heartbeat *Heartbeat
	var status *Status
	var schema uint32
	var peer *net.UDPAddr
	buffer := make([]byte, MaxDatagramSize+1)
	deadline := time.Now().Add(60 * time.Second)
	for time.Now().Before(deadline) && (len(heartbeats) < 3 || status == nil) {
		if err := conn.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
			t.Fatal(err)
		}
		n, sender, err := conn.ReadFromUDP(buffer)
		if err != nil {
			if err, ok := err.(net.Error); ok && err.Timeout() {
				continue
			}
			t.Fatal(err)
		}
		if !sender.IP.IsLoopback() || peer != nil && sender.String() != peer.String() {
			t.Fatal("unexpected application datagram peer")
		}
		peer = sender
		message, err := Parse(buffer[:n])
		if err != nil || message.ID != "WSJT-X - fixture1" {
			t.Fatal("actual application protocol or rig identity mismatch:", err)
		}
		schema = message.Schema
		switch message.Type {
		case HeartbeatMessage:
			heartbeats = append(heartbeats, time.Now())
			heartbeat = message.Heartbeat
		case StatusMessage:
			status = message.Status
			if status.TXEnabled || status.Transmitting {
				t.Fatal("fixture must remain non-transmitting")
			}
		}
	}
	if len(heartbeats) < 3 || heartbeat == nil || heartbeat.Version == nil || !strings.Contains(*heartbeat.Version, "3.0.2") || status == nil || status.DECall == nil || *status.DECall != "N0CALL" || status.DEGrid == nil || *status.DEGrid != "AA00aa" {
		t.Fatal("actual heartbeat/status evidence incomplete")
	}
	intervals := []float64{heartbeats[1].Sub(heartbeats[0]).Seconds(), heartbeats[2].Sub(heartbeats[1]).Seconds()}
	for _, interval := range intervals {
		if interval < 12 || interval > 18 {
			t.Fatal("unexpected actual heartbeat cadence")
		}
	}
	second, ok := observer.Snapshot("fixture2")
	if !ok || second.Observation.Availability != Live || second.Observation.Heartbeat == nil || second.Observation.Status == nil || second.Observation.Status.TXEnabled || second.Observation.Status.Transmitting || second.Observation.Counters.WrongIdentity != 0 || second.Observation.Counters.Malformed != 0 {
		t.Fatal("production observer did not accept the independent actual profile")
	}
	if second.Observation.Heartbeat.Version == nil || *second.Observation.Heartbeat.Version != *heartbeat.Version || second.Observation.Schema != schema || second.Observation.Status.DECall == nil || *second.Observation.Status.DECall != "N0CALL" || second.Observation.Status.DEGrid == nil || *second.Observation.Status.DEGrid != "AA00aa" || second.Transport.QueueDrops != 0 || second.Transport.WrongPeer != 0 {
		t.Fatal("independent actual profile evidence mismatch")
	}
	qtCore, err := os.Stat(audio.QtCore)
	if err != nil || !qtCore.Mode().IsRegular() {
		t.Fatal("reference Qt library unavailable")
	}
	for _, app := range applications {
		maps, err := os.ReadFile(fmt.Sprintf("/proc/%d/maps", app.Process.Pid))
		if err != nil {
			t.Fatal("actual application library identity unavailable")
		}
		matched := false
		for _, line := range strings.Split(string(maps), "\n") {
			fields := strings.Fields(line)
			if len(fields) != 6 || !strings.Contains(fields[5], "/libQt5Core.so.") {
				continue
			}
			if actual, err := os.Stat(fields[5]); err == nil && os.SameFile(actual, qtCore) {
				matched = true
			}
		}
		if !matched {
			t.Fatal("actual application Qt runtime differs from version reference")
		}
	}
	for i, port := range ports {
		raw, err := os.ReadFile(filepath.Join(root, "config", fmt.Sprintf("WSJT-X - fixture%d.ini", i+1)))
		if err != nil {
			t.Fatal("selected profile unavailable")
		}
		doc, err := ParseSettingsDocument(raw)
		if err != nil || doc.Routing().Current.Server.Value != "127.0.0.1" || doc.Routing().Current.Port.Value != fmt.Sprint(port) {
			t.Fatal("fixture routing or independent logging configuration changed")
		}
		// Nondefault logging sentinels distinguish preservation from a reset
		// to WSJT-X defaults. Check all logging keys, not only its port.
		for _, line := range []string{"AcceptUDPRequests=false", "BroadcastToN1MM=true", "N1MMServer=127.0.0.2", "N1MMServerPort=23333"} {
			if !bytes.Contains(raw, []byte("\n"+line+"\n")) {
				t.Fatal("fixture request policy or independent logging configuration changed")
			}
		}
	}
	report := map[string]any{"kind": "wsjtx-idle-application", "result": "partial", "package_sha256": applicationPackageHash,
		"application_version": *heartbeat.Version, "qt_runtime": audio.Qt, "schema": schema, "heartbeat_intervals_seconds": intervals,
		"passed":   []string{"official package integrity", "unprivileged device-free execution", "actual rig-name profiles", "passive actual heartbeat and status parsing", "actual heartbeat cadence", "independent profile through production observer", "separate N1MM configuration retained"},
		"untested": []string{"non-transmitting GUI change and status cadence", "reproducible Decode/Clear application input", "profile restoration and replacement lifecycle", "real systemd public lifecycle", "ARM64 capture workload", "physical radio/RF"}}
	raw, err := json.Marshal(report)
	if err != nil {
		t.Fatal(err)
	}
	fmt.Println(string(raw))
}
