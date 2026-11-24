package wsjtx

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"syscall"
)

const (
	historyMagic   = "DXBWSP01"
	historyName    = "ports.v1"
	historyLock    = "owner.lock"
	historyPending = "ports.pending"
	historySize    = len(historyMagic) + 8192 + sha256.Size
)

var ErrEndpointHistory = errors.New("WSJT-X endpoint history unavailable")

// CreateEndpointHistory explicitly initializes a NEW private state directory.
// It refuses an existing directory, even if its history is missing or corrupt.
// Never call this as automatic repair: recreating history could admit packets
// addressed to a previously published endpoint. A failed creation leaves its
// directory for inspection rather than silently resetting it.
func CreateEndpointHistory(path string) error {
	state, err := openPrivateStateDir(path, true)
	if err != nil {
		return historyError(err)
	}
	defer state.close()
	if err := state.replace(historyName, historyPending, encodeHistory([1024]uint64{})); err != nil {
		return historyError(err)
	}
	return historyError(state.syncParent())
}

// NewPersistentObserver opens existing endpoint history exclusively. Published
// ports are durably retired before Start returns, across clean or unclean exits.
// Missing, changed or invalid history is an error; it is never reinitialized.
// Close joins receivers before releasing ownership of the state directory.
// This does not reconcile program settings or activate any station adapter.
func NewPersistentObserver(clock Clock, path string) (*Observer, error) {
	h, err := openEndpointHistory(path)
	if err != nil {
		return nil, err
	}
	o := NewObserver(clock)
	o.history = h
	o.used = h.bits
	return o, nil
}

type endpointHistory struct {
	*privateStateDir
	bits     [1024]uint64
	poisoned error
	// Tests inject uncertain writes here. Any failed write poisons this owner
	// until close/reopen; the previous receiver is left running.
	write func([]byte) error
}

func historyError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrEndpointHistory, err)
}

func openEndpointHistory(path string) (*endpointHistory, error) {
	state, err := openPrivateStateDir(path, false)
	if err != nil {
		return nil, historyError(err)
	}
	h := &endpointHistory{privateStateDir: state}
	h.write = h.replace
	h.bits, err = h.read()
	if err == nil {
		err = h.check()
	}
	if err != nil {
		_ = h.close()
		return nil, historyError(err)
	}
	return h, nil
}

func encodeHistory(bits [1024]uint64) []byte {
	data := make([]byte, historySize)
	copy(data, historyMagic)
	for i, word := range bits {
		binary.BigEndian.PutUint64(data[len(historyMagic)+i*8:], word)
	}
	sum := sha256.Sum256(data[:historySize-sha256.Size])
	copy(data[historySize-sha256.Size:], sum[:])
	return data
}

func (h *endpointHistory) read() ([1024]uint64, error) {
	var bits [1024]uint64
	f, err := h.openFile(historyName, syscall.O_RDONLY)
	if err != nil {
		return bits, err
	}
	defer f.Close()
	data, err := io.ReadAll(io.LimitReader(f, int64(historySize+1)))
	if err != nil {
		return bits, err
	}
	if len(data) != historySize || string(data[:len(historyMagic)]) != historyMagic {
		return bits, errors.New("invalid endpoint history format")
	}
	sum := sha256.Sum256(data[:historySize-sha256.Size])
	if !bytes.Equal(sum[:], data[historySize-sha256.Size:]) {
		return bits, errors.New("endpoint history checksum mismatch")
	}
	for i := range bits {
		bits[i] = binary.BigEndian.Uint64(data[len(historyMagic)+i*8:])
	}
	return bits, nil
}

// The private parent is trusted. This coordinates cooperating owners, not a
// privileged process deliberately replacing files during a filesystem call.
func (h *endpointHistory) check() error {
	if h.poisoned != nil {
		return h.poisoned
	}
	verify := func() error {
		if err := h.checkIdentity(); err != nil {
			return err
		}
		bits, err := h.read()
		if err != nil {
			return err
		}
		if bits != h.bits {
			return errors.New("endpoint history changed outside its owner")
		}
		return nil
	}
	if err := verify(); err != nil {
		h.poisoned = historyError(err)
		return h.poisoned
	}
	return nil
}

func (h *endpointHistory) reserve(port uint16) error {
	if err := h.check(); err != nil {
		return err
	}
	if port == 0 || h.bits[port/64]&(uint64(1)<<(port%64)) != 0 {
		return historyError(errors.New("endpoint already retired or invalid"))
	}
	next := h.bits
	next[port/64] |= uint64(1) << (port % 64)
	if err := h.write(encodeHistory(next)); err != nil {
		h.poisoned = historyError(err)
		return h.poisoned
	}
	h.bits = next
	return nil
}

func (h *endpointHistory) replace(data []byte) error {
	return h.privateStateDir.replace(historyName, historyPending, data)
}
