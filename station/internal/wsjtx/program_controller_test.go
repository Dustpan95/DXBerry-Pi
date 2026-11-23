package wsjtx

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	programInvocationOne = "123456789abcdef0123456789abcdef0"
	programInvocationTwo = "123456789abcdef0123456789abcdef1"
)

type fixtureProgramLifecycle struct {
	mu         sync.Mutex
	boundary   programBoundary
	invocation string
	checks     int
	deny       bool
}

func (g *fixtureProgramLifecycle) set(boundary programBoundary, invocation string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.boundary, g.invocation = boundary, invocation
}

func (g *fixtureProgramLifecycle) check(ctx context.Context, radio, invocation string, boundary programBoundary) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.checks++
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if g.deny || boundary != g.boundary || invocation != g.invocation || !programRadio(radio) || boundary != programStopped && !programInvocation(invocation) {
		return ErrProgramLifecycle
	}
	return nil
}

type auditedProgramSettings struct {
	bridge               *SettingsBridge
	journal              *RoutingJournal
	observer             *Observer
	t                    *testing.T
	installs, restores   int
	installChecks        int
	restoreChecks        int
	onInstall, onRestore func(int)
	afterInstall         error
	afterRestore         error
}

func (s *auditedProgramSettings) Inspect(ctx context.Context, radio string) (RoutingSettings, error) {
	return s.bridge.Inspect(ctx, radio)
}

func (s *auditedProgramSettings) Install(ctx context.Context, patch RoutingPatch, guard func(context.Context) error) (RoutingSettings, error) {
	s.installs++
	view, err := s.bridge.Install(ctx, patch, func(ctx context.Context) error {
		s.installChecks++
		_, record, pending, err := s.journal.Lookup(patch.Radio())
		if err != nil || !pending || record.Phase != RoutingPrepared || record.RecoveryRequired {
			s.t.Error("install ran before durable preparation", err)
			return ErrRoutingJournalPhase
		}
		if _, ok := s.observer.Snapshot(patch.Radio()); !ok {
			s.t.Error("install ran without receiver")
			return ErrRoutingBinding
		}
		if s.onInstall != nil {
			s.onInstall(s.installChecks)
		}
		return guard(ctx)
	})
	if err == nil && s.afterInstall != nil {
		return RoutingSettings{}, s.afterInstall
	}
	return view, err
}

func (s *auditedProgramSettings) Restore(ctx context.Context, patch RoutingPatch, guard func(context.Context) error) (RoutingSettings, error) {
	s.restores++
	view, err := s.bridge.Restore(ctx, patch, func(ctx context.Context) error {
		s.restoreChecks++
		_, record, pending, err := s.journal.Lookup(patch.Radio())
		if err != nil || !pending || record.Phase != RoutingRestoring {
			s.t.Error("restore ran before durable restoration marker", err)
			return ErrRoutingJournalPhase
		}
		if _, ok := s.observer.Snapshot(patch.Radio()); ok {
			s.t.Error("restore retained an active receiver")
			return ErrRoutingBinding
		}
		if s.onRestore != nil {
			s.onRestore(s.restoreChecks)
		}
		return guard(ctx)
	})
	if err == nil && s.afterRestore != nil {
		return RoutingSettings{}, s.afterRestore
	}
	return view, err
}

type programFixture struct {
	c               *ProgramController
	f               bridgeFixture
	gate            *fixtureProgramLifecycle
	settings        *auditedProgramSettings
	history, routes string
	eligible        bool
}

func programController(t *testing.T) *programFixture {
	t.Helper()
	f := appBridge(t)
	if path := os.Getenv("DXB_SETTINGS_HELPER_BINARY"); path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		helper := filepath.Join(f.bin, "program-settings-helper")
		if err := os.WriteFile(helper, data, 0755); err != nil {
			t.Fatal(err)
		}
		f.bridge, err = newSettingsBridge(helper, f.home, 1000, 1000)
		if err != nil {
			t.Fatal(err)
		}
	}
	o, history := persistentObserver(t)
	j, routes := routeJournal(t)
	p := &programFixture{f: f, gate: &fixtureProgramLifecycle{boundary: programStopped}, history: history, routes: routes, eligible: true}
	p.settings = &auditedProgramSettings{bridge: f.bridge, journal: j, observer: o, t: t}
	p.c = newProgramController(o, j, p.settings, p.gate, func(context.Context, string) error {
		if !p.eligible {
			return ErrInvalidSession
		}
		return nil
	})
	t.Cleanup(func() { _ = p.c.Close() })
	return p
}

func (p *programFixture) preview(t *testing.T) ProgramRoutingPreview {
	t.Helper()
	p.gate.set(programStopped, "")
	preview, err := p.c.Preview(context.Background(), "radio1")
	if err != nil {
		t.Fatal(err)
	}
	return preview
}

func (p *programFixture) start(t *testing.T, approval, invocation string) {
	t.Helper()
	p.gate.set(programStartPre, invocation)
	if err := p.c.Start(context.Background(), "radio1", invocation, approval); err != nil {
		t.Fatal(err)
	}
}

func (p *programFixture) pending(t *testing.T, phase RoutingPhase) RoutingJournalSnapshot {
	t.Helper()
	_, record, pending, err := p.c.journal.Lookup("radio1")
	if err != nil || !pending || record.Phase != phase {
		t.Fatal("wrong pending route state", pending, record.Phase, err)
	}
	return record
}

func (p *programFixture) empty(t *testing.T) {
	t.Helper()
	if records, err := p.c.journal.Snapshots(); err != nil || len(records) != 0 {
		t.Fatal("route record was not completed", len(records), err)
	}
	if len(p.c.observer.Snapshots()) != 0 {
		t.Fatal("receiver survived restoration")
	}
}

func (p *programFixture) reopen(t *testing.T) {
	t.Helper()
	if err := p.c.Close(); err != nil {
		t.Fatal(err)
	}
	o := reopenObserver(t, p.history)
	j := reopenJournal(t, p.routes)
	p.settings.observer, p.settings.journal = o, j
	p.c = newProgramController(o, j, p.settings, p.gate, func(context.Context, string) error {
		if !p.eligible {
			return ErrInvalidSession
		}
		return nil
	})
}

func TestProgramControllerCompleteApplicationUserLifecycle(t *testing.T) {
	p := programController(t)
	before := fileBytes(t, p.f.path)
	preview := p.preview(t)
	preview.Settings.Current.Server.Value = "mutated preview copy"
	p.start(t, preview.Fingerprint, programInvocationOne)
	p.pending(t, RoutingInstalled)
	old := p.c.sessions["radio1"].binding
	if string(fileBytes(t, p.f.path)) == string(before) || p.settings.installChecks != 2 {
		t.Fatal("settings not installed at both bridge boundaries")
	}
	sender := udpSender(t, old.Address())
	for _, name := range []string{"heartbeat-s3", "status-full-s3", "decode-full-s3"} {
		send(t, sender, fixture(t, name))
	}
	status := received(t, p.c.observer, "radio1", 3)
	if status.Observation.Status == nil || len(status.Observation.Decodes) != 1 {
		t.Fatal("managed routing did not feed the correct observation session")
	}
	public, err := p.c.CollectionState(context.Background(), "radio1")
	if err != nil || public.Transport == nil || public.Transport.Received != 3 || public.Transport.QueueDrops != 0 || public.Transport.Failed {
		t.Fatal("transport counters missing from the public observation", err)
	}
	raw, err := json.Marshal(public)
	if err != nil || strings.Contains(string(raw), old.Address().String()) || strings.Contains(string(raw), status.Peer.String()) {
		t.Fatal("public observation exposed a private endpoint", err)
	}
	// A repeated hook in the same start-pre invocation is idempotent.
	p.start(t, preview.Fingerprint, programInvocationOne)
	if p.settings.installs != 1 || p.c.sessions["radio1"].binding != old {
		t.Fatal("duplicate start replaced the receiver")
	}
	p.gate.set(programStopPost, programInvocationOne)
	if err := p.c.Stopped(context.Background(), "radio1", programInvocationOne); err != nil {
		t.Fatal(err)
	}
	p.empty(t)
	if string(fileBytes(t, p.f.path)) != string(before) || p.settings.restoreChecks != 2 {
		t.Fatal("stop did not restore exact original settings")
	}
	// The approved namespace remains valid, but the endpoint never repeats.
	p.start(t, preview.Fingerprint, programInvocationTwo)
	current := p.c.sessions["radio1"].binding
	if current.Address() == old.Address() {
		t.Fatal("new invocation reused the retired endpoint")
	}
	if err := p.c.Stopped(context.Background(), "radio1", programInvocationOne); !errors.Is(err, ErrProgramRetired) {
		t.Fatal("old stop callback accepted", err)
	}
	if !currentRoutingBinding(current) {
		t.Fatal("late stop retired the replacement")
	}
}

func TestProgramControllerRequiresStoppedPreviewEligibilityAndApproval(t *testing.T) {
	p := programController(t)
	original := fileBytes(t, p.f.path)
	preview := p.preview(t)
	p.gate.set(programStartPre, programInvocationOne)
	if _, err := p.c.Preview(context.Background(), "radio1"); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("preview read a running program", err)
	}
	for _, approval := range []string{"", "bad", strings.Repeat("a", 64), programRoutingFingerprint("radio2", preview.Settings)} {
		if err := p.c.Start(context.Background(), "radio1", programInvocationOne, approval); err == nil {
			t.Fatal("unapproved namespace installed")
		}
	}
	p.eligible = false
	if err := p.c.Start(context.Background(), "radio1", programInvocationOne, preview.Fingerprint); !errors.Is(err, ErrInvalidSession) {
		t.Fatal("ineligible radio started", err)
	}
	p.eligible = true
	p.gate.set(programStopped, "")
	if err := p.c.Start(context.Background(), "radio1", programInvocationOne, preview.Fingerprint); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("install occurred outside start-pre", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := p.c.Start(ctx, "radio1", programInvocationOne, preview.Fingerprint); !errors.Is(err, context.Canceled) {
		t.Fatal("cancelled start accepted", err)
	}
	p.empty(t)
	if string(fileBytes(t, p.f.path)) != string(original) || p.settings.installs != 0 {
		t.Fatal("refused request changed program routing")
	}
}

func TestProgramControllerNamespaceChangeInvalidatesApproval(t *testing.T) {
	for _, change := range []struct{ before, after string }{
		{"CurrentName=Default", "CurrentName=Changed"},
		{"UDPServer=\"localhost\"", "UDPServer=other.example"},
		{"Other%20Profile\\Configuration\\UDPServerPort=2238", "Other%20Profile\\Configuration\\UDPServerPort=2239"},
	} {
		t.Run(change.after, func(t *testing.T) {
			p := programController(t)
			preview := p.preview(t)
			changed := strings.Replace(settingsExample, change.before, change.after, 1)
			if err := os.WriteFile(p.f.path, []byte(changed), 0640); err != nil {
				t.Fatal(err)
			}
			p.gate.set(programStartPre, programInvocationOne)
			if err := p.c.Start(context.Background(), "radio1", programInvocationOne, preview.Fingerprint); !errors.Is(err, ErrRoutingChanged) {
				t.Fatal("changed original namespace was approved", err)
			}
			p.empty(t)
			if string(fileBytes(t, p.f.path)) != changed {
				t.Fatal("rejected start overwrote operator settings")
			}
		})
	}
}

func TestProgramControllerDeniedBridgeBoundaryRetainsRecovery(t *testing.T) {
	for _, stage := range []int{1, 2} {
		t.Run(string(rune('0'+stage)), func(t *testing.T) {
			p := programController(t)
			preview := p.preview(t)
			p.settings.onInstall = func(check int) {
				if check == stage {
					p.gate.deny = true
				}
			}
			p.gate.set(programStartPre, programInvocationOne)
			if err := p.c.Start(context.Background(), "radio1", programInvocationOne, preview.Fingerprint); !errors.Is(err, ErrProgramLifecycle) {
				t.Fatal("lifecycle denial ignored", err)
			}
			p.pending(t, RoutingPrepared)
			if len(p.c.observer.Snapshots()) != 0 || string(fileBytes(t, p.f.path)) != settingsExample {
				t.Fatal("denial left a receiver or installed route")
			}
			p.gate.deny = false
			p.gate.set(programStopped, "")
			if _, err := p.c.Preview(context.Background(), "radio1"); !errors.Is(err, ErrRoutingJournalRecovery) {
				t.Fatal("pending managed route presented for approval", err)
			}
			if err := p.c.Recover(context.Background(), "radio1"); err != nil {
				t.Fatal(err)
			}
			p.empty(t)
		})
	}
}

func TestProgramControllerUncertainInstallRestoresBeforeRetry(t *testing.T) {
	p := programController(t)
	preview := p.preview(t)
	p.settings.afterInstall = ErrSettingsBridgeUncertain
	p.gate.set(programStartPre, programInvocationOne)
	if err := p.c.Start(context.Background(), "radio1", programInvocationOne, preview.Fingerprint); !errors.Is(err, ErrSettingsBridgeUncertain) {
		t.Fatal("uncertain install reported success", err)
	}
	record := p.pending(t, RoutingPrepared)
	if string(fileBytes(t, p.f.path)) == settingsExample || len(p.c.observer.Snapshots()) != 0 {
		t.Fatal("fixture did not fail after a proven write, or kept its receiver")
	}
	p.reopen(t)
	if !p.pending(t, RoutingPrepared).RecoveryRequired {
		t.Fatal("reopen adopted old routing")
	}
	p.settings.afterInstall = nil
	p.start(t, preview.Fingerprint, programInvocationTwo)
	if p.settings.restores != 1 || p.pending(t, RoutingInstalled).Managed.Port == record.Managed.Port {
		t.Fatal("retry did not restore and rotate first")
	}
}

func TestProgramControllerShutdownKeepsRouteUntilStoppedRecovery(t *testing.T) {
	p := programController(t)
	preview := p.preview(t)
	p.start(t, preview.Fingerprint, programInvocationOne)
	managed := fileBytes(t, p.f.path)
	p.reopen(t)
	if string(fileBytes(t, p.f.path)) != string(managed) || !p.pending(t, RoutingInstalled).RecoveryRequired {
		t.Fatal("daemon shutdown edited the running program or adopted its binding")
	}
	if err := p.c.Recover(context.Background(), "radio1"); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("recovery wrote during a running invocation", err)
	}
	p.gate.set(programStopPost, programInvocationTwo)
	if err := p.c.Stopped(context.Background(), "radio1", programInvocationOne); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("old invocation passed the real lifecycle boundary", err)
	}
	p.eligible = false // reassigned/unplugged radios still need restoration
	p.gate.set(programStopped, "")
	if err := p.c.Recover(context.Background(), "radio1"); err != nil {
		t.Fatal(err)
	}
	p.empty(t)
	if string(fileBytes(t, p.f.path)) != settingsExample {
		t.Fatal("restart recovery lost original settings")
	}
}

func TestProgramControllerConflictingRestorePreservesOperatorFile(t *testing.T) {
	p := programController(t)
	preview := p.preview(t)
	p.start(t, preview.Fingerprint, programInvocationOne)
	changed := strings.Replace(string(fileBytes(t, p.f.path)), "CurrentName=Default", "CurrentName=NewProfile", 1)
	if err := os.WriteFile(p.f.path, []byte(changed), 0640); err != nil {
		t.Fatal(err)
	}
	p.gate.set(programStopPost, programInvocationOne)
	if err := p.c.Stopped(context.Background(), "radio1", programInvocationOne); !errors.Is(err, ErrRoutingConflict) {
		t.Fatal("changed profile was overwritten", err)
	}
	p.pending(t, RoutingInstalled)
	if string(fileBytes(t, p.f.path)) != changed || len(p.c.observer.Snapshots()) != 0 {
		t.Fatal("conflict lost operator bytes or retained observation")
	}
}

func TestProgramControllerUncertainRestoreRetainsJournalAfterOriginalWrite(t *testing.T) {
	p := programController(t)
	preview := p.preview(t)
	p.start(t, preview.Fingerprint, programInvocationOne)
	p.settings.afterRestore = ErrSettingsBridgeUncertain
	p.gate.set(programStopPost, programInvocationOne)
	if err := p.c.Stopped(context.Background(), "radio1", programInvocationOne); !errors.Is(err, ErrSettingsBridgeUncertain) {
		t.Fatal("uncertain restore discarded", err)
	}
	p.pending(t, RoutingRestoring)
	if string(fileBytes(t, p.f.path)) != settingsExample {
		t.Fatal("fixture did not fail after original restoration")
	}
	p.reopen(t)
	p.settings.afterRestore = nil
	p.gate.set(programStopped, "")
	if err := p.c.Recover(context.Background(), "radio1"); err != nil {
		t.Fatal(err)
	}
	p.empty(t)
}

func TestProgramControllerRetiresMissedStopBeforeReplacement(t *testing.T) {
	p := programController(t)
	preview := p.preview(t)
	p.start(t, preview.Fingerprint, programInvocationOne)
	old := p.c.sessions["radio1"].binding
	p.start(t, preview.Fingerprint, programInvocationTwo)
	current := p.c.sessions["radio1"].binding
	if current == old || current.Address() == old.Address() || currentRoutingBinding(old) || p.settings.restores != 1 {
		t.Fatal("missed stop adopted previous session")
	}
	if err := p.c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.c.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.c.Start(context.Background(), "radio1", programInvocationTwo, preview.Fingerprint); !errors.Is(err, ErrProgramController) {
		t.Fatal("closed controller started", err)
	}
}

func TestProgramControllerDeniedRestoreRetainsDurableOwnership(t *testing.T) {
	for _, stage := range []int{1, 2} {
		t.Run(string(rune('0'+stage)), func(t *testing.T) {
			p := programController(t)
			preview := p.preview(t)
			p.start(t, preview.Fingerprint, programInvocationOne)
			managed := fileBytes(t, p.f.path)
			p.settings.onRestore = func(check int) {
				if check == stage {
					p.gate.deny = true
				}
			}
			p.gate.set(programStopPost, programInvocationOne)
			if err := p.c.Stopped(context.Background(), "radio1", programInvocationOne); !errors.Is(err, ErrProgramLifecycle) {
				t.Fatal("restore ignored lifecycle denial", err)
			}
			p.pending(t, RoutingRestoring)
			if string(fileBytes(t, p.f.path)) != string(managed) || len(p.c.observer.Snapshots()) != 0 {
				t.Fatal("denied restoration changed settings or retained observations")
			}
			p.gate.deny = false
			p.settings.onRestore = nil
			p.gate.set(programStopped, "")
			if err := p.c.Recover(context.Background(), "radio1"); err != nil {
				t.Fatal(err)
			}
			p.empty(t)
		})
	}
}

func TestProgramControllerJournalFailuresNeverPublishAnUnownedSession(t *testing.T) {
	for _, phase := range []RoutingPhase{RoutingPrepared, RoutingInstalled} {
		for _, committed := range []bool{false, true} {
			t.Run(string(phase)+"/"+map[bool]string{false: "before-commit", true: "after-commit"}[committed], func(t *testing.T) {
				p := programController(t)
				preview := p.preview(t)
				write := p.c.journal.write
				p.c.journal.write = func(raw []byte) error {
					data, err := decodeRoutingJournal(raw)
					if err != nil {
						t.Fatal(err)
					}
					if data.Records["radio1"].Phase == phase {
						if committed {
							if err := write(raw); err != nil {
								t.Fatal(err)
							}
						}
						return errors.New("injected uncertain journal commit")
					}
					return write(raw)
				}
				p.gate.set(programStartPre, programInvocationOne)
				if err := p.c.Start(context.Background(), "radio1", programInvocationOne, preview.Fingerprint); !errors.Is(err, ErrRoutingJournalUncertain) {
					t.Fatal("journal uncertainty reported success", err)
				}
				if len(p.c.sessions) != 0 || len(p.c.observer.Snapshots()) != 0 {
					t.Fatal("failed commit published an observation session")
				}
				if phase == RoutingPrepared && p.settings.installs != 0 {
					t.Fatal("file changed before durable preparation")
				}
				p.reopen(t)
				p.gate.set(programStopped, "")
				if err := p.c.Recover(context.Background(), "radio1"); err != nil {
					t.Fatal(err)
				}
				p.empty(t)
				if string(fileBytes(t, p.f.path)) != settingsExample {
					t.Fatal("reconciliation did not retain original settings")
				}
			})
		}
	}
}

func TestProgramControllerSerializesIndependentRadioTransactions(t *testing.T) {
	p := programController(t)
	radio2 := filepath.Join(p.f.home, ".config", "WSJT-X - radio2.ini")
	if err := os.WriteFile(radio2, []byte(settingsExample), 0640); err != nil {
		t.Fatal(err)
	}
	if err := os.Chown(radio2, 1000, 1000); err != nil {
		t.Fatal(err)
	}
	one := p.preview(t)
	two, err := p.c.Preview(context.Background(), "radio2")
	if err != nil {
		t.Fatal(err)
	}
	p.gate.set(programStartPre, programInvocationOne)
	if err := p.c.Start(context.Background(), "radio2", programInvocationOne, one.Fingerprint); !errors.Is(err, ErrRoutingChanged) {
		t.Fatal("another radio's approval accepted", err)
	}
	results := make(chan error, 2)
	for _, preview := range []ProgramRoutingPreview{one, two} {
		go func() {
			results <- p.c.Start(context.Background(), preview.Radio, programInvocationOne, preview.Fingerprint)
		}()
	}
	for i := 0; i < 2; i++ {
		if err := <-results; err != nil {
			t.Fatal(err)
		}
	}
	b1, b2 := p.c.sessions["radio1"].binding, p.c.sessions["radio2"].binding
	if b1.Address() == b2.Address() || len(p.c.observer.Snapshots()) != 2 {
		t.Fatal("two radio sessions shared a receiver")
	}
	p.gate.set(programStopPost, programInvocationOne)
	if err := p.c.Stopped(context.Background(), "radio1", programInvocationOne); err != nil {
		t.Fatal(err)
	}
	if currentRoutingBinding(b1) || !currentRoutingBinding(b2) || string(fileBytes(t, radio2)) == settingsExample {
		t.Fatal("stopping radio1 altered radio2")
	}
	if err := p.c.Stopped(context.Background(), "radio2", programInvocationOne); err != nil {
		t.Fatal(err)
	}
	p.empty(t)
	if string(fileBytes(t, radio2)) != settingsExample {
		t.Fatal("radio2 did not regain its own original settings")
	}
}

func TestProgramControllerComposesConcreteLifecycleGateAndCrossUserBridge(t *testing.T) {
	p := programController(t)
	manager := programSystemd(t)
	manager.gate.procRoot = "/proc"
	p.c.lifecycle = manager.gate
	manager.raw = fixtureProgramUnit("inactive", "dead", "", "")
	preview, err := p.c.Preview(context.Background(), "radio1")
	if err != nil {
		t.Fatal(err)
	}
	manager.raw = fixtureProgramUnit("activating", "start-pre", programInvocationOne, manager.group)
	if err := p.c.Start(context.Background(), "radio1", programInvocationOne, preview.Fingerprint); err != nil {
		t.Fatal(err)
	}
	managed := fileBytes(t, p.f.path)
	manager.raw = fixtureProgramUnit("active", "running", programInvocationOne, manager.group)
	if err := p.c.Stopped(context.Background(), "radio1", programInvocationOne); !errors.Is(err, ErrProgramLifecycle) {
		t.Fatal("concrete running state did not protect settings", err)
	}
	if string(fileBytes(t, p.f.path)) != string(managed) || len(p.c.observer.Snapshots()) != 1 {
		t.Fatal("refused running-program operation changed the active session")
	}
	manager.raw = fixtureProgramUnit("deactivating", "stop-post", programInvocationOne, manager.group)
	if err := p.c.Stopped(context.Background(), "radio1", programInvocationOne); err != nil {
		t.Fatal(err)
	}
	p.empty(t)
	if string(fileBytes(t, p.f.path)) != settingsExample {
		t.Fatal("composed lifecycle did not restore original settings")
	}
}

func TestProgramControllerContendingRequestHonorsEarlierDeadline(t *testing.T) {
	p := programController(t)
	preview := p.preview(t)
	p.gate.set(programStartPre, programInvocationOne)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	p.c.eligible = func(ctx context.Context, _ string) error {
		once.Do(func() { close(entered) })
		select {
		case <-release:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	started := make(chan error, 1)
	go func() {
		started <- p.c.Start(context.Background(), "radio1", programInvocationOne, preview.Fingerprint)
	}()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("start did not enter the held lifecycle transaction")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	contending := make(chan error, 1)
	go func() { contending <- p.c.Recover(ctx, "radio1") }()
	select {
	case err := <-contending:
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal("earlier deadline lost", err)
		}
	case <-time.After(time.Second):
		t.Fatal("contending request waited for an unrelated transaction")
	}
	unblock()
	if err := <-started; err != nil {
		t.Fatal(err)
	}
	p.pending(t, RoutingInstalled)
	if p.settings.restores != 0 {
		t.Fatal("expired contending operation later changed routing")
	}
}
