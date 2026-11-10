# agentwork R1 构建/测试基准测量报告（bench）

- 日期：2026-11-10
- 基线：agentwork-int 仓库全新 checkout（worktree HEAD `33f16dc`，"Merge feat-98d60d12 (agentwork deliver)"）
- 原则：只测量、不修改任何代码、不修任何失败测试；失败如实记录。

## 1. 环境

| 项目 | 值 |
| --- | --- |
| go version | go1.26.4 linux/arm64 |
| 内核 / 架构 | Linux 5.10.0-182.0.0.95.r3353_274.hce2.aarch64 / aarch64 |
| CPU | 7 vCPU |
| GOCACHE | /root/.cache/go-build |
| GOMODCACHE | /root/go/pkg/mod |
| 工作区 | 全新 checkout，首次测量前无本地构建缓存（冷） |

## 2. 各命令总耗时

| 命令 | 场景 | 总耗时 | 退出码 |
| --- | --- | --- | --- |
| `go build ./...` | 冷（独立 GOCACHE，模块缓存已热） | 38.4 s | 0 |
| `go build ./...` | 温（标准 GOCACHE，同会话连续复跑 3 次） | 3.0 s / 4.4 s / 5.6 s（中位 4.4 s） | 0 |
| `go test ./... -count=1` | 冷（禁用测试缓存，首次执行） | 14.5 s | 1（有失败用例） |
| `go test ./... -count=1` | 温（第二次复跑，仍为 -count=1） | 15.6 s | 1（有失败用例） |

说明：`-count=1` 使测试结果不读缓存，因此两次测试耗时稳定在 14.5~15.6 s；构建冷/温差异主要来自编译缓存（38.4 s → 4.4 s）。

## 3. 包级测试耗时 Top10（首次 `go test ./... -count=1`，共 27 个包）

| 排名 | 包 | 耗时 | 结果 |
| --- | --- | --- | --- |
| 1 | github.com/eushing/agentwork/internal/daemon | 8.17 s | pass |
| 2 | github.com/eushing/agentwork/cmd/agentwork-cli | 5.15 s | pass |
| 3 | github.com/eushing/agentwork/internal/service | 4.47 s | **FAIL** |
| 4 | github.com/eushing/agentwork/internal/notify | 0.91 s | pass |
| 5 | github.com/eushing/agentwork/internal/issue | 0.33 s | pass |
| 6 | github.com/eushing/agentwork/internal/backup | 0.30 s | pass |
| 7 | github.com/eushing/agentwork/internal/card/feishu | 0.05 s | pass |
| 8 | github.com/eushing/agentwork/internal/server/handler | 0.04 s | pass |
| 9 | github.com/eushing/agentwork/internal/link | 0.03 s | pass |
| 10 | github.com/eushing/agentwork/internal/proto/acpbackend | 0.03 s | pass |

其余包均 < 0.03 s；`cmd/agentwork-daemon`、`cmd/validate-templates`、`internal/card`、`internal/events`、`internal/gitcodeapi`、`internal/store` 等无测试文件（"[no test files]"，未计入耗时排名）。

## 4. 失败用例清单（3 条，均属同一父测试）

均位于 `internal/service`（`codes_test.go`），父测试 `TestCodesMatchRemoteTermJSON` FAIL：

| # | 用例 | 耗时 | 失败原因（摘要） |
| --- | --- | --- | --- |
| 1 | `TestCodesMatchRemoteTermJSON` | 0.17 s | 父测试汇总失败 |
| 2 | `TestCodesMatchRemoteTermJSON/https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/zh-cn/term.json` | 0.17 s | 远端 CDN 词表缺少部分编码（zh-cn） |
| 3 | `TestCodesMatchRemoteTermJSON/https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/en-us/term.json` | 0.01 s | 远端 CDN 词表缺少部分编码（en-us） |

代表性失败日志（`codes_test.go:102`，en-us 子用例）：

```
code "AW.10000025" missing from https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/en-us/term.json — add it via the CDN approval flow
code "AW.10000026" missing from ...
code "AW.10000027" missing from ...
code "AW.10000028" missing from ...
code "AW.10000029" missing from ...
code "AW.10000030" missing from ...
```

失败性质判断：该测试依赖外部 CDN 资源（res-static.hc-cdn.cn 的 term.json），远端词表尚未同步仓库侧新增的 AW.10000025~AW.10000030 编码，属于外部依赖未同步，而非本地代码缺陷。按要求不做任何修复，如实记录。

## 5. 汇总

- 构建：冷 38.4 s，温 3.0~5.6 s（中位 4.4 s），构建全部成功。
- 测试：`go test ./... -count=1` 14.5 s（复跑 15.6 s），27 个包，24 包通过、3 包无测试文件不计、1 包失败（internal/service）。
- 失败用例：3 条（TestCodesMatchRemoteTermJSON 及其 zh-cn/en-us 两个子用例），根因是远端 CDN 词表缺码（AW.10000025~30），已如实记录、未做修复。

## 6. 验证方式（命令 + 输出）

```bash
# 环境
$ go version && uname -m
go version go1.26.4 linux/arm64
aarch64

# 冷构建（独立 GOCACHE）
$ rm -rf /tmp/opencode/gocache-cold && mkdir -p /tmp/opencode/gocache-cold
$ { time env GOCACHE=/tmp/opencode/gocache-cold go build ./... ; } 2>&1
real    0m38.426s
user    2m15.470s
sys     0m21.475s

# 温构建（标准 GOCACHE，复跑 3 次）
$ { time go build ./... ; } 2>&1
real    0m3.031s / 0m4.424s / 0m5.576s   （user/sys 略）

# 测试（-count=1 禁用缓存，JSON 输出解析包级耗时）
$ { time go test ./... -count=1 -json > /tmp/opencode/test-r1.json ; } 2>&1
real    0m14.521s
（exit=1，internal/service 失败）

# 复跑确认稳定性
$ { time go test ./... -count=1 ; } 2>&1
real    0m15.556s
FAIL
FAIL    github.com/eushing/agentwork/internal/service   4.575s

# 包级耗时 Top10 解析（python 按 go test -json 的 Action=pass/fail、无 Test 字段事件取 Elapsed）
# 详见第 3 节表格；失败用例与日志由 -json 事件流与 --- FAIL 输出提取，见第 4 节。
```
