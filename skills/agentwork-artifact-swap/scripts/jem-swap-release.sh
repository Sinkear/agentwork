#!/usr/bin/env bash
# Replace the agentwork artifact (bin/ + web/) inside a job-environment sandbox
# WITHOUT touching the data directory. Safe flow: backup -> stop -> swap ->
# start, never delete/re-deploy (which recreates /workspace).
#
# v6 changelog (2026-11-04, validated against the real deployment):
#   - ARCH CHECK before stop: reject a release whose binaries do not match
#     the host arch (amd64 package on an arm64 host previously would have
#     stopped the env, swapped, failed at start, then rolled back — a
#     pointless outage).
#   - BAKED-PORT CHECK now greps both http://localhost:<p> and
#     http://127.0.0.1:<p> (JEM builds bake 127.0.0.1:<DAEMON_PORT>), and the
#     error message tells you how to rebuild for the target port.
#   - The previous version relied on GET /healthz?verbose=1 returning
#     {"home", "db", ...} — that endpoint DOES NOT EXIST in this codebase
#     (current daemons return plain "ok" for any /healthz request). All
#     data-root probing now uses the real commands:
#       * running daemon env:  /proc/<pid>/environ (HOME, AGENTWORK_HOME, AGENTWORK_DB)
#       * post-start verify:   agentwork-daemon paths  (prints home/db JSON)
#   - DB discovery prefers the JEM env card's cwd over a sandbox glob.
#   - Version-downgrade guard: WARN when the incoming release stamp is lower
#     than the currently running stamp (stamps are advisory; code is newer).
#
# Usage:
#   JEM_URL=http://127.0.0.1:8090 ENV_ID=agentwork DAEMON_PORT=17450 \
#     ./scripts/jem-swap-release.sh --release dist/agentwork-v0.0.1-beta.1-linux-arm64.tar.gz
#
# Options (env or flags):
#   --release PATH   new package dir or .tar.gz (required)
#   --env-id ID      job-environment env id (default agentwork)
#   --runtime-dir D  runtime dir on the host (default /root/runtime/$ENV_ID)
#   --data-home D    AGENTWORK_HOME host path (default: discover)
#   --db PATH        explicit SQLite path (default: discover)
#   --backup-dir D   backup root (default /root/workspace/agentwork-backups)
#   --daemon-port P  daemon port for health check (default 47813)
#   --no-backup      skip the pre-swap backup
#   --allow-arch     skip the architecture check (dangerous)
#
# Exit codes: 0 success, 1 failure. The DB is never modified; on a failed
# start the previous bin/web is restored automatically.
set -euo pipefail

JEM_URL="${JEM_URL:-http://127.0.0.1:8090}"
ENV_ID="${ENV_ID:-agentwork}"
RUNTIME_DIR="${RUNTIME_DIR:-}"
DATA_HOME="${DATA_HOME:-}"
DB="${DB:-}"
BACKUP_DIR="${BACKUP_DIR:-/root/workspace/agentwork-backups}"
DAEMON_PORT="${DAEMON_PORT:-47813}"
RELEASE="${RELEASE:-}"
DO_BACKUP=1
ALLOW_ARCH=${ALLOW_ARCH:-0}

usage() {
  awk 'NR > 1 { if ($0 ~ /^set -euo/) exit; sub(/^# ?/, ""); print }' "$0"
}
while [ $# -gt 0 ]; do
  case "$1" in
    --release) RELEASE="$2"; shift 2;;
    --env-id) ENV_ID="$2"; RUNTIME_DIR="${RUNTIME_DIR:-}"; shift 2;;
    --runtime-dir) RUNTIME_DIR="$2"; shift 2;;
    --data-home) DATA_HOME="$2"; shift 2;;
    --db) DB="$2"; shift 2;;
    --backup-dir) BACKUP_DIR="$2"; shift 2;;
    --daemon-port) DAEMON_PORT="$2"; shift 2;;
    --no-backup) DO_BACKUP=0; shift;;
    --allow-arch) ALLOW_ARCH=1; shift;;
    -h|--help) usage; exit 0;;
    *) echo "unknown argument: $1" >&2; usage; exit 2;;
  esac
done
[ -n "$RELEASE" ] || { usage; exit 2; }
RUNTIME_DIR="${RUNTIME_DIR:-/root/runtime/$ENV_ID}"

log()  { echo "[$(date +%H:%M:%S)] $*"; }
die()  { echo "[FATAL] $*" >&2; exit 1; }
warn() { echo "[WARN] $*" >&2; }
step() { echo; echo "━━━ $* ━━━"; }

cleanup() {
  [ -n "${WORK:-}" ] && rm -rf "$WORK" 2>/dev/null || true
}
trap cleanup EXIT

# ── 0. artifact layout + arch + baked port (ALL before stop) ───────────────
[ -e "$RELEASE" ] || die "release not found: $RELEASE"
WORK=$(mktemp -d /tmp/agentwork-swap.XXXXXX)
REL=""
if [ -d "$RELEASE" ]; then
  REL="$RELEASE"
else
  case "$RELEASE" in
    *.tar.gz|*.tgz) tar -xzf "$RELEASE" -C "$WORK" || die "extract failed";;
    *) die "release must be a directory or .tar.gz";;
  esac
  REL=$(find "$WORK" -maxdepth 3 -type f -name agentwork-daemon -printf '%h\n' 2>/dev/null | head -1)
  [ -n "$REL" ] && REL=$(dirname "$REL") || die "release has no bin/agentwork-daemon"
fi
[ -x "$REL/bin/agentwork" ] && [ -x "$REL/bin/agentwork-daemon" ] || die "release missing bin/agentwork{,-daemon}"
[ -f "$REL/web/server.js" ] || die "release missing web/server.js"

if [ "$ALLOW_ARCH" != 1 ]; then
  step "校验架构（release vs 宿主机）"
  HOST_ARCH="$(uname -m)"
  case "$HOST_ARCH" in
    x86_64) HOST_NORM="amd64";;
    aarch64) HOST_NORM="arm64";;
    *) HOST_NORM="$HOST_ARCH";;
  esac
  REL_ARCH=$(file -b "$REL/bin/agentwork" | grep -oE 'x86-64|ARM aarch64' | head -1 | sed 's/x86-64/amd64/;s/ARM aarch64/arm64/' || true)
  [ -n "$REL_ARCH" ] || die "cannot detect release arch from $(file -b "$REL/bin/agentwork")"
  log "host=$HOST_NORM release=$REL_ARCH"
  if [ "$REL_ARCH" != "$HOST_NORM" ]; then
    die "architecture mismatch: release is $REL_ARCH but this host is $HOST_NORM ($HOST_ARCH). " \
        "Rebuild locally with: bash scripts/prepare-swap-artifact.sh --daemon-port $DAEMON_PORT --out /tmp/aw-swap-artifact  " \
        "(or run upstream package.sh with ARCHS=$HOST_NORM)."
  fi
fi

step "校验前端反代端口（release web 烧录的 daemon 端口 == $DAEMON_PORT）"
# Next 把 daemon 反代目标固化在构建产物里；JEM 构建烧 127.0.0.1，包内构建烧 localhost，
# 两种都要认。端口不一致时页面能开、/backend/* 全 500，必须停机前拒绝。
WEB_BAKED_PORT=$(grep -rhoE 'http://(localhost|127\.0\.0\.1):[0-9][0-9]*' "$REL/web/.next/required-server-files.json" 2>/dev/null | head -1 | sed -E 's/.*://')
if [ -n "$WEB_BAKED_PORT" ]; then
  log "web proxies daemon :$WEB_BAKED_PORT"
  if [ "$WEB_BAKED_PORT" != "$DAEMON_PORT" ]; then
    die "release web proxies :$WEB_BAKED_PORT but --daemon-port=$DAEMON_PORT; rebuild with DAEMON_PORT=$DAEMON_PORT " \
        "(JEM 环境: bash scripts/prepare-swap-artifact.sh --daemon-port $DAEMON_PORT; 源码仓库: DAEMON_PORT=$DAEMON_PORT ./package.sh)"
  fi
else
  warn "could not find a baked proxy port in web/.next/required-server-files.json — skipping the port check (build style may differ)"
fi

# ── 1. env state + DB discovery (before stop) ──────────────────────────────
step "检查环境 $ENV_ID"
curl -sf "$JEM_URL/api/v1/envs/$ENV_ID" >/dev/null || die "job-env-manager unreachable: $JEM_URL"
ENV_JSON=$(curl -s "$JEM_URL/api/v1/envs/$ENV_ID")
STATE=$(printf '%s' "$ENV_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("state",""))')
CWD=$(printf '%s' "$ENV_JSON" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("cwd",""))')
case "$STATE" in
  running) WAS_RUNNING=1;;
  stopped) WAS_RUNNING=0;;
  *) die "env state $STATE is not running/stopped";;
esac
log "state=$STATE cwd=$CWD runtime=$RUNTIME_DIR"

DAEMON_PID=""
if [ "$WAS_RUNNING" = 1 ]; then
  # 运行中：daemon 自己的进程环境就是数据根的 ground truth（/healthz 只返回 "ok"，
  # 不带 home/db——别指望它）。
  DAEMON_PID=$(ss -tlnp 2>/dev/null | grep ":$DAEMON_PORT " | grep -oE 'pid=[0-9]+' | head -1 | cut -d= -f2 || true)
  if [ -n "$DAEMON_PID" ] && [ -d "/proc/$DAEMON_PID" ]; then
    ENVLINES=$(tr '\0' '\n' < "/proc/$DAEMON_PID/environ" 2>/dev/null || true)
    DEHOME=$(printf '%s\n' "$ENVLINES" | awk -F= '$1=="HOME"{print substr($0,index($0,"=")+1); exit}')
    DEAWH=$(printf '%s\n' "$ENVLINES" | awk -F= '$1=="AGENTWORK_HOME"{print substr($0,index($0,"=")+1); exit}')
    DEAWB=$(printf '%s\n' "$ENVLINES" | awk -F= '$1=="AGENTWORK_DB"{print substr($0,index($0,"=")+1); exit}')
    log "daemon pid=$DAEMON_PID env HOME=${DEHOME:-} AGENTWORK_HOME=${DEAWH:-} AGENTWORK_DB=${DEAWB:-}"
    if [ -z "$DB" ] && [ -n "$DEAWB" ]; then DB="$DEAWB"; fi
    if [ -z "$DB" ] && [ -n "$DEAWH" ]; then DB="$DEAWH/agentwork.db"; fi
    if [ -z "$DB" ] && [ -n "$DEHOME" ]; then DB="$DEHOME/.agentwork/agentwork.db"; fi
  fi
fi
if [ -z "$DB" ] && [ -n "$CWD" ] && [ -d "$CWD" ]; then
  DB="$CWD/.agentwork/agentwork.db"
  log "database from env card cwd: $DB"
fi
if [ -z "$DB" ] && [ -n "$DATA_HOME" ]; then
  DB="$DATA_HOME/agentwork.db"
fi
if [ -z "$DB" ]; then
  DB=$(ls -t /root/job-envs/sandboxes/${ENV_ID}-*/.agentwork/agentwork.db 2>/dev/null | head -1 || true)
fi
[ -n "$DB" ] && [ -f "$DB" ] || die "cannot locate agentwork.db; pass --data-home or --db"
DATA_HOME=$(dirname "$DB")
log "database: $DB"

# 版本降级警告（版本戳只是建议，代码可能更新但戳没 bump——只警告不阻断）
OLD_VER=$("$RUNTIME_DIR/bin/agentwork-daemon" version 2>/dev/null | head -1 || true)
REL_RUNNABLE=0
if [ "$ALLOW_ARCH" = 1 ]; then REL_RUNNABLE=1
elif [ -x "$REL/bin/agentwork-daemon" ] && [ "${REL_ARCH:-}" = "${HOST_NORM:-}" ]; then REL_RUNNABLE=1
fi
if [ "$REL_RUNNABLE" = 1 ]; then
  NEW_VER=$("$REL/bin/agentwork-daemon" version 2>/dev/null | head -1 || true)
  if [ -n "$OLD_VER" ] && [ -n "$NEW_VER" ] && [ "$OLD_VER" != "$NEW_VER" ]; then
    warn "version stamp changes: running=$OLD_VER release=$NEW_VER (互相比较仅供参考——代码新旧以功能为准，别只看戳)"
  fi
fi

# ── 2. stop ────────────────────────────────────────────────────────────────
if [ "$WAS_RUNNING" = 1 ]; then
  step "停止环境（workspace 保留）"
  curl -s -X POST "$JEM_URL/api/v1/envs/$ENV_ID/stop" >/dev/null
  ST=""
  for _ in $(seq 1 45); do
    ST=$(curl -s "$JEM_URL/api/v1/envs/$ENV_ID" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("state",""))')
    [ "$ST" = stopped ] && break
    [ "$ST" = error ] && break
    sleep 2
  done
  [ "$ST" = stopped ] || die "stop failed, state=$ST"
fi

# ── 3. backup DB (+ skills) ────────────────────────────────────────────────
if [ "$DO_BACKUP" = 1 ]; then
  step "备份数据"
  BK="$BACKUP_DIR/agentwork-$(date +%Y%m%d-%H%M%S)"
  mkdir -p "$BK"
  for f in agentwork.db agentwork.db-wal agentwork.db-shm; do
    [ -f "$(dirname "$DB")/$f" ] && cp -a "$(dirname "$DB")/$f" "$BK/"
  done
  python3 - "$BK/agentwork.db" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
assert db.execute("PRAGMA integrity_check").fetchone()[0] == "ok"
print("  integrity=ok goals=%d agents=%d skills=%d squads=%d schedules=%d" % (
    db.execute("SELECT count(*) FROM goal").fetchone()[0],
    db.execute("SELECT count(*) FROM agent").fetchone()[0],
    db.execute("SELECT count(*) FROM skill").fetchone()[0],
    db.execute("SELECT count(*) FROM squad").fetchone()[0],
    db.execute("SELECT count(*) FROM schedule").fetchone()[0]))
PY
  if [ -d "$DATA_HOME/runs/skills" ]; then
    tar -czf "$BK/runs-skills.tar.gz" -C "$DATA_HOME" runs/skills
    log "skills backup: $BK/runs-skills.tar.gz"
  fi
  log "backup: $BK"
fi

# ── 4. swap bin/web in place ───────────────────────────────────────────────
step "替换制品（只换 bin/web）"
[ -x "$RUNTIME_DIR/bin/agentwork-daemon" ] || die "runtime dir looks wrong: $RUNTIME_DIR"
RB="$WORK/rollback"
mkdir -p "$RB"
cp -a "$RUNTIME_DIR/bin" "$RB/bin" 2>/dev/null || true
[ -d "$RUNTIME_DIR/web" ] && cp -a "$RUNTIME_DIR/web" "$RB/web" 2>/dev/null || true
# $RB lives under $WORK, which the EXIT trap deletes. Keep a durable copy of
# the previous artifact so you can roll back days later, not just this run.
if [ "$DO_BACKUP" = 1 ] && [ -n "${BK:-}" ]; then
  cp -a "$RUNTIME_DIR/bin" "$BK/previous-bin" 2>/dev/null || true
  [ -d "$RUNTIME_DIR/web" ] && cp -a "$RUNTIME_DIR/web" "$BK/previous-web" 2>/dev/null || true
  log "previous artifact kept: $BK/previous-bin $BK/previous-web"
fi

cp "$REL/bin/agentwork" "$RUNTIME_DIR/bin/agentwork.new"
cp "$REL/bin/agentwork-daemon" "$RUNTIME_DIR/bin/agentwork-daemon.new"
chmod +x "$RUNTIME_DIR/bin/agentwork.new" "$RUNTIME_DIR/bin/agentwork-daemon.new"
mv "$RUNTIME_DIR/bin/agentwork.new" "$RUNTIME_DIR/bin/agentwork"
mv "$RUNTIME_DIR/bin/agentwork-daemon.new" "$RUNTIME_DIR/bin/agentwork-daemon"
rm -rf "$RUNTIME_DIR/web.new" "$RUNTIME_DIR/web.old"
cp -a "$REL/web" "$RUNTIME_DIR/web.new"
[ -d "$RUNTIME_DIR/web" ] && mv "$RUNTIME_DIR/web" "$RUNTIME_DIR/web.old"
mv "$RUNTIME_DIR/web.new" "$RUNTIME_DIR/web"
rm -rf "$RUNTIME_DIR/web.old"
log "artifact replaced (rollback: $RB)"

restore_rollback() {
  echo "[WARN] restoring previous bin/web" >&2
  rm -rf "$RUNTIME_DIR/web"
  cp -a "$RB/bin/." "$RUNTIME_DIR/bin/" 2>/dev/null || true
  [ -d "$RB/web" ] && cp -a "$RB/web" "$RUNTIME_DIR/web" || true
}

# ── 5. start + verify ──────────────────────────────────────────────────────
if [ "$WAS_RUNNING" = 1 ]; then
  step "启动环境"
  curl -s -X POST "$JEM_URL/api/v1/envs/$ENV_ID/start" >/dev/null
  ST=""
  for _ in $(seq 1 60); do
    ST=$(curl -s "$JEM_URL/api/v1/envs/$ENV_ID" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("state",""))')
    [ "$ST" = running ] && break
    [ "$ST" = error ] && break
    sleep 2
  done
  if [ "$ST" != running ]; then
    restore_rollback
    curl -s -X POST "$JEM_URL/api/v1/envs/$ENV_ID/start" >/dev/null || true
    die "start failed, state=$ST; old artifact restored"
  fi

  # 数据根校验：直接读【新 daemon 进程】自己的 environ（最权威——/healthz 只返回
  # "ok"；用脚本自己的 HOME 跑 `agentwork-daemon paths` 会给出错误答案，因为 JEM
  # 启动脚本设的是 HOME=沙箱 cwd）。
  NDAEMON_PID=$(ss -tlnp 2>/dev/null | grep ":$DAEMON_PORT " | grep -oE 'pid=[0-9]+' | head -1 | cut -d= -f2 || true)
  ACTUAL_HOME=""; ACTUAL_DB=""; ACTUAL_WH=""; ACTUAL_WDB=""
  if [ -n "$NDAEMON_PID" ] && [ -d "/proc/$NDAEMON_PID" ]; then
    ENVLINES=$(tr '\0' '\n' < "/proc/$NDAEMON_PID/environ" 2>/dev/null || true)
    ACTUAL_WH=$(printf '%s\n' "$ENVLINES" | awk -F= '$1=="AGENTWORK_HOME"{print substr($0,index($0,"=")+1); exit}')
    ACTUAL_WDB=$(printf '%s\n' "$ENVLINES" | awk -F= '$1=="AGENTWORK_DB"{print substr($0,index($0,"=")+1); exit}')
    ACTUAL_HOME=$(printf '%s\n' "$ENVLINES" | awk -F= '$1=="HOME"{print substr($0,index($0,"=")+1); exit}')
    if [ -n "$ACTUAL_WDB" ]; then ACTUAL_DB="$ACTUAL_WDB"
    elif [ -n "$ACTUAL_WH" ]; then ACTUAL_DB="$ACTUAL_WH/agentwork.db"
    elif [ -n "$ACTUAL_HOME" ]; then ACTUAL_DB="$ACTUAL_HOME/.agentwork/agentwork.db"
    fi
  fi
  if [ -z "$ACTUAL_DB" ]; then
    warn "cannot read daemon pid environ (pid=${NDAEMON_PID:-none}); fallback: HOME=$CWD agentwork-daemon paths"
    ACTUAL_PATHS=$(HOME="$CWD" "$RUNTIME_DIR/bin/agentwork-daemon" paths 2>/dev/null || true)
    ACTUAL_DB=$(printf '%s' "$ACTUAL_PATHS" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("database",""))' 2>/dev/null || true)
  fi
  if [ -n "$ACTUAL_DB" ] && [ -f "$ACTUAL_DB" ]; then
    EXPECT_DB=$(readlink -f "$DB")
    if [ "$(readlink -f "$ACTUAL_DB")" != "$EXPECT_DB" ]; then
      curl -s -X POST "$JEM_URL/api/v1/envs/$ENV_ID/stop" >/dev/null || true
      die "daemon used db=$ACTUAL_DB expected $EXPECT_DB. Set AGENTWORK_HOME=$DATA_HOME (or AGENTWORK_DB=$DB) in the JEM template env, then retry."
    fi
    log "data root confirmed (daemon environ): home=${ACTUAL_HOME:-?} db=$ACTUAL_DB"
    curl -sf --max-time 5 "http://127.0.0.1:$DAEMON_PORT/domains" >/dev/null || die "daemon health check failed on :$DAEMON_PORT"
  else
    warn "could not confirm data root (db=${ACTUAL_DB:-?}); falling back to HTTP checks"
    INFO=$(curl -sf --max-time 5 "http://127.0.0.1:$DAEMON_PORT/healthz" || true)
    log "daemon healthz: ${INFO:-unreachable}"
    curl -sf --max-time 5 "http://127.0.0.1:$DAEMON_PORT/domains" >/dev/null || die "daemon health check failed on :$DAEMON_PORT"
  fi

  V=$("$RUNTIME_DIR/bin/agentwork" version)
  log "version: $V"
  DOMAINS=$(curl -s --max-time 5 "http://127.0.0.1:$DAEMON_PORT/domains" | python3 -c 'import json,sys; d=json.load(sys.stdin); print(len(d) if isinstance(d,list) else d.get("domains",len(d)))' 2>/dev/null || echo "?")
  log "domains: $DOMAINS"
  python3 - "$DB" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
print("  data: goals=%d agents=%d skills=%d squads=%d schedules=%d" % (
    db.execute("SELECT count(*) FROM goal").fetchone()[0],
    db.execute("SELECT count(*) FROM agent").fetchone()[0],
    db.execute("SELECT count(*) FROM skill").fetchone()[0],
    db.execute("SELECT count(*) FROM squad").fetchone()[0],
    db.execute("SELECT count(*) FROM schedule").fetchone()[0]))
PY
  log "OK — artifact swapped, data preserved"
else
  log "OK — artifact swapped; env was stopped and stays stopped"
fi