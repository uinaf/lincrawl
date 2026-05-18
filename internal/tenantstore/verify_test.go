package tenantstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeManifest(t *testing.T, root string, m Manifest) {
	t.Helper()
	raw, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(root, ManifestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyEmptyManifest(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, Manifest{SchemaVersion: "lincrawl.store.v1", Snapshots: []Snapshot{}})
	res, err := Verify(root)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("expected ok, got findings: %v", res.Findings)
	}
}

func TestVerifyRejectsMissingSnapshotFile(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, Manifest{
		SchemaVersion: "lincrawl.store.v1",
		Snapshots: []Snapshot{
			{Kind: "full", Path: "artifacts/snapshots/full/2026/05/missing.jsonl.zst.age"},
		},
	})
	res, _ := Verify(root)
	if res.OK {
		t.Fatal("expected failure on missing snapshot")
	}
}

func TestVerifyAcceptsPresentSnapshot(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "artifacts", "snapshots", "full", "2026", "05")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "lincrawl-full-20260519T000000Z.jsonl.zst.age"), []byte("encrypted"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, root, Manifest{
		SchemaVersion: "lincrawl.store.v1",
		Snapshots: []Snapshot{
			{Kind: "full", Path: "artifacts/snapshots/full/2026/05/lincrawl-full-20260519T000000Z.jsonl.zst.age"},
		},
	})
	res, err := Verify(root)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("expected ok, got %v", res.Findings)
	}
	if len(res.Snapshots) != 1 {
		t.Fatalf("expected 1 snapshot, got %d", len(res.Snapshots))
	}
}

func TestVerifyRejectsPlaintextArtifact(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "leak.jsonl"), []byte("plain"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, root, Manifest{SchemaVersion: "lincrawl.store.v1", Snapshots: []Snapshot{}})
	res, _ := Verify(root)
	if res.OK {
		t.Fatal("expected failure on plaintext artifact")
	}
}

func TestVerifyRejectsForbiddenDir(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "logs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, root, Manifest{SchemaVersion: "lincrawl.store.v1", Snapshots: []Snapshot{}})
	res, _ := Verify(root)
	if res.OK {
		t.Fatal("expected failure on logs/")
	}
}

func TestSuggestSnapshotPath(t *testing.T) {
	got, err := SuggestSnapshotPath("full", "lincrawl-full-20260519T120000Z.jsonl.zst.age")
	if err != nil {
		t.Fatal(err)
	}
	want := "artifacts/snapshots/full/2026/05/lincrawl-full-20260519T120000Z.jsonl.zst.age"
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}
