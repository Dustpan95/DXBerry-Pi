package rig

import (
	"net"
	"syscall"
)

func connectionSocketIdentity(conn net.Conn) (socketIdentity, bool) {
	provider, ok := conn.(syscall.Conn)
	if !ok {
		return socketIdentity{}, false
	}
	raw, err := provider.SyscallConn()
	if err != nil {
		return socketIdentity{}, false
	}
	var stat syscall.Stat_t
	var statErr error
	// Control keeps the existing descriptor valid for this read-only fstat.
	// Conn.File would duplicate it and change its runtime I/O behavior.
	err = raw.Control(func(fd uintptr) { statErr = syscall.Fstat(int(fd), &stat) })
	if err != nil || statErr != nil || stat.Mode&syscall.S_IFMT != syscall.S_IFSOCK || stat.Ino == 0 {
		return socketIdentity{}, false
	}
	return socketIdentity{uint64(stat.Dev), uint64(stat.Ino), stat.Uid}, true
}
