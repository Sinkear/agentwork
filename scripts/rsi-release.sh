#!/bin/sh
# RSI 发布脚本（sg-RELEASE 交付物）：从指定 commit 构建 daemon+CLI，过测试门后
# 原子替换部署目录的 bin/，并借助环境守护循环重启 daemon 使新二进制生效。
#
# 用法:
#   scripts/rsi-release.sh <commit|branch>
#
# 环境变量（均可选）:
#   RELEASE_REPO     源仓库路径（默认: 本脚本所在仓库根）
#   RELEASE_BIN      部署目标 bin/ 目录
#                    （默认: /root/Agentwork/agentwork-v0.1.0-linux-arm64/bin）
#   RELEASE_SYMBOLS  冒号分隔的字符串清单，必须全部出现在新 daemon 二进制里，
#                    用于校验「新二进制确实含本轮修复」；留空跳过该校验。
#                    例: RELEASE_SYMBOLS='sanitizeVerifyEnv:auto-retry in'
#   SKIP_TESTS=1     跳过 go test 门（默认不跳；发布门要求全绿）
#   DAEMON_URL       健康检查地址（默认 http://127.0.0.1:47813）
#   STACK_LOG        栈启动日志（默认 /root/.agentwork/start-all.log）
#   DAEMON_LOG       daemon 日志（默认 /root/.agentwork/daemon.log；留空跳过检查）
#
# 为什么不整环境 stop→start：执行本脚本的 agent 会话本身挂在环境的 connect
# 进程树下，整环境 stop 会连带杀掉在途 run（含自己）。改用「rename 原子替换
# + 只杀 daemon 进程 + 守护循环（15s 一轮）自动重启栈」，效果等价（新二进制
# 生效）且不伤在途会话。rename 覆盖正在运行的可执行文件是允许的（ETXTBSY
# 只拦写式打开，不拦 rename），因此天然不会触发 Text file busy。
# 数据目录（AGENTWORK_HOME）全程不碰；web/ 不在替换范围。
set -eu

COMMIT="${1:?用法: scripts/rsi-release.sh <commit|branch>}"
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO="${RELEASE_REPO:-$(cd "$SCRIPT_DIR/.." && pwd)}"
BIN_DIR="${RELEASE_BIN:-/root/Agentwork/agentwork-v0.1.0-linux-arm64/bin}"
DAEMON_URL="${DAEMON_URL:-http://127.0.0.1:47813}"
STACK_LOG="${STACK_LOG:-/root/.agentwork/start-all.log}"
DAEMON_LOG="${DAEMON_LOG:-/root/.agentwork/daemon.log}"
STAGE="${RELEASE_STAGE:-/tmp/rsi-release}"

log() { echo "[rsi-release] $*"; }
fail() { echo "[rsi-release] FAIL: $*" >&2; exit 1; }

command -v go >/dev/null 2>&1 || fail "go 不在 PATH"
command -v curl >/dev/null 2>&1 || fail "curl 不在 PATH"
[ -d "$BIN_DIR" ] || fail "部署目录不存在: $BIN_DIR"

# ── 1. 检出源码 ──────────────────────────────────────────────
git -C "$REPO" worktree prune >/dev/null 2>&1 || true
SRC="$STAGE/src"
rm -rf "$SRC"
git -C "$REPO" worktree add --detach "$SRC" "$COMMIT" >/dev/null 2>&1 \
  || fail "无法检出 $COMMIT"
cleanup() {
  git -C "$REPO" worktree remove --force "$SRC" >/dev/null 2>&1 \
    || git -C "$REPO" worktree prune >/dev/null 2>&1 || true
}
trap cleanup EXIT
log "源码检出: $SRC @ $(git -C "$SRC" rev-parse --short HEAD)"

# ── 2. 测试门（与机器验证门同款命令）────────────────────────
# 用 env -u 隔离 AGENTWORK_HOME/AGENTWORK_DB：daemon 进程自带这两个变量时，
# paths.Home() 优先读它们，击穿 t.Setenv("HOME") 的用例隔离（digest 系用例
# 互踩），这正是 P-A 修的泄漏在测试进程自身的映射。
if [ "${SKIP_TESTS:-0}" != "1" ]; then
  log "go test 门: env -u AGENTWORK_HOME/DB go test ./... -count=1 ..."
  (cd "$SRC" && env -u AGENTWORK_HOME -u AGENTWORK_DB go test ./... -count=1) \
    || fail "go test 未全绿——拒绝发布"
  log "go test 全绿"
fi

# ── 3. 构建 ─────────────────────────────────────────────────
log "构建 daemon 与 CLI..."
(cd "$SRC" && go build -o "$STAGE/agentwork-daemon" ./cmd/agentwork-daemon) \
  || fail "daemon 构建失败"
(cd "$SRC" && go build -o "$STAGE/agentwork" ./cmd/agentwork-cli) \
  || fail "CLI 构建失败"

# ── 4. 修复符号校验（新二进制必须含本轮修复）────────────────
for sym in $(echo "${RELEASE_SYMBOLS:-}" | tr ':' ' '); do
  [ -n "$sym" ] || continue
  strings "$STAGE/agentwork-daemon" | grep -q "$sym" \
    || fail "新二进制缺少修复符号: $sym"
done
[ -n "${RELEASE_SYMBOLS:-}" ] && log "符号校验通过: $RELEASE_SYMBOLS"

# 冒烟：新二进制至少能被加载执行（防 ELF/链接损坏导致重启后起不来）
"$STAGE/agentwork-daemon" -h >/dev/null 2>&1
rc=$?
[ "$rc" -le 2 ] || fail "新 daemon 二进制无法执行（exit $rc）"

# ── 5. 备份 + 原子替换 bin/ ─────────────────────────────────
BACKUP="$BIN_DIR/../bin.bak-rsi-release-$(date +%Y%m%d-%H%M%S)"
mkdir -p "$BACKUP"
cp -p "$BIN_DIR/agentwork-daemon" "$BIN_DIR/agentwork" "$BACKUP/" 2>/dev/null || true
log "旧二进制备份: $BACKUP"

cp "$STAGE/agentwork-daemon" "$BIN_DIR/.agentwork-daemon.new"
cp "$STAGE/agentwork"       "$BIN_DIR/.agentwork.new"
mv -f "$BIN_DIR/.agentwork-daemon.new" "$BIN_DIR/agentwork-daemon"
mv -f "$BIN_DIR/.agentwork.new"        "$BIN_DIR/agentwork"
log "bin/ 已替换: daemon=$(sha256sum "$BIN_DIR/agentwork-daemon" | cut -c1-12)… cli=$(sha256sum "$BIN_DIR/agentwork" | cut -c1-12)…"

# ── 6. 重启 daemon：只杀 daemon 进程，守护循环自动重启栈 ────
# 记录日志行数偏移，重启后只检查新增行。
stack_lines_before=0
[ -f "$STACK_LOG" ] && stack_lines_before=$(wc -l < "$STACK_LOG")
daemon_lines_before=0
[ -f "$DAEMON_LOG" ] && daemon_lines_before=$(wc -l < "$DAEMON_LOG")

DAEMON_PID="$(pgrep -f 'agentwork-daemon -addr' | head -1 || true)"
if [ -n "$DAEMON_PID" ]; then
  log "kill daemon pid=$DAEMON_PID，等待守护循环（≤15s/轮）重启栈..."
  kill "$DAEMON_PID" 2>/dev/null || true
fi
ok=0
i=0
while [ "$i" -lt 60 ]; do
  if curl -sf -o /dev/null "$DAEMON_URL/goals"; then ok=1; break; fi
  i=$((i + 1)); sleep 2
done
if [ "$ok" != 1 ]; then
  log "守护循环未接住——直启 start-all.sh 兜底"
  (cd "$(dirname "$BIN_DIR")" && nohup env HOST=0.0.0.0 WEB_PORT=47814 \
    ./start-all.sh >>"$STACK_LOG" 2>&1 &)
  i=0
  while [ "$i" -lt 30 ]; do
    if curl -sf -o /dev/null "$DAEMON_URL/goals"; then ok=1; break; fi
    i=$((i + 1)); sleep 2
  done
fi
[ "$ok" = 1 ] || fail "daemon 重启后仍未就绪（回滚: cp $BACKUP/* $BIN_DIR/ 后重试）"
log "daemon 已用新二进制就绪: $DAEMON_URL"

# ── 7. 新起日志检查：重启后的新增行不得有 [ERROR]/panic ─────
sleep 2
check_log() {
  _f="$1" _off="$2" _name="$3"
  [ -f "$_f" ] || return 0
  _new="$(tail -n +$((_off + 1)) "$_f")" || return 0
  if echo "$_new" | grep -nE '\[ERROR\]|panic' >/dev/null 2>&1; then
    echo "$_new" | grep -nE '\[ERROR\]|panic' | head -5 >&2
    fail "$_name 新起段含错误，请人工检查 $_f"
  fi
}
check_log "$STACK_LOG" "$stack_lines_before" "start-all.log"
check_log "$DAEMON_LOG" "$daemon_lines_before" "daemon.log"

log "RELEASE 完成: commit=$COMMIT daemon=$DAEMON_URL bin=$BIN_DIR"
log "备注: 存量 connect 进程仍在内存中运行旧 CLI，新 CLI 二进制已就位，下次 connect 重启自然生效。"
