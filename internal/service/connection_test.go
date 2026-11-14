package service

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/eushing/agentwork/internal/events"
)

// connectionFixture seeds a repo domain + a squad leader and returns a
// ConnectionService wired to the same store.
func connectionFixture(t *testing.T) (*ConnectionService, *DomainService, string, string) {
	t.Helper()
	st := newTestStore(t)
	bus := events.NewBus()
	ctx := context.Background()
	agentID := seedAgent(t, st, "conn-leader")
	ds := NewDomainService(st, bus)
	d, err := ds.Create(ctx, Domain{
		Name:           "conn-repo",
		GitURL:         "https://github.com/acme/widgets.git",
		GitCredentials: "tok",
	})
	if err != nil {
		t.Fatalf("seed connection domain: %v", err)
	}
	sq, err := NewSquadService(st, bus).Create(ctx, Squad{Name: "conn-squad", LeaderID: agentID})
	if err != nil {
		t.Fatalf("seed connection squad: %v", err)
	}
	return NewConnectionService(st, bus), ds, d.ID, sq.ID
}

func TestConnectionCreateSecretAndLegacyReset(t *testing.T) {
	ctx := context.Background()
	svc, ds, domainID, squadID := connectionFixture(t)

	c1, err := svc.Create(ctx, CreateConnectionInput{SquadID: squadID, DomainID: domainID})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if c1.Secret == "" || !c1.SecretSet {
		t.Fatalf("secret must be generated: %q set=%v", c1.Secret, c1.SecretSet)
	}
	if len(c1.Secret) != 64 {
		t.Fatalf("secret must be 32 random bytes hex, got %d chars", len(c1.Secret))
	}
	if _, err := hex.DecodeString(c1.Secret); err != nil {
		t.Fatalf("secret must be hex: %v", err)
	}
	if c1.Provider != "github" || c1.Repo != "acme/widgets" {
		t.Fatalf("connection must inherit domain provider/repo, got %q/%q", c1.Provider, c1.Repo)
	}
	if c1.WebhookPath != "/webhooks/github/"+c1.ID {
		t.Fatalf("webhook path = %q", c1.WebhookPath)
	}
	if len(c1.Events) != 2 || c1.Events[0] != "issues" || c1.Events[1] != "pull_request" {
		t.Fatalf("default events = %v", c1.Events)
	}

	// Secret is readable server-side...
	got, err := svc.Get(ctx, c1.ID)
	if err != nil {
		t.Fatalf("get connection: %v", err)
	}
	if got.Secret != c1.Secret {
		t.Fatalf("stored secret differs")
	}
	// ...but never serialized by HTTP (json:"-").
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal connection: %v", err)
	}
	if strings.Contains(string(raw), `"secret":`) {
		t.Fatalf("connection JSON must not expose the secret: %s", raw)
	}

	// A second SQUAD's connection on the SAME domain must get a different path
	// (the original bug: one URL per provider). Same repo is allowed for
	// different squads; the same squad twice is rejected below.
	sq2 := secondConnectionSquad(t, svc, squadID)
	c2, err := svc.Create(ctx, CreateConnectionInput{SquadID: sq2, DomainID: domainID})
	if err != nil {
		t.Fatalf("create second connection: %v", err)
	}
	if c1.WebhookPath == c2.WebhookPath {
		t.Fatalf("two connections on one domain share webhook path %q", c1.WebhookPath)
	}
	if c1.Secret == c2.Secret {
		t.Fatal("connections must not share a secret")
	}

	// Creating a connection clears the domain's legacy direct-binding
	// assignees (otherwise both routing paths would fire).
	var issueAssignee, pullAssignee string
	if err := ds.st.DB().QueryRowContext(ctx,
		`SELECT issue_assignee, pull_assignee FROM domain WHERE id=?`, domainID).
		Scan(&issueAssignee, &pullAssignee); err != nil {
		t.Fatalf("read domain assignees: %v", err)
	}
	if issueAssignee != "" || pullAssignee != "" {
		t.Fatalf("legacy assignees must be cleared, got issue=%q pull=%q", issueAssignee, pullAssignee)
	}

	all, err := svc.List(ctx)
	if err != nil || len(all) != 2 {
		t.Fatalf("list connections = %d, %v", len(all), err)
	}
	bySquad, err := svc.ListBySquad(ctx, squadID)
	if err != nil || len(bySquad) != 1 {
		t.Fatalf("list by squad = %d, %v", len(bySquad), err)
	}
	// One squad may not hold two connections for the same repo: each would
	// poll it and create a duplicate goal for the same issue.
	if _, err := svc.Create(ctx, CreateConnectionInput{SquadID: squadID, DomainID: domainID}); err == nil {
		t.Fatal("duplicate connection for the same squad+domain must be rejected")
	} else if ve, ok := err.(*validationError); !ok || !strings.Contains(ve.Error(), "已接入") {
		t.Fatalf("duplicate rejection = %v, want validation error", err)
	}
}

func TestConnectionEventsValidationAndUpdate(t *testing.T) {
	ctx := context.Background()
	svc, _, domainID, squadID := connectionFixture(t)

	c, err := svc.Create(ctx, CreateConnectionInput{
		SquadID: squadID, DomainID: domainID,
		Events: []string{"pull_request", "issues", "pull_request"},
	})
	if err != nil {
		t.Fatalf("create with events: %v", err)
	}
	want := []string{"pull_request", "issues"}
	if len(c.Events) != 2 || c.Events[0] != want[0] || c.Events[1] != want[1] {
		t.Fatalf("events must be deduped in order, got %v", c.Events)
	}

	// All-invalid events is a validation error (not a silent default).
	if _, err := svc.Create(ctx, CreateConnectionInput{
		SquadID: squadID, DomainID: domainID, Events: []string{"push"},
	}); !errors.Is(err, ErrValidation) {
		t.Fatalf("invalid events must be a validation error, got %v", err)
	}
	// Mixed input keeps only the supported entries.
	mixed, err := svc.Create(ctx, CreateConnectionInput{
		SquadID: secondConnectionSquad(t, svc, squadID), DomainID: domainID, Events: []string{"push", "issues"},
	})
	if err != nil || len(mixed.Events) != 1 || mixed.Events[0] != "issues" {
		t.Fatalf("mixed events = %v, %v", mixed.Events, err)
	}

	// PATCH clears back to the default subscription.
	cleared, err := svc.Update(ctx, c.ID, UpdateConnectionInput{ClearEvents: true})
	if err != nil || len(cleared.Events) != 2 {
		t.Fatalf("clear events = %v, %v", cleared.Events, err)
	}
	// PATCH sets an explicit subset.
	patched, err := svc.Update(ctx, c.ID, UpdateConnectionInput{Events: []string{"issues"}})
	if err != nil || len(patched.Events) != 1 || patched.Events[0] != "issues" {
		t.Fatalf("patch events = %v, %v", patched.Events, err)
	}
	// Nil events + empty secret leaves both untouched.
	kept, err := svc.Update(ctx, c.ID, UpdateConnectionInput{})
	if err != nil || len(kept.Events) != 1 || kept.Secret != c.Secret {
		t.Fatalf("partial update changed values: %v", kept)
	}
	// A new secret replaces the old one.
	rotated, err := svc.Update(ctx, c.ID, UpdateConnectionInput{Secret: "rotated-secret"})
	if err != nil || rotated.Secret != "rotated-secret" || !rotated.SecretSet {
		t.Fatalf("secret rotation: %v", err)
	}
	if _, err := svc.Update(ctx, c.ID, UpdateConnectionInput{Secret: strings.Repeat("x", 257)}); !errors.Is(err, ErrValidation) {
		t.Fatalf("oversized secret must be rejected, got %v", err)
	}
}

func TestConnectionCreateRequiresTrackedRepo(t *testing.T) {
	ctx := context.Background()
	st := newTestStore(t)
	bus := events.NewBus()
	agentID := seedAgent(t, st, "conn-leader")
	ds := NewDomainService(st, bus)
	// example.com is neither github nor gitcode and has no owner/repo pair.
	d, err := ds.Create(ctx, Domain{Name: "no-repo", GitURL: "https://example.com/x.git"})
	if err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	sq, err := NewSquadService(st, bus).Create(ctx, Squad{Name: "no-repo-squad", LeaderID: agentID})
	if err != nil {
		t.Fatalf("seed squad: %v", err)
	}
	_, err = NewConnectionService(st, bus).Create(ctx, CreateConnectionInput{SquadID: sq.ID, DomainID: d.ID})
	if !errors.Is(err, ErrValidation) || !strings.Contains(err.Error(), "没有配置跟踪仓库（issue_repo）") {
		t.Fatalf("untracked repo must be a validation error, got %v", err)
	}
}

func TestConnectionDeleteAndDeliveryScope(t *testing.T) {
	ctx := context.Background()
	svc, _, domainID, squadID := connectionFixture(t)
	c, err := svc.Create(ctx, CreateConnectionInput{SquadID: squadID, DomainID: domainID})
	if err != nil {
		t.Fatalf("create connection: %v", err)
	}

	// Deliveries are scoped by connection_id (legacy rows must not leak in).
	now := now()
	for _, row := range []struct {
		id, connID, event string
	}{
		{"d1", c.ID, "issues"},
		{"d2", "", "issues"},
	} {
		if _, err := svc.st.DB().ExecContext(ctx,
			`INSERT INTO webhook_delivery (id,provider,repo,domain_id,connection_id,event,action,ok,error,received_at)
			 VALUES (?,?,?,?,?,?,?,?,?,?)`,
			row.id, "github", "acme/widgets", domainID, row.connID, row.event, "opened", 1, "", now); err != nil {
			t.Fatalf("insert delivery: %v", err)
		}
	}
	rows, err := svc.ListDeliveries(ctx, c.ID, 20)
	if err != nil || len(rows) != 1 || rows[0].ConnectionID != c.ID {
		t.Fatalf("scoped deliveries = %+v, %v", rows, err)
	}
	empty, err := svc.ListDeliveries(ctx, "no-such-connection", 20)
	if err != nil || empty == nil || len(empty) != 0 {
		t.Fatalf("empty deliveries must be non-nil, got %#v, %v", empty, err)
	}

	if err := svc.Delete(ctx, c.ID); err != nil {
		t.Fatalf("delete connection: %v", err)
	}
	if _, err := svc.Get(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleted connection must be not found, got %v", err)
	}
	if err := svc.Delete(ctx, c.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("deleting twice must be not found, got %v", err)
	}
}

// secondConnectionSquad seeds another squad (same leader) so tests can create
// a second connection for the same repo without tripping the one-connection-
// per-squad guard.
func secondConnectionSquad(t *testing.T, svc *ConnectionService, squadID string) string {
	t.Helper()
	ctx := context.Background()
	var leaderID string
	if err := svc.st.DB().QueryRowContext(ctx,
		`SELECT leader_id FROM squad WHERE id=?`, squadID).Scan(&leaderID); err != nil {
		t.Fatalf("read squad leader: %v", err)
	}
	sq, err := NewSquadService(svc.st, events.NewBus()).Create(ctx,
		Squad{Name: "conn-squad-" + newID()[:6], LeaderID: leaderID})
	if err != nil {
		t.Fatalf("seed second squad: %v", err)
	}
	return sq.ID
}

// TestDomainSetAssigneesRejectedWhileConnectionOwnsRepo locks the routing
// hand-off: once a connection exists, a non-empty legacy domain assignee must
// be rejected instead of stored (the poller skips the domain and legacy
// webhook dispatch no-ops, so it would never fire).
func TestDomainSetAssigneesRejectedWhileConnectionOwnsRepo(t *testing.T) {
	ctx := context.Background()
	svc, ds, domainID, squadID := connectionFixture(t)
	if _, err := svc.Create(ctx, CreateConnectionInput{SquadID: squadID, DomainID: domainID}); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	_, err := ds.SetAssignees(ctx, domainID, squadID, "squad", "", "")
	var ve *validationError
	if !errors.As(err, &ve) {
		t.Fatalf("non-empty legacy assignee must be rejected while a connection exists, got %v", err)
	}
	// Clearing both assignees stays allowed.
	if _, err := ds.SetAssignees(ctx, domainID, "", "agent", "", "agent"); err != nil {
		t.Fatalf("clearing legacy assignees must be allowed: %v", err)
	}
}
