// Package httpapi is dxberryd's HTTP front: same-origin checks, the session gate and the
// embedded pages. The handlers for the API itself register behind it.
package httpapi

import (
	"bytes"
	"encoding/json"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

// Session is one logged-in browser session.
type Session struct {
	ID, User, Device  string
	Created, LastUsed time.Time
	Addr              string
}

// AuthChecker resolves the session a request carries.
type AuthChecker interface {
	Session(r *http.Request) (*Session, bool)
}

// Config is everything New needs.
type Config struct {
	Listen  string
	Web     fs.FS
	Auth    AuthChecker
	Dev     bool // pages are served uncached
	Version string
	Routes  func(mux *http.ServeMux) // registers the API handlers behind the gate
}

func init() {
	// the manifest is served by name, and the system's mime table may not know the extension
	_ = mime.AddExtensionType(".webmanifest", "application/manifest+json")
}

// ungated paths answer without a session. /api/auth/ is the login itself. The manifest and its
// icons are open too: a browser fetches the manifest without the session cookie.
func ungated(p string) bool {
	switch p {
	case "/login", "/login.js", "/login.css", "/dxberry.css",
		"/manifest.webmanifest", "/icons/icon-192.png", "/icons/icon-512.png":
		return true
	}
	return p == "/station.crt" || p == "/api/version" || strings.HasPrefix(p, "/api/auth/")
}

// SameOrigin reports whether a request came from a page on this host: fetch metadata says
// same-origin, or the Origin header names the request's own host.
func SameOrigin(r *http.Request) bool {
	if r.Header.Get("Sec-Fetch-Site") == "same-origin" {
		return true
	}
	o := r.Header.Get("Origin")
	if o == "" {
		return false
	}
	u, err := url.Parse(o)
	return err == nil && u.Host != "" && u.Host == r.Host
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

type server struct {
	cfg Config
	mux *http.ServeMux
}

// New builds the HTTP server; its Handler is what the tests drive.
func New(cfg Config) *http.Server {
	s := &server{cfg: cfg, mux: http.NewServeMux()}
	s.mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"version": cfg.Version})
	})
	if cfg.Routes != nil {
		cfg.Routes(s.mux)
	}
	return &http.Server{
		Addr:              cfg.Listen,
		Handler:           s,
		ReadHeaderTimeout: 10 * time.Second,
	}
}

func isPageRequest(r *http.Request) bool {
	return strings.Contains(r.Header.Get("Accept"), "text/html") || path.Ext(r.URL.Path) == ""
}

func (s *server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	p := r.URL.Path
	w.Header().Set("X-Content-Type-Options", "nosniff")
	isAPI := strings.HasPrefix(p, "/api/")
	if isAPI && !(r.Method == http.MethodGet && (p == "/api/version" || p == "/api/auth/check")) && !SameOrigin(r) {
		writeError(w, http.StatusForbidden, "cross-site request refused")
		return
	}
	if !ungated(p) {
		if _, ok := s.cfg.Auth.Session(r); !ok {
			switch {
			case isAPI:
				writeError(w, http.StatusUnauthorized, "login required")
			case isPageRequest(r):
				http.Redirect(w, r, "/login?next="+url.QueryEscape(r.URL.RequestURI()), http.StatusSeeOther)
			default:
				writeError(w, http.StatusUnauthorized, "login required")
			}
			return
		}
	}
	if isAPI {
		if _, pat := s.mux.Handler(r); pat != "" {
			s.mux.ServeHTTP(w, r)
			return
		}
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	s.static(w, r)
}

// static serves a file from the embedded tree. An extension-less path is a page: its own
// <name>.html when there is one, otherwise index.html. Directories are never listed.
func (s *server) static(w http.ResponseWriter, r *http.Request) {
	name := strings.Trim(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	} else if path.Ext(name) == "" {
		if _, err := fs.Stat(s.cfg.Web, name+".html"); err == nil {
			name += ".html"
		} else {
			name = "index.html"
		}
	}
	info, err := fs.Stat(s.cfg.Web, name)
	if err != nil || info.IsDir() {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	data, err := fs.ReadFile(s.cfg.Web, name)
	if err != nil {
		writeError(w, http.StatusNotFound, "not found")
		return
	}
	if s.cfg.Dev {
		w.Header().Set("Cache-Control", "no-store")
	}
	http.ServeContent(w, r, name, info.ModTime(), bytes.NewReader(data))
}
