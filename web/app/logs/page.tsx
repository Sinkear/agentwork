"use client";

import { useEffect, useRef, useState, useCallback, useMemo } from "react";
import Link from "next/link";
import { Pause, Play, Trash2 } from "lucide-react";
import { Button, PageBody, PageHeader, SectionCard, Segmented, StatStrip } from "@/components/ui";
import { fetchLogs, getLogLevel, setLogLevel } from "@/lib/api";
import { useGoals } from "@/lib/queries";
import { useWSEvent } from "@/lib/ws";
import type { LogLine } from "@/lib/types";

// goalIDRe matches a full goal uuid inside a log line — ids are for the
// system, the panel renders them as TITLE links.
const goalIDRe = /([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})/gi;

// tsShort renders the timestamp compactly and FIXED-WIDTH (MM-DD HH:mm:ss)
// so the column stays aligned line to line — the full RFC3339 value is on
// the hover title.
function tsShort(ts: string): string {
  const d = new Date(ts);
  if (isNaN(d.getTime())) return ts;
  const p = (n: number) => String(n).padStart(2, "0");
  return `${p(d.getMonth() + 1)}-${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

// renderText splits a log line on goal uuids and renders known goals as
// titled links (unknown ids stay as the bare id).
function renderText(text: string, goalById: Map<string, string>) {
  const parts = text.split(goalIDRe);
  return parts.map((part, i) => {
    if (!goalIDRe.test(part)) return part; // plain segment
    goalIDRe.lastIndex = 0;
    const title = goalById.get(part);
    return (
      <Link
        key={i}
        href={`/goals/${part}`}
        className="text-sky-400 underline decoration-sky-700 hover:text-sky-300"
        title={part}
      >
        {title ?? part.slice(0, 8)}
      </Link>
    );
  });
}

const LEVELS = ["debug", "info", "warn", "error"] as const;

// 级别色走语义 token：面板底色随主题变（浅色白卡 / 深色深底），
// 所以文字用 -400 档——浅色主题会把同一槽位映射成深饱和值，两套主题都够对比。
const LEVEL_STYLE: Record<string, string> = {
  debug: "text-ink-3",
  info: "text-ink",
  warn: "text-amber-400",
  error: "text-red-400 font-medium",
};

const LEVEL_DOT: Record<string, string> = {
  debug: "bg-zinc-400",
  info: "bg-sky-400",
  warn: "bg-amber-500",
  error: "bg-red-500",
};

// LogsPanel is the daemon log viewer: a live tail (WS log:line) plus
// time/level/keyword-filtered history (GET /logs). The level selector is
// ALSO the runtime knob — it PUTs /logs/level, which persists.
export default function LogsPage() {
  const [lines, setLines] = useState<LogLine[]>([]);
  const { data: goals } = useGoals();
  // goal id → title — the log lines resolve ids into human titles.
  const goalById = useMemo(
    () => new Map((goals ?? []).map((g) => [g.id, g.title])),
    [goals]
  );
  const [level, setLevel] = useState<string>("info");
  const [keyword, setKeyword] = useState("");
  const [after, setAfter] = useState("");
  const [before, setBefore] = useState("");
  const [paused, setPaused] = useState(false);
  const [loading, setLoading] = useState(false);
  const [err, setErr] = useState<string | null>(null);
  const listRef = useRef<HTMLDivElement>(null);
  const pausedRef = useRef(paused);
  pausedRef.current = paused;

  const appendLive = useCallback((l: LogLine) => {
    if (pausedRef.current) return;
    setLines((prev) => [...prev.slice(-999), l]);
  }, []);

  useWSEvent("log:line", (p) => {
    const m = p as { ts?: string; level?: string; text?: string };
    if (typeof m?.text === "string") {
      appendLive({ ts: m.ts ?? "", level: m.level ?? "info", text: m.text });
    }
  });

  // Initial load + runtime level.
  useEffect(() => {
    setLoading(true);
    Promise.all([fetchLogs({ limit: 500, level }), getLogLevel().catch(() => null)])
      .then(([res, lv]) => {
        setLines(res.lines);
        if (lv) setLevel(lv.level);
      })
      .catch((e) => setErr(String(e)))
      .finally(() => setLoading(false));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // Autoscroll on new lines (unless the user scrolled up).
  useEffect(() => {
    const el = listRef.current;
    if (el) el.scrollTop = el.scrollHeight;
  }, [lines]);

  const changeLevel = async (lv: string) => {
    setLevel(lv);
    try {
      const r = await setLogLevel(lv);
      setLevel(r.level);
    } catch (e) {
      setErr(String(e));
    }
    setLoading(true);
    try {
      const res = await fetchLogs({ limit: 500, level: lv });
      setLines(res.lines);
    } catch (e) {
      setErr(String(e));
    } finally {
      setLoading(false);
    }
  };

  const queryHistory = async () => {
    setLoading(true);
    setErr(null);
    try {
      const res = await fetchLogs({
        after: after || undefined,
        before: before || undefined,
        limit: 1000,
        level,
      });
      setLines(res.lines);
    } catch (e) {
      setErr(String(e));
    } finally {
      setLoading(false);
    }
  };

  const visible = lines.filter((l) => !keyword || l.text.toLowerCase().includes(keyword.toLowerCase()));

  // 当前这批日志的级别分布——顶部概览条用它，比一串“暂无日志”有意义。
  const counts = useMemo(() => {
    const c: Record<string, number> = { debug: 0, info: 0, warn: 0, error: 0 };
    for (const l of visible) c[l.level] = (c[l.level] ?? 0) + 1;
    return c;
  }, [visible]);

  return (
    <PageBody className="space-y-5">
      <PageHeader
        title="日志"
        desc="~/.agentwork/daemon.log（10MB 滚动）· 实时 tail 与按时间查询"
        action={
          <Segmented
            value={level}
            onChange={changeLevel}
            options={LEVELS.map((lv) => ({ value: lv, label: lv }))}
          />
        }
      />

      <StatStrip
        items={[
          { label: "显示中", value: visible.length, hint: `共载入 ${lines.length} 行` },
          { label: "error", value: counts.error, tone: counts.error ? "err" : "muted" },
          { label: "warn", value: counts.warn, tone: counts.warn ? "warn" : "muted" },
          { label: "状态", value: paused ? "已暂停" : "实时", tone: paused ? "warn" : "ok", hint: paused ? "点击继续接收" : "来自 WS 的 log:line" },
        ]}
      />

      <SectionCard
        title="输出"
        count={visible.length}
        desc={paused ? "已暂停接收新日志" : level === "debug" ? "含 debug" : `级别 ≥ ${level}`}
        bodyClassName="p-0"
        action={
          <>
            <Button variant="outline" onClick={() => setPaused(!paused)} className="h-7 px-2 text-xs gap-1">
              {paused ? <Play className="h-3.5 w-3.5" /> : <Pause className="h-3.5 w-3.5" />}
              {paused ? "继续" : "暂停"}
            </Button>
            <Button variant="ghost" onClick={() => setLines([])} className="h-7 px-2 text-xs gap-1">
              <Trash2 className="h-3.5 w-3.5" />
              清空
            </Button>
          </>
        }
      >
        {/* 筛选行：时间范围 + 关键词 + 查询 */}
        <div className="flex flex-wrap items-center gap-2 border-b border-line px-3 py-2">
          <input
            value={keyword}
            onChange={(e) => setKeyword(e.target.value)}
            placeholder="关键词过滤…"
            className="w-52 rounded-md border border-line bg-surface-2 px-2.5 py-1.5 text-xs text-ink outline-none transition-colors placeholder:text-ink-3 focus:border-accent-dim"
          />
          <span className="text-xs text-ink-3">从</span>
          <input
            type="datetime-local"
            value={after}
            onChange={(e) => setAfter(e.target.value)}
            className="rounded-md border border-line bg-surface-2 px-2 py-1.5 text-xs text-ink outline-none focus:border-accent-dim"
          />
          <span className="text-xs text-ink-3">到</span>
          <input
            type="datetime-local"
            value={before}
            onChange={(e) => setBefore(e.target.value)}
            className="rounded-md border border-line bg-surface-2 px-2 py-1.5 text-xs text-ink outline-none focus:border-accent-dim"
          />
          <Button onClick={queryHistory} disabled={loading} variant="outline" className="h-7 px-2.5 text-xs">
            {loading ? "查询中…" : "按时间查询"}
          </Button>
          {(after || before) && (
            <button
              onClick={() => { setAfter(""); setBefore(""); queryHistory(); }}
              className="text-xs text-accent-dim hover:text-accent"
            >
              重置时间
            </button>
          )}
          {err && <span className="text-xs text-red-400">{err}</span>}
        </div>

        {/* 控制台：等宽字体、行悬停提亮、按级别着色 */}
        <div
          ref={listRef}
          className="h-[calc(100vh-360px)] min-h-[240px] overflow-y-auto bg-surface-0 px-3 py-2 font-mono text-[11.5px] leading-relaxed"
        >
          {visible.length === 0 && !loading && (
            <p className="py-10 text-center text-xs text-ink-3">
              {lines.length === 0 ? "暂无日志——上面点「按时间查询」拉历史，或等实时事件" : "没有匹配当前过滤条件的日志"}
            </p>
          )}
          {visible.map((l, i) => (
            <div key={i} className="flex gap-2 rounded px-1 transition-colors hover:bg-surface-2">
              <span className={`mt-[7px] h-1.5 w-1.5 shrink-0 rounded-full ${LEVEL_DOT[l.level] ?? LEVEL_DOT.info}`} />
              <span className="shrink-0 tabular-nums text-ink-3" title={l.ts}>{l.ts ? tsShort(l.ts) : ""}</span>
              <span className={`whitespace-pre-wrap break-words ${LEVEL_STYLE[l.level] ?? LEVEL_STYLE.info}`}>
                {renderText(l.text, goalById)}
              </span>
            </div>
          ))}
        </div>
      </SectionCard>
    </PageBody>
  );
}
