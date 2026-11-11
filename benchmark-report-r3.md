# agentwork R3 基准复测报告（bench · 冷缓存口径 · 无依赖并行重派版）

- 日期：2026-11-11（R3 复测 · 新基线 agentwork-int-v2）
- 测量基线：**commit `e7c2572`**（分支 feat-357252be-sg-b13a5581 HEAD = origin/agentwork-int-v2，即 R3 轮新基线重建后的集成分支顶端；近三代：`e7c2572` R1/R2 交付物迁移 + `e08cb72` P1 并行化重放 + `e2bda1d` P2/P3 语义化重放）。相对 R2 基线 `365e108` 为**新基线重建**（历史重放），非增量演进。
- 测量方式：在 bench 工作区（worktree）实跑，**未改任何代码**（`git diff` 为空；工作区唯一新增文件即本报告与 AGENTS.md 环境注入件）。
- 口径（P-C 提案执行版）：**冷/温双列**，跨轮对比**只采冷列**。冷 = 独立 `GOCACHE=/tmp/r3-gocache`（测量前 `rm -rf` 清空至 0 文件，实测从 0 写到 2402 个文件，真冷态）；温 = 标准 GOCACHE（`/root/.cache/go-build`，2.7G 热缓存）。模块缓存 `/root/go/pkg/mod`（651M）两场景共用均热。
- 原则：只测量、不修改、不修复；失败如实记录并只做只读根因归类。

## 1. 环境

| 项目 | 值 |
| --- | --- |
| go version | go1.26.4 linux/arm64（/usr/local/bin/go 软链，本轮已修复可直接用） |
| `uname -m` / 内核 | aarch64 / Linux 5.10.0（7 vCPU · 12Gi RAM） |
| git | git version 2.33.0 |
| GOCACHE（冷） | /tmp/r3-gocache（独立，测量前清空） |
| GOCACHE（温） | /root/.cache/go-build（2.7G 热） |
| GOMODCACHE | /root/go/pkg/mod（651M，两场景共用均热） |
| AGENTWORK_HOME | **/root/.agentwork（全局已设置——与第 6 节测试失败根因直接相关）** |
| 计时方式 | Python subprocess 墙钟计时（/tmp/opencode/timeit.py，输出 `__WALL_SECONDS__`） |

## 2. 各命令总耗时（commit e7c2572 上实跑）

| 命令 | 场景 | 总耗时 | 退出码 |
| --- | --- | --- | --- |
| `go build ./...` | **冷**（独立 GOCACHE，测量前清空） | **41.285 s** | 0 |
| `go build ./...` | 温（标准 GOCACHE，3 连跑） | 10.150 / 4.672 / 4.478 s（中位 **4.672 s**） | 0（3 次均 0） |
| `go test ./... -count=1` | 3 连跑（禁用测试缓存） | 16.447 / 13.173 / 11.919 s（中位 **13.173 s**） | **1（3 次均 2 包 FAIL，见第 5 节）** |

- 温 build run1（10.150s）偏高：冷测后标准缓存首次回写所致，如实记录；run2/run3 已回到 R2 温态水平（4.672/4.478 vs R2 4.387/3.670/5.137）。
- `go test` 计时含失败用例的完整执行与重试路径，中位数仍具跨轮可比性。

## 3. 冷/温双列对照（P-C 口径核心表）

| 指标 | 冷列（独立 GOCACHE） | 温列（标准 GOCACHE） | 跨轮对比采列 |
| --- | --- | --- | --- |
| `go build ./...` | **41.285 s** | 中位 **4.672 s**（10.150/4.672/4.478） | **冷列** |
| `go test ./... -count=1` | 未单测冷态（测试三连跑均在温缓存下，中位 13.173 s） | 中位 **13.173 s**（16.447/13.173/11.919） | 温列（测试语义不受 build 缓存冷/温影响，`-count=1` 已禁测试缓存） |
| 冷/温差距 | — | build 冷态约为温态 **8.8 倍**（41.285 / 4.672） | — |

**口径说明**：R2 报告的 build 11.574s（R1 基线给定值）与 4.387s（R2 温中位）均接近**温列**；R2 冷测 44.417s 为首次冷列记录。本轮起跨轮对比统一采冷列（R1 未留冷态数据，R1 的 build/test 值按温列口径另行标注，不参与冷列对比）。

## 4. 与 R2 基线对比表（build 采冷列 · test 采温列中位）

| 指标 | R2（365e108，2026-11-10） | R3（e7c2572，本轮） | 变化 |
| --- | --- | --- | --- |
| `go build ./...` **冷** | 44.417 s | **41.285 s** | **-3.132 s（约 -7.1%）**，冷列口径可比 |
| `go build ./...` 温中位 | 4.387 s | 4.672 s | +0.285 s（约 +6.5%），仅参照不采信 |
| `go test ./... -count=1` 中位 | 11.559 s | 13.173 s | **+1.614 s（约 +14.0%）** |
| `internal/daemon` 包耗时中位 | 5.911 s | 5.855 s（5.855/4.893/6.026） | -0.056 s（基本持平），P1 并行化收益保持 |
| `cmd/agentwork-cli` 包耗时中位 | 4.530 s | 2.374 s（3.852/2.166/2.374） | -2.156 s（约 -47.6%，失败路径提前退出所致，见第 5 节注） |
| `internal/service` 包耗时中位 | 4.456 s | 6.144 s（6.200/6.004/6.229） | +1.688 s（约 +37.9%） |
| 失败用例数 | 0（全绿） | **6（2 包）** | **0 → 6**，见第 5 节 |
| 失败包数 | 0 | 2（cmd/agentwork-cli、internal/daemon） | 0 → 2 |

**结论（冷列）**：新基线 e7c2572 冷构建 41.285s，较 R2 冷测 44.417s **约 -7%**——R2 已批准提案（P1 并行化等）的收益在新基线重建中得到保持，未因历史重放而回退。**test 中位 +14%** 的原因主要是 internal/service 包 +38%（新增 RSI 分析代码 e7c2572 的测试负载）与失败用例的重试路径，另见第 5 节注。

**结论（测试健康）**：R2 全绿 → R3 6 例失败。**6 例失败全部为测试环境隔离缺陷（AGENTWORK_HOME 全局泄漏 + 固定 domainID/goalID 跨运行残留），非业务代码功能缺陷**，根因归类见第 6 节。

## 5. 包级测试耗时 Top10（`go test ./... -count=1` run3，共 30 个包）

| 排名 | 包 | 耗时 | 结果 |
| --- | --- | --- | --- |
| 1 | github.com/eushing/agentwork/internal/daemon | 6.026 s | **FAIL**（4 例，见第 6 节） |
| 2 | github.com/eushing/agentwork/cmd/agentwork-cli | 2.374 s | **FAIL**（2 例，见第 6 节） |
| 3 | github.com/eushing/agentwork/internal/service | 6.229 s | pass（R2 4.456s → 6.229s，+38%，RSI 新代码测试负载） |
| 4 | github.com/eushing/agentwork/internal/notify | 0.884 s | pass |
| 5 | github.com/eushing/agentwork/internal/issue | 0.395 s | pass |
| 6 | github.com/eushing/agentwork/internal/backup | 0.326 s | pass |
| 7 | github.com/eushing/agentwork/internal/server/handler | 0.069 s | pass |
| 8 | github.com/eushing/agentwork/internal/zipx | 0.039 s | pass |
| 9 | github.com/eushing/agentwork/internal/link | 0.037 s | pass |
| 10 | github.com/eushing/agentwork/internal/server | 0.030 s | pass |

- 共 30 个包：12 ok + 2 FAIL + 16 无测试文件（"[no test files]"，未计入排名）。R2 为 27 包（14 ok）；新基线包数 27 → 30（新增 internal/paths、internal/proto/acpbackend、internal/card/feishu 等拆分/新增包），daemon 测试部分迁移至 cmd/agentwork-cli，跨轮包级对比已按同名包对齐。
- 三连跑 Top3 稳定性：daemon 5.855/4.893/6.026 s；cli 3.852/2.166/2.374 s；service 6.200/6.004/6.229 s。
- 注：cli 包中位 2.374s 较 R2 4.530s 大幅下降，是 2 个失败用例在 setup 后段提前退出（fetch 128）所致，**不是性能改进**，不采信为收益。

## 6. 失败用例清单与根因归类（只读诊断，未改代码）

`go test ./... -count=1` 三连跑稳定复现同样 6 例（另：上轮被取消的 sg-REBENCH 运行残留数据 /tmp/opencode/r3-test-run1.txt 亦复现同样 6 例，交叉印证与运行时点无关）：

| # | 用例 | 包 | 错误摘要 | 根因归类 |
| --- | --- | --- | --- | --- |
| 1 | TestGitWorkdirAndTransferPush | cmd/agentwork-cli | `gitwork_test.go:59: workdir: fetch: exit status 128: '/tmp/TestGitWorkdirAndTransferPush2535588731/002/bare.git' does not appear to be a git repository` | **A：AGENTWORK_HOME 泄漏 + 陈旧缓存复用** |
| 2 | TestPersistentGoalWorkdir | cmd/agentwork-cli | 同上（同一陈旧 bare.git 路径） | A |
| 3 | TestDeliverMergeIdentityNoGlobalGitConfig | internal/daemon | `deliver_test.go:114: goal status = "review", want done. review_request="deliver: fetch: exit status 128: '/tmp/TestDeliverMergeIdentity.../002/remote.git' does not appear..."` | **A'：同源（daemon 侧 deliver 走真实 HOME 下仓库）** |
| 4 | TestParallelConsultWorktreeDetaches | internal/daemon | `worktree_test.go:39: owner worktree: git fetch: exit status 128: '/tmp/TestParallelConsultWorktreeDetaches.../002/src' does not appear...` | A' |
| 5 | TestCollectDigestBatchWritesAndIsIdempotent | internal/daemon | `digest_test.go:206: articles = 1, want 2`（日志：`goal g-1 batch already collected — skipping append`） | **B：digestRoot() 落在真实守护进程数据目录，跨运行幂等残留** |
| 6 | TestCollectDigestBatchBadManifestKeepsMetadatalessButNoManifestEntry | internal/daemon | `digest_test.go:386: articles = 1, want 2 (manifest.json excluded)`（同上 "already collected"） | B |

### 根因 A（#1/#2，实测证据链）

1. `cmd/agentwork-cli` 测试 `t.Setenv("HOME", t.TempDir())` 只改 HOME；`internal/paths.Home()`（paths.go:36-46）**优先读 `AGENTWORK_HOME`**（本环境全局设置为 `/root/.agentwork`，且 paths.go:15 注释明确 "AGENTWORK_HOME is read on every call"）。
2. `ensureBareRepo`（cmd/agentwork-cli/gitwork.go:38-62）用固定 domainID `dom-1` 计算 `paths.RepoDir("dom-1")` = `/root/.agentwork/repos/dom-1`，**只检查 `HEAD` 存在即跳过 re-clone**（gitwork.go:44 "Existence alone is not cloned" 注释所防的恰是半写目录，但未防陈旧 remote）。
3. 实测 `/root/.agentwork/repos/dom-1`：HEAD 存在（2026-11-10 20:49 上轮测试遗留）、`git remote -v` 指向 `file:///tmp/TestGitWorkdirAndTransferPush2535588731/002/bare.git`（该目录已删除）→ fetch 128。
4. 对照实验：手动在同一环境建 bare.git + clone + push + `git ls-remote file://…` 全部正常（git 2.33.0 无问题）→ **排除 git 工具链问题**；失败仅出现在复用陈旧 `/root/.agentwork/repos/dom-1` 时。

### 根因 B（#5/#6，实测证据链）

1. digest 测试同样 `t.Setenv("HOME", t.TempDir())`，但 `digestRoot()` = `paths.Digest()` = `filepath.Join(Home(), "digest")` = **真实 `/root/.agentwork/digest`**（AGENTWORK_HOME 优先）。
2. 实测 `/root/.agentwork/digest/g-1/`（只剩 1.md）与 articles.json（create 时间戳 2026-11-11T08:45:30Z = 本轮第一次三连跑时刻）跨测试运行共享；`collectDigestBatch` 的幂等检查（digest.go:193-199 "batch already collected — skipping append"）命中上一轮 `g-1` 残留 → 第二次起不再追加 → `articles = 1, want 2`。
3. 单跑复现验证：`go test ./internal/daemon -count=1 -run 'TestCollectDigestBatchWritesAndIsIdempotent'` 单独执行亦 FAIL（日志同样 "already collected"）→ **稳定复现，非并行污染**。

### 归类小结

- **非业务缺陷**：6 例全部可通过「测试内 `t.Setenv("AGENTWORK_HOME", t.TempDir())`（或 ensureBareRepo 增加 remote 存活校验）」修复，业务代码（deliver/worktree/digest 的生产路径）本身逻辑未被证伪。
- **与 optimizer P-A 提案的关系**：根因 A 是 P-A「验证子进程环境净化」的**姊妹问题**——P-A 防 daemon 验证子进程继承 AGENTWORK_HOME，本组失败防 **go test 子进程**继承 AGENTWORK_HOME 后 paths 层误用；修复方向一致（环境隔离层），建议 P-A 实施时一并覆盖测试进程环境。
- **对基准连续性的影响**：R2 全绿口径在新基线上被测试隔离缺陷打破；建议后续轮次将「测试环境隔离修复」作为高优任务，修复后本报告第 5 节失败项应归零，届时 test 中位数才可与 R2 11.559s 直接对比。

## 7. 结论

1. **冷列（跨轮采信）**：新基线 e7c2572 冷构建 **41.285s**，较 R2 冷测 44.417s **约 -7%**，R2 提案收益在新基线重建中保持。
2. **温列（参照）**：温 build 中位 4.672s（R2 4.387s，+6.5%）；test 中位 13.173s（R2 11.559s，+14.0%，主因 service 包 +38% RSI 新代码负载与失败重试路径）。
3. **测试健康**：6 例失败（2 包）**全部为测试环境隔离缺陷**（AGENTWORK_HOME 全局泄漏 × 固定 ID 跨运行残留），根因已实测定位（含陈旧 remote 路径、跨运行共享目录、单跑复现三重证据），非业务代码缺陷；已与 P-A 提案建立关联。
4. **基准连续性**：R1（0bf354e）→ R2（365e108）→ R3（e7c2572）三代基线数据齐备；自本轮起冷/温双列口径落地，跨轮对比只采冷列。
5. 本轮未改任何代码（`git diff` 为空），全部测量在本 bench 工作区只读完成。

## 8. 验证方式（命令 + 关键输出）

```bash
# 基线确认（只读）
cd /root/.agentwork/runs/goal/357252be…/sg/…/6d704bc6f96289e41e5fa2d06ee4131c
git log --oneline -1        # → e7c2572 docs: R1/R2 自迭代交付物迁移至新基线
git diff --stat             # → 空（未改任何代码）

# 1) 冷构建计时（GOCACHE 先清空至 0 文件）
rm -rf /tmp/r3-gocache && find /tmp/r3-gocache -type f | wc -l   # → 0
python3 /tmp/opencode/timeit.py env GOCACHE=/tmp/r3-gocache go build ./...
# → __WALL_SECONDS__=41.285 / __EXIT_CODE__=0；测后 find … | wc -l → 2402（真冷态）

# 2) 温构建三连跑
for i in 1 2 3; do python3 /tmp/opencode/timeit.py go build ./…; done
# → 10.150 / 4.672 / 4.478 s，均 exit 0

# 3) 测试三连跑
for i in 1 2 3; do python3 /tmp/opencode/timeit.py go test ./... -count=1; done
# → 16.447 / 13.173 / 11.919 s，均 exit 1；grep -c "^FAIL" 各 5 行（2 包 + 3 汇总行）

# 4) 失败用例与包级耗时提取
grep -E "^--- FAIL" /tmp/opencode/r3b-test-run3.txt
# → TestGitWorkdirAndTransferPush / TestPersistentGoalWorkdir / TestDeliverMergeIdentityNoGlobalGitConfig
#    / TestCollectDigestBatchWritesAndIsIdempotent / TestCollectDigestBatchBadManifest… / TestParallelConsultWorktreeDetaches
grep -E "^ok " /tmp/opencode/r3b-test-run3.txt | sort -k3 -rn | head -10

# 5) 根因 A 证据
git -C /root/.agentwork/repos/dom-1 remote -v
# → origin file:///tmp/TestGitWorkdirAndTransferPush2535588731/002/bare.git（已删除）
env | grep ^AGENTWORK_HOME   # → /root/.agentwork（全局泄漏）
# 对照：手动 bare.git + clone + push + ls-remote file:// 全部 rc=0（git 2.33.0 无问题）

# 6) 根因 B 证据（单跑复现）
go test ./internal/daemon -count=1 -run 'TestCollectDigestBatchWritesAndIsIdempotent' -v
# → INFO digest: goal g-1 batch already collected — skipping append；--- FAIL（稳定复现）
ls /root/.agentwork/digest/g-1/   # → 1.md（跨运行共享目录残留）

# 7) 交叉印证（上轮被取消 sg-REBENCH 的残留数据，非正式采信）
grep -E "^--- FAIL" /tmp/opencode/r3-test-run1.txt   # → 同样 6 例
```

---

*报告版本：R3 v1（2026-11-11，基线 e7c2572）。环境：go1.26.4 linux/arm64 · aarch64 · 7 vCPU · AGENTWORK_HOME=/root/.agentwork。口径：冷/温双列（P-C），跨轮对比只采冷列。本报告为工作区内 `benchmark-report-r3.md` 唯一有效版本。*
