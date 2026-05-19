package lock

import (
	"os"
	"path/filepath"
	"strconv"
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

func TestAcquireRejectsEmptyPath(t *testing.T) {
	if _, err := Acquire(""); err == nil {
		t.Fatal("expected error on empty path")
	}
}

func TestNilReleaseIsSafe(t *testing.T) {
	var l *FileLock
	if err := l.Release(); err != nil {
		t.Fatalf("nil release: %v", err)
	}
}

func TestReleaseTwiceIsSafe(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lincrawl.db")
	l, err := Acquire(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("first release: %v", err)
	}
	if err := l.Release(); err != nil {
		t.Fatalf("second release should be a no-op: %v", err)
	}
}

func TestAcquireWritesPID(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lincrawl.db")
	l, err := Acquire(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = l.Release() })
	raw, err := os.ReadFile(dbPath + ".lock")
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) == 0 {
		t.Fatal("lock file empty")
	}
	pid, err := strconv.Atoi(string(raw))
	if err != nil || pid <= 0 {
		t.Fatalf("expected positive PID, got %q (%v)", raw, err)
	}
}

func TestAcquireReadOnlyDirFailsCleanly(t *testing.T) {
	dir := t.TempDir()
	if err := os.Chmod(dir, 0o500); err != nil {
		t.Skip("cannot chmod tmpdir")
	}
	t.Cleanup(func() { _ = os.Chmod(dir, 0o755) })
	if _, err := Acquire(filepath.Join(dir, "lincrawl.db")); err == nil {
		t.Skip("running as root; cannot fail open")
	}
}

func TestAcquireReleaseAcquireRoundTrip(t *testing.T) {
	dbPath := filepath.Join(t.TempDir(), "lincrawl.db")
	l1, err := Acquire(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := l1.Release(); err != nil {
		t.Fatal(err)
	}
	// After release, a new Acquire must succeed.
	l2, err := Acquire(dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_ = l2.Release()
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
