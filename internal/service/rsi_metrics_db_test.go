package service

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/store"
)

func newRSITestEnv(t *testing.T) (*store.Store, *GoalService, *RSIService) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "aw.db"))
	if err != nil {
		t.Fatalf("store.Open: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	now := time.Now().UTC()
	for _, q := range []string{
		`INSERT INTO runtime (id,name,status,created_at) VALUES ('rt-1','test@local','active',?)`,
		`INSERT INTO domain (id,type,name,created_at) VALUES ('dom-1','scratch','rsi-metrics',?)`,
		`INSERT INTO agent (id,name,runtime_id,created_at) VALUES ('ag-1','Agent','rt-1',?)`,
		`INSERT INTO agent (id,name,runtime_id,created_at) VALUES ('ag-v','Verifier','rt-1',?)`,
	} {
		if _, err := st.DB().Exec(q, now); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}
	bus := events.NewBus()
	gsvc := NewGoalService(st, bus)
	rsi := NewRSIService(st, bus)
	gsvc.SetRSI(rsi)
	return st, gsvc, rsi
}

func mkRSIGoal(t *testing.T, gsvc *GoalService, title, mode string) *Goal {
	t.Helper()
	g, err := gsvc.Create(context.Background(), Goal{ID: newID(), Title: title, Status: "active", DomainID: "dom-1",
		AssigneeType: "agent", AssigneeID: "ag-1", ExecutionMode: mode,
		ContentCapture: "redacted", SampleRate: 1, BudgetUSD: 2})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	return g
}

func insertRun(t *testing.T, st *store.Store, id, goalID, sgID, role string, start, end time.Time) {
	t.Helper()
	sts, fis := start.UTC().Format(time.RFC3339Nano), end.UTC().Format(time.RFC3339Nano)
	_, err := st.DB().Exec(`INSERT INTO run (id,goal_id,sub_goal_id,agent_id,role,status,attempt,queued_at,started_at,finished_at,created_at)
	 VALUES (?,?,?,?,'subgoal','completed',1,?,?,?,?)`, id, goalID, sgID, "ag-1", sts, sts, fis, sts)
	if role != "subgoal" {
		_, err = st.DB().Exec(`UPDATE run SET role=? WHERE id=?`, role, id)
	}
	if err != nil {
		t.Fatalf("insert run: %v", err)
	}
}

func TestBuildGoalWorkflowQueueAndHumanWaitSplit(t *testing.T) {
	st, gsvc, rsi := newRSITestEnv(t)
	g := mkRSIGoal(t, gsvc, "queue-split", "infer")
	base := time.Now().UTC().Add(-2 * time.Hour)
	created := base.Format(time.RFC3339Nano)
	for _, q := range []struct {
		id, deps string
	}{
		{"sg-a", "[]"},
		{"sg-b", `["sg-a"]`},
	} {
		if _, err := st.DB().Exec(`INSERT INTO sub_goal (id,goal_id,title,assignee_id,verifier_id,status,depends_on,created_at)
		 VALUES (?,?,'task','ag-1','ag-v','verified',?,?)`, q.id, g.ID, q.deps, created); err != nil {
			t.Fatalf("sub_goal: %v", err)
		}
	}
	insertRun(t, st, "r-a", g.ID, "sg-a", "subgoal", base, base.Add(20*time.Minute))
	insertRun(t, st, "r-b", g.ID, "sg-b", "subgoal", base.Add(80*time.Minute), base.Add(90*time.Minute))
	if _, err := st.DB().Exec(`INSERT INTO gate_decision (id,goal_id,run_id,gate_rule,decision,decided_by,decided_at,review_duration)
	 VALUES ('gd-1',?,'r-b','merge','approve','human',?,3600)`, g.ID, created); err != nil {
		t.Fatalf("gate: %v", err)
	}

	gf, err := rsi.BuildGoalWorkflow(context.Background(), g.ID)
	if err != nil {
		t.Fatalf("build: %v", err)
	}
	// ready = A end = +20m: dependency wait 20m, dispatch 60m, total 80m.
	if got := mi(gf.Metrics, "dependency_wait_ms"); got != 20*60*1000 {
		t.Fatalf("dependency_wait_ms = %d, want 1200000", got)
	}
	if got := mi(gf.Metrics, "dispatch_wait_ms"); got != 60*60*1000 {
		t.Fatalf("dispatch_wait_ms = %d, want 3600000", got)
	}
	if got := mi(gf.Metrics, "queue_wait_ms"); got != 80*60*1000 {
		t.Fatalf("queue_wait_ms = %d, want 4800000", got)
	}
	// wall = 90m, human wait = 60m → share = 60/(90+60) = 0.4.
	if got := mf(gf.Metrics, "human_wait_share"); got < 0.399 || got > 0.401 {
		t.Fatalf("human_wait_share = %v, want 0.4", got)
	}
	if got := mi(gf.Metrics, "human_wait_ms"); got != 3_600_000 {
		t.Fatalf("human_wait_ms = %d, want 3600000", got)
	}
}

func TestAnalyzeRoundLedgerAcrossDomainSessions(t *testing.T) {
	st, gsvc, rsi := newRSITestEnv(t)
	ctx := context.Background()
	g1 := mkRSIGoal(t, gsvc, "round-one", "train")
	g2 := mkRSIGoal(t, gsvc, "round-two", "train")
	sid := func(goalID string) string {
		var id string
		if err := st.DB().QueryRow(`SELECT id FROM training_session WHERE goal_id=?`, goalID).Scan(&id); err != nil {
			t.Fatalf("session: %v", err)
		}
		return id
	}
	if err := rsi.Analyze(ctx, sid(g1.ID)); err != nil {
		t.Fatalf("analyze g1: %v", err)
	}
	if err := rsi.Analyze(ctx, sid(g2.ID)); err != nil {
		t.Fatalf("analyze g2: %v", err)
	}
	// Re-analysis of the first session continues the domain series too.
	if err := rsi.Analyze(ctx, sid(g1.ID)); err != nil {
		t.Fatalf("re-analyze g1: %v", err)
	}
	rows, err := st.DB().Query(`SELECT round_no,delta_json,trigger FROM rsi_round ORDER BY created_at ASC`)
	if err != nil {
		t.Fatalf("rounds: %v", err)
	}
	defer rows.Close()
	var nums []int
	var deltas []string
	var triggers []string
	for rows.Next() {
		var n int
		var dj, trig string
		if err := rows.Scan(&n, &dj, &trig); err != nil {
			t.Fatalf("scan: %v", err)
		}
		nums = append(nums, n)
		deltas = append(deltas, dj)
		triggers = append(triggers, trig)
	}
	if len(nums) != 3 || nums[0] != 1 || nums[1] != 2 || nums[2] != 3 {
		t.Fatalf("round_no series = %v, want [1 2 3]", nums)
	}
	var d2 map[string]any
	if err := json.Unmarshal([]byte(deltas[1]), &d2); err != nil || len(d2) == 0 {
		t.Fatalf("round 2 delta should be non-empty: %q (%v)", deltas[1], err)
	}
	if triggers[1] != "terminal" {
		t.Fatalf("round 2 trigger = %q, want terminal (first analysis of g2)", triggers[1])
	}
}

func TestGetSessionFillsTopFinding(t *testing.T) {
	st, gsvc, rsi := newRSITestEnv(t)
	ctx := context.Background()
	g := mkRSIGoal(t, gsvc, "top-finding", "train")
	var sid string
	if err := st.DB().QueryRow(`SELECT id FROM training_session WHERE goal_id=?`, g.ID).Scan(&sid); err != nil {
		t.Fatalf("session: %v", err)
	}
	if err := rsi.Analyze(ctx, sid); err != nil {
		t.Fatalf("analyze: %v", err)
	}
	var rid string
	if err := st.DB().QueryRow(`SELECT id FROM rsi_report WHERE session_id=?`, sid).Scan(&rid); err != nil {
		t.Fatalf("report: %v", err)
	}
	if _, err := st.DB().Exec(`INSERT INTO rsi_finding (id,report_id,category,severity,confidence,title,explanation,suggestion,evidence_json,score,status,created_at)
	 VALUES ('f-1',?,'serial_chain','warn','L1','top title','expl','sugg','[]',99,'open',?)`, rid, time.Now().UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("finding: %v", err)
	}
	x, _, _, _, err := rsi.GetSession(ctx, sid)
	if err != nil {
		t.Fatalf("get: %v", err)
	}
	if x.TopCategory != "serial_chain" || x.TopTitle != "top title" {
		t.Fatalf("top finding = %q/%q, want serial_chain/top title", x.TopCategory, x.TopTitle)
	}
}
