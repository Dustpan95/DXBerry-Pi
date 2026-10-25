package rig

import (
	"bufio"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// fakeServer accepts connections and hands each command line to handler,
// which writes the reply to the connection.
type fakeServer struct {
	ln       net.Listener
	mu       sync.Mutex
	conns    int
	cmds     []string
	handler  func(n int, line string, w net.Conn) bool // false = close conn
	inFlight int
	maxSeen  int
	// before, if set, runs after a line is read and before the handler.
	before func(r *bufio.Reader, c net.Conn)
}

func newFake(t *testing.T, h func(n int, line string, w net.Conn) bool) *fakeServer {
	return newFakeAt(t, "127.0.0.1:0", h)
}

func newFakeAt(t *testing.T, addr string, h func(n int, line string, w net.Conn) bool) *fakeServer {
	t.Helper()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	f := &fakeServer{ln: ln, handler: h}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			f.mu.Lock()
			f.conns++
			n := f.conns
			f.mu.Unlock()
			go func() {
				defer c.Close()
				r := bufio.NewReader(c)
				for {
					line, err := r.ReadString('\n')
					if err != nil {
						return
					}
					line = strings.TrimRight(line, "\n")
					f.mu.Lock()
					f.cmds = append(f.cmds, line)
					f.inFlight++
					if f.inFlight > f.maxSeen {
						f.maxSeen = f.inFlight
					}
					f.mu.Unlock()
					if f.before != nil {
						f.before(r, c)
					}
					ok := f.handler(n, line, c)
					f.mu.Lock()
					f.inFlight--
					f.mu.Unlock()
					if !ok {
						return
					}
				}
			}()
		}
	}()
	return f
}

func (f *fakeServer) addr() string { return f.ln.Addr().String() }

func (f *fakeServer) connCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.conns
}

func fastClient(t *testing.T, addr string) *Client {
	t.Helper()
	c := dial(addr, nil, options{
		cmdTimeout: 300 * time.Millisecond,
		sleep:      func(ctx context.Context, d time.Duration) error { return sleepCtx(ctx, 10*time.Millisecond) },
	})
	t.Cleanup(c.Close)
	return c
}

func waitConnected(t *testing.T, c *Client) {
	t.Helper()
	for i := 0; i < 400; i++ {
		if c.Connected() {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatal("client never connected")
}

func ctx5(t *testing.T) context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	t.Cleanup(cancel)
	return ctx
}

func freqHandler(n int, line string, w net.Conn) bool {
	if line == `+\get_freq` {
		w.Write([]byte("get_freq:\nFrequency: 14074000\nRPRT 0\n"))
		return true
	}
	w.Write([]byte("x:\nRPRT -1\n"))
	return true
}

func TestClientGetFreqExtended(t *testing.T) {
	f := newFake(t, freqHandler)
	c := fastClient(t, f.addr())
	waitConnected(t, c)
	r, err := c.Do(ctx5(t), "get_freq")
	if err != nil {
		t.Fatal(err)
	}
	if r.RPRT != 0 || len(r.Lines) != 1 || r.Lines[0] != "Frequency: 14074000" {
		t.Fatalf("reply = %+v", r)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cmds[0] != `+\get_freq` {
		t.Fatalf("sent %q", f.cmds[0])
	}
}

func TestClientSendsArgs(t *testing.T) {
	f := newFake(t, func(n int, line string, w net.Conn) bool {
		w.Write([]byte("set_freq: 14074000\nRPRT 0\n"))
		return true
	})
	c := fastClient(t, f.addr())
	waitConnected(t, c)
	if _, err := c.Do(ctx5(t), "set_freq", "14074000"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.cmds[0] != `+\set_freq 14074000` {
		t.Fatalf("sent %q", f.cmds[0])
	}
}

func TestClientRejectsInjection(t *testing.T) {
	f := newFake(t, freqHandler)
	c := fastClient(t, f.addr())
	waitConnected(t, c)
	if _, err := c.Do(ctx5(t), "set_freq", "1\n+\\set_ptt 1"); err == nil {
		t.Fatal("newline in argument accepted")
	}
	if _, err := c.Do(ctx5(t), "get_freq\n"); err == nil {
		t.Fatal("newline in command accepted")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.cmds) != 0 {
		t.Fatalf("sent %v", f.cmds)
	}
}

func TestClientSerializesCommands(t *testing.T) {
	var pipelined sync.Map
	f := newFake(t, func(n int, line string, w net.Conn) bool {
		w.Write([]byte("get_freq:\nFrequency: 1\nRPRT 0\n"))
		return true
	})
	// While the server holds a command, a pipelining client would already
	// have written the next one: look for bytes before replying.
	f.before = func(r *bufio.Reader, c net.Conn) {
		time.Sleep(40 * time.Millisecond)
		c.SetReadDeadline(time.Now().Add(20 * time.Millisecond))
		if _, err := r.Peek(1); err == nil {
			pipelined.Store("yes", true)
		}
		c.SetReadDeadline(time.Time{})
	}
	c := fastClient(t, f.addr())
	waitConnected(t, c)
	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.Do(ctx5(t), "get_freq"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if _, bad := pipelined.Load("yes"); bad {
		t.Fatal("client sent a command before the previous reply arrived")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.cmds) != 4 {
		t.Fatalf("cmds=%d", len(f.cmds))
	}
}

func TestClientRigErrorCode(t *testing.T) {
	f := newFake(t, func(n int, line string, w net.Conn) bool {
		w.Write([]byte("set_freq: 5\nRPRT -11\n"))
		return true
	})
	c := fastClient(t, f.addr())
	waitConnected(t, c)
	r, err := c.Do(ctx5(t), "set_freq", "5")
	var re *RigError
	if !errors.As(err, &re) || re.Code != -11 {
		t.Fatalf("err = %v", err)
	}
	if r.RPRT != -11 {
		t.Fatalf("reply RPRT = %d", r.RPRT)
	}
	if !c.Connected() {
		t.Fatal("a rig error must not drop the connection")
	}
}

func TestClientDeadlineResetsConnection(t *testing.T) {
	f := newFake(t, func(n int, line string, w net.Conn) bool {
		if n == 1 {
			time.Sleep(time.Second) // stall past the deadline
			return false
		}
		return freqHandler(n, line, w)
	})
	c := fastClient(t, f.addr())
	waitConnected(t, c)
	if _, err := c.Do(ctx5(t), "get_freq"); !errors.Is(err, ErrDeadline) {
		t.Fatalf("err = %v, want ErrDeadline", err)
	}
	var r Reply
	var err error
	for i := 0; i < 200; i++ {
		r, err = c.Do(ctx5(t), "get_freq")
		if err == nil {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err != nil || r.Lines[0] != "Frequency: 14074000" {
		t.Fatalf("after reset: %v %+v", err, r)
	}
	if f.connCount() != 2 {
		t.Fatalf("connections = %d, want 2", f.connCount())
	}
}

func TestClientMalformedReplyReconnects(t *testing.T) {
	cases := map[string]string{
		"echo mismatch": "set_mode:\nFrequency: 1\nRPRT 0\n",
	}
	for name, bad := range cases {
		t.Run(name, func(t *testing.T) {
			f := newFake(t, func(n int, line string, w net.Conn) bool {
				if n == 1 {
					w.Write([]byte(bad))
					return true
				}
				return freqHandler(n, line, w)
			})
			c := fastClient(t, f.addr())
			waitConnected(t, c)
			if _, err := c.Do(ctx5(t), "get_freq"); !errors.Is(err, ErrProtocol) {
				t.Fatalf("err = %v, want ErrProtocol", err)
			}
			var err error
			for i := 0; i < 200; i++ {
				if _, err = c.Do(ctx5(t), "get_freq"); err == nil {
					break
				}
				time.Sleep(10 * time.Millisecond)
			}
			if err != nil {
				t.Fatalf("after reset: %v", err)
			}
			if f.connCount() != 2 {
				t.Fatalf("connections = %d, want 2", f.connCount())
			}
		})
	}
}

func TestClientQueuedCancel(t *testing.T) {
	release := make(chan struct{})
	f := newFake(t, func(n int, line string, w net.Conn) bool {
		<-release
		w.Write([]byte("get_freq:\nFrequency: 1\nRPRT 0\n"))
		return true
	})
	c := dial(f.addr(), nil, options{cmdTimeout: 3 * time.Second})
	defer c.Close()
	waitConnected(t, c)
	done := make(chan error, 1)
	go func() { _, err := c.Do(ctx5(t), "get_freq"); done <- err }()
	time.Sleep(50 * time.Millisecond)
	ctx, cancel := context.WithCancel(context.Background())
	res := make(chan error, 1)
	go func() { _, err := c.Do(ctx, "get_freq"); res <- err }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-res; !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.cmds) != 1 {
		t.Fatalf("cancelled command was sent: %v", f.cmds)
	}
}

func TestClientReconnectBackoff(t *testing.T) {
	// A port nothing listens on.
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()

	var mu sync.Mutex
	var got []time.Duration
	enough := make(chan struct{})
	c := dial(addr, nil, options{
		sleep: func(ctx context.Context, d time.Duration) error {
			mu.Lock()
			got = append(got, d)
			n := len(got)
			mu.Unlock()
			if n == 8 {
				close(enough)
			}
			if n >= 8 {
				<-ctx.Done()
				return ctx.Err()
			}
			return nil
		},
	})
	defer c.Close()
	select {
	case <-enough:
	case <-time.After(5 * time.Second):
		t.Fatal("backoff sleeper not called enough times")
	}
	want := []time.Duration{1, 2, 4, 8, 16, 30, 30, 30}
	mu.Lock()
	defer mu.Unlock()
	for i, w := range want {
		if got[i] != w*time.Second {
			t.Fatalf("sleep[%d] = %v, want %v (all %v)", i, got[i], w*time.Second, got)
		}
	}
	if c.Connected() {
		t.Fatal("connected to nothing")
	}
	if _, err := c.Do(context.Background(), "get_freq"); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("err = %v", err)
	}
}

func TestClientBackoffResetsAfterConnect(t *testing.T) {
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	addr := ln.Addr().String()
	ln.Close()
	var f *fakeServer
	var mu sync.Mutex
	var got []time.Duration
	c := dial(addr, nil, options{
		cmdTimeout: 300 * time.Millisecond,
		sleep: func(ctx context.Context, d time.Duration) error {
			mu.Lock()
			got = append(got, d)
			if d == 4*time.Second && f == nil {
				// third failed dial: bring the server up before the retry
				f = newFakeAt(t, addr, func(n int, line string, w net.Conn) bool {
					if n == 1 {
						return false // hang up on the first command
					}
					return freqHandler(n, line, w)
				})
			}
			mu.Unlock()
			return nil
		},
	})
	defer c.Close()
	waitConnected(t, c)
	c.Do(ctx5(t), "get_freq") // server hangs up: connection resets
	for i := 0; i < 400; i++ {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 4 {
			break
		}
		time.Sleep(5 * time.Millisecond)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []time.Duration{time.Second, 2 * time.Second, 4 * time.Second, time.Second}
	if len(got) < 4 {
		t.Fatalf("sleeps = %v", got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("sleeps = %v, want prefix %v", got, want)
		}
	}
}

func TestClientNoStaleCommandAfterReconnect(t *testing.T) {
	f := newFake(t, func(n int, line string, w net.Conn) bool {
		if n == 1 {
			return false // hang up on the first command
		}
		return freqHandler(n, line, w)
	})
	parked := make(chan struct{})
	enqueue := make(chan struct{})
	gate := make(chan struct{})
	c := dial(f.addr(), nil, options{
		cmdTimeout: 300 * time.Millisecond,
		sleep: func(ctx context.Context, d time.Duration) error {
			select { // hold the backoff until released
			case <-gate:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
		beforeEnqueue: func(cmd string) {
			if cmd == "set_freq" {
				close(parked)
				<-enqueue
			}
		},
	})
	defer c.Close()
	waitConnected(t, c)

	stale := make(chan error, 1)
	go func() { _, err := c.Do(ctx5(t), "set_freq", "7074000"); stale <- err }()
	<-parked // passed the connected check, not yet queued

	if _, err := c.Do(ctx5(t), "get_freq"); err == nil { // server hangs up
		t.Fatal("expected the dropped connection to fail the command")
	}
	for i := 0; i < 400 && c.Connected(); i++ {
		time.Sleep(5 * time.Millisecond)
	}
	close(enqueue) // the stale command now reaches the queue, mid-backoff
	select {
	case err := <-stale:
		if !errors.Is(err, ErrNotConnected) {
			t.Fatalf("stale err = %v, want ErrNotConnected", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("stale caller still waiting on the backoff")
	}
	close(gate) // let the client reconnect
	waitConnected(t, c)
	if _, err := c.Do(ctx5(t), "get_freq"); err != nil {
		t.Fatal(err)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, l := range f.cmds {
		if strings.Contains(l, "set_freq") {
			t.Fatalf("stale command reached the rig: %v", f.cmds)
		}
	}
}

func TestClientValueLineStartingRPRT(t *testing.T) {
	f := newFake(t, func(n int, line string, w net.Conn) bool {
		w.Write([]byte("get_info:\nRPRT zero is a value\nRPRT 0\n"))
		return true
	})
	c := fastClient(t, f.addr())
	waitConnected(t, c)
	r, err := c.Do(ctx5(t), "get_info")
	if err != nil || len(r.Lines) != 1 || r.Lines[0] != "RPRT zero is a value" {
		t.Fatalf("%+v %v", r, err)
	}
	if f.connCount() != 1 {
		t.Fatal("connection was reset")
	}
}

func TestClientPartialReplyIsBothErrors(t *testing.T) {
	f := newFake(t, func(n int, line string, w net.Conn) bool {
		if n == 1 {
			w.Write([]byte("get_freq:\nFrequency: 1\n")) // never finishes
			time.Sleep(time.Second)
			return false
		}
		return freqHandler(n, line, w)
	})
	c := fastClient(t, f.addr())
	waitConnected(t, c)
	_, err := c.Do(ctx5(t), "get_freq")
	if !errors.Is(err, ErrProtocol) || !errors.Is(err, ErrDeadline) {
		t.Fatalf("err = %v", err)
	}
}

func TestClientLongLineIsProtocolError(t *testing.T) {
	f := newFake(t, func(n int, line string, w net.Conn) bool {
		if n == 1 {
			w.Write([]byte("get_freq:\n" + strings.Repeat("x", 100<<10) + "\n"))
			return true
		}
		return freqHandler(n, line, w)
	})
	c := fastClient(t, f.addr())
	waitConnected(t, c)
	if _, err := c.Do(ctx5(t), "get_freq"); !errors.Is(err, ErrProtocol) {
		t.Fatalf("err = %v", err)
	}
}

func TestClientCloseIsPromptAndReportsDown(t *testing.T) {
	f := newFake(t, func(n int, line string, w net.Conn) bool {
		time.Sleep(3 * time.Second) // stall
		return false
	})
	states := make(chan bool, 4)
	c := dial(f.addr(), nil, options{cmdTimeout: 10 * time.Second, hold: true})
	c.OnState(func(b bool) { states <- b })
	c.release()
	waitConnected(t, c)
	go c.Do(context.Background(), "get_freq")
	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	c.Close()
	if d := time.Since(start); d > time.Second {
		t.Fatalf("Close took %v", d)
	}
	if !<-states {
		t.Fatal("want up first")
	}
	select {
	case v := <-states:
		if v {
			t.Fatal("want down")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no final down event")
	}
}

func TestClientOnState(t *testing.T) {
	f := newFake(t, func(n int, line string, w net.Conn) bool { return false })
	states := make(chan bool, 16)
	c := dial(f.addr(), nil, options{
		cmdTimeout: 300 * time.Millisecond,
		sleep:      func(ctx context.Context, d time.Duration) error { return sleepCtx(ctx, 10*time.Millisecond) },
		hold:       true,
	})
	defer c.Close()
	c.OnState(func(b bool) { time.Sleep(20 * time.Millisecond); states <- b }) // slow callback must not block the client
	c.release()
	if v := <-states; !v {
		t.Fatal("first state should be connected")
	}
	c.Do(ctx5(t), "get_freq") // server closes: reset
	if v := <-states; v {
		t.Fatal("second state should be disconnected")
	}
	if v := <-states; !v {
		t.Fatal("third state should be connected again")
	}
}

func TestIntegrationRigctld(t *testing.T) {
	path, err := exec.LookPath("rigctld")
	if err != nil {
		t.Skip("rigctld not on PATH")
	}
	ln, _ := net.Listen("tcp", "127.0.0.1:0")
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	cmd := exec.Command(path, "-m", "1", "-t", strconv.Itoa(port))
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill(); cmd.Wait() })

	c := dial("127.0.0.1:"+strconv.Itoa(port), nil, options{
		sleep: func(ctx context.Context, d time.Duration) error { return sleepCtx(ctx, 100*time.Millisecond) },
	})
	defer c.Close()
	for i := 0; i < 100 && !c.Connected(); i++ {
		time.Sleep(100 * time.Millisecond)
	}
	if !c.Connected() {
		t.Fatal("never connected to rigctld")
	}
	ctx := ctx5(t)
	if _, err := c.Do(ctx, "get_freq"); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Do(ctx, "set_freq", "14074000"); err != nil {
		t.Fatal(err)
	}
	r, err := c.Do(ctx, "get_freq")
	if err != nil || len(r.Lines) != 1 || r.Lines[0] != "Frequency: 14074000" {
		t.Fatalf("get_freq = %+v, %v", r, err)
	}
	r, err = c.Do(ctx, "dump_caps")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, l := range r.Lines {
		if strings.HasPrefix(l, "Model name:") {
			found = true
		}
	}
	if !found {
		t.Fatalf("no Model name: line in %d lines", len(r.Lines))
	}
}

func TestTranscriptParses(t *testing.T) {
	b, err := os.ReadFile("testdata/transcript-dummy.txt")
	if err != nil {
		t.Fatal(err)
	}
	// Every non-dump section must end with an RPRT line the parser accepts.
	for _, l := range strings.Split(string(b), "\n") {
		if strings.HasPrefix(l, "RPRT") {
			if _, ok := parseRPRT(l); !ok {
				t.Fatalf("parseRPRT rejects %q", l)
			}
		}
	}
	if _, ok := parseRPRT("RPRT zero"); ok {
		t.Fatal("accepted garbage")
	}
}
