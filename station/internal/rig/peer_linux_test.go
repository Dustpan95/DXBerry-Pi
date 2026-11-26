package rig

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

func TestPeerProcessFixture(t *testing.T) {
	if os.Getenv("DXB_PEER_FIXTURE") == "held" {
		socket := os.NewFile(3, "accepted")
		defer socket.Close()
		// Acknowledge the inherited TCP socket after the original process has
		// closed both of its copies. Start alone only confirms exec succeeded.
		var probe [1]byte
		if _, err := io.ReadFull(os.Stdin, probe[:]); err != nil || probe[0] != '?' {
			t.Fatal("missing handoff probe", err)
		}
		info, err := unix.GetsockoptTCPInfo(3, unix.IPPROTO_TCP, unix.TCP_INFO)
		if err != nil || info.State != 1 { // TCP_ESTABLISHED
			t.Fatal("inherited connection is not established", err)
		}
		link, err := os.Readlink("/proc/self/fd/3")
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println(link)
		io.Copy(io.Discard, os.Stdin)
		return
	}
	if os.Getenv("DXB_PEER_FIXTURE") != "1" {
		return
	}
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		panic(err)
	}
	defer listener.Close()
	fmt.Println(listener.Addr())
	conn, err := listener.Accept()
	if err != nil {
		panic(err)
	}
	defer conn.Close()
	fmt.Println("accepted")
	in := bufio.NewScanner(os.Stdin)
	for in.Scan() {
		switch in.Text() {
		case "listener":
			listener.Close()
		case "accepted":
			conn.Close()
		case "handoff":
			// Keep the connection established in another process while this
			// process retains only the listener. A listener-only check is wrong.
			file, err := conn.(*net.TCPConn).File()
			if err != nil {
				panic(err)
			}
			held := exec.Command(os.Args[0], "-test.run=^TestPeerProcessFixture$")
			held.Env = append(os.Environ(), "DXB_PEER_FIXTURE=held", "GORACE=atexit_sleep_ms=0")
			held.ExtraFiles = []*os.File{file}
			held.Stderr = os.Stderr
			ack, err := held.StdoutPipe()
			if err != nil {
				panic(err)
			}
			want, err := os.Readlink(fmt.Sprintf("/proc/self/fd/%d", file.Fd()))
			if err != nil {
				panic(err)
			}
			input, err := held.StdinPipe()
			if err != nil {
				panic(err)
			}
			if err := held.Start(); err != nil {
				panic(err)
			}
			defer func() { input.Close(); held.Process.Kill(); held.Wait() }()
			file.Close()
			conn.Close()
			if _, err := io.WriteString(input, "?"); err != nil {
				panic(err)
			}
			line, err := bufio.NewReader(ack).ReadString('\n')
			if err != nil || line != want+"\n" {
				t.Fatalf("socket holder reply %q, want %q (%v)", line, want, err)
			}
		case "exit":
			return
		default:
			panic("bad fixture command")
		}
		fmt.Println("closed")
	}
}

type peerChild struct {
	pid  int
	addr string
	send func(string)
	wait func(string)
}

func startPeerChild(t *testing.T) peerChild {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestPeerProcessFixture$")
	cmd.Env = append(os.Environ(), "DXB_PEER_FIXTURE=1", "GORACE=atexit_sleep_ms=0")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	cmd.Stderr = os.Stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stdin.Close()
		// Let the fixture close and reap its handoff process before forcing
		// termination; killing the parent first can orphan the socket holder.
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("peer fixture exit: %v", err)
			}
		case <-time.After(2 * time.Second):
			cmd.Process.Kill()
			<-done
			t.Error("peer fixture did not stop after stdin closed")
		}
	})
	out := bufio.NewScanner(stdout)
	if !out.Scan() {
		t.Fatal("no child listener", out.Err())
	}
	return peerChild{pid: cmd.Process.Pid, addr: out.Text(), send: func(s string) {
		t.Helper()
		if _, err := fmt.Fprintln(stdin, s); err != nil {
			t.Fatal(err)
		}
	}, wait: func(s string) {
		t.Helper()
		if !out.Scan() || out.Text() != s {
			t.Fatalf("child reply %q, want %q (%v)", out.Text(), s, out.Err())
		}
	}}
}

func TestInspectPeerBindsSeparateProcessAndBothSocketDescriptors(t *testing.T) {
	child := startPeerChild(t)
	client := fastClient(t, child.addr)
	waitConnected(t, client)
	child.wait("accepted")
	e, ok := client.ConnectionEndpoint()
	if !ok {
		t.Fatal("no local socket")
	}
	peer, err := e.InspectPeer(ctx5(t), child.pid)
	if err != nil {
		t.Fatal(err)
	}
	if peer.PID() != child.pid || peer.UID() != uint32(os.Getuid()) || peer.StartTicks() == 0 || peer.ListenerInode() == 0 || peer.SocketInode() == 0 || peer.SocketInode() == e.SocketInode() {
		t.Fatal("invalid peer evidence", peer)
	}
	argv, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", child.pid))
	if err != nil || peer.arguments != sha256.Sum256(argv) {
		t.Fatal("peer argument digest does not match the actual process", err)
	}
	again, err := e.InspectPeer(ctx5(t), child.pid)
	if err != nil || !peer.Same(again) || (Peer{}).Same(Peer{}) {
		t.Fatal("unchanged process/socket evidence disagreed", err)
	}
	//lint:ignore SA9005 This regression requires private evidence to serialize as an empty object.
	raw, err := json.Marshal(peer)
	if err != nil || string(raw) != "{}" {
		t.Fatal("private process evidence became wire data", string(raw), err)
	}
	// This process owns the client socket but neither the child's listener nor
	// reverse accepted socket. Matching UID and namespace alone must not pass.
	if _, err := e.InspectPeer(ctx5(t), os.Getpid()); !errors.Is(err, ErrPeerUnavailable) {
		t.Fatal("wrong process qualified", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := e.InspectPeer(ctx, child.pid); !errors.Is(err, context.Canceled) {
		t.Fatal("canceled inspection", err)
	}
	//lint:ignore SA1012 Deliberately verify that a nil context is refused.
	if _, err := e.InspectPeer(nil, child.pid); !errors.Is(err, ErrPeerUnavailable) {
		t.Fatal("nil context", err)
	}
	if _, err := e.InspectPeer(ctx5(t), 0); !errors.Is(err, ErrPeerUnavailable) {
		t.Fatal("zero PID", err)
	}
	if _, err := e.InspectPeer(ctx5(t), 2147483647); !errors.Is(err, ErrPeerUnavailable) {
		t.Fatal("missing PID", err)
	}
	client.Close()
	if _, err := e.InspectPeer(ctx5(t), child.pid); !errors.Is(err, ErrPeerUnavailable) {
		t.Fatal("closed client qualified", err)
	}
}

func TestInspectPeerRefusesMissingListenerAndHalfClosedConnection(t *testing.T) {
	for _, action := range []string{"listener", "accepted", "handoff"} {
		t.Run(action, func(t *testing.T) {
			child := startPeerChild(t)
			client := fastClient(t, child.addr)
			waitConnected(t, client)
			child.wait("accepted")
			e, ok := client.ConnectionEndpoint()
			if !ok {
				t.Fatal("no local endpoint")
			}
			before, err := e.InspectPeer(ctx5(t), child.pid)
			if err != nil {
				t.Fatal(err)
			}
			child.send(action)
			child.wait("closed")
			if action == "handoff" {
				raw, err := os.ReadFile("/proc/net/tcp")
				if err != nil {
					t.Fatal(err)
				}
				rows, err := peerTCPTable(raw, false)
				if err != nil {
					t.Fatal(err)
				}
				listener, accepted, err := peerSelect(rows, nil, e, before.UID())
				if err != nil || listener != before.ListenerInode() || accepted != before.SocketInode() {
					t.Fatal("handoff must preserve the exact established pair and listener", err)
				}
			}
			// No protocol command informed Client of peer loss. Locally current
			// metadata alone must not hide a removed listener or TCP half-close.
			if !e.Current() {
				t.Fatal("fixture unexpectedly invalidated the local endpoint")
			}
			if _, err := e.InspectPeer(ctx5(t), child.pid); !errors.Is(err, ErrPeerUnavailable) {
				t.Fatal("missing/closed socket qualified", err)
			}
		})
	}
}

func peerTableFixture(local, remote string, state string, uid, inode string) []byte {
	return []byte("  sl  local_address rem_address st tx_queue rx_queue tr tm->when retrnsmt uid timeout inode\n" +
		"0: " + local + " " + remote + " " + state + " 00000000:00000000 00:00000000 00000000 " + uid + " 0 " + inode + " 1\n")
}

func TestPeerKernelTableParsingAndRefusals(t *testing.T) {
	good := peerTableFixture("0100007F:11B4", "00000000:0000", "0A", "1000", "1234")
	rows, err := peerTCPTable(good, false)
	if err != nil || len(rows) != 1 || rows[0].local != netip.MustParseAddrPort("127.0.0.1:4532") || rows[0].inode != 1234 {
		t.Fatal(rows, err)
	}
	v6 := strings.Replace(string(peerTableFixture("00000000000000000000000001000000:11B4", "00000000000000000000000000000000:0000", "0A", "0", "20")), "rem_address", "remote_address", 1)
	ipv6, err := peerTCPTable([]byte(v6), true)
	if err != nil || len(ipv6) != 1 || ipv6[0].local != netip.MustParseAddrPort("[::1]:4532") {
		t.Fatal(ipv6, err)
	}
	for _, bad := range [][]byte{nil, []byte("no header\n"), good[:len(good)-1], append(append([]byte{}, good...), 0), []byte(strings.Replace(string(good), "0100007F", "GG00007F", 1)), []byte(strings.Replace(string(good), " 1000 ", " 01000 ", 1)), []byte(strings.Replace(string(good), " 1234 ", " -1 ", 1)), []byte(strings.Repeat("x", peerReadLimit+1)), []byte(strings.Repeat(string(good), 8194))} {
		if _, err := peerTCPTable(bad, false); !errors.Is(err, ErrPeerUnavailable) {
			t.Fatal("malformed table accepted", err)
		}
	}
	for _, bad := range []string{"", "0100007F", "0100007F:1", "00000000000000000000000001000000:11B4", "0100007F:GGGG", "0100007F:10000"} {
		if _, err := peerAddress(bad, false); !errors.Is(err, ErrPeerUnavailable) {
			t.Fatal("malformed IPv4 address accepted", bad, err)
		}
	}
	if _, err := peerAddress("0100007F:11B4", true); !errors.Is(err, ErrPeerUnavailable) {
		t.Fatal("IPv4 field accepted in IPv6 table")
	}
}

func TestPeerRequiresExactUniqueEstablishedPairAndLoopbackListener(t *testing.T) {
	e := Endpoint{data: endpointData{local: netip.MustParseAddrPort("127.0.0.1:56789"), peer: netip.MustParseAddrPort("127.0.0.1:4532"), socket: socketIdentity{inode: 10, uid: 1000}}}
	good := []peerTCP{{e.Peer(), netip.MustParseAddrPort("0.0.0.0:0"), 10, 2000, 20}, {e.Local(), e.Peer(), 1, 1000, 10}, {e.Peer(), e.Local(), 1, 2000, 30}}
	l, a, err := peerSelect(good, nil, e, 2000)
	if err != nil || l != 20 || a != 30 {
		t.Fatal(l, a, err)
	}
	cases := map[string][]peerTCP{"no listener": good[1:], "no local": {good[0], good[2]}, "no reverse": good[:2], "duplicate reverse": append(append([]peerTCP{}, good...), good[2]), "duplicate local": append(append([]peerTCP{}, good...), good[1]), "duplicate listener": append(append([]peerTCP{}, good...), good[0])}
	for name, change := range map[string]func([]peerTCP){"wildcard": func(r []peerTCP) { r[0].local = netip.MustParseAddrPort("0.0.0.0:4532") }, "wrong local inode": func(r []peerTCP) { r[1].inode++ }, "wrong local UID": func(r []peerTCP) { r[1].uid++ }, "wrong peer UID": func(r []peerTCP) { r[2].uid++ }, "half closed": func(r []peerTCP) { r[1].state = 8 }, "wrong reverse state": func(r []peerTCP) { r[2].state = 5 }, "zero inode": func(r []peerTCP) { r[2].inode = 0 }, "alias inode": func(r []peerTCP) { r[2].inode = 10 }} {
		copy := append([]peerTCP{}, good...)
		change(copy)
		cases[name] = copy
	}
	for name, rows := range cases {
		t.Run(name, func(t *testing.T) {
			if _, _, err := peerSelect(rows, nil, e, 2000); !errors.Is(err, ErrPeerUnavailable) {
				t.Fatal("incomplete/ambiguous socket proof passed", err)
			}
		})
	}
	if _, _, err := peerSelect(good, []peerTCP{{local: netip.MustParseAddrPort("[::]:4532"), state: 10}}, e, 2000); !errors.Is(err, ErrPeerUnavailable) {
		t.Fatal("ambiguous IPv6 listener passed")
	}
}

func TestPeerProcessIdentityParsing(t *testing.T) {
	stat := "123 (name with ) parentheses) S " + strings.Repeat("0 ", 18) + "456 0\n"
	if n, err := peerStart([]byte(stat), 123); err != nil || n != 456 {
		t.Fatal(n, err)
	}
	for _, bad := range []string{"", "123 (name) Z " + strings.Repeat("0 ", 18) + "456", strings.Replace(stat, "456", "0456", 1), strings.Replace(stat, "123 (", "124 (", 1), "123 (x) S 1"} {
		if _, err := peerStart([]byte(bad), 123); !errors.Is(err, ErrPeerUnavailable) {
			t.Fatal("bad process identity passed", strconv.Quote(bad), err)
		}
	}
	if uid, err := peerUID([]byte("Name: test\nUid:\t1000\t1000\t1000\t1000\n")); err != nil || uid != 1000 {
		t.Fatal(uid, err)
	}
	for _, bad := range []string{"", "Uid: 0 0 0", "Uid: 1 2 1 1", "Uid: 00 00 00 00", "Uid: 0 0 0 0\nUid: 0 0 0 0"} {
		if _, err := peerUID([]byte(bad)); !errors.Is(err, ErrPeerUnavailable) {
			t.Fatal("bad UID passed", bad, err)
		}
	}
}

func FuzzPeerKernelTables(f *testing.F) {
	f.Add(peerTableFixture("0100007F:11B4", "00000000:0000", "0A", "0", "1"), false)
	f.Add([]byte("bad\n"), true)
	f.Fuzz(func(t *testing.T, data []byte, ipv6 bool) { _, _ = peerTCPTable(data, ipv6) })
}
