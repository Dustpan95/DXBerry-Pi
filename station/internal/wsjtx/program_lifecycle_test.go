package wsjtx

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

const programUnitName = "dxberry-screen@wsjtx-radio1.service"

func fixtureProgramUnit(active, sub, invocation, group string) []byte {
	return []byte("Id=" + programUnitName + "\nLoadState=loaded\nActiveState=" + active + "\nSubState=" + sub + "\nMainPID=0\nInvocationID=" + invocation + "\nControlGroup=" + group + "\n")
}

type systemdProgramFixture struct {
	gate  *systemdProgramLifecycle
	raw   []byte
	group string
	calls int
}

func programSystemd(t *testing.T) *systemdProgramFixture {
	t.Helper()
	root := t.TempDir()
	f := &systemdProgramFixture{group: "/system.slice/system-dxberry\\x2dscreen.slice/" + programUnitName}
	f.gate = &systemdProgramLifecycle{uid: 1000, procRoot: filepath.Join(root, "proc"), cgroupRoot: filepath.Join(root, "cgroup")}
	for _, dir := range []string{f.gate.procRoot, filepath.Join(f.gate.cgroupRoot, f.group)} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	f.write(t, filepath.Join(f.gate.cgroupRoot, f.group, "cgroup.procs"), "")
	f.raw = fixtureProgramUnit("activating", "start-pre", programInvocationOne, f.group)
	f.gate.show = func(ctx context.Context, unit string) ([]byte, error) {
		f.calls++
		if unit != programUnitName {
			t.Fatal("unexpected unit", unit)
		}
		return f.raw, nil
	}
	return f
}

func (f *systemdProgramFixture) write(t *testing.T, path, value string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}

func (f *systemdProgramFixture) pid(t *testing.T, pid, uid, comm string, args ...string) {
	t.Helper()
	dir := filepath.Join(f.gate.procRoot, pid)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	f.write(t, filepath.Join(dir, "status"), "Name:\t"+comm+"\nUid:\t"+strings.Repeat(uid+"\t", 3)+uid+"\n")
	f.write(t, filepath.Join(dir, "comm"), comm+"\n")
	f.write(t, filepath.Join(dir, "cmdline"), strings.Join(args, "\x00")+"\x00")
}

func TestSystemdProgramLifecycleRequiresExactUnitBoundaryAndInvocation(t *testing.T) {
	for _, tc := range []struct {
		name, active, sub, invocation, requested string
		boundary                                 programBoundary
		allowed                                  bool
	}{
		{"start", "activating", "start-pre", programInvocationOne, programInvocationOne, programStartPre, true},
		{"stop", "deactivating", "stop-post", programInvocationOne, programInvocationOne, programStopPost, true},
		{"inactive", "inactive", "dead", "", "", programStopped, true},
		{"failed", "failed", "failed", programInvocationOne, "", programStopped, true},
		{"active", "active", "running", programInvocationOne, programInvocationOne, programStartPre, false},
		{"main-command", "activating", "start", programInvocationOne, programInvocationOne, programStartPre, false},
		{"stop-command", "deactivating", "stop-sigterm", programInvocationOne, programInvocationOne, programStopPost, false},
		{"old-invocation", "deactivating", "stop-post", programInvocationTwo, programInvocationOne, programStopPost, false},
		{"empty-invocation", "activating", "start-pre", "", "", programStartPre, false},
		{"zero-invocation", "activating", "start-pre", strings.Repeat("0", 32), strings.Repeat("0", 32), programStartPre, false},
		{"restarting", "activating", "auto-restart", programInvocationOne, "", programStopped, false},
		{"unknown-boundary", "inactive", "dead", "", "", "unknown", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := programSystemd(t)
			f.raw = fixtureProgramUnit(tc.active, tc.sub, tc.invocation, f.group)
			err := f.gate.check(context.Background(), "radio1", tc.requested, tc.boundary)
			if (err == nil) != tc.allowed {
				t.Fatal("incorrect lifecycle decision", err)
			}
			if tc.allowed && f.calls != 2 {
				t.Fatal("unit state was not rechecked after process inspection")
			}
		})
	}
}

func TestSystemdProgramLifecycleRejectsMissingMalformedOrChangedManagerEvidence(t *testing.T) {
	for _, change := range []func([]byte) []byte{
		func(raw []byte) []byte { return []byte(strings.Replace(string(raw), "MainPID=0", "MainPID=12", 1)) },
		func(raw []byte) []byte {
			return []byte(strings.Replace(string(raw), programUnitName, "other.service", 1))
		},
		func(raw []byte) []byte {
			return []byte(strings.Replace(string(raw), "LoadState=loaded", "LoadState=not-found", 1))
		},
		func(raw []byte) []byte { return append(raw, []byte("MainPID=0\n")...) },
		func(raw []byte) []byte { return []byte(strings.Replace(string(raw), "MainPID=0\n", "", 1)) },
		func(raw []byte) []byte { return append(raw, []byte("Other=value\n")...) },
		func(raw []byte) []byte { return raw[:len(raw)-1] },
		func(raw []byte) []byte { return []byte(strings.Repeat("x", maxProgramFileBytes+1)) },
	} {
		f := programSystemd(t)
		f.raw = change(f.raw)
		if err := f.gate.check(context.Background(), "radio1", programInvocationOne, programStartPre); !errors.Is(err, ErrProgramLifecycle) {
			t.Fatal("unverified manager evidence accepted", err)
		}
	}
	f := programSystemd(t)
	f.gate.show = func(context.Context, string) ([]byte, error) {
		f.calls++
		if f.calls == 1 {
			return f.raw, nil
		}
		return fixtureProgramUnit("active", "running", programInvocationOne, f.group), nil
	}
	if err := f.gate.check(context.Background(), "radio1", programInvocationOne, programStartPre); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("unit transition during process inspection accepted", err)
	}
	f.gate.show = func(context.Context, string) ([]byte, error) { return nil, errors.New("manager unavailable") }
	if err := f.gate.check(context.Background(), "radio1", programInvocationOne, programStartPre); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("missing manager accepted", err)
	}
}

func TestSystemdProgramLifecycleFindsNestedApplicationProcessesWithZeroMainPID(t *testing.T) {
	f := programSystemd(t)
	f.pid(t, "101", "0", "dxberry-app", "dxberry-app", "prepare", "wsjtx-radio1")
	f.write(t, filepath.Join(f.gate.cgroupRoot, f.group, "cgroup.procs"), "101\n")
	if err := f.gate.check(context.Background(), "radio1", programInvocationOne, programStartPre); err != nil {
		t.Fatal("root start-pre hook was treated as the application", err)
	}
	child := filepath.Join(f.gate.cgroupRoot, f.group, "child")
	if err := os.Mkdir(child, 0755); err != nil {
		t.Fatal(err)
	}
	f.pid(t, "102", "1000", "Xvnc", "Xvnc", ":1")
	f.write(t, filepath.Join(child, "cgroup.procs"), "102\n")
	if err := f.gate.check(context.Background(), "radio1", programInvocationOne, programStartPre); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("surviving nested application process accepted", err)
	}
	// Real/effective/saved/filesystem UIDs must all be checked.
	f.write(t, filepath.Join(f.gate.procRoot, "102", "status"), "Uid:\t0\t0\t1000\t0\n")
	if err := f.gate.check(context.Background(), "radio1", programInvocationOne, programStartPre); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("saved application identity accepted", err)
	}
}

func TestSystemdProgramLifecycleRefusesUnsafeOrUnknownCgroup(t *testing.T) {
	for _, group := range []string{"", "/", "/system.slice/other.service", "/../" + programUnitName, "/system.slice/../" + programUnitName, "relative/" + programUnitName} {
		f := programSystemd(t)
		f.raw = fixtureProgramUnit("activating", "start-pre", programInvocationOne, group)
		if err := f.gate.check(context.Background(), "radio1", programInvocationOne, programStartPre); !errors.Is(err, ErrProgramLifecycle) {
			t.Fatal("unsafe cgroup accepted", group, err)
		}
	}
	f := programSystemd(t)
	group := filepath.Join(f.gate.cgroupRoot, f.group)
	if err := os.RemoveAll(group); err != nil {
		t.Fatal(err)
	}
	if err := f.gate.check(context.Background(), "radio1", programInvocationOne, programStartPre); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("missing active cgroup accepted", err)
	}
	f.raw = fixtureProgramUnit("inactive", "dead", "", f.group)
	if err := f.gate.check(context.Background(), "radio1", "", programStopped); err != nil {
		t.Fatal("removed stopped cgroup was not allowed", err)
	}
	f.raw = fixtureProgramUnit("inactive", "dead", "", "")
	if err := f.gate.check(context.Background(), "radio1", "", programStopped); err != nil {
		t.Fatal("empty stopped cgroup was not allowed", err)
	}
	if err := os.Symlink(f.gate.procRoot, group); err != nil {
		t.Fatal(err)
	}
	f.raw = fixtureProgramUnit("activating", "start-pre", programInvocationOne, f.group)
	if err := f.gate.check(context.Background(), "radio1", programInvocationOne, programStartPre); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("cgroup symlink accepted", err)
	}
}

func TestSystemdProgramLifecycleDetectsManuallyLaunchedMatchingOrAmbiguousWSJTX(t *testing.T) {
	for _, tc := range []struct {
		args    []string
		allowed bool
	}{
		{[]string{"--rig-name=radio1"}, false},
		{[]string{"--rig-name", "radio1"}, false},
		{[]string{"-r", "radio1"}, false},
		{[]string{"-rradio1"}, false},
		{[]string{"--rig-name=radio2"}, true},
		{[]string{"--rig-name", "radio2"}, true},
		{[]string{"-r", "radio2"}, true},
		{[]string{"-rradio2"}, false},
		{[]string{"--rig-name=radio10"}, true},
		{nil, false},
		{[]string{"-r"}, false},
		{[]string{"--", "--rig-name=radio2"}, false},
		{[]string{"--rig-name=radio2", "-r", "radio1"}, false},
		{[]string{"--rig-name="}, false},
		{[]string{"--rig-name=../other"}, false},
		{[]string{"--config", "-rradio2"}, false},
		{[]string{"--rig-name=radio2", "--config", "Default"}, false},
	} {
		t.Run(fmt.Sprint(tc.args), func(t *testing.T) {
			f := programSystemd(t)
			f.pid(t, "123", "1000", "wsjtx", append([]string{"/usr/bin/wsjtx"}, tc.args...)...)
			err := f.gate.check(context.Background(), "radio1", programInvocationOne, programStartPre)
			if (err == nil) != tc.allowed {
				t.Fatal("manual WSJT-X lifecycle decision", err)
			}
		})
	}
}

func TestSystemdProgramLifecycleFailsClosedOnUninspectableProcesses(t *testing.T) {
	for _, change := range []func(*systemdProgramFixture, string){
		func(f *systemdProgramFixture, dir string) { f.write(t, filepath.Join(dir, "status"), "Uid:\t1000\n") },
		func(f *systemdProgramFixture, dir string) { f.write(t, filepath.Join(dir, "status"), "Name:\twsjtx\n") },
		func(f *systemdProgramFixture, dir string) {
			f.write(t, filepath.Join(dir, "status"), "Uid:\t0\t0\tbad\t0\n")
		},
		func(f *systemdProgramFixture, dir string) {
			f.write(t, filepath.Join(dir, "cmdline"), "wsjtx\x00--rig-name=radio2")
		},
		func(f *systemdProgramFixture, dir string) {
			f.write(t, filepath.Join(dir, "cmdline"), strings.Repeat("x", maxProgramFileBytes+1))
		},
	} {
		f := programSystemd(t)
		f.pid(t, "123", "1000", "wsjtx", "wsjtx", "--rig-name=radio2")
		change(f, filepath.Join(f.gate.procRoot, "123"))
		if err := f.gate.check(context.Background(), "radio1", programInvocationOne, programStartPre); !errors.Is(err, ErrProgramLifecycle) {
			t.Fatal("unknown process evidence accepted", err)
		}
	}
}

func TestSystemdProgramLifecycleReadsRealCrossUserProcessIdentity(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("cross-user process requires isolated root container")
	}
	f := programSystemd(t)
	f.gate.procRoot = "/proc"
	cmd := exec.Command("/bin/sleep", "60")
	cmd.Args[0] = "wsjtx" // inert process fixture, never the actual radio program
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 1000, Gid: 1000, Groups: []uint32{1000}}}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	if err := f.gate.check(context.Background(), "radio1", programInvocationOne, programStartPre); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("actual application-UID process was not observed", err)
	}
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if err := f.gate.check(context.Background(), "radio1", programInvocationOne, programStartPre); err != nil {
		t.Fatal("exited process still blocks the isolated boundary", err)
	}
}

func TestSystemdProgramLifecycleRefusesUnobservableApplicationArguments(t *testing.T) {
	for _, args := range []string{"", "launcher"} {
		f := programSystemd(t)
		f.pid(t, "123", "1000", "launcher", "launcher")
		f.write(t, filepath.Join(f.gate.procRoot, "123", "cmdline"), args)
		if err := f.gate.check(context.Background(), "radio1", programInvocationOne, programStartPre); !errors.Is(err, ErrProgramLifecycle) {
			t.Fatal("unobservable application arguments accepted", err)
		}
	}
}

func TestSystemdProgramLifecycleValidatesNamesAndCancellation(t *testing.T) {
	f := programSystemd(t)
	for _, name := range []string{"", "--help", "radio-1", "radio/1", "a123456789012", "1radio"} {
		if err := f.gate.check(context.Background(), name, programInvocationOne, programStartPre); !errors.Is(err, ErrProgramLifecycle) {
			t.Fatal("invalid unit instance accepted", name, err)
		}
	}
	if f.calls != 0 {
		t.Fatal("invalid instance reached the manager")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := f.gate.check(ctx, "radio1", programInvocationOne, programStartPre); !errors.Is(err, context.Canceled) {
		t.Fatal("cancellation lost", err)
	}
}

func TestProgramMetadataOutputRejectsFiniteOversizedSubprocess(t *testing.T) {
	for _, size := range []int{maxProgramFileBytes, maxProgramFileBytes + 1} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), programCheckTimeout)
			defer cancel()
			cmd := exec.CommandContext(ctx, "/bin/sh", "-c", fmt.Sprintf("head -c %d /dev/zero", size))
			var out boundedProgramOutput
			cmd.Stdout = &out
			err := cmd.Run()
			if size > maxProgramFileBytes {
				if err == nil {
					t.Fatal("subprocess copy bypassed output bound")
				}
			} else if err != nil {
				t.Fatal("boundary output refused", err)
			}
		})
	}
}
