package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/httpapi"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/presets"
)

func configurePreset(t *testing.T, c *CLI) {
	t.Helper()
	if err := c.SetPresetState("/trusted/state"); err != nil {
		t.Fatal(err)
	}
}

func TestPresetRequiresTrustedStateConfiguration(t *testing.T) {
	c := New("/trusted/bin", func(context.Context, string, []string, string, []string) (Result, error) {
		t.Fatal("unconfigured preset reached runner")
		return Result{}, nil
	})
	call := Call{"dxberry-preset", []string{"--json"}, presetInput(t, "list")}
	if c.Validate(call) == nil {
		t.Fatal("unconfigured command enabled")
	}
	for _, path := range []string{"", "relative", "/trusted/../other"} {
		if c.SetPresetState(path) == nil {
			t.Fatal("invalid trusted path accepted", path)
		}
	}
	if c.Validate(call) == nil {
		t.Fatal("invalid path enabled command")
	}
	configurePreset(t, c)
	if err := c.Validate(call); err != nil {
		t.Fatal(err)
	}
}

func presetInput(t *testing.T, operation string) string {
	t.Helper()
	r := presets.Request{Version: 1, Operation: operation, Radio: "radio1"}
	if operation != "list" {
		r.ExpectedRevision = strings.Repeat("a", 64)
	}
	if operation == "create" || operation == "update" {
		r.Label = "private fixture label"
		r.FrequencyHz = 14074000
	}
	if operation == "update" || operation == "delete" {
		r.ID = strings.Repeat("b", 32)
	}
	raw, err := json.Marshal(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestPresetCallsHaveFixedArgumentsAndStrictInput(t *testing.T) {
	c := New("/trusted/bin", func(context.Context, string, []string, string, []string) (Result, error) {
		t.Fatal("validation ran a process")
		return Result{}, nil
	})
	configurePreset(t, c)
	for _, op := range []string{"list", "create", "update", "delete"} {
		if err := c.Validate(Call{"dxberry-preset", []string{"--json"}, presetInput(t, op)}); err != nil {
			t.Fatalf("%s: %v", op, err)
		}
	}
	for _, args := range [][]string{nil, {"--state", "/private/path", "--json"}, {"--json", "--state=/private/path"}, {"--json=false"}, {"recall", "--json"}} {
		if err := c.Validate(Call{"dxberry-preset", args, presetInput(t, "list")}); err == nil || strings.Contains(err.Error(), "/private/path") {
			t.Fatalf("arguments accepted or leaked: %v %v", args, err)
		}
	}
	for _, input := range []string{"", `null`, `{"version":1,"operation":"recall","radio":"radio1"}`, `{"version":1,"operation":"list","radio":"radio1","state":"/private/path"}`, `{"version":1,"operation":"list","radio":"radio1","\u0072adio":"radio2"}`, presetInput(t, "create") + ` {}`, strings.Repeat(" ", presets.MaxBytes+1)} {
		err := c.Validate(Call{"dxberry-preset", []string{"--json"}, input})
		if err == nil || strings.Contains(err.Error(), "/private/path") || strings.Contains(err.Error(), "private fixture label") {
			t.Fatalf("stdin accepted or leaked: %v", err)
		}
	}
}

func TestPresetRunnerPreservesOnlyTheFixedPathAndJSONInput(t *testing.T) {
	input := presetInput(t, "create")
	calls := 0
	c := New("/trusted/bin", func(ctx context.Context, name string, args []string, stdin string, env []string) (Result, error) {
		calls++
		if name != "/trusted/bin/dxberry-preset" || strings.Join(args, "|") != "--json|--state|/trusted/state" || stdin != input || strings.Join(env, "|") != "PATH=/usr/sbin:/usr/bin:/sbin:/bin|LANG=C.UTF-8" {
			t.Fatal("runner inputs changed or expanded", name, args)
		}
		return Result{Stdout: `{"ok":true}`, Exit: 0}, nil
	})
	configurePreset(t, c)
	if _, err := c.Run(context.Background(), Call{"dxberry-preset", []string{"--json"}, input}); err != nil || calls != 1 {
		t.Fatal("valid preset call refused", err, calls)
	}
	if _, err := c.Run(context.Background(), Call{"dxberry-preset", []string{"--json"}, `{"version":1,"operation":"list","radio":"radio1","command":"set_ptt 1"}`}); err == nil || calls != 1 {
		t.Fatal("invalid input reached process", err, calls)
	}
}

func TestPresetHTTPUsesStationAuthenticationAndDoesNotLogInput(t *testing.T) {
	input := presetInput(t, "create")
	raw, _ := json.Marshal(map[string]any{"args": []string{"--json"}, "stdin": input})
	for _, tc := range []struct {
		name          string
		logged        bool
		origin        string
		body          []byte
		status, calls int
	}{
		{"logged out", false, "", raw, 401, 0},
		{"foreign origin", true, "https://foreign.example", raw, 403, 0},
		{"invalid input", true, "", []byte(`{"args":["--json"],"stdin":"private fixture invalid input"}`), 400, 0},
		{"accepted", true, "", raw, 200, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls, notices := 0, 0
			var logs []string
			c := New("/trusted/bin", func(context.Context, string, []string, string, []string) (Result, error) {
				calls++
				return Result{Stdout: `{"ok":true}`, Exit: 0}, nil
			})
			configurePreset(t, c)
			c.Logf = func(f string, args ...any) { logs = append(logs, fmt.Sprintf(f, args...)) }
			c.OnCall = func(cmd string, args []string) { notices++ }
			h := httpapi.New(httpapi.Config{Web: fstest.MapFS{"index.html": {Data: []byte("x")}}, Auth: stubAuth{ok: tc.logged}, Routes: c.Register}).Handler
			req := httptest.NewRequest("POST", "http://station.local/api/cli/dxberry-preset", strings.NewReader(string(tc.body)))
			req.Header.Set("Sec-Fetch-Site", "same-origin")
			if tc.origin != "" {
				req.Header.Del("Sec-Fetch-Site")
				req.Header.Set("Origin", tc.origin)
			}
			w := httptest.NewRecorder()
			h.ServeHTTP(w, req)
			if w.Code != tc.status || calls != tc.calls || notices != tc.calls {
				t.Fatalf("HTTP=%d calls=%d notices=%d body=%s", w.Code, calls, notices, w.Body.String())
			}
			for _, line := range logs {
				if strings.Contains(line, "private fixture") || strings.Contains(line, "frequency_hz") || strings.Contains(line, "14074000") {
					t.Fatal("stdin reached logs", line)
				}
			}
			if tc.status == http.StatusOK && w.Header().Get("Cache-Control") != "no-store" {
				t.Fatal("response can be cached")
			}
		})
	}
}

func TestPresetHTTPEnvelopeRejectsRepairAndAmbiguity(t *testing.T) {
	input := presetInput(t, "create")
	raw, _ := json.Marshal(callBody{Args: []string{"--json"}, Stdin: input})
	text := string(raw)
	bad := [][]byte{
		[]byte(text + ` {}`), []byte(strings.Replace(text, `"args":`, `"Args":`, 1)),
		[]byte(text[:len(text)-1] + `,"args":["--json"]}`),
		[]byte(text[:len(text)-1] + `,"\u0073tdin":"other"}`),
		[]byte(text[:len(text)-1] + `,"state":"/private/path"}`),
		[]byte(strings.Replace(text, "private fixture label", `\ud800`, 1)),
		[]byte(strings.Replace(text, "private fixture label", `\udc00`, 1)),
		[]byte(`{"args":null,"stdin":"{}"}`), []byte(`{"args":["--json"],"stdin":null}`),
		[]byte(strings.Repeat(" ", presets.MaxBytes+1) + text),
	}
	invalid := append([]byte(nil), raw...)
	invalid[bytes.Index(invalid, []byte("private fixture label"))] = 0xff
	bad = append(bad, invalid)
	calls := 0
	c := New("/trusted/bin", func(context.Context, string, []string, string, []string) (Result, error) {
		calls++
		return Result{}, nil
	})
	configurePreset(t, c)
	c.Logf = func(string, ...any) {}
	h := httpapi.New(httpapi.Config{Auth: stubAuth{ok: true}, Routes: c.Register}).Handler
	for i, body := range bad {
		req := httptest.NewRequest("POST", "/api/cli/dxberry-preset", bytes.NewReader(body))
		req.Header.Set("Sec-Fetch-Site", "same-origin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		if w.Code != 400 || calls != 0 || strings.Contains(w.Body.String(), "private fixture") || strings.Contains(w.Body.String(), "/private/path") {
			t.Fatalf("bad outer wrapper %d accepted/leaked: status=%d calls=%d body=%s", i, w.Code, calls, w.Body.String())
		}
	}
	paired := strings.Replace(text, "private fixture label", `\ud83d\udcfb`, 1)
	w := post(h, "/api/cli/dxberry-preset", paired)
	if w.Code != 200 || calls != 1 {
		t.Fatal("valid Unicode pair refused", w.Code, calls, w.Body.String())
	}
}

func FuzzPresetHTTPEnvelope(f *testing.F) {
	f.Add([]byte(`{"args":["--json"],"stdin":"{}"}`))
	f.Add([]byte(`{"args":null}`))
	f.Add([]byte(`{"stdin":"\ud800"}`))
	f.Add([]byte(`{"args":["--json"],"stdin":"` + strings.Repeat("<", 40<<10) + `"}`))
	f.Fuzz(func(t *testing.T, raw []byte) {
		body, err := decodePresetBody(raw)
		if err != nil {
			return
		}
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatal(err)
		}
		// JSON's HTML escaping can expand an otherwise bounded transport body.
		// The round-trip property applies only inside the decoder's byte limit.
		if len(encoded) > presets.MaxBytes {
			return
		}
		again, err := decodePresetBody(encoded)
		if err != nil || again.Stdin != body.Stdin || len(again.Args) != len(body.Args) || strings.Join(again.Args, "|") != strings.Join(body.Args, "|") {
			t.Fatal("outer wrapper silently changed text")
		}
	})
}
