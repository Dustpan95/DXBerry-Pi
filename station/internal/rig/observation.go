package rig

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ObservationLifetime bounds both the read sequence and the age of its result.
// An observation is evidence for a preview, never authorization for a CAT write.
const ObservationLifetime = 2 * time.Second

var (
	ErrObservationUnavailable  = errors.New("fresh radio observation unavailable")
	ErrObservationUnsupported  = errors.New("radio cannot report frequency, VFO and PTT")
	ErrObservationChanged      = errors.New("radio changed during or after observation")
	ErrObservationTransmitting = errors.New("radio is transmitting")
	ErrObservationBusy         = errors.New("radio observation already in progress")
)

// Observation is an immutable, process-local reading tied to one poller and
// its write/link/capability generation. It contains no JSON or recall token.
type Observation struct {
	poller     *Poller
	epoch      uint64
	writes     int64
	connection uint64
	started    time.Time
	frequency  int64
	vfo        string
}

func (o Observation) FrequencyHz() int64 { return o.frequency }
func (o Observation) VFO() string        { return o.vfo }

// StartedAt is the oldest possible time of a reading in this observation.
func (o Observation) StartedAt() time.Time { return o.started }

// Observe reads PTT, VFO and frequency twice, without using or updating State.
// It takes no write mutex, emits no write intents and does not claim a radio.
// The link must obey context cancellation, as Client does. Reads are separate
// protocol commands, not an atomic hardware snapshot; front-panel changes
// between them (including a change and return) remain a hardware limitation.
func (p *Poller) Observe(ctx context.Context) (Observation, error) {
	if err := ctx.Err(); err != nil {
		return Observation{}, err
	}
	if !p.observing.CompareAndSwap(false, true) {
		return Observation{}, ErrObservationBusy
	}
	defer p.observing.Store(false)
	ctx, cancel := context.WithTimeout(ctx, ObservationLifetime)
	defer cancel()
	p.mu.Lock()
	o := Observation{poller: p, epoch: p.observationEpoch, writes: p.gen.Load(), started: p.clock.Now()}
	connected := p.st.Connected
	known := p.st.Caps != nil && p.caps.CanGetPTT && p.caps.CanGetVFO && len(p.caps.VFOs) > 0
	vfos := slices.Clone(p.caps.VFOs)
	p.mu.Unlock()
	l, ok := p.link.(interface{ ConnectionGeneration() (uint64, bool) })
	if !ok {
		return Observation{}, ErrObservationUnsupported
	}
	var linked bool
	o.connection, linked = l.ConnectionGeneration()
	connected = connected && linked
	if !connected {
		return Observation{}, ErrObservationUnavailable
	}
	if !known {
		return Observation{}, ErrObservationUnsupported
	}
	if err := p.observationValid(o); err != nil {
		return Observation{}, err
	}
	read := func(cmd, label string) (string, error) {
		if err := ctx.Err(); err != nil {
			return "", err
		}
		if err := p.observationValid(o); err != nil {
			return "", err
		}
		r, err := p.link.Do(ctx, cmd)
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		if err != nil || r.RPRT != 0 {
			return "", ErrObservationUnavailable
		}
		if err := p.observationValid(o); err != nil {
			return "", err
		}
		// Unlike display polling, an observation requires exactly one value.
		if len(r.Lines) != 1 {
			return "", ErrObservationUnavailable
		}
		v := strings.TrimSpace(r.Lines[0])
		if key, value, ok := strings.Cut(v, ":"); ok {
			if strings.TrimSpace(key) != label {
				return "", ErrObservationUnavailable
			}
			v = strings.TrimSpace(value)
		}
		if v == "" {
			return "", ErrObservationUnavailable
		}
		return v, nil
	}
	ptt := func() error {
		v, err := read("get_ptt", "PTT")
		if err != nil {
			return err
		}
		switch v {
		case "0":
			return nil
		case "1", "2", "3":
			return ErrObservationTransmitting
		default:
			return ErrObservationUnavailable
		}
	}
	vfo := func() (string, error) {
		v, err := read("get_vfo", "VFO")
		if err != nil {
			return "", err
		}
		if !slices.Contains(vfos, v) {
			return "", ErrObservationUnavailable
		}
		// A relative/aggregate selector does not identify a selected VFO.
		switch strings.ToUpper(v) {
		case "CURR", "CURRVFO", "VFO", "TX", "RX", "ALL", "NONE", "OTHER", "MEM":
			return "", ErrObservationUnsupported
		}
		return v, nil
	}
	freq := func() (int64, error) {
		v, err := read("get_freq", "Frequency")
		if err != nil {
			return 0, err
		}
		if strings.Trim(v, "0123456789") != "" {
			return 0, ErrObservationUnavailable
		}
		hz, err := strconv.ParseInt(v, 10, 64)
		if err != nil || hz <= 0 || hz > 1<<53-1 {
			return 0, ErrObservationUnavailable
		}
		return hz, nil
	}
	if err := ptt(); err != nil {
		return Observation{}, err
	}
	var err error
	if o.vfo, err = vfo(); err != nil {
		return Observation{}, err
	}
	if o.frequency, err = freq(); err != nil {
		return Observation{}, err
	}
	f, err := freq()
	if err != nil {
		return Observation{}, err
	}
	if f != o.frequency {
		return Observation{}, ErrObservationChanged
	}
	v, err := vfo()
	if err != nil {
		return Observation{}, err
	}
	if v != o.vfo {
		return Observation{}, ErrObservationChanged
	}
	if err := ptt(); err != nil {
		return Observation{}, err
	}
	if err := p.observationValid(o); err != nil {
		return Observation{}, err
	}
	return o, nil
}

// ObservationCurrent checks local invalidation and age only. It does not read
// hardware, detect a silent front-panel change or replace recall revalidation.
func (p *Poller) ObservationCurrent(o Observation) bool { return p.observationValid(o) == nil }

func (p *Poller) observationValid(o Observation) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.transmitting.Load() {
		return ErrObservationTransmitting
	}
	l, ok := p.link.(interface{ ConnectionGeneration() (uint64, bool) })
	if !ok {
		return ErrObservationUnsupported
	}
	generation, connected := l.ConnectionGeneration()
	if o.poller != p || o.started.IsZero() || !p.st.Connected || o.epoch != p.observationEpoch ||
		o.writes != p.gen.Load() || p.pending.Load() != 0 || !connected || generation != o.connection {
		return ErrObservationChanged
	}
	age := p.clock.Now().Sub(o.started)
	if age < 0 || age >= ObservationLifetime {
		return ErrObservationChanged
	}
	return nil
}
