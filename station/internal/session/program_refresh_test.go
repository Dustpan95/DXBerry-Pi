package session

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
)

func TestProgramProbeFailureAfterStoppedBlocksClaimsUntilRecovery(t *testing.T) {
	failure := false
	programs := NewPrograms(func() []radios.Radio { return []radios.Radio{{Name: "radio1", Owner: "wsjtx"}} }, func(context.Context, string) (bool, string, error) {
		if failure {
			return false, "", errors.New("unavailable")
		}
		return false, "WSJT-X", nil
	})
	owners := NewOwners(newClock())
	owners.SetPrograms(programs)
	programs.Refresh(context.Background())
	if err := owners.Claim("radio1", tab("a")); err != nil {
		t.Fatal(err)
	}
	owners.Release("radio1", "a")
	failure = true
	for i := 0; i < 2; i++ {
		programs.Refresh(context.Background())
		if err := owners.Claim("radio1", tab("a")); !errors.Is(err, ErrProgramOwns) {
			t.Errorf("unknown program state allowed claim: %v", err)
		}
		if err := owners.TakeOver("radio1", tab("b")); !errors.Is(err, ErrProgramOwns) {
			t.Errorf("unknown program state allowed takeover: %v", err)
		}
	}
	failure = false
	programs.Refresh(context.Background())
	if err := owners.Claim("radio1", tab("a")); err != nil {
		t.Fatal("successful stopped answer did not recover", err)
	}
}

func TestSupersededProgramRefreshCannotPublishOldFreeResult(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var calls atomic.Int32
	programs := NewPrograms(func() []radios.Radio { return []radios.Radio{{Name: "radio1", Owner: "wsjtx"}} }, func(context.Context, string) (bool, string, error) {
		if calls.Add(1) == 1 {
			close(entered)
			<-release
			return false, "WSJT-X", nil
		}
		return true, "WSJT-X", nil
	})
	done := make(chan struct{})
	go func() { defer close(done); programs.Refresh(context.Background()) }()
	<-entered
	programs.Refresh(context.Background())
	close(release)
	<-done
	owners := NewOwners(newClock())
	owners.SetPrograms(programs)
	if err := owners.Claim("radio1", tab("a")); !errors.Is(err, ErrProgramOwns) {
		t.Fatal("older free result replaced newer held result", err)
	}
}

func TestCanceledProgramRefreshCannotPublishFreeFromIgnoringProbe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	programs := NewPrograms(func() []radios.Radio { return []radios.Radio{{Name: "radio1", Owner: "wsjtx"}} }, func(context.Context, string) (bool, string, error) { cancel(); return false, "WSJT-X", nil })
	programs.Refresh(ctx)
	owners := NewOwners(newClock())
	owners.SetPrograms(programs)
	if err := owners.Claim("radio1", tab("a")); !errors.Is(err, ErrProgramOwns) {
		t.Fatal("canceled refresh published free state", err)
	}
}

func TestAssignedProgramBlocksBeforeFirstProbeCompletes(t *testing.T) {
	programs := NewPrograms(func() []radios.Radio { return []radios.Radio{{Name: "radio1", Owner: "wsjtx"}} }, func(context.Context, string) (bool, string, error) { return false, "WSJT-X", nil })
	owners := NewOwners(newClock())
	owners.SetPrograms(programs)
	if err := owners.Claim("radio1", tab("a")); !errors.Is(err, ErrProgramOwns) {
		t.Fatal("unverified startup assignment allowed claim", err)
	}
	programs.Refresh(context.Background())
	if err := owners.Claim("radio1", tab("a")); err != nil {
		t.Fatal("verified stopped initial program did not release hold", err)
	}
}
