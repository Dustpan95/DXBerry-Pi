package wsjtx

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func releaseBundle(t *testing.T) string {
	t.Helper()
	if os.Geteuid() != 0 {
		t.Skip("root bundle ownership")
	}
	root := t.TempDir()
	for _, dir := range []string{"bin", "lib", "share"} {
		if err := os.Mkdir(filepath.Join(root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"bin/dxberryd", "bin/dxberry-app", "bin/dxberry-wsjtx-settings", "lib/wsjtx.sh"} {
		if err := os.WriteFile(filepath.Join(root, name), []byte("inert bundle fixture\n"), 0755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(root, "share/wsjtx-native-protocol"), []byte(ProgramBundleProtocol+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	return root
}

func inactiveReleaseJob(context.Context, string) ([]byte, error) {
	return []byte("LoadState=loaded\nActiveState=inactive\n"), nil
}

func exclusiveReleaseLock(t *testing.T, path string) *os.File {
	t.Helper()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = f.Close() })
	return f
}

func TestReleaseAuthorityRequiresDeclaredCompleteSafeBundle(t *testing.T) {
	root := releaseBundle(t)
	if ok, err := ProgramBundleCompatible(root); err != nil || !ok {
		t.Fatal(ok, err)
	}
	marker := filepath.Join(root, "share/wsjtx-native-protocol")
	for _, value := range []string{"2\n", "1", "1\r\n"} {
		if err := os.WriteFile(marker, []byte(value), 0644); err != nil {
			t.Fatal(err)
		}
		if ok, err := ProgramBundleCompatible(root); err != nil || ok {
			t.Fatal("unsupported protocol accepted", ok, err)
		}
	}
	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if ok, err := ProgramBundleCompatible(root); err != nil || ok {
		t.Fatal("legacy bundle guessed compatible", ok, err)
	}
	if err := os.WriteFile(marker, []byte("1\n"), 0644); err != nil {
		t.Fatal(err)
	}
	name := filepath.Join(root, "bin/dxberry-wsjtx-settings")
	if err := os.Chmod(name, 0777); err != nil {
		t.Fatal(err)
	}
	if _, err := ProgramBundleCompatible(root); err == nil {
		t.Fatal("writable helper accepted")
	}
	if err := os.Remove(name); err != nil {
		t.Fatal(err)
	}
	if _, err := ProgramBundleCompatible(root); err == nil {
		t.Fatal("incomplete declared bundle accepted")
	}
}

func TestReleaseAuthorityHoldsBothLocksUntilReceiptCompletion(t *testing.T) {
	root := releaseBundle(t)
	run := t.TempDir()
	release, err := acquireProgramReleaseLease(context.Background(), root, run, filepath.Join(root, "bin/dxberryd"), inactiveReleaseJob)
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"native-release.lock", "update.lock"} {
		f, err := os.OpenFile(filepath.Join(run, name), os.O_RDWR, 0600)
		if err != nil {
			t.Fatal(err)
		}
		if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err == nil {
			_ = f.Close()
			t.Fatal("release proof dropped its lock early", name)
		}
		_ = f.Close()
	}
	release()
	for _, name := range []string{"native-release.lock", "update.lock"} {
		f := exclusiveReleaseLock(t, filepath.Join(run, name))
		_ = f.Close()
	}
}

func TestReleaseAuthorityRejectsUpdaterLocksRunningJobsAndOldExecutable(t *testing.T) {
	for _, name := range []string{"native-release.lock", "update.lock"} {
		t.Run(name, func(t *testing.T) {
			root := releaseBundle(t)
			run := t.TempDir()
			f := exclusiveReleaseLock(t, filepath.Join(run, name))
			if done, err := acquireProgramReleaseLease(context.Background(), root, run, filepath.Join(root, "bin/dxberryd"), inactiveReleaseJob); err == nil {
				done()
				t.Fatal("active transition accepted")
			}
			_ = f.Close()
		})
	}
	root := releaseBundle(t)
	run := t.TempDir()
	if err := os.WriteFile(filepath.Join(run, "update-job.json"), []byte(`{"unit":"dxberry-job-update-rollback-123","kind":"rollback"}`), 0644); err != nil {
		t.Fatal(err)
	}
	for _, state := range []string{"active", "activating", "deactivating", "reloading", "unknown"} {
		show := func(context.Context, string) ([]byte, error) {
			return []byte("LoadState=loaded\nActiveState=" + state + "\n"), nil
		}
		if done, err := acquireProgramReleaseLease(context.Background(), root, run, filepath.Join(root, "bin/dxberryd"), show); err == nil {
			done()
			t.Fatal("active/unknown job accepted", state)
		}
	}
	old := filepath.Join(root, "old-running")
	if err := os.WriteFile(old, []byte("old inode"), 0755); err != nil {
		t.Fatal(err)
	}
	if done, err := acquireProgramReleaseLease(context.Background(), root, run, old, inactiveReleaseJob); err == nil {
		done()
		t.Fatal("surviving old daemon resumed collection")
	}
}

func TestReleaseAuthorityRefusesUnverifiableOrUntrustedJobEvidence(t *testing.T) {
	_ = releaseBundle(t)
	run := t.TempDir()
	path := filepath.Join(run, "update-job.json")
	for _, raw := range []string{`{broken`, `{"Unit":"dxberry-job-update-system-123"}`, `{"unit":"sshd"}`, `{"unit":"../unit"}`, strings.Repeat("x", 4097)} {
		if err := os.WriteFile(path, []byte(raw), 0644); err != nil {
			t.Fatal(err)
		}
		if err := noReleaseJob(context.Background(), path, inactiveReleaseJob); err == nil {
			t.Fatal("bad job metadata accepted")
		}
	}
	if err := os.WriteFile(path, []byte(`{"unit":"dxberry-job-update-system-123"}`), 0644); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{"", "LoadState=loaded\n", "LoadState=loaded\nActiveState=inactive\nActiveState=inactive\n", "LoadState=loaded\r\nActiveState=inactive\n"} {
		show := func(context.Context, string) ([]byte, error) { return []byte(raw), nil }
		if err := noReleaseJob(context.Background(), path, show); err == nil {
			t.Fatal("ambiguous manager evidence accepted")
		}
	}
	show := func(context.Context, string) ([]byte, error) { return nil, errors.New("manager unavailable") }
	if err := noReleaseJob(context.Background(), path, show); err == nil {
		t.Fatal("manager failure became no active job")
	}
}

func TestReleaseAuthorityVerifiesActualRunningExecutableIdentity(t *testing.T) {
	root := releaseBundle(t)
	run := t.TempDir()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(self)
	if err != nil {
		t.Fatal(err)
	}
	installed := filepath.Join(root, "bin/dxberryd")
	if err := os.WriteFile(installed, raw, 0755); err != nil {
		t.Fatal(err)
	}
	child := func(binary, expect string) {
		t.Helper()
		cmd := exec.Command(binary, "-test.run=^TestReleaseAuthorityChildHelper$")
		cmd.Env = append(os.Environ(), "DXB_RELEASE_CHILD=1", "DXB_RELEASE_BUNDLE="+root, "DXB_RELEASE_RUN="+run, "DXB_RELEASE_EXPECT="+expect)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("release executable proof: %v %s", err, output)
		}
	}
	child(installed, "ready")
	old := filepath.Join(root, "bin/old-running")
	if err := os.Rename(installed, old); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installed, raw, 0755); err != nil {
		t.Fatal(err)
	}
	child(old, "blocked")
}

func TestReleaseAuthorityChildHelper(t *testing.T) {
	if os.Getenv("DXB_RELEASE_CHILD") != "1" {
		return
	}
	done, err := AcquireProgramReleaseLease(context.Background(), os.Getenv("DXB_RELEASE_BUNDLE"), os.Getenv("DXB_RELEASE_RUN"))
	if err == nil {
		done()
	}
	if (err == nil) != (os.Getenv("DXB_RELEASE_EXPECT") == "ready") {
		os.Exit(2)
	}
	os.Exit(0)
}
