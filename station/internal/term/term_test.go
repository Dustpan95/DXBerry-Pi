package term

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/httpapi"
)

type stubAuth struct{ ok bool }

func (s stubAuth) Session(r *http.Request) (*httpapi.Session, bool) {
	return &httpapi.Session{ID: "s1", User: "dietpi"}, s.ok
}

type rigT struct {
	ts    *httptest.Server
	alive *atomic.Bool
}

// rig serves Handler behind httpapi's gate. The command writes its pid to pidfile, says hello and
// then echoes its input, so no login binary is needed.
func rig(t *testing.T, loggedIn bool, pidfile string, every time.Duration) rigT {
	t.Helper()
	alive := &atomic.Bool{}
	alive.Store(true)
	cmd := func(string) []string {
		return []string{"/bin/sh", "-c", "echo $$ > " + pidfile + "; echo hello; cat"}
	}
	h, running := newHandler(cmd,
		func(r *http.Request) string { return "dietpi" },
		func(r *http.Request) string { return "s1" },
		func(r *http.Request) bool { return alive.Load() }, every)
	srv := httpapi.New(httpapi.Config{
		Web:  fstest.MapFS{"index.html": {Data: []byte("x")}},
		Auth: stubAuth{ok: loggedIn},
		Routes: func(mux *http.ServeMux) {
			mux.Handle("GET /api/term", h)
		},
	})
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(running.Wait) // runs last: every handler goroutine is done before the test returns
	t.Cleanup(ts.Close)
	return rigT{ts, alive}
}

func dial(t *testing.T, ts *httptest.Server) *websocket.Conn {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/term", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {ts.URL}, "Sec-Fetch-Site": {"same-origin"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { c.CloseNow() })
	return c
}

// readUntil reads binary frames until the accumulated output contains want.
func readUntil(t *testing.T, c *websocket.Conn, want string) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	var got strings.Builder
	for !strings.Contains(got.String(), want) {
		typ, data, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("waiting for %q, got %q: %v", want, got.String(), err)
		}
		if typ != websocket.MessageBinary {
			t.Fatalf("output frame is not binary")
		}
		got.Write(data)
	}
	return got.String()
}

func readPid(t *testing.T, file string) int {
	t.Helper()
	for i := 0; i < 100; i++ {
		b, _ := os.ReadFile(file)
		if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil {
			return n
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("the process never wrote its pid")
	return 0
}

// gone reports whether pid no longer exists, waiting up to d.
func gone(pid int, d time.Duration) bool {
	end := time.Now().Add(d)
	for time.Now().Before(end) {
		if _, err := os.Stat("/proc/" + strconv.Itoa(pid)); err != nil {
			return true
		}
		time.Sleep(25 * time.Millisecond)
	}
	return false
}

func expectClose(t *testing.T, c *websocket.Conn, want websocket.StatusCode) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		_, _, err := c.Read(ctx)
		if err == nil {
			continue
		}
		if got := websocket.CloseStatus(err); got != want {
			t.Fatalf("closed with %d (%v), want %d", got, err, want)
		}
		return
	}
}

func TestTermEchoes(t *testing.T) {
	pf := filepath.Join(t.TempDir(), "pid")
	r := rig(t, true, pf, time.Hour)
	c := dial(t, r.ts)
	readUntil(t, c, "hello")
	if err := c.Write(context.Background(), websocket.MessageBinary, []byte("abc\n")); err != nil {
		t.Fatal(err)
	}
	readUntil(t, c, "abc")
}

func TestTermResizeAccepted(t *testing.T) {
	pf := filepath.Join(t.TempDir(), "pid")
	r := rig(t, true, pf, time.Hour)
	c := dial(t, r.ts)
	readUntil(t, c, "hello")
	ctx := context.Background()
	// a resize outside the bounds is ignored, a good one applies
	_ = c.Write(ctx, websocket.MessageText, []byte(`{"resize":{"cols":9999,"rows":30}}`))
	_ = c.Write(ctx, websocket.MessageText, []byte(`{"resize":{"cols":100,"rows":30}}`))
	// the shell cannot be asked inside cat, so read the window size the kernel holds for the tty
	pid := readPid(t, pf)
	var size string
	for i := 0; i < 100; i++ {
		size = ttySize(pid)
		if size == "30 100" {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if size != "30 100" {
		t.Fatalf("window size = %q, want 30 100", size)
	}
}

// ttySize reads the window size of the terminal the process holds as its standard input.
func ttySize(pid int) string {
	f, err := os.OpenFile("/proc/"+strconv.Itoa(pid)+"/fd/0", os.O_RDONLY, 0)
	if err != nil {
		return err.Error()
	}
	defer f.Close()
	rows, cols, err := pty.Getsize(f)
	if err != nil {
		return err.Error()
	}
	return strconv.Itoa(rows) + " " + strconv.Itoa(cols)
}

func TestTermClosesOnLogout(t *testing.T) {
	pf := filepath.Join(t.TempDir(), "pid")
	r := rig(t, true, pf, 50*time.Millisecond)
	c := dial(t, r.ts)
	readUntil(t, c, "hello")
	pid := readPid(t, pf)
	r.alive.Store(false)
	expectClose(t, c, websocket.StatusPolicyViolation)
	if !gone(pid, 2*time.Second) {
		t.Fatal("the process outlived the logout")
	}
}

func TestTermSocketCloseEndsProcess(t *testing.T) {
	pf := filepath.Join(t.TempDir(), "pid")
	r := rig(t, true, pf, time.Hour)
	c := dial(t, r.ts)
	readUntil(t, c, "hello")
	pid := readPid(t, pf)
	c.Close(websocket.StatusNormalClosure, "")
	if !gone(pid, 3*time.Second) {
		t.Fatal("the process outlived the socket")
	}
}

func TestTermRefusesWithoutSession(t *testing.T) {
	r := rig(t, false, filepath.Join(t.TempDir(), "pid"), time.Hour)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, resp, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(r.ts.URL, "http")+"/api/term", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {r.ts.URL}, "Sec-Fetch-Site": {"same-origin"}},
	})
	if err == nil {
		t.Fatal("dial succeeded without a session")
	}
	if resp == nil || resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("response = %v, want 401", resp)
	}
}

func TestTermSecondRequestClosesFirst(t *testing.T) {
	pf := filepath.Join(t.TempDir(), "pid")
	r := rig(t, true, pf, time.Hour)
	c1 := dial(t, r.ts)
	readUntil(t, c1, "hello")
	pid1 := readPid(t, pf)
	_ = os.Remove(pf)
	c2 := dial(t, r.ts)
	expectClose(t, c1, websocket.StatusGoingAway)
	if !gone(pid1, 3*time.Second) {
		t.Fatal("the first terminal's process outlived its replacement")
	}
	readUntil(t, c2, "hello")
}
