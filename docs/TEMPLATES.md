# 把自己的最佳实践做成模板

AgentWork 已经有完整的模板消费链路：从 Git 仓库读取 `registry.yaml` + YAML 模板，刷新到本地缓存，再在 Web / API 里一键 apply。之前缺的是**反向导出**：把自己已经调好的 agent、团队、项目配置写回模板仓库。

现在新增了 `agentwork-daemon export-template`，可以直接把线上的 agent / squad / project 导出成 registry-compatible YAML。

## 1. 模板系统的现状

| 能力 | 位置 |
| --- | --- |
| 模板仓库 | 默认 `https://gitcode.com/Wing-Jason/agentwork-templates.git`，分支 `main` |
| 覆盖仓库 | `app_settings` 的 `platform.template_repo`，值为 `{"git_url":"...","branch":"main"}` |
| 缓存 | `app_settings` 的 `platform.template_cache` |
| 模板类型 | `agent-template`、`squad-template`、`project-template` |
| 仓库结构 | `registry.yaml`、`agents/*.yaml`、`squads/*.yaml`、`projects/*.yaml`、`skills/<name>/` |
| Web | `/templates` 页面：列表、详情、刷新、apply |
| API | `GET /templates`、`GET /templates/{kind}/{id}`、`POST /templates/refresh`、`POST /templates/{squads|projects|agents}/{id}/apply` |

模板只能表达**定义**，不能带走运行中的 goal 历史、run 记录、聊天记录。这是有意的：模板是可复用的起点，不是数据库快照。

## 2. 从现有配置导出模板

命令在 daemon 二进制里，读取 `AGENTWORK_HOME` 指向的数据库：

```bash
# 先看会用到哪些路径
agentwork-daemon paths

# 导出团队（包含 leader + 所有 agent 成员）
agentwork-daemon export-template \
  -kind squad \
  -id "自动升级作业环境小队" \
  -out /path/to/your/agentwork-templates

# 导出单个 agent
agentwork-daemon export-template \
  -kind agent \
  -id "Verifier" \
  -out /path/to/your/agentwork-templates

# 导出项目（domain + 可选团队 + goals + schedules）
agentwork-daemon export-template \
  -kind project \
  -id "自动升级" \
  -squad "自动升级作业环境小队" \
  -out /path/to/your/agentwork-templates
```

`-id` 可以传数据库 ID，也可以传**精确名称**。项目导出时：

- `-squad` 不传时，会从该 domain 的 goal/schedule 里找唯一的 squad assignee；找到多个会报错，要求显式指定。
- 只导出未结束的 goal（跳过 `done` / `failed` / `cancelled`）。
- 只导出非 built-in 的 schedule（内置的每日精选等跳过）。
- repo 型 domain 会把 `git_url` 写进 `spec.domain.git_url`，apply 时可直接复用；也可用 `-git-url` 覆盖。新建仓库仍用 `spec.repo.create` + `repo_token`。如果原 domain 的 `git_url` 为空，导出会给出 warning。
- 指派给 human 的 goal/schedule 无法写进模板，会跳过并 warning。
- agent 的 `env`、MCP `env`/`headers` 会原样写入模板；键名含 token/secret/password/api_key/credential/authorization 时会 warning。**模板是明文，导出后请人工清理密钥**，不要在模板仓库里存凭证。

常用参数：

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `-kind` | 必填 | `squad` / `agent` / `project` |
| `-id` | 必填 | 线上 ID 或精确名称 |
| `-out` | 必填 | 模板仓库根目录；不存在会自动创建 |
| `-template-id` | 从名称推导 | 覆盖 `metadata.id`，必须是 `^[a-z0-9][a-z0-9-]{1,63}$` |
| `-version` | `0.1.0` | `metadata.version` |
| `-with-skills` | `true` | 把引用的技能文件嵌入 `skills/<name>/`，模板可移植 |
| `-force` | `false` | 覆盖已存在的模板文件 / registry 条目 / 技能文件 |
| `-squad` | 自动 | project 专用：指定团队 |
| `-db` | `$AGENTWORK_HOME/agentwork.db` | 显式指定数据库 |

导出结果示例：

```json
{
  "kind": "squad-template",
  "template_id": "auto-upgrade-squad",
  "name": "自动升级作业环境小队",
  "path": "/path/to/agentwork-templates/squads/auto-upgrade-squad.yaml",
  "registry": "/path/to/agentwork-templates/registry.yaml",
  "skills": ["job-env-artifact-upgrader", "job-env-artifact-verifier"],
  "warnings": [
    "squad instructions contain hard-coded mention://agent/<id> links; re-link them after applying"
  ]
}
```

### 导出后一定要看 warnings

最常见的两个坑：

1. **`mention://agent/<id>` 硬编码 ID**
   模板 apply 会新建 agent，ID 会变；squad instructions / system_prompt 里写死的 mention 不会自动重映射。导出会 warning，apply 后需要在 Web 里重新选择成员，或把 instructions 改成按名称描述、让 leader 自己分派。

2. **技能没有嵌入**
   如果 `-with-skills=false`，或某个技能只存在于远程机器、daemon 本地没有文件，模板只会保留技能名。目标环境必须已有同名技能，否则 apply 时会报技能缺失。要可移植就保持默认 `true`。

## 3. 验证导出的模板

生成的 YAML 在写盘前已经用 apply 路径同一个严格解析器校验过；还可以用仓库自带的 validator 做端到端 apply 验证：

```bash
# 假设模板仓库和 agentwork 仓库是兄弟目录
cd /home/sinkear/developer/agentwork
go run ./cmd/validate-templates
```

validator 会读取 `../agentwork-templates`，校验 registry、逐个解析模板，并在内存库里跑 squad/agent/project 的 apply 用例。

也可以只做结构检查：

```bash
cd /path/to/your/agentwork-templates
git diff
python3 -c 'import yaml,sys; print(yaml.safe_load(open("registry.yaml")))'
```

## 4. 让 AgentWork 使用你的模板仓库

### 方式 A：提交到官方仓库

导出到官方仓库的 clone，review 后提 PR。适合希望复用官方模板的场景，但你的内部 prompt / 团队配置可能不适合公开。

### 方式 B：使用自己的仓库（推荐）

把导出的目录推到自己的 Git 仓库，然后把 `platform.template_repo` 指过去：

```bash
# 先确认数据库路径（取 paths 输出的 database 字段）
DB=$(agentwork-daemon paths | python3 -c 'import json,sys; print(json.load(sys.stdin)["database"])')

python3 - "$DB" <<'PY'
import json, sqlite3, sys, datetime
db = sqlite3.connect(sys.argv[1])
cfg = {"git_url": "https://github.com/you/agentwork-templates.git", "branch": "main"}
db.execute(
    "INSERT INTO app_settings(key,value,updated_at) VALUES(?,?,?) "
    "ON CONFLICT(key) DO UPDATE SET value=excluded.value, updated_at=excluded.updated_at",
    ("platform.template_repo", json.dumps(cfg), datetime.datetime.now().isoformat()),
)
db.commit()
PY
```

然后刷新缓存：

```bash
curl -X POST http://127.0.0.1:${DAEMON_PORT:-47813}/templates/refresh \
  -H 'Content-Type: application/json' \
  -d '{"token":"<私有仓库 token，公开仓库可省略>"}'
```

之后 Web 的 `/templates` 页面就会列出你的模板。私有仓库的 token 只在 refresh 时使用，不会写进模板。

> 目前没有专门的“模板仓库设置”UI；`platform.template_repo` 是 `app_settings` 里的 JSON。上面这段 SQL 是当前最直接的配置方式。

## 5. 一个可复用的工作流模板长什么样

推荐把“最佳实践”拆成三层，而不是一个大而全的模板：

1. **agent 模板**：单个角色的 system_prompt、模型、技能、并发数。适合复用“审查者”“打包者”这类通用角色。
2. **squad 模板**：leader + 成员 + 分工 + 协作说明。适合复用“一个小队怎么协作”。
3. **project 模板**：domain 策略 + team + 初始 goals + schedules。适合复用“一个项目怎么启动”。

例如“制品升级流水线”可以这样组织：

```text
agent:  job-env-artifact-upgrader     # 升级执行者
agent:  job-env-artifact-verifier     # 验收者
squad:  job-env-upgrade-squad         # leader + upgrader + verifier + packager
project: job-env-upgrade              # scratch domain + team + 巡检 schedule
```

然后每次新环境：

```bash
# 通过 Web / API apply project 模板，传入项目名、目标环境等 override
curl -X POST http://127.0.0.1:${DAEMON_PORT:-47813}/templates/projects/job-env-upgrade/apply \
  -H 'Content-Type: application/json' \
  -d '{"name":"自动升级-新环境"}'
```

具体 override 字段以 `internal/service/template_apply.go` 的 `ApplyProjectOverrides` 为准；Web 的模板对话框会给出可填项。

## 6. 模板里不要放什么

- **不要放密钥**：`env` 里如果含 token，会被写进 Git。用目标环境的平台密钥 / 运行时注入。
- **不要放机器相关路径**：`~/.agentwork`、`/root/...` 这类路径在别的机器不成立；用技能和运行时抽象。
- **不要放硬编码 agent ID**：`mention://agent/<uuid>` 在 apply 后会失效；用名称/角色描述。
- **不要试图导出历史**：goal 的 run 记录、聊天、验收证据不属于模板；模板只负责“从什么配置开始”。

## 7. 一句话总结

可以。先用 `agentwork-daemon export-template` 把现有 squad/project 导出成 YAML，review 掉硬编码 ID 和密钥，提交到自己的模板仓库，把 `platform.template_repo` 指过去，之后就能在 `/templates` 里一键复用。