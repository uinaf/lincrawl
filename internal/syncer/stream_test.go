package syncer

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/uinaf/lincrawl/internal/linear"
	"github.com/uinaf/lincrawl/internal/store"
)

func mustStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(filepath.Join(t.TempDir(), "lincrawl.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestStreamIssuesCallbackErrorHaltsCursor(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := `{"data":{"issues":{
		  "pageInfo":{"endCursor":"page1","hasNextPage":true},
		  "nodes":[
		    {"id":"i1","identifier":"LIN-1","title":"one","description":"","priority":0,
		     "createdAt":"2026-05-18T00:00:00Z","updatedAt":"2026-05-18T01:00:00Z",
		     "team":{"id":"t1"},"project":null,"state":{"id":"s1"},"assignee":null,"creator":null,
		     "labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},
		     "comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}
		  ]
		}}}`
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	s := mustStore(t)
	client := linear.NewClient(srv.URL, "lin_api_test")

	pulled, err := StreamIssuesUpdatedSince(context.Background(), s, client,
		time.Now().Add(-24*time.Hour), 10, 0, func(iss linear.Issue) error {
			return errors.New("simulated stdout pipe closed")
		})
	if err == nil {
		t.Fatal("expected error from onIssue callback")
	}
	if pulled != 0 {
		t.Fatalf("pulled = %d, want 0 (issue did not reach consumer)", pulled)
	}
	cur, err := s.LoadCursor("issues.tail")
	if err != nil {
		t.Fatal(err)
	}
	if cur.HighWaterMark != "" || cur.Cursor != "" {
		t.Fatalf("cursor must not advance when consumer failed: %+v", cur)
	}
}

func TestStreamIssuesAdvancesCursorOnSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		body := `{"data":{"issues":{
		  "pageInfo":{"endCursor":"page1","hasNextPage":false},
		  "nodes":[
		    {"id":"i1","identifier":"LIN-1","title":"one","description":"","priority":0,
		     "createdAt":"2026-05-18T00:00:00Z","updatedAt":"2026-05-18T01:00:00Z",
		     "team":{"id":"t1"},"project":null,"state":{"id":"s1"},"assignee":null,"creator":null,
		     "labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},
		     "comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}
		  ]
		}}}`
		_, _ = w.Write([]byte(body))
	}))
	defer srv.Close()

	s := mustStore(t)
	client := linear.NewClient(srv.URL, "lin_api_test")

	got := []string{}
	pulled, err := StreamIssuesUpdatedSince(context.Background(), s, client,
		time.Now().Add(-24*time.Hour), 10, 0, func(iss linear.Issue) error {
			got = append(got, iss.Identifier)
			return nil
		})
	if err != nil {
		t.Fatal(err)
	}
	if pulled != 1 || len(got) != 1 || got[0] != "LIN-1" {
		t.Fatalf("pulled=%d got=%v", pulled, got)
	}
	cur, err := s.LoadCursor("issues.tail")
	if err != nil {
		t.Fatal(err)
	}
	if cur.HighWaterMark != "2026-05-18T01:00:00Z" {
		t.Fatalf("high_water_mark = %q", cur.HighWaterMark)
	}
}

func TestPaginationStallGuard(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"issues":{
		  "pageInfo":{"endCursor":"same","hasNextPage":true},
		  "nodes":[]
		}}}`))
	}))
	defer srv.Close()

	s := mustStore(t)
	client := linear.NewClient(srv.URL, "lin_api_test")

	_, err := IngestIssuesUpdatedSince(context.Background(), s, client,
		time.Now().Add(-24*time.Hour), 10, 0)
	if err == nil {
		t.Fatal("expected stall error")
	}
	if !strings.Contains(err.Error(), "stalled") {
		t.Fatalf("error should mention stall, got %v", err)
	}
	if hits > 3 {
		t.Fatalf("too many hits before stall detected: %d", hits)
	}
}

// keep json import used
var _ = json.Marshal
