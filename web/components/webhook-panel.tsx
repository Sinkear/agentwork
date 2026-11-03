"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { Copy, KeyRound, RefreshCw } from "lucide-react";
import { Button, Skeleton, inputCls } from "@/components/ui";
import { useDomainWebhook, useRegisterDomainWebhook, useSetDomainWebhookSecret } from "@/lib/queries";
import { cn, relTime } from "@/lib/utils";

// SECTION_LABEL — the one small-caps label style used by every block here.
const SECTION_LABEL = "font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3";

type Tone = "muted" | "accent" | "amber" | "green" | "red";

const TONE_CLS: Record<Tone, string> = {
  muted: "border-line text-ink-3",
  accent: "border-accent/40 text-accent",
  amber: "border-amber-500/30 text-amber-400",
  green: "border-green-500/30 text-green-400",
  red: "border-red-500/30 text-red-400",
};

// Chip is the panel-local mono tag (event names, hook ids, active state).
function Chip({ tone = "muted", children }: { tone?: Tone; children: React.ReactNode }) {
  return (
    <span className={`inline-flex items-center gap-1 rounded border px-1.5 py-0.5 font-mono text-[10px] ${TONE_CLS[tone]}`}>
      {children}
    </span>
  );
}

// describeDeliveryError humanizes the few error strings the backend records at
// the transport stage ("bad signature" etc.) so the log is readable at a glance;
// anything unknown (provider API text) is shown as-is.
function describeDeliveryError(err: string): string {
  if (err === "bad signature") return "签名不匹配 —— 仓库侧 secret 与这里不一致";
  if (err === "webhook not configured (no secret)") return "没有可用的 secret，无法校验签名";
  if (err.startsWith("webhook secret lookup:")) return "读取仓库 secret 失败";
  if (err.startsWith("parse webhook payload:")) return "投递负载无法解析";
  return err;
}

// describeHookError explains why the host hook list could not be read: the
// overwhelmingly common cause is a token without repo admin scope.
function describeHookError(err: string): string {
  const e = err || "未知错误";
  const authIssue = /401|403|bad credentials|not found/i.test(e);
  const hint = authIssue ? " —— 平台令牌无效或没有该仓库的 admin 权限" : "";
  const short = e.length > 160 ? e.slice(0, 160) + "…" : e;
  return `读取宿主 hook 失败：${short}${hint}`;
}

// Dot mirrors Badge's status dot: green = connected, amber = needs attention.
function Dot({ tone }: { tone: "green" | "amber" | "muted" }) {
  const cls = tone === "green" ? "bg-green-400" : tone === "amber" ? "bg-amber-400" : "bg-zinc-400";
  return <span className={`h-1.5 w-1.5 rounded-full ${cls}`} />;
}

// WebhookPanel shows one repo domain's webhook wiring: the URL to paste into
// the host repo, the effective secret source, the host's registered hooks
// (GitHub only — GitCode reports supported:false) and the latest deliveries.
// The delivery log is the ground truth "is it actually connected?" — it works
// even when the token lacks hook-management permission.
export function WebhookPanel({
  domainId,
  provider,
  repo,
}: {
  domainId: string;
  provider: string;
  repo: string;
}) {
  // Hooks stay unconditional: `repo` is the dialog's live form value and may
  // flip between renders; `enabled` gates the request instead.
  const q = useDomainWebhook(domainId, !!repo);
  const register = useRegisterDomainWebhook(domainId);
  const saveSecret = useSetDomainWebhookSecret(domainId);
  const [origin, setOrigin] = useState("");
  const [copied, setCopied] = useState(false);
  const [registered, setRegistered] = useState(false);
  // Repo secret editor — the panel is the only place this value can be typed.
  const [showSecretEditor, setShowSecretEditor] = useState(false);
  const [secretDraft, setSecretDraft] = useState("");
  const [secretSaved, setSecretSaved] = useState(false);
  const [confirmClear, setConfirmClear] = useState(false);
  const copyTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const savedTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const clearTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

  useEffect(() => {
    setOrigin(window.location.origin);
    return () => {
      if (copyTimer.current) clearTimeout(copyTimer.current);
      if (savedTimer.current) clearTimeout(savedTimer.current);
      if (clearTimer.current) clearTimeout(clearTimer.current);
    };
  }, []);

  if (!repo) {
    return (
      <div className="rounded-lg border border-line bg-surface-2 px-3 py-2.5 text-xs text-ink-3">
        填好跟踪仓库后，这里会给出 webhook 地址和接通状态。
      </div>
    );
  }

  const status = q.data;
  const hooks = status?.hooks;
  const required = status?.events_required ?? ["issues", "pull_request"];
  const webhookUrl = origin && status ? `${origin}${status.path}` : (status?.path ?? "");

  const copy = async () => {
    if (!webhookUrl) return;
    try {
      await navigator.clipboard.writeText(webhookUrl);
      setCopied(true);
      if (copyTimer.current) clearTimeout(copyTimer.current);
      copyTimer.current = setTimeout(() => setCopied(false), 2000);
    } catch {
      // Clipboard unavailable (insecure origin) — the URL stays selectable.
    }
  };

  const doRegister = () => {
    if (!webhookUrl || !status?.secret_set) return;
    setRegistered(false);
    register.mutate({ url: webhookUrl }, { onSuccess: () => setRegistered(true) });
  };

  const markSecretSaved = () => {
    setSecretSaved(true);
    if (savedTimer.current) clearTimeout(savedTimer.current);
    savedTimer.current = setTimeout(() => setSecretSaved(false), 2000);
  };

  const submitSecret = () => {
    const v = secretDraft.trim();
    if (!v || saveSecret.isPending) return;
    saveSecret.mutate(v, {
      onSuccess: () => {
        setShowSecretEditor(false);
        setSecretDraft("");
        markSecretSaved();
      },
    });
  };

  // Clearing needs a second click: the first turns the button red and starts a
  // 2.5s window (inline confirmation beats a browser confirm() here).
  const requestClear = () => {
    if (saveSecret.isPending) return;
    if (!confirmClear) {
      setConfirmClear(true);
      if (clearTimer.current) clearTimeout(clearTimer.current);
      clearTimer.current = setTimeout(() => setConfirmClear(false), 2500);
      return;
    }
    if (clearTimer.current) clearTimeout(clearTimer.current);
    setConfirmClear(false);
    setShowSecretEditor(false);
    saveSecret.mutate("", { onSuccess: () => markSecretSaved() });
  };

  // Overall state: connected = a hook covers both required events AND at
  // least one delivery succeeded (the host hook alone proves nothing).
  const covered = !!hooks && hooks.items.some((h) => required.every((e) => h.events.includes(e)));
  const hasOkDelivery = !!status && status.deliveries.some((d) => d.ok);
  let state: { label: string; tone: "green" | "amber" | "muted" } = {
    label: q.isError ? "检测失败" : "检测中",
    tone: "muted",
  };
  if (hooks) {
    if (!hooks.supported) state = { label: "需手动配置", tone: "amber" };
    // A failed hook probe is NOT a broken webhook — it usually means the token
    // can't read /repos/{repo}/hooks. Say what is true, not "检测失败".
    else if (!hooks.ok) state = { label: "无法自动检测", tone: "amber" };
    else if (hooks.items.length === 0) state = { label: "未注册", tone: "muted" };
    else if (!covered) state = { label: "缺事件", tone: "amber" };
    else if (!hasOkDelivery) state = { label: "待投递", tone: "muted" };
    else state = { label: "已接通", tone: "green" };
  }

  return (
    <div className="rounded-lg border border-line bg-surface-1 p-4 space-y-4">
      <div className="flex flex-wrap items-center gap-2">
        <span className={SECTION_LABEL}>WEBHOOK 接入</span>
        {provider && (
          <Chip>{provider === "github" ? "GitHub" : provider === "gitcode" ? "GitCode" : provider}</Chip>
        )}
        <span className="ml-auto inline-flex items-center gap-1.5 text-xs text-ink-2">
          <Dot tone={state.tone} />
          {state.label}
        </span>
        <Button
          type="button"
          variant="ghost"
          onClick={() => void q.refetch()}
          disabled={q.isFetching}
          className="h-6 gap-1 px-1.5 text-xs"
        >
          <RefreshCw className="h-3.5 w-3.5" />
          检测
        </Button>
      </div>

      {q.isLoading ? (
        <div className="space-y-2">
          <Skeleton className="h-8 w-full" />
          <Skeleton className="h-4 w-2/3" />
          <Skeleton className="h-4 w-1/2" />
        </div>
      ) : !status ? (
        <p className="text-xs text-red-400">{String(q.error ?? "无法读取 webhook 状态")}</p>
      ) : (
        <>
          <div className="flex items-center gap-2">
            <code
              className="min-w-0 flex-1 truncate rounded border border-line bg-surface-2 px-2 py-1.5 font-mono text-xs text-ink-2"
              title={webhookUrl}
            >
              {webhookUrl}
            </code>
            <Button
              type="button"
              variant="outline"
              onClick={() => void copy()}
              className="h-7 shrink-0 gap-1 px-2 text-xs"
            >
              <Copy className="h-3.5 w-3.5" />
              {copied ? "已复制" : "复制"}
            </Button>
          </div>

          <div className="flex flex-wrap items-center gap-1.5 text-xs text-ink-3">
            <span className={SECTION_LABEL}>事件</span>
            {required.map((ev) => (
              <Chip key={ev}>{ev}</Chip>
            ))}
            <span>宿主仓库的 webhook 需要同时订阅这两个事件</span>
          </div>

          <div className="space-y-1.5">
            <div className="flex flex-wrap items-center gap-1.5 text-xs">
              <KeyRound className="h-3.5 w-3.5 shrink-0 text-ink-3" />
              {status.secret_source === "repo" ? (
                <span className="text-green-400">仓库专属 secret</span>
              ) : status.secret_source === "platform" ? (
                <span className="text-ink-2">使用平台 secret</span>
              ) : (
                <>
                  <span className="text-amber-400">未配置 secret——请先配置平台 secret，或给仓库单独设置</span>
                  <Link href="/settings" className="text-accent hover:underline">
                    去设置
                  </Link>
                </>
              )}
              <span className="ml-auto inline-flex shrink-0 items-center gap-1.5">
                {secretSaved && <span className="text-green-400">已保存</span>}
                {status.secret_source === "repo" ? (
                  <>
                    <Button
                      type="button"
                      variant="outline"
                      onClick={() => {
                        setShowSecretEditor((v) => !v);
                        setConfirmClear(false);
                      }}
                      className="h-6 px-2 text-xs"
                    >
                      {showSecretEditor ? "收起" : "修改"}
                    </Button>
                    <Button
                      type="button"
                      variant="outline"
                      onClick={requestClear}
                      disabled={saveSecret.isPending}
                      className={cn("h-6 px-2 text-xs", confirmClear && "border-red-500/30 text-red-400")}
                    >
                      {saveSecret.isPending && saveSecret.variables === ""
                        ? "清除中…"
                        : confirmClear
                          ? "确认清除？"
                          : "清除"}
                    </Button>
                  </>
                ) : (
                  <Button
                    type="button"
                    variant="outline"
                    onClick={() => setShowSecretEditor((v) => !v)}
                    className="h-6 px-2 text-xs"
                  >
                    {showSecretEditor ? "收起" : "设置"}
                  </Button>
                )}
              </span>
            </div>
            {showSecretEditor && (
              <div className="flex flex-wrap items-center gap-2">
                <input
                  type="password"
                  autoComplete="new-password"
                  value={secretDraft}
                  onChange={(e) => setSecretDraft(e.target.value)}
                  onKeyDown={(e) => {
                    // 面板在编辑 domain 的 <form> 里：Enter 必须拦下，改为保存，
                    // 否则会提交父表单并关掉整个对话框。
                    if (e.key === "Enter") {
                      e.preventDefault();
                      submitSecret();
                    }
                  }}
                  placeholder="至少 16 位随机字符，如 openssl rand -hex 24"
                  className={cn(inputCls, "min-w-0 flex-1 font-mono text-xs")}
                />
                <Button
                  type="button"
                  onClick={submitSecret}
                  disabled={saveSecret.isPending || !secretDraft.trim()}
                  className="h-8 shrink-0 px-2.5 text-xs"
                >
                  {saveSecret.isPending ? "保存中…" : "保存"}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => {
                    setShowSecretEditor(false);
                    setSecretDraft("");
                  }}
                  disabled={saveSecret.isPending}
                  className="h-8 shrink-0 px-2.5 text-xs"
                >
                  取消
                </Button>
              </div>
            )}
            {saveSecret.isError && <p className="text-xs text-red-400">{String(saveSecret.error)}</p>}
          </div>

          <div className="space-y-2">
            <span className={SECTION_LABEL}>宿主 hook</span>
            {status.hooks.ok ? (
              status.hooks.items.length === 0 ? (
                <p className="text-xs text-ink-3">宿主仓库上还没有注册这个地址的 webhook。</p>
              ) : (
                <div className="divide-y divide-line rounded border border-line">
                  {status.hooks.items.map((h) => (
                    <div key={h.id} className="flex flex-wrap items-center gap-2 px-2.5 py-1.5">
                      <span className="font-mono text-[10px] text-ink-3">#{h.id}</span>
                      <span className="min-w-0 flex-1 truncate font-mono text-xs text-ink-2" title={h.url}>
                        {h.url}
                      </span>
                      <Chip tone={h.active ? "green" : "muted"}>{h.active ? "active" : "inactive"}</Chip>
                      {required.map((ev) =>
                        h.events.includes(ev) ? (
                          <Chip key={ev}>{ev}</Chip>
                        ) : (
                          <Chip key={ev} tone="amber">
                            {ev} 缺失
                          </Chip>
                        ),
                      )}
                    </div>
                  ))}
                </div>
              )
            ) : (
              <p className="text-xs text-ink-3">{describeHookError(status.hooks.error)}</p>
            )}
          </div>

          <div className="flex flex-wrap items-center gap-2">
            {status.hooks.supported && (
              <Button
                type="button"
                onClick={doRegister}
                disabled={!status.secret_set || register.isPending}
                className="h-7 px-2.5 text-xs"
              >
                {register.isPending ? "注册中…" : "注册到仓库"}
              </Button>
            )}
            {!status.secret_set && <span className="text-xs text-amber-400">没有可用的 secret，无法注册</span>}
            {registered && <span className="text-xs text-green-400">已注册：issues + pull_request</span>}
            {register.isError && <span className="text-xs text-red-400">{String(register.error)}</span>}
          </div>

          <div className="space-y-1.5 border-t border-line pt-3">
            <span className={SECTION_LABEL}>最近投递</span>
            {status.deliveries.length === 0 ? (
              <p className="text-xs text-ink-3">还没有收到投递——在宿主仓库发一个 issue 或 PR 试试。</p>
            ) : (
              <div className="space-y-1">
                {status.deliveries.slice(0, 5).map((d, i) => (
                  <div key={`${d.received_at}-${i}`} className="flex items-center gap-2 font-mono text-xs">
                    <span className={d.ok ? "text-green-400" : "text-red-400"}>{d.ok ? "ok" : "fail"}</span>
                    <span className="text-ink-2">
                      {d.event}
                      {d.action ? ` · ${d.action}` : ""}
                    </span>
                    {d.error && (
                      <span className="min-w-0 truncate text-ink-3" title={d.error}>
                        {describeDeliveryError(d.error)}
                      </span>
                    )}
                    <span className="ml-auto shrink-0 text-ink-3">{relTime(d.received_at)}</span>
                  </div>
                ))}
              </div>
            )}
          </div>
        </>
      )}
    </div>
  );
}