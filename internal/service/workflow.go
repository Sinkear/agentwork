package service

import (
	"context"
	"encoding/json"
	"sort"
	"time"
)

// Workflow-level observation (W-RSI, 2026-11-08). The orchestration graph is
// ASSEMBLED READ-TIME from entity tables (goal / sub_goal / run /
// verification_result / consult_request / change) — there is no node table
// and no span collection, so the graph can never drift from execution state.
// Declared dependencies live in sub_goal.depends_on; every other relation is
// derived from structure or, when only time-order suggests it, marked
// "inferred" (dashed in the UI). Inferred edges are DISPLAY EVIDENCE ONLY:
// the critical-path DP ignores them, otherwise two independent tasks that
// merely happened to run one after the other would be chained into a fake
// dependency and every serial execution would report speedup 1.0."

// WorkflowNode is one node of the orchestration graph. kind:
// goal|agent|verify|review|consult|integrate.
type WorkflowNode struct {
	Key           string  `json:"key"`
	Kind          string  `json:"kind"`
	Label         string  `json:"label"`
	AssigneeID    string  `json:"assignee_id"`
	SubGoalID     string  `json:"sub_goal_id,omitempty"`
	RunID         string  `json:"run_id,omitempty"`
	Status        string  `json:"status"`
	Attempt       int     `json:"attempt"`     // run count (agent nodes) / verification rounds (verify nodes)
	Reworks       int     `json:"reworks"`     // rejected verification rounds on this node
	DurationMS    int64   `json:"duration_ms"` // busy time (sum of run walls)
	StartedAtMS   int64   `json:"started_at_ms"`
	EndedAtMS     int64   `json:"ended_at_ms"`
	CriticalShare float64 `json:"critical_share"` // duration / critical path (filled by metrics)
}

// WorkflowEdge is one directed edge. kind:
// created|depends_on|seq|verified_by|consult|integrated. source:
// explicit (declared or structural by construction) | inferred (time-order
// heuristic).
type WorkflowEdge struct {
	From   string `json:"from"`
	To     string `json:"to"`
	Kind   string `json:"kind"`
	Source string `json:"source"`
}

// WorkflowGraph is the full assembled graph for one goal. Frozen snapshots
// are stored in rsi_report.workflow_json at analysis time.
type WorkflowGraph struct {
	GoalID      string         `json:"goal_id"`
	GeneratedAt string         `json:"generated_at"`
	Frozen      bool           `json:"frozen"`
	Nodes       []WorkflowNode `json:"nodes"`
	Edges       []WorkflowEdge `json:"edges"`
	Critical    []string       `json:"critical_path"` // node keys, root → sink
	Metrics     map[string]any `json:"metrics"`
}

// WorkflowGoalSummary is the mini-DAG row shown in the squad orchestration
// section and the Training list.
type WorkflowGoalSummary struct {
	GoalID          string  `json:"goal_id"`
	GoalTitle       string  `json:"goal_title"`
	Status          string  `json:"status"`
	NodeCount       int     `json:"node_count"`
	EdgeCount       int     `json:"edge_count"`
	CriticalPathMS  int64   `json:"critical_path_ms"`
	ParallelSpeedup float64 `json:"parallel_speedup"`
	ReworkRatio     float64 `json:"rework_ratio"`
}

// parseTimeMS parses an RFC3339Nano store timestamp into epoch millis.
func parseTimeMS(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339Nano, s)
	if err != nil {
		return 0, false
	}
	return t.UnixMilli(), true
}

// addBusy accumulates a run window into a node window.
func addBusy(n *WorkflowNode, start, finish string) {
	if ms, ok := parseTimeMS(start); ok && (n.StartedAtMS == 0 || ms < n.StartedAtMS) {
		n.StartedAtMS = ms
	}
	if ms, ok := parseTimeMS(finish); ok && ms > n.EndedAtMS {
		n.EndedAtMS = ms
	}
	if ms1, ok1 := parseTimeMS(start); ok1 {
		if ms2, ok2 := parseTimeMS(finish); ok2 && ms2 > ms1 {
			n.DurationMS += ms2 - ms1
		}
	}
}

// BuildGoalWorkflow assembles the orchestration graph for one goal.
func (s *RSIService) BuildGoalWorkflow(ctx context.Context, goalID string) (*WorkflowGraph, error) {
	var gTitle, gStatus string
	if err := s.st.DB().QueryRowContext(ctx,
		`SELECT status,title FROM goal WHERE id=?`, goalID).Scan(&gStatus, &gTitle); err != nil {
		return nil, err
	}

	graph := &WorkflowGraph{
		GoalID:      goalID,
		GeneratedAt: now(),
		Nodes:       []WorkflowNode{},
		Edges:       []WorkflowEdge{},
		Metrics:     map[string]any{},
	}
	graph.Nodes = append(graph.Nodes, WorkflowNode{Key: "goal", Kind: "goal", Label: gTitle, Status: gStatus})

	// ── sub-goal nodes + windows ────────────────────────────────────────
	type sgRow struct {
		id, title, assignee, verifier, status, createdAt, dependsOn string
		qualityIter                                                 int
	}
	sgRows := map[string]*sgRow{}
	sgOrder := []string{}
	rows, err := s.st.DB().QueryContext(ctx,
		`SELECT id,title,assignee_id,verifier_id,status,quality_iteration,created_at,depends_on
		 FROM sub_goal WHERE goal_id=? ORDER BY created_at ASC`, goalID)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var r sgRow
		if err := rows.Scan(&r.id, &r.title, &r.assignee, &r.verifier, &r.status, &r.qualityIter, &r.createdAt, &r.dependsOn); err != nil {
			rows.Close()
			return nil, err
		}
		sgRows[r.id] = &r
		sgOrder = append(sgOrder, r.id)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	type win struct {
		start, end, busy int64
		attempts         int
	}
	wins := map[string]*win{}
	rrows, err := s.st.DB().QueryContext(ctx,
		`SELECT sub_goal_id,started_at,finished_at FROM run
		 WHERE goal_id=? AND sub_goal_id!='' AND role='subgoal'`, goalID)
	if err != nil {
		return nil, err
	}
	for rrows.Next() {
		var sgid, st, fi string
		if err := rrows.Scan(&sgid, &st, &fi); err != nil {
			rrows.Close()
			return nil, err
		}
		w, ok := wins[sgid]
		if !ok {
			w = &win{}
			wins[sgid] = w
		}
		w.attempts++
		if ms, ok := parseTimeMS(st); ok && (w.start == 0 || ms < w.start) {
			w.start = ms
		}
		if ms, ok := parseTimeMS(fi); ok && ms > w.end {
			w.end = ms
		}
		if ms1, ok1 := parseTimeMS(st); ok1 {
			if ms2, ok2 := parseTimeMS(fi); ok2 && ms2 > ms1 {
				w.busy += ms2 - ms1
			}
		}
	}
	rrows.Close()
	if err := rrows.Err(); err != nil {
		return nil, err
	}

	// Declared dependencies, decoded once: used for gating-aware wait metrics
	// and for the explicit depends_on edges below.
	depsMap := map[string][]string{}
	for _, id := range sgOrder {
		var deps []string
		if err := json.Unmarshal([]byte(sgRows[id].dependsOn), &deps); err != nil {
			continue
		}
		depsMap[id] = deps
	}

	var depWait, dispatchWait int64
	for _, id := range sgOrder {
		r := sgRows[id]
		n := WorkflowNode{Key: "sg:" + id, Kind: "agent", Label: r.title, AssigneeID: r.assignee,
			SubGoalID: id, Status: r.status, Attempt: 1, Reworks: r.qualityIter}
		if w := wins[id]; w != nil {
			n.Attempt = w.attempts
			n.StartedAtMS, n.EndedAtMS, n.DurationMS = w.start, w.end, w.busy
			// Split queue time into dependency gate wait and dispatch delay:
			// ready_at = max(sub_goal.created_at, latest dependency run end).
			createdMS, _ := parseTimeMS(r.createdAt)
			readyMS := createdMS
			for _, dep := range depsMap[id] {
				if wd := wins[dep]; wd != nil && wd.end > readyMS {
					readyMS = wd.end
				}
			}
			if readyMS < createdMS {
				readyMS = createdMS
			}
			if readyMS > createdMS {
				depWait += readyMS - createdMS
			}
			if w.start > 0 && readyMS > 0 && w.start > readyMS {
				dispatchWait += w.start - readyMS
			}
		}
		graph.Nodes = append(graph.Nodes, n)
		graph.Edges = append(graph.Edges, WorkflowEdge{From: "goal", To: n.Key, Kind: "created", Source: "explicit"})
	}

	// Declared dependencies (explicit DAG) — best-effort JSON decode.
	for _, id := range sgOrder {
		var deps []string
		if err := json.Unmarshal([]byte(sgRows[id].dependsOn), &deps); err != nil || len(deps) == 0 {
			continue
		}
		for _, d := range deps {
			if _, ok := sgRows[d]; ok {
				graph.Edges = append(graph.Edges, WorkflowEdge{From: "sg:" + d, To: "sg:" + id, Kind: "depends_on", Source: "explicit"})
			}
		}
	}

	// Inferred sequence edges: B started strictly after A ended with no
	// declared edge either way. Dashed evidence only — never a strong claim.
	hasEdge := func(a, b string) bool {
		for _, e := range graph.Edges {
			if (e.From == a && e.To == b) || (e.From == b && e.To == a) {
				return true
			}
		}
		return false
	}
	for _, a := range sgOrder {
		wa := wins[a]
		if wa == nil || wa.end == 0 {
			continue
		}
		for _, b := range sgOrder {
			if a == b {
				continue
			}
			wb := wins[b]
			if wb == nil || wb.start == 0 || wb.start <= wa.end {
				continue
			}
			if hasEdge("sg:"+a, "sg:"+b) {
				continue
			}
			graph.Edges = append(graph.Edges, WorkflowEdge{From: "sg:" + a, To: "sg:" + b, Kind: "seq", Source: "inferred"})
		}
	}

	// ── verification nodes (one per sub-goal, rounds aggregated) ────────
	type vState struct {
		node   WorkflowNode
		runIDs []string
	}
	vmap := map[string]*vState{}
	vrows, err := s.st.DB().QueryContext(ctx,
		`SELECT sub_goal_id,status,verifier_run_id FROM verification_result WHERE goal_id=? ORDER BY created_at ASC`, goalID)
	if err != nil {
		return nil, err
	}
	for vrows.Next() {
		var sgid, status, vrun string
		if err := vrows.Scan(&sgid, &status, &vrun); err != nil {
			vrows.Close()
			return nil, err
		}
		v, ok := vmap[sgid]
		if !ok {
			verifier := ""
			if r := sgRows[sgid]; r != nil {
				verifier = r.verifier
			}
			v = &vState{node: WorkflowNode{Key: "v:" + sgid, Kind: "verify", Label: "验证",
				AssigneeID: verifier, SubGoalID: sgid, Status: "passed"}}
			vmap[sgid] = v
		}
		v.node.Attempt++
		if status == "rejected" {
			v.node.Reworks++
			v.node.Status = "rejected"
		}
		if vrun != "" {
			v.runIDs = append(v.runIDs, vrun)
		}
	}
	vrows.Close()
	if err := vrows.Err(); err != nil {
		return nil, err
	}
	for sgid, v := range vmap {
		for _, rid := range v.runIDs {
			var st, fi string
			if err := s.st.DB().QueryRowContext(ctx,
				`SELECT started_at,finished_at FROM run WHERE id=?`, rid).Scan(&st, &fi); err != nil {
				continue
			}
			addBusy(&v.node, st, fi)
		}
		graph.Nodes = append(graph.Nodes, v.node)
		graph.Edges = append(graph.Edges, WorkflowEdge{From: "sg:" + sgid, To: v.node.Key, Kind: "verified_by", Source: "explicit"})
	}

	// ── review + consult nodes ──────────────────────────────────────────
	crows, err := s.st.DB().QueryContext(ctx,
		`SELECT id,role,agent_id,status,started_at,finished_at FROM run
		 WHERE goal_id=? AND role IN ('review','consult')`, goalID)
	if err != nil {
		return nil, err
	}
	for crows.Next() {
		var id, role, agent, status, st, fi string
		if err := crows.Scan(&id, &role, &agent, &status, &st, &fi); err != nil {
			crows.Close()
			return nil, err
		}
		n := WorkflowNode{Key: role + ":" + id, Kind: role,
			Label:      map[string]string{"review": "审查", "consult": "咨询"}[role],
			AssigneeID: agent, RunID: id, Status: status}
		addBusy(&n, st, fi)
		graph.Nodes = append(graph.Nodes, n)
	}
	crows.Close()
	if err := crows.Err(); err != nil {
		return nil, err
	}

	// Consult structure edges: requester node → guest consult node.
	qrows, err := s.st.DB().QueryContext(ctx,
		`SELECT requester_run_id,guest_run_id FROM consult_request WHERE goal_id=? AND guest_run_id!=''`, goalID)
	if err != nil {
		return nil, err
	}
	for qrows.Next() {
		var reqID, guestID string
		if err := qrows.Scan(&reqID, &guestID); err != nil {
			qrows.Close()
			return nil, err
		}
		to := "consult:" + guestID
		if !nodeKeyExists(graph, to) {
			continue
		}
		from := "goal"
		var sgid string
		if err := s.st.DB().QueryRowContext(ctx,
			`SELECT sub_goal_id FROM run WHERE id=?`, reqID).Scan(&sgid); err == nil && sgid != "" {
			if _, ok := sgRows[sgid]; ok {
				from = "sg:" + sgid
			}
		}
		graph.Edges = append(graph.Edges, WorkflowEdge{From: from, To: to, Kind: "consult", Source: "explicit"})
	}
	qrows.Close()
	if err := qrows.Err(); err != nil {
		return nil, err
	}

	// ── integration nodes ───────────────────────────────────────────────
	chrows, err := s.st.DB().QueryContext(ctx,
		`SELECT id,sub_goal_id,status FROM change WHERE goal_id=?`, goalID)
	if err != nil {
		return nil, err
	}
	for chrows.Next() {
		var id, sgid, status string
		if err := chrows.Scan(&id, &sgid, &status); err != nil {
			chrows.Close()
			return nil, err
		}
		n := WorkflowNode{Key: "chg:" + id, Kind: "integrate", Label: "集成", SubGoalID: sgid, Status: status}
		graph.Nodes = append(graph.Nodes, n)
		graph.Edges = append(graph.Edges, WorkflowEdge{From: "sg:" + sgid, To: n.Key, Kind: "integrated", Source: "explicit"})
	}
	chrows.Close()
	if err := chrows.Err(); err != nil {
		return nil, err
	}

	computeWorkflowMetrics(graph)
	graph.Metrics["dependency_wait_ms"] = depWait
	graph.Metrics["dispatch_wait_ms"] = dispatchWait
	// queue_wait_ms kept as a compatibility alias; new readers should use the
	// split fields so dependency gating is not mistaken for dispatch delay.
	graph.Metrics["queue_wait_ms"] = depWait + dispatchWait
	// Human gate wait is entity-level (gate_decision), not derivable from run
	// windows. The share uses wall+wait so "占比" can never exceed 100%.
	var humanWaitMS int64
	_ = s.st.DB().QueryRowContext(ctx,
		`SELECT COALESCE(SUM(review_duration),0)*1000 FROM gate_decision WHERE goal_id=?`, goalID).Scan(&humanWaitMS)
	graph.Metrics["human_wait_ms"] = humanWaitMS
	wall := mi(graph.Metrics, "wall_ms")
	share := 0.0
	if denom := wall + humanWaitMS; denom > 0 {
		share = float64(humanWaitMS) / float64(denom)
	}
	graph.Metrics["human_wait_share"] = share
	return graph, nil
}

func nodeKeyExists(g *WorkflowGraph, key string) bool {
	for _, n := range g.Nodes {
		if n.Key == key {
			return true
		}
	}
	return false
}

// computeWorkflowMetrics fills Critical, CriticalShare, and Metrics. The
// critical path is a longest chain of node busy durations: nodes relax in
// start-time order and an edge only extends the chain when the source
// finishes before the target starts, so the pass is acyclic by construction
// even when the drawn graph has rework cycles.
func computeWorkflowMetrics(g *WorkflowGraph) {
	byKey := map[string]*WorkflowNode{}
	for i := range g.Nodes {
		byKey[g.Nodes[i].Key] = &g.Nodes[i]
	}
	order := append([]WorkflowNode(nil), g.Nodes...)
	sort.SliceStable(order, func(i, j int) bool {
		si, sj := order[i].StartedAtMS, order[j].StartedAtMS
		if order[i].Kind == "goal" || order[i].StartedAtMS == 0 {
			si = -1
		}
		if order[j].Kind == "goal" || order[j].StartedAtMS == 0 {
			sj = -1
		}
		return si < sj
	})

	preds := map[string][]string{} // target → sources (already processed earlier)
	for _, e := range g.Edges {
		// Inferred seq edges are evidence for display/queue analysis only. They
		// must not constrain the critical path: a pure time-order relation is
		// not a dependency, and admitting it makes every serial run look like
		// a 1.0-speedup chain.
		if e.Source == "inferred" {
			continue
		}
		if _, ok := byKey[e.From]; !ok {
			continue
		}
		if _, ok := byKey[e.To]; !ok {
			continue
		}
		preds[e.To] = append(preds[e.To], e.From)
	}

	type dp struct {
		length int64
		prev   string
	}
	best := map[string]dp{}
	for _, n := range order {
		// Review runs are unattributed by construction; letting one relax as
		// its own chain made a 120-min review the "critical path" of a 30-min
		// execution. They stay on the graph and in review_busy_ms only.
		if n.Kind == "review" {
			continue
		}
		d := dp{length: n.DurationMS}
		for _, from := range preds[n.Key] {
			p, ok := best[from]
			if !ok || p.length == 0 {
				continue
			}
			pn := byKey[from]
			// Time-order validity: source chain must end before target starts
			// (nodes without windows — goal root, not-yet-run — always chain).
			if pn.StartedAtMS > 0 && n.StartedAtMS > 0 && pn.StartedAtMS+pn.DurationMS > n.StartedAtMS {
				continue
			}
			// Chain length = predecessor chain + THIS node's busy time. Using
			// d.length here would double-count every fan-in predecessor.
			cand := p.length + n.DurationMS
			if cand > d.length || (cand == d.length && from < d.prev) {
				d = dp{length: cand, prev: from}
			}
		}
		best[n.Key] = d
	}

	path := []string{}
	var tail string
	var cpMS int64
	// Deterministic tail selection: walk the start-time-sorted order and break
	// equal-length ties by node key. Map iteration order must never decide the
	// critical path (400 runs of equal nodes previously split 361/39).
	for _, n := range order {
		b := best[n.Key]
		if b.length > cpMS || (b.length == cpMS && cpMS > 0 && (tail == "" || n.Key < tail)) {
			cpMS = b.length
			tail = n.Key
		}
	}
	for tail != "" {
		path = append([]string{tail}, path...)
		tail = best[tail].prev
	}
	g.Critical = path
	for i := range g.Nodes {
		g.Nodes[i].CriticalShare = 0
		if cpMS > 0 {
			g.Nodes[i].CriticalShare = float64(g.Nodes[i].DurationMS) / float64(cpMS)
		}
	}

	// Aggregates. Review runs are real elapsed work but have no attribution
	// edge, so they are reported separately instead of inflating work_sum /
	// speedup / lane imbalance (a review node with no incoming edge would
	// otherwise become its own critical path and inflate speedup).
	var workSum, maxNode, wallStart, wallEnd, verifyBusy, reviewBusy int64
	lane := map[string]int64{}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.DurationMS <= 0 {
			continue
		}
		if n.Kind == "review" {
			reviewBusy += n.DurationMS
			continue
		}
		workSum += n.DurationMS
		if n.DurationMS > maxNode {
			maxNode = n.DurationMS
		}
		if n.Kind == "verify" {
			verifyBusy += n.DurationMS
		}
		if n.AssigneeID != "" {
			lane[n.AssigneeID] += n.DurationMS
		}
		if n.StartedAtMS > 0 && (wallStart == 0 || n.StartedAtMS < wallStart) {
			wallStart = n.StartedAtMS
		}
		if n.EndedAtMS > wallEnd {
			wallEnd = n.EndedAtMS
		}
	}
	wall := wallEnd - wallStart
	if wall < 0 {
		wall = 0
	}

	// Parallel width peak: sweep of work intervals (review excluded).
	type iv struct {
		t     int64
		delta int
	}
	ints := []iv{}
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Kind == "review" {
			continue
		}
		if n.StartedAtMS > 0 && n.EndedAtMS > n.StartedAtMS {
			ints = append(ints, iv{t: n.StartedAtMS, delta: 1}, iv{t: n.EndedAtMS, delta: -1})
		}
	}
	sort.Slice(ints, func(i, j int) bool { return ints[i].t < ints[j].t })
	width, peak := 0, 0
	for _, x := range ints {
		width += x.delta
		if width > peak {
			peak = width
		}
	}

	var verifyRounds, rejRounds, consultCount int
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Kind == "verify" {
			verifyRounds += n.Attempt
			rejRounds += n.Reworks
		}
		if n.Kind == "consult" {
			consultCount++
		}
	}

	// serialization_loss = actual wall - dependency-constrained minimum wall.
	// (The old cp-maxNode formula measured imbalance, not lost time.)
	serLoss := wall - cpMS
	if serLoss < 0 {
		serLoss = 0
	}
	// parallel_speedup is actual throughput vs. one-lane execution:
	// work_sum/wall. 1.0 = fully serial, 2.0 = two lanes' worth of work in
	// the same wall. The old work/cp ratio was an average parallelism along
	// the chain, not a speedup.
	speedup := 0.0
	if wall > 0 {
		speedup = float64(workSum) / float64(wall)
	}
	verifyOverhead := 0.0
	reviewOverhead := 0.0
	if workSum > 0 {
		verifyOverhead = float64(verifyBusy) / float64(workSum)
		reviewOverhead = float64(reviewBusy) / float64(workSum)
	}
	var maxAttempts int
	for i := range g.Nodes {
		n := &g.Nodes[i]
		if n.Kind == "agent" && n.Attempt > maxAttempts {
			maxAttempts = n.Attempt
		}
	}
	rework := 0.0
	if verifyRounds > 0 {
		rework = float64(rejRounds) / float64(verifyRounds)
	}
	laneMax, laneSum := int64(0), int64(0)
	for _, v := range lane {
		if v > laneMax {
			laneMax = v
		}
		laneSum += v
	}
	laneImbalance := 0.0
	if len(lane) > 0 && laneSum > 0 {
		laneImbalance = float64(laneMax) / (float64(laneSum) / float64(len(lane)))
	}

	m := g.Metrics
	m["node_count"] = len(g.Nodes)
	m["edge_count"] = len(g.Edges)
	m["depth"] = len(path)
	m["work_sum_ms"] = workSum
	m["wall_ms"] = wall
	m["critical_path_ms"] = cpMS
	m["parallel_speedup"] = speedup
	m["serialization_loss_ms"] = serLoss
	m["parallel_width_peak"] = peak
	m["verify_overhead_ratio"] = verifyOverhead
	m["review_busy_ms"] = reviewBusy
	m["review_overhead_ratio"] = reviewOverhead
	m["retry_amplification"] = float64(maxAttempts)
	m["verify_rounds"] = verifyRounds
	m["rework_ratio"] = rework
	m["consult_count"] = consultCount
	m["lane_count"] = len(lane)
	m["lane_imbalance"] = laneImbalance
	m["lanes"] = lane
}

// WorkflowGoalSummaryFromGraph compresses a graph into the list row shape.
func WorkflowGoalSummaryFromGraph(g *WorkflowGraph) WorkflowGoalSummary {
	s := WorkflowGoalSummary{GoalID: g.GoalID, NodeCount: len(g.Nodes), EdgeCount: len(g.Edges)}
	if v, ok := g.Metrics["critical_path_ms"].(int64); ok {
		s.CriticalPathMS = v
	}
	if v, ok := g.Metrics["parallel_speedup"].(float64); ok {
		s.ParallelSpeedup = v
	}
	if v, ok := g.Metrics["rework_ratio"].(float64); ok {
		s.ReworkRatio = v
	}
	for _, n := range g.Nodes {
		if n.Key == "goal" {
			s.GoalTitle, s.Status = n.Label, n.Status
			break
		}
	}
	return s
}
