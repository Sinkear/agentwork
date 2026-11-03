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
#   web/server.js          前端自包含服务（:3000，无需 node_modules / 源码）
#   web/.next/static       前端静态资源
#   web/public             前端公共资源
#   start-all.sh           一键启动 daemon + 前端
#   README.deploy.md       部署说明
#
# 前端与架构无关（无 next/image → 不依赖 sharp），各架构包共用同一份产物，
# 用硬链接复制，不额外占磁盘。
#
# 依赖：Go ≥ 1.21（交叉编译零配置）、Node ≥ 20（运行前端 standalone 服务）。
set -e
cd "$(dirname "$0")"

# Go 构建缓存放仓库内：脚本自包含，也在受限文件沙箱（只允许写工作区）下可用。
export GOCACHE="${GOCACHE:-$PWD/tmp/go-cache}"

VERSION="${AGENTWORK_COMPILE_VERSION:-0.0.1-beta.1}"
ARCHS="${ARCHS:-amd64 arm64}"
OS="${OS:-linux}"
LDFLAGS="-X main.cliVersion=$VERSION -X github.com/eushing/agentwork/internal/daemon.DaemonVersion=$VERSION"

echo "── 1/4 构建前端（standalone，架构无关）──"
(cd web && npx next build)

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
# 一键启动：daemon(:${DAEMON_PORT:-47813}) + 前端(:${WEB_PORT:-3000})。
#   ./start-all.sh            前台运行（两个进程）
#   WEB_PORT=3000 HOST=0.0.0.0 ./start-all.sh
set -e
cd "$(dirname "$0")"
HOST="${HOST:-127.0.0.1}"
WEB_PORT="${WEB_PORT:-3000}"
DAEMON_PORT="${DAEMON_PORT:-47813}"
# 前端反代目标是构建期固化的 47813：改了 daemon 端口必须重新打包，
# 否则页面能打开、/backend 全 500。
if [ "$DAEMON_PORT" != "47813" ]; then
  echo "⚠ DAEMON_PORT=$DAEMON_PORT 与前端内置反代目标(47813)不一致——" >&2
  echo "  API 调用会失败。换端口请改 web/next.config.ts 后重新打包。" >&2
fi

./bin/agentwork-daemon -addr "127.0.0.1:$DAEMON_PORT" &
DAEMON_PID=$!
trap 'kill $DAEMON_PID 2>/dev/null' EXIT INT TERM

cd web
HOSTNAME="$HOST" PORT="$WEB_PORT" exec node server.js
EOF
  chmod +x "$REL/start-all.sh"

  cat > "$REL/README.deploy.md" <<EOF
# agentwork v$VERSION 部署包（$OS/$arch）

## 运行要求
  - $OS/$arch，Node ≥ 20（仅前端 standalone 服务需要；Go 二进制零依赖、静态链接）

## 快速开始
    ./start-all.sh
    # 打开 http://127.0.0.1:3000

## 端口
- 3000：Web 前端（/backend/* 与 /ws 由它反代到 daemon，浏览器无需直连 47813）
- 47813：daemon API（仅本机，不常用端口）

## 常用变量
- WEB_PORT（默认 3000）、HOST（默认 127.0.0.1；对外暴露设 0.0.0.0）
- DAEMON_PORT（默认 47813，改了它 Next 反代目标也要同步改）

## CLI
    ./bin/agentwork --help
EOF

  tar -czf "$REL.tar.gz" -C dist "$(basename "$REL")"
done

rm -rf "$WEB_STAGE"

echo ""
echo "✓ 打包完成:"
for arch in $ARCHS; do
  REL="dist/agentwork-v$VERSION-$OS-$arch"
  printf "  %-42s %s\n" "$REL.tar.gz" "$(du -h "$REL.tar.gz" | cut -f1)"
done