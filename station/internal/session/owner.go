// Package session holds who may transmit on each radio: one program session or one browser
// tab at a time.
package session

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
)

// IdleLimit is how long a tab keeps a radio without a transmit intent.
const IdleLimit = 10 * time.Minute

// Holder is whoever holds a radio's transmitter. A tab's ID is its login session plus the
// tab's own token (see TabID); a program's ID is the program name.
type Holder struct {
	Kind  string `json:"kind"` // "tab" or "program"
	ID    string `json:"id"`
	Label string `json:"label"`
}

// Refusals.
var (
	ErrProgramOwns  = errors.New("a program holds this radio's transmitter")
	ErrHeld         = errors.New("another tab holds this radio's transmitter")
	ErrTransmitting = errors.New("the radio is transmitting")
)

// Clock is the owner lock's source of time.
type Clock interface{ Now() time.Time }

// TabID names one browser tab: the login session plus the tab's token. The session id never
// leaves the process.
func TabID(sessionID, tab string) string { return sessionID + "|" + tab }

// TabLabel is the device name from a tab token of the form "name#nonce".
func TabLabel(tab string) string {
	if i := strings.LastIndex(tab, "#"); i > 0 {
		return tab[:i]
	}
	return tab
}

type claim struct {
	h    Holder
	last time.Time
}

// Owners is the lock for every radio.
type Owners struct {
	clock Clock

	mu           sync.Mutex
	claims       map[string]claim
	programs     *Programs
	transmitting func(radio string) bool
	onDrop       func(radio string)
	sockets      map[string]int // open events sockets per tab id
}

// NewOwners returns an empty lock.
func NewOwners(clock Clock) *Owners {
	return &Owners{clock: clock, claims: map[string]claim{}, sockets: map[string]int{}}
}

// SetPrograms makes running programs holders.
func (o *Owners) SetPrograms(p *Programs) {
	o.mu.Lock()
	o.programs = p
	o.mu.Unlock()
}

// SetTransmitting tells the lock how to see whether a radio's PTT reads on.
func (o *Owners) SetTransmitting(fn func(radio string) bool) {
	o.mu.Lock()
	o.transmitting = fn
	o.mu.Unlock()
}

// OnDrop sets a function told, after the lock is released, whenever a tab's claim on a radio ends
// (release, socket close, idle). The intents use it to end a running carrier.
func (o *Owners) OnDrop(fn func(radio string)) {
	o.mu.Lock()
	o.onDrop = fn
	o.mu.Unlock()
}

// dropped tells the OnDrop function about radios; call it with o.mu not held.
func (o *Owners) dropped(radios ...string) {
	o.mu.Lock()
	fn := o.onDrop
	o.mu.Unlock()
	if fn == nil {
		return
	}
	for _, r := range radios {
		fn(r)
	}
}

// SocketOpened counts an events socket of the tab id.
func (o *Owners) SocketOpened(id string) {
	o.mu.Lock()
	o.sockets[id]++
	o.mu.Unlock()
}

// SocketClosed ends the count of one socket; the tab's claims are released when its last
// socket closes (a reload opens the new socket before the old one's close arrives).
func (o *Owners) SocketClosed(id string) {
	o.mu.Lock()
	o.sockets[id]--
	last := o.sockets[id] <= 0
	if last {
		delete(o.sockets, id)
	}
	o.mu.Unlock()
	if last {
		o.ReleaseAll(id)
	}
}

func (o *Owners) programLocked(radio string) (Holder, bool) {
	if o.programs == nil {
		return Holder{}, false
	}
	return o.programs.Holder(radio)
}

// expireLocked drops a tab claim that has been idle too long.
func (o *Owners) expireLocked(radio string) (dropped bool) {
	if c, ok := o.claims[radio]; ok && o.clock.Now().Sub(c.last) >= IdleLimit {
		delete(o.claims, radio)
		return true
	}
	return false
}

// Claim gives radio to a tab. It is refused while a program runs on the radio or another tab
// holds it.
func (o *Owners) Claim(radio string, h Holder) error {
	o.mu.Lock()
	if _, ok := o.programLocked(radio); ok {
		o.mu.Unlock()
		return ErrProgramOwns
	}
	exp := o.expireLocked(radio)
	var err error
	if c, ok := o.claims[radio]; ok && c.h.ID != h.ID {
		err = ErrHeld
	} else {
		o.claims[radio] = claim{h: h, last: o.clock.Now()}
	}
	o.mu.Unlock()
	if exp {
		o.dropped(radio)
	}
	return err
}

// TakeOver moves a tab's claim to h, unless the radio is transmitting or a program holds it.
func (o *Owners) TakeOver(radio string, h Holder) error {
	o.mu.Lock()
	if _, ok := o.programLocked(radio); ok {
		o.mu.Unlock()
		return ErrProgramOwns
	}
	exp := o.expireLocked(radio)
	var err error
	if c, ok := o.claims[radio]; ok && c.h.ID != h.ID && o.transmitting != nil && o.transmitting(radio) {
		err = ErrTransmitting
	} else {
		o.claims[radio] = claim{h: h, last: o.clock.Now()}
	}
	o.mu.Unlock()
	if exp {
		o.dropped(radio)
	}
	return err
}

// Release drops id's claim on radio; anyone else's is left alone.
func (o *Owners) Release(radio, id string) {
	o.mu.Lock()
	c, ok := o.claims[radio]
	ok = ok && c.h.ID == id
	if ok {
		delete(o.claims, radio)
	}
	o.mu.Unlock()
	if ok {
		o.dropped(radio)
	}
}

// ReleaseAll drops every claim id holds (its events socket closed).
func (o *Owners) ReleaseAll(id string) {
	o.mu.Lock()
	var gone []string
	for r, c := range o.claims {
		if c.h.ID == id {
			delete(o.claims, r)
			gone = append(gone, r)
		}
	}
	o.mu.Unlock()
	o.dropped(gone...)
}

// Touch records a transmit intent by id, which restarts its idle time.
func (o *Owners) Touch(radio, id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if c, ok := o.claims[radio]; ok && c.h.ID == id {
		c.last = o.clock.Now()
		o.claims[radio] = c
	}
}

// Holder says who holds radio: a running program first, else a tab.
func (o *Owners) Holder(radio string) (Holder, bool) {
	o.mu.Lock()
	if h, ok := o.programLocked(radio); ok {
		o.mu.Unlock()
		return h, true
	}
	exp := o.expireLocked(radio)
	c, ok := o.claims[radio]
	o.mu.Unlock()
	if exp {
		o.dropped(radio)
	}
	return c.h, ok
}

// Probe says whether the program session (<program>-<radio>) holds the radio (running or
// starting), and the program's label. An error means it could not tell.
type Probe func(ctx context.Context, session string) (held bool, label string, err error)

// Programs derives program holders from the radio records and the running screens.
type Programs struct {
	list  func() []radios.Radio
	probe Probe

	mu       sync.Mutex
	holders  map[string]Holder
	failing  map[string]bool // radios whose last probe failed (logged once per streak)
	answered map[string]bool // radios a probe has answered for (held or free)
}

// NewPrograms returns a source that has not looked yet; Refresh or Run fills it.
func NewPrograms(list func() []radios.Radio, probe Probe) *Programs {
	return &Programs{list: list, probe: probe, holders: map[string]Holder{}, failing: map[string]bool{}, answered: map[string]bool{}}
}

// Refresh asks about every radio that has an owner and keeps the answers. When a probe cannot
// tell, the radio keeps the answer it had: a transient failure must not free a transmitter. With
// no answer yet (the first probe after a start fails) the radio's owner program is taken to hold
// it until a probe succeeds: failing closed.
func (p *Programs) Refresh(ctx context.Context) {
	next := map[string]Holder{}
	failing := map[string]bool{}
	answered := map[string]bool{}
	for _, r := range p.list() {
		if r.Owner == "" {
			continue
		}
		held, label, err := p.probe(ctx, r.Owner+"-"+r.Name)
		if err != nil {
			failing[r.Name] = true
			p.mu.Lock()
			prev, had := p.holders[r.Name]
			known := p.answered[r.Name]
			again := p.failing[r.Name]
			p.mu.Unlock()
			if !again {
				slog.Error("could not tell whether a program holds a radio; keeping the last answer", "radio", r.Name, "owner", r.Owner, "error", err, "kept", had, "answered_before", known)
			}
			switch {
			case had:
				next[r.Name] = prev
			case !known:
				next[r.Name] = Holder{Kind: "program", ID: r.Owner, Label: r.Owner}
			}
			answered[r.Name] = known
			continue
		}
		answered[r.Name] = true
		if held {
			if label == "" {
				label = r.Owner
			}
			next[r.Name] = Holder{Kind: "program", ID: r.Owner, Label: label}
		}
	}
	p.mu.Lock()
	p.holders, p.failing, p.answered = next, failing, answered
	p.mu.Unlock()
}

// Run refreshes every interval until ctx ends.
func (p *Programs) Run(ctx context.Context, every time.Duration) {
	p.Refresh(ctx)
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			p.Refresh(ctx)
		}
	}
}

// Holder is the program running on radio, as of the last refresh.
func (p *Programs) Holder(radio string) (Holder, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	h, ok := p.holders[radio]
	return h, ok
}

// ScreenProbe asks `dxberry-app screen <session> --json` through run. The session holds the radio
// while its state is "running" or "starting". A failed command, unreadable output or
// access:false is an error, not "free".
func ScreenProbe(run func(ctx context.Context, args []string) (stdout string, exit int, err error)) Probe {
	return func(ctx context.Context, session string) (bool, string, error) {
		out, exit, err := run(ctx, []string{"screen", session, "--json"})
		if err != nil {
			return false, "", err
		}
		if exit != 0 {
			return false, "", fmt.Errorf("dxberry-app screen exited %d", exit)
		}
		var v struct {
			State, Label string
			Access       *bool
		}
		if json.Unmarshal([]byte(out), &v) != nil {
			return false, "", errors.New("dxberry-app screen printed something unreadable")
		}
		if v.Access != nil && !*v.Access {
			return false, "", errors.New("the station user cannot read program screens (access false): log out and back in, or join the dxbscreen group")
		}
		return v.State == "running" || v.State == "starting", v.Label, nil
	}
}
