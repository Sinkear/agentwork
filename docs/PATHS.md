# AgentWork 路径与数据布局

这份文档回答两个问题：

1. AgentWork 到底把数据写到哪里？
2. 升级时哪些目录必须保留、哪些可以随制品一起替换？

## 0. 一句话原则

> **`bin/`、`web/` 是无状态制品，可以整体替换；`AGENTWORK_HOME` 是状态，必须独立于制品并持久化。**

从这一版开始，所有持久化路径都通过 `internal/paths` 统一计算：

```bash
# 默认值（兼容已有安装）
~/.agentwork

# 生产环境建议显式指定，放在制品目录和 /workspace 之外
export AGENTWORK_HOME=/root/agentwork-data
```

还支持单独覆盖 SQLite 文件：

```bash
export AGENTWORK_DB=/data/agentwork/agentwork.db
```

`AGENTWORK_HOME` 支持 `~/xxx` 展开，并且**每次读取都会重新求值**，不会被进程启动时缓存，因此测试和容器场景都可以安全使用。

查看当前进程实际会使用的全部路径：

```bash
# daemon 端
agentwork-daemon paths

# 机器端（远程执行机 / CLI 所在机器）
agentwork paths
```

命令输出 JSON，字段包括 `home`、`database`、`runs`、`repos`、`digest`、`daemon_log`、`cli_log`、`cli_state`、`agents_config`、`pending_reports`、`agents`、`skills`、`chat`、`bin`。

## 1. Daemon 端（服务端 / 调度器）

所有下表路径默认都在 `$AGENTWORK_HOME` 下，默认即 `~/.agentwork`。

| 路径 | 内容 | 是否必须保留 |
| --- | --- | --- |
| `agentwork.db` | SQLite 主库：agent、squad、domain、goal、run、schedule、settings 等 | **是，最高优先级** |
| `agentwork.db-wal`、`agentwork.db-shm` | SQLite WAL 辅助文件；停机备份时一并复制 | 是（随 DB） |
| `runs/repos/<domainID>/` | daemon 为每个 repo 型 domain 维护的裸仓库镜像 | 建议保留 |
| `runs/<runID>/` | daemon 侧一次 run 的工作树 | 否，可重建 |
| `runs/deliver-<goalID>/` | goal 交付阶段的临时工作树 | 否 |
| `runs/scratch/<domainName>/goals/<goalID>/` | scratch domain 的持久化项目目录 | **是**（用户产物） |
| `runs/proc/<runID>/` | processor run 的临时目录 | 否 |
| `runs/sessions/` | ACP 会话临时工作树，进程退出即失效 | 否 |
| `runs/gates/<runID>/` | 校验 / gate 的临时工作树 | 否 |
| `runs/skills/<skillID>/` | 平台技能目录，含 `package.zip` | **是**（否则技能丢失） |
| `digest/` | 每日 AI 知识精选抓取的文章与批次缓存 | 建议保留 |
| `daemon.log` | daemon 日志（lumberjack 10MB 轮转，保留 3 份） | 否 |
| `agents.yaml` | 可选的 agent 探测/覆盖配置 | 是（如果使用） |

说明：

- `runs/repos`、`runs/scratch`、`runs/skills`、`digest` 是 daemon 的持久状态，不只是缓存，升级时必须保留。
- `runs/<runID>`、`runs/proc`、`runs/sessions`、`runs/gates` 可以清理，不影响 agent/团队配置。
- daemon 启动时会在日志里打印 `agentwork: home=... db=...`，可以用来确认它是否真的用了你期望的数据目录。

## 2. 机器端（执行机 / CLI）

远程执行机上的 `agentwork` CLI 也支持 `AGENTWORK_HOME`，但默认用自己的 `~/.agentwork`；它和 daemon 端是**两套独立副本**（例如仓库镜像：daemon 用 `runs/repos`，机器端用 `repos`）。

| 路径 | 内容 | 是否必须保留 |
| --- | --- | --- |
| `agentwork-cli-state.json` | 机器身份、上次连接信息 | 建议保留 |
| `agents.yaml` | 机器端 agent/探测配置 | 是（如果使用） |
| `cli.log` | CLI 日志 | 否 |
| `pending-reports.json` | 未能上报的终态 run 报告 | 建议保留 |
| `agents/<agentID>/AGENTS.md` | 下发给该 agent 的项目说明 | 可重建（服务端有源） |
| `skills/<agentID>/` | 下发给该 agent 的技能包 | 可重建（服务端有源） |
| `chat/<agentID>/` | 按 cwd 隔离的稳定聊天目录 | 建议保留 |
| `bin/agentwork` | 注入到 agent PATH 的 shim | 可重建 |
| `repos/<domainID>/` | 机器端裸仓库镜像 | 可重建（需网络/凭据） |
| `runs/<runID>/` | 机器端 run 工作树 | 否（终态后可清） |
| `runs/proc/<runID>/` | processor 临时目录 | 否 |
| `runs/goal/<goalID>/<agentID>/` | 可写角色的持久 worktree | **建议保留**（未推送改动） |
| `runs/goal/<goalID>/sg/<subGoalID>/<agentID>/` | 子目标的持久 worktree | **建议保留** |
| `runs/scratch/<domainName>/goals/<goalID>/` | 机器端 scratch 项目目录 | **是**（用户产物） |

超过 30 天未触碰的 goal worktree 会被 CLI 的 `pruneStaleGoalWorkdirs` 清理；如果里面有未提交改动，请先提交/推送。

## 3. Web 与构建产物

这些都不包含业务数据，可以随版本整体替换或删除重建：

| 路径 | 内容 |
| --- | --- |
| `web/` | 打包后的 Next.js standalone 服务（`server.js`、`.next/`、`public/`） |
| `web/.next/` | Next.js 构建产物 |
| `dist/` | `package.sh` 产出的发布包与压缩包 |
| `tmp/` | 构建临时目录 |
| `build/` | 旧构建缓存 |
| 仓库根目录 `agentwork-daemon` | 本地 `go build` 产物 |

Web 只通过 `AGENTWORK_SERVER_URL` / Next rewrite 访问 daemon，不在本地保存 agent/团队配置。

## 4. 环境变量

| 变量 | 作用 | 默认 |
| --- | --- | --- |
| `AGENTWORK_HOME` | **全部持久数据根目录** | `~/.agentwork` |
| `AGENTWORK_DB` | 单独覆盖 SQLite 路径 | `$AGENTWORK_HOME/agentwork.db` |
| `AGENTWORK_SERVER_URL` | CLI/机器端访问 daemon 的地址 | `http://127.0.0.1:47813` |
| `AGENTWORK_GOAL_ID` / `AGENTWORK_RUN_ID` / `AGENTWORK_AGENT_ID` | 当前 run 上下文，由 daemon 注入 | 无 |
| `AGENTWORK_TOKEN` | 机器端上报/回调令牌 | 无 |
| `AGENTWORK_GIT_URL` / `AGENTWORK_GIT_CREDENTIALS` / `AGENTWORK_DEFAULT_BRANCH` | 机器端 git 上下文 | 无 |
| `AGENTWORK_ISSUE_REPO` / `AGENTWORK_ISSUE_NUMBER` / `AGENTWORK_SOURCE_KIND` | issue / PR 触发上下文 | 无 |
| `AGENTWORK_COMPILE_VERSION` | 构建版本号注入 | `0.0.1-beta.1` |
| `AGENTWORK_DAEMON_PORT` | 打包期 Next 反代 daemon 的端口（`DAEMON_PORT=... ./package.sh` 时注入） | `47813` |

> 注意：daemon 与机器端 CLI 是两套独立的数据根，可以不同。daemon 的 `AGENTWORK_HOME` 决定服务端数据；每台执行机上的 CLI 默认用自己的 `~/.agentwork`。如果显式设置了 `AGENTWORK_HOME`，要确保每个需要它的进程/执行机都能看到，否则会各自落到默认目录。

## 5. 在 JEM（job-environment）里的映射

当前部署方式下：

| 位置 | 路径 |
| --- | --- |
| 宿主机制品目录 | `/root/runtime/agentwork/`（`bin/`、`web/`） |
| 沙箱 workspace | `/root/job-envs/sandboxes/agentwork-*`，以 `/workspace` 挂进沙箱 |
| 默认数据目录 | `/workspace/.agentwork` |
| JEM 服务 | `http://127.0.0.1:8090` |
| JEM 数据 / 运行时 / 日志 | `/root/job-envs`、`/root/runtime`、`/root/log` |

关键事实：

- `stop` / `start` **保留** workspace；`upgrade` 也**不触碰** workspace。
- `deploy`（重新部署）和 `delete` **会重建/清空** workspace，因此会丢 `/workspace/.agentwork`。
- `POST /envs/{id}/upgrade` 才是 JEM 原生的“换制品、留 workspace”路径。

如果希望连 `deploy` 也不丢数据，需要把数据目录放到宿主机持久路径，并挂进沙箱：

```bash
# 宿主机
mkdir -p /root/agentwork-data

# 两件事都要做：
# 1) JEM 模板 writablePaths 把 /root/agentwork-data 挂进沙箱；
# 2) 启动环境（模板 env 或 start 包装脚本）export 同一个路径。
export AGENTWORK_HOME=/root/agentwork-data
```

只要 `/root/agentwork-data` 是宿主机目录（且没有被 sandbox 重建覆盖），无论 stop/start/upgrade/deploy，DB、runs、skills 都不会丢。

## 6. 迁移已有数据

从旧的默认位置迁移到独立数据目录：

```bash
# 1. 停 daemon（或 JEM stop），确保没有写入
# 2. 复制整个数据根，而不只是 DB
rsync -a --delete ~/.agentwork/ /root/agentwork-data/

# 3. 用新目录启动
export AGENTWORK_HOME=/root/agentwork-data
agentwork-daemon paths          # 确认输出
agentwork-daemon -addr 127.0.0.1:47813
```

只复制 `agentwork.db` 也能恢复 agent/团队/goal/schedule，但会丢技能包、scratch 项目、digest 缓存和 repos 镜像。生产迁移建议整目录复制。

## 7. 不要做的事

- 不要把 `AGENTWORK_HOME` 指到制品目录（`bin/`、`web/` 旁边），否则下一次替换制品会一起删掉。
- 不要用 `deploy` / `delete` 做日常升级；用 `stop → 替换 bin/web → start` 或 JEM `upgrade`。
- 不要在 daemon 还开着的时候只备份 `agentwork.db` 而忽略 `-wal`；要么停机，要么用 `agentwork-daemon backup`（内部 `VACUUM INTO`）。
- 不要把 daemon 的 `runs/repos` 和机器端的 `repos` 当成同一份；它们本来就是两份镜像。