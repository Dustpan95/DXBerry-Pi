package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// Every argument vector the pages in station/web can send, one per dxb.run call site, built the
// way the page builds it (radioFormArgs and cfgFormArgs in dxberry.js and settings.js, the rest
// inline). A vector the page can produce must pass the allowlist, or the person sees a refusal
// they cannot act on.
func pageVectors() []Call {
	r := func(a ...string) Call { return Call{Command: "dxberry-radio", Args: append(a, "--json")} }
	cfg := func(stdin string, a ...string) Call {
		return Call{Command: "dxberry-config", Args: append(a, "--json"), Stdin: stdin}
	}
	v := []Call{
		// dxberry.js: status, models, radioAction, radio form
		{Command: "dxberry-status", Args: []string{"--json"}},
		r("models"),
		r("claim", "radio1", "wsjtx"),
		r("release", "radio1"),
		r("remove", "radio1"),
		// radioFormArgs, Add: every field, a blank label (the form's default), then labels with punctuation
		r("add", "radio1", "--audio", "none", "--cat", "none", "--model", "1", "--baud", "0", "--ptt", "none", "--ptt-type", "NONE", "--wiring", "full", "--label", ""),
		r("add", "ic705", "--audio", "1-1.4:1.0", "--cat", "1-1.4:1.1", "--model", "3085", "--baud", "19200", "--ptt", "rigctld", "--ptt-type", "RIG", "--wiring", "full", "--label", "IC-705 (portable)"),
		r("add", "radio2", "--audio", "1-1.2", "--cat", "1-1.3", "--ptt-serial", "1-1.5", "--hid", "1-1.6", "--model", "1035", "--baud", "38400", "--ptt", "gpio", "--ptt-type", "NONE", "--gpio-line", "17", "--wiring", "names", "--label", "Bob's rig"),
		r("add", "radio3", "--audio", "none", "--cat", "none", "--hid", "1-1.6", "--model", "1", "--baud", "0", "--ptt", "cm108", "--ptt-type", "NONE", "--wiring", "full", "--label", "FT-991A #2"),
		r("add", "radio4", "--audio", "none", "--cat", "none", "--model", "1", "--baud", "0", "--ptt", "none", "--ptt-type", "NONE", "--wiring", "full", "--label", "TM-V71 2m/70cm \"mobile\" & <b> 100% [a]_b"),
		r("add", "radio5", "--audio", "none", "--cat", "none", "--model", "1", "--baud", "0", "--ptt", "none", "--ptt-type", "NONE", "--wiring", "full", "--label", strings.Repeat("x", 40)),
		// radioFormArgs, Edit: only what changed; a cleared label is --label ""
		r("set", "radio1", "--label", ""),
		r("set", "radio1", "--label", "FT-991A #2"),
		r("set", "radio1", "--audio", "1-1.4", "--cat", "1-1.5", "--ptt-serial", "1-1.6", "--hid", "1-1.7", "--model", "3085", "--baud", "9600", "--ptt", "rigctld", "--ptt-type", "RIG", "--wiring", "names", "--rig-control", "flrig"),
		r("set", "radio1", "--ptt", "gpio", "--gpio-line", "27"),
		// dashboard.js, programs.js, screen.js
		{Command: "dxberry-app", Args: []string{"start", "radio1", "--json"}},
		{Command: "dxberry-app", Args: []string{"stop", "radio1", "--json"}},
		{Command: "dxberry-app", Args: []string{"install", "wsjtx", "--json"}},
		{Command: "dxberry-app", Args: []string{"remove", "wsjtx", "--json"}},
		{Command: "dxberry-app", Args: []string{"screen", "wsjtx-radio1", "--json"}},
		{Command: "dxberry-update", Args: []string{"app", "wsjtx", "--json"}},
		{Command: "dxberry-logbook", Args: []string{"test", "--json"}, Stdin: "{}"},
		{Command: "dxberry-logbook", Args: []string{"set", "--json"}, Stdin: "{}"},
		{Command: "dxberry-logbook", Args: []string{"set", "--json"}, Stdin: `{"clear":true}`},
		// updates.js
		{Command: "dxberry-update", Args: []string{"check", "--json"}},
		{Command: "dxberry-update", Args: []string{"check", "--refresh", "--json"}},
		{Command: "dxberry-update", Args: []string{"job", "--json"}},
		{Command: "dxberry-update", Args: []string{"graywolf", "--json"}},
		{Command: "dxberry-update", Args: []string{"dxberry", "--json"}},
		{Command: "dxberry-update", Args: []string{"system", "--json"}},
		{Command: "dxberry-update", Args: []string{"rollback", "--json"}},
		{Command: "dxberry-update", Args: []string{"prereleases", "on", "--json"}},
		{Command: "dxberry-update", Args: []string{"prereleases", "off", "--json"}},
		// settings.js: cfgRun
		cfg("", "get"), cfg("", "job"), cfg("", "confirm"), cfg("", "revert"),
		// cfgFormArgs, network: static, then DHCP clearing the fixed address, WiFi with a password on stdin
		cfg("", "set", "STATIC_IP=192.168.1.90/24", "GATEWAY=192.168.1.1", "DNS=1.1.1.1,8.8.8.8", "WIFI_SSID=My Net-2.4", "WIFI_COUNTRY=US"),
		cfg("", "set", "STATIC_IP=", "GATEWAY=", "DNS="),
		cfg("WIFI_PASSWORD=hunter2 pass!\n", "set", "WIFI_SSID=Home", "--stdin"),
		cfg("WIFI_PASSWORD=x\n", "set", "--stdin"),
		// cfgFormArgs, system
		cfg("", "set", "HOSTNAME=dxberry", "TIMEZONE=America/Chicago", "GPS_DEVICE=/dev/ttyACM0", "GPS_BAUD=9600", "GPS_PPS=18", "POSITION_LOG=on"),
		cfg("", "set", "GPS_PPS=", "SSH_PUBKEY=", "GPS_DEVICE=auto"),
		cfg("", "set", "SSH_PUBKEY=ssh-ed25519 AAAAC3NzaC1lZDI1NTE5AAAAIabc you@laptop"),
		cfg("PASSWORD=correct horse\n", "set", "HOSTNAME=dxberry", "--stdin"),
		// dxberry.js: unitAction, power; app.js: the log panel
		{Command: "systemctl", Args: []string{"restart", "graywolf.service"}},
		{Command: "systemctl", Args: []string{"start", "rigctld@radio1.service"}},
		{Command: "systemctl", Args: []string{"stop", "dxberry-screen@wsjtx-radio1.service"}},
		{Command: "systemctl", Args: []string{"restart", "dxberryd.service"}},
		{Command: "systemctl", Args: []string{"--no-block", "start", "reboot.target"}},
		{Command: "systemctl", Args: []string{"--no-block", "start", "poweroff.target"}},
		{Command: "journalctl", Args: []string{"-u", "dxberryd.service", "-n", "300", "--no-pager", "-o", "short-iso"}},
		{Command: "journalctl", Args: []string{"-u", "rigctld@radio1.service", "-n", "300", "--no-pager", "-o", "short-iso"}},
	}
	return v
}

func TestPageVectorsValidate(t *testing.T) {
	c := newTest(t)
	for _, call := range pageVectors() {
		if err := c.Validate(call); err != nil {
			t.Errorf("%s %q refused: %v", call.Command, call.Args, err)
		}
	}
}

func TestRadioLabelRules(t *testing.T) {
	c := newTest(t)
	for _, bad := range []string{"--label", "--x", strings.Repeat("x", 41), "tab\there", "café", "new\nline"} {
		for _, args := range [][]string{
			{"add", "radio1", "--audio", "none", "--label", bad, "--json"},
			{"set", "radio1", "--label", bad, "--json"},
		} {
			if c.Validate(Call{Command: "dxberry-radio", Args: args}) == nil {
				t.Errorf("label %q accepted in %v", bad, args)
			}
		}
	}
	// the other options keep the stricter value pattern: empty and odd characters stay refused
	for _, args := range [][]string{
		{"add", "radio1", "--audio", "", "--json"},
		{"add", "radio1", "--cat", "a#b", "--json"},
		{"set", "radio1", "--ptt", "'x'", "--json"},
	} {
		if c.Validate(Call{Command: "dxberry-radio", Args: args}) == nil {
			t.Errorf("%v accepted", args)
		}
	}
}

func TestRefusalNeverEchoesAnEqualsValue(t *testing.T) {
	c := newTest(t)
	for _, call := range []Call{
		{Command: "dxberry-radio", Args: []string{"remove", "PASSWORD=hunter2", "--json"}},
		{Command: "dxberry-app", Args: []string{"install", "tok=abc", "--json"}},
		{Command: "dxberry-app", Args: []string{"screen", "a=b-c", "--json"}},
		{Command: "systemctl", Args: []string{"start", "a=secret.service"}},
	} {
		err := c.Validate(call)
		if err == nil {
			t.Fatalf("%v accepted", call)
		}
		if strings.Contains(err.Error(), "=") {
			t.Errorf("%v: refusal echoes the value: %v", call.Args, err)
		}
	}
}

// The unit pattern lives twice, in cli.go and in web/app.js; they must stay the same text.
func TestUnitPatternMatchesThePage(t *testing.T) {
	js, err := os.ReadFile("../../web/app.js")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`const UNIT = /(.+)/;`).FindSubmatch(js)
	if m == nil {
		t.Fatal("no UNIT pattern in app.js")
	}
	if string(m[1]) != reUnit.String() {
		t.Fatalf("app.js UNIT = %s\ncli.go reUnit = %s", m[1], reUnit.String())
	}
}
