package service

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/store"
)

// Connection is a first-class squad ↔ repository binding. Each connection has
// its OWN inbound webhook path and signing secret, so two squads (or one squad
// with two bindings) can track the same repository without the host hook /
// assignee collisions of the legacy domain-level binding.
//
// Secret is NEVER serialized (json:"-"); only Create returns the plaintext to
// the caller (once) via the handler's create response.
type Connection struct {
	ID          string   `json:"id"`
	SquadID     string   `json:"squad_id"`
	DomainID    string   `json:"domain_id"`
	Provider    string   `json:"provider"`
	Repo        string   `json:"repo"`
	Events      []string `json:"events"`
	Secret      string   `json:"-"`
	SecretSet   bool     `json:"secret_set"`
	WebhookPath string   `json:"webhook_path"`
	CreatedAt   string   `json:"created_at"`
}

// CreateConnectionInput is one binding request. SquadID comes from the path,
// DomainID from the body; Events/Secret are optional.
type CreateConnectionInput struct {
	SquadID  string
	DomainID string
	Events   []string
	Secret   string
}

// UpdateConnectionInput changes only the provided parts. A nil Events leaves
// the subscription untouched; ClearEvents (or an explicitly empty Events list)
// resets it to the default both-events subscription. An empty Secret keeps the
// stored one — connections always need a secret, so there is no clear.
type UpdateConnectionInput struct {
	Events      []string
	Secret      string
	ClearEvents bool
}

// DeliveryLog is one inbound webhook delivery attributed to a connection
// (the connection panel's ground-truth list).
type DeliveryLog struct {
	Event        string `json:"event"`
	Action       string `json:"action"`
	Repo         string `json:"repo"`
	OK           bool   `json:"ok"`
	Error        string `json:"error"`
	ReceivedAt   string `json:"received_at"`
	ConnectionID string `json:"connection_id"`
}

// ConnectionService owns connection CRUD + per-connection delivery logs.
type ConnectionService struct {
	st  *store.Store
	bus *events.Bus
}

func NewConnectionService(st *store.Store, bus *events.Bus) *ConnectionService {
	return &ConnectionService{st: st, bus: bus}
}

// connectionEvents are the only accepted subscription values.
const (
	connectionEventIssues      = "issues"
	connectionEventPullRequest = "pull_request"
)

func defaultConnectionEvents() []string {
	return []string{connectionEventIssues, connectionEventPullRequest}
}

// NormalizeConnectionEvents validates, de-duplicates (order preserved), and
// defaults the subscription. Empty input → both events. Input made up only of
// unknown values → ValidationError; mixed input drops the unknown entries.
func NormalizeConnectionEvents(in []string) ([]string, error) {
	if len(in) == 0 {
		return defaultConnectionEvents(), nil
	}
	out := make([]string, 0, len(in))
	seen := map[string]bool{}
	for _, e := range in {
		e = strings.TrimSpace(e)
		if e == "" || seen[e] {
			continue
		}
		switch e {
		case connectionEventIssues, connectionEventPullRequest:
			seen[e] = true
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return nil, NewValidationError("events 只支持 issues、pull_request")
	}
	return out, nil
}

// parseConnectionEvents decodes the stored comma-separated column. A row with
// an empty/corrupt value falls back to the default subscription (never nil —
// the JSON shape is always an array).
func parseConnectionEvents(raw string) []string {
	parts := strings.Split(raw, ",")
	out := make([]string, 0, len(parts))
	seen := map[string]bool{}
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if seen[p] {
			continue
		}
		switch p {
		case connectionEventIssues, connectionEventPullRequest:
			seen[p] = true
			out = append(out, p)
		}
	}
	if len(out) == 0 {
		return defaultConnectionEvents()
	}
	return out
}

func connectionWebhookPath(provider, id string) string {
	if provider == "" {
		provider = "github"
	}
	return "/webhooks/" + provider + "/" + id
}

// generateConnectionSecret returns 32 random bytes as hex (the per-connection
// webhook signing secret).
func generateConnectionSecret() (string, error) {
	var b [32]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", fmt.Errorf("generate connection secret: %w", err)
	}
	return hex.EncodeToString(b[:]), nil
}

const connectionSelectCols = `id, squad_id, domain_id, provider, repo, events, secret, created_at`

func (s *ConnectionService) scanConnection(sc interface {
	Scan(dest ...any) error
}) (*Connection, error) {
	var c Connection
	var eventsRaw string
	if err := sc.Scan(&c.ID, &c.SquadID, &c.DomainID, &c.Provider, &c.Repo, &eventsRaw, &c.Secret, &c.CreatedAt); err != nil {
		return nil, err
	}
	if c.Provider == "" {
		c.Provider = "github"
	}
	c.Events = parseConnectionEvents(eventsRaw)
	c.SecretSet = c.Secret != ""
	c.WebhookPath = connectionWebhookPath(c.Provider, c.ID)
	return &c, nil
}

// List returns every connection, newest first.
func (s *ConnectionService) List(ctx context.Context) ([]Connection, error) {
	return s.queryConnections(ctx, `SELECT `+connectionSelectCols+` FROM connection ORDER BY created_at DESC`)
}

// ListBySquad returns one squad's connections, newest first.
func (s *ConnectionService) ListBySquad(ctx context.Context, squadID string) ([]Connection, error) {
	return s.queryConnections(ctx,
		`SELECT `+connectionSelectCols+` FROM connection WHERE squad_id=? ORDER BY created_at DESC`, squadID)
}

func (s *ConnectionService) queryConnections(ctx context.Context, query string, args ...any) ([]Connection, error) {
	rows, err := s.st.DB().QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list connections: %w", err)
	}
	defer rows.Close()
	out := []Connection{}
	for rows.Next() {
		c, err := s.scanConnection(rows)
		if err != nil {
			return nil, fmt.Errorf("scan connection: %w", err)
		}
		out = append(out, *c)
	}
	return out, rows.Err()
}

// Get returns one connection (including its Secret, for the webhook delivery
// path — never serialized by HTTP).
func (s *ConnectionService) Get(ctx context.Context, id string) (*Connection, error) {
	row := s.st.DB().QueryRowContext(ctx,
		`SELECT `+connectionSelectCols+` FROM connection WHERE id=?`, id)
	c, err := s.scanConnection(row)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, fmt.Errorf("get connection: %w", err)
	}
	return c, nil
}

// Create binds a squad to a domain's repository. The connection's provider/repo
// are copied from the domain (the repo must be configured), the secret is
// generated when absent, and the domain's LEGACY direct-binding assignees are
// cleared so the two routing paths cannot both fire.
func (s *ConnectionService) Create(ctx context.Context, in CreateConnectionInput) (*Connection, error) {
	if err := mustExist(ctx, s.st, `SELECT COUNT(*) FROM squad WHERE id=?`, in.SquadID, "squad"); err != nil {
		return nil, err
	}
	if err := mustExist(ctx, s.st, `SELECT COUNT(*) FROM domain WHERE id=?`, in.DomainID, "domain"); err != nil {
		return nil, err
	}
	var repo, provider string
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT issue_repo, COALESCE(issue_provider,'') FROM domain WHERE id=?`, in.DomainID).
		Scan(&repo, &provider); err != nil {
		return nil, fmt.Errorf("read domain issue config: %w", err)
	}
	if repo == "" {
		return nil, NewValidationError("没有配置跟踪仓库（issue_repo）")
	}
	if provider == "" {
		provider = "github"
	}
	// One squad may hold at most one connection per repo: a second connection
	// would poll the same repo twice and create a duplicate goal for every
	// issue/PR (goals are deduped per connection). Issue and PR watching are
	// separate event flags on the SAME connection, not separate connections.
	var dup int
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM connection WHERE squad_id=? AND domain_id=?`,
		in.SquadID, in.DomainID).Scan(&dup); err != nil {
		return nil, fmt.Errorf("check existing connection: %w", err)
	}
	if dup > 0 {
		return nil, NewValidationError("该小队已接入这个仓库；如需调整监听范围，请编辑现有连接的事件")
	}
	events, err := NormalizeConnectionEvents(in.Events)
	if err != nil {
		return nil, err
	}
	secret := strings.TrimSpace(in.Secret)
	if len(secret) > 256 {
		return nil, NewValidationError("webhook secret 过长（最多 256 字符）")
	}
	if secret == "" {
		secret, err = generateConnectionSecret()
		if err != nil {
			return nil, err
		}
	}
	c := &Connection{
		ID:          newID(),
		SquadID:     in.SquadID,
		DomainID:    in.DomainID,
		Provider:    provider,
		Repo:        repo,
		Events:      events,
		Secret:      secret,
		SecretSet:   true,
		WebhookPath: connectionWebhookPath(provider, ""),
		CreatedAt:   now(),
	}
	c.WebhookPath = connectionWebhookPath(c.Provider, c.ID)

	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("create connection: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO connection (id,squad_id,domain_id,provider,repo,events,secret,created_at)
		 VALUES (?,?,?,?,?,?,?,?)`,
		c.ID, c.SquadID, c.DomainID, c.Provider, c.Repo,
		strings.Join(c.Events, ","), c.Secret, c.CreatedAt); err != nil {
		return nil, fmt.Errorf("insert connection: %w", err)
	}
	// The legacy direct binding (issue_assignee/pull_assignee) would keep
	// polling/webhook firing alongside the connection — clear it. The TYPE
	// columns are preserved (they are harmless without an assignee).
	if _, err := tx.ExecContext(ctx,
		`UPDATE domain SET issue_assignee='', pull_assignee='' WHERE id=?`, c.DomainID); err != nil {
		return nil, fmt.Errorf("clear legacy domain assignees: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("create connection: %w", err)
	}
	s.publish(ctx, "connection:created", *c)
	return c, nil
}

// Update edits events and/or the secret. Unprovided parts keep their value.
func (s *ConnectionService) Update(ctx context.Context, id string, in UpdateConnectionInput) (*Connection, error) {
	cur, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	events := cur.Events
	if in.ClearEvents {
		events = defaultConnectionEvents()
	} else if in.Events != nil {
		events, err = NormalizeConnectionEvents(in.Events)
		if err != nil {
			return nil, err
		}
	}
	secret := cur.Secret
	if in.Secret != "" {
		secret = strings.TrimSpace(in.Secret)
		// A whitespace-only secret is a mistake, not a request to clear it:
		// connections always sign with a secret, so reject instead of storing ""
		// (which would silently break every delivery).
		if secret == "" {
			return nil, NewValidationError("webhook secret 不能为空")
		}
		if len(secret) > 256 {
			return nil, NewValidationError("webhook secret 过长（最多 256 字符）")
		}
	}
	if _, err := s.st.DB().ExecContext(ctx,
		`UPDATE connection SET events=?, secret=? WHERE id=?`,
		strings.Join(events, ","), secret, id); err != nil {
		return nil, fmt.Errorf("update connection: %w", err)
	}
	out, err := s.Get(ctx, id)
	if err != nil {
		return nil, err
	}
	s.publish(ctx, "connection:updated", *out)
	return out, nil
}

// Delete removes a connection. Its historical delivery rows stay for
// debugging (trimmed by the shared retention sweep).
func (s *ConnectionService) Delete(ctx context.Context, id string) error {
	res, err := s.st.DB().ExecContext(ctx, `DELETE FROM connection WHERE id=?`, id)
	if err != nil {
		return fmt.Errorf("delete connection: %w", err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete connection: %w", err)
	}
	if n == 0 {
		return ErrNotFound
	}
	s.publish(ctx, "connection:deleted", id)
	return nil
}

// ListDeliveries returns a connection's most recent deliveries, newest first.
// Always a non-nil slice.
func (s *ConnectionService) ListDeliveries(ctx context.Context, connID string, limit int) ([]DeliveryLog, error) {
	if limit <= 0 {
		limit = 20
	}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT event, action, repo, ok, COALESCE(error,''), received_at, COALESCE(connection_id,'')
		   FROM webhook_delivery WHERE connection_id=?
		  ORDER BY received_at DESC LIMIT ?`, connID, limit)
	if err != nil {
		return nil, fmt.Errorf("list connection deliveries: %w", err)
	}
	defer rows.Close()
	out := []DeliveryLog{}
	for rows.Next() {
		var d DeliveryLog
		var ok int
		if err := rows.Scan(&d.Event, &d.Action, &d.Repo, &ok, &d.Error, &d.ReceivedAt, &d.ConnectionID); err != nil {
			return nil, fmt.Errorf("scan connection delivery: %w", err)
		}
		d.OK = ok != 0
		out = append(out, d)
	}
	return out, rows.Err()
}

// publish emits a change event for live UI refresh; the bus is optional so
// tests may construct the service without one.
func (s *ConnectionService) publish(ctx context.Context, topic string, payload any) {
	if s.bus == nil {
		return
	}
	s.bus.Publish(ctx, events.Event{Topic: topic, Payload: payload})
}
