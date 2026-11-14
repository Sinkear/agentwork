package daemon

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/eushing/agentwork/internal/proto"
	"github.com/eushing/agentwork/internal/store"
)

// insertToolRow persists one chat_message tool row as persistEvent would
// (role='tool', tool_calls = a JSON-encoded proto.Event). Tool_use and
// tool_result are separate rows aggregated by CallID.
func insertToolRow(t *testing.T, st *store.Store, runID string, ev proto.Event, at time.Time) {
	t.Helper()
	tc, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal tool event: %v", err)
	}
	id := fmt.Sprintf("cm-%s-%s-%d", ev.Type, ev.CallID, at.UnixNano())
	if _, err := st.DB().ExecContext(context.Background(),
		`INSERT INTO chat_message (id, run_id, role, content, tool_calls, created_at) VALUES (?,?,?,?,?,?)`,
		id, runID, "tool", "", string(tc), at.UTC().Format(time.RFC3339Nano)); err != nil {
		t.Fatalf("insert chat_message: %v", err)
	}
}

func watchdogSystemComments(t *testing.T, st *store.Store, goalID, runID string) []string {
	t.Helper()
	rows, err := st.DB().QueryContext(context.Background(),
		`SELECT content FROM comment WHERE goal_id=? AND author_type='system' AND run_id=? ORDER BY created_at`,
		goalID, runID)
	if err != nil {
		t.Fatalf("query system comments: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err != nil {
			t.Fatalf("scan comment: %v", err)
		}
		out = append(out, c)
	}
	return out
}

// TestWatchdogOpenToolBelowBudgetIsSilent: a run inside one tool call for 3
// minutes (a normal long command) must produce NO idle comment.
func TestWatchdogOpenToolBelowBudgetIsSilent(t *testing.T) {
	d, st, goalID, _, runID := seedWatchdogCtx(t)
	started := time.Now().Add(-3 * time.Minute)
	insertToolRow(t, st, runID, proto.Event{Type: proto.EventToolUse, Tool: "bash", CallID: "call-1"}, started)
	d.machineLastEventMu.Lock()
	d.machineLastEvent[runID] = started
	d.machineLastEventMu.Unlock()

	cancelSent := time.Time{}
	idleNotified := false
	stop := d.machineWatchdogTick(runID, "m1", time.Now(), &cancelSent, &idleNotified)
	if stop {
		t.Error("idle tick must not stop the watchdog")
	}
	if idleNotified {
		t.Error("long tool below budget must not notify")
	}
	if cs := watchdogSystemComments(t, st, goalID, runID); len(cs) != 0 {
		t.Fatalf("long tool below budget must stay silent, got %d comment(s): %v", len(cs), cs)
	}
}

// TestWatchdogOpenToolPastBudgetNotifiesOnce: an open tool call past the
// 15-minute budget posts exactly ONE comment naming the tool, and marks the
// run notified (no repeat on later ticks). The run is never cancelled.
func TestWatchdogOpenToolPastBudgetNotifiesOnce(t *testing.T) {
	d, st, goalID, _, runID := seedWatchdogCtx(t)
	started := time.Now().Add(-16 * time.Minute)
	insertToolRow(t, st, runID, proto.Event{Type: proto.EventToolUse, Tool: "bash", CallID: "call-2"}, started)
	d.machineLastEventMu.Lock()
	d.machineLastEvent[runID] = started
	d.machineLastEventMu.Unlock()

	cancelSent := time.Time{}
	idleNotified := false
	stop := d.machineWatchdogTick(runID, "m1", time.Now(), &cancelSent, &idleNotified)
	if stop {
		t.Error("long-tool notify tick must not stop the watchdog")
	}
	if !idleNotified {
		t.Error("open tool past budget must set idleNotified")
	}
	cs := watchdogSystemComments(t, st, goalID, runID)
	if len(cs) != 1 {
		t.Fatalf("expected exactly 1 long-tool comment, got %d: %v", len(cs), cs)
	}
	if !strings.Contains(cs[0], "bash") || !strings.Contains(cs[0], "long-running tool") {
		t.Errorf("long-tool comment must name the tool, got %q", cs[0])
	}

	// Second tick: no second comment (per-run once).
	d.machineWatchdogTick(runID, "m1", time.Now(), &cancelSent, &idleNotified)
	if cs := watchdogSystemComments(t, st, goalID, runID); len(cs) != 1 {
		t.Fatalf("long-tool notify must fire once, got %d comments", len(cs))
	}

	var status string
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT status FROM run WHERE id=?`, runID).Scan(&status); err != nil {
		t.Fatal(err)
	}
	if status != "running" {
		t.Errorf("idle watchdog must not cancel: status = %q", status)
	}
}

// TestWatchdogToolResultKeepsIdleNotify: once a tool_result lands (no open
// tool), the historical 2-minute idle notify still fires — the change is
// tool-aware, not a blanket silence.
func TestWatchdogToolResultKeepsIdleNotify(t *testing.T) {
	d, st, goalID, _, runID := seedWatchdogCtx(t)
	insertToolRow(t, st, runID, proto.Event{Type: proto.EventToolUse, Tool: "bash", CallID: "call-3"},
		time.Now().Add(-10*time.Minute))
	insertToolRow(t, st, runID, proto.Event{Type: proto.EventToolResult, Tool: "bash", CallID: "call-3"},
		time.Now().Add(-3*time.Minute))
	d.machineLastEventMu.Lock()
	d.machineLastEvent[runID] = time.Now().Add(-3 * time.Minute)
	d.machineLastEventMu.Unlock()

	cancelSent := time.Time{}
	idleNotified := false
	d.machineWatchdogTick(runID, "m1", time.Now(), &cancelSent, &idleNotified)
	if !idleNotified {
		t.Fatal("closed tool + 3min silence must still notify")
	}
	cs := watchdogSystemComments(t, st, goalID, runID)
	if len(cs) != 1 {
		t.Fatalf("expected 1 idle comment, got %d: %v", len(cs), cs)
	}
	if !strings.Contains(cs[0], "Run idle for") {
		t.Errorf("2-min idle comment text changed unexpectedly: %q", cs[0])
	}
	if strings.Contains(cs[0], "long-running tool") {
		t.Errorf("a closed tool must not use the long-tool text: %q", cs[0])
	}
}

// TestWatchdogMaxDurationUnchanged: the tool-aware idle path must not touch
// max_run_duration — a run past 2h is still cancelled with cancel_reason
// timeout even while a long tool is open.
func TestWatchdogMaxDurationUnchanged(t *testing.T) {
	d, st, _, _, runID := seedWatchdogCtx(t)
	insertToolRow(t, st, runID, proto.Event{Type: proto.EventToolUse, Tool: "bash", CallID: "call-4"},
		time.Now().Add(-16*time.Minute))
	d.machineLastEventMu.Lock()
	d.machineLastEvent[runID] = time.Now()
	d.machineLastEventMu.Unlock()

	cancelSent := time.Time{}
	// Already idle-notified: the max_run_duration branch must still fire in the
	// same tick, proving the tool-aware idle path did not disturb it.
	idleNotified := true
	stop := d.machineWatchdogTick(runID, "m1", time.Now().Add(-3*time.Hour), &cancelSent, &idleNotified)
	if stop {
		t.Error("first max_run_duration tick should not stop (sends cancel, waits grace)")
	}
	d.machinePendingMu.Lock()
	cancels := len(d.machineCancels["m1"])
	d.machinePendingMu.Unlock()
	if cancels != 1 {
		t.Fatalf("max_run_duration must enqueue 1 cancel, got %d", cancels)
	}
	var reason string
	if err := st.DB().QueryRowContext(context.Background(),
		`SELECT cancel_reason FROM run WHERE id=?`, runID).Scan(&reason); err != nil {
		t.Fatal(err)
	}
	if reason != "timeout" {
		t.Errorf("cancel_reason = %q, want timeout", reason)
	}
}
