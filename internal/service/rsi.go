package service

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/logging"
	"github.com/eushing/agentwork/internal/store"
)

// RsiSession is one training session: the observation window that starts
// when a goal (execution_mode=train) is created and closes when the goal
// reaches a terminal state. It is the unit the Training tab lists.
//
// RSI deliberately NEVER changes the executing agent's behavior in this
// version. It records structured spans, computes deterministic metrics, and
// emits reviewable findings — the human (or a later gated loop) decides what
// to adopt. This keeps the "training" loop inside the harness layer instead
// of touching model weights or verifier/scoring logic.
type RsiSession struct {
	ID             string  `json:"id"`
	GoalID         string  `json:"goal_id"`
	GoalTitle      string  `json:"goal_title"`
	DomainID       string  `json:"domain_id"`
	Status         string  `json:"status"` // waiting|collecting|analyzing|ready|failed|skipped
	ContentCapture string  `json:"content_capture"`
	SampleRate     float64 `json:"sample_rate"`
	BudgetUSD      float64 `json:"budget_usd"`
	TraceCount     int     `json:"trace_count"`
	CostUSD        float64 `json:"cost_usd"`
	FindingCount   int     `json:"finding_count"`
	TopCategory    string  `json:"top_category"`
	TopTitle       string  `json:"top_title"`
	Error          string  `json:"error"`
	StartedAt      string  `json:"started_at"`
	SealedAt       string  `json:"sealed_at"`
	FinishedAt     string  `json:"finished_at"`
}

// RsiSpan is one row of the wide trace table. kind:
// run|llm|tool|verify|gate|human_wait.
type RsiSpan struct {
	ID          string         `json:"id"`
	SessionID   string         `json:"session_id"`
	RunID       string         `json:"run_id"`
	SpanID      string         `json:"span_id"`
	ParentID    string         `json:"parent_span_id"`
	Kind        string         `json:"kind"`
	Name        string         `json:"name"`
	StartedAt   int64          `json:"started_at_ms"`
	EndedAt     int64          `json:"ended_at_ms"`
	DurationMS  int64          `json:"duration_ms"`
	Status      string         `json:"status"`
	ErrorClass  string         `json:"error_class"`
	RetryCount  int            `json:"retry_count"`
	ToolName    string         `json:"tool_name"`
	ArgsHash    string         `json:"args_hash"`
	TokensIn    int            `json:"tokens_in"`
	TokensOut   int            `json:"tokens_out"`
	CostUSD     float64        `json:"cost_usd"`
	ContextUsed int            `json:"context_used"`
	ContextSize int            `json:"context_size"`
	ContentSize int            `json:"content_bytes"`
	Attrs       map[string]any `json:"attrs"`
	CreatedAt   string         `json:"created_at"`
}

// RsiReport is the deterministic analysis output for one session.
type RsiReport struct {
	ID         string         `json:"id"`
	SessionID  string         `json:"session_id"`
	Status     string         `json:"status"` // ready|failed
	Summary    string         `json:"summary"`
	Metrics    map[string]any `json:"metrics"`
	Baseline   map[string]any `json:"baseline"`
	Narrative  string         `json:"narrative"`
	Error      string         `json:"error"`
	CreatedAt  string         `json:"created_at"`
	FinishedAt string         `json:"finished_at"`
}

// RsiFinding is one candidate optimization with its evidence and confidence.
type RsiFinding struct {
	ID          string   `json:"id"`
	ReportID    string   `json:"report_id"`
	Category    string   `json:"category"`
	Severity    string   `json:"severity"`
	Confidence  string   `json:"confidence"` // L1 observed | L2 recurring | L3 A/B verified
	Title       string   `json:"title"`
	Explanation string   `json:"explanation"`
	Suggestion  string   `json:"suggestion"`
	Evidence    []string `json:"evidence"`
	Score       float64  `json:"score"`
	Status      string   `json:"status"` // open|confirmed|dismissed
	CreatedAt   string   `json:"created_at"`
}

// RsiOverview is the aggregate strip above the Training list.
type RsiOverview struct {
	Sessions       int     `json:"sessions"`
	SuccessRate    float64 `json:"success_rate"`
	CostPerSuccess float64 `json:"cost_per_success"`
	P95LeadTimeMS  int64   `json:"p95_lead_time_ms"`
	HumanWaitMS    int64   `json:"human_wait_ms"`
	Findings       int     `json:"findings"`
}

// RSIService owns training-session bookkeeping, the live span sink, and the
// deterministic analyzer. One instance is shared by the daemon (sink) and
// the HTTP handler (read APIs).
type RSIService struct {
	st  *store.Store
	bus *events.Bus

	mu    sync.Mutex
	cache map[string]string // runID → sessionID

	// analyzeMu serializes analyses: analysis is fast (SQL + arithmetic) and
	// a single lock keeps a retry from racing a terminal trigger.
	analyzeMu sync.Mutex
}

func NewRSIService(st *store.Store, bus *events.Bus) *RSIService {
	return &RSIService{st: st, bus: bus, cache: make(map[string]string)}
}

// EnsureSession creates the training session for a train-mode goal if it does
// not exist. Sampling happens ONCE here: a sampled-out goal gets status
// 'skipped' and records no spans.
func (s *RSIService) EnsureSession(ctx context.Context, g *Goal) error {
	if g == nil || g.ExecutionMode != "train" {
		return nil
	}
	var exists int
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM training_session WHERE goal_id=?`, g.ID).Scan(&exists); err != nil {
		return err
	}
	if exists > 0 {
		return nil
	}
	status := "waiting"
	if g.SampleRate > 0 && g.SampleRate < 1 && rand.Float64() > g.SampleRate {
		status = "skipped"
	}
	_, err := s.st.DB().ExecContext(ctx,
		`INSERT OR IGNORE INTO training_session
		 (id,goal_id,content_capture,sample_rate,budget_usd,status,started_at)
		 VALUES (?,?,?,?,?,?,?)`,
		newID(), g.ID, g.ContentCapture, g.SampleRate, g.BudgetUSD, status, now())
	if err != nil {
		return fmt.Errorf("insert training session: %w", err)
	}
	return nil
}

// sessionForRun resolves the collecting training session for a run. Empty
// session (and nil error) means the run is not observed: inference goal, a
// sampled-out session, or the session already sealed.
func (s *RSIService) sessionForRun(ctx context.Context, runID string) string {
	if runID == "" {
		return ""
	}
	s.mu.Lock()
	if id, ok := s.cache[runID]; ok {
		s.mu.Unlock()
		return id
	}
	s.mu.Unlock()
	var id string
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT ts.id FROM training_session ts JOIN run r ON r.goal_id=ts.goal_id
		 WHERE r.id=? AND ts.status IN ('waiting','collecting')
		 ORDER BY ts.started_at DESC LIMIT 1`, runID).Scan(&id)
	if err != nil {
		return ""
	}
	s.mu.Lock()
	s.cache[runID] = id
	s.mu.Unlock()
	// First observed event moves the session from waiting to collecting.
	_, _ = s.st.DB().ExecContext(ctx,
		`UPDATE training_session SET status='collecting' WHERE id=? AND status='waiting'`, id)
	return id
}

func (s *RSIService) forgetRun(runID string) {
	s.mu.Lock()
	delete(s.cache, runID)
	s.mu.Unlock()
}

// RecordToolStart upserts the tool span for (run, call). ACP emits several
// tool_use updates per call; only the first stamps started_at, later ones
// refresh the size/attrs.
func (s *RSIService) RecordToolStart(ctx context.Context, runID, callID, tool, input, status string) {
	sid := s.sessionForRun(ctx, runID)
	if sid == "" {
		return
	}
	if callID == "" {
		callID = newID()
	}
	spanID := runID + ":" + callID + ":tool"
	hash := shortHash(input)
	attrs := s.attrsFor(ctx, sid, input)
	ab, _ := json.Marshal(attrs)
	nowMS := time.Now().UnixMilli()
	res, err := s.st.DB().ExecContext(ctx,
		`INSERT OR IGNORE INTO trace_span
		 (id,session_id,run_id,span_id,parent_span_id,kind,name,started_at_ms,ended_at_ms,duration_ms,status,tool_name,args_hash,content_bytes,attrs_json,created_at)
		 VALUES (?,?,?,?,?, 'tool', ?, ?, 0, 0, 'running', ?, ?, ?, ?, ?)`,
		spanID, sid, runID, spanID, runID+":run", tool, nowMS, tool, hash, len(input), string(ab), now())
	if err != nil {
		logging.Infof("rsi: record tool start %s: %v", runID, err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		_, _ = s.st.DB().ExecContext(ctx, `UPDATE training_session SET trace_count=trace_count+1 WHERE id=?`, sid)
		return
	}
	_, _ = s.st.DB().ExecContext(ctx,
		`UPDATE trace_span SET tool_name=?, args_hash=?, content_bytes=?, attrs_json=? WHERE id=?`,
		tool, hash, len(input), string(ab), spanID)
}

// RecordToolEnd closes the tool span (created lazily if the start was lost).
func (s *RSIService) RecordToolEnd(ctx context.Context, runID, callID, tool, output, status string) {
	sid := s.sessionForRun(ctx, runID)
	if sid == "" {
		return
	}
	if callID == "" {
		callID = newID()
	}
	spanID := runID + ":" + callID + ":tool"
	if tool == "" {
		tool = "tool"
	}
	st := "ok"
	if status == "failed" || status == "error" {
		st = "error"
	}
	nowMS := time.Now().UnixMilli()
	attrs := s.attrsFor(ctx, sid, output)
	ab, _ := json.Marshal(attrs)
	res, err := s.st.DB().ExecContext(ctx,
		`INSERT OR IGNORE INTO trace_span
		 (id,session_id,run_id,span_id,parent_span_id,kind,name,started_at_ms,ended_at_ms,duration_ms,status,tool_name,args_hash,content_bytes,attrs_json,created_at)
		 VALUES (?,?,?,?,?, 'tool', ?, ?, ?, 0, ?, ?, ?, ?, ?, ?)`,
		spanID, sid, runID, spanID, runID+":run", tool, nowMS, nowMS, st, tool, shortHash(output), len(output), string(ab), now())
	if err != nil {
		logging.Infof("rsi: record tool end %s: %v", runID, err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		_, _ = s.st.DB().ExecContext(ctx, `UPDATE training_session SET trace_count=trace_count+1 WHERE id=?`, sid)
		return
	}
	_, _ = s.st.DB().ExecContext(ctx,
		`UPDATE trace_span SET ended_at_ms=?, duration_ms=MAX(0,?-started_at_ms), status=?, content_bytes=MAX(content_bytes,?), attrs_json=? WHERE id=?`,
		nowMS, nowMS, st, len(output), string(ab), spanID)
}

// RecordUsage aggregates ACP usage_update reports into one llm span per run.
// The protocol reports context used/size and cost, not per-call token counts;
// tokens_in/out stay 0 until the runtime provides them (never fabricated).
func (s *RSIService) RecordUsage(ctx context.Context, runID string, used, total int, cost float64) {
	sid := s.sessionForRun(ctx, runID)
	if sid == "" {
		return
	}
	spanID := runID + ":usage"
	res, err := s.st.DB().ExecContext(ctx,
		`INSERT OR IGNORE INTO trace_span
		 (id,session_id,run_id,span_id,parent_span_id,kind,name,started_at_ms,ended_at_ms,duration_ms,status,cost_usd,context_used,context_size,attrs_json,created_at)
		 VALUES (?,?,?,?,?, 'llm', 'usage', ?, 0, 0, 'ok', ?, ?, ?, '{}', ?)`,
		spanID, sid, runID, spanID, runID+":run", time.Now().UnixMilli(), cost, used, total, now())
	if err != nil {
		logging.Infof("rsi: record usage %s: %v", runID, err)
		return
	}
	if n, _ := res.RowsAffected(); n > 0 {
		_, _ = s.st.DB().ExecContext(ctx, `UPDATE training_session SET trace_count=trace_count+1 WHERE id=?`, sid)
		return
	}
	_, _ = s.st.DB().ExecContext(ctx,
		`UPDATE trace_span SET cost_usd=cost_usd+?, context_used=MAX(context_used,?), context_size=MAX(context_size,?) WHERE id=?`,
		cost, used, total, spanID)
}

// attrsFor applies the goal's content-capture policy. Default (redacted)
// keeps measurements and a short hash, never the raw text.
func (s *RSIService) attrsFor(ctx context.Context, sessionID, content string) map[string]any {
	capture := "redacted"
	_ = s.st.DB().QueryRowContext(ctx,
		`SELECT content_capture FROM training_session WHERE id=?`, sessionID).Scan(&capture)
	a := map[string]any{"bytes": len(content), "hash": shortHash(content)}
	if capture == "full" {
		if len(content) > 8192 {
			content = content[:8192]
		}
		a["preview"] = content
	}
	return a
}

func shortHash(s string) string {
	if s == "" {
		return ""
	}
	h := sha256.Sum256([]byte(s))
	return hex.EncodeToString(h[:8])
}

// OnGoalTerminal seals the session and launches the analyzer in the
// background. Called AFTER the reconcile transaction commits, so the
// analysis always sees the goal's terminal status.
func (s *RSIService) OnGoalTerminal(ctx context.Context, goalID string) {
	var sid string
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT id FROM training_session WHERE goal_id=? ORDER BY started_at DESC LIMIT 1`, goalID).Scan(&sid)
	if errors.Is(err, sql.ErrNoRows) {
		return
	}
	if err != nil {
		logging.Infof("rsi: terminal lookup %s: %v", goalID, err)
		return
	}
	res, err := s.st.DB().ExecContext(ctx,
		`UPDATE training_session SET status='analyzing', sealed_at=? WHERE id=? AND status IN ('waiting','collecting')`, now(), sid)
	if err != nil {
		logging.Infof("rsi: seal session %s: %v", sid, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // already analyzing/ready/failed/skipped
	}
	go s.runAnalysis(sid)
}

func (s *RSIService) runAnalysis(sessionID string) {
	defer func() {
		if r := recover(); r != nil {
			logging.Errorf("rsi: analysis panic for %s: %v", sessionID, r)
		}
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if err := s.Analyze(ctx, sessionID); err != nil {
		logging.Infof("rsi: analyze %s: %v", sessionID, err)
		_, _ = s.st.DB().ExecContext(context.Background(),
			`UPDATE training_session SET status='failed', error=?, finished_at=? WHERE id=?`,
			err.Error(), now(), sessionID)
	}
}

// Retry re-runs a failed (or completed) analysis.
func (s *RSIService) Retry(ctx context.Context, sessionID string) error {
	res, err := s.st.DB().ExecContext(ctx,
		`UPDATE training_session SET status='analyzing', error='', finished_at='' WHERE id=? AND status IN ('failed','ready')`,
		sessionID)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return NewValidationError("session is not retryable (only failed/ready sessions can be re-analyzed)")
	}
	go s.runAnalysis(sessionID)
	return nil
}

// ListSessions returns the Training list, newest first.
func (s *RSIService) ListSessions(ctx context.Context, limit int) ([]RsiSession, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT ts.id,ts.goal_id,g.title,COALESCE(g.domain_id,''),ts.status,ts.content_capture,ts.sample_rate,ts.budget_usd,
		        ts.trace_count,ts.cost_usd,ts.error,ts.started_at,ts.sealed_at,ts.finished_at,
		        (SELECT COUNT(*) FROM rsi_finding f JOIN rsi_report rp ON f.report_id=rp.id WHERE rp.session_id=ts.id)
		 FROM training_session ts JOIN goal g ON g.id=ts.goal_id
		 ORDER BY ts.started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RsiSession{}
	for rows.Next() {
		var x RsiSession
		if err := rows.Scan(&x.ID, &x.GoalID, &x.GoalTitle, &x.DomainID, &x.Status, &x.ContentCapture, &x.SampleRate, &x.BudgetUSD,
			&x.TraceCount, &x.CostUSD, &x.Error, &x.StartedAt, &x.SealedAt, &x.FinishedAt, &x.FindingCount); err != nil {
			return nil, err
		}
		out = append(out, x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	for i := range out {
		_ = s.st.DB().QueryRowContext(ctx,
			`SELECT f.category,f.title FROM rsi_finding f JOIN rsi_report rp ON f.report_id=rp.id
			 WHERE rp.session_id=? ORDER BY f.score DESC LIMIT 1`, out[i].ID).
			Scan(&out[i].TopCategory, &out[i].TopTitle)
	}
	return out, nil
}

// GetSession returns one session with its report, findings, and spans.
func (s *RSIService) GetSession(ctx context.Context, id string) (*RsiSession, *RsiReport, []RsiFinding, []RsiSpan, error) {
	var x RsiSession
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT ts.id,ts.goal_id,g.title,COALESCE(g.domain_id,''),ts.status,ts.content_capture,ts.sample_rate,ts.budget_usd,
		        ts.trace_count,ts.cost_usd,ts.error,ts.started_at,ts.sealed_at,ts.finished_at,
		        (SELECT COUNT(*) FROM rsi_finding f JOIN rsi_report rp ON f.report_id=rp.id WHERE rp.session_id=ts.id)
		 FROM training_session ts JOIN goal g ON g.id=ts.goal_id WHERE ts.id=?`, id).
		Scan(&x.ID, &x.GoalID, &x.GoalTitle, &x.DomainID, &x.Status, &x.ContentCapture, &x.SampleRate, &x.BudgetUSD,
			&x.TraceCount, &x.CostUSD, &x.Error, &x.StartedAt, &x.SealedAt, &x.FinishedAt, &x.FindingCount)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, nil, nil, err
	}
	var report *RsiReport
	{
		var r RsiReport
		var metrics, baseline string
		err := s.st.DB().QueryRowContext(ctx,
			`SELECT id,session_id,status,summary,metrics_json,baseline_json,narrative,error,created_at,finished_at
			 FROM rsi_report WHERE session_id=?`, id).
			Scan(&r.ID, &r.SessionID, &r.Status, &r.Summary, &metrics, &baseline, &r.Narrative, &r.Error, &r.CreatedAt, &r.FinishedAt)
		if err == nil {
			r.Metrics = decodeMap(metrics)
			r.Baseline = decodeMap(baseline)
			report = &r
		} else if !errors.Is(err, sql.ErrNoRows) {
			return nil, nil, nil, nil, err
		}
	}
	findings := []RsiFinding{}
	if report != nil {
		rows, err := s.st.DB().QueryContext(ctx,
			`SELECT id,report_id,category,severity,confidence,title,explanation,suggestion,evidence_json,score,status,created_at
			 FROM rsi_finding WHERE report_id=? ORDER BY score DESC, created_at ASC`, report.ID)
		if err != nil {
			return nil, nil, nil, nil, err
		}
		defer rows.Close()
		for rows.Next() {
			var f RsiFinding
			var ev string
			if err := rows.Scan(&f.ID, &f.ReportID, &f.Category, &f.Severity, &f.Confidence, &f.Title, &f.Explanation, &f.Suggestion, &ev, &f.Score, &f.Status, &f.CreatedAt); err != nil {
				return nil, nil, nil, nil, err
			}
			f.Evidence = decodeStrings(ev)
			findings = append(findings, f)
		}
		if err := rows.Err(); err != nil {
			return nil, nil, nil, nil, err
		}
	}
	spans, err := s.spansFor(ctx, id)
	if err != nil {
		return nil, nil, nil, nil, err
	}
	return &x, report, findings, spans, nil
}

func (s *RSIService) spansFor(ctx context.Context, sessionID string) ([]RsiSpan, error) {
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id,session_id,run_id,span_id,parent_span_id,kind,name,started_at_ms,ended_at_ms,duration_ms,status,error_class,
		        retry_count,tool_name,args_hash,tokens_in,tokens_out,cost_usd,context_used,context_size,content_bytes,attrs_json,created_at
		 FROM trace_span WHERE session_id=? ORDER BY started_at_ms ASC, kind ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RsiSpan{}
	for rows.Next() {
		var x RsiSpan
		var attrs string
		if err := rows.Scan(&x.ID, &x.SessionID, &x.RunID, &x.SpanID, &x.ParentID, &x.Kind, &x.Name, &x.StartedAt, &x.EndedAt, &x.DurationMS,
			&x.Status, &x.ErrorClass, &x.RetryCount, &x.ToolName, &x.ArgsHash, &x.TokensIn, &x.TokensOut, &x.CostUSD,
			&x.ContextUsed, &x.ContextSize, &x.ContentSize, &attrs, &x.CreatedAt); err != nil {
			return nil, err
		}
		x.Attrs = decodeMap(attrs)
		out = append(out, x)
	}
	return out, rows.Err()
}

// UpdateFinding sets a finding's triage status (confirmed/dismissed/open).
func (s *RSIService) UpdateFinding(ctx context.Context, id, status string) error {
	if status != "confirmed" && status != "dismissed" && status != "open" {
		return NewValidationError("status must be confirmed, dismissed, or open")
	}
	res, err := s.st.DB().ExecContext(ctx, `UPDATE rsi_finding SET status=? WHERE id=?`, status, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// Overview aggregates the Training strip. days<=0 means all time.
func (s *RSIService) Overview(ctx context.Context, days int) (RsiOverview, error) {
	var out RsiOverview
	where := ""
	args := []any{}
	if days > 0 {
		where = " WHERE ts.started_at >= ?"
		args = append(args, time.Now().AddDate(0, 0, -days).UTC().Format(time.RFC3339Nano))
	}
	var success, total int
	var cost float64
	var leadTimes []int64
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT g.status,ts.cost_usd,ts.started_at,ts.finished_at,ts.status
		 FROM training_session ts JOIN goal g ON g.id=ts.goal_id`+where, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var gs, ss, start, finish string
		var c float64
		if err := rows.Scan(&gs, &c, &start, &finish, &ss); err != nil {
			return out, err
		}
		if ss == "skipped" {
			continue
		}
		total++
		cost += c
		if gs == "done" {
			success++
		}
		if t, ok := parseMS(start); ok && finish != "" {
			if e, ok2 := parseMS(finish); ok2 && e > t {
				leadTimes = append(leadTimes, e-t)
			}
		}
	}
	if err := rows.Err(); err != nil {
		return out, err
	}
	out.Sessions = total
	if total > 0 {
		out.SuccessRate = float64(success) / float64(total)
	}
	if success > 0 {
		out.CostPerSuccess = cost / float64(success)
	}
	out.P95LeadTimeMS = percentile95(leadTimes)
	_ = s.st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(SUM(review_duration),0)*1000 FROM gate_decision`).Scan(&out.HumanWaitMS)
	_ = s.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rsi_finding f JOIN rsi_report rp ON f.report_id=rp.id JOIN training_session ts ON ts.id=rp.session_id`+strings.Replace(where, "ts.started_at", "ts.started_at", 1)).Scan(&out.Findings)
	return out, nil
}

func decodeMap(s string) map[string]any {
	m := map[string]any{}
	if s == "" {
		return m
	}
	_ = json.Unmarshal([]byte(s), &m)
	return m
}

func decodeStrings(s string) []string {
	out := []string{}
	if s == "" {
		return out
	}
	_ = json.Unmarshal([]byte(s), &out)
	return out
}

func percentile95(v []int64) int64 {
	if len(v) == 0 {
		return 0
	}
	cp := append([]int64(nil), v...)
	sort.Slice(cp, func(i, j int) bool { return cp[i] < cp[j] })
	idx := int(float64(len(cp)-1) * 0.95)
	return cp[idx]
}
