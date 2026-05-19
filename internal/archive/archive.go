// Package archive owns the canonical lincrawl archive shape: a sorted
// NDJSON stream of typed records, compressed with zstd and encrypted with
// age. Matches the fincrawl artifact shape (`*.jsonl.zst.age`) with
// lincrawl record types.
package archive

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"strings"
	"time"

	"filippo.io/age"
	"filippo.io/age/agessh"
	"github.com/klauspost/compress/zstd"

	"github.com/uinaf/lincrawl/internal/linear"
)

const SchemaVersion = "lincrawl.archive.v1"

type Record struct {
	SchemaVersion string `json:"schema_version"`
	RecordType    string `json:"record_type"`
	ID            string `json:"id"`
	Identifier    string `json:"identifier,omitempty"`
	Key           string `json:"key,omitempty"`
	Name          string `json:"name,omitempty"`
	Email         string `json:"email,omitempty"`
	Title         string `json:"title,omitempty"`
	Description   string `json:"description,omitempty"`
	Body          string `json:"body,omitempty"`
	State         string `json:"state,omitempty"`
	Type          string `json:"type,omitempty"`
	TeamID        string `json:"team_id,omitempty"`
	ProjectID     string `json:"project_id,omitempty"`
	StateID       string `json:"state_id,omitempty"`
	AssigneeID    string `json:"assignee_id,omitempty"`
	CreatorID     string `json:"creator_id,omitempty"`
	AuthorID      string `json:"author_id,omitempty"`
	IssueID       string `json:"issue_id,omitempty"`
	// Priority is serialized without omitempty so 0 ("No priority" in
	// Linear) is preserved on the wire.
	Priority  int      `json:"priority"`
	LabelIDs  []string `json:"label_ids,omitempty"`
	CreatedAt string   `json:"created_at,omitempty"`
	UpdatedAt string   `json:"updated_at,omitempty"`
}

// SnapshotRecords converts a linear.Snapshot into the canonical, sorted
// record list ready for serialization. The output is deterministic:
// re-archiving the same Snapshot produces the same bytes.
func SnapshotRecords(snap linear.Snapshot) []Record {
	var records []Record
	for _, t := range snap.Teams {
		records = append(records, Record{
			SchemaVersion: SchemaVersion,
			RecordType:    "team",
			ID:            t.ID,
			Key:           t.Key,
			Name:          t.Name,
			UpdatedAt:     normalizeTime(t.UpdatedAt),
		})
	}
	for _, s := range snap.States {
		records = append(records, Record{
			SchemaVersion: SchemaVersion,
			RecordType:    "state",
			ID:            s.ID,
			TeamID:        s.TeamID,
			Name:          s.Name,
			Type:          s.Type,
		})
	}
	for _, u := range snap.Users {
		records = append(records, Record{
			SchemaVersion: SchemaVersion,
			RecordType:    "user",
			ID:            u.ID,
			Name:          u.Name,
			Email:         u.Email,
		})
	}
	for _, l := range snap.Labels {
		records = append(records, Record{
			SchemaVersion: SchemaVersion,
			RecordType:    "label",
			ID:            l.ID,
			TeamID:        l.TeamID,
			Name:          l.Name,
		})
	}
	for _, p := range snap.Projects {
		records = append(records, Record{
			SchemaVersion: SchemaVersion,
			RecordType:    "project",
			ID:            p.ID,
			Name:          p.Name,
			State:         p.State,
			UpdatedAt:     normalizeTime(p.UpdatedAt),
		})
	}
	for _, iss := range snap.Issues {
		records = append(records, Record{
			SchemaVersion: SchemaVersion,
			RecordType:    "issue",
			ID:            iss.ID,
			Identifier:    iss.Identifier,
			Title:         iss.Title,
			Description:   iss.Description,
			TeamID:        iss.TeamID,
			ProjectID:     iss.ProjectID,
			StateID:       iss.StateID,
			AssigneeID:    iss.AssigneeID,
			CreatorID:     iss.CreatorID,
			Priority:      iss.Priority,
			LabelIDs:      sortedStrings(iss.LabelIDs),
			CreatedAt:     normalizeTime(iss.CreatedAt),
			UpdatedAt:     normalizeTime(iss.UpdatedAt),
		})
		for _, c := range iss.Comments {
			records = append(records, Record{
				SchemaVersion: SchemaVersion,
				RecordType:    "comment",
				ID:            c.ID,
				IssueID:       iss.ID,
				AuthorID:      c.AuthorID,
				Body:          c.Body,
				CreatedAt:     normalizeTime(c.CreatedAt),
				UpdatedAt:     normalizeTime(c.UpdatedAt),
			})
		}
	}
	sortRecords(records)
	return records
}

// RecordsSnapshot is the inverse: parse a record stream back into a
// linear.Snapshot. Records may arrive in any order; comments are buffered
// and attached after their parent issues are registered.
func RecordsSnapshot(records []Record) (linear.Snapshot, error) {
	var snap linear.Snapshot
	issuesByID := map[string]int{}
	var pendingComments []Record
	for _, r := range records {
		if r.SchemaVersion != SchemaVersion {
			return linear.Snapshot{}, fmt.Errorf("unsupported schema version %q", r.SchemaVersion)
		}
		switch r.RecordType {
		case "team":
			snap.Teams = append(snap.Teams, linear.Team{ID: r.ID, Key: r.Key, Name: r.Name, UpdatedAt: r.UpdatedAt})
		case "state":
			snap.States = append(snap.States, linear.WorkflowState{ID: r.ID, TeamID: r.TeamID, Name: r.Name, Type: r.Type})
		case "user":
			snap.Users = append(snap.Users, linear.User{ID: r.ID, Name: r.Name, Email: r.Email})
		case "label":
			snap.Labels = append(snap.Labels, linear.Label{ID: r.ID, TeamID: r.TeamID, Name: r.Name})
		case "project":
			snap.Projects = append(snap.Projects, linear.Project{ID: r.ID, Name: r.Name, State: r.State, UpdatedAt: r.UpdatedAt})
		case "issue":
			snap.Issues = append(snap.Issues, linear.Issue{
				ID:          r.ID,
				Identifier:  r.Identifier,
				Title:       r.Title,
				Description: r.Description,
				TeamID:      r.TeamID,
				ProjectID:   r.ProjectID,
				StateID:     r.StateID,
				AssigneeID:  r.AssigneeID,
				CreatorID:   r.CreatorID,
				Priority:    r.Priority,
				LabelIDs:    sortedStrings(r.LabelIDs),
				CreatedAt:   r.CreatedAt,
				UpdatedAt:   r.UpdatedAt,
			})
			issuesByID[r.ID] = len(snap.Issues) - 1
		case "comment":
			pendingComments = append(pendingComments, r)
		default:
			return linear.Snapshot{}, fmt.Errorf("unsupported record type %q", r.RecordType)
		}
	}
	for _, r := range pendingComments {
		idx, ok := issuesByID[r.IssueID]
		if !ok {
			return linear.Snapshot{}, fmt.Errorf("comment %s references unknown issue %s", r.ID, r.IssueID)
		}
		snap.Issues[idx].Comments = append(snap.Issues[idx].Comments, linear.Comment{
			ID: r.ID, IssueID: r.IssueID, AuthorID: r.AuthorID, Body: r.Body,
			CreatedAt: r.CreatedAt, UpdatedAt: r.UpdatedAt,
		})
	}
	return snap, nil
}

func sortRecords(records []Record) {
	sort.SliceStable(records, func(i, j int) bool {
		l, r := recordSortKey(records[i]), recordSortKey(records[j])
		for idx := range l {
			if l[idx] == r[idx] {
				continue
			}
			return l[idx] < r[idx]
		}
		return false
	})
}

func recordSortKey(r Record) [5]string {
	identifier := r.Identifier
	if identifier == "" {
		identifier = r.ID
	}
	return [5]string{r.RecordType, identifier, r.UpdatedAt, r.CreatedAt, r.ID}
}

func WriteJSONL(w io.Writer, records []Record) error {
	enc := json.NewEncoder(w)
	enc.SetEscapeHTML(false)
	for _, record := range records {
		if err := enc.Encode(record); err != nil {
			return err
		}
	}
	return nil
}

func JSONLBytes(records []Record) ([]byte, error) {
	var buf bytes.Buffer
	if err := WriteJSONL(&buf, records); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func ReadJSONL(r io.Reader) ([]Record, error) {
	dec := json.NewDecoder(r)
	var records []Record
	lineNo := 0
	for {
		lineNo++
		var rec Record
		if err := dec.Decode(&rec); err != nil {
			if err == io.EOF {
				break
			}
			return nil, fmt.Errorf("decode JSONL line %d: %w", lineNo, err)
		}
		if rec.SchemaVersion != SchemaVersion {
			return nil, fmt.Errorf("decode JSONL line %d: unsupported schema version %q", lineNo, rec.SchemaVersion)
		}
		records = append(records, rec)
	}
	return records, nil
}

// WriteEncryptedJSONL streams records as JSONL through zstd compression
// and age encryption to path. The output file is created with O_EXCL|0o600
// so existing artifacts are never silently clobbered. On any error after
// the file is opened the partial artifact is removed so a retry with
// the same --out path succeeds.
func WriteEncryptedJSONL(path, recipientText string, records []Record) (retErr error) {
	recipient, err := ParseRecipient(recipientText)
	if err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create archive: %w", err)
	}
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(path)
		}
	}()
	ageWriter, err := age.Encrypt(file, recipient)
	if err != nil {
		_ = file.Close()
		return fmt.Errorf("create age writer: %w", err)
	}
	zstdWriter, err := zstd.NewWriter(ageWriter)
	if err != nil {
		_ = ageWriter.Close()
		_ = file.Close()
		return fmt.Errorf("create zstd writer: %w", err)
	}
	if err := WriteJSONL(zstdWriter, records); err != nil {
		_ = zstdWriter.Close()
		_ = ageWriter.Close()
		_ = file.Close()
		return err
	}
	if err := zstdWriter.Close(); err != nil {
		_ = ageWriter.Close()
		_ = file.Close()
		return fmt.Errorf("close zstd writer: %w", err)
	}
	if err := ageWriter.Close(); err != nil {
		_ = file.Close()
		return fmt.Errorf("close age writer: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync archive: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close archive: %w", err)
	}
	cleanup = false
	return nil
}

func ReadEncryptedJSONL(path, identityText string) ([]Record, error) {
	identities, err := ParseIdentities(identityText)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open archive: %w", err)
	}
	defer file.Close()
	ageReader, err := age.Decrypt(file, identities...)
	if err != nil {
		return nil, fmt.Errorf("decrypt archive: %w", err)
	}
	zstdReader, err := zstd.NewReader(ageReader)
	if err != nil {
		return nil, fmt.Errorf("create zstd reader: %w", err)
	}
	defer zstdReader.Close()
	return ReadJSONL(zstdReader)
}

func ParseRecipient(recipientText string) (age.Recipient, error) {
	recipientText = strings.TrimSpace(recipientText)
	if recipientText == "" {
		return nil, fmt.Errorf("age recipient is required")
	}
	if strings.HasPrefix(recipientText, "age1") {
		recipient, err := age.ParseX25519Recipient(recipientText)
		if err != nil {
			return nil, fmt.Errorf("parse age recipient: %w", err)
		}
		return recipient, nil
	}
	if strings.HasPrefix(recipientText, "ssh-") {
		recipient, err := agessh.ParseRecipient(recipientText)
		if err != nil {
			return nil, fmt.Errorf("parse ssh recipient: %w", err)
		}
		return recipient, nil
	}
	return nil, fmt.Errorf("unsupported age recipient format")
}

func ParseIdentities(identityText string) ([]age.Identity, error) {
	identityText = strings.TrimSpace(identityText)
	if identityText == "" {
		return nil, fmt.Errorf("age identity is required")
	}
	if strings.HasPrefix(identityText, "-----BEGIN") {
		identity, err := agessh.ParseIdentity([]byte(identityText))
		if err != nil {
			return nil, fmt.Errorf("parse ssh identity: %w", err)
		}
		return []age.Identity{identity}, nil
	}
	identities, err := age.ParseIdentities(strings.NewReader(identityText))
	if err != nil {
		return nil, fmt.Errorf("parse age identity: %w", err)
	}
	return identities, nil
}

func sortedStrings(values []string) []string {
	out := append([]string(nil), values...)
	sort.Strings(out)
	return out
}

func normalizeTime(value string) string {
	parsed, err := time.Parse(time.RFC3339, value)
	if err != nil {
		return value
	}
	return parsed.UTC().Format(time.RFC3339)
}
