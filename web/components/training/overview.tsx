"use client";

import { SectionCard, StatStrip } from "@/components/ui";
import type { RsiDetail } from "@/lib/types";
import {
  fmtInt,
  fmtMs,
  fmtRatio,
  fmtUsd,
  metricNum,
  totalTokens,
} from "./format";

// 没有 report 时的状态文案：会话状态机 → 人话。
// 分析是异步的，用户停在详情页时轮询会把这行字换掉，不用手动刷新。
function pendingCopy(status: string): { title: string; desc: string } {
  switch (status) {
    case "waiting":
      return {
        title: "等待这次执行开始",
        desc: "会话已建好；goal 的 run 启动后开始采集 span，跑完才会进入分析。",
      };
    case "collecting":
      return {
        title: "正在采集执行 trace…",
        desc: "会话运行期间持续记录 span；跑完后才会生成报告，可以先去做别的。",
      };
    case "analyzing":
      return {
        title: "正在分析…",
        desc: "trace 已封存，正在算指标并跑规则；通常几秒内出报告。",
      };
    case "ready":
      return {
        title: "没有拿到报告",
        desc: "会话标记已分析，但 report 为空——可能是旧数据或后端落库不完整。",
      };
    case "skipped":
      return {
        title: "本次会话未做训练分析",
        desc: "运行模式为推理（infer），或采样未命中；只保留了基础指标。",
      };
    case "failed":
      return {
        title: "分析失败",
        desc: "报告生成失败，可点右上角「重跑分析」重试；错误信息见下方。",
      };
    default:
      return { title: "等待分析", desc: "当前没有可展示的报告。" };
  }
}

export function TrainingOverview({ detail }: { detail: RsiDetail }) {
  const { session, report, findings } = detail;
  const hasFindings = Array.isArray(findings);

  if (!report) {
    const copy = pendingCopy(session.status);
    return (
      <SectionCard title="分析状态">
        <div className="px-4 py-5 space-y-2">
          <p className="text-sm font-medium text-ink-2">{copy.title}</p>
          <p className="text-xs leading-relaxed text-ink-3">{copy.desc}</p>
          {session.error && (
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

  const wall = metricNum(metrics, "wall_ms", "duration_ms");
  const llmShare = metricNum(metrics, "llm_share");
  const toolShare = metricNum(metrics, "tool_share");
  const humanWait = metricNum(metrics, "human_wait_ms");
  const tokens = totalTokens(metrics);
  const cost = metricNum(metrics, "cost_usd", "cost");
  const ctxPeak = metricNum(metrics, "context_peak_ratio");

  // 样本量：后端 baseline.sample_size = 参与对比的历史会话数；<5 时后端
  // 故意不吐中位数（会把噪声当证据）。读不到再退回本次 run 计数。
  const sampleSize = metricNum(baseline, "sample_size");
  const runCount = metricNum(metrics, "run_count", "runs", "n_runs");
  const lowSample =
    sampleSize !== null ? sampleSize < 5 : runCount !== null && runCount < 5;
  const sampleHint =
    sampleSize !== null
      ? `历史基线只有 ${fmtInt(sampleSize)} 个会话（<5），本次未计算与历史中位数的对比`
      : `本次只有 ${fmtInt(runCount)} 个 run（<5）`;

  // 置信度分布：finding.confidence 是 L1/L2/L3（§4.3），未知值忽略。
  const conf = { L1: 0, L2: 0, L3: 0 };
  for (const f of findings ?? []) {
    if (f.confidence === "L1" || f.confidence === "L2" || f.confidence === "L3") {
      conf[f.confidence] += 1;
    }
  }
  const confTotal = conf.L1 + conf.L2 + conf.L3;
  const confText =
    !hasFindings || confTotal === 0
      ? "本次未产出发现。"
      : `本次发现 L3 ${conf.L3} / L2 ${conf.L2} / L1 ${conf.L1} 条。`;

  // 后端 baseline 的键是 <metric>_median；sample_size>=5 才会出现。
  const baselineWall = metricNum(baseline, "wall_ms_median");
  const baselineToolShare = metricNum(baseline, "tool_share_median");
  const showBaseline =
    sampleSize !== null && sampleSize >= 5 && (baselineWall !== null || baselineToolShare !== null);

  return (
    <div className="space-y-4">
      <SectionCard
        title="分析结论"
        desc={report.status && report.status !== "ready" ? `报告状态：${report.status}` : undefined}
      >
        <div className="px-4 py-4 space-y-3">
          {report.summary ? (
            <p className="whitespace-pre-wrap text-sm leading-relaxed text-ink-2">
              {report.summary}
            </p>
          ) : (
            <p className="text-sm text-ink-3">
              报告没有摘要——本次分析只落了指标（通常是没有命中瓶颈规则）。
            </p>
          )}

          {report.narrative && (
            <div className="rounded-md border border-line bg-surface-2/40 px-3 py-2.5">
              <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3">
                归因叙述
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
            只基于单次执行的结论按 L1 读，别当成统计规律。
          </div>
        </div>
      </SectionCard>

      {lowSample && (
        <div className="rounded-md border border-amber-500/30 bg-amber-500/10 px-3.5 py-2.5 text-xs leading-relaxed text-amber-300">
          样本不足：{sampleHint}。发现置信度最高 L2（单 run 统计），
          多跑几次训练模式、攒够 5 个会话后才能做跨 run 基线对比。
        </div>
      )}

      <StatStrip
        items={[
          { label: "总耗时 WALL", value: fmtMs(wall), hint: `${fmtInt(session.trace_count)} 个 span` },
          { label: "LLM 占比", value: fmtRatio(llmShare), hint: "LLM 耗时 / 总耗时" },
          { label: "工具占比", value: fmtRatio(toolShare), hint: "工具耗时 / 总耗时" },
          { label: "人工等待", value: fmtMs(humanWait), hint: "等人确认 / 输入" },
          { label: "TOKENS", value: fmtInt(tokens), hint: "输入 + 输出" },
          { label: "成本", value: fmtUsd(cost), hint: "本次分析的 token 成本" },
          {
            label: "上下文峰值",
            value: fmtRatio(ctxPeak),
            hint: metrics.usage_available === false ? "usage 不可用" : "占模型窗口比例",
          },
        ]}
      />

      {showBaseline && (
        <p className="text-[11px] leading-relaxed text-ink-3">
          对比基线（{fmtInt(sampleSize)} 个历史会话的中位数）：wall {fmtMs(baselineWall)} · 工具占比{" "}
          {fmtRatio(baselineToolShare)}
        </p>
      )}
    </div>
  );
}