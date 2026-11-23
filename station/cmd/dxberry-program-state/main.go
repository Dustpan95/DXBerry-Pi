// Command dxberry-program-state advances a private program generation. It does
// not start/stop programs or control radios. The session unit invokes it first.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/programstate"
)

const commandLimit = 3 * time.Second

type response struct {
	OK       bool   `json:"ok"`
	Protocol int    `json:"protocol"`
	Code     string `json:"code,omitempty"`
	Reason   string `json:"reason,omitempty"`
}

type advanceFunc func(context.Context, string, bool) error

func advanceGeneration(ctx context.Context, state string, prepare bool) error {
	store, err := programstate.NewStore(state)
	if err != nil {
		return err
	}
	if prepare {
		if err := store.PrepareParent(ctx); err != nil {
			return err
		}
	}
	_, err = store.Advance(ctx)
	return err
}

func stateArgument(args []string) (string, bool, bool) {
	state := "/var/lib/dxberry/station"
	if len(args) == 3 && args[0] == "--state" {
		state, args = args[1], args[2:]
	}
	if len(args) != 1 || (args[0] != "advance" && args[0] != "prepare-advance") || len(state) > 4096 || !filepath.IsAbs(state) || filepath.Clean(state) != state || state == "/" || strings.ContainsRune(state, 0) {
		return "", false, false
	}
	return state, args[0] == "prepare-advance", true
}

// run never reads stdin or environment configuration. Only trusted operator
// argv selects the state parent. The buffered worker result cannot block after
// a timeout; main exits the process rather than waiting on a stalled syscall.
func run(args []string, out io.Writer, uid int, advance advanceFunc) int {
	fail := func(code, reason string, exit int) int {
		_ = json.NewEncoder(out).Encode(response{Protocol: 1, Code: code, Reason: reason})
		return exit
	}
	if uid != 0 {
		return fail("unavailable", "program generation requires root", 6)
	}
	state, prepare, ok := stateArgument(args)
	if !ok {
		return fail("invalid", "invalid program generation command options", 2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), commandLimit)
	defer cancel()
	result := make(chan error, 1)
	go func() { result <- advance(ctx, state, prepare) }()
	var err error
	select {
	case <-ctx.Done():
		// Publication may have happened without a result reaching this process.
		return fail("uncertain", programstate.ErrUncertain.Error(), 7)
	case err = <-result:
	}
	if ctx.Err() != nil {
		return fail("uncertain", programstate.ErrUncertain.Error(), 7)
	}
	if errors.Is(err, programstate.ErrUncertain) {
		return fail("uncertain", programstate.ErrUncertain.Error(), 7)
	}
	if err != nil {
		return fail("unavailable", programstate.ErrUnavailable.Error(), 6)
	}
	if json.NewEncoder(out).Encode(response{OK: true, Protocol: 1}) != nil {
		return 7
	}
	return 0
}

func main() {
	// A closed stdout pipe must report refusal/uncertainty through an exit code,
	// not terminate with SIGPIPE. The watchdog also bounds a full output pipe or
	// a worker stuck inside a filesystem/entropy call. It never reports success.
	signal.Ignore(syscall.SIGPIPE)
	watchdog := time.AfterFunc(commandLimit+250*time.Millisecond, func() { os.Exit(7) })
	code := run(os.Args[1:], os.Stdout, os.Geteuid(), advanceGeneration)
	watchdog.Stop()
	os.Exit(code)
}
