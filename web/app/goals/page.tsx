"use client";

import { useEffect, useState, Suspense } from "react";
import Link from "next/link";
import { useRouter, useSearchParams } from "next/navigation";
import { useGoals, useAgents, useSquads, useCancelGoal, useReopenGoal, useDeleteGoal, useGoalEvents } from "@/lib/queries";
import { Badge, AttentionChip, ReviewPhaseBadge, PageBody, PageHeader, SectionCard, Segmented, Toolbar, Empty } from "@/components/ui";
import { GoalStats } from "@/components/goal-stats";
import { GoalBoard } from "@/components/goal-board";
import { GoalBatchBar, GoalSelectCheckbox, CheckIcon } from "@/components/goal-batch-bar";
import { CategoryBadge } from "@/components/category-badge";
import { cn } from "@/lib/utils";
import type { Goal, GoalStatus } from "@/lib/types";

// 六种状态全列出来：待办 → 进行中 → 待审批 → 已完成 / 失败 / 已取消。
// 顺序照任务的生命周期走，而不是照枚举定义排。
const STATUS_TABS: { label: string; value: GoalStatus | "all" }[] = [
  { label: "全部", value: "all" },
  { label: "待办", value: "backlog" },
  { label: "进行中", value: "active" },
  { label: "待审批", value: "review" },
  { label: "已完成", value: "done" },
  { label: "失败", value: "failed" },
  { label: "已取消", value: "cancelled" },
];

// 卡片顶部状态色条（与 Badge 同色系，一眼可扫）
const STATUS_BAR: Record<string, string> = {
  backlog: "bg-zinc-500/60",
  active: "bg-blue-400",
  review: "bg-purple-400",
  done: "bg-emerald-400",
  failed: "bg-red-400",
  cancelled: "bg-zinc-500/60",
};

// 负责人头像首字母
const initials = (name: string) => (name ? name.slice(0, 1).toUpperCase() : "?");

export default function GoalsPage() {
  // useSearchParams 要求 Suspense 边界，否则静态渲染时 CSR bailout 会让构建失败。
  return (
    <Suspense fallback={<div className="p-8 text-sm text-ink-3">加载中…</div>}>
      <GoalsPageInner />
    </Suspense>
  );
}

function GoalsPageInner() {
  useGoalEvents();
  const { data: goals, isLoading } = useGoals();
  const { data: agents } = useAgents();
  const { data: squads } = useSquads();
  const cancelGoal = useCancelGoal();
  const reopenGoal = useReopenGoal();
  const deleteGoalMut = useDeleteGoal();
  // 筛选状态放 URL（?status=review）：点卡片进详情再「返回列表」不丢筛选，
  // 刷新/分享链接也能还原。无效值回退「全部」。
  const router = useRouter();
  const searchParams = useSearchParams();
  const statusParam = searchParams.get("status");
  const filter = (
    STATUS_TABS.some((t) => t.value === statusParam) ? statusParam : "all"
  ) as GoalStatus | "all";
  const setFilter = (v: GoalStatus | "all") =>
    router.replace(v === "all" ? "/goals" : `/goals?status=${v}`, { scroll: false });

  // 视图：看板（拖拽为主）/ 列表（批量与筛选为主）。选择记在 localStorage，
  // 刷新后保持；默认看板——任务面板的主入口。
  const [view, setView] = useState<"board" | "list">("board");
  useEffect(() => {
    const saved = window.localStorage.getItem("agentwork-goals-view");
    if (saved === "list" || saved === "board") setView(saved);
  }, []);
  const pickView = (v: "board" | "list") => {
    setView(v);
    window.localStorage.setItem("agentwork-goals-view", v);
  };

  const agentName = (aid: string) => agents?.find((a) => a.id === aid)?.name ?? aid;
  const squadName = (sid: string) => squads?.find((s) => s.id === sid)?.name ?? sid;

  const filtered = goals?.filter((g) => filter === "all" || g.status === filter) ?? [];

  // 多选批量操作（决策 7-6）：Set<goal id>，跨 tab 切换持久（Gmail 式），
  // 底部浮动批量条对选中项批量执行。batchBusy = 批量执行中（格子禁点）。
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [batchBusyId, setBatchBusyId] = useState<string | null>(null);
  const batchBusy = batchBusyId !== null;
  const toggleSelect = (id: string) => {
    if (batchBusy) return;
    setSelected((prev) => {
      const next = new Set(prev);
      if (next.has(id)) next.delete(id);
      else next.add(id);
      return next;
    });
  };
  const allFilteredSelected = filtered.length > 0 && filtered.every((g) => selected.has(g.id));
  const selectAllFiltered = () => {
    if (batchBusy) return;
    setSelected((prev) => {
      const next = new Set(prev);
      if (allFilteredSelected) {
        for (const g of filtered) next.delete(g.id);
      } else {
        for (const g of filtered) next.add(g.id);
      }
      return next;
    });
  };

  return (
    <PageBody full={view === "board"}>
      <PageHeader
        title="任务"
        desc="拖动卡片改状态，或者切到列表批量处理。"
        action={
          <div className="flex items-center gap-2">
            <Segmented
              value={view}
              onChange={pickView}
              options={[
                { value: "board", label: "看板" },
                { value: "list", label: "列表" },
              ]}
            />
            <Link
              href="/goals/new"
              className="inline-flex items-center justify-center rounded-md bg-accent px-3 py-1.5 text-sm font-medium text-accent-ink transition-colors duration-150 hover:bg-accent-dim"
            >
              + 新建
            </Link>
          </div>
        }
      />

      {/* Stats cards */}
      <GoalStats goals={goals} loading={isLoading} />

      {view === "board" ? (
        isLoading ? (
          <div className="text-sm text-ink-3 py-16 text-center">加载中…</div>
        ) : (
          <GoalBoard goals={goals ?? []} agents={agents ?? []} squads={squads ?? []} />
        )
      ) : (
        <>

      {/* 筛选：状态 tab + 全选（作用于当前筛选，配合多选批量条） */}
      <Toolbar className="mb-3 justify-between">
        <Segmented
          value={filter}
          onChange={setFilter}
          options={STATUS_TABS.map((t) => ({
            value: t.value,
            label: `${t.label}${
              (t.value === "all" ? goals?.length ?? 0 : goals?.filter((g) => g.status === t.value).length ?? 0) > 0
                ? ` ${t.value === "all" ? goals?.length ?? 0 : goals?.filter((g) => g.status === t.value).length ?? 0}`
                : ""
            }`,
          }))}
        />
        {filtered.length > 0 && (
          <button
            type="button"
            disabled={batchBusy}
            onClick={selectAllFiltered}
            className="flex items-center gap-2 px-2.5 py-1.5 rounded-md text-xs font-medium text-ink-3 hover:text-accent hover:bg-accent/10 transition-colors disabled:opacity-50 disabled:hover:text-ink-3 disabled:hover:bg-transparent"
          >
            <span
              className={cn(
                "h-4 w-4 rounded border flex items-center justify-center transition-colors",
                allFilteredSelected ? "bg-accent border-accent text-accent-ink" : "bg-surface-1 border-line-strong"
              )}
            >
              {allFilteredSelected && <CheckIcon className="h-2.5 w-2.5" />}
            </span>
            {allFilteredSelected ? "取消全选" : "全选"}
          </button>
        )}
      </Toolbar>

      {/* Goals — 卡片：状态色条 + 谁在干 + 元信息（不是表格的伪卡片） */}
      <SectionCard title="任务列表" count={filtered.length}>
      {isLoading ? (
        <div className="text-sm text-ink-3 py-16 text-center">加载中…</div>
      ) : filtered.length === 0 ? (
        <Empty>
          {filter === "all" ? "暂无 Goal，点击「+ 新建」创建第一个。" : "没有符合条件的 Goal。"}
        </Empty>
      ) : (
        <div className={cn("grid gap-3 md:grid-cols-2", batchBusy && "pointer-events-none")}>
          {filtered.map((g: Goal) => (
            <Link
              key={g.id}
              href={`/goals/${g.id}${filter === "all" ? "" : `?status=${filter}`}`}
              className={cn(
                "group relative flex bg-surface-1 rounded-lg border border-line overflow-hidden hover:border-accent/40 transition-colors duration-150",
                selected.has(g.id) && "border-accent/50 bg-accent/10",
                batchBusyId === g.id && "ring-2 ring-accent/60 border-accent/50",
                batchBusy && batchBusyId !== g.id && "opacity-60"
              )}
            >
              {/* 状态在左侧：竖向渐变条（一眼可扫） */}
              <div className={`w-1.5 shrink-0 ${STATUS_BAR[g.status] ?? "bg-zinc-500/60"}`} />
              <div className="p-4 flex-1 min-w-0">
                <div className="flex items-start justify-between gap-3">
                  <div className="flex items-start gap-2.5 min-w-0">
                    <GoalSelectCheckbox
                      checked={selected.has(g.id)}
                      disabled={batchBusy}
                      onToggle={() => toggleSelect(g.id)}
                    />
                    <h3 className="font-medium text-ink group-hover:text-accent transition-colors min-w-0">
                      {g.title}
                    </h3>
                  </div>
                  <div className="flex items-center gap-1.5 shrink-0">
                    <CategoryBadge category={g.category} />
                    <AttentionChip attention={g.attention} />
                    {g.status === "review" ? (
                      <ReviewPhaseBadge phase={g.review_phase} className="shrink-0" />
                    ) : (
                      <Badge status={g.status} className="shrink-0" />
                    )}
                  </div>
                </div>
                {g.status === "review" && g.review_request && (
                  <p className="mt-1.5 text-xs text-purple-300 line-clamp-1">{g.review_request}</p>
                )}
                {/* 元信息：谁在干 / 负责人 · 时间 */}
                <div className="mt-3 flex items-center gap-x-4 gap-y-1 flex-wrap text-xs text-ink-3">
                  {g.current_agent_id ? (
                    <span className="flex items-center gap-1.5 font-medium text-emerald-400">
                      <span className="h-1.5 w-1.5 rounded-full bg-emerald-500 animate-pulse" />
                      {agentName(g.current_agent_id)} 正在执行
                    </span>
                  ) : (
                    <span className="flex items-center gap-1.5">
                      <span className="inline-block h-4 w-4 rounded-full bg-accent/15 text-accent text-[10px] leading-4 text-center font-medium">
                        {initials(g.assignee_type === "squad" ? (squadName(g.assignee_id) || "?") : (agentName(g.assignee_id) || "?"))}
                      </span>
                      {g.assignee_type === "squad"
                        ? (squadName(g.assignee_id) || g.assignee_id)
                        : g.assignee_id
                          ? (agentName(g.assignee_id) || g.assignee_id)
                          : "未分配"}
                    </span>
                  )}
                  <span>
                    {g.created_at ? new Date(g.created_at).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" }) : ""}
                  </span>
                  {g.status === "done" && <span className="text-emerald-400 font-medium">✓ 已完成</span>}
                  {g.status === "failed" && <span className="text-red-400 font-medium">✕ 失败</span>}
                </div>
                {/* 行内操作：取消 / 重开 / 删除。卡片整体是 Link，
                    按钮必须 preventDefault + stopPropagation 阻止跳转。
                    cancel：非终态（backlog/active/review）；reopen：终态
                    （done/failed/cancelled，人类接管路径）；delete：任意状态
                    （级联删 run/评论/子目标，confirm 后执行）。 */}
                <div className="mt-2 flex items-center gap-3 justify-end">
                  {(g.status === "active" || g.status === "backlog" || g.status === "review") && (
                    <button
                      className="text-xs text-ink-3 hover:text-amber-400 transition-colors disabled:opacity-50"
                      disabled={cancelGoal.isPending}
                      onClick={(e) => { e.preventDefault(); e.stopPropagation(); cancelGoal.mutate(g.id); }}
                    >
                      取消
                    </button>
                  )}
                  {(g.status === "done" || g.status === "failed" || g.status === "cancelled") && (
                    <button
                      className="text-xs text-ink-3 hover:text-accent transition-colors disabled:opacity-50"
                      disabled={reopenGoal.isPending}
                      onClick={(e) => { e.preventDefault(); e.stopPropagation(); reopenGoal.mutate(g.id); }}
                    >
                      重开
                    </button>
                  )}
                  <button
                    className="text-xs text-ink-3 hover:text-red-400 transition-colors disabled:opacity-50"
                    disabled={deleteGoalMut.isPending}
                    onClick={(e) => {
                      e.preventDefault();
                      e.stopPropagation();
                      if (confirm(`删除 Goal「${g.title}」？级联删除其全部 run/评论/子目标，不可恢复。`)) deleteGoalMut.mutate(g.id);
                    }}
                  >
                    删除
                  </button>
                </div>
              </div>
            </Link>
          ))}
        </div>
      )}
      </SectionCard>
        </>
      )}

      {(cancelGoal.isError || reopenGoal.isError || deleteGoalMut.isError) && (
        <p className="text-sm text-red-400 mt-2">
          {String(cancelGoal.error ?? reopenGoal.error ?? deleteGoalMut.error)}
        </p>
      )}

      {/* 多选批量操作条（决策 7-6）：选中项 > 0 时出现，浮动底部 */}
      <GoalBatchBar
        goals={goals ?? []}
        selected={selected}
        onClear={() => setSelected(new Set())}
        onSelectionChange={setSelected}
        onBusyChange={setBatchBusyId}
      />
    </PageBody>
  );
}
