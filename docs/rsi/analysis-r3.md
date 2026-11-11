# RSI R3 分析报告（optimizer 产出，只读）

- 轮次：R3（新基线 agentwork-int-v2，基线 commit e7c2572）
- 数据源：daemon API（http://127.0.0.1:47813，/rsi/sessions、/rsi/sessions/{id}/rounds、/goals/{id}/runs、/goals/{id}/comments）+ 本仓 git log + 真实状态目录 `/root/.agentwork` 现场证据
- 输入会话：R1 session（goal R1，3 节点/3 边，finding=重试放大 3.5 次/节点）、R2 session（goal R2，5 节点/5 边，finding=重试放大 2.5 次/节点）
- 约束遵守：本轮未修改任何业务代码，仅产出本文件。

---

## 一、《过程暴露问题清单》（R2 goal 全部 runs 逐一核对）

R2 goal 共 8 条 run，其中失败/取消 5 条；R1 goal 共 31 条 run，其中失败/取消 18 条。逐条如下（根因分类：环境 / 代码 / 流程）。

### R2（当前轮数据）

| # | run id | 状态 | 错误摘要 | 根因分类 | 当前状态 |
|---|--------|------|----------|----------|----------|
| P-1a | a234f3422b5b9991ec96ccc8725bd222 | failed（验证门第 1 败） | `go test` 3 个测试同型失败：TestGitWorkdirAndTransferPush / TestPersistentGoalWorkdir（gitwork_test.go:59/:156，`fetch: exit status 128`）+ TestDeliverMergeIdentityNoGlobalGitConfig（deliver_test.go:114）。git 报 `/tmp/TestGitWorkdirAndTransferPush*/bare.git does not appear to be a git repository` | **环境→代码（环境变量泄漏）**：验证门子进程原样继承 daemon 的 `AGENTWORK_HOME`（见提案 A），门内测试解析状态根目录落到 daemon 真实家目录，跨测试状态互相污染 | 已绕过（第 4 次验证 b319cffb 通过；泄漏机制本身未修，正式修复见提案 A） |
| P-1b | 176d72f22468e367261a285b0e96207f | failed（验证门第 2 败） | 与 P-1a 完全同型、同 3 个测试同 3 行号 | 同上（失败模式重复即 RSI finding「重试放大」的直接来源） | 同上 |
| P-1c | d08e376d8a129de9782bfaeea2df5a7b | failed（验证门第 3 败） | 与 P-1a 完全同型 | 同上 | 同上 |
| P-2 | 37969c7dce3cabd75e50f98dd630555d | failed（owner run，验证失败） | 除同型三败外，digest 测试出现 `articles = 50, want 2`：真实状态目录 `/root/.agentwork/digest/articles.json` 被历次门内测试累计写入 50 条；错误文本含 `Please make sure you have the correct access rights`（"SSH 权限"表象） | **环境（同 P-1 根因）**：所谓 SSH 权限报错是 git 对被污染/不可达路径的标准 128 提示，并非真实 SSH 凭据问题；现场证据：`/root/.agentwork/digest/articles.json.corrupt` 至今存在 | 已解决表象、未除根因（与 P-1 同一修复） |
| P-3 | b77ba4c9cef45a1d9c236e03f000100f | cancelled（owner run 第 2 次尝试） | result_summary 为空；comments 记录「前一轮运行被人工停止（卡顿）」「Run cancelled: stopped by user」 | **流程/环境（双 connect 互抢 peer）**：同一机器重复 `agentwork connect` 抢占 peer 注册导致编排卡顿（R1 同类证据：run 75dbf852「machine offline — must run agentwork connect」） | 已绕过（人工停止后以单 connect 实例重入成功：87bf5d1447450275fc29777ce38b752b completed）；平台侧防重入待修 |

### R1（跨轮参照，仅列根因聚类）

| 聚类 | 数量 | 代表 run id | 根因分类 | 当前状态 |
|------|------|-------------|----------|----------|
| Model unavailable 重试循环 | 12 | 8318c5cc…（attempt1→3 × 多节点） | 环境（模型不可用却按可重试处理） | 已解决（R2 P3 落地不可重试白名单，起步仅 Model unavailable） |
| 机器离线 | 1 | 75dbf852… | 环境（connect 未运行） | 已绕过 |
| git fetch 被杀 / worktree add 取消 | 2 | 3dec4083…、deaee148… | 环境（资源/取消） | 已绕过 |
| 平台取消 | 3 | 32612975… 等 | 流程（人工停止） | 已绕过 |

### 上轮遗留问题状态汇总

- 重试放大（R1 3.5 次/节点 → R2 2.5 次/节点）：**未闭环**。P3 只覆盖 `Model unavailable` 一种不可重试错误；本轮 R2 的 3 连败属"确定性环境类失败仍被重试"的同一问题，是 R3 提案 A 的直接依据。
- 验证门环境泄漏：**待修**（提案 A）。
- rsi_round 轮次编号：**待修**（提案 B，证据见下）。
- 基准口径：**待修**（提案 C；R1 的 build 11.574s/test 23.012s 为温缓存口径，R2 补测冷缓存 44.417s，跨轮对比口径不一致）。

---

## 二、新提案（≤3 条）

### 提案 A：验证子进程环境净化（AGENTWORK_HOME 泄漏的正式修复）

- **问题依据**：R2 验证门三连败（run a234f342 / 176d72f2 / d08e376d，同一签名三测三行号）；owner run 37969c7d 实证门内测试读写了 daemon 真实状态（`articles = 50, want 2`）；现场 `/root/.agentwork/digest/articles.json.corrupt` 残留。代码依据：`internal/daemon/verify.go:171-187`（runVerifiedCmd）与 `verify.go:373-380`（runVerifyOutput）均 `exec.CommandContext(...)` 后**未设置 c.Env**，子进程完整继承 daemon 环境（含 AGENTWORK_HOME，如 `internal/paths/paths.go:27` 所定义）。
- **拟改动位置**：`internal/daemon/verify.go` 两处 exec 构造点——为验证/守卫子进程显式构造 `c.Env`：基于 `os.Environ()` 剔除 `AGENTWORK_HOME`，代之以每轮验证的隔离目录（如 `<verify 工作目录>/.agentwork-verify-home`，mkdir 一次复用），使门内任何未 `t.Setenv` 的代码路径也绝不可能触碰 daemon 真实状态根。同一函数族内 setup 命令（runSetupOnly，:152 复用 runVerifiedCmd）自动一并生效。
- **预期收益**（可量化）：消除 R2 中占失败 run 4/5 的整类失败；该类确定性失败当前平均烧 3 次重试才收敛（R2：2 节点 5 次 run），修复后这类失败归零，预计子任务平均 run 数从 2.5 降至 ~1（与 RSI finding「重试放大」闭环）；同时杜绝 daemon 真实数据被测试污染（digest 50 条/损坏文件类事故归零）。
- **风险与回滚**：风险=若某 domain 的 setup/verify 命令**合法依赖**读取真实 AGENTWORK_HOME（如需要 daemon 状态的集成验证），会被切到隔离目录。缓解：仅净化 AGENTWORK_HOME 一项（其余环境原样保留），并在 domain 模板文档标注该行为；如需真实状态可在命令内显式 `AGENTWORK_HOME=...` 覆盖。回滚：单 commit `git revert` 即可，无数据迁移。
- **用户可感知说明**：以后构建门不会再因为"测试误碰了系统真实数据"而莫名其妙连挂三次——任务一次通过率明显变高，等待结果的时间更短，也不会再出现系统目录里多出损坏的测试残留文件。

### 提案 B：rsi_round 轮次编号按 domain 递增

- **问题依据**：API `/rsi/sessions/{id}/rounds` 实测：R1（session 371a5055…）与 R2（session f31c3a6c…）的 round_no **均为 1**；两轮的 baseline_json 与 delta_json **均为空 {}**。代码依据：`internal/service/rsi_analyze.go:130` `SELECT COALESCE(MAX(round_no),0) FROM rsi_round WHERE session_id=?`——编号按 session 取 MAX，新 session 必从 1 重新计数；且因同 session 内无前轮记录，:137 的前轮基线查询永远取不到数据，跨轮 delta 机制**实际从未生效**。
- **拟改动位置**：`internal/service/rsi_analyze.go:130` 附近——轮次号改为按 domain 递增：先经 session→goal 取 domain_id（或给 rsi_round 冗余 domain_id 列，`internal/store/store.go:130-149` 建表处加列+索引迁移），查询改为同 domain 内 `MAX(round_no)+1`；基线查询（:137）同样按 domain 取上一轮 metrics，使 baseline_json/delta_json 真正填充。
- **预期收益**（可量化）：round_no 变为全局演进序号（R1=1、R2=2、R3=3…），delta_json 从恒空 {} 变为每轮必含「上轮 vs 本轮」的指标差值；演进报告第④节「跨轮指标对比」从依赖手工汇总变为 API 直读，报告生成器的对比节数据 100% 可自动获得。
- **风险与回滚**：风险=存量两行 round_no=1（不同 session）在改为 domain 维度后需要一次 backfill（按 started_at 顺序重排为 1、2）；uq_rsi_round_session 唯一索引保持不变，不冲突。回滚：revert commit + 如加了列，回滚脚本删列（单文件 SQLite，操作前备份）。
- **用户可感知说明**：在平台的"优化轮次"页面里，每一轮会清楚地显示这是第几轮、比上一轮变好还是变差多少——不再每一轮都显示"第 1 轮"且看不到前后对比。

### 提案 C：bench 基准统一冷缓存口径（固化成脚本 + 协作约定）

- **问题依据**：R1 基线（build 11.574s / test 23.012s）为温缓存口径；R2 复测给出温缓存中位（test 11.559s）+ 冷缓存单点（build 44.417s）双口径，「test -50%」的跨轮结论混入口径因素，无法归因纯净。R3 的 sg-REBENCH 简报已改用 `GOCACHE=/tmp/r3-gocache go build ./...` 冷口径，但口径目前只存在于简报文本中，未固化。
- **拟改动位置**：新增 `scripts/bench-caliber.sh`（固定命令序列：冷 build=独立 GOCACHE 计时；go test ./... -count=1 三连跑取中位；包级 Top10 提取；输出固定格式 markdown 片段），并在 `AGENTS.md` 协作约定节加一行"每轮 bench 必须用该脚本产数"。
- **预期收益**（可量化）：R1→R2→R3→… 所有 benchmark-report 的数字同口径可直接竖排对比；消除"温/冷混比"造成的不可归因差异（R2 数据显示同机冷缓 build 是温缓的 ~10 倍，44.4s vs 4.4s——不锁口径时跨轮结论可偏差一个数量级）。
- **风险与回滚**：无代码风险（纯脚本+文档）；唯一成本是每轮 bench 多跑一次冷缓存（约 45s）。回滚：删脚本+删约定行即可。
- **用户可感知说明**：以后每轮"改进了多少"的对比数字都是同一把尺子量出来的，用户看到的百分比提升是真提升，不会因为测量方法变化而虚高或虚低。

---

## 三、《下一轮候选任务清单》（按优先级）

1. **[高] 实施提案 A：验证子进程环境净化**。依据：R2 验证门三连败（run a234f342/176d72f2/d08e376d）+ 真实状态污染实证（articles=50、articles.json.corrupt）。预期收益：确定性环境类验证失败归零，子任务平均 run 数 2.5→~1。
2. **[高] 实施提案 B：rsi_round 轮次编号按 domain 递增 + delta 基线生效**。依据：/rounds API 实测两轮 round_no 恒 1、delta_json 恒空。预期收益：跨轮 delta 从 0% 可用率到 100%，演进报告对比节自动化。
3. **[中] 实施提案 C：bench 口径固化脚本**。依据：R1/R2 口径不一致（温 23.0s vs 冷/温混比）。预期收益：跨轮对比同尺子，结论可归因。
4. **[中] 不可重试白名单扩类**：把「确定性 git 128 / 环境缺依赖」纳入 isNonRetryable（`internal/service/goal.go:105-123` 白名单起步仅 Model unavailable）。依据：R2 三连败属确定性错误仍被重试；R1 12 次 Model unavailable 已闭环但类目未扩。预期收益：同类失败从 3 次重试收敛降为 1 次即终态。
5. **[低] connect 防重入**：同一机器重复 `agentwork connect` 互抢 peer 导致编排卡顿（R2 owner run b77ba4c9 被迫人工停止；R1 run 75dbf852 machine offline 同族）。预期收益：消除人工干预点，编排不再卡顿。
