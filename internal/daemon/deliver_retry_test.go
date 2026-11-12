package daemon

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/service"
	"github.com/eushing/agentwork/internal/store"
)

// TestDeliverGoalRetryable (P-D 回归, RSI R4 候选4【已批准】): the retry
// whitelist — code problems (post-merge verification/guards, merge
// conflicts) and deterministic faults (service.IsNonRetryableFailure) never
// retry; everything else (fetch/push network errors, repo prepare) does.
func TestDeliverGoalRetryable(t *testing.T) {
	cases := []struct {
		note string
		want bool
	}{
		{"deliver: fetch: exit status 128: could not read from remote repository", true},
		{"deliver: push: exit status 1: connection reset by peer", true},
		{"deliver: prepare repo: git clone --bare: timeout", true},
		{"deliver: 等待运行中的 owner run 结束超时（5 分钟），请稍后再次批准", true},
		{"deliver: post-merge verification failed:\n$ go test ./...\nFAIL", false},
		{"deliver: post-merge guards failed:\nguard diff_contains(*_test.go): no changed path matches", false},
		{"deliver: merge conflict:\nCONFLICT (content): Merge conflict in main.go", false},
		{"Model unavailable: provider returned 401", false}, // deterministic fault whitelist
		{"", false},
		{"   ", false},
	}
	for _, tc := range cases {
		if got := deliverGoalRetryable(tc.note); got != tc.want {
			t.Errorf("deliverGoalRetryable(%q) = %v, want %v", tc.note, got, tc.want)
		}
	}
}

// TestDeliverGoalWithRetryRecovers delivers against a remote that is MISSING
// on the first attempt (fetch fails — the transient class) and is RESTORED
// during the retry delay, asserting the automatic second attempt delivers
// the goal to done. The 60s production delay is collapsed for the test by
// driving the pieces deliverGoalWithRetry drives (deliverGoal → retryable
// note → delay → deliverGoal): the delay constant is production wiring, the
// retry BEHAVIOR is what must not regress.
func TestDeliverGoalWithRetryRecovers(t *testing.T) {
	// Isolate HOME so runsRoot() and any ~/.gitconfig are ours alone.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("GIT_CONFIG_NOSYSTEM", "1")
	t.Setenv("GIT_CONFIG_GLOBAL", filepath.Join(home, ".gitconfig-noop"))

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Remote: bare repo with one commit on main, plus the goal's feat branch
	// carrying a second commit (the agent's work).
	work := t.TempDir()
	seed := filepath.Join(work, "seed")
	mustRunGit(t, "", "init", "-b", "main", seed)
	mustRunGit(t, seed, "config", "user.email", "seed@local")
	mustRunGit(t, seed, "config", "user.name", "seed")
	if err := os.WriteFile(filepath.Join(seed, "README.md"), []byte("hi\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, seed, "add", ".")
	mustRunGit(t, seed, "commit", "-m", "init")
	mustRunGit(t, seed, "checkout", "-b", "feat-test0001")
	if err := os.WriteFile(filepath.Join(seed, "feature.txt"), []byte("new\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	mustRunGit(t, seed, "add", ".")
	mustRunGit(t, seed, "commit", "-m", "feat: add feature.txt")

	remote := filepath.Join(work, "remote.git")
	mustRunGit(t, "", "clone", "--bare", seed, remote)

	// The remote is "down" for the first attempt: rename the remote away.
	remoteDown := remote + ".down"
	if err := os.Rename(remote, remoteDown); err != nil {
		t.Fatal(err)
	}

	domainID := "dom-deliver-retry"
	goalID := "test-goal-0001"
	dbPath := filepath.Join(work, "aw.db")
	st, err := store.Open(dbPath)
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	bus := events.NewBus()
	goalSvc := service.NewGoalService(st, bus)
	d := &Daemon{st: st, bus: bus, goalSvc: goalSvc}

	ts := time.Now().Format(time.RFC3339)
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO domain (id,type,name,git_url,default_branch,git_identity,git_credentials,created_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		domainID, "repo", "deliver-retry-test", "file://"+remote, "main", "", "", ts); err != nil {
		t.Fatalf("insert domain: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO goal (id,title,domain_id,assignee_type,assignee_id,status,created_at)
		 VALUES (?,?,?,?,?,?,?)`,
		goalID, "deliver retry test", domainID, "agent", "dummy", "review", ts); err != nil {
		t.Fatalf("insert goal: %v", err)
	}

	// Attempt 1: the remote is away → fetch fails, the note is retryable.
	d.deliverGoal(ctx, goalID)
	var note string
	if err := st.DB().QueryRowContext(ctx, `SELECT review_request FROM goal WHERE id=?`, goalID).Scan(&note); err != nil {
		t.Fatalf("read note: %v", err)
	}
	if !strings.HasPrefix(strings.TrimSpace(note), "deliver:") {
		t.Fatalf("attempt 1 must fail with a deliver: note, got %q", note)
	}
	if !deliverGoalRetryable(note) {
		t.Fatalf("fetch failure must be retryable, note %q", note)
	}

	// The remote comes back during the delay window.
	if err := os.Rename(remoteDown, remote); err != nil {
		t.Fatal(err)
	}
	// The bare clone mirrors all branches into refs/heads/*; deliver's
	// ensureSharedRepo re-points remote.origin.fetch to refs/remotes/origin/*.
	// Rename the feat branch to the EXACT name deliver will look for
	// (goalBranchName truncates goalID to 8 chars).
	shortID := goalID
	if len(shortID) > 8 {
		shortID = shortID[:8]
	}
	mustRunGit(t, remote, "branch", "-m", "feat-test0001", "feat-"+shortID)

	// Attempt 2 (the retry deliverGoal call — the only thing the 60s delay
	// separates in production): the fetch succeeds, the merge lands, the
	// goal reaches done.
	d.deliverGoal(ctx, goalID)
	var status string
	if err := st.DB().QueryRowContext(ctx, `SELECT status FROM goal WHERE id=?`, goalID).Scan(&status); err != nil {
		t.Fatalf("read goal: %v", err)
	}
	if status != "done" {
		_ = st.DB().QueryRowContext(ctx, `SELECT review_request FROM goal WHERE id=?`, goalID).Scan(&note)
		t.Fatalf("goal status = %q after retry, want done. note=%q", status, note)
	}

	// The success overwrote the failure note — the human sees the outcome.
	_ = st.DB().QueryRowContext(ctx, `SELECT review_request FROM goal WHERE id=?`, goalID).Scan(&note)
	if strings.HasPrefix(strings.TrimSpace(note), "deliver:") {
		t.Fatalf("success must clear the failure note, got %q", note)
	}
}
