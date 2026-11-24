package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/programstate"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/settings"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/wsjtx"
)

func runProgramMode(ctx context.Context, state, runtime, releaseRun, hook, radio, invocation string, initialize bool) error {
	if os.Geteuid() != 0 || !filepath.IsAbs(state) || filepath.Clean(state) != state || !filepath.IsAbs(runtime) || filepath.Clean(runtime) != runtime || initialize && (hook != "" || radio != "" || invocation != "") {
		return wsjtx.ErrProgramController
	}
	if initialize {
		release, err := wsjtx.AcquireProgramInitializationLease(releaseRun)
		if err != nil {
			return err
		}
		defer release()
		if err := wsjtx.CheckProgramAcquisition(filepath.Join(state, "wsjtx")); err != nil {
			return err
		}
		store, err := programstate.NewStore(state)
		if err != nil {
			return err
		}
		if err := store.PrepareParent(ctx); err != nil {
			return err
		}
		return store.WithAdvanceLease(ctx, func(ctx context.Context, _ programstate.Generation) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			if err := wsjtx.CheckProgramAcquisition(filepath.Join(state, "wsjtx")); err != nil {
				return err
			}
			if err := wsjtx.InitializeProgramState(filepath.Join(state, "wsjtx")); err != nil {
				return err
			}
			parent, err := os.Open(filepath.Dir(state))
			if err != nil {
				return err
			}
			defer parent.Close()
			return parent.Sync()
		})
	}
	timeout := 25 * time.Second
	if hook == "stopped" {
		timeout = 12 * time.Second
	} // leave room in the unit's stop-post limit
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return wsjtx.CallProgramHook(ctx, runtime, filepath.Join(state, "wsjtx"), &settings.Store{Dir: filepath.Join(state, "radios")}, hook, radio, invocation)
}

func runProgramReleaseMode(ctx context.Context, state, runtime, action, target, receipt string) (wsjtx.ReleaseState, error) {
	if os.Geteuid() != 0 || !filepath.IsAbs(state) || filepath.Clean(state) != state || !filepath.IsAbs(runtime) || filepath.Clean(runtime) != runtime {
		return wsjtx.ReleaseState{}, wsjtx.ErrProgramReleaseState
	}
	if action == "check" {
		if receipt != "" {
			return wsjtx.ReleaseState{}, wsjtx.ErrProgramReleaseState
		}
		compatible, err := wsjtx.ProgramBundleCompatible(target)
		if err != nil {
			return wsjtx.ReleaseState{}, err
		}
		if compatible {
			return wsjtx.ReleaseState{}, nil
		}
		action = "release-block"
	} else {
		if target != "" {
			return wsjtx.ReleaseState{}, wsjtx.ErrProgramReleaseState
		}
		switch action {
		case "status":
			action = "release-status"
		case "resume":
			action = "release-resume"
		default:
			return wsjtx.ReleaseState{}, wsjtx.ErrProgramReleaseState
		}
	}
	return wsjtx.CallProgramRelease(ctx, runtime, filepath.Join(state, "wsjtx"), &settings.Store{Dir: filepath.Join(state, "radios")}, action, receipt)
}

// Program acquisition uses fresh records and the existing authoritative
// presence check, rather than the UI watcher's last-good cached inventory.
// present takes no record lock: a CLI start already holds it while systemd
// waits for this callback, so taking it here would deadlock that start.
func programEligibility(records, bin string) func(context.Context, string) error {
	return func(ctx context.Context, radio string) error {
		before, err := wsjtxRadioRecord(records, radio)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, filepath.Join(bin, "dxberry-app"), "verify-present", "wsjtx-"+radio)
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		cmd.Cancel = func() error {
			if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL); err != nil {
				if err == syscall.ESRCH {
					return os.ErrProcessDone
				}
				return err
			}
			return nil
		}
		cmd.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8", "DXB_RADIOS_FILE=" + records, "DXB_STATE_DIR=" + filepath.Dir(records)}
		cmd.Stdout, cmd.Stderr = io.Discard, io.Discard
		if err := cmd.Run(); err != nil {
			return errors.Join(wsjtx.ErrInvalidSession, ctx.Err())
		}
		after, err := wsjtxRadioRecord(records, radio)
		if err != nil || !bytes.Equal(before, after) {
			return wsjtx.ErrInvalidSession
		}
		return ctx.Err()
	}
}

func wsjtxRadioRecord(path, radio string) ([]byte, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK|syscall.O_CLOEXEC, 0)
	if err != nil {
		return nil, wsjtx.ErrInvalidSession
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		return nil, wsjtx.ErrInvalidSession
	}
	raw, err := io.ReadAll(io.LimitReader(f, 2*1024*1024+1))
	if err != nil || len(raw) > 2*1024*1024 {
		return nil, wsjtx.ErrInvalidSession
	}
	// Exact-spelling maps follow jq's record keys, rather than Go's
	// case-insensitive struct-field aliases.
	var root map[string]json.RawMessage
	var radios map[string]map[string]json.RawMessage
	if json.Unmarshal(raw, &root) != nil || json.Unmarshal(root["radios"], &radios) != nil {
		return nil, wsjtx.ErrInvalidSession
	}
	var owner string
	if json.Unmarshal(radios[radio]["owner"], &owner) != nil || owner != "wsjtx" {
		return nil, wsjtx.ErrInvalidSession
	}
	return raw, nil
}
