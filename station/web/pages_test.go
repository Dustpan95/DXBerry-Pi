package web

import (
	"strings"
	"testing"
)

func page(t *testing.T, name string) string {
	t.Helper()
	b, err := Files.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// Settings must not offer CONSOLE: 0.5.0 ignores it.
func TestSettingsDoesNotOfferConsole(t *testing.T) {
	s := page(t, "settings.js")
	for _, bad := range []string{"CONSOLE"} {
		if strings.Contains(s, bad) {
			t.Errorf("settings.js still has %s", bad)
		}
	}
}

// No user-visible text names Cockpit (comments may).
func TestUpdatesTextDoesNotNameCockpit(t *testing.T) {
	for _, l := range strings.Split(page(t, "updates.js"), "\n") {
		if strings.Contains(l, "Cockpit") && !strings.HasPrefix(strings.TrimSpace(l), "//") {
			t.Errorf("updates.js: %s", strings.TrimSpace(l))
		}
	}
}

// The login redirect keeps the hash, and an events socket that closes asks whether the session
// is gone; the restart flow waits for the Pi and then logs in again.
func TestPagesRecoverFromEndedSessions(t *testing.T) {
	app := page(t, "app.js")
	if !strings.Contains(app, "location.pathname + location.search + location.hash") {
		t.Error("app.js: the login redirect drops location.hash")
	}
	if !strings.Contains(app, `"/api/auth/check"`) || !strings.Contains(app, "toLogin()") {
		t.Error("app.js: a closed events socket never checks the session")
	}
	d := page(t, "dxberry.js")
	if !strings.Contains(d, `ex.problem === "unreachable"`) || !strings.Contains(d, `"/api/version"`) || !strings.Contains(d, "dxb.toLogin()") {
		t.Error("dxberry.js: Restart the Pi does not wait for the Pi and log in again")
	}
	if strings.Contains(d, `"disconnected"`) {
		t.Error("dxberry.js still waits for a Cockpit problem name")
	}
}
