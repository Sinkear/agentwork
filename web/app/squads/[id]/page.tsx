"use client";

import { useState } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import { useSquads, useAgents, useSquadMembers, useAddSquadMember, useUpdateSquad, useRemoveSquadMember, useGoalEvents, useDomains, usePatchDomainAssignees } from "@/lib/queries";
import { Button, PageHeader, Empty, Dialog, Field, inputCls } from "@/components/ui";
import type { Domain, Squad, SquadMember } from "@/lib/types";

export default function SquadDetailPage() {
  useGoalEvents();
  const params = useParams<{ id: string }>();
  const id = params.id;
  const { data: squads } = useSquads();
  const { data: agents } = useAgents();
  const { data: members, isLoading } = useSquadMembers(id);
  const addMember = useAddSquadMember();
  const updateSquad = useUpdateSquad();
  const removeMember = useRemoveSquadMember();
  const [showAdd, setShowAdd] = useState(false);
  const [showEdit, setShowEdit] = useState(false);
  const [editName, setEditName] = useState("");
  const [editLeader, setEditLeader] = useState("");
  const [editDesc, setEditDesc] = useState("");
  const [editInstr, setEditInstr] = useState("");

  const squad = squads?.find((s) => s.id === id);
  const agentName = (aid: string) => agents?.find((a) => a.id === aid)?.name ?? aid;

  if (!squad) {
    if (squads === undefined) return <div className="p-8 text-sm text-ink-3">加载中…</div>;
    return <div className="p-8 text-sm text-ink-3">找不到 Squad。</div>;
  }

  return (
    <div className="p-8 max-w-4xl space-y-5">
      <div>
        <Link href="/squads" className="text-sm text-ink-3 hover:text-ink-2 hover:underline">
          ← 返回列表
        </Link>
        <h1 className="text-lg font-semibold text-ink mt-3">{squad.name}</h1>
      </div>

      {/* Squad info */}
      <div className="bg-surface-1 rounded-xl border border-line p-5 space-y-3">
        <div className="flex items-center justify-between">
          <div className="grid grid-cols-2 gap-3 text-sm">
            <div>
              <span className="text-ink-3">Leader：</span>
              <span className="text-ink-2">{agentName(squad.leader_id)}</span>
            </div>
            <div>
              <span className="text-ink-3">创建时间：</span>
              <span className="text-ink-2">{new Date(squad.created_at).toLocaleString()}</span>
            </div>
          </div>
          <Button variant="outline" onClick={() => { setEditName(squad.name); setEditLeader(squad.leader_id); setEditDesc(squad.description || ""); setEditInstr(squad.instructions || ""); setShowEdit(true); }}>
            编辑
          </Button>
        </div>
        {squad.description && (
          <div>
            <span className="text-xs text-ink-3 block mb-1">描述</span>
            <p className="text-sm text-ink-2">{squad.description}</p>
          </div>
        )}
        {squad.instructions && (
          <div>
            <span className="text-xs text-ink-3 block mb-1">Instructions</span>
            <p className="text-sm text-ink-2 whitespace-pre-wrap">{squad.instructions}</p>
          </div>
        )}
      </div>

      {/* Members */}
      <div className="bg-surface-1 rounded-xl border border-line overflow-hidden">
        <div className="px-4 py-2.5 border-b border-line flex items-center justify-between">
          <span className="text-xs font-medium text-ink-3 uppercase tracking-wide">
            成员{members && members.length > 0 && `（${members.length}）`}
          </span>
          <Button onClick={() => setShowAdd(true)}>+ 添加</Button>
        </div>
        <div className="p-4">
          {isLoading ? (
            <div className="text-sm text-ink-3 text-center py-8">加载中…</div>
          ) : !members || members.length === 0 ? (
            <Empty>暂无成员。</Empty>
          ) : (
            <table className="w-full text-sm">
              <thead>
                <tr className="border-b border-line text-left text-xs font-medium text-ink-3 uppercase tracking-wide">
                  <th className="px-3 py-2">类型</th>
                  <th className="px-3 py-2">名称</th>
                  <th className="px-3 py-2">角色</th>
                  <th className="px-3 py-2">添加时间</th>
                  <th className="px-3 py-2"></th>
                </tr>
              </thead>
              <tbody>
                {members.map((m: SquadMember) => (
                  <tr key={m.id} className="border-b border-line">
                    <td className="px-3 py-2">
                      <span className={`px-2 py-0.5 text-xs rounded ${m.member_type === "agent" ? "bg-blue-500/10 text-blue-300" : "bg-surface-2 text-ink-2"}`}>
                        {m.member_type}
                      </span>
                    </td>
                    <td className="px-3 py-2 text-ink-2">
                      {m.member_type === "agent" ? (agentName(m.member_id) || m.member_id) : m.member_id}
                    </td>
                    <td className="px-3 py-2 text-ink-3">{m.role || "-"}</td>
                    <td className="px-3 py-2 text-ink-3 text-xs">{new Date(m.created_at).toLocaleString()}</td>
                    <td className="px-3 py-2">
                      <button
                        onClick={() => removeMember.mutate({ squadId: id, memberId: m.member_id })}
                        disabled={removeMember.isPending && removeMember.variables?.memberId === m.member_id}
                        className="text-red-400 hover:text-red-300 text-xs disabled:opacity-50"
                        title="移除该成员"
                      >✕</button>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          )}
        </div>
      </div>

      {/* 成员移除失败必须显示——后端 400 错误静默等于「点了没反应」。 */}
      {removeMember.isError && (
        <p className="text-sm text-red-400">{String(removeMember.error)}</p>
      )}

      {/* 仓库接入：squad ↔ repo 的绑定在两边都能看见、都能改（契约 3.3c） */}
      <RepoAccessSection squadId={id} agents={agents} squads={squads} />

      {/* Add member dialog */}
      {showAdd && (
        <AddMemberDialog
          squadId={id}
          agents={agents}
          onClose={() => setShowAdd(false)}
        />
      )}

      {showEdit && (
        <Dialog
          title={`编辑 Squad：${squad.name}`}
          onClose={() => setShowEdit(false)}
          footer={
            <>
              <Button variant="outline" onClick={() => setShowEdit(false)}>取消</Button>
              <Button
                onClick={() =>
                  updateSquad.mutate(
                    { id, name: editName, description: editDesc, leader_id: editLeader, instructions: editInstr },
                    { onSuccess: () => setShowEdit(false) }
                  )
                }
                disabled={updateSquad.isPending || !editName.trim() || !editLeader}
              >
                {updateSquad.isPending ? "保存中…" : "保存"}
              </Button>
            </>
          }
        >
          <div className="space-y-4">
            <Field label="名称" hint="必填">
              <input value={editName} onChange={(e) => setEditName(e.target.value)} className={inputCls} placeholder="Squad 名称…" />
            </Field>
            <Field label="Leader" hint="必填，leader 必须是 agent（角色动态生效：旧 leader 的 in-flight run 会孤儿化）">
              <select value={editLeader} onChange={(e) => setEditLeader(e.target.value)} className={inputCls}>
                <option value="">选择…</option>
                {agents?.map((a) => (
                  <option key={a.id} value={a.id}>{a.name}</option>
                ))}
              </select>
            </Field>
            <Field label="描述">
              <textarea value={editDesc} onChange={(e) => setEditDesc(e.target.value)} className={inputCls} rows={2} />
            </Field>
            <Field label="Instructions" hint="leader 运行时会注入这些 instructions">
              <textarea value={editInstr} onChange={(e) => setEditInstr(e.target.value)} className={inputCls} rows={3} placeholder="Squad 工作说明…" />
            </Field>
            {/* 保存失败必须显示——leader 校验等后端 400 错误静默等于「点了没反应」。 */}
            {updateSquad.isError && (
              <p className="text-sm text-red-400">{String(updateSquad.error)}</p>
            )}
          </div>
        </Dialog>
      )}
    </div>
  );
}

function AddMemberDialog({
  squadId,
  agents,
  onClose,
}: {
  squadId: string;
  agents?: { id: string; name: string }[];
  onClose: () => void;
}) {
  const addMember = useAddSquadMember();
  const [memberType, setMemberType] = useState("agent");
  const [memberId, setMemberId] = useState("");
  const [role, setRole] = useState("member");

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    addMember.mutate(
      { squadId, member_type: memberType, member_id: memberId, role },
      { onSuccess: onClose }
    );
  };

  return (
    <Dialog
      title="添加成员"
      onClose={onClose}
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button type="submit" form="add-member-form" disabled={addMember.isPending}>
            {addMember.isPending ? "…" : "添加"}
          </Button>
        </>
      }
    >
      <form id="add-member-form" onSubmit={handleSubmit} className="space-y-4">
        <Field label="成员类型">
          <select value={memberType} onChange={(e) => { setMemberType(e.target.value); setMemberId(""); }} className={inputCls}>
            <option value="agent">Agent</option>
            <option value="human">Human</option>
          </select>
        </Field>
        {memberType === "agent" && (
          <Field label="选择 Agent">
            <select value={memberId} onChange={(e) => setMemberId(e.target.value)} className={inputCls} required>
              <option value="">选择…</option>
              {agents?.map((a) => (
                <option key={a.id} value={a.id}>{a.name}</option>
              ))}
            </select>
          </Field>
        )}
        {memberType === "human" && (
          <Field label="Human ID">
            <input value={memberId} onChange={(e) => setMemberId(e.target.value)} className={inputCls} required placeholder="human id…" />
          </Field>
        )}
        <Field label="角色">
          <select value={role} onChange={(e) => setRole(e.target.value)} className={inputCls}>
            <option value="member">执行者（被 leader 分派干活）</option>
            <option value="reviewer">审核者（进审批时被平台自动拉去审查）</option>
          </select>
        </Field>
        {addMember.isError && (
          <p className="text-sm text-red-400">{String(addMember.error)}</p>
        )}
      </form>
    </Dialog>
  );
}

// RepoAccessSection binds tracked repos to this squad. Each repo has two
// independent toggles (issue route / PR route); clicking a bound one clears
// it, clicking an unbound one takes it over — the same PATCH the domain
// dialog exposes, so both sides stay in sync through query invalidation.
function RepoAccessSection({
  squadId,
  agents,
  squads,
}: {
  squadId: string;
  agents?: { id: string; name: string }[];
  squads?: Squad[];
}) {
  const { data: domains, isLoading } = useDomains();
  const patch = usePatchDomainAssignees();
  const repos = (domains ?? []).filter((d) => d.type === "repo" && d.issue_repo !== "");

  const nameOf = (type: string, aid: string) =>
    type === "squad" ? squads?.find((s) => s.id === aid)?.name ?? "" : agents?.find((a) => a.id === aid)?.name ?? "";

  const toggle = (d: Domain, kind: "issue" | "pull") => {
    const bound = kind === "issue" ? d.issue_assignee === squadId : d.pull_assignee === squadId;
    const next = bound ? "" : squadId;
    patch.mutate({
      domainId: d.id,
      issue_assignee: kind === "issue" ? next : d.issue_assignee,
      issue_assignee_type: kind === "issue" && !bound ? "squad" : d.issue_assignee_type,
      pull_assignee: kind === "pull" ? next : d.pull_assignee,
      pull_assignee_type: kind === "pull" && !bound ? "squad" : d.pull_assignee_type,
    });
  };

  const toggleBtn = (d: Domain, kind: "issue" | "pull") => {
    const bound = kind === "issue" ? d.issue_assignee === squadId : d.pull_assignee === squadId;
    const other = kind === "issue" ? d.issue_assignee : d.pull_assignee;
    const otherType = kind === "issue" ? d.issue_assignee_type : d.pull_assignee_type;
    const otherName = other && other !== squadId ? nameOf(otherType, other) : "";
    return (
      <button
        type="button"
        onClick={() => toggle(d, kind)}
        disabled={patch.isPending && patch.variables?.domainId === d.id}
        title={bound ? "点击解绑" : otherName ? `当前由 ${otherName} 处理，点击改为本 squad` : "点击绑定给本 squad"}
        className={`shrink-0 rounded border px-2 py-0.5 font-mono text-[10px] transition-colors disabled:opacity-50 ${
          bound
            ? "border-accent/40 bg-accent/10 text-accent"
            : "border-line text-ink-3 hover:border-line-strong hover:text-ink-2"
        }`}
      >
        {kind === "issue" ? "负责 issue" : "负责 PR"}
      </button>
    );
  };

  return (
    <div className="bg-surface-1 rounded-xl border border-line overflow-hidden">
      <div className="px-4 py-2.5 border-b border-line">
        <span className="text-xs font-medium text-ink-3 uppercase tracking-wide">仓库接入</span>
      </div>
      <div className="p-4 space-y-2.5">
        <p className="text-xs text-ink-3">绑定后，该仓库的新 issue/PR 会路由给本 squad 的 leader。</p>
        {isLoading ? (
          <div className="text-sm text-ink-3 text-center py-6">加载中…</div>
        ) : repos.length === 0 ? (
          <p className="text-sm text-ink-3">还没有跟踪仓库的项目——先去 Domains 页创建一个 repo 类型的 domain。</p>
        ) : (
          <div className="space-y-1.5">
            {repos.map((d) => (
              <div key={d.id} className="flex items-center gap-2">
                <span className="min-w-0 flex-1 truncate font-mono text-xs text-ink-2" title={d.issue_repo}>
                  {d.issue_repo}
                </span>
                {toggleBtn(d, "issue")}
                {toggleBtn(d, "pull")}
              </div>
            ))}
          </div>
        )}
        {patch.isError && <p className="text-xs text-red-400">{String(patch.error)}</p>}
      </div>
    </div>
  );
}
