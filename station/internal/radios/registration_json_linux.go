package radios

import (
	"encoding/json"
	"strconv"
	"unicode/utf8"
)

// encoding/json repairs unpaired surrogate escapes; identity reads must refuse
// ambiguous input instead. JSON syntax is checked separately by the token walk.
func registrationEncoding(raw []byte) bool {
	if len(raw) > registrationLimit || !utf8.Valid(raw) {
		return false
	}
	for i := 0; i < len(raw); {
		if raw[i] != '\\' {
			i++
			continue
		}
		if i+1 >= len(raw) {
			return false
		}
		if raw[i+1] != 'u' {
			i += 2
			continue
		}
		if i+6 > len(raw) {
			return false
		}
		n, err := strconv.ParseUint(string(raw[i+2:i+6]), 16, 16)
		if err != nil || n >= 0xdc00 && n <= 0xdfff {
			return false
		}
		if n >= 0xd800 && n <= 0xdbff {
			if i+12 > len(raw) || raw[i+6] != '\\' || raw[i+7] != 'u' {
				return false
			}
			low, err := strconv.ParseUint(string(raw[i+8:i+12]), 16, 16)
			if err != nil || low < 0xdc00 || low > 0xdfff {
				return false
			}
			i += 12
		} else {
			i += 6
		}
	}
	return true
}

func uniqueRegistrationJSON(d *json.Decoder, depth int) error {
	if depth > 32 {
		return ErrRegistrationUnavailable
	}
	token, err := d.Token()
	if err != nil {
		return ErrRegistrationUnavailable
	}
	switch token {
	case json.Delim('{'):
		seen := map[string]bool{}
		for d.More() {
			token, err := d.Token()
			key, ok := token.(string)
			if err != nil || !ok || seen[key] {
				return ErrRegistrationUnavailable
			}
			seen[key] = true
			if err := uniqueRegistrationJSON(d, depth+1); err != nil {
				return err
			}
		}
		token, err = d.Token()
		if err != nil || token != json.Delim('}') {
			return ErrRegistrationUnavailable
		}
	case json.Delim('['):
		for d.More() {
			if err := uniqueRegistrationJSON(d, depth+1); err != nil {
				return err
			}
		}
		token, err = d.Token()
		if err != nil || token != json.Delim(']') {
			return ErrRegistrationUnavailable
		}
	}
	return nil
}
