package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
)

// fakeBridge records what the signalling socket asked of the radio's bridge.
type fakeBridge struct {
	mu       sync.Mutex
	offers   []string
	tabs     []string
	cands    []ICECandidate
	closed   []string
	lost     map[int]func(tab, reason string)
	next     int
	failWith error
}

func (f *fakeBridge) Connect(ctx context.Context, offer string, tab string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.offers = append(f.offers, offer)
	f.tabs = append(f.tabs, tab)
	if f.failWith != nil {
		return "", f.failWith
	}
	return "ANSWER:" + offer, nil
}

func (f *fakeBridge) AddICECandidate(tab string, c ICECandidate) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.cands = append(f.cands, c)
	return nil
}

func (f *fakeBridge) Close(tab string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = append(f.closed, tab)
}

func (f *fakeBridge) OnLost(fn func(tab, reason string)) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lost == nil {
		f.lost = map[int]func(string, string){}
	}
	id := f.next
	f.next++
	f.lost[id] = fn
	return func() { f.mu.Lock(); delete(f.lost, id); f.mu.Unlock() }
}

func (f *fakeBridge) loseAll(tab, reason string) {
	f.mu.Lock()
	var fns []func(string, string)
	for _, fn := range f.lost {
		fns = append(fns, fn)
	}
	f.mu.Unlock()
	for _, fn := range fns {
		fn(tab, reason)
	}
}

func (f *fakeBridge) snapshot() (offers, tabs, closed []string, cands []ICECandidate) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string(nil), f.offers...), append([]string(nil), f.tabs...), append([]string(nil), f.closed...), append([]ICECandidate(nil), f.cands...)
}

type audioRig struct {
	ts        *httptest.Server
	bridge    *fakeBridge
	available atomic.Bool
	alive     atomic.Bool
}

func newAudioRig(t *testing.T, loggedIn bool) *audioRig {
	t.Helper()
	ar := &audioRig{bridge: &fakeBridge{}}
	ar.available.Store(true)
	ar.alive.Store(true)
	api := &AudioAPI{
		Auth:   rwAuth{ok: loggedIn},
		Radios: rwRecords{"radio1": radios.Radio{Name: "radio1"}, "radio2": radios.Radio{Name: "radio2"}},
		Available: func(radio string) (bool, string) {
			if ar.available.Load() {
				return true, ""
			}
			return false, "no sound server for " + radio + ": WSJT-X has the sound card"
		},
		Bridge:    func(radio string) (AudioBridge, bool) { return ar.bridge, radio == "radio1" || radio == "radio2" },
		Alive:     func(*http.Request) bool { return ar.alive.Load() },
		PollEvery: 50 * time.Millisecond,
	}
	srv := New(Config{Listen: "127.0.0.1:0", Auth: rwAuth{ok: loggedIn}, Routes: api.Register})
	ar.ts = httptest.NewServer(srv.Handler)
	t.Cleanup(ar.ts.Close)
	return ar
}

func (ar *audioRig) dial(t *testing.T, path string, hdr http.Header) (*websocket.Conn, *http.Response, error) {
	t.Helper()
	if hdr == nil {
		hdr = http.Header{"Sec-Fetch-Site": {"same-origin"}}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(ar.ts.URL, "http")+path, &websocket.DialOptions{HTTPHeader: hdr})
}

func readMsg(t *testing.T, c *websocket.Conn) map[string]any {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_, data, err := c.Read(ctx)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("not JSON: %q", data)
	}
	return m
}

func sendMsg(t *testing.T, c *websocket.Conn, v any) {
	t.Helper()
	b, _ := json.Marshal(v)
	if err := c.Write(context.Background(), websocket.MessageText, b); err != nil {
		t.Fatal(err)
	}
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSignalRequiresSession(t *testing.T) {
	ar := newAudioRig(t, false)
	_, resp, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%231", nil)
	if err == nil || resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("logged out: err %v resp %v", err, resp)
	}
	// logged in but from another site
	ar = newAudioRig(t, true)
	_, resp, err = ar.dial(t, "/api/audio/radio1/signal?tab=Browser%231", http.Header{"Origin": {"https://evil.example"}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site: err %v resp %v", err, resp)
	}
	_, resp, err = ar.dial(t, "/api/audio/radio1/signal?tab=Browser%231", http.Header{"Sec-Fetch-Site": {"cross-site"}})
	if err == nil || resp == nil || resp.StatusCode != http.StatusForbidden {
		t.Fatalf("cross-site metadata: err %v resp %v", err, resp)
	}
	// no tab token, an unknown radio, a malformed name
	for path, code := range map[string]int{
		"/api/audio/radio1/signal":                 http.StatusBadRequest,
		"/api/audio/radio1/signal?tab=":            http.StatusBadRequest,
		"/api/audio/nope/signal?tab=Browser%231":   http.StatusNotFound,
		"/api/audio/Radio1/signal?tab=Browser%231": http.StatusNotFound,
	} {
		_, resp, err := ar.dial(t, path, nil)
		if err == nil || resp == nil || resp.StatusCode != code {
			t.Fatalf("%s: err %v resp %v, want %d", path, err, resp, code)
		}
	}
	if offers, _, _, _ := ar.bridge.snapshot(); len(offers) != 0 {
		t.Fatalf("a refused socket reached the bridge: %v", offers)
	}
}

func TestSignalRefusedWhenUnavailable(t *testing.T) {
	ar := newAudioRig(t, true)
	ar.available.Store(false)
	c, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%231", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	m := readMsg(t, c)
	want := map[string]any{"type": "refused", "reason": "no sound server for radio1: WSJT-X has the sound card",
		"fix": "start the radio's sound server or stop the program that holds the sound card"}
	if len(m) != len(want) {
		t.Fatalf("refusal %v", m)
	}
	for k, v := range want {
		if m[k] != v {
			t.Fatalf("refusal %v", m)
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := c.Read(ctx); websocket.CloseStatus(err) != websocket.StatusNormalClosure {
		t.Fatalf("the socket stayed open after the refusal: %v", err)
	}
	if offers, _, _, _ := ar.bridge.snapshot(); len(offers) != 0 {
		t.Fatalf("the bridge was asked: %v", offers)
	}
}

func TestSignalOfferAnswerTrickleAndClose(t *testing.T) {
	ar := newAudioRig(t, true)
	// a WebSocket from a browser carries the token on the address; the header works too
	c, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%231", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	sendMsg(t, c, map[string]any{"type": "offer", "sdp": "v=0 offer"})
	m := readMsg(t, c)
	if m["type"] != "answer" || m["sdp"] != "ANSWER:v=0 offer" {
		t.Fatalf("answer %v", m)
	}
	mid, idx := "0", uint16(0)
	sendMsg(t, c, map[string]any{"type": "ice", "candidate": map[string]any{"candidate": "candidate:1 1 udp 2130706431 10.0.0.5 50123 typ host", "sdpMid": mid, "sdpMLineIndex": idx}})
	waitFor(t, "the candidate", func() bool { _, _, _, cands := ar.bridge.snapshot(); return len(cands) == 1 })
	_, tabs, _, cands := ar.bridge.snapshot()
	if tabs[0] != "sess-secret|Browser#1" || cands[0].Candidate != "candidate:1 1 udp 2130706431 10.0.0.5 50123 typ host" ||
		cands[0].SDPMid == nil || *cands[0].SDPMid != "0" || cands[0].SDPMLineIndex == nil || *cands[0].SDPMLineIndex != 0 {
		t.Fatalf("tab %q candidate %+v", tabs[0], cands[0])
	}
	// another tab's loss is not this socket's; this tab's is
	ar.bridge.loseAll("sess-secret|Other#2", "no audio from the browser")
	ar.bridge.loseAll("sess-secret|Browser#1", "")
	m = readMsg(t, c)
	if len(m) != 1 || m["type"] != "audio-lost" {
		t.Fatalf("lost %v", m)
	}
	ar.bridge.loseAll("sess-secret|Browser#1", "no audio from the radio's sound card (RADIO1.in)")
	if m = readMsg(t, c); m["type"] != "audio-lost" || m["reason"] != "no audio from the radio's sound card (RADIO1.in)" {
		t.Fatalf("lost with a reason %v", m)
	}
	// an offer the bridge cannot answer is an error the page can show
	ar.bridge.mu.Lock()
	ar.bridge.failWith = errors.New("the offer carries no audio")
	ar.bridge.mu.Unlock()
	sendMsg(t, c, map[string]any{"type": "offer", "sdp": "v=0 video"})
	if m = readMsg(t, c); m["type"] != "error" || m["reason"] != "the offer carries no audio" || m["fix"] == "" {
		t.Fatalf("error %v", m)
	}
	sendMsg(t, c, map[string]any{"type": "close"})
	waitFor(t, "the close", func() bool { _, _, closed, _ := ar.bridge.snapshot(); return len(closed) == 1 })
	_ = c.Close(websocket.StatusNormalClosure, "")
	time.Sleep(100 * time.Millisecond)
	// the socket's end closes the connection it made at most once more, and garbage is ignored
	_, _, closed, _ := ar.bridge.snapshot()
	for _, id := range closed {
		if id != "sess-secret|Browser#1" {
			t.Fatalf("closed %v", closed)
		}
	}
	// the radio's sound server went away between the socket opening and the offer
	c2, _, err := ar.dial(t, "/api/audio/radio1/signal", http.Header{"Sec-Fetch-Site": {"same-origin"}, TabHeader: {"Browser#3"}})
	if err != nil {
		t.Fatal(err)
	}
	defer c2.CloseNow()
	_ = c2.Write(context.Background(), websocket.MessageText, []byte("not json"))
	ar.available.Store(false)
	sendMsg(t, c2, map[string]any{"type": "offer", "sdp": "v=0 late"})
	if m = readMsg(t, c2); m["type"] != "refused" || !strings.Contains(m["reason"].(string), "WSJT-X") {
		t.Fatalf("late refusal %v", m)
	}
	if offers, _, _, _ := ar.bridge.snapshot(); len(offers) != 2 {
		t.Fatalf("offers %v", offers)
	}
}

func TestSignalClosesOnLogout(t *testing.T) {
	ar := newAudioRig(t, true)
	c, _, err := ar.dial(t, "/api/audio/radio2/signal?tab=Browser%231", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	sendMsg(t, c, map[string]any{"type": "offer", "sdp": "x"})
	readMsg(t, c)
	ar.alive.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if _, _, err := c.Read(ctx); websocket.CloseStatus(err) != websocket.StatusPolicyViolation {
		t.Fatalf("logout: %v", err)
	}
	waitFor(t, "the peer connection's close", func() bool {
		_, _, closed, _ := ar.bridge.snapshot()
		return len(closed) >= 1 && closed[0] == "sess-secret|Browser#1"
	})
}

func TestSignalReconnectKeepsTheNewConnection(t *testing.T) {
	ar := newAudioRig(t, true)
	old, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%231", nil)
	if err != nil {
		t.Fatal(err)
	}
	sendMsg(t, old, map[string]any{"type": "offer", "sdp": "first"})
	readMsg(t, old)
	// the same tab opens a new socket and offers before the old socket has ended
	c, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%231", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	sendMsg(t, c, map[string]any{"type": "offer", "sdp": "second"})
	readMsg(t, c)
	_ = old.Close(websocket.StatusNormalClosure, "")
	time.Sleep(200 * time.Millisecond)
	if _, _, closed, _ := ar.bridge.snapshot(); len(closed) != 0 {
		t.Fatalf("the old socket's end closed the new connection: %v", closed)
	}
	// a loss now goes to the new socket only
	ar.bridge.loseAll("sess-secret|Browser#1", "")
	if m := readMsg(t, c); m["type"] != "audio-lost" {
		t.Fatalf("lost %v", m)
	}
}
