package auth

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strconv"
)

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, code int, msg string) {
	writeJSON(w, code, map[string]string{"error": msg})
}

// logUser bounds what a client can put in a log line.
func logUser(u string) string {
	if r := []rune(u); len(r) > 64 {
		return string(r[:64])
	}
	return u
}

func setCookie(w http.ResponseWriter, value string, expire bool) {
	c := &http.Cookie{Name: cookieName, Value: value, Path: "/", HttpOnly: true, Secure: true,
		SameSite: http.SameSiteStrictMode}
	if expire {
		c.MaxAge = -1
	}
	http.SetCookie(w, c)
}

// Register mounts the three auth endpoints.
func (m *Manager) Register(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/auth/login", m.Login)
	mux.HandleFunc("POST /api/auth/logout", m.Logout)
	mux.HandleFunc("GET /api/auth/check", m.Check)
}

// Login checks the credentials and starts a session.
func (m *Manager) Login(w http.ResponseWriter, r *http.Request) {
	addr := sourceAddr(r)
	if ok, retry := m.allow(addr); !ok {
		slog.Warn("login limited", "from", addr)
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many attempts", "retry_after": retry})
		return
	}
	var body struct {
		User     string `json:"user"`
		Password string `json:"password"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4096)).Decode(&body); err != nil {
		writeError(w, http.StatusBadRequest, "bad request")
		return
	}
	refuse := func(code int, msg, reason string) {
		slog.Warn("login refused", "user", logUser(body.User), "from", addr, "reason", reason)
		writeError(w, code, msg)
	}
	switch {
	case body.User == "root":
		refuse(http.StatusForbidden, "root cannot log in", "root")
		return
	case body.User == "" || body.Password == "":
		refuse(http.StatusUnauthorized, "wrong user name or password", "empty")
		return
	}
	if err := m.auth.Authenticate(body.User, body.Password); err != nil {
		refuse(http.StatusUnauthorized, "wrong user name or password", "bad credentials")
		return
	}
	admin, err := m.groups.IsAdmin(body.User)
	if err != nil {
		refuse(http.StatusInternalServerError, "could not check the account", "group lookup failed")
		return
	}
	if !admin {
		refuse(http.StatusForbidden, "this account cannot administer the Pi", "not in sudo")
		return
	}
	s := m.create(body.User, addr)
	slog.Info("login ok", "user", logUser(body.User), "from", addr)
	setCookie(w, s.ID, false)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": s.User})
}

// Logout ends the request's session.
func (m *Manager) Logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		if s, ok := m.lookup(r, false); ok {
			slog.Info("logout", "user", logUser(s.User), "from", sourceAddr(r))
		}
		m.drop(c.Value)
	}
	setCookie(w, "", true)
	writeJSON(w, http.StatusOK, map[string]any{"ok": true})
}

// Check answers whether the request holds a live session. It does not renew the idle timer,
// so a page that only polls this (nginx's gate) does not keep a session alive.
func (m *Manager) Check(w http.ResponseWriter, r *http.Request) {
	s, ok := m.lookup(r, false)
	if !ok {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"ok": true, "user": s.User})
}
