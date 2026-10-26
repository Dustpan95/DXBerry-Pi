// Package audio carries the station's sound path: the radio's sound server, the capture and
// playback pipes, Opus, and the WebRTC bridge to the browser.
package audio

import (
	"fmt"
	"sync"

	"github.com/hraban/opus"
)

// The codec's fixed parameters: 48 kHz mono, 20 ms frames (FrameSamples), in-band FEC.
const (
	SampleRate = 48000
	// RXBitrate is the radio's audio to the browsers.
	RXBitrate = 32000
	// TXBitrate is the browser's microphone to the radio (asked of the browser in the answer).
	TXBitrate = 24000

	maxPacket   = 1500
	maxFrame    = 5760 // 120 ms, the longest Opus frame
	expectedFEC = 5    // percent loss the encoder plans its FEC for
)

// Encoder is an Opus encoder for one radio's receive audio, safe for concurrent use.
type Encoder struct {
	mu  sync.Mutex
	enc *opus.Encoder
	buf []byte
}

// NewEncoder makes a 48 kHz mono encoder at RXBitrate with in-band FEC on; SetBitrate changes the rate.
func NewEncoder() (*Encoder, error) {
	enc, err := opus.NewEncoder(SampleRate, 1, opus.AppAudio)
	if err != nil {
		return nil, fmt.Errorf("opus encoder: %w", err)
	}
	for _, set := range []func() error{
		func() error { return enc.SetBitrate(RXBitrate) },
		func() error { return enc.SetInBandFEC(true) },
		func() error { return enc.SetPacketLossPerc(expectedFEC) },
	} {
		if err := set(); err != nil {
			return nil, fmt.Errorf("opus encoder: %w", err)
		}
	}
	return &Encoder{enc: enc, buf: make([]byte, maxPacket)}, nil
}

// SetBitrate sets the encoder's bitrate in bit/s.
func (e *Encoder) SetBitrate(bps int) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.enc.SetBitrate(bps)
}

// Encode turns one frame of FrameSamples samples into a packet (a new slice each call).
func (e *Encoder) Encode(pcm []int16) ([]byte, error) {
	if len(pcm) != FrameSamples {
		return nil, fmt.Errorf("opus: a frame is %d samples, not %d", FrameSamples, len(pcm))
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	n, err := e.enc.Encode(pcm, e.buf)
	if err != nil {
		return nil, fmt.Errorf("opus encode: %w", err)
	}
	return append([]byte(nil), e.buf[:n]...), nil
}

// Decoder is an Opus decoder for one transmitting browser. It is not safe for concurrent use.
type Decoder struct {
	dec *opus.Decoder
	buf []int16
}

// NewDecoder makes a 48 kHz mono decoder.
func NewDecoder() (*Decoder, error) {
	dec, err := opus.NewDecoder(SampleRate, 1)
	if err != nil {
		return nil, fmt.Errorf("opus decoder: %w", err)
	}
	return &Decoder{dec: dec, buf: make([]int16, maxFrame)}, nil
}

// Decode turns a packet into samples (a new slice each call).
func (d *Decoder) Decode(pkt []byte) ([]int16, error) {
	n, err := d.dec.Decode(pkt, d.buf)
	if err != nil {
		return nil, fmt.Errorf("opus decode: %w", err)
	}
	return append([]int16(nil), d.buf[:n]...), nil
}

// DecodeFEC recovers the frame lost just before next from next's forward error correction.
func (d *Decoder) DecodeFEC(next []byte) ([]int16, error) {
	pcm := make([]int16, FrameSamples)
	if err := d.dec.DecodeFEC(next, pcm); err != nil {
		return nil, fmt.Errorf("opus FEC: %w", err)
	}
	return pcm, nil
}

// DecodeLost conceals one lost 20 ms frame (packet loss concealment).
func (d *Decoder) DecodeLost() ([]int16, error) {
	pcm := make([]int16, FrameSamples)
	if err := d.dec.DecodePLC(pcm); err != nil {
		return nil, fmt.Errorf("opus PLC: %w", err)
	}
	return pcm, nil
}
