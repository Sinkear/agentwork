# 派发/触发链路分析（findings-dispatch）

> 范围：从"一条评论 / 一次 `create_sub_goal` / 一次平台触发"到"run 落库可被 Claim"这一段——`internal/service/comment.go`、`internal/service/run.go`、`internal/service/subgoal.go`、`internal/service/goal.go`（reconcile/retry/consult 闭环）、`internal/daemon/daemon.go`（prompt 组装与协作计数）。
> 不覆盖：运行时（进程生命周期、watchdog、idle 误报、late-result）、看门狗/回收器——由 runtime 分析员负责。
> 基线：生产 `agentwork-daemon` 构建于 **2026-11-03**（当日最后提交 `23a7ce1`）；HEAD = `9931759`（2026-11-13 14:17）。每条结论都给出"HEAD 是否已修"的判定与 git 证据。

---

## 一、一页结论（按影响排序）

1. **P0｜派发有两条并行通道，彼此没有幂等键：`create_sub_goal` 与"带 mention 的评论"各起一条 run。** 平台自己替 leader 生成一条派发评论（`role=subgoal`），leader 模板里又把"@某人 + 标题"当成派发动作（`role=consult`）；两条 run 角色不同，入队合并键 `(goal, agent, role)` 必然合不上，Claim 的串行规则又只管 owner-owner 与同 sub_goal ⇒ **同一 agent 同一时刻跑两份同样的活**。本案例 3 个阶段各命中一次：Checker 71s、Upgrader 1674s、Packager 795s，**合计 2540s ≈ 42.3min，占全部算力 13.4%**。
2. **P1｜平台元 run 的固定成本没有压缩手段：verify 3 次 1591s + consult 3 次 465s = 2056s ≈ 34.3min（10.8%）。** verify/consult 是"只读角色"，被显式剥夺 session 续跑（`daemon.go:1905-1912`）且 prompt 只给工作项标题+描述（`daemon.go:2962-2964`）——机器已经算好的 diff/verify 证据（`run.evidence`）一个字都不注入，于是每次判断都从零重建上下文。加上 Reopened/警告 261s，元 run 合计 **2317s ≈ 38.6min（12.2%）**。
3. **P1｜派发侧对"并发"几乎不设限，重叠的 4554s 需要一次 DB 核对才能定性。** Claim 的 `NOT EXISTS` 只拦"owner vs owner 同 goal"和"同 sub_goal"（`run.go:679-684`）；同一 agent 的不同 sub_goal、subgoal vs consult、consult vs owner 全部放行。案例 run#9(6236s, 16:24–18:08) 与 run#12(4562s, 16:53–18:09) 在同一 agent 上重叠 4554s——若 #12 是 consult（最可能），这 4562s（24.1%，本案例最大单笔）就是"进度询问把忙碌 worker 又拉起来重跑一遍"；若 #12 是 watchdog 取消后的 retry，则属 runtime。两种都说明**派发层没有任何"同一 agent 同一 goal 的写执行单飞"**。
4. **P2｜"协作往返"计数口径错了：它数的是"agent 触发的 run 条数"，不是"同一对人之间的往返"。** `agentTriggeredRunCount`（`daemon.go:2493-2500`）把每条 agent 派发 run 都算一次，阈值 4（`MaxMentionHints`）。一条正常的 4 段流水线（每段 1 次派发 + 1 次询问）必然超过 4 ⇒ 警告必响。案例 run#20 里注入的"已往返 5 次，不要再派发"（261s）就是这么来的，而它的建议"自己干完"直接和多 agent 协作目标冲突。
5. **P2｜"启动失败"与"工作失败"共用同一个 attempt 预算。** 模型端点 404 让 3 次 attempt 在 1 分钟内烧光（5s/2s/4s）→ goal **failed** → 等人来点 Reopen（run#4 在 16:18，**25 分钟墙钟空档**）。重试退避（30s/60s）是有的（`goal.go:99-108`，2026-08-16 引入），但它救不了"端点本身死了"。

> HEAD 判定：**上述 1–4 全部未修**。`subgoal.go` / `comment.go` 最后修改 `90f0fa3`（2026-09-09），`run.go` 最后修改 `ad4f241`（2026-09-10）——都早于 11-03 构建，部署二进制与 HEAD 在这些路径上**逐字节一致**。`goal.go` 在 11-05/11-13 有改动，但内容是 `category`（hitl/hootl）与 connection，**不触碰** retry / consult 闭环。第 5 条同样未修。

---

## 二、逐项分析

### D1 双通道派发无幂等：`create_sub_goal` 与 mention 评论各起一条 run ｜ **P0**

**现象**：一次派发动作在 feed 里表现为两条相隔 6–9s 的评论，两条都触发 run，于是目标 agent 同时跑两份同样的活（本案例 Checker / Upgrader / Packager 各一次，两条 run 起始时间几乎相同、结束时间各自独立）。

**证据（两条通道的确切代码路径）**

通道 A——平台代 leader 生成派发评论并起 `subgoal` run（`internal/service/subgoal.go`）：

```go
149:	dispatchID := newID()
156:	dispatchContent := fmt.Sprintf("[@%s](mention://agent/%s) **%s**", label, sg.AssigneeID, sg.Title)
174:	if _, err := tx.ExecContext(ctx,
175:		`INSERT INTO comment (id,goal_id,author_type,author_id,parent_id,content,created_at) VALUES (?,?,?,?,NULL,?,?)`,
176:		dispatchID, sg.GoalID, createdByType, createdByID, dispatchContent, ts); err != nil {
179:	_, runEv, err := s.runSvc.enqueueSubGoalRunTx(ctx, tx, sg.ID, dispatchID)
```

两个关键细节：
- 这条评论的 `author_type/author_id` 是**发起人（leader）**，不是 `system`（`subgoal.go:174-178`）。它在 feed 里和 leader 自己写的评论**长得一模一样**，而 `role='subgoal'` 又被 pingpong 计数豁免——事后没人能从 feed 分辨"哪条是平台生成的派发"。
- 派发评论是**直接 INSERT**（不走 `CommentService.Create`），所以它自带的 mention **不会**再派发一次。这一点是对的。

通道 B——leader 自己发的 mention 评论走 `CommentService.create` 的 mention 循环（`internal/service/comment.go`）：

```go
322:		case "agent":
328:			// Mention-cycle guard: ...
332:			r, e := s.runSvc.EnqueueForMention(ctx, c.GoalID, m.ID, c.ID)
337:			// log "mention dispatch ... → consult run"
342:			// recordConsult (consult_request)
```

`EnqueueForMention`（`run.go:345-347`）→ `enqueue(..., isLeader=false, triggerCommentID)` → `resolveRunRole`：触发评论作者是 agent ⇒ **`role='consult'`**（`run.go:275-295`）。

于是同一目标 agent 上有两条 run：`role='subgoal'`（有 `sub_goal_id`）与 `role='consult'`（无 `sub_goal_id`）。

**为什么去重机制拦不住**

1. `ParseMentions` 只在**单条评论内**去重：
```go
82: func ParseMentions(content string) []Mention {
88:	key := mention.Type + ":" + mention.ID
89:	if _, dup := seen[key]; dup { continue }
```
两条评论之间**没有**幂等键，**没有**时间窗合并——`Create` 里也查不到任何"最近 N 秒同目标已派发"的逻辑（全文无 `dedupe`/`window`/`idempotent` 概念）。
2. 入队合并键带 role（`run.go:196-198`）：
```go
196:		SELECT id, status FROM run
197:		 WHERE goal_id=? AND agent_id=? AND role=?
198:		   AND status IN ('queued','running') ORDER BY queued_at LIMIT 1
```
`consult ≠ subgoal` ⇒ **必然 miss**。这不是 bug 而是刻意的：`:186-194` 的注释说明"跨 role 合并是正确性问题"（把 consult 合进 owner 会让只读问题获得写权限）。所以**只能在角色之上补一层"工作项幂等"，不能放松 role 键**。
3. subgoal 侧的合并键是 per sub_goal（`run.go:412-460`：`WHERE sub_goal_id=? AND role='subgoal'`）——两条 sub_goal 对同一 agent 也合不上。
4. Claim 的串行规则（`run.go:679-684`）：
```go
AND NOT EXISTS (SELECT 1 FROM run r2 WHERE r2.status='running'
  AND ((r.role='owner' AND r2.role='owner' AND r2.goal_id != '' AND r2.goal_id = r.goal_id)
       OR (r.sub_goal_id != '' AND r2.sub_goal_id = r.sub_goal_id)))
```
subgoal 与 consult 都不是 owner、`sub_goal_id` 也不同 ⇒ **两条 run 同时被 Claim**。`agent.max_concurrent` 默认 **3**（`agent.go:96-97`；worker 信号量 `daemon.go:212`），所以槽位也不拦。

**为什么 agent 会两条都发**：`roleContract("owner")` 把两个动作并排写进了同一条契约（`internal/daemon/team_context.go:249-256`）：

```
dispatch them: `agentwork subgoal create --title T --assignee <agent-id>`
Ask teammates (read-only consult) by commenting a mention:
`agentwork goal comment --text "[@Name](mention://agent/<id>)"`
```

而 `agentwork goal comment` 在 CLI 里**是走派发路径**的（`cmd/agentwork-cli/main.go:396-420` → RPC `goal.comment` `server.go:544-562` → `CommentService.Create(..., dispatch=true)`；HTTP 的 `POST comments` 同样 `h.Comment.Create`，`handler.go:458-465`）。DESIGN 里"mention 不再承担任务委派"（决策 5-2）在代码上没有护栏——模板却在教 agent 用 mention 派活。

**本案例量化**

| 阶段 | 重复 run | 秒 | 说明 |
|---|---|---:|---|
| Stage1 Checker | run#5 / #6 | 71 | 两条几乎同时起跑，同样的 release 源扫描 |
| Stage2 Upgrader | run#10 | 1674 | 与 run#9 撞车：两边都清运行环境、争 uv cache 锁；#10 只完成了 venv 构建，协调评论 #10/#11 |
| Stage4 Packager | run#17 | 795 | 前置闸门核验 + 打包重复一遍 |
| **合计** | | **2540** | **≈42.3min，13.4%** |

还要计入未被计量的二次成本：run#9 的 6236s 是在与 #10 撞车的情况下跑完的（清环境两次、抢锁）；以及 4 次 "Run idle for 2m0s" 误报（runtime 侧结论）部分由撞车放大。报告把原因记为"平台对两条 mention 各起一个 run"——**机制描述不准确**：单条评论内的重复 mention 会被 `ParseMentions` 去掉，两条同角色评论会被合并；真正绕过合并的是**角色不同**。

**HEAD 是否已修**：**否**。`subgoal.go`/`comment.go` 停在 2026-09-09（`90f0fa3`），`run.go` 停在 2026-09-10（`ad4f241`）；`git diff 23a7ce1..HEAD` 对这些文件无相关 hunk。

**先做一次事实核对**（5 分钟，决定后续优先级）：
```sql
SELECT id, role, sub_goal_id, status, started_at, finished_at, trigger_comment_id
FROM run WHERE goal_id='f68bd0a5e173da18437daf680a376409'
ORDER BY queued_at;
-- 期望：每段两条——一条 role='subgoal' 且 sub_goal_id!=''（平台派发），
--       一条 role='consult' 且 sub_goal_id=''（leader 的 mention 评论）。
SELECT id, author_type, author_id, substr(content,1,80), created_at
FROM comment WHERE goal_id='f68bd0a5e173da18437daf680a376409'
  AND created_at BETWEEN '2026-11-13T08:19:00Z' AND '2026-11-13T08:24:30Z';
```

**修复方案**

改 `internal/service/run.go` 的 `enqueueTx`（唯一收口点，所有通道都经过它）：

1. 在 role 精确合并**之前**，加一层"工作项幂等"：
   - 给 `run` 增加 `dedupe_ref TEXT NOT NULL DEFAULT ''`（建议复用 `sub_goal_id`：consult run 若命中一个刚派发的工作项，就把它填成那个 sub_goal_id——但**不要**因此改 role，只用于去重与展示）。
   - 新查询：同 `(goal_id, agent_id)`、`dedupe_ref != ''` 或 `role='subgoal'`、`status IN ('queued','running')`、`queued_at >= now - dispatch_window`（建议 120s，做成配置 `dispatch.dedupe_window`）。命中 ⇒ 直接返回该 run + `run:coalesced`（`reason="work_item"`），**不建 consult run**。
2. 派发评论生成 `dedupe_ref`：`CreateSubGoal` 在 `enqueueSubGoalRunTx` 时把 `sub_goal_id` 写进 `run.sub_goal_id`（已有），并在评论上带一个结构化标记（建议 `comment.meta` 或 content 前缀 `<!--dispatch:subgoal=<id>-->`，供人与平台区分"这条是平台代发的"）。
3. `CommentService.create` 的 mention 循环里，对 `case "agent"` 先查同一目标在窗口内是否有刚派发的 subgoal run；有则**只落评论**（记 `activity_log` "dispatch deduped"），不起 consult。
4. 模板侧（`team_context.go:249-256`）：明确"派发用 `subgoal create`，它对目标 agent 就是通知；不要再补一条 mention 评论；mention 只用于**问**，且不要问你刚派发的人"。这一条必须和代码护栏同时上——只改模板，长尾模型仍会两条都发。

**测试**（`internal/service`）：
- `TestMentionConsultCoalescesIntoFreshSubGoalRun`：同一 goal，先 `CreateSubGoal(assignee=A)`，紧接一条 agent 评论 mention A ⇒ 断言 run 表只有 1 条、`run:coalesced` 事件、评论仍在 feed。
- `TestConsultStillSpawnsForUnrelatedQuestion`：subgoal 已跑完（verified）后 mention A ⇒ 新建 consult run（窗口/终态都不能误杀）。
- `TestSubGoalDispatchCommentAuthorIsAttributed`：派发评论能反查出 `sub_goal_id`（可观测性）。

**风险**：120s 窗口误合并"真的想另问一句"的场景——用 `--ask`/`consult` 语义区分成本最低的兜底是：**只有触发评论内容与该 sub_goal 的 title/desc 高度重合时才合并**（可用 `strings.Contains` 粗判，别引入模型）。窗口做成配置，默认 120s；合并必须留 activity 记录，否则事后无法解释"为什么没起 run"。

---

### D2 `CreateSubGoal` 完全没有幂等键（同 agent 同标题可建 N 个）｜ **P1（本案例未直接命中，同源隐患）**

**证据**：`subgoal.go:49-138` 只做三项校验——title/assignee 非空、fan-out 上限 20（`:56-66`，`COUNT(*) WHERE status NOT IN ('verified','cancelled','failed')`）、reviewer-only 规则（`:87-105`）。**没有**同 (goal, assignee, title) 的存在性检查。`store/schema.sql` 对 `sub_goal` 只有 `idx_sub_goal_goal(goal_id, created_at)`，**没有唯一索引**。

**后果**：模型重试一次 `create_sub_goal`（工具调用重试/网络重试/agent 自言自语再调一次）就多一个 work item + 一条派发评论 + 一条 subgoal run。两条 subgoal run 的合并键是 per-sub_goal ⇒ 并发跑。本案例每段只有一条 subgoal + 一条 consult，所以是 D1 命中、D2 未命中。

**HEAD**：未修（同上，文件停在 09-09）。

**修复**：
- 服务层：`CreateSubGoal` 事务内先查 `SELECT id FROM sub_goal WHERE goal_id=? AND assignee_id=? AND title=? AND status NOT IN ('verified','cancelled','failed')`，命中即返回已有行（幂等 create，附 `already_exists=true`）。
- DB 兜底：`CREATE UNIQUE INDEX uq_sub_goal_active ON sub_goal(goal_id, assignee_id, title) WHERE status NOT IN ('verified','cancelled','failed')`（SQLite 支持部分索引）。注意 reject 后的 rework 是同一 sub_goal 的 `quality_iteration+1`，不会建新行，所以唯一索引不会挡正常返工。
- **不要**改成"同标题直接拒绝"——模拟重试会拿到错误，正确的幂等语义是"返回同一个东西"。

**测试**：连调两次 `CreateSubGoal` 相同参数 ⇒ 1 行 sub_goal、1 条派发评论、1 条 run；并发两次（goroutine）⇒ 唯一索引生效、第二次拿到 ErrDuplicate 并被服务层转成"返回已有"。

**风险**：不同 agent 同标题（真并行拆活）不受影响（键含 assignee_id）。历史数据里若已有重复，加索引前要先清理（写好 migration 的 dedupe 步骤）。

---

### D3 同一条评论 `@squad` + `@leader agent` ⇒ leader 起两条 run（owner + consult）｜ **P2（潜在）**

**证据**：mention 循环对每个 mention 独立分支，循环内**没有**按"解析后的 agent id"去重：
```go
317:	for _, m := range ParseMentions(c.Content) {
318:		if reopened && g.AssigneeType == m.Type && g.AssigneeID == m.ID { continue }
322:		case "agent":  → EnqueueForMention(...)      // role=consult
347:		case "squad":  → enqueueLeaderRunForMention(...) // isLeader=true → role=owner
```
`enqueueLeaderRunForMention`（`comment.go:448-462`）→ `enqueue(..., isLeader=true, squadID, ...)` ⇒ `resolveRunRole` 走 `isLeader` 分支 ⇒ `owner`；而同一评论里 `mention://agent/<leader>` 走 consult。两条 role 不同 ⇒ 合并 miss；owner vs consult 也不在 Claim 的串行条件里 ⇒ 并发。另外 `:458-460` 的注释明确"mention 触发的 run 可以和当前 assignee 的 run 并发，这是有意的"——对"问"合理，对"整条小队 + 它的 leader"就重复了。

**案例**：用户的建单评论只 mention 了 squad，未命中；但模板鼓励"@小队 + @leader"写法，属于同源隐患。

**修复**：循环内维护 `seenAgent := map[string]bool{}`（squad 分支解析出 leader 后先登记）；命中即跳过。三行代码，测试一条：`TestSquadAndLeaderMentionSpawnsOneRun`。

**风险**：几乎为零（多目标 agent 仍各起一条）。

---

### D4 consult 闭环按"每个 guest 完成"各唤醒一次 requester ｜ **P1（465s）**

**证据**：`goal.go:1116-1141`，任一 guest run 终态就查 `consult_request WHERE guest_run_id=?` 并立即 `EnqueueExistingTx(..., triggerCommentID="")` ⇒ `resolveRunRole` 走 `triggerCommentID == ""` 分支 ⇒ **owner run**：
```go
1141:	//   A COMPLETED run's report is NOT posted by the platform ...
1119:		`SELECT requester_agent_id, requester_run_id FROM consult_request WHERE guest_run_id=?`
1131:		_, runEv, err := s.runSvc.EnqueueExistingTx(ctx, tx, rc.GoalID, requesterAgent, 1, false, "", "", "")
```
N 个 guest ⇒ N 次 owner 唤醒。合并键是 `(goal, requester, 'owner')`，只有两个 guest **同时**终态时才可能合并；实际是串行到达（案例间隔几十秒到几分钟），于是 3 次独立唤醒：**76s + 202s + 187s = 465s ≈ 7.75min**。

owner 会话是续跑的（`daemon.go:1905-1912` 只对 owner/subgoal 给 `priorSession`），所以每次不是全量重建——但仍是"起进程 + 一轮模型 + 注入 consult status（`daemon.go:3051-3055`）"的固定开销，且每次都把 owner 从"等待"切回"思考"，制造 3 次决策抖动。

**修复**：在 `goal.go` 的 resume 前加"是否还有别的 consult 在飞"的判定——注意**必须放在同一事务里**（现在是 `pendingConsults` 计数在 owner 完成时算，位置不同）：
```go
// 在 enqueue 之前的同一 tx 内：
SELECT COUNT(*) FROM run r JOIN consult_request cr ON cr.guest_run_id = r.id
 WHERE r.goal_id=? AND r.role='consult' AND r.status IN ('queued','running')
   AND cr.requester_agent_id=?
```
`>0` ⇒ 本轮不唤醒（最后一个 guest 终态时自然唤醒）。已在 owner 完成路径实现过同样的口径（`goal.go:1213-1220`），直接复用。
可选加强：合并窗口（如 15s 内多个 guest 终态只唤醒一次）——用 `queued_at` 已能实现，不必引入定时器。

**测试**：两个 guest（A 先、B 后）⇒ 只有 B 完成时产生 1 条 resume run；B 永不完成而 A 完成 ⇒ A 不唤醒（可接受：等待语义由 finalization guard 承担，但要有超时回收，属于 runtime）；两个 guest 同时终态 ⇒ 1 条（合并）。

**风险**：如果"最后一个 guest"因 watchdog 被取消而**不**走终态，owner 永远不被唤醒——现有代码对所有终态（completed/failed/cancelled）都 resume，所以只要 guest 一定进终态就安全；guard 已在 `goal.go:1213-1220` 用同样假设，风险不新增。

---

### D5 verify run：1591s 里有多少是"重新发现上下文" ｜ **P1**

**现象**：3 次平台 verify（"Verify this work item"）各 299s / 673s / 619s，共 **1591s ≈ 26.5min**。每次都是 AI SHELL 上的 `role='verify'` run。

**证据链**

1. 触发：`sub_goal` 配了 agent verifier ⇒ subgoal run 完成后停 `verifying` 并建 verify run（`subgoal.go:274-302`）：
```go
278:		if sg.VerifierID != "" {
280:			`UPDATE sub_goal SET status='verifying' WHERE id=? AND status IN ('done','running')`
294:			_, runEv, err := s.runSvc.enqueueVerifyRunTx(ctx, tx, sg.ID)
```
`verifier_id=''` 时走机器侧（`:303-327`：`UPDATE sub_goal SET status='verified'` + `materializeChangeTx`），**零 LLM**——平台本来就有确定性通道。
2. 成本结构：verify run 的 prompt 只有标题+描述：
```go
2962:	case in.verifyRun:
2963:		wakeWho = "the platform"
2964:		wakeContent = "Verify this work item: " + in.title + "\n" + in.desc
```
`assemblePrompt` 的 extras 只对 `subGoalRun` 注入 reject 摘要/conflict（`:3000-3013`）、对 `owner` 注入 consult status（`:3051-3055`）——**verify 什么都不注入**。而机器侧已经把证据算好并落库了（`machine_dispatch.go:834`）：
```go
834:		`UPDATE run SET gates_hit=?, base_ref=?, head_ref=?, evidence=? WHERE id=?`
```
`buildEvidence`（`verify.go:385-399`）= `{diff_stat, changed[], verify, guards, agent}`。**这份 JSON 就在库里，verify run 的 prompt 却没引用。**
3. 无 session 续跑（`daemon.go:1905-1912`）：`priorSession, priorWorkdir = "", ""`，只给 owner/subgoal。注释理由正确（ACP 单写者），所以 verify 必须自己 `goal comments` 拉 feed + `git diff` 判读——这就是 299–673s 的主要来源。verify 的 workspace 是 sub_goal 分支的 detached 快照（`daemon.go:1459-1466`），diff 能算，但 base 要自己找。
4. 返工无上限：reject 时 `quality_iteration+1`（`subgoal.go:520`）没有上限，每 reject 一次又是一条 verify run + 一条 rework subgoal run。案例只各 1 轮，所以是 1591s；一旦频繁 reject 会线性放大（`MaxMentionCycle` 豁免 verify/subgoal，管不到）。

**本案例可削减量**：确定性注入能砍掉的"发现上下文"部分无法从报告里精确拆分，但同类 run 的 consult 侧成本（76–202s，只回答一个问题）说明"起进程 + 读 feed"的底价 ≈ 1–3min ⇒ 3 次 verify 的**下限改善约 180–540s**；若把低风险工作项改成 `verifier_id=''`（机器侧判定），可直接省掉整条 run。

**修复**（`daemon.assemblePrompt` 的 `verifyRun` 分支）：
1. 注入证据包（3 条查询，全部走已有列）：
   - 该 sub_goal 最近一条 `role='subgoal'` 且 `status='completed'` 的 run 的 `evidence`（`diff_stat` / `changed` / `verify` / `guards` / `agent`）与 `head_ref`、`base_ref`；
   - 机器判定结果（`verification_result` 里上一轮 reject 的 summary 已在 `:3000-3013` 注入，保持不变）；
   - 一句话告诉他 diff 的取法：`git diff <base_ref>..HEAD`。
   格式建议 `## Work item evidence (machine-computed, do not re-derive)`。
2. 给 `quality_iteration` 加上限（例如 5），到顶 ⇒ `sub_goal failed` + 升级给人，而不是再起 verify run。
3. 策略侧（不是代码）：**只在"机器判不了"的维度上配 agent verifier**。`verifier_id=''` 的路径已经完整，模板应写明"能写成机器检查的就别配 verifier"。这属于派发策略，和代码护栏一起上才有效。

**测试**：`TestVerifyPromptCarriesWorkItemEvidence`（断言 prompt 含 `diff_stat` 与 base ref，不依赖模型）；`TestQualityIterationCapsAndEscalates`；`TestVerifyRunSkippedWithoutVerifier`（已有机器路径不被破坏）。

**风险**：证据太长会撑爆 prompt——`buildEvidence` 是 JSON，`diff_stat` 有界，`verify`/`guards` 报告可能很长，**必须截断**（例如 4KB/段，超出给 run id 让 agent 自己取）。另外注入内容会改变 verify 的判断依据，需要一次线上对照（先 shadow 注入、不改 verdict 逻辑）。

---

### D6 "协作往返"计数口径错误：正常流水线必然触发警告 ｜ **P2（261s + 行为误导）**

**证据**：
```go
// comment.go:68-71
const ( MaxMentionHints = 4; MaxMentionCycle = 8 )

// daemon.go:2493-2500
`SELECT COUNT(*) FROM run r JOIN comment c ON c.id = r.trigger_comment_id
  WHERE r.goal_id=? AND c.author_type='agent'
    AND r.role NOT IN ('subgoal','verify','review')`
```
```go
// daemon.go:3048-3050
if n, err := d.agentTriggeredRunCount(ctx, q.GoalID); err == nil && n > service.MaxMentionHints {
	extras.WriteString(fmt.Sprintf("\n⚠️ Collaboration warning: agents have handed this task back and forth %d times. Do NOT hand it off again — finish the remaining work yourself, or end your turn and leave it for a human.\n", n))
}
```
它数的是**"agent 评论触发的 run 条数"**——每一次正常派发/询问都 +1，与"同一对 agent 来回"无关。4 段流水线（每段 1 派发 + 1 询问）= 8 ⇒ 第 5 条就报警。案例 run#20 注入的"已往返 5 次"正是这种假阳性。而豁免清单本身也说明口径是近似的（注释 `:2488-2492` 自称"与 service.MentionCycleCount 同一查询"）。

**二级问题（同一处）**：平台的派发评论以 **leader** 身份落库（`subgoal.go:174-178`），所以 `c.author_type='agent'` 为真；只因为 `role='subgoal'` 被豁免才没再计数。换句话说，"谁派发的"在数据层是**署名伪造**——审计、通知归属、pingpong 统计都受影响。

**量化**：run#20 的 261s 里含"Reopened + 注入警告"后的重规划；更贵的是**行为后果**——警告说"自己干完"，案例后续 run#21–23 确实是 AI SHELL 自己做了规范分析/重打包/必要性分析（305+1246+103s）。不能证明因果，但方向明确：警告在教 owner 绕过小队。

**修复**：
1. 改成"边"统计：`SELECT COUNT(*) FROM (SELECT c.author_id, r.agent_id FROM run r JOIN comment c ON c.id=r.trigger_comment_id WHERE r.goal_id=? AND c.author_type='agent' AND r.role NOT IN ('subgoal','verify','review') GROUP BY c.author_id, r.agent_id HAVING COUNT(*) > 1)`，阈值仍 4，但只在"同一对人 ≥2 次"时累计；文案改成"you and <agent> have handed this back and forth N times".
2. 派发评论改成 `author_type='system'` + `author_id=''`，把 leader 写进 payload（例如 content 里已有 `@leader` 语义，或用新列 `comment.dispatch_actor`）。注意：改成 system 会**改变 pingpong 计数**（现在靠 role 豁免，改后自然不计），也会改变前端头像/署名——需要一次 UI 对齐。
3. 文案里加"如果你已经派发过，别再重复派发；不要把工作收回自己做"——现在的文案在鼓励反模式。

**测试**：`TestPingpongCountsEdgesNotRuns`（3 条不同目标的派发 ⇒ 0；同一对 5 次 ⇒ 5 并触发）；`TestDispatchCommentIsSystemAuthored`。

**风险**：改 author_type 会影响 `MentionCycleCount` 的既有语义（`comment.go:375-382` 同样按键 `role NOT IN (...)` + `author_type='agent'`），两个查询要**同时**改，否则 hint 与 hard-stop 分裂（代码里已有"keep them in lockstep"的注释，正好是这次要遵守的）。

---

### D7 启动失败 = 工作失败：3 次 404 烧完 attempt → failed → 25min 人等待 ｜ **P2**

**证据**：
- 失败重试：`goal.go:1396-1429`，`rc.Attempt < maxAttempts(3)` ⇒ `EnqueueExistingTx(... attempt+1 ...)` 且 `queued_at = now + retryBackoffFor(attempt)`（30s / 60s，`goal.go:99-108`）。
- 退避是有效的：Claim 有 `AND r.queued_at <= ?`（`run.go:677`）——所以 3 次独立启动（5s/2s/4s）更可能是**同一 run 内的 spawn 重试**或端点探活，而不是 owner-retry 链（retry 之间至少隔 30s）。这点需要 runtime 侧确认，但**结论不变**：文本上 3 次快速失败后 goal 进 failed。
- 端点 404 属于**平台配置故障**，却计入 agent 的 attempt 预算，并让 goal 以"agent 失败"的姿态终结（`goal.go:1443-1446` 发 `goal:finished status=failed`），之后需要人点 Reopen（`goal.go:1871-1946`，`activity_log action='reopened'`，作者 `human`）——案例 15:53 → 16:18 的 **25 分钟空档**就是这么来的。

**修复**（分类点是 `daemon.failRun`，与 runtime 分析员重叠，这里只给派发侧口径）：
1. 区分"零模型轮次的失败"：`session_id == ''` 且 duration < 30s ⇒ 记 `run.cancel_reason='infra'`（新增枚举），**不消耗 attempt**，不走 `goal.go` 的 failed 分支；改为平台告警（notify "运行环境不可用"）+ 有界重试（指数退避到 10min，最多 N 次）。
2. goal 侧：infra 失败保持 `active`，置 `goal.attention='user_action'`（已有枚举，`goal.go` Attention 定义），这样"平台坏了"和"活干砸了"在 UI 上可区分，不需要人 Reopen。
3. 端点探活前置：`runtime` 表已有 `status='active|absent'` 与 machine 探针（Claim 的 `rt.status != 'absent'`）。把"端点可连"纳入 claim 前置检查（一次 HTTP/TCP 探活，失败则整批 run 不 claim），比让 agent 起三次便宜得多。

**测试**：`TestInfraFailureDoesNotConsumeAttempt`（attempt 不变、goal 仍 active、有 goal 级 infra 事件）；`TestInfraFailureEscalatesAfterN`。

**风险**：把真实 agent 崩溃误判成 infra（用 `session_id==''` 卡住大部分误判）；告警需要限流，否则一个死端点会刷屏。

---

## 三、看似问题、其实是设计取舍（不该按"重复 run"改）

| 现象 | 为什么不改 |
|---|---|
| 合并键含 role，跨角色不合并（`run.go:186-198`） | **正确**。把 consult 合进 owner 会让"只读询问"获得写权限与所有权，是权限模型漏洞。要补的是"工作项"维度（D1），不是拆 role。 |
| running 行被合并时**不**刷新 `queued_at`（`:205-219`） | 正确。prompt 在 Claim 时已组装，刷新等于"让已开始的 run 重读一个不存在的信号"；注释写得很清楚。 |
| consult/review/verify 与 owner 并发执行（`run.go:644-645`、`comment.go:458-460`） | 设计如此（只读快照）。它们跑在 detached worktree（`daemon.go:1459-1466`），无法污染分支。**但**环境副作用没有隔离——案例 run#12 在 consult 形态下做了 clean/deploy（如果核对确认是 consult），那是"只读契约没有强制手段"，不是一个并发 bug。 |
| `@all` 与 terminal/backlog 静默不触发（`comment.go:298-303`） | 正确。`@all` 只通知人；终态 goal 不接受无声新活。 |
| human 回复 agent 评论时提前 return，避免 mention 二次派发（`comment.go:288-291`） | 这本身就是一个已修的重复派发点，不要动。 |
| reopen 时跳过对当前 assignee 的 mention（`comment.go:318-321`） | 正确：reopen 的 owner run 已经带着这条评论。 |
| verify 是 LLM run（而非纯机器） | 设计如此（`subgoal.go:274-302`：机器判定是 gate，agent verifier 是**质量**判定，两层）。要改的是**成本**（D5 注入证据 + 策略上少配 verifier），不是取消这层。 |
| `MaxMentionCycle=8` 硬停 + 把 goal 判 failed | 正确（防死循环）。问题在 hint 的**计数口径**（D6），不在硬停。 |
| `maxActiveSubGoals=20` 只做数量上限 | 上限本身正确（防 work farm）；缺的是"同工作项幂等"（D2），两件事。 |

---

## 四、汇总与优先级

| ID | 严重度 | 一句话 | 本案例量化 | HEAD |
|---|---|---|---|---|
| D1 | P0 | subgoal 派发 + mention 评论双通道，无跨通道幂等 | 2540s / 42.3min / 13.4% | 未修 |
| D2 | P1 | `CreateSubGoal` 无幂等键、schema 无唯一索引 | 本案例未命中（0s），同源隐患 | 未修 |
| D3 | P2 | `@squad` + `@leader` 同条评论 ⇒ 两条 leader run | 本案例未命中，潜在 | 未修 |
| D4 | P1 | consult 闭环逐 guest 唤醒 owner | 465s / 7.75min | 未修 |
| D5 | P1 | verify run 零证据注入、零 session、返工无上限 | 1591s / 26.5min（+返工风险） | 未修 |
| D6 | P2 | 协作往返计数口径错 + 派发评论署名伪造 | 261s + 行为误导 | 未修 |
| D7 | P2 | 启动失败计入 agent attempt ⇒ failed ⇒ 等人 | 25min 墙钟 | 未修 |

**加总**：D1 + D4 + D5 + D6 = **4857s ≈ 81min（25.6%）**，与 `timeline.md` 的 dup+meta 口径一致。若 D1 的核对显示 run#12 也是 consult（4554s 重叠），派发侧最大单笔可达 **4562s（24.1%）**，需要在 D1 修复里一并处理"忙碌 worker 上的只读询问"——建议二选一：① consult run 若目标 agent 正在跑写角色 run，则**延后到其结束**再 claim（用 `queued_at` 排序天然做到，只需在 Claim 里加一条 `NOT EXISTS (running 的写 run 同 agent 同 goal)`）；② 直接把询问投递为 feed 评论，等 owner 下一轮读（零 run）。①更保守，②更省。

**最小可交付修复集**（按投入产出）：
1. D1 的 `enqueueTx` 工作项去重 + 模板文案（约 80 行 + 3 个测试）——砍 2540s 量级。
2. D5 的 verify prompt 证据注入 + `quality_iteration` 上限（约 60 行）——砍 1591s 的一部分并封住放大。
3. D4 的 resume 前 pending-consult 判定（约 15 行，复用已有查询）——砍 465s。
4. D6 的边计数 + 文案（约 30 行）——去掉假阳性。
5. D7 的 infra 失败分类（跨 runtime，需要与 runtime 分析员合稿）。

**验证方式**：修复后在同一 goal 上重放 4 段派发（Checker→Upgrader→Verifier→Packager），断言 `SELECT role, COUNT(*) FROM run GROUP BY role` 中每段 subgoal 1 条、无 consult dup；断言 verify prompt 含 `diff_stat`；断言 collaboraration warning 在 3 段流水线不出现。目标对齐 `README.md`：总计算 ≤3h、元 run <3%、零重复 run。

---

## 附：HEAD 判定证据

```
$ git log -1 --date=short --pretty='%h %ad %s' -- internal/service/comment.go internal/service/subgoal.go
90f0fa3 2026-09-09 fix(collab): retire platform-extracted result_summary — agent communicates via goal comment

$ git log -1 --date=short --pretty='%h %ad %s' -- internal/service/run.go
ad4f241 2026-09-10 fix(feed): always write creation comment; schedule-fired goals get one too

$ git log -S'Collaboration warning' --date=short --pretty='%h %ad %s' -- internal/daemon/daemon.go
8c906c1 2026-08-13 feat: v2 collaboration model — sub-goals, Change/Revision integration, Goal Coordinator

$ git log -1 --date=short --pretty='%h %ad %s' 23a7ce1      # 部署基线（2026-11-03）
23a7ce1 2026-11-03 优化启动端口替换成了低频端口，优化一下UI的全宽表示

$ git diff 23a7ce1..HEAD -- internal/service/goal.go | grep '^[+-]' | grep -c category
（仅 category/hootl/connection 相关 hunk；retry、consult 闭环、Reopen 无改动）
```