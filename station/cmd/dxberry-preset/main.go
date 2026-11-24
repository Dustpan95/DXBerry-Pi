// Command dxberry-preset edits saved frequency definitions. It never controls a radio.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"os"
	"path/filepath"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/presets"
)

type response struct {
	OK      bool             `json:"ok"`
	Library *presets.Library `json:"library,omitempty"`
	Code    string           `json:"code,omitempty"`
	Reason  string           `json:"reason,omitempty"`
}

// run owns in and is used only by this single-purpose process. Some inherited
// OS stdin pipes cannot be interrupted by File.Close, so the deadline ends the
// wait rather than depending on Close to unblock Read. Any blocked reader ends
// when main exits. Paths come only from trusted operator flags, never JSON.
func run(args []string, in io.ReadCloser, out io.Writer, uid int) int {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	defer func() { go func() { _ = in.Close() }() }()
	fail := func(code, reason string, exit int) int {
		_ = json.NewEncoder(out).Encode(response{Code: code, Reason: reason})
		return exit
	}
	if uid != 0 {
		return fail("unavailable", "preset configuration requires root", 6)
	}
	flags := flag.NewFlagSet("dxberry-preset", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	state := flags.String("state", "/var/lib/dxberry/station", "station state directory")
	wantJSON := flags.Bool("json", true, "return JSON")
	if flags.Parse(args) != nil || flags.NArg() != 0 || !*wantJSON || !filepath.IsAbs(*state) || filepath.Clean(*state) != *state {
		return fail("invalid", "invalid preset command options", 2)
	}
	type inputResult struct {
		raw []byte
		err error
	}
	input := make(chan inputResult, 1)
	go func() { raw, err := io.ReadAll(io.LimitReader(in, presets.MaxBytes+1)); input <- inputResult{raw, err} }()
	var received inputResult
	select {
	case <-ctx.Done():
		return fail("unavailable", "preset input is unavailable", 6)
	case received = <-input:
	}
	if received.err != nil || ctx.Err() != nil {
		return fail("unavailable", "preset input is unavailable", 6)
	}
	r, err := presets.DecodeRequest(received.raw)
	if err != nil {
		return fail("invalid", presets.ErrInvalid.Error(), 2)
	}
	store, err := presets.NewStore(filepath.Join(*state, "presets"))
	if err != nil {
		return fail("invalid", presets.ErrInvalid.Error(), 2)
	}
	var l presets.Library
	if r.Operation == "list" {
		l, err = store.List(ctx, r.Radio)
	} else {
		l, err = store.Edit(ctx, r)
	}
	if err != nil {
		switch err {
		case presets.ErrInvalid:
			return fail("invalid", err.Error(), 2)
		case presets.ErrConflict:
			return fail("conflict", err.Error(), 4)
		case presets.ErrNotFound:
			return fail("not_found", err.Error(), 3)
		case presets.ErrLimit:
			return fail("limit", err.Error(), 5)
		case presets.ErrUnknown:
			return fail("unknown", err.Error(), 7)
		default:
			return fail("unavailable", presets.ErrUnavailable.Error(), 6)
		}
	}
	if err := json.NewEncoder(out).Encode(response{OK: true, Library: &l}); err != nil {
		if r.Operation != "list" {
			return 7
		}
		return 6
	}
	return 0
}

func main() { os.Exit(run(os.Args[1:], os.Stdin, os.Stdout, os.Geteuid())) }
