package wsjtx

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/programstate"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/settings"
)

func mutationStore(t *testing.T, f *serviceFixture) *programstate.Store {
	t.Helper()
	s, err := programstate.NewStore(filepath.Dir(f.path))
	if err != nil {
		t.Fatal(err)
	}
	return s
}

type mutationApprovals struct {
	RoutingApprovalStore
	has func() (bool, error)
}

func (s *mutationApprovals) HasRoutingApprovals() (bool, error) { return s.has() }

func TestNativeReleaseMutationLeaseCoversAuditThroughCancellation(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root native mutation storage")
	}
	for _, action := range []string{"resume", "offline-pause"} {
		t.Run(action, func(t *testing.T) {
			approvals := &mutationApprovals{RoutingApprovalStore: &settings.Store{Dir: filepath.Join(t.TempDir(), "preferences")}, has: func() (bool, error) { return false, nil }}
			path := filepath.Join(t.TempDir(), "station", "wsjtx")
			service := newProgramService(path, approvals, func() (*ProgramController, error) { return nil, ErrProgramNotConfigured })
			t.Cleanup(func() { _ = service.Close() })
			paused, err := service.blockRelease(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			store, err := programstate.NewStore(filepath.Dir(path))
			if err != nil {
				t.Fatal(err)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var once sync.Once
			unblock := func() { once.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			approvals.has = func() (bool, error) {
				close(entered)
				<-release
				return false, nil
			}
			authorityReleased := make(chan struct{})
			service.resumeLease = func(context.Context) (func(), error) {
				return func() { close(authorityReleased) }, nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				var err error
				if action == "resume" {
					_, err = service.resumeRelease(ctx, paused.Receipt)
				} else {
					_, err = CallProgramRelease(ctx, filepath.Join(t.TempDir(), "missing"), path, approvals, "release-block", "")
				}
				done <- err
			}()
			select {
			case <-entered:
			case err := <-done:
				t.Fatal("audit was not entered", err)
			case <-time.After(3 * time.Second):
				t.Fatal("audit was not entered")
			}
			refuseMutationReader(t, store)
			cancel()
			refuseMutationReader(t, store)
			if action == "resume" {
				select {
				case <-authorityReleased:
					t.Error("authority released before canceled audit finished")
				default:
				}
			}
			unblock()
			if err := <-done; !errors.Is(err, context.Canceled) {
				t.Fatal("canceled audit reported success", err)
			}
			if action == "resume" {
				select {
				case <-authorityReleased:
				default:
					t.Fatal("authority leaked after mutation completed")
				}
			}
			state, err := service.releaseState(context.Background())
			if err != nil || !state.Paused {
				t.Fatal("canceled audit cleared pause", state, err)
			}
			if err := store.WithReadLease(context.Background(), func(context.Context, programstate.Generation) error { return nil }); err != nil {
				t.Fatal("completed audit leaked mutation lock", err)
			}
		})
	}
}

func refuseMutationReader(t *testing.T, store *programstate.Store) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	called := false
	err := store.WithReadLease(ctx, func(context.Context, programstate.Generation) error {
		called = true
		return nil
	})
	if called || !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("reader entered unfinished native mutation: called=%v err=%v", called, err)
	}
}

func TestNativeMutationLeaseCoversApprovalAndStartWork(t *testing.T) {
	for _, action := range []string{"enable", "start"} {
		t.Run(action, func(t *testing.T) {
			f := programService(t)
			view := f.approve(t)
			if action == "start" {
				f.p.gate.set(programStartPre, programInvocationOne)
			}
			entered, release := make(chan struct{}), make(chan struct{})
			var enteredOnce, releaseOnce sync.Once
			unblock := func() { releaseOnce.Do(func() { close(release) }) }
			t.Cleanup(unblock)
			f.p.c.eligible = func(context.Context, string) error {
				enteredOnce.Do(func() { close(entered) })
				<-release // Deliberately ignore cancellation until work has finished.
				return nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			done := make(chan error, 1)
			go func() {
				if action == "enable" {
					done <- f.service.Enable(ctx, "radio1", view.Fingerprint)
				} else {
					done <- f.service.hook(ctx, "start", "radio1", programInvocationOne)
				}
			}()
			select {
			case <-entered:
			case err := <-done:
				t.Fatalf("mutation did not enter work: %v", err)
			case <-time.After(3 * time.Second):
				t.Fatal("mutation did not enter work")
			}
			store := mutationStore(t, f)
			refuseMutationReader(t, store)
			cancel()
			refuseMutationReader(t, store)
			unblock()
			select {
			case err := <-done:
				if err == nil {
					t.Error("canceled mutation succeeded")
				}
			case <-time.After(3 * time.Second):
				t.Fatal("mutation did not finish")
			}
			if err := store.WithReadLease(context.Background(), func(context.Context, programstate.Generation) error { return nil }); err != nil {
				t.Fatal("mutation did not release its lease", err)
			}
		})
	}
}

func TestNativeReleasePauseOwnsMutationLeaseBeforeServiceMutex(t *testing.T) {
	f := programService(t)
	f.approve(t)
	store := mutationStore(t, f)
	before, err := store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	f.service.lock <- struct{}{}
	locked := true
	defer func() {
		if locked {
			<-f.service.lock
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := f.service.blockRelease(ctx); done <- err }()
	for {
		current, err := store.Read(ctx)
		if err == nil && !before.Same(current) {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("pause returned before service mutex: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Millisecond):
		}
	}
	refuseMutationReader(t, store)
	<-f.service.lock
	locked = false
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestNativeReleaseResumeAcquiresAuthorityOutsideMutationAndServiceLocks(t *testing.T) {
	f := programService(t)
	f.approve(t)
	paused, err := f.service.blockRelease(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	store := mutationStore(t, f)
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	unblock := func() { once.Do(func() { close(release) }) }
	t.Cleanup(unblock)
	f.service.resumeLease = func(ctx context.Context) (func(), error) {
		probeCtx, cancel := context.WithTimeout(ctx, 100*time.Millisecond)
		defer cancel()
		if err := f.service.acquire(probeCtx); err != nil {
			return nil, errors.New("authority acquired while holding service mutex")
		}
		<-f.service.lock
		if err := store.WithReadLease(probeCtx, func(context.Context, programstate.Generation) error { return nil }); err != nil {
			return nil, errors.New("authority acquired while holding mutation lease")
		}
		close(entered)
		select {
		case <-release:
			return func() {}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := f.service.resumeRelease(ctx, paused.Receipt); done <- err }()
	select {
	case <-entered:
	case err := <-done:
		t.Fatalf("authority ordering refused: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	newer, err := f.service.blockRelease(ctx)
	if err != nil || newer.Receipt == paused.Receipt {
		unblock()
		t.Fatal("pending resume blocked a newer pause", err)
	}
	unblock()
	if err := <-done; !errors.Is(err, ErrProgramReleaseReceipt) {
		t.Fatal("resume did not recheck exact receipt", err)
	}
	after, err := f.service.releaseState(ctx)
	if err != nil || after != newer || !after.Paused {
		t.Fatal("retired resume changed newer pause", after, err)
	}
}
