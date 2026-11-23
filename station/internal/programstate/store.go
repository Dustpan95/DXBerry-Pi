// Package programstate stores a private generation for future managed-program
// lifecycle tracking. A generation alone proves neither tracking coverage nor
// that a program is stopped, and grants no radio permission.
package programstate

import (
	"context"
	"crypto/rand"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
)

var (
	ErrUnavailable = errors.New("program generation unavailable")
	ErrUncertain   = errors.New("program generation may have changed")
)

// Store uses a trusted root-owned state directory. Construction is
// lazy. PrepareParent may create only the final state directory; Advance and
// WithAdvanceLease may create its private child or write generation files.
// The production owner is always root, including when constructed by another UID.
type Store struct {
	state  string
	random io.Reader
	sync   func(*os.File) error
}

func NewStore(stateDir string) (*Store, error) {
	if !filepath.IsAbs(stateDir) || filepath.Clean(stateDir) != stateDir || stateDir == "/" || strings.ContainsRune(stateDir, 0) {
		return nil, ErrUnavailable
	}
	return &Store{state: stateDir, random: rand.Reader, sync: func(f *os.File) error { return f.Sync() }}, nil
}

type directoryID struct {
	dev, ino       uint64
	mode, uid, gid uint32
}

type fileStamp struct {
	directoryID
	links             uint64
	size              int64
	mtimeSec, mtimeNS int64
	ctimeSec, ctimeNS int64
}

// Generation is private, immutable process-local evidence from a successful
// read. JSON encodes it as {}. Compare two fresh reads from the same Store;
// Same performs no I/O and does not extend freshness or reserve an operation.
type Generation struct {
	store             *Store
	parent, directory directoryID
	file              fileStamp
	token             [32]byte
}

func (g Generation) Same(other Generation) bool {
	return g.store != nil && g.token != [32]byte{} && g == other
}

func unavailable(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	return ErrUnavailable
}
