package wsjtx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/settings"
)

type serviceFixture struct {
	p       *programFixture
	service *ProgramService
	store   *settings.Store
	path    string
}

func programService(t *testing.T) *serviceFixture {
	t.Helper()
	p := programController(t)
	if err := p.c.Close(); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(t.TempDir(), "native")
	if err := InitializeProgramState(root); err != nil {
		t.Fatal(err)
	}
	p.history, p.routes = filepath.Join(root, "ports"), filepath.Join(root, "routes")
	p.reopen(t)
	f := &serviceFixture{p: p, store: &settings.Store{Dir: filepath.Join(t.TempDir(), "preferences")}, path: root}
	f.service = newProgramService(root, f.store, func() (*ProgramController, error) { return p.c, nil })
	t.Cleanup(func() { _ = f.service.Close() })
	return f
}

func (f *serviceFixture) approve(t *testing.T) ProgramRoutingPreview {
	t.Helper()
	f.p.gate.set(programStopped, "")
	preview, err := f.service.Preview(context.Background(), "radio1")
	if err != nil {
		t.Fatal(err)
	}
	if err := f.service.Enable(context.Background(), "radio1", preview.Fingerprint); err != nil {
		t.Fatal(err)
	}
	return preview
}

func TestProgramServiceConsentAndHooksComposeWithoutChangingTransmitGuards(t *testing.T) {
	f := programService(t)
	if err := f.store.Set("radio1", settings.RadioSettings{SWRLimit: 2.5, PTTTimeoutS: 90}); err != nil {
		t.Fatal(err)
	}
	preview := f.approve(t)
	if got, err := f.store.RoutingApproval("radio1"); err != nil || got != preview.Fingerprint {
		t.Fatal(got, err)
	}
	f.p.gate.set(programStartPre, programInvocationOne)
	if err := f.service.hook(context.Background(), "start", "radio1", programInvocationOne); err != nil {
		t.Fatal(err)
	}
	state, err := f.service.CollectionState(context.Background(), "radio1")
	if err != nil || !state.Configured || !state.Enabled || state.Phase != RoutingInstalled || state.Observation == nil {
		t.Fatal(state, err)
	}
	data, _ := json.Marshal(state)
	for _, private := range []string{"localhost", "Managed", "Original", "Transaction", "Address", "Peer"} {
		if strings.Contains(string(data), private) {
			t.Fatal("public collection state exposed private routing evidence", private)
		}
	}
	if err := f.service.Disable(context.Background(), "radio1"); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("running disable edited Qt settings", err)
	}
	if got, _ := f.store.RoutingApproval("radio1"); got != preview.Fingerprint {
		t.Fatal("running disable discarded consent")
	}
	f.p.gate.set(programStopPost, programInvocationOne)
	if err := f.service.hook(context.Background(), "stopped", "radio1", programInvocationOne); err != nil {
		t.Fatal(err)
	}
	f.p.gate.set(programStopped, "")
	if err := f.service.Disable(context.Background(), "radio1"); err != nil {
		t.Fatal(err)
	}
	value, err := f.store.ReadStrict("radio1")
	if err != nil || value.WSJTXRoutingApproval != "" || value.SWRLimit != 2.5 || value.PTTTimeoutS != 90 {
		t.Fatal("collection preference lost guards", value, err)
	}
	if string(fileBytes(t, f.p.f.path)) != settingsExample {
		t.Fatal("stop did not restore preferences")
	}
}

func TestProgramServiceRechecksPreviewInsteadOfPersistingClientRouting(t *testing.T) {
	f := programService(t)
	preview, err := f.service.Preview(context.Background(), "radio1")
	if err != nil {
		t.Fatal(err)
	}
	changed := strings.Replace(settingsExample, "UDPServerPort=02237", "UDPServerPort=2239", 1)
	if err := os.WriteFile(f.p.f.path, []byte(changed), 0640); err != nil {
		t.Fatal(err)
	}
	if err := f.service.Enable(context.Background(), "radio1", preview.Fingerprint); !errors.Is(err, ErrRoutingChanged) {
		t.Fatal("outdated preview enabled collection", err)
	}
	if got, err := f.store.RoutingApproval("radio1"); err != nil || got != "" {
		t.Fatal("refused approval persisted", got, err)
	}
	if f.p.settings.installs != 0 || string(fileBytes(t, f.p.f.path)) != changed {
		t.Fatal("approval performed a routing write")
	}
}

func TestProgramServiceDefaultOffNeedsNoInitializedStateOrApplicationHelper(t *testing.T) {
	root := filepath.Join(t.TempDir(), "absent")
	store := &settings.Store{Dir: filepath.Join(t.TempDir(), "radios")}
	s := newProgramService(root, store, func() (*ProgramController, error) {
		t.Fatal("default-off tried to open a controller")
		return nil, ErrProgramController
	})
	t.Cleanup(func() { _ = s.Close() })
	for _, action := range []string{"start", "stopped"} {
		if err := s.hook(context.Background(), action, "radio1", ""); err != nil {
			t.Fatal("legacy uncollected session failed", err)
		}
	}
	if state, err := s.CollectionState(context.Background(), "radio1"); err != nil || state.Configured || state.Enabled {
		t.Fatal(state, err)
	}
	if _, err := os.Stat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("default-off created collection state")
	}
	if _, err := s.Preview(context.Background(), "radio1"); !errors.Is(err, ErrProgramNotConfigured) {
		t.Fatal("preview initialized inventory implicitly", err)
	}
	if err := store.SaveRoutingApproval("radio1", "", strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if err := s.hook(context.Background(), "start", "radio1", programInvocationOne); !errors.Is(err, ErrProgramNotConfigured) {
		t.Fatal("lost enabled inventory became disabled", err)
	}
}

func TestProgramServiceRestorationDoesNotDependOnDamagedApprovalOrCurrentAssignment(t *testing.T) {
	f := programService(t)
	f.approve(t)
	f.p.gate.set(programStartPre, programInvocationOne)
	if err := f.service.hook(context.Background(), "start", "radio1", programInvocationOne); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(f.store.Dir, "radio1.json")
	if err := os.WriteFile(path, []byte(`{broken`), 0600); err != nil {
		t.Fatal(err)
	}
	f.p.eligible = false
	f.p.gate.set(programStopPost, programInvocationOne)
	if err := f.service.hook(context.Background(), "stopped", "radio1", programInvocationOne); err != nil {
		t.Fatal("restoration depended on current consent/owner", err)
	}
	f.p.empty(t)
	if string(fileBytes(t, f.p.f.path)) != settingsExample {
		t.Fatal("original destination not restored")
	}
	if raw, _ := os.ReadFile(path); string(raw) != `{broken` {
		t.Fatal("restoration reset damaged transmit settings")
	}
	if err := f.service.hook(context.Background(), "start", "radio1", programInvocationTwo); err == nil {
		t.Fatal("damaged consent allowed program startup")
	}
}

func TestProgramServiceUncollectedStartRestoresPendingRouteBeforeStarting(t *testing.T) {
	f := programService(t)
	preview := f.approve(t)
	f.p.gate.set(programStartPre, programInvocationOne)
	if err := f.service.hook(context.Background(), "start", "radio1", programInvocationOne); err != nil {
		t.Fatal(err)
	}
	// Simulate a root-side preference clear followed by a missed stop. The
	// durable route still belongs to the controller even without consent.
	if err := f.store.SaveRoutingApproval("radio1", preview.Fingerprint, ""); err != nil {
		t.Fatal(err)
	}
	f.p.gate.set(programStartPre, programInvocationTwo)
	if err := f.service.hook(context.Background(), "start", "radio1", programInvocationTwo); err != nil {
		t.Fatal(err)
	}
	f.p.empty(t)
	if f.p.settings.installs != 1 || string(fileBytes(t, f.p.f.path)) != settingsExample {
		t.Fatal("uncollected replacement kept or reinstalled the old route")
	}
}

func TestProgramStateInitializationRefusesExistingOrPartialInventory(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root-only inventory initialization")
	}
	path := filepath.Join(t.TempDir(), "native")
	if err := InitializeProgramState(path); err != nil {
		t.Fatal(err)
	}
	ports := fileBytes(t, filepath.Join(path, "ports", historyName))
	if err := InitializeProgramState(path); err == nil {
		t.Fatal("existing endpoint history reset")
	}
	if string(fileBytes(t, filepath.Join(path, "ports", historyName))) != string(ports) {
		t.Fatal("existing inventory changed")
	}
	partial := filepath.Join(t.TempDir(), "partial")
	if err := os.Mkdir(partial, 0700); err != nil {
		t.Fatal(err)
	}
	if err := InitializeProgramState(partial); err == nil {
		t.Fatal("partial inventory silently completed")
	}
	entries, _ := os.ReadDir(partial)
	if len(entries) != 0 {
		t.Fatal("partial inventory was modified")
	}
}
