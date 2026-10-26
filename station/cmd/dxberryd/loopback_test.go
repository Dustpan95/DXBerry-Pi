package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/audio"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/cli"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
)

// At start the TX loopbacks a crash left are unloaded, for each radio whose sound server is up.
func TestStartClearsStaleLoopbacks(t *testing.T) {
	dir, run := t.TempDir(), t.TempDir()
	argv := filepath.Join(dir, "pactl.argv")
	pactl := filepath.Join(dir, "pactl")
	body := "#!/bin/bash\necho \"$@\" >> '" + argv + "'\n" +
		"case \"$*\" in *\"list short modules\"*) printf '5\\tmodule-loopback\\tsource=RADIO1.tx.monitor sink=RADIO1 latency_msec=20\\t\\n' ;; esac\n"
	if err := os.WriteFile(pactl, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	ctl := audio.NewServerControl(cli.New(dir, nil), run)
	ctl.Bins = map[string]string{"pactl": pactl}
	if err := os.MkdirAll(filepath.Dir(ctl.Socket("radio1")), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(ctl.Socket("radio1"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	clearStaleLoopbacks(context.Background(), ctl, []radios.Radio{{Name: "radio1"}, {Name: "radio2"}})
	b, _ := os.ReadFile(argv)
	srv := "--server unix:" + ctl.Socket("radio1")
	if want := srv + " list short modules\n" + srv + " unload-module 5\n"; string(b) != want {
		t.Fatalf("pactl calls %q, want %q", b, want)
	}
}

// The radios are cleared at once, not one after another: the station serves only after the clear.
func TestStartClearsRadiosAtOnce(t *testing.T) {
	dir, run := t.TempDir(), t.TempDir()
	argv := filepath.Join(dir, "pactl.argv")
	pactl := filepath.Join(dir, "pactl")
	body := "#!/bin/bash\necho \"$@\" >> '" + argv + "'\nsleep 0.6\n"
	if err := os.WriteFile(pactl, []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	ctl := audio.NewServerControl(cli.New(dir, nil), run)
	ctl.Bins = map[string]string{"pactl": pactl}
	for _, r := range []string{"radio1", "radio2", "radio3"} {
		if err := os.MkdirAll(filepath.Dir(ctl.Socket(r)), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(ctl.Socket(r), nil, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	start := time.Now()
	clearStaleLoopbacks(context.Background(), ctl, []radios.Radio{{Name: "radio1"}, {Name: "radio2"}, {Name: "radio3"}})
	if took := time.Since(start); took > 1500*time.Millisecond {
		t.Fatalf("three radios took %v: cleared one after another", took)
	}
	b, _ := os.ReadFile(argv)
	if n := strings.Count(string(b), "list short modules"); n != 3 {
		t.Fatalf("pactl calls %q", b)
	}
}
