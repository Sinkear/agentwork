# 单次 run 运行时长与重试/续跑机制 —— 代码级分析

- **分析范围**：一次 run 的生命周期（claim → dispatch → 机器侧执行 → finish）、失败重试、预算/超时、续跑与会话复用、长命令期间的可观测性。**不含**评论派发去重与 idle 看门狗策略本身（由另一位分析员负责；本文只在"缺少进度信号"这一层引用它们）。
- **证据基座**：本仓库 HEAD `9931759`（2026-11-13）；生产二进制按 **2026-11-03 构建 = `23a7ce1`**。
- **版本对照结论（先说）**：`git diff --stat 23a7ce1..HEAD` 显示运行时长相关文件**全部未变**——`internal/service/run.go`、`internal/daemon/machine_dispatch.go`、`internal/proto/acpbackend/*`、`internal/runtime/*`、`cmd/agentwork-cli/exec.go`、`cmd/agentwork-cli/gitwork.go` 与生产完全一致；`internal/daemon/daemon.go` 的 36 行改动是 schedule 类别，`internal/service/goal.go` 的 97 行是 HITL/HOOTL category 与 connection_id 字段（`reconcileOnRunEndOnce` 新增的是 `g.Category == "hootl"` 自动完成分支），**都不触碰重试/续跑/预算**。因此下文每条的"HEAD 是否已修"均为 **未修，生产与 HEAD 同代码**，不再逐条重复该论证。
- **方法**：只读代码 + 报告逐 run 求和。所有数字标了来源与假设；报告自身口径不一致处单列（见 §5）。

---

## 一、一页结论（按对"执行慢"的影响排序）

| 排序 | 结论 | 代价（本案例） | 严重度 |
|---|---|---|---|
| 1 | **平台没有"这个目标已经有人在跑"的运行时间概念。** Claim 的单飞守卫只保护 `owner`/`subgoal`（[run.go:686-691](internal/service/run.go)，2026-08-13 引入），`consult/review/verify` 被**明确设计成"只读快照，可以自由并行"**（[run.go:638-645](internal/service/run.go)）；但案例里被派发的 consult run 在改同一个共享部署目标（icode 运行时目录）。重叠的两个 run 各自冷启动、各自重做同一段工作。 | 重复 run ≈ **7.0–7.1 ks ≈ 37%**（其中 run#12 的 4562s 在 run#9 仍在跑时重做了 clean→configure→deploy→accept 尾段，占总量 **24%**） | **P0** |
| 2 | **平台没有"续跑"，只有"重跑"。** 预算到点 = 取消 run + goal 保持 active，且 reconcile 明确拒绝自动重生 cancelled owner（[goal.go:2257-2274](internal/service/goal.go)）；会话续用只认前序 `status='completed'`（[daemon.go:2164-2170](internal/daemon/daemon.go)），`consult`/`verify` 永不续会话。任何"再来一次"都是**全新进程 + 新 worktree checkout + 从评论区重新发现上下文**。报告里的"恢复调度续跑"不是平台能力（§3.1、§5）。 | 案例中唯一接近"续跑"的 run#12 = 4562s 全量重做；平台能自动续的只有 consult-resume（唤醒 requester，[goal.go:1108-1140](internal/service/goal.go)），等价于"再派一次" | **P0** |
| 3 | **长命令静默期零进度信号。** 平台侧只有真实 agent 事件（`run:event`）；run 的事件时钟 `machineLastEvent` 只在事件批次到达时刷新（[machine_dispatch.go:369](internal/daemon/machine_dispatch.go)）。机器虽然每 5s 发心跳（[connect.go:352](cmd/agentwork-cli/connect.go)），但只刷 `machine.last_seen_at`，**不刷 run**。一次 30 分钟无输出的 `uv sync`/构建在平台视角与"卡死"完全等价，2 分钟后必然写 idle 评论（[machine_dispatch.go:640-650](internal/daemon/machine_dispatch.go)），案例 4 次误报，人和协调 agent 据此重派 → 直接催化 #1 的 4562s。 | 直接后果 ≥ **4562s**（占总量 24%）；间接的"人工盯盘/协调评论"成本无法用 run 计时衡量 | **P0** |
| 4 | **基础设施故障与任务失败共用同一个 3 次预算，且恢复后不自愈。** 3 次模型端点 404 失败把 attempt 烧到 `maxAttempts=3`（[goal.go:97](internal/service/goal.go)），goal 直接 `failed`（[goal.go:1443-1450](internal/service/goal.go)）；退避 30s/60s（[goal.go:102-108](internal/service/goal.go)）与报告中 15:52:07→15:52:38→15:53:40 的时间戳**逐秒吻合**。有失败卡片（[notify.go:454-482](internal/notify/notify.go) → ❌ 失败），但内容不可行动；端点恢复（约 25 分钟）后没有任何"再试一次"的路径。 | run 计算量只有 **11s**，但 wall clock 空档 **24m28s**（15:53:43→16:18:11）；卡片的 `summary` 被 `truncate(...,200)` 截断，且以"agent exited without responding"通用文案开头 | **P1** |
| 5 | **超时/预算存在两个权威，机器侧没有 deadline。** `reapRunawayRuns` 用 `domain.max_run_duration`（默认 7200s）+ 5m grace（[daemon.go:2582-2611](internal/daemon/daemon.go)、[daemon.go:2659-2673](internal/daemon/daemon.go)）；watchdog 却硬编码 **2h** 并注释成"total duration > 2h"（[machine_dispatch.go:661-663](internal/daemon/machine_dispatch.go)）；执行器 `ctx` 只是 `context.WithCancel`，**没有任何 per-run 超时**（[exec.go:106](cmd/agentwork-cli/exec.go)）。取消只能随机器 30s 轮询下发（[connect.go:280-317](cmd/agentwork-cli/connect.go)），最长 30s + 2min grace 才落终态。 | 本案例未触发（run#9 6236s < 7200s），但它是**配置无效**型隐患：domain 调到 <2h 时 watchdog 仍按 2h 杀；调大后 watchdog 提前杀 | **P1** |
| 6 | **单 run 固定启动成本确实存在，但不是主因。** 案例给出实测上界：run#1–#3 的 **2/4/5s** 覆盖了"worktree 准备 + AGENTS.md 写入 + skills 复制 + CLI 冷启动 + ACP initialize/newSession + 模型 404 往返"全链。可省的只有几处机械动作：每个 run 都 `git fetch origin`（[gitwork.go:58](cmd/agentwork-cli/gitwork.go)）、每次全量复制 skills（[exec.go:228](cmd/agentwork-cli/exec.go)）、无进程/会话复用（[exec.go:234-260](cmd/agentwork-cli/exec.go)）、只读角色每 run 全新 checkout（[gitwork.go:122-131](cmd/agentwork-cli/gitwork.go)）。 | 23 run × 2–5s ≈ 1–2 min（<1% of 19200s）。但 `started_at` 在 claim 时就打点（[run.go:671](internal/service/run.go)），冷启动被算进"执行时长"，会干扰观感与 SLO 口径 | **P2** |

---

## 二、量化：19200s 怎么拆的

### 2.1 方法

1. 基数用报告的**逐 run 明细**（22 条），逐条求和 = **18 966s**；报告总账 19 200s，残差 **234s**。报告 run id 从 #7 直接跳到 #9，**run#8 从未出现在明细里**——残差 234s 即该 run（时间窗必然在 16:20:21（#7 起）与 16:24:08（#9 起）之间，属 Stage1 的又一条平台 verify/consult）。22 条 + #8 = 23 次，与报告"23 次 run"吻合。
2. 每 run 归入四类：
   - **真实工作（N）**：产出交付物或该阶段不可省的推进。
   - **重复/并发（D）**：同一交付物被两个 run 各做一遍。**判据只用重叠**：重叠窗口内的工作"至少有一份是多余的"——不需要判断谁是正版（这正是本案例难以判断的地方，见 §2.3 敏感性）。
   - **平台元 run（M）**：平台注入的 verify/consult/Reopened 治理 run。
   - **重试（R）**：同一次失败后的 attempt 2/3。
3. 计时口径假设：一次 run 一个 `started_at → finished_at` 区间，**窗口重叠处重复计时**（与平台 `total_run_compute` 的口径一致；这正是它 5.3h 而 wall clock 41h50m 的原因）。若平台实际按"并发只计一次"统计，则报告 19200s 本身就不可比——但报告 §1 明说"含失败重试与重复调度"，即承认重复计时。

### 2.2 分账

| # | 阶段 | Agent | 起–止 | 时长 | 归类 |
|---|---|---|---|---|---|
| 1 | S0 | AI SHELL | 15:52:02–15:52:07 | 5s | R（infra 404） |
| 2 | S0 | AI SHELL | 15:52:38–15:52:39 | 2s | R |
| 3 | S0 | AI SHELL | 15:53:40–15:53:43 | 4s | R |
| — | S0 | — | 15:53:43–16:18:11 | **24m28s（wall，不计入 run 计算量）** | 端点恢复 + 人处理 |
| 4 | S0 | AI SHELL | 16:18:11–16:19:21 | 71s | N |
| 5 | S1 | Checker | 16:19:10–16:20:20 | 71s | N |
| 6 | S1 | Checker | 16:19:15–16:20:26 | 71s | **D**（与 #5 重叠 65s） |
| 7 | S1 | AI SHELL | 16:20:21–16:25:20 | 299s | M（verify） |
| 8 | S1 | ? | 未列（残差） | ~234s | 按 M 估计 |
| 9 | S2 | Upgrader | 16:24:08–18:08:04 | **6236s** | N（主升级，最大单项） |
| 10 | S2 | Upgrader | 16:24:17–16:52:11 | 1674s | **D**（全程落在 #9 内） |
| 11 | S2 | AI SHELL | 16:52:11–16:53:28 | 76s | M（consult-resume，触发点 = #10 终态） |
| 12 | S2 | Upgrader | 16:53:23–18:09:25 | **4562s** | **D**（与 #9 重叠 4481s，98%） |
| 13 | S3 | AI SHELL | 18:08:05–18:19:18 | 673s | M（verify） |
| 14 | S3 | AI SHELL | 18:09:25–18:12:47 | 202s | M（consult，触发点 = #12 终态 18:09:25） |
| 15 | S3 | Verifier | 18:12:44–18:14:59 | 135s | N |
| 16 | S4 | AI SHELL | 18:14:59–18:18:06 | 187s | M（consult） |
| 17 | S4 | Packager | 18:17:54–18:31:09 | 795s | **D**（与 #18 重叠 787s） |
| 18 | S4 | Packager | 18:18:02–18:40:51 | 1369s | N（主打包 + 元数据对齐） |
| 19 | S4 | AI SHELL | 18:31:09–18:41:28 | 619s | M（verify） |
| 20 | S4 | AI SHELL | 18:39:31–18:43:52 | 261s | M（Reopened + Collaboration Warning） |
| 21 | S5 | AI SHELL | 08:04:29–08:09:34 | 305s | N |
| 22 | S5 | AI SHELL | 08:26:47–08:47:34 | 1246s | N（重打包 1.0.4） |
| 23 | S5 | AI SHELL | 09:41:13–09:42:55 | 103s | N |

**求和**

| 分类 | 秒 | 占比 | 备注 |
|---|---|---|---|
| 真实工作 | 9 536 | 49.7% | #4 71 + #5 71 + #9 6236 + #15 135 + #18 1369 + #21 305 + #22 1246 + #23 103 |
| 重复/并发 | **7 102** | **37.0%** | #6 71 + #10 1674 + #12 4562 + #17 795（按"较小 run 全额"计） |
| 平台元 run | 2 317 (+234) | 12.1%（13.3%） | #7 299 + #11 76 + #13 673 + #14 202 + #16 187 + #19 619 + #20 261（+ #8 234） |
| 重试（infra） | 11 | 0.06% | #1–#3 |
| **合计** | **19 200** | 100% | 与报告总账一致 |

**按重叠窗口计**（同一份工作只算一次）：重复 = 65 + 1674 + 4481 + 787 = **7 007s**（36.5%）。两种口径差 95s，结论不变：**重复 run 是最大的单项浪费，量级 7.0–7.1 ks**。

### 2.3 三条必须说清的事

- **#9 vs #12 谁是"正版"不需要判定，也不能判定。** 两者都在 18:07:30 / 18:09:19 各自发出了 PASS 交接（评论#12/#13），说明同一验收被跑了两遍；重叠窗口里的 4481s 至少有一份是多余的。若读者坚持"#12 是救火、#9 已经卡住"，则重复只应计 #10+#6+#17 = 2 540s（13%），真实工作升至 14 100s（73%）——这是本报告唯一有实质分歧的归类，敏感性已在表中给出，我的默认取"重复"，理由是报告自己记录了 #9 在 17:06 / 17:12 / 18:07 持续产出评论（即它在推进，不是卡死）。
- **run#8 的 234s 是残差，不是实测。** 唯一用途是让总账闭合；归类（M）是估计，误差 ±234s（1.2%），不影响排序。
- **报告 §6 的"AI SHELL 约 4.7h"与其自身明细不自洽**：AI SHELL 14 条明细相加只有 4 053s（1.13h）；"verify/consult 5 次约 21min"按明细是 1 980s（33min）；AI SHELL 明细是 14 次而 §6 写 13 次。本文一律以逐 run 明细为准，并在此标注差异，避免用错误的分母做归因。

---

## 三、必查项的直接回答

### 3.1 `daemon.go`：调度、重试、失败后行为、预算到点、续跑（对应报告 run#12）

- **run 是怎么被拉起来的**：`dispatchOnce`（[daemon.go:1115-1192](internal/daemon/daemon.go)）按 worker 空闲槽把 ready agents 交给 `runSvc.Claim`，claim 成功即 `status='running', started_at=now()`（[run.go:669-672](internal/service/run.go)），再 `w.queue <- q` 给 `runTask`（[daemon.go:1720](internal/daemon/daemon.go)）。`runTask` 在机器架构下只做三件事：查 run/goal/domain/agent/runtime 的大 SQL（[daemon.go:1729-1742](internal/daemon/daemon.go)）→ 组装 prompt（[daemon.go:1888](internal/daemon/daemon.go)）并 `UPDATE run SET prompt=?`（[daemon.go:1898](internal/daemon/daemon.go)）→ `dispatchToMachine`（[daemon.go:1941](internal/daemon/daemon.go)）。**没有机器绑定的 runtime 直接 `failRun`**（[daemon.go:1962](internal/daemon/daemon.go)）。
- **maxAttempts=3 与退避**：daemon 侧 `maxAttempts` 在 [daemon.go:126](internal/daemon/daemon.go) 只是声明，真正生效在 `internal/service/goal.go:97` 与 `:102-108`：`delay := 30 * time.Second << (attempt - 2)`，上限 5m（attempt2=30s、attempt3=60s）。失败由 `reconcileOnRunEndOnce` 的 `failed` 分支处理（[goal.go:1385-1450](internal/service/goal.go)）：`if rc.Attempt < maxAttempts && !isImportGoal` → 同 agent 新 run（attempt+1，`EnqueueExistingTx` 在事务内落地），并 `UPDATE run SET queued_at = now()+delay` 做退避；否则 `UPDATE goal SET status='failed'` + 丢弃 queued runs，并发 `goal:finished{status:'failed'}`。
  **与报告逐秒吻合**：run#1 结束 15:52:07 → run#2 起 15:52:38（+31s）；run#2 结束 15:52:39 → run#3 起 15:53:40（+61s）。
- **budget=7200 到点后是失败还是续跑？——两者都不是。** 两条独立的收割路径：
  - `reapRunawayRuns`（[daemon.go:2582-2654](internal/daemon/daemon.go)）：每分钟扫 `status='running'`，`budget := runBudgetSeconds()`（domain `max_run_duration`，默认 7200，[daemon.go:2659-2673](internal/daemon/daemon.go)），超 `budget + runawayGrace(5m)` 则 `UPDATE run SET status='cancelled', cancel_reason='runaway'`，发 `run:cancelled{reason_code:"runaway"}`，**goal 保持 active**（源码注释引"决策 2-6: the goal stays active, the human decides"，[daemon.go:2640](internal/daemon/daemon.go)）。
  - 机器 watchdog（[machine_dispatch.go:661-681](internal/daemon/machine_dispatch.go)）：硬编码 2h 后 `cancel_reason='timeout'` + `enqueueMachineCancel(reason:"max_run_duration")`，等 2min 无终态就本地 stamp cancelled。
  **之后没人续跑**：`reconcileGoalOnce` 的注意力派发有一道显式守卫——最近一条 owner run 是 `cancelled` 就不 spawn（[goal.go:2257-2274](internal/service/goal.go)，注释："auto-spawning would loop the stuck agent (决策 2-6: a timeout is the human's call)"）。唯一的人手续跑入口是 `POST /goals/{id}/continue` → `ContinueGoal`（[daemon.go:1026-1029](internal/daemon/daemon.go)），wakeNote 是"Pick up your worktree state where it left it — pull the comment feed with `agentwork work goal comments`"。
- **"恢复调度续跑"（报告 Stage2 run#12）对应哪段代码？——没有对应的调度机制。** 平台的自动唤醒只有一条：consult 闭包。`recordConsult` 记下 requester/guest（[comment.go:337-345](internal/service/comment.go)），guest 终态时唤醒 **requester**（这里是 AI SHELL），事务内 `EnqueueExistingTx(..., attempt=1, ...)`（[goal.go:1108-1140](internal/service/goal.go)，`:1131`）。run#11（16:52:11 起）正好是 run#10 的终态时刻触发的 requester 唤醒；run#12（16:53:23 起）是在这次唤起之后由协调者重新派发的新 guest run。**它不是"接着 #9 跑"，是"另起一个 #9 再做一遍"**：`consult` 角色既不续会话（[daemon.go:1909-1912](internal/daemon/daemon.go) 只给 owner/subgoal 传 `PriorSessionID`），执行器也不 resume（[exec.go:276-308](cmd/agentwork-cli/exec.go) 的条件里 `PriorWorkDir == workdir`，只读角色每 run 是新 workdir），只能靠评论区重放上下文（[team_context.go:100-110](internal/daemon/team_context.go) 明确让 agent 自己 `agentwork goal comments` 拉历史）。
- **续跑是否重放上下文/重建会话？** 重建。进程、ACP 会话、worktree 全部新建；历史靠两条路径：`session/load`（仅 owner/subgoal 且前序 run `completed` 且 workdir 一致，[daemon.go:2164-2170](internal/daemon/daemon.go)、[session.go:82-88](internal/proto/acpbackend/session.go)）+ agent 自己读评论。会话续用对 `cancelled` 前序 run **永久关闭**，注释给了理由：被 kill 的 turn 可能把 session 写坏（"poisoned"，[daemon.go:2153-2159](internal/daemon/daemon.go)），而执行器已有的 fresh-fallback（[exec.go:296](cmd/agentwork-cli/exec.go)、[exec.go:329-345](cmd/agentwork-cli/exec.go)）本来就能兜住。**这个理由成立，但"一刀切不看 cancelled"把合法的"预算切断后继续"也排除了**（见 RT-2）。

### 3.2 一次 run 的固定启动成本（Q2）：分解与可复用性

| 环节 | 代码 | 成本 | 可否复用 |
|---|---|---|---|
| machine pickup | `dispatchToMachine` 入待取队列 + `wakeMachinePoll`（[machine_dispatch.go:132-180](internal/daemon/machine_dispatch.go)） | 唤醒即时；若靠轮询最长 30s | 已最优 |
| prompt 组装 | `assemblePrompt`（[daemon.go:2908+](internal/daemon/daemon.go)）+ `buildRunProfile`（[team_context.go:118](internal/daemon/team_context.go)）+ `UPDATE run SET prompt` | ms 级，但每 run 重查 goal/domain policy/team/comments/squad playbook 并重拼整块 AGENTS.md | **可缓存**（按 goal+agent+role+policy 版本号），收益小 |
| 裸仓 fetch | `ensureBareRepo`：`runGit(ctx, repo, "fetch", "origin")`（[gitwork.go:58](cmd/agentwork-cli/gitwork.go)） | **每 run 一次网络 fetch**，随仓库大小/网络线性增长 | **可节流**（RT-6） |
| worktree | `ensureGitWorkdir`：`persistent := p.Role == "owner" \|\| p.Role == "subgoal"`（[gitwork.go:122-131](cmd/agentwork-cli/gitwork.go)） | owner/subgoal 跨 run 复用（`goalAgentWorkdir`）；consult/verify/packager **每 run 全新 checkout** | 部分可复用（设计使然） |
| AGENTS.md | `syncAgentProfile` + `p.RunProfile` 合并写入（[exec.go:198-216](cmd/agentwork-cli/exec.go)） | 每次重写，小文件 | 可跳过（内容比对） |
| skills 复制 | `copyAgentSkills`（[exec.go:228](cmd/agentwork-cli/exec.go)）；`removeStaleSkillCopies` | **每 run 全量复制**，scratch 域先 wipe | **可增量**（mtime/size 清单） |
| 进程 + ACP 会话 | `runtime.Open` → `acpbackend.OpenSession`，`defer sess.Close()`（[exec.go:234-260](cmd/agentwork-cli/exec.go)、[exec.go:312-320](cmd/agentwork-cli/exec.go)） | 每次全新进程、initialize、newSession；**进程/session 池已随 05f09aa 删除**（`git log --diff-filter=D -- internal/daemon/session_pool.go`） | 不可复用（当前架构） |
| 历史回放 | `LoadSession` → `session/load`（[session.go:82-88](internal/proto/acpbackend/session.go)） | 仅 owner/subgoal 续用时发生，回放整段历史，**可能是秒级到十几秒** | 可按"上次事件 id"增量 |
| 计时口径 | `started_at` 在 claim 打点（[run.go:671](internal/service/run.go)） | 以上全部被算进"run 执行时长" | 建议拆 `ready_at` |

**结论**：案例实测上界 2–5s（run#1–#3 全链），**固定成本不是 19200s 的原因**；真正可省的只有 fetch/skills 两项机械动作，且它们只有在"run 数量被放大 3–5 倍"时才显得重要——**先把重复 run 砍掉，再谈这些**。

### 3.3 长命令无输出时平台能看到什么（Q3，只讲"缺进度信号"这一层）

- 进度信号的**唯一来源**是 agent 自己的 ACP 事件：文本/思考/`tool_use`/`tool_result`，经 `eventForwarder`（[backend.go:168-231](internal/proto/acpbackend/backend.go)）→ 执行器 100ms/64 条批量上报（[exec.go:353-396](cmd/agentwork-cli/exec.go)）→ `IngestRunEvents` 落 `chat_message` 并广播 `run:event`（[machine_dispatch.go:332-386](internal/daemon/machine_dispatch.go)）。
- **静默期没有任何周期性信号**：没有 `run:heartbeat`/`run:progress` 主题（`internal/server/ws/hub.go:27` 的 run 事件清单里没有）；`machineLastEvent[runID]` 只在**收到事件批次**时刷新（[machine_dispatch.go:369](internal/daemon/machine_dispatch.go)）；机器 5s 心跳（[connect.go:352](cmd/agentwork-cli/connect.go)）只到 `machine.last_seen_at`，与 run 无关。
- 因此 agent 跑一个 30 分钟静默命令时，平台能看到的事实只有："机器在线（5s 心跳）+ 这个 run 已经 N 分钟没有新事件"。2 分钟后 watchdog 写一条 system 评论（[machine_dispatch.go:640-650](internal/daemon/machine_dispatch.go)）：`Run idle for 2m0s — possibly stuck or in rate-limit backoff. The platform will not auto-cancel. Stop it manually if needed, otherwise wait for recovery.` —— 文案里没有"在跑什么工具""已经跑了多久""上次输出在几分钟前"。
- ACP 里天然存在的进度信息被丢掉了：`OnPlan`/`OnUsageUpdate`/`OnSessionInfo` 全是空实现（[backend.go:300-305](internal/proto/acpbackend/backend.go)），token 用量、plan 步骤、可用命令都不落库。agent 自己的 todo plan 本来是最好的进度条。
- 对 idle 判定与体验的影响（不重复 watchdog 策略）：**平台的 liveness 信号粒度是"run 有没有事件"，而真实语义是"进程是否存活 + 是否在推进"**。这两种状态在静默期无法区分，于是看板上只能给出"可能卡死"的模糊结论，人和协调 agent 的最优反应就是重派——这正是 run#12 的成因。修法是补信号（RT-3），不是收紧 idle 策略。

### 3.4 模型端点 404 这类错误在哪里暴露（Q2 后半）

- 暴露点有三层，**都不分类**：
  1. ACP 层：`prompt` 失败 → `Result{StatusFailed, Output: AppendStderr("prompt: "+err.Error(), conn.Stderr)}`（[backend.go:120-127](internal/proto/acpbackend/backend.go)）。
  2. **最关键的坑**：模型调用失败时 CLI 仍可能对 `session/prompt` 回一个"成功"，所以平台靠 **zero-event guard** 判失败——`if !fwd.hasEvents() { ... "agent exited without responding (zero session/update notifications)" }`（[backend.go:139-147](internal/proto/acpbackend/backend.go)，注释原话："the LLM call failed but it still returned a success response"）。**案例中的 404 走的就是这条**：run 在 2–5s 内"快速失败"，但失败文案是通用句，真正的 `HTTP 404` 躺在 `stderr` 里（[proto.go:28-37](internal/proto/proto.go) 追加成 `"\nstderr: ..."`）。
  3. 平台层：`run.result_summary` 存这段文本 → `goal:finished{failed}` → 飞书卡片 `milestoneBody(title, truncate(summary, 200))`（[notify.go:454-482](internal/notify/notify.go)）。200 字截断后，卡片很可能只显示通用句。
- **"快速失败"有（2–5s），"明确告警"只算半个**：卡片会发（❌ 失败），但没有 failure class、没有"这是模型端点问题、不是任务问题"的判断、没有恢复后的自动重试、重试过程（`goal:retrying`）完全没有 IM 通知（[notify.go:102-126](internal/notify/notify.go) 的订阅清单里没有它，只有 WS 到 Web UI：`internal/server/ws/hub.go:21`）。运维视角 = "任务红了，原因看起来像 agent 没说话"。

---

## 四、逐项发现

> 每条格式：ID / 严重度 / 证据 / 量化 / HEAD / 修复方案（函数级 + 测试 + 风险）。HEAD 一律"未修"（见文首版本对照），下文只写证据锚点。

### RT-1（P0）重复/并发 run：平台的运行时间概念缺失

**证据**
- 设计假设写在 Claim 的注释里：`consult/review/verify runs are read-only snapshots and run freely in parallel`（[run.go:638-645](internal/service/run.go)）；守卫只覆盖 owner 与 subgoal：`AND NOT EXISTS (SELECT 1 FROM run r2 WHERE r2.status='running' AND ((r.role='owner' AND r2.role='owner' AND r2.goal_id != '' AND r2.goal_id=r.goal_id) OR (r.sub_goal_id != '' AND r2.sub_goal_id=r.sub_goal_id)))`（[run.go:686-691](internal/service/run.go)，2026-08-13 `8c906c1` 引入）。
- 派发侧的合并守卫按 `(goal, agent, role)` 合并（[run.go:enqueueTx](internal/service/run.go) 的 `run: coalesced` 日志，2026-08-15 `e588bd7` 引入，**生产二进制已含**）。案例仍出现两两并发，说明两条派发落在了合并键之外（不同 role / 不同 agent / 不同 sub_goal）；报告未记录 `run.role`、`run.agent_id`，**无法从报告唯一定因**——这一层归派发去重分析员，本文不展开。
- 与运行时相关的是后果：`consult`/`verify` 每 run 全新 worktree checkout（[gitwork.go:122-131](cmd/agentwork-cli/gitwork.go)）、不续会话（[daemon.go:1909-1912](internal/daemon/daemon.go)）、各自冷启动；而案例里它们改的是**同一份平台外共享目标**（icode 运行时目录），"只读快照"的假设被证伪——评论#10 记录"runtime 曾被清两次、uv 缓存锁竞争"（报告 17:06:52）。

**量化**：重叠口径 7 007s / 全额口径 7 102s（36.5%–37.0%）。拆分：#12 4 481–4 562s（24%）、#10 1 674s、#17 787–795s、#6 65–71s。至少一份多余（无需判定谁是正版）。

**修复方案**（两档，先做小的）
1. **止损（~1 天）**：给"会写共享目标的 run"一个运行时准入。最小实现：`domain` 增加策略列 `single_writer_per_agent`（或复用 `max_concurrent`=1 + role 白名单），在 `RunService.Claim` 的 `NOT EXISTS` 里增加一条：`AND NOT (r.role NOT IN ('owner','subgoal') AND <该 agent 的 policy 要求单写者> AND EXISTS (同 goal 同 agent 的 running run))`；被挡下的 run 不 claim，由 `reconcileOnRunEndOnce` 在 winner 终态后按原 attempt 重新入队（等价于"排队"而不是"丢弃"）。配套：`Claim` 里目前"挡下 = 不留痕"，需在 `dispatchOnce` 打一行 `run: deferred` 日志，否则用户只看到"没跑"。
2. **结构性（不建议本轮）**：让同一 `(agent, goal)` 的后续 run **attach 到在飞会话**，把新 prompt 排进 `session.Prompt` 的 `turnMu` 队列（[session.go:95-185](internal/proto/acpbackend/session.go) 本来就是为多轮设计的），执行器持有 `(goal,agent) → 会话` 注册表。这正是 `session.go:12-18` 注释承诺的"one process + one ACP session across turns"，在机器执行架构（05f09aa）里丢失了。风险：单写者/取消/崩溃恢复语义都要重做，属于 P1 大改造。

**测试**：`Claim` 单测（两个同 goal 同 agent 的 consult run → 第二个不被 claim，winner 结束后被 claim）；`enqueueTx` 合并键单测保持；daemon 集成：run:deferred 日志与最终执行次数 = 1。
**风险**：阻塞合法的"审查者与写者并行"（verify 与 owner 并行是设计特性）→ 必须按 role/domain 开关而不是一刀切；排队语义会让"派的活没动"看起来像平台丢单 → 必须有可见状态。

### RT-2（P0）没有续跑语义：取消即终态，重来即全量重做

**证据**
- 预算/超时取消后，goal 保持 active 但**禁止自动重生**：`reconcileGoalOnce` 里 `lastOwnerCancelled`（[goal.go:2257-2274](internal/service/goal.go)）+ 派发条件 `if attention != "" && !lastOwnerCancelled && newestSignal > lastOwnerSpawn`（[goal.go:2294](internal/service/goal.go)）。
- 会话续用只认 `status='completed'`：`SELECT session_id, workdir FROM run WHERE goal_id=? AND agent_id=? AND role='owner' AND status='completed' AND session_id != '' ORDER BY finished_at DESC LIMIT 1`（[daemon.go:2164-2170](internal/daemon/daemon.go)）；`consult`/`verify`/`review` 一律不传 `PriorSessionID`（[daemon.go:1909-1912](internal/daemon/daemon.go)）。
- 唯一的"自动续"是 consult 闭包，且它续的是 **requester**（[goal.go:1108-1140](internal/service/goal.go)，`:1131` `EnqueueExistingTx(..., 1, ...)`），不是被打断的执行者。
- 人工续跑入口 `ContinueGoal` 的 wakeNote 只有一句话（[daemon.go:1026-1029](internal/daemon/daemon.go)），不带断点、不带最后进度。

**量化**：案例里"续跑"= run#12 的 4 562s 全量重做（占总量 24%）；其中 4 481s 与仍在跑的 run#9 重叠。若有"断点续跑"，理论上只需重做 tail 中未完成的部分（不可从报告估算，需事件级日志）。

**修复方案**
1. **断点记录（小，先做）**：`RunService.Finish` / `processMachineRunCompletion` 里把"最后一条事件的 kind + tool 名 + 已运行秒数"写进新列 `run.progress_tail`（或复用 `result_summary` 前缀），取消路径同样写。这样 `ContinueGoal`/后续 wakeNote 能拼出"你上次停在 deploy，已跑 1h44m"。
2. **允许从取消态续会话（中）**：`priorSessionFor` 增加参数，取"最近一条任意终态 run"，但**只有当 `run.session_state='clean'` 时**才返回 session——新增列由执行器在 `RunFinishedParams` 上报（completed=clean；cancel 且收到 `session/cancel` ack=clean；其余=unknown）。保留执行器现有 fresh-fallback（[exec.go:329-345](cmd/agentwork-cli/exec.go)）兜底，注释里说的 poisoned session 风险就被限制在 unknown 分支。
3. **可选自动接续（中，默认关）**：domain 增加 `auto_resume_on_timeout`（默认 false）；开启时 `reconcileGoalOnce` 对 `cancel_reason IN ('timeout','runaway')` 的最近 owner run 允许**一次** attempt+1（`EnqueueOwnerRunTx`，wakeNote 带断点），并把该次接续记进 `activity_log` 供审计。默认关是为了不违背决策 2-6（"a timeout is the human's call"）。

**测试**：`priorSessionFor` 单测（cancelled+clean → 返回；cancelled+unknown → 空）；`reconcileGoalOnce` 单测（auto_resume 开/关、只接一次）；执行器集成（cancel 后同一 workdir 第二次 run 走 `session/load` 成功）。
**风险**：自动接续 → autoloop/烧钱（必须有 domain 开关 + 次数上限 + 可观测）；poisoned session 误判 → 保留 fallback 并把 `session_state` 的判定写成显式逻辑而非猜测。

### RT-3（P0/P1）长命令无进度信号：静默 = 卡死

**证据**（详见 §3.3）
- 只有 `run:event`；`machineLastEvent` 仅事件批次刷新（[machine_dispatch.go:369](internal/daemon/machine_dispatch.go)）；机器 5s 心跳与 run 无关（[connect.go:352](cmd/agentwork-cli/connect.go)）。
- idle 评论文案没有工具名/已运行时长（[machine_dispatch.go:640-650](internal/daemon/machine_dispatch.go)）。
- ACP 的 plan/usage/session_info 回调空实现（[backend.go:300-305](internal/proto/acpbackend/backend.go)）。

**量化**：4 次误报（16:33:10、17:01:25、18:23:05、18:27:57）分布在 #9 与 #18 两个最长的 run 上；其可归因的后果至少是 #12 的 4 562s（占 24%）。反事实：若 idle 评论带"正在跑 `uv sync`，已 28 分钟"，重派的收益/成本会被正确评估。

**修复方案**
1. **执行器心跳（核心，~半天）**：`exec.go` 事件泵旁加 30s ticker → `callRPC(link.MethodRunProgress, {run_id, token, current_tool, elapsed_s, last_event_age_s})`；daemon 新增 `IngestRunProgress`（与 [machine_dispatch.go:332](internal/daemon/machine_dispatch.go) 并列），刷新 `machineLastEvent[runID]` 并广播 `run:progress`（WS 转发清单加一条）。**语义要写清**："executor 活着，agent 可能在等模型"——不是 liveness 证明，不能拿它替代 zero-event guard。
2. **idle 评论带上进度**：`machineWatchdogTick` 读最近一次 progress → `Run idle for 2m — last event 8m ago; running \`uv sync\` (12m); total 34m. Won't auto-cancel.` 同时把"距上次输出"和"已运行"分开，人能一眼判断是慢还是死。
3. **把 ACP 的 plan/usage 落成进度事件**：`OnPlan`/`OnUsageUpdate` 落 `chat_message(kind='plan'/'usage')`，agent 自己的 todo 就是最好的阶段条。

**测试**：fake machine 发 progress → 断言 ② 不写 idle 评论、③ `machineLastEvent` 被刷新；2min 无任何事件 → 评论包含 tool/elapsed；WS 转发单测。
**风险**：事件量（30s/run 可忽略）；心跳会"掩盖"真正的 hung read（读卡住但进程活着）——所以 idle 判定应改为"没有 progress **且** 没有事件"，且 progress 里要带 `last_event_age_s` 让人自己判断。

### RT-4（P1）基础设施失败与任务失败共用预算，恢复后不自愈

**证据**
- `maxAttempts=3`（[goal.go:97](internal/service/goal.go)）+ `retryBackoffFor` 30s/60s（`:102-108`）+ 耗尽即 `goal='failed'`（`:1443-1450`）。
- 失败分类不存在：zero-event guard 的通用句（[backend.go:139-147](internal/proto/acpbackend/backend.go)）与任务失败共用 `StatusFailed`；`RunFinishedParams` 没有 failure class 字段（[exec.go:431](cmd/agentwork-cli/exec.go)）。
- 通知面：`goal:finished{failed}` 有卡片（[notify.go:117](internal/notify/notify.go)、[notify.go:454-482](internal/notify/notify.go)），`goal:retrying`/`goal:retry_failed` **无订阅**（[notify.go:102-126](internal/notify/notify.go)）。
- 机器探活只探 CLI 二进制（[connect.go:326](cmd/agentwork-cli/connect.go) `probeTick` 每分钟 `--version`），平台根本不知道模型端点（端点是 CLI 自己的配置），所以"端点恢复即重试"只能靠失败分类 + 时间窗退避实现。

**量化**：run 计算量 11s；wall 空档 **24m28s**；3 次尝试在 ~100s 内烧完，恢复窗口（30s+60s 退避）远小于端点恢复所需的分钟级。

**修复方案**
1. **失败分类（先做小版本，不跨层）**：在 `reconcileOnRunEndOnce` 的 failed 分支里对 `rc.Summary` 做特征匹配——命中 `"agent exited without responding"` 且 `stderr:` 段含 `404`/`connection refused`/`rate limit` 的归 `infra`。infra 失败**不消耗 attempt**，改用窗口退避（1/2/4/8/16/30min，硬上限 6 次或 30min 窗口）；任务失败仍走 3 次。彻底方案是给 `proto.Result`/`RunFinishedParams`/`run` 表加 `failure_class`，由执行器判定（语义更准，但跨 4 层）。
2. **runtime 隔离**：infra 失败后把 runtime 标 `degraded` + `retry_at`（runtime 表已有 state 字段被 Claim 用于 `rt.status != 'absent'`，[run.go:678](internal/service/run.go)），Claim 期间 `degraded` 的 run 保持 queued，**不要 claim 了再失败**——这样重试不烧 attempt，也不会每 4 分钟在评论区留一条红。
3. **告警**：`notify.Subscribe("goal:retrying", ...)` 加一张 ⚠️ 卡（"第 2/3 次尝试失败（infra）：HTTP 404 · 30s 后重试"）；`onGoalFinished` failed 卡片把 failure_class 与 `stderr` 首行放前面，别让通用文案吃掉 200 字。

**测试**：分类函数单测（各失败样本）；infra 失败序列断言 attempt 不变、退避序列正确、窗口耗尽后 goal failed；notify 卡片快照；`Claim` 在 `degraded` 下不取单。
**风险**：把任务失败误判 infra → 无限重试烧钱（只认明确网络错误码 + 硬窗口 + 用户可 Stop）；"快速失败"不能丢（现状 2–5s 是对的行为，只补告警与自愈，不要加长重试间隔）。

### RT-5（P1）预算双权威 + 机器侧无 deadline

**证据**
- `reapRunawayRuns` 用 domain budget + 5m（[daemon.go:2605-2611](internal/daemon/daemon.go)）；`runBudgetSeconds` 的 COALESCE 默认 7200（[daemon.go:2659-2673](internal/daemon/daemon.go)）。
- watchdog 硬编码 2h，且注释就写着"total duration > 2h"（[machine_dispatch.go:661-663](internal/daemon/machine_dispatch.go)），注释块 `:600` 亦同。
- 执行器 `ctx, cancel := context.WithCancel(...)`（[exec.go:106](cmd/agentwork-cli/exec.go)）——**机器侧没有 per-run 截止**；取消经 poll 下发（[connect.go:310-312](cmd/agentwork-cli/connect.go)），最长 30s；watchdog 再等 2min 才本地 stamp（[machine_dispatch.go:678-681](internal/daemon/machine_dispatch.go)）。

**量化**：本案例未触发（#9 6236s < 7200s），但 7200s 与 2h 的关系是"恰好相等"，任何 domain 调整都会让两套权威分叉。最坏情况：domain 设 30m → 平台 35m 取消，watchdog 仍要 2h；domain 设 4h → watchdog 2h 先杀，用户的预算配置被静默无视。

**修复方案**
1. `runMachineWatchdog(runID, machineID)` 增加 `domainID`（或起手查一次 run 行的 goal→domain），`machineWatchdogTick` 用 `d.runBudgetSeconds(ctx, goalID, domainID)` 替代 `2*time.Hour`；注释同步改。
2. `exec.go` 用 `context.WithTimeout(ctx, budget+grace)`（budget 由 dispatch 参数下发，`link.RunStartParams` 已承载多字段，加一个 `max_run_duration_s`）；到点走既有的 `sess.Cancel` + `finish(..., "cancelled","max_run_duration")` 路径（[exec.go:393-396](cmd/agentwork-cli/exec.go)），机器侧先落终态，平台 watchdog 只兜底。
3. （P2）把 cancel 从"下轮 poll"改成推送，或把 poll hold 从 30s 降到 10s，减少取消延迟。

**测试**：watchdog 用 fake clock + domain=60s 断言 60s+grace 触发；执行器 ctx 超时 → `finish cancelled` 且不 commit（scratch/proc 分支）；`runBudgetSeconds` 回落默认。
**风险**：预算调小会杀掉长构建（必须让 domain 配置显式且 UI 可见）；grace 太小会把"正常尾段"误杀。

### RT-6（P2）固定启动成本与计时口径

**证据/量化**：见 §3.2；案例实测 ≤5s/run、23 run ≈ 1–2min。**结论是"不该把它当主因"**，但三处机械浪费与一个口径问题值得顺手修。

**修复方案**
1. `ensureBareRepo` fetch 节流：执行器进程内 `map[repo]lastFetch`，60s 内跳过（owner 的当前分支基线可强制 fetch）。收益：每次 run 省一次网络往返。
2. `copyAgentSkills` 走清单（path+mtime+size），未变则跳过；`removeStaleSkillCopies` 已有一半逻辑。
3. 计时分层：run 增 `ready_at`（claim ack 后、首条 session/update 前），报表区分"启动"与"执行"；否则 6236s 里混着 fetch/skills/冷启动，用户无法判断慢在平台还是慢在 agent。
4. （不建议）不要为了 5s 去恢复常驻进程池——05f09aa 的机器执行架构用"每 run 独立进程"换崩溃隔离与权限收敛，收益只有秒级，风险远大于收益。

**测试**：fetch 节流（fake git 计数）；skills 复制跳过；`ready_at` 断言。
**风险**：节流让分支基线过时 → owner 强制 fetch；`ready_at` 打点位置若选错会引入新歧义。

---

## 五、确实值得改 vs 设计取舍/不该改

**确实值得改（按建议顺序）**
1. **RT-3 进度信号**（半天，收益：把"卡死误判"变成"看得见的慢"，并直接削减重派冲动）。它是最便宜的 ROI：不改任何决策，只补一条 30s 心跳 + 评论文案 + plan 落库。
2. **RT-4 infra 失败分类 + 恢复自愈 + 重试告警**（1–2 天，收益：把 25min 空档从"人肉恢复"变成"自动等端点回来"）。案例的价值在于它精确命中了 3 次预算 + 30s/60s 退避，说明重试框架本身是对的，错的是"不分种类"。
3. **RT-1 止损档**（1 天，收益：直接消灭 37% 的重复计算里最粗的那部分）。只做 Claim 的单写者准入 + 可见的 deferred 状态，不做 attach 改造。
4. **RT-2 断点记录 + auto_resume 开关**（2–3 天，默认关）。它让"续跑"从营销词变成可配置能力，但价值依赖 RT-3 的断点数据。
5. **RT-5 预算单一权威**（半天）。便宜，防未来配置失效。
6. **RT-6** 只在顺手时做；不要为秒级收益回退架构。

**设计取舍 / 不该改**
- **idle 只通知、不取消**（决策 2-6）：理由是 rate-limit 退避会自愈，取消会把"慢"变成"坏"。**不改**。要改的是"看不到进度"，不是"要不要杀"。
- **timeout 不自动续跑**：同上，自动接续有 autoloop 风险。**默认不改**，加 domain 开关是折中。
- **consult/verify/owner 并行**：verify 与 owner 并行是审查窗口的设计特性（[run.go:644-645](internal/service/run.go)）。**不该一刀切串行**；需要的是"写者声明"（domain 级或 role 级），让"会改共享目标"的 run 排队，而纯只读的 verify 继续并行。
- **每 run 独立进程**：机器执行架构的有意选择（崩溃隔离、权限收敛、控制面/执行面分离）。**不为秒级开销回退**。
- **run 计时从 claim 开始**：与"D1 claim 即执行"的模型一致，改动会牵动看板与 SLO 口径；用新增 `ready_at` 而不是改 `started_at`。
- **报告"恢复调度续跑"这个说法**：不是机制，是现象。不该为它加一套"续跑调度器"，该做的是 RT-2 的断点续跑 + RT-3 的进度可见。

---

## 六、与报告结论的偏差（建议报告修订）

1. **A3 "平台 4 次误报 Run idle（…），并据此重复调度"** —— 后半句没有代码支撑：idle 处理只写一条 system 评论，**不取消、不派发**（[machine_dispatch.go:636-652](internal/daemon/machine_dispatch.go)，文案自己写着 "The platform will not auto-cancel"）。重复 run 来自两次 mention 派发。**建议改为**："平台 4 次误报 Run idle；由于没有任何真实的进度信号，人和协调 agent 只能选择重派，间接导致 run#12 的重复执行。"
2. **"恢复调度续跑"（run#12）** —— 平台无此机制（§3.1）。实际链路是：run#10 终态 → consult-resume 唤醒 requester（AI SHELL，run#11，16:52:11）→ 重新派发 guest（run#12，16:53:23）。**建议改为**"协调者重新派发的收尾 run（与原 run 并行重做了 tail）"。
3. **"25 分钟空档"不是平台退避** —— 平台在 ~100s 内烧完 3 次尝试（30s/60s 退避，与时间戳逐秒吻合）后把 goal 置 failed，**没有任何自动重试**。空档 = 端点恢复 + 人处理。报告 A1 的 impact "任务延迟约 25 分钟"作为 wall clock 是对的，但不该被读成"平台在退避重试"。
4. **§6 数字口径**：AI SHELL "约 4.7h" 与其 14 条明细（4 053s）及总账（19 200s）不自洽；"verify/consult 5 次约 21min"按明细是 1 980s（33min）；AI SHELL run 数明细是 14 次。建议以逐 run 明细为准重算（本文 §2.2 的表可直接用），否则归因会失真。
5. **run#8 缺失**：明细从 #7 跳到 #9，23 次 run 只列了 22 条；残差 234s 即 run#8（时间窗 16:20:21–16:24:08）。补上它，总账才能闭合。

---

## 七、后续验证方法（给复现者）

在案例库上跑这三条 SQL 即可把本文的分类落到真实数据（`goal_id='f68bd0a5e173da18437daf680a376409'`）：

```sql
-- 1) 逐 run 时长与角色（核对本文 §2.2 的表，确认 run#8 与 run.role）
SELECT id, role, attempt, status,
       ROUND((julianday(finished_at)-julianday(started_at))*86400) AS secs,
       SUBSTR(result_summary,1,120) AS summary
FROM run WHERE goal_id='f68bd0a5e173da18437daf680a376409'
ORDER BY started_at;

-- 2) 冷启动开销：claim 到首条事件的时间（本文 RT-6 的 ready_at 建议依据）
SELECT r.id, r.started_at, MIN(m.created_at) AS first_event,
       ROUND((julianday(MIN(m.created_at))-julianday(r.started_at))*86400) AS ready_secs
FROM run r JOIN chat_message m ON m.run_id=r.id
WHERE r.goal_id='f68bd0a5e173da18437daf680a376409'
GROUP BY r.id ORDER BY r.started_at;

-- 3) 重叠矩阵：任意两个 run 的重叠秒数（可直接复算 7 007s 口径）
SELECT a.id AS run_a, b.id AS run_b,
       ROUND((julianday(MIN(a.finished_at,b.finished_at))-julianday(MAX(a.started_at,b.started_at)))*86400) AS overlap_secs
FROM run a JOIN run b ON a.id<b.id
WHERE a.goal_id='f68bd0a5e173da18437daf680a376409'
  AND b.goal_id=a.goal_id
  AND MIN(a.finished_at,b.finished_at) > MAX(a.started_at,b.started_at);
```

日志侧（daemon log）对照关键词：`run: finished`（含 `status=%s attempt=%d`）、`run: coalesced`、`goal: retrying`、`machine: watchdog firing`、`machine: run %s event seq gap`、`prior session dropped (workdir ...)`。这七行足以判断一次"慢"是**重试、合并、双发、预算、还是冷启动**。