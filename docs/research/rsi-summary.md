# agentwork 工作流级 RSI（W-RSI）：调研总结与落地设计

> 版本：2026-11-08（第三版）· 范围：工作流编排级自迭代；已定范围 T1 结构+T2 分派+T3 信息流；trace 采集退役；产出只读提案+复测计划
> 方法：两名调研 agent 分别取学术（`docs/research/rsi-academic.md`）与业界（`docs/research/rsi-industry.md`）官方来源；本文做交叉核对、指标统一定义、可优化判定与 agentwork 落地设计。
> 可信度约定：论文数字标 A（同行评审/arXiv 原文）；官方产品文档标 A-（厂商自述，机制可信、效果未独立验证）；媒体转述标 B；未取到正文的标 C 并注明「未核实」，本文不把 C 当论据。
> 局限：本机 `web_search` 上游 402，检索依赖已知 URL 直取 + 站点索引；部分厂商页只有导航壳（OTel GenAI 属性名、ADK/Vertex、Uber M3、Copilot 指标定义），已在两份分报告文末列为未核实项。

---

## 0. 一页结论

**agentwork 的 RSI 不是「再训练接入的 agent」，也不是只给单个 agent 调 harness；而是把整条 agent workflow 当作可观测、可归因、可复测的优化对象。** 接入的 agent 是执行器，我们不训练它的权重；我们负责的是它被如何编排：任务怎么拆、谁来做、依赖怎么连、哪些能并行、在哪里验证、失败如何回流。

```
运行中的编排图（谁→谁、并行/串行、等待/验证）
   → 节点与路径指标（关键路径、并行收益、排队、返工、验证开销）
   → 结构归因（该拆 / 该并 / 该换人 / 该减验证 / 该重排）
   → 策略提案（squad 编排策略、分派规则、平台约束）
   → 同题复测（A/B 两个编排变体）
   → 采纳 / 回滚
```

四件必须做对的事：

1. **编排必须可见。** 现在 squad 详情只有成员、仓库和任务数，goal 详情只有扁平的子任务列表；看不到实际 DAG、并行泳道、关键路径、验证与集成挂在哪。没有这张图，「哪块慢」只能猜。
2. **结构优先于节点参数。** 一个环节该拆成两个并行节点、两个节点该合并、验证放错了位置、任务分给了不合适的 agent——这些是结构问题，调 system prompt 救不回来。优化顺序应是：结构 → 节点 prompt/工具/模型 → 参数。
3. **工作流是「策略 + 运行时涌现」，不是固定画布。** 不做 Coze 式拖拽 DAG。leader 仍按真实任务动态拆解，平台只做三件事：记录实际图、暴露结构低效、用策略补丁（squad instructions、分派/验证/并发约束）收敛。固定画布只能覆盖可预先枚举的流程，长尾真实任务会把它撑破。
4. **先只读、再复测、后应用。** MVP 不自动改流程，也不自动改验证器；每条结构建议必须带图证据、预期效果、回滚点，复测通过才允许进入应用。

关键设计决策：

| 决策 | 选择 | 理由 |
|---|---|---|
| 优化对象 | **工作流图 + 节点策略**，不是模型权重 | 接入 agent 的能力边界不归我们；编排效率是我们能改的 |
| 图的来源 | 运行时重建（sub_goal/run/verification/change/edge），不要求用户先画 | 保留动态拆解能力，避免 Coze 式僵硬 |
| 第一优先 | 编排可见性（squad / goal / training 三处同一张图） | 看不见结构就无法判断串行、并行、返工与验证开销 |
| 指标核心 | 关键路径、并行收益、排队等待、返工率、验证/集成开销 | 「总时长」不能区分「活多」与「编排差」 |
| 结构发现 | 规则 + 图算法，LLM 只解释 | 结构结论必须可计算、可复核 |
| 提案 | `workflow.*` 策略补丁 + 人工批准 + 同题 A/B | 不直接改图，改的是产生图的策略与约束 |
| MVP | 图 + 三个视图 + 结构指标/发现 + 只读提案 | 先让「该拆该并」有证据，再谈自动调度 |
| 优化范围 | T1 结构 + T2 分派 + T3 信息流（乒乓/简报/consult 前移）；T4 节点内部不做主动优化；T5 平台参数只观测；T6 元层永不自动改 | 2026-11-08 与用户共同确定；每条建议可落到 squad instructions/分派规则/handoff 模板 |
| trace 采集 | **退役**：删除 trace_span 及 tool/usage 采集，分析只用实体级数据 | 工作流级判据全部可从 sub_goal/run/verification/gate 算出；span 级数据维护成本高、MVP 用不上 |

**不做**：训练模型权重；固定 DAG 画布；让 agent 改验证器/gate；无人值守自动重排流程；把一次执行的图当成稳定工作流；默认采集 prompt/文件全文。

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

### 2.7 工作流编排产品与 Coze 教训

工作流产品大致三派：

- **画布式低代码**（Coze / Dify / n8n）：节点、连线、变量映射都预先定义，适合确定性流水线；优点是可控、可复用，缺点是长尾任务需要不断加分支，运行黑盒，优化靠人重新画。Coze 的具体兴衰不在本文论证范围（C，行业观察），这里只取工程教训：**固定画布 + 黑盒执行 + 无反馈闭环，遇到真实复杂任务会同时失去表达力和可优化性**。
- **图即代码**（LangGraph）：状态、节点、边显式写在代码里，可测试、可版本化；但图仍由作者预先定义，agent 不能在执行中长出新节点。
- **动态多 agent**（OpenAI Agents SDK handoff、AgentOps、agentwork squad）：agent 在运行时决定交给谁、拆什么；表达力最强，但实际图是涌现的，平台默认看不见。

agentwork 在第三派。正确做法不是退回第一派，而是补上第三派最缺的东西：**运行时图重建 + 结构分析 + 策略级收敛**。图是观测结果，不是编排入口；入口仍是 squad instructions、leader 的分派行为和平台约束。这样既保留动态性，又能把「这次为什么慢」落到具体节点和边上。

## 3. 指标定义（agentwork 口径）

### 3.1 层级与数据源

| 层级 | 实体 | 数据源 | 现有可得性 |
|---|---|---|---|
| Workflow | 节点、边、泳道、关键路径、并行收益、排队/验证/集成开销 | `sub_goal` + `run` + `verification_result` + `change` + 新 `workflow_edge` | **需新增图重建** |
| Goal | 交付周期、人工迭代、验证通过率、总成本 | goal / run / verification_result / gate_decision | 大部分已有 |
| Run/Node | wall time、attempt/重试、验证结果、分派对象 | `run` + `verification_result`（实体级） | 已有 |
| Step | ~~工具耗时、重复调用、LLM turn 延迟~~ | ~~trace_span~~ | **随 trace 退役**，节点内部观测不在范围内 |
| Label | 验证 passed/rejected、gate 决定、人工驳回 | verification_result / gate_decision / human_iterations | 已有 |

工作流的「节点」不是新执行实体：owner run、sub-goal 的 assignee run、verify run、review run、consult run、集成动作都是节点；`sub_goal` 是业务工作项，`run` 是它的执行实例。图重建时以 `sub_goal_id` / `run.role` / `verification_result` / `change` 为锚，不复制执行状态。

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

**Workflow 级**

| 指标 | 公式 | 说明 |
|---|---|---|
| `node_count` / `edge_count` / `depth` / `max_fanout` | 图的规模与形状 | 先回答「这条流程有多复杂」 |
| `work_sum_ms` | `Σ 节点活跃时长` | 所有节点各干各的时间之和；不是墙钟 |
| `critical_path_ms` | 图上最长路径（含等待边） | 真正决定交付时长的路径 |
| `parallel_speedup` | `work_sum_ms / critical_path_ms` | ≥1；越大说明并行越有效 |
| `serialization_loss_ms` | `critical_path_ms - 最长单节点时长` | 关键路径上「排队等别人」的部分 |
| `queue_wait_ms` | `Σ(run.started_at - 节点就绪时间)` | 子任务已创建但没被调度的时间 |
| `lane_imbalance` | `max(泳道工作量) / avg(泳道工作量)` | 某 agent 忙死、其他闲着 |
| `verify_overhead_ratio` | `verify+review 时长 / work_sum` | 验证/审查本身吃掉多少产能 |
| `rework_ratio` | `rejected 验证轮次 / 总验证轮次` | 返工而非一次做对 |
| `handoff_count` / `consult_count` | 按 run.role / cancel_reason / 事件计数 | 接力与咨询的协作税 |
| `critical_path_share(node)` | `node.duration / critical_path_ms` | 单节点对交付的支配度 |
| `parallel_width_peak` | 同时活跃节点数的峰值 | 实际用了多少并发 |

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
3. **可干预**：存在允许的 patch 类型能改变它（workflow 结构/依赖/分派、节点 prompt/工具策略、上下文策略、并发/超时、模型路由）。
4. **可回滚**：有版本、diff 与回滚点；不能回滚的改动不进提案。

`bottleneck_score = critical_path_share × reducible_weight × confidence`，取最大者为「主瓶颈」。

### 4.2 七类节点瓶颈与判据

> **2026-11-08 退役说明**：trace_span 移除后，本表仅「重试放大型」「人工等待型」保留（run/gate 实体数据可算）；「串行依赖型」并入 4.3 结构判据；工具/LLM/上下文/重复四类依赖 span 级数据，随 trace 一并退役，不再实现。

| 类别 | 可计算判据（默认阈值） | 典型证据 | 建议动作 | patch target |
|---|---|---|---|---|
| 工具耗时型 | `tool_share ≥0.5` 且 top tool ≥0.25 | 某工具 span 长、输入大、输出大 | 缓存、批量命令、换更快工具、并行读 | tool_policy |
| LLM 耗时型 | `llm_share ≥0.6` 且 turn 数偏高 | 相邻 tool 之间长间隔、turn 多 | 缩短 system prompt、少样本、模型路由、减少无效 turn | prompt / model_route |
| 重试放大型 | `retry_ms/wall ≥0.2` 或 error rate ≥0.15 | 同工具反复失败、backoff 长 | 修根因、fail-fast、调整超时/重试上限 | workflow_param |
| 上下文压力型 | 峰值 `used/size ≥0.7` 且 ≥2 次；或每 turn 增长 ≥30% | usage_update、长工具输出 | 工具输出截断/摘要、按需检索、子 agent 隔离 | context_policy |
| 重复调用型 | `duplicate_ratio ≥0.15` 且 ≥2 对 | 相同 `(tool,args_hash)` 重复 | 结果缓存、playbook、把稳定查询固化为脚本 | tool_policy / skill |
| 串行依赖型 | 关键路径 ≥0.5 且相邻只读工具无重叠 | 瀑布里本可并行的读操作串行 | 并行工具调用、预取、拆分任务 | workflow_param |
| 人工等待型 | `human_wait / lead_time ≥0.3` | gate 请求到决定的间隔 | 预审、异步通知、批量 gate、默认低风险自动通过（需人配置） | 流程配置（非自改） |

### 4.3 七类结构瓶颈与判据

节点级瓶颈回答「这个 agent 在干什么慢」；结构级瓶颈回答「这条流程为什么慢」。后者优先级更高。

| 类别 | 可计算判据（默认阈值） | 图证据 | 建议动作 | patch target |
|---|---|---|---|---|
| 串行链过长 `serial_chain` | 关键路径 ≥0.6 且 `parallel_speedup <1.5`，链上 ≥3 个节点 | 长链、相邻节点时间不重叠 | 拆成可并行子任务，或把只读前置 | workflow.parallelize |
| 并行机会丢失 `lost_parallelism` | 两个节点无依赖边、时间不重叠，且总时长 ≥20% wall | 独立分支被排成先后 | 同时创建/调度，去掉隐式等待 | workflow.parallelize |
| 单节点过载 `split_candidate` | 单节点 `critical_path_share ≥0.5` 且 step 数 ≥2×中位数，或工具/文件簇 ≥2 组 | 一个节点包揽多个独立工作面 | 按文件/阶段拆成 2–3 个节点 | workflow.split |
| 碎片节点过多 `merge_candidate` | ≥3 个节点单节点 <2min、串行、单节点开销占比高 | 大量小节点与上下文切换 | 合并同类工作，减少派发/验证次数 | workflow.merge |
| 验证过重 `over_verification` | `verify_overhead_ratio ≥0.25` 且 `rework_ratio <0.05` | 验证/审查节点占关键路径 | 降级为抽样验证、合并 verify/review | workflow.verify_policy |
| 协作乒乓 `ping_pong` | 同一对 agent 的 consult/handoff 往返 ≥2 次 | 边来回、节点反复唤醒 | 明确 owner、一次问清、合并职责 | workflow.assignee / workflow.reorder |
| 分派错配 `wrong_assignee`（T2） | 某节点重试/失败率 ≥ 同 agent 基线 2×，或返工 ≥2 轮 | 节点失败/返工集中在同一 assignee | 换 agent、换模型、补上下文/工具 | workflow.assignee / model.node |
| 协作乒乓 `ping_pong`（T3） | 同对 agent 的 consult/handoff 往返 ≥2 次 | consult run 链、trigger_comment 往返 | 明确 owner、一次问清、知识前置到 brief | workflow.assignee / squad instructions |
| 简报过薄 `brief_quality`（T3） | rejected 组 sub_goal 的 description 信息量（长度/含验收标准/文件引用）系统性低于 passed 组 | sub_goal 文本特征 × verification 结果分组对比 | 拆分 checklist 写入 squad instructions（验收标准/文件/边界必填） | squad instructions |
| consult 未前移 `consult_repeat`（T3） | 同主题 consult 跨 goal 复现 ≥2 次 | consult 内容聚类（仅结构/标题级，不采正文） | 拆分规则补充该领域背景 | squad instructions |

图算法的判定必须写清楚「边是推断还是显式」：显式依赖来自 `sub_goal.depends_on`，推断依赖来自 `trigger_comment_id`、创建顺序与时间重叠。**推断边只能生成 L1 观察**；要升级 L2，至少 2 条同类工作流同向复现。

### 4.4 三级置信度

- **L1 观察**：本次 run 内可复现的 span 证据；只能写「本次」。
- **L2 归因**：同类 pattern 在 ≥3 个 run 或 ≥2 个 goal 出现，且方向一致；可写「反复出现」。
- **L3 已验证**：有 A/B 或 replay 复测，CI 不含 0；才允许写「改进有效」。

UI 必须显示级别，禁止把 L1 写成因果结论。

### 4.5 一个具体例子

某 run：wall 18m，工具 9.2m（51%），其中 `grep` 3 次同参数共 2.1m；上下文峰值 82%；gate 等待 6m；无错误。

- 主瓶颈：人工等待（6m/18m=33%，但 reducible 低，因为等人）→ 建议异步通知/预审，而不是「优化 agent」。
- 次瓶颈：重复 grep（duplicate_ratio=2/38=5.3%，低于 15%，不单列）。
- 上下文：82% 但只出现 1 次，不满足「≥2 次」→ L1 观察，建议在报告里提示，不生成 patch。
- 工具：51% 但 top tool 只占 12%，不满足 25% → 不判工具型，避免把「正常干活」误判为瓶颈。

这就是「判据 + 阈值」的意义：防止 LLM 看到「工具占 51%」就下结论。

---

## 5. agentwork 落地设计

### 5.1 概念与边界（含当前实现）

**三层优化对象，优先级从高到低：**

1. **编排结构**：节点拆分/合并、依赖与并行、验证与审查位置、集成顺序、交接与咨询关系。
2. **节点策略**：每个节点的 agent/模型选择、prompt、工具白名单、上下文与超时。
3. **执行参数**：并发数、重试上限、预算、采样。

M0（2026-11-05 落地）建了 `goal.execution_mode` 开关与 training_session/rsi_report/rsi_finding 骨架，但其观测建立在 span 级 trace 上，只回答「哪个 agent 的哪一步慢」。**2026-11-08 决策：trace_span 与 tool/usage 采集退役（彻底删除），训练/推理模式改用新语义**——推理模式零额外采集（实体数据本就落库），训练模式在终态冻结编排快照并异步做结构分析。M0 遗留的节点级规则与 Trace 瀑布 UI 一并下线。

W-RSI MVP 要补齐的缺口：

| 缺口 | 现状 | 影响 |
|---|---|---|
| 编排图不可见 | squad 详情只有成员/仓库；goal 详情是扁平 `GoalSubGoals` 列表 | 看不到 DAG、并行、关键路径、验证位置 |
| 无结构指标 | 分析器把所有 run 平铺 | 串行、并行丢失、验证过重无法量化 |
| 无结构发现 | 规则只有 tool/llm/retry/context/duplicate/serial/human_wait | 「该拆该并」没有类别与证据 |
| 无工作流提案 | `rsi.patch` 只到 prompt/工具/模型 | 优化只能落到单节点，改不了编排 |
| 无编排复测 | 没有变体快照与同题对比 | 结构改动无法证明有效，容易拍脑袋 |

**边界**：不训练接入 agent 的权重；不引入固定 DAG 编辑器；不自动改验证器、gate、评分；不在 MVP 自动应用结构提案。

### 5.2 数据模型

保留 M0 的 `training_session`、`rsi_report`、`rsi_finding` 与 goal 的 4 个字段；**`trace_span` 删除**（迁移里 DROP TABLE IF EXISTS）；`rsi_metric_snapshot` 退役（从未有写入方，趋势由轮次账本派生）。新增三处：

```sql
-- 显式依赖：leader 创建子任务时可声明；不填则运行时推断
ALTER TABLE sub_goal ADD COLUMN depends_on TEXT NOT NULL DEFAULT '[]'; -- JSON: ["<sub_goal_id>", ...]

-- 推断/显式边：节点由现有实体派生，边单独存，避免复制执行状态
CREATE TABLE IF NOT EXISTS workflow_edge (
    id         TEXT PRIMARY KEY,
    goal_id    TEXT NOT NULL,
    from_node  TEXT NOT NULL,          -- node key: goal / run:<id> / sg:<id> / verify:<id>
    to_node    TEXT NOT NULL,
    kind       TEXT NOT NULL,          -- depends_on|created|assigned|verified_by|reviewed_by|integrated|handoff|consult|triggered_by
    source     TEXT NOT NULL,          -- explicit|inferred
    confidence TEXT NOT NULL DEFAULT 'L1',
    created_at TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_workflow_edge_goal ON workflow_edge(goal_id, created_at);
CREATE INDEX IF NOT EXISTS idx_workflow_edge_from ON workflow_edge(from_node);

-- 冻结图快照，供报告与复测对比（图会随 goal 变化，必须冻结）
ALTER TABLE rsi_report ADD COLUMN workflow_json TEXT NOT NULL DEFAULT '{}';
```

节点不建表：`workflow_node` 由 `goal`、`run`、`sub_goal`、`verification_result`、`change` 实时装配，字段包括 `key/kind/label/assignee_id/status/started_at/ended_at/duration_ms/tokens/cost/retry_count/parent_key`。这样不会出现「节点表与 run 状态不一致」的第二套真相。

### 5.3 图重建与采集

**节点装配规则：**

| 节点 | 来源 | kind |
|---|---|---|
| goal 根 | `goal` | `goal` |
| owner 执行 | `run.role='owner'` | `agent` |
| 子任务执行 | `run.sub_goal_id` 对应的 assignee run | `agent` |
| 验证 | `verification_result` + 对应 verify run | `verify` |
| 审查 | `run.role='review'` | `review` |
| 咨询/接力 | `run.role='consult'`；`handoff_event` | `consult` / `handoff` |
| 集成 | `change` / `change_revision` / integrate 动作 | `integrate` |

**边装配规则：**

1. `sub_goal.depends_on` → `depends_on`（explicit）。
2. owner run → 它创建的 sub-goal：`run.trigger_comment_id` 或创建时 run 归属 → `created`（inferred）。
3. sub-goal → assignee run → verify run → verification_result：`assigned/verified_by`（结构关系，明确）。
4. `change.sub_goal_id` → 集成节点：`integrated`。
5. `run.role='consult'` 的 trigger/requester → `consult`；`handoff_event` → `handoff`。
6. 无法归因的边宁可不画：图里标 `inferred`，分析时只当 L1。

**实时性**：图查询是读时装配，不依赖异步 job；WS 事件（`sub_goal.*`、`run.terminal`、`goal:assigned`）到达时前端重新拉取。分析时把图冻结进 `rsi_report.workflow_json`，保证报告可回看。

### 5.4 分析流水线

```
goal 终态
  → 冻结 workflow_json（节点/边/关键路径）
  → 计算 workflow 指标（work_sum、critical_path、speedup、queue、verify overhead、rework、handoff）
  → 结构规则（4.3：T1 五类 + T2 分派 + T3 信息流）→ workflow finding（L1；跨 goal 复现升 L2）
  → 实体级节点规则（重试放大、人工等待）→ node finding
  → 合并报告：先结构、后节点；结构 finding 的 score 权重更高
  → 只读提案（可选）
```

- 关键路径用 DAG 最长路径（节点时长 + 等待边），不是 `run_sum`。
- 无显式依赖时，用 `trigger_comment_id` + 时间重叠推断边；推断边只在图里画虚线，不参与「串行链」判据的强结论。
- 结构 finding 的 `evidence_json` 必须带 `node_keys` 与 `edge_keys`，前端可高亮。
- 报告摘要模板固定为：主瓶颈节点 / 结构问题 / 可并行收益 / 验证与返工成本 / 建议。

### 5.5 API

| 方法 | 路径 | 说明 |
|---|---|---|
| GET | `/workflows/goals/{goalId}` | 节点、边、泳道、关键路径、节点指标；`?live=1` 读实时，否则读冻结快照 |
| GET | `/workflows/squads/{squadId}` | 小队拓扑（leader/members/reviewers）+ 最近 N 个 goal 的编排摘要 |
| GET | `/rsi/sessions/{id}/workflow` | 训练报告关联的冻结图 + 结构 finding |
| GET | `/rsi/sessions` | 列表增加 `critical_path_ms/parallel_speedup/workflow_findings` |
| GET | `/rsi/overview` | 增加 `avg_parallel_speedup/verify_overhead/rework_ratio` |
| PATCH | `/rsi/findings/{id}` | 已有；结构 finding 同接口 |
| POST | `/rsi/sessions/{id}/retry` | 已有 |

`/workflows/*` 与 `/rsi/*` 共用 service 层图装配器；handler 只做参数与序列化。

### 5.6 UI/UX

**三个入口，同一张图：**

1. **Squad 详情新增「编排」区**（不是新一级导航）：顶部拓扑卡（leader → members，role 标识），下面「最近执行」列表，每条 goal 一行 mini-DAG（节点数/并行度/关键路径/状态）；点开进 goal 编排视图。解决「squad 里看不出怎么编排」。
2. **Goal 详情**：把现有扁平「子任务」升级为「编排」视图——左列节点（owner/subgoal/verify/review/integrate），右列 Gantt/泳道；节点可展开看 run 耗时、工具、tokens、验证结果。保留「+ 拆子任务」入口。
3. **Training 详情新增「编排」Tab**：冻结图 + 关键路径高亮 + 结构 finding（点击 finding 高亮节点/边）+ 「该拆/该并/该换人」建议。

**不做画布编辑器**：MVP 只读；最多允许在节点上改 assignee/model（作为提案而非直接生效）。列表页 `/training` 增加两列：`并行收益`、`结构发现数`；空态解释「训练模式才会记录编排图」。

### 5.7 提案与复测

提案 target 扩展：

```json
{
  "target": "workflow.parallelize|workflow.split|workflow.merge|workflow.reorder|workflow.dependency|workflow.assignee|workflow.verify_policy|model.node|prompt.node",
  "scope": {"goal_id": "...", "node_keys": ["sg:..."], "squad_id": "..."},
  "hypothesis": "...",
  "expected_effect": {"metric": "critical_path_ms", "direction": "down", "min_pct": 10},
  "evaluation_plan": {"mode": "same_issue_ab", "variants": 2, "min_pairs": 5},
  "risk": "low|medium|high",
  "rollback": "squad_policy_version / prompt_version"
}
```

结构提案不直接改图，而是改「产生图的策略」：squad instructions 的拆分规则、leader 的并行/验证约束、平台并发与派发配置。复测用同一 issue/domain/base commit 跑两个变体，比较 `critical_path_ms`、`verify_pass_rate`、`cost_per_success`，护栏不退化才采纳；MVP 只生成提案与复测计划，不自动应用。

### 5.8 MVP 能力清单与分期

| 阶段 | 能力 | 验收 |
|---|---|---|
| P0 可见性 | **训练/推理模式（新语义：推理零采集、训练冻结快照+异步分析）**；trace 采集与 trace_span 表退役；图装配器 + 3 个 API；Squad 编排区；Goal 编排视图；Training 编排 Tab | 任意有 sub_goal 的 goal 能看到正确 DAG、泳道、关键路径；实时状态随 WS 更新；推理 goal 无任何额外写放大 |
| P1 可判定 | workflow 指标 + 4.3 九类规则（T1 五类 + T2 + T3）+ 报告结构段 + 列表两列 | 构造「串行可并行」「反复 consult」样例能命中对应规则，证据可点回节点；简报质量类在样本 <10 时保持 L1 观察态 |
| P2 可提案 | 只读 `workflow.*` 提案 + 证据/预期效果/回滚点 | 每条提案能从 finding 生成，字段通过 schema 校验 |
| P3 可复测（MVP 边界外） | 同题 A/B、变体快照、对比报告 | 两变体同 issue 可对比；护栏/主指标输出 CI |

**MVP = P0 + P1 + P2**（只读闭环），P3 作为下一阶段。P0 不依赖分析 job，先让图可见；P1 复用现有 `rsi_analyze`；P2 复用 finding 详情。

### 5.9 文件级拆分

Go：
1. `internal/service/workflow.go`：图装配、节点/边、关键路径、指标（纯函数，便于单测）。
2. `internal/store/schema.sql` + `store.go`：`workflow_edge`、`sub_goal.depends_on`、`rsi_report.workflow_json`。
3. `internal/service/rsi_analyze.go`：结构规则、报告结构段、finding evidence。
4. `internal/server/handler/workflow.go` + `handler.go`：3 条路由。
5. `internal/service/collab.go`（或 sub-goal 创建路径）：`depends_on` 透传与校验（M1 调度前只存不算）。

Web：
6. `web/lib/{types,queries,api}.ts`：Workflow 类型与 hooks。
7. `web/components/workflow/{graph,gantt,node-card,mini-dag}.tsx`：复用一套只读图组件。
8. `web/app/squads/[id]/page.tsx`：编排区。
9. `web/app/goals/[id]/page.tsx` + `goal-subgoals.tsx`：编排视图。
10. `web/app/training/[id]/page.tsx` + `components/training/*`：编排 Tab。

测试：图装配单测（分叉/合流/串行/缺失边）、关键路径单测、结构规则样例、API handler、`npm run build`。

### 5.10 风险与开放问题

- **图推断错误**：`trigger_comment_id` 缺失或 leader 先建后派时，边可能错；推断边只给 L1，UI 用虚线，结论必须说明「推断」。
- **固定画布回潮**：一旦提供可视化编辑，产品会被拉回 Coze 路径；MVP 坚持只读，改动走策略提案。
- **观测者效应**：训练模式额外落盘会拉长 wall；图指标与推理 run 分开统计。
- **小样本**：单机一周几十个 goal，结构规则先做「本次可证」，跨 goal 复现才升 L2；不要用一次图下结论。
- **并行不是免费**：拆分会增加 worktree、上下文重建、集成冲突；`parallel_speedup` 必须与 `cost_per_success`、`rework_ratio` 一起看。
- **权限**：谁能改 squad 编排策略？MVP 沿用 squad 编辑权限；结构提案的应用必须留审计。

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