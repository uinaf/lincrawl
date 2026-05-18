package linear

import (
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

func TestSnapshotValidateRejectsDanglingState(t *testing.T) {
	snap := Snapshot{
		Teams:  []Team{{ID: "t1", Key: "LIN", Name: "Lincrawl"}},
		Issues: []Issue{{ID: "i1", Identifier: "LIN-1", TeamID: "t1", StateID: "missing"}},
	}
	if err := snap.Validate(); err == nil {
		t.Fatal("expected error for dangling state reference")
	}
}
