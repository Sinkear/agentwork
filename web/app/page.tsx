"use client";

import Link from "next/link";
import { AlertTriangle, ArrowRight, CheckCircle2, CircleDot, Zap } from "lucide-react";
import { useGoals, useAgents, useSquads, useGoalEvents } from "@/lib/queries";
import { Badge, PageBody, PageHeader, Button, Empty, SectionCard } from "@/components/ui";
import { relTime } from "@/lib/utils";
import type { Goal } from "@/lib/types";

// 总览：开门三件事——有什么要我批（待审批队列）、什么在跑（活跃任务）、
// 最近完成了什么（最近完成）。WS 订阅保证卡点变化实时进队列。
export default function Home() {
  useGoalEvents();
  const { data: goals } = useGoals();
  const { data: agents } = useAgents();
  const { data: squads } = useSquads();

  const agentName = (aid: string) => agents?.find((a) => a.id === aid)?.name ?? aid;
  const squadName = (sid: string) => squads?.find((s) => s.id === sid)?.name ?? sid;
  const assigneeLabel = (g: Goal) =>
    g.assignee_type === "squad" ? squadName(g.assignee_id) : g.assignee_id ? agentName(g.assignee_id) : "-";

  const all = goals ?? [];
  const review = all.filter((g) => g.status === "review");
  const active = all.filter((g) => g.status === "active");
  const done = all.filter((g) => g.status === "done").slice(0, 5);
  const failed = all.filter((g) => g.status === "failed");
  const doneCount = all.filter((g) => g.status === "done").length;

  // 统计卡次级信息：全部实时派生，不引入额外请求。
  const busyAgents = new Set(
    active.map((g) => (g.assignee_type === "agent" ? g.assignee_id : g.current_agent_id)).filter(Boolean)
  ).size;
  const earliest = (gs: Goal[]) => {
    const times = gs.map((g) => g.created_at).filter(Boolean).sort();
    return times[0] ? relTime(times[0]) : "—";
  };

  return (
    <PageBody>
      <PageHeader
        title="总览"
        desc="待办卡点、执行中的任务与 agent 负载，一屏看完。"
        action={
          <Link href="/goals">
            <Button variant="outline">全部任务 →</Button>
          </Link>
        }
      />

      {/* 读数行：数据本身中性，状态只出现在小圆点上；大数字用等宽字体。 */}
      <div className="grid grid-cols-2 gap-3 md:grid-cols-4">
        <StatCard
          label="待审批"
          value={review.length}
          dot="bg-purple-400"
          icon={<CircleDot className="h-3.5 w-3.5" />}
          sub={review.length > 0 ? `最早卡点 ${earliest(review)}` : "队列干净"}
        />
        <StatCard
          label="运行中"
          value={active.length}
          dot="bg-green-400"
          icon={<Zap className="h-3.5 w-3.5" />}
          sub={busyAgents > 0 ? `${busyAgents} 个 agent 在执行` : "无活跃执行"}
        />
        <StatCard
          label="已完成"
          value={doneCount}
          dot="bg-emerald-400"
          icon={<CheckCircle2 className="h-3.5 w-3.5" />}
          sub={all.length > 0 ? `占全部任务 ${Math.round((doneCount / all.length) * 100)}%` : "—"}
        />
        <StatCard
          label="失败"
          value={failed.length}
          dot={failed.length > 0 ? "bg-red-400" : "bg-zinc-500"}
          icon={<AlertTriangle className="h-3.5 w-3.5" />}
          sub={failed.length > 0 ? "需要处理" : "无异常"}
        />
      </div>

      {/* 待审批：本页最重要的一块，占满宽度，行内直接给「去审批」通路。 */}
      <SectionCard
        title="待审批"
        count={review.length}
        desc="agent 停下来等你拍板"
        action={
          <Link
            href="/goals?status=review"
            className="flex items-center gap-1 text-xs text-ink-3 transition-colors hover:text-accent"
          >
            查看全部 <ArrowRight className="h-3 w-3" />
          </Link>
        }
      >
        {review.length === 0 ? (
          <Empty>没有待审批的卡点——队列是干净的。</Empty>
        ) : (
          <div className="divide-y divide-line list-enter">
            {review.map((g) => (
              <Link
                key={g.id}
                href={`/goals/${g.id}`}
                className="group flex items-start gap-3 border-l-2 border-l-purple-400/70 px-4 py-3 transition-colors duration-150 hover:border-l-purple-400 hover:bg-surface-2/60"
              >
                <div className="min-w-0 flex-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="font-medium text-ink">{g.title}</span>
                    <Badge status={g.status} />
                    <span className="font-mono text-[11px] text-ink-3">{g.id.slice(0, 8)}</span>
                  </div>
                  <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-xs text-ink-3">
                    <span>负责 {assigneeLabel(g)}</span>
                    {g.created_at && <span className="font-mono">{relTime(g.created_at)}</span>}
                  </div>
                  {g.review_request && (
                    <p className="mt-1.5 line-clamp-2 text-sm text-ink-2">{g.review_request}</p>
                  )}
                </div>
                <span className="shrink-0 text-xs font-medium text-purple-300 opacity-80 transition-opacity group-hover:opacity-100">
                  去审批 →
                </span>
              </Link>
            ))}
          </div>
        )}
      </SectionCard>

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1.15fr)_minmax(0,1fr)]">
        {/* 运行中 */}
        <SectionCard
          title="运行中"
          count={active.length}
          desc="agent 正在执行"
          action={
            <Link href="/goals" className="flex items-center gap-1 text-xs text-ink-3 transition-colors hover:text-accent">
              看板 <ArrowRight className="h-3 w-3" />
            </Link>
          }
        >
          {active.length === 0 ? (
            <Empty>没有正在运行的任务。去「任务」里点亮一张待办卡。</Empty>
          ) : (
            <div className="divide-y divide-line list-enter">
              {active.map((g) => (
                <Link
                  key={g.id}
                  href={`/goals/${g.id}`}
                  className="group flex items-center gap-3 px-4 py-3 transition-colors hover:bg-surface-2"
                >
                  <span className="h-2 w-2 shrink-0 animate-pulse rounded-full bg-green-400" />
                  <div className="min-w-0 flex-1">
                    <div className="truncate text-sm font-medium text-ink">{g.title}</div>
                    <div className="mt-0.5 flex items-center gap-2 text-xs text-ink-3">
                      {g.current_agent_id ? (
                        <span className="text-emerald-300">{agentName(g.current_agent_id)} 正在执行</span>
                      ) : (
                        <span>{assigneeLabel(g)}</span>
                      )}
                      <span className="font-mono text-[11px]">{g.id.slice(0, 8)}</span>
                    </div>
                  </div>
                  <span className="shrink-0 font-mono text-[11px] text-ink-3">
                    {g.created_at ? relTime(g.created_at) : ""}
                  </span>
                </Link>
              ))}
            </div>
          )}
        </SectionCard>

        {/* Agent 概览：chip 网格，和列表形态区分开。 */}
        <SectionCard
          title="Agent 概览"
          count={agents?.length ?? 0}
          desc="谁在线、谁在跑"
          action={
            <Link href="/agents" className="flex items-center gap-1 text-xs text-ink-3 transition-colors hover:text-accent">
              管理 <ArrowRight className="h-3 w-3" />
            </Link>
          }
        >
          {(agents?.length ?? 0) === 0 ? (
            <Empty>还没有 agent。</Empty>
          ) : (
            <div className="grid gap-2 p-3 sm:grid-cols-2 list-enter">
              {agents!.map((a) => {
                const load = active.filter((g) => g.assignee_type === "agent" && g.assignee_id === a.id).length;
                return (
                  <Link
                    key={a.id}
                    href="/agents"
                    className="flex items-center gap-2.5 rounded-md border border-line px-2.5 py-2 transition-colors duration-150 hover:border-line-strong hover:bg-surface-2"
                  >
                    <span className="flex h-7 w-7 shrink-0 items-center justify-center rounded-full bg-surface-3 font-mono text-[11px] text-ink-2">
                      {a.name.slice(0, 2).toUpperCase()}
                    </span>
                    <span className="min-w-0 flex-1">
                      <span className="flex items-center gap-1.5">
                        <span className="max-w-28 truncate text-sm font-medium text-ink">{a.name}</span>
                        {a.type === "steward" && (
                          <span className="rounded border border-accent/40 px-1 font-mono text-[10px] leading-4 text-accent">
                            管家
                          </span>
                        )}
                      </span>
                      <span className="block truncate font-mono text-[10px] text-ink-3">{a.model}</span>
                    </span>
                    {load > 0 ? (
                      <span className="shrink-0 text-xs font-medium text-green-300">{load} 跑</span>
                    ) : (
                      <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-zinc-500" title="待命" />
                    )}
                  </Link>
                );
              })}
            </div>
          )}
        </SectionCard>
      </div>

      {/* 最近完成 */}
      {done.length > 0 && (
        <SectionCard
          title="最近完成"
          count={doneCount}
          action={
            <Link
              href="/goals?status=done"
              className="flex items-center gap-1 text-xs text-ink-3 transition-colors hover:text-accent"
            >
              全部 <ArrowRight className="h-3 w-3" />
            </Link>
          }
        >
          <div className="divide-y divide-line list-enter">
            {done.map((g) => (
              <Link
                key={g.id}
                href={`/goals/${g.id}`}
                className="flex items-center gap-3 px-4 py-3 transition-colors hover:bg-surface-2"
              >
                <Badge status="done" />
                <span className="min-w-0 flex-1 truncate text-sm font-medium text-ink">{g.title}</span>
                <span className="shrink-0 font-mono text-[11px] text-ink-3">
                  {g.created_at ? relTime(g.created_at) : ""}
                </span>
              </Link>
            ))}
          </div>
        </SectionCard>
      )}
    </PageBody>
  );
}

function StatCard({
  label,
  value,
  dot,
  icon,
  sub,
}: {
  label: string;
  value: number;
  dot: string;
  icon: React.ReactNode;
  sub?: string;
}) {
  return (
    <div className="rounded-lg border border-line bg-surface-1 p-4 transition-colors duration-150 hover:border-line-strong">
      <div className="flex items-center justify-between">
        <div className="font-mono text-3xl font-semibold tabular-nums text-ink">{value}</div>
        <span className={`h-2 w-2 rounded-full ${dot}`} />
      </div>
      <div className="mt-1.5 flex items-center gap-1.5 text-xs text-ink-3">
        <span className="text-ink-3">{icon}</span>
        {label}
      </div>
      {sub && <div className="mt-1 truncate text-[11px] text-ink-3">{sub}</div>}
    </div>
  );
}