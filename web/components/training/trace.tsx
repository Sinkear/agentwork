"use client";

import { useMemo, useState } from "react";
import { Empty, SectionCard } from "@/components/ui";
import type { RsiDetail, RsiSpan } from "@/lib/types";
import { cn } from "@/lib/utils";
import { DASH, asNum, fmtBytes, fmtInt, fmtMs, fmtUsd, SPAN_STATUS_LABEL } from "./format";

// kind 的颜色：只用色相区分“这段是谁在跑”，不引入图表库。
// 后端 kind 是枚举（run/llm/tool/verify/gate/human_wait/subgoal），未知值走兜底灰。
const KIND_META: Record<string, { label: string; bar: string; text: string }> = {
  run: { label: "Run", bar: "bg-emerald-400/70", text: "text-emerald-300" },
  llm: { label: "LLM", bar: "bg-blue-400/70", text: "text-blue-300" },
  tool: { label: "工具", bar: "bg-amber-400/70", text: "text-amber-300" },
  verify: { label: "验证", bar: "bg-purple-400/70", text: "text-purple-300" },
  gate: { label: "Gate", bar: "bg-sky-400/70", text: "text-sky-300" },
  human_wait: { label: "人工等待", bar: "bg-pink-400/70", text: "text-pink-300" },
  subgoal: { label: "子任务", bar: "bg-teal-400/70", text: "text-teal-300" },
};

function kindMeta(kind: string) {
  return KIND_META[kind] ?? { label: kind || "span", bar: "bg-zinc-400/60", text: "text-ink-3" };
}

// trace_span 的时间戳是 epoch 毫秒（started_at_ms/ended_at_ms），
// 直接做整数运算；缺失/脏值时退回 duration_ms，保证瀑布条不是零宽。
function spanStart(s: RsiSpan): number {
  return asNum(s.started_at_ms) ?? 0;
}

function spanEnd(s: RsiSpan): number {
  const start = spanStart(s);
  const end = asNum(s.ended_at_ms);
  if (end !== null && end >= start) return end;
  return start + Math.max(0, asNum(s.duration_ms) ?? 0);
}

function spanDur(s: RsiSpan): number | null {
  const d = asNum(s.duration_ms);
  if (d !== null) return d;
  const computed = spanEnd(s) - spanStart(s);
  return computed > 0 ? computed : null;
}

interface RunGroup {
  id: string;
  spans: RsiSpan[];
  start: number;
  end: number;
}

function KV({ label, value, mono = true }: { label: string; value: React.ReactNode; mono?: boolean }) {
  return (
    <div className="flex min-w-0 items-baseline justify-between gap-2 border-b border-line/60 py-1 last:border-b-0">
      <span className="shrink-0 text-[11px] text-ink-3">{label}</span>
      <span className={cn("truncate text-[11px] text-ink-2", mono && "font-mono")}>{value}</span>
    </div>
  );
}

export function TrainingTrace({ detail }: { detail: RsiDetail }) {
  const spans = detail.spans ?? [];

  const runs = useMemo<RunGroup[]>(() => {
    const groups = new Map<string, RsiSpan[]>();
    for (const s of spans) {
      const key = s.run_id || "";
      const list = groups.get(key);
      if (list) list.push(s);
      else groups.set(key, [s]);
    }
    const out: RunGroup[] = [];
    for (const [id, list] of groups) {
      const sorted = [...list].sort((a, b) => spanStart(a) - spanStart(b));
      const start = Math.min(...sorted.map(spanStart));
      const end = Math.max(...sorted.map(spanEnd));
      out.push({ id, spans: sorted, start, end: Math.max(end, start + 1) });
    }
    // 最新 run 在前：详情页默认看“最近一次执行”。
    return out.sort((a, b) => b.start - a.start);
  }, [spans]);

  const [pickedRun, setPickedRun] = useState("");
  const [selectedKey, setSelectedKey] = useState("");
  const activeRun = runs.find((r) => r.id === pickedRun) ?? runs[0];
  // span.id 理论上唯一；后端早期数据可能没落 id，用下标兜底，保证高亮/详情不错位。
  const keyOf = (s: RsiSpan, idx: number) => s.id || `${activeRun?.id ?? ""}-${idx}`;
  const selected = activeRun?.spans.find((s, i) => keyOf(s, i) === selectedKey) ?? null;

  if (!activeRun) {
    return (
      <SectionCard title="Trace">
        <Empty icon={<EmptyTraceIcon />}>
          还没有采集到 span。若任务仍在运行，等这次执行结束后回来；若运行模式是推理（infer），
          trace 不会记录。
        </Empty>
      </SectionCard>
    );
  }

  const wall = Math.max(1, activeRun.end - activeRun.start);
  const runLabel = (id: string) => (id ? id.slice(0, 8) : "未命名 run");

  return (
    <SectionCard
      title="Trace"
      count={spans.length}
      desc={activeRun.spans.length > 0 ? `${activeRun.spans.length} 个 span · 横轴为本次 run 的 wall time` : undefined}
      bodyClassName="p-0"
    >
      {runs.length > 1 && (
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-4 py-2">
          <span className="text-[11px] text-ink-3">Run</span>
          <select
            value={activeRun.id}
            onChange={(e) => {
              setPickedRun(e.target.value);
              setSelectedKey("");
            }}
            className="rounded-md border border-line bg-surface-2 px-2 py-1 font-mono text-xs text-ink outline-none focus:border-accent-dim"
          >
            {runs.map((r) => (
              <option key={r.id || "unknown"} value={r.id}>
                {runLabel(r.id)} · {r.spans.length} spans · {fmtMs(r.end - r.start)}
              </option>
            ))}
          </select>
          <span className="text-[11px] text-ink-3">只展示当前 run 的 span</span>
        </div>
      )}

      <div className="grid grid-cols-[minmax(0,1fr)_minmax(0,2fr)] gap-3 border-b border-line bg-surface-2/30 px-4 py-1.5 font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3">
        <div>Span</div>
        <div className="flex items-center justify-between">
          <span>0</span>
          <span>{fmtMs(activeRun.end - activeRun.start)}</span>
        </div>
      </div>

      <div className="max-h-[480px] overflow-y-auto">
        {activeRun.spans.map((s, idx) => {
          const meta = kindMeta(s.kind);
          const leftRaw = ((spanStart(s) - activeRun.start) / wall) * 100;
          const left = Math.min(100, Math.max(0, leftRaw));
          const widthRaw = ((spanEnd(s) - spanStart(s)) / wall) * 100;
          const width = Math.min(100 - left, Math.max(widthRaw, 0.5));
          const rowKey = keyOf(s, idx);
          const isSelected = rowKey === selectedKey;
          return (
            <button
              key={rowKey}
              type="button"
              onClick={() => setSelectedKey((prev) => (prev === rowKey ? "" : rowKey))}
              className={cn(
                "grid w-full grid-cols-[minmax(0,1fr)_minmax(0,2fr)] items-center gap-3 px-4 py-1.5 text-left transition-colors",
                isSelected ? "bg-accent/10" : "hover:bg-surface-2/50"
              )}
            >
              <div className="min-w-0">
                <div className="flex min-w-0 items-center gap-1.5">
                  <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", meta.bar)} />
                  <span className="truncate text-xs text-ink-2">
                    {s.name || s.tool_name || meta.label}
                  </span>
                  {s.status === "error" && (
                    <span className="shrink-0 rounded bg-red-500/15 px-1 text-[10px] text-red-300">
                      {SPAN_STATUS_LABEL.error}
                    </span>
                  )}
                </div>
                <div className="mt-0.5 flex items-center gap-2 pl-3 font-mono text-[10px] text-ink-3">
                  <span>{meta.label}</span>
                  <span className="tabular-nums">{fmtMs(spanDur(s))}</span>
                  {s.status === "error" && s.error_class && (
                    <span className="truncate text-red-300/80">{s.error_class}</span>
                  )}
                </div>
              </div>
              <div className="relative h-5 overflow-hidden rounded bg-surface-2/70">
                <div
                  className={cn("absolute top-1 bottom-1 rounded-sm", meta.bar)}
                  style={{ left: `${left}%`, width: `${width}%` }}
                  title={`${s.name || meta.label} · ${fmtMs(spanDur(s))}`}
                />
              </div>
            </button>
          );
        })}
      </div>

      {selected && (
        <div className="space-y-3 border-t border-line bg-surface-2/20 px-4 py-3">
          <div className="flex flex-wrap items-center gap-2">
            <span
              className={cn(
                "rounded border border-line bg-surface-2 px-1.5 py-0.5 text-[10px] font-medium",
                kindMeta(selected.kind).text
              )}
            >
              {kindMeta(selected.kind).label}
            </span>
            <span className="text-sm text-ink">{selected.name || DASH}</span>
            <span className="font-mono text-[10px] text-ink-3">
              {selected.span_id || selected.id}
            </span>
            {selected.status !== "ok" && (
              <span className="text-[11px] text-red-300">
                {SPAN_STATUS_LABEL[selected.status] ?? selected.status}
              </span>
            )}
            <button
              type="button"
              onClick={() => setSelectedKey("")}
              className="ml-auto text-[11px] text-ink-3 hover:text-ink"
            >
              关闭
            </button>
          </div>

          <div className="grid gap-x-6 gap-y-0 sm:grid-cols-2 lg:grid-cols-4">
            <KV label="时长" value={fmtMs(spanDur(selected))} />
            <KV label="内容字节" value={fmtBytes(asNum(selected.content_bytes))} />
            <KV
              label="Tokens in/out"
              value={`${fmtInt(asNum(selected.tokens_in))} / ${fmtInt(asNum(selected.tokens_out))}`}
            />
            <KV label="成本" value={fmtUsd(asNum(selected.cost_usd))} />
            <KV
              label="上下文"
              value={`${fmtInt(asNum(selected.context_used))} / ${fmtInt(asNum(selected.context_size))}`}
            />
            <KV label="工具" value={selected.tool_name || DASH} />
            <KV label="错误类别" value={selected.error_class || DASH} />
            <KV label="父 span" value={selected.parent_span_id || DASH} />
            {asNum(selected.retry_count) ? (
              <KV label="重试次数" value={fmtInt(asNum(selected.retry_count))} />
            ) : null}
            {selected.args_hash ? <KV label="参数摘要" value={selected.args_hash} /> : null}
          </div>

          <div>
            <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3">
              attrs
            </div>
            <pre className="mt-1 max-h-64 overflow-auto rounded-md border border-line bg-surface-0 p-2 font-mono text-[11px] leading-relaxed text-ink-2">
              {JSON.stringify(selected.attrs ?? {}, null, 2)}
            </pre>
          </div>
        </div>
      )}

      {spans.length < 5 && (
        <p className="border-t border-line px-4 py-2 text-[11px] leading-relaxed text-amber-300/90">
          样本不足（n&lt;5）：单次 run 的耗时只用于定位问题，不代表统计规律；据此得出的发现按
          L1/L2 理解。
        </p>
      )}
    </SectionCard>
  );
}

// 空态图标：用内联 SVG 而不是 emoji（与 Empty 的 lucide 风格一致）。
function EmptyTraceIcon() {
  return (
    <svg
      width="18"
      height="18"
      viewBox="0 0 24 24"
      fill="none"
      stroke="currentColor"
      strokeWidth="1.5"
      strokeLinecap="round"
      strokeLinejoin="round"
      aria-hidden="true"
    >
      <path d="M4 6h16M4 12h10M4 18h6" />
    </svg>
  );
}