package programstate

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestAdvanceLeasePublishesBeforeMutationAndExcludesProcesses(t *testing.T) {
	s := rootStore(t)
	callbackErr := errors.New("mutation failed after possible effects")
	var published Generation
	err := s.WithAdvanceLease(context.Background(), func(ctx context.Context, g Generation) error {
		published = g
		read, err := s.Read(ctx)
		if err != nil || !g.Same(read) {
			t.Fatalf("callback did not receive published generation: %v", err)
		}
		leaseWriter(t, s, true)
		leaseProcess(t, s, true, true)
		return callbackErr
	})
	if !errors.Is(err, callbackErr) {
		t.Fatal(err)
	}
	after, err := s.Read(context.Background())
	if err != nil || !published.Same(after) {
		t.Fatalf("callback error rolled back published generation: %v", err)
	}
	leaseProcess(t, s, true, false)
	leaseWriter(t, s, false)
}

func TestAdvanceLeaseCancellationRetainsLockThroughCallback(t *testing.T) {
	s := rootStore(t)
	before := mustAdvance(t, s)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	err := s.WithAdvanceLease(ctx, func(callbackCtx context.Context, g Generation) error {
		if callbackCtx != ctx || g.Same(before) {
			t.Fatal("callback context or generation incorrect")
		}
		cancel()
		leaseWriter(t, s, true)
		leaseProcess(t, s, true, true)
		return nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	after, err := s.Read(context.Background())
	if err != nil || before.Same(after) {
		t.Fatalf("cancellation restored old generation: %v", err)
	}
	leaseWriter(t, s, false)
}

func TestAdvanceLeaseWaitsForReaderWithoutChangingGeneration(t *testing.T) {
	s := rootStore(t)
	before := mustAdvance(t, s)
	err := s.WithReadLease(context.Background(), func(context.Context, Generation) error {
		ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
		defer cancel()
		err := s.WithAdvanceLease(ctx, func(context.Context, Generation) error {
			t.Fatal("mutation bypassed held reader")
			return nil
		})
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatal(err)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	after, err := s.Read(context.Background())
	if err != nil || !before.Same(after) {
		t.Fatalf("failed acquisition changed generation: %v", err)
	}
}

func TestAdvanceLeaseStorageFailureNeverRunsMutation(t *testing.T) {
	for _, fault := range []string{"nil", "canceled", "corrupt", "sync-before", "sync-after"} {
		t.Run(fault, func(t *testing.T) {
			s := rootStore(t)
			before := mustAdvance(t, s)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			callback := func(context.Context, Generation) error {
				t.Fatal("failed storage allowed mutation")
				return nil
			}
			switch fault {
			case "nil":
				callback = nil
			case "canceled":
				cancel()
			case "corrupt":
				if err := os.WriteFile(epochPath(s), []byte("damaged\n"), 0600); err != nil {
					t.Fatal(err)
				}
			case "sync-before", "sync-after":
				calls := 0
				s.sync = func(f *os.File) error {
					calls++
					if fault == "sync-before" && calls == 2 || fault == "sync-after" && calls == 3 {
						return io.ErrClosedPipe
					}
					return f.Sync()
				}
			}
			err := s.WithAdvanceLease(ctx, callback)
			if err == nil || fault == "sync-after" && !errors.Is(err, ErrUncertain) {
				t.Fatal(err)
			}
			if fault != "corrupt" {
				after, err := s.Read(context.Background())
				if err != nil || before.Same(after) == (fault == "sync-after") {
					t.Fatalf("wrong publication outcome: %v", err)
				}
			}
		})
	}
}

func TestAdvanceLeasePostcheckCannotConfirmReplacedLock(t *testing.T) {
	s := rootStore(t)
	err := s.WithAdvanceLease(context.Background(), func(context.Context, Generation) error {
		path := filepath.Join(s.state, "program-state", "lock")
		if err := os.WriteFile(path+".new", nil, 0600); err != nil {
			t.Fatal(err)
		}
		return os.Rename(path+".new", path)
	})
	if !errors.Is(err, ErrUncertain) || !errors.Is(err, ErrUnavailable) {
		t.Fatal(err)
	}
}

func TestAdvanceLeaseCallbackOutlivesStorageDeadline(t *testing.T) {
	s := rootStore(t)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err := s.WithAdvanceLease(ctx, func(callbackCtx context.Context, _ Generation) error {
		if callbackCtx != ctx {
			t.Fatal("caller context replaced")
		}
		timer := time.NewTimer(operationLimit + 25*time.Millisecond)
		defer timer.Stop()
		select {
		case <-timer.C:
			return nil
		case <-callbackCtx.Done():
			return callbackCtx.Err()
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}
