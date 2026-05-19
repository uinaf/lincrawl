package tenantstore

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestVerifiedSnapshotsHappyAndFailure(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "artifacts", "snapshots", "full", "2026", "05")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.jsonl.zst.age"), []byte("c"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, root, Manifest{
		SchemaVersion: "lincrawl.store.v1",
		Snapshots: []Snapshot{
			{Kind: "full", Path: "artifacts/snapshots/full/2026/05/x.jsonl.zst.age"},
		},
	})
	got, err := VerifiedSnapshots(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 || !strings.HasSuffix(got[0].FullPath, "x.jsonl.zst.age") {
		t.Fatalf("verified snapshots: %+v", got)
	}

	bad := t.TempDir()
	if _, err := VerifiedSnapshots(bad); err == nil {
		t.Fatal("expected error when manifest missing")
	}
}

func TestIsMonthEdges(t *testing.T) {
	if !isMonth("01") || !isMonth("12") {
		t.Error("valid months rejected")
	}
	if isMonth("1") || isMonth("aa") {
		t.Error("invalid months accepted")
	}
}

func TestIsYearEdges(t *testing.T) {
	if !isYear("2026") {
		t.Error("year rejected")
	}
	if isYear("202") || isYear("20266") || isYear("abcd") {
		t.Error("invalid years accepted")
	}
}

func TestSuggestSnapshotPathFallback(t *testing.T) {
	got, err := SuggestSnapshotPath("delta", "no-date-here.jsonl.zst.age")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(got, "no-date-here.jsonl.zst.age") {
		t.Errorf("fallback path = %q", got)
	}
}

func writeManifest(t *testing.T, root string, m Manifest) {
	t.Helper()
	raw, _ := json.MarshalIndent(m, "", "  ")
	if err := os.WriteFile(filepath.Join(root, ManifestName), raw, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestVerifyAcceptsListedSchemaVersions(t *testing.T) {
	// Pin the test against the constant so changes to AcceptedSchemaVersions
	// surface as a single failure rather than 11 hard-coded ones.
	for _, v := range AcceptedSchemaVersions {
		root := t.TempDir()
		writeManifest(t, root, Manifest{SchemaVersion: v, Snapshots: []Snapshot{}})
		res, err := Verify(root)
		if err != nil {
			t.Fatalf("%s: %v", v, err)
		}
		if !res.OK {
			t.Fatalf("%s should be accepted: %v", v, res.Findings)
		}
	}
	if got := len(AcceptedSchemaVersions); got != 2 {
		t.Fatalf("AcceptedSchemaVersions = %d, want 2 (lincrawl.store.v1 + putio.lincrawl.store.v1)", got)
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

func TestVerifyRejectsUnknownSchemaVersion(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, Manifest{SchemaVersion: "garbage.v0", Snapshots: []Snapshot{}})
	res, _ := Verify(root)
	if res.OK {
		t.Fatal("expected failure on unknown schema version")
	}
	found := false
	for _, f := range res.Findings {
		if strings.Contains(f, "schema_version") {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected schema_version finding, got %v", res.Findings)
	}
}

func TestVerifyEmptyManifestIsInformationalNotFailure(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, Manifest{SchemaVersion: "lincrawl.store.v1", Snapshots: []Snapshot{}})
	res, err := Verify(root)
	if err != nil {
		t.Fatal(err)
	}
	if !res.OK {
		t.Fatalf("empty manifest should be OK=true with informational finding, got findings=%v", res.Findings)
	}
	found := false
	for _, f := range res.Findings {
		if strings.Contains(f, "zero snapshots") {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected informational finding about empty snapshots, got %v", res.Findings)
	}
}

func TestVerifyRejectsDuplicateSnapshotPath(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "artifacts", "snapshots", "full", "2026", "05")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "x.jsonl.zst.age"), []byte("encrypted"), 0o600); err != nil {
		t.Fatal(err)
	}
	p := "artifacts/snapshots/full/2026/05/x.jsonl.zst.age"
	writeManifest(t, root, Manifest{
		SchemaVersion: "lincrawl.store.v1",
		Snapshots: []Snapshot{
			{Kind: "full", Path: p},
			{Kind: "full", Path: p},
		},
	})
	res, _ := Verify(root)
	if res.OK {
		t.Fatal("expected failure on duplicate snapshot path")
	}
}

func TestVerifyRejectsEmptySnapshotFile(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "artifacts", "snapshots", "full", "2026", "05")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	emptyPath := filepath.Join(dir, "empty.jsonl.zst.age")
	if err := os.WriteFile(emptyPath, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, root, Manifest{
		SchemaVersion: "lincrawl.store.v1",
		Snapshots: []Snapshot{
			{Kind: "full", Path: "artifacts/snapshots/full/2026/05/empty.jsonl.zst.age"},
		},
	})
	res, _ := Verify(root)
	if res.OK {
		t.Fatal("expected failure on empty snapshot file")
	}
}

func TestVerifyRejectsSnapshotSymlink(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "artifacts", "snapshots", "full", "2026", "05")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(t.TempDir(), "actual.bin")
	if err := os.WriteFile(target, []byte("ciphertext"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "linked.jsonl.zst.age")
	if err := os.Symlink(target, link); err != nil {
		t.Skip("symlinks unsupported:", err)
	}
	writeManifest(t, root, Manifest{
		SchemaVersion: "lincrawl.store.v1",
		Snapshots: []Snapshot{
			{Kind: "full", Path: "artifacts/snapshots/full/2026/05/linked.jsonl.zst.age"},
		},
	})
	res, _ := Verify(root)
	if res.OK {
		t.Fatal("expected failure on symlinked snapshot")
	}
}

func TestVerifyRejectsNonAllowedExtension(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "artifacts", "snapshots", "full", "2026", "05")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "wrong.bin"), []byte("data"), 0o600); err != nil {
		t.Fatal(err)
	}
	writeManifest(t, root, Manifest{SchemaVersion: "lincrawl.store.v1", Snapshots: []Snapshot{}})
	res, _ := Verify(root)
	if res.OK {
		t.Fatal("expected failure on non-allowed extension under artifacts/snapshots/")
	}
}

func TestVerifyRejectsTraversalInPath(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, Manifest{
		SchemaVersion: "lincrawl.store.v1",
		Snapshots: []Snapshot{
			{Kind: "full", Path: "artifacts/snapshots/full/../../etc/passwd.jsonl.zst.age"},
		},
	})
	res, _ := Verify(root)
	if res.OK {
		t.Fatal("expected failure on .. in snapshot path")
	}
}

func TestSuggestSnapshotPathRejectsInvalidKind(t *testing.T) {
	if _, err := SuggestSnapshotPath("bogus", "lincrawl-full-20260519T000000Z.jsonl.zst.age"); err == nil {
		t.Fatal("expected error on invalid kind")
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
