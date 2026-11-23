// Package presets stores frequency definitions. It has no radio control, program
// lifecycle, network destination or transmitter operation.
package presets

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxBytes             = 128 << 10
	MaxPresets           = 64
	MaxFrequencyHz int64 = 1<<53 - 1
	Absent               = "absent"
)

var (
	ErrInvalid     = errors.New("invalid preset request")
	ErrConflict    = errors.New("preset definitions changed; reload before editing")
	ErrUnavailable = errors.New("preset definitions are unavailable")
	ErrUnknown     = errors.New("preset save outcome is uncertain; reload before editing")
	ErrNotFound    = errors.New("no such preset")
	ErrLimit       = errors.New("this radio already has 64 presets")
	radioName      = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
)

type Definition struct {
	ID          string `json:"id"`
	Label       string `json:"label"`
	FrequencyHz int64  `json:"frequency_hz"`
}

type Library struct {
	Version  int          `json:"version"`
	Radio    string       `json:"radio"`
	Revision string       `json:"revision"`
	Presets  []Definition `json:"presets"`
}

// Request is a list, create, update or delete operation. JSON decoding requires
// the exact field set for that operation, including its revision precondition.
type Request struct {
	Version          int    `json:"version"`
	Operation        string `json:"operation"`
	Radio            string `json:"radio"`
	ExpectedRevision string `json:"expected_revision,omitempty"`
	ID               string `json:"id,omitempty"`
	Label            string `json:"label,omitempty"`
	FrequencyHz      int64  `json:"frequency_hz,omitempty"`
}

func validHex(s string, n int) bool {
	return len(s) == n && strings.Trim(s, "0123456789abcdef") == ""
}

func validLabel(s string) bool {
	if !utf8.ValidString(s) || len(s) > 320 || utf8.RuneCountInString(s) > 80 || strings.TrimSpace(s) == "" {
		return false
	}
	for _, r := range s {
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

func (p Definition) valid() bool {
	return validHex(p.ID, 32) && validLabel(p.Label) && p.FrequencyHz > 0 && p.FrequencyHz <= MaxFrequencyHz
}

func (l Library) valid() bool {
	if l.Version != 1 || !radioName.MatchString(l.Radio) || !validHex(l.Revision, 64) || l.Presets == nil || len(l.Presets) > MaxPresets {
		return false
	}
	seen := map[string]bool{}
	for _, p := range l.Presets {
		if !p.valid() || seen[p.ID] {
			return false
		}
		seen[p.ID] = true
	}
	return true
}

func empty(radio string) Library {
	return Library{Version: 1, Radio: radio, Revision: Absent, Presets: []Definition{}}
}

func (r Request) valid() bool {
	if r.Version != 1 || !radioName.MatchString(r.Radio) {
		return false
	}
	switch r.Operation {
	case "list":
		return r.ExpectedRevision == "" && r.ID == "" && r.Label == "" && r.FrequencyHz == 0
	case "create":
		return (r.ExpectedRevision == Absent || validHex(r.ExpectedRevision, 64)) && r.ID == "" && validLabel(r.Label) && r.FrequencyHz > 0 && r.FrequencyHz <= MaxFrequencyHz
	case "update":
		return validHex(r.ExpectedRevision, 64) && (Definition{r.ID, r.Label, r.FrequencyHz}).valid()
	case "delete":
		return validHex(r.ExpectedRevision, 64) && validHex(r.ID, 32) && r.Label == "" && r.FrequencyHz == 0
	}
	return false
}

// Encoding/json replaces unpaired surrogate escapes. Refuse them before decoding
// so labels and keys are never silently repaired into different text.
func validInput(raw []byte) bool {
	if len(raw) > MaxBytes || !utf8.Valid(raw) {
		return false
	}
	for i := 0; i < len(raw); {
		if raw[i] != '\\' {
			i++
			continue
		}
		if i+1 >= len(raw) {
			return false
		}
		if raw[i+1] != 'u' {
			i += 2
			continue
		}
		if i+6 > len(raw) {
			return false
		}
		v, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
		if err != nil {
			return false
		}
		if v >= 0xdc00 && v <= 0xdfff {
			return false
		}
		if v >= 0xd800 && v <= 0xdbff {
			if i+12 > len(raw) || raw[i+6] != '\\' || raw[i+7] != 'u' {
				return false
			}
			w, err := strconv.ParseUint(string(raw[i+8:i+12]), 16, 16)
			if err != nil || w < 0xdc00 || w > 0xdfff {
				return false
			}
			i += 12
		} else {
			i += 6
		}
	}
	return true
}

// ValidJSONEncoding checks the preset byte bound, UTF-8 and surrogate escapes.
// It prevents an outer JSON decoder from silently repairing input text before
// schema validation; callers must still check JSON syntax and exact fields.
func ValidJSONEncoding(raw []byte) bool { return validInput(raw) }

type field func(*json.Decoder) error

func object(d *json.Decoder, fields map[string]field) (map[string]bool, error) {
	t, err := d.Token()
	if err != nil || t != json.Delim('{') {
		return nil, ErrInvalid
	}
	seen := map[string]bool{}
	for d.More() {
		t, err = d.Token()
		key, ok := t.(string)
		fn, known := fields[key]
		if err != nil || !ok || !known || seen[key] {
			return nil, ErrInvalid
		}
		seen[key] = true
		if err := fn(d); err != nil {
			return nil, ErrInvalid
		}
	}
	if t, err = d.Token(); err != nil || t != json.Delim('}') {
		return nil, ErrInvalid
	}
	return seen, nil
}

func textField(dst *string) field {
	return func(d *json.Decoder) error {
		t, err := d.Token()
		s, ok := t.(string)
		if err != nil || !ok {
			return ErrInvalid
		}
		*dst = s
		return nil
	}
}
func numberField(dst *int64) field {
	return func(d *json.Decoder) error {
		t, err := d.Token()
		n, ok := t.(json.Number)
		if err != nil || !ok {
			return ErrInvalid
		}
		v, err := n.Int64()
		if err != nil {
			return ErrInvalid
		}
		*dst = v
		return nil
	}
}
func complete(seen map[string]bool, keys ...string) bool {
	if len(seen) != len(keys) {
		return false
	}
	for _, k := range keys {
		if !seen[k] {
			return false
		}
	}
	return true
}
func decoder(raw []byte) *json.Decoder {
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	return d
}
func end(d *json.Decoder) bool { _, err := d.Token(); return err == io.EOF }

func DecodeRequest(raw []byte) (Request, error) {
	var r Request
	if !validInput(raw) {
		return r, ErrInvalid
	}
	d := decoder(raw)
	var version int64
	seen, err := object(d, map[string]field{"version": numberField(&version), "operation": textField(&r.Operation), "radio": textField(&r.Radio), "expected_revision": textField(&r.ExpectedRevision), "id": textField(&r.ID), "label": textField(&r.Label), "frequency_hz": numberField(&r.FrequencyHz)})
	if err != nil || version != 1 || !end(d) {
		return Request{}, ErrInvalid
	}
	r.Version = 1
	keys := []string{"version", "operation", "radio"}
	switch r.Operation {
	case "list":
	case "create":
		keys = append(keys, "expected_revision", "label", "frequency_hz")
	case "update":
		keys = append(keys, "expected_revision", "id", "label", "frequency_hz")
	case "delete":
		keys = append(keys, "expected_revision", "id")
	default:
		return Request{}, ErrInvalid
	}
	if !complete(seen, keys...) || !r.valid() {
		return Request{}, ErrInvalid
	}
	return r, nil
}

// DecodeLibrary accepts only persisted documents. The "absent" revision belongs
// solely to a missing-file list response and cannot be saved as a document.
func DecodeLibrary(raw []byte) (Library, error) {
	if !validInput(raw) {
		return Library{}, ErrInvalid
	}
	d := decoder(raw)
	var l Library
	var version int64
	readPresets := func(d *json.Decoder) error {
		t, err := d.Token()
		if err != nil || t != json.Delim('[') {
			return ErrInvalid
		}
		l.Presets = []Definition{}
		for d.More() {
			if len(l.Presets) >= MaxPresets {
				return ErrInvalid
			}
			var p Definition
			seen, err := object(d, map[string]field{"id": textField(&p.ID), "label": textField(&p.Label), "frequency_hz": numberField(&p.FrequencyHz)})
			if err != nil || !complete(seen, "id", "label", "frequency_hz") {
				return ErrInvalid
			}
			l.Presets = append(l.Presets, p)
		}
		if t, err = d.Token(); err != nil || t != json.Delim(']') {
			return ErrInvalid
		}
		return nil
	}
	seen, err := object(d, map[string]field{"version": numberField(&version), "radio": textField(&l.Radio), "revision": textField(&l.Revision), "presets": readPresets})
	if err != nil || version != 1 || !complete(seen, "version", "radio", "revision", "presets") || !end(d) {
		return Library{}, ErrInvalid
	}
	l.Version = 1
	if !l.valid() {
		return Library{}, ErrInvalid
	}
	return l, nil
}
