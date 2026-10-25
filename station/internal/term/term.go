// Package term serves a login shell on a pseudo-terminal over a WebSocket. It sits behind the same
// session gate and same-origin rule as the rest of /api/, runs the command as the logged-in user
// (dxberryd itself runs as root), and ends the shell the moment the socket, the session or the
// process ends.
package term

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"sync"
	"syscall"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// pollEvery is how often a live terminal re-checks that its session is still logged in.
var pollEvery = 5 * time.Second

const (
	chunk     = 32 << 10
	startCols = 80
	startRows = 24
	maxCols   = 500
	maxRows   = 200
	killAfter = 2 * time.Second // a shell that ignores SIGHUP is killed after this
	drain     = time.Second     // output still wanted after the process exits
)

type resizeMsg struct {
	Resize *struct {
		Cols int `json:"cols"`
		Rows int `json:"rows"`
	} `json:"resize"`
}

// Handler serves GET /api/term. cmd gives the command line to run for a user, user names the
// logged-in user of a request, id names the request's session (one terminal per session; a second
// request closes the first) and alive re-checks that session, polled while the terminal is open.
func Handler(cmd func(user string) []string, user, id func(*http.Request) string, alive func(*http.Request) bool) http.Handler {
	var mu sync.Mutex
	type held struct{ close func() }
	open := map[string]*held{} // session id -> the terminal it holds
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		argv := cmd(user(r))
		if len(argv) == 0 {
			http.Error(w, "no command", http.StatusInternalServerError)
			return
		}
		ws, err := websocket.Accept(w, r, nil) // writes its own refusal, a foreign Origin among them
		if err != nil {
			return
		}
		ws.SetReadLimit(chunk)
		c := exec.Command(argv[0], argv[1:]...)
		c.Env = []string{"TERM=xterm-256color", "PATH=/usr/sbin:/usr/bin:/sbin:/bin"}
		tty, err := pty.StartWithSize(c, &pty.Winsize{Rows: startRows, Cols: startCols}) // own session and process group
		if err != nil {
			_ = ws.Close(websocket.StatusInternalError, "cannot start the terminal")
			return
		}
		ctx, cancel := context.WithCancel(r.Context())
		exited := make(chan struct{})
		var once sync.Once
		end := func(status websocket.StatusCode, reason string) {
			once.Do(func() {
				stop(c, tty, exited)
				_ = ws.Close(status, reason) // before cancel: a cancelled read drops the connection without a close frame
				cancel()
			})
		}
		key := id(r)
		me := &held{close: func() { end(websocket.StatusGoingAway, "another terminal replaced this one") }}
		mu.Lock()
		prev := open[key]
		open[key] = me
		mu.Unlock()
		if prev != nil {
			go prev.close() // closing waits on a socket handshake: never under the lock
		}

		go func() { // reap, then give the last output a moment to drain
			_ = c.Wait()
			close(exited)
			select {
			case <-time.After(drain):
				end(websocket.StatusNormalClosure, "the shell ended")
			case <-ctx.Done():
			}
		}()
		var wg sync.WaitGroup
		wg.Add(3)
		go func() { // process -> browser
			defer wg.Done()
			buf := make([]byte, chunk)
			for {
				n, err := tty.Read(buf)
				if n > 0 {
					if werr := ws.Write(ctx, websocket.MessageBinary, buf[:n]); werr != nil {
						end(websocket.StatusGoingAway, "")
						return
					}
				}
				if err != nil { // EIO once the process and its children let go of the tty
					end(websocket.StatusNormalClosure, "the shell ended")
					return
				}
			}
		}()
		go func() { // browser -> process
			defer wg.Done()
			for {
				typ, data, err := ws.Read(ctx)
				if err != nil {
					end(websocket.StatusNormalClosure, "")
					return
				}
				if typ == websocket.MessageText {
					resize(tty, data)
					continue
				}
				if _, err := tty.Write(data); err != nil {
					end(websocket.StatusNormalClosure, "the shell ended")
					return
				}
			}
		}()
		go func() { // the session may end while the terminal is open
			defer wg.Done()
			t := time.NewTicker(pollEvery)
			defer t.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-t.C:
					if !alive(r) {
						end(websocket.StatusPolicyViolation, "logged out")
						return
					}
				}
			}
		}()
		wg.Wait()
		cancel()
		<-exited
		mu.Lock()
		if open[key] == me { // a newer terminal may hold the key by now
			delete(open, key)
		}
		mu.Unlock()
	})
}

// resize applies {"resize":{"cols":C,"rows":R}}; anything else, or a size out of bounds, is ignored.
func resize(tty *os.File, data []byte) {
	var m resizeMsg
	if json.Unmarshal(data, &m) != nil || m.Resize == nil {
		return
	}
	cols, rows := m.Resize.Cols, m.Resize.Rows
	if cols < 1 || cols > maxCols || rows < 1 || rows > maxRows {
		return
	}
	_ = pty.Setsize(tty, &pty.Winsize{Rows: uint16(rows), Cols: uint16(cols)})
}

// stop hangs the shell up (SIGHUP to its whole process group), closes the pty, and kills what
// ignored the hangup after killAfter unless it has been reaped by then (exited closed). The
// caller's Wait reaps it.
func stop(c *exec.Cmd, tty *os.File, exited <-chan struct{}) {
	pid := c.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGHUP)
	_ = tty.Close()
	time.AfterFunc(killAfter, func() {
		select {
		case <-exited:
		default:
			_ = syscall.Kill(-pid, syscall.SIGKILL)
		}
	})
}
