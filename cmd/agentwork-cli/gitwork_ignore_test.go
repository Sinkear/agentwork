package main

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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

// newGitTestRepo initializes a temp git workdir with an optional .gitignore and
// a seed commit, returning its path. Shared by the exclude-guard tests.
func newGitTestRepo(t *testing.T, ignoreFile string) string {
	t.Helper()
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
	if ignoreFile != "" {
		if err := os.WriteFile(filepath.Join(wt, ".gitignore"), []byte(ignoreFile), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(wt, "real.txt"), []byte("work\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "real.txt")
	run("commit", "-m", "seed")
	return wt
}

// writeProjectSkill drops a project-level skill file (the platform-managed
// infrastructure that must never ride the agent's commit).
func writeProjectSkill(t *testing.T, wt string) {
	t.Helper()
	dir := filepath.Join(wt, ".claude", "skills", "demo")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "SKILL.md"), []byte("skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
}

func containsArg(args []string, want string) bool {
	for _, a := range args {
		if a == want {
			return true
		}
	}
	return false
}

// TestGitAddExcludeIgnoredProjectSkills is the review's residual-bug case: a
// repo whose .gitignore covers .claude/ must survive `git add -A` — naming the
// IGNORED project-skills dir in an exclude pathspec fails the add with
// "paths are ignored ... use -f", exactly like AGENTS.md did.
func TestGitAddExcludeIgnoredProjectSkills(t *testing.T) {
	t.Parallel()
	wt := newGitTestRepo(t, ".claude/\n")
	writeProjectSkill(t, wt)
	if err := os.WriteFile(filepath.Join(wt, "real.txt"), []byte("work 2\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	args := gitAddExcludeArgs(context.Background(), wt, false)
	if containsArg(args, ":(exclude).claude/skills") {
		t.Fatalf("ignored project-skills dir must not be named in the pathspec: %v", args)
	}
	if out, err := runGit(context.Background(), wt, append([]string{"add", "-A", "--"}, args...)...); err != nil {
		t.Fatalf("git add -A must survive a git-ignored project-skills dir: %v\n%s", err, out)
	}
	staged, _ := runGit(context.Background(), wt, "diff", "--cached", "--name-only")
	if !strings.Contains(staged, "real.txt") {
		t.Fatalf("regular work must still be staged: %q", staged)
	}
	tracked, _ := runGit(context.Background(), wt, "ls-files")
	if strings.Contains(tracked, ".claude/skills") {
		t.Fatalf("ignored project skill must not be committed: %q", tracked)
	}
}

// TestGitAddExcludeNonIgnoredProjectSkills pins the other half of the check:
// a NON-ignored project-skills dir is still excluded (staged skills are
// platform infrastructure, never the agent's commit) and the add succeeds.
func TestGitAddExcludeNonIgnoredProjectSkills(t *testing.T) {
	t.Parallel()
	wt := newGitTestRepo(t, "")
	writeProjectSkill(t, wt)

	args := gitAddExcludeArgs(context.Background(), wt, false)
	if !containsArg(args, ":(exclude).claude/skills") {
		t.Fatalf("non-ignored project-skills dir must be excluded: %v", args)
	}
	if out, err := runGit(context.Background(), wt, append([]string{"add", "-A", "--"}, args...)...); err != nil {
		t.Fatalf("git add must succeed with a project-skills exclude: %v\n%s", err, out)
	}
	staged, _ := runGit(context.Background(), wt, "diff", "--cached", "--name-only")
	if strings.Contains(staged, ".claude/skills") {
		t.Fatalf("project skills must never be staged: %q", staged)
	}
}

// TestGitAddExcludeNonIgnoredAgents pins the inverse of the AGENTS.md test: a
// NON-ignored AGENTS.md is excluded (the platform profile suffix must not ride a
// commit) and the add succeeds.
func TestGitAddExcludeNonIgnoredAgents(t *testing.T) {
	t.Parallel()
	wt := newGitTestRepo(t, "")
	if err := os.WriteFile(filepath.Join(wt, "AGENTS.md"), []byte("profile\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	args := gitAddExcludeArgs(context.Background(), wt, true)
	if !containsArg(args, ":(exclude)AGENTS.md") {
		t.Fatalf("non-ignored AGENTS.md must be excluded: %v", args)
	}
	if out, err := runGit(context.Background(), wt, append([]string{"add", "-A", "--"}, args...)...); err != nil {
		t.Fatalf("git add must succeed with an AGENTS.md exclude: %v\n%s", err, out)
	}
	staged, _ := runGit(context.Background(), wt, "diff", "--cached", "--name-only")
	if strings.Contains(staged, "AGENTS.md") {
		t.Fatalf("AGENTS.md must never be staged when the profile is platform-written: %q", staged)
	}
}
