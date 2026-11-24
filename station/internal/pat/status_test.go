package pat

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

const idleStatus = `{"active_listeners":[],"connected":false,"dialing":false,"remote_addr":"","http_clients":[],"config_hash":"upstream-config-hash"}`

// Keep the real client/HTTP transport and its fixed request destination. Only
// the fixture's TCP dial is redirected to a disposable loopback server.
func fixtureClient(t *testing.T, handler http.HandlerFunc) *client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	client := newClient()
	transport := client.http.Transport.(*http.Transport)
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "tcp" || address != "127.0.0.1:8180" {
			return nil, fmt.Errorf("unexpected dial")
		}
		return (&net.Dialer{}).DialContext(ctx, network, server.Listener.Addr().String())
	}
	return client
}

func TestReadUsesOnlyFixedReadOnlyEndpointAndProjectsStatus(t *testing.T) {
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1")
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1")
	t.Setenv("NO_PROXY", "")
	client := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" || r.URL.RequestURI() != "/api/status" || r.Host != "127.0.0.1:8180" || r.ContentLength != 0 {
			t.Error("unexpected upstream request", r.Method, r.URL.RequestURI(), r.Host, r.ContentLength)
		}
		if r.Header.Get("Authorization") != "" || r.Header.Get("Cookie") != "" {
			t.Error("status read included credentials")
		}
		// Pat's real handler does not explicitly set an application/json header.
		fmt.Fprint(w, `{"active_listeners":["ardop","telnet"],"connected":true,"dialing":false,"remote_addr":"PRIVATE_REMOTE","http_clients":["PRIVATE_CLIENT"],"config_hash":"PRIVATE_HASH","future_field":{"secret":"PRIVATE_EXTRA"}}`)
	})
	observed := time.Unix(123, 0).UTC()
	client.now = func() time.Time { return observed }
	status, err := client.read(context.Background())
	if err != nil || !status.Connected || status.Dialing || !reflect.DeepEqual(status.ActiveListeners, []string{"ardop", "telnet"}) || !status.ObservedAt.Equal(observed) {
		t.Fatal("incorrect observation", status, err)
	}
	raw, _ := json.Marshal(status)
	if strings.Contains(string(raw), "PRIVATE_") || strings.Contains(string(raw), "config_hash") || strings.Contains(string(raw), "remote_addr") || strings.Contains(string(raw), "http_clients") {
		t.Fatal("private upstream fields escaped projection", string(raw))
	}
	transport := client.http.Transport.(*http.Transport)
	if transport.Proxy != nil || !transport.DisableCompression || !transport.DisableKeepAlives {
		t.Fatal("client inherited proxy, compression or persistent connection policy")
	}
}

func TestReadDoesNotFollowRedirectsOrReturnErrorBodies(t *testing.T) {
	for _, code := range []int{301, 302, 307, 308, 401, 404, 500, 204} {
		t.Run(fmt.Sprint(code), func(t *testing.T) {
			var requests atomic.Int32
			client := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				w.Header().Set("Location", "http://127.0.0.1:8180/api/connect?url=PRIVATE_TARGET")
				w.WriteHeader(code)
				fmt.Fprint(w, "PRIVATE_ERROR_BODY")
			})
			status, err := client.read(context.Background())
			if err != ErrUnavailable || !reflect.DeepEqual(status, Status{}) || requests.Load() != 1 {
				t.Fatal("redirect/error was not closed", status, err, requests.Load())
			}
		})
	}
}

func TestReadHonorsEarlierDeadlineAndDoesNotCacheSuccess(t *testing.T) {
	var calls atomic.Int32
	client := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			fmt.Fprint(w, idleStatus)
			return
		}
		<-r.Context().Done()
	})
	if _, err := client.read(context.Background()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	started := time.Now()
	status, err := client.read(ctx)
	if err != ErrUnavailable || !reflect.DeepEqual(status, Status{}) || time.Since(started) > 500*time.Millisecond {
		t.Fatal("deadline retained old/zero-as-success status", status, err)
	}
}

func TestReadBoundsSlowBodyAndRejectsPartialOrOversizedResponses(t *testing.T) {
	for _, body := range []string{strings.Repeat(" ", maxResponseBytes+1), idleStatus + "{}", idleStatus[:len(idleStatus)-1]} {
		client := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) { fmt.Fprint(w, body) })
		if status, err := client.read(context.Background()); err != ErrUnavailable || !reflect.DeepEqual(status, Status{}) {
			t.Fatal("invalid body returned an observation", status, err)
		}
	}
	client := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"connected":`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	if status, err := client.read(ctx); err != ErrUnavailable || !reflect.DeepEqual(status, Status{}) {
		t.Fatal("slow body returned a partial observation", status, err)
	}
}

func TestReadHasDeadlineWithoutCallerTimeoutAndRedactsDialFailures(t *testing.T) {
	client := fixtureClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `{"connected":`)
		w.(http.Flusher).Flush()
		<-r.Context().Done()
	})
	started := time.Now()
	if status, err := client.read(context.Background()); err != ErrUnavailable || !reflect.DeepEqual(status, Status{}) || time.Since(started) > 2*time.Second {
		t.Fatal("unbounded body read or partial observation", status, err)
	}
	client = newClient()
	client.http.Transport.(*http.Transport).DialContext = func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("PRIVATE_TRANSPORT_DETAIL")
	}
	if status, err := client.read(context.Background()); err != ErrUnavailable || !reflect.DeepEqual(status, Status{}) {
		t.Fatal("transport details or zero-as-success escaped", status, err)
	}
}

func TestParseRequiresCompleteUnambiguousStatus(t *testing.T) {
	valid := []string{idleStatus, `{"connected":false,"dialing":true,"active_listeners":[]}`, `{"connected":true,"dialing":true,"active_listeners":["ardop"]}`}
	for _, raw := range valid {
		if status, err := parseStatus([]byte(raw)); err != nil || status.ActiveListeners == nil || !status.ObservedAt.IsZero() {
			t.Fatal("valid response refused or observation time invented", status, err)
		}
	}
	invalid := []string{
		`null`, `[]`, `{}`, `{"connected":false,"dialing":false}`,
		`{"connected":null,"dialing":false,"active_listeners":[]}`,
		`{"connected":false,"dialing":"false","active_listeners":[]}`,
		`{"connected":false,"dialing":false,"active_listeners":{}}`,
		`{"connected":false,"dialing":false,"active_listeners":null}`,
		`{"connected":false,"dialing":false,"active_listeners":[null]}`,
		`{"connected":false,"dialing":false,"active_listeners":[""]}`,
		`{"connected":false,"dialing":false,"active_listeners":["ardop\n"]}`,
		`{"connected":false,"connected":true,"dialing":false,"active_listeners":[]}`,
		`{"connected":false,"CONNECTED":true,"dialing":false,"active_listeners":[]}`,
		`{"connected":false,"dialing":false,"active_listeners":[],"extra":0,"extra":1}`,
		strings.Replace(idleStatus, `[]`, `["`+strings.Repeat("a", maxListenerBytes+1)+`"]`, 1),
		strings.Replace(idleStatus, `[]`, `[`+strings.TrimSuffix(strings.Repeat(`"ardop",`, maxListeners+1), ",")+`]`, 1),
		strings.Replace(idleStatus, `[]`, "[\"\xff\"]", 1),
	}
	for _, raw := range invalid {
		if status, err := parseStatus([]byte(raw)); !errors.Is(err, ErrUnavailable) || !reflect.DeepEqual(status, Status{}) {
			t.Fatal("malformed or ambiguous status accepted", status, err, raw)
		}
	}
}
