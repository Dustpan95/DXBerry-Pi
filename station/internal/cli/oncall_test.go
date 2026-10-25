package cli

import (
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/httpapi"
)

func TestCLIHandlerReportsFinishedCalls(t *testing.T) {
	c := newTest(t)
	var got []string
	c.OnCall = func(cmd string, args []string) { got = append(got, cmd+" "+strings.Join(args, " ")) }
	h := httpapi.New(httpapi.Config{Web: fstest.MapFS{"index.html": {Data: []byte("x")}}, Auth: stubAuth{ok: true}, Routes: c.Register}).Handler
	if rec := post(h, "/api/cli/dxberry-status", `{"args":["--json"]}`); rec.Code != 200 {
		t.Fatalf("got %d", rec.Code)
	}
	if rec := post(h, "/api/cli/nonsense", `{"args":["x"]}`); rec.Code != 400 {
		t.Fatalf("got %d", rec.Code)
	}
	if len(got) != 1 || got[0] != "dxberry-status --json" {
		t.Fatalf("OnCall saw %q (a refused call must not count)", got)
	}
}
