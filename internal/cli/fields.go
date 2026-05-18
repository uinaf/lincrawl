package cli

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// projectFields encodes v to JSON then drops every top-level key not in keep.
// keep is comma-separated. Empty keep returns the original encoding.
// Returns the projected JSON bytes (indented like writeJSON).
func projectFields(v any, keep string) ([]byte, error) {
	raw, err := json.Marshal(v)
	if err != nil {
		return nil, err
	}
	keep = strings.TrimSpace(keep)
	if keep == "" {
		return json.MarshalIndent(v, "", "  ")
	}
	wanted := map[string]bool{}
	for _, f := range strings.Split(keep, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			wanted[f] = true
		}
	}
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		return nil, fmt.Errorf("fields: object required for --fields, got non-object")
	}
	unknown := []string{}
	for k := range wanted {
		if _, ok := m[k]; !ok {
			unknown = append(unknown, k)
		}
	}
	if len(unknown) > 0 {
		sort.Strings(unknown)
		known := make([]string, 0, len(m))
		for k := range m {
			known = append(known, k)
		}
		sort.Strings(known)
		return nil, validationErr(fmt.Sprintf("unknown fields: %s (known: %s)",
			strings.Join(unknown, ","), strings.Join(known, ",")))
	}
	out := map[string]json.RawMessage{}
	for k, v := range m {
		if wanted[k] {
			out[k] = v
		}
	}
	return json.MarshalIndent(out, "", "  ")
}

// projectListItems applies field projection to each element of a slice value.
// Used for search results so --fields trims each row, not the envelope.
// Known keys are the UNION across all rows so a sparse row does not flag
// a legitimate key as unknown.
func projectListItems(items []json.RawMessage, keep string) ([]json.RawMessage, error) {
	keep = strings.TrimSpace(keep)
	if keep == "" {
		return items, nil
	}
	wanted := map[string]bool{}
	for _, f := range strings.Split(keep, ",") {
		f = strings.TrimSpace(f)
		if f != "" {
			wanted[f] = true
		}
	}
	parsed := make([]map[string]json.RawMessage, len(items))
	union := map[string]struct{}{}
	for i, raw := range items {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			return nil, fmt.Errorf("fields: row %d is not an object", i)
		}
		parsed[i] = m
		for k := range m {
			union[k] = struct{}{}
		}
	}
	if len(items) > 0 {
		var unknown []string
		for k := range wanted {
			if _, ok := union[k]; !ok {
				unknown = append(unknown, k)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			known := make([]string, 0, len(union))
			for k := range union {
				known = append(known, k)
			}
			sort.Strings(known)
			return nil, validationErr(fmt.Sprintf("unknown fields: %s (known: %s)",
				strings.Join(unknown, ","), strings.Join(known, ",")))
		}
	}
	out := make([]json.RawMessage, 0, len(parsed))
	for _, m := range parsed {
		proj := map[string]json.RawMessage{}
		for k, v := range m {
			if wanted[k] {
				proj[k] = v
			}
		}
		b, err := json.Marshal(proj)
		if err != nil {
			return nil, err
		}
		out = append(out, b)
	}
	return out, nil
}
