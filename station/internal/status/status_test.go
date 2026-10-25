package status

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/coder/websocket"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/httpapi"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
)

// fakeClock only moves when a test advances it; At fires when the clock reaches the deadline.
type fakeClock struct {
	mu      sync.Mutex
	now     time.Time
	waiters []fakeWaiter
}

type fakeWaiter struct {
	at time.Time
	ch chan time.Time
}

func newClock() *fakeClock { return &fakeClock{now: time.Unix(1_700_000_000, 0)} }

func (c *fakeClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *fakeClock) At(t time.Time) <-chan time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	ch := make(chan time.Time, 1)
	if !t.After(c.now) {
		ch <- c.now
		return ch
	}
	c.waiters = append(c.waiters, fakeWaiter{t, ch})
	return ch
}

func (c *fakeClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
	keep := c.waiters[:0]
	for _, w := range c.waiters {
		if w.at.After(c.now) {
			keep = append(keep, w)
		} else {
			w.ch <- c.now
		}
	}
	c.waiters = keep
}

func eventually(t *testing.T, what string, ok func() bool) {
	t.Helper()
	end := time.Now().Add(5 * time.Second)
	for time.Now().Before(end) {
		if ok() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// never asserts cond stays false for a short while.
func never(t *testing.T, what string, cond func() bool) {
	t.Helper()
	time.Sleep(60 * time.Millisecond)
	if cond() {
		t.Fatalf("unexpected: %s", what)
	}
}

type rig struct {
	st    *Status
	clock *fakeClock
	runs  atomic.Int32
	out   atomic.Value // string
	fail  atomic.Bool
}

func newRig(t *testing.T) *rig {
	t.Helper()
	r := &rig{clock: newClock()}
	r.out.Store(`{"station":{"ok":true,"callsign":"N0CALL"},"generated":1}`)
	r.st = New(func(ctx context.Context) (string, error) {
		r.runs.Add(1)
		if r.fail.Load() {
			return "", errors.New("dxberry-status failed")
		}
		return r.out.Load().(string), nil
	}, r.clock)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.st.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })
	return r
}

func (r *rig) count() int { return int(r.runs.Load()) }

func snap(t *testing.T, st *Status) (gen int64, stale bool, status json.RawMessage) {
	t.Helper()
	var m struct {
		Generated int64           `json:"generated"`
		Stale     bool            `json:"stale"`
		Status    json.RawMessage `json:"status"`
	}
	if err := json.Unmarshal(st.Snapshot(), &m); err != nil {
		t.Fatal(err)
	}
	return m.Generated, m.Stale, m.Status
}

func TestStatusCachesBetweenRuns(t *testing.T) {
	r := newRig(t)
	eventually(t, "the first run", func() bool { return r.count() == 1 })
	for i := 0; i < 5; i++ {
		_, stale, status := snap(t, r.st)
		if stale || !strings.Contains(string(status), "N0CALL") {
			t.Fatalf("snapshot %d: stale=%v status=%s", i, stale, status)
		}
	}
	r.clock.Advance(59 * time.Second)
	never(t, "a run before the idle interval", func() bool { return r.count() != 1 })
	r.clock.Advance(time.Second)
	eventually(t, "the run at 60 s", func() bool { return r.count() == 2 })
}

func TestStatusKeepsLastAnswerAndGoesStaleWhenARunFails(t *testing.T) {
	r := newRig(t)
	eventually(t, "the first run", func() bool { return r.count() == 1 })
	gen, _, _ := snap(t, r.st)
	if gen != r.clock.Now().UnixMilli() {
		t.Fatalf("generated %d, want the run's time", gen)
	}
	r.fail.Store(true)
	r.clock.Advance(60 * time.Second)
	eventually(t, "the failing run", func() bool { return r.count() == 2 })
	eventually(t, "stale", func() bool { _, s, _ := snap(t, r.st); return s })
	gen2, _, status := snap(t, r.st)
	if gen2 != gen || !strings.Contains(string(status), "N0CALL") {
		t.Fatalf("a failed run must keep the last answer: gen %d/%d status %s", gen, gen2, status)
	}
}

func TestStatusRefreshesAfterChangingCall(t *testing.T) {
	r := newRig(t)
	eventually(t, "the first run", func() bool { return r.count() == 1 })
	r.clock.Advance(2 * time.Second)
	r.st.Poke()
	eventually(t, "the poked run", func() bool { return r.count() == 2 })
	// a second poke inside the same second waits for the next second
	r.st.Poke()
	never(t, "an undebounced run", func() bool { return r.count() != 2 })
	r.clock.Advance(time.Second)
	eventually(t, "the debounced run", func() bool { return r.count() == 3 })
}

func TestChangesState(t *testing.T) {
	for _, c := range []struct {
		cmd  string
		args []string
		want bool
	}{
		{"dxberry-radio", []string{"claim", "radio1", "graywolf"}, true},
		{"dxberry-config", []string{"set", "X=1"}, true},
		{"systemctl", []string{"restart", "gpsd.service"}, true},
		{"dxberry-app", []string{"switch", "wsjtx"}, true},
		{"dxberry-radio", []string{"status", "--json"}, false},
		{"dxberry-config", []string{"get", "--json"}, false},
		{"dxberry-update", []string{"check", "--json"}, false},
		{"dxberry-update", []string{"job"}, false},
		{"dxberry-app", []string{"recent"}, false},
		{"dxberry-app", []string{"screen", "x-y"}, false},
		{"dxberry-radio", []string{"models"}, false},
		{"dxberry-radio", []string{"scan"}, false},
		{"dxberry-app", []string{"list"}, false},
		{"dxberry-status", []string{"--json"}, false},
		{"journalctl", []string{"-u", "gpsd.service"}, false},
		{"dxberry-app", nil, false},
	} {
		if got := ChangesState(c.cmd, c.args); got != c.want {
			t.Errorf("ChangesState(%s %v) = %v, want %v", c.cmd, c.args, got, c.want)
		}
	}
}

// ---- the events socket ------------------------------------------------------------------------

type stubAuth struct{ ok bool }

func (s stubAuth) Session(r *http.Request) (*httpapi.Session, bool) {
	return &httpapi.Session{ID: "s1", User: "dietpi"}, s.ok
}

func serve(t *testing.T, st *Status, loggedIn bool) *httptest.Server {
	t.Helper()
	srv := httpapi.New(httpapi.Config{
		Web:    fstest.MapFS{"index.html": {Data: []byte("x")}},
		Auth:   stubAuth{ok: loggedIn},
		Routes: func(mux *http.ServeMux) { st.Register(mux, func(*http.Request) bool { return true }) },
	})
	ts := httptest.NewServer(srv.Handler)
	t.Cleanup(ts.Close)
	return ts
}

func dial(t *testing.T, ts *httptest.Server) (*websocket.Conn, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/events", &websocket.DialOptions{
		HTTPHeader: http.Header{"Origin": {ts.URL}, "Sec-Fetch-Site": {"same-origin"}},
	})
	if err == nil {
		t.Cleanup(func() { c.CloseNow() })
	}
	return c, err
}

func readMsg(t *testing.T, c *websocket.Conn) map[string]json.RawMessage {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	typ, data, err := c.Read(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if typ != websocket.MessageText {
		t.Fatal("not a text frame")
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("%s: %v", data, err)
	}
	return m
}

func TestEventsSendsStatusOnConnect(t *testing.T) {
	r := newRig(t)
	eventually(t, "the first run", func() bool { return r.count() == 1 })
	ts := serve(t, r.st, true)
	c, err := dial(t, ts)
	if err != nil {
		t.Fatal(err)
	}
	m := readMsg(t, c)
	if string(m["type"]) != `"status"` {
		t.Fatalf("first message %v", m)
	}
	var d struct {
		Generated int64 `json:"generated"`
		Stale     bool  `json:"stale"`
		Status    struct {
			Station struct{ Callsign string } `json:"station"`
		} `json:"status"`
	}
	if err := json.Unmarshal(m["data"], &d); err != nil || d.Status.Station.Callsign != "N0CALL" || d.Stale || d.Generated == 0 {
		t.Fatalf("data %s: %v %+v", m["data"], err, d)
	}
	// a refresh pushes the same shape again
	r.out.Store(`{"station":{"ok":true,"callsign":"W1AW"}}`)
	r.st.Poke()
	r.clock.Advance(2 * time.Second)
	r.st.Poke()
	for i := 0; i < 3; i++ {
		m = readMsg(t, c)
		if strings.Contains(string(m["data"]), "W1AW") {
			return
		}
	}
	t.Fatal("no message carried the refreshed status")
}

func TestEventsRequiresSession(t *testing.T) {
	r := newRig(t)
	ts := serve(t, r.st, false)
	if _, err := dial(t, ts); err == nil {
		t.Fatal("the socket opened without a session")
	}
	if r.st.Listeners() != 0 {
		t.Fatal("a refused client was counted")
	}
	req, _ := http.NewRequest("GET", ts.URL+"/api/status", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 401 {
		t.Fatalf("/api/status without a session: %d", resp.StatusCode)
	}
}

func TestStatusEndpoint(t *testing.T) {
	r := newRig(t)
	eventually(t, "the first run", func() bool { return r.count() == 1 })
	ts := serve(t, r.st, true)
	req, _ := http.NewRequest("GET", ts.URL+"/api/status", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var m struct {
		Generated int64
		Stale     bool
		Status    struct{ Station struct{ Callsign string } }
	}
	if err := json.NewDecoder(resp.Body).Decode(&m); err != nil || resp.StatusCode != 200 || m.Status.Station.Callsign != "N0CALL" || m.Generated == 0 {
		t.Fatalf("%d %+v %v", resp.StatusCode, m, err)
	}
}

func TestEventsRateFollowsListeners(t *testing.T) {
	r := newRig(t)
	eventually(t, "the first run", func() bool { return r.count() == 1 })
	r.clock.Advance(10 * time.Second)
	never(t, "a 10 s run with nobody listening", func() bool { return r.count() != 1 })

	ts := serve(t, r.st, true)
	c, err := dial(t, ts)
	if err != nil {
		t.Fatal(err)
	}
	readMsg(t, c)
	eventually(t, "one listener", func() bool { return r.st.Listeners() == 1 })
	// the cache was 10 s old when the listener came, so it is refreshed now
	eventually(t, "a refresh for the new listener", func() bool { return r.count() == 2 })
	r.clock.Advance(10 * time.Second)
	eventually(t, "a 10 s run while listened to", func() bool { return r.count() == 3 })

	c.CloseNow()
	eventually(t, "no listeners", func() bool { return r.st.Listeners() == 0 })
	r.clock.Advance(10 * time.Second)
	never(t, "a 10 s run after the listener left", func() bool { return r.count() != 3 })
	r.clock.Advance(50 * time.Second)
	eventually(t, "the 60 s run", func() bool { return r.count() == 4 })
}

func TestEventsSendsPings(t *testing.T) {
	old := pingEvery
	pingEvery = 30 * time.Millisecond
	defer func() { pingEvery = old }()
	r := newRig(t)
	ts := serve(t, r.st, true)
	c, err := dial(t, ts)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if string(readMsg(t, c)["type"]) == `"ping"` {
			return
		}
	}
	t.Fatal("no ping")
}

func TestEventsIgnoresClientFrames(t *testing.T) {
	r := newRig(t)
	ts := serve(t, r.st, true)
	c, err := dial(t, ts)
	if err != nil {
		t.Fatal(err)
	}
	readMsg(t, c)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, f := range []string{`{"type":"watch","parts":["radios"]}`, `garbage`, `{"type":"nope"}`} {
		if err := c.Write(ctx, websocket.MessageText, []byte(f)); err != nil {
			t.Fatal(err)
		}
	}
	time.Sleep(50 * time.Millisecond)
	if r.st.Listeners() != 1 {
		t.Fatal("a client frame ended the stream")
	}
}

func TestEventsSlowClientClosed(t *testing.T) {
	old := writeTimeout
	writeTimeout = 300 * time.Millisecond
	defer func() { writeTimeout = old }()
	r := newRig(t)
	r.out.Store(`{"pad":"` + strings.Repeat("x", 8<<20) + `"}`)
	eventually(t, "the first run", func() bool { return r.count() == 1 })
	ts := serve(t, r.st, true)
	c, err := dial(t, ts) // and never read: the socket buffers fill
	if err != nil {
		t.Fatal(err)
	}
	_ = c
	eventually(t, "the client counted", func() bool { return r.st.Listeners() == 1 })
	for i := 0; i < 8 && r.st.Listeners() == 1; i++ {
		r.clock.Advance(10 * time.Second)
		time.Sleep(100 * time.Millisecond)
	}
	eventually(t, "the slow client closed", func() bool { return r.st.Listeners() == 0 })
}

func TestStatusNotJSONIsAFailure(t *testing.T) {
	r := &rig{clock: newClock()}
	r.st = New(func(ctx context.Context) (string, error) { r.runs.Add(1); return "not json", nil }, r.clock)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { r.st.Run(ctx); close(done) }()
	defer func() { cancel(); <-done }()
	eventually(t, "the run", func() bool { return r.count() == 1 })
	gen, stale, status := snap(t, r.st)
	if gen != 0 || !stale || string(status) != "null" {
		t.Fatalf("gen %d stale %v status %s", gen, stale, status)
	}
}

func TestEventsCloseOnLogout(t *testing.T) {
	r := newRig(t)
	eventually(t, "the first run", func() bool { return r.count() == 1 })
	var alive atomic.Bool
	alive.Store(true)
	srv := httpapi.New(httpapi.Config{
		Web:  fstest.MapFS{"index.html": {Data: []byte("x")}},
		Auth: stubAuth{ok: true},
		Routes: func(mux *http.ServeMux) {
			mux.Handle("GET /api/events", r.st.eventsHandler(func(*http.Request) bool { return alive.Load() }, 30*time.Millisecond))
		},
	})
	ts := httptest.NewServer(srv.Handler)
	defer ts.Close()
	c, err := dial(t, ts)
	if err != nil {
		t.Fatal(err)
	}
	readMsg(t, c)
	alive.Store(false)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for {
		if _, _, err = c.Read(ctx); err != nil {
			break
		}
	}
	if got := websocket.CloseStatus(err); got != websocket.StatusPolicyViolation {
		t.Fatalf("closed with %d (%v), want policy violation", got, err)
	}
	eventually(t, "the listener gone", func() bool { return r.st.Listeners() == 0 })
}

func TestEventsCarriesRadios(t *testing.T) {
	dir := t.TempDir()
	path := dir + "/radios.json"
	rec := func(label string) string {
		return `{"radios":{"a":{"label":"` + label + `","rigctld_port":4532,"owner":"station","ptt":{"method":"cat"},"rig":{"model":1,"control":"usb"},"cat":null}}}`
	}
	if err := os.WriteFile(path, []byte(rec("One")), 0o644); err != nil {
		t.Fatal(err)
	}
	w := radios.New(path, 10*time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	r := newRig(t)
	r.st.SetRadios(w)
	ts := serve(t, r.st, true)
	c, err := dial(t, ts)
	if err != nil {
		t.Fatal(err)
	}
	if m := readMsg(t, c); string(m["type"]) != `"status"` {
		t.Fatalf("first message %s", m["type"])
	}
	m := readMsg(t, c)
	if string(m["type"]) != `"radios"` || !strings.Contains(string(m["data"]), `"One"`) {
		t.Fatalf("radios message %v", m)
	}
	if err := os.WriteFile(path, []byte(rec("Two")), 0o644); err != nil {
		t.Fatal(err)
	}
	for {
		m = readMsg(t, c)
		if string(m["type"]) == `"radios"` {
			break
		}
	}
	if !strings.Contains(string(m["data"]), `"Two"`) {
		t.Fatalf("change not published: %s", m["data"])
	}
}
