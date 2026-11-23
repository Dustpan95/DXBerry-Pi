package wsjtx

import "context"

func auditProgramRoutes(programPath string, approvals RoutingApprovalStore, current *ProgramController) error {
	exists, err := programStateExists(programPath)
	if err != nil {
		return err
	}
	if !exists {
		approved, err := approvals.HasRoutingApprovals()
		if err != nil || approved {
			return ErrRoutingJournalRecovery
		}
		return nil
	}
	if current != nil {
		records, err := current.journal.Snapshots()
		if err != nil {
			return err
		}
		if len(records) != 0 {
			return ErrRoutingJournalRecovery
		}
		return nil
	}
	h, err := openEndpointHistory(programPath + "/ports")
	if err != nil {
		return err
	}
	defer h.close()
	j, err := OpenRoutingJournal(programPath + "/routes")
	if err != nil {
		return err
	}
	defer j.Close()
	records, err := j.Snapshots()
	if err != nil {
		return err
	}
	if len(records) != 0 {
		return ErrRoutingJournalRecovery
	}
	return nil
}

func (s *ProgramService) blockRelease(ctx context.Context) (ReleaseState, error) {
	var state ReleaseState
	err := withProgramMutation(ctx, s.path, true, func(ctx context.Context) error {
		var err error
		state, err = s.pauseRelease(ctx)
		return err
	})
	return state, err
}

func (s *ProgramService) pauseRelease(ctx context.Context) (ReleaseState, error) {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return ReleaseState{}, err
	}
	defer finish()
	if err := ctx.Err(); err != nil {
		return ReleaseState{}, err
	}
	state, err := PauseRelease(releasePath(s.path))
	if err != nil {
		return ReleaseState{}, err
	}
	// Pause precedes audit even for an unconfigured station, so later
	// initialization/approval cannot race the updater's tree transition.
	return state, auditProgramRoutes(s.path, s.approvals, s.controller)
}

func (s *ProgramService) releaseState(ctx context.Context) (ReleaseState, error) {
	_, finish, err := s.begin(ctx)
	if err != nil {
		return ReleaseState{}, err
	}
	defer finish()
	state, _, err := ReadReleaseState(releasePath(s.path))
	return state, err
}

func (s *ProgramService) resumeRelease(ctx context.Context, receipt string) (ReleaseState, error) {
	ctx, cancel := context.WithTimeout(ctx, programControlTimeout)
	defer cancel()
	if err := advanceProgramGeneration(ctx, s.path, false); err != nil {
		return ReleaseState{}, err
	}
	// Inspect under the service mutex, then release it before acquiring external
	// release coordination. A parent updater can hold that coordination while
	// waiting for a native pause. Retain neither service nor mutation lock here.
	state, authority, err := s.resumeAuthority(ctx, receipt)
	if err != nil || authority == nil {
		return state, err
	}
	release, err := authority(ctx)
	if err != nil {
		return ReleaseState{}, err
	}
	if release == nil {
		return ReleaseState{}, ErrProgramReleaseState
	}
	defer release()
	var result ReleaseState
	err = withProgramMutation(ctx, s.path, false, func(ctx context.Context) error {
		var err error
		result, err = s.resumeUnderLease(ctx, receipt)
		return err
	})
	return result, err
}

func (s *ProgramService) resumeAuthority(ctx context.Context, receipt string) (ReleaseState, func(context.Context) (func(), error), error) {
	_, finish, err := s.begin(ctx)
	if err != nil {
		return ReleaseState{}, nil, err
	}
	defer finish()
	state, exists, err := ReadReleaseState(releasePath(s.path))
	if err != nil {
		return ReleaseState{}, nil, err
	}
	if !exists || state.Receipt != receipt {
		return ReleaseState{}, nil, ErrProgramReleaseReceipt
	}
	if !state.Paused {
		return state, nil, nil
	} // retry of a committed result changes nothing
	if s.resumeLease == nil {
		return ReleaseState{}, nil, ErrProgramReleaseState
	}
	return state, s.resumeLease, nil
}

func (s *ProgramService) resumeUnderLease(ctx context.Context, receipt string) (ReleaseState, error) {
	ctx, finish, err := s.begin(ctx)
	if err != nil {
		return ReleaseState{}, err
	}
	defer finish()
	state, exists, err := ReadReleaseState(releasePath(s.path))
	if err != nil {
		return ReleaseState{}, err
	}
	if !exists || state.Receipt != receipt {
		return ReleaseState{}, ErrProgramReleaseReceipt
	}
	if !state.Paused {
		return state, nil
	}
	if err := auditProgramRoutes(s.path, s.approvals, s.controller); err != nil {
		return ReleaseState{}, err
	}
	if err := ctx.Err(); err != nil {
		return ReleaseState{}, err
	}
	if err := ResumeRelease(releasePath(s.path), receipt); err != nil {
		return ReleaseState{}, err
	}
	state, _, err = ReadReleaseState(releasePath(s.path))
	return state, err
}
