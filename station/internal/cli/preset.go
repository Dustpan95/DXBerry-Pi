package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"path/filepath"

	"github.com/Dustpan95/DXBerry-Pi/station/internal/presets"
)

// SetPresetState configures the helper's state directory from trusted server
// configuration. No call argument or request field can override this path.
// New leaves preset calls disabled until the server supplies its state path.
func (c *CLI) SetPresetState(state string) error {
	if !filepath.IsAbs(state) || filepath.Clean(state) != state {
		return errors.New("invalid preset state directory")
	}
	c.mu.Lock()
	c.presetState = state
	c.mu.Unlock()
	return nil
}

func (c *CLI) validatePreset(call Call) error {
	if call.Command != "dxberry-preset" {
		return nil
	}
	c.mu.Lock()
	enabled := c.presetState != ""
	c.mu.Unlock()
	if !enabled {
		return &Refusal{0, "preset definitions are not configured", "preset definitions are not configured"}
	}
	if _, err := presets.DecodeRequest([]byte(call.Stdin)); err != nil {
		return &Refusal{0, "invalid preset input", "invalid preset input"}
	}
	return nil
}

func (c *CLI) executionArgs(call Call) []string {
	if call.Command != "dxberry-preset" {
		return call.Args
	}
	c.mu.Lock()
	state := c.presetState
	c.mu.Unlock()
	return []string{"--json", "--state", state}
}

type callBody struct {
	Args  []string `json:"args"`
	Stdin string   `json:"stdin"`
}

// The preset wrapper is strict at both JSON layers. Otherwise encoding/json
// could repair malformed outer Unicode before the definition decoder sees it.
func decodePresetBody(raw []byte) (callBody, error) {
	bad := errors.New("bad request body")
	if !presets.ValidJSONEncoding(raw) {
		return callBody{}, bad
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	token, err := d.Token()
	if err != nil || token != json.Delim('{') {
		return callBody{}, bad
	}
	seen := map[string]bool{}
	var body callBody
	for d.More() {
		token, err = d.Token()
		key, ok := token.(string)
		if err != nil || !ok || seen[key] {
			return callBody{}, bad
		}
		seen[key] = true
		switch key {
		case "args":
			token, err = d.Token()
			if err != nil || token != json.Delim('[') {
				return callBody{}, bad
			}
			body.Args = []string{}
			for d.More() {
				if len(body.Args) >= 1 {
					return callBody{}, bad
				}
				token, err = d.Token()
				arg, ok := token.(string)
				if err != nil || !ok {
					return callBody{}, bad
				}
				body.Args = append(body.Args, arg)
			}
			token, err = d.Token()
			if err != nil || token != json.Delim(']') {
				return callBody{}, bad
			}
		case "stdin":
			token, err = d.Token()
			text, ok := token.(string)
			if err != nil || !ok {
				return callBody{}, bad
			}
			body.Stdin = text
		default:
			return callBody{}, bad
		}
	}
	token, err = d.Token()
	if err != nil || token != json.Delim('}') || len(seen) != 2 || !seen["args"] || !seen["stdin"] {
		return callBody{}, bad
	}
	if _, err = d.Token(); err != io.EOF {
		return callBody{}, bad
	}
	return body, nil
}
