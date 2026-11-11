# RSI R3 分析报告：R2 过程暴露问题清单与新提案（optimizer · sg-ANALYZE）

- 日期：2026-11-11
- 作者：optimizer（只读分析，未改任何业务代码）
- 数据源：daemon API（`http://127.0.0.1:47813` 的 `/goals`、`/goals/{id}/runs`、`/goals/{id}/comments`、`/rsi/sessions`、`/rsi/sessions/{id}`、`/rsi/sessions/{id}/rounds`、`/rsi/overview`）、`rsi_round` 账本（R1 `1521efe0…` / R2 `8aa88fdd…`）、`/root/.agentwork/daemon.log`、daemon 进程环境（/proc/449/environ）、git log（agentwork-int-v2 基线 `e7c2572`）
- 说明：goal 描述与 AGENTS.md 中写的 `/rsi` 端点实测 404，实际可用端点为 `/rsi/sessions`、`/rsi/sessions/{id}`、`/rsi/sessions/{id}/rounds`、`/rsi/overview`（已列入问题清单 #15）。

---

## 一、《过程暴露问题清单》（R1+R2 全部失败/异常 run 逐条）

### R2（goal 25da0cc684817ac4d8a685180e6bbc21，8 条 run，4 失败 1 取消）

| # | run id | 角色 | 状态 | 错误摘要 | 根因 | 当前状态 |
|---|--------|------|------|----------|------|----------|
| 1 | `37969c7dce3cabd75e50f98dd630555d` | owner | failed（验证门） | `go test ./... -count=1` FAIL：TestPersistentGoalWorkdir「bare.git does not appear to be a git repository」等 | **验证子进程继承 `AGENTWORK_HOME=/root/.agentwork`**，`paths.Home()` 优先读该变量（internal/paths/paths.go:33-42），`t.Setenv("HOME")` 的测试仍指向真实数据根：共享 runsRoot 里残留指向已删除临时 bare 仓的 clone → fetch 128 | **已绕过**（domain checks 改为 `unset AGENTWORK_HOME; …` 前缀），**待正式修复 → 提案 P-A** |
| 2 | `a234f3422b5b9991ec96ccc8725bd222` | sg-IMPL att1 | failed（验证门） | TestGitWorkdirAndTransferPush fetch 128 + TestDeliverMergeIdentityNoGlobalGitConfig「deliver: fetch: exit 128」+ TestCollectDigestBatch* 「articles = 1, want 2」 | 同上泄漏：deliver/digest/gitwork 三类 `t.Setenv` 测试全部击穿到真实 `/root/.agentwork`（digest 断言被宿主真实 articles.json 污染） | 同上（已绕过，待修） |
| 3 | `176d72f22468e367261a285b0e96207f` | sg-IMPL att2 | failed（验证门） | 与 #2 完全相同的三类 FAIL | 同上（失败模式重复，重试不修复原因） | 同上（已绕过，待修） |
| 4 | `d08e376d8a129de9782bfaeea2df5a7b` | sg-IMPL att3 | failed（验证门） | 同上，第三次 | 同上；**三连败烧尽该工作项重试预算**，是「验证门三连败」本体 | 同上（已绕过，待修） |
| 4b | `642a21d49e023135614f71ed71be52d4`、`e83e70cf75a31c35742fc19fe2373247`、`f1182ff5ffcfabd38d39f7085eba8bae` | R3 前置轮（owner att1 / sg att1 / sg att2，goal 357252be…） | failed（验证门） | `unset AGENTWORK_HOME; go build ./...` → `sh: line 1: go: command not found`（exit 127），三次全败且平台自动附上「acceptance-policy problem」提示 | **unset 前缀引入新缺陷**：daemon 进程 PATH 为 `/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin`（/proc/449/environ 实测），go 实际装在 `/usr/local/go/bin`（靠启动 shell 的 profile 别名/软链生效）；unset 后 `sh -c` 仍继承 daemon 的窄 PATH，go 不在列 → 127。daemon.log 实录 15:22/15:45/15:48/15:52 四次 0 秒退出 | **已解决**（人工在 /usr/local/bin 加 go/gofmt 软链，16:01 验证门 8s+15s 通过 → run `5dc64ab5…` verified ok）；教训：**环境变量修补要连带验证 PATH 完整性**，属 P-A 的姊妹问题（P-A 落地时应同时冻结 verify 子进程 PATH 语义） |
| 5 | `b77ba4c9cef45a1d9c236e03f000100f` | owner | cancelled（stopped） | 运行 71 分钟（12:50→14:01）无产出，人工停止并重发指令 | leader 轮运行卡顿（turn 未正常结束，看护未覆盖 owner 运行） | **已绕过**（人工停止 + 3cb160d91 重发）；候选任务 5 |
| 6 | —（非 run，deliver 阶段） | — | deliver 失败一次 | daemon.log 10:56:53：deliver fetch 失败「UNPROTECTED PRIVATE KEY FILE! Permissions 0750 for /root/.ssh/id_ed25519」 | 容器重启重置了 SSH 私钥权限；人工批准（10:56:51）后的自动 deliver 被拒 | **已解决**（权限修正后 11:09:01 重试 → 11:09:24 merged feat-25da0cc6 → agentwork-int）；但本次重试依赖人工，候选任务 4 |
| 7 | `b319cffb1e8b40b033554096c1cf6a95` | sg-IMPL（环境修复后） | completed | — | 验证门通过（unset 前缀生效） | ✅ 已解决 |
| 8 | `a25f178a99e4747071a657c198f8f2bb` | sg-REBENCH | completed | — | R2 复测全绿（14 包 0 FAIL） | ✅ 已解决 |

### R1（goal 3ec2bfc64b364e7ee24c5f91b67e4c22，31 条 run，15 失败 3 取消；本轮已按 API 全量复核）

| # | run id（节选，共 12 条同类） | 角色 | 状态 | 错误摘要 | 根因 | 当前状态 |
|---|--------|------|------|----------|------|----------|
| 9 | `8318c5cc…`（bench att1-3，含 `634c78a8…`、`9cd850bf…`）、`899a791b…`、`e65c9c3c…`、`c9d79653…`、`9922892c…`、`75415878…`、`ceffb017…`、`4716f2f2…`、`f97a5701…`、`e068d49d…` | bench/owner | failed | `Model unavailable: opencode/muse-ember-1.0`，每次 5-6s 即退出 | 新建会话拿到默认模型不在网关清单；凡新建会话的成员任务首 prompt 必败，重试预算被确定性错误烧光（12 条 run） | **已解决**（opengw 网关配置 + opencode 2.2.5 升级）；该教训已由 P3 不可重试快速失败对症 |
| 10 | `75dbf852eaa1af4e3b2e7d63360f5f4a` | owner | failed | `machine offline — the machine must run agentwork connect` | 连接中断 | **已解决**（重连） |
| 11 | `3dec40838070ad66c2ad1eae508ed691` | owner | failed | `git: fetch: signal: killed` | fetch 被杀（连接/资源竞争期间） | **已解决**（随 #12 修复） |
| 12 | `0ae48700b684d04e4c77395326733b33`（att2，cancelled）、`e1ca8d3faa7f87c52248dbbce7d8f008`（att1，cancelled）、`326129752dd07417876b7f7b958b580d`（att3，failed「cancelled by platform」）、`deaee148a46b066ccadfc8b79d672c7c`（failed「git: worktree add: context canceled」） | owner | cancelled/failed | 在途 run 被平台取消/打断 | **双 `agentwork connect` 实例互抢 peer**，杀掉在途 run | **已解决**（人工合并为单实例，见 204344e947f7 评论；已固化为单实例纪律） |

### 数据类问题（非 run 失败，但直接伤害可感知性）

| # | 问题 | 证据 | 当前状态 |
|---|------|------|----------|
| 13 | **rsi_round 轮次编号不递增**：两轮均 `round_no=1`、`trigger=terminal` | rsi_round 两行（R1 `1521efe0374196cf889d8e53f15b01d1` / R2 `8aa88fdd89937757d9b449e11efd3e6e`，`/rsi/sessions/{id}/rounds` 实测两行 round_no 均 1）；`/rsi/overview` trend 两条 round_no=1；delta_json 均为 `{}` | **待修 → 提案 P-B** |
| 14 | **基准口径漂移**：同一基线（33f16dc/0bf354e）test 实测 23.012s（首跑，含测试二进制编译）→ 14.5s（sg-B2 复跑）→ 11.3s（R1 冻结口径），差近 2 倍；build 冷 38-44s vs 温 3.9-5.6s 差约 10 倍 | run `25c28dc347ef`（23.012s）vs `3c3289b01724`（14.5s）；benchmark-report-r2.md 冷 44.417s / 温中位 4.387s | **待修 → 提案 P-C**（R3 sg-REBENCH 派发词已按冷缓存口径，属约定层修复） |
| 15 | **`/rsi` 端点口径不一致**：AGENTS.md/goal 描述写 `/rsi`，实测 404；R1 阻塞汇报（3baf73cff449）即已附带发现 | `curl /rsi` → "404 page not found"；实际为 `/rsi/sessions` 等 4 端点 | 待修（文档/别名级，候选任务 3） |
| 16 | **验证命令的 PATH 依赖宿主 shell 配置**：domain checks 无法假设 daemon PATH 含工具链 | R3 前置轮 642a21d4/e83e70cf/f1182ff5 三连败实录 + daemon 进程 environ 实测（见问题 #4b） | **已解决**（软链接治标）；根因待 P-A 一并固化（verify 子进程 PATH 约定写入验收策略文档） |

---

## 二、新提案（≤3 条，均【待评审】，等待人工批准）

### 提案 P-A【待评审】验证子进程环境净化（AGENTWORK_HOME 泄漏的正式修复）

- **问题依据**：R2 验证门三连败（run `a234f342…`/`176d72f2…`/`d08e376d…` + owner `37969c7d…`，共 4 条失败 run）全部同一根因：`runVerifiedCmd`（internal/daemon/verify.go:171-186）用 `exec.CommandContext` 启动验证命令时**不裁剪环境**，子进程继承 daemon 的 `AGENTWORK_HOME=/root/.agentwork`；而 `paths.Home()` 优先读该变量（internal/paths/paths.go:33-42），导致所有 `t.Setenv("HOME")` 的测试（deliver/digest/gitwork/worktree 四个文件）隔离击穿——gitwork 撞上共享 runsRoot 里的残留 clone、digest 断言撞上宿主真实 articles.json。当前靠 domain checks 里每个命令手写 `unset AGENTWORK_HOME;` 前缀绕过（本轮 domain `cd46c8f9…` checks 已如此配置），属「靠每个 verify 命令记得 unset」的脆弱约定。
- **拟改动位置**：internal/daemon/verify.go:171-186 `runVerifiedCmd`——构造 `c := exec.CommandContext(...)` 后新增 `c.Env = sanitizeVerifyEnv()`；新函数过滤 `os.Environ()` 中的 `AGENTWORK_HOME` 与 `AGENTWORK_DB` 两项（只剥离这两个改写状态根的变量，保留其余环境，避免误伤需要调 CLI 的 verify 命令）；同函数需一并固化 PATH 语义——若继承的 PATH 不含常用工具链目录（如 /usr/local/go/bin），在报告输出一行提示（探测 `go`/`node` 等是否存在，仅提示不改行为，避免 R3 前置轮 `642a21d4` 式 0 秒静默 127）。同步在 internal/daemon/verify_test.go 加回归用例：预设 `AGENTWORK_HOME` 后跑 `sh -c 'echo $AGENTWORK_HOME'` 断言输出为空。
- **预期收益**：该类验证门失败从 R2 的 4/8 条 run（50%）降为 0；domain checks 可去掉 unset 前缀（恢复策略原文 `go build ./...`）；任何域名的 verify 不再因宿主状态泄漏误红。可量化：验证门同类失败 4 → 0。
- **风险与回滚**：极低——仅影响 verify 子进程环境，不碰 agent/CLI 通道；若某域 verify 确需读宿主状态，可在 checks.Setup 显式 `export AGENTWORK_HOME=…` 找回。回滚 = revert 该单 commit（domain 里的 unset 前缀是幂等 no-op，回滚后行为不变）。
- **用户可感知说明**：交付前的自动质量检查不再因为「测试偷看了平台自己的数据」而莫名变红，任务一次通过率明显提高，你不用再反复点批准重试。

### 提案 P-B【待评审】rsi_round 轮次编号按 domain 递增

- **问题依据**：rsi_round 账本两行 round_no 均为 1（R1 `1521efe0…` / R2 `8aa88fdd…`，trigger 均为 terminal），`/rsi/overview` trend 无法区分轮次，跨轮 delta_json 恒为空 `{}`——「跨轮指标对比」与演进报告第五节失去数据基础。根因：internal/service/rsi_analyze.go:129-130 中 `round_no = MAX(round_no)+1` 按 `session_id` 取值，而每个 train goal 终态生成**新的** training_session（R1 session `371a5055…`、R2 session `f31c3a6c…`），编号永远从 1 重来。
- **拟改动位置**：internal/service/rsi_analyze.go:129-137——MAX 查询（:130）改为按 domain 取：`SELECT COALESCE(MAX(r.round_no),0) FROM rsi_round r JOIN goal g ON g.id=r.goal_id WHERE g.domain_id=?`（domain_id 从本次分析的目标行读取；domain 为空或无历史时回退现有 session 级查询）；prevMetrics 读取（:136-137）同步按新 prevNo 定位上一轮行（跨 session 取上一轮 metrics，delta 才有真实基数）；trigger 判定（:133-134）同步改为 domain 级 `prevNo==0`。schema 不动：唯一索引 `uq_rsi_round_session(session_id, round_no)`（internal/store/store.go:148）仍成立。
- **预期收益**：R3 起轮次编号 1→2→3 连续；洞察页趋势与每轮演进报告【跨轮指标对比】首次具备可比序列；delta_json 开始产出真实轮间差值（如「重试放大 3.5→2.5」将直接呈现为轮间 delta 而非手工查表）。
- **风险与回滚**：低——只影响新写入行的编号；已存两行 round_no=1 保持原样（历史读取按 created_at 排序仍正确，前端无破坏）。回滚 = revert 单 commit，编号回到 session 级。
- **用户可感知说明**：训练分析页的「轮次趋势」从此能看到 1→2→3 的连续编号和每轮之间的真实变化，而不是每轮都叫「第 1 轮」。

### 提案 P-C【待评审】bench 基准统一冷缓存口径

- **问题依据**：同一基线（agentwork-int HEAD 33f16dc）上，`go test ./... -count=1` 首跑实测 23.012s（run `25c28dc347ef`，含全部测试二进制编译），复跑降到 14.5s（run `3c3289b01724`）；`go build ./...` 冷 38-44s vs 温 3.9-5.6s 差约 10 倍（benchmark-report-r2.md）。R1→R2 报告里「测试 23.0s→11.6s、-50%」的结论混入了大量缓存口径噪音（docs/rsi-round-evolution-report.md 问题 #8 已自认），跨轮对比不可信。
- **拟改动位置**：纯约定层，不改业务代码——① docs/rsi/self-iter-playbook.md「每轮操作」小节固化口径：build 必测冷（独立 GOCACHE，如 `GOCACHE=$(mktemp -d) go build ./...`）+ 温中位各一列；test 以温 3 连跑中位为准并注明首跑编译已完成的口径；② 所有基准报告必须同时记录冷/温两列与 GOCACHE 路径；③ R3 的 sg-REBENCH 派发词已按此口径执行（`GOCACHE=/tmp/r3-gocache` 冷构建），本轮即为首个统一口径样本。下一轮可再落 scripts/rsi-bench.sh 统一计时（builder 实施）。
- **预期收益**：跨轮 build 对比噪音从 ±50%（23.0 vs 14.5 同基线）收敛到冷/温分列后各自 ±10% 内；演进报告【跨轮指标对比】的百分比首次可信，人工批准提案时有真实数字依据。
- **风险与回滚**：无代码风险，仅测量与文档约定；口径写错回滚 = 改回 playbook 文档即可。
- **用户可感知说明**：每轮报告里「这轮比上轮快了百分之多少」从此是真实改进，不再混入缓存冷热的假象，你看报告做批准决策更有依据。

---

## 三、《下一轮候选任务清单》（按优先级，供人工挑选成为 R4 任务）

1. **实施 P-A：验证子进程环境净化**（高优）——依据：R2 验证门三连败 4 条失败 run 同根因，且 R3 前置轮 unset 前缀又引入 PATH 三连败（#4b），验证子进程环境已是两轮最高频失败源；收益：同类验证门失败 4+3 → 0，domain checks 可简化；改动集中在 verify.go 一个函数 + 1 个回归测试，工作量最小。
2. **实施 P-B：rsi_round 按 domain 递增**（中优）——依据：两轮 round_no 均为 1、delta 恒空；收益：R4 报告【跨轮指标对比】首次有真序列；改动一条 SQL + trigger 判定 + prevMetrics 跨轮读取，随带回归验证 `/rsi/overview` trend。
3. **`/rsi` 端点口径修正**（低成本高一致性）——依据：AGENTS.md/playbook/goal 描述写的 `/rsi` 实测 404（R1 3baf73cff449 已附带发现，R3 再次踩到）；改法二选一：补文档为 `/rsi/sessions` 等真实端点，或 handler 加 1 行 `GET /rsi` 别名指向 overview。
4. **deliver 失败自动重试/退避**（中优）——依据：R2 人工批准后 deliver 因 SSH 权限失败一次（daemon.log 2026-11-11 10:56:53），直到 11:09:01 才重试成功，期间依赖人工推进；改法：deliverGoal 外层加一次带延迟的自动重试（可重试错误白名单复用 P3 的 isNonRetryable 思路，SSH 权限类修正后可重试）。
5. **owner/leader 运行卡顿看护**（中优）——依据：R2 owner run `b77ba4c9…` 运行 71 分钟（12:50:33→14:01:25）无产出被人工停止；改法：把 watchdog idle-notify/最大时长看护明确覆盖 owner 运行（目前主要命中 worker），超时先通知人工而非静默挂起。

---

*本报告只读产出，未改动任何业务代码；三条提案均待人工评审批准后方可由 builder 实施。*
