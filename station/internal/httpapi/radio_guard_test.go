package httpapi

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/rig"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/session"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/settings"
)

// swrRigs is a rig layer whose radio reports SWR over CAT.
type swrRigs struct{ w *rwRig }

func (r swrRigs) Writer(n string) (rig.Writer, bool) { return r.w, n == "radio1" }
func (r swrRigs) Caps(n string) (rig.Caps, bool) {
	c, ok := rwRigs{}.Caps(n)
	c.Modes = append(c.Modes, "FM")
	c.ReadableMeters = []string{"SWR"}
	return c, ok
}

type guardFixture struct {
	api   *RadioAPI
	h     func(method, path, body string, hdr map[string]string) string
	rig   *rwRig
	mu    sync.Mutex
	notes []string
}

func newGuardFixture(withSWR bool) *guardFixture {
	f := &guardFixture{rig: &rwRig{st: rig.State{Connected: true, Freq: 14074000, Mode: "FM", Levels: map[string]float64{}, Meters: map[string]float64{}, Optimistic: map[string]bool{}}}}
	var rigs Rigs = rwRigs{f.rig}
	if withSWR {
		rigs = swrRigs{f.rig}
	}
	f.api = &RadioAPI{Auth: multiAuth{}, Radios: rwRecords{"radio1": {Name: "radio1", Port: 4532, PTT: "rigctld"}}, Rigs: rigs,
		Owners: session.NewOwners(realNow{}), Ctx: context.Background(),
		Settings: func(string) settings.RadioSettings { return settings.Defaults() },
		Notify: func(radio string, msg any) {
			b, _ := json.Marshal(msg)
			f.mu.Lock()
			f.notes = append(f.notes, string(b))
			f.mu.Unlock()
		}}
	srv := New(Config{Listen: "127.0.0.1:0", Auth: multiAuth{}, Routes: f.api.Register}).Handler
	f.h = func(method, path, body string, hdr map[string]string) string {
		return strings.TrimSpace(rwDo(srv, method, path, body, hdr).Body.String())
	}
	return f
}

func (f *guardFixture) noteLog() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]string{}, f.notes...)
}

func (f *guardFixture) guardView(t *testing.T, hdr map[string]string) guardView {
	t.Helper()
	var g struct {
		Guard guardView `json:"guard"`
	}
	if err := json.Unmarshal([]byte(f.h("GET", "/api/radio/radio1", "", hdr)), &g); err != nil {
		t.Fatal(err)
	}
	return g.Guard
}

const routeReason = "SWR guard: 5.8:1 against 3.0:1; tune the antenna, change band, or override"

func TestSWRGuardOverrideRouteIsOwnerOnly(t *testing.T) {
	f := newGuardFixture(true)
	f.h("POST", "/api/radio/radio1/claim", `{}`, pttOwner)
	hooks := f.api.PollerHooks("radio1")
	hooks.Freq(14074000)
	for i := 0; i < 3; i++ {
		hooks.SWR(5.8, true)
	}
	if g := f.guardView(t, pttOwner); !g.Active || !g.Latched || g.Reason != routeReason {
		t.Fatalf("guard after the trip: %+v", g)
	}
	// a transmit intent is refused with the guard's words
	want := `{"ok":false,"reason":"` + routeReason + `","fix":"tune the antenna, change band, or override"}`
	if got := f.h("POST", "/api/radio/radio1/tune", `{"on":true}`, pttOwner); got != want {
		t.Fatalf("tune: %s", got)
	}
	// nobody but the owner overrides
	if got := f.h("POST", "/api/radio/radio1/override", `{}`, pttOther); strings.Contains(got, `"ok":true`) {
		t.Fatalf("a non-owner overrode: %s", got)
	}
	if got := f.h("POST", "/api/radio/radio1/override", `{}`, map[string]string{"X-Test-Session": "s1"}); strings.Contains(got, `"ok":true`) {
		t.Fatalf("no tab token: %s", got)
	}
	if !f.guardView(t, pttOwner).Latched {
		t.Fatal("a refused override cleared the latch")
	}
	if got := f.h("POST", "/api/radio/radio1/override", `{}`, pttOwner); got != `{"ok":true}` {
		t.Fatalf("owner override: %s", got)
	}
	if g := f.guardView(t, pttOwner); !g.Active || g.Latched || g.Reason != "" {
		t.Fatalf("guard after the override: %+v", g)
	}
	if got := f.h("POST", "/api/radio/radio1/tune", `{"on":true}`, pttOwner); got != `{"ok":true}` {
		t.Fatalf("tune after the override: %s", got)
	}
	notes := f.noteLog()
	if len(notes) != 2 || notes[0] != `{"type":"guard","radio":"radio1","latched":true,"reason":"`+routeReason+`"}` ||
		notes[1] != `{"type":"guard","radio":"radio1","latched":false,"reason":"override by iPad"}` {
		t.Fatalf("notices %v", notes)
	}
	// the trip unkeyed the radio
	deadline := time.Now().Add(3 * time.Second)
	for {
		f.rig.mu.Lock()
		got := strings.Join(f.rig.calls, ",")
		f.rig.mu.Unlock()
		if strings.Contains(got, "set_ptt 0") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("no unkey on the trip: %s", got)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestGuardViewOfARadioWithoutSWR(t *testing.T) {
	f := newGuardFixture(false)
	hooks := f.api.PollerHooks("radio1")
	for i := 0; i < 5; i++ {
		hooks.SWR(9, true)
	}
	if g := f.guardView(t, pttOwner); g.Active || g.Latched {
		t.Fatalf("guard: %+v", g)
	}
	f.h("POST", "/api/radio/radio1/claim", `{}`, pttOwner)
	if got := f.h("POST", "/api/radio/radio1/override", `{}`, pttOwner); strings.Contains(got, `"ok":true`) {
		t.Fatalf("override with no guard: %s", got)
	}
	if n := f.noteLog(); len(n) != 0 {
		t.Fatalf("notices %v", n)
	}
}

// The Radios page's settings section learns from the settings answer whether the radio has a
// guard at all.
func TestRadioSettingsSaysWhetherThereIsAnSWRGuard(t *testing.T) {
	for _, withSWR := range []bool{true, false} {
		f := newGuardFixture(withSWR)
		st := &RadioSettingsAPI{Radios: rwRecords{"radio1": {Name: "radio1", Port: 4532}}, Settings: &settings.Store{Dir: t.TempDir()},
			HasSWRGuard: func(n string) bool { g := f.api.GuardOf(n); a, _, _ := g.View(); return a }}
		srv := New(Config{Listen: "127.0.0.1:0", Auth: multiAuth{}, Routes: st.Register}).Handler
		var got struct {
			Guard *bool `json:"swr_guard"`
		}
		rec := rwDo(srv, "GET", "/api/radio/radio1/settings", "", pttOwner)
		if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || got.Guard == nil || *got.Guard != withSWR {
			t.Fatalf("with SWR %v: %s", withSWR, rec.Body.String())
		}
	}
}

// tunerRigs is swrRigs with an antenna tuner that CAT controls.
type tunerRigs struct{ swrRigs }

func (r tunerRigs) Caps(n string) (rig.Caps, bool) {
	c, ok := r.swrRigs.Caps(n)
	c.SettableFuncs = append(c.SettableFuncs, "TUNER")
	return c, ok
}

// Ruling 13 through the real poller hooks: inside an ATU window the reads taken on the tuner's own
// carrier are ignored, but three over the limit with the station itself transmitting (PTT, Tune)
// still trip the guard.
func TestPollerHooksGuardTheStationsKeyingInsideAnATUWindow(t *testing.T) {
	f := newGuardFixture(true)
	f.api.Rigs = tunerRigs{swrRigs{f.rig}}
	f.h("POST", "/api/radio/radio1/claim", `{}`, pttOwner)
	hooks := f.api.PollerHooks("radio1")
	hooks.Freq(14074000)
	if got := f.h("POST", "/api/radio/radio1/atu", `{}`, pttOwner); got != `{"ok":true}` {
		t.Fatalf("atu: %s", got)
	}
	for i := 0; i < 3; i++ {
		hooks.SWRTx(5.8, true, false) // the tuner's carrier
	}
	if g := f.guardView(t, pttOwner); g.Latched {
		t.Fatalf("the tuner's own carrier tripped the guard: %+v", g)
	}
	for i := 0; i < 3; i++ {
		hooks.SWRTx(5.8, true, true) // the station keying inside the window
	}
	if g := f.guardView(t, pttOwner); !g.Active || !g.Latched {
		t.Fatalf("the station's keying inside the ATU window did not trip the guard: %+v", g)
	}
}
