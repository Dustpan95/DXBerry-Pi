package rig

import (
	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
	"net/netip"
)

// Connection binds an existing manager entry/poller, reduced radio record and
// exact Client socket in one immutable snapshot. It is private server evidence,
// not a complete radio registration, capability or effective-process proof.
type Connection struct {
	manager  *Manager
	entry    *entry
	radio    radios.Radio
	endpoint Endpoint
}

func (c Connection) Radio() radios.Radio { return c.radio }
func (c Connection) Endpoint() Endpoint  { return c.endpoint }
func (c Connection) Poller() *Poller {
	if c.entry == nil {
		return nil
	}
	return c.entry.poller
}

// Current refuses a removed/replaced entry, changed reduced record or replaced
// socket. Pin/configuration and fresh program evidence must be checked separately.
func (c Connection) Current() bool {
	if c.manager == nil || c.entry == nil {
		return false
	}
	c.manager.mu.Lock()
	defer c.manager.mu.Unlock()
	e, ok := c.manager.entries[c.radio.Name]
	return ok && e == c.entry && e.radio == c.radio && configuredEndpoint(c.radio, c.endpoint) && c.endpoint.Current()
}

// ConnectionSnapshot avoids combining Radio and Client lookups from different
// manager entries. It opens no connection and starts/restarts no radio or unit.
func (m *Manager) ConnectionSnapshot(name string) (Connection, bool) {
	if m == nil {
		return Connection{}, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.entries[name]
	if !ok || e.radio.Name != name {
		return Connection{}, false
	}
	client, ok := e.conn.(*Client)
	if !ok {
		return Connection{}, false
	}
	endpoint, ok := client.ConnectionEndpoint()
	if !ok || !configuredEndpoint(e.radio, endpoint) {
		return Connection{}, false
	}
	return Connection{m, e, e.radio, endpoint}, true
}

// The manager's configured transport is the local per-radio rigctld port.
// A custom/mismatched dialer must not silently qualify a different endpoint.
func configuredEndpoint(r radios.Radio, e Endpoint) bool {
	return r.Port > 0 && r.Port <= 65535 && e.Peer().Addr() == netip.AddrFrom4([4]byte{127, 0, 0, 1}) && e.Peer().Port() == uint16(r.Port)
}
