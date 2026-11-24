package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/presets"
)

type failedOutput struct{}

func (failedOutput) Write(p []byte) (int, error) { return 0, errors.New("fixture output failure") }

func TestPresetCommandOutputFailureDoesNotImplyUnchangedState(t *testing.T) {
	state := t.TempDir()
	raw, _ := json.Marshal(presets.Request{Version: 1, Operation: "create", Radio: "radio1", ExpectedRevision: presets.Absent, Label: "committed", FrequencyHz: 14074000})
	if code := run([]string{"--state", state}, io.NopCloser(bytes.NewReader(raw)), failedOutput{}, 0); code != 7 {
		t.Fatal("post-commit response failure not uncertain", code)
	}
	r, code := invoke(t, state, presets.Request{Version: 1, Operation: "list", Radio: "radio1"}, 0)
	if code != 0 || r.Library == nil || len(r.Library.Presets) != 1 {
		t.Fatalf("published state rolled back after output failure: %+v %d", r, code)
	}
}

func invoke(t *testing.T, state string, input any, uid int) (response, int) {
	t.Helper()
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	code := run([]string{"--json", "--state", state}, io.NopCloser(bytes.NewReader(raw)), &out, uid)
	var r response
	if err := json.Unmarshal(out.Bytes(), &r); err != nil {
		t.Fatalf("not one JSON response: %q %v", out.String(), err)
	}
	return r, code
}

func TestPresetCommandListsAndEditsDefinitions(t *testing.T) {
	state := t.TempDir()
	list := presets.Request{Version: 1, Operation: "list", Radio: "radio1"}
	r, code := invoke(t, state, list, 0)
	if code != 0 || !r.OK || r.Library == nil || r.Library.Revision != presets.Absent {
		t.Fatalf("list: %+v %d", r, code)
	}
	if _, err := os.Stat(filepath.Join(state, "presets")); !os.IsNotExist(err) {
		t.Fatal("list created state")
	}
	create := presets.Request{Version: 1, Operation: "create", Radio: "radio1", ExpectedRevision: presets.Absent, Label: "receive", FrequencyHz: 14074000}
	r, code = invoke(t, state, create, 0)
	if code != 0 || !r.OK || len(r.Library.Presets) != 1 {
		t.Fatalf("create: %+v %d", r, code)
	}
	l := *r.Library
	stale, code := invoke(t, state, create, 0)
	if code != 4 || stale.OK || stale.Code != "conflict" || stale.Library != nil {
		t.Fatalf("conflict leaked state: %+v %d", stale, code)
	}
	update := presets.Request{Version: 1, Operation: "update", Radio: "radio1", ExpectedRevision: l.Revision, ID: l.Presets[0].ID, Label: "updated", FrequencyHz: 7100000}
	r, code = invoke(t, state, update, 0)
	if code != 0 || r.Library.Presets[0].FrequencyHz != 7100000 {
		t.Fatalf("update: %+v %d", r, code)
	}
	deleteReq := presets.Request{Version: 1, Operation: "delete", Radio: "radio1", ExpectedRevision: r.Library.Revision, ID: r.Library.Presets[0].ID}
	r, code = invoke(t, state, deleteReq, 0)
	if code != 0 || r.Library.Presets == nil || len(r.Library.Presets) != 0 {
		t.Fatalf("delete: %+v %d", r, code)
	}
	if entries, err := os.ReadDir(state); err != nil || len(entries) != 1 || entries[0].Name() != "presets" {
		t.Fatal("command changed unrelated state", entries, err)
	}
}

func TestPresetCommandRejectsPrivilegeOptionsAndControlFields(t *testing.T) {
	state := t.TempDir()
	list := map[string]any{"version": 1, "operation": "list", "radio": "radio1"}
	if r, code := invoke(t, state, list, 1000); code != 6 || r.OK || r.Code != "unavailable" {
		t.Fatalf("nonroot: %+v %d", r, code)
	}
	for _, args := range [][]string{{"--state", "relative"}, {"--unknown", "value"}, {"--json=false"}, {"recall"}} {
		var out bytes.Buffer
		if code := run(args, io.NopCloser(strings.NewReader(`{}`)), &out, 0); code != 2 {
			t.Fatal("invalid options accepted", args, code)
		}
	}
	for _, req := range []map[string]any{
		{"version": 1, "operation": "recall", "radio": "radio1"},
		{"version": 1, "operation": "list", "radio": "../other"},
		{"version": 1, "operation": "list", "radio": "radio1", "state": "/some/private/path"},
		{"version": 1, "operation": "list", "radio": "radio1", "command": "set_ptt 1"},
	} {
		r, code := invoke(t, state, req, 0)
		if code != 2 || r.OK || r.Library != nil || r.Reason != presets.ErrInvalid.Error() {
			t.Fatalf("unsupported request: %+v %d", r, code)
		}
	}
	if entries, err := os.ReadDir(state); err != nil || len(entries) != 0 {
		t.Fatal("refusal changed state", entries, err)
	}
}

func TestPresetCommandRefusesCorruptStateWithoutPrivateErrors(t *testing.T) {
	state := t.TempDir()
	dir := filepath.Join(state, "presets")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	private := "damaged private fixture content"
	path := filepath.Join(dir, "radio1.json")
	if err := os.WriteFile(path, []byte(private), 0600); err != nil {
		t.Fatal(err)
	}
	r, code := invoke(t, state, presets.Request{Version: 1, Operation: "list", Radio: "radio1"}, 0)
	if code != 6 || r.OK || r.Code != "unavailable" || r.Library != nil || strings.Contains(r.Reason, private) || strings.Contains(r.Reason, state) {
		t.Fatalf("bad response: %+v %d", r, code)
	}
	if raw, err := os.ReadFile(path); err != nil || string(raw) != private {
		t.Fatal("corrupt state replaced", err)
	}
}

type blockedInput struct{ release <-chan struct{} }

func (b blockedInput) Read(p []byte) (int, error) { <-b.release; return 0, io.EOF }
func (b blockedInput) Close() error               { <-b.release; return nil }

func TestPresetCommandBoundsInputAndStopsWaiting(t *testing.T) {
	state := t.TempDir()
	var out bytes.Buffer
	if code := run([]string{"--state", state}, io.NopCloser(strings.NewReader(strings.Repeat(" ", presets.MaxBytes+1))), &out, 0); code != 2 {
		t.Fatal("oversized input accepted", code)
	}
	release := make(chan struct{})
	defer close(release)
	out.Reset()
	done := make(chan int, 1)
	start := time.Now()
	go func() { done <- run([]string{"--state", state}, blockedInput{release}, &out, 0) }()
	select {
	case code := <-done:
		if code != 6 || time.Since(start) > 7*time.Second {
			t.Fatal("stalled input not bounded", code)
		}
	case <-time.After(7 * time.Second):
		t.Fatal("stdin deadline depended on interruptible Read/Close")
	}
	var result response
	if err := json.Unmarshal(out.Bytes(), &result); err != nil || result.Code != "unavailable" || result.Library != nil {
		t.Fatal("stalled response", result, err)
	}
}
