package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eushing/agentwork/internal/link"
)

// stagePersona writes an agent's pushed persona (what config.push lands in
// ~/.agentwork/agents/<id>/AGENTS.md) under the isolated AGENTWORK_HOME.
func stagePersona(t *testing.T, home, agentID, content string) {
	t.Helper()
	dir := filepath.Join(home, "agents", agentID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "AGENTS.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func readAgentsMd(t *testing.T, workdir string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(workdir, "AGENTS.md"))
	if err != nil {
		t.Fatalf("read AGENTS.md: %v", err)
	}
	return string(b)
}

// TestStageRunProfileScratchRewritesPerRun pins F3 (身份唯一性) for
// platform-owned scratch workdirs: a run's identity comes from THIS run
// alone. Two consecutive runs by different agents must leave ONLY the
// second run's persona + RunProfile — the first run's persona and fixed
// block may never survive the spawn (the old append-only merge layered
// them, hijacking the successor's role).
func TestStageRunProfileScratchRewritesPerRun(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTWORK_HOME", home)
	ctx := context.Background()
	workdir := t.TempDir()

	stagePersona(t, home, "agent-a", "PERSONA A: executor persona.")
	stagePersona(t, home, "agent-b", "PERSONA B: verifier persona.")

	p1 := link.RunDispatchParams{RunID: "run-1", AgentID: "agent-a", Scratch: true, RunProfile: "# FIXED BLOCK run-1\nrole: owner"}
	if got := stageRunProfile(ctx, p1, workdir); got != "PERSONA A: executor persona.\n\n# FIXED BLOCK run-1\nrole: owner" {
		t.Fatalf("run-1 return = platform block, got:\n%q", got)
	}
	first := readAgentsMd(t, workdir)
	if strings.Contains(first, "PERSONA B") || !strings.Contains(first, "PERSONA A") {
		t.Fatalf("run-1 file must hold run-1's identity, got:\n%s", first)
	}

	// The verifier's run — same shared workdir, different agent and role.
	p2 := link.RunDispatchParams{RunID: "run-2", AgentID: "agent-b", Scratch: true, RunProfile: "# FIXED BLOCK run-2\nrole: verify"}
	got2 := stageRunProfile(ctx, p2, workdir)
	second := readAgentsMd(t, workdir)
	if strings.Contains(second, "PERSONA A") || strings.Contains(second, "run-1") {
		t.Fatalf("scratch AGENTS.md must be REWRITTEN per run, predecessor identity leaked:\n%s", second)
	}
	if !strings.Contains(second, "PERSONA B") || !strings.Contains(second, "run-2") {
		t.Fatalf("scratch AGENTS.md must hold THIS run's identity, got:\n%s", second)
	}
	if second != got2 {
		t.Fatalf("scratch file must equal the platform block exactly (commit exclusion depends on HasSuffix)")
	}
}

// TestStageRunProfileScratchSameAgentNoLayering pins the fixed-block
// duplicate-merge bug: the SAME agent re-running with a DIFFERENT role
// contract (RunProfile) must not stack the old fixed block under the new
// one — the file holds exactly one persona and one profile.
func TestStageRunProfileScratchSameAgentNoLayering(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTWORK_HOME", home)
	ctx := context.Background()
	workdir := t.TempDir()

	stagePersona(t, home, "agent-x", "PERSONA X.")

	stageRunProfile(ctx, link.RunDispatchParams{RunID: "r1", AgentID: "agent-x", Scratch: true, RunProfile: "# FIXED\nrole: owner"}, workdir)
	stageRunProfile(ctx, link.RunDispatchParams{RunID: "r2", AgentID: "agent-x", Scratch: true, RunProfile: "# FIXED\nrole: verify"}, workdir)

	got := readAgentsMd(t, workdir)
	if n := strings.Count(got, "PERSONA X."); n != 1 {
		t.Fatalf("persona must appear exactly once, found %d:\n%s", n, got)
	}
	if n := strings.Count(got, "# FIXED"); n != 1 {
		t.Fatalf("fixed block must appear exactly once (no layering), found %d:\n%s", n, got)
	}
	if !strings.Contains(got, "role: verify") || strings.Contains(got, "role: owner") {
		t.Fatalf("the CURRENT run's profile must replace the previous one, got:\n%s", got)
	}
}

// TestStageRunProfileGitKeepsRepoBaseline pins the repo-owned ground
// truth: a committed AGENTS.md is the project's own instructions and must
// survive every spawn, while the platform block below it is REPLACED per
// run (never accumulated).
func TestStageRunProfileGitKeepsRepoBaseline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTWORK_HOME", home)
	ctx := context.Background()

	// Seed a real repo whose HEAD carries a committed AGENTS.md, then use a
	// plain clone as the workdir (stageRunProfile only reads HEAD:AGENTS.md).
	url := seedRepo(t)
	workdir := filepath.Join(t.TempDir(), "wt")
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", workdir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	if out, err := exec.Command("git", "clone", url, workdir).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(workdir, "AGENTS.md"), []byte("# REPO RULES\nproject ground truth"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "AGENTS.md")
	run("commit", "-m", "repo agents")
	run("push", "origin", "master")

	stagePersona(t, home, "agent-a", "PERSONA A.")
	stageRunProfile(ctx, link.RunDispatchParams{RunID: "r1", AgentID: "agent-a", RunProfile: "# FIXED run-1"}, workdir)
	_ = stageRunProfile(ctx, link.RunDispatchParams{RunID: "r2", AgentID: "agent-a", RunProfile: "# FIXED run-2"}, workdir)

	got := readAgentsMd(t, workdir)
	if !strings.Contains(got, "# REPO RULES") {
		t.Fatalf("committed repo AGENTS.md must survive staging, got:\n%s", got)
	}
	if !strings.HasPrefix(got, "# REPO RULES") {
		t.Fatalf("repo baseline must ride on TOP of the platform block, got:\n%s", got)
	}
	if n := strings.Count(got, "# FIXED"); n != 1 {
		t.Fatalf("platform profile must be replaced per run, not accumulated (%d copies):\n%s", n, got)
	}
	if !strings.Contains(got, "# FIXED run-2") || strings.Contains(got, "run-1") {
		t.Fatalf("current run's profile must replace the previous one, got:\n%s", got)
	}
}

// TestStageRunProfileRemovesStaleScratchFile: an agent with no persona and
// no RunProfile must CLEAR a stale platform file on a platform-owned
// (scratch) workdir instead of leaving a predecessor's identity behind.
func TestStageRunProfileRemovesStaleScratchFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTWORK_HOME", home)
	ctx := context.Background()
	workdir := t.TempDir()
	agentsMD := filepath.Join(workdir, "AGENTS.md")
	if err := os.WriteFile(agentsMD, []byte("STALE predecessor profile"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := link.RunDispatchParams{RunID: "r1", AgentID: "agent-empty", Scratch: true}
	if got := stageRunProfile(ctx, p, workdir); got != "" {
		t.Fatalf("no persona/RunProfile must return \"\", got %q", got)
	}
	if _, err := os.Stat(agentsMD); !os.IsNotExist(err) {
		t.Fatalf("stale scratch AGENTS.md must be removed, stat err=%v", err)
	}
}

// TestStageRunProfileEmptyBlockRestoresRepoBaseline: a persona-less,
// profile-less run on a git workdir restores the repo's committed
// AGENTS.md alone — the platform adds nothing this run.
func TestStageRunProfileEmptyBlockRestoresRepoBaseline(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTWORK_HOME", home)
	ctx := context.Background()

	url := seedRepo(t)
	workdir := filepath.Join(t.TempDir(), "wt")
	if out, err := exec.Command("git", "clone", url, workdir).CombinedOutput(); err != nil {
		t.Fatalf("clone: %v\n%s", err, out)
	}
	run := func(args ...string) {
		t.Helper()
		if out, err := exec.Command("git", append([]string{"-C", workdir}, args...)...).CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	run("config", "user.email", "t@t")
	run("config", "user.name", "t")
	if err := os.WriteFile(filepath.Join(workdir, "AGENTS.md"), []byte("# REPO RULES"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "AGENTS.md")
	run("commit", "-m", "repo agents")
	run("push", "origin", "master")
	// Simulate the OLD bug's leftover: a stale platform suffix below the baseline.
	_ = os.WriteFile(filepath.Join(workdir, "AGENTS.md"), []byte("# REPO RULES\n\nSTALE platform block"), 0o644)

	if got := stageRunProfile(ctx, link.RunDispatchParams{RunID: "r1", AgentID: "agent-empty"}, workdir); got != "" {
		t.Fatalf("no persona/RunProfile must return \"\", got %q", got)
	}
	if got := readAgentsMd(t, workdir); strings.TrimSpace(got) != "# REPO RULES" {
		t.Fatalf("repo baseline must be restored alone, got:\n%s", got)
	}
}
