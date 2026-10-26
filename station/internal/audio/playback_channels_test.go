package audio

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// A stereo playback (the keying tone, right channel only) asks pacat for two channels.
func TestPlaybackStereo(t *testing.T) {
	bins, dir := fakeBin(t)
	p := NewPlayback("unix:/run/dxberry/audio/radio1/native", "RADIO1.tx")
	p.Bins, p.Env, p.Channels = bins, []string{"DXB_FAKE_DIR=" + dir}, 2
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.Write([]int16{0, 0x1234}); err != nil {
		t.Fatal(err)
	}
	_ = p.Close()
	a, _ := os.ReadFile(filepath.Join(dir, "pacat.argv"))
	want := "--playback --server=unix:/run/dxberry/audio/radio1/native --device=RADIO1.tx --format=s16le --rate=48000 --channels=2 --latency-msec=20\n"
	if string(a) != want {
		t.Fatalf("argv %q", a)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "pacat.stdin"))
	if string(b) != "\x00\x00\x34\x12" {
		t.Fatalf("stdin %q", b)
	}
}
