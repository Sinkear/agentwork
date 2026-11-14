"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { ArrowRight, Check, Loader2, PenLine, Send, Sparkles, Square, Trash2 } from "lucide-react";
import { PageBody, PageHeader, SectionCard, Button, inputCls } from "@/components/ui";
import { NewGoalForm } from "@/components/new-goal-form";
import { StewardSetup } from "@/components/steward-setup";
import { AcpChatClient, AcpCloseInfo, ChatEvent, PermissionRequest, formatAcpClose } from "@/lib/acp";
import { listAgents, listGoals } from "@/lib/api";
import { describeStewardFailure } from "@/lib/steward";
import { useAgents, useSquads, useDomains } from "@/lib/queries";
import { cn } from "@/lib/utils";

// 「新建任务」页：一句话描述 → 管家用 agentwork CLI 建任务 → 落库后自动切回任务视图。
// 为什么是独立页面而不是弹窗/参数：
//   1) /goals?new=1 要先把整个任务列表拉起来才弹窗，点侧栏会卡一下，而且 URL 没变化时
//      二次点击没有任何反馈（用户反馈的“点两次不生效”）。
//   2) 建任务是要写一段话的，独立页面能把“描述—管家反馈—追问”这条线放得下。
// 兜底：管家不可用（没有在线机器）时，同一页里能直接手动填表，不是死路。

type Phase = "idle" | "connecting" | "ready" | "running" | "created" | "failed";

interface Line {
  id: number;
  role: "user" | "agent" | "tool" | "system";
  text: string;
  toolName?: string;
  pending?: boolean;
}

const EXAMPLES = [
  "把 webhook 投递失败的重试逻辑写成一份文档，放到 webhook 面板旁边",
  "给任务看板加一个按负责人筛选的入口",
  "整理最近三次 agent 执行失败的原因，汇总成一条评论",
];

export default function NewGoalPage() {
  const router = useRouter();
  const { data: agents } = useAgents();
  const { data: squads } = useSquads();
  const { data: domains } = useDomains();

  const [phase, setPhase] = useState<Phase>("idle");
  const [text, setText] = useState("");
  const [followUp, setFollowUp] = useState("");
  const [lines, setLines] = useState<Line[]>([]);
  const [error, setError] = useState("");
  const [createdTitle, setCreatedTitle] = useState("");
  const [manual, setManual] = useState(false);
  const [perm, setPerm] = useState<{
    req: PermissionRequest;
    respond: (o: { optionId?: string; cancelled?: boolean }) => void;
  } | null>(null);

  const clientRef = useRef<AcpChatClient | null>(null);
  // 握手中的 client（还没到 ready）：组件卸载/重试时必须能关掉它——旧代码
  // 只在 newSession 成功后才写 clientRef，握手期间被卸载的 socket 谁也关不掉，
  // 机器侧会留下一条悬空的 chat channel（daemon 日志里的 “web read: close 1005”）。
  const inflightRef = useRef<AcpChatClient | null>(null);
  const connectingRef = useRef<Promise<AcpChatClient> | null>(null);
  // 代际：每次重试/重来/卸载都 +1。旧连接的 onEvent/onClose 据此丢弃，
  // StrictMode 二次挂载和“重试”都不会被上一次连接的迟到回调污染。
  const genRef = useRef(0);
  // 一次“提交”的代次：失败后迟到的 success/error 不再改 UI。
  const attemptRef = useRef(0);
  // 同步的在途闸门：disabled 挡不住回车/双击的同一帧连点。
  const inFlightRef = useRef(false);
  // 失败后“重试”要重放的那句话（首轮 / 追问都要能重试）。
  const lastAskRef = useRef<{ say: string; first: boolean } | null>(null);
  const sessionRef = useRef("");
  const stewardRef = useRef<string | null>(null);
  // 起点时间：创建成功后用它挑出“这次新建的那条”任务，不依赖管家回传的 id。
  const startedAtRef = useRef(0);
  const lineId = useRef(0);
  const scrollRef = useRef<HTMLDivElement>(null);
  const busy = phase === "connecting" || phase === "running";
  // 半死连接提醒：连接在、但机器一直不回应时，别让用户盯着一个无声的转圈。
  const [slow, setSlow] = useState(false);

  const push = useCallback((line: Omit<Line, "id">) => {
    // Allocate the id OUTSIDE the updater: the updater runs lazily and may
    // run twice (StrictMode), so reading lineId.current inside it let two
    // queued pushes both see the latest counter → duplicate React keys.
    const id = (lineId.current += 1);
    setLines((prev) => {
      // 流式片段合并到一个气泡里（与管家聊天一致）。
      const last = prev[prev.length - 1];
      if (last && last.role === line.role && (line.role === "agent") && !last.pending) {
        return [...prev.slice(0, -1), { ...last, text: last.text + line.text }];
      }
      return [...prev, { ...line, id }];
    });
  }, []);

  useEffect(() => {
    scrollRef.current?.scrollTo(0, scrollRef.current.scrollHeight);
  }, [lines, busy]);

  // 关掉当前连接（含握手中的那一个）并让这一代的所有回调失效。
  // 先关 socket 再清 ref：旧连接的 onClose 在 gen 不匹配时会自行忽略，
  // 不会把“重试/重来”的界面改成失败态。
  const disposeClient = useCallback(() => {
    genRef.current += 1;
    connectingRef.current = null;
    const current = clientRef.current;
    const inflight = inflightRef.current;
    clientRef.current = null;
    inflightRef.current = null;
    sessionRef.current = "";
    if (inflight && inflight !== current) inflight.close();
    current?.close();
    setPerm(null);
  }, []);

  // 连接管家（懒连接：打开页面不建会话，不浪费一次 CLI 启动）。
  // 并发保护：同一帧里连点两次只会有一个握手（connectingRef 复用在途 promise）。
  const ensureClient = useCallback(async (): Promise<AcpChatClient> => {
    if (clientRef.current && sessionRef.current) return clientRef.current;
if (connectingRef.current) return connectingRef.current;
    const gen = genRef.current;
    const run = (async () => {
      setPhase("connecting");
      const steward = stewardRef.current
        ? { id: stewardRef.current }
        : (await listAgents()).find((a) => a.type === "steward");
      if (!steward) {
        throw new Error("还没有管家 agent——先连一台机器并重启服务，平台会自动创建管家。");
      }
      stewardRef.current = steward.id;
      const client = new AcpChatClient({
        onEvent: (ev: ChatEvent) => {
          if (gen !== genRef.current) return;
          if (ev.kind === "agent") push({ role: "agent", text: ev.text });
          else if (ev.kind === "tool") {
            if (ev.isResult) {
              setLines((prev) =>
                prev.map((l) =>
                  l.role === "tool" && l.toolName === ev.toolName && l.pending
                    ? { ...l, text: ev.output ?? "", pending: false }
                    : l
                )
              );
            } else {
              push({ role: "tool", text: ev.text, toolName: ev.toolName, pending: true });
            }
          }
        },
        onPermissionRequest: (req, respond) => {
          if (gen !== genRef.current) return;
          setPerm({ req, respond });
        },
        onClose: (info?: AcpCloseInfo) => {
          if (gen !== genRef.current) return;
          clientRef.current = null;
          sessionRef.current = "";
          setPhase((p) => (p === "created" ? p : "failed"));
          const detail = info ? formatAcpClose(info) : "连接已断开";
          // 这里只兜底“没建成就断了”的情况；具体失败原因由 ask 的 catch
          // 覆盖成带阶段的完整消息。
          setError(
            (e) => e || `与管家的连接断开了（${detail}）——可以点「重试」重新连接，或改用手动填写。`
          );
        },
      });
      inflightRef.current = client;
      const host = window.location.host;
      await client.connect(
        `ws${window.location.protocol === "https:" ? "s" : ""}://${host}/backend/agents/${steward.id}/acp`
      );
      if (gen !== genRef.current) {
        client.close();
        throw new Error("连接已取消（页面状态已重置）");
      }
      sessionRef.current = await client.newSession();
      if (gen !== genRef.current) {
        client.close();
        throw new Error("连接已取消（页面状态已重置）");
      }
      clientRef.current = client;
      inflightRef.current = null;
      return client;
    })();
    connectingRef.current = run;
    try {
      return await run;
    } finally {
      if (connectingRef.current === run) connectingRef.current = null;
    }
  }, [push]);

  useEffect(() => {
    return () => {
      // StrictMode 的第一次 cleanup 发生在本页还没连接时（懒连接），
      // 这里只关真实存在的 ref——不会误伤“首次渲染时正在建立”的 socket。
      attemptRef.current += 1;
      inFlightRef.current = false;
      disposeClient();
    };
  }, [disposeClient]);

  // 跟管家说一句话：把“要做什么”包成一条建任务的指令，让 CLI 直接建。
  const ask = useCallback(
    async (say: string, first: boolean) => {
      const body = say.trim();
      if (!body || inFlightRef.current) return;
      inFlightRef.current = true;
      const attempt = ++attemptRef.current;
      lastAskRef.current = { say: body, first };
      setError("");
      setSlow(false);
      try {
        const client = await ensureClient();
        // 期间被“重试/重来/卸载”取消了：这次请求的结果一律不再改 UI。
        if (attempt !== attemptRef.current) return;
        setPhase("running");
        if (first) {
          startedAtRef.current = Date.now();
          setLines([]);
          push({
            role: "user",
            text: body,
          });
          push({
            role: "system",
            text: "已交给管家：它用 agentwork CLI 建任务，建好会自动切回任务视图。",
          });
          await client.prompt(
            sessionRef.current,
            [
              "帮我创建一条任务（goal），需求如下：",
              "",
              body,
              "",
              "请用 `agentwork create goal --title \"...\"` 建出来（负责人/项目能判断就一起带上，缺字段就问我要）。",
              "建完后用一句话告诉我任务标题，不要做别的改动。",
            ].join("\n")
          );
        } else {
          push({ role: "user", text: body });
          await client.prompt(sessionRef.current, body);
        }
        if (attempt !== attemptRef.current) return;
        setPhase("ready");
        setFollowUp("");
      } catch (e) {
        if (attempt !== attemptRef.current) return;
        setPhase("failed");
        // 完整暴露失败阶段 + WS 关闭码/原因：绝不再只留一个无声的转圈。
        const raw = String((e as Error).message ?? e);
        setError(`没建成：${raw}`);
        disposeClient();
        // 诊断慢一点没关系，先摆事实，再补上具体到机器/CLI 的解释。
        void describeStewardFailure(stewardRef.current).then((why) => {
          if (why) setError((prev) => (prev ? `${prev}\n${why}` : why));
        });
      } finally {
        inFlightRef.current = false;
      }
    },
    [ensureClient, push, disposeClient]
  );

  // 失败后的「重试」：彻底关掉旧连接，用同一句话重新走一遍握手。
  const retry = useCallback(() => {
    const last = lastAskRef.current;
    // 让还在途中的旧请求作废（半死 socket 上的 prompt 可能很长），
    // 它的迟到回调不能再改这一次的界面。
    attemptRef.current += 1;
    inFlightRef.current = false;
    disposeClient();
    setError("");
    setSlow(false);
    setLines([]);
    if (!last) {
      setPhase("idle");
      return;
    }
    setPhase("running");
    void ask(last.say, last.first);
  }, [ask, disposeClient]);

  // 停止：只中断当前这一轮（session/cancel）——连接和会话都保留，
  // 改一句话就能接着继续，不用重来。握手阶段还没有会话可取消，
  // 那就作废这次连接（下一句话会重新握手）。
  const stop = useCallback(() => {
    if (phase === "connecting") {
      attemptRef.current += 1;
      inFlightRef.current = false;
      disposeClient();
      setPhase("ready");
      return;
    }
    const client = clientRef.current;
    if (client && sessionRef.current) client.cancel(sessionRef.current);
    // running 轮的 prompt 会以 cancelled 结束，ask 的收尾自然把 phase
    // 拉回 ready；这里不抢跑，避免和迟到的轮询/收尾打架。
  }, [phase, disposeClient]);

  // 守护：连接在、但 60s 没有任何进展 —— 提示可能是半死连接，别静默转圈。
  useEffect(() => {
    if (phase !== "running" && phase !== "connecting") {
      setSlow(false);
      return;
    }
    const timer = window.setTimeout(() => setSlow(true), 60_000);
    return () => window.clearTimeout(timer);
  }, [phase]);

  // 轮询：只要出现“本次提交之后创建”的任务，就算创建成功 → 切回任务视图。
  useEffect(() => {
    if (!startedAtRef.current) return;
    if (phase === "created" || phase === "failed") return;
    const startISO = new Date(startedAtRef.current - 2000).toISOString();
    const timer = window.setInterval(async () => {
      try {
        const goals = await listGoals();
        const hit = goals.find((g) => g.created_at && g.created_at >= startISO);
        if (hit) {
          setCreatedTitle(hit.title);
          setPhase("created");
          window.setTimeout(() => router.push(`/goals/${hit.id}`), 900);
        }
      } catch {
        // 轮询失败不打断对话，下一轮再试
      }
    }, 2000);
    return () => window.clearInterval(timer);
  }, [phase, router]);

  const submitFirst = () => {
    setPhase("running");
    void ask(text, true);
  };

  return (
    <PageBody>
      <PageHeader
        title="新建任务"
        desc="一句话说清楚要做什么，管家会拆解成任务建进平台；建好之后自动回到任务视图。"
        action={
          <Link href="/goals" className="text-sm text-ink-3 transition-colors hover:text-ink">
            回到任务 <ArrowRight className="ml-0.5 inline h-3.5 w-3.5" />
          </Link>
        }
      />

      <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_320px]">
        <div className="min-w-0 space-y-5">
          {phase === "idle" ? (
            <SectionCard
              title="要做什么？"
              desc="写目标就行，不用写实现步骤——管家会按平台的规矩补负责人和项目。"
            >
              <div className="space-y-3 p-4">
                <StewardSetup
                  onCreated={(id) => {
                    stewardRef.current = id;
                  }}
                />
                <textarea
                  autoFocus
                  value={text}
                  onChange={(e) => setText(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" && (e.metaKey || e.ctrlKey)) {
                      e.preventDefault();
                      submitFirst();
                    }
                  }}
                  rows={5}
                  placeholder="例如：把 webhook 投递失败的重试逻辑写成文档，放到 webhook 面板旁边"
                  className={cn(inputCls, "resize-none leading-relaxed")}
                />
                <div className="flex flex-wrap items-center gap-2">
                  <Button onClick={submitFirst} disabled={!text.trim()} className="gap-1.5">
                    <Sparkles className="h-3.5 w-3.5" /> 交给管家创建
                  </Button>
                  <Button variant="outline" onClick={() => setManual(true)} className="gap-1.5">
                    <PenLine className="h-3.5 w-3.5" /> 手动填写
                  </Button>
                  <span className="text-[11px] text-ink-3">⌘/Ctrl + Enter 发送</span>
                </div>
              </div>
            </SectionCard>
          ) : (
            <SectionCard
              title="管家"
              count={lines.filter((l) => l.role !== "system").length}
              desc={
                phase === "connecting"
                  ? "正在连接管家…"
                  : phase === "running"
                    ? "正在创建…"
                    : phase === "created"
                      ? "已创建，正在切回任务视图"
                      : phase === "failed"
                        ? "没建成"
                        : "可以继续补充要求"
              }
              action={
                <button
                  type="button"
                  onClick={() => {
                    // 回到初始态：作废在途尝试、关掉（含握手中的）连接。
                    attemptRef.current += 1;
                    inFlightRef.current = false;
                    lastAskRef.current = null;
                    disposeClient();
                    setLines([]);
                    setPhase("idle");
                    setText("");
                    setFollowUp("");
                    setError("");
                    setSlow(false);
                    startedAtRef.current = 0;
                  }}
                  className="flex items-center gap-1 rounded-md px-2 py-1 text-[11px] text-ink-3 transition-colors hover:bg-surface-2 hover:text-ink"
                >
                  <Trash2 className="h-3.5 w-3.5" /> 重来
                </button>
              }
            >
              <div ref={scrollRef} className="max-h-[46vh] min-h-[220px] space-y-3 overflow-y-auto p-4">
                {lines.map((l) => {
                  if (l.role === "system") {
                    return (
                      <p key={l.id} className="text-[11px] text-ink-3">
                        {l.text}
                      </p>
                    );
                  }
                  if (l.role === "tool") {
                    return (
                      <div
                        key={l.id}
                        className="rounded-md border border-line bg-surface-0 px-2.5 py-1.5 font-mono text-[11px] text-ink-2"
                      >
                        <span className="text-ink-3">$ </span>
                        {l.toolName}
                        {l.pending ? (
                          <Loader2 className="ml-1 inline h-3 w-3 animate-spin text-ink-3" />
                        ) : l.text ? (
                          <span className="mt-1 block whitespace-pre-wrap break-words text-ink-3">{l.text}</span>
                        ) : null}
                      </div>
                    );
                  }
                  return (
                    <div key={l.id} className={cn("flex", l.role === "user" ? "justify-end" : "justify-start")}>
                      <div
                        className={cn(
                          "max-w-[85%] whitespace-pre-wrap break-words rounded-lg px-3 py-2 text-sm",
                          l.role === "user" ? "bg-accent text-accent-ink" : "bg-surface-2 text-ink"
                        )}
                      >
                        {l.text}
                      </div>
                    </div>
                  );
                })}
                {busy && (
                  <div className="flex items-center gap-2 text-sm text-ink-3">
                    <Loader2 className="h-3.5 w-3.5 animate-spin" />
                    {phase === "connecting" ? "连接管家…" : "管家正在建任务…"}
                  </div>
                )}
                {phase === "created" && (
                  <div className="flex items-start gap-2 rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs text-emerald-300">
                    <Check className="mt-0.5 h-3.5 w-3.5 shrink-0" />
                    <span>已创建「{createdTitle || "任务"}」，正在打开任务详情…</span>
                  </div>
                )}
                {slow && phase !== "failed" && phase !== "created" && (
                  <div className="space-y-2 rounded-md border border-line bg-surface-2 px-3 py-2 text-xs text-ink-2">
                    <p>已经 60 秒没有新的进展——连接可能处于半死状态（socket 还在，但机器那头没回应）。</p>
                    <div className="flex flex-wrap items-center gap-3">
                      {lastAskRef.current && (
                        <button
                          type="button"
                          onClick={retry}
                          className="font-medium underline decoration-dotted underline-offset-2 hover:text-ink"
                        >
                          断开重试
                        </button>
                      )}
                      <button
                        type="button"
                        onClick={() => setManual(true)}
                        className="underline decoration-dotted underline-offset-2 hover:text-ink"
                      >
                        改用手动填写
                      </button>
                    </div>
                  </div>
                )}
                {error && (
                  <div className="space-y-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-300">
                    {error.split("\n").map((line, i) => (
                      <p key={i}>{line}</p>
                    ))}
                    <div className="flex flex-wrap items-center gap-3">
                      {lastAskRef.current && (
                        <button
                          type="button"
                          onClick={retry}
                          className="font-medium underline decoration-dotted underline-offset-2 hover:text-amber-200"
                        >
                          重试（重新连接并再提交一次）
                        </button>
                      )}
                      <button
                        type="button"
                        onClick={() => setManual(true)}
                        className="underline decoration-dotted underline-offset-2 hover:text-amber-200"
                      >
                        改用手动填写
                      </button>
                    </div>
                  </div>
                )}
              </div>

              {phase !== "created" && (
                <div className="flex gap-2 border-t border-line bg-surface-0/40 px-4 py-3">
                  <input
                    className={cn(inputCls, "flex-1")}
                    placeholder={
                      phase === "failed"
                        ? "管家没连上，可以直接手动填…"
                        : "补充一句：用哪个负责人、挂哪个项目…"
                    }
                    value={followUp}
                    onChange={(e) => setFollowUp(e.target.value)}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" && !e.shiftKey) {
                        e.preventDefault();
                        void ask(followUp, false);
                      }
                    }}
                    disabled={busy || phase === "failed"}
                  />
                  {busy ? (
                    <Button
                      variant="danger"
                      onClick={stop}
                      className="gap-1.5"
                      title="中断当前这轮（连接保留，可以继续补充）"
                    >
                      <Square className="h-3.5 w-3.5 fill-current" /> 停止
                    </Button>
                  ) : (
                    <Button
                      onClick={() => void ask(followUp, false)}
                      disabled={phase === "failed" || !followUp.trim()}
                      className="gap-1.5"
                    >
                      <Send className="h-3.5 w-3.5" /> 发送
                    </Button>
                  )}
                </div>
              )}
            </SectionCard>
          )}
        </div>

        <aside className="space-y-5">
          <SectionCard title="会被建成什么样">
            <ul className="space-y-2.5 p-4 text-xs leading-relaxed text-ink-2">
              <li className="flex gap-2">
                <span className="mt-1.5 h-1 w-1 shrink-0 rounded-full bg-accent" />
                没指定负责人 → 进「待办」，你回头在看板上拖到「进行中」才开跑。
              </li>
              <li className="flex gap-2">
                <span className="mt-1.5 h-1 w-1 shrink-0 rounded-full bg-accent" />
                说了负责人/项目 → 直接进「进行中」，agent 立刻开始。
              </li>
              <li className="flex gap-2">
                <span className="mt-1.5 h-1 w-1 shrink-0 rounded-full bg-accent" />
                信息不够时管家会先问你要（比如挂哪个项目），补一句就行。
              </li>
            </ul>
          </SectionCard>

          <SectionCard title="不会写？照这几个改" desc="点一下放进输入框">
            <div className="divide-y divide-line">
              {EXAMPLES.map((e) => (
                <button
                  key={e}
                  type="button"
                  onClick={() => {
                    setText(e);
                    if (phase !== "idle") {
                      setLines([]);
                      setPhase("idle");
                    }
                  }}
                  className="flex w-full items-start gap-2 px-4 py-2.5 text-left text-xs leading-relaxed text-ink-2 transition-colors hover:bg-surface-2 hover:text-ink"
                >
                  <ArrowRight className="mt-0.5 h-3 w-3 shrink-0 text-ink-3" />
                  {e}
                </button>
              ))}
            </div>
          </SectionCard>
        </aside>
      </div>

      {manual && (
        <NewGoalForm
          agents={agents}
          squads={squads}
          domains={domains}
          onClose={() => setManual(false)}
          onCreated={() => {
            setManual(false);
            router.push("/goals");
          }}
        />
      )}

      {perm && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50">
          <div className="w-[420px] rounded-xl border border-line bg-surface-1 p-4 shadow-xl shadow-black/30">
            <p className="text-sm font-medium text-ink">管家想执行一个命令</p>
            <p className="mt-1 text-xs text-ink-3">
              {perm.req.toolCall?.title || "tool"}（{perm.req.toolCall?.kind || "unknown"}）
            </p>
            <div className="mt-3 flex justify-end gap-2">
              {(perm.req.options ?? [])
                .filter((o) => o.kind?.startsWith("allow"))
                .map((o) => (
                  <Button
                    key={o.optionId}
                    onClick={() => {
                      perm.respond({ optionId: o.optionId });
                      setPerm(null);
                    }}
                  >
                    {o.name || "允许"}
                  </Button>
                ))}
              {(perm.req.options ?? [])
                .filter((o) => o.kind?.startsWith("reject"))
                .map((o) => (
                  <Button
                    key={o.optionId}
                    variant="outline"
                    onClick={() => {
                      perm.respond({ optionId: o.optionId });
                      setPerm(null);
                    }}
                  >
                    {o.name || "拒绝"}
                  </Button>
                ))}
              <Button
                variant="outline"
                onClick={() => {
                  perm.respond({ cancelled: true });
                  setPerm(null);
                }}
              >
                取消
              </Button>
            </div>
          </div>
        </div>
      )}
    </PageBody>
  );
}