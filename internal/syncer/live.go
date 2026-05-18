package syncer

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/uinaf/lincrawl/internal/linear"
	"github.com/uinaf/lincrawl/internal/store"
)

const issuesTailScope = "issues.tail"

type EntityResult struct {
	Viewer linear.ViewerInfo `json:"viewer"`
	Counts store.Counts      `json:"counts"`
}

func IngestEntities(ctx context.Context, s *store.Store, c *linear.Client) (EntityResult, error) {
	if s == nil || c == nil {
		return EntityResult{}, errors.New("syncer: nil store or client")
	}
	viewer, err := c.Viewer(ctx)
	if err != nil {
		return EntityResult{}, err
	}
	snap, err := c.FetchEntities(ctx)
	if err != nil {
		return EntityResult{}, err
	}
	if err := s.IngestSnapshot(snap); err != nil {
		return EntityResult{}, err
	}
	counts, err := s.Counts()
	if err != nil {
		return EntityResult{}, err
	}
	return EntityResult{Viewer: viewer, Counts: counts}, nil
}

type TailResult struct {
	Since          string       `json:"since"`
	Pages          int          `json:"pages"`
	IssuesPulled   int          `json:"issues_pulled"`
	CommentsPulled int          `json:"comments_pulled"`
	NewHighWater   string       `json:"new_high_water"`
	EndCursor      string       `json:"end_cursor"`
	Counts         store.Counts `json:"counts"`
}

const maxSyncPages = 1024

func IngestIssuesUpdatedSince(ctx context.Context, s *store.Store, c *linear.Client, since time.Time, pageSize, maxIssues int) (TailResult, error) {
	if s == nil || c == nil {
		return TailResult{}, errors.New("syncer: nil store or client")
	}
	res := TailResult{Since: since.UTC().Format(time.RFC3339)}
	cursor := ""
	for page := 0; page < maxSyncPages; page++ {
		first := pageSize
		if first <= 0 {
			first = 100
		}
		if maxIssues > 0 {
			remaining := maxIssues - res.IssuesPulled
			if remaining <= 0 {
				break
			}
			if remaining < first {
				first = remaining
			}
		}
		pg, err := c.FetchIssuesUpdatedSince(ctx, since, cursor, first)
		if err != nil {
			return res, err
		}
		res.Pages++
		issues := pg.Issues
		truncated := false
		if maxIssues > 0 && res.IssuesPulled+len(issues) > maxIssues {
			issues = issues[:maxIssues-res.IssuesPulled]
			truncated = true
		}
		frag := linear.Snapshot{Issues: issues}
		if err := s.IngestSnapshot(frag); err != nil {
			return res, err
		}
		for _, iss := range issues {
			res.IssuesPulled++
			res.CommentsPulled += len(iss.Comments)
			if iss.UpdatedAt > res.NewHighWater {
				res.NewHighWater = iss.UpdatedAt
			}
		}
		if !truncated {
			res.EndCursor = pg.EndCursor
		}
		if truncated {
			break
		}
		if maxIssues > 0 && res.IssuesPulled >= maxIssues {
			break
		}
		if !pg.HasNextPage || pg.EndCursor == "" {
			break
		}
		if pg.EndCursor == cursor {
			return res, fmt.Errorf("syncer: pagination stalled at cursor %q", cursor)
		}
		cursor = pg.EndCursor
	}
	if res.NewHighWater != "" {
		if err := s.SaveCursor(issuesTailScope, res.EndCursor, res.NewHighWater); err != nil {
			return res, fmt.Errorf("save cursor: %w", err)
		}
	}
	counts, err := s.Counts()
	if err != nil {
		return res, err
	}
	res.Counts = counts
	return res, nil
}

func StreamIssuesUpdatedSince(ctx context.Context, s *store.Store, c *linear.Client, since time.Time, pageSize, maxIssues int, onIssue func(linear.Issue) error) (int, error) {
	if s == nil || c == nil {
		return 0, errors.New("syncer: nil store or client")
	}
	cursor := ""
	pulled := 0
	committedHighWater := ""
	committedCursor := ""
	for page := 0; page < maxSyncPages; page++ {
		first := pageSize
		if first <= 0 {
			first = 100
		}
		if maxIssues > 0 {
			remaining := maxIssues - pulled
			if remaining <= 0 {
				break
			}
			if remaining < first {
				first = remaining
			}
		}
		pg, err := c.FetchIssuesUpdatedSince(ctx, since, cursor, first)
		if err != nil {
			return pulled, err
		}
		issues := pg.Issues
		truncated := false
		if maxIssues > 0 && pulled+len(issues) > maxIssues {
			issues = issues[:maxIssues-pulled]
			truncated = true
		}
		frag := linear.Snapshot{Issues: issues}
		if err := s.IngestSnapshot(frag); err != nil {
			return pulled, err
		}
		pageHigh := committedHighWater
		pageOK := true
		for _, iss := range issues {
			if err := onIssue(iss); err != nil {
				pageOK = false
				return pulled, err
			}
			pulled++
			if iss.UpdatedAt > pageHigh {
				pageHigh = iss.UpdatedAt
			}
		}
		if pageOK {
			committedHighWater = pageHigh
			if !truncated {
				committedCursor = pg.EndCursor
			}
		}
		if truncated || (maxIssues > 0 && pulled >= maxIssues) {
			break
		}
		if !pg.HasNextPage || pg.EndCursor == "" {
			break
		}
		if pg.EndCursor == cursor {
			return pulled, fmt.Errorf("syncer: pagination stalled at cursor %q", cursor)
		}
		cursor = pg.EndCursor
	}
	if committedHighWater != "" {
		if err := s.SaveCursor(issuesTailScope, committedCursor, committedHighWater); err != nil {
			return pulled, fmt.Errorf("save cursor: %w", err)
		}
	}
	return pulled, nil
}

type SingleIssueResult struct {
	Issue  linear.Issue `json:"issue"`
	Counts store.Counts `json:"counts"`
}

func IngestIssueByIdentifier(ctx context.Context, s *store.Store, c *linear.Client, id string) (SingleIssueResult, error) {
	if s == nil || c == nil {
		return SingleIssueResult{}, errors.New("syncer: nil store or client")
	}
	iss, err := c.FetchIssueByIdentifier(ctx, id)
	if err != nil {
		return SingleIssueResult{}, err
	}
	frag := linear.Snapshot{Issues: []linear.Issue{iss}}
	if err := s.IngestSnapshot(frag); err != nil {
		return SingleIssueResult{}, err
	}
	counts, err := s.Counts()
	if err != nil {
		return SingleIssueResult{}, err
	}
	return SingleIssueResult{Issue: iss, Counts: counts}, nil
}
