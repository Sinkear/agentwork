# RSI R1 基线与瓶颈提案（round-1）

> 只读分析产物。数据源：本机 RSI 训练接口（session / workflow / rounds）+ 本 goal 评论流。
> 指标口径：本轮 session `e9f5c086…`（goal 98d60d12，workflow 冻结于 11:08:15Z）。

## 0. 基线快照（R1）

| 指标 | 本轮值 | 说明 |
|---|---|---|
| critical_path_ms | 293,501 | 关键路径 = sg-B 单节点（bench 基准任务） |
| wall_ms | 293,501 | 墙钟与关键路径相等 |
| work_sum_ms | 293,501 | 全部"工作量"都压在一条串行链上 |
| queue_wait_ms | 294,330 | ≈ 墙钟的 100.3%：分析节点被依赖整段阻塞 |
| parallel_speedup | 1.00 | 峰值并行宽度 1，单通道，无并行收益 |
| rework_ratio | 0 | 本轮无返工 |
| verify_overhead_ratio | 0（verify_rounds=0） | 校验开销在本轮图中不可见 |
| consult_count | 0 | 无咨询 |
| human_wait | 未采集 | 平台指标集中无该字段，属测量缺口 |
| sg-B 实际负载 | 2 个 ≤5 行文件 + `go test`（0.009s） | 真实计算 <5s，其余全是 agent 会话开销 |

历史参照（同 domain、同基准任务的前两次尝试）：两轮均在 sg-B 上 attempt 3 才停（2 子任务共 4 次执行），goal 级失败；结构发现同为 `retry_amplification`（score 40，"重试放大：平均每节点 2.0 次 run"）。本轮 attempt 1 即通过，说明失败非必然、可预防。

---

## 提案 1（预期收益最大）：解除分析与基准之间的假依赖——分析前置、提案后写

- **假设**：sg-O 的输入（RSI session/workflow/rounds 数据）在 sg-B 执行期间就已可读，真正依赖 sg-B 的只有"最终指标与结论"一小段。把 sg-O 拆成"数据收集与历史分析（可并行）+ 指标落定与提案定稿（依赖 sg-B）"，即可消除绝大部分排队等待。
- **证据**：本轮 `queue_wait_ms=294,330 ≈ wall_ms=293,501`（100.3%），`parallel_speedup=1.00`、`parallel_width_peak=1`、`lane_count=1`；实测佐证：sg-B 运行期间（11:03Z）RSI 端点已可完整拉取 session 列表与 workflow 图，仅终值指标缺失。finding 佐证：`serialization_loss_ms=0` 但整条链 100% 串行——串行不是损失在"合并"，而是损失在"起跑线"。
- **预期效果**：每轮墙钟从 ~294s 降至 ~max(sg-B 时长, 前置分析时长)+增量；sg-O 的等待段从死等 294s 变为并行消化，多轮迭代的累计节省最大（瓶颈在关键路径上的占比最高）。
- **复测计划**：R2 同基准复测；对比 `queue_wait_ms/wall_ms` 比值与 `parallel_speedup`，目标：queue_wait 占比 <50%、parallel_speedup >1.3。
- **风险**：若 sg-O 提案必须引用 sg-B 的最终验证结论，前置写稿可能返工一次；缓解：前置阶段只做数据拉取与历史轮分析，指标段落留槽位。
- **回滚**：恢复 sg-O depends_on sg-B 的单段派发即可，无代码改动。

## 提案 2：压缩单节点会话开销——微任务的 brief 预热与产物模板

- **假设**：293.5s 的 sg-B 里，真实计算 <5s（go test 0.009s），其余是会话启动、上下文装载、工具轮次与报告。基准任务规格完全确定（文件路径、内容、验证命令都给死），把"可直接落盘的产物模板 + 预检清单"写进 brief，可把执行收敛为"写文件→跑验证→报告"三轮以内。
- **证据**：`work_sum_ms == critical_path_ms == wall_ms = 293,501`，单节点独占全链；对比历史轮：一次 13ms 即失败（attempt 3）、一次 122.7s 失败（attempt 3）——时长波动 4 个数量级，说明时间花在会话与重试路径而非任务本身。
- **预期效果**：微任务节点时长从 ~5min 降到 ~2min（保守估 30%+），每轮直接缩短关键路径；对 RSI 全循环（bench→分析→实施→复测）复利。
- **复测计划**：R2 复测 sg-B `duration_ms`，目标 <210s；同时观察 `rework_ratio` 不上升。
- **风险**：brief 过度模板化可能让 agent 跳过必要的上下文判断（如 repo 结构变化）；模板需随基准任务演进而维护。
- **回滚**：从 brief 模板移除预热段即可。

## 提案 3：消除重试放大——把前置自检写进派发简报

- **假设**：历史两轮 sg-B 各 attempt 3（finding `retry_amplification`，evidence `runs=4 nodes=2`，score 40）源于可自愈失败模式（环境/依赖/锁竞争类），重试本身不修复原因；把前置检查（go 工具链可用、目标包路径存在、无并发写锁）加进 brief 或改为派发前校验，可将 attempt 3→1。
- **证据**：两轮 goal 级失败均为同一 finding 触发；本轮同任务 attempt 1 即 verified，证明失败非任务固有，而是环境/简报差异。
- **预期效果**：失败轮次的墙钟从 attempt×duration 收敛为 1×duration；goal 级失败（两轮皆因 sg-B 失败而失败）归零，RSI 循环不再浪费整轮。
- **复测计划**：R2/R3 观察各节点 attempt 分布与 `retry_amplification` 是否再触发；目标：平均 attempt ≤1.2。
- **风险**：清单覆盖不了新失败模式，可能给出虚假安全感；需保留失败原因回传（cancel_reason）以便迭代清单。
- **回滚**：从 brief 模板移除自检清单，恢复原派发文案。

---

## 测量缺口（非提案，供 R2 补采）

- `human_wait` 未在平台指标集中出现，人工审查段的等待无法量化；建议 R2 在 workflow 图中纳入 review/人工节点计时。
- `verify_overhead_ratio=0` 与本轮"verified with no changes"（交付物在评论流、无集成变更）一致，但校验环节的真实耗时未进图；建议校验节点计时入图后再评估。
