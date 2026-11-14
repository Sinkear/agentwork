"use client";

import { useState } from "react";
import { Button, Dialog, Field, inputCls } from "@/components/ui";
import { useCreateGoal } from "@/lib/queries";
import type { CreateGoalBody } from "@/lib/api";
import type { ExecutionMode } from "@/lib/types";
import { cn } from "@/lib/utils";

// 新建 Goal 的表单（对话框形态）。
// 单独抽成组件是因为两个入口都用它：任务页的「手动新建」，以及 /goals/new 里
// 管家没能创建成功时的兜底表单——兜底路径不能是死路。
export function NewGoalForm({
  agents,
  squads,
  domains,
  onClose,
  onCreated,
}: {
  agents?: { id: string; name: string }[];
  squads?: { id: string; name: string }[];
  domains?: { id: string; name: string }[];
  onClose: () => void;
  onCreated?: () => void;
}) {
  const createGoal = useCreateGoal();
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [domainId, setDomainId] = useState("");
  const [domainErr, setDomainErr] = useState("");
  const [category, setCategory] = useState<"hitl" | "hootl">("hitl");
  const [assigneeType, setAssigneeType] = useState("");
  const [assigneeId, setAssigneeId] = useState("");
  const [assigneeErr, setAssigneeErr] = useState("");
  // 运行模式与训练高级项（§5.6）：默认推理，高级项只在训练时展开；
  // 默认值隐藏是因为 90% 的场景不需要动它们。
  const [executionMode, setExecutionMode] = useState<ExecutionMode>("infer");
  const [contentCapture, setContentCapture] = useState<"redacted" | "none" | "full">("redacted");
  const [budgetUsd, setBudgetUsd] = useState("2");
  const [sampleRatePct, setSampleRatePct] = useState("100");

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
const body: CreateGoalBody = { title, category };
    if (description) body.description = description;
    if (assigneeType && assigneeId) {
      body.assignee_type = assigneeType;
      body.assignee_id = assigneeId;
      body.status = "active";
    }
    // A chosen assignee TYPE without a chosen agent/squad would silently
    // drop the assignee and the backend rejects it as "agent goal without
    // assignee_id" — fail loudly here instead.
    if (assigneeType && !assigneeId) {
      setAssigneeErr(
        assigneeType === "agent"
          ? "请选择一个 Agent——或先到「Agent」页创建 runtime 和 agent"
          : "请选择一个 Squad——或先到「Squad」页创建小队"
      );
      return;
    }
    // v2: agent/squad-executed goals must belong to a domain (DESIGN.md §2).
    if (assigneeType === "agent" || assigneeType === "squad") {
      if (!domainId) {
        setDomainErr("请选择所属项目——agent 执行的 Goal 必须挂在一个项目上（项目提供 worktree 与验收策略）");
        return;
      }
      body.domain_id = domainId;
    }
    // 运行模式随 goal 一起落库：infer 只记基础指标，train 才建分析会话。
    // （后端缺省也是 infer，这里仍显式传，便于抓包核对。）
    body.execution_mode = executionMode;
    if (executionMode === "train") {
      body.content_capture = contentCapture;
      // 数值字段做一次夹取：空输入/非法值回落到默认，不让 NaN 写进库。
      // 后端约束：<=0 回落 2、>100 报 400，这里先夹到合法区间。
      const budget = Number(budgetUsd);
      body.budget_usd = Number.isFinite(budget) && budget > 0 && budget <= 100 ? budget : 2;
      const pct = Number(sampleRatePct);
      body.sample_rate = Number.isFinite(pct) && pct > 0 && pct <= 100 ? pct / 100 : 1;
    }
    createGoal.mutate(body, {
      onSuccess: () => (onCreated ? onCreated() : onClose()),
    });
  };

  return (
    <Dialog
      title="新建任务"
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button type="submit" form="new-goal-form" disabled={createGoal.isPending}>
            {createGoal.isPending ? "创建中…" : "创建"}
          </Button>
        </>
      }
    >
      <form id="new-goal-form" onSubmit={handleSubmit} className="space-y-4">
        <Field label="标题" hint="必填">
          <input value={title} onChange={(e) => setTitle(e.target.value)} className={inputCls} required placeholder="一句话说清要做什么…" />
        </Field>
        <Field label="描述">
          <textarea value={description} onChange={(e) => setDescription(e.target.value)} className={inputCls} rows={3} placeholder="可选：验收标准、约束条件…" />
        </Field>
        <Field label="所属项目" hint="agent/squad 执行的任务必填">
          <select value={domainId} onChange={(e) => setDomainId(e.target.value)} className={inputCls}>
            <option value="">选择…</option>
            {domains?.map((d) => (
              <option key={d.id} value={d.id}>
                {d.name}
              </option>
            ))}
          </select>
        </Field>
        {/* 验收分类：HITL 结束前走人工审批门禁；HOOTL 跳过复检，完成即结束。 */}
        <Field
          label="验收分类"
          hint={
            category === "hootl"
              ? "无需人工复检，任务完成即结束"
              : "结束前进入 review 阶段等待人工审批"
          }
        >
          <select
            value={category}
            onChange={(e) => setCategory(e.target.value === "hootl" ? "hootl" : "hitl")}
            className={inputCls}
          >
            <option value="hitl">HITL · 人工验收</option>
            <option value="hootl">HOOTL · 免复检</option>
          </select>
        </Field>
        <Field label="负责人类型">
          <select
            value={assigneeType}
            onChange={(e) => {
              setAssigneeType(e.target.value);
              setAssigneeId("");
            }}
            className={inputCls}
          >
            <option value="">无（先放进待办）</option>
            <option value="agent">Agent</option>
            <option value="squad">Squad</option>
          </select>
        </Field>
        {assigneeType === "agent" && (
          <Field label="选择 Agent">
            <select value={assigneeId} onChange={(e) => setAssigneeId(e.target.value)} className={inputCls} required>
              <option value="">选择…</option>
              {agents?.map((a) => (
                <option key={a.id} value={a.id}>
                  {a.name}
                </option>
              ))}
            </select>
          </Field>
        )}
        {assigneeType === "squad" && (
          <Field label="选择 Squad">
            <select value={assigneeId} onChange={(e) => setAssigneeId(e.target.value)} className={inputCls} required>
              <option value="">选择…</option>
              {squads?.map((s) => (
                <option key={s.id} value={s.id}>
                  {s.name}
                </option>
              ))}
            </select>
          </Field>
        )}
        {/* 运行模式：两栏单选卡片（不是下拉——两个选项各带一句解释，选错代价大）。 */}
        <div className="space-y-1">
          <span className="block text-xs font-medium text-ink-2">运行模式</span>
          <div className="grid grid-cols-2 gap-2">
            {(
              [
                { value: "infer", label: "推理", desc: "只记录基础指标（耗时/状态）" },
                { value: "train", label: "训练", desc: "记录编排与耗时元数据，完成后自动分析" },
              ] as const
            ).map((m) => (
              <label
                key={m.value}
                className={cn(
                  "flex cursor-pointer flex-col gap-1 rounded-md border px-3 py-2 transition-colors duration-150",
                  executionMode === m.value
                    ? "border-accent/50 bg-accent/10"
                    : "border-line bg-surface-2 hover:border-line-strong"
                )}
              >
                <span className="flex items-center gap-2 text-sm font-medium text-ink">
                  <input
                    type="radio"
                    name="execution_mode"
                    value={m.value}
                    checked={executionMode === m.value}
                    onChange={() => setExecutionMode(m.value)}
                    className="accent-accent"
                  />
                  {m.label}
                </span>
                <span className="pl-5 text-[11px] leading-relaxed text-ink-3">{m.desc}</span>
              </label>
            ))}
          </div>
          <span className="block text-xs text-ink-3">
            训练模式记录执行编排与耗时元数据，用于“哪块慢”的分析；不训练模型权重，也不保存对话正文。
          </span>
        </div>

        {executionMode === "train" && (
          <div className="space-y-3 rounded-md border border-line bg-surface-2/40 p-3">
            <div className="text-xs font-medium text-ink-2">训练高级项</div>
            <Field label="内容采集" hint="预留字段：当前仅保存该选项，尚未读取工具正文或参数。">
              <select
                value={contentCapture}
                onChange={(e) => setContentCapture(e.target.value as "redacted" | "none" | "full")}
                className={inputCls}
              >
                <option value="redacted">脱敏（推荐）</option>
                <option value="none">仅结构（不读正文）</option>
                <option value="full">完整（含正文）</option>
              </select>
            </Field>
            <div className="grid grid-cols-2 gap-3">
              <Field label="分析预算（美元）" hint="预留字段：当前仅保存上限，尚未触发预算降级">
                <input
                  type="number"
                  min={0}
                  step={0.5}
                  value={budgetUsd}
                  onChange={(e) => setBudgetUsd(e.target.value)}
                  className={inputCls}
                />
              </Field>
              <Field label="采样率（%）" hint="低于 100% 时随机跳过训练分析；当前不按任务成败豁免">
                <input
                  type="number"
                  min={1}
                  max={100}
                  step={1}
                  value={sampleRatePct}
                  onChange={(e) => setSampleRatePct(e.target.value)}
                  className={inputCls}
                />
              </Field>
            </div>
          </div>
        )}

        {(createGoal.isError || domainErr || assigneeErr) && (
          <p className="text-sm text-red-400">{domainErr || assigneeErr || String(createGoal.error)}</p>
        )}
      </form>
    </Dialog>
  );
}