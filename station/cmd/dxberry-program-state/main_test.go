package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/programstate"
)

func commandResponse(t *testing.T, args []string, uid int, advance advanceFunc) (response, int) {
	t.Helper()
	var out bytes.Buffer
	code := run(args, &out, uid, advance)
	var r response
	if json.Unmarshal(out.Bytes(), &r) != nil || r.Protocol != 1 {
		t.Fatalf("invalid response: %q", out.String())
	}
	return r, code
}

func TestProgramStateCommandRefusesPrivilegeAndUnsupportedArguments(t *testing.T) {
	called := 0
	f := func(context.Context, string, bool) error { called++; return nil }
	if r, code := commandResponse(t, []string{"prepare-advance"}, 65534, f); code != 6 || r.OK {
		t.Fatal(r, code)
	}
	if r, code := commandResponse(t, []string{"advance"}, 65534, f); code != 6 || r.OK || r.Code != "unavailable" {
		t.Fatal(r, code)
	}
	for _, args := range [][]string{nil, {"read"}, {"start"}, {"stop"}, {"recall"}, {"reset"}, {"advance", "--state", "/private/path"}, {"--state", "relative", "advance"}, {"--state", "/", "advance"}, {"--state", "/tmp/../tmp", "advance"}, {"--state", "/tmp/", "advance"}, {"--state", "/tmp/a\x00b", "advance"}, {"--state", "/one", "--state", "/two", "advance"}, {"--json", "advance"}, {"advance", "{\"command\":\"start\"}"}} {
		if r, code := commandResponse(t, args, 0, f); code != 2 || r.OK || r.Code != "invalid" || strings.Contains(r.Reason, "/private/path") {
			t.Fatalf("%q: %+v %d", args, r, code)
		}
	}
	if called != 0 {
		t.Fatal("refused input reached filesystem callback")
	}
}

func TestProgramStateCommandUsesOnlyExplicitStateAndPrivateResponse(t *testing.T) {
	t.Setenv("DXB_STATE_DIR", "/ignored/private")
	t.Setenv("DXB_PROGRAM_STATE", "/also/ignored")
	for _, tc := range []struct {
		args    []string
		state   string
		prepare bool
	}{{[]string{"advance"}, "/var/lib/dxberry/station", false}, {[]string{"--state", "/trusted/fixture", "advance"}, "/trusted/fixture", false}, {[]string{"--state", "/trusted/fixture", "prepare-advance"}, "/trusted/fixture", true}} {
		calls := 0
		r, code := commandResponse(t, tc.args, 0, func(ctx context.Context, state string, prepare bool) error {
			calls++
			if state != tc.state || prepare != tc.prepare || ctx.Err() != nil {
				t.Fatal("wrong trusted state/context")
			}
			return nil
		})
		if code != 0 || !r.OK || r.Protocol != 1 || r.Code != "" || r.Reason != "" || calls != 1 {
			t.Fatal(r, code, calls)
		}
		b, _ := json.Marshal(r)
		if string(b) != `{"ok":true,"protocol":1}` {
			t.Fatalf("unexpected wire fields: %s", b)
		}
	}
}

func TestProgramStateCommandMapsErrorsWithoutExposingPrivateDetails(t *testing.T) {
	for _, tc := range []struct {
		err  error
		code int
		name string
	}{{errors.New("/private/failure"), 6, "unavailable"}, {context.DeadlineExceeded, 6, "unavailable"}, {errors.Join(programstate.ErrUncertain, errors.New("private generation")), 7, "uncertain"}} {
		r, code := commandResponse(t, []string{"advance"}, 0, func(context.Context, string, bool) error { return tc.err })
		if code != tc.code || r.OK || r.Code != tc.name || strings.Contains(r.Reason, "private") {
			t.Fatal(r, code)
		}
	}
}

type failedOutput struct{}

func (failedOutput) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestProgramStateCommandLostSuccessResponseIsUncertain(t *testing.T) {
	called := 0
	code := run([]string{"advance"}, failedOutput{}, 0, func(context.Context, string, bool) error { called++; return nil })
	if code != 7 || called != 1 {
		t.Fatal("lost publication response treated as unchanged", code, called)
	}
}

func TestProgramStateCommandBoundsAnUnresponsiveWorker(t *testing.T) {
	release := make(chan struct{})
	defer close(release)
	start := time.Now()
	r, code := commandResponse(t, []string{"advance"}, 0, func(context.Context, string, bool) error { <-release; return nil })
	if code != 7 || r.Code != "uncertain" || r.OK || time.Since(start) > commandLimit+time.Second {
		t.Fatal("unresponsive worker not bounded", r, code)
	}
}
