"use client";

import { useState } from "react";
import { useParams } from "next/navigation";
import Link from "next/link";
import {
  useSquads,
  useAgents,
  useSquadMembers,
  useAddSquadMember,
  useUpdateSquad,
  useRemoveSquadMember,
  useGoalEvents,
  useDomains,
  usePatchDomainAssignees,
  useGoals,
} from "@/lib/queries";
import {
  Button,
  PageBody,
  PageHeader,
  SectionCard,
  StatStrip,
  Empty,
  Skeleton,
  Dialog,
  Field,
  inputCls,
} from "@/components/ui";
import { Users, GitBranch, Trash2 } from "lucide-react";
import { cn, relTime } from "@/lib/utils";
import type { Domain, Squad, SquadMember } from "@/lib/types";

export default function SquadDetailPage() {
  useGoalEvents();
  const params = useParams<{ id: string }>();
  const id = params.id;
  const { data: squads } = useSquads();
  const { data: agents } = useAgents();
  const { data: members, isLoading } = useSquadMembers(id);
  const { data: goals } = useGoals();
  const { data: domains } = useDomains();
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

  // 真数据派生：人数、reviewer 数、绑到本 squad 的仓库、状态为 active 的任务。
  const reviewerCount = (members ?? []).filter((m) => m.role === "reviewer").length;
  const agentMembers = (members ?? []).filter((m) => m.member_type === "agent").length;
  const humanMembers = (members ?? []).filter((m) => m.member_type === "human").length;
  const boundRepos = (domains ?? []).filter(
    (d) => d.issue_repo !== "" && (d.issue_assignee === id || d.pull_assignee === id),
  );
  const trackedRepoCount = (domains ?? []).filter((d) => d.type === "repo" && d.issue_repo !== "").length;
  const squadGoals = (goals ?? []).filter(
    (g) => g.assignee_type === "squad" && g.assignee_id === id,
  );
  const activeGoals = squadGoals.filter((g) => g.status === "active").length;
  const reviewGoals = squadGoals.filter((g) => g.status === "review").length;

  const openEdit = () => {
    if (!squad) return;
    setEditName(squad.name);
    setEditLeader(squad.leader_id);
    setEditDesc(squad.description || "");
    setEditInstr(squad.instructions || "");
    setShowEdit(true);
  };

  if (!squad) {
    return (
      <PageBody>
        <PageHeader title="小队详情" desc="成员、任务与仓库接入" />
        {squads === undefined ? (
          <div className="space-y-3">
            <Skeleton className="h-24" />
            <Skeleton className="h-44" />
          </div>
        ) : (
          <SectionCard bodyClassName="p-0">
            <Empty icon={<Users className="h-4 w-4" />}>
              找不到这个 Squad，可能已被删除。
              <Link href="/squads" className="mt-1 block text-accent hover:text-accent-dim">
                返回小队列表
              </Link>
            </Empty>
          </SectionCard>
        )}
      </PageBody>
    );
  }

  return (
    <PageBody>
      <PageHeader
        title={squad.name}
        desc={squad.description || "这支小队还没有描述。"}
        action={
          <div className="flex items-center gap-3">
            <Link
              href="/squads"
              className="text-xs text-ink-3 transition-colors hover:text-ink-2"
            >
              ← 返回小队列表
            </Link>
            <Button variant="outline" onClick={openEdit}>
              编辑
            </Button>
          </div>
        }
      />

      <StatStrip
        items={[
          {
            label: "成员数",
            value: members?.length ?? 0,
            hint: reviewerCount > 0 ? `${reviewerCount} 名 reviewer` : "agent / human 成员",
          },
          {
            label: "负责仓库",
            value: boundRepos.length,
            tone: boundRepos.length > 0 ? "info" : "muted",
            hint: `共跟踪 ${trackedRepoCount} 个仓库`,
          },
          {
            label: "进行中任务",
            value: activeGoals,
            tone: activeGoals > 0 ? "info" : "muted",
            hint: reviewGoals > 0 ? `另有 ${reviewGoals} 个待审批` : "状态 active 的任务",
          },
        ]}
      />

      {/* 小队信息：leader / 创建时间 / 成员构成 / instructions */}
      <SectionCard
        title="概览"
        desc="Leader 接任务并分派给成员；instructions 会注入 leader 的运行"
      >
        <dl className="divide-y divide-line">
          <div className="flex items-center justify-between gap-3 px-4 py-2.5">
            <dt className="text-xs text-ink-3">Leader</dt>
            <dd className="truncate text-sm text-ink-2">{agentName(squad.leader_id)}</dd>
          </div>
          <div className="flex items-center justify-between gap-3 px-4 py-2.5">
            <dt className="text-xs text-ink-3">成员构成</dt>
            <dd className="font-mono text-xs text-ink-2">
              {members?.length ?? 0} 人（agent {agentMembers} · human {humanMembers}）
            </dd>
          </div>
          <div className="flex items-center justify-between gap-3 px-4 py-2.5">
            <dt className="text-xs text-ink-3">创建时间</dt>
            <dd className="font-mono text-xs text-ink-2" title={squad.created_at}>
              {new Date(squad.created_at).toLocaleString()}
            </dd>
          </div>
        </dl>
        {squad.instructions && (
          <div className="border-t border-line px-4 py-3">
            <div className="font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3">
              Instructions
            </div>
            <p className="mt-1 whitespace-pre-wrap text-sm leading-relaxed text-ink-2">
              {squad.instructions}
            </p>
          </div>
        )}
      </SectionCard>

      {/* Members */}
      <SectionCard
        title="成员"
        count={members?.length ?? 0}
        desc="leader 由 squad 自身指定；reviewer 在进审批时被平台自动拉入"
        bodyClassName="p-0"
        action={
          <Button onClick={() => setShowAdd(true)} className="h-7 px-2.5 text-xs">
            + 添加
          </Button>
        }
      >
        {isLoading ? (
          <div className="divide-y divide-line">
            {[0, 1, 2].map((i) => (
              <div key={i} className="flex items-center gap-3 px-4 py-2.5">
                <Skeleton className="h-5 w-12" />
                <Skeleton className="h-3 w-48" />
              </div>
            ))}
          </div>
        ) : !members || members.length === 0 ? (
          <Empty icon={<Users className="h-4 w-4" />}>
            暂无成员。点「+ 添加」把 agent 拉进小队。
          </Empty>
        ) : (
          <ul className="divide-y divide-line">
            {members.map((m: SquadMember) => {
              const isLeader = m.member_type === "agent" && m.member_id === squad.leader_id;
              const memberName =
                m.member_type === "agent" ? agentName(m.member_id) || m.member_id : m.member_id;
              return (
                <li key={m.id} className="flex items-center gap-3 px-4 py-2.5">
                  <span
                    className={cn(
                      "inline-flex shrink-0 items-center rounded border px-1.5 py-0.5 font-mono text-[10px] font-medium",
                      m.member_type === "agent"
                        ? "border-blue-500/30 bg-blue-500/10 text-blue-300"
                        : "border-line bg-surface-2 text-ink-2",
                    )}
                  >
                    {m.member_type}
                  </span>
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="truncate text-sm text-ink-2">{memberName}</span>
                      {isLeader && (
                        <span className="inline-flex items-center rounded border border-accent/30 bg-accent/10 px-1.5 py-0.5 text-[10px] font-medium text-accent">
                          leader
                        </span>
                      )}
                      {m.role === "reviewer" && (
                        <span className="inline-flex items-center rounded border border-amber-500/30 bg-amber-500/10 px-1.5 py-0.5 text-[10px] font-medium text-amber-300">
                          reviewer
                        </span>
                      )}
                      {!isLeader && m.role && m.role !== "reviewer" && (
                        <span className="font-mono text-[10px] text-ink-3">{m.role}</span>
                      )}
                    </div>
                    <div className="mt-0.5 flex items-center gap-1.5 text-[10px] text-ink-3">
                      <span className="font-mono" title={m.member_id}>
                        {m.member_id.slice(0, 8)}
                      </span>
                      <span>·</span>
                      <span title={new Date(m.created_at).toLocaleString()}>
                        {relTime(m.created_at)}加入
                      </span>
                    </div>
                  </div>
                  <button
                    onClick={() => removeMember.mutate({ squadId: id, memberId: m.member_id })}
                    disabled={removeMember.isPending && removeMember.variables?.memberId === m.member_id}
                    className="inline-flex h-7 w-7 shrink-0 items-center justify-center rounded-md text-ink-3 transition-colors hover:bg-surface-2 hover:text-red-400 disabled:opacity-40"
                    title="移除该成员"
                    aria-label={`移除成员 ${memberName}`}
                  >
                    <Trash2 className="h-3.5 w-3.5" />
                  </button>
                </li>
              );
            })}
          </ul>
        )}
      </SectionCard>

      {/* 成员移除失败必须显示——后端 400 错误静默等于「点了没反应」。 */}
      {removeMember.isError && (
        <p className="text-sm text-red-400">{String(removeMember.error)}</p>
      )}

      {/* 仓库接入：squad ↔ repo 的绑定在两边都能看见、都能改（契约 3.3c） */}
      <SectionCard
        title="仓库接入"
        count={trackedRepoCount}
        desc="绑定后，该仓库的新 issue / PR 会路由给本 squad 的 leader"
      >
        <RepoAccessSection squadId={id} agents={agents} squads={squads} />
      </SectionCard>

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
    </PageBody>
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
// 外层版面由详情页的 SectionCard 提供；这里只负责内容与四字段整体覆盖。
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
    <div className="p-4">
      {isLoading ? (
        <div className="space-y-2">
          <Skeleton className="h-5 w-full" />
          <Skeleton className="h-5 w-full" />
        </div>
      ) : repos.length === 0 ? (
        <Empty icon={<GitBranch className="h-4 w-4" />}>
          还没有跟踪仓库的项目——先去 Domains 页创建一个 repo 类型的 domain。
        </Empty>
      ) : (
        <div className="divide-y divide-line">
          {repos.map((d) => (
            <div key={d.id} className="flex flex-wrap items-center gap-2 py-2.5 first:pt-0 last:pb-0">
              <div className="min-w-0 flex-1">
                <div className="truncate font-mono text-xs text-ink-2" title={d.issue_repo}>
                  {d.issue_repo}
                </div>
                <div className="mt-0.5 truncate text-[10px] text-ink-3">
                  {d.name} · {d.default_branch || "默认分支"}
                </div>
              </div>
              {toggleBtn(d, "issue")}
              {toggleBtn(d, "pull")}
            </div>
          ))}
        </div>
      )}
      {patch.isError && <p className="mt-2 text-xs text-red-400">{String(patch.error)}</p>}
    </div>
  );
}