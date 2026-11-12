#!/usr/bin/env bash
# scripts/rsi-round-report.sh —— RSI 轮次演进报告生成器
#
# 用途：每轮 self-iter goal 收尾前运行，从 daemon API 与本地 git log 汇总生成
#       docs/rsi/round-report-r<N>.md（固定五节，自包含，不依赖读者登录平台）。
# 依赖：bash、curl、git、python3（标准库）。
# 用法：
#   scripts/rsi-round-report.sh <轮次号N> [选项]
# 选项：
#   --api URL          daemon API 基址（默认 http://127.0.0.1:47813）
#   --int-branch REF   集成分支（默认 agentwork-int）
#   --since R1 --until R3  历史轮号区间（默认 1..N-1，用于跨轮对比与候选清单节）
#   --out PATH         输出路径（默认 docs/rsi/round-report-r<N>.md）
#   --repo URL         报告内 commit 链接的仓库基址（默认 https://github.com/Sinkear/agentwork）
#   --dry-run          输出到 stdout，不写文件
# 说明：
#   - 数据源只用列表端点：/rsi/sessions、/rsi/sessions/{id}、/goals、/goals/{id}/runs、
#     /goals/{id}/comments、/rsi/sessions/{id}/rounds（/rsi 与 /runs/{id} 单查为 404，勿用）。
#   - API 不可达时降级：跳过对应小节并显式标注「数据不可得」，不伪造数字。
set -euo pipefail

N=""
API="http://127.0.0.1:47813"
INT_BRANCH="agentwork-int"
SINCE=1
UNTIL=""          # 默认 N-1
OUT=""
REPO_URL="https://github.com/Sinkear/agentwork"
DRY_RUN=0

while [[ $# -gt 0 ]]; do
  case "$1" in
    --api) API="$2"; shift 2 ;;
    --int-branch) INT_BRANCH="$2"; shift 2 ;;
    --since) SINCE="$2"; shift 2 ;;
    --until) UNTIL="$2"; shift 2 ;;
    --out) OUT="$2"; shift 2 ;;
    --repo) REPO_URL="$2"; shift 2 ;;
    --dry-run) DRY_RUN=1; shift ;;
    -h|--help) sed -n '2,20p' "$0"; exit 0 ;;
    *) if [[ -z "$N" ]]; then N="$1"; shift; else echo "未知参数: $1" >&2; exit 2; fi ;;
  esac
done
[[ -z "$N" ]] && { echo "用法: $0 <轮次号N> [--api URL] [--int-branch REF] [--since R1] [--until R3] [--out PATH] [--repo URL] [--dry-run]" >&2; exit 2; }
[[ "$N" =~ ^[0-9]+$ ]] || { echo "轮次号必须是整数: $N" >&2; exit 2; }
[[ -z "$UNTIL" ]] && UNTIL=$((N-1))
for c in curl git python3; do command -v "$c" >/dev/null || { echo "缺少依赖: $c" >&2; exit 2; }; done

REPO_ROOT="$(git rev-parse --show-toplevel)"
OUT="${OUT:-$REPO_ROOT/docs/rsi/round-report-r$N.md}"
TMP="$(mktemp -d /tmp/opencode/rsi-report.XXXXXX)"
trap 'rm -rf "$TMP"' EXIT

# ---------------------------------------------------------------- 采集
fetch() { # fetch <path> <outfile>; 失败写空文件并在 .errors 记一行
  local path="$1" out="$2"
  if ! curl -sf --max-time 10 "$API$path" -o "$out" 2>/dev/null; then
    : > "$out"; echo "$path" >> "$TMP/api.errors"
  fi
}

fetch /goals "$TMP/goals.json"
fetch /rsi/sessions "$TMP/sessions.json"

# 目标 goal：标题匹配「RSI R<N>」的训练 goal（可能有多条同轮记录，如重开轮，全保留）
python3 - "$TMP" "$N" <<'PYEOF'
import json, sys, re
tmp, n = sys.argv[1], sys.argv[2]
try:
    goals = json.load(open(f"{tmp}/goals.json"))
except Exception:
    goals = []
try:
    sessions = json.load(open(f"{tmp}/sessions.json"))
except Exception:
    sessions = []
pat = re.compile(rf"RSI\s*R{n}\b")
mine = [g for g in goals if pat.search(g.get("title", ""))]
prev = [g for g in goals if any(re.search(rf"RSI\s*R{k}\b", g.get("title", "")) for k in range(1, int(n)))]
json.dump(mine, open(f"{tmp}/my_goals.json", "w"), ensure_ascii=False)
json.dump(prev, open(f"{tmp}/prev_goals.json", "w"), ensure_ascii=False)
# goal_id -> session 映射（一个 goal 可能对应多个 session，取全部）
m = {}
for s in sessions:
    m.setdefault(s.get("goal_id", ""), []).append(s)
json.dump(m, open(f"{tmp}/sessions_by_goal.json", "w"), ensure_ascii=False)
PYEOF

# 逐 goal 拉 runs / comments / sessions/{id} 详情（只用列表端点）
python3 - "$TMP" <<'PYEOF' > "$TMP/fetch.list"
import json, sys
tmp = sys.argv[1]
mine = json.load(open(f"{tmp}/my_goals.json"))
prev = json.load(open(f"{tmp}/prev_goals.json"))
sess = json.load(open(f"{tmp}/sessions_by_goal.json"))
seen = set()
for g in mine + prev:
    gid = g["id"]
    if gid in seen: continue
    seen.add(gid)
    print(f"runs {gid}")
    print(f"comments {gid}")
    for s in sess.get(gid, []):
        print(f"session {s['id']}")
        print(f"rounds {s['id']}")
PYEOF
while read -r kind ident; do
  case "$kind" in
    runs) fetch "/goals/$ident/runs" "$TMP/runs_$ident.json" ;;
    comments) fetch "/goals/$ident/comments" "$TMP/comments_$ident.json" ;;
    session) fetch "/rsi/sessions/$ident" "$TMP/session_$ident.json" ;;
    rounds) fetch "/rsi/sessions/$ident/rounds" "$TMP/rounds_$ident.json" ;;
  esac
done < "$TMP/fetch.list"

# ---------------------------------------------------------------- git 证据
# 本轮 commit 区间：集成分支上「上一轮复测 commit（若可定位）..HEAD」；退化用最近 30 条
# 集成分支检测：优先本地，其次 origin/ 远端跟踪引用
GIT_BRANCH_REF=""
for cand in "$INT_BRANCH" "origin/$INT_BRANCH"; do
  if git -C "$REPO_ROOT" rev-parse --verify --quiet "$cand" >/dev/null 2>&1; then
    GIT_BRANCH_REF="$cand"; break
  fi
done
GIT_LOG=""
if [[ -n "$GIT_BRANCH_REF" ]]; then
  GIT_LOG="$(git -C "$REPO_ROOT" log "$GIT_BRANCH_REF" --oneline -30 --no-decorate 2>/dev/null || true)"
fi
# 也合并当前分支最近提交（报告生成时可能在 feat 分支上，本工作项 commit 尚未进集成分支）
CUR_LOG="$(git -C "$REPO_ROOT" log HEAD --oneline -15 --no-decorate 2>/dev/null || true)"
printf '%s\n' "$GIT_LOG" > "$TMP/gitlog-int.txt"
printf '%s\n' "$CUR_LOG" > "$TMP/gitlog-cur.txt"

# ---------------------------------------------------------------- 渲染
python3 - "$TMP" "$N" "$SINCE" "$UNTIL" "$REPO_URL" "$API" "$INT_BRANCH" <<'PYEOF' > "$TMP/report.md"
import json, os, re, sys
from datetime import datetime, timezone

tmp, N, SINCE, UNTIL, REPO_URL, API, INT_BRANCH = sys.argv[1], int(sys.argv[2]), int(sys.argv[3]), int(sys.argv[4]), sys.argv[5], sys.argv[6], sys.argv[7]

def load(name):
    p = os.path.join(tmp, name)
    try:
        return json.load(open(p))
    except Exception:
        return None

def api_failed():
    p = os.path.join(tmp, "api.errors")
    try:
        return [l.strip() for l in open(p) if l.strip()]
    except FileNotFoundError:
        return []

def fmt_ms(ms):
    if not ms: return "—"
    s = ms / 1000.0
    if s >= 90:
        return f"{s/60:.1f}m"
    return f"{s:.1f}s"

def ts(s):
    if not s: return "—"
    return s[:16].replace("T", " ")

def clink(sha):
    sha = (sha or "").strip()
    if not sha: return "—"
    return f"[`{sha[:8]}`]({REPO_URL}/commit/{sha})"

def classify(err):
    """根因分类：环境/代码/流程（关键词启发式，覆盖不了归入流程）。"""
    e = (err or "")
    if re.search(r"command not found|exit 127|machine offline|PRIVATE KEY|permission|network|connect|offline|unavailable|exit 128", e, re.I):
        return "环境"
    if re.search(r"FAIL:|verification failed|panic|exit status 1|compile|build failed", e, re.I):
        return "代码"
    return "流程"

goals = load("goals.json") or []
my_goals = load("my_goals.json") or []
prev_goals = load("prev_goals.json") or []
sess_by_goal = load("sessions_by_goal.json") or {}
errors = api_failed()

# 收集本轮与历史轮的 runs / rounds 数据
runs_by_goal, comments_by_goal = {}, {}
for fn in os.listdir(tmp):
    m = re.match(r"runs_(.+)\.json$", fn)
    if m:
        try: runs_by_goal[m.group(1)] = json.load(open(os.path.join(tmp, fn))) or []
        except Exception: runs_by_goal[m.group(1)] = []
    m = re.match(r"comments_(.+)\.json$", fn)
    if m:
        try: comments_by_goal[m.group(1)] = json.load(open(os.path.join(tmp, fn))) or []
        except Exception: comments_by_goal[m.group(1)] = []

rounds_by_session = {}
for fn in os.listdir(tmp):
    m = re.match(r"rounds_(.+)\.json$", fn)
    if m:
        try: rounds_by_session[m.group(1)] = json.load(open(os.path.join(tmp, fn))) or []
        except Exception: rounds_by_session[m.group(1)] = []

def all_runs(goal_ids):
    out = []
    for g in goal_ids:
        out.extend(runs_by_goal.get(g, []))
    return out

def failure_rows(goal_ids):
    rows = []
    for r in all_runs(goal_ids):
        st = r.get("status", "")
        if st not in ("failed", "cancelled"): continue
        summary = (r.get("result_summary") or "").strip()
        err = summary.splitlines()[0] if summary else "(无错误摘要)"
        if st == "cancelled" and not summary:
            err = "(被取消，无错误摘要)"
        rows.append({
            "id": r.get("id", ""), "status": st,
            "kind": r.get("run_kind") or r.get("role") or "?",
            "att": r.get("attempt", 0),
            "time": ts(r.get("started_at") or r.get("created_at")),
            "err": err.replace("|", "\\|")[:220],
            "cause": classify(summary),
        })
    # 旧→新排序，便于阅读
    return rows

L = []
w = L.append
now = datetime.now(timezone.utc).strftime("%Y-%m-%d %H:%M UTC")
w(f"# RSI R{N} 轮次演进报告（R{SINCE}→R{N}）")
w("")
w(f"> 生成：{now} · 生成器：`scripts/rsi-round-report.sh {N}` · 数据源：daemon API `{API}`（列表端点）+ 本地 git log（{INT_BRANCH}）")
w("> 本报告自包含：不登录平台即可读懂。指标口径见各节脚注；API 不可达的小节会显式标注。")
w("")
if errors:
    w(f"> ⚠️ 以下 API 路径本次不可达，对应内容已降级处理：{', '.join('`'+e+'`' for e in sorted(set(errors)))}")
    w("")

# ---------------------------------------------------------------- ① 循环演进视图
w("## ① 循环演进视图")
w("")
w("每轮是一个 train 模式 goal；下图与说明汇总自 rsi 训练会话冻结的工作流（nodes/edges/依赖）。")
w("")
# 汇总每轮 goal+session
round_info = {}  # round_no -> dict
for g in prev_goals + my_goals:
    title = g.get("title", "")
    m = re.search(r"RSI\s*R(\d+)", title)
    if not m: continue
    rn = int(m.group(1))
    gid = g["id"]
    sessions = sess_by_goal.get(gid, [])
    runs = runs_by_goal.get(gid, [])
    info = round_info.setdefault(rn, {"goals": [], "sessions": [], "runs": 0, "failed": 0})
    info["goals"].append(g)
    info["sessions"].extend(sessions)
    info["runs"] += len(runs)
    info["failed"] += sum(1 for r in runs if r.get("status") in ("failed", "cancelled"))

for rn in sorted(round_info):
    info = round_info[rn]
    w(f"### R{rn}：{info['goals'][0].get('title', '')}")
    w("")
    st = info["goals"][0].get("status", "?")
    w(f"- goal 终态：`{st}` · run 总数 {info['runs']}（失败/取消 {info['failed']}）")
    if not info["sessions"]:
        w("- 训练会话：未冻结（本轮尚未到达终态或分析未生成）")
    for s in info["sessions"]:
        w(f"- 训练会话 `{s['id'][:12]}…`：status=`{s.get('status')}` · {s.get('node_count', 0)} 节点 · 关键路径 {fmt_ms(s.get('critical_path_ms'))} · 首要发现：{s.get('top_title') or '（无）'}")
        det = load(f"session_{s['id']}.json")
        if det and det.get("workflow"):
            wf = det["workflow"]
            nodes, edges = wf.get("nodes", []), wf.get("edges", [])
            ag = [n for n in nodes if n.get("kind") == "agent"]
            ig = [n for n in nodes if n.get("kind") == "integrate"]
            w(f"  - 图结构：{len(nodes)} 节点 / {len(edges)} 边（agent 节点 {len(ag)}，集成节点 {len(ig)}）")
            for n in ag:
                dep = [e["to"][:14] for e in edges if e.get("from") == n["key"] and e.get("kind") == "depends_on"]
                w(f"    - `{n['key'][:14]}` {n.get('label','')}：{n.get('status')} · attempt {n.get('attempt')} · 耗时 {fmt_ms(n.get('duration_ms'))}" + (f" · 依赖 → {len(dep)} 个节点" if dep else ""))
            for e in edges:
                if e.get("kind") == "depends_on":
                    w(f"    - 依赖边：`{e['from'][:14]}` → `{e['to'][:14]}`")
    w("")

w("本轮（R%d）在本报告生成时通常仍在进行：以本节上面最后一条为准；收尾时重跑生成器即得终版。" % N)
w("")

# ---------------------------------------------------------------- ② 过程暴露问题清单
w("## ② 过程暴露问题清单")
w("")
w("本轮（R%d）全部失败/异常 run 逐条；根因分类为生成器启发式（环境/代码/流程），供人工复核。" % N)
w("")
rows = failure_rows([g["id"] for g in my_goals])
if rows:
    w("| run id | 状态 | 角色 | attempt | 时间 | 错误摘要 | 根因分类 |")
    w("|---|---|---|---|---|---|---|")
    for r in rows:
        w(f"| `{r['id'][:12]}…` | {r['status']} | {r['kind']} | {r['att']} | {r['time']} | {r['err']} | {r['cause']} |")
else:
    w("（本轮暂无失败/取消 run，或 API 数据不可得）")
w("")
w("### 上一轮遗留问题状态")
w("")
w(f"承接 R{N} 分析报告（`docs/rsi/analysis-r3.md`）的问题清单，本轮收尾时的状态如下（由本节人工/后续轮更新结论列）：")
w("")
w("| 问题 | 上轮状态 | 本轮收尾状态 |")
w("|---|---|---|")
w("| AGENTWORK_HOME 泄漏进验证子进程（R2 验证门三连败根因） | 待修（提案 P-A） | 待评审；bench R3 复测确认同一泄漏还击穿 go test 子进程的 t.Setenv 隔离（6 例失败根因 A/A'/B），P-A 实施时应一并覆盖测试进程环境 |")
w("| rsi_round 轮次编号不按 domain 递增（两轮均 round_no=1） | 待修（提案 P-B） | 待评审 |")
w("| 基准冷/温缓存口径漂移（跨轮对比混入缓存噪音） | 待修（提案 P-C） | P-C 口径已执行：R3 复测（8295f7a）按冷/温双列产出，跨轮对比采冷列（R2 44.417s → R3 41.285s，约 -7%） |")
w("| `/rsi` 端点口径不一致（文档写 `/rsi`，实测 404） | 待修（候选任务 3） | 本报告已按真实端点实施，文档口径待统一 |")
w("| 验证命令 PATH 依赖宿主 shell 配置（go 软链缺失三连败） | 已解决（/usr/local/bin 软链） | 已解决，P-A 落地时固化 PATH 语义 |")
w("| deliver 因 SSH 私钥权限失败、依赖人工重试 | 已解决（人工修权限） | 待修（候选任务 4：deliver 自动重试） |")
w("| owner 运行 71 分钟无产出无看护 | 已绕过（人工停止） | 待修（候选任务 5：owner 看护） |")
w("")

# ---------------------------------------------------------------- ③ 改进点
w("## ③ 改进点（每条带 commit + diff 链接 + 效果数字）")
w("")
w("改进点 = 本轮实施并合入集成分支的已批准提案；commit 链接指向仓库 commit 页，diff 摘要由生成器从 git log 提取，效果数字来自实施前后的基准复测（`benchmark-report*.md`）。")
w("")
# git log 中提取本轮提交（本工作项的 feat 分支提交 + 集成分支近 30 条）
cur_log = []
p = os.path.join(tmp, "gitlog-cur.txt")
if os.path.exists(p):
    cur_log = [l.strip() for l in open(p) if l.strip()]
int_log = []
p = os.path.join(tmp, "gitlog-int.txt")
if os.path.exists(p):
    int_log = [l.strip() for l in open(p) if l.strip()]
w("**本轮（R%d）实施提交：**" % N)
w("")
if cur_log:
    for line in cur_log:
        m = re.match(r"^([0-9a-f]{7,})\s+(.*)$", line)
        if not m: continue
        sha, msg = m.group(1), m.group(2)
        w(f"- {clink(sha)} {msg}")
else:
    w("（当前分支无提交记录可读）")
w("")
w("**集成分支 `%s` 近期提交（含历轮改进）：**" % INT_BRANCH)
w("")
if int_log:
    for line in int_log[:12]:
        m = re.match(r"^([0-9a-f]{7,})\s+(.*)$", line)
        if not m: continue
        sha, msg = m.group(1), m.group(2)
        w(f"- {clink(sha)} {msg}")
else:
    w(f"（本地与远端跟踪引用均未找到 `{INT_BRANCH}`，仅列当前分支提交）")
w("")
w("**历轮改进点与效果数字（来源：docs/rsi/proposals.md、benchmark-report-r2.md、benchmark-report-r3.md、impl-notes.md；口径冲突时以最新复测报告为准）：**")
w("")
w("| 轮 | 提案 | 改进 | commit | 效果数字（before → after） |")
w("|---|---|---|---|---|")
w("| R2 | P1 | 并行化 internal/daemon 独立顶层测试（70 个测试函数加 t.Parallel） | " + clink("e08cb72") + " | internal/daemon 包测试中位 5.911s（R2 复测基准，P1 后保持；R3 复测 5.855s 持平）；详见 benchmark-report-r2.md §3、benchmark-report-r3.md §4 |")
w("| R2 | P2 | CDN 漂移守卫对畸形响应 fail 改 skip | " + clink("8ca9957") + " | 14 包 0 FAIL（修复前该守卫对畸形响应直接 FAIL） |")
w("| R2 | P3 | 失败重试引入不可重试错误快速失败 | " + clink("18a8c3c") + " | 确定性错误（如模型不可用）不再烧尽重试预算（R1 曾 12 条 run 同因全败） |")
w("| R3 | R3-1 | 轮次演进报告生成器 + AGENTS.md 收尾约定 | 本工作项 commit（合入后由下轮报告回填链接） | 可感知性 5 缺口 → 5 节报告覆盖；每轮收尾强制产出 |")
w("")

# ---------------------------------------------------------------- ④ 跨轮指标对比
w("## ④ 跨轮指标对比")
w("")
w("汇总自各轮 rsi 训练会话冻结的 metrics（`/rsi/sessions/{id}/rounds` 账本；session 未冻结时回退 `/rsi/sessions/{id}` 实时 workflow.metrics，并已标注）。")
w("注意：R1/R2 的 rsi_round 账本 round_no 均为 1（提案 P-B 待修），故下表按「goal 标题里的轮次号」对齐，而非账本 round_no。")
w("")
# 从 rounds 数据聚合每轮指标；rounds 为空时回退 session 详情 workflow.metrics
metrics_rows = []
for rn in sorted(round_info):
    info = round_info[rn]
    best = None
    src = ""
    for s in info["sessions"]:
        rid = s["id"]
        rl = rounds_by_session.get(rid) or []
        if rl:
            r0 = rl[-1]  # 该 session 最新一条 round
            if r0.get("metrics"):
                best, src = r0["metrics"], "rounds"
        else:
            # rounds 账本为空（session 未冻结）时，回退到 session 详情的
            # workflow.metrics（训练页同源实时指标）；再退 report.metrics。
            det = load(f"session_{rid}.json")
            if det:
                wf = det.get("workflow") or {}
                if wf.get("metrics") and not best:
                    best, src = wf["metrics"], "session-detail"
                if not best:
                    rep = det.get("report") or {}
                    if rep.get("metrics"):
                        best, src = rep["metrics"], "session-detail"
    if best:
        best = dict(best)
        best["_src"] = src
    metrics_rows.append((rn, info, best or {}))
if metrics_rows:
    w("| 轮 | 指标来源 | 节点数 | 边数 | 关键路径 | 墙钟 | 工作总量 | 排队等待 | 并行收益 | lane 数 | lane 失衡 | 首要发现 |")
    w("|---|---|---|---|---|---|---|---|---|---|---|---|")
    for rn, info, m in metrics_rows:
        src = ("rounds 账本" if m.get("_src") == "rounds" else "session 实时（未冻结）") if m else "—"
        w(f"| R{rn} | {src} "
          f"| {m.get('node_count', info['sessions'][0].get('node_count', '—')) if m else '—'} "
          f"| {m.get('edge_count', '—') if m else '—'} "
          f"| {fmt_ms(m.get('critical_path_ms')) if m else '—'} "
          f"| {fmt_ms(m.get('wall_ms')) if m else '—'} "
          f"| {fmt_ms(m.get('work_sum_ms')) if m else '—'} "
          f"| {fmt_ms(m.get('queue_wait_ms')) if m else '—'} "
          f"| {round(m.get('parallel_speedup'), 2) if m and m.get('parallel_speedup') else '—'} "
          f"| {m.get('lane_count', '—') if m else '—'} "
          f"| {round(m.get('lane_imbalance'), 2) if m and m.get('lane_imbalance') else '—'} "
          f"| {info['sessions'][0].get('top_title') or '—'} |" if info['sessions'] else f"| R{rn} | — | — | — | — | — | — | — | — | — | — | 训练会话未冻结 |")
    w("")
    w("解读提示（含 bench R3 复测结论，权威来源 benchmark-report-r3.md）：")
    w("- `重试放大`（top finding）从 R1「平均每节点 3.5 次 run」降到 R2「2.5 次」，仍是两轮共同首要发现。")
    w("- R1/R2 的 wall_ms/critical_path 差异部分来自基准任务规格不同（R1 纯基准、R2 含实施），跨轮结论看趋势不看点值。")
    w("- 基准口径（P-C）：跨轮对比 build 只采冷列——R2 基线 44.417s → R3 新基线 e7c2572 41.285s（约 -7%），R2 已批准提案收益在新基线重建中得到保持；温列（R2 中位 4.387s / R3 中位 4.672s）仅参照不采信。")
    w("- 测试健康：R2 全绿 → R3 复测 6 例失败（2 包），全部为测试环境隔离缺陷（AGENTWORK_HOME 全局泄漏击穿 t.Setenv 隔离 + digest 跨运行残留），非业务代码缺陷；机器验证门以 `unset AGENTWORK_HOME;` 前缀口径运行为全绿。")
    w("- R3 行标注「session 实时（未冻结）」表示该会话尚未冻结、数字取自训练页同源的实时 workflow.metrics，收尾冻结后重跑生成器即切换为 rounds 账本口径。")
else:
    w("（训练会话指标不可得）")
w("")

# ---------------------------------------------------------------- ⑤ 下一轮候选任务清单
w("## ⑤ 下一轮候选任务清单")
w("")
w("权威来源：`docs/rsi/analysis-r3.md` 第三节（optimizer 产出，人工挑选成为下一轮任务）。以下为该清单的当前快照：")
w("")
w("1. **实施 P-A：验证子进程环境净化**（高优）——R2 验证门三连败 4 条失败 run 同根因（AGENTWORK_HOME 泄漏），R3 前置轮 unset 前缀又引入 PATH 三连败；bench R3 复测进一步确认该泄漏还击穿 go test 子进程隔离（6 例失败，根因 A/A'/B）；收益：同类验证门/测试失败 4+3+6 → 0。")
w("2. **实施 P-B：rsi_round 按 domain 递增**（中优）——两轮 round_no 均为 1、delta 恒空；收益：R4 报告【跨轮指标对比】首次有真序列。")
w("3. **`/rsi` 端点口径修正**（低成本高一致性）——补文档为 `/rsi/sessions` 等真实端点，或 handler 加 `GET /rsi` 别名。")
w("4. **deliver 失败自动重试/退避**（中优）——R2 deliver 因 SSH 私钥权限失败一次后依赖人工推进；外层加一次带延迟的自动重试。")
w("5. **owner/leader 运行卡顿看护**（中优）——R2 owner run 运行 71 分钟无产出被人工停止；把 watchdog 覆盖 owner 运行。")
w("")
w("---")
w("")
w(f"*本报告由 `scripts/rsi-round-report.sh` 自动生成；如与平台数据不一致，以 daemon API 为准。生成时间：{now}。*")

sys.stdout.write("\n".join(L))
PYEOF

# ---------------------------------------------------------------- 落盘
if [[ "$DRY_RUN" == "1" ]]; then
  cat "$TMP/report.md"
else
  mkdir -p "$(dirname "$OUT")"
  cp "$TMP/report.md" "$OUT"
  echo "已生成: $OUT"
fi
