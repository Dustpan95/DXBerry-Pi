package wsjtx

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

const (
	MaxSettingsBridgeFrame = 2 * 1024 * 1024
	settingsBridgeTimeout  = 5 * time.Second
	settingsHelperPath     = "/opt/dxberry/bin/dxberry-wsjtx-settings"
	settingsAppHome        = "/var/lib/dxberry-apps"
)

var (
	ErrSettingsBridge          = errors.New("WSJT-X application-user settings bridge unavailable")
	ErrSettingsBridgeProtocol  = errors.New("invalid WSJT-X settings bridge exchange")
	ErrSettingsBridgeIdentity  = errors.New("WSJT-X settings bridge requires the configured root/application-user boundary")
	ErrSettingsBridgeUncertain = errors.New("WSJT-X settings bridge outcome requires reconciliation")
)

type bridgePatch struct {
	Profile    string      `json:"profile"`
	Generation uint64      `json:"generation"`
	Before     string      `json:"before"`
	After      UDPSettings `json:"after"`
}

type bridgeRequest struct {
	Version int          `json:"version"`
	Action  string       `json:"action"`
	Radio   string       `json:"radio"`
	Patch   *bridgePatch `json:"patch,omitempty"`
}

type bridgeResponse struct {
	Version  int              `json:"version"`
	Kind     string           `json:"kind"`
	Radio    string           `json:"radio"`
	Stage    int              `json:"stage,omitempty"`
	Settings *RoutingSettings `json:"settings,omitempty"`
	Code     string           `json:"code,omitempty"`
}

type bridgeDecision struct {
	Version int  `json:"version"`
	Stage   int  `json:"stage"`
	Commit  bool `json:"commit"`
}

func readBridgeFrame(reader *bufio.Reader, into any) error {
	var raw []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(raw)+len(part) > MaxSettingsBridgeFrame {
			return ErrSettingsBridgeProtocol
		}
		raw = append(raw, part...)
		if err == nil {
			break
		}
		if !errors.Is(err, bufio.ErrBufferFull) {
			return ErrSettingsBridgeProtocol
		}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(into); err != nil {
		return ErrSettingsBridgeProtocol
	}
	canonical, err := json.Marshal(into)
	if err != nil || !bytes.Equal(append(canonical, '\n'), raw) {
		return ErrSettingsBridgeProtocol
	}
	return nil
}

func writeBridgeFrame(w io.Writer, value any) error {
	raw, err := json.Marshal(value)
	if err != nil || len(raw)+1 > MaxSettingsBridgeFrame {
		return ErrSettingsBridgeProtocol
	}
	raw = append(raw, '\n')
	n, err := w.Write(raw)
	if err != nil {
		return err
	}
	if n != len(raw) {
		return io.ErrShortWrite
	}
	return nil
}

func bridgeRequestPatch(r bridgeRequest) (RoutingPatch, error) {
	if r.Version != 1 || !radioName.MatchString(r.Radio) {
		return RoutingPatch{}, ErrSettingsBridgeProtocol
	}
	if r.Action == "inspect" {
		if r.Patch != nil {
			return RoutingPatch{}, ErrSettingsBridgeProtocol
		}
		return RoutingPatch{}, nil
	}
	if r.Action != "install" && r.Action != "restore" || r.Patch == nil {
		return RoutingPatch{}, ErrSettingsBridgeProtocol
	}
	p := r.Patch
	if p.Generation == 0 || len(p.Before) != 64 || strings.Trim(p.Before, "0123456789abcdef") != "" {
		return RoutingPatch{}, ErrSettingsBridgeProtocol
	}
	view := RoutingSettings{Profile: p.Profile, Current: p.After, Saved: map[string]UDPSettings{}}
	if view.validate() != nil {
		return RoutingPatch{}, ErrSettingsBridgeProtocol
	}
	if r.Action == "install" {
		port, ok := configuredPort(p.After.Port)
		if !ok || port == 0 || !p.After.Port.Present || p.After.Port.Value != strconv.Itoa(int(port)) || p.After.Server != (RawSetting{true, "127.0.0.1"}) {
			return RoutingPatch{}, ErrSettingsBridgeProtocol
		}
	}
	return RoutingPatch{radio: r.Radio, generation: p.Generation, profile: p.Profile, before: p.Before, after: p.After}, nil
}

// ServeSettingsBridge is the unprivileged helper's private stdin/stdout protocol.
// home is a trusted startup argument; the request selects only a validated radio,
// never a path. A root process is refused before any settings file is opened.
// The parent must enforce stopped-program lifecycle and prepared-journal checks
// at both readiness boundaries. EOF, malformed decisions and denial fail closed.
func ServeSettingsBridge(input io.Reader, output io.Writer, home string) error {
	if os.Geteuid() == 0 {
		return ErrSettingsBridgeIdentity
	}
	if !filepath.IsAbs(home) || filepath.Clean(home) != home {
		return ErrSettingsBridgeProtocol
	}
	reader := bufio.NewReader(input)
	var request bridgeRequest
	if err := readBridgeFrame(reader, &request); err != nil {
		return err
	}
	patch, err := bridgeRequestPatch(request)
	if err != nil {
		return err
	}
	reply := func(kind string, stage int, view *RoutingSettings, code string) error {
		return writeBridgeFrame(output, bridgeResponse{1, kind, request.Radio, stage, view, code})
	}
	fail := func(code string) error { _ = reply("error", 0, nil, code); return ErrSettingsBridge }
	path := filepath.Join(home, ".config", "WSJT-X - "+request.Radio+".ini")
	h, err := OpenSettingsFile(path)
	if err != nil {
		return fail("unavailable")
	}
	defer h.Close()
	if request.Action != "inspect" {
		stage := 0
		permit := func() error {
			stage++
			if stage > 2 {
				return ErrSettingsBridgeProtocol
			}
			if err := reply("ready", stage, nil, ""); err != nil {
				return err
			}
			var decision bridgeDecision
			if err := readBridgeFrame(reader, &decision); err != nil {
				return err
			}
			if decision.Version != 1 || decision.Stage != stage || !decision.Commit {
				return ErrSettingsLifecycle
			}
			return nil
		}
		if err := h.Apply(patch, permit); err != nil {
			code := "refused"
			if errors.Is(err, ErrSettingsUncertain) {
				code = "uncertain"
			}
			return fail(code)
		}
		// An idempotent patch stages no file. Still require both parent
		// checks before reporting a verified completion.
		if stage == 1 {
			if err := permit(); err != nil {
				return fail("refused")
			}
		}
	}
	doc, err := h.Read()
	if err != nil {
		return fail("unavailable")
	}
	view := doc.Routing()
	return reply("done", 0, &view, "")
}

// SettingsBridge starts a fixed helper with application-user credentials. Its
// private pipe is not a browser API. No shell, inherited environment or stdin
// command/path is accepted. The controller must retain its lifecycle lock and
// recovery record throughout each call and reconcile every failed write.
type SettingsBridge struct {
	helper, home string
	uid, gid     uint32
	timeout      time.Duration
}

func NewSettingsBridge() (*SettingsBridge, error) {
	account, err := user.Lookup("dxbapps")
	if err != nil {
		return nil, ErrSettingsBridgeIdentity
	}
	uid, err := strconv.ParseUint(account.Uid, 10, 32)
	if err != nil {
		return nil, ErrSettingsBridgeIdentity
	}
	gid, err := strconv.ParseUint(account.Gid, 10, 32)
	if err != nil {
		return nil, ErrSettingsBridgeIdentity
	}
	return newSettingsBridge(settingsHelperPath, settingsAppHome, uint32(uid), uint32(gid))
}

func newSettingsBridge(helper, home string, uid, gid uint32) (*SettingsBridge, error) {
	if os.Geteuid() != 0 || uid == 0 || gid == 0 || !filepath.IsAbs(helper) || !filepath.IsAbs(home) || filepath.Clean(helper) != helper || filepath.Clean(home) != home {
		return nil, ErrSettingsBridgeIdentity
	}
	// A trusted, root-owned final directory prevents the application account
	// from replacing the helper between inspection and exec.
	for _, path := range []string{helper, filepath.Dir(helper)} {
		info, err := os.Lstat(path)
		if err != nil {
			return nil, ErrSettingsBridgeIdentity
		}
		stat, ok := info.Sys().(*syscall.Stat_t)
		if !ok || stat.Uid != 0 || info.Mode().Perm()&0022 != 0 || info.Mode()&(os.ModeSymlink|os.ModeSetuid|os.ModeSetgid) != 0 {
			return nil, ErrSettingsBridgeIdentity
		}
		if path == helper && (!info.Mode().IsRegular() || info.Mode().Perm()&0111 == 0) {
			return nil, ErrSettingsBridgeIdentity
		}
		if path != helper && !info.IsDir() {
			return nil, ErrSettingsBridgeIdentity
		}
	}
	return &SettingsBridge{helper, home, uid, gid, settingsBridgeTimeout}, nil
}

func (b *SettingsBridge) Inspect(ctx context.Context, radio string) (RoutingSettings, error) {
	return b.exchange(ctx, bridgeRequest{Version: 1, Action: "inspect", Radio: radio}, nil)
}

func (b *SettingsBridge) Install(ctx context.Context, p RoutingPatch, guard func(context.Context) error) (RoutingSettings, error) {
	return b.patch(ctx, "install", p, guard)
}

func (b *SettingsBridge) Restore(ctx context.Context, p RoutingPatch, guard func(context.Context) error) (RoutingSettings, error) {
	return b.patch(ctx, "restore", p, guard)
}

func (b *SettingsBridge) patch(ctx context.Context, action string, p RoutingPatch, guard func(context.Context) error) (RoutingSettings, error) {
	if guard == nil {
		return RoutingSettings{}, ErrSettingsLifecycle
	}
	return b.exchange(ctx, bridgeRequest{1, action, p.Radio(), &bridgePatch{p.Profile(), p.Generation(), p.ExpectedFingerprint(), p.Replacement()}}, guard)
}

func (b *SettingsBridge) exchange(ctx context.Context, request bridgeRequest, guard func(context.Context) error) (RoutingSettings, error) {
	if _, err := bridgeRequestPatch(request); err != nil {
		return RoutingSettings{}, err
	}
	if _, err := newSettingsBridge(b.helper, b.home, b.uid, b.gid); err != nil {
		return RoutingSettings{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, b.timeout)
	defer cancel()
	if err := ctx.Err(); err != nil {
		return RoutingSettings{}, err
	}
	cmd := exec.CommandContext(ctx, b.helper, "--home", b.home)
	cmd.Env = []string{"PATH=/usr/bin:/bin", "LANG=C.UTF-8", "HOME=" + b.home}
	cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: b.uid, Gid: b.gid, Groups: []uint32{b.gid}}, Pdeathsig: syscall.SIGKILL}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return RoutingSettings{}, ErrSettingsBridge
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return RoutingSettings{}, ErrSettingsBridge
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return RoutingSettings{}, ErrSettingsBridge
	}
	waited := false
	defer func() {
		cancel()
		_ = stdin.Close()
		_ = stdout.Close()
		if !waited {
			_ = cmd.Wait()
		}
	}()
	failure := func(cause error) (RoutingSettings, error) {
		if ctx.Err() != nil {
			cause = ctx.Err()
		}
		if request.Action != "inspect" {
			return RoutingSettings{}, fmt.Errorf("%w: %w", ErrSettingsBridgeUncertain, cause)
		}
		return RoutingSettings{}, fmt.Errorf("%w: %w", ErrSettingsBridge, cause)
	}
	if err := writeBridgeFrame(stdin, request); err != nil {
		return failure(ErrSettingsBridgeProtocol)
	}
	reader := bufio.NewReader(stdout)
	nextStage := 1
	for {
		var response bridgeResponse
		if err := readBridgeFrame(reader, &response); err != nil {
			return failure(err)
		}
		if err := ctx.Err(); err != nil {
			return failure(err)
		}
		if response.Version != 1 || response.Radio != request.Radio {
			return failure(ErrSettingsBridgeProtocol)
		}
		switch response.Kind {
		case "ready":
			if request.Action == "inspect" || nextStage > 2 || response.Stage != nextStage || response.Settings != nil || response.Code != "" {
				return failure(ErrSettingsBridgeProtocol)
			}
			if err := guard(ctx); err != nil {
				_ = writeBridgeFrame(stdin, bridgeDecision{1, nextStage, false})
				return failure(err)
			}
			if err := ctx.Err(); err != nil {
				return failure(err)
			}
			if err := writeBridgeFrame(stdin, bridgeDecision{1, nextStage, true}); err != nil {
				return failure(ErrSettingsBridgeProtocol)
			}
			nextStage++
		case "done":
			if response.Settings == nil || response.Settings.validate() != nil || response.Stage != 0 || response.Code != "" || request.Action != "inspect" && nextStage != 3 {
				return failure(ErrSettingsBridgeProtocol)
			}
			if request.Patch != nil && (response.Settings.Profile != request.Patch.Profile || response.Settings.Current != request.Patch.After) {
				return failure(ErrSettingsBridgeProtocol)
			}
			_ = stdin.Close()
			// Require EOF: extra protocol frames or process output invalidate
			// success. The context bounds a helper that does not terminate.
			if _, err := reader.ReadByte(); !errors.Is(err, io.EOF) {
				return failure(ErrSettingsBridgeProtocol)
			}
			err := cmd.Wait()
			waited = true
			if err != nil {
				return failure(ErrSettingsBridge)
			}
			if err := ctx.Err(); err != nil {
				return failure(err)
			}
			return cloneRouting(*response.Settings), nil
		case "error":
			if response.Settings != nil || response.Stage != 0 || (response.Code != "unavailable" && response.Code != "refused" && response.Code != "uncertain") {
				return failure(ErrSettingsBridgeProtocol)
			}
			return failure(ErrSettingsBridge)
		default:
			return failure(ErrSettingsBridgeProtocol)
		}
	}
}
