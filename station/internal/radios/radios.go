// Package radios watches the radio records file the provisioning scripts write and tells
// subscribers when the set of radios changes.
package radios

import (
	"context"
	"encoding/json"
	"log/slog"
	"os"
	"reflect"
	"sort"
	"strconv"
	"sync"
	"time"
)

// Radio is one radio record, reduced to what the station app needs.
type Radio struct {
	Name    string `json:"name"`
	Label   string `json:"label"`
	Port    int    `json:"port"`    // rigctld_port
	Owner   string `json:"owner"`   // who may transmit
	PTT     string `json:"ptt"`     // ptt.method
	Model   int    `json:"model"`   // rig.model (hamlib)
	Control string `json:"control"` // rig.control
	HasCAT  bool   `json:"has_cat"` // the record's cat is not null
}

type record struct {
	Label       string `json:"label"`
	RigctldPort int    `json:"rigctld_port"`
	Owner       string `json:"owner"`
	PTT         struct {
		Method string `json:"method"`
	} `json:"ptt"`
	Rig struct {
		Model   int    `json:"model"`
		Control string `json:"control"`
	} `json:"rig"`
	CAT json.RawMessage `json:"cat"`
}

// Watcher polls the records file's modification time and re-reads it when it changes.
type Watcher struct {
	path string
	poll time.Duration

	mu      sync.Mutex
	list    []Radio
	subs    map[chan []Radio]struct{}
	lastSig string // mtime and size of the file last read, good or bad
}

// New reads path once (a missing or bad file gives an empty list) and returns the watcher;
// Run keeps it current, checking every poll (2 s when poll is zero).
func New(path string, poll time.Duration) *Watcher {
	if poll <= 0 {
		poll = 2 * time.Second
	}
	w := &Watcher{path: path, poll: poll, subs: map[chan []Radio]struct{}{}}
	w.reload()
	return w
}

// Run polls until ctx ends.
func (w *Watcher) Run(ctx context.Context) {
	t := time.NewTicker(w.poll)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			w.reload()
		}
	}
}

func (w *Watcher) reload() {
	fi, err := os.Stat(w.path)
	if err != nil {
		return // absent: keep what we have
	}
	sig := fi.ModTime().Format(time.RFC3339Nano) + "/" + strconv.FormatInt(fi.Size(), 10)
	w.mu.Lock()
	same := sig == w.lastSig
	w.lastSig = sig
	w.mu.Unlock()
	if same {
		return // unchanged since the last read; a bad file is logged once, not every poll
	}
	data, err := os.ReadFile(w.path)
	if err != nil {
		slog.Warn("radio records unreadable", "path", w.path, "error", err)
		return
	}
	var file struct {
		Radios map[string]record `json:"radios"`
	}
	if err := json.Unmarshal(data, &file); err != nil {
		slog.Warn("radio records are not valid JSON; keeping the last good list", "path", w.path, "error", err)
		return
	}
	list := make([]Radio, 0, len(file.Radios))
	for name, r := range file.Radios {
		list = append(list, Radio{
			Name: name, Label: r.Label, Port: r.RigctldPort, Owner: r.Owner, PTT: r.PTT.Method,
			Model: r.Rig.Model, Control: r.Rig.Control,
			HasCAT: len(r.CAT) > 0 && string(r.CAT) != "null",
		})
	}
	sort.Slice(list, func(i, j int) bool { return list[i].Name < list[j].Name })

	w.mu.Lock()
	defer w.mu.Unlock()
	if reflect.DeepEqual(list, w.list) {
		return
	}
	w.list = list
	for ch := range w.subs {
		out := append([]Radio(nil), list...)
		select {
		case ch <- out:
		default: // a list is still pending: replace it with the latest
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- out:
			default:
			}
		}
	}
}

// List returns the radios sorted by name; the slice is the caller's.
func (w *Watcher) List() []Radio {
	w.mu.Lock()
	defer w.mu.Unlock()
	return append([]Radio{}, w.list...)
}

// Get returns the radio called name.
func (w *Watcher) Get(name string) (Radio, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for _, r := range w.list {
		if r.Name == name {
			return r, true
		}
	}
	return Radio{}, false
}

// Subscribe returns a channel (buffer 1) that receives the latest list after every change. Each
// call gets its own channel, so several subscribers each see every change.
func (w *Watcher) Subscribe() <-chan []Radio {
	ch := make(chan []Radio, 1)
	w.mu.Lock()
	w.subs[ch] = struct{}{}
	w.mu.Unlock()
	return ch
}

// Unsubscribe stops deliveries to a channel Subscribe returned.
func (w *Watcher) Unsubscribe(ch <-chan []Radio) {
	w.mu.Lock()
	defer w.mu.Unlock()
	for c := range w.subs {
		if (<-chan []Radio)(c) == ch {
			delete(w.subs, c)
		}
	}
}
