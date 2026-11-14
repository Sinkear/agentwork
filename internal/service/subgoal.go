package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/logging"
)

// SubGoal is a work item split off a goal (v2 model, 决策 6-1) — NOT a child
// goal: no parent recursion, no own deliver/verification terminal semantics.
// The goal stays the sole lifecycle authority; a sub-goal carries work
// responsibility (assignee) + quality responsibility (verifier, ” = machine).
// execution_attempt (machine retries ≤3) and quality_iteration (verifier
// rejects, unbounded) are the authoritative counters (决策 6-9) — run.attempt
// is just the instance ordinal.
type SubGoal struct {
	ID                string   `json:"id"`
	GoalID            string   `json:"goal_id"`
	Title             string   `json:"title"`
	Description       string   `json:"description"`
	AssigneeID        string   `json:"assignee_id"`
	VerifierID        string   `json:"verifier_id"`         // '' = machine (domain verify commands)
	DependsOn         []string `json:"depends_on"`          // first run waits until ALL are verified (W-RSI gating)
	DispatchCommentID string   `json:"dispatch_comment_id"` // the leader's dispatch comment for this work item ('' = legacy row): the run released LATER by wakeDependentsTx carries it as run.trigger_comment_id, so a gated work item's report still threads to its dispatch
	Status            string   `json:"status"`              // running|verifying|verified|rejected|cancelled|failed（pending/done 无写入路径，见 schema.sql）
	ExecutionAttempt  int      `json:"execution_attempt"`
	QualityIteration  int      `json:"quality_iteration"`
	CreatedAt         string   `json:"created_at"`
}

// maxExecutionAttempts bounds the sub-goal's machine retry chain (决策 6-5,
// the execution_attempt counter — verification failures and run failures
// share it; verifier rejects use quality_iteration instead).
const maxExecutionAttempts = 3

// maxActiveSubGoals bounds concurrent work items per goal (决策 6-8: fan-out
// ≤20 ACTIVE — history is unlimited, cancelled/failed/verified don't count).
const maxActiveSubGoals = 20

// CreateSubGoal splits a work item off a goal (决策 6-1): a sub_goal row with
// its own assignee (an agent), started running immediately with a sub-goal
// run enqueued. The goal's owner/status are untouched. Only the goal's owner
// (agent run), a squad leader, or a human may create (checked by the caller —
// the MCP tool's requireOwnerOf; the service trusts the caller identity).
func (s *GoalService) CreateSubGoal(ctx context.Context, goalID, title, description, assigneeID, verifierID string, dependsOn []string, createdByType, createdByID string) (*SubGoal, error) {
	if title == "" {
		return nil, NewFieldRequiredError("title")
	}
	if assigneeID == "" {
		return nil, NewFieldRequiredError("assignee_id")
	}
	// Fan-out cap (决策 6-8): at most maxActiveSubGoals non-terminal sub-goals
	// per goal — an agent must not turn one goal into an unbounded work farm.
	var active int
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sub_goal WHERE goal_id=? AND status NOT IN ('verified','cancelled','failed')`,
		goalID).Scan(&active); err != nil {
		return nil, fmt.Errorf("count active sub-goals: %w", err)
	}
	if active >= maxActiveSubGoals {
		return nil, NewValidationError(fmt.Sprintf("too many active sub-goals (%d max) — resolve existing ones first", maxActiveSubGoals))
	}

	// The goal must exist and be ACTIVE (P1-1): review is an execution freeze
	// point (决策 2-3 — new work must not mutate the state under the human's
	// judgment), terminal goals have no execution left to split, and backlog
	// is not dispatched yet. Only active allows new work — the state machine
	// must not self-loop.
	var gStatus string
	err := s.st.DB().QueryRowContext(ctx, `SELECT status FROM goal WHERE id=?`, goalID).Scan(&gStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("load goal: %w", err)
	}
	if gStatus != "active" {
		return nil, NewValidationError(fmt.Sprintf("cannot create a sub-goal: the goal is %s (only active goals can be split)", gStatus))
	}
	if err := mustExist(ctx, s.st, `SELECT COUNT(*) FROM agent WHERE id=?`, assigneeID, "assignee agent"); err != nil {
		return nil, err
	}
	// REVIEWER ONLY RULE: a squad's reviewer members review — they are never
	// handed work items. The leader must dispatch to non-reviewer members;
	// a misconfigured squad (every member is a reviewer) surfaces the error
	// here instead of producing a worker who later reviews its own output
	// (the player-referee trap).
	var squadID string
	_ = s.st.DB().QueryRowContext(ctx,
		`SELECT assignee_id FROM goal WHERE id=? AND assignee_type='squad'`, goalID).Scan(&squadID)
	if squadID != "" {
		var reviewer int
		if err := s.st.DB().QueryRowContext(ctx,
			`SELECT COUNT(*) FROM squad_member WHERE squad_id=? AND member_type='agent' AND member_id=? AND LOWER(TRIM(role))='reviewer'`,
			squadID, assigneeID).Scan(&reviewer); err != nil {
			return nil, fmt.Errorf("check reviewer role: %w", err)
		}
		if reviewer > 0 {
			return nil, NewValidationError("the assignee is a REVIEWER of this goal's squad — reviewers only review; dispatch work to a non-reviewer member")
		}
	}
	if verifierID != "" {
		if err := mustExist(ctx, s.st, `SELECT COUNT(*) FROM agent WHERE id=?`, verifierID, "verifier agent"); err != nil {
			return nil, err
		}
	}
	// Depends-on gating (W-RSI 2026-11-08): normalize declared dependencies.
	// A dep must exist, belong to the same goal, and not be terminal-failed;
	// the graph stays acyclic by construction (deps predate the new node).
	deps := make([]string, 0, len(dependsOn))
	seenDeps := map[string]bool{}
	for _, d := range dependsOn {
		d = strings.TrimSpace(d)
		if d == "" || seenDeps[d] {
			continue
		}
		seenDeps[d] = true
		deps = append(deps, d)
	}
	for _, d := range deps {
		var depGoal, depStatus string
		err := s.st.DB().QueryRowContext(ctx, `SELECT goal_id,status FROM sub_goal WHERE id=?`, d).Scan(&depGoal, &depStatus)
		if errors.Is(err, sql.ErrNoRows) {
			return nil, NewValidationError(fmt.Sprintf("depends_on: sub-goal %q not found", d))
		}
		if err != nil {
			return nil, fmt.Errorf("check dependency %s: %w", d, err)
		}
		if depGoal != goalID {
			return nil, NewValidationError("depends_on: sub-goal " + d + " belongs to another goal")
		}
		if depStatus == "failed" || depStatus == "cancelled" {
			return nil, NewValidationError("depends_on: sub-goal " + d + " is " + depStatus + " — it can never release this work item")
		}
	}
	ts := now()
	sg := SubGoal{
		ID:          newID(),
		GoalID:      goalID,
		Title:       title,
		Description: description,
		AssigneeID:  assigneeID,
		VerifierID:  verifierID,
		DependsOn:   deps,
		Status:      "running",
		CreatedAt:   ts,
	}
	if s.runSvc == nil {
		return nil, errors.New("goalSvc.runSvc not wired")
	}
	// P0-2: the sub_goal row and its FIRST run are born in ONE transaction —
	// a crash between the two inserts would leave a ghost sub-goal (running,
	// no run, stuck forever). The run event is returned and published only
	// after the commit (invariant 13).
	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO sub_goal (id,goal_id,title,description,assignee_id,verifier_id,status,execution_attempt,quality_iteration,created_at,depends_on)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?)`,
		sg.ID, sg.GoalID, sg.Title, sg.Description, sg.AssigneeID, sg.VerifierID, sg.Status, sg.ExecutionAttempt, sg.QualityIteration, sg.CreatedAt, encodeDeps(deps)); err != nil {
		return nil, fmt.Errorf("insert sub_goal: %w", err)
	}
	// P2-1 (决策 6-15⑩): the dispatch comment is born WITH the sub-goal and
	// its first run — the run carries it as trigger_comment_id, so the
	// assignee's report threads to the dispatch (the leader→assignee causal
	// chain), and the assignee's feed always contains the dispatch BEFORE the
	// run can claim (no dispatch-comment race).
	label, err := s.assigneeLabel(ctx, tx, "agent", sg.AssigneeID)
	if err != nil {
		return nil, fmt.Errorf("resolve assignee label: %w", err)
	}
	dispatchID := newID()
	// The dispatch comment is PURE MATERIALS (决策 6-18, 2026-08 修订):
	// the leader's own words — mention + title + one-line summary. No
	// platform template, no language branch (the work-item wake line carries
	// the agent's task; the comment serves the human feed + the causal
	// anchor). The full description is the assignee's prompt — repeating it
	// in the feed doubles the text for every reader.
	dispatchContent := fmt.Sprintf("[@%s](mention://agent/%s) **%s**", label, sg.AssigneeID, sg.Title)
	summary := strings.TrimSpace(sg.Description)
	if i := strings.IndexByte(summary, '\n'); i >= 0 {
		summary = summary[:i]
	}
	summary = strings.TrimSpace(summary)
	if r := []rune(summary); len(r) > 120 {
		// Cut at a word boundary, not mid-word (a live mid-sentence "…"
		// stayed in every replayed history as a broken fragment).
		cut := string(r[:120])
		if i := strings.LastIndexAny(cut, "，。；、,.；: "); i > 60 {
			cut = cut[:i]
		}
		summary = cut + "…"
	}
	if summary != "" {
		dispatchContent += " — " + summary
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO comment (id,goal_id,author_type,author_id,parent_id,content,created_at) VALUES (?,?,?,?,NULL,?,?)`,
		dispatchID, sg.GoalID, createdByType, createdByID, dispatchContent, ts); err != nil {
		return nil, fmt.Errorf("insert dispatch comment: %w", err)
	}
	// Anchor persistence (gating liveness, 2026-11-14): a GATED sub-goal gets
	// NO run at creation, so the dispatch comment id has nowhere to live on a
	// run row. Persist it here, in the same transaction, and wakeDependentsTx
	// will stamp it as the released run's trigger_comment_id — otherwise that
	// run is born with trigger='' and the assignee's report cannot thread to
	// the dispatch (comment.create falls back to the goal root, and the wake
	// context degrades to "the platform").
	if _, err := tx.ExecContext(ctx,
		`UPDATE sub_goal SET dispatch_comment_id=? WHERE id=?`, dispatchID, sg.ID); err != nil {
		return nil, fmt.Errorf("stamp dispatch comment id: %w", err)
	}
	sg.DispatchCommentID = dispatchID
	// Depends-on gating: the first run is born only when every declared
	// dependency is already verified. Otherwise the sub-goal waits (no run
	// row ⇒ nothing claimable) and wakeDependentsTx releases it in the
	// transaction that verifies the last unmet dep.
	depsReady := true
	if len(deps) > 0 {
		var verifiedCount int
		if err := tx.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM sub_goal WHERE goal_id=? AND status='verified' AND id IN (`+depsPlaceholders(len(deps))+`)`,
			append([]any{goalID}, depsArgs(deps)...)...).Scan(&verifiedCount); err != nil {
			return nil, fmt.Errorf("check dependencies: %w", err)
		}
		depsReady = verifiedCount == len(deps)
	}
	var runEv *events.Event
	if depsReady {
		_, ev, err := s.runSvc.enqueueSubGoalRunTx(ctx, tx, sg.ID, dispatchID)
		if err != nil {
			return nil, fmt.Errorf("enqueue sub-goal run: %w", err)
		}
		runEv = ev
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	logging.Infof("sub-goal: created %q (goal %s) → assignee=%s verifier=%s deps=%d gated=%v", sg.Title, sg.GoalID, sg.AssigneeID, sg.VerifierID, len(deps), !depsReady)
	s.bus.Publish(ctx, events.Event{Topic: "sub_goal.created", Payload: map[string]any{
		"goal_id": goalID, "sub_goal_id": sg.ID,
	}})
	if runEv != nil {
		s.bus.Publish(ctx, *runEv)
	}
	return &sg, nil
}

// GetSubGoal loads one sub-goal row.
func (s *GoalService) GetSubGoal(ctx context.Context, id string) (*SubGoal, error) {
	var sg SubGoal
	var depsRaw string
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT id,goal_id,title,description,assignee_id,verifier_id,status,execution_attempt,quality_iteration,created_at,COALESCE(depends_on,'[]'),COALESCE(dispatch_comment_id,'')
		 FROM sub_goal WHERE id=?`, id).
		Scan(&sg.ID, &sg.GoalID, &sg.Title, &sg.Description, &sg.AssigneeID, &sg.VerifierID, &sg.Status, &sg.ExecutionAttempt, &sg.QualityIteration, &sg.CreatedAt, &depsRaw, &sg.DispatchCommentID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	sg.DependsOn = decodeDeps(depsRaw)
	return &sg, nil
}

// ListSubGoals lists a goal's sub-goals (oldest first).
func (s *GoalService) ListSubGoals(ctx context.Context, goalID string) ([]SubGoal, error) {
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id,goal_id,title,description,assignee_id,verifier_id,status,execution_attempt,quality_iteration,created_at,COALESCE(depends_on,'[]'),COALESCE(dispatch_comment_id,'')
		 FROM sub_goal WHERE goal_id=? ORDER BY created_at`, goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SubGoal{}
	for rows.Next() {
		var sg SubGoal
		var depsRaw string
		if err := rows.Scan(&sg.ID, &sg.GoalID, &sg.Title, &sg.Description, &sg.AssigneeID, &sg.VerifierID, &sg.Status, &sg.ExecutionAttempt, &sg.QualityIteration, &sg.CreatedAt, &depsRaw, &sg.DispatchCommentID); err != nil {
			return nil, err
		}
		sg.DependsOn = decodeDeps(depsRaw)
		out = append(out, sg)
	}
	return out, rows.Err()
}

// ReconcileSubGoalRun is the sub-goal layer's arbitration (决策 6-1/6-3/6-5):
// a sub-goal run's terminal outcome advances the SUB-GOAL, never the goal.
//
//	completed → the daemon already ran machine verification (a completed run
//	            reached here only after it passed) → stamp verified + create
//	            the Change and its first Revision ATOMICALLY (Ready ⇔ a
//	            persisted Revision) — the daemon stamped base_ref/head_ref.
//	failed    → execution_attempt++, retry the assignee (≤3), else failed.
//	cancelled → same bounded retry chain (a watchdog stall is a machine
//	            hiccup for a sub-goal — the goal-level "human decides" loop
//	            exists for owner runs; sub-goal retries are cheap and bounded).
//
// ReconcileSubGoalRun runs under withBusyRetry (see ReconcileGoal): the
// snapshot-upgrade race is equally possible on the sub-goal latch edges.
func (s *GoalService) ReconcileSubGoalRun(ctx context.Context, rc goalRunContext) error {
	return withBusyRetry(func() error { return s.reconcileSubGoalRunOnce(ctx, rc) })
}

func (s *GoalService) reconcileSubGoalRunOnce(ctx context.Context, rc goalRunContext) error {
	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var sg SubGoal
	err = tx.QueryRowContext(ctx,
		`SELECT id,goal_id,title,description,assignee_id,verifier_id,status,execution_attempt,quality_iteration,created_at
		 FROM sub_goal WHERE id=?`, rc.SubGoalID).
		Scan(&sg.ID, &sg.GoalID, &sg.Title, &sg.Description, &sg.AssigneeID, &sg.VerifierID, &sg.Status, &sg.ExecutionAttempt, &sg.QualityIteration, &sg.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // sub-goal vanished — nothing to reconcile
	}
	if err != nil {
		return fmt.Errorf("load sub-goal for reconcile: %w", err)
	}

	var evs []events.Event
	switch rc.Status {
	case "completed":
		// The run's report is NOT posted by the platform — the agent
		// communicates results via `goal comment`.
		// An agent verifier was named (决策 6-5): machine checks passed, now
		// the QUALITY gate — park in verifying and enqueue the verifier run.
		// The verdict tool (verify_sub_goal) makes the verified/rejected
		// transition; this run's job ends here.
		if sg.VerifierID != "" {
			res, err := tx.ExecContext(ctx,
				`UPDATE sub_goal SET status='verifying' WHERE id=? AND status IN ('done','running')`, sg.ID)
			if err != nil {
				return fmt.Errorf("park sub-goal verifying: %w", err)
			}
			if n, _ := res.RowsAffected(); n == 0 {
				break // already terminal — no change
			}
			evs = append(evs, events.Event{Topic: "sub_goal.verifying", Payload: map[string]any{
				"goal_id": sg.GoalID, "sub_goal_id": sg.ID,
			}})
			// P0-3 (决策 6-13): the verifier run is born IN this transaction —
			// a crash after the commit can no longer leave a verifying
			// sub-goal with no verifier run. The run event is published after
			// the commit below (invariant 13).
			_, runEv, err := s.runSvc.enqueueVerifyRunTx(ctx, tx, sg.ID)
			if err != nil {
				return fmt.Errorf("enqueue verifier run: %w", err)
			}
			if runEv != nil {
				evs = append(evs, *runEv)
			}
			break
		}
		// Conditional transition: done → verified. (The daemon's verification
		// gate guarantees a completed run passed machine checks.)
		res, err := tx.ExecContext(ctx,
			`UPDATE sub_goal SET status='verified' WHERE id=? AND status IN ('done','running')`, sg.ID)
		if err != nil {
			return fmt.Errorf("verify sub-goal: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			break // already terminal (cancelled/rejected raced) — no change
		}
		// Machine verification audit row (2026-11-14): the human/agent verifier
		// path records a verification_result row in VerifySubGoal, but the
		// MACHINE path promoted verified with NO row — so the workflow graph
		// (which derives its v: nodes from verification_result) showed no
		// verification node for machine-verified work. Write the audit row in
		// the SAME transaction as the verified transition (P0-1); the row's
		// empty verifier_run_id marks it as the machine round (schema contract:
		// "machine verification records one row per run-end check").
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO verification_result (id,goal_id,sub_goal_id,verifier_run_id,status,summary,evidence,created_at) VALUES (?,?,?,'','passed',?,?,?)`,
			newID(), sg.GoalID, sg.ID, "machine verification passed", "machine gate passed (run "+rc.RunID+")", now()); err != nil {
			return fmt.Errorf("insert machine verification_result: %w", err)
		}
		changeID, err := s.materializeChangeTx(ctx, tx, sg, rc.BaseRef, rc.HeadRef)
		if err != nil {
			return err
		}
		evs = append(evs, events.Event{Topic: "sub_goal.verified", Payload: map[string]any{
			"goal_id": sg.GoalID, "sub_goal_id": sg.ID, "change_id": changeID,
		}})
		// The change is now READY for integration (v2 §11: change.ready — the
		// frontend change panel and the attention badge react to it). A
		// no-code sub-goal (materialize returned '') has no change to report.
		if changeID != "" {
			evs = append(evs, events.Event{Topic: "change.ready", Payload: map[string]any{
				"goal_id": sg.GoalID, "change_id": changeID, "sub_goal_id": sg.ID,
			}})
		}
		// Depends-on gating: release gated siblings whose deps are now ALL
		// verified (their first runs are born in THIS transaction, P0-3).
		wakeEvs, err := s.wakeDependentsTx(ctx, tx, sg.GoalID, sg.ID)
		if err != nil {
			return err
		}
		evs = append(evs, wakeEvs...)
	case "failed", "cancelled":
		if _, err := tx.ExecContext(ctx,
			`UPDATE sub_goal SET execution_attempt=execution_attempt+1 WHERE id=? AND status IN ('running','done')`,
			sg.ID); err != nil {
			return fmt.Errorf("bump execution_attempt: %w", err)
		}
		var status string
		var attempt int
		if err := tx.QueryRowContext(ctx, `SELECT status, execution_attempt FROM sub_goal WHERE id=?`, sg.ID).Scan(&status, &attempt); err != nil {
			return fmt.Errorf("reload sub-goal after failure: %w", err)
		}
		// Only a still-RUNNING sub-goal continues its retry chain. A CANCELLED
		// sub-goal must never be resurrected: CancelSubGoal cancelled its queued
		// runs in the cancel transaction, and the daemon then kills the in-flight
		// one — that run's late terminal outcome lands right here, and without
		// this guard the bounded retry would enqueue a fresh run on a work item
		// the owner already cancelled. (Any other raced terminal state is the
		// same: no retry, no status write.)
		if status != "running" {
			break
		}
		if attempt < maxExecutionAttempts {
			// Retry the assignee — the retry run is born in THIS transaction
			// (P0-3): a crash can no longer lose the successor run.
			evs = append(evs, events.Event{Topic: "sub_goal.retrying", Payload: map[string]any{
				"goal_id": sg.GoalID, "sub_goal_id": sg.ID, "execution_attempt": attempt,
			}})
			_, runEv, err := s.runSvc.enqueueSubGoalRunTx(ctx, tx, sg.ID, "")
			if err != nil {
				return fmt.Errorf("enqueue retry run: %w", err)
			}
			if runEv != nil {
				evs = append(evs, *runEv)
			}
		} else {
			if _, err := tx.ExecContext(ctx,
				`UPDATE sub_goal SET status='failed' WHERE id=? AND status='running'`, sg.ID); err != nil {
				return fmt.Errorf("fail sub-goal: %w", err)
			}
			evs = append(evs, events.Event{Topic: "sub_goal.failed", Payload: map[string]any{
				"goal_id": sg.GoalID, "sub_goal_id": sg.ID, "execution_attempt": attempt,
			}})
			// LIVENESS (2026-11-14): the retry chain is exhausted — this
			// dependency can never verify, so every still-gated dependent would
			// otherwise wait forever (no run is born for it, nothing reconciles
			// it, the goal finalization guard counts it pending, and `goal wait`
			// burns its 10min timeout). Cascade the failure to those dependents
			// in THIS transaction. A dependency that merely failed an attempt
			// (attempt < max, retry above) is NOT cascaded — it may still verify.
			cascadeEvs, err := s.failGatedDependentsTx(ctx, tx, sg.GoalID, sg.ID)
			if err != nil {
				return err
			}
			evs = append(evs, cascadeEvs...)
		}
	}

	// Stamp the run's reconciled_at in the SAME transaction as the sub-goal
	// transition (P0-1, 决策 6-11): the terminal outcome and its reconcile
	// are one atomic unit — a crash before this commit leaves
	// reconciled_at='' and the startup replay re-runs it.
	if _, err := tx.ExecContext(ctx, `UPDATE run SET reconciled_at=? WHERE id=?`, now(), rc.RunID); err != nil {
		return fmt.Errorf("stamp reconciled_at: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, e := range evs {
		s.bus.Publish(ctx, e)
	}
	return nil
}

// materializeChangeTx makes the sub-goal's deliverable concrete (决策 6-3),
// called inside the verifying/verified transition's transaction:
//
//	no-code sub-goal (决策 6-8): head empty or base == head — the deliverable
//	  is the run's report in the feed, NO Change is born ('' returned);
//	conflict rework: the sub-goal's previous Change was conflicted at
//	  integration — append revision seq N+1 on the SAME change (base = the
//	  new integration base) and return it to ready;
//	fresh round: create the Change and its first Revision atomically
//	  (Ready ⇔ a persisted Revision).
func (s *GoalService) materializeChangeTx(ctx context.Context, tx *sql.Tx, sg SubGoal, baseRef, headRef string) (string, error) {
	if headRef == "" || baseRef == headRef {
		return "", nil
	}
	var conflictChangeID string
	err := tx.QueryRowContext(ctx,
		`SELECT id FROM change WHERE sub_goal_id=? AND status='conflict' ORDER BY created_at DESC LIMIT 1`,
		sg.ID).Scan(&conflictChangeID)
	if err == nil && conflictChangeID != "" {
		var seq int
		if err := tx.QueryRowContext(ctx,
			`SELECT COALESCE(MAX(seq),0) FROM change_revision WHERE change_id=?`, conflictChangeID).Scan(&seq); err != nil {
			return "", fmt.Errorf("load revision seq: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO change_revision (id,change_id,seq,base_ref,head_ref,created_at) VALUES (?,?,?,?,?,?)`,
			newID(), conflictChangeID, seq+1, baseRef, headRef, now()); err != nil {
			return "", fmt.Errorf("append change revision: %w", err)
		}
		if _, err := tx.ExecContext(ctx,
			`UPDATE change SET status='ready' WHERE id=? AND status='conflict'`, conflictChangeID); err != nil {
			return "", fmt.Errorf("return change to ready: %w", err)
		}
		return conflictChangeID, nil
	}
	changeID := newID()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO change (id,goal_id,sub_goal_id,status,created_at) VALUES (?,?,?,'ready',?)`,
		changeID, sg.GoalID, sg.ID, now()); err != nil {
		return "", fmt.Errorf("insert change: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO change_revision (id,change_id,seq,base_ref,head_ref,created_at) VALUES (?,?,?,?,?,?)`,
		newID(), changeID, 1, baseRef, headRef, now()); err != nil {
		return "", fmt.Errorf("insert change revision: %w", err)
	}
	return changeID, nil
}

// VerifySubGoal is the verifier verdict tool's service entry (决策 6-5): the
// CALLING run must be the sub-goal's verify run (role + sub_goal_id match —
// the verdict authority lives in the run, not the tool caller's say-so).
// P1-2 hardens the boundary: the run's AGENT must be the sub-goal's named
// verifier (verifier_id is authority, not metadata) and the run must still
// be running (a cancelled verify run cannot judge). passed → verified +
// Change/Revision atomic + sub_goal.verified event; rejected →
// verification_result recorded + quality_iteration++ + back to running with
// the assignee's successor run. Every round appends a verification_result
// row (audit/evidence).
func (s *GoalService) VerifySubGoal(ctx context.Context, runID, verdict, summary, evidence string) error {
	if verdict != "passed" && verdict != "rejected" {
		return NewValidationError("verdict must be passed or rejected")
	}
	var role, subGoalID, agentID, goalID, runStatus string
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT role, sub_goal_id, agent_id, goal_id, status FROM run WHERE id=?`, runID).
		Scan(&role, &subGoalID, &agentID, &goalID, &runStatus)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load verify run: %w", err)
	}
	if role != "verify" {
		return NewValidationError("only a verify run can issue a sub-goal verdict")
	}
	if runStatus != "running" {
		return NewValidationError("the verify run is no longer running — its verdict window is closed")
	}

	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var sg SubGoal
	err = tx.QueryRowContext(ctx,
		`SELECT id,goal_id,title,description,assignee_id,verifier_id,status,execution_attempt,quality_iteration,created_at
		 FROM sub_goal WHERE id=?`, subGoalID).
		Scan(&sg.ID, &sg.GoalID, &sg.Title, &sg.Description, &sg.AssigneeID, &sg.VerifierID, &sg.Status, &sg.ExecutionAttempt, &sg.QualityIteration, &sg.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load sub-goal: %w", err)
	}
	// P1-2: verifier identity is authority, not metadata — the calling run's
	// agent must BE the sub-goal's named verifier.
	if agentID != sg.VerifierID {
		return NewValidationError("the verdict must come from the sub-goal's verifier agent")
	}
	// Conditional: only a verifying sub-goal takes a verdict (idempotency —
	// double verdicts from a retried tool call no-op).
	if sg.Status != "verifying" {
		return NewValidationError("the sub-goal is not awaiting a verdict")
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO verification_result (id,goal_id,sub_goal_id,verifier_run_id,status,summary,evidence,created_at) VALUES (?,?,?,?,?,?,?,?)`,
		newID(), sg.GoalID, sg.ID, runID, verdict, summary, evidence, now()); err != nil {
		return fmt.Errorf("insert verification_result: %w", err)
	}
	// The verdict closes the sub-goal's verify wait (passed → owner
	// integration; rejected → assignee rework round).
	logging.Infof("sub-goal: %s verdict=%s by verifier run %s (summary=%q)", sg.ID, verdict, runID, trimLog(summary, 60))

	var evs []events.Event
	if verdict == "passed" {
		if _, err := tx.ExecContext(ctx,
			`UPDATE sub_goal SET status='verified' WHERE id=? AND status='verifying'`, sg.ID); err != nil {
			return fmt.Errorf("verify sub-goal: %w", err)
		}
		// The revision refs come from the ASSIGNEE's latest run (the work
		// being judged), not the verifier run.
		var baseRef, headRef string
		if err := tx.QueryRowContext(ctx,
			`SELECT base_ref, head_ref FROM run WHERE sub_goal_id=? AND role='subgoal' ORDER BY finished_at DESC LIMIT 1`,
			sg.ID).Scan(&baseRef, &headRef); err != nil {
			return fmt.Errorf("load assignee refs: %w", err)
		}
		changeID, err := s.materializeChangeTx(ctx, tx, sg, baseRef, headRef)
		if err != nil {
			return err
		}
		evs = append(evs, events.Event{Topic: "sub_goal.verified", Payload: map[string]any{
			"goal_id": sg.GoalID, "sub_goal_id": sg.ID, "change_id": changeID,
		}})
		if changeID != "" {
			evs = append(evs, events.Event{Topic: "change.ready", Payload: map[string]any{
				"goal_id": sg.GoalID, "change_id": changeID, "sub_goal_id": sg.ID,
			}})
		}
		// Depends-on gating: release gated siblings (first runs born here).
		wakeEvs, err := s.wakeDependentsTx(ctx, tx, sg.GoalID, sg.ID)
		if err != nil {
			return err
		}
		evs = append(evs, wakeEvs...)
	} else {
		if _, err := tx.ExecContext(ctx,
			`UPDATE sub_goal SET status='running', quality_iteration=quality_iteration+1 WHERE id=? AND status='verifying'`,
			sg.ID); err != nil {
			return fmt.Errorf("reject sub-goal: %w", err)
		}
		evs = append(evs, events.Event{Topic: "sub_goal.rejected", Payload: map[string]any{
			"goal_id": sg.GoalID, "sub_goal_id": sg.ID,
		}})
		// P0-3 (决策 6-13): the assignee's rework run is born IN this
		// transaction — a crash after the commit can no longer lose it. The
		// run event is published after the commit below (invariant 13).
		_, runEv, err := s.runSvc.enqueueSubGoalRunTx(ctx, tx, sg.ID, "")
		if err != nil {
			return fmt.Errorf("enqueue rework run: %w", err)
		}
		if runEv != nil {
			evs = append(evs, *runEv)
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	for _, e := range evs {
		s.bus.Publish(ctx, e)
	}
	return nil
}

// MarkChangeIntegrated records an integration attempt's outcome (决策 6-3):
// the git merge itself runs in the owner's run workspace (the integrate_change
// tool); this stamps the authoritative Change state. success → integrated;
// conflict → conflict + the sub-goal goes back to running (quality_iteration++
// — the work was rejected by integration reality) with the assignee's
// successor run enqueued; a later revision appends change_revision seq N+1.
func (s *GoalService) MarkChangeIntegrated(ctx context.Context, changeID string, success bool) error {
	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	var sgID, goalID string
	if err := tx.QueryRowContext(ctx,
		`SELECT sub_goal_id, goal_id FROM change WHERE id=?`, changeID).Scan(&sgID, &goalID); err != nil {
		return fmt.Errorf("load change: %w", err)
	}
	if success {
		// Only ready/integrating may become integrated — a CONFLICTED change
		// must first return through the rework revision (conflict → rework →
		// ready), never be force-integrated around the loop.
		res, err := tx.ExecContext(ctx,
			`UPDATE change SET status='integrated' WHERE id=? AND status IN ('ready','integrating')`,
			changeID)
		if err != nil {
			return fmt.Errorf("mark integrated: %w", err)
		}
		if n, _ := res.RowsAffected(); n == 0 {
			return NewValidationError("the change is not integrable — a conflicted change must wait for the rework revision")
		}
		if err := tx.Commit(); err != nil {
			return err
		}
		s.bus.Publish(ctx, events.Event{Topic: "change.integrated", Payload: map[string]any{
			"goal_id": goalID, "change_id": changeID, "sub_goal_id": sgID,
		}})
		return nil
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE change SET status='conflict' WHERE id=? AND status IN ('ready','integrating')`,
		changeID); err != nil {
		return fmt.Errorf("mark conflict: %w", err)
	}
	// The sub-goal goes back to running for the assignee's rework round.
	if _, err := tx.ExecContext(ctx,
		`UPDATE sub_goal SET status='running', quality_iteration=quality_iteration+1 WHERE id=? AND status='verified'`,
		sgID); err != nil {
		return fmt.Errorf("rework sub-goal: %w", err)
	}
	// P0-3 (决策 6-13): the assignee's rework run is born IN this
	// transaction — the conflict transition and its successor run are one
	// atomic unit. The run event is published after the commit (invariant 13).
	var runEv *events.Event
	if _, ev, err := s.runSvc.enqueueSubGoalRunTx(ctx, tx, sgID, ""); err != nil {
		return fmt.Errorf("enqueue rework run: %w", err)
	} else {
		runEv = ev
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.bus.Publish(ctx, events.Event{Topic: "change.conflict", Payload: map[string]any{
		"goal_id": goalID, "change_id": changeID, "sub_goal_id": sgID,
	}})
	if runEv != nil {
		s.bus.Publish(ctx, *runEv)
	}
	return nil
}

// ReconcileVerifyRun closes a verify run (决策 6-5): the verdict tool already
// made the sub-goal transition — the run itself is discarded (verify runs
// have no goal authority). The verifier's verdict lives in verification_result
// (structured) + the agent's own `goal comment` if it has an opinion.
func (s *GoalService) ReconcileVerifyRun(ctx context.Context, rc goalRunContext) error {
	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	// Same P0-1 stamp as the other reconcile paths (决策 6-11).
	if _, err := tx.ExecContext(ctx, `UPDATE run SET reconciled_at=? WHERE id=?`, now(), rc.RunID); err != nil {
		return fmt.Errorf("stamp reconciled_at: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.bus.Publish(ctx, events.Event{Topic: "run:discarded", Payload: map[string]any{
		"run_id": rc.RunID, "goal_id": rc.GoalID, "reason": "verify-run",
	}})
	return nil
}

// Change is a logical deliverable produced by a sub-goal (决策 6-3). HeadRef
// is the LATEST revision's head — the ref the owner integrates.
type Change struct {
	ID        string `json:"id"`
	GoalID    string `json:"goal_id"`
	SubGoalID string `json:"sub_goal_id"`
	Status    string `json:"status"` // ready|integrating|integrated|conflict
	HeadRef   string `json:"head_ref"`
	CreatedAt string `json:"created_at"`
}

// GetChange loads a change with its latest revision's head ref.
func (s *GoalService) GetChange(ctx context.Context, changeID string) (*Change, error) {
	var c Change
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT c.id, c.goal_id, c.sub_goal_id, c.status, c.created_at,
		        COALESCE((SELECT r.head_ref FROM change_revision r WHERE r.change_id = c.id ORDER BY r.seq DESC LIMIT 1), '')
		 FROM change c WHERE c.id=?`, changeID).
		Scan(&c.ID, &c.GoalID, &c.SubGoalID, &c.Status, &c.CreatedAt, &c.HeadRef)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// ListChanges lists a goal's changes (for the owner's resume context).
func (s *GoalService) ListChanges(ctx context.Context, goalID string) ([]Change, error) {
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT c.id, c.goal_id, c.sub_goal_id, c.status, c.created_at,
		        COALESCE((SELECT r.head_ref FROM change_revision r WHERE r.change_id = c.id ORDER BY r.seq DESC LIMIT 1), '')
		 FROM change c WHERE c.goal_id=? ORDER BY c.created_at`, goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Change{}
	for rows.Next() {
		var c Change
		if err := rows.Scan(&c.ID, &c.GoalID, &c.SubGoalID, &c.Status, &c.CreatedAt, &c.HeadRef); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// ChangeRevision is one revision of a Change (决策 6-3): every revision binds
// the change to the integration base it was built against (base_ref) and the
// commit that delivers it (head_ref). Conflict rework appends seq N+1 on the
// SAME change — the revision history is the audit of which base each round
// targeted.
type ChangeRevision struct {
	ID        string `json:"id"`
	ChangeID  string `json:"change_id"`
	Seq       int    `json:"seq"`
	BaseRef   string `json:"base_ref"`
	HeadRef   string `json:"head_ref"`
	CreatedAt string `json:"created_at"`
}

// ChangeDetail is a Change with its revision history — the Web change
// panel's row (the owner's integration view).
type ChangeDetail struct {
	Change
	Revisions []ChangeRevision `json:"revisions"`
}

// ListChangeDetails lists a goal's changes with their revisions. A
// per-change revision query is fine at single-user scale (at most
// maxActiveSubGoals changes per goal).
func (s *GoalService) ListChangeDetails(ctx context.Context, goalID string) ([]ChangeDetail, error) {
	changes, err := s.ListChanges(ctx, goalID)
	if err != nil {
		return nil, err
	}
	out := make([]ChangeDetail, 0, len(changes))
	for _, c := range changes {
		rows, err := s.st.DB().QueryContext(ctx,
			`SELECT id, change_id, seq, base_ref, head_ref, created_at
			 FROM change_revision WHERE change_id=? ORDER BY seq`, c.ID)
		if err != nil {
			return nil, fmt.Errorf("list revisions: %w", err)
		}
		var revs []ChangeRevision
		for rows.Next() {
			var r ChangeRevision
			if err := rows.Scan(&r.ID, &r.ChangeID, &r.Seq, &r.BaseRef, &r.HeadRef, &r.CreatedAt); err != nil {
				rows.Close()
				return nil, fmt.Errorf("scan revision: %w", err)
			}
			revs = append(revs, r)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}
		out = append(out, ChangeDetail{Change: c, Revisions: revs})
	}
	return out, nil
}

// MarkChangeIntegrating stamps Ready → Integrating (the integrate_change tool
// merges next). A change that is NOT ready refuses the transition loudly —
// silently no-op'ing here let an agent re-integrate a CONFLICTED change and
// force it through without the rework revision (live: a double integrate
// after a conflict ended the goal's loop in a dead active state).
func (s *GoalService) MarkChangeIntegrating(ctx context.Context, changeID string) error {
	res, err := s.st.DB().ExecContext(ctx,
		`UPDATE change SET status='integrating' WHERE id=? AND status='ready'`, changeID)
	if err != nil {
		return fmt.Errorf("mark integrating: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		var status string
		_ = s.st.DB().QueryRowContext(ctx, `SELECT status FROM change WHERE id=?`, changeID).Scan(&status)
		return NewValidationError(fmt.Sprintf("the change is not ready (status=%s) — wait for the assignee's rework revision", status))
	}
	return nil
}

// VerificationResult is one verification round of a sub-goal (决策 6-5) —
// the audit row the get_verification tool expands from the resume index.
type VerificationResult struct {
	ID            string `json:"id"`
	GoalID        string `json:"goal_id"`
	SubGoalID     string `json:"sub_goal_id"`
	VerifierRunID string `json:"verifier_run_id"`
	Status        string `json:"status"` // passed|rejected
	Summary       string `json:"summary"`
	Evidence      string `json:"evidence"`
	CreatedAt     string `json:"created_at"`
}

// ListVerificationResults lists a sub-goal's verification rounds (newest first).
func (s *GoalService) ListVerificationResults(ctx context.Context, subGoalID string) ([]VerificationResult, error) {
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id,goal_id,sub_goal_id,verifier_run_id,status,summary,evidence,created_at
		 FROM verification_result WHERE sub_goal_id=? ORDER BY created_at DESC`, subGoalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []VerificationResult{}
	for rows.Next() {
		var v VerificationResult
		if err := rows.Scan(&v.ID, &v.GoalID, &v.SubGoalID, &v.VerifierRunID, &v.Status, &v.Summary, &v.Evidence, &v.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

// CancelSubGoal cancels a sub-goal (owner management, 决策 6-1): conditional
// transition on non-terminal states; the sub-goal's queued run is dropped and
// the daemon kills a running one (sub_goal.cancelled event → runCancels).
//
// LIVENESS (2026-11-14): a cancelled dependency can never verify either, so
// the cancellation and the cascade to its still-gated dependents happen in ONE
// transaction — without it a gated child stays running with no run, forever
// holds the goal's finalization guard, and makes `goal wait` time out.
func (s *GoalService) CancelSubGoal(ctx context.Context, subGoalID string) (*SubGoal, error) {
	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	res, err := tx.ExecContext(ctx,
		`UPDATE sub_goal SET status='cancelled' WHERE id=? AND status NOT IN ('verified','cancelled','failed')`,
		subGoalID)
	if err != nil {
		return nil, fmt.Errorf("cancel sub-goal: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, NewValidationError("the sub-goal is already terminal")
	}
	var goalID string
	if err := tx.QueryRowContext(ctx, `SELECT goal_id FROM sub_goal WHERE id=?`, subGoalID).Scan(&goalID); err != nil {
		return nil, fmt.Errorf("load cancelled sub-goal: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE run SET status='cancelled' WHERE sub_goal_id=? AND status='queued'`, subGoalID); err != nil {
		return nil, fmt.Errorf("cancel sub-goal queued runs: %w", err)
	}
	cascadeEvs, err := s.failGatedDependentsTx(ctx, tx, goalID, subGoalID)
	if err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.bus.Publish(ctx, events.Event{Topic: "sub_goal.cancelled", Payload: map[string]any{
		"goal_id": goalID, "sub_goal_id": subGoalID,
	}})
	for _, e := range cascadeEvs {
		s.bus.Publish(ctx, e)
	}
	return s.GetSubGoal(ctx, subGoalID)
}

// RetrySubGoal re-arms a terminal-failed sub-goal: reset execution_attempt=0
// and status='running', then enqueue a fresh run in the same transaction
// (P0-3: the retry transition and its successor run are one atomic unit). A
// non-failed sub-goal is rejected — this is the outlet for the case where the
// sub-goal hit the execution_attempt cap and reconcile stopped retrying.
func (s *GoalService) RetrySubGoal(ctx context.Context, subGoalID string) (*SubGoal, error) {
	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	var goalID, depsRaw, status string
	if err := tx.QueryRowContext(ctx,
		`SELECT goal_id, COALESCE(depends_on,'[]'), status FROM sub_goal WHERE id=?`, subGoalID).
		Scan(&goalID, &depsRaw, &status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("load sub-goal: %w", err)
	}
	if status != "failed" {
		return nil, NewValidationError("the sub-goal is not failed — nothing to retry")
	}
	// GATING (2026-11-14): a retry must not BYPASS depends_on. The retry makes
	// the work item runnable again — exactly what CreateSubGoal refuses while a
	// dependency is unsatisfied and what wakeDependentsTx enforces for a
	// released child. Without this check a failed gated child could be re-armed
	// and run before its dependencies verify (the gate would only be
	// cosmetic). The unmet deps are named so the owner knows what to restore
	// first (typically RetrySubGoal on a failed dependency, or waiting for a
	// verify run).
	if deps := decodeDeps(depsRaw); len(deps) > 0 {
		statuses, err := depStatusesTx(ctx, tx, deps)
		if err != nil {
			return nil, err
		}
		if unmet := unmetDeps(deps, statuses); len(unmet) > 0 {
			return nil, NewValidationError("retry refused: depends_on is not satisfied (unmet: " + strings.Join(unmet, ", ") + ") — restore the dependencies first")
		}
	}

	res, err := tx.ExecContext(ctx,
		`UPDATE sub_goal SET status='running', execution_attempt=0 WHERE id=? AND status='failed'`,
		subGoalID)
	if err != nil {
		return nil, fmt.Errorf("retry sub-goal: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, NewValidationError("the sub-goal is not failed — nothing to retry")
	}

	if s.runSvc == nil {
		return nil, errors.New("runSvc not wired")
	}
	_, runEv, err := s.runSvc.enqueueSubGoalRunTx(ctx, tx, subGoalID, "")
	if err != nil {
		return nil, fmt.Errorf("enqueue retry run: %w", err)
	}

	if err := tx.Commit(); err != nil {
		return nil, err
	}
	s.bus.Publish(ctx, events.Event{Topic: "sub_goal.retrying", Payload: map[string]any{
		"goal_id": goalID, "sub_goal_id": subGoalID, "execution_attempt": 0,
	}})
	if runEv != nil {
		s.bus.Publish(ctx, *runEv)
	}
	sg, err := s.GetSubGoal(ctx, subGoalID)
	if err != nil {
		return nil, err
	}
	return sg, nil
}

// waitChildrenTimeout bounds `goal wait` — the owner parks until its
// dispatched work settles, never forever.
const waitChildrenTimeout = 10 * time.Minute

// WaitChildren blocks until the goal's sub-goals settle (none pending —
// the same predicate as the finalization guard: only verified/failed/
// cancelled are terminal) or the wait times out, and returns the
// children's current states. The owner's `goal wait` tool: the agent
// parks here instead of guessing whether its dispatches landed.
func (s *GoalService) WaitChildren(ctx context.Context, goalID string) ([]SubGoal, error) {
	deadline := time.Now().Add(waitChildrenTimeout)
	for {
		all, err := s.ListSubGoals(ctx, goalID)
		if err != nil {
			return nil, err
		}
		pending := false
		for _, sg := range all {
			if sg.Status != "verified" && sg.Status != "failed" && sg.Status != "cancelled" {
				pending = true
				break
			}
		}
		if !pending || time.Now().After(deadline) {
			return all, nil
		}
		select {
		case <-ctx.Done():
			return all, ctx.Err()
		case <-time.After(5 * time.Second):
		}
	}
}

// wakeDependentsTx releases dependency-gated sub-goals after a verified
// transition (W-RSI depends_on gating, 2026-11-08): a gated sub-goal is
// created WITHOUT its first run; the first run is enqueued here in the SAME
// transaction that verified the last unmet dependency (P0-3 — the run cannot
// be lost to a crash). Returns run events for post-commit publish.
//
// LIVENESS (2026-11-14): a dependency that DIED (retry chain exhausted →
// failed, or cancelled) can never verify, so a dependent gated on it must not
// be `continue`d past forever. The cascade runs first, in this transaction:
// every gated dependent of a dead dependency goes terminally failed with a
// system comment (failGatedDependentsTx). The per-candidate dead-dep check
// below is the defensive belt to that suspenders — a dead dependency can never
// silently keep a child parked.
func (s *GoalService) wakeDependentsTx(ctx context.Context, tx *sql.Tx, goalID, justVerified string) ([]events.Event, error) {
	evs, err := s.failGatedDependentsTx(ctx, tx, goalID, justVerified)
	if err != nil {
		return nil, err
	}
	pending, err := gatedSubGoalsTx(ctx, tx, goalID)
	if err != nil {
		return nil, err
	}
	for _, g := range pending {
		if len(g.deps) == 0 {
			continue // never created without a run — ungated
		}
		statuses, err := depStatusesTx(ctx, tx, g.deps)
		if err != nil {
			return nil, err
		}
		if deadDep, deadStatus := firstDeadDep(g.deps, statuses); deadDep != "" {
			ev, err := s.failGatedSubGoalTx(ctx, tx, goalID, g.id, deadDep, deadStatus)
			if err != nil {
				return nil, err
			}
			if ev != nil {
				evs = append(evs, *ev)
			}
			continue
		}
		ready := true
		for _, d := range g.deps {
			if statuses[d] != "verified" {
				ready = false
				break
			}
		}
		if !ready {
			continue
		}
		// ANCHOR (2026-11-14): the released run carries the sub-goal's dispatch
		// comment id (persisted by CreateSubGoal) as its trigger_comment_id, so
		// the assignee's report threads to the dispatch and the wake context
		// names the leader instead of degrading to "the platform". Legacy rows
		// have '' — the run keeps no anchor, exactly the old behaviour.
		_, runEv, err := s.runSvc.enqueueSubGoalRunTx(ctx, tx, g.id, g.dispatchID)
		if err != nil {
			return nil, fmt.Errorf("wake gated sub-goal %s: %w", g.id, err)
		}
		logging.Infof("sub-goal: released %s — all depends_on verified (trigger %s)", g.id, justVerified)
		evs = append(evs, events.Event{Topic: "sub_goal.released", Payload: map[string]any{
			"goal_id": goalID, "sub_goal_id": g.id, "trigger_sub_goal_id": justVerified,
		}})
		if runEv != nil {
			evs = append(evs, *runEv)
		}
	}
	return evs, nil
}

// gatedSubGoal is a dependency-gated work item still waiting for release: it is
// 'running' and has NO sub-goal run yet (its first run is born only when every
// dependency verifies, or it is released by wakeDependentsTx). dispatchID is
// the dispatch comment anchor persisted at creation (” on legacy rows).
type gatedSubGoal struct {
	id         string
	deps       []string
	dispatchID string
}

// gatedSubGoalsTx scans a goal's still-gated work items — the release
// candidates and the cascade candidates are the same set (sub-goals that are
// running but have no role='subgoal' run yet).
func gatedSubGoalsTx(ctx context.Context, tx *sql.Tx, goalID string) ([]gatedSubGoal, error) {
	rows, err := tx.QueryContext(ctx,
		`SELECT id, COALESCE(depends_on,'[]'), COALESCE(dispatch_comment_id,'') FROM sub_goal
		 WHERE goal_id=? AND status='running'
		   AND NOT EXISTS (SELECT 1 FROM run WHERE run.sub_goal_id=sub_goal.id AND run.role='subgoal')`,
		goalID)
	if err != nil {
		return nil, fmt.Errorf("scan gated sub-goals: %w", err)
	}
	defer rows.Close()
	var pending []gatedSubGoal
	for rows.Next() {
		var g gatedSubGoal
		var depsRaw string
		if err := rows.Scan(&g.id, &depsRaw, &g.dispatchID); err != nil {
			return nil, err
		}
		g.deps = decodeDeps(depsRaw)
		pending = append(pending, g)
	}
	return pending, rows.Err()
}

// depStatusesTx loads the current status of every dependency id. An id absent
// from the map has NO row and is treated as dead by firstDeadDep — a vanished
// dependency can never verify.
func depStatusesTx(ctx context.Context, tx *sql.Tx, deps []string) (map[string]string, error) {
	out := make(map[string]string, len(deps))
	if len(deps) == 0 {
		return out, nil
	}
	rows, err := tx.QueryContext(ctx,
		`SELECT id,status FROM sub_goal WHERE id IN (`+depsPlaceholders(len(deps))+`)`, depsArgs(deps)...)
	if err != nil {
		return nil, fmt.Errorf("load dependency statuses: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, status string
		if err := rows.Scan(&id, &status); err != nil {
			return nil, err
		}
		out[id] = status
	}
	return out, rows.Err()
}

// firstDeadDep returns the first dependency that can never verify — failed
// (the retry chain is exhausted) or cancelled — plus its status. A dependency
// with no row at all is dead too ("missing"). Empty id + status means every
// dependency is still alive (running/verifying) or verified.
func firstDeadDep(deps []string, statuses map[string]string) (depID, status string) {
	for _, d := range deps {
		st, ok := statuses[d]
		if !ok {
			return d, "missing"
		}
		if st == "failed" || st == "cancelled" {
			return d, st
		}
	}
	return "", ""
}

// unmetDeps renders the dependencies that are not verified as "id (status)"
// (or "id (missing)") for the RetrySubGoal validation message.
func unmetDeps(deps []string, statuses map[string]string) []string {
	var out []string
	for _, d := range deps {
		st, ok := statuses[d]
		if !ok {
			out = append(out, d+" (missing)")
			continue
		}
		if st != "verified" {
			out = append(out, d+" ("+st+")")
		}
	}
	return out
}

// failGatedDependentsTx cascades a dependency's TERMINAL death (failed after
// the retry chain is exhausted, or cancelled) to the sub-goals it gates, in the
// caller's transaction (liveness fix, 2026-11-14).
//
// The defect it closes: a gated sub-goal is born WITHOUT a run, and only
// wakeDependentsTx can release it — but that only happens when a dependency
// VERIFIES. A dead dependency can never verify, so the child stayed 'running'
// with no run forever: nothing reconciles it, the goal's finalization guard
// counts it pending (so the goal can never reach done/review), and `goal wait`
// burns its 10min timeout. A dependency that merely failed ONE attempt is still
// retrying (attempt < maxExecutionAttempts, status='running') and is NOT
// cascaded — it may still verify.
//
// Transitivity: a cascaded dependent becomes terminally failed itself, i.e. a
// dead dependency of ITS dependents — so the loop repeats to a fixpoint (A
// dies → B gated on A fails → C gated on B fails). Every iteration permanently
// removes at least one candidate (its status leaves 'running'), so this
// terminates.
func (s *GoalService) failGatedDependentsTx(ctx context.Context, tx *sql.Tx, goalID, triggerSGID string) ([]events.Event, error) {
	var evs []events.Event
	for {
		pending, err := gatedSubGoalsTx(ctx, tx, goalID)
		if err != nil {
			return nil, err
		}
		changed := false
		for _, g := range pending {
			if len(g.deps) == 0 {
				continue
			}
			statuses, err := depStatusesTx(ctx, tx, g.deps)
			if err != nil {
				return nil, err
			}
			deadDep, deadStatus := firstDeadDep(g.deps, statuses)
			if deadDep == "" {
				continue
			}
			ev, err := s.failGatedSubGoalTx(ctx, tx, goalID, g.id, deadDep, deadStatus)
			if err != nil {
				return nil, err
			}
			if ev != nil {
				evs = append(evs, *ev)
				changed = true
			}
		}
		if !changed {
			if len(evs) > 0 {
				logging.Infof("sub-goal: cascaded %d gated dependent(s) after dependency %s became terminal (goal %s)", len(evs), triggerSGID, goalID)
			}
			return evs, nil
		}
	}
}

// failGatedSubGoalTx terminally fails ONE still-gated dependent whose
// dependency is dead and records the reason as a SYSTEM comment (the platform's
// voice): the owner/human must see WHY a work item died without ever running.
// The UPDATE is conditional on 'running' AND no role='subgoal' run — a child
// released between the scan and this write is never clobbered (nil event).
func (s *GoalService) failGatedSubGoalTx(ctx context.Context, tx *sql.Tx, goalID, sgID, depID, depStatus string) (*events.Event, error) {
	res, err := tx.ExecContext(ctx,
		`UPDATE sub_goal SET status='failed'
		  WHERE id=? AND status='running'
		    AND NOT EXISTS (SELECT 1 FROM run WHERE run.sub_goal_id=? AND run.role='subgoal')`,
		sgID, sgID)
	if err != nil {
		return nil, fmt.Errorf("fail gated sub-goal %s: %w", sgID, err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, nil
	}
	reason := "不存在"
	switch depStatus {
	case "failed":
		reason = "失败"
	case "cancelled":
		reason = "取消"
	}
	content := fmt.Sprintf("依赖 %s %s，无法满足门控，该工作项已置为 failed。", depID, reason)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO comment (id,goal_id,author_type,author_id,parent_id,content,created_at) VALUES (?,?,'system','',NULL,?,?)`,
		newID(), goalID, content, now()); err != nil {
		return nil, fmt.Errorf("insert gating-failure comment: %w", err)
	}
	logging.Infof("sub-goal: failed gated %s — dependency %s is %s", sgID, depID, depStatus)
	ev := events.Event{Topic: "sub_goal.failed", Payload: map[string]any{
		"goal_id": goalID, "sub_goal_id": sgID, "execution_attempt": 0,
		"cascade": true, "depends_on_failed": depID, "dependency_status": depStatus,
	}}
	return &ev, nil
}

// decodeDeps parses a sub_goal.depends_on JSON array (bad JSON → nil).
func decodeDeps(raw string) []string {
	var out []string
	if raw == "" {
		return nil
	}
	_ = json.Unmarshal([]byte(raw), &out)
	return out
}

// encodeDeps serializes a dependency list to the JSON column form.
func encodeDeps(deps []string) string {
	if len(deps) == 0 {
		return "[]"
	}
	b, err := json.Marshal(deps)
	if err != nil {
		return "[]"
	}
	return string(b)
}

// depsPlaceholders renders "?,?,?" for an IN clause.
func depsPlaceholders(n int) string {
	return strings.TrimSuffix(strings.Repeat("?,", n), ",")
}

// depsArgs lifts []string to []any for database/sql.
func depsArgs(deps []string) []any {
	out := make([]any, len(deps))
	for i, d := range deps {
		out[i] = d
	}
	return out
}
