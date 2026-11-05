package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/eushing/agentwork/internal/issue"
	"github.com/eushing/agentwork/internal/service"
)

// ── connections (contract §3: per-squad, per-repo webhook bindings) ──

// connectionWebhookResponse is the connection panel state. It deliberately
// mirrors domainWebhookResponse's hooks/deliveries blocks so the frontend can
// render both with the same components; only the identity + secret semantics
// differ (a connection's secret is never the platform fallback).
type connectionWebhookResponse struct {
	ConnectionID   string                  `json:"connection_id"`
	Provider       string                  `json:"provider"`
	Repo           string                  `json:"repo"`
	Path           string                  `json:"path"`
	SecretSet      bool                    `json:"secret_set"`
	EventsRequired []string                `json:"events_required"`
	Hooks          domainWebhookHooks      `json:"hooks"`
	Deliveries     []domainWebhookDelivery `json:"deliveries"`
}

// listConnections serves GET /connections (optionally filtered by squad).
func (h *Handlers) listConnections(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	var (
		out []service.Connection
		err error
	)
	if squadID := r.URL.Query().Get("squad_id"); squadID != "" {
		out, err = h.Connection.ListBySquad(ctx, squadID)
	} else {
		out, err = h.Connection.List(ctx)
	}
	writeJSON(w, out, err)
}

// createSquadConnection serves POST /squads/{id}/connections. The response is
// the only place the plaintext secret ever appears: Connection.Secret is
// json:"-" everywhere else.
func (h *Handlers) createSquadConnection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		DomainID string   `json:"domain_id"`
		Events   []string `json:"events"`
		Secret   string   `json:"secret"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if body.DomainID == "" {
		writeErr(w, http.StatusBadRequest, service.NewFieldRequiredError("domain_id"))
		return
	}
	conn, err := h.Connection.Create(r.Context(), service.CreateConnectionInput{
		SquadID:  r.PathValue("id"),
		DomainID: body.DomainID,
		Events:   body.Events,
		Secret:   body.Secret,
	})
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, struct {
		*service.Connection
		Secret string `json:"secret"`
	}{conn, conn.Secret}, nil)
}

// updateConnection serves PATCH /connections/{id} (partial: events and/or
// secret; clear_events resets the subscription to both).
func (h *Handlers) updateConnection(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Events      []string `json:"events"`
		Secret      string   `json:"secret"`
		ClearEvents bool     `json:"clear_events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	out, err := h.Connection.Update(r.Context(), r.PathValue("id"), service.UpdateConnectionInput{
		Events:      body.Events,
		Secret:      body.Secret,
		ClearEvents: body.ClearEvents,
	})
	writeJSON(w, out, err)
}

// deleteConnection serves DELETE /connections/{id} → 204.
func (h *Handlers) deleteConnection(w http.ResponseWriter, r *http.Request) {
	if err := h.Connection.Delete(r.Context(), r.PathValue("id")); err != nil {
		writeJSON(w, nil, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// getConnectionWebhook serves GET /connections/{id}/webhook: effective secret
// presence + host hook registrations + this connection's recent deliveries.
func (h *Handlers) getConnectionWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	conn, err := h.Connection.Get(ctx, r.PathValue("id"))
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	events := conn.Events
	if events == nil {
		events = []string{}
	}
	out := connectionWebhookResponse{
		ConnectionID:   conn.ID,
		Provider:       conn.Provider,
		Repo:           conn.Repo,
		Path:           conn.WebhookPath,
		SecretSet:      conn.SecretSet,
		EventsRequired: events,
		Hooks:          domainWebhookHooks{Items: []issue.Webhook{}},
		Deliveries:     []domainWebhookDelivery{},
	}
	// Deliveries first: useful even when the host API is unavailable.
	if rows, err := h.Connection.ListDeliveries(ctx, conn.ID, 20); err == nil {
		for _, row := range rows {
			out.Deliveries = append(out.Deliveries, domainWebhookDelivery{
				Event: row.Event, Action: row.Action, Repo: row.Repo,
				OK: row.OK, Error: row.Error, ReceivedAt: row.ReceivedAt,
			})
		}
	}
	if conn.Repo == "" {
		out.Hooks.Supported = true
		out.Hooks.Error = webhookNoRepoError
		writeJSON(w, out, nil)
		return
	}
	if conn.Provider == "gitcode" {
		out.Hooks.Supported = false
		out.Hooks.Error = webhookGitCodeError
		writeJSON(w, out, nil)
		return
	}
	// The host token lives on the connection's domain (connections do not
	// duplicate credentials). A missing domain/credentials only blocks the
	// hook API — the connection itself still works.
	domain, derr := h.Domain.Get(ctx, conn.DomainID)
	if derr != nil || domain.GitCredentials == "" {
		out.Hooks.Supported = true
		out.Hooks.Error = webhookNoTokenError
		writeJSON(w, out, nil)
		return
	}
	hm, ok := domainHookManager(domain)
	if !ok {
		out.Hooks.Supported = false
		out.Hooks.Error = webhookGitCodeError
		writeJSON(w, out, nil)
		return
	}
	out.Hooks.Supported = true
	hooks, err := hm.ListHooks(ctx, conn.Repo)
	if err != nil {
		out.Hooks.Error = err.Error()
		writeJSON(w, out, nil)
		return
	}
	out.Hooks.OK = true
	out.Hooks.Items = hooks
	writeJSON(w, out, nil)
}

// registerConnectionWebhook serves POST /connections/{id}/webhook/register.
// The hook always signs with THIS connection's secret and points at THIS
// connection's path; the caller may pass the full origin+path, but the path
// must match (the server never invents an origin).
func (h *Handlers) registerConnectionWebhook(w http.ResponseWriter, r *http.Request) {
	var body struct {
		URL    string   `json:"url"`
		Events []string `json:"events"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	if body.URL == "" {
		writeErr(w, http.StatusBadRequest, service.NewFieldRequiredError("url"))
		return
	}
	ctx := r.Context()
	conn, err := h.Connection.Get(ctx, r.PathValue("id"))
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	if conn.Repo == "" {
		writeJSON(w, nil, service.NewValidationError(webhookNoRepoError))
		return
	}
	if conn.Provider != "github" {
		writeJSON(w, nil, service.NewValidationError(webhookGitCodeError))
		return
	}
	// Require an ABSOLUTE http(s) URL whose path is exactly this connection's
	// endpoint. A bare path would make the host register a relative hook (dead),
	// and a bare origin would register one at "/" — both must be rejected.
	u, perr := url.Parse(body.URL)
	if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" ||
		u.Path != conn.WebhookPath || u.RawQuery != "" || u.Fragment != "" {
		writeJSON(w, nil, service.NewValidationError("webhook 地址必须是该连接的完整地址（https://你的域名"+conn.WebhookPath+"）"))
		return
	}
	domain, derr := h.Domain.Get(ctx, conn.DomainID)
	if derr != nil {
		writeJSON(w, nil, derr)
		return
	}
	if domain.GitCredentials == "" {
		writeJSON(w, nil, service.NewValidationError(webhookNoTokenError))
		return
	}
	if conn.Secret == "" {
		writeJSON(w, nil, service.NewValidationError(webhookNoSecretError))
		return
	}
	events := conn.Events
	if len(body.Events) > 0 {
		events, err = service.NormalizeConnectionEvents(body.Events)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
	}
	hm, ok := domainHookManager(domain)
	if !ok {
		writeJSON(w, nil, service.NewValidationError(webhookGitCodeError))
		return
	}
	hooks, err := hm.ListHooks(ctx, conn.Repo)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	spec := issue.HookSpec{URL: body.URL, Secret: conn.Secret, Events: events}
	var existing *issue.Webhook
	for i := range hooks {
		if hooks[i].URL == body.URL {
			existing = &hooks[i]
			break
		}
	}
	if existing != nil {
		hook, err := hm.UpdateHook(ctx, conn.Repo, existing.ID, spec)
		if err != nil {
			writeJSON(w, nil, err)
			return
		}
		writeJSON(w, struct {
			Created bool          `json:"created"`
			Hook    issue.Webhook `json:"hook"`
		}{false, hook}, nil)
		return
	}
	hook, err := hm.CreateHook(ctx, conn.Repo, spec)
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	writeJSON(w, struct {
		Created bool          `json:"created"`
		Hook    issue.Webhook `json:"hook"`
	}{true, hook}, nil)
}

// connectionWebhook serves POST /webhooks/{provider}/{connectionID}: the
// per-connection inbound endpoint. A provider mismatch is a 404 (the id is
// global; the path must name its own provider).
func (h *Handlers) connectionWebhook(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	provider := r.PathValue("provider")
	conn, err := h.Connection.Get(ctx, r.PathValue("connectionID"))
	if err != nil {
		writeJSON(w, nil, err)
		return
	}
	if !strings.EqualFold(conn.Provider, provider) {
		writeErr(w, http.StatusNotFound, fmt.Errorf("connection %s is not a %s connection", conn.ID, provider))
		return
	}
	wh := h.IssueWebhooks[provider]
	if wh == nil {
		writeErr(w, http.StatusNotFound, fmt.Errorf("webhook %s not configured", provider))
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err)
		return
	}
	sigHeader := "X-Hub-Signature-256"
	if provider == "gitcode" {
		sigHeader = "X-GitCode-Signature-256"
	}
	route := issue.ConnectionRoute{
		ConnectionID: conn.ID,
		DomainID:     conn.DomainID,
		AssigneeID:   conn.SquadID,
		AssigneeType: "squad",
		Repo:         conn.Repo,
		Secret:       conn.Secret,
		Events:       conn.Events,
	}
	if err := wh.HandleConnection(ctx, body, r.Header.Get(sigHeader), r.Header.Get("X-GitCode-Token"), route); err != nil {
		writeErr(w, http.StatusUnauthorized, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
