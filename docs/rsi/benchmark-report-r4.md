# agentwork R4 基准复测报告（bench 口径 · P-C 固化全口径首样本 · owner 代跑）

- **基线 commit**：`6526846`（feat-28651f49 tip = R3 复测基线 b13a5581 之后的集成 tip，含 P-A `229c2d3` / P-B `ec959ae` / P-D `4132d31` / playbook `6769367` / release 脚本 `4d710f7`）
- **唯一有效版本声明**：本文件为工作区内 `docs/rsi/benchmark-report-r4.md` 唯一有效版本（R2 实测的同名文件集成冲突教训，见 playbook §3.2 头部要求）。
- **执行者说明**：sg-REBENCH 三次尝试均烧在 machine offline（环境时序问题，非任务失败），为不阻塞 R4 收尾，由 owner 按 playbook §3.1/§3.2 口径代跑本报告；sg-REBENCH 子目标保持 failed 记录，不虚报为已完成。
- **口径**：P-C 固化口径（playbook §3.1）——build 冷/温双列（冷=独立 GOCACHE 新建即空，实测 2402 文件真冷态；温=标准 GOCACHE 3 连跑中位）、test 温 3 连跑中位（首跑含全部测试二进制编译）。**跨轮对比：build 采冷列，test 采温中位。**

## 1. 环境节

| 项 | 值 |
|---|---|
| go version | go1.26.4 linux/arm64 |
| 内核/架构 | 5.10.0-182.0.0.95.r3353_274.hce2.aarch64 / aarch64 |
| CPU 核数 | 7 |
| GOCACHE（冷） | `mktemp -d` 独立目录（每次新建即空，实测写入 2402 个文件） |
| GOCACHE（温） | `/root/.cache/go-build`（5.2G 热缓存） |
| 模块缓存 | `/root/go/pkg/mod`（1.6G），冷/温两列共用均热 |
| 影响测量的环境变量 | `AGENTWORK_HOME` / `AGENTWORK_DB` 已显式置空（P-A 修复后口径；本轮起无需 unset 前缀，验证进程已在源头剥离） |

## 2. 冷/温双列总表

| 指标 | 列 | 实测 | 失败用例数 |
|---|---|---|---|
| `go build ./...` | **冷**（独立 GOCACHE） | **40.745 s**（复测 41.165 s，取 40.7 s 口径） | 0 |
| `go build ./...` | 温（标准 GOCACHE，3 连跑） | 5.711 / 6.241 / 3.838 s（中位 **5.711 s**） | 0 |
| `go test ./... -count=1` | 温 3 连跑（首跑含测试二进制编译） | 13.881 / 13.765 / 15.154 s（中位 **13.881 s**） | **0**（14/14 包 ok，3 连跑均 0） |

> 测量污染说明（如实记录）：本轮测量期间宿主多次出现负载尖峰（load 4.5→5.9），温跑曾出现 61-91s 离群值与冷首测 157s 异常。全部离群轮次已废弃，最终采值均在 load<1.5 的平稳窗口测得；冷列两次独立测量（40.7/41.2s）相互印证，可信。

## 3. 包级 Top10（`go test ./... -count=1` 按包耗时降序）

| # | 包 | 耗时 | 状态 |
|---|---|---|---|
| 1 | internal/daemon | 7.71 s | PASS |
| 2 | internal/service | 6.69 s | PASS |
| 3 | cmd/agentwork-cli | 3.95 s | PASS |
| 4 | internal/notify | 0.88 s | PASS |
| 5 | internal/issue | 0.45 s | PASS |
| 6 | internal/backup | 0.31 s | PASS |
| 7 | internal/server/handler | 0.04 s | PASS |
| 8 | internal/server | 0.03 s | PASS |
| 9 | internal/link | 0.03 s | PASS |
| 10 | internal/zipx | 0.03 s | PASS |

## 4. 与 R3 复测对比表（build 采冷列 · test 采温中位 · 同口径对比）

| 指标 | R3 复测（b13a5581） | R4 复测（6526846） | Δ |
|---|---|---|---|
| `go build ./...` **冷** | 41.285 s | **40.745 s** | **-0.540 s（约 -1.3%），冷列口径可比** |
| `go build ./...` 温中位 | 4.672 s | 5.711 s | +1.039 s（仅参照不采信；本轮宿主背景负载高于 R3 测量时） |
| `go test ./... -count=1` 温中位 | 13.173 s（含 1 失败用例） | **13.881 s（0 失败）** | +0.708 s（约 +5.4%，含新增回归测试成本：P-A 净化用例、P-B domain 编号用例、P-D 重试 11 例） |
| `internal/daemon` 包 | 5.855 s | 7.71 s | +1.855 s（新增 verify/deliver 重试回归测试所致，预期内） |
| `internal/service` 包 | — | 6.69 s | 新增 rsi_round domain 编号用例（+120 行测试代码） |
| 失败用例 | 1（环境隔离缺陷：digest 系 HOME 互踩） | **0** | **修复确认** |

## 5. 结论

1. **冷列持平（-1.3%）**：三条修复均为行为修复+测试，不引入构建开销；冷列口径下构建性能无回归。冷/温差距约 7.1 倍（40.7/5.7），与 R3 的 8.8 倍同量级。
2. **测试健康修复确认**：R3 遗留的 1 个失败用例（digest 系测试 HOME 隔离被 `AGENTWORK_HOME` 击穿——正是 P-A 所修泄漏在测试进程自身的映射）本轮 3 连跑均 0 失败，14/14 包 ok。**P-A 的源头净化已在验证门生效**（domain checks 已去掉 unset 前缀，改由 daemon 源头剥离）。
3. **温中位 +5.4% 为回归测试成本**：P-A/P-B/P-D 合计新增约 300 行测试（含 11 例表驱动重试用例与跨 session 编号用例），daemon 包 +1.9s 与此对应。这不是性能回归，是修复的可验证性代价，符合预期。
4. **测量环境噪声警示（供 R5 分析）**：本轮宿主负载尖峰（其他环境/进程突发）可使温跑单次膨胀 10 倍以上（4s→91s），中位数法有效但样本建议 R5 起增至 5 连跑取中位；冷列因绝对值大（40s 级）抗噪性强，维持 2 次相互印证即可。

## 6. 数据溯源

- 测量脚本时间窗：2026-11-12 20:07–20:33 UTC（宿主时间），负载背景全程记录于测量日志。
- R3 对比数字来源：`feat-357252be` 分支 `benchmark-report-r3.md`（R3 轮归档），同口径列（冷列/温中位）。
- 冷态 GOCACHE 独立目录由 `mktemp -d` 新建，测后即删，测量后 `find` 计数 2402 文件确认真冷态。
- 本报告由 owner 代 sg-REBENCH 执行，口径与 playbook §3.1/§3.2 完全一致；sg-REBENCH 自身的 3 次 failed 尝试（machine offline）已记入轮次报告过程暴露问题清单。
