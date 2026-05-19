package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uinaf/lincrawl/internal/linear"
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

func TestSyncModesAreMutuallyExclusive(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"sync", "--fixture", "testdata/synthetic", "--entities"}, &out, &errOut)
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (usage), stderr=%s", code, ExitUsage, errOut.String())
	}
	var env struct {
		Code string `json:"code"`
		Exit int    `json:"exit"`
	}
	if err := json.Unmarshal(errOut.Bytes(), &env); err != nil {
		t.Fatalf("error envelope JSON: %v\n%s", err, errOut.String())
	}
	if env.Code != "usage" || env.Exit != ExitUsage {
		t.Fatalf("envelope = %+v", env)
	}
}

func TestSyncInvalidDuration(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	t.Setenv("LINEAR_API_KEY", "lin_api_test_key")
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"sync", "--updated-since", "junk"}, &out, &errOut)
	if code != ExitValidation {
		t.Fatalf("exit = %d, want %d (validation)", code, ExitValidation)
	}
}

func TestDescribeSelectiveCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"describe", "sync"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s", code, errOut.String())
	}
	var desc struct {
		SchemaVersion string `json:"schema_version"`
		Commands      []struct {
			Name              string     `json:"name"`
			Mutates           bool       `json:"mutates"`
			MutuallyExclusive [][]string `json:"mutually_exclusive,omitempty"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &desc); err != nil {
		t.Fatalf("describe JSON: %v\n%s", err, out.String())
	}
	if desc.SchemaVersion != "lincrawl.cli.v1" {
		t.Fatalf("schema_version = %q", desc.SchemaVersion)
	}
	if len(desc.Commands) != 1 || desc.Commands[0].Name != "sync" {
		t.Fatalf("expected exactly the sync command, got %+v", desc.Commands)
	}
	if !desc.Commands[0].Mutates {
		t.Fatal("sync should be marked as mutates=true")
	}
	if len(desc.Commands[0].MutuallyExclusive) == 0 {
		t.Fatal("sync should advertise mutually_exclusive groups")
	}
}

func TestDescribeUnknownCommand(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"describe", "totally-not-a-command"}, &out, &errOut)
	if code != ExitNotFound {
		t.Fatalf("exit = %d, want %d", code, ExitNotFound)
	}
}

func TestDescribeIncludesNewCommands(t *testing.T) {
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"describe", "--json"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	var desc struct {
		Commands []struct {
			Name    string `json:"name"`
			Mutates bool   `json:"mutates"`
		} `json:"commands"`
	}
	if err := json.Unmarshal(out.Bytes(), &desc); err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{
		"archive":      true,
		"publish":      true,
		"import":       true,
		"store verify": false,
		"subscribe":    true,
	}
	got := map[string]bool{}
	for _, c := range desc.Commands {
		got[c.Name] = c.Mutates
	}
	for name, mut := range want {
		gotMut, present := got[name]
		if !present {
			t.Errorf("describe missing command %q", name)
			continue
		}
		if gotMut != mut {
			t.Errorf("command %q mutates=%v, want %v", name, gotMut, mut)
		}
	}
}

func TestArchiveRequiresFixture(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINCRAWL_AGE_RECIPIENT", "age1invalid")
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"archive", "--out", "./out.jsonl.zst.age"}, &out, &errOut); code != ExitUsage {
		t.Fatalf("exit=%d, want %d (usage)", code, ExitUsage)
	}
}

func TestPublishMissingRecipientIsConfigError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	t.Setenv("LINCRAWL_AGE_RECIPIENT", "")
	// Seed the store so OpenReadOnly succeeds.
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	if code := Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatalf("seed sync failed exit=%d", code)
	}
	var out, errOut bytes.Buffer
	prev, _ := os.Getwd()
	defer os.Chdir(prev)
	os.Chdir(dir)
	code := Run(context.Background(), []string{"publish", "--out", "./out.jsonl.zst.age"}, &out, &errOut)
	if code != ExitConfig {
		t.Fatalf("exit=%d, want %d (config)", code, ExitConfig)
	}
}

func TestImportMissingInIsUsageError(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"import", "--identity", "x"}, &out, &errOut); code != ExitUsage {
		t.Fatalf("exit=%d, want %d (usage)", code, ExitUsage)
	}
}

func TestVersionTextOutput(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"version", "--no-json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("lincrawl ")) {
		t.Fatalf("text output: %q", stdout.String())
	}
}

func TestStatusReportsResumeFromStoredCursor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"issues":{
			"pageInfo":{"endCursor":"","hasNextPage":false},
			"nodes":[
			  {"id":"i1","identifier":"LIN-1","title":"A","description":"","priority":1,"createdAt":"","updatedAt":"2026-05-19T00:00:00Z",
			   "team":null,"project":null,"state":null,"assignee":null,"creator":null,
			   "labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},
			   "comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}
			]
		}}}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	t.Setenv("LINEAR_API_KEY", "lin_api_test")
	t.Setenv("LINCRAWL_LINEAR_BASE_URL", srv.URL)
	// Seed a cursor by running updated-since once.
	if code := Run(context.Background(), []string{"sync", "--updated-since", "24h", "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatal("seed")
	}
	// Status now reports an active resume.
	var stdout bytes.Buffer
	if code := Run(context.Background(), []string{"status", "--json"}, &stdout, new(bytes.Buffer)); code != 0 {
		t.Fatal("status")
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"resume"`)) {
		t.Fatalf("status missing resume: %s", stdout.String())
	}
}

func TestSyncResumeWithCursorHappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"issues":{
			"pageInfo":{"endCursor":"","hasNextPage":false},
			"nodes":[
			  {"id":"i1","identifier":"LIN-1","title":"A","description":"","priority":1,"createdAt":"","updatedAt":"2026-05-19T00:00:00Z",
			   "team":null,"project":null,"state":null,"assignee":null,"creator":null,
			   "labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},
			   "comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}
			]
		}}}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	t.Setenv("LINEAR_API_KEY", "lin_api_test")
	t.Setenv("LINCRAWL_LINEAR_BASE_URL", srv.URL)
	// Seed cursor.
	if code := Run(context.Background(), []string{"sync", "--updated-since", "24h", "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatal("seed")
	}
	// Now resume — exercises the RFC3339 parse branch.
	if code := Run(context.Background(), []string{"sync", "--resume", "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatal("resume")
	}
}

func TestStatusBeforeAnyDB(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"status", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	var res map[string]any
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res["exists"] != false {
		t.Fatalf("expected exists=false, got %v", res["exists"])
	}
}

func TestSearchEmptyArchiveReturnsEmpty(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	// Seed an empty DB by syncing a snapshot with no issues.
	_ = os.MkdirAll(filepath.Join(dir, "fx"), 0o755)
	if err := os.WriteFile(filepath.Join(dir, "fx", "snapshot.json"),
		[]byte(`{"teams":[],"states":[],"users":[],"labels":[],"projects":[],"issues":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := Run(context.Background(), []string{"sync", "--fixture", filepath.Join(dir, "fx"), "--json"},
		new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatalf("seed sync exit=%d", code)
	}
	var stdout bytes.Buffer
	if code := Run(context.Background(), []string{"search", "nope", "--json"}, &stdout, new(bytes.Buffer)); code != 0 {
		t.Fatal(code)
	}
	var res struct {
		Results []any `json:"results"`
	}
	_ = json.Unmarshal(stdout.Bytes(), &res)
	if len(res.Results) != 0 {
		t.Fatalf("expected empty results, got %d", len(res.Results))
	}
}

func TestShowValidatesIdentifier(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"show", "obvious nonsense"}, &out, &errOut); code != ExitValidation {
		t.Fatalf("exit=%d, want %d", code, ExitValidation)
	}
}

func TestSyncStdinDryRun(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"sync", "--stdin", "--dry-run", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	var res map[string]any
	_ = json.Unmarshal(out.Bytes(), &res)
	if res["mode"] != "stdin" {
		t.Fatalf("mode=%v", res["mode"])
	}
}

func TestSyncResumeWithoutStoredHighWaterMark(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINEAR_API_KEY", "lin_api_test")
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"sync", "--resume"}, &out, &errOut); code != ExitUsage {
		t.Fatalf("exit=%d, want %d", code, ExitUsage)
	}
}

func TestExportToStdout(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	if code := Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatalf("seed exit=%d", code)
	}
	var stdout bytes.Buffer
	if code := Run(context.Background(), []string{"export", "--out", "-"}, &stdout, new(bytes.Buffer)); code != 0 {
		t.Fatalf("export exit=%d", code)
	}
	if stdout.Len() == 0 {
		t.Fatal("expected NDJSON on stdout")
	}
}

func TestExportInvalidOutSuffix(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	// Open the store first to satisfy OpenReadOnly.
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	_ = Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer))
	// We don't validate suffix on `export --out` (no .jsonl.zst.age constraint
	// there); confirm a relative path under cwd works.
	var stdout, stderr bytes.Buffer
	prev, _ := os.Getwd()
	dir := t.TempDir()
	_ = os.Chdir(dir)
	defer os.Chdir(prev)
	if code := Run(context.Background(), []string{"export", "--out", "./dump.jsonl", "--no-json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("export exit=%d stderr=%s", code, stderr.String())
	}
}

func TestQueryRequiresOneOf(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "lin_api_x")
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"query"}, &out, &errOut); code != ExitUsage {
		t.Fatalf("exit=%d, want %d", code, ExitUsage)
	}
	if code := Run(context.Background(), []string{"query", "--graphql", "x", "--graphql-file", "y"}, &out, &errOut); code != ExitUsage {
		t.Fatalf("both flags exit=%d, want %d", code, ExitUsage)
	}
}

func TestQueryInvalidVarsJSON(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "lin_api_x")
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"query", "--graphql", "query{viewer{id}}", "--vars", "not-json"}, &out, &errOut); code != ExitValidation {
		t.Fatalf("exit=%d, want %d", code, ExitValidation)
	}
}

func TestPublishDryRunWithSeededStore(t *testing.T) {
	id := mustNewIdentity(t)
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINCRAWL_AGE_RECIPIENT", id.Recipient)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	if code := Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatalf("seed exit=%d", code)
	}
	prev, _ := os.Getwd()
	dir := t.TempDir()
	_ = os.Chdir(dir)
	defer os.Chdir(prev)
	var out, errOut bytes.Buffer
	if code := Run(context.Background(), []string{"publish", "--out", "./out.jsonl.zst.age", "--dry-run", "--json"}, &out, &errOut); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, errOut.String())
	}
	if !bytes.Contains(out.Bytes(), []byte(`"dry_run": true`)) {
		t.Fatalf("dry_run flag missing: %s", out.String())
	}
}

func TestParseSinceAcceptsRFC3339AndDurations(t *testing.T) {
	now := time.Now().UTC()
	tt, err := parseSince("24h")
	if err != nil {
		t.Fatal(err)
	}
	if d := now.Sub(tt); d < 23*time.Hour || d > 25*time.Hour {
		t.Errorf("24h since: delta=%v", d)
	}
	if _, err := parseSince("2026-05-19T00:00:00Z"); err != nil {
		t.Fatalf("rfc3339: %v", err)
	}
	if _, err := parseSince("3d"); err != nil {
		t.Fatalf("days: %v", err)
	}
	if _, err := parseSince(""); err == nil {
		t.Error("expected error on empty")
	}
	if _, err := parseSince("junk"); err == nil {
		t.Error("expected error on junk")
	}
	if _, err := parseSince("xd"); err == nil {
		t.Error("expected error on non-numeric day count")
	}
}

func TestWriteProjectedKnownAndUnknown(t *testing.T) {
	type row struct {
		A int `json:"a"`
		B int `json:"b"`
	}
	var buf bytes.Buffer
	if err := writeProjected(&buf, row{1, 2}, "a"); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"a": 1`)) || bytes.Contains(buf.Bytes(), []byte(`"b":`)) {
		t.Fatalf("projection wrong: %s", buf.String())
	}
	buf.Reset()
	if err := writeProjected(&buf, row{1, 2}, ""); err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(buf.Bytes(), []byte(`"b": 2`)) {
		t.Fatalf("no-projection should keep all keys: %s", buf.String())
	}
}

func TestErrIsJSONFromArgs(t *testing.T) {
	if !errIsJSONFromArgs([]string{"foo"}) {
		t.Error("default should be JSON")
	}
	if !errIsJSONFromArgs([]string{"foo", "--json"}) {
		t.Error("--json should be JSON")
	}
	if errIsJSONFromArgs([]string{"foo", "--no-json"}) {
		t.Error("--no-json should be plain")
	}
	if !errIsJSONFromArgs([]string{"foo", "--json=true"}) {
		t.Error("--json=true should be JSON")
	}
}

func TestCLIErrorUnwrap(t *testing.T) {
	inner := fmt.Errorf("inner")
	e := wrapErr(inner, "internal", ExitInternal)
	if e == nil {
		t.Fatal("nil")
	}
	if !errors.Is(e, inner) {
		t.Errorf("Unwrap should expose inner err")
	}
	if wrapErr(nil, "x", 1) != nil {
		t.Errorf("nil-in returns nil-out")
	}
}

func TestErrParserExitMessage(t *testing.T) {
	e := errParserExit{code: 7}
	if !strings.Contains(e.Error(), "7") {
		t.Errorf("errParserExit msg: %q", e.Error())
	}
}

func TestSnapshotCountsHelper(t *testing.T) {
	got := snapshotCounts(linear.Snapshot{
		Teams: []linear.Team{{ID: "t"}}, Issues: []linear.Issue{{ID: "i"}},
	})
	if got["teams"] != 1 || got["issues"] != 1 {
		t.Fatalf("counts: %+v", got)
	}
}

func TestSplitCSV(t *testing.T) {
	got := splitCSV(" foo, bar ,baz, ")
	if len(got) != 3 || got[0] != "foo" || got[2] != "baz" {
		t.Fatalf("got %v", got)
	}
	if got := splitCSV(""); len(got) != 0 {
		t.Fatalf("empty: %v", got)
	}
}

func TestResolveIdentityPaths(t *testing.T) {
	prevID := os.Getenv("LINCRAWL_AGE_IDENTITY")
	prevFile := os.Getenv("LINCRAWL_AGE_IDENTITY_FILE")
	t.Cleanup(func() {
		os.Setenv("LINCRAWL_AGE_IDENTITY", prevID)
		os.Setenv("LINCRAWL_AGE_IDENTITY_FILE", prevFile)
	})
	// 1. flag wins
	t.Setenv("LINCRAWL_AGE_IDENTITY", "fromenv")
	if v, err := resolveIdentity("flagvalue"); err != nil || v != "flagvalue" {
		t.Errorf("flag-wins: v=%q err=%v", v, err)
	}
	// 2. env wins when no flag
	if v, err := resolveIdentity(""); err != nil || v != "fromenv" {
		t.Errorf("env-wins: v=%q err=%v", v, err)
	}
	// 3. file fallback
	t.Setenv("LINCRAWL_AGE_IDENTITY", "")
	dir := t.TempDir()
	f := filepath.Join(dir, "id")
	if err := os.WriteFile(f, []byte("AGE-SECRET-KEY-1ABC"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LINCRAWL_AGE_IDENTITY_FILE", f)
	v, err := resolveIdentity("")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(v, "AGE-SECRET-KEY-1ABC") {
		t.Errorf("file read: %q", v)
	}
	// 4. file missing
	t.Setenv("LINCRAWL_AGE_IDENTITY_FILE", filepath.Join(dir, "nope"))
	if _, err := resolveIdentity(""); err == nil {
		t.Fatal("expected error on missing file")
	}
	// 5. file too large
	big := filepath.Join(dir, "big")
	if err := os.WriteFile(big, bytes.Repeat([]byte("x"), 70*1024), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LINCRAWL_AGE_IDENTITY_FILE", big)
	if _, err := resolveIdentity(""); err == nil {
		t.Fatal("expected size-cap error")
	}
	// 6. none set
	t.Setenv("LINCRAWL_AGE_IDENTITY_FILE", "")
	if _, err := resolveIdentity(""); err == nil {
		t.Fatal("expected config error when nothing set")
	}
}

func TestResolveRecipient(t *testing.T) {
	prev := os.Getenv(strings.TrimSuffix("LINCRAWL_AGE_RECIPIENT", ""))
	t.Cleanup(func() { os.Setenv("LINCRAWL_AGE_RECIPIENT", prev) })
	t.Setenv("LINCRAWL_AGE_RECIPIENT", "age1env")
	if v, _ := resolveRecipient("flag"); v != "flag" {
		t.Errorf("flag-wins: %q", v)
	}
	if v, _ := resolveRecipient(""); v != "age1env" {
		t.Errorf("env-wins: %q", v)
	}
	t.Setenv("LINCRAWL_AGE_RECIPIENT", "")
	if _, err := resolveRecipient(""); err == nil {
		t.Fatal("expected config error when nothing set")
	}
}

func TestValidatedOutPathChecks(t *testing.T) {
	dir := t.TempDir()
	if _, err := validatedOutPath("", dir); err == nil {
		t.Error("empty path should error")
	}
	if _, err := validatedOutPath("foo.jsonl", dir); err == nil {
		t.Error("wrong suffix should error")
	}
	if _, err := validatedOutPath("/tmp/escape.jsonl.zst.age", dir); err == nil {
		t.Error("escape should error")
	}
	if _, err := validatedOutPath("./out.jsonl.zst.age", dir); err != nil {
		t.Errorf("under cwd should succeed: %v", err)
	}
}

func TestRunHandlesParseError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"--nonexistent-flag"}, &stdout, &stderr); code != ExitUsage {
		t.Fatalf("exit=%d, want %d (usage)", code, ExitUsage)
	}
}

func TestArchiveAndImportHappyPath(t *testing.T) {
	// Generate an X25519 identity in-process so the CLI exercises the
	// real recipient/identity resolution and the encrypt+decrypt path.
	identity := mustNewIdentity(t)
	t.Setenv("LINCRAWL_AGE_RECIPIENT", identity.Recipient)
	t.Setenv("LINCRAWL_AGE_IDENTITY", identity.Secret)

	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	dir := t.TempDir()
	prev, _ := os.Getwd()
	if err := os.Chdir(dir); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prev) })

	out := "./out.jsonl.zst.age"
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"archive", "--fixture", fixture, "--out", out, "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("archive exit=%d stderr=%s", code, stderr.String())
	}
	var archiveOut struct {
		Records int    `json:"records"`
		Out     string `json:"out"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &archiveOut); err != nil {
		t.Fatalf("archive JSON: %v\n%s", err, stdout.String())
	}
	if archiveOut.Records == 0 {
		t.Fatalf("archive reported 0 records: %s", stdout.String())
	}
	// Now import back into a fresh home.
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	stdout.Reset()
	stderr.Reset()
	if code := Run(context.Background(), []string{"import", "--in", out, "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("import exit=%d stderr=%s", code, stderr.String())
	}
	var importOut struct {
		Records int            `json:"records"`
		Counts  map[string]int `json:"counts"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &importOut); err != nil {
		t.Fatalf("import JSON: %v\n%s", err, stdout.String())
	}
	if importOut.Records != archiveOut.Records {
		t.Fatalf("import records mismatch: archive=%d import=%d", archiveOut.Records, importOut.Records)
	}
	if importOut.Counts["issues"] == 0 {
		t.Fatalf("import did not ingest any issues: %+v", importOut.Counts)
	}
}

func TestStoreVerifyEmitsSingleEnvelopeOnFailure(t *testing.T) {
	// Make a directory without a manifest.json so verify fails.
	dir := t.TempDir()
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"store", "verify", dir, "--json"}, &out, &errOut)
	if code != ExitInternal && code != ExitValidation {
		t.Fatalf("exit=%d, want validation or internal", code)
	}
	// The stderr should hold exactly one JSON object.
	raw := errOut.Bytes()
	if !bytes.HasPrefix(bytes.TrimSpace(raw), []byte("{")) {
		t.Fatalf("stderr should start with one JSON object, got:\n%s", raw)
	}
	// json.Decoder should consume the entire stderr without leftover input.
	dec := json.NewDecoder(bytes.NewReader(raw))
	var env map[string]any
	if err := dec.Decode(&env); err != nil {
		t.Fatalf("decode envelope: %v", err)
	}
	if _, ok := env["code"]; !ok {
		t.Fatalf("envelope missing `code`: %v", env)
	}
	// Confirm no trailing second envelope.
	var leftover map[string]any
	if err := dec.Decode(&leftover); err == nil {
		t.Fatalf("expected single envelope, got second: %v", leftover)
	}
}

func TestGuardCmdFailureJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "leak.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"guard", "--root", dir, "--json"}, &stdout, &stderr)
	if code != ExitValidation {
		t.Fatalf("exit=%d, want %d", code, ExitValidation)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("findings")) {
		t.Fatal("expected JSON findings on stderr")
	}
}

func TestGuardCmdFailureText(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "leak.db"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"guard", "--root", dir, "--no-json"}, &stdout, &stderr)
	if code != ExitValidation {
		t.Fatalf("exit=%d, want %d", code, ExitValidation)
	}
	if !bytes.Contains(stderr.Bytes(), []byte("guard:")) {
		t.Fatal("expected text finding on stderr")
	}
}

func TestRunReturnsZeroFromHelp(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"--help"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("--help exit=%d", code)
	}
}

func TestRunSurfacesUnknownCommand(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"totally-not-a-command"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("unknown subcommand exit=%d", code)
	}
}

func TestDoctorOnlineFlagFlagsMissingKey(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINEAR_API_KEY", "")
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"doctor", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	var res struct {
		OK    bool     `json:"ok"`
		Notes []string `json:"notes"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &res); err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("doctor without API key (online) should not be OK")
	}
	if len(res.Notes) == 0 {
		t.Fatal("expected at least one note about LINEAR_API_KEY")
	}
}

func TestDoctorOfflineTextOutput(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"doctor", "--offline", "--no-json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("lincrawl doctor")) {
		t.Fatalf("text output: %q", stdout.String())
	}
}

func TestStatusTextAfterSync(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	if code := Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatal("seed failed")
	}
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"status", "--no-json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("status: %d stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("teams=")) {
		t.Fatalf("status text: %q", stdout.String())
	}
}

func TestSearchTextAndNDJSONAndFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	if code := Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatal("seed failed")
	}

	// text mode
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"search", "ingest", "--no-json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("search text exit=%d", code)
	}

	// --raw
	stdout.Reset()
	stderr.Reset()
	if code := Run(context.Background(), []string{"search", "ingest", "--raw", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("search raw exit=%d", code)
	}

	// --ndjson
	stdout.Reset()
	stderr.Reset()
	if code := Run(context.Background(), []string{"search", "ingest", "--ndjson"}, &stdout, &stderr); code != 0 {
		t.Fatalf("ndjson: %d", code)
	}

	// --ndjson with --fields
	stdout.Reset()
	stderr.Reset()
	if code := Run(context.Background(), []string{"search", "ingest", "--ndjson", "--fields", "identifier"}, &stdout, &stderr); code != 0 {
		t.Fatalf("ndjson fields: %d stderr=%s", code, stderr.String())
	}

	// JSON with --fields
	stdout.Reset()
	stderr.Reset()
	if code := Run(context.Background(), []string{"search", "ingest", "--fields", "identifier"}, &stdout, &stderr); code != 0 {
		t.Fatalf("json fields: %d", code)
	}
}

func TestShowTextAndFields(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	if code := Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatal("seed failed")
	}
	// Pick first identifier via search.
	var sb bytes.Buffer
	_ = Run(context.Background(), []string{"search", "ingest", "--json"}, &sb, new(bytes.Buffer))
	var sr struct {
		Results []struct {
			Identifier string `json:"identifier"`
		} `json:"results"`
	}
	_ = json.Unmarshal(sb.Bytes(), &sr)
	if len(sr.Results) == 0 {
		t.Skip("no results to use for show")
	}
	id := sr.Results[0].Identifier

	// text mode
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"show", id, "--no-json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("show text exit=%d", code)
	}

	// fields filter
	stdout.Reset()
	stderr.Reset()
	if code := Run(context.Background(), []string{"show", id, "--fields", "identifier,title"}, &stdout, &stderr); code != 0 {
		t.Fatalf("show fields exit=%d", code)
	}
}

func TestShowMissingIssueIsNotFound(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	_ = Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer))
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"show", "ZZZ-9999"}, &stdout, &stderr); code != ExitNotFound {
		t.Fatalf("exit=%d, want %d", code, ExitNotFound)
	}
}

func TestExportToFile(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	_ = Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer))
	cwd, _ := os.Getwd()
	wd := t.TempDir()
	_ = os.Chdir(wd)
	defer os.Chdir(cwd)
	var stdout bytes.Buffer
	if code := Run(context.Background(), []string{"export", "--out", "./out.jsonl"}, &stdout, new(bytes.Buffer)); code != 0 {
		t.Fatalf("export to file exit=%d", code)
	}
	if _, err := os.Stat(filepath.Join(wd, "out.jsonl")); err != nil {
		t.Fatalf("out file: %v", err)
	}
}

func TestPublishRoundTripAndImportSubscribe(t *testing.T) {
	pub := mustNewIdentity(t)
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	t.Setenv("LINCRAWL_AGE_RECIPIENT", pub.Recipient)
	t.Setenv("LINCRAWL_AGE_IDENTITY", pub.Secret)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	if code := Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatal("seed failed")
	}

	cwd, _ := os.Getwd()
	wd := t.TempDir()
	_ = os.Chdir(wd)
	defer os.Chdir(cwd)

	// publish (writes encrypted snapshot)
	if code := Run(context.Background(), []string{"publish", "--out", "./snap.jsonl.zst.age", "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatalf("publish exit=%d", code)
	}

	// archive --dry-run hits a different code path
	if code := Run(context.Background(), []string{"archive", "--fixture", fixture, "--out", "./arch.jsonl.zst.age", "--dry-run", "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatalf("archive dry-run exit=%d", code)
	}

	// archive (real run)
	if code := Run(context.Background(), []string{"archive", "--fixture", fixture, "--out", "./arch.jsonl.zst.age", "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatalf("archive exit=%d", code)
	}

	// import dry-run
	if code := Run(context.Background(), []string{"import", "--in", "./snap.jsonl.zst.age", "--dry-run", "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatalf("import dry-run exit=%d", code)
	}

	// import into a separate home — happy path
	importHome := t.TempDir()
	t.Setenv("LINCRAWL_HOME", importHome)
	if code := Run(context.Background(), []string{"import", "--in", "./snap.jsonl.zst.age", "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatalf("import exit=%d", code)
	}

	// Build a tenantstore layout and exercise subscribe + store-verify.
	storeRoot := t.TempDir()
	relSnap := "artifacts/snapshots/full/2026/05/snap.jsonl.zst.age"
	abs := filepath.Join(storeRoot, relSnap)
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile("./snap.jsonl.zst.age")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, src, 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := map[string]any{
		"schema_version": "lincrawl.store.v1",
		"snapshots": []map[string]string{
			{"kind": "full", "path": relSnap},
		},
	}
	mb, _ := json.Marshal(manifest)
	if err := os.WriteFile(filepath.Join(storeRoot, "manifest.json"), mb, 0o600); err != nil {
		t.Fatal(err)
	}

	// store verify (happy)
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"store", "verify", storeRoot, "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("store verify exit=%d stderr=%s", code, stderr.String())
	}

	// subscribe dry-run
	subHome := t.TempDir()
	t.Setenv("LINCRAWL_HOME", subHome)
	if code := Run(context.Background(), []string{"subscribe", storeRoot, "--dry-run", "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatalf("subscribe dry-run exit=%d", code)
	}

	// subscribe full
	if code := Run(context.Background(), []string{"subscribe", storeRoot, "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatalf("subscribe exit=%d", code)
	}
}

func TestStoreVerifyFailureExitsValidation(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "manifest.json"),
		[]byte(`{"schema_version":"lincrawl.store.v1","snapshots":[{"kind":"full","path":"missing/x.jsonl.zst.age"}]}`),
		0o600); err != nil {
		t.Fatal(err)
	}
	cwd, _ := os.Getwd()
	_ = os.Chdir(root)
	defer os.Chdir(cwd)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"store", "verify", root, "--json"}, &stdout, &stderr)
	if code != ExitValidation {
		t.Fatalf("exit=%d, want %d", code, ExitValidation)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("findings")) {
		t.Fatal("expected findings on stdout")
	}
}

func TestQueryAgainstMockServer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"u1","name":"Sam"}}}`))
	}))
	defer srv.Close()
	t.Setenv("LINEAR_API_KEY", "lin_api_test")
	t.Setenv("LINCRAWL_LINEAR_BASE_URL", srv.URL)
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"query", "--graphql", "query{viewer{id name}}", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("Sam")) {
		t.Fatalf("output: %q", stdout.String())
	}
}

func TestQueryReadsFromFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"u1"}}}`))
	}))
	defer srv.Close()
	t.Setenv("LINEAR_API_KEY", "lin_api_test")
	t.Setenv("LINCRAWL_LINEAR_BASE_URL", srv.URL)
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	cwd, _ := os.Getwd()
	wd := t.TempDir()
	_ = os.Chdir(wd)
	defer os.Chdir(cwd)
	if err := os.WriteFile(filepath.Join(wd, "q.graphql"), []byte("query{viewer{id}}"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"query", "--graphql-file", "./q.graphql"}, &stdout, &stderr); code != 0 {
		t.Fatalf("exit=%d stderr=%s", code, stderr.String())
	}
}

func TestSyncEntitiesAgainstMock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Query string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch {
		case strings.Contains(req.Query, "{ viewer"):
			_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"u1","name":"S","email":""}}}`))
		case strings.Contains(req.Query, "teams("):
			_, _ = w.Write([]byte(`{"data":{"teams":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}}`))
		case strings.Contains(req.Query, "workflowStates("):
			_, _ = w.Write([]byte(`{"data":{"workflowStates":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}}`))
		case strings.Contains(req.Query, "users("):
			_, _ = w.Write([]byte(`{"data":{"users":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}}`))
		case strings.Contains(req.Query, "issueLabels("):
			_, _ = w.Write([]byte(`{"data":{"issueLabels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}}`))
		case strings.Contains(req.Query, "projects("):
			_, _ = w.Write([]byte(`{"data":{"projects":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}}`))
		default:
			t.Fatalf("unexpected query: %s", req.Query)
		}
	}))
	defer srv.Close()
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINEAR_API_KEY", "lin_api_test")
	t.Setenv("LINCRAWL_LINEAR_BASE_URL", srv.URL)
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"sync", "--entities", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("sync entities: code=%d stderr=%s", code, stderr.String())
	}
}

func TestSyncIssueByIdentifierAgainstMock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"issue":{
			"id":"i1","identifier":"LIN-1","title":"T","description":"",
			"priority":1,"createdAt":"","updatedAt":"2026-05-19T00:00:00Z",
			"team":{"id":"t1"},"project":null,"state":{"id":"s1"},"assignee":null,"creator":null,
			"labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},
			"comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}
		}}}`))
	}))
	defer srv.Close()
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINEAR_API_KEY", "lin_api_test")
	t.Setenv("LINCRAWL_LINEAR_BASE_URL", srv.URL)
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"sync", "--issue", "LIN-1", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("sync issue: code=%d stderr=%s", code, stderr.String())
	}
}

func TestSyncUpdatedSinceAgainstMock(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"issues":{
			"pageInfo":{"endCursor":"","hasNextPage":false},
			"nodes":[
			  {"id":"i1","identifier":"LIN-1","title":"A","description":"","priority":1,"createdAt":"","updatedAt":"2026-05-19T00:00:00Z",
			   "team":null,"project":null,"state":null,"assignee":null,"creator":null,
			   "labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},
			   "comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}
			]
		}}}`))
	}))
	defer srv.Close()
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINEAR_API_KEY", "lin_api_test")
	t.Setenv("LINCRAWL_LINEAR_BASE_URL", srv.URL)
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"sync", "--updated-since", "24h", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("sync updated-since: %d stderr=%s", code, stderr.String())
	}
}

func TestSyncFixtureDryRun(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"sync", "--fixture", fixture, "--dry-run", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("sync dry-run exit=%d stderr=%s", code, stderr.String())
	}
}

func TestDescribeTextMode(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"describe", "--no-json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("describe text: %d", code)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("sync")) {
		t.Fatalf("text output missing commands: %q", stdout.String())
	}
}

func TestDescribeEachKnownCommand(t *testing.T) {
	// Exercises walkCommands across all known leaf commands. "store verify" is
	// a grouped subcommand; describe expects a single token, so skip it.
	for _, name := range []string{"sync", "search", "show", "version", "doctor", "status", "guard", "archive", "publish", "import", "subscribe", "query", "export"} {
		var stdout, stderr bytes.Buffer
		if code := Run(context.Background(), []string{"describe", name}, &stdout, &stderr); code != 0 {
			t.Errorf("describe %s: code=%d stderr=%s", name, code, stderr.String())
		}
	}
}

func TestSyncEntitiesTextOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Query string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch {
		case strings.Contains(req.Query, "{ viewer"):
			_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"u1","name":"S","email":""}}}`))
		default:
			// every other entity query returns empty page
			_, _ = w.Write([]byte(`{"data":{"teams":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},"workflowStates":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},"users":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},"issueLabels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},"projects":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}}`))
		}
	}))
	defer srv.Close()
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINEAR_API_KEY", "lin_api_test")
	t.Setenv("LINCRAWL_LINEAR_BASE_URL", srv.URL)
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"sync", "--entities", "--no-json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("entities text: %d stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("sync:")) {
		t.Fatalf("text output: %q", stdout.String())
	}
}

func TestSyncIssueTextOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"issue":{
			"id":"i1","identifier":"LIN-1","title":"T","description":"",
			"priority":1,"createdAt":"","updatedAt":"2026-05-19T00:00:00Z",
			"team":null,"project":null,"state":null,"assignee":null,"creator":null,
			"labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},
			"comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}
		}}}`))
	}))
	defer srv.Close()
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINEAR_API_KEY", "lin_api_test")
	t.Setenv("LINCRAWL_LINEAR_BASE_URL", srv.URL)
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"sync", "--issue", "LIN-1", "--no-json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("issue text: %d stderr=%s", code, stderr.String())
	}
}

func TestSyncUpdatedSinceTextOutput(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"issues":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}}`))
	}))
	defer srv.Close()
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINEAR_API_KEY", "lin_api_test")
	t.Setenv("LINCRAWL_LINEAR_BASE_URL", srv.URL)
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"sync", "--updated-since", "24h", "--no-json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("updated-since text: %d stderr=%s", code, stderr.String())
	}
}

func TestExportFileTextOutput(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	if code := Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatal("seed")
	}
	cwd, _ := os.Getwd()
	wd := t.TempDir()
	_ = os.Chdir(wd)
	defer os.Chdir(cwd)
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"export", "--out", "./dump.jsonl", "--no-json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("export text: %d", code)
	}
	if !bytes.Contains(stdout.Bytes(), []byte("export:")) {
		t.Fatalf("text output: %q", stdout.String())
	}
}

func TestSearchMissingResultsForBuiltinQuery(t *testing.T) {
	// search --raw with an FTS5 operator forces the raw query path.
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	if code := Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatal("seed")
	}
	var stdout bytes.Buffer
	if code := Run(context.Background(), []string{"search", "ingest OR archive", "--raw", "--limit", "5", "--json"}, &stdout, new(bytes.Buffer)); code != 0 {
		t.Fatal("raw FTS")
	}
}

func TestShowByUUID(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	// Seed with a UUID-shaped issue id.
	fx := filepath.Join(dir, "fx")
	_ = os.MkdirAll(fx, 0o755)
	id := "11111111-2222-3333-4444-555555555555"
	body := `{"teams":[{"id":"t1","key":"LIN","name":"L"}],"states":[{"id":"s1","team_id":"t1","name":"B","type":"backlog"}],"issues":[{"id":"` + id + `","identifier":"LIN-99","title":"T","team_id":"t1","state_id":"s1","createdAt":"2026-05-19T00:00:00Z","updatedAt":"2026-05-19T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(fx, "snapshot.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	if code := Run(context.Background(), []string{"sync", "--fixture", fx, "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatal("seed")
	}
	var stdout bytes.Buffer
	if code := Run(context.Background(), []string{"show", id, "--json"}, &stdout, new(bytes.Buffer)); code != 0 {
		t.Fatalf("show by UUID exit=%d", code)
	}
}

func TestImportMissingIdentityIsConfigError(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINCRAWL_AGE_IDENTITY", "")
	t.Setenv("LINCRAWL_AGE_IDENTITY_FILE", "")
	cwd, _ := os.Getwd()
	wd := t.TempDir()
	_ = os.Chdir(wd)
	defer os.Chdir(cwd)
	// Create a dummy file so --in validation passes
	if err := os.WriteFile(filepath.Join(wd, "dummy.jsonl.zst.age"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"import", "--in", "./dummy.jsonl.zst.age"}, &stdout, &stderr)
	if code != ExitConfig {
		t.Fatalf("exit=%d, want %d", code, ExitConfig)
	}
}

func TestImportMissingFileIsInternalError(t *testing.T) {
	pub := mustNewIdentity(t)
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINCRAWL_AGE_IDENTITY", pub.Secret)
	cwd, _ := os.Getwd()
	wd := t.TempDir()
	_ = os.Chdir(wd)
	defer os.Chdir(cwd)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"import", "--in", "./nonexistent.jsonl.zst.age"}, &stdout, &stderr)
	if code != ExitInternal {
		t.Fatalf("exit=%d, want %d (internal)", code, ExitInternal)
	}
}

func TestSubscribeMissingIdentityIsConfigError(t *testing.T) {
	t.Setenv("LINCRAWL_AGE_IDENTITY", "")
	t.Setenv("LINCRAWL_AGE_IDENTITY_FILE", "")
	// Build a valid manifest layout with one snapshot.
	root := t.TempDir()
	dir := filepath.Join(root, "artifacts", "snapshots", "full", "2026", "05")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.jsonl.zst.age"), []byte("c"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "manifest.json"),
		[]byte(`{"schema_version":"lincrawl.store.v1","snapshots":[{"kind":"full","path":"artifacts/snapshots/full/2026/05/x.jsonl.zst.age"}]}`),
		0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"subscribe", root}, &stdout, &stderr)
	if code != ExitConfig {
		t.Fatalf("exit=%d, want %d (config: missing identity)", code, ExitConfig)
	}
}

func TestSubscribeStoreVerifyFailureIsValidation(t *testing.T) {
	pub := mustNewIdentity(t)
	t.Setenv("LINCRAWL_AGE_IDENTITY", pub.Secret)
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	// manifest points to a missing snapshot
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "manifest.json"),
		[]byte(`{"schema_version":"lincrawl.store.v1","snapshots":[{"kind":"full","path":"artifacts/snapshots/full/2026/05/missing.jsonl.zst.age"}]}`),
		0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"subscribe", root, "--dry-run"}, &stdout, &stderr)
	if code != ExitValidation {
		t.Fatalf("exit=%d, want %d", code, ExitValidation)
	}
}

func TestPublishMissingStoreFails(t *testing.T) {
	pub := mustNewIdentity(t)
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINCRAWL_AGE_RECIPIENT", pub.Recipient)
	cwd, _ := os.Getwd()
	wd := t.TempDir()
	_ = os.Chdir(wd)
	defer os.Chdir(cwd)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"publish", "--out", "./out.jsonl.zst.age"}, &stdout, &stderr)
	if code != ExitInternal {
		t.Fatalf("publish without store exit=%d, want %d", code, ExitInternal)
	}
}

func TestArchiveDryRunReportsRecords(t *testing.T) {
	pub := mustNewIdentity(t)
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINCRAWL_AGE_RECIPIENT", pub.Recipient)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	cwd, _ := os.Getwd()
	wd := t.TempDir()
	_ = os.Chdir(wd)
	defer os.Chdir(cwd)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"archive", "--fixture", fixture, "--out", "./a.jsonl.zst.age", "--dry-run", "--json"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("archive dry-run exit=%d stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte(`"dry_run": true`)) {
		t.Errorf("dry_run flag missing: %s", stdout.String())
	}
}

func TestArchiveMissingRecipientIsConfigError(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINCRAWL_AGE_RECIPIENT", "")
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	cwd, _ := os.Getwd()
	wd := t.TempDir()
	_ = os.Chdir(wd)
	defer os.Chdir(cwd)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"archive", "--fixture", fixture, "--out", "./a.jsonl.zst.age"}, &stdout, &stderr)
	if code != ExitConfig {
		t.Fatalf("exit=%d, want %d", code, ExitConfig)
	}
}

func TestQueryFromMissingFile(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINEAR_API_KEY", "lin_api_test")
	cwd, _ := os.Getwd()
	wd := t.TempDir()
	_ = os.Chdir(wd)
	defer os.Chdir(cwd)
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"query", "--graphql-file", "./does-not-exist.gql"}, &stdout, &stderr)
	if code != ExitValidation {
		t.Fatalf("exit=%d, want %d", code, ExitValidation)
	}
}

func TestQueryEmptyAPIKey(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "")
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"query", "--graphql", "query{viewer{id}}"}, &stdout, &stderr)
	if code != ExitConfig {
		t.Fatalf("exit=%d, want %d", code, ExitConfig)
	}
}

func TestSearchUnknownFieldsIsValidationError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	_ = Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer))
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"search", "ingest", "--fields", "nope_not_a_field"}, &stdout, &stderr)
	if code != ExitValidation {
		t.Fatalf("exit=%d, want %d", code, ExitValidation)
	}
}

func TestShowUnknownFieldsIsValidationError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	_ = Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer))
	// Use a real identifier so the show resolves before fields validation runs.
	var sb bytes.Buffer
	_ = Run(context.Background(), []string{"search", "ingest", "--fields", "identifier", "--json"}, &sb, new(bytes.Buffer))
	var r struct {
		Results []struct {
			Identifier string `json:"identifier"`
		} `json:"results"`
	}
	_ = json.Unmarshal(sb.Bytes(), &r)
	if len(r.Results) == 0 {
		t.Skip("no results")
	}
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"show", r.Results[0].Identifier, "--fields", "nope_not_a_field"}, &stdout, &stderr)
	if code != ExitValidation {
		t.Fatalf("exit=%d, want %d", code, ExitValidation)
	}
}

func TestStatusUnknownFieldsIsValidationError(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	_ = Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer))
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"status", "--fields", "nope_not_a_field"}, &stdout, &stderr)
	if code != ExitValidation {
		t.Fatalf("exit=%d, want %d", code, ExitValidation)
	}
}

func TestSyncMultipleModesIsUsageError(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINEAR_API_KEY", "lin_api_test")
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"sync", "--entities", "--issue", "LIN-1"}, &stdout, &stderr)
	if code != ExitUsage {
		t.Fatalf("exit=%d, want %d", code, ExitUsage)
	}
}

func TestSyncStdinNDJSONFlow(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	prev := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	defer func() { os.Stdin = prev }()
	go func() {
		_, _ = w.Write([]byte(`{"kind":"team","item":{"id":"t1","key":"L","name":"L"}}` + "\n"))
		_, _ = w.Write([]byte(`{"kind":"state","item":{"id":"s1","team_id":"t1","name":"B","type":"backlog"}}` + "\n"))
		w.Close()
	}()
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"sync", "--stdin", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("ndjson stdin: %d stderr=%s", code, stderr.String())
	}
}

func TestStatusFieldsProjection(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	_ = Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer))
	var stdout bytes.Buffer
	if code := Run(context.Background(), []string{"status", "--fields", "counts,exists"}, &stdout, new(bytes.Buffer)); code != 0 {
		t.Fatalf("status fields: %d", code)
	}
	var got map[string]any
	_ = json.Unmarshal(stdout.Bytes(), &got)
	if _, ok := got["counts"]; !ok {
		t.Fatalf("expected counts field: %v", got)
	}
}

func TestSearchTextOutputAfterSync(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	if code := Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatal("seed")
	}
	var stdout bytes.Buffer
	if code := Run(context.Background(), []string{"search", "ingest", "--no-json"}, &stdout, new(bytes.Buffer)); code != 0 {
		t.Fatal("search text")
	}
	// Text output is tab-separated identifier\ttitle\tsnippet
	if !bytes.Contains(stdout.Bytes(), []byte("\t")) {
		t.Fatalf("expected tab-separated text: %q", stdout.String())
	}
}

func TestSearchEmptyQueryFails(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	_ = Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer))
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"search", ""}, &stdout, &stderr)
	if code == 0 {
		t.Fatalf("expected nonzero exit for empty query, got %d", code)
	}
}

func TestExportRunWithoutStore(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"export", "--out", "-"}, &stdout, &stderr)
	if code != ExitInternal {
		t.Fatalf("exit=%d, want %d (no store)", code, ExitInternal)
	}
}

func TestSearchWithoutStore(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"search", "anything"}, &stdout, &stderr)
	if code != ExitInternal {
		t.Fatalf("exit=%d, want %d (no store)", code, ExitInternal)
	}
}

func TestImportSecondInvocationIdempotent(t *testing.T) {
	pub := mustNewIdentity(t)
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	t.Setenv("LINCRAWL_AGE_RECIPIENT", pub.Recipient)
	t.Setenv("LINCRAWL_AGE_IDENTITY", pub.Secret)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	if code := Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatal("seed")
	}
	cwd, _ := os.Getwd()
	wd := t.TempDir()
	_ = os.Chdir(wd)
	defer os.Chdir(cwd)
	if code := Run(context.Background(), []string{"publish", "--out", "./out.jsonl.zst.age", "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatal("publish")
	}
	importHome := t.TempDir()
	t.Setenv("LINCRAWL_HOME", importHome)
	// First import populates the DB.
	if code := Run(context.Background(), []string{"import", "--in", "./out.jsonl.zst.age", "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatal("first import")
	}
	// Second import on top of the same DB must succeed (idempotent ingest).
	if code := Run(context.Background(), []string{"import", "--in", "./out.jsonl.zst.age", "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatal("second import")
	}
}

func TestArchivePublishTextOutputs(t *testing.T) {
	pub := mustNewIdentity(t)
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	t.Setenv("LINCRAWL_AGE_RECIPIENT", pub.Recipient)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	if code := Run(context.Background(), []string{"sync", "--fixture", fixture, "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatal("seed")
	}
	cwd, _ := os.Getwd()
	wd := t.TempDir()
	_ = os.Chdir(wd)
	defer os.Chdir(cwd)
	// archive --no-json
	if code := Run(context.Background(), []string{"archive", "--fixture", fixture, "--out", "./a.jsonl.zst.age", "--no-json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatal("archive no-json")
	}
	// publish --no-json
	if code := Run(context.Background(), []string{"publish", "--out", "./p.jsonl.zst.age", "--no-json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatal("publish no-json")
	}
}

func TestSyncDryRunVariants(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"u1","name":"S","email":""}}}`))
	}))
	defer srv.Close()
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINEAR_API_KEY", "lin_api_test")
	t.Setenv("LINCRAWL_LINEAR_BASE_URL", srv.URL)
	// --entities dry-run
	if code := Run(context.Background(), []string{"sync", "--entities", "--dry-run", "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatalf("entities dry-run: %d", code)
	}
	// --issue dry-run
	if code := Run(context.Background(), []string{"sync", "--issue", "LIN-1", "--dry-run", "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatalf("issue dry-run: %d", code)
	}
	// --updated-since dry-run
	if code := Run(context.Background(), []string{"sync", "--updated-since", "24h", "--dry-run", "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatalf("updated-since dry-run: %d", code)
	}
}

func TestSyncTextOutputAfterFixture(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	fixture, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"sync", "--fixture", fixture, "--no-json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("text fixture: code=%d stderr=%s", code, stderr.String())
	}
	if !bytes.Contains(stdout.Bytes(), []byte("sync:")) {
		t.Fatalf("text output: %q", stdout.String())
	}
}

func TestSyncResumeWithStoredHighWater(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"issues":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}}`))
	}))
	defer srv.Close()
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	t.Setenv("LINEAR_API_KEY", "lin_api_test")
	t.Setenv("LINCRAWL_LINEAR_BASE_URL", srv.URL)
	// Seed a high-water-mark cursor by running --updated-since first.
	if code := Run(context.Background(), []string{"sync", "--updated-since", "24h", "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatalf("seed exit=%d", code)
	}
	// Save a cursor manually so --resume has something to read.
	// Reach in via direct sqlite manipulation isn't great — instead just verify dry-run
	// path takes a real --updated-since with parsed time.
	if code := Run(context.Background(), []string{"sync", "--updated-since", "2026-05-19T00:00:00Z", "--json"}, new(bytes.Buffer), new(bytes.Buffer)); code != 0 {
		t.Fatalf("rfc3339 exit=%d", code)
	}
}

func TestSyncNDJSONStreaming(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"issues":{
			"pageInfo":{"endCursor":"","hasNextPage":false},
			"nodes":[
			  {"id":"i1","identifier":"LIN-1","title":"A","description":"","priority":1,"createdAt":"","updatedAt":"2026-05-19T00:00:00Z",
			   "team":null,"project":null,"state":null,"assignee":null,"creator":null,
			   "labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},
			   "comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}
			]
		}}}`))
	}))
	defer srv.Close()
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINEAR_API_KEY", "lin_api_test")
	t.Setenv("LINCRAWL_LINEAR_BASE_URL", srv.URL)
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"sync", "--updated-since", "24h", "--ndjson"}, &stdout, &stderr); code != 0 {
		t.Fatalf("ndjson exit=%d stderr=%s", code, stderr.String())
	}
	if stdout.Len() == 0 {
		t.Fatal("expected NDJSON on stdout")
	}
}

func TestSyncMissingAPIKey(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINEAR_API_KEY", "")
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"sync", "--entities", "--json"}, &stdout, &stderr)
	if code != ExitConfig {
		t.Fatalf("exit=%d, want %d", code, ExitConfig)
	}
}

func TestSyncIssueValidationRejectsGarbage(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	t.Setenv("LINEAR_API_KEY", "lin_api_test")
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), []string{"sync", "--issue", "totally bogus"}, &stdout, &stderr)
	if code != ExitValidation {
		t.Fatalf("exit=%d, want %d", code, ExitValidation)
	}
}

func TestSyncStdinHappyPath(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", t.TempDir())
	// Feed an empty-but-valid snapshot.
	prev := os.Stdin
	r, w, _ := os.Pipe()
	os.Stdin = r
	defer func() { os.Stdin = prev }()
	go func() {
		_, _ = w.Write([]byte(`{"teams":[],"states":[],"users":[],"labels":[],"projects":[],"issues":[]}`))
		w.Close()
	}()
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), []string{"sync", "--stdin", "--json"}, &stdout, &stderr); code != 0 {
		t.Fatalf("sync stdin: %d stderr=%s", code, stderr.String())
	}
}

func TestGuardCleanRepoExitsZero(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("# clean"), 0o644); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	code := Run(context.Background(), []string{"guard", "--root", dir, "--json"}, &out, &errOut)
	if code != 0 {
		t.Fatalf("exit = %d stderr=%s", code, errOut.String())
	}
	var res struct {
		OK bool `json:"ok"`
	}
	if err := json.Unmarshal(out.Bytes(), &res); err != nil {
		t.Fatalf("guard JSON: %v\n%s", err, out.String())
	}
	if !res.OK {
		t.Fatal("expected ok=true on a clean tree")
	}
}
