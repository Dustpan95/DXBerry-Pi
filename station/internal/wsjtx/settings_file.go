package wsjtx

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
)

var (
	ErrSettingsFile      = errors.New("WSJT-X settings file unavailable")
	ErrSettingsChanged   = errors.New("WSJT-X settings file changed during replacement")
	ErrSettingsLifecycle = errors.New("WSJT-X settings replacement requires a stopped program and prepared recovery record")
	ErrSettingsUncertain = errors.New("WSJT-X settings replacement outcome requires reconciliation")
)

// SettingsFile holds a cooperative settings lock as the file's own user. It
// must be opened by the future application-user bridge, never by elevating
// ownership. It cannot stop the program or create a durable route lease.
type SettingsFile struct {
	mu                            sync.Mutex
	path, name, pending, lockName string
	dir, lock                     *os.File
	closed                        bool
	poisoned                      error
	syncFile                      func(*os.File) error
	rename                        func(int, string, int, string) error
	syncDir                       func() error
}

type settingsImage struct {
	doc  *SettingsDocument
	info os.FileInfo
	gid  uint32
}

func settingsError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("%w: %w", ErrSettingsFile, err)
}

func settingsOwner(f *os.File, directory, lock bool) (os.FileInfo, uint32, error) {
	info, err := f.Stat()
	if err != nil {
		return nil, 0, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != uint32(os.Geteuid()) {
		return nil, 0, errors.New("settings must belong to the current user")
	}
	perm := info.Mode().Perm()
	// The program home uses UMask=0007: its own primary service group may
	// write .config. Preserve that existing policy, but refuse writes by
	// other groups or everyone. The UID must still match the file owner.
	unsafeWrite := perm&0002 != 0 || perm&0020 != 0 && stat.Gid != uint32(os.Getegid())
	if directory {
		if !info.IsDir() || perm&0700 != 0700 || unsafeWrite {
			return nil, 0, errors.New("settings directory permissions are unsupported")
		}
	} else {
		if !info.Mode().IsRegular() || stat.Nlink != 1 || perm&0600 != 0600 || unsafeWrite || info.Mode()&(os.ModeSetuid|os.ModeSetgid|os.ModeSticky) != 0 {
			return nil, 0, errors.New("settings must be an owner-writable regular file with one link")
		}
		if lock && perm != 0600 {
			return nil, 0, errors.New("settings lock must be private")
		}
	}
	return info, stat.Gid, nil
}

func OpenSettingsFile(path string) (*SettingsFile, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, settingsError(err)
	}
	name := filepath.Base(path)
	if len(name) > 128 || name == "." || name == ".." || strings.ContainsAny(name, "\x00\r\n") {
		return nil, ErrSettingsFile
	}
	dirPath := filepath.Dir(path)
	fd, err := syscall.Open(dirPath, syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, settingsError(err)
	}
	h := &SettingsFile{path: path, name: name, pending: "." + name + ".dxb-route.pending", lockName: "." + name + ".dxb-route.lock", dir: os.NewFile(uintptr(fd), dirPath)}
	h.syncFile = func(f *os.File) error { return f.Sync() }
	h.rename = syscall.Renameat
	h.syncDir = h.dir.Sync
	if _, _, err := settingsOwner(h.dir, true, false); err != nil {
		_ = h.Close()
		return nil, settingsError(err)
	}
	h.lock, err = h.open(h.lockName, syscall.O_RDWR|syscall.O_CREAT)
	if err == nil {
		_, _, err = settingsOwner(h.lock, false, true)
	}
	if err == nil {
		err = syscall.Flock(int(h.lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	}
	if err == nil {
		_, err = h.read()
	}
	if err != nil {
		_ = h.Close()
		return nil, settingsError(err)
	}
	return h, nil
}

func (h *SettingsFile) open(name string, flags int) (*os.File, error) {
	fd, err := syscall.Openat(int(h.dir.Fd()), name, flags|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0600)
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), name), nil
}

func (h *SettingsFile) check() error {
	if h.closed {
		return os.ErrClosed
	}
	pathInfo, err := os.Lstat(filepath.Dir(h.path))
	if err != nil {
		return err
	}
	dirInfo, _, err := settingsOwner(h.dir, true, false)
	if err != nil {
		return err
	}
	if !os.SameFile(pathInfo, dirInfo) {
		return errors.New("settings directory replaced")
	}
	lock, err := h.open(h.lockName, syscall.O_RDONLY)
	if err != nil {
		return err
	}
	defer lock.Close()
	current, _, err := settingsOwner(lock, false, true)
	if err != nil {
		return err
	}
	held, err := h.lock.Stat()
	if err != nil {
		return err
	}
	if !os.SameFile(current, held) {
		return errors.New("settings lock replaced")
	}
	return nil
}

func (h *SettingsFile) read() (settingsImage, error) {
	return h.readNamed(h.name)
}

func (h *SettingsFile) readNamed(name string) (settingsImage, error) {
	if err := h.check(); err != nil {
		return settingsImage{}, err
	}
	f, err := h.open(name, syscall.O_RDONLY)
	if err != nil {
		return settingsImage{}, err
	}
	defer f.Close()
	info, gid, err := settingsOwner(f, false, false)
	if err != nil {
		return settingsImage{}, err
	}
	if info.Size() > MaxSettingsBytes {
		return settingsImage{}, ErrRoutingLayout
	}
	data, err := io.ReadAll(io.LimitReader(f, MaxSettingsBytes+1))
	if err != nil {
		return settingsImage{}, err
	}
	doc, err := ParseSettingsDocument(data)
	if err != nil {
		return settingsImage{}, err
	}
	return settingsImage{doc, info, gid}, nil
}

func (h *SettingsFile) Read() (*SettingsDocument, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	image, err := h.read()
	if err != nil {
		return nil, settingsError(err)
	}
	return image.doc, nil
}

// Apply atomically replaces the pair and preserves unrelated bytes and file
// ownership/mode. permit must assert a stopped/quiescent program, an exclusive
// lifecycle lock, a prepared durable recovery journal and, on acquisition, the
// current observer binding. It is called before staging and before replacement;
// it must not reenter this SettingsFile. This library cannot prove those
// cross-process conditions; no station integration calls it yet.
func (h *SettingsFile) Apply(p RoutingPatch, permit func() error) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.poisoned != nil {
		return h.poisoned
	}
	if permit == nil {
		return ErrSettingsLifecycle
	}
	if err := permit(); err != nil {
		return fmt.Errorf("%w: %w", ErrSettingsLifecycle, err)
	}
	before, err := h.read()
	if err != nil {
		return settingsError(err)
	}
	after, err := before.doc.WithPatch(p)
	if err != nil {
		return err
	}
	if bytes.Equal(before.doc.data, after.data) {
		return nil
	}
	if err := syscall.Unlinkat(int(h.dir.Fd()), h.pending); err != nil && !errors.Is(err, syscall.ENOENT) {
		return settingsError(err)
	}
	stage, err := h.open(h.pending, syscall.O_WRONLY|syscall.O_CREAT|syscall.O_EXCL)
	if err != nil {
		return settingsError(err)
	}
	defer syscall.Unlinkat(int(h.dir.Fd()), h.pending)
	defer stage.Close()
	if err := stage.Chown(-1, int(before.gid)); err != nil {
		return settingsError(err)
	}
	if err := stage.Chmod(before.info.Mode().Perm()); err != nil {
		return settingsError(err)
	}
	n, err := stage.Write(after.data)
	if err != nil {
		return settingsError(err)
	}
	if n != len(after.data) {
		return settingsError(io.ErrShortWrite)
	}
	if err := h.syncFile(stage); err != nil {
		return settingsError(err)
	}
	stageInfo, err := stage.Stat()
	if err != nil {
		return settingsError(err)
	}
	if err := stage.Close(); err != nil {
		return settingsError(err)
	}
	if err := permit(); err != nil {
		return fmt.Errorf("%w: %w", ErrSettingsLifecycle, err)
	}
	current, err := h.read()
	if err != nil {
		return settingsError(err)
	}
	if !os.SameFile(before.info, current.info) || !bytes.Equal(before.doc.data, current.doc.data) || before.info.Mode() != current.info.Mode() || before.gid != current.gid {
		return ErrSettingsChanged
	}
	staged, err := h.readNamed(h.pending)
	if err != nil {
		return settingsError(err)
	}
	if !os.SameFile(stageInfo, staged.info) || !bytes.Equal(staged.doc.data, after.data) || staged.info.Mode() != before.info.Mode() || staged.gid != before.gid {
		return ErrSettingsChanged
	}
	uncertain := func(err error) error { h.poisoned = fmt.Errorf("%w: %w", ErrSettingsUncertain, err); return h.poisoned }
	if err := h.rename(int(h.dir.Fd()), h.pending, int(h.dir.Fd()), h.name); err != nil {
		return uncertain(err)
	}
	if err := h.syncDir(); err != nil {
		return uncertain(err)
	}
	verified, err := h.read()
	if err != nil {
		return uncertain(err)
	}
	if !bytes.Equal(verified.doc.data, after.data) {
		return uncertain(ErrSettingsChanged)
	}
	return nil
}

func (h *SettingsFile) Close() error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil
	}
	h.closed = true
	var a, b error
	if h.lock != nil {
		a = h.lock.Close()
	}
	if h.dir != nil {
		b = h.dir.Close()
	}
	return errors.Join(a, b)
}
