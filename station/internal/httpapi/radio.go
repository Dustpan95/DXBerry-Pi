package httpapi

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sync"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/rig"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/session"
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

	mu      sync.Mutex
	intents map[string]*intentSet
}

type intentSet struct {
	w  rig.Writer
	in *rig.Intents
}

// Register adds the radio routes, one pattern per intent. Call it from Config.Routes, so
// the session gate and the same-origin rule apply.
func (a *RadioAPI) Register(mux *http.ServeMux) {
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
		return in.Stop(r.Context())
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
		return in.Release(session.TabID(s.ID, tab))
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
		writeError(w, http.StatusNotFound, "no such radio")
		return radios.Radio{}, false
	}
	return rec, true
}

func (a *RadioAPI) intentsFor(rec radios.Radio, w rig.Writer) *rig.Intents {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.intents == nil {
		a.intents = map[string]*intentSet{}
	}
	if s, ok := a.intents[rec.Name]; ok && s.w == w {
		return s.in
	}
	name := rec.Name
	in := &rig.Intents{
		Radio:  name,
		Caps:   func() (rig.Caps, bool) { return a.Rigs.Caps(name) },
		Poller: w,
		Owners: a.Owners,
		Clock:  a.Clock,
	}
	a.intents[name] = &intentSet{w: w, in: in}
	return in
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
		rw, ok := a.Rigs.Writer(rec.Name)
		if !ok {
			writeJSON(w, http.StatusConflict, rig.Result{Reason: "this radio has no rig control", Fix: "give the radio a rig control port in its settings"})
			return
		}
		writeJSON(w, http.StatusOK, fn(r, a.intentsFor(rec, rw), &b, sess))
	}
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
	}{Radio: rec}
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
