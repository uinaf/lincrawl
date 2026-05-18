package syncer

import (
	"path/filepath"
	"testing"

	"github.com/uinaf/lincrawl/internal/store"
)

func TestIngestFixtureNilStore(t *testing.T) {
	if _, err := IngestFixture(nil, "testdata/synthetic"); err == nil {
		t.Fatal("expected error on nil store")
	}
}

func TestIngestFixtureSurfacesFixtureError(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "lincrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	if _, err := IngestFixture(s, filepath.Join(t.TempDir(), "does-not-exist")); err == nil {
		t.Fatal("expected error for missing fixture directory")
	}
}

func TestIngestFixtureWritesCounts(t *testing.T) {
	s, err := store.Open(filepath.Join(t.TempDir(), "lincrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	fixture, err := filepath.Abs(filepath.Join("..", "..", "testdata", "synthetic"))
	if err != nil {
		t.Fatal(err)
	}
	res, err := IngestFixture(s, fixture)
	if err != nil {
		t.Fatalf("IngestFixture: %v", err)
	}
	if res.Counts.Issues == 0 {
		t.Fatalf("expected issues > 0, got %+v", res.Counts)
	}
	if res.Source != fixture {
		t.Fatalf("Source = %q, want %q", res.Source, fixture)
	}
}
