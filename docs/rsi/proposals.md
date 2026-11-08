# RSI 自迭代 R1 — 编排层瓶颈提案（round-1）

- 作者：optimizer（sg-O，round-1）
- 日期：2026-11-08
- 性质：只读分析，不改业务代码。以下提案均为只读建议，需人工审查批准后由 builder 实施。
- 数据源：`GET /rsi/sessions`（按 goal_id 过滤）+ `GET /rsi/sessions/{id}` + `GET /rsi/sessions/{id}/rounds` + workflow 图（含本 goal 实时 session）。

## 一、数据与基线

9 类结构 finding 中本域仅触发 1 类（retry_amplification，score 40）。三次同题观察（goal_title 均为「RSI 自迭代 R1：基准测量」）：

| 指标 | sess 31b8ffa4（10:55，sealed） | sess 5806b2f4（10:56，sealed） | sess e9f5c086（本 goal，11:02，live） |
|---|---|---|---|
| sg-B attempt | 3（failed） | 3（failed） | 1（verified） |
| sg-B 末次 duration | 13 ms | 122,709 ms | 293,501 ms |
| goal 最终状态 | failed | failed | 进行中（sg-B 已 verified） |
| critical_path_ms | 13 | 122,709 | 293,501 |
| wall_ms | 1,005 | 123,657 | 293,501 |
| queue_wait_ms | 143 | 113 | 294,330 |
| parallel_speedup | 1.00 | 1.00 | 1.00 |
| verify_overhead_ratio | 0 | 0 | 0 |
| rework_ratio / consult_count | 0 / 0 | 0 / 0 | 0 / 0 |
| findings | retry_amplification (runs=4 nodes=2) | retry_amplification (runs=4 nodes=2) | 未 sealed |

基线说明：三个 report 均 `baseline={}`（narrative 明言「<5 个就绪会话无法计算基线」）。**本轮 sg-B verified 是首个成功样本，基线自本轮确立**；上表两个历史 sealed session 作为参考点。工作流图（3 节点 / 3 边）三次完全同构：goal → sg-B → sg-O（显式 depends_on），depth=1、lane_count=1、critical_share=1 —— 结构串行是设计使然。

## 二、瓶颈定位

1. **重试放大（唯一触发的 finding）**：两个历史 session 中 sg-B 均 attempt=3 耗尽后失败，goal 状态 failed。runs=4 vs nodes=2。且失败形态不同：sess 31b8ffa4 的 sg-B duration 仅 **13ms** —— 未真正执行即失败（派发/环境类错误）；sess 5806b2f4 跑了 122.7s 后失败。本轮同简报 sg-B **一次通过**，证明失败是环境性的、可预检的，而非任务本身问题。
2. **关键路径 100% 由 sg-B 构成**：本轮 critical_path_ms=293,501 = wall_ms = work_sum_ms，critical_share=1，parallel_speedup=1.00。而 sg-B 的任务本体（两文件 + go build + go test）执行仅 **0.009s**（bench 评论中的 go test 输出）—— 耗时与任务本体比值 ~3×10⁴，其余全是 agent 启动、上下文构建与模型往返。
3. **依赖等待占满墙钟 + 空转 run**：本轮 queue_wait_ms=294,330（≈wall 的 100%），对应 sg-O created 11:02:42.12 → started 11:07:36.27 的依赖等待。另：sg-O 首轮 run（11:02:42 被 mention → 11:03:13 结束）纯为记录「等待基准完成」，约 31s + token 的空转开销；11:07:36 才被唤醒真正开工。
4. **未触发类别确认非瓶颈**：verify_overhead_ratio=0（sg-B 节点结束晚于其评论 ~8s，约占 2.7%）、rework_ratio=0、consult_count=0、serialization_loss=0、lane_imbalance=1（单通道）。校验/返工/咨询当前不构成开销。

## 三、提案（按预期收益排序，最多 3 条）

### P1 消除 sg-B 重试放大：失败原因入库 + 派发前预检

- **假设**：历史 session 中 sg-B 的 3 次重试失败是环境性失败（一次 13ms 即失败，疑似派发/环境错误而非真实执行失败），失败模式重复且重试不修复根因；若把前置检查加进 brief / 派发前校验，首跑成功率可大幅提升。
- **证据**：sess 31b8ffa4：sg-B attempt=3、duration=13ms、status=failed、goal failed；sess 5806b2f4：attempt=3、duration=122,709ms、goal failed；两者 finding 均为 retry_amplification（score 40，evidence `runs=4 nodes=2`）；本轮 sess e9f5c086 同简报 attempt=1 通过（sg-B verified）—— 排除任务本身问题。
- **预期效果**：sg-B 首跑成功率 1/3 → 目标 ≥2/3；每轮减少最多 2 次无效 run（历史 runs/nodes=2.0 → 目标 ≤1.2）；避免 goal 因重试耗尽而整体 failed（3 次观察中 2 次整体失败）。
- **复测计划**：R2 跑同基准，检查 sg-B 的 attempt 数与 retry_amplification finding 是否消失；连续 3 轮 runs/nodes ≤ 1.2 且 goal 不因 sg-B 失败而 failed。
- **风险**：预检逻辑误判（把可自愈错误当权限错误、或反之），可能阻断本可成功派发的任务；根因未入库前预检规则只能凭猜测。
- **回滚**：预检做成旁路记录（只记不阻断），关闭开关即恢复原派发路径。

### P2 压缩关键路径上 sg-B 的执行时长（293.5s → 目标 <120s）

- **假设**：sg-B 是两文件 + 两条命令的微任务，但耗时 293.5s（本轮成功 run）/122.7s（历史失败 run），其中大部分是 agent 启动、上下文构建与模型往返；简报越聚焦、步骤越少，非执行开销越小。
- **证据**：本轮 workflow metrics：critical_path_ms=293,501 = wall_ms = work_sum_ms、critical_share=1、parallel_speedup=1.00 —— 关键路径完全由该节点构成，压缩它即压缩整轮墙钟；任务本体 go test 仅 0.009s（bench 评论），执行/开销比值 ~3×10⁴。
- **预期效果**：sg-B 简报加入「最小步骤清单」（直接创建两文件 → 跑两条验证命令 → 评论输出尾部，不探索不读无关文件），目标关键路径压到 <120s（历史失败 run 的 122.7s 为参考下界），整轮墙钟近等比下降。
- **复测计划**：R2 同基准对比 critical_path_ms 与 wall_ms；基线确立后连续 3 轮跟踪趋势（rounds API）。
- **风险**：过度压缩简报导致 bench 省略验证步骤或漏报验证输出，损害机器校验通过率；不同 agent 执行波动大，<120s 目标可能受模型侧延迟支配而不可控。
- **回滚**：简报模板还原为 sg-O 的原文描述即可，无代码改动。

### P3 失败遥测补齐：RSI API 暴露 sub_goal 失败原因（cancel_reason）

- **假设**：finding 的 suggestion 明确要求「查看失败 sub_goal 的 cancel_reason/错误」，但当前 `/rsi/sessions/{id}` 与 workflow 节点均不暴露该字段 —— 根因只能靠 duration（13ms vs 122.7s）间接推断；失败原因入库后，P1 的预检才有数据源，9 类 finding 的诊断链路才闭合。
- **证据**：sess 31b8ffa4 与 5806b2f4 的 report/rounds/finding 全文均无失败原因字段；finding.suggestion 原文要求查 cancel_reason（两次独立触发，score 40）；本提案分析中只能以「13ms 即失败」做形态推断，无法给出确定根因。
- **预期效果**：(a) R2+ 的 retry_amplification finding 可附确定根因，P1 预检规则有据可依；(b) 每轮免去一次「等待型空转 run」可顺带评估 —— 本轮 sg-O 首轮 run 花 31s 纯记录等待，若依赖唤醒时附带「依赖未就绪」标记，可省该空转与相应 token。
- **复测计划**：R2 检查 sessions API 是否新增 cancel_reason（或等价）字段并含失败样本；检查 sg-O 是否仍有等待型 run（comment feed 时间线：mention 时刻 vs 首个实质工作 run）。
- **风险**：改动点在平台侧（RSI 采集器/调度器），超出仓库代码范围，需 builder 与平台确认实施面；字段增加内容存储与隐私面（cancel_reason 可能含环境细节，需 redacted 处理，与现有 content_capture=redacted 一致）。
- **回滚**：字段做成可选输出，老消费方不读该字段即无影响。

## 四、复测与迭代

- R2 由 bench 复测同基准（同两文件 + 同两条命令），optimizer 依赖 R2 session 产出对比报告：本轮基线（critical_path 293,501ms、wall 293,501ms、attempt=1、runs/nodes=1.0）vs R2。
- 判定标准：P1 看 attempt 与 retry_amplification；P2 看 critical_path_ms 与 wall_ms；P3 看 cancel_reason 字段有无与等待型 run 消失与否。
- 本文档随每轮 RSI 迭代追加 round 章节，旧提案标记 状态（open/approved/rejected/verified）。
