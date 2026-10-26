//go:build rc3deps

// Package audio carries the station's sound path: the radio's sound server, capture and playback
// pipes, Opus, and the WebRTC bridge to the browser (rc3). The blank imports below pin the two
// modules the package is built on so the module graph is resolved before the code lands (go mod tidy reads every build tag; a
// normal build skips this file).
package audio

import (
	_ "github.com/hraban/opus"
	_ "github.com/pion/webrtc/v4"
)
