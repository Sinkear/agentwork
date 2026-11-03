"use client";

import Link from "next/link";
import { AlertTriangle, CheckCircle2, CircleDot, Zap } from "lucide-react";
import { useGoals, useAgents, useSquads, useGoalEvents } from "@/lib/queries";
import { Badge, PageHeader, Button, Empty } from "@/components/ui";
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

  // 统计卡次级信息：全部实时派生，不引入额外请求。
  const busyAgents = new Set(
    active.map((g) => (g.assignee_type === "agent" ? g.assignee_id : g.current_agent_id)).filter(Boolean)
  ).size;
  const earliest = (gs: Goal[]) => {
    const times = gs.map((g) => g.created_at).filter(Boolean).sort();
    return times[0]
      ? new Date(times[0]).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" })
      : "—";
  };

  return (
    <div className="p-8 space-y-8 max-w-6xl page-enter">
      <PageHeader
        title="总览"
        desc="待办卡点、执行中的任务与 agent 负载，一屏看完。"
        action={
          <Link href="/goals">
            <Button variant="outline">全部任务 →</Button>
          </Link>
        }
      />

      {/* 统计行：扁平深色卡，数据本身中性，状态只出现在小圆点上。
          大数字用等宽字体——仪表盘读数；次级行给一条实时派生的补充信息。 */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <StatCard label="待审批" value={review.length} dot="bg-purple-400" icon={<CircleDot className="h-3.5 w-3.5" />}
          sub={review.length > 0 ? `最早卡点：${earliest(review)}` : "队列干净"} />
        <StatCard label="运行中" value={active.length} dot="bg-green-400" icon={<Zap className="h-3.5 w-3.5" />}
          sub={busyAgents > 0 ? `${busyAgents} 个 agent 在执行` : "无活跃执行"} />
        <StatCard label="已完成" value={all.filter((g) => g.status === "done").length} dot="bg-emerald-400" icon={<CheckCircle2 className="h-3.5 w-3.5" />}
          sub={all.length > 0 ? `占全部任务 ${Math.round((all.filter((g) => g.status === "done").length / all.length) * 100)}%` : "—"} />
        <StatCard label="失败" value={failed.length} dot={failed.length > 0 ? "bg-red-400" : "bg-zinc-600"} icon={<AlertTriangle className="h-3.5 w-3.5" />}
          sub={failed.length > 0 ? "需要处理" : "无异常"} />
      </div>

      {/* 待审批队列 */}
      <section className="space-y-3">
        <SectionTitle>
          待审批
          {review.length > 0 && <span className="ml-1.5 text-purple-300 font-semibold">{review.length}</span>}
        </SectionTitle>
        {review.length === 0 ? (
          <Empty>没有待审批的卡点——都干净。</Empty>
        ) : (
          <div className="space-y-2 list-enter">
            {review.map((g) => (
              <Link
                key={g.id}
                href={`/goals/${g.id}`}
                className="group block bg-surface-1 rounded-lg border border-line border-l-2 border-l-purple-400/70 hover:border-purple-500/40 hover:border-l-purple-400 hover:bg-surface-2/60 transition-colors duration-150 p-4"
              >
                <div className="flex items-center gap-2 flex-wrap">
                  <span className="font-medium text-ink">{g.title}</span>
                  <Badge status={g.status} />
                  <span className="text-xs text-ink-3">负责:{assigneeLabel(g)}</span>
                  <span className="ml-auto text-xs text-purple-300 font-medium opacity-80 group-hover:opacity-100 transition-opacity">去审批 →</span>
                </div>
                {g.review_request && (
                  <p className="mt-1.5 text-sm text-ink-2 line-clamp-2">{g.review_request}</p>
                )}
              </Link>
            ))}
          </div>
        )}
      </section>

      {/* 活跃任务 */}
      <section className="space-y-3">
        <SectionTitle>运行中({active.length})</SectionTitle>
        {active.length === 0 ? (
          <Empty>没有正在运行的任务。</Empty>
        ) : (
          <div className="bg-surface-1 rounded-lg border border-line divide-y divide-line overflow-hidden list-enter">
            {active.map((g) => (
              <Link key={g.id} href={`/goals/${g.id}`} className="flex items-center gap-3 p-3.5 hover:bg-surface-2 transition-colors group">
                <span className="h-2 w-2 rounded-full bg-green-400 animate-pulse shrink-0" />
                <span className="font-medium text-sm text-ink">{g.title}</span>
                {g.current_agent_id ? (
                  <span className="text-xs font-medium text-emerald-300">{agentName(g.current_agent_id)} 正在执行</span>
                ) : (
                  <span className="text-xs text-ink-3">{assigneeLabel(g)}</span>
                )}
                <span className="text-xs text-ink-3 ml-auto font-mono">
                  {g.created_at ? new Date(g.created_at).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" }) : ""}
                </span>
              </Link>
            ))}
          </div>
        )}
      </section>

      {/* Agent 概览：与列表不同的形态——chip 网格。每个 agent 一枚：
          头像首字母 + 名字 + 型号，右侧执行中数量或待命点。 */}
      {(agents?.length ?? 0) > 0 && (
        <section className="space-y-3">
          <SectionTitle>Agent 概览({agents!.length})</SectionTitle>
          <div className="flex flex-wrap gap-2 list-enter">
            {agents!.map((a) => {
              const load = active.filter((g) => g.assignee_type === "agent" && g.assignee_id === a.id).length;
              return (
                <Link
                  key={a.id}
                  href={`/agents`}
                  className="flex items-center gap-2.5 bg-surface-1 border border-line rounded-lg px-3 py-2 hover:border-line-strong hover:bg-surface-2 transition-colors duration-150"
                >
                  <span className="h-8 w-8 rounded-full bg-surface-3 text-ink-2 text-xs font-mono flex items-center justify-center shrink-0">
                    {a.name.slice(0, 2).toUpperCase()}
                  </span>
                  <span className="min-w-0">
                    <span className="flex items-center gap-1.5">
                      <span className="text-sm font-medium text-ink truncate max-w-32">{a.name}</span>
                      {a.type === "steward" && (
                        <span className="text-[10px] font-mono text-accent border border-accent/40 rounded px-1 leading-4">管家</span>
                      )}
                    </span>
                    <span className="block text-[11px] font-mono text-ink-3 truncate max-w-40">{a.model}</span>
                  </span>
                  {load > 0 ? (
                    <span className="ml-1 text-xs font-medium text-green-300 shrink-0">{load} 执行中</span>
                  ) : (
                    <span className="ml-1 h-1.5 w-1.5 rounded-full bg-zinc-500 shrink-0" title="待命" />
                  )}
                </Link>
              );
            })}
          </div>
        </section>
      )}

      {/* 最近完成 */}
      {done.length > 0 && (
        <section className="space-y-3">
          <SectionTitle>最近完成</SectionTitle>
          <div className="bg-surface-1 rounded-lg border border-line divide-y divide-line overflow-hidden list-enter">
            {done.map((g) => (
              <Link key={g.id} href={`/goals/${g.id}`} className="flex items-center gap-3 p-3.5 hover:bg-surface-2 transition-colors group">
                <Badge status="done" />
                <span className="font-medium text-sm text-ink">{g.title}</span>
                <span className="text-xs text-ink-3 ml-auto font-mono">
                  {g.created_at ? new Date(g.created_at).toLocaleString("zh-CN") : ""}
                </span>
              </Link>
            ))}
          </div>
        </section>
      )}
    </div>
  );
}

function StatCard({ label, value, dot, icon, sub }: { label: string; value: number; dot: string; icon: React.ReactNode; sub?: string }) {
  return (
    <div className="bg-surface-1 border border-line rounded-lg p-4 transition-colors duration-150 hover:border-line-strong">
      <div className="flex items-center justify-between">
        <div className="text-3xl font-semibold font-mono tabular-nums text-ink">{value}</div>
        <span className={`h-2 w-2 rounded-full ${dot}`} />
      </div>
      <div className="flex items-center gap-1.5 text-xs mt-1.5 text-ink-3">
        <span className="text-ink-3">{icon}</span>
        {label}
      </div>
      {sub && <div className="text-[11px] text-ink-3 mt-1 truncate">{sub}</div>}
    </div>
  );
}

function SectionTitle({ children }: { children: React.ReactNode }) {
  return <h2 className="font-mono text-[11px] font-medium text-ink-3 uppercase tracking-[0.12em]">{children}</h2>;
}
