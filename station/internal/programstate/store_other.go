//go:build !linux

package programstate

import "context"

func (s *Store) PrepareParent(ctx context.Context) error {
	return unavailable(ctx)
}

func (s *Store) Read(ctx context.Context) (Generation, error) {
	return Generation{}, unavailable(ctx)
}

func (s *Store) Advance(ctx context.Context) (Generation, error) {
	return Generation{}, unavailable(ctx)
}

func (s *Store) WithReadLease(ctx context.Context, callback func(context.Context, Generation) error) error {
	return unavailable(ctx)
}

func (s *Store) WithAdvanceLease(ctx context.Context, callback func(context.Context, Generation) error) error {
	return unavailable(ctx)
}
