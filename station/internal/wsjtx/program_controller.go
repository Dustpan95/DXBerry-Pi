package wsjtx

import (
	"context"
	"errors"
	"path/filepath"
	"time"
)

const programControlTimeout = 30 * time.Second

var (
	ErrProgramController = errors.New("WSJT-X program controller unavailable")
	ErrProgramLifecycle  = errors.New("WSJT-X program is not at the required stopped lifecycle boundary")
	ErrProgramRetired    = errors.New("retired WSJT-X program invocation")
)

type programBoundary string

const (
	programStartPre programBoundary = "start-pre"
	programStopPost programBoundary = "stop-post"
	programStopped  programBoundary = "stopped"
)

type programLifecycle interface {
	check(context.Context, string, string, programBoundary) error
}

type programSettings interface {
	Inspect(context.Context, string) (RoutingSettings, error)
	Install(context.Context, RoutingPatch, func(context.Context) error) (RoutingSettings, error)
	Restore(context.Context, RoutingPatch, func(context.Context) error) (RoutingSettings, error)
}

// ProgramControllerConfig contains trusted daemon startup configuration. State
// must already have been explicitly initialized; opening never repairs it.
type ProgramControllerConfig struct {
	HistoryPath string
	JournalPath string
	// Eligible must freshly verify a registered, present radio assigned to
	// WSJT-X. Restoration deliberately does not depend on current assignment.
	Eligible func(context.Context, string) error
}

// ProgramRoutingPreview is private authenticated control-plane data. Approval
// covers this radio's complete original routing namespace, including saved
// profiles. It must be explicitly obtained from the operator and persisted by
// the caller; a default or absent destination is not implicit permission.
type ProgramRoutingPreview struct {
	Radio       string          `json:"radio"`
	Fingerprint string          `json:"fingerprint"`
	Settings    RoutingSettings `json:"settings"`
}

type programSession struct {
	invocation string
	binding    Binding
	token      JournalToken
}

// ProgramController composes the receiver, durable journal and application-user
// bridge under one lifecycle lock. Start/Stopped must be called from the root
// systemd start-pre/stop-post hooks, never while the Qt program runs. There is no RF command,
// outbound UDP, public listener or automatic opt-in here.
type ProgramController struct {
	lock              chan struct{}
	observer          *Observer
	journal           *RoutingJournal
	settings          programSettings
	lifecycle         programLifecycle
	eligible          func(context.Context, string) error
	sessions          map[string]programSession
	closed            bool
	acquisitionPermit func(context.Context) error
}

// OpenProgramController uses the real root/application-user boundary and the
// fixed WSJT-X systemd session units. It takes exclusive ownership of both
// private state stores until Close. No settings are changed on open.
func OpenProgramController(config ProgramControllerConfig) (*ProgramController, error) {
	if config.Eligible == nil || !filepath.IsAbs(config.HistoryPath) || !filepath.IsAbs(config.JournalPath) ||
		filepath.Clean(config.HistoryPath) != config.HistoryPath || filepath.Clean(config.JournalPath) != config.JournalPath || config.HistoryPath == config.JournalPath {
		return nil, ErrProgramController
	}
	bridge, err := NewSettingsBridge()
	if err != nil {
		return nil, err
	}
	lifecycle, err := newSystemdProgramLifecycle()
	if err != nil {
		return nil, err
	}
	if lifecycle.uid != uint64(bridge.uid) {
		return nil, ErrSettingsBridgeIdentity
	}
	o, err := NewPersistentObserver(nil, config.HistoryPath)
	if err != nil {
		return nil, err
	}
	j, err := OpenRoutingJournal(config.JournalPath)
	if err != nil {
		_ = o.Close()
		return nil, err
	}
	c := newProgramController(o, j, bridge, lifecycle, config.Eligible)
	c.acquisitionPermit = func(ctx context.Context) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		return CheckProgramAcquisition(filepath.Dir(config.JournalPath))
	}
	return c, nil
}

func newProgramController(o *Observer, j *RoutingJournal, settings programSettings, lifecycle programLifecycle, eligible func(context.Context, string) error) *ProgramController {
	return &ProgramController{lock: make(chan struct{}, 1), observer: o, journal: j, settings: settings, lifecycle: lifecycle, eligible: eligible, sessions: make(map[string]programSession)}
}

func (c *ProgramController) acquire(ctx context.Context) error {
	select {
	case c.lock <- struct{}{}:
		if err := ctx.Err(); err != nil {
			c.release()
			return err
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *ProgramController) release() { <-c.lock }

func (c *ProgramController) check(ctx context.Context, radio, invocation string, boundary programBoundary, acquiring bool) error {
	if c.closed {
		return ErrProgramController
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if !programRadio(radio) {
		return ErrInvalidSession
	}
	if err := c.lifecycle.check(ctx, radio, invocation, boundary); err != nil {
		return err
	}
	if acquiring {
		if c.acquisitionPermit != nil {
			if err := c.acquisitionPermit(ctx); err != nil {
				return err
			}
		}
		return c.eligible(ctx, radio)
	}
	return nil
}

func programRoutingFingerprint(radio string, settings RoutingSettings) string {
	return routingDigest(struct {
		Radio    string
		Settings RoutingSettings
	}{radio, cloneRouting(settings)})
}

// Preview requires an inactive/failed unit with no remaining application
// processes. Pending recovery must be resolved first, so an old managed route
// can never be presented as the operator's original destination.
func (c *ProgramController) Preview(ctx context.Context, radio string) (ProgramRoutingPreview, error) {
	ctx, cancel := context.WithTimeout(ctx, programControlTimeout)
	defer cancel()
	if err := c.acquire(ctx); err != nil {
		return ProgramRoutingPreview{}, err
	}
	defer c.release()
	if err := c.check(ctx, radio, "", programStopped, true); err != nil {
		return ProgramRoutingPreview{}, err
	}
	if _, _, pending, err := c.journal.Lookup(radio); err != nil {
		return ProgramRoutingPreview{}, err
	} else if pending {
		return ProgramRoutingPreview{}, ErrRoutingJournalRecovery
	}
	view, err := c.settings.Inspect(ctx, radio)
	if err != nil {
		return ProgramRoutingPreview{}, err
	}
	if err := view.validate(); err != nil {
		return ProgramRoutingPreview{}, err
	}
	if err := c.check(ctx, radio, "", programStopped, true); err != nil {
		return ProgramRoutingPreview{}, err
	}
	return ProgramRoutingPreview{radio, programRoutingFingerprint(radio, view), cloneRouting(view)}, nil
}

// Start runs while systemd holds the exact invocation at ExecStartPre. The
// approved fingerprint must come from a previously approved Preview. Stable
// approval authorizes fresh endpoints only while the entire original namespace
// still matches; the per-binding lease retains its stronger generation proof.
// The caller must not let ExecStart proceed after an error.
func (c *ProgramController) Start(ctx context.Context, radio, invocation, approved string) error {
	ctx, cancel := context.WithTimeout(ctx, programControlTimeout)
	defer cancel()
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	if err := c.check(ctx, radio, invocation, programStartPre, true); err != nil {
		return err
	}
	if approved == "" {
		return ErrRoutingApproval
	}
	if session, exists := c.sessions[radio]; exists && session.invocation == invocation {
		_, record, pending, err := c.journal.Lookup(radio)
		if err != nil {
			return err
		}
		if !pending || record.Transaction != session.token.transaction || record.Phase != RoutingInstalled || record.RecoveryRequired {
			return ErrRoutingJournalRecovery
		}
		if approved != programRoutingFingerprint(radio, record.Original) {
			return ErrRoutingChanged
		}
		view, err := c.settings.Inspect(ctx, radio)
		if err != nil {
			return err
		}
		expected := cloneRouting(record.Original)
		expected.Current = record.Managed
		if view.fingerprint() != expected.fingerprint() {
			return ErrRoutingConflict
		}
		return c.check(ctx, radio, invocation, programStartPre, true)
	}
	// A previous daemon or missed stop can leave a lease, but this start-pre
	// barrier proves the old program is gone. Restore before checking approval
	// and allocating another endpoint. Never adopt a pre-restart UDP binding.
	if err := c.restore(ctx, radio, invocation, programStartPre); err != nil {
		return err
	}
	view, err := c.settings.Inspect(ctx, radio)
	if err != nil {
		return err
	}
	if err := view.validate(); err != nil {
		return err
	}
	if approved != programRoutingFingerprint(radio, view) {
		return ErrRoutingChanged
	}
	binding, err := c.observer.Start(radio, "WSJT-X - "+radio)
	if err != nil {
		return err
	}
	installed := false
	defer func() {
		if !installed {
			c.observer.Stop(binding)
		}
	}()
	preview, err := PreviewRouting(binding, view)
	if err != nil {
		return err
	}
	lease, patch, err := ClaimRouting(binding, view, preview.Fingerprint)
	if err != nil {
		return err
	}
	if err := c.check(ctx, radio, invocation, programStartPre, true); err != nil {
		return err
	}
	token, err := c.journal.Prepare(lease, binding)
	if err != nil {
		return err
	}
	guard := func(ctx context.Context) error {
		if err := c.check(ctx, radio, invocation, programStartPre, true); err != nil {
			return err
		}
		return c.journal.PermitInstall(token)
	}
	managed, err := c.settings.Install(ctx, patch, guard)
	if err != nil {
		return err // even a denied/uncertain write keeps the recovery record
	}
	if err := guard(ctx); err != nil {
		return err
	}
	if err := c.journal.MarkInstalled(token, managed); err != nil {
		return err
	}
	c.sessions[radio] = programSession{invocation, binding, token}
	installed = true
	return nil
}

// Stopped runs from ExecStopPost and matches the exact systemd invocation.
// Delayed callbacks cannot retire a newer session. After daemon restart the
// real stop-post barrier allows restoration, never adoption of the old lease.
func (c *ProgramController) Stopped(ctx context.Context, radio, invocation string) error {
	ctx, cancel := context.WithTimeout(ctx, programControlTimeout)
	defer cancel()
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	if c.closed {
		return ErrProgramController
	}
	if session, exists := c.sessions[radio]; exists && session.invocation != invocation {
		return ErrProgramRetired
	}
	return c.restore(ctx, radio, invocation, programStopPost)
}

// Recover restores an outstanding route only after an inactive/failed unit and
// absence of application processes are verified. It works after reassignment
// or removal of a radio; acquiring a new route still requires eligibility.
func (c *ProgramController) Recover(ctx context.Context, radio string) error {
	ctx, cancel := context.WithTimeout(ctx, programControlTimeout)
	defer cancel()
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	return c.restore(ctx, radio, "", programStopped)
}

// RestoreBeforeStart releases any previous collection lease before an explicitly
// uncollected session starts. It needs the same exact start-pre barrier, but no
// current acquisition eligibility or new approval.
func (c *ProgramController) RestoreBeforeStart(ctx context.Context, radio, invocation string) error {
	ctx, cancel := context.WithTimeout(ctx, programControlTimeout)
	defer cancel()
	if err := c.acquire(ctx); err != nil {
		return err
	}
	defer c.release()
	return c.restore(ctx, radio, invocation, programStartPre)
}

// ProgramCollectionState contains no original destinations, private journal
// tokens or transport endpoints. Program-reported TX is observation only.
type ProgramCollectionState struct {
	Configured       bool                   `json:"configured"`
	Enabled          bool                   `json:"enabled"`
	ReleasePaused    bool                   `json:"release_paused"`
	Phase            RoutingPhase           `json:"phase,omitempty"`
	RecoveryRequired bool                   `json:"recovery_required"`
	Observation      *Snapshot              `json:"observation,omitempty"`
	Transport        *ProgramTransportState `json:"transport,omitempty"`
}

// ProgramTransportState reports observed loss without exposing socket endpoints
// or copying operating-system errors into the browser.
type ProgramTransportState struct {
	Received    uint64 `json:"received"`
	QueueDrops  uint64 `json:"queue_drops"`
	NonLoopback uint64 `json:"non_loopback"`
	WrongPeer   uint64 `json:"wrong_peer"`
	Failed      bool   `json:"failed"`
}

func (c *ProgramController) CollectionState(ctx context.Context, radio string) (ProgramCollectionState, error) {
	ctx, cancel := context.WithTimeout(ctx, programControlTimeout)
	defer cancel()
	if err := c.acquire(ctx); err != nil {
		return ProgramCollectionState{}, err
	}
	defer c.release()
	if c.closed || !programRadio(radio) {
		return ProgramCollectionState{}, ErrProgramController
	}
	_, record, pending, err := c.journal.Lookup(radio)
	if err != nil {
		return ProgramCollectionState{}, err
	}
	out := ProgramCollectionState{Configured: true}
	if pending {
		out.Phase, out.RecoveryRequired = record.Phase, record.RecoveryRequired
		if record.Phase == RoutingInstalled && !record.RecoveryRequired {
			if observed, ok := c.observer.Snapshot(radio); ok {
				out.Observation = &observed.Observation
				out.Transport = &ProgramTransportState{
					Received: observed.Transport.Received, QueueDrops: observed.Transport.QueueDrops,
					NonLoopback: observed.Transport.NonLoopback, WrongPeer: observed.Transport.WrongPeer,
					Failed: observed.TransportError != "",
				}
			}
		}
	}
	return out, nil
}

func (c *ProgramController) restore(ctx context.Context, radio, invocation string, boundary programBoundary) error {
	if err := c.check(ctx, radio, invocation, boundary, false); err != nil {
		return err
	}
	if session, exists := c.sessions[radio]; exists {
		c.observer.Stop(session.binding)
		delete(c.sessions, radio)
	}
	token, _, pending, err := c.journal.Lookup(radio)
	if err != nil || !pending {
		return err
	}
	view, err := c.settings.Inspect(ctx, radio)
	if err != nil {
		return err
	}
	if err := c.check(ctx, radio, invocation, boundary, false); err != nil {
		return err
	}
	patch, err := c.journal.PrepareRestore(token, view)
	if err != nil {
		return err
	}
	guard := func(ctx context.Context) error {
		if err := c.check(ctx, radio, invocation, boundary, false); err != nil {
			return err
		}
		return c.journal.PermitRestore(token)
	}
	restored, err := c.settings.Restore(ctx, patch, guard)
	if err != nil {
		return err
	}
	if err := guard(ctx); err != nil {
		return err
	}
	return c.journal.CompleteRestore(token, restored)
}

// Close retires receivers and releases state ownership without editing Qt
// settings: the program may still be running. The durable record must survive
// daemon shutdown and be restored at a verified stopped boundary after reopen.
func (c *ProgramController) Close() error {
	_ = c.acquire(context.Background())
	defer c.release()
	if c.closed {
		return nil
	}
	c.closed = true
	c.sessions = nil
	return errors.Join(c.observer.Close(), c.journal.Close())
}
