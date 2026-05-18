package lock

import (
	"path/filepath"
	"strings"
	"testing"
)

func TestAcquireReleaseRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lincrawl.db")
	l, err := Acquire(dbPath)
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	if l == nil {
		t.Fatal("expected non-nil lock")
	}
	if err := l.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
}

func TestAcquireRejectsSecondHolder(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lincrawl.db")
	first, err := Acquire(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer first.Release()

	if _, err := Acquire(dbPath); err == nil {
		t.Fatal("expected lock contention error")
	} else if !strings.Contains(err.Error(), "store is locked") {
		t.Fatalf("error should mention lock: %v", err)
	}
}
