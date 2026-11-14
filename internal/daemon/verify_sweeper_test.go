package daemon

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/service"
	"github.com/eushing/agentwork/internal/store"
)

// The verify-integrity sweeper (contract 7.3, findings §W6): a verify run can
// reach a terminal status without a verdict, and the run-end reconcile that
// should heal it can be lost in a crash between the terminal UPDATE and the
// reconcile transaction. The daemon's 2-minute sweep is the durable safety
// net — one re-dispatch, then a human park. These tests drive the daemon
// entry points (sweepVerifyingSubGoals / runVerifySweeper), not the service.

// verifySweeperFixture is a daemon wired to an in-memory store plus one
// active goal whose sub-goal is stuck verifying with a completed, verdict-less
// verify run (the crash-window shape: the run was stamped terminal but its
// reconcile never ran).
type verifySweeperFixture struct {
	d              *Daemon
	st             *store.Store
	ctx            context.Context
	goalID         string
	subGoalID      string
	verifyRunID    string
	subGoalTitle   string
	assigneeRunID  string
	verifierAgentI string
}

func newVerifySweeperFixture(t *testing.T) *verifySweeperFixture {
	t.Helper()
	ctx := context.Background()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	bus := events.NewBus()
	gs := service.NewGoalService(st, bus)
	rs := service.NewRunService(st, bus)
	gs.SetRunService(rs)
	rs.SetGoalService(gs)
	d := &Daemon{st: st, bus: bus, goalSvc: gs, runSvc: rs, ctx: ctx}

	rt, err := service.NewRuntimeService(st).Create(ctx, service.Runtime{Name: "rt", MachineID: "m1"})
	if err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	agentSvc := service.NewAgentService(st, bus)
	mk := func(name string) string {
		a, err := agentSvc.Create(ctx, service.Agent{Name: name, RuntimeID: rt.ID, MaxConcurrent: 2})
		if err != nil {
			t.Fatalf("seed agent %s: %v", name, err)
		}
		return a.ID
	}
	owner, assignee, verifier := mk("a"), mk("b"), mk("qa")
	ds := service.NewDomainService(st, bus)
	dom, err := ds.Create(ctx, service.Domain{Name: "dom", GitURL: "https://e.com/d.git"})
	if err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	if _, err := ds.FreezeChecks(ctx, dom.ID, service.Checks{}, "medium"); err != nil {
		t.Fatalf("freeze domain: %v", err)
	}
	g, err := gs.Create(ctx, service.Goal{
		Title: "g", DomainID: dom.ID, AssigneeType: "agent", AssigneeID: owner, Status: "active",
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	sg, err := gs.CreateSubGoal(ctx, g.ID, "work item", "sub", assignee, verifier, nil, "agent", owner)
	if err != nil {
		t.Fatalf("create sub-goal: %v", err)
	}
	var assigneeRun string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT id FROM run WHERE sub_goal_id=? AND role='subgoal' ORDER BY rowid LIMIT 1`, sg.ID).Scan(&assigneeRun); err != nil {
		t.Fatalf("assignee run: %v", err)
	}
	// The assignee finished (machine checks passed) and the verifier run was
	// stamped terminal by Finish — but the daemon died before its reconcile.
	ts := nowStr()
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE run SET status='completed', finished_at=?, base_ref='b1', head_ref='h1' WHERE id=?`, ts, assigneeRun); err != nil {
		t.Fatalf("finish assignee run: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE sub_goal SET status='verifying' WHERE id=?`, sg.ID); err != nil {
		t.Fatalf("park sub-goal verifying: %v", err)
	}
	verifyRunID := "verify-run-" + sg.ID[:8]
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO run (id,goal_id,agent_id,run_kind,role,status,attempt,sub_goal_id,queued_at,created_at,finished_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		verifyRunID, g.ID, verifier, "worker", "verify", "completed", 1, sg.ID, ts, ts, ts); err != nil {
		t.Fatalf("insert verify run: %v", err)
	}
	return &verifySweeperFixture{
		d: d, st: st, ctx: ctx, goalID: g.ID, subGoalID: sg.ID, verifyRunID: verifyRunID,
		subGoalTitle: sg.Title, assigneeRunID: assigneeRun, verifierAgentI: verifier,
	}
}

// verifyRuns lists the sub-goal's verify runs oldest first.
func (f *verifySweeperFixture) verifyRuns(t *testing.T) []string {
	t.Helper()
	rows, err := f.st.DB().Query(
		`SELECT id FROM run WHERE sub_goal_id=? AND role='verify' ORDER BY rowid`, f.subGoalID)
	if err != nil {
		t.Fatalf("list verify runs: %v", err)
	}
	defer rows.Close()
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		ids = append(ids, id)
	}
	return ids
}

func (f *verifySweeperFixture) commentCount(t *testing.T, prefix string) int {
	t.Helper()
	rows, err := f.st.DB().Query(
		`SELECT content FROM comment WHERE goal_id=? AND author_type='system'`, f.goalID)
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	defer rows.Close()
	n := 0
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		if strings.Contains(c, prefix) {
			n++
		}
	}
	return n
}

// TestVerifySweeperRequeuesOnce: one sweep heals the crash window with exactly
// one re-dispatch (attempt 2) and one audit comment; a second sweep is a
// no-op (the attempt-2 run is queued, and the attempt count blocks a second
// requeue).
func TestVerifySweeperRequeuesOnce(t *testing.T) {
	f := newVerifySweeperFixture(t)
	f.d.sweepVerifyingSubGoals(f.ctx)

	runs := f.verifyRuns(t)
	if len(runs) != 2 {
		t.Fatalf("sweeper must re-dispatch the verifier exactly once, runs=%v", runs)
	}
	var status string
	if err := f.st.DB().QueryRow(`SELECT status FROM run WHERE id=?`, runs[1]).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "queued" {
		t.Fatalf("attempt-2 verify run must be queued, got %q", status)
	}
	if got := f.commentCount(t, "Verifier run ended without a verdict"); got != 1 {
		t.Fatalf("requeue must post exactly one system comment, got %d", got)
	}
	// Second pass: nothing to do.
	f.d.sweepVerifyingSubGoals(f.ctx)
	if got := f.verifyRuns(t); len(got) != 2 {
		t.Fatalf("second sweep must not re-dispatch, runs=%v", got)
	}
	if got := f.commentCount(t, "Verifier run ended without a verdict"); got != 1 {
		t.Fatalf("second sweep duplicated the requeue comment: %d", got)
	}
}

// TestVerifySweeperParksAfterRequeued: once the re-dispatched attempt also
// ends without a verdict, the sweeper parks the goal for a human (never a
// fabricated verdict, never an endless retry).
func TestVerifySweeperParksAfterRequeued(t *testing.T) {
	f := newVerifySweeperFixture(t)
	f.d.sweepVerifyingSubGoals(f.ctx)
	runs := f.verifyRuns(t)
	if len(runs) != 2 {
		t.Fatalf("requeue did not happen, runs=%v", runs)
	}
	// Attempt 2 also ends without a verdict, again with a lost reconcile.
	if _, err := f.st.DB().Exec(
		`UPDATE run SET status='completed', finished_at=? WHERE id=?`, nowStr(), runs[1]); err != nil {
		t.Fatalf("finish attempt 2: %v", err)
	}
	f.d.sweepVerifyingSubGoals(f.ctx)

	var goalStatus, reviewRequest string
	if err := f.st.DB().QueryRow(
		`SELECT status, COALESCE(review_request,'') FROM goal WHERE id=?`, f.goalID).Scan(&goalStatus, &reviewRequest); err != nil {
		t.Fatal(err)
	}
	if goalStatus != "review" {
		t.Fatalf("no second re-dispatch: goal must park in review, got %q", goalStatus)
	}
	if reviewRequest != "verify_no_verdict: "+f.subGoalTitle {
		t.Fatalf("review_request = %q", reviewRequest)
	}
	if got := f.verifyRuns(t); len(got) != 2 {
		t.Fatalf("park must not spawn attempt 3, runs=%v", got)
	}
	if got := f.commentCount(t, "Verification produced no verdict (run "); got != 1 {
		t.Fatalf("park must post one no-verdict comment, got %d", got)
	}
}

// TestVerifySweeperStaleAlarm: a verifier run left queued/running for over an
// hour (a hung verifier, not a dead run) raises the ONE stale alarm.
func TestVerifySweeperStaleAlarm(t *testing.T) {
	f := newVerifySweeperFixture(t)
	old := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano)
	if _, err := f.st.DB().Exec(
		`UPDATE run SET status='running', created_at=? WHERE id=?`, old, f.verifyRunID); err != nil {
		t.Fatalf("age verify run: %v", err)
	}
	f.d.sweepVerifyingSubGoals(f.ctx)
	if got := f.commentCount(t, "has been verifying for 2h"); got != 1 {
		t.Fatalf("stale alarm comment missing, got %d", got)
	}
	var alerts int
	if err := f.st.DB().QueryRow(
		`SELECT COUNT(*) FROM activity_log WHERE goal_id=? AND action='verify_stale_alert'`, f.goalID).Scan(&alerts); err != nil {
		t.Fatal(err)
	}
	if alerts != 1 {
		t.Fatalf("stale alarm activity = %d, want 1", alerts)
	}
	f.d.sweepVerifyingSubGoals(f.ctx)
	if got := f.commentCount(t, "has been verifying for"); got != 1 {
		t.Fatalf("stale alarm must fire once, got %d comments", got)
	}
}

// TestVerifySweepInterval pins the contract cadence: two minutes.
func TestVerifySweepInterval(t *testing.T) {
	if verifySweepInterval != 2*time.Minute {
		t.Fatalf("verifySweepInterval = %s, want 2m", verifySweepInterval)
	}
}

// TestVerifySweeperStopsOnCancel: the sweeper goroutine must exit with the
// daemon context (Run defers no wait, so a leaked ticker would outlive it).
func TestVerifySweeperStopsOnCancel(t *testing.T) {
	d := &Daemon{}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		d.runVerifySweeper(ctx)
		close(done)
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("runVerifySweeper did not stop on context cancellation")
	}
}

// TestVerifySweeperNilServiceNoop: the defensive guard keeps a partially
// wired daemon (tests, early boot) from panicking.
func TestVerifySweeperNilServiceNoop(t *testing.T) {
	d := &Daemon{}
	d.sweepVerifyingSubGoals(context.Background())
}
