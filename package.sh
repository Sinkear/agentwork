#!/bin/sh
# 产出"解压即跑"的完整部署包：Go 二进制（CLI + daemon）+ 前端自包含服务。
#
#   ./package.sh                          # 默认版本
#   AGENTWORK_COMPILE_VERSION=0.0.1-beta.2 ./package.sh
#
# 包结构（dist/agentwork-v$VERSION/）：
#   bin/agentwork          CLI
#   bin/agentwork-daemon   daemon（API :7373）
#   web/server.js          前端自包含服务（:3000，无需 node_modules / 源码）
#   web/.next/static       前端静态资源
#   web/public             前端公共资源
#   start-all.sh           一键启动 daemon + 前端
#   README.deploy.md       部署说明
#
# 依赖：Go ≥ 1.x、Node ≥ 20（运行前端 standalone 服务需要 Node，Next SSR 的固有要求）。
# 前端构建复用 web/node_modules，无需 npm install。
set -e
cd "$(dirname "$0")"

# Go 构建缓存放仓库内：脚本自包含，也在受限文件沙箱（只允许写工作区）下可用。
export GOCACHE="${GOCACHE:-$PWD/tmp/go-cache}"

VERSION="${AGENTWORK_COMPILE_VERSION:-0.0.1-beta.1}"
REL="dist/agentwork-v$VERSION"

echo "── 1/4 构建 Go 二进制 ──"
AGENTWORK_COMPILE_VERSION="$VERSION" ./build.sh

echo "── 2/4 构建前端（standalone）──"
(cd web && npx next build)

echo "── 3/4 组装 $REL ──"
rm -rf "$REL"
mkdir -p "$REL/bin"
cp build/agentwork build/agentwork-daemon "$REL/bin/"

# standalone 服务本体（server.js + 裁剪后的 node_modules + package.json）
cp -r web/.next/standalone "$REL/web"
# Next 约定：static 与 public 不进 standalone，需手动放入
mkdir -p "$REL/web/.next"
cp -r web/.next/static "$REL/web/.next/static"
cp -r web/public "$REL/web/public"

echo "── 4/4 写启动脚本与说明 ──"
cat > "$REL/start-all.sh" <<'EOF'
#!/bin/sh
# 一键启动：daemon(:7373) + 前端(:${WEB_PORT:-3000})。
#   ./start-all.sh            前台运行（两个进程）
#   WEB_PORT=3000 HOST=0.0.0.0 ./start-all.sh
set -e
cd "$(dirname "$0")"
HOST="${HOST:-127.0.0.1}"
WEB_PORT="${WEB_PORT:-3000}"

./bin/agentwork-daemon &
DAEMON_PID=$!
trap 'kill $DAEMON_PID 2>/dev/null' EXIT INT TERM

cd web
HOSTNAME="$HOST" PORT="$WEB_PORT" exec node server.js
EOF
chmod +x "$REL/start-all.sh"

cat > "$REL/README.deploy.md" <<EOF
# agentwork v$VERSION 部署包

## 运行要求
- Linux/macOS，Node ≥ 20（仅前端 standalone 服务需要；Go 二进制零依赖）

## 快速开始
    ./start-all.sh
    # 打开 http://127.0.0.1:3000

## 端口
- 3000：Web 前端（/backend/* 与 /ws 由它反代到 daemon，浏览器无需直连 7373）
- 7373：daemon API（仅本机）

## 常用变量
- WEB_PORT（默认 3000）、HOST（默认 127.0.0.1；对外暴露设 0.0.0.0）

## CLI
    ./bin/agentwork --help
EOF

tar -czf "$REL.tar.gz" -C dist "agentwork-v$VERSION"
echo ""
echo "✓ 打包完成:"
du -sh "$REL" "$REL.tar.gz"
echo "  $REL/  与  $REL.tar.gz"
