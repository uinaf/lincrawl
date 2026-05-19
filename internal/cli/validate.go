package cli

import (
	"fmt"
	"path/filepath"
	"regexp"
	"strings"
)

var (
	identifierRE  = regexp.MustCompile(`^[A-Z0-9]{1,16}-[0-9]{1,9}$`)
	uuidRE        = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	controlCharRE = regexp.MustCompile(`[\x00-\x08\x0b\x0c\x0e-\x1f\x7f]`)
)

func validateIssueRef(in string) (string, error) {
	s := strings.TrimSpace(in)
	if s == "" {
		return "", validationErr("issue id is empty")
	}
	if controlCharRE.MatchString(s) {
		return "", validationErr("issue id contains control characters")
	}
	if uuidRE.MatchString(s) {
		return s, nil
	}
	upper := strings.ToUpper(s)
	if identifierRE.MatchString(upper) {
		return s, nil
	}
	return "", validationErr(fmt.Sprintf("issue id %q is not a UUID or TEAM-N identifier", s))
}

func validateQueryString(in string) (string, error) {
	if controlCharRE.MatchString(in) {
		return "", validationErr("query contains control characters")
	}
	if len(in) > 2048 {
		return "", validationErr("query exceeds 2048 chars")
	}
	return in, nil
}

// validateOutputPath enforces a CWD sandbox on agent-supplied --out targets:
// reject control chars, percent-encoded segments, escapes via "..", and
// symlinked parents that resolve outside the working directory.
func validateOutputPath(in, cwd string) (string, error) {
	abs, err := validateInputPath(in, cwd)
	if err != nil {
		return "", err
	}
	cwdAbs, err := filepath.Abs(cwd)
	if err != nil {
		return "", err
	}
	cwdReal, err := filepath.EvalSymlinks(cwdAbs)
	if err != nil {
		cwdReal = cwdAbs
	}
	parent := filepath.Dir(abs)
	parentReal, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", validationErr(fmt.Sprintf("--out %q: parent directory %s does not exist", in, parent))
	}
	resolved := filepath.Join(parentReal, filepath.Base(abs))
	if !pathInside(resolved, cwdReal) {
		return "", validationErr(fmt.Sprintf("--out %q escapes the working directory (resolved to %s)", in, resolved))
	}
	return resolved, nil
}

func pathInside(target, root string) bool {
	rootSep := filepath.Clean(root) + string(filepath.Separator)
	t := filepath.Clean(target) + string(filepath.Separator)
	return t == rootSep || strings.HasPrefix(t, rootSep)
}

func validateInputPath(in, cwd string) (string, error) {
	s := strings.TrimSpace(in)
	if s == "" {
		return "", validationErr("path is empty")
	}
	if controlCharRE.MatchString(s) {
		return "", validationErr("path contains control characters")
	}
	if strings.Contains(s, "%2e") || strings.Contains(s, "%2E") || strings.Contains(s, "%2f") || strings.Contains(s, "%2F") {
		return "", validationErr("path contains percent-encoded segments")
	}
	abs := s
	if !filepath.IsAbs(abs) {
		abs = filepath.Join(cwd, abs)
	}
	return filepath.Clean(abs), nil
}
