package buildinfo

import "testing"

func TestCurrentReflectsPackageVars(t *testing.T) {
	prevV, prevC, prevD := Version, Commit, Date
	t.Cleanup(func() { Version, Commit, Date = prevV, prevC, prevD })
	Version = "1.2.3"
	Commit = "deadbeef"
	Date = "2026-05-19T00:00:00Z"
	got := Current()
	if got.Version != "1.2.3" || got.Commit != "deadbeef" || got.Date != "2026-05-19T00:00:00Z" {
		t.Fatalf("Current = %+v", got)
	}
}
