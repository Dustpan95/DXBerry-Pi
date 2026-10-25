// Package auth is dxberryd's login: PAM checks the password, a sudo-group check decides who may
// administer the Pi, and sessions live in memory.
package auth

import (
	"bufio"
	"crypto/rand"
	"encoding/base64"
	"net"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/httpapi"
)

// Session is the one session type the server gates on.
type Session = httpapi.Session

// Authenticator checks a user name and password against the Pi's accounts.
type Authenticator interface {
	Authenticate(user, password string) error
}

// GroupLookup says whether an account may administer the Pi.
type GroupLookup interface {
	IsAdmin(user string) (bool, error)
}

// FileGroups reads membership of the sudo group from /etc/group and /etc/passwd.
type FileGroups struct {
	Group, Passwd string
}

// DefaultGroups reads the system files.
func DefaultGroups() FileGroups { return FileGroups{Group: "/etc/group", Passwd: "/etc/passwd"} }

func readColonFile(path string, fn func(f []string)) error {
	fh, err := os.Open(path)
	if err != nil {
		return err
	}
	defer fh.Close()
	sc := bufio.NewScanner(fh)
	for sc.Scan() {
		fn(strings.Split(sc.Text(), ":"))
	}
	return sc.Err()
}

// IsAdmin is true when user is listed in the sudo group or has it as the primary group.
func (g FileGroups) IsAdmin(user string) (bool, error) {
	gid, found, admin := "", false, false
	err := readColonFile(g.Group, func(f []string) {
		if len(f) < 4 || f[0] != "sudo" {
			return
		}
		found, gid = true, f[2]
		for _, m := range strings.Split(f[3], ",") {
			if m == user {
				admin = true
			}
		}
	})
	if err != nil {
		return false, err
	}
	if admin || !found {
		return admin, nil
	}
	err = readColonFile(g.Passwd, func(f []string) {
		if len(f) >= 4 && f[0] == user && f[3] == gid {
			admin = true
		}
	})
	return admin, err
}

const (
	cookieName  = "dxb_session"
	idleExpiry  = 12 * time.Hour
	maxSessions = 64
	window      = 60 * time.Second
	perAddr     = 5
	total       = 20
)

type bucket struct {
	start time.Time
	n     int
}

// Manager holds sessions and the login rate limits.
type Manager struct {
	auth   Authenticator
	groups GroupLookup
	now    func() time.Time

	mu       sync.Mutex
	sessions map[string]*Session
	addrs    map[string]*bucket
	all      bucket
}

// New builds a Manager. now is time.Now outside tests.
func New(a Authenticator, groups GroupLookup, now func() time.Time) *Manager {
	if now == nil {
		now = time.Now
	}
	return &Manager{auth: a, groups: groups, now: now,
		sessions: map[string]*Session{}, addrs: map[string]*bucket{}}
}

// sourceAddr is the client address: nginx on this host passes it in X-Forwarded-For.
func sourceAddr(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	if host == "127.0.0.1" {
		if xf := r.Header.Get("X-Forwarded-For"); xf != "" {
			first := strings.TrimSpace(strings.Split(xf, ",")[0])
			if first != "" {
				return first
			}
		}
	}
	return host
}

// allow counts one login attempt; when refused it returns the seconds until the window ends.
func (m *Manager) allow(addr string) (bool, int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for k, b := range m.addrs {
		if now.Sub(b.start) >= window {
			delete(m.addrs, k)
		}
	}
	b := m.addrs[addr]
	if b == nil {
		b = &bucket{start: now}
		m.addrs[addr] = b
	}
	if now.Sub(m.all.start) >= window {
		m.all = bucket{start: now}
	}
	if b.n >= perAddr || m.all.n >= total {
		start := b.start
		if b.n < perAddr {
			start = m.all.start
		}
		return false, retrySeconds(start.Add(window).Sub(now))
	}
	b.n++
	m.all.n++
	return true, 0
}

func retrySeconds(d time.Duration) int {
	s := int((d + time.Second - 1) / time.Second)
	if s < 1 {
		s = 1
	}
	return s
}

func newID() string {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		panic(err)
	}
	return base64.RawURLEncoding.EncodeToString(b)
}

func (m *Manager) create(user, addr string) *Session {
	m.mu.Lock()
	defer m.mu.Unlock()
	now := m.now()
	for len(m.sessions) >= maxSessions {
		oldest := ""
		for id, s := range m.sessions {
			if oldest == "" || s.Created.Before(m.sessions[oldest].Created) {
				oldest = id
			}
		}
		delete(m.sessions, oldest)
	}
	s := &Session{ID: newID(), User: user, Addr: addr, Created: now, LastUsed: now}
	m.sessions[s.ID] = s
	return s
}

// lookup finds the request's live session; renew moves its idle timer.
func (m *Manager) lookup(r *http.Request, renew bool) (*Session, bool) {
	c, err := r.Cookie(cookieName)
	if err != nil {
		return nil, false
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[c.Value]
	if !ok {
		return nil, false
	}
	now := m.now()
	if now.Sub(s.LastUsed) >= idleExpiry {
		delete(m.sessions, c.Value)
		return nil, false
	}
	if renew {
		s.LastUsed = now
	}
	cp := *s
	return &cp, true
}

// Session resolves the request's session and renews its idle timer.
func (m *Manager) Session(r *http.Request) (*Session, bool) { return m.lookup(r, true) }

func (m *Manager) drop(id string) {
	m.mu.Lock()
	delete(m.sessions, id)
	m.mu.Unlock()
}

// Alive says whether the request's session is still logged in, without renewing its idle timer:
// a long-lived connection that polls it must not keep an idle session alive.
func (m *Manager) Alive(r *http.Request) bool {
	_, ok := m.lookup(r, false)
	return ok
}
