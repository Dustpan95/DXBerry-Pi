package audio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/cli"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
)

// DefaultRunDir holds one directory per radio, each with the sound server's native socket.
const DefaultRunDir = "/run/dxberry/audio"

const (
	pactlTimeout = 5 * time.Second
	socketWait   = 5 * time.Second
)

var childEnv = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8"}

// programAudio says how a program reaches the sound card: "alsa" programs open the card
// themselves, so the radio's sound server is stopped while they run.
var programAudio = map[string]string{"wsjtx": "alsa", "flrig": "alsa", "pat": "alsa"}

var programNames = map[string]string{"wsjtx": "WSJT-X", "flrig": "flrig", "pat": "Pat"}

// Bin returns the absolute path of a helper (parec, pacat, pactl) from bins, falling back to /usr/bin.
func Bin(bins map[string]string, name string) string {
	if p := bins[name]; p != "" {
		return p
	}
	return "/usr/bin/" + name
}

// RadioLookup finds a radio's record (a *radios.Watcher satisfies it).
type RadioLookup interface {
	Get(name string) (radios.Radio, bool)
}

// ServerControl starts, stops and inspects the per-radio sound servers.
type ServerControl struct {
	cli    *cli.CLI
	runDir string

	// Radios supplies the record's owner for the "who has the sound card" reason; nil is allowed.
	Radios RadioLookup
	// Bins maps parec, pacat and pactl to absolute paths (tests point them at fakes).
	Bins map[string]string
	// Env is added to the fixed child environment.
	Env []string
	// SocketWait bounds Ensure's wait for the socket (5 s by default).
	SocketWait time.Duration
}

// NewServerControl returns a control that runs the unit through c and finds sockets under runDir.
func NewServerControl(c *cli.CLI, runDir string) *ServerControl {
	if runDir == "" {
		runDir = DefaultRunDir
	}
	return &ServerControl{cli: c, runDir: strings.TrimRight(runDir, "/"), SocketWait: socketWait}
}

// Card is the radio's sound card name: the radio name upper-cased.
func Card(radio string) string { return strings.ToUpper(radio) }

// Socket is the path of the radio's native protocol socket.
func (s *ServerControl) Socket(radio string) string {
	return filepath.Join(s.runDir, radio, "native")
}

// Server is the value for pactl/parec/pacat --server.
func (s *ServerControl) Server(radio string) string { return "unix:" + s.Socket(radio) }

func (s *ServerControl) audioCmd(ctx context.Context, verb, radio string) (cli.Result, error) {
	res, err := s.cli.Run(ctx, cli.Call{Command: "dxberry-audio", Args: []string{verb, radio, "--json"}})
	if err != nil {
		return res, err
	}
	if res.Exit != 0 {
		msg := strings.TrimSpace(res.Stderr)
		if msg == "" {
			msg = strings.TrimSpace(res.Stdout)
		}
		return res, fmt.Errorf("dxberry-audio %s %s: exit %d: %s", verb, radio, res.Exit, msg)
	}
	return res, nil
}

// Ensure starts the radio's sound server and waits for its socket.
func (s *ServerControl) Ensure(ctx context.Context, radio string) error {
	if _, err := s.audioCmd(ctx, "ensure", radio); err != nil {
		return err
	}
	wait := s.SocketWait
	if wait <= 0 {
		wait = socketWait
	}
	deadline := time.Now().Add(wait)
	for {
		if _, err := os.Stat(s.Socket(radio)); err == nil {
			return nil
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("sound server for %s: %s did not appear", radio, s.Socket(radio))
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(25 * time.Millisecond):
		}
	}
}

// Stop stops the radio's sound server.
func (s *ServerControl) Stop(ctx context.Context, radio string) error {
	_, err := s.audioCmd(ctx, "stop", radio)
	return err
}

func (s *ServerControl) missing(radio string) string {
	if s.Radios != nil {
		if r, ok := s.Radios.Get(radio); ok && programAudio[r.Owner] == "alsa" {
			name := programNames[r.Owner]
			return fmt.Sprintf("no sound server for %s: %s has the sound card", radio, name)
		}
	}
	return fmt.Sprintf("no sound server for %s: %s is missing", radio, s.Socket(radio))
}

// Available reports whether browser audio can run for radio: the socket exists and the server
// lists both <CARD> and <CARD>.tx. When not, reason says why.
func (s *ServerControl) Available(radio string) (bool, string) {
	ctx, cancel := context.WithTimeout(context.Background(), pactlTimeout)
	defer cancel()
	if _, err := os.Stat(s.Socket(radio)); err != nil {
		return false, s.missing(radio)
	}
	card := Card(radio)
	if res, err := s.cli.Run(ctx, cli.Call{Command: "dxberry-audio", Args: []string{"status", radio, "--json"}}); err == nil && res.Exit == 0 {
		var st struct {
			Card    string `json:"card"`
			Present *bool  `json:"present"`
		}
		if json.Unmarshal([]byte(res.Stdout), &st) == nil {
			if st.Present != nil && !*st.Present {
				return false, s.missing(radio)
			}
			if st.Card != "" {
				card = st.Card
			}
		}
	}
	out, err := s.pactlFor(ctx, radio, "list", "short", "sinks")
	if err != nil {
		return false, fmt.Sprintf("no sound server for %s: %v", radio, err)
	}
	have := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if f := strings.Split(line, "\t"); len(f) >= 2 {
			have[strings.TrimSpace(f[1])] = true
		}
	}
	for _, want := range []string{card, card + ".tx"} {
		if !have[want] {
			return false, fmt.Sprintf("no sound server for %s: sink %s is missing", radio, want)
		}
	}
	return true, ""
}

func (s *ServerControl) pactlFor(ctx context.Context, radio string, args ...string) (string, error) {
	return s.pactl(ctx, append([]string{"--server", s.Server(radio)}, args...)...)
}

// pactl runs pactl against the radio chosen by the caller's leading --server argument.
func (s *ServerControl) pactl(ctx context.Context, args ...string) (string, error) {
	return runPactl(ctx, s.Bins, s.Env, args)
}

func runPactl(ctx context.Context, bins map[string]string, env []string, args []string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, pactlTimeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, Bin(bins, "pactl"), args...)
	cmd.Env = append(append([]string(nil), childEnv...), env...)
	cmd.WaitDelay = time.Second
	var out, errb strings.Builder
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(errb.String()); msg != "" {
			return out.String(), fmt.Errorf("pactl: %v: %s", err, msg)
		}
		return out.String(), fmt.Errorf("pactl: %v", err)
	}
	return out.String(), nil
}

// LoadTXLoopback loops <CARD>.tx.monitor into <CARD> and returns the module id.
func (s *ServerControl) LoadTXLoopback(ctx context.Context, radio string) (int, error) {
	card := Card(radio)
	out, err := s.pactlFor(ctx, radio, "load-module", "module-loopback",
		"source="+card+".tx.monitor", "sink="+card, "latency_msec=20")
	if err != nil {
		return 0, err
	}
	id, err := strconv.Atoi(strings.TrimSpace(out))
	if err != nil {
		return 0, errors.New("pactl load-module: no module id in " + strconv.Quote(strings.TrimSpace(out)))
	}
	return id, nil
}

// UnloadTXLoopback removes the module LoadTXLoopback returned.
func (s *ServerControl) UnloadTXLoopback(ctx context.Context, radio string, moduleID int) error {
	_, err := s.pactlFor(ctx, radio, "unload-module", strconv.Itoa(moduleID))
	return err
}
