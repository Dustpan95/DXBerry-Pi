// Package web carries the station app's pages, compiled into the dxberryd binary.
package web

import "embed"

// Files is the web tree. Add a pattern here when a page or folder joins it.
//
//go:embed *.html
var Files embed.FS
