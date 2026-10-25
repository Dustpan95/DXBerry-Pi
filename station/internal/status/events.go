package status

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
	"github.com/coder/websocket"
)

// RadioSource is the radio records watcher as the events socket sees it.
type RadioSource interface {
	List() []radios.Radio
	Subscribe() <-chan []radios.Radio
	Unsubscribe(<-chan []radios.Radio)
}

// SetRadios makes events sockets also carry the radios part. Call it before serving.
func (s *Status) SetRadios(src RadioSource) { s.radios = src }

// RigUpdate is one rig delta from the hub. Package rig declares the same
// unnamed struct type (Link: the connection to the radio went up or down),
// so the two packages meet without importing each other.
type RigUpdate = struct {
	Radio string
	Data  []byte
	Link  bool
}

// RigHub is what the events socket needs from the rig layer (rig.Manager).
// Levels are plain ints: 0 none, 1 dashboard, 2 page.
type RigHub interface {
	SetWatch(radio string, level int)
	Snapshot(radio string) ([]byte, bool)
	Subscribe() (<-chan RigUpdate, func())
}

// SetRig connects the rig layer; without it watch messages are ignored.
func (s *Status) SetRig(h RigHub) { s.hub = h }

// SocketHook is told when an events socket opens and returns the function to run when it
// closes. The owner lock uses it to drop a tab's claim with its socket.
type SocketHook func(r *http.Request) (closed func())

// OnSocket sets the hook. Call it before serving.
func (s *Status) OnSocket(h SocketHook) { s.onSock = h }

const (
	watchNone      = 0
	watchDashboard = 1
	watchPage      = 2
)

// sock is one open events socket's per-radio watch levels.
type sock struct {
	mu     sync.Mutex
	levels map[string]int
	warned bool // an ignored watch has been logged for this socket
}

func (k *sock) level(radio string) int {
	k.mu.Lock()
	defer k.mu.Unlock()
	return k.levels[radio]
}

func (s *Status) addSock(k *sock) {
	s.smu.Lock()
	if s.socks == nil {
		s.socks = map[*sock]struct{}{}
	}
	s.socks[k] = struct{}{}
	s.smu.Unlock()
}

// maxLevel is the highest level any open socket holds for radio; s.smu held.
func (s *Status) maxLevel(radio string) int {
	m := watchNone
	for k := range s.socks {
		if l := k.level(radio); l > m {
			m = l
		}
	}
	return m
}

// setLevel records a socket's level for a radio and tells the hub the new max.
func (s *Status) setLevel(k *sock, radio string, level int) {
	s.smu.Lock()
	defer s.smu.Unlock()
	k.mu.Lock()
	if level == watchNone {
		delete(k.levels, radio)
	} else {
		k.levels[radio] = level
	}
	k.mu.Unlock()
	s.hub.SetWatch(radio, s.maxLevel(radio))
}

// dropSock forgets a closed socket's contributions.
func (s *Status) dropSock(k *sock) {
	s.smu.Lock()
	defer s.smu.Unlock()
	delete(s.socks, k)
	k.mu.Lock()
	var held []string
	for r := range k.levels {
		held = append(held, r)
	}
	k.mu.Unlock()
	if s.hub == nil {
		return
	}
	for _, r := range held {
		s.hub.SetWatch(r, s.maxLevel(r))
	}
}

func (s *Status) knownRadio(name string) bool {
	if s.radios == nil {
		return false
	}
	for _, r := range s.radios.List() {
		if r.Name == name {
			return true
		}
	}
	return false
}

// handleFrame acts on one client frame; only watch messages mean anything.
// It returns the radio whose level was raised, for an immediate state.
func (s *Status) handleFrame(k *sock, data []byte) (watched string) {
	var m struct {
		Type, Radio, Level string
	}
	if json.Unmarshal(data, &m) != nil || m.Type != "watch" || s.hub == nil {
		return ""
	}
	level, ok := map[string]int{"none": watchNone, "dashboard": watchDashboard, "page": watchPage}[m.Level]
	if !ok || !s.knownRadio(m.Radio) {
		k.mu.Lock()
		first := !k.warned
		k.warned = true
		k.mu.Unlock()
		if first {
			slog.Warn("events socket sent a watch for an unknown radio or level; ignored", "radio", m.Radio, "level", m.Level)
		}
		return ""
	}
	s.setLevel(k, m.Radio, level)
	if level == watchNone {
		return ""
	}
	return m.Radio
}

func rigMessage(radio string, data []byte) []byte {
	b, _ := json.Marshal(struct {
		Type  string          `json:"type"`
		Radio string          `json:"radio"`
		Data  json.RawMessage `json:"data"`
	}{"rig", radio, data})
	return b
}

func radiosMessage(list []radios.Radio) []byte {
	if list == nil {
		list = []radios.Radio{}
	}
	b, _ := json.Marshal(struct {
		Type string         `json:"type"`
		Data []radios.Radio `json:"data"`
	}{"radios", list})
	return b
}

var (
	pingEvery    = 30 * time.Second
	writeTimeout = 5 * time.Second // a client that cannot take a message in this long is closed
)

const readLimit = 64 << 10 // the most a client frame may carry

const pollEvery = 5 * time.Second // how often an open events socket re-checks that its session is still logged in

// Register mounts GET /api/status and GET /api/events. Both sit behind httpapi's session gate
// and same-origin rule; alive re-checks the login session of an open events socket.
func (s *Status) Register(mux *http.ServeMux, alive func(*http.Request) bool) {
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(s.Snapshot())
	})
	mux.Handle("GET /api/events", s.eventsHandler(alive, pollEvery))
}

func (s *Status) message() []byte {
	snap := s.Snapshot()
	b := make([]byte, 0, len(snap)+32)
	b = append(b, `{"type":"status","data":`...)
	b = append(b, snap...)
	return append(b, '}')
}

// events serves one browser: the current status at once, then after every refresh, with a ping
// in between. This goroutine writes; one reader goroutine drains and ignores what the browser
// sends (a "watch" message is accepted and does nothing in this release). Both end with the
// socket.
func (s *Status) eventsHandler(alive func(*http.Request) bool, every time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { s.events(w, r, alive, every) })
}

func (s *Status) events(w http.ResponseWriter, r *http.Request, alive func(*http.Request) bool, every time.Duration) {
	ws, err := websocket.Accept(w, r, nil) // writes its own refusal, a foreign Origin among them
	if err != nil {
		return
	}
	ws.SetReadLimit(readLimit)
	ctx, cancel := context.WithCancel(r.Context())
	updates, unsubscribe := s.Subscribe()
	readerDone := make(chan struct{})
	sk := &sock{levels: map[string]int{}}
	s.addSock(sk)
	var onClosed func()
	if s.onSock != nil {
		onClosed = s.onSock(r)
	}
	watched := make(chan string, 16) // radios whose level just went up: send their state now
	defer func() {
		cancel()
		_ = ws.CloseNow() // ends the reader's Read
		<-readerDone
		s.dropSock(sk)
		unsubscribe()
		if onClosed != nil {
			onClosed()
		}
	}()
	go func() {
		defer close(readerDone)
		for {
			_, data, err := ws.Read(ctx)
			if err != nil {
				cancel()
				return
			}
			if radio := s.handleFrame(sk, data); radio != "" {
				select {
				case watched <- radio:
				default: // the writer is behind; the next delta carries the state
				}
			}
		}
	}()

	send := func(b []byte) bool {
		wctx, c := context.WithTimeout(ctx, writeTimeout)
		defer c()
		return ws.Write(wctx, websocket.MessageText, b) == nil
	}
	var rigUpdates <-chan RigUpdate // nil (never ready) when no rig layer is set
	if s.hub != nil {
		var stop func()
		rigUpdates, stop = s.hub.Subscribe()
		defer stop()
	}
	if !send(s.message()) {
		return
	}
	var radioUpdates <-chan []radios.Radio // nil (never ready) when no source is set
	if s.radios != nil {
		radioUpdates = s.radios.Subscribe()
		defer s.radios.Unsubscribe(radioUpdates)
		if !send(radiosMessage(s.radios.List())) {
			return
		}
	}
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	check := time.NewTicker(every)
	defer check.Stop()
	pingMsg, _ := json.Marshal(map[string]string{"type": "ping"})
	for {
		select {
		case <-ctx.Done():
			return
		case <-updates:
			if !send(s.message()) {
				return
			}
		case l := <-radioUpdates:
			if !send(radiosMessage(l)) {
				return
			}
		case u := <-rigUpdates:
			if u.Link || sk.level(u.Radio) > watchNone { // a connection change goes to every socket
				if !send(rigMessage(u.Radio, u.Data)) {
					return
				}
			}
		case radio := <-watched:
			if data, ok := s.hub.Snapshot(radio); ok && sk.level(radio) > watchNone {
				if !send(rigMessage(radio, data)) {
					return
				}
			}
		case <-check.C:
			if !alive(r) {
				_ = ws.Close(websocket.StatusPolicyViolation, "logged out") // before cancel: see term.go
				return
			}
		case <-ping.C:
			if !send(pingMsg) {
				return
			}
		}
	}
}
