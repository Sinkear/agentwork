# agentwork 数据根与路径速查

替换制品时唯一需要关心的边界：**什么属于“数据”，什么属于“制品”**。

## 制品（可以整块换掉）

```text
<release>/
├── bin/agentwork                 CLI
├── bin/agentwork-daemon          daemon
├── web/server.js                 Next standalone
├── web/.next/                    ★ 反代端口在这里固化
├── web/public/
├── start-all.sh                  ★ 默认 AGENTWORK_HOME / DAEMON_PORT 在这里
└── docs/                         PATHS / UPGRADE / TEMPLATES
```

关键点：`web/.next/required-server-files.json` 里写死了 `http://localhost:<port>`。
端口在 `next build` 时注入（`AGENTWORK_DAEMON_PORT`），所以**包只能用于构建时的那个端口**。

## 数据（一个字节都不要动）

```text
$AGENTWORK_HOME/                默认 ~/.agentwork，可用 AGENTWORK_HOME 覆盖
├── agentwork.db + -wal + -shm  SQLite 主数据（agent/squad/goal/schedule/domain/skill/settings）
├── runs/
│   ├── repos/<domainID>/       daemon 侧 bare repo
│   ├── <runID>/                运行 worktree
│   ├── deliver-<goalID>/       goal 交付 worktree
│   ├── scratch/<domain>/goals/<goalID>/
│   ├── proc/<runID>/           进程工作目录
│   ├── sessions/
│   ├── gates/<runID>/          验收门禁产物
│   └── skills/<skillID>/package.zip   ★ 技能包（备份时单独打包）
├── digest/                     AI 知识精选（articles.json、batches）
├── daemon.log
└── agents.yaml
```

`AGENTWORK_DB` 可单独指定 SQLite 路径，优先于 `$AGENTWORK_HOME/agentwork.db`。
两者不一致时，以 `agentwork-daemon paths` 输出的 `database` 为准判断“到底读写哪个库”
（`/healthz` 只返回 "ok"，没有 home/db 字段）。

## 机器侧 CLI（独立，不必和 daemon 同一目录）

```text
~/.agentwork/                     CLI 自己的根，可用 AGENTWORK_HOME 覆盖
├── agentwork-cli-state.json
├── cli.log
├── pending-reports.json
├── agents/<agentID>/AGENTS.md
├── skills/<agentID>/
├── chat/<agentID>/
├── bin/agentwork                 注入到 PATH 的 shim
├── repos/<domainID>/             机器侧 bare repo
└── runs/<runID>/、runs/proc/<runID>/、runs/goal/<goalID>[/sg/<subGoalID>]/<agentID>/
```

## 环境变量

| 变量 | 作用 | 默认 |
|---|---|---|
| `AGENTWORK_HOME` | 数据根 | `~/.agentwork` |
| `AGENTWORK_DB` | SQLite 路径（优先于 HOME） | `$AGENTWORK_HOME/agentwork.db` |
| `AGENTWORK_DAEMON_PORT` | **构建期**注入前端反代端口 | `47813` |
| `DAEMON_PORT` | 打包/脚本用的 daemon 端口 | `47813` |
| `AGENTWORK_SERVER_URL` | CLI 访问 daemon 的地址 | `http://127.0.0.1:47813` |
| `AGENTWORK_COMPILE_VERSION` | 打包版本戳 | `0.0.1-beta.1` |

## JEM（job-environment）里的对应关系

| agentwork | JEM |
|---|---|
| `<release>/bin`、`<release>/web` | `/root/runtime/<ENV_ID>/{bin,web}` |
| `$AGENTWORK_HOME` | 沙箱 `/workspace/.agentwork`（默认，deploy 会清）或宿主机持久目录 `/root/agentwork-data`（推荐） |
| 备份 | `/root/agentwork-backups/agentwork-<ts>/` |
| 状态查询 | `GET /api/v1/envs/<ENV_ID>` → `.state` ∈ running/stopped/error/undeployed |
| stop/start | `POST /api/v1/envs/<ENV_ID>/{stop,start}`（workspace 保留） |
| 会清 workspace | `deploy`、删除环境（**不要用**） |

## 自检命令

```bash
agentwork-daemon paths                 # 当前 home/db/runs/... 全量 JSON（数据根唯一权威来源）
agentwork-daemon version
curl -s http://127.0.0.1:<port>/healthz    # 只返回 "ok"——没有 home/db JSON，别指望它
agentwork-daemon backup -out /root/agentwork-backups        # 一致性快照
agentwork-daemon backup -out /root/agentwork-backups -with-files
```