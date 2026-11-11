"use client";

import { useState } from "react";
import Link from "next/link";
import { useParams } from "next/navigation";
import { ArrowUpRight, RefreshCw } from "lucide-react";
import {
  Badge,
  Button,
  Empty,
  PageBody,
  PageHeader,
  SectionCard,
  Segmented,
  Skeleton,
} from "@/components/ui";
import { useRetryRsiSession, useRsiDetail } from "@/lib/queries";
import { TrainingOverview } from "@/components/training/overview";
import { TrainingFindings } from "@/components/training/findings";
import { WorkflowGraphView } from "@/components/training/workflow-graph";
import { TrainingInsights } from "@/components/training/insights";
import { categoryLabel, fmtInt, fmtMs, fmtSpeedup, fmtTime } from "@/components/training/format";
import { cn } from "@/lib/utils";

type Tab = "overview" | "findings" | "workflow" | "insights";

// 会话进行中不提供「重跑」：重跑是失败/跳过后的补救动作，
// 分析没跑完时按钮没有意义，disabled 也能防止误触打乱状态机。
const IN_FLIGHT = ["waiting", "analyzing"];

export default function TrainingDetailPage() {
  const params = useParams<{ id: string }>();
  const raw = params?.id;
  const id = typeof raw === "string" ? raw : "";

  const { data: detail, isLoading, isError, error } = useRsiDetail(id);
  const retry = useRetryRsiSession();
  const [tab, setTab] = useState<Tab>("overview");
  const [retryErr, setRetryErr] = useState("");

  const back = (
    <Link href="/training" className="text-xs text-ink-3 transition-colors hover:text-ink-2">
      ← 返回训练分析
    </Link>
  );

  if (isLoading) {
    return (
      <PageBody>
        <PageHeader title="训练会话" desc="执行分析详情" action={back} />
        <Skeleton className="h-16 w-full" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="h-64 w-full" />
      </PageBody>
    );
  }

  if (isError || !detail) {
    return (
      <PageBody>
        <PageHeader title="训练会话" desc="执行分析详情" action={back} />
        <SectionCard bodyClassName="p-0">
          <Empty>
            找不到这个训练会话，可能已被删除，或后端尚未部署 RSI 接口。
            {isError && (
              <span className="mt-2 block font-mono text-[11px] text-red-300">
                {error instanceof Error ? error.message : String(error)}
              </span>
            )}
          </Empty>
        </SectionCard>
      </PageBody>
    );
  }

  const { session, findings, workflow, report } = detail;
  const findingCount = findings?.length ?? 0;
  const nodeCount = workflow?.nodes?.length ?? session.node_count ?? 0;
  const inFlight = IN_FLIGHT.includes(session.status);

  const onRetry = () => {
    setRetryErr("");
    retry.mutate(id, {
      onError: (e) => setRetryErr(e instanceof Error ? e.message : String(e)),
    });
  };

  return (
    <PageBody>
      <PageHeader
        title={session.goal_title || "训练会话"}
        desc={`会话 ${session.id.slice(0, 8)} · 训练模式的执行分析`}
        action={
          <div className="flex items-center gap-3">
            {back}
            <Button variant="outline" onClick={onRetry} disabled={retry.isPending || inFlight}>
              <RefreshCw
                className={cn("mr-1 h-3.5 w-3.5", retry.isPending && "animate-spin")}
              />
              {retry.isPending ? "重跑中…" : "重跑分析"}
            </Button>
          </div>
        }
      />

      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-xs text-ink-3">
        <Badge status={session.status} />
        <span>开始 {fmtTime(session.started_at)}</span>
        <span>结束 {session.finished_at ? fmtTime(session.finished_at) : "—"}</span>
        <span>{fmtInt(nodeCount)} 节点</span>
        <span>关键路径 {fmtMs(session.critical_path_ms)}</span>
        <span>并行加速 {fmtSpeedup(session.parallel_speedup)}</span>
        <span>发现 {fmtInt(session.finding_count)} 条</span>
        {session.top_category && (
          <span>
            主瓶颈 {session.top_title || categoryLabel(session.top_category)}
          </span>
        )}
        {session.goal_id && (
          <Link
            href={`/goals/${session.goal_id}`}
            className="inline-flex items-center gap-0.5 text-ink-2 transition-colors hover:text-accent"
          >
            查看任务
            <ArrowUpRight className="h-3 w-3" />
          </Link>
        )}
      </div>

      {(session.error || retryErr) && (
        <p className="rounded-md border border-red-500/25 bg-red-500/10 px-3 py-2 font-mono text-[11px] leading-relaxed text-red-300">
          {retryErr ? `重跑失败：${retryErr}` : `会话错误：${session.error}`}
        </p>
      )}

      <div className="flex items-center justify-between">
        <Segmented
          value={tab}
          onChange={setTab}
          options={[
            { value: "overview", label: "概览" },
            { value: "findings", label: `发现${findingCount ? ` ${findingCount}` : ""}` },
            { value: "workflow", label: `编排${nodeCount ? ` ${nodeCount}` : ""}` },
            { value: "insights", label: "洞察" },
          ]}
        />
        <span className="text-[11px] text-ink-3">
          {inFlight
            ? "分析进行中，页面会自动刷新"
            : tab === "insights"
              ? "历史轮次冻结只读；确认/忽略只作用于最新轮"
              : "确认/忽略发现会写回后端"}
        </span>
      </div>

      {tab === "overview" && <TrainingOverview detail={detail} />}
      {tab === "findings" && <TrainingFindings detail={detail} />}
      {tab === "workflow" && (
        <SectionCard
          title="编排图"
          count={nodeCount || undefined}
          desc={
            workflow?.frozen
              ? "任务终态时冻结的只读 DAG；关键路径节点高亮，绿色连线为关键路径"
              : "实时编排图；任务终态后冻结并用于训练分析"
          }
          bodyClassName="p-0"
        >
          {!workflow && session.status === "skipped" ? (
            <Empty>
              本次会话被采样跳过或运行模式为推理，没有冻结编排图。
            </Empty>
          ) : !workflow && session.status === "failed" ? (
            <Empty>
              分析失败，没有可用编排图；可点「重跑分析」重试，或查看错误信息。
            </Empty>
          ) : (
            <WorkflowGraphView
              graph={workflow}
              metrics={report?.metrics ?? null}
              heightClass="min-h-[240px]"
            />
          )}
        </SectionCard>
      )}
      {tab === "insights" && <TrainingInsights detail={detail} />}
    </PageBody>
  );
}