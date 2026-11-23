package wsjtx

import (
	"bytes"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
)

const settingsExample = "[General]\nkeep=yes\n[%General]\nkeep=literal-group\n[Configuration]\nUDPServer=\"localhost\"\nUDPServerPort=02237\nAcceptUDPRequests=true\nN1MMServerPort=2333\nOpaque=@Variant(\\0\\0\\x7f\\\"keep\\\")\n\n[MultiSettings]\nCurrentName=Default\nOther%20Profile\\Configuration\\UDPServer=127.0.0.2\nOther%20Profile\\Configuration\\UDPServerPort=2238\nOther%20Profile\\OtherKey=keep\nBlank\\MultiSettingsPlaceHolder=@Invalid()\n\n[WSPR%20Band%20Hopping]\nphases\\1\\bands=opaque\n"

func settingsDoc(t *testing.T, data []byte) *SettingsDocument {
	t.Helper()
	d, err := ParseSettingsDocument(data)
	if err != nil {
		t.Fatal(err)
	}
	return d
}

func TestSettingsDocumentReadsCompleteProfileNamespace(t *testing.T) {
	d := settingsDoc(t, []byte(settingsExample))
	s := d.Routing()
	if s.Profile != "Default" || s.Current.Server.Value != `"localhost"` || s.Current.Port.Value != "02237" || len(s.Saved) != 2 || s.Saved["Blank"] != (UDPSettings{}) || s.Saved["Other Profile"].Port.Value != "2238" {
		t.Fatal("complete routing namespace was not retained")
	}
	s.Saved["Blank"] = s.Current
	data := d.Bytes()
	data[0] = 'x'
	if d.Routing().Saved["Blank"] != (UDPSettings{}) || !bytes.Equal(d.Bytes(), []byte(settingsExample)) {
		t.Fatal("document exposed mutable state")
	}
}

func TestSettingsDocumentPatchesOnlyBothCurrentKeys(t *testing.T) {
	_, _, b := observer(t)
	d := settingsDoc(t, []byte(settingsExample))
	l, p, _ := routingClaim(t, b, d.Routing())
	managed, err := d.WithPatch(p)
	if err != nil {
		t.Fatal(err)
	}
	expected := strings.Replace(settingsExample, `UDPServer="localhost"`, "UDPServer=127.0.0.1", 1)
	expected = strings.Replace(expected, "UDPServerPort=02237", "UDPServerPort="+p.Replacement().Port.Value, 1)
	if string(managed.Bytes()) != expected {
		t.Fatal("unrelated bytes changed")
	}
	restore, err := l.Restore(managed.Routing())
	if err != nil {
		t.Fatal(err)
	}
	original, err := managed.WithPatch(restore)
	if err != nil || string(original.Bytes()) != settingsExample {
		t.Fatal("raw round trip changed original", err)
	}
}

func TestSettingsDocumentAddsAndRemovesAbsentKeysExactly(t *testing.T) {
	_, _, b := observer(t)
	for _, original := range []string{
		"[Configuration]\nOther=keep\n\n[MultiSettings]\nCurrentName=Default\n",
		"[MultiSettings]\nCurrentName=Default\n[Configuration]\nOther=keep\n\n",
		"[Configuration]\nUDPServer=\n[MultiSettings]\nCurrentName=Default\n",
	} {
		d := settingsDoc(t, []byte(original))
		l, p, _ := routingClaim(t, b, d.Routing())
		managed, err := d.WithPatch(p)
		if err != nil {
			t.Fatal(err)
		}
		restore, err := l.Restore(managed.Routing())
		if err != nil {
			t.Fatal(err)
		}
		out, err := managed.WithPatch(restore)
		if err != nil || string(out.Bytes()) != original {
			t.Fatal("absent fields did not round trip", err)
		}
	}
}

func TestSettingsDocumentAcceptsRealFirstRunAndPreservesIt(t *testing.T) {
	data, err := os.ReadFile("../../../tests/fixtures/apps/wsjtx.ini")
	if err != nil {
		t.Fatal(err)
	}
	d := settingsDoc(t, data)
	_, _, b := observer(t)
	l, p, _ := routingClaim(t, b, d.Routing())
	managed, err := d.WithPatch(p)
	if err != nil {
		t.Fatal(err)
	}
	restore, err := l.Restore(managed.Routing())
	if err != nil {
		t.Fatal(err)
	}
	original, err := managed.WithPatch(restore)
	if err != nil || !bytes.Equal(original.Bytes(), data) {
		t.Fatal("real settings did not round trip", err)
	}
}

func TestSettingsDocumentRefusesAliasesAndUnsupportedLayouts(t *testing.T) {
	base := "[Configuration]\nUDPServer=first\nUDPServerPort=2237\n[MultiSettings]\nCurrentName=Default\n"
	cases := map[string]string{
		"duplicate-key":             strings.Replace(base, "UDPServer=first", "UDPServer=first\nUDPServer=second", 1),
		"duplicate-section":         base + "[Configuration]\nUDPServer=second\n",
		"percent-key":               strings.Replace(base, "UDPServer=", "UDP%53erver=", 1),
		"unicode-key":               strings.Replace(base, "UDPServer=", "UDP%U0053erver=", 1),
		"encoded-section":           strings.Replace(base, "Configuration", "Con%66iguration", 1),
		"root-alias":                "[General]\nConfiguration\\UDPServer=second\n" + base,
		"root-profile-alias":        "[General]\nMultiSettings\\Other\\Configuration\\UDPServer=second\n" + base,
		"profile-key-alias":         base + "Other\\Configuration\\UDP%53erver=second\n",
		"nested-section":            base + "[MultiSettings\\Other]\nConfiguration\\UDPServer=second\n",
		"duplicate-profile-alias":   base + "Other\\Configuration\\UDPServer=first\nO%74her\\Configuration\\UDPServer=second\n",
		"profile-rename-alias":      strings.Replace(base, "CurrentName=", "Current%4Eame=", 1),
		"whitespace-key":            strings.Replace(base, "UDPServer=", " UDPServer =", 1),
		"header-comment":            strings.Replace(base, "[Configuration]", "[Configuration] ;comment", 1),
		"unterminated":              strings.TrimSuffix(base, "\n"),
		"crlf":                      strings.ReplaceAll(base, "\n", "\r\n"),
		"bom":                       "\xef\xbb\xbf" + base,
		"bad-escape":                base + "Bad%Q0=keep\n",
		"non-ascii-name":            base + "[Non%U00E9]\nx=1\n",
		"continuation":              strings.Replace(base, "UDPServer=first", "Other=first\\\nUDPServer=first", 1),
		"continuation-spaces":       strings.Replace(base, "UDPServer=first", "Other=first\\  \nUDPServer=first", 1),
		"multiline-quotes":          strings.Replace(base, "UDPServer=first", "Other=\"first\nUDPServer=first\nend=\"", 1),
		"missing-profile":           strings.Replace(base, "CurrentName=Default\n", "", 1),
		"missing-config":            "[MultiSettings]\nCurrentName=Default\n",
		"legacy-migration":          "[General]\nCurrentMultiSettingsConfiguration=Other\n" + base,
		"active-also-saved":         base + "Default\\Other=keep\n",
		"quoted-profile":            strings.Replace(base, "CurrentName=Default", "CurrentName=\"Default\"", 1),
		"unknown-profile-metadata":  base + "UnknownMetadata=value\n",
		"profile-empty-component":   base + "Other\\\\OtherKey=keep\n",
		"profile-leading-separator": base + "\\Other\\OtherKey=keep\n",
		"profile-encoded-separator": base + "%2FOther\\OtherKey=keep\n",
		"profile-forward-separator": base + "Other/OtherKey=keep\n",
		"unsupported-port":          strings.Replace(base, "UDPServerPort=2237", "UDPServerPort=\"2237\"", 1),
		"nul":                       base + "Nul=\x00\n",
	}
	for name, data := range cases {
		t.Run(name, func(t *testing.T) {
			if d, err := ParseSettingsDocument([]byte(data)); !errors.Is(err, ErrRoutingLayout) || d != nil {
				t.Fatal("ambiguous layout accepted", err)
			}
		})
	}
}

func TestSettingsDocumentBounds(t *testing.T) {
	for _, data := range [][]byte{
		nil,
		[]byte(strings.Repeat("x", MaxSettingsBytes) + "\n"),
		[]byte(strings.Repeat("\n", maxSettingsLines+1)),
		[]byte("[Configuration]\nOther=" + strings.Repeat("x", maxSettingsLine) + "\n[MultiSettings]\nCurrentName=Default\n"),
	} {
		if _, err := ParseSettingsDocument(data); !errors.Is(err, ErrRoutingLayout) {
			t.Fatal("unbounded input accepted", err)
		}
	}
}

func TestSettingsDocumentRechecksRoutesButPreservesNewPreferences(t *testing.T) {
	_, _, b := observer(t)
	d := settingsDoc(t, []byte(settingsExample))
	_, p, _ := routingClaim(t, b, d.Routing())
	changed := settingsDoc(t, []byte(strings.Replace(settingsExample, "keep=yes", "keep=new-preference", 1)))
	out, err := changed.WithPatch(p)
	if err != nil || !bytes.Contains(out.Bytes(), []byte("keep=new-preference")) {
		t.Fatal("unrelated edit was lost", err)
	}
	changed = settingsDoc(t, []byte(strings.Replace(settingsExample, "UDPServerPort=02237", "UDPServerPort=9876", 1)))
	if _, err := changed.WithPatch(p); !errors.Is(err, ErrRoutingChanged) {
		t.Fatal("routing edit overwritten", err)
	}
}

func FuzzSettingsDocument(f *testing.F) {
	f.Add([]byte(settingsExample))
	f.Add([]byte("[Configuration]\n[MultiSettings]\nCurrentName=Default\n"))
	f.Add([]byte("[General]\nConfiguration\\UDPServer=alias\n" + settingsExample))
	f.Fuzz(func(t *testing.T, data []byte) {
		d, err := ParseSettingsDocument(data)
		if err != nil {
			return
		}
		view := d.Routing()
		if err := view.validate(); err != nil {
			t.Fatal(err)
		}
		p := RoutingPatch{profile: view.Profile, before: view.fingerprint(), after: view.Current}
		out, err := d.WithPatch(p)
		if err != nil || !reflect.DeepEqual(out.Bytes(), data) {
			t.Fatal("identity patch changed verified document", err)
		}
	})
}
