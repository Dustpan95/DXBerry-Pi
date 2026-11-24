//go:build wsjtxcompat && wsjtxmanager && wsjtxscreen

package wsjtx

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

const consoleFixtureURL = "http://127.0.0.1:18090"

func TestWSJTXConsoleDaemonCompatibility(t *testing.T) {
	runWSJTXScreenFixture(t, screenFixtureOptions{nativeRouting: true, consoleCLI: true, processCrash: true, consoleDaemon: true})
}

// Only real HTTP responses from the complete executable implement this service.
// No fixture control pipe is used after replacing the routing-server process.
type consoleDaemonFixture struct {
	ctx      context.Context
	client   *http.Client
	cookie   *http.Cookie
	password string
}

func (d *consoleDaemonFixture) request(ctx context.Context, method, path, body string, cookie *http.Cookie, site string) (int, []byte, []*http.Cookie, error) {
	request, err := http.NewRequestWithContext(ctx, method, consoleFixtureURL+path, strings.NewReader(body))
	if err != nil {
		return 0, nil, nil, err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Sec-Fetch-Site", site)
	if cookie != nil {
		// The actual Secure cookie is carried explicitly to the loopback HTTP
		// backend. This is not browser cookie transport or nginx/TLS evidence.
		request.AddCookie(cookie)
	}
	response, err := d.client.Do(request)
	if err != nil {
		return 0, nil, nil, err
	}
	defer response.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(response.Body, maxProgramFileBytes+1))
	if err != nil || len(raw) > maxProgramFileBytes {
		return 0, nil, nil, errors.Join(ErrProgramController, err)
	}
	return response.StatusCode, raw, response.Cookies(), nil
}

func (d *consoleDaemonFixture) login(t *testing.T, password string, expected int) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"user": "fixtureadmin", "password": password})
	if err != nil {
		t.Fatal(err)
	}
	status, _, cookies, err := d.request(d.ctx, "POST", "/api/auth/login", string(body), nil, "same-origin")
	if err != nil || status != expected {
		t.Fatal("actual PAM login result differs:", status, err)
	}
	if expected != http.StatusOK {
		if len(cookies) != 0 {
			t.Fatal("failed actual PAM login issued a cookie")
		}
		return
	}
	if len(cookies) != 1 || cookies[0].Value == "" || cookies[0].Path != "/" || !cookies[0].Secure || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("actual authenticated session cookie policy differs")
	}
	d.cookie = cookies[0]
}

func (d *consoleDaemonFixture) CollectionState(ctx context.Context, radio string) (ProgramCollectionState, error) {
	if radio != "fixture1" {
		return ProgramCollectionState{}, ErrInvalidSession
	}
	status, raw, _, err := d.request(ctx, "GET", "/api/radio/fixture1/wsjtx", "", d.cookie, "same-origin")
	var state ProgramCollectionState
	if err != nil || status != http.StatusOK || json.Unmarshal(raw, &state) != nil {
		return state, errors.Join(ErrProgramController, err)
	}
	return state, nil
}

func (d *consoleDaemonFixture) configure(ctx context.Context, radio, action, body string) ([]byte, error) {
	if radio != "fixture1" {
		return nil, ErrInvalidSession
	}
	status, raw, _, err := d.request(ctx, "POST", "/api/radio/fixture1/wsjtx/"+action, body, d.cookie, "same-origin")
	if err != nil {
		return nil, err
	}
	if status == http.StatusOK {
		return raw, nil
	}
	var failure struct{ Code string }
	if status == http.StatusConflict && json.Unmarshal(raw, &failure) == nil && failure.Code == "program-running" {
		return nil, ErrProgramLifecycle
	}
	return nil, ErrProgramController
}

func (d *consoleDaemonFixture) Preview(ctx context.Context, radio string) (ProgramRoutingPreview, error) {
	raw, err := d.configure(ctx, radio, "preview", "{}")
	var value ProgramRoutingPreview
	if err == nil {
		err = json.Unmarshal(raw, &value)
	}
	return value, err
}

func (d *consoleDaemonFixture) Enable(ctx context.Context, radio, fingerprint string) error {
	body, err := json.Marshal(map[string]string{"fingerprint": fingerprint})
	if err == nil {
		_, err = d.configure(ctx, radio, "enable", string(body))
	}
	return err
}

func (d *consoleDaemonFixture) Disable(ctx context.Context, radio string) error {
	_, err := d.configure(ctx, radio, "disable", "{}")
	return err
}

func (d *consoleDaemonFixture) Recover(ctx context.Context, radio string) error {
	_, err := d.configure(ctx, radio, "recover", "{}")
	return err
}

func fixtureManagerCommand(ctx context.Context, args ...string) (string, error) {
	callCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	command := exec.CommandContext(callCtx, "/usr/bin/systemctl", args...)
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	command.WaitDelay = 200 * time.Millisecond
	var output applicationOutput
	command.Stdout, command.Stderr = &output, &output
	err := command.Run()
	return output.text(), err
}

func (d *consoleDaemonFixture) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	_, err := fixtureManagerCommand(ctx, "stop", "dxberryd.service")
	d.client.CloseIdleConnections()
	return err
}

func (d *consoleDaemonFixture) ready(t *testing.T, previous string, restarts string) programUnit {
	t.Helper()
	for deadline := time.Now().Add(15 * time.Second); time.Now().Before(deadline); {
		raw, err := showProgramUnit(d.ctx, "dxberryd.service")
		unit, parseErr := parseProgramUnit(raw)
		if err == nil && parseErr == nil && unit.load == "loaded" && unit.active == "active" && unit.sub == "running" && processID(unit.pid) && unit.pid != previous && programInvocation(unit.invocation) {
			status, _, _, err := d.request(d.ctx, "GET", "/api/auth/check", "", nil, "same-origin")
			if err == nil && status == http.StatusUnauthorized {
				count, err := fixtureManagerCommand(d.ctx, "show", "--property=NRestarts", "--value", "dxberryd.service")
				if err != nil || strings.TrimSpace(count) != restarts {
					t.Fatal("unexpected actual daemon restart count:", count, err)
				}
				fixtureVerifyConsoleProcess(t, unit)
				return unit
			}
		}
		time.Sleep(100 * time.Millisecond)
	}
	t.Fatal("complete console daemon did not become ready with a fresh manager identity")
	return programUnit{}
}

func fixtureVerifyConsoleProcess(t *testing.T, unit programUnit) {
	t.Helper()
	reference, err := os.Stat("/opt/dxberry/bin/dxberryd")
	if err != nil {
		t.Fatal(err)
	}
	actual, err := os.Stat(filepath.Join("/proc", unit.pid, "exe"))
	if err != nil || !os.SameFile(reference, actual) {
		t.Fatal("real daemon unit is not running the complete console executable")
	}
	status, err := os.ReadFile(filepath.Join("/proc", unit.pid, "status"))
	if err != nil || !bytes.Contains(status, []byte("\nUid:\t0\t0\t0\t0\n")) {
		t.Fatal("complete daemon is not actually running as root")
	}
	members, err := os.ReadFile(filepath.Join("/sys/fs/cgroup", unit.cgroup, "cgroup.procs"))
	if err != nil || !strings.Contains(" "+strings.Join(strings.Fields(string(members)), " ")+" ", " "+unit.pid+" ") {
		t.Fatal("complete daemon is not in its actual manager cgroup")
	}
	socket, err := os.Lstat(filepath.Join(routingFixtureRuntime, "wsjtx.sock"))
	if err != nil || socket.Mode()&os.ModeSocket == 0 || socket.Mode().Perm() != 0600 || socket.Sys().(*syscall.Stat_t).Uid != 0 {
		t.Fatal("actual console root hook socket is unavailable")
	}
	listeners := 0
	for _, path := range []string{"/proc/net/tcp", "/proc/net/tcp6"} {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, line := range strings.Split(string(raw), "\n")[1:] {
			fields := strings.Fields(line)
			if len(fields) <= 9 || fields[3] != "0A" {
				continue
			}
			if fields[1] != fmt.Sprintf("0100007F:%04X", 18090) || fields[7] != "0" {
				t.Fatal("unexpected listener entered the isolated console guest")
			}
			fds, err := os.ReadDir(filepath.Join("/proc", unit.pid, "fd"))
			if err != nil {
				t.Fatal(err)
			}
			owned := false
			for _, fd := range fds {
				link, err := os.Readlink(filepath.Join("/proc", unit.pid, "fd", fd.Name()))
				owned = owned || err == nil && link == "socket:["+fields[9]+"]"
			}
			if !owned {
				t.Fatal("loopback HTTP listener is not owned by the actual console process")
			}
			listeners++
		}
	}
	if listeners != 1 {
		t.Fatal("one actual private loopback HTTP listener required")
	}
}

func (f *routingScreenFixture) replaceWithConsoleDaemon(t *testing.T, ctx context.Context) {
	t.Helper()
	before, err := showProgramUnit(ctx, "dxberry-screen@wsjtx-fixture1.service")
	if err != nil {
		t.Fatal(err)
	}
	view, err := f.bridge.Inspect(ctx, "fixture1")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"/run/dxb-console", "/etc/systemd/system/dxberryd.service.d"} {
		if err := os.Mkdir(dir, 0755); err != nil {
			t.Fatal("fresh console guest required:", err)
		}
	}
	records := `{"radios":{"fixture1":{"label":"Synthetic fixture","owner":"wsjtx","cat":null,"rigctld_port":0,"ptt":{"method":"none"}}}}` + "\n"
	if err := os.WriteFile("/run/dxb-console/radios.json", []byte(records), 0600); err != nil {
		t.Fatal(err)
	}
	template, err := os.ReadFile("/fixture/dxberryd.service")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile("/etc/systemd/system/dxberryd.service", template, 0644); err != nil {
		t.Fatal(err)
	}
	dropin := "[Service]\nExecStart=\nExecStart=/opt/dxberry/bin/dxberryd -listen 127.0.0.1:18090 -state /var/lib/dxb-native-state -bin /opt/dxberry/bin -radios /run/dxb-console/radios.json -tls /run/dxb-console/tls -screens /run/dxb-screen/screens -audio-run /run/dxb-console/no-audio -program-run /run/dxb-native-hooks -program-release-run /run/dxb-console\n"
	if err := os.WriteFile("/etc/systemd/system/dxberryd.service.d/fixture.conf", []byte(dropin), 0644); err != nil {
		t.Fatal(err)
	}
	d := &consoleDaemonFixture{ctx: ctx, password: "Fixture-" + rand.Text() + "-7", client: &http.Client{Timeout: 35 * time.Second,
		Transport: &http.Transport{Proxy: nil, DisableKeepAlives: true}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}
	password := exec.CommandContext(ctx, "/usr/sbin/chpasswd")
	password.Stdin = strings.NewReader("fixtureadmin:" + d.password + "\n")
	if err := password.Run(); err != nil {
		t.Fatal("disposable real PAM account password setup failed:", err)
	}
	f.process.killAndWait(t)
	f.process, f.service = nil, nil
	f.console, f.service = d, d
	if out, err := fixtureManagerCommand(ctx, "daemon-reload"); err != nil {
		t.Fatal("actual console unit reload failed:", err, out)
	}
	if out, err := fixtureManagerCommand(ctx, "start", "dxberryd.service"); err != nil {
		t.Fatal("actual console unit start failed:", err, out)
	}
	first := d.ready(t, "", "0")
	d.login(t, d.password+"wrong", http.StatusUnauthorized)
	d.login(t, d.password, http.StatusOK)
	f.checkConsoleRecovery(t, ctx)
	status, _, _, err := d.request(ctx, "POST", "/api/radio/fixture1/wsjtx/recover", "{}", d.cookie, "cross-site")
	if err != nil || status != http.StatusForbidden {
		t.Fatal("real authenticated HTTP accepted a foreign-origin mutation:", status, err)
	}
	pid, err := strconv.Atoi(first.pid)
	if err != nil {
		t.Fatal(err)
	}
	pidfd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		t.Fatal("actual daemon kernel process handle unavailable:", err)
	}
	defer unix.Close(pidfd)
	oldCookie := d.cookie
	if out, err := fixtureManagerCommand(ctx, "kill", "--kill-whom=main", "--signal=SIGKILL", "dxberryd.service"); err != nil {
		t.Fatal("actual manager daemon crash injection failed:", err, out)
	}
	poll := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
	for deadline := time.Now().Add(5 * time.Second); ; {
		remaining := time.Until(deadline)
		if remaining <= 0 {
			t.Fatal("old actual console process did not exit after SIGKILL")
		}
		n, err := unix.Poll(poll, int((remaining+time.Millisecond-1)/time.Millisecond))
		if errors.Is(err, syscall.EINTR) {
			continue
		}
		if err != nil || n != 1 || poll[0].Revents&unix.POLLIN == 0 {
			t.Fatal("old actual console process exit could not be verified:", n, err)
		}
		break
	}
	second := d.ready(t, first.pid, "1")
	if second.invocation == first.invocation {
		t.Fatal("automatic daemon restart retained the old manager invocation")
	}
	status, _, _, err = d.request(ctx, "GET", "/api/radio/fixture1/wsjtx", "", oldCookie, "same-origin")
	if err != nil || status != http.StatusUnauthorized {
		t.Fatal("new real daemon adopted an old login session:", status, err)
	}
	d.login(t, d.password, http.StatusOK)
	if d.cookie.Value == oldCookie.Value {
		t.Fatal("fresh actual PAM session reused the old cookie")
	}
	f.checkConsoleRecovery(t, ctx)
	if after, err := f.bridge.Inspect(ctx, "fixture1"); err != nil || after.fingerprint() != view.fingerprint() {
		t.Fatal("complete daemon crash/restart changed live managed routing")
	}
	if after, err := f.generation.Read(ctx); err != nil || !after.Same(f.active) {
		t.Fatal("complete daemon crash/restart changed acquisition generation")
	}
	if after, err := showProgramUnit(ctx, "dxberry-screen@wsjtx-fixture1.service"); err != nil || !bytes.Equal(before, after) {
		t.Fatal("complete daemon crash/restart changed the actual application invocation")
	}
	f.checkPreferences(t)
}

func (f *routingScreenFixture) checkConsoleRecovery(t *testing.T, ctx context.Context) {
	t.Helper()
	state, err := f.service.CollectionState(ctx, "fixture1")
	if err != nil || !state.Configured || !state.Enabled || state.Phase != RoutingInstalled || !state.RecoveryRequired || state.Observation != nil || state.Transport != nil {
		t.Fatal("actual authenticated daemon adopted an observation or lost its recovery obligation:", err)
	}
	if _, err := f.service.Preview(ctx, "fixture1"); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("actual authenticated daemon accepted running preview:", err)
	}
	if err := f.service.Recover(ctx, "fixture1"); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("actual authenticated daemon accepted running recovery:", err)
	}
	if err := f.service.Disable(ctx, "fixture1"); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("actual authenticated daemon accepted running disable:", err)
	}
}
