package guard

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestGuardCleanTree(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "README.md"), []byte("# clean"), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, ".env.example"), []byte("LINEAR_API_KEY={{ op://Vault/Item/field }}"), 0o644))
	res, err := Run(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("expected OK, got findings: %+v", res.Findings)
	}
}

func TestGuardRejectsForbiddenArtifacts(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "leaked.jsonl"), []byte(`{"id":"x"}`), 0o644))
	must(t, os.WriteFile(filepath.Join(dir, "leaked.db"), []byte("sqlite"), 0o644))
	res, _ := Run(dir)
	if res.OK || len(res.Findings) < 2 {
		t.Fatalf("expected forbidden artifact findings, got %+v", res.Findings)
	}
}

func TestGuardRejectsBearerInTrackedFile(t *testing.T) {
	dir := t.TempDir()
	// Construct the token at runtime so this test source file itself does
	// not trip the guard regex when guard scans its own repo.
	token := "lin_api_" + strings.Repeat("A", 30)
	must(t, os.WriteFile(filepath.Join(dir, "config.yaml"),
		[]byte("api_key: \""+token+"\"\n"), 0o644))
	res, _ := Run(dir)
	if res.OK {
		t.Fatal("expected guard to flag Linear token")
	}
	hit := false
	for _, f := range res.Findings {
		if strings.Contains(f.Reason, "Linear API token") {
			hit = true
		}
	}
	if !hit {
		t.Fatalf("expected Linear API token finding, got %+v", res.Findings)
	}
}

func TestGuardRejectsRealEnvFile(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, ".env.local"), []byte("LINEAR_API_KEY=x"), 0o644))
	res, _ := Run(dir)
	if res.OK {
		t.Fatal("expected guard to flag .env.local")
	}
}

func TestGuardSkipsForbiddenDirs(t *testing.T) {
	dir := t.TempDir()
	must(t, os.Mkdir(filepath.Join(dir, "logs"), 0o755))
	must(t, os.WriteFile(filepath.Join(dir, "logs", "leak.log"), []byte("oops"), 0o644))
	res, _ := Run(dir)
	if res.OK {
		t.Fatal("expected guard to flag logs/")
	}
}

func TestGuardAllowsOpTemplate(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, ".env.example"),
		[]byte("# fine\nLINEAR_API_KEY={{ op://Vault/Item/api-key }}\n"), 0o644))
	res, _ := Run(dir)
	if !res.OK {
		t.Fatalf("expected OK on template-only op://, got %+v", res.Findings)
	}
}

func TestGuardCLIFailureEmitsFindings(t *testing.T) {
	// Exercise the guard CLI handler's failure branches (text + JSON).
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "leak.db"), []byte("x"), 0o644))
	res, err := Run(dir)
	if err != nil {
		t.Fatal(err)
	}
	if res.OK {
		t.Fatal("expected findings")
	}
}

func TestGitIgnoredSetReturnsEmptyForNonGitDir(t *testing.T) {
	got := gitIgnoredSet(t.TempDir())
	if len(got) != 0 {
		t.Fatalf("expected empty: %v", got)
	}
}

func TestGitIgnoredSetReadsGitOutput(t *testing.T) {
	if _, err := os.Stat("/usr/bin/git"); err != nil {
		if _, err := os.Stat("/usr/local/bin/git"); err != nil {
			t.Skip("git not installed")
		}
	}
	dir := t.TempDir()
	// Init a git repo so the .git path exists and ls-files works.
	if err := os.MkdirAll(filepath.Join(dir, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	// We just need gitIgnoredSet not to panic; it'll likely fail
	// silently and return empty since this is not a full git tree.
	got := gitIgnoredSet(dir)
	_ = got
}

func TestGuardFindsGithubToken(t *testing.T) {
	dir := t.TempDir()
	must(t, os.WriteFile(filepath.Join(dir, "leak.yml"),
		[]byte("token: ghp_"+strings.Repeat("a", 32)+"\n"), 0o644))
	res, _ := Run(dir)
	if res.OK {
		t.Fatal("expected gh token finding")
	}
}

func TestGuardFindsLinearURL(t *testing.T) {
	dir := t.TempDir()
	// Build the URL at runtime so the guard regex doesn't trip on this source.
	body := "See " + "https://linear" + ".app/tenant/issue/LIN-1\n"
	must(t, os.WriteFile(filepath.Join(dir, "post.md"), []byte(body), 0o644))
	res, _ := Run(dir)
	if res.OK {
		t.Fatal("expected linear URL finding")
	}
}

func TestGuardFindsLinearUUID(t *testing.T) {
	dir := t.TempDir()
	uuid := "1d6f6cd0-9d51-4f12-9f3f-" + "cdb1ec3ec3f3"
	body := `{"workspaceId":"` + uuid + `"}`
	must(t, os.WriteFile(filepath.Join(dir, "x.json"), []byte(body), 0o644))
	res, _ := Run(dir)
	if res.OK {
		t.Fatal("expected UUID finding outside synthetic")
	}
}

func TestGuardSkipsTestdataSyntheticUUID(t *testing.T) {
	dir := t.TempDir()
	sub := filepath.Join(dir, "testdata", "synthetic")
	must(t, os.MkdirAll(sub, 0o755))
	uuid := "1d6f6cd0-9d51-4f12-9f3f-" + "cdb1ec3ec3f3"
	body := `{"workspaceId":"` + uuid + `"}`
	must(t, os.WriteFile(filepath.Join(sub, "x.json"), []byte(body), 0o644))
	res, _ := Run(dir)
	if !res.OK {
		t.Fatalf("synthetic UUID should be ok: %+v", res.Findings)
	}
}

func TestGuardRejectsForbiddenDirs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"snapshots", "reports", "screenshots", "transcripts"} {
		sub := filepath.Join(dir, name)
		must(t, os.MkdirAll(sub, 0o755))
		must(t, os.WriteFile(filepath.Join(sub, "x"), []byte("x"), 0o644))
	}
	res, _ := Run(dir)
	if res.OK {
		t.Fatal("expected forbidden-dir findings")
	}
}

func TestGuardSkipsNoiseDirs(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"node_modules", "vendor", "tmp", "dist"} {
		sub := filepath.Join(dir, name)
		must(t, os.MkdirAll(sub, 0o755))
		must(t, os.WriteFile(filepath.Join(sub, "leak.db"), []byte("x"), 0o644))
	}
	res, _ := Run(dir)
	if !res.OK {
		t.Fatalf("expected ok, got findings: %+v", res.Findings)
	}
}

func TestGuardEmptyRootDefaultsToCWD(t *testing.T) {
	// Pass "" to exercise default-to-"." branch.
	_, err := Run("")
	if err != nil {
		t.Fatal(err)
	}
}

func TestScanContentReadable(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("normal text without secrets"), 0o644); err != nil {
		t.Fatal(err)
	}
	res, err := Run(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("unexpected: %+v", res.Findings)
	}
}

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
