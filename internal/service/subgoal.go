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
	ID               string `json:"id"`
	GoalID           string `json:"goal_id"`
	Title            string `json:"title"`
	Description      string `json:"description"`
	AssigneeID       string `json:"assignee_id"`
	VerifierID       string `json:"verifier_id"` // '' = machine (domain verify commands)
	Status           string `json:"status"`      // running|verifying|verified|rejected|cancelled|failed（pending/done 无写入路径，见 schema.sql）
	ExecutionAttempt int    `json:"execution_attempt"`
	QualityIteration int    `json:"quality_iteration"`
	CreatedAt        string `json:"created_at"`
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
func (s *GoalService) CreateSubGoal(ctx context.Context, goalID, title, description, assigneeID, verifierID, createdByType, createdByID string) (*SubGoal, error) {
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
		// PLAYER-REFEREE RULE (contract 7.4, findings §W7): the verifier must
		// be an independent agent — a work item may not be judged by the
		// agent that executed it (the same principle the squad reviewer rule
		// above enforces). The goal OWNER as verifier stays allowed: the
		// owner is the integrator, and judging a sub-goal is often its job.
		if verifierID == assigneeID {
			return nil, NewValidationError("the verifier must be a different agent than the assignee (player-referee)")
		}
		if err := mustExist(ctx, s.st, `SELECT COUNT(*) FROM agent WHERE id=?`, verifierID, "verifier agent"); err != nil {
			return nil, err
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
		`INSERT INTO sub_goal (id,goal_id,title,description,assignee_id,verifier_id,status,execution_attempt,quality_iteration,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?)`,
		sg.ID, sg.GoalID, sg.Title, sg.Description, sg.AssigneeID, sg.VerifierID, sg.Status, sg.ExecutionAttempt, sg.QualityIteration, sg.CreatedAt); err != nil {
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
	_, runEv, err := s.runSvc.enqueueSubGoalRunTx(ctx, tx, sg.ID, dispatchID)
	if err != nil {
		return nil, fmt.Errorf("enqueue sub-goal run: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	logging.Infof("sub-goal: created %q (goal %s) → assignee=%s verifier=%s", sg.Title, sg.GoalID, sg.AssigneeID, sg.VerifierID)
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
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT id,goal_id,title,description,assignee_id,verifier_id,status,execution_attempt,quality_iteration,created_at
		 FROM sub_goal WHERE id=?`, id).
		Scan(&sg.ID, &sg.GoalID, &sg.Title, &sg.Description, &sg.AssigneeID, &sg.VerifierID, &sg.Status, &sg.ExecutionAttempt, &sg.QualityIteration, &sg.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &sg, nil
}

// ListSubGoals lists a goal's sub-goals (oldest first).
func (s *GoalService) ListSubGoals(ctx context.Context, goalID string) ([]SubGoal, error) {
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id,goal_id,title,description,assignee_id,verifier_id,status,execution_attempt,quality_iteration,created_at
		 FROM sub_goal WHERE goal_id=? ORDER BY created_at`, goalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []SubGoal{}
	for rows.Next() {
		var sg SubGoal
		if err := rows.Scan(&sg.ID, &sg.GoalID, &sg.Title, &sg.Description, &sg.AssigneeID, &sg.VerifierID, &sg.Status, &sg.ExecutionAttempt, &sg.QualityIteration, &sg.CreatedAt); err != nil {
			return nil, err
		}
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
	case "failed", "cancelled":
		if _, err := tx.ExecContext(ctx,
			`UPDATE sub_goal SET execution_attempt=execution_attempt+1 WHERE id=? AND status IN ('running','done')`,
			sg.ID); err != nil {
			return fmt.Errorf("bump execution_attempt: %w", err)
		}
		var attempt int
		_ = tx.QueryRowContext(ctx, `SELECT execution_attempt FROM sub_goal WHERE id=?`, sg.ID).Scan(&attempt)
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
				`UPDATE sub_goal SET status='failed' WHERE id=? AND status IN ('running','done')`, sg.ID); err != nil {
				return fmt.Errorf("fail sub-goal: %w", err)
			}
			evs = append(evs, events.Event{Topic: "sub_goal.failed", Payload: map[string]any{
				"goal_id": sg.GoalID, "sub_goal_id": sg.ID, "execution_attempt": attempt,
			}})
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
// be inside its verdict window. The window is "running", or "completed"
// within verifyLateVerdictWindow of finished_at — the reported race
// (findings §W6): Finish stamped the verify run completed while the verdict
// tool call was already in flight, and the old running-only guard rejected
// it, leaving the sub-goal verifying forever. failed/cancelled runs never
// judge. passed → verified + Change/Revision atomic + sub_goal.verified
// event; rejected → verification_result recorded + quality_iteration++ +
// back to running with the assignee's successor run. Every round appends a
// verification_result row (audit/evidence), so a retried verdict is
// idempotent: an existing row for THIS verifier run wins and no second
// Change can materialize.
func (s *GoalService) VerifySubGoal(ctx context.Context, runID, verdict, summary, evidence string) error {
	if verdict != "passed" && verdict != "rejected" {
		return NewValidationError("verdict must be passed or rejected")
	}
	var role, subGoalID, agentID, goalID, runStatus, finishedAt string
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT role, sub_goal_id, agent_id, goal_id, status, finished_at FROM run WHERE id=?`, runID).
		Scan(&role, &subGoalID, &agentID, &goalID, &runStatus, &finishedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return fmt.Errorf("load verify run: %w", err)
	}
	if role != "verify" {
		return NewValidationError("only a verify run can issue a sub-goal verdict")
	}
	if !verifyVerdictWindowOpen(runStatus, finishedAt, time.Now()) {
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
	// Idempotency FIRST: a retried verdict (the tool call replayed after the
	// first one committed — or a late verdict after the platform re-queued)
	// must never materialize a second Change or rework round. Checked by the
	// verifier RUN id: one run judges at most once.
	var dup int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM verification_result WHERE verifier_run_id=?`, runID).Scan(&dup); err != nil {
		return fmt.Errorf("check existing verdict: %w", err)
	}
	if dup > 0 {
		return NewValidationError("a verdict already exists for this verify run")
	}
	// Conditional: only a verifying sub-goal takes a verdict.
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

// verifyLateVerdictWindow bounds a LATE verdict for a completed verify run.
// The verdict tool call can still be in flight when the daemon's Finish
// stamps the run terminal (the reported race: run 793a7522 finished 18:19
// while the leader dispatched the verifier); a completed run therefore
// accepts a verdict for this window after finished_at. failed/cancelled
// runs never accept one — a dead machine must not judge.
const verifyLateVerdictWindow = 30 * time.Minute

// verifyStaleWindow is how long a sub-goal may sit in verifying with no
// verdict before the sweeper raises its ONE stale alarm.
const verifyStaleWindow = time.Hour

// verifyVerdictWindowOpen reports whether a verify run in runStatus (with
// run.finished_at = finishedAt) may still take a verdict. It fails CLOSED:
// a completed run with a missing/unparseable finished_at is outside the
// window (the boundary fixture writes status='completed' via direct SQL and
// leaves finished_at empty), and a future stamped time is treated as inside
// it (clock skew must not lose a real verdict).
func verifyVerdictWindowOpen(runStatus, finishedAt string, now time.Time) bool {
	if runStatus == "running" {
		return true
	}
	if runStatus != "completed" || finishedAt == "" {
		return false
	}
	done, err := time.Parse(time.RFC3339Nano, finishedAt)
	if err != nil {
		return false
	}
	return now.Sub(done) <= verifyLateVerdictWindow
}

// formatVerifyDuration renders the stale verifying wait at human scale —
// minutes below one hour, then hours (plus minutes when nonzero).
func formatVerifyDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Hour {
		if m := int(d.Minutes()); m > 0 {
			return fmt.Sprintf("%d min", m)
		}
		return "under a minute"
	}
	h := int(d.Hours())
	if m := int(d.Minutes()) - h*60; m > 0 {
		return fmt.Sprintf("%dh%dm", h, m)
	}
	return fmt.Sprintf("%dh", h)
}

// verifyHealResult is one verify-integrity heal's outcome. The run-end
// reconcile and the periodic sweeper share the policy through
// healVerifyingSubGoalTx so the two paths can never drift.
type verifyHealResult struct {
	requeued bool
	parked   bool
	events   []events.Event
}

// healVerifyingSubGoal is the tx-wrapper form of the heal (used by the
// sweeper): commit the transaction, THEN publish the events it collected
// (invariant 13).
func (s *GoalService) healVerifyingSubGoal(ctx context.Context, subGoalID, runID, runStatus string) (verifyHealResult, error) {
	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return verifyHealResult{}, err
	}
	defer tx.Rollback()
	out, err := s.healVerifyingSubGoalTx(ctx, tx, subGoalID, runID, runStatus)
	if err != nil {
		return verifyHealResult{}, err
	}
	if err := tx.Commit(); err != nil {
		return verifyHealResult{}, err
	}
	for _, e := range out.events {
		s.bus.Publish(ctx, e)
	}
	return out, nil
}

// healVerifyingSubGoalTx applies the verify-integrity heal for a sub-goal
// whose verify run reached a terminal status without a verdict (findings
// §W6, contract 7.2/7.3). Called INSIDE the caller's transaction, after the
// run's reconciled_at stamp:
//
//	still verifying, no verdict in the CURRENT verify wait, completed FIRST
//	  attempt of that wait → re-dispatch the verifier exactly once (attempt
//	  2; the per-wait attempt count and a pending-run check are the
//	  double-requeue guard);
//	otherwise (failed/cancelled, or already re-dispatched) → park the goal in
//	  review with review_request "verify_no_verdict: <title>", so a human
//	  decides retry/abort (handoff-park precedent: all side effects are
//	  guarded by the conditional UPDATE);
//	the platform NEVER auto-records a passed verdict — it cannot invent a
//	  judgement.
//
// A sub-goal that moved on (verified/rejected) or whose verdict already
// landed is a no-op. Side-effect rows (activity, system comment, successor
// run) are born in this transaction; the returned events are published by
// the caller after commit (invariant 13).
func (s *GoalService) healVerifyingSubGoalTx(ctx context.Context, tx *sql.Tx, subGoalID, runID, runStatus string) (verifyHealResult, error) {
	var out verifyHealResult
	var goalID, title, status, verifierID string
	err := tx.QueryRowContext(ctx,
		`SELECT goal_id, title, status, verifier_id FROM sub_goal WHERE id=?`, subGoalID).
		Scan(&goalID, &title, &status, &verifierID)
	if errors.Is(err, sql.ErrNoRows) {
		return out, nil
	}
	if err != nil {
		return out, fmt.Errorf("load sub-goal for verify heal: %w", err)
	}
	if status != "verifying" {
		return out, nil
	}
	// Scope every guard to the CURRENT verify wait (review §M1): a rejected
	// verdict starts a rework round, and every round re-enters verifying
	// with a FRESH attempt budget. The wait begins at the latest
	// subgoal-role (assignee/rework) run — verify runs and verdicts created
	// after that point belong to this round. Lifetime counts strand round-2+
	// no-verdict runs: the round-1 rejected verdict made `verdicts > 0` true
	// forever and the attempts gate unreachable, so the sub-goal sat
	// verifying while the goal could never finalize. (An empty boundary —
	// legacy rows with no subgoal-role run — degrades to the old lifetime
	// counts.)
	var waitStart string
	if err := tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(created_at), '') FROM run WHERE sub_goal_id=? AND role='subgoal'`, subGoalID).
		Scan(&waitStart); err != nil {
		return out, fmt.Errorf("load verify-wait boundary: %w", err)
	}
	var verdicts int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM verification_result WHERE sub_goal_id=?
		  AND (verifier_run_id IN (SELECT id FROM run WHERE sub_goal_id=? AND role='verify' AND created_at > ?)
		       OR (verifier_run_id='' AND created_at > ?))`,
		subGoalID, subGoalID, waitStart, waitStart).Scan(&verdicts); err != nil {
		return out, fmt.Errorf("count verdicts: %w", err)
	}
	if verdicts > 0 {
		return out, nil // the verdict landed normally in this wait
	}
	var attempts int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM run WHERE sub_goal_id=? AND role='verify' AND created_at > ?`,
		subGoalID, waitStart).Scan(&attempts); err != nil {
		return out, fmt.Errorf("count verify runs: %w", err)
	}
	// A queued/running verify run means a concurrent healer already acted
	// (the sweeper raced the run-end reconcile): the re-dispatch happened —
	// never park or requeue on top of it (review §m1: the old placement let
	// the park branch fire while the re-dispatched attempt was still
	// queued).
	var pending int
	if err := tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM run WHERE sub_goal_id=? AND role='verify' AND status IN ('queued','running')`,
		subGoalID).Scan(&pending); err != nil {
		return out, fmt.Errorf("check pending verifier run: %w", err)
	}
	if pending > 0 {
		return out, nil
	}
	ts := now()
	// Re-dispatch only when a NAMED agent verifier exists (legacy verifying
	// rows with a machine verifier have no agent to wake — park them).
	if runStatus == "completed" && attempts == 1 && verifierID != "" {
		if s.runSvc == nil {
			return out, errors.New("goalSvc.runSvc not wired")
		}
		if _, runEv, err := s.runSvc.enqueueVerifyRunTx(ctx, tx, subGoalID); err != nil {
			return out, fmt.Errorf("re-enqueue verifier run: %w", err)
		} else if runEv != nil {
			out.events = append(out.events, *runEv)
		}
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO comment (id,goal_id,author_type,author_id,parent_id,content,created_at) VALUES (?,?,'system','',NULL,?,?)`,
			newID(), goalID, "Verifier run ended without a verdict — re-queued verification once (attempt 2).", ts); err != nil {
			return out, fmt.Errorf("insert verify requeue comment: %w", err)
		}
		detail, _ := json.Marshal(map[string]string{"sub_goal_id": subGoalID, "run_id": runID})
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO activity_log (id,goal_id,actor_type,actor_id,action,detail,created_at) VALUES (?,?,'system','','verify_requeued',?,?)`,
			newID(), goalID, string(detail), ts); err != nil {
			return out, fmt.Errorf("insert verify requeued activity: %w", err)
		}
		logging.Infof("subgoal: %s verify run %s ended without a verdict — re-queued once (attempt 2)", subGoalID, runID)
		out.requeued = true
		return out, nil
	}
	// failed/cancelled, or an already-re-dispatched attempt: park the goal
	// for a human decision (the platform cannot retry forever, and it cannot
	// judge the work itself). Same shape as the handoff park.
	reason := "verify_no_verdict: " + title
	res, err := tx.ExecContext(ctx,
		`UPDATE goal SET status='review', review_request=? WHERE id=? AND status='active'`, reason, goalID)
	if err != nil {
		return out, fmt.Errorf("park goal for verify no-verdict: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		// Goal already review/terminal — no fake park markers (the handoff
		// precedent: a park that did not happen must not publish a card).
		return out, nil
	}
	detail, _ := json.Marshal(map[string]string{"reason": "verify_no_verdict", "sub_goal_id": subGoalID})
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO activity_log (id,goal_id,actor_type,actor_id,action,detail,created_at) VALUES (?,?,'system','','entered_review',?,?)`,
		newID(), goalID, string(detail), ts); err != nil {
		return out, fmt.Errorf("insert verify park activity: %w", err)
	}
	short := runID
	if len(short) > 8 {
		short = short[:8]
	}
	if short == "" {
		short = "unknown"
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO comment (id,goal_id,author_type,author_id,parent_id,content,created_at) VALUES (?,?,'system','',NULL,?,?)`,
		newID(), goalID, fmt.Sprintf("Verification produced no verdict (run %s %s). The goal is parked for a human decision.", short, runStatus), ts); err != nil {
		return out, fmt.Errorf("insert verify park comment: %w", err)
	}
	// Squad-owned goals: the reviewers' runs are this park's successor, born
	// in this transaction (the handoff/gate precedent). Self-guards non-squad.
	if err := s.enqueueSquadReviewTx(ctx, tx, goalID, runID, reason); err != nil {
		return out, fmt.Errorf("enqueue squad review: %w", err)
	}
	logging.Infof("goal: %s parked (verify_no_verdict: sub-goal %s, run %s %s)", goalID, subGoalID, short, runStatus)
	out.parked = true
	out.events = append(out.events, events.Event{Topic: "goal:reviewing", Payload: map[string]any{
		"goal_id": goalID,
		"run_id":  runID,
		"reason":  reason,
		// A machine-detected no-verdict park is NEVER auto-approved — the
		// human must decide (contract 7.2). Unlike the gate park, whose
		// created_by decides, there is no agent request to auto-approve.
		"auto_approve": false,
	}})
	return out, nil
}

// SweepVerifying is the verify-integrity sweeper (contract 7.3, findings
// §W6): for every sub_goal still in verifying, either heal it when its
// latest verify run is terminal without a verdict (same policy as the
// run-end reconcile), or raise ONE stale alarm after an hour with no
// verdict. Safe to run concurrently with the run-end reconcile: every
// transition is conditional, and the pending-run check plus the per-wait
// verify-attempt count are the double-requeue/double-park guards.
//
// Receiver note: the v2 sub-goal layer lives on GoalService (there is no
// SubGoalService type), so SweepVerifying is a GoalService method; the
// daemon wrapper in internal/daemon/verify_sweeper.go stays thin.
func (s *GoalService) SweepVerifying(ctx context.Context) (requeued, parked, alerted int, err error) {
	type verifyingRow struct {
		id, goalID, title, createdAt string
	}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id, goal_id, title, created_at FROM sub_goal WHERE status='verifying'`)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("scan verifying sub-goals: %w", err)
	}
	var items []verifyingRow
	for rows.Next() {
		var r verifyingRow
		if err := rows.Scan(&r.id, &r.goalID, &r.title, &r.createdAt); err != nil {
			rows.Close()
			return 0, 0, 0, err
		}
		items = append(items, r)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return 0, 0, 0, err
	}
	for _, sg := range items {
		var runID, runStatus, runCreated string
		qerr := s.st.DB().QueryRowContext(ctx,
			`SELECT id, status, created_at FROM run WHERE sub_goal_id=? AND role='verify' ORDER BY created_at DESC, rowid DESC LIMIT 1`,
			sg.id).Scan(&runID, &runStatus, &runCreated)
		if qerr != nil && !errors.Is(qerr, sql.ErrNoRows) {
			return requeued, parked, alerted, fmt.Errorf("load latest verify run: %w", qerr)
		}
		terminal := qerr == nil && (runStatus == "completed" || runStatus == "failed" || runStatus == "cancelled")
		if terminal {
			heal, herr := s.healVerifyingSubGoal(ctx, sg.id, runID, runStatus)
			if herr != nil {
				return requeued, parked, alerted, fmt.Errorf("heal verifying sub-goal %s: %w", sg.id, herr)
			}
			if heal.requeued {
				requeued++
			}
			if heal.parked {
				parked++
			}
			if heal.requeued || heal.parked {
				// The heal either restarted the wait (attempt 2) or already
				// parked the goal — a stale alarm would be redundant noise.
				continue
			}
		}
		// Stale check: the wait starts with the latest verify run; with no
		// verify run at all only the sub-goal's own created_at remains (the
		// sub_goal table has no updated_at — contract's schema check).
		start := runCreated
		if runID == "" {
			start = sg.createdAt
		}
		startAt, perr := time.Parse(time.RFC3339Nano, start)
		if perr != nil || time.Since(startAt) <= verifyStaleWindow {
			continue
		}
		fired, aerr := s.alertStaleVerifying(ctx, sg.id, sg.goalID, sg.title, runID, time.Since(startAt))
		if aerr != nil {
			return requeued, parked, alerted, fmt.Errorf("alert stale verifying sub-goal %s: %w", sg.id, aerr)
		}
		if fired {
			alerted++
		}
	}
	return requeued, parked, alerted, nil
}

// alertStaleVerifying posts the ONE stale-verifying alarm for a sub-goal
// (contract 7.3): a system comment plus activity_log action
// verify_stale_alert. Dedupe is the existing verify_stale_alert activity for
// this sub-goal (no new column) — checked and written in ONE transaction so
// a crash or a concurrent sweep cannot duplicate the comment. Returns
// whether a new alarm was posted.
func (s *GoalService) alertStaleVerifying(ctx context.Context, subGoalID, goalID, title, runID string, waited time.Duration) (bool, error) {
	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	pattern := `%"sub_goal_id":"` + subGoalID + `"%`
	var existing string
	err = tx.QueryRowContext(ctx,
		`SELECT id FROM activity_log WHERE goal_id=? AND action='verify_stale_alert' AND detail LIKE ? LIMIT 1`,
		goalID, pattern).Scan(&existing)
	if err == nil {
		return false, nil // already alarmed for this work item
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("check stale alert: %w", err)
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO comment (id,goal_id,author_type,author_id,parent_id,content,created_at) VALUES (?,?,'system','',NULL,?,?)`,
		newID(), goalID, fmt.Sprintf("Work item %s has been verifying for %s — no verdict yet. Check the verifier or park it manually.", title, formatVerifyDuration(waited)), now()); err != nil {
		return false, fmt.Errorf("insert stale alert comment: %w", err)
	}
	detail, _ := json.Marshal(map[string]string{"sub_goal_id": subGoalID, "run_id": runID})
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO activity_log (id,goal_id,actor_type,actor_id,action,detail,created_at) VALUES (?,?,'system','','verify_stale_alert',?,?)`,
		newID(), goalID, string(detail), now()); err != nil {
		return false, fmt.Errorf("insert stale alert activity: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	logging.Infof("subgoal: %s stale verifying (%s) — alarm posted", subGoalID, formatVerifyDuration(waited))
	return true, nil
}

// ReconcileVerifyRun closes a verify run (决策 6-5): the verdict tool already
// made the sub-goal transition — the run itself is discarded (verify runs
// have no goal authority). The verifier's verdict lives in verification_result
// (structured) + the agent's own `goal comment` if it has an opinion.
//
// Verify-integrity heal (findings §W6, contract 7.2): a verify run can end
// WITHOUT a verdict (machine cut, forgotten tool call, the late-verdict
// race). The run then must not die silently: in the same transaction as the
// reconciled_at stamp, healVerifyingSubGoalTx either re-dispatches the
// verifier once or parks the goal for a human. The heal's events are
// published after the commit, like the run:discarded notice.
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
	var heal verifyHealResult
	if rc.SubGoalID != "" {
		heal, err = s.healVerifyingSubGoalTx(ctx, tx, rc.SubGoalID, rc.RunID, rc.Status)
		if err != nil {
			return err
		}
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.bus.Publish(ctx, events.Event{Topic: "run:discarded", Payload: map[string]any{
		"run_id": rc.RunID, "goal_id": rc.GoalID, "reason": "verify-run",
	}})
	for _, e := range heal.events {
		s.bus.Publish(ctx, e)
	}
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
func (s *GoalService) CancelSubGoal(ctx context.Context, subGoalID string) (*SubGoal, error) {
	res, err := s.st.DB().ExecContext(ctx,
		`UPDATE sub_goal SET status='cancelled' WHERE id=? AND status NOT IN ('verified','cancelled','failed')`,
		subGoalID)
	if err != nil {
		return nil, fmt.Errorf("cancel sub-goal: %w", err)
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return nil, NewValidationError("the sub-goal is already terminal")
	}
	if _, err := s.st.DB().ExecContext(ctx,
		`UPDATE run SET status='cancelled' WHERE sub_goal_id=? AND status='queued'`, subGoalID); err != nil {
		return nil, fmt.Errorf("cancel sub-goal queued runs: %w", err)
	}
	sg, err := s.GetSubGoal(ctx, subGoalID)
	if err != nil {
		return nil, err
	}
	s.bus.Publish(ctx, events.Event{Topic: "sub_goal.cancelled", Payload: map[string]any{
		"goal_id": sg.GoalID, "sub_goal_id": sg.ID,
	}})
	return sg, nil
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

	var goalID string
	if err := tx.QueryRowContext(ctx, `SELECT goal_id FROM sub_goal WHERE id=?`, subGoalID).Scan(&goalID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("load sub-goal: %w", err)
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
