# agentwork 自迭代轮次演进报告 R3（R1 → R3）

- 生成：2026-11-11T09:06:38Z，由 scripts/rsi-round-report.sh 自动产出（RSI R3 可感知性改进机制的首次运行）
- 数据快照：daemon API `http://127.0.0.1:47813`（/rsi/sessions、/goals、runs、rounds、overview）+ git log（--all）
- 自包含声明：本报告不依赖平台登录即可理解；所有 ID/数字均可在 daemon API 与 git 历史复查

## ① 循环演进视图

> 阅读方式：每轮 = 一个 train 模式 goal，终态时平台把编排过程冻结成 workflow 图（agent / verify / review / consult / integrate 节点 + 显式依赖与时间推断边）。下表与图结构均从 daemon API 实拉，ID 可在平台复查；未登录平台的读者按文字即可理解全貌。同一轮出现多行 = 该轮存在多个 goal（重建/重开），主行取已出报告的终态 session，其余行保留以呈现前置失败（它们是②问题清单的证据）。

### 轮次总览

| 轮 | goal（标题） | 终态 | 周期 | workflow 规模 | 干了什么（一句话） |
|---|---|---|---|---|---|
| R1 | RSI R1：agentwork 自迭代首轮（基准→提案）（`3ec2bfc6`） | `ready` | 2026-11-10 → 2026-11-10 | 3 节点 / 3 边 | 建基线：bench 测出 build/test 基准（发现 1 失败包），optimizer 产出 3 条只读提案（P1 测试并行化 / P2 CDN 守卫 … |
| R2 | RSI R2：实施已批准提案（P1+P2+P3）（`25da0cc6`） | `ready` | 2026-11-10 → 2026-11-11 | 5 节点 / 5 边 | 实施已批准的 P1+P2+P3（4 commit）+ bench 复测全绿（14 包 0 FAIL，test 中位 11.559s），交付合入 agentwo… |
| R3 | RSI R3：可感知性改进（演进报告机制）（`bd99b9a9`） | `waiting` | 2026-11-11 → 进行中 | 7 节点 / 13 边 | 可感知性改进：builder 实现本报告生成器并写入协作约定；optimizer 产出 R2/R3 全量失败 run 问题清单与 3 条新提案（P-A 环境净… |
| R3 | RSI R3：可感知性改进（演进报告机制）（`357252be`） | `waiting` | 2026-11-11 → 进行中 | 13 节点 / 19 边 | 可感知性改进：builder 实现本报告生成器并写入协作约定；optimizer 产出 R2/R3 全量失败 run 问题清单与 3 条新提案（P-A 环境净… |

### R1 图结构（session `371a5055`，goal `3ec2bfc6`，主 session）

- **图规模**：3 节点 / 3 边；goal→子任务派发边 2 条、显式依赖边（depends_on）1 条
- **结构指标**：墙钟 2h55m，关键路径 45.6s，工作总量 45.6s，并行收益 1.00×，排队等待 330ms，串行损失 0s，重试率（Σattempts÷节点）3.50
- **节点明细**（依赖即派发顺序；集成节点代表交付合入）：
  - `goal` **[goal]** RSI R1：agentwork 自迭代首轮（基准→提案） — 终态 `failed`，耗时 0s
  - `sg:b757e167e34` **[agent]** sg-B：构建/测试基准测量（bench） — 终态 `failed`，耗时 45.6s，重试 5 次
  - `sg:751a53ebdf0` **[agent]** sg-O：R1 优化提案（只读）（optimizer） — 终态 `running`，耗时 0s
- **机器发现（findings）**：
  - [warn] 重试放大：平均每节点 3.5 次 run（score 70，retry_amplification）—— 2 个子任务共 7 次执行。重试通常意味着失败模式重复（环境/凭据/简报），重试本身不修复原因。

### R2 图结构（session `f31c3a6c`，goal `25da0cc6`，主 session）

- **图规模**：5 节点 / 5 边；goal→子任务派发边 2 条、显式依赖边（depends_on）1 条、集成边 2 条
- **结构指标**：墙钟 46m50s，关键路径 41m37s，工作总量 41m37s，并行收益 1.00×，排队等待 36m37s，串行损失 10m13s，重试率（Σattempts÷节点）2.50
- **节点明细**（依赖即派发顺序；集成节点代表交付合入）：
  - `goal` **[goal]** RSI R2：实施已批准提案（P1+P2+P3） — 终态 `done`，耗时 0s
  - `sg:23d061daa9f` **[agent]** sg-IMPL：实施已批准提案 P1+P2+P3（builder） — 终态 `verified`，耗时 31m24s，重试 3 次
  - `sg:b8584aabcfb` **[agent]** sg-REBENCH：实施后基准复测（bench） — 终态 `verified`，耗时 10m13s
  - `chg:11cd045f3e` **[integrate]** 集成 — 终态 `integrated`，耗时 0s
  - `chg:1dde9252bc` **[integrate]** 集成 — 终态 `integrated`，耗时 0s
- **机器发现（findings）**：
  - [warn] 重试放大：平均每节点 2.5 次 run（score 50，retry_amplification）—— 2 个子任务共 5 次执行。重试通常意味着失败模式重复（环境/凭据/简报），重试本身不修复原因。

### R3 图结构（session `079dc56e`，goal `bd99b9a9`，主 session）

- **图规模**：7 节点 / 13 边；goal→子任务派发边 6 条、显式依赖边（depends_on）3 条、时间推断/其他边 4 条
- **结构指标**：墙钟 42m11s，关键路径 8m47s，工作总量 16m25s，并行收益 1.87×，排队等待 10m24s，串行损失 80ms，重试率（Σattempts÷节点）2.00
- **节点明细**（依赖即派发顺序；集成节点代表交付合入）：
  - `goal` **[goal]** RSI R3：可感知性改进（演进报告机制） — 终态 `cancelled`，耗时 0s
  - `sg:d1b7f848f0c` **[agent]** sg-IMPL：轮次演进报告生成器（builder） — 终态 `failed`，耗时 31ms，重试 2 次
  - `sg:7d094326186` **[agent]** sg-ANALYZE：R2 问题清单与新提案（optimizer） — 终态 `failed`，耗时 49ms，重试 2 次
  - `sg:47976318991` **[agent]** sg-REPORT：生成首份正式演进报告（builder） — 终态 `cancelled`，耗时 0s
  - `sg:93ebd95d3cb` **[agent]** sg-REBENCH：R3 基准复测（bench） — 终态 `cancelled`，耗时 0s
  - `sg:18e22480125` **[agent]** sg-IMPL（重派）：轮次演进报告生成器（builder） — 终态 `cancelled`，耗时 8m47s，重试 1 次
  - `sg:2234fefdd37` **[agent]** sg-REBENCH（重派）：R3 基准复测（bench） — 终态 `cancelled`，耗时 7m37s，重试 1 次

### R3 图结构（session `4f5193cc`，goal `357252be`，前置/重建 session）

- **图规模**：13 节点 / 19 边；goal→子任务派发边 7 条、显式依赖边（depends_on）3 条、集成边 3 条、时间推断/其他边 6 条
- **结构指标**：墙钟 1h34m，关键路径 2h9m，工作总量 1h47m，并行收益 0.83×，排队等待 1.2s，串行损失 73m54s，重试率（Σattempts÷节点）1.57
- **节点明细**（依赖即派发顺序；集成节点代表交付合入）：
  - `goal` **[goal]** RSI R3：可感知性改进（演进报告机制） — 终态 `active`，耗时 0s
  - `sg:0e9ad6d5401` **[agent]** sg-IMPL：轮次演进报告生成器（builder） — 终态 `cancelled`，耗时 0s
  - `sg:c1e064464d4` **[agent]** sg-ANALYZE：R2 问题清单与新提案（optimizer） — 终态 `verified`，耗时 55m18s，重试 2 次
  - `sg:3edbe2b48d3` **[agent]** sg-REPORT：生成首份正式演进报告（builder） — 终态 `cancelled`，耗时 0s
  - `sg:52b00d7d4a4` **[agent]** sg-REBENCH：R3 基准复测（bench） — 终态 `cancelled`，耗时 0s
  - `sg:3991ce7f016` **[agent]** sg-IMPL-v2：轮次演进报告生成器（重派·执行角色）（builder） — 终态 `running`，耗时 25m13s，重试 2 次
  - `sg:b13a558163d` **[agent]** sg-REBENCH-v2：R3 基准复测（冷缓存口径·无依赖并行）（bench） — 终态 `verified`，耗时 18m35s
  - `sg:49c2619baec` **[agent]** sg-IMPL-v3：轮次演进报告生成器（重派·复用 sg-REBENCH-v2 已合入基…（builder） — 终态 `running`，耗时 0s
  - `consult:71670a` **[consult]** 咨询 — 终态 `completed`，耗时 8m6s
  - `consult:f3e50b` **[consult]** 咨询 — 终态 `running`，耗时 0s
  - `chg:2c5f00af09` **[integrate]** 集成 — 终态 `integrated`，耗时 0s
  - `chg:12e9a4dd63` **[integrate]** 集成 — 终态 `integrated`，耗时 0s
  - `chg:905861103f` **[integrate]** 集成 — 终态 `conflict`，耗时 0s

### 演进主线（R1 → R3）

1. **R1（基准→提案）**：首轮在新集成基线 `0bf354e` 上建立「同一基准任务每轮复跑」的度量惯例。bench 报告（benchmark-report.md）暴露 1 个失败包（internal/service CDN 缺码守卫）与 12 条 `Model unavailable` 失败 run；optimizer 据此产出 3 条提案（proposals.md，全部【已批准】）。结构上单通道（speedup 1.00×），sg-O 被 depends_on(sg-B) 整段阻塞——这条「假依赖」本身成为 R2 分析对象。
2. **R2（实施已批准提案）**：P1/P2/P3 落地（4 commit，每条独立 commit 便于单独 revert），bench 复测全绿：失败包 1→0、test 中位 11.559s（-50% 口径见第④节注记）、internal/daemon 包 -23%（P1 并行化）。同期暴露新失败模式：验证子进程继承 `AGENTWORK_HOME`，4 条 run 验证门三连败——改进生效的同时，下一类问题浮出水面（详见②③）。
3. **R3（可感知性改进，本报告）**：针对 R2 收尾暴露的五个可感知性缺口（过程不可感知 / 问题无呈现处 / 下轮任务未给到 / 无演进视图 / 改进不可见），本轮把「演进报告」变成机制：生成器入库（scripts/），「每轮收尾必须产出报告」写入 AGENTS.md 协作约定，optimizer 的全量失败 run 清单（analysis-r3.md）成为报告②⑤节的数据源。本文件即机制首次运行产物（R1→R3 全量回溯版）。

## ② 过程暴露问题清单

> 口径：**全部失败/异常 run 逐条列出**（数据源 `/goals/{id}/runs`，status ∈ failed/cancelled），不是指标汇总——每个失败都该有个去处。根因分类：环境 / 代码 / 流程。同一轮如有多个 goal（重建/重开），run 合并去重后一并呈现。

### R1（31 条 run，其中失败/异常 18 条）

| # | run id | 归属 | attempt | 状态 | 耗时 | 错误摘要 | 根因分类 |
|---|--------|------|---------|------|------|----------|----------|
| 1 | `8318c5cc` | bench | 1 | failed | 15.9s | 模型不可用：prompt: acp: session/prompt: Internal error: Model unavailable: opencode/muse-ember-1.0 | 环境 |
| 2 | `634c78a8` | bench | 2 | failed | 5.4s | 模型不可用：prompt: acp: session/prompt: Internal error: Model unavailable: opencode/muse-ember-1.0 | 环境 |
| 3 | `9cd850bf` | bench | 3 | failed | 5.5s | 模型不可用：prompt: acp: session/prompt: Internal error: Model unavailable: opencode/muse-ember-1.0 | 环境 |
| 4 | `899a791b` | optimizer | 1 | failed | 5.4s | 模型不可用：prompt: acp: session/prompt: Internal error: Model unavailable: opencode/muse-ember-1.0 | 环境 |
| 5 | `e65c9c3c` | bench | 1 | failed | 8.0s | 模型不可用：prompt: acp: session/prompt: Internal error: Model unavailable: opencode/muse-ember-1.0 | 环境 |
| 6 | `c9d79653` | bench | 2 | failed | 5.4s | 模型不可用：prompt: acp: session/prompt: Internal error: Model unavailable: opencode/muse-ember-1.0 | 环境 |
| 7 | `9922892c` | bench | 3 | failed | 5.4s | 模型不可用：prompt: acp: session/prompt: Internal error: Model unavailable: opencode/muse-ember-1.0 | 环境 |
| 8 | `75415878` | optimizer | 2 | failed | 5.7s | 模型不可用：prompt: acp: session/prompt: Internal error: Model unavailable: opencode/muse-ember-1.0 | 环境 |
| 9 | `ceffb017` | optimizer | 3 | failed | 5.4s | 模型不可用：prompt: acp: session/prompt: Internal error: Model unavailable: opencode/muse-ember-1.0 | 环境 |
| 10 | `4716f2f2` | optimizer | 1 | failed | 5.8s | 模型不可用：prompt: acp: session/prompt: Internal error: Model unavailable: opencode/muse-ember-1.0 | 环境 |
| 11 | `f97a5701` | optimizer | 2 | failed | 7.9s | 模型不可用：prompt: acp: session/prompt: Internal error: Model unavailable: opencode/muse-ember-1.0 | 环境 |
| 12 | `e068d49d` | optimizer | 3 | failed | 5.8s | 模型不可用：prompt: acp: session/prompt: Internal error: Model unavailable: opencode/muse-ember-1.0 | 环境 |
| 13 | `75dbf852` | optimizer | 1 | failed | 6ms | 机器离线/连接：machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| 14 | `3dec4083` | optimizer | 2 | failed | 35.8s | git/fetch/worktree：git: fetch: signal: killed: | 环境/流程 |
| 15 | `32612975` | optimizer | 3 | cancelled | 13.3s | 平台取消/停止：cancelled by platform | 流程 |
| 16 | `deaee148` | optimizer | 1 | failed | 6.6s | git/fetch/worktree：git: worktree add: context canceled: | 环境/流程 |
| 17 | `0ae48700` | optimizer | 2 | cancelled | 49.3s | 平台取消/停止：cancelled by platform | 流程 |
| 18 | `e1ca8d3f` | optimizer | 1 | cancelled | 64.9s | 平台取消/停止：cancelled by platform | 流程 |

错误类别小计：模型不可用 ×12，机器离线/连接 ×1，git/fetch/worktree ×2，平台取消/停止 ×3。

### R2（8 条 run，其中失败/异常 5 条）

| # | run id | 归属 | attempt | 状态 | 耗时 | 错误摘要 | 根因分类 |
|---|--------|------|---------|------|------|----------|----------|
| 1 | `37969c7d` | optimizer | 1 | failed | 69.5s | 机器离线/连接：verification failed: $ go build ./... $ go test ./... -count=1 --- FAIL: TestPersistentGoalWorkdir (0.57s) gitwork_test… | 环境 |
| 2 | `a234f342` | builder | 1 | failed | 21m2s | 验证门失败(环境串扰)：verification failed: $ go build ./... $ go test ./... -count=1 --- FAIL: TestGitWorkdirAndTransferPush (0.84s) gitwork_… | 环境/代码交界（验证子进程环境泄漏） |
| 3 | `b77ba4c9` | optimizer | 2 | cancelled | 70m51s | （无错误摘要）： | 代码/流程 |
| 4 | `176d72f2` | builder | 2 | failed | 2m12s | 验证门失败(环境串扰)：verification failed: $ go build ./... $ go test ./... -count=1 --- FAIL: TestGitWorkdirAndTransferPush (0.91s) gitwork_… | 环境/代码交界（验证子进程环境泄漏） |
| 5 | `d08e376d` | builder | 3 | failed | 3m25s | 验证门失败(环境串扰)：verification failed: $ go build ./... $ go test ./... -count=1 --- FAIL: TestGitWorkdirAndTransferPush (1.04s) gitwork_… | 环境/代码交界（验证子进程环境泄漏） |

错误类别小计：机器离线/连接 ×1，验证门失败(环境串扰) ×3，（无错误摘要） ×1。

### R3（30 条 run，其中失败/异常 15 条）

| # | run id | 归属 | attempt | 状态 | 耗时 | 错误摘要 | 根因分类 |
|---|--------|------|---------|------|------|----------|----------|
| 1 | `7a9f0ad5` | optimizer | 1 | failed | 13ms | 机器离线/连接：machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| 2 | `941d8a6e` | builder | 1 | failed | 9ms | 机器离线/连接：machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| 3 | `8052fa40` | optimizer | 1 | failed | 31ms | 机器离线/连接：machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| 4 | `d2d5065c` | optimizer | 2 | failed | 8ms | 机器离线/连接：machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| 5 | `31e5666d` | builder | 2 | failed | 10ms | 机器离线/连接：machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| 6 | `2b88eab9` | builder | 3 | failed | 10ms | 机器离线/连接：machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| 7 | `812174d8` | optimizer | 3 | failed | 9ms | 机器离线/连接：machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| 8 | `f4c76ca5` | optimizer | 2 | failed | 35m44s | 验证门失败(环境串扰)：verification failed: $ unset AGENTWORK_HOME; go build ./... sh: line 1: go: command not found [verify failed (exit 127)] | 环境/代码交界（验证子进程环境泄漏） |
| 9 | `a32bea54` | builder | 1 | cancelled | 8m47s | （无错误摘要）： | 代码/流程 |
| 10 | `e1121ff1` | bench | 1 | cancelled | 7m37s | （无错误摘要）： | 代码/流程 |
| 11 | `3a46ce6d` | optimizer | 3 | cancelled | 4m55s | （无错误摘要）： | 代码/流程 |
| 12 | `642a21d4` | optimizer | 1 | failed | 13m47s | 验证门失败(环境串扰)：verification failed: $ unset AGENTWORK_HOME; go build ./... sh: line 1: go: command not found [verify failed (exit 127)] | 环境/代码交界（验证子进程环境泄漏） |
| 13 | `e83e70cf` | optimizer | 1 | failed | 17m18s | 验证门失败(环境串扰)：verification failed: $ unset AGENTWORK_HOME; go build ./... sh: line 1: go: command not found [verify failed (exit 127)] | 环境/代码交界（验证子进程环境泄漏） |
| 14 | `f1182ff5` | optimizer | 2 | failed | 4m1s | 验证门失败(环境串扰)：verification failed: $ unset AGENTWORK_HOME; go build ./... sh: line 1: go: command not found [verify failed (exit 127)] | 环境/代码交界（验证子进程环境泄漏） |
| 15 | `9eb08fde` | builder | 1 | failed | 4m21s | git/fetch/worktree：commit/push: push feat-357252be-sg-3991ce7f: exit status 1: To github.com:Sinkear/agentwork.git ! [rejected] feat-35725… | 环境/流程 |

错误类别小计：机器离线/连接 ×7，验证门失败(环境串扰) ×4，（无错误摘要） ×3，git/fetch/worktree ×1。

### 上一轮遗留问题状态（滚动跟踪）

**R1 → R2 遗留**：
- 12 条 `Model unavailable` 失败 run（确定性模型配置错误）—— **已解决**：网关配置 + opencode 2.2.5 升级；平台侧 P3（不可重试快速失败）落地后该类错误不再烧重试预算。R2 零复现。
- CDN 缺码守卫致 1 包 FAIL —— **已解决**：P2 将畸形响应/缺码从 `Fatalf` 改为 `Skipf`（含 `AGENTWORK_SKIP_CDN_DRIFT` 环境开关），R2 起 `go test ./...` 全绿。
- sg-O 被 depends_on(sg-B) 阻塞的「假依赖」—— **部分处理**：R2 实测 queue_wait 仍占墙钟 78%（2.20m/2.81m），分析前置的派发结构调整留待后续轮次提案。

**R2 → R3 遗留（R3 收尾时点状态）**：
- **验证门三连败（AGENTWORK_HOME 泄漏）——待正式修复（→ 提案 P-A）**：`runVerifiedCmd`（internal/daemon/verify.go:171-186）不裁剪环境，子进程继承 `AGENTWORK_HOME=/root/.agentwork`，`t.Setenv("HOME")` 的测试隔离被 `paths.Home()` 的优先级击穿，共享 runsRoot 里的陈旧 clone 让 gitwork/deliver/digest 测试假红。当前以 domain checks 的 `unset AGENTWORK_HOME;` 前缀绕过（已生效，R2 run b319cffb 通过）。
- **rsi_round 轮次编号不递增 ——待修（→ 提案 P-B）**：两轮 `round_no` 均为 1（编号按 session 而非 domain 自增），`/rsi/overview` 趋势无法区分轮次，delta_json 恒为空。
- **bench 基准口径漂移 ——约定层修复中（→ 提案 P-C）**：R1 温单跑 vs R2 温中位的「-50%」口径混杂；R3 sg-REBENCH 已按冷缓存口径派发。
- **SSH 私钥权限致 deliver 失败一次 ——已解决**：容器重启重置权限（0750），修正后 11:09Z 重试合入成功；自动重试机制列为下一轮候选。
- **双 `agentwork connect` 互抢 peer ——已解决一次、土壤仍在**：R1 末人工合并为单实例；R3 分析时进程表再现 5+ 并存 connect 进程（活体复现），收敛方案列为下一轮候选。
- **owner 运行卡顿 71 分钟无看护 ——待治理（候选任务 5）**：R2 run b77ba4c9 人工停止；watchdog 覆盖范围待扩展。

## ③ 改进点（每条：问题依据 → commit → diff → 效果数字）

> commit 链接基于本仓库远程（`git@github.com:Sinkear/agentwork.git`，网页端把主机换为 github.com/Sinkear/agentwork）；效果数字均来自已提交的基准/实施报告原文，口径差异在第④节显式标注。R2 的三条提案各列两个 commit：原实施（agentwork-int 交付）与新基线 agentwork-int-v2 的语义化重放（历史重建，内容一致）。

### 改进点 1：P1 测试并行化（R2 提案 1）

- **【问题依据】** R1 基准显示 `internal/daemon` 包测试 8.00s 独占鳌头且 70 个顶层测试全部串行——互不依赖的测试没有理由排队（proposals.md 提案 1，已批准）。
- **【commit】** [`3cf2116`](https://github.com/Sinkear/agentwork/commit/3cf2116)（原实施） · [`e08cb72`](https://github.com/Sinkear/agentwork/commit/e08cb72)（新基线重放） — test(P1): 并行化 internal/daemon 独立顶层测试（提案1【已批准】）
- **【diff 摘要】**

  ```
  internal/daemon/deliver_test.go                |  0
  internal/daemon/digest_test.go                 |  0
  internal/daemon/domain_git_check_test.go       |  0
  internal/daemon/gate_test.go                   |  2 ++
  internal/daemon/handoff_cancel_test.go         |  8 +++++++
  internal/daemon/intake_test.go                 | 30 ++++++++++++++++++++++++++
  16 files changed, 70 insertions(+)
  ```

- **【效果数字（before → after）】** internal/daemon 包 8.00s → 5.911s（约 **-23%**，R2 基准报告第 4 节）；实施时三连跑 wall 5.30/5.73/4.99s（实施前基线 8.17s，约 -35%，impl-notes.md）。R3 冷口径复测 5.855s（中位），收益保持（benchmark-report-r3.md 第 4 节）。
- **状态**：已生效（R2 复测确认，R3 复测保持）。`t.Setenv` 的三个文件按提案保持串行。

### 改进点 2：P2 CDN 漂移守卫 fail→skip（R2 提案 2）

- **【问题依据】** R1 唯一失败包 internal/service：CDN 返回合法 JSON 但缺 6 码时守卫 `t.Fatalf`，导致全量测试永久红、掩盖真实回归（proposals.md 提案 2，已批准）。
- **【commit】** [`8ca9957`](https://github.com/Sinkear/agentwork/commit/8ca9957)（原实施） · [`e2bda1d`](https://github.com/Sinkear/agentwork/commit/e2bda1d)（新基线重放） — test(P2): CDN 漂移守卫对畸形响应 fail 改 skip（提案2【已批准】）
- **【diff 摘要】**

  ```
  internal/service/codes_test.go | 15 +++++++++++++--
  internal/service/goal.go       | 38 +++++++++++++++++++++++++++++++++++++-
  2 files changed, 50 insertions(+), 3 deletions(-)
  ```

- **【效果数字（before → after）】** 失败包 **1 → 0**：`go test ./... -count=1` 自 R2 起三连跑全绿（14 包 ok / 0 FAIL）；R1 的 3 条失败用例全部消除（R2 基准报告第 4 节）。
- **状态**：已生效。缺码漂移改 skip 为提案风险条款已接受项，skip 时输出完整缺码清单保可诊断性。

### 改进点 3：P3 不可重试错误快速失败（R2 提案 3）

- **【问题依据】** R1 12/14 条失败 run 是同一确定性错误 `Model unavailable`，却各自烧满 3 次重试预算（约 27s×N 空转 + backoff 唤醒）——重试不修复配置类原因（proposals.md 提案 3，已批准）。
- **【commit】** [`18a8c3c`](https://github.com/Sinkear/agentwork/commit/18a8c3c)（原实施） · [`e2bda1d`](https://github.com/Sinkear/agentwork/commit/e2bda1d)（新基线重放） — feat(P3): 失败重试引入不可重试错误快速失败（提案3【已批准】）
- **【diff 摘要】**

  ```
  internal/daemon/daemon.go |  5 ++++-
  internal/service/goal.go  | 46 ++++++++++++++++++++++++++++++++++++++++++----
  2 files changed, 46 insertions(+), 5 deletions(-)
  ```

- **【效果数字（before → after）】** 确定性失败不再烧重试预算：R2 起 `Model unavailable` 类失败 **零复现**（R2 全部 8 条 run 中 0 条该类错误，R1 为 12 条）；`nonRetryableMarkers` 白名单起步只收 `Model unavailable`。
- **状态**：已生效。白名单机制可扩展（R3 已暴露「machine offline」应纳入派发层判定，见候选任务）。

## ④ 跨轮指标对比

> 数据源：`rsi_round` 账本 / session metrics（daemon API 实拉）。⚠️ 口径注记：`round_no` 在本报告生成时按 session 自增（历史两轮均记 1，见问题清单 #13 与提案 P-B），下表「轮」列以 goal 标题中的轮次号为准；耗时类指标为平台冻结口径，与 bench 冷/温缓存口径（第③节）分属两把尺子，不可互比。R1/R2 为终态冻结值；R3 进行中，取实时重建 session 的图指标（交付后由 sg-REPORT 重跑生成器刷新为冻结值）。

| 指标 | R1 | R2 | R3 |
|---|---|---|---|
| goal 终态 | `ready`（`3ec2bfc6`） | `ready`（`25da0cc6`） | `waiting`（`bd99b9a9`） |
| workflow 节点数 | 3 | 5 | 7 |
| workflow 边数 | 3 | 5 | 13 |
| 关键路径 critical_path | 45.6s | 41m37s | 8m47s |
| 墙钟 wall | 2h55m | 46m50s | 42m11s |
| 工作总量 work_sum | 45.6s | 41m37s | 16m25s |
| 排队等待 queue_wait | 330ms | 36m37s | 10m24s |
| 并行收益 parallel_speedup | 1.00× | 1.00× | 1.87× |
| 串行损失 serialization_loss | 0s | 10m13s | 80ms |
| 重试率（Σattempts÷agent 节点） | 3.50 | 2.50 | 2.00 |
| 机器发现 findings | 1 | 1 | 0 |
| 首要发现 | 重试放大：平均每节点 3.5 次 run | 重试放大：平均每节点 2.5 次 run | — |
| 失败/异常 run 数 | 18 | 5 | 15 |

**平台侧趋势读法**：R1/R2 并行收益均为 1.00×（关键路径=工作总量，依赖声明驱动串行），R3 首次出现 >1 的并行度（重建 goal 无 depends_on 串行门）；失败模式从「模型不可用」（R1，12 条）迁移到「验证门环境串扰」（R2，4 条）再到「机器离线+派发层 127」（R3，本轮 15 条失败/异常 run）——修一类、暴露下一类，这正是本报告每轮滚动的原因。

**bench 基准口径对照**（build/test 秒数，来自各轮已提交 benchmark 报告；自 R3 起冷/温双列、跨轮对比只采冷列——提案 P-C）

| 轮 | 基线 commit | go build（冷列=采信） | go build（温列=参照） | go test（中位） | 失败包 | 口径注意 |
|---|---|---|---|---|---|---|
| R1 | `0bf354e`（复跑 v2 报告） | 未留冷态数据 | 3.9s（冷 40.6s 另测） | 单跑 11.3s | 1（internal/service） | 首跑含测试二进制编译（23.0s vs 复跑 14.5s）；按温列口径标注，不参与冷列对比 |
| R2 | `365e108` | 44.417s | 中位 4.387s | 三连中位 11.559s | **0** | 首次冷列记录；三连取中位；P1+P2 生效 |
| R3 | `e7c2572` | **41.285s**（-7.1% vs R2 冷列） | 中位 4.672s | 三连中位 13.173s（温） | 6 例 / 2 包 | **6 例失败全部为测试环境隔离缺陷**（AGENTWORK_HOME 泄漏 × 固定 ID 残留，非业务缺陷，见 benchmark-report-r3.md 第 6 节）；test +14% 主因 service 包 RSI 新代码负载与失败重试路径，修复隔离后口径才可与 R2 直接对比 |

## ⑤ 下一轮候选任务清单

> 引用自 optimizer 最新产出 `docs/rsi/analysis-r3.md`（commit `3d37689`，人工评审挑选后成为下一轮任务）。

1. **实施 P-A：验证子进程环境净化**（高优）——依据：R2 验证门三连败 4 条失败 run 同根因，且 R3 前置轮 unset 前缀又引入 PATH 三连败（#4b），验证子进程环境已是两轮最高频失败源；收益：同类验证门失败 4+3 → 0，domain checks 可简化；改动集中在 verify.go 一个函数 + 1 个回归测试，工作量最小。
2. **实施 P-B：rsi_round 按 domain 递增**（中优）——依据：两轮 round_no 均为 1、delta 恒空；收益：R4 报告【跨轮指标对比】首次有真序列；改动一条 SQL + trigger 判定 + prevMetrics 跨轮读取，随带回归验证 `/rsi/overview` trend。
3. **`/rsi` 端点口径修正**（低成本高一致性）——依据：AGENTS.md/playbook/goal 描述写的 `/rsi` 实测 404（R1 3baf73cff449 已附带发现，R3 再次踩到）；改法二选一：补文档为 `/rsi/sessions` 等真实端点，或 handler 加 1 行 `GET /rsi` 别名指向 overview。
4. **deliver 失败自动重试/退避**（中优）——依据：R2 人工批准后 deliver 因 SSH 权限失败一次（daemon.log 2026-11-11 10:56:53），直到 11:09:01 才重试成功，期间依赖人工推进；改法：deliverGoal 外层加一次带延迟的自动重试（可重试错误白名单复用 P3 的 isNonRetryable 思路，SSH 权限类修正后可重试）。
5. **owner/leader 运行卡顿看护**（中优）——依据：R2 owner run `b77ba4c9…` 运行 71 分钟（12:50:33→14:01:25）无产出被人工停止；改法：把 watchdog idle-notify/最大时长看护明确覆盖 owner 运行（目前主要命中 worker），超时先通知人工而非静默挂起。

**生成器自身给下一轮的候选**（本报告机制的可感知性缺口，供参考）：

- **round_no 依赖 goal 标题正则**——提案 P-B 落地（rsi_round 按 domain 递增）后，生成器应改读机读轮次号，去掉标题正则推断（本报告第④节口径注记可随之删除）。
- **③ 节改进点的 commit/diff 目前依赖静态映射**——改进点表随轮次增长，下一轮应把「问题依据→commit→效果」的映射固化到每轮 impl-notes 的结构化小节（如 YAML front-matter），生成器自动抽取，避免手维护。
- **bench 数字自动抽取**——第④节 bench 口径对照表目前按轮次静态维护；提案 P-C 的 `BENCHMARK-PROTOCOL.md` 固定字段落地后可从 `benchmark-report-r<N>.md` 自动抽取（本轮 R3 行已按 benchmark-report-r3.md 实填）。
- `/rsi` 端点文档口径：goal 描述写 `/rsi` 实测 404，实际端点为 `/rsi/sessions` 等 4 个（本脚本已按实拉口径实现）——建议文档层修正（候选任务 3）。

---
*报告生成：scripts/rsi-round-report.sh（R3 收尾产物，机器验证门前自检五节齐全）。 数据快照 2026-11-11T09:06:38Z；daemon API `http://127.0.0.1:47813`；git 基线分支 `agentwork-int-v2`，生成时 HEAD `e7c2572`（docs: R1/R2 自迭代交付物迁移至新基线（proposals/benchmark/impl-notes））。*
*协作约定：每轮 goal 收尾前必须运行本脚本生成 `docs/rsi/round-report-r<N>.md` 并随交付提交（见 AGENTS.md「RSI 轮次演进报告协作约定」节）。*
