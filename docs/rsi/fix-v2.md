# agentwork-int-v2 修复落地记录

- 日期：2026-11-14
- 基线：`origin/agentwork-int-v2` @ `ae12054`
- 分支：`agentwork-int-v2-fix`
- 审查记录：`docs/rsi/review-v2.md`（在 `agentwork-bench` / `agentwork-v2-review` 工作树，记录 17 个问题场景与采纳结论）

本文只记录“修后可拿”范围的**实际落地**：每个改动对应哪个真实场景、改了什么、用什么证据判定生效，以及仍未解决的问题。不复述审查过程。

## 1. 提交清单

| # | commit | 内容 |
|---|---|---|
| 1 | `2a4babe` | RSI 图/指标/规则：CP 扇入、seq 排除、review 隔离、speedup/串行损失/等待/human_wait/retry/轮次口径 + 回归测试 |
| 2 | `2aaa78b` | P2 CDN：缺码默认 FAIL、到期 allowlist、`AGENTWORK_SKIP_CDN_DRIFT=1` 才 skip |
| 3 | `5dfad20` | gitignore：project-skills 等所有 exclude 先 `check-ignore` |
| 4 | `90efd88` | link：生产调用点 handler-first + `StartReadLoop` 幂等；16 个 daemon 测试恢复 644 |
| 5 | `9ab2b65` | 轮次报告脚本重写为数据驱动、fail-loud |
| 6 | `571d391` | Training UI：状态化报告可见性、skipped/重跑/零值/错误/缩放、文案校准 |
| 7 | `a84b611` | depends_on 门控 liveness、dispatch 锚点、机器验证行、retry 门控 |
| 8 | `65c342f` | docs/RSI.md + playbook 指标与轮次约定校准 |
| 9 | `761271c` | Retry 清空上一轮 headline 指标，避免旧值残留 |
| 10 | `b007146` | OCR 复核修复：`created_at` 损坏时 queue split 归零 + 回归测试；detail 指标仅 ready 展示；报告脚本 ID 校验 / score 排序健壮性；schema 轮次注释改为 per-domain |

## 2. 场景 → 修复 → 是否生效

| 场景（审查编号） | 修复 | 验证 | 结论 |
|---|---|---|---|
| #9 CP 扇入重复计数（A∥B→C 30/30/10 被算成 70min） | `cand = p.length + n.DurationMS`；扇入 golden test | 单测 CP=40min、speedup=1.75；旧实现 70min/1.0 | 生效 |
| #9 推断 seq 边进入 CP，独立串行被当成依赖链 | CP DP 只吃非 `inferred` 边；seq 仅展示/诊断 | 两个独立 30min 串行：cp=30、wall=60、speedup=1.0、serLoss=30min | 生效 |
| #9 review 节点无边却进 work_sum/speedup，甚至自成 CP | review 单列 `review_busy_ms`/`review_overhead_ratio`；不进 work/wall/lane/width；DP 跳过 | 30min 执行 + 120min review：work=30、wall=30、cp=30、speedup=1.0、review_busy=120 | 生效 |
| #9 `parallel_speedup` 口径混淆 | 改为 `work_sum_ms / wall_ms`（实际吞吐；串行=1.0） | 扇入用例 1.75；串行用例 1.0 | 生效（口径变更，旧 work/cp 废弃） |
| #9 lost_parallelism 恒假（`1 < 0.7`） | 保留 `gain=work/cp` 与 `actual=work/wall`；新口径下规则可触发 | 新增规则级测试：两段 30min 串行必须产出 lost_parallelism | 生效 |
| #9 retry L2 永不触发 | 指标 `retry_amplification=max Attempt`；baseline 含该 key 且加 domain 过滤 | DB 测试与 baseline key 检查；L2 逻辑有真实比较对象 | 生效（L2 仍需同 domain ≥5 样本） |
| #9 queue_wait 把依赖等待算成派发延迟 | 拆 `dependency_wait_ms` + `dispatch_wait_ms`；`ready=max(created_at, pred end)`；`queue_wait_ms` 保留兼容别名 | 20min 依赖 + 60min 派发用例：20/60/80min | 生效 |
| #9 human_wait 未进 metrics，finding 分母错（600%） | `human_wait_ms`/`human_wait_share=human/(wall+human)` 进 metrics；规则用 share | gate 3600s + wall 90min → share=0.4；finding 不再 >100% | 生效 |
| #9 CP tie 由 map 迭代决定（361/39） | 按 start 顺序 + key 字典序稳定 tie-break | 500 次重算断言 CP 稳定 | 生效 |
| #9 轮次按 session 编号，跨轮 delta 永远 `{}`（P-B） | round_no 按 domain 连续编号；delta 对比同 domain 上一条 rsi_round；trigger 按本 session 是否已有轮次 | 三个 goal/同 domain：round=[1 2 3]，第二轮 delta 非空 | 生效（设计修正，非“加锁”） |
| #8 depends_on 依赖失败后 child 永久 running（goal 无法终结） | failed/cancelled 终态同事务级联失败仍 gated 的 dependents（不动点支持传递链），写事件+系统评论；dep 仅暂时失败不级联 | 新增 7 例：失败/取消/多依赖/retry 拒绝/依赖恢复/机器验证/锚点；`Depends|SubGoal|Verif` 21 PASS | 生效（service 层） |
| #8 唤醒 run 丢 dispatch 锚点 | `sub_goal.dispatch_comment_id`；wake 释放时传给 `enqueueSubGoalRunTx`；旧行空串回退 | 测试断言释放 run 的 `trigger_comment_id==dispatch id` 且报告评论挂到 dispatch | 生效 |
| #8 机器验证不写 verification_result，workflow 无 `v:` 节点 | 机器验证在 verified 事务内写 passed 行 | 测试断言 workflow 出现 `v:` 节点 | 生效 |
| #8 failed gated child 可 Retry 绕过门控 | RetrySubGoal 在依赖未全 verified 时返回带未满足依赖列表的 ValidationError | 测试：未 verified 时 retry 被拒；依赖恢复后可跑 | 生效 |
| #2 P2 守卫全 SKIP，真漂移被静默吞掉 | 缺码默认 FAIL；allowlist AW.10000025–30 到期 2026-12-31；仅 env=1 skip；网络/非200/畸形 JSON 仍 skip | 11 例表驱动；live CDN 两 locale 按 allowlist SKIP；未知码/过期 FAIL | 生效 |
| #13 gitignore 只修了 AGENTS.md，project-skills 同类 bug 残留 | 所有 exclude 先 `check-ignore`，被忽略就不加 pathspec | `.claude/` 仓库复现原 exit 1 并转绿；4 例测试 | 生效 |
| #12 link 只修测试 helper，生产仍 NewPeer→Handle 竞态 | `NewPeerUnserved` + `StartReadLoop`（sync.Once）；4 个生产点 handlers-first；`NewPeer` 兼容保留 | `go test ./internal/link -race -count=2` PASS；新增双启动/handler-first 回归 | 生效（单测/竞态层；未跑真实 daemon 集成） |
| #11 UI：skipped 可点重跑、重跑期间显示旧报告、零值/错误静默 | 按 status 控制报告可见性与 Retry；非 ready 显示“—/尚无数据”；错误提示+重试；图加缩放；文案与实现一致 | `tsc --noEmit` exit 0；`next build` 19/19 页面成功 | 生效（无 live daemon，契约/构建验证） |
| #15 报告脚本硬编码 R3 结论、参数崩、死 API 仍 exit 0 | 数据驱动五节；N/--since/--until 接受 3/R3；必需 API 失败 exit 1 且不写文件；无硬编码结论 | fixture 五节齐全 exit 0；死 API exit 1 且无输出；参数变体逐字节一致 | 生效（fixture 自测，未接生产 API） |
| #6/#15 文档与代码不一致 | workflow_edge 标“预留表”；`v:`/`chg:` 键；seq 不进 CP；指标段/轮次约定/strict 行为/T6 校准 | docs/RSI.md + playbook diff 审阅 | 生效 |
| #11 详情 top_category/top_title 永远空 | GetSession 填 top 字段 | DB 测试断言 TopCategory/TopTitle | 生效 |
| 重跑/失败时列表残留上一轮指标 | Retry 清空 node_count/critical_path/parallel_speedup/finding_count | 单测通过；UI 同时只信 ready 数值 | 生效 |

## 3. 验证记录（本机 2026-11-14）

- `go test ./... -count=1`：全绿；service 3.366s，daemon 2.419s，cli 0.589s。
- `go test ./internal/service -run 'Depends|SubGoal|Verif' -count=1 -v`：21 PASS。
- `go test ./internal/link -race -count=2`：ok 1.064s。
- `go vet ./...`：无输出。
- `web: npx tsc --noEmit`：exit 0；`npm run build`：19/19 页面，exit 0。
- 报告脚本：fixture `R3 --since R1 --until R3 --dry-run` → exit 0、五节；死 API → exit 1、无输出文件；`bash -n` 通过。
- OCR 复核：`ocr delegate preview --from ae12054 --to HEAD` 20 个可审文件（tests/docs 默认排除）；复核后 `b007146` 新增 queue 解析防护回归用例，`go test ./...` 仍全绿，web 构建仍 19/19。

### 指标口径前后对比（可复算）

| 场景 | 旧结果 | 新结果 |
|---|---|---|
| A∥B(30+30)→C(10) | CP=70min，loss=40min，speedup=1.00 | CP=40min，loss=0，speedup=1.75 |
| 两段独立 30min 串行 | CP=60min（seq 成链），speedup=1.00 | CP=30min，wall=60min，loss=30min，speedup=1.00（规则可触发） |
| 30min 执行 + 120min review | review 自成 CP=120min，speedup=2.64 | work=30，wall=30，cp=30，speedup=1.0，review_busy=120 |
| 依赖 20min + 派发 60min | queue_wait=80min（不可区分） | dependency=20min，dispatch=60min |
| gate 3600s + wall 90min | finding 占比 600% | share=0.4 |

## 4. 未修/残留风险（不要误以为已解决）

1. **P-A（AGENTWORK_HOME 泄漏）未实现**：`internal/daemon/verify.go` 的 `sh -c` 仍继承 daemon 环境；审查确认场景真实但 R2 fetch-128 根因未复现。若要修，需同时覆盖 `runVerifiedCmd`/`runVerifyOutput`，把 `AGENTWORK_HOME`+`HOME` 指向 per-run 临时目录，并保留 PATH/GOCACHE/代理。
2. **link 生产顺序仅单测/-race 验证**，未跑真实 daemon↔server 集成；`-race` 对 ordering 竞态天然不敏感，剩余窗口靠代码审查。
3. **循环 depends_on 未拒绝**：正常 CreateSubGoal 只能依赖已存在节点，构造不出环；历史坏数据仍可能互相等待。
4. **retry/rework 的 run 仍不带 dispatch 锚点**（既有约定“仅首轮锚定”）；RetrySubGoal 恢复后的首个 run 也没有锚点。若要求全链审计，需要把 dispatch id 继续传给 retry enqueue。
5. **文件 mode**：本次只恢复 16 个 daemon 测试；仓库仍有约 286 个 100755（含源码/文档），来自历史提交，未做全仓清理（避免 270+ 条噪声 diff）。
6. **CLI/MCP 创建 goal 仍不支持 execution_mode/depends_on 等字段**：训练轮次只能 web 表单或 curl JSON（P2.19 未做）。
7. **workflow_edge 表仍存在**但 Go 无读写，文档已标“预留”；未删表（避免破坏既有 DB）。
8. **报告脚本只经 fixture 自测**，未在生产 daemon/真实 R4 数据上跑过；五节内容按数据派生，但“候选项”仍需人工判断。
9. **无 live daemon/Training 页面截图验证**；UI 只做了类型、构建、契约验证。

## 5. 合并与使用

- 分支基于 `ae12054`，可直接以 `agentwork-int-v2-fix` 合入 `agentwork-int-v2` 或提 PR 到 `main`（共 13 个提交，含 3 个文档提交，无外部依赖变更）。
- 若只取部分：指标/规则必须与 `docs/RSI.md` 同批合入，否则文档口径超前；gating 与 workflow 指标互不依赖，可分开。
- 首次启动会跑 store 迁移：`sub_goal.dispatch_comment_id`、`rsi_round` 等；迁移是增量/幂等的，`workflow_edge` 保留。
- 复盘入口：本文件 + `docs/rsi/review-v2.md`（问题场景与审查证据）+ `docs/RSI.md`（最终指标口径）。
