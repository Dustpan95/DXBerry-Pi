package httpapi

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Dustpan95/DXBerry-Pi/station/web"
)

type stubAuth struct{ ok bool }

func (s stubAuth) Session(r *http.Request) (*Session, bool) {
	if !s.ok {
		return nil, false
	}
	return &Session{User: "dietpi"}, true
}

func testServer(loggedIn bool) http.Handler {
	web := fstest.MapFS{
		"index.html": {Data: []byte("<!doctype html><title>DXBerry</title>")},
		"login.html": {Data: []byte("<title>Login</title>")},
		"app.js":     {Data: []byte("// js")},
		"sub/x.txt":  {Data: []byte("x")},
	}
	return New(Config{Listen: "127.0.0.1:0", Web: web, Auth: stubAuth{ok: loggedIn}, Version: "9.9.9"}).Handler
}

func do(h http.Handler, method, target string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestServesIndexForRoot(t *testing.T) {
	rec := do(testServer(true), "GET", "/", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>") {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestRedirectsToLoginWithoutSession(t *testing.T) {
	rec := do(testServer(false), "GET", "/radios", nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/login?next=%2Fradios" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = do(testServer(false), "GET", "/app.js", map[string]string{"Accept": "text/html"})
	if rec.Code != 303 {
		t.Fatalf("html accept: got %d", rec.Code)
	}
	rec = do(testServer(false), "GET", "/app.js", nil)
	if rec.Code != 401 {
		t.Fatalf("asset without session: got %d", rec.Code)
	}
}

func TestLoginPageIsOpen(t *testing.T) {
	rec := do(testServer(false), "GET", "/login", nil)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Login") {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestLoginAssetsAreOpen(t *testing.T) {
	web := fstest.MapFS{
		"index.html":  {Data: []byte("<title>DXBerry</title>")},
		"login.js":    {Data: []byte("//")},
		"login.css":   {Data: []byte("/**/")},
		"dxberry.css": {Data: []byte("/**/")},
		"dxberry.js":  {Data: []byte("//")},
	}
	h := New(Config{Web: web, Auth: stubAuth{}}).Handler
	for _, p := range []string{"/login.js", "/login.css", "/dxberry.css"} {
		if rec := do(h, "GET", p, nil); rec.Code != 200 {
			t.Errorf("%s: got %d", p, rec.Code)
		}
	}
	if rec := do(h, "GET", "/dxberry.js", map[string]string{"Accept": "text/html"}); rec.Code != 303 {
		t.Errorf("/dxberry.js: got %d", rec.Code)
	}
}

func TestManifestAndIconsAreOpenAndTyped(t *testing.T) {
	web := fstest.MapFS{
		"index.html":           {Data: []byte("<title>DXBerry</title>")},
		"manifest.webmanifest": {Data: []byte("{}")},
		"icons/icon-192.png":   {Data: []byte("png")},
		"icons/icon-512.png":   {Data: []byte("png")},
	}
	h := New(Config{Web: web, Auth: stubAuth{}}).Handler
	for _, p := range []string{"/manifest.webmanifest", "/icons/icon-192.png", "/icons/icon-512.png"} {
		if rec := do(h, "GET", p, nil); rec.Code != 200 {
			t.Errorf("%s: got %d", p, rec.Code)
		}
	}
	if ct := do(h, "GET", "/manifest.webmanifest", nil).Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/manifest+json") {
		t.Errorf("manifest content type %q", ct)
	}
}

func TestAPIWithoutSessionIs401(t *testing.T) {
	rec := do(testServer(false), "GET", "/api/status", map[string]string{"Sec-Fetch-Site": "same-origin"})
	if rec.Code != 401 || strings.TrimSpace(rec.Body.String()) != `{"error":"login required"}` {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
		t.Fatalf("content type %q", ct)
	}
}

func TestAPICrossSiteIs403(t *testing.T) {
	rec := do(testServer(true), "GET", "/api/status", map[string]string{"Origin": "https://evil.example"})
	if rec.Code != 403 || strings.TrimSpace(rec.Body.String()) != `{"error":"cross-site request refused"}` {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	// the exempt auth paths are still same-origin only
	rec = do(testServer(false), "POST", "/api/auth/login", nil)
	if rec.Code != 403 {
		t.Fatalf("auth path: got %d", rec.Code)
	}
}

func TestVersionIsOpen(t *testing.T) {
	rec := do(testServer(false), "GET", "/api/version", nil)
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"version":"9.9.9"}` {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
}

func TestUnknownAPIPathIs404JSON(t *testing.T) {
	rec := do(testServer(true), "GET", "/api/nope", map[string]string{"Sec-Fetch-Site": "same-origin"})
	if rec.Code != 404 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
}

func TestSPAFallbackAndNoListings(t *testing.T) {
	h := testServer(true)
	if rec := do(h, "GET", "/radios/ic7300", nil); rec.Code != 200 || !strings.Contains(rec.Body.String(), "DXBerry") {
		t.Fatalf("spa fallback: %d", rec.Code)
	}
	if rec := do(h, "GET", "/sub/", nil); strings.Contains(rec.Body.String(), "x.txt") {
		t.Fatalf("directory listing served: %q", rec.Body.String())
	}
	if rec := do(h, "GET", "/missing.js", nil); rec.Code != 404 {
		t.Fatalf("missing file: %d", rec.Code)
	}
	if rec := do(h, "GET", "/app.js", nil); rec.Code != 200 {
		t.Fatalf("asset: %d", rec.Code)
	}
}

func TestSameOriginRejectsForeign(t *testing.T) {
	cases := []struct {
		name string
		hdr  map[string]string
		want bool
	}{
		{"foreign origin", map[string]string{"Origin": "https://evil.example"}, false},
		{"fetch metadata", map[string]string{"Sec-Fetch-Site": "same-origin"}, true},
		{"cross-site metadata", map[string]string{"Sec-Fetch-Site": "cross-site"}, false},
		{"matching origin", map[string]string{"Origin": "https://pi.local"}, true},
		{"neither", nil, false},
	}
	for _, c := range cases {
		req := httptest.NewRequest("GET", "/api/status", nil)
		req.Host = "pi.local"
		for k, v := range c.hdr {
			req.Header.Set(k, v)
		}
		if got := SameOrigin(req); got != c.want {
			t.Errorf("%s: got %v want %v", c.name, got, c.want)
		}
	}
}

// Every script the real index page names must be served (to a logged-in browser) as 200.
func TestIndexScriptsResolve(t *testing.T) {
	h := New(Config{Listen: "127.0.0.1:0", Web: web.Files, Auth: stubAuth{ok: true}, Version: "9.9.9"}).Handler
	idx := do(h, "GET", "/", nil)
	if idx.Code != 200 {
		t.Fatalf("index: got %d", idx.Code)
	}
	tags := regexp.MustCompile(`<script src="([^"]+)"`).FindAllStringSubmatch(idx.Body.String(), -1)
	var got []string
	for _, m := range tags {
		got = append(got, m[1])
		if rec := do(h, "GET", m[1], nil); rec.Code != 200 {
			t.Errorf("%s: got %d", m[1], rec.Code)
		}
	}
	want := "/app.js /dxberry.js /settings.js /updates.js /dashboard.js /programs.js"
	if strings.Join(got, " ") != want {
		t.Errorf("script tags %q, want %q", strings.Join(got, " "), want)
	}
}

func TestServesStationCertificateWithoutSession(t *testing.T) {
	f := filepath.Join(t.TempDir(), "station.crt")
	if err := os.WriteFile(f, []byte("-----BEGIN CERTIFICATE-----\nxx\n-----END CERTIFICATE-----\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h := New(Config{Listen: "127.0.0.1:0", Web: fstest.MapFS{}, Auth: stubAuth{ok: false}, CertFile: f}).Handler
	rec := do(h, "GET", "/station.crt", nil)
	if rec.Code != 200 || rec.Header().Get("Content-Type") != "application/x-x509-ca-cert" || !strings.Contains(rec.Body.String(), "BEGIN CERTIFICATE") {
		t.Fatalf("got %d %q %q", rec.Code, rec.Header().Get("Content-Type"), rec.Body.String())
	}
	h = New(Config{Listen: "127.0.0.1:0", Web: fstest.MapFS{}, Auth: stubAuth{ok: false}, CertFile: f + ".missing"}).Handler
	if rec := do(h, "GET", "/station.crt", nil); rec.Code != 404 {
		t.Fatalf("missing cert: %d", rec.Code)
	}
}
