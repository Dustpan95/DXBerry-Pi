package rig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
	"github.com/creack/pty"
	"golang.org/x/sys/unix"
)

type deviceFixture struct {
	f                  *serviceFixture
	r                  *DeviceReader
	service            ServiceObservation
	node, usb, sysNode string
	numbers            map[uint64]uint64
}

func newDeviceFixture(t *testing.T) *deviceFixture {
	t.Helper()
	f := newServiceFixture(t)
	server := newFakeAt(t, "127.0.0.1:4532", freqHandler)
	client := fastClient(t, server.addr())
	waitConnected(t, client)
	var ok bool
	f.e, ok = client.ConnectionEndpoint()
	if !ok {
		t.Fatal("no endpoint")
	}
	command := serviceCommandFixture(t, "hf", 4532)
	refreshServiceCommandPeer(t, f, command.Arguments())
	service, err := f.r.Read(ctx5(t), "hf", f.e)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	r := NewDeviceReader()
	r.dev, r.sys = root+"/dev", root+"/sys"
	node := r.dev + "/ttyUSB0"
	for _, dir := range []string{r.dev + "/dxberry", r.sys + "/dev/char", f.process + "/fd", f.process + "/fdinfo"} {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
	}
	// Root-owned regular files stand in for device metadata because this
	// Docker environment forbids mknod. The production constructor always
	// uses kernel stat; actual descriptor matching has a separate PTY test.
	if err := os.WriteFile(node, nil, 0660); err != nil {
		t.Fatal(err)
	}
	numbers := map[uint64]uint64{}
	var nodeStat unix.Stat_t
	if err := unix.Stat(node, &nodeStat); err != nil {
		t.Fatal(err)
	}
	numbers[nodeStat.Ino] = unix.Mkdev(1, 3)
	r.stat = func(dir int, name string, flags int) (deviceStamp, error) {
		st, err := statDeviceAt(dir, name, flags)
		if number, ok := numbers[st.inode]; err == nil && ok && st.mode&unix.S_IFMT == unix.S_IFREG {
			st.mode = (st.mode &^ unix.S_IFMT) | unix.S_IFCHR
			st.rdev = number
		}
		return st, err
	}
	if err := os.Symlink("../ttyUSB0", r.dev+"/dxberry/hf-cat"); err != nil {
		t.Fatal(err)
	}
	usb := r.sys + "/devices/platform/usb1/1-1/1-1.3"
	sysNode := usb + "/1-1.3:1.0/ttyUSB0/tty/ttyUSB0"
	serviceFixtureWrite(t, sysNode+"/dev", "1:3\n")
	serviceFixtureWrite(t, usb+"/idVendor", "1234\n")
	serviceFixtureWrite(t, usb+"/idProduct", "abcd\n")
	serviceFixtureWrite(t, usb+"/serial", "example\n")
	rel, err := filepath.Rel(r.sys+"/dev/char", sysNode)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel, r.sys+"/dev/char/1:3"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(node, f.process+"/fd/6"); err != nil {
		t.Fatal(err)
	}
	var st unix.Stat_t
	if err := unix.Stat(node, &st); err != nil {
		t.Fatal(err)
	}
	serviceFixtureWrite(t, f.process+"/fdinfo/6", fmt.Sprintf("pos:\t0\nflags:\t0100002\nmnt_id:\t1\nino:\t%d\n", st.Ino))
	return &deviceFixture{f, r, service, node, usb, sysNode, numbers}
}

func TestDeviceReaderBindsSyntheticNodeSysfsAndDescriptorWithoutOpeningDevice(t *testing.T) {
	f := newDeviceFixture(t)
	command := serviceCommandFixture(t, "hf", 4532)
	before, err := f.r.Read(ctx5(t), f.service, command)
	if err != nil {
		t.Fatal(err)
	}
	after, err := f.r.Read(ctx5(t), f.service, command)
	if err != nil || !before.Same(after) || (DeviceObservation{}).Same(DeviceObservation{}) {
		t.Fatal("unchanged metadata identity", err)
	}
	for _, value := range []any{before, after} {
		raw, err := json.Marshal(value)
		if err != nil || string(raw) != "{}" {
			t.Fatal("private device evidence became wire data", string(raw), err)
		}
	}
	if !before.devices[0].used || before.devices[0].descriptor != "6" || before.devices[1].used || before.devices[2].used {
		t.Fatal("incorrect bound descriptor set")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.r.Read(ctx, f.service, command); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	for _, reader := range []*DeviceReader{nil, {}} {
		if _, err := reader.Read(ctx5(t), f.service, command); !errors.Is(err, ErrDeviceUnavailable) {
			t.Fatal("zero reader accepted", err)
		}
	}
	//lint:ignore SA1012 Deliberately verify that a nil context is refused.
	if _, err := f.r.Read(nil, f.service, command); !errors.Is(err, ErrDeviceUnavailable) {
		t.Fatal(err)
	}
	// Removing the device-side identity refuses the next read; no prior success
	// is retained. All unit/process/sysfs/fd metadata here is synthetic.
	if err := os.Remove(f.node); err != nil {
		t.Fatal(err)
	}
	view, err := f.r.Read(ctx5(t), f.service, command)
	if !errors.Is(err, ErrDeviceUnavailable) || view != (DeviceObservation{}) {
		t.Fatal("missing node returned stale proof", err)
	}
}

func TestDeviceReaderRefusesMismatchedOrUntrustedMetadata(t *testing.T) {
	cases := map[string]func(*testing.T, *deviceFixture){
		"USB vendor": func(t *testing.T, f *deviceFixture) { serviceFixtureWrite(t, f.usb+"/idVendor", "5678\n") },
		"USB serial": func(t *testing.T, f *deviceFixture) { serviceFixtureWrite(t, f.usb+"/serial", "other\n") },
		"missing serial": func(t *testing.T, f *deviceFixture) {
			if err := os.Remove(f.usb + "/serial"); err != nil {
				t.Fatal(err)
			}
		},
		"malformed serial": func(t *testing.T, f *deviceFixture) { serviceFixtureWrite(t, f.usb+"/serial", "example") },
		"oversized serial": func(t *testing.T, f *deviceFixture) {
			serviceFixtureWrite(t, f.usb+"/serial", strings.Repeat("x", 514)+"\n")
		},
		"different device number": func(t *testing.T, f *deviceFixture) { serviceFixtureWrite(t, f.sysNode+"/dev", "1:5\n") },
		"wrong descriptor flags": func(t *testing.T, f *deviceFixture) {
			serviceFixtureWrite(t, f.f.process+"/fdinfo/6", "flags:\t0100000\nino:\t1\n")
		},
		"missing descriptor": func(t *testing.T, f *deviceFixture) {
			if err := os.Remove(f.f.process + "/fd/6"); err != nil {
				t.Fatal(err)
			}
		},
		"descriptor alias": func(t *testing.T, f *deviceFixture) {
			if err := os.Remove(f.f.process + "/fd/6"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(f.node+" (deleted)", f.f.process+"/fd/6"); err != nil {
				t.Fatal(err)
			}
		},
		"symlink device node": func(t *testing.T, f *deviceFixture) {
			if err := os.Rename(f.node, f.node+"-old"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(f.node+"-old", f.node); err != nil {
				t.Fatal(err)
			}
		},
		"regular device node": func(t *testing.T, f *deviceFixture) { f.r.stat = statDeviceAt },
		"world writable node": func(t *testing.T, f *deviceFixture) {
			if err := os.Chmod(f.node, 0666); err != nil {
				t.Fatal(err)
			}
		},
		"untrusted node owner": func(t *testing.T, f *deviceFixture) {
			if err := os.Chown(f.node, 65534, 65534); err != nil {
				t.Fatal(err)
			}
		},
		"writable device directory": func(t *testing.T, f *deviceFixture) {
			if err := os.Chmod(f.r.dev+"/dxberry", 0777); err != nil {
				t.Fatal(err)
			}
		},
		"noncanonical sysfs link": func(t *testing.T, f *deviceFixture) {
			p := f.r.sys + "/dev/char/1:3"
			target, err := os.Readlink(p)
			if err != nil {
				t.Fatal(err)
			}
			os.Remove(p)
			if err := os.Symlink(strings.Replace(target, "devices/", "devices/extra/../", 1), p); err != nil {
				t.Fatal(err)
			}
		},
		"indirect stable link": func(t *testing.T, f *deviceFixture) {
			p := f.r.dev + "/dxberry/hf-cat"
			os.Remove(p)
			if err := os.Symlink("../indirect/../ttyUSB0", p); err != nil {
				t.Fatal(err)
			}
		},
		"sysfs escape": func(t *testing.T, f *deviceFixture) {
			p := f.r.sys + "/dev/char/1:3"
			os.Remove(p)
			if err := os.Symlink("/etc", p); err != nil {
				t.Fatal(err)
			}
		},
		"sysfs symlink ancestor": func(t *testing.T, f *deviceFixture) {
			p := f.r.sys + "/devices/platform"
			if err := os.Rename(p, p+"-old"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(p+"-old", p); err != nil {
				t.Fatal(err)
			}
		},
		"sysfs attribute symlink": func(t *testing.T, f *deviceFixture) {
			p := f.usb + "/idVendor"
			if err := os.Rename(p, p+"-old"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(p+"-old", p); err != nil {
				t.Fatal(err)
			}
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newDeviceFixture(t)
			mutate(t, f)
			command := serviceCommandFixture(t, "hf", 4532)
			view, err := f.r.Read(ctx5(t), f.service, command)
			if !errors.Is(err, ErrDeviceUnavailable) || view != (DeviceObservation{}) {
				t.Fatal("untrusted metadata accepted", err)
			}
		})
	}
}

func TestDeviceReaderRechecksAfterTheLastServiceQuery(t *testing.T) {
	for _, kind := range []string{"serial", "node replacement", "invocation"} {
		t.Run(kind, func(t *testing.T) {
			f := newDeviceFixture(t)
			command := serviceCommandFixture(t, "hf", 4532)
			f.f.unitCalls = 0
			f.f.onUnit = func(n int) {
				if n != 3 {
					return
				}
				switch kind {
				case "serial":
					serviceFixtureWrite(t, f.usb+"/serial", "changed\n")
				case "node replacement":
					if err := os.Rename(f.node, f.node+"-old"); err != nil {
						t.Fatal(err)
					}
					if err := os.WriteFile(f.node, nil, 0660); err != nil {
						t.Fatal(err)
					}
					var st unix.Stat_t
					if err := unix.Stat(f.node, &st); err != nil {
						t.Fatal(err)
					}
					f.numbers[st.Ino] = unix.Mkdev(1, 3)
				case "invocation":
					f.f.unit = strings.Replace(f.f.unit, strings.Repeat("1", 32), strings.Repeat("2", 32), 1)
				}
			}
			view, err := f.r.Read(ctx5(t), f.service, command)
			if !errors.Is(err, ErrDeviceUnavailable) || view != (DeviceObservation{}) {
				t.Fatal("post-query drift returned proof", err)
			}
		})
	}
}

func TestOwnedDeviceFDMatchesRealPTYAndRefusesSameNumberDifferentInode(t *testing.T) {
	master, slave, err := pty.Open()
	if err != nil {
		t.Fatal(err)
	}
	defer master.Close()
	defer slave.Close()
	var st unix.Stat_t
	if err := unix.Fstat(int(slave.Fd()), &st); err != nil {
		t.Fatal(err)
	}
	dir, err := peerOpenDir(unix.AT_FDCWD, fmt.Sprintf("/proc/%d", os.Getpid()))
	if err != nil {
		t.Fatal(err)
	}
	defer dir.Close()
	fd, _, err := ownedDeviceFD(ctx5(t), int(dir.Fd()), deviceStat(st), slave.Name(), true)
	if err != nil || fd != fmt.Sprint(slave.Fd()) {
		t.Fatal("actual PTY descriptor not matched", fd, err)
	}
	wrong := deviceStat(st)
	wrong.inode++
	if _, _, err := ownedDeviceFD(ctx5(t), int(dir.Fd()), wrong, slave.Name(), true); !errors.Is(err, ErrDeviceUnavailable) {
		t.Fatal("same device number with wrong inode matched", err)
	}
	if err := slave.Close(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := ownedDeviceFD(ctx5(t), int(dir.Fd()), deviceStat(st), slave.Name(), true); !errors.Is(err, ErrDeviceUnavailable) {
		t.Fatal("closed PTY descriptor matched", err)
	}
}

func TestDescriptorFlagsRefusePathOnlyReadOnlyAndChangedInode(t *testing.T) {
	good := "pos:\t0\nflags:\t0100002\nmnt_id:\t1\nino:\t123\n"
	if _, ok := descriptorFlags([]byte(good), 123, true); !ok {
		t.Fatal("RW descriptor refused")
	}
	for _, bad := range []string{strings.Replace(good, "0100002", "0100000", 1), strings.Replace(good, "0100002", "010000002", 1), strings.Replace(good, "123", "124", 1), good + "flags:\t02\n", good + "ino:\t123\n", good[:len(good)-1], "flags:\tgarbage\nino:\t123\n"} {
		if _, ok := descriptorFlags([]byte(bad), 123, true); ok {
			t.Fatal("unknown descriptor flags accepted")
		}
	}
	writeOnly := strings.Replace(good, "0100002", "0100001", 1)
	if _, ok := descriptorFlags([]byte(writeOnly), 123, false); !ok {
		t.Fatal("write-only PTT descriptor refused")
	}
	if _, ok := descriptorFlags([]byte(writeOnly), 123, true); ok {
		t.Fatal("write-only CAT descriptor accepted")
	}
}

func TestUSBContextFollowsTheNearestActualInterface(t *testing.T) {
	for input, want := range map[string]string{
		"/sys/devices/platform/usb1/1-1/1-1.3/1-1.3:1.0/ttyUSB0/tty/ttyUSB0":      "usb-0:1.3:1.0",
		"/sys/devices/platform/usb2/2-2/2-2.1/2-2.1:1.3/0003:ABCD/hidraw/hidraw0": "usb-0:2.1:1.3",
	} {
		actual, device, ok := usbContext(input)
		if !ok || actual != want || !strings.HasPrefix(input, device+"/") {
			t.Fatal(actual, device, ok)
		}
	}
	for _, input := range []string{"/sys/devices/virtual/tty/tty0", "/sys/devices/usb1/1-2/1-1.3:1.0/ttyUSB0"} {
		if _, _, ok := usbContext(input); ok {
			t.Fatal("non-USB or inconsistent ancestry accepted")
		}
	}
}

func TestDeviceReaderIsLazyAndRefusesUnsupportedInputs(t *testing.T) {
	r := NewDeviceReader()
	if !r.initialized || r.dev != "/dev" || r.sys != "/sys" || r.stat == nil {
		t.Fatal("untrusted production paths")
	}
	if _, err := r.Read(context.Background(), ServiceObservation{}, radios.RigCommand{}); !errors.Is(err, ErrDeviceUnavailable) {
		t.Fatal(err)
	}
	if nativeDeviceCommand(radios.RigCommand{}) {
		t.Fatal("zero command supported")
	}
}

func FuzzDeviceDescriptorMetadata(f *testing.F) {
	f.Add([]byte("pos: 0\nflags: 0100002\nino: 123\n"), true)
	f.Fuzz(func(t *testing.T, raw []byte, rw bool) { _, _ = descriptorFlags(raw, 123, rw) })
}

func addDeviceFixturePin(t *testing.T, f *deviceFixture, role, kernel, usbPort, serial, fd string, minor uint32) {
	t.Helper()
	node := f.r.dev + "/" + kernel
	if err := os.WriteFile(node, nil, 0660); err != nil {
		t.Fatal(err)
	}
	var st unix.Stat_t
	if err := unix.Stat(node, &st); err != nil {
		t.Fatal(err)
	}
	f.numbers[st.Ino] = unix.Mkdev(1, minor)
	if err := os.Symlink("../"+kernel, f.r.dev+"/dxberry/hf-"+role); err != nil {
		t.Fatal(err)
	}
	usb := f.r.sys + "/devices/platform/usb1/1-1/1-" + usbPort
	iface := "1-" + usbPort + ":1.0"
	suffix := "/" + kernel + "/tty/" + kernel
	if role == "hid" {
		iface = "1-" + usbPort + ":1.3"
		suffix = "/0003:1234:ABCD/hidraw/" + kernel
	}
	sysNode := usb + "/" + iface + suffix
	serviceFixtureWrite(t, sysNode+"/dev", fmt.Sprintf("1:%d\n", minor))
	serviceFixtureWrite(t, usb+"/idVendor", "1234\n")
	serviceFixtureWrite(t, usb+"/idProduct", "abcd\n")
	serviceFixtureWrite(t, usb+"/serial", serial+"\n")
	rel, err := filepath.Rel(f.r.sys+"/dev/char", sysNode)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(rel, fmt.Sprintf("%s/dev/char/1:%d", f.r.sys, minor)); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(node, f.f.process+"/fd/"+fd); err != nil {
		t.Fatal(err)
	}
	serviceFixtureWrite(t, f.f.process+"/fdinfo/"+fd, fmt.Sprintf("pos: 0\nflags: 0100001\nmnt_id: 1\nino: %d\n", st.Ino))
}

func TestDeviceReaderChecksEveryUsedSerialOrHIDPin(t *testing.T) {
	for _, kind := range []string{"serial PTT", "HID PTT"} {
		t.Run(kind, func(t *testing.T) {
			f := newDeviceFixture(t)
			config := serviceConfigurationFixture(t, "hf", 4532, func(record map[string]any) {
				if kind == "serial PTT" {
					record["rig"].(map[string]any)["ptt_type"] = "DTR"
					record["ptt_serial"] = map[string]any{"path": "usb-0:1.4:1.0", "vidpid": "1234:abcd", "serial": "ptt"}
				} else {
					record["ptt"].(map[string]any)["method"] = "cm108"
					record["hid"] = map[string]any{"path": "usb-0:1.5:1.3", "vidpid": "1234:abcd", "serial": "hid"}
				}
			})
			owner := "none"
			slot := 1
			fd := "7"
			if kind == "serial PTT" {
				addDeviceFixturePin(t, f, "ptt", "ttyUSB1", "1.4", "ptt", fd, 5)
			} else {
				owner = "session"
				slot = 2
				fd = "8"
				addDeviceFixturePin(t, f, "hid", "hidraw0", "1.5", "hid", fd, 7)
			}
			command, ok := config.Rigctld(owner)
			if !ok {
				t.Fatal("no command")
			}
			refreshServiceCommandPeer(t, f.f, command.Arguments())
			service, err := f.f.r.Read(ctx5(t), "hf", f.f.e)
			if err != nil {
				t.Fatal(err)
			}
			view, err := f.r.Read(ctx5(t), service, command)
			if err != nil || !view.devices[0].used || !view.devices[slot].used || view.devices[slot].descriptor != fd {
				t.Fatal("used PTT pin not verified", err)
			}
			if err := os.Remove(f.f.process + "/fd/" + fd); err != nil {
				t.Fatal(err)
			}
			if _, err := f.r.Read(ctx5(t), service, command); !errors.Is(err, ErrDeviceUnavailable) {
				t.Fatal("missing used PTT descriptor accepted", err)
			}
		})
	}
}

func TestDeviceReaderRefusesUnknownIdentityAndUnsupportedRoutes(t *testing.T) {
	for _, kind := range []string{"unknown serial", "unknown USB ID", "dummy", "proxy", "flrig", "GPIO"} {
		t.Run(kind, func(t *testing.T) {
			f := newDeviceFixture(t)
			config := serviceConfigurationFixture(t, "hf", 4532, func(record map[string]any) {
				switch kind {
				case "unknown serial":
					delete(record["cat"].(map[string]any), "serial")
				case "unknown USB ID":
					delete(record["cat"].(map[string]any), "vidpid")
				case "dummy":
					record["rig"].(map[string]any)["model"] = 1
				case "proxy":
					record["rig"].(map[string]any)["model"] = 2
				case "flrig":
					record["rig"].(map[string]any)["control"] = "flrig"
				case "GPIO":
					record["ptt"].(map[string]any)["method"] = "gpio"
					record["ptt"].(map[string]any)["gpio_line"] = 17
				}
			})
			owner := "none"
			if kind == "GPIO" {
				owner = "session"
			}
			command, ok := config.Rigctld(owner)
			if !ok {
				t.Fatal("no command")
			}
			refreshServiceCommandPeer(t, f.f, command.Arguments())
			service, err := f.f.r.Read(ctx5(t), "hf", f.f.e)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := f.r.Read(ctx5(t), service, command); !errors.Is(err, ErrDeviceUnavailable) {
				t.Fatal("unknown device route accepted", err)
			}
		})
	}
}

func TestDeviceReaderAllowsKnownEmptySerialButNeverUnknown(t *testing.T) {
	f := newDeviceFixture(t)
	config := serviceConfigurationFixture(t, "hf", 4532, func(record map[string]any) { record["cat"].(map[string]any)["serial"] = "" })
	command, ok := config.Rigctld("none")
	if !ok {
		t.Fatal("no command")
	}
	if err := os.Remove(f.usb + "/serial"); err != nil {
		t.Fatal(err)
	}
	if _, err := f.r.Read(ctx5(t), f.service, command); err != nil {
		t.Fatal("device with no serial attribute refused", err)
	}
	serviceFixtureWrite(t, f.usb+"/serial", "\n")
	if _, err := f.r.Read(ctx5(t), f.service, command); err != nil {
		t.Fatal("known empty serial attribute refused", err)
	}
	serviceFixtureWrite(t, f.usb+"/serial", "")
	if _, err := f.r.Read(ctx5(t), f.service, command); !errors.Is(err, ErrDeviceUnavailable) {
		t.Fatal("empty failed sysfs read treated as no serial", err)
	}
}
