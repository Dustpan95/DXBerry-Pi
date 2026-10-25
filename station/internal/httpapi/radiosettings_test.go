package httpapi

import (
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/rig"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/settings"
)

type rsProbe struct {
	connected bool
	caps      rig.Caps
	err       error
	did       int
	stored    map[string]rig.Caps
	used      map[string]rig.Caps
}

func (p *rsProbe) Connected(name string) bool { return p.connected }
func (p *rsProbe) Discover(ctx context.Context, name string) (rig.Caps, error) {
	p.did++
	return p.caps, p.err
}
func (p *rsProbe) Store(model string, c rig.Caps) error { p.stored[model] = c; return nil }
func (p *rsProbe) Use(name string, c rig.Caps)          { p.used[name] = c }

func settingsServer(t *testing.T, loggedIn bool) (http.Handler, *rsProbe, *settings.Store) {
	p := &rsProbe{connected: true, caps: rig.Caps{ModelName: "IC-7300"}, stored: map[string]rig.Caps{}, used: map[string]rig.Caps{}}
	st := &settings.Store{Dir: t.TempDir()}
	api := &RadioSettingsAPI{
		Radios:   rwRecords{"radio1": {Name: "radio1", Model: 3073, Port: 4532}},
		Settings: st,
		Probe:    p,
	}
	h := New(Config{Listen: "127.0.0.1:0", Auth: rwAuth{ok: loggedIn}, Routes: api.Register}).Handler
	return h, p, st
}

func TestRadioSettingsRoutes(t *testing.T) {
	h, _, st := settingsServer(t, true)
	rec := rwDo(h, "GET", "/api/radio/radio1/settings", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"swr_limit":3`) || !strings.Contains(rec.Body.String(), `"ptt_timeout_s":180`) {
		t.Fatalf("get: %d %s", rec.Code, rec.Body)
	}
	rec = rwDo(h, "POST", "/api/radio/radio1/settings", `{"swr_limit":2.5,"ptt_timeout_s":90}`, nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("post: %d %s", rec.Code, rec.Body)
	}
	if g := st.Get("radio1"); g.SWRLimit != 2.5 || g.PTTTimeoutS != 90 {
		t.Fatalf("stored %+v", g)
	}
	rec = rwDo(h, "POST", "/api/radio/radio1/settings", `{"swr_limit":20,"ptt_timeout_s":90}`, nil)
	if !strings.Contains(rec.Body.String(), `"ok":false`) || !strings.Contains(rec.Body.String(), "between 1.5 and 10") || !strings.Contains(rec.Body.String(), `"fix"`) {
		t.Fatalf("refusal: %s", rec.Body)
	}
	if g := st.Get("radio1"); g.SWRLimit != 2.5 {
		t.Fatalf("a refused save changed the file: %+v", g)
	}
	rec = rwDo(h, "POST", "/api/radio/radio1/settings", `not json`, nil)
	if !strings.Contains(rec.Body.String(), `"ok":false`) {
		t.Fatalf("junk body: %d %s", rec.Code, rec.Body)
	}
}

func TestRadioSettingsGateAnd404(t *testing.T) {
	h, p, st := settingsServer(t, true)
	for _, tc := range []struct{ m, path string }{
		{"GET", "/api/radio/nope/settings"}, {"POST", "/api/radio/nope/settings"}, {"POST", "/api/radio/nope/reprobe"},
		{"GET", "/api/radio/Radio1/settings"}, {"POST", "/api/radio/..%2Fx/reprobe"},
	} {
		rec := rwDo(h, tc.m, tc.path, `{"swr_limit":3,"ptt_timeout_s":180}`, nil)
		if rec.Code != 404 {
			t.Fatalf("%s %s: %d", tc.m, tc.path, rec.Code)
		}
	}
	if p.did != 0 {
		t.Fatal("an unknown radio was probed")
	}
	if es := st.Get("nope"); es.SWRLimit != 3 {
		t.Fatal("defaults")
	}
	h, _, _ = settingsServer(t, false)
	for _, tc := range []struct{ m, path string }{
		{"GET", "/api/radio/radio1/settings"}, {"POST", "/api/radio/radio1/settings"}, {"POST", "/api/radio/radio1/reprobe"},
	} {
		if rec := rwDo(h, tc.m, tc.path, `{}`, nil); rec.Code != 401 {
			t.Fatalf("%s %s: %d", tc.m, tc.path, rec.Code)
		}
	}
}

func TestReprobeRoute(t *testing.T) {
	h, p, st := settingsServer(t, true)
	rec := rwDo(h, "POST", "/api/radio/radio1/reprobe", "", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("reprobe: %d %s", rec.Code, rec.Body)
	}
	if p.stored["3073"].ModelName != "IC-7300" || p.used["radio1"].ModelName != "IC-7300" {
		t.Fatalf("stored %v used %v", p.stored, p.used)
	}
	if st.Get("radio1").Probed.IsZero() {
		t.Fatal("probed time not recorded")
	}
	// not connected: refused, nothing probed
	p.connected = false
	p.did = 0
	rec = rwDo(h, "POST", "/api/radio/radio1/reprobe", "", nil)
	want := `"reason":"rig control unreachable; the radio's rigctld is restarting","fix":"wait a few seconds"`
	if !strings.Contains(rec.Body.String(), `"ok":false`) || !strings.Contains(rec.Body.String(), want) || p.did != 0 {
		t.Fatalf("disconnected: %s did=%d", rec.Body, p.did)
	}
	// a failed discovery is a refusal and stores nothing new
	p.connected = true
	p.err = context.DeadlineExceeded
	p.stored = map[string]rig.Caps{}
	rec = rwDo(h, "POST", "/api/radio/radio1/reprobe", "", nil)
	if !strings.Contains(rec.Body.String(), `"ok":false`) || len(p.stored) != 0 {
		t.Fatalf("failed: %s %v", rec.Body, p.stored)
	}
}
