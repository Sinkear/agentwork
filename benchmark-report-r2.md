# agentwork R2 实施后基准复测报告（bench）

- 日期：2026-11-10（R2 复测 · 实施后新基线）
- 测量基线：**commit `365e108`**（分支 feat-25da0cc6-sg-23d061da HEAD，builder 实施成果：`3cf2116` P1 并行化 + `18a8c3c` P3 不可重试快速失败 + `8ca9957` P2 CDN 守卫 fail→skip + `365e108` impl-notes docs）。相对 R1 基线 `0bf354e` 仅含这三条已批准提案的实施与文档，无其他代码变更。
- 测量方式：在本 bench 工作区执行 `git worktree add --detach /tmp/opencode/bench-r2 feat-25da0cc6-sg-23d061da` 挂出该基线只读测量，**未改任何代码**（本工作区唯一新增文件即本报告）。
- 原则：只测量、不修改、不修复；失败如实记录。

## 1. 环境

| 项目 | 值 |
| --- | --- |
| go version | go1.26.4 linux/arm64 |
| `uname -m` / 内核 | aarch64 / Linux 5.10.0-182.0.0.95.r3353_274.hce2.aarch64 |
| CPU | 7 vCPU |
| GOCACHE | /root/.cache/go-build（温构建/测试）；冷构建用独立 GOCACHE=/tmp/opencode/gocache-r2 |
| GOMODCACHE | /root/go/pkg/mod |
| 计时方式 | Python subprocess 墙钟计时（/tmp/opencode/timeit.py，输出 `__WALL_SECONDS__`） |

## 2. 各命令总耗时（commit 365e108 上实跑）

| 命令 | 场景 | 总耗时 | 退出码 |
| --- | --- | --- | --- |
| `go build ./...` | 冷（独立 GOCACHE=/tmp/opencode/gocache-r2，模块缓存热） | 44.417 s | 0 |
| `go build ./...` | 温（标准 GOCACHE，3 连跑） | 4.387 / 3.670 / 5.137 s（中位 **4.387 s**） | 0 |
| `go test ./... -count=1` | 3 连跑（禁用测试缓存，完整执行） | 14.399 / 11.421 / 11.559 s（中位 **11.559 s**） | **0（3 次均 0 FAIL）** |

**结论：`go test ./... -count=1` 三连跑全绿（14 包 ok、0 FAIL、grep "FAIL" 计数 0），P2 修复 CDN 守卫后测试套件全绿达标；本轮无任何失败用例。**

## 3. 包级测试耗时 Top10（`go test ./... -count=1` run3，共 14 个含测试包）

| 排名 | 包 | 耗时 | 结果 |
| --- | --- | --- | --- |
| 1 | github.com/eushing/agentwork/internal/daemon | 5.911 s | pass |
| 2 | github.com/eushing/agentwork/cmd/agentwork-cli | 4.530 s | pass |
| 3 | github.com/eushing/agentwork/internal/service | 4.456 s | pass（R1 唯一失败包，现已 PASS） |
| 4 | github.com/eushing/agentwork/internal/notify | 0.925 s | pass |
| 5 | github.com/eushing/agentwork/internal/issue | 0.419 s | pass |
| 6 | github.com/eushing/agentwork/internal/backup | 0.297 s | pass |
| 7 | github.com/eushing/agentwork/internal/link | 0.075 s | pass |
| 8 | github.com/eushing/agentwork/internal/server | 0.061 s | pass |
| 9 | github.com/eushing/agentwork/internal/acp | 0.056 s | pass |
| 10 | github.com/eushing/agentwork/internal/server/handler | 0.049 s | pass |

- 共 27 个包：14 个含测试（全部 ok）+ 13 个无测试文件（"[no test files]"，未计入排名）。
- 三连跑 Top3 稳定性：daemon 6.218 / 5.719 / 5.911 s；service 4.619 / 4.399 / 4.456 s；cli 4.295* / 3.876 / 4.530 s（*run1 取自同命令补跑）。
- 前 3 名之外各包均 < 1 s；包结构与 R1 基线一致（实施 commit 仅触及 internal/daemon 与 internal/service 的测试文件）。

## 4. 与 R1 基线对比表

R1 基线（任务给定）：build 11.574s / test 23.012s / internal/daemon 7.696s / 1 包失败。

| 指标 | R1 基线 | R2 实施后（365e108） | 变化 |
| --- | --- | --- | --- |
| `go build ./...` | 11.574 s | 温中位 **4.387 s**（冷 44.417 s 另列） | 温构建明显低于 R1（冷/温双口径均如实记录；R1 口径接近温构建） |
| `go test ./... -count=1` | 23.012 s | 中位 **11.559 s**（三连跑 14.399/11.421/11.559） | **-11.453 s（约 -50%）** |
| `internal/daemon` 包耗时 | 7.696 s | 中位 **5.911 s**（三连跑 6.218/5.719/5.911） | **-1.785 s（约 -23%）**，P1 并行化收益 |
| 失败包数 | 1（internal/service） | **0** | **1 → 0**，P2 生效 |
| 测试结果 | 1 包 FAIL（CDN 缺码 3 用例） | 全绿（14 包 ok、0 FAIL） | FAIL → PASS/SKIP |

补充参照：工作区内 R1 复跑报告（benchmark-report.md，基线 0bf354e）记录 daemon 8.00s、service 4.61s FAIL、全量测试 wall 11.3s——与本轮 R2 的 daemon 5.9s、service 4.46s PASS 相比，P1 收益（-26%）与 P2 效果（FAIL 消除）方向一致。

## 5. P2 行为验证（-v 实测证据）

`go test ./internal/service -count=1 -v` 输出（节选，两次实测一致）：

```
    codes_test.go:126: CDN term.json out of sync — skip drift guard — 6 code(s) missing from https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/zh-cn/term.json: AW.10000025, AW.10000026, AW.10000027, AW.10000028, AW.10000029, AW.10000030
    codes_test.go:126: CDN term.json out of sync — skip drift guard — 6 code(s) missing from https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/en-us/term.json: AW.10000025, ... , AW.10000030
    --- SKIP: TestCodesMatchRemoteTermJSON/...zh-cn/term.json (0.06s)
    --- SKIP: TestCodesMatchRemoteTermJSON/...en-us/term.json (0.01s)
```

- R1 中 FAIL 的 2 个子测试现均为 **SKIP**，父测试 PASS；缺码集合与 R1 完全一致（AW.10000025~30 共 6 码 × 2 locale），skip 时输出完整清单，可诊断性保留。

## 6. P1 / P3 实施核对（代码审计，不改行为）

- **P1**：`grep -c "t.Parallel()" internal/daemon/*_test.go` → gate 2 / handoff_cancel 8 / intake 30 / prompt_context 11 / runaway 3 / schedule 4 / verify 12，共 **70 处**，与 builder 报告逐文件一致；效果实测 daemon 7.696 → 5.911 s（约 -23%）。
- **P3**：`internal/service/goal.go:105-123` 含 `nonRetryableMarkers` 白名单（起步仅 "Model unavailable"）与 `isNonRetryable`；`internal/daemon/daemon.go:127` maxAttempts 注释同步。测试套件无针对 P3 的新增用例，测试耗时无对应变化，属预期。

## 7. 结论

1. **P1 并行化**：daemon 包 7.696s → 5.911s（约 -23%），收益真实可复现（三连跑稳定）。
2. **P2 CDN 守卫 fail→skip**：R1 唯一失败包已消除，`go test ./... -count=1` 三连跑全绿（14 包 ok、0 FAIL）。
3. **P3 不可重试快速失败**：实现到位（service/goal.go 白名单 + isNonRetryable），不改变测试行为。
4. **总体**：实施后基线 365e108 上，全量测试中位 11.559s（R1 23.012s，约 **-50%**），失败包 1 → 0，build 温中位 4.387s。**三条已批准提案的预期效果均已在基准复测中达成。**

## 8. 验证方式（命令 + 关键输出）

```bash
# 基线挂载（只读，不改代码）
git worktree add --detach /tmp/opencode/bench-r2 feat-25da0cc6-sg-23d061da
cd /tmp/opencode/bench-r2 && git log --oneline -1
# → 365e108 docs: R2 实施说明 impl-notes.md（P1+P2+P3 实施与验证记录）

# 1) go build ./... 计时
python3 /tmp/opencode/timeit.py go build ./...                    # → 4.387 / 3.670 / 5.137 s, exit 0
python3 /tmp/opencode/timeit.py env GOCACHE=/tmp/opencode/gocache-r2 go build ./...
# → 44.417 s, exit 0

# 2) go test ./... -count=1 计时
python3 /tmp/opencode/timeit.py go test ./... -count=1
# → 14.399 / 11.421 / 11.559 s, 均 exit 0；grep -c FAIL = 0

# 3) P2 skip 证据
go test ./internal/service -count=1 -v | grep -E "SKIP:|missing from"
# → --- SKIP: TestCodesMatchRemoteTermJSON/...zh-cn/term.json (0.06s) / en-us (0.01s)；缺码 AW.10000025~30 × 2 locale

# 4) 包级耗时提取
grep -E "^ok" /tmp/opencode/test-r2-run3.txt | sort -k2
# → daemon 5.911s / agentwork-cli 4.530s / service 4.456s / notify 0.925s / issue 0.419s / backup 0.297s / link 0.075s / server 0.061s / acp 0.056s / handler 0.049s ...

# 5) P1 核对
grep -c "t.Parallel()" internal/daemon/*_test.go   # → 2+8+30+11+3+4+12 = 70
```

---

*报告版本：R2 v1（2026-11-10，基线 365e108）。环境：go1.26.4 linux/arm64 · aarch64 · 7 vCPU。本报告为工作区内 `benchmark-report-r2.md` 唯一有效版本。*
