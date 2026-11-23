package httpapi

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/wsjtx"
)

type collectionFixture struct {
	calls []string
	err   error
}

func (f *collectionFixture) Preview(_ context.Context, radio string) (wsjtx.ProgramRoutingPreview, error) {
	f.calls = append(f.calls, "preview:"+radio)
	return wsjtx.ProgramRoutingPreview{Radio: radio, Fingerprint: strings.Repeat("a", 64)}, f.err
}
func (f *collectionFixture) Enable(_ context.Context, radio, fingerprint string) error {
	f.calls = append(f.calls, "enable:"+radio+":"+fingerprint)
	return f.err
}
func (f *collectionFixture) Disable(_ context.Context, radio string) error {
	f.calls = append(f.calls, "disable:"+radio)
	return f.err
}
func (f *collectionFixture) Recover(_ context.Context, radio string) error {
	f.calls = append(f.calls, "recover:"+radio)
	return f.err
}
func (f *collectionFixture) CollectionState(_ context.Context, radio string) (wsjtx.ProgramCollectionState, error) {
	f.calls = append(f.calls, "status:"+radio)
	return wsjtx.ProgramCollectionState{Configured: true}, f.err
}

func collectionServer(loggedIn bool) (http.Handler, *collectionFixture) {
	f := &collectionFixture{}
	a := &WSJTXAPI{Radios: rwRecords{"radio1": {Name: "radio1", Owner: "wsjtx"}}, Collection: f}
	return New(Config{Auth: rwAuth{ok: loggedIn}, Routes: a.Register}).Handler, f
}

func TestWSJTXRoutesRequireAuthenticationSameOriginAndKnownRadio(t *testing.T) {
	for _, action := range []string{"", "/preview", "/enable", "/disable", "/recover"} {
		method := "POST"
		if action == "" {
			method = "GET"
		}
		for _, tc := range []struct {
			login         bool
			radio, origin string
			code          int
		}{
			{false, "radio1", "same-origin", 401}, {true, "radio1", "cross-site", 403}, {true, "missing", "same-origin", 404}, {true, "Radio1", "same-origin", 404},
		} {
			h, f := collectionServer(tc.login)
			rec := rwDo(h, method, "/api/radio/"+tc.radio+"/wsjtx"+action, `{}`, map[string]string{"Sec-Fetch-Site": tc.origin})
			if rec.Code != tc.code || len(f.calls) != 0 {
				t.Fatal("gate reached collection service", action, rec.Code, rec.Body, f.calls)
			}
		}
	}
}

func TestWSJTXRoutesExposeOnlyNamedObservationAndApprovalActions(t *testing.T) {
	h, f := collectionServer(true)
	for _, action := range []string{"preview", "enable", "disable", "recover"} {
		body := `{}`
		if action == "enable" {
			body = `{"fingerprint":"` + strings.Repeat("a", 64) + `"}`
		}
		rec := rwDo(h, "POST", "/api/radio/radio1/wsjtx/"+action, body, nil)
		if rec.Code != 200 {
			t.Fatal(action, rec.Code, rec.Body)
		}
		if rec.Header().Get("Cache-Control") != "no-store" {
			t.Fatal("private preview is cacheable")
		}
	}
	rec := rwDo(h, "GET", "/api/radio/radio1/wsjtx", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"configured":true`) || !strings.Contains(rec.Body.String(), `"enabled":false`) {
		t.Fatal(rec.Code, rec.Body)
	}
	if len(f.calls) != 5 {
		t.Fatal(f.calls)
	}
	for _, path := range []string{"transmit", "tune", "send", "start", "stopped", "initialize", "udp", "release-block", "release-resume", "release-status"} {
		rec := rwDo(h, "POST", "/api/radio/radio1/wsjtx/"+path, `{}`, nil)
		if rec.Code != 404 {
			t.Fatal("unexpected program action", path, rec.Code)
		}
	}
	if len(f.calls) != 5 {
		t.Fatal("unknown action reached service")
	}
}

func TestWSJTXApprovalRejectsDuplicateAliasesExtraFieldsAndUnboundedInput(t *testing.T) {
	h, f := collectionServer(true)
	value := strings.Repeat("a", 64)
	for _, body := range []string{
		``, `null`, `[]`, `{} {}`, `{"fingerprint":null}`, `{"fingerprint":2}`, `{"fingerprint":"bad"}`,
		`{"Fingerprint":"` + value + `"}`, `{"fingerprint":"` + value + `","fingerprint":"` + value + `"}`,
		`{"fingerprint":"` + value + `","server":"127.0.0.1"}`, strings.Repeat(" ", 1025),
	} {
		rec := rwDo(h, "POST", "/api/radio/radio1/wsjtx/enable", body, nil)
		if rec.Code != 400 || len(f.calls) != 0 {
			t.Fatal("invalid approval accepted", rec.Code, f.calls)
		}
	}
	for _, action := range []string{"preview", "disable", "recover"} {
		rec := rwDo(h, "POST", "/api/radio/radio1/wsjtx/"+action, `{"radio":"radio2"}`, nil)
		if rec.Code != 400 || len(f.calls) != 0 {
			t.Fatal("unexpected configuration input accepted", action, rec.Code)
		}
	}
}

func TestWSJTXFailuresExplainRecoveryWithoutExposingPrivateSettings(t *testing.T) {
	for _, tc := range []struct {
		err    error
		code   int
		reason string
	}{
		{wsjtx.ErrProgramNotConfigured, 409, "not-configured"}, {wsjtx.ErrProgramLifecycle, 409, "program-running"},
		{wsjtx.ErrProgramReleasePaused, 409, "release-paused"},
		{wsjtx.ErrRoutingConflict, 409, "routing-conflict"}, {wsjtx.ErrRoutingJournalRecovery, 409, "recovery-required"},
		{errors.New("private original destination and raw journal"), 503, "unavailable"},
	} {
		h, f := collectionServer(true)
		f.err = tc.err
		rec := rwDo(h, "POST", "/api/radio/radio1/wsjtx/preview", `{}`, nil)
		if rec.Code != tc.code || !strings.Contains(rec.Body.String(), tc.reason) || strings.Contains(rec.Body.String(), "private original") {
			t.Fatal(rec.Code, rec.Body)
		}
	}
}
