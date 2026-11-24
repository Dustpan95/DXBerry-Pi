package wsjtx

import (
	"bytes"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"os"
	"path/filepath"
	"syscall"
)

const ProgramBundleProtocol = "1"

var (
	ErrProgramReleasePaused  = errors.New("native program collection is paused for release reconciliation")
	ErrProgramReleaseState   = errors.New("native program release state requires reconciliation")
	ErrProgramReleaseReceipt = errors.New("retired native program release receipt")
)

type ReleaseState struct {
	Version  int    `json:"version"`
	Revision uint64 `json:"revision"`
	Receipt  string `json:"receipt"`
	Paused   bool   `json:"paused"`
}

type releaseGateFile struct {
	State    ReleaseState `json:"state"`
	Checksum string       `json:"sha256"`
}

func releasePath(programPath string) string {
	return filepath.Join(filepath.Dir(programPath), "wsjtx-release")
}

func encodeReleaseState(state ReleaseState) []byte {
	raw, _ := json.Marshal(state)
	sum := sha256.Sum256(raw)
	encoded, _ := json.Marshal(releaseGateFile{state, hex.EncodeToString(sum[:])})
	return append(encoded, '\n')
}

func decodeReleaseState(raw []byte) (ReleaseState, error) {
	var file releaseGateFile
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if len(raw) == 0 || len(raw) > 1024 || decoder.Decode(&file) != nil || file.State.Version != 1 || file.State.Revision == 0 || !journalID(file.State.Receipt) || !bytes.Equal(raw, encodeReleaseState(file.State)) {
		return ReleaseState{}, ErrProgramReleaseState
	}
	return file.State, nil
}

func readReleaseState(state *privateStateDir) (ReleaseState, error) {
	if err := state.checkIdentity(); err != nil {
		return ReleaseState{}, ErrProgramReleaseState
	}
	f, err := state.openFile("release.v1", syscall.O_RDONLY)
	if err != nil {
		return ReleaseState{}, ErrProgramReleaseState
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, 1025))
	if err != nil {
		return ReleaseState{}, ErrProgramReleaseState
	}
	return decodeReleaseState(raw)
}

// ReadReleaseState distinguishes an absent, never-created gate from damaged or
// concurrently owned state. The short ownership lock covers each atomic view.
// No read initializes or repairs state; missing files in an existing directory
// are errors. Root must retain this directory through upgrades and rollback.
func ReadReleaseState(path string) (ReleaseState, bool, error) {
	if !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ReleaseState{}, false, ErrProgramReleaseState
	}
	exists, err := programStateExists(path)
	if err != nil {
		return ReleaseState{}, false, ErrProgramReleaseState
	}
	if !exists {
		return ReleaseState{}, false, nil
	}
	state, err := openPrivateStateDir(path, false)
	if err != nil {
		return ReleaseState{}, false, ErrProgramReleaseState
	}
	defer state.close()
	value, err := readReleaseState(state)
	return value, true, err
}

func permitReleaseAcquisition(path string) error {
	state, exists, err := ReadReleaseState(path)
	if err != nil {
		return err
	}
	if exists && state.Paused {
		return ErrProgramReleasePaused
	}
	return nil
}

// PauseRelease creates only blocked state on its first explicit call. Every
// attempt rotates the receipt before release audit; delayed resume callbacks
// cannot clear a replacement transition. Failure may have committed the pause.
func PauseRelease(path string) (ReleaseState, error) {
	if os.Geteuid() != 0 || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return ReleaseState{}, ErrSettingsBridgeIdentity
	}
	exists, err := programStateExists(path)
	if err != nil {
		return ReleaseState{}, ErrProgramReleaseState
	}
	state, err := openPrivateStateDir(path, !exists)
	if err != nil {
		return ReleaseState{}, ErrProgramReleaseState
	}
	defer state.close()
	value := ReleaseState{Version: 1}
	if exists {
		value, err = readReleaseState(state)
		if err != nil {
			return ReleaseState{}, err
		}
	}
	if value.Revision == math.MaxUint64 {
		return ReleaseState{}, ErrProgramReleaseState
	}
	value.Revision++
	value.Receipt = rand.Text()
	value.Paused = true
	if err := state.replace("release.v1", "release.pending", encodeReleaseState(value)); err != nil {
		return ReleaseState{}, ErrProgramReleaseState
	}
	if err := state.syncParent(); err != nil {
		return ReleaseState{}, ErrProgramReleaseState
	}
	verified, err := readReleaseState(state)
	if err != nil || verified != value {
		return ReleaseState{}, ErrProgramReleaseState
	}
	return value, nil
}

// ResumeRelease needs an exact receipt and a caller-held release authority
// lease proving no updater can swap the installed compatible bundle. It never
// automatically clears a pause after startup, elapsed time or a failed update.
func ResumeRelease(path, receipt string) error {
	if os.Geteuid() != 0 || !filepath.IsAbs(path) || filepath.Clean(path) != path || !journalID(receipt) {
		return ErrProgramReleaseReceipt
	}
	state, err := openPrivateStateDir(path, false)
	if err != nil {
		return ErrProgramReleaseState
	}
	defer state.close()
	value, err := readReleaseState(state)
	if err != nil {
		return err
	}
	if value.Receipt != receipt {
		return ErrProgramReleaseReceipt
	}
	if !value.Paused {
		return nil
	}
	if value.Revision == math.MaxUint64 {
		return ErrProgramReleaseState
	}
	value.Paused = false
	value.Revision++
	if err := state.replace("release.v1", "release.pending", encodeReleaseState(value)); err != nil {
		return ErrProgramReleaseState
	}
	verified, err := readReleaseState(state)
	if err != nil || verified != value {
		return ErrProgramReleaseState
	}
	return nil
}
