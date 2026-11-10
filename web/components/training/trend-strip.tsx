"use client";

import Link from "next/link";
import { Activity } from "lucide-react";
import { SectionCard, Skeleton } from "@/components/ui";
import type { RsiTrendPoint } from "@/lib/types";
import { cn } from "@/lib/utils";
import { DASH, fmtSpeedup, fmtTime } from "./format";

const W = 760;
const H = 170;
const PAD_L = 48;
const PAD_R = 20;
const PAD_T = 16;
const PAD_B = 28;
const PLOT_W = W - PAD_L - PAD_R;
const PLOT_H = H - PAD_T - PAD_B;
const ACCENT = "var(--color-accent)";
const LINE = "var(--color-line)";
const INK_3 = "var(--color-ink-3)";
const UP = "#34d399";
const DOWN = "#f87171";

function shortDate(iso: string): string {
  if (!iso) return DASH;
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return iso.slice(0, 10);
  return d.toLocaleDateString("zh-CN", { month: "2-digit", day: "2-digit" });
}

function shortTitle(s: string): string {
  const t = (s || "未命名任务").trim();
  return t.length > 18 ? `${t.slice(0, 18)}…` : t;
}

export function TrainingTrend({
  trend,
  loading = false,
}: {
  trend?: RsiTrendPoint[];
  loading?: boolean;
}) {
  const points = (trend ?? [])
    .filter(
      (p) =>
        p &&
        Number.isFinite(p.parallel_speedup) &&
        p.parallel_speedup >= 0 &&
        typeof p.session_id === "string"
    )
    .slice()
    .sort((a, b) => {
      const ta = Date.parse(a.created_at);
      const tb = Date.parse(b.created_at);
      return (Number.isNaN(ta) ? 0 : ta) - (Number.isNaN(tb) ? 0 : tb);
    });

  if (loading && points.length === 0) {
    return (
      <SectionCard title="趋势" bodyClassName="p-0">
        <div className="p-4">
          <Skeleton className="h-36 w-full" />
        </div>
      </SectionCard>
    );
  }
  // “没有轮次”不是错误状态：整块隐藏，列表页保持干净。
  if (points.length === 0) return null;

  const values = points.map((p) => p.parallel_speedup);
  const rawMin = Math.min(...values);
  const rawMax = Math.max(...values);
  const span = rawMax - rawMin || Math.max(rawMax * 0.2, 0.2);
  const lo = Math.max(0, rawMin - span * 0.18);
  const hi = rawMax + span * 0.18;
  const toY = (v: number) => PAD_T + (1 - (v - lo) / (hi - lo)) * PLOT_H;
  const toX = (i: number) =>
    points.length === 1 ? PAD_L + PLOT_W / 2 : PAD_L + (i / (points.length - 1)) * PLOT_W;

  const coords = points.map((p, i) => ({ p, x: toX(i), y: toY(p.parallel_speedup) }));
  const line = coords
    .map((c, i) => `${i === 0 ? "M" : "L"} ${c.x.toFixed(1)} ${c.y.toFixed(1)}`)
    .join(" ");
  const area = `${line} L ${coords[coords.length - 1].x.toFixed(1)} ${PAD_T + PLOT_H} L ${coords[0].x.toFixed(1)} ${PAD_T + PLOT_H} Z`;

  const yTicks = [hi, (hi + lo) / 2, lo];
  const labelEvery = Math.max(1, Math.ceil(points.length / 5));
  const hasBaseline = lo <= 1 && hi >= 1;
  const newestFirst = [...coords].reverse().slice(0, 12);

  return (
    <SectionCard
      title="趋势"
      count={points.length}
      desc="每个点是一轮分析；纵轴为并行加速，点越靠上越好。点可跳转到对应会话的洞察面板。"
      bodyClassName="p-0"
    >
      <div className="px-4 pt-2">
        <div className="relative">
          <svg
            viewBox={`0 0 ${W} ${H}`}
            className="h-auto w-full"
            role="img"
            aria-label={`并行加速趋势，共 ${points.length} 个轮次`}
          >
            <defs>
              <linearGradient id="rsi-trend-fill" x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={ACCENT} stopOpacity="0.22" />
                <stop offset="100%" stopColor={ACCENT} stopOpacity="0" />
              </linearGradient>
            </defs>

            {yTicks.map((t, i) => (
              <g key={i}>
                <line
                  x1={PAD_L}
                  x2={W - PAD_R}
                  y1={toY(t)}
                  y2={toY(t)}
                  stroke={LINE}
                  strokeWidth="1"
                  strokeDasharray={i === 2 ? "3 4" : undefined}
                />
                <text
                  x={PAD_L - 8}
                  y={toY(t) + 3}
                  textAnchor="end"
                  fontSize="9"
                  fill={INK_3}
                  fontFamily="var(--font-mono, ui-monospace, monospace)"
                >
                  {fmtSpeedup(t)}
                </text>
              </g>
            ))}

            {hasBaseline && (
              <g>
                <line
                  x1={PAD_L}
                  x2={W - PAD_R}
                  y1={toY(1)}
                  y2={toY(1)}
                  stroke="var(--color-line-strong)"
                  strokeWidth="1"
                  strokeDasharray="5 4"
                />
                <text x={W - PAD_R} y={toY(1) - 4} textAnchor="end" fontSize="8" fill={INK_3}>
                  1× 串行基线
                </text>
              </g>
            )}

            <path d={area} fill="url(#rsi-trend-fill)" />
            <path d={line} fill="none" stroke={ACCENT} strokeWidth="1.75" strokeLinejoin="round" />

            {coords.map((c, i) => {
              const prev = i > 0 ? coords[i - 1].p.parallel_speedup : null;
              const color =
                prev === null
                  ? ACCENT
                  : c.p.parallel_speedup >= prev
                    ? UP
                    : DOWN;
              const label =
                i % labelEvery === 0 || i === coords.length - 1
                  ? shortDate(c.p.created_at)
                  : "";
              return (
                <g key={`${c.p.round_id}-${i}`}>
                  <a
                    href={`/training/${c.p.session_id}`}
                    aria-label={`${c.p.goal_title || "任务"} 第 ${c.p.round_no} 轮，并行加速 ${fmtSpeedup(c.p.parallel_speedup)}`}
                  >
                    <title>
                      {`${c.p.goal_title || "未命名任务"} · R${c.p.round_no} · ${fmtSpeedup(c.p.parallel_speedup)} · ${fmtTime(c.p.created_at)}`}
                    </title>
                    <circle cx={c.x} cy={c.y} r="10" fill="transparent" />
                    <circle
                      cx={c.x}
                      cy={c.y}
                      r="3.6"
                      fill={color}
                      stroke="var(--color-surface-0)"
                      strokeWidth="1.5"
                    />
                  </a>
                  {label && (
                    <text
                      x={c.x}
                      y={H - 8}
                      textAnchor={i === 0 ? "start" : i === coords.length - 1 ? "end" : "middle"}
                      fontSize="9"
                      fill={INK_3}
                    >
                      {label}
                    </text>
                  )}
                </g>
              );
            })}
          </svg>
        </div>

        <div className="mt-1 flex flex-wrap items-center gap-x-4 gap-y-1 pb-2 text-[10px] text-ink-3">
          <span className="inline-flex items-center gap-1">
            <Activity className="h-3 w-3 text-accent" />
            并行加速（工作量 / 关键路径）
          </span>
          <span className="inline-flex items-center gap-1">
            <span className="h-2 w-2 rounded-full" style={{ background: UP }} />
            较上一点上升
          </span>
          <span className="inline-flex items-center gap-1">
            <span className="h-2 w-2 rounded-full" style={{ background: DOWN }} />
            较上一点下降
          </span>
        </div>
      </div>

      <div className="flex flex-wrap gap-1.5 border-t border-line px-4 py-2.5">
        {newestFirst.map(({ p }) => (
          <Link
            key={p.round_id}
            href={`/training/${p.session_id}`}
            className={cn(
              "inline-flex max-w-full items-center gap-1.5 rounded border border-line bg-surface-2/30 px-2 py-1",
              "text-[10px] text-ink-3 transition-colors hover:border-accent/40 hover:text-ink-2"
            )}
            title={`${p.goal_title || "未命名任务"} · 第 ${p.round_no} 轮 · ${fmtTime(p.created_at)}`}
          >
            <span className="truncate">{shortTitle(p.goal_title)}</span>
            <span className="shrink-0 font-mono text-ink-3">R{p.round_no}</span>
            <span className="shrink-0 font-mono tabular-nums text-ink-2">
              {fmtSpeedup(p.parallel_speedup)}
            </span>
          </Link>
        ))}
        {coords.length > 12 && (
          <span className="inline-flex items-center px-1 text-[10px] text-ink-3">
            另有 {coords.length - 12} 轮
          </span>
        )}
      </div>
    </SectionCard>
  );
}