package service

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/store"
)

// TestRSIRoundNumberingPerDomain (P-B 回归, RSI R3 提案【已批准】): the
// rsi_round ordinal is DOMAIN-scoped — every train goal terminal creates a
// NEW training_session, so the pre-P-B session-level MAX restarted the
// numbering at 1 each round and delta_json stayed empty forever. Two goals
// in the same domain, one analysis each, must produce round_no 1 then 2,
// with the second round's delta computed against the first round's metrics.
func TestRSIRoundNumberingPerDomain(t *testing.T) {
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
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.DB().Exec(`INSERT INTO domain (id,type,name,created_at) VALUES ('dom-pb','scratch','pb-test',?)`, ts); err != nil {
		t.Fatalf("domain: %v", err)
	}
	if _, err := st.DB().Exec(`INSERT INTO runtime (id,name,status,created_at) VALUES ('rt-pb','pb@local','active',?)`, ts); err != nil {
		t.Fatalf("runtime: %v", err)
	}
	if _, err := st.DB().Exec(`INSERT INTO agent (id,name,runtime_id,created_at) VALUES ('ag-pb','Pb','rt-pb',?)`, ts); err != nil {
		t.Fatalf("agent: %v", err)
	}

	// Two train goals in the SAME domain, each terminal → one analysis.
	analyze := func(goalID string) string {
		t.Helper()
		rsi.OnGoalTerminal(ctx, goalID)
		var sid, status string
		if err := st.DB().QueryRow(`SELECT id,status FROM training_session WHERE goal_id=?`, goalID).Scan(&sid, &status); err != nil {
			t.Fatalf("session for %s: %v", goalID, err)
		}
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
			t.Fatalf("goal %s: session status = %q (error=%q)", goalID, status, errMsg)
		}
		return sid
	}

	created1, err := gsvc.Create(ctx, Goal{Title: "第一轮训练", Status: "done", DomainID: "dom-pb",
		AssigneeType: "agent", AssigneeID: "ag-pb",
		ExecutionMode: "train", ContentCapture: "redacted", SampleRate: 1, BudgetUSD: 2})
	if err != nil {
		t.Fatalf("create goal1: %v", err)
	}
	created2, err := gsvc.Create(ctx, Goal{Title: "第二轮训练", Status: "done", DomainID: "dom-pb",
		AssigneeType: "agent", AssigneeID: "ag-pb",
		ExecutionMode: "train", ContentCapture: "redacted", SampleRate: 1, BudgetUSD: 2})
	if err != nil {
		t.Fatalf("create goal2: %v", err)
	}

	sid1 := analyze(created1.ID)
	sid2 := analyze(created2.ID)

	// Each session's ledger holds exactly one round.
	rounds1, err := rsi.ListRounds(ctx, sid1)
	if err != nil {
		t.Fatalf("ListRounds 1: %v", err)
	}
	rounds2, err := rsi.ListRounds(ctx, sid2)
	if err != nil {
		t.Fatalf("ListRounds 2: %v", err)
	}
	if len(rounds1) != 1 || len(rounds2) != 1 {
		t.Fatalf("rounds per session = %d/%d, want 1/1", len(rounds1), len(rounds2))
	}

	// DOMAIN-level numbering: 1 then 2 — not two "round 1" rows.
	if rounds1[0].RoundNo != 1 {
		t.Fatalf("round1.RoundNo = %d, want 1", rounds1[0].RoundNo)
	}
	if rounds2[0].RoundNo != 2 {
		t.Fatalf("round2.RoundNo = %d, want 2 (domain-scoped numbering)", rounds2[0].RoundNo)
	}
	if rounds1[0].Trigger != "terminal" {
		t.Fatalf("round1.Trigger = %q, want terminal", rounds1[0].Trigger)
	}
	if rounds2[0].Trigger != "manual_retry" {
		t.Fatalf("round2.Trigger = %q, want manual_retry (prevNo != 0)", rounds2[0].Trigger)
	}

	// Round 2's delta is computed against round 1's metrics — non-empty
	// (both graphs carry parallel_speedup ≥ 1, a headline delta key).
	if len(rounds1[0].Delta) != 0 {
		t.Fatalf("round1 delta must be empty (first round), got %v", rounds1[0].Delta)
	}
	if len(rounds2[0].Delta) == 0 {
		t.Fatalf("round2 delta_json is empty — prevMetrics was not located across sessions")
	}
	if _, ok := rounds2[0].Delta["parallel_speedup"]; !ok {
		t.Fatalf("round2 delta missing parallel_speedup: %v", rounds2[0].Delta)
	}
}
