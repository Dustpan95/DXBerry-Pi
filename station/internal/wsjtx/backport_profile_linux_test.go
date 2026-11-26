//go:build wsjtxcompat && wsjtxmanager && wsjtxscreen

package wsjtx

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestBackportApplicationProfile(t *testing.T) {
	wav := os.Getenv("DXB_BACKPORT_WAV")
	if runtime.GOARCH != "arm64" || os.Geteuid() != 65534 || wav == "" || os.Getenv("DISPLAY") == "" {
		t.Fatal("native unprivileged ARM64 display fixture required")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range interfaces {
		if i.Flags&net.FlagLoopback == 0 {
			t.Fatal("fixture requires loopback only")
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "/usr/bin/python3", "/fixture/audio-info.py").Output()
	var audio struct{ Input, Output string }
	if err != nil || json.Unmarshal(out, &audio) != nil || audio.Input == "" || audio.Output == "" {
		t.Fatalf("Qt fixture devices unavailable: %v", err)
	}
	observer := NewObserver(nil)
	defer observer.Close()
	binding, err := observer.Start("fixture1", "WSJT-X - fixture1")
	if err != nil {
		t.Fatal(err)
	}
	settings := fmt.Sprintf("[Configuration]\nMyCall=N0CALL\nMyGrid=AA00aa\nRig=None\nSoundInName=%q\nSoundOutName=%q\nUDPServer=127.0.0.1\nUDPServerPort=%d\nAcceptUDPRequests=false\n[Common]\nMode=FT8\nDialFreq=%d\n[MainWindow]\nAutoSeq=false\n", audio.Input, audio.Output, binding.Address().Port(), screenFT8DialHz)
	if err := os.WriteFile(filepath.Join(os.Getenv("XDG_CONFIG_HOME"), "WSJT-X - fixture1.ini"), []byte(settings), 0600); err != nil {
		t.Fatal(err)
	}
	app := exec.CommandContext(ctx, "/usr/bin/wsjtx", "--rig-name=fixture1")
	app.Stdout, app.Stderr = os.Stdout, os.Stderr
	if err := app.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		_ = app.Wait()
	}()
	deadline := time.Now().Add(25 * time.Second)
	for {
		s, ok := observer.Snapshot("fixture1")
		o := s.Observation
		if o.Status != nil && (o.Status.Transmitting || o.Status.TXEnabled) {
			t.Fatal("receive-only profile expected")
		}
		if ok && o.Availability == Live && o.Heartbeat != nil && o.Heartbeat.Version != nil && strings.Contains(*o.Heartbeat.Version, "3.0.2") && o.Status != nil && o.Status.DECall != nil && *o.Status.DECall == "N0CALL" && o.Status.DEGrid != nil && *o.Status.DEGrid == "AA00aa" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("expected the configured ARM64 application's heartbeat, callsign and locator")
		}
		time.Sleep(100 * time.Millisecond)
	}
	waitScreenFT8Ready(t, observer)
	x := func(args ...string) string {
		t.Helper()
		c, done := context.WithTimeout(ctx, 8*time.Second)
		defer done()
		out, err := exec.CommandContext(c, "xdotool", args...).CombinedOutput()
		if err != nil {
			t.Fatalf("xdotool %q: %v %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	pid := strconv.Itoa(app.Process.Pid)
	window := findScreenWindow(t, pid, x)
	checkScreenOfflineDecode(t, observer, x, window, pid, wav)
}
