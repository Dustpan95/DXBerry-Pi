package audio

import (
	"math"
	"testing"
)

// toneFrame is frame n of a continuous sine of freq Hz at amplitude amp.
func toneFrame(n int, freq float64, amp float64) []int16 {
	f := make([]int16, FrameSamples)
	for i := range f {
		t := float64(n*FrameSamples+i) / SampleRate
		f[i] = int16(amp * math.Sin(2*math.Pi*freq*t))
	}
	return f
}

func rms(s []int16) float64 {
	if len(s) == 0 {
		return 0
	}
	var sum float64
	for _, v := range s {
		sum += float64(v) * float64(v)
	}
	return math.Sqrt(sum / float64(len(s)))
}

func TestOpusRoundTripSilenceAndTone(t *testing.T) {
	enc, err := NewEncoder()
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder()
	if err != nil {
		t.Fatal(err)
	}
	// silence stays silence
	for i := 0; i < 10; i++ {
		pkt, err := enc.Encode(make([]int16, FrameSamples))
		if err != nil {
			t.Fatal(err)
		}
		pcm, err := dec.Decode(pkt)
		if err != nil {
			t.Fatal(err)
		}
		if len(pcm) != FrameSamples {
			t.Fatalf("silence frame %d: %d samples", i, len(pcm))
		}
		if r := rms(pcm); r > 30 {
			t.Fatalf("silence frame %d decodes with RMS %.1f", i, r)
		}
	}
	// a 1 kHz tone keeps its level within 10% (the first frames carry the codec's delay)
	var in, out []int16
	for n := 0; n < 50; n++ {
		f := toneFrame(n, 1000, 8000)
		pkt, err := enc.Encode(f)
		if err != nil {
			t.Fatal(err)
		}
		if len(pkt) == 0 || len(pkt) > 200 {
			t.Fatalf("frame %d: packet of %d bytes at 32 kbit/s", n, len(pkt))
		}
		pcm, err := dec.Decode(pkt)
		if err != nil {
			t.Fatal(err)
		}
		in, out = append(in, f...), append(out, pcm...)
	}
	ri, ro := rms(in[10*FrameSamples:]), rms(out[10*FrameSamples:])
	if math.Abs(ro-ri)/ri > 0.10 {
		t.Fatalf("tone RMS in %.1f out %.1f", ri, ro)
	}
	// a lost packet is concealed with one frame, and FEC recovers one from the next packet
	pcm, err := dec.DecodeLost()
	if err != nil || len(pcm) != FrameSamples {
		t.Fatalf("DecodeLost: %d samples, %v", len(pcm), err)
	}
	next, err := enc.Encode(toneFrame(51, 1000, 8000))
	if err != nil {
		t.Fatal(err)
	}
	if pcm, err = dec.DecodeFEC(next); err != nil || len(pcm) != FrameSamples {
		t.Fatalf("DecodeFEC: %d samples, %v", len(pcm), err)
	}
	// the call site sets the bitrate; a frame of the wrong size is refused
	if err := enc.SetBitrate(TXBitrate); err != nil {
		t.Fatal(err)
	}
	if _, err := enc.Encode(make([]int16, 100)); err == nil {
		t.Fatal("a 100-sample frame was encoded")
	}
	if RXBitrate != 32000 || TXBitrate != 24000 || SampleRate != 48000 {
		t.Fatalf("rates %d %d %d", RXBitrate, TXBitrate, SampleRate)
	}
}
