package audio

import (
	"context"
	"encoding/binary"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/rtp"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"
)

// toneBins writes a fake parec that plays a 1 kHz tone in real time (one 20 ms frame, then a
// 20 ms sleep, forever) and a fake pacat that appends what it is given to $DXB_FAKE_DIR/pacat.stdin.
func toneBins(t *testing.T) (map[string]string, []string) {
	t.Helper()
	dir := t.TempDir()
	frame := toneFrame(0, 1000, 8000) // 20 whole cycles: frames join without a click
	b := make([]byte, 2*len(frame))
	for i, v := range frame {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(v))
	}
	tone := filepath.Join(dir, "tone.raw")
	if err := os.WriteFile(tone, b, 0o644); err != nil {
		t.Fatal(err)
	}
	scripts := map[string]string{
		"parec": "#!/bin/bash\nwhile :; do cat \"$DXB_TONE\" || exit 1; sleep 0.02; done\n",
		"pacat": "#!/bin/bash\necho \"$@\" > \"$DXB_FAKE_DIR/pacat.argv\"\ncat >> \"$DXB_FAKE_DIR/pacat.stdin\"\n",
	}
	bins := map[string]string{}
	for n, body := range scripts {
		p := filepath.Join(dir, n)
		if err := os.WriteFile(p, []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
		bins[n] = p
	}
	return bins, []string{"DXB_TONE=" + tone, "DXB_FAKE_DIR=" + dir}
}

func fakeDir(env []string) string {
	for _, e := range env {
		if v, ok := strings.CutPrefix(e, "DXB_FAKE_DIR="); ok {
			return v
		}
	}
	return ""
}

func testLog(t *testing.T) *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// testBridge is radio1's bridge over the fake parec and pacat; it offers loopback candidates,
// the only interface the test container has.
func testBridge(t *testing.T) (*Bridge, string) {
	t.Helper()
	bins, env := toneBins(t)
	c := NewCapture("unix:/run/dxberry/audio/radio1/native", "RADIO1.in")
	c.Bins, c.Env = bins, env
	p := NewPlayback("unix:/run/dxberry/audio/radio1/native", "RADIO1.tx")
	p.Bins, p.Env = bins, env
	b := NewBridge("radio1", c, p, DefaultPorts, testLog(t))
	b.Loopback = true
	t.Cleanup(b.Shutdown)
	return b, fakeDir(env)
}

// browser is a Pion peer standing in for a browser tab: one sendrecv audio transceiver, a mic
// track it can write Opus into, and the RTP it receives.
type browser struct {
	pc  *webrtc.PeerConnection
	mic *webrtc.TrackLocalStaticSample
	rtp chan *rtp.Packet
}

func newBrowser(t *testing.T) *browser {
	t.Helper()
	se := webrtc.SettingEngine{}
	se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
	se.SetIncludeLoopbackCandidate(true)
	se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
	m := &webrtc.MediaEngine{}
	if err := m.RegisterDefaultCodecs(); err != nil {
		t.Fatal(err)
	}
	pc, err := webrtc.NewAPI(webrtc.WithSettingEngine(se), webrtc.WithMediaEngine(m)).NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pc.Close() })
	mic, err := webrtc.NewTrackLocalStaticSample(webrtc.RTPCodecCapability{MimeType: webrtc.MimeTypeOpus, ClockRate: 48000, Channels: 2}, "mic", "browser")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := pc.AddTransceiverFromTrack(mic, webrtc.RTPTransceiverInit{Direction: webrtc.RTPTransceiverDirectionSendrecv}); err != nil {
		t.Fatal(err)
	}
	br := &browser{pc: pc, mic: mic, rtp: make(chan *rtp.Packet, 500)}
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		for {
			p, _, err := tr.ReadRTP()
			if err != nil {
				return
			}
			select {
			case br.rtp <- p:
			default:
			}
		}
	})
	return br
}

func (br *browser) offer(t *testing.T) string {
	t.Helper()
	o, err := br.pc.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	done := webrtc.GatheringCompletePromise(br.pc)
	if err := br.pc.SetLocalDescription(o); err != nil {
		t.Fatal(err)
	}
	<-done
	return br.pc.LocalDescription().SDP
}

// connect offers to b as tab and applies the answer.
func (br *browser) connect(t *testing.T, b *Bridge, tab TabID) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ans, err := b.Connect(ctx, br.offer(t), tab)
	if err != nil {
		t.Fatal(err)
	}
	if err := br.pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: ans}); err != nil {
		t.Fatal(err)
	}
	return ans
}

// firstRTP waits up to within for a packet.
func (br *browser) firstRTP(t *testing.T, within time.Duration) *rtp.Packet {
	t.Helper()
	select {
	case p := <-br.rtp:
		return p
	case <-time.After(within):
		t.Fatalf("no RTP within %v", within)
		return nil
	}
}

// talk writes the encoded tone into the mic track every 20 ms until stop is closed.
func (br *browser) talk(t *testing.T, stop <-chan struct{}) *sync.WaitGroup {
	t.Helper()
	enc, err := NewEncoder()
	if err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		tk := time.NewTicker(20 * time.Millisecond)
		defer tk.Stop()
		for n := 0; ; n++ {
			select {
			case <-stop:
				return
			case <-tk.C:
			}
			pkt, err := enc.Encode(toneFrame(n, 1000, 8000))
			if err != nil {
				return
			}
			_ = br.mic.WriteSample(media.Sample{Data: pkt, Duration: 20 * time.Millisecond})
		}
	}()
	return &wg
}

func fileSize(p string) int64 {
	st, err := os.Stat(p)
	if err != nil {
		return 0
	}
	return st.Size()
}

func TestBridgeAnswersOffer(t *testing.T) {
	b, _ := testBridge(t)
	br := newBrowser(t)
	ans := br.connect(t, b, "sess|tab1")
	sd := webrtc.SessionDescription{Type: webrtc.SDPTypeAnswer, SDP: ans}
	parsed, err := sd.Unmarshal()
	if err != nil {
		t.Fatalf("the answer does not parse: %v", err)
	}
	if len(parsed.MediaDescriptions) != 1 || parsed.MediaDescriptions[0].MediaName.Media != "audio" {
		t.Fatalf("media %+v", parsed.MediaDescriptions)
	}
	if !strings.Contains(ans, "a=sendrecv") || !strings.Contains(strings.ToLower(ans), "opus/48000/2") {
		t.Fatalf("answer is not sendrecv opus:\n%s", ans)
	}
	if !strings.Contains(ans, "useinbandfec=1") || !strings.Contains(ans, "maxaveragebitrate=24000") {
		t.Fatalf("answer fmtp:\n%s", ans)
	}
	n := 0
	for _, line := range strings.Split(ans, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "a=candidate:") {
			continue
		}
		n++
		f := strings.Fields(line)
		if len(f) < 8 || f[6] != "typ" || f[7] != "host" || !strings.EqualFold(f[2], "udp") {
			t.Fatalf("not a UDP host candidate: %q", line)
		}
		port, err := strconv.Atoi(f[5])
		if err != nil || port < 50000 || port > 50199 {
			t.Fatalf("candidate port outside 50000-50199: %q", line)
		}
		if strings.HasSuffix(f[4], ".local") {
			t.Fatalf("an mDNS candidate: %q", line)
		}
	}
	if n == 0 {
		t.Fatalf("no candidates in the answer:\n%s", ans)
	}
	if b.Listeners() != 1 {
		t.Fatalf("listeners %d", b.Listeners())
	}
	// an offer that is not SDP, and one without audio, are refused
	ctx := context.Background()
	if _, err := b.Connect(ctx, "nonsense", "sess|tab2"); err == nil {
		t.Fatal("a garbled offer was answered")
	}
	data, err := webrtc.NewPeerConnection(webrtc.Configuration{})
	if err != nil {
		t.Fatal(err)
	}
	defer data.Close()
	if _, err := data.CreateDataChannel("x", nil); err != nil {
		t.Fatal(err)
	}
	o, err := data.CreateOffer(nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := b.Connect(ctx, o.SDP, "sess|tab2"); err == nil || !strings.Contains(err.Error(), "no audio") {
		t.Fatalf("an offer without audio: %v", err)
	}
	if b.Listeners() != 1 {
		t.Fatalf("a refused offer changed the listeners: %d", b.Listeners())
	}
}

func TestBridgeRXTrackCarriesAudio(t *testing.T) {
	b, _ := testBridge(t)
	br := newBrowser(t)
	br.connect(t, b, "sess|tab1")
	p := br.firstRTP(t, 2*time.Second)
	if p.PayloadType != 111 || len(p.Payload) == 0 {
		t.Fatalf("packet pt %d, %d bytes", p.PayloadType, len(p.Payload))
	}
	// the packets carry the tone: decode a second of them
	dec, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	var out []int16
	for i := 0; i < 50; i++ {
		pcm, err := dec.Decode(br.firstRTP(t, 2*time.Second).Payload)
		if err != nil {
			t.Fatal(err)
		}
		out = append(out, pcm...)
	}
	if r := rms(out[20*FrameSamples:]); r < 4000 || r > 7000 {
		t.Fatalf("decoded RX RMS %.1f, want the tone's ~5657", r)
	}
}

func TestBridgeTXIgnoredUnlessAllowed(t *testing.T) {
	b, dir := testBridge(t)
	br := newBrowser(t)
	br.connect(t, b, "sess|tab1")
	br.firstRTP(t, 2*time.Second) // connected
	stop := make(chan struct{})
	wg := br.talk(t, stop)
	defer func() { close(stop); wg.Wait() }()
	stdin := filepath.Join(dir, "pacat.stdin")

	time.Sleep(600 * time.Millisecond)
	if n := fileSize(stdin); n != 0 {
		t.Fatalf("%d bytes reached pacat before TX was allowed", n)
	}
	if b.TXAllowed("sess|tab1") {
		t.Fatal("TX allowed by default")
	}
	b.SetTXAllowed("sess|tab2", true) // another tab's PTT does not let this one talk
	time.Sleep(400 * time.Millisecond)
	if n := fileSize(stdin); n != 0 {
		t.Fatalf("%d bytes reached pacat on another tab's PTT", n)
	}
	b.SetTXAllowed("sess|tab2", false)

	b.SetTXAllowed("sess|tab1", true)
	if !b.TXAllowed("sess|tab1") {
		t.Fatal("TXAllowed did not follow SetTXAllowed")
	}
	deadline := time.Now().Add(2 * time.Second)
	for fileSize(stdin) < 20*FrameSamples*2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if n := fileSize(stdin); n < 20*FrameSamples*2 {
		t.Fatalf("only %d bytes reached pacat while TX was allowed", n)
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "pacat.argv"))
	if !strings.Contains(string(argv), "--device=RADIO1.tx") {
		t.Fatalf("pacat argv %q", argv)
	}
	b.SetTXAllowed("sess|tab1", false)
	time.Sleep(300 * time.Millisecond)
	was := fileSize(stdin)
	time.Sleep(400 * time.Millisecond)
	if now := fileSize(stdin); now != was {
		t.Fatalf("pacat still got audio after TX ended: %d -> %d", was, now)
	}
	// the TX audio is the browser's tone (the codec's delay is in the first frames)
	raw, _ := os.ReadFile(stdin)
	pcm := make([]int16, len(raw)/2)
	for i := range pcm {
		pcm[i] = int16(binary.LittleEndian.Uint16(raw[2*i:]))
	}
	if r := rms(pcm[10*FrameSamples:]); r < 4000 || r > 7000 {
		t.Fatalf("TX RMS %.1f", r)
	}
}

func TestTwoPeerConnectionsSeparateTracks(t *testing.T) {
	b, _ := testBridge(t)
	one, two := newBrowser(t), newBrowser(t)
	one.connect(t, b, "sess|tab1")
	two.connect(t, b, "sess|tab2")
	if b.Listeners() != 2 {
		t.Fatalf("listeners %d", b.Listeners())
	}
	p1, p2 := one.firstRTP(t, 2*time.Second), two.firstRTP(t, 2*time.Second)
	if p1.SSRC == p2.SSRC {
		t.Fatalf("both connections carry SSRC %d", p1.SSRC)
	}
	// closing one leaves the other playing
	b.Close("sess|tab1")
	if b.Listeners() != 1 {
		t.Fatalf("listeners after close %d", b.Listeners())
	}
	for len(two.rtp) > 0 {
		<-two.rtp
	}
	if p := two.firstRTP(t, 2*time.Second); p.SSRC != p2.SSRC {
		t.Fatalf("the remaining connection changed SSRC %d -> %d", p2.SSRC, p.SSRC)
	}
	// the same tab offering again replaces its connection
	three := newBrowser(t)
	three.connect(t, b, "sess|tab2")
	if b.Listeners() != 1 {
		t.Fatalf("listeners after a re-offer %d", b.Listeners())
	}
	three.firstRTP(t, 2*time.Second)
	b.Close("sess|tab2")
	b.Close("sess|tab2")
	if b.Listeners() != 0 {
		t.Fatalf("listeners %d", b.Listeners())
	}
}

func TestBridgeLostWithoutInboundRTP(t *testing.T) {
	b, _ := testBridge(t)
	b.LostAfter = 400 * time.Millisecond
	lost := make(chan string, 4)
	cancel := b.OnLost(func(tab TabID, reason string) { lost <- tab + ": " + reason })
	defer cancel()
	br := newBrowser(t)
	br.connect(t, b, "sess|tab1")
	br.firstRTP(t, 2*time.Second)
	// listening without TX never times out
	select {
	case m := <-lost:
		t.Fatalf("lost while only listening: %s", m)
	case <-time.After(800 * time.Millisecond):
	}
	// TX allowed but no RTP from the browser: the connection is torn down and the tab told
	b.SetTXAllowed("sess|tab1", true)
	select {
	case m := <-lost:
		if !strings.HasPrefix(m, "sess|tab1: ") {
			t.Fatalf("lost %q", m)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no audio-lost while TX had no RTP")
	}
	if b.Listeners() != 0 {
		t.Fatalf("listeners %d", b.Listeners())
	}
	b.SetTXAllowed("sess|tab1", false)
}

func TestBridgeCaptureStopsWithTheLastListener(t *testing.T) {
	b, _ := testBridge(t)
	br := newBrowser(t)
	br.connect(t, b, "sess|tab1")
	br.firstRTP(t, 2*time.Second)
	b.Close("sess|tab1")
	if b.capturing() {
		t.Fatal("capture still running with no listener")
	}
	// the next listener starts a fresh capture
	again := newBrowser(t)
	again.connect(t, b, "sess|tab2")
	again.firstRTP(t, 2*time.Second)
	if !b.capturing() {
		t.Fatal("no capture for the new listener")
	}
}

func TestBridgeTrickledCandidates(t *testing.T) {
	b, _ := testBridge(t)
	br := newBrowser(t)
	br.connect(t, b, "sess|tab1")
	if err := b.AddICECandidate("sess|tab1", Candidate{}); err != nil {
		t.Fatalf("an end-of-candidates marker: %v", err)
	}
	if err := b.AddICECandidate("sess|nobody", Candidate{Candidate: "candidate:1 1 udp 1 127.0.0.1 50001 typ host"}); err == nil {
		t.Fatal("a candidate for a tab with no connection was taken")
	}
	if err := b.AddICECandidate("sess|tab1", Candidate{Candidate: "garbage"}); err == nil {
		t.Fatal("a garbled candidate was taken")
	}
}
