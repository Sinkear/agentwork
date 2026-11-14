#!/usr/bin/env bash
# scripts/rsi-round-report.sh —— RSI 轮次报告生成器（数据驱动 / fail loud）
#
# 用途：为 RSI 自迭代第 N 轮生成 docs/rsi/round-report-r<N>.md。
#       报告固定五节，全部内容由 daemon API 数据与本地 git log 派生；
#       脚本本身不含任何轮次结论、数字或提案代号，换轮次只换参数。
#
# 依赖：bash、curl、git、python3（仅标准库）。
#
# 数据源（只用以下端点；/rsi 与 /runs/{id} 不是列表端点，本脚本不使用）：
#   GET /goals
#   GET /rsi/sessions
#   GET /goals/{id}/runs
#   GET /goals/{id}/comments
#   GET /rsi/sessions/{id}
#   GET /rsi/sessions/{id}/rounds
#
# 失败语义：
#   - 参数非法、缺依赖、不在 git 仓库内：exit 2。
#   - 必需 API（/goals、/rsi/sessions、范围内每个 goal 的 /goals/{id}/runs）
#     任一失败，或本轮找不到标题匹配的 goal：stderr 明确说明 + exit 1，
#     不写输出文件。
#   - 可选数据（comments / session 详情 / rounds）缺失：对应节标注「数据不可得」。
#
# 输出：默认 <repo>/docs/rsi/round-report-r<N>.md；先写同目录临时文件再 mv。
#       --dry-run 只输出到 stdout。
set -euo pipefail

PROG="$(basename "$0")"
API="http://127.0.0.1:47813"
INT_BRANCH="agentwork-int"
SINCE=""
UNTIL=""
OUT=""
REPO_URL="https://github.com/Sinkear/agentwork"
DRY_RUN=0
N=""

usage() {
  cat <<'USAGE'
用法：
  scripts/rsi-round-report.sh <N> [选项]

位置参数：
  N              轮次号，接受 3 或 R3（必须 ≥ 1）

选项：
  --api URL          daemon API 基址（默认 http://127.0.0.1:47813）
  --int-branch REF   集成分支（默认 agentwork-int）
  --since ROUND      跨轮对比起始轮（默认 1），接受 1 或 R1
  --until ROUND      跨轮对比结束轮（默认 N-1），接受 3 或 R3
  --out PATH         输出路径（默认 <repo>/docs/rsi/round-report-r<N>.md）
  --repo URL         报告内 commit 链接的仓库基址
                     （默认 https://github.com/Sinkear/agentwork）
  --dry-run          只输出到 stdout，不写文件
  -h, --help         显示本帮助

报告结构（五节，全部由数据派生）：
  ① 本轮概览        goals/sessions、node_count、critical_path、speedup、top findings
  ② 失败与取消      run 明细、attempt、result_summary/cancel_reason
  ③ 提交清单        git log（按轮次时间范围；取不到时退化最近 30 条并标注口径）
  ④ 指标            每个 session/round 的 metrics（无则标注「无」）
  ⑤ 候选项          findings 的 category/severity/title，注明「需人工判断，不自动采纳」

轮次标题匹配：
  「RSI R<N>」与「RSI 自迭代 R<N>」两种写法都识别。

数据源（仅以下端点）：
  GET /goals
  GET /rsi/sessions
  GET /goals/{id}/runs
  GET /goals/{id}/comments
  GET /rsi/sessions/{id}
  GET /rsi/sessions/{id}/rounds

退出码：
  0  成功
  1  必需 API 失败 / 未找到本轮 goal（stderr 说明，不写输出文件）
  2  参数非法、缺少依赖或不在 git 仓库内
USAGE
}

die2() { # die2 <消息> [--show-usage]
  echo "错误：$1" >&2
  [[ "${2:-}" == "--show-usage" ]] && usage >&2
  exit 2
}

# parse_round <3|R3|r3> → 输出十进制轮次号；非法返回 1
parse_round() {
  local raw="$1"
  raw="${raw#R}"
  raw="${raw#r}"
  [[ "$raw" =~ ^[0-9]+$ ]] || return 1
  local v=$((10#$raw))
  (( v >= 1 )) || return 1
  printf '%s\n' "$v"
}

while (( $# > 0 )); do
  case "$1" in
    -h|--help) usage; exit 0 ;;
    --dry-run) DRY_RUN=1; shift ;;
    --api)
      (( $# >= 2 )) || die2 "--api 需要参数"
      API="$2"; shift 2 ;;
    --int-branch)
      (( $# >= 2 )) || die2 "--int-branch 需要参数"
      INT_BRANCH="$2"; shift 2 ;;
    --since)
      (( $# >= 2 )) || die2 "--since 需要参数"
      SINCE="$2"; shift 2 ;;
    --until)
      (( $# >= 2 )) || die2 "--until 需要参数"
      UNTIL="$2"; shift 2 ;;
    --out)
      (( $# >= 2 )) || die2 "--out 需要参数"
      OUT="$2"; shift 2 ;;
    --repo)
      (( $# >= 2 )) || die2 "--repo 需要参数"
      REPO_URL="$2"; shift 2 ;;
    -*) die2 "未知选项：$1" --show-usage ;;
    *)
      if [[ -z "$N" ]]; then N="$1"; shift
      else die2 "多余的位置参数：$1" --show-usage
      fi ;;
  esac
done

[[ -n "$N" ]] || die2 "缺少轮次号 N" --show-usage
N_RAW="$N"
N="$(parse_round "$N")" || die2 "轮次号非法：$N_RAW（需形如 3 或 R3，且 ≥ 1）"

if [[ -n "$SINCE" ]]; then
  S_RAW="$SINCE"
  SINCE="$(parse_round "$SINCE")" || die2 "--since 非法：$S_RAW（需形如 1 或 R1，且 ≥ 1）"
else
  SINCE=1
fi
if [[ -n "$UNTIL" ]]; then
  U_RAW="$UNTIL"
  UNTIL="$(parse_round "$UNTIL")" || die2 "--until 非法：$U_RAW（需形如 3 或 R3，且 ≥ 1）"
else
  UNTIL=$((N-1))
fi
# N=1 时默认 until=0 表示「无历史轮」，允许；其余情况区间必须有序。
if (( UNTIL > 0 && SINCE > UNTIL )); then
  die2 "--since ($SINCE) 不能大于 --until ($UNTIL)"
fi

for c in curl git python3; do
  command -v "$c" >/dev/null 2>&1 || die2 "缺少依赖：$c"
done

REPO_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || true)"
[[ -n "$REPO_ROOT" ]] || die2 "当前目录不在 git 仓库内（③ 提交清单需要 git log，默认输出路径也基于仓库根）"

API="${API%/}"
REPO_URL="${REPO_URL%/}"
[[ -n "$INT_BRANCH" ]] || die2 "--int-branch 不能为空"

TMP="$(mktemp -d "${TMPDIR:-/tmp}/rsi-round-report.XXXXXX")"
OUT_TMP=""
cleanup() {
  rm -rf "$TMP"
  if [[ -n "$OUT_TMP" ]]; then rm -f "$OUT_TMP"; fi
}
trap cleanup EXIT

curl_err_text() {
  [[ -s "$TMP/curl.err" ]] || return 0
  head -c 400 "$TMP/curl.err" | tr '\n' ' '
}

# 必需数据：失败即 stderr 明确 + exit 1（此时尚未创建任何输出文件）
require_fetch() { # require_fetch <path> <out>
  local path="$1" out="$2" rc=0
  if curl -sfS --max-time 10 "$API$path" -o "$out" 2>"$TMP/curl.err"; then
    rm -f "$TMP/curl.err"
    return 0
  else
    rc=$?
  fi
  echo "错误：必需 API 不可用：$API$path（curl exit $rc）" >&2
  if [[ -s "$TMP/curl.err" ]]; then
    echo "  curl: $(curl_err_text)" >&2
  fi
  echo "错误：中止生成，未写输出文件。" >&2
  exit 1
}

# 可选数据：失败只登记，由渲染阶段在对应节标注「数据不可得」
optional_fetch() { # optional_fetch <path> <out>
  local path="$1" out="$2" rc=0
  if curl -sfS --max-time 10 "$API$path" -o "$out" 2>"$TMP/curl.err"; then
    rm -f "$TMP/curl.err"
    return 0
  else
    rc=$?
  fi
  printf '%s\n' "$path" >> "$TMP/optional.failures"
  {
    printf '%s (curl exit %s' "$path" "$rc"
    if [[ -s "$TMP/curl.err" ]]; then printf '：%s' "$(curl_err_text)"; fi
    printf ')\n'
  } >> "$TMP/optional.details"
  rm -f "$TMP/curl.err"
  return 0
}

require_fetch "/goals" "$TMP/goals.json"
require_fetch "/rsi/sessions?limit=200" "$TMP/sessions.json"

# 决定本轮/范围内每轮的目标 goal 与训练会话，并写出待拉取端点清单。
if ! python3 - "$TMP" "$N" "$SINCE" "$UNTIL" <<'PYEOF'
import json, os, re, sys

tmp, n, since, until = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4])

def fail(msg):
    print(msg, file=sys.stderr)
    sys.exit(1)

def load(path):
    try:
        with open(path, encoding="utf-8") as f:
            return json.load(f)
    except FileNotFoundError:
        fail("错误：缺少已下载的 API 数据：%s" % path)
    except Exception as e:
        fail("错误：%s 不是合法 JSON：%s" % (os.path.basename(path), e))

goals = load(os.path.join(tmp, "goals.json"))
sessions = load(os.path.join(tmp, "sessions.json"))
if not isinstance(goals, list):
    fail("错误：/goals 返回的不是 JSON 数组")
if not isinstance(sessions, list):
    fail("错误：/rsi/sessions 返回的不是 JSON 数组")

scope = set(range(since, until + 1)) if until >= since else set()
scope.add(n)
scope = sorted(scope)

patterns = {rn: re.compile(rf"RSI(?:\s*自迭代)?\s*R{rn}(?!\d)") for rn in scope}
by_round = {}
for g in goals:
    if not isinstance(g, dict):
        continue
    title = g.get("title") or ""
    for rn, pat in patterns.items():
        if pat.search(title):
            by_round.setdefault(rn, []).append(g)

if not by_round.get(n):
    sample = "\n".join("  - %s" % (g.get("title") or "?") for g in goals[:20] if isinstance(g, dict))
    fail(
        "错误：/goals 共 %d 条，未找到标题匹配「RSI R%d」或「RSI 自迭代 R%d」的 goal。\n"
        "最近的 goal 标题（最多 20 条）：\n%s\n"
        "错误：中止生成，未写输出文件。" % (len(goals), n, n, sample or "  （空）")
    )

by_round_ids = {
    str(rn): [str(g.get("id")) for g in gs if isinstance(g, dict) and g.get("id")]
    for rn, gs in by_round.items()
}
with open(os.path.join(tmp, "matches.json"), "w", encoding="utf-8") as f:
    json.dump({"scope": scope, "by_round": by_round_ids}, f, ensure_ascii=False)

sess_by_goal = {}
for s in sessions:
    if isinstance(s, dict):
        sess_by_goal.setdefault(s.get("goal_id") or "", []).append(s)

lines = []
for rn in scope:
    for g in by_round.get(rn, []):
        if not isinstance(g, dict):
            continue
        gid = g.get("id") or ""
        if not gid:
            continue
        lines.append("runs\t%d\t%s" % (rn, gid))
        lines.append("comments\t%d\t%s" % (rn, gid))
        for s in sess_by_goal.get(gid, []):
            sid = s.get("id") or ""
            if sid:
                lines.append("session\t%d\t%s" % (rn, sid))
                lines.append("rounds\t%d\t%s" % (rn, sid))
with open(os.path.join(tmp, "plan.tsv"), "w", encoding="utf-8") as f:
    f.write("\n".join(lines) + ("\n" if lines else ""))

# 本轮时间窗：goal 最早 created_at → 该轮 session 最晚 sealed/finished/started。
mine = [g for g in by_round.get(n, []) if isinstance(g, dict)]
starts = [g.get("created_at") for g in mine if g.get("created_at")]
ends = []
for g in mine:
    for s in sess_by_goal.get(g.get("id") or "", []):
        for k in ("sealed_at", "finished_at", "started_at"):
            if s.get(k):
                ends.append(s[k])
with open(os.path.join(tmp, "window.json"), "w", encoding="utf-8") as f:
    json.dump({"start": min(starts) if starts else "", "end": max(ends) if ends else ""}, f)
PYEOF
then
  # python 已把具体原因写到 stderr；这里只补一句结论。
  echo "错误：无法确定本轮报告的数据范围，未写输出文件。" >&2
  exit 1
fi

# 拉取范围内每轮 goal 的 runs（必需）/ comments（可选）与 session 详情/账本（可选）
# ID 来自 daemon API，但仍校验字符集：它同时进入 URL 和 $TMP 文件名，
# 不能让异常数据把路径拼出 TMP（例如含 / 或 ..）。
while IFS=$'\t' read -r kind rn ident; do
  [[ -n "$kind" ]] || continue
  [[ "$ident" =~ ^[A-Za-z0-9_-]{1,64}$ ]] || die2 "API 返回了非法 ID：$ident（plan.tsv）"
  case "$kind" in
    runs)     require_fetch "/goals/$ident/runs" "$TMP/runs_$ident.json" ;;
    comments) optional_fetch "/goals/$ident/comments" "$TMP/comments_$ident.json" ;;
    session)  optional_fetch "/rsi/sessions/$ident" "$TMP/session_$ident.json" ;;
    rounds)   optional_fetch "/rsi/sessions/$ident/rounds" "$TMP/rounds_$ident.json" ;;
    *) : ;;
  esac
done < "$TMP/plan.tsv"

# ── ③ 提交清单的 git 证据：优先集成分支 + 轮次时间窗；取不到退化最近 30 条并标注口径。
GIT_REF=""
for cand in "$INT_BRANCH" "origin/$INT_BRANCH"; do
  if git -C "$REPO_ROOT" rev-parse --verify --quiet "$cand^{commit}" >/dev/null 2>&1; then
    GIT_REF="$cand"
    break
  fi
done

W_START="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("start",""))' "$TMP/window.json")"
W_END="$(python3 -c 'import json,sys;print(json.load(open(sys.argv[1])).get("end",""))' "$TMP/window.json")"

GIT_FMT='%H%x09%ad%x09%s'
: > "$TMP/git.tsv"
GIT_MODE=""
GIT_CMD=""
if [[ -n "$GIT_REF" ]]; then
  if [[ -n "$W_START" ]]; then
    if [[ -n "$W_END" ]]; then
      GIT_CMD="git log $GIT_REF --since=$W_START --until=$W_END -n 200"
      git -C "$REPO_ROOT" log "$GIT_REF" --since="$W_START" --until="$W_END" -n 200 \
        --format="$GIT_FMT" --date=short --no-decorate > "$TMP/git.tsv" 2>"$TMP/git.err" || true
    else
      GIT_CMD="git log $GIT_REF --since=$W_START -n 200"
      git -C "$REPO_ROOT" log "$GIT_REF" --since="$W_START" -n 200 \
        --format="$GIT_FMT" --date=short --no-decorate > "$TMP/git.tsv" 2>"$TMP/git.err" || true
    fi
    if [[ ! -s "$TMP/git.tsv" ]]; then
      GIT_MODE="fallback-recent30"
      GIT_CMD="git log $GIT_REF -30（按轮次时间窗无结果，退化最近 30 条）"
      git -C "$REPO_ROOT" log "$GIT_REF" -30 \
        --format="$GIT_FMT" --date=short --no-decorate > "$TMP/git.tsv" 2>"$TMP/git.err" || true
    else
      GIT_MODE="window"
    fi
  else
    GIT_MODE="recent30"
    GIT_CMD="git log $GIT_REF -30（未取到轮次时间范围，退化最近 30 条）"
    git -C "$REPO_ROOT" log "$GIT_REF" -30 \
      --format="$GIT_FMT" --date=short --no-decorate > "$TMP/git.tsv" 2>"$TMP/git.err" || true
  fi
else
  GIT_MODE="head30"
  GIT_CMD="git log HEAD -30（本地/远端均未找到集成分支 $INT_BRANCH，退化当前分支最近 30 条）"
  git -C "$REPO_ROOT" log HEAD -30 \
    --format="$GIT_FMT" --date=short --no-decorate > "$TMP/git.tsv" 2>"$TMP/git.err" || true
fi
{
  printf 'mode=%s\n' "$GIT_MODE"
  printf 'cmd=%s\n' "$GIT_CMD"
  printf 'ref=%s\n' "${GIT_REF:-HEAD}"
} > "$TMP/git.meta"

# ── 渲染五节报告
python3 - "$TMP" "$N" "$SINCE" "$UNTIL" "$REPO_URL" "$API" "$INT_BRANCH" > "$TMP/report.md" <<'PYEOF'
import json, math, os, sys
from datetime import datetime, timezone

tmp = sys.argv[1]
N, SINCE, UNTIL = int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4])
REPO_URL, API, INT_BRANCH = sys.argv[5], sys.argv[6], sys.argv[7]

def load(name, default=None):
    p = os.path.join(tmp, name)
    if not os.path.exists(p):
        return default
    try:
        with open(p, encoding="utf-8") as f:
            return json.load(f)
    except Exception:
        return default

def read_lines(name):
    p = os.path.join(tmp, name)
    if not os.path.exists(p):
        return []
    with open(p, encoding="utf-8") as f:
        return [ln.rstrip("\n") for ln in f if ln.strip()]

def fmt_val(v):
    if isinstance(v, bool):
        return "true" if v else "false"
    if isinstance(v, int):
        return str(v)
    if isinstance(v, float):
        s = f"{v:.4f}".rstrip("0").rstrip(".")
        return s or "0"
    if v is None:
        return "null"
    if isinstance(v, (dict, list)):
        return json.dumps(v, ensure_ascii=False, separators=(",", ":"))[:400]
    return str(v)

def fmt_ms(ms):
    try:
        ms = float(ms or 0)
    except (TypeError, ValueError):
        return "—"
    if ms <= 0:
        return "0"
    s = ms / 1000.0
    if s >= 3600:
        return f"{s/3600:.1f}h"
    if s >= 90:
        return f"{s/60:.1f}m"
    return f"{s:.1f}s"

def fmt_metrics(m):
    if not isinstance(m, dict) or not m:
        return "无"
    return " · ".join(f"`{k}`={fmt_val(v)}" for k, v in sorted(m.items()))

def ts(s):
    if not s:
        return "—"
    return str(s)[:16].replace("T", " ")

def cell(s, limit=220):
    s = "" if s is None else str(s)
    s = s.replace("\r", " ").replace("\n", " ").replace("|", "\\|")
    if len(s) > limit:
        s = s[:limit] + "…"
    return s or "—"

def clink(sha):
    sha = (sha or "").strip()
    if not sha:
        return "—"
    return f"[`{sha[:8]}`]({REPO_URL}/commit/{sha})"

goals = load("goals.json", []) or []
sessions = load("sessions.json", []) or []
matches = load("matches.json", {}) or {}
scope = sorted(int(x) for x in matches.get("scope", [N]))
by_round_ids = {int(k): v for k, v in (matches.get("by_round") or {}).items()}
goal_by_id = {g.get("id"): g for g in goals if isinstance(g, dict)}
sess_by_goal = {}
for s in sessions:
    if isinstance(s, dict):
        sess_by_goal.setdefault(s.get("goal_id") or "", []).append(s)
fails = set(read_lines("optional.failures"))

def rounds_of(sid):
    if f"/rsi/sessions/{sid}/rounds" in fails:
        return None
    return load(f"rounds_{sid}.json", None)

def detail_of(sid):
    if f"/rsi/sessions/{sid}" in fails:
        return None
    return load(f"session_{sid}.json", None)

def runs_of(gid):
    return load(f"runs_{gid}.json", []) or []

def metrics_of(det):
    if not isinstance(det, dict):
        return {}
    rep = det.get("report")
    if isinstance(rep, dict) and isinstance(rep.get("metrics"), dict) and rep["metrics"]:
        return rep["metrics"]
    wf = det.get("workflow")
    if isinstance(wf, dict) and isinstance(wf.get("metrics"), dict) and wf["metrics"]:
        return wf["metrics"]
    return {}

def findings_of(det):
    if isinstance(det, dict) and isinstance(det.get("findings"), list):
        return det["findings"]
    return []

# 生成时间：SOURCE_DATE_EPOCH 可固定（自测时用于逐字节比对两次输出）
if os.environ.get("SOURCE_DATE_EPOCH", "").strip():
    now_dt = datetime.fromtimestamp(int(os.environ["SOURCE_DATE_EPOCH"]), tz=timezone.utc)
else:
    now_dt = datetime.now(timezone.utc)
now = now_dt.strftime("%Y-%m-%d %H:%M UTC")

cur_goal_ids = by_round_ids.get(N) or []
cur_session_ids = [
    s.get("id") for gid in cur_goal_ids for s in sess_by_goal.get(gid, [])
    if isinstance(s, dict) and s.get("id")
]

L = []
w = L.append
w(f"# RSI R{N} 轮次报告")
w("")
w(f"> 生成时间：{now} · 生成器：`scripts/rsi-round-report.sh {N}`")
if UNTIL == 0:
    w(f"> 范围：本轮 R{N}；无历史轮可对比（N=1）")
else:
    w(f"> 范围：本轮 R{N}；账本对比 R{SINCE}–R{UNTIL}")
w(f"> API：`{API}` · 集成分支：`{INT_BRANCH}` · 仓库链接基址：`{REPO_URL}`")
w("> 本报告五节内容全部由 daemon API 数据与本地 git log 派生，不含脚本硬编码的轮次结论；")
w("> 缺失的可选数据会在对应节显式标注「数据不可得」，不做补造。")
if fails:
    w("> 本次可选数据不可得：" + "、".join(f"`{p}`" for p in sorted(fails)))
w("")

# ── ① 本轮概览 ─────────────────────────────────────────────────────────────
w("## ① 本轮概览")
w("")
w(f"- 本轮 goal 数：{len(cur_goal_ids)}；训练会话数：{len(cur_session_ids)}")
w("")
for gid in cur_goal_ids:
    g = goal_by_id.get(gid) or {}
    w(f"### goal `{gid}` — {cell(g.get('title'), 160)}")
    w("")
    w(f"- 状态：`{cell(g.get('status'), 30)}` · 执行模式：`{cell(g.get('execution_mode'), 30)}` · 创建：{ts(g.get('created_at'))} · assignee：`{cell(g.get('assignee_type'), 10)}/{cell(g.get('assignee_id'), 40)}`")
    sess = sess_by_goal.get(gid, [])
    if not sess:
        w("- 训练会话：无（`/rsi/sessions` 未返回该 goal 的会话；数据不可得）")
    for s in sess:
        sid = s.get("id") or ""
        det = detail_of(sid)
        met = metrics_of(det)
        node_count = s.get("node_count") or met.get("node_count") or "—"
        cp = s.get("critical_path_ms") or met.get("critical_path_ms")
        sp = s.get("parallel_speedup") or met.get("parallel_speedup")
        w(f"- 训练会话 `{sid}`：status=`{cell(s.get('status'), 20)}` · node_count={fmt_val(node_count)} · critical_path={fmt_ms(cp)} · parallel_speedup={fmt_val(sp) if sp not in (None, '', 0) else '—'} · findings={fmt_val(s.get('finding_count', '—'))}")
        if s.get("top_title"):
            w(f"  - 会话首要发现：`{cell(s.get('top_category') or '?', 40)}` · {cell(s.get('top_title'), 160)}")
        if isinstance(det, dict):
            wf = det.get("workflow")
            if isinstance(wf, dict):
                w(f"  - 图规模：{len(wf.get('nodes') or [])} 节点 / {len(wf.get('edges') or [])} 边 · 冻结={bool(wf.get('frozen'))}")
                crit = wf.get("critical_path") or []
                if crit:
                    w("  - 关键路径（API 字段）：" + " → ".join(f"`{c}`" for c in crit[:8]))
            else:
                w("  - 图数据：无（session 尚未装配 workflow）")
        else:
            w(f"  - session 详情：数据不可得（`/rsi/sessions/{sid}` 请求失败）")
        fs = findings_of(det)
        if not fs:
            fs = []
            rl = rounds_of(sid)
            if isinstance(rl, list):
                for r in rl:
                    if isinstance(r, dict) and isinstance(r.get("findings"), list):
                        fs.extend(r["findings"])
        if fs:
            fs = sorted(fs, key=lambda f: (f.get("score") or 0), reverse=True)[:5]
            w("  - top findings（按 score 降序，最多 5 条）：")
            w("")
            w("    | 类别 | 严重度 | 置信度 | 标题 | score |")
            w("    |---|---|---|---|---|")
            for f in fs:
                w(f"    | `{cell(f.get('category'), 40)}` | {cell(f.get('severity'), 20)} | {cell(f.get('confidence'), 10)} | {cell(f.get('title'), 120)} | {fmt_val(f.get('score'))} |")
        else:
            w("  - top findings：无（findings 为空或数据不可得）")
    w("")

# ── ② 失败与取消 ───────────────────────────────────────────────────────────
w("## ② 失败与取消")
w("")
w("口径：逐 goal 读取 `/goals/{id}/runs`；失败=`failed`，取消=`cancelled`。"
  "错误摘要取 `result_summary` 首行，无则标注 `cancel_reason`。")
w("")
for gid in cur_goal_ids:
    runs = [r for r in runs_of(gid) if isinstance(r, dict)]
    counts = {}
    for r in runs:
        counts[r.get("status") or "?"] = counts.get(r.get("status") or "?", 0) + 1
    known = sum(counts.get(k, 0) for k in ("completed", "failed", "cancelled"))
    w(f"### goal `{gid}`")
    w("")
    w(f"- run 总数 {len(runs)}：completed {counts.get('completed', 0)} · failed {counts.get('failed', 0)} · cancelled {counts.get('cancelled', 0)} · 其他 {len(runs) - known}")
    bad = [r for r in runs if r.get("status") in ("failed", "cancelled")]
    if bad:
        w("")
        w("| run | 状态 | 角色 | attempt | cancel_reason | 开始 | 结束 | 结果摘要 / 错误 |")
        w("|---|---|---|---|---|---|---|---|")
        for r in bad:
            summary = (r.get("result_summary") or "").strip().splitlines()
            text = summary[0] if summary else ""
            if not text:
                cr = r.get("cancel_reason") or ""
                text = f"cancel_reason={cr}" if cr else "(无结果摘要)"
            w(f"| `{cell(r.get('id'), 40)}` | {cell(r.get('status'), 12)} | {cell(r.get('role') or r.get('run_kind'), 20)} "
              f"| {fmt_val(r.get('attempt', '—'))} | {cell(r.get('cancel_reason'), 30)} "
              f"| {ts(r.get('started_at') or r.get('queued_at'))} | {ts(r.get('finished_at'))} | {cell(text)} |")
    else:
        w("- 无失败/取消 run。")
    w("")

# ── ③ 提交清单 ─────────────────────────────────────────────────────────────
w("## ③ 提交清单")
w("")
git_meta = {}
for line in read_lines("git.meta"):
    k, _, v = line.partition("=")
    git_meta[k] = v
mode = git_meta.get("mode", "")
mode_desc = {
    "window": "按轮次时间范围过滤（本轮 goal 最早 `created_at` → 该轮 session 最晚 `sealed_at/finished_at/started_at`）。",
    "fallback-recent30": "轮次时间范围内无 commit，退化为集成分支最近 30 条；这些 commit 不一定都属于本轮，需人工核对。",
    "recent30": "未取到轮次时间范围，退化为集成分支最近 30 条；不保证都属于本轮。",
    "head30": f"未找到集成分支 `{INT_BRANCH}`（本地与远端跟踪引用均无），退化为当前 HEAD 最近 30 条；不保证都属于本轮。",
}.get(mode, "口径未知。")
w(f"- 命令：`{git_meta.get('cmd', '—')}`")
w(f"- 口径：{mode_desc}")
w("")
git_rows = []
for line in read_lines("git.tsv"):
    parts = line.split("\t")
    if len(parts) >= 2:
        git_rows.append(parts)
if git_rows:
    w("| commit | 日期 | 说明 |")
    w("|---|---|---|")
    for parts in git_rows:
        sha = parts[0] if len(parts) > 0 else ""
        date = parts[1] if len(parts) > 1 else ""
        msg = "\t".join(parts[2:]) if len(parts) > 2 else ""
        w(f"| {clink(sha)} | {cell(date, 12)} | {cell(msg, 200)} |")
else:
    w("（该口径下没有取到 commit 记录。）")
w("")

# ── ④ 指标 ─────────────────────────────────────────────────────────────────
w("## ④ 指标")
w("")
w("口径：以 `/rsi/sessions/{id}/rounds` 轮次账本为权威，metrics 键值原样输出（不改名、不重算）；"
  "账本为空或不可得时回退 `/rsi/sessions/{id}` 的 `report.metrics` / `workflow.metrics`，并标注来源。"
  "指标缺失时写「无」。")
w("")
for rn in scope:
    w(f"### R{rn}")
    w("")
    gids = by_round_ids.get(rn) or []
    if not gids:
        w("- 未找到标题匹配的 goal（数据不可得）。")
        w("")
        continue
    for gid in gids:
        g = goal_by_id.get(gid) or {}
        w(f"- goal `{gid}` — {cell(g.get('title'), 160)}")
        sess = sess_by_goal.get(gid, [])
        if not sess:
            w("  - 训练会话：无（数据不可得）")
        for s in sess:
            sid = s.get("id") or ""
            w(f"  - session `{sid}`（status=`{cell(s.get('status'), 20)}`）")
            rl = rounds_of(sid)
            if rl is None:
                w(f"    - 轮次账本：数据不可得（`/rsi/sessions/{sid}/rounds` 请求失败）")
            elif not rl:
                w("    - 轮次账本：空（该会话尚未冻结/分析，或账本无行）")
            else:
                for r in rl:
                    if not isinstance(r, dict):
                        continue
                    w(f"    - round_no={fmt_val(r.get('round_no', '?'))} · trigger=`{cell(r.get('trigger'), 20)}` · created={ts(r.get('created_at'))} · 来源：轮次账本")
                    w(f"      - metrics：{fmt_metrics(r.get('metrics'))}")
                    if r.get("delta"):
                        w(f"      - delta：{fmt_metrics(r.get('delta'))}")
            if rl is None or not rl:
                det = detail_of(sid)
                met = metrics_of(det)
                if met:
                    src = "report.metrics" if isinstance(det.get("report"), dict) and (det.get("report") or {}).get("metrics") else "workflow.metrics"
                    w(f"    - 回退来源：session 详情 {src}")
                    w(f"    - metrics：{fmt_metrics(met)}")
                elif det is None:
                    w(f"    - 回退指标：数据不可得（`/rsi/sessions/{sid}` 请求失败）")
                else:
                    w("    - 回退指标：无（session 详情既无 report.metrics 也无 workflow.metrics）")
    w("")

# ── ⑤ 候选项 ───────────────────────────────────────────────────────────────
w("## ⑤ 候选项")
w("")
w("> 候选项由 findings 的 `category` / `severity` / `title` / `suggestion` 派生，"
  "**需人工判断，不自动采纳**。本清单不是批准列表；实施前须人工评估、补复测计划。")
w("")
rows = []
missing = []
for rn in scope:
    for gid in by_round_ids.get(rn) or []:
        for s in sess_by_goal.get(gid, []):
            sid = s.get("id") or ""
            rl = rounds_of(sid)
            got = False
            if isinstance(rl, list) and rl:
                for r in rl:
                    if not isinstance(r, dict):
                        continue
                    rno = r.get("round_no", "?")
                    fs = r.get("findings")
                    if isinstance(fs, list):
                        for f in fs:
                            if isinstance(f, dict):
                                rows.append((rn, sid, rno, f))
                                got = True
            if not got:
                det = detail_of(sid)
                for f in findings_of(det):
                    if isinstance(f, dict):
                        rows.append((rn, sid, "—", f))
                        got = True
                if not got and rl is None and det is None:
                    missing.append(f"R{rn} session `{sid}`：轮次账本与 session 详情均不可得")
def score_of(f):
    """findings.score 排序键：非数值/NaN 一律按 0，避免一条脏数据毁掉整份报告。"""
    s = f.get("score")
    if isinstance(s, (int, float)) and not isinstance(s, bool) and math.isfinite(s):
        return float(s)
    return 0.0

rows.sort(key=lambda x: (x[0], -score_of(x[3])))
if rows:
    w(f"- 共 {len(rows)} 条候选项（范围内各轮 findings 汇总，按轮次、score 降序）。")
    w("")
    w("| 轮 | session | round_no | 类别 | 严重度 | 置信度 | 标题 | 建议（原文摘要） | finding 状态 |")
    w("|---|---|---|---|---|---|---|---|---|")
    for rn, sid, rno, f in rows:
        w(f"| R{rn} | `{sid}` | {fmt_val(rno)} | `{cell(f.get('category'), 40)}` | {cell(f.get('severity'), 20)} "
          f"| {cell(f.get('confidence'), 10)} | {cell(f.get('title'), 140)} | {cell(f.get('suggestion'), 180)} "
          f"| {cell(f.get('status'), 20)} |")
    dist = {}
    for _, _, _, f in rows:
        c = f.get("category") or "?"
        dist[c] = dist.get(c, 0) + 1
    w("")
    w("- 类别分布：" + " · ".join(f"`{k}`×{v}" for k, v in sorted(dist.items(), key=lambda kv: (-kv[1], kv[0]))))
else:
    if missing:
        for m in missing:
            w(f"- 数据不可得：{m}")
    w("（范围内 findings 为空，无可派生的候选项。）")
w("")
w("---")
w("")
w(f"*本报告由 `scripts/rsi-round-report.sh {N}` 自动生成；数据以 daemon API 为准。生成时间：{now}。*")

sys.stdout.write("\n".join(L) + "\n")
PYEOF

if (( DRY_RUN == 1 )); then
  cat "$TMP/report.md"
  exit 0
fi

OUT="${OUT:-$REPO_ROOT/docs/rsi/round-report-r$N.md}"
mkdir -p "$(dirname "$OUT")"
OUT_TMP="$(dirname "$OUT")/.$(basename "$OUT").tmp.$$"
cp "$TMP/report.md" "$OUT_TMP"
mv -f "$OUT_TMP" "$OUT"
OUT_TMP=""
echo "已生成: $OUT"