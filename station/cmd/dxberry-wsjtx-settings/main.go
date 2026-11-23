package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/wsjtx"
)

func main() {
	flags := flag.NewFlagSet("dxberry-wsjtx-settings", flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	home := flags.String("home", "/var/lib/dxberry-apps", "application settings home (trusted parent argument)")
	if err := flags.Parse(os.Args[1:]); err != nil || flags.NArg() != 0 {
		os.Exit(2)
	}
	if err := wsjtx.ServeSettingsBridge(os.Stdin, os.Stdout, *home); err != nil {
		fmt.Fprintln(os.Stderr, "WSJT-X settings helper refused or could not complete its private exchange")
		os.Exit(1)
	}
}
