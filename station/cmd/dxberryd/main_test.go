package main

import (
	"context"
	"io"
	"log/slog"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/audio"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/rig"
)

type lister struct {
	mu   sync.Mutex
	list []radios.Radio
	ch   chan []radios.Radio
}

func (l *lister) List() []radios.Radio {
	l.mu.Lock()
	defer l.mu.Unlock()
	return append([]radios.Radio{}, l.list...)
}
func (l *lister) Subscribe() <-chan []radios.Radio  { return l.ch }
func (l *lister) Unsubscribe(<-chan []radios.Radio) {}

// idleConn is a rigctld connection that is never up.
type idleConn struct{}

func (idleConn) Do(context.Context, string, ...string) (rig.Reply, error) {
	return rig.Reply{}, rig.ErrNotConnected
}
func (idleConn) Connected() bool    { return false }
func (idleConn) OnState(func(bool)) {}
func (idleConn) Close()             {}

// A radio that leaves the records takes its WebRTC bridge with it.
func TestRemovedRadioLosesItsBridge(t *testing.T) {
	quiet := slog.New(slog.NewTextHandler(io.Discard, nil))
	lst := &lister{list: []radios.Radio{{Name: "radio1", Port: 4532, Model: 1}}, ch: make(chan []radios.Radio, 2)}
	m := rig.NewManager(lst, rig.CapsCache{Dir: filepath.Join(t.TempDir(), "rigcaps")}, nil, quiet)
	m.Dialer = func(string) rig.Conn { return idleConn{} }
	bridges := audio.NewBridges(audio.NewServerControl(nil, t.TempDir()), audio.DefaultPorts, quiet)
	t.Cleanup(bridges.Shutdown)
	wireRadioRemoval(m, bridges)
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { m.Run(ctx); close(done) }()
	t.Cleanup(func() { cancel(); <-done })

	b := bridges.Get("radio1")
	if bridges.Get("radio1") != b {
		t.Fatal("Get made two bridges")
	}
	lst.ch <- nil
	deadline := time.Now().Add(3 * time.Second)
	for bridges.Get("radio1") == b {
		if time.Now().After(deadline) {
			t.Fatal("the radio left the records but kept its bridge")
		}
		time.Sleep(10 * time.Millisecond)
	}
}
