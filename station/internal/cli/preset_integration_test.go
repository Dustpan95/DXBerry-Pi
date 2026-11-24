//go:build presetintegration

package cli_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/cli"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/httpapi"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/presets"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/status"
)

type presetFixtureAuth bool

func (a presetFixtureAuth) Session(*http.Request) (*httpapi.Session, bool) {
	if !a {
		return nil, false
	}
	return &httpapi.Session{User: "fixture", ID: "fixture-session"}, true
}

// This opt-in check executes the actual helper through the production HTTP
// middleware/runner, with only an empty private tmpfs as station state.
func TestPresetHTTPActualCLI(t *testing.T) {
	const state = "/var/lib/dxberry/station"
	if os.Getenv("DXB_PRESET_INTEGRATION") != "1" || os.Geteuid() != 0 {
		t.Fatal("requires explicit isolated Docker root fixture")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("requires Docker")
	}
	interfaces, err := net.Interfaces()
	if err != nil {
		t.Fatal(err)
	}
	for _, i := range interfaces {
		if i.Flags&net.FlagLoopback == 0 {
			t.Fatal("requires --network none")
		}
	}
	for _, p := range []string{"/dev/snd", "/dev/ttyUSB*", "/dev/ttyACM*", "/dev/gpiochip*", "/dev/bus/usb"} {
		matches, err := filepath.Glob(p)
		if err != nil || len(matches) > 0 {
			t.Fatal("hardware must not be mounted")
		}
	}
	var fs syscall.Statfs_t
	if err := syscall.Statfs(state, &fs); err != nil || fs.Type != 0x01021994 {
		t.Fatal("station state must be a private empty tmpfs", err)
	}
	info, err := os.Stat(state)
	if err != nil || info.Mode().Perm() != 0700 {
		t.Fatal("station tmpfs must be private", err)
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 {
		t.Fatal("station tmpfs must be root-owned")
	}
	entries, err := os.ReadDir(state)
	if err != nil || len(entries) != 0 {
		t.Fatal("refusing nonempty station fixture", err)
	}
	if err := os.WriteFile(filepath.Join(state, "unrelated"), []byte("retain fixture settings\n"), 0600); err != nil {
		t.Fatal(err)
	}
	runner := cli.New("/fixture", nil)
	if err := runner.SetPresetState(state); err != nil {
		t.Fatal(err)
	}
	var calls, pokes atomic.Int64
	var mu sync.Mutex
	var logs []string
	runner.OnCall = func(command string, args []string) {
		calls.Add(1)
		if status.ChangesState(command, args) {
			pokes.Add(1)
		}
	}
	runner.Logf = func(f string, args ...any) {
		mu.Lock()
		defer mu.Unlock()
		logs = append(logs, fmt.Sprintf(f, args...))
	}
	srv := httptest.NewServer(httpapi.New(httpapi.Config{Auth: presetFixtureAuth(true), Routes: runner.Register}).Handler)
	defer srv.Close()
	client := &http.Client{Timeout: 4 * time.Second}
	post := func(url string, body []byte, foreign bool) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest("POST", url+"/api/cli/dxberry-preset", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		if foreign {
			req.Header.Set("Sec-Fetch-Site", "cross-site")
			req.Header.Set("Origin", "https://foreign.example")
		}
		res, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer res.Body.Close()
		raw, err := io.ReadAll(io.LimitReader(res.Body, 256<<10))
		if err != nil {
			t.Fatal(err)
		}
		if res.StatusCode == 200 && res.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("metadata response is cacheable")
		}
		return res.StatusCode, raw
	}
	type reply struct {
		OK      bool             `json:"ok"`
		Library *presets.Library `json:"library"`
		Code    string           `json:"code"`
	}
	body := func(r presets.Request) []byte {
		t.Helper()
		inner, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		outer, err := json.Marshal(map[string]any{"args": []string{"--json"}, "stdin": string(inner)})
		if err != nil {
			t.Fatal(err)
		}
		return outer
	}
	call := func(r presets.Request, exit int) reply {
		t.Helper()
		code, raw := post(srv.URL, body(r), false)
		if code != 200 {
			t.Fatalf("HTTP %d: %s", code, raw)
		}
		var envelope struct {
			Exit   int    `json:"exit"`
			Stdout string `json:"stdout"`
			Stderr string `json:"stderr"`
		}
		if err := json.Unmarshal(raw, &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Exit != exit || envelope.Stderr != "" {
			t.Fatalf("CLI exit=%d stderr=%q, wanted %d", envelope.Exit, envelope.Stderr, exit)
		}
		var rpl reply
		if err := json.Unmarshal([]byte(envelope.Stdout), &rpl); err != nil {
			t.Fatal(err)
		}
		return rpl
	}
	list := presets.Request{Version: 1, Operation: "list", Radio: "radio1"}
	rpl := call(list, 0)
	if !rpl.OK || rpl.Library == nil || rpl.Library.Revision != presets.Absent {
		t.Fatal("missing state not explicit", rpl)
	}
	if _, err := os.Stat(filepath.Join(state, "presets")); !os.IsNotExist(err) {
		t.Fatal("HTTP list created storage")
	}
	create := presets.Request{Version: 1, Operation: "create", Radio: "radio1", ExpectedRevision: presets.Absent, Label: "private HTTP fixture label", FrequencyHz: 14074000}
	rpl = call(create, 0)
	if !rpl.OK || rpl.Library == nil || len(rpl.Library.Presets) != 1 {
		t.Fatal("HTTP create failed", rpl)
	}
	lib := *rpl.Library
	rpl = call(create, 4)
	if rpl.OK || rpl.Code != "conflict" || rpl.Library != nil {
		t.Fatal("conflict returned old library as success", rpl)
	}
	update := presets.Request{Version: 1, Operation: "update", Radio: "radio1", ExpectedRevision: lib.Revision, ID: lib.Presets[0].ID, Label: "renamed fixture", FrequencyHz: 7100000}
	rpl = call(update, 0)
	if rpl.Library == nil || rpl.Library.Presets[0].FrequencyHz != 7100000 {
		t.Fatal("HTTP update failed", rpl)
	}
	lib = *rpl.Library
	rpl = call(presets.Request{Version: 1, Operation: "delete", Radio: "radio1", ExpectedRevision: lib.Revision, ID: lib.Presets[0].ID}, 0)
	if rpl.Library == nil || rpl.Library.Presets == nil || len(rpl.Library.Presets) != 0 {
		t.Fatal("HTTP delete failed", rpl)
	}
	before, err := os.ReadFile(filepath.Join(state, "presets/radio1.json"))
	if err != nil {
		t.Fatal(err)
	}
	finished := calls.Load()
	if finished != 5 {
		t.Fatal("finished-call hook was not exercised", finished)
	}
	for _, raw := range [][]byte{[]byte(`{"args":["--json","--state","/tmp/other"],"stdin":"{}"}`), []byte(`{"args":["--json"],"stdin":"{\"version\":1,\"operation\":\"recall\",\"radio\":\"radio1\"}"}`), append(body(list), []byte(` {}`)...)} {
		if code, _ := post(srv.URL, raw, false); code != 400 {
			t.Fatal("invalid request accepted", code)
		}
	}
	if code, _ := post(srv.URL, body(list), true); code != 403 {
		t.Fatal("cross-site request accepted", code)
	}
	loggedOut := httptest.NewServer(httpapi.New(httpapi.Config{Auth: presetFixtureAuth(false), Routes: runner.Register}).Handler)
	if code, _ := post(loggedOut.URL, body(list), false); code != 401 {
		loggedOut.Close()
		t.Fatal("logged-out request accepted", code)
	}
	loggedOut.Close()
	if calls.Load() != finished || pokes.Load() != 0 {
		t.Fatal("refusal started helper or preset woke full reporting", calls.Load(), finished, pokes.Load())
	}
	after, err := os.ReadFile(filepath.Join(state, "presets/radio1.json"))
	if err != nil || !bytes.Equal(before, after) {
		t.Fatal("refusal changed saved definitions", err)
	}
	if unrelated, err := os.ReadFile(filepath.Join(state, "unrelated")); err != nil || string(unrelated) != "retain fixture settings\n" {
		t.Fatal("unrelated station state changed", err)
	}
	mu.Lock()
	defer mu.Unlock()
	for _, line := range logs {
		if strings.Contains(line, "private HTTP") || strings.Contains(line, state) || strings.Contains(line, "frequency_hz") {
			t.Fatal("private stdin/path reached logs")
		}
	}
	t.Log("actual CLI HTTP CRUD/conflict, station middleware, fixed state path, no report refresh, and unchanged unrelated state passed; no radio acceptance")
}
