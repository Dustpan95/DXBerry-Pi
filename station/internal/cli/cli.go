// Package cli runs the dxberry-* commands (and a few systemctl and journalctl shapes) for the
// station app. Nothing reaches a process unless its command and every argument match the
// allowlist below; there is no shell, no PATH lookup and no client-chosen path.
package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	maxOutput   = 1 << 20 // per stream
	callTimeout = 60 * time.Second
	truncated   = "\n[truncated]"
)

// Call is one request: a command name, its arguments and optional standard input.
type Call struct {
	Command string
	Args    []string
	Stdin   string
}

// Result is what a command did. Exit is -1 when the process could not start or was killed.
type Result struct {
	Stdout, Stderr string
	Exit           int
}

// Runner starts one process. name is the resolved absolute path; env is the whole environment.
type Runner func(ctx context.Context, name string, args []string, stdin string, env []string) (Result, error)

// CLI validates and runs calls.
type CLI struct {
	binDir string
	run    Runner
	// SystemBin overrides the absolute path of the two system commands (tests point it at fakes).
	SystemBin map[string]string
	// Timeout bounds one call (60 s by default).
	Timeout time.Duration
	// Logf receives one line per call; it defaults to slog.
	Logf func(format string, args ...any)
	// OnCall, when set, is told about every call the HTTP handler ran to completion (not a refused one).
	OnCall func(cmd string, args []string)

	mu    sync.Mutex
	units map[string]bool // the services list from the last dxberry-status
}

var systemBin = map[string]string{"systemctl": "/usr/bin/systemctl", "journalctl": "/usr/bin/journalctl"}

var runEnv = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "LANG=C.UTF-8"}

// New returns a CLI that runs dxberry-* commands from binDir. A nil run uses os/exec.
func New(binDir string, run Runner) *CLI {
	if run == nil {
		run = runExec
	}
	return &CLI{binDir: strings.TrimRight(binDir, "/"), run: run, units: map[string]bool{}, Timeout: callTimeout,
		Logf: func(f string, a ...any) { slog.Info(fmt.Sprintf(f, a...)) }}
}

// The allowlist: command -> accepted argument patterns. A pattern is a sequence of tokens; a
// token ending in "..." matches zero or more of the token before the dots. Literals match
// exactly. The rest are named below.
var (
	reName     = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	reSession  = regexp.MustCompile(`^[a-z][a-z0-9]*-[a-z][a-z0-9-]*$`)
	reValue    = regexp.MustCompile(`^[A-Za-z0-9._:,/= +-]{1,128}$`)
	reKeyValue = regexp.MustCompile(`^[A-Z][A-Z0-9_]*=[^\x00-\x1f\x7f]{0,256}$`)
	reOption   = regexp.MustCompile(`^--(audio|cat|hid|ptt-serial|ptt|ptt-type|wiring|label|rig-control|model|baud|gpio-line)$`)
	reUnit     = regexp.MustCompile(`^(dxberry-[a-z0-9@._-]+|rigctld@[a-z0-9-]+|graywolf[a-z0-9-]*|gpsd|chrony|nginx|php[0-9.]*-fpm|mariadb)\.(service|timer|socket)$`)
	reNumber   = regexp.MustCompile(`^[1-9][0-9]{0,3}$`)
	reProg     = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,31}$`)
	reSSHKey   = regexp.MustCompile(`^SSH_PUBKEY=((ssh-(ed25519|rsa|ecdsa-sha2-nistp[0-9]+)|sk-[a-z0-9-]+@openssh\.com) [A-Za-z0-9+/=]+( [A-Za-z0-9._@: -]{0,128})?)?$`)
)

// Tokens: NAME (a radio), PROG (a program), SESSION, VALUE, KEYVALUE, UNIT, N (1-2000), N25 (1-25), OPTVAL (an option
// flag and its value, two arguments).
var allow = map[string][][]string{
	"dxberry-status": {{"--json"}},
	"dxberry-radio": {
		{"scan", "--json"},
		{"status", "--json"},
		{"status", "NAME", "--json"},
		{"add", "NAME", "OPTVAL...", "--json"},
		{"claim", "NAME", "PROG", "--json"},
		{"release", "NAME", "--json"},
		{"models", "--json"},
		{"set", "NAME", "OPTVAL...", "--json"},
		{"remove", "NAME", "--json"},
		{"hotplug", "--json"},
	},
	"dxberry-config": {
		{"get", "--json"},
		{"set", "KEYVALUE", "KEYVALUE...", "--json"},
		{"set", "KEYVALUE...", "--stdin", "--json"},
		{"job", "--json"},
		{"confirm", "--json"},
		{"revert", "--json"},
		{"apply", "--json"},
	},
	"dxberry-update": {
		{"check", "--json"},
		{"check", "--refresh", "--json"},
		{"graywolf", "--json"},
		{"dxberry", "--json"},
		{"system", "--json"},
		{"rollback", "--json"},
		{"job", "--json"},
		{"app", "PROG", "--json"},
		{"prereleases", "on", "--json"},
		{"prereleases", "off", "--json"},
	},
	"dxberry-app": {
		{"list", "--json"},
		{"install", "PROG", "--json"},
		{"remove", "PROG", "--json"},
		{"start", "NAME", "--json"},
		{"stop", "NAME", "--json"},
		{"screen", "SESSION", "--json"},
	},
	"dxberry-logbook": {
		{"get", "--json"},
		{"set", "--json"},
		{"test", "--json"},
		{"recent", "--json"},
		{"recent", "N25", "--json"},
		{"flush", "--json"},
	},
	"systemctl": {
		{"start", "UNIT"},
		{"stop", "UNIT"},
		{"restart", "UNIT"},
		{"is-active", "UNIT"},
		{"reload", "nginx.service"},
		{"--no-block", "start", "reboot.target"},
		{"--no-block", "start", "poweroff.target"},
	},
	"journalctl": {
		{"-u", "UNIT", "-n", "N", "--no-pager", "-o", "short-iso"},
		{"-b", "-n", "N", "--no-pager", "-o", "short-iso", "--priority=debug"},
	},
}

// width reports how many arguments token tok takes from args (0 when it does not match).
func (c *CLI) width(tok string, args []string) int {
	if len(args) == 0 {
		return 0
	}
	a := args[0]
	switch tok {
	case "NAME":
		return b2i(reName.MatchString(a))
	case "SESSION":
		return b2i(reSession.MatchString(a))
	case "VALUE":
		return b2i(reValue.MatchString(a))
	case "PROG":
		return b2i(reProg.MatchString(a))
	case "KEYVALUE":
		k, _, _ := strings.Cut(a, "=")
		if k == "SSH_PUBKEY" { // a public key has an @ in its comment and is longer than a value
			return b2i(len(a) <= 1024 && reSSHKey.MatchString(a))
		}
		return b2i(reKeyValue.MatchString(a) && k != "PASSWORD" && k != "WIFI_PASSWORD")
	case "UNIT":
		c.mu.Lock()
		known := c.units[a]
		c.mu.Unlock()
		return b2i(reUnit.MatchString(a) || known)
	case "N":
		return b2i(number(a, 2000))
	case "N25":
		return b2i(number(a, 25))
	case "OPTVAL":
		if len(args) >= 2 && reOption.MatchString(a) && reValue.MatchString(args[1]) {
			return 2
		}
		return 0
	}
	return b2i(a == tok)
}

func number(s string, max int) bool {
	n, err := strconv.Atoi(s)
	return reNumber.MatchString(s) && err == nil && n <= max
}

func b2i(b bool) int {
	if b {
		return 1
	}
	return 0
}

// match reports whether pat accepts args; on failure, pos is how many arguments were accepted
// before the first one that could not be (the furthest any branch got) and tok is the pattern
// token that refused it.
func (c *CLI) match(pat []string, args []string) (ok bool, pos int, tok string) {
	return c.matchAt(pat, args, 0)
}

func (c *CLI) matchAt(pat, args []string, at int) (bool, int, string) {
	if len(pat) == 0 {
		if len(args) == 0 {
			return true, at, ""
		}
		return false, at, "END"
	}
	tok := pat[0]
	if base, rep := strings.CutSuffix(tok, "..."); rep {
		best, btok := at, base
		for i := 0; ; {
			if ok, p, t := c.matchAt(pat[1:], args[i:], at+i); ok {
				return true, p, ""
			} else if p > best {
				best, btok = p, t
			}
			w := c.width(base, args[i:])
			if w == 0 {
				return false, best, btok
			}
			i += w
		}
	}
	w := c.width(tok, args)
	if w == 0 {
		return false, at, tok
	}
	return c.matchAt(pat[1:], args[w:], at+w)
}

// Refusal is why a call was not allowed; Pos is the 1-based argument at fault (0: the command).
type Refusal struct {
	Pos int
	Msg string // never holds an argument value: this is what gets logged
	// Public is Msg plus, where harmless, the offending value; it is the HTTP error text.
	Public string
}

func (r *Refusal) Error() string { return r.Public }

var reKeyName = regexp.MustCompile(`^[A-Z][A-Z0-9_]{0,39}$`)

// hint says which argument was refused without risking a secret. A KEYVALUE shows only its key;
// only tokens that can never carry a secret echo their value; everything else (literals, END,
// VALUE) says nothing.
func hint(tok, arg string) string {
	switch tok {
	case "KEYVALUE":
		if k, _, ok := strings.Cut(arg, "="); ok && reKeyName.MatchString(k) {
			return " (key " + k + ")"
		}
	case "NAME", "PROG", "SESSION", "UNIT", "N", "N25", "OPTVAL":
		if len(arg) > 40 {
			return " (too long to show)"
		}
		return " (" + strconv.Quote(arg) + ")"
	}
	return ""
}

// Validate reports why a call is refused, or nil when it is allowed to run.
func (c *CLI) Validate(call Call) error {
	pats, ok := allow[call.Command]
	if !ok {
		msg := "unknown command"
		return &Refusal{0, msg, msg}
	}
	best, btok := 0, ""
	for _, p := range pats {
		ok, pos, tok := c.match(p, call.Args)
		if ok {
			return nil
		}
		if pos >= best {
			best, btok = pos, tok
		}
	}
	if best >= len(call.Args) {
		msg := fmt.Sprintf("%s: arguments incomplete, expected %s after argument %d", call.Command, btok, best)
		return &Refusal{best, msg, msg}
	}
	msg := fmt.Sprintf("%s: argument %d does not match %s", call.Command, best+1, btok)
	return &Refusal{best + 1, msg, msg + hint(btok, call.Args[best])}
}

func (c *CLI) path(command string) string {
	if strings.HasPrefix(command, "dxberry-") {
		return c.binDir + "/" + command
	}
	if p, ok := c.SystemBin[command]; ok {
		return p
	}
	return systemBin[command]
}

// Run validates the call and runs it under a 60 s deadline. The error is a refusal; a command
// that fails, or cannot start, is a Result with its exit code.
func (c *CLI) Run(ctx context.Context, call Call) (Result, error) {
	if err := c.Validate(call); err != nil {
		return Result{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, c.Timeout)
	defer cancel()
	res, err := c.run(ctx, c.path(call.Command), call.Args, call.Stdin, append([]string(nil), runEnv...))
	if err != nil {
		return Result{Exit: -1, Stderr: err.Error()}, nil
	}
	if call.Command == "dxberry-status" && res.Exit == 0 {
		c.learnUnits(res.Stdout)
	}
	return res, nil
}

// learnUnits remembers the services a status report lists, so they may be started and stopped.
func (c *CLI) learnUnits(out string) {
	var rep struct {
		Services struct {
			Units []struct {
				Unit string `json:"unit"`
			} `json:"units"`
		} `json:"services"`
	}
	if json.Unmarshal([]byte(out), &rep) != nil {
		return
	}
	units := map[string]bool{}
	for _, u := range rep.Services.Units {
		if u.Unit != "" {
			units[u.Unit] = true
		}
	}
	c.mu.Lock()
	c.units = units
	c.mu.Unlock()
}

// capBuffer keeps the first maxOutput bytes and drops the rest without blocking the writer.
type capBuffer struct {
	buf  bytes.Buffer
	over bool
}

func (b *capBuffer) Write(p []byte) (int, error) {
	if room := maxOutput - b.buf.Len(); len(p) > room {
		b.buf.Write(p[:room])
		b.over = true
	} else {
		b.buf.Write(p)
	}
	return len(p), nil
}

func (b *capBuffer) String() string {
	if b.over {
		return b.buf.String() + truncated
	}
	return b.buf.String()
}

// runExec is the default Runner: exec of an absolute path, no working directory, a fixed
// environment, capped output.
// waitDelay is how long a finished command's leftover children may keep its pipes open.
var waitDelay = 2 * time.Second

func runExec(ctx context.Context, name string, args []string, stdin string, env []string) (Result, error) {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Env = env
	cmd.WaitDelay = waitDelay // a background child holding the pipes must not hold the call
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	var out, errb capBuffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	err := cmd.Run()
	res := Result{Stdout: out.String(), Stderr: errb.String()}
	var ee *exec.ExitError
	switch {
	case err == nil:
	case errors.Is(err, exec.ErrWaitDelay): // the command finished; a leftover child held the pipes
		res.Exit = cmd.ProcessState.ExitCode()
	case errors.As(err, &ee):
		res.Exit = ee.ExitCode()
		if ctx.Err() != nil {
			res.Stderr += "\n[timed out]"
		}
	case ctx.Err() != nil:
		res.Exit = -1
		res.Stderr += "\n[timed out]"
	default:
		res.Exit = -1
		res.Stderr = err.Error()
	}
	return res, nil
}
