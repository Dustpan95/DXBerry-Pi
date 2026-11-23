package wsjtx

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"
	"time"
)

var updateJobName = regexp.MustCompile(`^dxberry-job-update-[a-z0-9_-]{1,160}$`)

func rootReleaseFile(path string, directory bool) (os.FileInfo, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || stat.Uid != 0 || info.Mode().Perm()&0022 != 0 || info.Mode()&os.ModeSymlink != 0 || directory && !info.IsDir() || !directory && (!info.Mode().IsRegular() || stat.Nlink != 1 || info.Mode()&(os.ModeSetuid|os.ModeSetgid) != 0) {
		return nil, ErrProgramReleaseState
	}
	return info, nil
}

// ProgramBundleCompatible is a release contract, not a version guess. A
// checksummed publisher bundle declares protocol 1 and includes every required
// executable/hook. It is inspected as data; target binaries are never executed.
func ProgramBundleCompatible(root string) (bool, error) {
	if !filepath.IsAbs(root) || filepath.Clean(root) != root {
		return false, ErrProgramReleaseState
	}
	if _, err := rootReleaseFile(root, true); err != nil {
		return false, err
	}
	marker := filepath.Join(root, "share", "wsjtx-native-protocol")
	if _, err := os.Lstat(marker); os.IsNotExist(err) {
		return false, nil
	} else if err != nil {
		return false, err
	}
	for _, dir := range []string{filepath.Join(root, "bin"), filepath.Join(root, "lib"), filepath.Join(root, "share")} {
		if _, err := rootReleaseFile(dir, true); err != nil {
			return false, err
		}
	}
	if _, err := rootReleaseFile(marker, false); err != nil {
		return false, err
	}
	f, err := os.Open(marker)
	if err != nil {
		return false, err
	}
	raw, err := io.ReadAll(io.LimitReader(f, 33))
	_ = f.Close()
	if err != nil || len(raw) > 32 {
		return false, ErrProgramReleaseState
	}
	if string(raw) != ProgramBundleProtocol+"\n" {
		return false, nil
	}
	for _, name := range []string{"bin/dxberryd", "bin/dxberry-wsjtx-settings", "bin/dxberry-app", "lib/wsjtx.sh"} {
		info, err := rootReleaseFile(filepath.Join(root, name), false)
		if err != nil {
			return false, err
		}
		if strings.HasPrefix(name, "bin/") && info.Mode().Perm()&0111 == 0 {
			return false, ErrProgramReleaseState
		}
	}
	return true, nil
}

func sharedReleaseLock(run, name string) (*os.File, error) {
	if err := os.MkdirAll(run, 0755); err != nil {
		return nil, err
	}
	if _, err := rootReleaseFile(run, true); err != nil {
		return nil, err
	}
	fd, err := syscall.Open(filepath.Join(run, name), syscall.O_RDWR|syscall.O_CREAT|syscall.O_CLOEXEC|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0600)
	if err != nil {
		return nil, ErrProgramReleaseState
	}
	f := os.NewFile(uintptr(fd), name)
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, ErrProgramReleaseState
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok || !info.Mode().IsRegular() || info.Mode().Perm()&0022 != 0 || stat.Uid != 0 || stat.Nlink != 1 {
		_ = f.Close()
		return nil, ErrProgramReleaseState
	}
	if err := syscall.Flock(fd, syscall.LOCK_SH|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, ErrProgramReleasePaused
	}
	return f, nil
}

func showReleaseJob(ctx context.Context, unit string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/usr/bin/systemctl", "show", "--no-pager", "--property=LoadState,ActiveState", "--", unit+".service")
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "LC_ALL=C"}
	var output boundedProgramOutput
	cmd.Stdout, cmd.Stderr = &output, io.Discard
	if err := cmd.Run(); err != nil {
		return nil, ErrProgramReleaseState
	}
	return output.Bytes(), nil
}

func noReleaseJob(ctx context.Context, path string, show func(context.Context, string) ([]byte, error)) error {
	if _, err := os.Lstat(path); os.IsNotExist(err) {
		return nil
	} else if err != nil {
		return ErrProgramReleaseState
	}
	if _, err := rootReleaseFile(path, false); err != nil {
		return ErrProgramReleaseState
	}
	f, err := os.Open(path)
	if err != nil {
		return ErrProgramReleaseState
	}
	raw, err := io.ReadAll(io.LimitReader(f, 4097))
	_ = f.Close()
	if err != nil || len(raw) > 4096 {
		return ErrProgramReleaseState
	}
	var fields map[string]json.RawMessage
	var unit string
	if json.Unmarshal(raw, &fields) != nil || json.Unmarshal(fields["unit"], &unit) != nil || !updateJobName.MatchString(unit) {
		return ErrProgramReleaseState
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	_, _ = decoder.Token()
	seen := map[string]bool{}
	for decoder.More() {
		key, err := decoder.Token()
		name, ok := key.(string)
		if err != nil || !ok || seen[name] || strings.EqualFold(name, "unit") && name != "unit" {
			return ErrProgramReleaseState
		}
		seen[name] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return ErrProgramReleaseState
		}
	}
	result, err := show(ctx, unit)
	if err != nil || len(result) == 0 || len(result) > maxProgramFileBytes || bytes.ContainsAny(result, "\x00\r") || result[len(result)-1] != '\n' {
		return ErrProgramReleaseState
	}
	values := map[string]string{}
	for _, line := range strings.Split(strings.TrimSuffix(string(result), "\n"), "\n") {
		key, value, ok := strings.Cut(line, "=")
		if !ok || key != "LoadState" && key != "ActiveState" {
			return ErrProgramReleaseState
		}
		if _, seen := values[key]; seen {
			return ErrProgramReleaseState
		}
		values[key] = value
	}
	if len(values) != 2 {
		return ErrProgramReleaseState
	}
	if values["LoadState"] == "not-found" && values["ActiveState"] == "inactive" {
		return nil
	}
	if values["LoadState"] == "loaded" && (values["ActiveState"] == "inactive" || values["ActiveState"] == "failed") {
		return nil
	}
	return ErrProgramReleasePaused
}

// AcquireProgramReleaseLease holds both transition and legacy job-start locks
// through receipt completion. A surviving old daemon cannot clear the pause
// against another executable. Active/unknown updater evidence fails closed.
func AcquireProgramReleaseLease(ctx context.Context, bundle, run string) (func(), error) {
	return acquireProgramReleaseLease(ctx, bundle, run, "/proc/self/exe", showReleaseJob)
}

func acquireProgramReleaseLease(ctx context.Context, bundle, run, self string, show func(context.Context, string) ([]byte, error)) (func(), error) {
	if os.Geteuid() != 0 || !filepath.IsAbs(run) || filepath.Clean(run) != run || ctx.Err() != nil {
		return nil, ErrProgramReleaseState
	}
	transition, err := sharedReleaseLock(run, "native-release.lock")
	if err != nil {
		return nil, err
	}
	update, err := sharedReleaseLock(run, "update.lock")
	if err != nil {
		_ = transition.Close()
		return nil, err
	}
	release := func() { _ = update.Close(); _ = transition.Close() }
	fail := func(err error) (func(), error) { release(); return nil, err }
	if err := noReleaseJob(ctx, filepath.Join(run, "update-job.json"), show); err != nil {
		return fail(err)
	}
	compatible, err := ProgramBundleCompatible(bundle)
	if err != nil || !compatible {
		return fail(ErrProgramReleaseState)
	}
	installed, err := os.Stat(filepath.Join(bundle, "bin", "dxberryd"))
	if err != nil {
		return fail(err)
	}
	running, err := os.Stat(self)
	if err != nil || !os.SameFile(installed, running) {
		return fail(ErrProgramReleaseState)
	}
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	return release, nil
}

func AcquireProgramInitializationLease(run string) (func(), error) {
	if os.Geteuid() != 0 || !filepath.IsAbs(run) || filepath.Clean(run) != run {
		return nil, ErrProgramReleaseState
	}
	f, err := sharedReleaseLock(run, "native-release.lock")
	if err != nil {
		return nil, err
	}
	return func() { _ = f.Close() }, nil
}

func CheckProgramAcquisition(programPath string) error {
	return permitReleaseAcquisition(releasePath(programPath))
}
