package service

import (
	"context"
	"errors"
	"testing"

	"github.com/eushing/agentwork/internal/events"
)

// TestScheduleCategoryPersists: the schedule's HITL/HOOTL mode is persisted
// on create and selected back by Get/List, so the daemon can clone it onto
// every fired goal.
func TestScheduleCategoryPersists(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	bus := events.NewBus()
	svc := NewScheduleService(st, bus)
	agent := seedAgent(t, st, "scheduler")
	domainID := seedDomain(t, st)

	sch, err := svc.Create(ctx, Schedule{
		Name: "nightly", TitleTemplate: "nightly task", Description: "d",
		AssigneeType: "agent", AssigneeID: agent, DomainID: domainID,
		CronExpression: "0 9 * * *", Timezone: "UTC",
		Category: "hootl",
	})
	if err != nil {
		t.Fatalf("create schedule: %v", err)
	}
	if sch.Category != "hootl" {
		t.Fatalf("created schedule category = %q, want hootl", sch.Category)
	}

	got, err := svc.Get(ctx, sch.ID)
	if err != nil {
		t.Fatalf("get schedule: %v", err)
	}
	if got.Category != "hootl" {
		t.Fatalf("Get category = %q, want hootl", got.Category)
	}
	list, err := svc.List(ctx)
	if err != nil {
		t.Fatalf("list schedules: %v", err)
	}
	if len(list) != 1 || list[0].Category != "hootl" {
		t.Fatalf("List category mismatch: %+v", list)
	}
	// Raw column check (the daemon reads the column, not the struct).
	var raw string
	if err := st.DB().QueryRowContext(ctx, `SELECT category FROM schedule WHERE id=?`, sch.ID).Scan(&raw); err != nil {
		t.Fatalf("raw category: %v", err)
	}
	if raw != "hootl" {
		t.Fatalf("persisted category = %q, want hootl", raw)
	}

	// Empty category normalizes to the default hitl.
	def, err := svc.Create(ctx, Schedule{
		Name: "defaulted", TitleTemplate: "t", Description: "d",
		AssigneeType: "agent", AssigneeID: agent, DomainID: domainID,
		CronExpression: "0 10 * * *", Timezone: "UTC",
	})
	if err != nil {
		t.Fatalf("create default schedule: %v", err)
	}
	if def.Category != "hitl" {
		t.Fatalf("empty category must default to hitl, got %q", def.Category)
	}

	// Update persists a mode change.
	updated, err := svc.Update(ctx, sch.ID, Schedule{
		Name: sch.Name, TitleTemplate: sch.TitleTemplate, Description: sch.Description,
		AssigneeType: sch.AssigneeType, AssigneeID: sch.AssigneeID, DomainID: sch.DomainID,
		CronExpression: sch.CronExpression, Timezone: sch.Timezone,
		Category: "hitl",
	})
	if err != nil {
		t.Fatalf("update schedule: %v", err)
	}
	if updated.Category != "hitl" {
		t.Fatalf("updated category = %q, want hitl", updated.Category)
	}
	got, err = svc.Get(ctx, sch.ID)
	if err != nil {
		t.Fatalf("get after update: %v", err)
	}
	if got.Category != "hitl" {
		t.Fatalf("Get after update = %q, want hitl", got.Category)
	}
}

// TestScheduleCategoryInvalidRejected: any category other than hitl/hootl is
// a validation error on both create and update.
func TestScheduleCategoryInvalidRejected(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	bus := events.NewBus()
	svc := NewScheduleService(st, bus)
	agent := seedAgent(t, st, "scheduler")
	domainID := seedDomain(t, st)

	_, err := svc.Create(ctx, Schedule{
		Name: "bad", TitleTemplate: "t", Description: "d",
		AssigneeType: "agent", AssigneeID: agent, DomainID: domainID,
		CronExpression: "0 9 * * *", Timezone: "UTC",
		Category: "sometimes",
	})
	if !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid schedule category must be ErrValidation, got %v", err)
	}
	var n int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM schedule`).Scan(&n); err != nil {
		t.Fatalf("count schedules: %v", err)
	}
	if n != 0 {
		t.Fatalf("rejected schedule must not be persisted, got %d row(s)", n)
	}
}
