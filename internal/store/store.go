// Package store owns the lincrawl SQLite archive. Schema is intentionally
// flat and migration-friendly: each Linear entity gets one table and one set
// of upserts, with an FTS5 mirror over issue title/description and comment
// body so search has no JOIN-heavy hot path.
//
// The store is the source of truth for "what lincrawl has locally." It does
// not own provider semantics or fixture loading.
package store

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	ckstore "github.com/openclaw/crawlkit/store"

	"github.com/uinaf/lincrawl/internal/linear"
)

// SchemaVersion is the current lincrawl SQLite schema version. Bumped when
// the on-disk shape changes; crawlkit/store refuses to open a db whose
// `schema_migrations` exceeds this number.
const SchemaVersion = 1

// Store wraps a single SQLite database used as the local lincrawl archive.
type Store struct {
	inner *ckstore.Store
	db    *sql.DB
	path  string
}

// Open opens (or creates) the SQLite archive at path and applies the schema.
func Open(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: empty path")
	}
	// 0o700 on the archive directory: lincrawl stores tenant issue bodies
	// and comments; nothing on a shared machine should be able to read it.
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, err
	}
	inner, err := ckstore.Open(context.Background(), ckstore.Options{
		Path:          path,
		Schema:        schemaSQL,
		SchemaVersion: SchemaVersion,
	})
	if err != nil {
		return nil, err
	}
	// crawlkit chmod 0600 the .db file but not -wal / -shm.
	for _, p := range []string{path + "-wal", path + "-shm"} {
		if _, err := os.Stat(p); err == nil {
			if chmodErr := os.Chmod(p, 0o600); chmodErr != nil {
				_ = inner.Close()
				return nil, chmodErr
			}
		}
	}
	return &Store{inner: inner, db: inner.DB(), path: path}, nil
}

// OpenReadOnly opens an existing SQLite archive in read-only mode.
// search/show/status/export use this to avoid contending with a concurrent
// writer holding the WAL.
func OpenReadOnly(path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: empty path")
	}
	inner, err := ckstore.OpenReadOnly(context.Background(), path)
	if err != nil {
		return nil, err
	}
	return &Store{inner: inner, db: inner.DB(), path: path}, nil
}

// Close releases the underlying database handle.
func (s *Store) Close() error { return s.inner.Close() }

// Path returns the on-disk location for status output.
func (s *Store) Path() string { return s.path }

// DB exposes the underlying handle for callers that need direct access
// (e.g. crawlkit/state.New). Callers must not Close it.
func (s *Store) DB() *sql.DB { return s.db }

// Counts summarises how many of each entity the archive holds.
type Counts struct {
	Teams    int `json:"teams"`
	States   int `json:"states"`
	Users    int `json:"users"`
	Labels   int `json:"labels"`
	Projects int `json:"projects"`
	Issues   int `json:"issues"`
	Comments int `json:"comments"`
}

// Counts reports current row counts across the main tables.
func (s *Store) Counts() (Counts, error) {
	tables := []struct {
		name string
		dst  *int
	}{
		{"teams", new(int)},
		{"workflow_states", new(int)},
		{"users", new(int)},
		{"labels", new(int)},
		{"projects", new(int)},
		{"issues", new(int)},
		{"comments", new(int)},
	}
	for _, t := range tables {
		row := s.db.QueryRow("SELECT count(*) FROM " + t.name)
		if err := row.Scan(t.dst); err != nil {
			return Counts{}, fmt.Errorf("count %s: %w", t.name, err)
		}
	}
	return Counts{
		Teams: *tables[0].dst, States: *tables[1].dst, Users: *tables[2].dst,
		Labels: *tables[3].dst, Projects: *tables[4].dst,
		Issues: *tables[5].dst, Comments: *tables[6].dst,
	}, nil
}

// SearchResult is one hit from the FTS path with enough metadata to render
// either JSON or NDJSON output without extra round-trips.
type SearchResult struct {
	IssueID    string  `json:"id"`
	Identifier string  `json:"identifier"`
	Title      string  `json:"title"`
	TeamKey    string  `json:"team_key"`
	StateName  string  `json:"state_name"`
	StateType  string  `json:"state_type"`
	UpdatedAt  string  `json:"updated_at"`
	Snippet    string  `json:"snippet"`
	Score      float64 `json:"score"`
}

// PhraseQuery wraps user input as an FTS5 phrase so characters that would
// otherwise be FTS5 operators (`:`, `*`, parens, hyphens) become literal
// search text. Callers that want raw FTS5 syntax pass through to Search
// directly. Empty input yields an empty string; Search rejects that.
func PhraseQuery(input string) string {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return ""
	}
	// FTS5 phrase escape: doubled double quotes inside a "..." phrase.
	return `"` + strings.ReplaceAll(trimmed, `"`, `""`) + `"`
}

// Search runs an FTS5 query against issue title, description, and comments.
// The query is passed verbatim to FTS5; callers that accept untrusted input
// should wrap it with PhraseQuery first.
func (s *Store) Search(query string, limit int) ([]SearchResult, error) {
	q := strings.TrimSpace(query)
	if q == "" {
		return nil, errors.New("search: empty query")
	}
	if limit <= 0 {
		limit = 50
	}
	// bm25 column weights: identifier 0, title 10, description 5, comments 1.
	// Title hits dominate so an issue with the query word in its title beats
	// a comment that quotes it five times.
	const sqlText = `
SELECT i.id, i.identifier, i.title, COALESCE(t.key,''), COALESCE(w.name,''),
       COALESCE(w.type,''), i.updated_at,
       snippet(issue_fts, 2, '<<', '>>', '…', 16), bm25(issue_fts, 0.0, 10.0, 5.0, 1.0)
FROM issue_fts
JOIN issues i ON i.rowid = issue_fts.rowid
LEFT JOIN teams t ON t.id = i.team_id
LEFT JOIN workflow_states w ON w.id = i.state_id
WHERE issue_fts MATCH ?
ORDER BY bm25(issue_fts, 0.0, 10.0, 5.0, 1.0) ASC
LIMIT ?`
	rows, err := s.db.Query(sqlText, q, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []SearchResult
	for rows.Next() {
		var r SearchResult
		if err := rows.Scan(&r.IssueID, &r.Identifier, &r.Title, &r.TeamKey,
			&r.StateName, &r.StateType, &r.UpdatedAt, &r.Snippet, &r.Score); err != nil {
			return nil, err
		}
		r.Snippet = SafeSnippet(r.Snippet, 240)
		out = append(out, r)
	}
	return out, rows.Err()
}

// SafeSnippet strips control characters and collapses internal whitespace so
// FTS5 snippets stay one line of UTF-8 text fit for terminals + JSON.
func SafeSnippet(in string, maxBytes int) string {
	if in == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(in))
	lastSpace := false
	for _, r := range in {
		switch {
		case r == '\t' || r == '\n' || r == '\r':
			if !lastSpace {
				b.WriteByte(' ')
				lastSpace = true
			}
		case r < 0x20 || r == 0x7f:
			continue
		default:
			b.WriteRune(r)
			lastSpace = r == ' '
		}
	}
	out := strings.TrimSpace(b.String())
	if maxBytes > 0 && len(out) > maxBytes {
		out = out[:maxBytes] + "…"
	}
	return out
}

// IssueRecord is the show payload: an Issue plus resolved team key, state,
// labels, and comments. It is intentionally denormalized for display and does
// not embed linear.Issue so the JSON shape is stable independent of upstream
// tag changes.
type IssueRecord struct {
	ID          string           `json:"id"`
	Identifier  string           `json:"identifier"`
	Title       string           `json:"title"`
	Description string           `json:"description"`
	TeamID      string           `json:"team_id"`
	ProjectID   string           `json:"project_id"`
	StateID     string           `json:"state_id"`
	AssigneeID  string           `json:"assignee_id"`
	CreatorID   string           `json:"creator_id"`
	Priority    int              `json:"priority"`
	CreatedAt   string           `json:"created_at"`
	UpdatedAt   string           `json:"updated_at"`
	TeamKey     string           `json:"team_key"`
	StateName   string           `json:"state_name"`
	StateType   string           `json:"state_type"`
	Labels      []string         `json:"labels"`
	Comments    []linear.Comment `json:"comments"`
}

// Show resolves an issue by either its Linear UUID or its `LIN-12` identifier
// (case-insensitive on the team key portion).
func (s *Store) Show(idOrIdentifier string) (IssueRecord, error) {
	key := strings.TrimSpace(idOrIdentifier)
	if key == "" {
		return IssueRecord{}, errors.New("show: empty id")
	}
	const sqlText = `
SELECT i.id, i.identifier, i.title, COALESCE(i.description,''),
       COALESCE(i.team_id,''), COALESCE(i.project_id,''),
       COALESCE(i.state_id,''), COALESCE(i.assignee_id,''),
       COALESCE(i.creator_id,''), i.priority,
       i.created_at, i.updated_at,
       COALESCE(t.key,''), COALESCE(w.name,''), COALESCE(w.type,'')
FROM issues i
LEFT JOIN teams t ON t.id = i.team_id
LEFT JOIN workflow_states w ON w.id = i.state_id
WHERE i.id = ?1 OR upper(i.identifier) = upper(?1)
LIMIT 1`
	var rec IssueRecord
	err := s.db.QueryRow(sqlText, key).Scan(
		&rec.ID, &rec.Identifier, &rec.Title, &rec.Description,
		&rec.TeamID, &rec.ProjectID, &rec.StateID, &rec.AssigneeID,
		&rec.CreatorID, &rec.Priority, &rec.CreatedAt, &rec.UpdatedAt,
		&rec.TeamKey, &rec.StateName, &rec.StateType,
	)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return IssueRecord{}, fmt.Errorf("show: no such issue %q", key)
		}
		return IssueRecord{}, err
	}
	labels, err := s.issueLabels(rec.ID)
	if err != nil {
		return IssueRecord{}, err
	}
	rec.Labels = labels
	comments, err := s.issueComments(rec.ID)
	if err != nil {
		return IssueRecord{}, err
	}
	rec.Comments = comments
	if rec.Labels == nil {
		rec.Labels = []string{}
	}
	if rec.Comments == nil {
		rec.Comments = []linear.Comment{}
	}
	return rec, nil
}

func (s *Store) issueLabels(issueID string) ([]string, error) {
	rows, err := s.db.Query(`
SELECT l.name FROM issue_labels il
JOIN labels l ON l.id = il.label_id
WHERE il.issue_id = ?
ORDER BY l.name`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	return out, rows.Err()
}

func (s *Store) issueComments(issueID string) ([]linear.Comment, error) {
	rows, err := s.db.Query(`
SELECT id, issue_id, COALESCE(author_id,''), body, created_at, updated_at
FROM comments WHERE issue_id = ?
ORDER BY created_at, id`, issueID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []linear.Comment
	for rows.Next() {
		var c linear.Comment
		if err := rows.Scan(&c.ID, &c.IssueID, &c.AuthorID, &c.Body, &c.CreatedAt, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) SaveCursor(scope, cursor, highWaterMark string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	entityType, entityID := splitScope(scope)
	payload, err := json.Marshal(cursorPayload{Cursor: cursor, HighWaterMark: highWaterMark})
	if err != nil {
		return err
	}
	_, err = s.db.Exec(`
INSERT INTO sync_state(source_name, entity_type, entity_id, value, updated_at)
VALUES(?,?,?,?,?)
ON CONFLICT(source_name, entity_type, entity_id) DO UPDATE SET
  value=excluded.value,
  updated_at=excluded.updated_at`, "linear", entityType, entityID, string(payload), now)
	return err
}

type cursorPayload struct {
	Cursor        string `json:"cursor"`
	HighWaterMark string `json:"high_water_mark"`
}

func splitScope(scope string) (entityType, entityID string) {
	if idx := strings.IndexByte(scope, '.'); idx >= 0 {
		return scope[:idx], scope[idx+1:]
	}
	return scope, "default"
}

type SyncState struct {
	Scope         string `json:"scope"`
	Cursor        string `json:"cursor"`
	HighWaterMark string `json:"high_water_mark"`
	UpdatedAt     string `json:"updated_at"`
}

func (s *Store) LoadCursor(scope string) (SyncState, error) {
	entityType, entityID := splitScope(scope)
	row := s.db.QueryRow(`
SELECT value, updated_at FROM sync_state
WHERE source_name = ? AND entity_type = ? AND entity_id = ?`,
		"linear", entityType, entityID)
	var value, updatedAt string
	if err := row.Scan(&value, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return SyncState{Scope: scope}, nil
		}
		return SyncState{}, err
	}
	var payload cursorPayload
	if err := json.Unmarshal([]byte(value), &payload); err != nil {
		return SyncState{}, fmt.Errorf("sync_state value: %w", err)
	}
	return SyncState{
		Scope:         scope,
		Cursor:        payload.Cursor,
		HighWaterMark: payload.HighWaterMark,
		UpdatedAt:     updatedAt,
	}, nil
}

// IngestStream reads either a single Snapshot JSON document or an NDJSON
// stream of `{"kind":"team|state|user|label|project|issue","item":{...}}`
// envelopes (the shape ExportNDJSON emits) and upserts everything found.
// Returns the count of objects ingested.
func (s *Store) IngestStream(r io.Reader, sizeCap int64) (int, error) {
	if sizeCap <= 0 {
		sizeCap = 200 << 20
	}
	capped := io.LimitReader(r, sizeCap+1)
	dec := json.NewDecoder(capped)
	var first json.RawMessage
	if err := dec.Decode(&first); err != nil {
		if err == io.EOF {
			return 0, errors.New("ingest: empty input")
		}
		return 0, fmt.Errorf("ingest: decode first object: %w", err)
	}
	var probe map[string]json.RawMessage
	if err := json.Unmarshal(first, &probe); err != nil {
		return 0, fmt.Errorf("ingest: top-level must be a JSON object: %w", err)
	}
	if _, hasKind := probe["kind"]; hasKind {
		snap, err := decodeFirstAndRemainder(first, dec)
		if err != nil {
			return 0, err
		}
		return ingestAndCount(s, snap)
	}
	var snap linear.Snapshot
	if err := json.Unmarshal(first, &snap); err != nil {
		return 0, fmt.Errorf("ingest: top-level object is neither Snapshot nor envelope: %w", err)
	}
	if err := snap.Validate(); err != nil {
		return 0, err
	}
	return ingestAndCount(s, snap)
}

func decodeFirstAndRemainder(first json.RawMessage, dec *json.Decoder) (linear.Snapshot, error) {
	var snap linear.Snapshot
	if err := appendEnvelope(&snap, first, 1); err != nil {
		return snap, err
	}
	count := 1
	for {
		var raw json.RawMessage
		if err := dec.Decode(&raw); err != nil {
			if err == io.EOF {
				return snap, nil
			}
			return snap, fmt.Errorf("ingest: envelope %d: %w", count+1, err)
		}
		count++
		if err := appendEnvelope(&snap, raw, count); err != nil {
			return snap, err
		}
	}
}

func appendEnvelope(snap *linear.Snapshot, raw json.RawMessage, count int) error {
	var env struct {
		Kind string          `json:"kind"`
		Item json.RawMessage `json:"item"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("ingest: envelope %d: %w", count, err)
	}
	switch env.Kind {
	case "team":
		var t linear.Team
		if err := json.Unmarshal(env.Item, &t); err != nil {
			return fmt.Errorf("ingest: team %d: %w", count, err)
		}
		snap.Teams = append(snap.Teams, t)
	case "state":
		var s linear.WorkflowState
		if err := json.Unmarshal(env.Item, &s); err != nil {
			return fmt.Errorf("ingest: state %d: %w", count, err)
		}
		snap.States = append(snap.States, s)
	case "user":
		var u linear.User
		if err := json.Unmarshal(env.Item, &u); err != nil {
			return fmt.Errorf("ingest: user %d: %w", count, err)
		}
		snap.Users = append(snap.Users, u)
	case "label":
		var l linear.Label
		if err := json.Unmarshal(env.Item, &l); err != nil {
			return fmt.Errorf("ingest: label %d: %w", count, err)
		}
		snap.Labels = append(snap.Labels, l)
	case "project":
		var p linear.Project
		if err := json.Unmarshal(env.Item, &p); err != nil {
			return fmt.Errorf("ingest: project %d: %w", count, err)
		}
		snap.Projects = append(snap.Projects, p)
	case "issue":
		var iss linear.Issue
		if err := json.Unmarshal(env.Item, &iss); err != nil {
			return fmt.Errorf("ingest: issue %d: %w", count, err)
		}
		snap.Issues = append(snap.Issues, iss)
	default:
		return fmt.Errorf("ingest: unknown kind %q at envelope %d", env.Kind, count)
	}
	return nil
}

func ingestAndCount(s *Store, snap linear.Snapshot) (int, error) {
	if err := s.IngestSnapshot(snap); err != nil {
		return 0, err
	}
	return len(snap.Teams) + len(snap.States) + len(snap.Users) + len(snap.Labels) + len(snap.Projects) + len(snap.Issues), nil
}

// IngestSnapshot upserts every entity from a linear.Snapshot. Idempotent;
// safe to re-run from the same fixture or a refreshed sync.
func (s *Store) IngestSnapshot(snap linear.Snapshot) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for _, t := range snap.Teams {
		if _, err := tx.Exec(`
INSERT INTO teams(id, key, name, updated_at) VALUES(?,?,?,?)
ON CONFLICT(id) DO UPDATE SET key=excluded.key, name=excluded.name, updated_at=excluded.updated_at`,
			t.ID, t.Key, t.Name, t.UpdatedAt); err != nil {
			return fmt.Errorf("teams: %w", err)
		}
	}
	for _, w := range snap.States {
		if _, err := tx.Exec(`
INSERT INTO workflow_states(id, team_id, name, type) VALUES(?,?,?,?)
ON CONFLICT(id) DO UPDATE SET team_id=excluded.team_id, name=excluded.name, type=excluded.type`,
			w.ID, w.TeamID, w.Name, w.Type); err != nil {
			return fmt.Errorf("states: %w", err)
		}
	}
	for _, u := range snap.Users {
		if _, err := tx.Exec(`
INSERT INTO users(id, name, email) VALUES(?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, email=excluded.email`,
			u.ID, u.Name, u.Email); err != nil {
			return fmt.Errorf("users: %w", err)
		}
	}
	for _, l := range snap.Labels {
		if _, err := tx.Exec(`
INSERT INTO labels(id, team_id, name) VALUES(?,?,?)
ON CONFLICT(id) DO UPDATE SET team_id=excluded.team_id, name=excluded.name`,
			l.ID, l.TeamID, l.Name); err != nil {
			return fmt.Errorf("labels: %w", err)
		}
	}
	for _, p := range snap.Projects {
		if _, err := tx.Exec(`
INSERT INTO projects(id, name, state, updated_at) VALUES(?,?,?,?)
ON CONFLICT(id) DO UPDATE SET name=excluded.name, state=excluded.state, updated_at=excluded.updated_at`,
			p.ID, p.Name, p.State, p.UpdatedAt); err != nil {
			return fmt.Errorf("projects: %w", err)
		}
	}
	for _, iss := range snap.Issues {
		if err := stubMissingRefs(tx, iss); err != nil {
			return err
		}
		newHash := issueContentHash(iss)
		var existingHash string
		err := tx.QueryRow(`SELECT content_hash FROM issues WHERE id = ?`, iss.ID).Scan(&existingHash)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("read content_hash: %w", err)
		}
		if newHash != "" && newHash == existingHash {
			continue
		}
		if _, err := tx.Exec(`
INSERT INTO issues(id, identifier, title, description, team_id, project_id,
                   state_id, assignee_id, creator_id, priority, content_hash,
                   created_at, updated_at)
VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  identifier=excluded.identifier,
  title=excluded.title,
  description=excluded.description,
  team_id=excluded.team_id,
  project_id=excluded.project_id,
  state_id=excluded.state_id,
  assignee_id=excluded.assignee_id,
  creator_id=excluded.creator_id,
  priority=excluded.priority,
  content_hash=excluded.content_hash,
  created_at=excluded.created_at,
  updated_at=excluded.updated_at`,
			iss.ID, iss.Identifier, iss.Title, iss.Description, nilIfEmpty(iss.TeamID),
			nilIfEmpty(iss.ProjectID), nilIfEmpty(iss.StateID), nilIfEmpty(iss.AssigneeID),
			nilIfEmpty(iss.CreatorID), iss.Priority, newHash, iss.CreatedAt, iss.UpdatedAt,
		); err != nil {
			return fmt.Errorf("issues: %w", err)
		}
		if _, err := tx.Exec(`DELETE FROM issue_labels WHERE issue_id = ?`, iss.ID); err != nil {
			return fmt.Errorf("issue_labels purge: %w", err)
		}
		for _, lid := range iss.LabelIDs {
			if _, err := tx.Exec(`INSERT INTO issue_labels(issue_id, label_id) VALUES(?,?)`, iss.ID, lid); err != nil {
				return fmt.Errorf("issue_labels insert: %w", err)
			}
		}
		// Purge comments before re-inserting so a refresh that drops a comment
		// upstream actually removes it locally (mirrors issue_labels).
		if _, err := tx.Exec(`DELETE FROM comments WHERE issue_id = ?`, iss.ID); err != nil {
			return fmt.Errorf("comments purge: %w", err)
		}
		for _, c := range iss.Comments {
			if _, err := tx.Exec(`
INSERT INTO comments(id, issue_id, author_id, body, created_at, updated_at)
VALUES(?,?,?,?,?,?)
ON CONFLICT(id) DO UPDATE SET
  issue_id=excluded.issue_id,
  author_id=excluded.author_id,
  body=excluded.body,
  created_at=excluded.created_at,
  updated_at=excluded.updated_at`,
				c.ID, iss.ID, nilIfEmpty(c.AuthorID), c.Body, c.CreatedAt, c.UpdatedAt); err != nil {
				return fmt.Errorf("comments: %w", err)
			}
		}
		if err := rebuildFTSForIssue(tx, iss.ID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func issueContentHash(iss linear.Issue) string {
	h := sha256.New()
	io.WriteString(h, iss.Identifier)
	h.Write([]byte{0})
	io.WriteString(h, iss.Title)
	h.Write([]byte{0})
	io.WriteString(h, iss.Description)
	h.Write([]byte{0})
	io.WriteString(h, iss.TeamID)
	h.Write([]byte{0})
	io.WriteString(h, iss.ProjectID)
	h.Write([]byte{0})
	io.WriteString(h, iss.StateID)
	h.Write([]byte{0})
	io.WriteString(h, iss.AssigneeID)
	h.Write([]byte{0})
	io.WriteString(h, iss.CreatorID)
	h.Write([]byte{0})
	fmt.Fprintf(h, "p=%d", iss.Priority)
	h.Write([]byte{0})
	for _, lid := range iss.LabelIDs {
		io.WriteString(h, lid)
		h.Write([]byte{1})
	}
	h.Write([]byte{0})
	for _, c := range iss.Comments {
		io.WriteString(h, c.ID)
		h.Write([]byte{1})
		io.WriteString(h, c.AuthorID)
		h.Write([]byte{1})
		io.WriteString(h, c.Body)
		h.Write([]byte{1})
		io.WriteString(h, c.UpdatedAt)
		h.Write([]byte{2})
	}
	return fmt.Sprintf("%x", h.Sum(nil))
}

// StoreRawBlob persists a raw provider payload keyed by sha256 for replay
// and future schema migrations. Duplicates are no-ops.
func (s *Store) StoreRawBlob(kind, entityID string, payload []byte) error {
	if len(payload) == 0 {
		return nil
	}
	sum := sha256.Sum256(payload)
	_, err := s.db.Exec(`
INSERT INTO raw_blobs(sha256, kind, entity_id, payload, ingested_at)
VALUES(?,?,?,?,?)
ON CONFLICT(sha256) DO NOTHING`,
		fmt.Sprintf("%x", sum[:]), kind, entityID, string(payload), time.Now().UTC().Format(time.RFC3339Nano))
	return err
}

func stubMissingRefs(tx *sql.Tx, iss linear.Issue) error {
	if iss.TeamID != "" {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO teams(id, key, name) VALUES(?, '', '')`, iss.TeamID); err != nil {
			return fmt.Errorf("stub team: %w", err)
		}
	}
	if iss.ProjectID != "" {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO projects(id, name) VALUES(?, '')`, iss.ProjectID); err != nil {
			return fmt.Errorf("stub project: %w", err)
		}
	}
	if iss.StateID != "" {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO workflow_states(id, name, type) VALUES(?, '', '')`, iss.StateID); err != nil {
			return fmt.Errorf("stub state: %w", err)
		}
	}
	for _, uid := range []string{iss.AssigneeID, iss.CreatorID} {
		if uid == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO users(id, name) VALUES(?, '')`, uid); err != nil {
			return fmt.Errorf("stub user: %w", err)
		}
	}
	for _, lid := range iss.LabelIDs {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO labels(id, name) VALUES(?, '')`, lid); err != nil {
			return fmt.Errorf("stub label: %w", err)
		}
	}
	for _, c := range iss.Comments {
		if c.AuthorID == "" {
			continue
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO users(id, name) VALUES(?, '')`, c.AuthorID); err != nil {
			return fmt.Errorf("stub comment author: %w", err)
		}
	}
	return nil
}

func rebuildFTSForIssue(tx *sql.Tx, issueID string) error {
	if _, err := tx.Exec(`DELETE FROM issue_fts WHERE rowid = (SELECT rowid FROM issues WHERE id = ?)`, issueID); err != nil {
		return fmt.Errorf("fts purge: %w", err)
	}
	if _, err := tx.Exec(`
INSERT INTO issue_fts(rowid, identifier, title, description, comments)
SELECT i.rowid, i.identifier, i.title, COALESCE(i.description,''),
       COALESCE((SELECT group_concat(body, ' ') FROM comments WHERE issue_id = i.id),'')
FROM issues i WHERE i.id = ?`, issueID); err != nil {
		return fmt.Errorf("fts insert: %w", err)
	}
	return nil
}

func nilIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

const schemaSQL = `
CREATE TABLE IF NOT EXISTS teams (
  id TEXT PRIMARY KEY,
  key TEXT NOT NULL,
  name TEXT NOT NULL,
  updated_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS workflow_states (
  id TEXT PRIMARY KEY,
  team_id TEXT,
  name TEXT NOT NULL,
  type TEXT NOT NULL,
  FOREIGN KEY(team_id) REFERENCES teams(id)
);

CREATE TABLE IF NOT EXISTS users (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  email TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS labels (
  id TEXT PRIMARY KEY,
  team_id TEXT,
  name TEXT NOT NULL,
  FOREIGN KEY(team_id) REFERENCES teams(id)
);

CREATE TABLE IF NOT EXISTS projects (
  id TEXT PRIMARY KEY,
  name TEXT NOT NULL,
  state TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS issues (
  id TEXT PRIMARY KEY,
  identifier TEXT NOT NULL UNIQUE,
  title TEXT NOT NULL,
  description TEXT,
  team_id TEXT,
  project_id TEXT,
  state_id TEXT,
  assignee_id TEXT,
  creator_id TEXT,
  priority INTEGER NOT NULL DEFAULT 0,
  content_hash TEXT NOT NULL DEFAULT '',
  created_at TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL DEFAULT '',
  FOREIGN KEY(team_id) REFERENCES teams(id),
  FOREIGN KEY(project_id) REFERENCES projects(id),
  FOREIGN KEY(state_id) REFERENCES workflow_states(id),
  FOREIGN KEY(assignee_id) REFERENCES users(id),
  FOREIGN KEY(creator_id) REFERENCES users(id)
);

CREATE INDEX IF NOT EXISTS issues_updated_at_idx ON issues(updated_at);
CREATE INDEX IF NOT EXISTS issues_team_idx ON issues(team_id);
CREATE INDEX IF NOT EXISTS issues_team_updated_idx ON issues(team_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS issues_state_updated_idx ON issues(state_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS issues_assignee_updated_idx ON issues(assignee_id, updated_at DESC);
CREATE INDEX IF NOT EXISTS issues_project_updated_idx ON issues(project_id, updated_at DESC);

CREATE TABLE IF NOT EXISTS issue_labels (
  issue_id TEXT NOT NULL,
  label_id TEXT NOT NULL,
  PRIMARY KEY(issue_id, label_id),
  FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE,
  FOREIGN KEY(label_id) REFERENCES labels(id) ON DELETE CASCADE
);

CREATE TABLE IF NOT EXISTS comments (
  id TEXT PRIMARY KEY,
  issue_id TEXT NOT NULL,
  author_id TEXT,
  body TEXT NOT NULL,
  created_at TEXT NOT NULL DEFAULT '',
  updated_at TEXT NOT NULL DEFAULT '',
  FOREIGN KEY(issue_id) REFERENCES issues(id) ON DELETE CASCADE,
  FOREIGN KEY(author_id) REFERENCES users(id)
);

CREATE INDEX IF NOT EXISTS comments_issue_idx ON comments(issue_id);
CREATE INDEX IF NOT EXISTS comments_issue_created_idx ON comments(issue_id, created_at, id);

CREATE TABLE IF NOT EXISTS raw_blobs (
  sha256 TEXT PRIMARY KEY,
  kind TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  payload TEXT NOT NULL,
  ingested_at TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS raw_blobs_entity_idx ON raw_blobs(kind, entity_id);

CREATE VIRTUAL TABLE IF NOT EXISTS issue_fts USING fts5 (
  identifier, title, description, comments,
  tokenize='unicode61 remove_diacritics 2'
);

CREATE TABLE IF NOT EXISTS sync_state (
  source_name TEXT NOT NULL,
  entity_type TEXT NOT NULL,
  entity_id TEXT NOT NULL,
  value TEXT NOT NULL,
  updated_at TEXT NOT NULL,
  PRIMARY KEY (source_name, entity_type, entity_id)
);
CREATE INDEX IF NOT EXISTS idx_sync_state_updated_at ON sync_state(updated_at DESC);
`
