"use client";

import { useId, useMemo, useRef, useState } from "react";
import {
  Bot,
  Eye,
  GitMerge,
  Maximize2,
  MessageSquare,
  ShieldCheck,
  Target,
  Waypoints,
  ZoomIn,
  ZoomOut,
} from "lucide-react";
import type { LucideIcon } from "lucide-react";
import type { WorkflowEdge, WorkflowGraph, WorkflowNode } from "@/lib/types";
import { cn } from "@/lib/utils";
import {
  EDGE_KIND_LABEL,
  DASH,
  fmtInt,
  fmtMs,
  fmtRatio,
  metricNum,
  nodeKindMeta,
} from "./format";

// 只读 DAG 的分层布局。不引第三方图库：
// 横向按拓扑层排列，纵向同层横排；坐标是固定的 CSS 像素，连线用 SVG 画。
const NODE_W = 196;
const NODE_H = 82;
const GAP_X = 58;
const GAP_Y = 16;
const COL_W = NODE_W + GAP_X;
const ROW_H = NODE_H + GAP_Y;

const KIND_ICON: Record<string, LucideIcon> = {
  goal: Target,
  agent: Bot,
  verify: ShieldCheck,
  review: Eye,
  consult: MessageSquare,
  integrate: GitMerge,
};

interface LaidOut {
  node: WorkflowNode;
  layer: number;
  top: number;
  x: number;
}

interface Layout {
  width: number;
  height: number;
  nodes: LaidOut[];
  byKey: Map<string, LaidOut>;
  maxLayer: number;
  maxRow: number;
}

function sortNodes(a: WorkflowNode, b: WorkflowNode): number {
  const ta = Number.isFinite(a.started_at_ms) ? a.started_at_ms : 0;
  const tb = Number.isFinite(b.started_at_ms) ? b.started_at_ms : 0;
  if (ta !== tb) return ta - tb;
  return a.key.localeCompare(b.key);
}

/**
 * 拓扑分层：所有边都参与（不只是 depends_on/seq），create 边把 agent 放到
 * goal 下一层，verified_by/consult/integrated 把 verify 等节点放到其来源之后。
 * 层号取最长路径（Kahn），这样依赖链不会“抄近道”排到同一层。
 * 图里若有时序环（理论上不应有），剩余节点追加到最深层，页面不退化成空白。
 */
function computeLayout(graph: WorkflowGraph): Layout {
  const nodes = (graph.nodes ?? []).filter((n) => n && typeof n.key === "string");
  const byKeyRaw = new Map(nodes.map((n) => [n.key, n]));
  const edges = (graph.edges ?? []).filter(
    (e) => byKeyRaw.has(e.from) && byKeyRaw.has(e.to) && e.from !== e.to
  );

  const indeg = new Map<string, number>();
  const out = new Map<string, string[]>();
  for (const n of nodes) {
    indeg.set(n.key, 0);
    out.set(n.key, []);
  }
  for (const e of edges) {
    indeg.set(e.to, (indeg.get(e.to) ?? 0) + 1);
    out.get(e.from)?.push(e.to);
  }

  const layer = new Map<string, number>();
  const queue: string[] = [];
  for (const n of nodes) if ((indeg.get(n.key) ?? 0) === 0) queue.push(n.key);
  // 队列按开始时间取，保证同层输出稳定。
  queue.sort((a, b) => sortNodes(byKeyRaw.get(a)!, byKeyRaw.get(b)!));

  let head = 0;
  while (head < queue.length) {
    const key = queue[head++];
    const base = layer.get(key) ?? 0;
    for (const to of out.get(key) ?? []) {
      layer.set(to, Math.max(layer.get(to) ?? 0, base + 1));
      indeg.set(to, (indeg.get(to) ?? 1) - 1);
      if ((indeg.get(to) ?? 0) === 0) queue.push(to);
    }
  }

  // 环保护：没被拓扑排序覆盖的节点接到当前最深层后面。
  let maxLayer = 0;
  for (const v of layer.values()) maxLayer = Math.max(maxLayer, v);
  for (const n of nodes) {
    if (!layer.has(n.key)) {
      layer.set(n.key, maxLayer + 1);
      maxLayer += 1;
    }
  }

  // review 节点在图上没有结构边（后端只建 agent/verify/consult/integrate 的边）。
  // 纯按拓扑会把它们扔到和 goal 同一列，看起来像“审查先于执行”。把它们放到
  // 当前最右列：不改后端图，只修正展示层对孤立节点的理解。
  const linked = new Set<string>();
  for (const e of edges) {
    linked.add(e.from);
    linked.add(e.to);
  }
  const lastLayer = Math.max(1, maxLayer);
  for (const n of nodes) {
    if (!linked.has(n.key) && n.kind !== "goal") layer.set(n.key, lastLayer);
  }
  maxLayer = Math.max(maxLayer, lastLayer);

  const groups = new Map<number, WorkflowNode[]>();
  for (const n of nodes) {
    const l = layer.get(n.key) ?? 0;
    if (!groups.has(l)) groups.set(l, []);
    groups.get(l)!.push(n);
  }
  for (const arr of groups.values()) arr.sort(sortNodes);

  const maxRow = Math.max(1, ...Array.from(groups.values(), (a) => a.length));
  const height = maxRow * ROW_H - GAP_Y;
  const width = (maxLayer + 1) * COL_W - GAP_X;

  const laidOut: LaidOut[] = [];
  for (const [l, arr] of groups) {
    const offset = ((maxRow - arr.length) * ROW_H) / 2;
    arr.forEach((node, i) => {
      laidOut.push({
        node,
        layer: l,
        top: offset + i * ROW_H,
        x: l * COL_W,
      });
    });
  }

  return {
    width,
    height,
    nodes: laidOut,
    byKey: new Map(laidOut.map((n) => [n.node.key, n])),
    maxLayer,
    maxRow,
  };
}

function nodeStatusDot(status: string): string {
  switch (status) {
    case "done":
    case "ready":
    case "approved":
      return "bg-emerald-400";
    case "failed":
    case "rejected":
    case "error":
      return "bg-red-400";
    case "active":
    case "review":
    case "reviewing":
      return "bg-blue-400";
    default:
      return "bg-ink-3";
  }
}

function edgePath(from: LaidOut, to: LaidOut): string {
  const sx = from.x + NODE_W;
  const sy = from.top + NODE_H / 2;
  const tx = to.x;
  const ty = to.top + NODE_H / 2;
  const dx = Math.max(24, (tx - sx) / 2);
  return `M ${sx} ${sy} C ${sx + dx} ${sy}, ${tx - dx} ${ty}, ${tx} ${ty}`;
}

function NodeCard({
  laid,
  critical,
  criticalPath,
}: {
  laid: LaidOut;
  critical: boolean;
  criticalPath: boolean;
}) {
  const { node } = laid;
  const meta = nodeKindMeta(node.kind);
  const Icon = KIND_ICON[node.kind] ?? Waypoints;
  const showAttempt = node.attempt > 0;
  const tooltip = [
    `${node.label || node.key}（${meta.label}）`,
    node.status ? `状态：${node.status}` : "",
    `耗时：${fmtMs(Number.isFinite(node.duration_ms) ? node.duration_ms : null)}`,
    node.assignee_id ? `执行者：${node.assignee_id}` : "",
    node.attempt > 0 ? `尝试：${node.attempt}` : "",
    node.reworks > 0 ? `返工：${node.reworks}` : "",
    Number.isFinite(node.critical_share) && node.critical_share > 0
      ? `关键路径占比：${fmtRatio(node.critical_share, 0)}`
      : "",
    node.key,
  ]
    .filter(Boolean)
    .join("\n");

  return (
    <div
      className={cn(
        "absolute overflow-hidden rounded-lg border bg-surface-1 px-2.5 py-2 shadow-sm",
        meta.ring,
        critical && "border-accent/70 bg-accent/[0.06] ring-1 ring-accent/25",
        criticalPath && !critical && "border-accent/40"
      )}
      style={{ left: laid.x, top: laid.top, width: NODE_W, height: NODE_H }}
      title={tooltip}
    >
      <div className="flex items-center gap-1.5">
        <span
          className={cn(
            "inline-flex shrink-0 items-center gap-1 rounded border px-1.5 py-0.5 text-[10px] font-medium",
            meta.badge
          )}
        >
          <Icon className="h-3 w-3" />
          {meta.label}
        </span>
        <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", nodeStatusDot(node.status))} />
        <span className="ml-auto shrink-0 font-mono text-[10px] tabular-nums text-ink-3">
          {fmtMs(Number.isFinite(node.duration_ms) ? node.duration_ms : null)}
        </span>
      </div>
      <div className="mt-1.5 truncate text-xs font-medium text-ink" title={node.label}>
        {node.label || node.key}
      </div>
      <div className="mt-1 flex items-center gap-2 text-[10px] text-ink-3">
        {showAttempt && <span>尝试 {node.attempt}</span>}
        {node.reworks > 0 && (
          <span className={node.reworks > 0 ? "text-amber-300" : undefined}>
            返工 {node.reworks}
          </span>
        )}
        {!showAttempt && node.reworks <= 0 && (
          <span className="font-mono" title={node.key}>
            {node.key.length > 22 ? `${node.key.slice(0, 22)}…` : node.key}
          </span>
        )}
        {critical && (
          <span className="ml-auto shrink-0 font-mono text-accent">
            关键 {fmtRatio(node.critical_share, 0)}
          </span>
        )}
      </div>
    </div>
  );
}

function EdgeLine({
  edge,
  from,
  to,
  critical,
  markerBase,
  markerCritical,
}: {
  edge: WorkflowEdge;
  from: LaidOut;
  to: LaidOut;
  critical: boolean;
  markerBase: string;
  markerCritical: string;
}) {
  const inferredSeq = edge.kind === "seq" && edge.source === "inferred";
  return (
    <path
      d={edgePath(from, to)}
      fill="none"
      stroke={critical ? "#22c55e" : "#71717a"}
      strokeWidth={critical ? 2.2 : 1}
      strokeOpacity={critical ? 0.9 : 0.45}
      strokeDasharray={inferredSeq ? "4 4" : edge.source === "inferred" ? "2 3" : undefined}
      markerEnd={`url(#${critical ? markerCritical : markerBase})`}
    />
  );
}

export function WorkflowMetricsRow({
  metrics,
}: {
  metrics: Record<string, unknown> | null | undefined;
}) {
  const m = metrics ?? {};
  const items: { label: string; value: string; tone?: string }[] = [
    {
      label: "并行加速",
      value: fmtSpeedupSafe(metricNum(m, "parallel_speedup")),
      tone: "accent",
    },
    { label: "关键路径", value: fmtMs(metricNum(m, "critical_path_ms")) },
    { label: "串行损失", value: fmtMs(metricNum(m, "serialization_loss_ms")) },
    { label: "验证开销", value: fmtRatio(metricNum(m, "verify_overhead_ratio")) },
    { label: "返工率", value: fmtRatio(metricNum(m, "rework_ratio")) },
    { label: "咨询次数", value: fmtInt(metricNum(m, "consult_count")) },
    { label: "排队等待", value: fmtMs(metricNum(m, "queue_wait_ms")) },
    {
      label: "分派失衡",
      value: fmtLaneImbalance(metricNum(m, "lane_imbalance")),
    },
  ];

  return (
    <div className="grid grid-cols-2 gap-2 sm:grid-cols-4">
      {items.map((it) => (
        <div
          key={it.label}
          className="rounded-md border border-line bg-surface-2/30 px-2.5 py-2"
        >
          <div className="font-mono text-[10px] uppercase tracking-[0.1em] text-ink-3">
            {it.label}
          </div>
          <div
            className={cn(
              "mt-1 font-mono text-sm tabular-nums",
              it.tone === "accent" ? "text-accent" : "text-ink-2"
            )}
          >
            {it.value}
          </div>
        </div>
      ))}
    </div>
  );
}

function fmtSpeedupSafe(v: number | null): string {
  if (v === null) return DASH;
  return `${v.toFixed(2)}×`;
}

function fmtLaneImbalance(v: number | null): string {
  if (v === null) return DASH;
  // lane_imbalance = 最忙 lane / 平均 lane，>=1 才有意义；不是 0..1 比例。
  return `${v.toFixed(2)}×`;
}

export function WorkflowGraphView({
  graph,
  loading,
  error,
  metrics,
  analyzing = false,
  heightClass = "min-h-[260px]",
  showMetrics = true,
}: {
  graph: WorkflowGraph | null | undefined;
  loading?: boolean;
  error?: string | null;
  /** 图不可用时（分析失败/无图）仍可展示报告里的 metrics。 */
  metrics?: Record<string, unknown> | null;
  /** 分析进行中：此时图是实时图，分析完成后才冻结，文案不能写“已冻结”。 */
  analyzing?: boolean;
  heightClass?: string;
  showMetrics?: boolean;
}) {
  const uid = useId().replace(/[:]/g, "");
  const markerBase = `wf-arrow-${uid}`;
  const markerCritical = `wf-arrow-c-${uid}`;
  // 30 节点链宽度可达 7.5k px，直接铺开会把整页撑爆：限制容器高度并允许双向滚动，
  // 再提供缩放/适应宽度，默认 1×。
  const scrollRef = useRef<HTMLDivElement | null>(null);
  const [zoom, setZoom] = useState(1);
  const MIN_ZOOM = 0.15;
  const MAX_ZOOM = 2;

  const layout = useMemo(() => (graph && (graph.nodes?.length ?? 0) > 0 ? computeLayout(graph) : null), [graph]);
  const graphMetrics = graph?.metrics ?? metrics ?? null;
  const clampZoom = (z: number) => Math.min(MAX_ZOOM, Math.max(MIN_ZOOM, z));
  const zoomBy = (delta: number) => setZoom((z) => clampZoom(Number((z + delta).toFixed(2))));
  const fitWidth = () => {
    const el = scrollRef.current;
    if (!el || !layout?.width) return;
    // 留出左右 padding（px-4 = 16px × 2）和一点滚动条余量。
    setZoom(clampZoom(Number(((el.clientWidth - 40) / layout.width).toFixed(2))));
  };

  if (loading) {
    return (
      <div className={cn("space-y-3 p-4", heightClass)}>
        <div className="flex gap-3">
          {[0, 1, 2].map((i) => (
            <div key={i} className="h-[82px] flex-1 animate-pulse rounded-lg bg-surface-2" />
          ))}
        </div>
        <div className="h-16 animate-pulse rounded-md bg-surface-2/60" />
      </div>
    );
  }

  if (error) {
    return (
      <div className="space-y-3 p-4">
        <p className="rounded-md border border-red-500/25 bg-red-500/10 px-3 py-2 font-mono text-[11px] leading-relaxed text-red-300">
          编排图读取失败：{error}
        </p>
        {showMetrics && <WorkflowMetricsRow metrics={graphMetrics} />}
      </div>
    );
  }

  if (!layout) {
    return (
      <div className="space-y-3 p-4">
        <div
          className={cn(
            "flex flex-col items-center justify-center gap-2 rounded-lg border border-dashed border-line px-4 py-10 text-center",
            heightClass
          )}
        >
          <Waypoints className="h-5 w-5 text-ink-3" />
          <p className="text-sm text-ink-2">还没有可展示的编排图</p>
          <p className="max-w-md text-xs leading-relaxed text-ink-3">
            训练会话进入分析、任务进入终态并完成分析后，这里会展示冻结的编排 DAG；
            分析失败或任务尚未拆解时为空。
          </p>
        </div>
        {showMetrics && <WorkflowMetricsRow metrics={graphMetrics} />}
      </div>
    );
  }

  const cpSet = new Set(graph?.critical_path ?? []);
  const nodesByKey = layout.byKey;
  const maxShare = Math.max(
    0,
    ...layout.nodes.map((n) => (Number.isFinite(n.node.critical_share) ? n.node.critical_share : 0))
  );

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 px-4 text-[11px] text-ink-3">
        <span className="inline-flex items-center gap-1 text-ink-2">
          <Waypoints className="h-3.5 w-3.5" />
          {analyzing
            ? graph?.frozen
              ? "上次分析快照（分析中）"
              : "实时编排图（分析中）"
            : graph?.frozen
              ? "已冻结编排图"
              : "实时编排图"}
        </span>
        {analyzing && (
          <span className="text-ink-3">
            {graph?.frozen
              ? "重跑中，完成后更新为本次快照"
              : "分析完成后才会冻结为只读快照"}
          </span>
        )}
        <span>
          节点 {fmtInt(layout.nodes.length)} · 边 {fmtInt((graph?.edges ?? []).length)} · 深度{" "}
          {fmtInt(layout.maxLayer + 1)}
        </span>
        {graph?.generated_at && <span>生成于 {graph.generated_at}</span>}
        {cpSet.size > 0 && (
          <span className="inline-flex items-center gap-1 text-accent">
            <span className="h-1.5 w-3.5 rounded-sm bg-accent" />
            关键路径
          </span>
        )}
        <span className="inline-flex items-center gap-1">
          <span className="h-0.5 w-3.5 bg-ink-3/60" />
          依赖
        </span>
        <span className="inline-flex items-center gap-1">
          <span className="h-0.5 w-3.5 border-t border-dashed border-ink-3/60" />
          推断时序
        </span>
        {maxShare > 0 && (
          <span className="text-ink-3">节点右上角为耗时，关键路径节点显示占比</span>
        )}
        <div className="ml-auto inline-flex items-center gap-1.5">
          <button
            type="button"
            onClick={() => zoomBy(-0.25)}
            disabled={zoom <= MIN_ZOOM}
            title="缩小"
            aria-label="缩小编排图"
            className="inline-flex h-6 w-6 items-center justify-center rounded border border-line text-ink-3 transition-colors hover:text-ink-2 disabled:opacity-40"
          >
            <ZoomOut className="h-3.5 w-3.5" />
          </button>
          <span className="w-10 text-center font-mono tabular-nums text-ink-2">
            {Math.round(zoom * 100)}%
          </span>
          <button
            type="button"
            onClick={() => zoomBy(0.25)}
            disabled={zoom >= MAX_ZOOM}
            title="放大"
            aria-label="放大编排图"
            className="inline-flex h-6 w-6 items-center justify-center rounded border border-line text-ink-3 transition-colors hover:text-ink-2 disabled:opacity-40"
          >
            <ZoomIn className="h-3.5 w-3.5" />
          </button>
          <button
            type="button"
            onClick={fitWidth}
            title="缩放到容器宽度"
            className="inline-flex h-6 items-center gap-1 rounded border border-line px-1.5 text-ink-3 transition-colors hover:text-ink-2"
          >
            <Maximize2 className="h-3 w-3" />
            适应宽度
          </button>
          <button
            type="button"
            onClick={() => setZoom(1)}
            title="恢复 1:1"
            className="h-6 rounded border border-line px-1.5 text-ink-3 transition-colors hover:text-ink-2"
          >
            1:1
          </button>
        </div>
      </div>

      <div ref={scrollRef} className="max-h-[72vh] max-w-full overflow-auto px-4 pb-1">
        <div
          className="relative"
          style={{ width: layout.width * zoom, height: layout.height * zoom }}
        >
          <div
            className="relative"
            style={{
              width: layout.width,
              height: layout.height,
              transform: `scale(${zoom})`,
              transformOrigin: "top left",
            }}
          >
          <svg
            width={layout.width}
            height={layout.height}
            className="pointer-events-none absolute inset-0"
            aria-hidden="true"
          >
            <defs>
              <marker
                id={markerBase}
                viewBox="0 0 8 8"
                refX="7"
                refY="4"
                markerWidth="6"
                markerHeight="6"
                orient="auto"
              >
                <path d="M 0 0 L 8 4 L 0 8 z" fill="#71717a" />
              </marker>
              <marker
                id={markerCritical}
                viewBox="0 0 8 8"
                refX="7"
                refY="4"
                markerWidth="6"
                markerHeight="6"
                orient="auto"
              >
                <path d="M 0 0 L 8 4 L 0 8 z" fill="#22c55e" />
              </marker>
            </defs>
            {(graph?.edges ?? []).map((e, i) => {
              const from = nodesByKey.get(e.from);
              const to = nodesByKey.get(e.to);
              if (!from || !to) return null;
              const critical = criticalEdge(e, from, to, cpSet);
              return (
                <EdgeLine
                  key={`${e.from}-${e.to}-${e.kind}-${i}`}
                  edge={e}
                  from={from}
                  to={to}
                  critical={critical}
                  markerBase={markerBase}
                  markerCritical={markerCritical}
                />
              );
            })}
          </svg>

          {layout.nodes.map((laid) => {
            const share = Number.isFinite(laid.node.critical_share)
              ? laid.node.critical_share
              : 0;
            return (
              <NodeCard
                key={laid.node.key}
                laid={laid}
                critical={cpSet.has(laid.node.key) || share > 0.5}
                criticalPath={cpSet.has(laid.node.key)}
              />
            );
          })}
          </div>
        </div>
      </div>

      {cpSet.size > 0 && (
        <div className="flex flex-wrap items-center gap-1.5 px-4 pb-1 text-[10px] text-ink-3">
          <span className="shrink-0">关键路径：</span>
          {graph!.critical_path.map((key, i) => {
            const n = nodesByKey.get(key)?.node;
            return (
              <span key={`${key}-${i}`} className="inline-flex items-center gap-1.5">
                {i > 0 && <span className="text-ink-3/60">→</span>}
                <span className="rounded border border-accent/25 bg-accent/[0.06] px-1.5 py-0.5 text-accent">
                  {n?.label || key}
                </span>
              </span>
            );
          })}
        </div>
      )}

      {showMetrics && <div className="px-4 pb-1"><WorkflowMetricsRow metrics={graphMetrics} /></div>}

      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 border-t border-line px-4 py-2 text-[10px] text-ink-3">
        {Object.entries(EDGE_KIND_LABEL).map(([kind, label]) => (
          <span key={kind}>
            <span className="font-mono">{kind}</span> {label}
          </span>
        ))}
      </div>
    </div>
  );
}

function criticalEdge(
  edge: WorkflowEdge,
  from: LaidOut,
  to: LaidOut,
  cpSet: Set<string>
): boolean {
  if (cpSet.has(edge.from) && cpSet.has(edge.to)) return true;
  const fa = Number.isFinite(from.node.critical_share) ? from.node.critical_share : 0;
  const ta = Number.isFinite(to.node.critical_share) ? to.node.critical_share : 0;
  return fa > 0 && ta > 0;
}