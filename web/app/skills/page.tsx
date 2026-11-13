"use client";

import { useState } from "react";
import { Sparkles } from "lucide-react";
import {
  useSkills,
  useCreateSkill,
  useDeleteSkill,
  useConfigureSkillSource,
  useClearSkillSource,
  useSkillSyncCompare,
  useRunSkillSync,
  useCreateSkillPullRequest,
} from "@/lib/queries";
import {
  Button,
  Dialog,
  Empty,
  Field,
  inputCls,
  PageBody,
  PageHeader,
  SectionCard,
  Skeleton,
  StatStrip,
  Toolbar,
} from "@/components/ui";
import { relTime } from "@/lib/utils";
import type {
  Skill,
  SkillSource,
  SkillPullRequestResult,
  SyncEntry,
} from "@/lib/types";

const ROW_GRID =
  "grid grid-cols-1 gap-1.5 px-4 py-3 transition-colors hover:bg-surface-2/40 md:grid-cols-[minmax(0,1fr)_184px_72px_112px] md:items-center md:gap-3";

const HEAD_GRID =
  "hidden md:grid md:grid-cols-[minmax(0,1fr)_184px_72px_112px] md:gap-3 border-b border-line px-4 py-2 font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3";

const SYNC_STATE_META: Record<string, { label: string; cls: string }> = {
  in_sync: { label: "已同步", cls: "border-emerald-500/30 bg-emerald-500/10 text-emerald-300" },
  local_ahead: { label: "本地有新改动", cls: "border-sky-500/30 bg-sky-500/10 text-sky-300" },
  remote_ahead: { label: "远端有更新", cls: "border-amber-500/30 bg-amber-500/10 text-amber-300" },
  diverged: { label: "双向分叉", cls: "border-red-500/30 bg-red-500/10 text-red-300" },
};

const SYNC_ACTION_LABEL: Record<string, string> = {
  pulled: "拉取",
  pushed: "推送",
  pr: "提交 PR",
};

// 后端 diff 以「拉取」为视角：added=远端有/本地无（拉取会新增），
// modified=两边都有但内容不同，removed=本地有/远端无（拉取会删除）。
const SYNC_GROUPS = [
  { key: "added", label: "远端新增", sign: "+", cls: "text-emerald-300" },
  { key: "modified", label: "内容修改", sign: "~", cls: "text-amber-300" },
  { key: "removed", label: "本地独有", sign: "−", cls: "text-red-300" },
] as const;

function fullTime(iso: string): string {
  return iso ? new Date(iso).toLocaleString() : "—";
}

function repoShort(url: string): string {
  return url.replace(/^https?:\/\//, "").replace(/\.git$/, "");
}

function stateMeta(state?: string) {
  return (
    SYNC_STATE_META[state ?? ""] ?? {
      label: "未比较",
      cls: "border-line-strong bg-surface-2 text-ink-3",
    }
  );
}

// 平台 Skill 模型只有 id/name/description/created_at：没有分类、大小、
// updated_at 字段，所以列表不编这些列，只显示真实存在的「上传时间」。
// Feature A：source 存在时显示远端仓库摘要与最近同步状态。
function SkillRow({
  sk,
  onDelete,
  onSync,
  deleting,
  error,
}: {
  sk: Skill;
  onDelete: (id: string) => void;
  onSync: (sk: Skill) => void;
  deleting: boolean;
  error?: string;
}) {
  const src = sk.source;
  return (
    <li className={ROW_GRID}>
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <span className="truncate text-sm font-medium text-ink">{sk.name}</span>
          <span className="shrink-0 font-mono text-[11px] text-ink-3">{sk.id.slice(0, 8)}</span>
        </div>
        <p className="mt-0.5 text-xs leading-relaxed text-ink-3">
          {sk.description || "（无描述）"}
        </p>
        {error && <p className="mt-1 text-xs text-red-400">{error}</p>}
      </div>

      <div className="min-w-0">
        <div className="flex items-center justify-between gap-2 md:block">
          <span className="text-[11px] text-ink-3 md:hidden">来源</span>
          {src ? (
            <span
              className="truncate font-mono text-[11px] text-ink-2"
              title={src.repo_url}
            >
              {repoShort(src.repo_url)}
            </span>
          ) : (
            <span className="text-[11px] text-ink-3" title="未绑定远端仓库，仅存在于平台">
              本地
            </span>
          )}
        </div>
        {src ? (
          <p className="mt-0.5 truncate font-mono text-[10px] text-ink-3">
            {src.branch || "main"} · {src.path ? `/${src.path}` : "/"}
          </p>
        ) : (
          <p className="mt-0.5 text-[10px] text-ink-3">未绑定远端仓库</p>
        )}
        {src &&
          (src.last_synced_at ? (
            <p className="mt-0.5 truncate text-[10px] text-ink-3" title={fullTime(src.last_synced_at)}>
              最近同步：{relTime(src.last_synced_at)}
              {src.last_sync_action ? ` · ${SYNC_ACTION_LABEL[src.last_sync_action] ?? src.last_sync_action}` : ""}
            </p>
          ) : (
            <p className="mt-0.5 text-[10px] text-ink-3">尚未同步过</p>
          ))}
      </div>

      <div className="flex items-center justify-between gap-2 text-xs md:block">
        <span className="text-[11px] text-ink-3 md:hidden">上传</span>
        <span className="text-ink-2" title={fullTime(sk.created_at)}>
          {relTime(sk.created_at)}
        </span>
      </div>

      <div className="flex items-center justify-end gap-3 text-xs">
        <button
          type="button"
          onClick={() => onSync(sk)}
          className="text-xs text-ink-2 transition-colors hover:text-accent"
        >
          同步
        </button>
        <button
          type="button"
          disabled={deleting}
          onClick={() => {
            if (confirm(`删除 skill「${sk.name}」？`)) onDelete(sk.id);
          }}
          className="text-xs text-ink-3 transition-colors hover:text-red-400 disabled:cursor-not-allowed disabled:opacity-50"
        >
          {deleting ? "删除中…" : "删除"}
        </button>
      </div>
    </li>
  );
}

export default function SkillsPage() {
  const { data: skills, isLoading } = useSkills();
  const del = useDeleteSkill();
  const [showCreate, setShowCreate] = useState(false);
  const [keyword, setKeyword] = useState("");
  const [syncSkill, setSyncSkill] = useState<Skill | null>(null);

  const described = (skills ?? []).filter((s) => s.description?.trim()).length;
  const latest = (skills ?? []).reduce<Skill | undefined>(
    (acc, s) => (!acc || new Date(s.created_at) > new Date(acc.created_at) ? s : acc),
    undefined
  );

  const visible = (skills ?? []).filter((s) => {
    const q = keyword.trim().toLowerCase();
    return !q || s.name.toLowerCase().includes(q) || s.description?.toLowerCase().includes(q);
  });

  return (
    <PageBody>
      <PageHeader
        title="技能"
        desc="平台技能库：上传 skill 包（SKILL.md），勾选给 agent 后随 run 下发到项目级 skills 目录。可绑定远端仓库（GitHub / GitCode）做双向同步或发起 PR。"
        action={<Button onClick={() => setShowCreate(true)}>上传 Skill</Button>}
      />

      <StatStrip
        items={[
          {
            label: "技能",
            value: isLoading ? "—" : (skills?.length ?? 0),
            hint: "平台托管的 skill 包",
          },
          {
            label: "有描述",
            value: isLoading ? "—" : described,
            tone: described === (skills?.length ?? 0) && described > 0 ? "ok" : "default",
            hint: `${(skills?.length ?? 0) - described} 个待补充描述`,
          },
          {
            label: "最近上传",
            value: latest ? relTime(latest.created_at) : "—",
            hint: latest ? latest.name : "还没有技能包",
          },
        ]}
      />

      <SectionCard
        title="技能库"
        count={isLoading ? undefined : visible.length}
        desc={keyword.trim() ? "按关键词过滤" : undefined}
        action={
          (skills?.length ?? 0) > 0 ? (
            <Toolbar>
              <input
                value={keyword}
                onChange={(e) => setKeyword(e.target.value)}
                placeholder="搜索名称或描述…"
                className="w-52 rounded-md border border-line bg-surface-2 px-2.5 py-1.5 text-xs text-ink outline-none transition-colors placeholder:text-ink-3 focus:border-accent-dim"
              />
            </Toolbar>
          ) : undefined
        }
        bodyClassName="p-0"
      >
        {isLoading ? (
          <div className="divide-y divide-line">
            {[0, 1, 2].map((i) => (
              <div key={i} className="space-y-2 px-4 py-4">
                <Skeleton className="h-4 w-1/4" />
                <Skeleton className="h-3 w-2/3" />
              </div>
            ))}
          </div>
        ) : !skills?.length ? (
          <Empty icon={<Sparkles className="h-5 w-5" />}>
            技能库为空——上传一个 skill 包开始；平台会从包内 SKILL.md 的 frontmatter 解析名称与描述。
          </Empty>
        ) : !visible.length ? (
          <Empty>没有匹配当前关键词的技能。</Empty>
        ) : (
          <>
            <div className={HEAD_GRID}>
              <span>技能</span>
              <span>来源 / 同步</span>
              <span>上传</span>
              <span className="text-right">操作</span>
            </div>
            <ul className="divide-y divide-line">
              {visible.map((sk) => (
                <SkillRow
                  key={sk.id}
                  sk={sk}
                  deleting={del.isPending && del.variables === sk.id}
                  error={
                    del.isError && del.variables === sk.id ? String(del.error) : undefined
                  }
                  onDelete={(id) => del.mutate(id)}
                  onSync={setSyncSkill}
                />
              ))}
            </ul>
          </>
        )}
      </SectionCard>

      {showCreate && <CreateSkillDialog onClose={() => setShowCreate(false)} />}
      {syncSkill && (
        <SkillSyncDialog skill={syncSkill} onClose={() => setSyncSkill(null)} />
      )}
    </PageBody>
  );
}

// CreateSkillDialog uploads a skill package as a ZIP archive. The skill's
// name and description are PARSED by the platform from the archive's
// SKILL.md frontmatter — the user only picks the zip. A zip without a valid
// SKILL.md (or without a name in its frontmatter) is rejected as "not a
// skill package".
function CreateSkillDialog({ onClose }: { onClose: () => void }) {
  const create = useCreateSkill();
  const [file, setFile] = useState<File | null>(null);

  const submit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!file) return;
    create.mutate({ file }, { onSuccess: onClose });
  };

  return (
    <Dialog
      title="上传 Skill"
      onClose={onClose}
      wide
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button type="submit" form="skill-form" disabled={create.isPending || !file}>
            {create.isPending ? "上传中…" : "上传"}
          </Button>
        </>
      }
    >
      <form id="skill-form" onSubmit={submit} className="space-y-4">
        <Field
          label="Skill 包（.zip）"
          hint="包内必须有 SKILL.md，且其 frontmatter 含 name（可选 description）。平台解析 SKILL.md 自动回填名称与描述，原样下发到机器解压。"
        >
          <input
            type="file"
            accept=".zip"
            onChange={(e) => setFile(e.target.files?.[0] ?? null)}
            className={`${inputCls} file:mr-3 file:rounded-lg file:border-0 file:bg-accent/10 file:px-3 file:py-1 file:text-sm file:text-accent`}
            required
          />
          {file && (
            <p className="text-xs text-ink-3">
              已选择：{file.name}（{(file.size / 1024).toFixed(0)} KB）
            </p>
          )}
        </Field>
        <p className="text-xs text-ink-3">
          SKILL.md 示例：<br />
          <code className="font-mono">{"---"}</code><br />
          <code className="font-mono">name: code-review-checklist</code><br />
          <code className="font-mono">description: 一句话说明这个 skill 干什么</code><br />
          <code className="font-mono">{"---"}</code><br />
          （以下是 skill 指令正文）
        </p>
        {create.isError && <p className="text-sm text-red-400">{String(create.error)}</p>}
      </form>
    </Dialog>
  );
}

// SkillSyncDialog 是 Feature A 的唯一入口：
// - 未绑定 source → 来源配置表单（repo_url/provider/branch/path/token）。
// - 已绑定 → 只读 compare 结果（state / 身份 / 权限 / 文件差异）+ 拉取/推送/PR。
// 所有写操作成功后，react-query 同时刷新技能列表与该技能的 compare。
function SkillSyncDialog({ skill, onClose }: { skill: Skill; onClose: () => void }) {
  const configure = useConfigureSkillSource();
  const clear = useClearSkillSource();
  const runSync = useRunSkillSync();
  const createPr = useCreateSkillPullRequest();

  const [source, setSource] = useState<SkillSource | null>(skill.source ?? null);
  const [editing, setEditing] = useState(!skill.source);
  const [pr, setPr] = useState<SkillPullRequestResult | null>(null);
  const [repoUrl, setRepoUrl] = useState(skill.source?.repo_url ?? "");
  const [provider, setProvider] = useState(skill.source?.provider ?? "");
  const [branch, setBranch] = useState(skill.source?.branch || "main");
  const [path, setPath] = useState(skill.source?.path ?? "");
  const [token, setToken] = useState("");

  // compare 只在「已绑定且不在编辑」时拉取；PR 成功后列表会刷新。
  const compare = useSkillSyncCompare(skill.id, !editing && !!source);

  const submitSource = (e: React.FormEvent) => {
    e.preventDefault();
    configure.mutate(
      {
        id: skill.id,
        body: {
          repo_url: repoUrl.trim(),
          provider: provider || undefined,
          branch: branch.trim() || undefined,
          path: path.trim(),
          token: token.trim() || undefined,
        },
      },
      {
        onSuccess: (updated) => {
          setSource(updated.source ?? null);
          setEditing(false);
          setPr(null);
          setToken("");
        },
      }
    );
  };

  const unlink = () => {
    if (!confirm(`解除「${skill.name}」的远端仓库绑定？已保存的 token 也会一并清除。`)) return;
    clear.mutate(skill.id, {
      onSuccess: () => {
        setSource(null);
        setEditing(true);
        setPr(null);
        setRepoUrl("");
        setProvider("");
        setBranch("main");
        setPath("");
        setToken("");
      },
    });
  };

  const data = compare.data;
  const canPush = Boolean(data?.can_push);
  const entries: SyncEntry[] = data?.entries ?? [];
  const counts = data?.counts;
  const state = stateMeta(data?.state);

  const configuredFooter = (
    <>
      <Button variant="outline" onClick={onClose}>关闭</Button>
      <Button variant="ghost" onClick={() => compare.refetch()} disabled={compare.isFetching}>
        {compare.isFetching ? "比较中…" : "重新比较"}
      </Button>
      <Button variant="ghost" onClick={() => setEditing(true)}>修改来源</Button>
    </>
  );

  const editingFooter = (
    <>
      <Button variant="outline" onClick={onClose}>取消</Button>
      {source && (
        <Button variant="ghost" onClick={() => setEditing(false)}>返回同步</Button>
      )}
      <Button
        type="submit"
        form="skill-source-form"
        disabled={configure.isPending || !repoUrl.trim()}
      >
        {configure.isPending ? "保存中…" : "保存来源"}
      </Button>
    </>
  );

  const mutationError =
    runSync.error ?? createPr.error ?? configure.error ?? clear.error;

  return (
    <Dialog
      title={`同步 · ${skill.name}`}
      onClose={onClose}
      wide
      footer={editing ? editingFooter : configuredFooter}
    >
      <div className="space-y-4">
        {editing ? (
          <form id="skill-source-form" onSubmit={submitSource} className="space-y-4">
            <p className="text-xs text-ink-3">
              绑定一个包含该 skill 的远端仓库目录。平台以仓库文件为准做比较：
              本地改动可推送/发起 PR，远端改动可拉取覆盖本地（会重写本地文件与 package.zip）。
            </p>
            <Field label="仓库地址" hint="http(s) 地址，例如 https://github.com/owner/repo">
              <input
                value={repoUrl}
                onChange={(e) => setRepoUrl(e.target.value)}
                className={inputCls}
                required
                placeholder="https://github.com/owner/repo"
              />
            </Field>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <Field label="平台" hint="留空按域名自动识别">
                <select
                  value={provider}
                  onChange={(e) => setProvider(e.target.value)}
                  className={inputCls}
                >
                  <option value="">自动识别</option>
                  <option value="github">GitHub</option>
                  <option value="gitcode">GitCode</option>
                </select>
              </Field>
              <Field label="分支" hint="默认 main">
                <input
                  value={branch}
                  onChange={(e) => setBranch(e.target.value)}
                  className={inputCls}
                  placeholder="main"
                />
              </Field>
            </div>
            <Field
              label="仓库内路径"
              hint="skill 目录在仓库中的相对路径；留空表示仓库根目录"
            >
              <input
                value={path}
                onChange={(e) => setPath(e.target.value)}
                className={inputCls}
                placeholder="skills/code-review"
              />
            </Field>
            <Field
              label="访问 Token"
              hint={
                source?.has_token
                  ? "已保存 token：留空保留，填写则替换。推送 / PR 需要写权限。"
                  : "同步（拉取/推送）与发起 PR 都需要 token 具备仓库写权限；未配置 token 时同步会返回 403。"
              }
            >
              <input
                type="password"
                value={token}
                onChange={(e) => setToken(e.target.value)}
                className={inputCls}
                placeholder={source?.has_token ? "留空保留已保存 token" : "ghp_…（可选）"}
                autoComplete="new-password"
              />
            </Field>
            {configure.isError && (
              <p className="text-sm text-red-400">{String(configure.error)}</p>
            )}
          </form>
        ) : (
          <>
            {source && (
              <div className="space-y-2 rounded-md border border-line bg-surface-2/50 px-3 py-2">
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px]">
                  <span className="font-mono text-ink-2" title={source.repo_url}>
                    {repoShort(source.repo_url)}
                  </span>
                  <span className="text-ink-3">分支 {source.branch || "main"}</span>
                  <span className="text-ink-3">路径 {source.path ? `/${source.path}` : "/"}</span>
                  <span className="text-ink-3">{source.provider || "自动识别"}</span>
                  {source.has_token ? (
                    <span className="text-emerald-300">token 已保存</span>
                  ) : (
                    <span className="text-amber-300">未配置 token</span>
                  )}
                </div>
                <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[11px] text-ink-3">
                  {source.last_synced_at ? (
                    <span title={fullTime(source.last_synced_at)}>
                      最近同步 {relTime(source.last_synced_at)}
                      {source.last_sync_action
                        ? ` · ${SYNC_ACTION_LABEL[source.last_sync_action] ?? source.last_sync_action}`
                        : ""}
                    </span>
                  ) : (
                    <span>尚未同步过</span>
                  )}
                  {source.last_remote_sha && (
                    <span className="font-mono">远端 {source.last_remote_sha.slice(0, 8)}</span>
                  )}
                  <button
                    type="button"
                    onClick={unlink}
                    disabled={clear.isPending}
                    className="ml-auto text-ink-3 transition-colors hover:text-red-400 disabled:opacity-50"
                  >
                    {clear.isPending ? "解除中…" : "解除绑定"}
                  </button>
                </div>
              </div>
            )}

            {pr && (
              <div className="space-y-1 rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs text-emerald-200">
                <div>
                  已创建 PR
                  <span className="ml-1 font-mono font-medium">#{pr.pr_number}</span>
                  <a
                    href={pr.pr_url}
                    target="_blank"
                    rel="noreferrer"
                    className="ml-2 break-all underline decoration-emerald-400/50 underline-offset-2 hover:decoration-emerald-300"
                  >
                    {pr.pr_url}
                  </a>
                </div>
                <div className="text-[11px] text-emerald-300/80">
                  {pr.branch} → {pr.base} · {pr.identity}
                  {pr.commit ? ` · commit ${pr.commit.slice(0, 8)}` : ""}
                </div>
              </div>
            )}

            <div className="flex items-center gap-2">
              {compare.isLoading ? (
                <span className="text-xs text-ink-3">比较中…</span>
              ) : (
                <span
                  className={`inline-flex items-center rounded-md border px-2 py-0.5 text-[11px] font-medium ${state.cls}`}
                >
                  {state.label}
                </span>
              )}
              <span
                className={`inline-flex items-center rounded-md border px-2 py-0.5 text-[11px] font-medium ${
                  canPush
                    ? "border-emerald-500/30 bg-emerald-500/10 text-emerald-300"
                    : "border-amber-500/30 bg-amber-500/10 text-amber-300"
                }`}
              >
                {canPush ? "有推送/PR 权限" : "无推送权限"}
              </span>
              {data?.identity && (
                <span className="truncate text-[11px] text-ink-3">身份 {data.identity}</span>
              )}
              {data?.checked_at && (
                <span className="ml-auto text-[11px] text-ink-3" title={fullTime(data.checked_at)}>
                  {relTime(data.checked_at)}比较
                </span>
              )}
            </div>

            {counts && (
              <div className="flex flex-wrap gap-x-4 gap-y-1 text-[11px]">
                <span className="text-emerald-300">远端新增 {counts.added}</span>
                <span className="text-amber-300">修改 {counts.modified}</span>
                <span className="text-red-300">本地独有 {counts.removed}</span>
                <span className="text-ink-3">未变化 {counts.unchanged}</span>
                {data?.base_branch && <span className="text-ink-3">基线 {data.base_branch}</span>}
                {data?.remote_sha && (
                  <span className="font-mono text-ink-3">远端 {data.remote_sha.slice(0, 8)}</span>
                )}
              </div>
            )}

            {!compare.isError && entries.length > 0 && (
              <p className="text-[11px] leading-relaxed text-ink-3">
                新增=远端有本地无；本地独有=本地有远端无（拉取会删除）。
              </p>
            )}

            {compare.isError ? (
              <p className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-300">
                比较失败：{String(compare.error)}
              </p>
            ) : entries.length ? (
              <div className="max-h-56 space-y-2 overflow-y-auto rounded-md border border-line bg-surface-1/60 px-3 py-2">
                {SYNC_GROUPS.map((g) => {
                  const list = entries.filter((e) => e.status === g.key);
                  if (!list.length) return null;
                  return (
                    <div key={g.key}>
                      <div className={`mb-1 text-[11px] font-medium ${g.cls}`}>
                        {g.label}（{list.length}）
                      </div>
                      <ul className="space-y-0.5">
                        {list.map((e) => (
                          <li
                            key={`${g.key}:${e.path}`}
                            className="flex items-center gap-2 font-mono text-[11px] text-ink-2"
                          >
                            <span className={`w-3 shrink-0 ${g.cls}`}>{g.sign}</span>
                            <span className="truncate" title={e.path}>
                              {e.path}
                            </span>
                          </li>
                        ))}
                      </ul>
                    </div>
                  );
                })}
              </div>
            ) : compare.isLoading ? (
              <Skeleton className="h-16 w-full" />
            ) : data ? (
              <p className="rounded-md border border-line bg-surface-1/60 px-3 py-2 text-xs text-ink-3">
                本地与远端一致，没有文件差异。
              </p>
            ) : null}

            <div className="flex flex-wrap items-center gap-2">
              <Button
                variant="outline"
                onClick={() => {
                  if (
                    entries.length &&
                    !confirm("拉取远端会用远端文件覆盖本地 skill 目录（含 package.zip），确定继续？")
                  ) {
                    return;
                  }
                  runSync.mutate({ id: skill.id, direction: "pull" });
                }}
                disabled={runSync.isPending}
              >
                {runSync.isPending && runSync.variables?.direction === "pull"
                  ? "拉取中…"
                  : "拉取远端"}
              </Button>
              <Button
                variant="outline"
                onClick={() => runSync.mutate({ id: skill.id, direction: "push" })}
                disabled={!canPush || runSync.isPending}
                title={canPush ? "把本地改动直接提交到配置的分支" : "当前 token 没有推送权限"}
              >
                {runSync.isPending && runSync.variables?.direction === "push"
                  ? "推送中…"
                  : "推送到远端"}
              </Button>
              <Button
                variant="outline"
                onClick={() => createPr.mutate({ id: skill.id })}
                disabled={!canPush || createPr.isPending}
                title={canPush ? "把本地改动推到新分支并创建 PR" : "当前 token 没有推送/PR 权限"}
              >
                {createPr.isPending ? "创建 PR 中…" : "提交 PR"}
              </Button>
              {!canPush && (
                <span className="text-[11px] text-ink-3">
                  当前 token 无仓库写权限：同步（含拉取）与 PR 都会被拒绝（403），请在「修改来源」里替换为有写权限的 token。
                </span>
              )}
            </div>

            {runSync.isSuccess && runSync.data && (
              <p className="rounded-md border border-emerald-500/30 bg-emerald-500/10 px-3 py-2 text-xs text-emerald-300">
                {runSync.data.action === "pull"
                  ? "已拉取远端，本地文件与 package.zip 已更新。"
                  : "已推送本地改动到远端分支"}
                {runSync.data.action !== "pull" && runSync.data.commit
                  ? `（commit ${runSync.data.commit.slice(0, 8)}）`
                  : ""}
              </p>
            )}

            {mutationError && (
              <p className="rounded-md border border-red-500/30 bg-red-500/10 px-3 py-2 text-xs text-red-300">
                {String(mutationError)}
                {(String(mutationError).includes("forbidden") ||
                  String(mutationError).includes("403") ||
                  String(mutationError).includes("permission") ||
                  String(mutationError).includes("权限")) && (
                  <span className="mt-1 block text-red-300/80">
                    推送 / PR 需要 token 具备仓库写权限；可先在「修改来源」里替换 token。
                  </span>
                )}
                {(String(mutationError).includes("no local changes") ||
                  String(mutationError).includes("没有本地改动") ||
                  String(mutationError).includes("no changes")) && (
                  <span className="mt-1 block text-red-300/80">
                    本地与远端没有差异，无需推送或提交 PR。
                  </span>
                )}
              </p>
            )}
          </>
        )}
      </div>
    </Dialog>
  );
}