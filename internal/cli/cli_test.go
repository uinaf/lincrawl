package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
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
