package service

import (
	"context"
	"testing"
	"time"
)

const wfTestBase int64 = 1_700_000_000_000

func wfAgent(key string, startMS, durMS int64) WorkflowNode {
	return WorkflowNode{Key: key, Kind: "agent", Label: key, Status: "verified",
		StartedAtMS: startMS, EndedAtMS: startMS + durMS, DurationMS: durMS, Attempt: 1}
}

func wfGraph(nodes []WorkflowNode, edges []WorkflowEdge) *WorkflowGraph {
	return &WorkflowGraph{GoalID: "g", Nodes: nodes, Edges: edges, Metrics: map[string]any{}}
}

// Regression: a fan-in node's chain must add only its own duration. The old
// code added the accumulated d.length again, so A||B -> C (30/30/10 min)
// produced 70 min instead of the hand-computed 40 min.
func TestWorkflowCriticalPathFanIn(t *testing.T) {
	m := int64(time.Minute / time.Millisecond)
	g := wfGraph(
		[]WorkflowNode{
			wfAgent("sg:a", wfTestBase, 30*m),
			wfAgent("sg:b", wfTestBase, 30*m),
			wfAgent("sg:c", wfTestBase+30*m, 10*m),
		},
		[]WorkflowEdge{
			{From: "sg:a", To: "sg:c", Kind: "depends_on", Source: "explicit"},
			{From: "sg:b", To: "sg:c", Kind: "depends_on", Source: "explicit"},
		})
	computeWorkflowMetrics(g)
	if got := mi(g.Metrics, "critical_path_ms"); got != 40*m {
		t.Fatalf("critical_path_ms = %d, want %d", got, 40*m)
	}
	if got := mi(g.Metrics, "work_sum_ms"); got != 70*m {
		t.Fatalf("work_sum_ms = %d, want %d", got, 70*m)
	}
	if got := mi(g.Metrics, "serialization_loss_ms"); got != 0 {
		t.Fatalf("serialization_loss_ms = %d, want 0", got)
	}
	if got := mf(g.Metrics, "parallel_speedup"); got != 1.75 {
		t.Fatalf("parallel_speedup = %v, want 1.75", got)
	}
	if len(g.Critical) != 2 {
		t.Fatalf("critical path = %v, want 2 nodes", g.Critical)
	}
}

// Inferred seq edges must not constrain the critical path. Two independent
// 30-min tasks that merely ran one after another are a serialization loss,
// not a dependency chain.
func TestWorkflowInferredSeqExcludedFromCriticalPath(t *testing.T) {
	m := int64(time.Minute / time.Millisecond)
	g := wfGraph(
		[]WorkflowNode{
			wfAgent("sg:a", wfTestBase, 30*m),
			wfAgent("sg:b", wfTestBase+30*m, 30*m),
		},
		[]WorkflowEdge{{From: "sg:a", To: "sg:b", Kind: "seq", Source: "inferred"}})
	computeWorkflowMetrics(g)
	if got := mi(g.Metrics, "critical_path_ms"); got != 30*m {
		t.Fatalf("critical_path_ms = %d, want %d (seq must be ignored)", got, 30*m)
	}
	if got := mi(g.Metrics, "wall_ms"); got != 60*m {
		t.Fatalf("wall_ms = %d, want %d", got, 60*m)
	}
	if got := mi(g.Metrics, "serialization_loss_ms"); got != 30*m {
		t.Fatalf("serialization_loss_ms = %d, want %d", got, 30*m)
	}
	if got := mf(g.Metrics, "parallel_speedup"); got != 1.0 {
		t.Fatalf("parallel_speedup = %v, want 1.0", got)
	}
}

// Review runs have no attribution edge: report their busy time separately
// instead of letting them inflate work_sum/speedup or become their own CP.
func TestWorkflowReviewIsolatedFromWorkAndSpeedup(t *testing.T) {
	m := int64(time.Minute / time.Millisecond)
	g := wfGraph(
		[]WorkflowNode{
			wfAgent("sg:a", wfTestBase, 30*m),
			{Key: "review:r1", Kind: "review", Label: "审查", Status: "completed",
				StartedAtMS: wfTestBase + 30*m, EndedAtMS: wfTestBase + 150*m, DurationMS: 120 * m},
		}, nil)
	computeWorkflowMetrics(g)
	if got := mi(g.Metrics, "work_sum_ms"); got != 30*m {
		t.Fatalf("work_sum_ms = %d, want %d (review excluded)", got, 30*m)
	}
	if got := mi(g.Metrics, "wall_ms"); got != 30*m {
		t.Fatalf("wall_ms = %d, want %d (review excluded)", got, 30*m)
	}
	if got := mi(g.Metrics, "review_busy_ms"); got != 120*m {
		t.Fatalf("review_busy_ms = %d, want %d", got, 120*m)
	}
	if got := mf(g.Metrics, "parallel_speedup"); got != 1.0 {
		t.Fatalf("parallel_speedup = %v, want 1.0", got)
	}
	if got := mi(g.Metrics, "critical_path_ms"); got != 30*m {
		t.Fatalf("critical_path_ms = %d, want %d", got, 30*m)
	}
}

// Equal-length tails must resolve deterministically; the old map iteration
// picked sg-a 361 / sg-b 39 times out of 400.
func TestWorkflowCriticalPathDeterministicTie(t *testing.T) {
	m := int64(time.Minute / time.Millisecond)
	mk := func() *WorkflowGraph {
		return wfGraph([]WorkflowNode{
			wfAgent("sg:a", wfTestBase, 30*m),
			wfAgent("sg:b", wfTestBase, 30*m),
		}, nil)
	}
	first := mk()
	computeWorkflowMetrics(first)
	want := append([]string(nil), first.Critical...)
	for i := 0; i < 500; i++ {
		g := mk()
		computeWorkflowMetrics(g)
		if len(g.Critical) != len(want) || (len(want) > 0 && g.Critical[0] != want[0]) {
			t.Fatalf("iteration %d: critical path = %v, want stable %v", i, g.Critical, want)
		}
	}
}

// Splitting a long serial gap into two independent tasks is exactly the
// "lost parallelism" case: work 60, cp 30, actual speedup 1.0.
func TestWorkflowLostParallelismInputs(t *testing.T) {
	m := int64(time.Minute / time.Millisecond)
	g := wfGraph(
		[]WorkflowNode{
			wfAgent("sg:a", wfTestBase, 30*m),
			wfAgent("sg:b", wfTestBase+30*m, 30*m),
		}, nil)
	computeWorkflowMetrics(g)
	gain := float64(mi(g.Metrics, "work_sum_ms")) / float64(mi(g.Metrics, "critical_path_ms"))
	actual := mf(g.Metrics, "parallel_speedup")
	if gain != 2 || actual != 1 {
		t.Fatalf("gain=%v actual=%v, want 2 and 1", gain, actual)
	}
	if !(actual < gain*0.7) {
		t.Fatalf("lost_parallelism guard should fire: actual=%v gain=%v", actual, gain)
	}
}

// The lost_parallelism rule must actually fire on the inputs it was written
// for. Previously speedup was work/cp, so "speedup < gain*0.7" reduced to
// 1 < 0.7 and no finding could ever be produced.
func TestLostParallelismRuleFires(t *testing.T) {
	m := int64(time.Minute / time.Millisecond)
	g := wfGraph(
		[]WorkflowNode{
			wfAgent("sg:a", wfTestBase, 30*m),
			wfAgent("sg:b", wfTestBase+30*m, 30*m),
		}, nil)
	computeWorkflowMetrics(g)
	svc := &RSIService{}
	findings := svc.structureFindings(context.Background(), "goal-1", g)
	for _, f := range findings {
		if f.Category == "lost_parallelism" {
			return
		}
	}
	t.Fatalf("lost_parallelism did not fire; metrics=%v", g.Metrics)
}
