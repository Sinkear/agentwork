package handler

import (
	"encoding/json"
	"net/http"
	"strconv"

	"github.com/eushing/agentwork/internal/service"
)

// listRsiSessions returns training sessions, newest first.
func (h *Handlers) listRsiSessions(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if raw := r.URL.Query().Get("limit"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 {
			writeErr(w, http.StatusBadRequest, service.NewValidationError("limit must be a positive integer"))
			return
		}
		if n > 200 {
			n = 200
		}
		limit = n
	}
	if h.RSI == nil {
		writeJSON(w, []service.RsiSession{}, nil)
		return
	}
	out, err := h.RSI.ListSessions(r.Context(), limit)
	if out == nil {
		out = []service.RsiSession{}
	}
	writeJSON(w, out, err)
}

// getRsiSession returns one session with its report, findings and the
// orchestration graph (frozen at analysis time, or live-built while waiting).
func (h *Handlers) getRsiSession(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if h.RSI == nil {
		writeErr(w, http.StatusNotFound, service.ErrNotFound)
		return
	}
	session, report, findings, graph, err := h.RSI.GetSession(r.Context(), id)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	if findings == nil {
		findings = []service.RsiFinding{}
	}
	writeJSON(w, map[string]any{
		"session":  session,
		"report":   report,
		"findings": findings,
		"workflow": graph,
	}, nil)
}

// listRsiRounds serves the insight panel's optimization-round ledger for one
// session: every frozen analysis round with its delta vs the previous round.
func (h *Handlers) listRsiRounds(w http.ResponseWriter, r *http.Request) {
	if h.RSI == nil {
		writeErr(w, http.StatusNotFound, service.ErrNotFound)
		return
	}
	rounds, err := h.RSI.ListRounds(r.Context(), r.PathValue("id"))
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, rounds, nil)
}

// rsiOverview returns aggregate training metrics over the last N days.
func (h *Handlers) rsiOverview(w http.ResponseWriter, r *http.Request) {
	days := 30
	if raw := r.URL.Query().Get("days"); raw != "" {
		n, err := strconv.Atoi(raw)
		if err != nil || n < 1 || n > 365 {
			writeErr(w, http.StatusBadRequest, service.NewValidationError("days must be between 1 and 365"))
			return
		}
		days = n
	}
	if h.RSI == nil {
		writeJSON(w, service.RsiOverview{}, nil)
		return
	}
	out, err := h.RSI.Overview(r.Context(), days)
	writeJSON(w, out, err)
}

// goalWorkflow returns the live orchestration graph for one goal.
func (h *Handlers) goalWorkflow(w http.ResponseWriter, r *http.Request) {
	if h.RSI == nil {
		writeErr(w, http.StatusNotFound, service.ErrNotFound)
		return
	}
	graph, err := h.RSI.GoalWorkflow(r.Context(), r.PathValue("goalId"))
	writeJSON(w, graph, err)
}

// squadWorkflows returns compact workflow summaries for a squad's recent
// goals — orchestration visibility for the squad page.
func (h *Handlers) squadWorkflows(w http.ResponseWriter, r *http.Request) {
	if h.RSI == nil {
		writeJSON(w, []service.WorkflowGoalSummary{}, nil)
		return
	}
	limit := 10
	if raw := r.URL.Query().Get("limit"); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n > 0 && n <= 50 {
			limit = n
		}
	}
	out, err := h.RSI.SquadWorkflows(r.Context(), r.PathValue("squadId"), limit)
	if out == nil {
		out = []service.WorkflowGoalSummary{}
	}
	writeJSON(w, out, err)
}

// patchRsiFinding confirms or dismisses one finding.
func (h *Handlers) patchRsiFinding(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Status string `json:"status"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, service.NewValidationError("invalid JSON body"))
		return
	}
	if body.Status != "confirmed" && body.Status != "dismissed" {
		writeErr(w, http.StatusBadRequest, service.NewValidationError("status must be confirmed or dismissed"))
		return
	}
	if h.RSI == nil {
		writeErr(w, http.StatusNotFound, service.ErrNotFound)
		return
	}
	err := h.RSI.UpdateFinding(r.Context(), r.PathValue("id"), body.Status)
	writeJSON(w, map[string]any{"ok": err == nil}, err)
}

// retryRsiSession re-runs analysis for a failed session.
func (h *Handlers) retryRsiSession(w http.ResponseWriter, r *http.Request) {
	if h.RSI == nil {
		writeErr(w, http.StatusNotFound, service.ErrNotFound)
		return
	}
	err := h.RSI.Retry(r.Context(), r.PathValue("id"))
	writeJSON(w, map[string]any{"ok": err == nil}, err)
}
