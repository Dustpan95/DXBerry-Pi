// Package session holds who may transmit on each radio: one program session or one browser
// tab at a time.
package session

import (
	"context"
	"encoding/json"
	"errors"
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
}

// NewOwners returns an empty lock.
func NewOwners(clock Clock) *Owners {
	return &Owners{clock: clock, claims: map[string]claim{}}
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

func (o *Owners) programLocked(radio string) (Holder, bool) {
	if o.programs == nil {
		return Holder{}, false
	}
	return o.programs.Holder(radio)
}

// expireLocked drops a tab claim that has been idle too long.
func (o *Owners) expireLocked(radio string) {
	if c, ok := o.claims[radio]; ok && o.clock.Now().Sub(c.last) >= IdleLimit {
		delete(o.claims, radio)
	}
}

// Claim gives radio to a tab. It is refused while a program runs on the radio or another tab
// holds it.
func (o *Owners) Claim(radio string, h Holder) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.programLocked(radio); ok {
		return ErrProgramOwns
	}
	o.expireLocked(radio)
	if c, ok := o.claims[radio]; ok && c.h.ID != h.ID {
		return ErrHeld
	}
	o.claims[radio] = claim{h: h, last: o.clock.Now()}
	return nil
}

// TakeOver moves a tab's claim to h, unless the radio is transmitting or a program holds it.
func (o *Owners) TakeOver(radio string, h Holder) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if _, ok := o.programLocked(radio); ok {
		return ErrProgramOwns
	}
	o.expireLocked(radio)
	if c, ok := o.claims[radio]; ok && c.h.ID != h.ID && o.transmitting != nil && o.transmitting(radio) {
		return ErrTransmitting
	}
	o.claims[radio] = claim{h: h, last: o.clock.Now()}
	return nil
}

// Release drops id's claim on radio; anyone else's is left alone.
func (o *Owners) Release(radio, id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if c, ok := o.claims[radio]; ok && c.h.ID == id {
		delete(o.claims, radio)
	}
}

// ReleaseAll drops every claim id holds (its events socket closed).
func (o *Owners) ReleaseAll(id string) {
	o.mu.Lock()
	defer o.mu.Unlock()
	for r, c := range o.claims {
		if c.h.ID == id {
			delete(o.claims, r)
		}
	}
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
	defer o.mu.Unlock()
	if h, ok := o.programLocked(radio); ok {
		return h, true
	}
	o.expireLocked(radio)
	c, ok := o.claims[radio]
	return c.h, ok
}

// Probe says whether the program session (<program>-<radio>) is running, and the program's
// label.
type Probe func(ctx context.Context, session string) (running bool, label string)

// Programs derives program holders from the radio records and the running screens.
type Programs struct {
	list  func() []radios.Radio
	probe Probe

	mu      sync.Mutex
	holders map[string]Holder
}

// NewPrograms returns a source that has not looked yet; Refresh or Run fills it.
func NewPrograms(list func() []radios.Radio, probe Probe) *Programs {
	return &Programs{list: list, probe: probe, holders: map[string]Holder{}}
}

// Refresh asks about every radio that has an owner and keeps the answers.
func (p *Programs) Refresh(ctx context.Context) {
	next := map[string]Holder{}
	for _, r := range p.list() {
		if r.Owner == "" {
			continue
		}
		if ok, label := p.probe(ctx, r.Owner+"-"+r.Name); ok {
			if label == "" {
				label = r.Owner
			}
			next[r.Name] = Holder{Kind: "program", ID: r.Owner, Label: label}
		}
	}
	p.mu.Lock()
	p.holders = next
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

// ScreenProbe asks `dxberry-app screen <session> --json` through run and reads state == "running".
// run executes that command with the given arguments (the cli package's runner, in the service).
func ScreenProbe(run func(ctx context.Context, args []string) (stdout string, exit int, err error)) Probe {
	return func(ctx context.Context, session string) (bool, string) {
		out, exit, err := run(ctx, []string{"screen", session, "--json"})
		if err != nil || exit != 0 {
			return false, ""
		}
		var v struct{ State, Label string }
		if json.Unmarshal([]byte(out), &v) != nil {
			return false, ""
		}
		return v.State == "running", v.Label
	}
}
