"use client";

import { useState, useMemo, useRef, useCallback } from "react";
import { useGoalRuns, useGoalRunMessages, useAgents } from "@/lib/queries";
import { useQueryClient } from "@tanstack/react-query";
import { stopRun } from "@/lib/api";
import { useWSEvent } from "@/lib/ws";
import { Badge, Empty } from "@/components/ui";
import type { Run } from "@/lib/types";
import type { ChatMessage } from "@/lib/api";
import { groupMessages, StreamCards } from "@/lib/run-messages";

// useThrottled returns a throttled wrapper (leading + trailing edge): at
// most one call per window, and a call landing inside an open window is
// delivered when it closes. Used to tame the per-token run:event storm —
// the live stream panel refetches at most once per second.
function useThrottled(fn: () => void, ms: number) {
  const fnRef = useRef(fn);
  fnRef.current = fn;
  const last = useRef(0);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  return useCallback(() => {
    const now = Date.now();
    if (timer.current) return; // trailing edge already scheduled
    if (now - last.current >= ms) {
      last.current = now;
      fnRef.current();
      return;
    }
    timer.current = setTimeout(() => {
      timer.current = null;
      last.current = Date.now();
      fnRef.current();
    }, ms - (now - last.current));
  }, [ms]);
}

export function GoalRuns({ goalId }: { goalId: string }) {
  const { data: runs, isLoading, refetch } = useGoalRuns(goalId);
  const { data: agents } = useAgents();
  const [showPast, setShowPast] = useState(false);

  // Agent id → name (the runs table reads who is working, not a hex id).
  const agentName = (id: string) => agents?.find((a) => a.id === id)?.name ?? id.slice(0, 8);

  // Refresh on run LIFECYCLE events only (enqueued/claimed/cancelled/
  // terminal) — the TERMINAL events matter most for the stop button: the
  // stop request only fires the cancel; the run flips terminal when the
  // backend reports back, and without these subscriptions the card stays
  // "running" forever after a stop click (the immediate invalidation
  // refetches while the run is still winding down). run:event is
  // deliberately NOT subscribed: it is a per-token broadcast for every
  // run on the platform, and the list rows show nothing that changes per
  // stream event — subscribing refetched the whole list dozens of times
  // per second while any agent worked (the live stream panel below
  // refreshes itself per open run). Events are global — only refetch for
  // THIS goal.
  const refetchIfOwn = (p: { goal_id?: string }) => {
    if (p?.goal_id === goalId) refetch();
  };
  useWSEvent("run:enqueued", refetchIfOwn);
  useWSEvent("run:claimed", refetchIfOwn);
  useWSEvent("run:cancelled", refetchIfOwn);
  useWSEvent("run.terminal", refetchIfOwn);

  const activeRuns = useMemo(() => runs?.filter((r) => r.status === "queued" || r.status === "running") ?? [], [runs]);
  const pastRuns = useMemo(
    () => (runs?.filter((r) => r.status !== "queued" && r.status !== "running") ?? [])
      .sort((a, b) => {
        const aTime = a.finished_at || a.started_at || a.created_at || "";
        const bTime = b.finished_at || b.started_at || b.created_at || "";
        return bTime.localeCompare(aTime);
      }),
    [runs]
  );

  return (
    <div className="bg-surface-1 rounded-2xl border border-line/80 shadow-sm overflow-hidden">
      <div className="px-4 py-2.5 border-b border-line text-xs font-medium text-ink-3 uppercase tracking-wide">
        运行历史{runs && runs.length > 0 && `（${runs.length}）`}
      </div>
      <div className="p-4">
        {isLoading ? (
          <div className="text-sm text-ink-3 text-center py-8">加载中…</div>
        ) : !runs || runs.length === 0 ? (
          <Empty>尚未有运行记录。</Empty>
        ) : (
          <div className="space-y-3">
            {/* Active runs */}
            {activeRuns.length > 0 && (
              <div>
                <div className="text-xs font-medium text-ink-3 mb-2">活跃运行</div>
                <RunTable runs={activeRuns} goalId={goalId} agentName={agentName} />
              </div>
            )}

            {/* Past runs (collapsible) */}
            {pastRuns.length > 0 && (
              <div>
                <button
                  onClick={() => setShowPast(!showPast)}
                  className="text-xs font-medium text-ink-3 hover:text-ink-2 mb-2 flex items-center gap-1"
                >
                  {showPast ? "▾" : "▸"} 历史运行（{pastRuns.length}）
                </button>
                {showPast && <RunTable runs={pastRuns} goalId={goalId} agentName={agentName} />}
              </div>
            )}
          </div>
        )}
      </div>
    </div>
  );
}

function RunTable({ runs, goalId, agentName }: { runs: Run[]; goalId: string; agentName: (id: string) => string }) {
  return (
    <div className="space-y-2">
      {runs.map((r) => (
        <RunCard key={r.id} run={r} goalId={goalId} agentName={agentName} />
      ))}
    </div>
  );
}

// RunCard shows one run as a card; clicking it opens the LIVE interaction
// stream (chat_message — what the agent is doing right now), refreshed on
// every run:event over the WS.
function RunCard({ run, goalId, agentName }: { run: Run; goalId: string; agentName: (id: string) => string }) {
  const [open, setOpen] = useState(false);
  // 折叠的卡片不拉交互流——enabled 钉在 open 上，展开才拉取
  // （否则打开一个 goal 会为每条历史 run 各发一次消息请求）。
  const { data: messages, refetch } = useGoalRunMessages(goalId, run.id, open);
  // The live stream refreshes on run:event — but that broadcast is
  // PER-TOKEN for every run on the platform: filter to THIS run and
  // throttle (the persisted rows are aggregated, so adjacent events
  // rarely change anything — 1/s keeps the panel fresh without the storm).
  const throttledRefetch = useThrottled(() => refetch(), 1000);
  useWSEvent("run:event", (p) => {
    if (!open) return;
    if ((p as { run_id?: string })?.run_id !== run.id) return;
    throttledRefetch();
  });

  const timeRange = [
    run.started_at ? new Date(run.started_at).toLocaleString("zh-CN") : "-",
    run.finished_at ? new Date(run.finished_at).toLocaleString("zh-CN") : null,
  ].filter(Boolean).join(" → ");

  return (
    <div className="bg-surface-1 rounded-2xl border border-line/80 shadow-sm overflow-hidden">
      <div className="flex items-center p-3.5">
        <button
          onClick={() => setOpen(!open)}
          className="flex-1 flex items-center gap-3 text-left hover:bg-surface-0/60 transition-colors rounded-lg py-0.5"
        >
          <span className="h-2 w-2 rounded-full bg-accent shrink-0" />
          <span className="font-medium text-sm text-ink">{run.agent_id ? agentName(run.agent_id) : "-"}</span>
          <Badge status={run.status} />
          {(run.role === "consult" || run.role === "review" || run.role === "subgoal" || run.role === "verify") && (
            <span className="text-[11px] px-1.5 py-0.5 rounded bg-violet-500/10 text-violet-400 border border-violet-500/20 shrink-0">
              {run.role === "review" ? "审查" : run.role === "subgoal" ? "子任务" : run.role === "verify" ? "验证" : "咨询"}
            </span>
          )}
          <span className="text-xs text-ink-3 shrink-0">#{run.attempt}</span>
          {run.status === "cancelled" && run.cancel_reason && (
            <span className="text-[11px] px-1.5 py-0.5 rounded bg-amber-500/10 text-amber-400 border border-amber-500/20 shrink-0">
              {cancelReasonLabel(run.cancel_reason)}
            </span>
          )}
          <span className="text-xs text-ink-3 ml-auto hidden sm:block">{timeRange}</span>
          <span className="text-ink-3 text-xs shrink-0">{open ? "▾" : "▸"}</span>
        </button>
        {(run.status === "running" || run.status === "queued") && <StoppingRun goalId={goalId} runId={run.id} />}
      </div>

      {open && (
        <div className="border-t border-line bg-surface-0/60 p-4 space-y-3">
          {run.prompt && (
            <div className="text-xs text-ink-2">
              <div className="text-[11px] font-medium text-ink-3 uppercase tracking-wide mb-1">任务指令（prompt）</div>
              <div className="max-h-72 overflow-y-auto whitespace-pre-wrap font-mono text-ink-2 bg-surface-1 rounded-lg border border-line/60 p-2.5">
                {run.prompt}
              </div>
            </div>
          )}
          {run.status === "running" && (
            <div className="text-[11px] text-emerald-400 flex items-center gap-1">
              <span className="inline-block w-1.5 h-1.5 rounded-full bg-emerald-500 animate-pulse" />
              运行中——实时交互流
            </div>
          )}
          <RunMessages messages={messages ?? []} />
        </div>
      )}
    </div>
  );
}

// ── Run interaction stream: grouped + collapsible cards ──
//
// RunMessages renders the interaction stream as collapsible cards (thought /
// tool / text) via the shared StreamCards component — the same renderer the
// timeline side card and the domains compile stream use, so every real-time
// surface looks identical.

// RunMessages renders the interaction stream as collapsible cards: thoughts
// and tool calls fold on click; assistant text is always shown.
function RunMessages({ messages }: { messages: ChatMessage[] }) {
  if (messages.length === 0) {
    return <div className="text-[11px] text-ink-3 py-2">尚无交互记录…</div>;
  }
  return (
    <div className="max-h-72 overflow-y-auto">
      <StreamCards items={groupMessages(messages)} />
    </div>
  );
}

// StoppingRun terminates a running run on human command (决策 4-12): the run
// cancels (no attempt consumed, no auto-retry), goal state untouched — the
// worktree keeps its state and recovery is the human's call.
// cancelReasonLabel renders the structured cancellation cause for the runs
// panel — a dropped intent's fate is visible at a glance.
function cancelReasonLabel(reason: string): string {
  switch (reason) {
    case "idle_watchdog": return "静默超时";
    case "handoff": return "交接终止";
    case "stopped": return "人工停止";
    case "timeout": return "超时";
    case "runaway": return "失控终止";
    case "goal_terminal": return "目标终态丢弃";
    case "goal_cancelled": return "目标取消";
    default: return reason;
  }
}

function StoppingRun({ goalId, runId }: { goalId: string; runId: string }) {
  const qc = useQueryClient();
  const [stopping, setStopping] = useState(false);
  const [requested, setRequested] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  return (
    <div className="flex items-center gap-2">
      {err && <span className="text-[11px] text-red-400">{err}</span>}
      {requested ? (
        <span className="text-[11px] text-ink-3">已请求停止，等待终止…</span>
      ) : (
        <button
          onClick={async () => {
            setStopping(true);
            setErr(null);
            try {
              await stopRun(goalId, runId);
              // The stop request is accepted; the run's terminal state lands
              // async (cancel → backend reports back). Show the pending note;
              // the run:cancelled/run.terminal subscriptions flip the card
              // when the terminal state lands.
              setRequested(true);
              setStopping(false);
              // The real runs-list key is qk.goalRuns = ["goals", goalId,
              // "runs"] — the old ["goal-runs", …] key matched nothing and the
              // list only refreshed via the run:event refetch.
              qc.invalidateQueries({ queryKey: ["goals", goalId, "runs"] });
            } catch (e) {
              setErr(String(e));
              setStopping(false);
            }
          }}
          disabled={stopping}
          className="text-[11px] text-red-400 hover:text-red-300 hover:underline disabled:opacity-50"
        >
          {stopping ? "停止中…" : "停止"}
        </button>
      )}
    </div>
  );
}
