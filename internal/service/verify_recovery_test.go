package service

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/eushing/agentwork/internal/store"
)

// The verify-integrity contract (findings §W6 / contract v2 Task 7): no
// verify run may die silently. A verdict may arrive late (the run was
// stamped completed while the tool call was in flight); a run that ended
// without a verdict is healed exactly once (re-dispatch attempt 2, then a
// human park); a sub-goal stuck in verifying for an hour raises ONE alarm;
// and an agent may not verify its own work. These tests pin each behavior.

// verifyFixture is one active agent goal with a sub-goal parked in verifying
// and its first verify run queued — the exact production shape the heal and
// the sweeper act on. Assignee b, verifier qa, goal owner a.
type verifyFixture struct {
	ctx         context.Context
	gs          *GoalService
	rs          *RunService
	st          *store.Store
	goal        *Goal
	subGoal     *SubGoal
	verifyRunID string
}

func newVerifyFixture(t *testing.T) *verifyFixture {
	t.Helper()
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	a := seedAgent(t, st, "a")
	b := seedAgent(t, st, "b")
	qa := seedAgent(t, st, "qa")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{Title: "g", Description: "d", DomainID: domID, AssigneeType: "agent", AssigneeID: a, Status: "active"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	sg, err := gs.CreateSubGoal(ctx, g.ID, "work item", "sub", b, qa, "agent", a)
	if err != nil {
		t.Fatalf("create sub-goal: %v", err)
	}
	var assigneeRun string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT id FROM run WHERE sub_goal_id=? AND role='subgoal' ORDER BY rowid LIMIT 1`, sg.ID).Scan(&assigneeRun); err != nil {
		t.Fatalf("assignee run: %v", err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE run SET base_ref='b1', head_ref='h1' WHERE id=?`, assigneeRun); err != nil {
		t.Fatalf("stamp refs: %v", err)
	}
	if err := rs.Finish(ctx, assigneeRun, "completed", "implemented"); err != nil {
		t.Fatalf("finish assignee: %v", err)
	}
	sg, err = gs.GetSubGoal(ctx, sg.ID)
	if err != nil {
		t.Fatalf("reload sub-goal: %v", err)
	}
	if sg.Status != "verifying" {
		t.Fatalf("named verifier must park verifying, got %q", sg.Status)
	}
	var verifyRunID string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT id FROM run WHERE sub_goal_id=? AND role='verify' ORDER BY rowid DESC LIMIT 1`, sg.ID).Scan(&verifyRunID); err != nil {
		t.Fatalf("verify run must be enqueued: %v", err)
	}
	return &verifyFixture{ctx: ctx, gs: gs, rs: rs, st: st, goal: g, subGoal: sg, verifyRunID: verifyRunID}
}

// verifyRunIDs lists the sub-goal's verify runs oldest first.
func verifyRunIDs(t *testing.T, st *store.Store, subGoalID string) []string {
	t.Helper()
	rows, err := st.DB().Query(
		`SELECT id FROM run WHERE sub_goal_id=? AND role='verify' ORDER BY rowid`, subGoalID)
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

// runStatusOf reads one run's status.
func runStatusOf(t *testing.T, st *store.Store, runID string) string {
	t.Helper()
	var status string
	if err := st.DB().QueryRow(`SELECT status FROM run WHERE id=?`, runID).Scan(&status); err != nil {
		t.Fatalf("load run status: %v", err)
	}
	return status
}

// systemComments lists the goal's system comments oldest first.
func systemComments(t *testing.T, st *store.Store, goalID string) []string {
	t.Helper()
	rows, err := st.DB().Query(
		`SELECT content FROM comment WHERE goal_id=? AND author_type='system' ORDER BY rowid`, goalID)
	if err != nil {
		t.Fatalf("list comments: %v", err)
	}
	defer rows.Close()
	contents := []string{}
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatal(err)
		}
		contents = append(contents, c)
	}
	return contents
}

// activityRows lists (action, detail) pairs for the goal oldest first.
func activityRows(t *testing.T, st *store.Store, goalID string) [][2]string {
	t.Helper()
	rows, err := st.DB().Query(
		`SELECT action, detail FROM activity_log WHERE goal_id=? ORDER BY rowid`, goalID)
	if err != nil {
		t.Fatalf("list activities: %v", err)
	}
	defer rows.Close()
	out := [][2]string{}
	for rows.Next() {
		var action, detail string
		if err := rows.Scan(&action, &detail); err != nil {
			t.Fatal(err)
		}
		out = append(out, [2]string{action, detail})
	}
	return out
}

func countAction(acts [][2]string, action string) int {
	n := 0
	for _, a := range acts {
		if a[0] == action {
			n++
		}
	}
	return n
}

// actionDetail returns the FIRST detail for an action ("" when none).
func actionDetail(acts [][2]string, action string) string {
	for _, a := range acts {
		if a[0] == action {
			return a[1]
		}
	}
	return ""
}

func countCommentPrefix(comments []string, prefix string) int {
	n := 0
	for _, c := range comments {
		if strings.Contains(c, prefix) {
			n++
		}
	}
	return n
}

// stampFinished writes a verify run's terminal status + finished_at directly
// (no reconcile: the crash-window shape the runtime sweeper must heal).
func stampFinished(t *testing.T, st *store.Store, runID, status, finishedAt string) {
	t.Helper()
	if _, err := st.DB().Exec(
		`UPDATE run SET status=?, finished_at=? WHERE id=?`, status, finishedAt, runID); err != nil {
		t.Fatalf("stamp run %s %s: %v", runID, status, err)
	}
}

// TestVerifySubGoalLateVerdictWindow (7.1): the reported race — Finish stamps
// the verify run completed while the verdict tool call is still in flight.
// A verdict within verifyLateVerdictWindow must be ACCEPTED (previously the
// running-only guard rejected it and the sub-goal stayed verifying forever);
// a stale completed run, or a failed/cancelled one, must still be rejected
// with the original wording; a second verdict for the same run must not
// materialize a second Change.
func TestVerifySubGoalLateVerdictWindow(t *testing.T) {
	f := newVerifyFixture(t)
	// The run ends completed moments ago — the verdict is in flight.
	stampFinished(t, f.st, f.verifyRunID, "completed", now())
	if err := f.gs.VerifySubGoal(f.ctx, f.verifyRunID, "passed", "late but right", ""); err != nil {
		t.Fatalf("late verdict on a just-completed verify run must be accepted: %v", err)
	}
	sg, err := f.gs.GetSubGoal(f.ctx, f.subGoal.ID)
	if err != nil {
		t.Fatal(err)
	}
	if sg.Status != "verified" {
		t.Fatalf("passed verdict must verify the sub-goal, got %q", sg.Status)
	}
	var verdict string
	var changes int
	if err := f.st.DB().QueryRow(
		`SELECT status FROM verification_result WHERE verifier_run_id=?`, f.verifyRunID).Scan(&verdict); err != nil {
		t.Fatalf("verification_result must record the late verdict: %v", err)
	}
	if verdict != "passed" {
		t.Fatalf("verification_result status = %q", verdict)
	}
	if err := f.st.DB().QueryRow(
		`SELECT COUNT(*) FROM change WHERE sub_goal_id=?`, f.subGoal.ID).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if changes != 1 {
		t.Fatalf("one verdict must materialize exactly one Change, got %d", changes)
	}
	// A retried verdict for the same verify run is idempotent: rejected, and
	// no second Change appears.
	if err := f.gs.VerifySubGoal(f.ctx, f.verifyRunID, "passed", "again", ""); err == nil ||
		!strings.Contains(err.Error(), "verdict already exists") {
		t.Fatalf("duplicate verdict must be rejected with the idempotency error, got %v", err)
	}
	if err := f.st.DB().QueryRow(
		`SELECT COUNT(*) FROM change WHERE sub_goal_id=?`, f.subGoal.ID).Scan(&changes); err != nil {
		t.Fatal(err)
	}
	if changes != 1 {
		t.Fatalf("duplicate verdict must not materialize a second Change, got %d", changes)
	}
}

// TestVerifySubGoalStaleCompletedRunRejected (7.1): a completed verify run
// outside the late window is closed — the fail-closed boundary the existing
// boundary test relies on (empty finished_at included).
func TestVerifySubGoalStaleCompletedRunRejected(t *testing.T) {
	f := newVerifyFixture(t)
	old := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano)
	stampFinished(t, f.st, f.verifyRunID, "completed", old)
	err := f.gs.VerifySubGoal(f.ctx, f.verifyRunID, "passed", "too late", "")
	if err == nil || !strings.Contains(err.Error(), "no longer running") {
		t.Fatalf("stale completed verify run must be rejected, got %v", err)
	}
	if got := runStatusOf(t, f.st, f.verifyRunID); got != "completed" {
		t.Fatalf("run status changed: %q", got)
	}
	if sg, _ := f.gs.GetSubGoal(f.ctx, f.subGoal.ID); sg.Status != "verifying" {
		t.Fatalf("rejected verdict must leave the sub-goal verifying, got %q", sg.Status)
	}
}

// TestVerifySubGoalFailedRunNoVerdict (7.1): a failed verify run never judges
// (the verifier machine died — that is not a quality verdict).
func TestVerifySubGoalFailedRunNoVerdict(t *testing.T) {
	f := newVerifyFixture(t)
	stampFinished(t, f.st, f.verifyRunID, "failed", now())
	err := f.gs.VerifySubGoal(f.ctx, f.verifyRunID, "rejected", "no", "")
	if err == nil || !strings.Contains(err.Error(), "no longer running") {
		t.Fatalf("failed verify run must not accept a verdict, got %v", err)
	}
	var verdicts int
	if err := f.st.DB().QueryRow(
		`SELECT COUNT(*) FROM verification_result WHERE sub_goal_id=?`, f.subGoal.ID).Scan(&verdicts); err != nil {
		t.Fatal(err)
	}
	if verdicts != 0 {
		t.Fatalf("failed verify run must not record a verdict, got %d rows", verdicts)
	}
}

// TestReconcileVerifyRunHealsOnceThenParks (7.2): a verify run that ends
// WITHOUT a verdict is healed in the same transaction as its reconcile — one
// re-dispatch while the sub-goal is on its first attempt, then a human park
// (never a fabricated passed verdict, never an endless retry).
func TestReconcileVerifyRunHealsOnceThenParks(t *testing.T) {
	f := newVerifyFixture(t)
	// The verifier ends its turn without calling the verdict tool.
	if err := f.rs.Finish(f.ctx, f.verifyRunID, "completed", "ended without verdict"); err != nil {
		t.Fatalf("finish verify run: %v", err)
	}
	runs := verifyRunIDs(t, f.st, f.subGoal.ID)
	if len(runs) != 2 {
		t.Fatalf("first no-verdict run must be re-dispatched exactly once: runs=%v", runs)
	}
	if got := runStatusOf(t, f.st, runs[1]); got != "queued" {
		t.Fatalf("attempt-2 verify run must be queued, got %q", got)
	}
	var verifier string
	if err := f.st.DB().QueryRow(`SELECT agent_id FROM run WHERE id=?`, runs[1]).Scan(&verifier); err != nil {
		t.Fatal(err)
	}
	_ = verifier // the run targets the sub-goal's verifier (enqueueVerifyRunTx)
	comments := systemComments(t, f.st, f.goal.ID)
	if countCommentPrefix(comments, "Verifier run ended without a verdict") != 1 {
		t.Fatalf("requeue must post exactly one system comment, got %v", comments)
	}
	acts := activityRows(t, f.st, f.goal.ID)
	if countAction(acts, "verify_requeued") != 1 {
		t.Fatalf("requeue must record one verify_requeued activity, got %v", acts)
	}
	detail := actionDetail(acts, "verify_requeued")
	if !strings.Contains(detail, `"sub_goal_id":"`+f.subGoal.ID+`"`) ||
		!strings.Contains(detail, `"run_id":"`+f.verifyRunID+`"`) {
		t.Fatalf("verify_requeued detail must name the sub-goal and run, got %s", detail)
	}
	// The sub-goal still waits; the goal is untouched.
	if sg, _ := f.gs.GetSubGoal(f.ctx, f.subGoal.ID); sg.Status != "verifying" {
		t.Fatalf("requeued sub-goal must stay verifying, got %q", sg.Status)
	}
	var goalStatus string
	if err := f.st.DB().QueryRow(`SELECT status FROM goal WHERE id=?`, f.goal.ID).Scan(&goalStatus); err != nil {
		t.Fatal(err)
	}
	if goalStatus != "active" {
		t.Fatalf("requeue must not park the goal, got %q", goalStatus)
	}

	// Attempt 2 also ends without a verdict → park for a human, no attempt 3.
	if err := f.rs.Finish(f.ctx, runs[1], "completed", "ended without verdict again"); err != nil {
		t.Fatalf("finish attempt-2 verify run: %v", err)
	}
	if got := verifyRunIDs(t, f.st, f.subGoal.ID); len(got) != 2 {
		t.Fatalf("the platform must not re-dispatch a second time: runs=%v", got)
	}
	var reviewRequest string
	if err := f.st.DB().QueryRow(
		`SELECT status, COALESCE(review_request,'') FROM goal WHERE id=?`, f.goal.ID).Scan(&goalStatus, &reviewRequest); err != nil {
		t.Fatal(err)
	}
	if goalStatus != "review" {
		t.Fatalf("second no-verdict run must park the goal for a human, got %q", goalStatus)
	}
	if reviewRequest != "verify_no_verdict: work item" {
		t.Fatalf("review_request = %q", reviewRequest)
	}
	acts = activityRows(t, f.st, f.goal.ID)
	foundEnter := false
	for _, a := range acts {
		if a[0] == "entered_review" && strings.Contains(a[1], `"reason":"verify_no_verdict"`) &&
			strings.Contains(a[1], `"sub_goal_id":"`+f.subGoal.ID+`"`) {
			foundEnter = true
		}
	}
	if !foundEnter {
		t.Fatalf("park must record entered_review with reason verify_no_verdict, got %v", acts)
	}
	if countCommentPrefix(systemComments(t, f.st, f.goal.ID), "Verification produced no verdict (run ") != 1 {
		t.Fatalf("park must post one no-verdict comment, got %v", systemComments(t, f.st, f.goal.ID))
	}
	if sg, _ := f.gs.GetSubGoal(f.ctx, f.subGoal.ID); sg.Status != "verifying" {
		t.Fatalf("park leaves the sub-goal verifying (the goal is the parked unit), got %q", sg.Status)
	}
}

// TestSweepVerifyingHealsCrashWindowAndParks (7.3): the run-end reconcile can
// be lost in a crash between the terminal UPDATE and the reconcile — the
// sweeper heals that window exactly once, then parks. Re-running the sweeper
// is idempotent (attempt count is the guard).
func TestSweepVerifyingHealsCrashWindowAndParks(t *testing.T) {
	f := newVerifyFixture(t)
	// The daemon crashed after stamping the run terminal: no reconcile ran.
	stampFinished(t, f.st, f.verifyRunID, "completed", now())

	requeued, parked, alerted, err := f.gs.SweepVerifying(f.ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if requeued != 1 || parked != 0 || alerted != 0 {
		t.Fatalf("first sweep = (%d,%d,%d), want (1,0,0)", requeued, parked, alerted)
	}
	runs := verifyRunIDs(t, f.st, f.subGoal.ID)
	if len(runs) != 2 {
		t.Fatalf("sweep must re-dispatch exactly once, runs=%v", runs)
	}
	// Idempotent: the queued attempt 2 is not terminal, and the attempt count
	// blocks any second requeue.
	requeued, parked, alerted, err = f.gs.SweepVerifying(f.ctx)
	if err != nil {
		t.Fatalf("sweep 2: %v", err)
	}
	if requeued != 0 || parked != 0 || alerted != 0 {
		t.Fatalf("second sweep = (%d,%d,%d), want (0,0,0)", requeued, parked, alerted)
	}
	if got := verifyRunIDs(t, f.st, f.subGoal.ID); len(got) != 2 {
		t.Fatalf("second sweep must not re-dispatch, runs=%v", got)
	}
	if countCommentPrefix(systemComments(t, f.st, f.goal.ID), "Verifier run ended without a verdict") != 1 {
		t.Fatalf("requeue comment must not be duplicated: %v", systemComments(t, f.st, f.goal.ID))
	}

	// Attempt 2 ends without a verdict (again a lost reconcile) → park.
	stampFinished(t, f.st, runs[1], "completed", now())
	requeued, parked, alerted, err = f.gs.SweepVerifying(f.ctx)
	if err != nil {
		t.Fatalf("sweep 3: %v", err)
	}
	if requeued != 0 || parked != 1 || alerted != 0 {
		t.Fatalf("third sweep = (%d,%d,%d), want (0,1,0)", requeued, parked, alerted)
	}
	var goalStatus, reviewRequest string
	if err := f.st.DB().QueryRow(
		`SELECT status, COALESCE(review_request,'') FROM goal WHERE id=?`, f.goal.ID).Scan(&goalStatus, &reviewRequest); err != nil {
		t.Fatal(err)
	}
	if goalStatus != "review" || reviewRequest != "verify_no_verdict: work item" {
		t.Fatalf("parked goal = (%q,%q)", goalStatus, reviewRequest)
	}
	if got := verifyRunIDs(t, f.st, f.subGoal.ID); len(got) != 2 {
		t.Fatalf("park must not re-dispatch again, runs=%v", got)
	}
	// Park is idempotent too: the goal is already review, so a re-run sweep
	// reports nothing and posts no duplicate card.
	requeued, parked, alerted, err = f.gs.SweepVerifying(f.ctx)
	if err != nil {
		t.Fatalf("sweep 4: %v", err)
	}
	if requeued != 0 || parked != 0 || alerted != 0 {
		t.Fatalf("fourth sweep = (%d,%d,%d), want (0,0,0)", requeued, parked, alerted)
	}
	if countCommentPrefix(systemComments(t, f.st, f.goal.ID), "Verification produced no verdict (run ") != 1 {
		t.Fatalf("park comment must not be duplicated: %v", systemComments(t, f.st, f.goal.ID))
	}
}

// TestSweepVerifyingStaleAlertOnce (7.3): a sub-goal may sit verifying for an
// hour with no verdict while the run itself is still alive (a hung verifier).
// The sweeper raises exactly ONE alarm (comment + activity), deduped by the
// existing activity row — no new column.
func TestSweepVerifyingStaleAlertOnce(t *testing.T) {
	f := newVerifyFixture(t)
	old := time.Now().UTC().Add(-2 * time.Hour).Format(time.RFC3339Nano)
	if _, err := f.st.DB().Exec(
		`UPDATE run SET created_at=? WHERE id=?`, old, f.verifyRunID); err != nil {
		t.Fatalf("age verify run: %v", err)
	}
	requeued, parked, alerted, err := f.gs.SweepVerifying(f.ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if requeued != 0 || parked != 0 || alerted != 1 {
		t.Fatalf("stale sweep = (%d,%d,%d), want (0,0,1)", requeued, parked, alerted)
	}
	comments := systemComments(t, f.st, f.goal.ID)
	if countCommentPrefix(comments, "Work item work item has been verifying for 2h") != 1 {
		t.Fatalf("stale alarm comment missing or malformed: %v", comments)
	}
	acts := activityRows(t, f.st, f.goal.ID)
	if countAction(acts, "verify_stale_alert") != 1 {
		t.Fatalf("stale alarm must record one verify_stale_alert activity, got %v", acts)
	}
	if detail := actionDetail(acts, "verify_stale_alert"); !strings.Contains(detail, `"sub_goal_id":"`+f.subGoal.ID+`"`) {
		t.Fatalf("stale alert detail must name the sub-goal, got %s", detail)
	}
	// One alarm only: the second sweep finds the dedupe row.
	requeued, parked, alerted, err = f.gs.SweepVerifying(f.ctx)
	if err != nil {
		t.Fatalf("sweep 2: %v", err)
	}
	if requeued != 0 || parked != 0 || alerted != 0 {
		t.Fatalf("second stale sweep = (%d,%d,%d), want (0,0,0)", requeued, parked, alerted)
	}
	if got := countCommentPrefix(systemComments(t, f.st, f.goal.ID), "has been verifying for"); got != 1 {
		t.Fatalf("stale alarm comment duplicated: %d", got)
	}
}

// TestSweepVerifyingStaleWithoutVerifyRun (7.3): a verifying sub-goal with no
// verify run at all (crash between the verifying stamp and the verifier
// enqueue — pre-P0-3 data) falls back to the sub-goal's created_at for the
// stale window.
func TestSweepVerifyingStaleWithoutVerifyRun(t *testing.T) {
	gs, _, _, st := newTestCluster(t)
	ctx := context.Background()
	a := seedAgent(t, st, "a")
	b := seedAgent(t, st, "b")
	qa := seedAgent(t, st, "qa")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{Title: "g", Description: "d", DomainID: domID, AssigneeType: "agent", AssigneeID: a, Status: "active"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	old := time.Now().UTC().Add(-3 * time.Hour).Format(time.RFC3339Nano)
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO sub_goal (id,goal_id,title,description,assignee_id,verifier_id,status,execution_attempt,quality_iteration,created_at)
		 VALUES (?,?,?,?,?,?,'verifying',0,0,?)`,
		newID(), g.ID, "orphan item", "d", b, qa, old); err != nil {
		t.Fatalf("insert verifying sub-goal: %v", err)
	}
	requeued, parked, alerted, err := gs.SweepVerifying(ctx)
	if err != nil {
		t.Fatalf("sweep: %v", err)
	}
	if requeued != 0 || parked != 0 || alerted != 1 {
		t.Fatalf("sweep = (%d,%d,%d), want (0,0,1)", requeued, parked, alerted)
	}
	comments := systemComments(t, st, g.ID)
	if countCommentPrefix(comments, "Work item orphan item has been verifying for 3h") != 1 {
		t.Fatalf("stale alarm must use the sub-goal's created_at: %v", comments)
	}
}

// TestCreateSubGoalRejectsSelfVerification (7.4): the player-referee rule —
// an agent cannot verify its own sub-goal; a verifier equal to the GOAL owner
// stays allowed (the owner may referee its assignee's work).
func TestCreateSubGoalRejectsSelfVerification(t *testing.T) {
	gs, _, _, st := newTestCluster(t)
	ctx := context.Background()
	a := seedAgent(t, st, "a")
	b := seedAgent(t, st, "b")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{Title: "g", Description: "d", DomainID: domID, AssigneeType: "agent", AssigneeID: a, Status: "active"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	_, err = gs.CreateSubGoal(ctx, g.ID, "self-verified", "d", a, a, "agent", a)
	if err == nil || !strings.Contains(err.Error(), "player-referee") {
		t.Fatalf("self-verification must be rejected, got %v", err)
	}
	var subs int
	if err := st.DB().QueryRow(
		`SELECT COUNT(*) FROM sub_goal WHERE goal_id=?`, g.ID).Scan(&subs); err != nil {
		t.Fatal(err)
	}
	if subs != 0 {
		t.Fatalf("rejected self-verification must not create a sub-goal, got %d", subs)
	}
	// Verifier == goal owner (a) with a DIFFERENT assignee (b) is legal.
	if _, err := gs.CreateSubGoal(ctx, g.ID, "refereed", "d", b, a, "agent", a); err != nil {
		t.Fatalf("goal owner as verifier of another agent's work must be allowed: %v", err)
	}
}

// TestReconcileVerifyRunFailedParksImmediately (7.2): a verify run that FAILS
// (the verifier machine died) is not a quality verdict — no re-dispatch, park
// for a human right away.
func TestReconcileVerifyRunFailedParksImmediately(t *testing.T) {
	f := newVerifyFixture(t)
	if err := f.rs.Finish(f.ctx, f.verifyRunID, "failed", "verifier machine died"); err != nil {
		t.Fatalf("finish verify run failed: %v", err)
	}
	if got := verifyRunIDs(t, f.st, f.subGoal.ID); len(got) != 1 {
		t.Fatalf("failed verify run must not be re-dispatched, runs=%v", got)
	}
	var goalStatus, reviewRequest string
	if err := f.st.DB().QueryRow(
		`SELECT status, COALESCE(review_request,'') FROM goal WHERE id=?`, f.goal.ID).Scan(&goalStatus, &reviewRequest); err != nil {
		t.Fatal(err)
	}
	if goalStatus != "review" || reviewRequest != "verify_no_verdict: work item" {
		t.Fatalf("failed verify run must park = (%q,%q)", goalStatus, reviewRequest)
	}
	if countCommentPrefix(systemComments(t, f.st, f.goal.ID), "Verification produced no verdict (run ") != 1 {
		t.Fatalf("park comment missing: %v", systemComments(t, f.st, f.goal.ID))
	}
	if countAction(activityRows(t, f.st, f.goal.ID), "verify_requeued") != 0 {
		t.Fatal("failed verify run must not record a requeue")
	}
}

// TestReconcileVerifyRunVerdictPresentNoAction (7.2): the verdict landed
// normally before the run's reconcile — the heal must not touch the goal or
// post audit rows.
func TestReconcileVerifyRunVerdictPresentNoAction(t *testing.T) {
	f := newVerifyFixture(t)
	if _, err := f.st.DB().Exec(
		`UPDATE run SET status='running' WHERE id=?`, f.verifyRunID); err != nil {
		t.Fatal(err)
	}
	if err := f.gs.VerifySubGoal(f.ctx, f.verifyRunID, "rejected", "needs work", ""); err != nil {
		t.Fatalf("verdict rejected: %v", err)
	}
	// The verifier ends its turn after the verdict; the reconcile runs with a
	// verification_result already recorded for the sub-goal.
	if err := f.rs.Finish(f.ctx, f.verifyRunID, "completed", "rejected verdict given"); err != nil {
		t.Fatalf("finish verify run: %v", err)
	}
	if countCommentPrefix(systemComments(t, f.st, f.goal.ID), "Verification produced no verdict") != 0 {
		t.Fatalf("verdict-present reconcile must not park: %v", systemComments(t, f.st, f.goal.ID))
	}
	runs := verifyRunIDs(t, f.st, f.subGoal.ID)
	if len(runs) != 1 {
		t.Fatalf("verdict-present reconcile must not re-dispatch, runs=%v", runs)
	}
	var goalStatus string
	if err := f.st.DB().QueryRow(`SELECT status FROM goal WHERE id=?`, f.goal.ID).Scan(&goalStatus); err != nil {
		t.Fatal(err)
	}
	if goalStatus != "active" {
		t.Fatalf("verdict-present reconcile must leave the goal active, got %q", goalStatus)
	}
}
