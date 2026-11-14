package daemon

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestSweepRunWorktreesKeepsSkills: the startup sweep must drop stale run
// worktrees but leave runs/skills/<skillID>/ (the persistent skill library)
// alone — that path is a named sibling, not a run worktree.
func TestSweepRunWorktreesKeepsSkills(t *testing.T) {
	home := t.TempDir()
	t.Setenv("AGENTWORK_HOME", home)
	runs := filepath.Join(home, "runs")
	skillFile := filepath.Join(runs, "skills", "abcdef", "SKILL.md")
	if err := os.MkdirAll(filepath.Dir(skillFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(skillFile, []byte("# skill\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	stale := filepath.Join(runs, "run-123456")
	if err := os.MkdirAll(stale, 0o755); err != nil {
		t.Fatal(err)
	}

	d := &Daemon{}
	d.sweepRunWorktrees(context.Background())

	if _, err := os.Stat(skillFile); err != nil {
		t.Fatalf("sweep removed the skill library: %v", err)
	}
	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Fatalf("sweep kept stale run worktree %s (err=%v)", stale, err)
	}
}
