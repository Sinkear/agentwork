"use client";

import Link from "next/link";
import { Network } from "lucide-react";
import { Badge, Empty, SectionCard, Skeleton } from "@/components/ui";
import { useSquadWorkflows } from "@/lib/queries";
import { fmtInt, fmtMs, fmtRatio, fmtSpeedup } from "./format";

const ROW_GRID =
  "grid gap-1 px-4 py-2.5 transition-colors hover:bg-surface-2/40 md:grid-cols-[minmax(0,1fr)_80px_56px_56px_92px_72px_72px] md:items-center md:gap-3";
const HEAD_GRID =
  "hidden md:grid md:grid-cols-[minmax(0,1fr)_80px_56px_56px_92px_72px_72px] border-b border-line px-4 py-2 font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3 md:gap-3";

/**
 * squad 详情页的「编排」摘要表：GET /workflows/squads/{id}?limit=10。
 * 摘要行没有 training session id，所以统一跳到 goal 详情——那里有实时 DAG；
 * 训练详情从任务页/列表进入，不在这里猜。
 */
export function SquadWorkflows({ squadId }: { squadId: string }) {
  const { data, isLoading, isError, error } = useSquadWorkflows(squadId, 10);

  return (
    <SectionCard
      title="编排"
      count={data?.length ?? 0}
      desc="最近 10 个任务的编排图摘要；点击进入任务详情查看实时 DAG"
      bodyClassName="p-0"
    >
      {isLoading ? (
        <div className="space-y-2 px-4 py-4">
          {[0, 1, 2].map((i) => (
            <div key={i} className="flex items-center gap-3">
              <Skeleton className="h-4 w-1/3" />
              <Skeleton className="h-3 w-1/4" />
            </div>
          ))}
        </div>
      ) : isError ? (
        <Empty icon={<Network className="h-4 w-4" />}>
          读取编排摘要失败：
          {error instanceof Error ? error.message : String(error)}
        </Empty>
      ) : !data?.length ? (
        <Empty icon={<Network className="h-4 w-4" />}>
          暂无编排数据。任务拆出子任务、产生验证/咨询记录后，这里会出现近期 DAG 摘要。
        </Empty>
      ) : (
        <>
          <div className={HEAD_GRID}>
            <span>任务</span>
            <span>状态</span>
            <span className="text-right">节点</span>
            <span className="text-right">边</span>
            <span className="text-right">关键路径</span>
            <span className="text-right">加速</span>
            <span className="text-right">返工率</span>
          </div>
          <ul className="divide-y divide-line">
            {data.map((w) => (
              <li key={w.goal_id}>
                <Link href={`/goals/${w.goal_id}`} className={ROW_GRID}>
                  <div className="min-w-0">
                    <span className="block truncate text-sm font-medium text-ink">
                      {w.goal_title || w.goal_id}
                    </span>
                    <span className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-ink-3 md:hidden">
                      <Badge status={w.status} />
                      <span>{fmtInt(w.node_count)} 节点</span>
                      <span>{fmtMs(w.critical_path_ms)}</span>
                      <span>{fmtSpeedup(w.parallel_speedup)}</span>
                      <span>返工 {fmtRatio(w.rework_ratio)}</span>
                    </span>
                  </div>
                  <span className="hidden md:block">
                    <Badge status={w.status} />
                  </span>
                  <span className="hidden text-right font-mono text-xs tabular-nums text-ink-2 md:block">
                    {fmtInt(w.node_count)}
                  </span>
                  <span className="hidden text-right font-mono text-xs tabular-nums text-ink-2 md:block">
                    {fmtInt(w.edge_count)}
                  </span>
                  <span className="hidden text-right font-mono text-xs tabular-nums text-ink-2 md:block">
                    {fmtMs(w.critical_path_ms)}
                  </span>
                  <span className="hidden text-right font-mono text-xs tabular-nums text-ink-2 md:block">
                    {fmtSpeedup(w.parallel_speedup)}
                  </span>
                  <span className="hidden text-right font-mono text-xs tabular-nums text-ink-2 md:block">
                    {fmtRatio(w.rework_ratio)}
                  </span>
                </Link>
              </li>
            ))}
          </ul>
        </>
      )}
    </SectionCard>
  );
}