package audio

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// FrameSamples is one 20 ms frame at 48 kHz mono.
const FrameSamples = 960

// Capture reads a radio's receive audio from parec as 20 ms frames.
type Capture struct {
	server, source string

	// Bins maps parec to an absolute path (tests point it at a fake).
	Bins map[string]string
	// Env is added to the fixed child environment.
	Env []string

	frames chan []int16
	mu     sync.Mutex
	err    error
	cancel context.CancelFunc
	done   chan struct{}
}

// NewCapture captures source (<CARD>.in) from the sound server at server (unix:/path).
func NewCapture(server, source string) *Capture {
	return &Capture{server: server, source: source, frames: make(chan []int16, 50)}
}

// Frames yields frames of FrameSamples samples; it is closed when capture ends (see Err).
func (c *Capture) Frames() <-chan []int16 { return c.frames }

// Err is why capture ended (nil while it runs or after a Close).
func (c *Capture) Err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.err
}

// Start launches parec; it is restarted once if it exits, then Err reports the end.
func (c *Capture) Start(ctx context.Context) error {
	c.mu.Lock()
	if c.cancel != nil {
		c.mu.Unlock()
		return errors.New("capture already started")
	}
	ctx, c.cancel = context.WithCancel(ctx)
	c.done = make(chan struct{})
	c.mu.Unlock()
	go c.loop(ctx)
	return nil
}

// Close stops parec and waits for the reader to finish.
func (c *Capture) Close() error {
	c.mu.Lock()
	cancel, done := c.cancel, c.done
	c.mu.Unlock()
	if cancel == nil {
		return nil
	}
	cancel()
	<-done
	return nil
}

func (c *Capture) setErr(err error) {
	c.mu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.mu.Unlock()
}

func (c *Capture) loop(ctx context.Context) {
	defer close(c.done)
	defer close(c.frames)
	var last error
	for attempt := 0; attempt < 2; attempt++ {
		last = c.runOnce(ctx)
		if ctx.Err() != nil {
			return
		}
	}
	c.setErr(fmt.Errorf("no audio from the radio's sound card (%s): %w", c.source, last))
}

func (c *Capture) runOnce(ctx context.Context) error {
	cmd := exec.CommandContext(ctx, Bin(c.Bins, "parec"),
		"--server="+c.server, "--device="+c.source, "--format=s16le", "--rate=48000", "--channels=1", "--latency-msec=20")
	cmd.Env = append(append([]string(nil), childEnv...), c.Env...)
	cmd.WaitDelay = time.Second
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	stop := make(chan struct{})
	defer close(stop)
	go func() { // a cancelled context must unblock the read even if something else holds the pipe
		select {
		case <-ctx.Done():
			_ = out.Close()
		case <-stop:
		}
	}()
	buf := make([]byte, FrameSamples*2)
	var rerr error
	for {
		if _, rerr = io.ReadFull(out, buf); rerr != nil {
			break
		}
		f := make([]int16, FrameSamples)
		for i := range f {
			f[i] = int16(binary.LittleEndian.Uint16(buf[2*i:]))
		}
		select {
		case c.frames <- f:
		case <-ctx.Done():
			_ = cmd.Wait()
			return ctx.Err()
		}
	}
	_ = cmd.Process.Kill()
	_ = cmd.Wait()
	return fmt.Errorf("parec ended: %v", rerr)
}

// Playback writes transmit audio to pacat.
type Playback struct {
	server, sink string

	// Bins maps pacat to an absolute path (tests point it at a fake).
	Bins map[string]string
	// Env is added to the fixed child environment.
	Env []string

	mu    sync.Mutex
	cmd   *exec.Cmd
	stdin io.WriteCloser
	done  chan struct{}
}

// NewPlayback plays into sink (<CARD>.tx) on the sound server at server (unix:/path).
func NewPlayback(server, sink string) *Playback {
	return &Playback{server: server, sink: sink}
}

// Start launches pacat; the process dies with ctx.
func (p *Playback) Start(ctx context.Context) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cmd != nil {
		return errors.New("playback already started")
	}
	cmd := exec.CommandContext(ctx, Bin(p.Bins, "pacat"), "--playback",
		"--server="+p.server, "--device="+p.sink, "--format=s16le", "--rate=48000", "--channels=1", "--latency-msec=20")
	cmd.Env = append(append([]string(nil), childEnv...), p.Env...)
	cmd.WaitDelay = time.Second
	in, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return err
	}
	p.cmd, p.stdin, p.done = cmd, in, make(chan struct{})
	done := p.done
	go func() {
		_ = cmd.Wait()
		p.mu.Lock()
		if p.cmd == cmd { // pacat died by itself: the caller may Start again
			p.cmd, p.stdin = nil, nil
		}
		p.mu.Unlock()
		close(done)
	}()
	return nil
}

// Write sends one frame as s16le.
func (p *Playback) Write(frame []int16) error {
	p.mu.Lock()
	in := p.stdin
	p.mu.Unlock()
	if in == nil {
		return errors.New("playback not running")
	}
	b := make([]byte, 2*len(frame))
	for i, s := range frame {
		binary.LittleEndian.PutUint16(b[2*i:], uint16(s))
	}
	_, err := in.Write(b)
	return err
}

// Close stops pacat and waits for it.
func (p *Playback) Close() error {
	p.mu.Lock()
	cmd, in, done := p.cmd, p.stdin, p.done
	p.stdin, p.cmd = nil, nil
	p.mu.Unlock()
	if cmd == nil {
		return nil
	}
	if in != nil {
		_ = in.Close() // let pacat drain what it has
	}
	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		_ = cmd.Process.Kill()
		<-done
	}
	return nil
}
