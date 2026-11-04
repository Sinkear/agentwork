#!/usr/bin/env bash
# Replace the agentwork artifact (bin/ + web/) inside a job-environment sandbox
# WITHOUT touching the data directory. Safe flow: backup -> stop -> swap ->
# start, never delete/re-deploy (which recreates /workspace).
#
# Usage:
#   JEM_URL=http://127.0.0.1:8090 ENV_ID=agentwork DAEMON_PORT=17450 \
#     ./scripts/jem-swap-release.sh dist/agentwork-v0.0.1-beta.2-linux-amd64.tar.gz
#
# Options (env or flags):
#   --release PATH   new package dir or .tar.gz (required)
#   --env-id ID      job-environment env id (default agentwork)
#   --runtime-dir D  runtime dir on the host (default /root/runtime/$ENV_ID)
#   --data-home D    AGENTWORK_HOME host path (default: discover from sandbox)
#   --db PATH        explicit SQLite path (default: $DATA_HOME/agentwork.db)
#   --backup-dir D   backup root (default /root/workspace/agentwork-backups)
#   --daemon-port P  daemon port for health check (default 47813)
#   --no-backup      skip the pre-swap backup
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
    -h|--help) usage; exit 0;;
    *) echo "unknown argument: $1" >&2; usage; exit 2;;
  esac
done
[ -n "$RELEASE" ] || { usage; exit 2; }
RUNTIME_DIR="${RUNTIME_DIR:-/root/runtime/$ENV_ID}"

log()  { echo "[$(date +%H:%M:%S)] $*"; }
die()  { echo "[FATAL] $*" >&2; exit 1; }
step() { echo; echo "━━━ $* ━━━"; }

cleanup() {
  [ -n "${WORK:-}" ] && rm -rf "$WORK" 2>/dev/null || true
}
trap cleanup EXIT

# ── 0. artifact layout ──────────────────────────────────────────────────────
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
# Next 把 daemon 反代端口固化在构建产物里；端口不一致时页面会 500，
# 必须在停机前就拒绝。
WEB_BAKED_PORT=$(grep -rho 'http://localhost:[0-9][0-9]*' "$REL/web/.next/required-server-files.json" 2>/dev/null | head -1 | sed 's/.*://')
if [ -n "$WEB_BAKED_PORT" ] && [ "$WEB_BAKED_PORT" != "$DAEMON_PORT" ]; then
  die "release web proxies :$WEB_BAKED_PORT but --daemon-port=$DAEMON_PORT; rebuild with DAEMON_PORT=$DAEMON_PORT ./package.sh"
fi
[ -n "$WEB_BAKED_PORT" ] && log "web proxies daemon :$WEB_BAKED_PORT"

# ── 1. env state + DB discovery (before stop) ──────────────────────────────
step "检查环境 $ENV_ID"
curl -sf "$JEM_URL/api/v1/envs/$ENV_ID" >/dev/null || die "job-env-manager unreachable: $JEM_URL"
STATE=$(curl -s "$JEM_URL/api/v1/envs/$ENV_ID" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("state",""))')
case "$STATE" in
  running) WAS_RUNNING=1;;
  stopped) WAS_RUNNING=0;;
  *) die "env state $STATE is not running/stopped";;
esac
log "state=$STATE runtime=$RUNTIME_DIR"

# Prefer the running daemon's own answer over guessing from the sandbox path:
# a migrated env may keep AGENTWORK_HOME on a host directory.
if [ -z "$DB" ] && [ "$WAS_RUNNING" = 1 ]; then
  HEALTH=$(curl -sf --max-time 5 "http://127.0.0.1:$DAEMON_PORT/healthz?verbose=1" || true)
  DB=$(printf '%s' "$HEALTH" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("db",""))' 2>/dev/null || true)
  [ -n "$DB" ] && log "database from running daemon: $DB"
fi

if [ -z "$DB" ]; then
  if [ -n "$DATA_HOME" ]; then
    DB="$DATA_HOME/agentwork.db"
  else
    DB=$(ls -t /root/job-envs/sandboxes/${ENV_ID}-*/.agentwork/agentwork.db 2>/dev/null | head -1 || true)
  fi
fi
[ -n "$DB" ] && [ -f "$DB" ] || die "cannot locate agentwork.db; pass --data-home or --db"
DATA_HOME=$(dirname "$DB")
log "database: $DB"

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
print("  integrity=ok goals=%d agents=%d skills=%d" % (
    db.execute("SELECT count(*) FROM goal").fetchone()[0],
    db.execute("SELECT count(*) FROM agent").fetchone()[0],
    db.execute("SELECT count(*) FROM skill").fetchone()[0]))
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
  # The artifact can only preserve data if the daemon really uses the same
  # home we backed up. start-all.sh defaults to $HOME/.agentwork, so a host
  # data dir must be exported by the JEM template env as AGENTWORK_HOME.
  HEALTH=$(curl -sf --max-time 5 "http://127.0.0.1:$DAEMON_PORT/healthz?verbose=1" || true)
  ACTUAL_HOME=$(printf '%s' "$HEALTH" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("home",""))' 2>/dev/null || true)
  ACTUAL_DB=$(printf '%s' "$HEALTH" | python3 -c 'import json,sys; print(json.load(sys.stdin).get("db",""))' 2>/dev/null || true)
  if [ -n "$ACTUAL_HOME" ] && [ "$(readlink -f "$ACTUAL_HOME")" != "$(readlink -f "$DATA_HOME")" ]; then
    curl -s -X POST "$JEM_URL/api/v1/envs/$ENV_ID/stop" >/dev/null || true
    die "daemon used home=$ACTUAL_HOME db=$ACTUAL_DB, expected home=$DATA_HOME. Set AGENTWORK_HOME=$DATA_HOME in the JEM template env (or migrate the old DB there), then retry."
  fi
  V=$("$RUNTIME_DIR/bin/agentwork" version)
  log "version: $V"
  log "daemon home: ${ACTUAL_HOME:-unknown} db: ${ACTUAL_DB:-unknown}"
  curl -sf --max-time 5 "http://127.0.0.1:$DAEMON_PORT/domains" >/dev/null || die "daemon health check failed on :$DAEMON_PORT"
  python3 - "$DB" <<'PY'
import sqlite3, sys
db = sqlite3.connect(sys.argv[1])
print("  data: goals=%d agents=%d skills=%d" % (
    db.execute("SELECT count(*) FROM goal").fetchone()[0],
    db.execute("SELECT count(*) FROM agent").fetchone()[0],
    db.execute("SELECT count(*) FROM skill").fetchone()[0]))
PY
  log "OK — artifact swapped, data preserved"
else
  log "OK — artifact swapped; env was stopped and stays stopped"
fi
