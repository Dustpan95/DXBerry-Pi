package httpapi

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/pat"
)

type patReaderFunc func(context.Context, string) (pat.Observation, error)

func (f patReaderFunc) Read(ctx context.Context, radio string) (pat.Observation, error) {
	return f(ctx, radio)
}

func patObservation() pat.Observation {
	return pat.Observation{Radio: "radio1", Status: pat.Status{Connected: true, ActiveListeners: []string{"ardop"}, ObservedAt: time.Unix(123, 0).UTC()}}
}

func patServer(login bool, reader PatStatusReader) http.Handler {
	a := &PatAPI{Radios: rwRecords{"radio1": {Name: "radio1", Owner: "pat"}}, Reader: reader}
	return New(Config{Auth: rwAuth{ok: login}, Routes: a.Register}).Handler
}

func TestPatAPIRejectsUngatedUnknownAndUnexpectedInputsBeforeReader(t *testing.T) {
	for _, tc := range []struct {
		method, path, body, site string
		login                    bool
		code                     int
	}{
		{"GET", "/api/radio/radio1/pat", "", "same-origin", false, 401},
		{"GET", "/api/radio/radio1/pat", "", "cross-site", true, 403},
		{"GET", "/api/radio/radio1/pat", "", "", true, 403},
		{"GET", "/api/radio/missing/pat", "", "same-origin", true, 404},
		{"GET", "/api/radio/Radio1/pat", "", "same-origin", true, 404},
		{"GET", "/api/radio/radio1/pat?url=http://PRIVATE_TARGET", "", "same-origin", true, 400},
		{"GET", "/api/radio/radio1/pat", `{"connect":"PRIVATE_TARGET"}`, "same-origin", true, 400},
		{"HEAD", "/api/radio/radio1/pat", "", "same-origin", true, 405},
		{"POST", "/api/radio/radio1/pat", "{}", "same-origin", true, 404},
		{"PUT", "/api/radio/radio1/pat", "{}", "same-origin", true, 404},
		{"DELETE", "/api/radio/radio1/pat", "", "same-origin", true, 404},
		{"POST", "/api/radio/radio1/pat/connect", "{}", "same-origin", true, 404},
		{"POST", "/api/radio/radio1/pat/disconnect", "{}", "same-origin", true, 404},
		{"POST", "/api/radio/radio1/pat/config", "{}", "same-origin", true, 404},
	} {
		calls := 0
		h := patServer(tc.login, patReaderFunc(func(context.Context, string) (pat.Observation, error) { calls++; return patObservation(), nil }))
		rec := rwDo(h, tc.method, tc.path, tc.body, map[string]string{"Sec-Fetch-Site": tc.site})
		if rec.Code != tc.code || calls != 0 || strings.Contains(rec.Body.String(), "PRIVATE_") {
			t.Errorf("%s %s: code=%d calls=%d body=%s", tc.method, tc.path, rec.Code, calls, rec.Body.String())
		}
	}
}

func TestPatAPIProjectsObservationAndMakesItUncacheable(t *testing.T) {
	calls := 0
	h := patServer(true, patReaderFunc(func(ctx context.Context, radio string) (pat.Observation, error) {
		calls++
		if radio != "radio1" {
			t.Error("wrong radio", radio)
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 3*time.Second {
			t.Error("read budget missing")
		}
		return patObservation(), nil
	}))
	rec := rwDo(h, "GET", "/api/radio/radio1/pat", "", nil)
	var got pat.Observation
	if rec.Code != 200 || json.Unmarshal(rec.Body.Bytes(), &got) != nil || got.Radio != "radio1" || !got.Status.Connected || calls != 1 {
		t.Fatal(rec.Code, rec.Body, calls)
	}
	if rec.Header().Get("Cache-Control") != "no-store" || rec.Header().Get("Content-Type") != "application/json" {
		t.Fatal("observation response has incorrect cache/type headers", rec.Header())
	}
}

func TestPatAPIFailureDoesNotReturnPrivateOrPreviousStatus(t *testing.T) {
	for _, failure := range []string{"error", "other-radio", "missing-time", "missing-listeners", "no-reader"} {
		t.Run(failure, func(t *testing.T) {
			calls := 0
			reader := PatStatusReader(patReaderFunc(func(context.Context, string) (pat.Observation, error) {
				calls++
				observation := patObservation()
				if calls == 1 {
					return observation, nil
				}
				switch failure {
				case "other-radio":
					observation.Radio = "radio2"
				case "missing-time":
					observation.Status.ObservedAt = time.Time{}
				case "missing-listeners":
					observation.Status.ActiveListeners = nil
				default:
					return observation, errors.New("PRIVATE_PROCESS_AND_SETTINGS")
				}
				return observation, nil
			}))
			h := patServer(true, reader)
			if rec := rwDo(h, "GET", "/api/radio/radio1/pat", "", nil); rec.Code != 200 {
				t.Fatal("initial valid observation failed", rec.Code)
			}
			if failure == "no-reader" {
				h = patServer(true, nil)
			}
			rec := rwDo(h, "GET", "/api/radio/radio1/pat", "", nil)
			if rec.Code != 503 || !strings.Contains(rec.Body.String(), `"code":"unavailable"`) || strings.Contains(rec.Body.String(), "PRIVATE_") || strings.Contains(rec.Body.String(), `"connected"`) {
				t.Fatal("failed observation returned stale/private state", rec.Code, rec.Body)
			}
		})
	}
}

func TestPatAPIPassesEarlierCancellationAndDiscardsLateSuccess(t *testing.T) {
	h := patServer(true, patReaderFunc(func(ctx context.Context, _ string) (pat.Observation, error) {
		<-ctx.Done()
		return patObservation(), nil
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	req := httptest.NewRequest("GET", "/api/radio/radio1/pat", nil).WithContext(ctx)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	started := time.Now()
	h.ServeHTTP(rec, req)
	if rec.Code != 503 || time.Since(started) > time.Second {
		t.Fatal("caller cancellation retained successful state", rec.Code, rec.Body)
	}
}

func TestPatAPIRealHTTPKeepsCrossSiteAndControlRequestsOutOfReader(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(patServer(true, patReaderFunc(func(context.Context, string) (pat.Observation, error) { calls.Add(1); return patObservation(), nil })))
	defer server.Close()
	for _, tc := range []struct {
		method, path, site string
		code               int
	}{
		{"GET", "/api/radio/radio1/pat", "same-origin", 200},
		{"GET", "/api/radio/radio1/pat", "cross-site", 403},
		{"POST", "/api/radio/radio1/pat/connect", "same-origin", 404},
	} {
		req, err := http.NewRequest(tc.method, server.URL+tc.path, nil)
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Sec-Fetch-Site", tc.site)
		resp, err := server.Client().Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		resp.Body.Close()
		if resp.StatusCode != tc.code {
			t.Fatal(tc.method, tc.path, resp.StatusCode)
		}
	}
	if calls.Load() != 1 {
		t.Fatal("rejected request reached reader", calls.Load())
	}
}
