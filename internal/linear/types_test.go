package linear

import (
	"os"
	"path/filepath"
	"testing"
)

func TestLoadFixtureSynthetic(t *testing.T) {
	repo := filepath.Join("..", "..", "testdata", "synthetic")
	snap, err := LoadFixture(repo)
	if err != nil {
		t.Fatalf("LoadFixture: %v", err)
	}
	if len(snap.Teams) == 0 || len(snap.Issues) == 0 || len(snap.States) == 0 {
		t.Fatalf("expected non-empty entities, got teams=%d issues=%d states=%d",
			len(snap.Teams), len(snap.Issues), len(snap.States))
	}
	for _, iss := range snap.Issues {
		if iss.Identifier == "" {
			t.Fatalf("issue %s missing identifier", iss.ID)
		}
	}
}

func TestLoadFixtureRejectsNonDirectory(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "file")
	if err := os.WriteFile(p, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFixture(p); err == nil {
		t.Fatal("expected non-dir error")
	}
}

func TestLoadFixtureRejectsMissingDirectory(t *testing.T) {
	if _, err := LoadFixture(filepath.Join(t.TempDir(), "nope")); err == nil {
		t.Fatal("expected stat error")
	}
}

func TestLoadFixtureRejectsEmptyDir(t *testing.T) {
	if _, err := LoadFixture(t.TempDir()); err == nil {
		t.Fatal("expected no-snapshot error")
	}
}

func TestLoadFixtureRejectsMalformedJSON(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "snapshot.json"), []byte("not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFixture(dir); err == nil {
		t.Fatal("expected JSON parse error")
	}
}

func TestLoadFixtureMergesMultipleFiles(t *testing.T) {
	dir := t.TempDir()
	a := `{"teams":[{"id":"t1","key":"LIN","name":"L"}],"states":[{"id":"s1","team_id":"t1","name":"B","type":"backlog"}],"issues":[{"id":"i1","identifier":"LIN-1","title":"X","team_id":"t1","state_id":"s1","createdAt":"2026-05-19T00:00:00Z","updatedAt":"2026-05-19T00:00:00Z"}]}`
	b := `{"teams":[],"issues":[{"id":"i2","identifier":"LIN-2","title":"Y","team_id":"t1","state_id":"s1","createdAt":"2026-05-19T00:00:00Z","updatedAt":"2026-05-19T00:00:00Z"}]}`
	if err := os.WriteFile(filepath.Join(dir, "snapshot.json"), []byte(a), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "more.snapshot.json"), []byte(b), 0o600); err != nil {
		t.Fatal(err)
	}
	snap, err := LoadFixture(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Issues) != 2 {
		t.Fatalf("issues = %d", len(snap.Issues))
	}
}

func TestSnapshotValidateBranches(t *testing.T) {
	// Missing id/identifier
	if err := (Snapshot{Issues: []Issue{{ID: "", Identifier: ""}}}).Validate(); err == nil {
		t.Error("missing id should error")
	}
	// Unknown team
	if err := (Snapshot{Issues: []Issue{{ID: "i", Identifier: "I-1", TeamID: "ghost"}}}).Validate(); err == nil {
		t.Error("unknown team should error")
	}
	// Unknown project
	teams := []Team{{ID: "t1"}}
	if err := (Snapshot{Teams: teams, Issues: []Issue{{ID: "i", Identifier: "I-1", TeamID: "t1", ProjectID: "ghost"}}}).Validate(); err == nil {
		t.Error("unknown project should error")
	}
	// Unknown assignee
	if err := (Snapshot{Teams: teams, Issues: []Issue{{ID: "i", Identifier: "I-1", TeamID: "t1", AssigneeID: "ghost"}}}).Validate(); err == nil {
		t.Error("unknown assignee should error")
	}
	// Unknown label
	if err := (Snapshot{Teams: teams, Issues: []Issue{{ID: "i", Identifier: "I-1", TeamID: "t1", LabelIDs: []string{"ghost"}}}}).Validate(); err == nil {
		t.Error("unknown label should error")
	}
	// Bad updated_at
	if err := (Snapshot{Teams: teams, Issues: []Issue{{ID: "i", Identifier: "I-1", TeamID: "t1", UpdatedAt: "notatime"}}}).Validate(); err == nil {
		t.Error("bad updated_at should error")
	}
}

func TestSnapshotValidateRejectsDanglingState(t *testing.T) {
	snap := Snapshot{
		Teams:  []Team{{ID: "t1", Key: "LIN", Name: "Lincrawl"}},
		Issues: []Issue{{ID: "i1", Identifier: "LIN-1", TeamID: "t1", StateID: "missing"}},
	}
	if err := snap.Validate(); err == nil {
		t.Fatal("expected error for dangling state reference")
	}
}
