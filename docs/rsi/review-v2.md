# agentwork-int-v2 自迭代分支审查报告

> 审查对象：`origin/agentwork-int-v2` @ `ae12054`（相对 `origin/agentwork-int` 领先 28 个提交）
> 审查时间：2026-11-14
> 审查方式：静态代码审计 + 合成 fixture/受控 A/B + 真实复现 + 全量构建/测试
> 结论先行：**方向成立、产出有真实价值，但“效果证据链”不可信。工程修复多数可采纳；RSI 分析器目前只能当观测脚手架，不能当自动判优依据。**

---

## 0. 一句话结论

自迭代这一轮确实发现并修掉了几类真问题（P1 串行测试、P2 CDN 漂移、P3 重试风暴、AGENTS.md 集成冲突、depends_on 门控缺失），但同一批产出里混着**算错的指标、失效的规则、硬编码结论的报告脚本、不可比的基准数字**，而且现有测试只断言“形状”不断言“数值”，所以 CI 全绿并不能证明分析结论正确。

采纳建议分三档：

| 档位 | 内容 |
|---|---|
| **可直接采纳** | P3 非重试白名单（补测试）、P1 `t.Parallel()`（改回文件权限、修正宣传口径）、`ae12054` gitignore 修复（扩展到 project-skills）、`3ac0b1f` AGENTS.md 迁出（接受 fresh clone 少一份指引的代价） |
| **修后采纳** | depends_on 门控（必须先修依赖失败后的死锁）、P2 CDN 守卫（默认必须 strict，不能全 SKIP）、Training UI（先修 top_category、skipped retry、旧报告残留）、RSI 图/指标（先修 3 个 P0）、报告生成器（重写成数据驱动） |
| **不建议采纳** | `workflow_edge` 表、`lost_parallelism` 现公式、`content_capture/budget` 的 UI 承诺、`e31877b` 作为“生产竞态修复”、R2/R3 的收益结论、把 P-B 当代码 bug 修 |

---

## 1. 审查方法与边界

- **双路验证**：一路读代码/文档找声明；另一路用合成 fixture、受控 A/B、真实命令复现，确认“场景是否真的存在”，再判断“修改是否真的生效”。
- **环境**：Linux amd64，16 核；Go 1.26.5；v2 工作树 `/home/sinkear/developer/agentwork-v2-review`；本地 DB 为 2026-11-08 的早期 MVP 数据，**不是** R1–R3 生产库（生产数据在 `/root/.agentwork`，本机不可读）。
- **能验证的**：代码逻辑、合成数据、公开构建/测试、git/环境变量复现、UI 类型与构建契约。
- **不能验证的**：R1–R3 生产库原始行、R2 那 6 个失败的真实根因、UI 真机截图（本机没有对应 daemon）。
- 因此下文凡是“生产数据”结论，只作为**文档自述**引用，不当作已证实事实；凡是“代码/合成 fixture/命令复现”结论，都给了复现路径。

**验证基线（本机实测）**

- `GOCACHE=/tmp/gocache-v2-cold go build ./...` → PASS，23.103s（冷）。
- `go test ./... -count=1` → exit 0；daemon 2.537s、service 3.140s、cli 0.841s、link 0.008s、server 0.016s。
- 所以 v2 在本机是绿的；R3 报告里的 6 个失败是 `/root` HOME/AGENTWORK_HOME 环境隔离问题，不是本机必现。

---

## 2. 场景核验与修改效果总表

| # | 声称的问题场景 | 场景是否真实 | 修改方案 | 是否生效 | 采纳建议 |
|---|---|---|---|---|---|
| 1 | daemon 测试串行，占 `go test ./...` 56%（8.17s/14.5s） | **真实**：基线 16 个 daemon 测试文件 0 个 `t.Parallel()` | 70 个测试加 `t.Parallel()`，+70/-0 | **部分生效**：daemon 墙钟中位 3.615→3.44s（-4.8%）；全量 suite 配对中位仅省 0.56s，且 daemon 不在关键路径，无法归因 | 采纳，但删掉“-35%/-25%”口径 |
| 2 | `codes_test.go:96-99` 的 decode 失败应跳过；CDN 缺 AW.10000025~30 | **真实（但诊断错位）**：CDN HTTP 200，zh 543/en 351 keys，均缺 25–30；红是**缺码断言**（`:102`），不是 decode | 4 条路径全部改 `t.Skipf` | **生效但方向反了**：CI 变绿，同时也失去 FAIL 路径，真漂移会被静默吞掉 | 修订后采纳：默认 strict + 过期 allowlist + 开关 |
| 3 | 14 个失败 run 中 12 个同因 `Model unavailable`，重试 3 次纯烧时间 | **真实**：12 条同因可佐证；“12/14”分母已过期，analysis-r3 为 31 run/15 failed/3 cancelled（12/15） | `isNonRetryable` 白名单 + 终态后缀 + 跳过重试 | **生效**：14 例表驱动分类符合预期；瞬时错误仍重试；无双写 | 采纳，补提交测试、收窄匹配、补 auth/config 类 |
| 4 | P-B：`rsi_round` 两行都是 round_no=1、delta 恒 `{}` | **真实，但不是代码 bug**：每个 goal 一个 session，`MAX(round_no) WHERE session_id=?` 是对的；跨轮比较本就跨 session | 文档提议按 session 加锁/去重 | **不适用**：加锁解决不了跨轮；需要按 domain 建“轮次序列” | 采纳问题，改方案 |
| 5 | #14 基准冷/温漂移，23.012s vs 14.5s vs 11.3s | **真实**：本机冷/温 build 20.56 vs 2.85s（7.2×），test 26.15 vs 7.36s（3.6×） | 文档记录现象 | **未生效**：R2 的 -50% 是缓存假象；R3 的 build -7% 与“本轮未改代码”自相矛盾 | 采纳 #14 现象，重定基准协议 |
| 6 | #15 `/rsi` 文档端点 404 | **真实但只是文档错**：8 个真实端点都在；`/rsi` 从未有别名 | 改文档 | **生效**：文档已列真实端点 | 采纳（文档修复） |
| 7 | #4b unset `AGENTWORK_HOME` 后 `go: command not found`（127） | **环境层面可能真实**：daemon PATH 无 `/usr/local/go/bin`；生产复现不可得 | 手工 unset + 前置检查 | **未真正修复**：只是绕过；根因在子进程环境继承 | 归入 P-A，做环境消毒 |
| 8 | depends_on：sg-O 必须在 sg-B 验证后才跑 | **真实**：这是自迭代编排的硬需求 | 创建时门控 + 验证后唤醒 | **happy path 生效**；依赖失败后**会永久卡死**；唤醒 run 丢失 trigger comment 锚点 | 修后采纳（阻断合并） |
| 9 | RSI 图/指标能定位串行、丢失并行、重试放大 | **场景真实**（R1/R3 确实串行、重试多），但指标实现不可信 | 读时装配 + 冻结快照 + 9 类规则 | **作为决策依据未生效**：CP 扇入重复计数、seq 边进 CP、review 无边、lost_parallelism 恒假 | 只采纳脚手架，先修 P0 |
| 10 | 9 类 finding + human_wait 能指导优化 | 部分真实 | 规则表 | **部分生效**：serial/split 可算但被污染；lost/retry-L2 死；human_wait 不进指标 | 修规则后再用 |
| 11 | Training 页能看每轮训练与 workflow | **真实需求**；页面能构建、路由通 | 新页面 8 个路由 + 图/洞察/趋势 | **部分生效**：3 个 MAJOR 契约缺陷（详情 top 为空、skipped 可点重跑、重跑期间显示旧报告） | 修后采纳 |
| 12 | `TestPeerCallRoundtrip` 竞态是生产“method not found”根因 | **场景真实（ordering/TOCTOU，不是 data race）** | 测试服务器先注册 handler 再启动 reader | **生产未生效**：只改了测试 helper，生产调用点仍是 NewPeer→Handle | 不采纳为生产修复；按生产顺序修 |
| 13 | AGENTS.md 被 `.gitignore` 忽略时 `git add` 点名 exclude 会 exit 1 | **真实，已复现** | `check-ignore` 为真时不加 pathspec | **生效**：exit 0 且正确跳过；但 project-skills 的 exclude 仍未加守卫 | 采纳 + 扩展 |
| 14 | 根级 AGENTS.md 被平台注入，tracked 后集成 merge 冲突 | **真实**：平台每次注入未跟踪文件，tracked 版本会让 `git merge` 拒绝覆盖 | 删 tracked AGENTS.md、忽略之、约定迁 playbook §6 | **生效**：集成冲突根因消除；代价是 fresh clone 少指引 | 采纳 |
| 15 | 需要一个可复用的“轮次报告生成器” | **真实需求**；实现只是 R3 一次性打印器 | `scripts/rsi-round-report.sh` 454 行 | **未生效**：硬编码 R3 结论、`--since/--until` 会崩、API 挂时仍 exit 0 | 不采纳为可信工具，重写 |
| 16 | P-A：`AGENTWORK_HOME` 泄漏进 verify 子进程，污染测试仓库 | **真实（代码+复现）**：`verify.go` 的 `sh -c` 不传 `c.Env`；`paths.go` 优先 AGENTWORK_HOME | 文档提议 unset/隔离 | **v2 未实现**；且 R2 fetch-128 根因未能复现 | 采纳方向，补实现与回归测试 |
| 17 | 文档与代码一致 | 约 10 条真、8 条假/陈旧 | 改文档 | 部分 | 逐条修（见 §3.10） |

---

## 3. 分项详述

### 3.1 P1：测试并行化

**场景核实**：真实。基线 `internal/daemon` 16 个测试文件、0 个 `t.Parallel()`；串行测试占满 0–1.544s，是包内主要耗时。

**修改**：70 个测试加 `t.Parallel()`（gate 2、handoff_cancel 8、intake 30、prompt_context 11、runaway 3、schedule 4、verify 12），33 个仍串行（含用 `t.Setenv` 的 deliver/digest/worktree）。

**效果（受控 A/B，`e2bda1d` vs `e08cb72`，GOCACHE 温、8 次）**：

- daemon 墙钟中位 3.615s → 3.44s（-4.8%）；包 `Elapsed` 2.51 → 2.39s；`GOMAXPROCS=8` 3.68 → 3.53s。
- 全量 suite 5 组交错 10.09 → 7.38s 中位，但配对中位只省 0.56s（~-6%）；daemon 省下的 0.17s 不在关键路径（service 约 4.6s），**全量收益不可归因**。
- `-race -count=2` PASS 24.57s；覆盖 44.7% → 44.8%；diff +70/-0，无删断言。
- 副作用：16 个测试文件 mode 644 → 755，应改回。

**为什么收益有限**：Go 顶层 `t.Parallel()` 要等所有串行测试跑完才一起放行，70 个并行测试在 1.544s 之后作为一个批次跑 0.766s，而它们原先串行总耗时约 0.80s。所以省的是批次内的重叠，不是“串行段被并行覆盖”。

**结论**：修改本身正确、可采纳；但文档里的“8.17s → 3–4s”“-35%”“全量 -25%”在本机（16 核）不可复现，属机器差异与口径问题，必须改口径。

---

### 3.2 P2：CDN 漂移守卫

**场景核实**：

- before 树（`112d50f`）确实 FAIL（rc=1），但红因是 **`codes_test.go:102` 缺码断言**：CDN 返回合法 JSON、缺少 `AW.10000025`，不是 decode 失败。proposal 里的“decode 失败”诊断不准确。
- CDN 实测 HTTP 200：zh-cn 543 keys、en-us 351 keys，两个 locale 都恰好缺 `AW.10000025–30`；23/24 存在；31/32 不在仓库导出范围。
- HEAD 没有 `AGENTWORK_SKIP_CDN_DRIFT`（v1 `8ca9957` 有，且判断写成 `!= ""` 的 bug）。

**修改**：网络不可达、非 200、畸形 body、缺码四条路径全部 `t.Skipf`；缺码 SKIP 时打印 6 个码。

**效果与问题**：

- CI 从红变绿，但**守卫已无 FAIL 路径**。用 200+HTML 的 scratch 验证畸形 body 也走 SKIP。真出现新的 CDN 漂移时，测试会静默跳过，等于把“漂移检测器”改成了“漂移消音器”。
- 正确方向应是：**默认 strict**（缺码 FAIL），配一个**带过期时间的 allowlist**（当前只放行已知的 6 个码），再加显式开关 `AGENTWORK_SKIP_CDN_DRIFT=1`（判断必须 `== "1"`）；畸形 JSON 可以 SKIP，但要有告警；再加一个定时 strict job。

**结论**：不要原样采纳；按上述修订后采纳。

---

### 3.3 P3：非重试白名单

**场景核实**：真实。12 条失败同因：

`acp: session/prompt: Internal error: Model unavailable: opencode/muse-ember-1.0`

“12/14”分母已过期，仓库后审计是 31 run / 15 failed / 3 cancelled（即 12/15）。

**修改**：`nonRetryableMarkers = []string{"Model unavailable"}`；`isNonRetryable` 做 TrimSpace、空串 false、大小写敏感子串匹配；命中后 summary 追加“（环境/配置错误，重试无效）”，跳过重试直接进 failed。退避 30s/60s 未变；终态只有一条条件 UPDATE + 一条 `goal:finished`，无双写。

**效果**：14 例表驱动分类符合预期：线上错误、带空格、其他 provider、agent 输出里引用该子串都判 true；空、超时、429、502、connection refused、auth invalid key、小写都判 false。

**风险**：裸子串会误判（agent 复述错误文本也会被当成不可重试）；auth/config 类未覆盖；仓库里**没有提交测试**；failed 分支缺 done 分支的 `RowsAffected` 检查与 `break`（既有低危问题）。

**结论**：采纳；补表驱动测试、把 marker 改成结构化错误码/前缀匹配、补 auth 类。

---

### 3.4 depends_on 执行门控（新功能，阻断合并项）

**设计**：`CreateSubGoal(..., dependsOn []string, ...)` 在创建事务里写 `sub_goal.depends_on`；依赖未全 verified 时不建 run（门控），验证通过时在**同一事务**里 `wakeDependentsTx` 唤醒。测试覆盖 happy path 与创建期校验。

**真实缺陷（代码已确认，测试未覆盖）**：

1. **依赖失败后子任务永久卡死**。失败/取消分支只 `execution_attempt+1`，不会级联失败/取消 dependent；`wakeDependentsTx` 只在 verified 时调用，且只唤醒“status='running' 且没有 run”的子任务；依赖永远不会 verified → 子任务永远无 run。
2. 目标终态守卫把 `status NOT IN ('verified','cancelled','failed')` 计为未完成 → **goal 永远到不了 done/review**；`WaitChildren` 10 分钟后超时放弃。用户只能手工 `CancelSubGoal`。
3. 唤醒出来的 run `triggerCommentID=""`：brief 还在（daemon 从 sub_goal 覆盖 title/desc），但 wake 行显示“the platform”而不是派发者，agent 的报告评论也不会挂在 dispatch comment 下（`comment.go:152` 只在 trigger_comment_id 非空时挂父评论）。审计链断。
4. gated 子任务仍占 `maxActiveSubGoals=20` 配额，容易把 active 名额耗尽。

**验收场景（必须补）**：dep 失败后 child 应自动 failed（或明确策略）且 goal 能进 review；dep 取消；多依赖部分完成；goal 取消时 gated child 的清理；唤醒 run 的 trigger comment/anchor；配额交互。

**结论**：功能方向对、happy path 可用；但当前状态**不能合并**。先修 liveness（失败级联/唤醒策略）与 trigger 锚点，再补上述测试。

---

### 3.5 RSI 图/指标/分析器（核心可信度问题）

方向（读时装配、冻结快照、确定性规则、不调用 LLM）是好的；但当前实现存在会直接改变结论的缺陷。

#### 3.5.1 CP 扇入重复计数（新发现，最严重）

`internal/service/workflow.go:438-440`：

```go
if p.length+d.length > d.length {
    d = dp{length: p.length + d.length, prev: from}
}
```

`d.length` 已经包含“前一个 pred + 本节点”，第二个 pred 再相加就重复计入。正确应为 `p.length + n.DurationMS`。

合成 fixture（A、B 各 30min 并行 → C 依赖 A、B，10min）：

- 正确 CP = 40min；
- 实测 CP = **4,200,000ms（70min）**，serialization_loss = 2,400,000ms（正确 600,000），speedup = 1.000（正确 1.750）。
- Critical path 列表显示 sg-b、sg-c，但长度仍把两条 pred 都加了。
- 持久化到 `rsi_report.metrics_json` 的值同样是错的。

扇入 ≥2 的任何 DAG 都会中招；RSI 的“review 汇聚”“集成汇聚”恰好都是扇入场景。

#### 3.5.2 seq 推断边进了关键路径

`workflow.go:407-416` 用**全部**边构建 `preds`，没有按 `Source` 过滤；`seq` 边是 O(n²) 两两推断（A.end < B.start 且无边），n=40/80/160 时 seq 边 780/3160/12720，构建 3.86/34.7/334.1ms，CP 被推成 3.2985e17/5.2777e18/5.2777e18ms，speedup≈0。

这等于把“两个任务时间上先后发生”当成“因果依赖”，CP 退化为“最长非重叠时间链”，`parallel_speedup`、`serialization_loss`、`lost_parallelism` 全部失真。代码注释和 `docs/RSI.md:88` 都说 seq 只是“L1 evidence / 虚线展示”，但 DP 实际把它当真依赖。

#### 3.5.3 review 节点没有入边，却被计入工作量

`workflow.go:295-318` 只创建 review 节点，不建任何入边（consult 边只连 consult）。但 review 的 busy 时间进入 `work_sum`、lane、verify_overhead 和 speedup 分母。

fixture：2 个 agent 各 30min + 2 个 verify 各 10min + review 55min + consult 10min：

- 节点 8、边 6；work = 8.7e6ms；**CP = 3.3e6ms（review 节点自己）**；speedup = 2.636。
- 若 review 正确挂到两个 agent 上，真 CP = 85min，speedup = 1.706。
- verify_overhead = 0.517（75/145），serialization_loss = 0。

#### 3.5.4 其他会改变结论的缺陷

| 缺陷 | 位置 | 后果 |
|---|---|---|
| integrate/chg 节点零时长 | `workflow.go:350-369` | `change` 只有 created_at，没有 integrated_at；集成成本不可见、永不在 CP |
| queue_wait 混入依赖等待 | `workflow.go:183-195` | `sg.created_at→首个 run start` 含门控等待；合成 fixture 120min 里 60min 是依赖等待；`proposals.md` 的 queue_wait≈wall 100.3% 正是这个 artifact |
| human_wait 不进 metrics | `rsi_analyze.go:445-458`、`workflow.go:561-575` | finding 用 gate review_duration 当分子、wall 当分母，能算出“人工等待占墙钟 600%”；delta 白名单有 human_wait_ms 但永远不产出 |
| lost_parallelism 恒假 | `rsi_analyze.go:267-281` | `speedup` 就是 `work/cp`，条件 `gain>=2 && speedup<gain*0.7` 恒为 `1<0.7`；4 个并行 30min agent 的 probe，gain=4.0、speedup=4.0，不出 finding |
| retry_amplification L2 不可达 | `rsi_analyze.go:424-443` | baseline 只产 6 个 key（无 retry_amplification），L2 永远读不到；且 baseline 无 domain 过滤；所有规则实际只跑 L1 |
| 机器验证不写 verification_result | `subgoal.go:332-370` | `verifier_id==""` 时直接 verified，无 `v:` 节点；verify_rounds/rework_ratio/brief_quality 对机器验证全盲 |
| verify 节点状态卡 rejected | `workflow.go:270-273` | 后续 passed 也不重置 |
| CP 结果不确定 | `workflow.go:443-451` | map 迭代 + 严格 `>`；400 次构建两个等长 agent，CP 落在 sg-a 361 次 / sg-b 39 次 |
| `workflow_edge` 死表 | `schema.sql:571-583`；Go 引用 0 | docs/RSI.md:27 的“写入同步落行、读取合并”是假的；唯一真相是 `sub_goal.depends_on` JSON |
| 节点 key 文档漂移 | docs `verify:`/`change:` vs 代码 `v:`/`chg:` | 文档/UI 对不上 |
| E2E 只断形状 | `rsi_workflow_test.go:149-153,204-223` | 只断言 speedup≥1、queue_wait>0、round 1→2；不断言 CP 组成/数值、fan-in、seq 数、review/integrate、机器验证、跨 session、确定性 |

#### 3.5.5 轮次账本（P-B）

把 P-B 说成“`MAX(round_no) WHERE session_id` 的代码 bug”不准确：同一 session 重跑分析确实会得到 rounds [1,2]，有唯一索引 + `analyzeMu` 串行化，不会撞号。

真实问题是**工作流设计**：每个训练 goal 一个 session，每个 R<n> 都是新 goal → 永远 round 1、delta `{}`。所以“用 delta 证明优化生效”在当前模型下不可能成立。修法是建立 **domain 级轮次序列**（join `goal.domain_id` 或显式 experiment id），delta 对“同 domain 上一轮”算；不要给 session 加锁。

#### 3.5.6 分析器可用性评级

| 规则 | 可算 | 可信 |
|---|---|---|
| serial_chain | 可算 | 被 seq 链污染，会虚报 |
| split_candidate | 可算 | CP 污染会带偏 |
| lost_parallelism | 可算 | **恒假（死规则）** |
| merge_candidate | 可算 | 相对干净 |
| wrong_assignee | 可算 | lane 含 review/verify/consult |
| retry_amplification | 可算 | L1 可用，L2 不可达 |
| ping_pong | 可算 | 无状态过滤 |
| brief_quality | 部分 | 只看人工验证 |
| consult_repeat | 可算 | 无状态过滤 |
| human_wait | 可算 | 只是观测，不进 metrics |

**结论**：`docs/RSI.md` 的描述与 UI 可以作为“观测面板”保留；但任何“因为 RSI 说 A 比 B 好，所以改 A”的自动决策都必须停用，直到 P0 修复 + 数值型回归测试就位。

---

### 3.6 Training UI / API

**通过项**：8 个真实路由都在；UI `api.ts` 与 `/backend` 代理匹配；`tsc --noEmit`、`next build`（19/19）、`go build` 全过；empty/waiting/analyzing/ready-0/ready/failed/skipped/live/frozen/loading/error 都有渲染路径。

**MAJOR（修后采纳）**：

1. `GetSession` 不填 `top_category/top_title`，只有 `ListSessions` 每行补查 → 详情页“Top finding”块永远空；列表显示 “human_wait / 人工等待占墙钟 38%”，详情是空串。
2. skipped 会话显示可点的“重跑分析”，后端 `Retry` 只允许 waiting/failed/ready → 点了 400。
3. Retry 只把 status 改成 analyzing，旧 report 仍被 `GetSession` 返回 → 重跑期间和重跑失败后，overview/findings/insights 展示上一轮旧数据（`overview.tsx:44` 只看 `!report`）。

**MINOR**：waiting/skipped 把 0 当真实值渲染；analyzing 文案说“图已冻结”但 API 返回 live 图；30 节点链 ≈7.5k px 无缩放/适配、贝塞尔穿卡片、review 节点被强制最后一层；overview 查询错误静默；列表硬上限 50 无分页；“全部”只是把 overview 窗口放到 365d；findings 空态文案对 skipped/failed 不对；verify key `v:` 与文档不一致；human_wait delta 永不产出。

---

### 3.7 工程化修复

#### gitignore / AGENTS.md（`ae12054`）

**场景真实且已复现**：临时仓库 `.gitignore=AGENTS.md`，`git add -A -- ':(exclude)AGENTS.md'` → exit 1：

```
The following paths are ignored by one of your .gitignore files: AGENTS.md
```

`git add -A --`（无 pathspec）则 exit 0 并静默跳过被忽略文件。

**修改正确**：`gitAddExcludeArgs` 只在 `check-ignore` 说不被忽略时才加 exclude；不碰 `git add -f`。测试只覆盖“被忽略”分支（`-run GitAddExclude` 才匹配；`-run Gitwork` 匹配不到）。

**残留同类 bug**：project-skills 的 exclude 仍无条件添加（`gitwork.go:187-189`）；若仓库忽略 `.claude/`，`:(exclude).claude/skills` 会触发同样的 exit 1。采纳时一并修 + 补测试。

#### AGENTS.md 迁出（`3ac0b1f`）

tracked 的根 AGENTS.md 会导致“平台注入未跟踪 AGENTS.md + 集成 merge 拒绝覆盖”的冲突。删 tracked、加 `.gitignore`、约定迁 `docs/rsi/self-iter-playbook.md §6` 是正确解法。代价：fresh clone 不再有根指引；平台每 run 还会注入，实际运行不受影响。可采纳。

#### link “竞态”（`e31877b`）

真实问题是**启动顺序/TOCTOU**（reader 先跑、handler 未注册 → method not found），不是 Go data race；`-race` 测不出来（`Peer.handlers/pending` 有 mutex）。

提交只给测试加了 `newPeerNoRead`/`StartReadLoop` 和 `NewPeerForTest`；**生产调用点仍是** `NewPeer` → `Handle`：

- `internal/server/server.go:249`（connect）与 `:512`（/rpc）
- `cmd/agentwork-cli/connect.go:172`、`rpc.go:45`

所以该提交对生产“method not found”无效，只能算测试稳定性修复。要修就改生产顺序：先注册 handler，再 `startReadLoop`，并加双启动保护。

#### 报告脚本（`scripts/rsi-round-report.sh`）

不是“SQL / git add -f”问题（两个怀疑都是假前提）；真实问题：

- 轮次选择正则 `RSI\s*R{n}\b` 匹配不了 playbook 规定的 `RSI 自迭代 R1：基准测量` → ①/④ 空。
- 文档写的 `--since R1 --until R3` 会 `ValueError` 崩；数字 `--until` 是 no-op（输出字节相同）。
- ③ 效果表、② 历史表、④ 叙述、⑤ 候选**全部硬编码 R3 结论**；API 挂时仍 exit 0，并打印 stale “R4 usually in progress”。
- 有 metrics 的轮次里任选 session；空 metrics 无 fallback；声称的 commit+diff 对比链接不存在。

**结论**：不能当轮次工具用。要重写成“数据驱动 + 缺数据 fail loud + 模板只留结构”，并把标题约定写进生成器/测试。

#### P-A：AGENTWORK_HOME 泄漏

**真实**：`internal/daemon/verify.go:171-186` 和 `:373-381` 的 `sh -c` 不传 `c.Env`，子进程继承 daemon 环境；`internal/paths/paths.go:29-43` 优先 `AGENTWORK_HOME`。复现：设置 `AGENTWORK_HOME=/tmp/p-a-leak-home` 跑测试，测试通过但仓库写到了 `/tmp/p-a-leak-home/repos/dom-1`，无视 `t.Setenv("HOME")`。

**但 R2 fetch-128 的具体机制未复现**：同一 AGENTWORK_HOME 下 `TestParallelConsultWorktreeDetaches` 两次都通过（0.145s/0.052s）。所以“P-A 是 R2 失败根因”目前只是强相关假设，不能写成已证实。

**v2 未实现 P-A**：没有 `c.Env`/sanitize，没有测试，也没有对应提交。提案方向对但不完整：要同时覆盖 `runVerifyOutput`，把 `AGENTWORK_HOME` 和 `HOME` 都指向 per-run 临时目录（只 unset 仍会落到真实 HOME），保留 PATH/GOCACHE/proxy 并探测 toolchain，加回归测试。

---

### 3.8 基准与“收益”结论

| 报告 | 结论 | 审查判定 |
|---|---|---|
| benchmark-report-r2 | 23.012→11.559s（-50%），daemon 7.696→5.911（-23%），P1/P2/P3 均达成 | **-50% 不成立**：23.012s 是首跑含测试二进制编译；11.559s 是温 build cache。同机冷/温 test 26.15 vs 7.36s（3.6×），不能跨列相减 |
| benchmark-report-r3 | cold build 41.285 vs 44.417（-7.1%），“P1 收益保持” | **不成立**：R3 自述“本轮未改任何代码”；P1 只动测试，不可能影响 build；-7% 是噪声 |
| analysis-r3 #14 | 承认 23.012 含编译，冷/温 build 38–44 vs 3.9–5.6s | **成立**，这是本轮最诚实的自我修正 |
| proposals.md（R1） | queue_wait≈wall 证明 sg-O 被依赖阻塞 | 结论可能对，**证据是 metric artifact**：queue_wait 定义就含依赖等待 |

**公平协议建议**：固定 commit/机器/GOMAXPROCS/module cache；冷/温分列、同列跨轮比较、绝不跨列相减；冷跑用全新 GOCACHE；每格 ≥3 次取中位并给 min/max；A/B 交错；编译与执行分开（包 `Elapsed` 或 `go test -c`）；记录负载与测试数。

---

### 3.9 文档与流程约定

**真的**：8 个路由、`/rsi` 与 `/runs/{id}` 确实 404、阈值、baseline 20/<5、training_session 1:1、`MarkDelivered→OnGoalTerminal`、状态机。

**假/陈旧**：

- `docs/RSI.md:98` 说 `TestCodesMatchRemoteTermJSON` 离线/缺码会 FAIL——现在 SKIP。
- `docs/RSI.md:27` 的 `workflow_edge` 写入/合并——代码里没有。
- “同类历史≥2 升 L2”——未实现。
- node key `verify:`/`change:` 与代码 `v:`/`chg:` 不一致。
- playbook 要求 commit+diff 对比链接——脚本只给 commit 页链接。
- playbook 的标题约定与脚本正则不兼容。
- T6“平台不得自动改 meta 层”没有代码强制。
- `docs/PATHS.md:28` “tests/containers safe”与 P-A 矛盾。
- 代码注释 `rsi_analyze.go:25-29` 把 10 类写成 9 类。
- `docs/RSI.md:75` 趋势“每轮一个点”实际是每条 ledger 行一个点。

---

## 4. 修复优先级（若采纳该分支）

### P0（阻断合并/阻断自动决策）

1. 修 CP 扇入：`p.length + n.DurationMS`，加扇入 golden fixture（40min/1.750×）。
2. CP 只用 explicit 结构边（depends_on/verified_by/consult/integrated/created），排除 `source=="inferred"`；seq 只用于展示/辅助指标。
3. review 节点挂到被审对象（或单独隔离，不进 work_sum/speedup）。
4. depends_on liveness：依赖 failed/cancelled 时级联失败/取消 dependent，goal 能进 review；补 5 个缺失测试。
5. 唤醒 run 恢复 trigger comment/anchor（或显式“platform 释放”标记，不能静默丢审计链）。

### P1（可信度）

6. queue_wait 拆成 dependency_wait 与 dispatch_wait（`ready_at=max(created_at, pred end)`）。
7. human_wait_ms 写进 graph metrics，delta 才有意义；修正 finding 分母（wall 含 gate 等待）。
8. 修 lost_parallelism 公式（用可并行工作量/CP，不是 work/cp）；retry L2 用真实 baseline key + domain 过滤。
9. 机器验证写 `verification_result`（verifier_type=machine, passed=true）；verify 节点状态可重置。
10. 轮次按 domain 序列编号与 delta。
11. 稳定 tie-break（按 key 排序，不用 map 迭代）。
12. P2 默认 strict + 过期 allowlist；补 FAIL 路径测试。

### P2（体验/工程）

13. GetSession 填 top 字段；skipped 禁用重跑；重跑期间显示“分析中”而不是旧报告（或返回 report 状态）。
14. 图加 zoom/fit、节点限宽、review 层正确；overview 错误可见；列表分页。
15. gitignore 守卫覆盖 project-skills；file mode 改回 644。
16. 删 `workflow_edge` 或实现写通；统一文档 key；补 docs 校准。
17. link 生产顺序修复（register→startReadLoop）+ 双启动保护。
18. 报告脚本重写：数据驱动、参数校验、缺数据非零退出、无硬编码结论。
19. CLI/MCP 支持 execution_mode/depends_on 等字段，避免只能手写 curl。

---

## 5. 可采纳修改清单（给合并/后续）

### 可直接拿

- `e2bda1d` 中的 **P3**（白名单 + 终态后缀）。补：表驱动测试、匹配收窄、auth/config 类。
- `e08cb72` 的 **P1**。改：16 个文件 mode 755→644；文档口径改为“daemon -4.8%，全量不可归因”。
- `ae12054` 的 **gitignore 守卫**。扩展：project-skills 同类 exclude；补“未被忽略的 AGENTS.md”测试。
- `3ac0b1f` 的 **AGENTS.md 迁出**。接受 fresh clone 少根指引的代价。
- `docs/RSI.md` 的**结构与真实 API 表**（修正 §3.9 的假条目后）。
- `docs/rsi/analysis-r3.md` 作为**复盘材料**（它承认了 #13/#14/#15），但不能当作“修改有效”的证据。
- `depends_on` 的**数据模型与创建期校验**（在修 liveness 之后）。

### 修后拿

- **depends_on 门控**：先修失败级联 + trigger 锚点，再补测试。
- **Training UI**：先修 3 个 MAJOR。
- **RSI 分析器**：先修 §4 的 P0/P1；在那之前只保留“观测”，不输出“优化建议”结论。
- **报告生成器**：重写后拿。
- **P2**：反向默认（strict）+ allowlist 后拿。

### 不要拿

- `workflow_edge` 表（没有写入/读取，纯负债）。
- `lost_parallelism` 现公式；`retry L2` 现实现。
- `content_capture/budget_usd/sample_rate` 的 UI 承诺（代码没实现；要么实现，要么删文案）。
- `e31877b` 作为“生产竞态修复”（只是测试修复）。
- `scripts/rsi-round-report.sh` 作为可信工具（当前是 R3 一次性打印器）。
- R2 的“-50%”“P1/P2/P3 预期均达成”；R3 的“build -7% = P1 收益保持”。
- P-B 的“加锁/去重”方案（问题真、方案错）。

### 合并策略建议

不建议把 v2 整分支直接 merge 回 `agentwork-int`：v2 把 R2 的 P1/P2/P3 用**不同 SHA** 重放（`e08cb72`/`e2bda1d` vs `3cf2116`/`8ca9957`/`18a8c3c`），整分支合并会制造重复/冲突历史，且会把坏的门控、报告脚本、指标一起带进去。

更稳的做法：**按类别 cherry-pick**——先 P3、P1、gitignore、AGENTS.md 四组；再单独一个 PR 修 depends_on liveness；RSI 分析器作为“实验特性”单独 PR，默认关闭或只读展示，P0 修完再启用。

---

## 6. 自迭代质量评价（复盘用）

### 6.1 做得好的

- **问题发现是真的**：P1（串行测试）、P2（CDN 缺码）、P3（重试风暴）、P-A（环境泄漏）、P-B（跨轮 delta 空）、#14（冷/温基准）都能在代码或复现里找到对应物，不是凭空造需求。
- **有自我修正**：analysis-r3 主动承认 #13 编号问题、#14 基准不可比、#15 文档端点错，这在自迭代系统里是稀缺品质。
- **工程闭环意识**：知道要“提案 → 实现 → 基准 → 报告 → 约定”，并留下了 playbook 和轮次产物。

### 6.2 做得差的

- **用坏尺子量自己**：CP 算错、seq 进 CP、lost_parallelism 恒假，导致“串行/并行”诊断本身不可信；却又拿这些指标去证明优化有效。
- **测试只保形状**：E2E 断言 speedup≥1，任何正数都过；没有 golden 数值，CI 绿=假安全。
- **报告先有结论再找数据**：脚本硬编码 R3 叙事、API 挂了也 exit 0；R2/R3 收益把缓存噪声当改进。
- **修复宣传大于证据**：link 提交说修竞态，实际只改测试；P2 把“消音”叫“修复”；P-B 把设计问题叫代码 bug。
- **文档漂移**：端点、key、L2、T6、diff 链接多处与代码不一致。
- **自迭代没有独立验收**：同一套系统既提方案、又实现、又打分，缺少外部 verifier。

### 6.3 评级（主观，但给依据）

| 维度 | 评级 | 依据 |
|---|---|---|
| 问题发现 | A- | 多数场景真实，且有生产复盘支撑 |
| 修复有效性 | B- | P3/P1/gitignore/AGENTS.md 真修好；门控/RSI/UI/link 未达可用 |
| 验证严谨性 | D | 形状断言、缓存噪声、硬编码报告、生产数据缺失 |
| 文档与可复盘 | B | 结构完整、有 playbook/analysis，但多处失真 |
| 自动化闭环 | C | 能跑通轮次，但结论需人复核；报告工具不可复用 |

**一句话**：这是一个**有价值的自迭代原型**，但还不是**可信的自优化器**。它的最佳用法是“发现问题 + 生成待验证假设”，而不是“自动判定优化是否生效”。

---

## 7. 复现与证据索引

| 证据 | 位置/命令 |
|---|---|
| 全量测试 | `cd /home/sinkear/developer/agentwork-v2-review && GOCACHE=/tmp/gocache-v2-cold go test ./... -count=1` |
| 冷构建 | `GOCACHE=/tmp/gocache-v2-cold go build ./...`（23.103s） |
| CP 扇入 | `internal/service/zz_core_probe_test.go`（probe build tag）；日志 `/tmp/probe.log` |
| 400 次确定性 | probe `TestProbeDeterminism`；结果 sg-a 361 / sg-b 39 |
| P1 A/B | `review-claims.md`；e2bda1d vs e08cb72 |
| P2 复现 | `codes_test.go:102`（before）；CDN 543/351 keys；`review-claims.md` |
| P3 分类 | `goal.go:109-127,1427-1429`；14 例表 |
| gitignore 复现 | 临时仓库 + `.gitignore=AGENTS.md`；`gitwork.go:185-216` |
| AGENTWORK_HOME 泄漏 | `AGENTWORK_HOME=/tmp/p-a-leak-home go test …`；`verify.go:171-186` |
| 报告脚本 | `scripts/rsi-round-report.sh`；DB copy + 两个临时 daemon（47899/47898） |
| UI 构建 | `web: tsc --noEmit`、`next build`（19/19） |
| 审计报告 | `review-core.md`、`review-web.md`、`review-claims.md`、`review-process.md`（各 worktree，未提交） |

---

## 8. 局限与未决

1. **生产数据不可得**：`/root/.agentwork` 无权限，R1–R3 的 run/round 原始行无法独立核对；“12/15”“31 run”等只能引用分析文档。
2. **R2 fetch-128 根因未证实**：P-A 泄漏本身有代码与复现，但“它就是 R2 那 6 个失败的原因”未能复现闭环。
3. **机器差异**：本机 16 核 amd64 vs 文档 7 vCPU arm64，绝对耗时不可直接对比；本报告只接受同机受控 A/B。
4. **UI 未做真机截图**：没有对应 daemon，只做了类型/构建/契约验证。
5. **未验证真实自迭代 R4**：需要一次新的 train goal 才能验证修完门控/指标后闭环是否成立；本轮未跑。

---

## 9. 给下一轮的自迭代规则（建议写进 playbook）

1. **先证伪，再实现**：每个提案必须附“失败前”的复现命令/测试；没有失败证据的提案不进实现。
2. **数值断言**：涉及指标/性能的测试必须断言具体数值与容差（golden fixture），禁止只断 `>=1` 或“非空”。
3. **独立验收**：自迭代系统不得给自己的效果打分；效果判断由人（或独立脚本+固定协议）做。
4. **基准协议**：冷/温分列、同机同 cache 状态、≥3 次中位、编译/执行分离、记录负载；禁止跨列相减。
5. **报告生成器必须 fail loud**：缺数据非零退出，禁止硬编码上一轮结论；轮次选择用显式 id/名字，不用正则猜标题。
6. **单一真相**：`workflow_edge` 要么写通要么删；`depends_on` 只能有一个存储/读取路径。
7. **meta 层改动必须人工批准**（T6），但要有代码层面的“禁止自动 apply”闸门，而不是只写在文档里。
8. **保留原始数据**：RSI 需要跨轮复盘，生产 DB/ledger 至少保留 N 轮，否则“优化是否生效”永远无法回查。

## 10. 修复落地（2026-11-14）

"修后可拿"范围的修复已落在独立分支 `agentwork-int-v2-fix`（worktree `/home/sinkear/developer/agentwork-v2-fix`，基线 `ae12054`，未 push），逐项场景→修复→证据见 `docs/rsi/fix-v2.md`。要点：

- 指标核心：CP 扇入重复计数修正；推断 seq 边移出 CP；review 与执行分离统计；`parallel_speedup=work_sum/wall`；`serialization_loss=wall-cp`；队列等待拆依赖/派发；human_wait 进入 metrics；retry 放大取最大 Attempt；baseline/轮次按 domain。
- 门控 liveness：依赖失败/取消级联失败仍 gated 的子任务；释放 run 保留 dispatch 锚点；机器验证补 `verification_result` 行；RetrySubGoal 不得绕过门控。
- 工程项：P2 缺码默认 FAIL + 到期 allowlist；gitignore 所有 exclude 先 check-ignore；link 生产调用点 handler-first + 幂等 StartReadLoop；报告脚本改为数据驱动、fail-loud；Training UI 状态化；文档指标口径校准。
- 验证：`go test ./...` 全绿；`go vet ./...` 无输出；web `tsc --noEmit` / `next build` 通过；报告脚本 fixture 五节 + 死 API exit 1；指标前后对比与 23 行场景表见 `fix-v2.md`。

仍未解决（见 `fix-v2.md` §4）：P-A `AGENTWORK_HOME` 泄漏未实现；link 未做真实 daemon 集成；循环 depends_on 未拒绝；retry 链无 dispatch 锚点；全仓 286 个 755 文件未清理；CLI/MCP 创建 goal 仍缺 execution_mode/depends_on；workflow_edge 仍作预留表；报告脚本仅 fixture 验证；UI 无 live daemon 截图。
