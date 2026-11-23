//go:build linux

package pat

import (
	"context"
	"path/filepath"
	"time"
)

const managedReadTimeout = 3 * time.Second

// Observation has no process IDs, invocation IDs, paths or package metadata.
// It is a last observation, not a promise the program remains running afterward.
type Observation struct {
	Radio  string `json:"radio"`
	Status Status `json:"status"`
}

// Reader verifies the sole managed Pat instance before and after each status
// request. It has no background activity, retained observations or controls.
type Reader struct {
	gate  chan struct{}
	probe func(context.Context, string) (instance, error)
	read  func(context.Context) (Status, error)
}

// NewReader prepares a lazy read-only source. records is the station's trusted
// radio file, never an HTTP parameter. No program or network request is started.
func NewReader(records string) (*Reader, error) {
	if !filepath.IsAbs(records) || filepath.Clean(records) != records {
		return nil, ErrUnavailable
	}
	authority := newAuthority(records)
	client := newClient()
	return &Reader{gate: make(chan struct{}, 1), probe: authority.probe, read: client.read}, nil
}

func (r *Reader) Read(ctx context.Context, radio string) (Observation, error) {
	if r == nil || r.gate == nil || r.probe == nil || r.read == nil || !validRadio(radio) {
		return Observation{}, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, managedReadTimeout)
	defer cancel()
	select {
	case r.gate <- struct{}{}:
		defer func() { <-r.gate }()
	case <-ctx.Done():
		return Observation{}, ErrUnavailable
	}
	if ctx.Err() != nil {
		return Observation{}, ErrUnavailable
	}
	before, err := r.probe(ctx, radio)
	if err != nil || ctx.Err() != nil {
		return Observation{}, ErrUnavailable
	}
	status, err := r.read(ctx)
	if err != nil || ctx.Err() != nil {
		return Observation{}, ErrUnavailable
	}
	after, err := r.probe(ctx, radio)
	if err != nil || before != after || ctx.Err() != nil {
		return Observation{}, ErrUnavailable
	}
	return Observation{Radio: radio, Status: status}, nil
}
