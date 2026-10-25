package settings

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSettingsDefaults(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	got := s.Get("radio1")
	if got.SWRLimit != 3.0 || got.PTTTimeoutS != 180 || !got.Probed.IsZero() {
		t.Fatalf("defaults: %+v", got)
	}
	// unreadable file: defaults too
	if err := os.WriteFile(filepath.Join(s.Dir, "radio2.json"), []byte("{nope"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := s.Get("radio2"); got.SWRLimit != 3.0 || got.PTTTimeoutS != 180 {
		t.Fatalf("unreadable: %+v", got)
	}
	// a bad name never reaches a path
	if got := s.Get("../x"); got.SWRLimit != 3.0 {
		t.Fatalf("bad name: %+v", got)
	}
}

func TestSettingsValidation(t *testing.T) {
	s := Store{Dir: t.TempDir()}
	for _, tc := range []struct {
		swr  float64
		ptt  int
		want string
	}{
		{1.4, 180, "1.5"}, {10.1, 180, "10"}, {3, 29, "30"}, {3, 601, "600"},
	} {
		err := s.Set("radio1", RadioSettings{SWRLimit: tc.swr, PTTTimeoutS: tc.ptt})
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Fatalf("%v/%v: err %v, want a mention of %s", tc.swr, tc.ptt, err, tc.want)
		}
	}
	if err := s.Set("../etc", RadioSettings{SWRLimit: 3, PTTTimeoutS: 180}); err == nil {
		t.Fatal("bad name accepted")
	}
	if es, _ := os.ReadDir(s.Dir); len(es) != 0 {
		t.Fatalf("a refused Set wrote files: %v", es)
	}
	for _, ok := range []RadioSettings{{SWRLimit: 1.5, PTTTimeoutS: 30}, {SWRLimit: 10, PTTTimeoutS: 600}} {
		if err := s.Set("radio1", ok); err != nil {
			t.Fatalf("%+v: %v", ok, err)
		}
	}
}

func TestSettingsRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "radios") // created on first write
	s := Store{Dir: dir}
	at := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	if err := s.Set("radio1", RadioSettings{SWRLimit: 2.5, PTTTimeoutS: 90, Probed: at}); err != nil {
		t.Fatal(err)
	}
	got := s.Get("radio1")
	if got.SWRLimit != 2.5 || got.PTTTimeoutS != 90 || !got.Probed.Equal(at) {
		t.Fatalf("got %+v", got)
	}
	fi, err := os.Stat(filepath.Join(dir, "radio1.json"))
	if err != nil || fi.Mode().Perm() != 0o600 {
		t.Fatalf("stat %v %v", fi, err)
	}
	if es, _ := os.ReadDir(dir); len(es) != 1 {
		t.Fatalf("temp file left behind: %v", es)
	}
	// SetProbed keeps the limits
	at2 := at.Add(time.Hour)
	if err := s.SetProbed("radio1", at2); err != nil {
		t.Fatal(err)
	}
	got = s.Get("radio1")
	if got.SWRLimit != 2.5 || got.PTTTimeoutS != 90 || !got.Probed.Equal(at2) {
		t.Fatalf("after SetProbed %+v", got)
	}
}
