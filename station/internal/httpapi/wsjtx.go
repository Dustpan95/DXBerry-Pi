package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/wsjtx"
)

type WSJTXCollection interface {
	Preview(context.Context, string) (wsjtx.ProgramRoutingPreview, error)
	Enable(context.Context, string, string) error
	Disable(context.Context, string) error
	Recover(context.Context, string) error
	CollectionState(context.Context, string) (wsjtx.ProgramCollectionState, error)
}

// WSJTXAPI registers only named observation and configuration operations behind
// the station's normal authentication/same-origin gate. It has no protocol-send
// action, UDP address, settings path or process-lifecycle input.
type WSJTXAPI struct {
	Radios     RadioRecords
	Collection WSJTXCollection
}

func (a *WSJTXAPI) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/radio/{name}/wsjtx", a.status)
	for _, action := range []string{"preview", "enable", "disable", "recover"} {
		mux.HandleFunc("POST /api/radio/{name}/wsjtx/"+action, func(w http.ResponseWriter, r *http.Request) { a.configure(w, r, action) })
	}
}

func (a *WSJTXAPI) known(w http.ResponseWriter, r *http.Request) (string, bool) {
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

func wsjtxFailure(w http.ResponseWriter, err error) {
	code, reason, fix := "unavailable", "Native program status is unavailable.", "Keep the full program window available and review the station service."
	status := http.StatusServiceUnavailable
	switch {
	case errors.Is(err, wsjtx.ErrProgramReleasePaused):
		code, reason, fix, status = "release-paused", "Native collection is paused after a release transition.", "Complete release reconciliation before enabling collection.", http.StatusConflict
	case errors.Is(err, wsjtx.ErrProgramNotConfigured):
		code, reason, fix, status = "not-configured", "Native program status is not configured on this station.", "Complete station setup before enabling collection.", http.StatusConflict
	case errors.Is(err, wsjtx.ErrProgramLifecycle):
		code, reason, fix, status = "program-running", "The program must be stopped before changing collection settings.", "Stop WSJT-X, then try again.", http.StatusConflict
	case errors.Is(err, wsjtx.ErrRoutingChanged), errors.Is(err, wsjtx.ErrRoutingConflict):
		code, reason, fix, status = "routing-conflict", "Program routing changed or needs review.", "Review the existing destination and obtain a fresh preview after recovery.", http.StatusConflict
	case errors.Is(err, wsjtx.ErrRoutingJournalRecovery):
		code, reason, fix, status = "recovery-required", "The previous program route still needs recovery.", "Stop WSJT-X and restore its original destination before enabling collection.", http.StatusConflict
	}
	writeJSON(w, status, map[string]any{"ok": false, "code": code, "reason": reason, "fix": fix})
}

func (a *WSJTXAPI) status(w http.ResponseWriter, r *http.Request) {
	name, ok := a.known(w, r)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()
	state, err := a.Collection.CollectionState(ctx, name)
	if err != nil {
		wsjtxFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, state)
}

func (a *WSJTXAPI) configure(w http.ResponseWriter, r *http.Request, action string) {
	name, ok := a.known(w, r)
	if !ok {
		return
	}
	// Only enable has one input field. Exact keys and a duplicate-key scan
	// reject ambiguous approval input; whitespace is allowed.
	raw, err := io.ReadAll(http.MaxBytesReader(w, r.Body, 1024))
	var fields map[string]json.RawMessage
	if err != nil || json.Unmarshal(raw, &fields) != nil || fields == nil {
		writeError(w, http.StatusBadRequest, "invalid collection request")
		return
	}
	var body struct {
		Fingerprint string `json:"fingerprint"`
	}
	if action == "enable" {
		if len(fields) != 1 || fields["fingerprint"] == nil || json.Unmarshal(raw, &body) != nil || len(body.Fingerprint) != 64 || strings.Trim(body.Fingerprint, "0123456789abcdef") != "" {
			writeError(w, http.StatusBadRequest, "invalid collection approval")
			return
		}
	} else if len(fields) != 0 {
		writeError(w, http.StatusBadRequest, "unexpected collection input")
		return
	}
	// A map alone cannot detect a duplicate same-spelling key.
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	_, _ = decoder.Token()
	seen := make(map[string]bool)
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil || seen[key.(string)] {
			writeError(w, http.StatusBadRequest, "ambiguous collection input")
			return
		}
		seen[key.(string)] = true
		var value any
		if decoder.Decode(&value) != nil {
			writeError(w, http.StatusBadRequest, "invalid collection input")
			return
		}
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if action == "preview" {
		preview, err := a.Collection.Preview(ctx, name)
		if err != nil {
			wsjtxFailure(w, err)
			return
		}
		writeJSON(w, http.StatusOK, preview)
		return
	}
	switch action {
	case "enable":
		err = a.Collection.Enable(ctx, name, body.Fingerprint)
	case "disable":
		err = a.Collection.Disable(ctx, name)
	case "recover":
		err = a.Collection.Recover(ctx, name)
	}
	if err != nil {
		wsjtxFailure(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
}
