"use client";

import { useState } from "react";
import { useAgents, useRuntimes, useDeleteAgent, useAgentDeleteImpact, usePinnedAgents, useSetAgentPin, useGoalEvents } from "@/lib/queries";
import { AgentForm } from "@/components/agent-form";
import { ChatPanel } from "@/components/chat-panel";
import {
  Button,
  Dialog,
  PageBody,
  PageHeader,
  SectionCard,
  StatStrip,
  Toolbar,
  Segmented,
  Empty,
  Skeleton,
} from "@/components/ui";
import { Pin, PinOff, Pencil, Trash2, MessageSquare, Bot, Sparkles, Search } from "lucide-react";
import { cn, relTime } from "@/lib/utils";
import type { Agent } from "@/lib/types";

type AgentScope = "all" | "pinned";

export default function AgentsPage() {
  useGoalEvents();
  const { data: agents, isLoading } = useAgents();
  const { data: runtimes } = useRuntimes();
  const { data: pinnedIds } = usePinnedAgents();
  const del = useDeleteAgent();
  const toggle = useSetAgentPin();
  const [showForm, setShowForm] = useState(false);
  const [editing, setEditing] = useState<Agent | null>(null);
  const [chatting, setChatting] = useState<Agent | null>(null);
  // 待删除的 agent：打开确认弹窗（先拉 delete-impact，再决定删不删）。
  const [deleting, setDeleting] = useState<Agent | null>(null);
  // 纯渲染层筛选：搜索名称/描述/短 id/运行时，或只看已固定。
  const [query, setQuery] = useState("");
  const [scope, setScope] = useState<AgentScope>("all");

  const pinnedSet = new Set(pinnedIds ?? []);
  const runtimeName = (id: string) => runtimes?.find((r) => r.id === id)?.name ?? id;
  const pinnedAgents = (agents ?? []).filter((a) => pinnedSet.has(a.id));

  const q = query.trim().toLowerCase();
  const visibleAgents = (agents ?? []).filter((a) => {
    if (scope === "pinned" && !pinnedSet.has(a.id)) return false;
    if (!q) return true;
    return [a.name, a.description ?? "", a.id, runtimeName(a.runtime_id)].some((v) =>
      v.toLowerCase().includes(q)
    );
  });

  const total = agents?.length ?? 0;
  const stewardCount = (agents ?? []).filter((a) => a.type === "steward").length;
  const usedRuntimes = new Set((agents ?? []).map((a) => a.runtime_id)).size;

  const togglePin = (a: Agent) => {
    const next = !pinnedSet.has(a.id);
    toggle.mutate({ id: a.id, pinned: next });
  };

  return (
    <PageBody>
      <PageHeader
        title="智能体"
        desc="每个 agent 是一名可复用的执行者：绑定运行时、模型与技能，固定后可从左侧快捷栏直接开聊。"
        action={<Button onClick={() => setShowForm(true)}>+ 新建</Button>}
      />

      <StatStrip
        items={[
          { label: "Agent 总数", value: total, hint: `${stewardCount} 个系统管家角色` },
          {
            label: "已固定",
            value: pinnedAgents.length,
            tone: pinnedAgents.length > 0 ? "info" : "muted",
            hint: "显示在左侧快捷栏",
          },
          {
            label: "运行时",
            value: runtimes?.length ?? 0,
            hint: `被 agent 使用 ${usedRuntimes} 个`,
          },
        ]}
      />

      <div className="flex flex-col items-start gap-5 lg:flex-row">
        {/* 左侧：固定的 agent 侧边栏 —— 点击直接开聊。pinned 状态来自后端
            agent_pin 表，刷新/重启后仍在。这里只换用 SectionCard 版面，
            开聊与取消固定的交互保持不变。 */}
        <aside className="w-full shrink-0 self-start lg:sticky lg:top-6 lg:w-60">
          <SectionCard title="固定" count={pinnedAgents.length} bodyClassName="p-0">
            {pinnedAgents.length === 0 ? (
              <Empty icon={<Pin className="h-4 w-4" />}>
                还没有固定的 agent。在右侧列表点「固定」，即可在此快速对话。
              </Empty>
            ) : (
              <ul className="divide-y divide-line">
                {pinnedAgents.map((a) => (
                  <li key={a.id} className="group flex items-center gap-2 px-3 py-2.5">
                    <button
                      onClick={() => setChatting(a)}
                      className="flex min-w-0 flex-1 items-center gap-2 text-left transition-colors hover:text-accent"
                      title={`与 ${a.name} 对话`}
                    >
                      <Bot className="h-3.5 w-3.5 shrink-0 text-ink-3" />
                      <span className="min-w-0">
                        <span className="block truncate text-sm font-medium text-ink group-hover:text-accent">
                          {a.name}
                        </span>
                        <span className="block truncate font-mono text-[10px] text-ink-3" title={a.id}>
                          {a.id.slice(0, 8)}
                        </span>
                      </span>
                    </button>
                    <button
                      onClick={() => togglePin(a)}
                      className="shrink-0 text-ink-3 opacity-0 transition-colors hover:text-red-400 group-hover:opacity-100"
                      title="取消固定"
                      aria-label={`取消固定 ${a.name}`}
                    >
                      <PinOff className="h-3.5 w-3.5" />
                    </button>
                  </li>
                ))}
              </ul>
            )}
          </SectionCard>
        </aside>

        {/* 右侧：全部 agent 的行式列表，每行带固定/编辑/删除/对话 */}
        <SectionCard
          className="min-w-0 w-full flex-1"
          title="全部 Agent"
          count={visibleAgents.length}
          desc={q ? `筛选自 ${total} 个` : scope === "pinned" ? `共固定 ${pinnedAgents.length} 个` : undefined}
          bodyClassName="p-0"
          action={
            <Toolbar>
              <div className="relative">
                <Search className="pointer-events-none absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-ink-3" />
                <input
                  value={query}
                  onChange={(e) => setQuery(e.target.value)}
                  placeholder="搜索名称 / id / 运行时…"
                  className="w-52 rounded-md border border-line bg-surface-2 py-1.5 pl-7 pr-2.5 text-xs text-ink outline-none transition-colors placeholder:text-ink-3 focus:border-accent-dim"
                />
              </div>
              <Segmented
                value={scope}
                onChange={setScope}
                options={[
                  { value: "all", label: "全部" },
                  { value: "pinned", label: "已固定" },
                ]}
              />
            </Toolbar>
          }
        >
          {isLoading ? (
            <div className="divide-y divide-line">
              {[0, 1, 2].map((i) => (
                <div key={i} className="flex items-center gap-3 px-4 py-3.5">
                  <Skeleton className="h-7 w-7 rounded-md" />
                  <div className="flex-1 space-y-2">
                    <Skeleton className="h-3 w-40" />
                    <Skeleton className="h-3 w-64" />
                  </div>
                </div>
              ))}
            </div>
          ) : visibleAgents.length === 0 ? (
            <Empty icon={<Search className="h-4 w-4" />}>
              {total === 0
                ? "还没有 agent。点「+ 新建」创建一个。"
                : "没有匹配当前搜索/筛选条件的 agent。"}
            </Empty>
          ) : (
            <ul className="divide-y divide-line">
              {visibleAgents.map((a: Agent) => {
                const pinned = pinnedSet.has(a.id);
                const isSteward = a.type === "steward";
                return (
                  <li key={a.id} className="group px-4 py-3 transition-colors hover:bg-surface-2/40">
                    <div className="flex items-start gap-3">
                      <span
                        className={cn(
                          "mt-0.5 flex h-7 w-7 shrink-0 items-center justify-center rounded-md border",
                          pinned
                            ? "border-accent/30 bg-accent/10 text-accent"
                            : "border-line bg-surface-2 text-ink-3"
                        )}
                      >
                        {isSteward ? <Sparkles className="h-3.5 w-3.5" /> : <Bot className="h-3.5 w-3.5" />}
                      </span>

                      <div className="min-w-0 flex-1">
                        <div className="flex flex-wrap items-center gap-2">
                          <button
                            onClick={() => setEditing(a)}
                            className="truncate text-sm font-medium text-ink transition-colors hover:text-accent"
                            title="点击编辑 agent"
                          >
                            {a.name}
                          </button>
                          <span className="font-mono text-[11px] text-ink-3" title={a.id}>
                            {a.id.slice(0, 8)}
                          </span>
                          {isSteward && (
                            <span className="inline-flex items-center rounded border border-accent/30 bg-accent/10 px-1.5 py-0.5 text-[10px] font-medium text-accent">
                              管家
                            </span>
                          )}
                          {pinned && (
                            <span className="inline-flex items-center gap-1 text-[10px] font-medium text-accent">
                              <Pin className="h-2.5 w-2.5 fill-current" />
                              已固定
                            </span>
                          )}
                        </div>
                        <p className="mt-0.5 line-clamp-1 text-xs text-ink-3">
                          {a.description || "未填写描述"}
                        </p>
                        <div className="mt-1 flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-ink-3">
                          <span title={a.runtime_id}>运行时 · {runtimeName(a.runtime_id)}</span>
                          <span className="font-mono">model · {a.model || "默认"}</span>
                          <span className="font-mono">并发 · {a.max_concurrent}</span>
                          <span title={new Date(a.created_at).toLocaleString()}>
                            {relTime(a.created_at)}创建
                          </span>
                        </div>
                      </div>

                      <div className="flex shrink-0 items-center gap-0.5">
                        <Button
                          variant="ghost"
                          className="h-7 gap-1 px-2 text-xs"
                          onClick={() => setChatting(a)}
                          title={`与 ${a.name} 对话`}
                        >
                          <MessageSquare className="h-3.5 w-3.5" />
                          对话
                        </Button>
                        <Button
                          variant="ghost"
                          className="h-7 gap-1 px-2 text-xs"
                          onClick={() => setEditing(a)}
                          title="编辑 agent"
                        >
                          <Pencil className="h-3.5 w-3.5" />
                          编辑
                        </Button>
                        <Button
                          variant="ghost"
                          className={cn("h-7 gap-1 px-2 text-xs", pinned && "text-accent")}
                          onClick={() => togglePin(a)}
                          disabled={toggle.isPending && toggle.variables?.id === a.id}
                          title={pinned ? "取消固定" : "固定到侧边栏"}
                        >
                          {pinned ? (
                            <Pin className="h-3.5 w-3.5 fill-current" />
                          ) : (
                            <Pin className="h-3.5 w-3.5" />
                          )}
                          {pinned ? "已固定" : "固定"}
                        </Button>
                        <button
                          onClick={() => {
                            // 清掉上一次删除的错误，避免弹窗里显示旧报错。
                            del.reset();
                            setDeleting(a);
                          }}
                          className="inline-flex h-7 items-center gap-1 rounded-md px-2 text-xs text-ink-3 transition-colors hover:bg-surface-2 hover:text-red-400"
                          title="删除 agent"
                        >
                          <Trash2 className="h-3.5 w-3.5" />
                          删除
                        </button>
                      </div>
                    </div>
                  </li>
                );
              })}
            </ul>
          )}
        </SectionCard>
      </div>

      {/* 删除失败必须显示——后端守卫（agent 被 goal/schedule/squad 引用）
          返回 400 coded error，静默等于「点了没反应」。固定/取消固定同理。 */}
      {del.isError && <p className="text-sm text-red-400">{String(del.error)}</p>}
      {toggle.isError && <p className="text-sm text-red-400">{String(toggle.error)}</p>}

      {showForm && <AgentForm onClose={() => setShowForm(false)} />}
      {editing && <AgentForm agent={editing} onClose={() => setEditing(null)} />}
      {chatting && (
        <ChatPanel agentId={chatting.id} agentName={chatting.name} onClose={() => setChatting(null)} />
      )}
      {deleting && (
        <DeleteAgentDialog
          agent={deleting}
          pending={del.isPending}
          error={del.isError ? String(del.error) : ""}
          onClose={() => setDeleting(null)}
          onConfirm={() =>
            del.mutate(deleting.id, {
              onSuccess: () => setDeleting(null),
            })
          }
        />
      )}
    </PageBody>
  );
}

// 删除确认弹窗：先查 GET /agents/{id}/delete-impact，把"删掉什么、留下
// 什么、为什么现在不能删"摆清楚，再让用户确认。后端接口未就绪/请求失败
// 时退回一条警告并允许继续——后端自身的引用守卫仍是最后一道关。
// 统计接口给出的字段：
//   can_delete / active_runs / active_goals / runs / schedules /
//   enabled_schedules / terminal_goals / sub_goals /
//   blockers[{kind, count, message}]
function DeleteAgentDialog({
  agent,
  pending,
  error,
  onClose,
  onConfirm,
}: {
  agent: Agent;
  pending: boolean;
  error: string;
  onClose: () => void;
  onConfirm: () => void;
}) {
  const { data: impact, isLoading, isError, error: impactError } = useAgentDeleteImpact(agent.id);
  const blocked = !!impact && (!impact.can_delete || impact.blockers.length > 0);

  return (
    <Dialog
      title={`删除 Agent：${agent.name}`}
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button variant="danger" onClick={onConfirm} disabled={isLoading || blocked || pending}>
            {pending ? "删除中…" : "确认删除"}
          </Button>
        </>
      }
    >
      <p className="text-sm text-ink-2">确定要删除 agent「{agent.name}」吗？</p>

      {isLoading ? (
        <p className="text-xs text-ink-3">正在统计删除影响…</p>
      ) : isError ? (
        <>
          <p className="text-xs text-red-400">无法获取删除影响：{String(impactError)}</p>
          <p className="text-xs text-ink-3">
            仍可直接删除；agent 被 goal / schedule / squad 引用时后端会拒绝。
          </p>
        </>
      ) : impact ? (
        blocked ? (
          <div className="space-y-1">
            <p className="text-xs font-medium text-amber-300">暂时不能删除：</p>
            <ul className="list-disc space-y-0.5 pl-4 text-xs text-ink-3">
            {/* 后端不变量: can_delete === (blockers.length === 0)，所以走到
                blocked 分支时 blockers 必非空，直接渲染即可（原先的
                "该 agent 仍在使用中"回退句在这个不变量下永不可达）。 */}
              {impact.blockers.map((b) => (
                // 后端的 message 是英文诊断句；两类已知 blocker 用中文
                // 说清楚"先做什么"，未知类型才回退到原始 message。
                <li key={b.kind}>
                  {b.kind === "active_runs"
                    ? `还有 ${b.count} 个排队/运行中的 run，请先等它们结束或取消`
                    : b.kind === "active_goals"
                      ? `还有 ${b.count} 个进行中的 goal，请先完成、取消或改派`
                      : b.message}
                </li>
              ))}
            </ul>
            {impact.active_runs > 0 && (
              <p className="text-[11px] text-ink-3">
                活跃运行 {impact.active_runs} 个 · 活跃任务 {impact.active_goals} 个
              </p>
            )}
          </div>
        ) : (
          <div className="space-y-1 text-xs text-ink-3">
            <p>
              将删除 {impact.schedules} 个调度（含 {impact.enabled_schedules} 个启用）、
              {impact.runs} 条运行记录；{impact.terminal_goals} 条历史任务将保留（归属显示为已删除 agent）。
            </p>
            {/* agent 删除会把该 agent 名下非终态的子任务一并取消
                （agent.go Delete: UPDATE sub_goal SET status='cancelled'…）。 */}
            {impact.sub_goals > 0 && <p>{impact.sub_goals} 条未完结子任务将一并取消。</p>}
          </div>
        )
      ) : null}

      {error && <p className="text-sm text-red-400">{error}</p>}
    </Dialog>
  );
}
