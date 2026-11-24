//go:build browserfixture

package httpapi

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/audio"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/rig"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/session"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/settings"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/status"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/wsjtx"
	"github.com/Dustpan95/DXBerry-Pi/station/web"
)

type browserAudioBridge struct{ *audio.Bridge }

type browserSoundServer struct{ ptServer }

func (s *browserSoundServer) LoadTXLoopback(ctx context.Context, radio string) (int, error) {
	id, err := s.ptServer.LoadTXLoopback(ctx, radio)
	fmt.Println("fixture loopback load", id)
	return id, err
}

func (s *browserSoundServer) UnloadStaleTXLoopbacks(ctx context.Context, radio string) (int, error) {
	n, err := s.ptServer.UnloadStaleTXLoopbacks(ctx, radio)
	fmt.Println("fixture loopback clear", n)
	return n, err
}

func (b browserAudioBridge) AddICECandidate(tab string, c ICECandidate) error {
	return b.Bridge.AddICECandidate(tab, audio.Candidate(c))
}

// TestBrowserFixture is an opt-in, loopback-only station for real browser checks.
// The HTTP, ownership, PTT and WebRTC implementations are production components.
// The radio writer and sound-device pipes are fixtures; no hardware is opened.
func TestBrowserFixture(t *testing.T) {
	if os.Getenv("DXB_BROWSER_FIXTURE") != "1" {
		t.Skip("requires the isolated browser test runner")
	}
	report, err := os.ReadFile(os.Getenv("DXB_BROWSER_STATUS"))
	if err != nil || !json.Valid(report) {
		t.Fatalf("status fixture: %v", err)
	}
	dir := t.TempDir()
	pcm := make([]byte, audio.FrameSamples*2)
	for i := 0; i < audio.FrameSamples; i++ {
		v := int16(6000 * math.Sin(2*math.Pi*1000*float64(i)/audio.SampleRate))
		binary.LittleEndian.PutUint16(pcm[i*2:], uint16(v))
	}
	tone := filepath.Join(dir, "tone.raw")
	playFile := filepath.Join(dir, "play.raw")
	if err := os.WriteFile(tone, pcm, 0o600); err != nil {
		t.Fatal(err)
	}
	bins := map[string]string{}
	for name, body := range map[string]string{
		"parec": "#!/bin/bash\nwhile :; do cat \"$DXB_TONE\" || exit 1; sleep 0.02; done\n",
		"pacat": "#!/bin/bash\ncat > \"$DXB_PLAY\"\n",
	} {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(body), 0o700); err != nil {
			t.Fatal(err)
		}
		bins[name] = p
	}
	env := []string{"DXB_TONE=" + tone, "DXB_PLAY=" + playFile}
	capture := audio.NewCapture("fixture", "RADIO1.in")
	capture.Bins, capture.Env = bins, env
	play := audio.NewPlayback("fixture", "RADIO1.tx")
	play.Bins, play.Env = bins, env
	bridge := audio.NewBridge("radio1", capture, play, audio.DefaultPorts, slog.New(slog.NewTextHandler(io.Discard, nil)))
	bridge.Loopback = true
	t.Cleanup(bridge.Shutdown)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	clock := realNow{}
	auth := rwAuth{ok: true}
	owners := session.NewOwners(clock)
	radio := &rwRig{st: rig.State{Connected: true, Freq: 14074000, Mode: "USB",
		Levels: map[string]float64{}, Meters: map[string]float64{}, Optimistic: map[string]bool{}}}
	records := rwRecords{"radio1": radios.Radio{Name: "radio1", Label: "Fixture radio", Port: 4532, PTT: "rigctld", HasCAT: true}}
	if os.Getenv("DXB_BROWSER_PROGRAM") == "wsjtx" {
		r := records["radio1"]
		r.Owner = "wsjtx"
		records["radio1"] = r
		programs := session.NewPrograms(func() []radios.Radio { return []radios.Radio{r} },
			func(context.Context, string) (bool, string, error) { return true, "WSJT-X", nil })
		programs.Refresh(ctx)
		owners.SetPrograms(programs)
	}
	sound := &browserSoundServer{ptServer: ptServer{ok: true}}
	stream := status.New(func(context.Context) (string, error) { return string(report), nil }, nil)
	var native *browserProgramSource
	if os.Getenv("DXB_BROWSER_PROGRAM") == "wsjtx" {
		native = &browserProgramSource{}
		events := wsjtx.NewProgramEvents(native.read, func(name string) bool { return name == "radio1" })
		stream.SetPrograms(events)
		stream.SetRadios(browserProgramRadios{records["radio1"]})
		go events.Run(ctx)
	}
	go stream.Run(ctx)
	stream.OnSocket(func(r *http.Request) func() {
		tab := r.URL.Query().Get("tab")
		if !ValidTab(tab) {
			return nil
		}
		id := session.TabID("sess-secret", tab)
		owners.SocketOpened(id)
		return func() { owners.SocketClosed(id) }
	})
	conf := settings.Defaults()
	conf.PTTTimeoutS = settings.MinPTT
	store := &settings.Store{Dir: filepath.Join(dir, "settings")}
	if err := store.Set("radio1", conf); err != nil {
		t.Fatal(err)
	}
	settingsAPI := &RadioSettingsAPI{Radios: records, Settings: store}
	api := &RadioAPI{Auth: auth, Radios: records, Rigs: rwRigs{radio}, Owners: owners, Ctx: ctx,
		Available: sound.Available, Notify: stream.Notify, Settings: store.Get,
		Audio: func(string) PTTAudio { return PTTAudio{Bridge: bridge, Server: sound} }}
	owners.SetTransmitting(func(name string) bool { return api.PTTOn(name) })
	audioAPI := &AudioAPI{Auth: auth, Radios: records, Available: sound.Available,
		Alive:  func(*http.Request) bool { return true },
		Bridge: func(string) (AudioBridge, bool) { return browserAudioBridge{bridge}, true }}
	done := make(chan struct{})
	var finish sync.Once
	routes := func(mux *http.ServeMux) {
		if native != nil {
			native.register(mux, stream)
		}
		api.Register(mux)
		settingsAPI.Register(mux)
		audioAPI.Register(mux)
		stream.Register(mux, func(*http.Request) bool { return true })
		mux.HandleFunc("POST /api/cli/{command}", func(w http.ResponseWriter, r *http.Request) {
			var call struct {
				Args []string `json:"args"`
			}
			_ = json.NewDecoder(r.Body).Decode(&call)
			out := `{}`
			switch r.PathValue("command") {
			case "dxberry-status":
				out = string(report)
			case "dxberry-radio":
				out = `[]`
			case "dxberry-config":
				out = `{"keys":{},"valid":true,"errors":[],"network_editable":false,"job":null}`
			case "dxberry-update":
				out = `{"job":null,"system":{"count":0,"packages":[]},"apps":{}}`
			}
			writeJSON(w, http.StatusOK, map[string]any{"exit": 0, "stdout": out, "stderr": ""})
		})
		mux.HandleFunc("GET /fixture/state", func(w http.ResponseWriter, r *http.Request) {
			sound.mu.Lock()
			loaded := sound.loaded
			sound.mu.Unlock()
			radio.mu.Lock()
			calls := append([]string{}, radio.calls...)
			keyed := radio.st.PTT
			radio.mu.Unlock()
			var bytes int64
			if st, err := os.Stat(playFile); err == nil {
				bytes = st.Size()
			}
			writeJSON(w, http.StatusOK, map[string]any{"ptt": api.PTTOn("radio1"), "keyed": keyed, "loopbacks": loaded, "calls": calls, "play_bytes": bytes})
		})
		mux.HandleFunc("POST /fixture/drop-audio", func(w http.ResponseWriter, r *http.Request) {
			var body struct {
				Tab string `json:"tab"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			bridge.Close(session.TabID("sess-secret", body.Tab))
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		})
		mux.HandleFunc("POST /fixture/done", func(w http.ResponseWriter, r *http.Request) {
			finish.Do(func() { close(done) })
			writeJSON(w, http.StatusOK, map[string]bool{"ok": true})
		})
	}
	srv := New(Config{Listen: "127.0.0.1:8090", Web: web.Files, Auth: auth, Dev: true, Routes: routes})
	defer srv.Close()
	errs := make(chan error, 1)
	go func() { errs <- srv.ListenAndServe() }()
	fmt.Println("browser fixture listening on 127.0.0.1:8090")
	select {
	case <-done:
	case err := <-errs:
		t.Fatal(err)
	case <-time.After(2 * time.Minute):
		t.Fatal("browser fixture timed out")
	}
}
