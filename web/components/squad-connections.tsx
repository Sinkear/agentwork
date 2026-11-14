"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import Link from "next/link";
import { Check, Copy, Pencil, RefreshCw, Trash2, Webhook as WebhookIcon } from "lucide-react";
import {
  Button,
  ConfirmDialog,
  Dialog,
  Empty,
  Field,
  SectionCard,
  Skeleton,
  inputCls,
} from "@/components/ui";
import {
  useConnectionWebhook,
  useConnections,
  useCreateConnection,
  useDeleteConnection,
  useDomains,
  useRegisterConnectionWebhook,
  useUpdateConnection,
} from "@/lib/queries";
import { cn, relTime } from "@/lib/utils";
import type { Connection, ConnectionWebhookStatus, Domain } from "@/lib/types";

// 每条 connection 有自己的接收端点（/webhooks/{provider}/{id}）和自己的
// secret，所以同一仓库被多个小队同时接入时互不覆盖——这是整个区块的前提。
const SECTION_LABEL = "font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3";

const EVENT_LABEL: Record<string, string> = { issues: "Issue", pull_request: "PR" };
const EVENT_OPTIONS: { value: string; label: string; hint: string }[] = [
  { value: "issues", label: "Issue", hint: "新建 / 重开 issue" },
  { value: "pull_request", label: "PR", hint: "新建 / 更新 PR" },
];

const PROVIDER_LABEL: Record<string, string> = { github: "GitHub", gitcode: "GitCode" };
const providerLabel = (p: string) => PROVIDER_LABEL[p] ?? (p || "未知");
const eventLabel = (ev: string) => EVENT_LABEL[ev] ?? ev;

// useOrigin resolves the site origin after mount (SSR-safe): the webhook URL
// shown to the human is the full address the host repo must call.
function useOrigin() {
  const [origin, setOrigin] = useState("");
  useEffect(() => {
    setOrigin(window.location.origin);
  }, []);
  return origin;
}

// useCopy keeps one "已复制" flash at a time; `key` scopes it per button so
// the list's URL copy and the dialog's secret copy don't light up together.
function useCopy() {
  const [copied, setCopied] = useState("");
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
    },
    [],
  );
  const copy = async (key: string, text: string) => {
    if (!text) return;
    try {
      await navigator.clipboard.writeText(text);
      setCopied(key);
      if (timer.current) clearTimeout(timer.current);
      timer.current = setTimeout(() => setCopied(""), 2000);
    } catch {
      // insecure origin（http）下剪贴板不可用：地址/secret 仍然可选可读。
    }
  };
  return { copied, copy };
}

// webhookState derives the card's connection state from the host probe.
// A failed probe is not a broken webhook (usually a token without hook
// permission), so it says "无法自动检测" instead of "检测失败".
type HookTone = "green" | "amber" | "muted";

// hookMatchesPath reports whether a host hook URL points at THIS connection.
// The host returns full URLs; a bare path is accepted too. Matching on the
// path (not just "some hook covers the events") is what keeps two squads on
// the same repo from both claiming 已注册 when only one endpoint is wired.
function hookMatchesPath(hookURL: string, path: string): boolean {
  if (!hookURL || !path) return false;
  try {
    return new URL(hookURL).pathname === path;
  } catch {
    return hookURL.endsWith(path);
  }
}

function webhookState(status?: ConnectionWebhookStatus): { label: string; tone: HookTone; cls: string } | null {
  if (!status) return null;
  if (!status.hooks.supported) return { label: "需手动配置", tone: "amber", cls: "text-amber-400" };
  if (!status.hooks.ok) return { label: "无法自动检测", tone: "amber", cls: "text-amber-400" };
  const own = status.hooks.items.filter((h) => hookMatchesPath(h.url, status.path));
  if (own.length === 0) return { label: "未注册", tone: "muted", cls: "text-ink-3" };
  const covered = own.some((h) => status.events_required.every((e) => h.events.includes(e)));
  if (!covered) return { label: "缺事件", tone: "amber", cls: "text-amber-400" };
  return { label: "已注册", tone: "green", cls: "text-green-400" };
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

function SectionLabel({ children }: { children: React.ReactNode }) {
  return <span className={SECTION_LABEL}>{children}</span>;
}

function EventChip({ event }: { event: string }) {
  return (
    <span className="inline-flex shrink-0 items-center rounded border border-line bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] text-ink-2">
      {eventLabel(event)}
    </span>
  );
}

// ── 小队详情页的「仓库接入」区块 ──
export function SquadConnections({ squadId }: { squadId: string }) {
  const { data: connections, isLoading } = useConnections(squadId);
  const { data: domains } = useDomains();
  const remove = useDeleteConnection();
  const [showCreate, setShowCreate] = useState(false);
  const [editTarget, setEditTarget] = useState<Connection | null>(null);
  const [deleteTarget, setDeleteTarget] = useState<Connection | null>(null);

  const list = connections ?? [];
  const domainOf = (id: string) => domains?.find((d) => d.id === id);

  return (
    <div id="repo-connections" className="scroll-mt-24">
      <SectionCard
        title="仓库接入"
        count={connections?.length}
        desc="每条连接 = 独立 webhook；同一仓库可被多个小队同时接入，互不覆盖"
        bodyClassName="p-0"
        action={
          <Button onClick={() => setShowCreate(true)} className="h-7 px-2.5 text-xs">
            + 新建连接
          </Button>
        }
      >
        {isLoading ? (
          <div className="divide-y divide-line">
            {[0, 1].map((i) => (
              <div key={i} className="space-y-2 px-4 py-3">
                <Skeleton className="h-4 w-64" />
                <Skeleton className="h-8 w-full" />
              </div>
            ))}
          </div>
        ) : list.length === 0 ? (
          <Empty icon={<WebhookIcon className="h-4 w-4" />}>
            <span>
              还没有仓库连接。每条连接都有独立的 webhook 地址与 secret，同一仓库可被多个小队同时接入。
            </span>
            <button
              type="button"
              onClick={() => setShowCreate(true)}
              className="mt-2 block text-accent transition-colors hover:text-accent-dim"
            >
              + 新建连接
            </button>
          </Empty>
        ) : (
          <ul className="divide-y divide-line">
            {list.map((conn) => (
              <ConnectionCard
                key={conn.id}
                conn={conn}
                domain={domainOf(conn.domain_id)}
                onEdit={() => setEditTarget(conn)}
                onDelete={() => setDeleteTarget(conn)}
              />
            ))}
          </ul>
        )}
      </SectionCard>

      {/* 删除失败必须显示——后端 400 静默等于「点了没反应」。 */}
      {remove.isError && <p className="mt-2 text-sm text-red-400">{String(remove.error)}</p>}

      {showCreate && (
        <CreateConnectionDialog
          squadId={squadId}
          domains={domains}
          connectedDomainIds={list.map((c) => c.domain_id)}
          onClose={() => setShowCreate(false)}
        />
      )}
      {editTarget && <EditEventsDialog conn={editTarget} onClose={() => setEditTarget(null)} />}
      {deleteTarget && (
        <ConfirmDialog
          title="删除仓库连接"
          message={`确定删除 ${deleteTarget.repo} 的这条连接吗？删除后该连接不再接收 webhook 投递；仓库本身和该仓库上的其它小队连接不受影响。`}
          onConfirm={() =>
            remove.mutate(deleteTarget.id, { onSuccess: () => setDeleteTarget(null) })
          }
          onClose={() => setDeleteTarget(null)}
          loading={remove.isPending}
        />
      )}
    </div>
  );
}

// ── 单条连接卡片 ──
function ConnectionCard({
  conn,
  domain,
  onEdit,
  onDelete,
}: {
  conn: Connection;
  domain?: Domain;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const origin = useOrigin();
  const { data: status, isLoading: statusLoading } = useConnectionWebhook(conn.id);
  const register = useRegisterConnectionWebhook(conn.id);
  const { copied, copy } = useCopy();
  const url = origin ? `${origin}${conn.webhook_path}` : conn.webhook_path;
  const state = webhookState(status);
  const isGithub = conn.provider === "github";
  const hookError = status && status.hooks.supported && !status.hooks.ok ? status.hooks.error : "";
  const lastDelivery = status?.deliveries[0];

  return (
    <li className="px-4 py-3">
      <div className="flex flex-wrap items-start gap-x-3 gap-y-2">
        <div className="min-w-0 flex-1 space-y-2">
          <div className="flex flex-wrap items-center gap-2">
            <span className="truncate font-mono text-sm text-ink" title={conn.repo}>
              {conn.repo || "（仓库未配置）"}
            </span>
            <span className="inline-flex shrink-0 items-center rounded border border-line bg-surface-2 px-1.5 py-0.5 font-mono text-[10px] text-ink-2">
              {providerLabel(conn.provider)}
            </span>
            {conn.events.map((ev) => (
              <EventChip key={ev} event={ev} />
            ))}
            <span className="font-mono text-[10px] text-ink-3" title={new Date(conn.created_at).toLocaleString()}>
              {relTime(conn.created_at)}创建
            </span>
          </div>

          <div className="flex flex-wrap items-center gap-x-2 gap-y-1 text-[11px] text-ink-3">
            <span>项目</span>
            <Link
              href="/domains"
              className="min-w-0 truncate text-ink-2 transition-colors hover:text-accent"
              title={domain?.name ?? conn.domain_id}
            >
              {domain?.name ?? conn.domain_id.slice(0, 8)}
            </Link>
            <span>·</span>
            <span className={conn.secret_set ? "text-green-400" : "text-amber-400"}>
              {conn.secret_set ? "专属 secret 已生成" : "secret 未设置"}
            </span>
            {state && (
              <>
                <span>·</span>
                <span className={state.cls}>{state.label}</span>
              </>
            )}
            {statusLoading && <span>· 检测中…</span>}
          </div>

          <div className="flex items-center gap-2">
            <code
              className="min-w-0 flex-1 truncate rounded border border-line bg-surface-2 px-2 py-1.5 font-mono text-xs text-ink-2"
              title={url}
            >
              {url}
            </code>
            <Button
              type="button"
              variant="outline"
              onClick={() => void copy("url", url)}
              className="h-7 shrink-0 gap-1 px-2 text-xs"
            >
              <Copy className="h-3.5 w-3.5" />
              {copied === "url" ? "已复制" : "复制"}
            </Button>
          </div>

          {hookError && <p className="text-[11px] text-amber-400">{describeHookError(hookError)}</p>}
          {status && !status.hooks.supported && (
            <p className="text-[11px] text-ink-3">
              GitCode 不支持自动注册：请把上面的地址与 secret 手动填到仓库的 Webhook 设置。
            </p>
          )}
          {lastDelivery && (
            <p className="font-mono text-[10px] text-ink-3">
              最近投递：
              <span className={lastDelivery.ok ? "text-green-400" : "text-red-400"}>
                {lastDelivery.ok ? "ok" : "fail"}
              </span>
              {" · "}
              {lastDelivery.event || "unknown"}
              {lastDelivery.action ? ` · ${lastDelivery.action}` : ""}
              {" · "}
              {relTime(lastDelivery.received_at)}
            </p>
          )}
          {register.isSuccess && (
            <p className="text-[11px] text-green-400">
              {register.data?.created ? "已注册到仓库" : "仓库上的 webhook 已更新"}
            </p>
          )}
          {register.isError && <p className="text-[11px] text-red-400">{String(register.error)}</p>}
        </div>

        <div className="flex shrink-0 flex-wrap items-center gap-1">
          {isGithub && (
            <Button
              type="button"
              variant="outline"
              onClick={() => register.mutate({ url, events: conn.events })}
              disabled={!conn.secret_set || register.isPending}
              className="h-7 gap-1 px-2 text-xs"
              title={conn.secret_set ? "在本仓库创建/更新指向该连接的 webhook" : "没有 secret，无法注册"}
            >
              <RefreshCw className={cn("h-3.5 w-3.5", register.isPending && "animate-spin")} />
              {register.isPending ? "注册中…" : "注册 Webhook"}
            </Button>
          )}
          <Button type="button" variant="outline" onClick={onEdit} className="h-7 gap-1 px-2 text-xs">
            <Pencil className="h-3.5 w-3.5" />
            编辑事件
          </Button>
          <button
            type="button"
            onClick={onDelete}
            className="inline-flex h-7 items-center gap-1 rounded-md px-2 text-xs text-ink-3 transition-colors hover:bg-surface-2 hover:text-red-400"
          >
            <Trash2 className="h-3.5 w-3.5" />
            删除
          </button>
        </div>
      </div>
    </li>
  );
}

// ── 新建连接弹窗 ──
// 创建成功后停在结果态：一次性 secret 只在这个响应里出现，关闭后无法再取。
function CreateConnectionDialog({
  squadId,
  domains,
  connectedDomainIds,
  onClose,
}: {
  squadId: string;
  domains?: Domain[];
  connectedDomainIds?: string[];
  onClose: () => void;
}) {
  const create = useCreateConnection();
  const origin = useOrigin();
  const register = useRegisterConnectionWebhook("");
  const { copied, copy } = useCopy();
  const [domainId, setDomainId] = useState("");
  const [events, setEvents] = useState<string[]>(["issues", "pull_request"]);
  const [created, setCreated] = useState<Connection | null>(null);
  const [autoReg, setAutoReg] = useState<"idle" | "pending" | "ok" | "error">("idle");
  const [autoRegError, setAutoRegError] = useState("");
  const autoStarted = useRef(false);

  // 一个小队对同一仓库只能有一条连接（否则同 issue 会被轮询/投递两次）：
  // 已经接入的仓库从下拉里去掉，避免用户创建后撞后端校验。
  const eligible = useMemo(() => {
    const taken = new Set(connectedDomainIds ?? []);
    return (domains ?? []).filter(
      (d) => d.type === "repo" && d.issue_repo !== "" && !taken.has(d.id),
    );
  }, [domains, connectedDomainIds]);
  const domain = (domains ?? []).find((d) => d.id === domainId);

  // 默认选中第一个可用仓库（列表数据是异步到的）。
  useEffect(() => {
    if (domainId || eligible.length === 0) return;
    setDomainId(eligible[0].id);
  }, [domainId, eligible]);

  // GitHub + 有 git 凭据时自动注册到宿主仓库；失败只提示，不回滚连接。
  // 注册只触发一次（autoStarted 守卫），效果依赖省略 mutation 对象。
  useEffect(() => {
    if (!created || !origin || autoStarted.current) return;
    const d = (domains ?? []).find((x) => x.id === created.domain_id);
    if (created.provider !== "github" || !d?.git_credentials) return;
    autoStarted.current = true;
    setAutoReg("pending");
    register.mutate(
      { id: created.id, url: `${origin}${created.webhook_path}`, events: created.events },
      {
        onSuccess: () => setAutoReg("ok"),
        onError: (err) => {
          setAutoReg("error");
          setAutoRegError(String(err));
        },
      },
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [created, origin, domains]);

  const toggleEvent = (ev: string) =>
    setEvents((prev) => (prev.includes(ev) ? prev.filter((x) => x !== ev) : [...prev, ev]));

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!domainId || events.length === 0 || create.isPending) return;
    create.mutate(
      { squadId, domain_id: domainId, events },
      { onSuccess: (conn) => setCreated(conn) },
    );
  };

  const createdUrl = created ? (origin ? `${origin}${created.webhook_path}` : created.webhook_path) : "";

  return (
    <Dialog
      title={created ? "连接已创建" : "新建仓库连接"}
      onClose={onClose}
      wide
      footer={
        created ? (
          <Button onClick={onClose}>完成</Button>
        ) : (
          <>
            <Button variant="outline" onClick={onClose}>
              取消
            </Button>
            <Button
              type="submit"
              form="connection-create-form"
              disabled={create.isPending || !domainId || events.length === 0}
            >
              {create.isPending ? "创建中…" : "创建连接"}
            </Button>
          </>
        )
      }
    >
      {created ? (
        <div className="space-y-4">
          <div className="flex items-center gap-2 text-sm text-green-400">
            <Check className="h-4 w-4" />
            连接已创建：{created.repo}
          </div>
          <p className="text-sm text-ink-2">
            webhook secret 只在这里显示一次，请立即复制保存；关闭后无法再次查看。
          </p>

          <div className="space-y-2">
            <SectionLabel>Webhook 地址</SectionLabel>
            <div className="flex items-center gap-2">
              <code
                className="min-w-0 flex-1 truncate rounded border border-line bg-surface-2 px-2 py-1.5 font-mono text-xs text-ink-2"
                title={createdUrl}
              >
                {createdUrl}
              </code>
              <Button
                type="button"
                variant="outline"
                onClick={() => void copy("url", createdUrl)}
                className="h-7 shrink-0 gap-1 px-2 text-xs"
              >
                <Copy className="h-3.5 w-3.5" />
                {copied === "url" ? "已复制" : "复制"}
              </Button>
            </div>
          </div>

          <div className="space-y-2">
            <SectionLabel>Secret（一次性）</SectionLabel>
            <div className="flex items-center gap-2">
              <code
                className="min-w-0 flex-1 truncate rounded border border-amber-500/30 bg-amber-500/5 px-2 py-1.5 font-mono text-xs text-amber-300"
                title={created.secret ?? ""}
              >
                {created.secret ?? "（响应未返回 secret）"}
              </code>
              {created.secret && (
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => void copy("secret", created.secret ?? "")}
                  className="h-7 shrink-0 gap-1 px-2 text-xs"
                >
                  <Copy className="h-3.5 w-3.5" />
                  {copied === "secret" ? "已复制" : "复制"}
                </Button>
              )}
            </div>
          </div>

          <div className="flex flex-wrap items-center gap-1.5 text-xs text-ink-3">
            <SectionLabel>事件</SectionLabel>
            {created.events.map((ev) => (
              <EventChip key={ev} event={ev} />
            ))}
            <span>宿主仓库的 webhook 需要订阅这些事件</span>
          </div>

          {created.provider === "github" ? (
            autoReg === "pending" ? (
              <p className="text-xs text-ink-3">正在自动注册到仓库…</p>
            ) : autoReg === "ok" ? (
              <p className="text-xs text-green-400">已自动注册到仓库，secret 已同步。</p>
            ) : autoReg === "error" ? (
              <p className="text-xs text-amber-400">
                自动注册失败：{autoRegError}。连接已创建，可稍后在列表里手动点「注册 Webhook」。
              </p>
            ) : (
              <p className="text-xs text-ink-3">
                该项目的 git_credentials 为空，无法自动注册：请手动把地址与 secret 填到仓库的 Webhook 设置。
              </p>
            )
          ) : (
            <p className="text-xs text-ink-3">
              GitCode 不支持自动注册：请把上面的地址与 secret 手动填到仓库的 Webhook 设置。
            </p>
          )}
        </div>
      ) : (
        <form id="connection-create-form" onSubmit={submit} className="space-y-4">
          <Field label="仓库" hint="只列出 repo 类型且已配置跟踪仓库（issue_repo）的项目">
            {eligible.length === 0 ? (
              <div className="rounded-md border border-line bg-surface-2 px-3 py-2 text-xs leading-relaxed text-ink-3">
                没有可接入的仓库：要么还没有 repo 类型且填了「跟踪仓库」的项目，要么本小队已经接入了全部可用仓库。先到{" "}
                <Link href="/domains" className="text-accent hover:text-accent-dim">
                  Domains 页
                </Link>{" "}
                创建 repo 类型的项目并填写「跟踪仓库」。
              </div>
            ) : (
              <select
                value={domainId}
                onChange={(e) => setDomainId(e.target.value)}
                className={inputCls}
                required
              >
                <option value="">选择仓库…</option>
                {eligible.map((d) => (
                  <option key={d.id} value={d.id}>
                    {d.name} — {d.issue_repo}（{providerLabel(d.issue_provider || "github")}）
                  </option>
                ))}
              </select>
            )}
          </Field>

          <Field label="监听事件" hint="Issue 与 PR 可分开监听；至少勾选一个">
            <div className="divide-y divide-line rounded-lg border border-line">
              {EVENT_OPTIONS.map((o) => (
                <label
                  key={o.value}
                  className="flex cursor-pointer items-center gap-2 px-3 py-2 text-sm hover:bg-surface-0"
                >
                  <input
                    type="checkbox"
                    checked={events.includes(o.value)}
                    onChange={() => toggleEvent(o.value)}
                  />
                  <span className="text-ink-2">{o.label}</span>
                  <span className="text-[11px] text-ink-3">{o.hint}</span>
                </label>
              ))}
            </div>
          </Field>

          {domain && (
            <p className="text-xs leading-relaxed text-ink-3">
              {domain.name} · <span className="font-mono">{domain.issue_repo}</span> ·{" "}
              {providerLabel(domain.issue_provider || "github")}
              {domain.git_credentials
                ? " · 已配置 git 凭据，GitHub 连接会自动注册"
                : " · 未配置 git 凭据（GitHub 连接需手动注册）"}
            </p>
          )}
          {events.length === 0 && <p className="text-xs text-amber-400">至少勾选一个事件。</p>}
          {create.isError && <p className="text-sm text-red-400">{String(create.error)}</p>}
        </form>
      )}
    </Dialog>
  );
}

// ── 编辑事件弹窗 ──
function EditEventsDialog({ conn, onClose }: { conn: Connection; onClose: () => void }) {
  const update = useUpdateConnection();
  const [events, setEvents] = useState<string[]>(conn.events);

  const toggleEvent = (ev: string) =>
    setEvents((prev) => (prev.includes(ev) ? prev.filter((x) => x !== ev) : [...prev, ev]));

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (events.length === 0 || update.isPending) return;
    update.mutate({ id: conn.id, events }, { onSuccess: onClose });
  };

  return (
    <Dialog
      title={`编辑监听事件：${conn.repo}`}
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>
            取消
          </Button>
          <Button
            type="submit"
            form="connection-events-form"
            disabled={update.isPending || events.length === 0}
          >
            {update.isPending ? "保存中…" : "保存"}
          </Button>
        </>
      }
    >
      <form id="connection-events-form" onSubmit={submit} className="space-y-4">
        <div className="rounded-lg border border-line bg-surface-2 px-3 py-2 font-mono text-xs text-ink-2">
          {conn.webhook_path}
        </div>
        <Field
          label="监听事件"
          hint="宿主 hook 缺事件时，注册状态会显示「缺事件」，可点「注册 Webhook」补齐"
        >
          <div className="divide-y divide-line rounded-lg border border-line">
            {EVENT_OPTIONS.map((o) => (
              <label
                key={o.value}
                className="flex cursor-pointer items-center gap-2 px-3 py-2 text-sm hover:bg-surface-0"
              >
                <input
                  type="checkbox"
                  checked={events.includes(o.value)}
                  onChange={() => toggleEvent(o.value)}
                />
                <span className="text-ink-2">{o.label}</span>
                <span className="text-[11px] text-ink-3">{o.hint}</span>
              </label>
            ))}
          </div>
        </Field>
        {events.length === 0 && <p className="text-xs text-amber-400">至少勾选一个事件。</p>}
        {update.isError && <p className="text-sm text-red-400">{String(update.error)}</p>}
      </form>
    </Dialog>
  );
}