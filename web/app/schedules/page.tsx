"use client";

import { useState } from "react";
import Link from "next/link";
import { CalendarClock, Plus } from "lucide-react";
import {
  useSchedules,
  useAgents,
  useSquads,
  useDomains,
  useCreateSchedule,
  useUpdateSchedule,
  useDeleteSchedule,
  useSetScheduleEnabled,
  useGoalEvents,
  useScheduleRuns,
} from "@/lib/queries";
import {
  Button,
  PageBody,
  PageHeader,
  Empty,
  Dialog,
  Field,
  inputCls,
  ConfirmDialog,
  Badge,
  SectionCard,
  Skeleton,
  StatStrip,
} from "@/components/ui";
import { cn, relTime } from "@/lib/utils";
import type { Schedule } from "@/lib/types";

export default function SchedulesPage() {
  useGoalEvents();
  const { data: schedules, isLoading } = useSchedules();
  const { data: agents } = useAgents();
  const { data: squads } = useSquads();
  const { data: domains } = useDomains();
  const createSchedule = useCreateSchedule();
  const deleteSchedule = useDeleteSchedule();
  const setEnabled = useSetScheduleEnabled();
  const [showForm, setShowForm] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);
  const [detail, setDetail] = useState<Schedule | null>(null);
  const [editTarget, setEditTarget] = useState<Schedule | null>(null);

  const agentName = (aid: string) => agents?.find((a) => a.id === aid)?.name ?? aid;
  const squadName = (sid: string) => squads?.find((s) => s.id === sid)?.name ?? sid;
  const domainName = (did: string) => domains?.find((d) => d.id === did)?.name ?? did;
  const assigneeLabel = (s: Schedule) =>
    s.assignee_type === "squad" ? (squadName(s.assignee_id) || s.assignee_id) : (agentName(s.assignee_id) || s.assignee_id);

  const list = schedules ?? [];
  const enabledCount = list.filter((s) => s.enabled).length;
  // 最近一次触发 = 所有任务里最大的 last_run_at（'' 表示从未触发）。
  const lastRunAt = list.reduce((acc, s) => (s.last_run_at > acc ? s.last_run_at : acc), "");
  // 下次触发 = 已启用任务里最早的 next_run_at，回答「这套调度下一次动是什么时候」。
  const nextRunAt = list.reduce((acc, s) => {
    if (!s.enabled || !s.next_run_at) return acc;
    return !acc || s.next_run_at < acc ? s.next_run_at : acc;
  }, "");

  return (
    <PageBody className="space-y-5">
      <PageHeader
        title="定时任务"
        desc="按 cron 自动派生 goal：每次触发在所属项目里创建一个任务，并派给指定的 agent 或小队。"
        action={
          <Button onClick={() => setShowForm(true)} className="gap-1.5">
            <Plus className="h-3.5 w-3.5" />
            新建
          </Button>
        }
      />

      <StatStrip
        items={[
          {
            label: "定时任务",
            value: list.length,
            hint: list.length === 0
              ? "还没有任务"
              : enabledCount === list.length
                ? "全部启用中"
                : `${enabledCount} 个已启用`,
          },
          {
            label: "已启用",
            value: enabledCount,
            tone: enabledCount ? "ok" : "muted",
            hint: list.length ? `${list.length - enabledCount} 个已停用` : "还没有任务",
          },
          {
            label: "最近一次触发",
            value: lastRunAt ? relTime(lastRunAt) : "从未",
            tone: lastRunAt ? "info" : "muted",
            hint: lastRunAt
              ? new Date(lastRunAt).toLocaleString("zh-CN", { hour12: false })
              : "创建后由 cron 自动触发",
          },
          {
            label: "下次触发",
            value: nextRunAt ? relTime(nextRunAt) : "—",
            tone: nextRunAt ? "info" : "muted",
            hint: nextRunAt
              ? new Date(nextRunAt).toLocaleString("zh-CN", { hour12: false })
              : "没有已启用的任务",
          },
        ]}
      />

      <SectionCard
        title="任务列表"
        count={list.length}
        desc="点名称查看触发历史；右侧开关控制启停"
      >
        {isLoading ? (
          <div className="divide-y divide-line">
            {[0, 1, 2].map((i) => (
              <div key={i} className="flex items-center gap-3 px-4 py-3.5">
                <div className="flex-1 space-y-2">
                  <Skeleton className="h-4 w-44" />
                  <Skeleton className="h-3 w-64" />
                </div>
                <Skeleton className="h-5 w-9 rounded-full" />
              </div>
            ))}
          </div>
        ) : list.length === 0 ? (
          <Empty icon={<CalendarClock className="h-5 w-5" />}>
            暂无定时任务。点右上角「新建」创建一条 cron 任务——它会按计划自动创建 goal。
          </Empty>
        ) : (
          <div className="divide-y divide-line">
            {list.map((s: Schedule) => (
              <div key={s.id} className="flex flex-wrap items-start gap-x-4 gap-y-2 px-4 py-3">
                <div className="min-w-0 flex-1 space-y-1">
                  <div className="flex flex-wrap items-center gap-2">
                    <button
                      onClick={() => setDetail(s)}
                      className="truncate text-sm font-medium text-ink transition-colors hover:text-accent hover:underline text-left"
                    >
                      {s.name}
                    </button>
                    <code className="rounded border border-line bg-surface-2 px-1.5 py-0.5 font-mono text-[11px] text-ink-2">
                      {s.cron_expression}
                    </code>
                    <span className="font-mono text-[10px] text-ink-3">{s.timezone || "UTC"}</span>
                  </div>
                  <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-ink-3">
                    <span>负责人 {assigneeLabel(s)}</span>
                    <span>项目 {domainName(s.domain_id)}</span>
                    <span>创建于 {relTime(s.created_at)}</span>
                  </div>
                </div>

                <div className="flex shrink-0 flex-wrap items-center gap-x-4 gap-y-2">
                  <div className="min-w-[132px]">
                    <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3">下次触发</div>
                    <div className="mt-0.5 text-xs text-ink-2">
                      {s.next_run_at ? relTime(s.next_run_at) : "—"}
                    </div>
                    {s.next_run_at && (
                      <div className="text-[10px] text-ink-3">
                        {new Date(s.next_run_at).toLocaleString("zh-CN", { hour12: false })}
                      </div>
                    )}
                  </div>
                  <LastRunBadge schedule={s} />
                  <Toggle
                    enabled={s.enabled}
                    pending={setEnabled.isPending && setEnabled.variables?.id === s.id}
                    onToggle={() => setEnabled.mutate({ id: s.id, enabled: !s.enabled })}
                  />
                  <div className="flex items-center gap-2">
                    <button
                      onClick={() => setEditTarget(s)}
                      className="text-xs text-ink-3 transition-colors hover:text-ink"
                    >
                      编辑
                    </button>
                    <button
                      onClick={() => setDeleteTarget(s.id)}
                      className="text-xs text-ink-3 transition-colors hover:text-red-400"
                    >
                      删除
                    </button>
                  </div>
                </div>
              </div>
            ))}
          </div>
        )}
      </SectionCard>

      {showForm && <NewScheduleForm agents={agents} squads={squads} domains={domains} onClose={() => setShowForm(false)} />}
      {detail && (
        <ScheduleDetail
          schedule={detail}
          agentName={agentName}
          squadName={squadName}
          domainName={domainName}
          onClose={() => setDetail(null)}
          onEdit={() => { setEditTarget(detail); setDetail(null); }}
        />
      )}
      {editTarget && (
        <EditScheduleDialog
          schedule={editTarget}
          agents={agents}
          squads={squads}
          domains={domains}
          onClose={() => setEditTarget(null)}
        />
      )}
      {deleteTarget && (
        <ConfirmDialog
          title="确认删除"
          message="确定要删除此 Schedule 吗？"
          onConfirm={() => deleteSchedule.mutate(deleteTarget, { onSuccess: () => setDeleteTarget(null) })}
          onClose={() => setDeleteTarget(null)}
          loading={deleteSchedule.isPending}
        />
      )}

      {/* 启停/删除失败必须显示——静默等于「点了没反应」。 */}
      {(setEnabled.isError || deleteSchedule.isError) && (
        <p className="text-sm text-red-400">
          {String(setEnabled.error ?? deleteSchedule.error)}
        </p>
      )}
    </PageBody>
  );
}

// LastRunBadge 显示最近一次触发的真实结果：读该任务的触发历史（最新一条），
// goal 状态即这次触发的产出状态；调度失败（dispatched 未落库 goal）显示 failed。
// 从未触发过不发请求，直接给文案。
function LastRunBadge({ schedule }: { schedule: Schedule }) {
  const { data: runs, isLoading } = useScheduleRuns(schedule.id, !!schedule.last_run_at);
  const last = runs?.[0];
  return (
    <div className="min-w-[86px]">
      <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3">最近结果</div>
      <div className="mt-1">
        {!schedule.last_run_at ? (
          <span className="text-[11px] text-ink-3">未触发</span>
        ) : isLoading && !last ? (
          <span className="text-[11px] text-ink-3">读取中…</span>
        ) : last ? (
          <Badge status={last.status === "failed" ? "failed" : last.goal_status || "pending"} />
        ) : (
          <span className="text-[11px] text-ink-3">无记录</span>
        )}
      </div>
    </div>
  );
}

// Toggle 是启停开关：role=switch + aria-checked，颜色只走语义 token。
function Toggle({
  enabled,
  pending,
  onToggle,
}: {
  enabled: boolean;
  pending: boolean;
  onToggle: () => void;
}) {
  return (
    <button
      type="button"
      role="switch"
      aria-checked={enabled}
      aria-label={enabled ? "停用" : "启用"}
      title={enabled ? "点击停用" : "点击启用"}
      disabled={pending}
      onClick={onToggle}
      className={cn(
        "relative h-5 w-9 shrink-0 rounded-full border transition-colors duration-150 disabled:opacity-50",
        enabled ? "border-accent bg-accent" : "border-line-strong bg-surface-3"
      )}
    >
      <span
        className={cn(
          "absolute top-0.5 h-3.5 w-3.5 rounded-full transition-[left] duration-150",
          enabled ? "left-[18px] bg-accent-ink" : "left-0.5 bg-ink-3"
        )}
      />
    </button>
  );
}

// ScheduleDetail is the schedule's firing history view (clicking the name):
// the template config at the top, each firing below with the goal it
// produced and that goal's current status, linked to the goal detail.
function ScheduleDetail({
  schedule,
  agentName,
  squadName,
  domainName,
  onClose,
  onEdit,
}: {
  schedule: Schedule;
  agentName: (id: string) => string;
  squadName: (id: string) => string;
  domainName: (id: string) => string;
  onClose: () => void;
  onEdit: () => void;
}) {
  const { data: runs, isLoading } = useScheduleRuns(schedule.id);
  const assigneeLabel =
    schedule.assignee_type === "squad" ? (squadName(schedule.assignee_id) || schedule.assignee_id) : (agentName(schedule.assignee_id) || schedule.assignee_id);

  return (
    <Dialog
      title={`定时任务：${schedule.name}`}
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onEdit}>编辑</Button>
          <Button variant="outline" onClick={onClose}>关闭</Button>
        </>
      }
    >
      <div className="space-y-3">
        {/* Template config */}
        <div className="grid grid-cols-2 gap-x-4 gap-y-1.5 text-sm text-ink-2">
          <span><span className="text-ink-3 text-xs">Cron：</span><code className="text-xs bg-surface-2 px-1.5 py-0.5 rounded">{schedule.cron_expression}</code>（{schedule.timezone || "UTC"}）</span>
          <span><span className="text-ink-3 text-xs">负责人：</span>{assigneeLabel}</span>
          <span><span className="text-ink-3 text-xs">项目：</span>{domainName(schedule.domain_id)}</span>
          <span>
            <span className="text-ink-3 text-xs">状态：</span>
            <Badge status={schedule.enabled ? "running" : "cancelled"} className="scale-90" />
          </span>
          <span><span className="text-ink-3 text-xs">下次触发：</span>{schedule.next_run_at ? new Date(schedule.next_run_at).toLocaleString("zh-CN") : "-"}</span>
          <span><span className="text-ink-3 text-xs">上次触发：</span>{schedule.last_run_at ? new Date(schedule.last_run_at).toLocaleString("zh-CN") : "从未"}</span>
        </div>
        {schedule.title_template && (
          <p className="text-xs text-ink-3">
            <span className="text-ink-3">标题模板：</span>{schedule.title_template}
          </p>
        )}

        {/* Firing history */}
        <div className="border-t border-line pt-2">
          <p className="text-xs font-medium text-ink-3 uppercase tracking-wide mb-2">
            触发历史{runs && runs.length > 0 && `（${runs.length}）`}
          </p>
          {isLoading ? (
            <p className="text-xs text-ink-3 py-3">加载中…</p>
          ) : !runs || runs.length === 0 ? (
            <p className="text-xs text-ink-3 py-3">还没有触发过。</p>
          ) : (
            <ul className="space-y-1 max-h-64 overflow-y-auto">
              {runs.map((r) => (
                <li key={r.id} className="flex items-center gap-2 text-xs">
                  <span className="text-ink-3 shrink-0">{new Date(r.planned_at).toLocaleString("zh-CN")}</span>
                  <Badge status={r.goal_status || "cancelled"} className="scale-90 shrink-0" />
                  <Link
                    href={`/goals/${r.goal_id}`}
                    onClick={onClose}
                    className="text-ink-2 hover:text-accent hover:underline truncate"
                  >
                    {r.goal_title || r.goal_id.slice(0, 8)}
                  </Link>
                </li>
              ))}
            </ul>
          )}
        </div>
      </div>
    </Dialog>
  );
}

// ScheduleFormFields is the shared field set for create + edit. It receives
// the form state + setters from the parent (the parent owns submission).
function ScheduleFormFields({
  form,
  set,
  agents,
  squads,
  domains,
}: {
  form: ScheduleFormState;
  set: (patch: Partial<ScheduleFormState>) => void;
  agents?: { id: string; name: string }[];
  squads?: { id: string; name: string }[];
  domains?: { id: string; name: string }[];
}) {
  return (
    <>
      <Field label="名称" hint="必填">
        <input value={form.name} onChange={(e) => set({ name: e.target.value })} className={inputCls} required placeholder="定时任务名称…" />
      </Field>
      <Field label="标题模板" hint="必填，每次触发时使用此模板创建 Goal 标题">
        <input value={form.title_template} onChange={(e) => set({ title_template: e.target.value })} className={inputCls} required placeholder="每日站会 {{date}}" />
      </Field>
      <Field label="描述">
        <textarea value={form.description} onChange={(e) => set({ description: e.target.value })} className={inputCls} rows={2} />
      </Field>
      <Field label="负责人类型">
        <select value={form.assignee_type} onChange={(e) => { set({ assignee_type: e.target.value, assignee_id: "" }); }} className={inputCls}>
          <option value="agent">Agent</option>
          <option value="squad">Squad</option>
        </select>
      </Field>
      {form.assignee_type === "agent" && (
        <Field label="选择 Agent" hint="必填">
          <select value={form.assignee_id} onChange={(e) => set({ assignee_id: e.target.value })} className={inputCls} required>
            <option value="">选择…</option>
            {agents?.map((a) => (
              <option key={a.id} value={a.id}>{a.name}</option>
            ))}
          </select>
        </Field>
      )}
      {form.assignee_type === "squad" && (
        <Field label="选择 Squad" hint="必填">
          <select value={form.assignee_id} onChange={(e) => set({ assignee_id: e.target.value })} className={inputCls} required>
            <option value="">选择…</option>
            {squads?.map((s) => (
              <option key={s.id} value={s.id}>{s.name}</option>
            ))}
          </select>
        </Field>
      )}
      <Field label="所属项目" hint="必填，每次触发时在此项目的仓库上执行（验收策略 + worktree 来自该项目）">
        <select value={form.domain_id} onChange={(e) => set({ domain_id: e.target.value })} className={inputCls} required>
          <option value="">选择…</option>
          {domains?.map((d) => (
            <option key={d.id} value={d.id}>{d.name}</option>
          ))}
        </select>
      </Field>
      <Field label="Cron 表达式" hint="5 字段 cron: 分 时 日 月 星期（必填）">
        <input value={form.cron_expression} onChange={(e) => set({ cron_expression: e.target.value })} className={`${inputCls} font-mono`} required placeholder="0 9 * * 1-5" />
      </Field>
      <Field label="时区">
        <input value={form.timezone} onChange={(e) => set({ timezone: e.target.value })} className={inputCls} placeholder="UTC" />
      </Field>
    </>
  );
}

type ScheduleFormState = {
  name: string;
  title_template: string;
  description: string;
  assignee_type: string;
  assignee_id: string;
  domain_id: string;
  cron_expression: string;
  timezone: string;
};

function NewScheduleForm({
  agents,
  squads,
  domains,
  onClose,
}: {
  agents?: { id: string; name: string }[];
  squads?: { id: string; name: string }[];
  domains?: { id: string; name: string }[];
  onClose: () => void;
}) {
  const createSchedule = useCreateSchedule();
  const [form, setForm] = useState<ScheduleFormState>({
    name: "", title_template: "", description: "",
    assignee_type: "agent", assignee_id: "", domain_id: "",
    cron_expression: "", timezone: "UTC",
  });
  const set = (patch: Partial<ScheduleFormState>) => setForm((f) => ({ ...f, ...patch }));

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    createSchedule.mutate(form, { onSuccess: onClose });
  };

  return (
    <Dialog
      title="新建 Schedule"
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button type="submit" form="schedule-form" disabled={createSchedule.isPending}>
            {createSchedule.isPending ? "创建中…" : "创建"}
          </Button>
        </>
      }
    >
      <form id="schedule-form" onSubmit={handleSubmit} className="space-y-4">
        <ScheduleFormFields form={form} set={set} agents={agents} squads={squads} domains={domains} />
        {createSchedule.isError && (
          <p className="text-sm text-red-400">{String(createSchedule.error)}</p>
        )}
      </form>
    </Dialog>
  );
}

// EditScheduleDialog预填当前 schedule 的值，提交调 updateSchedule。
// 改 assignee/domain 只影响后续触发——用提示说明，后端不限制。
function EditScheduleDialog({
  schedule,
  agents,
  squads,
  domains,
  onClose,
}: {
  schedule: Schedule;
  agents?: { id: string; name: string }[];
  squads?: { id: string; name: string }[];
  domains?: { id: string; name: string }[];
  onClose: () => void;
}) {
  const updateSchedule = useUpdateSchedule();
  const [form, setForm] = useState<ScheduleFormState>({
    name: schedule.name,
    title_template: schedule.title_template,
    description: schedule.description,
    assignee_type: schedule.assignee_type || "agent",
    assignee_id: schedule.assignee_id,
    domain_id: schedule.domain_id,
    cron_expression: schedule.cron_expression,
    timezone: schedule.timezone || "UTC",
  });
  const set = (patch: Partial<ScheduleFormState>) => setForm((f) => ({ ...f, ...patch }));

  // assignee/domain 与原值不同时显示"仅影响后续触发"提示。
  const assigneeChanged = form.assignee_id !== schedule.assignee_id || form.assignee_type !== schedule.assignee_type;
  const domainChanged = form.domain_id !== schedule.domain_id;

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    updateSchedule.mutate({ id: schedule.id, ...form }, { onSuccess: onClose });
  };

  return (
    <Dialog
      title={`编辑：${schedule.name}`}
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button type="submit" form="schedule-edit-form" disabled={updateSchedule.isPending}>
            {updateSchedule.isPending ? "保存中…" : "保存"}
          </Button>
        </>
      }
    >
      <form id="schedule-edit-form" onSubmit={handleSubmit} className="space-y-4">
        <ScheduleFormFields form={form} set={set} agents={agents} squads={squads} domains={domains} />
        {(assigneeChanged || domainChanged) && (
          <p className="text-xs text-amber-400 bg-amber-500/10 border border-amber-500/30 rounded px-3 py-2">
            改动负责人或项目仅影响<strong>后续触发</strong>，已派生的 goal 保持原值不变。
          </p>
        )}
        {updateSchedule.isError && (
          <p className="text-sm text-red-400">{String(updateSchedule.error)}</p>
        )}
      </form>
    </Dialog>
  );
}
