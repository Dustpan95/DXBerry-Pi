package wsjtx

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"
)

const DefaultProgramRuntime = "/run/dxberry/station-programs"

type programHookRequest struct {
	Version    int    `json:"version"`
	Action     string `json:"action"`
	Radio      string `json:"radio"`
	Invocation string `json:"invocation"`
	Receipt    string `json:"receipt,omitempty"`
}

type programHookReply struct {
	Version int           `json:"version"`
	Action  string        `json:"action"`
	Radio   string        `json:"radio"`
	OK      bool          `json:"ok"`
	Code    string        `json:"code"`
	Release *ReleaseState `json:"release,omitempty"`
}

func programErrorCode(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, ErrRoutingConflict), errors.Is(err, ErrRoutingChanged):
		return "conflict"
	case errors.Is(err, ErrProgramLifecycle), errors.Is(err, ErrProgramRetired):
		return "lifecycle"
	case errors.Is(err, ErrRoutingApproval):
		return "approval"
	case errors.Is(err, context.Canceled), errors.Is(err, context.DeadlineExceeded):
		return "timeout"
	case errors.Is(err, ErrProgramNotConfigured):
		return "not-configured"
	case errors.Is(err, ErrProgramReleasePaused):
		return "release-paused"
	case errors.Is(err, ErrRoutingJournalRecovery):
		return "recovery-required"
	default:
		return "unavailable"
	}
}

func rootProgramPeer(conn *net.UnixConn) bool {
	raw, err := conn.SyscallConn()
	if err != nil {
		return false
	}
	ok := false
	if err := raw.Control(func(fd uintptr) {
		cred, err := syscall.GetsockoptUcred(int(fd), syscall.SOL_SOCKET, syscall.SO_PEERCRED)
		ok = err == nil && cred.Uid == 0
	}); err != nil {
		return false
	}
	return ok
}

// ProgramHookServer has one private root-only socket, eight bounded in-flight
// clients and named lifecycle actions. It never accepts a settings path,
// destination, approval, shell command or browser credential.
type ProgramHookServer struct {
	state    *privateStateDir
	listener *net.UnixListener
	service  *ProgramService
	ctx      context.Context
	cancel   context.CancelFunc
	once     sync.Once
}

func ListenProgramHooks(runtimeDir string, service *ProgramService) (*ProgramHookServer, error) {
	if os.Geteuid() != 0 || service == nil || !filepath.IsAbs(runtimeDir) || filepath.Clean(runtimeDir) != runtimeDir {
		return nil, ErrProgramController
	}
	if err := os.MkdirAll(filepath.Dir(runtimeDir), 0755); err != nil {
		return nil, err
	}
	_, err := os.Lstat(runtimeDir)
	state, err := openPrivateStateDir(runtimeDir, errors.Is(err, os.ErrNotExist))
	if err != nil {
		return nil, err
	}
	fail := func(err error) (*ProgramHookServer, error) { _ = state.close(); return nil, err }
	path := filepath.Join(runtimeDir, "wsjtx.sock")
	if info, err := os.Lstat(path); err == nil {
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || info.Mode()&os.ModeSocket == 0 || info.Mode().Perm() != 0600 {
			return fail(ErrProgramController)
		}
		// Exclusive runtime ownership is held before removing a stale socket.
		if err := os.Remove(path); err != nil {
			return fail(err)
		}
	} else if !errors.Is(err, os.ErrNotExist) {
		return fail(err)
	}
	listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: path, Net: "unix"})
	if err != nil {
		return fail(err)
	}
	if err := os.Chmod(path, 0600); err != nil {
		_ = listener.Close()
		return fail(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &ProgramHookServer{state: state, listener: listener, service: service, ctx: ctx, cancel: cancel}, nil
}

func (s *ProgramHookServer) Serve(ctx context.Context) error {
	stop := context.AfterFunc(ctx, func() { _ = s.Close() })
	defer stop()
	var handlers sync.WaitGroup
	defer handlers.Wait()
	slots := make(chan struct{}, 8)
	for {
		conn, err := s.listener.AcceptUnix()
		if err != nil {
			if s.ctx.Err() != nil {
				return nil
			}
			return err
		}
		select {
		case slots <- struct{}{}:
			handlers.Add(1)
			go func() { defer handlers.Done(); defer func() { <-slots }(); s.handle(conn) }()
		default:
			_ = conn.Close()
		}
	}
}

func (s *ProgramHookServer) handle(conn *net.UnixConn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(programControlTimeout))
	if !rootProgramPeer(conn) {
		_ = writeBridgeFrame(conn, programHookReply{Version: 1, Code: "denied"})
		return
	}
	if err := s.state.checkIdentity(); err != nil {
		return
	}
	_ = conn.SetReadDeadline(time.Now().Add(2 * time.Second))
	reader := bufio.NewReader(io.LimitReader(conn, 4097))
	var request programHookRequest
	if err := readBridgeFrame(reader, &request); err != nil {
		return
	}
	if _, err := reader.ReadByte(); err != io.EOF {
		return
	}
	if request.Version != 1 {
		return
	}
	ctx, cancel := context.WithTimeout(s.ctx, programControlTimeout)
	defer cancel()
	var err error
	var state *ReleaseState
	switch request.Action {
	case "start", "stopped":
		if !programRadio(request.Radio) || len(request.Invocation) > 32 || strings.Trim(request.Invocation, "0123456789abcdef") != "" || request.Receipt != "" {
			return
		}
		err = s.service.hook(ctx, request.Action, request.Radio, request.Invocation)
	case "release-block", "release-status", "release-resume":
		if request.Radio != "" || request.Invocation != "" || request.Action != "release-resume" && request.Receipt != "" || request.Action == "release-resume" && !journalID(request.Receipt) {
			return
		}
		var value ReleaseState
		switch request.Action {
		case "release-block":
			value, err = s.service.blockRelease(ctx)
		case "release-status":
			value, err = s.service.releaseState(ctx)
		case "release-resume":
			value, err = s.service.resumeRelease(ctx, request.Receipt)
		}
		state = &value
	default:
		return
	}
	_ = writeBridgeFrame(conn, programHookReply{Version: 1, Action: request.Action, Radio: request.Radio, OK: err == nil, Code: programErrorCode(err), Release: state})
}

func (s *ProgramHookServer) Close() error {
	var err error
	s.once.Do(func() { s.cancel(); err = s.listener.Close(); _ = s.state.close() })
	return err
}

// CallProgramHook is the trusted root command's client. A daemon failure may
// skip an entirely uncollected radio only after strict preferences and the
// valid durable journal prove no route is owned. Missing/damaged state never
// becomes permission to start with an unknown destination.
func CallProgramHook(ctx context.Context, runtimeDir, statePath string, approvals RoutingApprovalStore, action, radio, invocation string) error {
	if os.Geteuid() != 0 || approvals == nil || !filepath.IsAbs(runtimeDir) || filepath.Clean(runtimeDir) != runtimeDir || !filepath.IsAbs(statePath) || filepath.Clean(statePath) != statePath || !programRadio(radio) || action != "start" && action != "stopped" || len(invocation) > 32 || strings.Trim(invocation, "0123456789abcdef") != "" {
		return ErrProgramLifecycle
	}
	ctx, cancel := context.WithTimeout(ctx, programControlTimeout)
	defer cancel()
	if action == "start" {
		exists, err := programStateExists(statePath)
		if err != nil {
			return err
		}
		if exists {
			if err := advanceProgramGeneration(ctx, statePath, false); err != nil {
				return err
			}
		}
	}
	path := filepath.Join(runtimeDir, "wsjtx.sock")
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", path)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		approval, err := approvals.RoutingApproval(radio)
		if err != nil || approval != "" {
			return ErrProgramController
		}
		exists, err := programStateExists(statePath)
		if err != nil {
			return err
		}
		if !exists {
			return nil
		}
		history, err := openEndpointHistory(filepath.Join(statePath, "ports"))
		if err != nil {
			return err
		}
		defer history.close()
		j, err := OpenRoutingJournal(filepath.Join(statePath, "routes"))
		if err != nil {
			return err
		}
		defer j.Close()
		_, _, pending, err := j.Lookup(radio)
		if err != nil || pending {
			return ErrRoutingJournalRecovery
		}
		return nil
	}
	defer conn.Close()
	unix, ok := conn.(*net.UnixConn)
	if !ok || !rootProgramPeer(unix) {
		return ErrProgramController
	}
	deadline, _ := ctx.Deadline()
	_ = conn.SetDeadline(deadline)
	stop := context.AfterFunc(ctx, func() { _ = conn.Close() })
	defer stop()
	request := programHookRequest{Version: 1, Action: action, Radio: radio, Invocation: invocation}
	if err := writeBridgeFrame(conn, request); err != nil {
		return ErrProgramController
	}
	if err := unix.CloseWrite(); err != nil {
		return ErrProgramController
	}
	reader := bufio.NewReader(conn)
	var reply programHookReply
	if err := readBridgeFrame(reader, &reply); err != nil {
		return ErrProgramController
	}
	if _, err := reader.ReadByte(); err != io.EOF || reply.Version != 1 || reply.Action != action || reply.Radio != radio || !reply.OK || reply.Code != "" || reply.Release != nil {
		return ErrProgramController
	}
	return nil
}
