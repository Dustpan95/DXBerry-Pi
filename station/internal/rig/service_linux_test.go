package rig

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func serviceUnitFixture() string {
	return "Id=rigctld@hf.service\nLoadState=loaded\nActiveState=active\nSubState=running\nMainPID=100\nInvocationID=" + strings.Repeat("1", 32) + "\nControlGroup=/system.slice/system-rigctld.slice/rigctld@hf.service\n"
}

func TestServiceUnitParserRefusesIncompleteOrAmbiguousManagerEvidence(t *testing.T) {
	good := serviceUnitFixture()
	if _, err := parseServiceUnit([]byte(good), "hf"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", good[:len(good)-1], good + "MainPID=100\n", good + "Unknown=x\n", strings.Replace(good, "ActiveState=active", "ActiveState=inactive", 1), strings.Replace(good, "SubState=running", "SubState=exited", 1), strings.Replace(good, "MainPID=100", "MainPID=0", 1), strings.Replace(good, "MainPID=100", "MainPID=0100", 1), strings.Replace(good, "MainPID=100", "MainPID=4294967296", 1), strings.Replace(good, "rigctld@hf.service", "rigctld@other.service", 1), strings.Replace(good, strings.Repeat("1", 32), strings.Repeat("0", 32), 1), strings.Replace(good, strings.Repeat("1", 32), strings.Repeat("A", 32), 1), strings.Replace(good, "/system.slice/", "/user.slice/", 1), strings.Replace(good, "/system.slice/", "/system.slice/../system.slice/", 1), strings.Replace(good, "/system.slice/", "/system.slice/\t/", 1), good + "\x00", good + "\r\n", strings.Repeat("x", 64<<10+1)} {
		if _, err := parseServiceUnit([]byte(bad), "hf"); !errors.Is(err, ErrServiceUnavailable) {
			t.Fatal("ambiguous/inactive manager evidence accepted", err)
		}
	}
	for _, name := range []string{"", "../hf", "hf/other", "HF", "hf.service", "a234567890123", "hf;start", "hf\n"} {
		if _, err := parseServiceUnit([]byte(good), name); !errors.Is(err, ErrServiceUnavailable) {
			t.Fatal("invalid name accepted", name, err)
		}
	}
}

type serviceFixture struct {
	r                    *ServiceReader
	e                    Endpoint
	peer                 Peer
	unit                 string
	unitCalls, peerCalls int
	onUnit, onPeer       func(int)
	process, group       string
}

func newServiceFixture(t *testing.T) *serviceFixture {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("root-owned service fixture; production refusal is checked separately")
	}
	root := t.TempDir()
	f := &serviceFixture{r: NewServiceReader(), unit: serviceUnitFixture()}
	f.r.proc, f.r.cgroups, f.r.executable = root+"/proc", root+"/cgroup", root+"/rigctld"
	f.process = f.r.proc + "/100"
	f.group = f.r.cgroups + "/system.slice/system-rigctld.slice/rigctld@hf.service"
	write := func(name, value string) { serviceFixtureWrite(t, name, value) }
	write(f.r.executable, "inert executable identity; never executed")
	if err := os.Chmod(f.r.executable, 0755); err != nil {
		t.Fatal(err)
	}
	write(f.process+"/stat", "100 (fixture rig) S "+strings.Repeat("0 ", 18)+"1234 0\n")
	write(f.process+"/status", "Name: rigctld\nUid: 0 0 0 0\n")
	write(f.process+"/cmdline", "rigctld\x00-m\x001\x00-T\x00127.0.0.1\x00-t\x004532\x00")
	write(f.process+"/cgroup", "0::/system.slice/system-rigctld.slice/rigctld@hf.service\n")
	write(f.group+"/cgroup.type", "domain\n")
	write(f.group+"/cgroup.procs", "100\n")
	if err := os.Mkdir(f.process+"/ns", 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("net:[42]", f.process+"/ns/net"); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(f.r.executable, f.process+"/exe"); err != nil {
		t.Fatal(err)
	}
	server := newFake(t, freqHandler)
	client := fastClient(t, server.addr())
	waitConnected(t, client)
	var ok bool
	f.e, ok = client.ConnectionEndpoint()
	if !ok {
		t.Fatal("no fixture endpoint")
	}
	dir, err := serviceDirectory(f.process)
	if err != nil {
		t.Fatal(err)
	}
	f.peer, err = peerProcessSnapshot(int(dir.Fd()), f.e, 100, "net:[42]")
	dir.Close()
	if err != nil {
		t.Fatal(err)
	}
	f.peer.listener, f.peer.accepted = 20, 30
	f.peer.listenerFD, f.peer.acceptedFD = "3", "4"
	f.r.command = func(ctx context.Context, executable string, args ...string) ([]byte, error) {
		t.Helper()
		want := []string{"show", "--no-pager", "--property=" + serviceProperties, "--", "rigctld@hf.service"}
		if executable != "/usr/bin/systemctl" || !reflect.DeepEqual(args, want) {
			t.Fatal("unexpected control command", executable, args)
		}
		f.unitCalls++
		if f.onUnit != nil {
			f.onUnit(f.unitCalls)
		}
		return []byte(f.unit), ctx.Err()
	}
	// Unit, proc/cgroup files and peer identity are synthetic in this fixture.
	// Actual process/socket ownership is covered by the separate kernel tests.
	f.r.peer = func(ctx context.Context, e Endpoint, pid int) (Peer, error) {
		t.Helper()
		if e != f.e || pid != 100 {
			return Peer{}, ErrPeerUnavailable
		}
		f.peerCalls++
		if f.onPeer != nil {
			f.onPeer(f.peerCalls)
		}
		return f.peer, ctx.Err()
	}
	return f
}

func serviceFixtureWrite(t *testing.T, name, value string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(name), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(name, []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestServiceReaderFreshChecksAndPrivateIdentity(t *testing.T) {
	f := newServiceFixture(t)
	before, err := f.r.Read(ctx5(t), "hf", f.e)
	if err != nil {
		t.Fatal(err)
	}
	after, err := f.r.Read(ctx5(t), "hf", f.e)
	if err != nil || !before.Same(after) || before.Peer() != f.peer || (ServiceObservation{}).Same(ServiceObservation{}) {
		t.Fatal("fresh identity", err)
	}
	if f.unitCalls != 4 || f.peerCalls != 4 {
		t.Fatal("fresh sources not read twice per call", f.unitCalls, f.peerCalls)
	}
	//lint:ignore SA9005 This regression requires private evidence to serialize as an empty object.
	raw, err := json.Marshal(before)
	if err != nil || string(raw) != "{}" {
		t.Fatal("private evidence became wire data", string(raw), err)
	}
	f.unit = strings.Replace(f.unit, strings.Repeat("1", 32), strings.Repeat("2", 32), 1)
	replacement, err := f.r.Read(ctx5(t), "hf", f.e)
	if err != nil || before.Same(replacement) {
		t.Fatal("new service invocation reused evidence", err)
	}
	calls := f.unitCalls
	for _, name := range []string{"../hf", "hf;start", "HF", "hf\n"} {
		if _, err := f.r.Read(ctx5(t), name, f.e); !errors.Is(err, ErrServiceUnavailable) {
			t.Fatal(name, err)
		}
	}
	if f.unitCalls != calls {
		t.Fatal("invalid name reached manager command")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := f.r.Read(ctx, "hf", f.e); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	//lint:ignore SA1012 Deliberately verify that a nil context is refused.
	if _, err := f.r.Read(nil, "hf", f.e); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatal(err)
	}
	if _, err := (*ServiceReader)(nil).Read(ctx5(t), "hf", f.e); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatal(err)
	}
	if _, err := (&ServiceReader{}).Read(ctx5(t), "hf", f.e); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatal(err)
	}
}

func TestServiceReaderRefusesUntrustedFilesAndMismatchedProcess(t *testing.T) {
	cases := map[string]func(*testing.T, *serviceFixture){
		"writable executable": func(t *testing.T, f *serviceFixture) {
			if err := os.Chmod(f.r.executable, 0777); err != nil {
				t.Fatal(err)
			}
		},
		"nonexecutable": func(t *testing.T, f *serviceFixture) {
			if err := os.Chmod(f.r.executable, 0644); err != nil {
				t.Fatal(err)
			}
		},
		"wrong executable owner": func(t *testing.T, f *serviceFixture) {
			if err := os.Chown(f.r.executable, 65534, 65534); err != nil {
				t.Fatal(err)
			}
		},
		"hardlinked executable": func(t *testing.T, f *serviceFixture) {
			if err := os.Link(f.r.executable, f.r.executable+"-link"); err != nil {
				t.Fatal(err)
			}
		},
		"symlink executable": func(t *testing.T, f *serviceFixture) {
			if err := os.Rename(f.r.executable, f.r.executable+"-old"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(f.r.executable+"-old", f.r.executable); err != nil {
				t.Fatal(err)
			}
		},
		"different process executable": func(t *testing.T, f *serviceFixture) {
			serviceFixtureWrite(t, f.r.executable+"-other", "other")
			os.Remove(f.process + "/exe")
			if err := os.Symlink(f.r.executable+"-other", f.process+"/exe"); err != nil {
				t.Fatal(err)
			}
		},
		"wrong UID": func(t *testing.T, f *serviceFixture) { serviceFixtureWrite(t, f.process+"/status", "Uid: 1 1 1 1\n") },
		"reused PID": func(t *testing.T, f *serviceFixture) {
			serviceFixtureWrite(t, f.process+"/stat", "100 (fixture rig) S "+strings.Repeat("0 ", 18)+"9999 0\n")
		},
		"changed argv": func(t *testing.T, f *serviceFixture) { serviceFixtureWrite(t, f.process+"/cmdline", "other\x00") },
		"different process cgroup": func(t *testing.T, f *serviceFixture) {
			serviceFixtureWrite(t, f.process+"/cgroup", "0::/system.slice/other.service\n")
		},
		"missing cgroup membership": func(t *testing.T, f *serviceFixture) { serviceFixtureWrite(t, f.group+"/cgroup.procs", "200\n") },
		"duplicate member":          func(t *testing.T, f *serviceFixture) { serviceFixtureWrite(t, f.group+"/cgroup.procs", "100\n100\n") },
		"threaded cgroup":           func(t *testing.T, f *serviceFixture) { serviceFixtureWrite(t, f.group+"/cgroup.type", "threaded\n") },
		"writable cgroup": func(t *testing.T, f *serviceFixture) {
			if err := os.Chmod(f.group, 0777); err != nil {
				t.Fatal(err)
			}
		},
		"symlink cgroup ancestor": func(t *testing.T, f *serviceFixture) {
			p := f.r.cgroups + "/system.slice"
			if err := os.Rename(p, p+"-old"); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(p+"-old", p); err != nil {
				t.Fatal(err)
			}
		},
		"FIFO cgroup members": func(t *testing.T, f *serviceFixture) {
			p := f.group + "/cgroup.procs"
			os.Remove(p)
			if err := unix.Mkfifo(p, 0600); err != nil {
				t.Fatal(err)
			}
		},
		"writable membership file": func(t *testing.T, f *serviceFixture) {
			if err := os.Chmod(f.group+"/cgroup.procs", 0666); err != nil {
				t.Fatal(err)
			}
		},
		"oversized process argv": func(t *testing.T, f *serviceFixture) {
			serviceFixtureWrite(t, f.process+"/cmdline", strings.Repeat("x", 64<<10)+"\x00")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			f := newServiceFixture(t)
			mutate(t, f)
			if _, err := f.r.Read(ctx5(t), "hf", f.e); !errors.Is(err, ErrServiceUnavailable) {
				t.Fatal("untrusted/mismatched service accepted", err)
			}
		})
	}
}

func TestServiceReaderRefusesDriftAndDoesNotRetainPriorSuccess(t *testing.T) {
	for _, kind := range []string{"invocation", "peer", "executable", "cgroup", "expired", "manager error", "peer error"} {
		t.Run(kind, func(t *testing.T) {
			f := newServiceFixture(t)
			if _, err := f.r.Read(ctx5(t), "hf", f.e); err != nil {
				t.Fatal(err)
			}
			f.unitCalls, f.peerCalls = 0, 0
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			f.onUnit = func(n int) {
				if n != 2 {
					return
				}
				switch kind {
				case "invocation":
					f.unit = strings.Replace(f.unit, strings.Repeat("1", 32), strings.Repeat("2", 32), 1)
				case "executable":
					serviceFixtureWrite(t, f.r.executable+"-new", "replacement")
					if err := os.Chmod(f.r.executable+"-new", 0755); err != nil {
						t.Fatal(err)
					}
					if err := os.Rename(f.r.executable+"-new", f.r.executable); err != nil {
						t.Fatal(err)
					}
				case "cgroup":
					serviceFixtureWrite(t, f.group+"/cgroup.procs", "200\n")
				case "expired":
					cancel()
				}
			}
			if kind == "peer" {
				f.onPeer = func(n int) {
					if n == 2 {
						f.peer.accepted++
					}
				}
			}
			if kind == "manager error" {
				f.r.command = func(context.Context, string, ...string) ([]byte, error) {
					return []byte(f.unit), errors.New("private failure must not escape")
				}
			}
			if kind == "peer error" {
				f.r.peer = func(context.Context, Endpoint, int) (Peer, error) {
					return f.peer, errors.New("private failure must not escape")
				}
			}
			view, err := f.r.Read(ctx, "hf", f.e)
			if err == nil || view != (ServiceObservation{}) {
				t.Fatal("drift returned stale success", err)
			}
			if kind == "expired" {
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			} else if !errors.Is(err, ErrServiceUnavailable) {
				t.Fatal("raw failure escaped", err)
			}
		})
	}
}

func TestServiceReaderProductionConstructorAndNonrootRefusal(t *testing.T) {
	r := NewServiceReader()
	if !r.initialized || r.proc != "/proc" || r.cgroups != "/sys/fs/cgroup" || r.executable != "/usr/bin/rigctld" || r.command == nil || r.peer == nil {
		t.Fatal("incomplete production reader")
	}
	if os.Geteuid() == 0 {
		return
	}
	server := newFake(t, freqHandler)
	client := fastClient(t, server.addr())
	waitConnected(t, client)
	e, ok := client.ConnectionEndpoint()
	if !ok {
		t.Fatal("no socket")
	}
	r.command = func(context.Context, string, ...string) ([]byte, error) {
		t.Fatal("nonroot read reached command")
		return nil, nil
	}
	if _, err := r.Read(ctx5(t), "hf", e); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatal(err)
	}
}

func TestServiceMetadataRunnerBoundsAndCancellation(t *testing.T) {
	t.Setenv("DXB_SERVICE_TEST_ENV", "not-inherited")
	raw, err := serviceMetadataCommand(ctx5(t), "/bin/sh", "-c", "printf 'metadata\\n'; printf '%s' \"$DXB_SERVICE_TEST_ENV\"")
	if err != nil || string(raw) != "metadata\n" {
		t.Fatal("metadata output or environment", len(raw), err)
	}
	if _, err := serviceMetadataCommand(ctx5(t), "/bin/sh", "-c", "head -c 65537 /dev/zero"); !errors.Is(err, ErrServiceUnavailable) {
		t.Fatal("oversized stdout accepted", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := serviceMetadataCommand(ctx, "/bin/sh", "-c", "sleep 30 & wait"); !errors.Is(err, ErrServiceUnavailable) || time.Since(started) > time.Second {
		t.Fatal("metadata process group not canceled", err, time.Since(started))
	}
}

func FuzzServiceUnitMetadata(f *testing.F) {
	f.Add([]byte(serviceUnitFixture()), "hf")
	f.Add([]byte("bad\n"), "../hf")
	f.Fuzz(func(t *testing.T, raw []byte, name string) { _, _ = parseServiceUnit(raw, name) })
}
