#!/usr/bin/env bash
# Prepare a JEM-compatible agentwork swap artifact (bin/ + web/) from the
# upstream source. Solves the two mismatches that block a packaged release:
#   1. arch:   build GOARCH=native for THIS host (amd64 package on arm64 host)
#   2. port:   bake the JEM daemon port into the Next proxies
#              (web/.next/required-server-files.json must proxy the SAME
#              daemon port the JEM env uses, e.g. 17450 — packaged releases
#              bake the default 47813 and would 500 against a 17450 daemon).
#
# v6 (2026-11-04): born from the real deployment review. The upstream
# package.sh bakes localhost:47813; JEM envs run daemon on 17450/175xx and
# their Web on 17460. This script produces the "runtime dir" shape the JEM
# swap expects: bin/{agentwork,agentwork-daemon} + web/{server.js,...}.
#
# Usage:
#   bash prepare-swap-artifact.sh \
#       --daemon-port 17450 \
#       --version 0.0.1-beta.1 \
#       --out /tmp/aw-swap-artifact
#   # then:
#   bash scripts/jem-swap-release.sh --release /tmp/aw-swap-artifact \
#       --env-id agentwork --daemon-port 17450 --runtime-dir /root/runtime/agentwork
#
# Options:
#   --daemon-port P  daemon port to bake into the web proxies (default 17450)
#   --out DIR        output directory (default /tmp/aw-swap-artifact)
#   --src-dir DIR    reuse an existing source checkout instead of cloning
#   --repo URL       upstream repo (default https://github.com/Sinkear/agentwork.git)
#   --arch A         GOARCH (default: auto from uname -m)
#   --version V      version stamp (default upstream build.sh default)
set -euo pipefail

DAEMON_PORT="${DAEMON_PORT:-17450}"
OUT="${OUT:-/tmp/aw-swap-artifact}"
SRC="${SRC:-}"
REPO="${REPO:-https://github.com/Sinkear/agentwork.git}"
ARCH="${ARCH:-auto}"
VERSION="${VERSION:-}"
NODE_BIN_DIR="${NODE_BIN_DIR:-/root/runtime/agentwork/node/bin}"
NPM_REGISTRY="${NPM_REGISTRY:-https://registry.npmmirror.com}"

while [ $# -gt 0 ]; do
  case "$1" in
    --daemon-port) DAEMON_PORT="$2"; shift 2;;
    --out) OUT="$2"; shift 2;;
    --src-dir) SRC="$2"; shift 2;;
    --repo) REPO="$2"; shift 2;;
    --arch) ARCH="$2"; shift 2;;
    --version) VERSION="$2"; shift 2;;
    -h|--help) sed -n '2,30p' "$0"; exit 0;;
    *) echo "unknown argument: $1" >&2; exit 2;;
  esac
done

log() { echo "[$(date +%H:%M:%S)] $*"; }
die() { echo "[FATAL] $*" >&2; exit 1; }
step() { echo; echo "━━━ $* ━━━"; }

if [ "$ARCH" = "auto" ]; then
  case "$(uname -m)" in
    x86_64) ARCH="amd64";;
    aarch64) ARCH="arm64";;
    *) die "unknown host arch $(uname -m); pass --arch";;
  esac
fi
[ -x "$NODE_BIN_DIR/node" ] || die "no bundled node at $NODE_BIN_DIR/node (build_and_deploy.sh provisions it) — set NODE_BIN_DIR"

WORK=$(mktemp -d /tmp/aw-prepare.XXXXXX)
trap 'rm -rf "$WORK"' EXIT

# ── 1. source ──────────────────────────────────────────────────────────────
if [ -n "$SRC" ]; then
  [ -f "$SRC/build.sh" ] || die "--src-dir $SRC has no build.sh"
  CODE="$SRC"
else
  step "克隆上游源码"
  git clone --depth 1 "$REPO" "$WORK/src" 2>/dev/null || die "clone failed: $REPO"
  CODE="$WORK/src"
fi
log "source: $CODE"

VERSION="${VERSION:-$(grep -oE 'VERSION="\$\{AGENTWORK_COMPILE_VERSION:-[^}]*\}"' "$CODE/build.sh" | sed -E 's/.*:-([^}]*)\}.*/\1/')}"
log "version stamp: $VERSION  arch: $ARCH  daemon port baked: $DAEMON_PORT"

# ── 2. web: rewrite proxies to the JEM daemon port, keep standalone ────────
step "构建 Web 前端 (Next standalone, proxies -> 127.0.0.1:$DAEMON_PORT)"
cat > "$CODE/web/next.config.ts" <<EOF
import type { NextConfig } from "next";

// 部署适配（JEM 制品原地替换）：daemon 监听 127.0.0.1:$DAEMON_PORT（平台
// 预留 17000~17999 段），web 与 daemon 同机部署，端口固化进构建产物。
const nextConfig: NextConfig = {
  allowedDevOrigins: ["127.0.0.1", "localhost"],
  output: "standalone",
  turbopack: { root: __dirname },
  async rewrites() {
    return [
      { source: "/ws", destination: "http://127.0.0.1:$DAEMON_PORT/ws" },
      { source: "/backend/:path*", destination: "http://127.0.0.1:$DAEMON_PORT/:path*" },
      { source: "/webhooks/:path*", destination: "http://127.0.0.1:$DAEMON_PORT/webhooks/:path*" },
    ];
  },
};

export default nextConfig;
EOF

export PATH="$NODE_BIN_DIR:$PATH"
(cd "$CODE/web" && npm ci --registry "$NPM_REGISTRY" --no-audit --no-fund && npm run build) || die "web build failed"

# ── 3. go: native arch, same stamp ─────────────────────────────────────────
step "构建 Go 二进制 (linux/$ARCH, stamp=$VERSION)"
(cd "$CODE" && CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" AGENTWORK_COMPILE_VERSION="$VERSION" ./build.sh) || die "go build failed"
file "$CODE/build/agentwork" "$CODE/build/agentwork-daemon"

# ── 4. assemble swap dir ───────────────────────────────────────────────────
step "组装输出目录 $OUT"
rm -rf "$OUT"
mkdir -p "$OUT/bin" "$OUT/web/.next"
cp "$CODE/build/agentwork" "$OUT/bin/agentwork"
cp "$CODE/build/agentwork-daemon" "$OUT/bin/agentwork-daemon"
chmod +x "$OUT/bin/agentwork" "$OUT/bin/agentwork-daemon"
cp -r "$CODE/web/.next/standalone/." "$OUT/web"
cp -r "$CODE/web/.next/static" "$OUT/web/.next/static"
cp -r "$CODE/web/public" "$OUT/web/public"

# ── 5. verify baked port ──────────────────────────────────────────────────
step "校验产物"
BAKED=$(grep -rhoE 'http://(localhost|127\.0\.0\.1):[0-9][0-9]*' "$OUT/web/.next/required-server-files.json" 2>/dev/null | head -1 | sed -E 's/.*://')
log "web proxies daemon :${BAKED:-??}"
[ "$BAKED" = "$DAEMON_PORT" ] || die "baked port $BAKED != expected $DAEMON_PORT — aborting (build produced wrong proxy target)"
[ -f "$OUT/web/server.js" ] || die "missing web/server.js"
"$OUT/bin/agentwork" version 2>/dev/null | sed 's/^/  cli version: /' || true
"$OUT/bin/agentwork-daemon" version 2>/dev/null | sed 's/^/  daemon version: /' || true

echo
echo "✓ swap artifact ready: $OUT  (arch=$ARCH daemon_port=$DAEMON_PORT version=$VERSION)"
echo "  下一步: bash scripts/jem-swap-release.sh --release $OUT --env-id agentwork --daemon-port $DAEMON_PORT"