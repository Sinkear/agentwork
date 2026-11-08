"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import Link from "next/link";
import { ArrowRight, Check, Loader2, PenLine, Send, Sparkles, Trash2 } from "lucide-react";
import { PageBody, PageHeader, SectionCard, Button, inputCls } from "@/components/ui";
import { NewGoalForm } from "@/components/new-goal-form";
import { StewardSetup } from "@/components/steward-setup";
import { AcpChatClient, ChatEvent, PermissionRequest } from "@/lib/acp";
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
  const sessionRef = useRef("");
  const stewardRef = useRef<string | null>(null);
  // 起点时间：创建成功后用它挑出“这次新建的那条”任务，不依赖管家回传的 id。
  const startedAtRef = useRef(0);
  const lineId = useRef(0);
  const scrollRef = useRef<HTMLDivElement>(null);
  const busy = phase === "connecting" || phase === "running";

  const push = useCallback((line: Omit<Line, "id">) => {
    lineId.current += 1;
    setLines((prev) => {
      // 流式片段合并到一个气泡里（与管家聊天一致）。
      const last = prev[prev.length - 1];
      if (last && last.role === line.role && (line.role === "agent") && !last.pending) {
        return [...prev.slice(0, -1), { ...last, text: last.text + line.text }];
      }
      return [...prev, { ...line, id: lineId.current }];
    });
  }, []);

  useEffect(() => {
    scrollRef.current?.scrollTo(0, scrollRef.current.scrollHeight);
  }, [lines, busy]);

  // 连接管家（懒连接：打开页面不建会话，不浪费一次 CLI 启动）。
  const ensureClient = useCallback(async () => {
    if (clientRef.current && sessionRef.current) return clientRef.current;
    setPhase("connecting");
    const steward = stewardRef.current
      ? { id: stewardRef.current }
      : (await listAgents()).find((a) => a.type === "steward");
    if (!steward) {
      setPhase("failed");
      setError("还没有管家 agent。管家只会自动绑定 hwcloud CLI；可以在这里或「对话」页立即手动创建。");
      throw new Error("no steward");
    }
    stewardRef.current = steward.id;
    const client = new AcpChatClient({
      onEvent: (ev: ChatEvent) => {
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
      onPermissionRequest: (req, respond) => setPerm({ req, respond }),
      onClose: () => setError((e) => e || "与管家的连接断开了"),
    });
    const host = window.location.host;
    await client.connect(
      `ws${window.location.protocol === "https:" ? "s" : ""}://${host}/backend/agents/${steward.id}/acp`
    );
    sessionRef.current = await client.newSession();
    clientRef.current = client;
    setPhase("ready");
    return client;
  }, [push]);

  useEffect(() => {
    return () => {
      clientRef.current?.close();
      clientRef.current = null;
      sessionRef.current = "";
    };
  }, []);

  // 跟管家说一句话：把“要做什么”包成一条建任务的指令，让 CLI 直接建。
  const ask = useCallback(
    async (say: string, first: boolean) => {
      const body = say.trim();
      if (!body || busy) return;
      setError("");
      try {
        const client = await ensureClient();
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
        setPhase("ready");
        setFollowUp("");
      } catch (e) {
        setPhase("failed");
        const raw = String((e as Error).message ?? e);
        const head = /连接已关闭|连接失败|not connected/.test(raw)
          ? "与管家的连接没建立起来。"
          : raw;
        setError(head);
        // 诊断慢一点没关系，先把失败原因摆出来，再补上具体到机器/CLI 的解释。
        void describeStewardFailure(stewardRef.current).then((why) => {
          if (why) setError((prev) => (prev ? `${prev}\n${why}` : why));
        });
      }
    },
    [busy, ensureClient, push]
  );

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
                    setLines([]);
                    setPhase("idle");
                    setText("");
                    setError("");
                    startedAtRef.current = 0;
                    clientRef.current?.close();
                    clientRef.current = null;
                    sessionRef.current = "";
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
                {error && (
                  <div className="space-y-2 rounded-md border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-xs text-amber-300">
                    {error.split("\n").map((line, i) => (
                      <p key={i}>{line}</p>
                    ))}
                    <button
                      type="button"
                      onClick={() => setManual(true)}
                      className="underline decoration-dotted underline-offset-2 hover:text-amber-200"
                    >
                      改用手动填写
                    </button>
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
                  <Button
                    onClick={() => void ask(followUp, false)}
                    disabled={busy || phase === "failed" || !followUp.trim()}
                    className="gap-1.5"
                  >
                    <Send className="h-3.5 w-3.5" /> 发送
                  </Button>
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