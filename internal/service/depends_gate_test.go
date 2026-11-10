package service

// Depends-on gating tests (W-RSI, 2026-11-08): a sub-goal created with
// depends_on only gets its first run when every declared dependency is
// verified; wakeDependentsTx releases gated sub-goals in the same
// transaction that verifies the last unmet dep.

import (
	"context"
	"testing"

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
