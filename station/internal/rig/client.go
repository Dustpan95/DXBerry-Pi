// Package rig talks to Hamlib's rigctld. One Client owns one TCP connection to
// one rigctld, speaks the extended response protocol (+\cmd), runs one command
// at a time in FIFO order, and reconnects with backoff.
package rig

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultCmdTimeout = 2 * time.Second
	dialTimeout       = 2 * time.Second
	backoffStart      = time.Second
	backoffMax        = 30 * time.Second
	queueSize         = 256
)

var (
	// ErrDeadline means a command got no complete reply within its deadline.
	// The connection is closed and re-established.
	ErrDeadline = errors.New("rig: command deadline exceeded")
	// ErrProtocol means the reply was not a well-formed extended-protocol
	// reply. The connection is closed and re-established.
	ErrProtocol = errors.New("rig: malformed reply")
	// ErrNotConnected is returned while there is no connection to rigctld.
	ErrNotConnected = errors.New("rig: not connected")
	// ErrClosed is returned after Close.
	ErrClosed = errors.New("rig: client closed")
)

// RigError is a non-zero RPRT status from rigctld. The connection stays up.
type RigError struct{ Code int }

func (e *RigError) Error() string { return fmt.Sprintf("rig: rigctld returned RPRT %d", e.Code) }

// Reply is a command's reply without the echo line and without the RPRT line.
type Reply struct {
	Lines []string
	RPRT  int
}

var (
	rprtRe = regexp.MustCompile(`^RPRT -?\d+$`)
	nameRe = regexp.MustCompile(`^[A-Za-z0-9_]+$`)
)

func parseRPRT(line string) (int, bool) {
	if !rprtRe.MatchString(line) {
		return 0, false
	}
	n, err := strconv.Atoi(line[5:])
	return n, err == nil
}

// options carries the injectable pieces; tests replace them.
type options struct {
	cmdTimeout time.Duration
	sleep      func(ctx context.Context, d time.Duration) error
	hold       bool // do not connect until release() (tests)
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

type request struct {
	ctx  context.Context
	line string
	cmd  string
	res  chan result
}

type result struct {
	reply Reply
	err   error
}

// Client is one connection to one rigctld.
type Client struct {
	addr string
	log  *slog.Logger
	opt  options

	ctx    context.Context
	cancel context.CancelFunc
	reqs   chan *request
	start  chan struct{}
	done   chan struct{}

	mu        sync.Mutex
	connected bool
	callbacks []func(bool)
	events    []bool
	wake      chan struct{}
}

// Dial returns at once and connects in the background, retrying with backoff
// (1 s doubling to 30 s) until Close.
func Dial(addr string, log *slog.Logger) *Client { return dial(addr, log, options{}) }

func dial(addr string, log *slog.Logger, opt options) *Client {
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	if opt.cmdTimeout == 0 {
		opt.cmdTimeout = defaultCmdTimeout
	}
	if opt.sleep == nil {
		opt.sleep = sleepCtx
	}
	ctx, cancel := context.WithCancel(context.Background())
	c := &Client{
		addr: addr, log: log, opt: opt,
		ctx: ctx, cancel: cancel,
		reqs:  make(chan *request, queueSize),
		start: make(chan struct{}),
		done:  make(chan struct{}),
		wake:  make(chan struct{}, 1),
	}
	if !opt.hold {
		close(c.start)
	}
	go c.dispatch()
	go c.run()
	return c
}

func (c *Client) release() { close(c.start) }

// Close stops the client and closes the connection.
func (c *Client) Close() {
	c.cancel()
	<-c.done
}

// Connected reports whether a connection to rigctld is up.
func (c *Client) Connected() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.connected
}

// OnState registers fn to be called, on a separate goroutine and in order,
// each time the connection comes up (true) or goes down (false). It is not
// called for a state that predates the registration.
func (c *Client) OnState(fn func(connected bool)) {
	c.mu.Lock()
	c.callbacks = append(c.callbacks, fn)
	c.mu.Unlock()
}

func (c *Client) setState(up bool) {
	c.mu.Lock()
	if c.connected == up {
		c.mu.Unlock()
		return
	}
	c.connected = up
	c.events = append(c.events, up)
	c.mu.Unlock()
	if up {
		c.log.Info("rigctld connected", "addr", c.addr)
	} else {
		c.log.Warn("rigctld disconnected", "addr", c.addr)
	}
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *Client) dispatch() {
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-c.wake:
		}
		for {
			c.mu.Lock()
			if len(c.events) == 0 {
				c.mu.Unlock()
				break
			}
			ev := c.events[0]
			c.events = c.events[1:]
			cbs := append([]func(bool){}, c.callbacks...)
			c.mu.Unlock()
			for _, fn := range cbs {
				fn(ev)
			}
		}
	}
}

// Do sends one command and returns its reply. Callers are served in FIFO
// order, one command at a time. cmd is a long command name such as get_freq.
func (c *Client) Do(ctx context.Context, cmd string, args ...string) (Reply, error) {
	if !nameRe.MatchString(cmd) {
		return Reply{}, fmt.Errorf("rig: invalid command %q", cmd)
	}
	for _, a := range args {
		if strings.ContainsAny(a, "\r\n") {
			return Reply{}, errors.New("rig: argument contains a line break")
		}
	}
	if err := ctx.Err(); err != nil {
		return Reply{}, err
	}
	if c.ctx.Err() != nil {
		return Reply{}, ErrClosed
	}
	if !c.Connected() {
		return Reply{}, ErrNotConnected
	}
	line := `+\` + cmd
	if len(args) > 0 {
		line += " " + strings.Join(args, " ")
	}
	req := &request{ctx: ctx, line: line, cmd: cmd, res: make(chan result, 1)}
	select {
	case c.reqs <- req:
	case <-ctx.Done():
		return Reply{}, ctx.Err()
	case <-c.ctx.Done():
		return Reply{}, ErrClosed
	}
	select {
	case r := <-req.res:
		return r.reply, r.err
	case <-ctx.Done():
		return Reply{}, ctx.Err()
	case <-c.ctx.Done():
		return Reply{}, ErrClosed
	}
}

func (c *Client) run() {
	defer close(c.done)
	select {
	case <-c.start:
	case <-c.ctx.Done():
		return
	}
	backoff := backoffStart
	for c.ctx.Err() == nil {
		d := net.Dialer{Timeout: dialTimeout}
		conn, err := d.DialContext(c.ctx, "tcp", c.addr)
		if err != nil {
			if c.ctx.Err() != nil {
				return
			}
			c.log.Debug("rigctld dial failed", "addr", c.addr, "err", err, "retry_in", backoff)
			if c.opt.sleep(c.ctx, backoff) != nil {
				return
			}
			backoff = min(backoff*2, backoffMax)
			continue
		}
		backoff = backoffStart
		c.setState(true)
		c.serve(conn)
		conn.Close()
		c.setState(false)
		c.drain()
		if c.ctx.Err() != nil {
			return
		}
		if c.opt.sleep(c.ctx, backoff) != nil {
			return
		}
		backoff = min(backoff*2, backoffMax)
	}
}

// drain fails everything queued on a dead connection.
func (c *Client) drain() {
	for {
		select {
		case r := <-c.reqs:
			r.res <- result{err: ErrNotConnected}
		default:
			return
		}
	}
}

// serve runs queued commands on conn until it breaks or the client closes.
func (c *Client) serve(conn net.Conn) {
	br := bufio.NewReaderSize(conn, 64<<10)
	for {
		select {
		case <-c.ctx.Done():
			return
		case r := <-c.reqs:
			if r.ctx.Err() != nil {
				continue // caller gave up while queued
			}
			reply, err := c.exec(conn, br, r)
			r.res <- result{reply, err}
			if errors.Is(err, ErrDeadline) || errors.Is(err, ErrProtocol) || isConnErr(err) {
				c.log.Warn("rigctld connection reset", "addr", c.addr, "cmd", r.cmd, "err", err)
				return
			}
		}
	}
}

type connErr struct{ error }

func (e connErr) Unwrap() error { return e.error }

func isConnErr(err error) bool { var ce connErr; return errors.As(err, &ce) }

func (c *Client) exec(conn net.Conn, br *bufio.Reader, r *request) (Reply, error) {
	conn.SetDeadline(time.Now().Add(c.opt.cmdTimeout))
	if _, err := io.WriteString(conn, r.line+"\n"); err != nil {
		return Reply{}, wrapIO(err)
	}
	var lines []string
	for i := 0; ; i++ {
		l, err := br.ReadString('\n')
		if err != nil {
			e := wrapIO(err)
			if errors.Is(e, ErrDeadline) && (i > 0 || len(l) > 0) {
				// Some reply arrived but never reached its RPRT line.
				return Reply{}, fmt.Errorf("%w: no RPRT line before the deadline", ErrProtocol)
			}
			return Reply{}, e
		}
		l = strings.TrimRight(l, "\r\n")
		if i == 0 {
			if !strings.HasPrefix(l, r.cmd+":") {
				return Reply{}, fmt.Errorf("%w: echo %q for %q", ErrProtocol, l, r.cmd)
			}
			continue
		}
		if strings.HasPrefix(l, "RPRT ") {
			n, ok := parseRPRT(l)
			if !ok {
				return Reply{}, fmt.Errorf("%w: bad status line %q", ErrProtocol, l)
			}
			rep := Reply{Lines: lines, RPRT: n}
			if n != 0 {
				return rep, &RigError{Code: n}
			}
			return rep, nil
		}
		lines = append(lines, l)
	}
}

func wrapIO(err error) error {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return ErrDeadline
	}
	if errors.Is(err, io.EOF) {
		return connErr{fmt.Errorf("rig: connection closed by rigctld: %w", err)}
	}
	return connErr{fmt.Errorf("rig: %w", err)}
}
