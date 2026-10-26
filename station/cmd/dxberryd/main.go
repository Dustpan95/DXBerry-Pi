// Command dxberryd is the DXBerry-Pi station app: one service behind nginx.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/audio"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/auth"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/cli"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/httpapi"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/rig"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/session"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/settings"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/status"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/term"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/tlscert"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/vnc"
	"github.com/Dustpan95/DXBerry-Pi/station/web"
)

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

// audioBridge is an audio.Bridge as the signalling socket takes it.
type audioBridge struct{ *audio.Bridge }

func (b audioBridge) AddICECandidate(tab string, c httpapi.ICECandidate) error {
	return b.Bridge.AddICECandidate(tab, audio.Candidate(c))
}

// wireRadioRemoval forgets a radio's PTT and intents (forget: RadioAPI.Forget), then ends its
// WebRTC bridge (and every connection on it), when the radio leaves the records. Forget comes
// first: the PTT window closes and lets go of the bridge before the bridge goes, and a radio added
// again under the same name gets a PTT on its new bridge.
func wireRadioRemoval(m *rig.Manager, forget func(radio string), bridges *audio.Bridges) {
	m.OnRemoved = func(radio string) {
		forget(radio)
		bridges.Remove(radio)
	}
}

// version is set at build time (-X main.version=...).
var version = "dev"

func main() {
	listen := flag.String("listen", "127.0.0.1:8090", "address nginx proxies to")
	state := flag.String("state", "/var/lib/dxberry/station", "state directory")
	bin := flag.String("bin", "/opt/dxberry/bin", "directory of the dxberry-* commands")
	tlsDir := flag.String("tls", "/etc/dxberry/tls", "directory of station.crt and station.key")
	screens := flag.String("screens", "/run/dxberry/screens", "directory of the program windows' unix sockets")
	rigCaps := flag.String("rigcaps", "", "radio capability cache directory (default <state>/rigcaps)")
	radiosPath := flag.String("radios", "/var/lib/dxberry/radios.json", "radio records file")
	audioRun := flag.String("audio-run", audio.DefaultRunDir, "directory of the radios' sound server sockets")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	mgr := auth.New(auth.PAM{Service: "dxberry"}, auth.DefaultGroups(), time.Now)
	runner := cli.New(*bin, nil)
	st := status.New(func(ctx context.Context) (string, error) {
		res, err := runner.Run(ctx, cli.Call{Command: "dxberry-status", Args: []string{"--json"}})
		if err == nil && res.Exit != 0 {
			err = fmt.Errorf("dxberry-status exited %d", res.Exit)
		}
		return res.Stdout, err
	}, nil)
	rw := radios.New(*radiosPath, 0)
	st.SetRadios(rw)
	if *rigCaps == "" {
		*rigCaps = filepath.Join(*state, "rigcaps")
	}
	rigs := rig.NewManager(rw, rig.CapsCache{Dir: *rigCaps}, nil, slog.Default())
	st.SetRig(rigs)
	owners := session.NewOwners(wallClock{})
	programs := session.NewPrograms(rw.List, session.ScreenProbe(func(ctx context.Context, args []string) (string, int, error) {
		res, err := runner.Run(ctx, cli.Call{Command: "dxberry-app", Args: args})
		return res.Stdout, res.Exit, err
	}))
	owners.SetPrograms(programs)
	radioStore := &settings.Store{Dir: filepath.Join(*state, "radios")}
	radioSettings := &httpapi.RadioSettingsAPI{Radios: rw, Settings: radioStore,
		Probe: httpapi.ManagerProbe(rigs, rig.CapsCache{Dir: *rigCaps})}
	// a carrier keyed when the service died is owed its unkey at the next start (<state>/keyed);
	// PTT notices go to every tab
	radioAPI := &httpapi.RadioAPI{Auth: mgr, Radios: rw, Rigs: httpapi.ManagerRigs(rigs), Owners: owners,
		Marks: &rig.KeyedMarks{Dir: filepath.Join(*state, "keyed")}, Settings: radioStore.Get, Notify: st.Notify}
	// a radio is transmitting while its PTT reads on or a PTT window is open (vox keys no PTT line)
	owners.SetTransmitting(func(radio string) bool {
		p, ok := rigs.Radio(radio)
		return ok && p.State().PTT || radioAPI.PTTOn(radio)
	})
	// a tab's claim ends with its events socket (the page opens it with ?tab=<its token>)
	st.OnSocket(func(r *http.Request) func() {
		s, ok := mgr.Session(r)
		tab := r.URL.Query().Get("tab")
		if !ok || !httpapi.ValidTab(tab) {
			return nil
		}
		id := session.TabID(s.ID, tab)
		owners.SocketOpened(id)
		return func() { owners.SocketClosed(id) }
	})
	// browser audio: one WebRTC bridge per radio on the radio's sound server
	audioCtl := audio.NewServerControl(runner, *audioRun)
	audioCtl.Radios = rw
	bridges := audio.NewBridges(audioCtl, audio.DefaultPorts, slog.Default())
	audioAPI := &httpapi.AudioAPI{Auth: mgr, Radios: rw, Available: audioCtl.Available, Alive: mgr.Alive,
		Bridge: func(radio string) (httpapi.AudioBridge, bool) { return audioBridge{bridges.Get(radio)}, true }}
	// PTT: the radio's bridge lets the owner's microphone through, the sound server loops <CARD>.tx
	// into the card, and a digirig_tone radio is keyed by a stereo tone into <CARD>.tx
	radioAPI.Available = audioCtl.Available
	radioAPI.Audio = func(radio string) httpapi.PTTAudio {
		return httpapi.PTTAudio{Bridge: bridges.Get(radio), Server: audioCtl, Tone: func() rig.TonePlayer {
			p := audio.NewPlayback(audioCtl.Server(radio), audio.Card(radio)+".tx")
			p.Bins, p.Env, p.Channels = audioCtl.Bins, audioCtl.Env, 2
			return p
		}}
	}
	// the SWR guard listens to each radio's poller; the Radios page asks whether a radio has one
	rigs.OnStart = func(radio string, p *rig.Poller) { p.SetHooks(radioAPI.PollerHooks(radio)) }
	radioSettings.HasSWRGuard = func(radio string) bool {
		g := radioAPI.GuardOf(radio)
		if g == nil {
			return false
		}
		active, _, _ := g.View()
		return active
	}
	wireRadioRemoval(rigs, radioAPI.Forget, bridges)
	runner.OnCall = func(cmd string, args []string) {
		if status.ChangesState(cmd, args) {
			st.Poke()
		}
	}
	ensureCertificate(*tlsDir, runner)
	routes := func(mux *http.ServeMux) {
		mgr.Register(mux)
		runner.Register(mux)
		st.Register(mux, mgr.Alive)
		radioAPI.Register(mux)
		radioSettings.Register(mux)
		audioAPI.Register(mux)
		sessionOf := func(r *http.Request) *httpapi.Session {
			s, _ := mgr.Session(r)
			return s
		}
		mux.Handle("GET /api/term", term.Handler(
			func(user string) []string { return []string{"/bin/login", "-f", user} },
			func(r *http.Request) string {
				if s := sessionOf(r); s != nil {
					return s.User
				}
				return ""
			},
			func(r *http.Request) string {
				if s := sessionOf(r); s != nil {
					return s.ID
				}
				return ""
			},
			mgr.Alive))
		mux.Handle("GET /api/vnc/{session}", vnc.Handler(*screens, func(ctx context.Context, session string) bool {
			res, err := runner.Run(ctx, cli.Call{Command: "systemctl", Args: []string{"is-active", "dxberry-screen@" + session + ".service"}})
			return err == nil && res.Exit == 0
		}, mgr.Alive))
	}
	srv := httpapi.New(httpapi.Config{Listen: *listen, Web: web.Files, Auth: mgr, Version: version, CertFile: filepath.Join(*tlsDir, "station.crt"), Routes: routes})
	log.Printf("dxberryd %s listening on %s (state %s, bin %s)", version, *listen, *state, *bin)

	statusCtx, stopStatus := context.WithCancel(context.Background())
	defer stopStatus()
	radioAPI.Ctx = statusCtx // owed-unkey retries end with the service
	go st.Run(statusCtx)
	go rw.Run(statusCtx)
	go rigs.Run(statusCtx)
	go programs.Run(statusCtx, 2*time.Second)
	radioAPI.OweMarkedCarriers()

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	done := make(chan struct{})
	go func() {
		<-stop
		// no new intents first, then every PTT window is closed and every carrier we keyed or owe
		// is unkeyed (the rig layer still runs: statusCtx ends only when main returns)
		httpapi.StopService(srv, 5*time.Second, radioAPI)
		bridges.Shutdown()
		close(done)
	}()
	// a TX loopback left loaded by a crash (the sound server outlives dxberryd) is unloaded before
	// the station takes a request, so the clear can never take a first press's own loopback
	clearStaleLoopbacks(statusCtx, audioCtl, rw.List())
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
	<-done
}

// ensureCertificate makes the self-signed certificate on first start (hostname, dxberry.local and
// every non-loopback IPv4 as SANs) and has nginx pick it up when it was just made. Never fatal:
// without it nginx simply cannot serve TLS yet and the next start tries again.
func ensureCertificate(dir string, runner *cli.CLI) {
	hosts := []string{"dxberry.local"}
	if h, err := os.Hostname(); err == nil && h != "" {
		hosts = append(hosts, h)
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && n.IP.To4() != nil && !n.IP.IsLoopback() {
				hosts = append(hosts, n.IP.String())
			}
		}
	}
	changed, err := tlscert.Ensure(dir, hosts)
	if err != nil {
		slog.Error("certificate", "error", err)
		return
	}
	if !changed {
		return
	}
	slog.Info("made a new station certificate", "dir", dir)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if res, err := runner.Run(ctx, cli.Call{Command: "systemctl", Args: []string{"reload", "nginx.service"}}); err != nil || res.Exit != 0 {
		slog.Warn("nginx did not reload after the new certificate", "error", err)
	}
}

// clearStaleLoopbacks unloads the TX loopbacks of every radio whose sound server is up: one left
// by a crash would loop <CARD>.tx into the card beside the next PTT window's own. The radios are
// cleared at once, each within 5 s, and it returns when all are done.
func clearStaleLoopbacks(ctx context.Context, ctl *audio.ServerControl, list []radios.Radio) {
	var wg sync.WaitGroup
	for _, r := range list {
		if _, err := os.Stat(ctl.Socket(r.Name)); err != nil {
			continue // no sound server: nothing loaded
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
			defer cancel()
			if _, err := ctl.UnloadStaleTXLoopbacks(cctx, r.Name); err != nil {
				slog.Warn("could not clear old TX loopbacks at start", "radio", r.Name, "error", err)
			}
		}()
	}
	wg.Wait()
}
