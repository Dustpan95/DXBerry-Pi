package rig

import (
	"context"
	"errors"
)

// ErrPeerUnavailable means the expected process could not be bound to the
// current connection. It must not be replaced with cached or port-only evidence.
var ErrPeerUnavailable = errors.New("rig peer unavailable")

// Peer is private, immutable evidence from a read-only process/socket inspection.
// It does not establish a trusted executable, service, configuration, device or
// program-free state. No fields are included in radio state or JSON responses.
type Peer struct {
	endpoint               Endpoint
	pid                    int
	start                  uint64
	uid                    uint32
	netns                  string
	listener, accepted     uint64
	listenerFD, acceptedFD string
	process                [32]byte
	arguments              [32]byte
}

func (p Peer) PID() int              { return p.pid }
func (p Peer) StartTicks() uint64    { return p.start }
func (p Peer) UID() uint32           { return p.uid }
func (p Peer) ListenerInode() uint64 { return p.listener }
func (p Peer) SocketInode() uint64   { return p.accepted }

// Same compares two successful inspections, not a snapshot with live state.
// Call InspectPeer again; this method performs no I/O and extends no lifetime.
func (p Peer) Same(other Peer) bool {
	return p.pid > 0 && p.start != 0 && p.listener != 0 && p.accepted != 0 && p == other
}

// InspectPeer binds the current IPv4 loopback connection to an expected PID's
// listening and reverse established sockets. The PID must come from a separate
// trusted service check; finding a socket owner does not make it trusted. Linux
// metadata is read on demand, with no protocol commands or service operations.
func (e Endpoint) InspectPeer(ctx context.Context, pid int) (Peer, error) {
	if ctx == nil || pid <= 0 || !e.Current() || !e.Local().Addr().Is4() || !e.Peer().Addr().Is4() || e.Local().Addr().String() != "127.0.0.1" || e.Peer().Addr().String() != "127.0.0.1" {
		return Peer{}, ErrPeerUnavailable
	}
	if err := ctx.Err(); err != nil {
		return Peer{}, err
	}
	return inspectPeer(ctx, e, pid)
}
