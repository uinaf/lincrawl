// Package tenantstore verifies a generic encrypted-snapshot store on disk:
// a manifest.json plus age-encrypted snapshot artifacts. Used by both
// `lincrawl store verify <path>` and `lincrawl subscribe <path>`.
package tenantstore

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

const ManifestName = "manifest.json"

// AcceptedSchemaVersions is the allowlist of manifest schema versions
// this binary understands. Verify rejects everything else.
var AcceptedSchemaVersions = []string{
	"lincrawl.store.v1",
}

// Manifest is the on-disk store manifest.
type Manifest struct {
	SchemaVersion               string     `json:"schema_version"`
	OverlapSeconds              int        `json:"overlap_seconds,omitempty"`
	LastSuccessfulHighWaterMark string     `json:"last_successful_high_water_mark,omitempty"`
	Snapshots                   []Snapshot `json:"snapshots"`
}

type Snapshot struct {
	Kind          string `json:"kind"`
	Path          string `json:"path"`
	SHA256        string `json:"sha256,omitempty"`
	Bytes         int64  `json:"bytes,omitempty"`
	Records       int    `json:"records,omitempty"`
	CreatedAt     string `json:"created_at,omitempty"`
	HighWaterMark string `json:"high_water_mark,omitempty"`
}

type Result struct {
	OK        bool       `json:"ok"`
	Root      string     `json:"root"`
	Manifest  *Manifest  `json:"manifest,omitempty"`
	Snapshots []Snapshot `json:"snapshots,omitempty"`
	Findings  []string   `json:"findings,omitempty"`
}

var (
	allowedExtensions = []string{".jsonl.zst.age", ".tar.zst.age"}
	forbiddenSuffixes = []string{".jsonl", ".jsonl.zst", ".tar", ".tar.zst", ".log", ".har", ".db", ".db-wal", ".db-shm", ".sqlite"}
	forbiddenDirs     = map[string]bool{"logs": true, "reports": true, "screenshots": true, "transcripts": true}
	ignoredScratch    = map[string]bool{"state": true, "tmp": true, ".git": true}
)

// Verify reads manifest.json from root, validates that every listed
// snapshot points to an existing encrypted artifact under the canonical
// `artifacts/snapshots/{full,delta}/YYYY/MM/` layout, and rejects
// plaintext archives, runtime state files, symlinks, and disallowed
// directories under tracked paths.
func Verify(root string) (Result, error) {
	var res Result
	res.OK = true
	res.Root = root
	manifestPath := filepath.Join(root, ManifestName)
	info, err := os.Lstat(manifestPath)
	if err != nil {
		return res, fmt.Errorf("read manifest: %w", err)
	}
	if info.Mode()&os.ModeSymlink != 0 {
		return Result{Root: root, Findings: []string{"manifest.json is a symlink"}}, nil
	}
	raw, err := os.ReadFile(manifestPath)
	if err != nil {
		return res, fmt.Errorf("read manifest: %w", err)
	}
	var manifest Manifest
	if err := json.Unmarshal(raw, &manifest); err != nil {
		return res, fmt.Errorf("decode manifest: %w", err)
	}
	res.Manifest = &manifest

	if !schemaVersionAccepted(manifest.SchemaVersion) {
		res.OK = false
		res.Findings = append(res.Findings, fmt.Sprintf("manifest schema_version %q is not in the accepted set %v",
			manifest.SchemaVersion, AcceptedSchemaVersions))
	}

	if len(manifest.Snapshots) == 0 {
		res.Findings = append(res.Findings, "manifest has zero snapshots; nothing to subscribe to yet")
	}

	// Snapshot path checks.
	seen := map[string]bool{}
	for _, snap := range manifest.Snapshots {
		if snap.Path == "" {
			res.OK = false
			res.Findings = append(res.Findings, "manifest snapshot has empty path")
			continue
		}
		if seen[snap.Path] {
			res.OK = false
			res.Findings = append(res.Findings, fmt.Sprintf("duplicate snapshot path: %s", snap.Path))
			continue
		}
		seen[snap.Path] = true
		if !validSnapshotPath(snap.Path) {
			res.OK = false
			res.Findings = append(res.Findings, fmt.Sprintf("snapshot %q is outside the canonical artifacts layout", snap.Path))
			continue
		}
		abs := filepath.Join(root, snap.Path)
		fi, err := os.Lstat(abs)
		if err != nil {
			res.OK = false
			res.Findings = append(res.Findings, fmt.Sprintf("snapshot %q is listed but missing: %v", snap.Path, err))
			continue
		}
		if fi.Mode()&os.ModeSymlink != 0 {
			res.OK = false
			res.Findings = append(res.Findings, fmt.Sprintf("snapshot %q is a symlink", snap.Path))
			continue
		}
		if fi.Size() == 0 {
			res.OK = false
			res.Findings = append(res.Findings, fmt.Sprintf("snapshot %q is empty", snap.Path))
			continue
		}
		res.Snapshots = append(res.Snapshots, snap)
	}

	// Tree walk: reject forbidden artifacts / dirs / symlinks anywhere.
	skipScratch := isGitCheckout(root)
	err = filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if path == root {
			return nil
		}
		rel, _ := filepath.Rel(root, path)
		if d.IsDir() {
			name := d.Name()
			if skipScratch && ignoredScratch[name] {
				return filepath.SkipDir
			}
			if forbiddenDirs[name] {
				res.OK = false
				res.Findings = append(res.Findings, fmt.Sprintf("forbidden directory: %s", rel))
				return filepath.SkipDir
			}
			return nil
		}
		base := d.Name()
		// Skip files under ignored scratch paths.
		if skipScratch {
			parts := strings.Split(rel, string(filepath.Separator))
			for _, p := range parts {
				if ignoredScratch[p] {
					return nil
				}
			}
		}
		info, err := d.Info()
		if err != nil {
			return nil
		}
		if info.Mode()&os.ModeSymlink != 0 {
			res.OK = false
			res.Findings = append(res.Findings, fmt.Sprintf("symlink not allowed: %s", rel))
			return nil
		}
		// Plaintext / runtime-state suffix block.
		for _, suf := range forbiddenSuffixes {
			if strings.HasSuffix(base, suf) {
				res.OK = false
				res.Findings = append(res.Findings, fmt.Sprintf("forbidden artifact suffix %q: %s", suf, rel))
				return nil
			}
		}
		// Artifact extension check: only allow expected encrypted shapes
		// under artifacts/snapshots/.
		if strings.HasPrefix(rel, "artifacts/snapshots"+string(filepath.Separator)) || strings.HasPrefix(rel, "artifacts/snapshots/") {
			if base == ".gitkeep" {
				return nil
			}
			ok := false
			for _, ext := range allowedExtensions {
				if strings.HasSuffix(base, ext) {
					ok = true
					break
				}
			}
			if !ok {
				res.OK = false
				res.Findings = append(res.Findings, fmt.Sprintf("snapshot artifact %q does not match an allowed encrypted extension", rel))
				return nil
			}
			if !validSnapshotPath(filepath.ToSlash(rel)) {
				res.OK = false
				res.Findings = append(res.Findings, fmt.Sprintf("snapshot artifact %q is outside the canonical layout", rel))
				return nil
			}
		}
		return nil
	})
	if err != nil {
		return res, err
	}

	sort.Strings(res.Findings)
	return res, nil
}

// VerifiedSnapshots returns the manifest snapshots in the order they
// appeared, each with its absolute path resolved against root.
type VerifiedSnapshot struct {
	Snapshot
	FullPath string `json:"full_path"`
}

func VerifiedSnapshots(root string) ([]VerifiedSnapshot, error) {
	res, err := Verify(root)
	if err != nil {
		return nil, err
	}
	if !res.OK {
		return nil, fmt.Errorf("store verify failed: %s", strings.Join(res.Findings, "; "))
	}
	out := make([]VerifiedSnapshot, 0, len(res.Snapshots))
	for _, snap := range res.Snapshots {
		out = append(out, VerifiedSnapshot{Snapshot: snap, FullPath: filepath.Join(root, snap.Path)})
	}
	return out, nil
}

// validSnapshotPath enforces artifacts/snapshots/<full|delta>/YYYY/MM/<file>.
func validSnapshotPath(p string) bool {
	p = filepath.ToSlash(p)
	parts := strings.Split(p, "/")
	if len(parts) != 6 {
		return false
	}
	if parts[0] != "artifacts" || parts[1] != "snapshots" {
		return false
	}
	if parts[2] != "full" && parts[2] != "delta" {
		return false
	}
	if !isYear(parts[3]) || !isMonth(parts[4]) {
		return false
	}
	for _, ext := range allowedExtensions {
		if strings.HasSuffix(parts[5], ext) {
			return true
		}
	}
	return false
}

func isYear(s string) bool {
	if len(s) != 4 {
		return false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func isMonth(s string) bool {
	if len(s) != 2 {
		return false
	}
	return s >= "01" && s <= "12"
}

func isGitCheckout(root string) bool {
	_, err := os.Stat(filepath.Join(root, ".git"))
	return err == nil
}

func schemaVersionAccepted(v string) bool {
	for _, ok := range AcceptedSchemaVersions {
		if v == ok {
			return true
		}
	}
	return false
}

// SuggestSnapshotPath returns the canonical artifacts/snapshots/<kind>/
// YYYY/MM/<filename> path for a new snapshot. Callers join with the
// store root to get the absolute path.
func SuggestSnapshotPath(kind, filename string) (string, error) {
	if kind != "full" && kind != "delta" {
		return "", errors.New("kind must be full or delta")
	}
	// filename includes year/month somewhere in its timestamp; pull
	// them from the filename if present, else fall back to current.
	year, month := extractYearMonth(filename)
	return filepath.ToSlash(filepath.Join("artifacts", "snapshots", kind, year, month, filename)), nil
}

func extractYearMonth(filename string) (string, string) {
	// Look for a YYYYMMDD or YYYYMMDDTHHMMSSZ token; fall back to 2026/01.
	for i := 0; i+8 <= len(filename); i++ {
		if isYear(filename[i:i+4]) && isMonth(filename[i+4:i+6]) {
			return filename[i : i+4], filename[i+4 : i+6]
		}
	}
	return "2026", "01"
}
