package vnc

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/httpapi"
)

type stubAuth struct{ ok bool }

func (s stubAuth) Session(r *http.Request) (*httpapi.Session, bool) {
	return &httpapi.Session{User: "dietpi"}, s.ok
}

// rig serves Handler behind httpapi's gate, as dxberryd does.
func rig(t *testing.T, dir string, active bool, loggedIn bool) *httptest.Server {
	t.Helper()
	srv := httpapi.New(httpapi.Config{
		Web:  fstest.MapFS{"index.html": {Data: []byte("x")}},
		Auth: stubAuth{ok: loggedIn},
		Routes: func(mux *http.ServeMux) {
			mux.Handle("GET /api/vnc/{session}", Handler(dir, func(ctx context.Context, s string) bool { return active }, func(*http.Request) bool { return true }))
		},
	})
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	return ts
}

func sockDir(t *testing.T) string {
	t.Helper()
	d, err := os.MkdirTemp("", "vnc")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(d) })
	return d
}

// listen starts a unix server at dir/name.sock that runs serve on each connection.
func listen(t *testing.T, dir, name string, serve func(net.Conn)) {
	t.Helper()
	l, err := net.Listen("unix", filepath.Join(dir, name+".sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go serve(c)
		}
	}()
}

func echo(c net.Conn) { defer c.Close(); _, _ = io.Copy(c, c) }

func dial(ts *httptest.Server, session string, hdr map[string]string) (*websocket.Conn, *http.Response, error) {
	h := http.Header{}
	h.Set("Sec-Fetch-Site", "same-origin")
	for k, v := range hdr {
		h.Set(k, v)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/vnc/"+session, &websocket.DialOptions{HTTPHeader: h})
}

func TestVNCRelaysBytes(t *testing.T) {
	dir := sockDir(t)
	listen(t, dir, "wsjtx-radio2", echo)
	c, _, err := dial(rig(t, dir, true, true), "wsjtx-radio2", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	c.SetReadLimit(1 << 20) // a browser has no limit; the test measures the server's frames itself
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	// 150 KB in 30 KB messages: the relay may regroup them, the bytes come back in order
	want := []byte("RFB 003.008\n" + strings.Repeat("x", 150000))
	for off := 0; off < len(want); off += 30000 {
		if err := c.Write(ctx, websocket.MessageBinary, want[off:min(off+30000, len(want))]); err != nil {
			t.Fatal(err)
		}
	}
	var got []byte
	for len(got) < len(want) {
		typ, b, err := c.Read(ctx)
		if err != nil {
			t.Fatalf("after %d bytes: %v", len(got), err)
		}
		if typ != websocket.MessageBinary {
			t.Fatalf("frame type %v, want binary", typ)
		}
		if len(b) > 64<<10 {
			t.Fatalf("frame of %d bytes exceeds 64 KiB", len(b))
		}
		got = append(got, b...)
	}
	if string(got) != string(want) {
		t.Fatal("bytes came back different")
	}
}

func refusal(t *testing.T, ts *httptest.Server, session string, hdr map[string]string, code int, body string) {
	t.Helper()
	_, resp, err := dial(ts, session, hdr)
	if err == nil || resp == nil {
		t.Fatalf("%s: expected a refusal, got err=%v", session, err)
	}
	b, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != code || (body != "" && strings.TrimSpace(string(b)) != body) {
		t.Fatalf("%s: got %d %q, want %d %q", session, resp.StatusCode, b, code, body)
	}
}

func TestVNCRefusesBadName(t *testing.T) {
	dir := sockDir(t)
	ts := rig(t, dir, true, true)
	for _, s := range []string{"..%2Fx", "x", "Wsjtx-radio2", "wsjtx-radio2.sock", "a-b%2F..%2F..%2Fetc", strings.Repeat("a", 40) + "-r"} {
		if strings.Contains(s, "%2F") { // decodes to a path with .. in it: the server refuses it before routing
			refusal(t, ts, s, nil, 404, `{"error":"not found"}`)
			continue
		}
		refusal(t, ts, s, nil, 400, `{"error":"bad session name"}`)
	}
}

func TestVNCRefusesInactiveSession(t *testing.T) {
	dir := sockDir(t)
	listen(t, dir, "wsjtx-radio2", echo)
	refusal(t, rig(t, dir, false, true), "wsjtx-radio2", nil, 409, `{"error":"the program window is not running"}`)
}

func TestVNCRefusesWhenSocketDoesNotAnswer(t *testing.T) {
	refusal(t, rig(t, sockDir(t), true, true), "wsjtx-radio2", nil, 502, `{"error":"the program window did not answer"}`)
}

func TestVNCRefusesForeignOrigin(t *testing.T) {
	dir := sockDir(t)
	listen(t, dir, "wsjtx-radio2", echo)
	ts := rig(t, dir, true, true)
	_, resp, err := websocket.Dial(context.Background(), "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/vnc/wsjtx-radio2",
		&websocket.DialOptions{HTTPHeader: http.Header{"Origin": {"http://evil.example"}}})
	if err == nil || resp == nil || resp.StatusCode != 403 {
		t.Fatalf("foreign origin: err=%v resp=%v", err, resp)
	}
}

func TestVNCRefusesWithoutSession(t *testing.T) {
	dir := sockDir(t)
	listen(t, dir, "wsjtx-radio2", echo)
	refusal(t, rig(t, dir, true, false), "wsjtx-radio2", nil, 401, "")
}

func TestVNCClosesWhenSocketCloses(t *testing.T) {
	dir := sockDir(t)
	listen(t, dir, "wsjtx-radio2", func(c net.Conn) {
		buf := make([]byte, 16)
		_, _ = c.Read(buf)
		c.Close()
	})
	c, _, err := dial(rig(t, dir, true, true), "wsjtx-radio2", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.Write(ctx, websocket.MessageBinary, []byte("hello")); err != nil {
		t.Fatal(err)
	}
	if _, _, err := c.Read(ctx); websocket.CloseStatus(err) == -1 {
		t.Fatalf("expected a close from the server, got %v", err)
	}
}

func TestVNCClosesSocketWhenBrowserCloses(t *testing.T) {
	dir := sockDir(t)
	gone := make(chan struct{})
	listen(t, dir, "wsjtx-radio2", func(c net.Conn) {
		_, _ = io.Copy(io.Discard, c) // returns when the relay closes its end
		close(gone)
	})
	c, _, err := dial(rig(t, dir, true, true), "wsjtx-radio2", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := c.Write(context.Background(), websocket.MessageBinary, []byte("hi")); err != nil {
		t.Fatal(err)
	}
	_ = c.Close(websocket.StatusNormalClosure, "")
	select {
	case <-gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the program's socket stayed open after the browser left")
	}
}

func TestVNCRefusesOversizedFrame(t *testing.T) {
	dir := sockDir(t)
	listen(t, dir, "wsjtx-radio2", echo)
	c, _, err := dial(rig(t, dir, true, true), "wsjtx-radio2", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	c.SetReadLimit(-1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = c.Write(ctx, websocket.MessageBinary, make([]byte, 200<<10))
	if _, _, err := c.Read(ctx); websocket.CloseStatus(err) != websocket.StatusMessageTooBig {
		t.Fatalf("want StatusMessageTooBig, got %v", err)
	}
}

func TestVNCClosesOnLogout(t *testing.T) {
	dir := sockDir(t)
	gone := make(chan struct{})
	listen(t, dir, "wsjtx-radio2", func(c net.Conn) {
		_, _ = io.Copy(io.Discard, c)
		close(gone)
	})
	var alive atomic.Bool
	alive.Store(true)
	srv := httpapi.New(httpapi.Config{
		Web:  fstest.MapFS{"index.html": {Data: []byte("x")}},
		Auth: stubAuth{ok: true},
		Routes: func(mux *http.ServeMux) {
			mux.Handle("GET /api/vnc/{session}", newHandler(dir, func(context.Context, string) bool { return true },
				func(*http.Request) bool { return alive.Load() }, 30*time.Millisecond))
		},
	})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	c, _, err := dial(ts, "wsjtx-radio2", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	alive.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, _, err = c.Read(ctx)
	if got := websocket.CloseStatus(err); got != websocket.StatusPolicyViolation {
		t.Fatalf("closed with %d (%v), want policy violation", got, err)
	}
	select {
	case <-gone:
	case <-time.After(5 * time.Second):
		t.Fatal("the program's socket stayed open after the logout")
	}
}
