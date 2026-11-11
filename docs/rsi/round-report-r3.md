# RSI R3 轮次演进报告（R1→R3）

> 生成：2026-11-11 09:36 UTC · 生成器：`scripts/rsi-round-report.sh 3` · 数据源：daemon API `http://127.0.0.1:47813`（列表端点）+ 本地 git log（agentwork-int-v2）
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

- goal 终态：`active` · run 总数 39（失败/取消 22）
- 训练会话 `4f5193ccc3d0…`：status=`waiting` · 0 节点 · 关键路径 — · 首要发现：（无）
  - 图结构：14 节点 / 22 边（agent 节点 7，集成节点 3）
    - `sg:0e9ad6d5401` sg-IMPL：轮次演进报告生成器：cancelled · attempt 1 · 耗时 97.5m · 依赖 → 2 个节点
    - `sg:c1e064464d4` sg-ANALYZE：R2 问题清单与新提案：verified · attempt 3 · 耗时 55.3m · 依赖 → 1 个节点
    - `sg:3edbe2b48d3` sg-REPORT：生成首份正式演进报告：cancelled · attempt 1 · 耗时 —
    - `sg:52b00d7d4a4` sg-REBENCH：R3 基准复测：cancelled · attempt 1 · 耗时 —
    - `sg:3991ce7f016` sg-IMPL-v2：轮次演进报告生成器（重派·执行角色）：running · attempt 5 · 耗时 45.0m
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

本轮（R3）在本报告生成时通常仍在进行：以本节上面最后一条为准；收尾时重跑生成器即得终版。

## ② 过程暴露问题清单

本轮（R3）全部失败/异常 run 逐条；根因分类为生成器启发式（环境/代码/流程），供人工复核。

| run id | 状态 | 角色 | attempt | 时间 | 错误摘要 | 根因分类 |
|---|---|---|---|---|---|---|
| `642a21d49e02…` | failed | worker | 1 | 2026-11-11 07:31 | verification failed: | 环境 |
| `e83e70cf75a3…` | failed | worker | 1 | 2026-11-11 07:31 | verification failed: | 环境 |
| `f1182ff5ffcf…` | failed | worker | 2 | 2026-11-11 07:48 | verification failed: | 环境 |
| `9eb08fde37a0…` | failed | worker | 1 | 2026-11-11 09:01 | commit/push: push feat-357252be-sg-3991ce7f: exit status 1: To github.com:Sinkear/agentwork.git | 代码 |
| `92fc64e0ad97…` | failed | worker | 2 | 2026-11-11 09:05 | commit/push: push feat-357252be-sg-3991ce7f: exit status 1: To github.com:Sinkear/agentwork.git | 代码 |
| `7a9f0ad5301c…` | failed | worker | 1 | 2026-11-11 06:46 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `941d8a6e14da…` | failed | worker | 1 | 2026-11-11 06:46 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `8052fa405818…` | failed | worker | 1 | 2026-11-11 06:46 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `d2d5065cfd0e…` | failed | worker | 2 | 2026-11-11 06:46 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `31e5666d9c3a…` | failed | worker | 2 | 2026-11-11 06:46 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `2b88eab9a066…` | failed | worker | 3 | 2026-11-11 06:46 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `812174d809ee…` | failed | worker | 3 | 2026-11-11 06:46 | machine offline — the machine must run `agentwork connect` (machine 9f00055a-89c8-4130-8f44-24ea2166e049) | 环境 |
| `f4c76ca53811…` | failed | worker | 2 | 2026-11-11 06:46 | verification failed: | 环境 |
| `a32bea54e21e…` | cancelled | worker | 1 | 2026-11-11 07:19 | (被取消，无错误摘要) | 流程 |
| `e1121ff1bf1f…` | cancelled | worker | 1 | 2026-11-11 07:21 | (被取消，无错误摘要) | 流程 |
| `3a46ce6d6a04…` | cancelled | worker | 3 | 2026-11-11 07:23 | (被取消，无错误摘要) | 流程 |
| `d7a1a90c7ad2…` | failed | worker | 1 | 2026-11-11 06:35 | load config: sql: Scan error on column index 3, name "id": converting NULL to string is unsupported | 流程 |
| `9decf713dee2…` | failed | worker | 1 | 2026-11-11 06:35 | load config: sql: Scan error on column index 3, name "id": converting NULL to string is unsupported | 流程 |
| `ccedfe866595…` | failed | worker | 2 | 2026-11-11 06:35 | load config: sql: Scan error on column index 3, name "id": converting NULL to string is unsupported | 流程 |
| `057b1a71533f…` | failed | worker | 2 | 2026-11-11 06:35 | load config: sql: Scan error on column index 3, name "id": converting NULL to string is unsupported | 流程 |
| `303f02e65498…` | failed | worker | 3 | 2026-11-11 06:35 | load config: sql: Scan error on column index 3, name "id": converting NULL to string is unsupported | 流程 |
| `6e8220574d9f…` | failed | worker | 3 | 2026-11-11 06:35 | load config: sql: Scan error on column index 3, name "id": converting NULL to string is unsupported | 流程 |

### 上一轮遗留问题状态

承接 R3 分析报告（`docs/rsi/analysis-r3.md`）的问题清单，本轮收尾时的状态如下（由本节人工/后续轮更新结论列）：

| 问题 | 上轮状态 | 本轮收尾状态 |
|---|---|---|
| AGENTWORK_HOME 泄漏进验证子进程（R2 验证门三连败根因） | 待修（提案 P-A） | 待评审；bench R3 复测确认同一泄漏还击穿 go test 子进程的 t.Setenv 隔离（6 例失败根因 A/A'/B），P-A 实施时应一并覆盖测试进程环境 |
| rsi_round 轮次编号不按 domain 递增（两轮均 round_no=1） | 待修（提案 P-B） | 待评审 |
| 基准冷/温缓存口径漂移（跨轮对比混入缓存噪音） | 待修（提案 P-C） | P-C 口径已执行：R3 复测（8295f7a）按冷/温双列产出，跨轮对比采冷列（R2 44.417s → R3 41.285s，约 -7%） |
| `/rsi` 端点口径不一致（文档写 `/rsi`，实测 404） | 待修（候选任务 3） | 本报告已按真实端点实施，文档口径待统一 |
| 验证命令 PATH 依赖宿主 shell 配置（go 软链缺失三连败） | 已解决（/usr/local/bin 软链） | 已解决，P-A 落地时固化 PATH 语义 |
| deliver 因 SSH 私钥权限失败、依赖人工重试 | 已解决（人工修权限） | 待修（候选任务 4：deliver 自动重试） |
| owner 运行 71 分钟无产出无看护 | 已绕过（人工停止） | 待修（候选任务 5：owner 看护） |

## ③ 改进点（每条带 commit + diff 链接 + 效果数字）

改进点 = 本轮实施并合入集成分支的已批准提案；commit 链接指向仓库 commit 页，diff 摘要由生成器从 git log 提取，效果数字来自实施前后的基准复测（`benchmark-report*.md`）。

**本轮（R3）实施提交：**

- [`90bcc13`](https://github.com/Sinkear/agentwork/commit/90bcc13) docs(rsi): 对齐最新集成基点 dfcd444 重新生成 R3 演进报告（R3-1 rework）
- [`3ac0b1f`](https://github.com/Sinkear/agentwork/commit/3ac0b1f) fix(rsi): 协作约定迁出根级 AGENTS.md，根治集成冲突（R3-1 修订）
- [`a47f403`](https://github.com/Sinkear/agentwork/commit/a47f403) docs(rsi): 对齐新集成基点——报告吸收 bench R3 复测权威数字（解决集成冲突）
- [`c3af19e`](https://github.com/Sinkear/agentwork/commit/c3af19e) docs(rsi): R3 轮次演进报告回填本工作项 commit 链接
- [`d5b4651`](https://github.com/Sinkear/agentwork/commit/d5b4651) feat(rsi): 轮次演进报告生成器 + R3 首份演进报告 + AGENTS.md 收尾约定（R3-1）
- [`dfcd444`](https://github.com/Sinkear/agentwork/commit/dfcd444) Integrate 12e9a4dd631b729c2734f3cf0f174ec9
- [`8295f7a`](https://github.com/Sinkear/agentwork/commit/8295f7a) bench: R3 基准复测报告（冷缓存口径·冷/温双列·6例测试隔离缺陷根因归类）
- [`0bb81bb`](https://github.com/Sinkear/agentwork/commit/0bb81bb) Integrate 2c5f00af0966f59d50db0616a16f412c
- [`d420946`](https://github.com/Sinkear/agentwork/commit/d420946) docs(rsi): R3 分析复核——补充 R3 前置轮 PATH 127 三连败（642a21d4/e83e70cf/f1182ff5）为问题 #4b/#16，P-A 增补 verify 子进程 PATH 语义探测，P-B 补 prevMetrics 跨轮读取行号修正
- [`c3126ff`](https://github.com/Sinkear/agentwork/commit/c3126ff) docs(rsi): R3 分析复核——按 API 全量修正 R1 头部统计（15 failed/3 cancelled）并补齐 634c78a8/9cd850bf/326129752 三条遗漏 run
- [`eab6b4a`](https://github.com/Sinkear/agentwork/commit/eab6b4a) docs(rsi): R3 分析报告——R2 过程暴露问题清单 + 3 条新提案 + 下一轮候选任务
- [`ff797d2`](https://github.com/Sinkear/agentwork/commit/ff797d2) docs(rsi): R3 分析——R2 全量失败 run 清单与三份新提案（AGENTWORK_HOME 泄漏 / rsi_round 编号 / bench 口径）
- [`e7c2572`](https://github.com/Sinkear/agentwork/commit/e7c2572) docs: R1/R2 自迭代交付物迁移至新基线（proposals/benchmark/impl-notes）
- [`e08cb72`](https://github.com/Sinkear/agentwork/commit/e08cb72) test(P1): 并行化 internal/daemon 独立顶层测试（R2 提案1在新基线重放）
- [`e2bda1d`](https://github.com/Sinkear/agentwork/commit/e2bda1d) test(P2)+feat(P3): R2 已批准提案在新基线（agentwork-int-v2）语义化重放

**集成分支 `agentwork-int-v2` 近期提交（含历轮改进）：**

- [`e7c2572`](https://github.com/Sinkear/agentwork/commit/e7c2572) docs: R1/R2 自迭代交付物迁移至新基线（proposals/benchmark/impl-notes）
- [`e08cb72`](https://github.com/Sinkear/agentwork/commit/e08cb72) test(P1): 并行化 internal/daemon 独立顶层测试（R2 提案1在新基线重放）
- [`e2bda1d`](https://github.com/Sinkear/agentwork/commit/e2bda1d) test(P2)+feat(P3): R2 已批准提案在新基线（agentwork-int-v2）语义化重放
- [`112d50f`](https://github.com/Sinkear/agentwork/commit/112d50f) Merge pull request #3 from Sinkear/feat/workflow-rsi
- [`95bab5e`](https://github.com/Sinkear/agentwork/commit/95bab5e) docs(rsi): playbook 补 agentwork-int 集成分支与 retry 兜底
- [`cab2fbf`](https://github.com/Sinkear/agentwork/commit/cab2fbf) fix(rsi): deliver 终态接入训练分析 + retry 放开 waiting
- [`284970a`](https://github.com/Sinkear/agentwork/commit/284970a) 修复管家对话bug新建任务
- [`d71507d`](https://github.com/Sinkear/agentwork/commit/d71507d) feat(rsi): sub-goal depends_on 执行门控 + opencode 探测修复
- [`777d744`](https://github.com/Sinkear/agentwork/commit/777d744) workflow rsi 修改左侧栏样式
- [`4289095`](https://github.com/Sinkear/agentwork/commit/4289095) workflow rsi mvp version
- [`3aecbef`](https://github.com/Sinkear/agentwork/commit/3aecbef) Merge pull request #2 from Sinkear/fix/bugs
- [`b9259ea`](https://github.com/Sinkear/agentwork/commit/b9259ea) feat(rsi): RSI workflow — 训练任务、分析服务与前端页面

**历轮改进点与效果数字（来源：docs/rsi/proposals.md、benchmark-report-r2.md、benchmark-report-r3.md、impl-notes.md；口径冲突时以最新复测报告为准）：**

| 轮 | 提案 | 改进 | commit | 效果数字（before → after） |
|---|---|---|---|---|
| R2 | P1 | 并行化 internal/daemon 独立顶层测试（70 个测试函数加 t.Parallel） | [`e08cb72`](https://github.com/Sinkear/agentwork/commit/e08cb72) | internal/daemon 包测试中位 5.911s（R2 复测基准，P1 后保持；R3 复测 5.855s 持平）；详见 benchmark-report-r2.md §3、benchmark-report-r3.md §4 |
| R2 | P2 | CDN 漂移守卫对畸形响应 fail 改 skip | [`8ca9957`](https://github.com/Sinkear/agentwork/commit/8ca9957) | 14 包 0 FAIL（修复前该守卫对畸形响应直接 FAIL） |
| R2 | P3 | 失败重试引入不可重试错误快速失败 | [`18a8c3c`](https://github.com/Sinkear/agentwork/commit/18a8c3c) | 确定性错误（如模型不可用）不再烧尽重试预算（R1 曾 12 条 run 同因全败） |
| R3 | R3-1 | 轮次演进报告生成器 + AGENTS.md 收尾约定 | 本工作项 commit（合入后由下轮报告回填链接） | 可感知性 5 缺口 → 5 节报告覆盖；每轮收尾强制产出 |

## ④ 跨轮指标对比

汇总自各轮 rsi 训练会话冻结的 metrics（`/rsi/sessions/{id}` 与 `/rsi/sessions/{id}/rounds`）。
注意：R1/R2 的 rsi_round 账本 round_no 均为 1（提案 P-B 待修），故下表按「goal 标题里的轮次号」对齐，而非账本 round_no。

| 轮 | 节点数 | 边数 | 关键路径 | 墙钟 | 工作总量 | 排队等待 | 并行收益 | lane 数 | lane 失衡 | 首要发现 |
|---|---|---|---|---|---|---|---|---|---|---|
| R1 | 3 | 3 | 45.6s | 175.4m | 45.6s | 0.3s | 1 | 1 | 1 | 重试放大：平均每节点 3.5 次 run |
| R2 | 5 | 5 | 41.6m | 46.8m | 41.6m | 36.6m | 1 | 2 | 1.51 | 重试放大：平均每节点 2.5 次 run |
| R3 | — | — | — | — | — | — | — | — | — | — |

解读提示（含 bench R3 复测结论，权威来源 benchmark-report-r3.md）：
- `重试放大`（top finding）从 R1「平均每节点 3.5 次 run」降到 R2「2.5 次」，仍是两轮共同首要发现。
- R1/R2 的 wall_ms/critical_path 差异部分来自基准任务规格不同（R1 纯基准、R2 含实施），跨轮结论看趋势不看点值。
- 基准口径（P-C）：跨轮对比 build 只采冷列——R2 基线 44.417s → R3 新基线 e7c2572 41.285s（约 -7%），R2 已批准提案收益在新基线重建中得到保持；温列（R2 中位 4.387s / R3 中位 4.672s）仅参照不采信。
- 测试健康：R2 全绿 → R3 复测 6 例失败（2 包），全部为测试环境隔离缺陷（AGENTWORK_HOME 全局泄漏击穿 t.Setenv 隔离 + digest 跨运行残留），非业务代码缺陷；机器验证门以 `unset AGENTWORK_HOME;` 前缀口径运行为全绿。
- R3 本轮会话尚未冻结（goal 进行中），收尾后重跑生成器补入。

## ⑤ 下一轮候选任务清单

权威来源：`docs/rsi/analysis-r3.md` 第三节（optimizer 产出，人工挑选成为下一轮任务）。以下为该清单的当前快照：

1. **实施 P-A：验证子进程环境净化**（高优）——R2 验证门三连败 4 条失败 run 同根因（AGENTWORK_HOME 泄漏），R3 前置轮 unset 前缀又引入 PATH 三连败；bench R3 复测进一步确认该泄漏还击穿 go test 子进程隔离（6 例失败，根因 A/A'/B）；收益：同类验证门/测试失败 4+3+6 → 0。
2. **实施 P-B：rsi_round 按 domain 递增**（中优）——两轮 round_no 均为 1、delta 恒空；收益：R4 报告【跨轮指标对比】首次有真序列。
3. **`/rsi` 端点口径修正**（低成本高一致性）——补文档为 `/rsi/sessions` 等真实端点，或 handler 加 `GET /rsi` 别名。
4. **deliver 失败自动重试/退避**（中优）——R2 deliver 因 SSH 私钥权限失败一次后依赖人工推进；外层加一次带延迟的自动重试。
5. **owner/leader 运行卡顿看护**（中优）——R2 owner run 运行 71 分钟无产出被人工停止；把 watchdog 覆盖 owner 运行。

---

*本报告由 `scripts/rsi-round-report.sh` 自动生成；如与平台数据不一致，以 daemon API 为准。生成时间：2026-11-11 09:36 UTC。*