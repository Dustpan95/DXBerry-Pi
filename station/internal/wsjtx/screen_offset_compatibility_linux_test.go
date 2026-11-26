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

const screenFT8DialHz = 14074000

// Use the application owner's private display and the verified service PID.
func openScreenWindow(t *testing.T, ctx context.Context, dir, pid string, uid, gid int) (func(...string) string, string) {
	t.Helper()
	x := func(args ...string) string {
		t.Helper()
		commandCtx, cancel := context.WithTimeout(ctx, 8*time.Second)
		defer cancel()
		cmd := exec.CommandContext(commandCtx, "/usr/bin/xdotool", args...)
		cmd.Env = []string{"DISPLAY=:102", "XAUTHORITY=" + filepath.Join(dir, "Xauthority"), "LANG=C.UTF-8"}
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{}}}
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("screen GUI command %q: %v (context %v, output %q)", args, err, commandCtx.Err(), out)
		}
		return strings.TrimSpace(string(out))
	}
	return x, findScreenWindow(t, pid, x)
}

func findScreenWindow(t *testing.T, pid string, x func(...string) string) string {
	t.Helper()
	window := x("search", "--sync", "--all", "--onlyvisible", "--pid", pid, "--name", "^WSJT-X.*fixture1[[:space:]]+v3[.]0[.]2")
	if _, err := strconv.ParseUint(window, 10, 32); err != nil || x("getwindowpid", window) != pid {
		t.Fatal("expected one managed WSJT-X main window")
	}
	x("windowactivate", "--sync", window)
	return window
}

// WSJT-X suppresses UDP decodes for 0.6 FT8 periods after a band change.
// Require the configured idle profile throughout that interval before using
// the GUI. Startup can briefly report the saved dial and then change away.
func waitScreenFT8Ready(t *testing.T, receiver screenReceiver) Snapshot {
	t.Helper()
	var stableSince time.Time
	var last Snapshot
	for deadline := time.Now().Add(35 * time.Second); time.Now().Before(deadline); {
		current, ok := receiver.Snapshot("fixture1")
		last = current.Observation
		s := last.Status
		if !ok || last.Availability != Live || s == nil || last.StatusSeenAt == nil {
			t.Fatal("initial receive status unavailable")
		}
		if s.TXEnabled || s.Transmitting {
			t.Fatal("transmit active during receive-only fixture startup")
		}
		if s.Mode != nil && *s.Mode == "FT8" && s.DialFrequency == screenFT8DialHz && s.RXOffset < 4999 {
			if stableSince.IsZero() {
				stableSince = time.Now()
			} else if time.Since(stableSince) >= 10*time.Second {
				return last
			}
		} else {
			stableSince = time.Time{}
		}
		time.Sleep(50 * time.Millisecond)
	}
	mode := ""
	if last.Status != nil && last.Status.Mode != nil {
		mode = *last.Status.Mode
	}
	t.Fatalf("FT8 profile did not settle: mode=%q dial=%d RX=%d", mode, last.Status.DialFrequency, last.Status.RXOffset)
	return Snapshot{}
}

func checkScreenReceiveOffset(t *testing.T, receiver screenReceiver, o Snapshot, x func(...string) string, window string) {
	t.Helper()
	now, ok := receiver.Snapshot("fixture1")
	afterFocus := now.Observation
	if !ok || afterFocus.Availability != Live || afterFocus.Generation != o.Generation || afterFocus.Epoch != o.Epoch ||
		afterFocus.InstanceID != o.InstanceID || afterFocus.Status == nil || afterFocus.Status.Mode == nil ||
		*afterFocus.Status.Mode != "FT8" || afterFocus.Status.DialFrequency != screenFT8DialHz ||
		afterFocus.Status.RXOffset != o.Status.RXOffset || afterFocus.Status.TXOffset != o.Status.TXOffset ||
		afterFocus.Status.TXEnabled || afterFocus.Status.Transmitting {
		t.Fatal("settled FT8 session changed before the first receive action")
	}
	initial := o.Status
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
