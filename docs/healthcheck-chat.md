# 健康检查：聊天与建任务失败（opencode ACP `--pure` 兼容性问题）

目标：**5 分钟内定位"聊天打不开、建任务失败"，并给出最小修复**。

一句话结论：

> 内置探针表把 opencode 的 ACP 启动命令硬编码为 `opencode acp --pure`，而当前安装的
> opencode（实测 v2.2.5）已不识别 `--pure`，agent 进程在 ACP 握手前就退出 →
> 聊天打不开、建任务（解析/运行）失败。

## 1. 症状

- 给 agent 发消息（聊天）报错：`chat.open` 失败 / runtime 类错误；聊天永远打不开，或首条消息石沉大海。
- 建任务失败：一切走机器执行的运行（intake 解析、goal 编译等）派发即失败，错误详情里带 opencode 的 stderr：

  ```
  ERROR
    Unrecognized flag: --pure in command opencode acp
  ```

- 反直觉：`agentwork connect` 的探针报告里 opencode 是"健康"的，runtime 行也是 active ——
  因为探针只跑 `opencode --version`，**不会执行真正的 ACP 启动命令**。失败只在第一次真正 spawn 时出现。

## 2. 根因

- 内置探针表 `cmd/agentwork-cli/probe.go`（opencode 条目）把 ACP 启动命令硬编码为
  `["opencode", "acp", "--pure"]` —— 这是早期 opencode 的启动写法，内置表沿用至今。
- 当前安装的 opencode（实测 v2.2.5）的 `acp` 子命令只支持 `--login` 与全局 flags，
  `--pure` 直接报 `Unrecognized flag: --pure in command opencode acp` 并退出。
- ACP 是 stdio JSON-RPC 协议：agent 进程在 initialize 握手前就退出
  （握手发生在 `internal/proto/acpbackend/backend.go` 的 `sess.Initialize`），
  聊天 open 与任务派发必然失败。
- 影响链：聊天（`cmd/agentwork-cli/chat.go` 的 `handleChatOpen`）与任务派发
  （`internal/daemon/intake.go`、`internal/daemon/daemon.go`）都用 runtime 行里的
  `args`（即 ACPSpawn）spawn —— 所以**聊天与建任务一起挂**。
- 探针为什么没拦住：`probe_cmd` 只跑 `opencode --version`，成功 →
  `ReconcileProbeRuntimes`（`internal/service/machine.go`）把 runtime 行标记 active。
  "版本检查通过" ≠ "ACP spawn 可用"。

## 3. 健康检查步骤（5 分钟定位）

```bash
# 1) 复现真实 spawn：报 Unrecognized flag 即命中本问题
opencode acp --pure

# 2) 对照正确写法：应进入等待（不报错），Ctrl+C 退出
opencode acp

# 3) 查 runtime 行里的 args 是否还带着 --pure
agentwork-daemon paths          # 确认 DB 位置后
sqlite3 "$AGENTWORK_HOME/agentwork.db" \
  "SELECT name, args, status FROM runtime WHERE name LIKE 'opencode@%';"

# 4) 看机器日志：chat.open 失败 / run 失败详情里应有
#    "Unrecognized flag: --pure in command opencode acp"
```

判据：`opencode acp --pure` 报 `Unrecognized flag`，且 runtime 行 `args` 含 `--pure` →
命中本问题，按第 4 节修复。

## 4. 修复

### 4.1 立即修复（不改代码）：agents.yaml 覆盖内置条目

`~/.agentwork/agents.yaml` 同名条目**覆盖内置**（`mergeProbeTables`：custom wins），
配置热重载（connect 启动时与每个探针 tick 都会重读），无需重启：

```yaml
agents:
  - name: opencode
    probe_cmd: "opencode --version"
    acp_spawn: ["opencode", "acp"]   # 去掉 --pure
    skills_dir: "~/.config/opencode/skills"
    project_skills_dir: ".opencode/skill"
    profile_files: ["AGENTS.md"]
```

覆盖后无需手工改库：`ReconcileProbeRuntimes` 会用新 `args` upsert runtime 行
（`ON CONFLICT(name) DO UPDATE SET args=excluded.args`），下一次聊天/建任务即恢复。

### 4.2 代码修复：probe.go

`cmd/agentwork-cli/probe.go` 的 opencode 条目：

```go
// 修复前
ACPSpawn: []string{"opencode", "acp", "--pure"},
// 修复后
ACPSpawn: []string{"opencode", "acp"},
```

升级内置表后，用 4.1 的 agents.yaml 覆盖仍可保留（同名覆盖即兜底，两处写法一致即可）。

## 5. 预防

- 探针不能只 `--version`：版本检查通过 ≠ ACP spawn 可用。后续可让 probe 实测一次
  ACPSpawn（spawn → initialize 握手成功 → 立即退出），把"能握手"纳入 runtime active 的判定，
  让这类失败在 connect 阶段就被暴露，而不是等到第一次聊天/建任务。
- 升级 opencode 大版本后，跑一遍第 3 节的健康检查确认 ACP 启动命令仍有效。
