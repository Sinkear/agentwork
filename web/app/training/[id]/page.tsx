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
import {
  categoryLabel,
  fmtInt,
  fmtMs,
  fmtSpeedup,
  fmtTime,
  measured,
} from "@/components/training/format";
import { cn } from "@/lib/utils";

type Tab = "overview" | "findings" | "workflow" | "insights";

// 重跑是失败/跳过后的补救动作，但后端 Retry 只接受 waiting/failed/ready
// 三种状态（internal/service/rsi.go:226），对 skipped 会直接 400。
// 所以按钮可见性必须严格按状态来，不能“没在跑就显示”。
const RETRYABLE = ["waiting", "failed", "ready"];

// 分析进行中页面靠轮询刷新；这里只用于文案。
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
  const status = session.status;
  const findingCount = findings?.length ?? 0;
  // 只有 ready 才算“当前报告已就绪”：analyzing 会返回旧 report，
  // 不能拿它当数字来源（否则重跑期间会显示过期指标）。
  const ready = status === "ready" && report != null;
  // 0 表示“还没测到”，统一显示「—」而不是真实的 0；非 ready 状态下
  // training_session 里可能残留上一次分析的列值，也不当作当前结果展示。
  const displayNodeCount = ready ? workflow?.nodes?.length ?? measured(session.node_count) : null;
  const displayFindingCount = ready ? findingCount : null;
  const findingText =
    displayFindingCount === null ? "尚无数据" : `${fmtInt(displayFindingCount)} 条`;
  const displayCp = ready ? measured(session.critical_path_ms) : null;
  const displaySpeedup = ready ? measured(session.parallel_speedup) : null;
  const canRetry = RETRYABLE.includes(status);
  const inFlight = IN_FLIGHT.includes(status);

  // 后端 GetSession 现在会回填 top_category/top_title；findings 最高分的兜底
  // 仍然保留，用于旧数据或两者不一致时。
  const topFinding = [...(findings ?? [])].sort((a, b) => {
    const sa = typeof a.score === "number" && Number.isFinite(a.score) ? a.score : -Infinity;
    const sb = typeof b.score === "number" && Number.isFinite(b.score) ? b.score : -Infinity;
    if (sb !== sa) return sb - sa;
    return a.id.localeCompare(b.id);
  })[0];
  const topCategory = session.top_category || (ready ? topFinding?.category : "") || "";
  const topTitle =
    session.top_title || (ready && topCategory === topFinding?.category ? topFinding?.title : "");

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
            {canRetry ? (
              <Button
                variant="outline"
                onClick={onRetry}
                disabled={retry.isPending}
              >
                <RefreshCw
                  className={cn("mr-1 h-3.5 w-3.5", retry.isPending && "animate-spin")}
                />
                {retry.isPending ? "重跑中…" : "重跑分析"}
              </Button>
            ) : (
              <span className="text-[11px] text-ink-3">
                {status === "analyzing" ? "分析中，完成后可重跑" : "已跳过，无法重跑"}
              </span>
            )}
          </div>
        }
      />

      <div className="flex flex-wrap items-center gap-x-3 gap-y-2 text-xs text-ink-3">
        <Badge status={status} />
        <span>开始 {fmtTime(session.started_at)}</span>
        <span>结束 {session.finished_at ? fmtTime(session.finished_at) : "—"}</span>
        <span>{fmtInt(displayNodeCount)} 节点</span>
        <span>关键路径 {fmtMs(displayCp)}</span>
        <span>并行加速 {fmtSpeedup(displaySpeedup)}</span>
        <span>发现 {findingText}</span>
        {topCategory && (
          <span>主瓶颈 {topTitle || categoryLabel(topCategory)}</span>
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

      {status === "skipped" && (
        <p className="rounded-md border border-line bg-surface-2 px-3 py-2 text-[11px] leading-relaxed text-ink-3">
          本次会话被跳过：运行模式为推理（infer），或采样未命中（会话采样率
          {session.sample_rate === 1 ? "100%" : `${Math.round(session.sample_rate * 100)}%`}
          ），没有执行训练分析，因此没有报告与图快照。重跑只对 waiting / failed / ready
          开放，跳过会话不可重跑。
        </p>
      )}

      {status === "failed" && (
        <p className="rounded-md border border-red-500/25 bg-red-500/10 px-3 py-2 font-mono text-[11px] leading-relaxed text-red-300">
          {retryErr
            ? `重跑失败：${retryErr}`
            : `分析失败：${session.error || "后端未返回错误原因"}。旧报告已隐藏，可点「重跑分析」重试。`}
        </p>
      )}

      {retryErr && status !== "failed" && (
        <p className="rounded-md border border-red-500/25 bg-red-500/10 px-3 py-2 font-mono text-[11px] leading-relaxed text-red-300">
          重跑失败：{retryErr}
        </p>
      )}

      <div className="flex items-center justify-between">
        <Segmented
          value={tab}
          onChange={setTab}
          options={[
            { value: "overview", label: "概览" },
            { value: "findings", label: `发现${displayFindingCount ? ` ${displayFindingCount}` : ""}` },
            { value: "workflow", label: `编排${displayNodeCount ? ` ${displayNodeCount}` : ""}` },
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
          count={displayNodeCount || undefined}
          desc={
            status === "analyzing"
              ? workflow?.frozen
                ? "重跑分析中；下方是上一次分析冻结的快照，完成后才会更新为本次快照，旧报告指标暂不展示"
                : "当前实时编排图；分析完成后才会冻结为只读快照，分析期间不展示报告指标"
              : status === "waiting"
                ? "任务进入终态后会冻结编排图并用于训练分析"
                : status === "failed"
                  ? "分析失败；下方图仅用于查看编排结构，旧报告指标已隐藏"
                  : workflow?.frozen
                    ? "任务终态时冻结的只读 DAG；关键路径节点高亮，绿色连线为关键路径"
                    : "实时编排图；任务终态后冻结并用于训练分析"
          }
          bodyClassName="p-0"
        >
          {!workflow && status === "skipped" ? (
            <Empty>
              本次会话被采样跳过或运行模式为推理，没有冻结编排图。
            </Empty>
          ) : !workflow && status === "failed" ? (
            <Empty>
              分析失败，没有可用编排图；可点「重跑分析」重试，或查看错误信息。
            </Empty>
          ) : (
            <WorkflowGraphView
              graph={workflow}
              metrics={ready ? report?.metrics ?? null : null}
              analyzing={status === "analyzing"}
              showMetrics={ready}
              heightClass="min-h-[240px]"
            />
          )}
        </SectionCard>
      )}
      {tab === "insights" && <TrainingInsights detail={detail} />}
    </PageBody>
  );
}