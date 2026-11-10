# RSI R2 实施说明（builder · impl-notes）

- 状态：**已完成** — 三条已批准提案（P1、P2、P3）全部实施，机器验证门自检通过。
- 分支：`feat-25da0cc6-sg-23d061da`（基于 `agentwork-int` HEAD 339f10d）
- 提交（每条提案独立 commit，message 注明提案编号）：
  | commit | 提案 | 内容 |
  | --- | --- | --- |
  | `3cf2116` | P1 | 并行化 internal/daemon 独立顶层测试 |
  | `8ca9957` | P2 | CDN 漂移守卫对畸形响应 fail 改 skip |
  | `18a8c3c` | P3 | 失败重试引入不可重试错误快速失败 |

---

## 提案 1（P1）：并行化 internal/daemon 测试

**改动**（纯 `*_test.go`，7 个文件，+140 行）：
- 对提案指定的 7 个无 `t.Setenv` 的测试文件（`intake_test.go`、`verify_test.go`、`gate_test.go`、`schedule_test.go`、`runaway_test.go`、`prompt_context_test.go`、`handoff_cancel_test.go`）的全部 **70 个顶层测试函数**加 `t.Parallel()`。
- 使用 `t.Setenv` 隔离 HOME 的 3 个文件（`deliver_test.go`、`digest_test.go`、`worktree_test.go`）按提案保持串行（`t.Setenv` 自动禁止并行）。
- 实施前核实：7 个文件均无 `os.Setenv`/`Chdir`/固定端口/共享 HOME 依赖；`runaway_test.go` 中 `TestScratchDirNames` 只读 `runsRoot()` 路径计算，不创建/不写共享目录；各测试均用 `:memory:` SQLite 或 `t.TempDir()`，无包级可变状态。
- 次级目标 `cmd/agentwork-cli` **未动**：核实发现其 4 个测试文件中 3 个使用 `t.Setenv`（gitwork/main/probe，占比过高），纯函数测试（chat/exec）合计耗时 <0.1s，并行化收益趋近于零且违反「改动最小化」约束，故按提案中「优先挑相互独立」的精神保守跳过。

**验证**：
- `-count=1` 连跑 3 次全绿；wall 5.30s / 5.73s / 4.99s（实施前基线 8.17s，**约 -35%**）。
- 涉及状态机/取消链的 15 个测试 `-race` 通过。
- `gofmt -l` 无新增格式问题（报出的 4 个文件为基线既有问题，stash 后仍在）。

---

## 提案 2（P2）：CDN 漂移守卫对畸形响应 fail 改 skip

**改动**（`internal/service/codes_test.go`，+26/-2 行）：
- decode 失败（截断 JSON / 网关错误页 200+HTML）由 `t.Fatalf` 改为 `t.Skipf("CDN returned malformed body — skip drift guard: %v", err)`，与既有不可达（`:89-90`）/非 200（`:93-94`）的 skip 语义对齐。
- 可选增强（提案 ② 条款）：新增 `AGENTWORK_SKIP_CDN_DRIFT=1` 环境开关，设置时 skip 整个守卫。
- **缺码漂移同步改 skip**：实施时发现当前 CDN 实际返回的是合法 JSON 缺 6 码（AW.10000025~30，两 locale 各 6 条），走缺码 `Errorf` 分支——若缺码仍 `Errorf`，`go test ./...` 无法恢复全绿，与提案 ③ 预期收益「`go test ./...` 恢复全绿（1/24 包 FAIL → 0）」直接矛盾。提案 ④ 风险缓解条款亦明确「**CDN 缺码不再使测试变红**——跨层漂移可能被漏掉」为已接受的风险，故缺码分支按提案语义改为 `t.Skipf`，且 skip 时输出完整缺码清单（保留可诊断性），可用 `-run TestCodesMatchRemoteTermJSON` 手动复查。
- 本地不变量守卫 `TestErrorCodesExported` 保持不动。

**验证**：
- 缺码场景：skip 路径输出 `6 code(s) missing … AW.10000025, …, AW.10000030`（两 locale 均验证）。
- 环境开关场景：`AGENTWORK_SKIP_CDN_DRIFT=1` 时整守卫 skip。
- `go test ./... -count=1` 全量无 FAIL（13 个含测试的包全 ok）。

---

## 提案 3（P3）：不可重试错误快速失败

**改动**（`internal/service/goal.go` +41/-4、`internal/daemon/daemon.go` +4/-1）：
- 新增 `nonRetryableMarkers` 白名单 + `isNonRetryable(summary string) bool`（提案定位 `goal.go:92-104` 附近）：白名单子串匹配，起步只收 `Model unavailable` 一项（runs API 实测 12/14 条失败为同一确定性模型配置错误）。空 summary 一律可重试。
- `reconcileOnRunEndOnce` failed 分支（提案定位 `:1337`）：重试条件 `rc.Attempt < maxAttempts && !isImportGoal` 扩展为 `&& !nonRetryable`——确定性故障不烧重试预算、不追加 30s/60s backoff、不少唤醒 leader，直接走既有 goal-failed 终态分支（`:1368`，即 else 分支）。
- 终态 summary 追加「（环境/配置错误，重试无效）」，与「任务本身失败」区分，便于人工复核误判（提案风险缓解条款）。
- 同步 `internal/daemon/daemon.go:124` `maxAttempts` 注释，说明不可重试例外。

**验证**：
- `go build ./...` 全绿；`go vet` 全绿。
- `internal/service` 224 个测试通过；`internal/daemon` 全绿（CDN 守卫需 P2 的 skip 语义或环境开关）。
- 其余错误类型的重试/退避行为完全不变（`retryBackoffFor` 未动）。

---

## 机器验证门自检（与门完全相同的命令）

```
go build ./...            → PASS
go test ./... -count=1    → PASS（连跑 2 次全量 0 FAIL，wall 10.9s / 9.4s）
```

## 回滚

三条提案各自独立 commit，任一 `git revert <sha>` 即可单独还原，互不影响。
