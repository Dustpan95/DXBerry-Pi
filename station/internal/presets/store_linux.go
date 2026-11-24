package presets

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
	"time"
)

// Store is a local definition directory. Expected ownership is the creating
// process's effective UID; the configuration CLI separately requires root.
// All child access is relative to the verified directory descriptor.
type Store struct {
	dir    string
	owner  uint32
	random io.Reader
	sync   func(*os.File) error
	rename func(int, string, int, string) error
}

func NewStore(dir string) (*Store, error) {
	if !filepath.IsAbs(dir) || filepath.Clean(dir) != dir || dir == "/" {
		return nil, ErrInvalid
	}
	return &Store{dir: dir, owner: uint32(os.Geteuid()), random: rand.Reader, sync: func(f *os.File) error { return f.Sync() }, rename: syscall.Renameat}, nil
}

func (s *Store) directory(create bool) (*os.File, error) {
	if s == nil || s.dir == "" {
		return nil, ErrUnavailable
	}
	parentFD, err := syscall.Open(filepath.Dir(s.dir), syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, ErrUnavailable
	}
	parent := os.NewFile(uintptr(parentFD), "preset parent")
	defer parent.Close()
	parentInfo, err := parent.Stat()
	if err != nil {
		return nil, ErrUnavailable
	}
	parentStat, ok := parentInfo.Sys().(*syscall.Stat_t)
	if !ok || !parentInfo.IsDir() || parentStat.Uid != s.owner || parentStat.Mode&0022 != 0 {
		return nil, ErrUnavailable
	}
	name := filepath.Base(s.dir)
	if create {
		if err := syscall.Mkdirat(parentFD, name, 0700); err != nil && !errors.Is(err, syscall.EEXIST) {
			return nil, ErrUnavailable
		}
	}
	fd, err := syscall.Openat(parentFD, name, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if os.IsNotExist(err) && !create {
		return nil, nil
	}
	if err != nil {
		return nil, ErrUnavailable
	}
	f := os.NewFile(uintptr(fd), s.dir)
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, ErrUnavailable
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.IsDir() || st.Uid != s.owner || st.Mode&07777 != 0700 {
		f.Close()
		return nil, ErrUnavailable
	}
	// Sync the verified parent on every mutation, including a retry after an
	// earlier failure created this directory but could not confirm durability.
	if create {
		if err := s.sync(parent); err != nil {
			f.Close()
			return nil, ErrUnavailable
		}
	}
	return f, nil
}

func lock(ctx context.Context, f *os.File, write bool) error {
	mode := syscall.LOCK_SH
	if write {
		mode = syscall.LOCK_EX
	}
	for {
		if ctx.Err() != nil {
			return ErrUnavailable
		}
		err := syscall.Flock(int(f.Fd()), mode|syscall.LOCK_NB)
		if err == nil {
			return nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) && !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EINTR) {
			return ErrUnavailable
		}
		t := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			t.Stop()
			return ErrUnavailable
		case <-t.C:
		}
	}
}

func (s *Store) sameDirectory(f *os.File) bool {
	current, err := s.directory(false)
	if err != nil || current == nil {
		return false
	}
	defer current.Close()
	a, err := f.Stat()
	if err != nil {
		return false
	}
	b, err := current.Stat()
	return err == nil && os.SameFile(a, b)
}

type fileStamp struct {
	dev, ino, nlink uint64
	size            int64
	mode, uid       uint32
	mtime, ctime    syscall.Timespec
}

func stamp(info os.FileInfo) (fileStamp, bool) {
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileStamp{}, false
	}
	return fileStamp{st.Dev, st.Ino, uint64(st.Nlink), st.Size, st.Mode, st.Uid, st.Mtim, st.Ctim}, true
}

type stored struct {
	library Library
	raw     []byte
	stamp   fileStamp
	exists  bool
}

func (s *Store) load(dir *os.File, radio string) (stored, error) {
	name := radio + ".json"
	fd, err := syscall.Openat(int(dir.Fd()), name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if os.IsNotExist(err) {
		return stored{library: empty(radio)}, nil
	}
	if err != nil {
		return stored{}, ErrUnavailable
	}
	f := os.NewFile(uintptr(fd), name)
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return stored{}, ErrUnavailable
	}
	before, ok := stamp(info)
	if !ok || !info.Mode().IsRegular() || before.mode&07777 != 0600 || before.uid != s.owner || before.nlink != 1 || before.size > MaxBytes {
		return stored{}, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(f, MaxBytes+1))
	if err != nil || len(raw) > MaxBytes {
		return stored{}, ErrUnavailable
	}
	info, err = f.Stat()
	if err != nil {
		return stored{}, ErrUnavailable
	}
	after, ok := stamp(info)
	if !ok || before != after {
		return stored{}, ErrUnavailable
	}
	// Atomic replacement during the read must not turn an old descriptor into
	// current configuration evidence, even if an outside writer ignores flock.
	fd2, err := syscall.Openat(int(dir.Fd()), name, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return stored{}, ErrUnavailable
	}
	current := os.NewFile(uintptr(fd2), name)
	currentInfo, err := current.Stat()
	current.Close()
	if err != nil {
		return stored{}, ErrUnavailable
	}
	currentStamp, ok := stamp(currentInfo)
	if !ok || currentStamp != before {
		return stored{}, ErrUnavailable
	}
	l, err := DecodeLibrary(raw)
	if err != nil || l.Radio != radio {
		return stored{}, ErrUnavailable
	}
	return stored{l, raw, before, true}, nil
}

// List does not create a directory/file. A missing library is explicitly "absent";
// an invalid existing document never becomes an empty successful response.
func (s *Store) List(ctx context.Context, radio string) (Library, error) {
	if !radioName.MatchString(radio) {
		return Library{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return Library{}, ErrUnavailable
	}
	dir, err := s.directory(false)
	if err != nil {
		return Library{}, err
	}
	if dir == nil {
		return empty(radio), nil
	}
	defer dir.Close()
	if err := lock(ctx, dir, false); err != nil {
		return Library{}, err
	}
	current, err := s.load(dir, radio)
	if err != nil || !s.sameDirectory(dir) || ctx.Err() != nil {
		return Library{}, ErrUnavailable
	}
	return current.library, nil
}

func (s *Store) nonce(n int) (string, error) {
	b := make([]byte, n)
	if _, err := io.ReadFull(s.random, b); err != nil {
		return "", ErrUnavailable
	}
	return hex.EncodeToString(b), nil
}

func (s *Store) revision(old string) (string, error) {
	for i := 0; i < 8; i++ {
		v, err := s.nonce(32)
		if err != nil {
			return "", err
		}
		if v != old {
			return v, nil
		}
	}
	return "", ErrUnavailable
}

// Edit changes only definitions, under a cross-process directory lock. A CAS
// conflict, validation failure or pre-publication I/O failure retains old bytes.
func (s *Store) Edit(ctx context.Context, r Request) (Library, error) {
	if !r.valid() || r.Operation == "list" {
		return Library{}, ErrInvalid
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if ctx.Err() != nil {
		return Library{}, ErrUnavailable
	}
	dir, err := s.directory(true)
	if err != nil {
		return Library{}, err
	}
	defer dir.Close()
	if err := lock(ctx, dir, true); err != nil {
		return Library{}, err
	}
	base, err := s.load(dir, r.Radio)
	if err != nil {
		return Library{}, err
	}
	if base.library.Revision != r.ExpectedRevision {
		return Library{}, ErrConflict
	}
	l := base.library
	l.Presets = append([]Definition{}, base.library.Presets...)
	switch r.Operation {
	case "create":
		if len(l.Presets) >= MaxPresets {
			return Library{}, ErrLimit
		}
		id := ""
		for i := 0; i < 8; i++ {
			id, err = s.nonce(16)
			if err != nil {
				return Library{}, err
			}
			found := false
			for _, p := range l.Presets {
				if p.ID == id {
					found = true
					break
				}
			}
			if !found {
				break
			}
			id = ""
		}
		if id == "" {
			return Library{}, ErrUnavailable
		}
		l.Presets = append(l.Presets, Definition{id, r.Label, r.FrequencyHz})
	case "update", "delete":
		at := -1
		for i, p := range l.Presets {
			if p.ID == r.ID {
				at = i
				break
			}
		}
		if at < 0 {
			return Library{}, ErrNotFound
		}
		if r.Operation == "update" {
			l.Presets[at] = Definition{r.ID, r.Label, r.FrequencyHz}
		} else {
			l.Presets = append(l.Presets[:at], l.Presets[at+1:]...)
		}
	}
	l.Revision, err = s.revision(base.library.Revision)
	if err != nil {
		return Library{}, err
	}
	if !l.valid() {
		return Library{}, ErrInvalid
	}
	raw, err := json.MarshalIndent(l, "", "  ")
	if err != nil {
		return Library{}, ErrUnavailable
	}
	raw = append(raw, '\n')
	if len(raw) > MaxBytes {
		return Library{}, ErrInvalid
	}
	if err := s.publish(ctx, dir, r.Radio, base, raw); err != nil {
		return Library{}, err
	}
	return l, nil
}

func (s *Store) publish(ctx context.Context, dir *os.File, radio string, base stored, raw []byte) error {
	var f *os.File
	var tmp string
	for i := 0; i < 8; i++ {
		n, err := s.nonce(16)
		if err != nil {
			return err
		}
		tmp = "." + radio + "-" + n + ".tmp"
		fd, err := syscall.Openat(int(dir.Fd()), tmp, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0600)
		if errors.Is(err, syscall.EEXIST) {
			continue
		}
		if err != nil {
			return ErrUnavailable
		}
		f = os.NewFile(uintptr(fd), tmp)
		break
	}
	if f == nil {
		return ErrUnavailable
	}
	defer syscall.Unlinkat(int(dir.Fd()), tmp)
	defer f.Close()
	if err := f.Chmod(0600); err != nil {
		return ErrUnavailable
	}
	if n, err := f.Write(raw); err != nil || n != len(raw) {
		return ErrUnavailable
	}
	if err := s.sync(f); err != nil {
		return ErrUnavailable
	}
	if err := f.Close(); err != nil {
		return ErrUnavailable
	}
	current, err := s.load(dir, radio)
	if err != nil {
		return err
	}
	if current.exists != base.exists || current.stamp != base.stamp || !bytes.Equal(current.raw, base.raw) {
		return ErrConflict
	}
	if !s.sameDirectory(dir) || ctx.Err() != nil {
		return ErrUnavailable
	}
	if err := s.rename(int(dir.Fd()), tmp, int(dir.Fd()), radio+".json"); err != nil {
		return ErrUnavailable
	}
	// Publication happened. Do not roll an older revision back over a possibly
	// durable new one if fsync/readback/cancellation now prevents confirmation.
	if err := s.sync(dir); err != nil {
		return ErrUnknown
	}
	readback, err := s.load(dir, radio)
	if err != nil || !bytes.Equal(readback.raw, raw) || !s.sameDirectory(dir) || ctx.Err() != nil {
		return ErrUnknown
	}
	return nil
}
