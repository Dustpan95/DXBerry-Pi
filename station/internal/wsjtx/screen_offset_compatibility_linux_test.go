//go:build wsjtxcompat && wsjtxmanager && wsjtxscreen

package wsjtx

import (
	"context"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Unmodified F12/F11 move only the FT8 receive offset. Use the application
// owner's private display and the window belonging to its verified service PID.
func checkScreenReceiveOffset(t *testing.T, ctx context.Context, receiver screenReceiver, dir, pid string, uid, gid int) {
	t.Helper()
	x := func(args ...string) string {
		t.Helper()
		commandCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		cmd := exec.CommandContext(commandCtx, "/usr/bin/xdotool", args...)
		cmd.Env = []string{"DISPLAY=:102", "XAUTHORITY=" + filepath.Join(dir, "Xauthority"), "LANG=C.UTF-8"}
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid)}}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("receive offset GUI command %s: %v (%s)", args[0], err, out)
		}
		return strings.TrimSpace(string(out))
	}
	window := x("search", "--sync", "--all", "--onlyvisible", "--pid", pid, "--name", "^WSJT-X.*fixture1[[:space:]]+v3[.]0[.]2")
	if _, err := strconv.ParseUint(window, 10, 32); err != nil || x("getwindowpid", window) != pid {
		t.Fatal("expected one managed WSJT-X main window")
	}
	x("windowactivate", "--sync", window)
	original, ok := receiver.Snapshot("fixture1")
	if !ok || original.Observation.Status == nil || original.Observation.StatusSeenAt == nil {
		t.Fatal("initial receive status unavailable")
	}
	o := original.Observation
	initial := o.Status
	if initial.Mode == nil || *initial.Mode != "FT8" || initial.RXOffset >= 4999 || initial.TXEnabled || initial.Transmitting {
		t.Fatal("expected an idle FT8 receive offset")
	}
	for _, step := range []struct {
		key    string
		offset uint32
	}{{"F12", initial.RXOffset + 1}, {"F11", initial.RXOffset}} {
		if x("getactivewindow") != window {
			t.Fatal("managed main window lost focus before receive action")
		}
		start := time.Now()
		x("key", step.key)
		seen := false
		for time.Since(start) < 5*time.Second {
			s, ok := receiver.Snapshot("fixture1")
			current := s.Observation
			if !ok || current.Availability != Live || current.Generation != o.Generation || current.Epoch != o.Epoch || current.InstanceID != o.InstanceID || current.Status == nil || current.StatusSeenAt == nil {
				t.Fatal("receive observation changed identity during GUI check")
			}
			status := current.Status
			if status.TXEnabled || status.Transmitting || status.TXOffset != initial.TXOffset || status.DialFrequency != initial.DialFrequency || current.Counters.Malformed != o.Counters.Malformed || current.Counters.WrongIdentity != o.Counters.WrongIdentity {
				t.Fatalf("%s changed unexpected status: RX=%d TX-enabled=%t transmitting=%t TX-offset=%d (was %d) dial=%d (was %d) malformed=%d (was %d) wrong-identity=%d (was %d)",
					step.key, status.RXOffset, status.TXEnabled, status.Transmitting, status.TXOffset, initial.TXOffset,
					status.DialFrequency, initial.DialFrequency, current.Counters.Malformed, o.Counters.Malformed,
					current.Counters.WrongIdentity, o.Counters.WrongIdentity)
			}
			if status.RXOffset == step.offset && current.StatusSeenAt.After(start) {
				t.Logf("Receive offset %d Hz observed %v after %s", step.offset, current.StatusSeenAt.Sub(start), step.key)
				seen = true
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
		if !seen {
			t.Fatalf("missing fresh Status for receive offset %d Hz after %s", step.offset, step.key)
		}
	}
}
