package main

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestGitAddExcludeIgnoredAgents reproduces the R3/R4 failure class: a
// workdir whose .gitignore ignores AGENTS.md must survive `git add -A` —
// naming the ignored file in ANY pathspec (even an exclude) fails the add.
func TestGitAddExcludeIgnoredAgents(t *testing.T) {
	t.Parallel()
	wt := t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, err := runGit(context.Background(), wt, args...)
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return out
	}
	run("init", ".")
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(wt, ".gitignore"), []byte("AGENTS.md\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "AGENTS.md"), []byte("profile\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wt, "real.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "real.txt")
	run("commit", "-m", "seed")

	// Scenario 1: AGENTS.md git-ignored — add must succeed and NOT stage it.
	args := append([]string{"add", "-A", "--"}, gitAddExcludeArgs(context.Background(), wt, true)...)
	if out, err := runGit(context.Background(), wt, args...); err != nil {
		t.Fatalf("git add -A must survive a git-ignored AGENTS.md: %v\n%s", err, out)
	}
	st, _ := runGit(context.Background(), wt, "status", "--porcelain")
	for _, line := range splitLines(st) {
		if len(line) > 3 && line[3:] == "AGENTS.md" {
			t.Fatalf("ignored AGENTS.md must not be staged: %s", st)
		}
	}
}

func splitLines(s string) []string {
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			if i > start {
				out = append(out, s[start:i])
			}
			start = i + 1
		}
	}
	if start < len(s) {
		out = append(out, s[start:])
	}
	return out
}
