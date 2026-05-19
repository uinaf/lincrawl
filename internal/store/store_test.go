package store

import (
	"bytes"
	"encoding/json"
	"path/filepath"
	"strings"
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
				LabelIDs:  []string{"label-1"},
				UpdatedAt: "2026-05-18T10:00:00Z", CreatedAt: "2026-05-18T09:00:00Z",
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
		Teams:    []linear.Team{{ID: "t1", Key: "LIN", Name: "L"}},
		States:   []linear.WorkflowState{{ID: "s1", TeamID: "t1", Name: "B", Type: "backlog"}},
		Users:    []linear.User{{ID: "u1", Name: "Sam"}},
		Labels:   []linear.Label{{ID: "l1", TeamID: "t1", Name: "alpha"}},
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

func TestSaveAndLoadCursor(t *testing.T) {
	s := mustOpen(t)
	got, err := s.LoadCursor("issues.tail")
	if err != nil {
		t.Fatalf("LoadCursor empty: %v", err)
	}
	if got.Scope != "issues.tail" || got.Cursor != "" || got.HighWaterMark != "" {
		t.Fatalf("empty state: %+v", got)
	}
	if err := s.SaveCursor("issues.tail", "cursor-1", "2026-05-19T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	got, err = s.LoadCursor("issues.tail")
	if err != nil {
		t.Fatal(err)
	}
	if got.Cursor != "cursor-1" || got.HighWaterMark != "2026-05-19T00:00:00Z" {
		t.Fatalf("LoadCursor mismatch: %+v", got)
	}
	if got.UpdatedAt == "" {
		t.Fatalf("expected updated_at to be set")
	}
	if err := s.SaveCursor("issues.tail", "cursor-2", "2026-05-20T00:00:00Z"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.LoadCursor("issues.tail")
	if got.Cursor != "cursor-2" || got.HighWaterMark != "2026-05-20T00:00:00Z" {
		t.Fatalf("overwrite: %+v", got)
	}
	if err := s.SaveCursor("misc", "x", "y"); err != nil {
		t.Fatal(err)
	}
	got, _ = s.LoadCursor("misc")
	if got.Cursor != "x" || got.HighWaterMark != "y" {
		t.Fatalf("bare scope: %+v", got)
	}
}

func TestSplitScope(t *testing.T) {
	cases := []struct {
		in, wantType, wantID string
	}{
		{"issues.tail", "issues", "tail"},
		{"misc", "misc", "default"},
		{"a.b.c", "a", "b.c"},
		{"", "", "default"},
	}
	for _, tc := range cases {
		et, ei := splitScope(tc.in)
		if et != tc.wantType || ei != tc.wantID {
			t.Errorf("splitScope(%q) = (%q,%q), want (%q,%q)", tc.in, et, ei, tc.wantType, tc.wantID)
		}
	}
}

func TestOpenReadOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "lincrawl.db")
	w, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	w.Close()
	r, err := OpenReadOnly(path)
	if err != nil {
		t.Fatalf("OpenReadOnly: %v", err)
	}
	defer r.Close()
	if r.Path() != path {
		t.Fatalf("Path() = %q, want %q", r.Path(), path)
	}
	if r.DB() == nil {
		t.Fatal("DB() returned nil")
	}
	if _, err := r.Counts(); err != nil {
		t.Fatalf("Counts on RO: %v", err)
	}
}

func TestOpenRejectsEmptyPath(t *testing.T) {
	if _, err := Open(""); err == nil {
		t.Fatal("expected error on empty path")
	}
	if _, err := OpenReadOnly(""); err == nil {
		t.Fatal("expected error on empty path for OpenReadOnly")
	}
}

func TestStoreRawBlobDedupes(t *testing.T) {
	s := mustOpen(t)
	payload := []byte(`{"foo":"bar"}`)
	if err := s.StoreRawBlob("issue", "i1", payload); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreRawBlob("issue", "i1", payload); err != nil {
		t.Fatal(err)
	}
	if err := s.StoreRawBlob("issue", "i1", nil); err != nil {
		t.Fatal(err)
	}
	var n int
	if err := s.DB().QueryRow(`SELECT COUNT(*) FROM raw_blobs`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("raw_blobs count = %d, want 1", n)
	}
}

func TestSnapshotMaterializesFullGraph(t *testing.T) {
	s := mustOpen(t)
	src := linear.Snapshot{
		Teams:    []linear.Team{{ID: "t1", Key: "LIN", Name: "Lincrawl", UpdatedAt: "2026-05-19T00:00:00Z"}},
		States:   []linear.WorkflowState{{ID: "s1", TeamID: "t1", Name: "B", Type: "backlog"}},
		Users:    []linear.User{{ID: "u1", Name: "Sam"}},
		Labels:   []linear.Label{{ID: "l1", TeamID: "t1", Name: "ingest"}},
		Projects: []linear.Project{{ID: "p1", Name: "MVP", State: "started", UpdatedAt: "2026-05-19T00:00:00Z"}},
		Issues: []linear.Issue{
			{
				ID: "i1", Identifier: "LIN-1", Title: "T", TeamID: "t1", StateID: "s1",
				ProjectID: "p1", AssigneeID: "u1", LabelIDs: []string{"l1"}, Priority: 0,
				CreatedAt: "2026-05-19T00:00:00Z", UpdatedAt: "2026-05-19T00:00:01Z",
				Comments: []linear.Comment{
					{
						ID: "c1", IssueID: "i1", AuthorID: "u1", Body: "hi",
						CreatedAt: "2026-05-19T00:00:02Z", UpdatedAt: "2026-05-19T00:00:02Z",
					},
				},
			},
		},
	}
	if err := s.IngestSnapshot(src); err != nil {
		t.Fatal(err)
	}
	got, err := s.Snapshot()
	if err != nil {
		t.Fatalf("Snapshot: %v", err)
	}
	if len(got.Teams) != 1 || len(got.States) != 1 || len(got.Users) != 1 ||
		len(got.Labels) != 1 || len(got.Projects) != 1 || len(got.Issues) != 1 {
		t.Fatalf("counts: %+v", got)
	}
	if got.Issues[0].Identifier != "LIN-1" {
		t.Fatalf("identifier: %q", got.Issues[0].Identifier)
	}
	if len(got.Issues[0].LabelIDs) != 1 || got.Issues[0].LabelIDs[0] != "l1" {
		t.Fatalf("labels lost: %+v", got.Issues[0].LabelIDs)
	}
	if len(got.Issues[0].Comments) != 1 || got.Issues[0].Comments[0].ID != "c1" {
		t.Fatalf("comments lost: %+v", got.Issues[0].Comments)
	}
}

func TestIngestSnapshotIdempotentSecondPassSkips(t *testing.T) {
	s := mustOpen(t)
	snap := linear.Snapshot{
		Teams:  []linear.Team{{ID: "t1", Key: "LIN", Name: "L"}},
		States: []linear.WorkflowState{{ID: "s1", TeamID: "t1", Name: "B", Type: "backlog"}},
		Issues: []linear.Issue{
			{
				ID: "i1", Identifier: "LIN-1", Title: "Hello", Description: "World",
				TeamID: "t1", StateID: "s1", Priority: 2,
				CreatedAt: "2026-05-18T00:00:00Z", UpdatedAt: "2026-05-18T00:00:00Z",
			},
		},
	}
	if err := s.IngestSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	// Second pass with identical content should hit the hash-skip branch.
	if err := s.IngestSnapshot(snap); err != nil {
		t.Fatal(err)
	}
	rec, err := s.Show("LIN-1")
	if err != nil {
		t.Fatal(err)
	}
	if rec.Title != "Hello" {
		t.Fatal("title lost")
	}
}

func TestIngestSnapshotStubsMissingRefs(t *testing.T) {
	s := mustOpen(t)
	// Reference a team/state/project that does not exist in the snapshot
	// to exercise stubMissingRefs.
	snap := linear.Snapshot{
		Issues: []linear.Issue{
			{
				ID: "i1", Identifier: "LIN-1", Title: "x", TeamID: "ghost-t", ProjectID: "ghost-p", StateID: "ghost-s",
				CreatedAt: "2026-05-18T00:00:00Z", UpdatedAt: "2026-05-18T00:00:00Z",
			},
		},
	}
	if err := s.IngestSnapshot(snap); err != nil {
		t.Fatal(err)
	}
}

func TestSnapshotEmptyDB(t *testing.T) {
	s := mustOpen(t)
	snap, err := s.Snapshot()
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Teams) != 0 || len(snap.Issues) != 0 {
		t.Fatalf("expected empty: %+v", snap)
	}
}

func TestExportNDJSONEmptyDB(t *testing.T) {
	s := mustOpen(t)
	var buf bytes.Buffer
	n, err := s.ExportNDJSON(&buf)
	if err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatalf("empty export count = %d", n)
	}
}

func TestIngestStreamRejectsEmpty(t *testing.T) {
	s := mustOpen(t)
	if _, err := s.IngestStream(bytes.NewReader(nil), 0); err == nil {
		t.Fatal("expected empty-input error")
	}
}

func TestIngestStreamRejectsNonObject(t *testing.T) {
	s := mustOpen(t)
	if _, err := s.IngestStream(bytes.NewReader([]byte(`[1,2,3]`)), 0); err == nil {
		t.Fatal("expected error on non-object top-level")
	}
}

func TestIngestStreamRejectsUnknownKind(t *testing.T) {
	s := mustOpen(t)
	envelope := `{"kind":"alien","item":{}}`
	if _, err := s.IngestStream(bytes.NewReader([]byte(envelope)), 0); err == nil {
		t.Fatal("expected unknown-kind error")
	}
}

func TestIngestStreamMultipleEnvelopes(t *testing.T) {
	s := mustOpen(t)
	stream := `{"kind":"team","item":{"id":"t1","key":"LIN","name":"L"}}
{"kind":"state","item":{"id":"s1","team_id":"t1","name":"B","type":"backlog"}}
{"kind":"user","item":{"id":"u1","name":"X","email":""}}
{"kind":"label","item":{"id":"l1","team_id":"t1","name":"a"}}
{"kind":"project","item":{"id":"p1","name":"P","state":"active","updated_at":""}}
{"kind":"issue","item":{"id":"i1","identifier":"LIN-1","title":"T","description":"","priority":0,"team_id":"t1","state_id":"s1","createdAt":"2026-05-19T00:00:00Z","updatedAt":"2026-05-19T00:00:00Z"}}`
	if _, err := s.IngestStream(bytes.NewReader([]byte(stream)), 0); err != nil {
		t.Fatal(err)
	}
	counts, _ := s.Counts()
	if counts.Teams != 1 || counts.Issues != 1 || counts.Labels != 1 {
		t.Fatalf("counts: %+v", counts)
	}
}

func TestIngestStreamMalformedItem(t *testing.T) {
	s := mustOpen(t)
	// Properly-typed envelope but item is the wrong shape.
	stream := `{"kind":"team","item":"not-an-object"}`
	if _, err := s.IngestStream(bytes.NewReader([]byte(stream)), 0); err == nil {
		t.Fatal("expected unmarshal error on malformed item")
	}
}

func TestSafeSnippetCovers(t *testing.T) {
	if SafeSnippet("", 200) != "" {
		t.Error("empty snippet should be empty")
	}
	long := strings.Repeat("x", 5_000)
	got := SafeSnippet(long, 200)
	if len(got) >= len(long) {
		t.Errorf("expected truncation, got len=%d", len(got))
	}
	if !strings.Contains(SafeSnippet("hi <mark>there</mark> friend", 200), "there") {
		t.Error("expected mark tags to be stripped while preserving text")
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
