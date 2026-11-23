package wsjtx

import (
	"bytes"
	"context"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

const (
	programCheckTimeout = 2 * time.Second
	maxProgramProcesses = 32768
	maxProgramFileBytes = 64 * 1024
)

// systemdProgramLifecycle verifies the manager's exact invocation/state on
// both sides of process inspection. MainPID=0 alone is insufficient: surviving
// application processes in nested unit cgroups, and manually launched WSJT-X
// instances using this radio's settings, prevent edits too.
type systemdProgramLifecycle struct {
	uid        uint64
	procRoot   string
	cgroupRoot string
	show       func(context.Context, string) ([]byte, error)
}

func newSystemdProgramLifecycle() (*systemdProgramLifecycle, error) {
	if os.Geteuid() != 0 {
		return nil, ErrSettingsBridgeIdentity
	}
	account, err := user.Lookup("dxbapps")
	if err != nil {
		return nil, ErrSettingsBridgeIdentity
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil || uid == 0 {
		return nil, ErrSettingsBridgeIdentity
	}
	return &systemdProgramLifecycle{uid: uid, procRoot: "/proc", cgroupRoot: "/sys/fs/cgroup", show: showProgramUnit}, nil
}

// A named buffer prevents io.Copy from bypassing Write through promoted ReadFrom.
type boundedProgramOutput struct{ buffer bytes.Buffer }

func (b *boundedProgramOutput) Bytes() []byte { return b.buffer.Bytes() }

func (b *boundedProgramOutput) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > maxProgramFileBytes {
		return 0, ErrProgramLifecycle
	}
	return b.buffer.Write(p)
}

func showProgramUnit(ctx context.Context, unit string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", "show", "--no-pager", "--property=Id,LoadState,ActiveState,SubState,MainPID,InvocationID,ControlGroup", "--", unit)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	var out boundedProgramOutput
	cmd.Stdout, cmd.Stderr = &out, io.Discard
	if err := cmd.Run(); err != nil {
		return nil, ErrProgramLifecycle
	}
	return out.buffer.Bytes(), nil
}

func programRadio(radio string) bool {
	// Match dxberry-app's PROGRAM-RADIO contract, which is deliberately
	// narrower than the standalone UDP tracker's session-name contract.
	if len(radio) == 0 || len(radio) > 12 || radio[0] < 'a' || radio[0] > 'z' {
		return false
	}
	for _, c := range radio {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func programInvocation(id string) bool {
	return len(id) == 32 && strings.Trim(id, "0123456789abcdef") == "" && id != strings.Repeat("0", 32)
}

type programUnit struct {
	id, load, active, sub, pid, invocation, cgroup string
}

func parseProgramUnit(raw []byte) (programUnit, error) {
	var out programUnit
	if len(raw) == 0 || len(raw) > maxProgramFileBytes || raw[len(raw)-1] != '\n' || bytes.ContainsAny(raw, "\x00\r") {
		return out, ErrProgramLifecycle
	}
	fields := map[string]*string{"Id": &out.id, "LoadState": &out.load, "ActiveState": &out.active, "SubState": &out.sub, "MainPID": &out.pid, "InvocationID": &out.invocation, "ControlGroup": &out.cgroup}
	seen := make(map[string]bool)
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		field, known := fields[key]
		if !ok || !known || seen[key] {
			return programUnit{}, ErrProgramLifecycle
		}
		seen[key], *field = true, value
	}
	if len(seen) != len(fields) {
		return programUnit{}, ErrProgramLifecycle
	}
	return out, nil
}

func (u programUnit) matches(unit, invocation string, boundary programBoundary) bool {
	if u.id != unit || u.load != "loaded" || u.pid != "0" {
		return false
	}
	switch boundary {
	case programStartPre:
		return programInvocation(invocation) && u.invocation == invocation && u.active == "activating" && u.sub == "start-pre"
	case programStopPost:
		return programInvocation(invocation) && u.invocation == invocation && u.active == "deactivating" && u.sub == "stop-post"
	case programStopped:
		return invocation == "" && (u.active == "inactive" && u.sub == "dead" || u.active == "failed" && u.sub == "failed")
	default:
		return false
	}
}

func (g *systemdProgramLifecycle) check(ctx context.Context, radio, invocation string, boundary programBoundary) error {
	if !programRadio(radio) {
		return ErrProgramLifecycle
	}
	if ctx.Err() != nil {
		return errors.Join(ErrProgramLifecycle, ctx.Err())
	}
	ctx, cancel := context.WithTimeout(ctx, programCheckTimeout)
	defer cancel()
	fail := func() error { return errors.Join(ErrProgramLifecycle, ctx.Err()) }
	unit := "dxberry-screen@wsjtx-" + radio + ".service"
	raw, err := g.show(ctx, unit)
	if err != nil {
		return fail()
	}
	before, err := parseProgramUnit(raw)
	if err != nil || !before.matches(unit, invocation, boundary) {
		return fail()
	}
	if err := g.checkCgroup(ctx, unit, before.cgroup, boundary == programStopped); err != nil {
		return fail()
	}
	if err := g.checkManualPrograms(ctx, radio); err != nil {
		return fail()
	}
	raw, err = g.show(ctx, unit)
	if err != nil {
		return fail()
	}
	after, err := parseProgramUnit(raw)
	if err != nil || after != before || !after.matches(unit, invocation, boundary) || ctx.Err() != nil {
		return fail()
	}
	return nil
}

func readProgramFile(path string) ([]byte, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, maxProgramFileBytes+1))
	if err != nil || len(data) > maxProgramFileBytes {
		return nil, ErrProgramLifecycle
	}
	return data, nil
}

func processID(s string) bool {
	pid, err := strconv.ParseUint(s, 10, 32)
	return err == nil && pid != 0 && strconv.FormatUint(pid, 10) == s
}

func (g *systemdProgramLifecycle) applicationPID(pid string) (bool, error) {
	data, err := readProgramFile(filepath.Join(g.procRoot, pid, "status"))
	if errors.Is(err, os.ErrNotExist) {
		return false, nil // process exited during the scan
	}
	if err != nil {
		return false, err
	}
	found, matches := false, false
	for _, line := range strings.Split(string(data), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		values := strings.Fields(line)
		if found || len(values) != 5 {
			return false, ErrProgramLifecycle
		}
		found = true
		for _, raw := range values[1:] {
			uid, err := strconv.ParseUint(raw, 10, 32)
			if err != nil {
				return false, ErrProgramLifecycle
			}
			matches = matches || uid == g.uid
		}
	}
	if !found {
		return false, ErrProgramLifecycle
	}
	return matches, nil
}

func (g *systemdProgramLifecycle) checkCgroup(ctx context.Context, unit, group string, stopped bool) error {
	if group == "" && stopped {
		return nil
	}
	if !filepath.IsAbs(group) || filepath.Clean(group) != group || filepath.Base(group) != unit {
		return ErrProgramLifecycle
	}
	root := filepath.Join(g.cgroupRoot, group)
	dirs, processes := 0, 0
	return filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			if path == root && stopped && errors.Is(err, os.ErrNotExist) {
				return nil
			}
			return err
		}
		if ctx.Err() != nil || entry.Type()&os.ModeSymlink != 0 {
			return ErrProgramLifecycle
		}
		if !entry.IsDir() {
			return nil
		}
		dirs++
		if dirs > 256 {
			return ErrProgramLifecycle
		}
		data, err := readProgramFile(filepath.Join(path, "cgroup.procs"))
		if err != nil {
			return err
		}
		for _, pid := range strings.Fields(string(data)) {
			processes++
			if processes > maxProgramProcesses || !processID(pid) || ctx.Err() != nil {
				return ErrProgramLifecycle
			}
			app, err := g.applicationPID(pid)
			if err != nil || app {
				return ErrProgramLifecycle
			}
		}
		return nil
	})
}

func (g *systemdProgramLifecycle) checkManualPrograms(ctx context.Context, radio string) error {
	f, err := os.Open(g.procRoot)
	if err != nil {
		return err
	}
	defer f.Close()
	entries, err := f.ReadDir(maxProgramProcesses + 1)
	if err != nil && err != io.EOF || len(entries) > maxProgramProcesses {
		return ErrProgramLifecycle
	}
	for _, entry := range entries {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if !entry.IsDir() || !processID(entry.Name()) {
			continue
		}
		app, err := g.applicationPID(entry.Name())
		if err != nil {
			return err
		}
		if !app {
			continue
		}
		comm, err := readProgramFile(filepath.Join(g.procRoot, entry.Name(), "comm"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		cmdline, err := readProgramFile(filepath.Join(g.procRoot, entry.Name(), "cmdline"))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		// A live application process can briefly have no command line during
		// exec. Its comm may still name a launcher, so it cannot be excluded
		// from the settings owner check until its arguments are observable.
		if len(cmdline) == 0 || cmdline[len(cmdline)-1] != 0 {
			return ErrProgramLifecycle
		}
		args := strings.Split(strings.TrimSuffix(string(cmdline), "\x00"), "\x00")
		if strings.TrimSuffix(string(comm), "\n") != "wsjtx" && filepath.Base(args[0]) != "wsjtx" {
			continue
		}
		if !otherRadioProgram(args[1:], radio) {
			return ErrProgramLifecycle
		}
	}
	return nil
}

// Only the simple verified rig-name forms for another radio are ignored. A
// missing/repeated name, compact option, other flag or positional argument is
// ambiguous: a string resembling -r might be another option's value. Root's
// managed command uses exactly --rig-name=RADIO.
func otherRadioProgram(args []string, radio string) bool {
	name := ""
	if len(args) == 1 && strings.HasPrefix(args[0], "--rig-name=") {
		name = strings.TrimPrefix(args[0], "--rig-name=")
	} else if len(args) == 2 && (args[0] == "--rig-name" || args[0] == "-r") {
		name = args[1]
	}
	return programRadio(name) && name != radio
}
