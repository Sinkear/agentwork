#!/bin/sh
# 产出"解压即跑"的完整部署包：Go 二进制（CLI + daemon）+ 前端自包含服务。
#
#   ./package.sh                              # 默认 linux amd64 + arm64
#   ARCHS="amd64" ./package.sh                # 只打 x86
#   ARCHS="arm64 arm" ./package.sh            # 自定义架构组合
#   OS=darwin ARCHS=arm64 ./package.sh        # macOS Apple Silicon
#   AGENTWORK_COMPILE_VERSION=0.0.1-beta.2 ./package.sh
#
# 每个架构产出独立压缩包（dist/agentwork-v$VERSION-linux-<arch>.tar.gz）：
#   bin/agentwork          CLI
#   bin/agentwork-daemon   daemon（API :47813）
#   web/server.js          前端自包含服务（:47814，无需 node_modules / 源码）
#   web/.next/static       前端静态资源
#   web/public             前端公共资源
#   start-all.sh           一键启动 daemon + 前端
#   README.deploy.md       部署说明
#   docs/                  路径 / 升级 / 模板文档
#
# 前端与架构无关（无 next/image → 不依赖 sharp），各架构包共用同一份产物，
# 用硬链接复制，不额外占磁盘。
#
# 依赖：Go ≥ 1.21（交叉编译零配置）、Node ≥ 20（运行前端 standalone）、curl（启动健康检查）。
set -e
cd "$(dirname "$0")"

# Go 构建缓存放仓库内：脚本自包含，也在受限文件沙箱（只允许写工作区）下可用。
export GOCACHE="${GOCACHE:-$PWD/tmp/go-cache}"

VERSION="${AGENTWORK_COMPILE_VERSION:-0.0.1-beta.1}"
DAEMON_PORT="${DAEMON_PORT:-47813}"
ARCHS="${ARCHS:-amd64 arm64}"
OS="${OS:-linux}"
LDFLAGS="-X main.cliVersion=$VERSION -X github.com/eushing/agentwork/internal/daemon.DaemonVersion=$VERSION"

echo "── 1/4 构建前端（standalone，架构无关；daemon=:$DAEMON_PORT）──"
(cd web && AGENTWORK_DAEMON_PORT="$DAEMON_PORT" npx next build)

# 组装前端树：standalone 本体 + Next 约定需手动放入的 static/public。
# 先收进一个共享暂存目录，各架构包用硬链接复制。
WEB_STAGE="tmp/package-web"
rm -rf "$WEB_STAGE"
mkdir -p "$WEB_STAGE/.next"
# 注意用 source/. ：目录已存在时 `cp -r src dest` 会把 src 整个嵌进 dest。
cp -r web/.next/standalone/. "$WEB_STAGE"
cp -r web/.next/static "$WEB_STAGE/.next/static"
cp -r web/public "$WEB_STAGE/public"

echo "── 2/4 交叉编译 Go 二进制 ──"
FAILED=""
for arch in $ARCHS; do
  echo "   $OS/$arch …"
  if ! CGO_ENABLED=0 GOOS="$OS" GOARCH="$arch" go build -trimpath -ldflags "$LDFLAGS" \
        -o "tmp/pkg-$arch-agentwork" ./cmd/agentwork-cli; then
    FAILED="$FAILED $arch"; continue
  fi
  CGO_ENABLED=0 GOOS="$OS" GOARCH="$arch" go build -trimpath -ldflags "$LDFLAGS" \
      -o "tmp/pkg-$arch-daemon" ./cmd/agentwork-daemon || FAILED="$FAILED $arch"
done
if [ -n "$FAILED" ]; then
  echo "✗ 以下架构编译失败:$FAILED" >&2
  exit 1
fi

echo "── 3/4 组装各架构包 ──"
for arch in $ARCHS; do
  REL="dist/agentwork-v$VERSION-$OS-$arch"
  rm -rf "$REL"
  mkdir -p "$REL/bin"
  mv "tmp/pkg-$arch-agentwork" "$REL/bin/agentwork"
  mv "tmp/pkg-$arch-daemon" "$REL/bin/agentwork-daemon"
  # 前端树用硬链接复制（同磁盘秒级，不重复占空间）
  cp -al "$WEB_STAGE" "$REL/web" 2>/dev/null || cp -r "$WEB_STAGE" "$REL/web"
done

echo "── 4/4 写启动脚本、说明并打包 ──"
for arch in $ARCHS; do
  REL="dist/agentwork-v$VERSION-$OS-$arch"

  cat > "$REL/start-all.sh" <<'EOF'
#!/bin/sh
# 一键启动：daemon(:${DAEMON_PORT:-47813}) + 前端(:${WEB_PORT:-47814})。
#   ./start-all.sh            前台运行（两个进程）
#   WEB_PORT=47814 HOST=0.0.0.0 ./start-all.sh
#
# 数据目录（agents / squads / goals / skills / runs）默认在
# $AGENTWORK_HOME（缺省 ~/.agentwork），可用 AGENTWORK_HOME 或 AGENTWORK_DB
# 指向一个制品之外、持久可写的位置；升级只换 bin/web，不碰这里。
set -e
cd "$(dirname "$0")"
HOST="${HOST:-127.0.0.1}"
WEB_PORT="${WEB_PORT:-47814}"
DAEMON_PORT="${DAEMON_PORT:-47813}"
AGENTWORK_HOME="${AGENTWORK_HOME:-$HOME/.agentwork}"
export AGENTWORK_HOME
DB="${AGENTWORK_DB:-$AGENTWORK_HOME/agentwork.db}"
mkdir -p "$AGENTWORK_HOME"
# 前端反代目标是构建期固化的 47813：改了 daemon 端口必须重新打包，
# 否则页面能打开、/backend 全 500。
if [ "$DAEMON_PORT" != "47813" ]; then
  echo "⚠ DAEMON_PORT=$DAEMON_PORT 与前端内置反代目标(47813)不一致——" >&2
  echo "  API 调用会失败。换端口请改 web/next.config.ts 后重新打包。" >&2
fi
echo "agentwork: home=$AGENTWORK_HOME db=$DB"

./bin/agentwork-daemon -addr "127.0.0.1:$DAEMON_PORT" -db "$DB" &
DAEMON_PID=$!
trap 'kill $DAEMON_PID 2>/dev/null' EXIT INT TERM

# 先确认 daemon 真的起来了：端口被占用时它会立刻退出，不能继续把 web
# 指向一个旧的/别人的 daemon。
READY=0
i=0
while [ "$i" -lt 50 ]; do
  if ! kill -0 "$DAEMON_PID" 2>/dev/null; then
    echo "✗ daemon 启动失败：127.0.0.1:$DAEMON_PORT 可能被占用" >&2
    exit 1
  fi
  if curl -sf "http://127.0.0.1:$DAEMON_PORT/healthz" >/dev/null 2>&1; then
    READY=1
    break
  fi
  i=$((i + 1))
  sleep 0.2
done
if [ "$READY" != 1 ]; then
  echo "✗ daemon 未在 10s 内就绪" >&2
  kill "$DAEMON_PID" 2>/dev/null || true
  exit 1
fi

cd web
HOSTNAME="$HOST" PORT="$WEB_PORT" node server.js
EOF
  chmod +x "$REL/start-all.sh"
  # start-all.sh 是 quoted heredoc（避免 $ 被展开）；端口占位后统一替换。
  awk -v p="$DAEMON_PORT" '{gsub(/47813/, p); print}' "$REL/start-all.sh" > "$REL/start-all.sh.tmp"
  mv "$REL/start-all.sh.tmp" "$REL/start-all.sh"
  chmod +x "$REL/start-all.sh"

  cat > "$REL/README.deploy.md" <<EOF
# agentwork v$VERSION 部署包（$OS/$arch）
EOF
  cat >> "$REL/README.deploy.md" <<'EOF'

## 运行要求
  - Node ≥ 20（仅前端 standalone 服务需要；Go 二进制零依赖、静态链接）

## 快速开始
    ./start-all.sh
    # 打开 http://127.0.0.1:47814

## 端口
- 47814：Web 前端（/backend/* 与 /ws 由它反代到 daemon，浏览器无需直连 daemon 端口）
- daemon 端口：见 `start-all.sh` 的 `DAEMON_PORT`（本包在构建时固化）

## 常用变量
- WEB_PORT（默认 47814）、HOST（默认 127.0.0.1；对外暴露设 0.0.0.0）
- DAEMON_PORT（默认见 start-all.sh；本包的反代目标在打包时固化，换端口必须重新打包）
- AGENTWORK_HOME（默认 ~/.agentwork）：**所有持久数据（SQLite、runs、skills）的根目录**
- AGENTWORK_DB：单独指定 SQLite 路径（优先于 AGENTWORK_HOME/agentwork.db）

## 数据与升级（重要）
制品（bin/、web/、start-all.sh）与数据（AGENTWORK_HOME）是分开的。升级时**只替换 bin/ 和 web/**，
不要删除数据目录：
1. 停掉旧 daemon（不要 delete/重新 deploy 沙箱，否则 /workspace 会被清空）；
2. 可选：`./bin/agentwork-daemon backup -out /path/to/backups` 做一份一致性备份；
3. 用新包覆盖 bin/ 和 web/（或解压新包到新目录，把 `AGENTWORK_HOME` 指到同一数据目录）；
4. 用同一个 `AGENTWORK_HOME` 启动 `start-all.sh`；
5. 打开 Web 确认 agents/teams/goals 仍在。

> 在 job-environment 这类沙箱里，`~/.agentwork` 位于 `/workspace`；`stop → 替换 → start` 会保留它，
> 但 `delete / deploy` 会清空 `/workspace`。要连 deploy 也不丢，需要把 `AGENTWORK_HOME` 指到宿主机
> 持久目录（通过模板 writablePaths 挂进沙箱），例如 `/root/agentwork-data`。

## 备份 / 恢复
    ./bin/agentwork-daemon backup -out ./backups
    ./bin/agentwork-daemon backup -out ./backups -with-files   # 连同 runs/skills
恢复：停 daemon，把备份 db 覆盖 `$AGENTWORK_HOME/agentwork.db`（删除旧的 -wal/-shm），再启动。

## 详细文档
- docs/PATHS.md：全部数据路径与环境变量
- docs/UPGRADE.md：保留数据的升级流程
- docs/TEMPLATES.md：把现有配置导出成模板

## CLI
    ./bin/agentwork --help
EOF
  mkdir -p "$REL/docs"
  cp docs/PATHS.md docs/UPGRADE.md docs/TEMPLATES.md "$REL/docs/"

  tar -czf "$REL.tar.gz" -C dist "$(basename "$REL")"
done

rm -rf "$WEB_STAGE"

echo ""
echo "✓ 打包完成:"
for arch in $ARCHS; do
  REL="dist/agentwork-v$VERSION-$OS-$arch"
  printf "  %-42s %s\n" "$REL.tar.gz" "$(du -h "$REL.tar.gz" | cut -f1)"
done