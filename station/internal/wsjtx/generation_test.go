package wsjtx

import (
	"context"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/programstate"
	"golang.org/x/sys/unix"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func corruptProgramGeneration(t *testing.T, f *serviceFixture) {
	t.Helper()
	store, err := programstate.NewStore(filepath.Dir(f.path))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = store.Advance(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(filepath.Dir(f.path), "program-state", "epoch"), []byte("damaged\n"), 0600); err != nil {
		t.Fatal(err)
	}
}

func TestNativeStoppedHookDoesNotWaitForGenerationWriter(t *testing.T) {
	f := programService(t)
	f.approve(t)
	view, err := f.service.Preview(context.Background(), "radio1")
	if err != nil {
		t.Fatal(err)
	}
	lock, err := os.OpenFile(filepath.Join(filepath.Dir(f.path), "program-state", "lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	info, err := lock.Stat()
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.service.Enable(ctx, "radio1", view.Fingerprint) }()
	// Observe the second open description for this exact private fixture inode.
	// This proves acquisition reached its real writer-lock wait before cleanup.
	deadline := time.Now().Add(time.Second)
	for {
		entries, err := os.ReadDir("/proc/self/fd")
		if err != nil {
			t.Fatal(err)
		}
		count := 0
		for _, entry := range entries {
			other, err := os.Stat(filepath.Join("/proc/self/fd", entry.Name()))
			if err == nil && os.SameFile(info, other) {
				count++
			}
		}
		if count >= 2 {
			break
		}
		select {
		case err := <-done:
			t.Fatal("acquisition bypassed held generation lock", err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("acquisition did not reach generation lock")
		}
		time.Sleep(time.Millisecond)
	}
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer stopCancel()
	if err := f.service.hook(stopCtx, "stopped", "radio1", programInvocationOne); err != nil {
		t.Fatal("stopped hook waited on generation", err)
	}
	select {
	case err := <-done:
		t.Fatal("acquisition completed through held lock", err)
	default:
	}
	cancel()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("canceled acquisition succeeded")
		}
	case <-time.After(time.Second):
		t.Fatal("canceled acquisition stuck")
	}
}

func TestNativeClientsRefuseGenerationBeforeIPC(t *testing.T) {
	for _, action := range []string{"start", "release-block"} {
		t.Run(action, func(t *testing.T) {
			f := programService(t)
			f.approve(t)
			corruptProgramGeneration(t, f)
			runtime := t.TempDir()
			listener, err := net.ListenUnix("unix", &net.UnixAddr{Name: filepath.Join(runtime, "wsjtx.sock"), Net: "unix"})
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
			defer cancel()
			if action == "start" {
				err = CallProgramHook(ctx, runtime, f.path, f.store, "start", "radio1", programInvocationOne)
			} else {
				_, err = CallProgramRelease(ctx, runtime, f.path, f.store, "release-block", "")
			}
			if err == nil {
				t.Fatal("client accepted broken generation")
			}
			if err := listener.SetDeadline(time.Now().Add(20 * time.Millisecond)); err != nil {
				t.Fatal(err)
			}
			conn, err := listener.AcceptUnix()
			if err == nil {
				conn.Close()
				t.Fatal("failed tracking reached native IPC")
			}
			if timeout, ok := err.(net.Error); !ok || !timeout.Timeout() {
				t.Fatal(err)
			}
		})
	}
}

func TestNativeAcquisitionMutationsRefuseBrokenGeneration(t *testing.T) {
	for _, action := range []string{"enable", "start", "block", "resume"} {
		t.Run(action, func(t *testing.T) {
			f := programService(t)
			f.approve(t)
			view, err := f.service.Preview(context.Background(), "radio1")
			if err != nil {
				t.Fatal(err)
			}
			var receipt string
			if action == "resume" {
				paused, err := PauseRelease(releasePath(f.path))
				if err != nil {
					t.Fatal(err)
				}
				receipt = paused.Receipt
				f.service.resumeLease = func(context.Context) (func(), error) { return func() {}, nil }
			}
			before, _ := os.ReadFile(releasePath(f.path))
			corruptProgramGeneration(t, f)
			f.p.gate.set(programStartPre, programInvocationOne)
			switch action {
			case "enable":
				err = f.service.Enable(context.Background(), "radio1", view.Fingerprint)
			case "start":
				err = f.service.hook(context.Background(), "start", "radio1", programInvocationOne)
			case "block":
				_, err = f.service.blockRelease(context.Background())
			case "resume":
				_, err = f.service.resumeRelease(context.Background(), receipt)
			}
			if err == nil {
				t.Fatal("native mutation ignored broken generation")
			}
			after, _ := os.ReadFile(releasePath(f.path))
			if string(before) != string(after) {
				t.Fatal("failed tracking changed release state")
			}
		})
	}
}

func TestNativeGenerationRefusalPrecedesServiceMutex(t *testing.T) {
	f := programService(t)
	f.approve(t)
	view, err := f.service.Preview(context.Background(), "radio1")
	if err != nil {
		t.Fatal(err)
	}
	corruptProgramGeneration(t, f)
	f.service.lock <- struct{}{}
	defer func() { <-f.service.lock }()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- f.service.Enable(ctx, "radio1", view.Fingerprint) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("tracking refusal lost")
		}
	case <-time.After(500 * time.Millisecond):
		t.Fatal("generation refusal waited behind service mutex")
	}
}

func TestNativeStoppedHookRestoresWithBrokenGeneration(t *testing.T) {
	f := programService(t)
	f.approve(t)
	f.p.gate.set(programStartPre, programInvocationOne)
	if err := f.service.hook(context.Background(), "start", "radio1", programInvocationOne); err != nil {
		t.Fatal(err)
	}
	corruptProgramGeneration(t, f)
	f.p.gate.set(programStopPost, programInvocationOne)
	if err := f.service.hook(context.Background(), "stopped", "radio1", programInvocationOne); err != nil {
		t.Fatal("tracking blocked owed restoration", err)
	}
}
