package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/store"
)

// rsiWorkflowFixture builds: goal(train) → 2 sub-goals (B depends_on A) with
// runs, one rejected then one passed verification on B, one consult pair
// (requester in A's run → target), one change row, and one gate_decision.
func rsiWorkflowFixture(t *testing.T, st *store.Store, svc *GoalService, bus *events.Bus) (goalID, sessionVia string) {
	t.Helper()
	ctx := context.Background()
	now := time.Now().UTC()

	if _, err := st.DB().Exec(`INSERT INTO runtime (id,name,status,created_at) VALUES ('rt-1','test@local','active',?)`, now); err != nil {
		t.Fatalf("runtime: %v", err)
	}
	if _, err := st.DB().Exec(`INSERT INTO domain (id,type,name,created_at) VALUES ('dom-1','scratch','rsi-test',?)`, now); err != nil {
		t.Fatalf("domain: %v", err)
	}
	mkAgent := func(id, name string) {
		_, err := st.DB().Exec(`INSERT INTO agent (id,name,runtime_id,created_at) VALUES (?,?, 'rt-1',?)`, id, name, now)
		if err != nil {
			t.Fatalf("agent: %v", err)
		}
	}
	mkAgent("ag-lead", "Lead")
	mkAgent("ag-a", "Alice")
	mkAgent("ag-b", "Bob")

	created, err := svc.Create(ctx, Goal{ID: newID(), Title: "编排训练样本", Status: "active", DomainID: "dom-1",
		AssigneeType: "agent", AssigneeID: "ag-lead",
		ExecutionMode: "train", ContentCapture: "redacted", SampleRate: 1, BudgetUSD: 2})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	goalID = created.ID

	base := now.Add(-2 * time.Hour)
	cts := base.Add(-5 * time.Minute).UTC().Format(time.RFC3339Nano)
	sub := func(id, title, assignee, deps, status string) {
		_, err := st.DB().Exec(`INSERT INTO sub_goal (id,goal_id,title,assignee_id,verifier_id,status,depends_on,created_at)
		 VALUES (?,?,?,?,?,?,?,?)`, id, goalID, title, assignee, "ag-lead", status, deps, cts)
		if err != nil {
			t.Fatalf("sub_goal: %v", err)
		}
	}
	sub("sg-a", "实现解析器", "ag-a", "[]", "verified")
	sub("sg-b", "实现渲染器", "ag-b", `["sg-a"]`, "verified")

	run := func(id, sgid, agent, role string, start, finish time.Time, status string) {
		// Store the same RFC3339Nano strings the app writes (the SQLite
		// driver's own time.Time encoding has a different layout).
		sts, fis := start.UTC().Format(time.RFC3339Nano), finish.UTC().Format(time.RFC3339Nano)
		_, err := st.DB().Exec(`INSERT INTO run (id,goal_id,sub_goal_id,agent_id,role,status,attempt,queued_at,started_at,finished_at,created_at)
		 VALUES (?,?,?,?,?,?,1,?,?,?,?)`, id, goalID, sgid, agent, role, status, sts, sts, fis, sts)
		if err != nil {
			t.Fatalf("run: %v", err)
		}
	}
	run("run-a1", "sg-a", "ag-a", "subgoal", base, base.Add(20*time.Minute), "completed")
	// B runs AFTER A ends (creates an explicit depends_on edge too).
	run("run-b1", "sg-b", "ag-b", "subgoal", base.Add(30*time.Minute), base.Add(50*time.Minute), "completed")
	// A consults B mid-flight (mid-execution consult).
	run("run-consult-1", "", "ag-b", "consult", base.Add(10*time.Minute), base.Add(12*time.Minute), "completed")
	_, err = st.DB().Exec(`INSERT INTO consult_request (id,goal_id,requester_agent_id,requester_run_id,target_agent_id,guest_run_id,created_at)
	 VALUES ('cq-1',?,?,?,?,'run-consult-1',?)`, goalID, "ag-a", "run-a1", "ag-b", base.Add(9*time.Minute))
	if err != nil {
		t.Fatalf("consult_request: %v", err)
	}

	// Verification on B: one rejection (rework), then a pass.
	_, err = st.DB().Exec(`INSERT INTO verification_result (id,goal_id,sub_goal_id,verifier_run_id,status,summary,created_at)
	 VALUES ('vr-b1',?,'sg-b','run-b1','rejected','缺少边界情况',?)`, goalID, base.Add(55*time.Minute))
	if err != nil {
		t.Fatalf("verification: %v", err)
	}
	run("run-b2", "sg-b", "ag-b", "subgoal", base.Add(60*time.Minute), base.Add(80*time.Minute), "completed")
	_, err = st.DB().Exec(`INSERT INTO verification_result (id,goal_id,sub_goal_id,verifier_run_id,status,summary,created_at)
	 VALUES ('vr-b2',?,'sg-b','run-b2','passed','ok',?)`, goalID, base.Add(85*time.Minute))
	if err != nil {
		t.Fatalf("verification2: %v", err)
	}

	_, err = st.DB().Exec(`INSERT INTO change (id,goal_id,sub_goal_id,status,created_at) VALUES ('chg-1',?,'sg-b','integrated',?)`, goalID, base.Add(90*time.Minute))
	if err != nil {
		t.Fatalf("change: %v", err)
	}
	_, err = st.DB().Exec(`INSERT INTO gate_decision (id,goal_id,run_id,gate_rule,decision,decided_by,decided_at,review_duration)
	 VALUES ('gd-1',?,'run-b2','merge','approve','human',?,1800)`, goalID, base.Add(100*time.Minute))
	if err != nil {
		t.Fatalf("gate: %v", err)
	}
	return goalID, ""
}

func TestRSIWorkflowEndToEnd(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "aw.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	defer st.Close()
	bus := events.NewBus()
	gsvc := NewGoalService(st, bus)
	rsi := NewRSIService(st, bus)
	gsvc.SetRSI(rsi)

	ctx := context.Background()
	goalID, _ := rsiWorkflowFixture(t, st, gsvc, bus)

	// Session was ensured at goal creation.
	var sid, status string
	if err := st.DB().QueryRow(`SELECT id,status FROM training_session WHERE goal_id=?`, goalID).Scan(&sid, &status); err != nil {
		t.Fatalf("session missing: %v", err)
	}
	if status != "waiting" {
		t.Fatalf("status = %q, want waiting", status)
	}

	// Live graph build (pre-analysis).
	g, err := rsi.BuildGoalWorkflow(ctx, goalID)
	if err != nil {
		t.Fatalf("BuildGoalWorkflow: %v", err)
	}
	if len(g.Nodes) < 5 {
		t.Fatalf("nodes = %d, want ≥5 (goal + 2 agent + verify + consult)", len(g.Nodes))
	}
	var hasDeps, hasConsult bool
	for _, e := range g.Edges {
		if e.Kind == "depends_on" {
			hasDeps = true
		}
		if e.Kind == "consult" {
			hasConsult = true
		}
	}
	if !hasDeps {
		t.Fatalf("depends_on edge missing: %+v", g.Edges)
	}
	if !hasConsult {
		t.Fatalf("consult edge missing: %+v", g.Edges)
	}
	if g.Metrics["parallel_speedup"].(float64) < 1 {
		t.Fatalf("parallel_speedup = %v", g.Metrics["parallel_speedup"])
	}
	if mi(g.Metrics, "queue_wait_ms") <= 0 {
		t.Fatalf("queue_wait_ms = %v, want > 0", g.Metrics["queue_wait_ms"])
	}

	// Terminal → analyze.
	rsi.OnGoalTerminal(ctx, goalID)
	deadline := time.Now().Add(10 * time.Second)
	for {
		_ = st.DB().QueryRow(`SELECT status FROM training_session WHERE id=?`, sid).Scan(&status)
		if status == "ready" || status == "failed" || time.Now().After(deadline) {
			break
		}
		time.Sleep(50 * time.Millisecond)
	}
	if status != "ready" {
		var errMsg string
		_ = st.DB().QueryRow(`SELECT error FROM training_session WHERE id=?`, sid).Scan(&errMsg)
		t.Fatalf("session status = %q (error=%q)", status, errMsg)
	}

	_, report, findings, frozen, err := rsi.GetSession(ctx, sid)
	if err != nil {
		t.Fatalf("GetSession: %v", err)
	}
	if report == nil || report.Status != "ready" {
		t.Fatalf("report = %+v", report)
	}
	if frozen == nil || !frozen.Frozen {
		t.Fatalf("frozen graph missing: %+v", frozen)
	}
	if len(findings) == 0 {
		t.Fatalf("expected findings for the fixture (rework=100%%), got 0")
	}
	cats := map[string]bool{}
	for _, f := range findings {
		cats[f.Category] = true
	}
	if !cats["brief_quality"] {
		t.Fatalf("brief_quality finding missing, got %v", cats)
	}

	// Idempotent re-analysis (Retry path): report stays latest-only, but the
	// round ledger APPENDS — that history is the insight panel.
	if err := rsi.Analyze(ctx, sid); err != nil {
		t.Fatalf("re-analyze: %v", err)
	}
	var n int
	_ = st.DB().QueryRow(`SELECT COUNT(*) FROM rsi_report WHERE session_id=?`, sid).Scan(&n)
	if n != 1 {
		t.Fatalf("reports = %d, want 1", n)
	}

	// Round ledger: round 1 (terminal, no delta) + round 2 (manual_retry, delta).
	rounds, err := rsi.ListRounds(ctx, sid)
	if err != nil {
		t.Fatalf("ListRounds: %v", err)
	}
	if len(rounds) != 2 {
		t.Fatalf("rounds = %d, want 2", len(rounds))
	}
	if rounds[0].RoundNo != 1 || rounds[0].Trigger != "terminal" || len(rounds[0].Delta) != 0 {
		t.Fatalf("round1 = %+v", rounds[0])
	}
	if rounds[1].RoundNo != 2 || rounds[1].Trigger != "manual_retry" || len(rounds[1].Delta) == 0 {
		t.Fatalf("round2 = %+v", rounds[1])
	}
	if len(rounds[1].Findings) == 0 {
		t.Fatalf("round2 findings not frozen")
	}
	if _, ok := rounds[1].Delta["rework_ratio"]; !ok {
		t.Fatalf("round2 delta missing rework_ratio: %v", rounds[1].Delta)
	}

	// Overview + squad summaries don't error; trend carries the rounds.
	ov, err := rsi.Overview(ctx, 30)
	if err != nil {
		t.Fatalf("Overview: %v", err)
	}
	if len(ov.Trend) < 2 {
		t.Fatalf("trend points = %d, want ≥2", len(ov.Trend))
	}
	if _, err := rsi.GoalWorkflow(ctx, goalID); err != nil {
		t.Fatalf("GoalWorkflow: %v", err)
	}
}
