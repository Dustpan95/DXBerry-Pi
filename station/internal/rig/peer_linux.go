package rig

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"io"
	"net/netip"
	"os"
	"strconv"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const peerReadLimit = 2 << 20

func peerOpenDir(parent int, name string) (*os.File, error) {
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_DIRECTORY, 0)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func peerRead(parent int, name string, limit int64) ([]byte, error) {
	fd, err := unix.Openat(parent, name, unix.O_RDONLY|unix.O_CLOEXEC|unix.O_NOFOLLOW|unix.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, ErrPeerUnavailable
	}
	b, err := io.ReadAll(io.LimitReader(f, limit+1))
	if err != nil || int64(len(b)) > limit {
		return nil, ErrPeerUnavailable
	}
	return b, nil
}

func peerLink(parent int, name string) (string, error) {
	buf := make([]byte, 4096)
	n, err := unix.Readlinkat(parent, name, buf)
	if err != nil || n == 0 || n == len(buf) {
		return "", ErrPeerUnavailable
	}
	return string(buf[:n]), nil
}

func peerDecimal(s string, bits int, zero bool) (uint64, bool) {
	n, err := strconv.ParseUint(s, 10, bits)
	return n, err == nil && (zero || n != 0) && strconv.FormatUint(n, 10) == s
}

func peerStart(raw []byte, pid int) (uint64, error) {
	end := bytes.LastIndexByte(raw, ')')
	if !bytes.HasPrefix(raw, []byte(strconv.Itoa(pid)+" (")) || end < 0 {
		return 0, ErrPeerUnavailable
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 20 || len(fields[0]) != 1 || !strings.Contains("RSDTtIKWP", fields[0]) {
		return 0, ErrPeerUnavailable
	}
	n, ok := peerDecimal(fields[19], 64, false)
	if !ok {
		return 0, ErrPeerUnavailable
	}
	return n, nil
}

func peerUID(raw []byte) (uint32, error) {
	found := false
	var uid uint64
	for _, line := range strings.Split(string(raw), "\n") {
		if !strings.HasPrefix(line, "Uid:") {
			continue
		}
		f := strings.Fields(line)
		if found || len(f) != 5 {
			return 0, ErrPeerUnavailable
		}
		found = true
		for i, value := range f[1:] {
			n, ok := peerDecimal(value, 32, true)
			if !ok || i != 0 && n != uid {
				return 0, ErrPeerUnavailable
			}
			uid = n
		}
	}
	if !found {
		return 0, ErrPeerUnavailable
	}
	return uint32(uid), nil
}

func peerProcessSnapshot(dir int, e Endpoint, pid int, netns string) (Peer, error) {
	var none Peer
	stat, err := peerRead(dir, "stat", 64<<10)
	if err != nil {
		return none, ErrPeerUnavailable
	}
	start, err := peerStart(stat, pid)
	if err != nil {
		return none, err
	}
	status, err := peerRead(dir, "status", 64<<10)
	if err != nil {
		return none, ErrPeerUnavailable
	}
	uid, err := peerUID(status)
	if err != nil {
		return none, err
	}
	ns, err := peerOpenDir(dir, "ns")
	if err != nil {
		return none, ErrPeerUnavailable
	}
	defer ns.Close()
	identity, err := peerLink(int(ns.Fd()), "net")
	if err != nil || identity != netns {
		return none, ErrPeerUnavailable
	}
	// Include executable metadata, argv and cgroup in the comparison without
	// treating their contents as a trusted configuration or publishing them.
	var exe unix.Stat_t
	if unix.Fstatat(dir, "exe", &exe, 0) != nil || exe.Mode&unix.S_IFMT != unix.S_IFREG {
		return none, ErrPeerUnavailable
	}
	args, err := peerRead(dir, "cmdline", 64<<10)
	if err != nil || len(args) == 0 || args[len(args)-1] != 0 {
		return none, ErrPeerUnavailable
	}
	group, err := peerRead(dir, "cgroup", 64<<10)
	if err != nil || len(group) == 0 {
		return none, ErrPeerUnavailable
	}
	h := sha256.New()
	fmt.Fprintf(h, "%d/%d/%d/%d/%d/%d/%d/%d/%d/%d/", exe.Dev, exe.Ino, exe.Mode, exe.Uid, exe.Gid, exe.Size, exe.Mtim.Sec, exe.Mtim.Nsec, exe.Ctim.Sec, exe.Ctim.Nsec)
	h.Write(args)
	h.Write([]byte{0}) // argv ends in NUL; cgroup contains no NUL on procfs
	h.Write(group)
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return Peer{endpoint: e, pid: pid, start: start, uid: uid, netns: identity, process: digest, arguments: sha256.Sum256(args)}, nil
}

type peerTCP struct {
	local, remote netip.AddrPort
	state         uint8
	uid           uint32
	inode         uint64
}

func peerAddress(raw string, ipv6 bool) (netip.AddrPort, error) {
	addr, port, ok := strings.Cut(raw, ":")
	if !ok || len(port) != 4 || (!ipv6 && len(addr) != 8) || (ipv6 && len(addr) != 32) {
		return netip.AddrPort{}, ErrPeerUnavailable
	}
	p, err := strconv.ParseUint(port, 16, 16)
	if err != nil {
		return netip.AddrPort{}, ErrPeerUnavailable
	}
	buf, err := hex.DecodeString(addr)
	if err != nil {
		return netip.AddrPort{}, ErrPeerUnavailable
	}
	// procfs presents each address word as a native-endian 32-bit integer.
	for i := 0; i < len(buf); i += 4 {
		n := binary.BigEndian.Uint32(buf[i : i+4])
		binary.NativeEndian.PutUint32(buf[i:i+4], n)
	}
	a, ok := netip.AddrFromSlice(buf)
	if !ok {
		return netip.AddrPort{}, ErrPeerUnavailable
	}
	return netip.AddrPortFrom(a, uint16(p)), nil
}

func peerTCPTable(raw []byte, ipv6 bool) ([]peerTCP, error) {
	if len(raw) == 0 || len(raw) > peerReadLimit || raw[len(raw)-1] != '\n' || bytes.IndexByte(raw, 0) >= 0 {
		return nil, ErrPeerUnavailable
	}
	lines := strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n")
	remoteHeader := "rem_address"
	if ipv6 {
		remoteHeader = "remote_address"
	}
	if len(lines) > 8193 || !strings.Contains(lines[0], "local_address") || !strings.Contains(lines[0], remoteHeader) {
		return nil, ErrPeerUnavailable
	}
	rows := make([]peerTCP, 0, len(lines)-1)
	for _, line := range lines[1:] {
		f := strings.Fields(line)
		if len(f) < 10 {
			return nil, ErrPeerUnavailable
		}
		local, err := peerAddress(f[1], ipv6)
		if err != nil {
			return nil, err
		}
		remote, err := peerAddress(f[2], ipv6)
		if err != nil {
			return nil, err
		}
		state, err := strconv.ParseUint(f[3], 16, 8)
		uid, uidOK := peerDecimal(f[7], 32, true)
		inode, inodeOK := peerDecimal(f[9], 64, true)
		if err != nil || len(f[3]) != 2 || !uidOK || !inodeOK {
			return nil, ErrPeerUnavailable
		}
		rows = append(rows, peerTCP{local, remote, uint8(state), uint32(uid), inode})
	}
	return rows, nil
}

func peerSelect(rows, ipv6 []peerTCP, e Endpoint, uid uint32) (uint64, uint64, error) {
	var listener, accepted uint64
	localCount := 0
	for _, row := range rows {
		switch {
		case row.state == 0x0a && row.local.Port() == e.Peer().Port():
			if listener != 0 || row.local != e.Peer() || row.remote.Port() != 0 || !row.remote.Addr().IsUnspecified() || row.uid != uid || row.inode == 0 {
				return 0, 0, ErrPeerUnavailable
			}
			listener = row.inode
		case row.local == e.Local() && row.remote == e.Peer():
			if row.state != 1 || row.inode != e.SocketInode() || row.uid != e.SocketUID() {
				return 0, 0, ErrPeerUnavailable
			}
			localCount++
		case row.local == e.Peer() && row.remote == e.Local():
			if row.state != 1 || row.inode == 0 || row.uid != uid || accepted != 0 {
				return 0, 0, ErrPeerUnavailable
			}
			accepted = row.inode
		}
	}
	for _, row := range ipv6 {
		if row.state == 0x0a && row.local.Port() == e.Peer().Port() {
			return 0, 0, ErrPeerUnavailable
		}
	}
	if localCount != 1 || listener == 0 || accepted == 0 || accepted == listener || accepted == e.SocketInode() {
		return 0, 0, ErrPeerUnavailable
	}
	return listener, accepted, nil
}

func peerOwnedFDs(ctx context.Context, dir int, listener, accepted uint64) (string, string, error) {
	fds, err := peerOpenDir(dir, "fd")
	if err != nil {
		return "", "", ErrPeerUnavailable
	}
	defer fds.Close()
	entries, err := fds.ReadDir(1025)
	if err != nil && err != io.EOF || len(entries) > 1024 {
		return "", "", ErrPeerUnavailable
	}
	var l, a string
	for _, entry := range entries {
		if ctx.Err() != nil {
			return "", "", ctx.Err()
		}
		if _, ok := peerDecimal(entry.Name(), 32, true); !ok {
			return "", "", ErrPeerUnavailable
		}
		target, err := peerLink(int(fds.Fd()), entry.Name())
		if err != nil {
			// Unrelated descriptors can close during enumeration; require both
			// target descriptors again after the second kernel-table read.
			continue
		}
		if target == fmt.Sprintf("socket:[%d]", listener) && (l == "" || entry.Name() < l) {
			l = entry.Name()
		}
		if target == fmt.Sprintf("socket:[%d]", accepted) && (a == "" || entry.Name() < a) {
			a = entry.Name()
		}
	}
	if l == "" || a == "" {
		return "", "", ErrPeerUnavailable
	}
	return l, a, nil
}

func inspectPeer(ctx context.Context, e Endpoint, pid int) (Peer, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	proc, err := peerOpenDir(unix.AT_FDCWD, "/proc")
	if err != nil {
		return Peer{}, ErrPeerUnavailable
	}
	defer proc.Close()
	var fs unix.Statfs_t
	if unix.Fstatfs(int(proc.Fd()), &fs) != nil || fs.Type != unix.PROC_SUPER_MAGIC {
		return Peer{}, ErrPeerUnavailable
	}
	self, err := peerOpenDir(int(proc.Fd()), strconv.Itoa(os.Getpid()))
	if err != nil {
		return Peer{}, ErrPeerUnavailable
	}
	defer self.Close()
	selfNS, err := peerOpenDir(int(self.Fd()), "ns")
	if err != nil {
		return Peer{}, ErrPeerUnavailable
	}
	defer selfNS.Close()
	netns, err := peerLink(int(selfNS.Fd()), "net")
	if err != nil {
		return Peer{}, ErrPeerUnavailable
	}
	dir, err := peerOpenDir(int(proc.Fd()), strconv.Itoa(pid))
	if err != nil {
		return Peer{}, ErrPeerUnavailable
	}
	defer dir.Close()
	before, err := peerProcessSnapshot(int(dir.Fd()), e, pid, netns)
	if err != nil {
		return Peer{}, err
	}
	for pass := 0; pass < 2; pass++ {
		if err := ctx.Err(); err != nil {
			return Peer{}, err
		}
		netDir, err := peerOpenDir(int(self.Fd()), "net")
		if err != nil {
			return Peer{}, ErrPeerUnavailable
		}
		var rows, ipv6 []peerTCP
		for _, table := range []string{"tcp", "tcp6"} {
			raw, err := peerRead(int(netDir.Fd()), table, peerReadLimit)
			if table == "tcp6" && os.IsNotExist(err) {
				continue
			}
			if err == nil {
				var parsed []peerTCP
				parsed, err = peerTCPTable(raw, table == "tcp6")
				if table == "tcp" {
					rows = parsed
				} else {
					ipv6 = parsed
				}
			}
			if err != nil {
				netDir.Close()
				return Peer{}, ErrPeerUnavailable
			}
		}
		netDir.Close()
		listener, accepted, err := peerSelect(rows, ipv6, e, before.uid)
		if err != nil {
			return Peer{}, err
		}
		l, a, err := peerOwnedFDs(ctx, int(dir.Fd()), listener, accepted)
		if err != nil {
			return Peer{}, err
		}
		current, err := peerProcessSnapshot(int(dir.Fd()), e, pid, netns)
		if err != nil {
			return Peer{}, err
		}
		current.listener, current.accepted, current.listenerFD, current.acceptedFD = listener, accepted, l, a
		if pass == 0 {
			withoutSockets := current
			withoutSockets.listener, withoutSockets.accepted, withoutSockets.listenerFD, withoutSockets.acceptedFD = 0, 0, "", ""
			if withoutSockets != before {
				return Peer{}, ErrPeerUnavailable
			}
		} else if current != before {
			return Peer{}, ErrPeerUnavailable
		}
		before = current
	}
	afterNS, err := peerLink(int(selfNS.Fd()), "net")
	if err != nil || afterNS != netns || !e.Current() {
		return Peer{}, ErrPeerUnavailable
	}
	if err := ctx.Err(); err != nil {
		return Peer{}, err
	}
	return before, nil
}
