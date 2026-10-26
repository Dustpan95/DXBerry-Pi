package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/rig"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/settings"
)

// Prober is what the re-probe route needs from the rig layer.
type Prober interface {
	// State says whether the manager runs the radio at all, and whether rigctld answers.
	State(name string) (present, connected bool)
	Discover(ctx context.Context, name string) (rig.Caps, error)
	Store(model string, c rig.Caps) error
	Use(name string, c rig.Caps)
}

type managerProbe struct {
	m     *rig.Manager
	cache rig.CapsCache
}

// ManagerProbe adapts a rig.Manager and the caps cache to Prober.
func ManagerProbe(m *rig.Manager, cache rig.CapsCache) Prober { return managerProbe{m, cache} }

func (p managerProbe) State(name string) (bool, bool) {
	pl, ok := p.m.Radio(name)
	if !ok {
		return false, false
	}
	return true, pl.State().Connected
}

func (p managerProbe) Discover(ctx context.Context, name string) (rig.Caps, error) {
	c, ok := p.m.Client(name)
	if !ok {
		return rig.Caps{}, errors.New("no rig connection")
	}
	return rig.Discover(ctx, c)
}

func (p managerProbe) Store(model string, c rig.Caps) error { return p.cache.Store(model, c) }

func (p managerProbe) Use(name string, c rig.Caps) { p.m.SetCaps(name, c) }

// RadioSettingsAPI serves /api/radio/{name}/settings and /api/radio/{name}/reprobe.
type RadioSettingsAPI struct {
	Radios   RadioRecords
	Settings *settings.Store
	Probe    Prober
	// HasSWRGuard says whether a radio has an SWR guard: it reports SWR over CAT (nil: not said).
	HasSWRGuard func(name string) bool
}

// Register adds the routes; call it from Config.Routes so the gate applies.
func (a *RadioSettingsAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/radio/{name}/settings", a.get)
	mux.HandleFunc("POST /api/radio/{name}/settings", a.set)
	mux.HandleFunc("POST /api/radio/{name}/reprobe", a.reprobe)
}

type settingsView struct {
	SWRLimit    float64    `json:"swr_limit"`
	PTTTimeoutS int        `json:"ptt_timeout_s"`
	Probed      *time.Time `json:"probed"`
	SWRGuard    *bool      `json:"swr_guard,omitempty"` // false: this radio has no SWR guard
}

type settingsBody struct {
	SWRLimit    *float64 `json:"swr_limit"`
	PTTTimeoutS *int     `json:"ptt_timeout_s"`
}

// known answers 404 itself for a name that is not a radio record; nothing touches the disk before.
func (a *RadioSettingsAPI) known(w http.ResponseWriter, r *http.Request) (string, bool) {
	name := r.PathValue("name")
	if !reRadioName.MatchString(name) {
		writeError(w, http.StatusNotFound, "no such radio")
		return "", false
	}
	if _, ok := a.Radios.Get(name); !ok {
		writeError(w, http.StatusNotFound, "no such radio")
		return "", false
	}
	return name, true
}

func (a *RadioSettingsAPI) get(w http.ResponseWriter, r *http.Request) {
	name, ok := a.known(w, r)
	if !ok {
		return
	}
	s := a.Settings.Get(name)
	v := settingsView{SWRLimit: s.SWRLimit, PTTTimeoutS: s.PTTTimeoutS}
	if a.HasSWRGuard != nil {
		has := a.HasSWRGuard(name)
		v.SWRGuard = &has
	}
	if !s.Probed.IsZero() {
		v.Probed = &s.Probed
	}
	writeJSON(w, http.StatusOK, v)
}

func (a *RadioSettingsAPI) set(w http.ResponseWriter, r *http.Request) {
	name, ok := a.known(w, r)
	if !ok {
		return
	}
	var b settingsBody
	dec := json.NewDecoder(http.MaxBytesReader(w, r.Body, 4<<10))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil {
		var te *json.UnmarshalTypeError
		if errors.As(err, &te) && te.Field == "ptt_timeout_s" {
			writeJSON(w, http.StatusBadRequest, rig.Result{Reason: "the PTT timeout must be a whole number of seconds", Fix: "enter a whole number between 30 and 600"})
			return
		}
		writeJSON(w, http.StatusBadRequest, badBody())
		return
	}
	if b.SWRLimit == nil || b.PTTTimeoutS == nil {
		writeJSON(w, http.StatusBadRequest, badBody())
		return
	}
	if err := (settings.RadioSettings{SWRLimit: *b.SWRLimit, PTTTimeoutS: *b.PTTTimeoutS}).Validate(); err != nil {
		writeJSON(w, http.StatusOK, rig.Result{Reason: err.Error(), Fix: "enter a value in that range"})
		return
	}
	if err := a.Settings.Modify(name, func(s *settings.RadioSettings) { s.SWRLimit, s.PTTTimeoutS = *b.SWRLimit, *b.PTTTimeoutS }); err != nil {
		log.Printf("radio settings: %s: %v", name, err)
		writeJSON(w, http.StatusOK, rig.Result{Reason: "the settings could not be saved: " + err.Error(), Fix: "check the station state directory"})
		return
	}
	writeJSON(w, http.StatusOK, rig.Result{OK: true})
}

func (a *RadioSettingsAPI) reprobe(w http.ResponseWriter, r *http.Request) {
	name, ok := a.known(w, r)
	if !ok {
		return
	}
	rec, _ := a.Radios.Get(name)
	present, connected := a.Probe.State(name)
	if !present {
		writeJSON(w, http.StatusConflict, rig.Result{Reason: "this radio has no rig control", Fix: "give the radio a rig control port in its settings"})
		return
	}
	if !connected {
		writeJSON(w, http.StatusOK, rig.Result{Reason: "rig control unreachable; the radio's rigctld is restarting", Fix: "wait a few seconds"})
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 10*time.Second)
	defer cancel()
	caps, err := a.Probe.Discover(ctx, name)
	if err != nil {
		log.Printf("radio settings: re-probe %s: %v", name, err)
		writeJSON(w, http.StatusOK, rig.Result{Reason: "the radio's capabilities could not be read", Fix: "try again in a few seconds"})
		return
	}
	if err := a.Probe.Store(rig.CapsKey(rec), caps); err != nil {
		log.Printf("radio settings: caching caps for %s: %v", name, err)
	}
	a.Probe.Use(name, caps)
	if err := a.Settings.SetProbed(name, time.Now().UTC()); err != nil {
		log.Printf("radio settings: %s: recording the probe time: %v", name, err)
	}
	writeJSON(w, http.StatusOK, rig.Result{OK: true})
}
