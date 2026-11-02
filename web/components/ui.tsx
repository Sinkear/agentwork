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
      {status}
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
export function PageHeader({
  title,
  action,
}: {
  title: string;
  action?: React.ReactNode;
}) {
  return (
    <div className="flex items-center justify-between mb-5">
      <h1 className="text-lg font-semibold text-ink">{title}</h1>
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
