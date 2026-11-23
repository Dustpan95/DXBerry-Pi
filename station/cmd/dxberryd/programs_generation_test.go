package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/programstate"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/wsjtx"
)

func TestNativeInitializationRefusesBrokenGenerationBeforeInventory(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root native initialization")
	}
	root := t.TempDir()
	state := filepath.Join(root, "station")
	store, err := programstate.NewStore(state)
	if err != nil {
		t.Fatal(err)
	}
	if err = store.PrepareParent(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err = store.Advance(context.Background()); err != nil {
		t.Fatal(err)
	}
	epoch := filepath.Join(state, "program-state", "epoch")
	if err = os.WriteFile(epoch, []byte("damaged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err = runProgramMode(context.Background(), state, filepath.Join(root, "run"), root, "", "", "", true); err == nil {
		t.Fatal("native initialization ignored broken tracker")
	}
	if _, err = os.Lstat(filepath.Join(state, "wsjtx")); !os.IsNotExist(err) {
		t.Fatal("failed tracking initialized inventory")
	}
	raw, _ := os.ReadFile(epoch)
	if string(raw) != "damaged\n" {
		t.Fatal("damaged tracker repaired")
	}
}

func TestNativeInitializationRechecksPauseAfterMutationLockWait(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("root native initialization")
	}
	state, releaseRun := t.TempDir(), t.TempDir()
	store, err := programstate.NewStore(state)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.Advance(context.Background()); err != nil {
		t.Fatal(err)
	}
	lockInfo, err := os.Stat(filepath.Join(state, "program-state", "lock"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	done := make(chan error, 1)
	err = store.WithReadLease(ctx, func(context.Context, programstate.Generation) error {
		go func() {
			done <- runProgramMode(ctx, state, filepath.Join(releaseRun, "missing"), releaseRun, "", "", "", true)
		}()
		deadline := time.Now().Add(time.Second)
		for {
			entries, err := os.ReadDir("/proc/self/fd")
			if err != nil {
				return err
			}
			count := 0
			for _, entry := range entries {
				info, err := os.Stat(filepath.Join("/proc/self/fd", entry.Name()))
				if err == nil && os.SameFile(lockInfo, info) {
					count++
				}
			}
			if count >= 2 {
				break
			}
			if time.Now().After(deadline) {
				return errors.New("initialization did not wait on held generation lock")
			}
			select {
			case err := <-done:
				return errors.Join(errors.New("initialization returned before lock wait"), err)
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(time.Millisecond):
			}
		}
		// Metadata-only simulation of a pause appearing after the first check.
		// No real updater, systemd manager or program workload runs here.
		_, err := wsjtx.PauseRelease(filepath.Join(state, "wsjtx-release"))
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-done:
		if !errors.Is(err, wsjtx.ErrProgramReleasePaused) {
			t.Fatal("initialization ignored pause published during lock wait", err)
		}
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if _, err := os.Lstat(filepath.Join(state, "wsjtx")); !os.IsNotExist(err) {
		t.Fatal("paused initialization created inventory", err)
	}
}
