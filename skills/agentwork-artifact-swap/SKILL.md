---
name: agentwork-artifact-swap
description: 在不丢失数据的前提下替换/升级/回滚已部署的 agentwork 制品（bin/ + web/）。适用于 job-environment(JEM) 自定义作业环境和裸机/systemd 部署。触发场景：升级 agentwork、重新部署但不丢 agent/团队/配置、换制品、redeploy、jem swap、打包后端口不符导致页面 500。v2：经真实替换验证——加架构校验、端口双格式校验、数据根改用 agentwork-daemon paths / /proc/<pid>/environ（不再依赖不存在的 /healthz?verbose=1 JSON）、新增 prepare-swap-artifact.sh。
---

# agentwork 制品替换（不丢数据）

把一个**已经在跑**的 agentwork 换成新版本，同时保证 SQLite 里的 agent / squad / goal / schedule、
`runs/` 下的 worktree 与技能包、`agents.yaml` 全部存活。

## 何时用 / 何时不用

用：

- 手上有 `dist/agentwork-v<ver>-linux-<arch>.tar.gz` 或解压后的发布目录，要替换运行中的 `bin/` + `web/`；
- 部署在 job-environment（JEM）里，或裸机 + systemd；
- 上次升级后 agent/团队/配置不见了，要做一次“换制品但保数据”的补救。

不用：

- 首次部署（还没有数据，直接 deploy 更简单）；
- 你准备 `deploy` / 删除环境（那会重建 `/workspace`，数据哪怕在新制品里也救不回来）；
- 需要破坏性数据迁移（schema 不兼容）——先做完整备份另议，别用本流程硬顶。

## 三条核心原则

1. **制品与数据分离**
   - 制品 = `bin/{agentwork,agentwork-daemon}` + `web/`
   - 数据 = `$AGENTWORK_HOME`（JEM host 环境默认 `$HOME/.agentwork`，HOME=沙箱 cwd）：
     `agentwork.db`(+`-wal`/`-shm`)、`runs/`、`digest/`、`daemon.log`、`agents.yaml`
2. **只走 stop → 备份 → 换 bin/web → start**。绝不 `deploy`/`delete`/`destroy`。
3. **先验证架构、端口和数据根，再停机**。停机后才发现“前端内置端口不对”或“home 指向别处”，损失的是可用性。

## 第 0 步：先摸清三件事（只读，不停服）

```bash
# 1) 数据根 + DB（最关键）——注意：/healthz 在 agentwork 里只返回 "ok"，
#    不存在 /healthz?verbose=1 的 {home,db} JSON（旧文档写错了，别被骗）。
#    权威来源是 daemon 的 paths 命令和它自己的进程环境：
agentwork-daemon paths                  # JSON: home / database / runs / repos / digest / log…
DPID=$(ss -tlnp | grep ":<DAEMON_PORT> " | grep -oE 'pid=[0-9]+' | head -1 | cut -d= -f2)
tr '\0' '\n' < /proc/$DPID/environ | grep -E '^(HOME|AGENTWORK_HOME|AGENTWORK_DB)='

# 2) JEM 环境状态（只接受 running / stopped）＋ 环境卡片 cwd（数据根定位）
curl -s http://127.0.0.1:8090/api/v1/envs/<ENV_ID> \
  | python3 -c 'import json,sys;d=json.load(sys.stdin);print(d.get("state"),d.get("cwd",""))'

# 3) 架构 + 端口
uname -m                 # 决定拿 amd64 还是 arm64 包
# web 烧录的 daemon 反代端口（必须与运行环境一致，否则页面 500）：
tar -xOf dist/xxx.tar.gz '*/web/.next/required-server-files.json' 2>/dev/null \
  | grep -oE 'http://(localhost|127\.0\.0\.1):[0-9]+' | sort -u
```

结论决定路线：

| `home` 值 | 含义 | 动作 |
|---|---|---|
| `/workspace/.agentwork` | 数据在沙箱内 | 本流程安全（stop/start 保留 workspace），但 JEM `deploy`/删环境会清空 |
| `/root/agentwork-data` | 宿主机持久目录 | 最理想，直接换 |
| 其他（如 `/root/.agentwork`） | 自定义 | `--data-home` 指到它 |

## 第 1 步：准备制品（两个 mismatch 停机前必须解决）

**① 架构**：包名/二进制架构必须 == 宿主机架构。给你 amd64 的包 ≠ 能用在 arm64 宿主上
（Go 二进制跨架构跑不起来）。**② 端口**：Next 的反代端口在**构建时**固化进 `web/.next/`，
包默认烧 `localhost:47813`，JEM 环境 daemon 通常在 17450/175xx —— 端口不一致页面全 500。

两个选择：

```bash
# A. 包本身兼容（架构对、端口对）→ 直接用
cd /path/to/agentwork
DAEMON_PORT=<DAEMON_PORT> AGENTWORK_COMPILE_VERSION=<新版本> ./package.sh
# → dist/agentwork-v<ver>-linux-<arch>.tar.gz（package.sh 默认 ARCHS="amd64 arm64"，按需指定）

# B. 包不兼容（架构错/端口错）→ 用本 skill 的 prepare-swap-artifact.sh 本地重打，
#    从源码构建“本机架构 + 目标 daemon 端口”的兼容制品（JEM 环境推荐）
bash <SKILL_DIR>/scripts/prepare-swap-artifact.sh \
    --daemon-port <DAEMON_PORT> --out /tmp/aw-swap-artifact --version 0.0.1-beta.1
```

## 第 2 步（一次性）：把数据根挪到宿主机持久目录

只有当 `home` 还是 `/workspace/.agentwork`、且你希望连 `deploy` 也不丢时才需要。做完一次，之后都是普通替换。

```bash
# 停（保留 workspace）
curl -X POST http://127.0.0.1:8090/api/v1/envs/<ENV_ID>/stop
until [ "$(curl -s http://127.0.0.1:8090/api/v1/envs/<ENV_ID> \
  | python3 -c 'import json,sys;print(json.load(sys.stdin)["state"])')" = stopped ]; do sleep 2; done

# 搬数据
SANDBOX=$(curl -s http://127.0.0.1:8090/api/v1/envs/<ENV_ID> | python3 -c 'import json,sys;print(json.load(sys.stdin)["cwd"])')
mkdir -p /root/agentwork-data
cp -a "$SANDBOX/.agentwork/." /root/agentwork-data/

# JEM 模板里必须同时做两件事（缺一不可）：
#   writablePaths 增加宿主机 /root/agentwork-data
#   env 增加 AGENTWORK_HOME=/root/agentwork-data

# 用【旧】制品启动，确认真的读新目录
curl -X POST http://127.0.0.1:8090/api/v1/envs/<ENV_ID>/start && sleep 5
DPID=$(ss -tlnp | grep ":<DAEMON_PORT> " | grep -oE 'pid=[0-9]+' | head -1 | cut -d= -f2)
tr '\0' '\n' < /proc/$DPID/environ | grep AGENTWORK_HOME   # 必须是 /root/agentwork-data
```

`--data-home` 只是告诉脚本“备份/校验哪个目录”，**不会**改变沙箱内 daemon 的 `AGENTWORK_HOME`；后者只能由 JEM 模板 env 提供。

## 第 3 步：执行替换

### JEM 环境（推荐，用脚本）

脚本在本 skill 的 `scripts/jem-swap-release.sh`（与 agentwork-env-builder skill 维护同一份）。
目标主机没有仓库时，把 skill 目录或脚本拷过去即可。

```bash
JEM_URL=http://127.0.0.1:8090 \
./scripts/jem-swap-release.sh \
  --release /root/agentwork-release/agentwork-v<ver>-linux-<arch>.tar.gz \
  --env-id <ENV_ID> \
  --runtime-dir /root/runtime/<ENV_ID> \
  --data-home /root/agentwork-data \
  --daemon-port <DAEMON_PORT> \
  --backup-dir /root/agentwork-backups
```

脚本顺序（这就是“不丢数据”的依据，全部校验都在停机前完成）：

1. **架构校验**：release 二进制架构 vs `uname -m`，不匹配直接退出并给重建命令（不会再白停一次服）；
2. 校验包结构 + 前端固化端口 == `--daemon-port`（localhost 与 127.0.0.1 两种格式都认，不符 → 停机前退出）；
3. 版本降级警告（新戳 < 旧戳只警告不阻断——代码可能更新但戳没 bump）；
4. 读 JEM 状态，只接受 running/stopped；
5. 定位 DB：`--data-home`/`--db` → **运行中 daemon 的 /proc/<pid>/environ**（HOME/AGENTWORK_HOME/AGENTWORK_DB）→ 环境卡片 cwd → sandbox glob 兜底；
6. **stop** 轮询到 `stopped`（workspace 保留）；
7. 备份 `agentwork.db`+`-wal`+`-shm`，跑 `PRAGMA integrity_check`，打包 `runs/skills`；
8. 备份旧 `bin/`、`web/`（临时回滚目录 + 备份目录的 `previous-bin`/`previous-web`）；
9. **只**替换 `$RUNTIME_DIR/bin/{agentwork,agentwork-daemon}` 与 `$RUNTIME_DIR/web`；
10. **start** 轮询到 `running`；
11. 校验**新 daemon 进程 environ 推导的 db == 备份的 DB**（读真实的进程环境，不用脚本自己的 HOME 跑 paths——那会拿到平台 7373 实例的目录）、版本、`/domains`、DB 计数；
12. 起不来 → 自动还原旧 bin/web 并重新 start。

成功输出 `OK — artifact swapped, data preserved` 与 `goals=/agents=/skills=` 计数。

### 裸机 / systemd（手工等价）

```bash
systemctl stop agentwork
BK=/var/backups/agentwork/$(date +%Y%m%d-%H%M%S); mkdir -p "$BK"
cp -a "$AGENTWORK_HOME"/agentwork.db{,-wal,-shm} "$BK/" 2>/dev/null
tar -czf "$BK/runs-skills.tar.gz" -C "$AGENTWORK_HOME" runs/skills 2>/dev/null
cp -a /opt/agentwork/bin "$BK/previous-bin"
cp -a /opt/agentwork/web "$BK/previous-web"

tar -xzf new.tar.gz -C /tmp/awnew
rsync -a --delete /tmp/awnew/agentwork-v*-linux-<arch>/bin/ /opt/agentwork/bin/
rsync -a --delete /tmp/awnew/agentwork-v*-linux-<arch>/web/ /opt/agentwork/web/

systemctl start agentwork
/opt/agentwork/bin/agentwork-daemon paths    # 确认 home/db 与备份一致
```

## 第 4 步：验收

- `agentwork-daemon paths`：`home`、`database` 与替换前一致（或直接读新 daemon 进程 environ）；
- `GET /domains` 返回原有数量（不是 `[]`）；
- Web 里 agent、团队、goal 看板、schedule、技能都在；
- `agentwork-daemon version` 是新版本（若新戳 < 旧戳只是警告，看功能面判断新旧）；
- 机器执行器 `GET /machines` 里本机 machine 是 connected；
- 备份目录有 `agentwork.db` + `*.manifest.json` + `previous-bin/` + `previous-web/`。

## 第 5 步：回滚

制品回滚（数据没动过，只需换回旧 bin/web）：

```bash
curl -X POST "$JEM_URL/api/v1/envs/<ENV_ID>/stop"
rsync -a --delete "$BK/previous-bin/" "$RUNTIME_DIR/bin/"
rsync -a --delete "$BK/previous-web/" "$RUNTIME_DIR/web/"
curl -X POST "$JEM_URL/api/v1/envs/<ENV_ID>/start"
```

数据回滚（只有确认 DB 被误改/误删才做）：

```bash
# 停服后
rm -f "$AGENTWORK_HOME"/agentwork.db-wal "$AGENTWORK_HOME"/agentwork.db-shm
cp /root/agentwork-backups/agentwork-<ts>/agentwork.db "$AGENTWORK_HOME/agentwork.db"
chmod 600 "$AGENTWORK_HOME/agentwork.db"
tar -xzf /root/agentwork-backups/agentwork-<ts>/runs-skills.tar.gz -C "$AGENTWORK_HOME"
```

## 失败对照表

| 现象 | 原因 | 处理 |
|---|---|---|
| `architecture mismatch: release is amd64 but this host is arm64` | 拿错架构包（v2 新增闸门） | 本机重打 `ARCHS=$(uname -m)` 或用 prepare-swap-artifact.sh |
| `release web proxies :47813 but --daemon-port=17450` | 包按错误端口构建 | `DAEMON_PORT=<目标> ./package.sh` 重打，或用 prepare-swap-artifact.sh |
| Web 打开、接口全 500 | 同上，但已经被硬换上去 | 回滚制品，重新打包 |
| `cannot locate agentwork.db` | 数据根不对 / 环境已迁走 | 传 `--data-home` 或 `--db`；先看 `agentwork-daemon paths` 或进程 environ |
| `daemon used db=X, expected Y` | JEM 模板没设 `AGENTWORK_HOME` | 模板加 env 后重跑（脚本会把环境停掉，不会半吊子运行） |
| `env state error/undeployed` | 环境本身坏了 | 先手工 start 修好，或从备份恢复，再换 |
| 换完 agent/团队没了 | 换了 DB（或 home 变了导致空库） | 停服，按“数据回滚”放回快照，复查 `paths` 的 database |
| healthz 返回 ok 但不知道数据在哪 | 别用 healthz 查数据 | `agentwork-daemon paths` / 进程 environ |

## 红线

1. **不 deploy、不 delete、不 destroy**：会重建 `/workspace`，数据目录还在里面就没了。只有 `stop`/`start`/`upgrade` 保数据。
2. **不动平台自带的 7373 daemon**，只操作本环境的 runtime。
3. **不并行跑两次替换**；先确认前一次已 `running` 且计数一致。
4. **不要在数据根没迁走时改 `-db` 参数**：数据根统一由 `AGENTWORK_HOME` 决定，否则会造出第二个空库。
5. **备份目录不要放在会被 deploy 清掉的位置**（`/root/agentwork-backups` 优于 `/workspace/...`）。

## 参考

- 本 skill：`references/paths-quickref.md`（数据根与路径速查）
- 仓库文档：`docs/UPGRADE.md`、`docs/PATHS.md`（相对 agentwork 仓库根）
- 路径自述：`agentwork-daemon paths`（JSON：home/database/runs/repos/digest/log…）
- 一致性备份：`agentwork-daemon backup -out <dir> [-with-files]`

## 版本历史

| 版本 | 日期 | 主要变更 |
|---|---|---|
| v2 | 2026-11-04 | 依据真实替换实战修正：新增架构校验（amd64 包在 arm64 宿主上停机前拒绝）、前端反代端口双格式校验（localhost/127.0.0.1）并给重建指引、数据根改用 `agentwork-daemon paths` 与 `/proc/<pid>/environ`（**修正错误的 `/healthz?verbose=1` JSON 接口依赖**——新旧 daemon 都只返回 ok）、DB 定位优先运行进程 environ 与环境卡片 cwd、版本降级警告、新增 `scripts/prepare-swap-artifact.sh`（构建本机架构+目标端口兼容制品）；失败对照表同步更新 |
| v1 | 2026-11-04 | 初版（草稿）：healthz?verbose=1 探测、sandbox glob 定位 DB、无架构校验 |