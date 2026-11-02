"use client";

import { useState } from "react";
import { useRuntimes, useDeleteRuntime, useMachines, useGoalEvents } from "@/lib/queries";
import { Button, PageHeader, Empty } from "@/components/ui";
import type { Runtime } from "@/lib/types";

function RuntimeRow({ rt, machineName, onDelete }: { rt: Runtime; machineName: string; onDelete: (id: string) => void }) {
  const [confirming, setConfirming] = useState(false);
  return (
    <tr className="border-b border-line last:border-0 hover:bg-surface-0/60">
      <td className="px-4 py-3 font-medium text-ink">{rt.name}</td>
      <td className="px-4 py-3">
        <span className="px-2 py-0.5 text-xs rounded bg-emerald-500/10 text-emerald-300">{machineName || "—"}</span>
      </td>
      <td className="px-4 py-3">
        {rt.status === "absent" ? (
          <span className="px-2 py-0.5 text-xs rounded bg-amber-500/10 text-amber-300">CLI 已不在机器上</span>
        ) : (
          <span className="px-2 py-0.5 text-xs rounded bg-surface-0 text-ink-3">active</span>
        )}
      </td>
      <td className="px-4 py-3 text-ink-2 font-mono text-xs">{(rt.args ?? []).join(" ")}</td>
      <td className="px-4 py-3 text-ink-3">{new Date(rt.created_at).toLocaleString()}</td>
      <td className="px-4 py-3 text-right">
        {confirming ? (
          <span className="text-xs space-x-2">
            <button
              className="text-red-400 hover:text-red-300 font-medium"
              onClick={() => { onDelete(rt.id); setConfirming(false); }}
            >
              确认删除
            </button>
            <button className="text-ink-3 hover:text-ink-2" onClick={() => setConfirming(false)}>取消</button>
          </span>
        ) : (
          <button
            className="text-xs text-ink-3 hover:text-red-400 transition-colors"
            onClick={() => setConfirming(true)}
          >
            删除
          </button>
        )}
      </td>
    </tr>
  );
}

export default function RuntimesPage() {
  useGoalEvents();
  const { data: runtimes, isLoading } = useRuntimes();
  const { data: machines } = useMachines();
  const del = useDeleteRuntime();

  const machineName = (id: string) => machines?.find((m) => m.id === id)?.name ?? id;

  return (
    <div className="p-6 max-w-5xl mx-auto">
      <PageHeader title="Runtime" />
      <p className="text-sm text-ink-3 -mt-3 mb-4">
        Runtime 由远端机器上的 `agentwork connect` 探测自动生成（&lt;cli&gt;@&lt;机器名&gt;）——不需要手工创建。
      </p>

      {isLoading ? (
        <p className="text-ink-3 text-sm">加载中…</p>
      ) : !runtimes?.length ? (
        <Empty>
          还没有 Runtime——在机器上运行 `agentwork connect`，探测到的 agent CLI 会自动注册到这里。
        </Empty>
      ) : (
        <div className="bg-surface-1 rounded-2xl border border-line/80 shadow-sm overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="text-left text-xs text-ink-3 border-b border-line">
                <th className="px-4 py-2.5 font-medium">名称</th>
                <th className="px-4 py-2.5 font-medium">机器</th>
                <th className="px-4 py-2.5 font-medium">状态</th>
                <th className="px-4 py-2.5 font-medium">启动参数（acp_spawn）</th>
                <th className="px-4 py-2.5 font-medium">创建时间</th>
                <th className="px-4 py-2.5 font-medium text-right">操作</th>
              </tr>
            </thead>
            <tbody>
              {runtimes.map((rt) => (
                <RuntimeRow key={rt.id} rt={rt} machineName={machineName(rt.machine_id)} onDelete={(id) => del.mutate(id)} />
              ))}
            </tbody>
          </table>
        </div>
      )}

      {del.isError && <p className="mt-2 text-sm text-red-400">{String(del.error)}</p>}
    </div>
  );
}
