"use client";

import { useMemo, useState } from "react";
import { Monitor } from "lucide-react";
import { useMachines, useRuntimes } from "@/lib/queries";
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
import type { Machine, ProbeCLI } from "@/lib/types";

const ROW_GRID =
  "grid grid-cols-1 gap-1.5 px-4 py-3 transition-colors hover:bg-surface-2/40 md:grid-cols-[minmax(0,1fr)_64px_104px_88px] md:items-center md:gap-3";

const HEAD_GRID =
  "hidden md:grid md:grid-cols-[minmax(0,1fr)_64px_104px_88px] md:gap-3 border-b border-line px-4 py-2 font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3";

// parseCLIs decodes the machine's probe report (best-effort — bad JSON
// shows as "未知" rather than crashing the page).
function parseCLIs(raw: string): ProbeCLI[] {
  try {
    const v = JSON.parse(raw);
    return Array.isArray(v) ? (v as ProbeCLI[]) : [];
  } catch {
    return [];
  }
}

function fullTime(iso: string): string {
  return iso ? new Date(iso).toLocaleString() : "—";
}

function MachineRow({ m, runtimeCount }: { m: Machine; runtimeCount: number }) {
  const clis = parseCLIs(m.probed_clis);
  const online = m.status === "connected";

  return (
    <li className={ROW_GRID}>
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <span
            className={`h-1.5 w-1.5 shrink-0 rounded-full ${online ? "bg-emerald-400" : "bg-zinc-400"}`}
          />
          <span className="truncate text-sm font-medium text-ink">{m.name}</span>
          <span className="truncate font-mono text-[11px] text-ink-3">{m.hostname}</span>
          {m.version && (
            <span className="shrink-0 text-[11px] text-ink-3">agentwork {m.version}</span>
          )}
        </div>

        <div className="mt-0.5 flex flex-wrap items-center gap-x-2 gap-y-0.5 pl-3.5 text-[11px] text-ink-3">
          <span className={online ? "text-emerald-300" : "text-ink-3"}>
            {online ? "在线" : "离线"}
          </span>
          <span>·</span>
          <span title={fullTime(m.created_at)}>接入于 {relTime(m.created_at)}</span>
          {!online && m.last_seen_at && <span>· 最后心跳 {relTime(m.last_seen_at)}</span>}
        </div>

        {clis.length > 0 ? (
          <div className="mt-1.5 flex flex-wrap items-center gap-x-3 gap-y-1 pl-3.5 text-[11px]">
            {clis.map((c) => (
              <span key={c.name} className="inline-flex items-center gap-1.5">
                <span className="font-mono text-ink-2">{c.name}</span>
                {c.version && <span className="text-ink-3">{c.version}</span>}
                {c.skills_dir && (
                  <span className="truncate font-mono text-ink-3" title={c.skills_dir}>
                    {c.skills_dir}
                  </span>
                )}
              </span>
            ))}
          </div>
        ) : (
          <p className="mt-1 pl-3.5 text-[11px] text-ink-3">未探测到 agent CLI</p>
        )}
      </div>

      <div className="flex items-center justify-between gap-2 text-xs md:block md:text-right">
        <span className="text-[11px] text-ink-3 md:hidden">运行时</span>
        <span className="font-mono tabular-nums text-ink">{runtimeCount}</span>
      </div>

      <div className="flex items-center justify-between gap-2 text-xs md:block">
        <span className="text-[11px] text-ink-3 md:hidden">最后心跳</span>
        <span className="text-ink-2" title={fullTime(m.last_seen_at)}>
          {m.last_seen_at ? relTime(m.last_seen_at) : "—"}
        </span>
      </div>

      <div className="flex items-center justify-between gap-2 text-xs md:block">
        <span className="text-[11px] text-ink-3 md:hidden">接入时间</span>
        <span className="text-ink-2" title={fullTime(m.created_at)}>
          {relTime(m.created_at) || "—"}
        </span>
      </div>
    </li>
  );
}

export default function MachinesPage() {
  const { data: machines, isLoading } = useMachines();
  const { data: runtimes } = useRuntimes();
  const [filter, setFilter] = useState<"all" | "connected" | "offline">("all");

  // machine_id → 该机器上的运行时数量。
  const runtimeCountByMachine = useMemo(() => {
    const map = new Map<string, number>();
    for (const rt of runtimes ?? []) {
      map.set(rt.machine_id, (map.get(rt.machine_id) ?? 0) + 1);
    }
    return map;
  }, [runtimes]);

  const onlineCount = (machines ?? []).filter((m) => m.status === "connected").length;
  const offlineCount = (machines ?? []).length - onlineCount;
  const latest = machines?.[0]; // List 按 last_seen_at DESC 排序
  const machinesWithRuntimes = (machines ?? []).filter(
    (m) => (runtimeCountByMachine.get(m.id) ?? 0) > 0
  ).length;

  const visible = (machines ?? []).filter((m) =>
    filter === "all"
      ? true
      : filter === "connected"
        ? m.status === "connected"
        : m.status !== "connected"
  );

  return (
    <PageBody>
      <PageHeader
        title="机器"
        desc="远端主机执行 agentwork connect 后注册到这里——在线状态、探测到的 agent CLI 与它承载的运行时。"
      />

      <StatStrip
        items={[
          {
            label: "机器",
            value: isLoading ? "—" : (machines?.length ?? 0),
            hint: "connect 自动注册",
          },
          {
            label: "在线",
            value: isLoading ? "—" : onlineCount,
            tone: onlineCount ? "ok" : "muted",
            hint: offlineCount ? `${offlineCount} 台离线` : "全部在线",
          },
          {
            label: "运行时",
            value: isLoading ? "—" : (runtimes?.length ?? 0),
            hint: `分布在 ${machinesWithRuntimes} 台机器`,
          },
          {
            label: "最近心跳",
            value: latest?.last_seen_at ? relTime(latest.last_seen_at) : "—",
            tone: latest?.status === "connected" ? "ok" : "muted",
            hint: latest ? latest.name : "还没有心跳",
          },
        ]}
      />

      <SectionCard
        title="机器"
        count={isLoading ? undefined : visible.length}
        desc={filter === "all" ? undefined : filter === "connected" ? "仅在线" : "仅离线"}
        action={
          <Toolbar>
            <Segmented
              value={filter}
              onChange={setFilter}
              options={[
                { value: "all", label: "全部" },
                { value: "connected", label: "在线" },
                { value: "offline", label: "离线" },
              ]}
            />
          </Toolbar>
        }
        bodyClassName="p-0"
      >
        {isLoading ? (
          <div className="divide-y divide-line">
            {[0, 1].map((i) => (
              <div key={i} className="flex items-center gap-4 px-4 py-4">
                <Skeleton className="h-4 w-1/3" />
                <Skeleton className="h-4 w-16" />
                <Skeleton className="ml-auto h-4 w-24" />
              </div>
            ))}
          </div>
        ) : !machines?.length ? (
          <Empty icon={<Monitor className="h-5 w-5" />}>
            还没有注册的机器——在远端主机安装 agentwork 并运行{" "}
            <code className="font-mono">agentwork connect</code>。
          </Empty>
        ) : !visible.length ? (
          <Empty>当前筛选下没有机器。</Empty>
        ) : (
          <>
            <div className={HEAD_GRID}>
              <span>机器</span>
              <span className="text-right">运行时</span>
              <span>最后心跳</span>
              <span>接入时间</span>
            </div>
            <ul className="divide-y divide-line">
              {visible.map((m) => (
                <MachineRow
                  key={m.id}
                  m={m}
                  runtimeCount={runtimeCountByMachine.get(m.id) ?? 0}
                />
              ))}
            </ul>
          </>
        )}
      </SectionCard>

      <SectionCard
        title="如何接入"
        desc="在远端主机安装 agentwork 后在 shell 里执行"
        bodyClassName="p-4 space-y-2.5"
      >
        <div className="rounded-md border border-line bg-surface-0 px-3 py-2 font-mono text-xs text-ink">
          <span className="mr-2 text-accent">$</span>agentwork connect
        </div>
        <ul className="space-y-1 text-xs leading-relaxed text-ink-3">
          <li>首次运行会向平台注册本机，之后按心跳保活；心跳丢失会被标记为离线。</li>
          <li>连接时会上报本机探测到的 agent CLI，每个 CLI 自动生成一个运行时（CLI@机器名）。</li>
          <li>机器离线时它上面的运行时无法领取新 run；CLI 被卸载后对应运行时标记为「CLI 已移除」。</li>
        </ul>
      </SectionCard>
    </PageBody>
  );
}