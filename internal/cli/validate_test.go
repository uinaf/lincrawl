package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateIssueRef(t *testing.T) {
	cases := []struct {
		in   string
		want string
		fail bool
	}{
		{"LIN-1", "LIN-1", false},
		{"lin-1", "lin-1", false},
		{"LIN-999999999", "LIN-999999999", false},
		{"08224eca-0c99-4f4f-989b-969a44f01923", "08224eca-0c99-4f4f-989b-969a44f01923", false},
		{"", "", true},
		{"bad id", "", true},
		{"LIN", "", true},
		{"LIN-", "", true},
		{"LIN-abc", "", true},
		{"with\x00ctrl", "", true},
	}
	for _, tc := range cases {
		got, err := validateIssueRef(tc.in)
		if tc.fail && err == nil {
			t.Errorf("validateIssueRef(%q): want error, got %q", tc.in, got)
		}
		if !tc.fail && err != nil {
			t.Errorf("validateIssueRef(%q): unexpected %v", tc.in, err)
		}
		if !tc.fail && got != tc.want {
			t.Errorf("validateIssueRef(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestValidateQueryString(t *testing.T) {
	if _, err := validateQueryString("hello world"); err != nil {
		t.Fatalf("plain query: %v", err)
	}
	if _, err := validateQueryString("with\x07bell"); err == nil {
		t.Fatal("expected error on control char")
	}
	if _, err := validateQueryString(strings.Repeat("a", 3000)); err == nil {
		t.Fatal("expected error on oversized query")
	}
}

func TestValidateOutputPathSandbox(t *testing.T) {
	cwd := t.TempDir()
	cwdReal, _ := filepath.EvalSymlinks(cwd)
	inside := filepath.Join(cwd, "snapshots", "file.jsonl")
	if err := os.MkdirAll(filepath.Dir(inside), 0o755); err != nil {
		t.Fatal(err)
	}
	got, err := validateOutputPath("snapshots/file.jsonl", cwd)
	if err != nil {
		t.Fatalf("inside cwd: %v", err)
	}
	if !strings.HasPrefix(got, cwdReal) {
		t.Fatalf("resolved path %q not under cwdReal %q", got, cwdReal)
	}
	if _, err := validateOutputPath("/tmp/outside.jsonl", cwd); err == nil {
		t.Fatal("expected error for absolute path outside cwd")
	}
	if _, err := validateOutputPath("../../etc/passwd", cwd); err == nil {
		t.Fatal("expected error for .. traversal")
	}
	if _, err := validateOutputPath("snapshots/%2e%2e/file", cwd); err == nil {
		t.Fatal("expected error for percent-encoded segments")
	}
	if _, err := validateOutputPath("snapshots/with\x00null", cwd); err == nil {
		t.Fatal("expected error for control char")
	}
}

func TestValidateOutputPathRejectsSymlinkEscape(t *testing.T) {
	cwd := t.TempDir()
	outside := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, "link-parent"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cwd, "evil")); err != nil {
		t.Skip("symlink unsupported:", err)
	}
	if _, err := validateOutputPath("evil/file.jsonl", cwd); err == nil {
		t.Fatal("expected sandbox error on symlink escape")
	}
}

func TestValidateOutputPathRejectsFinalSymlink(t *testing.T) {
	cwd := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.jsonl")
	target := filepath.Join(cwd, "out.jsonl")
	if err := os.Symlink(outside, target); err != nil {
		t.Skip("symlink unsupported:", err)
	}
	if _, err := validateOutputPath("out.jsonl", cwd); err == nil {
		t.Fatal("expected error for final-component symlink")
	}
}

func TestValidateSandboxedInputPathRejectsFinalSymlinkEscape(t *testing.T) {
	cwd := t.TempDir()
	outside := filepath.Join(t.TempDir(), "query.graphql")
	if err := os.WriteFile(outside, []byte("{ viewer { id } }"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, filepath.Join(cwd, "query.graphql")); err != nil {
		t.Skip("symlink unsupported:", err)
	}
	if _, err := validateSandboxedInputPath("--graphql-file", "query.graphql", cwd); err == nil {
		t.Fatal("expected sandbox error on final-component symlink escape")
	}
}
