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

// ── 七类瓶颈 / 严重度 / 置信度 ──
// category 是后端 rsi_analyze.go 的规则 id；未知值原样显示，
// 避免后端新增一类时页面显示空白。旧短名同时保留兼容。
export const CATEGORY_LABELS: Record<string, string> = {
  tool_slow: "工具慢",
  llm_slow: "LLM 慢",
  retries: "失败重试",
  context_pressure: "上下文压力",
  duplicate_calls: "重复调用",
  serial_dependency: "串行依赖",
  human_wait: "人工等待",
  // 兼容更早的短枚举
  tool: "工具",
  llm: "LLM",
  retry: "重试",
  context: "上下文",
  duplicate: "重复调用",
  serial: "串行",
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

export const SPAN_STATUS_LABEL: Record<string, string> = {
  ok: "正常",
  error: "错误",
  cancelled: "已取消",
};