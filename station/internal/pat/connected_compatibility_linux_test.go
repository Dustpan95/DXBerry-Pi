//go:build patmanager && patcompat

package pat

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"os"
	"strings"
	"testing"
	"time"
)

const fixtureTelnetAddr = "127.0.0.1:18772"

// Only the disposable manager guest uses this second synthetic config.
// Reuse its normal Pat command line and keep the service's private devices.
func configureFixturePeer(t *testing.T, path string) {
	t.Helper()
	var config map[string]json.RawMessage
	if err := json.Unmarshal([]byte(compatibilityConfig), &config); err != nil {
		t.Fatal(err)
	}
	config["listen"] = json.RawMessage(`["telnet"]`)
	telnet, err := json.Marshal(map[string]string{"listen_addr": fixtureTelnetAddr, "password": "DXB-FIXTURE"})
	if err != nil {
		t.Fatal(err)
	}
	config["telnet"] = telnet
	content, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, content, 0600); err != nil {
		t.Fatal(err)
	}
}

func waitFixturePeerStatus(t *testing.T, ctx context.Context, r *Reader, connected bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); {
		v, err := r.Read(ctx, "fixture1")
		if err == nil && v.Radio == "fixture1" && !v.Status.Dialing && v.Status.Connected == connected &&
			len(v.Status.ActiveListeners) == 1 && v.Status.ActiveListeners[0] == "telnet" && !v.Status.ObservedAt.IsZero() {
			return
		}
		time.Sleep(75 * time.Millisecond)
	}
	t.Fatalf("missing connected=%t from the managed loopback peer listener", connected)
}

func openFixturePeer(t *testing.T) net.Conn {
	t.Helper()
	conn, err := net.DialTimeout("tcp4", fixtureTelnetAddr, 2*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	if err := conn.SetDeadline(time.Now().Add(5 * time.Second)); err != nil {
		t.Fatal(err)
	}
	reader := bufio.NewReader(conn)
	for _, item := range []struct{ prompt, answer string }{
		{"Callsign :", "N0CALL-3"}, {"Password :", "DXB-FIXTURE"},
	} {
		line, err := reader.ReadString('\r')
		if err != nil || strings.TrimSpace(line) != item.prompt {
			t.Fatal("expected fixture P2P login prompt", item.prompt, err)
		}
		if _, err := io.WriteString(conn, item.answer+"\r"); err != nil {
			t.Fatal(err)
		}
	}
	return conn
}
