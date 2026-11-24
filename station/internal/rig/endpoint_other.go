//go:build !linux

package rig

import "net"

// Unavailable metadata must not prevent the existing protocol client working.
func connectionSocketIdentity(net.Conn) (socketIdentity, bool) { return socketIdentity{}, false }
