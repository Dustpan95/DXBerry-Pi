package auth_test

import (
	"bytes"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/auth"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/httpapi"
)

type fakeAuth struct{}

func (fakeAuth) Authenticate(user, pw string) error {
	if pw == "right pässword ü" || pw == "right" {
		return nil
	}
	return errors.New("denied")
}

type fakeGroups map[string]bool

func (g fakeGroups) IsAdmin(u string) (bool, error) { return g[u], nil }

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

type rig struct {
	h   http.Handler
	clk *clock
}

func newRig() *rig {
	clk := &clock{t: time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)}
	m := auth.New(fakeAuth{}, fakeGroups{"dietpi": true, "bob": false, "root": true}, clk.now)
	web := fstest.MapFS{"index.html": {Data: []byte("x")}}
	h := httpapi.New(httpapi.Config{Web: web, Auth: m, Routes: m.Register}).Handler
	return &rig{h: h, clk: clk}
}

func (r *rig) do(method, target, body, addr string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.RemoteAddr = addr
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	r.h.ServeHTTP(rec, req)
	return rec
}

func (r *rig) login(user, pw string) *httptest.ResponseRecorder {
	return r.do("POST", "/api/auth/login", `{"user":"`+user+`","password":"`+pw+`"}`, "10.0.0.5:1234", nil)
}

func cookieOf(t *testing.T, rec *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range rec.Result().Cookies() {
		if c.Name == "dxb_session" {
			return c
		}
	}
	t.Fatalf("no session cookie in %v", rec.Header())
	return nil
}

func withCookie(c *http.Cookie) map[string]string {
	return map[string]string{"Cookie": c.Name + "=" + c.Value}
}

func TestLoginSetsCookieAndCheckSucceeds(t *testing.T) {
	r := newRig()
	rec := r.login("dietpi", "right")
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), `"user":"dietpi"`) {
		t.Fatalf("login: %d %s", rec.Code, rec.Body)
	}
	c := cookieOf(t, rec)
	if !c.HttpOnly || !c.Secure || c.SameSite != http.SameSiteStrictMode || c.Path != "/" || c.MaxAge != 0 || len(c.Value) < 43 {
		t.Fatalf("cookie flags wrong: %+v", c)
	}
	chk := r.do("GET", "/api/auth/check", "", "10.0.0.5:1", withCookie(c))
	if chk.Code != 200 || !strings.Contains(chk.Body.String(), `"ok":true`) {
		t.Fatalf("check: %d %s", chk.Code, chk.Body)
	}
}

func TestWrongPasswordIs401(t *testing.T) {
	r := newRig()
	rec := r.login("dietpi", "nope")
	if rec.Code != 401 || len(rec.Result().Cookies()) != 0 {
		t.Fatalf("got %d cookies %v", rec.Code, rec.Result().Cookies())
	}
}

func TestRootRefused(t *testing.T) {
	r := newRig()
	rec := r.login("root", "right")
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "root cannot log in") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestNonSudoRefused(t *testing.T) {
	r := newRig()
	rec := r.login("bob", "right")
	if rec.Code != 403 || !strings.Contains(rec.Body.String(), "this account cannot administer the Pi") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	if len(rec.Result().Cookies()) != 0 {
		t.Fatal("cookie set for refused account")
	}
}

func TestRateLimitPerAddress(t *testing.T) {
	r := newRig()
	for i := 1; i <= 5; i++ {
		if rec := r.login("dietpi", "nope"); rec.Code != 401 {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	rec := r.login("dietpi", "right")
	if rec.Code != 429 || rec.Header().Get("Retry-After") == "" || !strings.Contains(rec.Body.String(), `"retry_after":60`) ||
		!strings.Contains(rec.Body.String(), "too many attempts") {
		t.Fatalf("6th: %d %q %s", rec.Code, rec.Header().Get("Retry-After"), rec.Body)
	}
	other := r.do("POST", "/api/auth/login", `{"user":"dietpi","password":"right"}`, "10.0.0.6:1", nil)
	if other.Code != 200 {
		t.Fatalf("other address: %d", other.Code)
	}
	r.clk.t = r.clk.t.Add(61 * time.Second)
	if rec := r.login("dietpi", "right"); rec.Code != 200 {
		t.Fatalf("after window: %d", rec.Code)
	}
}

func TestRateLimitTotal(t *testing.T) {
	r := newRig()
	for i := 0; i < 20; i++ {
		rec := r.do("POST", "/api/auth/login", `{"user":"dietpi","password":"nope"}`, "10.0."+string(rune('0'+i/10))+"."+string(rune('0'+i%10))+":1", nil)
		if rec.Code != 401 {
			t.Fatalf("attempt %d: %d", i, rec.Code)
		}
	}
	rec := r.do("POST", "/api/auth/login", `{"user":"dietpi","password":"nope"}`, "10.9.9.9:1", nil)
	if rec.Code != 429 {
		t.Fatalf("21st: %d", rec.Code)
	}
}

func TestForwardedForOnlyFromLoopback(t *testing.T) {
	r := newRig()
	xf := map[string]string{"X-Forwarded-For": "192.168.1.9, 127.0.0.1"}
	for i := 0; i < 5; i++ {
		r.do("POST", "/api/auth/login", `{"user":"a","password":"b"}`, "127.0.0.1:5", xf)
	}
	if rec := r.do("POST", "/api/auth/login", `{}`, "127.0.0.1:5", xf); rec.Code != 429 {
		t.Fatalf("same forwarded client: %d", rec.Code)
	}
	if rec := r.do("POST", "/api/auth/login", `{}`, "127.0.0.1:5", map[string]string{"X-Forwarded-For": "192.168.1.10"}); rec.Code == 429 {
		t.Fatal("different forwarded client shared a bucket")
	}
	// a non-loopback peer cannot pick its own bucket
	for i := 0; i < 5; i++ {
		r.do("POST", "/api/auth/login", `{}`, "10.0.0.50:5", map[string]string{"X-Forwarded-For": "1.1.1." + string(rune('0'+i))})
	}
	if rec := r.do("POST", "/api/auth/login", `{}`, "10.0.0.50:5", map[string]string{"X-Forwarded-For": "1.1.1.9"}); rec.Code != 429 {
		t.Fatalf("spoofed XFF escaped the limit: %d", rec.Code)
	}
}

func TestSessionIdleExpiry(t *testing.T) {
	r := newRig()
	c := cookieOf(t, r.login("dietpi", "right"))
	r.clk.t = r.clk.t.Add(11 * time.Hour)
	if rec := r.do("GET", "/api/x", "", "10.0.0.5:1", withCookie(c)); rec.Code == 401 {
		t.Fatal("expired early")
	}
	r.clk.t = r.clk.t.Add(11 * time.Hour) // 22 h after login, 11 h idle: renewed by use
	if rec := r.do("GET", "/api/x", "", "10.0.0.5:1", withCookie(c)); rec.Code == 401 {
		t.Fatal("use did not renew the session")
	}
	r.clk.t = r.clk.t.Add(12 * time.Hour)
	if rec := r.do("GET", "/api/auth/check", "", "10.0.0.5:1", withCookie(c)); rec.Code != 401 {
		t.Fatalf("check after 12h idle: %d", rec.Code)
	}
}

func TestCheckDoesNotRenew(t *testing.T) {
	r := newRig()
	c := cookieOf(t, r.login("dietpi", "right"))
	for i := 0; i < 3; i++ {
		r.clk.t = r.clk.t.Add(5 * time.Hour)
		rec := r.do("GET", "/api/auth/check", "", "10.0.0.5:1", withCookie(c))
		if i < 2 && rec.Code != 200 {
			t.Fatalf("check %d: %d", i, rec.Code)
		}
		if i == 2 && rec.Code != 401 {
			t.Fatalf("polling check kept the session alive: %d", rec.Code)
		}
	}
}

func TestLogoutInvalidates(t *testing.T) {
	r := newRig()
	c := cookieOf(t, r.login("dietpi", "right"))
	rec := r.do("POST", "/api/auth/logout", "", "10.0.0.5:1", withCookie(c))
	if rec.Code != 200 || cookieOf(t, rec).MaxAge >= 0 {
		t.Fatalf("logout: %d %+v", rec.Code, rec.Result().Cookies())
	}
	if chk := r.do("GET", "/api/auth/check", "", "10.0.0.5:1", withCookie(c)); chk.Code != 401 {
		t.Fatalf("after logout: %d", chk.Code)
	}
}

func TestAPIRefusesWithoutSession(t *testing.T) {
	r := newRig()
	rec := r.do("GET", "/api/anything", "", "10.0.0.5:1", nil)
	if rec.Code != 401 || !strings.Contains(rec.Body.String(), "login required") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

func TestAPIRefusesForeignOrigin(t *testing.T) {
	r := newRig()
	c := cookieOf(t, r.login("dietpi", "right"))
	h := withCookie(c)
	h["Origin"] = "https://evil.example"
	h["Sec-Fetch-Site"] = "cross-site"
	if rec := r.do("POST", "/api/anything", "{}", "10.0.0.5:1", h); rec.Code != 403 {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestSessionCapDropsOldest(t *testing.T) {
	r := newRig()
	first := cookieOf(t, r.do("POST", "/api/auth/login", `{"user":"dietpi","password":"right"}`, "10.1.0.1:1", nil))
	for i := 0; i < 64; i++ {
		r.clk.t = r.clk.t.Add(4 * time.Second) // stays under the 20-per-minute total
		addr := "10.2." + string(rune('0'+i/10)) + "." + string(rune('0'+i%10)) + ":1"
		if rec := r.do("POST", "/api/auth/login", `{"user":"dietpi","password":"right"}`, addr, nil); rec.Code != 200 {
			t.Fatalf("login %d: %d", i, rec.Code)
		}
	}
	if rec := r.do("GET", "/api/auth/check", "", "10.0.0.5:1", withCookie(first)); rec.Code != 401 {
		t.Fatalf("oldest session survived the cap: %d", rec.Code)
	}
}

func TestLoginPasswordNeverLogged(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)
	r := newRig()
	r.login("dietpi", "wrong pässword ü")
	good := r.login("dietpi", "right pässword ü")
	r.login("bob", "right pässword ü")
	r.login("root", "right pässword ü")
	out := buf.String()
	if strings.Contains(out, "pässword") || strings.Contains(out, "ü") {
		t.Fatalf("password in log: %s", out)
	}
	if id := cookieOf(t, good).Value; strings.Contains(out, id) {
		t.Fatal("session id in log")
	}
	for _, want := range []string{"msg=\"login ok\" user=dietpi from=10.0.0.5", "msg=\"login refused\" user=dietpi from=10.0.0.5 reason=", "msg=\"login refused\" user=root"} {
		if !strings.Contains(out, want) {
			t.Fatalf("log lacks %q: %s", want, out)
		}
	}
}

func TestFileGroups(t *testing.T) {
	d := t.TempDir()
	grp := filepath.Join(d, "group")
	pw := filepath.Join(d, "passwd")
	os.WriteFile(grp, []byte("root:x:0:\nsudo:x:27:alice,carol\nusers:x:100:\n"), 0o644)
	os.WriteFile(pw, []byte("alice:x:1000:1000::/h:/bin/sh\ndave:x:1001:27::/h:/bin/sh\neve:x:1002:100::/h:/bin/sh\n"), 0o644)
	g := auth.FileGroups{Group: grp, Passwd: pw}
	for user, want := range map[string]bool{"alice": true, "carol": true, "dave": true, "eve": false, "nobody": false, "ali": false} {
		got, err := g.IsAdmin(user)
		if err != nil || got != want {
			t.Errorf("%s: got %v %v want %v", user, got, err, want)
		}
	}
	if _, err := (auth.FileGroups{Group: filepath.Join(d, "missing")}).IsAdmin("alice"); err == nil {
		t.Error("missing group file gave no error")
	}
}

func TestRealPAM(t *testing.T) {
	user, pw := os.Getenv("DXB_PAM_TEST_USER"), os.Getenv("DXB_PAM_TEST_PASSWORD")
	if _, err := os.Stat("/etc/pam.d"); err != nil || user == "" || pw == "" {
		t.Skip("needs /etc/pam.d and DXB_PAM_TEST_USER / DXB_PAM_TEST_PASSWORD")
	}
	p := auth.PAM{Service: "login"}
	if err := p.Authenticate(user, pw); err != nil {
		t.Fatalf("good password refused: %v", err)
	}
	if err := p.Authenticate(user, pw+"x"); err == nil {
		t.Fatal("bad password accepted")
	}
}

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	os.Exit(m.Run())
}

func TestCheckNeedsNoOriginHeaders(t *testing.T) {
	r := newRig()
	c := cookieOf(t, r.login("dietpi", "right"))
	h := withCookie(c)
	h["Sec-Fetch-Site"] = ""
	if rec := r.do("GET", "/api/auth/check", "", "127.0.0.1:9", h); rec.Code != 200 {
		t.Fatalf("check without origin headers: %d", rec.Code)
	}
	if rec := r.do("POST", "/api/auth/logout", "", "127.0.0.1:9", h); rec.Code != 403 {
		t.Fatalf("logout without origin headers: %d", rec.Code)
	}
}

func TestLoggedUserIsTruncated(t *testing.T) {
	var buf bytes.Buffer
	old := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, nil)))
	defer slog.SetDefault(old)
	newRig().login(strings.Repeat("u", 200), "x")
	if strings.Contains(buf.String(), strings.Repeat("u", 65)) || !strings.Contains(buf.String(), strings.Repeat("u", 64)) {
		t.Fatalf("user not truncated to 64: %s", buf.String())
	}
}
