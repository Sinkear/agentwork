# RSI 能力自迭代：学术调研

> 目标读者：agentwork 的设计者。agentwork 的闭环是 `goal → run → 机器验证 → 人闸门 → 交付`，本调研回答一个问题：**把「让平台自己变好」做成一个可验证、可回滚、不自我欺骗的闭环，学术界已经提供了哪些零件，哪些坑已经踩过。**
>
> 调研方法：`web_search` 在本环境因上游余额不足不可用（HTTP 402），全部结论改为直接抓取 arXiv 摘要页 / arXiv API / 官方文档 / 本仓库代码得到。凡未能取到原文正文的，只引用摘要明确陈述的内容，并标注【未核实】。
>
> 标注约定：
> - **【论文】**：论文摘要或标准文本中明确陈述的事实与数字。
> - **【推论】**：本文基于论文的推理、类比或设计判断，论文未如此说。
> - **【代码事实】**：agentwork 本仓库代码/文档中实际存在（附路径）。
> - **【建议】**：面向 agentwork 的设计提议，尚无实现。

---

## 0. 一页结论

1. **自我改进 = 带验证器的搜索循环**，不是「agent 反思一下」。它的五元组是 `(A, P, E, S, Π)`：工件空间 A、提议者 P、评估器 E、选择/存档 S、预算策略 Π。所有工作的差异都落在这五项上，尤其落在 E。【推论】
2. **E 的保真度是天花板，不是 P 的聪明程度。** Gödel Machine 要求「改写前先证明净收益」，实践中不可行，DGM 明确以经验验证替代证明【论文：arXiv 2505.22954】。所有能跑的工作都退回到「在基准上实测」。
3. **改进对象存在风险阶梯**：prompt/context < 编排参数/workflow graph < 工具与技能库 < 代码 < 权重 < 模型路由。证据强度与「改坏了难发现」的程度同步上升。
4. **防退化的主流答案不是「把 reward 定义得更窄」**。Skalse 等证明：奖励对访问计数线性时，只有当两个 reward 之一是常数，才可能对所有随机策略都不可 hack——即「窄 proxy」不构成安全保证【论文：arXiv 2209.13085】。真正有效的是外部可执行真值 + 祖先存档/回滚 + 留出集 + 统计显著性 + 人闸门。
5. **单条 trace 只能给出相关性**。定位「可优化点」需要 span 树 → 关键路径 → 反事实重放三步；只有存在合法干预（patch）的瓶颈才值得优化。【推论】
6. 对 agentwork 最现实的第一步**不是让 agent 改代码**，而是让它改域内的 prompt 片段 / 技能 / 编排参数，用已有的机器验证 + 人闸门做守卫。**前置工程缺口只有一个但很致命：ACP 的 `usage_update` 在 run 路径上是空实现，token 与成本被丢弃**（`internal/proto/acpbackend/session.go:230`、`backend.go:304`）【代码事实】。

---

## 1. 自我改进的闭环：改进对象谱系

### 1.1 闭环的一般形式

把任意自改进系统写成一次搜索：在工件空间 `A` 上，由 `P(A, 历史) → A'` 提出候选，由 `E(A') → ℝ^d` 打分，由 `S` 决定保留/丢弃/存档，由 `Π` 约束调用次数、token、wall-clock。**「自我」的强弱取决于 P 和 A 是否包含系统自身**：

- P 只改产物（prompt 优化器）→ 不是 RSI，是超参数搜索。
- P 改 P 自己（STOP、DGM）→ 强自指，也是退化风险最高的地方。
- A 包含 E（改验证器）→ 直接崩溃，因为梯度指向「让 E 说好话」。

这条分类轴比「是否用 LLM」更能解释文献差异。【推论】

### 1.2 代表工作对比

| 工作 | 年 | 改进对象 A | 提议者 P | 评价信号 E | 防退化/防 hack 机制 | 关键数字【论文】 |
|---|---|---|---|---|---|---|
| Gödel Machine (cs/0309048) | 2003 | 自身任意代码（含证明器） | 证明搜索 | **形式证明**净收益 | 改写前必须证明；理论上无局部极大 | 无实证 |
| STOP (2310.02304) | 2024 | scaffolding 程序 + 改进器自身 | LM 生成代码变异 | 验证器分数 | 沙箱；把「绕过沙箱的频率」当安全指标 | LM 自发提出 beam/GA/模拟退火 |
| ADAS (2408.08435) | 2024 | 新 agent 的代码 | meta agent 编程 | benchmark 分数 | 递增 archive；跨域留出 | 超手工 SOTA，可跨域/跨模型迁移 |
| DGM (2505.22954) | 2025 | agent 代码（含「改代码的能力」） | 基础模型 + archive 采样 | SWE-bench / Polyglot 实测 | archive + 树状开放探索；沙箱；人监督 | SWE-bench 20.0→50.0%；Polyglot 14.2→30.7% |
| SICA (2504.15228) | 2025 | 自身代码与工具使用 | LLM reflection 产生 edit | SWE-bench Verified 子集 | 无梯度、data-efficient；保留可执行基线 | 17%→53% |
| Gödel Agent (2410.04444) | 2024 | 运行时 logic/behavior | 高层目标 prompt 引导 | 任务分数 | 不依赖预定义 routine | 数学推理与 agent 任务持续提升 |
| SEAL (2506.10943) | 2025 | 自身权重（self-edit→SFT） | 模型生成 self-edit | **更新后**下游表现（RL reward） | reward 用更新后模型测，避免自评 | 持久权重更新 |
| AFlow (2410.10762) | 2024 | workflow 图（代码表示） | MCTS + 代码修改 | 执行反馈 + 验证分数 | 树状经验、预算约束 | 6 数据集均 +5.7%；4.55% 成本胜 GPT-4o |
| MaAS (2502.04180) | 2025 | 架构**分布**（agentic supernet） | 按 query 采样 | 任务分数 − 成本 | 成本硬约束；query-dependent | 6–45% 成本，+0.54~+11.82% |
| ACE (2510.04618) | 2025 | context playbook | generator/reflector/curator | 自然执行反馈（可无标签） | delta 增量更新，防 collapse/brevity | agents +10.6%，finance +8.6% |
| Reflexion (2303.11366) | 2023 | 情节记忆（prompt 内） | verbal reflection | 任务成功/失败 | 只写记忆不改权重；buffer 有限 | HumanEval pass@1 91% |
| Voyager (2305.16291) | 2023 | 技能库（可执行代码） | 迭代 prompting | 环境反馈 + 自验证 | 技能**通过验证才入库** | 3.3× 物品 / 2.3× 距离 / 15.3× 里程碑速度 |
| DSPy (2310.03714) | 2023 | 文本变换图 + demonstrations | compiler（按 metric） | 给定 metric | 编译集/验证集分离 | 超 few-shot >25%/>65% |
| TextGrad (2406.07496) | 2024 | 任意文本变量 | 文本「梯度」反传 | 自然语言 loss | 类 PyTorch，可组合 | GPQA 51→55% |
| OPRO (2309.03409) | 2023 | prompt | LLM 当优化器 | 分数回填 | 只从候选池选最优 | GSM8K +8%，BBH +50% |
| PromptWizard (2405.18369) | 2024 | instructions + examples | critique + synthesis | 任务分数 | 迭代至收敛；成本约束 | 45 任务，显著降 API/token 成本 |

### 1.3 三个非显然的结论

**(a) 改「改进器」比改产物更值钱。** STOP 让 seed improver 改进自己，LM 自行提出 beam search、遗传算法、模拟退火等搜索策略【论文：2310.02304】；DGM 允许改动「改代码的能力」本身【论文：2505.22954】；AFlow 把「workflow 怎么连」变成可搜索对象【论文：2410.10762】。这三者提升的都是 P，而不是某次输出。

**(b) 存档（archive）是抗退化的结构，不是可选优化。** DGM 维护已发现 agent 的 archive，采样后生成新版本，形成开放式的树；ADAS 的 meta agent 依赖一个不断增长的 archive 才不至于原地打转【论文：2408.08435、2505.22954】。单点爬山（只保留当前最优）一旦遇到「当前最优在局部平台」，就再也回不去了。【推论】

**(c) 证明换搜索是优雅但不可行。** Gödel Machine 的核心是把「要不要改写」变成可证明命题，从而保证全局最优、无局部极大；代价是证明搜索本身不可判定/不可行【论文：cs/0309048】。DGM 直接承认「在实践中证明大多数改动净收益为正是不可能的」，改为在编码基准上实测【论文：2505.22954】。**工程上这意味着：所有可运行的 RSI 系统都是「经验验证 + 统计」，所以统计功效就是系统的智商上限。**【推论】

### 1.4 「变好了」的信号谱（按保真度降序）

| 信号 | 代表工作 | 保真度 | 可被 gaming 的方式 | agentwork 是否已有 |
|---|---|---|---|---|
| 形式证明 | Gödel Machine | 最高（但不可行） | 公理本身可错 | 否 |
| 隐藏测试 / 可执行检查 | SWE-bench、Voyager、ADAS | 高 | 过拟合测试、硬编码 | **是**（域 verify checks） |
| 留出基准分数 | DGM、SICA、AFlow | 中高 | 污染、过拟合 benchmark | 部分（无留出集） |
| 成本-精度联合分 | MaAS、AI Agents That Matter | 中高 | 用「不做」换成本 | 否（成本未采集） |
| 跨模型 judge | Agent-as-a-Judge | 中 | judge 偏置、风格偏好 | 否 |
| 同模型自评 | Self-Rewarding LM | 低（有 self-preference） | 自偏好，实证线性相关 | 否（且不应引入） |
| 人类评分/闸门 | 全行业 | 中（贵、慢、噪声大） | 评审疲劳 | **是**（gate_decision） |

【论文】自评偏置不是猜测：Panickssery 等发现开箱即用的 GPT-4/Llama 2 已能以非平凡准确率区分自己的输出，微调后「自我识别能力」与 self-preference 强度线性相关，受控实验支持因果解释（arXiv 2404.13076）。Self-Rewarding LM 则证明自评可以带来真实提升（Llama 2 70B 三轮迭代 DPO 后在 AlpacaEval 2.0 上超过 Claude 2 / Gemini Pro / GPT-4 0613），但它是「指令跟随」这一宽任务，且 judge 与 policy 同源的风险由后续工作反复指出【论文：2401.10020】。**结论：自评可以作为「提议」的信号，不能作为「验收」的唯一信号。**【推论】

---

## 2. 退化：机制与防护

### 2.1 reward hacking 的形式化

Skalse 等给出定义：若优化 proxy reward 的期望回报上升会导致 true reward 的期望回报下降，则 proxy 被 hack；「unhackable」= 提升 proxy 期望永不降低 true 期望。他们进一步证明：当 reward 对 state-action 访问计数线性时，在**所有随机策略**集合上，两个 reward 只有其中一个是常数时才可能 unhackable——因此把 proxy 设计得「更窄/更具体」并不能保证安全【论文：2209.13085】。

**对 agentwork 的直接含义**：只要「变好」由某个代理指标（更少的 run 数、更短的 review_duration、更高的 verify 通过率）定义，优化压力就会去找该指标的漏洞。例如缩短 review_duration 的最省力方式是「不认真审」；提高 verify 通过率的最省力方式是「少写测试」。**唯一稳健的解法是让 true reward 尽量接近可执行事实，并让代理指标始终成组出现（成组指标比单一指标难 hack 得多）。**【推论】

### 2.2 自我指涉的四种崩溃模式

| 崩溃模式 | 机制 | 证据 | 在 agentwork 的可能表现 |
|---|---|---|---|
| 验证器被改 | A 包含 E，搜索直接优化 E 本身 | STOP 专门统计「生成代码绕过沙箱的频率」【论文：2310.02304】 | agent 修改 `checks.json` 或 gate_rule，让验证恒过 |
| 自评偏置 | 同一模型既提议又打分 | self-recognition↔self-preference 线性相关【论文：2404.13076】 | reviewer 与 worker 同模型同 prompt 家族，评审趋同 |
| context collapse | 反复整体重写摘要，细节被侵蚀；为简洁丢洞见（brevity bias） | ACE 以此命名两大病症并用 delta 更新对抗【论文：2510.04618】 | 域 prompt/skill 被反复「精简」，边界条件丢失 |
| model collapse | 用自身生成数据递归训练，分布尾部消失 | Shumailov et al., Nature 2024【论文】 | 用平台自己的 run 输出当训练语料做微调 |

### 2.3 防护矩阵（可直接映射到 agentwork）

| 威胁 | 学术机制 | agentwork 落点【建议】 |
|---|---|---|
| 改验证器 | 评价器与搜索空间隔离（STOP 的沙箱监控） | 宪法层：`checks.json`/gate_rule/scoring/评测集**只允许人改**，patch 校验器直接拒绝 |
| 自评偏置 | 交叉模型 judge、过程级证据 | 机器 verify 为唯一验收；LLM judge 只能用于「提议」或过程诊断 |
| 过拟合评测 | 留出集、任务分层、holdout | 域内 replay 集分 train/holdout，holdout 永不出现在提议上下文 |
| 单点锁死 | archive + 树状探索 | 保留每个 patch 的 `before_hash` 与父 patch，可回滚；不入 archive 的候选自动丢弃 |
| 指标 gaming | 成组指标 + 成本受控评测 | 同时上报 success/latency/cost/human_interventions，任一护栏破线即否决 |
| 噪声当成提升 | 配对检验 + 序贯监控 | 见 §3.5；n<20 对时不做「已改进」结论 |
| 爆炸半径 | 沙箱 + 人监督（DGM 明确采用） | 单 patch 单域生效；全局参数变更强制人闸门 |

---

## 3. Trace 的结构化与指标

### 3.1 三层 span 模型与 OTel GenAI 映射

OpenTelemetry 的 GenAI 语义约定把 span 定义为「一次逻辑操作」，且**自动重试的 span 应覆盖全部重试时长**【论文/标准：semantic-conventions-genai, `docs/gen-ai/gen-ai-spans.md`】。关键属性包括 `gen_ai.operation.name`（chat / execute_tool / fetch_response / search_memory…）、`gen_ai.provider.name`、`gen_ai.request.model`、`gen_ai.response.model`、`gen_ai.response.finish_reasons`、`gen_ai.conversation.id`、`gen_ai.agent.name`、`gen_ai.tool.name`、`gen_ai.tool.call.id`、`gen_ai.tool.type`、`gen_ai.tool.call.arguments`/`result`（Opt-In，敏感）、`gen_ai.input.messages`/`output.messages`（Opt-In，可用 `*_ref` 外置）、`error.type`。工具 span 命名 SHOULD 为 `execute_tool {gen_ai.tool.name}`，kind 为 INTERNAL；内容捕获默认 `NO_CONTENT`。

**关键缺口【推论】**：GenAI semconv 只覆盖到「一次 LLM 调用 / 一次工具执行 / 一次 retrieval」这一层，**没有「一次 goal 的完整工作流」概念**。agentwork 的 `goal → run → verify → gate → deliver` 必须自建上层 span 语义，再向下挂载标准 GenAI span。

建议的映射：

| agentwork 概念 | span 名 | 关键属性 |
|---|---|---|
| 一次 goal 生命周期 | `agentwork.goal` | `goal.id`、`goal.status`、`human_iterations`、`execution_attempt` |
| 一次 run（turn） | `agentwork.run` | `run.id`、`run.role`、`run.attempt`、`run.cancel_reason`、`gen_ai.conversation.id` |
| 一次 LLM 调用 | 标准 `chat` | `gen_ai.request.model`、token usage、`finish_reasons` |
| 一次工具执行 | `execute_tool {name}` | `gen_ai.tool.call.id`、`args_hash`、`duration` |
| 一次验证 | `agentwork.verify` | `verify.run_id`、`passed/rejected`、`checks_failed[]` |
| 一次人闸门 | `agentwork.gate` | `gate.rule`、`decision`、`review_duration` |

### 3.2 指标清单

| 层 | 指标 | 定义 | 数据来源 | 用途 |
|---|---|---|---|---|
| goal | 交付周期 | deliver − goal.created | goal/run 时间戳 | 端到端体验 |
| goal | 人迭代数 | `goal.human_iterations` | goal 表【代码事实】 | 返工成本 |
| goal | 机器重试数 | `goal.execution_attempt` | goal 表【代码事实】 | 稳定性 |
| goal | 验证通过率 | passed / (passed+rejected) | verification_result【代码事实】 | 质量信号 |
| run | 墙钟时长 | started_at→finished_at | run 表【代码事实】 | 性能 |
| run | 取消原因分布 | `cancel_reason` 枚举 | run 表【代码事实】 | 失败模式（idle_watchdog/timeout/runaway 各占多少） |
| run | token / 美元成本 | usage_update.used/size/cost | **当前被丢弃** | 成本归因 |
| run | 上下文峰值占比 | used / size | usage_update | 上下文膨胀 |
| step | LLM 调用次数与占比 | n_calls、Σllm_dur / run_dur | 需新采 | 成本主因 |
| step | 工具调用次数、重复率 | 相同 (tool,args_hash) 计数 | chat_message + 需 args_hash | 重复工具调用 |
| step | 关键路径占比 | CP / run_dur | 需 span 父子 + 时序 | 瓶颈定位 |
| 人类 | review_duration | gate_decision 内已有 | 【代码事实】 | 人闸门负荷 |

### 3.3 六类瓶颈的可计算定义

| 瓶颈 | 判据 | 需要的字段 | 可行动作 |
|---|---|---|---|
| 关键路径过长 | 最长路径 / 总时长 ≥ 0.5，且该路径上无并行可能 | span 父子、start/end | 并行化独立子任务、拆分 run |
| 重试放大 | 有重试的 span 总时长 / 有效执行时长 ≥ 0.2 | `attempt`、retry span | 修根因、退避、失败快速短路 |
| 等待/空转 | 工具间隔 > p95 且无 token 产出；或 gate 等待占 goal 周期 > 30% | 相邻事件时间差 | 异步化、预取、减少同步点 |
| 串行依赖 | 存在无数据依赖的工具对却严格串行 | 工具 read/write 集合 | 合并 read、批量工具、并行调用 |
| 上下文膨胀 | 上下文 used 随 step 单调上升且重复读同一资源；`used/size > 0.7` | usage_update、资源路径 | 摘要/引用化、delta 记忆、分段 |
| 重复工具调用 | 同一 (tool, args_hash) 出现 ≥2 次且结果未变 | args_hash、结果 hash | 缓存、去重、把结论写入 playbook |

这些定义都属于【推论】：它们是工程可计算量，不是论文里的标准术语。但「过程挖掘」提供了成熟的语言：把每次 run 的事件序列当 trace，用 PM4Py 的 conformance checking（fitness / precision）对照声明的工作流模型，可以直接量化「实际执行偏离设计流程多少」【论文：van der Aalst, *Process Mining*；工具 https://pm4py.fit.fraunhofer.de/ 】。**这条对 agentwork 特别有用：自迭代改了编排之后，fitness 下降就是「流程漂移」的早期警报。**【推论】

### 3.4 从 trace 到「可优化点」：三段式

1. **现象**：某个指标超出基线（如 `dup_tool_ratio` 从 0.05 → 0.31）。
2. **机制**：在 span 级找到一致的模式（例：每次 verify 前都重复 read 同一文件 3 次，且三次 hash 相同）。
3. **干预**：一个最小 patch（例：在 verify prompt 中加入「已读文件清单」，或让工具层对同 run 内同路径 read 做 60s 缓存）。

只有 2 成立时，3 才是「可优化的」；否则只是把噪声当信号。**这一步是绝大多数「自动优化」系统翻车的地方：它们跳过 2，直接从 1 生成 3。**【推论】

### 3.5 统计：怎么知道不是噪声

- **实验应被视为抽样**：Miller 主张把评测问题看作从超总体中抽样，给出差异检验与实验规划的公式，以最小化统计噪声【论文：2411.00640】。
- **连续监控必须用 always-valid 方法**：反复 peeking 会让 p 值失效；Johari 等给出 always-valid p 值与置信区间，等价于序贯检验接口，已在工业 A/B 平台分析数十万实验【论文：1512.04922】。agentwork 的「边跑边看指标」天然是连续监控，**不能直接用固定样本的 t 检验**。
- **配对设计 + bootstrap**：同一批 replay 任务上做 A/B 是配对的；二值结果用 McNemar，连续结果（时长、成本）用配对 bootstrap 置信区间（`scipy.stats.bootstrap`）。比值型指标（cost-per-success）用 bootstrap 比 delta method 稳。
- **功效**：RSI 场景里真实效应通常 5%–20%，任务级方差极大，n<20 对的比较基本不可结论。【建议】起步 n≥20 对，宣称「已改进」需 95% CI 不含 0 且效应 ≥5%。
- **成本口径**：AI Agents That Matter 指出当前评测只看 accuracy、忽视成本，主张成本受控评测与 accuracy-cost 联合的 Pareto 分析，并批评 holdout 不足与复现性差【论文：2407.01502】。

---

## 4. 评测与可比性

| 基准 | 规模/形态 | 评判方式 | 关键数字【论文】 | 对 agentwork 的可借用点 |
|---|---|---|---|---|
| SWE-bench (2310.06770) | 2,294 issue × 12 Python 仓 | 隐藏测试 | 当时最佳 1.96% | 「改代码 → 跑测试」即 gold standard；agentwork 的域 verify 同构 |
| GAIA (2311.12983) | 466 真实问题，多步 + web + 工具 | 精确答案 | 人 92% vs GPT-4+插件 15% | 难度分层（L1/L2/L3） |
| WebArena (2307.13854) | 4 个自托管站点 | 程序化功能测试 | 最佳 14.41% vs 人 78.24% | 用「环境内可执行断言」而非字符串匹配 |
| τ-bench (2406.12045) | retail/airline 动态对话 | 域 policy 合规 + 目标达成 | gpt-4o <50%，pass^8 retail <25% | **pass^k**：衡量一致性而非单次运气 |
| AgentBench (2308.03688) | 8 个交互环境 | 任务成功率 | 商用 ≫ 开源 | 多环境交叉验证泛化 |
| Agent-as-a-Judge (2410.10934) | DevAI：55 任务 / 365 需求 | agent 评 agent（过程级） | 接近人工互评且更省 | 过程级反馈可作为「提议信号」 |
| PRM / process supervision (2305.20050) | MATH + PRM800K | 步级人类标签 | 78%（代表子集） | 步级信用分配优于结果级 |

### 4.1 构造「同一任务的可比性」四要素

1. **任务集固定且留出**：replay 集必须冻结；用于提议的任务与用于验收的任务分离。否则 RSI 会变成「对 20 条已知任务过拟合」。
2. **难度分层**：按历史 `execution_attempt`、`human_iterations`、diff 规模把任务分层（易/中/难），分层配对后再汇总，避免「专挑简单任务」拉高均值。
3. **pass@k vs pass^k**：pass@k 衡量「k 次里有一次成功」，适合能力上限；pass^k 衡量「k 次全成功」，适合可靠性。agentwork 是交付型系统，**应看 pass^k**（τ-bench 的核心贡献）【论文：2406.12045】。
4. **cost-per-success**：`Σcost / n_success`。它对「失败也烧钱」敏感，是比平均成本更好的分母；与成功率一起构成二维目标。

### 4.2 过程评估（trajectory evaluation）

结果对了不代表过程对（可能是碰巧、可能是 hack）。过程级信号有三个可用档位：步级人工/模型标注（PRM，贵）、agent 评审（DevAI，中）、可执行的过程断言（例：必须调用 verify、必须读过某文件）。agentwork 已有 verification_result 与 gate_decision，**最便宜的过程断言就是「验证是否真的执行了、检查项是否真的失败了」**【建议】。

---

## 5. 形式化：什么叫「可优化」

### 5.1 目标向量与 Pareto

令一次交付的目标向量
`J = ( success_rate, −p95_latency, −cost_per_success, −human_interventions, −regression_rate )`
（负号表示越小越好）。patch `p` 相对基线 `b` 是 **Pareto 改进** 当且仅当 `∀i: J_i(p) ≥ J_i(b)` 且 `∃i: J_i(p) > J_i(b)`。

实用上需要 **ε-支配**：允许 ±ε 噪声（ε 由 bootstrap CI 给出），否则任何抖动都会被判为「改进/退化」【推论】。这与 Kapoor 等主张的 accuracy-cost 联合 Pareto 分析一致【论文：2407.01502】。

### 5.2 反事实归因

要回答「如果去掉这个重试，时长会少多少」，需要 `do(干预)` 的分布。观测数据只能给相关性；agentwork 有一个罕见优势：**run 表记录了 `base_ref`、`head_ref`、`prompt`、`workdir`，使得「同一任务重放」在工程上可行**【代码事实】。这等价于允许做小规模随机实验（A/B 重放），而不是纯观测推断。【推论】

可计算的分摊：对关键路径上的每个 span 做「移除后重放的边际时长」估计（Shapley 近似，按 span 顺序抽样），得到 `Δ_duration_i`；成本同理按 token 归因。**注意只有「可移除/可并行/可换模型」这三类干预是可估的，其余归因是虚构。**【推论】

### 5.3 瓶颈的可操作定义

`bottleneck := argmax_s ( share(s) × reducible(s) )`，其中 `share(s)` 是该 span 占关键路径的时长比例，`reducible(s) ∈ {0,1}` 表示是否存在合法 patch 作用于它（并行化、缓存、换模型、裁剪上下文、合并调用）。

**只有 `reducible(s)=1` 的瓶颈才进入优化队列**；`reducible=0` 的「瓶颈」只是系统的固有成本，优化它只会产生噪声 patch。【推论】这一定义把「可优化」从形容词变成了可判定谓词。

### 5.4 「可优化」判定的四条件

一个点要被称为「可优化」，需同时满足：
1. **显著**：指标偏差的 95% CI 不覆盖基线（序贯监控用 always-valid）；
2. **可归因**：span 级机制一致，且反事实重放显示移除该机制后指标改善；
3. **可干预**：存在落在允许改动范围内的最小 patch，且预期效应量 ≥ 预设 MDE；
4. **可回滚**：patch 有 `before_hash`，回滚不需人工重建状态。

四条缺一，就只是「有个奇怪的现象」。【推论】

---

## 6. 给 agentwork 的落地建议

### 6.1 现状盘点

| 能力 | 现状 | 证据 |
|---|---|---|
| run 结果 / 重试 / 取消原因 | ✅ | run 表 `status`/`attempt`/`cancel_reason`（idle_watchdog/timeout/runaway…）【代码事实】 |
| 机器验证（每轮一条） | ✅ | verification_result：verifier_run_id、passed/rejected、summary+evidence【代码事实】 |
| 人闸门与评审耗时 | ✅ | gate_decision：gate_rule、decision、decided_by、**review_duration**【代码事实】 |
| 域基线 | ✅ | `domain.metrics_baseline`（测试数/覆盖率）【代码事实】 |
| step 级 trace | ⚠️ 部分 | chat_message（role/tool_calls/created_at），一个 tool call 一行、按 CallID 配对【代码事实】 |
| token / 成本 | ❌ | `internal/acp/types.go` 有 usage_update(used/size/cost)，但 `session.go:230`、`backend.go:304` 的 `OnUsageUpdate` 是空实现【代码事实】 |
| span 父子 / 时长 | ❌ | chat_message 只有 created_at，无 end/无 parent |
| prompt 版本标识 | ❌ | run 表存了 `prompt` 原文，无 hash/版本号 ⇒ patch 与效果无法精确关联 |
| 关键路径 | ❌ | 无时序 DAG |
| 留出集 | ❌ | 无 train/holdout 划分 |

**结论：agentwork 已经具备「验证 + 闸门」这两个最贵的部件，缺的是「观测」与「归因」两个便宜部件。顺序应该是先补观测，再做 RSI。**【推论】

### 6.2 指标采集三层

- **L1（run 级，1 天工作量）**：把 `usage_update` 的 used/size/cost 落库（新增 `run_usage` 表或 run 表加列）；`started_at/finished_at` 已可算时长。这一项单独就能支撑「成本归因」与「上下文膨胀」两类分析。
- **L2（step 级，3–5 天）**：chat_message 加 `span_id`/`parent_id`/`duration_ms`；tool_use 存 `args_hash`、`result_hash`；LLM 调用存 token delta 与 model。有了这三样，§3.3 的六类瓶颈全部可算。
- **L3（goal 级，已有）**：`human_iterations`、`execution_attempt`、验证通过率、review_duration、交付周期——直接查表聚合即可。

### 6.3 「可优化」判定流水线（可直接实现）

1. 取同域最近 30 个 run（或一个 goal 的全部 run）。
2. 算 §3.2 指标，用 bootstrap 得 95% CI；与 `metrics_baseline` 比。
3. 若某瓶颈 `share ≥ 0.25` 且 `reducible=1`，进入候选。
4. 生成 patch，在冻结 replay 集上做配对 A/B（n≥20 对，同一 base_ref）。
5. 判定：主指标改善 ≥5%、95% CI 不含 0、所有护栏指标下降 ≤1pp、成本上升 ≤3% ⇒ 通过；否则丢弃并记录「负结果」（负结果本身是训练数据）。

【建议】5% / 1pp / 3% 是起始值，应按域校准；域越稳定，MDE 可以越小。

### 6.4 训练任务应产出什么：`rsi.patch/v1`

```json
{
  "schema": "rsi.patch/v1",
  "patch_id": "pat_01J...",
  "created_by_run": "run_01J...",
  "target": {
    "kind": "prompt_fragment | skill | workflow_policy | tool_allowlist | context_playbook | model_routing",
    "domain_id": "dom_web",
    "path": "domains/web/prompts/verify.md",
    "before_hash": "sha256:...",
    "after_hash": "sha256:...",
    "diff_format": "unified",
    "diff": "@@ -12,3 +12,5 @@ ..."
  },
  "hypothesis": {
    "bottleneck": "context_bloat",
    "mechanism": "verify 前重复 read 同一文件，平均 3.2 次/run，三次结果 hash 相同",
    "evidence": { "run_ids": ["run_a","run_b"], "metric": "dup_tool_ratio", "before": 0.31 }
  },
  "expected_effect": {
    "primary": { "metric": "cost_per_success", "delta_pct": -12 },
    "guardrails": [ { "metric": "pass_rate", "max_drop_pp": 1.0 },
                    { "metric": "p95_latency", "max_rise_pct": 3.0 } ]
  },
  "evaluation_plan": { "suite_id": "replay/web/v3", "n_pairs": 24, "design": "paired", "alpha": 0.05 },
  "risk": { "blast_radius": "domain", "reversible": true, "requires_human": false },
  "rollback": { "to_hash": "sha256:...", "auto_on": ["pass_rate_drop>1pp","error_rate>2x"] },
  "provenance": { "proposer_model": "x", "parent_patch_id": null, "created_at": "..." }
}
```

**硬约束【建议】**：
- `target.kind` 不得为 `verify_checks` / `gate_rule` / `scoring` / `replay_suite`——这四类是宪法层，只能人改。这条直接对应 STOP 的沙箱监控与 Skalse 的结论：一旦允许改评价器，系统必然学会改评价器。
- `diff` 必须是增量（delta），禁止整段重写——对应 ACE 对 context collapse 的解法【论文：2510.04618】。
- 每个 patch 必须带 `before_hash` 与 `rollback`，否则拒绝入库——对应 DGM 的 archive/回滚思想【论文：2505.22954】。
- `hypothesis.bottleneck` 必须是 §3.3 六类之一，且 `evidence.run_ids` 非空——**没有 trace 证据的 patch 一律视为 LLM 幻觉**。

### 6.5 防退化五闸门

1. **宪法闸门**：评价器不可自改（上面的硬约束，校验器强制）。
2. **证据闸门**：patch 必须挂 trace 证据与可计算机制，否则丢弃。
3. **统计闸门**：配对 A/B + always-valid 监控；n<20 对不得宣称改进。
4. **裁判闸门**：机器 verify 为唯一验收；人闸门负责「是否值得上线」而非「是否通过测试」；reviewer 与 proposer 若同模型，必须用不同 evidence 通道（对应 self-preference 证据）。
5. **回滚闸门**：自动回滚触发器（pass_rate 掉 >1pp、error_rate >2×、成本 >3%），且 archive 保留祖先版本。

### 6.6 路线（M0–M3）

- **M0 观测**：usage 落库、step span 化、trace 视图、replay 集冻结。无此步不做 RSI。
- **M1 只读提议**：agent 分析 trace 产出 patch 草案，人审后手动应用。此阶段目标是**校准提议质量**（多少草案被人接受），不是自动化率。
- **M2 半自动**：replay A/B 自动跑，通过四条件后自动应用可回滚 patch；宪法层仍人改。此阶段开始积累「哪种 patch 类型有效」的元数据。
- **M3 自改代码**：只在沙箱 + 人监督下（DGM 的做法），且先从「工具实现/脚本」这类低爆炸半径对象开始，不碰 daemon 核心调度。

### 6.7 风险清单

| 风险 | 触发条件 | 缓解 |
|---|---|---|
| 对 replay 集过拟合 | 同一批任务反复用于 proposal + evaluation | holdout 永不见于提议上下文；定期换血 |
| 指标 gaming | 单指标决策 | 成组指标 + 成本受控 |
| review 疲劳 | patch 数量超过人的审阅带宽 | 自动预筛（证据闸门）+ 每域每周配额 |
| prompt 膨胀 | 只加不减 | patch 必须声明替换而非仅追加；定期 audit token |
| 归因幻觉 | 无重放即下结论 | 强制 `evidence.run_ids` + 反事实重放 |
| 自我加速失控 | M3 阶段改调度器 | 爆炸半径限制 + 人闸门 + 速率上限 |

---

## 7. 参考文献（均为本次实际抓取）

**自改进 / RSI**
- Schmidhuber, *Gödel Machines*, arXiv cs/0309048 — https://arxiv.org/abs/cs/0309048
- Zelikman et al., *STOP: Self-Taught Optimizer*, arXiv 2310.02304 — https://arxiv.org/abs/2310.02304
- Hu, Lu, Clune, *ADAS / Meta Agent Search*, arXiv 2408.08435 — https://arxiv.org/abs/2408.08435
- Zhang et al., *Darwin Gödel Machine*, arXiv 2505.22954 — https://arxiv.org/abs/2505.22954
- Robeyns et al., *SICA*, arXiv 2504.15228 — https://arxiv.org/abs/2504.15228
- Yin et al., *Gödel Agent*, arXiv 2410.04444 — https://arxiv.org/abs/2410.04444
- Zweiger et al., *SEAL*, arXiv 2506.10943 — https://arxiv.org/abs/2506.10943
- Zhang et al., *AFlow*, arXiv 2410.10762 — https://arxiv.org/abs/2410.10762
- Zhang et al., *MaAS*, arXiv 2502.04180 — https://arxiv.org/abs/2502.04180
- Zhang et al., *ACE*, arXiv 2510.04618 — https://arxiv.org/abs/2510.04618
- Shinn et al., *Reflexion*, arXiv 2303.11366 — https://arxiv.org/abs/2303.11366
- Wang et al., *Voyager*, arXiv 2305.16291 — https://arxiv.org/abs/2305.16291
- Khattab et al., *DSPy*, arXiv 2310.03714 — https://arxiv.org/abs/2310.03714
- Yuksekgonul et al., *TextGrad*, arXiv 2406.07496 — https://arxiv.org/abs/2406.07496
- Yang et al., *OPRO*, arXiv 2309.03409 — https://arxiv.org/abs/2309.03409
- Agarwal et al., *PromptWizard*, arXiv 2405.18369 — https://arxiv.org/abs/2405.18369 ；ACL 2025 findings — https://aclanthology.org/2025.findings-acl.1025.pdf
- Gao et al., *A Survey of Self-Evolving Agents*, arXiv 2507.21046 — https://arxiv.org/abs/2507.21046

**退化与奖励**
- Skalse et al., *Defining and Characterizing Reward Hacking*, arXiv 2209.13085 — https://arxiv.org/abs/2209.13085
- Yuan et al., *Self-Rewarding Language Models*, arXiv 2401.10020 — https://arxiv.org/abs/2401.10020
- Panickssery, Bowman, Feng, *LLM Evaluators Recognize and Favor Their Own Generations*, arXiv 2404.13076 — https://arxiv.org/abs/2404.13076
- Shumailov et al., *AI models collapse when trained on recursively generated data*, Nature 2024 — https://www.nature.com/articles/s41586-024-07566-y

**评测与统计**
- Jimenez et al., *SWE-bench*, arXiv 2310.06770 — https://arxiv.org/abs/2310.06770
- Mialon et al., *GAIA*, arXiv 2311.12983 — https://arxiv.org/abs/2311.12983
- Zhou et al., *WebArena*, arXiv 2307.13854 — https://arxiv.org/abs/2307.13854
- Yao et al., *τ-bench*, arXiv 2406.12045 — https://arxiv.org/abs/2406.12045
- Liu et al., *AgentBench*, arXiv 2308.03688 — https://arxiv.org/abs/2308.03688
- Zhuge et al., *Agent-as-a-Judge*, arXiv 2410.10934 — https://arxiv.org/abs/2410.10934
- Lightman et al., *Let's Verify Step by Step*, arXiv 2305.20050 — https://arxiv.org/abs/2305.20050
- Kapoor et al., *AI Agents That Matter*, arXiv 2407.01502 — https://arxiv.org/abs/2407.01502
- Miller, *Adding Error Bars to Evals*, arXiv 2411.00640 — https://arxiv.org/abs/2411.00640
- Johari, Pekelis, Walsh, *Always Valid Inference*, arXiv 1512.04922 — https://arxiv.org/abs/1512.04922
- Lu et al., *The AI Scientist*, arXiv 2408.06292 — https://arxiv.org/abs/2408.06292

**标准与工程**
- OTel GenAI semantic conventions — https://github.com/open-telemetry/semantic-conventions-genai （spans: `docs/gen-ai/gen-ai-spans.md`；agent spans: `gen-ai-agent-spans.md`）
- van der Aalst, *Process Mining: Data Science in Action* — https://link.springer.com/book/10.1007/978-3-662-49851-4
- PM4Py（conformance checking / fitness / precision）— https://pm4py.fit.fraunhofer.de/
- `scipy.stats.bootstrap` — https://docs.scipy.org/doc/scipy/reference/generated/scipy.stats.bootstrap.html
- 本仓库：`internal/store/schema.sql`、`internal/proto/proto.go`、`internal/proto/acpbackend/session.go:230`、`backend.go:304`、`DESIGN.md`

---

## 8. 未核实与坦白

- **AlphaEvolve**（DeepMind 2025）的原始 blog 抓取失败（fetch failed），本文未引用其具体数字；若需引用其「进化搜索 + 自动评估器」范式，需重新抓取原文。
- 各论文的**正文细节**（如 DGM 的 archive 具体大小、SICA 的 reflection 模板、ACE 的 curator 更新频率）本次只依据摘要，未逐篇读全文；凡本文未标注具体机制细节处，均为摘要级结论。
- Gödel Machine 的「证明搜索」在原文中有更细的约束（如 proof searcher 的目标定理形式），本文只取了「改写前需证明净收益」这一条主干。
- AFlow 的摘要版本为 v4、DGM 为 v3；数字以其 arXiv 摘要页当时展示为准。

**如果只记一句话**：RSI 的难点不在生成候选，而在**建立一个便宜、可执行、不可被自己改写的验证器，并给它配上统计显著性**。agentwork 已经拥有前者的一半（域 verify + 人闸门），缺的是另一半（usage/span 级观测与配对统计）。