package radios

import (
	"crypto/sha256"
	"encoding/json"
	"regexp"
	"strconv"
	"strings"
)

// ControlPin is a declared USB selector, not evidence of a present/open device.
// Missing identity fields remain unknown; a known empty serial is distinct.
// Private selectors must never be added to browser state or logs.
type ControlPin struct {
	path, vidpid, serial     string
	vidpidKnown, serialKnown bool
}

func (p ControlPin) Path() string           { return p.path }
func (p ControlPin) USBID() (string, bool)  { return p.vidpid, p.vidpidKnown }
func (p ControlPin) Serial() (string, bool) { return p.serial, p.serialKnown }

// RigConfiguration is the separately validated control subset of a fresh
// Registration. It is immutable private evidence; it creates no radio route.
type RigConfiguration struct {
	valid                       bool
	radio, control, method, ptt string
	model, port, baud, gpio     int
	gpioSet                     bool
	cat, serial, hid            ControlPin
}

// RigCommand describes the exact existing provisioning argument vector and
// only the pins those arguments use. It is not a command runner or permission.
type RigCommand struct {
	valid            bool
	radio, arguments string
	port             int
	cat, serial, hid ControlPin
}

func (c RigCommand) RadioName() string             { return c.radio }
func (c RigCommand) Port() int                     { return c.port }
func (c RigCommand) CAT() (ControlPin, bool)       { return c.cat, c.valid && c.cat.path != "" }
func (c RigCommand) SerialPTT() (ControlPin, bool) { return c.serial, c.valid && c.serial.path != "" }
func (c RigCommand) HID() (ControlPin, bool)       { return c.hid, c.valid && c.hid.path != "" }

// Arguments returns a value copy. Do not execute it: this is read-only evidence
// for comparing the already running service, not a lifecycle/control helper.
func (c RigCommand) Arguments() []string {
	if !c.valid {
		return nil
	}
	return strings.Split(strings.TrimSuffix(c.arguments, "\x00"), "\x00")
}

func (c RigCommand) MatchesArgumentsDigest(digest [32]byte) bool {
	return c.valid && sha256.Sum256([]byte(c.arguments)) == digest
}

// Rigctld follows dxb_rigctld_env's existing routing rules. ownerKind must come
// from fresh trusted program-module classification (none/graywolf/session),
// not from a request or an assumption that a stopped program is unassigned.
// This comparison does not establish program-free state or physical identity.
func (c RigConfiguration) Rigctld(ownerKind string) (RigCommand, bool) {
	if !c.valid || ownerKind != "none" && ownerKind != "graywolf" && ownerKind != "session" {
		return RigCommand{}, false
	}
	result := RigCommand{valid: true, radio: c.radio, port: c.port}
	model := c.model
	catPath, serialPath := "", ""
	if c.cat.path != "" {
		catPath = "/dev/dxberry/" + c.radio + "-cat"
	}
	if c.serial.path != "" {
		serialPath = "/dev/dxberry/" + c.radio + "-ptt"
	}
	var rigArgs, pttArgs []string
	if catPath != "" {
		rigArgs = []string{"-r", catPath, "-s", strconv.Itoa(c.baud)}
		result.cat = c.cat
	}
	if c.control == "flrig" {
		model = 4
		rigArgs = []string{"-r", "127.0.0.1:" + strconv.Itoa(12345+(c.port-4532)/2)}
		result.cat = ControlPin{}
		if serialPath == "" {
			catPath = ""
		}
	}
	switch c.ptt {
	case "RIG":
		pttArgs = []string{"-P", "RIG"}
	case "RTS", "DTR":
		pin := serialPath
		if pin == "" {
			pin = catPath
		}
		if pin != "" {
			pttArgs = []string{"-P", c.ptt, "-p", pin}
		}
	}
	if ownerKind == "session" {
		switch c.method {
		case "cm108":
			pttArgs = nil
			if c.hid.path != "" {
				pttArgs = []string{"-P", "CM108", "-p", "/dev/dxberry/" + c.radio + "-hid"}
			}
		case "gpio":
			pttArgs = nil
			if c.gpioSet {
				pttArgs = []string{"-P", "GPIO", "-p", strconv.Itoa(c.gpio)}
			}
		}
	}
	// Record only selectors actually referenced by the final argument vector.
	for _, argument := range pttArgs {
		switch argument {
		case "/dev/dxberry/" + c.radio + "-cat":
			result.cat = c.cat
		case "/dev/dxberry/" + c.radio + "-ptt":
			result.serial = c.serial
		case "/dev/dxberry/" + c.radio + "-hid":
			result.hid = c.hid
		}
	}
	args := []string{"/usr/bin/rigctld", "-m", strconv.Itoa(model), "-T", "127.0.0.1", "-t", strconv.Itoa(c.port)}
	args = append(args, rigArgs...)
	args = append(args, pttArgs...)
	result.arguments = strings.Join(args, "\x00") + "\x00"
	return result, true
}

var controlPath = regexp.MustCompile(`^usb-[0-9]+:[0-9.]+:[0-9]+\.[0-9]+$`)
var controlUSBID = regexp.MustCompile(`^[0-9a-f]{4}:[0-9a-f]{4}$`)

func controlString(obj map[string]json.RawMessage, key string) (string, bool) {
	var s string
	raw, exists := obj[key]
	if !exists || string(raw) == "null" || json.Unmarshal(raw, &s) != nil {
		return "", false
	}
	return s, true
}

func controlInteger(obj map[string]json.RawMessage, key string) (int, bool) {
	var n int
	raw, exists := obj[key]
	if !exists || string(raw) == "null" || json.Unmarshal(raw, &n) != nil || n < 0 || n > 2147483647 {
		return 0, false
	}
	return n, true
}

func parseControlPin(raw json.RawMessage) (ControlPin, bool) {
	var p ControlPin
	if len(raw) == 0 {
		return p, false
	}
	if string(raw) == "null" {
		return p, true
	}
	obj, err := registrationObject(raw, "path", "vidpid", "serial")
	if err != nil {
		return p, false
	}
	var ok bool
	p.path, ok = controlString(obj, "path")
	if !ok || len(p.path) > 128 || !controlPath.MatchString(p.path) {
		return ControlPin{}, false
	}
	if value, exists := obj["vidpid"]; exists && string(value) != "null" {
		p.vidpid, ok = controlString(obj, "vidpid")
		if !ok || !controlUSBID.MatchString(p.vidpid) {
			return ControlPin{}, false
		}
		p.vidpidKnown = true
	}
	if value, exists := obj["serial"]; exists && string(value) != "null" {
		p.serial, ok = controlString(obj, "serial")
		if !ok || len(p.serial) > 512 {
			return ControlPin{}, false
		}
		for _, r := range p.serial {
			if r < 32 || r == 127 {
				return ControlPin{}, false
			}
		}
		p.serialKnown = true
	}
	return p, true
}

// Called only after the bounded encoding/schema/duplicate-key registration
// validation. Unknown extension metadata stays in the registration identity.
func parseRigConfiguration(raw []byte, radio Radio) (RigConfiguration, bool) {
	var none RigConfiguration
	root, err := registrationObject(raw, "version", "radios")
	if err != nil {
		return none, false
	}
	all, err := registrationObject(root["radios"])
	if err != nil {
		return none, false
	}
	obj, err := registrationObject(all[radio.Name], "rig", "ptt", "cat", "ptt_serial", "hid")
	if err != nil {
		return none, false
	}
	rig, err := registrationObject(obj["rig"], "model", "control", "baud", "ptt_type")
	if err != nil {
		return none, false
	}
	ptt, err := registrationObject(obj["ptt"], "method", "gpio_line")
	if err != nil {
		return none, false
	}
	c := RigConfiguration{radio: radio.Name, model: radio.Model, port: radio.Port, control: radio.Control, method: radio.PTT}
	if c.model < 1 || c.model > 2147483647 {
		return none, false
	}
	if c.control == "" {
		c.control = "hamlib"
	}
	var ok bool
	c.baud, ok = controlInteger(rig, "baud")
	if !ok {
		return none, false
	}
	c.ptt, ok = controlString(rig, "ptt_type")
	if !ok || c.ptt != "NONE" && c.ptt != "RIG" && c.ptt != "RTS" && c.ptt != "DTR" {
		return none, false
	}
	if value, exists := ptt["gpio_line"]; !exists {
		return none, false
	} else if string(value) != "null" {
		c.gpio, ok = controlInteger(ptt, "gpio_line")
		if !ok {
			return none, false
		}
		c.gpioSet = true
	}
	c.cat, ok = parseControlPin(obj["cat"])
	if !ok {
		return none, false
	}
	c.serial, ok = parseControlPin(obj["ptt_serial"])
	if !ok {
		return none, false
	}
	c.hid, ok = parseControlPin(obj["hid"])
	if !ok {
		return none, false
	}
	c.valid = true
	return c, true
}
