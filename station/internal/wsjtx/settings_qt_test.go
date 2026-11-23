package wsjtx

import (
	"encoding/json"
	"os"
	"sort"
	"testing"
)

// TestSettingsQtReferenceHelper is invoked only by the independent PyQt test
// inside a container. Normal Go test runs leave it dormant. It creates no radio
// or program process; the permit stands in for that absent fixture lifecycle.
func TestSettingsQtReferenceHelper(t *testing.T) {
	path := os.Getenv("DXB_QT_SETTINGS_CASE")
	if path == "" {
		return
	}
	resultPath := os.Getenv("DXB_QT_SETTINGS_RESULT")
	result := struct {
		Accepted      bool
		Profile       string
		SavedProfiles []string
		Port          uint16
	}{}
	defer func() {
		data, err := json.Marshal(result)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(resultPath, data, 0600); err != nil {
			t.Fatal(err)
		}
	}()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	d, err := ParseSettingsDocument(data)
	if err != nil {
		return
	}
	h, err := OpenSettingsFile(path)
	if err != nil {
		t.Fatal(err)
	}
	defer h.Close()
	o := NewObserver(nil)
	defer o.Close()
	b, err := o.Start("qt-fixture", fixtureID)
	if err != nil {
		t.Fatal(err)
	}
	l, p, _ := routingClaim(t, b, d.Routing())
	if err := h.Apply(p, testLifecyclePermit); err != nil {
		t.Fatal(err)
	}
	managed, err := h.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".managed", managed.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	restore, err := l.Restore(managed.Routing())
	if err != nil {
		t.Fatal(err)
	}
	if err := h.Apply(restore, testLifecyclePermit); err != nil {
		t.Fatal(err)
	}
	restored, err := h.Read()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path+".restored", restored.Bytes(), 0600); err != nil {
		t.Fatal(err)
	}
	result.Accepted = true
	result.Profile = d.settings.Profile
	result.Port = b.Address().Port()
	result.SavedProfiles = make([]string, 0, len(d.settings.Saved))
	for name := range d.settings.Saved {
		result.SavedProfiles = append(result.SavedProfiles, name)
	}
	sort.Strings(result.SavedProfiles)
}
