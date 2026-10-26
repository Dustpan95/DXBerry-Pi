package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/session"
)

// ICECandidate is a trickled candidate as the browser's RTCIceCandidate.toJSON() gives it; it
// converts to audio.Candidate (httpapi does not import audio: audio's CLI runs import back here
// through the cli tests).
type ICECandidate struct {
	Candidate        string  `json:"candidate"`
	SDPMid           *string `json:"sdpMid,omitempty"`
	SDPMLineIndex    *uint16 `json:"sdpMLineIndex,omitempty"`
	UsernameFragment *string `json:"usernameFragment,omitempty"`
}

// AudioBridge is a radio's WebRTC bridge as the signalling socket needs it (an *audio.Bridge
// behind a small adapter in dxberryd).
type AudioBridge interface {
	Connect(ctx context.Context, offerSDP string, tab string) (string, error)
	AddICECandidate(tab string, c ICECandidate) error
	Close(tab string)
	OnLost(fn func(tab, reason string)) (cancel func())
}

// AudioFix is the refusal's fix when a radio has no sound server.
const AudioFix = "start the radio's sound server or stop the program that holds the sound card"

const (
	audioPollEvery = 5 * time.Second
	signalLimit    = 64 << 10 // an offer with every candidate is a few kB
	signalWrite    = 5 * time.Second
	// MaxAudioPerLogin caps one login's peer connections with one radio.
	MaxAudioPerLogin = 4
)

// AudioAPI serves WS /api/audio/{radio}/signal: the browser's offer, the answer, trickled ICE
// candidates and audio-lost notices for the tab's peer connection with the radio's bridge.
type AudioAPI struct {
	Auth   AuthChecker
	Radios RadioRecords
	// Available says whether the radio has a running sound server, and why not.
	Available func(radio string) (bool, string)
	// Bridge is the radio's bridge.
	Bridge func(radio string) (AudioBridge, bool)
	// Alive says whether the request's login is still good; polled while the socket is open.
	Alive func(*http.Request) bool
	// PollEvery is how often Alive is asked (5 s by default).
	PollEvery time.Duration

	mu     sync.Mutex // guards serial, slots and every slot's owner and refs; never held across a bridge call
	serial uint64
	slots  map[string]*tabSlot // radio + tab id -> the tab's slot
}

// tabSlot is one tab's place with one radio. Its own mutex orders the tab's close against its
// next connect, so a reconnecting tab's new socket is never closed by its old one, without
// holding up any other tab.
type tabSlot struct {
	mu    sync.Mutex
	radio string
	login string // the session id (memory only, never logged)
	owner uint64 // the socket that made the tab's current connection (0: none)
	refs  int    // sockets on this slot
}

// connectionsLocked counts login's tabs with a connection to radio, other than skip.
func (a *AudioAPI) connectionsLocked(radio, login string, skip *tabSlot) int {
	n := 0
	for _, sl := range a.slots {
		if sl != skip && sl.owner != 0 && sl.radio == radio && sl.login == login {
			n++
		}
	}
	return n
}

// Register adds the route. Call it from Config.Routes, so the session gate and the same-origin
// rule apply.
func (a *AudioAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/audio/{radio}/signal", a.signal)
}

type signalMsg struct {
	Type      string        `json:"type"`
	SDP       string        `json:"sdp,omitempty"`
	Candidate *ICECandidate `json:"candidate,omitempty"`
}

type signalNotice struct {
	Type   string `json:"type"`
	SDP    string `json:"sdp,omitempty"`
	Reason string `json:"reason,omitempty"`
	Fix    string `json:"fix,omitempty"`
}

// refusal is {"type":"refused","reason","fix"}, every field present.
type refusal struct {
	Type   string `json:"type"`
	Reason string `json:"reason"`
	Fix    string `json:"fix"`
}

func (a *AudioAPI) signal(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("radio")
	if !reRadioName.MatchString(name) {
		writeError(w, http.StatusNotFound, "no such radio")
		return
	}
	if _, ok := a.Radios.Get(name); !ok {
		writeError(w, http.StatusNotFound, "no such radio")
		return
	}
	sess, ok := a.Auth.Session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	tab := r.Header.Get(TabHeader)
	if tab == "" {
		tab = r.URL.Query().Get("tab")
	}
	if !ValidTab(tab) {
		writeError(w, http.StatusBadRequest, "the request was not understood")
		return
	}
	bridge, ok := a.Bridge(name)
	if !ok {
		writeError(w, http.StatusNotFound, "no such radio")
		return
	}
	ws, err := websocket.Accept(w, r, nil) // writes its own refusal, a foreign Origin among them
	if err != nil {
		return
	}
	defer ws.CloseNow()
	ws.SetReadLimit(signalLimit)
	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()
	send := func(v any) error {
		b, err := json.Marshal(v)
		if err != nil {
			return err
		}
		wctx, wcancel := context.WithTimeout(ctx, signalWrite)
		defer wcancel()
		return ws.Write(wctx, websocket.MessageText, b)
	}
	refuse := func(why string) {
		_ = send(refusal{Type: "refused", Reason: why, Fix: AudioFix})
		_ = ws.Close(websocket.StatusNormalClosure, "no browser audio")
	}
	if ok, why := a.Available(name); !ok {
		refuse(why)
		return
	}

	id := session.TabID(sess.ID, tab)
	key := name + "\x00" + id
	a.mu.Lock()
	if a.slots == nil {
		a.slots = map[string]*tabSlot{}
	}
	sl := a.slots[key]
	if sl == nil {
		sl = &tabSlot{radio: name, login: sess.ID}
		a.slots[key] = sl
	}
	sl.refs++
	a.serial++
	me := a.serial
	a.mu.Unlock()
	defer func() {
		a.mu.Lock()
		if sl.refs--; sl.refs == 0 && sl.owner == 0 {
			delete(a.slots, key)
		}
		a.mu.Unlock()
	}()
	// mine reports whether this socket made the tab's current connection
	mine := func() bool {
		a.mu.Lock()
		defer a.mu.Unlock()
		return sl.owner == me
	}
	// release closes the tab's connection only if this socket made it (a reconnecting tab's new
	// socket may already have replaced it)
	release := func() {
		sl.mu.Lock()
		defer sl.mu.Unlock()
		a.mu.Lock()
		was := sl.owner == me
		if was {
			sl.owner = 0
		}
		a.mu.Unlock()
		if was {
			bridge.Close(id)
		}
	}
	// claim makes this socket the tab's connection maker, unless the login has too many
	claim := func() bool {
		sl.mu.Lock()
		defer sl.mu.Unlock()
		a.mu.Lock()
		defer a.mu.Unlock()
		if sl.owner == 0 && a.connectionsLocked(name, sess.ID, sl) >= MaxAudioPerLogin {
			return false
		}
		sl.owner = me
		return true
	}
	defer release()
	stopLost := bridge.OnLost(func(t, reason string) {
		if t == id && mine() {
			_ = send(signalNotice{Type: "audio-lost", Reason: reason})
		}
	})
	defer stopLost()

	every := a.PollEvery
	if every <= 0 {
		every = audioPollEvery
	}
	go func() { // the login may end while the socket is open
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if a.Alive != nil && !a.Alive(r) {
					release()
					_ = ws.Close(websocket.StatusPolicyViolation, "logged out") // before cancel: a cancelled read drops the close frame
					cancel()
					return
				}
			}
		}
	}()

	for {
		typ, data, err := ws.Read(ctx)
		if err != nil {
			return
		}
		var m signalMsg
		if typ != websocket.MessageText || json.Unmarshal(data, &m) != nil {
			continue
		}
		switch m.Type {
		case "offer":
			if ok, why := a.Available(name); !ok {
				refuse(why)
				return
			}
			if !claim() {
				_ = send(refusal{Type: "refused", Reason: "too many audio connections from this login", Fix: "close other tabs"})
				_ = ws.Close(websocket.StatusNormalClosure, "too many audio connections")
				return
			}
			answer, err := bridge.Connect(ctx, m.SDP, id)
			if err != nil {
				_ = send(signalNotice{Type: "error", Reason: err.Error(), Fix: "tap to reconnect"})
				continue
			}
			_ = send(signalNotice{Type: "answer", SDP: answer})
		case "ice":
			if m.Candidate != nil && mine() {
				_ = bridge.AddICECandidate(id, *m.Candidate)
			}
		case "close":
			release()
		}
	}
}
