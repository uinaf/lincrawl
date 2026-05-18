package cli

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestProjectFieldsKeepWhitelist(t *testing.T) {
	in := struct {
		A int    `json:"a"`
		B string `json:"b"`
		C bool   `json:"c"`
	}{1, "two", true}
	got, err := projectFields(in, "a,c")
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	if err := json.Unmarshal(got, &m); err != nil {
		t.Fatal(err)
	}
	if _, ok := m["b"]; ok {
		t.Fatalf("b should have been dropped: %v", m)
	}
	if len(m) != 2 {
		t.Fatalf("want 2 keys, got %v", m)
	}
}

func TestProjectFieldsEmptyKeepReturnsAll(t *testing.T) {
	in := struct {
		A int `json:"a"`
	}{1}
	got, err := projectFields(in, "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(got), `"a"`) {
		t.Fatalf("expected full encoding, got %s", string(got))
	}
}

func TestProjectFieldsRejectsUnknown(t *testing.T) {
	in := map[string]any{"a": 1, "b": 2}
	_, err := projectFields(in, "a,bogus")
	if err == nil {
		t.Fatal("expected unknown-field error")
	}
	if !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("error should mention bogus: %v", err)
	}
}

func TestProjectListItemsKnownKeysAggregated(t *testing.T) {
	rows := []json.RawMessage{
		json.RawMessage(`{"a":1,"b":2}`),
		json.RawMessage(`{"a":3,"b":4,"c":5}`),
	}
	out, err := projectListItems(rows, "a,c")
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("expected 2 rows, got %d", len(out))
	}
	var first map[string]any
	_ = json.Unmarshal(out[0], &first)
	if _, ok := first["b"]; ok {
		t.Fatal("row 0 should have dropped b")
	}
	var second map[string]any
	_ = json.Unmarshal(out[1], &second)
	if _, ok := second["c"]; !ok {
		t.Fatalf("row 1 should retain c: %v", second)
	}
}
