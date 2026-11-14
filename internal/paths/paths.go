// Package paths is the single source of truth for agentwork's filesystem
// layout.
//
// All durable state lives under one data root. By default that root is
// ~/.agentwork (kept for backwards compatibility with existing installs), but
// it can — and for production deployments SHOULD — be moved outside the
// release artifact with AGENTWORK_HOME:
//
//	AGENTWORK_HOME=/var/lib/agentwork ./bin/agentwork-daemon
//
// The release directory (bin/, web/) is stateless and can be replaced on every
// upgrade; only this root must survive. Keeping the root stable is what makes
// "replace the artifact, keep the agents/teams/config" safe.
//
// AGENTWORK_HOME is read on every call (never cached) so tests that set HOME
// or AGENTWORK_HOME with t.Setenv keep working.
package paths

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// EnvHome is the environment variable that overrides the data root.
const EnvHome = "AGENTWORK_HOME"

// EnvDB optionally overrides just the SQLite file (useful for tests and for
// splitting the DB onto a different volume). Relative paths stay relative to
// the process working directory, matching the daemon's -db flag.
const EnvDB = "AGENTWORK_DB"

// Home returns the state root: $AGENTWORK_HOME when set, otherwise
// ~/.agentwork. A leading "~" in AGENTWORK_HOME is expanded.
func Home() string {
	if v := strings.TrimSpace(os.Getenv(EnvHome)); v != "" {
		return expandUser(v)
	}
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		return filepath.Join(home, ".agentwork")
	}
	// Last-resort fallback: keep the process alive when HOME is unavailable
	// (containers). Operators should always set AGENTWORK_HOME explicitly.
	return filepath.Join(os.TempDir(), "agentwork-home")
}

// EnsureHome creates the data root if needed.
func EnsureHome() (string, error) {
	home := Home()
	if err := os.MkdirAll(home, 0o755); err != nil {
		return home, err
	}
	return home, nil
}

func expandUser(p string) string {
	if p == "~" {
		if home, err := os.UserHomeDir(); err == nil {
			return home
		}
		return p
	}
	if strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, p[2:])
		}
	}
	return p
}

// DB is the SQLite database file.
func DB() string {
	if v := strings.TrimSpace(os.Getenv(EnvDB)); v != "" {
		return expandUser(v)
	}
	return filepath.Join(Home(), "agentwork.db")
}

// Runs is the root of run/domain filesystem state.
func Runs() string { return filepath.Join(Home(), "runs") }

// Repos is the daemon's per-domain bare mirror root.
func Repos() string { return filepath.Join(Runs(), "repos") }

// RunWorktree is one run's execution worktree.
func RunWorktree(runID string) string { return filepath.Join(Runs(), runID) }

// GoalWorktree is the ephemeral deliver worktree for a goal.
func GoalWorktree(goalID string) string { return filepath.Join(Runs(), "deliver-"+goalID) }

// ScratchDomain is a scratch domain's persistent project root.
func ScratchDomain(name string) string { return filepath.Join(Runs(), "scratch", name) }

// ScratchGoal is a scratch goal's durable project directory.
func ScratchGoal(domainName, goalID string) string {
	return filepath.Join(ScratchDomain(domainName), "goals", goalID)
}

// Proc is a processor run's scratch directory.
func Proc(runID string) string { return filepath.Join(Runs(), "proc", runID) }

// GoalAgent is the machine-side persistent worktree for a writable run,
// keyed by (goal, agent) or (goal, sub-goal, agent).
func GoalAgent(goalID, subGoalID, agentID string) string {
	if subGoalID != "" {
		return filepath.Join(Runs(), "goal", goalID, "sg", subGoalID, agentID)
	}
	return filepath.Join(Runs(), "goal", goalID, agentID)
}

// Digest is where collected daily-digest articles land.
func Digest() string { return filepath.Join(Home(), "digest") }

// DaemonLog is the daemon's rotated log file.
func DaemonLog() string { return filepath.Join(Home(), "daemon.log") }

// CLILog is the machine-side CLI log.
func CLILog() string { return filepath.Join(Home(), "cli.log") }

// CLIState is the machine identity / last connection state.
func CLIState() string { return filepath.Join(Home(), "agentwork-cli-state.json") }

// AgentsConfig is the optional agents.yaml probe override.
func AgentsConfig() string { return filepath.Join(Home(), "agents.yaml") }

// PendingReports holds terminal run reports that could not be delivered.
func PendingReports() string { return filepath.Join(Home(), "pending-reports.json") }

// AgentProfileDir holds a pushed AGENTS.md per agent.
func AgentProfileDir(agentID string) string { return filepath.Join(Home(), "agents", agentID) }

// MachineSkillRoot holds pushed skill packages per agent on the machine.
func MachineSkillRoot(agentID string) string { return filepath.Join(Home(), "skills", agentID) }

// ChatDir is the stable, cwd-keyed chat session directory per agent.
func ChatDir(agentID string) string { return filepath.Join(Home(), "chat", agentID) }

// BinDir holds the `agentwork` shim injected into agent PATH.
func BinDir() string { return filepath.Join(Home(), "bin") }

// RepoDir is the machine-side bare repo mirror for a domain. The daemon keeps
// its own copy under Runs/repos; a remote machine has this one.
func RepoDir(domainID string) string { return filepath.Join(Home(), "repos", domainID) }

// Layout is a JSON-friendly snapshot of every top-level path, used by the
// `paths` command and the operations docs.
type Layout struct {
	Home           string `json:"home"`
	Database       string `json:"database"`
	Runs           string `json:"runs"`
	Repos          string `json:"repos"`
	Digest         string `json:"digest"`
	DaemonLog      string `json:"daemon_log"`
	CLILog         string `json:"cli_log"`
	CLIState       string `json:"cli_state"`
	AgentsConfig   string `json:"agents_config"`
	PendingReports string `json:"pending_reports"`
	Agents         string `json:"agents"`
	Skills         string `json:"skills"`
	Chat           string `json:"chat"`
	Bin            string `json:"bin"`
}

// Current returns the current layout snapshot.
func Current() Layout {
	return Layout{
		Home:           Home(),
		Database:       DB(),
		Runs:           Runs(),
		Repos:          Repos(),
		Digest:         Digest(),
		DaemonLog:      DaemonLog(),
		CLILog:         CLILog(),
		CLIState:       CLIState(),
		AgentsConfig:   AgentsConfig(),
		PendingReports: PendingReports(),
		Agents:         filepath.Join(Home(), "agents"),
		Skills:         filepath.Join(Home(), "skills"),
		Chat:           filepath.Join(Home(), "chat"),
		Bin:            BinDir(),
	}
}

// JSON renders the layout for CLI output.
func (l Layout) JSON() string {
	b, _ := json.MarshalIndent(l, "", "  ")
	return string(b)
}
