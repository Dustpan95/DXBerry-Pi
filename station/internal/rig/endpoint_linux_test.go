package rig

import (
	"encoding/json"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func endpointProcAddress(a netip.AddrPort) string {
	ip := a.Addr().As4()
	return fmt.Sprintf("%02X%02X%02X%02X:%04X", ip[3], ip[2], ip[1], ip[0], a.Port())
}

func TestConnectionEndpointMatchesActualLinuxSocketWithoutCommands(t *testing.T) {
	f := newFake(t, freqHandler)
	c := fastClient(t, f.addr())
	waitConnected(t, c)
	e, ok := c.ConnectionEndpoint()
	if !ok || !e.Current() || e.SocketInode() == 0 || e.Generation() == 0 || e.SocketUID() != uint32(os.Geteuid()) {
		t.Fatal(e, ok)
	}
	peer, err := netip.ParseAddrPort(f.addr())
	if err != nil || e.Peer() != peer || !e.Local().Addr().IsLoopback() || e.Local().Port() == 0 {
		t.Fatal(e, err)
	}
	fdDir, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	foundFD := false
	for _, entry := range fdDir {
		target, err := os.Readlink(filepath.Join("/proc/self/fd", entry.Name()))
		if err == nil && target == fmt.Sprintf("socket:[%d]", e.SocketInode()) {
			foundFD = true
		}
	}
	if !foundFD {
		t.Fatal("endpoint inode not owned by the actual process")
	}
	raw, err := os.ReadFile("/proc/net/tcp")
	if err != nil {
		t.Fatal(err)
	}
	foundTuple := false
	for _, line := range strings.Split(string(raw), "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 10 && fields[9] == strconv.FormatUint(e.SocketInode(), 10) {
			if !strings.EqualFold(fields[1], endpointProcAddress(e.Local())) || !strings.EqualFold(fields[2], endpointProcAddress(e.Peer())) || fields[3] != "01" || fields[7] != strconv.FormatUint(uint64(e.SocketUID()), 10) {
				t.Fatal("kernel tuple does not match endpoint", line, e)
			}
			foundTuple = true
		}
	}
	if !foundTuple {
		t.Fatal("endpoint missing from established TCP table")
	}
	for i := 0; i < 1000; i++ {
		copy, ok := c.ConnectionEndpoint()
		if !ok || copy != e || !copy.Current() {
			t.Fatal("metadata lookup changed connection")
		}
	}
	f.mu.Lock()
	calls := len(f.cmds)
	f.mu.Unlock()
	if calls != 0 {
		t.Fatal("metadata lookup sent protocol commands", calls)
	}
	//lint:ignore SA9005 This regression requires private evidence to serialize as an empty object.
	encoded, err := json.Marshal(e)
	if err != nil || string(encoded) != "{}" {
		t.Fatal("private endpoint became wire data", string(encoded), err)
	}
	reply, err := c.Do(ctx5(t), "get_freq")
	if err != nil || len(reply.Lines) != 1 || reply.Lines[0] != "Frequency: 14074000" {
		t.Fatal("metadata capture broke protocol I/O", reply, err)
	}
	c.Close()
	if _, ok := c.ConnectionEndpoint(); ok || e.Current() {
		t.Fatal("closed socket evidence remained current")
	}
}

func TestConnectionEndpointIsPublishedBeforeTheUpCallback(t *testing.T) {
	f := newFake(t, freqHandler)
	c := dial(f.addr(), nil, options{hold: true})
	t.Cleanup(c.Close)
	results := make(chan bool, 1)
	c.OnState(func(up bool) {
		if up {
			e, ok := c.ConnectionEndpoint()
			results <- ok && e.Current()
		}
	})
	c.release()
	select {
	case valid := <-results:
		if !valid {
			t.Fatal("up event preceded socket identity")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no up callback")
	}
}

func TestConnectionEndpointRejectsOldSocketAfterReconnect(t *testing.T) {
	f := newFake(t, func(n int, line string, conn net.Conn) bool {
		if n == 1 {
			return false
		}
		return freqHandler(n, line, conn)
	})
	c := fastClient(t, f.addr())
	waitConnected(t, c)
	before, ok := c.ConnectionEndpoint()
	if !ok {
		t.Fatal("no first endpoint")
	}
	if _, err := c.Do(ctx5(t), "get_freq"); err == nil {
		t.Fatal("fixture did not close the first connection")
	}
	var after Endpoint
	plEventually(t, "replacement socket", func() bool {
		candidate, ok := c.ConnectionEndpoint()
		if ok && candidate.Generation() > before.Generation() {
			after = candidate
			return true
		}
		return false
	})
	if before.Current() || !after.Current() || before == after || before.Peer() != after.Peer() {
		t.Fatal("reconnect reused evidence", before, after)
	}
	if _, err := c.Do(ctx5(t), "get_freq"); err != nil {
		t.Fatal(err)
	}
}

type endpointFileConn struct {
	net.Conn
	file *os.File
}

func (c endpointFileConn) LocalAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 1234}
}
func (c endpointFileConn) RemoteAddr() net.Addr {
	return &net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 4532}
}
func (c endpointFileConn) SyscallConn() (syscall.RawConn, error) { return c.file.SyscallConn() }

func TestConnectionEndpointUnavailableMetadataDoesNotBreakCommands(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	if data := captureEndpoint(a); data != (endpointData{}) {
		t.Fatal("unsupported transport produced identity")
	}
	file, err := os.CreateTemp(t.TempDir(), "not-a-socket")
	if err != nil {
		t.Fatal(err)
	}
	if data := captureEndpoint(endpointFileConn{file: file}); data != (endpointData{}) {
		t.Fatal("ordinary file accepted as TCP socket")
	}
	file.Close()
	if data := captureEndpoint(endpointFileConn{file: file}); data != (endpointData{}) {
		t.Fatal("closed descriptor accepted")
	}
	f := newFake(t, freqHandler)
	c := fastClient(t, f.addr())
	waitConnected(t, c)
	// Simulate unavailable metadata without changing the established protocol
	// state. Qualification must fail; normal existing commands still work.
	c.mu.Lock()
	c.endpoint = Endpoint{}
	c.mu.Unlock()
	if _, ok := c.ConnectionEndpoint(); ok {
		t.Fatal("unknown metadata was synthesized")
	}
	if _, err := c.Do(ctx5(t), "get_freq"); err != nil || !c.Connected() {
		t.Fatal("metadata absence broke protocol", err)
	}
	if _, ok := (*Client)(nil).ConnectionEndpoint(); ok || (Endpoint{}).Current() {
		t.Fatal("nil/zero metadata accepted")
	}
}

func endpointManagerHandler(n int, line string, conn net.Conn) bool {
	if line == `+\get_freq` {
		return freqHandler(n, line, conn)
	}
	if line == `+\get_mode` {
		fmt.Fprint(conn, "get_mode:\nMode: USB\nPassband: 2400\nRPRT 0\n")
		return true
	}
	fmt.Fprintf(conn, "%s:\nRPRT -1\n", strings.TrimPrefix(line, `+\`))
	return true
}

func TestManagerConnectionSnapshotTracksRecordEntryAndSocket(t *testing.T) {
	f := newFake(t, endpointManagerHandler)
	second := newFake(t, endpointManagerHandler)
	port, err := strconv.Atoi(strings.Split(f.addr(), ":")[1])
	if err != nil {
		t.Fatal(err)
	}
	otherPort, err := strconv.Atoi(strings.Split(second.addr(), ":")[1])
	if err != nil {
		t.Fatal(err)
	}
	r := newMgRig(t, radio("hf", port, 3073))
	if err := r.cache.Store("3073", Caps{ModelName: "Fixture", Probed: time.Now()}); err != nil {
		t.Fatal(err)
	}
	// Use real TCP clients in this manager fixture, not scripted Conn objects.
	r.m.Dialer = func(addr string) Conn { return Dial(addr, nil) }
	r.run()
	var before Connection
	plEventually(t, "manager socket snapshot", func() bool {
		candidate, ok := r.m.ConnectionSnapshot("hf")
		if ok {
			before = candidate
			return true
		}
		return false
	})
	if !before.Current() || before.Radio().Port != port || before.Endpoint().Peer().Port() != uint16(port) || before.Poller() == nil {
		t.Fatal(before)
	}
	metadata := before.Radio()
	metadata.Label = "updated"
	r.lst.push(metadata)
	plEventually(t, "record metadata invalidation", func() bool { return !before.Current() })
	changed, ok := r.m.ConnectionSnapshot("hf")
	if !ok || !changed.Current() || changed.Poller() != before.Poller() {
		t.Fatal("record update lost or replaced poller")
	}
	r.lst.push(radio("hf", otherPort, 3073))
	var replacement Connection
	plEventually(t, "manager replacement", func() bool {
		candidate, ok := r.m.ConnectionSnapshot("hf")
		if ok && candidate.Endpoint().Peer().Port() == uint16(otherPort) {
			replacement = candidate
			return true
		}
		return false
	})
	if changed.Current() || replacement.Poller() == changed.Poller() || !replacement.Current() {
		t.Fatal("replaced entry remained current")
	}
	r.lst.push()
	plEventually(t, "removed snapshot", func() bool { _, ok := r.m.ConnectionSnapshot("hf"); return !ok && !replacement.Current() })
	//lint:ignore SA9005 This regression requires private evidence to serialize as an empty object.
	encoded, err := json.Marshal(replacement)
	if err != nil || string(encoded) != "{}" {
		t.Fatal("manager evidence became wire data", string(encoded), err)
	}
	if _, ok := (*Manager)(nil).ConnectionSnapshot("hf"); ok || (Connection{}).Current() {
		t.Fatal("nil/zero manager evidence accepted")
	}
}

func TestManagerConnectionSnapshotDoesNotDialMissingOrInventFakeMetadata(t *testing.T) {
	r := newMgRig(t, radio("hf", 4532, 3073))
	r.run()
	plEventually(t, "scripted poller", func() bool { _, ok := r.m.Radio("hf"); return ok })
	if _, ok := r.m.ConnectionSnapshot("hf"); ok {
		t.Fatal("scripted connection acquired kernel identity")
	}
	if _, ok := r.m.ConnectionSnapshot("missing"); ok {
		t.Fatal("missing entry acquired identity")
	}
	if len(r.all()) != 1 {
		t.Fatal("metadata lookup dialed another connection")
	}
}

func TestManagerConnectionSnapshotRefusesADialerWithTheWrongPeer(t *testing.T) {
	f := newFake(t, endpointManagerHandler)
	port, err := strconv.Atoi(strings.Split(f.addr(), ":")[1])
	if err != nil {
		t.Fatal(err)
	}
	configured := port + 1
	if configured > 65535 {
		configured = port - 1
	}
	r := newMgRig(t, radio("hf", configured, 3073))
	if err := r.cache.Store("3073", Caps{ModelName: "Fixture", Probed: time.Now()}); err != nil {
		t.Fatal(err)
	}
	r.m.Dialer = func(string) Conn { return Dial(f.addr(), nil) }
	r.run()
	plEventually(t, "actual client endpoint", func() bool {
		client, ok := r.m.Client("hf")
		if !ok {
			return false
		}
		_, ok = client.ConnectionEndpoint()
		return ok
	})
	if _, ok := r.m.ConnectionSnapshot("hf"); ok {
		t.Fatal("mismatched configured port qualified")
	}
}

func TestEndpointCurrentRejectsCanceledClientBeforeEventDelivery(t *testing.T) {
	f := newFake(t, freqHandler)
	c := fastClient(t, f.addr())
	waitConnected(t, c)
	e, ok := c.ConnectionEndpoint()
	if !ok {
		t.Fatal("no endpoint")
	}
	c.cancel() // invalid immediately, even before the run loop publishes down
	if e.Current() {
		t.Fatal("canceled client still qualified")
	}
	if _, ok := c.ConnectionEndpoint(); ok {
		t.Fatal("canceled client returned a socket")
	}
}
