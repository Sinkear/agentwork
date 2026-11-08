"use client";

import { useEffect, useRef } from "react";
import { Sprout } from "lucide-react";
import { cn } from "@/lib/utils";

// ── Button ──
// 扁平化：无渐变、无彩色阴影、无 hover 上浮。层级靠亮度差表达，
// 动效只做背景色的 150ms 过渡。
type ButtonVariant = "primary" | "ghost" | "danger" | "outline";

const BTN_VARIANTS: Record<ButtonVariant, string> = {
  primary: "bg-accent text-accent-ink hover:bg-accent-dim",
  outline:
    "border border-line-strong bg-transparent text-ink-2 hover:border-accent/50 hover:text-ink hover:bg-surface-2",
  ghost: "text-ink-2 hover:bg-surface-2 hover:text-ink",
  danger: "bg-red-500/90 text-white hover:bg-red-500",
};

export function Button({
  variant = "primary",
  className,
  ...props
}: React.ButtonHTMLAttributes<HTMLButtonElement> & { variant?: ButtonVariant }) {
  return (
    <button
      className={cn(
        "inline-flex items-center justify-center px-3 py-1.5 text-sm font-medium rounded-md transition-colors duration-150 disabled:opacity-50 disabled:cursor-not-allowed",
        BTN_VARIANTS[variant],
        className
      )}
      {...props}
    />
  );
}

// ── Badge ──
// 深色底上的状态色：低饱和填充 + 亮色文字（-500/10 填充 + -300 文字）。
// 同一状态一个主色，圆点给"活着"的提示。
const STATUS_COLORS: Record<string, string> = {
  // Goal statuses
  backlog: "bg-zinc-500/10 text-ink-3 dot-zinc",
  active: "bg-blue-500/10 text-blue-300 dot-blue",
  review: "bg-purple-500/10 text-purple-300 dot-purple",
  done: "bg-emerald-500/10 text-emerald-300 dot-emerald",
  failed: "bg-red-500/10 text-red-300 dot-red",
  cancelled: "bg-zinc-500/10 text-ink-3 dot-zinc",
  // Run statuses
  queued: "bg-zinc-500/10 text-ink-3 dot-zinc",
  running: "bg-green-500/10 text-green-300 dot-green",
  completed: "bg-emerald-500/10 text-emerald-300 dot-emerald",
  // RSI 训练分析会话状态（§5.2 状态机）：与 goal 状态共用一套色槽位，
  // 列表/详情页直接复用 Badge，不再单独维护一份颜色表。
  waiting: "bg-zinc-500/10 text-ink-3 dot-zinc",
  analyzing: "bg-blue-500/10 text-blue-300 dot-blue",
  ready: "bg-emerald-500/10 text-emerald-300 dot-emerald",
  skipped: "bg-zinc-500/10 text-ink-3 dot-zinc",
};

// 状态的中文显示名：UI 里不再直接暴露 backlog/active 这类枚举值。
export const STATUS_LABEL: Record<string, string> = {
  backlog: "待办",
  active: "进行中",
  review: "待审批",
  failed: "失败",
  cancelled: "已取消",
  done: "已完成",
  completed: "已完成",
  queued: "排队中",
  running: "执行中",
  waiting: "等待执行",
  analyzing: "分析中",
  ready: "已分析",
  skipped: "已跳过",
  online: "在线",
  offline: "离线",
  paused: "已暂停",
};

const STATUS_DOTS: Record<string, string> = {
  "dot-zinc": "bg-zinc-400",
  "dot-blue": "bg-blue-400",
  "dot-amber": "bg-amber-400",
  "dot-purple": "bg-purple-400",
  "dot-emerald": "bg-emerald-400",
  "dot-red": "bg-red-400",
  "dot-green": "bg-green-400",
};

// review_phase 的显示层（决策 6-19 延伸）：park 后有 reviewer 的 goal 是三阶段
// ——待审查（run 已入队、未开跑）→ 审查中（reviewer 在跑）→ 待审批（意见齐了
// 才轮到人）。无 reviewer 直接到待审批。
const REVIEW_PHASES: Record<string, { label: string; cls: string; dot: string }> = {
  awaiting_review: { label: "待审查", cls: "bg-sky-500/10 text-sky-300", dot: "bg-sky-400" },
  reviewing: { label: "审查中", cls: "bg-amber-500/10 text-amber-300", dot: "bg-amber-400" },
  awaiting_approval: { label: "待审批", cls: "bg-purple-500/10 text-purple-300", dot: "bg-purple-400" },
};

export function ReviewPhaseBadge({ phase, className }: { phase?: string; className?: string }) {
  const meta = REVIEW_PHASES[phase ?? "awaiting_approval"] ?? REVIEW_PHASES.awaiting_approval;
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 px-2 py-0.5 text-xs font-medium rounded-md",
        meta.cls,
        className
      )}
    >
      <span className={cn("h-1.5 w-1.5 rounded-full", meta.dot)} />
      {meta.label}
    </span>
  );
}

export function Badge({ status, className }: { status: string; className?: string }) {
  const colors = STATUS_COLORS[status] ?? "bg-zinc-500/10 text-ink-3 dot-zinc";
  const dot = STATUS_DOTS[colors.split(" ").find((c) => c.startsWith("dot-")) ?? "dot-zinc"] ?? "bg-zinc-400";
  return (
    <span
      className={cn(
        "inline-flex items-center gap-1.5 px-2 py-0.5 text-xs font-medium rounded-md",
        colors.replace(/ dot-\S+/, ""),
        className
      )}
    >
      <span className={cn("h-1.5 w-1.5 rounded-full", dot, status === "running" && "animate-pulse")} />
      {STATUS_LABEL[status] ?? status}
    </span>
  );
}

// ── Attention chip ──
// The v2 OwnerAttention (goal.attention, derived by the Coordinator): what
// the goal's owner is being woken for. '' renders nothing.
const ATTENTION_LABELS: Record<string, { label: string; cls: string }> = {
  integration: { label: "待集成变更", cls: "bg-blue-500/10 text-blue-300 border-blue-500/30" },
  wrapup: { label: "待收尾确认", cls: "bg-teal-500/10 text-teal-300 border-teal-500/30" },
  recovery: { label: "需处理失败", cls: "bg-amber-500/10 text-amber-300 border-amber-500/30" },
  user_action: { label: "待人工决策", cls: "bg-red-500/10 text-red-300 border-red-500/30" },
};

export function AttentionChip({ attention, className }: { attention: string; className?: string }) {
  if (!attention) return null;
  const chips = attention
    .split(",")
    .map((a) => ATTENTION_LABELS[a])
    .filter(Boolean);
  if (chips.length === 0) return null;
  return (
    <>
      {chips.map((c) => (
        <span
          key={c.label}
          className={cn(
            "inline-flex items-center gap-1 px-1.5 py-0.5 text-[11px] font-medium rounded-md border",
            c.cls,
            className
          )}
        >
          <span className="h-1.5 w-1.5 rounded-full bg-current animate-pulse" />
          {c.label}
        </span>
      ))}
    </>
  );
}

// ── Form primitives ──
export function Field({
  label,
  hint,
  children,
}: {
  label: string;
  hint?: string;
  children: React.ReactNode;
}) {
  return (
    <label className="block space-y-1">
      <span className="text-xs font-medium text-ink-2">{label}</span>
      {children}
      {hint && <span className="block text-xs text-ink-3">{hint}</span>}
    </label>
  );
}

export const inputCls =
  "w-full px-3 py-2 border border-line-strong rounded-md text-sm bg-surface-2 text-ink placeholder:text-ink-3 focus:border-accent-dim focus:ring-2 focus:ring-accent/15 outline-none transition-colors";

// ── Dialog ──
export function Dialog({
  title,
  onClose,
  children,
  footer,
  wide,
}: {
  title: string;
  onClose: () => void;
  children: React.ReactNode;
  footer: React.ReactNode;
  wide?: boolean; // 内容宽的表单（结构化编辑器）用 max-w-lg
}) {
  // Esc to close.
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [onClose]);

  // Backdrop-click-to-close requires BOTH press and release on the backdrop.
  // A text-selection drag that starts inside the panel and releases over the
  // backdrop fires a click on their common ancestor (the backdrop) — with a
  // naive onClick that would close the dialog mid-selection ("selecting all
  // closes the dialog").
  const pressedOnBackdrop = useRef(false);

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-black/60 p-4"
      onMouseDown={(e) => {
        pressedOnBackdrop.current = e.target === e.currentTarget;
      }}
      onClick={() => {
        if (pressedOnBackdrop.current) onClose();
      }}
    >
      <div
        className={`w-full ${wide ? "max-w-lg" : "max-w-md"} bg-surface-1 rounded-xl shadow-2xl shadow-black/40 border border-line flex flex-col max-h-[90vh] page-enter`}
        onClick={(e) => e.stopPropagation()}
      >
        <div className="flex items-center justify-between px-5 py-3.5 border-b border-line">
          <h2 className="text-base font-semibold text-ink">{title}</h2>
          <button
            onClick={onClose}
            className="h-7 w-7 -mr-1 flex items-center justify-center rounded-md text-ink-3 hover:text-ink hover:bg-surface-2 text-lg leading-none transition-colors"
            aria-label="关闭"
          >
            ×
          </button>
        </div>
        <div className="p-5 space-y-4 overflow-y-auto">{children}</div>
        <div className="flex justify-end gap-2 px-5 py-3.5 border-t border-line">
          {footer}
        </div>
      </div>
    </div>
  );
}

// ── Confirm Dialog ──
export function ConfirmDialog({
  title,
  message,
  onConfirm,
  onClose,
  loading,
}: {
  title: string;
  message: string;
  onConfirm: () => void;
  onClose: () => void;
  loading?: boolean;
}) {
  return (
    <Dialog
      title={title}
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button variant="danger" onClick={onConfirm} disabled={loading}>
            {loading ? "…" : "确认删除"}
          </Button>
        </>
      }
    >
      <p className="text-sm text-ink-2">{message}</p>
    </Dialog>
  );
}

// ── Page header ──
// 标题即版面锚点：大半号、收紧字距，可选一行副标题。
export function PageHeader({
  title,
  desc,
  action,
}: {
  title: string;
  desc?: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="flex items-end justify-between mb-6">
      <div>
        <h1 className="text-2xl font-semibold tracking-tight text-ink">{title}</h1>
        {desc && <p className="mt-1 text-sm text-ink-3">{desc}</p>}
      </div>
      {action}
    </div>
  );
}

// ── Empty state ──
// 图标用 lucide SVG（emoji 当图标是反模式），外面套一个低调的圆角底。
export function Empty({ children, icon }: { children: React.ReactNode; icon?: React.ReactNode }) {
  return (
    <div className="flex flex-col items-center justify-center py-14 text-center">
      <span className="mb-3 flex h-11 w-11 items-center justify-center rounded-lg border border-line bg-surface-2 text-ink-3">
        {icon ?? <Sprout className="h-5 w-5" />}
      </span>
      <p className="text-sm text-ink-3 max-w-sm leading-relaxed">{children}</p>
    </div>
  );
}

// ── Skeleton ──
export function Skeleton({ className }: { className?: string }) {
  return (
    <div
      className={cn(
        "animate-pulse rounded-md bg-surface-2",
        className
      )}
    />
  );
}

// ── Page body ──
// 全站唯一的版面容器：统一最大宽度、内边距与垂直节奏。
// 宽度对全站所有页面（含看板、对话）都是同一个 1360，切换视图/切页面时内容
// 不会横向跳。`full` 只表达"这一页要占满高度"（看板/对话），不再影响宽度。
export function PageBody({
  children,
  className,
  full,
}: {
  children: React.ReactNode;
  className?: string;
  full?: boolean; // 看板这类需要撑满高度/宽度的页面
}) {
  return (
    <div
      className={cn(
        "mx-auto w-full max-w-[1360px] px-8 py-7 space-y-6 page-enter",
        full && "h-full flex flex-col",
        className
      )}
    >
      {children}
    </div>
  );
}

// ── Stat strip ──
// 列表页顶部的概览条：一行 2–4 个数字，等宽字体、不用彩色卡片、不用图标堆叠。
// 作用是把“这一页现在什么状况”压成一眼可读的一行，而不是让页面只剩一个列表。
const STAT_TONE: Record<string, string> = {
  default: "text-ink",
  ok: "text-emerald-400",
  warn: "text-amber-400",
  err: "text-red-400",
  info: "text-sky-400",
  muted: "text-ink-3",
};

export function StatStrip({
  items,
  className,
}: {
  items: { label: string; value: React.ReactNode; hint?: string; tone?: keyof typeof STAT_TONE }[];
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-wrap overflow-hidden rounded-lg border border-line bg-surface-1",
        className
      )}
    >
      {items.map((it) => (
        <div
          key={it.label}
          className="min-w-[150px] flex-1 border-l border-line px-4 py-3 first:border-l-0"
        >
          <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3">
            {it.label}
          </div>
          <div className={cn("mt-1.5 font-mono text-xl tabular-nums leading-none", STAT_TONE[it.tone ?? "default"])}>
            {it.value}
          </div>
          {it.hint && <div className="mt-1.5 truncate text-[11px] text-ink-3">{it.hint}</div>}
        </div>
      ))}
    </div>
  );
}

// ── Section card ──
// 列表主体：标题行（计数 + 右侧操作）+ 内容。列表行用 divide-line 分隔，
// 不再给每张卡加阴影——层级靠底色和描边。
export function SectionCard({
  title,
  count,
  desc,
  action,
  children,
  className,
  bodyClassName,
}: {
  title?: string;
  count?: number;
  desc?: string;
  action?: React.ReactNode;
  children: React.ReactNode;
  className?: string;
  bodyClassName?: string;
}) {
  return (
    <section className={cn("overflow-hidden rounded-lg border border-line bg-surface-1", className)}>
      {(title || action) && (
        <header className="flex items-center gap-2.5 border-b border-line px-4 py-2.5">
          {title && <h2 className="text-sm font-medium text-ink">{title}</h2>}
          {typeof count === "number" && (
            <span className="font-mono text-[11px] text-ink-3">{count}</span>
          )}
          {desc && <span className="truncate text-xs text-ink-3">{desc}</span>}
          {action && <div className="ml-auto flex items-center gap-2">{action}</div>}
        </header>
      )}
      <div className={cn(bodyClassName)}>{children}</div>
    </section>
  );
}

// ── Toolbar ──
// 页面内的筛选/搜索行：左侧控件，右侧计数或视图切换。
export function Toolbar({ children, className }: { children: React.ReactNode; className?: string }) {
  return (
    <div className={cn("flex flex-wrap items-center gap-2", className)}>{children}</div>
  );
}

// 分段控件（视图切换、范围切换）：一处定义，全站同款。
export function Segmented<T extends string>({
  value,
  onChange,
  options,
}: {
  value: T;
  onChange: (v: T) => void;
  options: { value: T; label: string }[];
}) {
  return (
    <div className="flex items-center gap-0.5 rounded-md border border-line bg-surface-1 p-0.5">
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          onClick={() => onChange(o.value)}
          className={cn(
            "rounded px-2.5 py-1 text-xs font-medium transition-colors duration-150",
            value === o.value ? "bg-surface-3 text-ink" : "text-ink-3 hover:text-ink"
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}
