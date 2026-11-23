// Package pat reads the reviewed Pat 1.0.1 status protocol. It contains no
// connection, mailbox, configuration, rig or transmit operations.
package pat

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	statusURL        = "http://127.0.0.1:8180/api/status"
	maxResponseBytes = 64 << 10
	readTimeout      = time.Second
	maxListeners     = 32
	maxListenerBytes = 128
)

var ErrUnavailable = errors.New("Pat status unavailable")

// Status deliberately omits Pat's config hash, HTTP client addresses and remote
// connection address. Connected/Dialing describe a Winlink transport, not PTT
// or an RF measurement. ObservedAt is this client's successful observation time.
type Status struct {
	Connected       bool      `json:"connected"`
	Dialing         bool      `json:"dialing"`
	ActiveListeners []string  `json:"active_listeners"`
	ObservedAt      time.Time `json:"observed_at"`
}

// client has a fixed loopback destination and a single GET operation. It does
// not follow redirects or use environment proxies. It has no background loop,
// cache, startup side effect or exported destination/transport override.
//
// Only Reader calls this transport, after checking the supported version and
// managed process/listener identity. An HTTP response alone proves neither.
type client struct {
	http *http.Client
	now  func() time.Time
}

func newClient() *client {
	transport := &http.Transport{
		Proxy:                  nil,
		DialContext:            (&net.Dialer{Timeout: 500 * time.Millisecond}).DialContext,
		DisableKeepAlives:      true,
		DisableCompression:     true,
		MaxResponseHeaderBytes: 16 << 10,
		ResponseHeaderTimeout:  500 * time.Millisecond,
	}
	return &client{
		http: &http.Client{
			Transport: transport,
			Timeout:   readTimeout,
			CheckRedirect: func(*http.Request, []*http.Request) error {
				return ErrUnavailable
			},
		},
		now: time.Now,
	}
}

// read returns a zero Status and an error on failure, never a cached observation.
// Raw upstream bodies, URLs and transport errors are absent from returned errors.
func (c *client) read(ctx context.Context) (Status, error) {
	if c == nil || c.http == nil || c.now == nil {
		return Status{}, ErrUnavailable
	}
	ctx, cancel := context.WithTimeout(ctx, readTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, statusURL, nil)
	if err != nil {
		return Status{}, ErrUnavailable
	}
	req.Header.Set("Accept", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return Status{}, ErrUnavailable
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Status{}, ErrUnavailable
	}
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil || len(raw) > maxResponseBytes || ctx.Err() != nil {
		return Status{}, ErrUnavailable
	}
	status, err := parseStatus(raw)
	if err != nil || ctx.Err() != nil {
		return Status{}, ErrUnavailable
	}
	status.ObservedAt = c.now()
	return status, nil
}

func parseStatus(raw []byte) (Status, error) {
	var status Status
	if len(raw) == 0 || len(raw) > maxResponseBytes || !utf8.Valid(raw) || !json.Valid(raw) {
		return status, ErrUnavailable
	}
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	if token, err := decoder.Token(); err != nil || token != json.Delim('{') {
		return status, ErrUnavailable
	}
	seen := make(map[string]bool)
	for decoder.More() {
		token, err := decoder.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return Status{}, ErrUnavailable
		}
		seen[key] = true
		var value json.RawMessage
		if decoder.Decode(&value) != nil {
			return Status{}, ErrUnavailable
		}
		switch key {
		case "connected", "dialing":
			// A null/missing boolean must not become an invented false/idle.
			var flag *bool
			if json.Unmarshal(value, &flag) != nil || flag == nil {
				return Status{}, ErrUnavailable
			}
			if key == "connected" {
				status.Connected = *flag
			} else {
				status.Dialing = *flag
			}
		case "active_listeners":
			if json.Unmarshal(value, &status.ActiveListeners) != nil || status.ActiveListeners == nil || len(status.ActiveListeners) > maxListeners {
				return Status{}, ErrUnavailable
			}
			for _, listener := range status.ActiveListeners {
				if listener == "" || len(listener) > maxListenerBytes || strings.IndexFunc(listener, unicode.IsControl) >= 0 {
					return Status{}, ErrUnavailable
				}
			}
		default:
			// Do not accept case aliases of known fields, which Go's struct
			// decoder would otherwise match case-insensitively.
			for _, field := range []string{"connected", "dialing", "active_listeners"} {
				if strings.EqualFold(key, field) {
					return Status{}, ErrUnavailable
				}
			}
		}
	}
	if !seen["connected"] || !seen["dialing"] || !seen["active_listeners"] {
		return Status{}, ErrUnavailable
	}
	return status, nil
}
