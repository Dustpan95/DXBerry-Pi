package httpapi

import (
	"bufio"
	"context"
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
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/presetpreview"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/presets"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/rig"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/session"
)

type previewHTTPClock struct{ at time.Time }

func (c previewHTTPClock) Now() time.Time                { return c.at }
func (c previewHTTPClock) At(time.Time) <-chan time.Time { return make(chan time.Time) }

type previewHTTPSource struct {
	target presetpreview.Target
	calls  atomic.Int32
}

func (s *previewHTTPSource) Inspect(_ context.Context, r radios.Registration) (presetpreview.Target, error) {
	s.calls.Add(1)
	if r.Radio().Name != "radio1" {
		return presetpreview.Target{}, presetpreview.ErrUnavailable
	}
	return s.target, nil
}

func TestPresetPreviewHTTPActualServiceAndProtocol(t *testing.T) {
	if os.Geteuid() != 0 {
		if os.Getenv("DXB_PRESET_PREVIEW_INTEGRATION") == "1" {
			t.Fatal("Docker-root fixture required")
		}
		t.Skip("root-owned registration fixture")
	}
	if os.Getenv("DXB_PRESET_PREVIEW_INTEGRATION") == "1" {
		interfaces, err := net.Interfaces()
		if err != nil {
			t.Fatal(err)
		}
		for _, i := range interfaces {
			if i.Flags&net.FlagLoopback == 0 {
				t.Fatal("loopback-only fixture required")
			}
		}
		for _, pattern := range []string{"/dev/snd", "/dev/ttyUSB*", "/dev/ttyACM*", "/dev/gpiochip*", "/dev/serial"} {
			paths, err := filepath.Glob(pattern)
			if err != nil || len(paths) > 0 {
				t.Fatal("hardware exposed", pattern, err)
			}
		}
	}
	root := t.TempDir()
	store, err := presets.NewStore(filepath.Join(root, "presets"))
	if err != nil {
		t.Fatal(err)
	}
	library, err := store.Edit(context.Background(), presets.Request{Version: 1, Operation: "create", Radio: "radio1", ExpectedRevision: presets.Absent, Label: "Fixture frequency", FrequencyHz: 14075000})
	if err != nil {
		t.Fatal(err)
	}
	records := filepath.Join(root, "radios.json")
	raw := []byte(`{"version":1,"radios":{"radio1":{"label":"Fixture","owner":"","rigctld_port":4532,"ptt":{"method":"rigctld"},"rig":{"model":3073,"control":"hamlib"},"cat":{"path":"usb-0:1.3:1.0"}}}}`)
	if err := os.WriteFile(records, raw, 0600); err != nil {
		t.Fatal(err)
	}
	reader, err := radios.NewRegistrationReader(records)
	if err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	var wireMu sync.Mutex
	var commands []string
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() {
				defer conn.Close()
				reader := bufio.NewReader(conn)
				for {
					line, err := reader.ReadString('\n')
					if err != nil {
						return
					}
					cmd := strings.TrimSpace(line)
					wireMu.Lock()
					commands = append(commands, cmd)
					wireMu.Unlock()
					value := map[string]string{`+\get_ptt`: "PTT: 0", `+\get_vfo`: "VFO: VFOA", `+\get_freq`: "Frequency: 14074000"}[cmd]
					if value == "" {
						fmt.Fprint(conn, "unknown:\nRPRT -1\n")
						continue
					}
					fmt.Fprintf(conn, "%s:\n%s\nRPRT 0\n", strings.TrimPrefix(cmd, `+\`), value)
				}
			}()
		}
	}()
	client := rig.Dial(listener.Addr().String(), nil)
	t.Cleanup(client.Close)
	deadline := time.Now().Add(3 * time.Second)
	for !client.Connected() {
		if time.Now().After(deadline) {
			t.Fatal("fixture client did not connect")
		}
		time.Sleep(time.Millisecond)
	}
	clock := previewHTTPClock{time.Now()}
	caps := rig.Caps{ModelName: "Fixture", Probed: clock.Now(), CanGetPTT: true, CanGetVFO: true, VFOs: []string{"VFOA"}, FreqRanges: []rig.FreqRange{{Lo: 14000000, Hi: 14350000}}}
	source := &previewHTTPSource{target: presetpreview.Target{Poller: rig.NewPoller(client, caps, clock, nil), Caps: caps, Identity: "private synthetic route/program generation"}}
	owners := session.NewOwners(clock)
	if err := owners.Claim("radio1", session.Holder{Kind: "tab", ID: session.TabID("login-a", "phone#fixture")}); err != nil {
		t.Fatal(err)
	}
	service, err := presetpreview.New(presetpreview.Config{Definitions: store, Registrations: reader, Owners: owners, Targets: source, Now: clock.Now})
	if err != nil {
		t.Fatal(err)
	}
	auth := presetAuthFunc(func(r *http.Request) (*Session, bool) {
		cookie, err := r.Cookie("fixture-login")
		if err != nil || cookie.Value != "login-a" && cookie.Value != "login-b" {
			return nil, false
		}
		return &Session{ID: cookie.Value, User: "fixture"}, true
	})
	api := &PresetPreviewAPI{Auth: auth, Service: service}
	server := httptest.NewServer(New(Config{Auth: auth, Routes: api.Register}).Handler)
	defer server.Close()
	request := func(action, radio, body, login, origin string) (int, []byte) {
		t.Helper()
		req, err := http.NewRequest("POST", server.URL+"/api/radio/"+radio+"/presets/"+action, strings.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Origin", origin)
		req.Header.Set(TabHeader, "phone#fixture")
		if login != "" {
			req.AddCookie(&http.Cookie{Name: "fixture-login", Value: login})
		}
		response, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer response.Body.Close()
		data, err := io.ReadAll(response.Body)
		if err != nil {
			t.Fatal(err)
		}
		if response.Header.Get("Cache-Control") != "no-store" {
			t.Fatal("response cacheable")
		}
		return response.StatusCode, data
	}
	body := fmt.Sprintf(`{"id":%q,"revision":%q}`, library.Presets[0].ID, library.Revision)
	code, data := request("preview", "radio1", body, "login-a", server.URL)
	var result struct {
		OK      bool                  `json:"ok"`
		Preview presetpreview.Preview `json:"preview"`
	}
	if err := json.Unmarshal(data, &result); err != nil || code != 200 || !result.OK || result.Preview.DeltaHz != 1000 {
		t.Fatal(code, string(data), err)
	}
	tokenBody := fmt.Sprintf(`{"token":%q}`, result.Preview.Token)
	if code, data := request("recheck", "radio1", tokenBody, "login-a", server.URL); code != 200 {
		t.Fatal(code, string(data))
	}
	baseline := source.calls.Load()
	for _, tc := range []struct {
		action, radio, body, login, origin string
		status                             int
	}{
		{"recheck", "radio2", tokenBody, "login-a", server.URL, 409},
		{"recheck", "radio1", tokenBody, "login-b", server.URL, 409},
		{"preview", "radio1", body, "", server.URL, 401},
		{"preview", "radio1", body, "login-a", "https://foreign.invalid", 403},
		{"preview", "radio1", strings.TrimSuffix(body, "}") + `,"login":"login-b"}`, "login-a", server.URL, 400},
	} {
		if code, data := request(tc.action, tc.radio, tc.body, tc.login, tc.origin); code != tc.status {
			t.Fatal(code, string(data))
		}
	}
	if source.calls.Load() != baseline {
		t.Fatal("refused HTTP request reached fresh sources")
	}
	if code, data := request("preview", "radio1", body, "login-a", server.URL); code != 200 {
		t.Fatal(code, string(data))
	} else if err := json.Unmarshal(data, &result); err != nil {
		t.Fatal(err)
	}
	tokenBody = fmt.Sprintf(`{"token":%q}`, result.Preview.Token)
	if err := os.WriteFile(records+".new", raw, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(records+".new", records); err != nil {
		t.Fatal(err)
	}
	if code, data := request("recheck", "radio1", tokenBody, "login-a", server.URL); code != 409 || !strings.Contains(string(data), `"code":"conflict"`) {
		t.Fatal(code, string(data))
	}
	latest, err := store.List(context.Background(), "radio1")
	if err != nil || latest.Revision != library.Revision {
		t.Fatal("HTTP flow edited definitions", err)
	}
	current, err := os.ReadFile(records)
	if err != nil || string(current) != string(raw) {
		t.Fatal("HTTP flow edited registration contents", err)
	}
	wireMu.Lock()
	defer wireMu.Unlock()
	if len(commands) != 24 {
		t.Fatal("unexpected read count", commands)
	}
	for _, command := range commands {
		if command != `+\get_ptt` && command != `+\get_vfo` && command != `+\get_freq` {
			t.Fatal("write command emitted", command)
		}
	}
	t.Log("production HTTP middleware/adapter, real store/registration/claims/rig client and read-only protocol passed; synthetic authentication, clock, target verifier and radio replies; no physical acceptance")
}
