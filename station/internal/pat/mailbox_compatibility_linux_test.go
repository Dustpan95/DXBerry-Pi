//go:build patmanager && patcompat

package pat

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"testing"
	"time"
)

const fixtureOutboxURL = "http://127.0.0.1:8180/api/mailbox/out"
const fixtureSubject = "Managed session persistence check"
const fixtureBody = "Synthetic offline mailbox fixture."

func fixtureMailboxClient() *http.Client {
	return &http.Client{
		Timeout:       5 * time.Second,
		Transport:     &http.Transport{DisableKeepAlives: true},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
}

// The manager guest has only loopback, an empty mailbox and no radio devices.
func queueFixtureMessage(t *testing.T, ctx context.Context) {
	t.Helper()
	form := url.Values{
		"to": {"N0CALL-2"}, "subject": {fixtureSubject}, "body": {fixtureBody},
		"p2ponly": {"true"}, "date": {time.Now().UTC().Format(time.RFC3339)},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, fixtureOutboxURL, strings.NewReader(form.Encode()))
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	client := fixtureMailboxClient()
	defer client.CloseIdleConnections()
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal("could not submit fixture message", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("fixture outbox returned HTTP %d", resp.StatusCode)
	}
}

func readFixtureMailbox(t *testing.T, ctx context.Context, endpoint string, dst any) {
	t.Helper()
	client := fixtureMailboxClient()
	defer client.CloseIdleConnections()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatal("could not read fixture mailbox", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("fixture mailbox returned HTTP %d", resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 32768)).Decode(dst); err != nil {
		t.Fatal("invalid fixture mailbox response", err)
	}
}

func readFixtureMessage(t *testing.T, ctx context.Context) string {
	t.Helper()
	var rows []struct{ MID string }
	readFixtureMailbox(t, ctx, fixtureOutboxURL, &rows)
	if len(rows) != 1 || !regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`).MatchString(rows[0].MID) {
		t.Fatal("expected one queued fixture message")
	}
	var message struct {
		MID, Subject, Body string
		P2POnly            bool
	}
	readFixtureMailbox(t, ctx, fixtureOutboxURL+"/"+rows[0].MID, &message)
	if message.MID != rows[0].MID || message.Subject != fixtureSubject || strings.TrimSpace(message.Body) != fixtureBody || !message.P2POnly {
		t.Fatal("queued fixture content mismatch")
	}
	return message.MID
}

func checkFixtureDelivery(t *testing.T, ctx context.Context, mid string) {
	t.Helper()
	var out, sent []struct{ MID string }
	readFixtureMailbox(t, ctx, fixtureOutboxURL, &out)
	readFixtureMailbox(t, ctx, "http://127.0.0.1:8180/api/mailbox/sent", &sent)
	if len(out) != 0 || len(sent) != 1 || sent[0].MID != mid {
		t.Fatal("expected empty Pat outbox and original MID in sent mailbox")
	}
}
