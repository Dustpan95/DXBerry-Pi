package presets

import (
	"encoding/json"
	"strings"
	"testing"
)

func sampleLibrary() Library {
	return Library{1, "radio1", strings.Repeat("1", 64), []Definition{{strings.Repeat("a", 32), "20 m receive", 14074000}}}
}
func encode(t *testing.T, v any) []byte {
	t.Helper()
	raw, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestDefinitionAndRequestRoundTrips(t *testing.T) {
	l := sampleLibrary()
	got, err := DecodeLibrary(encode(t, l))
	if err != nil || got.Presets[0] != l.Presets[0] {
		t.Fatalf("document: %+v %v", got, err)
	}
	requests := []Request{{Version: 1, Operation: "list", Radio: "radio1"}, {1, "create", "radio1", Absent, "", "名字 📻", MaxFrequencyHz}, {1, "update", "radio1", l.Revision, l.Presets[0].ID, "new label", 1}, {Version: 1, Operation: "delete", Radio: "radio1", ExpectedRevision: l.Revision, ID: l.Presets[0].ID}}
	for _, want := range requests {
		got, err := DecodeRequest(encode(t, want))
		if err != nil || got != want {
			t.Fatalf("request: %+v %v, want %+v", got, err, want)
		}
	}
	valid := `{"version":1,"operation":"create","radio":"radio1","expected_revision":"absent","label":"\ud83d\udcfb","frequency_hz":14074000}`
	if r, err := DecodeRequest([]byte(valid)); err != nil || r.Label != "📻" {
		t.Fatalf("valid surrogate pair: %+v %v", r, err)
	}
}

func TestRequestRefusesAmbiguousOrUnsupportedInput(t *testing.T) {
	base := `{"version":1,"operation":"create","radio":"radio1","expected_revision":"absent","label":"receive","frequency_hz":14074000}`
	cases := []string{
		`null`, `[]`, base + ` {}`, base + ` false`, base[:len(base)-1],
		strings.Replace(base, `"radio":`, `"Radio":`, 1),
		strings.Replace(base, `"radio":"radio1"`, `"radio":"radio1","\u0072adio":"radio2"`, 1),
		strings.Replace(base, `"label":"receive"`, `"label":"receive","label":"other"`, 1),
		strings.Replace(base, `"label":"receive"`, `"label":null`, 1),
		strings.Replace(base, `"label":"receive"`, `"label":"\ud800"`, 1),
		strings.Replace(base, `"label":"receive"`, `"label":"\udc00"`, 1),
		strings.Replace(base, `"label":"receive"`, `"label":"\ud800\u0041"`, 1),
		strings.Replace(base, `"label":"receive"`, `"label":"\u0000"`, 1),
		strings.Replace(base, `"label":"receive"`, `"label":"  "`, 1),
		strings.Replace(base, `"label":"receive"`, `"label":"`+strings.Repeat("x", 81)+`"`, 1),
		strings.Replace(base, `14074000`, `9007199254740992`, 1),
		strings.Replace(base, `14074000`, `1e6`, 1), strings.Replace(base, `14074000`, `14074000.0`, 1),
		strings.Replace(base, `14074000`, `"14074000"`, 1), strings.Replace(base, `14074000`, `null`, 1), strings.Replace(base, `14074000`, `-1`, 1),
		strings.Replace(base, `"radio1"`, `"../outside"`, 1), strings.Replace(base, `"create"`, `"recall"`, 1),
		strings.Replace(base, `"version":1`, `"version":2`, 1), strings.Replace(base, `"version":1`, `"version":"1"`, 1),
		strings.Replace(base, `"label":"receive",`, "", 1),
		strings.Replace(base, `"expected_revision":"absent",`, "", 1),
		strings.Replace(base, `"expected_revision":"absent"`, `"expected_revision":null`, 1),
		strings.Replace(base, `"expected_revision":"absent"`, `"expected_revision":"bad"`, 1),
		strings.Replace(base, `"frequency_hz":14074000`, `"frequency_hz":14074000,"destination":"127.0.0.1:4532"`, 1),
		`{"version":1,"operation":"list","radio":"radio1","expected_revision":"absent"}`,
		`{"version":1,"operation":"list","radio":"radio1","id":""}`,
		strings.Repeat(" ", MaxBytes+1) + base,
	}
	for i, raw := range cases {
		if _, err := DecodeRequest([]byte(raw)); err != ErrInvalid {
			t.Errorf("case %d accepted or unsanitized error: %v", i, err)
		}
	}
	raw := []byte(base)
	raw[strings.Index(base, "receive")] = 0xff
	if _, err := DecodeRequest(raw); err != ErrInvalid {
		t.Fatal("invalid UTF-8 accepted")
	}
}

func TestLibraryRefusesCorruptionAndBounds(t *testing.T) {
	base := string(encode(t, sampleLibrary()))
	cases := []string{
		strings.Replace(base, `"presets":[`, `"presets":null,"ignored":[`, 1),
		strings.Replace(base, `"presets":`, `"Presets":`, 1),
		strings.Replace(base, `"frequency_hz":`, `"Frequency_hz":`, 1),
		strings.Replace(base, `"id":`, `"id":null,"second_id":`, 1),
		strings.Replace(base, `"version":1`, `"version":1,"version":1`, 1),
		strings.Replace(base, `"revision":"`+strings.Repeat("1", 64)+`"`, `"revision":"absent"`, 1),
		strings.Replace(base, `"presets":[`, `"presets":[null,`, 1),
		strings.Replace(base, `"label":"20 m receive"`, `"label":false`, 1),
		strings.Replace(base, `"frequency_hz":14074000`, `"frequency_hz":0`, 1),
	}
	for i, raw := range cases {
		if _, err := DecodeLibrary([]byte(raw)); err != ErrInvalid {
			t.Errorf("case %d accepted: %v", i, err)
		}
	}
	l := sampleLibrary()
	l.Presets = append(l.Presets, l.Presets[0])
	if _, err := DecodeLibrary(encode(t, l)); err != ErrInvalid {
		t.Fatal("duplicate ID accepted")
	}
	l = sampleLibrary()
	l.Presets = nil
	if _, err := DecodeLibrary(encode(t, l)); err != ErrInvalid {
		t.Fatal("null list accepted")
	}
	l = sampleLibrary()
	for len(l.Presets) < MaxPresets+1 {
		p := l.Presets[0]
		p.ID = strings.Repeat("0", 30) + strconvHex(len(l.Presets))
		l.Presets = append(l.Presets, p)
	}
	if _, err := DecodeLibrary(encode(t, l)); err != ErrInvalid {
		t.Fatal("too many definitions accepted")
	}
}

func strconvHex(v int) string { const h = "0123456789abcdef"; return string([]byte{h[v/16], h[v%16]}) }

func FuzzPresetDecoders(f *testing.F) {
	raw, _ := json.Marshal(sampleLibrary())
	f.Add(raw)
	f.Add([]byte(`{"version":1,"operation":"list","radio":"radio1"}`))
	f.Add([]byte(`{"presets":[{"id":null}]}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		if l, err := DecodeLibrary(raw); err == nil {
			if !l.valid() {
				t.Fatal("invalid decoded library")
			}
			next, err := DecodeLibrary(encode(t, l))
			if err != nil || next.Revision != l.Revision {
				t.Fatal("library not stable")
			}
		}
		if r, err := DecodeRequest(raw); err == nil {
			if !r.valid() {
				t.Fatal("invalid decoded request")
			}
			next, err := DecodeRequest(encode(t, r))
			if err != nil || next != r {
				t.Fatal("request not stable")
			}
		}
	})
}
