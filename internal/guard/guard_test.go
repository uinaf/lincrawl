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

func must(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatal(err)
	}
}
