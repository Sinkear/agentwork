package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/eushing/agentwork/internal/events"
)

// Structure analysis (W-RSI). Input: the assembled WorkflowGraph + entity
// tables. Output: one rsi_report (summary/metrics/baseline/frozen workflow)
// + zero or more rsi_findings, one per fired rule. All rules are
// deterministic thresholds over the metrics — no LLM in the loop, nothing
// executes by itself. Scope (user decision 2026-11-08): T1 structure +
// T2 dispatch + T3 info-flow only; node-internal strategy and platform
// parameters are out of optimization scope (observed at most); the meta
// layer is never auto-changed.
//
// Nine rule families (docs/research/rsi-summary.md §4.3):
//
//	structure:  serial_chain, lost_parallelism, split_candidate, merge_candidate
//	dispatch:   wrong_assignee (lane imbalance)
//	info-flow:  ping_pong (consult loops), brief_quality (rework magnet),
//	            consult_repeat (mid-execution clarification)
//	node-level: retry_amplification, human_wait (entity-level, not spans)
const (
	ruleSerialChainThreshold = 4    // ≥4-node chain on the critical path
	ruleLostParallelGain     = 2.0  // achievable speedup ≥2×
	ruleSplitCount           = 2    // ≥2 heavy nodes on the critical path
	ruleSplitShare           = 0.45 // heavy node ≥45% of critical path
	ruleMergeWall            = 3    // ≥3 leaf nodes ending within the window
	ruleMergeSpread          = 0.60 // their end spread ≤60% of the max node duration
	ruleLaneImbalance        = 2.0  // busiest lane ≥2× lane average
	rulePingPongPairs        = 3    // ≥3 consults between the same pair
	ruleReworkRatio          = 0.5  // ≥50% verification rounds rejected
	ruleConsultRepeat        = 3    // ≥3 mid-execution consults
	ruleRetryAmplification   = 2.0  // attempts ≥2× node count
	ruleHumanWaitShare       = 0.30 // gate wait ≥30% of wall clock
)

// Analyze is idempotent: it deletes the previous report+findings for the
// session and rebuilds them, so Retry always converges.
func (s *RSIService) Analyze(ctx context.Context, sessionID string) error {
	var goalID string
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT goal_id FROM training_session WHERE id=?`, sessionID).Scan(&goalID); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}

	graph, err := s.BuildGoalWorkflow(ctx, goalID)
	if err != nil {
		return fmt.Errorf("build workflow: %w", err)
	}
	graph.Frozen = true
	metrics := graph.Metrics
	baseline := s.baseline(ctx, sessionID)

	findings := s.structureFindings(ctx, goalID, graph)
	findings = append(findings, s.dispatchFindings(ctx, goalID, graph)...)
	findings = append(findings, s.infoFlowFindings(ctx, goalID, graph)...)
	findings = append(findings, s.nodeFindings(ctx, goalID, graph, metrics, baseline)...)
	sort.SliceStable(findings, func(i, j int) bool { return findings[i].Score > findings[j].Score })
	for i := range findings {
		findings[i].ID = newID()
		findings[i].Status = "open"
		findings[i].CreatedAt = now()
	}

	summary := summarize(graph, findings)
	narr := narrative(graph, findings, baseline)

	tx, err := s.st.DB().BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `DELETE FROM rsi_finding WHERE report_id IN (SELECT id FROM rsi_report WHERE session_id=?)`, sessionID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM rsi_report WHERE session_id=?`, sessionID); err != nil {
		return err
	}
	reportID := newID()
	wfJSON, _ := json.Marshal(graph)
	mj, _ := json.Marshal(metrics)
	bj, _ := json.Marshal(baseline)
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO rsi_report (id,session_id,status,summary,metrics_json,baseline_json,workflow_json,narrative,created_at,finished_at)
		 VALUES (?,?, 'ready', ?,?,?,?,?,?,?)`,
		reportID, sessionID, summary, string(mj), string(bj), string(wfJSON), narr, now(), now()); err != nil {
		return err
	}
	for i := range findings {
		findings[i].ReportID = reportID
		ev, _ := json.Marshal(findings[i].Evidence)
		if _, err := tx.ExecContext(ctx,
			`INSERT INTO rsi_finding (id,report_id,category,severity,confidence,title,explanation,suggestion,evidence_json,score,status,created_at)
			 VALUES (?,?,?,?,?,?,?,?,?,?, 'open', ?)`,
			findings[i].ID, reportID, findings[i].Category, findings[i].Severity, findings[i].Confidence,
			findings[i].Title, findings[i].Explanation, findings[i].Suggestion, string(ev), findings[i].Score, findings[i].CreatedAt); err != nil {
			return err
		}
	}
	var cpMS int64
	if v, ok := metrics["critical_path_ms"].(int64); ok {
		cpMS = v
	}
	speedup, _ := metrics["parallel_speedup"].(float64)
	if _, err := tx.ExecContext(ctx,
		`UPDATE training_session SET status='ready', finished_at=?, error='',
		 node_count=?, critical_path_ms=?, parallel_speedup=?, finding_count=? WHERE id=?`,
		now(), len(graph.Nodes), cpMS, speedup, len(findings), sessionID); err != nil {
		return err
	}

	// Insight-panel ledger: one optimization round per analysis run, numbered
	// across the DOMAIN (each R<n> is a new goal/session, so a per-session MAX
	// always produced round_no=1 and an empty delta). Delta compares against
	// the previous round anywhere in the domain; a session's first analysis
	// is "terminal", later runs are "manual_retry".
	var domainID string
	_ = tx.QueryRowContext(ctx,
		`SELECT COALESCE(g.domain_id,'') FROM training_session t JOIN goal g ON g.id=t.goal_id WHERE t.id=?`, sessionID).Scan(&domainID)
	var prevNo, sessionRounds int
	_ = tx.QueryRowContext(ctx,
		`SELECT COALESCE(MAX(r.round_no),0) FROM rsi_round r
		 JOIN training_session t ON t.id=r.session_id
		 JOIN goal g ON g.id=t.goal_id
		 WHERE COALESCE(g.domain_id,'')=?`, domainID).Scan(&prevNo)
	_ = tx.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM rsi_round WHERE session_id=?`, sessionID).Scan(&sessionRounds)
	roundNo := prevNo + 1
	trigger := "manual_retry"
	if sessionRounds == 0 {
		trigger = "terminal"
	}
	var prevMetrics string
	if prevNo > 0 {
		_ = tx.QueryRowContext(ctx,
			`SELECT r.metrics_json FROM rsi_round r
			 JOIN training_session t ON t.id=r.session_id
			 JOIN goal g ON g.id=t.goal_id
			 WHERE COALESCE(g.domain_id,'')=? ORDER BY r.created_at DESC LIMIT 1`, domainID).Scan(&prevMetrics)
	}
	criticalCount := 0
	for _, f := range findings {
		if f.Severity == "critical" {
			criticalCount++
		}
	}
	topCat, topTitle := "", ""
	if len(findings) > 0 {
		topCat, topTitle = findings[0].Category, findings[0].Title
	}
	fj, _ := json.Marshal(findings)
	dj, _ := json.Marshal(computeRoundDelta(decodeMap(prevMetrics), metrics))
	if _, err := tx.ExecContext(ctx,
		`INSERT INTO rsi_round (id,session_id,goal_id,round_no,trigger,metrics_json,baseline_json,delta_json,
		  finding_count,critical_count,top_category,top_title,findings_json,summary,narrative,created_at)
		 VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`,
		newID(), sessionID, goalID, roundNo, trigger, string(mj), string(bj), string(dj),
		len(findings), criticalCount, topCat, topTitle, string(fj), summary, narr, now()); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return err
	}
	s.bus.Publish(ctx, events.Event{Topic: "rsi:ready", Payload: map[string]any{
		"session_id": sessionID, "goal_id": goalID, "findings": len(findings), "round_no": roundNo,
	}})
	return nil
}

// computeRoundDelta compares the insight-panel headline metrics between two
// consecutive rounds: {key: {from, to, pct}} for every key present in both.
// pct is omitted when from == 0 (a 0→x jump has no meaningful percentage).
func computeRoundDelta(prev, cur map[string]any) map[string]any {
	out := map[string]any{}
	if len(prev) == 0 {
		return out
	}
	for _, k := range []string{"parallel_speedup", "critical_path_ms", "serialization_loss_ms",
		"dependency_wait_ms", "dispatch_wait_ms", "verify_overhead_ratio", "review_overhead_ratio",
		"rework_ratio", "consult_count", "human_wait_ms", "retry_amplification"} {
		pf, ok1 := num(prev[k])
		cf, ok2 := num(cur[k])
		if !ok1 || !ok2 {
			continue
		}
		d := map[string]any{"from": pf, "to": cf}
		if pf != 0 {
			d["pct"] = (cf - pf) / pf
		}
		out[k] = d
	}
	return out
}

// ── helpers over metrics ────────────────────────────────────────────────

func mi(m map[string]any, k string) int64 {
	if v, ok := m[k].(int64); ok {
		return v
	}
	return 0
}
func mf(m map[string]any, k string) float64 {
	if v, ok := m[k].(float64); ok {
		return v
	}
	return 0
}

// agentNodes returns the agent (sub-goal) nodes.
func agentNodes(g *WorkflowGraph) []WorkflowNode {
	out := []WorkflowNode{}
	for _, n := range g.Nodes {
		if n.Kind == "agent" {
			out = append(out, n)
		}
	}
	return out
}

// ── T1 structure rules ──────────────────────────────────────────────────

func (s *RSIService) structureFindings(ctx context.Context, goalID string, g *WorkflowGraph) []RsiFinding {
	out := []RsiFinding{}
	m := g.Metrics
	cp := mi(m, "critical_path_ms")
	work := mi(m, "work_sum_ms")
	speedup := mf(m, "parallel_speedup")

	// serial_chain: a long forced chain — was this parallelism necessary?
	if depth := len(g.Critical); depth >= ruleSerialChainThreshold {
		out = append(out, RsiFinding{
			Category: "serial_chain", Severity: "warn", Confidence: "L1",
			Title: fmt.Sprintf("关键路径为 %d 节点串行链", depth),
			Explanation: fmt.Sprintf("关键路径时长 %s（占墙钟 %.0f%%），整图 work/critical = %.2f×。若链上存在人为引入的依赖（本应并行），这是最大的提速杠杆。",
				humanMS(cp), pct(cp, mi(m, "wall_ms")), speedup),
			Suggestion: "逐边检查链上的 depends_on/seq：拆掉仅因「先做完再做」习惯而引入的依赖，把无数据依赖的节点改为并行派发。",
			Evidence:   []string{fmt.Sprintf("critical_path=%s", strings.Join(g.Critical, " → "))},
			Score:      float64(depth) * 10,
		})
	}

	// split_candidate: heavy nodes dominate the path.
	heavy := []WorkflowNode{}
	for _, k := range g.Critical {
		for _, n := range g.Nodes {
			if n.Key == k && n.Kind == "agent" && cp > 0 && float64(n.DurationMS)/float64(cp) >= ruleSplitShare {
				heavy = append(heavy, n)
			}
		}
	}
	if len(heavy) >= ruleSplitCount {
		names := make([]string, 0, len(heavy))
		ev := make([]string, 0, len(heavy))
		for _, n := range heavy {
			names = append(names, n.Label)
			ev = append(ev, fmt.Sprintf("%s: %s（占关键路径 %.0f%%）", n.Label, humanMS(n.DurationMS), 100*float64(n.DurationMS)/float64(cp)))
		}
		out = append(out, RsiFinding{
			Category: "split_candidate", Severity: "warn", Confidence: "L1",
			Title: fmt.Sprintf("%d 个重型节点占据关键路径，可拆分并行", len(heavy)),
			Explanation: fmt.Sprintf("节点 [%s] 各自占关键路径 ≥%d%%。若其内部是「多个独立子项顺序处理」，拆成并行子节点可把该段时长从求和降为取最大。",
				strings.Join(names, "、"), int(ruleSplitShare*100)),
			Suggestion: "把这些节点的产出物按数据依赖拆分：每份独立子项开一个 sub_goal（声明 depends_on 只指向真正的前置），由同一 squad 并行认领。",
			Evidence:   ev,
			Score:      float64(len(heavy)) * 25,
		})
	}

	// lost_parallelism: achievable speedup is large but unrealized.
	if cp > 0 && work > 0 {
		gain := float64(work) / float64(cp)
		if gain >= ruleLostParallelGain && speedup < gain*0.7 {
			out = append(out, RsiFinding{
				Category: "lost_parallelism", Severity: "info", Confidence: "L1",
				Title: fmt.Sprintf("理论并行收益 %.1f×，实际仅 %.1f×", gain, speedup),
				Explanation: fmt.Sprintf("总工作量 %s，墙钟 %s。大部分时间节点在排队而非工作 —— 收益差距来自串行化（依赖声明过强或派发过晚）。",
					humanMS(work), humanMS(mi(m, "wall_ms"))),
				Suggestion: "对照 seq 边（虚线）复核：时间先后 ≠ 数据依赖。把不共享读写对象的相邻节点改为可并行。",
				Evidence:   []string{fmt.Sprintf("work_sum_ms=%d wall_ms=%d critical_path_ms=%d", work, mi(m, "wall_ms"), cp)},
				Score:      gain * 15,
			})
		}
	}

	// merge_candidate: several small siblings finish together — merge overhead.
	leaves := endingLeaves(g)
	if len(leaves) >= ruleMergeWall {
		sort.Slice(leaves, func(i, j int) bool { return leaves[i].EndedAtMS < leaves[j].EndedAtMS })
		spread := leaves[len(leaves)-1].EndedAtMS - leaves[0].EndedAtMS
		var maxDur int64
		for _, n := range leaves {
			if n.DurationMS > maxDur {
				maxDur = n.DurationMS
			}
		}
		if maxDur > 0 && float64(spread)/float64(maxDur) <= ruleMergeSpread {
			out = append(out, RsiFinding{
				Category: "merge_candidate", Severity: "info", Confidence: "L1",
				Title: fmt.Sprintf("%d 个末梢节点几乎同时结束，合并可省派发开销", len(leaves)),
				Explanation: fmt.Sprintf("节点 [%s] 在 %s 窗口内先后结束，且单个节点都很小（max %s）。每个 sub_goal 都有固定的认领/验证/集成开销。",
					shortNames(leaves), humanMS(spread), humanMS(maxDur)),
				Suggestion: "若这些节点由同一执行者完成且共享上下文，合并为一个 sub_goal 的多产出物；仅当需要不同执行者并行时才保留拆分。",
				Evidence:   []string{fmt.Sprintf("leaves=%d end_spread_ms=%d", len(leaves), spread)},
				Score:      float64(len(leaves)) * 8,
			})
		}
	}
	return out
}

// ── T2 dispatch rules ───────────────────────────────────────────────────

func (s *RSIService) dispatchFindings(ctx context.Context, goalID string, g *WorkflowGraph) []RsiFinding {
	out := []RsiFinding{}
	m := g.Metrics
	if imb := mf(m, "lane_imbalance"); imb >= ruleLaneImbalance && mi(m, "lane_count") >= 2 {
		out = append(out, RsiFinding{
			Category: "wrong_assignee", Severity: "warn", Confidence: "L1",
			Title: fmt.Sprintf("负载不均：最忙通道是均值的 %.1f 倍", imb),
			Explanation: fmt.Sprintf("%d 条执行通道（%s）。最忙通道决定墙钟下限 —— 关键路径大概率压在同一个执行者身上。",
				mi(m, "lane_count"), laneDump(m)),
			Suggestion: "把最忙通道上可独立的 sub_goal 重新分派给空闲成员（squad 页编排图可直观看通道分布）；或在派发时按 in-flight 数选人。",
			Evidence:   []string{fmt.Sprintf("lane_imbalance=%.2f lanes=%d", imb, mi(m, "lane_count"))},
			Score:      imb * 20,
		})
	}
	return out
}

// ── T3 info-flow rules ──────────────────────────────────────────────────

func (s *RSIService) infoFlowFindings(ctx context.Context, goalID string, g *WorkflowGraph) []RsiFinding {
	out := []RsiFinding{}

	// ping_pong: consult_request pairs repeating within one goal.
	type pair struct {
		req, tgt string
		n        int
	}
	pairs := map[string]*pair{}
	order := []string{}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT requester_agent_id,target_agent_id FROM consult_request WHERE goal_id=?`, goalID)
	if err == nil {
		for rows.Next() {
			var req, tgt string
			if err := rows.Scan(&req, &tgt); err != nil {
				continue
			}
			k := req + "→" + tgt
			p, ok := pairs[k]
			if !ok {
				p = &pair{req: req, tgt: tgt}
				pairs[k] = p
				order = append(order, k)
			}
			p.n++
		}
		rows.Close()
	}
	for _, k := range order {
		p := pairs[k]
		if p.n >= rulePingPongPairs {
			out = append(out, RsiFinding{
				Category: "ping_pong", Severity: "warn", Confidence: "L1",
				Title: fmt.Sprintf("咨询乒乓：同一对 agent 往来 %d 次", p.n),
				Explanation: fmt.Sprintf("%s → %s 往返 %d 次。每次往返都是一整轮 run（排队+执行+等待），信息本可一次给全。",
					p.req, p.tgt, p.n),
				Suggestion: "把第一次咨询的问题清单化（一次性列出全部待确认项）；或在派发时把该 target 的上下文直接写进 sub_goal 描述，省掉来回。",
				Evidence:   []string{fmt.Sprintf("pair=%s count=%d", k, p.n)},
				Score:      float64(p.n) * 15,
			})
		}
	}

	// brief_quality: verification keeps rejecting — the brief is the magnet.
	rework := mf(g.Metrics, "rework_ratio")
	var reworked []string
	for _, n := range g.Nodes {
		if n.Kind == "verify" && n.Reworks > 0 && n.Attempt >= 2 {
			if r := sgLabel(g, n.SubGoalID); r != "" {
				reworked = append(reworked, fmt.Sprintf("%s（%d 轮 %d 拒）", r, n.Attempt, n.Reworks))
			}
		}
	}
	if rework >= ruleReworkRatio && len(reworked) > 0 {
		out = append(out, RsiFinding{
			Category: "brief_quality", Severity: "warn", Confidence: "L1",
			Title: fmt.Sprintf("返工率 %.0f%%：任务简报疑似信息不足", rework*100),
			Explanation: fmt.Sprintf("验证轮次中 %.0f%% 被拒：%s。反复被拒通常不是执行问题，而是 brief 缺少验收标准或关键上下文。",
				rework*100, strings.Join(reworked, "；")),
			Suggestion: "在 sub_goal 描述里补：验收标准（verifier 按什么判 pass）、输入产出物路径、已知约束；必要时先 consult 再派发（前移而不是事后返工）。",
			Evidence:   reworked,
			Score:      rework * 40,
		})
	}

	// consult_repeat / consult front-loading: consults requested BY a
	// sub-goal run — clarification happening mid-execution.
	late := 0
	crows, err := s.st.DB().QueryContext(ctx,
		`SELECT COUNT(*) FROM consult_request cr JOIN run r ON r.id=cr.requester_run_id
		 WHERE cr.goal_id=? AND r.sub_goal_id!=''`, goalID)
	if err == nil {
		_ = crows.Scan(&late)
		crows.Close()
	}
	if late >= ruleConsultRepeat {
		out = append(out, RsiFinding{
			Category: "consult_repeat", Severity: "info", Confidence: "L1",
			Title:       fmt.Sprintf("%d 次咨询发生在执行中途", late),
			Explanation: "执行者边干边问，说明派发时上下文没给全。中途咨询把该节点的墙钟拉长为「执行+等待回复」。",
			Suggestion:  "把这些咨询问题前移到 sub_goal 创建前（先 consult 再拆任务），或在描述中链接参考产出物。",
			Evidence:    []string{fmt.Sprintf("mid_execution_consults=%d", late)},
			Score:       float64(late) * 10,
		})
	}
	return out
}

// ── node-level (entity-derived) rules ───────────────────────────────────

func (s *RSIService) nodeFindings(ctx context.Context, goalID string, g *WorkflowGraph, metrics, baseline map[string]any) []RsiFinding {
	out := []RsiFinding{}

	// retry_amplification: some node ran far more than once. The metric is
	// the max Attempt (not the average), so a single retry loop is visible
	// instead of being diluted by well-behaved siblings.
	attempts := 0
	for _, n := range agentNodes(g) {
		attempts += n.Attempt
	}
	nodes := len(agentNodes(g))
	maxAttempts := int(mf(metrics, "retry_amplification"))
	if nodes > 0 && maxAttempts >= int(ruleRetryAmplification) {
		conf := "L1"
		if b := numOr(baseline["retry_amplification"]); b > 0 && float64(maxAttempts) > b {
			conf = "L2"
		}
		out = append(out, RsiFinding{
			Category: "retry_amplification", Severity: "warn", Confidence: conf,
			Title:       fmt.Sprintf("重试放大：单个子任务最多执行 %d 次", maxAttempts),
			Explanation: fmt.Sprintf("%d 个子任务共 %d 次执行。重试通常意味着失败模式重复（环境/凭据/简报），重试本身不修复原因。", nodes, attempts),
			Suggestion:  "查看失败 sub_goal 的 cancel_reason/错误：若是可自愈类（依赖缺失、锁竞争），把前置检查加进 brief；若是权限类，改为派发前校验。",
			Evidence:    []string{fmt.Sprintf("max_attempts=%d runs=%d nodes=%d", maxAttempts, attempts, nodes)},
			Score:       float64(attempts) * 10,
		})
	}

	// human_wait: gate review seconds vs total elapsed (wall + gate wait).
	// Using wall alone produced impossible >100% shares because gate time
	// happens after the run windows end.
	humanMSv := mi(metrics, "human_wait_ms")
	wall := mi(metrics, "wall_ms")
	share := mf(metrics, "human_wait_share")
	if share >= ruleHumanWaitShare {
		out = append(out, RsiFinding{
			Category: "human_wait", Severity: "info", Confidence: "L1",
			Title: fmt.Sprintf("人工等待占总时长 %.0f%%", 100*share),
			Explanation: fmt.Sprintf("gate 审查累计 %s，机器执行墙钟 %s（合计 %s）。这是工作流里最大的非机器等待。",
				humanMS(humanMSv), humanMS(wall), humanMS(wall+humanMSv)),
			Suggestion: "把高频 gate 的证据包固化成模板（结论+差异+回滚方案置顶），缩短人工判读时间；纯技术性 gate 可评估改为自动通过+事后抽查。",
			Evidence:   []string{fmt.Sprintf("human_wait_ms=%d wall_ms=%d share=%.2f", humanMSv, wall, share)},
			Score:      float64(humanMSv) / 1000,
		})
	}
	return out
}

// ── baseline & presentation ─────────────────────────────────────────────

// baseline returns the median structure metrics of the last N ready
// sessions (excluding this one). <5 samples → empty map (no comparison
// claims until there is history to compare against).
func (s *RSIService) baseline(ctx context.Context, sessionID string) map[string]any {
	// Same-domain history only: comparing a benchmark task against an
	// unrelated goal's metrics makes L2 claims meaningless.
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT rp.metrics_json FROM rsi_report rp
		 JOIN training_session ts ON ts.id=rp.session_id
		 JOIN goal g ON g.id=ts.goal_id
		 WHERE rp.status='ready' AND rp.session_id!=?
		   AND COALESCE(g.domain_id,'')=(
		     SELECT COALESCE(g2.domain_id,'') FROM training_session t2 JOIN goal g2 ON g2.id=t2.goal_id WHERE t2.id=?)
		 ORDER BY rp.created_at DESC LIMIT 20`, sessionID, sessionID)
	if err != nil {
		return map[string]any{}
	}
	defer rows.Close()
	samples := []map[string]any{}
	for rows.Next() {
		var mj string
		if err := rows.Scan(&mj); err != nil {
			continue
		}
		samples = append(samples, decodeMap(mj))
	}
	if len(samples) < 5 {
		return map[string]any{}
	}
	keys := []string{"critical_path_ms", "parallel_speedup", "serialization_loss_ms",
		"dependency_wait_ms", "dispatch_wait_ms", "verify_overhead_ratio", "review_overhead_ratio",
		"rework_ratio", "consult_count", "human_wait_ms", "retry_amplification"}
	out := map[string]any{}
	for _, k := range keys {
		vals := []float64{}
		for _, smp := range samples {
			if v, ok := num(smp[k]); ok {
				vals = append(vals, v)
			}
		}
		if len(vals)*2 > len(samples) { // majority coverage required
			sort.Slice(vals, func(i, j int) bool { return vals[i] < vals[j] })
			out[k] = vals[len(vals)/2]
		}
	}
	return out
}

func summarize(g *WorkflowGraph, findings []RsiFinding) string {
	m := g.Metrics
	top := "无明显结构问题"
	if len(findings) > 0 {
		top = fmt.Sprintf("首要发现：%s", findings[0].Title)
	}
	return fmt.Sprintf("%d 节点 / %d 边，关键路径 %s（墙钟 %s），并行收益 %.2f×。%s",
		len(g.Nodes), len(g.Edges), humanMS(mi(m, "critical_path_ms")), humanMS(mi(m, "wall_ms")),
		mf(m, "parallel_speedup"), top)
}

func narrative(g *WorkflowGraph, findings []RsiFinding, baseline map[string]any) string {
	var b strings.Builder
	m := g.Metrics
	fmt.Fprintf(&b, "本次训练会话把该 goal 的编排过程冻结为一张 %d 节点 / %d 边的工作流图（agent/verify/review/consult/integrate 节点，显式依赖与时间推断边）。\n\n", len(g.Nodes), len(g.Edges))
	fmt.Fprintf(&b, "结构指标：总工作量 %s，墙钟 %s，关键路径 %s（%d 节点），并行收益 %.2f×，串行损失 %s，峰值并行宽度 %d；验证开销占比 %.0f%%，返工率 %.0f%%，咨询 %d 次，通道 %d 条（不均衡度 %.2f）。\n\n",
		humanMS(mi(m, "work_sum_ms")), humanMS(mi(m, "wall_ms")), humanMS(mi(m, "critical_path_ms")), len(g.Critical),
		mf(m, "parallel_speedup"), humanMS(mi(m, "serialization_loss_ms")), mi(m, "parallel_width_peak"),
		mf(m, "verify_overhead_ratio")*100, mf(m, "rework_ratio")*100, mi(m, "consult_count"), mi(m, "lane_count"), mf(m, "lane_imbalance"))
	if len(baseline) > 0 {
		fmt.Fprintf(&b, "与近 20 次就绪会话的中位数基线对比：关键路径 %s → %s，并行收益 %.2f× → %.2f×。\n\n",
			humanMS(int64(numOr(baseline["critical_path_ms"]))), humanMS(mi(m, "critical_path_ms")),
			numOr(baseline["parallel_speedup"]), mf(m, "parallel_speedup"))
	} else {
		b.WriteString("尚无足够历史（<5 个就绪会话）计算基线，本报告只做单次观察。\n\n")
	}
	if len(findings) == 0 {
		b.WriteString("九类结构规则均未触发：编排结构在本次样本上没有可判定的瓶颈。")
	} else {
		fmt.Fprintf(&b, "结构发现（按得分排序，均为只读提案，不自动执行）：\n")
		for i, f := range findings {
			fmt.Fprintf(&b, "%d. [%s/%s] %s — %s\n   建议：%s\n", i+1, f.Category, f.Severity, f.Title, f.Explanation, f.Suggestion)
		}
	}
	return b.String()
}

// ── small formatting helpers ────────────────────────────────────────────

func humanMS(ms int64) string {
	switch {
	case ms <= 0:
		return "0ms"
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 60000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	default:
		return fmt.Sprintf("%.1fm", float64(ms)/60000)
	}
}
func pct(part, whole int64) float64 {
	if whole <= 0 {
		return 0
	}
	return float64(part) / float64(whole) * 100
}
func numOr(v any) float64 {
	f, _ := num(v)
	return f
}
func shortNames(nodes []WorkflowNode) string {
	names := make([]string, 0, len(nodes))
	for _, n := range nodes {
		names = append(names, n.Label)
	}
	return strings.Join(names, "、")
}
func laneDump(m map[string]any) string {
	lanes, ok := m["lanes"].(map[string]any)
	if !ok {
		return "{}"
	}
	parts := []string{}
	for k, v := range lanes {
		parts = append(parts, fmt.Sprintf("%s=%dms", k, int64(numOr(v))))
	}
	sort.Strings(parts)
	return strings.Join(parts, ", ")
}
func sgLabel(g *WorkflowGraph, sgid string) string {
	for _, n := range g.Nodes {
		if n.Kind == "agent" && n.SubGoalID == sgid {
			return n.Label
		}
	}
	return ""
}
func endingLeaves(g *WorkflowGraph) []WorkflowNode {
	outgoing := map[string]bool{}
	for _, e := range g.Edges {
		outgoing[e.From] = true
	}
	out := []WorkflowNode{}
	for _, n := range g.Nodes {
		if n.Kind == "agent" && n.EndedAtMS > 0 && !outgoing[n.Key] {
			out = append(out, n)
		}
	}
	return out
}
