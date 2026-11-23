package wsjtx

import (
	"context"
	"errors"
	"path/filepath"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/programstate"
)

// Root acquisition/maintenance entries invalidate before taking the service
// mutex. A blocked tracker must not hold that mutex against stopped/recovery.
func advanceProgramGeneration(ctx context.Context, program string, prepare bool) error {
	store, err := programstate.NewStore(filepath.Dir(program))
	if err == nil && prepare {
		err = store.PrepareParent(ctx)
	}
	if err == nil {
		_, err = store.Advance(ctx)
	}
	if err != nil {
		return errors.Join(ErrProgramController, err)
	}
	return nil
}

// The root mutation owner retains its own generation lock through local work.
// Clients must release their entry stamp before IPC; child work must not acquire
// this lock recursively. Release coordination is acquired before this boundary.
func withProgramMutation(ctx context.Context, program string, prepare bool, mutation func(context.Context) error) error {
	store, err := programstate.NewStore(filepath.Dir(program))
	if err == nil && prepare {
		err = store.PrepareParent(ctx)
	}
	if err == nil {
		err = store.WithAdvanceLease(ctx, func(ctx context.Context, _ programstate.Generation) error { return mutation(ctx) })
	}
	if err != nil {
		return errors.Join(ErrProgramController, err)
	}
	return nil
}
