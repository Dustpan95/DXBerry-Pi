// Package vnc relays a browser's WebSocket to a program window's VNC socket. Each program's
// screen listens only on a unix socket under socketDir; the browser reaches it here, behind the
// same session gate and same-origin rule as the rest of /api/.
package vnc

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"regexp"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// reSession is the session name pattern (the same one the page checks): <app>-<radio>. Only a
// name that matches is ever joined onto socketDir.
var reSession = regexp.MustCompile(`^[a-z][a-z0-9_]{0,15}-[a-z][a-z0-9]{0,11}$`)

const (
	chunk       = 64 << 10 // the most held in flight, per direction
	dialTimeout = 3 * time.Second
	pollEvery   = 5 * time.Second // how often an open window re-checks that its session is still logged in
)

func refuse(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// Handler serves GET .../{session}. isActive says whether the session's screen unit is running;
// alive says whether the request's login session still is, and is polled while the window is open.
// Mount it behind the session gate; it refuses a bad name (400), an inactive session (409) and a
// socket that does not answer (502) before it upgrades.
func Handler(socketDir string, isActive func(ctx context.Context, session string) bool, alive func(*http.Request) bool) http.Handler {
	return newHandler(socketDir, isActive, alive, pollEvery)
}

// newHandler is Handler with its own poll interval.
func newHandler(socketDir string, isActive func(ctx context.Context, session string) bool, alive func(*http.Request) bool, every time.Duration) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		session := r.PathValue("session")
		if !reSession.MatchString(session) {
			refuse(w, http.StatusBadRequest, "bad session name")
			return
		}
		if !isActive(r.Context(), session) {
			refuse(w, http.StatusConflict, "the program window is not running")
			return
		}
		sock, err := net.DialTimeout("unix", socketDir+"/"+session+".sock", dialTimeout)
		if err != nil {
			refuse(w, http.StatusBadGateway, "the program window did not answer")
			return
		}
		ws, err := websocket.Accept(w, r, nil) // writes its own refusal, a foreign Origin among them
		if err != nil {
			sock.Close()
			return
		}
		relay(r.Context(), ws, sock, func() bool { return alive(r) }, every)
	})
}

// relay copies binary frames both ways until either side ends, then closes both.
func relay(parent context.Context, ws *websocket.Conn, sock net.Conn, alive func() bool, every time.Duration) {
	ws.SetReadLimit(chunk)
	ctx, cancel := context.WithCancel(parent)
	var once sync.Once
	end := func(status websocket.StatusCode, reason string) {
		once.Do(func() {
			sock.Close()
			_ = ws.Close(status, reason) // before cancel: a cancelled read drops the connection without a close frame
			cancel()
		})
	}
	var wg sync.WaitGroup
	wg.Add(3)
	go func() { // the login session may end while the window is open
		defer wg.Done()
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if !alive() {
					end(websocket.StatusPolicyViolation, "logged out")
					return
				}
			}
		}
	}()
	go func() { // program -> browser
		defer wg.Done()
		buf := make([]byte, chunk)
		for {
			n, err := sock.Read(buf)
			if n > 0 {
				if werr := ws.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
					end(websocket.StatusGoingAway, "")
					return
				}
			}
			if err != nil {
				if err == io.EOF {
					end(websocket.StatusNormalClosure, "program window closed")
				} else {
					end(websocket.StatusGoingAway, "program window closed")
				}
				return
			}
		}
	}()
	go func() { // browser -> program
		defer wg.Done()
		for {
			typ, data, err := ws.Read(ctx)
			if err != nil {
				end(websocket.StatusNormalClosure, "")
				return
			}
			if typ != websocket.MessageBinary {
				end(websocket.StatusUnsupportedData, "binary frames only")
				return
			}
			if _, err := sock.Write(data); err != nil {
				end(websocket.StatusGoingAway, "program window closed")
				return
			}
		}
	}()
	wg.Wait()
	cancel()
}
