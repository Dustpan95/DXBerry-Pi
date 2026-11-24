//go:build wsjtxcompat && wsjtxmanager && wsjtxscreen

package wsjtx

import (
	"context"
	"encoding/binary"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Observe the unchanged helper's real atomic replacement, then kill the server
// before it acknowledges installation in the durable journal. The observed
// file and journal must prove that window; a late injection is a failure.
func (f *routingScreenFixture) interruptWrite(t *testing.T, ctx context.Context, unit, path string) string {
	t.Helper()
	if f.process == nil {
		t.Fatal("helper interruption requires a separate actual server process")
	}
	fd, err := syscall.InotifyInit1(syscall.IN_CLOEXEC | syscall.IN_NONBLOCK)
	if err != nil {
		t.Fatal(err)
	}
	watch := os.NewFile(uintptr(fd), "actual-helper-rename")
	defer watch.Close()
	if _, err := syscall.InotifyAddWatch(fd, filepath.Dir(path), syscall.IN_MOVED_FROM|syscall.IN_MOVED_TO); err != nil {
		t.Fatal(err)
	}
	if err := watch.SetReadDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	startCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	start := exec.CommandContext(startCtx, "/usr/bin/systemctl", "start", unit)
	var output applicationOutput
	start.Stdout, start.Stderr = &output, &output
	start.WaitDelay = 200 * time.Millisecond
	if err := start.Start(); err != nil {
		t.Fatal(err)
	}
	waited := false
	defer func() {
		if !waited {
			_ = start.Process.Kill()
			_ = start.Wait()
		}
	}()
	fixtureWaitHelperRename(t, watch, filepath.Base(path))
	f.process.killAndWait(t)
	f.process, f.service = nil, nil
	startErr := start.Wait()
	waited = true
	if startErr == nil || startCtx.Err() != nil {
		t.Fatal("interrupted actual start-pre must fail before its deadline:", startErr, output.text())
	}
	guard, err := newSystemdProgramLifecycle()
	if err != nil || guard.check(ctx, "fixture1", "", programStopped) != nil {
		t.Fatal("failed actual unit did not establish a stopped recovery boundary:", err)
	}
	unitRaw, err := showProgramUnit(ctx, unit)
	state, parseErr := parseProgramUnit(unitRaw)
	if err != nil || parseErr != nil || state.pid != "0" {
		t.Fatal("actual application started despite interrupted start-pre")
	}
	if _, err := os.Lstat("/run/dxb-screen/screens/wsjtx-fixture1.sock"); !os.IsNotExist(err) {
		t.Fatal("screen socket appeared despite interrupted start-pre")
	}
	phases, err := os.ReadFile("/run/dxb-wsjtx/phase-events")
	if err != nil || string(phases) != "pre:refused\npost:refused\n" {
		t.Fatalf("interrupted actual CLI phase evidence mismatch: %q", phases)
	}
	// The dead server has been reaped; only now open its real checksum-checked
	// journal. Close this inspection owner before creating the next service.
	journal, err := OpenRoutingJournal(filepath.Join(routingFixtureState, "routes"))
	if err != nil {
		t.Fatal("retained actual journal unavailable:", err)
	}
	_, record, pending, lookupErr := journal.Lookup("fixture1")
	closeErr := journal.Close()
	if lookupErr != nil || closeErr != nil || !pending || record.Phase != RoutingPrepared || !record.RecoveryRequired || record.Original.fingerprint() != f.original.fingerprint() {
		t.Fatalf("actual helper interruption window missed or journal invalid: phase=%s pending=%v lookup=%v close=%v", record.Phase, pending, lookupErr, closeErr)
	}
	view, err := f.bridge.Inspect(ctx, "fixture1")
	expected := cloneRouting(f.original)
	expected.Current = record.Managed
	if err != nil || view.fingerprint() != expected.fingerprint() {
		t.Fatal("helper had not installed the actual managed routing at interruption:", err)
	}
	failedGeneration, err := f.generation.Read(ctx)
	if err != nil || failedGeneration.Same(f.before) {
		t.Fatal("failed actual acquisition did not invalidate the prior generation:", err)
	}
	f.openServer(t, ctx)
	reopened, err := f.service.CollectionState(ctx, "fixture1")
	if err != nil || !reopened.Configured || !reopened.Enabled || reopened.Phase != RoutingPrepared || !reopened.RecoveryRequired || reopened.Observation != nil || reopened.Transport != nil {
		t.Fatal("new process failed to retain the prepared recovery obligation:", err)
	}
	if err := f.service.Recover(ctx, "fixture1"); err != nil {
		t.Fatal("actual stopped recovery failed:", err)
	}
	f.active = failedGeneration
	f.stopped(t, ctx)
	f.before = failedGeneration
	return record.Managed.Port.Value
}

func fixtureWaitHelperRename(t *testing.T, watch *os.File, name string) {
	t.Helper()
	var pendingCookie uint32
	buffer := make([]byte, 64*1024)
	for {
		n, err := watch.Read(buffer)
		if err != nil {
			t.Fatal("actual helper atomic rename not observed:", err)
		}
		for offset := 0; offset < n; {
			if n-offset < syscall.SizeofInotifyEvent {
				t.Fatal("incomplete actual inotify event")
			}
			header := buffer[offset : offset+syscall.SizeofInotifyEvent]
			mask := binary.NativeEndian.Uint32(header[4:8])
			cookie := binary.NativeEndian.Uint32(header[8:12])
			length := int(binary.NativeEndian.Uint32(header[12:16]))
			end := offset + syscall.SizeofInotifyEvent + length
			if end > n || mask&syscall.IN_Q_OVERFLOW != 0 {
				t.Fatal("actual inotify evidence incomplete or overflowed")
			}
			entry := strings.TrimRight(string(buffer[offset+syscall.SizeofInotifyEvent:end]), "\x00")
			if mask&syscall.IN_MOVED_FROM != 0 && entry == "."+name+".dxb-route.pending" {
				pendingCookie = cookie
			}
			if mask&syscall.IN_MOVED_TO != 0 && entry == name {
				if cookie == 0 || cookie != pendingCookie {
					t.Fatal("profile rename was not from the actual helper pending file")
				}
				return
			}
			offset = end
		}
	}
}
