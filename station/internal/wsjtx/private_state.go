package wsjtx

import (
	"errors"
	"io"
	"os"
	"path/filepath"
	"syscall"
)

// privateStateDir owns a private, cooperatively locked state directory. Its
// callers validate their own bounded/checksummed formats and retain uncertain
// transactions. The trusted parent is outside this helper's ownership scope.
type privateStateDir struct {
	path      string
	dir, lock *os.File
}

func privateStateFile(f *os.File, directory bool) error {
	info, err := f.Stat()
	if err != nil {
		return err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return errors.New("state must belong to the current user")
	}
	if directory {
		if !info.IsDir() || info.Mode().Perm() != 0700 {
			return errors.New("state directory must have mode 0700")
		}
	} else if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || stat.Nlink != 1 {
		return errors.New("state files must be private regular files with one link")
	}
	return nil
}

func openPrivateStateDir(path string, create bool) (*privateStateDir, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	if create {
		if err := os.Mkdir(path, 0700); err != nil {
			return nil, err
		}
	}
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	s := &privateStateDir{path: path, dir: os.NewFile(uintptr(fd), path)}
	if err := privateStateFile(s.dir, true); err != nil {
		_ = s.close()
		return nil, err
	}
	flags := syscall.O_RDWR
	if create {
		flags |= syscall.O_CREAT | syscall.O_EXCL
	}
	s.lock, err = s.openFile(historyLock, flags)
	if err == nil {
		err = syscall.Flock(int(s.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	}
	if err == nil && create {
		err = s.lock.Sync()
	}
	if err == nil {
		err = s.checkIdentity()
	}
	if err != nil {
		_ = s.close()
		return nil, err
	}
	return s, nil
}

func (s *privateStateDir) openFile(name string, flags int) (*os.File, error) {
	fd, err := syscall.Openat(int(s.dir.Fd()), name, flags|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, err
	}
	f := os.NewFile(uintptr(fd), name)
	if err := privateStateFile(f, false); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

func (s *privateStateDir) checkIdentity() error {
	pathInfo, err := os.Lstat(s.path)
	if err != nil {
		return err
	}
	info, err := s.dir.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(pathInfo, info) {
		return errors.New("state directory replaced")
	}
	if err := privateStateFile(s.dir, true); err != nil {
		return err
	}
	lock, err := s.openFile(historyLock, syscall.O_RDONLY)
	if err != nil {
		return err
	}
	defer lock.Close()
	current, err := lock.Stat()
	if err != nil {
		return err
	}
	held, err := s.lock.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(current, held) {
		return errors.New("state ownership lock replaced")
	}
	return nil
}

func (s *privateStateDir) replace(name, pending string, data []byte) error {
	if err := s.checkIdentity(); err != nil {
		return err
	}
	// One fixed staging name bounds leftovers. Never use pending content to
	// reconstruct committed state; unlink without following a possible link.
	if err := syscall.Unlinkat(int(s.dir.Fd()), pending); err != nil && !errors.Is(err, syscall.ENOENT) {
		return err
	}
	f, err := s.openFile(pending, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL)
	if err != nil {
		return err
	}
	defer syscall.Unlinkat(int(s.dir.Fd()), pending)
	n, err := f.Write(data)
	if err == nil && n != len(data) {
		err = io.ErrShortWrite
	}
	if err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err != nil {
		return err
	}
	if closeErr != nil {
		return closeErr
	}
	if err := syscall.Renameat(int(s.dir.Fd()), pending, int(s.dir.Fd()), name); err != nil {
		return err
	}
	return s.dir.Sync()
}

func (s *privateStateDir) syncParent() error {
	parent, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return err
	}
	defer parent.Close()
	return parent.Sync()
}

func (s *privateStateDir) close() error {
	var a, b error
	if s.lock != nil {
		a = s.lock.Close()
	}
	if s.dir != nil {
		b = s.dir.Close()
	}
	return errors.Join(a, b)
}
