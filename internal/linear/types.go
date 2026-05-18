// Package linear holds the typed surface of the Linear API that lincrawl
// consumes: entity types and the fixture loader that feeds the store. The
// live GraphQL client lands in its own file alongside these types.
package linear

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// Team is the minimal Linear team shape lincrawl mirrors locally.
type Team struct {
	ID         string `json:"id"`
	Key        string `json:"key"`
	Name       string `json:"name"`
	UpdatedAt  string `json:"updated_at"`
}

// WorkflowState matches Linear's state.type vocabulary (`triage`, `backlog`,
// `unstarted`, `started`, `completed`, `canceled`).
type WorkflowState struct {
	ID     string `json:"id"`
	TeamID string `json:"team_id"`
	Name   string `json:"name"`
	Type   string `json:"type"`
}

// User is a minimal Linear user reference.
type User struct {
	ID    string `json:"id"`
	Name  string `json:"name"`
	Email string `json:"email"`
}

// Label is a Linear label reference.
type Label struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	TeamID string `json:"team_id"`
}

// Project is the small slice of Linear project metadata lincrawl tracks.
type Project struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	State     string `json:"state"`
	UpdatedAt string `json:"updated_at"`
}

// Comment is a Linear issue comment.
type Comment struct {
	ID        string `json:"id"`
	IssueID   string `json:"issue_id"`
	AuthorID  string `json:"author_id"`
	Body      string `json:"body"`
	CreatedAt string `json:"created_at"`
	UpdatedAt string `json:"updated_at"`
}

// Issue is a Linear issue plus enough denormalized references to make local
// listing useful without joining every time.
type Issue struct {
	ID          string    `json:"id"`
	Identifier  string    `json:"identifier"` // e.g. LIN-12
	Title       string    `json:"title"`
	Description string    `json:"description"`
	TeamID      string    `json:"team_id"`
	ProjectID   string    `json:"project_id"`
	StateID     string    `json:"state_id"`
	AssigneeID  string    `json:"assignee_id"`
	CreatorID   string    `json:"creator_id"`
	Priority    int       `json:"priority"`
	LabelIDs    []string  `json:"label_ids"`
	CreatedAt   string    `json:"created_at"`
	UpdatedAt   string    `json:"updated_at"`
	Comments    []Comment `json:"comments,omitempty"`
}

// Snapshot is the on-disk fixture shape: a single JSON document with all
// entities. This intentionally mirrors what a future paginated GraphQL sync
// would deliver, so the same store layer can ingest both.
type Snapshot struct {
	Teams    []Team          `json:"teams"`
	States   []WorkflowState `json:"states"`
	Users    []User          `json:"users"`
	Labels   []Label         `json:"labels"`
	Projects []Project       `json:"projects"`
	Issues   []Issue         `json:"issues"`
}

// LoadFixture reads a fixture directory. The contract: the directory contains
// a single `snapshot.json` (or, for forward compatibility, `*.snapshot.json`
// files merged in lexical order). Real tenant data must never be checked into
// the fixture tree.
func LoadFixture(dir string) (Snapshot, error) {
	info, err := os.Stat(dir)
	if err != nil {
		return Snapshot{}, fmt.Errorf("fixture: %w", err)
	}
	if !info.IsDir() {
		return Snapshot{}, fmt.Errorf("fixture: %s is not a directory", dir)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return Snapshot{}, fmt.Errorf("fixture: %w", err)
	}
	var files []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		name := e.Name()
		if name == "snapshot.json" || strings.HasSuffix(name, ".snapshot.json") {
			files = append(files, filepath.Join(dir, name))
		}
	}
	sort.Strings(files)
	if len(files) == 0 {
		return Snapshot{}, fmt.Errorf("fixture: no snapshot.json under %s", dir)
	}
	var merged Snapshot
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			return Snapshot{}, fmt.Errorf("fixture: %s: %w", path, err)
		}
		var s Snapshot
		if err := json.Unmarshal(raw, &s); err != nil {
			return Snapshot{}, fmt.Errorf("fixture: %s: %w", path, err)
		}
		merged.Teams = append(merged.Teams, s.Teams...)
		merged.States = append(merged.States, s.States...)
		merged.Users = append(merged.Users, s.Users...)
		merged.Labels = append(merged.Labels, s.Labels...)
		merged.Projects = append(merged.Projects, s.Projects...)
		merged.Issues = append(merged.Issues, s.Issues...)
	}
	if err := merged.Validate(); err != nil {
		return Snapshot{}, err
	}
	return merged, nil
}

// Validate enforces simple referential-integrity rules so a typo in a fixture
// fails the smoke test rather than producing mysterious empty queries.
func (s Snapshot) Validate() error {
	teams := indexBy(s.Teams, func(t Team) string { return t.ID })
	states := indexBy(s.States, func(w WorkflowState) string { return w.ID })
	projects := indexBy(s.Projects, func(p Project) string { return p.ID })
	users := indexBy(s.Users, func(u User) string { return u.ID })
	labels := indexBy(s.Labels, func(l Label) string { return l.ID })

	for _, iss := range s.Issues {
		if iss.ID == "" || iss.Identifier == "" {
			return fmt.Errorf("issue missing id/identifier: %+v", iss)
		}
		if _, ok := teams[iss.TeamID]; !ok && iss.TeamID != "" {
			return fmt.Errorf("issue %s references unknown team %q", iss.Identifier, iss.TeamID)
		}
		if iss.StateID != "" {
			if _, ok := states[iss.StateID]; !ok {
				return fmt.Errorf("issue %s references unknown state %q", iss.Identifier, iss.StateID)
			}
		}
		if iss.ProjectID != "" {
			if _, ok := projects[iss.ProjectID]; !ok {
				return fmt.Errorf("issue %s references unknown project %q", iss.Identifier, iss.ProjectID)
			}
		}
		if iss.AssigneeID != "" {
			if _, ok := users[iss.AssigneeID]; !ok {
				return fmt.Errorf("issue %s references unknown assignee %q", iss.Identifier, iss.AssigneeID)
			}
		}
		for _, lid := range iss.LabelIDs {
			if _, ok := labels[lid]; !ok {
				return fmt.Errorf("issue %s references unknown label %q", iss.Identifier, lid)
			}
		}
		if iss.UpdatedAt != "" {
			if _, err := time.Parse(time.RFC3339, iss.UpdatedAt); err != nil {
				return fmt.Errorf("issue %s updated_at: %w", iss.Identifier, err)
			}
		}
	}
	return nil
}

func indexBy[T any](items []T, key func(T) string) map[string]T {
	m := make(map[string]T, len(items))
	for _, item := range items {
		m[key(item)] = item
	}
	return m
}
