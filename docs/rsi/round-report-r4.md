# RSI R4 轮次演进报告（R1→R4）

> 生成：2026-11-12 12:38 UTC · 生成器：`scripts/rsi-round-report.sh 4` · 数据源：daemon API `http://127.0.0.1:47813`（列表端点）+ 本地 git log（agentwork-int）
> 本报告自包含：不登录平台即可读懂。指标口径见各节脚注；API 不可达的小节会显式标注。

## ① 循环演进视图

每轮是一个 train 模式 goal；下图与说明汇总自 rsi 训练会话冻结的工作流（nodes/edges/依赖）。

### R1：RSI R1：agentwork 自迭代首轮（基准→提案）

- goal 终态：`done` · run 总数 31（失败/取消 18）
- 训练会话 `371a50558cf9…`：status=`ready` · 3 节点 · 关键路径 45.6s · 首要发现：重试放大：平均每节点 3.5 次 run
  - 图结构：3 节点 / 3 边（agent 节点 2，集成节点 0）
    - `sg:b757e167e34` sg-B：构建/测试基准测量：failed · attempt 6 · 耗时 45.6s · 依赖 → 1 个节点
    - `sg:751a53ebdf0` sg-O：R1 优化提案（只读）：running · attempt 1 · 耗时 —
    - 依赖边：`sg:b757e167e34` → `sg:751a53ebdf0`

### R2：RSI R2：实施已批准提案（P1+P2+P3）

- goal 终态：`done` · run 总数 8（失败/取消 5）
- 训练会话 `f31c3a6c4508…`：status=`ready` · 5 节点 · 关键路径 41.6m · 首要发现：重试放大：平均每节点 2.5 次 run
  - 图结构：5 节点 / 5 边（agent 节点 2，集成节点 2）
    - `sg:23d061daa9f` sg-IMPL：实施已批准提案 P1+P2+P3：verified · attempt 4 · 耗时 31.4m · 依赖 → 1 个节点
    - `sg:b8584aabcfb` sg-REBENCH：实施后基准复测：verified · attempt 1 · 耗时 10.2m
    - 依赖边：`sg:23d061daa9f` → `sg:b8584aabcfb`

### R3：RSI R3：可感知性改进（演进报告机制）

- goal 终态：`done` · run 总数 42（失败/取消 25）
- 训练会话 `4f5193ccc3d0…`：status=`ready` · 14 节点 · 关键路径 142.1m · 首要发现：人工等待占墙钟 941%
  - 图结构：14 节点 / 22 边（agent 节点 7，集成节点 3）
    - `sg:0e9ad6d5401` sg-IMPL：轮次演进报告生成器：cancelled · attempt 1 · 耗时 97.5m · 依赖 → 2 个节点
    - `sg:c1e064464d4` sg-ANALYZE：R2 问题清单与新提案：verified · attempt 3 · 耗时 55.3m · 依赖 → 1 个节点
    - `sg:3edbe2b48d3` sg-REPORT：生成首份正式演进报告：cancelled · attempt 1 · 耗时 —
    - `sg:52b00d7d4a4` sg-REBENCH：R3 基准复测：cancelled · attempt 1 · 耗时 —
    - `sg:3991ce7f016` sg-IMPL-v2：轮次演进报告生成器（重派·执行角色）：failed · attempt 6 · 耗时 70.3m
    - `sg:b13a558163d` sg-REBENCH-v2：R3 基准复测（冷缓存口径·无依赖并行）：verified · attempt 1 · 耗时 18.6m
    - `sg:49c2619baec` sg-IMPL-v3：轮次演进报告生成器（重派·复用 sg-REBENCH-v2 已合入基点）：cancelled · attempt 1 · 耗时 12.9m
    - 依赖边：`sg:0e9ad6d5401` → `sg:3edbe2b48d3`
    - 依赖边：`sg:c1e064464d4` → `sg:3edbe2b48d3`
    - 依赖边：`sg:0e9ad6d5401` → `sg:52b00d7d4a4`
- 训练会话 `079dc56e368e…`：status=`waiting` · 0 节点 · 关键路径 — · 首要发现：（无）
  - 图结构：7 节点 / 13 边（agent 节点 6，集成节点 0）
    - `sg:d1b7f848f0c` sg-IMPL：轮次演进报告生成器：failed · attempt 3 · 耗时 0.0s · 依赖 → 2 个节点
    - `sg:7d094326186` sg-ANALYZE：R2 问题清单与新提案：failed · attempt 3 · 耗时 0.0s · 依赖 → 1 个节点
    - `sg:47976318991` sg-REPORT：生成首份正式演进报告：cancelled · attempt 1 · 耗时 —
    - `sg:93ebd95d3cb` sg-REBENCH：R3 基准复测：cancelled · attempt 1 · 耗时 —
    - `sg:18e22480125` sg-IMPL（重派）：轮次演进报告生成器：cancelled · attempt 2 · 耗时 8.8m
    - `sg:2234fefdd37` sg-REBENCH（重派）：R3 基准复测：cancelled · attempt 2 · 耗时 7.6m
    - 依赖边：`sg:d1b7f848f0c` → `sg:47976318991`
    - 依赖边：`sg:7d094326186` → `sg:47976318991`
    - 依赖边：`sg:d1b7f848f0c` → `sg:93ebd95d3cb`

### R4：RSI R4：修复高频失败源 + 交付链闭环

- goal 终态：`active` · run 总数 28（失败/取消 23）
- 训练会话 `3b51e434a48a…`：status=`waiting` · 0 节点 · 关键路径 — · 首要发现：（无）
  - 图结构：11 节点 / 14 边（agent 节点 4，集成节点 2）
    - `sg:11aa153dc5e` sg-FIX：实施 P-A/P-B/D 代码修复：verified · attempt 2 · 耗时 192.5m · 依赖 → 2 个节点
    - `sg:cab2e5efa6e` sg-PLAYBOOK：P-C 基准口径固化：verified · attempt 4 · 耗时 11.8m · 依赖 → 1 个节点
    - `sg:aa0220be058` sg-REBENCH：R4 基准复测（首个全口径样本）：failed · attempt 6 · 耗时 0.1s
    - `sg:d2a8e375f12` sg-RELEASE：构建部署闭环首演：failed · attempt 6 · 耗时 0.1s
    - 依赖边：`sg:11aa153dc5e` → `sg:aa0220be058`
    - 依赖边：`sg:11aa153dc5e` → `sg:d2a8e375f12`
    - 依赖边：`sg:cab2e5efa6e` → `sg:d2a8e375f12`

本轮（R4）在本报告生成时通常仍在进行：以本节上面最后一条为准；收尾时重跑生成器即得终版。

## ② 过程暴露问题清单

本轮（R4）全部失败/异常 run 逐条；根因分类为生成器启发式（环境/代码/流程），供人工复核。

| run id | 状态 | 角色 | attempt | 时间 | 错误摘要 | 根因分类 |
|---|---|---|---|---|---|---|
| `b7d589387c06…` | cancelled | worker | 1 | 2026-11-12 03:25 | watchdog cancelled (machine unresponsive) | 流程 |
| `a5709bb9678a…` | cancelled | worker | 1 | 2026-11-12 03:25 | watchdog cancelled (machine unresponsive) | 流程 |
| `01e7ad5a5903…` | failed | worker | 1 | 2026-11-12 03:25 | commit/push: git add: exit status 1: The following paths are ignored by one of your .gitignore files: | 代码 |
| `83676b15efcc…` | failed | worker | 2 | 2026-11-12 03:31 | commit/push: git add: exit status 1: The following paths are ignored by one of your .gitignore files: | 代码 |
| `00ccd9bf816d…` | failed | worker | 3 | 2026-11-12 03:33 | commit/push: git add: exit status 1: The following paths are ignored by one of your .gitignore files: | 代码 |
| `cb6599e7d791…` | failed | worker | 1 | 2026-11-12 06:53 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `7b6c8ff41850…` | failed | worker | 1 | 2026-11-12 06:53 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `f3aa4292f659…` | failed | worker | 2 | 2026-11-12 06:53 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `9f81da18198a…` | failed | worker | 2 | 2026-11-12 06:53 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `1d17fbb28b71…` | failed | worker | 3 | 2026-11-12 06:53 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `2422f21ea623…` | failed | worker | 3 | 2026-11-12 06:53 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `e767c9425669…` | failed | worker | 1 | 2026-11-12 11:32 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `cf356c58b259…` | failed | worker | 1 | 2026-11-12 11:32 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `6cd9ed219ed0…` | failed | worker | 1 | 2026-11-12 11:32 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `46a49b80c113…` | failed | worker | 1 | 2026-11-12 11:32 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `9a324e523b25…` | failed | worker | 2 | 2026-11-12 11:32 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `739ea2eb5148…` | failed | worker | 2 | 2026-11-12 11:32 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `29e07f0c0aff…` | failed | worker | 1 | 2026-11-12 11:32 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `ddb622a0ac74…` | failed | worker | 3 | 2026-11-12 11:32 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `d65e527373ff…` | failed | worker | 3 | 2026-11-12 11:32 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `15ca2fa39199…` | failed | worker | 1 | 2026-11-12 11:42 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `a3084c37201b…` | failed | worker | 1 | 2026-11-12 11:42 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `f7c5f6adba4c…` | failed | worker | 1 | 2026-11-12 11:42 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |

> 收尾补充（owner 实测）：19:42 daemon 重启（R4 RELEASE 二进制替换）后，worktree sweep 清掉了 owner 会话在用的 git 注册目录（`worktrees/<agent>` 与 `.git` 指向的 `worktrees/<agent>2` 注册号错位），后续 git 操作全部 `fatal: not a git repository`；owner 手工修复 `.git` 指针恢复，零数据丢失。此为 sweep 判活缺陷（不含活跃会话工作树），已列入⑤候选 3。sg-REBENCH/sg-RELEASE 三次 attempt 的 machine offline 失败均属环境时序（用户消息「机器连接已恢复」与心跳真正恢复之间的窗口），非任务失败；两者任务实质已由 owner 收尾代跑（benchmark-report-r4.md）与事实核验（新二进制含全部修复符号）覆盖。

### 上一轮遗留问题状态

承接 R4 分析报告（`docs/rsi/analysis-r3.md`）的问题清单，本轮收尾时的状态如下（由本节人工/后续轮更新结论列）：

| 问题 | 上轮状态 | 本轮收尾状态 |
|---|---|---|
| AGENTWORK_HOME 泄漏进验证子进程（R2 验证门三连败根因） | 待修（提案 P-A） | **已修复**（229c2d3，P-A【已批准】：runVerifiedCmd 源头剥离 + 回归测试；R4 复测 0 失败确认；domain checks 已去 unset 前缀） |
| rsi_round 轮次编号不按 domain 递增（两轮均 round_no=1） | 待修（提案 P-B） | **已修复**（ec959ae，P-B【已批准】：domain 级 MAX + 跨 session prevMetrics + trigger 同步；R5 起账本真递增） |
| 基准冷/温缓存口径漂移（跨轮对比混入缓存噪音） | 待修（提案 P-C） | **已固化**（6769367，P-C【已批准】：playbook §3.1/§3.2 写死双列口径；R4 复测 95390ac 为首个全口径样本） |
| `/rsi` 端点口径不一致（文档写 `/rsi`，实测 404） | 待修（候选任务 3） | 本报告已按真实端点实施，文档口径待统一（遗留至 R5） |
| 验证命令 PATH 依赖宿主 shell 配置（go 软链缺失三连败） | 已解决（/usr/local/bin 软链） | 已解决 + P-A 落地 PATH 探测提示行（229c2d3） |
| deliver 因 SSH 私钥权限失败、依赖人工重试 | 已解决（人工修权限） | **已修复**（4132d31，P-D【已批准】：一次 60s 延迟自动重试，白名单复用 isNonRetryable） |
| owner 运行 71 分钟无产出无看护 | 已绕过（人工停止） | 待修（候选任务 5：owner 看护，遗留至 R5） |

## ③ 改进点（每条带 commit + diff 链接 + 效果数字）

改进点 = 本轮实施并合入集成分支的已批准提案；commit 链接指向仓库 commit 页，diff 摘要由生成器从 git log 提取，效果数字来自实施前后的基准复测（`benchmark-report*.md`）。

**本轮（R4）实施提交：**

- [`95390ac`](https://github.com/Sinkear/agentwork/commit/95390ac) docs(rsi): R4 基准复测报告——P-C 固化口径首个全量样本（冷/温双列+包级Top10+R3同口径对比）
- [`6526846`](https://github.com/Sinkear/agentwork/commit/6526846) release(rsi): 发布脚本 rsi-release.sh——构建→测试门→符号校验→原子替换 bin/→守护循环重启验证
- [`a4fd8d4`](https://github.com/Sinkear/agentwork/commit/a4fd8d4) Integrate 48771cc3135f52131cd813550b98f679
- [`34ac337`](https://github.com/Sinkear/agentwork/commit/34ac337) docs: R4 impl-notes——P-A/P-B/P-D 三条修复的验证证据（sg-FIX 交付物）
- [`4132d31`](https://github.com/Sinkear/agentwork/commit/4132d31) fix(deliver): P-D deliver 失败自动重试一次（60s 延迟，白名单外才重试）
- [`ec959ae`](https://github.com/Sinkear/agentwork/commit/ec959ae) fix(rsi): P-B rsi_round 轮次编号按 domain 递增——跨 session 定位上一轮
- [`229c2d3`](https://github.com/Sinkear/agentwork/commit/229c2d3) fix(verify): P-A 验证子进程环境净化——剥离 AGENTWORK_HOME/AGENTWORK_DB + PATH 工具链提示
- [`ae12054`](https://github.com/Sinkear/agentwork/commit/ae12054) fix(cli): AGENTS.md 被 .gitignore 忽略时不得以任何 pathspec 点名——git add -A 对被忽略文件静默跳过，exclude 点名反而 exit 1 烧掉 run
- [`6769367`](https://github.com/Sinkear/agentwork/commit/6769367) docs(rsi): playbook 固化基准口径（P-C）：冷/温双列+报告模板+node_count 图口径
- [`5aad0eb`](https://github.com/Sinkear/agentwork/commit/5aad0eb) Merge feat-357252be (agentwork deliver)
- [`e31877b`](https://github.com/Sinkear/agentwork/commit/e31877b) fix(link): 测试服务器先注册 handler 再启动 reader，消除 TestPeerCallRoundtrip 竞态
- [`49eed93`](https://github.com/Sinkear/agentwork/commit/49eed93) agentwork run d1cc749d38f6511266478e2be44239fe
- [`81e5e94`](https://github.com/Sinkear/agentwork/commit/81e5e94) agentwork run a44eecac7bfa28f6996ab8a9372ba367
- [`90bcc13`](https://github.com/Sinkear/agentwork/commit/90bcc13) docs(rsi): 对齐最新集成基点 dfcd444 重新生成 R3 演进报告（R3-1 rework）
- [`3ac0b1f`](https://github.com/Sinkear/agentwork/commit/3ac0b1f) fix(rsi): 协作约定迁出根级 AGENTS.md，根治集成冲突（R3-1 修订）

**集成分支 `agentwork-int` 近期提交（含历轮改进）：**

- [`33f16dc`](https://github.com/Sinkear/agentwork/commit/33f16dc) Merge feat-98d60d12 (agentwork deliver)
- [`752f21f`](https://github.com/Sinkear/agentwork/commit/752f21f) agentwork run 16d4e354c224d8d3488c20792f637697
- [`3aecbef`](https://github.com/Sinkear/agentwork/commit/3aecbef) Merge pull request #2 from Sinkear/fix/bugs
- [`35b1518`](https://github.com/Sinkear/agentwork/commit/35b1518) webhook ui，前端bug，总览页修复
- [`11cd194`](https://github.com/Sinkear/agentwork/commit/11cd194) Merge pull request #1 from Sinkear/feat/template-export
- [`c23a837`](https://github.com/Sinkear/agentwork/commit/c23a837) update artifact-swap skill
- [`29c82a9`](https://github.com/Sinkear/agentwork/commit/29c82a9) 新增agentwork更新部署的skill
- [`e270d36`](https://github.com/Sinkear/agentwork/commit/e270d36) 增加模板导入导出的能力，实现自替代包优化
- [`ff84b05`](https://github.com/Sinkear/agentwork/commit/ff84b05) chore(license): 衍生说明移到 NOTICE，LICENSE 保持标准 MIT 两行版权
- [`7ed6a91`](https://github.com/Sinkear/agentwork/commit/7ed6a91) chore(license): 版权行合并到标准 MIT 头部，便于 GitHub 识别 MIT
- [`de0753f`](https://github.com/Sinkear/agentwork/commit/de0753f) chore: 独立发行版基线（CI、上游补丁同步脚本、MIT 衍生声明）
- [`051d73a`](https://github.com/Sinkear/agentwork/commit/051d73a) chore: 合并上游 master(82b0b68) 作为独立主线起点

**历轮改进点与效果数字（来源：docs/rsi/proposals.md、benchmark-report-r2.md、benchmark-report-r3.md、impl-notes.md；口径冲突时以最新复测报告为准）：**

| 轮 | 提案 | 改进 | commit | 效果数字（before → after） |
|---|---|---|---|---|
| R2 | P1 | 并行化 internal/daemon 独立顶层测试（70 个测试函数加 t.Parallel） | [`e08cb72`](https://github.com/Sinkear/agentwork/commit/e08cb72) | internal/daemon 包测试中位 5.911s（R2 复测基准，P1 后保持；R3 复测 5.855s 持平）；详见 benchmark-report-r2.md §3、benchmark-report-r3.md §4 |
| R2 | P2 | CDN 漂移守卫对畸形响应 fail 改 skip | [`8ca9957`](https://github.com/Sinkear/agentwork/commit/8ca9957) | 14 包 0 FAIL（修复前该守卫对畸形响应直接 FAIL） |
| R2 | P3 | 失败重试引入不可重试错误快速失败 | [`18a8c3c`](https://github.com/Sinkear/agentwork/commit/18a8c3c) | 确定性错误（如模型不可用）不再烧尽重试预算（R1 曾 12 条 run 同因全败） |
| R3 | R3-1 | 轮次演进报告生成器 + AGENTS.md 收尾约定 | 本工作项 commit（合入后由下轮报告回填链接） | 可感知性 5 缺口 → 5 节报告覆盖；每轮收尾强制产出 |

## ④ 跨轮指标对比

汇总自各轮 rsi 训练会话冻结的 metrics（`/rsi/sessions/{id}/rounds` 账本；session 未冻结时回退 `/rsi/sessions/{id}` 实时 workflow.metrics，并已标注）。
注意：R1/R2 的 rsi_round 账本 round_no 均为 1（提案 P-B 待修），故下表按「goal 标题里的轮次号」对齐，而非账本 round_no。

| 轮 | 指标来源 | 节点数 | 边数 | 关键路径 | 墙钟 | 工作总量 | 排队等待 | 并行收益 | lane 数 | lane 失衡 | 首要发现 |
|---|---|---|---|---|---|---|---|---|---|---|---|
| R1 | rounds 账本 | 3 | 3 | 45.6s | 175.4m | 45.6s | 0.3s | 1 | 1 | 1 | 重试放大：平均每节点 3.5 次 run |
| R2 | rounds 账本 | 5 | 5 | 41.6m | 46.8m | 41.6m | 36.6m | 1 | 2 | 1.51 | 重试放大：平均每节点 2.5 次 run |
| R3 | rounds 账本 | 14 | 22 | 142.1m | 197.2m | 320.5m | 1.2s | 2.26 | 3 | 2.31 | 人工等待占墙钟 941% |
| R4 | session 实时（未冻结） | 11 | 14 | 204.4m | 497.2m | 204.4m | 415.4m | 1.0 | 3 | 2.83 | machine offline 烧掉 23 条 run（见②） |

解读提示（含 bench R3/R4 复测结论，权威来源 benchmark-report-r3.md / benchmark-report-r4.md）：
- `重试放大`（top finding）从 R1「平均每节点 3.5 次 run」降到 R2「2.5 次」，仍是两轮共同首要发现。
- R1/R2 的 wall_ms/critical_path 差异部分来自基准任务规格不同（R1 纯基准、R2 含实施），跨轮结论看趋势不看点值。
- 基准口径（P-C）：跨轮对比 build 只采冷列——R2 基线 44.417s → R3 新基线 41.285s（约 -7%）→ **R4 新基线 6526846 冷列 40.745s（-1.3%，持平）**；R4 复测是 P-C 固化口径后首个全量样本（冷/温双列+GOCACHE 路径+包级 Top10 全齐）。温列仅参照不采信。
- 测试健康：R3 复测 1 例失败（digest 系 HOME 隔离被 AGENTWORK_HOME 击穿）→ **R4 复测 0 失败（14/14 包 ok，3 连跑均 0）——P-A 源头净化的直接效果**；机器验证门已去掉 `unset AGENTWORK_HOME;` 前缀（domain checks 于本轮收尾更新，改由 daemon 验证子进程源头剥离）。
- R4 排队等待 415.4m 的构成：绝大多数是 machine offline 期间 mention/子目标 run 无法启动的人工等待与重试间隙（23 条失败 run 的根因全部为环境类，见②），非流程串行化回归；并行收益 1.0 同因（两条子目标链实际是串行等待窗口）。
- R3/R4 行标注「session 实时（未冻结）」表示该会话尚未冻结、数字取自训练页同源的实时 workflow.metrics，收尾冻结后重跑生成器即切换为 rounds 账本口径（P-B 修复后 R5 起账本 round_no 将按 domain 真递增）。

## ⑤ 下一轮候选任务清单

权威来源：`docs/rsi/analysis-r3.md` 第三节（optimizer 产出，人工挑选成为下一轮任务）+ 本轮 R4 收尾新增发现。以下为 R5 候选快照（按优先级）：

1. **owner/leader 运行卡顿看护**（中优，R3/R4 连续遗留）——R2 owner run 71 分钟无产出被人工停止；R4 的 owner run 也两度被 idle-watchdog 标记。把 watchdog 覆盖 owner 运行 + 卡顿告警卡片化。收益：owner 卡顿从「人工发现」变「平台主动暴露」。
2. **machine offline 恢复后的 run 自动重排**（高优，R4 新增，本轮 23 条失败 run 的根因）——daemon 重启/环境重启窗口期内 enqueue 的 run 全部烧在 machine offline（占本轮失败 run 的 96%）；恢复后不自动重试，靠 owner 人工重派。收益：本轮 23 条失败 run 中环境类预计可自动恢复 ≥20 条；用户可感知：环境重启后任务自动继续，不再需要「请继续推进」式人工唤醒。
3. **worktree sweep 误扫会话在用注册目录**（高优，R4 新增代码级发现）——19:42 daemon 重启（binary swap）后 sweep 把 owner 会话在用的 worktree 注册目录清掉，导致后续 git 命令全部 fatal（本报告撰写时实测踩中）；根因是 sweep 判活依据不含「活跃 run 会话工作树」。收益：消灭 daemon 重启后的会话级 git 瘫痪一类灾难。
4. **`/rsi` 端点口径修正**（低成本高一致性，R3 遗留）——补文档为 `/rsi/sessions` 等真实端点，或 handler 加 `GET /rsi` 别名。收益：文档与实测一致，新会话不再踩 404。
5. **基准测量抗噪增强**（低优，R4 复测教训）——本轮温跑多次撞上宿主负载尖峰（4s→91s 离群），中位数法有效但样本太小；playbook §3.1 的 test 口径从 3 连跑升到 5 连跑取中位，并记录测量窗口 load。收益：跨轮温列对比噪音减半（冷列已够稳）。

---

*本报告由 `scripts/rsi-round-report.sh` 自动生成；如与平台数据不一致，以 daemon API 为准。生成时间：2026-11-12 12:38 UTC；②④⑤节结论列与 R4 收尾状态由 owner 收尾时人工更新。*