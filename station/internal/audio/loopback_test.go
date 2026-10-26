package audio

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// The radio's TX loopbacks (and only its own: not another radio's, not other modules) are found
// in pactl's module list and unloaded.
func TestUnloadStaleTXLoopbacks(t *testing.T) {
	c, _ := fakeAudioCLI(t, "{}", 0)
	s, dir := control(t, c, "radio1", "")
	// the fake pactl answers "list short modules" from this file
	list := "0\tmodule-alsa-sink\tdevice=hw:1 sink_name=RADIO1\t\n" +
		"7\tmodule-loopback\tsource=RADIO1.tx.monitor sink=RADIO1 latency_msec=20\t\n" +
		"9\tmodule-loopback\tsource=RADIO10.tx.monitor sink=RADIO10 latency_msec=20\t\n" +
		"11\tmodule-loopback\tsource=RADIO1.tx.monitor sink=RADIO1 latency_msec=20\t\n" +
		"12\tmodule-null-sink\tsink_name=RADIO1.tx\t\n"
	pactl := "#!/bin/bash\necho \"$@\" >> \"$DXB_FAKE_DIR/pactl.argv\"\n" +
		"case \"$*\" in *\"list short modules\"*) cat \"$DXB_FAKE_DIR/modules\" ;; esac\n"
	if err := os.WriteFile(filepath.Join(dir, "modules"), []byte(list), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(s.Bins["pactl"], []byte(pactl), 0o755); err != nil {
		t.Fatal(err)
	}
	n, err := s.UnloadStaleTXLoopbacks(context.Background(), "radio1")
	if err != nil || n != 2 {
		t.Fatalf("n %d err %v", n, err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "pactl.argv"))
	srv := "--server unix:" + s.Socket("radio1")
	want := srv + " list short modules\n" + srv + " unload-module 7\n" + srv + " unload-module 11\n"
	if string(b) != want {
		t.Fatalf("argv %q", b)
	}
	// nothing to unload: no error, none unloaded
	if err := os.WriteFile(filepath.Join(dir, "modules"), []byte("0\tmodule-alsa-sink\tsink_name=RADIO1\t\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if n, err := s.UnloadStaleTXLoopbacks(context.Background(), "radio1"); err != nil || n != 0 {
		t.Fatalf("empty: n %d err %v", n, err)
	}
	// the server cannot be asked: an error, nothing unloaded
	if err := os.WriteFile(s.Bins["pactl"], []byte("#!/bin/bash\necho 'Connection refused' >&2\nexit 1\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	if n, err := s.UnloadStaleTXLoopbacks(context.Background(), "radio1"); err == nil || n != 0 || !strings.Contains(err.Error(), "Connection refused") {
		t.Fatalf("refused: n %d err %v", n, err)
	}
}
