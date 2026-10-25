package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/rig"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/session"
)

type rwRecords map[string]radios.Radio

func (r rwRecords) Get(n string) (radios.Radio, bool) { x, ok := r[n]; return x, ok }

type rwRig struct {
	mu    sync.Mutex
	st    rig.State
	calls []string
}

func (f *rwRig) State() rig.State { f.mu.Lock(); defer f.mu.Unlock(); return f.st }
func (f *rwRig) Write(ctx context.Context, fn func(rig.Commander) error, opt func(*rig.State)) error {
	f.mu.Lock()
	if opt != nil {
		opt(&f.st)
	}
	f.mu.Unlock()
	return fn(f)
}
func (f *rwRig) Do(ctx context.Context, cmd string, args ...string) (rig.Reply, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, strings.TrimSpace(cmd+" "+strings.Join(args, " ")))
	return rig.Reply{}, nil
}

type rwRigs struct{ w *rwRig }

func (r rwRigs) Writer(n string) (rig.Writer, bool) {
	if n != "radio1" {
		return nil, false
	}
	return r.w, true
}
func (r rwRigs) Caps(n string) (rig.Caps, bool) {
	return rig.Caps{ModelName: "IC-7300", Modes: []string{"USB", "CW"}, FreqRanges: []rig.FreqRange{{Lo: 30000, Hi: 74800000}}}, n == "radio1"
}

type rwAuth struct{ ok bool }

func (a rwAuth) Session(r *http.Request) (*Session, bool) {
	return &Session{ID: "sess-secret", User: "dietpi"}, a.ok
}

func radioServer(loggedIn bool) (http.Handler, *rwRig) {
	f := &rwRig{st: rig.State{Connected: true, Freq: 14074000, Mode: "USB", Levels: map[string]float64{}, Meters: map[string]float64{}, Optimistic: map[string]bool{}}}
	api := &RadioAPI{
		Auth:   rwAuth{ok: loggedIn},
		Radios: rwRecords{"radio1": {Name: "radio1", Label: "IC-7300", Port: 4532}},
		Rigs:   rwRigs{f},
		Owners: session.NewOwners(realNow{}),
	}
	h := New(Config{Listen: "127.0.0.1:0", Web: nil, Auth: rwAuth{ok: loggedIn}, Routes: api.Register}).Handler
	return h, f
}

type realNow struct{}

func (realNow) Now() time.Time { return time.Now() }

func rwDo(h http.Handler, method, target, body string, hdr map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, target, strings.NewReader(body))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestRadioRoutes404Unknown(t *testing.T) {
	h, _ := radioServer(true)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/radio/nope"},
		{"POST", "/api/radio/nope/freq"},
		{"POST", "/api/radio/..%2Fetc/stop"},
		{"GET", "/api/radio/Radio1"},
	} {
		rec := rwDo(h, tc.method, tc.path, `{"hz":7074000}`, nil)
		if rec.Code != 404 || !strings.Contains(rec.Body.String(), "no such radio") && !strings.Contains(rec.Body.String(), "not found") {
			t.Fatalf("%s %s: got %d %q", tc.method, tc.path, rec.Code, rec.Body.String())
		}
	}
	rec := rwDo(h, "GET", "/api/radio/nope", "", nil)
	if rec.Body.String() != "{\"error\":\"no such radio\"}\n" {
		t.Fatalf("body %q", rec.Body.String())
	}
}

func TestRadioRoutesRequireSession(t *testing.T) {
	h, f := radioServer(false)
	for _, tc := range []struct{ method, path string }{
		{"GET", "/api/radio/radio1"},
		{"POST", "/api/radio/radio1/freq"},
		{"POST", "/api/radio/radio1/stop"},
	} {
		if rec := rwDo(h, tc.method, tc.path, `{"hz":7074000}`, nil); rec.Code != 401 {
			t.Fatalf("%s %s: got %d", tc.method, tc.path, rec.Code)
		}
	}
	req := httptest.NewRequest("POST", "/api/radio/radio1/freq", strings.NewReader(`{"hz":7074000}`))
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 403 {
		t.Fatalf("cross-site: got %d", rec.Code)
	}
	if len(f.calls) != 0 {
		t.Fatalf("a refused request reached the radio: %v", f.calls)
	}
}

func TestRadioFreqAndRefusal(t *testing.T) {
	h, f := radioServer(true)
	rec := rwDo(h, "POST", "/api/radio/radio1/freq", `{"hz":7074000}`, nil)
	if rec.Code != 200 || strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	if len(f.calls) != 1 || f.calls[0] != "set_freq 7074000" {
		t.Fatalf("calls %v", f.calls)
	}
	rec = rwDo(h, "POST", "/api/radio/radio1/freq", `{"hz":99999999}`, nil)
	var res rig.Result
	_ = json.Unmarshal(rec.Body.Bytes(), &res)
	if res.OK || res.Reason == "" || res.Fix == "" {
		t.Fatalf("got %d %q", rec.Code, rec.Body.String())
	}
	if rec := rwDo(h, "POST", "/api/radio/radio1/freq", `{`, nil); rec.Code != 400 {
		t.Fatalf("bad json: %d", rec.Code)
	}
	if rec := rwDo(h, "POST", "/api/radio/radio1/freq", `{}`, nil); rec.Code != 200 || strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("missing hz: %d %q", rec.Code, rec.Body.String())
	}
}

func TestRadioClaimTuneAndGet(t *testing.T) {
	h, f := radioServer(true)
	tab := map[string]string{TabHeader: "iPad#a1"}
	if rec := rwDo(h, "POST", "/api/radio/radio1/tune", `{"on":true}`, tab); strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("tune before claim: %q", rec.Body.String())
	}
	if rec := rwDo(h, "POST", "/api/radio/radio1/claim", `{"device":"iPad#a1"}`, nil); strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Fatalf("claim: %q", rec.Body.String())
	}
	rec := rwDo(h, "GET", "/api/radio/radio1", "", tab)
	var got struct {
		Radio radios.Radio
		State map[string]any
		Caps  map[string]any
		Owner map[string]any
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil || rec.Code != 200 {
		t.Fatalf("get: %d %v %q", rec.Code, err, rec.Body.String())
	}
	if got.Radio.Name != "radio1" || got.State["freq"] == nil || got.Caps == nil || got.Owner["mine"] != true || got.Owner["label"] != "iPad" {
		t.Fatalf("get body: %q", rec.Body.String())
	}
	if strings.Contains(rec.Body.String(), "sess-secret") {
		t.Fatal("session id leaked")
	}
	rec = rwDo(h, "GET", "/api/radio/radio1", "", map[string]string{TabHeader: "other#b2"})
	if !strings.Contains(rec.Body.String(), `"mine":false`) {
		t.Fatalf("other tab sees mine: %q", rec.Body.String())
	}
	if rec := rwDo(h, "POST", "/api/radio/radio1/tune", `{"on":true}`, tab); strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Fatalf("tune: %q", rec.Body.String())
	}
	if f.calls[len(f.calls)-1] != "set_ptt 1" {
		t.Fatalf("calls %v", f.calls)
	}
	if rec := rwDo(h, "POST", "/api/radio/radio1/stop", `{}`, map[string]string{TabHeader: "other#b2"}); strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Fatalf("stop: %q", rec.Body.String())
	}
}

// otherSession lets a test log in as a second session.
type multiAuth struct{}

func (multiAuth) Session(r *http.Request) (*Session, bool) {
	return &Session{ID: r.Header.Get("X-Test-Session"), User: "dietpi"}, true
}

func TestTuneAndATURefuseWithoutTheirOwnTab(t *testing.T) {
	f := &rwRig{st: rig.State{Connected: true, Mode: "USB", Levels: map[string]float64{}, Meters: map[string]float64{}, Optimistic: map[string]bool{}}}
	owners := session.NewOwners(realNow{})
	api := &RadioAPI{Auth: multiAuth{}, Radios: rwRecords{"radio1": {Name: "radio1", Port: 4532}}, Rigs: rwRigs{f}, Owners: owners}
	h := New(Config{Listen: "127.0.0.1:0", Auth: multiAuth{}, Routes: api.Register}).Handler
	own := map[string]string{TabHeader: "iPad#a1", "X-Test-Session": "s1"}
	if rec := rwDo(h, "POST", "/api/radio/radio1/claim", `{}`, own); strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Fatalf("claim: %q", rec.Body.String())
	}
	cases := map[string]map[string]string{
		"no header":     {"X-Test-Session": "s1"},
		"empty header":  {TabHeader: "", "X-Test-Session": "s1"},
		"other session": {TabHeader: "iPad#a1", "X-Test-Session": "s2"},
		"other tab":     {TabHeader: "iPad#zz", "X-Test-Session": "s1"},
		"unprintable":   {TabHeader: "a\tb", "X-Test-Session": "s1"},
	}
	for name, hdr := range cases {
		for _, route := range []string{"tune", "atu"} {
			rec := rwDo(h, "POST", "/api/radio/radio1/"+route, `{"on":true}`, hdr)
			if strings.Contains(rec.Body.String(), `"ok":true`) {
				t.Fatalf("%s %s: accepted %q", route, name, rec.Body.String())
			}
		}
	}
	for _, c := range f.calls {
		if strings.HasPrefix(c, "set_ptt") || strings.HasPrefix(c, "set_func") {
			t.Fatalf("a refused request keyed the radio: %v", f.calls)
		}
	}
	if rec := rwDo(h, "POST", "/api/radio/radio1/tune", `{"on":true}`, own); strings.TrimSpace(rec.Body.String()) != `{"ok":true}` {
		t.Fatalf("the owner's tune: %q", rec.Body.String())
	}
}

// vanishing records: Get answers from a map the test edits and counts misses.
type vanishRecords struct {
	mu     sync.Mutex
	m      map[string]radios.Radio
	misses int
}

func (v *vanishRecords) Get(n string) (radios.Radio, bool) {
	v.mu.Lock()
	defer v.mu.Unlock()
	r, ok := v.m[n]
	if !ok {
		v.misses++
	}
	return r, ok
}

func (v *vanishRecords) remove(n string) { v.mu.Lock(); delete(v.m, n); v.mu.Unlock() }
func (v *vanishRecords) missCount() int  { v.mu.Lock(); defer v.mu.Unlock(); return v.misses }

func TestOwedUnkeyEndsWhenTheRadioLeavesTheRecords(t *testing.T) {
	f := &rwRig{st: rig.State{Connected: true, Mode: "USB", Levels: map[string]float64{}, Meters: map[string]float64{}, Optimistic: map[string]bool{}}}
	recs := &vanishRecords{m: map[string]radios.Radio{"radio1": {Name: "radio1", Port: 4532}}}
	api := &RadioAPI{Auth: multiAuth{}, Radios: recs, Rigs: rwRigs{f}, Owners: session.NewOwners(realNow{}), Ctx: context.Background()}
	h := New(Config{Listen: "127.0.0.1:0", Auth: multiAuth{}, Routes: api.Register}).Handler
	hdr := map[string]string{TabHeader: "iPad#a1", "X-Test-Session": "s1"}
	rwDo(h, "POST", "/api/radio/radio1/claim", `{}`, hdr)
	if rec := rwDo(h, "POST", "/api/radio/radio1/tune", `{"on":true}`, hdr); !strings.Contains(rec.Body.String(), `"ok":true`) {
		t.Fatalf("tune: %q", rec.Body.String())
	}
	f.mu.Lock()
	f.st.Connected = false
	f.mu.Unlock()
	rwDo(h, "POST", "/api/radio/radio1/stop", `{}`, hdr) // refused; the unkey is owed and retried
	recs.remove("radio1")
	deadline := time.Now().Add(4 * time.Second)
	for recs.missCount() == 0 {
		if time.Now().After(deadline) {
			t.Fatal("the retry loop never consulted the records")
		}
		time.Sleep(20 * time.Millisecond)
	}
	f.mu.Lock()
	f.st.Connected = true
	f.mu.Unlock()
	time.Sleep(1500 * time.Millisecond) // more than one retry period
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.calls {
		if c == "set_ptt 0" {
			t.Fatalf("the retry loop outlived its radio: %v", f.calls)
		}
	}
}
