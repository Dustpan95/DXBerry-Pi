package status

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/cli"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/httpapi"
)

func TestServiceHealthReadDoesNotQueueAFullReport(t *testing.T) {
	r := newRig(t)
	eventually(t, "the startup cache", func() bool { gen, _, _ := snap(t, r.st); return gen != 0 })
	var calls atomic.Int32
	runner := cli.New("/unused", func(context.Context, string, []string, string, []string) (cli.Result, error) {
		calls.Add(1)
		return cli.Result{Stdout: "active\n", Exit: 0}, nil
	})
	runner.Logf = func(string, ...any) {}
	runner.OnCall = func(command string, args []string) {
		if ChangesState(command, args) {
			r.st.Poke()
		}
	}
	handler := httpapi.New(httpapi.Config{Web: fstest.MapFS{"index.html": {Data: []byte("x")}}, Auth: stubAuth{ok: true}, Routes: runner.Register}).Handler
	call := func(verb, unit string) {
		t.Helper()
		req := httptest.NewRequest(http.MethodPost, "/api/cli/systemctl", strings.NewReader(`{"args":["`+verb+`","`+unit+`"]}`))
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		res := httptest.NewRecorder()
		handler.ServeHTTP(res, req)
		if res.Code != http.StatusOK {
			t.Fatalf("%s returned %d: %s", verb, res.Code, res.Body.String())
		}
	}
	r.clock.Advance(time.Hour)
	for i := 0; i < 20; i++ {
		call("is-active", "graywolf.service")
	}
	if calls.Load() != 20 {
		t.Fatal("health reads must still execute and return their answer")
	}
	never(t, "a full report after read-only service queries", func() bool { return r.count() != 1 })
	for i, action := range []struct{ verb, unit string }{{"start", "graywolf.service"}, {"stop", "graywolf.service"}, {"restart", "graywolf.service"}, {"reload", "nginx.service"}} {
		r.clock.Advance(time.Second)
		call(action.verb, action.unit)
		wantGen := r.clock.Now().UnixMilli()
		// Wait for publication before moving fake time again. Entering the
		// runner alone does not establish the completed attempt's timestamp.
		eventually(t, action.verb+" refresh publication", func() bool {
			gen, _, _ := snap(t, r.st)
			return r.count() == i+2 && gen == wantGen
		})
	}
}
