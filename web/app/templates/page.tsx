"use client";

import { useState } from "react";
import { Boxes, Package, RefreshCw, Users } from "lucide-react";
import {
  Button,
  Empty,
  PageBody,
  PageHeader,
  SectionCard,
  Segmented,
  Skeleton,
  StatStrip,
} from "@/components/ui";
import { useTemplates, useRefreshTemplates } from "@/lib/queries";
import { ProjectTemplateDialog } from "@/components/project-template-dialog";
import { SquadTemplateDialog } from "@/components/squad-template-dialog";
import type { TemplateSummary } from "@/lib/types";

// 模板库入口：squad / project 两个 tab，行式列表列出可用模板，点「应用」打开
// 对应的 apply 弹窗（弹窗内含列表→预览→覆盖表单→结果全流程）。
// 本页是自测前端的独立路由；AI_Shell_WEB 把弹窗挂在各实体页的「从模板创建」
// 按钮上，这里统一到一个入口便于本地联调。
export default function TemplatesPage() {
  const [kind, setKind] = useState<"project" | "squad">("project");
  const [open, setOpen] = useState<null | "project" | "squad">(null);
  const { data: templates, isLoading } = useTemplates(kind);
  const refresh = useRefreshTemplates();
  const kindLabel = kind === "project" ? "项目模板" : "小队模板";
  const errorCount = refresh.data?.errors?.length ?? 0;

  return (
    <PageBody className="space-y-5">
      <PageHeader
        title="模板库"
        desc="从 GitCode 模板仓库拉取项目与小队的标准定义；应用后按模板预填创建流程。"
        action={
          <Button
            variant="outline"
            onClick={() => refresh.mutate(undefined)}
            disabled={refresh.isPending}
            title="从模板仓库重新拉取"
            className="gap-1.5"
          >
            <RefreshCw className={`h-3.5 w-3.5 ${refresh.isPending ? "animate-spin" : ""}`} />
            刷新模板仓库
          </Button>
        }
      />

      <StatStrip
        items={[
          {
            label: "模板数",
            value: templates?.length ?? 0,
            hint: kindLabel,
          },
          {
            label: "类型",
            value: kindLabel,
            tone: "info",
            hint: "在项目模板 / 小队模板之间切换",
          },
          {
            label: "上次拉取",
            value: refresh.data ? refresh.data.fetched : "—",
            tone: errorCount ? "warn" : "muted",
            hint: refresh.data
              ? errorCount
                ? `${errorCount} 个错误，点「刷新模板仓库」重试`
                : "拉取成功，无错误"
              : "尚未在本页刷新过",
          },
        ]}
      />

      <SectionCard
        title={kindLabel}
        count={templates?.length ?? 0}
        desc={refresh.data ? `本次从模板仓库拉取 ${refresh.data.fetched} 个` : "来自 GitCode 模板仓库"}
        action={
          <Segmented
            value={kind}
            onChange={setKind}
            options={[
              { value: "project", label: "项目模板" },
              { value: "squad", label: "小队模板" },
            ]}
          />
        }
      >
        {isLoading ? (
          <div className="divide-y divide-line">
            {[0, 1, 2].map((i) => (
              <div key={i} className="flex items-start gap-3 px-4 py-3">
                <Skeleton className="h-9 w-9 rounded-md" />
                <div className="flex-1 space-y-2">
                  <Skeleton className="h-4 w-48" />
                  <Skeleton className="h-3 w-3/4" />
                  <Skeleton className="h-3 w-1/3" />
                </div>
              </div>
            ))}
          </div>
        ) : !templates || templates.length === 0 ? (
          <Empty icon={<Package className="h-5 w-5" />}>
            暂无{kind === "project" ? "项目" : "小队"}模板 — 点击右上角「刷新模板仓库」从 GitCode 拉取；
            拉取后会在这里逐行列出，点行内「应用」即可套用。
          </Empty>
        ) : (
          <div className="divide-y divide-line">
            {templates.map((t) => (
              <TemplateRow key={t.id} t={t} onApply={() => setOpen(kind)} />
            ))}
          </div>
        )}
      </SectionCard>

      {open === "project" && <ProjectTemplateDialog onClose={() => setOpen(null)} />}
      {open === "squad" && <SquadTemplateDialog onClose={() => setOpen(null)} />}
    </PageBody>
  );
}

// TemplateRow 是一行模板元数据：图标、名称/版本/类型、描述、id 与标签；
// 「应用」留在行内，打开对应的 apply 弹窗。
function TemplateRow({ t, onApply }: { t: TemplateSummary; onApply: () => void }) {
  const Icon = t.kind === "squad-template" ? Users : Boxes;
  return (
    <div className="flex items-start gap-3 px-4 py-3">
      <span className="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-md border border-line bg-surface-2 text-ink-3">
        <Icon className="h-4 w-4" />
      </span>
      <div className="min-w-0 flex-1 space-y-1">
        <div className="flex flex-wrap items-center gap-2">
          <span className="truncate text-sm font-medium text-ink">{t.name}</span>
          <span className="font-mono text-[10px] text-ink-3">v{t.version}</span>
          <BadgeLike kind={t.kind} />
          <Button variant="outline" className="ml-auto h-7 shrink-0 px-2.5 text-xs" onClick={onApply}>
            应用
          </Button>
        </div>
        {t.description && (
          <p className="line-clamp-2 text-xs leading-relaxed text-ink-3">{t.description}</p>
        )}
        <div className="flex flex-wrap items-center gap-x-2 gap-y-1 font-mono text-[10px] text-ink-3">
          <span className="truncate" title={t.id}>{t.id}</span>
          {t.tags?.length
            ? t.tags.map((tag) => (
                <span key={tag} className="rounded border border-line px-1.5 py-0.5 text-ink-3">
                  {tag}
                </span>
              ))
            : null}
        </div>
      </div>
    </div>
  );
}

// BadgeLike 沿用既有 Badge 的状态色槽位，但显示中文类型名——模板类型不是
// 运行状态，直接复用 Badge 会露出英文 status。
function BadgeLike({ kind }: { kind: string }) {
  const isProject = kind === "project-template";
  return (
    <span
      className={`inline-flex items-center gap-1.5 rounded-md px-2 py-0.5 text-xs font-medium ${
        isProject ? "bg-blue-500/10 text-blue-300" : "bg-purple-500/10 text-purple-300"
      }`}
    >
      <span className={`h-1.5 w-1.5 rounded-full ${isProject ? "bg-blue-400" : "bg-purple-400"}`} />
      {isProject ? "项目模板" : "小队模板"}
    </span>
  );
}
