package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func newTestClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	c := NewClient(srv.URL, "lin_api_test_token_xyz")
	// Make retries snappy + deterministic.
	c.RetryBackoff = time.Millisecond
	c.MaxAttempts = 3
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	return c
}

func TestNewClientDefaultsAndAuth(t *testing.T) {
	c := NewClient("", "lin_api_personal")
	if c.endpoint != "https://api.linear.app/graphql" {
		t.Errorf("default endpoint = %q", c.endpoint)
	}
	if c.authPrefix != "" {
		t.Errorf("personal API key should have empty prefix, got %q", c.authPrefix)
	}
	c2 := NewClient("https://example.test/graphql", "oauth_token_no_prefix")
	if c2.endpoint != "https://example.test/graphql" {
		t.Errorf("explicit endpoint = %q", c2.endpoint)
	}
	if c2.authPrefix != "Bearer " {
		t.Errorf("oauth should use Bearer, got %q", c2.authPrefix)
	}
	if c.Endpoint() != "https://api.linear.app/graphql" {
		t.Errorf("Endpoint() accessor mismatch")
	}
}

func TestSetHTTPClient(t *testing.T) {
	c := NewClient("", "lin_api_x")
	custom := &http.Client{Timeout: time.Second}
	c.SetHTTPClient(custom)
	if c.httpClient != custom {
		t.Fatal("SetHTTPClient did not override")
	}
}

func TestContextSleep(t *testing.T) {
	if err := contextSleep(context.Background(), 0); err != nil {
		t.Fatalf("zero duration: %v", err)
	}
	if err := contextSleep(context.Background(), -time.Hour); err != nil {
		t.Fatalf("negative duration: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := contextSleep(ctx, time.Minute); err == nil {
		t.Fatal("expected ctx err on cancelled context")
	}
	if err := contextSleep(context.Background(), time.Millisecond); err != nil {
		t.Fatalf("real sleep: %v", err)
	}
}

func TestParseRetryAfter(t *testing.T) {
	cases := []struct {
		in   string
		want time.Duration
	}{
		{"", 0},
		{"   ", 0},
		{"15", 15 * time.Second},
		{"invalid", 0},
	}
	for _, tc := range cases {
		if got := parseRetryAfter(tc.in); got != tc.want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", tc.in, got, tc.want)
		}
	}
	// HTTP-date format (1 sec in the future) → positive duration.
	future := time.Now().Add(30 * time.Second).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(future); got <= 0 || got > 30*time.Second+5*time.Second {
		t.Errorf("parseRetryAfter(future http date) = %v", got)
	}
	past := time.Now().Add(-time.Minute).UTC().Format(http.TimeFormat)
	if got := parseRetryAfter(past); got != 0 {
		t.Errorf("parseRetryAfter(past http date) = %v, want 0", got)
	}
}

func TestFirstMessage(t *testing.T) {
	if got := firstMessage(nil); got != "" {
		t.Errorf("nil: %q", got)
	}
	if got := firstMessage([]string{"a", "b"}); got != "a" {
		t.Errorf("first: %q", got)
	}
}

func TestPeelErrorMessages(t *testing.T) {
	got := peelErrorMessages([]byte(`{"errors":[{"message":"boom"},{"message":"crash"}]}`))
	if len(got) != 2 || got[0] != "boom" {
		t.Errorf("structured: %v", got)
	}
	got = peelErrorMessages([]byte("plain body"))
	if len(got) != 1 || got[0] != "plain body" {
		t.Errorf("plain: %v", got)
	}
	// Empty stays empty.
	if got := peelErrorMessages([]byte("   ")); got != nil {
		t.Errorf("whitespace: %v", got)
	}
	// Large body is trimmed.
	big := bytes.Repeat([]byte("x"), 1000)
	got = peelErrorMessages(big)
	if len(got) != 1 || len(got[0]) > 250 {
		t.Errorf("trim: len(got)=%d, len(msg)=%d", len(got), len(got[0]))
	}
}

func TestIsRetryable(t *testing.T) {
	if !isRetryable(&RateLimitError{}) {
		t.Error("rate limit should retry")
	}
	if !isRetryable(&APIError{Status: 502, Messages: []string{"bad gateway"}}) {
		t.Error("5xx APIError should retry")
	}
	if isRetryable(&APIError{Status: 400, Messages: []string{"bad request"}}) {
		t.Error("4xx APIError should NOT retry")
	}
	if !isRetryable(fmt.Errorf("linear: transport: connection refused")) {
		t.Error("transport-wrapped err should retry")
	}
	if isRetryable(fmt.Errorf("something else")) {
		t.Error("non-classified err should not retry")
	}
}

func TestErrorTypesError(t *testing.T) {
	r := &RateLimitError{Status: 429, RetryAfter: 2 * time.Second, Message: "slow down"}
	if !strings.Contains(r.Error(), "rate limited") {
		t.Errorf("rate limit err: %s", r.Error())
	}
	a := &AuthError{Status: 401, Messages: []string{"bad token"}}
	if !strings.Contains(a.Error(), "auth") || !strings.Contains(a.Error(), "bad token") {
		t.Errorf("auth err: %s", a.Error())
	}
	n := &NotFoundError{Resource: "issue", Messages: []string{"nope"}}
	if !strings.Contains(n.Error(), "issue not found") {
		t.Errorf("not-found err: %s", n.Error())
	}
	api := &APIError{Status: 500}
	if !strings.Contains(api.Error(), "HTTP 500") {
		t.Errorf("api status: %s", api.Error())
	}
	api2 := &APIError{Status: 500, Messages: []string{"oops"}}
	if !strings.Contains(api2.Error(), "oops") {
		t.Errorf("api with msg: %s", api2.Error())
	}
}

func TestViewer(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("method = %s", r.Method)
		}
		if r.Header.Get("Authorization") != "lin_api_test_token_xyz" {
			t.Errorf("auth header = %q", r.Header.Get("Authorization"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("content-type = %q", r.Header.Get("Content-Type"))
		}
		_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"u1","name":"Sam","email":"sam@example.test"}}}`))
	})
	got, err := c.Viewer(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got.ID != "u1" || got.Name != "Sam" || got.Email != "sam@example.test" {
		t.Errorf("viewer = %+v", got)
	}
}

func TestQueryRaw(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Query     string         `json:"query"`
			Variables map[string]any `json:"variables"`
		}
		_ = json.NewDecoder(r.Body).Decode(&req)
		if !strings.Contains(req.Query, "viewer") {
			t.Errorf("query = %q", req.Query)
		}
		_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"u1"}}}`))
	})
	raw, err := c.Query(context.Background(), "query { viewer { id } }", map[string]any{"x": 1})
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("u1")) {
		t.Errorf("raw = %s", raw)
	}
}

func TestDoRetriesOn429ThenSucceeds(t *testing.T) {
	var hits int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"errors":[{"message":"slow"}]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":{"viewer":{"id":"u1"}}}`))
	})
	if _, err := c.Viewer(context.Background()); err != nil {
		t.Fatalf("expected eventual success, got %v", err)
	}
	if atomic.LoadInt32(&hits) < 2 {
		t.Errorf("expected at least 2 hits, got %d", hits)
	}
}

func TestDoRetriesOn5xxThenGivesUp(t *testing.T) {
	var hits int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"errors":[{"message":"oops"}]}`))
	})
	if _, err := c.Viewer(context.Background()); err == nil {
		t.Fatal("expected error after exhausted retries")
	}
	if got := atomic.LoadInt32(&hits); got != int32(c.MaxAttempts) {
		t.Errorf("hits = %d, want %d", got, c.MaxAttempts)
	}
}

func TestDoDoesNotRetryOn401(t *testing.T) {
	var hits int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"errors":[{"message":"bad token"}]}`))
	})
	_, err := c.Viewer(context.Background())
	if err == nil {
		t.Fatal("expected auth error")
	}
	if _, ok := err.(*AuthError); !ok {
		t.Errorf("err type = %T", err)
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Errorf("auth should not retry, hits=%d", hits)
	}
}

func TestDoDoesNotRetryOn404(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"errors":[{"message":"gone"}]}`))
	})
	_, err := c.Viewer(context.Background())
	if _, ok := err.(*NotFoundError); !ok {
		t.Errorf("err type = %T", err)
	}
}

func TestDoSurfacesGraphQLErrors(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"errors":[{"message":"invalid"}]}`))
	})
	_, err := c.Viewer(context.Background())
	ae, ok := err.(*APIError)
	if !ok {
		t.Fatalf("err type = %T", err)
	}
	if len(ae.Messages) != 1 || ae.Messages[0] != "invalid" {
		t.Errorf("messages = %v", ae.Messages)
	}
}

func TestDoRejectsMalformedEnvelope(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not-json`))
	})
	if _, err := c.Viewer(context.Background()); err == nil {
		t.Fatal("expected decode error")
	}
}

func TestDoTransportErrorRetries(t *testing.T) {
	// Pointing at an unroutable endpoint forces transport-layer errors.
	c := NewClient("http://127.0.0.1:1/graphql", "lin_api_x")
	c.RetryBackoff = time.Millisecond
	c.MaxAttempts = 2
	c.Sleep = func(context.Context, time.Duration) error { return nil }
	c.httpClient.Timeout = 50 * time.Millisecond
	if _, err := c.Viewer(context.Background()); err == nil {
		t.Fatal("expected transport error")
	}
}

func TestFetchEntitiesPaginates(t *testing.T) {
	var teamPage, statePage, userPage, labelPage, projectPage int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Query string }
		_ = json.NewDecoder(r.Body).Decode(&req)
		switch {
		case strings.Contains(req.Query, "teams("):
			n := atomic.AddInt32(&teamPage, 1)
			if n == 1 {
				fmt.Fprintf(w, `{"data":{"teams":{"pageInfo":{"endCursor":"t-1","hasNextPage":true},"nodes":[{"id":"t1","key":"LIN","name":"L","updatedAt":""}]}}}`)
			} else {
				fmt.Fprintf(w, `{"data":{"teams":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[{"id":"t2","key":"OPS","name":"Ops","updatedAt":""}]}}}`)
			}
		case strings.Contains(req.Query, "workflowStates("):
			atomic.AddInt32(&statePage, 1)
			fmt.Fprintf(w, `{"data":{"workflowStates":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[{"id":"s1","team":{"id":"t1"},"name":"B","type":"backlog"}]}}}`)
		case strings.Contains(req.Query, "users("):
			atomic.AddInt32(&userPage, 1)
			fmt.Fprintf(w, `{"data":{"users":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[{"id":"u1","name":"Sam","email":""}]}}}`)
		case strings.Contains(req.Query, "issueLabels("):
			atomic.AddInt32(&labelPage, 1)
			fmt.Fprintf(w, `{"data":{"issueLabels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[{"id":"l1","name":"x","team":{"id":"t1"}}]}}}`)
		case strings.Contains(req.Query, "projects("):
			atomic.AddInt32(&projectPage, 1)
			fmt.Fprintf(w, `{"data":{"projects":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[{"id":"p1","name":"M","state":"started","updatedAt":""}]}}}`)
		default:
			t.Fatalf("unexpected query: %s", req.Query)
		}
	})
	snap, err := c.FetchEntities(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(snap.Teams) != 2 || len(snap.States) != 1 || len(snap.Users) != 1 || len(snap.Labels) != 1 || len(snap.Projects) != 1 {
		t.Errorf("snap shape: %+v", snap)
	}
	if atomic.LoadInt32(&teamPage) != 2 {
		t.Errorf("teams pages = %d, want 2", teamPage)
	}
}

func TestPaginateStallGuard(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintf(w, `{"data":{"teams":{"pageInfo":{"endCursor":"same","hasNextPage":true},"nodes":[]}}}`)
	})
	_, err := c.FetchEntities(context.Background())
	if err == nil {
		t.Fatal("expected stall error")
	}
	if !strings.Contains(err.Error(), "stalled") {
		t.Errorf("err = %v", err)
	}
}

func TestFetchIssuesUpdatedSinceNoNextedPagination(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"issues":{
		  "pageInfo":{"endCursor":"end","hasNextPage":false},
		  "nodes":[
		    {"id":"i1","identifier":"LIN-1","title":"T","description":"",
		     "priority":0,"createdAt":"2026-05-19T00:00:00Z","updatedAt":"2026-05-19T00:00:01Z",
		     "team":{"id":"t1"},"project":null,"state":{"id":"s1"},"assignee":null,"creator":null,
		     "labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[{"id":"l1"}]},
		     "comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[
		       {"id":"c1","user":{"id":"u1"},"body":"hi","createdAt":"2026-05-19T00:00:00Z","updatedAt":"2026-05-19T00:00:00Z"}
		     ]}}
		  ]
		}}}`))
	})
	page, err := c.FetchIssuesUpdatedSince(context.Background(), time.Now().Add(-time.Hour), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Issues) != 1 || page.Issues[0].Identifier != "LIN-1" {
		t.Errorf("page: %+v", page)
	}
	if len(page.Issues[0].LabelIDs) != 1 || len(page.Issues[0].Comments) != 1 {
		t.Errorf("nested: %+v", page.Issues[0])
	}
	if page.Issues[0].Priority != 0 {
		t.Errorf("priority must round-trip 0, got %d", page.Issues[0].Priority)
	}
}

func TestFetchIssuesUpdatedSinceDrainsNestedLabelsAndComments(t *testing.T) {
	var labelPage, commentPage, issuePage int32
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct{ Query string }
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		switch {
		case strings.Contains(req.Query, "labels(first: $first"):
			atomic.AddInt32(&labelPage, 1)
			_, _ = w.Write([]byte(`{"data":{"issue":{"labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[{"id":"l2"}]}}}}`))
		case strings.Contains(req.Query, "comments(first: $first"):
			atomic.AddInt32(&commentPage, 1)
			_, _ = w.Write([]byte(`{"data":{"issue":{"comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[
				{"id":"c2","user":{"id":"u2"},"body":"more","createdAt":"2026-05-19T00:01:00Z","updatedAt":"2026-05-19T00:01:00Z"}
			]}}}}`))
		default:
			atomic.AddInt32(&issuePage, 1)
			_, _ = w.Write([]byte(`{"data":{"issues":{
				"pageInfo":{"endCursor":"","hasNextPage":false},
				"nodes":[
					{"id":"i1","identifier":"LIN-1","title":"T","description":"",
					 "priority":3,"createdAt":"","updatedAt":"",
					 "team":null,"project":null,"state":null,"assignee":null,"creator":null,
					 "labels":{"pageInfo":{"endCursor":"l-1","hasNextPage":true},"nodes":[{"id":"l1"}]},
					 "comments":{"pageInfo":{"endCursor":"c-1","hasNextPage":true},"nodes":[
					   {"id":"c1","user":null,"body":"","createdAt":"","updatedAt":""}
					 ]}}
				]
			}}}`))
		}
	})
	page, err := c.FetchIssuesUpdatedSince(context.Background(), time.Now().Add(-time.Hour), "", 10)
	if err != nil {
		t.Fatal(err)
	}
	if got := len(page.Issues[0].LabelIDs); got != 2 {
		t.Errorf("expected 2 labels (1 inline + 1 drained), got %d", got)
	}
	if got := len(page.Issues[0].Comments); got != 2 {
		t.Errorf("expected 2 comments, got %d", got)
	}
	if atomic.LoadInt32(&labelPage) == 0 || atomic.LoadInt32(&commentPage) == 0 {
		t.Errorf("drain helpers not called: labels=%d comments=%d", labelPage, commentPage)
	}
}

func TestFetchIssueByIdentifierHappyPath(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"issue":{
			"id":"i1","identifier":"LIN-42","title":"T","description":"",
			"priority":1,"createdAt":"","updatedAt":"",
			"team":{"id":"t1"},"project":{"id":"p1"},"state":{"id":"s1"},
			"assignee":{"id":"u1"},"creator":{"id":"u2"},
			"labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[{"id":"l1"}]},
			"comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}
		}}}`))
	})
	iss, err := c.FetchIssueByIdentifier(context.Background(), "LIN-42")
	if err != nil {
		t.Fatal(err)
	}
	if iss.Identifier != "LIN-42" || iss.TeamID != "t1" || iss.AssigneeID != "u1" {
		t.Errorf("issue: %+v", iss)
	}
}

func TestFetchIssueByIdentifierRejectsEmpty(t *testing.T) {
	c := NewClient("", "lin_api_x")
	if _, err := c.FetchIssueByIdentifier(context.Background(), "   "); err == nil {
		t.Fatal("expected empty-id error")
	}
}

func TestFetchIssueByIdentifierNotFound(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"issue":null}}`))
	})
	_, err := c.FetchIssueByIdentifier(context.Background(), "LIN-NOPE")
	if err == nil || !strings.Contains(err.Error(), "not found") {
		t.Fatalf("err = %v", err)
	}
}

func TestFetchIssueByIdentifierPopulatesComments(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"issue":{
			"id":"i1","identifier":"LIN-42","title":"T","description":"",
			"priority":1,"createdAt":"","updatedAt":"",
			"team":null,"project":null,"state":null,"assignee":null,"creator":null,
			"labels":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]},
			"comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[
			  {"id":"c1","user":{"id":"u1"},"body":"hi","createdAt":"a","updatedAt":"b"},
			  {"id":"c2","user":null,"body":"anon","createdAt":"c","updatedAt":"d"}
			]}
		}}}`))
	})
	iss, err := c.FetchIssueByIdentifier(context.Background(), "LIN-42")
	if err != nil {
		t.Fatal(err)
	}
	if len(iss.Comments) != 2 {
		t.Fatalf("comments=%+v", iss.Comments)
	}
	if iss.Comments[0].AuthorID != "u1" {
		t.Errorf("first author: %q", iss.Comments[0].AuthorID)
	}
	if iss.Comments[1].AuthorID != "" {
		t.Errorf("second comment had nil user; expected empty AuthorID, got %q", iss.Comments[1].AuthorID)
	}
}

func TestFetchIssueByIdentifierDrainsNestedLabels(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		if _, hasAfter := req.Variables["after"]; hasAfter {
			// Second-page labels.
			_, _ = w.Write([]byte(`{"data":{"issue":{"labels":{
				"pageInfo":{"endCursor":"","hasNextPage":false},
				"nodes":[{"id":"l2"}]
			}}}}`))
			return
		}
		// First fetch: issue with labels page that has next.
		_, _ = w.Write([]byte(`{"data":{"issue":{
			"id":"i1","identifier":"LIN-42","title":"T","description":"",
			"priority":1,"createdAt":"","updatedAt":"",
			"team":null,"project":null,"state":null,"assignee":null,"creator":null,
			"labels":{"pageInfo":{"endCursor":"L1","hasNextPage":true},"nodes":[{"id":"l1"}]},
			"comments":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}
		}}}`))
	})
	iss, err := c.FetchIssueByIdentifier(context.Background(), "LIN-42")
	if err != nil {
		t.Fatal(err)
	}
	if len(iss.LabelIDs) != 2 {
		t.Fatalf("labels not paginated: %+v", iss.LabelIDs)
	}
}

func TestFetchIssuesUpdatedSinceClampsFirst(t *testing.T) {
	var seenFirst int
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Variables map[string]any `json:"variables"`
		}
		body, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(body, &req)
		if f, ok := req.Variables["first"].(float64); ok {
			seenFirst = int(f)
		}
		_, _ = w.Write([]byte(`{"data":{"issues":{"pageInfo":{"endCursor":"","hasNextPage":false},"nodes":[]}}}`))
	})
	if _, err := c.FetchIssuesUpdatedSince(context.Background(), time.Now(), "", 9999); err != nil {
		t.Fatal(err)
	}
	if seenFirst != 100 {
		t.Errorf("clamped first = %d, want default 100", seenFirst)
	}
}
