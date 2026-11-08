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
4. **任务洞察面板**：「洞察」tab 展示**优化轮次账本**——goal 终态自动分析是第 1 轮（`terminal`），人工修完问题后点「重跑分析」产生复测轮（`manual_retry`），每轮冻结当时的指标/基线/发现，并给出与上一轮的 **delta 对比**（加速比↑、返工率↓…即优化是否生效的结果展示）。训练列表页另有跨会话趋势条（每轮一个数据点）。
5. 编排可视化（只读，无需训练模式）：
   - goal 详情页「编排」区：`GET /workflows/goals/{goalId}` 实时装配该 goal 的 DAG。
   - squad 详情页「编排」区：`GET /workflows/squads/{squadId}` 近期 goal 的编排摘要表。

## 数据模型

| 表 | 作用 |
| --- | --- |
| `training_session` | 一次训练会话，与 goal 一对一；状态机 `waiting → analyzing → ready / failed / skipped`；冗余 `node_count / critical_path_ms / parallel_speedup / finding_count` 供列表直读 |
| `workflow_edge` | 显式依赖的持久化落点（`depends_on` 写入时同步落行）；读取时与推断边合并装配，`source=explicit\|inferred` |
| `rsi_report` | 最新一轮分析的指标 `metrics_json`、基线 `baseline_json`、冻结图 `workflow_json` 与中文叙述（每次分析重建） |
| `rsi_finding` | 规则发现：类别、严重度、置信度（L1/L2/L3）、证据、建议、确认状态（最新一轮） |
| `rsi_round` | **优化轮次账本**（洞察面板数据源）：每次分析追加一行——`round_no` 递增、`trigger`（terminal/manual_retry）、冻结 metrics/baseline/findings、与上一轮的 `delta_json`；趋势数据由它派生 |

- `sub_goal` 新增 `depends_on`（JSON 数组，leader 分派时声明）；`sub_goal.id` 即图节点键 `sg:<sub_goal_id>`。
- `trace_span` 已在 v2 退役（迁移时 `DROP TABLE IF EXISTS`）；重试/人工等待改用实体级事实（`run.attempt`、`verification_result`、`gate_decision`）。

图节点键：`goal`（根）｜`sg:<sub_goal_id>`（执行节点）｜`verify:<sub_goal_id>`｜`review:<run_id>`｜`consult:<run_id>`｜`chg:<change_id>`。
边类型：`created`（goal→首节点）｜`depends_on`（显式依赖）｜`seq`（时间窗不重叠推断，`source=inferred`）｜`verified_by`｜`consult`｜`integrated`。

## 指标口径（workflow 级）

- `work_sum_ms`：全部执行节点 busy 时长之和；`wall_ms`：节点窗口 min start → max end。
- `critical_path_ms`：DAG 最长链（DP，边有效要求源结束 ≤ 目标开始）；`parallel_speedup = work_sum / critical_path`。
- `serialization_loss_ms = critical_path − max(单节点时长)`：串行化损失。
- `parallel_width_peak`：时间扫描的并发宽度峰值；`lane_count / lane_imbalance`：按 assignee 分桶的负载不均。
- `queue_wait_ms`：Σ(sub_goal.created_at → 首 run 开始)，分派延迟。
- `verify_overhead_ratio`：(verify + review busy) / work_sum；`verify_rounds`、`rework_ratio`（rejected 轮占比）。
- `consult_count`：咨询次数；`human_wait_ms`：`gate_decision.review_duration` 之和。
- 基线：最近 20 个 `ready` 会话的中位数；样本少于 5 时不给 delta。

## 可优化判定（9 类结构/分派/信息流发现）

范围决策（2026-11-08）：**T1 结构 + T2 分派 + T3 信息流全做；节点内部（T4）不做主动优化；平台参数（T5）只观测；元层（T6）永不自动改。**

| 类别 | 层 | 触发条件 |
| --- | --- | --- |
| `serial_chain` | 结构 | 关键路径节点数 ≥ 4 |
| `split_candidate` | 结构 | 关键路径上单节点占关键路径 ≥ 45% 且 ≥ 2 个此类节点 |
| `lost_parallelism` | 结构 | work/cp ≥ 2.0 且实测加速 < 理论增益 × 0.7 |
| `merge_candidate` | 结构 | ≥ 3 个无出边叶节点且结束时间散布/最大时长 ≤ 0.6 |
| `wrong_assignee` | 分派 | lane_imbalance ≥ 2 且 lane_count ≥ 2 |
| `retry_amplification` | 分派 | Σattempt/节点数 ≥ 2（基线更高时升 L2） |
| `ping_pong` | 信息流 | 同一对 requester→target 咨询 ≥ 3 次 |
| `brief_quality` | 信息流 | rework_ratio ≥ 0.5 且存在 verify 轮数 ≥ 2 |
| `consult_repeat` | 信息流 | 执行中途（requester run 有 sub_goal）咨询 ≥ 3 次 |
| `human_wait` | 观测 | 人工等待 / wall ≥ 0.30 |

发现默认 L1（人工复核）；同类历史 ≥ 2 次升 L2。产出=**只读提案 + 复测计划**，不自动执行。

## API

| 方法 | 路径 | 说明 |
| --- | --- | --- |
| GET | `/rsi/sessions` | 会话列表（裸数组） |
| GET | `/rsi/sessions/{id}` | `{session, report, findings, workflow}` |
| GET | `/rsi/sessions/{id}/rounds` | 优化轮次账本（洞察面板）：每轮冻结指标 + delta 对比 |
| GET | `/rsi/overview?days=30` | 总览：会话数、成功率、P95 交付、人工等待、平均并行加速/验证开销/返工率、发现数、`trend`（每轮一个点） |
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
go test ./...   # TestCodesMatchRemoteTermJSON 依赖远端 CDN 术语表，缺网/远端缺码时失败，与本特性无关
cd web && npx tsc --noEmit && npm run build
```
