package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/httpapi"
)

func newTest(t *testing.T) *CLI {
	t.Helper()
	bin, err := filepath.Abs("testdata/bin")
	if err != nil {
		t.Fatal(err)
	}
	c := New(bin, nil)
	c.SystemBin = map[string]string{"systemctl": bin + "/systemctl", "journalctl": bin + "/journalctl"}
	c.Logf = func(string, ...any) {}
	return c
}

func TestCLIRefusesUnknownCommand(t *testing.T) {
	err := newTest(t).Validate(Call{Command: "rm", Args: []string{"-rf", "/"}})
	if err == nil {
		t.Fatal("rm was accepted")
	}
}

func TestCLIRefusesBadArgument(t *testing.T) {
	err := newTest(t).Validate(Call{Command: "dxberry-radio", Args: []string{"set", "x", "../../etc"}})
	if err == nil || !strings.Contains(err.Error(), "argument 3") {
		t.Fatalf("got %v", err)
	}
}

func TestCLIRefusesUnknownUnit(t *testing.T) {
	c := newTest(t)
	if err := c.Validate(Call{Command: "systemctl", Args: []string{"start", "sshd.service"}}); err == nil {
		t.Fatal("sshd.service was accepted")
	}
	if err := c.Validate(Call{Command: "systemctl", Args: []string{"start", "graywolf.service"}}); err != nil {
		t.Fatal(err)
	}
}

func TestCLIKnownUnitsComeFromStatus(t *testing.T) {
	c := newTest(t)
	call := Call{Command: "systemctl", Args: []string{"restart", "ssh.service"}}
	if c.Validate(call) == nil {
		t.Fatal("ssh.service accepted before status listed it")
	}
	if _, err := c.Run(context.Background(), Call{Command: "dxberry-status", Args: []string{"--json"}}); err != nil {
		t.Fatal(err)
	}
	if err := c.Validate(call); err != nil {
		t.Fatalf("after status: %v", err)
	}
}

func TestCLIErrorNeverEchoesLongValue(t *testing.T) {
	long := strings.Repeat("a", 90) + "/../x"
	err := newTest(t).Validate(Call{Command: "dxberry-radio", Args: []string{"remove", long, "--json"}})
	if err == nil || strings.Contains(err.Error(), long) || strings.Contains(err.Error(), "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa") {
		t.Fatalf("got %v", err)
	}
}

func TestCLIRunsFakeStatus(t *testing.T) {
	res, err := newTest(t).Run(context.Background(), Call{Command: "dxberry-status", Args: []string{"--json"}})
	if err != nil || res.Exit != 0 || !strings.Contains(res.Stdout, `"ok":true`) {
		t.Fatalf("got %+v %v", res, err)
	}
}

func TestCLIPassesStdinNotArgs(t *testing.T) {
	res, err := newTest(t).Run(context.Background(), Call{Command: "dxberry-radio", Args: []string{"hotplug", "--json"}, Stdin: "secret-word\n"})
	if err != nil {
		t.Fatal(err)
	}
	if res.Stdout != "2\nsecret-word\n" {
		t.Fatalf("stdout %q", res.Stdout)
	}
}

func TestCLINonZeroExitIsReported(t *testing.T) {
	res, err := newTest(t).Run(context.Background(), Call{Command: "dxberry-config", Args: []string{"get", "--json"}})
	if err != nil || res.Exit != 6 || !strings.Contains(res.Stderr, "[ERROR] bad") {
		t.Fatalf("got %+v %v", res, err)
	}
}

func TestCLIMissingBinaryIsExitMinusOne(t *testing.T) {
	c := New(t.TempDir(), nil)
	res, err := c.Run(context.Background(), Call{Command: "dxberry-status", Args: []string{"--json"}})
	if err != nil || res.Exit != -1 {
		t.Fatalf("got %+v %v", res, err)
	}
}

func TestCLICapsOutput(t *testing.T) {
	c := New("/x", func(ctx context.Context, name string, args []string, stdin string, env []string) (Result, error) {
		return runExec(ctx, "/bin/sh", []string{"-c", "head -c 3000000 /dev/zero | tr '\\0' a"}, "", env)
	})
	res, err := c.Run(context.Background(), Call{Command: "dxberry-status", Args: []string{"--json"}})
	if err != nil || !strings.HasSuffix(res.Stdout, "\n[truncated]") || len(res.Stdout) > maxOutput+20 {
		t.Fatalf("len %d err %v", len(res.Stdout), err)
	}
}

func TestCLIRunnerEnvironment(t *testing.T) {
	var got []string
	c := New("/x", func(ctx context.Context, name string, args []string, stdin string, env []string) (Result, error) {
		got = env
		return Result{}, nil
	})
	if _, err := c.Run(context.Background(), Call{Command: "dxberry-status", Args: []string{"--json"}}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, "|") != "PATH=/usr/sbin:/usr/bin:/sbin:/bin|LANG=C.UTF-8" {
		t.Fatalf("env %v", got)
	}
}

// Every allowlisted command has at least one shape that passes validation.
func TestCLIAcceptsEveryCommand(t *testing.T) {
	c := newTest(t)
	ok := map[string][][]string{
		"dxberry-status":  {{"--json"}},
		"dxberry-radio":   {{"scan", "--json"}, {"status", "--json"}, {"status", "radio1", "--json"}, {"add", "radio1", "--audio", "1", "--cat", "2:1", "--model", "3073", "--label", "IC-7300", "--json"}, {"set", "radio1", "--audio", "none", "--json"}, {"remove", "radio1", "--json"}, {"hotplug", "--json"}, {"claim", "radio1", "wsjtx", "--json"}, {"claim", "radio1", "my_prog", "--json"}, {"release", "radio1", "--json"}, {"models", "--json"}},
		"dxberry-config":  {{"get", "--json"}, {"set", "GPS_BAUD=9600", "POSITION_LOG=on", "--json"}, {"set", "WIFI_COUNTRY=US", "--stdin", "--json"}, {"set", "SSH_PUBKEY=ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIabc+/= dietpi@laptop", "--json"}, {"set", "WIFI_SSID=Dustin's Café #2 & more", "--json"}, {"set", "HOSTNAME=" + strings.Repeat("é", 256), "--json"}, {"set", "SSH_PUBKEY=", "--json"}, {"set", "SSH_PUBKEY=ssh-ed25519 " + strings.Repeat("A", 990) + " c", "--json"}, {"job", "--json"}, {"confirm", "--json"}, {"revert", "--json"}, {"apply", "--json"}},
		"dxberry-update":  {{"check", "--json"}, {"check", "--refresh", "--json"}, {"graywolf", "--json"}, {"dxberry", "--json"}, {"system", "--json"}, {"rollback", "--json"}, {"job", "--json"}, {"app", "wsjtx", "--json"}, {"app", "my_prog", "--json"}, {"prereleases", "on", "--json"}, {"prereleases", "off", "--json"}},
		"dxberry-app":     {{"list", "--json"}, {"install", "wsjtx", "--json"}, {"remove", "wsjtx", "--json"}, {"start", "radio1", "--json"}, {"stop", "radio1", "--json"}, {"screen", "wsjtx-radio1", "--json"}},
		"dxberry-audio":   {{"ensure", "radio1", "--json"}, {"stop", "radio1", "--json"}, {"status", "radio1", "--json"}},
		"dxberry-logbook": {{"get", "--json"}, {"set", "--json"}, {"test", "--json"}, {"recent", "--json"}, {"recent", "25", "--json"}, {"flush", "--json"}},
		"systemctl":       {{"start", "gpsd.service"}, {"stop", "dxberry-screen@wsjtx-radio1.service"}, {"is-active", "dxberry-screen@wsjtx-radio1.service"}, {"restart", "rigctld@radio1.service"}, {"restart", "dxberry-logbook-flush.timer"}, {"reload", "nginx.service"}, {"--no-block", "start", "reboot.target"}, {"--no-block", "start", "poweroff.target"}},
		"journalctl":      {{"-u", "nginx.service", "-n", "200", "--no-pager", "-o", "short-iso"}, {"-b", "-n", "2000", "--no-pager", "-o", "short-iso", "--priority=debug"}},
	}
	for cmd, shapes := range ok {
		for _, a := range shapes {
			if err := c.Validate(Call{Command: cmd, Args: a}); err != nil {
				t.Errorf("%s %v: %v", cmd, a, err)
			}
		}
	}
	bad := map[string][][]string{
		"dxberry-status":  {{}, {"--json", "pi"}},
		"dxberry-radio":   {{"scan"}, {"apply", "--json"}, {"claim", "radio1", "Bad", "--json"}, {"claim", "radio1", "--json"}, {"release", "--json"}, {"release", "a", "b", "--json"}, {"models", "extra", "--json"}, {"add", "Bad", "--json"}, {"add", "r", "--bogus", "1", "--json"}, {"add", "r", "--audio", "--json"}},
		"dxberry-config":  {{"set", "PASSWORD=x", "--json"}, {"set", "wifi=x", "--json"}, {"set", "WIFI_SSID=a\nb", "--json"}, {"set", "WIFI_SSID=" + strings.Repeat("a", 257), "--json"}, {"set", "SSH_PUBKEY=ssh-ed25519 AAAA c;rm -rf", "--json"}, {"set", "SSH_PUBKEY=ssh-ed25519 AAAA $(id)", "--json"}, {"set", "SSH_PUBKEY=ssh-ed25519 " + strings.Repeat("A", 1000) + " c", "--json"}, {"set", "SSH_PUBKEY=ssh-dss AAAA", "--json"}, {"set", "--json"}},
		"dxberry-update":  {{"dxberry", "--version", "1.0", "--json"}, {"apply", "system", "--json"}, {"prereleases", "maybe", "--json"}, {"prereleases", "--json"}},
		"dxberry-app":     {{"start", "a", "b", "--json"}, {"screen", "nodash", "--json"}},
		"dxberry-audio":   {{"ensure", "--json"}, {"ensure", "Bad", "--json"}, {"start", "radio1", "--json"}, {"status", "a", "b", "--json"}},
		"dxberry-logbook": {{"recent", "26", "--json"}, {"submit", "--json"}, {"recent", "0", "--json"}},
		"systemctl":       {{"enable", "gpsd.service"}, {"start", "gpsd"}, {"reload", "sshd.service"}, {"reload", "nginx"}, {"reload", "nginx.service", "x"}, {"reboot"}, {"poweroff"}, {"--no-block", "start", "halt.target"}, {"start", "reboot.target"}},
		"journalctl":      {{"-u", "nginx.service", "-n", "2001", "--no-pager", "-o", "short-iso"}, {"-f"}},
	}
	for cmd, shapes := range bad {
		for _, a := range shapes {
			if err := c.Validate(Call{Command: cmd, Args: a}); err == nil {
				t.Errorf("%s %v was accepted", cmd, a)
			}
		}
	}
}

type stubAuth struct{ ok bool }

func (s stubAuth) Session(r *http.Request) (*httpapi.Session, bool) {
	if !s.ok {
		return nil, false
	}
	return &httpapi.Session{User: "dietpi"}, true
}

func handlerServer(t *testing.T, loggedIn bool) http.Handler {
	t.Helper()
	c := newTest(t)
	return httpapi.New(httpapi.Config{
		Listen: "127.0.0.1:0", Web: fstest.MapFS{"index.html": {Data: []byte("x")}},
		Auth: stubAuth{ok: loggedIn}, Routes: c.Register,
	}).Handler
}

func post(h http.Handler, target, body string) *httptest.ResponseRecorder {
	req := httptest.NewRequest("POST", target, strings.NewReader(body))
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestCLIHandlerRequiresSession(t *testing.T) {
	rec := post(handlerServer(t, false), "/api/cli/dxberry-status", `{"args":["--json"]}`)
	if rec.Code != 401 {
		t.Fatalf("got %d", rec.Code)
	}
}

func TestCLIHandlerRunsAndRefuses(t *testing.T) {
	h := handlerServer(t, true)
	rec := post(h, "/api/cli/dxberry-config", `{"args":["get","--json"]}`)
	var out struct {
		Exit           int
		Stdout, Stderr string
	}
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &out) != nil || out.Exit != 6 || !strings.Contains(out.Stderr, "bad") {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
	for _, tc := range []struct{ path, body string }{
		{"/api/cli/rm", `{"args":["-rf"]}`},
		{"/api/cli/dxberry-radio", `{"args":["set","x","../../etc"]}`},
		{"/api/cli/dxberry-status", `not json`},
	} {
		rec = post(h, tc.path, tc.body)
		if rec.Code != 400 || !strings.Contains(rec.Body.String(), `"error"`) {
			t.Fatalf("%s: got %d %s", tc.path, rec.Code, rec.Body)
		}
	}
}

func TestCLIHandlerLogsWithoutStdin(t *testing.T) {
	var sb strings.Builder
	c := newTest(t)
	c.Logf = func(f string, a ...any) { sb.WriteString(strings.TrimSpace(fmt.Sprintf(f, a...)) + "\n") }
	mux := http.NewServeMux()
	c.Register(mux)
	req := httptest.NewRequest("POST", "/api/cli/dxberry-radio", strings.NewReader(`{"args":["hotplug","--json"],"stdin":"topsecret"}`))
	mux.ServeHTTP(httptest.NewRecorder(), req)
	if strings.Contains(sb.String(), "topsecret") || !strings.HasPrefix(sb.String(), "cli dxberry-radio hotplug ") || !strings.Contains(sb.String(), "exit=0 ms=") {
		t.Fatalf("log %q", sb.String())
	}
}

func TestCLITimeoutKillsChild(t *testing.T) {
	c := New("/x", func(ctx context.Context, name string, args []string, stdin string, env []string) (Result, error) {
		return runExec(ctx, "/bin/sh", []string{"-c", "exec sleep 5"}, "", env)
	})
	c.Timeout = 200 * time.Millisecond
	start := time.Now()
	res, err := c.Run(context.Background(), Call{Command: "dxberry-status", Args: []string{"--json"}})
	if err != nil || res.Exit == 0 || !strings.Contains(res.Stderr, "[timed out]") || time.Since(start) > 3*time.Second {
		t.Fatalf("got %+v %v after %v", res, err, time.Since(start))
	}
}

func TestCLIBackgroundChildKeepsRealExit(t *testing.T) {
	c := New("/x", func(ctx context.Context, name string, args []string, stdin string, env []string) (Result, error) {
		return runExec(ctx, "/bin/sh", []string{"-c", "sleep 4 & exit 3"}, "", env)
	})
	res, err := c.Run(context.Background(), Call{Command: "dxberry-status", Args: []string{"--json"}})
	if err != nil || res.Exit != 3 {
		t.Fatalf("got %+v %v", res, err)
	}
}

func TestCLIHandlerLogsRefusal(t *testing.T) {
	var sb strings.Builder
	c := newTest(t)
	c.Logf = func(f string, a ...any) { sb.WriteString(fmt.Sprintf(f, a...)) }
	mux := http.NewServeMux()
	c.Register(mux)
	req := httptest.NewRequest("POST", "/api/cli/dxberry-radio", strings.NewReader(`{"args":["set","x","../../etc"]}`))
	mux.ServeHTTP(httptest.NewRecorder(), req)
	if !strings.HasPrefix(sb.String(), "cli refused ") || !strings.Contains(sb.String(), "pos=3") {
		t.Fatalf("log %q", sb.String())
	}
}

func TestCLIExitZeroWithLeftoverChild(t *testing.T) {
	old := waitDelay
	waitDelay = 100 * time.Millisecond
	defer func() { waitDelay = old }()
	c := New("/x", func(ctx context.Context, name string, args []string, stdin string, env []string) (Result, error) {
		return runExec(ctx, "/bin/sh", []string{"-c", "sleep 4 & exit 0"}, "", env)
	})
	res, err := c.Run(context.Background(), Call{Command: "dxberry-status", Args: []string{"--json"}})
	if err != nil || res.Exit != 0 {
		t.Fatalf("got %+v %v", res, err)
	}
}

func TestCLIRefusalNeverLeaksValues(t *testing.T) {
	var sb strings.Builder
	c := newTest(t)
	c.Logf = func(f string, a ...any) { sb.WriteString(fmt.Sprintf(f, a...) + "\n") }
	mux := http.NewServeMux()
	c.Register(mux)
	for _, body := range []string{
		`{"args":["set","PASSWORD=hunter2","--json"]}`,
		`{"args":["set","SSH_PUBKEY=ssh-ed25519 AAAA hunter2;x","--json"]}`,
		`{"args":["set","hunter2","--json"]}`,
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/cli/dxberry-config", strings.NewReader(body)))
		if rec.Code != 400 || strings.Contains(rec.Body.String(), "hunter2") {
			t.Fatalf("%s: %d %s", body, rec.Code, rec.Body)
		}
	}
	if strings.Contains(sb.String(), "hunter2") {
		t.Fatalf("log leaks: %q", sb.String())
	}
	if !strings.HasPrefix(sb.String(), "cli refused dxberry-config pos=2 reason=dxberry-config: argument 2 does not match KEYVALUE\n") {
		t.Fatalf("log %q", sb.String())
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/cli/dxberry-config", strings.NewReader(`{"args":["set","PASSWORD=hunter2","--json"]}`)))
	if !strings.Contains(rec.Body.String(), "key PASSWORD") {
		t.Fatalf("body %s", rec.Body)
	}
}

func TestCLIRefusalDoesNotEchoStrayArguments(t *testing.T) {
	var sb strings.Builder
	c := newTest(t)
	c.Logf = func(f string, a ...any) { sb.WriteString(fmt.Sprintf(f, a...) + "\n") }
	mux := http.NewServeMux()
	c.Register(mux)
	for _, tc := range []struct{ path, body string }{
		{"/api/cli/dxberry-config", `{"args":["get","PASSWORD=hunter2","--json"]}`},
		{"/api/cli/dxberry-radio", `{"args":["models","PASSWORD=hunter2","--json"]}`},
	} {
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, httptest.NewRequest("POST", tc.path, strings.NewReader(tc.body)))
		if rec.Code != 400 || strings.Contains(rec.Body.String(), "hunter2") {
			t.Fatalf("%s: %d %s", tc.body, rec.Code, rec.Body)
		}
	}
	if strings.Contains(sb.String(), "hunter2") {
		t.Fatalf("log leaks: %q", sb.String())
	}
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, httptest.NewRequest("POST", "/api/cli/rm", strings.NewReader(`{"args":[]}`)))
	if rec.Code != 400 || strings.TrimSpace(rec.Body.String()) != `{"error":"unknown command"}` {
		t.Fatalf("got %d %s", rec.Code, rec.Body)
	}
}

// A browser that goes away mid-call must not kill the command: the child gets SIGTERM at the
// deadline (so its traps run), not SIGKILL when the request is cancelled.
func TestCLIClientDisconnectDoesNotKill(t *testing.T) {
	dir := t.TempDir()
	started, termed := dir+"/started", dir+"/termed"
	script := "trap 'echo term > " + termed + "; exit 0' TERM; echo go > " + started + "; while :; do sleep 0.05; done"
	c := New("/x", func(ctx context.Context, name string, args []string, stdin string, env []string) (Result, error) {
		return runExec(ctx, "/bin/sh", []string{"-c", script}, "", env)
	})
	c.Logf = func(string, ...any) {}
	c.Timeout = 1500 * time.Millisecond
	mux := http.NewServeMux()
	c.Register(mux)
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest("POST", "/api/cli/dxberry-status", strings.NewReader(`{"args":["--json"]}`)).WithContext(ctx)
	rec := httptest.NewRecorder()
	done := make(chan struct{})
	go func() { mux.ServeHTTP(rec, req); close(done) }()
	exists := func(p string) bool { _, err := os.Stat(p); return err == nil }
	for i := 0; i < 100 && !exists(started); i++ {
		time.Sleep(20 * time.Millisecond)
	}
	if !exists(started) {
		t.Fatal("the command never started")
	}
	cancel() // the browser leaves
	time.Sleep(400 * time.Millisecond)
	select {
	case <-done:
		t.Fatal("the call ended when the request was cancelled")
	default:
	}
	if exists(termed) {
		t.Fatal("the command was signalled at cancel, not at the deadline")
	}
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the call outlived its deadline")
	}
	if !exists(termed) {
		t.Fatalf("the command was not given SIGTERM at the deadline (answer %s)", rec.Body)
	}
}
