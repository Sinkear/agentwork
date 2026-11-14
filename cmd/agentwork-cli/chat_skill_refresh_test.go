package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/eushing/agentwork/internal/paths"
)

// stageSkill materializes one skill dir under the machine's platform-managed
// staging root (what a config.push extraction does).
func stageSkill(t *testing.T, agentID, name string, files map[string]string) {
	t.Helper()
	root := filepath.Join(machineSkillRoot(agentID), name)
	for rel, body := range files {
		target := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			t.Fatalf("mkdir staging: %v", err)
		}
		if err := os.WriteFile(target, []byte(body), 0o644); err != nil {
			t.Fatalf("write staging: %v", err)
		}
	}
}

func readChatFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

// TestRefreshAgentSkillCopiesNextSessionSeesNewSkill is the acceptance test for
// "after using the skill sync interface, the agent's NEXT session uses the new
// skill without restarting the agent": chat.open copies the staging dir into
// the chat cwd ONCE; a later sync rewrites staging (config.push) and the bridge
// must refresh every LIVE chat cwd of that agent, so a new ACP session in the
// same live CLI process (which reads its cwd again) sees the new files.
func TestRefreshAgentSkillCopiesNextSessionSeesNewSkill(t *testing.T) {
	t.Setenv("AGENTWORK_HOME", t.TempDir())

	const agentID = "agent-refresh"
	const subdir = ".agents/skills" // stand-in for the CLI's project skills dir
	cwd := paths.ChatDir(agentID)
	stageSkill(t, agentID, "demo", map[string]string{
		"SKILL.md": "---\nname: demo\ndescription: d\n---\nversion one\n",
	})
	copyAgentSkills(agentID, cwd, subdir, true) // the live chat's initial copy
	target := filepath.Join(cwd, subdir, "demo", "SKILL.md")
	if got := readChatFile(t, target); !strings.Contains(got, "version one") {
		t.Fatalf("initial chat copy = %q", got)
	}

	// A second agent's live chat must stay independent.
	const otherAgent = "agent-other"
	otherCwd := paths.ChatDir(otherAgent)
	stageSkill(t, otherAgent, "demo", map[string]string{"SKILL.md": "other agent v1\n"})
	copyAgentSkills(otherAgent, otherCwd, subdir, true)

	b := newChatBridge()
	b.sessions["chat-1"] = chatSession{agentID: agentID, cwd: cwd, subdir: subdir}
	b.sessions["chat-2"] = chatSession{agentID: otherAgent, cwd: otherCwd, subdir: subdir}

	// The sync interface landed new content: config.push rewrote staging.
	if err := os.RemoveAll(machineSkillRoot(agentID)); err != nil {
		t.Fatalf("reset staging: %v", err)
	}
	stageSkill(t, agentID, "demo", map[string]string{
		"SKILL.md":       "---\nname: demo\ndescription: d\n---\nversion two\n",
		"scripts/run.sh": "echo two\n",
	})

	// connect.go calls this after a successful config.push extraction.
	b.refreshAgentSkillCopies(agentID)

	if got := readChatFile(t, target); !strings.Contains(got, "version two") {
		t.Fatalf("live chat copy after refresh = %q, want version two", got)
	}
	if got := readChatFile(t, filepath.Join(cwd, subdir, "demo", "scripts", "run.sh")); got != "echo two\n" {
		t.Fatalf("new files must land in the live chat copy, got %q", got)
	}

	// The next ACP session in the same process reads the chat cwd again (a
	// session re-init/another chat.open does its own copy; either way it must
	// see the new content, not a restart-required stale file).
	copyAgentSkills(agentID, cwd, subdir, false)
	if got := readChatFile(t, target); !strings.Contains(got, "version two") {
		t.Fatalf("next session copy = %q", got)
	}

	if got := readChatFile(t, filepath.Join(otherCwd, subdir, "demo", "SKILL.md")); got != "other agent v1\n" {
		t.Fatalf("another agent's live chat must not be refreshed, got %q", got)
	}
}

// TestRefreshAgentSkillCopiesClearsWhenNoSkillsStaged: an agent whose skills
// were all deselected must not keep stale skill files in its live chat cwd.
func TestRefreshAgentSkillCopiesClearsWhenNoSkillsStaged(t *testing.T) {
	t.Setenv("AGENTWORK_HOME", t.TempDir())

	const agentID = "agent-clear"
	const subdir = ".agents/skills"
	cwd := paths.ChatDir(agentID)
	stageSkill(t, agentID, "demo", map[string]string{"SKILL.md": "v1\n"})
	copyAgentSkills(agentID, cwd, subdir, true)
	target := filepath.Join(cwd, subdir, "demo")
	if _, err := os.Stat(target); err != nil {
		t.Fatalf("precondition: chat copy missing: %v", err)
	}

	b := newChatBridge()
	b.sessions["chat-1"] = chatSession{agentID: agentID, cwd: cwd, subdir: subdir}

	// config.push with zero skills wipes staging (connect.go RemoveAll).
	if err := os.RemoveAll(machineSkillRoot(agentID)); err != nil {
		t.Fatalf("reset staging: %v", err)
	}
	b.refreshAgentSkillCopies(agentID)

	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatalf("stale skill copy must be removed, stat err = %v", err)
	}
}
