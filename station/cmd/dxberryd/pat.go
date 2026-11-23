package main

import (
	"flag"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/httpapi"
	"github.com/Dustpan95/DXBerry-Pi/station/internal/pat"
)

func patStatusOption(flags *flag.FlagSet) *bool {
	return flags.Bool("pat-native-status", false, "enable read-only Pat status routes after application/lifecycle acceptance")
}

// Disabled means no reader construction or route. Enabling is still lazy:
// version/process checks occur only on an authenticated explicit read.
func newPatStatusAPI(enabled bool, records string, radios httpapi.RadioRecords) (*httpapi.PatAPI, error) {
	if !enabled {
		return nil, nil
	}
	reader, err := pat.NewReader(records)
	if err != nil {
		return nil, err
	}
	return &httpapi.PatAPI{Radios: radios, Reader: reader}, nil
}
