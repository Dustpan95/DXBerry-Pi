package audio

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// fakeBin writes bash scripts for parec, pacat and pactl into a temp dir and returns the Bins map.
func fakeBin(t *testing.T) (map[string]string, string) {
	t.Helper()
	dir := t.TempDir()
	scripts := map[string]string{
		// parec: DXB_FAKE_SAMPLES samples of a counting ramp (value = index), then exit; runs counted in $DXB_FAKE_DIR/runs.
		"parec": `#!/bin/bash
echo "$@" >> "$DXB_FAKE_DIR/parec.argv"
echo x >> "$DXB_FAKE_DIR/runs"
s=""
for ((i=0;i<DXB_FAKE_SAMPLES;i++)); do printf -v b '\\x%02x\\x%02x' $((i&255)) $((i>>8&255)); s+=$b; done
printf '%b' "$s"
`,
		"pacat": `#!/bin/bash
echo "$@" > "$DXB_FAKE_DIR/pacat.argv"
cat > "$DXB_FAKE_DIR/pacat.stdin"
`,
		"pactl": `#!/bin/bash
echo "$@" >> "$DXB_FAKE_DIR/pactl.argv"
case "$*" in
*"list short sinks"*) printf '%b' "$DXB_FAKE_SINKS" ;;
*load-module*) echo "${DXB_FAKE_MODULE:-7}" ;;
esac
`,
	}
	bins := map[string]string{}
	for n, body := range scripts {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		bins[n] = p
	}
	return bins, dir
}

func collect(t *testing.T, c *Capture) [][]int16 {
	t.Helper()
	var got [][]int16
	timeout := time.After(10 * time.Second)
	for {
		select {
		case f, ok := <-c.Frames():
			if !ok {
				return got
			}
			got = append(got, f)
		case <-timeout:
			t.Fatal("capture did not finish")
		}
	}
}

func TestCaptureFrames960(t *testing.T) {
	bins, dir := fakeBin(t)
	c := NewCapture("unix:/run/dxberry/audio/radio1/native", "RADIO1.in")
	c.Bins = bins
	c.Env = []string{"DXB_FAKE_DIR=" + dir, "DXB_FAKE_SAMPLES=2500"}
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	got := collect(t, c)
	// two runs (one restart), each 2500 samples = 2 whole frames and 580 dropped samples
	if len(got) != 4 {
		t.Fatalf("got %d frames", len(got))
	}
	for n, f := range got[:2] {
		if len(f) != 960 {
			t.Fatalf("frame %d has %d samples", n, len(f))
		}
		for i, v := range f {
			if int(v) != n*960+i {
				t.Fatalf("frame %d sample %d = %d", n, i, v)
			}
		}
	}
	// a sample above 255 exercises the high byte
	if got[1][959] != 1919 {
		t.Fatalf("last sample %d", got[1][959])
	}
	b, _ := os.ReadFile(filepath.Join(dir, "parec.argv"))
	want := "--server=unix:/run/dxberry/audio/radio1/native --device=RADIO1.in --format=s16le --rate=48000 --channels=1 --latency-msec=20"
	if !strings.HasPrefix(string(b), want+"\n") {
		t.Fatalf("argv %q", b)
	}
}

func TestCaptureRestartsOnce(t *testing.T) {
	bins, dir := fakeBin(t)
	c := NewCapture("unix:/x/native", "RADIO1.in")
	c.Bins = bins
	c.Env = []string{"DXB_FAKE_DIR=" + dir, "DXB_FAKE_SAMPLES=960"}
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if got := collect(t, c); len(got) != 2 {
		t.Fatalf("got %d frames, want 2 (one per run)", len(got))
	}
	if err := c.Err(); err == nil || !strings.Contains(err.Error(), "RADIO1.in") {
		t.Fatalf("err %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "runs"))
	if n := strings.Count(string(b), "x"); n != 2 {
		t.Fatalf("parec ran %d times", n)
	}
}

func TestCaptureCloseStopsReader(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "parec")
	if err := os.WriteFile(p, []byte("#!/bin/bash\nsleep 30\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	before := runtime.NumGoroutine()
	c := NewCapture("unix:/x/native", "RADIO1.in")
	c.Bins = map[string]string{"parec": p}
	if err := c.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	time.Sleep(100 * time.Millisecond)
	done := make(chan struct{})
	go func() { c.Close(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close hung")
	}
	if c.Err() != nil {
		t.Fatalf("err after Close: %v", c.Err())
	}
	time.Sleep(100 * time.Millisecond)
	if n := runtime.NumGoroutine(); n > before+1 {
		t.Fatalf("goroutines %d -> %d", before, n)
	}
}

func TestPlaybackWritesPCM(t *testing.T) {
	bins, dir := fakeBin(t)
	p := NewPlayback("unix:/run/dxberry/audio/radio1/native", "RADIO1.tx")
	p.Bins = bins
	p.Env = []string{"DXB_FAKE_DIR=" + dir}
	if err := p.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := p.Write([]int16{1, -2, 0x1234}); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "pacat.stdin"))
	if string(b) != "\x01\x00\xfe\xff\x34\x12" {
		t.Fatalf("stdin %q", b)
	}
	a, _ := os.ReadFile(filepath.Join(dir, "pacat.argv"))
	want := "--playback --server=unix:/run/dxberry/audio/radio1/native --device=RADIO1.tx --format=s16le --rate=48000 --channels=1 --latency-msec=20\n"
	if string(a) != want {
		t.Fatalf("argv %q", a)
	}
	if p.Write([]int16{1}) == nil {
		t.Fatal("write after Close succeeded")
	}
}
