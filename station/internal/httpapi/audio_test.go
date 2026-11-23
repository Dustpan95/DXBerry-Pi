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
	mu          sync.Mutex
	offers      []string
	tabs        []string
	generations []uint64
	cands       []ICECandidate
	closed      []string
	lost        map[int]func(tab string, generation uint64, reason string)
	next        int
	failWith    error
	gate        chan struct{} // when set, Close waits for it
	connectHook func(context.Context, string, string, uint64) error
}

func (f *fakeBridge) ConnectGeneration(ctx context.Context, offer string, tab string, generation uint64) (string, error) {
	f.mu.Lock()
	f.offers = append(f.offers, offer)
	f.tabs = append(f.tabs, tab)
	f.generations = append(f.generations, generation)
	fail, hook := f.failWith, f.connectHook
	f.mu.Unlock()
	if hook != nil {
		if err := hook(ctx, offer, tab, generation); err != nil {
			return "", err
		}
	}
	if fail != nil {
		return "", fail
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
	gate := f.gate
	f.mu.Unlock()
	if gate != nil {
		<-gate
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.closed = append(f.closed, tab)
}

func (f *fakeBridge) OnLostGeneration(fn func(tab string, generation uint64, reason string)) func() {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.lost == nil {
		f.lost = map[int]func(string, uint64, string){}
	}
	id := f.next
	f.next++
	f.lost[id] = fn
	return func() { f.mu.Lock(); delete(f.lost, id); f.mu.Unlock() }
}

func (f *fakeBridge) loseAll(tab, reason string) {
	f.mu.Lock()
	var generation uint64
	for i := range f.tabs {
		if f.tabs[i] == tab {
			generation = f.generations[i]
		}
	}
	f.mu.Unlock()
	f.loseGeneration(tab, generation, reason)
}

func (f *fakeBridge) loseGeneration(tab string, generation uint64, reason string) {
	f.mu.Lock()
	var fns []func(string, uint64, string)
	for _, fn := range f.lost {
		fns = append(fns, fn)
	}
	f.mu.Unlock()
	for _, fn := range fns {
		fn(tab, generation, reason)
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

func TestSignalCapsConnectionsPerLogin(t *testing.T) {
	ar := newAudioRig(t, true)
	var socks []*websocket.Conn
	for i := 1; i <= 4; i++ {
		c, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%23"+string(rune('0'+i)), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer c.CloseNow()
		sendMsg(t, c, map[string]any{"type": "offer", "sdp": "o"})
		if m := readMsg(t, c); m["type"] != "answer" {
			t.Fatalf("tab %d: %v", i, m)
		}
		socks = append(socks, c)
	}
	// a tab offering again is still one connection
	sendMsg(t, socks[0], map[string]any{"type": "offer", "sdp": "again"})
	if m := readMsg(t, socks[0]); m["type"] != "answer" {
		t.Fatalf("re-offer %v", m)
	}
	// another radio is counted on its own
	other, _, err := ar.dial(t, "/api/audio/radio2/signal?tab=Browser%235", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer other.CloseNow()
	sendMsg(t, other, map[string]any{"type": "offer", "sdp": "o"})
	if m := readMsg(t, other); m["type"] != "answer" {
		t.Fatalf("radio2 %v", m)
	}
	// a fifth tab of the same login on radio1 is refused
	c, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%235", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	sendMsg(t, c, map[string]any{"type": "offer", "sdp": "o"})
	m := readMsg(t, c)
	if len(m) != 3 || m["type"] != "refused" || m["reason"] != "too many audio connections from this login" || m["fix"] != "close other tabs" {
		t.Fatalf("fifth %v", m)
	}
	// one tab leaving makes room
	_ = socks[1].Close(websocket.StatusNormalClosure, "")
	waitFor(t, "the slot", func() bool { _, _, closed, _ := ar.bridge.snapshot(); return len(closed) > 0 })
	c6, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%236", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c6.CloseNow()
	sendMsg(t, c6, map[string]any{"type": "offer", "sdp": "o"})
	if m := readMsg(t, c6); m["type"] != "answer" {
		t.Fatalf("after a tab left %v", m)
	}
}

func TestSignalFailedOfferFreesConnectionSlot(t *testing.T) {
	for _, connected := range []bool{false, true} {
		name := "first offer"
		if connected {
			name = "replacement offer"
		}
		t.Run(name, func(t *testing.T) {
			ar := newAudioRig(t, true)
			failed, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%23failed", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer failed.CloseNow()
			if connected {
				sendMsg(t, failed, map[string]any{"type": "offer", "sdp": "original"})
				if m := readMsg(t, failed); m["type"] != "answer" {
					t.Fatalf("original offer: %v", m)
				}
			}
			ar.bridge.mu.Lock()
			ar.bridge.failWith = errors.New("bad audio offer")
			ar.bridge.mu.Unlock()
			sendMsg(t, failed, map[string]any{"type": "offer", "sdp": "bad"})
			if m := readMsg(t, failed); m["type"] != "error" {
				t.Fatalf("failed offer: %v", m)
			}
			ar.bridge.mu.Lock()
			ar.bridge.failWith = nil
			ar.bridge.mu.Unlock()
			// Keep the failed socket open: it must not consume a peer slot.
			var healthy []*websocket.Conn
			for i := 0; i < MaxAudioPerLogin; i++ {
				c, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%23healthy"+string(rune('a'+i)), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer c.CloseNow()
				healthy = append(healthy, c)
				sendMsg(t, c, map[string]any{"type": "offer", "sdp": "healthy"})
				if m := readMsg(t, c); m["type"] != "answer" {
					t.Fatalf("healthy tab %d blocked by failed offer: %v", i, m)
				}
			}
			_, _, closed, _ := ar.bridge.snapshot()
			if len(closed) != 1 || closed[0] != "sess-secret|Browser#failed" {
				t.Fatalf("failed offer did not close its own peer only: %v", closed)
			}
			// A later retry on the same socket can claim a newly available slot.
			sendMsg(t, healthy[0], map[string]any{"type": "close"})
			waitFor(t, "healthy tab release", func() bool {
				_, _, closed, _ := ar.bridge.snapshot()
				for _, tab := range closed {
					if tab == "sess-secret|Browser#healthya" {
						return true
					}
				}
				return false
			})
			sendMsg(t, failed, map[string]any{"type": "offer", "sdp": "retry"})
			if m := readMsg(t, failed); m["type"] != "answer" {
				t.Fatalf("retry on failed socket: %v", m)
			}
		})
	}
}

func TestSignalLostPeerFreesConnectionSlotWithoutSocketClose(t *testing.T) {
	ar := newAudioRig(t, true)
	lost, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%23lost", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer lost.CloseNow()
	sendMsg(t, lost, map[string]any{"type": "offer", "sdp": "original"})
	if m := readMsg(t, lost); m["type"] != "answer" {
		t.Fatalf("original offer: %v", m)
	}
	ar.bridge.loseAll("sess-secret|Browser#lost", "audio link dropped")
	if m := readMsg(t, lost); m["type"] != "audio-lost" {
		t.Fatalf("lost peer: %v", m)
	}
	// Leave the lost peer's socket open. Only actual or pending peers consume
	// quota, so four other tabs must still be allowed to establish audio.
	for i := 0; i < MaxAudioPerLogin; i++ {
		c, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%23afterloss"+string(rune('a'+i)), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer c.CloseNow()
		sendMsg(t, c, map[string]any{"type": "offer", "sdp": "healthy"})
		if m := readMsg(t, c); m["type"] != "answer" {
			t.Fatalf("healthy peer %d blocked by lost peer: %v", i, m)
		}
	}
}

func TestSignalLossNotificationEndsOnlyItsSocketLifecycle(t *testing.T) {
	for _, reason := range []string{"", "no audio from the radio's sound card (RADIO1.in)"} {
		t.Run(reason, func(t *testing.T) {
			ar := newAudioRig(t, true)
			c, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%23notice", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer c.CloseNow()
			sendMsg(t, c, map[string]any{"type": "offer", "sdp": "first"})
			readMsg(t, c)
			ar.bridge.loseAll("sess-secret|Other#2", "unrelated loss")
			ar.bridge.loseAll("sess-secret|Browser#notice", reason)
			m := readMsg(t, c)
			if m["type"] != "audio-lost" || (reason != "" && m["reason"] != reason) || (reason == "" && len(m) != 1) {
				t.Fatalf("loss notice: %v", m)
			}
			ar.bridge.loseAll("sess-secret|Browser#notice", "duplicate loss")
			sendMsg(t, c, map[string]any{"type": "ice", "candidate": map[string]any{"candidate": "late candidate"}})
			sendMsg(t, c, map[string]any{"type": "offer", "sdp": "racing retry"})
			if m := readMsg(t, c); m["type"] != "error" {
				t.Fatalf("duplicate notice or revived ended socket: %v", m)
			}
			if offers, _, closed, cands := ar.bridge.snapshot(); len(offers) != 1 || len(closed) != 0 || len(cands) != 0 {
				t.Fatalf("ended socket reached bridge: offers=%v closed=%v candidates=%v", offers, closed, cands)
			}
		})
	}
}

func TestSignalDelayedLossKeepsReplacement(t *testing.T) {
	for _, newSocket := range []bool{false, true} {
		name := "same socket"
		if newSocket {
			name = "replacement socket"
		}
		t.Run(name, func(t *testing.T) {
			ar := newAudioRig(t, true)
			old, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%23same", nil)
			if err != nil {
				t.Fatal(err)
			}
			defer old.CloseNow()
			sendMsg(t, old, map[string]any{"type": "offer", "sdp": "first"})
			readMsg(t, old)
			ar.bridge.mu.Lock()
			first := ar.bridge.generations[0]
			ar.bridge.mu.Unlock()
			current := old
			if newSocket {
				current, _, err = ar.dial(t, "/api/audio/radio1/signal?tab=Browser%23same", nil)
				if err != nil {
					t.Fatal(err)
				}
				defer current.CloseNow()
			}
			sendMsg(t, current, map[string]any{"type": "offer", "sdp": "replacement"})
			if m := readMsg(t, current); m["type"] != "answer" {
				t.Fatalf("replacement: %v", m)
			}
			ar.bridge.mu.Lock()
			second := ar.bridge.generations[1]
			ar.bridge.mu.Unlock()
			if first == 0 || second == 0 || first == second {
				t.Fatalf("offers reused an identity: %d %d", first, second)
			}
			ar.bridge.loseGeneration("sess-secret|Browser#same", first, "delayed original loss")
			// A stale loss must neither free the current peer's quota nor send
			// a loss notice that would make its browser close working audio.
			for i := 0; i < MaxAudioPerLogin; i++ {
				c, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%23quota"+string(rune('a'+i)), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer c.CloseNow()
				sendMsg(t, c, map[string]any{"type": "offer", "sdp": "healthy"})
				m := readMsg(t, c)
				want := "answer"
				if i == MaxAudioPerLogin-1 {
					want = "refused"
				}
				if m["type"] != want {
					t.Fatalf("quota after stale loss: peer %d got %v, want %s", i, m, want)
				}
			}
			sendMsg(t, current, map[string]any{"type": "ice", "candidate": map[string]any{"candidate": "current peer"}})
			sendMsg(t, current, map[string]any{"type": "offer", "sdp": "still current"})
			if m := readMsg(t, current); m["type"] != "answer" {
				t.Fatalf("stale loss reached replacement: %v", m)
			}
			if newSocket {
				_ = old.Close(websocket.StatusNormalClosure, "")
			}
			_, _, closed, cands := ar.bridge.snapshot()
			if len(closed) != 0 || len(cands) != 1 {
				t.Fatalf("replacement disrupted: closed=%v candidates=%v", closed, cands)
			}
		})
	}
}

func TestSignalLossDuringOfferDoesNotReturnEndedPeer(t *testing.T) {
	ar := newAudioRig(t, true)
	ar.bridge.connectHook = func(ctx context.Context, offer, tab string, generation uint64) error {
		if offer == "lost during connect" {
			ar.bridge.loseGeneration(tab, generation, "connection ended before answer")
		}
		return nil
	}
	c, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%23earlyloss", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	sendMsg(t, c, map[string]any{"type": "offer", "sdp": "lost during connect"})
	if m := readMsg(t, c); m["type"] != "audio-lost" {
		t.Fatalf("early loss: %v", m)
	}
	if m := readMsg(t, c); m["type"] != "error" {
		t.Fatalf("ended peer answered: %v", m)
	}
	// This socket's lost lifecycle cannot be revived by racing another offer.
	sendMsg(t, c, map[string]any{"type": "offer", "sdp": "retry"})
	if m := readMsg(t, c); m["type"] != "error" {
		t.Fatalf("ended socket revived a peer: %v", m)
	}
	replacement, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%23earlyloss", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer replacement.CloseNow()
	sendMsg(t, replacement, map[string]any{"type": "offer", "sdp": "retry"})
	if m := readMsg(t, replacement); m["type"] != "answer" {
		t.Fatalf("fresh socket retry: %v", m)
	}
}

func TestSignalOverlappingOffersStayInOrderWithoutBlockingOtherTabs(t *testing.T) {
	ar := newAudioRig(t, true)
	entered, resume := make(chan struct{}), make(chan struct{})
	defer close(resume)
	ar.bridge.connectHook = func(ctx context.Context, offer, tab string, generation uint64) error {
		if offer == "slow original" {
			close(entered)
			select {
			case <-resume:
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		return nil
	}
	old, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%23overlap", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer old.CloseNow()
	sendMsg(t, old, map[string]any{"type": "offer", "sdp": "slow original"})
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("original did not enter Connect")
	}
	current, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%23overlap", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer current.CloseNow()
	sendMsg(t, current, map[string]any{"type": "offer", "sdp": "new socket"})
	// Quota includes the pending original. A different tab can connect while
	// the same tab's replacement waits for its own preceding offer.
	other, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%23independent", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer other.CloseNow()
	sendMsg(t, other, map[string]any{"type": "offer", "sdp": "independent"})
	if m := readMsg(t, other); m["type"] != "answer" {
		t.Fatalf("other tab blocked: %v", m)
	}
	for i := 0; i < MaxAudioPerLogin-1; i++ {
		c, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%23pendingquota"+string(rune('a'+i)), nil)
		if err != nil {
			t.Fatal(err)
		}
		defer c.CloseNow()
		sendMsg(t, c, map[string]any{"type": "offer", "sdp": "quota"})
		want := "answer"
		if i == MaxAudioPerLogin-2 {
			want = "refused"
		}
		if m := readMsg(t, c); m["type"] != want {
			t.Fatalf("pending peer not counted in quota: %v, want %s", m, want)
		}
	}
	// Ending the original offer allows the replacement to follow it. Keep
	// the channel open here; the deferred close also releases failure paths.
	resume <- struct{}{}
	readMsg(t, old)
	if m := readMsg(t, current); m["type"] != "answer" {
		t.Fatalf("replacement: %v", m)
	}
	offers, _, _, _ := ar.bridge.snapshot()
	if strings.Join(offers, ",") != "slow original,independent,quota,quota,new socket" {
		t.Fatalf("overlapping offers reached bridge out of order: %v", offers)
	}
	_ = old.Close(websocket.StatusNormalClosure, "")
	sendMsg(t, current, map[string]any{"type": "offer", "sdp": "after old closes"})
	if m := readMsg(t, current); m["type"] != "answer" {
		t.Fatalf("current after old closes: %v", m)
	}
	if _, _, closed, _ := ar.bridge.snapshot(); len(closed) != 0 {
		t.Fatalf("old socket closed replacement: %v", closed)
	}
}

// One tab's slow close does not hold up another tab's offer.
func TestSignalCloseDoesNotBlockOtherTabs(t *testing.T) {
	ar := newAudioRig(t, true)
	gate := make(chan struct{})
	ar.bridge.mu.Lock()
	ar.bridge.gate = gate
	ar.bridge.mu.Unlock()
	defer func() {
		select {
		case <-gate:
		default:
			close(gate)
		}
	}()
	a, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%231", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer a.CloseNow()
	sendMsg(t, a, map[string]any{"type": "offer", "sdp": "a"})
	readMsg(t, a)
	sendMsg(t, a, map[string]any{"type": "close"}) // its Close now hangs on the gate
	time.Sleep(100 * time.Millisecond)
	b, _, err := ar.dial(t, "/api/audio/radio1/signal?tab=Browser%232", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer b.CloseNow()
	sendMsg(t, b, map[string]any{"type": "offer", "sdp": "b"})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, data, err := b.Read(ctx)
	if err != nil || !strings.Contains(string(data), `"answer"`) {
		t.Fatalf("another tab's offer waited on a close: %v %q", err, data)
	}
	close(gate)
}
