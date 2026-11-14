package service

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/eushing/agentwork/internal/events"
)

// ageOwnerRun marks the goal's queued owner run terminal and rewrites its
// queued/finished timestamps so the idle fallback (task 8.1) sees it as old.
func ageOwnerRun(t *testing.T, gs *GoalService, goalID string, queued, finished time.Time) {
	t.Helper()
	if _, err := gs.st.DB().ExecContext(context.Background(),
		`UPDATE run SET status='completed', queued_at=?, finished_at=? WHERE goal_id=? AND role='owner' AND status='queued'`,
		queued.UTC().Format(time.RFC3339Nano), finished.UTC().Format(time.RFC3339Nano), goalID); err != nil {
		t.Fatalf("age owner run: %v", err)
	}
}

// TestIdleWrapupWakesOwnerOnce: an ACTIVE goal with nothing in flight and a
// last run finished >10 minutes ago is woken ONCE by the idle fallback, with
// the close-out wake note + the synthetic anchor. A completed fallback wake
// run must NOT re-arm the fallback (the signal excludes it).
func TestIdleWrapupWakesOwnerOnce(t *testing.T) {
	gs, _, _, st := newTestCluster(t)
	ctx := context.Background()
	dom := seedDomain(t, st)
	agent := seedAgent(t, st, "idle-owner")
	g, err := gs.Create(ctx, Goal{
		Title: "idle goal", AssigneeType: "agent", AssigneeID: agent,
		Status: "active", DomainID: dom,
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}

	now := time.Now().UTC()
	ageOwnerRun(t, gs, g.ID, now.Add(-30*time.Minute), now.Add(-20*time.Minute))

	if err := gs.ReconcileGoal(ctx, g.ID); err != nil {
		t.Fatalf("reconcile idle goal: %v", err)
	}
	got, err := gs.Get(ctx, g.ID)
	if err != nil {
		t.Fatalf("get goal: %v", err)
	}
	if !strings.Contains(got.Attention, "wrapup") {
		t.Fatalf("idle goal must arm wrapup, attention=%q", got.Attention)
	}
	var note, anchor string
	var queued int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM run WHERE goal_id=? AND role='owner' AND status='queued'`, g.ID).Scan(&queued); err != nil {
		t.Fatal(err)
	}
	if queued != 1 {
		t.Fatalf("idle fallback must spawn exactly one owner run, got %d queued", queued)
	}
	if err := st.DB().QueryRowContext(ctx,
		`SELECT wake_note, wake_anchor FROM run WHERE goal_id=? AND role='owner' AND status='queued'`, g.ID).
		Scan(&note, &anchor); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(note, "No work remains in flight") || !strings.Contains(note, "submit the goal for review") {
		t.Fatalf("idle wake note must name the fallback + close-out path, got:\n%s", note)
	}
	if anchor != wrapupIdleAnchor {
		t.Fatalf("idle wake anchor = %q, want %q", anchor, wrapupIdleAnchor)
	}

	// The owner wakes, reviews, and ends its turn WITHOUT submitting. Time
	// passes (>10 min). The fallback must not wake it again: the completed
	// wake run is excluded from the idle signal, so no newer signal exists.
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE run SET status='completed', queued_at=?, finished_at=? WHERE goal_id=? AND role='owner' AND status='queued'`,
		now.Add(-20*time.Minute).Format(time.RFC3339Nano), now.Add(-15*time.Minute).Format(time.RFC3339Nano), g.ID); err != nil {
		t.Fatalf("complete wake run: %v", err)
	}
	if err := gs.ReconcileGoal(ctx, g.ID); err != nil {
		t.Fatalf("reconcile after wake: %v", err)
	}
	got, err = gs.Get(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Status != "active" {
		t.Fatalf("goal must stay active without submit, got %q", got.Status)
	}
	if got.Attention != "" {
		t.Fatalf("completed idle wake must not re-arm attention, got %q", got.Attention)
	}
	var total int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM run WHERE goal_id=? AND role='owner'`, g.ID).Scan(&total); err != nil {
		t.Fatal(err)
	}
	if total != 2 {
		t.Fatalf("idle fallback must wake the owner exactly once, got %d owner runs", total)
	}
}

// TestIdleWrapupZeroRunsUntouched: a goal with NO runs at all (e.g. a
// strict-mode/scratch goal created outside the run pipeline) must not arm
// the fallback or spawn anything.
func TestIdleWrapupZeroRunsUntouched(t *testing.T) {
	st := newTestStore(t)
	gs := NewGoalService(st, events.NewBus())
	ctx := context.Background()
	dom := seedDomain(t, st)
	agent := seedAgent(t, st, "no-run")
	g, err := gs.Create(ctx, Goal{
		Title: "no runs", AssigneeType: "agent", AssigneeID: agent,
		Status: "active", DomainID: dom,
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	if err := gs.ReconcileGoal(ctx, g.ID); err != nil {
		t.Fatalf("reconcile zero-run goal: %v", err)
	}
	got, err := gs.Get(ctx, g.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Attention != "" {
		t.Fatalf("zero-run goal must stay untouched, attention=%q", got.Attention)
	}
	var runs int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM run WHERE goal_id=?`, g.ID).Scan(&runs); err != nil {
		t.Fatal(err)
	}
	if runs != 0 {
		t.Fatalf("zero-run goal must not gain a run, got %d", runs)
	}
}

// TestSubmitForReview: the explicit active → review transition writes the
// status, review_request, activity row, system comment and publishes
// goal:reviewing with auto_approve=false.
func TestSubmitForReview(t *testing.T) {
	gs, _, _, st := newTestCluster(t)
	ctx := context.Background()
	dom := seedDomain(t, st)
	agent := seedAgent(t, st, "submit-owner")
	g, err := gs.Create(ctx, Goal{
		Title: "submit me", AssigneeType: "agent", AssigneeID: agent,
		Status: "active", DomainID: dom,
	})
	if err != nil {
		t.Fatalf("create goal: %v", err)
	}
	// Create enqueued the first owner run; finishing it makes the goal
	// submittable (no live run).
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE run SET status='completed', finished_at=? WHERE goal_id=?`, now(), g.ID); err != nil {
		t.Fatalf("finish first run: %v", err)
	}

	got := make(chan events.Event, 4)
	gs.bus.Subscribe("goal:reviewing", func(_ context.Context, e events.Event) { got <- e })

	reason := "deliverable lives in the feed"
	out, err := gs.SubmitForReview(ctx, g.ID, reason)
	if err != nil {
		t.Fatalf("submit for review: %v", err)
	}
	if out.Status != "review" || out.ReviewRequest != reason {
		t.Fatalf("submitted goal status=%q review_request=%q", out.Status, out.ReviewRequest)
	}
	var detail string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT detail FROM activity_log WHERE goal_id=? AND action='submitted_review'`, g.ID).Scan(&detail); err != nil {
		t.Fatalf("submitted_review activity missing: %v", err)
	}
	var d map[string]string
	if err := json.Unmarshal([]byte(detail), &d); err != nil || d["reason"] != reason {
		t.Fatalf("activity detail = %q (err %v), want reason %q", detail, err, reason)
	}
	var comment string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT content FROM comment WHERE goal_id=? AND author_type='system' AND content LIKE 'Goal submitted for review:%'`, g.ID).
		Scan(&comment); err != nil {
		t.Fatalf("system comment missing: %v", err)
	}
	if !strings.Contains(comment, reason) {
		t.Fatalf("system comment = %q, want reason %q", comment, reason)
	}
	select {
	case e := <-got:
		payload, ok := e.Payload.(map[string]any)
		if !ok {
			t.Fatalf("goal:reviewing payload type %T", e.Payload)
		}
		if payload["goal_id"] != g.ID || payload["auto_approve"] != false || payload["reason"] != reason {
			t.Fatalf("goal:reviewing payload = %#v", payload)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("goal:reviewing event not published")
	}

	// A goal already in review refuses the second submit.
	if _, err := gs.SubmitForReview(ctx, g.ID, "again"); !errors.Is(err, ErrValidation) ||
		!strings.Contains(err.Error(), "only an active goal") {
		t.Fatalf("second submit error = %v, want active-only ValidationError", err)
	}
}

// TestSubmitForReviewRefusals: live runs and non-active goals are
// ValidationErrors; unknown goals are ErrNotFound; an empty reason defaults
// to "submitted for review".
func TestSubmitForReviewRefusals(t *testing.T) {
	gs, _, _, st := newTestCluster(t)
	ctx := context.Background()
	dom := seedDomain(t, st)
	agent := seedAgent(t, st, "refuse-owner")

	// Live run (the Create-time queued owner run) blocks the submit.
	live, err := gs.Create(ctx, Goal{
		Title: "live", AssigneeType: "agent", AssigneeID: agent,
		Status: "active", DomainID: dom,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := gs.SubmitForReview(ctx, live.ID, "too early"); !errors.Is(err, ErrValidation) ||
		!strings.Contains(err.Error(), "stop queued/running runs") {
		t.Fatalf("live-run submit error = %v, want stop-runs ValidationError", err)
	}

	// Default reason.
	idle, err := gs.Create(ctx, Goal{
		Title: "idle", AssigneeType: "agent", AssigneeID: agent,
		Status: "active", DomainID: dom,
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := st.DB().ExecContext(ctx,
		`UPDATE run SET status='completed', finished_at=? WHERE goal_id=?`, now(), idle.ID); err != nil {
		t.Fatal(err)
	}
	out, err := gs.SubmitForReview(ctx, idle.ID, "")
	if err != nil {
		t.Fatalf("default-reason submit: %v", err)
	}
	if out.ReviewRequest != "submitted for review" {
		t.Fatalf("default review_request = %q", out.ReviewRequest)
	}

	if _, err := gs.SubmitForReview(ctx, "missing-goal", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing goal error = %v, want ErrNotFound", err)
	}
}
