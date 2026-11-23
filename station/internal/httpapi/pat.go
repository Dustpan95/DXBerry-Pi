package httpapi

import (
	"context"
	"net/http"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/pat"
)

type PatStatusReader interface {
	Read(context.Context, string) (pat.Observation, error)
}

// PatAPI exposes one observation operation behind the station's session and
// same-origin gate. It has no destination, method, settings or control input.
type PatAPI struct {
	Radios RadioRecords
	Reader PatStatusReader
}

func (a *PatAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/radio/{name}/pat", a.status)
}

func patUnavailable(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{
		"ok": false, "code": "unavailable", "reason": "Pat status is unavailable.",
	})
}

func (a *PatAPI) status(w http.ResponseWriter, r *http.Request) {
	// Go's GET route also matches HEAD; do not run metadata work for that.
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeError(w, http.StatusMethodNotAllowed, "status requires GET")
		return
	}
	name := r.PathValue("name")
	if !reRadioName.MatchString(name) || a.Radios == nil {
		writeError(w, http.StatusNotFound, "no such radio")
		return
	}
	if _, ok := a.Radios.Get(name); !ok {
		writeError(w, http.StatusNotFound, "no such radio")
		return
	}
	if r.URL.RawQuery != "" || r.ContentLength != 0 || len(r.TransferEncoding) != 0 {
		writeError(w, http.StatusBadRequest, "unexpected status input")
		return
	}
	if a.Reader == nil {
		patUnavailable(w)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Second)
	defer cancel()
	observation, err := a.Reader.Read(ctx, name)
	if err != nil || ctx.Err() != nil || observation.Radio != name || observation.Status.ObservedAt.IsZero() || observation.Status.ActiveListeners == nil {
		patUnavailable(w)
		return
	}
	writeJSON(w, http.StatusOK, observation)
}
