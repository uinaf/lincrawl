package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/uinaf/lincrawl/internal/buildinfo"
)

type Client struct {
	endpoint     string
	token        string
	authPrefix   string
	httpClient   *http.Client
	userAgent    string
	MaxAttempts  int
	RetryBackoff time.Duration
	Sleep        func(context.Context, time.Duration) error
	Now          func() time.Time
}

func NewClient(endpoint, token string) *Client {
	if endpoint == "" {
		endpoint = "https://api.linear.app/graphql"
	}
	prefix := ""
	if !strings.HasPrefix(token, "lin_api_") {
		prefix = "Bearer "
	}
	transport := &http.Transport{
		MaxIdleConns:        16,
		MaxIdleConnsPerHost: 8,
		IdleConnTimeout:     90 * time.Second,
		TLSHandshakeTimeout: 10 * time.Second,
	}
	ua := fmt.Sprintf("lincrawl/%s (+https://github.com/uinaf/lincrawl)", buildinfo.Current().Version)
	return &Client{
		endpoint:     endpoint,
		token:        token,
		authPrefix:   prefix,
		httpClient:   &http.Client{Timeout: 90 * time.Second, Transport: transport},
		userAgent:    ua,
		MaxAttempts:  4,
		RetryBackoff: 500 * time.Millisecond,
		Sleep:        contextSleep,
		Now:          func() time.Time { return time.Now() },
	}
}

func contextSleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return nil
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (c *Client) SetHTTPClient(h *http.Client) { c.httpClient = h }

func (c *Client) Endpoint() string { return c.endpoint }

// RateLimitError is returned when Linear answers with 429 (or `Retry-After`
// is present on a 5xx). Callers can retry after waiting `RetryAfter`.
type RateLimitError struct {
	RetryAfter time.Duration
	Status     int
	Message    string
}

func (e *RateLimitError) Error() string {
	return fmt.Sprintf("linear: rate limited (status=%d, retry-after=%s): %s", e.Status, e.RetryAfter, e.Message)
}

// AuthError is returned on 401/403 — the caller should not retry.
type AuthError struct {
	Status   int
	Messages []string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("linear: auth (status=%d): %s", e.Status, strings.Join(e.Messages, "; "))
}

// NotFoundError is returned on 404 or when GraphQL says the entity is gone.
type NotFoundError struct {
	Resource string
	Messages []string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("linear: %s not found: %s", e.Resource, strings.Join(e.Messages, "; "))
}

// Query runs an arbitrary GraphQL document with variables and returns the
// raw `data` envelope. Callers own response shape; lincrawl makes no
// assumptions. Use this to drive Linear directly when the built-in
// sync paths do not cover what you need.
func (c *Client) Query(ctx context.Context, query string, vars map[string]any) (json.RawMessage, error) {
	var raw json.RawMessage
	if err := c.do(ctx, gqlRequest{Query: query, Variables: vars}, &raw); err != nil {
		return nil, err
	}
	return raw, nil
}

type gqlRequest struct {
	Query     string         `json:"query"`
	Variables map[string]any `json:"variables,omitempty"`
}

type gqlError struct {
	Message    string         `json:"message"`
	Path       []any          `json:"path,omitempty"`
	Extensions map[string]any `json:"extensions,omitempty"`
}

type APIError struct {
	Status   int
	Messages []string
}

func (e *APIError) Error() string {
	if e.Status != 0 && len(e.Messages) == 0 {
		return fmt.Sprintf("linear: HTTP %d", e.Status)
	}
	return "linear: " + strings.Join(e.Messages, "; ")
}

func (c *Client) do(ctx context.Context, req gqlRequest, dst any) error {
	body, err := json.Marshal(req)
	if err != nil {
		return err
	}
	attempts := c.MaxAttempts
	if attempts <= 0 {
		attempts = 1
	}
	var lastErr error
	for attempt := 0; attempt < attempts; attempt++ {
		if attempt > 0 {
			delay := c.RetryBackoff * time.Duration(1<<(attempt-1))
			if rl, ok := lastErr.(*RateLimitError); ok && rl.RetryAfter > delay {
				delay = rl.RetryAfter
			}
			delay += time.Duration(rand.Int64N(int64(c.RetryBackoff))) // #nosec G404 -- backoff jitter, not crypto
			if err := c.Sleep(ctx, delay); err != nil {
				return err
			}
		}
		err := c.doOnce(ctx, body, dst)
		if err == nil {
			return nil
		}
		lastErr = err
		if !isRetryable(err) {
			return err
		}
	}
	return lastErr
}

func (c *Client) doOnce(ctx context.Context, body []byte, dst any) error {
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/json")
	httpReq.Header.Set("User-Agent", c.userAgent)
	httpReq.Header.Set("Authorization", c.authPrefix+c.token)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("linear: transport: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, 32<<20))
	if err != nil {
		return fmt.Errorf("linear: read: %w", err)
	}

	if resp.StatusCode == http.StatusTooManyRequests || (resp.StatusCode >= 500 && resp.Header.Get("Retry-After") != "") {
		return &RateLimitError{
			Status:     resp.StatusCode,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After")),
			Message:    firstMessage(peelErrorMessages(raw)),
		}
	}
	if resp.StatusCode == http.StatusUnauthorized || resp.StatusCode == http.StatusForbidden {
		return &AuthError{Status: resp.StatusCode, Messages: peelErrorMessages(raw)}
	}
	if resp.StatusCode == http.StatusNotFound {
		return &NotFoundError{Resource: "endpoint", Messages: peelErrorMessages(raw)}
	}
	if resp.StatusCode >= 500 {
		return &APIError{Status: resp.StatusCode, Messages: peelErrorMessages(raw)}
	}
	if resp.StatusCode >= 400 {
		return &APIError{Status: resp.StatusCode, Messages: peelErrorMessages(raw)}
	}

	envelope := struct {
		Data   json.RawMessage `json:"data"`
		Errors []gqlError      `json:"errors"`
	}{}
	if err := json.Unmarshal(raw, &envelope); err != nil {
		return fmt.Errorf("linear: decode envelope: %w", err)
	}
	if len(envelope.Errors) > 0 {
		msgs := make([]string, 0, len(envelope.Errors))
		for _, e := range envelope.Errors {
			msgs = append(msgs, e.Message)
		}
		return &APIError{Status: resp.StatusCode, Messages: msgs}
	}
	if dst == nil {
		return nil
	}
	if err := json.Unmarshal(envelope.Data, dst); err != nil {
		return fmt.Errorf("linear: decode data: %w", err)
	}
	return nil
}

func isRetryable(err error) bool {
	var rl *RateLimitError
	if errors.As(err, &rl) {
		return true
	}
	var ae *APIError
	if errors.As(err, &ae) && ae.Status >= 500 {
		return true
	}
	// Transport-level error (network).
	return strings.Contains(err.Error(), "transport:")
}

func parseRetryAfter(header string) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(header); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}
	return 0
}

func firstMessage(msgs []string) string {
	if len(msgs) == 0 {
		return ""
	}
	return msgs[0]
}

func peelErrorMessages(raw []byte) []string {
	var env struct {
		Errors []gqlError `json:"errors"`
	}
	if err := json.Unmarshal(raw, &env); err == nil && len(env.Errors) > 0 {
		msgs := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			msgs = append(msgs, e.Message)
		}
		return msgs
	}
	trim := strings.TrimSpace(string(raw))
	if len(trim) > 200 {
		trim = trim[:200] + "…"
	}
	if trim == "" {
		return nil
	}
	return []string{trim}
}

type ViewerInfo struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

func (c *Client) Viewer(ctx context.Context) (ViewerInfo, error) {
	const q = `query { viewer { id name email } }`
	var out struct {
		Viewer ViewerInfo `json:"viewer"`
	}
	if err := c.do(ctx, gqlRequest{Query: q}, &out); err != nil {
		return ViewerInfo{}, err
	}
	return out.Viewer, nil
}

type pageInfo struct {
	EndCursor   string `json:"endCursor"`
	HasNextPage bool   `json:"hasNextPage"`
}

func (c *Client) FetchEntities(ctx context.Context) (Snapshot, error) {
	const pageSize = 100
	snap := Snapshot{}

	teamsQ := `query($first: Int!, $after: String) {
  teams(first: $first, after: $after) {
    pageInfo { endCursor hasNextPage }
    nodes { id key name updatedAt }
  }
}`
	if err := paginate(ctx, c, teamsQ, pageSize, func(raw json.RawMessage) (pageInfo, error) {
		var body struct {
			Teams struct {
				PageInfo pageInfo `json:"pageInfo"`
				Nodes    []Team   `json:"nodes"`
			} `json:"teams"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return pageInfo{}, err
		}
		snap.Teams = append(snap.Teams, body.Teams.Nodes...)
		return body.Teams.PageInfo, nil
	}); err != nil {
		return Snapshot{}, err
	}

	statesQ := `query($first: Int!, $after: String) {
  workflowStates(first: $first, after: $after) {
    pageInfo { endCursor hasNextPage }
    nodes { id team { id } name type }
  }
}`
	if err := paginate(ctx, c, statesQ, pageSize, func(raw json.RawMessage) (pageInfo, error) {
		var body struct {
			WorkflowStates struct {
				PageInfo pageInfo `json:"pageInfo"`
				Nodes    []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
					Type string `json:"type"`
					Team *struct {
						ID string `json:"id"`
					} `json:"team"`
				} `json:"nodes"`
			} `json:"workflowStates"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return pageInfo{}, err
		}
		for _, n := range body.WorkflowStates.Nodes {
			ws := WorkflowState{ID: n.ID, Name: n.Name, Type: n.Type}
			if n.Team != nil {
				ws.TeamID = n.Team.ID
			}
			snap.States = append(snap.States, ws)
		}
		return body.WorkflowStates.PageInfo, nil
	}); err != nil {
		return Snapshot{}, err
	}

	usersQ := `query($first: Int!, $after: String) {
  users(first: $first, after: $after) {
    pageInfo { endCursor hasNextPage }
    nodes { id name email }
  }
}`
	if err := paginate(ctx, c, usersQ, pageSize, func(raw json.RawMessage) (pageInfo, error) {
		var body struct {
			Users struct {
				PageInfo pageInfo `json:"pageInfo"`
				Nodes    []User   `json:"nodes"`
			} `json:"users"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return pageInfo{}, err
		}
		snap.Users = append(snap.Users, body.Users.Nodes...)
		return body.Users.PageInfo, nil
	}); err != nil {
		return Snapshot{}, err
	}

	labelsQ := `query($first: Int!, $after: String) {
  issueLabels(first: $first, after: $after) {
    pageInfo { endCursor hasNextPage }
    nodes { id name team { id } }
  }
}`
	if err := paginate(ctx, c, labelsQ, pageSize, func(raw json.RawMessage) (pageInfo, error) {
		var body struct {
			IssueLabels struct {
				PageInfo pageInfo `json:"pageInfo"`
				Nodes    []struct {
					ID   string `json:"id"`
					Name string `json:"name"`
					Team *struct {
						ID string `json:"id"`
					} `json:"team"`
				} `json:"nodes"`
			} `json:"issueLabels"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return pageInfo{}, err
		}
		for _, n := range body.IssueLabels.Nodes {
			l := Label{ID: n.ID, Name: n.Name}
			if n.Team != nil {
				l.TeamID = n.Team.ID
			}
			snap.Labels = append(snap.Labels, l)
		}
		return body.IssueLabels.PageInfo, nil
	}); err != nil {
		return Snapshot{}, err
	}

	projectsQ := `query($first: Int!, $after: String) {
  projects(first: $first, after: $after) {
    pageInfo { endCursor hasNextPage }
    nodes { id name state updatedAt }
  }
}`
	if err := paginate(ctx, c, projectsQ, pageSize, func(raw json.RawMessage) (pageInfo, error) {
		var body struct {
			Projects struct {
				PageInfo pageInfo  `json:"pageInfo"`
				Nodes    []Project `json:"nodes"`
			} `json:"projects"`
		}
		if err := json.Unmarshal(raw, &body); err != nil {
			return pageInfo{}, err
		}
		snap.Projects = append(snap.Projects, body.Projects.Nodes...)
		return body.Projects.PageInfo, nil
	}); err != nil {
		return Snapshot{}, err
	}

	return snap, nil
}

const maxPaginationPages = 1024

func paginate(ctx context.Context, c *Client, query string, pageSize int, ingest func(json.RawMessage) (pageInfo, error)) error {
	cursor := ""
	for page := 0; page < maxPaginationPages; page++ {
		vars := map[string]any{"first": pageSize}
		if cursor != "" {
			vars["after"] = cursor
		}
		var raw json.RawMessage
		if err := c.do(ctx, gqlRequest{Query: query, Variables: vars}, &raw); err != nil {
			return err
		}
		pi, err := ingest(raw)
		if err != nil {
			return err
		}
		if !pi.HasNextPage || pi.EndCursor == "" {
			return nil
		}
		if pi.EndCursor == cursor {
			return fmt.Errorf("linear: pagination stalled at cursor %q", cursor)
		}
		cursor = pi.EndCursor
	}
	return fmt.Errorf("linear: pagination exceeded %d pages", maxPaginationPages)
}

type IssuesPage struct {
	Issues      []Issue
	EndCursor   string
	HasNextPage bool
}

func (c *Client) FetchIssuesUpdatedSince(ctx context.Context, since time.Time, after string, first int) (IssuesPage, error) {
	if first <= 0 || first > 250 {
		first = 100
	}
	const q = `
query Issues($since: DateTimeOrDuration!, $first: Int!, $after: String) {
  issues(
    filter: { updatedAt: { gte: $since } },
    first: $first,
    after: $after,
    orderBy: updatedAt
  ) {
    pageInfo { endCursor hasNextPage }
    nodes {
      id identifier title description priority createdAt updatedAt
      team { id } project { id } state { id } assignee { id } creator { id }
      labels(first: 50) {
        pageInfo { endCursor hasNextPage }
        nodes { id }
      }
      comments(first: 100) {
        pageInfo { endCursor hasNextPage }
        nodes { id user { id } body createdAt updatedAt }
      }
    }
  }
}`
	type commentNode struct {
		ID   string `json:"id"`
		User *struct {
			ID string `json:"id"`
		} `json:"user"`
		Body      string `json:"body"`
		CreatedAt string `json:"createdAt"`
		UpdatedAt string `json:"updatedAt"`
	}
	type idRef struct {
		ID string `json:"id"`
	}
	type issueNode struct {
		ID          string `json:"id"`
		Identifier  string `json:"identifier"`
		Title       string `json:"title"`
		Description string `json:"description"`
		Priority    int    `json:"priority"`
		CreatedAt   string `json:"createdAt"`
		UpdatedAt   string `json:"updatedAt"`
		Team        *idRef `json:"team"`
		Project     *idRef `json:"project"`
		State       *idRef `json:"state"`
		Assignee    *idRef `json:"assignee"`
		Creator     *idRef `json:"creator"`
		Labels      struct {
			PageInfo pageInfo `json:"pageInfo"`
			Nodes    []idRef  `json:"nodes"`
		} `json:"labels"`
		Comments struct {
			PageInfo pageInfo      `json:"pageInfo"`
			Nodes    []commentNode `json:"nodes"`
		} `json:"comments"`
	}
	type body struct {
		Issues struct {
			PageInfo pageInfo    `json:"pageInfo"`
			Nodes    []issueNode `json:"nodes"`
		} `json:"issues"`
	}
	vars := map[string]any{
		"since": since.UTC().Format(time.RFC3339),
		"first": first,
	}
	if after != "" {
		vars["after"] = after
	}
	var b body
	if err := c.do(ctx, gqlRequest{Query: q, Variables: vars}, &b); err != nil {
		return IssuesPage{}, err
	}
	page := IssuesPage{
		EndCursor:   b.Issues.PageInfo.EndCursor,
		HasNextPage: b.Issues.PageInfo.HasNextPage,
	}
	for _, n := range b.Issues.Nodes {
		iss := Issue{
			ID:          n.ID,
			Identifier:  n.Identifier,
			Title:       n.Title,
			Description: n.Description,
			Priority:    n.Priority,
			CreatedAt:   n.CreatedAt,
			UpdatedAt:   n.UpdatedAt,
		}
		if n.Team != nil {
			iss.TeamID = n.Team.ID
		}
		if n.Project != nil {
			iss.ProjectID = n.Project.ID
		}
		if n.State != nil {
			iss.StateID = n.State.ID
		}
		if n.Assignee != nil {
			iss.AssigneeID = n.Assignee.ID
		}
		if n.Creator != nil {
			iss.CreatorID = n.Creator.ID
		}
		for _, l := range n.Labels.Nodes {
			iss.LabelIDs = append(iss.LabelIDs, l.ID)
		}
		for _, c := range n.Comments.Nodes {
			cm := Comment{
				ID:        c.ID,
				IssueID:   n.ID,
				Body:      c.Body,
				CreatedAt: c.CreatedAt,
				UpdatedAt: c.UpdatedAt,
			}
			if c.User != nil {
				cm.AuthorID = c.User.ID
			}
			iss.Comments = append(iss.Comments, cm)
		}
		if n.Labels.PageInfo.HasNextPage {
			extra, err := c.fetchAllIssueLabels(ctx, n.ID, n.Labels.PageInfo.EndCursor)
			if err != nil {
				return IssuesPage{}, err
			}
			iss.LabelIDs = append(iss.LabelIDs, extra...)
		}
		if n.Comments.PageInfo.HasNextPage {
			extra, err := c.fetchAllIssueComments(ctx, n.ID, n.Comments.PageInfo.EndCursor)
			if err != nil {
				return IssuesPage{}, err
			}
			iss.Comments = append(iss.Comments, extra...)
		}
		page.Issues = append(page.Issues, iss)
	}
	return page, nil
}

func (c *Client) fetchAllIssueLabels(ctx context.Context, issueID, after string) ([]string, error) {
	const q = `query($id: String!, $first: Int!, $after: String) {
  issue(id: $id) {
    labels(first: $first, after: $after) {
      pageInfo { endCursor hasNextPage }
      nodes { id }
    }
  }
}`
	var out []string
	cursor := after
	for page := 0; page < maxPaginationPages; page++ {
		vars := map[string]any{"id": issueID, "first": 100}
		if cursor != "" {
			vars["after"] = cursor
		}
		var b struct {
			Issue struct {
				Labels struct {
					PageInfo pageInfo `json:"pageInfo"`
					Nodes    []struct {
						ID string `json:"id"`
					} `json:"nodes"`
				} `json:"labels"`
			} `json:"issue"`
		}
		if err := c.do(ctx, gqlRequest{Query: q, Variables: vars}, &b); err != nil {
			return nil, err
		}
		for _, n := range b.Issue.Labels.Nodes {
			out = append(out, n.ID)
		}
		next := b.Issue.Labels.PageInfo
		if !next.HasNextPage || next.EndCursor == "" {
			return out, nil
		}
		if next.EndCursor == cursor {
			return nil, fmt.Errorf("linear: issue %s labels paginate stalled at cursor %q", issueID, cursor)
		}
		cursor = next.EndCursor
	}
	return nil, fmt.Errorf("linear: issue %s labels exceeded %d pages", issueID, maxPaginationPages)
}

func (c *Client) fetchAllIssueComments(ctx context.Context, issueID, after string) ([]Comment, error) {
	const q = `query($id: String!, $first: Int!, $after: String) {
  issue(id: $id) {
    comments(first: $first, after: $after) {
      pageInfo { endCursor hasNextPage }
      nodes { id user { id } body createdAt updatedAt }
    }
  }
}`
	var out []Comment
	cursor := after
	for page := 0; page < maxPaginationPages; page++ {
		vars := map[string]any{"id": issueID, "first": 100}
		if cursor != "" {
			vars["after"] = cursor
		}
		var b struct {
			Issue struct {
				Comments struct {
					PageInfo pageInfo `json:"pageInfo"`
					Nodes    []struct {
						ID   string `json:"id"`
						User *struct {
							ID string `json:"id"`
						} `json:"user"`
						Body      string `json:"body"`
						CreatedAt string `json:"createdAt"`
						UpdatedAt string `json:"updatedAt"`
					} `json:"nodes"`
				} `json:"comments"`
			} `json:"issue"`
		}
		if err := c.do(ctx, gqlRequest{Query: q, Variables: vars}, &b); err != nil {
			return nil, err
		}
		for _, n := range b.Issue.Comments.Nodes {
			cm := Comment{
				ID:        n.ID,
				IssueID:   issueID,
				Body:      n.Body,
				CreatedAt: n.CreatedAt,
				UpdatedAt: n.UpdatedAt,
			}
			if n.User != nil {
				cm.AuthorID = n.User.ID
			}
			out = append(out, cm)
		}
		next := b.Issue.Comments.PageInfo
		if !next.HasNextPage || next.EndCursor == "" {
			return out, nil
		}
		if next.EndCursor == cursor {
			return nil, fmt.Errorf("linear: issue %s comments paginate stalled at cursor %q", issueID, cursor)
		}
		cursor = next.EndCursor
	}
	return nil, fmt.Errorf("linear: issue %s comments exceeded %d pages", issueID, maxPaginationPages)
}

func (c *Client) FetchIssueByIdentifier(ctx context.Context, idOrIdentifier string) (Issue, error) {
	id := strings.TrimSpace(idOrIdentifier)
	if id == "" {
		return Issue{}, errors.New("linear: empty issue id")
	}
	const q = `
query OneIssue($id: String!) {
  issue(id: $id) {
    id identifier title description priority createdAt updatedAt
    team { id } project { id } state { id } assignee { id } creator { id }
    labels(first: 50) {
      pageInfo { endCursor hasNextPage }
      nodes { id }
    }
    comments(first: 100) {
      pageInfo { endCursor hasNextPage }
      nodes { id user { id } body createdAt updatedAt }
    }
  }
}`
	type commentNode struct {
		ID   string `json:"id"`
		User *struct {
			ID string `json:"id"`
		} `json:"user"`
		Body      string `json:"body"`
		CreatedAt string `json:"createdAt"`
		UpdatedAt string `json:"updatedAt"`
	}
	type idRef struct {
		ID string `json:"id"`
	}
	type body struct {
		Issue *struct {
			ID          string `json:"id"`
			Identifier  string `json:"identifier"`
			Title       string `json:"title"`
			Description string `json:"description"`
			Priority    int    `json:"priority"`
			CreatedAt   string `json:"createdAt"`
			UpdatedAt   string `json:"updatedAt"`
			Team        *idRef `json:"team"`
			Project     *idRef `json:"project"`
			State       *idRef `json:"state"`
			Assignee    *idRef `json:"assignee"`
			Creator     *idRef `json:"creator"`
			Labels      struct {
				PageInfo pageInfo `json:"pageInfo"`
				Nodes    []idRef  `json:"nodes"`
			} `json:"labels"`
			Comments struct {
				PageInfo pageInfo      `json:"pageInfo"`
				Nodes    []commentNode `json:"nodes"`
			} `json:"comments"`
		} `json:"issue"`
	}
	var b body
	if err := c.do(ctx, gqlRequest{Query: q, Variables: map[string]any{"id": id}}, &b); err != nil {
		return Issue{}, err
	}
	if b.Issue == nil {
		return Issue{}, fmt.Errorf("linear: issue %q not found", id)
	}
	iss := Issue{
		ID:          b.Issue.ID,
		Identifier:  b.Issue.Identifier,
		Title:       b.Issue.Title,
		Description: b.Issue.Description,
		Priority:    b.Issue.Priority,
		CreatedAt:   b.Issue.CreatedAt,
		UpdatedAt:   b.Issue.UpdatedAt,
	}
	if b.Issue.Team != nil {
		iss.TeamID = b.Issue.Team.ID
	}
	if b.Issue.Project != nil {
		iss.ProjectID = b.Issue.Project.ID
	}
	if b.Issue.State != nil {
		iss.StateID = b.Issue.State.ID
	}
	if b.Issue.Assignee != nil {
		iss.AssigneeID = b.Issue.Assignee.ID
	}
	if b.Issue.Creator != nil {
		iss.CreatorID = b.Issue.Creator.ID
	}
	for _, l := range b.Issue.Labels.Nodes {
		iss.LabelIDs = append(iss.LabelIDs, l.ID)
	}
	for _, c := range b.Issue.Comments.Nodes {
		cm := Comment{
			ID:        c.ID,
			IssueID:   b.Issue.ID,
			Body:      c.Body,
			CreatedAt: c.CreatedAt,
			UpdatedAt: c.UpdatedAt,
		}
		if c.User != nil {
			cm.AuthorID = c.User.ID
		}
		iss.Comments = append(iss.Comments, cm)
	}
	if b.Issue.Labels.PageInfo.HasNextPage {
		extra, err := c.fetchAllIssueLabels(ctx, b.Issue.ID, b.Issue.Labels.PageInfo.EndCursor)
		if err != nil {
			return Issue{}, err
		}
		iss.LabelIDs = append(iss.LabelIDs, extra...)
	}
	if b.Issue.Comments.PageInfo.HasNextPage {
		extra, err := c.fetchAllIssueComments(ctx, b.Issue.ID, b.Issue.Comments.PageInfo.EndCursor)
		if err != nil {
			return Issue{}, err
		}
		iss.Comments = append(iss.Comments, extra...)
	}
	return iss, nil
}
