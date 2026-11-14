// A minimal ACP client over WebSocket (Phase 6 chat). Speaks the STANDARD
// ACP lifecycle — initialize, session/list, session/new, session/load,
// session/prompt, event streaming, permission requests — against our own
// daemon endpoint (GET /agents/{id}/acp), which relays the frames
// unparsed to the machine. Protocol shapes mirror internal/acp.
//
// Reliability contract (bug 2 — /goals/new "交给管家创建" hung silently):
//   * Every HANDSHAKE step has a deadline (connect 15s, initialize 20s,
//     session/new 30s, session/list 20s). A turn (session/prompt) still has
//     no artificial limit — an agent takes as long as it takes; its socket
//     death rejects it immediately.
//   * Every failure message carries the phase and, for socket deaths, the
//     real WS close code + reason, so the UI can show "why" instead of
//     spinning forever.
//   * onclose rejects ALL pending requests (each labelled with its phase)
//     and fires onClose with the close info, including when the owner
//     abandons the client during a handshake.

export interface SessionInfo {
  sessionId: string;
  cwd: string;
  title?: string;
  updatedAt?: string;
}

export interface PermissionOption {
  optionId: string;
  name: string;
  kind: "allow_once" | "allow_always" | "reject_once" | "reject_always";
}

export interface PermissionRequest {
  sessionId: string;
  toolCall: { title?: string; kind?: string; status?: string };
  options: PermissionOption[];
}

export interface ChatEvent {
  kind: "user" | "agent" | "thought" | "tool";
  text: string;
  // tool-only fields — let the chat panel pair a tool_call (use) with its
  // tool_call_update (result) by callId, the same way the run-detail stream
  // pairs chat_message rows. Without these, a tool call rendered as two
  // unrelated bubbles (a "🔧 title" line + a "↳ output" line).
  callId?: string;
  toolName?: string;
  output?: string;
  isResult?: boolean;
}

// AcpPhase names the lifecycle step a close/timeout happened in. Included in
// every error message so "连接已关闭" is never the whole story.
export type AcpPhase =
  | "connect"
  | "initialize"
  | "session/list"
  | "session/new"
  | "session/load"
  | "session/prompt"
  | "ready"
  | "closed";

export interface AcpCloseInfo {
  code: number;
  reason: string;
  phase: AcpPhase;
}

export interface AcpChatCallbacks {
  onEvent?: (ev: ChatEvent) => void;
  onPermissionRequest?: (
    req: PermissionRequest,
    respond: (outcome: { optionId?: string; cancelled?: boolean }) => void,
  ) => void;
  onClose?: (info?: AcpCloseInfo) => void;
}

// Handshake deadlines. Deliberately distinct from the (absent) turn deadline:
// a spawn/initialize/session handshake that has not answered by then is a
// half-dead channel, not a slow agent. See the header contract.
const CONNECT_OPEN_TIMEOUT_MS = 15_000;
const INITIALIZE_TIMEOUT_MS = 20_000;
const SESSION_LIST_TIMEOUT_MS = 20_000;
const SESSION_NEW_TIMEOUT_MS = 30_000;

interface PendingEntry {
  resolve: (v: unknown) => void;
  reject: (e: Error) => void;
  method: string;
  timer?: ReturnType<typeof setTimeout>;
}

// Human label per JSON-RPC method — the phase users see in errors.
function methodLabel(method: string): string {
  switch (method) {
    case "initialize":
      return "初始化(initialize)";
    case "session/new":
      return "创建会话(session/new)";
    case "session/list":
      return "拉取会话列表(session/list)";
    case "session/load":
      return "加载会话(session/load)";
    case "session/prompt":
      return "本轮对话(session/prompt)";
    default:
      return method;
  }
}

// formatAcpClose renders a WS close event as "code + reason (+ phase)" for
// both the class's rejections and the page banner.
export function formatAcpClose(info: AcpCloseInfo): string {
  let codeText: string;
  switch (info.code) {
    case 1000:
      codeText = "1000，正常关闭";
      break;
    case 1001:
      codeText = "1001，端点离开";
      break;
    case 1005:
      codeText = "1005，没有状态码";
      break;
    case 1006:
      codeText = "1006，连接异常中断（没有收到关闭帧）";
      break;
    default:
      codeText = `${info.code}`;
  }
  const reason = info.reason ? `，原因: ${info.reason}` : "，未提供关闭原因";
  return `连接已关闭(code ${codeText}${reason}；阶段: ${info.phase})`;
}

export class AcpChatClient {
  private ws?: WebSocket;
  private id = 0;
  // closed marks an abandoned/closed client: React StrictMode runs the effect
  // twice (mount → cleanup → mount), and a retry/「重来」 closes the old
  // client. A late onopen must self-close instead of leaking a machine-side
  // chat channel, and a late onclose must not fire the owner's onClose.
  private closed = false;
  private phase: AcpPhase = "connect";
  private pending = new Map<number, PendingEntry>();
  private cb: AcpChatCallbacks;

  constructor(cb: AcpChatCallbacks) {
    this.cb = cb;
  }

  connect(url: string): Promise<void> {
    return new Promise((resolve, reject) => {
      const ws = new WebSocket(url);
      // Publish the socket before it opens: close() (unmount/retry) must be
      // able to abort a CONNECTING socket, otherwise the first render's
      // abandoned attempt keeps a machine-side chat channel open.
      this.ws = ws;
      let settled = false;
      let openTimer: ReturnType<typeof setTimeout> | undefined;

      const fail = (err: Error) => {
        if (settled) return;
        settled = true;
        if (openTimer) clearTimeout(openTimer);
        this.close();
        reject(err);
      };

      openTimer = setTimeout(() => {
        fail(
          new Error(
            `连接超时(${CONNECT_OPEN_TIMEOUT_MS / 1000}s；阶段: connect)：WebSocket 没有打开——daemon 不可达，或反代没有转发 upgrade`,
          ),
        );
      }, CONNECT_OPEN_TIMEOUT_MS);

      ws.onopen = () => {
        if (openTimer) clearTimeout(openTimer);
        if (this.closed) {
          ws.close();
          return;
        }
        this.phase = "initialize";
        this.initialize().then(
          () => {
            if (settled) return;
            settled = true;
            this.phase = "ready";
            resolve();
          },
          (e) => {
            const msg = (e as Error)?.message ?? String(e);
            fail(new Error(`初始化失败（阶段: initialize）：${msg}`));
          },
        );
      };
      // No rejection here: the browser always follows an error event with a
      // close event, and THAT carries the code/reason we want to show. The
      // open timeout above covers a socket that never gets either.
      ws.onerror = () => {};
      ws.onclose = (e) => {
        if (openTimer) clearTimeout(openTimer);
        this.ws = undefined;
        if (this.closed) {
          // Deliberate close (unmount / retry): don't fire onClose, but never
          // leave an awaited connect()/send() dangling.
          if (!settled) {
            settled = true;
            reject(new Error("连接已取消（客户端已关闭）"));
          }
          this.rejectPending("连接已关闭（客户端主动关闭）");
          return;
        }
        const info: AcpCloseInfo = {
          code: e.code,
          reason: e.reason ?? "",
          phase: this.phase,
        };
        const desc = formatAcpClose(info);
        if (!settled) {
          settled = true;
          reject(new Error(desc));
        }
        // A dead connection rejects every in-flight request with its own
        // phase: the UI unblocks the moment the socket actually dies.
        this.rejectPending(desc);
        this.cb.onClose?.(info);
      };
      ws.onmessage = (e) => this.onMessage(String(e.data));
    });
  }

  close() {
    if (!this.closed) this.closed = true;
    const ws = this.ws;
    this.ws = undefined;
    this.phase = "closed";
    if (ws) {
      try {
        // A real close frame (not just TCP FIN) so proxies/middleboxes
        // forward the teardown instead of holding a half-open socket.
        ws.close(1000, "client closed");
      } catch {
        try {
          ws.close();
        } catch {
          // already closing — nothing to do
        }
      }
    }
    // Reject in-flight work even on a deliberate close: an awaited caller
    // must not hang forever behind an abandoned socket.
    this.rejectPending("连接已关闭（客户端主动关闭）");
  }

  private rejectPending(reason: string) {
    const entries = [...this.pending.values()];
    this.pending.clear();
    for (const p of entries) {
      if (p.timer) clearTimeout(p.timer);
      p.reject(new Error(`${reason}；进行中: ${methodLabel(p.method)}`));
    }
  }

  private send(method: string, params: unknown, timeoutMs = 0): Promise<unknown> {
    return new Promise((resolve, reject) => {
      if (this.closed) return reject(new Error("连接尚未建立（客户端已关闭）"));
      if (!this.ws) return reject(new Error(`连接尚未建立（阶段: ${method}）`));
      const id = ++this.id;
      const entry: PendingEntry = { resolve, reject, method };
      this.pending.set(id, entry);
      if (timeoutMs > 0) {
        entry.timer = setTimeout(() => {
          if (!this.pending.delete(id)) return;
          // Timeout must also prove the channel is dead — otherwise a half-
          // open socket would survive and the retry would race it.
          this.close();
          reject(
            new Error(
              `${methodLabel(method)} 超时(${Math.round(timeoutMs / 1000)}s；阶段: ${method})：机器在期限内没有回应——CLI 可能没启动、机器离线，或连接处于半死状态`,
            ),
          );
        }, timeoutMs);
      }
      try {
        this.ws.send(JSON.stringify({ jsonrpc: "2.0", id, method, params }));
      } catch (e) {
        if (this.pending.delete(id) && entry.timer) clearTimeout(entry.timer);
        reject(new Error(`发送失败（阶段: ${method}）：${(e as Error).message}`));
      }
      // session/prompt intentionally has no deadline: a real turn takes as
      // long as it takes, and onclose rejects it the moment the socket dies.
    });
  }

  private onMessage(raw: string) {
    let msg: any;
    try {
      msg = JSON.parse(raw);
    } catch {
      return;
    }
    if (typeof msg.id !== "undefined" && msg.id !== null && msg.method === undefined) {
      // Response to one of our requests.
      const p = this.pending.get(msg.id);
      if (!p) return;
      this.pending.delete(msg.id);
      if (p.timer) clearTimeout(p.timer);
      if (msg.error) p.reject(new Error(msg.error.message ?? "acp error"));
      else p.resolve(msg.result);
      return;
    }
    if (msg.method === "session/update") {
      const u = msg.params?.update ?? {};
      const ev = updateToEvent(u);
      if (ev) this.cb.onEvent?.(ev);
      return;
    }
    if (msg.method === "session/request_permission") {
      const req = msg.params as PermissionRequest;
      const opts = req?.options ?? [];
      this.cb.onPermissionRequest?.(req, (outcome) => {
        // The ACP outcome is a tagged union: {"outcome":"selected",
        // "optionId":...} or {"outcome":"cancelled"} — the discriminant
        // is REQUIRED (live: omitting it made opencode read our approvals
        // as rejections; the VS Code client sends "selected" and works).
        // Also validate against the request's own options: a foreign or
        // empty optionId must not masquerade as a selection.
        let oc: Record<string, unknown>;
        if (
          outcome.optionId !== undefined &&
          opts.some((o) => o.optionId === outcome.optionId && o.optionId !== "")
        ) {
          oc = { outcome: "selected", optionId: outcome.optionId };
        } else {
          oc = { outcome: "cancelled" };
        }
        this.ws?.send(
          JSON.stringify({
            jsonrpc: "2.0",
            id: msg.id,
            result: { outcome: oc },
          }),
        );
      });
      return;
    }
    // fs/terminal delegations and other agent→client requests: respond
    // with an error so the agent's tool call fails visibly instead of
    // hanging.
    if (typeof msg.id !== "undefined" && msg.id !== null && msg.method) {
      this.ws?.send(
        JSON.stringify({
          jsonrpc: "2.0",
          id: msg.id,
          error: { code: -32601, message: "not supported by the web chat client" },
        }),
      );
    }
  }

  initialize(): Promise<void> {
    this.phase = "initialize";
    // Timeout is enforced in send(); this method only mirrors the phase.
    return this.send(
      "initialize",
      {
        protocolVersion: 2,
        capabilities: { session: {} },
        info: { name: "agentwork-web", title: "Agentwork Web", version: "1.0.0" },
        clientInfo: { name: "agentwork-web", title: "Agentwork Web", version: "1.0.0" },
      },
      INITIALIZE_TIMEOUT_MS,
    ).then(() => undefined);
  }

  // The web never sends a cwd: the machine spawns the CLI with its process
  // cwd already set to the agent's chat directory, and the ACP session
  // defaults to it. Sending a path made the CLI resolve a doubled relative
  // path (a '~' the CLI does not expand) — lstat ENOENT, "service failure".
  listSessions(): Promise<SessionInfo[]> {
    this.phase = "session/list";
    return this.send("session/list", {}, SESSION_LIST_TIMEOUT_MS)
      .then((r) => (r as { sessions: SessionInfo[] }).sessions ?? [])
      .finally(() => {
        if (this.phase === "session/list") this.phase = "ready";
      });
  }

  newSession(): Promise<string> {
    this.phase = "session/new";
    return this.send("session/new", { mcpServers: [] }, SESSION_NEW_TIMEOUT_MS)
      .then((r) => (r as { sessionId: string }).sessionId)
      .finally(() => {
        if (this.phase === "session/new") this.phase = "ready";
      });
  }

  loadSession(sessionId: string, cwd: string): Promise<void> {
    // History replays as session/update events during the load response.
    // cwd is REQUIRED by the CLIs (the session store is keyed by project
    // directory) — take it from the session/list entry. Only ABSOLUTE
    // paths are trusted: a '~' form (a poisoned entry from an early bug,
    // or any non-absolute value) would be resolved by the CLI against the
    // process cwd into a doubled path — omit it and the CLI falls back to
    // the spawn cwd (the agent's chat directory), where chat sessions
    // actually live.
    const params: Record<string, unknown> = { sessionId, mcpServers: [] };
    if (cwd.startsWith("/")) params.cwd = cwd;
    this.phase = "session/load";
    return this.send("session/load", params)
      .then(() => undefined)
      .finally(() => {
        if (this.phase === "session/load") this.phase = "ready";
      });
  }

  prompt(sessionId: string, text: string): Promise<void> {
    // The ACP shape: a flat array of ContentBlock — the same shape the
    // Go client sends (verified against claude/opencode in the run flow).
    this.phase = "session/prompt";
    return this.send("session/prompt", {
      sessionId,
      prompt: [{ type: "text", text }],
    })
      .then(() => undefined)
      .finally(() => {
        if (this.phase === "session/prompt") this.phase = "ready";
      });
  }

  cancel(sessionId: string): void {
    // session/cancel is a NOTIFICATION per the spec (the Go client sends
    // it without an id): no response comes back. The interrupted turn's
    // session/prompt response (stopReason cancelled) ends the busy state.
    if (!this.ws) return;
    this.ws.send(
      JSON.stringify({
        jsonrpc: "2.0",
        method: "session/cancel",
        params: { sessionId },
      }),
    );
  }
}

// updateToEvent maps one ACP session/update to a chat transcript entry
// ('' for updates the panel does not render).
function updateToEvent(u: any): ChatEvent | null {
  const kind = u.sessionUpdate;
  switch (kind) {
    case "user_message_chunk":
    case "agent_message_chunk": {
      const block = u.content;
      const text = typeof block?.text === "string" ? block.text : "";
      if (!text) return null;
      return { kind: kind === "user_message_chunk" ? "user" : "agent", text };
    }
    case "agent_thought_chunk": {
      const t = typeof u.content === "string" ? u.content : "";
      return t ? { kind: "thought", text: t } : null;
    }
    case "tool_call": {
      // Carry the callId + toolName so the panel pairs this use with its
      // later result update — no emoji in the text (the renderer styles it).
      return { kind: "tool", text: "", callId: u.toolCallId, toolName: u.title ?? "tool" };
    }
    case "tool_call_update": {
      const st = u.status ?? "";
      // failed 也是终态——丢弃会让工具卡永远停在「运行中…」。
      if (st !== "completed" && st !== "failed") return null;
      const raw = typeof u.rawOutput === "string" ? u.rawOutput : JSON.stringify(u.rawOutput ?? "");
      const out = st === "failed" ? `（失败）${raw}` : raw;
      return { kind: "tool", text: "", callId: u.toolCallId, output: out, isResult: true };
    }
    default:
      return null;
  }
}