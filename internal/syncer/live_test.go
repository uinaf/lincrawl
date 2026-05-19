package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/uinaf/lincrawl/internal/linear"
	"github.com/uinaf/lincrawl/internal/store"
)

func mustOpenStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "lincrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func newFastClient(srv *httptest.Server) *linear.Client {
	c := linear.NewClient(srv.URL, "lin_api_test")
	c.RetryBackoff = time.Millisecond
	c.MaxAttempts = 1
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func TestIngestEntities(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Query string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch {
		case strings.Contains(req.Query, "{ viewer"):
			_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"u1","name":"Sam","email":"sam@example.test"}}}`))
		case strings.Contains(req.Query, "teams("):
			_, _ = w.Write([]byte(`{"data":{"teams":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[{"id":"t1","key":"LIN","name":"L","updatedAt":""}]}}}`))
		case strings.Contains(req.Query, "workflowStates("):
			_, _ = w.Write([]byte(`{"data":{"workflowStates":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[{"id":"s1","team":{"id":"t1"},"name":"B","type":"backlog"}]}}}`))
		case strings.Contains(req.Query, "users("):
			_, _ = w.Write([]byte(`{"data":{"users":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[{"id":"u1","name":"Sam","email":""}]}}}`))
		case strings.Contains(req.Query, "issueLabels("):
			_, _ = w.Write([]byte(`{"data":{"issueLabels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[{"id":"l1","name":"x","team":{"id":"t1"}}]}}}`))
		case strings.Contains(req.Query, "projects("):
			_, _ = w.Write([]byte(`{"data":{"projects":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[{"id":"p1","name":"M","state":"started","updatedAt":""}]}}}`))
		default:
			t.Fatalf("unexpected query: %s", req.Query)
		}
	}))
	defer srv.Close()
	s := mustOpenStore(t)
	c := newFastClient(srv)
	res, err := IngestEntities(context.Background(), s, c)
	if err != nil {
		t.Fatal(err)
	}
	if res.Viewer.ID != "u1" {
		t.Fatalf("viewer = %+v", res.Viewer)
	}
	if res.Counts.Teams != 1 || res.Counts.States != 1 || res.Counts.Users != 1 {
		t.Fatalf("counts: %+v", res.Counts)
	}
}

func TestIngestEntitiesNilGuards(t *testing.T) {
	if _, err := IngestEntities(context.Background(), nil, nil); err == nil {
		t.Fatal("expected guard")
	}
}

func TestIngestIssueByIdentifier(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"issue":{
			"id":"i1","identifier":"LIN-7","title":"Hydrate test","description":"",
			"priority":2,"createdAt":"2026-05-19T00:00:00Z","updatedAt":"2026-05-19T00:00:01Z",
			"team":{"id":"t1"},"project":null,"state":{"id":"s1"},"assignee":null,"creator":null,
			"labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},
			"comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}
		}}}`))
	}))
	defer srv.Close()
	s := mustOpenStore(t)
	c := newFastClient(srv)
	res, err := IngestIssueByIdentifier(context.Background(), s, c, "LIN-7")
	if err != nil {
		t.Fatal(err)
	}
	if res.Issue.Identifier != "LIN-7" {
		t.Fatalf("identifier = %q", res.Issue.Identifier)
	}
	if res.Counts.Issues != 1 {
		t.Fatalf("counts: %+v", res.Counts)
	}
}

func TestIngestIssueByIdentifierNilGuards(t *testing.T) {
	if _, err := IngestIssueByIdentifier(context.Background(), nil, nil, "LIN-1"); err == nil {
		t.Fatal("expected guard")
	}
}

func TestIngestIssuesUpdatedSinceMaxIssuesSlicesPage(t *testing.T) {
	var pages int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&pages, 1)
		_, _ = w.Write([]byte(`{"data":{"issues":{
			"pageInfo":{"endCursor":"cur1","hasNextPage":true},
			"nodes":[
			  {"id":"i1","identifier":"LIN-1","title":"T","description":"","priority":0,"createdAt":"","updatedAt":"2026-05-19T00:00:01Z",
			   "team":null,"project":null,"state":null,"assignee":null,"creator":null,
			   "labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},
			   "comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}},
			  {"id":"i2","identifier":"LIN-2","title":"T2","description":"","priority":0,"createdAt":"","updatedAt":"2026-05-19T00:00:02Z",
			   "team":null,"project":null,"state":null,"assignee":null,"creator":null,
			   "labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},
			   "comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}
			]
		}}}`))
	}))
	defer srv.Close()
	s := mustOpenStore(t)
	c := newFastClient(srv)
	res, err := IngestIssuesUpdatedSince(context.Background(), s, c, time.Now().Add(-time.Hour), 5, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.IssuesPulled != 1 {
		t.Fatalf("issues_pulled = %d, want 1 (max-issues slice)", res.IssuesPulled)
	}
	if atomic.LoadInt32(&pages) != 1 {
		t.Fatalf("pages = %d, want exactly 1 (truncated)", pages)
	}
}

func TestIngestIssuesUpdatedSinceNilGuards(t *testing.T) {
	if _, err := IngestIssuesUpdatedSince(context.Background(), nil, nil, time.Now(), 1, 1); err == nil {
		t.Fatal("expected guard")
	}
}

func TestIngestFixtureNilStoreReturnsError(t *testing.T) {
	if _, err := IngestFixture(nil, ""); err == nil {
		t.Fatal("expected guard")
	}
}

func TestIngestEntitiesPropagatesIngestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Query string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch {
		case strings.Contains(req.Query, "{ viewer"):
			_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"u1","name":"S"}}}`))
		default:
			_, _ = w.Write([]byte(`{"data":{"teams":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},"workflowStates":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},"users":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},"issueLabels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},"projects":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}}`))
		}
	}))
	defer srv.Close()
	s := mustOpenStore(t)
	// Close the inner DB so IngestSnapshot/Counts fail.
	_ = s.DB().Close()
	c := newFastClient(srv)
	if _, err := IngestEntities(context.Background(), s, c); err == nil {
		t.Fatal("expected ingest error after DB close")
	}
}

func TestStreamIssuesUpdatedSinceIngestError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"issues":{
			"pageInfo":{"endCursor":"","hasNextPage":false},
			"nodes":[
			  {"id":"i1","identifier":"LIN-1","title":"A","description":"","priority":1,"createdAt":"","updatedAt":"2026-05-19T00:00:00Z",
			   "team":null,"project":null,"state":null,"assignee":null,"creator":null,
			   "labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},
			   "comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}
			]
		}}}`))
	}))
	defer srv.Close()
	s := mustOpenStore(t)
	_ = s.DB().Close()
	c := newFastClient(srv)
	if _, err := StreamIssuesUpdatedSince(context.Background(), s, c, time.Now().Add(-time.Hour), 50, 0, func(linear.Issue) error { return nil }); err == nil {
		t.Fatal("expected ingest error after DB close")
	}
}

func TestIngestFixtureRejectsBadDir(t *testing.T) {
	s := mustOpenStore(t)
	if _, err := IngestFixture(s, "/nonexistent/fixture/path/xyz"); err == nil {
		t.Fatal("expected error")
	}
}

func TestIngestEntitiesNilClient(t *testing.T) {
	s := mustOpenStore(t)
	if _, err := IngestEntities(context.Background(), s, nil); err == nil {
		t.Fatal("expected guard")
	}
}

func TestIngestEntitiesViewerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no", http.StatusUnauthorized)
	}))
	defer srv.Close()
	s := mustOpenStore(t)
	c := newFastClient(srv)
	if _, err := IngestEntities(context.Background(), s, c); err == nil {
		t.Fatal("expected viewer error")
	}
}

func TestIngestIssueByIdentifierClientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fake error", http.StatusInternalServerError)
	}))
	defer srv.Close()
	s := mustOpenStore(t)
	c := linear.NewClient(srv.URL, "lin_api_test")
	c.RetryBackoff = time.Millisecond
	c.MaxAttempts = 1
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	if _, err := IngestIssueByIdentifier(context.Background(), s, c, "LIN-1"); err == nil {
		t.Fatal("expected client error")
	}
}

func TestIngestIssuesUpdatedSinceClientError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "fake", http.StatusInternalServerError)
	}))
	defer srv.Close()
	s := mustOpenStore(t)
	c := linear.NewClient(srv.URL, "lin_api_test")
	c.RetryBackoff = time.Millisecond
	c.MaxAttempts = 1
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	if _, err := IngestIssuesUpdatedSince(context.Background(), s, c, time.Now().Add(-time.Hour), 50, 0); err == nil {
		t.Fatal("expected client error")
	}
}

func TestIngestIssuesUpdatedSinceEmptyPageStallAborts(t *testing.T) {
	// Server keeps reporting hasNextPage=true with zero issues to trigger
	// the maxZeroItemRuns guard. Advance the cursor each call so the stall
	// detector doesn't fire first.
	calls := int32(0)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		body := fmt.Sprintf(`{"data":{"issues":{
			"pageInfo":{"endCursor":"c%d","hasNextPage":true},
			"nodes":[]
		}}}`, n)
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()
	s := mustOpenStore(t)
	c := newFastClient(srv)
	if _, err := IngestIssuesUpdatedSince(context.Background(), s, c, time.Now().Add(-time.Hour), 50, 0); err == nil {
		t.Fatal("expected zero-page-stall guard error")
	}
}

func TestIngestIssuesUpdatedSinceMaxIssuesStopsEarly(t *testing.T) {
	// Page returns 2 issues per call; with maxIssues=2 the run should stop
	// after slicing the first page.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"issues":{
			"pageInfo":{"endCursor":"","hasNextPage":false},
			"nodes":[
			  {"id":"i1","identifier":"LIN-1","title":"A","description":"","priority":1,"createdAt":"","updatedAt":"2026-05-19T00:00:01Z",
			   "team":null,"project":null,"state":null,"assignee":null,"creator":null,
			   "labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},
			   "comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}},
			  {"id":"i2","identifier":"LIN-2","title":"B","description":"","priority":1,"createdAt":"","updatedAt":"2026-05-19T00:00:02Z",
			   "team":null,"project":null,"state":null,"assignee":null,"creator":null,
			   "labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},
			   "comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}
			]
		}}}`))
	}))
	defer srv.Close()
	s := mustOpenStore(t)
	c := newFastClient(srv)
	res, err := IngestIssuesUpdatedSince(context.Background(), s, c, time.Now().Add(-time.Hour), 50, 1)
	if err != nil {
		t.Fatal(err)
	}
	if res.IssuesPulled != 1 {
		t.Fatalf("expected exactly 1 issue, got %d", res.IssuesPulled)
	}
}

func TestStreamIssuesUpdatedSinceCallsOnIssue(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"issues":{
			"pageInfo":{"endCursor":"","hasNextPage":false},
			"nodes":[
			  {"id":"i1","identifier":"LIN-1","title":"A","description":"","priority":1,"createdAt":"","updatedAt":"2026-05-19T00:00:00Z",
			   "team":null,"project":null,"state":null,"assignee":null,"creator":null,
			   "labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},
			   "comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}
			]
		}}}`))
	}))
	defer srv.Close()
	s := mustOpenStore(t)
	c := newFastClient(srv)
	seen := 0
	n, err := StreamIssuesUpdatedSince(context.Background(), s, c, time.Now().Add(-time.Hour), 50, 0, func(linear.Issue) error {
		seen++
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 || seen != 1 {
		t.Fatalf("n=%d seen=%d", n, seen)
	}
}

func TestStreamIssuesUpdatedSinceOnIssueErrorAborts(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"issues":{
			"pageInfo":{"endCursor":"","hasNextPage":false},
			"nodes":[
			  {"id":"i1","identifier":"LIN-1","title":"A","description":"","priority":1,"createdAt":"","updatedAt":"2026-05-19T00:00:00Z",
			   "team":null,"project":null,"state":null,"assignee":null,"creator":null,
			   "labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},
			   "comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}
			]
		}}}`))
	}))
	defer srv.Close()
	s := mustOpenStore(t)
	c := newFastClient(srv)
	wantErr := errors.New("kaboom")
	_, err := StreamIssuesUpdatedSince(context.Background(), s, c, time.Now().Add(-time.Hour), 50, 0, func(linear.Issue) error {
		return wantErr
	})
	if err == nil || !errors.Is(err, wantErr) {
		t.Fatalf("err=%v", err)
	}
}

func TestStreamIssuesUpdatedSinceNilGuards(t *testing.T) {
	if _, err := StreamIssuesUpdatedSince(context.Background(), nil, nil, time.Now(), 1, 1, nil); err == nil {
		t.Fatal("expected guard")
	}
}

// keep linear import alive
var _ = linear.Snapshot{}
