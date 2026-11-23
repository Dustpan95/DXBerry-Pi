package rig

import (
	"net"
	"net/netip"
)

type socketIdentity struct {
	device, inode uint64
	uid           uint32
}
type endpointData struct {
	local, peer netip.AddrPort
	socket      socketIdentity
}

func (d endpointData) valid() bool {
	return d.local.IsValid() && d.peer.IsValid() && d.local.Port() != 0 && d.peer.Port() != 0 && d.socket.inode != 0
}

// Endpoint is immutable process-local metadata for one Client connection.
// Its private fields never enter the radio state/events protocol. It does not
// identify a service, prove peer health or authorize a radio operation.
type Endpoint struct {
	client     *Client
	generation uint64
	data       endpointData
}

func (e Endpoint) Local() netip.AddrPort { return e.data.local }
func (e Endpoint) Peer() netip.AddrPort  { return e.data.peer }
func (e Endpoint) SocketInode() uint64   { return e.data.socket.inode }
func (e Endpoint) SocketDevice() uint64  { return e.data.socket.device }
func (e Endpoint) SocketUID() uint32     { return e.data.socket.uid }
func (e Endpoint) Generation() uint64    { return e.generation }

// Current checks the locally known connection identity, not whether its peer
// is currently responsive. Remote loss can remain unknown until a command runs.
func (e Endpoint) Current() bool {
	if e.client == nil {
		return false
	}
	e.client.mu.Lock()
	defer e.client.mu.Unlock()
	return e.client.endpointAvailableLocked() && e.client.endpoint == e
}

// ConnectionEndpoint returns the exact socket metadata captured before that
// connection became available. It performs no I/O, duplicates no descriptor and
// does not inspect the command queue. Unknown metadata does not break normal
// Client commands, but cannot be used as authority for a preview target.
func (c *Client) ConnectionEndpoint() (Endpoint, bool) {
	if c == nil {
		return Endpoint{}, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.endpointAvailableLocked() {
		return Endpoint{}, false
	}
	return c.endpoint, true
}

func (c *Client) endpointAvailableLocked() bool {
	return c.connected && c.ctx != nil && c.ctx.Err() == nil && c.endpoint.client == c && c.endpoint.generation == c.epoch && c.endpoint.data.valid()
}

func captureEndpoint(conn net.Conn) endpointData {
	local, ok := conn.LocalAddr().(*net.TCPAddr)
	if !ok {
		return endpointData{}
	}
	peer, ok := conn.RemoteAddr().(*net.TCPAddr)
	if !ok {
		return endpointData{}
	}
	identity, ok := connectionSocketIdentity(conn)
	if !ok {
		return endpointData{}
	}
	l, p := local.AddrPort(), peer.AddrPort()
	l = netip.AddrPortFrom(l.Addr().Unmap(), l.Port())
	p = netip.AddrPortFrom(p.Addr().Unmap(), p.Port())
	data := endpointData{local: l, peer: p, socket: identity}
	if !data.valid() {
		return endpointData{}
	}
	return data
}
