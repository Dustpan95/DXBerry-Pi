package httpapi

import (
	"strings"
	"testing"
)

func TestRadioPageServesTheShell(t *testing.T) {
	h := testServer(true)
	for _, p := range []string{"/radio/ic7300", "/radio/a", "/radio/radio-1"} {
		rec := do(h, "GET", p, nil)
		if rec.Code != 200 || !strings.Contains(rec.Body.String(), "<title>DXBerry</title>") {
			t.Errorf("%s: got %d %q", p, rec.Code, rec.Body.String())
		}
	}
}

func TestRadioPageIsGated(t *testing.T) {
	h := testServer(false)
	rec := do(h, "GET", "/radio/ic7300", nil)
	if rec.Code != 303 || rec.Header().Get("Location") != "/login?next=%2Fradio%2Fic7300" {
		t.Fatalf("got %d %q", rec.Code, rec.Header().Get("Location"))
	}
}

func TestRadioPageRefusesABadName(t *testing.T) {
	h := testServer(true)
	long := "/radio/a" + strings.Repeat("b", 32)
	for _, p := range []string{"/radio/Bad", "/radio/1abc", "/radio/-x", "/radio/a_b", "/radio/a.b", "/radio/a%20b", long} {
		if rec := do(h, "GET", p, nil); rec.Code != 404 {
			t.Errorf("%s: got %d", p, rec.Code)
		}
	}
	// the longest allowed name is 32 characters
	if rec := do(h, "GET", "/radio/a"+strings.Repeat("b", 31), nil); rec.Code != 200 {
		t.Errorf("32 characters: got %d", rec.Code)
	}
}

func TestTrailingSlashRedirectsPagesNotAPI(t *testing.T) {
	h := testServer(true)
	rec := do(h, "GET", "/radios/", nil)
	if rec.Code != 301 || rec.Header().Get("Location") != "/radios" {
		t.Fatalf("/radios/: got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	rec = do(h, "GET", "/radio/ic7300/?x=1", nil)
	if rec.Code != 301 || rec.Header().Get("Location") != "/radio/ic7300?x=1" {
		t.Fatalf("with a query: got %d %q", rec.Code, rec.Header().Get("Location"))
	}
	// only a trailing slash is forgiven; anything else unclean stays a 404
	for _, p := range []string{"/a//b/", "/a/../b/", "//evil.example/"} {
		if rec := do(h, "GET", p, nil); rec.Code != 404 {
			t.Errorf("%s: got %d", p, rec.Code)
		}
	}
	// API paths keep the JSON 404, and a POST is not redirected
	rec = do(h, "GET", "/api/version/", map[string]string{"Sec-Fetch-Site": "same-origin"})
	if rec.Code != 404 || !strings.HasPrefix(rec.Header().Get("Content-Type"), "application/json") {
		t.Fatalf("api: got %d %q", rec.Code, rec.Header().Get("Content-Type"))
	}
	if rec := do(h, "POST", "/radios/", nil); rec.Code != 404 {
		t.Fatalf("post: got %d", rec.Code)
	}
}
