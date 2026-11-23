package wsjtx

import (
	"errors"
	"fmt"
	"net"
	"reflect"
	"strconv"
	"strings"
	"testing"
)

func routingSettings() RoutingSettings {
	return RoutingSettings{
		Profile: "Default",
		Current: UDPSettings{RawSetting{true, `"localhost"`}, RawSetting{true, "02237"}},
		Saved: map[string]UDPSettings{
			"Other": {RawSetting{true, "private-destination"}, RawSetting{true, "2334"}},
		},
	}
}

func routingClaim(t *testing.T, b Binding, s RoutingSettings) (*RoutingLease, RoutingPatch, RoutingSettings) {
	t.Helper()
	preview, err := PreviewRouting(b, s)
	if err != nil {
		t.Fatal(err)
	}
	l, p, err := ClaimRouting(b, s, preview.Fingerprint)
	if err != nil {
		t.Fatal(err)
	}
	managed, err := p.Apply(s)
	if err != nil {
		t.Fatal(err)
	}
	return l, p, managed
}

func TestRoutingAlwaysRequiresTheCurrentPreview(t *testing.T) {
	_, _, b := observer(t)
	for _, pair := range []UDPSettings{{}, {RawSetting{true, ""}, RawSetting{true, ""}}, routingSettings().Current} {
		s := routingSettings()
		s.Current = pair
		before := cloneRouting(s)
		preview, err := PreviewRouting(b, s)
		if err != nil {
			t.Fatal(err)
		}
		if len(preview.Fingerprint) != 64 || preview.Existing != pair || preview.Radio != b.Radio() || preview.Generation != b.Generation() || preview.Profile != s.Profile {
			t.Fatal("preview lost identity or original settings")
		}
		if l, _, err := ClaimRouting(b, s, ""); !errors.Is(err, ErrRoutingApproval) || l != nil {
			t.Fatal("routing was implicitly approved", err)
		}
		if l, _, err := ClaimRouting(b, s, "wrong-preview"); !errors.Is(err, ErrRoutingChanged) || l != nil {
			t.Fatal("wrong preview accepted", err)
		}
		_, patch, managed := routingClaim(t, b, s)
		want := UDPSettings{RawSetting{true, "127.0.0.1"}, RawSetting{true, strconv.Itoa(int(b.Address().Port()))}}
		if managed.Current != want || patch.Replacement() != want || patch.Radio() != b.Radio() || patch.Generation() != b.Generation() || patch.Profile() != s.Profile || patch.ExpectedFingerprint() == "" {
			t.Fatal("route was not derived from exact binding")
		}
		if !reflect.DeepEqual(s, before) || !reflect.DeepEqual(managed.Saved, s.Saved) {
			t.Fatal("claim changed caller or saved profiles")
		}
	}
}

func TestRoutingRestoresBothRawValuesAndAbsence(t *testing.T) {
	_, _, b := observer(t)
	pairs := []UDPSettings{
		{},
		{RawSetting{true, ""}, RawSetting{true, ""}},
		{RawSetting{true, `"localhost"`}, RawSetting{true, "002237"}},
		{RawSetting{}, RawSetting{true, "0"}},
		{RawSetting{true, "private-destination"}, RawSetting{}},
	}
	for _, pair := range pairs {
		s := routingSettings()
		s.Current = pair
		l, _, managed := routingClaim(t, b, s)
		patch, err := l.Restore(managed)
		if err != nil {
			t.Fatal(err)
		}
		restored, err := patch.Apply(managed)
		if err != nil || !reflect.DeepEqual(restored, s) || patch.Replacement() != pair {
			t.Fatal("raw route pair did not round trip", err)
		}
		if managed.Current == pair {
			t.Fatal("restore mutated its input")
		}
	}
}

func routingMutations() map[string]func(*RoutingSettings) {
	return map[string]func(*RoutingSettings){
		"server":         func(s *RoutingSettings) { s.Current.Server = RawSetting{true, "operator-choice"} },
		"port":           func(s *RoutingSettings) { s.Current.Port = RawSetting{true, "9876"} },
		"profile-rename": func(s *RoutingSettings) { s.Profile = "Renamed" },
		"profile-clone":  func(s *RoutingSettings) { s.Saved["Clone"] = s.Current },
		"profile-delete": func(s *RoutingSettings) { delete(s.Saved, "Other") },
		"saved-route":    func(s *RoutingSettings) { s.Saved["Other"] = UDPSettings{} },
		"profile-switch": func(s *RoutingSettings) {
			old := s.Current
			s.Current = s.Saved["Other"]
			delete(s.Saved, "Other")
			s.Saved[s.Profile] = old
			s.Profile = "Other"
		},
	}
}

func TestRoutingApprovalDetectsAllRoutingAndProfileChanges(t *testing.T) {
	_, _, b := observer(t)
	s := routingSettings()
	preview, err := PreviewRouting(b, s)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range routingMutations() {
		t.Run(name, func(t *testing.T) {
			changed := cloneRouting(s)
			mutate(&changed)
			before := cloneRouting(changed)
			if l, _, err := ClaimRouting(b, changed, preview.Fingerprint); !errors.Is(err, ErrRoutingChanged) || l != nil {
				t.Fatal("outdated approval accepted", err)
			}
			if !reflect.DeepEqual(changed, before) {
				t.Fatal("refused claim changed preferences")
			}
		})
	}
}

func TestRoutingRestorationRefusesChangedRoutesOrProfiles(t *testing.T) {
	_, _, b := observer(t)
	l, _, managed := routingClaim(t, b, routingSettings())
	for name, mutate := range routingMutations() {
		t.Run(name, func(t *testing.T) {
			changed := cloneRouting(managed)
			mutate(&changed)
			before := cloneRouting(changed)
			p, err := l.Restore(changed)
			if !errors.Is(err, ErrRoutingConflict) || p.ExpectedFingerprint() != "" {
				t.Fatal("ambiguous restoration produced a patch", err)
			}
			if !reflect.DeepEqual(changed, before) {
				t.Fatal("conflict changed operator preferences")
			}
			if strings.Contains(err.Error(), "private-destination") || strings.Contains(err.Error(), changed.Profile) {
				t.Fatal("error leaked settings contents")
			}
		})
	}
}

func TestRoutingPatchRechecksTheCompleteNamespace(t *testing.T) {
	_, _, b := observer(t)
	l, claim, managed := routingClaim(t, b, routingSettings())
	restore, err := l.Restore(managed)
	if err != nil {
		t.Fatal(err)
	}
	for name, mutate := range routingMutations() {
		t.Run(name, func(t *testing.T) {
			beforeClaim := routingSettings()
			mutate(&beforeClaim)
			if _, err := claim.Apply(beforeClaim); !errors.Is(err, ErrRoutingChanged) {
				t.Fatal("stale acquisition patch applied", err)
			}
			beforeRestore := cloneRouting(managed)
			mutate(&beforeRestore)
			if _, err := restore.Apply(beforeRestore); !errors.Is(err, ErrRoutingChanged) {
				t.Fatal("stale restore patch applied", err)
			}
		})
	}
	if _, err := (RoutingPatch{}).Apply(routingSettings()); !errors.Is(err, ErrRoutingChanged) {
		t.Fatal("zero patch accepted", err)
	}
}

func TestRoutingAlreadyRestoredIsIdempotent(t *testing.T) {
	o, _, b := observer(t)
	s := routingSettings()
	l, _, _ := routingClaim(t, b, s)
	// Restoration is for a stopped program and does not require a live socket.
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		patch, err := l.Restore(s)
		if err != nil {
			t.Fatal(err)
		}
		out, err := patch.Apply(s)
		if err != nil || !reflect.DeepEqual(out, s) {
			t.Fatal("idempotent restore changed original", err)
		}
	}
	if _, err := (*RoutingLease)(nil).Restore(s); !errors.Is(err, ErrRoutingLease) {
		t.Fatal("nil lease accepted", err)
	}
	if _, err := (&RoutingLease{}).Restore(s); !errors.Is(err, ErrRoutingLease) {
		t.Fatal("zero lease accepted", err)
	}
}

func TestRoutingApprovalCannotFollowAReplacedBinding(t *testing.T) {
	o, _, old := observer(t)
	s := routingSettings()
	preview, err := PreviewRouting(old, s)
	if err != nil {
		t.Fatal(err)
	}
	newBinding, err := o.Start(old.Radio(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewRouting(old, s); !errors.Is(err, ErrRoutingBinding) {
		t.Fatal("retired binding previewed", err)
	}
	if _, _, err := ClaimRouting(old, s, preview.Fingerprint); !errors.Is(err, ErrRoutingBinding) {
		t.Fatal("retired binding claimed", err)
	}
	if _, _, err := ClaimRouting(newBinding, s, preview.Fingerprint); !errors.Is(err, ErrRoutingChanged) {
		t.Fatal("old approval followed replacement", err)
	}
	other, err := o.Start("radio2", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := ClaimRouting(other, s, preview.Fingerprint); !errors.Is(err, ErrRoutingChanged) {
		t.Fatal("approval followed another radio", err)
	}
	if _, err := PreviewRouting(Binding{}, s); !errors.Is(err, ErrRoutingBinding) {
		t.Fatal("zero binding accepted", err)
	}
	if !o.Stop(newBinding) {
		t.Fatal("stop failed")
	}
	if _, err := PreviewRouting(newBinding, s); !errors.Is(err, ErrRoutingBinding) {
		t.Fatal("stopped binding previewed", err)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := PreviewRouting(other, s); !errors.Is(err, ErrRoutingBinding) {
		t.Fatal("closed observer previewed", err)
	}
}

func TestRoutingOldLeaseCannotRestoreANewerDestination(t *testing.T) {
	o, _, b := observer(t)
	l, _, _ := routingClaim(t, b, routingSettings())
	newBinding, err := o.Start(b.Radio(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	_, _, current := routingClaim(t, newBinding, routingSettings())
	if _, err := l.Restore(current); !errors.Is(err, ErrRoutingConflict) {
		t.Fatal("old lease adopted a new destination", err)
	}
}

func TestRoutingApprovalExpiresAcrossObserverLifetimes(t *testing.T) {
	o, _, old := observer(t)
	s := routingSettings()
	preview, err := PreviewRouting(old, s)
	if err != nil {
		t.Fatal(err)
	}
	if err := o.Close(); err != nil {
		t.Fatal(err)
	}
	// Use the memory-only fixture constructor to force identical wire identity.
	// Production must instead retain ports with NewPersistentObserver.
	other := NewObserver(nil)
	t.Cleanup(func() { _ = other.Close() })
	other.listen = func(string, *net.UDPAddr) (*net.UDPConn, error) {
		return net.ListenUDP("udp4", net.UDPAddrFromAddrPort(old.Address()))
	}
	fresh, err := other.Start(old.Radio(), fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	if fresh.Generation() != old.Generation() || fresh.Address() != old.Address() {
		t.Fatal("test did not reproduce same endpoint and generation")
	}
	if _, _, err := ClaimRouting(fresh, s, preview.Fingerprint); !errors.Is(err, ErrRoutingChanged) {
		t.Fatal("approval survived observer lifetime", err)
	}
}

func TestRoutingPlansDoNotShareMutableMaps(t *testing.T) {
	_, _, b := observer(t)
	s := routingSettings()
	l, patch, managed := routingClaim(t, b, s)
	s.Saved["Other"] = UDPSettings{}
	if _, err := l.Restore(managed); err != nil {
		t.Fatal("caller mutation changed captured lease", err)
	}
	first, err := patch.Apply(routingSettings())
	if err != nil {
		t.Fatal(err)
	}
	first.Saved["Other"] = UDPSettings{}
	second, err := patch.Apply(routingSettings())
	if err != nil || reflect.DeepEqual(first, second) {
		t.Fatal("returned maps share state", err)
	}
}

func TestRoutingFingerprintsIgnoreMapOrderAndEmptyMapRepresentation(t *testing.T) {
	_, _, b := observer(t)
	a := routingSettings()
	a.Saved["Another"] = UDPSettings{}
	c := cloneRouting(a)
	c.Saved = map[string]UDPSettings{"Another": {}, "Other": a.Saved["Other"]}
	pa, err := PreviewRouting(b, a)
	if err != nil {
		t.Fatal(err)
	}
	pc, err := PreviewRouting(b, c)
	if err != nil {
		t.Fatal(err)
	}
	if pa.Fingerprint != pc.Fingerprint {
		t.Fatal("map iteration affected approval")
	}
	a.Saved = nil
	c.Saved = map[string]UDPSettings{}
	pa, err = PreviewRouting(b, a)
	if err != nil {
		t.Fatal(err)
	}
	pc, err = PreviewRouting(b, c)
	if err != nil {
		t.Fatal(err)
	}
	if pa.Fingerprint != pc.Fingerprint {
		t.Fatal("nil and empty namespace differ")
	}
}

func TestRoutingRejectsPortsAlreadyConfiguredInAnyProfile(t *testing.T) {
	_, _, b := observer(t)
	for _, saved := range []bool{false, true} {
		s := routingSettings()
		pair := UDPSettings{RawSetting{true, "unrelated-host-spelling"}, RawSetting{true, "0" + strconv.Itoa(int(b.Address().Port()))}}
		if saved {
			s.Saved["Other"] = pair
		} else {
			s.Current = pair
		}
		if _, err := PreviewRouting(b, s); !errors.Is(err, ErrRoutingEndpoint) {
			t.Fatal("preexisting destination port admitted", err)
		}
		if _, _, err := ClaimRouting(b, s, "approved"); !errors.Is(err, ErrRoutingEndpoint) {
			t.Fatal("approval bypassed existing destination conflict", err)
		}
	}
}

func TestRoutingRejectsIncompleteOrUnsupportedViews(t *testing.T) {
	_, _, b := observer(t)
	mutations := map[string]func(*RoutingSettings){
		"empty-profile":    func(s *RoutingSettings) { s.Profile = "" },
		"nested-profile":   func(s *RoutingSettings) { s.Profile = "one/two" },
		"escaped-profile":  func(s *RoutingSettings) { s.Profile = `one\two` },
		"long-profile":     func(s *RoutingSettings) { s.Profile = strings.Repeat("x", MaxIDSize+1) },
		"duplicate-active": func(s *RoutingSettings) { s.Saved[s.Profile] = UDPSettings{} },
		"bad-saved-name":   func(s *RoutingSettings) { s.Saved["\x00"] = UDPSettings{} },
		"too-many-profiles": func(s *RoutingSettings) {
			for i := 0; i <= MaxSavedProfiles; i++ {
				s.Saved[fmt.Sprint(i)] = UDPSettings{}
			}
		},
		"absent-value":    func(s *RoutingSettings) { s.Current.Server = RawSetting{false, "not-absent"} },
		"multiline":       func(s *RoutingSettings) { s.Current.Server.Value = "first\nsecond" },
		"carriage-return": func(s *RoutingSettings) { s.Current.Server.Value = "x\ry" },
		"nul-value":       func(s *RoutingSettings) { s.Current.Server.Value = "x\x00y" },
		"utf8-value":      func(s *RoutingSettings) { s.Current.Server.Value = string([]byte{255}) },
		"long-value":      func(s *RoutingSettings) { s.Current.Server.Value = strings.Repeat("x", MaxRoutingValue+1) },
		"long-port":       func(s *RoutingSettings) { s.Current.Port.Value = strings.Repeat("0", MaxRoutingValue+1) },
		"negative-port":   func(s *RoutingSettings) { s.Current.Port.Value = "-1" },
		"large-port":      func(s *RoutingSettings) { s.Current.Port.Value = "65536" },
		"encoded-port":    func(s *RoutingSettings) { s.Current.Port.Value = `"2237"` },
		"saved-invalid":   func(s *RoutingSettings) { s.Saved["Other"] = UDPSettings{Port: RawSetting{true, "bad"}} },
	}
	l, patch, _ := routingClaim(t, b, routingSettings())
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			s := routingSettings()
			mutate(&s)
			if _, err := PreviewRouting(b, s); !errors.Is(err, ErrRoutingLayout) {
				t.Fatal("invalid view previewed", err)
			}
			if _, _, err := ClaimRouting(b, s, "approved"); !errors.Is(err, ErrRoutingLayout) {
				t.Fatal("invalid view claimed", err)
			}
			if _, err := l.Restore(s); !errors.Is(err, ErrRoutingLayout) {
				t.Fatal("invalid view restored", err)
			}
			if _, err := patch.Apply(s); !errors.Is(err, ErrRoutingLayout) {
				t.Fatal("invalid view patched", err)
			}
		})
	}
}

func TestRoutingPortDefaultsAndBoundaries(t *testing.T) {
	for _, tc := range []struct {
		setting RawSetting
		port    uint16
	}{
		{RawSetting{}, 2237}, {RawSetting{true, ""}, 0}, {RawSetting{true, "0"}, 0},
		{RawSetting{true, "00001"}, 1}, {RawSetting{true, "65535"}, 65535},
	} {
		port, ok := configuredPort(tc.setting)
		if !ok || port != tc.port {
			t.Fatal("incorrect Qt port/default interpretation")
		}
	}
}
