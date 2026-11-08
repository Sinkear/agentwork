"use client";

import {
  ArrowDownRight,
  ArrowUpRight,
  ChevronDown,
  History,
  Minus,
} from "lucide-react";
import { Empty, SectionCard, Skeleton, StatStrip } from "@/components/ui";
import { useRsiRounds } from "@/lib/queries";
import type { RsiDetail, RsiDeltaEntry, RsiRound } from "@/lib/types";
import { cn } from "@/lib/utils";
import {
  DASH,
  DELTA_LABELS,
  DELTA_METRIC_KEYS,
  ROUND_TRIGGER_LABEL,
  asNum,
  categoryLabel,
  confidenceMeta,
  deltaTone,
  fmtDeltaPct,
  fmtDeltaValue,
  fmtInt,
  fmtMs,
  fmtRatio,
  fmtSpeedup,
  fmtTime,
  metricNum,
  severityMeta,
} from "./format";

type Tone = ReturnType<typeof deltaTone>;

const TONE_TEXT: Record<Tone, string> = {
  improved: "text-emerald-300",
  worsened: "text-red-300",
  same: "text-ink-3",
};

const TONE_LABEL: Record<Tone, string> = {
  improved: "改善",
  worsened: "恶化",
  same: "持平",
};

function shortDate(iso: string): { date: string; time: string } {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) {
    return { date: iso.slice(5, 10) || DASH, time: iso.slice(11, 16) || "" };
  }
  const mm = String(d.getMonth() + 1).padStart(2, "0");
  const dd = String(d.getDate()).padStart(2, "0");
  const hh = String(d.getHours()).padStart(2, "0");
  const mi = String(d.getMinutes()).padStart(2, "0");
  return { date: `${mm}-${dd}`, time: `${hh}:${mi}` };
}

function TriggerChip({ trigger }: { trigger: string }) {
  const manual = trigger === "manual_retry";
  return (
    <span
      className={cn(
        "inline-flex items-center rounded border px-1.5 py-0.5 text-[10px] font-medium",
        manual
          ? "border-amber-500/30 bg-amber-500/10 text-amber-300"
          : "border-line bg-surface-2 text-ink-2"
      )}
    >
      {ROUND_TRIGGER_LABEL[trigger] ?? trigger}
    </span>
  );
}

function DeltaRow({ name, entry }: { name: string; entry: RsiDeltaEntry }) {
  const from = asNum(entry.from);
  const to = asNum(entry.to);
  if (from === null || to === null) return null;

  const tone = deltaTone(name, from, to);
  const up = to > from;
  const same = tone === "same";
  const Icon = same ? Minus : up ? ArrowUpRight : ArrowDownRight;
  const pct = fmtDeltaPct(asNum(entry.pct));

  return (
    <div className="flex items-center gap-2 py-0.5 text-[11px]">
      <span className="w-20 shrink-0 text-ink-3">{DELTA_LABELS[name] ?? name}</span>
      <span className="font-mono tabular-nums text-ink-2">
        {fmtDeltaValue(name, from)}
        <span className="mx-1 text-ink-3">→</span>
        {fmtDeltaValue(name, to)}
      </span>
      <span className={cn("ml-auto inline-flex items-center gap-1 font-mono tabular-nums", TONE_TEXT[tone])}>
        <Icon className="h-3 w-3" aria-hidden="true" />
        {pct || TONE_LABEL[tone]}
        <span className="sr-only">{TONE_LABEL[tone]}</span>
      </span>
    </div>
  );
}

function RoundDelta({ round }: { round: RsiRound }) {
  if (round.round_no <= 1) {
    return (
      <p className="rounded-md border border-dashed border-line bg-surface-2/20 px-3 py-2 text-[11px] text-ink-3">
        首轮基线：没有上一轮可对比，后续重跑会在这里展示变化。
      </p>
    );
  }

  const known = DELTA_METRIC_KEYS.filter((k) => round.delta?.[k]);
  const extra = Object.keys(round.delta ?? {}).filter(
    (k) => !(DELTA_METRIC_KEYS as readonly string[]).includes(k)
  );
  if (known.length === 0 && extra.length === 0) {
    return (
      <p className="rounded-md border border-dashed border-line bg-surface-2/20 px-3 py-2 text-[11px] text-ink-3">
        与上一轮没有同口径指标，无法计算变化。
      </p>
    );
  }

  return (
    <div className="rounded-md border border-line bg-surface-2/20 px-3 py-2">
      <div className="mb-1 font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3">
        较上一轮
      </div>
      <div className="divide-y divide-line/60">
        {[...known, ...extra].map((name) => (
          <DeltaRow key={name} name={name} entry={round.delta[name]} />
        ))}
      </div>
    </div>
  );
}

// 冻结 findings 只读展示：确认/忽略只作用于最新轮（发现 tab），历史轮不提供操作。
function FrozenFinding({ finding }: { finding: RsiDetail["findings"][number] }) {
  const sev = severityMeta(finding.severity);
  const conf = confidenceMeta(finding.confidence);
  const evidence = Array.isArray(finding.evidence)
    ? finding.evidence.filter((x): x is string => typeof x === "string" && x.length > 0)
    : [];

  return (
    <div className="rounded-md border border-line bg-surface-1 px-3 py-2">
      <div className="flex flex-wrap items-center gap-1.5">
        <span className={cn("rounded border px-1.5 py-0.5 text-[10px] font-medium", sev.cls)}>
          {sev.label}
        </span>
        <span className="text-[10px] text-ink-3">{categoryLabel(finding.category)}</span>
        <span
          className="rounded border border-line bg-surface-2 px-1 py-0.5 font-mono text-[10px] text-ink-2"
          title={conf.desc}
        >
          {conf.label}
        </span>
        <span className="ml-auto font-mono text-[10px] tabular-nums text-ink-3">
          score {typeof finding.score === "number" && Number.isFinite(finding.score) ? finding.score.toFixed(2) : DASH}
        </span>
      </div>
      <p className="mt-1 text-xs font-medium text-ink">{finding.title || "未命名发现"}</p>
      {finding.explanation && (
        <p className="mt-0.5 whitespace-pre-wrap text-[11px] leading-relaxed text-ink-2">
          {finding.explanation}
        </p>
      )}
      {finding.suggestion && (
        <p className="mt-1 whitespace-pre-wrap text-[11px] leading-relaxed text-ink-3">
          建议：{finding.suggestion}
        </p>
      )}
      {evidence.length > 0 && (
        <p className="mt-1 truncate font-mono text-[10px] text-ink-3" title={evidence.join("\n")}>
          证据：{evidence.slice(0, 3).join(" · ")}
          {evidence.length > 3 ? ` · +${evidence.length - 3}` : ""}
        </p>
      )}
    </div>
  );
}

function RoundCard({ round }: { round: RsiRound }) {
  const findings = round.findings ?? [];
  const top = round.top_title || (round.top_category ? categoryLabel(round.top_category) : "");

  return (
    <li className="relative">
      <span
        className="absolute -left-[21px] top-3 h-2.5 w-2.5 rounded-full border border-line bg-surface-0"
        aria-hidden="true"
      />
      <article className="overflow-hidden rounded-lg border border-line bg-surface-1">
        <header className="flex flex-wrap items-center gap-x-2 gap-y-1 px-4 py-2.5">
          <span className="inline-flex items-center rounded border border-accent/30 bg-accent/[0.08] px-1.5 py-0.5 font-mono text-[11px] font-medium text-accent">
            R{fmtInt(round.round_no)}
          </span>
          <TriggerChip trigger={round.trigger} />
          <span className="text-[11px] text-ink-3">{fmtTime(round.created_at)}</span>
          <span className="ml-auto flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px]">
            <span className="text-ink-3">发现 {fmtInt(round.finding_count)} 条</span>
            {round.critical_count > 0 && (
              <span className="rounded border border-red-500/30 bg-red-500/10 px-1.5 py-0.5 text-[10px] text-red-300">
                高危 {fmtInt(round.critical_count)}
              </span>
            )}
          </span>
        </header>

        {(top || round.summary) && (
          <div className="space-y-1 border-t border-line px-4 py-2.5">
            {top && (
              <p className="text-[11px] text-ink-2">
                主瓶颈 <span className="text-ink">{top}</span>
                {round.top_category && (
                  <span className="ml-1.5 text-[10px] text-ink-3">
                    {categoryLabel(round.top_category)}
                  </span>
                )}
              </p>
            )}
            {round.summary && (
              <p className="whitespace-pre-wrap text-[11px] leading-relaxed text-ink-3">
                {round.summary}
              </p>
            )}
          </div>
        )}

        <div className="border-t border-line px-4 py-2.5">
          <RoundDelta round={round} />
        </div>

        <details className="group border-t border-line">
          <summary className="flex cursor-pointer select-none items-center gap-1.5 px-4 py-2.5 text-[11px] text-ink-2 transition-colors hover:bg-surface-2/40">
            <History className="h-3.5 w-3.5 text-ink-3" />
            当轮冻结发现 {fmtInt(findings.length)} 条（只读）
            <ChevronDown className="ml-auto h-3.5 w-3.5 text-ink-3 transition-transform group-open:rotate-180" />
          </summary>
          <div className="max-h-80 space-y-2 overflow-y-auto border-t border-line bg-surface-2/20 px-4 py-2.5">
            {findings.length === 0 ? (
              <p className="text-[11px] text-ink-3">当轮没有产出发现。</p>
            ) : (
              findings.map((f) => <FrozenFinding key={f.id} finding={f} />)
            )}
          </div>
        </details>
      </article>
    </li>
  );
}

export function TrainingInsights({ detail }: { detail: RsiDetail }) {
  const { session, report } = detail;
  const inFlight = session.status === "waiting" || session.status === "analyzing";
  const { data: rounds, isLoading, isError, error } = useRsiRounds(session.id, inFlight);

  if (isLoading) {
    return (
      <SectionCard title="洞察">
        <div className="space-y-2 p-4">
          <Skeleton className="h-16 w-full" />
          <Skeleton className="h-24 w-full" />
          <Skeleton className="h-24 w-full" />
        </div>
      </SectionCard>
    );
  }

  if (isError) {
    return (
      <SectionCard title="洞察">
        <Empty icon={<History className="h-4 w-4" />}>
          洞察数据暂时读不到。
          <span className="mt-1 block font-mono text-[11px] text-ink-3">
            {error instanceof Error ? error.message : String(error)}
          </span>
        </Empty>
      </SectionCard>
    );
  }

  const list = (rounds ?? []).filter((r) => r && Number.isFinite(r.round_no));
  if (list.length === 0) {
    return (
      <SectionCard title="洞察">
        <Empty icon={<History className="h-4 w-4" />}>
          {inFlight
            ? "首次分析完成后会在这里记录第 1 轮（终态分析）；页面会自动刷新。"
            : session.status === "skipped"
              ? "本次会话未做分析（推理模式或采样未命中），因此没有优化轮次。"
              : report
                ? "这个会话没有轮次记录——可能是轮次账本启用前的历史数据。"
                : "还没有优化轮次。任务进入终态并完成分析后，这里会记录每轮指标与变化。"}
        </Empty>
      </SectionCard>
    );
  }

  const latest = list[list.length - 1];
  const latestMetrics = latest.metrics ?? {};
  const speedup = metricNum(latestMetrics, "parallel_speedup") ?? session.parallel_speedup;
  const criticalPath = metricNum(latestMetrics, "critical_path_ms") ?? session.critical_path_ms;
  const rework = metricNum(latestMetrics, "rework_ratio");

  // 最新在上：轮次只会追加，倒序让“刚复测完”的结果第一眼可见。
  const desc = [...list].sort((a, b) => b.round_no - a.round_no);
  const when = shortDate(latest.created_at);

  return (
    <div className="space-y-4">
      <SectionCard
        title="优化轮次"
        desc="每次分析追加一轮：终态自动分析为第 1 轮，之后每次「重跑分析」是手动复测轮"
        bodyClassName="p-0"
      >
        <div className="p-4">
          <StatStrip
            items={[
              {
                label: "总轮数",
                value: fmtInt(list.length),
                hint: "同一会话的历史分析次数",
                tone: "info",
              },
              {
                label: "最新轮次",
                value: `R${fmtInt(latest.round_no)}`,
                hint: "轮次按分析次数递增",
              },
              {
                label: "触发方式",
                value: ROUND_TRIGGER_LABEL[latest.trigger] ?? latest.trigger,
                hint: latest.trigger === "manual_retry" ? "重跑分析产生" : "goal 终态自动触发",
              },
              {
                label: "最近分析",
                value: when.date,
                hint: `${when.time} 冻结`,
              },
            ]}
          />
          <div className="mt-3 grid grid-cols-3 gap-2">
            {[
              { label: "并行加速", value: fmtSpeedup(speedup) },
              { label: "关键路径", value: fmtMs(criticalPath) },
              { label: "返工率", value: fmtRatio(rework) },
            ].map((m) => (
              <div key={m.label} className="rounded-md border border-line bg-surface-2/25 px-2.5 py-2">
                <div className="font-mono text-[10px] uppercase tracking-[0.1em] text-ink-3">
                  {m.label}
                </div>
                <div className="mt-1 font-mono text-sm tabular-nums text-ink-2">{m.value}</div>
              </div>
            ))}
          </div>
        </div>
      </SectionCard>

      <SectionCard
        title="轮次时间线"
        count={desc.length}
        desc="新轮在上；delta 为与上一轮的同口径指标变化"
        bodyClassName="p-0"
      >
        <div className="px-4 py-3">
          <ol className="relative space-y-3 border-l border-line pl-5">
            {desc.map((r) => (
              <RoundCard key={r.id} round={r} />
            ))}
          </ol>
        </div>
      </SectionCard>
    </div>
  );
}