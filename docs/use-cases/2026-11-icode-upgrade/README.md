# 真实用例：icode 升级（AgentWork 小队全流程）

本目录沉淀一个**真实生产用例**的原始执行报告与事后复盘，用于校准 AgentWork 的平台行为、性能与协作协议。

| 项 | 内容 |
|---|---|
| 用例 | 升级 icode 0.28.0 → 0.35.2，并完成验证、打包、按规范重打包 |
| 发生时间 | 2026-11-13 15:52 – 2026-11-14 10:30（北京时间） |
| Goal ID | `f68bd0a5e173da18437daf680a376409` |
| 小队 | 自动升级作业环境小队（leader: AI SHELL） |
| 参与 Agent | AI SHELL / Checker / Upgrader / Verifier / Packager |
| 规模 | 23 次 run，合计约 18 966s ≈ 5.27h 计算；墙钟约 41h50m |
| 结论 | 任务本体完成、功能无回归；遗留版本账面错位与产物未发布 |

## 文件

- [`execution-report.md`](execution-report.md) — 原始执行报告（只读副本，sha256 `df296984…`，内容未改动）。
- [`timeline.md`](timeline.md) — 由报告时间线重算的 run 级耗时账本与浪费分类。
- `findings-dispatch.md` — 派发/触发链路的问题与修复方案（分析产出）。
- `findings-runtime.md` — 单次 run 运行时长、重试、续跑的问题与修复方案（分析产出）。
- `findings-watchdog.md` — 看门狗误报、调度闸门、交付件持久化的问题与修复方案（分析产出）。
- [`analysis.md`](analysis.md) — 汇总复盘：哪些是真问题、优先级、改动计划。

## 使用方式

1. 先读 `execution-report.md` 建立事实基线（报告由外部架构伙伴撰写，时间与耗时来自 AgentWork API）。
2. 用 `timeline.md` 核对算力去向，避免把"真实工作"误判成平台开销。
3. 读 `analysis.md` 的结论与路线图；细节证据在三个 `findings-*.md` 中。
4. 修复后用同一用例的 run 账本做回归对照（目标：总计算 ≤ 3h、平台元 run 占比 < 5%、同一执行者零并发 run）。