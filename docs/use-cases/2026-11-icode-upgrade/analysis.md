# 案例分析：icode 0.28.0 → 0.35.2 升级作业

> 分析对象：`execution-report.md`（原始执行报告，2026-11-13 15:52 – 11-14 10:30）
> 代码基线：`/home/sinkear/developer/agentwork` HEAD `9931759`（2026-11-13）。生产 daemon 为 11-03 构建（`19f9c40`/`23a7ce1`），本文引用的 daemon/service 路径在两者上逐字节一致（已用 `git diff 23a7ce1 HEAD -- <path>` 核对）。
> 配套：`findings-dispatch.md`（派发链路 D1–D7）、`findings-runtime.md`（运行生命周期 RT-1–RT-6）、`findings-watchdog.md`（看门狗与交付 W1–W8）。本文是三者与 report 的交叉结论，**若干处修正了 report 与单点分析的口径**。

---

## 0. 结论先行

这个案例的 5.3 小时计算量里，**约一半不是"活难"，而是平台的调度与验收机制在空转**：

| 口径 | 平台侧可归因 | 占 18966s | 说明 |
|---|---|---|---|
| 保守（只算可确证重复 + 平台自有 meta run） | **~4857s ≈ 81min** | **25.6%** | 双派发 2540s + verify/consult/reopen 2317s |
| 重叠口径（含与 run#9 时间重叠的 run#12） | **~9300s ≈ 2.6h** | **~49%** | 重复/并发 7007s + meta 2317s |

- **最大单笔是 run#12（4562s，24%）**：AI SHELL 16:52 收到 consult 回复后，在 Upgrader 主 run（run#9，16:24–18:08）**仍在运行**时直接"重新派发"。两边都改同一个 icode 运行目录（run#10 评论：`runtime cleared twice, uv cache lock`），互相拖慢，最后只留一份有效结果。这是"派发侧"问题，不是"活难"。
- **第二笔是 4 个阶段的成对重复 run（2540s，13.4%）**：平台 `create_sub_goal` 与模板里的"@某人派发"是**两条派发通道、两个 run 角色**，现有按 `(goal, agent, role)` 的合并键天然吸不住。
- **第三笔是平台自己的 verify/consult 元动作（2317s，12.2%）**：verify 一个工作项 673s，prompt 却只有标题+描述，机器已经算好的 diff/校验证据一句都没注入。
- 另有 **3 次模型端点 404 烧完 3 次 attempt → goal failed → 人手动 Reopen，25 分钟墙钟**（11s 实际计算）；以及 **scratch 域交付完全不跑机器校验**，A7 的打包口径不严、A8 的版本三轴错位都是从这个缺口漏过去的。

一句话：**慢的主因是"重复派发 / 咨询回流 / 元动作空转"，不是模型慢；而"质量问题"的主因是交付前平台没有执行任何机器验收。**

---

## 1. 案例事实与时间账

### 1.1 事实

- Goal `f68bd0a5e173da18437daf680a376409`「升级icode至最新版本」，squad「自动升级作业环境小队」，leader AI SHELL，执行者 Checker / Upgrader / Verifier / Packager。
- 23 个 run，18966s ≈ **5.27h** 计算，41h50m 墙钟（其中约 20h 是人在等/夜间停摆，平台本身没有 20h 的空转）。
- 产物：icode 1.0.3 → 被取代的 1.0.4；打包 1.0.3 → 1.0.4。

### 1.2 时间账（按 run 表 + report §6 交叉核对）

| 类别 | 秒 | 占比 | 内容 |
|---|---|---|---|
| 真实工作 | 14098（口径 A，report 分法） | 74.3% | 7 个主 run |
| ｜重叠口径修正后 | 9536（口径 B） | 49.7% | 抹掉 run#12 与 run#9 重叠的 4554s |
| 重复/并发 run | 2540（保守）/ 7007–7102（重叠） | 13.4% / 36.5–37% | Checker 71、Upgrader dup 1674、PackagerA 795、run#12 4481–4562 |
| 平台 meta run | 2317 | 12.2% | verify 1591、consult 465、Reopen/warning 261 |
| 启动失败 | 11 | 0.06% | 3×404 |

口径 B 的算法与核对 SQL 见 `findings-runtime.md` §1（`run#12` 的 `role/sub_goal_id/trigger_comment_id` 三条 SQL 一跑就能定归属）。

### 1.3 三个需要修正的原报告结论

1. **"4 次 idle 误报 → 平台反复调度"**：不成立。idle 评论走 `CreateNoDispatch`（`internal/service/comment.go:125-127`），在 `comment.go:205-207` 直接 `return`，既不 Reopen 也不派发。4 条是 4 个**并发 run** 各一条（每个 run 至多一条，`machine_dispatch.go:608-618`）。真正的重复来自双派发与 consult 回流。
2. **"25min 空档是平台退避/重试"**：不成立。3 次 404 在约 100 秒内烧完（退避 30s+60s），第 3 次直接 goal failed，平台**没有自动重试**；25 分钟是"人发现 failed → Reopen → 重新派发"的墙钟。
3. **"平台对两条 mention 各起一个 run"**：机制不准。单条评论内的 mention 会去重（`comment.go:82-96`），同角色评论也会 coalesce；绕过去的是**两条通道的角色不同**（`subgoal` vs `consult`）。
4. 报告 §6 的 AI SHELL 4.7h 与其 14 行明细合计 4053s 对不上；且漏了 run#8（234s，16:20–16:24）。

---

## 2. 慢在哪里：一次"干净"的派发如何变成 4 个 run

以 Upgrader 阶段（report 中最贵的一段）为例：

```
16:19:09  leader 调 create_sub_goal
            ├─ 同一事务里裸 INSERT 一条 leader 署名的派发评论
            │   （subgoal.go:170-180；直接写 comment 表，**不触发派发**）
            └─ 显式入队 role='subgoal' run        ← run#9 (6236s)
16:19:4x  leader 按模板"在评论里 @Upgrader 派发"
            └─ CommentService.Create → EnqueueForMention
                 role = consult（agent author）    ← run#10 (1674s)
            ⇒ 同一 agent 两个 run 并发（max_concurrent=3，Claim 不拦）
16:33/17:01 idle 评论 ×2（不派发，纯噪声）
16:52:11  run#10 结束 → consult-resume 给 requester(AI SHELL) 入队 owner run
16:53:23  AI SHELL 看到"任务结束"（其实 run#9 还在跑）→ 重新派发  ← run#12 (4562s)
18:08     run#9 结束；18:09 run#12 的产物覆盖/重做
```

这条链上平台做了 4 次"本该合并"的动作：两条派发通道、一次 consult 回流、一次无并发感知的重派。**任何一环有"同一工作项/同一执行者已在跑"的判据，都能省掉 4562s。**

---

## 3. 一级发现（确实存在、可改、影响可量化）

### R1[P0] 同一工作项有两条派发通道，角色不同 → coalesce 吸不住 → 成对重复 run

**证据**（HEAD 未修）

- `internal/service/subgoal.go:156-180`：`CreateSubGoal` 在同一事务里 `INSERT INTO comment` 写一条派发评论（`AuthorType/AuthorID = createdByType/createdByID`——**leader 本人**，feed 上与手写评论不可区分），然后显式 `enqueueSubGoalRunTx` → `role='subgoal'`。注意这条评论是**裸 INSERT，不经过 CommentService，不触发派发**——所以第二个 run 不是它起的。
- 第二个 run 来自 leader 自己的评论：模板要求"派发后在评论里 @ 执行者"，`goal comment` 走 `CommentService.Create(dispatch=true)` → `EnqueueForMention` → `role='consult'`（`comment.go:108-360`、`run.go:275-295`）。
- `internal/service/comment.go:108-360`：`Create` 持久化后派发 mention；`mention://agent` → `EnqueueForMention`，角色由触发作者推导（`internal/service/run.go:275-295`）：agent/human → `consult`，system → `review`。
- `internal/service/run.go:165-245`：合并键 `SELECT id,status FROM run WHERE goal_id=? AND agent_id=? AND role=? AND status IN ('queued','running') ORDER BY queued_at LIMIT 1`——**跨角色不合并**（注释明确这是正确性约束，不能放松）。
- `internal/service/run.go:686-691`：Claim 只做两类单飞——owner-owner 同 goal、同 `sub_goal`。`consult` 与 `subgoal` 是不同 run、同 `sub_goal` 但不同 role → 放行。
- 两处代码最后修改于 2026-09（`90f0fa3`、`ad4f241`），早于 11-03 构建，**部署版同缺陷**。

**本案例代价**：Checker 71 + Upgrader 1674 + Packager 795 = **2540s（42.3min，13.4%）**；且 Upgrader 两个 run 互相清 runtime、抢 uv 缓存锁，额外放大了 run#9 的时长。

**改法**

1. `internal/service/run.go`：`enqueueTx` 在 `(goal,agent,role)` 合并**之前**增加"工作项级"去重——入参带 `WorkRef`（子任务场景 = `sub_goal_id`，owner 场景 = goal id），若同 `WorkRef` 已有 `queued|running` 且 `role IN ('subgoal','consult','owner')` 的 run，则合并/丢弃并写 `activity_log`（`dedupe:workref`）。窗口不设时间，靠 run 终态收敛；`review/verify` 不参与。
2. 更简的等价实现：`CreateSubGoal` 在 `:179` 之后不再让模板派发——**模板侧改为"派发只用 `subgoal create`，@mention 只用于提问"**（`agentwork-templates` 同步改，平台加 prompt 硬规则）。
3. 测试：`internal/service/run_test.go` 新增"create_sub_goal 后紧跟一条 @assignee 评论 → 只产生 1 个 run"；`TestMentionDispatchRoleMatrix`。

### R2[P0] 平台没有"这个 goal 的执行者已经在跑"的概念，咨询回流又在推波助澜

**证据**

- `internal/service/run.go:632-645`：并发模型是"每个 run 一个 worktree"（决策 6-2），所以去掉了 per-goal 锁，只留两道闸：owner-owner 同 goal、同 sub_goal；注释明确 consult/review/verify 是 "read-only snapshots and run freely in parallel"。
- **这个假设在本案例不成立**：run 之间确实有独立 worktree，但冲突发生在 **worktree 之外**——同一台机器上的 uv 缓存、`/root/jobEnvPackage`、icode 运行目录。worktree 隔离挡不住机器级共享状态，"只读 snapshot"实际上在写共享目录。
- 唤醒 prompt 不含"进行中的 run"：`daemon.go:3051-3055` 只给 consult 状态摘要；leader 被 consult 唤醒时看不到 run#9 还在跑，于是判断"活干完了"。
- `CreateSubGoal` 不校验"同 goal+assignee+标题"是否已有非终态工作项（`subgoal.go:49+`），重派一定建新工作项，`Claim` 的同 sub_goal 闸门自然拦不住。
- `internal/service/goal.go:1116-1141`：**每个 guest run 终态**都给 requester 入队一个 owner run（`EnqueueExistingTx`），没有"还有别的 consult 在跑"的判断（该判断只存在于 `:1213-1220` 的另一条路径）。
- `internal/daemon/daemon.go:1905-1912`：consult/verify 不续用 session，每次全量重放上下文。
- run#12 与 run#9 重叠 4554s 且都写同一 icode 运行目录（report 评论 `runtime cleared twice, uv cache lock`）。

**本案例代价**：run#12 **4562s（24%）**；consult 回流本身 465s（76+202+187）。即使 run#12 是"新 sub_goal"而非 consult，R2 的执行者级单飞也能拦住。

**改法**

1. Claim 增加执行者级单飞：`AND NOT EXISTS (SELECT 1 FROM run r2 WHERE r2.goal_id=r.goal_id AND r2.agent_id=r.agent_id AND r2.status='running' AND r2.id<>r.id)`，对 `role IN ('owner','subgoal','consult')` 生效；`verify/review` 保持并行（只读）。若确有并行需求，用 `agent.max_concurrent` 显式打开（默认 1）。
2. consult 续跑改为批量：同一批 guest 全部终态后再唤醒一次 requester（复用 `:1213-1220` 的 `pendingConsults` 口径，在终态事务里判断 `NOT EXISTS other pending consult`）。
3. consult prompt 明确写"这是咨询，不要改共享工作目录；需要改就新建工作项"。
4. 唤醒 prompt 注入 live state：`assemblePrompt` 的 owner/consult 分支加一段"Active work in this goal: run#9（Upgrader, subgoal, running since 16:24）"——平台本来就知道这些数据，leader 不需要猜。
5. `CreateSubGoal` 加幂等：同 goal+assignee+归一化标题已有非终态工作项时，返回已有项（`already dispatched: <sg id>`），不新建。
6. 测试：`TestClaimBlocksSecondRunningRunForSameAgent`、`TestConsultResumeCoalesced`、`TestOwnerWakeListsRunningRuns`。

### R3[P1] verify run 只拿到标题+描述，机器已算好的证据不注入 → 每个工作项再花 10 分钟重算

**证据**

- 触发：`internal/service/subgoal.go:274-302`（subgoal run completed 且有 `verifier_id` → park `verifying` + 入队 `role='verify'`）。
- prompt：`internal/daemon/daemon.go:2962-2964` 只有 `"Verify this work item: "+title+"\n"+desc`。
- 证据其实已经存在：`internal/daemon/machine_dispatch.go:834` 把 `run.evidence = {diff_stat, changed, verify, guards, agent}` 写进 run；`internal/daemon/verify.go:385-399` 生成它。**但没有任何代码把它注入 verify prompt。**
- `internal/daemon/daemon.go:1905-1912`：verify 不续 session，无法"看一眼上次的输出"。
- 无上限返工：`internal/service/subgoal.go:520` 每次 reject `quality_iteration+1`，不封顶。

**本案例代价**：verify 共 **1591s（26.5min，8.4%）**，单次 299/673/619s；run#7/#13/#19 都是"从零复述+重跑校验"。

**改法**

1. `daemon.assemblePrompt` 的 verify 分支注入 evidence：`diff_stat`、`changed`（截断 4KB）、`verify` 输出尾部（2KB）、`guards` 结果；格式固定小标题，超限截断并标注。
2. 先"影子模式"：只记录"如果注入会多给多少 token/是否会改变 verdict"，跑 3 个案例再启用。
3. `quality_iteration >= 5` 时不再自动返工，转 goal review（人决定）。
4. `verifier_id == ""` 时走纯机器通道（`subgoal.go:303-327` 已存在），能机器判的（编译/测试/版本戳）不要起 LLM run。
5. 测试：`daemon/prompt_test.go` 断言 evidence 出现在 verify prompt；`TestVerifyIterationCap`。

### R4[P1] 基础设施失败与任务失败共用 3 次 attempt，且失败后无自动恢复 → 25 分钟墙钟

**证据**

- `internal/proto/acpbackend/backend.go:139-147`：3 次 `NewSession` 全失败后 run 以 "agent session did not start" 结束；一次 session 都没建立成（11s 计算）。
- `internal/service/goal.go:97`、`:102-108`、`:1443-1450`：所有终止原因共用 `maxAttempts=3`、退避 30s/60s；第 3 次 → goal `failed`。
- `internal/daemon/daemon.go:2582-2673` / `goal.go:2257-2274`：失败/超时后不会自动重建 run；唯一入口是人 `POST /goals/{id}/continue`。
- `internal/daemon/notify.go:454-482` 把失败摘要截到 200 字符；`goal:retrying` 没有 IM 订阅（`notify.go:102-126`）——人只看到"失败了"，看不到"模型端点 404"。

**本案例代价**：11s 计算 → **25 分钟墙钟**（人 Reopen + 重新派发）；且这 25 分钟发生在最贵阶段之前，放大了整条链的空档。

**改法**

1. 引入失败分类 `FailureClass`：`infra`（session 未建立 / 端点 4xx/5xx / 连接拒绝 / 0 事件）vs `task`。`infra` 不消耗 attempt、不把 goal 置 failed，而是 goal 保持 active → `queued_at = now + backoff` 重新入队（最多 5 次），同时发 IM 告警"模型端点不可用"。
2. 告警里带原始错误（不截到 200 字符；或截断处附 `run.<id>.error` 链接）。
3. claim 前做端点 preflight（现有 `probeTick` 只探 CLI 二进制，`connect.go:326`，加一次模型 `/models` 或最小补全探测，失败就不 dispatch）。
4. 测试：`TestInfraFailureDoesNotConsumeAttempt`、`TestGoalStaysActiveOn503`。

### R5[P1] scratch 域交付**完全不跑机器校验**：A7 口径不严、A8 版本三轴错位都从这里漏过

**证据**

- `internal/daemon/machine_dispatch.go:697-707`：`if domainType == "scratch" || role == "consult" || role == "review" { return "" }`——**在 checks/guards/gates 之前早退**，`buildEvidence` 根本不会被调用，`run.evidence` 为空。
- `internal/daemon/deliver.go:102-105`：`finishDeliver(..., true, "无仓库交付——产物在项目目录，按汇报验收", nil)`——**审批依据是 agent 的自述**。
- 设计上 scratch 的产物就是 report + 强制人卡点（`p0_regression_test.go:473-505` 验证"必 park review"），但"人卡点"不等于"有证据"。

**本案例代价**（不是慢，是正确性）：A7 的 `--env view` 非严格口径、缺 license/upstream/attribution/THIRD_PARTY；A8 的 1.0.2/1.0.3/1.0.4 三轴错位直到人工补验 zz.009（10:15，`agentwork_runs: 0`）才发现。平台全程零参与。

**改法**

1. scratch 域也允许声明 `checks.Verify`：由 exec CLI 在 agent 退出后执行（`cmd/agentwork-cli/exec.go` 已有事件泵与退出码通道），产出 `reports/verify-evidence.json`，`ArtifactFiles` 带上；非零退出 → run failed。
2. `deliver` 在"域冻结了 Verify 命令"时，无 evidence 不放行（默认只告警，`verification_strength=strong` 才硬拦——避免调研类目标被卡）。
3. 把"严格 spec 扫描""版本一致性（zz.009 等价）"写成该 packaging domain 的 Verify 命令（由模板仓库提供，平台只执行/留证），**不要给 goal 加 template_version 列做对账**（三轴都不在平台状态里，见 `findings-watchdog.md` W8）。
4. 测试：`TestScratchDomainRunsVerifyChecks`、`TestDeliverRequiresEvidenceWhenChecksFrozen`。

### R6[P1] 证据不落盘：报告默认写 `/tmp`，平台没有任何"持久产物目录"的约定

**证据**

- 持久 goal 目录**已经存在**：`internal/paths/paths.go:95-98` `ScratchGoal`；exec 的 cwd 就是它（`cmd/agentwork-cli/exec.go:715-722`）。
- 但只"告知"不强制：`internal/daemon/team_context.go:71-78` 只给路径，不给报告子目录、不禁止 `/tmp`。
- 没有目录环境变量：`exec.go:739-743` 只注入 `AGENTWORK_SERVER_URL/GOAL_ID/RUN_ID/AGENT_ID/TOKEN`。
- 证据只能内联：`collab.go:78` 的 `--evidence` 是字符串，`subgoal.go:483-487` 落库；`ArtifactFiles` 只服务 processor/import/digest（`link.go:284,505-508`、`exec.go:431-438`、`daemon.go:1949/2035`）。
- 所以 `/tmp` 是 skill/模板的选择（job-env 系列硬编码 `/tmp/job-env-*`），不是平台默认，也不是容器。

**本案例代价**：A9——Checker/Verifier/spec 报告丢失；最终验收靠人回忆 + 重跑。

**改法**

1. `exec.go` `buildEnv` 注入 `AGENTWORK_WORKDIR` / `AGENTWORK_GOAL_DIR` / `AGENTWORK_REPORT_DIR`（scratch = `<goalDir>/reports`）。
2. `team_context.go` 固定块加硬规则："所有报告/证据写到 `AGENTWORK_REPORT_DIR`；`/tmp` 下的文件不持久，视为缺失证据"。
3. CLI 增加 `subgoal verify --evidence-file <path>`（读文件，64KB 上限，与 `--evidence` 互斥）。
4. scratch owner run 设 `ArtifactFiles: ["reports/manifest.json"]`，daemon 落副本（跨机审计的最小通道）。
5. 模板仓库同步改（平台改不了的部分）。

### R7[P2] 长静默命令 2 分钟必误报 idle；且"无进度"这件事人看不到

**证据**

- `machineLastEvent` 只由 claim（`machine_dispatch.go:313-316`）和每批 ACP 事件（`:369`）刷新；工具执行中零事件。
- 事件泵只在 `len(buf)>0` 时 flush（`cmd/agentwork-cli/exec.go:351-396`）——`uv sync` 装 183 个包期间可以 5–10 分钟无字节。
- `run` 级没有心跳：`connect.go` 的 5s heartbeat 只更新 `machine.last_seen_at`；ACP 的 `OnPlan/OnUsageUpdate/OnSessionInfo` 全是空实现（`backend.go:300-305`）。
- 触发动作仅 `CreateNoDispatch` 评论（`comment.go:205-207` return），**不取消、不重派、不 reopen**——这一点原报告归因错误。

**本案例代价**：4 条噪声（16:33/17:01/18:23/18:27），误导人"是不是卡了"；没有直接计算浪费，但直接促成了 report 的错误归因，也给人"平台在瞎猜"的体感。

**改法**

1. 通知前先看最后一条持久化事件（`chat_message`）：若最后是 `tool_use` 且同一 CallID 无 `tool_result`，则视为"在一条命令里"；在 `toolSilenceBudget`（15min 或 `min(30min, max_run_duration/4)`）内不发，超预算发一条带 `tool/callID/已运行时长` 的说明，且每个 open call 只发一次。
2. 文案改为"最近一次可见事件在 HH:MM:SS；若正在跑长命令可忽略"。
3. 不建议加"run 心跳"替代（心跳只证明 CLI 活着，不证明 agent 在推进，会把该报的卡死变成永不报）。
4. 测试：`TestWatchdogSilentLongToolDoesNotNotify`、`TestWatchdogOpenToolPastBudgetNotifiesOnce`。

### R8[P2] 两套预算权威：domain 7200s vs 机器 watchdog 硬编码 2h

**证据**

- `internal/service/domain.go:38,276-277` `MaxRunDuration`（默认 7200）由本地 reaper 使用（`daemon.go:2605-2673`，+5min grace）。
- `machine_dispatch.go:661-681` 机器 watchdog 写死 `if time.Since(start) <= 2*time.Hour`，忽略 domain 配置；且 executor ctx 无 deadline（`exec.go:106`），取消靠 30s 轮询（`connect.go:310-312`）。

**本案例代价**：未触发（都是正常结束），但 run#9 6236s 已逼近 7200s，换一个更慢的包就会撞上两套不同的取消语义。

**改法**：watchdog 从 `domain.MaxRunDuration` 取预算（缺省 7200），与 reaper 用同一个值；取消时把 `cancel_reason` 与"已运行时长"写进评论。测试：`TestMachineWatchdogUsesDomainBudget`。

---

## 4. 二级发现（真实但影响小，顺手改）

| # | 问题 | 证据 | 代价/风险 | 改法 |
|---|---|---|---|---|
| S1 | verify run 结束但没给 verdict → sub_goal 永久 `verifying` | `subgoal.go:618-641` 只 `stamp reconciled_at`，不查裁决；全仓无恢复路径 | 本案例未触发，但这是"真实卡死"最可能的位置 | 同事务查 `sub_goal.status`/`verification_result`：无裁决且 run completed → 重派一次；否则 park goal review；加 sweeper |
| S2 | `create_sub_goal` 无幂等键 | `subgoal.go:49+` 无唯一约束，同 (goal,assignee,title) 可建 N 个 | 模型重试一次就多一个工作项 | service 层 `INSERT ... ON CONFLICT` + 部分唯一索引（`status<>'done'`） |
| S3 | 同一条评论 @squad + @leader → owner+consult 两个 run | `comment.go` mention 循环无 `seenAgent` | 少见，但一次就双跑 | 循环内按 agent 去重，保留优先级最高的角色 |
| S4 | Collaboration Warning 对所有角色无差别注入，且计数含 consult | `daemon.go:3048-3050` 无 role 判断；`:2493-2500` 计数含 consult | 4 段正常流水线必触发；verify prompt 里出现"别再派发"会让 verifier 不敢报问题；run#20 261s 并导致 owner 绕开小队自己干 | 只注入 owner/consult；计数拆"consult / 重复派发"两项 |
| S5 | verifier 可与 assignee 同一人（玩家-裁判） | `subgoal.go:97-113` 只对 squad reviewer 强制，`verifier_id` 只校验存在 | 本案例 owner AI SHELL 亲自跑 verify run，独立 Verifier 只跑 34 条 base 用例 | 拒绝 `verifier==assignee`（`--self-verify` 逃生门）；owner 可当 verifier 但卡上标注"verified by goal owner" |
| S6 | 工具/事件时序成本 | `gitwork.go:58` 每 run `git fetch`；`exec.go:228` 全量拷 skills；无进程复用；只读角色每次新 checkout | 单 run 2–5s，23 run <2min，**不是慢的主因** | 只读角色共享只读快照、skills 增量同步；优先级低 |
| S7 | 跨机器路径 | `team_context.go:75` 用 daemon 侧 `paths.Runs()` 渲染绝对路径；scratch 无镜像通道 | 换执行机/容器即复现 A9，且更彻底 | 路径改占位符由 exec 替换；`reports/` 走 manifest 镜像（上限 10MB） |
| S8 | 模板仓库硬编码 `/tmp/job-env-*` | 本仓库无该脚本，来自 `agentwork-templates` | A9 的另一半 | 模板侧改为 `${AGENTWORK_REPORT_DIR:-$PWD/reports}`；平台侧开 issue |

---

## 5. 改动路线图（按"收益/成本"排序）

| 顺序 | 项 | 改动面 | 预计收益 | 风险 |
|---|---|---|---|---|
| 1 | **R6 证据落盘**（env + 固定块 + `--evidence-file`） | S：`exec.go` 3 行 env、`team_context.go` 固定块、`collab.go` 一个参数 | 消除 A9 类丢失；为 R5 铺路 | 低（只加约定，不改语义） |
| 2 | **R1 工作项去重 / 模板改口径** | M：`run.go` enqueueTx + 调用方传 `WorkRef`；模板同步 | 直接去掉 2540s 类重复；本案例 -13% | 中（合并错会丢合法派发，需 shadow 一周） |
| 3 | **R2 执行者单飞 + consult 批量续跑 + 唤醒注入 live state** | M：Claim SQL + `goal.go:1116-1141` + `assemblePrompt` | 去掉 run#12 类 4562s；本案例 -24% | 中（会串行化本来并行的 consult，吞吐略降但更省） |
| 4 | **R3 verify 注入 evidence + 返工上限** | S-M：`assemblePrompt` + 阈值 | verify 1591s → 估 800s 内；-4% | 低（先 shadow） |
| 5 | **R4 infra 失败不烧 attempt + 端点 preflight** | S：`goal.go` 分类 + `connect.go` 探活 | 25min 墙钟 → <2min 自动重试 | 低 |
| 6 | **R5 scratch 域机器验收** | M：dispatch 参数 + exec 执行 + deliver 闸 | A7/A8 类问题在平台内闭环；不直接影响速度 | 中（需域策略人工确认，已有 frozen 语义） |
| 7 | **R7 idle 上下文感知** | S：watchdog tick + 一次 `chat_message` 查询 | 4 条噪声 → 0；报告归因不再跑偏 | 低 |
| 8 | S1/S2/S4/S5 | S 各一处 | 消除静默卡死、重复工作项、错误警告、自验 | 低 |

**不建议改**（避免过度设计）：idle 自动取消（`7cb954d` 已移除，恢复会让"慢但会完成"变"坏 run"）；idle 触发重调度（会把观察变成干预）；平台级 `packageVersion` 三轴对账（数据源不在平台）；mention cycle ≥8 直接 failed（粗暴但明确，别下调阈值）。

---

## 6. 验收标准（写进 README 的回归目标）

同一类 icode 升级任务重跑：

1. 总计算 ≤ 3h（当前 5.27h），平台 meta run 占比 < 5%（当前 12.2%）。
2. 同一 (goal, agent) 无并发 run；同一工作项无重复 run（`run` 表按 `sub_goal_id` 分组 `COUNT(*)>1` 为 0）。
3. verify 单次 ≤ 300s，且 prompt 含 `run.evidence`。
4. 端点不可用时 goal 保持 active，自动重试 ≥3 次不置 failed。
5. scratch 交付卡必须带 `verify-evidence`（域配置了 Verify 时）；报告只能在 `AGENTWORK_REPORT_DIR` 找到。
6. 全程 idle 评论数为 0（长命令场景），或每条都带 `tool/callID/时长`。

当前可测的量化基线：`run#9 6236s / run#12 4562s / verify 1591s / dup 2540s / 404 11s+25min 墙钟`。

---

## 7. 与原始报告的差异汇总

| 报告结论 | 本文结论 | 依据 |
|---|---|---|
| A3「idle 误报并加剧重复调度」 | idle 只发评论不派发；重复来自双派发 + consult 回流 | `comment.go:205-207`、`goal.go:1116-1141` |
| A1「25min 空档是退避/重试」 | 3 次 attempt 约 100s 烧完 → failed → 人 Reopen；平台无自动重试 | `goal.go:97,102-108,1443-1450` |
| A2「平台对每条 mention 起一个 run」 | 单评论内去重、同角色合并；跨 role 不合并才是根因 | `comment.go:82-96`、`run.go:196-198` |
| §6 AI SHELL 4.7h | 其 14 行明细合计 4053s；且漏 run#8（234s） | `findings-runtime.md` §1 |
| A7/A8 归因于 agent 不守规范 | 平台在 scratch 域**不执行任何机器校验**，交付只看自述 | `machine_dispatch.go:697-707`、`deliver.go:102-105` |

---

## 8. 证据索引

- 派发：`findings-dispatch.md`（D1 `subgoal.go:156-179` + `comment.go:108-360` + `run.go:165-245,686-691`；D4 `goal.go:1116-1141`；D5 `daemon.go:2962-2964` + `machine_dispatch.go:834`；D7 `goal.go:97,102-108,1396-1450`）。
- 运行：`findings-runtime.md`（RT-1 Claim 并发；RT-2 无 resume；RT-3 无进度；RT-4 失败分类；RT-5 双预算；RT-6 启动成本）。
- 看门狗/交付：`findings-watchdog.md`（W1 scratch 零校验 `machine_dispatch.go:697-707` + `deliver.go:102-105`；W2 证据落盘 `team_context.go:71-78` + `exec.go:739-743`；W4 idle 语义 `comment.go:205-207`；W6 `subgoal.go:618-641`；W7 `subgoal.go:97-113`）。
- 原始：`execution-report.md`；时间表：`timeline.md`。