package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var (
	_ = os.Setenv
	_ = strings.HasPrefix
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

func TestLinearAPIKeyTrimsWhitespace(t *testing.T) {
	t.Setenv("LINEAR_API_KEY", "   secret\n  ")
	if got := LinearAPIKey(); got != "secret" {
		t.Fatalf("LinearAPIKey() = %q", got)
	}
	t.Setenv("LINEAR_API_KEY", "")
	if got := LinearAPIKey(); got != "" {
		t.Fatalf("empty: %q", got)
	}
}

func TestEnsureDirsCreatesPaths(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "lincrawl-home")
	rt := Runtime{Home: dir, ConfigDir: filepath.Join(dir, "config")}
	if err := EnsureDirs(rt); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(rt.Home); err != nil {
		t.Errorf("home not created: %v", err)
	}
	if _, err := os.Stat(rt.ConfigDir); err != nil {
		t.Errorf("config dir not created: %v", err)
	}
}

func TestDefaultDataDirRespectsXDG(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "/tmp/xdg")
	got, err := defaultDataDir()
	if err != nil {
		t.Fatal(err)
	}
	if got != filepath.Join("/tmp/xdg", AppID) {
		t.Errorf("xdg: %s", got)
	}
}

func TestDefaultDataDirFallsBackToHome(t *testing.T) {
	t.Setenv("XDG_DATA_HOME", "")
	got, err := defaultDataDir()
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, filepath.Join(".local", "share", AppID)) {
		t.Errorf("home fallback: %s", got)
	}
}

func TestRedact(t *testing.T) {
	if Redact(true) != "set" {
		t.Error("true")
	}
	if Redact(false) != "unset" {
		t.Error("false")
	}
}

func TestLoadDotEnvMalformedLineReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.local")
	if err := os.WriteFile(path, []byte("VALID=ok\nNOT_KEY_VALUE_LINE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := LoadDotEnv(path); err == nil {
		t.Fatal("expected parse error")
	}
}

func TestLoadDotEnvSingleQuotedAndEmptyKey(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, ".env.local")
	if err := os.WriteFile(path, []byte("SQ_TEST='single quoted'\nEMPTY_KEY=\n=novalue\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	_ = os.Unsetenv("SQ_TEST")
	if _, _, err := LoadDotEnv(path); err != nil {
		t.Fatal(err)
	}
	if got := os.Getenv("SQ_TEST"); got != "single quoted" {
		t.Errorf("SQ_TEST = %q", got)
	}
}

func TestEnsureDirsIdempotent(t *testing.T) {
	dir := t.TempDir()
	rt := Runtime{Home: dir, ConfigDir: filepath.Join(dir, "config")}
	if err := EnsureDirs(rt); err != nil {
		t.Fatal(err)
	}
	if err := EnsureDirs(rt); err != nil {
		t.Fatal(err)
	}
}

func TestEnsureDirsFailsOnFileCollision(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "blocker"), []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	rt := Runtime{Home: filepath.Join(dir, "blocker"), ConfigDir: filepath.Join(dir, "blocker", "config")}
	if err := EnsureDirs(rt); err == nil {
		t.Fatal("expected EnsureDirs to fail on file collision")
	}
}

func TestUnquoteEdges(t *testing.T) {
	if got := unquote(``); got != "" {
		t.Errorf("empty: %q", got)
	}
	if got := unquote(`x`); got != "x" {
		t.Errorf("single char: %q", got)
	}
	if got := unquote(`"q"`); got != "q" {
		t.Errorf("doublequoted: %q", got)
	}
	if got := unquote(`bare`); got != "bare" {
		t.Errorf("bare: %q", got)
	}
}

func TestLoadRuntimeWithoutLINCRAWLHome(t *testing.T) {
	t.Setenv("LINCRAWL_HOME", "")
	t.Setenv("XDG_DATA_HOME", t.TempDir())
	rt, err := LoadRuntime()
	if err != nil {
		t.Fatal(err)
	}
	if rt.Home == "" {
		t.Fatal("expected XDG-derived home")
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
