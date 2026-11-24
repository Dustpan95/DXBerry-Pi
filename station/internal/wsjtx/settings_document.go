package wsjtx

import (
	"bytes"
	"strconv"
	"strings"
	"unicode/utf8"
)

const (
	MaxSettingsBytes = 1024 * 1024
	maxSettingsLines = 16384
	maxSettingsLine  = 64 * 1024
)

// SettingsDocument is a bounded, immutable LF-terminated Qt INI document.
// Only a conservative canonical subset is accepted for routing/profile names;
// unrelated values remain opaque. It starts no program and performs no I/O.
type SettingsDocument struct {
	data       []byte
	settings   RoutingSettings
	lines      []string
	serverLine int
	portLine   int
	configEnd  int
}

// decodedININame decodes only the ASCII Qt key-name subset. Unsupported
// non-ASCII encodings are refused, never discarded. iniName below also
// normalizes separators when checking for aliases.
func decodedININame(raw string) (string, bool) {
	var out strings.Builder
	for i := 0; i < len(raw); i++ {
		c := raw[i]
		if c == '%' {
			n := 2
			if i+1 < len(raw) && raw[i+1] == 'U' {
				n = 5
			}
			if i+n >= len(raw) {
				return "", false
			}
			start, digits := i+1, n
			if n == 5 {
				start++
				digits--
			}
			for _, h := range raw[start : start+digits] {
				if !strings.ContainsRune("0123456789abcdefABCDEF", h) {
					return "", false
				}
			}
			v, err := strconv.ParseUint(raw[start:start+digits], 16, 16)
			if err != nil || v == 0 || v > 127 {
				return "", false
			}
			c = byte(v)
			i += n
		}
		if c < 32 || c > 126 {
			return "", false
		}
		out.WriteByte(c)
	}
	return out.String(), true
}

func iniName(raw string) (string, bool) {
	decoded, ok := decodedININame(raw)
	if !ok {
		return "", false
	}
	parts := strings.FieldsFunc(decoded, func(r rune) bool { return r == '/' || r == '\\' })
	return strings.Join(parts, "/"), true
}

func singleLineValue(value string) bool {
	quoted, escaped := false, false
	for _, c := range value {
		if escaped {
			escaped = false
			continue
		}
		if c == '\\' {
			escaped = true
			continue
		}
		if c == '"' {
			quoted = !quoted
		}
	}
	return !quoted
}

func canonicalProfile(raw string) (string, bool) {
	// CurrentName is a string value, not a key path. Reject escapes, quoting,
	// lists and special QVariant syntax until their semantics are supported.
	if !routingName(raw) || strings.ContainsAny(raw, "\";,@=") || strings.TrimSpace(raw) != raw {
		return "", false
	}
	for _, c := range raw {
		if c < 32 || c > 126 {
			return "", false
		}
	}
	return raw, true
}

// ParseSettingsDocument verifies the whole key namespace, including saved
// profiles and root-group aliases. Legacy migration markers, duplicate decoded
// keys/sections, continuations, unsupported names, CRLF/BOM and unterminated
// files are errors. Values of unrelated keys are retained byte-for-byte.
func ParseSettingsDocument(data []byte) (*SettingsDocument, error) {
	if len(data) == 0 || len(data) > MaxSettingsBytes || data[len(data)-1] != '\n' || !utf8.Valid(data) || bytes.ContainsAny(data, "\x00\r") || bytes.Contains(data, []byte{0xef, 0xbb, 0xbf}) {
		return nil, ErrRoutingLayout
	}
	if bytes.Count(data, []byte{'\n'}) > maxSettingsLines {
		return nil, ErrRoutingLayout
	}
	lines := strings.SplitAfter(string(data), "\n")
	lines = lines[:len(lines)-1]
	if len(lines) > maxSettingsLines {
		return nil, ErrRoutingLayout
	}
	d := &SettingsDocument{data: bytes.Clone(data), lines: lines, serverLine: -1, portLine: -1, configEnd: -1}
	d.settings.Saved = make(map[string]UDPSettings)
	keys := make(map[string]bool)
	groups := make(map[string]bool)
	group, rawGroup := "", ""
	configHeader, profileFound := false, false
	for i, physical := range lines {
		line := strings.TrimSuffix(physical, "\n")
		if len(line) > maxSettingsLine {
			return nil, ErrRoutingLayout
		}
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, ";") || strings.HasPrefix(trimmed, "#") {
			continue
		}
		tail := strings.TrimRight(line, " \t")
		slashes := len(tail) - len(strings.TrimRight(tail, "\\"))
		if slashes%2 != 0 {
			return nil, ErrRoutingLayout
		}
		if strings.HasPrefix(trimmed, "[") {
			if group == "Configuration" {
				d.configEnd = i
			}
			if line != trimmed || !strings.HasSuffix(line, "]") || strings.Count(line, "]") != 1 {
				return nil, ErrRoutingLayout
			}
			rawGroup = line[1 : len(line)-1]
			var ok bool
			if rawGroup == "General" {
				group, ok = "", true
			} else if rawGroup == "%General" {
				group, ok = "General", true
			} else {
				group, ok = iniName(rawGroup)
			}
			if !ok || group == "" && rawGroup != "General" || group == "General" && rawGroup != "%General" {
				return nil, ErrRoutingLayout
			}
			if groups[group] {
				return nil, ErrRoutingLayout
			}
			groups[group] = true
			if group == "Configuration" {
				if rawGroup != "Configuration" {
					return nil, ErrRoutingLayout
				}
				configHeader = true
				d.configEnd = len(lines)
			}
			if (group == "MultiSettings" || strings.HasPrefix(group, "MultiSettings/")) && rawGroup != "MultiSettings" {
				return nil, ErrRoutingLayout
			}
			continue
		}
		eq := strings.IndexByte(line, '=')
		if eq <= 0 {
			return nil, ErrRoutingLayout
		}
		rawKey, value := line[:eq], line[eq+1:]
		if !singleLineValue(value) {
			return nil, ErrRoutingLayout
		}
		key, ok := iniName(strings.TrimSpace(rawKey))
		if !ok || key == "" {
			return nil, ErrRoutingLayout
		}
		full := key
		if group != "" {
			full = group + "/" + key
		}
		if keys[full] {
			return nil, ErrRoutingLayout
		}
		keys[full] = true
		if full == "CurrentMultiSettingsConfiguration" {
			return nil, ErrRoutingLayout
		}
		switch full {
		case "Configuration/UDPServer", "Configuration/UDPServerPort":
			wanted := strings.TrimPrefix(full, "Configuration/")
			if rawGroup != "Configuration" || rawKey != wanted {
				return nil, ErrRoutingLayout
			}
			if wanted == "UDPServer" {
				d.settings.Current.Server = RawSetting{true, value}
				d.serverLine = i
			} else {
				d.settings.Current.Port = RawSetting{true, value}
				d.portLine = i
			}
		case "MultiSettings/CurrentName":
			if rawGroup != "MultiSettings" || rawKey != "CurrentName" {
				return nil, ErrRoutingLayout
			}
			profile, ok := canonicalProfile(value)
			if !ok {
				return nil, ErrRoutingLayout
			}
			d.settings.Profile = profile
			profileFound = true
		default:
			if !strings.HasPrefix(full, "MultiSettings/") {
				continue
			}
			// Canonical Qt puts profile paths as backslash-separated keys in
			// this exact section; root and escaped-section aliases are refused.
			if rawGroup != "MultiSettings" || rawKey != strings.TrimSpace(rawKey) {
				return nil, ErrRoutingLayout
			}
			parts := strings.Split(strings.TrimPrefix(full, "MultiSettings/"), "/")
			if len(parts) < 2 || parts[0] == "CurrentName" {
				return nil, ErrRoutingLayout
			}
			// Do not normalize empty/encoded separators into a different Qt
			// profile inventory. Every physical path component must be flat.
			physicalParts := strings.Split(rawKey, "\\")
			if len(physicalParts) != len(parts) {
				return nil, ErrRoutingLayout
			}
			for j, rawPart := range physicalParts {
				decoded, ok := decodedININame(rawPart)
				if !ok || decoded == "" || strings.ContainsAny(decoded, "/\\") || decoded != parts[j] {
					return nil, ErrRoutingLayout
				}
			}
			name := parts[0]
			if _, ok := canonicalProfile(name); !ok {
				return nil, ErrRoutingLayout
			}
			pair := d.settings.Saved[name]
			if len(parts) == 3 && parts[1] == "Configuration" && (parts[2] == "UDPServer" || parts[2] == "UDPServerPort") {
				// An encoded alias must not be accepted as a canonical edit target.
				prefix, sep, found := strings.Cut(rawKey, "\\")
				decoded, valid := iniName(prefix)
				if !found || !valid || decoded != name || sep != "Configuration\\"+parts[2] {
					return nil, ErrRoutingLayout
				}
				if parts[2] == "UDPServer" {
					pair.Server = RawSetting{true, value}
				} else {
					pair.Port = RawSetting{true, value}
				}
			}
			d.settings.Saved[name] = pair
			if len(d.settings.Saved) > MaxSavedProfiles {
				return nil, ErrRoutingLayout
			}
		}
	}
	if !configHeader || !profileFound {
		return nil, ErrRoutingLayout
	}
	if err := d.settings.validate(); err != nil {
		return nil, err
	}
	return d, nil
}

func (d *SettingsDocument) Routing() RoutingSettings { return cloneRouting(d.settings) }
func (d *SettingsDocument) Bytes() []byte            { return bytes.Clone(d.data) }

// WithPatch edits/deletes only the two canonical current UDP lines, adding
// absent fields before the next section. Every unrelated byte is preserved.
func (d *SettingsDocument) WithPatch(p RoutingPatch) (*SettingsDocument, error) {
	settings, err := p.Apply(d.settings)
	if err != nil {
		return nil, err
	}
	var out strings.Builder
	for i := 0; i <= len(d.lines); i++ {
		if i == d.configEnd {
			if d.serverLine < 0 && settings.Current.Server.Present {
				out.WriteString("UDPServer=" + settings.Current.Server.Value + "\n")
			}
			if d.portLine < 0 && settings.Current.Port.Present {
				out.WriteString("UDPServerPort=" + settings.Current.Port.Value + "\n")
			}
		}
		if i == len(d.lines) {
			break
		}
		switch i {
		case d.serverLine:
			if settings.Current.Server.Present {
				out.WriteString("UDPServer=" + settings.Current.Server.Value + "\n")
			}
		case d.portLine:
			if settings.Current.Port.Present {
				out.WriteString("UDPServerPort=" + settings.Current.Port.Value + "\n")
			}
		default:
			out.WriteString(d.lines[i])
		}
	}
	result, err := ParseSettingsDocument([]byte(out.String()))
	if err != nil {
		return nil, err
	}
	if result.settings.fingerprint() != settings.fingerprint() {
		return nil, ErrRoutingLayout
	}
	return result, nil
}
