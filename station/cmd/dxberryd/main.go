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
	"syscall"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/auth"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/cli"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/httpapi"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/radios"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/rig"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/session"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/status"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/term"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/tlscert"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/vnc"
	"github.com/Dustpan95/DXBerry-Pi/station/web"
)

type wallClock struct{}

func (wallClock) Now() time.Time { return time.Now() }

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
	owners.SetTransmitting(func(radio string) bool {
		p, ok := rigs.Radio(radio)
		return ok && p.State().PTT
	})
	radioAPI := &httpapi.RadioAPI{Auth: mgr, Radios: rw, Rigs: httpapi.ManagerRigs(rigs), Owners: owners}
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
	go st.Run(statusCtx)
	go rw.Run(statusCtx)
	go rigs.Run(statusCtx)
	go programs.Run(statusCtx, 2*time.Second)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGTERM, syscall.SIGINT)
	done := make(chan struct{})
	go func() {
		<-stop
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(ctx)
		close(done)
	}()
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
