# 用例耗时账本（run 级重算）

> 来源：`execution-report.md` §3 时间线。所有耗时取报告中的 run 持续秒数。报告未列 run#8；按 §3 的空档（16:20–16:24）推测其为一次约 234s 的 run，未计入下表（计入则总量 +1.2%，不改变结论）。
> 分类口径：`work` = 直接产出交付物/判断；`dup` = 并发重复 run 或可被吸收的重复劳动；`meta` = 平台自动触发的 verify/consult/reopen 元 run；`fail` = 启动失败。

## 总账

| 分类 | 秒 | 占比 |
|---|---:|---:|
| work（真实工作） | 14 098 | 74.3% |
| dup（重复/并发浪费） | 2 540 | 13.4% |
| meta（平台元 run） | 2 317 | 12.2% |
| fail（端点失败） | 11 | 0.1% |
| **合计** | **18 966** | **5.27h** |

> 口径 B（按 run 时间窗口去重叠，用于回答"到底多少算力是白花的"）：总 19 200s；真实工作 9 536s（49.7%）；重复/并发 7 007–7 102s（36.5–37.0%，含 run#12 的 4 481–4 562s）；平台 meta 2 317s + run#8 残值 234s（12.1–13.3%）。口径 A 把 run#12 当工作，口径 B 把它当"与 run#9 重叠的重复"。两口径的差全在 run#12 的归属上，见文末 `*` 注。

- Upgrader 两条 run（6236s + 4562s）合计 10 798s，占 **56.9%**——单点最长，但其中含"恢复调度续跑"的上下文重放成本，报告无法拆出纯重放部分。
- 平台元 run 2 317s ≈ **38.6 分钟**：verify 1591s（3 次）+ consult 465s（3 次）+ Reopened/警告 261s。
- 重复 run 2 540s ≈ **42.3 分钟**：Checker 71s + Upgrader 1674s + Packager A 795s。
- 另外：3 次启动失败本身只烧 11s，但造成 **约 25 分钟墙钟空档**（模型端点 404，无快速失败/告警）。

## run 明细

| # | 主体 | 事件 | 秒 | 分类 |
|---|---|---|---:|---|
| 1 | AI SHELL | 启动失败（端点 404） | 5 | fail |
| 2 | AI SHELL | 重试失败 | 2 | fail |
| 3 | AI SHELL | 重试失败 | 4 | fail |
| 4 | AI SHELL | 解析意图、派发 Checker | 71 | work |
| 5 | Checker | 扫描 release 源 | 71 | work |
| 6 | Checker | 并发重复 run | 71 | dup |
| 7 | AI SHELL | 平台 verify Checker | 299 | meta |
| 9 | Upgrader | 主升级 run | 6 236 | work |
| 10 | Upgrader | 并发重复 run（后仅建 venv） | 1 674 | dup |
| 11 | AI SHELL | 平台 consult（响应并发 run） | 76 | meta |
| 12 | Upgrader | 恢复调度续跑（clean→configure→deploy→accept） | 4 562 | work* |
| 13 | AI SHELL | 平台 verify Upgrader | 673 | meta |
| 14 | AI SHELL | 平台 consult | 202 | meta |
| 15 | Verifier | 34 条 base 用例 | 135 | work |
| 16 | AI SHELL | 平台 consult | 187 | meta |
| 17 | Packager | 打包 run A（前置闸门核验+打包） | 795 | dup |
| 18 | Packager | 打包 run B（主） | 1 369 | work |
| 19 | AI SHELL | 平台 verify Packager | 619 | meta |
| 20 | AI SHELL | 平台 Reopened + Collaboration Warning | 261 | meta |
| 21 | AI SHELL | 规范分析 | 305 | work |
| 22 | AI SHELL | 重打包 1.0.4 | 1 246 | work |
| 23 | AI SHELL | python 必要性分析 | 103 | work |

\* run#12 与 run#9 在报告时间线上重叠（16:53–18:09 vs 16:24–18:08）。若二者确为同一 goal 的并发 owner run，则不仅是重复成本，还说明 per-goal single-flight 在某条路径上失效——需用平台库表核对 `started_at`/`queued_at` 后再定性。

## 结论

1. **"慢"首先是一个算术问题**：平台自造的元 run + 重复 run 合计 4 857s ≈ **81 分钟，占 25.6%**，且尚未计入它们造成的串行等待与协调评论。
2. Upgrader 的 10 798s 是真实重活（183 包 venv、clone、部署、验收），但不该被 2h 预算切成两条 run 并重放上下文。
3. 25 分钟端点空档是**可用性**问题，不是算力问题；修复收益在墙钟而非 token。