"use client";

import { useMemo, useState } from "react";
import { Terminal } from "lucide-react";
import {
  useRuntimes,
  useDeleteRuntime,
  useMachines,
  useGoalEvents,
  useAgents,
} from "@/lib/queries";
import {
  Empty,
  PageBody,
  PageHeader,
  SectionCard,
  Segmented,
  Skeleton,
  StatStrip,
  Toolbar,
} from "@/components/ui";
import { relTime } from "@/lib/utils";
import type { Agent, Runtime } from "@/lib/types";

// 行网格：左侧主信息 + 中间元信息列 + 右侧操作；窄屏退化为标签-值行。
const ROW_GRID =
  "grid grid-cols-1 gap-1.5 px-4 py-3 md:grid-cols-[minmax(0,1fr)_88px_150px_128px_52px_60px] md:items-center md:gap-3";

const HEAD_GRID =
  "hidden md:grid md:grid-cols-[minmax(0,1fr)_88px_150px_128px_52px_60px] md:gap-3 border-b border-line px-4 py-2 font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3";

// 运行时有三种读时状态：active=CLI 探测在；inactive=机器心跳丢失（存储仍是
// active，List 读时 join 出来的）；absent=机器最新探测里已经没有这个 CLI。
const STATUS_META: Record<string, { label: string; dot: string; text: string }> = {
  active: { label: "在线", dot: "bg-emerald-400", text: "text-emerald-300" },
  inactive: { label: "机器离线", dot: "bg-amber-400", text: "text-amber-300" },
  absent: { label: "CLI 已移除", dot: "bg-red-400", text: "text-red-300" },
};

function statusMeta(status: string) {
  return STATUS_META[status] ?? { label: status, dot: "bg-zinc-400", text: "text-ink-3" };
}

// 探测注册的运行时名叫 "<cli>@<机器名>"，前缀即 CLI 类型（claude / codex…）。
function cliType(name: string): string {
  const at = name.indexOf("@");
  return at > 0 ? name.slice(0, at) : name;
}

function modelsOf(agents: Agent[]): string {
  if (agents.length === 0) return "—";
  const models = Array.from(new Set(agents.map((a) => a.model).filter(Boolean)));
  return models.length ? models.join(" / ") : "runtime 默认";
}

function RuntimeRow({
  rt,
  machineName,
  agents,
  onDelete,
}: {
  rt: Runtime;
  machineName: string;
  agents: Agent[];
  onDelete: (id: string) => void;
}) {
  const [confirming, setConfirming] = useState(false);
  const st = statusMeta(rt.status);
  const models = modelsOf(agents);
  const created = new Date(rt.created_at).toLocaleString();

  return (
    <li className={`${ROW_GRID} transition-colors hover:bg-surface-2/40`}>
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <span className={`h-1.5 w-1.5 shrink-0 rounded-full ${st.dot}`} />
          <span className="truncate text-sm font-medium text-ink">{rt.name}</span>
          <span className="shrink-0 font-mono text-[11px] text-ink-3">{rt.id.slice(0, 8)}</span>
        </div>
        <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 pl-3.5 text-[11px] text-ink-3">
          <span className={st.text}>{st.label}</span>
          <span>·</span>
          <span title={created}>创建于 {relTime(rt.created_at)}</span>
        </div>
      </div>

      <div className="flex items-center justify-between gap-2 text-xs md:block">
        <span className="text-[11px] text-ink-3 md:hidden">类型</span>
        <span className="font-mono text-ink-2">{cliType(rt.name)}</span>
      </div>

      {/* 模型列是定宽 grid item：父级必须 min-w-0/overflow-hidden，值用 block
          truncate（inline 的 span 上 truncate 不生效），长模型串才不会压到「机器」。 */}
      <div className="flex min-w-0 items-center justify-between gap-2 overflow-hidden text-xs md:block md:w-full">
        <span className="shrink-0 text-[11px] text-ink-3 md:hidden">模型</span>
        <span className="block min-w-0 truncate text-ink-2" title={models}>
          {models}
        </span>
      </div>

      <div className="flex items-center justify-between gap-2 text-xs md:block">
        <span className="text-[11px] text-ink-3 md:hidden">机器</span>
        <span className="truncate text-ink-2">{machineName || "—"}</span>
      </div>

      <div className="flex items-center justify-between gap-2 text-xs md:block md:text-right">
        <span className="text-[11px] text-ink-3 md:hidden">Agent</span>
        <span className="font-mono tabular-nums text-ink">{agents.length}</span>
      </div>

      <div className="flex items-center justify-end text-xs">
        {confirming ? (
          <span className="space-x-2 whitespace-nowrap">
            <button
              type="button"
              className="font-medium text-red-400 transition-colors hover:text-red-300"
              onClick={() => {
                onDelete(rt.id);
                setConfirming(false);
              }}
            >
              确认删除
            </button>
            <button
              type="button"
              className="text-ink-3 transition-colors hover:text-ink-2"
              onClick={() => setConfirming(false)}
            >
              取消
            </button>
          </span>
        ) : (
          <button
            type="button"
            className="text-xs text-ink-3 transition-colors hover:text-red-400"
            onClick={() => setConfirming(true)}
          >
            删除
          </button>
        )}
      </div>
    </li>
  );
}

export default function RuntimesPage() {
  useGoalEvents();
  const { data: runtimes, isLoading } = useRuntimes();
  const { data: machines } = useMachines();
  const { data: agents } = useAgents();
  const del = useDeleteRuntime();
  const [filter, setFilter] = useState<"all" | "active" | "attention">("all");

  const machineName = (id: string) => machines?.find((m) => m.id === id)?.name ?? id;

  // runtime_id → 该运行时下的 agent（数量与模型都从这里来）。
  const agentsByRuntime = useMemo(() => {
    const map = new Map<string, Agent[]>();
    for (const a of agents ?? []) {
      const list = map.get(a.runtime_id) ?? [];
      list.push(a);
      map.set(a.runtime_id, list);
    }
    return map;
  }, [agents]);

  const activeCount = (runtimes ?? []).filter((r) => r.status === "active").length;
  const absentCount = (runtimes ?? []).filter((r) => r.status === "absent").length;

  // 平台没有「默认运行时」字段；agent 各自绑定 runtime_id。这里取关联 agent
  // 最多的那个当「主力」，在 hint 里说清是按绑定数推定的，不伪造设置项。
  const mainRuntime = useMemo(() => {
    let best: { rt: Runtime; count: number } | null = null;
    for (const rt of runtimes ?? []) {
      const count = agentsByRuntime.get(rt.id)?.length ?? 0;
      if (count > 0 && (!best || count > best.count)) best = { rt, count };
    }
    return best;
  }, [runtimes, agentsByRuntime]);

  const usedRuntimeCount = (runtimes ?? []).filter(
    (r) => (agentsByRuntime.get(r.id)?.length ?? 0) > 0
  ).length;

  const visible = (runtimes ?? []).filter((r) =>
    filter === "all" ? true : filter === "active" ? r.status === "active" : r.status !== "active"
  );

  return (
    <PageBody>
      <PageHeader
        title="运行时"
        desc="远端机器运行 agentwork connect 时探测到的 agent CLI 会自动注册成运行时（CLI@机器名）；Agent 与 Run 都绑定到具体运行时。"
      />

      <StatStrip
        items={[
          {
            label: "运行时",
            value: isLoading ? "—" : (runtimes?.length ?? 0),
            hint: "探测自动注册",
          },
          {
            label: "在线",
            value: isLoading ? "—" : activeCount,
            tone: activeCount ? "ok" : "muted",
            hint: absentCount ? `${absentCount} 个 CLI 已移除` : "全部可用",
          },
          {
            label: "关联 Agent",
            value: isLoading ? "—" : (agents?.length ?? 0),
            hint: `覆盖 ${usedRuntimeCount} 个运行时`,
          },
          {
            label: "主力运行时",
            value: mainRuntime ? (
              <span className="block truncate text-sm" title={mainRuntime.rt.name}>
                {mainRuntime.rt.name}
              </span>
            ) : (
              <span className="text-sm text-ink-3">—</span>
            ),
            hint: mainRuntime ? `${mainRuntime.count} 个 agent 绑定` : "暂无 agent 绑定",
            tone: "default",
          },
        ]}
      />

      {del.isError && <p className="text-sm text-red-400">{String(del.error)}</p>}

      <SectionCard
        title="运行时"
        count={isLoading ? undefined : visible.length}
        desc={filter === "all" ? undefined : filter === "active" ? "仅在线" : "仅需处理"}
        action={
          <Toolbar>
            <Segmented
              value={filter}
              onChange={setFilter}
              options={[
                { value: "all", label: "全部" },
                { value: "active", label: "在线" },
                { value: "attention", label: "需处理" },
              ]}
            />
          </Toolbar>
        }
        bodyClassName="p-0"
      >
        {isLoading ? (
          <div className="divide-y divide-line">
            {[0, 1, 2].map((i) => (
              <div key={i} className="flex items-center gap-4 px-4 py-4">
                <Skeleton className="h-4 w-1/3" />
                <Skeleton className="h-4 w-16" />
                <Skeleton className="ml-auto h-4 w-24" />
              </div>
            ))}
          </div>
        ) : !runtimes?.length ? (
          <Empty icon={<Terminal className="h-5 w-5" />}>
            还没有运行时——在远端机器上运行 <code className="font-mono">agentwork connect</code>
            ，探测到的 agent CLI 会自动注册到这里。
          </Empty>
        ) : !visible.length ? (
          <Empty>当前筛选下没有运行时。</Empty>
        ) : (
          <>
            <div className={HEAD_GRID}>
              <span>运行时</span>
              <span>类型</span>
              <span>模型</span>
              <span>机器</span>
              <span className="text-right">Agent</span>
              <span className="text-right">操作</span>
            </div>
            <ul className="divide-y divide-line">
              {visible.map((rt) => (
                <RuntimeRow
                  key={rt.id}
                  rt={rt}
                  machineName={machineName(rt.machine_id)}
                  agents={agentsByRuntime.get(rt.id) ?? []}
                  onDelete={(id) => del.mutate(id)}
                />
              ))}
            </ul>
          </>
        )}
      </SectionCard>
    </PageBody>
  );
}