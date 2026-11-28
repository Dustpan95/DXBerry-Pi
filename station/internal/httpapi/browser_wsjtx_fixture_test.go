//go:build browserfixture

package httpapi

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"

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
	// Minimal shared RFB 3.8 desktop for the mobile viewer. The real client
	// negotiates its display and emits ordinary key and pointer frames.
	var peerMu sync.Mutex
	var peer *websocket.Conn
	mux.HandleFunc("GET /api/vnc/wsjtx-radio1", func(w http.ResponseWriter, r *http.Request) {
		ws, err := websocket.Accept(w, r, nil)
		if err != nil {
			return
		}
		defer ws.CloseNow()
		peerMu.Lock()
		peer = ws
		peerMu.Unlock()
		header := make([]byte, 24)
		binary.BigEndian.PutUint16(header, 1200)
		binary.BigEndian.PutUint16(header[2:], 800)
		copy(header[4:], []byte{32, 24, 0, 1, 0, 255, 0, 255, 0, 255, 16, 8, 0})
		for i, packet := range [][]byte{[]byte("RFB 003.008\n"), {1, 1}, {0, 0, 0, 0}, header} {
			if ws.Write(r.Context(), websocket.MessageBinary, packet) != nil {
				return
			}
			_, msg, err := ws.Read(r.Context())
			if err != nil || (i == 2 && (len(msg) != 1 || msg[0] != 1)) {
				return
			}
		}
		for {
			if _, _, err := ws.Read(r.Context()); err != nil {
				return
			}
		}
	})
	mux.HandleFunc("POST /fixture/drop-viewer", func(w http.ResponseWriter, r *http.Request) {
		peerMu.Lock()
		defer peerMu.Unlock()
		if peer != nil {
			peer.CloseNow()
		}
		w.WriteHeader(http.StatusNoContent)
	})
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
