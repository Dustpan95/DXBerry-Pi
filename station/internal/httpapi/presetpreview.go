package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/presetpreview"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/presets"
)

type PresetPreviewer interface {
	Preview(context.Context, presetpreview.Principal, presetpreview.Request) (presetpreview.Preview, error)
	Recheck(context.Context, presetpreview.Principal, string, string) (presetpreview.Preview, error)
}

// PresetPreviewAPI exposes only read-only comparison/recheck operations. A nil
// backend is unavailable; it does not construct a verifier or trust cached
// inventory. Register behind New's session/same-origin middleware.
type PresetPreviewAPI struct {
	Auth    AuthChecker
	Service PresetPreviewer
}

var presetRadioName = regexp.MustCompile(`^[a-z][a-z0-9]{0,11}$`)

const presetPreviewBodyLimit = 1024

func (a *PresetPreviewAPI) Register(mux *http.ServeMux) {
	for _, action := range []string{"preview", "recheck"} {
		mux.HandleFunc("POST /api/radio/{name}/presets/"+action, func(w http.ResponseWriter, r *http.Request) { a.handle(w, r, action) })
	}
}

func (a *PresetPreviewAPI) handle(w http.ResponseWriter, r *http.Request, action string) {
	if !SameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-site request refused")
		return
	}
	if a.Auth == nil {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	sess, ok := a.Auth.Session(r)
	if !ok || sess == nil || sess.ID == "" {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	radio := r.PathValue("name")
	if !presetRadioName.MatchString(radio) {
		writeError(w, http.StatusNotFound, "no such radio")
		return
	}
	tabs := r.Header.Values(TabHeader)
	if len(tabs) != 1 || !ValidTab(tabs[0]) || r.URL.RawQuery != "" || r.URL.ForceQuery {
		writeError(w, http.StatusBadRequest, "invalid preset preview input")
		return
	}
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, presetPreviewBodyLimit))
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid preset preview input")
		return
	}
	fields, err := decodePresetPreviewBody(raw, action)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid preset preview input")
		return
	}
	if a.Service == nil {
		presetPreviewFailure(w, presetpreview.ErrUnavailable)
		return
	}
	principal := presetpreview.Principal{Login: sess.ID, Tab: tabs[0]}
	ctx, cancel := context.WithTimeout(r.Context(), presetpreview.Lifetime)
	defer cancel()
	var result presetpreview.Preview
	if action == "preview" {
		result, err = a.Service.Preview(ctx, principal, presetpreview.Request{Radio: radio, ID: fields["id"], Revision: fields["revision"]})
	} else {
		result, err = a.Service.Recheck(ctx, principal, radio, fields["token"])
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		presetPreviewFailure(w, err)
		return
	}
	if !validPresetPreviewResult(result, radio) || action == "preview" && (result.PresetID != fields["id"] || result.Revision != fields["revision"]) || action == "recheck" && result.Token != fields["token"] {
		presetPreviewFailure(w, presetpreview.ErrUnavailable)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "preview": result})
}

// Decode exact keys and string values, with no Unicode repair, case aliases,
// nulls, duplicate keys or trailing JSON. Frequency/identity overrides have no
// input field. The HTTP body bound applies before any backend call.
func decodePresetPreviewBody(raw []byte, action string) (map[string]string, error) {
	bad := presetpreview.ErrInvalid
	if len(raw) > presetPreviewBodyLimit || !presets.ValidJSONEncoding(raw) {
		return nil, bad
	}
	want := map[string]int{"id": 32, "revision": 64}
	if action == "recheck" {
		want = map[string]int{"token": 64}
	} else if action != "preview" {
		return nil, bad
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return nil, bad
	}
	fields := map[string]string{}
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok || want[key] == 0 {
			return nil, bad
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, bad
		}
		token, err = d.Token()
		value, ok := token.(string)
		if err != nil || !ok || !presetHex(value, want[key]) {
			return nil, bad
		}
		fields[key] = value
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') || len(fields) != len(want) {
		return nil, bad
	}
	if _, err = d.Token(); err != io.EOF {
		return nil, bad
	}
	return fields, nil
}

func presetHex(s string, n int) bool { return len(s) == n && strings.Trim(s, "0123456789abcdef") == "" }
func validPresetPreviewResult(p presetpreview.Preview, radio string) bool {
	if p.Radio != radio || !presetHex(p.PresetID, 32) || !presetHex(p.Revision, 64) || !presetHex(p.Token, 64) || p.ExpiresInMS <= 0 || p.ExpiresInMS > presetpreview.Lifetime.Milliseconds() ||
		p.CurrentFrequencyHz <= 0 || p.CurrentFrequencyHz > presets.MaxFrequencyHz || p.RequestedFrequencyHz <= 0 || p.RequestedFrequencyHz > presets.MaxFrequencyHz || p.DeltaHz != p.RequestedFrequencyHz-p.CurrentFrequencyHz ||
		p.VFO == "" || len(p.VFO) > 32 || !utf8.ValidString(p.VFO) || len(p.Label) > 320 || !utf8.ValidString(p.Label) || strings.TrimSpace(p.Label) == "" {
		return false
	}
	for _, s := range []string{p.VFO, p.Label} {
		for _, c := range s {
			if unicode.IsControl(c) {
				return false
			}
		}
	}
	return true
}

func presetPreviewFailure(w http.ResponseWriter, err error) {
	status, code, reason := http.StatusServiceUnavailable, "unavailable", "Preset preview is unavailable."
	for _, item := range []struct {
		err          error
		status       int
		code, reason string
	}{
		{presetpreview.ErrInvalid, 400, "invalid", "The preset preview request was not understood."},
		{presetpreview.ErrNotFound, 404, "not-found", "The preset or radio is no longer available."},
		{presetpreview.ErrNotOwner, 409, "not-owner", "This tab does not hold the radio."},
		{presetpreview.ErrProgramRunning, 409, "program-running", "A program holds the radio."},
		{presetpreview.ErrTransmitting, 409, "transmitting", "The radio is transmitting."},
		{presetpreview.ErrUnsupported, 422, "unsupported", "This frequency or radio observation is unsupported."},
		{presetpreview.ErrConflict, 409, "conflict", "The preset or radio changed; preview again."},
		{presetpreview.ErrExpired, 409, "expired", "The preset preview expired; preview again."},
		{presetpreview.ErrBusy, 429, "busy", "Preset preview is busy; retry shortly."},
		{context.Canceled, 408, "canceled", "The preset preview request was canceled."},
		{context.DeadlineExceeded, 504, "timeout", "The preset preview request timed out."},
	} {
		if errors.Is(err, item.err) {
			status, code, reason = item.status, item.code, item.reason
			break
		}
	}
	if status == http.StatusTooManyRequests {
		w.Header().Set("Retry-After", "1")
	}
	writeJSON(w, status, map[string]any{"ok": false, "code": code, "reason": reason})
}
