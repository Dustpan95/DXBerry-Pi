package main

import (
	"flag"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/httpapi"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
)

type patRecords struct{}

func (patRecords) Get(name string) (radios.Radio, bool) {
	return radios.Radio{Name: name, Owner: "pat"}, name == "radio1"
}

type patAuth struct{}

func (patAuth) Session(*http.Request) (*httpapi.Session, bool) {
	return &httpapi.Session{User: "fixture"}, true
}

func TestPatStatusFlagDefaultsOffWithoutRouteOrReaderInitialization(t *testing.T) {
	flags := flag.NewFlagSet("fixture", flag.ContinueOnError)
	enabled := patStatusOption(flags)
	if err := flags.Parse(nil); err != nil || *enabled {
		t.Fatal("native Pat status enabled by default", err)
	}
	api, err := newPatStatusAPI(*enabled, "invalid-relative-path", nil)
	if err != nil || api != nil {
		t.Fatal("disabled feature initialized reader", api, err)
	}
	server := httpapi.New(httpapi.Config{Auth: patAuth{}, Routes: func(mux *http.ServeMux) {
		if api != nil {
			api.Register(mux)
		}
	}})
	req := httptest.NewRequest("GET", "/api/radio/radio1/pat", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != 404 {
		t.Fatal("default-off route exposed", rec.Code, rec.Body)
	}
}

func TestPatStatusExplicitEnableIsLazyAndDoesNotUseCachedAssignment(t *testing.T) {
	flags := flag.NewFlagSet("fixture", flag.ContinueOnError)
	enabled := patStatusOption(flags)
	if err := flags.Parse([]string{"--pat-native-status"}); err != nil || !*enabled {
		t.Fatal("explicit option not parsed", err)
	}
	records := filepath.Join(t.TempDir(), "absent-records.json")
	api, err := newPatStatusAPI(*enabled, records, patRecords{})
	if err != nil || api == nil {
		t.Fatal("reader preparation failed", api, err)
	}
	if _, err := os.Stat(records); !os.IsNotExist(err) {
		t.Fatal("constructor wrote station records", err)
	}
	server := httpapi.New(httpapi.Config{Auth: patAuth{}, Routes: api.Register})
	req := httptest.NewRequest("GET", "/api/radio/radio1/pat", nil)
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	rec := httptest.NewRecorder()
	server.Handler.ServeHTTP(rec, req)
	if rec.Code != 503 {
		t.Fatal("cached assignment supplied status without fresh evidence", rec.Code, rec.Body)
	}
	if _, err := newPatStatusAPI(true, "relative-records.json", patRecords{}); err == nil {
		t.Fatal("untrusted relative record path accepted")
	}
}
