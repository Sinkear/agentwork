package paths

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHomeHonorsEnv(t *testing.T) {
	dir := t.TempDir()
	t.Setenv(EnvHome, dir)
	t.Setenv(EnvDB, "")

	if got, want := Home(), dir; got != want {
		t.Fatalf("Home() = %q, want %q", got, want)
	}
	if got, want := DB(), filepath.Join(dir, "agentwork.db"); got != want {
		t.Fatalf("DB() = %q, want %q", got, want)
	}
	if got, want := Runs(), filepath.Join(dir, "runs"); got != want {
		t.Fatalf("Runs() = %q, want %q", got, want)
	}
}

func TestHomeDefaultsToDotAgentwork(t *testing.T) {
	home := t.TempDir()
	t.Setenv(EnvHome, "")
	t.Setenv(EnvDB, "")
	t.Setenv("HOME", home)

	if got, want := Home(), filepath.Join(home, ".agentwork"); got != want {
		t.Fatalf("Home() = %q, want %q", got, want)
	}
}

func TestHomeExpandsTilde(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(EnvHome, "~/custom-agentwork")

	if got, want := Home(), filepath.Join(home, "custom-agentwork"); got != want {
		t.Fatalf("Home() = %q, want %q", got, want)
	}
}

func TestDBOverride(t *testing.T) {
	root := t.TempDir()
	db := filepath.Join(root, "other", "state.db")
	t.Setenv(EnvHome, filepath.Join(root, "home"))
	t.Setenv(EnvDB, db)

	if got := DB(); got != db {
		t.Fatalf("DB() = %q, want %q", got, db)
	}
}

func TestEnsureHome(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested", "agentwork")
	t.Setenv(EnvHome, dir)

	got, err := EnsureHome()
	if err != nil {
		t.Fatalf("EnsureHome: %v", err)
	}
	if got != dir {
		t.Fatalf("EnsureHome() = %q, want %q", got, dir)
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		t.Fatalf("data root not created: %v", err)
	}
}

func TestLayoutCoversEveryRoot(t *testing.T) {
	root := t.TempDir()
	t.Setenv(EnvHome, root)
	t.Setenv(EnvDB, "")

	l := Current()
	for name, got := range map[string]string{
		"Home":           l.Home,
		"Database":       l.Database,
		"Runs":           l.Runs,
		"Digest":         l.Digest,
		"DaemonLog":      l.DaemonLog,
		"CLILog":         l.CLILog,
		"CLIState":       l.CLIState,
		"AgentsConfig":   l.AgentsConfig,
		"PendingReports": l.PendingReports,
		"Agents":         l.Agents,
		"Skills":         l.Skills,
		"Chat":           l.Chat,
		"Bin":            l.Bin,
	} {
		if got == "" {
			t.Errorf("%s is empty", name)
		}
		if !strings.HasPrefix(got, root+string(os.PathSeparator)) && got != root {
			t.Errorf("%s = %q, want under %q", name, got, root)
		}
	}
	if !strings.Contains(l.JSON(), `"database"`) {
		t.Fatalf("JSON missing database field: %s", l.JSON())
	}
}
