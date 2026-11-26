//go:build patmanager && patcompat

package pat

import (
	"bufio"
	"context"
	"io"
	"net"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

// Exercise the shipped session template with a synthetic sysfs inventory.
func TestPatProductionSession(t *testing.T) {
	requirePatManagerGuest(t)
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Second)
	defer cancel()
	run := func(phase, input string) {
		t.Helper()
		cmd := exec.CommandContext(ctx, "/bin/bash", "/fixture/session-fixture.sh", phase)
		cmd.Stdin = strings.NewReader(input)
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("session fixture %s: %v\n%s", phase, err, output)
		}
	}
	config := strings.Replace(compatibilityConfig, `"auto_download_size_limit":-1`, `"auto_download_size_limit":321`, 1)
	run("setup", config)
	reader, err := NewReader("/var/lib/dxberry/radios.json")
	if err != nil {
		t.Fatal(err)
	}
	const radio = "fixture2"
	if _, err := reader.Read(ctx, radio); err != ErrUnavailable {
		t.Fatalf("absent fixture supplied a status: %v", err)
	}
	run("start", "")
	deadline := time.Now().Add(15 * time.Second)
	for {
		observation, err := reader.Read(ctx, radio)
		if err == nil {
			if observation.Radio != radio || observation.Status.Connected || observation.Status.Dialing || len(observation.Status.ActiveListeners) != 0 {
				t.Fatal("expected the idle production session")
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("production session never supplied idle status:", err)
		}
		time.Sleep(100 * time.Millisecond)
	}
	owned, err := reader.probe(ctx, radio)
	if err != nil || owned.pid == owned.unit.main {
		t.Fatal("expected an owned Pat child beneath the session runner")
	}
	cmdline, err := os.ReadFile("/proc/" + owned.unit.main + "/cmdline")
	if err != nil || !strings.HasSuffix(string(cmdline), "/opt/dxberry/bin/dxberry-app\x00session\x00pat-fixture2\x00") {
		t.Fatal("unit main process is not the shipped session runner")
	}
	client, err := os.ReadFile("/run/dxberry/sessions/pat-fixture2.d/client.conf")
	if err != nil || string(client) != "autospawn = no\n" {
		t.Fatal("session runner did not prepare its private client configuration")
	}
	checkARDOPCompanion(t)
	run("observe", "")
	run("stop", "")
	if _, err := reader.Read(ctx, radio); err != ErrUnavailable {
		t.Fatalf("stopped production session supplied a status: %v", err)
	}
}

// Ask the actual companion for its version over its local control socket.
// Other startup notifications can precede the command response.
func checkARDOPCompanion(t *testing.T) {
	t.Helper()
	conn, err := net.DialTimeout("tcp4", "127.0.0.1:8515", 2*time.Second)
	if err != nil {
		t.Fatal("ARDOP command socket:", err)
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, err := io.WriteString(conn, "VERSION\r"); err != nil {
		t.Fatal(err)
	}
	replies := bufio.NewReader(io.LimitReader(conn, 8192))
	for {
		line, err := replies.ReadString('\r')
		if err != nil {
			t.Fatal("expected ARDOP version response:", err)
		}
		if strings.EqualFold(strings.TrimSpace(line), "VERSION ardopcf_1.0.4.1.3") {
			return
		}
	}
}
