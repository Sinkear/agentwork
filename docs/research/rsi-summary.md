# agentwork RSI 能力自迭代：调研总结与落地设计

> 版本：2026-11-05 · 范围：仅本特性，不改现有 goal/run 语义
> 方法：两名调研 agent 分别取学术（`docs/research/rsi-academic.md`）与业界（`docs/research/rsi-industry.md`）官方来源；本文做交叉核对、指标统一定义、可优化判定与 agentwork 落地设计。
> 可信度约定：论文数字标 A（同行评审/arXiv 原文）；官方产品文档标 A-（厂商自述，机制可信、效果未独立验证）；媒体转述标 B；未取到正文的标 C 并注明「未核实」，本文不把 C 当论据。
> 局限：本机 `web_search` 上游 402，检索依赖已知 URL 直取 + 站点索引；部分厂商页只有导航壳（OTel GenAI 属性名、ADK/Vertex、Uber M3、Copilot 指标定义），已在两份分报告文末列为未核实项。

---

## 0. 一页结论

**在 agentwork 里，「RSI 能力自迭代」应定义为 harness 层的自我改进闭环，而不是训练模型权重。**

```
记录 trace → 计算指标 → 归因到具体瓶颈 → 生成可回滚提案 → 离线/在线复测 → 采纳或回滚
   (P0)        (P0)          (P1)             (P2)              (P2)
```

四件必须做对的事：

1. **执行过程必须变成结构化 trace，而不是聊天记录。** 现在只有 `chat_message`（按文本聚合），没有 span 起止、tokens、上下文占用、人工等待，无法稳定回答「哪块慢」。P0 先补 `trace_spans` 宽表（goal/run/llm/tool/verify/human_wait），采集不阻塞主流程。
2. **「训练模式」是 goal 级开关，但底层拆三层语义**：采集（是否记）、内容（记不记 prompt/文件正文）、训练（能否进分析/数据集）。UI 上 v1 只给一个「训练 / 推理」开关 + 高级项默认值，避免把产品复杂度甩给用户。
3. **分析必须是异步、有状态、可复核的后台实体**（LangSmith Insights / Braintrust Patterns 的直接对标），不是点开页面现算。goal 到 `done|failed` 后封存 trace、跑确定性规则、可选 LLM 归因，产出带证据的 findings，状态停在 `needs_review` 等人确认。
4. **第一版绝不自动改代码、自动改验证器、自动应用提案。** 学术侧最硬的教训是 reward hacking（Skalse et al., arXiv:2209.13085）：任何能改「评分/验证器」的自我改进系统，最终都会优化评分而不是优化工作。允许自改的范围先锁在 prompt / few-shot / 工具白名单与超时 / 上下文压缩策略 / 模型路由，验证器与 gate 规则是宪法层。

关键设计决策：

| 决策 | 选择 | 理由 |
|---|---|---|
| 训练对象 | prompt / 工具策略 / 上下文策略 / 验证阈值（只读建议） | 权重训练成本与数据量都不现实；DSPy 的 optimizer 已证明「不训权重」也能显著改进行为 |
| trace 存储 | 单张 `trace_spans` 宽表，trace 级字段冗余 | 单机 SQLite 避免多表 join；Langfuse 单表 observation 的成熟取舍 |
| 分析触发 | goal 终态后创建 `training_session` + 异步 job | 与 Insighs/Patterns 一致；失败/取消也有分析价值，但默认只分析 done/failed |
| 指标口径 | 交付周期、成功率、cost_per_success、P95、人工等待、上下文峰值、重复调用率 | 业界六件套 + agentwork 天然 label（验证结果、人工干预） |
| 「可优化」 | 四条件 + 七类瓶颈 + 三级置信度 | 单机小样本，禁止把一次观察说成统计显著 |
| 提案应用 | 人工批准 + 复测，regressed 自动回滚 | 学术五闸门（宪法/证据/统计/裁判/回滚）的工程化 |
| 首个里程碑 | M0：开关 + span + 规则报告 + Training tab 列表/详情 | 先把「哪块慢」答对，再谈「怎么优化」的 LLM 叙事 |

**不做**：训练权重；让 agent 改自己的验证器/评分/gate；无人值守自动应用；把一次 run 的 LLM 聚类结论当因果；默认全量采集 prompt/文件内容。

---

## 1. 学术侧：RSI 是什么，为什么容易失败

### 1.1 定义

Recursive Self-Improvement（RSI）在学术语境里指：系统能修改自身，并用修改后的系统继续产生改进，形成递归。2026 年的 RSI-Dojo（arXiv:2611.03698）把它拆成三类可操作对象：**模型、harness、基础设施**。对 agentwork 有意义的只有第二类——harness：system prompt、工具集、上下文管理、控制流、验证与重试策略。

形式化地，一个 RSI 循环是 `(A, P, E, S, Π)`：agent A、问题分布 P、可执行环境 E、评分/验证器 S、搜索/提案策略 Π。改进 = 在 E 上执行 A，拿 S 评分，按 Π 修改 A，再验证。**没有 S 的自我修改只是随机漂移**；agentwork 恰好已有 S（`verification_result` + gate + 人工决定），这是做 RSI 的先天优势。

### 1.2 已发表系统与证据

| 系统 | 改什么 | 关键机制 | 报告效果（原文口径） |
|---|---|---|---|
| Gödel Machine（Schmidhuber, 2003） | 自身任意代码 | 只有「可证明有益」才自改 | 理论优雅、实践不可行（证明成本高于收益）→ 转向经验验证 |
| STOP（Zelikman et al., arXiv:2310.02304） | 递归改进的推理器 | 用自生成轨迹迭代训练 | 有限样本下持续改进，但依赖大量采样 |
| ADAS（Hu et al., arXiv:2408.08435） | 新 agent 的代码 | meta-agent 搜索 agent 空间并存档 | 跨领域迁移有效，成本高 |
| DGM（arXiv:2505.22954） | agent 代码 + 工具 | 维护 archive、自改代码、用 benchmark 实证选择 | SWE-bench 20.0%→50.0%，Polyglot 14.2%→30.7% |
| SICA（arXiv:2504.15228） | 自身代码 | 无梯度的 LLM 反思自编辑 | SWE-bench Verified 子集 17%→53% |
| AFlow（arXiv:2410.10762） | 工作流图 | MCTS 搜索代码化工作流 | 小模型上以更低成本超过手工 workflow |
| RSI-Dojo（arXiv:2611.03698） | harness + 训练环境 | 281 个可执行任务、可验证奖励 | PostTrainBench 上微调 Qwen 后 +8.74 / +16.87 |

结论三点：

- **收益真实但高度依赖验证器质量**；DGM 自己也强调沙箱与人工监督。
- **改进对象有风险阶梯**：prompt/context < workflow < 工具技能 < 代码 < 权重。风险越高，越需要离线 replay 与回滚。
- **archive + 选择** 比单次自改可靠：保留多个候选、用同一套评测选，而不是「改完就上」。

### 1.3 四类退化模式

1. **验证器攻击 / reward hacking**：改评分规则、改测试、只挑容易样本。窄 proxy 不保证安全（Skalse et al., arXiv:2209.13085）。
2. **自评偏置**：模型偏好自己的输出（self-preference，arXiv:2404.13076），LLM-as-judge 单独用会漂移。
3. **上下文坍缩**：多轮自我修改把上下文压成同质摘要，探索能力下降（ACE, arXiv:2510.04618）。
4. **模型坍缩**：用自生成数据反复训练导致分布收窄（model collapse）。

### 1.4 五道闸门（agentwork 直接照抄）

| 闸门 | 做法 |
|---|---|
| 宪法 | 明确禁止自改的对象：验证器、gate 规则、评分函数、replay 套件、权限/预算上限 |
| 证据 | 每条结论必须能指到 span/run/event，不能只有一段自然语言 |
| 统计 | 配对比较、置信区间、最小效应；小样本只报观察，不报显著 |
| 裁判 | 自动指标 + 人工复核双轨；LLM 只做归因叙事，不做最终裁决 |
| 回滚 | 每个提案有版本、diff、回滚点；复测退化自动回滚 |

这五条直接决定后面的数据模型与状态机。

---

## 2. 业界侧：trace、面板、异步分析与自优化

### 2.1 trace 的两种存储流派

- **树形 run/span**：LangSmith（run=span，trace_id 绑定，单 trace 上限 25,000 run；thread 是多次会话的 trace 序列）、OpenAI Agents SDK（task/turn/agent/generation/function/guardrail/handoff 等 10 类 span）、Arize Phoenix（OpenInference `openinference.span.kind`）、AgentOps（session→agent→workflow→operation）。
- **单表 observation 宽表**：Langfuse 只有 observations 一张表，trace 级属性（user/session/tags/metadata）复制到每行，避免 join。对单机 SQLite 是正确取舍。

agentwork 的映射：**goal ≈ thread，run ≈ trace，tool/step ≈ span，session ≈ 一次 goal 的执行上下文**。span 树用 `parent_span_id` 表达；根 span 是 run，goal 是逻辑分组。

### 2.2 面板六件套

1. trace 瀑布（起止、耗时占比、父子、并行分支）
2. token / cost（按 span/run/model 聚合）
3. 错误率（按 error class 分组，不只是一个总数）
4. 重试（api_error、retries_exhausted 等事件）
5. **人工等待**（Claude Code 的 `claude_code.tool.blocked_on_user`：duration_ms + decision + source）——最值得直接抄的指标
6. 吞吐/产出（会话数、活跃时长、PR/commit 数）

### 2.3 异步分析是成熟产品形态

- **LangSmith Insights**：后台 job（≤30 分钟），双模型分工（强模型聚类、便宜模型逐条摘要），样本上限 1000 trace，1000 threads 约 $1–4；报告给「百分比结论 + 可点击 trace 证据」，支持 daily/weekly/cron。
- **Braintrust Patterns / Loop**：定时巡检 trace，finding 带证据、解释、建议动作、severity、category；发现会刷新、会在连续两个工作日不再出现时关闭；可一键转 scorer/分类器/编码任务；**读操作不打断人，写操作必须人批准**。
- **Devin Session Insights**：会话结束即自动产出轻量分类与建议，大会话才跑重分析。

三者共同证明：分析要有状态、可调度、可复核；「一次性聊天回答」不是产品。

### 2.4 自优化闭环的现实定义

`trace → dataset → eval → 改 prompt/策略 → 复测`。DSPy 把它做成 optimizer（BootstrapFewShot / MIPROv2 / GEPA），作用对象是 program 的 prompt 与示例，不是权重。Braintrust/LangSmith 把它产品化成 dataset + experiment + scorer。

**对 agentwork：「训练」= 从历史 run 里提取成功/失败模式，生成 prompt few-shot、工具策略、上下文策略的候选改动，再用冻结任务集复测。** 这与「训练模型」不是一回事，产品文案要避免让用户误解。

### 2.5 开关的三层语义

| 层 | 问题 | 默认 |
|---|---|---|
| 采集 | 记不记 trace | 训练模式开，推理模式只记基础 run 指标 |
| 内容 | 记不记 prompt/文件正文 | 默认 `redacted`（结构 + 长度 + 哈希），可升 `full` |
| 训练 | 数据能否进入分析/数据集 | 仅训练模式的 goal 进入；推理模式数据不喂分析 |

权限优先级照抄 Claude Code：**组织策略 > 项目配置 > 个人配置，关闭优先于开启**；仓库内配置不能强行打开采集。

### 2.6 平台能力阶梯

埋点 → 面板 → 分析 → 优化。多数产品停在第二、三级。agentwork 要自称 RSI，必须做到第四级（提案 + 复测 + 回滚），否则只是更好看的 trace 浏览器。

---

## 3. 指标定义（agentwork 口径）

### 3.1 层级与数据源

| 层级 | 实体 | 数据源 | 现有可得性 |
|---|---|---|---|
| Goal | 交付周期、人工迭代、验证通过率、总成本 | goal / run / verification_result / gate_decision | 大部分已有 |
| Run | wall time、LLM/工具/等待占比、token、上下文峰值、重试 | 新 `trace_spans` + usage | 需新增 |
| Step | 工具耗时、错误、重复调用、LLM turn 延迟 | tool_use→tool_result 时间差、usage_update | 需新增埋点 |
| Label | 验证 passed/rejected、gate 决定、人工驳回 | verification_result / gate_decision / human_iterations | 已有 |

### 3.2 指标清单与公式

**Goal 级**

| 指标 | 公式 | 说明 |
|---|---|---|
| 交付周期 `lead_time_ms` | `done_at - created_at` | 含人工等待；与纯机器耗时分开报 |
| 机器重试 `machine_retries` | `max(run.attempt)` 或失败 run 数 | 与人工迭代分开 |
| 人工迭代 `human_iterations` | `goal.human_iterations` | 已有 |
| 验证通过率 `verify_pass_rate` | `passed / (passed + rejected)` | 分母不含未验证 |
| 一次成功率 `first_pass_rate` | 首次 verify 即 passed 的 goal / 有 verify 的 goal | 比总通过率更能反映稳定性 |
| 每次成功成本 `cost_per_success` | `Σcost(所有 run + 分析成本) / 成功 goal 数` | 必须含失败与重试，否则严重低估 |
| 人工等待 `human_wait_ms` | `Σ gate_wait` | 单独 span，不混进 P95 |
| MTTR | 首次失败到连续 3 次成功 | 单机 N=3 |

**Run 级**

| 指标 | 公式 | 说明 |
|---|---|---|
| `wall_ms` | `finished_at - started_at` | |
| `llm_ms` / `tool_ms` / `wait_ms` | 对应 kind 的 span 时长和（并行时取并集） | 瀑布同源 |
| `critical_path_ms` | span 树最长路径 | 不是所有 span 时长之和 |
| `span_share` | `span.duration / critical_path` | 关键路径占比 |
| `tokens_in/out`、`cost_usd` | usage_update 聚合 | 无 usage 时按字符估算并标 `estimated=true` |
| `context_peak_ratio` | `max(used/size)` | 峰值，不是累计 |
| `tool_calls` / `tool_error_rate` | 按 tool name 分组 | 失败调用也计入 |
| `duplicate_ratio` | 重复 `(tool_name, args_hash)` 次数 / 总调用 | 排除首次 |
| `retry_amplification` | 重试花费时长 / wall | 失败后成功也算重试成本 |
| `steps` | 模型 turn 数或工具调用数（二选一，写死） | 不与「工具数」混用 |

**统计口径**

- P50/P95 用原始样本或直方图，不用「平均的分位数」；样本 <20 时 P95 不展示，只给中位数与极值。
- 对比用配对 bootstrap（延迟/成本）或 McNemar（通过/失败）；报告 95% CI 与效应量。
- 小样本（n<8）只报「本次观察 + 基线中位数」，不写「显著」。
- always-valid 置信序列（arXiv:1512.04922）可用于持续监控，避免「看一次算一次」的假阳性。

### 3.3 口径陷阱（写进实现注释）

- 成功率分母只含进入终态的 run；cancelled 单列。
- `cost_per_success` 必须含失败、重试、分析自身的成本。
- 上下文报「峰值 + 累计计费」两个数，cache read/write 分列。
- 人工等待必须独立 span，否则长尾会污染 agent 延迟。
- 训练 run 与推理 run 的指标分开（观测者效应）；训练模式本身有落盘/序列化开销。
- 验证器通过 ≠ 业务成功；label 偏差靠人工标注队列校准。

---

## 4. 「可优化」怎么判定

### 4.1 四个必要条件

一条 finding 只有同时满足才进入「可优化」列表：

1. **显著**：相对基线偏离超过阈值（默认：中位数 +20%，或 robust z ≥2.5；n≥20 时才允许「统计显著」措辞）。
2. **可归因**：能指到具体机制与证据（≥2 个 span 或 ≥3 个同类 run），不是「感觉慢」。
3. **可干预**：存在允许的 patch 类型能改变它（prompt/工具策略/上下文策略/并发/超时/模型路由）。
4. **可回滚**：有版本、diff 与回滚点；不能回滚的改动不进提案。

`bottleneck_score = critical_path_share × reducible_weight × confidence`，取最大者为「主瓶颈」。

### 4.2 七类瓶颈与判据

| 类别 | 可计算判据（默认阈值） | 典型证据 | 建议动作 | patch target |
|---|---|---|---|---|
| 工具耗时型 | `tool_share ≥0.5` 且 top tool ≥0.25 | 某工具 span 长、输入大、输出大 | 缓存、批量命令、换更快工具、并行读 | tool_policy |
| LLM 耗时型 | `llm_share ≥0.6` 且 turn 数偏高 | 相邻 tool 之间长间隔、turn 多 | 缩短 system prompt、少样本、模型路由、减少无效 turn | prompt / model_route |
| 重试放大型 | `retry_ms/wall ≥0.2` 或 error rate ≥0.15 | 同工具反复失败、backoff 长 | 修根因、fail-fast、调整超时/重试上限 | workflow_param |
| 上下文压力型 | 峰值 `used/size ≥0.7` 且 ≥2 次；或每 turn 增长 ≥30% | usage_update、长工具输出 | 工具输出截断/摘要、按需检索、子 agent 隔离 | context_policy |
| 重复调用型 | `duplicate_ratio ≥0.15` 且 ≥2 对 | 相同 `(tool,args_hash)` 重复 | 结果缓存、playbook、把稳定查询固化为脚本 | tool_policy / skill |
| 串行依赖型 | 关键路径 ≥0.5 且相邻只读工具无重叠 | 瀑布里本可并行的读操作串行 | 并行工具调用、预取、拆分任务 | workflow_param |
| 人工等待型 | `human_wait / lead_time ≥0.3` | gate 请求到决定的间隔 | 预审、异步通知、批量 gate、默认低风险自动通过（需人配置） | 流程配置（非自改） |

### 4.3 三级置信度

- **L1 观察**：本次 run 内可复现的 span 证据；只能写「本次」。
- **L2 归因**：同类 pattern 在 ≥3 个 run 或 ≥2 个 goal 出现，且方向一致；可写「反复出现」。
- **L3 已验证**：有 A/B 或 replay 复测，CI 不含 0；才允许写「改进有效」。

UI 必须显示级别，禁止把 L1 写成因果结论。

### 4.4 一个具体例子

某 run：wall 18m，工具 9.2m（51%），其中 `grep` 3 次同参数共 2.1m；上下文峰值 82%；gate 等待 6m；无错误。

- 主瓶颈：人工等待（6m/18m=33%，但 reducible 低，因为等人）→ 建议异步通知/预审，而不是「优化 agent」。
- 次瓶颈：重复 grep（duplicate_ratio=2/38=5.3%，低于 15%，不单列）。
- 上下文：82% 但只出现 1 次，不满足「≥2 次」→ L1 观察，建议在报告里提示，不生成 patch。
- 工具：51% 但 top tool 只占 12%，不满足 25% → 不判工具型，避免把「正常干活」误判为瓶颈。

这就是「判据 + 阈值」的意义：防止 LLM 看到「工具占 51%」就下结论。

---

## 5. agentwork 落地设计

### 5.1 概念与边界

- `goal.execution_mode`：`infer`（默认）| `train`。
- 训练模式 = 记录完整结构 trace + 终态异步分析；推理模式 = 只记录 run 级基础指标（耗时、状态、token 若可用），不建 session、不进分析。
- 分析对象是 harness 行为，不训练权重；分析器只能**提议**，不能改验证器/评分/gate。
- 内容采集默认 `redacted`：记录工具名、参数哈希、输入输出长度、状态、错误类别；正文写内容寻址文件（可选 `full` 时），DB 只留 `content_ref`。

### 5.2 数据模型（Schema）

在 `internal/store/schema.sql` 新增；`goal.execution_mode` 走 `store.go` 的 best-effort migration（该文件已有此模式）。

```sql
ALTER TABLE goal ADD COLUMN execution_mode TEXT NOT NULL DEFAULT 'infer'; -- infer|train

CREATE TABLE IF NOT EXISTS training_session (
    id              TEXT PRIMARY KEY,
    goal_id         TEXT NOT NULL REFERENCES goal(id),
    domain_id       TEXT NOT NULL DEFAULT '',
    mode            TEXT NOT NULL DEFAULT 'train',      -- train|sample
    content_capture TEXT NOT NULL DEFAULT 'redacted',   -- none|redacted|full
    sample_rate     REAL NOT NULL DEFAULT 1.0,
    budget_usd      REAL NOT NULL DEFAULT 2.0,
    status          TEXT NOT NULL DEFAULT 'collecting', -- collecting|sealing|analyzing|needs_review|ready|failed|skipped
    trace_count     INTEGER NOT NULL DEFAULT 0,
    error           TEXT NOT NULL DEFAULT '',
    started_at      TEXT NOT NULL,
    sealed_at       TEXT NOT NULL DEFAULT '',
    finished_at     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_training_session_goal ON training_session(goal_id, started_at);

CREATE TABLE IF NOT EXISTS trace_span (
    id             TEXT PRIMARY KEY,
    session_id     TEXT NOT NULL DEFAULT '',
    goal_id        TEXT NOT NULL DEFAULT '',
    run_id         TEXT NOT NULL DEFAULT '',
    span_id        TEXT NOT NULL,
    parent_span_id TEXT NOT NULL DEFAULT '',
    kind           TEXT NOT NULL,   -- run|llm|tool|verify|gate|human_wait|subgoal
    name           TEXT NOT NULL,
    started_at     TEXT NOT NULL,
    ended_at       TEXT NOT NULL DEFAULT '',
    duration_ms    INTEGER NOT NULL DEFAULT 0,
    status         TEXT NOT NULL DEFAULT 'ok',  -- ok|error|cancelled
    error_class    TEXT NOT NULL DEFAULT '',
    retry_count    INTEGER NOT NULL DEFAULT 0,
    tool_name      TEXT NOT NULL DEFAULT '',
    args_hash      TEXT NOT NULL DEFAULT '',
    input_bytes    INTEGER NOT NULL DEFAULT 0,
    output_bytes   INTEGER NOT NULL DEFAULT 0,
    tokens_in      INTEGER NOT NULL DEFAULT 0,
    tokens_out     INTEGER NOT NULL DEFAULT 0,
    cost_usd       REAL NOT NULL DEFAULT 0,
    context_used   INTEGER NOT NULL DEFAULT 0,
    context_size   INTEGER NOT NULL DEFAULT 0,
    attrs_json     TEXT NOT NULL DEFAULT '{}',
    content_ref    TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_trace_span_run ON trace_span(run_id, started_at);
CREATE INDEX IF NOT EXISTS idx_trace_span_goal ON trace_span(goal_id, started_at);
CREATE INDEX IF NOT EXISTS idx_trace_span_session ON trace_span(session_id, started_at);

CREATE TABLE IF NOT EXISTS rsi_report (
    id             TEXT PRIMARY KEY,
    session_id     TEXT NOT NULL REFERENCES training_session(id),
    goal_id        TEXT NOT NULL,
    status         TEXT NOT NULL DEFAULT 'pending', -- pending|running|ready|failed
    summary        TEXT NOT NULL DEFAULT '',
    metrics_json   TEXT NOT NULL DEFAULT '{}',
    baseline_json  TEXT NOT NULL DEFAULT '{}',
    narrative      TEXT NOT NULL DEFAULT '',       -- 可选 LLM 归因
    analyzer_run_id TEXT NOT NULL DEFAULT '',
    error          TEXT NOT NULL DEFAULT '',
    created_at     TEXT NOT NULL,
    finished_at    TEXT NOT NULL DEFAULT ''
);

CREATE TABLE IF NOT EXISTS rsi_finding (
    id          TEXT PRIMARY KEY,
    report_id   TEXT NOT NULL REFERENCES rsi_report(id),
    goal_id     TEXT NOT NULL,
    category    TEXT NOT NULL,   -- tool|llm|retry|context|duplicate|serial|human_wait
    severity    TEXT NOT NULL,   -- high|medium|low
    confidence  TEXT NOT NULL,   -- L1|L2|L3
    title       TEXT NOT NULL,
    explanation TEXT NOT NULL,
    suggestion  TEXT NOT NULL DEFAULT '',
    evidence_json TEXT NOT NULL DEFAULT '[]',
    score       REAL NOT NULL DEFAULT 0,
    status      TEXT NOT NULL DEFAULT 'open', -- open|confirmed|dismissed
    created_at  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_rsi_finding_report ON rsi_finding(report_id, score DESC);
```

暂不做 `proposals`/`metric_snapshots` 表：P1 用 JSON 存提案草稿，P2 再独立表，避免 M0 过度设计。

### 5.3 采集与埋点

1. **proto 增加 usage 事件**：`EventUsage`（tokens_in/out、cost、context used/size、model）。`internal/proto/acpbackend/session.go:230`、`backend.go:304` 的 `OnUsageUpdate` 目前是空实现，改为发事件；这是 token/成本/上下文指标的前置。
2. **daemon.persistEvent 埋点**（`internal/daemon/daemon.go:2173`）：首个 `tool_use` 插入 `trace_span`（kind=tool，记录 started_at、tool_name、args_hash、input_bytes），首个 `tool_result` 回填 ended_at/status/output_bytes/error_class；`CallID` 作为关联键。现有 `chat_message` 聚合逻辑不动，span 是旁路写入，失败只记日志、不影响执行。
3. **run 级 span**：daemon 在 run 开始/结束各写一条 kind=run；`parent_span_id` 为空。
4. **LLM turn span**：usage_update 到达时写 kind=llm，记录 tokens/cost/context；若 backend 不发 usage，analyzer 用 `chat_message` 时间差估算 turn 间隔，并标 `estimated=true`（不伪造 token）。
5. **人工等待 span**：goal 进入 `review`/gate 请求时写 kind=human_wait，`gate_decision.decided_at` 回填结束；已有 `review_duration` 可交叉校验。
6. **性能**：span 写入走单写者 batch（每 200 行或 500ms），不阻塞事件流；训练模式采样（失败 100% 保留，成功按 sample_rate）。
7. **隐私**：默认只写结构；`content_ref` 指向 `$AGENTWORK_HOME/rsi-content/<sha256>`，可按 session 删除。

### 5.4 分析流水线

```
goal 终态(done|failed)
  → 创建 training_session(status=sealing) + 封存 span（幂等：session.goal_id 唯一）
  → 确定性分析（Go，秒级）：指标、关键路径、七类规则、基线对比 → rsi_report(status=ready 的最低保障)
  → 可选 LLM 归因：enqueue processor run(run_type=rsi_analyze, domain=goal.domain_id)
        prompt 内含结构化 metrics/findings + 证据 span 摘要；要求输出 JSON（narrative + 补充假设）
  → 合并 narrative，状态 needs_review
  → 用户确认 finding（open→confirmed/dismissed），P2 生成 proposal
```

要点：

- **确定性结果先落库**：即使没有可用 agent、LLM 超时，报告也有指标与规则 findings，状态 ready，不失败。
- **LLM 不做裁决**：只解释「为什么慢 / 可能怎么改」，finding 的 severity/score 由规则计算，LLM 不能改；输出 schema 校验失败则丢弃 narrative。
- **job 状态机**：`collecting → sealing → analyzing → needs_review → ready`；失败 `failed`，可重试 3 次；daemon 重启扫描 `sealing|analyzing` 且超时的 session 重新入队（租约/心跳，参照 industry 文档）。
- **预算**：session 预算（默认 $2）+ 单次分析样本上限（500 span）+ 全局每日预算；触顶降级为纯规则分析。
- **触发位置**：`GoalService.reconcileOnRunEndOnce` 事务提交后的 afterCommit 回调（goal 已变 done/failed 时），创建 session 并入队；不要在事务内起 goroutine。

### 5.5 API

| 方法 | 路径 | 说明 |
|---|---|---|
| POST | `/goals` | body 增加 `execution_mode`（缺省 infer） |
| GET | `/rsi/sessions?domain_id=&status=&limit=` | 列表（join goal 标题、report 摘要） |
| GET | `/rsi/sessions/{id}` | 详情：session + report + findings + spans |
| GET | `/rsi/goals/{goalId}` | goal 视角入口（0/1 条） |
| POST | `/rsi/sessions/{id}/retry` | 重跑分析（幂等） |
| PATCH | `/rsi/findings/{id}` | `{status: confirmed|dismissed}` |
| GET | `/rsi/overview?days=30` | 卡片：成功率、cost_per_success、P95、人工等待、发现数 |

handler 注册在 `internal/server/handler/handler.go:66 Mount`；响应直接复用 service 结构体 + JSON tag。

### 5.6 UI/UX

导航（`web/components/nav.tsx`）：工作台组在「任务」之后插入 `{ href: "/training", label: "训练分析", icon: BarChart3 }`，与 goal 平级；不做二级菜单。goal 详情页只加一个「训练」状态条 + 跳转，不复制面板。

**列表页 `/training`**：
- 顶部 4 卡：分析会话数、平均交付周期、cost_per_success、人工等待总时长。
- 表格列：goal 标题 / 时间 / 模式徽章 / 状态 / span 数 / 成本 / 发现数 / 主瓶颈。筛选：状态、项目、时间、是否有高危。
- 空态：解释「在新建任务时打开训练模式」。

**详情页 `/training/[id]`**：Tabs 概览 / 发现 / Trace / 对比。
- 概览：3–5 条结论（带百分比与置信度）、指标卡、成本拆分（LLM/工具/分析）。
- 发现：按 score 排序，卡片含 severity、判据数值、证据 span 链接、建议、置信度、确认/忽略按钮。
- Trace：三栏（run 列表 | 瀑布 | span 详情），瀑布用 CSS 绝对定位；本期只做单 run，不做嵌套虚拟滚动。
- 对比：本次 vs 同 domain 基线（中位数）双列 delta；n<5 时显示「样本不足」。

**新建任务表单**（`web/components/new-goal-form.tsx`）：加「运行模式」radio：
`推理（默认，只记基础指标）` / `训练（记录执行 trace，完成后自动分析）`；选中训练时展开高级项（内容采集：脱敏/仅结构/完整；预算；采样率），默认值隐藏。`body.execution_mode = "train"`。`/goals/new` 的管家流程同样透传。

**goal 详情**：`execution_mode=train` 时显示「训练中 · N spans」徽章，完成后「分析完成 · N 条发现」链接到详情。

### 5.7 提案与复测（P2，但接口先留）

`rsi.patch/v1`：

```json
{
  "target": "prompt.system|prompt.fewshot|tool_policy|context_policy|workflow_param|model_route",
  "hypothesis": "...",
  "expected_effect": {"metric": "lead_time_ms", "direction": "down", "min_pct": 5},
  "evaluation_plan": {"replay_suite": "...", "min_pairs": 20, "test": "bootstrap|mcnemar"},
  "risk": "low|medium|high",
  "rollback": "config version id / git ref"
}
```

采纳条件：主指标改善 ≥5%，护栏（成功率、成本、P95）不退化 >1pp/3%，CI 不含 0；否则拒绝。应用后进入 `verified/regressed`，退化自动回滚并生成 finding。**验证器、gate 规则、评分、replay 套件永不在 target 枚举内。**

### 5.8 分期与验收

| 阶段 | 范围 | 验收 |
|---|---|---|
| M0（本期） | execution_mode、schema、span 采集、usage 落库、规则分析、API、Training 列表/详情、goal 表单开关 | 训练 goal 完成后 30s 内出报告；瀑布与指标可核对；推理 goal 无 session；构建通过 |
| M1 | 异步 job 状态机/租约、基线对比、finding 确认、成本预算 | 重启不丢 job；对比视图有 n≥5 基线 |
| M2 | LLM 归因叙事、proposal 生成、replay 复测 | 提案可回滚；复测报告含 CI |
| M3 | 模式库（跨 session）、定时巡检、灰度自动应用 | 人工批准率、回滚率可观测 |

**M0 明确不做**：LLM 分析、提案自动应用、跨 session 聚类、内容全文采集、嵌套多 agent span。

### 5.9 文件级工作拆分

Go：
1. `internal/store/schema.sql` + `internal/store/store.go`（migration）
2. `internal/service/goal.go`（字段、Create、终态触发）
3. `internal/service/rsi.go`（session/report/finding CRUD、封存、入队）
4. `internal/rsi/analyze.go`（指标、关键路径、七类规则、基线）
5. `internal/daemon/rsi_span.go`（persistEvent 旁路、run/span 生命周期）
6. `internal/proto/proto.go` + `internal/proto/acpbackend/{session,backend}.go`（usage）
7. `internal/server/handler/rsi.go` + `handler.go` 路由
8. `internal/daemon/daemon.go`（run_type=rsi_analyze 的 processor 分支、结果落报告）

Web：
9. `web/lib/types.ts`、`queries.ts`、`api.ts`
10. `web/components/nav.tsx`、`new-goal-form.tsx`
11. `web/app/training/page.tsx`、`web/app/training/[id]/page.tsx`
12. `web/components/training/*`（卡片、finding、瀑布）

测试：analyzer 单测（构造 span 树断言关键路径/规则命中）、schema 打开旧库的 migration 测试、API handler 测试、`./build.sh`。

### 5.10 风险与开放问题

- **观测者效应**：训练模式落盘会拉长 wall；报告里必须标注「训练 run」，基线只用推理 run。
- **token 不可得**：不是所有 ACP backend 发 usage；M0 允许空值，UI 显示「未上报」，不要估算冒充真实值。
- **小样本**：单机一周几十 run，P95/聚类不稳；M0 只做单 run 规则 + 同 domain 基线中位数。
- **label 偏差**：验证器通过≠业务成功；finding 的 confirmed/dismissed 人工反馈要回流成校准集。
- **多 agent/squad**：M0 只采集 owner run；sub-goal/verify 的 span 先按 run 边界记录，跨 run 树（goal 根）M1 再做。
- **数据保留**：span 默认保留 30 天，report/finding 长期；按 session 删除要级联。
- **命名**：「训练」容易被理解为训模型，UI 文案用「训练分析（记录并优化执行过程）」，文档明确不训权重。

---

## 6. 参考来源

学术（A）：
- DGM — arXiv:2505.22954；SICA — arXiv:2504.15228；RSI-Dojo — arXiv:2611.03698
- STOP — arXiv:2310.02304；ADAS — arXiv:2408.08435；AFlow — arXiv:2410.10762
- self-preference — arXiv:2404.13076；reward hacking — arXiv:2209.13085；ACE — arXiv:2510.04618；always-valid — arXiv:1512.04922
- Gödel Machine — Schmidhuber, 2003（无 arXiv）

业界（A-，官方文档）：
- LangSmith 数据模型 / Insights / few-shot evaluator / online evaluator / sampling：`docs.langchain.com/langsmith/*`
- Langfuse 数据模型 / 采样：`langfuse.com/docs/observability/*`
- Braintrust Loop / Patterns：`docs.braintrust.dev/loop/index.md`、`docs.braintrust.dev/observe/patterns/index.md`
- W&B Weave trace tree、Arize Phoenix tracing/evaluation、AgentOps core concepts、Helicone cost tracking
- OpenAI Agents SDK tracing、Claude Code monitoring（blocked_on_user）、Cursor Teams Analytics、Devin Session Insights
- DSPy optimizers：`dspy.ai/learn/optimization/optimizers/`
- Prometheus histogram 口径：`prometheus.io/docs/practices/histograms/`

未核实（C，仅留入口）：OTel GenAI semconv 属性名、Google ADK/Vertex Evaluation、Chrome perf bot、Uber M3、GitHub Copilot metrics 定义、OpenAI Evals 平台细节。两份分报告文末有完整清单与 URL。