package service

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/store"
)

// seedExecutorAgent seeds a runtime+agent with an explicit max_concurrent so
// the executor single-flight test can exercise both the default (1) and the
// opt-in parallel (2) shapes. (seedAgent in goal_test.go fixes 2.)
func seedExecutorAgent(t *testing.T, st *store.Store, name string, maxConcurrent int) string {
	t.Helper()
	ctx := context.Background()
	rt, err := NewRuntimeService(st).Create(ctx, Runtime{Name: "rt-" + name, MachineID: "m1"})
	if err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	if err := NewMachineService(st).Register(ctx, Machine{ID: "m1", Name: "m1", Hostname: "host"}, "[]"); err != nil {
		t.Fatalf("seed machine: %v", err)
	}
	a, err := NewAgentService(st, events.NewBus()).Create(ctx, Agent{Name: name, RuntimeID: rt.ID, MaxConcurrent: maxConcurrent})
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	return a.ID
}

// TestClaimSerializesMutatingRunsPerAgent: with the default max_concurrent=1 a
// consult run for the SAME (goal, agent) as a running owner run must stay
// queued — one agent must not execute two mutating turns of one goal at once
// (the "double run" defect: subgoal dispatch + mention consult overlapped).
// After the owner finishes, the consult claims.
func TestClaimSerializesMutatingRunsPerAgent(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	agentA := seedExecutorAgent(t, st, "A", 1)
	g, err := gs.Create(ctx, Goal{Title: "serialize", AssigneeType: "agent", AssigneeID: agentA, Status: "active", DomainID: seedDomain(t, st)})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	owner := enqueueFirst(t, rs, g)
	c, err := rs.Claim(ctx, []string{agentA})
	if err != nil {
		t.Fatalf("claim owner: %v", err)
	}
	if c == nil || c.RunID != owner.ID {
		t.Fatalf("owner run must claim first, got %+v", c)
	}

	consult, err := rs.EnqueueForMention(ctx, g.ID, agentA, "mention-1")
	if err != nil {
		t.Fatalf("enqueue consult: %v", err)
	}
	if consult.ID == owner.ID {
		t.Fatal("consult must be a distinct run from the owner run")
	}
	if got, err := rs.Claim(ctx, []string{agentA}); err != nil {
		t.Fatalf("claim consult while owner runs: %v", err)
	} else if got != nil {
		t.Fatalf("mutating run must not claim while the same agent is running this goal, got %s role=%s", got.RunID, "consult")
	}

	// Owner finishes → the queued consult is admitted.
	if _, err := st.DB().ExecContext(ctx, `UPDATE run SET status='completed', finished_at=? WHERE id=?`, now(), owner.ID); err != nil {
		t.Fatal(err)
	}
	c, err = rs.Claim(ctx, []string{agentA})
	if err != nil {
		t.Fatalf("claim consult after owner finished: %v", err)
	}
	if c == nil || c.RunID != consult.ID {
		t.Fatalf("consult must claim once the owner finishes, got %+v", c)
	}
}

// TestClaimAllowsParallelMutatingRunsWithMaxConcurrent: max_concurrent>1 is the
// explicit opt-in for parallel executors on one agent — two mutating runs of
// the same goal may then run at once. (Owner-owner single-flight is a branch
// correctness rule and stays, so the pair here is owner + consult.)
func TestClaimAllowsParallelMutatingRunsWithMaxConcurrent(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	agentA := seedExecutorAgent(t, st, "A", 2)
	g, err := gs.Create(ctx, Goal{Title: "parallel", AssigneeType: "agent", AssigneeID: agentA, Status: "active", DomainID: seedDomain(t, st)})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	owner := enqueueFirst(t, rs, g)
	if c, err := rs.Claim(ctx, []string{agentA}); err != nil {
		t.Fatalf("claim owner: %v", err)
	} else if c == nil || c.RunID != owner.ID {
		t.Fatalf("owner run must claim, got %+v", c)
	}
	consult, err := rs.EnqueueForMention(ctx, g.ID, agentA, "mention-2")
	if err != nil {
		t.Fatalf("enqueue consult: %v", err)
	}
	c, err := rs.Claim(ctx, []string{agentA})
	if err != nil {
		t.Fatalf("claim consult with max_concurrent=2: %v", err)
	}
	if c == nil || c.RunID != consult.ID {
		t.Fatalf("max_concurrent=2 must allow the consult in parallel, got %+v", c)
	}
}

// TestMentionDedupeSkipsRedundantConsult: a mention for an agent that already
// has a queued/running run on the goal must NOT enqueue a second executor
// turn. The comment itself is kept (it is the feed's record of the ask) and
// activity_log records action=dedupe_consult with the existing run id. Once
// that run finishes, the same mention dispatches normally again.
func TestMentionDedupeSkipsRedundantConsult(t *testing.T) {
	gs, rs, cs, st := newTestCluster(t)
	ctx := context.Background()
	agentA := seedExecutorAgent(t, st, "A", 1)
	other := seedExecutorAgent(t, st, "B", 1)
	domID := seedDomain(t, st)
	g, err := gs.Create(ctx, Goal{Title: "dedupe", AssigneeType: "agent", AssigneeID: agentA, Status: "active", DomainID: domID})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	owner := enqueueFirst(t, rs, g)
	if c, err := rs.Claim(ctx, []string{agentA}); err != nil {
		t.Fatalf("claim owner: %v", err)
	} else if c == nil || c.RunID != owner.ID {
		t.Fatalf("owner run must claim, got %+v", c)
	}

	content := "[@" + "A" + "](mention://agent/" + agentA + ") please double-check"
	created, err := cs.Create(ctx, Comment{GoalID: g.ID, AuthorType: "human", AuthorID: "h1", Content: content})
	if err != nil {
		t.Fatalf("create mentioning comment: %v", err)
	}

	// No second run, comment kept.
	var n int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM run WHERE goal_id=? AND agent_id=?`, g.ID, agentA).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("dedupe must keep exactly the existing run, got %d runs", n)
	}
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM comment WHERE id=?`, created.ID).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("the mentioning comment itself must be kept, got %d rows", n)
	}
	var detailJSON string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT detail FROM activity_log WHERE goal_id=? AND action='dedupe_consult'`, g.ID).Scan(&detailJSON); err != nil {
		t.Fatalf("dedupe_consult activity row: %v", err)
	}
	var detail map[string]string
	if err := json.Unmarshal([]byte(detailJSON), &detail); err != nil {
		t.Fatalf("decode dedupe detail %q: %v", detailJSON, err)
	}
	if detail["agent_id"] != agentA || detail["existing_run_id"] != owner.ID {
		t.Fatalf("dedupe detail = %v, want agent_id=%s existing_run_id=%s", detail, agentA, owner.ID)
	}

	// Same mention on an agent with NO live run dispatches normally. Use a
	// second goal so agentA's finished-run history does not confuse the check.
	g2, err := gs.Create(ctx, Goal{Title: "dedupe2", AssigneeType: "agent", AssigneeID: other, Status: "active", DomainID: domID})
	if err != nil {
		t.Fatalf("create second goal: %v", err)
	}
	if _, err := cs.Create(ctx, Comment{GoalID: g2.ID, AuthorType: "human", AuthorID: "h1",
		Content: "[@A](mention://agent/" + agentA + ") please help"}); err != nil {
		t.Fatalf("create second mentioning comment: %v", err)
	}
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM run WHERE goal_id=? AND agent_id=?`, g2.ID, agentA).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("mention without a live run must dispatch a consult, got %d runs", n)
	}
}
