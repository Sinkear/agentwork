"use client";

// 管家未就绪时的说明 + 手动创建入口。
//
// 背景：管家的自动创建是 hwcloud-only（见 internal/service/agent.go 的
// StewardSeedCLIName）。机器上只有 opencode/claude 等 CLI 时，平台不会自动
// 建管家，但旧 UI 只显示「连接一台机器并重启服务后自动创建」，用户照做也
// 不会有反应。这里把真实原因说清楚，并给一个「立即创建」按钮：绑定当前
// 可用的运行时（优先 hwcloud），走与「智能体」页相同的创建接口。
//
// 手动创建的管家不会被自动改绑；只有它绑定的运行时失效时，后台的
// EnsureStewardRuntime 才会按 hwcloud 优先重新寻找。

import { useState } from "react";
import Link from "next/link";
import { Bot, Loader2, Plus } from "lucide-react";
import { Button } from "@/components/ui";
import { useAgents, useCreateAgent, useMachines, useRuntimes } from "@/lib/queries";
import type { Machine } from "@/lib/types";
import { cn } from "@/lib/utils";

interface ProbedCLI {
  name?: string;
  version?: string;
}

function probedCLIs(m: Machine): ProbedCLI[] {
  try {
    const v = JSON.parse(m.probed_clis || "[]");
    return Array.isArray(v) ? v : [];
  } catch {
    return [];
  }
}

export function StewardSetup({
  onCreated,
  className,
}: {
  onCreated?: (id: string) => void;
  className?: string;
}) {
  const { data: agents, isLoading: agentsLoading } = useAgents();
  const { data: machines } = useMachines();
  const { data: runtimes } = useRuntimes();
  const create = useCreateAgent();
  const [error, setError] = useState("");

  const hasSteward = (agents ?? []).some((a) => a.type === "steward");
  if (agentsLoading || hasSteward) return null;

  const online = (machines ?? []).filter((m) => m.status === "connected");
  const detected = online.flatMap((m) =>
    probedCLIs(m).map((c) => ({ name: c.name ?? "未知 CLI", version: c.version ?? "", machine: m.name }))
  );
  const hasHwcloud = detected.some((c) => c.name === "hwcloud");
  const detectedLabel = Array.from(
    new Set(detected.map((c) => c.version || c.name))
  ).join("、");

  const connectedIDs = new Set(online.map((m) => m.id));
  const usable = (runtimes ?? []).filter(
    (r) => r.status === "active" && connectedIDs.has(r.machine_id)
  );
  const candidate = usable.find((r) => r.name.startsWith("hwcloud@")) ?? usable[0];

  let reason: string;
  if (online.length === 0) {
    reason =
      "还没有在线机器。先在目标机器上运行 agentwork connect；连接后如果那台机器装有 hwcloud CLI，管家会自动创建。";
  } else if (hasHwcloud) {
    reason = "已探测到 hwcloud，但管家还没有创建。重启 daemon 后会自动创建，也可以现在立即创建。";
  } else {
    reason = `管家只会自动绑定 hwcloud CLI；当前在线机器上探测到：${detectedLabel || "无可用 CLI"}（没有 hwcloud）。可以现在创建一个绑定现有运行时的管家，或在那台机器安装 hwcloud 后重启服务。`;
  }

  const createSteward = () => {
    if (!candidate) return;
    setError("");
    create.mutate(
      {
        name: "管家",
        type: "steward",
        description: "",
        runtime_id: candidate.id,
        system_prompt: "",
        model: "",
        env: {},
        mcp_servers: [],
        skills: [],
        max_concurrent: 3,
      },
      {
        onSuccess: (agent) => onCreated?.(agent.id),
        onError: (e) => setError(`创建失败：${(e as Error).message}`),
      }
    );
  };

  return (
    <div
      className={cn(
        "rounded-lg border border-amber-500/30 bg-amber-500/5 p-3.5 text-left",
        className
      )}
    >
      <div className="flex items-center gap-2">
        <Bot className="h-4 w-4 shrink-0 text-amber-400" />
        <p className="text-sm font-medium text-ink">管家未就绪</p>
      </div>
      <p className="mt-1.5 text-xs leading-relaxed text-ink-2">{reason}</p>
      <div className="mt-3 flex flex-wrap items-center gap-2">
        <Button
          onClick={createSteward}
          disabled={!candidate || create.isPending}
          className="gap-1.5"
        >
          {create.isPending ? (
            <Loader2 className="h-3.5 w-3.5 animate-spin" />
          ) : (
            <Plus className="h-3.5 w-3.5" />
          )}
          立即创建管家
        </Button>
        <Link
          href="/agents"
          className="text-[11px] text-ink-3 transition-colors hover:text-ink"
        >
          去智能体页手动配置 →
        </Link>
      </div>
      {candidate ? (
        <p className="mt-2 text-[11px] text-ink-3">将绑定运行时：{candidate.name}</p>
      ) : (
        <p className="mt-2 text-[11px] text-ink-3">
          暂时没有可用的在线运行时——先连接一台机器，再点这里创建。
        </p>
      )}
      {error && <p className="mt-2 text-[11px] text-red-300">{error}</p>}
    </div>
  );
}