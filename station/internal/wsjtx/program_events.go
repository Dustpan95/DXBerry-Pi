package wsjtx

import (
	"bytes"
	"context"
	"encoding/json"
	"sort"
	"sync"
	"time"
)

const (
	MaxProgramEventData = 256 << 10
	programEventEvery   = time.Second
	programEventBudget  = 500 * time.Millisecond
)

// ProgramEventView is a replacement snapshot, never an instruction. Error
// codes are fixed; private transport identities, approvals and settings are
// absent. Omitted history remains in the bounded tracker/HTTP snapshot.
type ProgramEventView struct {
	Version        int                     `json:"version"`
	Available      bool                    `json:"available"`
	Code           string                  `json:"code,omitempty"`
	State          *ProgramCollectionState `json:"state,omitempty"`
	DecodesOmitted int                     `json:"decodes_omitted"`
}

func unavailableProgramEvent(code string) []byte {
	raw, _ := json.Marshal(ProgramEventView{Version: 1, Code: code})
	return raw
}

// encodeProgramEvent fits a contiguous newest-history suffix into the wire
// budget. It never truncates a text field or changes the stored observation.
func encodeProgramEvent(radio string, state ProgramCollectionState) []byte {
	view := ProgramEventView{Version: 1, Available: true, State: &state}
	var history []ReceivedDecode
	if state.Observation != nil {
		if state.Observation.Radio != radio {
			return unavailableProgramEvent("unavailable")
		}
		observation := *state.Observation
		history = observation.Decodes
		observation.Decodes = nil
		state.Observation = &observation
		view.DecodesOmitted = len(history)
	}
	base, err := json.Marshal(view)
	if err != nil || len(base) > MaxProgramEventData-1024 {
		return unavailableProgramEvent("observation-too-large")
	}
	used, start := len(base), len(history)
	for start > 0 {
		raw, err := json.Marshal(history[start-1])
		if err != nil {
			return unavailableProgramEvent("unavailable")
		}
		if used+len(raw)+1 > MaxProgramEventData-1024 {
			break
		}
		used += len(raw) + 1
		start--
	}
	if state.Observation != nil {
		state.Observation.Decodes = history[start:]
		view.DecodesOmitted = start
	}
	raw, err := json.Marshal(view)
	if err != nil || len(raw) > MaxProgramEventData {
		return unavailableProgramEvent("observation-too-large")
	}
	return raw
}

type programEventWatch struct{ raw []byte }

// ProgramEvents polls all watched radios once per second in one bounded
// producer. Subscriber queues hold only a coalesced change signal; slow sockets
// read the latest cache instead of retaining old decode snapshots. A watch
// never initializes inventory, changes consent or starts a program.
type ProgramEvents struct {
	mu      sync.Mutex
	runMu   sync.Mutex
	watches map[string]*programEventWatch
	subs    map[chan struct{}]struct{}
	read    func(context.Context, string) (ProgramCollectionState, error)
	known   func(string) bool
}

func NewProgramEvents(read func(context.Context, string) (ProgramCollectionState, error), known func(string) bool) *ProgramEvents {
	return &ProgramEvents{watches: map[string]*programEventWatch{}, subs: map[chan struct{}]struct{}{}, read: read, known: known}
}

// SetWatch receives the maximum interest across sockets, like the rig hub.
func (p *ProgramEvents) SetWatch(radio string, level int) {
	if !programRadio(radio) || level < 0 || level > 2 {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if level == 0 {
		delete(p.watches, radio)
	} else if p.watches[radio] == nil && len(p.watches) < MaxSessions {
		p.watches[radio] = &programEventWatch{raw: unavailableProgramEvent("loading")}
	}
}

func (p *ProgramEvents) Snapshot(radio string) ([]byte, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if watch := p.watches[radio]; watch != nil {
		return append([]byte(nil), watch.raw...), true
	}
	if programRadio(radio) {
		return unavailableProgramEvent("capacity"), true
	}
	return nil, false
}

func (p *ProgramEvents) Subscribe() (<-chan struct{}, func()) {
	ch := make(chan struct{}, 1)
	p.mu.Lock()
	p.subs[ch] = struct{}{}
	p.mu.Unlock()
	var once sync.Once
	return ch, func() { once.Do(func() { p.mu.Lock(); delete(p.subs, ch); p.mu.Unlock() }) }
}

func (p *ProgramEvents) refresh(ctx context.Context) {
	p.mu.Lock()
	watches := make(map[string]*programEventWatch, len(p.watches))
	var names []string
	for name, watch := range p.watches {
		names = append(names, name)
		watches[name] = watch
	}
	p.mu.Unlock()
	sort.Strings(names)
	ctx, cancel := context.WithTimeout(ctx, programEventBudget)
	defer cancel()
	changed := false
	for _, name := range names {
		if ctx.Err() != nil {
			// Publish unavailable for the rest too: never retain a live label
			// because a lifecycle operation occupied the service lock.
			changed = p.publish(name, watches[name], unavailableProgramEvent("unavailable")) || changed
			continue
		}
		raw := unavailableProgramEvent("unavailable")
		if p.known != nil && p.known(name) && p.read != nil {
			state, err := p.read(ctx, name)
			if err == nil && ctx.Err() == nil {
				raw = encodeProgramEvent(name, state)
			}
		}
		changed = p.publish(name, watches[name], raw) || changed
	}
	if changed {
		p.mu.Lock()
		defer p.mu.Unlock()
		for ch := range p.subs {
			select {
			case ch <- struct{}{}:
			default:
			}
		}
	}
}

func (p *ProgramEvents) publish(name string, watch *programEventWatch, raw []byte) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.watches[name] != watch || bytes.Equal(watch.raw, raw) {
		return false
	}
	watch.raw = raw
	return true
}

func (p *ProgramEvents) Run(ctx context.Context) {
	if !p.runMu.TryLock() {
		return
	}
	defer p.runMu.Unlock()
	tick := time.NewTicker(programEventEvery)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
			p.refresh(ctx)
		}
	}
}
