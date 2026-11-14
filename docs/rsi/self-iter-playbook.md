# agentwork 自迭代手册（用 agentwork 迭代 agentwork）

> 场景：觉得 agentwork 执行任务比「裸 agent」慢，怀疑编排层开销（排队/派发/校验/评审/串行化）。
> 方法：让 agentwork 跑自己的基准任务，用 W-RSI 训练分析量化开销，产出只读提案，人工批准后改造，下一轮复测对比。
> 本文档是操作手册：团队怎么建、每轮怎么跑、指标怎么读。

## 1. 循环结构（每轮 = 一个 train 模式 goal）

```
R<n> goal (execution_mode=train, assignee=squad self-iter)
 ├─ sg-B  bench 基准任务            ← 无依赖，立即执行（它是"被测负载"）
 ├─ sg-O  optimizer 瓶颈提案        ← depends_on [sg-B]（基准完成才分析）
 │          ↓ goal review（人工闸门：批准/打回提案）
 └─ (R<n+1>) sg-B 复测同一基准      ← 对比 rsi_round 趋势：改没改对，数字说话
```

- sg-B 每轮**同一个任务模板**（只有轮次标记不同），保证轮间可比。
- sg-O 产出 `docs/rsi/proposals.md`（只读提案：假设/证据/预期效果/复测计划/风险）。
- 人工在 goal review 批准后，builder 才实施（`sg-Bd`，depends_on sg-O）；T6 约束：平台永不自动改自己。
- 每轮分析冻结进 `rsi_round`（round_no + delta_json），训练页「洞察」tab 看趋势。

## 2. 一次性搭建（已完成，实体 ID 见 /tmp/selfiter.env）

| 实体 | ID | 说明 |
|---|---|---|
| machine | `9a5bce86…` (sinkear-dev) | `build/agentwork connect -name sinkear-dev` 常驻（nohup），它负责在本机 spawn opencode |
| runtime | `fc6354ff…` (opencode@sinkear-dev) | args=`["opencode","acp"]`（由探测自动 upsert） |
| domain | `a6f7ec84…` (agentwork-self) | type=repo，`git@github.com:Sinkear/agentwork.git`（**必须 SSH**，HTTPS 无凭证） |
| agent bench | `1d5ff0af…` | 基准执行者：直接干活，不拆分/不咨询/不加流程 |
| agent optimizer | `0b55bf51…` | RSI 分析师：读 /rsi/* API，写只读提案；squad leader |
| agent builder | `b7efb1ce…` | 改造执行者：只实施 goal 评论中标注【已批准】的提案 |
| squad | `9733828a…` (self-iter) | leader=optimizer，成员=bench+builder |

踩坑记录（2026-11-08 实测）：
1. runtime 必须 machine 挂靠——无 machine 的 runtime 被 daemon 拒绝 spawn（`agentwork connect` 是唯一正道）。
2. opencode v2.2.5 不认 `acp --pure`（打印 usage 即退出 → "transport closed"），探测表已修为 `opencode acp`。
3. 域名 git_url 用 SSH；HTTPS 走 ghfast.top 代理只读可通、push 无凭证必败。
4. **GitHub main 有 repository rule 禁直推**（deliver push 被拒）→ 已建不受保护集成分支 `agentwork-int`，domain.default_branch 已指向它；每轮 deliver 合并进 agentwork-int，人工定期 PR 到 main。
5. train goal 经 review 批准→deliver 完成时才触发训练分析（MarkDelivered 已接 OnGoalTerminal）；若分析缺失用 `POST /rsi/sessions/{id}/retry` 兜底。

## 3. 每轮操作（人 = 编排者）

```bash
source /tmp/selfiter.env   # SQUAD / DOMAIN / 上一轮 ID

# ① 建轮 goal（train 模式 → 终态自动冻结工作流 + 起分析）
GOAL=$(curl -s -X POST localhost:47813/goals -H 'Content-Type: application/json' -d '{
 "title":"RSI 自迭代 R<n>：基准测量", "description":"…",
 "domain_id":"'"$DOMAIN"'", "assignee_type":"squad", "assignee_id":"'"$SQUAD"'",
 "status":"active", "execution_mode":"train"}' | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')

# ② sg-B：基准任务（改轮次标记即可）
SGB=$(curl -s -X POST localhost:47813/goals/$GOAL/sub-goals -H 'Content-Type: application/json' \
 -d '{"title":"sg-B 基准任务：round-<n> 标记","description":"<固定基准任务>","assignee_id":"1d5ff0aff4dc6780479aa16e6c4e1c02"}' | python3 -c 'import json,sys;print(json.load(sys.stdin)["id"])')

# ③ sg-O：依赖 sg-B（未满足前不派发；sg-B verified 后自动释放）
curl -s -X POST localhost:47813/goals/$GOAL/sub-goals -H 'Content-Type: application/json' \
 -d '{"title":"sg-O RSI 分析提案 round-<n>","description":"…","assignee_id":"0b55bf515c4467fac8c68825749d90b5","depends_on":["'"$SGB"'"]}'

# ④ 看进度
curl -s localhost:47813/goals/$GOAL/sub-goals | python3 -m json.tool
tail -f /tmp/agentwork-daemon.log

# ⑤ sg-O 完成后：训练页读分析，review 批准提案；需要 builder 时再建 sg-Bd（depends_on sg-O）
# ⑥ 复测对比：GET /rsi/sessions/{id}/rounds（轮次账本）或 /rsi/overview 的 trend（同样逐条取自 rsi_round）
```

## 4. 指标怎么读（回答「为什么比裸 agent 慢」）

训练页（/training）→ 编排 tab / 洞察 tab：

| 指标 | 含义 | 慢的证据 |
|---|---|---|
| `dependency_wait_ms` | sg 创建→其依赖全部 run 结束（ready）的累计等待 | 大 = 依赖门控太长（前置链没并行） |
| `dispatch_wait_ms` | ready→首个 sub_goal run 开始的累计等待 | 大 = 派发/认领链路开销 |
| `queue_wait_ms` | **legacy 兼容别名** = 上两者之和 | 仅用于旧报表；新分析看拆分字段 |
| `critical_path_ms` | explicit 依赖链的最短墙钟（排除 inferred seq） | 接近 wall = 依赖过度串行 |
| `parallel_speedup` | work_sum ÷ wall（实际吞吐加速比；串行=1.0） | 接近 1 = 没吃到并行 |
| `serialization_loss_ms` | wall − critical_path（排队/派发/抖动） | 大 = 依赖声明过度或分派太碎 |
| `verify_overhead_ratio` | verify 忙时÷work_sum（review 单独计） | 大 = 校验流程吃掉执行时间 |
| `review_busy_ms` / `review_overhead_ratio` | 评审忙时 / 评审忙时÷work_sum | 大 = 评审粒度太细或评审本身慢 |
| `human_wait_share` | human_wait_ms÷(wall+human_wait)（0..1） | ≥0.30 = 人工闸门是主要等待 |
| `retry_amplification` | agent 节点的**最大** attempt（float） | ≥2 = 单个子任务反复重试 |
| findings（9 类 + human_wait） | 结构/分派/信息流瓶颈的机器判据 | serial_chain / merge_candidate / ping_pong… |

判定「可优化」四条件：统计显著（对比**同 domain** baseline 中位数；样本 <5 时 baseline 为空，只做单次观察，不声称显著）、可归因（finding + 具体节点/边）、可干预（改 depends_on/分派/闸门粒度）、可回滚。

## 5. 铁律

- **T6**：平台代码的自改必须走「人工批准提案 → builder 实施 → 下一轮复测」；任何自动改元层的路径都不允许。该约束是协作约定，**无代码强制**（没有拦截器或守卫，违反只能靠 review 发现）。
- sg-O 只读：分析 agent 不得修改业务代码。
- 提案上限 3 条/轮，按预期收益排序（约定，无代码强制）；复测计划必须写进提案（下一轮 sg-B 如何验证）。
- bench 的任务每轮保持同构；改基准 = 重新建立基线（旧趋势作废）。

## 6. 每轮收尾：轮次演进报告（强制协作约定）

- 每个 RSI 自迭代 goal（R1、R2、…）到达终态前，**必须**运行
  `scripts/rsi-round-report.sh <轮次号N>`（N 可写 `3` 或 `R3`），生成
  `docs/rsi/round-report-r<N>.md` 并随本轮交付一并提交（commit message 用
  `docs(rsi): R<N> 轮次演进报告`）。
- 报告固定五节，缺一不可，节名与生成器输出一致：
  【① 本轮概览】【② 失败与取消】【③ 提交清单】【④ 指标】【⑤ 候选项】。
  - ① 本轮概览：goals/sessions、node_count、critical_path、parallel_speedup、top findings；
  - ② 失败与取消：run 明细、attempt、result_summary / cancel_reason；
  - ③ 提交清单：git log 区间（`--int-branch` + 轮次时间窗），**不是**逐条改进的
    commit+diff 链接；diff 需点开 commit 查看，效果数字仍来自基准复测报告；
  - ④ 指标：每个 session / `rsi_round` 的 metrics 与 delta（缺失写「无」）；
  - ⑤ 候选项：从 findings 的 category / severity / title / suggestion 派生，
    **需人工判断，不自动采纳**（不是批准列表）。
- 生成器是数据驱动的：脚本不含任何轮次结论/数字；必需 API 失败会 stderr + 退出码 1
  且不写输出文件；可选数据缺失在对应节标注「数据不可得」。`--dry-run` 只输出到
  stdout 便于审阅。轮次标题匹配同时支持 `RSI R<N>` 与 `RSI 自迭代 R<N>`。
- ③ 的 commit 范围按集成分支（默认 `agentwork-int`）+ 本轮时间窗（goal 最早
  `created_at` → session 最晚 `sealed_at/finished_at`）过滤；区间内无 commit 时
  退化最近 30 条，并在报告中标注口径。
- 报告必须自包含：不依赖读者登录平台即可理解；数据源为 daemon API 列表端点
  （`/rsi/sessions`、`/rsi/sessions/{id}`、`/goals`、`/goals/{id}/runs`、
  `/goals/{id}/comments`、`/rsi/sessions/{id}/rounds`）与本地 git log。
  注意：`/rsi` 与 `/runs/{id}` 单查端点为 404，不要使用。
- 报告中的效果数字（before/after）必须来自基准复测报告（`benchmark-report*.md`），
  不得凭记忆填写；API 不可得的小节显式标注，不得伪造。
- 下一轮的 optimizer 分析与本轮收尾验证都以最新一份 round-report 为起点。
- **路径约定**：本约定宿主在 `docs/rsi/self-iter-playbook.md`（随仓库跟踪、
  集成无冲突）。不要把本节写入仓库根 `AGENTS.md`——根级 AGENTS.md 是
  agentwork 平台每次 run 注入 agent 简报的路径（运行时未跟踪文件），跟踪它
  会让任何 change integrate 的 `git merge` 因「untracked working tree files
  would be overwritten」被拒（R3 实测两次集成冲突即此根因）；`.gitignore`
  已将 `AGENTS.md`/`AGENTWORK.md` 列为忽略项。
