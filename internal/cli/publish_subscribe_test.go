package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"filippo.io/age"
)

// TestPublishSubscribeRoundTrip exercises the full
// publish -> store verify -> subscribe loop end-to-end:
//  1. seed a source archive by syncing testdata/synthetic
//  2. publish it as an encrypted snapshot under a tenant-store layout
//  3. write a manifest pointing at that snapshot
//  4. store verify the tenant tree
//  5. subscribe into a fresh LINCRAWL_HOME with the matching identity
//  6. assert dst counts equal src counts
func TestPublishSubscribeRoundTrip(t *testing.T) {
	identity, err := age.GenerateX25519Identity()
	if err != nil {
		t.Fatal(err)
	}
	recipient := identity.Recipient().String()
	identityText := identity.String()

	srcHome := t.TempDir()
	dstHome := t.TempDir()
	storeRoot := t.TempDir()
	identityFile := filepath.Join(t.TempDir(), "identity")
	if err := os.WriteFile(identityFile, []byte(identityText), 0o600); err != nil {
		t.Fatal(err)
	}

	fixture, err := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	if err != nil {
		t.Fatal(err)
	}

	// 1. seed src
	t.Setenv("LINCRAWL_HOME", srcHome)
	t.Setenv("LINCRAWL_AGE_RECIPIENT", recipient)
	t.Setenv("LINCRAWL_AGE_IDENTITY_FILE", identityFile)

	if code := runCapture(t, []string{"sync", "--fixture", fixture, "--json"}); code != 0 {
		t.Fatalf("sync src: exit=%d", code)
	}
	srcCounts := parseCounts(t, runJSON(t, []string{"status", "--json"}))

	// 2. publish into the tenant store at the canonical path
	snapPath := filepath.Join("artifacts", "snapshots", "full", "2026", "05", "lincrawl-full-20260519T000000Z.jsonl.zst.age")
	if err := os.MkdirAll(filepath.Join(storeRoot, filepath.Dir(snapPath)), 0o700); err != nil {
		t.Fatal(err)
	}
	prevDir, _ := os.Getwd()
	if err := os.Chdir(storeRoot); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(prevDir) })

	publishOut := runJSON(t, []string{"publish", "--out", snapPath, "--json"})
	records := jsonInt(t, publishOut, "records")
	if records == 0 {
		t.Fatalf("publish reported 0 records: %s", publishOut)
	}

	// 3. write a manifest pointing at it
	manifestPath := filepath.Join(storeRoot, "manifest.json")
	manifest := `{"schema_version":"lincrawl.store.v1","snapshots":[{"kind":"full","path":"` + snapPath + `"}]}`
	if err := os.WriteFile(manifestPath, []byte(manifest), 0o600); err != nil {
		t.Fatal(err)
	}

	// 4. store verify
	verifyOut := runJSON(t, []string{"store", "verify", storeRoot, "--json"})
	if !jsonBool(t, verifyOut, "ok") {
		t.Fatalf("store verify failed: %s", verifyOut)
	}

	// 5. subscribe into a fresh home
	t.Setenv("LINCRAWL_HOME", dstHome)
	subscribeOut := runJSON(t, []string{"subscribe", storeRoot, "--identity", identityText, "--json"})
	if got := jsonInt(t, subscribeOut, "ingested"); got != 1 {
		t.Fatalf("subscribe ingested = %d, want 1: %s", got, subscribeOut)
	}

	dstCounts := parseCounts(t, runJSON(t, []string{"status", "--json"}))

	// 6. counts match
	for _, key := range []string{"teams", "states", "users", "labels", "projects", "issues", "comments"} {
		if srcCounts[key] != dstCounts[key] {
			t.Errorf("counts.%s: src=%d dst=%d", key, srcCounts[key], dstCounts[key])
		}
	}
}

func runCapture(t *testing.T, args []string) int {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := Run(context.Background(), args, &stdout, &stderr)
	if code != 0 {
		t.Logf("stdout: %s", stdout.String())
		t.Logf("stderr: %s", stderr.String())
	}
	return code
}

func runJSON(t *testing.T, args []string) []byte {
	t.Helper()
	var stdout, stderr bytes.Buffer
	if code := Run(context.Background(), args, &stdout, &stderr); code != 0 {
		t.Fatalf("args=%v exit=%d stderr=%s", args, code, stderr.String())
	}
	return stdout.Bytes()
}

func parseCounts(t *testing.T, raw []byte) map[string]int {
	t.Helper()
	var envelope struct {
		Counts map[string]int `json:"counts"`
	}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		t.Fatalf("parseCounts: %v\n%s", err, raw)
	}
	return envelope.Counts
}

func jsonInt(t *testing.T, raw []byte, key string) int {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("jsonInt: %v\n%s", err, raw)
	}
	v, ok := m[key]
	if !ok {
		t.Fatalf("jsonInt: missing %q in %s", key, raw)
	}
	var n int
	if err := json.Unmarshal(v, &n); err != nil {
		t.Fatalf("jsonInt: %v for %q in %s", err, key, raw)
	}
	return n
}

func jsonBool(t *testing.T, raw []byte, key string) bool {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("jsonBool: %v\n%s", err, raw)
	}
	v, ok := m[key]
	if !ok {
		return false
	}
	var b bool
	if err := json.Unmarshal(v, &b); err != nil {
		t.Fatalf("jsonBool: %v for %q in %s", err, key, raw)
	}
	return b
}

// guard against unused imports if I remove a helper later
var _ = strings.TrimSpace
