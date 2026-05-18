package store

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/uinaf/lincrawl/internal/linear"
)

// ExportNDJSON streams the entire local archive as canonical NDJSON: one
// record per line, typed via an envelope so consumers can branch on kind.
// Order: teams, states, users, labels, projects, issues (with embedded
// labels + comments). Idempotent re-read into another lincrawl via
// `sync --stdin` round-trips losslessly.
func (s *Store) ExportNDJSON(w io.Writer) (int, error) {
	enc := json.NewEncoder(w)
	count := 0

	type envelope struct {
		Kind string      `json:"kind"`
		Item interface{} `json:"item"`
	}

	teamRows, err := s.db.Query(`SELECT id, key, name, updated_at FROM teams ORDER BY id`)
	if err != nil {
		return count, err
	}
	for teamRows.Next() {
		var t linear.Team
		if err := teamRows.Scan(&t.ID, &t.Key, &t.Name, &t.UpdatedAt); err != nil {
			teamRows.Close()
			return count, err
		}
		if err := enc.Encode(envelope{Kind: "team", Item: t}); err != nil {
			teamRows.Close()
			return count, err
		}
		count++
	}
	teamRows.Close()

	stateRows, err := s.db.Query(`SELECT id, COALESCE(team_id,''), name, type FROM workflow_states ORDER BY id`)
	if err != nil {
		return count, err
	}
	for stateRows.Next() {
		var st linear.WorkflowState
		if err := stateRows.Scan(&st.ID, &st.TeamID, &st.Name, &st.Type); err != nil {
			stateRows.Close()
			return count, err
		}
		if err := enc.Encode(envelope{Kind: "state", Item: st}); err != nil {
			stateRows.Close()
			return count, err
		}
		count++
	}
	stateRows.Close()

	userRows, err := s.db.Query(`SELECT id, name, email FROM users ORDER BY id`)
	if err != nil {
		return count, err
	}
	for userRows.Next() {
		var u linear.User
		if err := userRows.Scan(&u.ID, &u.Name, &u.Email); err != nil {
			userRows.Close()
			return count, err
		}
		if err := enc.Encode(envelope{Kind: "user", Item: u}); err != nil {
			userRows.Close()
			return count, err
		}
		count++
	}
	userRows.Close()

	labelRows, err := s.db.Query(`SELECT id, COALESCE(team_id,''), name FROM labels ORDER BY id`)
	if err != nil {
		return count, err
	}
	for labelRows.Next() {
		var l linear.Label
		if err := labelRows.Scan(&l.ID, &l.TeamID, &l.Name); err != nil {
			labelRows.Close()
			return count, err
		}
		if err := enc.Encode(envelope{Kind: "label", Item: l}); err != nil {
			labelRows.Close()
			return count, err
		}
		count++
	}
	labelRows.Close()

	projectRows, err := s.db.Query(`SELECT id, name, state, updated_at FROM projects ORDER BY id`)
	if err != nil {
		return count, err
	}
	for projectRows.Next() {
		var p linear.Project
		if err := projectRows.Scan(&p.ID, &p.Name, &p.State, &p.UpdatedAt); err != nil {
			projectRows.Close()
			return count, err
		}
		if err := enc.Encode(envelope{Kind: "project", Item: p}); err != nil {
			projectRows.Close()
			return count, err
		}
		count++
	}
	projectRows.Close()

	issueRows, err := s.db.Query(`
SELECT id, identifier, title, COALESCE(description,''),
       COALESCE(team_id,''), COALESCE(project_id,''), COALESCE(state_id,''),
       COALESCE(assignee_id,''), COALESCE(creator_id,''), priority,
       created_at, updated_at
FROM issues ORDER BY id`)
	if err != nil {
		return count, err
	}
	var issues []linear.Issue
	for issueRows.Next() {
		var iss linear.Issue
		if err := issueRows.Scan(&iss.ID, &iss.Identifier, &iss.Title, &iss.Description,
			&iss.TeamID, &iss.ProjectID, &iss.StateID, &iss.AssigneeID, &iss.CreatorID,
			&iss.Priority, &iss.CreatedAt, &iss.UpdatedAt); err != nil {
			issueRows.Close()
			return count, err
		}
		issues = append(issues, iss)
	}
	if err := issueRows.Err(); err != nil {
		issueRows.Close()
		return count, err
	}
	issueRows.Close()
	for i := range issues {
		labels, err := s.issueLabelIDs(issues[i].ID)
		if err != nil {
			return count, fmt.Errorf("export labels %s: %w", issues[i].ID, err)
		}
		issues[i].LabelIDs = labels
		comments, err := s.issueComments(issues[i].ID)
		if err != nil {
			return count, fmt.Errorf("export comments %s: %w", issues[i].ID, err)
		}
		issues[i].Comments = comments
		if err := enc.Encode(envelope{Kind: "issue", Item: issues[i]}); err != nil {
			return count, err
		}
		count++
	}
	return count, nil
}

// Snapshot reads the entire local archive into a linear.Snapshot. Used by
// the publish/archive flows that need the full materialized graph.
func (s *Store) Snapshot() (linear.Snapshot, error) {
	var snap linear.Snapshot
	teamRows, err := s.db.Query(`SELECT id, key, name, updated_at FROM teams ORDER BY id`)
	if err != nil {
		return snap, err
	}
	for teamRows.Next() {
		var t linear.Team
		if err := teamRows.Scan(&t.ID, &t.Key, &t.Name, &t.UpdatedAt); err != nil {
			teamRows.Close()
			return snap, err
		}
		snap.Teams = append(snap.Teams, t)
	}
	teamRows.Close()
	stateRows, err := s.db.Query(`SELECT id, COALESCE(team_id,''), name, type FROM workflow_states ORDER BY id`)
	if err != nil {
		return snap, err
	}
	for stateRows.Next() {
		var ws linear.WorkflowState
		if err := stateRows.Scan(&ws.ID, &ws.TeamID, &ws.Name, &ws.Type); err != nil {
			stateRows.Close()
			return snap, err
		}
		snap.States = append(snap.States, ws)
	}
	stateRows.Close()
	userRows, err := s.db.Query(`SELECT id, name, email FROM users ORDER BY id`)
	if err != nil {
		return snap, err
	}
	for userRows.Next() {
		var u linear.User
		if err := userRows.Scan(&u.ID, &u.Name, &u.Email); err != nil {
			userRows.Close()
			return snap, err
		}
		snap.Users = append(snap.Users, u)
	}
	userRows.Close()
	labelRows, err := s.db.Query(`SELECT id, COALESCE(team_id,''), name FROM labels ORDER BY id`)
	if err != nil {
		return snap, err
	}
	for labelRows.Next() {
		var l linear.Label
		if err := labelRows.Scan(&l.ID, &l.TeamID, &l.Name); err != nil {
			labelRows.Close()
			return snap, err
		}
		snap.Labels = append(snap.Labels, l)
	}
	labelRows.Close()
	projectRows, err := s.db.Query(`SELECT id, name, state, updated_at FROM projects ORDER BY id`)
	if err != nil {
		return snap, err
	}
	for projectRows.Next() {
		var p linear.Project
		if err := projectRows.Scan(&p.ID, &p.Name, &p.State, &p.UpdatedAt); err != nil {
			projectRows.Close()
			return snap, err
		}
		snap.Projects = append(snap.Projects, p)
	}
	projectRows.Close()

	issueRows, err := s.db.Query(`
SELECT id, identifier, title, COALESCE(description,''),
       COALESCE(team_id,''), COALESCE(project_id,''), COALESCE(state_id,''),
       COALESCE(assignee_id,''), COALESCE(creator_id,''), priority,
       created_at, updated_at
FROM issues ORDER BY id`)
	if err != nil {
		return snap, err
	}
	var issues []linear.Issue
	for issueRows.Next() {
		var iss linear.Issue
		if err := issueRows.Scan(&iss.ID, &iss.Identifier, &iss.Title, &iss.Description,
			&iss.TeamID, &iss.ProjectID, &iss.StateID, &iss.AssigneeID, &iss.CreatorID,
			&iss.Priority, &iss.CreatedAt, &iss.UpdatedAt); err != nil {
			issueRows.Close()
			return snap, err
		}
		issues = append(issues, iss)
	}
	issueRows.Close()
	for i := range issues {
		labels, err := s.issueLabelIDs(issues[i].ID)
		if err != nil {
			return snap, err
		}
		issues[i].LabelIDs = labels
		comments, err := s.issueComments(issues[i].ID)
		if err != nil {
			return snap, err
		}
		issues[i].Comments = comments
	}
	snap.Issues = issues
	return snap, nil
}

func (s *Store) issueLabelIDs(issueID string) ([]string, error) {
	rows, err := s.db.Query(`SELECT label_id FROM issue_labels WHERE issue_id = ? ORDER BY label_id`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}
