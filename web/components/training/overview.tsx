"use client";

import { SectionCard, StatStrip } from "@/components/ui";
import type { RsiDetail } from "@/lib/types";
import { WorkflowMetricsRow } from "./workflow-graph";
import { fmtInt, fmtMs, fmtSpeedup, measured, metricNum } from "./format";

// 没有 report 时的状态文案：会话状态机 → 人话。
// 分析是异步的，用户停在详情页时轮询会把这行字换掉，不用手动刷新。
function pendingCopy(status: string): { title: string; desc: string } {
  switch (status) {
    case "waiting":
      return {
        title: "等待任务进入终态",
        desc: "训练会话已建好；任务跑到终态后才会冻结编排图并开始分析。",
      };
    case "analyzing":
      return {
        title: "正在分析编排图…",
        desc: "正在读取当前编排图并计算关键路径/并行度/分派与信息流指标；分析完成后才会冻结为只读快照。重跑期间旧报告不展示。",
      };
    case "ready":
      return {
        title: "没有拿到报告",
        desc: "会话标记已分析，但 report 为空——可能是旧数据或后端落库不完整。",
      };
    case "skipped":
      return {
        title: "本次会话未做训练分析",
        desc: "运行模式为推理（infer），或采样未命中；只保留了基础指标，没有冻结编排图。",
      };
    case "failed":
      return {
        title: "分析失败",
        desc: "报告生成失败，上一次的报告不再展示；可点右上角「重跑分析」重试，错误信息见下方。",
      };
    default:
      return { title: "等待分析", desc: "当前没有可展示的报告。" };
  }
}

export function TrainingOverview({ detail }: { detail: RsiDetail }) {
  const { session, report, findings, workflow } = detail;

  // 只有 ready 才展示报告。重跑会把 status 改回 analyzing，但旧的 report
  // 仍会随响应返回——不能按 `!report` 判断，否则会把过期数字当成当前结果。
  if (session.status !== "ready" || !report) {
    const copy = pendingCopy(session.status);
    return (
      <SectionCard title="分析状态">
        <div className="space-y-2 px-4 py-5">
          <p className="text-sm font-medium text-ink-2">{copy.title}</p>
          <p className="text-xs leading-relaxed text-ink-3">{copy.desc}</p>
          {session.status === "failed" && session.error && (
            <p className="rounded-md border border-red-500/25 bg-red-500/10 px-3 py-2 font-mono text-[11px] leading-relaxed text-red-300">
              {session.error}
            </p>
          )}
        </div>
      </SectionCard>
    );
  }

  const metrics = report.metrics ?? {};
  const baseline = report.baseline ?? {};
  const sessionMetrics = Object.keys(metrics).length > 0 ? metrics : workflow?.metrics ?? {};

  const nodes = metricNum(sessionMetrics, "node_count");
  const cp = metricNum(sessionMetrics, "critical_path_ms", "critical_path");
  const speedup = metricNum(sessionMetrics, "parallel_speedup");
  // 报告与 session 列都可能缺值；0 表示“没测到”，逐级回退，最终显示「—」。
  const displayNodes = measured(nodes) ?? measured(session.node_count);
  const displayCp = measured(cp) ?? measured(session.critical_path_ms);
  const displaySpeedup = measured(speedup) ?? measured(session.parallel_speedup);
  const baselineCp = measured(metricNum(baseline, "critical_path_ms", "critical_path_ms_median"));
  const baselineSpeedup = measured(metricNum(baseline, "parallel_speedup", "parallel_speedup_median"));
  const hasBaseline = baselineCp !== null || baselineSpeedup !== null;

  // 置信度分布：finding.confidence 是 L1/L2/L3（§4.3），未知值忽略。
  const conf = { L1: 0, L2: 0, L3: 0 };
  for (const f of findings ?? []) {
    if (f.confidence === "L1" || f.confidence === "L2" || f.confidence === "L3") {
      conf[f.confidence] += 1;
    }
  }
  const confTotal = conf.L1 + conf.L2 + conf.L3;
  const confText =
    confTotal === 0
      ? "本次未产出发现。"
      : `本次发现 L3 ${conf.L3} / L2 ${conf.L2} / L1 ${conf.L1} 条。`;

  const findingCount = findings?.length ?? 0;

  return (
    <div className="space-y-4">
      <SectionCard
        title="分析结论"
        desc={report.status && report.status !== "ready" ? `报告状态：${report.status}` : undefined}
      >
        <div className="space-y-3 px-4 py-4">
          {report.summary ? (
            <p className="whitespace-pre-wrap text-sm leading-relaxed text-ink-2">
              {report.summary}
            </p>
          ) : (
            <p className="text-sm text-ink-3">
              报告没有摘要——本次分析只落了指标（通常是没有命中结构规则）。
            </p>
          )}

          {report.narrative && (
            <div className="rounded-md border border-line bg-surface-2/40 px-3 py-2.5">
              <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3">
                分析叙述
              </div>
              <p className="mt-1.5 whitespace-pre-wrap text-xs leading-relaxed text-ink-2">
                {report.narrative}
              </p>
            </div>
          )}

          {report.error && (
            <p className="rounded-md border border-red-500/25 bg-red-500/10 px-3 py-2 font-mono text-[11px] text-red-300">
              {report.error}
            </p>
          )}

          <div className="rounded-md border border-line bg-surface-2/30 px-3 py-2 text-[11px] leading-relaxed text-ink-3">
            <span className="text-ink-2">置信度规则：</span>
            L3 跨 run 基线 &gt; L2 单 run 统计 &gt; L1 单次观察。{confText}
            只基于单次编排的结论按 L1 读，别当成统计规律。
          </div>
        </div>
      </SectionCard>

      <StatStrip
        items={[
          {
            label: "编排节点",
            value: fmtInt(displayNodes),
            hint: "冻结 DAG 的节点数与边数",
          },
          {
            label: "关键路径",
            value: fmtMs(displayCp),
            hint: "最长依赖链的忙碌时长",
          },
          {
            label: "并行加速",
            value: fmtSpeedup(displaySpeedup),
            tone:
              displaySpeedup !== null && displaySpeedup >= 1.5
                ? "ok"
                : displaySpeedup !== null && displaySpeedup < 1.05
                  ? "warn"
                  : "default",
            hint: "总工作量 / 关键路径；1× = 完全串行",
          },
          {
            label: "结构发现",
            value: fmtInt(findingCount),
            tone: findingCount > 0 ? "warn" : "ok",
            hint: "只读提案，确认后再人工落地",
          },
        ]}
      />

      {!hasBaseline && (
        <div className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3.5 py-2.5 text-xs leading-relaxed text-amber-300">
          样本不足：历史就绪会话不足 5 个，本次未生成跨会话中位数基线；
          发现置信度最高 L2（单 run 统计），多跑几次训练模式后才能做跨 run 对比。
        </div>
      )}

      {hasBaseline && (
        <div className="rounded-md border border-line bg-surface-2/30 px-3.5 py-2.5 text-xs leading-relaxed text-ink-3">
          <span className="text-ink-2">与历史就绪会话的中位数对比：</span>
          关键路径 {fmtMs(measured(baselineCp))} → {fmtMs(displayCp)} · 并行加速{" "}
          {fmtSpeedup(measured(baselineSpeedup))} → {fmtSpeedup(displaySpeedup)}
        </div>
      )}

      <SectionCard title="编排指标" desc="同一组数字在「编排」tab 的图下方也会出现">
        <div className="p-4">
          <WorkflowMetricsRow metrics={sessionMetrics} />
        </div>
      </SectionCard>
    </div>
  );
}
