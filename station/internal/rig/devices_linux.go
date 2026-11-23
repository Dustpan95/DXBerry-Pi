package rig

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
	"golang.org/x/sys/unix"
)

var ErrDeviceUnavailable = errors.New("rig device identity unavailable")

// DeviceReader is a lazy, read-only Linux metadata reader. Production roots are
// fixed. It never opens a character device, duplicates its descriptor or uses
// ioctl, and does not probe a radio or change a unit, GPIO line or USB setting.
type DeviceReader struct {
	initialized bool
	dev, sys    string
	stat        deviceStatter
}

func NewDeviceReader() *DeviceReader {
	return &DeviceReader{initialized: true, dev: "/dev", sys: "/sys", stat: statDeviceAt}
}

type deviceStatter func(int, string, int) (deviceStamp, error)

type deviceStamp struct {
	dev, inode, rdev, links uint64
	mode, uid, gid          uint32
	ctimeSec, ctimeNS       int64
}

func deviceStat(st unix.Stat_t) deviceStamp {
	return deviceStamp{uint64(st.Dev), st.Ino, uint64(st.Rdev), uint64(st.Nlink), st.Mode, st.Uid, st.Gid, st.Ctim.Sec, st.Ctim.Nsec}
}

type deviceBinding struct {
	used                                      bool
	kernel, sysPath, descriptor               string
	flags                                     uint64
	parent, link, node, sysLink, sysNode, usb deviceStamp
}

// DeviceObservation is private snapshot evidence, not hardware/RF acceptance
// or an operation permit. Same requires two new successful Read calls; it does
// not re-read live state or extend a preview token's lifetime.
type DeviceObservation struct {
	reader  *DeviceReader
	service ServiceObservation
	command radios.RigCommand
	devices [3]deviceBinding
}

func (o DeviceObservation) Same(other DeviceObservation) bool {
	return o.reader != nil && o == other && o.service.Same(other.service)
}

func nativeDeviceCommand(command radios.RigCommand) bool {
	args := command.Arguments()
	_, cat := command.CAT()
	if !cat || len(args) < 9 || args[2] == "1" || args[2] == "2" || args[2] == "4" || args[7] != "-r" || args[8] != "/dev/dxberry/"+command.RadioName()+"-cat" {
		return false // dummy, proxy and implicit/default-device routes are unknown
	}
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-P" && args[i+1] == "GPIO" {
			return false // this reader does not establish GPIO resource identity
		}
	}
	return true
}

func (r *DeviceReader) Read(ctx context.Context, service ServiceObservation, command radios.RigCommand) (DeviceObservation, error) {
	if r == nil || !r.initialized || r.stat == nil || ctx == nil || os.Geteuid() != 0 || !service.MatchesCommand(command) || !nativeDeviceCommand(command) {
		return DeviceObservation{}, ErrDeviceUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	result, err := r.read(ctx, service, command)
	if ctx.Err() != nil {
		return DeviceObservation{}, ctx.Err()
	}
	if err != nil {
		return DeviceObservation{}, ErrDeviceUnavailable
	}
	return result, nil
}

func (r *DeviceReader) read(ctx context.Context, service ServiceObservation, command radios.RigCommand) (DeviceObservation, error) {
	var none DeviceObservation
	if ctx.Err() != nil {
		return none, ctx.Err()
	}
	fresh, err := service.reader.Read(ctx, service.radio, service.peer.endpoint)
	if err != nil || !service.Same(fresh) {
		return none, ErrDeviceUnavailable
	}
	pins := [3]radios.ControlPin{}
	used := [3]bool{}
	pins[0], used[0] = command.CAT()
	pins[1], used[1] = command.SerialPTT()
	pins[2], used[2] = command.HID()
	roles := [3]string{"cat", "ptt", "hid"}
	for i, pin := range pins {
		if !used[i] {
			continue
		}
		_, idKnown := pin.USBID()
		_, serialKnown := pin.Serial()
		if !idKnown || !serialKnown {
			return none, ErrDeviceUnavailable
		}
	}
	var before [3]deviceBinding
	for pass := 0; pass < 2; pass++ {
		if ctx.Err() != nil {
			return none, ctx.Err()
		}
		if pass == 1 {
			after, err := service.reader.Read(ctx, service.radio, service.peer.endpoint)
			if err != nil || !service.Same(after) {
				return none, ErrDeviceUnavailable
			}
		}
		process, err := serviceDirectory(service.reader.proc + "/" + service.unit.main)
		if err != nil {
			return none, err
		}
		var current [3]deviceBinding
		if !deviceProcessCurrent(int(process.Fd()), service.peer) {
			process.Close()
			return none, ErrDeviceUnavailable
		}
		for i, pin := range pins {
			if !used[i] {
				continue
			}
			binding, err := r.binding(ctx, int(process.Fd()), command.RadioName(), roles[i], pin)
			if err != nil {
				process.Close()
				return none, err
			}
			current[i] = binding
		}
		if !deviceProcessCurrent(int(process.Fd()), service.peer) {
			process.Close()
			return none, ErrDeviceUnavailable
		}
		process.Close()
		if pass != 0 && before != current {
			return none, ErrDeviceUnavailable
		}
		before = current
	}
	if !service.peer.endpoint.Current() || ctx.Err() != nil {
		return none, ErrDeviceUnavailable
	}
	return DeviceObservation{r, service, command, before}, nil
}

func deviceProcessCurrent(process int, peer Peer) bool {
	observed, err := peerProcessSnapshot(process, peer.endpoint, peer.pid, peer.netns)
	peer.listener, peer.accepted, peer.listenerFD, peer.acceptedFD = 0, 0, "", ""
	return err == nil && observed == peer
}

var serialDeviceName = regexp.MustCompile(`^tty(USB|ACM)[0-9]+$`)
var hidDeviceName = regexp.MustCompile(`^hidraw[0-9]+$`)
var usbInterfaceName = regexp.MustCompile(`^([0-9]+)-([0-9]+(?:\.[0-9]+)*):([0-9]+)\.([0-9]+)$`)

func statDeviceAt(dir int, name string, flags int) (deviceStamp, error) {
	var st unix.Stat_t
	if unix.Fstatat(dir, name, &st, flags) != nil {
		return deviceStamp{}, ErrDeviceUnavailable
	}
	return deviceStat(st), nil
}

func stampDirectory(dir *os.File) (deviceStamp, error) {
	var st unix.Stat_t
	if unix.Fstat(int(dir.Fd()), &st) != nil {
		return deviceStamp{}, ErrDeviceUnavailable
	}
	return deviceStat(st), nil
}

func (r *DeviceReader) binding(ctx context.Context, process int, radio, role string, pin radios.ControlPin) (deviceBinding, error) {
	var none deviceBinding
	stable, err := serviceDirectory(r.dev + "/dxberry")
	if err != nil {
		return none, err
	}
	defer stable.Close()
	parent, err := stampDirectory(stable)
	if err != nil {
		return none, err
	}
	name := radio + "-" + role
	link, err := statDeviceAt(int(stable.Fd()), name, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil || link.mode&unix.S_IFMT != unix.S_IFLNK || link.uid != 0 || link.links != 1 {
		return none, ErrDeviceUnavailable
	}
	target, err := peerLink(int(stable.Fd()), name)
	if err != nil {
		return none, ErrDeviceUnavailable
	}
	kernelTarget := strings.TrimPrefix(target, "../")
	if kernelTarget == target {
		kernelTarget = strings.TrimPrefix(target, r.dev+"/")
	}
	if kernelTarget == target || path.Base(kernelTarget) != kernelTarget {
		return none, ErrDeviceUnavailable
	}
	resolved := r.dev + "/" + kernelTarget
	kernel := path.Base(resolved)
	if role == "hid" && !hidDeviceName.MatchString(kernel) || role != "hid" && !serialDeviceName.MatchString(kernel) {
		return none, ErrDeviceUnavailable
	}
	dev, err := serviceDirectory(r.dev)
	if err != nil {
		return none, err
	}
	defer dev.Close()
	node, err := r.stat(int(dev.Fd()), kernel, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil || node.mode&unix.S_IFMT != unix.S_IFCHR || node.uid != 0 || node.links != 1 || node.mode&0002 != 0 {
		return none, ErrDeviceUnavailable
	}
	sysPath, sysLink, sysNode, usb, err := r.usb(pin, node, kernel)
	if err != nil || sysPath == "" || sysLink.inode == 0 || sysNode.inode == 0 || usb.inode == 0 {
		return none, ErrDeviceUnavailable
	}
	fd, flags, err := ownedDeviceFDWithStat(ctx, process, node, resolved, role == "cat", r.stat)
	if err != nil {
		return none, err
	}
	return deviceBinding{true, kernel, sysPath, fd, flags, parent, link, node, sysLink, sysNode, usb}, nil
}

func usbContext(sysPath string) (string, string, bool) {
	parts := strings.Split(sysPath, "/")
	for i := len(parts) - 1; i >= 0; i-- {
		m := usbInterfaceName.FindStringSubmatch(parts[i])
		if m == nil {
			continue
		}
		device := m[1] + "-" + m[2]
		for j := i - 1; j >= 0; j-- {
			if parts[j] == device {
				return "usb-0:" + m[2] + ":" + m[3] + "." + m[4], strings.Join(parts[:j+1], "/"), true
			}
		}
		return "", "", false
	}
	return "", "", false
}

func (r *DeviceReader) usb(pin radios.ControlPin, node deviceStamp, kernel string) (string, deviceStamp, deviceStamp, deviceStamp, error) {
	var empty deviceStamp
	char, err := serviceDirectory(r.sys + "/dev/char")
	if err != nil {
		return "", empty, empty, empty, err
	}
	defer char.Close()
	number := fmt.Sprintf("%d:%d", unix.Major(node.rdev), unix.Minor(node.rdev))
	link, err := statDeviceAt(int(char.Fd()), number, unix.AT_SYMLINK_NOFOLLOW)
	if err != nil || link.mode&unix.S_IFMT != unix.S_IFLNK || link.uid != 0 || link.links != 1 {
		return "", empty, empty, empty, ErrDeviceUnavailable
	}
	target, err := peerLink(int(char.Fd()), number)
	if err != nil || path.Clean(target) != target {
		return "", empty, empty, empty, ErrDeviceUnavailable
	}
	resolved := target
	if !path.IsAbs(resolved) {
		resolved = path.Join(r.sys, "dev/char", resolved)
	}
	if path.Clean(resolved) != resolved || !strings.HasPrefix(resolved, r.sys+"/devices/") || path.Base(resolved) != kernel {
		return "", empty, empty, empty, ErrDeviceUnavailable
	}
	function, usbPath, ok := usbContext(resolved)
	if !ok || function != pin.Path() {
		return "", empty, empty, empty, ErrDeviceUnavailable
	}
	sysNode, err := serviceDirectory(resolved)
	if err != nil {
		return "", empty, empty, empty, err
	}
	defer sysNode.Close()
	dev, err := serviceFile(int(sysNode.Fd()), "dev", 64)
	if err != nil || string(dev) != number+"\n" {
		return "", empty, empty, empty, ErrDeviceUnavailable
	}
	usb, err := serviceDirectory(usbPath)
	if err != nil {
		return "", empty, empty, empty, err
	}
	defer usb.Close()
	vendor, err := serviceFile(int(usb.Fd()), "idVendor", 16)
	if err != nil {
		return "", empty, empty, empty, err
	}
	product, err := serviceFile(int(usb.Fd()), "idProduct", 16)
	if err != nil {
		return "", empty, empty, empty, err
	}
	id, known := pin.USBID()
	if !known || len(vendor) != 5 || len(product) != 5 || vendor[4] != '\n' || product[4] != '\n' || string(vendor[:4])+":"+string(product[:4]) != id {
		return "", empty, empty, empty, ErrDeviceUnavailable
	}
	var serial []byte
	var serialStat unix.Stat_t
	serialErr := unix.Fstatat(int(usb.Fd()), "serial", &serialStat, unix.AT_SYMLINK_NOFOLLOW)
	switch serialErr {
	case unix.ENOENT: // no serial attribute is a known empty device serial
	case nil:
		serial, err = serviceFile(int(usb.Fd()), "serial", 513)
		if err != nil || len(serial) == 0 || serial[len(serial)-1] != '\n' {
			return "", empty, empty, empty, ErrDeviceUnavailable
		}
	default:
		return "", empty, empty, empty, ErrDeviceUnavailable
	}
	want, known := pin.Serial()
	if !known || strings.TrimSuffix(string(serial), "\n") != want {
		return "", empty, empty, empty, ErrDeviceUnavailable
	}
	nodeStamp, err := stampDirectory(sysNode)
	if err != nil {
		return "", empty, empty, empty, err
	}
	usbStamp, err := stampDirectory(usb)
	if err != nil {
		return "", empty, empty, empty, err
	}
	return resolved, link, nodeStamp, usbStamp, nil
}

func descriptorFlags(raw []byte, inode uint64, readWrite bool) (uint64, bool) {
	if len(raw) == 0 || len(raw) > 4096 || raw[len(raw)-1] != '\n' {
		return 0, false
	}
	var flags uint64
	foundFlags, foundInode := false, false
	for _, line := range strings.Split(strings.TrimSuffix(string(raw), "\n"), "\n") {
		key, value, ok := strings.Cut(line, ":")
		if !ok {
			return 0, false
		}
		switch key {
		case "flags":
			if foundFlags {
				return 0, false
			}
			foundFlags = true
			var err error
			flags, err = strconv.ParseUint(strings.TrimSpace(value), 8, 32)
			if err != nil {
				return 0, false
			}
		case "ino":
			if foundInode {
				return 0, false
			}
			foundInode = true
			actual, ok := peerDecimal(strings.TrimSpace(value), 64, false)
			if !ok || actual != inode {
				return 0, false
			}
		}
	}
	access := flags & unix.O_ACCMODE
	if !foundFlags || !foundInode || flags&unix.O_PATH != 0 || readWrite && access != unix.O_RDWR || !readWrite && access != unix.O_RDWR && access != unix.O_WRONLY {
		return 0, false
	}
	return flags, true
}

func ownedDeviceFD(ctx context.Context, process int, expected deviceStamp, nodePath string, readWrite bool) (string, uint64, error) {
	return ownedDeviceFDWithStat(ctx, process, expected, nodePath, readWrite, statDeviceAt)
}

func ownedDeviceFDWithStat(ctx context.Context, process int, expected deviceStamp, nodePath string, readWrite bool, stat deviceStatter) (string, uint64, error) {
	fds, err := peerOpenDir(process, "fd")
	if err != nil {
		return "", 0, ErrDeviceUnavailable
	}
	defer fds.Close()
	info, err := peerOpenDir(process, "fdinfo")
	if err != nil {
		return "", 0, ErrDeviceUnavailable
	}
	defer info.Close()
	entries, err := fds.ReadDir(1025)
	if err != nil && err != io.EOF || len(entries) > 1024 {
		return "", 0, ErrDeviceUnavailable
	}
	var chosen string
	var chosenFlags uint64
	for _, entry := range entries {
		if ctx.Err() != nil {
			return "", 0, ctx.Err()
		}
		if _, ok := peerDecimal(entry.Name(), 32, true); !ok {
			return "", 0, ErrDeviceUnavailable
		}
		actual, err := stat(int(fds.Fd()), entry.Name(), 0)
		if err != nil || actual != expected {
			continue
		}
		target, err := peerLink(int(fds.Fd()), entry.Name())
		if err != nil || target != nodePath {
			continue
		}
		raw, err := peerRead(int(info.Fd()), entry.Name(), 4096)
		if err != nil {
			continue
		}
		flags, ok := descriptorFlags(raw, expected.inode, readWrite)
		if !ok {
			continue
		}
		if chosen == "" || entry.Name() < chosen {
			chosen, chosenFlags = entry.Name(), flags
		}
	}
	if chosen == "" {
		return "", 0, ErrDeviceUnavailable
	}
	return chosen, chosenFlags, nil
}
