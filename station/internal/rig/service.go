package rig

import (
	"context"
	"errors"
	"os"
	"time"
)

var ErrServiceUnavailable = errors.New("rig service unavailable")

// ServiceReader inspects only the named local rigctld instance. Its production
// paths and read-only command are fixed; request data cannot select them.
type ServiceReader struct {
	initialized               bool
	proc, cgroups, executable string
	command                   func(context.Context, string, ...string) ([]byte, error)
	peer                      func(context.Context, Endpoint, int) (Peer, error)
}

// NewServiceReader is lazy: it runs no command, opens no file and starts no unit.
func NewServiceReader() *ServiceReader { return newServiceReader() }

// ServiceObservation binds a peer to a running unit and root-owned executable.
// Read alone does not verify rig arguments, physical devices or program-free
// authority. Private evidence is never added to the radio state/event protocol.
type ServiceObservation struct {
	reader  *ServiceReader
	radio   string
	unit    serviceUnit
	peer    Peer
	process [32]byte
}

type serviceUnit struct{ id, load, active, sub, main, invocation, group string }

func (s ServiceObservation) Peer() Peer { return s.peer }

// Same compares two successful inspections, not evidence against live state.
func (s ServiceObservation) Same(other ServiceObservation) bool {
	return s.reader != nil && s == other && s.peer.Same(other.peer)
}

func serviceRadioName(name string) bool {
	if len(name) == 0 || len(name) > 12 || name[0] < 'a' || name[0] > 'z' {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= '0' && c <= '9') {
			return false
		}
	}
	return true
}

// Read requires a separately selected current endpoint. It performs fresh
// unit/process/peer reads on each call, with no cached or port-only fallback.
// Linux root metadata access is required; no privileges are requested or added.
func (r *ServiceReader) Read(ctx context.Context, radio string, endpoint Endpoint) (ServiceObservation, error) {
	if r == nil || !r.initialized || r.command == nil || r.peer == nil || ctx == nil || os.Geteuid() != 0 || !serviceRadioName(radio) || !endpoint.Current() {
		return ServiceObservation{}, ErrServiceUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return ServiceObservation{}, err
	}
	result, err := readService(ctx, r, radio, endpoint)
	if ctx.Err() != nil {
		return ServiceObservation{}, ctx.Err()
	}
	if err != nil {
		return ServiceObservation{}, ErrServiceUnavailable
	}
	return result, nil
}
