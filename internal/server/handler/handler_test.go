package handler

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/service"
	"github.com/eushing/agentwork/internal/store"
)

// newTestHandlers wires a GoalService against an in-memory store and mounts
// the HTTP routes. Agents are seeded so goal creation passes existence checks.
func newTestHandlers(t *testing.T) (*Handlers, *store.Store, string) {
	t.Helper()
	st, err := store.Open(":memory:")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	ctx := context.Background()
	bus := events.NewBus()
	gs := service.NewGoalService(st, bus)

	rt, err := service.NewRuntimeService(st).Create(ctx, service.Runtime{Name: "rt-test", MachineID: "m1"})
	if err != nil {
		t.Fatalf("seed runtime: %v", err)
	}
	as := service.NewAgentService(st, events.NewBus())
	agent, err := as.Create(ctx, service.Agent{Name: "A", RuntimeID: rt.ID, MaxConcurrent: 2})
	if err != nil {
		t.Fatalf("seed agent: %v", err)
	}
	dom, err := service.NewDomainService(st, events.NewBus()).Create(ctx, service.Domain{Name: "handler-test", GitURL: "https://example.com/handler.git"})
	if err != nil {
		t.Fatalf("seed domain: %v", err)
	}

	h := &Handlers{Goal: gs, Agent: as}
	mux := http.NewServeMux()
	h.Mount(mux)

	for _, title := range []string{"oldest", "middle", "newest"} {
		if _, err := gs.Create(ctx, service.Goal{Title: title, AssigneeType: "agent", AssigneeID: agent.ID, Status: "active", DomainID: dom.ID}); err != nil {
			t.Fatalf("create goal %q: %v", title, err)
		}
	}
	return h, st, agent.ID
}

func get(t *testing.T, mux *http.ServeMux, path string) (*httptest.ResponseRecorder, []service.Goal) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	var goals []service.Goal
	if rec.Code == http.StatusOK {
		if err := json.Unmarshal(rec.Body.Bytes(), &goals); err != nil {
			t.Fatalf("decode response: %v", err)
		}
	}
	return rec, goals
}

// TestListGoalsLimit: GET /goals returns all by default; ?limit=N truncates
// to the N most recent; malformed limit values are rejected with 400.
func TestListGoalsLimit(t *testing.T) {
	h, _, _ := newTestHandlers(t)
	mux := http.NewServeMux()
	h.Mount(mux)

	rec, all := get(t, mux, "/goals")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /goals: status %d, body %s", rec.Code, rec.Body.String())
	}
	if len(all) != 3 {
		t.Fatalf("GET /goals: expected 3 goals, got %d", len(all))
	}
	if all[0].Title != "newest" {
		t.Fatalf("expected newest-first, got first=%q", all[0].Title)
	}

	rec, limited := get(t, mux, "/goals?limit=2")
	if rec.Code != http.StatusOK {
		t.Fatalf("GET /goals?limit=2: status %d, body %s", rec.Code, rec.Body.String())
	}
	if len(limited) != 2 {
		t.Fatalf("GET /goals?limit=2: expected 2 goals, got %d", len(limited))
	}
	if limited[0].Title != "newest" || limited[1].Title != "middle" {
		t.Fatalf("limit=2: expected [newest middle], got %q", []string{limited[0].Title, limited[1].Title})
	}

	for _, bad := range []string{"/goals?limit=abc", "/goals?limit=0", "/goals?limit=-1"} {
		rec, _ = get(t, mux, bad)
		if rec.Code != http.StatusBadRequest {
			t.Fatalf("GET %s: expected 400, got %d", bad, rec.Code)
		}
	}
}

// TestAgentDeleteImpactHandler: GET /agents/{id}/delete-impact returns the
// snake_case preflight JSON (blocked by the seeded active goals) and 404s for
// an unknown agent.
// TestSubmitGoalReviewHandler: POST /goals/{id}/submit-review wraps the goal
// in {"goal": ...} on 200, maps the service ValidationError to 400 (live runs
// or a non-active goal), and 404s for an unknown goal.
func TestSubmitGoalReviewHandler(t *testing.T) {
	h, _, _ := newTestHandlers(t)
	mux := http.NewServeMux()
	h.Mount(mux)

	_, goals := get(t, mux, "/goals")
	if len(goals) == 0 {
		t.Fatal("seed produced no goals")
	}
	goalID := goals[0].ID

	post := func(path, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(http.MethodPost, path, strings.NewReader(body))
		rec := httptest.NewRecorder()
		mux.ServeHTTP(rec, req)
		return rec
	}

	rec := post("/goals/"+goalID+"/submit-review", `{"reason":"交付物在 feed 里，提交审核"}`)
	if rec.Code != http.StatusOK {
		t.Fatalf("submit-review: status %d, body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		Goal service.Goal `json:"goal"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode submit-review: %v (body %s)", err, rec.Body.String())
	}
	if body.Goal.ID != goalID || body.Goal.Status != "review" || body.Goal.ReviewRequest == "" {
		t.Fatalf("unexpected submit-review body: %s", rec.Body.String())
	}

	// Already in review → the active-only refusal is a 400.
	if rec := post("/goals/"+goalID+"/submit-review", `{"reason":"again"}`); rec.Code != http.StatusBadRequest {
		t.Fatalf("second submit: status %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	// Malformed JSON is a 400 before the service is consulted.
	if rec := post("/goals/"+goalID+"/submit-review", `{"reason":`); rec.Code != http.StatusBadRequest {
		t.Fatalf("malformed body: status %d, want 400 (body %s)", rec.Code, rec.Body.String())
	}
	// Unknown goal → ErrNotFound → 404.
	if rec := post("/goals/does-not-exist/submit-review", `{"reason":"x"}`); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown goal: status %d, want 404 (body %s)", rec.Code, rec.Body.String())
	}
}

func TestAgentDeleteImpactHandler(t *testing.T) {
	h, _, agentID := newTestHandlers(t)
	mux := http.NewServeMux()
	h.Mount(mux)

	req := httptest.NewRequest(http.MethodGet, "/agents/"+agentID+"/delete-impact", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("delete-impact: status %d, body %s", rec.Code, rec.Body.String())
	}
	var body struct {
		AgentID       string `json:"agent_id"`
		CanDelete     bool   `json:"can_delete"`
		ActiveRuns    int    `json:"active_runs"`
		ActiveGoals   int    `json:"active_goals"`
		TerminalGoals int    `json:"terminal_goals"`
		Blockers      []struct {
			Kind    string `json:"kind"`
			Count   int    `json:"count"`
			Message string `json:"message"`
		} `json:"blockers"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode delete-impact: %v (body %s)", err, rec.Body.String())
	}
	if body.AgentID != agentID || body.CanDelete || body.ActiveGoals != 3 || len(body.Blockers) != 1 ||
		body.Blockers[0].Kind != "active_goals" || body.Blockers[0].Count != 3 || body.Blockers[0].Message == "" {
		t.Fatalf("unexpected delete-impact body: %s", rec.Body.String())
	}

	req = httptest.NewRequest(http.MethodGet, "/agents/does-not-exist/delete-impact", nil)
	rec = httptest.NewRecorder()
	mux.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("unknown agent: status %d, body %s", rec.Code, rec.Body.String())
	}
}
