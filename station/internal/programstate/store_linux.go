package programstate

import (
	"context"
	"encoding/hex"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/sys/unix"
)

const operationLimit = 2 * time.Second

func statFile(f *os.File) (fileStamp, error) {
	var st unix.Stat_t
	if unix.Fstat(int(f.Fd()), &st) != nil {
		return fileStamp{}, ErrUnavailable
	}
	return fileStamp{directoryID{uint64(st.Dev), st.Ino, st.Mode, st.Uid, st.Gid}, uint64(st.Nlink), st.Size, st.Mtim.Sec, st.Mtim.Nsec, st.Ctim.Sec, st.Ctim.Nsec}, nil
}

func openDirectory(fd int, name string) (*os.File, error) {
	n, err := unix.Openat(fd, name, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, ErrUnavailable
	}
	return os.NewFile(uintptr(n), "program generation directory"), nil
}

func trustedDirectory(st fileStamp, final bool) bool {
	return st.mode&unix.S_IFMT == unix.S_IFDIR && st.uid == 0 && (st.mode&0022 == 0 || !final && st.mode&unix.S_ISVTX != 0)
}

// Walk every component without following symlinks. Root-owned sticky ancestors
// allow a private /tmp fixture; the configured state parent cannot be writable.
func (s *Store) parent(ctx context.Context) (*os.File, directoryID, error) {
	var none directoryID
	dir, err := openDirectory(unix.AT_FDCWD, "/")
	if err != nil {
		return nil, none, ErrUnavailable
	}
	defer func() {
		if dir != nil {
			dir.Close()
		}
	}()
	parts := strings.Split(strings.TrimPrefix(s.state, "/"), "/")
	if s.state == "/" {
		parts = nil
	}
	for i := 0; ; i++ {
		st, err := statFile(dir)
		if err != nil || ctx.Err() != nil || !trustedDirectory(st, i == len(parts)) {
			return nil, none, unavailable(ctx)
		}
		if i == len(parts) {
			result := dir
			dir = nil
			return result, st.directoryID, nil
		}
		next, err := openDirectory(int(dir.Fd()), parts[i])
		if err != nil {
			return nil, none, ErrUnavailable
		}
		dir.Close()
		dir = next
	}
}

func (s *Store) directory(ctx context.Context, create bool) (*os.File, directoryID, directoryID, error) {
	var none directoryID
	parent, parentID, err := s.parent(ctx)
	if err != nil {
		return nil, none, none, err
	}
	defer parent.Close()
	if create {
		if err := unix.Mkdirat(int(parent.Fd()), "program-state", 0700); err != nil && !errors.Is(err, unix.EEXIST) {
			return nil, none, none, ErrUnavailable
		}
	}
	dir, err := openDirectory(int(parent.Fd()), "program-state")
	if err != nil {
		return nil, none, none, ErrUnavailable
	}
	st, err := statFile(dir)
	if err != nil || st.uid != 0 || st.mode&07777 != 0700 || ctx.Err() != nil || create && s.sync(parent) != nil {
		dir.Close()
		return nil, none, none, unavailable(ctx)
	}
	return dir, parentID, st.directoryID, nil
}

func regular(st fileStamp, size int64) bool {
	return st.mode&unix.S_IFMT == unix.S_IFREG && st.mode&07777 == 0600 && st.uid == 0 && st.links == 1 && st.size == size
}

func openFile(dir *os.File, name string, flags int) (*os.File, error) {
	fd, err := unix.Openat(int(dir.Fd()), name, flags|unix.O_NOFOLLOW|unix.O_NONBLOCK|unix.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), "program generation file"), nil
}

func decode(raw []byte) ([32]byte, bool) {
	var token [32]byte
	if len(raw) != 65 || raw[64] != '\n' || strings.Trim(string(raw[:64]), "0123456789abcdef") != "" {
		return token, false
	}
	_, err := hex.Decode(token[:], raw[:64])
	return token, err == nil && token != [32]byte{}
}

// Missing is distinguished internally only for explicit Advance initialization.
// Read never substitutes a missing or damaged file with a default generation.
func load(dir *os.File) (Generation, bool, error) {
	f, err := openFile(dir, "epoch", unix.O_RDONLY)
	if errors.Is(err, unix.ENOENT) {
		return Generation{}, false, nil
	}
	if err != nil {
		return Generation{}, false, ErrUnavailable
	}
	defer f.Close()
	before, err := statFile(f)
	if err != nil || !regular(before, 65) {
		return Generation{}, false, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(f, 66))
	if err != nil {
		return Generation{}, false, ErrUnavailable
	}
	token, ok := decode(raw)
	after, err := statFile(f)
	if !ok || err != nil || before != after {
		return Generation{}, false, ErrUnavailable
	}
	return Generation{file: before, token: token}, true, nil
}

func (s *Store) usable(ctx context.Context) bool {
	return s != nil && s.state != "" && s.random != nil && s.sync != nil && os.Geteuid() == 0 && ctx.Err() == nil
}

// PrepareParent creates only the final configured parent under an existing,
// trusted, protected ancestor. It never chmods/chowns existing directories or
// touches the private epoch. A failure can leave a newly created empty parent.
func (s *Store) PrepareParent(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, operationLimit)
	defer cancel()
	if !s.usable(ctx) {
		return unavailable(ctx)
	}
	ancestor := *s
	ancestor.state = filepath.Dir(s.state)
	base, baseID, err := ancestor.parent(ctx)
	if err != nil {
		return unavailable(ctx)
	}
	defer base.Close()
	created := false
	if err := unix.Mkdirat(int(base.Fd()), filepath.Base(s.state), 0700); err == nil {
		created = true
	} else if !errors.Is(err, unix.EEXIST) {
		return unavailable(ctx)
	}
	parent, err := openDirectory(int(base.Fd()), filepath.Base(s.state))
	if err != nil {
		return unavailable(ctx)
	}
	defer parent.Close()
	st, err := statFile(parent)
	if err != nil || !trustedDirectory(st, true) || created && st.mode&07777 != 0700 || ctx.Err() != nil {
		return unavailable(ctx)
	}
	if s.sync(parent) != nil || s.sync(base) != nil || ctx.Err() != nil {
		return unavailable(ctx)
	}
	freshBase, freshBaseID, err := ancestor.parent(ctx)
	if err != nil {
		return unavailable(ctx)
	}
	freshBase.Close()
	freshParent, freshParentID, err := s.parent(ctx)
	if err != nil {
		return unavailable(ctx)
	}
	freshParent.Close()
	if freshBaseID != baseID || freshParentID != st.directoryID || ctx.Err() != nil {
		return unavailable(ctx)
	}
	return nil
}

// Read performs no writes, creates no files and refuses absent state. It checks
// the complete path again before publishing. Its two-second context bounds
// checkpoints, not the duration of an individual local filesystem operation.
func (s *Store) Read(ctx context.Context) (Generation, error) {
	ctx, cancel := context.WithTimeout(ctx, operationLimit)
	defer cancel()
	if !s.usable(ctx) {
		return Generation{}, unavailable(ctx)
	}
	dir, parentID, dirID, err := s.directory(ctx, false)
	if err != nil {
		return Generation{}, unavailable(ctx)
	}
	defer dir.Close()
	g, exists, err := load(dir)
	if err != nil || !exists {
		return Generation{}, unavailable(ctx)
	}
	current, currentParent, currentDir, err := s.directory(ctx, false)
	if err != nil {
		return Generation{}, unavailable(ctx)
	}
	defer current.Close()
	latest, exists, err := load(current)
	if err != nil || !exists || latest != g || parentID != currentParent || dirID != currentDir || ctx.Err() != nil {
		return Generation{}, unavailable(ctx)
	}
	g.store, g.parent, g.directory = s, parentID, dirID
	return g, nil
}

func acquire(ctx context.Context, f *os.File) error {
	return acquireMode(ctx, f, unix.LOCK_EX)
}

func acquireMode(ctx context.Context, f *os.File, mode int) error {
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		err := unix.Flock(int(f.Fd()), mode|unix.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, unix.EINTR) && !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return ErrUnavailable
		}
		t := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// leaseRead binds a fresh epoch to the existing locked inode and directory path.
// Privileged replacement can still race a check; flock only excludes cooperating
// writers using the same inode. No descriptor is exposed to the callback.
func (s *Store) leaseRead(ctx context.Context, lock *os.File, stamp fileStamp, parentID, dirID directoryID) (Generation, error) {
	g, err := s.Read(ctx)
	if err != nil {
		return Generation{}, err
	}
	dir, currentParent, currentDir, err := s.directory(ctx, false)
	if err != nil {
		return Generation{}, unavailable(ctx)
	}
	defer dir.Close()
	currentLock, err := openFile(dir, "lock", unix.O_RDONLY)
	if err != nil {
		return Generation{}, unavailable(ctx)
	}
	defer currentLock.Close()
	currentStamp, currentErr := statFile(currentLock)
	heldStamp, heldErr := statFile(lock)
	if currentErr != nil || heldErr != nil || currentStamp != stamp || heldStamp != stamp || !regular(stamp, 0) ||
		currentParent != parentID || currentDir != dirID || g.parent != parentID || g.directory != dirID || ctx.Err() != nil {
		return Generation{}, unavailable(ctx)
	}
	return g, nil
}

// WithReadLease holds a shared process lock for the entire synchronous callback.
// It never creates or repairs state. Acquisition/preflight and postflight each
// have two-second checkpoint limits; the callback receives the caller's context.
// Cancellation cannot unlock an in-flight callback: all its work must finish or
// be joined before it returns. Advance on this store/path inside the callback
// will contend with its own read lease and must not be used.
//
// This excludes cooperating generation advances, not ongoing mutations whose
// advance already completed, cleanup paths, or nonparticipating writers. It
// grants no program-idle or radio authority. A returned error does not undo or
// disprove callback effects; postflight failure also returns ErrUncertain.
func (s *Store) WithReadLease(ctx context.Context, callback func(context.Context, Generation) error) error {
	preflight, cancel := context.WithTimeout(ctx, operationLimit)
	defer cancel()
	if callback == nil || !s.usable(preflight) {
		return unavailable(preflight)
	}
	dir, parentID, dirID, err := s.directory(preflight, false)
	if err != nil {
		return unavailable(preflight)
	}
	defer dir.Close()
	lock, err := openFile(dir, "lock", unix.O_RDONLY)
	if err != nil {
		return unavailable(preflight)
	}
	defer lock.Close()
	stamp, err := statFile(lock)
	if err != nil || !regular(stamp, 0) {
		return unavailable(preflight)
	}
	if err := acquireMode(preflight, lock, unix.LOCK_SH); err != nil {
		return err
	}
	g, err := s.leaseRead(preflight, lock, stamp, parentID, dirID)
	if err != nil {
		return err
	}
	cancel()
	return s.runLeaseCallback(ctx, lock, stamp, parentID, dirID, g, callback)
}

func (s *Store) runLeaseCallback(ctx context.Context, lock *os.File, stamp fileStamp, parentID, dirID directoryID, g Generation, callback func(context.Context, Generation) error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	callbackErr := callback(ctx, g)
	// Check retained evidence even after caller cancellation, while still holding
	// the lock. The separate deadline does not extend the callback's context.
	postflight, done := context.WithTimeout(context.WithoutCancel(ctx), operationLimit)
	defer done()
	after, checkErr := s.leaseRead(postflight, lock, stamp, parentID, dirID)
	if checkErr == nil && !g.Same(after) {
		checkErr = ErrUnavailable
	}
	if checkErr != nil {
		checkErr = errors.Join(ErrUncertain, checkErr)
	}
	return errors.Join(callbackErr, ctx.Err(), checkErr)
}

// Advance explicitly initializes or replaces the generation under a process-
// shared lock. It refuses damaged existing state. Publication is atomic and
// synchronized; a failure after rename is uncertain and is never rolled back.
// Callers must refuse activation on either error; this is not tracking coverage.
func (s *Store) Advance(ctx context.Context) (Generation, error) {
	return s.advance(ctx, nil)
}

// WithAdvanceLease advances before invoking a trusted synchronous mutation and
// keeps its exclusive lock through the callback and postcheck. The callback
// receives the original context, not the two-second storage-phase deadline.
// Cancellation never releases a running callback. All work must finish or be
// joined before return; callbacks must not reenter Advance or either lease on
// this path, or wait for a child/IPC operation that does so.
//
// A failure can retain the new generation, including when the callback was not
// reached. No callback effects or published generation are rolled back. Only
// participating operations are excluded; this supplies no idle/radio authority.
func (s *Store) WithAdvanceLease(ctx context.Context, callback func(context.Context, Generation) error) error {
	if callback == nil {
		return unavailable(ctx)
	}
	_, err := s.advance(ctx, callback)
	return err
}

func (s *Store) advance(ctx context.Context, callback func(context.Context, Generation) error) (Generation, error) {
	caller := ctx
	ctx, cancel := context.WithTimeout(ctx, operationLimit)
	defer cancel()
	if !s.usable(ctx) {
		return Generation{}, unavailable(ctx)
	}
	dir, parentID, dirID, err := s.directory(ctx, true)
	if err != nil {
		return Generation{}, unavailable(ctx)
	}
	defer dir.Close()
	lock, err := openFile(dir, "lock", unix.O_RDWR|unix.O_CREAT)
	if err != nil {
		return Generation{}, unavailable(ctx)
	}
	defer lock.Close()
	lockStamp, err := statFile(lock)
	if err != nil || !regular(lockStamp, 0) {
		return Generation{}, ErrUnavailable
	}
	if err := acquire(ctx, lock); err != nil {
		return Generation{}, err
	}
	previous, exists, err := load(dir)
	if err != nil {
		return Generation{}, unavailable(ctx)
	}
	var token [32]byte
	for attempt := 0; ; attempt++ {
		if attempt == 4 || ctx.Err() != nil {
			return Generation{}, unavailable(ctx)
		}
		if _, err := io.ReadFull(s.random, token[:]); err != nil {
			return Generation{}, unavailable(ctx)
		}
		if token != [32]byte{} && (!exists || token != previous.token) {
			break
		}
	}
	if ctx.Err() != nil {
		return Generation{}, ctx.Err()
	}
	name := ".epoch-" + hex.EncodeToString(token[:])
	tmp, err := openFile(dir, name, unix.O_WRONLY|unix.O_CREAT|unix.O_EXCL)
	if err != nil {
		return Generation{}, unavailable(ctx)
	}
	defer tmp.Close()
	defer unix.Unlinkat(int(dir.Fd()), name, 0)
	// New files must have the exact private mode even under a restrictive umask.
	// Do this on the exclusively created descriptor, never an existing path.
	if unix.Fchmod(int(tmp.Fd()), 0600) != nil {
		return Generation{}, ErrUnavailable
	}
	raw := hex.EncodeToString(token[:]) + "\n"
	if _, err := io.WriteString(tmp, raw); err != nil || s.sync(tmp) != nil || ctx.Err() != nil {
		return Generation{}, unavailable(ctx)
	}
	tempStamp, err := statFile(tmp)
	if err != nil || !regular(tempStamp, 65) {
		return Generation{}, unavailable(ctx)
	}
	// Recheck both the configured path and the lock inode after waiting/writing.
	current, currentParent, currentDir, err := s.directory(ctx, false)
	if err != nil {
		return Generation{}, unavailable(ctx)
	}
	defer current.Close()
	currentLock, err := openFile(current, "lock", unix.O_RDONLY)
	if err != nil {
		return Generation{}, unavailable(ctx)
	}
	defer currentLock.Close()
	latestLock, lockErr := statFile(currentLock)
	latest, nowExists, loadErr := load(current)
	if lockErr != nil || latestLock != lockStamp || loadErr != nil || latest != previous || exists != nowExists || parentID != currentParent || dirID != currentDir || ctx.Err() != nil {
		return Generation{}, unavailable(ctx)
	}
	if unix.Renameat(int(dir.Fd()), name, int(dir.Fd()), "epoch") != nil {
		return Generation{}, unavailable(ctx)
	}
	if s.sync(dir) != nil || ctx.Err() != nil {
		return Generation{}, errors.Join(ErrUncertain, unavailable(ctx))
	}
	result, err := s.Read(ctx)
	if err != nil || result.token != token || result.parent != parentID || result.directory != dirID {
		return Generation{}, errors.Join(ErrUncertain, unavailable(ctx))
	}
	if callback != nil {
		fresh, err := s.leaseRead(ctx, lock, lockStamp, parentID, dirID)
		if err != nil || !result.Same(fresh) {
			return Generation{}, errors.Join(ErrUncertain, unavailable(ctx))
		}
		cancel()
		if err := s.runLeaseCallback(caller, lock, lockStamp, parentID, dirID, result, callback); err != nil {
			return Generation{}, err
		}
	}
	return result, nil
}
