package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadDotEnvSetsMissingKeys(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.local")
	contents := "# header\nLINCRAWL_TEST_KEY=val1\nQUOTED=\"val 2\"\nEMPTY_LINE=\n\nNOTSET=val3\n"
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("LINCRAWL_TEST_KEY", "")
	if err := os.Unsetenv("LINCRAWL_TEST_KEY"); err != nil {
		t.Fatal(err)
	}
	t.Setenv("NOTSET", "preset")

	loaded, abs, err := LoadDotEnv(path)
	if err != nil {
		t.Fatalf("LoadDotEnv: %v", err)
	}
	if !loaded {
		t.Fatal("expected loaded=true")
	}
	if !strings.HasSuffix(abs, ".env.local") {
		t.Fatalf("unexpected absolute path: %s", abs)
	}
	if got := os.Getenv("LINCRAWL_TEST_KEY"); got != "val1" {
		t.Fatalf("LINCRAWL_TEST_KEY = %q, want val1", got)
	}
	if got := os.Getenv("QUOTED"); got != "val 2" {
		t.Fatalf("QUOTED = %q, want %q", got, "val 2")
	}
	if got := os.Getenv("NOTSET"); got != "preset" {
		t.Fatalf("NOTSET = %q, want preset (preset value must win)", got)
	}
}

func TestLoadDotEnvMissingFileIsNoOp(t *testing.T) {
	loaded, _, err := LoadDotEnv(filepath.Join(t.TempDir(), "missing"))
	if err != nil {
		t.Fatalf("LoadDotEnv missing: %v", err)
	}
	if loaded {
		t.Fatal("expected loaded=false for missing file")
	}
}

func TestLoadRuntimeHomeOverride(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("LINCRAWL_HOME", dir)
	t.Setenv("LINEAR_API_KEY", "secret-but-we-only-check-presence")
	t.Setenv("LINCRAWL_LINEAR_BASE_URL", "https://example.test/graphql")

	rt, err := LoadRuntime()
	if err != nil {
		t.Fatalf("LoadRuntime: %v", err)
	}
	if rt.Home != dir {
		t.Fatalf("Home = %q, want %q", rt.Home, dir)
	}
	if rt.LinearAPIBase != "https://example.test/graphql" {
		t.Fatalf("LinearAPIBase = %q", rt.LinearAPIBase)
	}
	if !rt.LinearAPIKeySet {
		t.Fatal("expected LinearAPIKeySet=true")
	}
	if Redact(rt.LinearAPIKeySet) != "set" {
		t.Fatalf("Redact(true) = %q", Redact(rt.LinearAPIKeySet))
	}
	if Redact(false) != "unset" {
		t.Fatalf("Redact(false) = %q", Redact(false))
	}
}
