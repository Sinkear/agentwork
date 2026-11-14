"use client";

import { useState } from "react";
import { useAgents, useRuntimes, useDeleteAgent, usePinnedAgents, useSetAgentPin, useGoalEvents } from "@/lib/queries";
import { AgentForm } from "@/components/agent-form";
import { ChatPanel } from "@/components/chat-panel";
import {
  Button,
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
                            if (confirm(`删除 agent「${a.name}」？被 goal/schedule/squad 引用时会被拒绝。`)) del.mutate(a.id);
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
    </PageBody>
  );
}