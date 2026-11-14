package service

// Depends-on gating tests (W-RSI, 2026-11-08): a sub-goal created with
// depends_on only gets its first run when every declared dependency is
// verified; wakeDependentsTx releases gated sub-goals in the same
// transaction that verifies the last unmet dep.

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/store"
)

func subGoalRunCount(t *testing.T, st *store.Store, sgID string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM run WHERE sub_goal_id=? AND role='subgoal'`, sgID).Scan(&n); err != nil {
		t.Fatalf("count runs: %v", err)
	}
	return n
}

func TestDependsOnGatesFirstRun(t *testing.T) {
	gs, _, _, st := newTestCluster(t)
	ctx := context.Background()
	a := seedAgent(t, st, "a")
	b := seedAgent(t, st, "b")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{Title: "g", Description: "d", DomainID: domID, AssigneeType: "agent", AssigneeID: a, Status: "active"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	// sgA: ungated — run born immediately.
	sgA, err := gs.CreateSubGoal(ctx, g.ID, "step A", "first", b, "", nil, "human", "")
	if err != nil {
		t.Fatalf("create sgA: %v", err)
	}
	if n := subGoalRunCount(t, st, sgA.ID); n != 1 {
		t.Fatalf("ungated sub-goal must have exactly 1 run, got %d", n)
	}
	// sgB: gated on sgA (running) — created WITHOUT a run.
	sgB, err := gs.CreateSubGoal(ctx, g.ID, "step B", "after A", b, "", []string{sgA.ID}, "human", "")
	if err != nil {
		t.Fatalf("create gated sgB: %v", err)
	}
	if n := subGoalRunCount(t, st, sgB.ID); n != 0 {
		t.Fatalf("gated sub-goal must be born with NO run, got %d", n)
	}
	// Stored depends_on round-trips through Get/List.
	got, err := gs.GetSubGoal(ctx, sgB.ID)
	if err != nil {
		t.Fatalf("get gated sub-goal: %v", err)
	}
	if len(got.DependsOn) != 1 || got.DependsOn[0] != sgA.ID {
		t.Fatalf("depends_on round-trip broken: %v", got.DependsOn)
	}
	// Release: mark sgA verified and wake — sgB's first run is born.
	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sub_goal SET status='verified' WHERE id=?`, sgA.ID); err != nil {
		t.Fatal(err)
	}
	evs, err := gs.wakeDependentsTx(ctx, tx, g.ID, sgA.ID)
	if err != nil {
		t.Fatalf("wake: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if len(evs) == 0 {
		t.Fatalf("wake must emit sub_goal.released + run event, got 0")
	}
	if n := subGoalRunCount(t, st, sgB.ID); n != 1 {
		t.Fatalf("released sub-goal must have exactly 1 run, got %d", n)
	}
	// sgC gated on ALREADY-verified sgA → run born immediately at creation.
	sgC, err := gs.CreateSubGoal(ctx, g.ID, "step C", "deps ready", b, "", []string{sgA.ID}, "human", "")
	if err != nil {
		t.Fatalf("create sgC: %v", err)
	}
	if n := subGoalRunCount(t, st, sgC.ID); n != 1 {
		t.Fatalf("sub-goal with satisfied deps must start immediately, got %d runs", n)
	}
}

func TestDependsOnValidation(t *testing.T) {
	gs, _, _, st := newTestCluster(t)
	ctx := context.Background()
	a := seedAgent(t, st, "a")
	b := seedAgent(t, st, "b")
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{Title: "g", Description: "d", DomainID: domID, AssigneeType: "agent", AssigneeID: a, Status: "active"})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if _, err := gs.CreateSubGoal(ctx, g.ID, "x", "d", b, "", []string{"sg-nope"}, "human", ""); err == nil {
		t.Fatal("unknown dep must be rejected")
	}
	// A failed dep can never release — rejected at creation.
	sgF, err := gs.CreateSubGoal(ctx, g.ID, "f", "will fail", b, "", nil, "human", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx, `UPDATE sub_goal SET status='failed' WHERE id=?`, sgF.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := gs.CreateSubGoal(ctx, g.ID, "y", "d", b, "", []string{sgF.ID}, "human", ""); err == nil {
		t.Fatal("failed dep must be rejected")
	}
	// Dedup: duplicate deps collapse, validation still passes on satisfied deps.
	sgOK, err := gs.CreateSubGoal(ctx, g.ID, "z", "d", b, "", []string{"sg-nope", "sg-nope"}, "human", "")
	_ = sgOK
	if err == nil {
		t.Fatal("duplicates of an invalid dep must still be rejected")
	}
}

// ── Liveness + anchor (2026-11-14) ─────────────────────────────────────────
//
// A dependency-gated sub-goal is born WITHOUT a run; only wakeDependentsTx
// (on a dep VERIFY) can release it. A dependency that dies — retry chain
// exhausted → failed, or cancelled — can never verify, so without a cascade the
// child stays 'running' with no run forever: the goal's finalization guard
// counts it pending (done/review unreachable) and `goal wait` burns its 10min
// timeout. These tests pin the cascade, the retry gate and the dispatch anchor.

// gatingTestGoal creates an active goal on a frozen no-gate domain.
func gatingTestGoal(t *testing.T, gs *GoalService, st *store.Store) *Goal {
	t.Helper()
	owner := seedAgent(t, st, "owner")
	domID := seedDomain(t, st)
	g, err := gs.Create(context.Background(), Goal{
		Title: "gating goal", Description: "depends_on liveness", DomainID: domID,
		AssigneeType: "agent", AssigneeID: owner, Status: "active",
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	return g
}

// latestSubGoalRunID returns the most recently inserted run of a sub-goal
// (rowid — created_at is only second-resolution).
func latestSubGoalRunID(t *testing.T, st *store.Store, sgID string) string {
	t.Helper()
	var id string
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT id FROM run WHERE sub_goal_id=? AND role='subgoal' ORDER BY rowid DESC LIMIT 1`, sgID).Scan(&id); err != nil {
		t.Fatalf("latest sub-goal run: %v", err)
	}
	return id
}

// failSubGoalRun finishes the sub-goal's current run as failed, driving its
// retry chain one step.
func failSubGoalRun(t *testing.T, rs *RunService, st *store.Store, sgID string) {
	t.Helper()
	if err := rs.Finish(context.Background(), latestSubGoalRunID(t, st, sgID), "failed", "boom"); err != nil {
		t.Fatalf("finish sub-goal run failed: %v", err)
	}
}

// cascadeCommentCount counts the SYSTEM comments that mention a dependency id
// (the cascade's human-readable reason in the goal feed).
func cascadeCommentCount(t *testing.T, st *store.Store, goalID, depID string) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT COUNT(*) FROM comment WHERE goal_id=? AND author_type='system' AND content LIKE ?`,
		goalID, "%"+depID+"%").Scan(&n); err != nil {
		t.Fatalf("count cascade comments: %v", err)
	}
	return n
}

// forceSubGoalStatus sets a sub-goal's status/attempt directly (test fixture
// for states no normal path reaches synchronously, e.g. attempt exhaustion).
func forceSubGoalStatus(t *testing.T, st *store.Store, sgID, status string, attempt int) {
	t.Helper()
	if _, err := st.DB().ExecContext(context.Background(),
		`UPDATE sub_goal SET status=?, execution_attempt=? WHERE id=?`, status, attempt, sgID); err != nil {
		t.Fatalf("force sub-goal status: %v", err)
	}
}

// TestDependsOnDependencyFailureCascades: a dependency whose retry chain is
// exhausted (failed at attempt 3) terminally fails its still-gated dependents in
// the same transaction, records a system comment naming the dependency, lets
// `goal wait` return immediately and lets the goal finalize to done. While the
// dependency is STILL retrying (attempts 1..2), the dependent must NOT be
// cascaded — it may yet verify.
func TestDependsOnDependencyFailureCascades(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	g := gatingTestGoal(t, gs, st)
	assignee := seedAgent(t, st, "worker")
	sgA, err := gs.CreateSubGoal(ctx, g.ID, "A", "dep", assignee, "", nil, "human", "")
	if err != nil {
		t.Fatalf("create sgA: %v", err)
	}
	sgB, err := gs.CreateSubGoal(ctx, g.ID, "B", "gated", assignee, "", []string{sgA.ID}, "human", "")
	if err != nil {
		t.Fatalf("create gated sgB: %v", err)
	}
	if n := subGoalRunCount(t, st, sgB.ID); n != 0 {
		t.Fatalf("gated sub-goal must start without a run, got %d", n)
	}

	// Attempts 1 and 2: the dependency is retrying — NO cascade.
	for i := 1; i <= 2; i++ {
		failSubGoalRun(t, rs, st, sgA.ID)
		a, _ := gs.GetSubGoal(ctx, sgA.ID)
		if a.Status != "running" || a.ExecutionAttempt != i {
			t.Fatalf("after failure %d the dependency must still be retrying (running/%d), got %s/%d", i, i, a.Status, a.ExecutionAttempt)
		}
		b, _ := gs.GetSubGoal(ctx, sgB.ID)
		if b.Status != "running" {
			t.Fatalf("dependent must NOT be cascaded while the dependency is still retrying (failure %d), got %q", i, b.Status)
		}
		if n := subGoalRunCount(t, st, sgB.ID); n != 0 {
			t.Fatalf("dependent must stay run-less while the dependency retries, got %d runs", n)
		}
		if n := cascadeCommentCount(t, st, g.ID, sgA.ID); n != 0 {
			t.Fatalf("no gating-failure comment may exist while the dependency retries, got %d", n)
		}
	}

	// Attempt 3 exhausts the chain: A failed → B cascaded failed.
	failSubGoalRun(t, rs, st, sgA.ID)
	a, _ := gs.GetSubGoal(ctx, sgA.ID)
	if a.Status != "failed" {
		t.Fatalf("exhausted dependency must be failed, got %q", a.Status)
	}
	b, _ := gs.GetSubGoal(ctx, sgB.ID)
	if b.Status != "failed" {
		t.Fatalf("gated dependent of a terminally failed dependency must be cascaded to failed, got %q", b.Status)
	}
	if n := subGoalRunCount(t, st, sgB.ID); n != 0 {
		t.Fatalf("cascaded dependent must never get a run, got %d", n)
	}
	if n := cascadeCommentCount(t, st, g.ID, sgA.ID); n == 0 {
		t.Fatalf("the cascade must record a system comment naming the failed dependency %s", sgA.ID)
	}

	// `goal wait` returns immediately — nothing is pending any more.
	start := time.Now()
	children, err := gs.WaitChildren(ctx, g.ID)
	if err != nil {
		t.Fatalf("wait children: %v", err)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("WaitChildren must not block after the cascade (waited %s; the 10min timeout is the bug)", elapsed)
	}
	for _, c := range children {
		if c.Status != "verified" && c.Status != "failed" && c.Status != "cancelled" {
			t.Fatalf("WaitChildren returned a pending child %s/%s", c.ID, c.Status)
		}
	}

	// Finalization: a completed owner run now reaches done.
	ownerRun := enqueueFirst(t, rs, g)
	if err := rs.Finish(ctx, ownerRun.ID, "completed", "everything terminal"); err != nil {
		t.Fatalf("finish owner: %v", err)
	}
	after, _ := gs.Get(ctx, g.ID)
	if after.Status != "done" {
		t.Fatalf("goal must finalize after the cascade (all children terminal), got %q", after.Status)
	}
}

// TestCancelSubGoalCascadesToDependent: cancelling a dependency is terminal
// too — its gated dependents fail in the same transaction and the goal can
// finalize.
func TestCancelSubGoalCascadesToDependent(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	g := gatingTestGoal(t, gs, st)
	assignee := seedAgent(t, st, "worker")
	sgA, err := gs.CreateSubGoal(ctx, g.ID, "A", "will be cancelled", assignee, "", nil, "human", "")
	if err != nil {
		t.Fatalf("create sgA: %v", err)
	}
	sgB, err := gs.CreateSubGoal(ctx, g.ID, "B", "gated on A", assignee, "", []string{sgA.ID}, "human", "")
	if err != nil {
		t.Fatalf("create gated sgB: %v", err)
	}
	if _, err := gs.CancelSubGoal(ctx, sgA.ID); err != nil {
		t.Fatalf("cancel sgA: %v", err)
	}
	a, _ := gs.GetSubGoal(ctx, sgA.ID)
	if a.Status != "cancelled" {
		t.Fatalf("dependency must be cancelled, got %q", a.Status)
	}
	b, _ := gs.GetSubGoal(ctx, sgB.ID)
	if b.Status != "failed" {
		t.Fatalf("dependent of a cancelled dependency must be failed, got %q", b.Status)
	}
	if n := subGoalRunCount(t, st, sgB.ID); n != 0 {
		t.Fatalf("cancelled-cascade dependent must have no run, got %d", n)
	}
	if n := cascadeCommentCount(t, st, g.ID, sgA.ID); n == 0 {
		t.Fatalf("cancellation cascade must record a system comment naming %s", sgA.ID)
	}
	// A's queued run was dropped by the cancellation.
	var runStatus string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT status FROM run WHERE sub_goal_id=? AND role='subgoal' ORDER BY rowid LIMIT 1`, sgA.ID).Scan(&runStatus); err != nil {
		t.Fatalf("dependency run: %v", err)
	}
	if runStatus != "cancelled" {
		t.Fatalf("the cancelled dependency's queued run must be cancelled, got %q", runStatus)
	}
	// Finalization still works.
	ownerRun := enqueueFirst(t, rs, g)
	if err := rs.Finish(ctx, ownerRun.ID, "completed", "wrap"); err != nil {
		t.Fatalf("finish owner: %v", err)
	}
	after, _ := gs.Get(ctx, g.ID)
	if after.Status != "done" {
		t.Fatalf("goal must finalize after the cancellation cascade, got %q", after.Status)
	}
}

// TestDependsOnMultiDepOneFailed: with several dependencies, ONE dead one is
// enough — even when another is already verified.
func TestDependsOnMultiDepOneFailed(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	g := gatingTestGoal(t, gs, st)
	assignee := seedAgent(t, st, "worker")

	// dep 1: succeeds → machine-verified.
	sg1, err := gs.CreateSubGoal(ctx, g.ID, "dep-ok", "will verify", assignee, "", nil, "human", "")
	if err != nil {
		t.Fatalf("create sg1: %v", err)
	}
	if err := rs.Finish(ctx, latestSubGoalRunID(t, st, sg1.ID), "completed", "ok"); err != nil {
		t.Fatalf("finish sg1: %v", err)
	}
	if ok, _ := gs.GetSubGoal(ctx, sg1.ID); ok.Status != "verified" {
		t.Fatalf("sg1 must be machine-verified, got %q", ok.Status)
	}
	// dep 2: will exhaust its retry chain.
	sg2, err := gs.CreateSubGoal(ctx, g.ID, "dep-bad", "will fail", assignee, "", nil, "human", "")
	if err != nil {
		t.Fatalf("create sg2: %v", err)
	}
	// child gated on BOTH — born without a run (sg2 still running).
	sgC, err := gs.CreateSubGoal(ctx, g.ID, "C", "gated on ok+bad", assignee, "", []string{sg1.ID, sg2.ID}, "human", "")
	if err != nil {
		t.Fatalf("create gated sgC: %v", err)
	}
	if n := subGoalRunCount(t, st, sgC.ID); n != 0 {
		t.Fatalf("multi-dep child must start without a run, got %d", n)
	}
	for i := 0; i < maxExecutionAttempts; i++ {
		failSubGoalRun(t, rs, st, sg2.ID)
	}
	if bad, _ := gs.GetSubGoal(ctx, sg2.ID); bad.Status != "failed" {
		t.Fatalf("sg2 must be terminally failed, got %q", bad.Status)
	}
	c, _ := gs.GetSubGoal(ctx, sgC.ID)
	if c.Status != "failed" {
		t.Fatalf("child with one verified and one failed dependency must be failed, got %q", c.Status)
	}
	if n := subGoalRunCount(t, st, sgC.ID); n != 0 {
		t.Fatalf("multi-dep cascaded child must have no run, got %d", n)
	}
	if n := cascadeCommentCount(t, st, g.ID, sg2.ID); n == 0 {
		t.Fatalf("cascade comment must name the failed dependency %s", sg2.ID)
	}
}

// TestRetryGatedSubGoalRespectsDependsOn: RetrySubGoal must not bypass the
// gate — it is refused while a dependency is unverified (the error names the
// unmet deps), and accepted once the dependency has verified.
func TestRetryGatedSubGoalRespectsDependsOn(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	g := gatingTestGoal(t, gs, st)
	assignee := seedAgent(t, st, "worker")
	sgA, err := gs.CreateSubGoal(ctx, g.ID, "A", "dep", assignee, "", nil, "human", "")
	if err != nil {
		t.Fatalf("create sgA: %v", err)
	}
	sgB, err := gs.CreateSubGoal(ctx, g.ID, "B", "gated", assignee, "", []string{sgA.ID}, "human", "")
	if err != nil {
		t.Fatalf("create sgB: %v", err)
	}
	// B is failed (e.g. an earlier release that later died) but A is still
	// running/unverified → retry must be refused, no run may appear.
	forceSubGoalStatus(t, st, sgB.ID, "failed", maxExecutionAttempts)
	_, err = gs.RetrySubGoal(ctx, sgB.ID)
	if err == nil {
		t.Fatal("retry of a gated child whose dependency is not verified must be refused")
	}
	if !strings.Contains(err.Error(), "depends_on") || !strings.Contains(err.Error(), sgA.ID) {
		t.Fatalf("refusal must name the unsatisfied depends_on (got %q)", err.Error())
	}
	if n := subGoalRunCount(t, st, sgB.ID); n != 0 {
		t.Fatalf("refused retry must not enqueue a run, got %d", n)
	}
	if b, _ := gs.GetSubGoal(ctx, sgB.ID); b.Status != "failed" {
		t.Fatalf("refused retry must leave the sub-goal failed, got %q", b.Status)
	}

	// Restore the dependency: retry it, then its run verifies → child retry is
	// now legal.
	forceSubGoalStatus(t, st, sgA.ID, "failed", maxExecutionAttempts)
	if _, err := gs.RetrySubGoal(ctx, sgA.ID); err != nil {
		t.Fatalf("retry the dependency: %v", err)
	}
	if err := rs.Finish(ctx, latestSubGoalRunID(t, st, sgA.ID), "completed", "recovered"); err != nil {
		t.Fatalf("finish dependency: %v", err)
	}
	if a, _ := gs.GetSubGoal(ctx, sgA.ID); a.Status != "verified" {
		t.Fatalf("dependency must be verified after its retry, got %q", a.Status)
	}
	rearmed, err := gs.RetrySubGoal(ctx, sgB.ID)
	if err != nil {
		t.Fatalf("retry must be allowed once every dependency is verified: %v", err)
	}
	if rearmed.Status != "running" || rearmed.ExecutionAttempt != 0 {
		t.Fatalf("retried child must be running/0, got %s/%d", rearmed.Status, rearmed.ExecutionAttempt)
	}
	if n := subGoalRunCount(t, st, sgB.ID); n != 1 {
		t.Fatalf("retried child must get exactly 1 run, got %d", n)
	}
}

// TestWakeReleaseCarriesDispatchAnchor: the run released by wakeDependentsTx
// carries the gated child's dispatch comment id as its trigger_comment_id (so
// the assignee's report threads to the dispatch and the wake context names the
// leader, not "the platform"). Legacy rows fall back to ”.
func TestWakeReleaseCarriesDispatchAnchor(t *testing.T) {
	gs, _, cs, st := newTestCluster(t)
	ctx := context.Background()
	g := gatingTestGoal(t, gs, st)
	assignee := seedAgent(t, st, "worker")
	sgA, err := gs.CreateSubGoal(ctx, g.ID, "A", "dep", assignee, "", nil, "human", "")
	if err != nil {
		t.Fatalf("create sgA: %v", err)
	}
	sgB, err := gs.CreateSubGoal(ctx, g.ID, "B", "gated", assignee, "", []string{sgA.ID}, "human", "")
	if err != nil {
		t.Fatalf("create sgB: %v", err)
	}
	got, _ := gs.GetSubGoal(ctx, sgB.ID)
	if got.DispatchCommentID == "" {
		t.Fatal("CreateSubGoal must persist the dispatch comment id on the sub-goal")
	}
	var dispatchAuthors string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT author_type FROM comment WHERE id=?`, got.DispatchCommentID).Scan(&dispatchAuthors); err != nil {
		t.Fatalf("dispatch comment missing: %v", err)
	}

	tx, err := st.DB().BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := tx.ExecContext(ctx, `UPDATE sub_goal SET status='verified' WHERE id=?`, sgA.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := gs.wakeDependentsTx(ctx, tx, g.ID, sgA.ID); err != nil {
		t.Fatalf("wake: %v", err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	runID := latestSubGoalRunID(t, st, sgB.ID)
	var trigger string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT trigger_comment_id FROM run WHERE id=?`, runID).Scan(&trigger); err != nil {
		t.Fatalf("released run: %v", err)
	}
	if trigger == "" {
		t.Fatal("the released run must carry a non-empty trigger_comment_id (the dispatch anchor)")
	}
	if trigger != got.DispatchCommentID {
		t.Fatalf("released run trigger_comment_id %q != sub-goal dispatch_comment_id %q", trigger, got.DispatchCommentID)
	}
	// The assignee's report comment (stamped with the run id) threads to the
	// dispatch — this is the user-visible half of the anchor fix.
	report, err := cs.Create(ctx, Comment{
		GoalID: g.ID, AuthorType: "agent", AuthorID: assignee, Content: "report", RunID: runID,
	})
	if err != nil {
		t.Fatalf("create report comment: %v", err)
	}
	if report.ParentID != got.DispatchCommentID {
		t.Fatalf("report must thread to the dispatch comment: parent %q != %q", report.ParentID, got.DispatchCommentID)
	}
}

// TestMachineVerifyRecordsResultAndWorkflowNode: machine verification
// (VerifierID == "") must write a verification_result row in the same
// transaction as the verified transition — workflow.go derives its v: nodes
// from those rows, so without it machine-verified work had no verification node.
func TestMachineVerifyRecordsResultAndWorkflowNode(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	g := gatingTestGoal(t, gs, st)
	assignee := seedAgent(t, st, "worker")
	sg, err := gs.CreateSubGoal(ctx, g.ID, "machine", "machine-verified", assignee, "", nil, "human", "")
	if err != nil {
		t.Fatalf("create sub-goal: %v", err)
	}
	runID := latestSubGoalRunID(t, st, sg.ID)
	if err := rs.Finish(ctx, runID, "completed", "done"); err != nil {
		t.Fatalf("finish sub-goal: %v", err)
	}
	after, _ := gs.GetSubGoal(ctx, sg.ID)
	if after.Status != "verified" {
		t.Fatalf("machine sub-goal must verify, got %q", after.Status)
	}
	var verifierRunID, vstatus string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT verifier_run_id,status FROM verification_result WHERE sub_goal_id=? ORDER BY rowid DESC LIMIT 1`, sg.ID).
		Scan(&verifierRunID, &vstatus); err != nil {
		t.Fatalf("machine verification_result row missing: %v", err)
	}
	if verifierRunID != "" {
		t.Fatalf("machine verification row must have an empty verifier_run_id, got %q", verifierRunID)
	}
	if vstatus != "passed" {
		t.Fatalf("machine verification row must be passed, got %q", vstatus)
	}

	// The workflow graph reads verification_result → the v: node exists.
	graph, err := NewRSIService(st, events.NewBus()).BuildGoalWorkflow(ctx, g.ID)
	if err != nil {
		t.Fatalf("build workflow: %v", err)
	}
	found := false
	for _, n := range graph.Nodes {
		if n.Key == "v:"+sg.ID && n.Kind == "verify" {
			found = true
			if n.Status != "passed" {
				t.Fatalf("machine verification node must be passed, got %q", n.Status)
			}
		}
	}
	if !found {
		var keys []string
		for _, n := range graph.Nodes {
			keys = append(keys, n.Key)
		}
		t.Fatalf("workflow must contain the machine verification node v:%s (nodes: %v)", sg.ID, keys)
	}
}
