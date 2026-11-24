package settings

import (
	"os"
	"path/filepath"
	"strings"
	"sync"
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

func TestSettingsConcurrentSetAndProbed(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			if err := s.Modify("radio1", func(r *RadioSettings) { r.SWRLimit = 2.5; r.PTTTimeoutS = 60 + i }); err != nil {
				t.Error(err)
			}
		}(i)
		go func() {
			defer wg.Done()
			if err := s.SetProbed("radio1", time.Now()); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	got := s.Get("radio1")
	if got.SWRLimit != 2.5 || got.PTTTimeoutS < 60 || got.Probed.IsZero() {
		t.Fatalf("a writer lost an update: %+v", got)
	}
	// a stale temp file from a crash is cleaned by the next write
	stale := filepath.Join(s.Dir, ".radio1-123.tmp")
	if err := os.WriteFile(stale, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := s.Set("radio1", Defaults()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(stale); err == nil {
		t.Fatal("stale temp file kept")
	}
}

func TestRoutingApprovalPreservesGuardSettingsAndConcurrentUpdates(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if err := s.Set("radio1", RadioSettings{SWRLimit: 2.5, PTTTimeoutS: 90}); err != nil {
		t.Fatal(err)
	}
	approval := strings.Repeat("a", 64)
	if err := s.SaveRoutingApproval("radio1", "", approval); err != nil {
		t.Fatal(err)
	}
	at := time.Now().UTC()
	if err := s.SetProbed("radio1", at); err != nil {
		t.Fatal(err)
	}
	if err := s.Modify("radio1", func(v *RadioSettings) { v.SWRLimit = 2; v.PTTTimeoutS = 60 }); err != nil {
		t.Fatal(err)
	}
	value, err := s.ReadStrict("radio1")
	if err != nil || value.WSJTXRoutingApproval != approval || value.SWRLimit != 2 || value.PTTTimeoutS != 60 || !value.Probed.Equal(at) {
		t.Fatal("an ordinary update lost consent or guard settings", value, err)
	}
	if err := s.SaveRoutingApproval("radio1", "", strings.Repeat("b", 64)); err == nil {
		t.Fatal("stale consent overwrote newer approval")
	}
	if err := s.SaveRoutingApproval("radio1", approval, ""); err != nil {
		t.Fatal(err)
	}
	value, err = s.ReadStrict("radio1")
	if err != nil || value.WSJTXRoutingApproval != "" || value.SWRLimit != 2 || value.PTTTimeoutS != 60 {
		t.Fatal("disabling reset guards", value, err)
	}
}

func TestRoutingApprovalInventoryDistinguishesEmptyConsentAndDamage(t *testing.T) {
	s := &Store{Dir: filepath.Join(t.TempDir(), "radios")}
	if approved, err := s.HasRoutingApprovals(); err != nil || approved {
		t.Fatal(approved, err)
	}
	if err := s.SaveRoutingApproval("radio1", "", strings.Repeat("a", 64)); err != nil {
		t.Fatal(err)
	}
	if approved, err := s.HasRoutingApprovals(); err != nil || !approved {
		t.Fatal(approved, err)
	}
	if err := s.SaveRoutingApproval("radio1", strings.Repeat("a", 64), ""); err != nil {
		t.Fatal(err)
	}
	if approved, err := s.HasRoutingApprovals(); err != nil || approved {
		t.Fatal(approved, err)
	}
	if err := os.WriteFile(filepath.Join(s.Dir, "radio2.json"), []byte(`{broken`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := s.HasRoutingApprovals(); err == nil {
		t.Fatal("damaged preferences hid possibly lost consent")
	}
}

func TestStrictSettingsRefuseAmbiguousDamagedOrUnsafeFilesWithoutWrites(t *testing.T) {
	for _, raw := range []string{
		`null`, `{nope`, `{} {}`, `{"swr_limit":2,"swr_limit":3}`, `{"SWR_LIMIT":2}`,
		`{"ptt_timeout_s":null}`, `{"wsjtx_routing_approval":null}`, `{"wsjtx_routing_approval":"bad"}`,
		`{"WSJTX_ROUTING_APPROVAL":""}`, `{"future_setting":true}`, `{"swr_limit":0}`, strings.Repeat(" ", 16385),
	} {
		t.Run(raw[:min(len(raw), 40)], func(t *testing.T) {
			s := &Store{Dir: t.TempDir()}
			path := filepath.Join(s.Dir, "radio1.json")
			if err := os.WriteFile(path, []byte(raw), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := s.RoutingApproval("radio1"); err == nil {
				t.Fatal("damaged approval became disabled")
			}
			if err := s.SetProbed("radio1", time.Now()); err == nil {
				t.Fatal("probe replaced damaged settings with defaults")
			}
			if err := s.SaveRoutingApproval("radio1", "", strings.Repeat("a", 64)); err == nil {
				t.Fatal("approval replaced damaged settings")
			}
			if got, _ := os.ReadFile(path); string(got) != raw {
				t.Fatal("refusal changed existing bytes")
			}
		})
	}
	for _, kind := range []string{"symlink", "hardlink", "permissions", "directory"} {
		t.Run(kind, func(t *testing.T) {
			s := &Store{Dir: t.TempDir()}
			original := filepath.Join(s.Dir, "original")
			path := filepath.Join(s.Dir, "radio1.json")
			if err := os.WriteFile(original, []byte(`{}`), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "symlink":
				err = os.Symlink(original, path)
			case "hardlink":
				err = os.Link(original, path)
			case "permissions":
				err = os.WriteFile(path, []byte(`{}`), 0666)
			case "directory":
				err = os.Mkdir(path, 0700)
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.ReadStrict("radio1"); err == nil {
				t.Fatal("unsafe settings accepted")
			}
		})
	}
}

func TestStrictSettingsKeepLegacyDefaultsAndGuardReaderIndependent(t *testing.T) {
	s := &Store{Dir: t.TempDir()}
	if value, err := s.ReadStrict("radio1"); err != nil || value != Defaults() {
		t.Fatal(value, err)
	}
	path := filepath.Join(s.Dir, "radio1.json")
	if err := os.WriteFile(path, []byte(`{"swr_limit":2.5,"ptt_timeout_s":90}`), 0600); err != nil {
		t.Fatal(err)
	}
	if value, err := s.ReadStrict("radio1"); err != nil || value.SWRLimit != 2.5 || value.WSJTXRoutingApproval != "" {
		t.Fatal(value, err)
	}
	for _, approval := range []string{`"bad"`, `42`, `true`, `[]`, `null`, `"` + strings.Repeat("a", 64) + `"`} {
		if err := os.WriteFile(path, []byte(`{"swr_limit":2.5,"ptt_timeout_s":90,"wsjtx_routing_approval":`+approval+`}`), 0600); err != nil {
			t.Fatal(err)
		}
		if value := s.Get("radio1"); value.SWRLimit != 2.5 || value.PTTTimeoutS != 90 || value.WSJTXRoutingApproval != "" {
			t.Fatal("collection preference changed guard reads or supplied fallback consent", value)
		}
	}
}
