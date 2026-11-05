# RSI：训练模式与训练分析

RSI（Recursive Self-Improvement，能力自迭代）把 agentwork 的一次 goal 执行变成可统计、可归因的 trace，并在任务结束后异步产出优化建议。**这里不训练模型权重**，训练的是 harness：prompt、上下文、工具策略、流程编排与验证器。

## 使用

1. 新建任务时选择运行模式：
   - **推理**（默认，`execution_mode=infer`）：行为与以前完全一致，不采集 trace。
   - **训练**（`execution_mode=train`）：整个 goal 生命周期采集 trace；goal 进入 `done` 或 `failed` 后异步分析。
2. 训练高级项：
   - `content_capture`：`none` / `redacted`（默认，仅参数长度与 hash）/ `full`（工具输入输出前 8KB）。
   - `budget_usd`：会话预算上限（默认 2，当前只记录与展示，超过后不强制中止）。
   - `sample_rate`：采样率，`0 < rate <= 1`；未命中采样时会话标记 `skipped`。
3. 左侧「训练分析」(`/training`) 查看全部会话；点进详情看概览、发现与 Trace 瀑布。

## 数据模型

| 表 | 作用 |
| --- | --- |
| `training_session` | 一次训练会话，与 goal 一对一；状态机 `waiting → collecting → analyzing → ready / failed / skipped` |
| `trace_span` | 宽表 span：`run / tool / llm / verify / gate / human_wait`，记录起止、状态、重试、tokens、成本、上下文与内容摘要 |
| `rsi_report` | 每次分析的指标、基线、摘要 |
| `rsi_finding` | 规则发现：类别、严重度、置信度、证据、建议、确认状态 |
| `rsi_metric_snapshot` | 会话指标快照，供趋势对比 |

`goal` 增加 `execution_mode / content_capture / budget_usd / sample_rate`；迁移是 best-effort `ALTER TABLE`，老库自动补列。

## 指标口径

- `wall_ms`：会话墙钟时间；`active_ms`：各 run 起止并集（排除人工等待与串行空档）。
- `lead_time_ms`：goal 创建到终态。
- `tool_share` / `llm_share`：工具/模型耗时占 active 的比例。
- `human_wait_share`：`gate_decision` 的人工等待时长占比。
- `serial_share`：`run_sum_ms / wall_ms`，越高说明越串行。
- `retry_amplification`：总 run 尝试数 / 不同 run 数。
- `duplicate_calls`：同一 `(tool, args_hash)` 重复调用的次数与占比。
- `context_peak_ratio`：最大上下文占用 / 窗口大小。
- 基线：最近 20 个 `ready` 会话的中位数；样本少于 5 时不给 delta。

## 可优化判定

规则发现（L1，人工复核；同类历史 ≥2 次升 L2）：

| 类别 | 触发条件 |
| --- | --- |
| `tool_slow` | 工具耗时占比 ≥ 0.30 |
| `llm_slow` | 模型耗时占比 ≥ 0.45 |
| `retries` | 重试放大 ≥ 1.2，或失败 run ≥ 1 |
| `context_pressure` | 上下文峰值 ≥ 0.70 |
| `duplicate_calls` | 重复调用 ≥ 2 且占比 ≥ 0.15 |
| `serial_dependency` | 多个 run、串行占比 ≥ 0.70、active/wall ≥ 0.5 |
| `human_wait` | 人工等待占比 ≥ 0.15 |

每条发现都给出证据与建议。是否「可优化」仍需满足：效应显著、可归因、可干预、可回滚。M0 只读建议，不自动改代码。

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/rsi/sessions` | 会话列表（裸数组） |
| GET | `/rsi/sessions/{id}` | `{session, report, findings, spans}` |
| GET | `/rsi/overview?days=30` | 总览：会话数、成功率、单成功成本、P95 交付时长、人工等待、发现数 |
| PATCH | `/rsi/findings/{id}` | body `{"status":"confirmed"}` 或 `dismissed` |
| POST | `/rsi/sessions/{id}/retry` | 重新分析 |

## 采集与隐私

- 默认 `redacted`：只落工具名、参数 hash、长度、状态与耗时，不落正文。
- `full` 才落 8KB 预览，且建议只在本地/可信环境使用。
- 训练会话与推理会话指标分开统计，避免观测者效应。

## 限制

- 不训练模型权重；分析器目前是确定性规则 + 统计，不含 LLM 归因（M2）。
- token 用量依赖 ACP `usage_update`；上游不提供时为 0，`usage_available=false`。
- `budget_usd` 仅展示，超预算不会中断执行。
- `overview` 的人工等待暂未按 `days` 过滤。
- 无自动应用/回滚通道；改动需人工确认并另行 A/B 复测。

## 测试

```sh
go build ./...
go test ./...
cd web && npx tsc --noEmit && npm run build
```