package rig

import (
	"bytes"
	"context"
	"crypto/sha256"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"strconv"
	"strings"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

const serviceProperties = "Id,LoadState,ActiveState,SubState,MainPID,InvocationID,ControlGroup"

func newServiceReader() *ServiceReader {
	return &ServiceReader{initialized: true, proc: "/proc", cgroups: "/sys/fs/cgroup", executable: "/usr/bin/rigctld", command: serviceMetadataCommand, peer: func(ctx context.Context, e Endpoint, pid int) (Peer, error) { return e.InspectPeer(ctx, pid) }}
}

// Keep the buffer named: embedding promotes ReadFrom and bypasses Write
// when os/exec copies stdout through io.Copy.
type serviceOutput struct{ buffer bytes.Buffer }

func (b *serviceOutput) Write(p []byte) (int, error) {
	if b.buffer.Len()+len(p) > 64<<10 {
		return 0, ErrServiceUnavailable
	}
	return b.buffer.Write(p)
}

func serviceMetadataCommand(ctx context.Context, name string, args ...string) ([]byte, error) {
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
	var out serviceOutput
	cmd.Stdout, cmd.Stderr = &out, io.Discard
	if cmd.Run() != nil {
		return nil, ErrServiceUnavailable
	}
	return out.buffer.Bytes(), nil
}

func parseServiceUnit(raw []byte, radio string) (serviceUnit, error) {
	var u serviceUnit
	if !serviceRadioName(radio) || len(raw) == 0 || len(raw) > 64<<10 || raw[len(raw)-1] != '\n' || bytes.ContainsAny(raw, "\x00\r") {
		return u, ErrServiceUnavailable
	}
	fields := map[string]*string{"Id": &u.id, "LoadState": &u.load, "ActiveState": &u.active, "SubState": &u.sub, "MainPID": &u.main, "InvocationID": &u.invocation, "ControlGroup": &u.group}
	seen := map[string]bool{}
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		field, known := fields[key]
		if !ok || !known || seen[key] {
			return serviceUnit{}, ErrServiceUnavailable
		}
		seen[key] = true
		*field = value
	}
	name := "rigctld@" + radio + ".service"
	_, pidOK := peerDecimal(u.main, 32, false)
	if len(seen) != len(fields) || u.id != name || u.load != "loaded" || u.active != "active" || u.sub != "running" || !pidOK || len(u.invocation) != 32 || strings.Trim(u.invocation, "0123456789abcdef") != "" || u.invocation == strings.Repeat("0", 32) || len(u.group) > 4096 || strings.IndexFunc(u.group, func(c rune) bool { return c < 0x21 || c > 0x7e }) >= 0 || !strings.HasPrefix(u.group, "/system.slice/") || path.Clean(u.group) != u.group || path.Base(u.group) != name {
		return serviceUnit{}, ErrServiceUnavailable
	}
	return u, nil
}

func (r *ServiceReader) unit(ctx context.Context, radio string) (serviceUnit, error) {
	raw, err := r.command(ctx, "/usr/bin/systemctl", "show", "--no-pager", "--property="+serviceProperties, "--", "rigctld@"+radio+".service")
	if err != nil || ctx.Err() != nil {
		return serviceUnit{}, ErrServiceUnavailable
	}
	return parseServiceUnit(raw, radio)
}

// All path components must be root-controlled directories. A sticky root-owned
// ancestor supports a private temporary fixture; the final directory never
// permits group/other writes. No symlink component is followed.
func serviceDirectory(p string) (*os.File, error) {
	if !path.IsAbs(p) || path.Clean(p) != p {
		return nil, ErrServiceUnavailable
	}
	dir, err := peerOpenDir(unix.AT_FDCWD, "/")
	if err != nil {
		return nil, ErrServiceUnavailable
	}
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i := 0; ; i++ {
		var st unix.Stat_t
		if unix.Fstat(int(dir.Fd()), &st) != nil || st.Uid != 0 || st.Mode&0022 != 0 && (p == "/" || i == len(parts) || st.Mode&unix.S_ISVTX == 0) {
			dir.Close()
			return nil, ErrServiceUnavailable
		}
		if p == "/" || i == len(parts) {
			return dir, nil
		}
		next, err := peerOpenDir(int(dir.Fd()), parts[i])
		dir.Close()
		if err != nil {
			return nil, ErrServiceUnavailable
		}
		dir = next
	}
}

type serviceStamp struct {
	dev, ino          uint64
	mode, uid, gid    uint32
	size              int64
	mtimeSec, mtimeNS int64
	ctimeSec, ctimeNS int64
}

func serviceStat(st unix.Stat_t) serviceStamp {
	return serviceStamp{uint64(st.Dev), uint64(st.Ino), st.Mode, st.Uid, st.Gid, st.Size, st.Mtim.Sec, st.Mtim.Nsec, st.Ctim.Sec, st.Ctim.Nsec}
}

func serviceOpenFile(dir int, name string) (*os.File, serviceStamp, error) {
	fd, err := unix.Openat(dir, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, serviceStamp{}, ErrServiceUnavailable
	}
	f := os.NewFile(uintptr(fd), name)
	var st unix.Stat_t
	if unix.Fstat(fd, &st) != nil || st.Mode&unix.S_IFMT != unix.S_IFREG || st.Uid != 0 || st.Mode&0022 != 0 || st.Nlink != 1 {
		f.Close()
		return nil, serviceStamp{}, ErrServiceUnavailable
	}
	return f, serviceStat(st), nil
}

func serviceFile(dir int, name string, limit int64) ([]byte, error) {
	f, before, err := serviceOpenFile(dir, name)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, limit+1))
	var after unix.Stat_t
	if err != nil || int64(len(raw)) > limit || unix.Fstat(int(f.Fd()), &after) != nil || serviceStat(after) != before {
		return nil, ErrServiceUnavailable
	}
	return raw, nil
}

func serviceExecutable(p string) (serviceStamp, error) {
	if !path.IsAbs(p) || path.Clean(p) != p {
		return serviceStamp{}, ErrServiceUnavailable
	}
	dir, err := serviceDirectory(path.Dir(p))
	if err != nil {
		return serviceStamp{}, err
	}
	defer dir.Close()
	f, stamp, err := serviceOpenFile(int(dir.Fd()), path.Base(p))
	if err != nil {
		return serviceStamp{}, err
	}
	f.Close()
	if stamp.mode&0111 == 0 {
		return serviceStamp{}, ErrServiceUnavailable
	}
	return stamp, nil
}

func serviceMember(raw []byte, pid string) bool {
	if len(raw) == 0 || len(raw) > 16<<10 || raw[len(raw)-1] != '\n' {
		return false
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	seen := map[string]bool{}
	for _, line := range lines {
		if _, ok := peerDecimal(line, 32, false); !ok || seen[line] || len(seen) >= 256 {
			return false
		}
		seen[line] = true
	}
	return seen[pid]
}

func (r *ServiceReader) process(ctx context.Context, u serviceUnit, p Peer, executable serviceStamp) ([32]byte, error) {
	var none [32]byte
	if ctx.Err() != nil {
		return none, ErrServiceUnavailable
	}
	dir, err := serviceDirectory(r.proc + "/" + u.main)
	if err != nil {
		return none, err
	}
	defer dir.Close()
	observed, err := peerProcessSnapshot(int(dir.Fd()), p.endpoint, p.pid, p.netns)
	base := p
	base.listener, base.accepted, base.listenerFD, base.acceptedFD = 0, 0, "", ""
	if err != nil || observed != base || p.uid != 0 {
		return none, ErrServiceUnavailable
	}
	var exe unix.Stat_t
	if unix.Fstatat(int(dir.Fd()), "exe", &exe, 0) != nil || serviceStat(exe) != executable {
		return none, ErrServiceUnavailable
	}
	raw, err := peerRead(int(dir.Fd()), "cgroup", 64<<10)
	if err != nil || string(raw) != "0::"+u.group+"\n" {
		return none, ErrServiceUnavailable
	}
	group, err := serviceDirectory(r.cgroups + u.group)
	if err != nil {
		return none, err
	}
	defer group.Close()
	kind, err := serviceFile(int(group.Fd()), "cgroup.type", 64)
	if err != nil || string(kind) != "domain\n" {
		return none, ErrServiceUnavailable
	}
	members, err := serviceFile(int(group.Fd()), "cgroup.procs", 16<<10)
	if err != nil || !serviceMember(members, u.main) {
		return none, ErrServiceUnavailable
	}
	var gd, pd unix.Stat_t
	if unix.Fstat(int(group.Fd()), &gd) != nil || unix.Fstat(int(dir.Fd()), &pd) != nil || ctx.Err() != nil {
		return none, ErrServiceUnavailable
	}
	h := sha256.New()
	fmt.Fprintf(h, "%d/%d/%d/%d/", gd.Dev, gd.Ino, pd.Dev, pd.Ino)
	h.Write(p.process[:])
	h.Write(members)
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest, nil
}

func readService(ctx context.Context, r *ServiceReader, radio string, e Endpoint) (ServiceObservation, error) {
	var none ServiceObservation
	u, err := r.unit(ctx, radio)
	if err != nil {
		return none, err
	}
	pid, _ := strconv.ParseUint(u.main, 10, 32)
	peer, err := r.peer(ctx, e, int(pid))
	if err != nil || peer.pid != int(pid) || peer.uid != 0 || peer.endpoint != e || !peer.Same(peer) {
		return none, ErrServiceUnavailable
	}
	executable, err := serviceExecutable(r.executable)
	if err != nil {
		return none, err
	}
	process, err := r.process(ctx, u, peer, executable)
	if err != nil {
		return none, err
	}
	after, err := r.unit(ctx, radio)
	if err != nil || after != u {
		return none, ErrServiceUnavailable
	}
	newExecutable, err := serviceExecutable(r.executable)
	if err != nil || newExecutable != executable {
		return none, ErrServiceUnavailable
	}
	newPeer, err := r.peer(ctx, e, int(pid))
	if err != nil || !peer.Same(newPeer) {
		return none, ErrServiceUnavailable
	}
	newProcess, err := r.process(ctx, u, peer, executable)
	if err != nil || newProcess != process || !e.Current() || ctx.Err() != nil {
		return none, ErrServiceUnavailable
	}
	return ServiceObservation{r, radio, u, peer, process}, nil
}
