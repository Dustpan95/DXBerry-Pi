package pat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

type authorityFixture struct {
	a                 *authority
	unitText, version string
	unitCalls         int
}

func writeFixture(t *testing.T, path, raw string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
		t.Fatal(err)
	}
}

func statFixture(pid, start string) string {
	f := []string{"S"}
	for len(f) < 19 {
		f = append(f, "0")
	}
	f = append(f, start)
	return pid + " (pat worker) " + strings.Join(f, " ") + "\n"
}

func newAuthorityFixture(t *testing.T) *authorityFixture {
	t.Helper()
	root := t.TempDir()
	f := &authorityFixture{version: "installed 1.0.1\n"}
	f.a = &authority{records: filepath.Join(root, "radios.json"), proc: filepath.Join(root, "proc"), cgroups: filepath.Join(root, "cgroup"), executable: filepath.Join(root, "pat"), uid: func() (uint32, error) { return 1000, nil }}
	group := "/system.slice/system-dxberry\\x2dscreen.slice/dxberry-screen@pat-radio1.service"
	f.unitText = "Id=dxberry-screen@pat-radio1.service\nLoadState=loaded\nActiveState=active\nSubState=running\nMainPID=100\nInvocationID=" + strings.Repeat("1", 32) + "\nControlGroup=" + group + "\n"
	f.a.command = func(ctx context.Context, name string, args ...string) ([]byte, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		switch name {
		case "/usr/bin/dpkg-query":
			want := []string{"--show", "--showformat=${db:Status-Status} ${Version}\n", "--", "pat"}
			if !reflect.DeepEqual(args, want) {
				t.Error("unexpected package command", args)
			}
			return []byte(f.version), nil
		case "/usr/bin/systemctl":
			want := []string{"show", "--no-pager", "--property=Id,LoadState,ActiveState,SubState,MainPID,InvocationID,ControlGroup", "--", "dxberry-screen@pat-radio1.service"}
			if !reflect.DeepEqual(args, want) {
				t.Error("unexpected manager command", args)
			}
			f.unitCalls++
			return []byte(f.unitText), nil
		default:
			t.Error("unexpected executable", name)
			return nil, ErrUnavailable
		}
	}
	writeFixture(t, f.a.records, `{"radios":{"radio1":{"owner":"pat"},"radio2":{"owner":null}}}`)
	writeFixture(t, f.a.executable, "inert executable identity")
	if err := os.Chmod(f.a.executable, 0755); err != nil {
		t.Fatal(err)
	}
	launcher := filepath.Join(root, "bash")
	writeFixture(t, launcher, "inert launcher")
	for pid, target := range map[string]string{"100": launcher, "200": f.a.executable} {
		dir := filepath.Join(f.a.proc, pid)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(target, filepath.Join(dir, "exe")); err != nil {
			t.Fatal(err)
		}
	}
	writeFixture(t, filepath.Join(f.a.cgroups, group, "cgroup.procs"), "100\n200\n")
	writeFixture(t, filepath.Join(f.a.proc, "200", "status"), "Name:\tpat\nUid:\t1000\t1000\t1000\t1000\n")
	writeFixture(t, filepath.Join(f.a.proc, "200", "cgroup"), "0::"+group+"\n")
	writeFixture(t, filepath.Join(f.a.proc, "200", "stat"), statFixture("200", "1234"))
	writeFixture(t, filepath.Join(f.a.proc, "200", "cmdline"), "pat\x00http\x00--addr\x00127.0.0.1:8180\x00")
	writeFixture(t, filepath.Join(f.a.proc, "net", "tcp"), "  sl  local_address rem_address st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode\n 0: 0100007F:1FF4 00000000:0000 0A 00000000:00000000 00:00000000 00000000 1000 0 5678 1\n")
	fd := filepath.Join(f.a.proc, "200", "fd")
	if err := os.Mkdir(fd, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink("socket:[5678]", filepath.Join(fd, "5")); err != nil {
		t.Fatal(err)
	}
	return f
}

func TestAuthorityRequiresOneVersionedManagedProcessAndItsListener(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("trusted root metadata fixture")
	}
	f := newAuthorityFixture(t)
	id, err := f.a.probe(context.Background(), "radio1")
	if err != nil || id.pid != "200" || id.start != "1234" || id.socket != "5678" || id.uid != 1000 || f.unitCalls != 2 {
		t.Fatal("wrong managed identity", id, err, f.unitCalls)
	}
}

func TestAuthorityRejectsUntrustedOrAmbiguousOwnershipEvidence(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("trusted root metadata fixture")
	}
	changes := map[string]func(*authorityFixture){
		"no-assignment": func(f *authorityFixture) { writeFixture(t, f.a.records, `{"radios":{"radio1":{"owner":"wsjtx"}}}`) },
		"two-owners": func(f *authorityFixture) {
			writeFixture(t, f.a.records, `{"radios":{"radio1":{"owner":"pat"},"radio2":{"owner":"pat"}}}`)
		},
		"duplicate-owner": func(f *authorityFixture) {
			writeFixture(t, f.a.records, `{"radios":{"radio1":{"owner":"pat","owner":"wsjtx"}}}`)
		},
		"writable-record": func(f *authorityFixture) {
			if err := os.Chmod(f.a.records, 0666); err != nil {
				t.Fatal(err)
			}
		},
		"unknown-version": func(f *authorityFixture) { f.version = "installed 1.1.0\n" },
		"stopped-unit": func(f *authorityFixture) {
			f.unitText = strings.Replace(f.unitText, "ActiveState=active", "ActiveState=inactive", 1)
		},
		"stopping-unit": func(f *authorityFixture) {
			f.unitText = strings.Replace(f.unitText, "ActiveState=active", "ActiveState=deactivating", 1)
		},
		"wrong-unit": func(f *authorityFixture) {
			f.unitText = strings.Replace(f.unitText, "Id=dxberry-screen@pat-radio1.service", "Id=other.service", 1)
		},
		"no-invocation": func(f *authorityFixture) {
			f.unitText = strings.Replace(f.unitText, strings.Repeat("1", 32), strings.Repeat("0", 32), 1)
		},
		"duplicate-manager-field": func(f *authorityFixture) { f.unitText += "MainPID=100\n" },
		"foreign-cgroup": func(f *authorityFixture) {
			writeFixture(t, filepath.Join(f.a.proc, "200", "cgroup"), "0::/other.service\n")
		},
		"wrong-process-user": func(f *authorityFixture) {
			writeFixture(t, filepath.Join(f.a.proc, "200", "status"), "Uid:\t1000\t0\t1000\t1000\n")
		},
		"zombie": func(f *authorityFixture) {
			writeFixture(t, filepath.Join(f.a.proc, "200", "stat"), strings.Replace(statFixture("200", "1234"), ") S", ") Z", 1))
		},
		"different-command": func(f *authorityFixture) {
			writeFixture(t, filepath.Join(f.a.proc, "200", "cmdline"), "pat\x00connect\x00PRIVATE_TARGET\x00")
		},
		"wrong-listener-address": func(f *authorityFixture) {
			p := filepath.Join(f.a.proc, "net", "tcp")
			raw, _ := os.ReadFile(p)
			writeFixture(t, p, strings.Replace(string(raw), "0100007F:1FF4", "00000000:1FF4", 1))
		},
		"wrong-listener-owner": func(f *authorityFixture) {
			p := filepath.Join(f.a.proc, "net", "tcp")
			raw, _ := os.ReadFile(p)
			writeFixture(t, p, strings.Replace(string(raw), "1000 0 5678", "1001 0 5678", 1))
		},
		"listener-not-in-process": func(f *authorityFixture) {
			if err := os.Remove(filepath.Join(f.a.proc, "200", "fd", "5")); err != nil {
				t.Fatal(err)
			}
		},
		"replaced-executable": func(f *authorityFixture) {
			old := f.a.executable + ".running"
			if err := os.Link(f.a.executable, old); err != nil {
				t.Fatal(err)
			}
			link := filepath.Join(f.a.proc, "200", "exe")
			if err := os.Remove(link); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(old, link); err != nil {
				t.Fatal(err)
			}
			other := f.a.executable + ".new"
			writeFixture(t, other, "replacement")
			if err := os.Rename(other, f.a.executable); err != nil {
				t.Fatal(err)
			}
		},
		"root-app-user": func(f *authorityFixture) { f.a.uid = func() (uint32, error) { return 0, nil } },
		"symlinked-record": func(f *authorityFixture) {
			old := f.a.records + ".old"
			if err := os.Rename(f.a.records, old); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(old, f.a.records); err != nil {
				t.Fatal(err)
			}
		},
		"record-fifo": func(f *authorityFixture) {
			if err := os.Remove(f.a.records); err != nil {
				t.Fatal(err)
			}
			if err := syscall.Mkfifo(f.a.records, 0600); err != nil {
				t.Fatal(err)
			}
		},
		"record-limit": func(f *authorityFixture) { writeFixture(t, f.a.records, strings.Repeat(" ", 2<<20+1)) },
		"second-pat-process": func(f *authorityFixture) {
			dir := filepath.Join(f.a.proc, "201")
			if err := os.Mkdir(dir, 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(f.a.executable, filepath.Join(dir, "exe")); err != nil {
				t.Fatal(err)
			}
			group := strings.TrimSuffix(strings.Split(f.unitText, "ControlGroup=")[1], "\n")
			writeFixture(t, filepath.Join(f.a.cgroups, group, "cgroup.procs"), "100\n200\n201\n")
		},
		"two-listeners": func(f *authorityFixture) {
			p := filepath.Join(f.a.proc, "net", "tcp")
			raw, _ := os.ReadFile(p)
			row := strings.Split(string(raw), "\n")[1]
			writeFixture(t, p, string(raw)+strings.Replace(row, "5678", "5679", 1)+"\n")
		},
	}
	for name, change := range changes {
		t.Run(name, func(t *testing.T) {
			f := newAuthorityFixture(t)
			change(f)
			id, err := f.a.probe(context.Background(), "radio1")
			if err != ErrUnavailable || id != (instance{}) {
				t.Fatal("unverified identity accepted", id, err)
			}
		})
	}
}

func TestAuthorityRechecksManagerAndRecordsWithinProbe(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("trusted root metadata fixture")
	}
	for _, change := range []string{"invocation", "records"} {
		f := newAuthorityFixture(t)
		original := f.a.command
		f.a.command = func(ctx context.Context, name string, args ...string) ([]byte, error) {
			if name == "/usr/bin/systemctl" && f.unitCalls == 1 {
				if change == "invocation" {
					f.unitText = strings.Replace(f.unitText, strings.Repeat("1", 32), strings.Repeat("2", 32), 1)
				} else {
					writeFixture(t, f.a.records, `{"radios":{"radio1":{"owner":null}}}`)
				}
			}
			return original(ctx, name, args...)
		}
		if _, err := f.a.probe(context.Background(), "radio1"); err != ErrUnavailable {
			t.Fatal("changed evidence accepted", change, err)
		}
	}
}

func TestListenerAndProcessStartMatchRealProcMetadata(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:8180")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	a := &authority{proc: "/proc"}
	inode, err := a.listener(uint32(os.Getuid()))
	pid := strconv.Itoa(os.Getpid())
	if err != nil || !a.ownsSocket(pid, inode) {
		t.Fatal("real listening socket not matched to owner", inode, err)
	}
	raw, err := boundedFile(filepath.Join("/proc", pid, "stat"), 64<<10, false)
	if err != nil {
		t.Fatal(err)
	}
	if start, err := processStart(raw, pid); err != nil || start == "" {
		t.Fatal("real process start unreadable", start, err)
	}
}

func TestMetadataCommandBoundsOutputAndHonorsEarlierDeadline(t *testing.T) {
	root := t.TempDir()
	script := filepath.Join(root, "metadata")
	writeFixture(t, script, "#!/bin/sh\nwhile :; do echo PRIVATE_OUTPUT; done\n")
	if err := os.Chmod(script, 0755); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if _, err := metadataCommand(ctx, script); err != ErrUnavailable {
		t.Fatal("unbounded metadata output accepted", err)
	}
	writeFixture(t, script, "#!/bin/sh\nsleep 60 &\nwait\n")
	ctx, cancel = context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()
	started := time.Now()
	if _, err := metadataCommand(ctx, script); err != ErrUnavailable || time.Since(started) > time.Second {
		t.Fatal("metadata process outlived caller", err)
	}
}

func TestManagedReadDiscardsReplacedEvidenceAndDoesNotReadWithoutAuthority(t *testing.T) {
	base := instance{pid: "200", start: "1234", socket: "5678"}
	for _, change := range []string{"before-failure", "after-failure", "pid", "start", "socket", "invocation", "records", "uid", "executable"} {
		t.Run(change, func(t *testing.T) {
			calls, reads := 0, 0
			r := &Reader{gate: make(chan struct{}, 1), probe: func(context.Context, string) (instance, error) {
				calls++
				id := base
				if calls == 1 && change == "before-failure" || calls == 2 && change == "after-failure" {
					return instance{}, errors.New("PRIVATE_REASON")
				}
				if calls == 2 {
					switch change {
					case "pid":
						id.pid = "201"
					case "start":
						id.start = "5555"
					case "socket":
						id.socket = "6666"
					case "invocation":
						id.unit.invocation = "changed"
					case "records":
						id.records.digest[0] = 1
					case "uid":
						id.uid = 1001
					case "executable":
						id.executable.inode = 2
					}
				}
				return id, nil
			}, read: func(context.Context) (Status, error) { reads++; return Status{Connected: true}, nil }}
			got, err := r.Read(context.Background(), "radio1")
			if err != ErrUnavailable || !reflect.DeepEqual(got, Observation{}) {
				t.Fatal("old or private state escaped", got, err)
			}
			if change == "before-failure" && reads != 0 {
				t.Fatal("read before authority", reads)
			}
		})
	}
}

func TestManagedReadWaitIsCancellableAndObservationIsPrivate(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	r := &Reader{gate: make(chan struct{}, 1), probe: func(context.Context, string) (instance, error) { return instance{pid: "PRIVATE_PID"}, nil }, read: func(context.Context) (Status, error) {
		close(started)
		<-release
		return Status{Connected: true, ObservedAt: time.Now()}, nil
	}}
	done := make(chan Observation, 1)
	go func() {
		v, err := r.Read(context.Background(), "radio1")
		if err != nil {
			t.Error(err)
		}
		done <- v
	}()
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if _, err := r.Read(ctx, "radio1"); err != ErrUnavailable {
		t.Fatal("blocked caller was not cancelled", err)
	}
	close(release)
	got := <-done
	if got.Radio != "radio1" || !got.Status.Connected {
		t.Fatal("valid observation refused", got)
	}
	raw, err := json.Marshal(got)
	if err != nil || strings.Contains(string(raw), "PRIVATE_") {
		t.Fatal("private identity exposed", got)
	}
}

func TestManagedAuthoritySurroundsRealHTTPReadAndDetectsAssignmentRewrite(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("trusted root metadata fixture")
	}
	for _, change := range []string{"stable", "no-owner", "assignment-rewrite", "process-restart", "listener-replaced", "binary-modified"} {
		t.Run(change, func(t *testing.T) {
			f := newAuthorityFixture(t)
			var requests atomic.Int32
			if change == "no-owner" {
				writeFixture(t, f.a.records, `{"radios":{}}`)
			}
			client := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				switch change {
				case "assignment-rewrite":
					raw, err := os.ReadFile(f.a.records)
					if err != nil {
						t.Error(err)
						return
					}
					next := f.a.records + ".new"
					writeFixture(t, next, string(raw))
					if err := os.Rename(next, f.a.records); err != nil {
						t.Error(err)
						return
					}
				case "process-restart":
					writeFixture(t, filepath.Join(f.a.proc, "200", "stat"), statFixture("200", "1235"))
				case "listener-replaced":
					path := filepath.Join(f.a.proc, "net", "tcp")
					raw, _ := os.ReadFile(path)
					writeFixture(t, path, strings.Replace(string(raw), "5678", "5679", 1))
					fd := filepath.Join(f.a.proc, "200", "fd", "5")
					if err := os.Remove(fd); err != nil {
						t.Error(err)
						return
					}
					if err := os.Symlink("socket:[5679]", fd); err != nil {
						t.Error(err)
						return
					}
				case "binary-modified":
					writeFixture(t, f.a.executable, "modified executable in same inode")
				}
				fmt.Fprint(w, idleStatus)
			})
			reader := &Reader{gate: make(chan struct{}, 1), probe: f.a.probe, read: client.read}
			got, err := reader.Read(context.Background(), "radio1")
			if change == "stable" {
				if err != nil || got.Radio != "radio1" || got.Status.ObservedAt.IsZero() {
					t.Fatal("stable managed read refused", got, err)
				}
			} else if err != ErrUnavailable || !reflect.DeepEqual(got, Observation{}) {
				t.Fatal("changed identity returned a live observation", got, err)
			}
			if change == "no-owner" && requests.Load() != 0 {
				t.Fatal("unauthorized status request occurred")
			}
		})
	}
}

func TestReaderRejectsInvalidRadioWithoutMetadataOrHTTP(t *testing.T) {
	r := &Reader{gate: make(chan struct{}, 1), probe: func(context.Context, string) (instance, error) {
		t.Error("unexpected metadata read")
		return instance{}, ErrUnavailable
	}, read: func(context.Context) (Status, error) {
		t.Error("unexpected HTTP read")
		return Status{}, ErrUnavailable
	}}
	for _, radio := range []string{"", "../radio1", "Radio1", "radio1;id", strings.Repeat("a", 13)} {
		if _, err := r.Read(context.Background(), radio); err != ErrUnavailable {
			t.Fatal("invalid identity accepted", radio)
		}
	}
	if _, err := NewReader("relative.json"); err != ErrUnavailable {
		t.Fatal("relative authority path accepted")
	}
}

func TestMetadataCommandRefusesFiniteOversizedOutput(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if _, err := metadataCommand(ctx, "/bin/sh", "-c", "head -c 65537 /dev/zero"); err != ErrUnavailable {
		t.Fatal("finite oversized metadata output accepted", err)
	}
	raw, err := metadataCommand(ctx, "/bin/sh", "-c", "head -c 65536 /dev/zero")
	if err != nil || len(raw) != 65536 {
		t.Fatal("valid boundary output refused", len(raw), err)
	}
}
