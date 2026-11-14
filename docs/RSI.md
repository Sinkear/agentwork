# W-RSI：工作流级训练模式与训练分析

W-RSI（Workflow-level Recursive Self-Improvement）把 agentwork 的一次 goal 执行过程冻结成一张**工作流编排图**（DAG），在任务结束后异步分析编排结构、分派与信息流，产出只读的优化提案与复测计划。**这里不训练模型权重**，也不改 agent harness 内部（prompt/工具/上下文策略由各 agent 自治）；优化对象是**编排层**：节点怎么拆、依赖怎么连、任务派给谁、信息怎么流转。

设计文档（论点、指标体系、Coze 教训、分期路线）见 `docs/research/rsi-summary.md`。

## 使用

1. 新建任务时选择运行模式：
   - **推理**（默认，`execution_mode=infer`）：行为与以前完全一致，零采集。
   - **训练**（`execution_mode=train`）：goal 终态（`done` / `failed`）时把全部编排事实冻结成 `workflow_json`，随后异步分析。
2. 训练高级项：
   - `content_capture`：`none` / `redacted`（默认）/ `full`。注意 v2 起图装配只依赖结构化事实（run/verification/consult/change/gate 行），`full` 不再采集工具正文，仅作为未来节级深挖的预留位。
   - `budget_usd`：会话预算上限（默认 2，当前只记录与展示，超过后不强制中止）。
   - `sample_rate`：采样率，`0 < rate <= 1`；未命中采样时会话标记 `skipped`。
3. 左侧「训练分析」(`/training`) 查看全部会话；点进详情看概览、发现与编排图。
4. **任务洞察面板**：「洞察」tab 展示**优化轮次账本**——goal 终态自动分析是第 1 轮（`terminal`），人工修完问题后点「重跑分析」产生复测轮（`manual_retry`），每轮冻结当时的指标/基线/发现，并给出与**同 domain 上一条 `rsi_round`**（可跨会话）的 **delta 对比**（加速比↑、返工率↓…即优化是否生效的结果展示）。训练列表页的趋势条逐条取自 `rsi_round` 账本行（跨会话按 `created_at` 排序；同一会话人工重跑也各算一点），**不是每个 R<n> 一个点**。
5. 编排可视化（只读，无需训练模式）：
   - goal 详情页「编排」区：`GET /workflows/goals/{goalId}` 实时装配该 goal 的 DAG。
   - squad 详情页「编排」区：`GET /workflows/squads/{squadId}` 近期 goal 的编排摘要表。

## 数据模型

| 表 | 作用 |
| --- | --- |
| `training_session` | 一次训练会话，与 goal 一对一；状态机 `waiting → analyzing → ready / failed / skipped`；冗余 `node_count / critical_path_ms / parallel_speedup / finding_count` 供列表直读 |
| `workflow_edge` | **预留表**：当前 Go 代码没有任何读/写路径（`rg workflow_edge` 只命中 schema/设计文档），DAG 边在读取时由 `sub_goal.depends_on` 与 run/verification_result/consult_request/change 实体现场装配；表内 `source` 列（explicit/inferred）是设计预留，不代表落库事实 |
| `rsi_report` | 最新一轮分析的指标 `metrics_json`、基线 `baseline_json`、冻结图 `workflow_json` 与中文叙述（每次分析重建） |
| `rsi_finding` | 规则发现：类别、严重度、置信度（L1/L2/L3）、证据、建议、确认状态（最新一轮） |
| `rsi_round` | **优化轮次账本**（洞察面板数据源）：每次分析追加一行——`round_no` 按 **domain** 递增、`trigger`（terminal/manual_retry）、冻结 metrics/baseline/findings、与同 domain 上一条账本的 `delta_json`；趋势数据由它逐行派生 |

- `sub_goal` 新增 `depends_on`（JSON 数组，leader 分派时声明；只存声明，不落 `workflow_edge`）；`sub_goal.id` 即图节点键 `sg:<sub_goal_id>`。
- `trace_span` 已在 v2 退役（迁移时 `DROP TABLE IF EXISTS`）；重试/人工等待改用实体级事实（`run.attempt`、`verification_result`、`gate_decision`）。

图节点键（代码实际生成）：`goal`（根）｜`sg:<sub_goal_id>`（执行节点）｜`v:<sub_goal_id>`（验证节点，注意不是 `verify:`）｜`review:<run_id>`｜`consult:<run_id>`｜`chg:<change_id>`（集成节点）。
边类型：`created`（goal→首节点）｜`depends_on`（显式依赖）｜`seq`（时间窗不重叠推断，`source=inferred`，**不参与关键路径**）｜`verified_by`｜`consult`｜`integrated`。

## 指标口径（workflow 级）

指标由 `internal/service/workflow.go` 的 `computeWorkflowMetrics` 在读取时从节点/边计算；JSON 键名与单位以 API 返回为准（时间为毫秒）。

- `node_count / edge_count / depth`：节点数、边数、explicit 关键路径节点数。
- `work_sum_ms`：agent + verify + consult + integrate 节点的 busy 时长之和；**不含 review**。
- `wall_ms`：上述工作节点窗口的 max(end) − min(start)；不含 review 与 goal 根节点。
- `critical_path_ms`：只沿 explicit/结构边（`created` / `depends_on` / `verified_by` / `consult` / `integrated`）做最长链 DP，并要求前驱结束 ≤ 后继开始；**排除 `source=inferred` 的 `seq` 边**，review 节点也不入链。它是「依赖约束下的最短墙钟」，不是速度指标。
- `serialization_loss_ms = max(0, wall_ms − critical_path_ms)`：墙钟里未被依赖链解释的等待（排队/派发/抖动）。
- `parallel_speedup = work_sum_ms / wall_ms`（`wall_ms=0` 时为 0）：**实际吞吐加速比**，串行 = 1.0；两个 30min 独立任务并行，wall=30、work=60 → 2.0。旧公式 `work_sum / critical_path` 已废弃，不再作为速度展示。
- `parallel_width_peak`：工作节点区间扫描的并发峰值（不含 review）。
- `verify_overhead_ratio = verify busy / work_sum_ms`；`review_busy_ms` 与 `review_overhead_ratio = review busy / work_sum_ms` 单独统计（验证与评审分开，评审不再混入验证开销）。
- `verify_rounds`：verify 节点 attempt 之和；`rework_ratio = rejected verify 轮数 / verify_rounds`。
- `consult_count`：consult 节点数。
- `lane_count / lane_imbalance`：按 assignee 分桶的 busy 负载；只统计工作节点（不含 review）；imbalance = 最忙 lane ÷ 各 lane 均值。
- `dependency_wait_ms`：sub_goal 创建 → 其依赖全部 run 结束（ready）的累计等待，衡量依赖门控。
- `dispatch_wait_ms`：ready → 首个 sub_goal run 开始的累计等待，衡量派发链路。
- `queue_wait_ms`：**legacy 兼容别名** = `dependency_wait_ms + dispatch_wait_ms`；新代码与新文档以拆分字段为准。
- `human_wait_ms`：`Σ gate_decision.review_duration × 1000`；`human_wait_share = human_wait_ms / (wall_ms + human_wait_ms)`（0..1，分母含人工等待，因此不会超过 100%）。
- `retry_amplification`：agent 节点中的**最大** Attempt（float；无 agent 节点为 0），不是平均重试次数。
- `lanes`：上述分桶明细（内部展示用）。

基线与 delta：

- baseline = **同 domain** 最近 20 个 `ready` 会话 metrics 的中位数，键覆盖 `critical_path_ms / parallel_speedup / serialization_loss_ms / dependency_wait_ms / dispatch_wait_ms / verify_overhead_ratio / review_overhead_ratio / rework_ratio / consult_count / human_wait_ms / retry_amplification`；样本 < 5 时 baseline 为空，某键覆盖不足半数时该键不进 baseline。
- delta = 当前分析 vs **同 domain 上一条 `rsi_round`**（跨 session，不要求同一会话）；`round_no` 按 domain 递增，会话首次分析 `trigger=terminal`，其后 `manual_retry`；首轮无上一条时 `delta={}`。结构为 `{key: {from, to, pct}}`（`from=0` 时省略 `pct`）。
- 「样本 <5 不给 delta」不成立：delta 只依赖上一条账本；<5 只约束 baseline。跨轮历史累计的 L2 升级当前未实现，finding 默认 L1，仅 `retry_amplification` 在超出同 domain baseline 时标 L2。

## 可优化判定（结构/分派/信息流 9 类 + human_wait 观测）

范围决策（2026-11-08）：**T1 结构 + T2 分派 + T3 信息流全做；节点内部（T4）不做主动优化；平台参数（T5）只观测；元层（T6）永不自动改。**

| 类别 | 层 | 触发条件 |
| --- | --- | --- |
| `serial_chain` | 结构 | explicit 关键路径节点数 ≥ 4 |
| `split_candidate` | 结构 | explicit 关键路径上 agent 节点时长 ÷ `critical_path_ms` ≥ 45%，且 ≥ 2 个此类节点（`cp=0` 时回退 `wall_ms` 作分母） |
| `lost_parallelism` | 结构 | 潜在加速 `gain = work_sum / critical_path ≥ 2.0`，且实际加速 `actual = work_sum / wall < gain × 0.7` |
| `merge_candidate` | 结构 | ≥ 3 个无出边的 **agent** 叶节点，结束时间散布 ≤ 最大单节点时长 × 0.6 |
| `wrong_assignee` | 分派 | lane_imbalance ≥ 2 且 lane_count ≥ 2 |
| `retry_amplification` | 分派 | agent 节点最大 Attempt ≥ 2（高于同 domain baseline 时升 L2） |
| `ping_pong` | 信息流 | 同一对 requester→target 咨询 ≥ 3 次 |
| `brief_quality` | 信息流 | rework_ratio ≥ 0.5 且存在 verify 轮数 ≥ 2 |
| `consult_repeat` | 信息流 | 执行中途（requester run 有 sub_goal）咨询 ≥ 3 次 |
| `human_wait` | 观测 | `human_wait_share ≥ 0.30` |

发现默认 L1（人工复核）；`retry_amplification` 超出同 domain baseline 时标 L2。当前**没有**「同类历史 ≥ 2 次自动升 L2」的跨轮累计逻辑。产出=**只读提案 + 复测计划**，不自动执行。

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/rsi/sessions` | 会话列表（裸数组） |
| GET | `/rsi/sessions/{id}` | `{session, report, findings, workflow}` |
| GET | `/rsi/sessions/{id}/rounds` | 优化轮次账本（洞察面板）：每轮冻结指标 + delta 对比 |
| GET | `/rsi/overview?days=30` | 总览：会话数、成功率、P95 交付、人工等待、平均并行加速/验证开销/返工率、发现数、`trend`（逐条 `rsi_round` 账本行，含 session/goal/round_no；跨会话，非每个 R<n> 一个点） |
| PATCH | `/rsi/findings/{id}` | body `{"status":"confirmed"}` 或 `dismissed` |
| POST | `/rsi/sessions/{id}/retry` | 重新分析 |
| GET | `/workflows/goals/{goalId}` | 实时装配 goal 的 WorkflowGraph |
| GET | `/workflows/squads/{squadId}?limit=10` | squad 近期 goal 的编排摘要 |

## 采集与隐私

- 图装配只读结构化行（run / sub_goal / verification_result / consult_request / change / gate_decision），**不读取工具正文与 prompt**；`content_capture` 不再有落盘正文路径。
- 训练会话与推理会话指标分开统计，避免观测者效应；推理模式零查询。

## 限制

- 不训练模型权重；分析器目前是确定性规则 + 统计，不含 LLM 归因。
- `seq` 边由时间窗推断，run 重叠的真实依赖会被画成并行；显式 `depends_on` 声明越完整图越准。
- `budget_usd` 仅展示，超预算不会中断执行。
- token / 成本维度在 v2 移除（trace 退役）；`budget_usd` 列保留作未来节级成本回归的挂点。
- 提案不自动应用、不复测；改动需人工确认并另行 A/B 验证。

## 测试

```sh
go build ./...
go test ./...
# TestErrorCodesExported 校验后端码表本身（唯一、非空）。
# TestCodesMatchRemoteTermJSON 是 code ↔ 远端 term.json 的跨层漂移守卫：
#   - 当前基线（ae12054）：CDN 不可达 / 非 200 / 响应畸形 / 缺码都 t.Skip，只留诊断信息；
#   - fix/hardening 合入后：默认 FAIL + 到期 allowlist（未知缺码立刻 FAIL，允许清单
#     截至 2026-12-31 UTC；`AGENTWORK_SKIP_CDN_DRIFT=1` 是离线/降级 CDN 的显式逃生口）。
cd web && npx tsc --noEmit && npm run build
```
