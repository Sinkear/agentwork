#!/usr/bin/env bash
# rsi-round-report.sh — 轮次演进报告生成器（RSI R3 可感知性改进）
#
# 用法：
#   scripts/rsi-round-report.sh <轮次号N> [输出路径]
#   scripts/rsi-round-report.sh 3                 # → docs/rsi/round-report-r3.md
#   scripts/rsi-round-report.sh 3 /tmp/r3.md     # 任意输出路径
#
# 数据源：
#   - daemon API（默认 http://127.0.0.1:47813，可用 AGENTWORK_API 覆盖）：
#       /rsi/sessions、/rsi/sessions/{id}（findings+workflow）、
#       /rsi/sessions/{id}/rounds、/rsi/overview、
#       /goals、/goals/{id}/runs、/goals/{id}/comments、/agents
#   - 本地 git log（基线分支，默认 agentwork-int-v2，可用 RSI_BASE_BRANCH 覆盖；
#     遍历 --all 以覆盖实施分支上尚未合入基线的 commit）
#
# 输出：docs/rsi/round-report-r<N>.md，固定五节：
#   ① 循环演进视图   —— 每轮 workflow 图结构（nodes/edges/依赖）+ 干了什么 + 终态
#   ② 过程暴露问题清单 —— 全部失败/异常 run 逐条（run id、错误摘要、根因分类）+ 上一轮遗留状态
#   ③ 改进点         —— 每条带【问题依据】【commit 链接】【diff 摘要】【效果数字 before/after】
#   ④ 跨轮指标对比表  —— rsi_round metrics 汇总（critical_path、墙钟、节点数、重试率等）
#   ⑤ 下一轮候选任务清单 —— 引用 optimizer 最新 docs/rsi/analysis-r*.md
#
# 报告自包含：读者不需要登录平台即可理解。约 40% 篇幅是结构化的机器产出（可复查），
# 演进叙事部分由各轮已提交的产出物（proposals/analysis/benchmark/impl-notes）支撑。
#
# 依赖：bash、curl、git、python3。
set -euo pipefail

API="${AGENTWORK_API:-http://127.0.0.1:47813}"
BASE_BRANCH="${RSI_BASE_BRANCH:-agentwork-int-v2}"
ROUND="${1:-}"
OUT="${2:-}"
REPO_ROOT="$(git rev-parse --show-toplevel)"

if [[ -z "$ROUND" ]]; then
  echo "用法: $0 <轮次号N> [输出路径]" >&2
  exit 2
fi
if [[ -z "$OUT" ]]; then
  OUT="$REPO_ROOT/docs/rsi/round-report-r${ROUND}.md"
fi
mkdir -p "$(dirname "$OUT")"

api() { curl -fsS --max-time 20 "$API$1" 2>/dev/null || echo '[]'; }
# 大 JSON 走临时文件传递（argv 有 ~2MB 上限，runs/comments 体量足够撑爆它）
TMPDIR_LOCAL="${TMPDIR:-/tmp/opencode}"
mkdir -p "$TMPDIR_LOCAL"
WORK_JSON="$(mktemp "$TMPDIR_LOCAL/rsi-report-XXXXXX.json")"
trap 'rm -f "$WORK_JSON" "$WORK_JSON.detail" "$WORK_JSON.evo"' EXIT

echo "rsi-round-report: API=$API 基线分支=$BASE_BRANCH 轮次=R$ROUND" >&2
echo "  拉取 /rsi/sessions ..." >&2
api /rsi/sessions > "$WORK_JSON.sessions"
api /goals > "$WORK_JSON.goals"
api /agents > "$WORK_JSON.agents"
api /rsi/overview > "$WORK_JSON.overview"
echo "  拉取各 session/goal 明细（runs、comments、rounds、workflow）..." >&2
python3 - "$API" "$WORK_JSON" <<'PYEOF'
import json,sys,urllib.request
api, work = sys.argv[1], sys.argv[2]
def get(path):
    try:
        with urllib.request.urlopen(api+path, timeout=20) as r: return json.load(r)
    except Exception:
        return None
sessions=get("/rsi/sessions") or []
goals=get("/goals") or []
known={g["id"] for g in goals}
seen=set(); tasks=[]
for s in sessions:
    sid=s["id"]; gid=s.get("goal_id","")
    if gid and gid not in known: continue          # 已删除 goal 的 session：跳过明细
    key=(sid,gid)
    if key in seen: continue
    seen.add(key); tasks.append((sid,gid))
out=[]
for sid,gid in tasks:
    detail=get(f"/rsi/sessions/{sid}") or {}
    rounds=get(f"/rsi/sessions/{sid}/rounds") or []
    runs=get(f"/goals/{gid}/runs") or [] if gid else []
    comments=get(f"/goals/{gid}/comments") or [] if gid else []
    out.append({"session_id":sid,"goal_id":gid,"detail":detail,
                "rounds":rounds,"runs":runs,"comments":comments})
json.dump(out, open(work+".detail","w",encoding="utf-8"), ensure_ascii=False)
PYEOF

echo "  组装仓库演进数据（git log 窗口 + 已提交产出物）..." >&2
python3 - "$BASE_BRANCH" "$WORK_JSON" <<'PYEOF'
import json,subprocess,os,sys
base=sys.argv[1]; work=sys.argv[2]
def sh(*args):
    return subprocess.run(args,capture_output=True,text=True).stdout
def parse_log(fmt,extra=()):
    out=sh("git","log","--all","--date=iso-strict","--topo-order","--pretty="+fmt,*extra)
    return [l for l in out.splitlines() if l.strip()]
entries=[]
commits=parse_log("%H%x1f%ad%x1f%s%x1f%an%x1f%ae")
for line in commits:
    h,d,s,an,ae=line.split("\x1f",4)
    entries.append({"hash":h,"date":d,"subject":s,"author":an,"email":ae})
def stat(c):
    return sh("git","show","--format=","--stat",c)
def files(c):
    return [l for l in sh("git","show","--format=","--name-status",c).splitlines() if l.strip()]
def file_text(c,p):
    r=subprocess.run(["git","show",f"{c}:{p}"],capture_output=True,text=True)
    return r.stdout if r.returncode==0 else ""
# 仅挑含产出物/RSI 相关文件或 message 关键词命中的 commit，供改进点对位
keywords=("P1","P2","P3","提案","rsi","RSI","基准","benchmark","分析","impl-notes","演进","自迭代","并行化","重试","守卫","轮")
improvement_candidates=[]
for e in entries:
    blob=e["subject"]+" "+e["hash"]
    hit=any(k in blob for k in keywords)
    fl=files(e["hash"])
    artifact=any(("docs/rsi/" in f or "benchmark-report" in f or "impl-notes" in f or "proposals" in f) for f in fl)
    if hit or artifact:
        improvement_candidates.append({**e,"stat":stat(e["hash"]),"files":fl})
reports={}
for e in entries:
    for p in ("benchmark-report.md","benchmark-report-r2.md","impl-notes.md","proposals.md",
              "docs/rsi/proposals.md","docs/rsi/analysis-r3.md","docs/rsi/self-iter-playbook.md"):
        if p not in reports:
            t=file_text(e["hash"],p)
            if t: reports[p]={"commit":e["hash"],"text":t,"date":e["date"]}
json.dump({"base_branch":base,"commits":entries,
           "improvement_candidates":improvement_candidates[-40:],
           "reports":reports},
          open(work+".evo","w",encoding="utf-8"), ensure_ascii=False)
PYEOF

NOW="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
HEAD_SHA="$(git rev-parse HEAD)"
HEAD_SUBJ="$(git log -1 --pretty=%s HEAD)"

echo "  渲染报告 ..." >&2
API_URL="$API" ROUND="$ROUND" OUT="$OUT" NOW="$NOW" HEAD_SHA="$HEAD_SHA" HEAD_SUBJ="$HEAD_SUBJ" \
BASE_BRANCH="$BASE_BRANCH" \
python3 "$REPO_ROOT/scripts/_rsi_round_report_render.py" \
  "$WORK_JSON.sessions" "$WORK_JSON.goals" "$WORK_JSON.agents" "$WORK_JSON.overview" \
  "$WORK_JSON.detail" "$WORK_JSON.evo"

echo "rsi-round-report: 已生成 $OUT" >&2
