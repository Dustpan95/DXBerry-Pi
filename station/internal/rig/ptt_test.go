package rig

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/settings"
)

// ptBridge is the radio's WebRTC bridge as PTT sees it. Its OnLost and OnClosed subscribers are
// kept apart, as the real bridge keeps them: a close is heard only by OnClosed.
type ptBridge struct {
	mu      sync.Mutex
	allowed map[string]bool
	inbound map[string]bool
	events  *[]string
	lost    map[int]func(string, string)
	closed  map[int]func(string)
	next    int
}

func (b *ptBridge) SetTXAllowed(tab string, on bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if on {
		b.allowed[tab] = true
	} else {
		delete(b.allowed, tab)
	}
	*b.events = append(*b.events, "tx "+tab+" "+map[bool]string{true: "on", false: "off"}[on])
}

func (b *ptBridge) TXAllowed(tab string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.allowed[tab]
}

func (b *ptBridge) HasInbound(tab string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.inbound[tab]
}

func (b *ptBridge) OnLost(fn func(tab, reason string)) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	id := b.next
	b.next++
	b.lost[id] = fn
	return func() { b.mu.Lock(); delete(b.lost, id); b.mu.Unlock() }
}

func (b *ptBridge) OnClosed(fn func(tab string)) func() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed == nil {
		b.closed = map[int]func(string){}
	}
	id := b.next
	b.next++
	b.closed[id] = fn
	return func() { b.mu.Lock(); delete(b.closed, id); b.mu.Unlock() }
}

// close is the bridge's Close: the connection goes (no microphone any more) and only OnClosed
// hears it.
func (b *ptBridge) close(tab string) {
	b.mu.Lock()
	b.inbound[tab] = false
	fns := []func(string){}
	for _, f := range b.closed {
		fns = append(fns, f)
	}
	b.mu.Unlock()
	for _, f := range fns {
		f(tab)
	}
}

func (b *ptBridge) lose(tab, reason string) {
	b.mu.Lock()
	fns := []func(string, string){}
	for _, f := range b.lost {
		fns = append(fns, f)
	}
	b.mu.Unlock()
	for _, f := range fns {
		f(tab, reason)
	}
}

// ptServer is the radio's sound server control.
type ptServer struct {
	mu        sync.Mutex
	ok        bool
	why       string
	loadErr   error
	unloadErr error
	loaded    map[int]bool
	next      int
	events    *[]string
}

func (s *ptServer) Available(radio string) (bool, string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ok, s.why
}

func (s *ptServer) LoadTXLoopback(ctx context.Context, radio string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.loadErr != nil {
		return 0, s.loadErr
	}
	s.next++
	s.loaded[s.next] = true
	*s.events = append(*s.events, "load "+radio)
	return s.next, nil
}

func (s *ptServer) UnloadTXLoopback(ctx context.Context, radio string, id int) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.unloadErr != nil {
		*s.events = append(*s.events, "unload failed")
		return s.unloadErr
	}
	delete(s.loaded, id)
	*s.events = append(*s.events, "unload "+radio)
	return nil
}

func (s *ptServer) UnloadStaleTXLoopbacks(ctx context.Context, radio string) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	n := len(s.loaded)
	s.loaded = map[int]bool{}
	*s.events = append(*s.events, "clear "+radio)
	return n, nil
}

func (s *ptServer) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.loaded)
}

// ptTone records what the keying tone wrote.
type ptTone struct {
	mu      sync.Mutex
	started bool
	closed  bool
	frames  [][]int16
}

func (p *ptTone) Start(ctx context.Context) error {
	p.mu.Lock()
	p.started = true
	p.mu.Unlock()
	return nil
}
func (p *ptTone) Write(f []int16) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return errors.New("closed")
	}
	p.frames = append(p.frames, append([]int16(nil), f...))
	return nil
}
func (p *ptTone) Close() error { p.mu.Lock(); p.closed = true; p.mu.Unlock(); return nil }

type ptHarness struct {
	*ixHarness
	p      *PTT
	br     *ptBridge
	srv    *ptServer
	tone   *ptTone
	timeS  int
	mu     sync.Mutex // the fakes' shared event log and the notices
	events []string   // bridge and server calls, in order
	notes  []string
}

func newPTTHarness(t *testing.T, method string) *ptHarness {
	t.Helper()
	ix := newIxHarness(ixCaps())
	h := &ptHarness{ixHarness: ix, tone: &ptTone{}, timeS: settings.DefaultPTTTimeoutS}
	h.br = &ptBridge{allowed: map[string]bool{}, inbound: map[string]bool{"a": true, "b": true}, events: &h.events, lost: map[int]func(string, string){}}
	h.srv = &ptServer{ok: true, loaded: map[int]bool{}, events: &h.events}
	h.p = NewPTT(ix.in, lockedBridge{h}, lockedServer{h}, func() settings.RadioSettings {
		h.mu.Lock()
		defer h.mu.Unlock()
		return settings.RadioSettings{SWRLimit: 3, PTTTimeoutS: h.timeS}
	}, method, func(radio string, msg any) {
		b, err := json.Marshal(msg)
		if err != nil {
			t.Errorf("notice does not encode: %v", err)
		}
		h.mu.Lock()
		h.notes = append(h.notes, string(b))
		h.mu.Unlock()
	}, ix.clock, slog.New(slog.NewTextHandler(io.Discard, nil)))
	h.p.Tone = func() TonePlayer { return h.tone }
	// the owner lock's drop path, as RadioAPI.Register wires it
	ix.own.OnDrop(func(string) { h.p.OwnerGone(); ix.in.Unkey() })
	return h
}

// lockedBridge and lockedServer serialize the fakes on the harness lock, so their shared event
// log is consistent.
type lockedBridge struct{ h *ptHarness }

func (l lockedBridge) SetTXAllowed(tab string, on bool) {
	l.h.mu.Lock()
	defer l.h.mu.Unlock()
	l.h.br.SetTXAllowed(tab, on)
}
func (l lockedBridge) HasInbound(tab string) bool { return l.h.br.HasInbound(tab) }
func (l lockedBridge) OnLost(fn func(tab, reason string)) func() {
	return l.h.br.OnLost(fn)
}
func (l lockedBridge) OnClosed(fn func(tab string)) func() { return l.h.br.OnClosed(fn) }

type lockedServer struct{ h *ptHarness }

func (l lockedServer) Available(radio string) (bool, string) { return l.h.srv.Available(radio) }
func (l lockedServer) LoadTXLoopback(ctx context.Context, radio string) (int, error) {
	l.h.mu.Lock()
	defer l.h.mu.Unlock()
	return l.h.srv.LoadTXLoopback(ctx, radio)
}
func (l lockedServer) UnloadStaleTXLoopbacks(ctx context.Context, radio string) (int, error) {
	l.h.mu.Lock()
	defer l.h.mu.Unlock()
	return l.h.srv.UnloadStaleTXLoopbacks(ctx, radio)
}
func (l lockedServer) UnloadTXLoopback(ctx context.Context, radio string, id int) error {
	l.h.mu.Lock()
	defer l.h.mu.Unlock()
	return l.h.srv.UnloadTXLoopback(ctx, radio, id)
}

func (h *ptHarness) eventLog() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string{}, h.events...)
}

func (h *ptHarness) noticeLog() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]string{}, h.notes...)
}

func (h *ptHarness) claim(t *testing.T, tab string) {
	t.Helper()
	if r := h.in.Claim(ixHolder(tab)); !r.OK {
		t.Fatalf("claim %s: %+v", tab, r)
	}
}

func (h *ptHarness) on(t *testing.T, tab string) {
	t.Helper()
	if r := h.p.On(context.Background(), tab); !r.OK {
		t.Fatalf("ptt on: %+v (calls %v, events %v)", r, h.rig.log(), h.eventLog())
	}
}

// waitFor polls cond in real time (the fake clock is moved by the test itself).
func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("never: %s", what)
		}
		time.Sleep(2 * time.Millisecond)
	}
}

func offNotice(reason string) string {
	b, _ := json.Marshal(reason)
	return `{"type":"ptt","radio":"radio1","on":false,"reason":` + string(b) + `,"window":`
}

func (h *ptHarness) noticed(reason string) bool {
	for _, n := range h.noticeLog() {
		if strings.HasPrefix(n, offNotice(reason)) {
			return true
		}
	}
	return false
}

func TestPTTOnKeysAndAllowsTX(t *testing.T) {
	for _, method := range []string{"rigctld", "cm108", "gpio"} {
		h := newPTTHarness(t, method)
		dir := t.TempDir()
		h.in.Marks = &KeyedMarks{Dir: dir}
		h.claim(t, "a")
		h.on(t, "a")
		if got := strings.Join(h.rig.log(), ","); got != "set_ptt 1" {
			t.Fatalf("%s: rig calls %q", method, got)
		}
		if !h.rig.State().PTT || !h.in.Owed() || !markerAt(t, dir) {
			t.Fatalf("%s: the carrier machinery does not know it is keyed", method)
		}
		if got := strings.Join(h.eventLog(), ","); got != "clear radio1,load radio1,tx a on" {
			t.Fatalf("%s: audio side %q", method, got)
		}
		if !h.br.TXAllowed("a") || h.br.TXAllowed("b") || h.srv.count() != 1 {
			t.Fatalf("%s: TX allowed for the wrong tab", method)
		}
		if !h.p.Active() {
			t.Fatalf("%s: not active", method)
		}
		// a second "on" from the same tab is a heartbeat, not a second key-on
		h.on(t, "a")
		if h.count("set_ptt 1") != 1 || h.srv.count() != 1 {
			t.Fatalf("%s: a resent on keyed again: %v %v", method, h.rig.log(), h.eventLog())
		}
		if r := h.p.Off("a", "released"); !r.OK {
			t.Fatalf("%s: off: %+v", method, r)
		}
		if h.count("set_ptt 0") != 1 || h.rig.State().PTT || h.in.Owed() || markerAt(t, dir) {
			t.Fatalf("%s: not unkeyed: %v", method, h.rig.log())
		}
		if got := strings.Join(h.eventLog(), ","); got != "clear radio1,load radio1,tx a on,tx a off,unload radio1" {
			t.Fatalf("%s: audio side after off %q", method, got)
		}
		if got := h.noticeLog(); len(got) != 1 || !strings.HasPrefix(got[0], offNotice("released")) {
			t.Fatalf("%s: notices %v", method, got)
		}
		// off again is idempotent: nothing more is sent, no second notice
		if r := h.p.Off("a", "released"); !r.OK || h.count("set_ptt 0") != 1 || len(h.noticeLog()) != 1 {
			t.Fatalf("%s: second off: %+v %v %v", method, r, h.rig.log(), h.noticeLog())
		}
		if h.p.Active() {
			t.Fatalf("%s: still active", method)
		}
	}
}

func TestPTTRequiresOwner(t *testing.T) {
	h := newPTTHarness(t, "rigctld")
	ctx := context.Background()
	ixRefused(t, h.p.On(ctx, "a"), "does not hold the transmitter", "claim the radio first")
	h.claim(t, "b")
	ixRefused(t, h.p.On(ctx, "a"), "does not hold the transmitter", "claim the radio first")
	if len(h.rig.log()) != 0 || len(h.eventLog()) != 0 {
		t.Fatalf("a refused PTT reached the radio: %v %v", h.rig.log(), h.eventLog())
	}
	// the owner's heartbeat and off from a tab that is not the owner are ignored
	h.on(t, "b")
	if r := h.p.Heartbeat("a"); r.OK || r.Reason != "you do not hold the transmitter" {
		t.Fatalf("heartbeat from a non-owner: %+v", r)
	}
	if r := h.p.Off("a", "released"); r.OK || r.Reason != "you do not hold the transmitter" {
		t.Fatalf("off from a non-owner: %+v", r)
	}
	if !h.br.TXAllowed("b") || h.count("set_ptt 0") != 0 {
		t.Fatal("a non-owner ended the owner's PTT")
	}
	h.p.Off("b", "released")
}

func TestPTTRefusedWithoutSoundServerWhenAudioRequested(t *testing.T) {
	h := newPTTHarness(t, "rigctld")
	h.claim(t, "a")
	h.srv.ok, h.srv.why = false, "no sound server for radio1: sink RADIO1.tx is missing"
	r := h.p.On(context.Background(), "a")
	if r.OK || r.Reason != "no sound server for radio1: sink RADIO1.tx is missing" ||
		r.Fix != "start the radio's sound server or stop the program that holds the sound card" {
		t.Fatalf("got %+v", r)
	}
	// the sound server is there but this tab's connection carries no microphone yet
	h.srv.ok, h.srv.why = true, ""
	h.br.mu.Lock()
	h.br.inbound["a"] = false
	h.br.mu.Unlock()
	r = h.p.On(context.Background(), "a")
	if r.OK || r.Reason != "the microphone is not open yet" || r.Fix != "press PTT again once the browser has asked for the microphone" {
		t.Fatalf("got %+v", r)
	}
	if len(h.rig.log()) != 0 || len(h.eventLog()) != 0 || h.in.Owed() || h.p.Active() {
		t.Fatalf("a refused PTT keyed or opened something: %v %v", h.rig.log(), h.eventLog())
	}
	// the loopback cannot be loaded after the key-on: the key-on is undone at once
	h.br.mu.Lock()
	h.br.inbound["a"] = true
	h.br.mu.Unlock()
	h.srv.loadErr = errors.New("pactl: Failure: No such entity")
	r = h.p.On(context.Background(), "a")
	if r.OK || !strings.Contains(r.Reason, "No such entity") {
		t.Fatalf("got %+v", r)
	}
	if got := strings.Join(h.rig.log(), ","); got != "set_ptt 1,set_ptt 0" || h.in.Owed() || h.p.Active() || h.br.TXAllowed("a") {
		t.Fatalf("a failed loopback left the radio keyed: %q %v", got, h.eventLog())
	}
}

func TestPTTDropsOnHeartbeatLoss(t *testing.T) {
	h := newPTTHarness(t, "rigctld")
	h.claim(t, "a")
	h.on(t, "a")
	h.clock.Advance(5 * time.Second)
	if r := h.p.Heartbeat("a"); !r.OK {
		t.Fatalf("heartbeat: %+v", r)
	}
	h.clock.Advance(5 * time.Second) // 10 s on, 5 s since the heartbeat
	settle()
	if !h.p.Active() || h.count("set_ptt 0") != 0 {
		t.Fatal("dropped while the heartbeat was fresh")
	}
	h.clock.Advance(2 * time.Second) // 7 s since the heartbeat
	waitFor(t, "PTT dropped for a lost heartbeat", func() bool { return !h.p.Active() && h.count("set_ptt 0") == 1 })
	waitFor(t, "the notice", func() bool { return h.noticed("heartbeat lost") })
	if h.br.TXAllowed("a") || h.srv.count() != 0 {
		t.Fatal("the audio side was left open")
	}
	if r := h.p.Heartbeat("a"); r.OK {
		t.Fatalf("a heartbeat after the drop was taken: %+v", r)
	}
}

func TestPTTDropsOnTimeout(t *testing.T) {
	for _, tc := range []struct {
		secs int
		want string
	}{{30, "PTT timed out at 0:30"}, {180, "PTT timed out at 3:00"}, {125, "PTT timed out at 2:05"}} {
		h := newPTTHarness(t, "rigctld")
		h.timeS = tc.secs
		h.claim(t, "a")
		h.on(t, "a")
		for i := 0; i < tc.secs/5-1; i++ { // heartbeats every 5 s up to just short of the limit
			h.clock.Advance(5 * time.Second)
			if r := h.p.Heartbeat("a"); !r.OK {
				t.Fatalf("%d s: heartbeat at %d: %+v", tc.secs, (i+1)*5, r)
			}
		}
		settle()
		if !h.p.Active() {
			t.Fatalf("%d s: dropped before the timeout: %v", tc.secs, h.noticeLog())
		}
		h.clock.Advance(time.Duration(tc.secs)*time.Second - time.Duration(tc.secs/5-1)*5*time.Second)
		waitFor(t, tc.want, func() bool { return h.noticed(tc.want) })
		if h.p.Active() || h.count("set_ptt 0") != 1 || h.br.TXAllowed("a") {
			t.Fatalf("%d s: not dropped: %v", tc.secs, h.rig.log())
		}
	}
}

func TestPTTDropsOnOwnerSocketClose(t *testing.T) {
	h := newPTTHarness(t, "rigctld")
	h.own.SocketOpened("a")
	h.own.SocketOpened("a") // a reload: the new socket opens before the old one closes
	h.claim(t, "a")
	h.on(t, "a")
	h.own.SocketClosed("a")
	if !h.p.Active() {
		t.Fatal("dropped while the tab still had a socket")
	}
	h.own.SocketClosed("a")
	waitFor(t, "owner disconnected", func() bool { return h.noticed("owner disconnected") })
	if h.p.Active() || h.count("set_ptt 0") != 1 || h.br.TXAllowed("a") || h.srv.count() != 0 {
		t.Fatalf("socket close left PTT: %v %v", h.rig.log(), h.eventLog())
	}
}

func TestPTTDropsOnStop(t *testing.T) {
	h := newPTTHarness(t, "rigctld")
	h.claim(t, "a")
	h.on(t, "a")
	// any tab may STOP; the notice names the device that pressed it
	if r := h.p.Stop(context.Background(), "iPhone"); !r.OK {
		t.Fatalf("stop: %+v", r)
	}
	if h.p.Active() || h.count("set_ptt 0") != 1 || h.br.TXAllowed("a") || h.srv.count() != 0 {
		t.Fatalf("stop left PTT: %v %v", h.rig.log(), h.eventLog())
	}
	if got := h.noticeLog(); len(got) != 1 || !strings.HasPrefix(got[0], offNotice("STOP from iPhone")) {
		t.Fatalf("notices %v", got)
	}
	// STOP with no PTT on still unkeys the radio, whatever keyed it
	if r := h.p.Stop(context.Background(), "iPhone"); !r.OK || h.count("set_ptt 0") != 2 {
		t.Fatalf("stop without PTT: %+v %v", r, h.rig.log())
	}
	// a set_ptt 0 from the carrier machinery (Stop from the intents) ends the PTT window too
	h.on(t, "a")
	h.in.Stop(context.Background())
	waitFor(t, "the window closed with the carrier", func() bool { return !h.p.Active() && !h.br.TXAllowed("a") })
	// vox: nothing of ours keyed, STOP still sends set_ptt 0 and closes the audio path
	v := newPTTHarness(t, "vox")
	v.claim(t, "a")
	v.on(t, "a")
	if r := v.p.Stop(context.Background(), "iPad"); !r.OK || v.count("set_ptt 0") != 1 || v.p.Active() || v.srv.count() != 0 {
		t.Fatalf("vox stop: %+v %v %v", r, v.rig.log(), v.eventLog())
	}
	if !v.noticed("STOP from iPad") {
		t.Fatalf("vox notices %v", v.noticeLog())
	}
}

func TestPTTVoxKeysNothing(t *testing.T) {
	for _, method := range []string{"vox", "none"} {
		h := newPTTHarness(t, method)
		dir := t.TempDir()
		h.in.Marks = &KeyedMarks{Dir: dir}
		h.claim(t, "a")
		h.rig.setUp(false) // an audio-only radio has no link to lose
		h.on(t, "a")
		if len(h.rig.log()) != 0 || h.in.Owed() || markerAt(t, dir) {
			t.Fatalf("%s: keyed the radio: %v", method, h.rig.log())
		}
		if got := strings.Join(h.eventLog(), ","); got != "clear radio1,load radio1,tx a on" {
			t.Fatalf("%s: audio path %q", method, got)
		}
		if r := h.p.Off("a", "released"); !r.OK {
			t.Fatalf("%s: off: %+v", method, r)
		}
		if len(h.rig.log()) != 0 || h.srv.count() != 0 || h.br.TXAllowed("a") {
			t.Fatalf("%s: off: %v %v", method, h.rig.log(), h.eventLog())
		}
		h.tone.mu.Lock()
		started := h.tone.started
		h.tone.mu.Unlock()
		if started {
			t.Fatalf("%s: played the keying tone", method)
		}
	}
}

// digirig_tone keys with the plumbing spec's tone on the right channel of <CARD>.tx.
func TestPTTDigirigToneKeysWithTheTone(t *testing.T) {
	h := newPTTHarness(t, "digirig_tone")
	h.claim(t, "a")
	h.on(t, "a")
	if len(h.rig.log()) != 0 {
		t.Fatalf("keyed through rigctld: %v", h.rig.log())
	}
	waitFor(t, "tone frames", func() bool { h.tone.mu.Lock(); defer h.tone.mu.Unlock(); return len(h.tone.frames) >= 3 })
	h.tone.mu.Lock()
	f := h.tone.frames[1]
	h.tone.mu.Unlock()
	if len(f) != 2*960 {
		t.Fatalf("frame of %d samples, want 960 stereo pairs", len(f))
	}
	var left, peak int
	for i := 0; i < len(f); i += 2 {
		if f[i] != 0 {
			left++
		}
		peak = max(peak, abs(int(f[i+1])))
	}
	if left != 0 {
		t.Fatalf("%d left samples carry the tone", left)
	}
	if want := int(math.Round(ToneLevel * 32767)); peak < want-200 || peak > want+1 {
		t.Fatalf("tone peak %d, want about %d", peak, want)
	}
	h.p.Off("a", "released")
	h.tone.mu.Lock()
	closed := h.tone.closed
	h.tone.mu.Unlock()
	if !closed || h.srv.count() != 0 {
		t.Fatal("the tone outlived PTT")
	}
}

func abs(x int) int {
	if x < 0 {
		return -x
	}
	return x
}

func TestPTTOffRetriesWhileLinkDown(t *testing.T) {
	h := newPTTHarness(t, "rigctld")
	h.claim(t, "a")
	h.on(t, "a")
	h.rig.setUp(false)
	r := h.p.Off("a", "released")
	if r.OK || r.Reason != "rig control unreachable; the radio's rigctld is restarting" {
		t.Fatalf("off while down: %+v", r)
	}
	// the audio side closes at once; the unkey stays owed
	if h.br.TXAllowed("a") || h.srv.count() != 0 || h.p.Active() || !h.in.Pending() || !h.noticed("released") {
		t.Fatalf("off while down: %v %v", h.eventLog(), h.noticeLog())
	}
	if u := h.rig.urgentLog(); len(u) == 0 || !u[0] {
		t.Fatalf("the owed unkey did not make the link urgent: %v", u)
	}
	h.clock.Advance(3 * time.Second)
	settle()
	if h.count("set_ptt 0") != 0 {
		t.Fatal("sent while down")
	}
	h.rig.setUp(true)
	h.tick(t, "set_ptt 0 after the link returns", func() bool { return h.count("set_ptt 0") > 0 })
	h.tick(t, "nothing owed", func() bool { return !h.in.Pending() && !h.in.Owed() })
}

func TestStalePTTOffIgnored(t *testing.T) {
	h := newPTTHarness(t, "rigctld")
	h.own.SocketOpened("a")
	h.claim(t, "a")
	h.on(t, "a")
	h.own.SocketClosed("a") // tab a drops out: its PTT and claim end
	waitFor(t, "a's PTT dropped", func() bool { return !h.p.Active() })
	h.claim(t, "b")
	h.on(t, "b")
	keys := h.count("set_ptt 0")
	// a's late release arrives (it reconnected): it must not unkey b
	if r := h.p.Off("a", "released"); r.OK || r.Reason != "you do not hold the transmitter" {
		t.Fatalf("stale off: %+v", r)
	}
	if r := h.p.Heartbeat("a"); r.OK {
		t.Fatalf("stale heartbeat: %+v", r)
	}
	if !h.p.Active() || !h.br.TXAllowed("b") || h.count("set_ptt 0") != keys {
		t.Fatalf("the stale release unkeyed the new owner: %v %v", h.rig.log(), h.eventLog())
	}
	h.p.Off("b", "released")
}

func TestShutdownUnkeys(t *testing.T) {
	h := newPTTHarness(t, "rigctld")
	h.claim(t, "a")
	h.on(t, "a")
	h.p.Shutdown()
	if h.p.Active() || h.count("set_ptt 0") != 1 || h.br.TXAllowed("a") || h.srv.count() != 0 || h.in.Owed() {
		t.Fatalf("shutdown left PTT: %v %v", h.rig.log(), h.eventLog())
	}
	if !h.noticed("the station is stopping") {
		t.Fatalf("notices %v", h.noticeLog())
	}
	ixRefused(t, h.p.On(context.Background(), "a"), "the station is stopping", "")
	// vox: the loopback and the TX switch close too
	v := newPTTHarness(t, "vox")
	v.claim(t, "a")
	v.on(t, "a")
	v.p.Shutdown()
	if v.p.Active() || v.br.TXAllowed("a") || v.srv.count() != 0 {
		t.Fatalf("vox shutdown: %v", v.eventLog())
	}
}

// While PTT is on, the tune carrier and a take-over are refused; while a carrier is up PTT is.
func TestPTTExcludesTuneAndTakeOver(t *testing.T) {
	h := newPTTHarness(t, "rigctld")
	h.claim(t, "a")
	h.on(t, "a")
	ixRefused(t, h.in.Tune(context.Background(), "a", true), "PTT is on", "release PTT first")
	ixRefused(t, h.in.TakeOver(ixHolder("b")), "transmitting", "")
	h.p.Off("a", "released")
	if r := h.in.Tune(context.Background(), "a", true); !r.OK {
		t.Fatalf("tune: %+v", r)
	}
	ixRefused(t, h.p.On(context.Background(), "a"), "a tune carrier is up", "")
	h.in.Stop(context.Background())
	h.on(t, "a")
	h.p.Off("a", "released")
}

func TestPTTDropsWhenAudioIsLost(t *testing.T) {
	h := newPTTHarness(t, "rigctld")
	h.claim(t, "a")
	h.on(t, "a")
	h.br.lose("b", "no audio from the browser") // another tab's loss is not ours
	settle()
	if !h.p.Active() {
		t.Fatal("another tab's audio loss dropped PTT")
	}
	h.br.lose("a", "no audio from the browser")
	waitFor(t, "audio lost", func() bool { return h.noticed("audio lost") && !h.p.Active() })
	if h.count("set_ptt 0") != 1 {
		t.Fatalf("calls %v", h.rig.log())
	}
}

func TestPTTRefusedWhileTheLinkIsDown(t *testing.T) {
	h := newPTTHarness(t, "rigctld")
	h.claim(t, "a")
	h.rig.setUp(false)
	ixRefused(t, h.p.On(context.Background(), "a"), "rig control unreachable", "wait a few seconds")
	if len(h.rig.log()) != 0 || len(h.eventLog()) != 0 {
		t.Fatalf("sent: %v %v", h.rig.log(), h.eventLog())
	}
}

func TestPTTTimeoutText(t *testing.T) {
	for secs, want := range map[int]string{30: "0:30", 180: "3:00", 600: "10:00", 61: "1:01"} {
		if got := minSec(time.Duration(secs) * time.Second); got != want {
			t.Errorf("minSec(%d s) = %q, want %q", secs, got, want)
		}
	}
}

// A STOP that lands while PTT is still opening ends it: the window is undone once its key-on has
// answered, nothing is left keyed or open, and no second press can open a window meanwhile.
func TestPTTStopWhileStartingUndoesIt(t *testing.T) {
	h := newPTTHarness(t, "rigctld")
	h.claim(t, "a")
	var second Result
	armed := true
	h.rig.mu.Lock()
	h.rig.beforeDo = func(call string) {
		if call != "set_ptt 1" || !armed {
			return
		}
		armed = false
		if r := h.p.Stop(context.Background(), "iPhone"); !r.OK {
			t.Errorf("stop: %+v", r)
		}
		second = h.p.On(context.Background(), "a")
	}
	h.rig.mu.Unlock()
	r := h.p.On(context.Background(), "a")
	if r.OK || r.Reason != "PTT was stopped: STOP from iPhone" {
		t.Fatalf("on: %+v", r)
	}
	if second.OK || second.Reason != "PTT is already starting" {
		t.Fatalf("a second press while starting: %+v", second)
	}
	if h.p.Active() || h.rig.State().PTT || h.in.Owed() || h.br.TXAllowed("a") || h.srv.count() != 0 {
		t.Fatalf("left open: %v %v", h.rig.log(), h.eventLog())
	}
	if got := h.rig.log(); got[len(got)-1] != "set_ptt 0" {
		t.Fatalf("calls %v", got)
	}
	if !h.noticed("STOP from iPhone") {
		t.Fatalf("notices %v", h.noticeLog())
	}
	h.on(t, "a") // and the next press works
	h.p.Off("a", "released")
}

// A tab whose audio connection closes (the signalling socket ended, the page stopped its audio)
// ends its window at once; a heartbeat that finds no microphone ends it too.
func TestPTTDropsWhenTheAudioConnectionCloses(t *testing.T) {
	h := newPTTHarness(t, "rigctld")
	h.claim(t, "a")
	h.on(t, "a")
	h.br.close("a")
	waitFor(t, "audio lost", func() bool { return h.noticed("audio lost") && !h.p.Active() })
	if h.count("set_ptt 0") != 1 || h.br.TXAllowed("a") || h.srv.count() != 0 {
		t.Fatalf("left open: %v %v", h.rig.log(), h.eventLog())
	}
	// the microphone gone without any callback: the next heartbeat finds out
	h.br.mu.Lock()
	h.br.inbound["a"] = true
	h.br.mu.Unlock()
	h.on(t, "a")
	h.br.mu.Lock()
	h.br.inbound["a"] = false
	h.br.mu.Unlock()
	if r := h.p.Heartbeat("a"); r.OK || r.Reason != "audio lost" {
		t.Fatalf("heartbeat without a microphone: %+v", r)
	}
	if h.p.Active() || h.count("set_ptt 0") != 2 || h.br.TXAllowed("a") {
		t.Fatalf("still on: %v %v", h.rig.log(), h.eventLog())
	}
}

// The microphone going while the window opens: the window is undone, not opened on dead air.
func TestPTTDropsWhenTheMicGoesWhileOpening(t *testing.T) {
	h := newPTTHarness(t, "rigctld")
	h.claim(t, "a")
	h.rig.mu.Lock()
	h.rig.beforeDo = func(call string) {
		if call == "set_ptt 1" {
			h.br.close("a")
		}
	}
	h.rig.mu.Unlock()
	r := h.p.On(context.Background(), "a")
	if r.OK || r.Reason != "PTT was stopped: audio lost" {
		t.Fatalf("on: %+v", r)
	}
	if h.p.Active() || h.rig.State().PTT || h.in.Owed() || h.br.TXAllowed("a") || h.srv.count() != 0 {
		t.Fatalf("left open: %v %v", h.rig.log(), h.eventLog())
	}
}

// failTone is a keying tone player that cannot take a frame.
type failTone struct{ ptTone }

func (f *failTone) Write([]int16) error { return errors.New("pacat: broken pipe") }

func TestPTTToneFailureWhileOpening(t *testing.T) {
	h := newPTTHarness(t, "digirig_tone")
	ft := &failTone{}
	h.p.Tone = func() TonePlayer { return ft }
	h.claim(t, "a")
	r := h.p.On(context.Background(), "a")
	if r.OK || !strings.Contains(r.Reason, "the keying tone could not be played") {
		t.Fatalf("on: %+v", r)
	}
	ft.mu.Lock()
	closed := ft.closed
	ft.mu.Unlock()
	if h.p.Active() || h.br.TXAllowed("a") || h.srv.count() != 0 || !closed {
		t.Fatalf("left open: %v", h.eventLog())
	}
}

// Every window has its own number: On answers it, a repeated on answers it again, and the drop
// notice names it, so a page can tell a late notice about its last press from one about this.
func TestPTTWindowNumbers(t *testing.T) {
	h := newPTTHarness(t, "rigctld")
	h.claim(t, "a")
	first := h.p.On(context.Background(), "a")
	again := h.p.On(context.Background(), "a")
	if !first.OK || first.Window == 0 || again.Window != first.Window {
		t.Fatalf("on %+v, again %+v", first, again)
	}
	h.p.Off("a", "released")
	second := h.p.On(context.Background(), "a")
	if !second.OK || second.Window == first.Window {
		t.Fatalf("second window %+v after %+v", second, first)
	}
	h.p.Off("a", "released")
	n := h.noticeLog()
	want := func(i int, w uint64) string { b, _ := json.Marshal(w); return offNotice("released") + string(b) + "}" }
	if len(n) != 2 || n[0] != want(0, first.Window) || n[1] != want(1, second.Window) {
		t.Fatalf("notices %v (windows %d %d)", n, first.Window, second.Window)
	}
}

// A loopback that would not unload, or a load that failed after pactl may have loaded it, is
// cleared with every other TX loopback of the radio.
func TestPTTClearsLoopbacksAfterAFailedUnloadOrLoad(t *testing.T) {
	h := newPTTHarness(t, "vox")
	h.claim(t, "a")
	h.on(t, "a")
	h.srv.mu.Lock()
	h.srv.unloadErr = errors.New("pactl: timed out")
	h.srv.mu.Unlock()
	h.p.Off("a", "released")
	if got := strings.Join(h.eventLog(), ","); got != "clear radio1,load radio1,tx a on,tx a off,unload failed,clear radio1" || h.srv.count() != 0 {
		t.Fatalf("after a failed unload: %q", got)
	}
	h.srv.mu.Lock()
	h.srv.unloadErr, h.srv.loadErr = nil, errors.New("pactl: timed out")
	h.srv.mu.Unlock()
	if r := h.p.On(context.Background(), "a"); r.OK {
		t.Fatalf("on with a failing load: %+v", r)
	}
	if ev := h.eventLog(); ev[len(ev)-1] != "clear radio1" {
		t.Fatalf("a failed load was not cleared: %v", ev)
	}
}
