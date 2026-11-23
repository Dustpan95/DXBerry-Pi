package radios

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"os"
	"os/exec"
	"reflect"
	"strings"
	"testing"
	"time"
)

var rigctldFixture = flag.String("rigctld-fixture", "", "explicit read-only provisioning script for command parity checks")

const rigConfigurationFixture = `{"version":1,"radios":{"radio1":{"label":"Fixture","owner":"","rigctld_port":4532,"rig":{"model":3073,"baud":115200,"ptt_type":"RIG","control":"hamlib"},"ptt":{"method":"rigctld","gpio_line":null},"cat":{"path":"usb-0:1.3:1.0","vidpid":"1234:abcd","serial":"example"},"ptt_serial":null,"hid":null}}}`

func configurationDocument(t *testing.T, change func(map[string]any)) []byte {
	t.Helper()
	var root map[string]any
	if err := json.Unmarshal([]byte(rigConfigurationFixture), &root); err != nil {
		t.Fatal(err)
	}
	r := root["radios"].(map[string]any)["radio1"].(map[string]any)
	if change != nil {
		change(r)
	}
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func configurationRead(t *testing.T, raw []byte) (Registration, RigConfiguration, bool) {
	t.Helper()
	r, path := registrationReaderFixture(t)
	write(t, path, string(raw))
	registration, err := r.Read(context.Background(), "radio1")
	if err != nil {
		t.Fatal("reduced registration changed unexpectedly", err)
	}
	config, ok := registration.Configuration()
	return registration, config, ok
}

func TestRigConfigurationIsSeparatePrivateImmutableEvidence(t *testing.T) {
	registration, config, ok := configurationRead(t, []byte(rigConfigurationFixture))
	if !ok {
		t.Fatal("complete control view unavailable")
	}
	command, ok := config.Rigctld("none")
	if !ok {
		t.Fatal("command unavailable")
	}
	want := []string{"/usr/bin/rigctld", "-m", "3073", "-T", "127.0.0.1", "-t", "4532", "-r", "/dev/dxberry/radio1-cat", "-s", "115200", "-P", "RIG"}
	if !reflect.DeepEqual(command.Arguments(), want) || command.RadioName() != "radio1" || command.Port() != 4532 {
		t.Fatal(command.Arguments())
	}
	arguments := command.Arguments()
	arguments[1] = "--unsafe"
	if !reflect.DeepEqual(command.Arguments(), want) {
		t.Fatal("mutable command")
	}
	digest := sha256.Sum256([]byte(strings.Join(want, "\x00") + "\x00"))
	if !command.MatchesArgumentsDigest(digest) || command.MatchesArgumentsDigest(sha256.Sum256([]byte("wrong"))) {
		t.Fatal("argv comparison")
	}
	pin, ok := command.CAT()
	if !ok || pin.Path() != "usb-0:1.3:1.0" {
		t.Fatal(pin, ok)
	}
	if id, known := pin.USBID(); !known || id != "1234:abcd" {
		t.Fatal(id, known)
	}
	if serial, known := pin.Serial(); !known || serial != "example" {
		t.Fatal(serial, known)
	}
	if _, ok := command.SerialPTT(); ok {
		t.Fatal("unused PTT pin")
	}
	if _, ok := command.HID(); ok {
		t.Fatal("unused HID")
	}
	for _, value := range []any{registration, config, command, pin} {
		raw, err := json.Marshal(value)
		if err != nil || string(raw) != "{}" {
			t.Fatal("private selector became wire data", string(raw), err)
		}
	}
	if _, ok := (Registration{}).Configuration(); ok {
		t.Fatal("zero registration proof")
	}
	if _, ok := (RigConfiguration{}).Rigctld("none"); ok {
		t.Fatal("zero configuration proof")
	}
	if (RigCommand{}).Arguments() != nil || (RigCommand{}).MatchesArgumentsDigest(digest) {
		t.Fatal("zero command proof")
	}
	for _, kind := range []string{"", "unknown", "session;start"} {
		if _, ok := config.Rigctld(kind); ok {
			t.Fatal("unknown module classification", kind)
		}
	}
	_, _, ok = configurationRead(t, []byte(registrationFixture))
	if ok {
		t.Fatal("minimal historical record acquired control proof")
	}
}

func TestRigConfigurationRefusesMalformedCriticalFieldsWithoutChangingReducedView(t *testing.T) {
	cases := map[string]func(map[string]any){
		"missing baud":          func(r map[string]any) { delete(r["rig"].(map[string]any), "baud") },
		"null baud":             func(r map[string]any) { r["rig"].(map[string]any)["baud"] = nil },
		"string baud":           func(r map[string]any) { r["rig"].(map[string]any)["baud"] = "115200" },
		"negative baud":         func(r map[string]any) { r["rig"].(map[string]any)["baud"] = -1 },
		"fractional baud":       func(r map[string]any) { r["rig"].(map[string]any)["baud"] = 1.5 },
		"oversized baud":        func(r map[string]any) { r["rig"].(map[string]any)["baud"] = 2147483648 },
		"baud alias":            func(r map[string]any) { r["rig"].(map[string]any)["Baud"] = 115200 },
		"bad PTT type":          func(r map[string]any) { r["rig"].(map[string]any)["ptt_type"] = "RTS -p elsewhere" },
		"missing gpio selector": func(r map[string]any) { delete(r["ptt"].(map[string]any), "gpio_line") },
		"bad gpio selector":     func(r map[string]any) { r["ptt"].(map[string]any)["gpio_line"] = -1 },
		"missing HID":           func(r map[string]any) { delete(r, "hid") },
		"missing serial PTT":    func(r map[string]any) { delete(r, "ptt_serial") },
		"invalid pin path":      func(r map[string]any) { r["cat"].(map[string]any)["path"] = "../../other" },
		"oversized path": func(r map[string]any) {
			r["cat"].(map[string]any)["path"] = "usb-0:" + strings.Repeat("1", 128) + ":1.0"
		},
		"invalid USB ID":   func(r map[string]any) { r["cat"].(map[string]any)["vidpid"] = "1234:abcd;start" },
		"USB ID alias":     func(r map[string]any) { r["cat"].(map[string]any)["VIDPID"] = "1234:abcd" },
		"serial control":   func(r map[string]any) { r["cat"].(map[string]any)["serial"] = "other\nline" },
		"oversized serial": func(r map[string]any) { r["cat"].(map[string]any)["serial"] = strings.Repeat("x", 513) },
		"wrong HID type":   func(r map[string]any) { r["hid"] = []any{} },
		"oversized model":  func(r map[string]any) { r["rig"].(map[string]any)["model"] = 2147483648 },
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			registration, config, ok := configurationRead(t, configurationDocument(t, change))
			if ok {
				t.Fatal("malformed control view accepted")
			}
			if _, ok := config.Rigctld("none"); ok {
				t.Fatal("unavailable configuration yielded arguments")
			}
			if registration.Radio().Name != "radio1" {
				t.Fatal("lost reduced evidence")
			}
		})
	}
}

func TestRigConfigurationDistinguishesUnknownAndKnownEmptyPinIdentity(t *testing.T) {
	for _, state := range []string{"missing", "null", "empty"} {
		t.Run(state, func(t *testing.T) {
			raw := configurationDocument(t, func(r map[string]any) {
				pin := r["cat"].(map[string]any)
				switch state {
				case "missing":
					delete(pin, "serial")
					delete(pin, "vidpid")
				case "null":
					pin["serial"] = nil
					pin["vidpid"] = nil
				case "empty":
					pin["serial"] = ""
				}
			})
			_, config, ok := configurationRead(t, raw)
			if !ok {
				t.Fatal("argument evidence unavailable")
			}
			command, _ := config.Rigctld("none")
			pin, _ := command.CAT()
			serial, known := pin.Serial()
			if serial != "" || known != (state == "empty") {
				t.Fatal("unknown serial guessed empty", serial, known)
			}
			_, idKnown := pin.USBID()
			if idKnown != (state == "empty") {
				t.Fatal("unknown USB ID guessed", idKnown)
			}
		})
	}
}

func TestRigCommandMatchesExistingProvisioningArgumentRules(t *testing.T) {
	if *rigctldFixture == "" {
		t.Skip("explicit -rigctld-fixture required for Docker provisioning parity")
	}
	if _, err := os.Stat(*rigctldFixture); err != nil {
		t.Fatal(err)
	}
	for _, program := range []string{"bash", "jq", "sha256sum"} {
		if _, err := exec.LookPath(program); err != nil {
			t.Fatal("missing parity prerequisite", program, err)
		}
	}
	cases := map[string]func(map[string]any){
		"native RIG": nil,
		"native RTS": func(r map[string]any) { r["rig"].(map[string]any)["ptt_type"] = "RTS" },
		"separate DTR": func(r map[string]any) {
			r["rig"].(map[string]any)["ptt_type"] = "DTR"
			r["ptt_serial"] = map[string]any{"path": "usb-0:1.4:1.0"}
		},
		"flrig RIG": func(r map[string]any) { r["rig"].(map[string]any)["control"] = "flrig" },
		"flrig unpinned RTS": func(r map[string]any) {
			r["rig"].(map[string]any)["control"] = "flrig"
			r["rig"].(map[string]any)["ptt_type"] = "RTS"
		},
		"flrig separate RTS": func(r map[string]any) {
			r["rig"].(map[string]any)["control"] = "flrig"
			r["rig"].(map[string]any)["ptt_type"] = "RTS"
			r["ptt_serial"] = map[string]any{"path": "usb-0:1.4:1.0"}
		},
		"CM108 HID": func(r map[string]any) {
			r["ptt"].(map[string]any)["method"] = "cm108"
			r["hid"] = map[string]any{"path": "usb-0:1.5:1.3"}
		},
		"CM108 missing HID": func(r map[string]any) { r["ptt"].(map[string]any)["method"] = "cm108" },
		"GPIO": func(r map[string]any) {
			r["ptt"].(map[string]any)["method"] = "gpio"
			r["ptt"].(map[string]any)["gpio_line"] = 17
		},
		"GPIO unset": func(r map[string]any) { r["ptt"].(map[string]any)["method"] = "gpio" },
		"dummy no CAT": func(r map[string]any) {
			r["cat"] = nil
			r["rig"].(map[string]any)["model"] = 1
			r["rig"].(map[string]any)["ptt_type"] = "NONE"
		},
		"last flrig port": func(r map[string]any) { r["rigctld_port"] = 65534; r["rig"].(map[string]any)["control"] = "flrig" },
		"historical control default": func(r map[string]any) {
			delete(r["rig"].(map[string]any), "control")
			r["rig"].(map[string]any)["baud"] = 0
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			raw := configurationDocument(t, change)
			_, config, ok := configurationRead(t, raw)
			if !ok {
				t.Fatal("fixture control view unavailable")
			}
			var doc struct {
				Radios map[string]json.RawMessage `json:"radios"`
			}
			if err := json.Unmarshal(raw, &doc); err != nil {
				t.Fatal(err)
			}
			for _, kind := range []string{"none", "graywolf", "session"} {
				ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
				script := `dxb_warn(){ :; }; source "$1"; r=$(cat); dxb_rigctld_env "$2" "$r" "$3"`
				cmd := exec.CommandContext(ctx, "bash", "-euc", script, "fixture", *rigctldFixture, "radio1", kind)
				cmd.Stdin = bytes.NewReader(doc.Radios["radio1"])
				out, err := cmd.Output()
				cancel()
				if err != nil {
					t.Fatal("provisioning fixture failed", kind, err)
				}
				fields := map[string]string{}
				for _, line := range strings.Split(strings.TrimSpace(string(out)), "\n") {
					key, value, ok := strings.Cut(line, "=")
					if !ok {
						t.Fatal("bad fixture output")
					}
					fields[key] = value
				}
				want := []string{"/usr/bin/rigctld", "-m", fields["MODEL"], "-T", "127.0.0.1", "-t", fields["PORT"]}
				want = append(want, strings.Fields(fields["RIG_ARGS"])...)
				want = append(want, strings.Fields(fields["PTT_ARGS"])...)
				command, ok := config.Rigctld(kind)
				if !ok || !reflect.DeepEqual(command.Arguments(), want) {
					t.Fatal("argument rules differ from provisioning", kind, command.Arguments(), want)
				}
				for suffix, get := range map[string]func() (ControlPin, bool){"cat": command.CAT, "ptt": command.SerialPTT, "hid": command.HID} {
					_, used := get()
					wanted := false
					for _, arg := range want {
						wanted = wanted || arg == "/dev/dxberry/radio1-"+suffix
					}
					if used != wanted {
						t.Fatal("wrong used selector set", suffix, kind)
					}
				}
			}
		})
	}
}

func FuzzRigConfiguration(f *testing.F) {
	f.Add([]byte(rigConfigurationFixture))
	f.Add([]byte(registrationFixture))
	f.Fuzz(func(t *testing.T, raw []byte) {
		radio, err := parseRegistration(raw, "radio1")
		if err != nil {
			return
		}
		config, ok := parseRigConfiguration(raw, radio)
		if !ok {
			return
		}
		for _, kind := range []string{"none", "graywolf", "session"} {
			command, ok := config.Rigctld(kind)
			rawArgs := strings.Join(command.Arguments(), "\x00") + "\x00"
			if !ok || !command.MatchesArgumentsDigest(sha256.Sum256([]byte(rawArgs))) {
				t.Fatal("inconsistent validated command")
			}
		}
	})
}
