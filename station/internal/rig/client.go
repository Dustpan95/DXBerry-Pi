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
	stillDownEvery    = 5 * time.Minute
	queueSize         = 256
	maxLine           = 64 << 10
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
	now        func() time.Time // the link log's clock (tests)
	hold       bool             // do not connect until release() (tests)
	// beforeEnqueue, if set, runs in Do between the connected check and the
	// enqueue (tests widen that window with it).
	beforeEnqueue func(cmd string)
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
	ctx   context.Context
	epoch uint64
	line  string
	cmd   string
	res   chan result
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
	epoch     uint64 // bumped on every successful connect
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
	if opt.now == nil {
		opt.now = time.Now
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
// called for a state that predates the registration, so register first and
// then read Connected(). Close delivers the final false event (if the
// connection was up) asynchronously.
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
	if up {
		c.epoch++
	}
	c.events = append(c.events, up)
	c.mu.Unlock()
	select {
	case c.wake <- struct{}{}:
	default:
	}
}

func (c *Client) dispatch() {
	for {
		stop := false
		select {
		case <-c.done:
			stop = true // run has finished: flush what is left, then exit
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
		if stop {
			return
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
	c.mu.Lock()
	up, epoch := c.connected, c.epoch
	c.mu.Unlock()
	if !up {
		return Reply{}, ErrNotConnected
	}
	if h := c.opt.beforeEnqueue; h != nil {
		h(cmd)
	}
	line := `+\` + cmd
	if len(args) > 0 {
		line += " " + strings.Join(args, " ")
	}
	req := &request{ctx: ctx, epoch: epoch, line: line, cmd: cmd, res: make(chan result, 1)}
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

// linkLog is the run loop's account of the link for the log: a link is "up" once a command
// has succeeded on its connection, so a rigctld that accepts and never answers (a radio switched
// off behind it) is one streak of "down", logged when it starts and every stillDownEvery, not
// a connected/disconnected pair every few seconds.
type linkLog struct {
	c         *Client
	up        bool      // a command succeeded on the current connection
	noted     bool      // the start of this down streak is in the log
	downSince time.Time // when the streak began
	lastNote  time.Time // the last line about it
}

// answered is called after each command that succeeded on the current connection.
func (l *linkLog) answered() {
	if l.up {
		return
	}
	l.up = true
	l.c.log.Info("rigctld connected", "addr", l.c.addr)
}

// failed is called for a dial that failed or a connection that ended. It reports whether the
// link had been up (the backoff then starts again).
func (l *linkLog) failed(err error) bool {
	now := l.c.opt.now()
	if l.up {
		l.up, l.noted, l.downSince, l.lastNote = false, true, now, now
		l.c.log.Warn("rigctld disconnected", "addr", l.c.addr, "error", err)
		return true
	}
	switch {
	case !l.noted:
		l.noted, l.lastNote = true, now
		l.c.log.Warn("rigctld not answering", "addr", l.c.addr, "error", err)
	case now.Sub(l.lastNote) >= stillDownEvery:
		l.lastNote = now
		l.c.log.Warn("rigctld still down", "addr", l.c.addr, "for", now.Sub(l.downSince).Round(time.Second), "error", err)
	default:
		l.c.log.Debug("rigctld link still down", "addr", l.c.addr, "error", err)
	}
	return false
}

func (c *Client) run() {
	defer close(c.done)
	select {
	case <-c.start:
	case <-c.ctx.Done():
		return
	}
	backoff := backoffStart
	ll := &linkLog{c: c, downSince: c.opt.now()}
	for c.ctx.Err() == nil {
		d := net.Dialer{Timeout: dialTimeout}
		conn, err := d.DialContext(c.ctx, "tcp", c.addr)
		if err != nil {
			if c.ctx.Err() != nil {
				return
			}
			ll.failed(err)
			c.log.Debug("rigctld dial failed", "addr", c.addr, "err", err, "retry_in", backoff)
			if c.opt.sleep(c.ctx, backoff) != nil {
				return
			}
			backoff = min(backoff*2, backoffMax)
			continue
		}
		stopClose := context.AfterFunc(c.ctx, func() { conn.Close() })
		c.setState(true)
		why := c.serve(conn, ll)
		stopClose()
		conn.Close()
		c.setState(false)
		if c.ctx.Err() != nil {
			return
		}
		if ll.failed(why) {
			backoff = backoffStart // the connection answered: start again from 1 s
		}
		if c.sleepDraining(backoff) != nil {
			return
		}
		backoff = min(backoff*2, backoffMax)
	}
}

// sleepDraining sleeps through the backoff while failing every request that
// reaches the queue, so nobody waits out the backoff and nothing stale
// survives into the next connection.
func (c *Client) sleepDraining(d time.Duration) error {
	stop := make(chan struct{})
	drained := make(chan struct{})
	go func() {
		defer close(drained)
		for {
			select {
			case r := <-c.reqs:
				r.res <- result{err: ErrNotConnected}
			case <-stop:
				return
			}
		}
	}()
	err := c.opt.sleep(c.ctx, d)
	close(stop)
	<-drained
	return err
}

// serve runs queued commands on conn until it breaks or the client closes, and says why it ended.
func (c *Client) serve(conn net.Conn, ll *linkLog) error {
	br := bufio.NewReaderSize(conn, maxLine)
	for {
		select {
		case <-c.ctx.Done():
			return ErrClosed
		case r := <-c.reqs:
			if r.ctx.Err() != nil {
				continue // caller gave up while queued
			}
			c.mu.Lock()
			stale := r.epoch != c.epoch
			c.mu.Unlock()
			if stale {
				r.res <- result{err: ErrNotConnected} // queued for an earlier connection
				continue
			}
			reply, err := c.exec(conn, br, r)
			r.res <- result{reply, err}
			if err == nil {
				ll.answered()
			}
			if errors.Is(err, ErrDeadline) || errors.Is(err, ErrProtocol) || isConnErr(err) {
				lvl := slog.LevelDebug // a link that never answered: its streak has its own lines
				if ll.up {
					lvl = slog.LevelWarn
				}
				c.log.Log(context.Background(), lvl, "rigctld connection reset", "addr", c.addr, "cmd", r.cmd, "err", err)
				return err
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
		raw, err := br.ReadSlice('\n')
		if errors.Is(err, bufio.ErrBufferFull) {
			return Reply{}, fmt.Errorf("%w: line longer than %d bytes", ErrProtocol, maxLine)
		}
		if err != nil {
			e := wrapIO(err)
			if errors.Is(e, ErrDeadline) && (i > 0 || len(raw) > 0) {
				// Some reply arrived but never reached its RPRT line.
				return Reply{}, fmt.Errorf("%w: no RPRT line before the deadline", errors.Join(ErrProtocol, ErrDeadline))
			}
			return Reply{}, e
		}
		l := strings.TrimRight(string(raw), "\r\n")
		if i == 0 {
			if !strings.HasPrefix(l, r.cmd+":") {
				return Reply{}, fmt.Errorf("%w: echo %q for %q", ErrProtocol, l, r.cmd)
			}
			continue
		}
		if n, ok := parseRPRT(l); ok {
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
