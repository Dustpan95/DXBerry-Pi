// Command dxberryd is the DXBerry-Pi station app: one service behind nginx.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/auth"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/cli"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/httpapi"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/vnc"
	"github.com/Dustpan95/DXBerry-Pi/station/web"
)

// version is set at build time (-X main.version=...).
var version = "dev"

func main() {
	listen := flag.String("listen", "127.0.0.1:8090", "address nginx proxies to")
	state := flag.String("state", "/var/lib/dxberry/station", "state directory")
	bin := flag.String("bin", "/opt/dxberry/bin", "directory of the dxberry-* commands")
	screens := flag.String("screens", "/run/dxberry/screens", "directory of the program windows' unix sockets")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, nil)))
	mgr := auth.New(auth.PAM{Service: "dxberry"}, auth.DefaultGroups(), time.Now)
	runner := cli.New(*bin, nil)
	routes := func(mux *http.ServeMux) {
		mgr.Register(mux)
		runner.Register(mux)
		mux.Handle("GET /api/vnc/{session}", vnc.Handler(*screens, func(ctx context.Context, session string) bool {
			res, err := runner.Run(ctx, cli.Call{Command: "systemctl", Args: []string{"is-active", "dxberry-screen@" + session + ".service"}})
			return err == nil && res.Exit == 0
		}))
	}
	srv := httpapi.New(httpapi.Config{Listen: *listen, Web: web.Files, Auth: mgr, Version: version, Routes: routes})
	log.Printf("dxberryd %s listening on %s (state %s, bin %s)", version, *listen, *state, *bin)

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
