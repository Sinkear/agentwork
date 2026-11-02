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

  return (
    <div className="p-8 space-y-7 max-w-5xl page-enter">
      <PageHeader
        title="总览"
        action={
          <Link href="/goals">
            <Button variant="outline">全部任务 →</Button>
          </Link>
        }
      />

      {/* 统计行：扁平深色卡，数据本身中性，状态只出现在小圆点上。
          大数字用等宽字体——仪表盘读数,不是渐变海报。 */}
      <div className="grid grid-cols-2 md:grid-cols-4 gap-3">
        <StatCard label="待审批" value={review.length} dot="bg-purple-400" icon={<CircleDot className="h-3.5 w-3.5" />} />
        <StatCard label="运行中" value={active.length} dot="bg-green-400" icon={<Zap className="h-3.5 w-3.5" />} />
        <StatCard label="已完成" value={all.filter((g) => g.status === "done").length} dot="bg-emerald-400" icon={<CheckCircle2 className="h-3.5 w-3.5" />} />
        <StatCard label="失败" value={failed.length} dot={failed.length > 0 ? "bg-red-400" : "bg-zinc-600"} icon={<AlertTriangle className="h-3.5 w-3.5" />} />
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

function StatCard({ label, value, dot, icon }: { label: string; value: number; dot: string; icon: React.ReactNode }) {
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
    </div>
  );
}

function SectionTitle({ children }: { children: React.ReactNode }) {
  return <h2 className="font-mono text-[11px] font-medium text-ink-3 uppercase tracking-[0.12em]">{children}</h2>;
}
