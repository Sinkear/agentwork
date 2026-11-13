package service

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/eushing/agentwork/internal/events"
)

// TestGoalCategoryHITLParkedByGate is the HITL control arm for the v3
// HITL/HOOTL feature: a completed run that hits a checkpoint gate still parks
// the goal in review, the reviewing event carries the category, and the
// goal's creation activity records the default hitl mode.
func TestGoalCategoryHITLParkedByGate(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	agent := seedAgent(t, st, "A")
	domID := seedDomainWithGates(t, st)

	// Empty category normalizes to the default hitl — the API must return the
	// resolved mode, never "".
	g, err := gs.Create(ctx, Goal{Title: "gated work", AssigneeType: "agent", AssigneeID: agent, Status: "active", DomainID: domID})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if g.Category != "hitl" {
		t.Fatalf("empty category must default to hitl, got %q", g.Category)
	}
	got, err := gs.Get(ctx, g.ID)
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	if got.Category != "hitl" {
		t.Fatalf("Get must select the category, got %q", got.Category)
	}
	list, err := gs.List(ctx)
	if err != nil {
		t.Fatalf("list goals: %v", err)
	}
	if len(list) != 1 || list[0].Category != "hitl" {
		t.Fatalf("List must select the category, got %+v", list)
	}
	// The creation activity records the category for the UI timeline.
	var createdDetail string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT detail FROM activity_log WHERE goal_id=? AND action='created'`, g.ID).Scan(&createdDetail); err != nil {
		t.Fatalf("created activity: %v", err)
	}
	var created map[string]any
	if err := json.Unmarshal([]byte(createdDetail), &created); err != nil || created["category"] != "hitl" {
		t.Fatalf("created activity detail must carry category hitl, got %q (err %v)", createdDetail, err)
	}

	// Capture the reviewing event: HITL keeps the gate behavior and tags the
	// event with the category.
	type reviewPayload struct {
		category string
		reason   string
	}
	reviewed := make(chan reviewPayload, 4)
	gs.bus.Subscribe("goal:reviewing", func(_ context.Context, e events.Event) {
		m, _ := e.Payload.(map[string]any)
		cat, _ := m["category"].(string)
		reason, _ := m["reason"].(string)
		reviewed <- reviewPayload{category: cat, reason: reason}
	})

	r := enqueueFirst(t, rs, g)
	finishWithMergeGate(t, st, rs, r, "done")

	after, err := gs.Get(ctx, g.ID)
	if err != nil {
		t.Fatalf("get after finish: %v", err)
	}
	if after.Status != "review" {
		t.Fatalf("HITL gated completion must park in review, got %q", after.Status)
	}
	if after.ReviewRequest == "" {
		t.Fatalf("expected review_request with the gate reason")
	}
	var autoCompleted int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM activity_log WHERE goal_id=? AND action='auto_completed'`, g.ID).Scan(&autoCompleted); err != nil {
		t.Fatalf("count auto_completed: %v", err)
	}
	if autoCompleted != 0 {
		t.Fatalf("HITL must never write auto_completed, got %d row(s)", autoCompleted)
	}
	select {
	case p := <-reviewed:
		if p.category != "hitl" {
			t.Fatalf("goal:reviewing must carry category hitl, got %q", p.category)
		}
		if p.reason == "" {
			t.Fatalf("goal:reviewing must carry the gate reason")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HITL gate hit must publish goal:reviewing")
	}
}

// TestGoalCategoryHOOTLAutoCompletes is the HOOTL arm: the SAME gated domain
// and the SAME triggering gate must NOT park the goal — the finalization guard
// passes, gatesForGoal is skipped, the goal promotes to done, an auto_completed
// activity row records the skip, and goal:finished carries category +
// auto_approved.
func TestGoalCategoryHOOTLAutoCompletes(t *testing.T) {
	gs, rs, _, st := newTestCluster(t)
	ctx := context.Background()
	agent := seedAgent(t, st, "A")
	domID := seedDomainWithGates(t, st)

	g, err := gs.Create(ctx, Goal{
		Title: "unattended work", Category: "hootl",
		AssigneeType: "agent", AssigneeID: agent, Status: "active", DomainID: domID,
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if g.Category != "hootl" {
		t.Fatalf("explicit category must be kept, got %q", g.Category)
	}

	type finishedPayload struct {
		status       string
		category     string
		autoApproved bool
	}
	finished := make(chan finishedPayload, 4)
	gs.bus.Subscribe("goal:finished", func(_ context.Context, e events.Event) {
		m, _ := e.Payload.(map[string]any)
		status, _ := m["status"].(string)
		cat, _ := m["category"].(string)
		auto, _ := m["auto_approved"].(bool)
		finished <- finishedPayload{status: status, category: cat, autoApproved: auto}
	})

	r := enqueueFirst(t, rs, g)
	finishWithMergeGate(t, st, rs, r, "done")

	after, err := gs.Get(ctx, g.ID)
	if err != nil {
		t.Fatalf("get after finish: %v", err)
	}
	if after.Status != "done" {
		t.Fatalf("HOOTL completion must promote to done (gates skipped), got %q", after.Status)
	}
	if after.ReviewRequest != "" {
		t.Fatalf("HOOTL completion must not write a review_request, got %q", after.ReviewRequest)
	}
	var enteredReview int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM activity_log WHERE goal_id=? AND action='entered_review'`, g.ID).Scan(&enteredReview); err != nil {
		t.Fatalf("count entered_review: %v", err)
	}
	if enteredReview != 0 {
		t.Fatalf("HOOTL must not enter review, got %d entered_review row(s)", enteredReview)
	}
	var detail string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT detail FROM activity_log WHERE goal_id=? AND action='auto_completed'`, g.ID).Scan(&detail); err != nil {
		t.Fatalf("auto_completed activity: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal([]byte(detail), &got); err != nil {
		t.Fatalf("parse auto_completed detail %q: %v", detail, err)
	}
	if got["category"] != "hootl" || got["gate_skipped"] != true {
		t.Fatalf("auto_completed detail mismatch: %q", detail)
	}
	select {
	case p := <-finished:
		if p.status != "done" || p.category != "hootl" || !p.autoApproved {
			t.Fatalf("goal:finished mismatch: %+v", p)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("HOOTL auto-completion must publish goal:finished")
	}
}

// TestGoalCategoryInvalidRejected: only hitl/hootl are legal; a client typo
// must fail validation instead of silently becoming hitl (it would change
// whether a human checkpoints the goal).
func TestGoalCategoryInvalidRejected(t *testing.T) {
	gs, _, _, st := newTestCluster(t)
	ctx := context.Background()
	agent := seedAgent(t, st, "A")
	domID := seedDomain(t, st)

	_, err := gs.Create(ctx, Goal{
		Title: "bad category", Category: "sometimes",
		AssigneeType: "agent", AssigneeID: agent, Status: "active", DomainID: domID,
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid category must be ErrValidation, got %v", err)
	}
	var n int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM goal`).Scan(&n); err != nil {
		t.Fatalf("count goals: %v", err)
	}
	if n != 0 {
		t.Fatalf("rejected goal must not be persisted, got %d row(s)", n)
	}
}
