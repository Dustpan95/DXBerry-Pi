//go:build !linux

package rig

import "context"

func inspectPeer(context.Context, Endpoint, int) (Peer, error) { return Peer{}, ErrPeerUnavailable }
