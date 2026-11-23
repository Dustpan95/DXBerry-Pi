//go:build browserfixture

package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/status"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/wsjtx"
)

type browserProgramInput struct {
	State  wsjtx.ProgramCollectionState `json:"state"`
	Fail   bool                         `json:"fail"`
	Freeze bool                         `json:"freeze"`
}

// Only the tagged loopback browser fixture supplies program observations.
// The producer/socket/renderer are real; no Qt program, radio or routing state
// is opened, and this provider has no collection/lifecycle mutation methods.
type browserProgramSource struct {
	mu    sync.Mutex
	input browserProgramInput
}

func (p *browserProgramSource) read(ctx context.Context, radio string) (wsjtx.ProgramCollectionState, error) {
	if ctx.Err() != nil || radio != "radio1" {
		return wsjtx.ProgramCollectionState{}, errors.New("unavailable fixture radio")
	}
	p.mu.Lock()
	raw, err := json.Marshal(p.input)
	p.mu.Unlock()
	var input browserProgramInput
	if err != nil || json.Unmarshal(raw, &input) != nil || input.Fail {
		return wsjtx.ProgramCollectionState{}, errors.New("private fixture detail must not reach the browser")
	}
	if observation := input.State.Observation; observation != nil && observation.Availability == wsjtx.Live && !input.Freeze {
		now := time.Now()
		observation.LastSeen = &now
	}
	return input.State, nil
}

func (p *browserProgramSource) register(mux *http.ServeMux, stream *status.Status) {
	mux.HandleFunc("POST /fixture/native", func(w http.ResponseWriter, r *http.Request) {
		var input browserProgramInput
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&input) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		p.mu.Lock()
		p.input = input
		p.mu.Unlock()
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
	// Explicit malformed/cross-radio delivery exercises client filtering too.
	// It is not a route in any ordinary or release build.
	mux.HandleFunc("POST /fixture/native-frame", func(w http.ResponseWriter, r *http.Request) {
		var frame json.RawMessage
		if json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&frame) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		stream.Notify("radio1", frame)
		writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
	})
}

type browserProgramRadios struct{ radio radios.Radio }

func (s browserProgramRadios) List() []radios.Radio            { return []radios.Radio{s.radio} }
func (browserProgramRadios) Subscribe() <-chan []radios.Radio  { return make(chan []radios.Radio) }
func (browserProgramRadios) Unsubscribe(<-chan []radios.Radio) {}
