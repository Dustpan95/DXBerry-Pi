//go:build patmanager && patcompat

package pat

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"
)

// The peer holds each normal handshake boundary until the production reader
// observes it. This makes short-lived states observable without timing guesses.
func checkFixtureOutgoing(t *testing.T, ctx context.Context, r *Reader) {
	t.Helper()
	sessionCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	if err := listener.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	target := url.URL{Scheme: "telnet", User: url.UserPassword("N0CALL", "DXB-FIXTURE"), Host: listener.Addr().String(), Path: "/N0CALL-2"}
	done := make(chan error, 1)
	go func() { done <- connectFixturePat(sessionCtx, target.String()) }()
	conn, err := listener.AcceptTCP()
	if err != nil {
		select {
		case result := <-done:
			t.Fatalf("no outgoing connection: Pat request returned %v", result)
		default:
			t.Fatal("no outgoing connection from Pat", err)
		}
	}
	defer conn.Close()
	if err := conn.SetDeadline(time.Now().Add(15 * time.Second)); err != nil {
		t.Fatal(err)
	}
	waitFixturePeerStatus(t, sessionCtx, r, true, false)
	reader := bufio.NewReader(conn)
	for _, item := range []struct{ prompt, answer string }{
		{"Callsign :", "N0CALL"}, {"Password :", "DXB-FIXTURE"},
	} {
		if _, err := io.WriteString(conn, item.prompt+"\r"); err != nil {
			t.Fatal(err)
		}
		answer, err := reader.ReadString('\r')
		if err != nil || strings.TrimSuffix(answer, "\r") != item.answer {
			t.Fatal("Pat returned unexpected fixture credentials", err)
		}
	}
	waitFixturePeerStatus(t, sessionCtx, r, false, true)
	if reader.Buffered() != 0 {
		t.Fatal("unexpected bytes before the responding peer opened B2F")
	}
	file, err := conn.File()
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	peer := fixturePeerCommand(sessionCtx, "respond")
	peer.ExtraFiles = []*os.File{file}
	if out, err := peer.CombinedOutput(); err != nil {
		t.Fatalf("responding fixture peer: %v (%s)", err, out)
	}
	if err := <-done; err != nil {
		t.Fatal("outgoing Pat session", err)
	}
	waitFixturePeerStatus(t, sessionCtx, r, false, false)
	t.Log("Pat reported dialing, connected and idle around a successful outgoing B2F exchange")
}

func connectFixturePat(ctx context.Context, target string) error {
	form := url.Values{"url": {target}}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://127.0.0.1:8180/api/connect", strings.NewReader(form.Encode()))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := fixtureMailboxClient()
	client.Timeout = 0 // The session context covers the entire blocking connect request.
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	var result struct{ NumReceived *int }
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4096)).Decode(&result); err != nil {
		return err
	}
	if result.NumReceived == nil || *result.NumReceived != 0 {
		return fmt.Errorf("unexpected received-message count")
	}
	return nil
}
