//go:build wsjtxcompat

package wsjtx

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// Debugging is explicit and separate from the unchanged untraced gate. GDB is
// the actual application's parent, so no host capability or ptrace-policy
// change is needed. Kernel metadata identifies its real inferior, not GDB.
type compatibilityApplication struct {
	pid      int
	debugger *exec.Cmd
}

func startCompatibilityApplication(t *testing.T, start func(string, ...string) *exec.Cmd, profile string) compatibilityApplication {
	t.Helper()
	switch os.Getenv("DXB_WSJTX_DEBUGGER") {
	case "":
		return compatibilityApplication{pid: start("/usr/bin/wsjtx", "--rig-name="+profile).Process.Pid}
	case "1":
	default:
		t.Fatal("invalid explicit fixture debugger mode")
	}
	debugger := start("/usr/bin/gdb", "--batch", "--nx", "--quiet", "--return-child-result",
		"-ex", "set pagination off", "-ex", "set confirm off", "-ex", "set auto-load off",
		"-ex", "set print thread-events off", "-ex", "set disable-randomization off",
		"-ex", "set startup-with-shell off", "-ex", "handle SIGPIPE nostop noprint pass", "-ex", "run", "-ex", "bt 40", "-ex", "thread apply all bt 12",
		"--args", "/usr/bin/wsjtx", "--rig-name="+profile)
	reference, err := os.Stat("/usr/bin/wsjtx")
	if err != nil {
		t.Fatal(err)
	}
	for deadline := time.Now().Add(12 * time.Second); time.Now().Before(deadline); {
		children, err := filepath.Glob(fmt.Sprintf("/proc/%d/task/*/children", debugger.Process.Pid))
		if err != nil {
			t.Fatal(err)
		}
		for _, path := range children {
			raw, err := os.ReadFile(path)
			if err != nil || len(raw) > 64<<10 {
				continue
			}
			for _, child := range strings.Fields(string(raw)) {
				pid, err := strconv.Atoi(child)
				if err != nil || pid <= 0 || strconv.Itoa(pid) != child {
					t.Fatal("invalid actual debugger child identity")
				}
				actual, err := os.Stat(filepath.Join("/proc", child, "exe"))
				if err != nil || !os.SameFile(reference, actual) {
					continue
				}
				args, err := os.ReadFile(filepath.Join("/proc", child, "cmdline"))
				if err != nil || !bytes.Equal(args, []byte("/usr/bin/wsjtx\x00--rig-name="+profile+"\x00")) {
					continue
				}
				pidfd, err := unix.PidfdOpen(pid, 0)
				if err != nil {
					t.Fatal("actual debugger inferior kernel handle unavailable:", err)
				}
				t.Cleanup(func() {
					// The pidfd cannot signal a reused PID if GDB already reaped
					// the inferior. Each diagnostic uses a fresh container.
					_ = unix.PidfdSendSignal(pidfd, unix.SIGKILL, nil, 0)
					_ = unix.Close(pidfd)
				})
				return compatibilityApplication{pid: pid, debugger: debugger}
			}
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("actual WSJT-X debugger inferior unavailable; failure cleanup retains debugger output")
	return compatibilityApplication{}
}

// Allow a bounded diagnostic to finish before the driver cancels its context.
// A tracing stop cannot count as a live successful application observation.
func (a compatibilityApplication) requireDebuggerRunning(t *testing.T) {
	t.Helper()
	if a.debugger == nil {
		return
	}
	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); {
		debugger, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", a.debugger.Process.Pid))
		if err != nil || bytes.Contains(debugger, []byte("Z (zombie)")) || bytes.Contains(debugger, []byte("X (dead)")) {
			t.Fatal("actual debugger exited before runtime verification; failure cleanup retains its bounded backtrace")
		}
		status, err := os.ReadFile(fmt.Sprintf("/proc/%d/status", a.pid))
		output := a.debugger.Stdout.(*applicationOutput).text()
		fault := strings.Contains(output, "received signal SIGSEGV") || strings.Contains(output, "received signal SIGABRT") || strings.Contains(output, "received signal SIGBUS") || strings.Contains(output, "received signal SIGFPE")
		if err == nil && !fault && !bytes.Contains(status, []byte("t (tracing stop)")) && !bytes.Contains(status, []byte("Z (zombie)")) && !bytes.Contains(status, []byte("X (dead)")) {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("actual traced application did not return to a running state; failure cleanup retains debugger output")
}
