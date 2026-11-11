#!/usr/bin/env python3
"""_rsi_round_report_render.py — rsi-round-report.sh 的报告渲染器（内部，勿直接调用）。

argv: sessions goals agents overview detail evolution（均为 JSON 字符串）
env : ROUND OUT NOW HEAD_SHA HEAD_SUBJ API_URL BASE_BRANCH

五节固定结构（① 循环演进视图 / ② 过程暴露问题清单 / ③ 改进点 / ④ 跨轮指标对比 / ⑤ 下一轮候选任务清单），
外加固定标题、脚注与「数字与 API 一致」的口径说明。所有机器可查的部分（ID、数字、错误摘要）
直接取自 daemon API 与 git，人工叙事引用各轮已提交产出物。
"""
import json
import os
import re
import sys
from collections import OrderedDict

ROUND = os.environ.get("ROUND", "?")
OUT = os.environ.get("OUT", "-")
NOW = os.environ.get("NOW", "")
HEAD_SHA = os.environ.get("HEAD_SHA", "")
HEAD_SUBJ = os.environ.get("HEAD_SUBJ", "")
API_URL = os.environ.get("API_URL", "http://127.0.0.1:47813")
BASE_BRANCH = os.environ.get("BASE_BRANCH", "agentwork-int-v2")

def _load(path):
    with open(path, encoding="utf-8") as f:
        return json.load(f)


sessions = _load(sys.argv[1])
goals = _load(sys.argv[2])
agents = _load(sys.argv[3])
overview = _load(sys.argv[4])
detail = _load(sys.argv[5])
evo = _load(sys.argv[6])


def short(h, n=7):
    return (h or "")[:n]


def fmt_ms(ms):
    try:
        ms = int(ms or 0)
    except (TypeError, ValueError):
        ms = 0
    if ms <= 0:
        return "0s"
    if ms < 1000:
        return f"{ms}ms"
    s = ms / 1000.0
    if s < 90:
        return f"{s:.1f}s"
    m, sec = divmod(s, 60)
    if m < 90:
        return f"{int(m)}m{int(sec)}s"
    h, m = divmod(int(m), 60)
    return f"{h}h{m}m"


def speed_of(rd):
    """并行收益：优先 session 冻结值，其次 workflow 实时值；无值返回 None。"""
    v = rd["session"].get("parallel_speedup")
    if not v:
        v = metrics_of(rd).get("parallel_speedup")
    return float(v) if v else None


def agent_name(aid):
    for a in agents:
        if a.get("id") == aid:
            return a.get("name") or aid[:8]
    return (aid or "")[:8] or "—"


def esc(text, limit=220):
    t = (text or "").strip().replace("\n", " ")
    t = re.sub(r"\s+", " ", t)
    return (t[: limit - 1] + "…") if len(t) > limit else t


def parse_ts(ts):
    try:
        return datetime_from_iso(ts)
    except Exception:
        return None


def datetime_from_iso(ts):
    from datetime import datetime

    return datetime.fromisoformat(ts.replace("Z", "+00:00"))


def dur_between(a, b):
    try:
        return fmt_ms(int((datetime_from_iso(b) - datetime_from_iso(a)).total_seconds() * 1000))
    except Exception:
        return "—"


goal_by_id = {g["id"]: g for g in goals}

# ── 会话 ↔ 轮次匹配 ──────────────────────────────────────────────────────────
# rsi_round 的 round_no 目前按 session 自增（历史两轮均为 1，见问题 #13/提案 P-B），
# 真实轮次号从 goal 标题提取（「RSI R1…」「R2」「R3」）。同一标题可能对应多个 goal
# （轮中重建/重开），按 started_at 新者优先取每轮的主 session，其余会话仍完整展示
# 在图结构小节（前置轮的失败过程是②的问题证据，不丢弃）。
round_re = re.compile(r"[Rr]\s*(\d+)")


def round_of(s):
    g = goal_by_id.get(s.get("goal_id"))
    title = (g or {}).get("title") or s.get("goal_title") or ""
    m = round_re.search(title)
    return int(m.group(1)) if m else None


det_by_session = {d["session_id"]: d for d in detail}
det_by_goal = {d.get("goal_id"): d for d in detail if d.get("goal_id")}

known_sessions = [s for s in sessions if s.get("goal_id") in goal_by_id]
known_sessions.sort(key=lambda s: (round_of(s) is None, round_of(s) or 0, s.get("started_at") or ""))
for i, s in enumerate(known_sessions):
    r = round_of(s)
    s["_round"] = r if r is not None else i + 1
    s["_primary"] = False
primary_by_round = {}
for s in known_sessions:
    key = (s.get("status") in ("ready",), s.get("finished_at") or s.get("sealed_at") or "")
    if s["_round"] not in primary_by_round or key > primary_by_round[s["_round"]][0]:
        primary_by_round[s["_round"]] = (key, s)
for _, s in primary_by_round.values():
    s["_primary"] = True

rounds_data = []
for s in known_sessions:
    d = det_by_session.get(s["id"]) or det_by_goal.get(s.get("goal_id"), {})
    wf = ((d.get("detail") or {}).get("workflow")) or {}
    runs = d.get("runs") or []
    rounds_data.append(
        {
            "session": s,
            "goal": goal_by_id.get(s.get("goal_id"), {}),
            "workflow": wf,
            "findings": ((d.get("detail") or {}).get("findings")) or [],
            "round_ledger": d.get("rounds") or [],
            "runs": runs,
            "comments": d.get("comments") or [],
        }
    )

# 同轮多 goal 的 run 合并去重（保留全部失败证据；重跑的 run 属于同一轮的问题清单）
seen_run = set()
for rd in rounds_data:
    uniq = []
    for r in rd["runs"]:
        if r["id"] in seen_run:
            continue
        seen_run.add(r["id"])
        uniq.append(r)
    rd["runs"] = uniq

# ── runs 分类（②的机器部分）────────────────────────────────────────────────
CATEGORY_RULES = [
    ("模型不可用", r"model unavailable|model.*not.*found|模型不可用"),
    ("机器离线/连接", r"machine offline|connect|heartbeat|peer|offline|连接"),
    ("验证门失败(环境串扰)", r"verification failed|does not appear to be a git repository|bare\.git|verify failed"),
    ("git/fetch/worktree", r"git[:：]|fetch|worktree|push|merge"),
    ("平台取消/停止", r"cancelled by platform|stopped|cancel"),
    ("权限/凭据", r"permission|publickey|private key|credential|ssh"),
]
GENERIC_FAIL = ("其他", r".*")


def categorize(summary):
    text = (summary or "").strip()
    if not text:
        return "（无错误摘要）"
    low = text.lower()
    for name, pat in CATEGORY_RULES:
        if re.search(pat, low):
            return name
    return "其他"


def classify_root(name):
    if name in ("模型不可用", "机器离线/连接", "权限/凭据"):
        return "环境"
    if name.startswith("验证门失败"):
        return "环境/代码交界（验证子进程环境泄漏）"
    if name in ("平台取消/停止",):
        return "流程"
    if name.startswith("git/"):
        return "环境/流程"
    return "代码/流程"


def runs_table(rows):
    if not rows:
        return ["_本轮无失败/异常 run。_", ""]
    out = [
        "| # | run id | 归属 | attempt | 状态 | 耗时 | 错误摘要 | 根因分类 |",
        "|---|--------|------|---------|------|------|----------|----------|",
    ]
    for i, r in enumerate(rows, 1):
        agent = agent_name(r.get("agent_id"))
        dur = dur_between(r.get("started_at") or r.get("created_at"), r.get("finished_at") or "")
        out.append(
            f"| {i} | `{short(r.get('id'), 8)}` | {agent} | {r.get('attempt') or 1} "
            f"| {r.get('status')} | {dur} | {esc(categorize(r.get('result_summary')), 60)}：{esc(r.get('result_summary'), 120)} "
            f"| {classify_root(categorize(r.get('result_summary')))} |"
        )
    out.append("")
    return out


# ── ① 循环演进视图 ───────────────────────────────────────────────────────────
def node_line(n):
    label = esc(n.get("label"), 46)
    kind = n.get("kind")
    status = n.get("status")
    att = n.get("attempt") or 0
    dur = fmt_ms(n.get("duration_ms"))
    who = agent_name(n.get("assignee_id")) if n.get("assignee_id") else ""
    who = f"（{who}）" if who and kind == "agent" else ""
    att_s = f"，重试 {att - 1} 次" if att and att > 1 else ""
    return f"  - `{short(n.get('key'), 14)}` **[{kind}]** {label}{who} — 终态 `{status}`，耗时 {dur}{att_s}"


def edge_summary(edges):
    dep = [e for e in edges if e.get("kind") == "depends_on"]
    created = [e for e in edges if e.get("kind") == "created"]
    integrated = [e for e in edges if e.get("kind") == "integrated"]
    parts = []
    if created:
        parts.append(f"goal→子任务派发边 {len(created)} 条")
    if dep:
        parts.append(f"显式依赖边（depends_on）{len(dep)} 条")
    if integrated:
        parts.append(f"集成边 {len(integrated)} 条")
    other = len(edges) - len(dep) - len(created) - len(integrated)
    if other > 0:
        parts.append(f"时间推断/其他边 {other} 条")
    return "、".join(parts) if parts else f"共 {len(edges)} 条"


def evolution_section():
    L = []
    L.append("## ① 循环演进视图")
    L.append("")
    L.append(
        "> 阅读方式：每轮 = 一个 train 模式 goal，终态时平台把编排过程冻结成 workflow 图"
        "（agent / verify / review / consult / integrate 节点 + 显式依赖与时间推断边）。"
        "下表与图结构均从 daemon API 实拉，ID 可在平台复查；未登录平台的读者按文字即可理解全貌。"
        "同一轮出现多行 = 该轮存在多个 goal（重建/重开），主行取已出报告的终态 session，"
        "其余行保留以呈现前置失败（它们是②问题清单的证据）。"
    )
    L.append("")
    L.append("### 轮次总览")
    L.append("")
    L.append("| 轮 | goal（标题） | 终态 | 周期 | workflow 规模 | 干了什么（一句话） |")
    L.append("|---|---|---|---|---|---|")
    for rd in rounds_data:
        s, g = rd["session"], rd["goal"]
        nodes = rd["workflow"].get("nodes") or []
        edges = rd["workflow"].get("edges") or []
        scale = f"{len(nodes)} 节点 / {len(edges)} 边"
        what = WHAT_BY_ROUND.get(s["_round"], esc(g.get("title") or s.get("goal_title"), 60))
        start = (s.get("started_at") or g.get("created_at") or "")[:10]
        end = (s.get("finished_at") or s.get("sealed_at") or "")[:10] or "进行中"
        L.append(
            f"| R{s['_round']} | {esc(g.get('title') or s.get('goal_title'), 40)}（`{short(g.get('id'), 8)}`） "
            f"| `{s.get('status')}` | {start} → {end} | {scale} | {esc(what, 80)} |"
        )
    L.append("")
    for rd in rounds_data:
        s, g, wf = rd["session"], rd["goal"], rd["workflow"]
        nodes = wf.get("nodes") or []
        edges = wf.get("edges") or []
        m = ((rd["round_ledger"] or [{}])[0].get("metrics")) or wf.get("metrics") or {}
        prim = "主 session" if s.get("_primary") else "前置/重建 session"
        L.append(f"### R{s['_round']} 图结构（session `{short(s.get('id'), 8)}`，goal `{short(g.get('id'), 8)}`，{prim}）")
        L.append("")
        L.append(f"- **图规模**：{len(nodes)} 节点 / {len(edges)} 边；{edge_summary(edges)}")
        L.append(
            f"- **结构指标**：墙钟 {fmt_ms(m.get('wall_ms'))}，关键路径 {fmt_ms(m.get('critical_path_ms'))}，"
            f"工作总量 {fmt_ms(m.get('work_sum_ms'))}，并行收益 {fmt_speed(speed_of(rd))}，"
            f"排队等待 {fmt_ms(m.get('queue_wait_ms'))}，串行损失 {fmt_ms(m.get('serialization_loss_ms'))}，"
            f"重试率（Σattempts÷节点）{(m.get('retry_amplification') if m.get('retry_amplification') is not None else retry_amp_from_nodes(nodes))}"
        )
        if nodes:
            L.append("- **节点明细**（依赖即派发顺序；集成节点代表交付合入）：")
            L.extend(node_line(n) for n in nodes)
        else:
            L.append("- **节点明细**：本轮 session 尚未冻结（`status=waiting`），无图可展示。")
        if rd["findings"]:
            L.append("- **机器发现（findings）**：")
            for f in rd["findings"]:
                L.append(
                    f"  - [{f.get('severity')}] {f.get('title')}（score {f.get('score')}，{f.get('category')}）"
                    f"—— {esc(f.get('explanation'), 120)}"
                )
        L.append("")
    L.append(f"### 演进主线（R1 → R{max(s['_round'] for s in known_sessions)}）")
    L.append("")
    L.extend(EVOLUTION_NARRATIVE)
    L.append("")
    return L


# ── ② 过程暴露问题清单 ───────────────────────────────────────────────────────
def metrics_of(rd):
    """主指标来源：workflow 实时 metrics（进行中也有值）→ round 账本 → report。"""
    m = (rd.get("workflow") or {}).get("metrics") or {}
    if not m:
        m = ((rd.get("round_ledger") or [{}])[0].get("metrics")) or {}
    if not m:
        m = ((rd.get("detail") or {}).get("report") or {}).get("metrics") or {}
    if m and m.get("retry_amplification") is None:
        m = {**m, "retry_amplification": retry_amp_from_nodes((rd.get("workflow") or {}).get("nodes") or [])}
    return m or {}


def issues_section():
    L = []
    L.append("## ② 过程暴露问题清单")
    L.append("")
    L.append(
        "> 口径：**全部失败/异常 run 逐条列出**（数据源 `/goals/{id}/runs`，status ∈ failed/cancelled），"
        "不是指标汇总——每个失败都该有个去处。根因分类：环境 / 代码 / 流程。"
        "同一轮如有多个 goal（重建/重开），run 合并去重后一并呈现。"
    )
    L.append("")
    by_round = OrderedDict()
    for rd in rounds_data:
        by_round.setdefault(rd["session"]["_round"], []).append(rd)
    for rno, rds in by_round.items():
        bad = []
        total_ids = set()
        for rd in rds:
            for r in rd["runs"]:
                if r["id"] in total_ids:
                    continue
                total_ids.add(r["id"])
                if r.get("status") in ("failed", "cancelled"):
                    bad.append(r)
        total = len(total_ids)
        L.append(f"### R{rno}（{total} 条 run，其中失败/异常 {len(bad)} 条）")
        L.append("")
        L.extend(runs_table(bad))
        # 错误类别小计
        if bad:
            cats = OrderedDict()
            for r in bad:
                c = categorize(r.get("result_summary"))
                cats[c] = cats.get(c, 0) + 1
            L.append("错误类别小计：" + "，".join(f"{k} ×{v}" for k, v in cats.items()) + "。")
            L.append("")
    L.append("### 上一轮遗留问题状态（滚动跟踪）")
    L.append("")
    L.extend(CARRYOVER_STATUS)
    L.append("")
    return L


# ── ③ 改进点 ────────────────────────────────────────────────────────────────
def improvement_section():
    L = []
    L.append("## ③ 改进点（每条：问题依据 → commit → diff → 效果数字）")
    L.append("")
    L.append(
        "> commit 链接基于本仓库远程（`git@github.com:Sinkear/agentwork.git`，网页端把主机换为 github.com/Sinkear/agentwork）；"
        "效果数字均来自已提交的基准/实施报告原文，口径差异在第④节显式标注。"
        "R2 的三条提案各列两个 commit：原实施（agentwork-int 交付）与新基线 agentwork-int-v2 的语义化重放（历史重建，内容一致）。"
    )
    L.append("")
    for imp in IMPROVEMENTS:
        L.append(f"### 改进点 {imp['no']}：{imp['title']}")
        L.append("")
        L.append(f"- **【问题依据】** {imp['problem']}")
        commit_links = f"[`{short(imp['commit'], 7)}`]({imp['commit_url']})（原实施）"
        if imp.get("commit2"):
            commit_links += f" · [`{short(imp['commit2'], 7)}`]({_REPO_WEB}{imp['commit2']})（新基线重放）"
        L.append(f"- **【commit】** {commit_links} — {esc(imp['subject'], 100)}")
        if imp.get("diff_summary"):
            L.append("- **【diff 摘要】**")
            L.append("")
            L.append("  ```")
            L.extend(f"  {line}" for line in imp["diff_summary"])
            L.append("  ```")
            L.append("")
        L.append(f"- **【效果数字（before → after）】** {imp['effect']}")
        if imp.get("status_note"):
            L.append(f"- **状态**：{imp['status_note']}")
        L.append("")
    return L


# ── ④ 跨轮指标对比 ───────────────────────────────────────────────────────────
def fmt_speed(v):
    try:
        return f"{float(v):.2f}×"
    except (TypeError, ValueError):
        return "—"


def retry_amp_from_nodes(nodes):
    tot = sum((n.get("attempt") or 0) for n in nodes if n.get("kind") == "agent")
    cnt = sum(1 for n in nodes if n.get("kind") == "agent")
    return f"{tot / cnt:.2f}" if cnt else "—"


def metrics_section():
    L = []
    L.append("## ④ 跨轮指标对比")
    L.append("")
    L.append(
        "> 数据源：`rsi_round` 账本 / session metrics（daemon API 实拉）。"
        "⚠️ 口径注记：`round_no` 在本报告生成时按 session 自增（历史两轮均记 1，见问题清单 #13 与提案 P-B），"
        "下表「轮」列以 goal 标题中的轮次号为准；耗时类指标为平台冻结口径，与 bench 冷/温缓存口径（第③节）分属两把尺子，不可互比。"
        "R1/R2 为终态冻结值；R3 进行中，取实时重建 session 的图指标（交付后由 sg-REPORT 重跑生成器刷新为冻结值）。"
    )
    L.append("")
    by_round = OrderedDict()
    for rd in rounds_data:
        by_round.setdefault(rd["session"]["_round"], []).append(rd)
    cols = []
    for rno, rds in by_round.items():
        # 进行中的轮取有实时图数据的 session（重建 goal），已冻结轮取主 session
        live = next((rd for rd in rds if rd["session"].get("status") not in ("waiting",) and (rd["workflow"].get("nodes") or [])), None)
        prim = live or next((rd for rd in rds if rd["session"].get("_primary")), rds[-1])
        cols.append((rno, prim))
    L.append("| 指标 | " + " | ".join(f"R{rno}" for rno, _ in cols) + " |")
    L.append("|---|" + "---|" * len(cols))

    def cell(fn):
        return " | ".join(fn(rd) for _, rd in cols)

    rows = [
        ("goal 终态", lambda rd: f"`{rd['session'].get('status')}`（`{short(rd['goal'].get('id'), 8)}`）"),
        ("workflow 节点数", lambda rd: str(len(rd["workflow"].get("nodes") or []) or rd["session"].get("node_count") or 0)),
        ("workflow 边数", lambda rd: str(len(rd["workflow"].get("edges") or []) or 0)),
        ("关键路径 critical_path", lambda rd: fmt_ms(metrics_of(rd).get("critical_path_ms") or rd["session"].get("critical_path_ms"))),
        ("墙钟 wall", lambda rd: fmt_ms(metrics_of(rd).get("wall_ms"))),
        ("工作总量 work_sum", lambda rd: fmt_ms(metrics_of(rd).get("work_sum_ms"))),
        ("排队等待 queue_wait", lambda rd: fmt_ms(metrics_of(rd).get("queue_wait_ms"))),
        ("并行收益 parallel_speedup", lambda rd: fmt_speed(speed_of(rd))),
        ("串行损失 serialization_loss", lambda rd: fmt_ms(metrics_of(rd).get("serialization_loss_ms"))),
        ("重试率（Σattempts÷agent 节点）", lambda rd: str(retry_amp_from_nodes(rd["workflow"].get("nodes") or []))),
        ("机器发现 findings", lambda rd: str(rd["session"].get("finding_count") or 0)),
        ("首要发现", lambda rd: esc(rd["session"].get("top_title"), 40) or "—"),
        (
            "失败/异常 run 数",
            lambda rd: str(
                len({r["id"] for x in by_round[rd["session"]["_round"]] for r in x["runs"] if r.get("status") in ("failed", "cancelled")})
            ),
        ),
    ]
    for name, fn in rows:
        L.append(f"| {name} | {cell(fn)} |")
    L.append("")
    live_round = max(by_round)
    live_runs = len({
        r["id"] for rd in by_round[live_round] for r in rd["runs"]
        if r.get("status") in ("failed", "cancelled")
    })
    L.append(
        f"**平台侧趋势读法**：R1/R2 并行收益均为 1.00×（关键路径=工作总量，依赖声明驱动串行），R3 首次出现 >1 的并行度"
        f"（重建 goal 无 depends_on 串行门）；失败模式从「模型不可用」（R1，12 条）迁移到「验证门环境串扰」"
        f"（R2，4 条）再到「机器离线+派发层 127」（R3，本轮 {live_runs} 条失败/异常 run）——修一类、暴露下一类，"
        "这正是本报告每轮滚动的原因。"
    )
    L.append("")
    L.append("**bench 基准口径对照**（build/test 秒数，来自各轮已提交 benchmark 报告；自 R3 起冷/温双列、跨轮对比只采冷列——提案 P-C）")
    L.append("")
    L.append("| 轮 | 基线 commit | go build（冷列=采信） | go build（温列=参照） | go test（中位） | 失败包 | 口径注意 |")
    L.append("|---|---|---|---|---|---|---|")
    L.append("| R1 | `0bf354e`（复跑 v2 报告） | 未留冷态数据 | 3.9s（冷 40.6s 另测） | 单跑 11.3s | 1（internal/service） | 首跑含测试二进制编译（23.0s vs 复跑 14.5s）；按温列口径标注，不参与冷列对比 |")
    L.append("| R2 | `365e108` | 44.417s | 中位 4.387s | 三连中位 11.559s | **0** | 首次冷列记录；三连取中位；P1+P2 生效 |")
    L.append("| R3 | `e7c2572` | **41.285s**（-7.1% vs R2 冷列） | 中位 4.672s | 三连中位 13.173s（温） | 6 例 / 2 包 | **6 例失败全部为测试环境隔离缺陷**（AGENTWORK_HOME 泄漏 × 固定 ID 残留，非业务缺陷，见 benchmark-report-r3.md 第 6 节）；test +14% 主因 service 包 RSI 新代码负载与失败重试路径，修复隔离后口径才可与 R2 直接对比 |")
    L.append("")
    return L


# ── ⑤ 下一轮候选任务清单 ────────────────────────────────────────────────────
def next_section():
    L = []
    L.append("## ⑤ 下一轮候选任务清单")
    L.append("")
    src = NEXT_SOURCE
    if src:
        L.append(
            f"> 引用自 optimizer 最新产出 `{src['path']}`（commit `{short(src['commit'], 7)}`，"
            "人工评审挑选后成为下一轮任务）。"
        )
        L.append("")
        L.extend(src["lines"])
    else:
        L.append("_未找到 optimizer 的 docs/rsi/analysis-r*.md 产出，请人工补充。_")
    L.append("")
    L.append("**生成器自身给下一轮的候选**（本报告机制的可感知性缺口，供参考）：")
    L.append("")
    for item in GENERATOR_NEXT:
        L.append(f"- {item}")
    L.append("")
    return L


def footer():
    L = []
    L.append("---")
    L.append(
        f"*报告生成：scripts/rsi-round-report.sh（R{ROUND} 收尾产物，机器验证门前自检五节齐全）。 "
        f"数据快照 {NOW}；daemon API `{API_URL}`；git 基线分支 `{BASE_BRANCH}`，"
        f"生成时 HEAD `{short(HEAD_SHA, 7)}`（{esc(HEAD_SUBJ, 60)}）。*"
    )
    L.append(
        "*协作约定：每轮 goal 收尾前必须运行本脚本生成 `docs/rsi/round-report-r<N>.md` 并随交付提交（见 AGENTS.md「RSI 轮次演进报告协作约定」节）。*"
    )
    return L


# ── 静态叙事块（引用已提交产出物，保证自包含）───────────────────────────────
WHAT_BY_ROUND = {
    1: "建基线：bench 测出 build/test 基准（发现 1 失败包），optimizer 产出 3 条只读提案（P1 测试并行化 / P2 CDN 守卫 fail→skip / P3 不可重试快速失败），人工批准",
    2: "实施已批准的 P1+P2+P3（4 commit）+ bench 复测全绿（14 包 0 FAIL，test 中位 11.559s），交付合入 agentwork-int；验证门暴露 AGENTWORK_HOME 泄漏（4 条失败 run）",
    3: "可感知性改进：builder 实现本报告生成器并写入协作约定；optimizer 产出 R2/R3 全量失败 run 问题清单与 3 条新提案（P-A 环境净化 / P-B 轮次编号 / P-C 基准口径）；bench 复测维持基准连续性",
}

EVOLUTION_NARRATIVE = [
    "1. **R1（基准→提案）**：首轮在新集成基线 `0bf354e` 上建立「同一基准任务每轮复跑」的度量惯例。bench 报告"
    "（benchmark-report.md）暴露 1 个失败包（internal/service CDN 缺码守卫）与 12 条 `Model unavailable` 失败 run；"
    "optimizer 据此产出 3 条提案（proposals.md，全部【已批准】）。结构上单通道（speedup 1.00×），"
    "sg-O 被 depends_on(sg-B) 整段阻塞——这条「假依赖」本身成为 R2 分析对象。",
    "2. **R2（实施已批准提案）**：P1/P2/P3 落地（4 commit，每条独立 commit 便于单独 revert），bench 复测"
    "全绿：失败包 1→0、test 中位 11.559s（-50% 口径见第④节注记）、internal/daemon 包 -23%（P1 并行化）。"
    "同期暴露新失败模式：验证子进程继承 `AGENTWORK_HOME`，4 条 run 验证门三连败——改进生效的同时，"
    "下一类问题浮出水面（详见②③）。",
    "3. **R3（可感知性改进，本报告）**：针对 R2 收尾暴露的五个可感知性缺口（过程不可感知 / 问题无呈现处 / "
    "下轮任务未给到 / 无演进视图 / 改进不可见），本轮把「演进报告」变成机制：生成器入库（scripts/），"
    "「每轮收尾必须产出报告」写入 AGENTS.md 协作约定，optimizer 的全量失败 run 清单（analysis-r3.md）"
    "成为报告②⑤节的数据源。本文件即机制首次运行产物（R1→R3 全量回溯版）。",
]

CARRYOVER_STATUS = [
    "**R1 → R2 遗留**：",
    "- 12 条 `Model unavailable` 失败 run（确定性模型配置错误）—— **已解决**：网关配置 + opencode 2.2.5 升级；"
    "平台侧 P3（不可重试快速失败）落地后该类错误不再烧重试预算。R2 零复现。",
    "- CDN 缺码守卫致 1 包 FAIL —— **已解决**：P2 将畸形响应/缺码从 `Fatalf` 改为 `Skipf`（含 `AGENTWORK_SKIP_CDN_DRIFT` 环境开关），R2 起 `go test ./...` 全绿。",
    "- sg-O 被 depends_on(sg-B) 阻塞的「假依赖」—— **部分处理**：R2 实测 queue_wait 仍占墙钟 78%（2.20m/2.81m），分析前置的派发结构调整留待后续轮次提案。",
    "",
    "**R2 → R3 遗留（R3 收尾时点状态）**：",
    "- **验证门三连败（AGENTWORK_HOME 泄漏）——待正式修复（→ 提案 P-A）**：`runVerifiedCmd`（internal/daemon/verify.go:171-186）"
    "不裁剪环境，子进程继承 `AGENTWORK_HOME=/root/.agentwork`，`t.Setenv(\"HOME\")` 的测试隔离被 `paths.Home()` 的优先级击穿，"
    "共享 runsRoot 里的陈旧 clone 让 gitwork/deliver/digest 测试假红。当前以 domain checks 的 `unset AGENTWORK_HOME;` 前缀绕过（已生效，R2 run b319cffb 通过）。",
    "- **rsi_round 轮次编号不递增 ——待修（→ 提案 P-B）**：两轮 `round_no` 均为 1（编号按 session 而非 domain 自增），"
    "`/rsi/overview` 趋势无法区分轮次，delta_json 恒为空。",
    "- **bench 基准口径漂移 ——约定层修复中（→ 提案 P-C）**：R1 温单跑 vs R2 温中位的「-50%」口径混杂；R3 sg-REBENCH 已按冷缓存口径派发。",
    "- **SSH 私钥权限致 deliver 失败一次 ——已解决**：容器重启重置权限（0750），修正后 11:09Z 重试合入成功；自动重试机制列为下一轮候选。",
    "- **双 `agentwork connect` 互抢 peer ——已解决一次、土壤仍在**：R1 末人工合并为单实例；R3 分析时进程表再现 5+ 并存 connect 进程（活体复现），收敛方案列为下一轮候选。",
    "- **owner 运行卡顿 71 分钟无看护 ——待治理（候选任务 5）**：R2 run b77ba4c9 人工停止；watchdog 覆盖范围待扩展。",
]

# 改进点：三条已批准提案（R2 实施）。每条两个 commit：原实施（agentwork-int，R2 轮交付）+ 新基线语义化重放（agentwork-int-v2 重建）。效果数字取自已提交报告原文。
_REPO_WEB = "https://github.com/Sinkear/agentwork/commit/"
IMPROVEMENTS = [
    {
        "no": 1,
        "title": "P1 测试并行化（R2 提案 1）",
        "problem": "R1 基准显示 `internal/daemon` 包测试 8.00s 独占鳌头且 70 个顶层测试全部串行——"
                   "互不依赖的测试没有理由排队（proposals.md 提案 1，已批准）。",
        "commit": "3cf2116",
        "commit2": "e08cb72",
        "subject": "test(P1): 并行化 internal/daemon 独立顶层测试（提案1【已批准】）",
        "commit_url": _REPO_WEB + "3cf2116",
        "diff_summary": None,  # 由 git stat 动态填充
        "effect": "internal/daemon 包 8.00s → 5.911s（约 **-23%**，R2 基准报告第 4 节）；"
                  "实施时三连跑 wall 5.30/5.73/4.99s（实施前基线 8.17s，约 -35%，impl-notes.md）。"
                  "R3 冷口径复测 5.855s（中位），收益保持（benchmark-report-r3.md 第 4 节）。",
        "status_note": "已生效（R2 复测确认，R3 复测保持）。`t.Setenv` 的三个文件按提案保持串行。",
    },
    {
        "no": 2,
        "title": "P2 CDN 漂移守卫 fail→skip（R2 提案 2）",
        "problem": "R1 唯一失败包 internal/service：CDN 返回合法 JSON 但缺 6 码时守卫 `t.Fatalf`，"
                   "导致全量测试永久红、掩盖真实回归（proposals.md 提案 2，已批准）。",
        "commit": "8ca9957",
        "commit2": "e2bda1d",
        "subject": "test(P2): CDN 漂移守卫对畸形响应 fail 改 skip（提案2【已批准】）",
        "commit_url": _REPO_WEB + "8ca9957",
        "diff_summary": None,
        "effect": "失败包 **1 → 0**：`go test ./... -count=1` 自 R2 起三连跑全绿（14 包 ok / 0 FAIL）；"
                  "R1 的 3 条失败用例全部消除（R2 基准报告第 4 节）。",
        "status_note": "已生效。缺码漂移改 skip 为提案风险条款已接受项，skip 时输出完整缺码清单保可诊断性。",
    },
    {
        "no": 3,
        "title": "P3 不可重试错误快速失败（R2 提案 3）",
        "problem": "R1 12/14 条失败 run 是同一确定性错误 `Model unavailable`，却各自烧满 3 次重试预算"
                   "（约 27s×N 空转 + backoff 唤醒）——重试不修复配置类原因（proposals.md 提案 3，已批准）。",
        "commit": "18a8c3c",
        "commit2": "e2bda1d",
        "subject": "feat(P3): 失败重试引入不可重试错误快速失败（提案3【已批准】）",
        "commit_url": _REPO_WEB + "18a8c3c",
        "diff_summary": None,
        "effect": "确定性失败不再烧重试预算：R2 起 `Model unavailable` 类失败 **零复现**（R2 全部 8 条 run 中 0 条该类错误，"
                  "R1 为 12 条）；`nonRetryableMarkers` 白名单起步只收 `Model unavailable`。",
        "status_note": "已生效。白名单机制可扩展（R3 已暴露「machine offline」应纳入派发层判定，见候选任务）。",
    },
]

GENERATOR_NEXT = [
    "**round_no 依赖 goal 标题正则**——提案 P-B 落地（rsi_round 按 domain 递增）后，生成器应改读"
    "机读轮次号，去掉标题正则推断（本报告第④节口径注记可随之删除）。",
    "**③ 节改进点的 commit/diff 目前依赖静态映射**——改进点表随轮次增长，下一轮应把「问题依据→commit→效果」"
    "的映射固化到每轮 impl-notes 的结构化小节（如 YAML front-matter），生成器自动抽取，避免手维护。",
    "**bench 数字自动抽取**——第④节 bench 口径对照表目前按轮次静态维护；提案 P-C 的 `BENCHMARK-PROTOCOL.md` "
    "固定字段落地后可从 `benchmark-report-r<N>.md` 自动抽取（本轮 R3 行已按 benchmark-report-r3.md 实填）。",
    "`/rsi` 端点文档口径：goal 描述写 `/rsi` 实测 404，实际端点为 `/rsi/sessions` 等 4 个（本脚本已按实拉口径实现）——建议文档层修正（候选任务 3）。",
]


def load_improvement_diffs():
    """从 evo['improvement_candidates'] 取 diffstat，匹配 IMPROVEMENTS（原实施 commit 优先）。"""
    by_hash = {c["hash"]: c for c in evo.get("improvement_candidates", [])}
    for imp in IMPROVEMENTS:
        c = by_hash.get(imp["commit"])
        if not c and imp.get("commit2"):
            c = by_hash.get(imp["commit2"])
            if c:
                imp["subject"] = c["subject"]  # 重放 commit 的 subject 描述更完整
        if not c:
            # subject 前缀（如 "test(P1)"）匹配
            key = imp["subject"].split(":")[0]
            for h, cc in by_hash.items():
                if cc["subject"].startswith(key):
                    c = cc
                    break
        if c and c.get("stat"):
            stat_lines = [l.strip() for l in c["stat"].strip().splitlines() if l.strip()]
            tail = stat_lines[-1] if stat_lines else ""
            keep = [
                l for l in stat_lines[:-1]
                if any(k in l for k in ("_test.go", "goal.go", "daemon.go", "codes_test"))
            ][:6]
            imp["diff_summary"] = keep + ([tail] if tail else [])


def load_next_source():
    """提取 docs/rsi/analysis-r*.md（取 revision 最新的那份）的「下一轮候选任务清单」节。"""
    reports = evo.get("reports") or {}
    best = None
    for path in sorted(reports):
        m = re.match(r"docs/rsi/analysis-r(\d+)\.md$", path)
        if m and (best is None or int(m.group(1)) >= best[0]):
            best = (int(m.group(1)), path, reports[path])
    if not best:
        return None
    _, path, rep = best
    text = rep["text"]
    m = re.search(r"##\s*[^\n]*下一轮候选任务清单[^\n]*\n(.*?)(?=\n---|\Z)", text, re.S)
    if not m:
        return None
    lines = [l for l in m.group(1).strip().splitlines() if l.strip() and not l.strip().startswith("*本报告")]
    out = []
    for l in lines:
        l = l.rstrip()
        if len(l) > 240:
            l = l[:239] + "…"
        out.append(l)
    return {"commit": rep["commit"], "path": path, "lines": out}


def main():
    load_improvement_diffs()
    global NEXT_SOURCE
    NEXT_SOURCE = load_next_source()


    out = []
    out.append(f"# agentwork 自迭代轮次演进报告 R{ROUND}（R1 → R{ROUND}）")
    out.append("")
    out.append(
        f"- 生成：{NOW}，由 scripts/rsi-round-report.sh 自动产出（RSI R3 可感知性改进机制的首次运行）"
    )
    out.append(f"- 数据快照：daemon API `{API_URL}`（/rsi/sessions、/goals、runs、rounds、overview）+ git log（--all）")
    out.append(
        "- 自包含声明：本报告不依赖平台登录即可理解；所有 ID/数字均可在 daemon API 与 git 历史复查"
    )
    out.append("")
    out.extend(evolution_section())
    out.extend(issues_section())
    out.extend(improvement_section())
    out.extend(metrics_section())
    out.extend(next_section())
    out.extend(footer())
    out.append("")

    text = "\n".join(out)
    if OUT == "-":
        sys.stdout.write(text)
    else:
        with open(OUT, "w", encoding="utf-8") as f:
            f.write(text)


if __name__ == "__main__":
    main()
