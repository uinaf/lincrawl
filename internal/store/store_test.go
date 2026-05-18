package store

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"testing"

	"github.com/uinaf/lincrawl/internal/linear"
)

func mustOpen(t *testing.T) *Store {
	t.Helper()
	dir := t.TempDir()
	s, err := Open(filepath.Join(dir, "lincrawl.db"))
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestIngestAndSearchRoundTrip(t *testing.T) {
	s := mustOpen(t)
	snap := linear.Snapshot{
		Teams:  []linear.Team{{ID: "team-1", Key: "LIN", Name: "Lincrawl"}},
		States: []linear.WorkflowState{{ID: "state-1", TeamID: "team-1", Name: "Backlog", Type: "backlog"}},
		Users:  []linear.User{{ID: "user-1", Name: "Sam"}},
		Labels: []linear.Label{{ID: "label-1", TeamID: "team-1", Name: "ingest"}},
		Issues: []linear.Issue{
			{
				ID: "issue-1", Identifier: "LIN-1", Title: "Crawl GraphQL ingest path",
				Description: "Need to ingest the cursor-paginated issues query.",
				TeamID:      "team-1", StateID: "state-1", AssigneeID: "user-1",
				LabelIDs:    []string{"label-1"},
				UpdatedAt:   "2026-05-18T10:00:00Z", CreatedAt: "2026-05-18T09:00:00Z",
				Comments: []linear.Comment{
					{ID: "c1", IssueID: "issue-1", AuthorID: "user-1", Body: "Looks straightforward.", CreatedAt: "2026-05-18T09:30:00Z", UpdatedAt: "2026-05-18T09:30:00Z"},
				},
			},
			{
				ID: "issue-2", Identifier: "LIN-2", Title: "Snapshot the archive nightly",
				Description: "Encrypted snapshot publish path.", TeamID: "team-1", StateID: "state-1",
				UpdatedAt: "2026-05-18T11:00:00Z", CreatedAt: "2026-05-18T08:00:00Z",
			},
		},
	}
	if err := s.IngestSnapshot(snap); err != nil {
		t.Fatalf("IngestSnapshot: %v", err)
	}

	if c, err := s.Counts(); err != nil {
		t.Fatalf("Counts: %v", err)
	} else if c.Issues != 2 || c.Comments != 1 || c.Labels != 1 {
		t.Fatalf("Counts = %+v", c)
	}

	results, err := s.Search("ingest", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(results) == 0 || results[0].Identifier != "LIN-1" {
		t.Fatalf("Search results = %+v", results)
	}

	rec, err := s.Show("LIN-1")
	if err != nil {
		t.Fatalf("Show: %v", err)
	}
	if rec.Title == "" || rec.TeamKey != "LIN" || len(rec.Comments) != 1 || rec.Labels[0] != "ingest" {
		t.Fatalf("Show record = %+v", rec)
	}

	// Idempotency: re-ingest must not duplicate.
	if err := s.IngestSnapshot(snap); err != nil {
		t.Fatalf("re-ingest: %v", err)
	}
	c, _ := s.Counts()
	if c.Issues != 2 || c.Comments != 1 {
		t.Fatalf("re-ingest changed counts: %+v", c)
	}
}

func TestShowMissing(t *testing.T) {
	s := mustOpen(t)
	if _, err := s.Show("no-such"); err == nil {
		t.Fatal("expected error for missing issue")
	}
}

func TestShowCaseInsensitiveIdentifier(t *testing.T) {
	s := mustOpen(t)
	snap := linear.Snapshot{
		Teams:  []linear.Team{{ID: "t1", Key: "LIN", Name: "L"}},
		States: []linear.WorkflowState{{ID: "s1", TeamID: "t1", Name: "B", Type: "backlog"}},
		Issues: []linear.Issue{{
			ID: "i1", Identifier: "LIN-7", Title: "Case test", TeamID: "t1", StateID: "s1",
			CreatedAt: "2026-05-18T00:00:00Z", UpdatedAt: "2026-05-18T00:00:00Z",
		}},
	}
	if err := s.IngestSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"LIN-7", "lin-7", "Lin-7"} {
		rec, err := s.Show(id)
		if err != nil {
			t.Fatalf("Show(%q): %v", id, err)
		}
		if rec.Identifier != "LIN-7" {
			t.Fatalf("Show(%q) returned %q", id, rec.Identifier)
		}
	}
}

func TestIngestPurgesRemovedCommentsAndLabels(t *testing.T) {
	s := mustOpen(t)
	base := linear.Snapshot{
		Teams:  []linear.Team{{ID: "t1", Key: "LIN", Name: "L"}},
		States: []linear.WorkflowState{{ID: "s1", TeamID: "t1", Name: "B", Type: "backlog"}},
		Users:  []linear.User{{ID: "u1", Name: "A"}},
		Labels: []linear.Label{{ID: "l1", TeamID: "t1", Name: "alpha"}, {ID: "l2", TeamID: "t1", Name: "beta"}},
		Issues: []linear.Issue{{
			ID: "i1", Identifier: "LIN-1", Title: "T", TeamID: "t1", StateID: "s1",
			LabelIDs:  []string{"l1", "l2"},
			CreatedAt: "2026-05-18T00:00:00Z", UpdatedAt: "2026-05-18T00:00:00Z",
			Comments: []linear.Comment{
				{ID: "c1", IssueID: "i1", AuthorID: "u1", Body: "first", CreatedAt: "2026-05-18T00:00:00Z", UpdatedAt: "2026-05-18T00:00:00Z"},
				{ID: "c2", IssueID: "i1", AuthorID: "u1", Body: "second", CreatedAt: "2026-05-18T00:00:01Z", UpdatedAt: "2026-05-18T00:00:01Z"},
			},
		}},
	}
	if err := s.IngestSnapshot(base); err != nil {
		t.Fatal(err)
	}

	// Refresh: drop one label and one comment.
	refreshed := base
	refreshed.Issues[0].LabelIDs = []string{"l1"}
	refreshed.Issues[0].Comments = refreshed.Issues[0].Comments[:1]
	if err := s.IngestSnapshot(refreshed); err != nil {
		t.Fatal(err)
	}

	rec, err := s.Show("LIN-1")
	if err != nil {
		t.Fatal(err)
	}
	if len(rec.Labels) != 1 || rec.Labels[0] != "alpha" {
		t.Fatalf("labels = %v, want [alpha]", rec.Labels)
	}
	if len(rec.Comments) != 1 || rec.Comments[0].ID != "c1" {
		t.Fatalf("comments = %+v, want [c1]", rec.Comments)
	}
}

func TestExportThenIngestStreamRoundTrip(t *testing.T) {
	src := mustOpen(t)
	dst := mustOpen(t)
	snap := linear.Snapshot{
		Teams:  []linear.Team{{ID: "t1", Key: "LIN", Name: "L"}},
		States: []linear.WorkflowState{{ID: "s1", TeamID: "t1", Name: "B", Type: "backlog"}},
		Users:  []linear.User{{ID: "u1", Name: "Sam"}},
		Labels: []linear.Label{{ID: "l1", TeamID: "t1", Name: "alpha"}},
		Projects: []linear.Project{{ID: "p1", Name: "Proj", State: "active", UpdatedAt: "2026-05-19T00:00:00Z"}},
		Issues: []linear.Issue{{
			ID: "i1", Identifier: "LIN-1", Title: "T", TeamID: "t1", StateID: "s1", AssigneeID: "u1",
			LabelIDs:  []string{"l1"},
			CreatedAt: "2026-05-18T00:00:00Z", UpdatedAt: "2026-05-19T00:00:00Z",
			Comments: []linear.Comment{
				{ID: "c1", IssueID: "i1", AuthorID: "u1", Body: "hi", CreatedAt: "2026-05-18T00:01:00Z", UpdatedAt: "2026-05-18T00:01:00Z"},
			},
		}},
	}
	if err := src.IngestSnapshot(snap); err != nil {
		t.Fatal(err)
	}

	var buf bytes.Buffer
	if _, err := src.ExportNDJSON(&buf); err != nil {
		t.Fatalf("ExportNDJSON: %v", err)
	}

	n, err := dst.IngestStream(&buf, 0)
	if err != nil {
		t.Fatalf("IngestStream: %v", err)
	}
	if n == 0 {
		t.Fatal("expected non-zero ingest count")
	}
	srcC, _ := src.Counts()
	dstC, _ := dst.Counts()
	if srcC != dstC {
		t.Fatalf("counts differ:\nsrc=%+v\ndst=%+v", srcC, dstC)
	}
	rec, err := dst.Show("LIN-1")
	if err != nil {
		t.Fatalf("Show on dst: %v", err)
	}
	if rec.Title != "T" || len(rec.Labels) != 1 || len(rec.Comments) != 1 {
		t.Fatalf("round-trip lost detail: %+v", rec)
	}
}

func TestIngestStreamSingleSnapshot(t *testing.T) {
	s := mustOpen(t)
	snap := linear.Snapshot{
		Teams:  []linear.Team{{ID: "t1", Key: "LIN", Name: "L"}},
		States: []linear.WorkflowState{{ID: "s1", TeamID: "t1", Name: "B", Type: "backlog"}},
		Issues: []linear.Issue{{ID: "i1", Identifier: "LIN-1", Title: "T", TeamID: "t1", StateID: "s1", CreatedAt: "2026-05-18T00:00:00Z", UpdatedAt: "2026-05-18T00:00:00Z"}},
	}
	raw, _ := json.Marshal(snap)
	n, err := s.IngestStream(bytes.NewReader(raw), 0)
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("expected non-zero ingest count")
	}
}

func TestIngestStreamRejectsGarbage(t *testing.T) {
	s := mustOpen(t)
	if _, err := s.IngestStream(bytes.NewReader([]byte("garbage")), 0); err == nil {
		t.Fatal("expected error on non-JSON input")
	}
}

func TestPhraseQueryHandlesOperators(t *testing.T) {
	s := mustOpen(t)
	snap := linear.Snapshot{
		Teams:  []linear.Team{{ID: "t1", Key: "LIN", Name: "L"}},
		States: []linear.WorkflowState{{ID: "s1", TeamID: "t1", Name: "B", Type: "backlog"}},
		Issues: []linear.Issue{
			{ID: "i1", Identifier: "LIN-1", Title: "foo bar baz", TeamID: "t1", StateID: "s1", CreatedAt: "2026-05-18T00:00:00Z", UpdatedAt: "2026-05-18T00:00:00Z"},
		},
	}
	if err := s.IngestSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	// Hostile inputs that crash a raw FTS5 query but must work through PhraseQuery.
	hostile := []string{`foo:bar`, `foo-bar`, `foo "quoted" bar`, `(foo OR bar)`}
	for _, in := range hostile {
		results, err := s.Search(PhraseQuery(in), 10)
		if err != nil {
			t.Fatalf("PhraseQuery(%q) -> Search: %v", in, err)
		}
		_ = results
	}
}
