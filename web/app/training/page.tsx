"use client";

import { useMemo, useState } from "react";
import Link from "next/link";
import { BarChart3, Plus } from "lucide-react";
import {
  Badge,
  Button,
  Empty,
  PageBody,
  PageHeader,
  SectionCard,
  Segmented,
  Skeleton,
  StatStrip,
  Toolbar,
} from "@/components/ui";
import { useRsiOverview, useRsiSessions } from "@/lib/queries";
import type { RsiSession, RsiSessionStatus } from "@/lib/types";
import { relTime } from "@/lib/utils";
import { TrainingTrend } from "@/components/training/trend-strip";
import {
  DASH,
  categoryLabel,
  fmtInt,
  fmtMs,
  fmtRatio,
  fmtSpeedup,
  measured,
} from "@/components/training/format";

// 状态筛选把中间态折成「进行中」：用户关心的是“还没好 / 好了 / 失败”，
// 而不是 waiting/analyzing 两段实现细节（详情页会显示精确状态）。
type StatusFilter = "all" | "active" | "ready" | "failed" | "skipped";
type RangeFilter = "all" | "7" | "30";

const STATUS_OPTIONS: { value: StatusFilter; label: string }[] = [
  { value: "all", label: "全部" },
  { value: "active", label: "进行中" },
  { value: "ready", label: "已分析" },
  { value: "failed", label: "失败" },
  { value: "skipped", label: "已跳过" },
];

const RANGE_OPTIONS: { value: RangeFilter; label: string }[] = [
  { value: "30", label: "30 天" },
  { value: "7", label: "7 天" },
  { value: "all", label: "全部" },
];

const ACTIVE_STATUSES: RsiSessionStatus[] = ["waiting", "analyzing"];

// 列宽：任务名自适应，其余定宽；窄屏下只保留任务名 + 一行摘要。
const ROW_GRID =
  "grid gap-1.5 px-4 py-3 transition-colors hover:bg-surface-2/40 md:grid-cols-[minmax(0,1fr)_92px_82px_56px_80px_68px_56px_minmax(0,150px)] md:items-center md:gap-3";
const HEAD_GRID =
  "hidden md:grid md:grid-cols-[minmax(0,1fr)_92px_82px_56px_80px_68px_56px_minmax(0,150px)] border-b border-line px-4 py-2 font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3 md:gap-3";

function fullTime(iso: string): string {
  if (!iso) return DASH;
  const t = new Date(iso);
  return Number.isNaN(t.getTime()) ? iso : t.toLocaleString("zh-CN");
}

function matchesStatus(status: RsiSessionStatus, filter: StatusFilter): boolean {
  switch (filter) {
    case "all":
      return true;
    case "active":
      return ACTIVE_STATUSES.includes(status);
    default:
      return status === filter;
  }
}

function TrainingRow({ session }: { session: RsiSession }) {
  const time = session.started_at ? relTime(session.started_at) : DASH;
  // 只有 ready 才有“当前报告”。waiting/skipped 的列值是 0，analyzing/failed
  // 则可能残留上一次分析的列值——都不是当前结果，统一显示「—」。
  // ready 时 finding_count=0 是真实结果，保留 0。
  const ready = session.status === "ready";
  const nodeValue = ready ? fmtInt(measured(session.node_count)) : DASH;
  const cpValue = ready ? fmtMs(measured(session.critical_path_ms)) : DASH;
  const speedupValue = ready ? fmtSpeedup(measured(session.parallel_speedup)) : DASH;
  const findingValue = ready ? fmtInt(session.finding_count) : DASH;
  const topCategory = ready ? session.top_category : "";
  return (
    <li>
      <Link href={`/training/${session.id}`} className={ROW_GRID}>
        <div className="min-w-0">
          <div className="truncate text-sm font-medium text-ink">
            {session.goal_title || "（未命名任务）"}
          </div>
          {/* 窄屏摘要行：状态/时间/数量压成一行，桌面端由各列分别显示。 */}
          <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-ink-3 md:hidden">
            <Badge status={session.status} />
            <span>{time}</span>
            <span>{nodeValue} 节点</span>
            <span>关键路径 {cpValue}</span>
            <span>{speedupValue}</span>
            <span>{findingValue} 发现</span>
            {topCategory && <span>{categoryLabel(topCategory)}</span>}
          </div>
        </div>

        <span className="hidden text-xs text-ink-2 md:block" title={fullTime(session.started_at)}>
          {time}
        </span>

        <span className="hidden md:block">
          <Badge status={session.status} />
        </span>

        <span className="hidden text-right font-mono text-xs tabular-nums text-ink-2 md:block">
          {nodeValue}
        </span>

        <span className="hidden text-right font-mono text-xs tabular-nums text-ink-2 md:block">
          {cpValue}
        </span>

        <span className="hidden text-right font-mono text-xs tabular-nums text-ink-2 md:block">
          {speedupValue}
        </span>

        <span className="hidden text-right font-mono text-xs tabular-nums text-ink-2 md:block">
          {findingValue}
        </span>

        <span className="hidden min-w-0 md:block">
          {topCategory ? (
            <>
              <span className="block truncate text-xs text-ink-2">
                {session.top_title || categoryLabel(topCategory)}
              </span>
              <span className="block font-mono text-[10px] text-ink-3">
                {categoryLabel(topCategory)}
                {session.error ? " · 有错误" : ""}
              </span>
            </>
          ) : (
            <span className="text-xs text-ink-3">{DASH}</span>
          )}
        </span>
      </Link>
    </li>
  );
}

export default function TrainingPage() {
  const { data: sessions, isLoading, isError, error } = useRsiSessions();
  const [statusFilter, setStatusFilter] = useState<StatusFilter>("all");
  const [range, setRange] = useState<RangeFilter>("30");

  // 概览跟随时间范围（契约 days 参数），状态筛选只作用于列表。
  const days = range === "7" ? 7 : range === "30" ? 30 : 365;
  const {
    data: overview,
    isLoading: overviewLoading,
    isError: overviewError,
    error: overviewErr,
    refetch: refetchOverview,
  } = useRsiOverview(days);

  const visible = useMemo(() => {
    const cutoff =
      range === "all" ? null : Date.now() - Number(range) * 24 * 60 * 60 * 1000;
    return (sessions ?? [])
      .filter((s) => matchesStatus(s.status, statusFilter))
      .filter((s) => {
        if (cutoff === null) return true;
        const t = Date.parse(s.started_at);
        return !Number.isNaN(t) && t >= cutoff;
      })
      .sort((a, b) => {
        const ta = Date.parse(a.started_at);
        const tb = Date.parse(b.started_at);
        return (Number.isNaN(tb) ? 0 : tb) - (Number.isNaN(ta) ? 0 : ta);
      });
  }, [sessions, statusFilter, range]);

  const rangeLabel = range === "7" ? "近 7 天" : range === "30" ? "近 30 天" : "全部时间";
  // overview 在没有任何会话时各项都是 0；显示「0% / 0.00×」会比「—」更像真实值。
  // 查询失败时同样不能用 0 冒充数据，全部退化为「—」并给出重试入口。
  const hasSessions = !overviewError && (overview?.sessions ?? 0) > 0;
  const successRate = hasSessions ? overview?.success_rate ?? null : null;
  const avgSpeedup = hasSessions ? overview?.avg_parallel_speedup ?? null : null;
  const avgRework = hasSessions ? overview?.avg_rework_ratio ?? null : null;
  const overviewUnavailable = overviewLoading || overviewError;

  return (
    <PageBody>
      <PageHeader
        title="训练分析"
        desc="训练模式会话的编排分析：冻结 DAG、关键路径与结构瓶颈。推理模式只记录基础指标，不产生训练分析。"
        action={
          <Link
            href="/goals/new"
            className="inline-flex items-center justify-center rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink transition-colors duration-150 hover:bg-accent-dim"
          >
            <Plus className="mr-1 h-4 w-4" />
            新建任务
          </Link>
        }
      />

      {overviewError && (
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2 rounded-md border border-red-500/25 bg-red-500/10 px-3.5 py-2 text-[11px] text-red-300">
          <span className="min-w-0 flex-1">
            训练概览读取失败：
            {overviewErr instanceof Error ? overviewErr.message : String(overviewErr)}
          </span>
          <Button
            variant="outline"
            className="h-6 px-2 text-[11px]"
            onClick={() => void refetchOverview()}
          >
            重试
          </Button>
        </div>
      )}

      <StatStrip
        items={[
          {
            label: "会话数",
            value: overviewUnavailable ? DASH : fmtInt(overview?.sessions ?? null),
            hint:
              range === "all"
                ? "训练会话（不含跳过；概览上限 365 天）"
                : `${rangeLabel}的训练会话`,
          },
          {
            label: "成功率",
            value: overviewUnavailable ? DASH : fmtRatio(successRate),
            tone:
              successRate === null
                ? "default"
                : successRate >= 0.8
                  ? "ok"
                  : successRate <= 0.5
                    ? "warn"
                    : "default",
            hint: "目标完成 / 全部会话（不含跳过）",
          },
          {
            label: "平均并行加速",
            value: overviewUnavailable ? DASH : fmtSpeedup(avgSpeedup),
            tone: avgSpeedup !== null && avgSpeedup >= 1.5 ? "ok" : avgSpeedup !== null && avgSpeedup < 1.05 ? "warn" : "default",
            hint: "总工作量 / 关键路径；1× = 完全串行",
          },
          {
            label: "平均返工率",
            value: overviewUnavailable ? DASH : fmtRatio(avgRework),
            tone: avgRework !== null && avgRework >= 0.3 ? "warn" : "default",
            hint: "验证 rejected / 总验证轮次",
          },
        ]}
      />

      <div className="flex flex-wrap items-center gap-x-4 gap-y-1 rounded-md border border-line bg-surface-2/25 px-3.5 py-2 text-[11px] text-ink-3">
        <span>
          人工等待{" "}
          <span className="font-mono text-ink-2">
            {fmtMs(overviewUnavailable ? null : overview?.human_wait_ms ?? null)}
          </span>
        </span>
        <span>
          验证开销{" "}
          <span className="font-mono text-ink-2">
            {fmtRatio(overviewUnavailable ? null : overview?.avg_verify_overhead_ratio ?? null)}
          </span>
        </span>
        <span>
          结构发现{" "}
          <span className="font-mono text-ink-2">
            {overviewUnavailable ? DASH : fmtInt(overview?.findings ?? null)}
          </span>
        </span>
        <span>
          P95 交付耗时{" "}
          <span className="font-mono text-ink-2">
            {fmtMs(hasSessions ? overview?.p95_lead_time_ms ?? null : null)}
          </span>
        </span>
      </div>

      {!overviewError && <TrainingTrend trend={overview?.trend} loading={overviewLoading} />}

      <SectionCard
        title="会话"
        count={visible.length}
        desc={`${rangeLabel} · 按开始时间倒序`}
        bodyClassName="p-0"
      >
        <div className="border-b border-line px-4 py-2.5">
          <Toolbar>
            <Segmented value={statusFilter} onChange={setStatusFilter} options={STATUS_OPTIONS} />
            <div className="ml-auto flex items-center gap-2">
              <span className="text-[11px] text-ink-3">时间范围</span>
              <Segmented value={range} onChange={setRange} options={RANGE_OPTIONS} />
            </div>
          </Toolbar>
        </div>

        {isLoading ? (
          <div className="divide-y divide-line">
            {[0, 1, 2, 3].map((i) => (
              <div key={i} className="space-y-2 px-4 py-4">
                <Skeleton className="h-4 w-1/3" />
                <Skeleton className="h-3 w-2/3" />
              </div>
            ))}
          </div>
        ) : isError ? (
          <Empty icon={<BarChart3 className="h-5 w-5" />}>
            读取训练会话失败：{error instanceof Error ? error.message : String(error)}
          </Empty>
        ) : !sessions?.length ? (
          <Empty icon={<BarChart3 className="h-5 w-5" />}>
            还没有训练分析会话。在「新建任务」里把运行模式切到「训练」，任务跑完后会自动分析并出现在这里。
            <Link href="/goals/new" className="mt-3 block text-xs text-accent-dim hover:text-accent">
              去新建任务 →
            </Link>
          </Empty>
        ) : !visible.length ? (
          <Empty>当前筛选条件下没有会话；换个状态或时间范围看看。</Empty>
        ) : (
          <>
            <div className={HEAD_GRID}>
              <span>任务</span>
              <span>开始</span>
              <span>状态</span>
              <span className="text-right">节点</span>
              <span className="text-right">关键路径</span>
              <span className="text-right">加速</span>
              <span className="text-right">发现</span>
              <span>主瓶颈</span>
            </div>
            <ul className="divide-y divide-line">
              {visible.map((s) => (
                <TrainingRow key={s.id} session={s} />
              ))}
            </ul>
          </>
        )}
      </SectionCard>
    </PageBody>
  );
}