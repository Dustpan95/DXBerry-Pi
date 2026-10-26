package audio

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pion/ice/v4"
	"github.com/pion/interceptor"
	"github.com/pion/webrtc/v4"
	"github.com/pion/webrtc/v4/pkg/media"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/session"
)

// TabID names one browser tab: session.TabID(session, tab token). The PTT layer uses the same
// value for SetTXAllowed.
type TabID = string

// PortRange is the UDP ports the host candidates are taken from.
type PortRange struct{ Min, Max uint16 }

// DefaultPorts is the station's WebRTC range.
var DefaultPorts = PortRange{Min: 50000, Max: 50199}

// LostAfter is how long a transmitting connection may go without RTP from the browser.
const LostAfter = 5 * time.Second

const (
	gatherWait = 5 * time.Second
	frameTime  = 20 * time.Millisecond
	maxGap     = 10 // a sequence jump larger than this is a restart, not loss
)

// opusCodec is the one codec offered: Opus 48 kHz (always "/2" in SDP), FEC on, TX at TXBitrate.
var opusCodec = webrtc.RTPCodecCapability{
	MimeType:    webrtc.MimeTypeOpus,
	ClockRate:   SampleRate,
	Channels:    2,
	SDPFmtpLine: fmt.Sprintf("minptime=10;useinbandfec=1;maxaveragebitrate=%d", TXBitrate),
}

// askOpus makes the answer's Opus fmtp ask the browser for in-band FEC and TXBitrate: Pion
// answers with the offer's own parameters, which name neither.
func askOpus(sdp string) string {
	lines := strings.Split(sdp, "\r\n")
	pts := map[string]bool{}
	for _, l := range lines {
		if rest, ok := strings.CutPrefix(l, "a=rtpmap:"); ok {
			if pt, enc, ok := strings.Cut(rest, " "); ok && strings.HasPrefix(strings.ToLower(enc), "opus/") {
				pts[pt] = true
			}
		}
	}
	want := []string{"useinbandfec=1", fmt.Sprintf("maxaveragebitrate=%d", TXBitrate)}
	seen := map[string]bool{}
	for i, l := range lines {
		rest, ok := strings.CutPrefix(l, "a=fmtp:")
		if !ok {
			continue
		}
		pt, params, _ := strings.Cut(rest, " ")
		if !pts[pt] {
			continue
		}
		seen[pt] = true
		kept := []string{}
		for _, p := range strings.Split(params, ";") {
			k, _, _ := strings.Cut(strings.TrimSpace(p), "=")
			if p = strings.TrimSpace(p); p != "" && k != "useinbandfec" && k != "maxaveragebitrate" {
				kept = append(kept, p)
			}
		}
		lines[i] = "a=fmtp:" + pt + " " + strings.Join(append(kept, want...), ";")
	}
	out := make([]string, 0, len(lines)+len(pts))
	for _, l := range lines {
		out = append(out, l)
		if rest, ok := strings.CutPrefix(l, "a=rtpmap:"); ok {
			if pt, _, _ := strings.Cut(rest, " "); pts[pt] && !seen[pt] {
				out = append(out, "a=fmtp:"+pt+" "+strings.Join(want, ";"))
			}
		}
	}
	return strings.Join(out, "\r\n")
}

// Candidate is a trickled ICE candidate as the browser's RTCIceCandidate.toJSON() gives it.
type Candidate struct {
	Candidate        string  `json:"candidate"`
	SDPMid           *string `json:"sdpMid,omitempty"`
	SDPMLineIndex    *uint16 `json:"sdpMLineIndex,omitempty"`
	UsernameFragment *string `json:"usernameFragment,omitempty"`
}

// conn is one tab's peer connection.
type conn struct {
	tab     TabID
	pc      *webrtc.PeerConnection
	track   *webrtc.TrackLocalStaticSample
	lastRTP atomic.Int64 // unix nanoseconds of the last packet from the browser (the add time at first)
	upAt    atomic.Int64 // unix nanoseconds the link came up (0: not yet)
	inbound atomic.Bool  // a packet has come from the browser (its microphone is attached)
	done    chan struct{}
	once    sync.Once
}

// close ends the connection; it reports whether this call did it.
func (c *conn) close() bool {
	first := false
	c.once.Do(func() {
		first = true
		close(c.done)
		_ = c.pc.Close()
	})
	return first
}

// Bridge carries one radio's audio to and from the browsers: one peer connection per tab, one
// Opus encoder for every listener, and the microphone of a tab the PTT layer allows to the radio.
type Bridge struct {
	radio string
	ports PortRange
	log   *slog.Logger

	// Loopback offers loopback host candidates too (tests only: a browser is never on loopback).
	Loopback bool
	// LostAfter bounds a transmitting connection's silence (LostAfter by default).
	LostAfter time.Duration

	ctx    context.Context
	cancel context.CancelFunc
	now    func() time.Time

	apiOnce sync.Once
	api     *webrtc.API
	apiErr  error

	encOnce sync.Once
	enc     *Encoder
	encErr  error

	txMu sync.Mutex // serializes the playback's start and stop

	mu      sync.Mutex
	conns   map[TabID]*conn
	allowed map[TabID]time.Time
	tmpl    *Capture
	used    bool
	cap     *Capture
	play    *Playback
	playOn  bool
	playGen uint64 // counts pacat starts, so a write error is matched to the pacat it hit
	retried bool
	lostFns map[int]func(TabID, string)
	nextFn  int
	shut    bool
}

// NewBridge makes radio's bridge. capture is <CARD>.in, play is <CARD>.tx; capture runs only
// while somebody listens (a fresh copy of it after it stops), play only while a tab may transmit.
func NewBridge(radio string, capture *Capture, play *Playback, ports PortRange, log *slog.Logger) *Bridge {
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(context.Background())
	return &Bridge{
		radio: radio, ports: ports, log: log.With("radio", radio), LostAfter: LostAfter,
		ctx: ctx, cancel: cancel, tmpl: capture, play: play,
		conns: map[TabID]*conn{}, allowed: map[TabID]time.Time{}, lostFns: map[int]func(TabID, string){},
	}
}

func (b *Bridge) webrtcAPI() (*webrtc.API, error) {
	b.apiOnce.Do(func() {
		se := webrtc.SettingEngine{}
		if err := se.SetEphemeralUDPPortRange(b.ports.Min, b.ports.Max); err != nil {
			b.apiErr = err
			return
		}
		se.SetNetworkTypes([]webrtc.NetworkType{webrtc.NetworkTypeUDP4})
		se.SetICEMulticastDNSMode(ice.MulticastDNSModeDisabled)
		se.SetIncludeLoopbackCandidate(b.Loopback)
		m := &webrtc.MediaEngine{}
		if err := m.RegisterCodec(webrtc.RTPCodecParameters{RTPCodecCapability: opusCodec, PayloadType: 111}, webrtc.RTPCodecTypeAudio); err != nil {
			b.apiErr = err
			return
		}
		ir := &interceptor.Registry{}
		if err := webrtc.RegisterDefaultInterceptors(m, ir); err != nil {
			b.apiErr = err
			return
		}
		b.api = webrtc.NewAPI(webrtc.WithSettingEngine(se), webrtc.WithMediaEngine(m), webrtc.WithInterceptorRegistry(ir))
	})
	return b.api, b.apiErr
}

func (b *Bridge) encoder() (*Encoder, error) {
	b.encOnce.Do(func() { b.enc, b.encErr = NewEncoder() })
	return b.enc, b.encErr
}

// Connect answers a tab's offer with a peer connection carrying the radio's audio (and, while
// the tab may transmit, its microphone to the radio). The answer holds every host candidate;
// the browser's own may follow through AddICECandidate. An earlier connection of the tab is replaced.
func (b *Bridge) Connect(ctx context.Context, offerSDP string, tab TabID) (string, error) {
	if tab == "" {
		return "", errors.New("no tab")
	}
	if _, err := b.encoder(); err != nil {
		return "", err
	}
	api, err := b.webrtcAPI()
	if err != nil {
		return "", fmt.Errorf("webrtc: %w", err)
	}
	pc, err := api.NewPeerConnection(webrtc.Configuration{}) // no ICE servers: host candidates only
	if err != nil {
		return "", fmt.Errorf("webrtc: %w", err)
	}
	fail := func(err error) (string, error) {
		_ = pc.Close()
		return "", err
	}
	if err := pc.SetRemoteDescription(webrtc.SessionDescription{Type: webrtc.SDPTypeOffer, SDP: offerSDP}); err != nil {
		return fail(fmt.Errorf("the browser's offer was not understood: %w", err))
	}
	hasAudio := false
	for _, tr := range pc.GetTransceivers() {
		if tr.Kind() == webrtc.RTPCodecTypeAudio {
			hasAudio = true
		}
	}
	if !hasAudio {
		return fail(errors.New("the offer carries no audio"))
	}
	track, err := webrtc.NewTrackLocalStaticSample(opusCodec, "audio", "dxberry-"+b.radio)
	if err != nil {
		return fail(err)
	}
	sender, err := pc.AddTrack(track) // takes the offer's audio transceiver
	if err != nil {
		return fail(err)
	}
	go func() { // RTCP must be read for the interceptors to run
		buf := make([]byte, 1500)
		for {
			if _, _, err := sender.Read(buf); err != nil {
				return
			}
		}
	}()
	c := &conn{tab: tab, pc: pc, track: track, done: make(chan struct{})}
	pc.OnTrack(func(tr *webrtc.TrackRemote, _ *webrtc.RTPReceiver) {
		if tr.Kind() == webrtc.RTPCodecTypeAudio {
			go b.receive(c, tr)
		}
	})
	pc.OnConnectionStateChange(func(s webrtc.PeerConnectionState) {
		if s == webrtc.PeerConnectionStateConnected {
			c.upAt.CompareAndSwap(0, b.clock().UnixNano())
		}
		if s == webrtc.PeerConnectionStateFailed || s == webrtc.PeerConnectionStateDisconnected {
			go b.lose(c, "the connection to the browser dropped")
		}
	})
	answer, err := pc.CreateAnswer(nil)
	if err != nil {
		return fail(fmt.Errorf("no answer for the offer: %w", err))
	}
	gathered := webrtc.GatheringCompletePromise(pc)
	if err := pc.SetLocalDescription(answer); err != nil {
		return fail(err)
	}
	select {
	case <-gathered:
	case <-ctx.Done():
		return fail(ctx.Err())
	case <-time.After(gatherWait):
		return fail(errors.New("no network address for audio"))
	}
	if err := b.add(c); err != nil {
		return fail(err)
	}
	go b.watch(c)
	// Pion keeps the answer it made; the browser gets it with the Opus parameters asked for
	return askOpus(pc.LocalDescription().SDP), nil
}

// add registers c, replacing the tab's earlier connection, and starts capture for the first listener.
func (b *Bridge) add(c *conn) error {
	b.mu.Lock()
	if b.shut {
		b.mu.Unlock()
		return errors.New("the station is stopping")
	}
	select {
	case <-c.done:
		b.mu.Unlock()
		return errors.New("the connection to the browser dropped")
	default:
	}
	old := b.conns[c.tab]
	b.conns[c.tab] = c
	c.lastRTP.Store(b.clock().UnixNano())
	var err error
	if b.cap == nil {
		err = b.startCaptureLocked()
	}
	if err != nil {
		delete(b.conns, c.tab)
	}
	b.mu.Unlock()
	if old != nil {
		old.close()
	}
	return err
}

func (b *Bridge) startCaptureLocked() error {
	c := b.tmpl
	if b.used {
		c = NewCapture(b.tmpl.server, b.tmpl.source)
		c.Bins, c.Env = b.tmpl.Bins, b.tmpl.Env
	}
	b.used = true
	if err := c.Start(b.ctx); err != nil {
		return err
	}
	b.cap = c
	go b.pump(c)
	return nil
}

// stopCaptureLocked takes the capture away when nobody listens; the caller closes it unlocked.
func (b *Bridge) stopCaptureLocked() *Capture {
	if len(b.conns) > 0 || b.cap == nil {
		return nil
	}
	c := b.cap
	b.cap = nil
	return c
}

// pump encodes each captured frame once and sends it on every listener's track.
func (b *Bridge) pump(c *Capture) {
	enc, _ := b.encoder()
	var tracks []*webrtc.TrackLocalStaticSample
	for f := range c.Frames() {
		tracks = tracks[:0]
		b.mu.Lock()
		if b.cap == c {
			for _, cn := range b.conns {
				tracks = append(tracks, cn.track)
			}
		}
		b.mu.Unlock()
		if len(tracks) == 0 {
			continue
		}
		pkt, err := enc.Encode(f)
		if err != nil {
			b.log.Warn("encoding the radio's audio", "error", err)
			continue
		}
		for _, t := range tracks {
			_ = t.WriteSample(media.Sample{Data: pkt, Duration: frameTime})
		}
	}
	err := c.Err()
	b.mu.Lock()
	current := b.cap == c
	var victims []*conn
	if current {
		b.cap = nil
		if err != nil {
			for _, cn := range b.conns {
				victims = append(victims, cn)
			}
		}
	}
	b.mu.Unlock()
	if current && err != nil {
		b.log.Warn("receive audio ended", "error", err)
		for _, cn := range victims {
			b.lose(cn, err.Error())
		}
	}
}

// receive reads the browser's microphone; it reaches the radio only while the tab may transmit.
func (b *Bridge) receive(c *conn, tr *webrtc.TrackRemote) {
	dec, err := NewDecoder()
	if err != nil {
		b.log.Warn("no decoder for the browser's audio", "error", err)
		return
	}
	var last uint16
	have := false
	for {
		pkt, _, err := tr.ReadRTP()
		if err != nil {
			return
		}
		c.lastRTP.Store(b.clock().UnixNano())
		c.inbound.Store(true)
		if !b.TXAllowed(c.tab) {
			have = false
			continue
		}
		if have {
			d := int16(pkt.SequenceNumber - last)
			if d <= 0 {
				continue // late or repeated
			}
			if gap := int(d) - 1; gap > 0 && gap <= maxGap {
				for i := 0; i < gap-1; i++ {
					if pcm, err := dec.DecodeLost(); err == nil {
						b.writeTX(c.tab, pcm)
					}
				}
				if pcm, err := dec.DecodeFEC(pkt.Payload); err == nil {
					b.writeTX(c.tab, pcm)
				}
			}
		}
		last, have = pkt.SequenceNumber, true
		if len(pkt.Payload) == 0 {
			continue
		}
		pcm, err := dec.Decode(pkt.Payload)
		if err != nil {
			continue
		}
		b.writeTX(c.tab, pcm)
	}
}

// writeTX sends one frame of tab's microphone to the radio, if tab may still transmit. pacat
// failing is restarted once per transmission; failing again drops the transmitting tabs (their
// OnLost tells the PTT layer).
func (b *Bridge) writeTX(tab TabID, pcm []int16) {
	b.mu.Lock()
	_, allowed := b.allowed[tab]
	on, gen := b.playOn && allowed, b.playGen
	b.mu.Unlock()
	if !on {
		return
	}
	err := b.play.Write(pcm)
	if err == nil {
		return
	}
	b.txMu.Lock()
	b.mu.Lock()
	if !b.playOn || b.playGen != gen { // revoked or already restarted meanwhile: not a death
		b.mu.Unlock()
		b.txMu.Unlock()
		return
	}
	if !b.retried {
		b.retried = true
		b.playGen++
		b.mu.Unlock()
		b.log.Warn("transmit audio stopped; restarting it once", "error", err)
		_ = b.play.Close()
		if err := b.play.Start(b.ctx); err != nil {
			b.log.Warn("transmit audio did not restart", "error", err)
		}
		b.txMu.Unlock()
		return
	}
	b.playGen++ // one loss per death
	var talkers []*conn
	for t := range b.allowed {
		if c := b.conns[t]; c != nil {
			talkers = append(talkers, c)
		}
	}
	b.mu.Unlock()
	b.txMu.Unlock()
	b.log.Warn("transmit audio stopped again", "error", err)
	for _, c := range talkers {
		b.lose(c, "the radio's transmit audio stopped")
	}
}

// watch closes a transmitting connection that has gone LostAfter without RTP.
func (b *Bridge) watch(c *conn) {
	every := b.LostAfter / 10
	if every < 20*time.Millisecond {
		every = 20 * time.Millisecond
	}
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-c.done:
			return
		case <-t.C:
		}
		b.mu.Lock()
		since, ok := b.allowed[c.tab]
		b.mu.Unlock()
		up := c.upAt.Load()
		if !ok || up == 0 { // not transmitting, or the link is not up yet (a failed one is lost by its state)
			continue
		}
		last := time.Unix(0, max(c.lastRTP.Load(), up))
		if last.Before(since) {
			last = since
		}
		if b.clock().Sub(last) > b.LostAfter {
			b.lose(c, "no audio from the browser")
			return
		}
	}
}

// lose tears c down and tells the OnLost subscribers when c was the tab's connection.
func (b *Bridge) lose(c *conn, reason string) {
	if !c.close() {
		return
	}
	b.mu.Lock()
	registered := b.conns[c.tab] == c
	if registered {
		delete(b.conns, c.tab)
	}
	stop := b.stopCaptureLocked()
	fns := make([]func(TabID, string), 0, len(b.lostFns))
	for _, fn := range b.lostFns {
		fns = append(fns, fn)
	}
	b.mu.Unlock()
	if stop != nil {
		_ = stop.Close()
	}
	if registered {
		b.log.Info("browser audio lost", "device", deviceLabel(c.tab), "reason", reason)
		for _, fn := range fns {
			fn(c.tab, reason)
		}
	}
}

// OnLost calls fn(tab, reason) when a tab's connection is torn down for lost audio or a dropped
// link (not for Close). The returned func unsubscribes.
func (b *Bridge) OnLost(fn func(tab TabID, reason string)) (cancel func()) {
	b.mu.Lock()
	id := b.nextFn
	b.nextFn++
	b.lostFns[id] = fn
	b.mu.Unlock()
	return func() {
		b.mu.Lock()
		delete(b.lostFns, id)
		b.mu.Unlock()
	}
}

// AddICECandidate adds a candidate the browser trickled; an empty one (end of candidates) is a no-op.
func (b *Bridge) AddICECandidate(tab TabID, cand Candidate) error {
	if cand.Candidate == "" {
		return nil
	}
	b.mu.Lock()
	c := b.conns[tab]
	b.mu.Unlock()
	if c == nil {
		return errors.New("no audio connection for this tab")
	}
	return c.pc.AddICECandidate(webrtc.ICECandidateInit{Candidate: cand.Candidate, SDPMid: cand.SDPMid,
		SDPMLineIndex: cand.SDPMLineIndex, UsernameFragment: cand.UsernameFragment})
}

// Close ends tab's connection, if it has one.
func (b *Bridge) Close(tab TabID) {
	b.mu.Lock()
	c := b.conns[tab]
	delete(b.conns, tab)
	stop := b.stopCaptureLocked()
	b.mu.Unlock()
	if c != nil {
		c.close()
	}
	if stop != nil {
		_ = stop.Close()
	}
}

// Listeners is the number of connected tabs.
func (b *Bridge) Listeners() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.conns)
}

func (b *Bridge) capturing() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.cap != nil
}

// TXAllowed reports whether tab's microphone goes to the radio.
func (b *Bridge) TXAllowed(tab TabID) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	_, ok := b.allowed[tab]
	return ok
}

// HasInbound reports whether tab's connection carries the browser's microphone yet: at least one
// RTP packet has come in on it. The PTT layer refuses to key before then, so the no-audio timer
// that starts with TX never runs out during the browser's first microphone prompt.
func (b *Bridge) HasInbound(tab TabID) bool {
	b.mu.Lock()
	c := b.conns[tab]
	b.mu.Unlock()
	return c != nil && c.inbound.Load()
}

// SetTXAllowed lets tab's microphone through to the radio, or stops it (the PTT layer's switch).
// The transmit pipe runs while any tab is allowed.
func (b *Bridge) SetTXAllowed(tab TabID, on bool) {
	b.txMu.Lock()
	defer b.txMu.Unlock()
	b.mu.Lock()
	if on {
		if _, ok := b.allowed[tab]; !ok {
			b.allowed[tab] = b.clock()
		}
	} else {
		delete(b.allowed, tab)
	}
	want := len(b.allowed) > 0 && !b.shut
	start, stop := want && !b.playOn, !want && b.playOn
	b.playOn = want
	if start {
		b.retried = false
		b.playGen++
	}
	b.mu.Unlock()
	if start {
		if err := b.play.Start(b.ctx); err != nil {
			b.log.Warn("transmit audio did not start", "error", err)
		}
	}
	if stop {
		_ = b.play.Close()
	}
}

// Shutdown ends every connection, the capture and the transmit pipe.
func (b *Bridge) Shutdown() {
	b.txMu.Lock()
	b.mu.Lock()
	b.shut = true
	conns := make([]*conn, 0, len(b.conns))
	for _, c := range b.conns {
		conns = append(conns, c)
	}
	b.conns = map[TabID]*conn{}
	capt := b.cap
	b.cap = nil
	b.allowed = map[TabID]time.Time{}
	b.playOn = false
	b.mu.Unlock()
	for _, c := range conns {
		c.close()
	}
	if capt != nil {
		_ = capt.Close()
	}
	_ = b.play.Close()
	b.txMu.Unlock()
	b.cancel()
}

// Bridges holds one Bridge per radio, made on first use from the radio's sound server.
type Bridges struct {
	server *ServerControl
	ports  PortRange
	log    *slog.Logger

	mu sync.Mutex
	m  map[string]*Bridge
}

// NewBridges makes the registry; each radio's capture and playback use server's socket and helpers.
func NewBridges(server *ServerControl, ports PortRange, log *slog.Logger) *Bridges {
	return &Bridges{server: server, ports: ports, log: log, m: map[string]*Bridge{}}
}

// Get is radio's bridge.
func (bs *Bridges) Get(radio string) *Bridge {
	bs.mu.Lock()
	defer bs.mu.Unlock()
	if b, ok := bs.m[radio]; ok {
		return b
	}
	srv, card := bs.server.Server(radio), Card(radio)
	c := NewCapture(srv, card+".in")
	c.Bins, c.Env = bs.server.Bins, bs.server.Env
	p := NewPlayback(srv, card+".tx")
	p.Bins, p.Env = bs.server.Bins, bs.server.Env
	b := NewBridge(radio, c, p, bs.ports, bs.log)
	bs.m[radio] = b
	return b
}

// Shutdown ends every bridge.
func (bs *Bridges) Shutdown() {
	bs.mu.Lock()
	all := make([]*Bridge, 0, len(bs.m))
	for _, b := range bs.m {
		all = append(all, b)
	}
	bs.mu.Unlock()
	for _, b := range all {
		b.Shutdown()
	}
}

func (b *Bridge) clock() time.Time {
	if b.now != nil {
		return b.now()
	}
	return time.Now()
}

// deviceLabel is the device name of a tab id ("<session>|<device>#<nonce>") for the log: the
// session part is the login cookie and is never logged.
func deviceLabel(tab TabID) string {
	if i := strings.IndexByte(tab, '|'); i >= 0 {
		tab = tab[i+1:]
	} else {
		return "unknown"
	}
	return session.TabLabel(tab)
}

// Remove ends radio's bridge and every connection on it (the radio left the records); a later
// Get makes a new one.
func (bs *Bridges) Remove(radio string) {
	bs.mu.Lock()
	b := bs.m[radio]
	delete(bs.m, radio)
	bs.mu.Unlock()
	if b != nil {
		b.Shutdown()
	}
}
