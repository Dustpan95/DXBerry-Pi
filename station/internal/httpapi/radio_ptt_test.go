package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/rig"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/session"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/settings"
)

// ptBridge is a radio's WebRTC bridge as PTT sees it: which tabs have a microphone coming in,
// which may talk.
type ptBridge struct {
	mu      sync.Mutex
	inbound map[string]bool
	allowed map[string]bool
}

func (b *ptBridge) SetTXAllowed(tab string, on bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if on {
		b.allowed[tab] = true
	} else {
		delete(b.allowed, tab)
	}
}
func (b *ptBridge) HasInbound(tab string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inbound[tab]
}
func (b *ptBridge) OnLost(fn func(tab, reason string)) func() { return func() {} }
func (b *ptBridge) OnClosed(fn func(tab string)) func()       { return func() {} }
func (b *ptBridge) talking(tab string) bool                   { b.mu.Lock(); defer b.mu.Unlock(); return b.allowed[tab] }

type ptServer struct {
	mu     sync.Mutex
	ok     bool
	why    string
	loaded int
}

func (s *ptServer) Available(string) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ok, s.why
}
func (s *ptServer) LoadTXLoopback(context.Context, string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loaded++
	return 7, nil
}

// UnloadStaleTXLoopbacks unloads every TX loopback, as PTT's teardown does (by source, not number).
func (s *ptServer) UnloadStaleTXLoopbacks(context.Context, string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := s.loaded
	s.loaded = 0
	return n, nil
}
func (s *ptServer) UnloadTXLoopback(context.Context, string, int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.loaded--
	return nil
}

type pttFixture struct {
	h     http.Handler
	rig   *rwRig
	br    *ptBridge
	srv   *ptServer
	mu    sync.Mutex
	notes []string
}

func (f *pttFixture) noticeLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.notes...)
}

func newPTTFixture(loggedIn bool) *pttFixture {
	f := &pttFixture{
		rig: &rwRig{st: rig.State{Connected: true, Mode: "USB", Levels: map[string]float64{}, Meters: map[string]float64{}, Optimistic: map[string]bool{}}},
		br:  &ptBridge{inbound: map[string]bool{}, allowed: map[string]bool{}},
		srv: &ptServer{ok: true},
	}
	var auth AuthChecker = multiAuth{}
	if !loggedIn {
		auth = rwAuth{ok: false}
	}
	api := &RadioAPI{
		Auth: auth, Radios: rwRecords{"radio1": {Name: "radio1", Port: 4532, PTT: "rigctld"}}, Rigs: rwRigs{f.rig},
		Owners: session.NewOwners(realNow{}), Ctx: context.Background(),
		Audio:          func(string) PTTAudio { return PTTAudio{Bridge: f.br, Server: f.srv} },
		Available:      f.srv.Available,
		Settings:       func(string) settings.RadioSettings { return settings.Defaults() },
		PTTInboundWait: 50 * time.Millisecond,
		Notify: func(radio string, msg any) {
			b, _ := json.Marshal(msg)
			f.mu.Lock()
			f.notes = append(f.notes, string(b))
			f.mu.Unlock()
		},
	}
	f.h = New(Config{Listen: "127.0.0.1:0", Auth: auth, Routes: api.Register}).Handler
	return f
}

var (
	pttOwner = map[string]string{TabHeader: "iPad#a1", "X-Test-Session": "s1"}
	pttOther = map[string]string{TabHeader: "iPhone#b2", "X-Test-Session": "s2"}
)

func body(rec *httptest.ResponseRecorder) string { return strings.TrimSpace(rec.Body.String()) }

func TestPTTRouteRequiresSession(t *testing.T) {
	f := newPTTFixture(false)
	if rec := rwDo(f.h, "POST", "/api/radio/radio1/ptt", `{"on":true}`, pttOwner); rec.Code != 401 {
		t.Fatalf("logged out: %d", rec.Code)
	}
	f = newPTTFixture(true)
	req := httptest.NewRequest("POST", "/api/radio/radio1/ptt", strings.NewReader(`{"on":true}`))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	req.Header.Set(TabHeader, "iPad#a1")
	rec := httptest.NewRecorder()
	f.h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("cross-site: %d", rec.Code)
	}
	if rec := rwDo(f.h, "POST", "/api/radio/nope/ptt", `{"on":true}`, pttOwner); rec.Code != 404 {
		t.Fatalf("unknown radio: %d", rec.Code)
	}
	if rec := rwDo(f.h, "POST", "/api/radio/radio1/ptt", `{}`, pttOwner); strings.Contains(body(rec), `"ok":true`) {
		t.Fatalf("no on and no hb: %q", body(rec))
	}
	if len(f.rig.calls) != 0 {
		t.Fatalf("a refused request reached the radio: %v", f.rig.calls)
	}
}

func TestPTTRouteOwnerOnly(t *testing.T) {
	f := newPTTFixture(true)
	f.br.inbound["s1|iPad#a1"], f.br.inbound["s2|iPhone#b2"] = true, true
	if got := body(rwDo(f.h, "POST", "/api/radio/radio1/ptt", `{"on":true}`, pttOwner)); got != `{"ok":false,"reason":"this tab does not hold the transmitter","fix":"claim the radio first"}` {
		t.Fatalf("before claim: %s", got)
	}
	rwDo(f.h, "POST", "/api/radio/radio1/claim", `{}`, pttOwner)
	if got := body(rwDo(f.h, "POST", "/api/radio/radio1/ptt", `{"on":true}`, pttOwner)); !strings.HasPrefix(got, `{"ok":true,"window":`) {
		t.Fatalf("on: %s", got)
	}
	if !f.br.talking("s1|iPad#a1") || f.rig.calls[len(f.rig.calls)-1] != "set_ptt 1" {
		t.Fatalf("not keyed: %v", f.rig.calls)
	}
	// another tab's on, heartbeat and off are refused and change nothing
	if got := body(rwDo(f.h, "POST", "/api/radio/radio1/ptt", `{"on":true}`, pttOther)); strings.Contains(got, `"ok":true`) {
		t.Fatalf("other on: %s", got)
	}
	if got := body(rwDo(f.h, "POST", "/api/radio/radio1/ptt", `{"hb":true}`, pttOther)); got != `{"ok":false,"reason":"you do not hold the transmitter"}` {
		t.Fatalf("other heartbeat: %s", got)
	}
	if got := body(rwDo(f.h, "POST", "/api/radio/radio1/ptt", `{"on":false}`, pttOther)); got != `{"ok":false,"reason":"you do not hold the transmitter"}` {
		t.Fatalf("other off: %s", got)
	}
	if !f.br.talking("s1|iPad#a1") {
		t.Fatal("another tab ended the owner's PTT")
	}
	if got := body(rwDo(f.h, "POST", "/api/radio/radio1/ptt", `{"hb":true}`, pttOwner)); got != `{"ok":true}` {
		t.Fatalf("heartbeat: %s", got)
	}
	// the owner's GET says PTT is on and its; the sound server's state comes with it
	var g struct {
		Audio struct {
			Available bool   `json:"available"`
			Reason    string `json:"reason"`
		} `json:"audio"`
		PTT struct {
			On       bool   `json:"on"`
			Mine     bool   `json:"mine"`
			TimeoutS int    `json:"timeout_s"`
			Method   string `json:"method"`
		} `json:"ptt"`
	}
	rec := rwDo(f.h, "GET", "/api/radio/radio1", "", pttOwner)
	if err := json.Unmarshal(rec.Body.Bytes(), &g); err != nil {
		t.Fatal(err)
	}
	if !g.Audio.Available || !g.PTT.On || !g.PTT.Mine || g.PTT.TimeoutS != 180 || g.PTT.Method != "rigctld" {
		t.Fatalf("get: %s", rec.Body.String())
	}
	rec = rwDo(f.h, "GET", "/api/radio/radio1", "", pttOther)
	_ = json.Unmarshal(rec.Body.Bytes(), &g)
	if !g.PTT.On || g.PTT.Mine {
		t.Fatalf("other's get: %s", rec.Body.String())
	}
	if got := body(rwDo(f.h, "POST", "/api/radio/radio1/ptt", `{"on":false}`, pttOwner)); got != `{"ok":true}` {
		t.Fatalf("off: %s", got)
	}
	if f.br.talking("s1|iPad#a1") || f.rig.calls[len(f.rig.calls)-1] != "set_ptt 0" {
		t.Fatalf("not unkeyed: %v", f.rig.calls)
	}
	if got := f.noticeLog(); len(got) != 1 || got[0] != `{"type":"ptt","radio":"radio1","on":false,"reason":"released","window":1}` {
		t.Fatalf("notices %v", got)
	}
}

func TestPTTRouteRefusesUntilTheMicIsOpen(t *testing.T) {
	f := newPTTFixture(true)
	rwDo(f.h, "POST", "/api/radio/radio1/claim", `{}`, pttOwner)
	want := `{"ok":false,"reason":"the microphone is not open yet","fix":"press PTT again once the browser has asked for the microphone"}`
	if got := body(rwDo(f.h, "POST", "/api/radio/radio1/ptt", `{"on":true}`, pttOwner)); got != want {
		t.Fatalf("got %s", got)
	}
	if len(f.rig.calls) != 0 || f.srv.loaded != 0 {
		t.Fatalf("keyed before the mic was open: %v", f.rig.calls)
	}
	f.br.mu.Lock()
	f.br.inbound["s1|iPad#a1"] = true
	f.br.mu.Unlock()
	if got := body(rwDo(f.h, "POST", "/api/radio/radio1/ptt", `{"on":true}`, pttOwner)); !strings.HasPrefix(got, `{"ok":true,"window":`) {
		t.Fatalf("with the mic: %s", got)
	}
	rwDo(f.h, "POST", "/api/radio/radio1/ptt", `{"on":false}`, pttOwner)
}

func TestPTTRouteRefusedWithoutSoundServer(t *testing.T) {
	f := newPTTFixture(true)
	f.br.inbound["s1|iPad#a1"] = true
	f.srv.ok, f.srv.why = false, "no sound server for radio1: WSJT-X has the sound card"
	rwDo(f.h, "POST", "/api/radio/radio1/claim", `{}`, pttOwner)
	want := `{"ok":false,"reason":"no sound server for radio1: WSJT-X has the sound card","fix":"start the radio's sound server or stop the program that holds the sound card"}`
	if got := body(rwDo(f.h, "POST", "/api/radio/radio1/ptt", `{"on":true}`, pttOwner)); got != want {
		t.Fatalf("got %s", got)
	}
	var g struct {
		Audio map[string]any `json:"audio"`
	}
	rec := rwDo(f.h, "GET", "/api/radio/radio1", "", pttOwner)
	_ = json.Unmarshal(rec.Body.Bytes(), &g)
	if g.Audio["available"] != false || g.Audio["reason"] != "no sound server for radio1: WSJT-X has the sound card" {
		t.Fatalf("get: %s", rec.Body.String())
	}
}

func TestStopRouteEndsPTTAndNamesTheDevice(t *testing.T) {
	f := newPTTFixture(true)
	f.br.inbound["s1|iPad#a1"] = true
	rwDo(f.h, "POST", "/api/radio/radio1/claim", `{}`, pttOwner)
	rwDo(f.h, "POST", "/api/radio/radio1/ptt", `{"on":true}`, pttOwner)
	if got := body(rwDo(f.h, "POST", "/api/radio/radio1/stop", `{}`, pttOther)); got != `{"ok":true}` {
		t.Fatalf("stop: %s", got)
	}
	if f.br.talking("s1|iPad#a1") || f.rig.calls[len(f.rig.calls)-1] != "set_ptt 0" {
		t.Fatalf("stop left PTT: %v", f.rig.calls)
	}
	if got := f.noticeLog(); len(got) != 1 || got[0] != `{"type":"ptt","radio":"radio1","on":false,"reason":"STOP from iPhone","window":1}` {
		t.Fatalf("notices %v", got)
	}
}

func TestReleaseEndsPTT(t *testing.T) {
	f := newPTTFixture(true)
	f.br.inbound["s1|iPad#a1"] = true
	rwDo(f.h, "POST", "/api/radio/radio1/claim", `{}`, pttOwner)
	rwDo(f.h, "POST", "/api/radio/radio1/ptt", `{"on":true}`, pttOwner)
	rwDo(f.h, "POST", "/api/radio/radio1/release", `{}`, pttOwner)
	if f.br.talking("s1|iPad#a1") || f.rig.calls[len(f.rig.calls)-1] != "set_ptt 0" {
		t.Fatalf("release left PTT: %v", f.rig.calls)
	}
	if got := f.noticeLog(); len(got) != 1 || !strings.Contains(got[0], `"reason":"the transmitter was released"`) {
		t.Fatalf("notices %v", got)
	}
}

func TestShutdownEndsPTT(t *testing.T) {
	f := &rwRig{st: rig.State{Connected: true, Mode: "USB", Levels: map[string]float64{}, Meters: map[string]float64{}, Optimistic: map[string]bool{}}}
	br := &ptBridge{inbound: map[string]bool{"s1|iPad#a1": true}, allowed: map[string]bool{}}
	srv := &ptServer{ok: true}
	var mu sync.Mutex
	var notes []string
	api := &RadioAPI{Auth: multiAuth{}, Radios: rwRecords{"radio1": {Name: "radio1", Port: 4532, PTT: "vox"}}, Rigs: rwRigs{f},
		Owners: session.NewOwners(realNow{}), Ctx: context.Background(),
		Audio: func(string) PTTAudio { return PTTAudio{Bridge: br, Server: srv} },
		Notify: func(_ string, m any) {
			b, _ := json.Marshal(m)
			mu.Lock()
			notes = append(notes, string(b))
			mu.Unlock()
		}}
	h := New(Config{Listen: "127.0.0.1:0", Auth: multiAuth{}, Routes: api.Register}).Handler
	rwDo(h, "POST", "/api/radio/radio1/claim", `{}`, pttOwner)
	if got := body(rwDo(h, "POST", "/api/radio/radio1/ptt", `{"on":true}`, pttOwner)); !strings.HasPrefix(got, `{"ok":true,"window":`) {
		t.Fatalf("on: %s", got)
	}
	// a vox radio owes no unkey, yet shutdown closes its audio path
	StopService(&shutdownRecorder{f: f}, 0, api)
	if br.talking("s1|iPad#a1") || srv.loaded != 0 {
		t.Fatal("shutdown left the audio path open")
	}
	mu.Lock()
	defer mu.Unlock()
	if len(notes) != 1 || !strings.Contains(notes[0], `"reason":"the station is stopping"`) {
		t.Fatalf("notices %v", notes)
	}
}

// Without an audio side the route refuses PTT in words, it does not panic.
func TestPTTRouteWithoutAudio(t *testing.T) {
	f := &rwRig{st: rig.State{Connected: true, Levels: map[string]float64{}, Meters: map[string]float64{}, Optimistic: map[string]bool{}}}
	api := &RadioAPI{Auth: multiAuth{}, Radios: rwRecords{"radio1": radios.Radio{Name: "radio1", Port: 4532, PTT: "rigctld"}}, Rigs: rwRigs{f}, Owners: session.NewOwners(realNow{})}
	h := New(Config{Listen: "127.0.0.1:0", Auth: multiAuth{}, Routes: api.Register}).Handler
	rwDo(h, "POST", "/api/radio/radio1/claim", `{}`, pttOwner)
	got := body(rwDo(h, "POST", "/api/radio/radio1/ptt", `{"on":true}`, pttOwner))
	if !strings.Contains(got, `"ok":false`) || !strings.Contains(got, "no browser audio") {
		t.Fatalf("got %s", got)
	}
	rec := rwDo(h, "GET", "/api/radio/radio1", "", pttOwner)
	if !strings.Contains(rec.Body.String(), `"audio":{"available":false,"reason":"this station has no browser audio"}`) {
		t.Fatalf("get: %s", rec.Body.String())
	}
	if len(f.calls) != 0 {
		t.Fatalf("calls %v", f.calls)
	}
}

// Every radio record has a rigctld port (provision/lib/radio.sh allocates one on add and rejects a
// record without one; a radio with no CAT runs rigctld on Hamlib's dummy model), so the manager
// has a Writer for a vox radio too, even while its dummy rigctld is down: claim, PTT, release and
// STOP pass the rig-control gate and key nothing through CAT.
func TestVoxRadioWithADummyRigctldClaimsAndTalks(t *testing.T) {
	f := &rwRig{st: rig.State{Connected: false, Levels: map[string]float64{}, Meters: map[string]float64{}, Optimistic: map[string]bool{}}}
	br := &ptBridge{inbound: map[string]bool{"s1|iPad#a1": true}, allowed: map[string]bool{}}
	srv := &ptServer{ok: true}
	api := &RadioAPI{Auth: multiAuth{}, Radios: rwRecords{"radio1": {Name: "radio1", Port: 4534, PTT: "vox", Model: 1}}, Rigs: rwRigs{f},
		Owners: session.NewOwners(realNow{}), Ctx: context.Background(),
		Audio: func(string) PTTAudio { return PTTAudio{Bridge: br, Server: srv} }}
	h := New(Config{Listen: "127.0.0.1:0", Auth: multiAuth{}, Routes: api.Register}).Handler
	if got := body(rwDo(h, "POST", "/api/radio/radio1/claim", `{}`, pttOwner)); got != `{"ok":true}` {
		t.Fatalf("claim: %s", got)
	}
	if got := body(rwDo(h, "POST", "/api/radio/radio1/ptt", `{"on":true}`, pttOwner)); !strings.HasPrefix(got, `{"ok":true,"window":`) {
		t.Fatalf("on: %s", got)
	}
	if !br.talking("s1|iPad#a1") || srv.loaded != 1 {
		t.Fatal("the audio path did not open")
	}
	if got := body(rwDo(h, "POST", "/api/radio/radio1/ptt", `{"on":false}`, pttOwner)); got != `{"ok":true}` {
		t.Fatalf("off: %s", got)
	}
	if got := body(rwDo(h, "POST", "/api/radio/radio1/release", `{}`, pttOwner)); got != `{"ok":true}` {
		t.Fatalf("release: %s", got)
	}
	if br.talking("s1|iPad#a1") || srv.loaded != 0 || len(f.calls) != 0 {
		t.Fatalf("left open or keyed through CAT: %v", f.calls)
	}
}

// The poller's hooks carry all three of the guard's inputs, the station-transmit flag included:
// a read through SWRTx reaches the radio's guard.
func TestPollerHooksCarryTheStationTransmitFlag(t *testing.T) {
	f := newGuardFixture(true)
	hooks := f.api.PollerHooks("radio1")
	if hooks.SWR == nil || hooks.SWRTx == nil || hooks.Freq == nil {
		t.Fatalf("hooks missing: SWR %v SWRTx %v Freq %v", hooks.SWR != nil, hooks.SWRTx != nil, hooks.Freq != nil)
	}
	hooks.Freq(14074000)
	for i := 0; i < 3; i++ {
		hooks.SWRTx(5.8, true, true)
	}
	if g := f.guardView(t, pttOwner); !g.Active || !g.Latched {
		t.Fatalf("SWRTx reads did not reach the guard: %+v", g)
	}
	// a radio the records lack: the hooks are safe to call and do nothing
	other := f.api.PollerHooks("gone")
	other.SWR(9, true)
	other.SWRTx(9, true, true)
	other.Freq(7074000)
}
