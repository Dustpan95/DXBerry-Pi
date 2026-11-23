package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/presetpreview"
)

type presetAuthFunc func(*http.Request) (*Session, bool)

func (f presetAuthFunc) Session(r *http.Request) (*Session, bool) { return f(r) }
func presetAuth(logged bool) AuthChecker {
	return presetAuthFunc(func(*http.Request) (*Session, bool) {
		if !logged {
			return nil, false
		}
		return &Session{ID: "authenticated-login", User: "fixture"}, true
	})
}

type presetCall struct {
	action       string
	principal    presetpreview.Principal
	request      presetpreview.Request
	radio, token string
	deadline     time.Duration
}
type presetBackend struct {
	calls  []presetCall
	err    error
	mutate func(*presetpreview.Preview)
	wait   bool
}

func (f *presetBackend) run(ctx context.Context, call presetCall) (presetpreview.Preview, error) {
	if deadline, ok := ctx.Deadline(); ok {
		call.deadline = time.Until(deadline)
	}
	f.calls = append(f.calls, call)
	if f.wait {
		<-ctx.Done()
		return presetpreview.Preview{}, ctx.Err()
	}
	p := presetpreview.Preview{Radio: call.radio, PresetID: strings.Repeat("1", 32), Revision: strings.Repeat("2", 64), Label: "Fixture", VFO: "VFOA", CurrentFrequencyHz: 14074000, RequestedFrequencyHz: 14075000, DeltaHz: 1000, Token: strings.Repeat("3", 64), ExpiresInMS: 2000}
	if call.action == "preview" {
		p.Radio, p.PresetID, p.Revision = call.request.Radio, call.request.ID, call.request.Revision
	} else {
		p.Token = call.token
	}
	if f.mutate != nil {
		f.mutate(&p)
	}
	return p, f.err
}
func (f *presetBackend) Preview(ctx context.Context, p presetpreview.Principal, r presetpreview.Request) (presetpreview.Preview, error) {
	return f.run(ctx, presetCall{action: "preview", principal: p, request: r})
}
func (f *presetBackend) Recheck(ctx context.Context, p presetpreview.Principal, radio, token string) (presetpreview.Preview, error) {
	return f.run(ctx, presetCall{action: "recheck", principal: p, radio: radio, token: token})
}

func presetBody(action string) string {
	if action == "recheck" {
		return `{"token":"` + strings.Repeat("3", 64) + `"}`
	}
	return `{"id":"` + strings.Repeat("1", 32) + `","revision":"` + strings.Repeat("2", 64) + `"}`
}
func presetServer(logged bool) (http.Handler, *presetBackend) {
	backend := &presetBackend{}
	auth := presetAuth(logged)
	api := &PresetPreviewAPI{Auth: auth, Service: backend}
	return New(Config{Auth: auth, Routes: api.Register}).Handler, backend
}
func presetRequest(h http.Handler, action, body string) *httptest.ResponseRecorder {
	return rwDo(h, "POST", "/api/radio/radio1/presets/"+action, body, map[string]string{TabHeader: "phone#nonce"})
}

func TestPresetPreviewHTTPPassesOnlyTrustedIdentityAndExplicitSelection(t *testing.T) {
	for _, action := range []string{"preview", "recheck"} {
		h, f := presetServer(true)
		rec := presetRequest(h, action, presetBody(action))
		if rec.Code != 200 || len(f.calls) != 1 || rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal(rec.Code, rec.Body, f.calls)
		}
		call := f.calls[0]
		if call.principal != (presetpreview.Principal{Login: "authenticated-login", Tab: "phone#nonce"}) || call.action != action || call.deadline <= 0 || call.deadline > presetpreview.Lifetime {
			t.Fatal(call)
		}
		if action == "preview" && call.request != (presetpreview.Request{Radio: "radio1", ID: strings.Repeat("1", 32), Revision: strings.Repeat("2", 64)}) {
			t.Fatal(call)
		}
		if action == "recheck" && (call.radio != "radio1" || call.token != strings.Repeat("3", 64)) {
			t.Fatal(call)
		}
		var result struct {
			OK      bool                  `json:"ok"`
			Preview presetpreview.Preview `json:"preview"`
		}
		if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil || !result.OK || result.Preview.Radio != "radio1" {
			t.Fatal(rec.Body, err)
		}
		if strings.Contains(rec.Body.String(), "authenticated-login") || strings.Contains(rec.Body.String(), "phone#nonce") {
			t.Fatal("identity echoed in preview")
		}
	}
}

func TestPresetPreviewHTTPGatesBeforeBackend(t *testing.T) {
	for _, action := range []string{"preview", "recheck"} {
		for _, which := range []string{"logged-out", "cross-site", "foreign-origin", "missing-tab", "bad-tab", "query", "invalid-radio"} {
			t.Run(action+"/"+which, func(t *testing.T) {
				h, f := presetServer(which != "logged-out")
				path := "/api/radio/radio1/presets/" + action
				headers := map[string]string{TabHeader: "phone#nonce"}
				want := 400
				switch which {
				case "logged-out":
					want = 401
				case "cross-site":
					headers["Sec-Fetch-Site"] = "cross-site"
					want = 403
				case "foreign-origin":
					headers["Sec-Fetch-Site"] = ""
					headers["Origin"] = "https://foreign.invalid"
					want = 403
				case "missing-tab":
					headers[TabHeader] = ""
				case "bad-tab":
					headers[TabHeader] = strings.Repeat("x", maxTab+1)
				case "query":
					path += "?token=private"
				case "invalid-radio":
					path = "/api/radio/Radio1/presets/" + action
					want = 404
				}
				rec := rwDo(h, "POST", path, presetBody(action), headers)
				if rec.Code != want || len(f.calls) != 0 {
					t.Fatal(rec.Code, rec.Body, f.calls)
				}
			})
		}
	}
	h, f := presetServer(true)
	req := httptest.NewRequest("POST", "/api/radio/radio1/presets/preview", strings.NewReader(presetBody("preview")))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Add(TabHeader, "first")
	req.Header.Add(TabHeader, "second")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 400 || len(f.calls) != 0 {
		t.Fatal("duplicate tab header accepted", rec.Code, f.calls)
	}
}

func TestPresetPreviewHTTPRequiresLiveHandlerSession(t *testing.T) {
	for _, which := range []string{"nil-auth", "nil-session", "expired-session", "empty-session"} {
		f := &presetBackend{}
		auth := presetAuth(true)
		switch which {
		case "nil-auth":
			auth = nil
		case "nil-session":
			auth = presetAuthFunc(func(*http.Request) (*Session, bool) { return nil, true })
		case "expired-session":
			auth = presetAuth(false)
		case "empty-session":
			auth = presetAuthFunc(func(*http.Request) (*Session, bool) { return &Session{}, true })
		}
		a := &PresetPreviewAPI{Auth: auth, Service: f}
		h := New(Config{Auth: presetAuth(true), Routes: a.Register}).Handler
		rec := presetRequest(h, "preview", presetBody("preview"))
		if rec.Code != 401 || len(f.calls) != 0 {
			t.Fatal(which, rec.Code, f.calls)
		}
	}
}

func TestPresetPreviewHTTPStrictBodiesAndNoControlRoutes(t *testing.T) {
	for _, action := range []string{"preview", "recheck"} {
		h, f := presetServer(true)
		valid := presetBody(action)
		bad := []string{"", `null`, `[]`, `{}`, valid + `{}`, `{"token":null}`, `{"id":1}`, valid + "x", strings.Repeat(" ", presetPreviewBodyLimit+1)}
		if action == "preview" {
			bad = append(bad, strings.Replace(valid, `"id":`, `"ID":`, 1), strings.Replace(valid, `"id":`, `"id":"`+strings.Repeat("1", 32)+`","id":`, 1), strings.Replace(valid, "}", `,"login":"spoofed"}`, 1), strings.Replace(valid, "}", `,"frequency_hz":7074000}`, 1), strings.Replace(valid, strings.Repeat("1", 32), `\ud800`, 1), strings.Replace(valid, strings.Repeat("1", 32), string([]byte{0xff}), 1))
		} else {
			bad = append(bad, strings.Replace(valid, `"token":`, `"Token":`, 1), strings.Replace(valid, `"token":`, `"token":"`+strings.Repeat("3", 64)+`","token":`, 1), strings.Replace(valid, "}", `,"radio":"other"}`, 1))
		}
		for _, body := range bad {
			rec := presetRequest(h, action, body)
			if rec.Code != 400 || len(f.calls) != 0 {
				t.Fatal(action, rec.Code, rec.Body, f.calls)
			}
		}
	}
	h, f := presetServer(true)
	for _, action := range []string{"recall", "undo", "apply", "tune", "ptt", "save", "delete"} {
		if rec := presetRequest(h, action, `{}`); rec.Code != 404 || len(f.calls) != 0 {
			t.Fatal("control route exposed", action, rec.Code)
		}
	}
	for _, method := range []string{"GET", "HEAD", "PUT", "DELETE"} {
		rec := rwDo(h, method, "/api/radio/radio1/presets/preview", presetBody("preview"), map[string]string{TabHeader: "phone#nonce"})
		if rec.Code == 200 || len(f.calls) != 0 {
			t.Fatal(method, rec.Code)
		}
	}
}

func TestPresetPreviewHTTPFailuresAreFixedAndDoNotReturnStaleSuccess(t *testing.T) {
	for _, tc := range []struct {
		err    error
		status int
		code   string
	}{
		{presetpreview.ErrInvalid, 400, "invalid"}, {presetpreview.ErrNotFound, 404, "not-found"}, {presetpreview.ErrNotOwner, 409, "not-owner"}, {presetpreview.ErrProgramRunning, 409, "program-running"}, {presetpreview.ErrTransmitting, 409, "transmitting"}, {presetpreview.ErrUnsupported, 422, "unsupported"}, {presetpreview.ErrConflict, 409, "conflict"}, {presetpreview.ErrExpired, 409, "expired"}, {presetpreview.ErrBusy, 429, "busy"}, {context.Canceled, 408, "canceled"}, {context.DeadlineExceeded, 504, "timeout"}, {errors.New("/private/process and selector"), 503, "unavailable"},
	} {
		h, f := presetServer(true)
		f.err = tc.err
		rec := presetRequest(h, "preview", presetBody("preview"))
		if rec.Code != tc.status || !strings.Contains(rec.Body.String(), `"code":"`+tc.code+`"`) || strings.Contains(rec.Body.String(), "preview\":") || strings.Contains(rec.Body.String(), "/private/") {
			t.Fatal(rec.Code, rec.Body)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("failure cacheable")
		}
	}
	for _, which := range []string{"radio", "id", "revision", "token", "frequency", "expiry", "label"} {
		h, f := presetServer(true)
		f.mutate = func(p *presetpreview.Preview) {
			switch which {
			case "radio":
				p.Radio = "radio2"
			case "id":
				p.PresetID = "bad"
			case "revision":
				p.Revision = "bad"
			case "token":
				p.Token = ""
			case "frequency":
				p.CurrentFrequencyHz = 0
			case "expiry":
				p.ExpiresInMS = 0
			case "label":
				p.Label = ""
			}
		}
		if rec := presetRequest(h, "preview", presetBody("preview")); rec.Code != 503 {
			t.Fatal(which, rec.Code)
		}
	}
	a := &PresetPreviewAPI{Auth: presetAuth(true)}
	h := New(Config{Auth: presetAuth(true), Routes: a.Register}).Handler
	if rec := presetRequest(h, "preview", presetBody("preview")); rec.Code != 503 {
		t.Fatal("unconfigured backend was usable", rec.Code)
	}
}

func TestPresetPreviewHTTPPreservesCancellation(t *testing.T) {
	h, f := presetServer(true)
	f.wait = true
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest("POST", "/api/radio/radio1/presets/preview", strings.NewReader(presetBody("preview"))).WithContext(ctx)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set(TabHeader, "phone#nonce")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 408 || len(f.calls) != 1 {
		t.Fatal(rec.Code, rec.Body, f.calls)
	}
}

func FuzzPresetPreviewHTTPBody(f *testing.F) {
	f.Add([]byte(presetBody("preview")))
	f.Add([]byte(presetBody("recheck")))
	f.Add([]byte(`{"id":null}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		for _, action := range []string{"preview", "recheck"} {
			fields, err := decodePresetPreviewBody(raw, action)
			if err != nil {
				continue
			}
			encoded, err := json.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			again, err := decodePresetPreviewBody(encoded, action)
			if err != nil || len(fields) != len(again) {
				t.Fatal("accepted input did not preserve schema", err)
			}
		}
	})
}
