package radios

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"
)

const two = `{"radios":{
 "ft991a":{"label":"FT-991A","rigctld_port":4532,"owner":"station","ptt":{"method":"cat"},"rig":{"model":1035,"control":"usb"},"cat":{"device":"/dev/ttyUSB0"}},
 "alpha":{"label":"Alpha","rigctld_port":4533,"owner":"graywolf","ptt":{"method":"gpio"},"rig":{"model":2,"control":"net"},"cat":null}}}`

func write(t *testing.T, p, s string) {
	t.Helper()
	if err := os.WriteFile(p, []byte(s), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestWatcherReadsRecords(t *testing.T) {
	p := filepath.Join(t.TempDir(), "radios.json")
	write(t, p, two)
	w := New(p, 20*time.Millisecond)
	l := w.List()
	if len(l) != 2 || l[0].Name != "alpha" || l[1].Name != "ft991a" {
		t.Fatalf("list = %+v", l)
	}
	if l[0].HasCAT || !l[1].HasCAT {
		t.Errorf("HasCAT wrong: %+v", l)
	}
	want := Radio{Name: "ft991a", Label: "FT-991A", Port: 4532, Owner: "station", PTT: "cat", Model: 1035, Control: "usb", HasCAT: true}
	if g, ok := w.Get("ft991a"); !ok || g != want {
		t.Errorf("Get = %+v %v", g, ok)
	}
	if _, ok := w.Get("nope"); ok {
		t.Error("unknown name found")
	}
}

func TestWatcherNotifiesOnChange(t *testing.T) {
	p := filepath.Join(t.TempDir(), "radios.json")
	write(t, p, two)
	w := New(p, 20*time.Millisecond)
	a, b := w.Subscribe(), w.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	write(t, p, `{"radios":{"solo":{"label":"Solo","rigctld_port":4540,"owner":"station","ptt":{"method":"vox"},"rig":{"model":1,"control":"none"},"cat":null}}}`)
	for i, ch := range []<-chan []Radio{a, b} {
		select {
		case l := <-ch:
			if len(l) != 1 || l[0].Name != "solo" {
				t.Errorf("subscriber %d got %+v", i, l)
			}
		case <-time.After(60 * time.Millisecond * 5):
			t.Fatalf("subscriber %d not notified", i)
		}
	}
	if len(w.List()) != 1 {
		t.Errorf("List = %+v", w.List())
	}
}

func TestWatcherIgnoresInvalidJSON(t *testing.T) {
	p := filepath.Join(t.TempDir(), "radios.json")
	write(t, p, two)
	w := New(p, 10*time.Millisecond)
	ch := w.Subscribe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go w.Run(ctx)
	write(t, p, `{"radios": {oops`)
	select {
	case l := <-ch:
		t.Fatalf("notified for bad JSON: %+v", l)
	case <-time.After(120 * time.Millisecond):
	}
	if len(w.List()) != 2 {
		t.Errorf("last good list lost: %+v", w.List())
	}
}
