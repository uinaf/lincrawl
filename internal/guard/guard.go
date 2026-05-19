// Package guard scans the working tree for tenant data, plaintext
// archives, secret-looking values, and real provider artifacts. Invoked by
// `lincrawl guard --json` before commit and from scripts/verify.
package guard

import (
	"bufio"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

type Finding struct {
	Path   string `json:"path"`
	Reason string `json:"reason"`
}

type Result struct {
	OK       bool      `json:"ok"`
	Scanned  int       `json:"scanned"`
	Findings []Finding `json:"findings"`
}

var (
	bearerLike     = regexp.MustCompile(`(?i)(api[_-]?key|access[_-]?token|secret|bearer)\s*[=:]\s*['"]?[A-Za-z0-9_\-./+]{20,}`)
	linApiToken    = regexp.MustCompile(`lin_api_[A-Za-z0-9_-]{20,}`)
	githubToken    = regexp.MustCompile(`(ghp_|github_pat_)[A-Za-z0-9_-]{20,}`)
	opRefLive      = regexp.MustCompile(`op://[A-Za-z0-9._-]+/[A-Za-z0-9._-]+/[A-Za-z0-9._-]+`)
	opRefTemplate  = regexp.MustCompile(`\{\{\s*op://[^}]*\}\}`)
	linearURL      = regexp.MustCompile(`https://linear\.app/[A-Za-z0-9_-]+/issue/`)
	linearUUIDHint = regexp.MustCompile(`"(workspaceId|teamId|issueId|organizationId)"\s*:\s*"[0-9a-fA-F-]{36}"`)
)

var (
	forbiddenSuffixes = []string{".jsonl", ".jsonl.zst", ".jsonl.zst.age", ".tar", ".tar.zst", ".tar.zst.age", ".db", ".db-wal", ".db-shm", ".sqlite", ".log", ".har"}
	forbiddenDirs     = map[string]bool{"snapshots": true, "reports": true, "screenshots": true, "logs": true, "transcripts": true}
)

const maxScanBytes = 2 << 20 // 2 MiB

// Run walks the working tree from root, scanning every tracked-style file
// (skips .git, node_modules, vendor, etc.) for committable tenant data.
// When `root` is a git checkout, the scan honors .gitignore so untracked
// scratch files (e.g. .env.local) don't trip the guard.
func Run(root string) (Result, error) {
	var res Result
	res.OK = true
	if root == "" {
		root = "."
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		return res, err
	}
	ignored := gitIgnoredSet(abs)
	err = filepath.WalkDir(abs, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			name := d.Name()
			if name == ".git" || name == "node_modules" || name == "vendor" || name == "tmp" || name == "dist" {
				return filepath.SkipDir
			}
			rel, _ := filepath.Rel(abs, path)
			parts := strings.Split(rel, string(filepath.Separator))
			for _, p := range parts {
				if forbiddenDirs[p] {
					res.OK = false
					res.Findings = append(res.Findings, Finding{Path: rel, Reason: fmt.Sprintf("forbidden directory %q", p)})
					return filepath.SkipDir
				}
			}
			return nil
		}
		rel, _ := filepath.Rel(abs, path)
		if rel == "" || rel == "." {
			return nil
		}
		if ignored[rel] {
			return nil
		}
		base := filepath.Base(rel)

		// Env files: only .env.example / .env.local.example may be tracked.
		if strings.HasPrefix(base, ".env") && base != ".env.example" && base != ".env.local.example" {
			res.OK = false
			res.Findings = append(res.Findings, Finding{Path: rel, Reason: "env file with secrets must not be committed"})
			return nil
		}

		for _, suf := range forbiddenSuffixes {
			if strings.HasSuffix(base, suf) {
				res.OK = false
				res.Findings = append(res.Findings, Finding{Path: rel, Reason: fmt.Sprintf("forbidden artifact suffix %q", suf)})
				return nil
			}
		}

		info, err := d.Info()
		if err != nil {
			return nil
		}
		res.Scanned++
		if info.Size() > maxScanBytes {
			return nil
		}
		if err := scanContent(path, rel, &res); err != nil {
			if errors.Is(err, fs.ErrPermission) {
				return nil
			}
			return err
		}
		return nil
	})
	if err != nil {
		return res, err
	}
	sort.Slice(res.Findings, func(i, j int) bool {
		if res.Findings[i].Path == res.Findings[j].Path {
			return res.Findings[i].Reason < res.Findings[j].Reason
		}
		return res.Findings[i].Path < res.Findings[j].Path
	})
	dedup := res.Findings[:0]
	for i, f := range res.Findings {
		if i > 0 && res.Findings[i-1] == f {
			continue
		}
		dedup = append(dedup, f)
	}
	res.Findings = dedup
	return res, nil
}

func scanContent(path, rel string, res *Result) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	for scanner.Scan() {
		line := scanner.Text()
		if linApiToken.MatchString(line) {
			addFinding(res, rel, "tracked Linear API token shape")
		}
		if githubToken.MatchString(line) {
			addFinding(res, rel, "tracked GitHub token shape")
		}
		if bearerLike.MatchString(line) {
			addFinding(res, rel, "tracked bearer-like value")
		}
		if linearURL.MatchString(line) {
			addFinding(res, rel, "tracked Linear issue URL")
		}
		if !strings.HasPrefix(rel, "testdata/synthetic/") && linearUUIDHint.MatchString(line) {
			addFinding(res, rel, "tracked Linear UUID outside synthetic fixtures")
		}
		if opRefLive.MatchString(line) && !opRefTemplate.MatchString(line) {
			if base := filepath.Base(rel); base != ".env.example" && base != ".env.local.example" && !strings.HasPrefix(rel, "docs/") {
				addFinding(res, rel, "real op:// reference outside .env.example/docs")
			}
		}
	}
	return scanner.Err()
}

func addFinding(res *Result, path, reason string) {
	res.OK = false
	res.Findings = append(res.Findings, Finding{Path: path, Reason: reason})
}

// gitIgnoredSet returns a set of paths (relative to abs) that .gitignore
// would exclude. Empty set if abs is not a git checkout or git is missing.
func gitIgnoredSet(abs string) map[string]bool {
	out := map[string]bool{}
	if _, err := os.Stat(filepath.Join(abs, ".git")); err != nil {
		return out
	}
	cmd := exec.Command("git", "ls-files", "--others", "--ignored", "--exclude-standard", "-z")
	cmd.Dir = abs
	data, err := cmd.Output()
	if err != nil {
		return out
	}
	for _, p := range strings.Split(string(data), "\x00") {
		if p != "" {
			out[p] = true
		}
	}
	return out
}
