"use client";

import { useEffect, useRef, useState, useCallback, useMemo } from "react";
import Link from "next/link";
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

// The log list sits on a DARK panel (bg-black/40) — these are dark-theme
// text colors. The previous light-theme set (info = zinc-700 on near-black)
// made the most common lines nearly invisible.
const LEVEL_STYLE: Record<string, string> = {
  debug: "text-ink-3",
  info: "text-ink",
  warn: "text-amber-400",
  error: "text-red-400 font-medium",
};

const LEVEL_DOT: Record<string, string> = {
  debug: "bg-zinc-300",
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

  return (
    <div className="p-6 max-w-5xl mx-auto space-y-4">
      <div className="flex items-center gap-3 flex-wrap">
        <h1 className="text-lg font-semibold text-ink">日志</h1>
        <span className="text-xs text-ink-3">~/.agentwork/daemon.log（10MB 滚动）</span>
        <div className="ml-auto flex items-center gap-2">
          <select
            value={level}
            onChange={(e) => changeLevel(e.target.value)}
            className="border border-line rounded-lg px-2 py-1.5 text-xs bg-surface-1"
            title="运行时日志级别（写盘过滤，重启后保持）"
          >
            {LEVELS.map((lv) => (
              <option key={lv} value={lv}>{lv}</option>
            ))}
          </select>
          <button
            onClick={() => setPaused(!paused)}
            className={`text-xs px-2.5 py-1.5 rounded-lg border ${paused ? "border-amber-500/40 bg-amber-500/10 text-amber-300" : "border-line text-ink-2 hover:bg-surface-0"}`}
          >
            {paused ? "已暂停（点击继续）" : "暂停"}
          </button>
          <button
            onClick={() => setLines([])}
            className="text-xs px-2.5 py-1.5 rounded-lg border border-line text-ink-2 hover:bg-surface-0"
          >
            清空
          </button>
        </div>
      </div>

      <div className="flex items-center gap-2 flex-wrap">
        <input
          value={keyword}
          onChange={(e) => setKeyword(e.target.value)}
          placeholder="关键词过滤…"
          className="border border-line rounded-lg px-2.5 py-1.5 text-xs w-56"
        />
        <span className="text-xs text-ink-3">从</span>
        <input
          type="datetime-local"
          value={after}
          onChange={(e) => setAfter(e.target.value)}
          className="border border-line rounded-lg px-2 py-1.5 text-xs"
        />
        <span className="text-xs text-ink-3">到</span>
        <input
          type="datetime-local"
          value={before}
          onChange={(e) => setBefore(e.target.value)}
          className="border border-line rounded-lg px-2 py-1.5 text-xs"
        />
        <button
          onClick={queryHistory}
          disabled={loading}
          className="text-xs px-2.5 py-1.5 rounded-lg bg-accent text-accent-ink hover:bg-accent-dim disabled:opacity-50"
        >
          {loading ? "查询中…" : "按时间查询"}
        </button>
        {(after || before) && (
          <button
            onClick={() => { setAfter(""); setBefore(""); queryHistory(); }}
            className="text-xs text-accent hover:underline"
          >
            重置时间
          </button>
        )}
      </div>

      {err && <p className="text-xs text-red-400">{err}</p>}

      <div ref={listRef} className="bg-black/40 rounded-xl border border-line-strong p-3 h-[calc(100vh-260px)] overflow-y-auto font-mono text-[11px] leading-relaxed">
        {visible.length === 0 && !loading && <p className="text-ink-3 py-4 text-center">暂无日志</p>}
        {visible.map((l, i) => (
          <div key={i} className="flex gap-2 hover:bg-white/5 rounded px-1">
            <span className={`shrink-0 mt-[7px] h-1.5 w-1.5 rounded-full ${LEVEL_DOT[l.level] ?? LEVEL_DOT.info}`} />
            <span className="shrink-0 text-ink-3 tabular-nums" title={l.ts}>{l.ts ? tsShort(l.ts) : ""}</span>
            <span className={`whitespace-pre-wrap break-words ${LEVEL_STYLE[l.level] ?? LEVEL_STYLE.info}`}>{renderText(l.text, goalById)}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
