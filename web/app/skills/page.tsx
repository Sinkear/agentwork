"use client";

import { useState } from "react";
import { Sparkles } from "lucide-react";
import { useSkills, useCreateSkill, useDeleteSkill } from "@/lib/queries";
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
import type { Skill } from "@/lib/types";

const ROW_GRID =
  "grid grid-cols-1 gap-1.5 px-4 py-3 transition-colors hover:bg-surface-2/40 md:grid-cols-[minmax(0,1fr)_72px_64px] md:items-center md:gap-3";

const HEAD_GRID =
  "hidden md:grid md:grid-cols-[minmax(0,1fr)_72px_64px] md:gap-3 border-b border-line px-4 py-2 font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3";

function fullTime(iso: string): string {
  return iso ? new Date(iso).toLocaleString() : "—";
}

// 平台 Skill 模型只有 id/name/description/created_at：没有来源、分类、大小、
// updated_at 字段，所以列表不编这些列，只显示真实存在的「上传时间」。
function SkillRow({
  sk,
  onDelete,
  deleting,
  error,
}: {
  sk: Skill;
  onDelete: (id: string) => void;
  deleting: boolean;
  error?: string;
}) {
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

      <div className="flex items-center justify-between gap-2 text-xs md:block">
        <span className="text-[11px] text-ink-3 md:hidden">上传</span>
        <span className="text-ink-2" title={fullTime(sk.created_at)}>
          {relTime(sk.created_at)}
        </span>
      </div>

      <div className="flex items-center justify-end text-xs">
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
        desc="平台技能库：上传 skill 包（SKILL.md），勾选给 agent 后随 run 下发到项目级 skills 目录，不碰机器上的全局 skills。"
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
                />
              ))}
            </ul>
          </>
        )}
      </SectionCard>

      {showCreate && <CreateSkillDialog onClose={() => setShowCreate(false)} />}
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