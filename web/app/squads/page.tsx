"use client";

import { useState } from "react";
import Link from "next/link";
import { useSquads, useAgents, useCreateSquad, useAddSquadMember, useDeleteSquad, useImportTeam, useGoalEvents, useDomains } from "@/lib/queries";
import { Button, PageHeader, Empty, Dialog, Field, inputCls, ConfirmDialog } from "@/components/ui";
import type { Domain, Squad } from "@/lib/types";

export default function SquadsPage() {
  useGoalEvents();
  const { data: squads, isLoading } = useSquads();
  const { data: agents } = useAgents();
  const { data: domains } = useDomains();
  const createSquad = useCreateSquad();
  const deleteSquad = useDeleteSquad();
  const [showForm, setShowForm] = useState(false);
  const [showImport, setShowImport] = useState(false);
  const [deleteTarget, setDeleteTarget] = useState<string | null>(null);

  const agentName = (aid: string) => agents?.find((a) => a.id === aid)?.name ?? aid;

  return (
    <div className="p-8">
      <PageHeader
        title="Squad"
        action={
          <div className="flex gap-2">
            <Button variant="outline" onClick={() => setShowImport(true)}>导入团队</Button>
            <Button onClick={() => setShowForm(true)}>+ 新建</Button>
          </div>
        }
      />

      {isLoading ? (
        <div className="text-sm text-ink-3 py-16 text-center">加载中…</div>
      ) : !squads || squads.length === 0 ? (
        <Empty>暂无 Squad。点「+ 新建」创建一个，或「导入团队」从代码仓导入。</Empty>
      ) : (
        <div className="bg-surface-1 rounded-xl border border-line overflow-hidden">
          <table className="w-full text-sm">
            <thead>
              <tr className="border-b border-line bg-surface-0/50 text-left text-xs font-medium text-ink-3 uppercase tracking-wide">
                <th className="px-4 py-3">名称</th>
                <th className="px-4 py-3">Leader</th>
                <th className="px-4 py-3">描述</th>
                <th className="px-4 py-3">创建时间</th>
                <th className="px-4 py-3 w-20"></th>
              </tr>
            </thead>
            <tbody>
              {squads.map((s: Squad) => (
                <tr key={s.id} className="border-b border-line last:border-0 hover:bg-surface-0/60">
                  <td className="px-4 py-3 font-medium text-ink">
                    <Link href={`/squads/${s.id}`} className="hover:text-blue-400 hover:underline">
                      {s.name}
                    </Link>
                  </td>
                  <td className="px-4 py-3 text-ink-2">{agentName(s.leader_id)}</td>
                  <td className="px-4 py-3 text-ink-3 max-w-[220px]">
                    <div className="truncate">{s.description || "-"}</div>
                    <SquadRepos squadId={s.id} domains={domains} />
                  </td>
                  <td className="px-4 py-3 text-ink-3">{new Date(s.created_at).toLocaleString()}</td>
                  <td className="px-4 py-3 text-right">
                    <button
                      onClick={() => setDeleteTarget(s.id)}
                      className="text-xs text-ink-3 hover:text-red-400 transition-colors"
                    >
                      删除
                    </button>
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      )}

      {showForm && <NewSquadForm agents={agents} onClose={() => setShowForm(false)} />}
      {showImport && (
        <ImportTeamForm
          onClose={() => setShowImport(false)}
          onSubmitted={() => setShowImport(false)}
        />
      )}
      {deleteTarget && (
        <ConfirmDialog
          title="确认删除"
          message="确定要删除此 Squad 吗？被 goal/schedule 引用时会被拒绝。"
          onConfirm={() => deleteSquad.mutate(deleteTarget, { onSuccess: () => setDeleteTarget(null) })}
          onClose={() => setDeleteTarget(null)}
          loading={deleteSquad.isPending}
        />
      )}

      {/* 删除失败必须显示——后端守卫（squad 被 goal/schedule 引用）返回
          400 coded error，静默等于「点了没反应」。 */}
      {deleteSquad.isError && (
        <p className="text-sm text-red-400 mt-3">{String(deleteSquad.error)}</p>
      )}
    </div>
  );
}

// SquadRepos is the reverse view of the domain's assignee badges (契约 3.3b):
// which tracked repos route their issues/PRs here. It links to /domains,
// where the binding is actually edited, so the row stays a read-only summary.
function SquadRepos({ squadId, domains }: { squadId: string; domains?: Domain[] }) {
  const repos = (domains ?? []).filter(
    (d) => d.issue_repo !== "" && (d.issue_assignee === squadId || d.pull_assignee === squadId),
  );
  if (repos.length === 0) {
    return <div className="mt-1 text-[11px] text-ink-3">未绑定仓库</div>;
  }
  return (
    <div className="mt-1 flex flex-wrap items-center gap-1.5">
      {repos.map((d) => (
        <Link
          key={d.id}
          href="/domains"
          className="inline-flex max-w-full items-center gap-1.5 rounded border border-line px-1.5 py-0.5 font-mono text-[10px] text-ink-3 transition-colors hover:border-line-strong hover:text-ink-2"
          title={d.issue_repo}
        >
          <span className="truncate">{d.issue_repo}</span>
          {d.issue_assignee === squadId && <span className="shrink-0 text-accent">issue</span>}
          {d.pull_assignee === squadId && <span className="shrink-0 text-accent">PR</span>}
        </Link>
      ))}
    </div>
  );
}

function NewSquadForm({
  agents,
  onClose,
}: {
  agents?: { id: string; name: string }[];
  onClose: () => void;
}) {
  const createSquad = useCreateSquad();
  const addMember = useAddSquadMember();
  const [name, setName] = useState("");
  const [members, setMembers] = useState<string[]>([]);
  const [leaderId, setLeaderId] = useState("");
  const [reviewerId, setReviewerId] = useState("");
  const [description, setDescription] = useState("");
  const [instructions, setInstructions] = useState("");

  const memberAgents = (agents ?? []).filter((a) => members.includes(a.id));
  const toggleMember = (id: string) => {
    const next = members.includes(id) ? members.filter((m) => m !== id) : [...members, id];
    setMembers(next);
    if (!next.includes(leaderId)) setLeaderId("");
    if (!next.includes(reviewerId)) setReviewerId("");
  };

  const [memberFailures, setMemberFailures] = useState<string[]>([]);

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    createSquad.mutate(
      { name, leader_id: leaderId, description, instructions },
      {
        onSuccess: (sq) => {
          const rest = members.filter((m) => m !== leaderId);
          const failures: string[] = [];
          const enqueue = (i: number) => {
            if (i >= rest.length) {
              // 全部成员入队完毕：有失败留在弹窗里展示，全成功才关。
              if (failures.length > 0) setMemberFailures(failures);
              else onClose();
              return;
            }
            const m = rest[i];
            const mname = agents?.find((a) => a.id === m)?.name ?? m;
            addMember.mutate(
              { squadId: sq.id, member_type: "agent", member_id: m, role: m === reviewerId ? "reviewer" : "member" },
              { onSuccess: () => enqueue(i + 1), onError: () => { failures.push(mname); enqueue(i + 1); } }
            );
          };
          enqueue(0);
        },
        // 创建失败：弹窗保持打开，错误已在表单内展示（createSquad.isError）。
        // 原来这里 onClose() 会把错误信息和已填的表单一起吞掉。
      }
    );
  };

  return (
    <Dialog
      title="新建 Squad"
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button type="submit" form="squad-form" disabled={createSquad.isPending}>
            {createSquad.isPending ? "创建中…" : "创建"}
          </Button>
        </>
      }
    >
      <form id="squad-form" onSubmit={handleSubmit} className="space-y-4">
        <Field label="名称" hint="必填">
          <input value={name} onChange={(e) => setName(e.target.value)} className={inputCls} required placeholder="Squad 名称…" />
        </Field>
        <Field label="成员" hint="leader = 协调者；reviewer = 审查者（进审批时平台自动拉去审查）。先选成员，再指定 leader 和 reviewer">
          <div className="border border-line rounded-lg divide-y divide-line max-h-48 overflow-y-auto">
            {(agents ?? []).map((a) => (
              <label key={a.id} className="flex items-center gap-2 px-3 py-2 text-sm cursor-pointer hover:bg-surface-0">
                <input type="checkbox" checked={members.includes(a.id)} onChange={() => toggleMember(a.id)} />
                {a.name}
              </label>
            ))}
          </div>
        </Field>
        <Field label="Leader" hint="必填，从成员中选">
          <select value={leaderId} onChange={(e) => setLeaderId(e.target.value)} className={inputCls} required>
            <option value="">选择…</option>
            {memberAgents.map((a) => (
              <option key={a.id} value={a.id}>{a.name}</option>
            ))}
          </select>
        </Field>
        <Field label="审核者（reviewer）" hint="可选——squad 任务进审批时，平台会自动拉审核者审查">
          <select value={reviewerId} onChange={(e) => setReviewerId(e.target.value)} className={inputCls}>
            <option value="">无</option>
            {memberAgents.filter((a) => a.id !== leaderId).map((a) => (
              <option key={a.id} value={a.id}>{a.name}</option>
            ))}
          </select>
        </Field>
        <Field label="描述">
          <textarea value={description} onChange={(e) => setDescription(e.target.value)} className={inputCls} rows={2} />
        </Field>
        <Field label="Instructions" hint="leader 运行时会注入这些 instructions">
          <textarea value={instructions} onChange={(e) => setInstructions(e.target.value)} className={inputCls} rows={3} placeholder="Squad 工作说明…" />
        </Field>
        {createSquad.isError && (
          <p className="text-sm text-red-400">{String(createSquad.error)}</p>
        )}
        {memberFailures.length > 0 && (
          <p className="text-sm text-amber-400">
            Squad 已创建，但以下成员加入失败：{memberFailures.join("、")}。可稍后在 Squad 详情页手动添加。
          </p>
        )}
      </form>
    </Dialog>
  );
}

function ImportTeamForm({
  onClose,
  onSubmitted,
}: {
  onClose: () => void;
  onSubmitted: () => void;
}) {
  const importMut = useImportTeam();
  const [gitUrl, setGitUrl] = useState("");
  const [credentials, setCredentials] = useState("");
  const [branch, setBranch] = useState("");

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    if (!gitUrl) return;
    importMut.mutate(
      {
        git_url: gitUrl,
        git_credentials: credentials,
        default_branch: branch,
      },
      {
        onSuccess: () => onSubmitted(),
      },
    );
  };

  return (
    <Dialog
      title="导入团队"
      onClose={onClose}
      wide
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button type="submit" form="import-form" disabled={importMut.isPending || !gitUrl}>
            {importMut.isPending ? "导入中…" : "开始导入"}
          </Button>
        </>
      }
    >
      <div className="space-y-2">
        <p className="text-sm text-ink-3">
          从代码仓导入团队定义。管家 agent 会克隆仓库、探索 team.md 及引用的角色和技能文件，
          自动创建 agent、squad 和 skill，并为每个 agent 分配可用 runtime。仓库格式不固定——agent 凭理解力解析。
        </p>
        <form id="import-form" onSubmit={handleSubmit} className="space-y-4">
          <Field label="团队仓 Git URL" hint="https://github.com/org/demo-team.git">
            <input
              className={inputCls}
              value={gitUrl}
              onChange={(e) => setGitUrl(e.target.value)}
              placeholder="https://github.com/org/demo-team.git"
              required
            />
          </Field>
          <div className="grid grid-cols-2 gap-4">
            <Field label="Git Token (可选)" hint="私有仓需要">
              <input
                className={inputCls}
                type="password"
                value={credentials}
                onChange={(e) => setCredentials(e.target.value)}
                placeholder="ghp_..."
              />
            </Field>
            <Field label="默认分支 (可选)">
              <input
                className={inputCls}
                value={branch}
                onChange={(e) => setBranch(e.target.value)}
                placeholder="main"
              />
            </Field>
          </div>
          {importMut.isError && (
            <p className="text-sm text-red-400">{String(importMut.error?.message ?? "导入失败")}</p>
          )}
        </form>
      </div>
    </Dialog>
  );
}
