package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"sort"
	"sync"
	"time"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/logging"
	"github.com/eushing/agentwork/internal/store"
)

// W-RSI (2026-11-08): the training loop observes the ORCHESTRATION LAYER,
// not the agent internals. Inference-mode goals are never touched. A
// train-mode goal gets a training session; when the goal reaches a terminal
// state the session is sealed, the workflow graph is frozen read-time from
// the entity tables (workflow.go), the frozen copy is stored in
// rsi_report.workflow_json, and a deterministic structure analysis emits
// reviewable findings. RSI never changes executing behavior by itself — the
// human decides what to adopt.
type RsiSession struct {
	ID              string  `json:"id"`
	GoalID          string  `json:"goal_id"`
	GoalTitle       string  `json:"goal_title"`
	DomainID        string  `json:"domain_id"`
	Status          string  `json:"status"` // waiting|analyzing|ready|failed|skipped
	ContentCapture  string  `json:"content_capture"`
	SampleRate      float64 `json:"sample_rate"`
	BudgetUSD       float64 `json:"budget_usd"`
	NodeCount       int     `json:"node_count"`
	CriticalPathMS  int64   `json:"critical_path_ms"`
	ParallelSpeedup float64 `json:"parallel_speedup"`
	FindingCount    int     `json:"finding_count"`
	TopCategory     string  `json:"top_category"`
	TopTitle        string  `json:"top_title"`
	Error           string  `json:"error"`
	StartedAt       string  `json:"started_at"`
	SealedAt        string  `json:"sealed_at"`
	FinishedAt      string  `json:"finished_at"`
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
	Sessions           int             `json:"sessions"`
	SuccessRate        float64         `json:"success_rate"`
	P95LeadTimeMS      int64           `json:"p95_lead_time_ms"`
	HumanWaitMS        int64           `json:"human_wait_ms"`
	AvgParallelSpeedup float64         `json:"avg_parallel_speedup"`
	AvgVerifyOverhead  float64         `json:"avg_verify_overhead_ratio"`
	AvgReworkRatio     float64         `json:"avg_rework_ratio"`
	Findings           int             `json:"findings"`
	Trend              []RsiTrendPoint `json:"trend"`
}

// RsiTrendPoint is one insight-panel data point: a finished optimization
// round, oldest first. Charts read this — no separate snapshot table.
type RsiTrendPoint struct {
	RoundID             string  `json:"round_id"`
	SessionID           string  `json:"session_id"`
	GoalID              string  `json:"goal_id"`
	GoalTitle           string  `json:"goal_title"`
	RoundNo             int     `json:"round_no"`
	CreatedAt           string  `json:"created_at"`
	ParallelSpeedup     float64 `json:"parallel_speedup"`
	CriticalPathMS      int64   `json:"critical_path_ms"`
	ReworkRatio         float64 `json:"rework_ratio"`
	VerifyOverhead      float64 `json:"verify_overhead_ratio"`
	SerializationLossMS int64   `json:"serialization_loss_ms"`
	FindingCount        int     `json:"finding_count"`
}

// RsiRound is one frozen optimization round in the ledger (rsi_round).
// Metrics/baseline/findings are copies taken at round time; delta compares
// this round against the previous one (empty for round 1).
type RsiRound struct {
	ID            string         `json:"id"`
	SessionID     string         `json:"session_id"`
	GoalID        string         `json:"goal_id"`
	RoundNo       int            `json:"round_no"`
	Trigger       string         `json:"trigger"` // terminal | manual_retry
	Metrics       map[string]any `json:"metrics"`
	Baseline      map[string]any `json:"baseline"`
	Delta         map[string]any `json:"delta"`
	FindingCount  int            `json:"finding_count"`
	CriticalCount int            `json:"critical_count"`
	TopCategory   string         `json:"top_category"`
	TopTitle      string         `json:"top_title"`
	Findings      []RsiFinding   `json:"findings"`
	Summary       string         `json:"summary"`
	Narrative     string         `json:"narrative"`
	CreatedAt     string         `json:"created_at"`
}

// RSIService owns training-session bookkeeping and the deterministic
// workflow analyzer. One instance is shared by the daemon (terminal hook)
// and the HTTP handler (read APIs). There is no live collection path: the
// entity tables are already the record.
type RSIService struct {
	st  *store.Store
	bus *events.Bus

	// analyzeMu serializes analyses: analysis is fast (SQL + arithmetic) and
	// a single lock keeps a retry from racing a terminal trigger.
	analyzeMu sync.Mutex
}

func NewRSIService(st *store.Store, bus *events.Bus) *RSIService {
	return &RSIService{st: st, bus: bus}
}

// EnsureSession creates the training session for a train-mode goal if it
// does not exist. Sampling happens ONCE here: a sampled-out goal gets status
// 'skipped' and is never analyzed.
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

// OnGoalTerminal seals the session and launches the analyzer in the
// background. Called AFTER the reconcile transaction commits, so the
// analysis always sees the goal's terminal status.
func (s *RSIService) OnGoalTerminal(ctx context.Context, goalID string) {
	var sid, status string
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT id,status FROM training_session WHERE goal_id=? ORDER BY started_at DESC LIMIT 1`, goalID).
		Scan(&sid, &status)
	if errors.Is(err, sql.ErrNoRows) {
		return // inference goal or pre-RSI goal: nothing to analyze
	}
	if err != nil {
		logging.Infof("rsi: terminal lookup %s: %v", goalID, err)
		return
	}
	if status == "skipped" {
		return
	}
	res, err := s.st.DB().ExecContext(ctx,
		`UPDATE training_session SET status='analyzing', sealed_at=? WHERE id=? AND status IN ('waiting','analyzing')`, now(), sid)
	if err != nil {
		logging.Infof("rsi: seal session %s: %v", sid, err)
		return
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return // already analyzing/ready/failed
	}
	go s.runAnalysis(sid)
}

func (s *RSIService) runAnalysis(sessionID string) {
	defer func() {
		if r := recover(); r != nil {
			logging.Errorf("rsi: analysis panic for %s: %v", sessionID, r)
		}
	}()
	s.analyzeMu.Lock()
	defer s.analyzeMu.Unlock()
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
	var status string
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT status FROM training_session WHERE id=?`, sessionID).Scan(&status); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	// waiting is retryable: covers a goal that reached a terminal state
	// before the RSI hook existed (backfill), and re-analysis on demand.
	if status != "failed" && status != "ready" && status != "waiting" {
		return NewValidationError("session is not retryable (only waiting/failed/ready sessions can be re-analyzed)")
	}
	if _, err := s.st.DB().ExecContext(ctx,
		`UPDATE training_session SET status='analyzing', error='', finished_at='' WHERE id=?`, sessionID); err != nil {
		return err
	}
	go s.runAnalysis(sessionID)
	return nil
}

const sessionColumns = `ts.id,ts.goal_id,g.title,COALESCE(g.domain_id,''),ts.status,ts.content_capture,ts.sample_rate,ts.budget_usd,
	COALESCE(ts.node_count,0),COALESCE(ts.critical_path_ms,0),COALESCE(ts.parallel_speedup,0),
	ts.error,ts.started_at,ts.sealed_at,ts.finished_at,
	(SELECT COUNT(*) FROM rsi_finding f JOIN rsi_report rp ON f.report_id=rp.id WHERE rp.session_id=ts.id)`

func scanSession(sc func(...any) error) (*RsiSession, error) {
	var x RsiSession
	if err := sc(&x.ID, &x.GoalID, &x.GoalTitle, &x.DomainID, &x.Status, &x.ContentCapture, &x.SampleRate, &x.BudgetUSD,
		&x.NodeCount, &x.CriticalPathMS, &x.ParallelSpeedup,
		&x.Error, &x.StartedAt, &x.SealedAt, &x.FinishedAt, &x.FindingCount); err != nil {
		return nil, err
	}
	return &x, nil
}

// ListSessions returns the Training list, newest first.
func (s *RSIService) ListSessions(ctx context.Context, limit int) ([]RsiSession, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT `+sessionColumns+` FROM training_session ts JOIN goal g ON g.id=ts.goal_id
		 ORDER BY ts.started_at DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RsiSession{}
	for rows.Next() {
		x, err := scanSession(rows.Scan)
		if err != nil {
			return nil, err
		}
		out = append(out, *x)
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

// GetSession returns one session with its report, findings, and the
// orchestration graph. The graph is the frozen analysis-time copy when a
// report exists, otherwise a live build (so a waiting session already shows
// the DAG forming).
func (s *RSIService) GetSession(ctx context.Context, id string) (*RsiSession, *RsiReport, []RsiFinding, *WorkflowGraph, error) {
	x, err := scanSession(func(dst ...any) error {
		return s.st.DB().QueryRowContext(ctx,
			`SELECT `+sessionColumns+` FROM training_session ts JOIN goal g ON g.id=ts.goal_id WHERE ts.id=?`, id).
			Scan(dst...)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, nil, nil, ErrNotFound
	}
	if err != nil {
		return nil, nil, nil, nil, err
	}
	// Fill the list/detail headline finding here too. ListSessions did this
	// per row, but GetSession never did — the detail page's top-finding block
	// was permanently empty.
	_ = s.st.DB().QueryRowContext(ctx,
		`SELECT f.category,f.title FROM rsi_finding f JOIN rsi_report rp ON f.report_id=rp.id
		 WHERE rp.session_id=? ORDER BY f.score DESC LIMIT 1`, id).
		Scan(&x.TopCategory, &x.TopTitle)
	var report *RsiReport
	var frozen *WorkflowGraph
	{
		var r RsiReport
		var metrics, baseline, wf string
		err := s.st.DB().QueryRowContext(ctx,
			`SELECT id,session_id,status,summary,metrics_json,baseline_json,workflow_json,narrative,error,created_at,finished_at
			 FROM rsi_report WHERE session_id=?`, id).
			Scan(&r.ID, &r.SessionID, &r.Status, &r.Summary, &metrics, &baseline, &wf, &r.Narrative, &r.Error, &r.CreatedAt, &r.FinishedAt)
		if err == nil {
			r.Metrics = decodeMap(metrics)
			r.Baseline = decodeMap(baseline)
			report = &r
			frozen = decodeWorkflow(wf)
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
	graph := frozen
	if graph == nil && x.Status != "skipped" {
		graph, err = s.BuildGoalWorkflow(ctx, x.GoalID)
		if err != nil {
			return nil, nil, nil, nil, err
		}
	}
	return x, report, findings, graph, nil
}

func decodeWorkflow(s string) *WorkflowGraph {
	if s == "" || s == "{}" {
		return nil
	}
	var g WorkflowGraph
	if err := json.Unmarshal([]byte(s), &g); err != nil {
		return nil
	}
	return &g
}

// GoalWorkflow builds the live orchestration graph for a goal (read API for
// the goal detail view; no training session required).
func (s *RSIService) GoalWorkflow(ctx context.Context, goalID string) (*WorkflowGraph, error) {
	graph, err := s.BuildGoalWorkflow(ctx, goalID)
	if err != nil {
		// Unknown goal id → 404, not a 500 with a raw driver error.
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, err
	}
	return graph, nil
}

// SquadWorkflows returns compact workflow summaries for the most recent
// goals assigned to a squad — the "orchestration visibility" the squad page
// never had.
func (s *RSIService) SquadWorkflows(ctx context.Context, squadID string, limit int) ([]WorkflowGoalSummary, error) {
	if squadID == "" {
		return nil, NewValidationError("squad id is required")
	}
	if limit <= 0 || limit > 50 {
		limit = 10
	}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id FROM goal WHERE assignee_type='squad' AND assignee_id=?
		 ORDER BY created_at DESC LIMIT ?`, squadID, limit)
	if err != nil {
		return nil, err
	}
	ids := []string{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	out := []WorkflowGoalSummary{}
	for _, id := range ids {
		g, err := s.BuildGoalWorkflow(ctx, id)
		if err != nil {
			continue // a torn-down goal must not break the squad page
		}
		out = append(out, WorkflowGoalSummaryFromGraph(g))
	}
	return out, nil
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
	var leadTimes []int64
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT g.status,ts.started_at,ts.finished_at,ts.status
		 FROM training_session ts JOIN goal g ON g.id=ts.goal_id`+where, args...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		var gs, ss, start, finish string
		if err := rows.Scan(&gs, &start, &finish, &ss); err != nil {
			return out, err
		}
		if ss == "skipped" {
			continue
		}
		total++
		if gs == "done" {
			success++
		}
		if t, ok := parseTimeMS(start); ok && finish != "" {
			if e, ok2 := parseTimeMS(finish); ok2 && e > t {
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
	out.P95LeadTimeMS = percentile95(leadTimes)

	// Human wait: gate review seconds (all goals, not only train sessions).
	hwArgs := []any{}
	hwWhere := ""
	if days > 0 {
		hwWhere = " WHERE decided_at >= ?"
		hwArgs = append(hwArgs, args[len(args)-1])
	}
	_ = s.st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(SUM(review_duration),0)*1000 FROM gate_decision`+hwWhere, hwArgs...).Scan(&out.HumanWaitMS)

	// Workflow averages over ready reports in the window.
	mrows, err := s.st.DB().QueryContext(ctx,
		`SELECT rp.metrics_json FROM rsi_report rp JOIN training_session ts ON ts.id=rp.session_id
		 WHERE rp.status='ready'`+where, args...)
	if err == nil {
		var nSpeed, nOver, nRework int
		var sumSpeed, sumOver, sumRework float64
		for mrows.Next() {
			var mj string
			if err := mrows.Scan(&mj); err != nil {
				continue
			}
			m := decodeMap(mj)
			if v, ok := num(m["parallel_speedup"]); ok {
				sumSpeed += v
				nSpeed++
			}
			if v, ok := num(m["verify_overhead_ratio"]); ok {
				sumOver += v
				nOver++
			}
			if v, ok := num(m["rework_ratio"]); ok {
				sumRework += v
				nRework++
			}
		}
		mrows.Close()
		if nSpeed > 0 {
			out.AvgParallelSpeedup = sumSpeed / float64(nSpeed)
		}
		if nOver > 0 {
			out.AvgVerifyOverhead = sumOver / float64(nOver)
		}
		if nRework > 0 {
			out.AvgReworkRatio = sumRework / float64(nRework)
		}
	}

	_ = s.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rsi_finding f JOIN rsi_report rp ON f.report_id=rp.id JOIN training_session ts ON ts.id=rp.session_id`+where, args...).Scan(&out.Findings)

	// Insight-panel trend: every finished optimization round, oldest first.
	// Rounds — not sessions — are the unit of improvement, so a re-analysis
	// after applying a fix lands as its own point.
	out.Trend = []RsiTrendPoint{}
	tq := `SELECT r.id,r.session_id,r.goal_id,COALESCE(g.title,''),r.round_no,r.created_at,r.metrics_json,r.finding_count
	       FROM rsi_round r LEFT JOIN goal g ON g.id=r.goal_id`
	var targs []any
	if days > 0 {
		tq += ` WHERE r.created_at >= ?`
		targs = append(targs, args[len(args)-1])
	}
	tq += ` ORDER BY r.created_at ASC`
	if trows, err := s.st.DB().QueryContext(ctx, tq, targs...); err == nil {
		for trows.Next() {
			var p RsiTrendPoint
			var mj string
			if err := trows.Scan(&p.RoundID, &p.SessionID, &p.GoalID, &p.GoalTitle, &p.RoundNo, &p.CreatedAt, &mj, &p.FindingCount); err != nil {
				continue
			}
			m := decodeMap(mj)
			if v, ok := num(m["parallel_speedup"]); ok {
				p.ParallelSpeedup = v
			}
			if v, ok := m["critical_path_ms"].(float64); ok {
				p.CriticalPathMS = int64(v)
			} else if v, ok := m["critical_path_ms"].(int64); ok {
				p.CriticalPathMS = v
			}
			if v, ok := num(m["rework_ratio"]); ok {
				p.ReworkRatio = v
			}
			if v, ok := num(m["verify_overhead_ratio"]); ok {
				p.VerifyOverhead = v
			}
			if v, ok := num(m["serialization_loss_ms"]); ok {
				p.SerializationLossMS = int64(v)
			}
			out.Trend = append(out.Trend, p)
		}
		trows.Close()
	}
	return out, nil
}

// roundColumns mirrors the SELECT list in ListRounds.
const roundColumns = `id,session_id,goal_id,round_no,trigger,metrics_json,baseline_json,delta_json,
 finding_count,critical_count,top_category,top_title,findings_json,summary,narrative,created_at`

// ListRounds returns the full optimization-round ledger for a session,
// oldest first — the insight panel's timeline.
func (s *RSIService) ListRounds(ctx context.Context, sessionID string) ([]RsiRound, error) {
	if sessionID == "" {
		return nil, NewValidationError("session id is required")
	}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT `+roundColumns+` FROM rsi_round WHERE session_id=? ORDER BY round_no ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []RsiRound{}
	for rows.Next() {
		var r RsiRound
		var mj, bj, dj, fj string
		if err := rows.Scan(&r.ID, &r.SessionID, &r.GoalID, &r.RoundNo, &r.Trigger,
			&mj, &bj, &dj, &r.FindingCount, &r.CriticalCount, &r.TopCategory, &r.TopTitle,
			&fj, &r.Summary, &r.Narrative, &r.CreatedAt); err != nil {
			return nil, err
		}
		r.Metrics = decodeMap(mj)
		r.Baseline = decodeMap(bj)
		r.Delta = decodeMap(dj)
		r.Findings = []RsiFinding{}
		_ = json.Unmarshal([]byte(fj), &r.Findings)
		out = append(out, r)
	}
	return out, rows.Err()
}

func num(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case int64:
		return float64(x), true
	case int:
		return float64(x), true
	}
	return 0, false
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
