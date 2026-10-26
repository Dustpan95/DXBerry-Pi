package audio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/cli"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
)

type fakeRadios map[string]radios.Radio

func (f fakeRadios) Get(n string) (radios.Radio, bool) { r, ok := f[n]; return r, ok }

// fakeAudioCLI returns a CLI whose dxberry-audio prints status JSON from $STATUS and records argv.
func fakeAudioCLI(t *testing.T, status string, exit int) (*cli.CLI, string) {
	t.Helper()
	bin := t.TempDir()
	log := filepath.Join(t.TempDir(), "audio.argv")
	body := "#!/bin/bash\necho \"$@\" >> '" + log + "'\n" +
		"if [ \"$1\" = status ]; then cat <<'EOF'\n" + status + "\nEOF\nfi\nexit " + string(rune('0'+exit)) + "\n"
	if err := os.WriteFile(filepath.Join(bin, "dxberry-audio"), []byte(body), 0o755); err != nil {
		t.Fatal(err)
	}
	c := cli.New(bin, nil)
	c.Logf = func(string, ...any) {}
	return c, log
}

func control(t *testing.T, c *cli.CLI, radio string, sinks string) (*ServerControl, string) {
	t.Helper()
	bins, dir := fakeBin(t)
	run := t.TempDir()
	s := NewServerControl(c, run)
	s.Bins = bins
	s.Env = []string{"DXB_FAKE_DIR=" + dir, "DXB_FAKE_SINKS=" + sinks}
	return s, dir
}

func makeSocket(t *testing.T, s *ServerControl, radio string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(s.Socket(radio)), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Socket(radio), nil, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestAvailableReasons(t *testing.T) {
	good := `{"radio":"radio1","active":true,"socket":"x","card":"RADIO1","present":true}`
	c, _ := fakeAudioCLI(t, good, 0)
	both := "0\\tRADIO1\\tmodule-alsa-sink.c\\ts16le\\n1\\tRADIO1.tx\\tmodule-null-sink.c\\ts16le\\n"
	s, dir := control(t, c, "radio1", both)

	// no socket, owner is a pulse program (or unknown): the socket is missing
	ok, why := s.Available("radio1")
	if ok || why != "no sound server for radio1: "+s.Socket("radio1")+" is missing" {
		t.Fatalf("got %v %q", ok, why)
	}
	// the default run directory spelling
	d := NewServerControl(c, "")
	if _, why := d.Available("radio1"); why != "no sound server for radio1: /run/dxberry/audio/radio1/native is missing" {
		t.Fatalf("default: %q", why)
	}
	// an alsa program owns the card
	s.Radios = fakeRadios{"radio1": {Name: "radio1", Owner: "wsjtx"}}
	if ok, why = s.Available("radio1"); ok || why != "no sound server for radio1: WSJT-X has the sound card" {
		t.Fatalf("got %v %q", ok, why)
	}
	s.Radios = fakeRadios{"radio1": {Name: "radio1", Owner: "js8call"}}
	if ok, why = s.Available("radio1"); ok || !strings.HasSuffix(why, "native is missing") {
		t.Fatalf("pulse owner: %v %q", ok, why)
	}
	// socket there, both sinks listed
	makeSocket(t, s, "radio1")
	if ok, why = s.Available("radio1"); !ok || why != "" {
		t.Fatalf("got %v %q", ok, why)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "pactl.argv"))
	if !strings.Contains(string(b), "--server unix:"+s.Socket("radio1")+" list short sinks") {
		t.Fatalf("pactl argv %q", b)
	}
	// the tx sink missing
	s.Env = []string{"DXB_FAKE_DIR=" + dir, "DXB_FAKE_SINKS=0\\tRADIO1\\tm\\ts\\n"}
	if ok, why = s.Available("radio1"); ok || why != "no sound server for radio1: sink RADIO1.tx is missing" {
		t.Fatalf("got %v %q", ok, why)
	}
	// the unit reports the card absent
	c2, _ := fakeAudioCLI(t, `{"radio":"radio1","active":false,"socket":"x","card":"RADIO1","present":false}`, 0)
	s.cli = c2
	if ok, _ = s.Available("radio1"); ok {
		t.Fatal("present=false was available")
	}
}

func TestEnsureRunsUnitAndWaitsForSocket(t *testing.T) {
	c, log := fakeAudioCLI(t, "{}", 0)
	s, _ := control(t, c, "radio1", "")
	s.SocketWait = 200 * time.Millisecond
	if err := s.Ensure(context.Background(), "radio1"); err == nil {
		t.Fatal("no socket, no error")
	}
	b, _ := os.ReadFile(log)
	if string(b) != "ensure radio1 --json\n" {
		t.Fatalf("argv %q", b)
	}
	go func() { time.Sleep(60 * time.Millisecond); makeSocket(t, s, "radio1") }()
	s.SocketWait = 5 * time.Second
	if err := s.Ensure(context.Background(), "radio1"); err != nil {
		t.Fatal(err)
	}
	cf, _ := fakeAudioCLI(t, "{}", 6)
	s.cli = cf
	if err := s.Ensure(context.Background(), "radio1"); err == nil || !strings.Contains(err.Error(), "exit 6") {
		t.Fatalf("err %v", err)
	}
	if err := s.Ensure(context.Background(), "Bad Name"); err == nil {
		t.Fatal("bad name accepted")
	}
}

func TestLoopbackLoadUnload(t *testing.T) {
	c, _ := fakeAudioCLI(t, "{}", 0)
	s, dir := control(t, c, "radio1", "")
	s.Env = append(s.Env, "DXB_FAKE_MODULE=42")
	id, err := s.LoadTXLoopback(context.Background(), "radio1")
	if err != nil || id != 42 {
		t.Fatalf("id %d err %v", id, err)
	}
	if err := s.UnloadTXLoopback(context.Background(), "radio1", id); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "pactl.argv"))
	srv := "--server unix:" + s.Socket("radio1")
	want := srv + " load-module module-loopback source=RADIO1.tx.monitor sink=RADIO1 latency_msec=20\n" +
		srv + " unload-module 42\n"
	if string(b) != want {
		t.Fatalf("argv %q", b)
	}
	s.Env = append(s.Env, "DXB_FAKE_MODULE=oops")
	if _, err := s.LoadTXLoopback(context.Background(), "radio1"); err == nil {
		t.Fatal("garbage module id accepted")
	}
}

func TestTwoRadiosIsolatedSinks(t *testing.T) {
	c, _ := fakeAudioCLI(t, "{}", 0)
	a, dirA := control(t, c, "radio1", "")
	b, dirB := control(t, c, "radio2", "")
	if _, err := a.LoadTXLoopback(context.Background(), "radio1"); err != nil {
		t.Fatal(err)
	}
	if _, err := b.LoadTXLoopback(context.Background(), "radio2"); err != nil {
		t.Fatal(err)
	}
	if a.Socket("radio1") == b.Socket("radio2") {
		t.Fatal("shared socket")
	}
	ra, _ := os.ReadFile(filepath.Join(dirA, "pactl.argv"))
	rb, _ := os.ReadFile(filepath.Join(dirB, "pactl.argv"))
	if strings.Contains(string(ra), "RADIO2") || strings.Contains(string(ra), "radio2") ||
		strings.Contains(string(rb), "RADIO1") || strings.Contains(string(rb), "radio1") {
		t.Fatalf("crosstalk:\n%s%s", ra, rb)
	}
	ca, cb := NewCapture(a.Server("radio1"), Card("radio1")+".in"), NewCapture(b.Server("radio2"), Card("radio2")+".in")
	if ca.server == cb.server || ca.source == cb.source {
		t.Fatal("captures share a server or source")
	}
}
