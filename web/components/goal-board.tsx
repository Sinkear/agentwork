"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowRight, Loader2 } from "lucide-react";
import { AttentionChip, Button, Dialog, ReviewPhaseBadge, STATUS_LABEL } from "@/components/ui";
import {
  useActivateGoal,
  useCancelGoal,
  useReopenGoal,
  useResolveGoalReview,
} from "@/lib/queries";
import { cn, relTime } from "@/lib/utils";
import type { Agent, Goal, GoalStatus, Squad } from "@/lib/types";

// 看板的一列。fundamental 的 6 个 GoalStatus 被折叠成 5 列：
// failed 与 cancelled 同属「已结束」——它们对使用者是同一件事（不再推进了），
// 而「待审批」是 agent 跑完把关点自动进入的独立工位。
export type BoardCol = "backlog" | "active" | "review" | "done" | "closed";

const COLUMNS: {
  id: BoardCol;
  label: string;
  hint: string;
  dot: string;
}[] = [
  { id: "backlog", label: "待办", hint: "排队等开工", dot: "bg-zinc-400" },
  { id: "active", label: "进行中", hint: "agent 正在跑", dot: "bg-blue-400" },
  { id: "review", label: "待审批", hint: "等你拍板", dot: "bg-purple-400" },
  { id: "done", label: "已完成", hint: "验收通过", dot: "bg-emerald-400" },
  { id: "closed", label: "已结束", hint: "失败 · 取消", dot: "bg-zinc-500" },
];

const COL_OF: Record<GoalStatus, BoardCol> = {
  backlog: "backlog",
  active: "active",
  review: "review",
  done: "done",
  failed: "closed",
  cancelled: "closed",
};

// 一次落点允许做的事。只有后端真的支持的动作才会出现在这里——
// 看板不制造「拖过去就成功」的假象：不合法的落点会给出原因。
type MoveKind = "activate" | "cancel" | "reopen" | "approve" | "reject";

type Move = {
  kind: MoveKind;
  drop: string; // 拖动时列头显示的提示
  done: string; // 成功后的 toast
  confirmTitle?: string;
  confirmBody?: string;
  confirmCta?: string;
  danger?: boolean;
};

function planMove(g: Goal, to: BoardCol): Move | null {
  const from = COL_OF[g.status];
  if (from === to) return null;

  if (from === "backlog" && to === "active")
    return { kind: "activate", drop: "松手开始执行", done: "已开始执行" };

  if (from === "review" && to === "done")
    return {
      kind: "approve",
      drop: "松手通过并合并",
      done: "已通过，正在合并（合并完成前仍留在待审批）",
      confirmTitle: "通过验收并合并？",
      confirmBody:
        "通过后平台会异步把分支合入默认分支，合并期间任务仍停留在「待审批」，完成后自动进入已完成。",
      confirmCta: "通过并合并",
    };

  if (from === "review" && to === "active")
    return {
      kind: "reject",
      drop: "松手打回重做",
      done: "已打回，agent 继续修",
      confirmTitle: "打回给 agent 重做？",
      confirmBody: "任务回到进行中，agent 按你的意见继续修，然后再跑一轮验收。",
      confirmCta: "打回",
    };

  if ((from === "done" || from === "closed") && to === "active")
    return {
      kind: "reopen",
      drop: "松手重新打开",
      done: "已重新打开",
      confirmTitle: "重新打开任务？",
      confirmBody: "会派发一次新的执行；原来的关闭记录留在时间线里。",
      confirmCta: "重新打开",
    };

  if ((from === "backlog" || from === "active") && to === "closed")
    return {
      kind: "cancel",
      drop: "松手取消任务",
      done: "已取消",
      danger: true,
      confirmTitle: "取消这个任务？",
      confirmBody: "排队中的 run 会被丢弃，正在跑的 run 会被回收；之后还能重新打开。",
      confirmCta: "取消任务",
    };

  return null;
}

// 后端会把“为什么不行”直接放在错误里（英文）。看板只翻译几个真的会发生的，
// 其余原样透出——宁可看到原文，也不要编一个不准确的中文说法。
function describeError(e: unknown): string {
  const raw = e instanceof Error ? e.message : String(e);
  if (/already approved/i.test(raw))
    return "这个任务已经通过审批，正在等交付/合并完成；详情页里能看到合并结果。";
  if (/only backlog goals can be activated/i.test(raw)) return "只有「待办」里的任务可以开始执行。";
  if (/goal is not in review/i.test(raw)) return "任务已经不在「待审批」了，刷新一下看最新状态。";
  if (/not found/i.test(raw)) return "任务已经不存在了，可能已被删除。";
  return raw;
}

function refusal(g: Goal, to: BoardCol): string {
  const from = COL_OF[g.status];
  if (to === "backlog") return "不能拖回「待办」：任务只会往前走，要中止请拖到「已结束」。";
  if (to === "review") return "「待审批」由 agent 跑完把关点自动进入，不能手动放进去。";
  if (to === "done" && from === "active") return "完成由验收决定：agent 跑完会自己进入「待审批」。";
  if (to === "done" && from === "backlog") return "先开始执行，验收通过后才会到「已完成」。";
  if (to === "closed" && from === "review") return "待审批只能「通过」或「打回」：先打回，再取消。";
  if (to === "closed" && (from === "done" || from === "closed"))
    return "已完成 / 已结束的任务只能重新打开，不能再取消。";
  return "这一列不接受这个任务，松手位置没变。";
}

interface Drag {
  goal: Goal;
  from: GoalStatus;
  w: number;
  ox: number;
  oy: number;
  x: number;
  y: number;
}

interface Pending {
  goal: Goal;
  from: GoalStatus;
  sx: number;
  sy: number;
  ox: number;
  oy: number;
  w: number;
}

interface Toast {
  tone: "ok" | "warn" | "err";
  text: string;
}

const TOAST_CLS: Record<Toast["tone"], string> = {
  ok: "border-accent/40 bg-surface-1 text-ink",
  warn: "border-amber-500/40 bg-surface-1 text-amber-300",
  err: "border-red-500/40 bg-surface-1 text-red-300",
};

const initials = (s: string) => (s ? s.slice(0, 1).toUpperCase() : "?");

export function GoalBoard({
  goals,
  agents,
  squads,
}: {
  goals: Goal[];
  agents: Agent[];
  squads: Squad[];
}) {
  const activate = useActivateGoal();
  const cancel = useCancelGoal();
  const reopen = useReopenGoal();
  const resolve = useResolveGoalReview();

  const [drag, setDrag] = useState<Drag | null>(null);
  const [hover, setHover] = useState<{ col: BoardCol; legal: boolean; hint: string } | null>(null);
  const [moved, setMoved] = useState<Record<string, BoardCol>>({});
  const [confirm, setConfirm] = useState<{ goal: Goal; col: BoardCol; move: Move } | null>(null);
  // 已通过、正在异步合并的任务：后端合并期间仍留在「待审批」，用一个小徽标
  // 说明“点了有效果”，否则卡片原地不动会被当成没生效。
  const [merging, setMerging] = useState<Record<string, boolean>>({});
  const [toast, setToast] = useState<Toast | null>(null);

  const pending = useRef<Pending | null>(null);
  const listeners = useRef<{ move: (e: PointerEvent) => void; up: (e: PointerEvent) => void } | null>(null);
  const dragging = useRef<Drag | null>(null);
  const suppressUntil = useRef(0);
  const scrollRef = useRef<HTMLDivElement | null>(null);

  // toast 自动消失
  useEffect(() => {
    if (!toast) return;
    const t = setTimeout(() => setToast(null), 4200);
    return () => clearTimeout(t);
  }, [toast]);

  // 拖动时全局禁选中，避免文字被一起拖蓝
  useEffect(() => {
    if (!drag) return;
    const el = document.body;
    el.classList.add("select-none");
    return () => el.classList.remove("select-none");
  }, [drag]);

  // 合并完成后后端把状态推进到 done，徽标随之消失
  useEffect(() => {
    setMerging((m) => {
      const stale = Object.keys(m).filter((id) => goals.find((g) => g.id === id)?.status !== "review");
      if (stale.length === 0) return m;
      const next = { ...m };
      for (const id of stale) delete next[id];
      return next;
    });
  }, [goals]);

  const nameOf = useMemo(() => {
    const m = new Map<string, string>();
    for (const a of agents) m.set(`agent:${a.id}`, a.name);
    for (const s of squads) m.set(`squad:${s.id}`, s.name);
    return (g: Goal) => m.get(`${g.assignee_type}:${g.assignee_id}`) || g.assignee_type;
  }, [agents, squads]);

  const byCol = useMemo(() => {
    const box: Record<BoardCol, Goal[]> = { backlog: [], active: [], review: [], done: [], closed: [] };
    const sorted = [...goals].sort((a, b) => (a.created_at < b.created_at ? 1 : -1));
    for (const g of sorted) box[moved[g.id] ?? COL_OF[g.status]].push(g);
    return box;
  }, [goals, moved]);

  function runMove(goal: Goal, col: BoardCol, move: Move) {
    // 通过验收不会立刻改变列：后端在异步合并期间仍把任务留在「待审批」。
    // 若这里也乐观移动，卡片会在几百毫秒后自己弹回去，看起来像操作失败。
    if (move.kind !== "approve") setMoved((m) => ({ ...m, [goal.id]: col }));
    const done = () => {
      setToast({ tone: "ok", text: move.done });
      if (move.kind === "approve")
        setMerging((m) => ({ ...m, [goal.id]: true }));
      setTimeout(
        () =>
          setMoved((m) => {
            const next = { ...m };
            delete next[goal.id];
            return next;
          }),
        1200
      );
    };
    const fail = (e: unknown) => {
      setToast({ tone: "err", text: describeError(e) });
      setMoved((m) => {
        const next = { ...m };
        delete next[goal.id];
        return next;
      });
    };
    const opts = { onSuccess: done, onError: fail };
    switch (move.kind) {
      case "activate":
        activate.mutate(goal.id, opts);
        break;
      case "cancel":
        cancel.mutate(goal.id, opts);
        break;
      case "reopen":
        reopen.mutate(goal.id, opts);
        break;
      case "approve":
        resolve.mutate({ id: goal.id, decision: "approve" }, opts);
        break;
      case "reject":
        resolve.mutate({ id: goal.id, decision: "reject" }, opts);
        break;
    }
  }

  function attempt(goal: Goal, col: BoardCol) {
    if (col === COL_OF[goal.status]) return;
    const move = planMove(goal, col);
    if (!move) {
      setToast({ tone: "warn", text: refusal(goal, col) });
      return;
    }
    if (move.confirmTitle) setConfirm({ goal, col, move });
    else runMove(goal, col, move);
  }

  // ── 指针拖动 ──
  // 只有移动超过 5px 才算拖动；否则交给 <a> 的原生点击（支持中键/Cmd 新开标签）。
  // 监听挂在 document 上而不是靠 setPointerCapture：浏览器会把同一帧内的多次
  // pointermove 合并成一次，capture 在合并/跨元素时并不可靠（真机上表现为
  // “拖不动”），而 document 监听无论指针落到哪都能收到。
  function detach() {
    const d = listeners.current;
    if (!d) return;
    document.removeEventListener("pointermove", d.move);
    document.removeEventListener("pointerup", d.up);
    document.removeEventListener("pointercancel", d.up);
    listeners.current = null;
  }

  useEffect(() => () => detach(), []);

  function onDown(e: React.PointerEvent<HTMLAnchorElement>, goal: Goal) {
    if (e.button !== 0) return;
    if ((e.target as HTMLElement).closest("[data-no-drag]")) return;
    const r = e.currentTarget.getBoundingClientRect();
    pending.current = {
      goal,
      from: goal.status,
      sx: e.clientX,
      sy: e.clientY,
      ox: e.clientX - r.left,
      oy: e.clientY - r.top,
      w: r.width,
    };
    if (listeners.current) detach();
    const move = (ev: PointerEvent) => onDocMove(ev);
    const up = (ev: PointerEvent) => onDocUp(ev);
    listeners.current = { move, up };
    document.addEventListener("pointermove", move);
    document.addEventListener("pointerup", up);
    document.addEventListener("pointercancel", up);
  }

  function colAt(x: number, y: number): BoardCol | null {
    const el = document.elementFromPoint(x, y) as HTMLElement | null;
    const box = el?.closest("[data-board-col]") as HTMLElement | null;
    const col = box?.getAttribute("data-board-col");
    return (col as BoardCol | null) ?? null;
  }

  function onDocMove(e: PointerEvent) {
    const p = pending.current;
    if (!p) return;
    if (!dragging.current) {
      if (Math.hypot(e.clientX - p.sx, e.clientY - p.sy) < 5) return;
      dragging.current = { goal: p.goal, from: p.from, w: p.w, ox: p.ox, oy: p.oy, x: e.clientX, y: e.clientY };
      setDrag(dragging.current);
    }
    dragging.current = { ...dragging.current!, x: e.clientX, y: e.clientY };
    setDrag(dragging.current);

    const col = colAt(e.clientX, e.clientY);
    if (!col) setHover(null);
    else {
      const move = planMove(p.goal, col);
      setHover({ col, legal: !!move, hint: move ? move.drop : refusal(p.goal, col) });
    }

    // 靠近左右边缘时自动横向滚动
    const sc = scrollRef.current;
    if (sc) {
      const r = sc.getBoundingClientRect();
      if (e.clientX > r.right - 90) sc.scrollLeft += 18;
      else if (e.clientX < r.left + 90) sc.scrollLeft -= 18;
    }
  }

  function onDocUp(e: PointerEvent) {
    const d = dragging.current;
    pending.current = null;
    detach();
    if (!d) return; // 纯点击：交给原生 <a>

    dragging.current = null;
    setDrag(null);
    setHover(null);
    suppressUntil.current = Date.now() + 300; // 松手后的 click 不再导航

    const col = colAt(e.clientX, e.clientY);
    if (!col) {
      setToast({ tone: "warn", text: "把卡片拖到某一列里再松手。" });
      return;
    }
    attempt(d.goal, col);
  }

  const cardBase =
    "group relative block rounded-lg border border-line bg-surface-1 p-3 transition-colors duration-150 hover:border-line-strong";

  return (
    <div className="flex min-h-0 flex-1 flex-col">
      <div className="mb-2 flex items-center gap-2 text-[11px] text-ink-3">
        <ArrowRight className="h-3 w-3" />
        <span>拖动卡片改状态；也可以点卡片底部的操作，或用「列表」视图批量处理。</span>
      </div>

      <div ref={scrollRef} className="flex min-h-0 flex-1 gap-2.5 overflow-x-auto pb-1">
        {COLUMNS.map((col) => {
          const items = byCol[col.id];
          const hovered = drag && hover?.col === col.id;
          return (
            <section
              key={col.id}
              data-board-col={col.id}
              className={cn(
                // 宽屏时五列均分铺满，窄屏（<1180）才横向滚动：min-w 保证卡片不被挤扁。
                "flex min-w-[180px] flex-1 basis-[272px] flex-col rounded-lg border transition-colors duration-150",
                "border-line bg-surface-0/60",
                hovered && (hover!.legal ? "border-accent/45 bg-accent/[0.04]" : "border-amber-500/40 bg-amber-500/[0.05]")
              )}
            >
              <header className="flex items-center gap-2 rounded-t-lg border-b border-line bg-surface-1/95 px-3 py-2 backdrop-blur">
                <span className={cn("h-1.5 w-1.5 rounded-full", col.dot)} />
                <span className="text-[12px] font-medium text-ink">{col.label}</span>
                <span className="font-mono text-[10px] tabular-nums text-ink-3">{items.length}</span>
                <span className="ml-auto truncate text-[10px] text-ink-3" title={hovered ? hover!.hint : col.hint}>
                  {hovered ? (
                    <span className={hover!.legal ? "text-accent" : "text-amber-400"}>
                      {hover!.legal ? hover!.hint : "不能放在这里"}
                    </span>
                  ) : (
                    col.hint
                  )}
                </span>
              </header>

              <div className="flex min-h-0 flex-1 flex-col gap-2 overflow-y-auto p-2">
                {items.length === 0 && (
                  <div
                    className={cn(
                      "flex h-20 items-center justify-center rounded-lg border border-dashed text-[11px]",
                      hovered && hover!.legal
                        ? "border-accent/40 text-accent"
                        : "border-line/70 text-ink-3/70"
                    )}
                  >
                    {hovered && hover!.legal ? hover!.hint : "这一列是空的"}
                  </div>
                )}

                {items.map((g) => {
                  const isDragging = drag?.goal.id === g.id;
                  const isPending = moved[g.id] !== undefined;
                  return (
                    <a
                      key={g.id}
                      href={`/goals/${g.id}`}
                      draggable={false}
                      onPointerDown={(e) => onDown(e, g)}
                      onClickCapture={(e) => {
                        if (Date.now() < suppressUntil.current) {
                          e.preventDefault();
                          e.stopPropagation();
                        }
                      }}
                      className={cn(
                        cardBase,
                        "cursor-grab active:cursor-grabbing",
                        isDragging && "opacity-30",
                        isPending && "border-accent/40 opacity-80"
                      )}
                    >
                      <div className="flex items-start gap-2">
                        <span className="mt-1 h-1.5 w-1.5 shrink-0 rounded-full bg-ink-3/60" />
                        <p className="line-clamp-3 text-[13px] font-medium leading-snug text-ink">{g.title}</p>
                      </div>

                      {(g.attention || g.review_phase) && (
                        <div className="mt-2 flex flex-wrap items-center gap-1">
                          {g.review_phase && <ReviewPhaseBadge phase={g.review_phase} />}
                          {g.attention && <AttentionChip attention={g.attention} />}
                        </div>
                      )}

                      {merging[g.id] && (
                        <div className="mt-2 flex items-center gap-1 text-[11px] text-accent">
                          <Loader2 className="h-3 w-3 animate-spin" /> 已通过，合并中…
                        </div>
                      )}

                      {g.status === "review" && g.review_request && (
                        <p className="mt-2 line-clamp-2 text-[11px] leading-relaxed text-purple-300">
                          {g.review_request}
                        </p>
                      )}

                      <div className="mt-2 flex items-center gap-1.5 text-[11px] text-ink-3">
                        <span className="flex h-4 w-4 items-center justify-center rounded-full bg-surface-3 font-mono text-[9px] text-ink-2">
                          {initials(nameOf(g))}
                        </span>
                        <span className="max-w-[110px] truncate text-ink-2">{nameOf(g)}</span>
                        <span aria-hidden>·</span>
                        <span className="font-mono">{relTime(g.created_at)}</span>
                        {g.source_ref && (
                          <span className="ml-auto truncate font-mono text-[10px] text-ink-3" title={g.source_ref}>
                            {g.source_ref.split("/").pop()}
                          </span>
                        )}
                      </div>

                      {isPending && (
                        <div className="mt-2 flex items-center gap-1 text-[10px] text-accent">
                          <Loader2 className="h-3 w-3 animate-spin" /> 同步中…
                        </div>
                      )}

                      {/* 键盘 / 触屏通路：与拖拽完全等价的动作 */}
                      <div
                        data-no-drag
                        // 这些按钮在 <a> 内部：拦掉冒泡到链接的点击，否则动作执行后会顺带跳进详情页
                        onClick={(e) => {
                          e.preventDefault();
                          e.stopPropagation();
                        }}
                        className="mt-2 flex items-center gap-2 border-t border-line/70 pt-2 text-[11px] text-ink-3"
                      >
                        {g.status === "backlog" && (
                          <button
                            type="button"
                            className="hover:text-accent transition-colors"
                            onClick={() => attempt(g, "active")}
                          >
                            开始
                          </button>
                        )}
                        {g.status === "active" && (
                          <button
                            type="button"
                            className="hover:text-red-300 transition-colors"
                            onClick={() => attempt(g, "closed")}
                          >
                            取消
                          </button>
                        )}
                        {g.status === "review" && (
                          <>
                            <button
                              type="button"
                              className="hover:text-emerald-300 transition-colors"
                              onClick={() => attempt(g, "done")}
                            >
                              通过
                            </button>
                            <button
                              type="button"
                              className="hover:text-amber-300 transition-colors"
                              onClick={() => attempt(g, "active")}
                            >
                              打回
                            </button>
                          </>
                        )}
                        {(g.status === "done" || g.status === "failed" || g.status === "cancelled") && (
                          <button
                            type="button"
                            className="hover:text-accent transition-colors"
                            onClick={() => attempt(g, "active")}
                          >
                            重新打开
                          </button>
                        )}
                        <span className="ml-auto font-mono text-[10px] text-ink-3/70">
                          {STATUS_LABEL[g.status] ?? g.status}
                        </span>
                      </div>
                    </a>
                  );
                })}
              </div>
            </section>
          );
        })}
      </div>

      {/* 拖动跟随的幽灵卡 */}
      {drag && (
        <div
          className="pointer-events-none fixed z-[60]"
          style={{ left: drag.x - drag.ox, top: drag.y - drag.oy, width: drag.w }}
        >
          <div className="rounded-lg border border-accent/50 bg-surface-1 p-3 shadow-2xl shadow-black/40">
            <p className="line-clamp-2 text-[13px] font-medium leading-snug text-ink">{drag.goal.title}</p>
            <div className="mt-2 font-mono text-[10px] text-ink-3">{relTime(drag.goal.created_at)}</div>
          </div>
        </div>
      )}

      {toast && (
        <div
          className={cn(
            "fixed bottom-6 left-1/2 z-[70] max-w-[min(560px,90vw)] -translate-x-1/2 rounded-lg border px-3.5 py-2 text-[12px] shadow-xl shadow-black/30 page-enter",
            TOAST_CLS[toast.tone]
          )}
        >
          {toast.text}
        </div>
      )}

      {confirm && (
        <Dialog
          title={confirm.move.confirmTitle!}
          onClose={() => setConfirm(null)}
          footer={
            <>
              <Button variant="ghost" onClick={() => setConfirm(null)}>
                取消
              </Button>
              <Button
                variant={confirm.move.danger ? "danger" : "primary"}
                onClick={() => {
                  const { goal, col, move } = confirm;
                  setConfirm(null);
                  runMove(goal, col, move);
                }}
              >
                {confirm.move.confirmCta}
              </Button>
            </>
          }
        >
          <p className="text-sm leading-relaxed text-ink-2">{confirm.move.confirmBody}</p>
          <p className="mt-3 line-clamp-2 rounded-md border border-line bg-surface-2 px-2.5 py-2 text-[12px] text-ink">
            {confirm.goal.title}
          </p>
        </Dialog>
      )}
    </div>
  );
}