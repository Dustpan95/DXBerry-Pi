package rig

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// Commander is the part of Client that capability discovery needs.
type Commander interface {
	Do(ctx context.Context, cmd string, args ...string) (Reply, error)
}

var _ Commander = (*Client)(nil)

// HiddenControl is a control or meter the VFO page does not show, with the
// plain-words reason shown under "Why isn't X here?".
type HiddenControl struct{ Name, Why string }

// FreqRange is one receive range in Hz, inclusive.
type FreqRange struct{ Lo, Hi int64 }

// Caps is what one radio model can do over CAT.
type Caps struct {
	ModelName                                      string
	Modes                                          []string
	VFOs                                           []string
	CanSetSplit, CanGetSplit, CanSetPTT, CanGetPTT bool
	SettableLevels                                 []string
	SettableFuncs                                  []string
	ReadableMeters                                 []string
	Hidden                                         []HiddenControl
	FreqRanges                                     []FreqRange
	Probed                                         time.Time
}

const dummyWhy = "rig control is Hamlib's dummy model: this radio has no CAT"

// probedLevels are read with get_level to see what the radio reports.
var probedLevels = []string{"STRENGTH", "SWR", "ALC", "RFPOWER", "RFPOWER_METER_WATTS", "AF", "RF", "SQL"}

// settableControls are the levels the page offers as controls.
var settableControls = []string{"RFPOWER", "AF", "RF", "SQL"}

// Allows reports whether mode is one of the radio's modes (exact match).
func (c Caps) Allows(mode string) bool { return slices.Contains(c.Modes, mode) }

// InRange reports whether hz lies inside any receive range, bounds included.
func (c Caps) InRange(hz int64) bool {
	for _, r := range c.FreqRanges {
		if hz >= r.Lo && hz <= r.Hi {
			return true
		}
	}
	return false
}

var rangeRe = regexp.MustCompile(`^\s+(\d+) Hz - (\d+) Hz\s*$`)

// parseCaps reads the text of dump_caps. Only unindented lines are the
// top-level fields; indented ones belong to the range blocks.
func parseCaps(lines []string) Caps {
	var c Caps
	inRX := false
	seen := map[FreqRange]bool{}
	for _, l := range lines {
		l = strings.TrimRight(l, "\r")
		if m := rangeRe.FindStringSubmatch(l); m != nil {
			if inRX {
				lo, e1 := strconv.ParseInt(m[1], 10, 64)
				hi, e2 := strconv.ParseInt(m[2], 10, 64)
				r := FreqRange{lo, hi}
				if e1 == nil && e2 == nil && !seen[r] {
					seen[r] = true
					c.FreqRanges = append(c.FreqRanges, r)
				}
			}
			continue
		}
		if l == "" || l[0] == ' ' || l[0] == '\t' {
			continue
		}
		inRX = strings.HasPrefix(l, "RX ranges #") && strings.HasSuffix(l, ":")
		key, val, ok := strings.Cut(l, ":")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch key {
		case "Model name":
			c.ModelName = val
		case "Mode list":
			if c.Modes == nil {
				c.Modes = strings.Fields(val)
			}
		case "VFO list":
			if c.VFOs == nil {
				c.VFOs = strings.Fields(val)
			}
		case "Can set Split VFO":
			c.CanSetSplit = val == "Y"
		case "Can get Split VFO":
			c.CanGetSplit = val == "Y"
		case "Can set PTT":
			c.CanSetPTT = val == "Y"
		case "Can get PTT":
			c.CanGetPTT = val == "Y"
		case "Set level":
			c.SettableLevels = levelNames(val)
		case "Set functions":
			c.SettableFuncs = strings.Fields(val)
		}
	}
	slices.SortFunc(c.FreqRanges, func(a, b FreqRange) int {
		switch {
		case a.Lo != b.Lo:
			return cmp64(a.Lo, b.Lo)
		default:
			return cmp64(a.Hi, b.Hi)
		}
	})
	return c
}

func cmp64(a, b int64) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

// levelNames turns "AF(0..1/0.1) RF(0..1/0.1)" into [AF RF].
func levelNames(s string) []string {
	var out []string
	for _, f := range strings.Fields(s) {
		name, _, _ := strings.Cut(f, "(")
		if name != "" {
			out = append(out, name)
		}
	}
	return out
}

// numericReply reports whether a get_level reply carries a number, with or
// without rigctld's "Level Value:" label.
func numericReply(r Reply) bool {
	for _, l := range r.Lines {
		if i := strings.LastIndex(l, ":"); i >= 0 {
			l = l[i+1:]
		}
		if _, err := strconv.ParseFloat(strings.TrimSpace(l), 64); err == nil {
			return true
		}
	}
	return false
}

// Discover reads the radio's dump_caps and probes which meters it answers. A
// *RigError from a probe means "not readable"; any other error (deadline,
// protocol, closed) aborts so the caller can retry later.
func Discover(ctx context.Context, c Commander) (Caps, error) {
	rep, err := c.Do(ctx, "dump_caps")
	if err != nil {
		return Caps{}, fmt.Errorf("rig: dump_caps: %w", err)
	}
	caps := parseCaps(rep.Lines)
	caps.Probed = time.Now()

	if caps.ModelName == "Dummy" {
		for _, n := range probedLevels {
			caps.Hidden = append(caps.Hidden, HiddenControl{n, dummyWhy})
		}
		caps.Hidden = append(caps.Hidden, HiddenControl{"SPLIT", dummyWhy}, HiddenControl{"VFO", dummyWhy})
		return caps, nil
	}

	for _, n := range probedLevels {
		r, err := c.Do(ctx, "get_level", n)
		var re *RigError
		switch {
		case err == nil && r.RPRT == 0 && numericReply(r):
			caps.ReadableMeters = append(caps.ReadableMeters, n)
			continue
		case err == nil || errors.As(err, &re):
			caps.Hidden = append(caps.Hidden, HiddenControl{n, fmt.Sprintf("this radio does not report %s over CAT", n)})
		default:
			return Caps{}, fmt.Errorf("rig: get_level %s: %w", n, err)
		}
	}
	cannot := func(n string) {
		caps.Hidden = append(caps.Hidden, HiddenControl{n, fmt.Sprintf("this radio cannot set %s over CAT", n)})
	}
	for _, n := range settableControls {
		if !slices.Contains(caps.SettableLevels, n) {
			cannot(n)
		}
	}
	if !caps.CanSetSplit {
		cannot("SPLIT")
	}
	// Two real VFOs are needed to pick between them; MEM is not one.
	if !slices.Contains(caps.VFOs, "VFOA") || !slices.Contains(caps.VFOs, "VFOB") {
		cannot("VFO")
	}
	return caps, nil
}

const capsMaxAge = 7 * 24 * time.Hour

var modelRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// CapsCache keeps one JSON file per radio model under Dir.
type CapsCache struct{ Dir string }

func (cc CapsCache) path(model string) (string, bool) {
	if !modelRe.MatchString(model) {
		return "", false
	}
	return filepath.Join(cc.Dir, "caps-"+model+".json"), true
}

// Load returns the cached caps, or false when missing, unreadable or older
// than a week (by the file or by its Probed stamp).
func (cc CapsCache) Load(model string) (Caps, bool) {
	p, ok := cc.path(model)
	if !ok {
		return Caps{}, false
	}
	st, err := os.Stat(p)
	if err != nil || time.Since(st.ModTime()) > capsMaxAge {
		return Caps{}, false
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return Caps{}, false
	}
	var c Caps
	if json.Unmarshal(b, &c) != nil || c.Probed.IsZero() || time.Since(c.Probed) > capsMaxAge {
		return Caps{}, false
	}
	return c, true
}

// Store writes caps for model, mode 0644, replacing any earlier file.
func (cc CapsCache) Store(model string, c Caps) error {
	p, ok := cc.path(model)
	if !ok {
		return fmt.Errorf("rig: bad model %q for the caps cache", model)
	}
	if err := os.MkdirAll(cc.Dir, 0o755); err != nil {
		return err
	}
	b, err := json.Marshal(c)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(cc.Dir, ".caps-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(b); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp.Name(), 0o644); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), p)
}
