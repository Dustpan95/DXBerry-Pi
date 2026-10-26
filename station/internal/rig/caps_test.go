package rig

import (
	"cmp"
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"
)

// fakeRig answers Discover's commands from a table.
type fakeRig struct {
	caps   []string
	levels map[string]Reply // level name -> reply (err derived from RPRT)
	errs   map[string]error // level name -> transport error
	calls  []string
}

func (f *fakeRig) Do(ctx context.Context, cmd string, args ...string) (Reply, error) {
	f.calls = append(f.calls, strings.TrimSpace(cmd+" "+strings.Join(args, " ")))
	switch cmd {
	case "dump_caps":
		return Reply{Lines: f.caps}, nil
	case "get_level":
		name := args[0]
		if err := f.errs[name]; err != nil {
			return Reply{}, err
		}
		r, ok := f.levels[name]
		if !ok {
			return Reply{RPRT: -11}, &RigError{Code: -11}
		}
		if r.RPRT != 0 {
			return r, &RigError{Code: r.RPRT}
		}
		return r, nil
	}
	return Reply{}, errors.New("unexpected command " + cmd)
}

func fixture(t *testing.T, model string) []string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", "caps-"+model+".txt"))
	if err != nil {
		t.Fatal(err)
	}
	return strings.Split(strings.TrimRight(string(b), "\n"), "\n")
}

func hiddenWhy(c Caps, name string) (string, bool) {
	for _, h := range c.Hidden {
		if h.Name == name {
			return h.Why, true
		}
	}
	return "", false
}

func hiddenKind(c Caps, name string) string {
	for _, h := range c.Hidden {
		if h.Name == name {
			return h.Kind
		}
	}
	return ""
}

func allLevels(v string) map[string]Reply {
	m := map[string]Reply{}
	for _, n := range probedLevels {
		m[n] = Reply{Lines: []string{"Level Value: " + v}}
	}
	return m
}

func TestCapsParsesIC7300(t *testing.T) {
	f := &fakeRig{caps: fixture(t, "3073"), levels: allLevels("0.5")}
	c, err := Discover(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if c.ModelName != "IC-7300" {
		t.Errorf("ModelName = %q", c.ModelName)
	}
	for _, m := range []string{"USB", "CW", "PKTUSB"} {
		if !c.Allows(m) {
			t.Errorf("mode %s not allowed; modes %v", m, c.Modes)
		}
	}
	if c.Allows("usb") || c.Allows("WFM") {
		t.Error("Allows must be exact and case-sensitive")
	}
	for _, l := range []string{"RFPOWER", "AF"} {
		if !slices.Contains(c.SettableLevels, l) {
			t.Errorf("settable levels lack %s", l)
		}
	}
	if !c.CanSetSplit || !c.CanGetSplit || !c.CanSetPTT || !c.CanGetPTT {
		t.Errorf("split/ptt flags wrong: %+v", c)
	}
	if !slices.Equal(c.VFOs, []string{"VFOA", "VFOB", "MEM"}) {
		t.Errorf("VFOs = %v", c.VFOs)
	}
	if !slices.Contains(c.SettableFuncs, "TUNER") {
		t.Errorf("SettableFuncs lacks TUNER: %v", c.SettableFuncs)
	}
	if len(c.FreqRanges) == 0 || c.FreqRanges[0] != (FreqRange{Lo: 30000, Hi: 74800000}) {
		t.Errorf("FreqRanges = %v", c.FreqRanges)
	}
	if !c.InRange(14074000) || !c.InRange(30000) || !c.InRange(74800000) || c.InRange(29999) || c.InRange(74800001) {
		t.Error("InRange bounds wrong")
	}
	if !slices.Contains(c.ReadableMeters, "SWR") || !slices.Contains(c.ReadableMeters, "STRENGTH") {
		t.Errorf("ReadableMeters = %v", c.ReadableMeters)
	}
	if time.Since(c.Probed) > time.Minute || c.Probed.IsZero() {
		t.Errorf("Probed = %v", c.Probed)
	}
	if len(c.Hidden) != 0 {
		t.Errorf("IC-7300 with every level readable should hide nothing: %v", c.Hidden)
	}
}

func TestCapsParseOtherFixtures(t *testing.T) {
	ts := parseCaps(fixture(t, "2031"))
	if ts.ModelName != "TS-590S" || !ts.Allows("PKTUSB") || !ts.CanSetVFO || !ts.CanSetSplit {
		t.Errorf("TS-590S parse: %+v", ts)
	}
	if len(ts.FreqRanges) == 0 || !slices.IsSortedFunc(ts.FreqRanges, func(a, b FreqRange) int {
		return cmp.Or(cmp.Compare(a.Lo, b.Lo), cmp.Compare(a.Hi, b.Hi))
	}) {
		t.Errorf("TS-590S ranges: %v", ts.FreqRanges)
	}
	ft := parseCaps(fixture(t, "1036"))
	if ft.ModelName != "FT-891" || !ft.Allows("FMN") || ft.Allows("WFM") || ft.CanSetVFO {
		t.Errorf("FT-891 parse: %+v", ft)
	}
	// RX #1 and #2 are identical in the fixture; the TX ranges must not leak in.
	if want := []FreqRange{{30000, 470000000}}; !slices.Equal(ft.FreqRanges, want) {
		t.Errorf("FT-891 ranges = %v, want %v", ft.FreqRanges, want)
	}
	if !ft.InRange(7100000) {
		t.Error("FT-891 should cover 40 m")
	}
}

func TestCapsDummyRigHidesEverythingButPTT(t *testing.T) {
	f := &fakeRig{caps: fixture(t, "1"), levels: allLevels("0.5")}
	c, err := Discover(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if c.ModelName != "Dummy" {
		t.Errorf("ModelName = %q", c.ModelName)
	}
	if want := parseCaps(fixture(t, "1")).SettableLevels; len(want) == 0 || !slices.Equal(c.SettableLevels, want) {
		t.Errorf("SettableLevels = %v, want the fixture's %v", c.SettableLevels, want)
	}
	const why = "rig control is Hamlib's dummy model: this radio has no CAT"
	kinds := map[string]string{"STRENGTH": KindMeter, "SWR": KindMeter, "ALC": KindMeter, "RFPOWER": KindMeter,
		"RFPOWER_METER_WATTS": KindMeter, "AF": KindMeter, "RF": KindMeter, "SQL": KindMeter, "SPLIT": KindControl, "VFO": KindControl}
	for n, k := range kinds {
		got, ok := hiddenWhy(c, n)
		if !ok || got != why {
			t.Errorf("%s: hidden=%v why=%q", n, ok, got)
		}
		if hiddenKind(c, n) != k {
			t.Errorf("%s: kind %q, want %q", n, hiddenKind(c, n), k)
		}
	}
	if len(c.ReadableMeters) != 0 {
		t.Errorf("dummy has readable meters %v", c.ReadableMeters)
	}
	if _, ok := hiddenWhy(c, "PTT"); ok {
		t.Error("PTT must not be hidden on the dummy")
	}
	if !c.CanSetPTT {
		t.Error("dummy can set PTT")
	}
	for _, call := range f.calls {
		if strings.HasPrefix(call, "get_level") {
			t.Errorf("dummy must not be probed: %s", call)
		}
	}
}

func TestDiscoverProbesLevels(t *testing.T) {
	lv := allLevels("0.5")
	lv["SWR"] = Reply{RPRT: -11}
	lv["STRENGTH"] = Reply{Lines: []string{"Level Value: -12"}}
	lv["ALC"] = Reply{Lines: []string{"Level Value: not-a-number"}} // RPRT 0 but no number
	f := &fakeRig{caps: fixture(t, "3073"), levels: lv}
	c, err := Discover(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(c.ReadableMeters, "STRENGTH") {
		t.Errorf("STRENGTH should be readable: %v", c.ReadableMeters)
	}
	if slices.Contains(c.ReadableMeters, "SWR") || slices.Contains(c.ReadableMeters, "ALC") {
		t.Errorf("SWR/ALC must not be readable: %v", c.ReadableMeters)
	}
	why, ok := hiddenWhy(c, "SWR")
	if !ok || why != "this radio does not report SWR over CAT" {
		t.Errorf("SWR hidden=%v why=%q", ok, why)
	}
	if hiddenKind(c, "SWR") != KindMeter {
		t.Errorf("SWR kind = %q", hiddenKind(c, "SWR"))
	}
	if _, ok := hiddenWhy(c, "ALC"); !ok {
		t.Error("ALC with a non-numeric value must be hidden")
	}
	if _, ok := hiddenWhy(c, "STRENGTH"); ok {
		t.Error("STRENGTH must not be hidden")
	}
	want := []string{"get_level STRENGTH", "get_level SWR", "get_level ALC", "get_level RFPOWER", "get_level RFPOWER_METER_WATTS", "get_level AF", "get_level RF", "get_level SQL"}
	var got []string
	for _, call := range f.calls {
		if strings.HasPrefix(call, "get_level") {
			got = append(got, call)
		}
	}
	if !slices.Equal(got, want) {
		t.Errorf("probe calls = %v", got)
	}
}

func TestDiscoverHidesControlsTheCapsCannotSet(t *testing.T) {
	// FT-891 caps cannot set SQL? use a trimmed caps text instead of guessing.
	caps := []string{
		"Model name:\tToy",
		"Mode list: USB LSB",
		"VFO list: VFOA",
		"Set level: AF(0.000000..1.000000/0.1)",
		"Can set Split VFO:\tN",
		"Can set PTT:\tY",
	}
	f := &fakeRig{caps: caps, levels: allLevels("1")}
	c, err := Discover(context.Background(), f)
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range []string{"RFPOWER", "RF", "SQL", "SPLIT", "VFO"} {
		why, ok := hiddenWhy(c, n)
		if !ok || why != "this radio cannot set "+n+" over CAT" {
			t.Errorf("%s hidden=%v why=%q", n, ok, why)
		}
		if hiddenKind(c, n) != KindControl {
			t.Errorf("%s kind = %q", n, hiddenKind(c, n))
		}
	}
	if _, ok := hiddenWhy(c, "AF"); ok {
		t.Error("AF is settable and must not be hidden")
	}
}

func TestDiscoverHidesVFOWhenCannotSetVFO(t *testing.T) {
	caps := []string{
		"Model name:\tToy",
		"Mode list: USB",
		"VFO list: VFOA VFOB MEM",
		"Set level: AF(0..1/0.1) RF(0..1/0.1) SQL(0..1/0.1) RFPOWER(0..1/0.1)",
		"Can set VFO:\tN",
		"Can set Split VFO:\tY",
	}
	c, err := Discover(context.Background(), &fakeRig{caps: caps, levels: allLevels("1")})
	if err != nil {
		t.Fatal(err)
	}
	if why, ok := hiddenWhy(c, "VFO"); !ok || why != "this radio cannot set VFO over CAT" {
		t.Errorf("VFO hidden=%v why=%q", ok, why)
	}
	caps[4] = "Can set VFO:\tY"
	c, err = Discover(context.Background(), &fakeRig{caps: caps, levels: allLevels("1")})
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := hiddenWhy(c, "VFO"); ok {
		t.Error("VFO with Can set VFO Y and VFOA/VFOB must be shown")
	}
}

func TestDiscoverAbortsOnTransportError(t *testing.T) {
	for _, e := range []error{ErrDeadline, ErrProtocol} {
		f := &fakeRig{caps: fixture(t, "3073"), levels: allLevels("1"), errs: map[string]error{"ALC": e}}
		if _, err := Discover(context.Background(), f); !errors.Is(err, e) {
			t.Errorf("err = %v, want %v", err, e)
		}
	}
}

// Only "invalid", "not implemented", "rejected" and "not available" mean the radio cannot
// report a level; a timeout, an I/O or protocol error, or any other code is not a fact about
// the radio and must not be cached for a week.
func TestDiscoverRigErrorsThatAreFactsAndThatAreNot(t *testing.T) {
	for _, code := range []int{-1, -4, -9, -11} {
		lv := allLevels("1")
		lv["SWR"] = Reply{RPRT: code}
		c, err := Discover(context.Background(), &fakeRig{caps: fixture(t, "3073"), levels: lv})
		if err != nil {
			t.Fatalf("RPRT %d: %v", code, err)
		}
		if why, ok := hiddenWhy(c, "SWR"); !ok || why != "this radio does not report SWR over CAT" {
			t.Fatalf("RPRT %d: SWR hidden=%v why=%q", code, ok, why)
		}
	}
	for _, code := range []int{-5, -6, -8, -2, -3, -7, -10, -12} {
		lv := allLevels("1")
		lv["SWR"] = Reply{RPRT: code}
		_, err := Discover(context.Background(), &fakeRig{caps: fixture(t, "3073"), levels: lv})
		var re *RigError
		if err == nil || !errors.As(err, &re) || re.Code != code {
			t.Fatalf("RPRT %d: err = %v, want discovery to fail with it", code, err)
		}
	}
}

func TestCapsCacheRoundTripAndAge(t *testing.T) {
	dir := t.TempDir()
	cc := CapsCache{Dir: dir}
	if _, ok := cc.Load("3073"); ok {
		t.Fatal("empty cache must miss")
	}
	c := parseCaps(fixture(t, "3073"))
	c.Probed = time.Now()
	if err := cc.Store("3073", c); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(dir, "caps-3073.json")
	st, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	if st.Mode().Perm() != 0o644 {
		t.Errorf("mode = %v", st.Mode().Perm())
	}
	got, ok := cc.Load("3073")
	if !ok || got.ModelName != "IC-7300" || !slices.Equal(got.Modes, c.Modes) || !slices.Equal(got.FreqRanges, c.FreqRanges) {
		t.Fatalf("round trip failed: ok=%v %+v", ok, got)
	}
	old := time.Now().Add(-8 * 24 * time.Hour)
	if err := os.Chtimes(p, old, old); err != nil {
		t.Fatal(err)
	}
	if _, ok := cc.Load("3073"); ok {
		t.Error("file older than a week must miss")
	}
	c.Probed = old
	if err := cc.Store("3073", c); err != nil {
		t.Fatal(err)
	}
	if _, ok := cc.Load("3073"); ok {
		t.Error("Probed older than a week must miss")
	}
	c.Probed = time.Now().Add(time.Hour)
	if err := cc.Store("3073", c); err != nil {
		t.Fatal(err)
	}
	if _, ok := cc.Load("3073"); ok {
		t.Error("Probed in the future must miss")
	}
	if err := os.WriteFile(p, []byte("{not json"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, ok := cc.Load("3073"); ok {
		t.Error("unreadable file must miss")
	}
	if _, ok := cc.Load("../etc/passwd"); ok {
		t.Error("bad model must miss")
	}
	if err := cc.Store("../x", c); err == nil {
		t.Error("bad model must not store")
	}
}
