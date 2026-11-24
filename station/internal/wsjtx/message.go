// Package wsjtx observes a bounded subset of WSJT-X UDP program status. It
// never sends program commands or controls a radio. Settings primitives can
// apply a checked routing plan; no station collection is activated here.
package wsjtx

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"unicode/utf8"
)

const (
	MaxDatagramSize = 16 * 1024
	MaxIDSize       = 256
	MaxTextSize     = 4 * 1024
	magic           = 0xadbccbda
)

type Type uint32

const (
	HeartbeatMessage Type = 0
	StatusMessage    Type = 1
	DecodeMessage    Type = 2
	ClearMessage     Type = 3
	CloseMessage     Type = 6
)

var (
	ErrMalformed         = errors.New("malformed WSJT-X message")
	ErrTooLarge          = errors.New("WSJT-X datagram exceeds limit")
	ErrUnsupportedSchema = errors.New("unsupported WSJT-X schema")
	// ErrIgnoredType distinguishes an unobserved message type from corrupt input.
	// Neither should refresh the observer's liveness or current state.
	ErrIgnoredType = errors.New("unobserved WSJT-X message type")
)

// Message holds exactly one observed payload, or just its identity for Clear and
// Close. Strings are nullable because Qt distinguishes null and empty byte arrays.
type Message struct {
	Schema    uint32
	Type      Type
	ID        string
	Heartbeat *Heartbeat
	Status    *Status
	Decode    *Decode
}

type Heartbeat struct {
	MaxSchema uint32
	Version   *string
	Revision  *string
}

// Status describes the program's report, not independently verified radio state.
// Nullable suffix fields may be absent in an older application version even
// when it uses the same wire schema. The schema only selects Qt serialization.
type Status struct {
	DialFrequency      uint64
	Mode               *string
	DXCall             *string
	Report             *string
	TXMode             *string
	TXEnabled          bool
	Transmitting       bool
	Decoding           bool
	RXOffset           uint32
	TXOffset           uint32
	DECall             *string
	DEGrid             *string
	DXGrid             *string
	TXWatchdog         *bool
	SubMode            *string
	FastMode           *bool
	SpecialOperation   *uint8
	FrequencyTolerance *uint32
	TRPeriod           *uint32
	ConfigurationName  *string
	TXMessage          *string
}

type Decode struct {
	New bool
	// TimeMS is UTC milliseconds since midnight, not a date or arrival time.
	// A null QTime is nil. Never infer its calendar date from this field alone.
	TimeMS         *uint32
	SNR            int32
	DeltaTime      float64
	DeltaFrequency uint32
	Mode           *string
	Text           *string
	LowConfidence  *bool
	OffAir         *bool
}

// Parse reads one complete datagram, accepts schemas 2 and 3, and ignores fields
// appended after the known layout. Required-field truncation, partial optional
// fields, invalid UTF-8 and invalid lengths are errors. No wire length is used
// for an allocation before checking both the field cap and remaining bytes.
func Parse(datagram []byte) (Message, error) {
	if len(datagram) > MaxDatagramSize {
		return Message{}, ErrTooLarge
	}
	d := reader{b: datagram}
	if d.u32() != magic && d.err == nil {
		d.fail("invalid magic")
	}
	schema := d.u32()
	if d.err != nil {
		return Message{}, d.err
	}
	if schema != 2 && schema != 3 {
		return Message{}, ErrUnsupportedSchema
	}
	m := Message{Schema: schema, Type: Type(d.u32())}
	id := d.text(MaxIDSize)
	if d.err != nil {
		return Message{}, d.err
	}
	if id == nil || *id == "" {
		return Message{}, fmt.Errorf("%w: empty instance ID", ErrMalformed)
	}
	m.ID = *id
	switch m.Type {
	case HeartbeatMessage:
		h := &Heartbeat{MaxSchema: 2}
		if d.remaining() > 0 {
			h.MaxSchema = d.u32()
			if h.MaxSchema < 2 {
				d.fail("invalid maximum schema")
			}
		} else if schema == 3 {
			d.fail("missing maximum schema")
		}
		if d.remaining() > 0 {
			h.Version = d.text(MaxTextSize)
		}
		if d.remaining() > 0 {
			h.Revision = d.text(MaxTextSize)
		}
		m.Heartbeat = h
	case StatusMessage:
		s := &Status{
			DialFrequency: d.u64(), Mode: d.text(MaxTextSize), DXCall: d.text(MaxTextSize),
			Report: d.text(MaxTextSize), TXMode: d.text(MaxTextSize),
			TXEnabled: d.boolean(), Transmitting: d.boolean(), Decoding: d.boolean(),
			RXOffset: d.u32(), TXOffset: d.u32(), DECall: d.text(MaxTextSize),
			DEGrid: d.text(MaxTextSize), DXGrid: d.text(MaxTextSize),
		}
		s.TXWatchdog = d.optionalBool()
		if d.remaining() > 0 {
			s.SubMode = d.text(MaxTextSize)
		}
		s.FastMode = d.optionalBool()
		if d.remaining() > 0 {
			value := d.u8()
			s.SpecialOperation = &value
		}
		s.FrequencyTolerance = d.optionalU32()
		s.TRPeriod = d.optionalU32()
		if d.remaining() > 0 {
			s.ConfigurationName = d.text(MaxTextSize)
		}
		if d.remaining() > 0 {
			s.TXMessage = d.text(MaxTextSize)
		}
		m.Status = s
	case DecodeMessage:
		v := &Decode{New: d.boolean(), TimeMS: d.qtime(), SNR: int32(d.u32()),
			DeltaTime: math.Float64frombits(d.u64()), DeltaFrequency: d.u32(),
			Mode: d.text(MaxTextSize), Text: d.text(MaxTextSize)}
		if math.IsNaN(v.DeltaTime) || math.IsInf(v.DeltaTime, 0) {
			d.fail("nonfinite decode time offset")
		}
		v.LowConfidence = d.optionalBool()
		v.OffAir = d.optionalBool()
		m.Decode = v
	case ClearMessage, CloseMessage:
		// Outbound Clear and Close have no payload. Do not reinterpret appended
		// extension bytes as a command or as a second message.
	default:
		return Message{}, ErrIgnoredType
	}
	if d.err != nil {
		return Message{}, d.err
	}
	return m, nil
}

type reader struct {
	b   []byte
	err error
}

func (d *reader) fail(reason string) {
	if d.err == nil {
		d.err = fmt.Errorf("%w: %s", ErrMalformed, reason)
	}
}

func (d *reader) remaining() int {
	if d.err != nil {
		return 0
	}
	return len(d.b)
}

func (d *reader) take(n int) []byte {
	if d.err != nil {
		return nil
	}
	if n > len(d.b) {
		d.fail("truncated field")
		return nil
	}
	b := d.b[:n]
	d.b = d.b[n:]
	return b
}

func (d *reader) u8() uint8 {
	if b := d.take(1); len(b) == 1 {
		return b[0]
	}
	return 0
}

func (d *reader) u32() uint32 {
	if b := d.take(4); len(b) == 4 {
		return binary.BigEndian.Uint32(b)
	}
	return 0
}

func (d *reader) u64() uint64 {
	if b := d.take(8); len(b) == 8 {
		return binary.BigEndian.Uint64(b)
	}
	return 0
}

func (d *reader) text(limit uint32) *string {
	n := d.u32()
	if d.err != nil || n == math.MaxUint32 {
		return nil
	}
	if n > limit {
		d.fail("text exceeds limit")
		return nil
	}
	b := d.take(int(n))
	if d.err != nil {
		return nil
	}
	if !utf8.Valid(b) {
		d.fail("invalid UTF-8")
		return nil
	}
	s := string(b)
	return &s
}

func (d *reader) boolean() bool {
	v := d.u8()
	if v > 1 {
		d.fail("invalid boolean")
	}
	return v == 1
}

func (d *reader) optionalBool() *bool {
	if d.remaining() == 0 {
		return nil
	}
	v := d.boolean()
	return &v
}

func (d *reader) optionalU32() *uint32 {
	if d.remaining() == 0 {
		return nil
	}
	v := d.u32()
	if v == math.MaxUint32 {
		return nil
	}
	return &v
}

func (d *reader) qtime() *uint32 {
	v := d.u32()
	if v == math.MaxUint32 {
		return nil
	}
	if v >= 24*60*60*1000 {
		d.fail("invalid QTime")
	}
	return &v
}
