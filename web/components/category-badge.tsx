"use client";

// Feature B：任务验收分类徽章（HITL 人工验收 / HOOTL 免复检）。
// 单独一个小组件而不是塞进 ui.tsx：只有 goal 与 schedule 两个页面需要它。
const CATEGORY_META: Record<
  string,
  { label: string; hint: string; cls: string; dot: string }
> = {
  hitl: {
    label: "HITL 人工验收",
    hint: "结束前进入 review 阶段，等待人工审批",
    cls: "border-sky-500/30 bg-sky-500/10 text-sky-300",
    dot: "bg-sky-400",
  },
  hootl: {
    label: "HOOTL 免复检",
    hint: "无需人工复检，任务完成即结束",
    cls: "border-violet-500/30 bg-violet-500/10 text-violet-300",
    dot: "bg-violet-400",
  },
};

export function CategoryBadge({
  category,
  className,
}: {
  category?: string;
  className?: string;
}) {
  const meta = CATEGORY_META[category === "hootl" ? "hootl" : "hitl"];
  return (
    <span
      title={meta.hint}
      className={[
        "inline-flex shrink-0 items-center gap-1.5 rounded-md border px-1.5 py-0.5 text-[11px] font-medium",
        meta.cls,
        className ?? "",
      ].join(" ")}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${meta.dot}`} />
      {meta.label}
    </span>
  );
}

export const CATEGORY_LABEL: Record<string, string> = {
  hitl: "HITL 人工验收",
  hootl: "HOOTL 免复检",
};