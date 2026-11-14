package daemon

import (
	"context"
	"strings"
	"testing"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/service"
	"github.com/eushing/agentwork/internal/store"
)

// scratchEvidenceJSON builds a bundle shaped like the CLI's upload.
func scratchEvidenceJSON(passed bool) string {
	state := "true"
	if !passed {
		state = "false"
	}
	return `{"kind":"scratch-verify","passed":` + state + `,"checked_at":"2026-11-14T00:00:00Z","report":"$ echo ok","commands":[{"cmd":"verify: echo ok","exit":0}]}`
}

func newScratchGateDaemon(t *testing.T) (*Daemon, *store.Store, string, string, string) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	bus := events.NewBus()
	rt, err := service.NewRuntimeService(st).Create(ctx, service.Runtime{Name: "rt-gate", MachineID: "m1"})
	if err != nil {
		t.Fatalf("runtime: %v", err)
	}
	agent, err := service.NewAgentService(st, bus).Create(ctx, service.Agent{Name: "gate-agent", RuntimeID: rt.ID})
	if err != nil {
		t.Fatalf("agent: %v", err)
	}
	domain, err := service.NewDomainService(st, bus).Create(ctx, service.Domain{Name: "gate-scratch", Type: "scratch"})
	if err != nil {
		t.Fatalf("domain: %v", err)
	}
	goal, err := service.NewGoalService(st, bus).Create(ctx, service.Goal{
		Title: "gate goal", DomainID: domain.ID, AssigneeType: "agent", AssigneeID: agent.ID,
	})
	if err != nil {
		t.Fatalf("goal: %v", err)
	}
	return &Daemon{st: st}, st, domain.ID, goal.ID, agent.ID
}

// TestScratchEvidencePassed fails closed: only a passed bundle of the right
// kind satisfies the gate — garbage, another kind, and passed=false do not.
func TestScratchEvidencePassed(t *testing.T) {
	cases := []struct {
		name string
		ev   string
		want bool
	}{
		{"empty", "", false},
		{"garbage", "not json", false},
		{"other kind", `{"kind":"repo-verify","passed":true}`, false},
		{"failed", scratchEvidenceJSON(false), false},
		{"passed", scratchEvidenceJSON(true), true},
	}
	for _, c := range cases {
		if got := scratchEvidencePassed(c.ev); got != c.want {
			t.Errorf("%s: scratchEvidencePassed=%v want %v", c.name, got, c.want)
		}
	}
}

// TestStampScratchEvidenceRequiresBundle locks the completion gate: a frozen
// Verify policy without a passed bundle fails the run's completion; a passed
// bundle is stamped on run.evidence. A domain with no frozen policy keeps
// report-only behavior.
func TestStampScratchEvidenceRequiresBundle(t *testing.T) {
	d, st, domainID, goalID, agentID := newScratchGateDaemon(t)
	ctx := context.Background()
	runID := "run-gate-1"
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO run (id,goal_id,agent_id,status,role,attempt,queued_at,created_at) VALUES (?,?,?,'completed','owner',1,'2026-11-14T00:59:00Z','2026-11-14T00:59:00Z')`,
		runID, goalID, agentID); err != nil {
		t.Fatalf("seed run: %v", err)
	}

	// Unfrozen: evidence optional, no failure.
	if got := d.stampScratchEvidence(ctx, runID, domainID, "owner", ""); got != "" {
		t.Fatalf("unfrozen domain should accept report-only delivery, got %q", got)
	}

	// Freeze the domain with a Verify policy.
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE domain SET checks='{"verify":["echo ok"]}', checks_compiled_at='2026-11-14T00:00:00Z' WHERE id=?`,
		domainID); err != nil {
		t.Fatalf("freeze domain: %v", err)
	}
	got := d.stampScratchEvidence(ctx, runID, domainID, "owner", "")
	if !strings.Contains(got, "verification evidence") {
		t.Fatalf("frozen domain without evidence: got failure %q, want evidence failure", got)
	}
	got = d.stampScratchEvidence(ctx, runID, domainID, "owner", scratchEvidenceJSON(false))
	if !strings.Contains(got, "verification evidence") {
		t.Fatalf("failed evidence must not pass the gate, got %q", got)
	}
	if got := d.stampScratchEvidence(ctx, runID, domainID, "owner", scratchEvidenceJSON(true)); got != "" {
		t.Fatalf("passed evidence should pass the gate, got %q", got)
	}
	var stamped string
	if err := st.DB().QueryRowContext(ctx, `SELECT evidence FROM run WHERE id=?`, runID).Scan(&stamped); err != nil {
		t.Fatalf("read evidence: %v", err)
	}
	if !scratchEvidencePassed(stamped) {
		t.Fatalf("evidence not stamped on the run: %q", stamped)
	}

	// Read-only roles are never gated (they judge, they don't deliver).
	if got := d.stampScratchEvidence(ctx, "run-verify-1", domainID, "verify", ""); got != "" {
		t.Fatalf("verify role must not be gated, got %q", got)
	}
}

// TestScratchDeliveryBlocker locks the delivery face: a frozen Verify policy
// refuses delivery when the latest completed writable run has no passed
// bundle; an unfrozen domain delivers on the report.
func TestScratchDeliveryBlocker(t *testing.T) {
	d, st, domainID, goalID, agentID := newScratchGateDaemon(t)
	ctx := context.Background()
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO run (id,goal_id,agent_id,status,role,attempt,queued_at,created_at,finished_at) VALUES ('run-deliver-1',?,?,'completed','owner',1,'2026-11-14T00:59:00Z','2026-11-14T00:59:00Z','2026-11-14T01:00:00Z')`,
		goalID, agentID); err != nil {
		t.Fatalf("seed run: %v", err)
	}
	if _, ok := d.scratchDeliveryBlocker(ctx, goalID, domainID); !ok {
		t.Fatalf("unfrozen domain should deliver on the report")
	}
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE domain SET checks='{"verify":["echo ok"]}', checks_compiled_at='2026-11-14T00:00:00Z' WHERE id=?`,
		domainID); err != nil {
		t.Fatalf("freeze domain: %v", err)
	}
	if msg, ok := d.scratchDeliveryBlocker(ctx, goalID, domainID); ok || !strings.Contains(msg, "交付校验") {
		t.Fatalf("frozen domain without evidence must refuse delivery, got ok=%v msg=%q", ok, msg)
	}
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE run SET evidence=? WHERE id='run-deliver-1'`, scratchEvidenceJSON(true)); err != nil {
		t.Fatalf("stamp evidence: %v", err)
	}
	if msg, ok := d.scratchDeliveryBlocker(ctx, goalID, domainID); !ok {
		t.Fatalf("passed evidence should allow delivery, got %q", msg)
	}
}
