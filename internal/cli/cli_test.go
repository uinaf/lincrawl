package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
)

func TestVersionJSON(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"version", "--json"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%s", code, errOut.String())
	}
	var info struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
	}
	if err := json.Unmarshal(out.Bytes(), &info); err != nil {
		t.Fatalf("version JSON: %v\n%s", err, out.String())
	}
	if info.Version == "" {
		t.Fatal("empty version")
	}
}

func TestDescribeIncludesAllCommands(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"describe", "--json"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%s", code, errOut.String())
	}
	var desc struct {
		Commands []struct {
			Name  string `json:"name"`
			Flags []struct {
				Name string `json:"name"`
				Type string `json:"type"`
			} `json:"flags"`
		} `json:"commands"`
		ExitCodes  map[string]int      `json:"exit_codes"`
		FieldMasks map[string][]string `json:"field_masks"`
	}
	if err := json.Unmarshal(out.Bytes(), &desc); err != nil {
		t.Fatalf("describe JSON: %v\n%s", err, out.String())
	}
	want := []string{"doctor", "describe", "status", "sync", "search", "show", "version"}
	got := map[string]bool{}
	for _, c := range desc.Commands {
		got[c.Name] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Fatalf("command %q missing from describe", w)
		}
	}
	if len(desc.ExitCodes) == 0 || desc.ExitCodes["not_found"] != 3 {
		t.Fatalf("exit_codes missing or wrong: %+v", desc.ExitCodes)
	}
	if _, ok := desc.FieldMasks["show"]; !ok {
		t.Fatalf("field_masks for show missing: %+v", desc.FieldMasks)
	}
}

func TestDoctorOfflineJSON(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)

	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"doctor", "--offline", "--json"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit code = %d, stderr=%s", code, errOut.String())
	}
	var res struct {
		OK             bool   `json:"ok"`
		Home           string `json:"home"`
		Offline        bool   `json:"offline"`
		LinearAPIToken string `json:"linear_api_token"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("doctor JSON: %v\n%s", err, out.String())
	}
	if !res.OK || !res.Offline || res.Home != dir {
		t.Fatalf("unexpected doctor result: %+v", res)
	}
}

func TestSyncSearchShowFlow(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)

	fixture, err := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	if err != nil {
		t.Fatal(err)
	}

	syncArgs := []string{"sync", "--fixture", fixture, "--json"}
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), syncArgs, &out, &errOut); code != 0 {
		t.Fatalf("sync exit=%d stderr=%s", code, errOut.String())
	}
	var syncRes struct {
		Counts struct {
			Issues   int `json:"issues"`
			Comments int `json:"comments"`
		} `json:"counts"`
	}
	if err := json.Unmarshal(out.Bytes(), &syncRes); err != nil {
		t.Fatalf("sync JSON: %v\n%s", err, out.String())
	}
	if syncRes.Counts.Issues == 0 {
		t.Fatalf("expected issues > 0, got %+v", syncRes)
	}

	out.Reset()
	errOut.Reset()
	if code := Run(context.Background(), []string{"search", "ingest", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("search exit=%d stderr=%s", code, errOut.String())
	}
	var searchRes struct {
		Results []struct {
			Identifier string `json:"identifier"`
		} `json:"results"`
	}
	if err := json.Unmarshal(out.Bytes(), &searchRes); err != nil {
		t.Fatalf("search JSON: %v\n%s", err, out.String())
	}
	if len(searchRes.Results) == 0 {
		t.Fatalf("expected at least one search result; output=%s", out.String())
	}
	first := searchRes.Results[0].Identifier

	out.Reset()
	errOut.Reset()
	if code := Run(context.Background(), []string{"show", first, "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("show exit=%d stderr=%s", code, errOut.String())
	}
	var showRes struct {
		Identifier string `json:"identifier"`
		TeamKey    string `json:"team_key"`
	}
	if err := json.Unmarshal(out.Bytes(), &showRes); err != nil {
		t.Fatalf("show JSON: %v\n%s", err, out.String())
	}
	if showRes.Identifier != first {
		t.Fatalf("show identifier = %q, want %q", showRes.Identifier, first)
	}
}

func TestSyncRequiresFixture(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"sync"}, &out, &errOut); code == 0 {
		t.Fatal("expected non-zero exit when --fixture is omitted")
	}
}
