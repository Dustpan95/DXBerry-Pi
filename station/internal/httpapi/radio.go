package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/rig"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/session"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/settings"
)

// reRadioName is the radio name pattern; a name that does not match is never looked up.
var reRadioName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// TabHeader carries a tab's token on every radio request. The same token opens the events
// socket (?tab=) and is the body's device on claim and takeover.
const TabHeader = "X-DXB-Tab"

const maxTab = 64

// RadioRecords is the radio records as the API needs them (*radios.Watcher).
type RadioRecords interface {
	Get(name string) (radios.Radio, bool)
}

// Rigs is the rig layer as the API needs it.
type Rigs interface {
	Writer(name string) (rig.Writer, bool)
	Caps(name string) (rig.Caps, bool)
}

type managerRigs struct{ m *rig.Manager }

// ManagerRigs adapts a rig.Manager to Rigs.
func ManagerRigs(m *rig.Manager) Rigs { return managerRigs{m} }

func (r managerRigs) Writer(name string) (rig.Writer, bool) {
	p, ok := r.m.Radio(name)
	if !ok {
		return nil, false
	}
	return rig.PollerWriter(p), true
}

func (r managerRigs) Caps(name string) (rig.Caps, bool) { return r.m.Caps(name) }

// RadioAPI serves /api/radio/{name}/... .
type RadioAPI struct {
	Auth   AuthChecker
	Radios RadioRecords
	Rigs   Rigs
	Owners *session.Owners
	Clock  rig.Clock // nil: real time
	// Ctx ends the owed-unkey retry loops with the service (nil: they end only when the radio leaves the records).
	Ctx context.Context
	// Marks are the keyed markers (nil: none). OweMarkedCarriers reads them at start.
	Marks *rig.KeyedMarks
	// Audio is a radio's audio side for PTT (nil: PTT is refused, the station has no browser audio).
	Audio func(radio string) PTTAudio
	// Available says whether a radio's sound server can carry browser audio, and why not (the GET
	// answer's audio field; nil: no browser audio).
	Available func(radio string) (bool, string)
	// Settings is a radio's station settings, for its PTT timeout (nil: the defaults).
	Settings func(radio string) settings.RadioSettings
	// Notify sends a notice to every tab (nil: none are sent).
	Notify rig.Notifier

	mu      sync.Mutex
	intents map[string]*rig.Intents
	ptts    map[string]*rig.PTT
	avail   map[string]availAnswer
}

// PTTAudio is one radio's audio side as PTT needs it: its WebRTC bridge, the sound server
// control and the keying tone's player (digirig_tone).
type PTTAudio struct {
	Bridge rig.TXBridge
	Server rig.SoundServer
	Tone   func() rig.TonePlayer
}

// availAnswer is a cached Available answer: the page asks every few seconds and each answer
// runs the sound server's status and pactl.
type availAnswer struct {
	ok  bool
	why string
	at  time.Time
}

const availFor = 3 * time.Second

const noBrowserAudio = "this station has no browser audio"

// UnkeyWithin bounds the unkeys sent as the service stops.
const UnkeyWithin = 3 * time.Second

// Shutdowner is the HTTP server as StopService needs it (*http.Server).
type Shutdowner interface {
	Shutdown(ctx context.Context) error
}

// StopService is the service's stop: the HTTP server stops taking requests first (so no new
// intent can key anything; it gets up to within), then every carrier the station keyed or
// still owes an unkey is unkeyed, within UnkeyWithin. A keyed transmitter left with nobody
// holding it is the worst state the station can leave.
func StopService(srv Shutdowner, within time.Duration, a *RadioAPI) {
	ctx, cancel := context.WithTimeout(context.Background(), within)
	_ = srv.Shutdown(ctx)
	cancel()
	ctx, cancel = context.WithTimeout(context.Background(), UnkeyWithin)
	defer cancel()
	a.UnkeyAll(ctx)
}

// UnkeyAll sends one set_ptt 0, all radios at once, to every radio whose carrier may be up, and
// returns when they have answered or ctx ends.
// Every PTT window is closed first (its loopback unloaded, its tab's microphone stopped), and no
// new one opens.
func (a *RadioAPI) UnkeyAll(ctx context.Context) {
	a.mu.Lock()
	all := make([]*rig.Intents, 0, len(a.intents))
	for _, in := range a.intents {
		all = append(all, in)
	}
	ptts := make([]*rig.PTT, 0, len(a.ptts))
	for _, p := range a.ptts {
		ptts = append(ptts, p)
	}
	a.mu.Unlock()
	var pw sync.WaitGroup
	for _, p := range ptts {
		pw.Add(1)
		go func() { defer pw.Done(); p.Shutdown() }()
	}
	pdone := make(chan struct{})
	go func() { pw.Wait(); close(pdone) }()
	select {
	case <-pdone:
	case <-ctx.Done():
	}
	var wg sync.WaitGroup
	for _, in := range all {
		if !in.Owed() {
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			in.UnkeyNow(ctx)
		}()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-ctx.Done():
	}
}

// OweMarkedCarriers owes an unkey to every radio a keyed marker names (call it once at start,
// with the rig layer running). A marker for a radio no longer in the records is dropped.
func (a *RadioAPI) OweMarkedCarriers() {
	if a.Marks == nil {
		return
	}
	for _, name := range a.Marks.List() {
		rec, ok := a.Radios.Get(name)
		if !ok {
			slog.Info("dropping the keyed marker of a radio that is gone", "radio", name)
			if err := a.Marks.Clear(name); err != nil {
				slog.Warn("could not remove the keyed marker", "radio", name, "error", err)
			}
			continue
		}
		a.intentsFor(rec).Owe()
	}
}

// dynWriter resolves the radio's poller on every call, so a reconnected or replaced poller is
// followed without rebuilding the intents (and losing a carrier's pending unkey).
type dynWriter struct {
	a    *RadioAPI
	name string
}

func (d dynWriter) State() rig.State {
	if w, ok := d.a.Rigs.Writer(d.name); ok {
		return w.State()
	}
	return rig.State{}
}

func (d dynWriter) Write(ctx context.Context, fn func(rig.Commander) error, opt func(*rig.State)) error {
	if w, ok := d.a.Rigs.Writer(d.name); ok {
		return w.Write(ctx, fn, opt)
	}
	return rig.ErrNotConnected
}

// SetUrgent passes an owed unkey's urgency to the radio's current link.
func (d dynWriter) SetUrgent(on bool) {
	if w, ok := d.a.Rigs.Writer(d.name); ok {
		if u, ok := w.(rig.Urgent); ok {
			u.SetUrgent(on)
		}
	}
}

// SetTransmitting passes the station's own keying to the radio's current poller.
func (d dynWriter) SetTransmitting(on bool) {
	if w, ok := d.a.Rigs.Writer(d.name); ok {
		if t, ok := w.(rig.Transmitter); ok {
			t.SetTransmitting(on)
		}
	}
}

// Register adds the radio routes, one pattern per intent. Call it from Config.Routes, so
// the session gate and the same-origin rule apply.
func (a *RadioAPI) Register(mux *http.ServeMux) {
	// a tab's claim ending (release, socket close, idle) ends its carrier
	a.Owners.OnDrop(func(radio string) {
		if rec, ok := a.Radios.Get(radio); ok {
			a.pttFor(rec).OwnerGone()
			a.intentsFor(rec).Unkey()
		}
	})
	mux.HandleFunc("GET /api/radio/{name}", a.get)
	post := func(intent string, fn func(r *http.Request, in *rig.Intents, b *radioBody, sess *Session) rig.Result) {
		mux.HandleFunc("POST /api/radio/{name}/"+intent, a.post(fn))
	}
	post("freq", func(r *http.Request, in *rig.Intents, b *radioBody, _ *Session) rig.Result {
		if b.HZ == nil {
			return badBody()
		}
		return in.Freq(r.Context(), *b.HZ)
	})
	post("mode", func(r *http.Request, in *rig.Intents, b *radioBody, _ *Session) rig.Result {
		if b.Mode == nil {
			return badBody()
		}
		pb := 0
		if b.Passband != nil {
			pb = *b.Passband
		}
		return in.Mode(r.Context(), *b.Mode, pb)
	})
	post("vfo", func(r *http.Request, in *rig.Intents, b *radioBody, _ *Session) rig.Result {
		if b.VFO == nil {
			return badBody()
		}
		return in.VFO(r.Context(), *b.VFO)
	})
	post("split", func(r *http.Request, in *rig.Intents, b *radioBody, _ *Session) rig.Result {
		if b.On == nil {
			return badBody()
		}
		return in.Split(r.Context(), *b.On)
	})
	post("level", func(r *http.Request, in *rig.Intents, b *radioBody, _ *Session) rig.Result {
		if b.Name == nil || b.Value == nil {
			return badBody()
		}
		return in.Level(r.Context(), *b.Name, *b.Value)
	})
	post("tune", func(r *http.Request, in *rig.Intents, b *radioBody, s *Session) rig.Result {
		tab, ok := tabToken(r, b)
		if !ok || b.On == nil {
			return badBody()
		}
		return in.Tune(r.Context(), session.TabID(s.ID, tab), *b.On)
	})
	post("atu", func(r *http.Request, in *rig.Intents, b *radioBody, s *Session) rig.Result {
		tab, ok := tabToken(r, b)
		if !ok {
			return badBody()
		}
		return in.ATU(r.Context(), session.TabID(s.ID, tab))
	})
	post("stop", func(r *http.Request, in *rig.Intents, b *radioBody, _ *Session) rig.Result {
		device := "a browser"
		if tab, ok := tabToken(r, b); ok {
			device = session.TabLabel(tab)
		}
		return a.pttOf(in.Radio).Stop(r.Context(), device)
	})
	post("ptt", func(r *http.Request, in *rig.Intents, b *radioBody, s *Session) rig.Result {
		tab, ok := tabToken(r, b)
		if !ok {
			return badBody()
		}
		id, p := session.TabID(s.ID, tab), a.pttOf(in.Radio)
		switch {
		case b.HB != nil && *b.HB:
			return p.Heartbeat(id)
		case b.On == nil:
			return badBody()
		case *b.On:
			return p.On(r.Context(), id)
		}
		return p.Off(id, "released")
	})
	post("override", func(r *http.Request, in *rig.Intents, b *radioBody, s *Session) rig.Result {
		tab, ok := tabToken(r, b)
		if !ok {
			return badBody()
		}
		return in.Override(session.TabID(s.ID, tab))
	})
	post("claim", func(r *http.Request, in *rig.Intents, b *radioBody, s *Session) rig.Result {
		tab, ok := tabToken(r, b)
		if !ok {
			return badBody()
		}
		return in.Claim(session.Holder{Kind: "tab", ID: session.TabID(s.ID, tab), Label: session.TabLabel(tab)})
	})
	post("release", func(r *http.Request, in *rig.Intents, b *radioBody, s *Session) rig.Result {
		tab, ok := tabToken(r, b)
		if !ok {
			return badBody()
		}
		id := session.TabID(s.ID, tab)
		if p := a.pttOf(in.Radio); p.Active() {
			p.Off(id, "the transmitter was released")
		}
		return in.Release(id)
	})
	post("takeover", func(r *http.Request, in *rig.Intents, b *radioBody, s *Session) rig.Result {
		tab, ok := tabToken(r, b)
		if !ok {
			return badBody()
		}
		return in.TakeOver(session.Holder{Kind: "tab", ID: session.TabID(s.ID, tab), Label: session.TabLabel(tab)})
	})
}

// radioBody is every intent's JSON body; a field a route needs is a pointer so a missing one is
// told from a zero.
type radioBody struct {
	HZ       *int64   `json:"hz"`
	Mode     *string  `json:"mode"`
	Passband *int     `json:"passband"`
	VFO      *string  `json:"vfo"`
	On       *bool    `json:"on"`
	HB       *bool    `json:"hb"`
	Name     *string  `json:"name"`
	Value    *float64 `json:"value"`
	Device   string   `json:"device"`
}

func badBody() rig.Result {
	return rig.Result{Reason: "the request was not understood", Fix: "reload the page"}
}

// ValidTab reports whether s can be a tab token: 1 to 64 printable ASCII characters.
func ValidTab(s string) bool {
	if s == "" || len(s) > maxTab {
		return false
	}
	for _, c := range s {
		if c < ' ' || c > '~' {
			return false
		}
	}
	return true
}

// tabToken is the tab's token: the header on any request, else the body's device.
func tabToken(r *http.Request, b *radioBody) (string, bool) {
	t := r.Header.Get(TabHeader)
	if t == "" && b != nil {
		t = b.Device
	}
	return t, ValidTab(t)
}

// lookup finds the radio or answers 404 itself.
func (a *RadioAPI) lookup(w http.ResponseWriter, r *http.Request) (radios.Radio, bool) {
	name := r.PathValue("name")
	if !reRadioName.MatchString(name) {
		writeError(w, http.StatusNotFound, "no such radio")
		return radios.Radio{}, false
	}
	rec, ok := a.Radios.Get(name)
	if !ok {
		a.drop(name)
		writeError(w, http.StatusNotFound, "no such radio")
		return radios.Radio{}, false
	}
	return rec, true
}

// drop forgets a radio's Intents (the radio left the records) and ends their retry loop.
func (a *RadioAPI) drop(name string) {
	a.mu.Lock()
	in, p := a.intents[name], a.ptts[name]
	delete(a.intents, name)
	delete(a.ptts, name)
	a.mu.Unlock()
	if p != nil {
		p.Close()
	}
	if in != nil {
		in.Close()
	}
}

func (a *RadioAPI) intentsFor(rec radios.Radio) *rig.Intents {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.intentsLocked(rec)
}

// pttFor is the radio's PTT (made with its Intents on first use); it follows the record's PTT method.
func (a *RadioAPI) pttFor(rec radios.Radio) *rig.PTT {
	a.mu.Lock()
	defer a.mu.Unlock()
	if p, ok := a.ptts[rec.Name]; ok {
		p.SetMethod(rec.PTT)
		return p
	}
	in := a.intentsLocked(rec)
	var au PTTAudio
	if a.Audio != nil {
		au = a.Audio(rec.Name)
	}
	name := rec.Name
	var conf func() settings.RadioSettings
	if a.Settings != nil {
		conf = func() settings.RadioSettings { return a.Settings(name) }
	}
	p := rig.NewPTT(in, au.Bridge, au.Server, conf, rec.PTT, a.Notify, a.Clock, nil)
	p.Tone = au.Tone
	if a.ptts == nil {
		a.ptts = map[string]*rig.PTT{}
	}
	a.ptts[name] = p
	return p
}

// pttOf is pttFor by name, for a radio the request has already found in the records.
func (a *RadioAPI) pttOf(name string) *rig.PTT {
	rec, _ := a.Radios.Get(name)
	rec.Name = name
	return a.pttFor(rec)
}

// PTTOn reports whether a PTT window is open on radio (the owner lock's "transmitting").
func (a *RadioAPI) PTTOn(radio string) bool {
	a.mu.Lock()
	p := a.ptts[radio]
	a.mu.Unlock()
	return p != nil && p.Active()
}

// available is Available, answered from a cache for availFor.
func (a *RadioAPI) available(radio string) (bool, string) {
	if a.Available == nil {
		return false, noBrowserAudio
	}
	now := time.Now()
	a.mu.Lock()
	c, ok := a.avail[radio]
	a.mu.Unlock()
	if ok && now.Sub(c.at) < availFor {
		return c.ok, c.why
	}
	got, why := a.Available(radio)
	a.mu.Lock()
	if a.avail == nil {
		a.avail = map[string]availAnswer{}
	}
	a.avail[radio] = availAnswer{ok: got, why: why, at: now}
	a.mu.Unlock()
	return got, why
}

func (a *RadioAPI) intentsLocked(rec radios.Radio) *rig.Intents {
	if a.intents == nil {
		a.intents = map[string]*rig.Intents{}
	}
	if in, ok := a.intents[rec.Name]; ok {
		return in
	}
	name := rec.Name
	in := &rig.Intents{
		Radio:  name,
		Caps:   func() (rig.Caps, bool) { return a.Rigs.Caps(name) },
		Poller: dynWriter{a, name},
		Owners: a.Owners,
		Clock:  a.Clock,
		Ctx:    a.Ctx,
		Marks:  a.Marks,
		Gone: func() bool {
			_, ok := a.Radios.Get(name)
			return !ok
		},
	}
	a.intents[name] = in
	limit := func() float64 {
		if a.Settings != nil {
			if v := a.Settings(name).SWRLimit; v > 0 {
				return v
			}
		}
		return settings.DefaultSWRLimit
	}
	notify := func(radio string, msg any) {
		if a.Notify != nil {
			a.Notify(radio, msg)
		}
	}
	rig.NewGuard(in, limit, notify).Drop = func(reason string) { a.pttOf(name).Drop(reason) }
	return in
}

// GuardOf is the radio's SWR guard (nil for a radio the records lack).
func (a *RadioAPI) GuardOf(name string) *rig.Guard {
	rec, ok := a.Radios.Get(name)
	if !ok {
		return nil
	}
	return a.intentsFor(rec).Guard()
}

// PollerHooks are the callbacks for radio's poller (rig.Manager.OnStart installs them): its SWR
// reads and frequency go to the radio's guard, found by name on every call so a replaced poller
// or a rebuilt Intents is followed.
func (a *RadioAPI) PollerHooks(name string) rig.Hooks {
	return rig.Hooks{
		SWR: func(swr float64, on bool) {
			if g := a.GuardOf(name); g != nil {
				g.Read(swr, on)
			}
		},
		// the poller's station-transmit flag (a PTT window or Tune carrier of ours) must reach
		// the guard: it keeps the ATU window from blinding the guard to our own keying
		SWRTx: func(swr float64, on, stationTx bool) {
			if g := a.GuardOf(name); g != nil {
				g.ReadTx(swr, on, stationTx)
			}
		},
		Freq: func(hz int64) {
			if g := a.GuardOf(name); g != nil {
				g.NoteFreq(hz)
			}
		},
	}
}

func (a *RadioAPI) post(fn func(r *http.Request, in *rig.Intents, b *radioBody, sess *Session) rig.Result) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		rec, ok := a.lookup(w, r)
		if !ok {
			return
		}
		sess, ok := a.Auth.Session(r)
		if !ok {
			writeError(w, http.StatusUnauthorized, "login required")
			return
		}
		var b radioBody
		dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
		if err := dec.Decode(&b); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, http.StatusBadRequest, badBody())
			return
		}
		if _, ok := a.Rigs.Writer(rec.Name); !ok {
			writeJSON(w, http.StatusConflict, rig.Result{Reason: "this radio has no rig control", Fix: "give the radio a rig control port in its settings"})
			return
		}
		writeJSON(w, http.StatusOK, fn(r, a.intentsFor(rec), &b, sess))
	}
}

// audioView is whether the radio's sound server can carry browser audio, and why not.
type audioView struct {
	Available bool   `json:"available"`
	Reason    string `json:"reason"`
}

// pttView is the radio's PTT window as the page shows it.
type pttView struct {
	On       bool   `json:"on"`
	Mine     bool   `json:"mine"`
	TimeoutS int    `json:"timeout_s"`
	Method   string `json:"method"`
}

// guardView is the SWR guard as the page shows it: whether the radio has one (it reports SWR
// over CAT), whether it is latched, and the words for the banner.
type guardView struct {
	Active  bool   `json:"active"`
	Latched bool   `json:"latched"`
	Reason  string `json:"reason"`
}

type ownerView struct {
	Kind  string `json:"kind"`
	Label string `json:"label"`
	Mine  bool   `json:"mine"`
}

func (a *RadioAPI) get(w http.ResponseWriter, r *http.Request) {
	rec, ok := a.lookup(w, r)
	if !ok {
		return
	}
	sess, ok := a.Auth.Session(r)
	if !ok {
		writeError(w, http.StatusUnauthorized, "login required")
		return
	}
	out := struct {
		Radio radios.Radio `json:"radio"`
		State *rig.State   `json:"state"`
		Caps  *rig.Caps    `json:"caps"`
		Owner *ownerView   `json:"owner"`
		Audio audioView    `json:"audio"`
		PTT   pttView      `json:"ptt"`
		Guard guardView    `json:"guard"`
	}{Radio: rec}
	if g := a.GuardOf(rec.Name); g != nil {
		out.Guard.Active, out.Guard.Latched, out.Guard.Reason = g.View()
	}
	out.Audio.Available, out.Audio.Reason = a.available(rec.Name)
	on, pttTab, timeout, method := a.pttFor(rec).State()
	out.PTT = pttView{On: on, TimeoutS: int(timeout / time.Second), Method: method}
	if tab, ok := tabToken(r, nil); ok && on && pttTab == session.TabID(sess.ID, tab) {
		out.PTT.Mine = true
	}
	if rw, ok := a.Rigs.Writer(rec.Name); ok {
		st := rw.State()
		st.Caps = nil // the capabilities travel beside the state
		out.State = &st
	}
	if c, ok := a.Rigs.Caps(rec.Name); ok {
		out.Caps = &c
	}
	if h, ok := a.Owners.Holder(rec.Name); ok {
		v := &ownerView{Kind: h.Kind, Label: h.Label}
		if tab, ok := tabToken(r, nil); ok && h.Kind == "tab" && h.ID == session.TabID(sess.ID, tab) {
			v.Mine = true
		}
		out.Owner = v
	}
	writeJSON(w, http.StatusOK, out)
}
