package issue

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/service"
	"github.com/eushing/agentwork/internal/store"
)

// connStubProvider is a Provider whose issue/PR lists come from the test.
type connStubProvider struct {
	issues, pulls         []Issue
	pullsList             []Pull
	issueCalls, pullCalls int
}

func (s *connStubProvider) ListOpenIssues(context.Context, string) ([]Issue, error) {
	s.issueCalls++
	return s.issues, nil
}
func (s *connStubProvider) ListComments(context.Context, string, int) ([]Comment, error) {
	return nil, nil
}
func (s *connStubProvider) CreateComment(context.Context, string, int, string) error { return nil }
func (s *connStubProvider) CloseIssue(context.Context, string, int) error            { return nil }
func (s *connStubProvider) CommitURL(string, string) string                          { return "" }
func (s *connStubProvider) ListOpenPulls(context.Context, string) ([]Pull, error) {
	s.pullCalls++
	return s.pullsList, nil
}
func (s *connStubProvider) ListPullComments(context.Context, string, int) ([]Comment, error) {
	return nil, nil
}
func (s *connStubProvider) CreatePullComment(context.Context, string, int, string) error {
	return nil
}

// connectionHarness builds a store with one repo domain x/y (optionally with a
// LEGACY direct assignee), a squad sq1 and a matching ConnectionRoute. The
// legacy webhook secret is domain-secret; the connection secret is
// conn-secret.
func connectionHarness(t *testing.T, legacyAssignee string) (context.Context, *store.Store, *WebhookHandler, *Poller, *service.GoalService, ConnectionRoute) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = st.Close() })
	ctx := context.Background()
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	for _, sql := range []string{
		`INSERT INTO runtime (id,name,created_at) VALUES ('rt1','rt1','` + ts + `')`,
		`INSERT INTO agent (id,name,runtime_id,max_concurrent,created_at) VALUES ('a1','worker','rt1',1,'` + ts + `')`,
		`INSERT INTO squad (id,name,leader_id,created_at) VALUES ('sq1','sq1','a1','` + ts + `')`,
	} {
		if _, err := st.DB().ExecContext(ctx, sql); err != nil {
			t.Fatalf("seed harness: %v", err)
		}
	}
	bus := events.NewBus()
	domain := service.Domain{
		Name: "d-conn", GitURL: "https://e.com/d.git",
		IssueRepo: "x/y", IssueProvider: "github", GitCredentials: "tok",
		WebhookSecret: "domain-secret",
	}
	if legacyAssignee != "" {
		domain.IssueAssignee = legacyAssignee
		domain.IssueAssigneeType = "agent"
	}
	ds := service.NewDomainService(st, bus)
	d, err := ds.Create(ctx, domain)
	if err != nil {
		t.Fatalf("seed domain: %v", err)
	}
	goalSvc := service.NewGoalService(st, bus)
	runSvc := service.NewRunService(st, bus)
	goalSvc.SetRunService(runSvc)
	runSvc.SetGoalService(goalSvc)
	p := NewPoller(st, goalSvc, runSvc)
	secretFor := func(context.Context, string) (string, error) { return "domain-secret", nil }
	h := NewWebhookHandler("github", st, p, secretFor)
	route := ConnectionRoute{
		ConnectionID: "conn1",
		DomainID:     d.ID,
		AssigneeID:   "sq1",
		AssigneeType: "squad",
		Repo:         "x/y",
		Secret:       "conn-secret",
		Events:       []string{"issues", "pull_request"},
	}
	return ctx, st, h, p, goalSvc, route
}

func sign(secret string, payload []byte) string {
	return "sha256=" + hmacSHA256Hex(secret, payload)
}

func TestHandleConnectionSecretAndRepoChecks(t *testing.T) {
	ctx, st, h, _, _, route := connectionHarness(t, "")
	payload := []byte(`{"action":"opened","issue":{"number":9,"title":"conn","body":"b"},"repository":{"full_name":"x/y"}}`)

	if err := h.HandleConnection(ctx, payload, sign("wrong-secret", payload), "", route); err == nil {
		t.Fatal("wrong secret must be rejected")
	}
	// The legacy domain secret must NOT open a connection endpoint.
	if err := h.HandleConnection(ctx, payload, sign("domain-secret", payload), "", route); err == nil {
		t.Fatal("legacy domain secret must not verify on the connection path")
	}
	var bad int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM webhook_delivery WHERE connection_id='conn1' AND ok=0 AND error='bad signature'`).
		Scan(&bad); err != nil {
		t.Fatal(err)
	}
	if bad != 2 {
		t.Fatalf("bad signature deliveries = %d, want 2", bad)
	}

	// Valid secret but a different repo → rejected, never routed to sq1.
	other := []byte(`{"action":"opened","issue":{"number":3,"title":"other","body":"b"},"repository":{"full_name":"x/z"}}`)
	err := h.HandleConnection(ctx, other, sign("conn-secret", other), "", route)
	if err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("repo mismatch must be rejected, got %v", err)
	}
	var goals int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM goal WHERE source_ref LIKE 'github:x/%'`).Scan(&goals); err != nil {
		t.Fatal(err)
	}
	if goals != 0 {
		t.Fatalf("mismatched delivery created %d goals", goals)
	}

	// A connection with no secret is an explicit failure.
	noSecret := route
	noSecret.Secret = ""
	if err := h.HandleConnection(ctx, payload, sign("", payload), "", noSecret); err == nil {
		t.Fatal("empty connection secret must be rejected")
	}
}

func TestHandleConnectionRoutesIssueToSquad(t *testing.T) {
	ctx, st, h, _, _, route := connectionHarness(t, "")
	payload := []byte(`{"action":"opened","issue":{"number":7,"title":"do it","body":"b"},"repository":{"full_name":"x/y"}}`)
	if err := h.HandleConnection(ctx, payload, sign("conn-secret", payload), "", route); err != nil {
		t.Fatalf("valid connection delivery: %v", err)
	}
	var assigneeType, assigneeID, domainID string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT assignee_type, assignee_id, domain_id FROM goal WHERE source_ref='github:x/y#7'`).
		Scan(&assigneeType, &assigneeID, &domainID); err != nil {
		t.Fatalf("goal not created: %v", err)
	}
	if assigneeType != "squad" || assigneeID != "sq1" || domainID != route.DomainID {
		t.Fatalf("goal routed to %s/%s domain=%s", assigneeType, assigneeID, domainID)
	}
	var event, connID, domID string
	var ok int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT event, ok, connection_id, domain_id FROM webhook_delivery WHERE connection_id='conn1'`).
		Scan(&event, &ok, &connID, &domID); err != nil {
		t.Fatal(err)
	}
	if event != "issues" || ok != 1 || connID != "conn1" || domID != route.DomainID {
		t.Fatalf("delivery = event %q ok %d conn %q domain %q", event, ok, connID, domID)
	}
}

func TestHandleConnectionEventsFilter(t *testing.T) {
	ctx, st, h, _, _, route := connectionHarness(t, "")
	route.Events = []string{"issues"} // PRs not subscribed

	pr := []byte(`{"action":"opened","pull_request":{"number":5,"title":"pr","body":"b"},"repository":{"full_name":"x/y"}}`)
	if err := h.HandleConnection(ctx, pr, sign("conn-secret", pr), "", route); err != nil {
		t.Fatalf("unsubscribed event must be a no-op: %v", err)
	}
	var goals int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM goal WHERE source_ref='github:x/y!5'`).Scan(&goals); err != nil {
		t.Fatal(err)
	}
	if goals != 0 {
		t.Fatalf("unsubscribed PR event created %d goals", goals)
	}
	var event string
	var ok int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT event, ok FROM webhook_delivery WHERE connection_id='conn1' ORDER BY received_at DESC LIMIT 1`).
		Scan(&event, &ok); err != nil {
		t.Fatal(err)
	}
	if event != "pull_request" || ok != 1 {
		t.Fatalf("filtered delivery = event %q ok %d", event, ok)
	}

	// The subscribed issue event still routes.
	iss := []byte(`{"action":"opened","issue":{"number":8,"title":"issue","body":"b"},"repository":{"full_name":"x/y"}}`)
	if err := h.HandleConnection(ctx, iss, sign("conn-secret", iss), "", route); err != nil {
		t.Fatalf("subscribed issue event: %v", err)
	}
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM goal WHERE source_ref='github:x/y#8'`).Scan(&goals); err != nil {
		t.Fatal(err)
	}
	if goals != 1 {
		t.Fatalf("subscribed issue created %d goals", goals)
	}
}

func TestLegacyDispatchNoopWhenConnectionOwnsRepo(t *testing.T) {
	ctx, st, h, _, _, route := connectionHarness(t, "a1")
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO connection (id,squad_id,domain_id,provider,repo,events,secret,created_at)
		 VALUES ('conn1','sq1',?,'github','x/y','issues,pull_request','conn-secret',?)`, route.DomainID, ts); err != nil {
		t.Fatal(err)
	}
	payload := []byte(`{"action":"opened","issue":{"number":11,"title":"legacy","body":"b"},"repository":{"full_name":"x/y"}}`)
	if err := h.Handle(ctx, payload, sign("domain-secret", payload), ""); err != nil {
		t.Fatalf("legacy delivery must still be accepted: %v", err)
	}
	var goals int
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM goal WHERE source_ref='github:x/y#11'`).Scan(&goals); err != nil {
		t.Fatal(err)
	}
	if goals != 0 {
		t.Fatalf("legacy dispatch must be a no-op once a connection owns the repo, created %d goals", goals)
	}
	// It is still recorded (with an empty connection_id — legacy path).
	var connID string
	var ok int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(connection_id,''), ok FROM webhook_delivery WHERE repo='x/y' ORDER BY received_at DESC LIMIT 1`).
		Scan(&connID, &ok); err != nil {
		t.Fatal(err)
	}
	if connID != "" || ok != 1 {
		t.Fatalf("legacy delivery row = conn %q ok %d", connID, ok)
	}

	// Without the connection the legacy path works again — proving the guard
	// (not some other fixture) suppressed it.
	if _, err := st.DB().ExecContext(ctx, `DELETE FROM connection WHERE id='conn1'`); err != nil {
		t.Fatal(err)
	}
	if err := h.Handle(ctx, payload, sign("domain-secret", payload), ""); err != nil {
		t.Fatalf("legacy delivery after connection removal: %v", err)
	}
	if err := st.DB().QueryRowContext(ctx, `SELECT COUNT(*) FROM goal WHERE source_ref='github:x/y#11'`).Scan(&goals); err != nil {
		t.Fatal(err)
	}
	if goals != 1 {
		t.Fatalf("legacy dispatch must create the goal after the connection is gone, got %d", goals)
	}
}

func TestPollerPollsConnectionNotLegacyDomain(t *testing.T) {
	ctx, st, _, p, _, route := connectionHarness(t, "a1")
	ts := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := st.DB().ExecContext(ctx,
		`INSERT INTO connection (id,squad_id,domain_id,provider,repo,events,secret,created_at)
		 VALUES ('conn1','sq1',?,'github','x/y','issues','conn-secret',?)`, route.DomainID, ts); err != nil {
		t.Fatal(err)
	}
	stub := &connStubProvider{
		issues:    []Issue{{Number: 21, Title: "polled", Body: "b"}},
		pullsList: []Pull{{Number: 31, Title: "pr"}},
	}
	p.newProvider = func(string, string) (Provider, error) { return stub, nil }

	created, err := p.Poll(ctx)
	if err != nil {
		t.Fatalf("poll: %v", err)
	}
	if created != 1 {
		t.Fatalf("poll created %d goals, want 1 (connection issues only)", created)
	}
	if stub.issueCalls != 1 {
		t.Fatalf("issue list calls = %d, want 1 (domain must be skipped)", stub.issueCalls)
	}
	if stub.pullCalls != 0 {
		t.Fatalf("pull list calls = %d, want 0 (connection subscribes issues only)", stub.pullCalls)
	}
	var assigneeType, assigneeID string
	if err := st.DB().QueryRowContext(ctx,
		`SELECT assignee_type, assignee_id FROM goal WHERE source_ref='github:x/y#21'`).
		Scan(&assigneeType, &assigneeID); err != nil {
		t.Fatalf("connection goal not created: %v", err)
	}
	if assigneeType != "squad" || assigneeID != "sq1" {
		t.Fatalf("connection goal assignee = %s/%s", assigneeType, assigneeID)
	}
	// Second poll: idempotent via the unique source_ref.
	created, err = p.Poll(ctx)
	if err != nil || created != 0 {
		t.Fatalf("second poll = %d, %v", created, err)
	}
}

// TestHandleConnectionSameRepoIndependentGoals: two squads' connections watch
// the SAME repo. Each delivery is authenticated by its own connection and must
// produce ITS OWN goal — the per-connection dedupe key, not a global
// source_ref. A redelivery to one connection must not double-create.
func TestHandleConnectionSameRepoIndependentGoals(t *testing.T) {
	ctx, st, h, _, _, route := connectionHarness(t, "")
	second := route
	second.ConnectionID = "conn2"
	second.AssigneeID = "sq1" // second squad is irrelevant to the dedupe assertion
	second.Secret = "conn-secret-2"

	payload := []byte(`{"action":"opened","issue":{"number":42,"title":"shared","body":"b"},"repository":{"full_name":"x/y"}}`)
	for _, r := range []ConnectionRoute{route, second} {
		if err := h.HandleConnection(ctx, payload, sign(r.Secret, payload), "", r); err != nil {
			t.Fatalf("delivery on %s: %v", r.ConnectionID, err)
		}
	}
	var goals int
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM goal WHERE source_ref='github:x/y#42'`).Scan(&goals); err != nil {
		t.Fatal(err)
	}
	if goals != 2 {
		t.Fatalf("same repo on two connections = %d goals, want 2", goals)
	}
	rows, err := st.DB().QueryContext(ctx,
		`SELECT connection_id FROM goal WHERE source_ref='github:x/y#42' ORDER BY connection_id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var got []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		got = append(got, id)
	}
	if len(got) != 2 || got[0] != "conn1" || got[1] != "conn2" {
		t.Fatalf("goal connection ids = %v, want [conn1 conn2]", got)
	}
	// Redelivery to conn1 stays deduped.
	if err := h.HandleConnection(ctx, payload, sign(route.Secret, payload), "", route); err != nil {
		t.Fatalf("redelivery: %v", err)
	}
	if err := st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM goal WHERE source_ref='github:x/y#42'`).Scan(&goals); err != nil {
		t.Fatal(err)
	}
	if goals != 2 {
		t.Fatalf("redelivery mutated goal count to %d", goals)
	}
}
