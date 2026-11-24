//go:build linux && programstateintegration

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/programstate"
	"golang.org/x/sys/unix"
)

var integrationBinary = flag.String("program-state-binary", "", "actual native helper for isolated Docker fixture")

func isolatedHelper(t *testing.T) string {
	t.Helper()
	if os.Getenv("DXB_PROGRAMSTATE_INTEGRATION") != "1" {
		t.Skip("explicit hardware-free integration fixture only")
	}
	if _, err := os.Stat("/.dockerenv"); err != nil {
		t.Fatal("Docker fixture required")
	}
	var fs unix.Statfs_t
	if unix.Statfs("/", &fs) != nil || fs.Flags&unix.ST_RDONLY == 0 {
		t.Fatal("read-only root required")
	}
	if unix.Statfs("/var/lib/dxberry", &fs) != nil || fs.Type != unix.TMPFS_MAGIC {
		t.Fatal("private tmpfs state required")
	}
	links, err := os.ReadDir("/sys/class/net")
	if err != nil || len(links) != 1 || links[0].Name() != "lo" {
		t.Fatal("network-free fixture required")
	}
	for _, pattern := range []string{"/dev/snd", "/dev/ttyUSB*", "/dev/ttyACM*", "/dev/hidraw*", "/dev/gpiochip*"} {
		matches, err := filepath.Glob(pattern)
		if err != nil || len(matches) != 0 {
			t.Fatal("radio devices must not be mounted")
		}
	}
	st, err := os.Lstat(*integrationBinary)
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm()&0111 == 0 {
		t.Fatal("actual native helper flag is required")
	}
	return *integrationBinary
}

func TestProgramStateActualProvisionBootstrap(t *testing.T) {
	binary := isolatedHelper(t)
	if os.Geteuid() != 0 {
		t.Fatal("bootstrap requires root fixture")
	}
	const parent = "/var/lib/dxberry/station"
	if _, err := os.Lstat(parent); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("bootstrap must start without parent")
	}
	call := func(state, action string, want int) {
		t.Helper()
		r, code := actualCall(t, binary, []string{"--state", state, action})
		if code != want || r.OK != (want == 0) {
			t.Fatal("bootstrap result", r, code, want)
		}
	}
	call(parent, "advance", 6)
	call("/var/lib/dxberry/missing/station", "prepare-advance", 6)
	if _, err := os.Lstat("/var/lib/dxberry/missing"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("created ancestor")
	}
	call(parent, "prepare-advance", 0)
	var st unix.Stat_t
	if unix.Lstat(parent, &st) != nil || st.Uid != 0 || st.Gid != 0 || st.Mode&07777 != 0700 {
		t.Fatal("bootstrap parent mode/owner")
	}
	store, err := programstate.NewStore(parent)
	if err != nil {
		t.Fatal(err)
	}
	first, err := store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	marker := parent + "/unrelated"
	if err := os.WriteFile(marker, []byte("retained\n"), 0600); err != nil {
		t.Fatal(err)
	}
	call(parent, "prepare-advance", 0)
	second, err := store.Read(context.Background())
	if err != nil || first.Same(second) {
		t.Fatal("repeat provisioning did not rotate", err)
	}
	epoch := parent + "/program-state/epoch"
	before, _ := os.ReadFile(epoch)
	if err := os.Chmod(parent, 0777); err != nil {
		t.Fatal(err)
	}
	call(parent, "prepare-advance", 6)
	if unix.Lstat(parent, &st) != nil || st.Mode&07777 != 0777 {
		t.Fatal("unsafe parent repaired")
	}
	if err := os.Chmod(parent, 0700); err != nil {
		t.Fatal(err)
	}
	after, _ := os.ReadFile(epoch)
	if !bytes.Equal(before, after) {
		t.Fatal("refused bootstrap changed epoch")
	}
	if err := os.Symlink(parent, "/var/lib/dxberry/alias"); err != nil {
		t.Fatal(err)
	}
	call("/var/lib/dxberry/alias", "prepare-advance", 6)
	if err := os.WriteFile("/var/lib/dxberry/file", []byte("retained file\n"), 0600); err != nil {
		t.Fatal(err)
	}
	call("/var/lib/dxberry/file", "prepare-advance", 6)
	if err := os.WriteFile(epoch, []byte("damaged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	call(parent, "prepare-advance", 6)
	damaged, _ := os.ReadFile(epoch)
	retained, _ := os.ReadFile(marker)
	file, _ := os.ReadFile("/var/lib/dxberry/file")
	if string(damaged) != "damaged\n" || string(retained) != "retained\n" || string(file) != "retained file\n" {
		t.Fatal("retained state changed")
	}
}

func actualCall(t *testing.T, binary string, args []string) (response, int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, args...)
	cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "DXB_STATE_DIR=/must-not-be-used"}
	var out, stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		var exit *exec.ExitError
		if !errors.As(err, &exit) {
			t.Fatal(err)
		}
		code = exit.ExitCode()
	}
	if ctx.Err() != nil || stderr.Len() != 0 {
		t.Fatalf("helper failed boundary: %v %q", ctx.Err(), stderr.String())
	}
	var r response
	d := json.NewDecoder(&out)
	d.DisallowUnknownFields()
	if d.Decode(&r) != nil || r.Protocol != 1 {
		t.Fatal("missing or unexpected response fields")
	}
	var extra any
	if !errors.Is(d.Decode(&extra), io.EOF) {
		t.Fatal("extra response")
	}
	if r.OK != (code == 0) {
		t.Fatalf("success/exit disagreement: %+v %d", r, code)
	}
	return r, code
}

func TestProgramStateActualCLI(t *testing.T) {
	binary := isolatedHelper(t)
	if os.Geteuid() != 0 {
		t.Fatal("root-positive fixture must run as root")
	}
	const state = "/var/lib/dxberry/station"
	entries, err := os.ReadDir(state)
	if err != nil || len(entries) != 0 {
		t.Fatal("empty disposable state required")
	}
	marker := filepath.Join(state, "unrelated")
	if err := os.WriteFile(marker, []byte("fixture remains unchanged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{nil, {"read"}, {"start"}, {"stop"}, {"recall"}, {"advance", "--state", "/other"}, {"--state", "relative", "advance"}, {"--state", "/private/../other", "advance"}, {"--state", "/one", "--state", "/two", "advance"}} {
		r, code := actualCall(t, binary, args)
		if code != 2 || r.Code != "invalid" || strings.Contains(r.Reason, "/private") {
			t.Fatal(r, code)
		}
	}
	entries, _ = os.ReadDir(state)
	if len(entries) != 1 {
		t.Fatal("refused options initialized state")
	}
	r, code := actualCall(t, binary, []string{"advance"})
	if code != 0 || !r.OK || r.Code != "" || r.Reason != "" {
		t.Fatal(r, code)
	}
	epoch := filepath.Join(state, "program-state", "epoch")
	first, err := os.ReadFile(epoch)
	if err != nil || len(first) != 65 {
		t.Fatal("missing epoch", err)
	}
	for name, mode := range map[string]os.FileMode{"program-state": 0700, "program-state/epoch": 0600, "program-state/lock": 0600} {
		st, err := os.Lstat(filepath.Join(state, name))
		if err != nil || st.Mode().Perm() != mode {
			t.Fatal(name, err)
		}
	}
	// Real inherited stdin remains open, with command-looking input: it is unused.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, binary, "--state", state, "advance")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(input, `{"command":"start","state":"/other"}`)
	if err := cmd.Wait(); err != nil || stderr.Len() != 0 || stdout.String() != "{\"ok\":true,\"protocol\":1}\n" {
		t.Fatalf("open stdin prevented completion: %v %q", err, stderr.String())
	}
	second, err := os.ReadFile(epoch)
	if err != nil || bytes.Equal(first, second) {
		t.Fatal("subsequent process did not rotate generation")
	}
	// A real exclusive lock blocks another process; refusal must preserve bytes.
	lock, err := os.OpenFile(filepath.Join(state, "program-state", "lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	r, code = actualCall(t, binary, []string{"advance"})
	if code != 6 || r.Code != "unavailable" {
		t.Fatal(r, code)
	}
	unchanged, _ := os.ReadFile(epoch)
	if !bytes.Equal(second, unchanged) {
		t.Fatal("contended helper changed generation")
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	_, code = actualCall(t, binary, []string{"advance"})
	if code != 0 {
		t.Fatal("helper did not recover")
	}
	before, _ := os.ReadFile(epoch)
	// A closed OS stdout pipe must produce exit 7, not SIGPIPE or rollback.
	readEnd, writeEnd, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	readEnd.Close()
	cmd = exec.CommandContext(ctx, binary, "advance")
	cmd.Stdout = writeEnd
	cmd.Stderr = &stderr
	err = cmd.Run()
	writeEnd.Close()
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 7 {
		t.Fatalf("closed stdout lost uncertainty: %v", err)
	}
	after, _ := os.ReadFile(epoch)
	if bytes.Equal(before, after) {
		t.Fatal("response loss rolled back publication")
	}
	// Fill an inherited pipe and leave its reader idle. The actual main watchdog
	// must end blocked JSON output; this cannot be established by a memory writer.
	readEnd, writeEnd, err = os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer readEnd.Close()
	defer writeEnd.Close()
	outputFD := int(writeEnd.Fd())
	if err := unix.SetNonblock(outputFD, true); err != nil {
		t.Fatal(err)
	}
	for {
		_, err := unix.Write(outputFD, make([]byte, 4096))
		if errors.Is(err, unix.EAGAIN) {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
	}
	if err := unix.SetNonblock(outputFD, false); err != nil {
		t.Fatal(err)
	}
	beforeBlocked, _ := os.ReadFile(epoch)
	blockedCtx, blockedCancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer blockedCancel()
	cmd = exec.CommandContext(blockedCtx, binary, "advance")
	cmd.Stdout = writeEnd
	cmd.Stderr = &stderr
	start := time.Now()
	err = cmd.Run()
	if !errors.As(err, &exit) || exit.ExitCode() != 7 || blockedCtx.Err() != nil || time.Since(start) > 5*time.Second {
		t.Fatalf("blocked stdout watchdog failed: %v", err)
	}
	afterBlocked, _ := os.ReadFile(epoch)
	if bytes.Equal(beforeBlocked, afterBlocked) {
		t.Fatal("watchdog test did not reach publication before blocked response")
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected private diagnostics: %q", stderr.String())
	}
	// Bad active state is retained, not silently reset by a start-prefix helper.
	if err := os.WriteFile(epoch, []byte("damaged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r, code = actualCall(t, binary, []string{"advance"})
	if code != 6 || r.Code != "unavailable" {
		t.Fatal(r, code)
	}
	damaged, _ := os.ReadFile(epoch)
	if string(damaged) != "damaged\n" {
		t.Fatal("damaged state replaced")
	}
	unrelated, _ := os.ReadFile(marker)
	if string(unrelated) != "fixture remains unchanged\n" {
		t.Fatal("unrelated state changed")
	}
	entries, _ = os.ReadDir(state)
	if len(entries) != 2 {
		t.Fatal("helper changed unrelated state layout")
	}
}

func TestProgramStateActualUnprivilegedCLI(t *testing.T) {
	binary := isolatedHelper(t)
	if os.Geteuid() == 0 {
		t.Fatal("unprivileged fixture must run as another UID")
	}
	for _, args := range [][]string{{"advance"}, {"--state", "/tmp/unused", "advance"}, {"--state", "/tmp/unused", "prepare-advance"}} {
		r, code := actualCall(t, binary, args)
		if code != 6 || r.OK || r.Code != "unavailable" || r.Reason != "program generation requires root" {
			t.Fatal(r, code)
		}
	}
	if _, err := os.Stat("/tmp/unused"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unprivileged helper initialized state")
	}
}

// This executes the literal template prefix, not a systemd manager. Ordering and
// failure propagation in a real manager remain a separate acceptance gate.
func TestProgramStateUnitActivationPrefix(t *testing.T) {
	binary := isolatedHelper(t)
	if os.Geteuid() != 0 {
		t.Fatal("activation prefix fixture requires root")
	}
	if binary != "/opt/dxberry/bin/dxberry-program-state" {
		t.Fatal("mount helper at the literal unit path")
	}
	raw, err := os.ReadFile("/fixture/dxberry-screen@.service")
	if err != nil {
		t.Fatal(err)
	}
	var condition, pre, stop, start []string
	section := ""
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.HasPrefix(line, "[") {
			section = line
			continue
		}
		if section != "[Service]" {
			continue
		}
		key, value, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		switch key {
		case "ExecCondition":
			condition = append(condition, value)
		case "ExecStartPre":
			pre = append(pre, value)
		case "ExecStopPost":
			stop = append(stop, value)
		case "ExecStart":
			start = append(start, value)
		}
	}
	if len(condition) != 2 || condition[0] != "+/opt/dxberry/bin/dxberry-program-state advance" || condition[1] != "+/opt/dxberry/bin/dxberry-app present %i" || len(pre) != 1 || pre[0] != "+/opt/dxberry/bin/dxberry-app prepare %i" {
		t.Fatalf("missing/nonmandatory/out-of-order activation prefix: conditions=%q pre=%q", condition, pre)
	}
	if len(stop) != 1 || stop[0] != "+/opt/dxberry/bin/dxberry-app stopped %i" || len(start) != 1 || start[0] != "/opt/dxberry/bin/dxberry-app session %i" {
		t.Fatal("cleanup/start contract changed")
	}
	args := strings.Fields(strings.TrimPrefix(condition[0], "+"))
	// A parent CLI may hold record.lock while waiting for this pre-start step.
	// The actual helper must use only its own lock rather than reenter that one.
	record, err := os.OpenFile("/run/dxberry/record.lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer record.Close()
	if err := unix.Flock(int(record.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	r, code := actualCall(t, args[0], args[1:])
	if code != 0 || !r.OK {
		t.Fatal("prefix waited on parent's record lock", r, code)
	}
	const state = "/var/lib/dxberry/station/program-state"
	epoch := filepath.Join(state, "epoch")
	before, err := os.ReadFile(epoch)
	if err != nil || len(before) != 65 {
		t.Fatal("activation prefix did not publish a generation")
	}
	lock, err := os.OpenFile(filepath.Join(state, "lock"), os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	r, code = actualCall(t, args[0], args[1:])
	if code != 6 || r.Code != "unavailable" {
		t.Fatal("contended prefix did not refuse", r, code)
	}
	after, _ := os.ReadFile(epoch)
	if !bytes.Equal(before, after) {
		t.Fatal("failed prefix changed active generation")
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	r, code = actualCall(t, args[0], args[1:])
	if code != 0 || !r.OK {
		t.Fatal("prefix failed recovery", r, code)
	}
	after, _ = os.ReadFile(epoch)
	if bytes.Equal(before, after) {
		t.Fatal("activation generation did not change")
	}
	if err := os.WriteFile(epoch, []byte("damaged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	r, code = actualCall(t, args[0], args[1:])
	if code != 6 || r.Code != "unavailable" {
		t.Fatal("damaged prefix state did not refuse", r, code)
	}
	// The exact unit line has no '-' error-ignore prefix. No synthetic service
	// launch or PTT request is used to claim manager/hardware acceptance here.
}

func TestProgramStateActualRadioRecordWriter(t *testing.T) {
	binary := isolatedHelper(t)
	if os.Geteuid() != 0 {
		t.Fatal("record writer fixture requires root")
	}
	var fs unix.Statfs_t
	if unix.Statfs("/var/lib/dxberry", &fs) != nil || fs.Type != unix.TMPFS_MAGIC {
		t.Fatal("disposable record tmpfs required")
	}
	const parent = "/var/lib/dxberry"
	const state = parent + "/station"
	const record = parent + "/radios.json"
	if _, err := os.Lstat(record); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("record fixture must start empty")
	}
	marker := parent + "/unrelated"
	if err := os.WriteFile(marker, []byte("retained fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := programstate.NewStore(state)
	if err != nil {
		t.Fatal(err)
	}
	// Only the actual common/record libraries are sourced. No scanner, apply,
	// module wiring, service command or radio-control function is invoked.
	const script = `set -uo pipefail
source /fixture/lib/common.sh
source /fixture/lib/radio.sh
dxb_radio_load || exit 91
dxb_radio_save "$1"
save_result=$?
printf '%s\n' "$DXB_RADIOS"
exit "$save_result"
`
	call := func(payload, helper string, want int) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-c", script, "record-fixture", payload)
		cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "DXB_STATE_DIR=" + parent, "DXB_RADIOS_FILE=" + record, "DXB_LOG_FILE=/dev/null", "DXB_PROGRAM_STATE_CMD=" + helper, "DXB_RUN_DIR=/run/dxberry", "DXB_RADIO_APPLY_LOCK_WAIT=0"}
		var out, stderr bytes.Buffer
		cmd.Stdout = &out
		cmd.Stderr = &stderr
		err := cmd.Run()
		code := 0
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		if code != want || ctx.Err() != nil {
			t.Fatalf("record writer: exit %d want %d; %v stderr %q", code, want, ctx.Err(), stderr.String())
		}
		var cache any
		if json.Unmarshal(out.Bytes(), &cache) != nil {
			t.Fatalf("invalid cached record: %q", out.String())
		}
		return out.Bytes()
	}
	equalJSON := func(a, b []byte) bool {
		var left, right any
		return json.Unmarshal(a, &left) == nil && json.Unmarshal(b, &right) == nil && reflect.DeepEqual(left, right)
	}
	const original = `{"version":1,"radios":{"radio1":{"label":"fixture","audio":null,"cat":{"path":"usb-0:1.1:1.0","vidpid":"1234:abcd","serial":"fixture"},"hid":null,"ptt_serial":null,"ptt":{"method":"none","gpio_line":null},"rig":{"model":1,"baud":0,"ptt_type":"NONE"},"rigctld_port":4532,"wiring":"full","owner":""}}}`
	// Invalid data cannot initialize the epoch or publish a record.
	call("not-json", binary, 2)
	if _, err := store.Read(context.Background()); !errors.Is(err, programstate.ErrUnavailable) {
		t.Fatal("invalid input initialized epoch", err)
	}
	if _, err := os.Lstat(record); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid input wrote record")
	}
	cache := call(original, binary, 0)
	first, err := store.Read(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(record)
	if err != nil || !equalJSON(cache, raw) || !equalJSON(raw, []byte(original)) {
		t.Fatal("initial record/cache mismatch", err)
	}
	owner := strings.Replace(original, `"owner":""`, `"owner":"wsjtx"`, 1)
	// Publication must not change either the real generation or record while
	// another process owns the derived-state gate. Release permits the retry.
	applyLock, err := os.OpenFile("/run/dxberry/apply.lock", os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer applyLock.Close()
	if err := unix.Flock(int(applyLock.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		t.Fatal(err)
	}
	cache = call(owner, binary, 6)
	blockedGeneration, err := store.Read(context.Background())
	if err != nil || !first.Same(blockedGeneration) {
		t.Fatal("contended publication advanced generation", err)
	}
	blockedRecord, err := os.ReadFile(record)
	if err != nil || !bytes.Equal(raw, blockedRecord) || !equalJSON(cache, raw) {
		t.Fatal("contended publication changed record/cache", err)
	}
	if err := unix.Flock(int(applyLock.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	call(owner, binary, 0)
	second, err := store.Read(context.Background())
	if err != nil || first.Same(second) {
		t.Fatal("ownership did not advance generation", err)
	}
	call(original, binary, 0)
	third, err := store.Read(context.Background())
	if err != nil || first.Same(third) || second.Same(third) {
		t.Fatal("ownership change and return lost", err)
	}
	afterABA, err := os.ReadFile(record)
	if err != nil || !bytes.Equal(raw, afterABA) {
		t.Fatal("fixture did not return to identical record bytes")
	}
	// A completed advance followed by uncertain delivery still prevents record
	// publication. The new epoch remains; it is never restored to the old value.
	changed := strings.Replace(original, `"label":"fixture"`, `"label":"changed"`, 1)
	cache = call(changed, "/fixture/uncertain-helper", 6)
	advanced, err := store.Read(context.Background())
	if err != nil || third.Same(advanced) {
		t.Fatal("uncertain helper did not advance", err)
	}
	unchanged, _ := os.ReadFile(record)
	if !bytes.Equal(raw, unchanged) || !equalJSON(cache, raw) {
		t.Fatal("uncertain helper published record or changed cache")
	}
	// Make only the record parent unwritable. The existing private station
	// child stays writable, so the real generation advances before write fails.
	if err := os.Chmod(parent, 0555); err != nil {
		t.Fatal(err)
	}
	cache = call(changed, binary, 6)
	if err := os.Chmod(parent, 0755); err != nil {
		t.Fatal(err)
	}
	writeFailure, err := store.Read(context.Background())
	if err != nil || advanced.Same(writeFailure) {
		t.Fatal("failed record write rolled back epoch", err)
	}
	unchanged, _ = os.ReadFile(record)
	if !bytes.Equal(raw, unchanged) || !equalJSON(cache, raw) {
		t.Fatal("failed write changed active record/cache")
	}
	// An unusable tracker refuses another change without resetting its bytes.
	epoch := state + "/program-state/epoch"
	if err := os.WriteFile(epoch, []byte("damaged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	cache = call(changed, binary, 6)
	unchanged, _ = os.ReadFile(record)
	if !bytes.Equal(raw, unchanged) || !equalJSON(cache, raw) {
		t.Fatal("tracking refusal changed record")
	}
	bad, _ := os.ReadFile(epoch)
	if string(bad) != "damaged\n" {
		t.Fatal("corrupt epoch repaired")
	}
	retained, _ := os.ReadFile(marker)
	if string(retained) != "retained fixture\n" {
		t.Fatal("unrelated data changed")
	}
}

func TestProgramStateActualRadioApplyCoordinator(t *testing.T) {
	binary := isolatedHelper(t)
	if os.Geteuid() != 0 {
		t.Fatal("apply fixture requires root")
	}
	for _, path := range []string{"/var/lib/dxberry", "/run/dxberry"} {
		var fs unix.Statfs_t
		if unix.Statfs(path, &fs) != nil || fs.Type != unix.TMPFS_MAGIC {
			t.Fatal("private disposable apply tmpfs required")
		}
	}
	const parent = "/var/lib/dxberry"
	const record = parent + "/radios.json"
	const state = parent + "/station"
	const epoch = state + "/program-state/epoch"
	const effects = parent + "/apply-effects"
	const mirror = "/run/dxberry/radios-state.json"
	const original = "{\"version\":1,\"radios\":{},\"gps\":{\"device\":\"auto\",\"baud\":9600,\"pps\":\"\"}}\n"
	if _, err := os.Lstat(record); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("fresh record fixture required")
	}
	marker := parent + "/unrelated"
	if err := os.WriteFile(marker, []byte("retained fixture\n"), 0600); err != nil {
		t.Fatal(err)
	}
	store, err := programstate.NewStore(state)
	if err != nil {
		t.Fatal(err)
	}
	// The record is empty: no per-radio branch can control a device or owner.
	// The coordinator and runtime-mirror publisher are real; scanning, udev,
	// module loading and orphan service actions are explicit marker functions.
	const script = `set -uo pipefail
source /fixture/lib/common.sh
source /fixture/lib/radio.sh
effect() {
  [[ -s /var/lib/dxberry/station/program-state/epoch ]] || exit 91
  if cmp -s /var/lib/dxberry/before-epoch /var/lib/dxberry/station/program-state/epoch; then exit 92; fi
  flock -n /run/dxberry/apply.lock true
  lock_result=$?
  [[ $lock_result == 1 ]] || exit 93
  printf '%s\n' "$1" >> /var/lib/dxberry/apply-effects
}
dxb_radio_scan_cache() { effect scan; DXB_RADIO_SCAN='[]'; }
dxb_radio_udev_write() { effect udev; }
dxb_radio_modprobe_install() { effect modprobe; }
dxb_rigctld_stop_all_except() { effect orphan-rigctld; }
dxb_audio_stop_all_except() { effect orphan-audio; }
dxb_radio_apply "$1"
exit $?
`
	call := func(mode, helper, target string, want int) string {
		t.Helper()
		if err := os.Remove(effects); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		before, err := os.ReadFile(epoch)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		if err := os.WriteFile(parent+"/before-epoch", before, 0600); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-c", script, "apply-fixture", mode)
		cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C", "DXB_STATE_DIR=" + parent, "DXB_RADIOS_FILE=" + record, "DXB_LOG_FILE=/dev/null", "DXB_PROGRAM_STATE_CMD=" + helper, "DXB_RUN_DIR=/run/dxberry", "DXB_RADIOS_STATE=" + target, "DXB_RADIO_APPLY_LOCK_WAIT=0"}
		var out, stderr bytes.Buffer
		cmd.Stdout, cmd.Stderr = &out, &stderr
		err = cmd.Run()
		code := 0
		if err != nil {
			var exit *exec.ExitError
			if !errors.As(err, &exit) {
				t.Fatal(err)
			}
			code = exit.ExitCode()
		}
		if code != want || ctx.Err() != nil || out.Len() != 0 {
			t.Fatalf("apply: exit %d want %d; deadline %v; stdout %q stderr %q", code, want, ctx.Err(), out.String(), stderr.String())
		}
		seen, err := os.ReadFile(effects)
		if err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
		return string(seen)
	}
	refuse := func(helper string) {
		t.Helper()
		before, beforeErr := os.ReadFile(mirror)
		if seen := call("hotplug", helper, mirror, 6); seen != "" {
			t.Fatal("refused tracking reached derived work", seen)
		}
		after, afterErr := os.ReadFile(mirror)
		if !bytes.Equal(before, after) || errors.Is(beforeErr, os.ErrNotExist) != errors.Is(afterErr, os.ErrNotExist) {
			t.Fatal("refused tracking changed runtime mirror")
		}
	}
	changed := func(previous programstate.Generation) programstate.Generation {
		t.Helper()
		next, err := store.Read(context.Background())
		if err != nil || previous.Same(next) {
			t.Fatal("apply did not advance generation", err)
		}
		return next
	}
	if err := os.WriteFile(record, []byte("not-json\n"), 0600); err != nil {
		t.Fatal(err)
	}
	refuse(binary)
	if _, err := os.Lstat(epoch); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("invalid JSON initialized generation")
	}
	if err := os.WriteFile(record, []byte(original), 0600); err != nil {
		t.Fatal(err)
	}
	refuse("/fixture/missing-helper")
	seen := call("full", binary, mirror, 0)
	if seen != "scan\nudev\nmodprobe\norphan-rigctld\norphan-audio\n" {
		t.Fatal("full apply fixture actions", seen)
	}
	first := changed(programstate.Generation{})
	call("full", binary, mirror, 0)
	second := changed(first)
	seen = call("hotplug", binary, mirror, 0)
	if seen != "scan\norphan-rigctld\norphan-audio\n" {
		t.Fatal("hotplug fixture actions", seen)
	}
	third := changed(second)
	var runtimeState struct {
		Generated string                     `json:"generated"`
		Radios    map[string]json.RawMessage `json:"radios"`
	}
	raw, err := os.ReadFile(mirror)
	if err != nil || json.Unmarshal(raw, &runtimeState) != nil || runtimeState.Generated == "" || runtimeState.Radios == nil || len(runtimeState.Radios) != 0 {
		t.Fatal("actual runtime mirror was not published", err)
	}
	refuse("/fixture/uncertain-helper")
	uncertain := changed(third)
	// The actual state publisher now encounters a real permission error after
	// advance. The private station child remains writable; capabilities are off.
	const blocked = "/run/dxberry/blocked"
	if err := os.Mkdir(blocked, 0555); err != nil {
		t.Fatal(err)
	}
	if seen := call("hotplug", binary, blocked+"/state.json", 6); seen == "" {
		t.Fatal("publication failure did not reach marked derived work")
	}
	publicationFailure := changed(uncertain)
	if _, err := os.Lstat(blocked + "/state.json"); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("unwritable runtime mirror was published")
	}
	lock, err := os.OpenFile("/run/dxberry/apply.lock", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	refuse(binary)
	unchanged, err := store.Read(context.Background())
	if err != nil || !publicationFailure.Same(unchanged) {
		t.Fatal("contended apply advanced generation", err)
	}
	if err := unix.Flock(int(lock.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	writerLock, err := os.OpenFile(state+"/program-state/lock", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer writerLock.Close()
	if err := unix.Flock(int(writerLock.Fd()), unix.LOCK_EX); err != nil {
		t.Fatal(err)
	}
	refuse(binary)
	unchanged, err = store.Read(context.Background())
	if err != nil || !publicationFailure.Same(unchanged) {
		t.Fatal("contended generation writer changed epoch", err)
	}
	if err := unix.Flock(int(writerLock.Fd()), unix.LOCK_UN); err != nil {
		t.Fatal(err)
	}
	call("hotplug", binary, mirror, 0)
	changed(publicationFailure)
	if err := os.WriteFile(epoch, []byte("damaged\n"), 0600); err != nil {
		t.Fatal(err)
	}
	refuse(binary)
	damaged, _ := os.ReadFile(epoch)
	if string(damaged) != "damaged\n" {
		t.Fatal("corrupt epoch repaired")
	}
	recordAfter, _ := os.ReadFile(record)
	retained, _ := os.ReadFile(marker)
	if string(recordAfter) != original || string(retained) != "retained fixture\n" {
		t.Fatal("apply changed registration or unrelated data")
	}
}
