package radios

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
)

var (
	ErrRegistrationUnavailable = errors.New("fresh radio registration unavailable")
	ErrRegistrationNotFound    = errors.New("radio is not registered")
)

const registrationLimit = 2 << 20

// RegistrationReader reads a trusted registration file afresh. It does not use
// the watcher's last-good cache and performs no filesystem writes at startup or
// while reading. The production owner is root; the path is server configuration.
type RegistrationReader struct {
	path  string
	owner uint32
}

func NewRegistrationReader(path string) (*RegistrationReader, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path || path == "/" {
		return nil, ErrRegistrationUnavailable
	}
	return &RegistrationReader{path: path, owner: 0}, nil
}

type registrationStamp struct {
	dev, ino, links uint64
	size            int64
	mode, uid       uint32
	mtime, ctime    syscall.Timespec
}

func registrationStat(f *os.File) (registrationStamp, error) {
	var st syscall.Stat_t
	if syscall.Fstat(int(f.Fd()), &st) != nil {
		return registrationStamp{}, ErrRegistrationUnavailable
	}
	return registrationStamp{st.Dev, st.Ino, uint64(st.Nlink), st.Size, st.Mode, st.Uid, st.Mtim, st.Ctim}, nil
}

type registrationParent struct {
	dev, ino  uint64
	mode, uid uint32
}

// Registration is immutable process-local identity evidence. Radio returns a
// value copy; selector details and the full-file digest are never wire data.
// This establishes configuration identity, not USB presence or CAT identity.
type Registration struct {
	reader        *RegistrationReader
	radio         Radio
	file          registrationStamp
	parent        registrationParent
	digest        [32]byte
	configuration RigConfiguration
}

func (r Registration) Radio() Radio { return r.radio }

// Configuration is an additional strict control view. A legacy/incomplete
// record may remain valid display/identity evidence while this proof is absent.
func (r Registration) Configuration() (RigConfiguration, bool) {
	return r.configuration, r.reader != nil && r.configuration.valid
}

// Same requires successful reads from the same source, exact file/parent
// identity and metadata, and exact full-file bytes. Even an identical atomic
// replacement invalidates it. Call Read again to obtain fresh comparison data.
func (r Registration) Same(other Registration) bool {
	return r.reader != nil && r.reader == other.reader && r.radio == other.radio &&
		r.file == other.file && r.parent == other.parent && r.digest == other.digest
}

func registrationName(s string) bool {
	if len(s) == 0 || len(s) > 12 || s[0] < 'a' || s[0] > 'z' {
		return false
	}
	return strings.Trim(s, "abcdefghijklmnopqrstuvwxyz0123456789") == ""
}

// Open every path component relative to its verified directory descriptor.
// A root-owned sticky directory (notably /tmp for isolated fixtures) is allowed
// as an ancestor; all other directories must deny group/other writes. The
// immediate parent must also deny them. Symlinks are never followed.
func (r *RegistrationReader) open() (*os.File, registrationParent, error) {
	var none registrationParent
	if r == nil || r.path == "" || !filepath.IsAbs(r.path) || filepath.Clean(r.path) != r.path || r.path == "/" {
		return nil, none, ErrRegistrationUnavailable
	}
	parts := strings.Split(strings.TrimPrefix(r.path, "/"), "/")
	fd, err := syscall.Open("/", syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, none, ErrRegistrationUnavailable
	}
	dir := os.NewFile(uintptr(fd), "registration ancestor")
	defer func() { dir.Close() }()
	for i := 0; i < len(parts); i++ {
		st, err := registrationStat(dir)
		sticky := st.uid == 0 && st.mode&syscall.S_ISVTX != 0
		if err != nil || st.mode&syscall.S_IFMT != syscall.S_IFDIR || (st.uid != 0 && st.uid != r.owner) || st.mode&0022 != 0 && (!sticky || i == len(parts)-1) {
			return nil, none, ErrRegistrationUnavailable
		}
		if i == len(parts)-1 {
			parent := registrationParent{st.dev, st.ino, st.mode, st.uid}
			fd, err = syscall.Openat(int(dir.Fd()), parts[i], syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
			if err != nil {
				return nil, none, ErrRegistrationUnavailable
			}
			return os.NewFile(uintptr(fd), "radio registration"), parent, nil
		}
		fd, err = syscall.Openat(int(dir.Fd()), parts[i], syscall.O_RDONLY|syscall.O_DIRECTORY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
		if err != nil {
			return nil, none, ErrRegistrationUnavailable
		}
		next := os.NewFile(uintptr(fd), "registration ancestor")
		dir.Close()
		dir = next
	}
	return nil, none, ErrRegistrationUnavailable
}

func (r *RegistrationReader) trusted(st registrationStamp) bool {
	return st.mode&syscall.S_IFMT == syscall.S_IFREG && st.uid == r.owner && st.mode&0022 == 0 && st.links == 1 && st.size >= 0 && st.size <= registrationLimit
}

func (r *RegistrationReader) Read(ctx context.Context, name string) (Registration, error) {
	var none Registration
	if err := ctx.Err(); err != nil {
		return none, err
	}
	if !registrationName(name) {
		return none, ErrRegistrationNotFound
	}
	f, parent, err := r.open()
	if err != nil {
		return none, err
	}
	defer f.Close()
	before, err := registrationStat(f)
	if err != nil || !r.trusted(before) {
		return none, ErrRegistrationUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(f, registrationLimit+1))
	if err != nil || len(raw) > registrationLimit || int64(len(raw)) != before.size {
		return none, ErrRegistrationUnavailable
	}
	after, err := registrationStat(f)
	if err != nil || before != after {
		return none, ErrRegistrationUnavailable
	}
	radio, err := parseRegistration(raw, name)
	if err != nil {
		return none, err
	}
	configuration, _ := parseRigConfiguration(raw, radio)
	// Reopen through the trusted path: a rename during the read must not leave
	// a successful observation of the now-unlinked former registration.
	current, currentParent, err := r.open()
	if err != nil {
		return none, ErrRegistrationUnavailable
	}
	defer current.Close()
	latest, err := registrationStat(current)
	if err != nil || latest != before || currentParent != parent {
		return none, ErrRegistrationUnavailable
	}
	if err := ctx.Err(); err != nil {
		return none, err
	}
	return Registration{r, radio, before, parent, sha256.Sum256(raw), configuration}, nil
}

// Decode through maps to avoid encoding/json's case-insensitive struct field
// aliases. Unknown extension fields are retained in the digest, not interpreted.
func registrationObject(raw []byte, fields ...string) (map[string]json.RawMessage, error) {
	var obj map[string]json.RawMessage
	if json.Unmarshal(raw, &obj) != nil || obj == nil {
		return nil, ErrRegistrationUnavailable
	}
	for key := range obj {
		for _, field := range fields {
			if key != field && strings.EqualFold(key, field) {
				return nil, ErrRegistrationUnavailable
			}
		}
	}
	return obj, nil
}

func parseRegistration(raw []byte, name string) (Radio, error) {
	bad := ErrRegistrationUnavailable
	if !registrationEncoding(raw) {
		return Radio{}, bad
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.UseNumber()
	if uniqueRegistrationJSON(d, 0) != nil {
		return Radio{}, bad
	}
	if _, err := d.Token(); err != io.EOF {
		return Radio{}, bad
	}
	root, err := registrationObject(raw, "version", "radios")
	if err != nil || string(root["version"]) != "1" {
		return Radio{}, bad
	}
	all, err := registrationObject(root["radios"])
	if err != nil || len(all) > 128 {
		return Radio{}, bad
	}
	var selected Radio
	ports := map[int]bool{}
	for n, v := range all {
		if !registrationName(n) {
			return Radio{}, bad
		}
		r, err := parseRadioRegistration(v, n)
		if err != nil || ports[r.Port] {
			return Radio{}, bad
		}
		ports[r.Port] = true
		if n == name {
			selected = r
		}
	}
	if selected.Name == "" {
		return Radio{}, ErrRegistrationNotFound
	}
	return selected, nil
}

func parseRadioRegistration(value []byte, name string) (Radio, error) {
	bad := ErrRegistrationUnavailable
	obj, err := registrationObject(value, "label", "rigctld_port", "owner", "ptt", "rig", "cat")
	if err != nil {
		return Radio{}, bad
	}
	var radio Radio
	radio.Name = name
	text := func(m map[string]json.RawMessage, key string, out *string) bool {
		v, ok := m[key]
		return ok && string(v) != "null" && json.Unmarshal(v, out) == nil
	}
	integer := func(m map[string]json.RawMessage, key string, out *int) bool {
		v, ok := m[key]
		return ok && string(v) != "null" && json.Unmarshal(v, out) == nil
	}
	if !text(obj, "label", &radio.Label) || !text(obj, "owner", &radio.Owner) || !integer(obj, "rigctld_port", &radio.Port) || radio.Port < 4532 || radio.Port > 65534 || radio.Port%2 != 0 {
		return Radio{}, bad
	}
	if len(radio.Label) > 40 {
		return Radio{}, bad
	}
	for _, c := range radio.Label {
		if c < ' ' || c > '~' {
			return Radio{}, bad
		}
	}
	ptt, err := registrationObject(obj["ptt"], "method")
	if err != nil || !text(ptt, "method", &radio.PTT) {
		return Radio{}, bad
	}
	rig, err := registrationObject(obj["rig"], "model", "control")
	if err != nil || !integer(rig, "model", &radio.Model) || radio.Model < 1 {
		return Radio{}, bad
	}
	// Keep the watcher's historical empty value for an omitted control field;
	// the manager already treats that as Hamlib. Explicit values remain strict.
	if _, ok := rig["control"]; ok {
		if !text(rig, "control", &radio.Control) || radio.Control != "hamlib" && radio.Control != "flrig" {
			return Radio{}, bad
		}
	}
	switch radio.PTT {
	case "rigctld", "cm108", "gpio", "vox", "digirig_tone", "none":
	default:
		return Radio{}, bad
	}
	cat, ok := obj["cat"]
	if !ok {
		return Radio{}, bad
	}
	if string(cat) != "null" {
		pin, err := registrationObject(cat, "path")
		var path string
		if err != nil || !text(pin, "path", &path) || path == "" {
			return Radio{}, bad
		}
		radio.HasCAT = true
	}
	return radio, nil
}
