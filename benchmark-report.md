# agentwork-int 构建/测试基准报告（RSI R1）

- 测量人：bench
- 日期：2026-11-10
- 基线：agentwork-int（HEAD：33f16dc "Merge feat-98d60d12 (agentwork deliver)"）
- 约束遵守：未改动任何代码、未修复任何失败测试；失败如实记录。

## 1. 环境

| 项目 | 值 |
| --- | --- |
| go version | go1.26.4 linux/arm64 |
| uname -m | aarch64 |
| OS | Linux |
| 工作目录 | agentwork worktree（仓库根） |

## 2. 命令总耗时

| 命令 | real | user | sys | 结果 |
| --- | --- | --- | --- | --- |
| `time go build ./...` | 11.574s | 10.988s | 1.785s | ✅ 全部构建成功 |
| `time go test ./... -count=1` | 23.012s | 50.328s | 14.854s | ❌ 1 个包失败（internal/service） |

说明：`go test` 的 user+sys（≈65s）远大于 real（23s），说明包级测试并行度较高；各包耗时之和（≈20.0s）小于总 wall time，差额为并行调度与编译开销。

## 3. 包级耗时表（go test ./... -count=1）

按耗时降序，Top 10 加粗：

| 排名 | 包 | 耗时 | 状态 |
| --- | --- | --- | --- |
| 1 | internal/daemon | 7.696s | ok |
| 2 | cmd/agentwork-cli | 5.751s | ok |
| 3 | internal/service | 4.568s | **FAIL** |
| 4 | internal/notify | 0.860s | ok |
| 5 | internal/issue | 0.287s | ok |
| 6 | internal/backup | 0.245s | ok |
| 7 | internal/server/handler | 0.045s | ok |
| 8 | internal/server | 0.039s | ok |
| 9 | internal/link | 0.027s | ok |
| 10 | internal/paths | 0.021s | ok |
| 11 | internal/zipx | 0.018s | ok |
| 12 | internal/proto/acpbackend | 0.013s | ok |
| 13 | internal/acp | 0.008s | ok |
| 14 | internal/card/feishu | 0.008s | ok |

无测试文件的包（`? [no test files]`，13 个）：cmd/agentwork-daemon、cmd/validate-templates、internal/card、internal/events、internal/gitcodeapi、internal/gitutil、internal/logging、internal/proto、internal/proto/jsonlbackend、internal/proto/jsonrpcbackend、internal/runtime、internal/server/ws、internal/store。

## 4. 失败用例清单

共 1 个顶层测试失败（internal/service），含 2 个子测试，失败原因一致：**远端 CDN term.json 缺少 6 个错误码**（外部依赖数据不全，非本地代码缺陷）。

- `TestCodesMatchRemoteTermJSON`（codes_test.go:102）
  - 子测试 `https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/zh-cn/term.json`（0.29s）— FAIL
  - 子测试 `https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/en-us/term.json`（0.01s）— FAIL
  - 缺失错误码（两个 locale 各报 6 条，共 12 条日志）：`AW.10000025`、`AW.10000026`、`AW.10000027`、`AW.10000028`、`AW.10000029`、`AW.10000030`
  - 错误信息：`code "AW.100000xx" missing from <term.json URL> — add it via the CDN approval flow`

其余 13 个有测试的包全部通过（ok）。

## 5. 复现方式

```bash
go version && uname -m
{ time go build ./... ; } 2>&1
{ time go test ./... -count=1 ; } 2>&1 | tee /tmp/test.log
grep -E '^(ok|FAIL|\?)' /tmp/test.log
grep -A8 '^--- FAIL' /tmp/test.log
```
