package wsjtx

import (
	"crypto/rand"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync"
	"sync/atomic"
	"time"
)

const (
	ReceiveQueueDepth = 32
	receiveBufferSize = 256 * 1024
	freshBindAttempts = 64
)

var (
	ErrObserverClosed  = errors.New("WSJT-X observer closed")
	ErrNoFreshEndpoint = errors.New("no fresh WSJT-X loopback endpoint available")
	loopback           = netip.AddrFrom4([4]byte{127, 0, 0, 1})
)

// Binding belongs to one observer and radio generation. Its endpoint is for
// local program configuration, never a browser-selected destination.
type Binding struct {
	owner *Observer
	token Token
	addr  netip.AddrPort
}

func (b Binding) Address() netip.AddrPort { return b.addr }
func (b Binding) Radio() string           { return b.token.radio }
func (b Binding) Generation() uint64      { return b.token.generation }

// TransportCounters count what this receiver actually observes. QueueDrops
// excludes UDP packets lost in the kernel/network, which it cannot measure.
type TransportCounters struct {
	Received    uint64
	QueueDrops  uint64
	NonLoopback uint64
	WrongPeer   uint64
}

type ReceiverSnapshot struct {
	Observation    Snapshot
	Address        netip.AddrPort
	Peer           netip.AddrPort
	Transport      TransportCounters
	TransportError string
}

// Observer owns receive-only IPv4 loopback sockets and their captured tokens.
// It never sends protocol messages or changes a program's settings.
type Observer struct {
	mu        sync.Mutex
	instance  string // isolates preview approvals across observer/process lifetimes
	tracker   *Tracker
	receivers map[string]*receiver
	closed    bool
	// An 8 KiB bitset prevents endpoint reuse for this observer's lifetime.
	// Exhaustion fails explicitly rather than admitting delayed old packets.
	used    [1024]uint64
	history *endpointHistory
	listen  func(string, *net.UDPAddr) (*net.UDPConn, error)
}

// NewObserver keeps retired endpoints only in memory, for isolated users such
// as fixture tests. Station integration must use NewPersistentObserver.
func NewObserver(clock Clock) *Observer {
	return &Observer{instance: rand.Text(), tracker: NewTracker(clock), receivers: make(map[string]*receiver), listen: net.ListenUDP}
}

// Start first obtains a fresh socket. Bind/start failures preserve the current
// generation and socket. Successful replacement closes and joins the old one.
func (o *Observer) Start(radio, instanceID string) (Binding, error) {
	// Reuse the tracker's input contract without changing a current generation.
	if !validSession(radio, instanceID) {
		return Binding{}, ErrInvalidSession
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.closed {
		return Binding{}, ErrObserverClosed
	}
	if _, exists := o.receivers[radio]; !exists && len(o.receivers) >= MaxSessions {
		return Binding{}, ErrSessionLimit
	}
	conn, addr, err := o.bindFresh()
	if err != nil {
		return Binding{}, err
	}
	if o.history != nil {
		if err := o.history.reserve(addr.Port()); err != nil {
			_ = conn.Close()
			return Binding{}, err
		}
	}
	// Retire before publication, even if registering the generation then fails.
	o.used[addr.Port()/64] |= uint64(1) << (addr.Port() % 64)
	token, err := o.tracker.Start(radio, instanceID)
	if err != nil {
		_ = conn.Close()
		return Binding{}, err
	}
	r := &receiver{conn: conn, addr: addr, token: token, tracker: o.tracker,
		stopCh: make(chan struct{}), frames: make(chan datagram, ReceiveQueueDepth)}
	old := o.receivers[radio]
	o.receivers[radio] = r
	if old != nil {
		old.stop()
		old.wg.Wait()
	}
	r.wg.Add(2)
	go r.read()
	go r.consume()
	return Binding{owner: o, token: token, addr: addr}, nil
}

func (o *Observer) bindFresh() (*net.UDPConn, netip.AddrPort, error) {
	if o.history != nil {
		if err := o.history.check(); err != nil {
			return nil, netip.AddrPort{}, err
		}
	}
	for i := 0; i < freshBindAttempts; i++ {
		conn, err := o.listen("udp4", &net.UDPAddr{IP: net.IP{127, 0, 0, 1}})
		if err != nil {
			return nil, netip.AddrPort{}, fmt.Errorf("WSJT-X loopback bind: %w", err)
		}
		addr := conn.LocalAddr().(*net.UDPAddr).AddrPort()
		addr = netip.AddrPortFrom(addr.Addr().Unmap(), addr.Port())
		if addr.Addr() != loopback || addr.Port() == 0 {
			_ = conn.Close()
			return nil, netip.AddrPort{}, errors.New("WSJT-X receiver requires IPv4 loopback")
		}
		if o.used[addr.Port()/64]&(uint64(1)<<(addr.Port()%64)) != 0 {
			_ = conn.Close()
			continue
		}
		if err := conn.SetReadBuffer(receiveBufferSize); err != nil {
			_ = conn.Close()
			return nil, netip.AddrPort{}, fmt.Errorf("WSJT-X receive buffer: %w", err)
		}
		return conn, addr, nil
	}
	return nil, netip.AddrPort{}, ErrNoFreshEndpoint
}

// Stop only removes its exact binding; delayed lifecycle notifications cannot
// tear down a replacement. The endpoint remains retired after removal.
func (o *Observer) Stop(b Binding) bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	r := o.receivers[b.token.radio]
	if b.owner != o || r == nil || r.token != b.token {
		return false
	}
	delete(o.receivers, b.token.radio)
	o.tracker.Remove(b.token)
	r.stop()
	r.wg.Wait()
	return true
}

func (o *Observer) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.closed = true
	for radio, r := range o.receivers {
		o.tracker.Remove(r.token)
		r.stop()
		r.wg.Wait()
		delete(o.receivers, radio)
	}
	if o.history != nil {
		err := o.history.close()
		o.history = nil
		return err
	}
	return nil
}

func (o *Observer) Snapshot(radio string) (ReceiverSnapshot, bool) {
	o.mu.Lock()
	defer o.mu.Unlock()
	r := o.receivers[radio]
	if r == nil {
		return ReceiverSnapshot{}, false
	}
	s, ok := o.tracker.Snapshot(radio)
	if !ok {
		return ReceiverSnapshot{}, false
	}
	return r.snapshot(s), true
}

func (o *Observer) Snapshots() []ReceiverSnapshot {
	o.mu.Lock()
	defer o.mu.Unlock()
	states := o.tracker.Snapshots()
	out := make([]ReceiverSnapshot, 0, len(states))
	for _, s := range states {
		out = append(out, o.receivers[s.Radio].snapshot(s))
	}
	return out
}

type datagram struct {
	data       []byte
	peer       netip.AddrPort
	observedAt time.Time
}

type receiver struct {
	conn    *net.UDPConn
	addr    netip.AddrPort
	token   Token
	tracker *Tracker
	stopCh  chan struct{}
	stopOne sync.Once
	wg      sync.WaitGroup
	frames  chan datagram
	mu      sync.Mutex
	peer    netip.AddrPort
	err     string

	received, queueDrops, nonLoopback, wrongPeer atomic.Uint64
}

func (r *receiver) stop() {
	r.stopOne.Do(func() {
		close(r.stopCh)
		_ = r.conn.Close()
		r.tracker.retire(r.token)
	})
}

func (r *receiver) read() {
	defer r.wg.Done()
	defer r.drain()
	defer r.stop()
	// One extra byte ensures a truncated oversized datagram is rejected rather
	// than interpreted as a complete valid prefix.
	buf := make([]byte, MaxDatagramSize+1)
	for {
		n, peer, err := r.conn.ReadFromUDPAddrPort(buf)
		if err != nil {
			select {
			case <-r.stopCh:
			default:
				r.mu.Lock()
				r.err = err.Error()
				r.mu.Unlock()
			}
			return
		}
		observedAt := r.tracker.clock.Now()
		r.received.Add(1)
		peer = netip.AddrPortFrom(peer.Addr().Unmap(), peer.Port())
		if !peer.Addr().IsLoopback() {
			r.nonLoopback.Add(1)
			continue
		}
		if !r.enqueue(buf[:n], peer, observedAt) {
			return
		}
	}
}

// The only producer has exited before this runs. Release queued packet copies
// after retirement instead of retaining them with a closed observation.
func (r *receiver) drain() {
	for {
		select {
		case <-r.frames:
		default:
			return
		}
	}
}

func (r *receiver) enqueue(b []byte, peer netip.AddrPort, observedAt time.Time) bool {
	select {
	case <-r.stopCh:
		return false
	default:
	}
	// There is one producer, so the queue cannot fill between this check and
	// enqueue. Avoid allocating a datagram copy at all when already full.
	if len(r.frames) == cap(r.frames) {
		r.queueDrops.Add(1)
		return true
	}
	copyOfData := append([]byte(nil), b...)
	select {
	case <-r.stopCh:
		return false
	case r.frames <- datagram{data: copyOfData, peer: peer, observedAt: observedAt}:
		return true
	}
}

func (r *receiver) consume() {
	defer r.wg.Done()
	defer r.stop()
	var peer netip.AddrPort
	for {
		select {
		case <-r.stopCh:
			return
		case frame := <-r.frames:
			if peer.IsValid() && frame.peer != peer {
				r.wrongPeer.Add(1)
				continue
			}
			m, parseErr := Parse(frame.data)
			// A snapshot that sees the first accepted observation must also
			// see its selected peer. No path holds tracker.mu while taking r.mu.
			r.mu.Lock()
			err := r.tracker.accept(r.token, m, parseErr, frame.observedAt)
			if !peer.IsValid() && err == nil {
				peer = frame.peer
				r.peer = peer
			}
			r.mu.Unlock()
			if errors.Is(err, ErrRetired) || err == nil && m.Type == CloseMessage {
				return
			}
		}
	}
}

func (r *receiver) snapshot(s Snapshot) ReceiverSnapshot {
	r.mu.Lock()
	defer r.mu.Unlock()
	return ReceiverSnapshot{Observation: s, Address: r.addr, Peer: r.peer, TransportError: r.err,
		Transport: TransportCounters{Received: r.received.Load(), QueueDrops: r.queueDrops.Load(),
			NonLoopback: r.nonLoopback.Load(), WrongPeer: r.wrongPeer.Load()}}
}
