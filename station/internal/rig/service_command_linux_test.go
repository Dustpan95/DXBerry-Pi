package rig

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
)

func serviceCommandFixture(t *testing.T, name string, port int) radios.RigCommand {
	t.Helper()
	config := serviceConfigurationFixture(t, name, port, nil)
	command, ok := config.Rigctld("none")
	if !ok {
		t.Fatal("fixture command unavailable")
	}
	return command
}

func serviceConfigurationFixture(t *testing.T, name string, port int, change func(map[string]any)) radios.RigConfiguration {
	t.Helper()
	record := map[string]any{
		"label": "Fixture", "owner": "", "rigctld_port": port,
		"rig":        map[string]any{"model": 3073, "baud": 115200, "ptt_type": "RIG", "control": "hamlib"},
		"ptt":        map[string]any{"method": "rigctld", "gpio_line": nil},
		"cat":        map[string]any{"path": "usb-0:1.3:1.0", "vidpid": "1234:abcd", "serial": "example"},
		"ptt_serial": nil, "hid": nil,
	}
	if change != nil {
		change(record)
	}
	raw, err := json.Marshal(map[string]any{"version": 1, "radios": map[string]any{name: record}})
	if err != nil {
		t.Fatal(err)
	}
	path := t.TempDir() + "/radios.json"
	serviceFixtureWrite(t, path, string(raw))
	reader, err := radios.NewRegistrationReader(path)
	if err != nil {
		t.Fatal(err)
	}
	registration, err := reader.Read(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	config, ok := registration.Configuration()
	if !ok {
		t.Fatal("fixture config unavailable")
	}
	return config
}

func refreshServiceCommandPeer(t *testing.T, f *serviceFixture, arguments []string) {
	t.Helper()
	serviceFixtureWrite(t, f.process+"/cmdline", strings.Join(arguments, "\x00")+"\x00")
	dir, err := serviceDirectory(f.process)
	if err != nil {
		t.Fatal(err)
	}
	peer, err := peerProcessSnapshot(int(dir.Fd()), f.e, 100, "net:[42]")
	dir.Close()
	if err != nil {
		t.Fatal(err)
	}
	peer.listener, peer.accepted, peer.listenerFD, peer.acceptedFD = 20, 30, "3", "4"
	f.peer = peer
}

func TestServiceCommandMatchesOnlyExactFreshRegistrationArguments(t *testing.T) {
	f := newServiceFixture(t)
	// Fixed ports are isolated inside the Docker network namespace; neither
	// systemd nor rigctld runs here. Unit/proc/peer identity remains synthetic.
	server := newFakeAt(t, "127.0.0.1:4532", freqHandler)
	client := fastClient(t, server.addr())
	waitConnected(t, client)
	var ok bool
	f.e, ok = client.ConnectionEndpoint()
	if !ok {
		t.Fatal("no endpoint")
	}
	command := serviceCommandFixture(t, "hf", 4532)
	refreshServiceCommandPeer(t, f, command.Arguments())
	before, err := f.r.Read(ctx5(t), "hf", f.e)
	if err != nil || !before.MatchesCommand(command) {
		t.Fatal("matching process argv refused", err)
	}
	if before.MatchesCommand(radios.RigCommand{}) || (ServiceObservation{}).MatchesCommand(command) {
		t.Fatal("zero evidence matched")
	}
	other := serviceCommandFixture(t, "other", 4532)
	if before.MatchesCommand(other) {
		t.Fatal("different radio command matched")
	}
	for _, kind := range []string{"model", "bind", "port", "baud", "PTT", "extra", "duplicate", "argv0"} {
		t.Run(kind, func(t *testing.T) {
			args := command.Arguments()
			switch kind {
			case "model":
				args[2] = "4"
			case "bind":
				args[4] = "0.0.0.0"
			case "port":
				args[6] = "4534"
			case "baud":
				args[10] = "9600"
			case "PTT":
				args[len(args)-1] = "RTS"
			case "extra":
				args = append(args, "-vvvv")
			case "duplicate":
				args = append(args, "-m", "3073")
			case "argv0":
				args[0] = "rigctld"
			}
			refreshServiceCommandPeer(t, f, args)
			view, err := f.r.Read(ctx5(t), "hf", f.e)
			if err != nil {
				t.Fatal("service identity fixture failed before command comparison", err)
			}
			if view.MatchesCommand(command) || before.Same(view) {
				t.Fatal("mismatched process argv accepted", kind)
			}
		})
	}
	// Identical expected argv does not qualify a connection to another port.
	otherServer := newFakeAt(t, "127.0.0.1:4534", freqHandler)
	otherClient := fastClient(t, otherServer.addr())
	waitConnected(t, otherClient)
	f.e, ok = otherClient.ConnectionEndpoint()
	if !ok {
		t.Fatal("no alternate endpoint")
	}
	refreshServiceCommandPeer(t, f, command.Arguments())
	wrongPort, err := f.r.Read(ctx5(t), "hf", f.e)
	if err != nil || wrongPort.MatchesCommand(command) {
		t.Fatal("wrong actual peer port qualified", err)
	}
}
