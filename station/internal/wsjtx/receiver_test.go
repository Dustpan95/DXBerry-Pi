package wsjtx

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"testing"
	"time"
)

func observer(t *testing.T) (*Observer, *observationClock, Binding) {
	t.Helper()
	c := &observationClock{now: time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)}
	o := NewObserver(c)
	t.Cleanup(func() { _ = o.Close() })
	b, err := o.Start("radio1", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	return o, c, b
}

func udpSender(t *testing.T, addr netip.AddrPort) *net.UDPConn {
	t.Helper()
	c, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(addr))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func send(t *testing.T, c *net.UDPConn, b []byte) {
	t.Helper()
	n, err := c.Write(b)
	if err != nil || n != len(b) {
		t.Fatalf("UDP write %d/%d: %v", n, len(b), err)
	}
}

func eventually(t *testing.T, ready func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if ready() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("receiver condition did not become ready")
}

func received(t *testing.T, o *Observer, radio string, count uint64) ReceiverSnapshot {
	t.Helper()
	var s ReceiverSnapshot
	eventually(t, func() bool {
		var ok bool
		s, ok = o.Snapshot(radio)
		return ok && s.Observation.Counters.Accepted >= count
	})
	return s
}

func joined(t *testing.T, r *receiver) {
	t.Helper()
	done := make(chan struct{})
	go func() { r.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("receiver goroutines did not stop")
	}
}

func TestObserverReceivesLoopbackWithoutSending(t *testing.T) {
	o, _, b := observer(t)
	if b.Radio() != "radio1" || b.Generation() == 0 || b.Address().Addr() != loopback || b.Address().Port() == 0 {
		t.Fatalf("bad endpoint: %+v", b)
	}
	c := udpSender(t, b.Address())
	for _, name := range []string{"heartbeat-s3", "status-full-s3", "decode-full-s3"} {
		send(t, c, fixture(t, name))
	}
	s := received(t, o, "radio1", 3)
	if s.Address != b.Address() || s.Observation.Generation != b.Generation() || s.Observation.Availability != Live || s.Observation.Status == nil || len(s.Observation.Decodes) != 1 || s.Transport.Received != 3 || s.Transport.QueueDrops != 0 || s.TransportError != "" {
		t.Fatalf("unexpected loopback observation: %+v", s)
	}
	peer := c.LocalAddr().(*net.UDPAddr).AddrPort()
	peer = netip.AddrPortFrom(peer.Addr().Unmap(), peer.Port())
	if s.Peer != peer {
		t.Fatalf("wrong peer: %v, want %v", s.Peer, peer)
	}
	value(t, s.Observation.Status.Mode, "FT8")
	value(t, s.Observation.Decodes[0].Data.Text, "CQ K1ABC EM00")
	if err := c.SetReadDeadline(time.Now().Add(75 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	var buf [32]byte
	_, err := c.Read(buf[:])
	var networkError net.Error
	if !errors.As(err, &networkError) || !networkError.Timeout() {
		t.Fatalf("receiver sent a packet or failed unexpectedly: %v", err)
	}
}

func TestObserverReplacementCannotReceiveOldEndpointTraffic(t *testing.T) {
	o, _, old := observer(t)
	previous := o.receivers["radio1"]
	oldClient := udpSender(t, old.Address())
	send(t, oldClient, fixture(t, "status-full-s3"))
	received(t, o, "radio1", 1)
	current, err := o.Start("radio1", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	joined(t, previous)
	if current.Address() == old.Address() || current.Generation() <= old.Generation() {
		t.Fatal("replacement reused endpoint or generation")
	}
	if o.Stop(old) {
		t.Fatal("old lifecycle callback stopped replacement")
	}
	_, _ = oldClient.Write(fixture(t, "status-full-s3")) // closed destination may return an ICMP refusal
	client := udpSender(t, current.Address())
	send(t, client, fixture(t, "heartbeat-s3"))
	s := received(t, o, "radio1", 1)
	if s.Observation.Status != nil || s.Observation.Counters.Accepted != 1 || s.Observation.Generation != current.Generation() {
		t.Fatalf("old traffic entered replacement: %+v", s)
	}
	if !o.Stop(current) || o.Stop(current) {
		t.Fatal("wrong exact-binding stop result")
	}
	if _, ok := o.Snapshot("radio1"); ok {
		t.Fatal("stopped binding retained")
	}
}

func TestObserverRoutesSameIDRadiosIndependently(t *testing.T) {
	o, _, one := observer(t)
	two, err := o.Start("radio2", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if one.Address() == two.Address() {
		t.Fatal("radios share a UDP endpoint")
	}
	a, b := udpSender(t, one.Address()), udpSender(t, two.Address())
	send(t, a, fixture(t, "status-full-s3"))
	send(t, b, fixture(t, "decode-full-s3"))
	first, second := received(t, o, "radio1", 1), received(t, o, "radio2", 1)
	if first.Observation.Status == nil || len(first.Observation.Decodes) != 0 || second.Observation.Status != nil || len(second.Observation.Decodes) != 1 {
		t.Fatal("cross-radio observations")
	}
	all := o.Snapshots()
	if len(all) != 2 || all[0].Observation.Radio != "radio1" || all[1].Observation.Radio != "radio2" {
		t.Fatal("unstable receiver order")
	}
	if !o.Stop(one) {
		t.Fatal("could not stop first radio")
	}
	send(t, b, fixture(t, "heartbeat-s3"))
	received(t, o, "radio2", 2)
}

func TestObserverPinsOnlyAValidPeerAndRejectsOtherSources(t *testing.T) {
	o, clock, b := observer(t)
	stranger := udpSender(t, b.Address())
	send(t, stranger, nil)
	send(t, stranger, fixture(t, "ignored"))
	wrong := bytes.Replace(fixture(t, "heartbeat-s3"), []byte(fixtureID), []byte("WSJT-X - radio2"), 1)
	send(t, stranger, wrong)
	eventually(t, func() bool {
		s, _ := o.Snapshot("radio1")
		return s.Observation.Counters.WrongIdentity == 1 && s.Observation.Counters.Malformed == 1 && s.Observation.Counters.IgnoredType == 1
	})
	s, _ := o.Snapshot("radio1")
	if s.Peer.IsValid() || s.Observation.Availability != Waiting {
		t.Fatal("rejected packet selected a peer")
	}
	rightful := udpSender(t, b.Address())
	send(t, rightful, fixture(t, "heartbeat-s3"))
	first := received(t, o, "radio1", 1)
	clock.advance(FreshFor - time.Nanosecond)
	send(t, stranger, fixture(t, "status-full-s3"))
	send(t, stranger, fixture(t, "close-s3"))
	eventually(t, func() bool { s, _ := o.Snapshot("radio1"); return s.Transport.WrongPeer == 2 })
	s, _ = o.Snapshot("radio1")
	if s.Peer != first.Peer || s.Observation.Counters.Accepted != 1 || s.Observation.Status != nil || !s.Observation.LastSeen.Equal(*first.Observation.LastSeen) {
		t.Fatal("other source changed current state")
	}
	clock.advance(time.Nanosecond)
	s, _ = o.Snapshot("radio1")
	if s.Observation.Availability != Stale {
		t.Fatal("other source extended liveness")
	}
	send(t, rightful, fixture(t, "heartbeat-s3"))
	s = received(t, o, "radio1", 2)
	if s.Observation.Availability != Live || s.Observation.Status != nil {
		t.Fatal("reconnect invented a reading")
	}
}

func TestObserverOversizedDatagramCannotActAsAValidPrefix(t *testing.T) {
	o, _, b := observer(t)
	bad := udpSender(t, b.Address())
	packet := append(fixture(t, "close-s3"), make([]byte, 40000)...)
	send(t, bad, packet)
	eventually(t, func() bool { s, _ := o.Snapshot("radio1"); return s.Observation.Counters.Oversized == 1 })
	s, _ := o.Snapshot("radio1")
	if s.Peer.IsValid() || s.Observation.Availability != Waiting || s.TransportError != "" {
		t.Fatal("oversized prefix closed/pinned the receiver")
	}
	good := udpSender(t, b.Address())
	send(t, good, fixture(t, "heartbeat-s3"))
	received(t, o, "radio1", 1)
}

func TestObserverBindAndValidationFailuresPreserveCurrentBinding(t *testing.T) {
	o, _, b := observer(t)
	c := udpSender(t, b.Address())
	send(t, c, fixture(t, "status-full-s3"))
	received(t, o, "radio1", 1)
	attempts := 0
	bindError := errors.New("test bind unavailable")
	o.listen = func(string, *net.UDPAddr) (*net.UDPConn, error) { attempts++; return nil, bindError }
	if _, err := o.Start("radio1", ""); !errors.Is(err, ErrInvalidSession) || attempts != 0 {
		t.Fatal("invalid start tried to bind")
	}
	if _, err := o.Start("radio1", fixtureID); !errors.Is(err, bindError) {
		t.Fatal(err)
	}
	send(t, c, fixture(t, "heartbeat-s3"))
	s := received(t, o, "radio1", 2)
	if s.Address != b.Address() || s.Observation.Generation != b.Generation() || s.Observation.Status == nil {
		t.Fatal("failed start retired working receiver")
	}
}

func TestObserverQueueDelayDoesNotRefreshTheReadTimestamp(t *testing.T) {
	o, clock, b := observer(t)
	r := o.receivers["radio1"]
	client := udpSender(t, b.Address())
	first := clock.Now()
	func() {
		o.tracker.mu.Lock()
		defer o.tracker.mu.Unlock()
		send(t, client, fixture(t, "status-full-s3"))
		eventually(t, func() bool { return r.received.Load() == 1 })
		clock.advance(20 * time.Second)
	}()
	s := received(t, o, "radio1", 1)
	if !s.Observation.LastSeen.Equal(first) || !s.Observation.StatusSeenAt.Equal(first) {
		t.Fatal("queue processing time replaced the socket read time")
	}
	clock.advance(FreshFor - 20*time.Second)
	s, _ = o.Snapshot("radio1")
	if s.Observation.Availability != Stale || s.Observation.Status != nil {
		t.Fatal("buffered status extended liveness beyond its observed arrival")
	}
}

func TestObserverExpiredQueuePacketCannotPinAPeer(t *testing.T) {
	o, clock, b := observer(t)
	r := o.receivers["radio1"]
	late := udpSender(t, b.Address())
	func() {
		o.tracker.mu.Lock()
		defer o.tracker.mu.Unlock()
		send(t, late, fixture(t, "heartbeat-s3"))
		eventually(t, func() bool { return r.received.Load() == 1 })
		clock.advance(FreshFor)
	}()
	eventually(t, func() bool { s, _ := o.Snapshot("radio1"); return s.Observation.Counters.Late == 1 })
	s, _ := o.Snapshot("radio1")
	if s.Peer.IsValid() || s.Observation.LastSeen != nil || s.Observation.Availability != Waiting || s.Observation.Counters.Accepted != 0 {
		t.Fatal("expired queue packet established a live peer")
	}
	fresh := udpSender(t, b.Address())
	send(t, fresh, fixture(t, "heartbeat-s3"))
	if received(t, o, "radio1", 1).Transport.WrongPeer != 0 {
		t.Fatal("old queued source locked out the current peer")
	}
}

func TestObserverRefusesRetiredPortsAndClosesRejectedSockets(t *testing.T) {
	o, _, old := observer(t)
	if !o.Stop(old) {
		t.Fatal("stop failed")
	}
	attempts := 0
	o.listen = func(network string, requested *net.UDPAddr) (*net.UDPConn, error) {
		attempts++
		if network != "udp4" || !requested.IP.Equal(net.IP{127, 0, 0, 1}) || requested.Port != 0 {
			t.Error("nonloopback/requested destination")
		}
		return net.ListenUDP("udp4", net.UDPAddrFromAddrPort(old.Address()))
	}
	if _, err := o.Start("radio1", fixtureID); !errors.Is(err, ErrNoFreshEndpoint) || attempts != freshBindAttempts {
		t.Fatalf("retired endpoint admitted: %v (%d attempts)", err, attempts)
	}
	probe, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(old.Address()))
	if err != nil {
		t.Fatalf("rejected socket leaked: %v", err)
	}
	_ = probe.Close()
	o.listen = net.ListenUDP
	current, err := o.Start("radio1", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if current.Address() == old.Address() {
		t.Fatal("forgot retired port after stop")
	}
}

func TestObserverNoFreshPortPreservesExistingGeneration(t *testing.T) {
	o, _, b := observer(t)
	for i := range o.used {
		o.used[i] = ^uint64(0)
	}
	if _, err := o.Start("radio1", fixtureID); !errors.Is(err, ErrNoFreshEndpoint) {
		t.Fatal(err)
	}
	c := udpSender(t, b.Address())
	send(t, c, fixture(t, "heartbeat-s3"))
	if s := received(t, o, "radio1", 1); s.Observation.Generation != b.Generation() {
		t.Fatal("port exhaustion retired current generation")
	}
}

func TestObserverRejectsAndClosesAWildcardSocket(t *testing.T) {
	o := NewObserver(nil)
	defer o.Close()
	var rejected *net.UDPAddr
	o.listen = func(string, *net.UDPAddr) (*net.UDPConn, error) {
		c, err := net.ListenUDP("udp4", &net.UDPAddr{})
		if err == nil {
			rejected = c.LocalAddr().(*net.UDPAddr)
		}
		return c, err
	}
	if _, err := o.Start("radio1", fixtureID); err == nil {
		t.Fatal("wildcard receiver accepted")
	}
	if _, ok := o.Snapshot("radio1"); ok {
		t.Fatal("failed bind registered observation")
	}
	if rejected == nil {
		t.Fatal("test bind failed")
	}
	probe, err := net.ListenUDP("udp4", rejected)
	if err != nil {
		t.Fatal("wildcard socket leaked")
	}
	_ = probe.Close()
}

func TestObserverSocketFailureImmediatelyRemovesCurrentReadings(t *testing.T) {
	o, _, b := observer(t)
	c := udpSender(t, b.Address())
	send(t, c, fixture(t, "status-full-s3"))
	received(t, o, "radio1", 1)
	r := o.receivers["radio1"]
	if err := r.conn.Close(); err != nil {
		t.Fatal(err)
	}
	joined(t, r)
	s, _ := o.Snapshot("radio1")
	if s.TransportError == "" || s.Observation.Availability != Closed || s.Observation.Status != nil || s.Observation.StatusSeenAt != nil || s.Observation.Counters.Accepted != 1 {
		t.Fatalf("failed socket still appears current: %+v", s)
	}
	newBinding, err := o.Start("radio1", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if o.Stop(b) || newBinding.Address() == b.Address() {
		t.Fatal("replacement of failed socket was not isolated")
	}
	client := udpSender(t, newBinding.Address())
	send(t, client, fixture(t, "heartbeat-s3"))
	if received(t, o, "radio1", 1).TransportError != "" {
		t.Fatal("old failure leaked into replacement")
	}
}

func TestObserverProgramCloseStopsTransportWithoutReportingAFault(t *testing.T) {
	o, _, b := observer(t)
	r := o.receivers["radio1"]
	c := udpSender(t, b.Address())
	send(t, c, fixture(t, "close-s3"))
	joined(t, r)
	s, _ := o.Snapshot("radio1")
	if s.Observation.Availability != Closed || s.TransportError != "" || s.Observation.Counters.Accepted != 1 {
		t.Fatalf("program Close misreported: %+v", s)
	}
	if len(r.frames) != 0 {
		t.Fatal("closed transport retained queued datagrams")
	}
	probe, err := net.ListenUDP("udp4", net.UDPAddrFromAddrPort(b.Address()))
	if err != nil {
		t.Fatalf("closed transport retained socket: %v", err)
	}
	_ = probe.Close()
	if !o.Stop(b) {
		t.Fatal("could not remove closed observation")
	}
}

func TestReceiverQueueIsBoundedAndOwnsDatagramBytes(t *testing.T) {
	r := &receiver{stopCh: make(chan struct{}), frames: make(chan datagram, ReceiveQueueDepth)}
	peer := netip.AddrPortFrom(loopback, 12345)
	at := time.Unix(1, 0)
	b := []byte{7}
	if !r.enqueue(b, peer, at) {
		t.Fatal("queue unexpectedly stopped")
	}
	b[0] = 9
	frame := <-r.frames
	if len(frame.data) != 1 || frame.data[0] != 7 || frame.peer != peer || !frame.observedAt.Equal(at) {
		t.Fatal("receive buffer aliases queued data")
	}
	for i := 0; i < ReceiveQueueDepth; i++ {
		r.enqueue(b, peer, at)
	}
	for i := 0; i < 100; i++ {
		if !r.enqueue(b, peer, at) {
			t.Fatal("full queue stopped receive loop")
		}
	}
	if len(r.frames) != ReceiveQueueDepth || r.queueDrops.Load() != 100 {
		t.Fatal("queue growth/drop accounting incorrect")
	}
	close(r.stopCh)
	if r.enqueue(b, peer, at) || r.queueDrops.Load() != 100 {
		t.Fatal("stopped queue continued accepting")
	}
}

func TestObserverCapacityAndShutdownBoundResources(t *testing.T) {
	o, _, first := observer(t)
	other := NewObserver(nil)
	defer other.Close()
	if other.Stop(first) || other.Stop(Binding{}) {
		t.Fatal("binding crossed observers")
	}
	for i := 2; i <= MaxSessions; i++ {
		if _, err := o.Start(fmt.Sprintf("radio%d", i), fixtureID); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := o.Start("extra", fixtureID); !errors.Is(err, ErrSessionLimit) {
		t.Fatal(err)
	}
	if _, err := o.Start("radio1", fixtureID); err != nil {
		t.Fatal("replacement failed at session cap", err)
	}
	all := make([]*receiver, 0, MaxSessions)
	for _, r := range o.receivers {
		all = append(all, r)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	for _, r := range all {
		joined(t, r)
	}
	if err := o.Close(); err != nil || len(o.Snapshots()) != 0 {
		t.Fatal("shutdown not idempotent")
	}
	if _, err := o.Start("radio1", fixtureID); !errors.Is(err, ErrObserverClosed) {
		t.Fatal(err)
	}
	if o.Stop(first) {
		t.Fatal("stopped closed observer")
	}
}

func TestObserverConcurrentReplacementReadsAndShutdown(t *testing.T) {
	o, _, first := observer(t)
	packet := fixture(t, "heartbeat-s3")
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 40; i++ {
			b, err := o.Start("radio1", fixtureID)
			if errors.Is(err, ErrObserverClosed) {
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			c, err := net.DialUDP("udp4", nil, net.UDPAddrFromAddrPort(b.Address()))
			if err != nil {
				t.Error(err)
				return
			}
			_, _ = c.Write(packet)
			_ = c.Close()
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 100; i++ {
			for _, s := range o.Snapshots() {
				if s.Address.Addr() != loopback || s.Observation.InstanceID != fixtureID {
					t.Error("inconsistent transport snapshot")
					return
				}
			}
			o.Stop(first)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 20; i++ {
			o.Snapshots()
		}
		_ = o.Close()
	}()
	wg.Wait()
	if err := o.Close(); err != nil || len(o.Snapshots()) != 0 {
		t.Fatal("shutdown left live receivers")
	}
}
