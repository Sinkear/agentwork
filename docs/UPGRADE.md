# 保留数据的升级 / 替换制品

目标：**agent、团队、goal、schedule、技能、scratch 项目全部保留**，只把 `bin/` 和 `web/` 换成新版本；失败可以回滚。

核心做法只有一句话：

> 数据目录 `AGENTWORK_HOME` 独立于制品目录；升级时先停、备份、只换 `bin/` 和 `web/`、再启动，绝不 `deploy` / `delete`。

## 1. 升级前检查

```bash
# 确认数据实际落在哪
agentwork-daemon paths

# 记录当前版本
agentwork-daemon version

# 记录当前数据量（可选）
agentwork-daemon backup -db "$AGENTWORK_HOME/agentwork.db" -out /tmp/aw-backup-check
```

如果 `paths` 输出的 `home` 不是你预期的目录，先修正 `AGENTWORK_HOME`，不要急着升级。

## 2. 先按目标端口构建发布包

Next 的 rewrite 会把 daemon 端口固化进 `web/` 构建产物，运行时改 `DAEMON_PORT` 不会改变反代目标。默认端口是 47813；如果环境用别的端口（例如 17450），先这样打包：

```bash
DAEMON_PORT=17450 AGENTWORK_COMPILE_VERSION=0.0.1-beta.3 ./package.sh
```

`start-all.sh` 的默认 daemon 端口也会被写成 17450，`jem-swap-release.sh` 会校验发布包内的端口与 `--daemon-port` 一致，不一致会在停机前拒绝。

## 3. 方式 A：JEM（job-environment）环境

推荐使用仓库里的脚本：

```bash
cd /home/sinkear/developer/agentwork

./scripts/jem-swap-release.sh \
  --release dist/agentwork-v0.0.1-beta.2-linux-amd64.tar.gz \
  --env-id agentwork \
  --runtime-dir /root/runtime/agentwork \
  --data-home /root/agentwork-data \
  --daemon-port 17450
```

> 前端反代 daemon 的端口在打包时固化。若目标端口不是默认的 47813，必须先用
> `DAEMON_PORT=17450 ./package.sh` 打包；脚本会在停机前校验发布包里的端口，
> 不一致直接拒绝，避免停服后才发现页面 500。

脚本按以下顺序执行：

1. 校验发布包里有 `bin/agentwork`、`bin/agentwork-daemon`、`web/server.js`；
2. 查询 JEM 环境状态（只接受 `running` / `stopped`）；
3. 定位数据库（`--data-home` / `--db`，或从运行中 daemon 的 `/healthz?verbose=1`，最后才是 sandbox 自动发现）；
4. **stop** 并轮询到 `stopped`；
5. 备份 `agentwork.db` + `-wal` + `-shm`，做 `PRAGMA integrity_check`，并打包 `runs/skills`；
6. 备份旧 `bin/`、`web/` 到临时回滚目录；
7. 只替换 `$RUNTIME_DIR/bin/{agentwork,agentwork-daemon}` 和 `$RUNTIME_DIR/web`；
8. **start** 并轮询到 `running`；
9. 校验版本、HTTP、DB 计数；
10. 启动失败时自动恢复旧 `bin/` / `web/` 并重新 start。

默认备份目录是 `/root/workspace/agentwork-backups`，可用 `--backup-dir` 修改。数据库本身不会被脚本修改，脚本只读它来做完整性检查。

> 如果环境本来就是 `stopped`，脚本替换后保持 `stopped`，不会擅自启动。

### 数据目录放在哪里才不会丢

| 数据目录 | stop/start | upgrade | deploy/delete |
| --- | --- | --- | --- |
| `/workspace/.agentwork`（默认） | 保留 | 保留 | **丢失**（workspace 重建） |
| `/root/agentwork-data`（宿主机持久目录） | 保留 | 保留 | 保留（只要该目录不被删） |

推荐：

```bash
mkdir -p /root/agentwork-data
# 让沙箱里的 AGENTWORK_HOME 指向挂载进来的宿主机目录：
# 1) JEM 模板 writablePaths 加入 /root/agentwork-data；
# 2) JEM 模板/启动环境 env 加入 AGENTWORK_HOME=/root/agentwork-data。
```

**只给脚本传 `--data-home` 不够。** 它只告诉脚本“备份/校验哪个目录”，不会改变
沙箱里 `start-all.sh` 看到的 `AGENTWORK_HOME`。如果 JEM 启动环境没有这个变量，
daemon 仍会使用 `/workspace/.agentwork`（`start-all.sh` 的默认值），脚本会在
启动后通过 `/healthz?verbose=1` 发现 home 不一致并停止环境、报错退出。

已有环境迁移到宿主机目录的顺序：

```bash
# 1. 先停环境
curl -s -X POST http://127.0.0.1:8090/api/v1/envs/agentwork/stop
# 2. 找到当前 sandbox 的 workspace（或直接用 daemon 打印的 home）
ls -d /root/job-envs/sandboxes/agentwork-*/.agentwork
# 3. 复制到宿主机持久目录（保留权限）
cp -a /root/job-envs/sandboxes/agentwork-*/.agentwork /root/agentwork-data
# 4. 修改 JEM 模板：挂载 /root/agentwork-data，并设置
#    AGENTWORK_HOME=/root/agentwork-data
# 5. 先用旧制品 start，确认：
curl -s http://127.0.0.1:17450/healthz?verbose=1
#    返回的 home 必须是 /root/agentwork-data
# 6. 再执行本页第 2 节的换制品脚本
```

这样即使以后误用 `deploy`，数据也还在宿主机上；重新部署后把 `AGENTWORK_HOME` 指回去即可恢复。

### 不要用 JEM 的 deploy/delete 做日常升级

- `deploy`：重新展开制品并重建 sandbox workspace，会清掉 `/workspace`；
- `delete`：连 sandbox 一起删；
- `stop → 替换 bin/web → start`：保留 workspace；
- JEM 原生 `POST /envs/{id}/upgrade`：保留 workspace，但需要符合 JEM 的制品包格式。

日常发版用本脚本或 JEM `upgrade`，不要用 `deploy`。

## 4. 方式 B：裸机 / systemd

假设：

```bash
APP=/opt/agentwork
DATA=/var/lib/agentwork
export AGENTWORK_HOME=$DATA
```

步骤：

```bash
# 0. 新版本解压到旁路目录，不覆盖正在运行的目录
sudo tar -xzf agentwork-v0.0.1-beta.2-linux-amd64.tar.gz -C /opt
sudo mv /opt/agentwork-v0.0.1-beta.2-linux-amd64 /opt/agentwork-next

# 1. 停服务（systemd 或 start-all.sh 的进程）
sudo systemctl stop agentwork        # 如果有 unit
# 或：kill $(cat /run/agentwork.pid)

# 2. 备份
sudo -u agentwork env AGENTWORK_HOME=$DATA \
  /opt/agentwork/bin/agentwork-daemon backup -out /var/backups/agentwork --with-files

# 3. 只替换 bin/ 和 web/；数据目录完全不动
sudo rsync -a --delete /opt/agentwork-next/bin/ /opt/agentwork/bin/
sudo rsync -a --delete /opt/agentwork-next/web/ /opt/agentwork/web/

# 4. 启动
sudo systemctl start agentwork
curl -sf http://127.0.0.1:47813/domains >/dev/null && echo OK
```

如果第 4 步失败，回滚：

```bash
sudo systemctl stop agentwork
sudo rsync -a --delete /var/backups/agentwork/previous-bin/ /opt/agentwork/bin/
sudo rsync -a --delete /var/backups/agentwork/previous-web/ /opt/agentwork/web/
sudo systemctl start agentwork
```

数据目录没有被动过，所以回滚制品即可，不需要回滚数据库。

## 5. 备份与恢复

### 备份

```bash
# 停机备份（最稳）
agentwork-daemon backup \
  -db "$AGENTWORK_HOME/agentwork.db" \
  -out /backups/agentwork \
  -with-files
```

产物：

- `<timestamp>/agentwork.db`：`VACUUM INTO` 出来的一致性快照；
- `<timestamp>/agentwork.db.manifest.json`：时间、大小、各表计数；
- `<timestamp>/agentwork-files.tar.gz`：`runs/skills` + `agents.yaml`（仅在 `-with-files` 时）。

`agentwork-daemon backup` 可以在 daemon 运行时执行，不要求停机；它用 SQLite 的 `VACUUM INTO` 生成一致快照。

### 恢复

```bash
# 1. 停 daemon / JEM stop
# 2. 清掉旧 WAL
rm -f "$AGENTWORK_HOME/agentwork.db-wal" "$AGENTWORK_HOME/agentwork.db-shm"
# 3. 放回快照
cp /backups/agentwork/<timestamp>/agentwork.db "$AGENTWORK_HOME/agentwork.db"
chmod 600 "$AGENTWORK_HOME/agentwork.db"
# 4. 如需技能
tar -xzf /backups/agentwork/<timestamp>/agentwork-files.tar.gz -C "$AGENTWORK_HOME"
# 5. 启动并核对
agentwork-daemon paths
agentwork-daemon -addr 127.0.0.1:47813
```

恢复后打开 Web 检查：Agent 数量、团队、goal 看板、schedule、技能是否都在。

## 6. 升级后的自检清单

```bash
# 版本
agentwork-daemon version

# 数据路径
agentwork-daemon paths

# 数据计数（JSON）
agentwork-daemon backup -db "$AGENTWORK_HOME/agentwork.db" -out /tmp/aw-verify

# HTTP
curl -sf http://127.0.0.1:47813/agents | head
```

Web 端至少检查：

- Agent 列表数量与升级前一致；
- 团队（squad）成员和 leader 正常；
- goal 看板没有变空；
- schedule 仍存在且时间正确；
- 技能包仍可下载 / 下发；
- 新建一个测试 goal 能正常入队（确认 daemon 不是只读恢复）。

## 7. 常见故障

| 现象 | 原因 | 处理 |
| --- | --- | --- |
| 升级后 agent/团队全空 | daemon 用了另一个 `AGENTWORK_HOME`，或用了新 sandbox 的 `/workspace` | `agentwork-daemon paths` 对比；把旧 DB 所在目录指回去 |
| DB 打开报 `database is locked` | 旧 daemon 未完全退出 / WAL 残留 | 确认进程退出；停掉后清 `-wal`/`-shm` 再启动 |
| 升级后技能丢失 | 只备份了 DB，没备份 `runs/skills` | 从备份恢复 `runs/skills`，或在 Web 重新上传 |
| 页面能开但接口 502 | Web 的 rewrite 指向了旧 daemon 端口 | 重新构建 web 或检查 `web/next.config.ts` / 启动端口 |
| JEM start 失败 | 新制品缺少 `web/server.js` 或端口冲突 | 看 `/root/log/agent-env.log`；脚本会自动回滚旧 bin/web |