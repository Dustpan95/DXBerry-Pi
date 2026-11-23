package wsjtx

import (
	"bytes"
	"encoding/binary"
	"errors"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const fixtureID = "WSJT-X - radio1"
const fixtureHeaderSize = 16 + len(fixtureID)

func fixture(t testing.TB, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name+".dat"))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func parsed(t testing.TB, name string) Message {
	t.Helper()
	m, err := Parse(fixture(t, name))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func value[T comparable](t testing.TB, got *T, want T) {
	t.Helper()
	if got == nil || *got != want {
		t.Fatalf("got %v, want pointer to %v", got, want)
	}
}

func TestQtMessages(t *testing.T) {
	for _, schema := range []uint32{2, 3} {
		t.Run(strconv.Itoa(int(schema)), func(t *testing.T) {
			suffix := "-s" + strconv.Itoa(int(schema))
			m := parsed(t, "heartbeat"+suffix)
			if m.Schema != schema || m.ID != fixtureID || m.Type != HeartbeatMessage || m.Heartbeat.MaxSchema != 3 {
				t.Fatalf("unexpected heartbeat: %+v", m)
			}
			value(t, m.Heartbeat.Version, "3.0.2")
			value(t, m.Heartbeat.Revision, "r-test")

			m = parsed(t, "status-full"+suffix)
			s := m.Status
			if m.Type != StatusMessage || s.DialFrequency != 14074000 || !s.TXEnabled || s.Transmitting || !s.Decoding || s.RXOffset != 1234 || s.TXOffset != 1500 {
				t.Fatalf("unexpected status: %+v", s)
			}
			value(t, s.Mode, "FT8")
			value(t, s.DXCall, "K1ABC")
			value(t, s.Report, "-07")
			value(t, s.TXMode, "FT8")
			value(t, s.DECall, "W1AW")
			value(t, s.DEGrid, "FN31")
			value(t, s.DXGrid, "EM00")
			value(t, s.TXWatchdog, false)
			value(t, s.SubMode, "A")
			value(t, s.FastMode, true)
			value(t, s.SpecialOperation, uint8(7))
			value(t, s.FrequencyTolerance, uint32(50))
			value(t, s.TRPeriod, uint32(15))
			value(t, s.ConfigurationName, "Test α")
			value(t, s.TXMessage, "CQ W1AW FN31")

			d := parsed(t, "decode-full"+suffix).Decode
			if !d.New || d.SNR != -23 || d.DeltaTime != -0.25 || d.DeltaFrequency != 1750 {
				t.Fatalf("unexpected decode: %+v", d)
			}
			value(t, d.TimeMS, uint32(86399999))
			value(t, d.Mode, "~")
			value(t, d.Text, "CQ K1ABC EM00")
			value(t, d.LowConfidence, true)
			value(t, d.OffAir, true)
		})
	}
}

func TestMissingOptionalFieldsRemainUnknown(t *testing.T) {
	s := parsed(t, "status-core-s2").Status
	if s.TXWatchdog != nil || s.SubMode != nil || s.FastMode != nil || s.SpecialOperation != nil || s.FrequencyTolerance != nil || s.TRPeriod != nil || s.ConfigurationName != nil || s.TXMessage != nil {
		t.Fatalf("invented status suffix: %+v", s)
	}
	d := parsed(t, "decode-core-s3").Decode
	if d.LowConfidence != nil || d.OffAir != nil {
		t.Fatalf("invented confidence or off-air flag: %+v", d)
	}
	h := parsed(t, "heartbeat-legacy").Heartbeat
	if h.MaxSchema != 2 || h.Version != nil || h.Revision != nil {
		t.Fatalf("unexpected legacy heartbeat: %+v", h)
	}
}

func TestQtNullAndEmptyAreDifferent(t *testing.T) {
	d := parsed(t, "decode-null").Decode
	if d.New || d.TimeMS != nil || d.Mode != nil {
		t.Fatalf("lost null fields: %+v", d)
	}
	value(t, d.Text, "")
	value(t, d.LowConfidence, false)
	value(t, d.OffAir, false)
	s := parsed(t, "status-null").Status
	if s.Mode != nil || s.TXMode != nil || s.DECall != nil || s.FrequencyTolerance != nil || s.TRPeriod != nil {
		t.Fatalf("lost null/sentinel fields: %+v", s)
	}
	value(t, s.DXCall, "")
	value(t, s.TXMessage, "")
}

func TestClearCloseAndUnknownType(t *testing.T) {
	for _, schema := range []string{"2", "3"} {
		for _, tc := range []struct {
			name string
			kind Type
		}{{"clear", ClearMessage}, {"close", CloseMessage}} {
			m := parsed(t, tc.name+"-s"+schema)
			if m.Type != tc.kind || m.ID != fixtureID || m.Heartbeat != nil || m.Status != nil || m.Decode != nil {
				t.Fatalf("unexpected message: %+v", m)
			}
		}
	}
	if _, err := Parse(fixture(t, "ignored")); !errors.Is(err, ErrIgnoredType) {
		t.Fatalf("unknown extension type: %v", err)
	}
}

func TestEveryRequiredPrefixTruncationFails(t *testing.T) {
	for _, name := range []string{"status-core-s2", "status-core-s3", "decode-core-s2", "decode-core-s3", "clear-s3"} {
		b := fixture(t, name)
		for n := 0; n < len(b); n++ {
			if _, err := Parse(b[:n]); !errors.Is(err, ErrMalformed) {
				t.Fatalf("%s truncated at %d: %v", name, n, err)
			}
		}
	}
}

func TestOnlyCompleteOptionalFieldsAreAccepted(t *testing.T) {
	for _, schema := range []string{"2", "3"} {
		name := "status-full-s" + schema
		b := fixture(t, name)
		boundaries, err := os.ReadFile(filepath.Join("testdata", name+".boundaries"))
		if err != nil {
			t.Fatal(err)
		}
		valid := map[int]bool{}
		for _, text := range strings.Fields(string(boundaries)) {
			n, err := strconv.Atoi(text)
			if err != nil {
				t.Fatal(err)
			}
			valid[n] = true
		}
		for n := len(fixture(t, "status-core-s"+schema)); n <= len(b); n++ {
			_, err := Parse(b[:n])
			if valid[n] && err != nil || !valid[n] && !errors.Is(err, ErrMalformed) {
				t.Fatalf("schema %s prefix %d (field boundary %v): %v", schema, n, valid[n], err)
			}
		}
	}
}

func TestAppendedFieldsAreIgnored(t *testing.T) {
	for _, name := range []string{"status-full-s3", "decode-full-s2", "heartbeat-s3", "clear-s2", "close-s3"} {
		b := append(fixture(t, name), 0xde, 0xad, 0xbe, 0xef)
		if _, err := Parse(b); err != nil {
			t.Fatalf("%s with future extension: %v", name, err)
		}
	}
}

func header(id []byte) []byte {
	b := make([]byte, 16+len(id))
	binary.BigEndian.PutUint32(b, magic)
	binary.BigEndian.PutUint32(b[4:], 3)
	binary.BigEndian.PutUint32(b[8:], uint32(CloseMessage))
	binary.BigEndian.PutUint32(b[12:], uint32(len(id)))
	copy(b[16:], id)
	return b
}

func replace32(b []byte, at int, v uint32) []byte {
	binary.BigEndian.PutUint32(b[at:], v)
	return b
}

func TestMalformedAndBoundedFields(t *testing.T) {
	for _, schema := range []uint32{0, 1, 4, math.MaxUint32} {
		if _, err := Parse(replace32(header([]byte(fixtureID)), 4, schema)); !errors.Is(err, ErrUnsupportedSchema) {
			t.Fatalf("schema %d: %v", schema, err)
		}
	}
	for _, id := range [][]byte{nil, {0xff}, bytes.Repeat([]byte{'x'}, MaxIDSize+1)} {
		if _, err := Parse(header(id)); !errors.Is(err, ErrMalformed) {
			t.Fatalf("invalid ID length %d: %v", len(id), err)
		}
	}
	if _, err := Parse(header(bytes.Repeat([]byte{'x'}, MaxIDSize))); err != nil {
		t.Fatal(err)
	}
	cases := map[string][]byte{
		"bad magic":        replace32(header([]byte(fixtureID)), 0, 0),
		"null ID":          replace32(header(nil), 12, math.MaxUint32),
		"overflow ID":      replace32(header(nil), 12, math.MaxUint32-1),
		"truncated ID":     replace32(header(nil), 12, 1),
		"oversized text":   replace32(fixture(t, "status-core-s3"), fixtureHeaderSize+8, MaxTextSize+1),
		"overflow text":    replace32(fixture(t, "status-core-s3"), fixtureHeaderSize+8, math.MaxUint32-1),
		"invalid QTime":    replace32(fixture(t, "decode-full-s3"), fixtureHeaderSize+1, 86400000),
		"zero max schema":  replace32(fixture(t, "heartbeat-s3"), fixtureHeaderSize, 0),
		"no max schema s3": replace32(fixture(t, "heartbeat-legacy"), 4, 3),
	}
	invalidUTF8 := fixture(t, "status-core-s3")
	invalidUTF8[fixtureHeaderSize+12] = 0xff
	cases["invalid UTF-8"] = invalidUTF8
	invalidBool := fixture(t, "decode-full-s3")
	invalidBool[fixtureHeaderSize] = 2
	cases["invalid boolean"] = invalidBool
	for name, number := range map[string]float64{"NaN": math.NaN(), "infinity": math.Inf(1)} {
		b := fixture(t, "decode-full-s3")
		binary.BigEndian.PutUint64(b[fixtureHeaderSize+9:], math.Float64bits(number))
		cases[name] = b
	}
	for name, b := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := Parse(b); !errors.Is(err, ErrMalformed) {
				t.Fatalf("got %v", err)
			}
		})
	}
	if _, err := Parse(make([]byte, MaxDatagramSize+1)); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized packet: %v", err)
	}
	// A known message can carry a bounded unknown suffix up to the packet cap.
	b := header([]byte(fixtureID))
	b = append(b, make([]byte, MaxDatagramSize-len(b))...)
	if _, err := Parse(b); err != nil {
		t.Fatal(err)
	}
}

func TestParsedStringsSurviveReceiveBufferReuse(t *testing.T) {
	b := fixture(t, "status-full-s3")
	m, err := Parse(b)
	if err != nil {
		t.Fatal(err)
	}
	clear(b)
	if m.ID != fixtureID {
		t.Fatal("ID aliases the input buffer")
	}
	value(t, m.Status.Mode, "FT8")
	value(t, m.Status.ConfigurationName, "Test α")
}

func FuzzParse(f *testing.F) {
	files, err := filepath.Glob("testdata/*.dat")
	if err != nil || len(files) == 0 {
		f.Fatalf("missing Qt fixtures: %v", err)
	}
	for _, name := range files {
		b, err := os.ReadFile(name)
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
	}
	f.Add([]byte{})
	f.Add(replace32(header(nil), 12, math.MaxUint32-1))
	f.Fuzz(func(t *testing.T, b []byte) {
		m, err := Parse(b)
		if err != nil {
			return
		}
		if m.Schema < 2 || m.Schema > 3 || len(m.ID) == 0 || len(m.ID) > MaxIDSize {
			t.Fatalf("invalid accepted header: %+v", m)
		}
		payloads := 0
		if m.Heartbeat != nil {
			payloads++
		}
		if m.Status != nil {
			payloads++
		}
		if m.Decode != nil {
			payloads++
			if math.IsNaN(m.Decode.DeltaTime) || math.IsInf(m.Decode.DeltaTime, 0) || m.Decode.TimeMS != nil && *m.Decode.TimeMS >= 86400000 {
				t.Fatal("invalid accepted decode")
			}
		}
		if m.Type == ClearMessage || m.Type == CloseMessage {
			if payloads != 0 {
				t.Fatal("unexpected payload")
			}
		} else if payloads != 1 {
			t.Fatal("missing payload")
		}
	})
}
