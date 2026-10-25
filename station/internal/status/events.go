package status

import (
	"context"
	"encoding/json"
	"net/http"
	"time"

	"github.com/coder/websocket"
)

var (
	pingEvery    = 30 * time.Second
	writeTimeout = 5 * time.Second // a client that cannot take a message in this long is closed
)

const readLimit = 64 << 10 // the most a client frame may carry

// Register mounts GET /api/status and GET /api/events. Both sit behind httpapi's session gate
// and same-origin rule.
func (s *Status) Register(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/status", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_, _ = w.Write(s.Snapshot())
	})
	mux.HandleFunc("GET /api/events", s.events)
}

func (s *Status) message() []byte {
	snap := s.Snapshot()
	b := make([]byte, 0, len(snap)+32)
	b = append(b, `{"type":"status","data":`...)
	b = append(b, snap...)
	return append(b, '}')
}

// events serves one browser: the current status at once, then after every refresh, with a ping
// in between. This goroutine writes; one reader goroutine drains and ignores what the browser
// sends (a "watch" message is accepted and does nothing in this release). Both end with the
// socket.
func (s *Status) events(w http.ResponseWriter, r *http.Request) {
	ws, err := websocket.Accept(w, r, nil) // writes its own refusal, a foreign Origin among them
	if err != nil {
		return
	}
	ws.SetReadLimit(readLimit)
	ctx, cancel := context.WithCancel(r.Context())
	updates, unsubscribe := s.Subscribe()
	readerDone := make(chan struct{})
	defer func() {
		cancel()
		_ = ws.CloseNow() // ends the reader's Read
		<-readerDone
		unsubscribe()
	}()
	go func() {
		defer close(readerDone)
		for {
			if _, _, err := ws.Read(ctx); err != nil {
				cancel()
				return
			}
		}
	}()

	send := func(b []byte) bool {
		wctx, c := context.WithTimeout(ctx, writeTimeout)
		defer c()
		return ws.Write(wctx, websocket.MessageText, b) == nil
	}
	if !send(s.message()) {
		return
	}
	ping := time.NewTicker(pingEvery)
	defer ping.Stop()
	pingMsg, _ := json.Marshal(map[string]string{"type": "ping"})
	for {
		select {
		case <-ctx.Done():
			return
		case <-updates:
			if !send(s.message()) {
				return
			}
		case <-ping.C:
			if !send(pingMsg) {
				return
			}
		}
	}
}
