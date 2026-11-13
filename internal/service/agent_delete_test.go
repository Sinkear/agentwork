package service

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/store"
)

// Delete-guard/impact tests: deleting an agent must refuse only LIVE work
// (queued/running runs, non-terminal goals) and must cascade its schedules
// (with their schedule_run history). Terminal goals survive as audit history.

func mustExec(t *testing.T, st *store.Store, query string, args ...any) {
	t.Helper()
	if _, err := st.DB().ExecContext(context.Background(), query, args...); err != nil {
		t.Fatalf("exec %q: %v", query, err)
	}
}

func countRows(t *testing.T, st *store.Store, query string, args ...any) int {
	t.Helper()
	var n int
	if err := st.DB().QueryRowContext(context.Background(), query, args...).Scan(&n); err != nil {
		t.Fatalf("count %q: %v", query, err)
	}
	return n
}

func codedCode(err error) string {
	var ce CodedError
	if errors.As(err, &ce) {
		return ce.Code()
	}
	return ""
}

// seedRun inserts a run for the agent in the given status and returns its id.
func seedRun(t *testing.T, st *store.Store, agentID, status string) string {
	t.Helper()
	id := "run-" + status
	mustExec(t, st, `INSERT INTO run (id,agent_id,status,queued_at,created_at) VALUES (?,?,?,?,?)`,
		id, agentID, status, now(), now())
	return id
}

// seedGoal inserts a goal assigned to the agent with the given status and
// returns its id.
func seedGoal(t *testing.T, st *store.Store, n int, agentID, status string) string {
	t.Helper()
	id := fmt.Sprintf("goal-%s-%d", status, n)
	mustExec(t, st, `INSERT INTO goal (id,title,assignee_type,assignee_id,status,created_at) VALUES (?,?,?,?,?,?)`,
		id, id, "agent", agentID, status, now())
	return id
}

// seedSchedule inserts a schedule assigned to the agent and returns its id.
func seedSchedule(t *testing.T, st *store.Store, agentID string, enabled int) string {
	t.Helper()
	id := fmt.Sprintf("sched-%d", enabled)
	mustExec(t, st, `INSERT INTO schedule (id,name,title_template,assignee_type,assignee_id,cron_expression,enabled,created_at)
		VALUES (?,?,?,?,?,?,?,?)`,
		id, "nightly", "nightly goal", "agent", agentID, "0 3 * * *", enabled, now())
	return id
}

// TestDeleteAgent_TerminalHistoryAndDisabledSchedule: only terminal goals and
// a disabled schedule exist → the delete succeeds, keeps the goal rows and
// removes the schedule with its schedule_run rows, the agent's runs and the
// run's chat_message cache.
func TestDeleteAgent_TerminalHistoryAndDisabledSchedule(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	as := NewAgentService(st, events.NewBus())
	agentID := seedAgent(t, st, "worker")

	doneGoal := seedGoal(t, st, 1, agentID, "done")
	seedGoal(t, st, 2, agentID, "cancelled")
	schedID := seedSchedule(t, st, agentID, 0)
	mustExec(t, st, `INSERT INTO schedule_run (id,schedule_id,goal_id,planned_at,created_at) VALUES (?,?,?,?,?)`,
		"sr-1", schedID, doneGoal, now(), now())
	runID := seedRun(t, st, agentID, "completed")
	mustExec(t, st, `INSERT INTO chat_message (id,run_id,role,content,created_at) VALUES (?,?,?,?,?)`,
		"cm-1", runID, "assistant", "done", now())

	if err := as.Delete(ctx, agentID); err != nil {
		t.Fatalf("delete agent with terminal history: %v", err)
	}

	if n := countRows(t, st, `SELECT COUNT(*) FROM agent WHERE id=?`, agentID); n != 0 {
		t.Fatalf("agent row survived delete: %d", n)
	}
	// Terminal goal history is deliberately kept (dangling assignee id).
	if n := countRows(t, st, `SELECT COUNT(*) FROM goal WHERE assignee_id=?`, agentID); n != 2 {
		t.Fatalf("terminal goal history must be kept, got %d", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM schedule WHERE id=?`, schedID); n != 0 {
		t.Fatalf("assigned schedule must be deleted, got %d", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM schedule_run WHERE schedule_id=?`, schedID); n != 0 {
		t.Fatalf("schedule_run rows must be deleted with their schedule, got %d", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM run WHERE agent_id=?`, agentID); n != 0 {
		t.Fatalf("run rows must be deleted, got %d", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM chat_message WHERE run_id=?`, runID); n != 0 {
		t.Fatalf("chat_message cache must be deleted, got %d", n)
	}
}

// TestDeleteAgent_RefusesActiveGoal: a non-terminal goal (active/review/
// backlog) is live work and must refuse the delete with CodeAgentHasGoals.
func TestDeleteAgent_RefusesActiveGoal(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	as := NewAgentService(st, events.NewBus())
	agentID := seedAgent(t, st, "owner")
	seedGoal(t, st, 1, agentID, "active")
	seedGoal(t, st, 2, agentID, "review")

	err := as.Delete(ctx, agentID)
	if err == nil {
		t.Fatal("delete must be refused while a non-terminal goal is assigned")
	}
	if code := codedCode(err); code != CodeAgentHasGoals {
		t.Fatalf("code = %q, want %q (err=%v)", code, CodeAgentHasGoals, err)
	}
	var de DetailedError
	if !errors.As(err, &de) || de.Detail()["count"] != 2 {
		t.Fatalf("detail count should be 2, got %#v", de.Detail())
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM agent WHERE id=?`, agentID); n != 1 {
		t.Fatalf("agent must survive a refused delete, got %d", n)
	}

	imp, err := as.DeleteImpact(ctx, agentID)
	if err != nil {
		t.Fatalf("impact: %v", err)
	}
	if imp.CanDelete || imp.ActiveGoals != 2 || len(imp.Blockers) != 1 || imp.Blockers[0].Kind != "active_goals" {
		t.Fatalf("impact with active goals = %+v", imp)
	}
}

// TestDeleteAgent_RefusesQueuedRun: a queued run would be orphaned mid-flight,
// so it blocks the delete with CodeAgentHasRunningRuns (same for running).
func TestDeleteAgent_RefusesQueuedRun(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	as := NewAgentService(st, events.NewBus())
	agentID := seedAgent(t, st, "worker")
	seedRun(t, st, agentID, "queued")
	seedRun(t, st, agentID, "running")
	// history alone does not block
	seedRun(t, st, agentID, "completed")

	err := as.Delete(ctx, agentID)
	if err == nil {
		t.Fatal("delete must be refused while queued/running runs exist")
	}
	if code := codedCode(err); code != CodeAgentHasRunningRuns {
		t.Fatalf("code = %q, want %q (err=%v)", code, CodeAgentHasRunningRuns, err)
	}

	imp, err := as.DeleteImpact(ctx, agentID)
	if err != nil {
		t.Fatalf("impact: %v", err)
	}
	if imp.CanDelete || imp.ActiveRuns != 2 || imp.Runs != 3 || len(imp.Blockers) != 1 ||
		imp.Blockers[0].Kind != "active_runs" || imp.Blockers[0].Count != 2 {
		t.Fatalf("impact with active runs = %+v", imp)
	}
}

// TestAgentDeleteImpact_TerminalHistoryOnly covers the CodeArts-like data:
// 25 done/cancelled goals + 1 disabled schedule → can_delete=true with the
// cascade counts the confirm dialog shows, and the delete then succeeds while
// keeping all 25 terminal goal rows.
func TestAgentDeleteImpact_TerminalHistoryOnly(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()
	as := NewAgentService(st, events.NewBus())
	agentID := seedAgent(t, st, "codearts")

	for i := 0; i < 25; i++ {
		status := "done"
		if i%2 == 1 {
			status = "cancelled"
		}
		seedGoal(t, st, i, agentID, status)
	}
	seedSchedule(t, st, agentID, 0)
	// A non-terminal sub-goal would be cancelled by the delete.
	firstGoal := ""
	_ = st.DB().QueryRowContext(ctx,
		`SELECT id FROM goal WHERE assignee_id=? LIMIT 1`, agentID).Scan(&firstGoal)
	mustExec(t, st, `INSERT INTO sub_goal (id,goal_id,title,assignee_id,status,created_at) VALUES (?,?,?,?,?,?)`,
		"sg-1", firstGoal, "work", agentID, "running", now())

	imp, err := as.DeleteImpact(ctx, agentID)
	if err != nil {
		t.Fatalf("impact: %v", err)
	}
	if !imp.CanDelete {
		t.Fatalf("terminal-only agent must be deletable, blockers=%+v", imp.Blockers)
	}
	if imp.AgentID != agentID || imp.ActiveRuns != 0 || imp.ActiveGoals != 0 ||
		imp.Runs != 0 || imp.Schedules != 1 || imp.EnabledSchedules != 0 ||
		imp.TerminalGoals != 25 || imp.SubGoals != 1 || len(imp.Blockers) != 0 {
		t.Fatalf("unexpected impact: %+v", imp)
	}
	// Blockers must encode as [] (not null) for the frontend.
	if imp.Blockers == nil {
		t.Fatal("blockers must be an empty slice, not nil")
	}

	if err := as.Delete(ctx, agentID); err != nil {
		t.Fatalf("delete terminal-only agent: %v", err)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM goal WHERE assignee_id=?`, agentID); n != 25 {
		t.Fatalf("25 terminal goals must survive, got %d", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM schedule WHERE assignee_id=?`, agentID); n != 0 {
		t.Fatalf("schedule must be deleted, got %d", n)
	}
	if n := countRows(t, st, `SELECT COUNT(*) FROM sub_goal WHERE assignee_id=? AND status!='cancelled'`, agentID); n != 0 {
		t.Fatalf("non-terminal sub-goal must be cancelled, got %d", n)
	}
}

// TestAgentDeleteImpact_NotFound: unknown agent → ErrNotFound (handler 404).
func TestAgentDeleteImpact_NotFound(t *testing.T) {
	st := newTestStore(t)
	as := NewAgentService(st, events.NewBus())
	if _, err := as.DeleteImpact(context.Background(), "ghost"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("unknown agent impact: got %v, want ErrNotFound", err)
	}
}
