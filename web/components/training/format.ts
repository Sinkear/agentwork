// 训练分析展示层的共用格式化。
//
// 后端 metrics / baseline / attrs 都是 map[string]any（直接序列化的 Go 结构体），
// 字段可能缺失、为 null、或是字符串化的数字。统一原则：读不到就显示「—」，
// 不猜、不把 0 当成有值。比例字段按 0..1 读取，后端若直接下发百分数也能显示。

export const DASH = "—";

/** 把 unknown 收窄成有限数字；其余（null/undefined/""/NaN/对象）一律 null。 */
export function asNum(v: unknown): number | null {
  if (typeof v === "number") return Number.isFinite(v) ? v : null;
  if (typeof v === "string" && v.trim() !== "") {
    const n = Number(v);
    return Number.isFinite(n) ? n : null;
  }
  return null;
}

/** 按候选键顺序从 metrics 对象取值（后端键名可能随版本微调）。 */
export function metricNum(
  metrics: Record<string, unknown> | null | undefined,
  ...keys: string[]
): number | null {
  if (!metrics) return null;
  for (const key of keys) {
    const n = asNum(metrics[key]);
    if (n !== null) return n;
  }
  return null;
}

export function fmtInt(v: number | null): string {
  if (v === null) return DASH;
  return Math.round(v).toLocaleString("zh-CN");
}

/**
 * 把 0 / 负数 / 缺失都收窄成 null：这些值在训练分析里表示“还没有测量到”，
 * 而不是真实结果。展示层统一用 fmt* → 「—」，避免 waiting/skipped 的
 * 0 节点 / 0ms / 0.00× 被误读成真实指标。
 */
export function measured(v: unknown): number | null {
  const n = asNum(v);
  return n !== null && n > 0 ? n : null;
}

export function fmtUsd(v: number | null): string {
  if (v === null) return DASH;
  // 小钱（<$0.01）用 4 位，否则 2 位——$0.00 会让“有成本”看起来像免费。
  if (v > 0 && v < 0.01) return `$${v.toFixed(4)}`;
  return `$${v.toFixed(2)}`;
}

export function fmtMs(v: number | null): string {
  if (v === null) return DASH;
  if (v < 0) return DASH;
  if (v < 1000) return `${Math.round(v)}ms`;
  const s = v / 1000;
  if (s < 60) return `${s.toFixed(1)}s`;
  const m = Math.floor(s / 60);
  const rest = Math.round(s % 60);
  if (m < 60) return `${m}m${rest}s`;
  const h = Math.floor(m / 60);
  return `${h}h${m % 60}m`;
}

/**
 * 比例 → 百分比。契约是 0..1；但容忍后端直接给百分数（>1 且 ≤100），
 * 否则 45 会被显示成 4500% 这种一眼假的值。
 */
export function fmtRatio(v: number | null, digits = 1): string {
  if (v === null) return DASH;
  const pct = v > 1 && v <= 100 ? v : v * 100;
  return `${pct.toFixed(digits)}%`;
}

export function fmtBytes(v: number | null): string {
  if (v === null) return DASH;
  if (v < 1024) return `${Math.round(v)} B`;
  if (v < 1024 * 1024) return `${(v / 1024).toFixed(1)} KB`;
  return `${(v / 1024 / 1024).toFixed(1)} MB`;
}

export function fmtTime(iso: string): string {
  if (!iso) return DASH;
  const t = new Date(iso);
  if (Number.isNaN(t.getTime())) return iso;
  return t.toLocaleString("zh-CN", {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  });
}

/**
 * token 总量：优先读 metrics.tokens/tokens_total；
 * 没有再退回 tokens_in + tokens_out（能读到哪个算哪个）。
 */
export function totalTokens(metrics: Record<string, unknown> | null | undefined): number | null {
  const direct = metricNum(metrics, "tokens", "tokens_total");
  if (direct !== null) return direct;
  const tin = metricNum(metrics, "tokens_in");
  const tout = metricNum(metrics, "tokens_out");
  if (tin === null && tout === null) return null;
  return (tin ?? 0) + (tout ?? 0);
}

// ── 十类编排瓶颈 / 严重度 / 置信度 / 节点类型 ──
// category 是后端 rsi_analyze.go 的规则 id；未知值原样显示，
// 避免后端新增一类时页面显示空白。
export const CATEGORY_LABELS: Record<string, string> = {
  serial_chain: "串行链",
  split_candidate: "可拆分节点",
  lost_parallelism: "并行度损失",
  merge_candidate: "可合并节点",
  wrong_assignee: "分派失衡",
  ping_pong: "来回返工",
  brief_quality: "任务描述质量",
  consult_repeat: "重复咨询",
  retry_amplification: "重试放大",
  human_wait: "人工等待",
};

export function categoryLabel(c: string): string {
  if (!c) return "未分类";
  return CATEGORY_LABELS[c] ?? c;
}

export const SEVERITY_META: Record<string, { label: string; cls: string }> = {
  critical: { label: "高危", cls: "border-red-500/30 bg-red-500/10 text-red-300" },
  warn: { label: "中", cls: "border-amber-500/30 bg-amber-500/10 text-amber-300" },
  info: { label: "低", cls: "border-sky-500/30 bg-sky-500/10 text-sky-300" },
  // 兼容旧命名
  high: { label: "高危", cls: "border-red-500/30 bg-red-500/10 text-red-300" },
  medium: { label: "中", cls: "border-amber-500/30 bg-amber-500/10 text-amber-300" },
  low: { label: "低", cls: "border-sky-500/30 bg-sky-500/10 text-sky-300" },
};

export function severityMeta(s: string) {
  return SEVERITY_META[s] ?? { label: s || "未知", cls: "border-line bg-surface-2 text-ink-3" };
}

// 三级置信度（§4.3）：L1 单次观察 / L2 单 run 统计 / L3 跨 run 基线。
export const CONFIDENCE_META: Record<string, { label: string; desc: string }> = {
  L1: { label: "L1", desc: "单次观察：只说明这一次，不能外推" },
  L2: { label: "L2", desc: "单 run 统计：同一 run 内多次出现" },
  L3: { label: "L3", desc: "跨 run 基线：与同项目历史对比后仍然成立" },
};

export function confidenceMeta(c: string) {
  return CONFIDENCE_META[c] ?? { label: c || "—", desc: "未知置信度" };
}

export function fmtSpeedup(v: number | null): string {
  if (v === null) return DASH;
  // 并行加速比低于 1 说明关键路径比总工作量还长（数据异常）；原样显示，别修饰。
  return `${v.toFixed(2)}×`;
}

export const NODE_KIND_META: Record<
  string,
  { label: string; desc: string; badge: string; ring: string; bar: string }
> = {
  goal: {
    label: "目标",
    desc: "编排根节点",
    badge: "border-emerald-500/30 bg-emerald-500/10 text-emerald-300",
    ring: "border-emerald-500/40",
    bar: "bg-emerald-400",
  },
  agent: {
    label: "执行",
    desc: "子任务的一次执行（可含多轮 run）",
    badge: "border-blue-500/30 bg-blue-500/10 text-blue-300",
    ring: "border-blue-500/40",
    bar: "bg-blue-400",
  },
  verify: {
    label: "验证",
    desc: "质量验证轮次；rejected 表示有返工",
    badge: "border-amber-500/30 bg-amber-500/10 text-amber-300",
    ring: "border-amber-500/40",
    bar: "bg-amber-400",
  },
  review: {
    label: "审查",
    desc: "人工/自动审批",
    badge: "border-purple-500/30 bg-purple-500/10 text-purple-300",
    ring: "border-purple-500/40",
    bar: "bg-purple-400",
  },
  consult: {
    label: "咨询",
    desc: "运行中向其他 agent 发起的咨询",
    badge: "border-sky-500/30 bg-sky-500/10 text-sky-300",
    ring: "border-sky-500/40",
    bar: "bg-sky-400",
  },
  integrate: {
    label: "集成",
    desc: "变更集的集成节点",
    badge: "border-rose-500/30 bg-rose-500/10 text-rose-300",
    ring: "border-rose-500/40",
    bar: "bg-rose-400",
  },
};

export function nodeKindMeta(kind: string) {
  return (
    NODE_KIND_META[kind] ?? {
      label: kind || "节点",
      desc: "未知节点类型",
      badge: "border-line bg-surface-2 text-ink-3",
      ring: "border-line",
      bar: "bg-ink-3",
    }
  );
}

export const EDGE_KIND_LABEL: Record<string, string> = {
  created: "创建",
  depends_on: "依赖",
  seq: "时序（推断）",
  verified_by: "验证",
  consult: "咨询",
  integrated: "集成",
};
// ── 优化轮次 delta（W-RSI v3 insight panel） ──
// delta 的 pct 是相对变化率（(to-from)/from），不是百分点；from=0 时后端省略。
export const DELTA_METRIC_KEYS = [
  "parallel_speedup",
  "critical_path_ms",
  "serialization_loss_ms",
  "verify_overhead_ratio",
  "rework_ratio",
  "consult_count",
  "human_wait_ms",
] as const;

export const DELTA_LABELS: Record<string, string> = {
  parallel_speedup: "并行加速",
  critical_path_ms: "关键路径",
  serialization_loss_ms: "串行损失",
  verify_overhead_ratio: "验证开销",
  rework_ratio: "返工率",
  consult_count: "咨询次数",
  human_wait_ms: "人工等待",
};

// 越低越好的指标；parallel_speedup 越高越好，其余全部是成本/损耗类。
const LOWER_IS_BETTER = new Set([
  "critical_path_ms",
  "serialization_loss_ms",
  "verify_overhead_ratio",
  "rework_ratio",
  "consult_count",
  "human_wait_ms",
]);

export type DeltaTone = "improved" | "worsened" | "same";

/**
 * 方向语义：按“改善/恶化”判断，而不是数值涨跌。
 * 相对阈值 0.05% 用来吸收浮点噪声，避免 1.0001→1.0 被标成改善。
 */
export function deltaTone(key: string, from: number, to: number): DeltaTone {
  if (!Number.isFinite(from) || !Number.isFinite(to)) return "same";
  const rel = from === 0 ? Math.abs(to - from) : Math.abs((to - from) / from);
  if (rel < 0.0005) return "same";
  const up = to > from;
  const improved = key === "parallel_speedup" ? up : LOWER_IS_BETTER.has(key) ? !up : up;
  return improved ? "improved" : "worsened";
}

export function fmtDeltaValue(key: string, v: number): string {
  if (!Number.isFinite(v)) return DASH;
  if (key === "parallel_speedup") return fmtSpeedup(v);
  if (key === "consult_count") return fmtInt(v);
  if (key.endsWith("_ms")) return fmtMs(v);
  if (key.endsWith("_ratio")) return fmtRatio(v);
  return fmtInt(v);
}

/** 相对变化率 → 带符号百分比；pct 缺省返回空串（只显示 from→to）。 */
export function fmtDeltaPct(pct: number | null | undefined): string {
  const p = asNum(pct);
  if (p === null) return "";
  if (Math.abs(p) < 0.0005) return "0%";
  const sign = p > 0 ? "+" : "−";
  const abs = Math.abs(p) * 100;
  return `${sign}${abs >= 10 ? abs.toFixed(0) : abs.toFixed(1)}%`;
}

export const ROUND_TRIGGER_LABEL: Record<string, string> = {
  terminal: "终态分析",
  manual_retry: "手动复测",
};
