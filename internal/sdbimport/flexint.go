package sdbimport

import (
	"encoding/json"
	"strconv"
	"strings"
)

// FlexInt decodes a JSON number OR a numeric string, and remembers when it had
// to cope with something else.
//
// WHY THIS EXISTS. stashdb's declared schema types `band_size` as `Int`, and
// sampling 15,000 performers across three pages found 2,507 values, every one
// of them a JSON number. The first full run still died on page 1 with
// "cannot unmarshal number into Go struct field .band_size of type string" --
// which is the source being dirty on a record the sample did not reach, exactly
// the failure mode a sample cannot rule out.
//
// A plain `*int` would crash the run on the first such record; a plain `*any`
// would import garbage. This accepts both shapes, and when it meets neither it
// records the raw value and yields "absent" so the record still imports with
// the field dropped -- counted, never silently discarded.
type FlexInt struct {
	Value   *int
	Invalid string // the raw text when it could not be parsed; empty otherwise
}

// UnmarshalJSON implements json.Unmarshaler.
func (f *FlexInt) UnmarshalJSON(b []byte) error {
	trimmed := strings.TrimSpace(string(b))
	if trimmed == "null" || trimmed == "" {
		f.Value = nil
		return nil
	}

	// A bare number.
	if trimmed[0] != '"' {
		n, err := strconv.Atoi(trimmed)
		if err != nil {
			f.Value = nil
			f.Invalid = trimmed
			return nil
		}
		f.Value = &n
		return nil
	}

	// A quoted value: accept it only if it is actually a number.
	var s string
	if err := json.Unmarshal(b, &s); err != nil {
		f.Value = nil
		f.Invalid = trimmed
		return nil
	}
	s = strings.TrimSpace(s)
	if s == "" {
		f.Value = nil
		return nil
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		f.Value = nil
		f.Invalid = s
		return nil
	}
	f.Value = &n
	return nil
}

// Int returns the parsed value, or nil.
func (f FlexInt) Int() *int { return f.Value }
