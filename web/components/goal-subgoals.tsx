"use client";

import { useState } from "react";
import { useSubGoals, useCreateSubGoal, useAgents, useSubGoalVerifications } from "@/lib/queries";
import { useWSEvent } from "@/lib/ws";
import { Button, Dialog, Field, inputCls, Empty } from "@/components/ui";
import type { SubGoal, VerificationResult } from "@/lib/types";

const SUBGOAL_STATUS_COLORS: Record<string, string> = {
  running: "bg-blue-500/10 text-blue-300 dot-blue",
  verifying: "bg-amber-500/10 text-amber-300 dot-amber",
  verified: "bg-emerald-500/10 text-emerald-300 dot-emerald",
  rejected: "bg-orange-500/10 text-orange-300 dot-amber",
  cancelled: "bg-surface-2 text-ink-3 dot-zinc",
  failed: "bg-red-500/10 text-red-300 dot-red",
  done: "bg-emerald-500/10 text-emerald-300 dot-emerald",
};

const SUBGOAL_STATUS_LABEL: Record<string, string> = {
  running: "执行中",
  verifying: "待验证",
  verified: "已验证",
  rejected: "已驳回",
  cancelled: "已取消",
  failed: "失败",
  pending: "待开始",
};

// SubGoalStatus renders the sub-goal's lightweight lifecycle state.
function SubGoalStatus({ status }: { status: string }) {
  const colors = SUBGOAL_STATUS_COLORS[status] ?? "bg-surface-2 text-ink-2 dot-zinc";
  return (
    <span
      className={`inline-flex items-center gap-1.5 px-2 py-0.5 text-[11px] font-medium rounded-full ${colors.replace(/ dot-\S+/, "")}`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${colors.split(" ").find((c) => c.startsWith("dot-")) ?? "bg-zinc-400"}`} />
      {SUBGOAL_STATUS_LABEL[status] ?? status}
    </span>
  );
}

// Verifications renders a sub-goal's verification rounds (v2 决策 6-5) —
// machine checks or an agent verifier's structured verdict, newest first.
function Verifications({ goalId, subGoalId }: { goalId: string; subGoalId: string }) {
  const { data: verifications, isLoading } = useSubGoalVerifications(goalId, subGoalId);
  if (isLoading) return <p className="text-xs text-ink-3 px-3 pb-3">验证记录加载中…</p>;
  if (!verifications || verifications.length === 0) {
    return <p className="text-xs text-ink-3 px-3 pb-3">暂无验证记录。</p>;
  }
  return (
    <div className="px-3 pb-3 pt-1 space-y-2 border-t border-line">
      {verifications.map((v: VerificationResult) => (
        <div key={v.id} className="text-xs">
          <div className="flex items-center gap-2">
            <span
              className={`px-1.5 py-px rounded-full text-[10px] font-medium ${
                v.status === "passed" ? "bg-emerald-500/10 text-emerald-300" : "bg-red-500/10 text-red-300"
              }`}
            >
              {v.status === "passed" ? "通过" : "驳回"}
            </span>
            <span className="text-ink-3">
              {v.verifier_run_id ? "agent 验证" : "机器验证"} ·{" "}
              {new Date(v.created_at).toLocaleString("zh-CN", { month: "numeric", day: "numeric", hour: "2-digit", minute: "2-digit" })}
            </span>
          </div>
          {v.summary && <p className="mt-1 text-ink-2 whitespace-pre-wrap">{v.summary}</p>}
          {v.evidence && (
            <details className="mt-1">
              <summary className="text-ink-3 cursor-pointer hover:text-ink-2">证据</summary>
              <p className="mt-1 text-ink-3 whitespace-pre-wrap">{v.evidence}</p>
            </details>
          )}
        </div>
      ))}
    </div>
  );
}

// GoalSubGoals renders the goal's sub-goal panel (v2 model): work items split
// off the goal — their own assignee, run, and (optional) agent verifier.
// Rows expand to show the sub-goal's description + verification rounds.
export function GoalSubGoals({ goalId }: { goalId: string }) {
  const { data: subGoals, refetch } = useSubGoals(goalId);
  const { data: agents } = useAgents();
  const createSubGoal = useCreateSubGoal();
  const [showForm, setShowForm] = useState(false);
  const [expandedId, setExpandedId] = useState("");

  // Sub-goal state changes arrive over WS (the same bus as the rest).
  useWSEvent("sub_goal.created", () => refetch());
  useWSEvent("sub_goal.verifying", () => refetch());
  useWSEvent("sub_goal.verified", () => refetch());
  useWSEvent("sub_goal.rejected", () => refetch());
  useWSEvent("sub_goal.retrying", () => refetch());
  useWSEvent("sub_goal.failed", () => refetch());
  useWSEvent("sub_goal.cancelled", () => refetch());

  const agentName = (id: string) => agents?.find((a) => a.id === id)?.name ?? id.slice(0, 8);
  const depTitle = (id: string) => subGoals?.find((s) => s.id === id)?.title ?? id.slice(0, 8);

  return (
    <div className="bg-surface-1 rounded-xl border border-line overflow-hidden">
      <div className="px-4 py-2.5 border-b border-line flex items-center justify-between">
        <span className="text-xs font-medium text-ink-3 uppercase tracking-wide">
          子任务{subGoals && subGoals.length > 0 && `（${subGoals.length}）`}
        </span>
        <Button variant="outline" onClick={() => setShowForm(true)}>+ 拆子任务</Button>
      </div>
      <div className="p-4">
        {!subGoals || subGoals.length === 0 ? (
          <Empty>没有子任务。Owner 可以把独立的工作项拆出来，交给其他 agent 并行干。</Empty>
        ) : (
          <ul className="space-y-2">
            {subGoals.map((sg: SubGoal) => {
              const expanded = expandedId === sg.id;
              return (
                <li key={sg.id} className="text-sm border border-line rounded-lg">
                  <button
                    onClick={() => setExpandedId(expanded ? "" : sg.id)}
                    className="w-full flex items-center gap-2 px-3 py-2 text-left hover:bg-surface-0/60 rounded-lg transition-colors"
                  >
                    <span className={`transition-transform text-ink-3 ${expanded ? "rotate-90" : ""}`}>▸</span>
                    <span className="font-medium text-ink truncate max-w-[30%]">{sg.title}</span>
                    <SubGoalStatus status={sg.status} />
                    {sg.depends_on && sg.depends_on.length > 0 && sg.status === "running" && (
                      <span title="等待依赖完成" aria-label="等待依赖">🔒</span>
                    )}
                    {sg.depends_on && sg.depends_on.length > 0 && (
                      <span className="flex items-center gap-1 flex-wrap">
                        {sg.depends_on.map((depId) => (
                          <span key={depId} className="px-1.5 py-px rounded-full text-[10px] bg-surface-2 text-ink-3">
                            🔗 {depTitle(depId)}
                          </span>
                        ))}
                      </span>
                    )}
                    <span className="text-ink-3 text-xs">{agentName(sg.assignee_id)}</span>
                    {sg.verifier_id && (
                      <span className="text-ink-3 text-xs">· 验证：{agentName(sg.verifier_id)}</span>
                    )}
                    {sg.execution_attempt > 0 && (
                      <span className="text-ink-3 text-xs">· 机器重试 {sg.execution_attempt}</span>
                    )}
                    {sg.quality_iteration > 0 && (
                      <span className="text-ink-3 text-xs">· 质量迭代 {sg.quality_iteration}</span>
                    )}
                  </button>
                  {expanded && (
                    <div className="px-3 pb-3 pt-1 space-y-2">
                      {sg.description && <p className="text-xs text-ink-3 whitespace-pre-wrap">{sg.description}</p>}
                      <Verifications goalId={goalId} subGoalId={sg.id} />
                    </div>
                  )}
                </li>
              );
            })}
          </ul>
        )}
      </div>

      {showForm && (
        <NewSubGoalForm
          goalId={goalId}
          agents={agents}
          candidates={subGoals ?? []}
          onClose={() => setShowForm(false)}
          onCreate={() => setShowForm(false)}
        />
      )}
    </div>
  );
}

function NewSubGoalForm({
  goalId,
  agents,
  candidates,
  onClose,
  onCreate,
}: {
  goalId: string;
  agents?: { id: string; name: string }[];
  candidates?: SubGoal[]; // 依赖候选 = 同 goal 其他 sub-goal（创建时不存在自身，全部可选）
  onClose: () => void;
  onCreate: () => void;
}) {
  const createSubGoal = useCreateSubGoal();
  const [title, setTitle] = useState("");
  const [description, setDescription] = useState("");
  const [assigneeId, setAssigneeId] = useState("");
  const [verifierId, setVerifierId] = useState("");
  const [dependsOn, setDependsOn] = useState<string[]>([]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    createSubGoal.mutate(
      { goalId, title, description, assignee_id: assigneeId, verifier_id: verifierId || undefined, depends_on: dependsOn.length > 0 ? dependsOn : undefined },
      { onSuccess: onCreate }
    );
  };

  return (
    <Dialog
      title="拆出一个子任务"
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button type="submit" form="subgoal-form" disabled={createSubGoal.isPending}>
            {createSubGoal.isPending ? "创建中…" : "创建"}
          </Button>
        </>
      }
    >
      <form id="subgoal-form" onSubmit={handleSubmit} className="space-y-4">
        <Field label="标题" hint="必填——这块工作的目标">
          <input value={title} onChange={(e) => setTitle(e.target.value)} className={inputCls} required placeholder="如：实现 OAuth callback" />
        </Field>
        <Field label="描述">
          <textarea value={description} onChange={(e) => setDescription(e.target.value)} className={inputCls} rows={2} />
        </Field>
        <Field label="指派给 Agent" hint="必填——该 agent 在独立 worktree 上并行干这块活">
          <select value={assigneeId} onChange={(e) => setAssigneeId(e.target.value)} className={inputCls} required>
            <option value="">选择…</option>
            {agents?.map((a) => (
              <option key={a.id} value={a.id}>{a.name}</option>
            ))}
          </select>
        </Field>
        <Field label="验证者 Agent" hint="可选——留空 = 机器验证（项目的验收策略命令）">
          <select value={verifierId} onChange={(e) => setVerifierId(e.target.value)} className={inputCls}>
            <option value="">机器验证（默认）</option>
            {agents?.map((a) => (
              <option key={a.id} value={a.id}>{a.name}</option>
            ))}
          </select>
        </Field>
        {candidates && candidates.length > 0 && (
          <Field label="依赖" hint="可选——勾选的前置子任务全部 verified 后，这个子任务才会开始执行">
            <div className="border border-line-strong rounded-md max-h-40 overflow-y-auto divide-y divide-line">
              {candidates.map((sg) => (
                <label key={sg.id} className="flex items-center gap-2 px-3 py-2 text-sm cursor-pointer hover:bg-surface-0">
                  <input
                    type="checkbox"
                    checked={dependsOn.includes(sg.id)}
                    onChange={(e) =>
                      setDependsOn((prev) =>
                        e.target.checked ? [...prev, sg.id] : prev.filter((x) => x !== sg.id)
                      )
                    }
                  />
                  <span className="truncate">{sg.title}</span>
                </label>
              ))}
            </div>
          </Field>
        )}
        {createSubGoal.isError && (
          <p className="text-sm text-red-400">{String(createSubGoal.error)}</p>
        )}
      </form>
    </Dialog>
  );
}
