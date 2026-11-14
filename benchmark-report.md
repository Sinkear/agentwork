# agentwork R1 构建/测试基准测量报告（bench）

- 日期：2026-11-10（R1 复跑 · 冲突重做修订 v2）
- 基线：**agentwork-int 新集成基线 `0bf354e`**（"Integrate 46d681ea…"，含 optimizer 提案 proposals.md；相对旧基线 `33f16dc` 仅新增文档，无代码变更）
- 本修订替换此前基于 `33f16dc` 的 v1 修订（该修订在集成时与同队另一条 bench 运行的同名 `benchmark-report.md` 发生路径冲突）。
- 原则：只测量、不修改任何代码、不修任何失败测试；失败如实记录。本报告为工作区内该文件唯一有效版本。

## 1. 环境

| 项目 | 值 |
| --- | --- |
| go version | go1.26.4 linux/arm64 |
| 内核 / 架构 | Linux 5.10.0-182.0.0.95.r3353_274.hce2.aarch64 / aarch64 |
| CPU | 7 vCPU |
| GOCACHE | /root/.cache/go-build |
| GOMODCACHE | /root/go/pkg/mod |
| 工作区 | 全新 checkout（含 v1 报告文件），标准 GOCACHE 为热缓存；冷构建用独立 GOCACHE 单测 |

## 2. 各命令总耗时（在基线 0bf354e 上完整复跑）

| 命令 | 场景 | 总耗时 | 退出码 |
| --- | --- | --- | --- |
| `go build ./...` | 冷（独立 GOCACHE=/tmp/opencode/gocache-verify，模块缓存热） | 40.6 s | 0 |
| `go build ./...` | 温（标准 GOCACHE，测后复跑验证） | 3.9 s | 0 |
| `go test ./... -count=1` | 冷（禁用测试缓存，完整执行） | 11.3 s | 1（有失败用例） |

说明：v1（基线 33f16dc）冷构建 38.4 s、温构建中位 4.4 s、测试 14.5 s；本轮（基线 0bf354e）数据同量级，测试 wall 由 14.5 s 降至 11.3 s 属机器负载波动范围（Top 包 daemon 8.17s → 8.00s 基本不变）。

## 3. 包级测试耗时 Top10（`go test ./... -count=1`，共 27 个包）

| 排名 | 包 | 耗时 | 结果 |
| --- | --- | --- | --- |
| 1 | github.com/eushing/agentwork/internal/daemon | 8.00 s | pass |
| 2 | github.com/eushing/agentwork/internal/service | 4.61 s | **FAIL** |
| 3 | github.com/eushing/agentwork/cmd/agentwork-cli | 4.20 s | pass |
| 4 | github.com/eushing/agentwork/internal/notify | 0.89 s | pass |
| 5 | github.com/eushing/agentwork/internal/issue | 0.30 s | pass |
| 6 | github.com/eushing/agentwork/internal/backup | 0.26 s | pass |
| 7 | github.com/eushing/agentwork/internal/server/handler | 0.05 s | pass |
| 8 | github.com/eushing/agentwork/internal/paths | 0.04 s | pass |
| 9 | github.com/eushing/agentwork/internal/server | 0.03 s | pass |
| 10 | github.com/eushing/agentwork/internal/acp | 0.03 s | pass |

其余包均 < 0.03 s；`cmd/agentwork-daemon`、`cmd/validate-templates`、`internal/card`、`internal/events`、`internal/gitcodeapi`、`internal/store` 等无测试文件（"[no test files]"，未计入排名）。

## 4. 失败用例清单（3 条，与 v1 一致）

均位于 `internal/service`（`codes_test.go`），父测试 `TestCodesMatchRemoteTermJSON` FAIL：

| # | 用例 | 耗时 | 失败原因（摘要） |
| --- | --- | --- | --- |
| 1 | `TestCodesMatchRemoteTermJSON` | 0.27 s | 父测试汇总失败 |
| 2 | `TestCodesMatchRemoteTermJSON/https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/zh-cn/term.json` | 0.26 s | 远端 CDN 词表缺少部分编码（zh-cn） |
| 3 | `TestCodesMatchRemoteTermJSON/https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/en-us/term.json` | 0.01 s | 远端 CDN 词表缺少部分编码（en-us） |

缺失编码集合（两份词表一致，由 `go test -json` 输出提取）：

```
AW.10000025  AW.10000026  AW.10000027
AW.10000028  AW.10000029  AW.10000030
```

代表性失败日志（`codes_test.go:102`）：

```
code "AW.10000025" missing from https://res-static.hc-cdn.cn/asset/locales/PROD/CloudMarket/hds/pc/en-us/term.json — add it via the CDN approval flow
```

失败性质判断：该测试依赖外部 CDN 资源（res-static.hc-cdn.cn 的 term.json），远端词表尚未同步仓库侧新增的 AW.10000025~AW.10000030 编码，属外部依赖未同步，而非本地代码缺陷。按要求不做任何修复，如实记录。

## 5. 汇总

- 构建：冷 40.6 s，温 3.9 s，构建全部成功。
- 测试：`go test ./... -count=1` 11.3 s；27 个包，24 包通过、1 包失败（internal/service）、其余无测试文件。
- 失败用例：3 条（TestCodesMatchRemoteTermJSON 及 zh-cn/en-us 两个子用例），根因为远端 CDN 词表缺码（AW.10000025~30），已如实记录、未修复。
- 与 v1 差异：无实质差异；失败用例集合与根因完全一致，包级耗时排序因波动略有变化（daemon 仍为最慢包）。

## 6. 验证方式（命令 + 输出）

```bash
# 环境
$ go version && uname -m
go version go1.26.4 linux/arm64
aarch64

# 冷构建（独立 GOCACHE，基线 0bf354e）
$ { time env GOCACHE=/tmp/opencode/gocache-verify go build ./... ; } 2>&1
real    0m40.627s
user    2m17.678s
sys     0m23.475s

# 温构建复跑验证
$ { time go build ./... ; } 2>&1
real    0m3.889s

# 测试（-count=1 禁用缓存，JSON 输出解析包级耗时与失败用例）
$ { time go test ./... -count=1 -json > /tmp/opencode/test-r1v2.json ; } 2>&1
real    0m11.279s
（exit=1，internal/service 失败）

# 失败用例（-json 事件流提取）
FAIL 0.27s internal/service :: TestCodesMatchRemoteTermJSON
FAIL 0.26s internal/service :: TestCodesMatchRemoteTermJSON/...zh-cn/term.json
FAIL 0.01s internal/service :: TestCodesMatchRemoteTermJSON/...en-us/term.json
missing codes: AW.10000025 ~ AW.10000030

# 代码零改动：git status 仅本报告文件（新增），无任何源码修改
```
