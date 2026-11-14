"use client";

import { useState, useEffect, useRef, useCallback } from "react";
import { Trash2, Send, Square } from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui";
import { StewardSetup } from "@/components/steward-setup";
import { AcpChatClient, ChatEvent, PermissionRequest } from "@/lib/acp";
import { describeStewardFailure } from "@/lib/steward";
import { StreamCards } from "@/lib/run-messages";
import { useStickToBottom } from "@/lib/use-stick-to-bottom";
import { listAgents } from "@/lib/api";
import type { Agent } from "@/lib/types";

// StewardChat — 「对话」页面的主体。默认跟管家（steward）聊，
// 也可以用头部的「切换引擎」下拉换任何一个 agent——每个 agent 绑定
// 自己的运行时/模型，切换即换对话对象（m00002 的"切换引擎"）。
// 协议与 chat-panel 相同：连 /agents/{id}/acp，自动建会话，流式收事件。
// 放在页面里而不是弹窗里，是因为跟 agent 说话常常要一边看任务/机器状态。

interface Msg extends ChatEvent {
  id: number;
}

// 聊天记录按 agent 分开存（每个 agent 一份，切换不串台）。
// LEGACY_KEY 是旧的管家单一历史：管家第一次打开时自动迁移过来。
const HISTORY_KEY_LEGACY = "agentwork-chat-history";
const HISTORY_LIMIT = 100;
const historyKey = (agentId: string) => `agentwork-chat-history-${agentId}`;

function readHistoryKey(key: string): Msg[] | null {
  try {
    const raw = localStorage.getItem(key);
    if (!raw) return null;
    const parsed = JSON.parse(raw);
    if (Array.isArray(parsed) && parsed.length) return parsed.slice(-HISTORY_LIMIT);
    return null;
  } catch {
    return null;
  }
}

function loadHistory(agentId: string, isSteward: boolean): Msg[] {
  const own = readHistoryKey(historyKey(agentId));
  if (own) return own;
  if (isSteward) {
    const legacy = readHistoryKey(HISTORY_KEY_LEGACY);
    if (legacy) {
      // 迁移：写进按 agent 的新 key，删掉旧 key，之后清空/重开都以新 key 为准。
      try {
        localStorage.setItem(historyKey(agentId), JSON.stringify(legacy));
        localStorage.removeItem(HISTORY_KEY_LEGACY);
      } catch {
        // quota exceeded — silently keep legacy in place
      }
      return legacy;
    }
  }
  return [];
}

function saveHistory(agentId: string, msgs: Msg[]) {
  try {
    localStorage.setItem(historyKey(agentId), JSON.stringify(msgs.slice(-HISTORY_LIMIT)));
  } catch {
    // quota exceeded or SSR — silently drop
  }
}

const STARTERS = [
  "帮我创建一个任务：把 webhook 投递失败重试写成文档",
  "现在有哪些任务卡在待审批？",
  "看下最近一次 agent 执行失败的原因",
];

export function StewardChat({ className }: { className?: string }) {
  const [messages, setMessages] = useState<Msg[]>([]);
  const [input, setInput] = useState("");
  const [busy, setBusy] = useState(false);
  const [perm, setPerm] = useState<{ req: PermissionRequest; respond: (o: { optionId?: string; cancelled?: boolean }) => void } | null>(null);
  const [error, setError] = useState("");
  // 切换引擎：agents 是可选对象列表，selectedId 是当前对话的 agent。
  const [agents, setAgents] = useState<Agent[]>([]);
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [noAgents, setNoAgents] = useState(false);
  const clientRef = useRef<AcpChatClient | null>(null);
  const sessionRef = useRef<string>("");
  // 单调 id：每次载入历史都从已有最大 id 起算，实时事件不会撞号。
  const msgID = useRef(1);
  // 跟到底部：messages 每变一次（含 streaming tool_call_update 合并进旧
  // 卡片、thought/agent 片段增长）就贴底；用户主动往上翻则先不打扰。
  const scrollRef = useStickToBottom<HTMLDivElement>([messages, busy]);
  const agentActivity = useRef(false);
  // busy 镜像到 ref：连接 effect 的 cleanup 关闭的是 mount 时的状态，
  // 没有 ref 永远看到 false，会漏掉 cancel，留下一个孤儿 turn。
  const busyRef = useRef(false);
  useEffect(() => { busyRef.current = busy; }, [busy]);

  const selected = agents.find((a) => a.id === selectedId) ?? null;
  const isStewardChat = selected?.type === "steward";

  // 拉一次 agent 列表：默认选管家；没有管家但有别的 agent 也能聊。
  useEffect(() => {
    if (agents.length || noAgents) return;
    listAgents()
      .then((list) => {
        if (!list.length) {
          setNoAgents(true);
          return;
        }
        setAgents(list);
        const steward = list.find((a) => a.type === "steward");
        setSelectedId((cur) => cur ?? steward?.id ?? list[0].id);
      })
      .catch(() => setNoAgents(true));
  }, [agents.length, noAgents]);

  const push = useCallback((ev: ChatEvent) => {
    msgID.current += 1;
    if (ev.kind !== "user") agentActivity.current = true;
    setMessages((prev) => {
      const last = prev[prev.length - 1];
      // 连续的 agent/thought 片段合并成一个气泡。
      if (last && last.kind === ev.kind && (ev.kind === "agent" || ev.kind === "thought")) {
        return [...prev.slice(0, -1), { ...last, text: last.text + ev.text, id: last.id }];
      }
      // tool_result 合并进前面同 callId 的 tool_use。
      if (ev.kind === "tool" && ev.isResult && ev.callId) {
        const idx = prev.findLastIndex((m) => m.kind === "tool" && m.callId === ev.callId && !m.isResult);
        if (idx >= 0) {
          const next = [...prev];
          next[idx] = { ...next[idx], output: ev.output, isResult: true };
          return next;
        }
      }
      return [...prev, { ...ev, id: msgID.current }];
    });
  }, []);

  // 换对话对象：中断在途的那轮（cleanup 会 cancel），载入这个 agent 自己的
  // 历史，随后连接 effect 用新 id 重连。
  const switchAgent = useCallback((id: string) => {
    if (!id || id === selectedId) return;
    setBusy(false);
    setError("");
    setInput("");
    setMessages([]);
    setSelectedId(id);
  }, [selectedId]);

  // 载入当前 agent 的本机历史（在连接之前，避免首帧闪空再跳出来）。
  useEffect(() => {
    if (!selected) return;
    const hist = loadHistory(selected.id, selected.type === "steward");
    msgID.current = Math.max(msgID.current, ...hist.map((m) => m.id || 0)) + 1;
    setMessages(hist);
  }, [selectedId]); // eslint-disable-line react-hooks/exhaustive-deps

  // 连上 ACP，自动建会话（单一连续对话，会话存在 CLI 侧，刷新还在）。
  useEffect(() => {
    if (!selectedId) return;

    const client = new AcpChatClient({
      onEvent: push,
      onPermissionRequest: (req, respond) => setPerm({ req, respond }),
      onClose: () => {
        // 切换引擎后旧连接的关闭不打扰新会话的界面。
        if (clientRef.current === client) setError((e) => e || "连接已断开");
      },
    });
    clientRef.current = client;
    const host = window.location.host;
    client
      .connect(`ws${window.location.protocol === "https:" ? "s" : ""}://${host}/backend/agents/${selectedId}/acp`)
      .then(async () => {
        try {
          sessionRef.current = await client.newSession();
        } catch (e) {
          setError(`创建会话失败：${(e as Error).message}`);
        }
      })
      .catch((e) => {
        setError(`连接失败：${e.message}`);
        // 具体是「机器离线」还是「CLI 起不来」，追加一句能自己动手的提示。
        const sel = agents.find((a) => a.id === selectedId);
        if (sel?.type === "steward") {
          void describeStewardFailure(selectedId).then((why) => {
            if (why) setError((prev) => (prev.includes(why) ? prev : `${prev}　${why}`));
          });
        }
      });

    return () => {
      if (busyRef.current && sessionRef.current) client.cancel(sessionRef.current);
      client.close();
      clientRef.current = null;
      sessionRef.current = "";
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [selectedId]);

  useEffect(() => {
    if (selectedId) saveHistory(selectedId, messages);
  }, [messages, selectedId]);

  const clearHistory = useCallback(() => {
    setMessages([]);
    if (selectedId) {
      try { localStorage.removeItem(historyKey(selectedId)); } catch {}
    }
  }, [selectedId]);

  // 停止：只中断当前这一轮（session/cancel），会话和聊天记录都保留，
  // 随时可以接着追问。
  const stopTurn = useCallback(() => {
    const client = clientRef.current;
    if (client && sessionRef.current) client.cancel(sessionRef.current);
  }, []);

  const send = async (textOverride?: string) => {
    const client = clientRef.current;
    const text = (textOverride ?? input).trim();
    if (!client || !sessionRef.current || !text || busy) return;
    setInput("");
    agentActivity.current = false;
    push({ kind: "user", text });
    setBusy(true);
    try {
      await client.prompt(sessionRef.current, text);
    } catch (e) {
      // 切换引擎时旧连接被关掉：它的迟到报错不污染新会话。
      if (clientRef.current !== client) return;
      // 历史被污染：换新会话重试一次（与 chat-panel 同一套判断）。
      if (!agentActivity.current) {
        push({ kind: "agent", text: "（会话历史已损坏——自动新建会话重试）" });
        try {
          const sid = await client.newSession();
          sessionRef.current = sid;
          await client.prompt(sid, text);
        } catch (e2) {
          setError(`发送失败：${(e2 as Error).message}`);
        }
        setBusy(false);
        return;
      }
      setError(`发送失败：${(e as Error).message}`);
    }
    setBusy(false);
  };

  return (
    <div className={cn("flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg border border-line bg-surface-1", className)}>
      <header className="flex items-center gap-2.5 border-b border-line px-4 py-2.5">
        <span className={cn("h-1.5 w-1.5 shrink-0 rounded-full", error ? "bg-red-400" : selectedId ? "bg-accent" : "bg-zinc-400")} />
        {/* 切换引擎：换对话对象 = 换绑定的运行时/模型（agent 即引擎）。 */}
        <select
          value={selectedId ?? ""}
          onChange={(e) => switchAgent(e.target.value)}
          disabled={!agents.length}
          aria-label="切换对话引擎"
          title="切换引擎：选择要对话的智能体（每个智能体绑定自己的运行时/模型）"
          className="max-w-[200px] cursor-pointer truncate rounded-md border border-line bg-surface-2 px-1.5 py-1 text-sm font-medium text-ink outline-none transition-colors hover:border-accent-dim focus:border-accent-dim disabled:opacity-50"
        >
          {agents.map((a) => (
            <option key={a.id} value={a.id}>
              {a.name}{a.model ? ` · ${a.model}` : ""}
            </option>
          ))}
        </select>
        <span className="font-mono text-[11px] text-ink-3">ACP · 会话自动保存</span>
        {messages.length > 0 && (
          <button
            onClick={clearHistory}
            className="ml-auto flex items-center gap-1 rounded-md px-2 py-1 text-[11px] text-ink-3 transition-colors hover:bg-surface-2 hover:text-red-400"
            title="清空本机记录（不影响 CLI 侧会话）"
          >
            <Trash2 className="h-3.5 w-3.5" /> 清空
          </button>
        )}
      </header>

      <div ref={scrollRef} className="min-h-0 flex-1 space-y-3 overflow-y-auto px-4 py-4">
        {noAgents && (
          <StewardSetup
            className="mx-auto my-8 max-w-md"
            onCreated={(id) => {
              setNoAgents(false);
              setSelectedId(id);
            }}
          />
        )}
        {!noAgents && messages.length === 0 && (
          isStewardChat ? (
            <div className="py-6">
              <p className="mb-3 text-sm text-ink-2">跟管家说话，它会直接改这个平台：建任务、查状态、调配置。</p>
              <div className="flex flex-col items-start gap-1.5">
                {STARTERS.map((s) => (
                  <button
                    key={s}
                    onClick={() => send(s)}
                    disabled={busy || !sessionRef.current}
                    className="rounded-md border border-line bg-surface-2 px-2.5 py-1.5 text-left text-xs text-ink-2 transition-colors hover:border-accent/40 hover:text-ink disabled:opacity-50"
                  >
                    {s}
                  </button>
                ))}
              </div>
            </div>
          ) : (
            <div className="py-6">
              <p className="text-sm text-ink-2">
                正在与 <span className="font-medium text-ink">{selected?.name ?? "agent"}</span> 对话——用头部下拉可切回管家或换其他智能体。
              </p>
            </div>
          )
        )}
        {messages.map((m) => {
          if (m.kind === "thought") {
            return (
              <StreamCards
                key={m.id}
                items={[{ kind: "thought", key: String(m.id), content: m.text, isStreaming: busy }]}
              />
            );
          }
          if (m.kind === "tool") {
            return (
              <StreamCards
                key={m.id}
                items={[{
                  kind: "tool",
                  key: m.callId ?? String(m.id),
                  toolName: m.toolName ?? "tool",
                  input: "",
                  output: m.output ?? "",
                  hasResult: !!m.isResult,
                }]}
              />
            );
          }
          return (
            <div key={m.id} className={cn("flex", m.kind === "user" ? "justify-end" : "justify-start")}>
              <div
                className={cn(
                  "max-w-[85%] whitespace-pre-wrap break-words rounded-lg px-3 py-2 text-sm",
                  m.kind === "user" ? "bg-accent text-accent-ink" : "bg-surface-2 text-ink"
                )}
              >
                {m.text}
              </div>
            </div>
          );
        })}
        {busy && (
          <div className="flex items-center gap-3">
            <span className="text-sm text-ink-3">思考中…</span>
          </div>
        )}
        {error && <p className="text-xs text-red-400">{error}</p>}
      </div>

      <div className="flex gap-2 border-t border-line bg-surface-0/40 px-4 py-3">
        <input
          className="flex-1 rounded-md border border-line-strong bg-surface-2 px-3 py-2 text-sm text-ink outline-none transition-colors placeholder:text-ink-3 focus:border-accent-dim focus:ring-2 focus:ring-accent/15 disabled:opacity-50"
          placeholder={noAgents ? "管家未就绪…" : "输入消息，Enter 发送，Shift+Enter 换行"}
          value={input}
          onChange={(e) => setInput(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter" && !e.shiftKey) {
              e.preventDefault();
              send();
            }
          }}
          disabled={busy || noAgents}
        />
        {busy ? (
          // 停止按钮：中断当前这轮回复（会话与历史保留），发送位置切换显示。
          <Button variant="danger" onClick={stopTurn} className="gap-1.5" title="中断当前回复，不影响会话">
            <Square className="h-3.5 w-3.5 fill-current" /> 停止
          </Button>
        ) : (
          <Button onClick={() => send()} disabled={!input.trim() || noAgents} className="gap-1.5">
            <Send className="h-3.5 w-3.5" /> 发送
          </Button>
        )}
      </div>

      {perm && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/40">
          <div className="w-96 rounded-xl border border-line bg-surface-1 p-4 shadow-xl shadow-black/30">
            <p className="text-sm font-medium text-ink">{selected?.name ?? "管家"}请求权限</p>
            <p className="mt-1 text-xs text-ink-3">
              {perm.req.toolCall?.title || "tool"}（{perm.req.toolCall?.kind || "unknown"}）
            </p>
            <div className="mt-3 flex justify-end gap-2">
              {(perm.req.options ?? []).filter((o) => o.kind?.startsWith("allow")).map((o) => (
                <Button key={o.optionId} onClick={() => { perm.respond({ optionId: o.optionId }); setPerm(null); }}>
                  {o.name || "允许"}
                </Button>
              ))}
              {(perm.req.options ?? []).filter((o) => o.kind?.startsWith("reject")).map((o) => (
                <Button key={o.optionId} variant="outline" onClick={() => { perm.respond({ optionId: o.optionId }); setPerm(null); }}>
                  {o.name || "拒绝"}
                </Button>
              ))}
              <Button variant="outline" onClick={() => { perm.respond({ cancelled: true }); setPerm(null); }}>
                取消
              </Button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
