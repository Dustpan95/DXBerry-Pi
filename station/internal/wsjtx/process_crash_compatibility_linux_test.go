//go:build wsjtxcompat && wsjtxmanager && wsjtxscreen

package wsjtx

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/settings"
)

// This private fixture channel drives actual service methods in the child. It
// neither replaces the production hook socket nor supplies simulated evidence.
type routingProcessRequest struct {
	Version  int
	Action   string
	Approval string `json:",omitempty"`
}

type routingProcessReply struct {
	Version    int
	Ready      bool                    `json:",omitempty"`
	Error      string                  `json:",omitempty"`
	Preview    *ProgramRoutingPreview  `json:",omitempty"`
	State      *ProgramCollectionState `json:",omitempty"`
	Binding    *screenBinding          `json:",omitempty"`
	Snapshot   *ReceiverSnapshot       `json:",omitempty"`
	SnapshotOK bool                    `json:",omitempty"`
}

func TestWSJTXRoutingServerProcess(t *testing.T) {
	requireWSJTXManagerGuest(t)
	if os.Getenv("DXB_ROUTING_SERVER_PROCESS") != "1" {
		t.Fatal("explicit private server-process role required")
	}
	input, output := os.NewFile(3, "fixture-control-in"), os.NewFile(4, "fixture-control-out")
	defer input.Close()
	defer output.Close()
	service, err := fixtureNewRoutingService(&settings.Store{Dir: filepath.Join(filepath.Dir(routingFixtureState), "radios")})
	if err != nil {
		t.Fatal(err)
	}
	server, err := ListenProgramHooks(routingFixtureRuntime, service)
	if err != nil {
		_ = service.Close()
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- server.Serve(ctx) }()
	defer func() {
		cancel()
		_ = server.Close()
		if err := <-done; err != nil {
			t.Error(err)
		}
		if err := service.Close(); err != nil {
			t.Error(err)
		}
	}()
	if err := writeBridgeFrame(output, routingProcessReply{Version: 1, Ready: true}); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(input)
	for {
		var request routingProcessRequest
		if err := readBridgeFrame(reader, &request); err != nil {
			if errors.Is(err, io.EOF) {
				return
			}
			t.Fatal("fixture control frame unavailable:", err)
		}
		if request.Version != 1 || request.Action != "enable" && request.Approval != "" {
			t.Fatal("invalid fixed fixture control request")
		}
		callCtx, finish := context.WithTimeout(ctx, programControlTimeout)
		reply := routingProcessReply{Version: 1}
		var callErr error
		switch request.Action {
		case "preview":
			value, err := service.Preview(callCtx, "fixture1")
			callErr, reply.Preview = err, &value
		case "enable":
			callErr = service.Enable(callCtx, "fixture1", request.Approval)
		case "disable":
			callErr = service.Disable(callCtx, "fixture1")
		case "recover":
			callErr = service.Recover(callCtx, "fixture1")
		case "state":
			value, err := service.CollectionState(callCtx, "fixture1")
			callErr, reply.State = err, &value
		case "receiver":
			observer, binding, err := fixtureRoutingReceiver(callCtx, service)
			callErr = err
			if err == nil {
				snapshot, ok := observer.Snapshot("fixture1")
				reply.Binding, reply.Snapshot, reply.SnapshotOK = &binding, &snapshot, ok
			}
		case "quit":
		default:
			callErr = ErrInvalidSession
		}
		finish()
		if callErr != nil {
			if errors.Is(callErr, ErrProgramRetired) {
				reply.Error = "retired"
			} else {
				reply.Error = programErrorCode(callErr)
			}
		}
		if err := writeBridgeFrame(output, reply); err != nil {
			t.Fatal(err)
		}
		if request.Action == "quit" {
			return
		}
	}
}

type routingServiceProcess struct {
	mu            sync.Mutex
	ctx           context.Context
	cmd           *exec.Cmd
	input, output *os.File
	reader        *bufio.Reader
	log           applicationOutput
	closed        bool
}

func startRoutingServiceProcess(t *testing.T, ctx context.Context) *routingServiceProcess {
	t.Helper()
	childIn, parentOut, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	parentIn, childOut, err := os.Pipe()
	if err != nil {
		_ = childIn.Close()
		_ = parentOut.Close()
		t.Fatal(err)
	}
	p := &routingServiceProcess{ctx: ctx, input: parentIn, output: parentOut, reader: bufio.NewReader(parentIn)}
	p.cmd = exec.CommandContext(ctx, "/fixture/wsjtx-manager.test", "-test.timeout=180s", "-test.run=^TestWSJTXRoutingServerProcess$")
	p.cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "GOMAXPROCS=2", "DXB_WSJTX_MANAGER_FIXTURE=1", "DXB_ROUTING_SERVER_PROCESS=1"}
	p.cmd.ExtraFiles = []*os.File{childIn, childOut}
	p.cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	p.cmd.WaitDelay = 200 * time.Millisecond
	p.cmd.Stdout, p.cmd.Stderr = &p.log, &p.log
	err = p.cmd.Start()
	_ = childIn.Close()
	_ = childOut.Close()
	if err != nil {
		_ = parentIn.Close()
		_ = parentOut.Close()
		t.Fatal("actual server process failed to start:", err)
	}
	_ = p.input.SetReadDeadline(time.Now().Add(8 * time.Second))
	var ready routingProcessReply
	if err := readBridgeFrame(p.reader, &ready); err != nil || ready.Version != 1 || !ready.Ready || ready.Error != "" {
		_ = p.cmd.Process.Kill()
		_ = p.finish()
		t.Fatal("actual server process did not become ready:", err, p.log.text())
	}
	return p
}

func (p *routingServiceProcess) exchange(ctx context.Context, request routingProcessRequest) (routingProcessReply, error) {
	if p.closed || ctx.Err() != nil {
		return routingProcessReply{}, errors.Join(ErrProgramController, ctx.Err())
	}
	deadline := time.Now().Add(programControlTimeout)
	if value, ok := ctx.Deadline(); ok && value.Before(deadline) {
		deadline = value
	}
	if err := p.input.SetReadDeadline(deadline); err != nil {
		return routingProcessReply{}, err
	}
	if err := p.output.SetWriteDeadline(deadline); err != nil {
		return routingProcessReply{}, err
	}
	stop := context.AfterFunc(ctx, func() { _ = p.input.SetReadDeadline(time.Now()); _ = p.output.SetWriteDeadline(time.Now()) })
	defer stop()
	request.Version = 1
	if err := writeBridgeFrame(p.output, request); err != nil {
		return routingProcessReply{}, err
	}
	var reply routingProcessReply
	if err := readBridgeFrame(p.reader, &reply); err != nil || reply.Version != 1 || reply.Ready {
		return routingProcessReply{}, errors.Join(ErrProgramController, err)
	}
	switch reply.Error {
	case "":
		return reply, nil
	case "lifecycle":
		return reply, ErrProgramLifecycle
	case "retired":
		return reply, ErrProgramRetired
	default:
		return reply, fmt.Errorf("actual child service refused: %s", reply.Error)
	}
}

func (p *routingServiceProcess) call(ctx context.Context, request routingProcessRequest) (routingProcessReply, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.exchange(ctx, request)
}

func (p *routingServiceProcess) Preview(ctx context.Context, radio string) (ProgramRoutingPreview, error) {
	if radio != "fixture1" {
		return ProgramRoutingPreview{}, ErrInvalidSession
	}
	r, err := p.call(ctx, routingProcessRequest{Action: "preview"})
	if err != nil || r.Preview == nil {
		return ProgramRoutingPreview{}, errors.Join(ErrProgramController, err)
	}
	return *r.Preview, nil
}

func (p *routingServiceProcess) Enable(ctx context.Context, radio, approval string) error {
	if radio != "fixture1" {
		return ErrInvalidSession
	}
	_, err := p.call(ctx, routingProcessRequest{Action: "enable", Approval: approval})
	return err
}

func (p *routingServiceProcess) Disable(ctx context.Context, radio string) error {
	if radio != "fixture1" {
		return ErrInvalidSession
	}
	_, err := p.call(ctx, routingProcessRequest{Action: "disable"})
	return err
}

func (p *routingServiceProcess) Recover(ctx context.Context, radio string) error {
	if radio != "fixture1" {
		return ErrInvalidSession
	}
	_, err := p.call(ctx, routingProcessRequest{Action: "recover"})
	return err
}

func (p *routingServiceProcess) CollectionState(ctx context.Context, radio string) (ProgramCollectionState, error) {
	if radio != "fixture1" {
		return ProgramCollectionState{}, ErrInvalidSession
	}
	r, err := p.call(ctx, routingProcessRequest{Action: "state"})
	if err != nil || r.State == nil {
		return ProgramCollectionState{}, errors.Join(ErrProgramController, err)
	}
	return *r.State, nil
}

func (p *routingServiceProcess) Snapshot(radio string) (ReceiverSnapshot, bool) {
	if radio != "fixture1" {
		return ReceiverSnapshot{}, false
	}
	r, err := p.call(p.ctx, routingProcessRequest{Action: "receiver"})
	if err != nil || r.Snapshot == nil {
		return ReceiverSnapshot{}, false
	}
	return *r.Snapshot, r.SnapshotOK
}

// The caller holds mu. Reap once before any later process may reuse this PID.
func (p *routingServiceProcess) finish() error {
	p.closed = true
	_ = p.output.Close()
	_ = p.input.Close()
	return p.cmd.Wait()
}

func (p *routingServiceProcess) killAndWait(t *testing.T) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		t.Fatal("server process already stopped before crash injection")
	}
	killErr := p.cmd.Process.Kill()
	err := p.finish()
	var exit *exec.ExitError
	if killErr != nil || !errors.As(err, &exit) || !exit.ProcessState.Sys().(syscall.WaitStatus).Signaled() || exit.ProcessState.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
		t.Fatal("actual server process did not exit from the injected SIGKILL:", killErr, err, p.log.text())
	}
}

func (p *routingServiceProcess) Close() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_, err := p.exchange(ctx, routingProcessRequest{Action: "quit"})
	if err != nil {
		_ = p.cmd.Process.Kill()
	}
	waitErr := p.finish()
	if err != nil || waitErr != nil {
		return fmt.Errorf("actual server process cleanup failed: %w: %s", errors.Join(err, waitErr), p.log.text())
	}
	return nil
}
