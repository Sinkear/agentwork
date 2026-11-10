"use client";

import Link from "next/link";
import { ArrowRight } from "lucide-react";
import { useGoals, useAgents, useSquads, useGoalEvents } from "@/lib/queries";
import { Badge, PageBody, PageHeader, Button, Empty, SectionCard } from "@/components/ui";
import { relTime } from "@/lib/utils";
import type { Goal } from "@/lib/types";

// 总览只回答三件事：什么等我拍板（待审批）、什么在跑（运行中/失败）、刚做完什么（最近完成）。
// 全部读数压成页首一行状态条，不再用大数字卡占屏；agent 负载只在运行中底部留一行摘要。
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
  const failed = all.filter((g) => g.status === "failed");
  const done = all.filter((g) => g.status === "done").slice(0, 5);
  const doneCount = all.filter((g) => g.status === "done").length;

  // 状态条右侧 + 运行中底部的 agent 负载摘要，全部实时派生。
  const agentLoad = new Map<string, number>();
  for (const g of active) {
    const aid = g.assignee_type === "agent" ? g.assignee_id : g.current_agent_id;
    if (aid) agentLoad.set(aid, (agentLoad.get(aid) ?? 0) + 1);
  }
  const busy = [...agentLoad.entries()];

  return (
    <PageBody>
      <PageHeader
        title="总览"
        desc="等我拍板的、正在跑的、刚做完的。"
        action={
          <Link href="/goals">
            <Button variant="outline">全部任务 →</Button>
          </Link>
        }
      />

      {/* 读数条：一行代替四张统计卡；数据中性，状态只出现在小圆点上。 */}
      <div className="flex flex-wrap items-center gap-x-6 gap-y-1.5 rounded-lg border border-line bg-surface-1 px-4 py-2.5 font-mono text-xs text-ink-3">
        <span className="flex items-center gap-1.5">
          <span className="h-2 w-2 rounded-full bg-purple-400" />
          待审批 <span className="font-semibold text-ink">{review.length}</span>
        </span>
        <span className="flex items-center gap-1.5">
          <span className="h-2 w-2 rounded-full bg-green-400" />
          运行中 <span className="font-semibold text-ink">{active.length}</span>
        </span>
        <span className="flex items-center gap-1.5">
          <span className={`h-2 w-2 rounded-full ${failed.length > 0 ? "bg-red-400" : "bg-zinc-500"}`} />
          失败 <span className={`font-semibold ${failed.length > 0 ? "text-red-300" : "text-ink"}`}>{failed.length}</span>
        </span>
        <span className="flex items-center gap-1.5">
          <span className="h-2 w-2 rounded-full bg-emerald-400" />
          已完成 <span className="font-semibold text-ink">{doneCount}</span>
          <span className="text-ink-3">/ {all.length}</span>
        </span>
        <span className="ml-auto hidden sm:inline">
          {busy.length > 0 ? `${busy.length} 个 agent 在执行` : "无活跃执行"}
        </span>
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

      {/* 失败：只在有失败时出现，可操作信息不该只藏在读数里。 */}
      {failed.length > 0 && (
        <SectionCard
          title="失败"
          count={failed.length}
          desc="需要重试或归档"
          action={
            <Link
              href="/goals?status=failed"
              className="flex items-center gap-1 text-xs text-ink-3 transition-colors hover:text-accent"
            >
              全部 <ArrowRight className="h-3 w-3" />
            </Link>
          }
        >
          <div className="divide-y divide-line list-enter">
            {failed.map((g) => (
              <Link
                key={g.id}
                href={`/goals/${g.id}`}
                className="flex items-center gap-3 border-l-2 border-l-red-400/70 px-4 py-2.5 transition-colors hover:bg-surface-2"
              >
                <Badge status="failed" />
                <span className="min-w-0 flex-1 truncate text-sm font-medium text-ink">{g.title}</span>
                <span className="shrink-0 font-mono text-[11px] text-ink-3">
                  {g.created_at ? relTime(g.created_at) : ""}
                </span>
              </Link>
            ))}
          </div>
        </SectionCard>
      )}

      {/* 运行中：agent 网格撤出总览（完整页在 /agents），只留底部一行负载摘要。 */}
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
          <>
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
            {busy.length > 0 && (
              <div className="border-t border-line px-4 py-2 text-xs text-ink-3">
                负载：
                {busy.map(([aid, n], i) => (
                  <span key={aid}>
                    {i > 0 && "、"}
                    <span className="text-ink-2">{agentName(aid)}</span> ×{n}
                  </span>
                ))}
                <Link href="/agents" className="ml-3 text-ink-3 transition-colors hover:text-accent">
                  管理全部 →
                </Link>
              </div>
            )}
          </>
        )}
      </SectionCard>

      {/* 最近完成：只保留最近 5 条作为上下文，完整历史在任务页。 */}
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
