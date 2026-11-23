package wsjtx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"syscall"
)

var ErrProgramNotConfigured = errors.New("WSJT-X native collection is not configured")

type RoutingApprovalStore interface {
	RoutingApproval(string) (string, error)
	SaveRoutingApproval(radio, expected, approved string) error
	HasRoutingApprovals() (bool, error)
}

// InitializeProgramState is an explicit, root-only initialization of a NEW
// collection inventory. Existing/partial state is never reset or completed.
// The latest inventory must be retained through upgrades and rollback.
func InitializeProgramState(path string) error {
	if os.Geteuid() != 0 || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ErrProgramController
	}
	if err := os.Mkdir(path, 0700); err != nil {
		return err
	}
	if err := CreateEndpointHistory(filepath.Join(path, "ports")); err != nil {
		return err
	}
	if err := CreateRoutingJournal(filepath.Join(path, "routes")); err != nil {
		return err
	}
	parent, err := os.Open(filepath.Dir(path))
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}

type ProgramServiceConfig struct {
	StatePath   string
	Approvals   RoutingApprovalStore
	Eligible    func(context.Context, string) error
	ResumeLease func(context.Context) (func(), error)
}

// ProgramService shares serialized approval and lifecycle operations between
// authenticated HTTP requests and root-only unit hooks. State opening is lazy:
// an unconfigured station needs no helper/account to keep its existing apps.
type ProgramService struct {
	lock        chan struct{}
	path        string
	approvals   RoutingApprovalStore
	open        func() (*ProgramController, error)
	controller  *ProgramController
	closed      bool
	resumeLease func(context.Context) (func(), error)
}

func NewProgramService(config ProgramServiceConfig) (*ProgramService, error) {
	if !filepath.IsAbs(config.StatePath) || filepath.Clean(config.StatePath) != config.StatePath || config.Approvals == nil || config.Eligible == nil {
		return nil, ErrProgramController
	}
	s := newProgramService(config.StatePath, config.Approvals, func() (*ProgramController, error) {
		return OpenProgramController(ProgramControllerConfig{HistoryPath: filepath.Join(config.StatePath, "ports"), JournalPath: filepath.Join(config.StatePath, "routes"), Eligible: config.Eligible})
	})
	s.resumeLease = config.ResumeLease
	return s, nil
}

func newProgramService(path string, approvals RoutingApprovalStore, open func() (*ProgramController, error)) *ProgramService {
	return &ProgramService{lock: make(chan struct{}, 1), path: path, approvals: approvals, open: open}
}

func (s *ProgramService) acquire(ctx context.Context) error {
	select {
	case s.lock <- struct{}{}:
		if err := ctx.Err(); err != nil {
			<-s.lock
			return err
		}
		if s.closed {
			<-s.lock
			return ErrProgramController
		}
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (s *ProgramService) begin(ctx context.Context) (context.Context, func(), error) {
	ctx, cancel := context.WithTimeout(ctx, programControlTimeout)
	if err := s.acquire(ctx); err != nil {
		cancel()
		return ctx, nil, err
	}
	return ctx, func() { <-s.lock; cancel() }, nil
}

func (s *ProgramService) get() (*ProgramController, error) {
	if s.controller != nil {
		return s.controller, nil
	}
	exists, err := programStateExists(s.path)
	if err == nil && !exists {
		return nil, ErrProgramNotConfigured
	}
	if err != nil {
		return nil, err
	}
	s.controller, err = s.open()
	if err == nil {
		s.controller.acquisitionPermit = func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			return CheckProgramAcquisition(s.path)
		}
	}
	return s.controller, err
}

func programStateExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil || !info.IsDir() || info.Mode().Perm() != 0700 {
		return false, ErrProgramController
	}
	if stat, ok := info.Sys().(*syscall.Stat_t); !ok || stat.Uid != uint32(os.Geteuid()) {
		return false, ErrProgramController
	}
	return true, nil
}

func (s *ProgramService) Preview(ctx context.Context, radio string) (ProgramRoutingPreview, error) {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return ProgramRoutingPreview{}, err
	}
	defer finish()
	if _, err := s.approvals.RoutingApproval(radio); err != nil {
		return ProgramRoutingPreview{}, err
	}
	c, err := s.get()
	if err != nil {
		return ProgramRoutingPreview{}, err
	}
	return c.Preview(ctx, radio)
}

func (s *ProgramService) Enable(ctx context.Context, radio, fingerprint string) error {
	return withProgramMutation(ctx, s.path, false, func(ctx context.Context) error {
		return s.enable(ctx, radio, fingerprint)
	})
}

func (s *ProgramService) enable(ctx context.Context, radio, fingerprint string) error {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if err := CheckProgramAcquisition(s.path); err != nil {
		return err
	}
	old, err := s.approvals.RoutingApproval(radio)
	if err != nil {
		return err
	}
	c, err := s.get()
	if err != nil {
		return err
	}
	preview, err := c.Preview(ctx, radio)
	if err != nil {
		return err
	}
	if fingerprint == "" || fingerprint != preview.Fingerprint {
		return ErrRoutingChanged
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.approvals.SaveRoutingApproval(radio, old, fingerprint)
}

// Disable requires a stopped program and verified restoration before removing
// consent. A running Qt file is never edited as a side effect of preference UI.
func (s *ProgramService) Disable(ctx context.Context, radio string) error {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	old, err := s.approvals.RoutingApproval(radio)
	if err != nil {
		return err
	}
	c, err := s.get()
	if errors.Is(err, ErrProgramNotConfigured) && old == "" {
		return nil
	}
	if err != nil {
		return err
	}
	if err := c.Recover(ctx, radio); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return s.approvals.SaveRoutingApproval(radio, old, "")
}

func (s *ProgramService) Recover(ctx context.Context, radio string) error {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	c, err := s.get()
	if err != nil {
		return err
	}
	return c.Recover(ctx, radio)
}

func (s *ProgramService) CollectionState(ctx context.Context, radio string) (ProgramCollectionState, error) {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return ProgramCollectionState{}, err
	}
	defer finish()
	release, _, err := ReadReleaseState(releasePath(s.path))
	if err != nil {
		return ProgramCollectionState{}, err
	}
	approval, err := s.approvals.RoutingApproval(radio)
	if err != nil {
		return ProgramCollectionState{}, err
	}
	c, err := s.get()
	if errors.Is(err, ErrProgramNotConfigured) && approval == "" {
		return ProgramCollectionState{ReleasePaused: release.Paused}, nil
	}
	if err != nil {
		return ProgramCollectionState{}, err
	}
	state, err := c.CollectionState(ctx, radio)
	state.Enabled = approval != ""
	state.ReleasePaused = release.Paused
	return state, err
}

func (s *ProgramService) hook(ctx context.Context, action, radio, invocation string) error {
	if !programRadio(radio) || action != "start" && action != "stopped" {
		return ErrInvalidSession
	}
	if action == "start" {
		exists, err := programStateExists(s.path)
		if err != nil {
			return err
		}
		if !exists {
			// This branch can only return a default-off no-op/refusal; never enter
			// a newly initialized controller without first invalidating evidence.
			_, finish, err := s.begin(ctx)
			if err != nil {
				return err
			}
			defer finish()
			if s.controller != nil {
				return ErrProgramController
			}
			approval, err := s.approvals.RoutingApproval(radio)
			if err != nil {
				return err
			}
			if approval == "" {
				return nil
			}
			return ErrProgramNotConfigured
		}
		return withProgramMutation(ctx, s.path, false, func(ctx context.Context) error {
			return s.hookCurrent(ctx, action, radio, invocation)
		})
	}
	return s.hookCurrent(ctx, action, radio, invocation)
}

// The start caller owns a mutation lease. The stopped caller remains independent
// of generation state so owed cleanup is still attempted after tracking fails.
func (s *ProgramService) hookCurrent(ctx context.Context, action, radio, invocation string) error {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return err
	}
	defer finish()
	if !programRadio(radio) || action != "start" && action != "stopped" {
		return ErrInvalidSession
	}
	if action == "stopped" {
		c, err := s.get()
		if errors.Is(err, ErrProgramNotConfigured) {
			approval, readErr := s.approvals.RoutingApproval(radio)
			if readErr == nil && approval == "" {
				return nil
			}
		}
		if err != nil {
			return err
		}
		state, err := c.CollectionState(ctx, radio)
		if err != nil {
			return err
		}
		if state.Phase == "" {
			return nil
		}
		return c.Stopped(ctx, radio, invocation)
	}
	approval, err := s.approvals.RoutingApproval(radio)
	if err != nil {
		return err
	}
	c, err := s.get()
	if errors.Is(err, ErrProgramNotConfigured) && approval == "" {
		return nil
	}
	if err != nil {
		return err
	}
	state, err := c.CollectionState(ctx, radio)
	if err != nil {
		return err
	}
	if approval == "" && state.Phase == "" {
		return nil
	}
	if approval == "" {
		return c.RestoreBeforeStart(ctx, radio, invocation)
	}
	return c.Start(ctx, radio, invocation, approval)
}

func (s *ProgramService) Close() error {
	s.lock <- struct{}{}
	defer func() { <-s.lock }()
	s.closed = true
	if s.controller != nil {
		return s.controller.Close()
	}
	return nil
}
