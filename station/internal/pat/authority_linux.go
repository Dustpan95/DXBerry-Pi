package pat

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode/utf8"
)

// instance is private identity evidence, never browser data or persisted state.
type instance struct {
	records            recordEvidence
	unit               unitState
	pid, start, socket string
	uid                uint32
	executable         fileStamp
}

type fileStamp struct {
	device, inode           uint64
	size, modified, changed int64
	mode                    os.FileMode
	uid                     uint32
}

type recordEvidence struct {
	digest [32]byte
	file   fileStamp
}

func stamp(info os.FileInfo) fileStamp {
	st := info.Sys().(*syscall.Stat_t)
	return fileStamp{device: uint64(st.Dev), inode: uint64(st.Ino), size: info.Size(), modified: info.ModTime().UnixNano(), changed: int64(st.Ctim.Sec)*1e9 + int64(st.Ctim.Nsec), mode: info.Mode(), uid: st.Uid}
}

type authority struct {
	records, proc, cgroups, executable string
	uid                                func() (uint32, error)
	command                            func(context.Context, string, ...string) ([]byte, error)
}

func newAuthority(records string) *authority {
	return &authority{records: records, proc: "/proc", cgroups: "/sys/fs/cgroup", executable: "/usr/bin/pat", uid: localAppsUID, command: metadataCommand}
}

func validRadio(s string) bool {
	if len(s) == 0 || len(s) > 12 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

func decimal(s string, bits int, zero bool) bool {
	n, err := strconv.ParseUint(s, 10, bits)
	return err == nil && (zero || n != 0) && strconv.FormatUint(n, 10) == s
}

func boundedFile(path string, limit int64, trusted bool) ([]byte, error) {
	raw, _, err := fileEvidence(path, limit, trusted)
	return raw, err
}

func fileEvidence(path string, limit int64, trusted bool) ([]byte, fileStamp, error) {
	var none fileStamp
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, none, ErrUnavailable
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, none, ErrUnavailable
	}
	if trusted {
		st, ok := info.Sys().(*syscall.Stat_t)
		if !ok || st.Uid != 0 || info.Mode().Perm()&0022 != 0 {
			return nil, none, ErrUnavailable
		}
	}
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(raw)) > limit {
		return nil, none, ErrUnavailable
	}
	before := stamp(info)
	if trusted {
		after, err := f.Stat()
		if err != nil || before != stamp(after) {
			return nil, none, ErrUnavailable
		}
	}
	return raw, before, nil
}

// Reject duplicate object keys, including inside a radio record. The depth and
// input limits bound work; parsing never interprets configuration as instructions.
func uniqueJSON(d *json.Decoder, depth int) error {
	if depth > 64 {
		return ErrUnavailable
	}
	t, err := d.Token()
	if err != nil {
		return ErrUnavailable
	}
	switch t {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			t, err := d.Token()
			key, ok := t.(string)
			if err != nil || !ok || seen[key] {
				return ErrUnavailable
			}
			seen[key] = true
			if uniqueJSON(d, depth+1) != nil {
				return ErrUnavailable
			}
		}
		t, err = d.Token()
		if err != nil || t != json.Delim('}') {
			return ErrUnavailable
		}
	case json.Delim('['):
		for d.More() {
			if uniqueJSON(d, depth+1) != nil {
				return ErrUnavailable
			}
		}
		t, err = d.Token()
		if err != nil || t != json.Delim(']') {
			return ErrUnavailable
		}
	}
	return nil
}

func (a *authority) assignment(radio string) (recordEvidence, error) {
	var none recordEvidence
	raw, file, err := fileEvidence(a.records, 2<<20, true)
	if err != nil || !utf8.Valid(raw) || !json.Valid(raw) || uniqueJSON(json.NewDecoder(bytes.NewReader(raw)), 0) != nil {
		return none, ErrUnavailable
	}
	var root map[string]json.RawMessage
	var radios map[string]map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil || json.Unmarshal(root["radios"], &radios) != nil || radios == nil || len(radios) > 128 {
		return none, ErrUnavailable
	}
	owners := 0
	found := false
	for name, record := range radios {
		if !validRadio(name) || record == nil {
			return none, ErrUnavailable
		}
		owner := ""
		if value, exists := record["owner"]; exists && json.Unmarshal(value, &owner) != nil {
			return none, ErrUnavailable
		}
		if owner == "pat" {
			owners++
			found = found || name == radio
		}
	}
	if !found || owners != 1 {
		return none, ErrUnavailable
	}
	return recordEvidence{digest: sha256.Sum256(raw), file: file}, nil
}

func localAppsUID() (uint32, error) {
	raw, err := boundedFile("/etc/passwd", 128<<10, true)
	if err != nil {
		return 0, err
	}
	var uid uint32
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Split(line, ":")
		if fields[0] != "dxbapps" {
			continue
		}
		if found || len(fields) != 7 || !decimal(fields[2], 32, false) {
			return 0, ErrUnavailable
		}
		n, _ := strconv.ParseUint(fields[2], 10, 32)
		uid, found = uint32(n), true
	}
	if !found {
		return 0, ErrUnavailable
	}
	return uid, nil
}

// Keep the buffer named: embedding promotes ReadFrom and bypasses Write
// when os/exec copies stdout through io.Copy.
type boundedOutput struct{ buffer bytes.Buffer }

func (b *boundedOutput) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > 64<<10 {
		return 0, ErrUnavailable
	}
	return b.buffer.Write(p)
}

func metadataCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error {
		err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		if err == syscall.ESRCH {
			return os.ErrProcessDone
		}
		return err
	}
	cmd.WaitDelay = 200 * time.Millisecond
	var out boundedOutput
	cmd.Stdout, cmd.Stderr = &out, io.Discard
	if cmd.Run() != nil {
		return nil, ErrUnavailable
	}
	return out.buffer.Bytes(), nil
}

type unitState struct{ id, load, active, sub, main, invocation, group string }

func (a *authority) unit(ctx context.Context, radio string) (unitState, error) {
	var u unitState
	name := "dxberry-screen@pat-" + radio + ".service"
	raw, err := a.command(ctx, "/usr/bin/systemctl", "show", "--no-pager", "--property=Id,LoadState,ActiveState,SubState,MainPID,InvocationID,ControlGroup", "--", name)
	if err != nil || len(raw) == 0 || len(raw) > 64<<10 || raw[len(raw)-1] != '\n' || bytes.ContainsAny(raw, "\x00\r") {
		return u, ErrUnavailable
	}
	fields := map[string]*string{"Id": &u.id, "LoadState": &u.load, "ActiveState": &u.active, "SubState": &u.sub, "MainPID": &u.main, "InvocationID": &u.invocation, "ControlGroup": &u.group}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		field, known := fields[key]
		if !ok || !known || seen[key] {
			return unitState{}, ErrUnavailable
		}
		seen[key] = true
		*field = value
	}
	if len(seen) != len(fields) || u.id != name || u.load != "loaded" || u.active != "active" || u.sub != "running" || !decimal(u.main, 32, false) || len(u.invocation) != 32 || strings.Trim(u.invocation, "0123456789abcdef") != "" || u.invocation == strings.Repeat("0", 32) || !filepath.IsAbs(u.group) || filepath.Clean(u.group) != u.group || filepath.Base(u.group) != name {
		return unitState{}, ErrUnavailable
	}
	return u, nil
}

func (a *authority) processes(ctx context.Context, u unitState) ([]string, error) {
	var pids []string
	seen := map[string]bool{}
	dirs := 0
	err := filepath.WalkDir(filepath.Join(a.cgroups, u.group), func(path string, entry fs.DirEntry, err error) error {
		if err != nil || ctx.Err() != nil || entry.Type()&os.ModeSymlink != 0 {
			return ErrUnavailable
		}
		if !entry.IsDir() {
			return nil
		}
		dirs++
		if dirs > 64 {
			return ErrUnavailable
		}
		raw, err := boundedFile(filepath.Join(path, "cgroup.procs"), 16<<10, false)
		if err != nil {
			return err
		}
		for _, pid := range strings.Fields(string(raw)) {
			if !decimal(pid, 32, false) || seen[pid] || len(pids) >= 256 {
				return ErrUnavailable
			}
			seen[pid] = true
			pids = append(pids, pid)
		}
		return nil
	})
	if err != nil || !seen[u.main] {
		return nil, ErrUnavailable
	}
	return pids, nil
}

func processStart(raw []byte, pid string) (string, error) {
	end := bytes.LastIndexByte(raw, ')')
	if !bytes.HasPrefix(raw, []byte(pid+" (")) || end < 0 {
		return "", ErrUnavailable
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 20 || fields[0] == "Z" || fields[0] == "X" || !decimal(fields[19], 64, false) {
		return "", ErrUnavailable
	}
	return fields[19], nil
}

func (a *authority) process(pid string, uid uint32, group string) (string, error) {
	dir := filepath.Join(a.proc, pid)
	raw, err := boundedFile(filepath.Join(dir, "status"), 64<<10, false)
	if err != nil {
		return "", err
	}
	found := false
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		fields := strings.Fields(line)
		if found || len(fields) != 5 {
			return "", ErrUnavailable
		}
		found = true
		for _, field := range fields[1:] {
			n, err := strconv.ParseUint(field, 10, 32)
			if err != nil || n != uint64(uid) {
				return "", ErrUnavailable
			}
		}
	}
	if !found {
		return "", ErrUnavailable
	}
	raw, err = boundedFile(filepath.Join(dir, "cgroup"), 64<<10, false)
	if err != nil {
		return "", err
	}
	found = false
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "0::") {
			continue
		}
		path := strings.TrimPrefix(line, "0::")
		if found || !filepath.IsAbs(path) || filepath.Clean(path) != path || !(path == group || strings.HasPrefix(path, group+"/")) {
			return "", ErrUnavailable
		}
		found = true
	}
	if !found {
		return "", ErrUnavailable
	}
	raw, err = boundedFile(filepath.Join(dir, "stat"), 64<<10, false)
	if err != nil {
		return "", err
	}
	return processStart(raw, pid)
}

func (a *authority) listener(uid uint32) (string, error) {
	inode := ""
	for _, family := range []string{"tcp", "tcp6"} {
		path := filepath.Join(a.proc, "net", family)
		if family == "tcp6" {
			if _, err := os.Stat(path); errors.Is(err, os.ErrNotExist) {
				continue
			}
		}
		raw, err := boundedFile(path, 1<<20, false)
		if err != nil {
			return "", err
		}
		lines := strings.Split(strings.TrimSpace(string(raw)), "\n")
		if len(lines) == 0 || !strings.Contains(lines[0], "local_address") || len(lines) > 4097 {
			return "", ErrUnavailable
		}
		for _, line := range lines[1:] {
			if strings.TrimSpace(line) == "" {
				continue
			}
			f := strings.Fields(line)
			if len(f) < 10 {
				return "", ErrUnavailable
			}
			addr, port, ok := strings.Cut(f[1], ":")
			if !ok {
				return "", ErrUnavailable
			}
			if !strings.EqualFold(port, "1FF4") || f[3] != "0A" {
				continue
			}
			owner, err := strconv.ParseUint(f[7], 10, 32)
			if family != "tcp" || !strings.EqualFold(addr, "0100007F") || err != nil || owner != uint64(uid) || inode != "" || !decimal(f[9], 64, false) {
				return "", ErrUnavailable
			}
			inode = f[9]
		}
	}
	if inode == "" {
		return "", ErrUnavailable
	}
	return inode, nil
}

func (a *authority) ownsSocket(pid, inode string) bool {
	f, err := os.Open(filepath.Join(a.proc, pid, "fd"))
	if err != nil {
		return false
	}
	defer f.Close()
	entries, err := f.ReadDir(1025)
	if err != nil && err != io.EOF || len(entries) > 1024 {
		return false
	}
	for _, entry := range entries {
		if !decimal(entry.Name(), 32, true) {
			return false
		}
		target, err := os.Readlink(filepath.Join(a.proc, pid, "fd", entry.Name()))
		if errors.Is(err, os.ErrNotExist) {
			continue // an unrelated descriptor can close during enumeration
		}
		if err != nil {
			return false
		}
		if target == "socket:["+inode+"]" {
			return true
		}
	}
	return false
}

func (a *authority) probe(ctx context.Context, radio string) (instance, error) {
	var none instance
	if os.Geteuid() != 0 || !validRadio(radio) || ctx.Err() != nil {
		return none, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, time.Second)
	defer cancel()
	records, err := a.assignment(radio)
	if err != nil {
		return none, ErrUnavailable
	}
	uid, err := a.uid()
	if err != nil || uid == 0 {
		return none, ErrUnavailable
	}
	version, err := a.command(ctx, "/usr/bin/dpkg-query", "--show", "--showformat=${db:Status-Status} ${Version}\n", "--", "pat")
	if err != nil || string(version) != "installed 1.0.1\n" {
		return none, ErrUnavailable
	}
	u, err := a.unit(ctx, radio)
	if err != nil {
		return none, ErrUnavailable
	}
	exe, err := os.Stat(a.executable)
	if err != nil || !exe.Mode().IsRegular() || exe.Mode().Perm()&0022 != 0 {
		return none, ErrUnavailable
	}
	stat, ok := exe.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		return none, ErrUnavailable
	}
	pids, err := a.processes(ctx, u)
	if err != nil {
		return none, ErrUnavailable
	}
	pid, start := "", ""
	for _, candidate := range pids {
		if ctx.Err() != nil {
			return none, ErrUnavailable
		}
		info, err := os.Stat(filepath.Join(a.proc, candidate, "exe"))
		if err != nil {
			return none, ErrUnavailable
		}
		if !os.SameFile(info, exe) {
			continue
		}
		if pid != "" {
			return none, ErrUnavailable
		}
		pid = candidate
		start, err = a.process(pid, uid, u.group)
		if err != nil {
			return none, ErrUnavailable
		}
		raw, err := boundedFile(filepath.Join(a.proc, pid, "cmdline"), 64<<10, false)
		if err != nil || len(raw) == 0 || raw[len(raw)-1] != 0 {
			return none, ErrUnavailable
		}
		args := strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00")
		if len(args) != 4 || filepath.Base(args[0]) != "pat" || args[1] != "http" || args[2] != "--addr" || args[3] != "127.0.0.1:8180" {
			return none, ErrUnavailable
		}
	}
	if pid == "" {
		return none, ErrUnavailable
	}
	inode, err := a.listener(uid)
	if err != nil || !a.ownsSocket(pid, inode) {
		return none, ErrUnavailable
	}
	after, err := a.unit(ctx, radio)
	if err != nil || u != after {
		return none, ErrUnavailable
	}
	latest, err := a.assignment(radio)
	if err != nil || records != latest || ctx.Err() != nil {
		return none, ErrUnavailable
	}
	return instance{records: records, unit: u, pid: pid, start: start, socket: inode, uid: uid, executable: stamp(exe)}, nil
}
