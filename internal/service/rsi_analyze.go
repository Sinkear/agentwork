package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/eushing/agentwork/internal/events"
	"github.com/eushing/agentwork/internal/logging"
)

// analyzeRun is one execution segment considered by the analyzer.
type analyzeRun struct {
	ID         string
	AgentID    string
	Role       string
	Status     string
	Attempt    int
	StartedMS  int64
	FinishedMS int64
	Summary    string
}

type spanRec struct {
	ID       string
	Kind     string
	Name     string
	Tool     string
	ArgsHash string
	StartMS  int64
	EndMS    int64
	Duration int64
	Status   string
	Cost     float64
	Used     int
	Size     int
}

// Analyze computes the deterministic report and findings for one session,
// rewrites the derived run/verify/gate spans, and marks the session ready.
// It is idempotent: a retry replaces the previous report.
func (s *RSIService) Analyze(ctx context.Context, sessionID string) error {
	s.analyzeMu.Lock()
	defer s.analyzeMu.Unlock()

	var goalID, status, startedAt string
	err := s.st.DB().QueryRowContext(ctx,
		`SELECT goal_id,status,started_at FROM training_session WHERE id=?`, sessionID).
		Scan(&goalID, &status, &startedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	var goalTitle, goalStatus, goalCreated string
	err = s.st.DB().QueryRowContext(ctx,
		`SELECT title,status,created_at FROM goal WHERE id=?`, goalID).
		Scan(&goalTitle, &goalStatus, &goalCreated)
	if errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("goal %s vanished", goalID)
	}
	if err != nil {
		return err
	}

	// Load runs (worker runs only — processor runs are compile/intake tasks,
	// not the goal's execution).
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id,COALESCE(agent_id,''),COALESCE(role,''),status,attempt,
		        COALESCE(started_at,''),COALESCE(finished_at,''),COALESCE(result_summary,'')
		 FROM run WHERE goal_id=? AND goal_id<>'' AND COALESCE(run_kind,'worker')='worker'
		 ORDER BY created_at ASC`, goalID)
	if err != nil {
		return err
	}
	defer rows.Close()
	var runs []analyzeRun
	for rows.Next() {
		var r analyzeRun
		var st, fin string
		if err := rows.Scan(&r.ID, &r.AgentID, &r.Role, &r.Status, &r.Attempt, &st, &fin, &r.Summary); err != nil {
			return err
		}
		r.StartedMS, _ = parseMS(st)
		r.FinishedMS, _ = parseMS(fin)
		runs = append(runs, r)
	}
	if err := rows.Err(); err != nil {
		return err
	}

	existing, err := s.spansFor(ctx, sessionID)
	if err != nil {
		return err
	}
	var toolSpans []spanRec
	for _, sp := range existing {
		if sp.Kind == "tool" || sp.Kind == "llm" {
			toolSpans = append(toolSpans, spanRec{ID: sp.ID, Kind: sp.Kind, Name: sp.Name, Tool: sp.ToolName,
				ArgsHash: sp.ArgsHash, StartMS: sp.StartedAt, EndMS: sp.EndedAt, Duration: sp.DurationMS,
				Status: sp.Status, Cost: sp.CostUSD, Used: sp.ContextUsed, Size: sp.ContextSize})
		}
	}

	// Human checkpoint decisions (waiting time is its own span).
	type gateRec struct {
		Rule, Decision, Reason, DecidedBy, At string
		DurationS                             int64
	}
	var gates []gateRec
	grows, err := s.st.DB().QueryContext(ctx,
		`SELECT COALESCE(gate_rule,''),COALESCE(decision,''),COALESCE(reason,''),COALESCE(decided_by,''),COALESCE(decided_at,''),COALESCE(review_duration,0)
		 FROM gate_decision WHERE goal_id=? ORDER BY decided_at ASC`, goalID)
	if err != nil {
		return err
	}
	for grows.Next() {
		var g gateRec
		if err := grows.Scan(&g.Rule, &g.Decision, &g.Reason, &g.DecidedBy, &g.At, &g.DurationS); err != nil {
			grows.Close()
			return err
		}
		gates = append(gates, g)
	}
	grows.Close()

	// Verification results are labels, not costs.
	type verifyRec struct {
		RunID, Status, Summary, At string
	}
	var verifies []verifyRec
	vrows, err := s.st.DB().QueryContext(ctx,
		`SELECT COALESCE(verifier_run_id,''),COALESCE(status,''),COALESCE(summary,''),COALESCE(created_at,'')
		 FROM verification_result WHERE goal_id=? ORDER BY created_at ASC`, goalID)
	if err != nil {
		return err
	}
	for vrows.Next() {
		var v verifyRec
		if err := vrows.Scan(&v.RunID, &v.Status, &v.Summary, &v.At); err != nil {
			vrows.Close()
			return err
		}
		verifies = append(verifies, v)
	}
	vrows.Close()

	// ── Derived spans (idempotent rewrite) ──
	if _, err := s.st.DB().ExecContext(ctx,
		`DELETE FROM trace_span WHERE session_id=? AND kind IN ('run','verify','gate','human_wait')`, sessionID); err != nil {
		return err
	}
	for _, r := range runs {
		st := "ok"
		switch r.Status {
		case "failed":
			st = "error"
		case "cancelled":
			st = "cancelled"
		case "running", "queued":
			st = "running"
		}
		start := r.StartedMS
		if start == 0 {
			start = r.FinishedMS
		}
		attrs := map[string]any{"agent_id": r.AgentID, "role": r.Role, "attempt": r.Attempt}
		if r.Summary != "" && len(r.Summary) <= 500 {
			attrs["summary"] = r.Summary
		}
		s.insertDerivedSpan(ctx, sessionID, r.ID, r.ID+":run", "", "run", firstNonEmpty(r.Role, "run"), start, r.FinishedMS, st, "", maxInt(0, r.Attempt-1), attrs)
	}
	for _, g := range gates {
		if g.At == "" {
			continue
		}
		at, ok := parseMS(g.At)
		if !ok {
			continue
		}
		start := at - g.DurationS*1000
		if start < 0 {
			start = at
		}
		s.insertDerivedSpan(ctx, sessionID, "", "gate:"+g.Rule+":"+g.At, "", "gate", g.Rule, start, at, g.Decision, "", 0,
			map[string]any{"decision": g.Decision, "reason": g.Reason, "decided_by": g.DecidedBy})
		s.insertDerivedSpan(ctx, sessionID, "", "wait:"+g.Rule+":"+g.At, "gate:"+g.Rule+":"+g.At, "human_wait", g.Rule, start, at, "ok", "", 0,
			map[string]any{"duration_s": g.DurationS, "decision": g.Decision})
	}
	for _, v := range verifies {
		at, _ := parseMS(v.At)
		st := "ok"
		if v.Status == "rejected" {
			st = "error"
		}
		s.insertDerivedSpan(ctx, sessionID, v.RunID, firstNonEmpty(v.RunID+":verify", "verify:"+v.At), "", "verify", v.Status, at, at, st, "", 0,
			map[string]any{"summary": v.Summary})
	}

	// ── Metrics ──
	startMS, _ := parseMS(startedAt)
	if startMS == 0 {
		startMS, _ = parseMS(goalCreated)
	}
	endMS := int64(0)
	for _, r := range runs {
		if r.StartedMS > 0 && (startMS == 0 || r.StartedMS < startMS) {
			startMS = r.StartedMS
		}
		if r.FinishedMS > endMS {
			endMS = r.FinishedMS
		}
	}
	for _, g := range gates {
		if at, ok := parseMS(g.At); ok && at > endMS {
			endMS = at
		}
	}
	for _, v := range verifies {
		if at, ok := parseMS(v.At); ok && at > endMS {
			endMS = at
		}
	}
	if endMS == 0 {
		endMS = time.Now().UnixMilli()
	}
	if endMS < startMS {
		endMS = startMS
	}
	wallMS := endMS - startMS
	if wallMS <= 0 {
		wallMS = 1
	}

	var intervals [][2]int64
	var sumRunMS, failedMS int64
	var failedRuns, retryRuns, retryCount int
	for _, r := range runs {
		if r.StartedMS > 0 && r.FinishedMS > r.StartedMS {
			intervals = append(intervals, [2]int64{r.StartedMS, r.FinishedMS})
			sumRunMS += r.FinishedMS - r.StartedMS
		}
		if r.Status == "failed" {
			failedRuns++
			if r.FinishedMS > r.StartedMS {
				failedMS += r.FinishedMS - r.StartedMS
			}
		}
		if r.Attempt > 1 {
			retryRuns++
			retryCount += r.Attempt - 1
		}
	}
	activeMS := unionMS(intervals)
	if activeMS == 0 {
		activeMS = wallMS
	}

	var toolMS, cost float64
	var toolCalls int
	contextPeak := 0.0
	var tokensIn, tokensOut int
	dupExtraCalls := 0
	var dupExtraMS int64
	groups := map[string][]spanRec{}
	for _, sp := range toolSpans {
		if sp.Kind == "tool" {
			toolMS += float64(sp.Duration)
			toolCalls++
			if sp.Tool != "" && sp.ArgsHash != "" {
				key := sp.Tool + "|" + sp.ArgsHash
				groups[key] = append(groups[key], sp)
			}
		}
		cost += sp.Cost
		tokensIn += 0
		tokensOut += 0
		if sp.Size > 0 {
			r := float64(sp.Used) / float64(sp.Size)
			if r > contextPeak {
				contextPeak = r
			}
		}
	}
	var dupNames []string
	for key, list := range groups {
		if len(list) > 1 {
			dupExtraCalls += len(list) - 1
			for _, sp := range list[1:] {
				dupExtraMS += sp.Duration
			}
			if len(dupNames) < 3 {
				dupNames = append(dupNames, fmt.Sprintf("%s ×%d", firstNonEmpty(list[0].Tool, key), len(list)))
			}
		}
	}
	humanMS := int64(0)
	for _, g := range gates {
		humanMS += g.DurationS * 1000
	}
	// Human waiting can overlap a run (a gate decided while other runs are
	// still active); subtract it from active time so the three shares do not
	// double-count the same wall-clock second.
	llmMS := activeMS - int64(toolMS) - humanMS
	if llmMS < 0 {
		llmMS = 0
	}
	shares := func(v float64) float64 {
		if wallMS <= 0 {
			return 0
		}
		r := v / float64(wallMS)
		if r > 1 {
			r = 1
		}
		return r
	}
	toolShare := shares(toolMS)
	llmShare := shares(float64(llmMS))
	humanShare := shares(float64(humanMS))
	serialShare := 0.0
	if sumRunMS > 0 {
		serialShare = float64(activeMS) / float64(sumRunMS)
	}
	dupShare := 0.0
	if toolCalls > 0 {
		dupShare = float64(dupExtraCalls) / float64(toolCalls)
	}
	retryAmplification := 0.0
	if wallMS > 0 {
		retryAmplification = float64(failedMS) / float64(wallMS)
	}
	leadTimeMS := int64(0)
	if goalCreatedMS, ok := parseMS(goalCreated); ok && endMS > goalCreatedMS {
		leadTimeMS = endMS - goalCreatedMS
	}

	metrics := map[string]any{
		"wall_ms": wallMS, "active_ms": activeMS, "lead_time_ms": leadTimeMS,
		"run_count": len(runs), "step_count": toolCalls,
		"tool_ms": int64(toolMS), "tool_share": round3(toolShare),
		"llm_ms": llmMS, "llm_share": round3(llmShare),
		"human_wait_ms": humanMS, "human_share": round3(humanShare),
		"serial_share": round3(serialShare),
		"run_sum_ms":   sumRunMS,
		"retry_runs":   retryRuns, "retry_count": retryCount,
		"failed_runs": failedRuns, "retry_amplification": round3(retryAmplification),
		"duplicate_calls": dupExtraCalls, "duplicate_share": round3(dupShare),
		"context_peak_ratio": round3(contextPeak),
		"tokens_in":          tokensIn, "tokens_out": tokensOut,
		"cost_usd":        round3(cost),
		"usage_available": contextPeak > 0,
		"tool_calls":      toolCalls,
	}
	baseline := s.baseline(ctx, sessionID, metrics)

	// ── Findings ──
	gateDesc := ""
	if len(gates) > 0 {
		gateDesc = fmt.Sprintf("（%d 次人工审批）", len(gates))
	}
	findings := buildFindings(metrics, dupNames, gateDesc)
	for i := range findings {
		findings[i].Confidence = s.confidenceFor(ctx, sessionID, findings[i].Category)
	}
	summary := buildSummary(goalTitle, metrics, findings)

	// ── Persist report + findings atomically ──
	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx,
		`DELETE FROM rsi_finding WHERE report_id IN (SELECT id FROM rsi_report WHERE session_id=?)`, sessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM rsi_report WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	mb, _ := json.Marshal(metrics)
	bb, _ := json.Marshal(baseline)
	reportID := newID()
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO rsi_report (id,session_id,status,summary,metrics_json,baseline_json,narrative,error,created_at,finished_at)
		 VALUES (?,?,'ready',?,?,?,'','',?,?)`,
		reportID, sessionID, summary, string(mb), string(bb), now(), now()); err != nil {
		return err
	}
	for _, f := range findings {
		eb, _ := json.Marshal(f.Evidence)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO rsi_finding (id,report_id,category,severity,confidence,title,explanation,suggestion,evidence_json,score,status,created_at)
			 VALUES (?,?,?,?,?,?,?,?,?,?,'open',?)`,
			newID(), reportID, f.Category, f.Severity, f.Confidence, f.Title, f.Explanation, f.Suggestion, string(eb), f.Score, now()); err != nil {
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE training_session SET status='ready', error='', finished_at=?,
		   trace_count=(SELECT COUNT(*) FROM trace_span WHERE session_id=?),
		   cost_usd=(SELECT COALESCE(SUM(cost_usd),0) FROM trace_span WHERE session_id=?)
		 WHERE id=?`, now(), sessionID, sessionID, sessionID); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	if s.bus != nil {
		s.bus.Publish(ctx, events.Event{Topic: "rsi:ready", Payload: map[string]any{
			"session_id": sessionID, "goal_id": goalID, "findings": len(findings),
		}})
	}
	s.forgetRun("")
	return nil
}

// baseline compares this session against the median of the last 20 ready
// sessions. With fewer than 5 priors the deltas are omitted (they would be
// noise, not evidence).
func (s *RSIService) baseline(ctx context.Context, sessionID string, cur map[string]any) map[string]any {
	out := map[string]any{}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT rp.metrics_json FROM rsi_report rp JOIN training_session ts ON ts.id=rp.session_id
		 WHERE ts.id<>? AND rp.status='ready' ORDER BY rp.created_at DESC LIMIT 20`, sessionID)
	if err != nil {
		return out
	}
	defer rows.Close()
	keys := []string{"wall_ms", "tool_share", "llm_share", "human_share", "context_peak_ratio", "duplicate_share"}
	vals := map[string][]float64{}
	n := 0
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			continue
		}
		m := decodeMap(raw)
		n++
		for _, k := range keys {
			if v, ok := toFloat(m[k]); ok {
				vals[k] = append(vals[k], v)
			}
		}
	}
	out["sample_size"] = n
	if n < 5 {
		return out
	}
	out["sample_size"] = n
	for _, k := range keys {
		if len(vals[k]) == 0 {
			continue
		}
		med := median(vals[k])
		out[k+"_median"] = round3(med)
		if cv, ok := toFloat(cur[k]); ok && med > 0 {
			out[k+"_delta"] = round3((cv - med) / med)
		}
	}
	return out
}

func (s *RSIService) confidenceFor(ctx context.Context, sessionID, category string) string {
	var n int
	_ = s.st.DB().QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rsi_finding f JOIN rsi_report rp ON f.report_id=rp.id
		 JOIN training_session ts ON ts.id=rp.session_id
		 WHERE ts.id<>? AND f.category=? AND f.created_at >= ?`,
		sessionID, category, time.Now().AddDate(0, 0, -30).UTC().Format(time.RFC3339Nano)).Scan(&n)
	if n >= 2 {
		return "L2"
	}
	return "L1"
}

func (s *RSIService) insertDerivedSpan(ctx context.Context, sessionID, runID, spanID, parent, kind, name string, start, end int64, status, errClass string, retry int, attrs map[string]any) {
	if start == 0 {
		start = end
	}
	if end < start {
		end = start
	}
	ab, _ := json.Marshal(attrs)
	_, err := s.st.DB().ExecContext(ctx,
		`INSERT OR REPLACE INTO trace_span
		 (id,session_id,run_id,span_id,parent_span_id,kind,name,started_at_ms,ended_at_ms,duration_ms,status,error_class,retry_count,tool_name,args_hash,tokens_in,tokens_out,cost_usd,context_used,context_size,content_bytes,attrs_json,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?, '', '', 0,0,0,0,0,0,?,?)`,
		sessionID+"-"+spanID, sessionID, runID, spanID, parent, kind, name, start, end, end-start, status, errClass, retry, string(ab), now())
	if err != nil {
		loggingWarn("rsi: insert derived span %s: %v", spanID, err)
	}
}

func buildFindings(m map[string]any, dupNames []string, gateDesc string) []RsiFinding {
	var out []RsiFinding
	get := func(k string) float64 { v, _ := toFloat(m[k]); return v }
	add := func(cat, sev, title, expl, sug string, score float64, ev ...string) {
		if score > 1 {
			score = 1
		}
		score = round3(score)
		sevNow := sev
		if score >= 0.5 && sev == "warn" {
			sevNow = "critical"
		}
		out = append(out, RsiFinding{Category: cat, Severity: sevNow, Title: title, Explanation: expl, Suggestion: sug, Score: score, Evidence: ev})
	}
	toolShare, llmShare, humanShare := get("tool_share"), get("llm_share"), get("human_share")
	toolMS, llmMS := get("tool_ms"), get("llm_ms")
	humanMS := get("human_wait_ms")
	failedRuns, retryCount, retryAmp := get("failed_runs"), get("retry_count"), get("retry_amplification")
	dupCalls, dupShare := get("duplicate_calls"), get("duplicate_share")
	ctxPeak, serialShare := get("context_peak_ratio"), get("serial_share")
	runCount := get("run_count")
	steps := get("step_count")

	if toolShare >= 0.30 && toolMS >= 1000 {
		add("tool_slow", "warn", "工具调用占用了主要执行时间",
			fmt.Sprintf("工具耗时 %.1fs，占墙钟 %.0f%%；共 %.0f 次调用。", toolMS/1000, toolShare*100, steps),
			"按工具名排序定位最慢的调用；对重复的读/搜索做缓存，把可并行的工具调用并行化，或缩小返回内容。",
			toolShare*0.6, fmtEv("tool_ms", toolMS), fmtEv("tool_share", toolShare), fmtEv("steps", steps))
	}
	if llmShare >= 0.45 && llmMS >= 1000 {
		add("llm_slow", "warn", "模型思考/生成占比较高",
			fmt.Sprintf("扣掉工具后的 run 活跃时间约 %.1fs，占墙钟 %.0f%%。", llmMS/1000, llmShare*100),
			"检查是否把大段日志/文件读进了上下文；用更小的上下文、更明确的验收标准减少来回；必要时拆分 run。",
			llmShare*0.4, fmtEv("llm_ms", llmMS), fmtEv("llm_share", llmShare))
	}
	if failedRuns >= 1 && (retryAmp >= 0.20 || retryCount >= 2) {
		add("retries", "critical", "失败与重试放大了交付时间",
			fmt.Sprintf("失败 run %.0f 个，重试计数 %.0f，失败耗时占墙钟 %.0f%%。", failedRuns, retryCount, retryAmp*100),
			"先看失败 run 的错误分类：环境/权限失败重试无益，应前置检查；验收失败应补上下文，而不是原样重跑。",
			retryAmp*0.8, fmtEv("failed_runs", failedRuns), fmtEv("retry_amplification", retryAmp))
	}
	if ctxPeak >= 0.70 {
		add("context_pressure", "warn", "上下文接近上限",
			fmt.Sprintf("上下文峰值占用 %.0f%%（used/size）。", ctxPeak*100),
			"把长文件/日志改成按需读取或摘要；在 prompt 中明确「先检索再读全文」；考虑提高上下文预算或压缩历史。",
			ctxPeak*0.5, fmtEv("context_peak_ratio", ctxPeak))
	}
	if dupCalls >= 2 && dupShare >= 0.15 {
		ev := append([]string{fmtEv("duplicate_calls", dupCalls)}, dupNames...)
		add("duplicate_calls", "warn", "存在重复的工具调用",
			fmt.Sprintf("重复调用 %.0f 次，占调用总量 %.0f%%；相同工具+参数被重复执行。", dupCalls, dupShare*100),
			"在 prompt/策略中要求先读已有结果再调用；对幂等查询加短期缓存；合并同参数的连续调用。",
			dupShare*0.7, ev...)
	}
	if runCount > 1 && serialShare >= 0.70 && activeShareOf(m) >= 0.5 {
		add("serial_dependency", "info", "多段执行基本是串行的",
			fmt.Sprintf("%.0f 个 run 的活跃时间并集 / 总和 = %.0f%%，几乎没有并行。", runCount, serialShare*100),
			"确认是否存在可并行的子任务（独立模块/独立验证）；用 squad 并行或合并没有依赖的步骤，减少交接次数。",
			serialShare*0.5, fmtEv("serial_share", serialShare), fmtEv("run_count", runCount))
	}
	if humanShare >= 0.15 && humanMS >= 1000 {
		add("human_wait", "info", "人工审批/介入等待占比高",
			fmt.Sprintf("人工等待 %.1fs，占墙钟 %.0f%%%s。", humanMS/1000, humanShare*100, gateDesc),
			"对低风险改动放宽 gate 或改为抽样审批；把审批问题一次性问清；设置审批时限与自动升级。",
			humanShare*0.8, fmtEv("human_wait_ms", humanMS), fmtEv("human_share", humanShare))
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Score > out[j].Score })
	return out
}

func buildSummary(goalTitle string, m map[string]any, findings []RsiFinding) string {
	get := func(k string) float64 { v, _ := toFloat(m[k]); return v }
	wall := get("wall_ms") / 1000
	line := fmt.Sprintf("「%s」执行墙钟 %.1fs，%d 个 run、%d 次工具调用；工具占 %.0f%%、LLM 占 %.0f%%、人工等待占 %.0f%%。",
		goalTitle, wall, int(get("run_count")), int(get("step_count")), get("tool_share")*100, get("llm_share")*100, get("human_share")*100)
	if len(findings) == 0 {
		return line + " 未发现超过阈值的瓶颈；样本内执行结构较均衡。"
	}
	top := findings[0]
	return line + fmt.Sprintf(" 首要可优化项：%s（%s，score %.2f）。", top.Title, top.Confidence, top.Score)
}

func activeShareOf(m map[string]any) float64 {
	a, _ := toFloat(m["active_ms"])
	w, _ := toFloat(m["wall_ms"])
	if w <= 0 {
		return 0
	}
	return a / w
}

func fmtEv(k string, v float64) string {
	return fmt.Sprintf("%s=%.2f", k, v)
}

func parseMS(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	for _, layout := range []string{time.RFC3339Nano, time.RFC3339, "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t.UnixMilli(), true
		}
	}
	return 0, false
}

func unionMS(iv [][2]int64) int64 {
	if len(iv) == 0 {
		return 0
	}
	sort.Slice(iv, func(i, j int) bool { return iv[i][0] < iv[j][0] })
	var total int64
	curA, curB := iv[0][0], iv[0][1]
	for _, x := range iv[1:] {
		if x[0] <= curB {
			if x[1] > curB {
				curB = x[1]
			}
			continue
		}
		total += curB - curA
		curA, curB = x[0], x[1]
	}
	total += curB - curA
	return total
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	cp := append([]float64(nil), v...)
	sort.Float64s(cp)
	n := len(cp)
	if n%2 == 1 {
		return cp[n/2]
	}
	return (cp[n/2-1] + cp[n/2]) / 2
}

func round3(v float64) float64 {
	return float64(int64(v*1000+0.5)) / 1000
}

func toFloat(v any) (float64, bool) {
	switch x := v.(type) {
	case float64:
		return x, true
	case float32:
		return float64(x), true
	case int:
		return float64(x), true
	case int64:
		return float64(x), true
	case json.Number:
		f, err := x.Float64()
		return f, err == nil
	}
	return 0, false
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// loggingWarn keeps the analyzer's non-fatal writes observable without
// pulling the logging package into every branch.
func loggingWarn(format string, args ...any) {
	logging.Infof(format, args...)
}
