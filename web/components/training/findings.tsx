"use client";

import { useState } from "react";
import { ArrowDown, Check, EyeOff } from "lucide-react";
import { Button, Empty, SectionCard } from "@/components/ui";
import { useUpdateRsiFinding } from "@/lib/queries";
import type { RsiDetail, RsiFinding } from "@/lib/types";
import { cn } from "@/lib/utils";
import { categoryLabel, confidenceMeta, severityMeta } from "./format";

function scoreText(score: unknown): string {
  if (typeof score !== "number" || !Number.isFinite(score)) return "—";
  return score.toFixed(2);
}

// 处理过的 finding 保留在列表里（不回退、不隐藏），只是把操作换成状态字：
// 用户能回看自己确认/忽略过什么，比“点完就消失”更可解释。
function FindingStatusChip({ status }: { status: RsiFinding["status"] }) {
  if (status === "confirmed") {
    return (
      <span className="inline-flex items-center gap-1 rounded-md border border-emerald-500/30 bg-emerald-500/10 px-1.5 py-0.5 text-[11px] text-emerald-300">
        <Check className="h-3 w-3" /> 已确认
      </span>
    );
  }
  if (status === "dismissed") {
    return (
      <span className="inline-flex items-center gap-1 rounded-md border border-line bg-surface-2 px-1.5 py-0.5 text-[11px] text-ink-3">
        <EyeOff className="h-3 w-3" /> 已忽略
      </span>
    );
  }
  return null;
}

export function TrainingFindings({ detail }: { detail: RsiDetail }) {
  const update = useUpdateRsiFinding();
  const [err, setErr] = useState("");

  // score 降序（undefined/NaN 沉底），同分按 id 稳定排序，避免每次渲染抖动。
  const findings = [...(detail.findings ?? [])].sort((a, b) => {
    const sa = typeof a.score === "number" && Number.isFinite(a.score) ? a.score : -Infinity;
    const sb = typeof b.score === "number" && Number.isFinite(b.score) ? b.score : -Infinity;
    if (sb !== sa) return sb - sa;
    return a.id.localeCompare(b.id);
  });

  const act = (f: RsiFinding, status: "confirmed" | "dismissed") => {
    setErr("");
    update.mutate(
      { id: f.id, status },
      { onError: (e) => setErr(e instanceof Error ? e.message : String(e)) }
    );
  };

  if (findings.length === 0) {
    const inFlight = ["waiting", "analyzing"].includes(detail.session.status);
    return (
      <SectionCard title="发现">
        <Empty icon={<EyeOff className="h-5 w-5" />}>
          {inFlight
            ? "分析还在进行；报告生成后这里会列出命中的结构/分派/信息流瓶颈。"
            : "本次分析没有命中结构规则：要么编排没有明显异常，要么样本太少不足以判定。"}
        </Empty>
      </SectionCard>
    );
  }

  return (
    <div className="space-y-3">
      <div className="flex items-center gap-2 text-[11px] text-ink-3">
        <ArrowDown className="h-3.5 w-3.5" />
        共 {findings.length} 条发现，按 score 降序（高分 = 更值得先看）
      </div>

      {err && (
        <p className="rounded-md border border-red-500/25 bg-red-500/10 px-3 py-2 text-xs text-red-300">
          操作失败：{err}
        </p>
      )}

      {findings.map((f) => {
        const sev = severityMeta(f.severity);
        const conf = confidenceMeta(f.confidence);
        const pending = update.isPending && update.variables?.id === f.id;
        // 后端 evidence_json 是 []string：目前是 "metric=value" 测量串，
        // 也允许以后塞 span/run id；原样展示，不做二次解释。
        const evidence = Array.isArray(f.evidence)
          ? f.evidence.filter((x): x is string => typeof x === "string" && x.length > 0)
          : [];
        return (
          <article
            key={f.id}
            className={cn(
              "overflow-hidden rounded-lg border border-line bg-surface-1",
              f.status === "dismissed" && "opacity-60"
            )}
          >
            <header className="flex items-start gap-3 px-4 py-3">
              <span
                className={cn(
                  "mt-0.5 shrink-0 rounded border px-1.5 py-0.5 text-[11px] font-medium",
                  sev.cls
                )}
              >
                {sev.label}
              </span>
              <div className="min-w-0 flex-1">
                <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                  <h3 className="text-sm font-medium text-ink">{f.title || "未命名发现"}</h3>
                  <FindingStatusChip status={f.status} />
                </div>
                <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-ink-3">
                  <span>{categoryLabel(f.category)}</span>
                  <span className="inline-flex items-center gap-1">
                    置信度
                    <span
                      className="rounded border border-line bg-surface-2 px-1 py-0.5 font-mono text-[10px] text-ink-2"
                      title={conf.desc}
                    >
                      {conf.label}
                    </span>
                  </span>
                  <span title={evidence.join("\n")}>证据 {evidence.length} 条</span>
                  <span className="font-mono tabular-nums">score {scoreText(f.score)}</span>
                </div>
              </div>
              <div className="flex shrink-0 items-center gap-1.5">
                {f.status === "open" ? (
                  <>
                    <Button
                      variant="outline"
                      className="h-7 px-2 text-xs hover:border-emerald-500/40 hover:text-emerald-300"
                      disabled={pending}
                      onClick={() => act(f, "confirmed")}
                    >
                      <Check className="mr-1 h-3.5 w-3.5" /> 确认
                    </Button>
                    <Button
                      variant="ghost"
                      className="h-7 px-2 text-xs hover:text-red-300"
                      disabled={pending}
                      onClick={() => act(f, "dismissed")}
                    >
                      <EyeOff className="mr-1 h-3.5 w-3.5" /> 忽略
                    </Button>
                  </>
                ) : null}
              </div>
            </header>

            <div className="space-y-2.5 border-t border-line px-4 py-3">
              <div>
                <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3">
                  判据
                </div>
                <p className="mt-1 whitespace-pre-wrap text-xs leading-relaxed text-ink-2">
                  {f.explanation || "—"}
                </p>
                {evidence.length > 0 && (
                  <p className="mt-1.5 truncate font-mono text-[10px] text-ink-3" title={evidence.join("\n")}>
                    证据：{evidence.slice(0, 4).join(" · ")}
                    {evidence.length > 4 ? ` · +${evidence.length - 4}` : ""}
                  </p>
                )}
              </div>
              {f.suggestion && (
                <div className="rounded-md border-l-2 border-accent/50 bg-surface-2/40 px-3 py-2">
                  <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3">
                    建议
                  </div>
                  <p className="mt-1 whitespace-pre-wrap text-xs leading-relaxed text-ink-2">
                    {f.suggestion}
                  </p>
                </div>
              )}
            </div>
          </article>
        );
      })}
    </div>
  );
}