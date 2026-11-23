package wsjtx

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
)

// CallProgramRelease carries only named root maintenance actions and an opaque
// receipt. Target paths/capability inspection stay in the trusted local command.
// Only a failed connection permits offline pause+audit, never offline resume.
func CallProgramRelease(ctx context.Context, runtime, program string, approvals RoutingApprovalStore, action, receipt string) (ReleaseState, error) {
	if os.Geteuid() != 0 || approvals == nil || !filepath.IsAbs(runtime) || filepath.Clean(runtime) != runtime || !filepath.IsAbs(program) || filepath.Clean(program) != program || action != "release-block" && action != "release-status" && action != "release-resume" || action == "release-resume" && !journalID(receipt) || action != "release-resume" && receipt != "" {
		return ReleaseState{}, ErrProgramReleaseState
	}
	ctx, cancel := context.WithTimeout(ctx, programControlTimeout)
	defer cancel()
	if action != "release-status" {
		if err := advanceProgramGeneration(ctx, program, action == "release-block"); err != nil {
			return ReleaseState{}, errors.Join(ErrProgramReleaseState, err)
		}
	}
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", filepath.Join(runtime, "wsjtx.sock"))
	if err != nil {
		if ctx.Err() != nil {
			return ReleaseState{}, ctx.Err()
		}
		if action == "release-status" {
			state, _, err := ReadReleaseState(releasePath(program))
			return state, err
		}
		if action != "release-block" {
			return ReleaseState{}, ErrProgramReleaseState
		}
		var state ReleaseState
		err := withProgramMutation(ctx, program, true, func(ctx context.Context) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			var err error
			state, err = PauseRelease(releasePath(program))
			if err != nil {
				return err
			}
			return auditProgramRoutes(program, approvals, nil)
		})
		return state, err
	}
	defer conn.Close()
	unix, ok := conn.(*net.UnixConn)
	if !ok || !rootProgramPeer(unix) {
		return ReleaseState{}, ErrProgramReleaseState
	}
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	if err := writeBridgeFrame(conn, programHookRequest{Version: 1, Action: action, Receipt: receipt}); err != nil {
		return ReleaseState{}, ErrProgramReleaseState
	}
	if err := unix.CloseWrite(); err != nil {
		return ReleaseState{}, ErrProgramReleaseState
	}
	reader := bufio.NewReader(conn)
	var reply programHookReply
	if err := readBridgeFrame(reader, &reply); err != nil {
		return ReleaseState{}, ErrProgramReleaseState
	}
	if _, err := reader.ReadByte(); err != io.EOF || reply.Version != 1 || reply.Action != action || reply.Radio != "" || reply.Release == nil {
		return ReleaseState{}, ErrProgramReleaseState
	}
	if !reply.OK {
		switch reply.Code {
		case "release-paused":
			return *reply.Release, ErrProgramReleasePaused
		case "recovery-required":
			return *reply.Release, ErrRoutingJournalRecovery
		default:
			return *reply.Release, ErrProgramReleaseState
		}
	}
	if reply.Code != "" {
		return ReleaseState{}, ErrProgramReleaseState
	}
	state := *reply.Release
	if state.Version == 0 && state != (ReleaseState{}) {
		return ReleaseState{}, ErrProgramReleaseState
	}
	if state.Version != 0 && (state.Version != 1 || !journalID(state.Receipt) || state.Revision == 0) {
		return ReleaseState{}, ErrProgramReleaseState
	}
	return state, nil
}
