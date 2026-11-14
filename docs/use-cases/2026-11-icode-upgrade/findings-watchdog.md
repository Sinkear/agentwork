# 看门狗 / 自动调度闸门 / 交付件持久化 / 验收口径 — 源码核对与治理结论

- 分析对象：`/home/sinkear/developer/agentwork`，HEAD = `9931759`（2026-11-13 14:17）。
- 生产部署二进制：以 `f0c7563`（2026-11-03 20:10）/ `23a7ce1`（11-03 21:27）为近似构建点。
- 部署版 vs HEAD 的判定方法：对每个论点做 `git blame` + `git merge-base --is-ancestor`，结论写在每项的「HEAD 是否已修」里。
- 不在本文件范围（已由他人负责）：comment mention 派发去重（A2）、run 预算/续跑（runtime-analyst）。涉及这两处的联动只在必要时引用，不展开。
- 只读分析，未修改任何源码。本文件是唯一写入。

**部署版判定的直接证据**：本案例 4 条 `Run idle for 2m0s` 文案只存在于 `7cb954d`（2026-08-31，`fix(watchdog): stop auto-cancelling idle runs, notify instead`）之后。`git show 7cb954d^:internal/daemon/machine_dispatch.go | grep "Run idle"` 为空，即旧版 idle 分支直接取消、不发评论；`7cb954d` 是 `f0c7563`、`23a7ce1`、`HEAD` 的共同祖先。所以**生产上运行的已经是"只通知、不取消"的新看门狗**，不是"老版本自动取消"。这一点决定了下面 A3 的归因。

---

## 一、一页结论（按影响排序）

| ID | 结论（一句话） | 严重度 | HEAD 状态 |
|---|---|---|---|
| **W1** | scratch 域（本案例就是）的平台验收是**零机器校验**：run 完成跳过全部 checks/guards/gates，交付审批"按汇报验收"直接 delivered。A7（1.0.3 未按严格 spec）与 A8（三轴版本错位）平台没有任何机会发现——这是同一个根因的两个表现。 | **P1** | 未修（`a7f584a3` 08-14 / `11bcf2eb` 08-16 至今） |
| **W2** | 交付证据默认不落盘：平台已提供持久 goal 目录，但没有报告路径约定、没有 `AGENTWORK_*DIR` 环境变量、`subgoal verify --evidence` 只收内联字符串、worker 的 scratch 交付件没有上传通道。A9 的 /tmp 丢失是机制性结果，不是偶然。 | **P1** | 未修 |
| **W3** | 机器执行模型下 scratch 交付目录只在**执行机磁盘**上；跨机器/容器重建即丢。且固定块里打印的是 daemon 侧算出的绝对路径，跨主机部署时该路径在执行机上不存在。 | **P1**（结构性风险） | 未修 |
| **W4** | idle 看门狗的时间戳是"最后一批 ACP 事件"，无 run 级心跳；任何 >2min 无输出的命令（`uv sync` 183 包）必然误报。A3 的 4 条告警是**设计内噪声**；idle 评论走 `CreateNoDispatch`，**不会**再触发 consult/wake/二次 run，"据此重复调度"在代码层不成立。 | P2 | 未修（通知行为本身就是 08-31 修复后的设计） |
| **W5** | Collaboration Warning 注入**不区分 run 角色**（verify/subgoal 也会收到"不要再派发"），计数把 consult 算进去；它只是 prompt 文本，没有任何拦截。A6 里"停止派发"是模型自律，不是平台闸门。 | P2 | 未修（`11bcf2eb` 08-16） |
| **W6** | verify run 结束但没调用 `verify_sub_goal` 时，`ReconcileVerifyRun` 只盖 `reconciled_at` 章，sub_goal 永久停在 `verifying`，无 sweeper/重派/告警。 | P2 | 未修（`8c906c14` 08-13） |
| **W7** | 平台不校验 verifier 独立性：`CreateSubGoal` 允许 `verifier_id == assignee_id`（连自己验自己都行）。本案例 verify run 由 owner（AI SHELL）执行，独立 Verifier agent 只被当成普通 worker 用。squad reviewer 有"玩家-裁判"校验，verifier 字段没有。 | P2 | 未修 |
| **W8** | A8 的"packageVersion 与模板注册表对账"**不适合**放在平台做：平台没有 goal.template/version 落库（`template_apply.go` 只解析不落库，schema `goal` 无该列），三轴版本全部在制品/skill 域内。正确层是"把 skill 的严格一致性检查编译进 domain checks，由平台执行并留证"（依赖 W1）。 | P2 | 未修（也没有数据基础） |

---

## 二、逐项分析

### W1 [P1] scratch 域交付验收 = 无校验（A7 / A8 的平台侧根因）

**证据**

1. `internal/daemon/machine_dispatch.go:697-707`，机器执行 run 的统一完成处理：

```go
func (d *Daemon) processMachineRunCompletion(ctx context.Context, runID, agentSummary, headSHA string) string {
	...
	if domainType == "scratch" || role == "consult" || role == "review" {
		return "" // scratch: no diff; read-only roles: nothing to verify or gate
	}
```

即 scratch 域 owner/subgoal 的完成**直接返回**：不跑 `checks.Setup/Verify`、不跑 guards、不评 gates、`buildEvidence` 不调用、`run.evidence/refs` 不落库。平台接受 agent 的 `completed` 自证。

2. `internal/daemon/deliver.go:102-105`，审批后交付：

```go
	if domainType == "scratch" {
		d.finishDeliver(ctx, goalID, true, "无仓库交付——产物在项目目录，按汇报验收", nil)
		return
	}
```

连"报告文件是否存在"都不查，`finished=true` 直接落账。

3. 为什么这是设计而非失误：`internal/store/schema.sql:33` 把 scratch 定义为"无仓库：持久项目目录，交付物=汇报；人卡点强制"；`p0_regression_test.go:473-505` 断言 scratch 无论 strength/gates 都必须 park review。设计意图是"汇报即交付 + 人必看"，但**人看到的是 agent 自述，不是平台证据**。

**本案例影响**

- A7：Packager（run#18）用 `--env` 存量视角跑了 spec 扫描、把许可证归因项降级放行，出了 1.0.3；平台没有任何 verify 命令在跑，AI SHELL 的 verify run 也只能读 Packager 自述/报告，于是"通过"。
- A8：三轴版本错位（JEM 注册表 1.0.2 / env.packageVersion 1.0.3 / 磁盘 1.0.4）最终是**人工补验**跑 zz.009 才发现。平台交付时既没有版本一致性证据，也没有"检查未跑"的告警。
- 这两项是本案例唯一触达"对外制品"的风险（1.0.3 差点被发布），所以排在影响第一。

**HEAD 是否已修**：否。`git blame`：`deliver.go:102` = `a7f584a3`（2026-08-14），`machine_dispatch.go:705` = `11bcf2eb`（2026-08-16），均早于部署构建，部署版与 HEAD 行为一致。

**修复方案（最合适的一层：domain 验收策略；执行侧在机器）**

不能把 spec 写进平台（平台不认识 icode/JEM），正确做法是**让 scratch 域也能声明机器验收命令，并由平台（不是 Packager agent）执行**：

1. `internal/daemon/machine_dispatch.go:697` 拆分支：
   ```go
   if domainType == "scratch" {
       return d.processScratchRunCompletion(ctx, runID, role, goalID, domainID) // owner 才跑
   }
   if role == "consult" || role == "review" { return "" }
   ```
2. 由于 scratch 目录在**执行机**而 checks 在 daemon，验收必须放到执行侧：`internal/daemon/daemon.go:1945` 的 dispatch 参数新增 `ScratchChecks`（仅 scratch owner run 下发 `checks.Setup/Verify` + `verify_timeout`，不下发 guards/gates——它们需要域 diff，scratch 无 diff）；`cmd/agentwork-cli/exec.go` 在 agent 进程退出、`status=="completed"` 后，**由 CLI 自身**（agent 不可见）在 `workdir` 顺序执行 setup/verify，写 `reports/verify-evidence.json`（命令、退出码、耗时、workdir），作为 `ArtifactFiles` 上传；任一退出码非 0 则把 run 置 `failed`（summary=`machine verification failed: <cmd>`），走既有失败/重试链。这是把 repo 域"daemon 算、goal 层判"的影子模型搬到 scratch，执行主体从 daemon 换成 CLI。
3. `internal/daemon/deliver.go:102` 收紧：当 domain 冻结了 `checks.Verify`（`checks_compiled_at != ''` 且 Verify 非空）而 owner run 的 `evidence` 为空/失败 → `finishDeliver(ctx, goalID, false, "scratch 交付缺少机器验收证据（verify 未跑或未通过）")`，并 park review 让人决定"重跑验收"或"接受例外"。
4. A8 的正确落点：把 zz.009/严格 spec 扫描这类脚本作为**该 packaging domain 的 `checks.Verify` 条目**（如 `python3 scripts/zz_scan.py --strict --metadata`），而不是让 Packager 在对话里选口径。这样"用 --env 宽松口径"这种降级在平台层根本不可能发生。A7 的许可证三件套同理变成 verify 脚本的断言（`test -f runtime/THIRD_PARTY_NOTICES` 等）。
5. 测试：
   - exec 侧新增 `TestScratchVerifyRunsOutsideAgent`（fake workdir，verify 失败 → status failed，evidence JSON 正确）；
   - daemon 侧 `TestScratchDeliverRequiresEvidence`（有 Verify 但 evidence 空 → finishDeliver false；有 passed evidence → true）；
   - `TestScratchWithoutChecksStillDeliversByReport`（无 Verify 的 research 域保持现行为，防回归）。
6. 风险：
   - 现有 scratch 域（如调研）大多没有 Verify，行为不变；只在配置了 Verify 的域生效，所以默认安全。
   - scratch 的 setup 可能很贵（本案例 `uv sync` 183 包），`verify_timeout=600s` 默认值不够；建议 scratch 的 setup 不跑、只跑 verify（setup 归 agent/模板），或让域显式配置 setup。
   - 把 checks 下发到执行机会让"agent 理论上可读到 dispatch 载荷"的暴露面变大；只走 CLI 内存 + 不写盘、不入 env，可接受；真正的隔离要靠后续权限模型。

---

### W2 [P1] 审计证据默认不落盘（A9）

**证据**

1. 持久目录**已经存在**，但只是"告知"，不是"约定"，更不是"校验"：
   - `internal/paths/paths.go:95-98`：`ScratchGoal(name, goalID) = runs/scratch/<name>/goals/<goalID>`；
   - `internal/service/util.go:179-181` 注释："the branch analog: the goal's durable state lives HERE across runs"；
   - `cmd/agentwork-cli/exec.go:715-722`：执行机的 cwd 就是这个目录（`os.MkdirAll(base, 0o755)`）。
2. 平台注入的固定块只写了"产物在这个目录、会跨轮存活"，没有报告子路径、没有"不要写 /tmp"：
   - `internal/daemon/team_context.go:71-78`：
     ```go
     b.WriteString("This is a scratch project (no git repository): your artifacts live in\n")
     b.WriteString("the project directory (" + scratchGoalDir(domainName, goalID) + ") and\n")
     b.WriteString("survive between turns; the comment feed is only for coordination. The\n")
     b.WriteString("PARENT directory is user-maintained shared material: READ-ONLY. Write\n")
     b.WriteString("only inside this goal's directory.\n")
     ```
3. 环境变量只有身份，没有目录：`cmd/agentwork-cli/exec.go:739-743` 仅 `AGENTWORK_SERVER_URL/GOAL_ID/RUN_ID/AGENT_ID/TOKEN`。
4. verify 的 evidence 只能内联：`cmd/agentwork-cli/collab.go:78` `fs.String("evidence", "", "evidence (command output etc.)")`，服务端 `internal/service/subgoal.go:483-487` 写 `verification_result.evidence`。报告文件本身不会被收进证据，只能贴一段文字或写一个 `/tmp/...` 路径——任务结束即悬空。
5. worker 的 scratch 交付件没有上传通道；现有 `Artifacts` 通道只服务 processor/import/digest：
   - `internal/link/link.go:284` `ArtifactFiles []string`，`:505-508` `Artifacts map[string]string`；
   - `cmd/agentwork-cli/exec.go:431-438` 在 `status=="completed"` 时按 `p.ArtifactFiles` 从 workdir 读文件上传；
   - 但 `ArtifactFiles` 只有三处设置：`daemon.go:1949/2035`（digest/import/compile）与 `intake.go:74`（intake），普通 worker 恒为 nil。
   - digest 是现成范式：`internal/service/digest_seed.go:45-47` `DigestArtifactFiles = []string{"manifest.json", "1.md", ...}`，注释"Fixed names — the executor's prompt mandates them"。

**本案例影响**

A9：Checker 的 `upgrades.json`（`/tmp/job-env-upgrade-checker/`）、Upgrader 的 `icode-snapshot.json`（`/tmp/job-env-uvgrade/`）、Verifier 首轮报告（`/tmp/job-env-artifact-verifier/reports/`）、Packager 的 `artifact-spec-report.json`（`/tmp/`）全部丢失。最终只能靠本轮人工补验重跑一遍（§6，105s，`agentwork_runs: 0`）——"测试内容等价、治理不等价"正是这个缺口。平台侧只留下了 28 条交接评论，**报告内容本身不可复核**。

/tmp 的机制性原因：平台没有指定报告路径、没给目录环境变量、证据只收内联字符串；而 skill/模板脚本自带 `/tmp/<skill-name>/` 约定（本仓库无该脚本，来自 agentwork-templates 的 job-env 系列），agent 在"技能说的路径"和"固定块说的目录"之间选择了前者。**不是平台把 /tmp 当默认，也不是容器 /tmp**：`exec.go` 直接 spawn agent（全仓无 docker/container 运行时），cwd 就是 goal 目录。

**HEAD 是否已修**：否（`team_context.go:71` 08-16；`collab.go` evidence 参数自引入未变）。

**修复方案（小改动、高收益）**

1. `cmd/agentwork-cli/exec.go:736-745`（`buildEnv`）在 `scratchWorkdir` 解析后注入：
   ```go
   env = append(env, "AGENTWORK_WORKDIR="+workdir,
                    "AGENTWORK_GOAL_DIR="+goaldir,        // repo 域为 worktree 根
                    "AGENTWORK_REPORT_DIR="+filepath.Join(workdir, "reports"))
   ```
   （`buildEnv` 目前拿得到 `workdir` 参数，scratch 的 `workdir` 就是 goal 目录；repo 域可给 `reports/` 留空，避免污染仓库。）
2. `internal/daemon/team_context.go:71-78` 固定块加硬规则：
   "Write every report/evidence file under `AGENTWORK_REPORT_DIR` (i.e. `<goalDir>/reports/`). Files under `/tmp`, `/var/tmp`, or outside the goal directory are NOT durable and will be treated as missing evidence."
3. CLI 增加 `subgoal verify --evidence-file <path>`：读文件（上限 64KB，超限截断并注明），与 `--evidence` 互斥；`internal/server/server.go:707` 已有透传。这样 Verifier 的报告正文直接进 `verification_result.evidence`，即使文件丢了也有结构化证据。
4. worker 的 scratch 交付走 digest 通道：daemon 在 dispatch 时给 scratch owner run 设 `ArtifactFiles: []string{"reports/manifest.json"}`（固定名，模板/固定块强制），`exec.go:431` 天然支持；daemon 在 `IngestRunFinished` 里把 manifest 落到 daemon 侧 `runs/scratch/<name>/goals/<id>/reports/`（或 DB），实现跨机器可审计的一小份"收据"。manifest 只放小文件清单/sha256/版本三元组，不放大制品（74MB tar 仍留在 `/root/jobEnvPackage/` 或走 release）。
5. 模板侧（另一个仓库）必须同步把 job-env 系列脚本的输出根从 `/tmp` 改为 `${AGENTWORK_REPORT_DIR:-$PWD/reports}`；平台改不了这部分，建议在 templates 仓库开一条对应 issue。
6. 测试：`exec_test`（env 与 manifest 上传）、`team_context` 固定块含新规、`collab_test`（evidence-file 读入与截断）、`daemon` 落盘路径。
7. 风险：manifest 缺失会让旧 scratch 域在交付时被"证据缺失"拦住 → 用"域配置 + 默认警告"两档：默认只写告警评论，`verification_strength=strong` 或显式配置才硬拦。

---

### W3 [P1，结构性] 跨机器/容器：scratch 交付目录不可迁移；固定块路径可能是错的

**证据**

1. scratch 目录是**执行机本地**的：`cmd/agentwork-cli/exec.go:715-722` 用 `paths.ScratchGoal(...)`（`paths.Runs()` 基于本机 home），daemon 的 `scratchGoalDir`（`daemon.go:1262-1266`）只在 daemon 本机有意义。worker run 全部 dispatch 到 machine（`daemon.go:1960-1963`："every run dispatches to its machine ... Legacy transports have no executor anymore"），不存在 daemon 本地执行路径。
2. 固定块里的路径是 **daemon 侧**算的：`team_context.go:75` `scratchGoalDir(domainName, goalID)` → `daemon.go:1258-1265` → `paths.Runs()`。若执行机与 daemon 不同主机/不同 home，这段 prompt 里的绝对路径在执行机上不存在。本案例能落到 `…/goals/f68bd0a5…/reports/` 说明是同机（或同 home），但这个前提没有任何地方被断言。
3. 删除也只在 daemon 侧：`daemon.go:774-780` 删 goal 时 `RemoveAll(scratchGoalDir)`；执行机上的目录没人清理也没人收集（与 repo 域不同：repo 靠 `git push` 回共享仓，scratch 没有等价通道）。

**本案例影响**：未直接触发（同机、目录存活，产物 1.0.4 三件套在 `/root/jobEnvPackage/`）。但"换个执行机/容器重建"就会重演 A9，且更彻底——连 `/root/jobEnvPackage/` 都可能没了。这是平台侧治理上的最大单点：**scratch 交付件没有任何离开执行机的通道**。

**HEAD 是否已修**：否。

**修复方案**

1. 路径不写死：`team_context.go:75` 改为不渲染绝对路径，只说"当前工作目录（`AGENTWORK_WORKDIR`）就是 goal 目录，`AGENTWORK_REPORT_DIR` 是报告目录"；或由 `exec.go` 在合并 `AGENTS.md`（run profile）时把 `{{AGENTWORK_WORKDIR}}` 占位替换成执行机真实路径。改 `exec.go` 的 profile 合并点即可，根除跨机错路径。
2. 小报告跨机：W2 的 manifest 上传即为最小方案；若需要完整目录镜像，复用 `Artifacts` 通道做"reports 目录打包上传（≤N MB）"，daemon 落到自己的 `runs/scratch/<name>/goals/<id>/reports/`；大制品不镜像，明确"制品发布走 release/CDN 或对象存储"。
3. 域级约束：scratch domain 可声明"必须单机执行"（已有 `runtime.machine_id` 机制，但 domain/goal 没有 pin）。最小可行：交付卡上显示"交付目录所在机器"，跨机时提示"未镜像"。
4. 测试：profile 占位替换单测；manifest 跨机落盘测试（daemon 侧 fake machine artifacts）。
5. 风险：镜像会引入大小/权限问题；建议只镜像 `reports/` 且设上限（例如 10MB），失败不阻塞交付但写告警。

---

### W4 [P2] idle 看门狗：误报是设计，且不会二次触发 run（A3 归因修正）

**证据**

1. 窗口定义：`internal/daemon/daemon.go:118-122`
   ```go
   // idleWindow is the no-activity budget after which the idle watchdog notifies
   // the human (it no longer cancels — the silence may be a recoverable rate-limit
   // backoff, not a stuck run). max_run_duration is the platform's only unilateral
   // cancellation authority.
   const idleWindow = 2 * time.Minute
   ```
2. 时间戳来源：`machineLastEvent`，只在**claim** 与**每批事件**刷新：
   - `internal/daemon/machine_dispatch.go:313-316`：`d.machineLastEvent[p.RunID] = time.Now()` + `go d.runMachineWatchdog(p.RunID, mid)`；
   - `machine_dispatch.go:369`：`IngestRunEvents` 每批 `d.machineLastEvent[p.RunID] = time.Now()`；
   - run 终态删除（`:507-511`）。
   评论、工具是否执行完、进程存活、HTTP 心跳都**不参与**判定（`grep heartbeat` 只命中 `cmd/agentwork-cli/connect.go` 的机器链路心跳，没有 run 级心跳）。
3. 触发动作：只发 system 评论，不取消、不重排：
   - `machine_dispatch.go:640-658`：
     ```go
     if ok && time.Since(last) > idleWindow && !*idleNotified {
         ...
         content := fmt.Sprintf("Run idle for %s — possibly stuck or in rate-limit backoff. The platform will not auto-cancel. Stop it manually if needed, otherwise wait for recovery.", idleWindow)
         if _, err := d.commentSvc.CreateNoDispatch(... AuthorType:"system", RunID: runID); err == nil {
             *idleNotified = true // only lock after a successful post
         }
         ...
         return false
     }
     ```
     每个 run 至多一条（`idleNotified` 在 watchdog goroutine 闭包里，`machine_dispatch.go:608-618`）。只有 `max_run_duration > 2h` 才 `UPDATE run SET cancel_reason='timeout'` + 机器 cancel + 2min grace 本地 stamp（`:662-681`）。
4. 评论不会派发：`internal/service/comment.go:125-127` `CreateNoDispatch` → `create(..., false)`；`comment.go:205-207` 在 reopen/mention 之前直接返回：
   ```go
   if !dispatch {
       return &c, nil // pure comment — no reopen, no mention-triggered runs
   }
   ```
   即：既不 Reopen goal，也不 Enqueue consult/owner run。
5. 长命令必然误报：`cmd/agentwork-cli/exec.go:351-396` 的事件泵只在 `len(buf) > 0` 时 flush（100ms ticker）。一个 `uv sync --frozen`（183 包）从 `tool_call` 事件到 `tool_result` 之间可能 5-10 分钟零事件 → `machineLastEvent` 不动 → 2 分钟必发一条。模型 rate-limit backoff 同理（这正是文案里的 "possibly ... backoff"）。

**本案例影响**

- 4 条告警分别落在 16:33:10 / 17:01:25 / 18:23:05 / 18:27:57，对应当时处于 >2min 静默的 run（Upgrader run#9/#10、Packager run#17/#18 两个阶段各有并发 run）。每个 run 至多一条，所以"4 条"不代表同一 run 反复告警；用评论的 `run_id` 可精确归位。
- **"并据此重复调度"A3 的归因不成立**：idle 评论没有 dispatch 副作用；本案例的重复 run 来自 A2（同一派发发两条 mention 评论 → 两个 run）与 consult 自动续跑（guest run 终态 → 给 requester 入队新 owner run，`internal/service/goal.go:1116-1141`）。报告里 16:53:23 / 18:09:25 / 18:14:59 的"平台 consult / 恢复调度续跑"正是后者，与 idle 无关。若仍怀疑，核对 run 的 `trigger_comment_id` 指向哪条评论即可（idle 评论是 system，不会被当成 trigger）。
- 真实损害是**告警噪声与语义误导**：文案说 "possibly stuck"，但实际上 agent 正在跑长命令；人若据此手动 stop 会打断正常任务（本案例没停，只加剧了人对平台的困惑）。

**HEAD 是否已修**：否；但"不取消"的部分已在 `7cb954d`（08-31）修掉且部署版已含（见文首证据）。本项剩下的"误报"是通知策略问题，不是回归。

**修复方案（不加协议、低风险，先做上下文感知的通知）**

1. `internal/daemon/machine_dispatch.go:625` 的 `machineWatchdogTick` 不要只看 `machineLastEvent` 的时间差，改成读**最后一条持久化事件**（`chat_message` 表，`schema.sql:420-424`；工具调用按 CallID 聚合，`persistEvent` 见 `daemon.go:2190-2300`）：
   - 新增 `func (d *Daemon) lastRunActivity(ctx, runID) (kind, tool, callID string, since time.Time)`：`SELECT tool_calls, created_at FROM chat_message WHERE run_id=? ORDER BY created_at DESC LIMIT 1`，解析 event kind。
   - 若最后事件是 `tool_use` 且该 CallID 尚无 `tool_result`：说明"在一条命令里"。
     - `since < toolSilenceBudget`（建议 15min，或 `min(30min, domain.max_run_duration/4)`）→ **不发** idle 评论；
     - 超过预算 → 发一条**带上下文**的评论："Run is inside tool call `bash` (`call_...`) for 23m — long command or hang; not auto-cancelling."，并且每个 open call 只发一次（用 run_id+callID 记录，或直接依赖 `idleNotified` 的"每次 run 一次"上限会漏掉后续；可把 flag 改为 `lastNotifiedCallID`）。
   - 否则（最后事件是文本/思考/工具结果）→ 维持现有 2min 通知，但文案补 "last event: assistant text/result at HH:MM:SS"。
2. 可选（结构性但收益有限）：给 run 加 30s 心跳 RPC（`link.MethodRunHeartbeat` + `IngestRunHeartbeat` 更新 `machineLastEvent`）。注意它的语义陷阱：心跳证明 **CLI 进程**活着，不证明 agent 在推进；agent 卡在工具调用里时心跳照发，会把"该通知"变成"永远不通知"。所以心跳只能作为"机器链路存活"的辅助信号，不能替代上面的工具感知。建议先做 1，观察后再决定是否加 2。
3. 测试：`internal/daemon/watchdog_test.go` 已有 `TestWatchdogIdlePostsCommentAndDoesNotCancel` / `MaxRunDurationCancels` / `TerminalRunStops`。新增：
   - `TestWatchdogSilentLongToolDoesNotNotify`（插入 tool_use 行，10min 无 result → 0 条评论）；
   - `TestWatchdogOpenToolPastBudgetNotifiesOnce`（20min → 恰 1 条，含 tool 名）；
   - `TestWatchdogIdleCommentNeverDispatches`（断言 comment 后 goal 无新 run、无 consult_request）。
4. 风险：
   - `chat_message.created_at` 是首次 INSERT 时间，正好等于工具开始时间；工具运行中会原地 UPDATE `tool_calls`（`daemon.go:2288-2294`），时间戳不变——语义正确。
   - 文本 chunk 是缓冲聚合的（`daemon.go:2200-2220`），最后一行可能滞后于真实思考；对"长文本生成"仍可能误报，但那种场景下 2min 通知本身是合理的（backoff 提示）。
   - 完全去掉长命令误报的代价是"长命令真卡死"也只能靠 max_run_duration 兜底；这是可接受的取舍，因为文案已明确"平台不自动取消"。

---

### W5 [P2] Collaboration Warning：角色无差别注入 + 仅提示（A6）

**证据**

1. 注入点 `internal/daemon/daemon.go:3048-3050`（`assemblePrompt` 内，对所有 runRole 走同一路径）：
   ```go
   if n, err := d.agentTriggeredRunCount(ctx, q.GoalID); err == nil && n > service.MaxMentionHints {
       extras.WriteString(fmt.Sprintf("\n⚠️ Collaboration warning: agents have handed this task back and forth %d times. Do NOT hand it off again — finish the remaining work yourself, or end your turn and leave it for a human.\n", n))
   }
   ```
   `n > MaxMentionHints` 且 `MaxMentionHints = 4`（`internal/service/comment.go:69-70`），即第 5 次起注入。**没有 `if in.runRole == ...` 包裹**，所以 verify run（"Verify this work item"）、subgoal run、consult run 的 prompt 里也会出现"不要再派发"。
2. 计数口径 `internal/daemon/daemon.go:2493-2500`：
   ```sql
   SELECT COUNT(*) FROM run r JOIN comment c ON c.id = r.trigger_comment_id
    WHERE r.goal_id=? AND c.author_type='agent'
      AND r.role NOT IN ('subgoal','verify','review')
   ```
   排除了 subgoal/verify/review，但**把 consult 算在内**，也会把 owner 被 agent 评论重开的 run 算在内。咨询次数在业务上不全是"乒乓"（本案例 Checker/Upgrader/Packager 都是必要的交接 + A2 的重复派发），计数把这两类混在一起。
3. 真正的硬闸门不在这里：
   - mention cycle ≥ `MaxMentionCycle(8)` → `forceMentionCycleFailed` → `UPDATE goal SET status='failed'`（`comment.go:328-331, 397-430`）；
   - 所有权 handoff：`goal.go:115-116` `handoffWarnThreshold=4` / `handoffCycleThreshold=8`，第 4 次写 system 警告评论（`goal.go:650-655`），≥8 次 park review（`goal.go:656-680`）。
   本案例走的是 comment 派发（mention/consult），不是 `goal assign`，所以 handoff 线没触发。
4. 另一处易被忽略：警告注入不改变任何派发行为——`comment.go:317-346` 的 mention enqueue 没有任何 cycle 前置拦截（除了 ≥8 的 goal fail）。

**本案例影响**

A6：18:39 AI SHELL 的 owner run（run#20）收到"已往返 5 次，不要再派发"，此后 08:26 的重打包（run#22）由 AI SHELL 自己执行、不再走小队派发。**从治理效果看"闸门生效了"，但机制是模型服从 prompt，不是平台拦截**——换一个不听话的模型/一次上下文丢失，就会继续乒乓。同时，A6 的副作用是"团队协作模式被绕开"：本该由 Packager 干的严格重打包变成 owner 手工做，独立性下降（并与 W1/W7 叠加）。

**HEAD 是否已修**：否（`11bcf2eb` 08-16，部署版已含）。

**修复方案**

1. 角色收窄（小改）：把 `daemon.go:3048` 的注入放进 `if in.runRole == "owner" || in.runRole == "consult"`；verify/subgoal/review 的 prompt 不应讨论"交接循环"（它们没有派发权，看到这句话只会困惑，甚至可能因此不敢在 verdict 里写问题）。补充测试：`prompt_context_test.go` 增加"verify run 的 prompt 不含 Collaboration warning"。
2. 计数口径分离：新增 `consultsSinceLastHuman(ctx, goalID)`（只数 `consult_request` 且 requester 是 agent、中间无人类评论），或沿用 `agentTriggeredRunCount` 但在 UI/评论里显示"其中 consult N 次 / 重复派发 M 次"。改 `daemon.go:2493-2500` 的 SQL 会牵动 warning 阈值，建议先加一个并列查询，阈值不变。
3. 可选硬闸门（设计取舍，见第三节）：在 `comment.go` 的 agent mention 路径上，当 consult 次数 ≥5 时**不直接 enqueue**，而是把一个 `ask_human` 评论/审批卡挂到人（"第 6 次交接需要人工确认"），人点一次后放行。这比"goal 直接 failed（≥8）"温和，也比纯提示可信。
4. 风险：把 owner 重开 run 和 consult 混计的问题修掉后，阈值 5 的实际触发会变晚（本案例可能 6-7 次才触发）；需要同步调整 `MaxMentionHints` 或保留旧口径做展示、用新口径做闸门。

---

### W6 [P2] verify run 结束但无裁决 → sub_goal 永久 `verifying`

**证据**

1. 触发：`internal/service/subgoal.go:271-301`，subgoal run completed 且 `sg.VerifierID != ""`：
   ```go
   `UPDATE sub_goal SET status='verifying' WHERE id=? AND status IN ('done','running')`
   ...
   _, runEv, err := s.runSvc.enqueueVerifyRunTx(ctx, tx, sg.ID)
   ```
   注：`'done'` 是死分支——`subgoal.go:29` 明确 "pending/done 无写入路径"，实际只有 `'running'` 会命中；真正的去重是"verified 不在 ('done','running') 集合里"。
2. 去重：`internal/service/run.go:490-536` `enqueueVerifyRunTx`，`:507-511` 按 `(sub_goal_id, role='verify', status IN ('queued','running'))` coalesce。同一工作项不会重复 verify——**这部分是好的**。
3. 裁决守卫：`subgoal.go:449-482` 要求 `role=='verify'`、run 仍 running、`agentID == sg.VerifierID`、`sg.Status=='verifying'`；passed → verified + materialize change，rejected → running + quality_iteration+1 + 返工。
4. 缺口：`internal/service/subgoal.go:618-641` `ReconcileVerifyRun`（`goal.go:1061` 在 `rc.Role=="verify"` 时调用）：
   ```go
   if _, err := tx.ExecContext(ctx, `UPDATE run SET reconciled_at=? WHERE id=?`, now(), rc.RunID); err != nil {
       return fmt.Errorf("stamp reconciled_at: %w", err)
   }
   ...
   s.bus.Publish(ctx, events.Event{Topic: "run:discarded", ... "reason": "verify-run"})
   ```
   不检查"有没有裁决"，也没有重派/告警。verify run 若因机器断链、被 stop、agent 忘记调用 `subgoal verify` 而结束，sub_goal 永远 `verifying`（全仓 grep 无 `verifying` 的恢复路径；只有人工重开 run 才能救）。

**本案例影响**：未触发（AI SHELL 每轮都给了 verdict）。但这是 W4 告警"stuck"之下**最可能真实卡死**的位置之一：verifier run 被静默打断，人看到"verifying"却没有任何平台动作。A6 之后若 AI SHELL 不再派发，这类卡死只能靠人发现。

**HEAD 是否已修**：否（`8c906c14` 08-13）。

**修复方案**

1. `internal/service/subgoal.go:618` `ReconcileVerifyRun` 落章后，在**同一事务**里查：
   ```sql
   SELECT status FROM sub_goal WHERE id=?
   ```
   若仍为 `verifying`（说明裁决没落）：
   - run 是 `completed`（agent 正常结束但忘了 verdict）→ 允许一次自动重派：先查 `SELECT COUNT(*) FROM verification_result WHERE sub_goal_id=?`；0 次时 `enqueueVerifyRunTx` 再排一个 verify run（attempt 2），并在评论里写"verifier did not return a verdict — re-queued once"；
   - run 是 `failed/cancelled` 或已重派过 → `UPDATE goal SET status='review', review_request='verify_no_verdict: <sub_goal title>'`（与 handoff park 同构，人决定重试/终止），并写 system 评论。
2. 兜底 sweeper：daemon 启动/定时（`sweep_test.go` 已有框架）扫 `sub_goal.status='verifying'` 且其 verify run 已终态 → 按上一条 park/重派；同时把"verifying 超过 N 分钟"写进 runbook 告警。
3. 测试：`collab_test.go` 新增 `TestVerifyRunWithoutVerdictParksGoal`（finish verify run completed、不调用 verdict → goal review + reason 含 `verify_no_verdict`）；重派路径加 `TestVerifyRunWithoutVerdictRequeuesOnce`。
4. 风险：自动重派会与"agent 只是慢，verdict 在 run 结束前发出"的正常路径竞争——用 `verification_result` 存在性判断即可幂等；重派上限 1 次，避免无限循环。

---

### W7 [P2] verifier 独立性不校验（玩家-裁判）

**证据**

1. `internal/service/subgoal.go:97-113` 有一条很清楚的"玩家-裁判"规则，但只针对 **squad reviewer**：
   ```go
   // REVIEWER ONLY RULE: a squad's reviewer members review — they are never
   // handed work items. ... instead of producing a worker who later reviews
   // its own output (the player-referee trap).
   ```
2. 但 `verifierID` 只做存在性校验（`subgoal.go:106-110`）：
   ```go
   if verifierID != "" {
       if err := mustExist(ctx, s.st, `SELECT COUNT(*) FROM agent WHERE id=?`, verifierID, "verifier agent"); err != nil {
           return nil, err
       }
   }
   ```
   不比较 `verifierID` 与 `assigneeID`，也不比较与 goal owner。照此可以创建"我干活、我验收"的工作项。
3. 本案例的实证：报告 §2/§6 明确"AI SHELL 兼平台触发的 verify/consult run 执行者"——即 goal owner 亲自跑平台 verify run（run#7/#13/#19），独立 Verifier agent（run#15）只被派去跑 34 条 base 用例。最终补验（10:15）甚至是"架构伙伴直跑 skill"，`agentwork_runs: 0`。治理上的"独立验收"在平台结构里没有被强制。

**HEAD 是否已修**：否。

**修复方案**

1. `CreateSubGoal` 增加：`if verifierID != "" && verifierID == assigneeID { return ValidationError("the verifier must be a different agent than the assignee (player-referee)") }`；goal owner 作为 verifier 是否禁止，建议**不禁止**（owner 是集成者、常见且合理），但在 `verification_result` / gate 卡上标注"verified by goal owner"，让"独立性"可见。
2. 进阶（可选）：goal 由 squad 承接时，verifier 默认是 squad 的非 leader 成员（`squad_member.role='reviewer'` 优先）；由 UI 预填，减少 leader 自选。
3. 测试：`collab_test.go` 增加 `TestCreateSubGoalRejectsSelfVerifier`；现有 `TestVerifySubGoalPermission` 保持（verdict 身份校验不变）。
4. 风险：单 agent 的 scratch 调研目标会因"verifier 必须不同"而无法建子任务——所以只拒绝 `verifier==assignee`，不拒绝 `verifier==owner`；单 agent 场景 owner 与 assignee 同一人时仍会被拒，需要显式 `--self-verify` 逃生门（默认关）。

---

### W8 [P2] A8 的"版本三轴对账"不该由平台硬编码，落点在 domain checks

**证据**

1. 模板版本只解析、不落库：`internal/service/template_apply.go:19-30`（`templateSpec.Metadata.Version`）、`:316` 只校验 `api_version`；`internal/service/template.go:43-48` 的 `TemplateMeta.Version` 只在内存缓存里用于导出。schema 的 `goal` 表（`schema.sql:192-226`）没有 `template_id/template_version` 列，`skill` 表（`:142-163`）也没有 version 列。
2. 也就是说 A8 的三轴——JEM 模板注册表 1.0.2（在 icode 环境内部）、`env.packageVersion` 1.0.3（在 env 元数据里）、磁盘模板/新产物 1.0.4——**没有一轴是 AgentWork 平台状态**。平台就算新增 `goal.template_version` 也只能对"目标声称的模板版本"和"goal 声称的产物版本"做对账，碰不到 JEM 注册表。
3. 平台已有的可执行层是 domain checks：`internal/service/domain.go:63-86`（`Setup/Excludes/Verify/Guards/Gates`）；`internal/daemon/verify.go:118-150` `runVerification` 就是逐条执行 `checks.Verify` 并产出 evidence。对 repo 域它已经工作；scratch 域只是被 W1 的早退跳过了。

**本案例影响**：A8 最终由人工补验的 zz.009 发现，平台零参与；A7 的宽松口径同样是"agent 自选脚本参数"，平台零参与。两者都说明：**验收脚本必须由平台执行，参数必须来自域策略**。

**HEAD 是否已修**：否，也不应该靠加列修。

**修复方案**

1. 依赖 W1：把 "严格 spec 扫描（`--metadata` 口径）" 与 "版本一致性（zz.009 等价）" 作为该 packaging domain 的 `checks.Verify` 命令；命令本身由 skill/template 仓库提供（`artifact-upgrader` 的 scanner 增加 `--strict` 默认），平台只负责执行、留证、不通过即失败。
2. 若仍要平台级"账面留痕"（审计用，不作判定）：在 `reports/manifest.json`（W2）里约定 `versions: {artifact, env_package, template_registry}` + 各文件 sha256，交付时存入 DB/活动日志。它是**声明**，不是权威，因此不能替代 verify。
3. 不要做：给 `goal` 加 `template_version` 再"对账"——数据源和执行者都不在平台，会把治理成本转嫁到平台且仍然发现不了 JEM 注册表错位。
4. 测试：domain checks 在 scratch 下执行的单测（依赖 W1 的实现）；manifest schema 校验测试。
5. 风险：verify 命令是域策略的一部分，需人类确认（已有 `checks_compiled_at` 的 frozen 语义，`machine_dispatch.go:709-713` 对未冻结策略直接跳过），所以不会"平台偷偷给业务加规则"。

---

## 三、"确实值得改" vs "设计取舍 / 不该改"

### 确实值得改（建议进 roadmap）

| 顺序 | 项 | 改动量 | 理由 |
|---|---|---|---|
| 1 | W2 报告落盘 + `--evidence-file` + manifest | S（env + 固定块 + CLI 参数 + 一个上传清单） | 成本最低、直接消除 A9 类证据丢失；不改变现有交付语义 |
| 2 | W1 scratch 域机器验收（exec 侧跑 checks，deliver 要 evidence） | M（dispatch 参数 + exec 执行器 + deliver 闸） | A7/A8 的**唯一**平台侧根治；让"严格口径"从 agent 自律变成平台强制 |
| 3 | W4 上下文感知的 idle 通知（工具调用期间不误报，超预算只报一次并说明） | S-M（watchdogTick + chat_message 查询 + 测试） | 不引入协议；把 4 次噪声降到 0，同时保留"长命令可能卡死"的提示 |
| 4 | W5 警告只注入 owner/consult + 计数口径分离 | S | 一行条件 + 一个查询；避免 verify/subgoal prompt 被无关警告污染 |
| 5 | W6 verify 无裁决 → park/重派一次 | S | 消除"永久 verifying"静默卡死 |
| 6 | W7 拒绝 verifier==assignee（`--self-verify` 逃生门） | S | 把已在 squad reviewer 上承认的原则补到 verifier 字段 |
| 7 | W3 固定块路径改为占位符/`AGENTWORK_WORKDIR` + 小报告镜像 | S-M | 跨机部署的前置条件；不做镜像则至少修路径错误 |

### 设计取舍 / 不该改

1. **idle 不自动取消**（`7cb954d`）：保持。取消会把"慢但会完成"变成"坏 run"，而 `max_run_duration` 已提供契约型取消。A3 的建议应是"减少误报 + 说清上下文"，不是回到自动取消。
2. **idle 通知不触发重调度**：保持。`CreateNoDispatch` 是正确设计；若让 idle 能触发 run，会把"观察"变成"干预"，与 A2 的教训相冲突。
3. **scratch 默认"汇报即交付 + 人卡点"**：保持默认。W1 的验收应是"域可配置的能力"（有 Verify 才强制证据），不能对所有 scratch 目标强制跑命令，否则调研类目标会被无意义的 verify 卡住。
4. **mention cycle ≥8 直接 goal failed**：保持。它粗暴但明确；W5 的第 3 条（≥5 人工确认）如果要加，建议作为**可选的更早、更温和**闸门，而不是下调 8 这个阈值（否则正常多阶段任务会被误杀）。
5. **平台侧 packageVersion 与模板注册表对账**：不做。三轴都不在平台状态里（W8），平台做了也只是对账 agent 的自述；正确做法是把一致性检查编译为 Verify 命令并强制执行。
6. **A2 的"同一派发两条评论"**：不在本文件范围（dispatch-analyst），但注意它与 W4 的联动只在于"并发 run 越多，idle 告警条数越多"；修 A2 会顺带减少告警，但不能替代 W4 的误报修复。

---

## 四、核对记录（本次实际验证过、结论"没问题"的点）

- **verify 去重存在**：`sub_goal` 条件更新（verified 不在 `('done','running')`）+ `enqueueVerifyRunTx` 按 `(sub_goal, role='verify', queued/running)` coalesce（`run.go:507-511`）。不存在"同一工作项 verify 两次"的路径（除非人手动重开 run，那也会被 `sg.Status != 'verifying'` 拒绝）。
- **claim 闸门对 verify 的约束**：`internal/service/run.go:632-715` 的单条 SQL 要求 goal active（review 只放 `role='review'`）、同一 sub_goal 单飞、owner 单飞；verify 与 consult 可与 owner 并行（注释 `run.go:644-645` "consult/review/verify runs are read-only snapshots and run freely in parallel"），这是设计，不是漏洞。
- **idle 评论的副作用**：持久化 + `comment:created` 事件 + `activity_log`，无 goal 状态变更、无 run enqueue（`comment.go:155-207`）。
- **部署版已含 watchdog 修复**：`git merge-base --is-ancestor 7cb954d f0c7563/23a7ce1/HEAD` 均为真；且生产文案 "Run idle for 2m0s" 只存在于修复后代码。
- **scratch 人卡点**：`p0_regression_test.go:473-505` `TestScratchGoalForcesHumanGate` 证明无论 strength/gates，scratch 必 park review；W1 说的是"park 之后平台没有证据"，不是"没 park"。
- **exec 无容器运行时**：全仓 `grep -n "docker|container"` 无命中；`exec.go` 直接 spawn ACP agent，所以 /tmp 是 agent/skill 的选择，不是平台容器。

---

## 五、给模板/外部仓库的两条配套（平台改不了，但不改则前功尽弃）

1. **agentwork-templates / job-env 系列 skill**：所有报告输出根改为 `${AGENTWORK_REPORT_DIR:-$PWD/reports}`，禁止硬编码 `/tmp/job-env-*`；Packager 的 spec 扫描默认 `--metadata` 严格口径。这是 A9/A7 的另一半。
2. **发布归一**：1.0.4 三件套的 sha256、JEM 注册表、env.packageVersion 的对齐脚本，应该作为该 packaging domain 的 Verify 命令随域一起冻结（W8），并在发布前作为门禁执行；平台只负责"执行 + 留证 + 阻断"，不接管版本语义。