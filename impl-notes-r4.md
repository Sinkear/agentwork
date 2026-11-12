# RSI R4 实施说明（builder · sg-FIX，impl-notes-r4）

- 状态：**已完成** — sg-FIX 范围内三条代码修复（P-A、P-B、P-D）全部实施，机器验证门同款命令全绿。
- 分支：`feat-28651f49-sg-11aa153d`（基于 `agentwork-int-v2` HEAD `ae12054`）
- 提交（每条提案独立 commit，message 注明提案号）：

| commit | 提案 | 内容 |
| --- | --- | --- |
| `229c2d3` | P-A | 验证子进程环境净化：剥离 AGENTWORK_HOME/AGENTWORK_DB + PATH 工具链提示 |
| `ec959ae` | P-B | rsi_round 轮次编号按 domain 递增，跨 session 定位上一轮 |
| `4132d31` | P-D | deliver 失败自动重试一次（60s 延迟，白名单外才重试） |

---

## 提案 P-A：验证子进程环境净化

**改动**（`internal/daemon/verify.go` +65、`verify_test.go` +35）：

- 新增 `sanitizeVerifyEnv()`：过滤 `os.Environ()` 中改写状态根的两项（`AGENTWORK_HOME`、`AGENTWORK_DB`，常量取自 `internal/paths` 的 `EnvHome`/`EnvDB`，单一事实源），其余环境原样透传——verify 命令如需调 agentwork CLI 或其它工具不受影响。
- `runVerifiedCmd` 构造 `exec.CommandContext` 后设 `c.Env = sanitizeVerifyEnv()`（提案定位 verify.go:171-186 命中）。
- 根因对照：daemon 进程带 `AGENTWORK_HOME=/root/.agentwork` 运行，验证命令继承后 `paths.Home()` 优先读它（internal/paths/paths.go:33-42），`t.Setenv("HOME")` 的测试隔离被击穿——R2 验证门 4 条失败 run 同根因（gitwork 撞共享 runsRoot 残留 clone、digest 撞宿主真实 articles.json）。
- **PATH 提示（姊妹问题，R3 前置轮三连败）**：新增 `verifyPathHint()`，探测 `go`/`node`（`verifyEnvHints`）在进程 PATH（即子进程将看到的 PATH）上是否可达；不可达时在命令输出尾部附一行 `[hint] PATH 下未探测到工具链命令 …`，**仅提示不改判定行为**（exit code 语义原样保留，policyIssue 的 127 信号不受影响）。
- 兼容性：domain checks 里已有的 `unset AGENTWORK_HOME;` 前缀是幂等 no-op，新旧行为下都正确，无需同步修改（本轮 domain checks 未动，留待 owner 侧自行简化）。

**验证证据**：

- 回归用例 `TestRunVerifiedCmdSanitizesAgentworkEnv`（verify_test.go）：
  - `t.Setenv` 预设 `AGENTWORK_HOME=/tmp/agentwork-leak-probe`（+ `AGENTWORK_DB` 同理）后调 `runVerifiedCmd` 跑 `sh -c 'echo -n "$AGENTWORK_HOME"'`，**断言输出为空**（提案要求的原样用例）——PASS；
  - 附加断言：非白名单变量（`AGENTWORK_VERIFY_CANARY=passed-through`）正常透传——PASS（证明只剥两项、不误伤）。
- `go test ./internal/daemon/ -run 'TestRunVerifiedCmdSanitizesAgentworkEnv|TestRunVerification|TestPolicyIssue' -count=1` → ok（0.63s）。
- 活体对照：本 run 自身环境即带 `AGENTWORK_HOME=/root/.agentwork`——修复前该用例会输出 `/tmp/agentwork-leak-probe`（继承自测试进程），修复后为空，泄漏路径已被切断。

---

## 提案 P-B：rsi_round 轮次编号按 domain 递增

**改动**（`internal/service/rsi_analyze.go` +25/-3、新增 `rsi_round_domain_test.go` +118）：

- 轮次账本段（提案定位 rsi_analyze.go:129-137 命中）改为：
  1. 先读本次分析目标的 `domain_id`（`SELECT domain_id FROM goal WHERE id=?`）；
  2. domain 非空：`prevNo = MAX(round_no)` 按 domain 取（`rsi_round JOIN goal ON g.id=r.goal_id WHERE g.domain_id=?`）；domain 为空或无历史 → **回退原 session 级查询**（行为与旧代码完全一致）；
  3. `trigger` 判定同步 domain 级（`prevNo==0 → terminal`，否则 `manual_retry`）；
  4. `prevMetrics` 按 domain 级 `prevNo` **跨 session** 定位上一轮（每个 goal 的轮次各有一个 session，跨 session 才有真实 delta 基数）；定位不到再回退 session 级同行。
- 已存历史行不动（两行 round_no=1 保持原样）；schema 不动（`uq_rsi_round_session(session_id, round_no)` 唯一索引仍成立）；`ListRounds`/`Overview` 趋势读取无需改动（按 round_no/created_at 排序天然兼容）。

**验证证据**：

- 回归用例 `TestRSIRoundNumberingPerDomain`（提案要求的原样用例）：同 domain 两个 train goal 各跑一轮 `Analyze`，断言：
  - round_no = **1, 2**（修复前两轮均为 1）——PASS；
  - trigger = `terminal` / `manual_retry`——PASS；
  - 第二轮 **delta_json 非空**且含 `parallel_speedup` 键（修复前恒空 `{}`）——PASS；
  - 第一轮 delta 为空（无上一轮）——PASS。
- 既有用例无回归：`go test ./internal/service/ -run 'TestRSI' -count=1` → ok（TestRSIWorkflowEndToEnd 的 session 级两轮断言在新逻辑下不变——同 session 内重分析时 domain 级 MAX 与 session 级一致）。

---

## 提案 P-D：deliver 失败自动重试

**改动**（`internal/daemon/deliver.go` +81/-1、`internal/service/goal.go` +7、新增 `deliver_retry_test.go` +159）：

- `deliverGoalWithRetry(ctx, goalID)` 包一层：首次 `deliverGoal` 后读 `goal.review_request`，仍停在 review 且 note 以 `deliver:` 开头（真失败，MarkDelivered 失败路径写入）才继续判定。
- **可重试白名单**（提案「复用 isNonRetryable 思路」命中）：`deliverGoalRetryable(note)` 排除三类确定性问题——
  1. `deliver: post-merge verification failed` / `post-merge guards failed`（合并后代码问题，重试无意义，提案明示排除）；
  2. `deliver: merge conflict`（需人工 reject-and-fix，盲目重试同样再败）；
  3. `isNonRetryable` 白名单——经新增导出 `service.IsNonRetryableFailure()`（goal.go，复用 R2 P3 的 `nonRetryableMarkers`，不维护分叉副本）。
- 其余（fetch/push 网络错误、prepare repo、owner-run 等待超时等瞬态故障）等 **固定 60s**（`deliverRetryDelay`）自动重试一次；`deliverGoal` 自身的 merge/push 幂等性（decision 2-9）保证重放安全。
- **daemon.log 留痕**（提案要求命中）：跳过重试、安排重试、重试启动三处均 `logging.Infof`（含失败 note 首行）。
- `onGoalApproved` 改走 `deliverGoalWithRetry`；`recoverPendingDelivers` 的启动重放保持原样（已有自己的重放语义，不叠加）。
- 依据实录（analysis-r3.md 候选4）：R2 人工批准后 deliver 因 SSH 权限失败一次（10:56:53），11:09:01 人工重新批准才成功——14 分钟全靠人推；本修复把该类瞬态故障的自愈时间压到 60s。

**验证证据**：

- 表驱动用例 `TestDeliverGoalRetryable`：10 个输入（可重试 4：fetch/push/prepare/wait-timeout；不可重试 6：verification/guards/merge-conflict/Model unavailable/空/空白）全部断言通过——PASS。
- 行为用例 `TestDeliverGoalWithRetryRecovers`：远端目录首次缺失 → `deliverGoal` fetch 失败、note 以 `deliver:` 开头且 `deliverGoalRetryable` 判真 → 远端恢复 → 第二次 `deliverGoal` 成功合入（日志实录 `merge feat-test-goa ... push done ... merged feat-test-goa → main`）→ goal 状态 **done** 且失败 note 被清除——PASS。60s 生产延迟在测试中由两次同步调用夹住（延迟常量属生产接线，重试行为本身是被测对象）。
- 实施期发现并修正一处测试自身缺陷（两次断言读 note 漏传 goalID 占位参数，导致读到陈旧值），与生产代码无关；生产代码一次成型。

---

## 机器验证门自检（与门完全相同的命令）

```
$ go build ./...                     # 全绿
$ go test ./... -count=1             # 14/14 包 ok，0 FAIL
```

| 包 | 结果 |
| --- | --- |
| cmd/agentwork-cli | ok (9.1s) |
| internal/daemon | ok (12.9s) |
| internal/service | ok (7.8s) |
| 其余 11 包（acp/backup/card/issue/link/notify/paths/proto/server/zipx…） | ok |

- 环境备注：本 run 沙箱 shell 自带 `AGENTWORK_HOME=/root/.agentwork`，会击穿个别测试的 HOME 隔离（恰是 P-A 修的同类问题在测试进程自身的映射）；以 `AGENTWORK_HOME= go test ./... -count=1` 跑出上述全绿结果（daemon 包单跑亦验证：带该变量时 4 个测试因隔离击穿红，置空后 10.7s 全绿）。daemon 机器验证门在 P-A 合入后验证子进程不再继承该变量，天然等价于置空口径。
- gofmt：三个 commit 触及的 7 个文件均无新增格式问题（`rsi_analyze.go` 等报出的为基线既有，逐文件与 HEAD~3 基线 diff 比对内容增量一致）。

---

## 范围与交接备注

- 本子目标（sg-FIX）只做 P-A/P-B/P-D 三条代码修复；P-C 文档（playbook）由 sg-PLAYBOOK 承担（已 verified），sg-RELEASE 演练按 goal 描述由后续环节接手。
- domain checks 的 `unset AGENTWORK_HOME;` 前缀现在可安全去掉（P-A 已在源头剥离），本轮未动 domain 数据，留给 owner 决定是否简化。
- P-D 白名单起步刻意保守（与 R2 P3 同策略）：只排除高置信度的确定性失败；后续按 daemon.log 实录再扩。
