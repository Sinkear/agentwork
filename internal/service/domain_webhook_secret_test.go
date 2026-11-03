package service

import (
	"context"
	"errors"
	"testing"

	"github.com/eushing/agentwork/internal/events"
)

// TestSetWebhookSecret covers the explicit write/clear path for the repo-level
// webhook secret: values are trimmed, an empty value clears it (falling back to
// the platform secret), and a missing domain reports ErrNotFound.
func TestSetWebhookSecret(t *testing.T) {
	st := newTestStore(t)
	svc := NewDomainService(st, events.NewBus())
	ctx := context.Background()

	d, err := svc.Create(ctx, Domain{
		Name:   "webhook-secret",
		GitURL: "https://github.com/eushing/agentwork.git",
	})
	if err != nil {
		t.Fatalf("create domain: %v", err)
	}

	set, err := svc.SetWebhookSecret(ctx, d.ID, "  repo-secret  ")
	if err != nil {
		t.Fatalf("set webhook secret: %v", err)
	}
	if set.WebhookSecret != "repo-secret" {
		t.Fatalf("expected trimmed secret %q, got %q", "repo-secret", set.WebhookSecret)
	}

	got, err := svc.Get(ctx, d.ID)
	if err != nil {
		t.Fatalf("get domain: %v", err)
	}
	if got.WebhookSecret != "repo-secret" {
		t.Fatalf("round-trip mismatch: %q", got.WebhookSecret)
	}

	cleared, err := svc.SetWebhookSecret(ctx, d.ID, "")
	if err != nil {
		t.Fatalf("clear webhook secret: %v", err)
	}
	if cleared.WebhookSecret != "" {
		t.Fatalf("expected cleared secret, got %q", cleared.WebhookSecret)
	}
	got, err = svc.Get(ctx, d.ID)
	if err != nil {
		t.Fatalf("get domain after clear: %v", err)
	}
	if got.WebhookSecret != "" {
		t.Fatalf("expected empty secret after clear, got %q", got.WebhookSecret)
	}

	if _, err := svc.SetWebhookSecret(ctx, "does-not-exist", "x"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("expected ErrNotFound for missing domain, got %v", err)
	}
}
