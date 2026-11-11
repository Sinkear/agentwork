# RSI 能力自迭代：业界调研

> 调研对象：agent 可观测性 / 评测平台、agent 自优化产品、训练-推理开关与可视化范式。
> 目的：为 agentwork（Go daemon + Next.js，goal → run → 验证 → 交付，SQLite）设计「训练模式 + 异步训练分析 Tab」提供可借鉴的业界依据。
> 方法：官方文档直取（2026-11 抓取）。web_search 在本机因上游 402 不可用，改用已知 URL + 站点 `llms.txt` 索引 + 本地全文检索；抓取失败或只能拿到导航壳的页面统一列入文末「未核实项」，不做脑补。

---

## 0. 结论速览

1. **trace 的行业标准形状是「树」**：run/span 为节点，parent_span_id 成树，root span 代表一次请求。LangSmith、OpenAI Agents SDK、Arize Phoenix、AgentOps 都是这个模型，差异只在「span 类型枚举」和「容器层级」（project / agent / environment / session）。
2. **Langfuse 走的是「树在语义、单表在存储」**：observation 可嵌套，但 trace 级属性（user_id、session_id、tags、metadata）会复制到每一条 observation 行，只留一张宽表换取查询与聚合性能。单机 SQLite 场景这个取舍非常值得抄。
3. **会话是第二层聚合**：LangSmith 的 thread、OpenAI 的 group_id、AgentOps 的 session、Langfuse 的 session 都在回答「这十几个 trace 属于同一次多轮任务」。agentwork 的 goal 天然就是这一层，不需要再造概念。
4. **指标面板的最小集合**：trace 瀑布（各 span 起止 + 耗时占比）、token/cost、错误率、重试次数、**人工等待时长**。最后一项被大多数平台忽略，Claude Code 却专门定义了 `claude_code.tool.blocked_on_user`（duration_ms + decision + source）——这是最值得直接抄的指标。
5. **异步分析已经是成熟产品形态**：LangSmith Insights（后台 job，最长 30 分钟，双模型：聚类模型 + 摘要模型，样本上限 1000 trace，1000 threads 约 $1–4）、Braintrust Patterns（定时巡检、发现带证据、可复核/关闭、可一键转成 scorer 或修复任务）。二者正是 agentwork「训练分析 Tab」的直接对标。
6. **自优化的闭环不是「训练模型」，而是「trace → 数据集 → 评测 → 改 prompt/策略 → 复测」**。DSPy 把这套做成 optimizer（BootstrapFewShot / MIPROv2 / GEPA），Braintrust 把它产品化成 Loop + Experiments + Scorers。agentwork 第一版不应该训练权重，而应该优化 prompt、工具调用策略和验证规则。
7. **开关必须分三层**：采集开关（是否记录）、内容开关（记录不记录 prompt/文件正文）、训练开关（数据能否进入分析/数据集）。OpenAI Agents SDK 默认开 trace 但可用 `trace_include_sensitive_data=False` 只留结构；Claude Code 默认不采集 prompt 内容、只记长度，需 `OTEL_LOG_USER_PROMPTS=1` 才开。把三者合并成一个「训练模式」布尔值是产品设计上的错误。
8. **成本控制靠三层闸门**：概率采样（LangSmith `LANGSMITH_TRACING_SAMPLING_RATE`）、规则过滤 + 采样率（online evaluator 的 filter + sampling + spend limit）、分析侧样本上限（Insights 1000 条）。agentwork 单机也应该有「每日分析预算」而不只是条数上限。
9. **可视化不要发明新范式**：W&B Weave 的三栏（列表 / trace 树 / 详情）+ 四种视图（Trace tree、Code composition、Flame graph、Graph）+ 五种 scrubber（Timeline、Peers、Siblings、Stack、Path）已经是充分验证的信息架构；agentwork 直接做「列表 + 瀑布 + 详情 + 对比」即可。
10. **agentwork 有一个别人没有的优势：验证结果就是天然的 label**。goal → run → 验证 → 交付让每条 trace 自带成功/失败标签，不需要像 LangSmith 那样靠人工 correction 或 LLM-as-judge 去造标签。训练分析的第一版应该围绕「验证通过/失败 + 人工干预事件」做归因，而不是先做聚类。

---

## 1. Trace 数据模型：四种流派

### 1.1 树形 run/span（主流）

LangSmith 的定义最完整：一次操作触发的所有 run 属于同一个 trace，用 trace_id 绑定；run 等价于 span，可嵌套；**单 trace 上限 25,000 runs**，超出后新 run 被拒。再往上一层是 thread：一次多轮会话的 trace 序列，靠 `thread_id` 归组；trajectory 则是 thread 的扁平投影——human/AI/tool 消息各出现一次、按首次出现排序、去掉嵌套。

容器层级是 org → workspace → agent → environment（Production / Staging / Development / Local）。这一点很关键：**环境是一等公民**，查询生产行为时不需要手工排除测试流量。

- https://docs.langchain.com/langsmith/observability-concepts

OpenAI Agents SDK 的 span 类型可以直接当作 agent 运行时的「标准动作表」：`task_span`（runner 调用）、`turn_span`（每个模型 turn）、`agent_span`、`generation_span`、`function_span`、`guardrail_span`、`handoff_span`、`transcription_span`、`speech_span`。每个 span 有 `started_at / ended_at / trace_id / parent_id / span_data`。

- https://openai.github.io/openai-agents-python/tracing/

Arize Phoenix 用 OpenInference 约定 `openinference.span.kind`，枚举为 Chain、Retriever、Reranker、LLM、Embedding、Agent、Tool、Guardrail、Evaluator、Decision、Prompt；project 是 trace 的容器。

- https://arize.com/docs/phoenix/tracing/concepts-tracing

AgentOps 在此基础上加了业务语义层级：session（`init()` 自动创建）→ agent → workflow → operation/task → LLM / tool。

- https://docs.agentops.ai/v2/concepts/core-concepts

### 1.2 单表 observation（Langfuse）

Langfuse 只有三个概念：observation（单步：LLM 调用、工具调用、retrieval，可嵌套，类型含 generation / event / span）、trace（同一 trace_id 的逻辑分组）、session。它的工程取舍是：**trace 级属性复制到每行 observation，物理上只有一张 observations 表**，以此避免 join、加速过滤与聚合。数据基于 OpenTelemetry，导出走后台批量，短生命周期进程必须 `flush()`。

- https://langfuse.com/docs/observability/data-model

对 agentwork 的启示：单机 SQLite 不要做「trace 表 + span 表 + 事件表」的过度规范化；一张 `trace_spans` 宽表 + 少量关联表（标注、发现）就够，树关系用 `parent_span_id` 表达，查询用 `(session_id, started_at_ms)` 复合索引。

### 1.3 会话层与「轨迹」

- thread（LangSmith）/ group_id（OpenAI）/ session（Langfuse、AgentOps）解决「多次交互属于同一任务」。
- trajectory（LangSmith）是把会话压平成消息序列，用于喂给 LLM 分析或做数据集。压平会丢嵌套，但换来 token 可控与可比较。

agentwork 的映射：**goal ≈ thread**，**run ≈ trace**，**step/tool call ≈ span**。这个映射写进设计文档，后面所有指标口径都以它为准。

### 1.4 富化层：feedback / annotation / attributes

- LangSmith：Feedback = tag + score，绑定 `run_id`，score 可连续可离散；另有 Tags、Metadata。人工 correction 还能自动进入 corrections dataset，回填成 judge prompt 的 few-shot 示例（默认 5 条）。
  - https://docs.langchain.com/langsmith/create-few-shot-evaluators
- Claude Code：`tool_decision`（accept/reject）、`code_edit_tool.decision` 都是「人对 agent 的决策」，被当成一等指标而非日志。
- Devin Session Insights：会话结束时自动生成轻量分类（category、languages、tools），大会话（L/XL）在 teardown 时自动跑完整分析，小会话可手动触发。
  - https://docs.devin.ai/product-guides/session-insights

---

## 2. 指标面板、采样与异步分析管线

### 2.1 面板该有什么

把各家面板拆开，稳定出现的是六块：

1. **trace 瀑布**：每个 span 的起止、耗时、父子关系、并行分支；可展开到 tool 入参/出参。
2. **token / cost**：按 trace、按 run、按模型聚合；Helicone 甚至做到逐请求成本计算（含流式/非流式差异）。
   - https://docs.helicone.ai/guides/cookbooks/cost-tracking
3. **错误率**：按 error class 分组，而不是只给一个总数。Claude Code 的 `tool.execution` 事件带 `success` 与 error 类别（如 `Error:ENOENT`、`ShellError`）。
4. **重试**：`api_error`、`api_retries_exhausted` 这类独立事件比「重试率」一个数字更有用。
5. **人工介入**：`claude_code.tool.blocked_on_user`（duration_ms，decision=accept/reject/cancelled，source=config/hook/user_temporary）。这是把「人等待」与「机器耗时」分开的关键设计。
   - https://code.claude.com/docs/en/monitoring-usage
6. **吞吐/活跃**：session.count、active_time.total、lines_of_code、commit、PR 数——把 agent 产出折算成业务单位。

### 2.2 采样与存储成本

- LangSmith：`LANGSMITH_TRACING_SAMPLING_RATE ∈ [0,1]` 做概率采样；也可在 Client 上按操作采样。**采样（概率）与 conditional tracing（确定性开关）是两件事**，文档专门做了对比。
  - https://docs.langchain.com/langsmith/sample-traces
- LangSmith online evaluator：filter + sampling rate + spend limit，三者叠加，避免生产流量把评测预算烧光。
  - https://docs.langchain.com/langsmith/online-evaluations-llm-as-judge
- LangSmith SaaS 保留 180 天，dataset 永久保留。**原始 trace 短期、提炼后的数据集长期**，这是成本与价值的分界线。
- Langfuse：基于 OTel 的后台批量导出；进程退出前必须 flush，否则丢数据。
  - https://langfuse.com/docs/observability/features/sampling

### 2.3 异步分析管线（最重要的对标）

**LangSmith Insights**：后台 job（最长 30 分钟），"Insights" tab 列出报告；执行摘要给关键发现的百分比 + 可点击的 trace 引用；顶层 category 带分布条与聚合指标（error rate、latency、cost、feedback 分、attributes），可下钻 subcategory → traces table → 单 trace。配置上有两个值得抄的点：

- **双模型分工**：thinking model 负责聚类，summarization model 负责逐 trace 摘要；成本基准是 1000 threads ≈ $1.00–2.00（OpenAI）/ $3.00–4.00（Anthropic）。
- **分析输入可控**：sample size 上限 1000 traces，summary prompt 是 Mustache 模板，可引用 `{{run.inputs}} {{run.outputs}} {{run.error}} {{run.feedback}} {{all_thread_messages}}`，支持 attributes 抽取与 feedback 过滤；支持 daily/weekly/cron 定时。

- https://docs.langchain.com/langsmith/insights

**Braintrust Patterns**：定时让 Loop（内置分析 agent）巡检 trace，产出「发现（pattern）」——每条带证据、解释、建议的下一步、severity、category；发现会被刷新（更新证据与受影响 trace 数）、会在行为连续两个工作日不再出现时被关闭；支持把发现一键转成 scorer、分类器或编码 agent 任务。Loop 读操作不打断人，写操作（改 prompt、建 dataset、开 PR）要人批准。

- https://docs.braintrust.dev/observe/patterns/index.md
- https://docs.braintrust.dev/loop/index.md

这两者证明：**「分析」必须是一个有状态、可调度、可复核的后台实体，而不是一次性的聊天回答**。

---

## 3. 自优化能力盘点

### 3.1 trace → dataset → eval → prompt（产品化闭环）

Braintrust 把自己定位成 active observability：production traces → human feedback → test datasets → experiments → quality scores → prompt iteration → model routing → deployment。

- https://docs.braintrust.dev/loop/index.md

LangSmith 的路径更细：生产 trace 上挂 online evaluator（LLM-as-judge 或代码规则）→ 低分 trace 进 dataset → 改 prompt 后在 dataset 上跑 experiment → 对比。few-shot evaluator 还能用人工 correction 自动改进 judge 本身。

- https://docs.langchain.com/langsmith/create-few-shot-evaluators

Arize Phoenix：OpenInference span + 预置/自定义 LLM-as-judge + 代码 evaluator + 数据集批量评测，实验在 Playground 中逐次记录分数。

- https://arize.com/docs/phoenix/evaluation/llm-evals

### 3.2 optimizer 层（真正「自动调」的部分）

DSPy 的定义很清楚：optimizer 是「调程序参数（prompt 和/或 LM 权重）以最大化指标」的算法；输入是 program + metric + 少量训练样本（5–10 条，可以无标签）。具体方法：

- BootstrapFewShot / LabeledFewShot / BootstrapFewShotWithRandomSearch：跑程序、收集成功轨迹、合成 few-shot 示例。
- MIPROv2：三阶段——bootstrapping（收集高分轨迹）→ grounded proposal（依据代码/数据/轨迹起草指令）→ discrete search（小批量评估候选、更新代理模型）。
- GEPA、BootstrapFinetune（构造数据集微调权重）。
- 优化器可组合（BetterTogether），并可对 top-5 候选做 Ensemble。

- https://dspy.ai/learn/optimization/optimizers/

对 agentwork 的直接含义：**「训练」= 用历史 run 自动改 prompt / few-shot / 工具选择策略 / 验证阈值，然后用验证集复测**。这与 DSPy 的 train→compile→eval 结构同构，只是作用对象从「DSPy program」变成「goal 的 system prompt + 工具白名单 + 验证规则」。

### 3.3 产品遥测反哺

- Cursor Teams Analytics：AI code tracking 在设备上记录每条 AI 建议行的签名，之后与该用户后续 git commit 比对来判定「AI 代码占比」；提供 AI Share、Agent Edits（suggested vs accepted）、Tab Completions 等图表，支持 CSV 导出与 Admin API（Enterprise）。
  - https://cursor.com/docs/account/teams/analytics
- Devin Session Insights：把「本次会话值不值」压缩成 ACU 消耗、用户消息数、会话规模、类别、问题时间线、可执行建议、知识库使用情况。
  - https://docs.devin.ai/product-guides/session-insights

### 3.4 传统性能工程的「发现→归因→修复→复测」

Chrome perf bot 与 Uber M3 是常被引用的样本，但本次 **抓取失败**（见文末未核实项），不引用其具体机制。可以确认的同类做法来自 Braintrust Patterns 的验证逻辑：按 model / agent 设置 / 集成 / 实验变体分组对比，判断某个行为是否真的只出现在某一组——这就是性能工程里的「按维度切分归因」。

### 3.5 平台对比总表

| 平台 | 数据模型 | 指标面板 | 自优化能力 | 可借鉴点 |
|---|---|---|---|---|
| LangSmith | run=span 树，trace/thread/trajectory 三层；容器为 agent + environment | trace 瀑布、cost/latency、error rate、feedback 分布 | Insights 自动聚类 + few-shot evaluator 自我改进入 + online evaluator | 分析报告要「结论带百分比 + 可点击证据」；双模型分工控成本 |
| Langfuse | observation/trace/session；单表宽行，trace 属性冗余 | trace 详情、generation 级 token/cost、session 视图 | LLM-as-judge、数据集实验、prompt 管理 | 单机存储用宽表 + 冗余字段，别过度规范化 |
| Braintrust | trace + span，project 为容器；scorer 为一等实体 | logs 表、trace 视图、experiments 对比 | Loop（分析 agent）+ Patterns（定时巡检、发现可复核/关闭/转任务） | 「发现」要有状态、证据、severity；写操作必须人工批准 |
| W&B Weave | op/call 树，trace 为调用集合 | 三栏 + 四视图 + 五 scrubber，tokens/cost/latency 可开关 | 评测表、数据集、trace 对比 | 同一对象提供多种可视化心智模型； scrubber 提升定位效率 |
| Arize Phoenix | OTel + OpenInference，span kind 枚举 | trace 瀑布、span 属性、实验分数 | 预置/自定义 LLM-as-judge、代码 evaluator、批量评测 | span kind 枚举可直接映射为 agentwork 的 span 类型 |
| AgentOps | session→agent→workflow→operation→LLM/tool | 会话回放、成本、错误 | 较弱，偏调试 | 会话/工作流层级可直接映射 goal/run/step |
| Helicone | 请求级代理日志 | 成本、缓存命中、延迟、按用户/自定义属性聚合 | 告警、报告 | 成本要能逐请求算，且区分缓存与流式 |
| OpenAI Agents SDK | trace + 10 类 span，group_id 关联会话 | 依赖下游平台（本身是埋点层） | 无 | span 类型表可直接作为 agentwork 的埋点规范 |
| Claude Code | OTel metrics + events | 会话数、成本、token、代码行、PR、人工等待 | 无（数据出到外部平台） | `blocked_on_user` 式人工卡点指标；默认最小采集 |
| Cursor Analytics | 设备端签名 + git 比对 | AI 代码占比、Agent Edits、Tab 补全接受率 | 无（洞察驱动人工改进） | 用「最终产物」而非「过程日志」做归因 |
| Devin Session Insights | 会话级分析报告 | ACU、消息数、规模、问题时间线 | 自动分类 + 可执行建议 | 会话结束即自动产出轻量结论，大会话才跑重分析 |
| DSPy | program + optimizer（无 trace 产品） | 依赖外部 | BootstrapFewShot / MIPROv2 / GEPA 自动改 prompt 与示例 | 优化 = 指标 + 训练样本 + 搜索，不需要训练权重 |

这张表的核心结论是：**平台的能力分布是「埋点 → 面板 → 分析 → 优化」四级阶梯**，多数产品停在第二、三级；agentwork 若要自称 RSI，必须做到第四级（提案 + 复测），否则只是一个更好看的 trace 浏览器。

---

## 4. 训练/推理开关的产品形态

### 4.1 三种开关语义必须分开

| 层 | 回答的问题 | 业界实现 |
|---|---|---|
| 采集开关 | 要不要记录 trace | OpenAI 默认开；`OPENAI_AGENTS_DISABLE_TRACING=1` 关；Claude Code 需 `CLAUDE_CODE_ENABLE_TELEMETRY=1` 才开 |
| 内容开关 | 记录不记录 prompt / 文件正文 | OpenAI `trace_include_sensitive_data`（默认 True，关掉后 span 只留结构，Responses span 保留 response_id）；Claude Code 默认不采集 prompt 内容、只记长度，`OTEL_LOG_USER_PROMPTS=1` 才开，工具细节另需 `OTEL_LOG_TOOL_DETAILS` |
| 训练开关 | 数据能否进入分析/数据集 | LangSmith 用 dataset 与 sampling 隔离；Braintrust 用 project + 数据集权限；OpenAI ZDR 组织直接不可用 tracing |

- https://openai.github.io/openai-agents-python/tracing/
- https://code.claude.com/docs/en/monitoring-usage

Claude Code 还有一条很硬的产品规则：**仓库里的 `.claude/settings.json` 不能开启遥测或内容采集（会被忽略），但可以把 exporter 设为 none 关闭信号**；managed settings 还会移除冲突的开发者变量。也就是说「组织策略 > 项目配置 > 个人配置」，且关闭能力永远大于开启能力。agentwork 若面向团队，应照抄这个优先级。

### 4.2 采样策略

- 概率采样：按 trace 或按操作，`sampling_rate ∈ [0,1]`。优点是实现简单、成本线性；缺点是会漏掉稀有失败。
- 规则采样：按 filter 命中再按比例采（LangSmith online evaluator）。适合「只分析失败/低分/高成本」的场景。
- 分层采样：按 goal 类型、模型、工具分层，保证每层都有样本。单机小样本场景比纯随机更靠谱。
- 尾部保留：所有失败 trace 100% 保留，成功 trace 低比例保留——这是 agentwork 应该做的默认策略。

### 4.3 标注与 reward

- 显式：feedback score、accept/reject 决策、人工修改后的最终产物（correction）。
- 隐式：验证通过/失败、重试次数、人工接管、交付后被回滚。
- LLM-as-judge：便宜但有漂移，需要定期用人工标注校准；LangSmith 的 few-shot correction 就是校准手段。

agentwork 的天然 label 是「验证结果 + 人工干预事件」，隐式信号比显式打分便宜一个数量级，第一版应优先用隐式信号。

---

## 5. 可视化范式

### 5.1 W&B Weave 的三栏 + 四视图

Traces 页由三块组成：左侧 trace 列表（可排序分页，带 tokens / cost / latency）、中间 trace 树（`@weave.op()` 调用层级）、右侧选中 op 的详情。树上方可按名字/类型正则过滤（如 `tool`、`openai.response.create`），并可开关 cost / tokens / latency 的显示。

同一页面内可切换四种视图：**Trace tree（默认）、Code composition、Flame graph、Graph**。树下方有五个 scrubber：Timeline（时间顺序）、Peers（同类型下一个）、Siblings（同父下一个）、Stack（上下调用栈）、Path（仅代码组合视图，沿相同代码路径跳转）。

- https://weave-docs.wandb.ai/guides/tracking/trace-tree

这套 IA 的价值在于：**同一个对象（trace）提供多种心智模型**，调试看树、看热点看火焰图、看数据流看 graph，且切换不丢选中态。

### 5.2 LangSmith Insights 的下钻路径

报告列表 → 报告详情（执行摘要 + 百分比结论 + 可点击 trace 引用）→ 顶层 category（分布条 + 聚合指标）→ subcategory → traces table → 单 trace。这是一条「结论 → 证据」的可验证路径，而不是一张静态图表。

- https://docs.langchain.com/langsmith/insights

### 5.3 对比视图

- LangSmith trajectory 可把多次会话压平成消息序列做并排比较。
- Braintrust Patterns 的 verification 按维度分组对比（模型/设置/集成/实验变体）。
- CI 场景的常规做法是「本次 vs 基线」双列 waterfall + 差值列。

agentwork 的训练分析 Tab 应该有一个固定入口：**本次 run vs 基线 run 的双瀑布对比**，差异 span 高亮，顶部给 cost / latency / steps 的 delta。

---

## 6. 指标定义：口径与陷阱

| 指标 | 建议口径 | 陷阱 |
|---|---|---|
| 成功率 | 成功 run 数 / 进入终态的 run 数；终态 = delivered 或 failed，cancelled 单列 | 分母含未结束 run 会虚低；验证器假阳性会把失败算成功；重试 run 算一条还是多条必须写死 |
| cost per success | 区间内总成本（含失败、含重试、含分析成本）/ 成功 run 数 | 只统计成功 run 的成本会严重低估；缓存命中价与订阅制会让 USD 失真，需同时给 token 口径 |
| P50 / P95 延迟 | 用直方图/原始样本算，不用「平均的分位数」 | Prometheus 明确说分位数不可聚合，summary 的预计算分位数无法跨实例合并；样本少（<20）时 P95 无意义 |
| 步骤数 | step 定义固定为「模型 turn」或「工具调用」，二者不可混用 | 步骤多不必然差（复杂任务），要按任务类型分层 |
| 工具调用次数 | 按 tool name 分组，含失败调用 | 只统计成功调用会掩盖「反复试错」 |
| 重试率 | 显式 retry 次数 / 总调用次数；同参数重复调用单独记为「重复调用」 | 把「失败后重试成功」直接算成功会掩盖稳定性问题 |
| 上下文 token 占用 | 峰值上下文 + 累计计费 token 两个数 | 只报累计会低估单次爆上下文的风险；cache read/write 要分列 |
| 人工卡点耗时 | 独立 span/事件，duration_ms + decision + source（Claude Code 口径） | 混进 agent 延迟会污染 P95；人工等待常是长尾主因 |
| MTTR | 从首次失败/告警到「连续 N 次成功」的时间 | 「恢复」定义不写死就不可比；单机场景 N 建议 ≥3 |

Prometheus 关于 histogram / summary 的说明是分位数口径的权威依据：summary 在客户端预计算分位，无法再聚合；histogram 保留桶，可跨维度聚合后再算任意分位。

- https://prometheus.io/docs/practices/histograms/

---

## 7. 给 agentwork 的落地建议

### 7.1 新 Tab 的信息架构（与 goal 齐平）

顶层导航：`Goals` / `Runs` / **`Training`** / `Settings`。Training 与 Goals 平级，因为它回答的是「我的 agent 有没有变强」，而不是「某个 goal 怎么样了」。

三级结构：

```
Training
├─ 会话列表（默认）
│   列：goal 名 / 模式(训练|推理) / 时间 / trace 数 / 状态徽章 / 成本 / 发现数 / 成功率
│   筛选：模式、状态、时间、是否有高危发现
│   顶部：4 张卡（成功率、cost per success、P95、人工等待总时长）
├─ 会话详情 (training_session)
│   Tab：概览 | 发现 | Trace | 对比 | 标注 | 提案
│   ├─ 概览：结论摘要（3–5 条，带百分比）+ 指标卡 + 成本明细
│   ├─ 发现：按 severity 排序的 finding 列表 → 点击展开证据 span + 建议动作
│   ├─ Trace：左侧 run 列表，中间瀑布，右侧 span 详情（对齐 Weave 三栏）
│   ├─ 对比：本次 vs 基线双瀑布，delta 表（cost/latency/steps/retry）
│   ├─ 标注：待人工确认的样本队列（成功/失败/存疑）
│   └─ 提案：prompt/策略 diff + 离线复测结果 + 应用按钮（默认禁用）
└─ 全局视图
    ├─ 指标趋势（按周）
    ├─ 模式库（findings 聚类后的长期问题，参考 Patterns）
    └─ 任务历史（job 状态机全量）
```

与 goal 详情页的关系：goal 详情里加一个「训练」小节，展示本次 goal 是否处于训练模式、trace 是否已封存、分析 job 状态，并提供跳转到 Training 详情的链接。**不复制面板**，只做入口与状态。

### 7.2 异步训练任务状态机

```
                 ┌──────────── cancelled ◄── 用户取消（任何非终态）
                 │
queued ──► collecting ──► sealing ──► analyzing ──► needs_review ──► applied ──► verified
  │            │             │            │              │              │
  │            └─ failed_retryable ◄──────┴──────────────┘              └─ regressed
  │                     │
  └─────────────────────┴──► failed_terminal
```

规则：

- **queued**：goal 进入训练模式时创建，`idempotency_key = session_id`。同一 session 只允许一个活跃 job。
- **collecting**：run 执行期持续写 span；goal 完成（delivered/failed）后自动进入 sealing。
- **sealing**：封存 trace、冻结样本集、计算快照指标；此阶段可重试，必须幂等。
- **analyzing**：调用分析器（本地规则 + LLM）。按阶段 checkpoint，支持断点续跑；单阶段超时（建议 10 分钟）+ 全局超时（30 分钟，对齐 Insights）。
- **needs_review**：产出 findings 与 proposal，等人确认。**没有人工确认不允许 applied**。
- **applied**：写入配置变更（prompt 版本、阈值），记录 diff 与回滚点。
- **verified / regressed**：用后续 N 次 run 复测；regressed 自动回滚并生成新 finding。
- 失败分支：`failed_retryable`（网络/模型限流/超时）最多重试 3 次，指数退避；`failed_terminal`（数据损坏、schema 不兼容）需人工介入。
- 心跳与租约：daemon 重启后扫描 `status IN (collecting, sealing, analyzing)` 且 `lease_expires_at < now` 的 job，标记为 retryable 并重新入队。

### 7.3 SQLite 表设计建议

单机 SQLite 的约束：单写者、WAL、无分区。设计原则是「宽表 + 少量关联表 + 大文本外置」。

```sql
-- 训练会话：与 goal 关联，承载模式与预算
CREATE TABLE training_sessions (
  id            TEXT PRIMARY KEY,           -- ULID
  goal_id       TEXT NOT NULL,
  mode          TEXT NOT NULL CHECK (mode IN ('off','sample','full')),
  sample_rate   REAL NOT NULL DEFAULT 1.0,
  content_capture TEXT NOT NULL DEFAULT 'redacted'
                CHECK (content_capture IN ('none','redacted','full')),
  status        TEXT NOT NULL,
  budget_usd    REAL,
  trace_count   INTEGER NOT NULL DEFAULT 0,
  started_at_ms INTEGER NOT NULL,
  sealed_at_ms  INTEGER,
  created_at_ms INTEGER NOT NULL
);
CREATE INDEX idx_ts_goal ON training_sessions(goal_id, created_at_ms DESC);

-- span 宽表：树用 parent_span_id 表达，trace 级属性冗余进来（抄 Langfuse）
CREATE TABLE trace_spans (
  id             TEXT PRIMARY KEY,
  session_id     TEXT NOT NULL,
  run_id         TEXT,
  span_id        TEXT NOT NULL,
  parent_span_id TEXT,
  kind           TEXT NOT NULL,   -- goal|run|step|llm|tool|verify|human_wait
  name           TEXT NOT NULL,
  started_at_ms  INTEGER NOT NULL,
  ended_at_ms    INTEGER,
  duration_ms    INTEGER,
  status         TEXT,            -- ok|error|cancelled
  error_class    TEXT,
  retry_count    INTEGER DEFAULT 0,
  tokens_in      INTEGER, tokens_out INTEGER,
  cost_usd       REAL,
  attrs_json     TEXT,            -- 小字段，< 4KB
  content_ref    TEXT,            -- 大文本外置：内容寻址文件 (sha256)
  trace_id       TEXT NOT NULL,   -- 冗余，避免 join
  metadata_json  TEXT
);
CREATE INDEX idx_spans_session ON trace_spans(session_id, started_at_ms);
CREATE INDEX idx_spans_run     ON trace_spans(run_id, parent_span_id);
CREATE INDEX idx_spans_trace   ON trace_spans(trace_id);

-- 分析任务：状态机 + 幂等 + 租约
CREATE TABLE training_jobs (
  id              TEXT PRIMARY KEY,
  session_id      TEXT NOT NULL,
  stage           TEXT NOT NULL,        -- collecting|sealing|analyzing|...
  status          TEXT NOT NULL,
  attempt         INTEGER NOT NULL DEFAULT 0,
  idempotency_key TEXT NOT NULL UNIQUE,
  checkpoint_json TEXT,
  lease_expires_at_ms INTEGER,
  error_code      TEXT,
  error_detail    TEXT,
  started_at_ms   INTEGER,
  finished_at_ms  INTEGER
);

-- 发现：分析的产出，可复核、可转提案
CREATE TABLE findings (
  id           TEXT PRIMARY KEY,
  session_id   TEXT NOT NULL,
  category     TEXT NOT NULL,
  severity     TEXT NOT NULL,     -- high|medium|low
  title        TEXT NOT NULL,
  explanation  TEXT NOT NULL,
  suggestion   TEXT,
  evidence_json TEXT,             -- span_id 列表 + 指标对比
  status       TEXT NOT NULL DEFAULT 'open', -- open|confirmed|dismissed|resolved
  first_seen_ms INTEGER, last_seen_ms INTEGER,
  occurrences  INTEGER DEFAULT 1
);

-- 指标快照：离线计算，避免每次查询扫 span
CREATE TABLE metric_snapshots (
  id           TEXT PRIMARY KEY,
  scope        TEXT NOT NULL,     -- session|goal|global
  scope_id     TEXT NOT NULL,
  window_start_ms INTEGER NOT NULL,
  window_end_ms   INTEGER NOT NULL,
  success_rate REAL, cost_usd REAL, cost_per_success REAL,
  p50_ms INTEGER, p95_ms INTEGER,
  steps_avg REAL, tool_calls_avg REAL, retry_rate REAL,
  human_wait_ms INTEGER, tokens_p50 INTEGER, tokens_p95 INTEGER
);
CREATE UNIQUE INDEX idx_snap ON metric_snapshots(scope, scope_id, window_start_ms);

-- 标注：人工反馈与验证结果统一入口
CREATE TABLE labels (
  id          TEXT PRIMARY KEY,
  target_type TEXT NOT NULL,     -- span|run|session
  target_id   TEXT NOT NULL,
  source      TEXT NOT NULL,     -- verifier|human|implicit
  label       TEXT NOT NULL,     -- pass|fail|unknown
  score       REAL,
  rationale   TEXT,
  annotator   TEXT,
  created_at_ms INTEGER NOT NULL
);

-- 提案与变更：可回滚
CREATE TABLE proposals (
  id           TEXT PRIMARY KEY,
  session_id   TEXT NOT NULL,
  kind         TEXT NOT NULL,    -- prompt|policy|threshold
  diff_json    TEXT NOT NULL,
  expected_effect TEXT,
  eval_result_json TEXT,
  status       TEXT NOT NULL DEFAULT 'draft', -- draft|approved|applied|rolled_back
  applied_at_ms INTEGER, rolled_back_at_ms INTEGER
);
```

补充：

- 大文本（prompt、工具输出、文件 diff）不进 SQLite，写 `content_ref` 指向 daemon 管理的内容寻址目录，表里只留 hash 与长度；这直接对应 OpenAI 的 `trace_include_sensitive_data` 与 Claude Code 的「默认不采内容」。
- 保留策略：`trace_spans` 按 session 删除；`findings` / `metric_snapshots` / `proposals` 长期保留。这就是 LangSmith「trace 180 天、dataset 永久」的单机版。
- 写入：批量事务（每 200 行或每 500ms），`PRAGMA journal_mode=WAL`、`busy_timeout`；daemon 单写者，分析任务只读。

### 7.4 与 goal 创建表单的集成

在 goal 创建表单加一个「运行模式」区块，默认「推理」：

```
运行模式
( ) 推理：只记录基础指标（成功/耗时/成本），不采集内容
(•) 训练：完整记录 trace，完成后自动分析     [推荐]
      采样率   [100% ▾]   内容采集 [脱敏 ▾]
      分析预算 $[2.00]     保留 [30 天 ▾]
      完成后 [x] 生成改进提案  [ ] 自动应用（默认关）
```

交互要点：

- 模式切换时实时显示预估成本（参考 Insights 的 $1–4 / 1000 条）与存储量，避免用户无意识开全量。
- 「训练」不是布尔值，而是 `mode + sample_rate + content_capture + budget` 四元组，落到 `training_sessions`。
- 对高敏感 goal 提供「仅结构」档：记 span 树与指标，不记输入输出。
- 创建后在 goal 详情显示一个轻量徽章「训练中 · 3/12 runs」，完成后变「分析完成 · 4 条发现」。

### 7.5 分析管线与成本控制

1. **采集**：run 执行时写 span（异步 buffer，避免阻塞主流程）。
2. **封存**：goal 终态后冻结样本，生成 `metric_snapshots`。
3. **筛选**：失败/人工干预/高成本 trace 100% 保留，成功 trace 按 `sample_rate` 抽。
4. **归因**：先用确定性规则（错误分类、步骤异常、重试密集、上下文接近上限、人工等待占比），再对 Top-N 用 LLM 做聚类与摘要（双模型：便宜模型摘要、强模型聚类）。
5. **复核**：findings 进 `needs_review`，人工确认后才允许生成 proposal。
6. **复测**：proposal 应用到后续 run，比较 `metric_snapshots` 的 delta；regressed 自动回滚。

成本闸门三层：session 预算 → 单次分析样本上限（建议 200–1000 条）→ 全局每日预算。任一层触顶则降级为「只跑规则分析」。

### 7.6 分期建议

- **P0（1–2 周）**：训练开关 + span 采集 + 单会话指标卡 + 瀑布视图。不做 LLM 分析。
- **P1**：异步 job（状态机 + 租约）+ 规则型 findings + 对比视图。
- **P2**：LLM 归因与聚类 + 提案生成 + 复测闭环。
- **P3**：模式库（跨 session 聚类）+ 定时巡检 + 自动应用（需灰度）。

---

## 8. 未核实项与风险

以下内容本次未能取得正文，**不作事实引用**，仅保留入口 URL 供后续核实：

1. **OpenTelemetry GenAI semconv 的具体属性名**（`gen_ai.operation.name` 取值、`gen_ai.usage.*` 等）：官网 gen-ai 页已迁移到 `semantic-conventions-genai` 仓库，raw 抓取超时，页面为 JS 渲染。
   - https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-spans/ （未核实）
2. **Google ADK / Vertex AI Evaluation**：ADK observability 页只取到导航；Vertex 评测文档抓取失败。
   - https://google.github.io/adk-docs/observability/ （未核实）
3. **Chrome perf bot**：官方 `perf_bots.md` 返回 404。
   - https://chromium.googlesource.com/chromium/src/+/main/docs/speed/perf_bots.md （未核实）
4. **Uber M3**：博客返回 "Not Acceptable"（区域/反爬）。
   - https://www.uber.com/blog/m3/ （未核实）
5. **GitHub Copilot 的遥测驱动改进机制**：只取到 metrics API 端点标题，未取得指标定义正文。
   - https://docs.github.com/en/rest/copilot/copilot-metrics （未核实）
6. **OpenAI Evals 平台细节**：platform 文档被 Cloudflare 拦截；仅确认 GitHub 仓库存在。
   - https://github.com/openai/evals

风险提示：

- **观测者效应**：训练模式本身会改变延迟与成本（序列化、落盘、分析占用 CPU），指标必须区分「训练 run」与「推理 run」，否则横向对比失真。
- **标签偏差**：验证器通过 ≠ 业务成功；若只用验证结果做 label，优化目标会退化成「讨好验证器」。建议保留人工标注队列作为校准集。
- **小样本**：单机场景一周可能只有几十个 run，P95 与聚类都不稳定。第一版应以规则归因与对比视图为主，LLM 聚类只在样本 ≥50 时启用。
- **隐私**：内容采集默认关闭或脱敏；「训练模式」必须在 UI 上明确告知数据用途与保留期，参照 Claude Code 的默认最小采集原则。

---

## 附录：来源清单

| 主题 | URL |
|---|---|
| LangSmith 数据模型 | https://docs.langchain.com/langsmith/observability-concepts |
| LangSmith 采样 | https://docs.langchain.com/langsmith/sample-traces |
| LangSmith Insights | https://docs.langchain.com/langsmith/insights |
| LangSmith few-shot evaluator | https://docs.langchain.com/langsmith/create-few-shot-evaluators |
| LangSmith online evaluator | https://docs.langchain.com/langsmith/online-evaluations-llm-as-judge |
| Langfuse 数据模型 | https://langfuse.com/docs/observability/data-model |
| Langfuse 采样 | https://langfuse.com/docs/observability/features/sampling |
| Braintrust Loop | https://docs.braintrust.dev/loop/index.md |
| Braintrust Patterns | https://docs.braintrust.dev/observe/patterns/index.md |
| Braintrust Patterns 复核 | https://docs.braintrust.dev/observe/patterns/review.md |
| W&B Weave Trace view | https://weave-docs.wandb.ai/guides/tracking/trace-tree |
| Arize Phoenix trace 概念 | https://arize.com/docs/phoenix/tracing/concepts-tracing |
| Arize Phoenix 评测 | https://arize.com/docs/phoenix/evaluation/llm-evals |
| AgentOps 核心概念 | https://docs.agentops.ai/v2/concepts/core-concepts |
| Helicone 成本追踪 | https://docs.helicone.ai/guides/cookbooks/cost-tracking |
| Helicone 成本计算口径 | https://docs.helicone.ai/references/how-we-calculate-cost |
| OpenAI Agents SDK tracing | https://openai.github.io/openai-agents-python/tracing/ |
| OTel GenAI semconv | https://opentelemetry.io/docs/specs/semconv/gen-ai/gen-ai-spans/ |
| Claude Code 遥测 | https://code.claude.com/docs/en/monitoring-usage |
| Cursor Teams Analytics | https://cursor.com/docs/account/teams/analytics |
| Devin Session Insights | https://docs.devin.ai/product-guides/session-insights |
| DSPy optimizers | https://dspy.ai/learn/optimization/optimizers/ |
| OpenAI Evals 仓库 | https://github.com/openai/evals |
| 分位数口径 | https://prometheus.io/docs/practices/histograms/ |
| Google ADK observability | https://google.github.io/adk-docs/observability/ |