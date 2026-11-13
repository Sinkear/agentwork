"use client";

import { useState } from "react";
import { useCreateAgent, useUpdateAgent, useRuntimes, useSkills } from "@/lib/queries";
import type { Agent, McpServer } from "@/lib/types";
import { Button, Dialog, Field, inputCls } from "@/components/ui";

// 常用模型清单：只是输入便利，不限制可用模型——不在清单里的用「自定义…」。
// 提交时仍然只写 agent.model 字段，后端/平台不感知这份清单。
const COMMON_MODELS: string[] = [
  // Claude
  "claude-sonnet-4-5",
  "claude-opus-4-1",
  "claude-haiku-4-5",
  // GPT
  "gpt-5",
  "gpt-5-mini",
  "gpt-4.1",
  // Gemini
  "gemini-2.5-pro",
  "gemini-2.5-flash",
  // DeepSeek
  "deepseek-chat",
  "deepseek-reasoner",
];
const CUSTOM_MODEL = "__custom__";

// KVRows is a name-value pair list editor (env vars / headers) — a "+"
// appends an empty row, ✕ removes one. Rows carry {name, value} (the acp
// wire shape), so the form never touches JSON.
function KVRows({ rows, onChange, keyPlaceholder, valuePlaceholder }: {
  rows: { name: string; value: string }[];
  onChange: (rows: { name: string; value: string }[]) => void;
  keyPlaceholder?: string;
  valuePlaceholder?: string;
}) {
  return (
    <div className="space-y-1.5">
      {rows.map((r, i) => (
        <div key={i} className="flex gap-1.5">
          <input
            value={r.name}
            onChange={(e) => onChange(rows.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))}
            className={`${inputCls} flex-1 min-w-0 font-mono !py-1 text-xs`}
            placeholder={keyPlaceholder ?? "名称"}
          />
          <input
            value={r.value}
            onChange={(e) => onChange(rows.map((x, j) => (j === i ? { ...x, value: e.target.value } : x)))}
            className={`${inputCls} flex-1 min-w-0 font-mono !py-1 text-xs`}
            placeholder={valuePlaceholder ?? "值"}
          />
          <button
            type="button"
            onClick={() => onChange(rows.filter((_, j) => j !== i))}
            className="text-ink-3 hover:text-red-400 shrink-0"
            title="删除"
          >✕</button>
        </div>
      ))}
      <button type="button" className="text-accent hover:text-accent text-xs" onClick={() => onChange([...rows, { name: "", value: "" }])}>
        + 添加
      </button>
    </div>
  );
}

// AgentForm: 新建/编辑 Agent。基础配置平铺，运维项（并发/环境变量/额外
// MCP 服务器）收进"高级设置"折叠区——env 是键值对行，MCP 服务器是结构化
// 条目，都不要求手写 JSON。
export function AgentForm({ agent, onClose }: { agent?: Agent; onClose: () => void }) {
  const create = useCreateAgent();
  const update = useUpdateAgent();
  const { data: runtimes } = useRuntimes();
  const [name, setName] = useState(agent?.name ?? "");
  const [type, setType] = useState(agent?.type ?? "standard");
  const [description, setDescription] = useState(agent?.description ?? "");
  const [runtimeId, setRuntimeId] = useState(agent?.runtime_id ?? "");
  const [systemPrompt, setSystemPrompt] = useState(agent?.system_prompt ?? "");
  const [model, setModel] = useState(agent?.model ?? "");
  // 编辑已有 agent 时，model 不在常用清单里 → 自动落到「自定义…」并回填。
  const [customModel, setCustomModel] = useState(
    () => Boolean(agent?.model) && !COMMON_MODELS.includes(agent?.model ?? "")
  );
  // 记住上一次手输的模型 ID：在「常用项 ↔ 自定义…」之间来回切时不丢输入。
  const [customDraft, setCustomDraft] = useState(() =>
    Boolean(agent?.model) && !COMMON_MODELS.includes(agent?.model ?? "") ? (agent?.model ?? "") : ""
  );
  const [envRows, setEnvRows] = useState<{ name: string; value: string }[]>(
    agent?.env ? Object.entries(agent.env).map(([name, value]) => ({ name, value })) : []
  );
  const [mcpRows, setMcpRows] = useState<McpServer[]>(agent?.mcp_servers ?? []);
  const [maxConcurrent, setMaxConcurrent] = useState(String(agent?.max_concurrent ?? 3));
  const [skills, setSkills] = useState<string[]>(agent?.skills ?? []);
  // 技能库可能几十上百条：名字/ID 大小写不敏感过滤，避免在长列表里翻找。
  const [skillQuery, setSkillQuery] = useState("");
  const { data: skillLib } = useSkills();
  const skillQ = skillQuery.trim().toLowerCase();
  const visibleSkills = (skillLib ?? []).filter(
    (sk) =>
      !skillQ ||
      sk.name.toLowerCase().includes(skillQ) ||
      sk.id.toLowerCase().includes(skillQ)
  );

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    const parsedEnv: Record<string, string> = {};
    for (const r of envRows) {
      if (r.name.trim()) parsedEnv[r.name.trim()] = r.value;
    }
    const body = {
      name,
      type,
      description,
      runtime_id: runtimeId,
      system_prompt: systemPrompt,
      model,
      env: parsedEnv,
      // acp wire semantics: type "" = stdio (not the string "stdio").
      mcp_servers: mcpRows
        .filter((m) => m.name.trim())
        .map((m) => ({ ...m, type: m.type === "stdio" ? "" : m.type })),
      max_concurrent: parseInt(maxConcurrent) || 3,
      skills,
    };
    if (agent) {
      update.mutate({ id: agent.id, ...body }, { onSuccess: onClose });
    } else {
      create.mutate(body, { onSuccess: onClose });
    }
  };

  return (
    <Dialog
      title={agent ? `编辑 Agent：${agent.name}` : "新建 Agent"}
      onClose={onClose}
      wide
      footer={
        <>
          <Button variant="outline" onClick={onClose}>取消</Button>
          <Button type="submit" form="agent-form" disabled={create.isPending || update.isPending}>
            {(create.isPending || update.isPending) ? "保存中…" : agent ? "保存" : "创建"}
          </Button>
        </>
      }
    >
      <form id="agent-form" onSubmit={handleSubmit} className="space-y-4">
        <Field label="名称">
          <input value={name} onChange={(e) => setName(e.target.value)} className={inputCls} required />
        </Field>

        <Field label="类型" hint="标准=普通 agent；管家=系统级 agent（team 导入 / 自然语言指令）">
          <select value={type} onChange={(e) => setType(e.target.value)} className={inputCls}>
            <option value="standard">标准</option>
            <option value="steward">管家</option>
          </select>
        </Field>

        <Field label="描述" hint="一句话说明这个 agent 干什么（列表里展示）；和 System Prompt 不同，这是给人看的">
          <input value={description} onChange={(e) => setDescription(e.target.value)} className={inputCls} placeholder="如：前端实现 + UI 审查" />
        </Field>

        <Field label="Runtime">
          <select value={runtimeId} onChange={(e) => setRuntimeId(e.target.value)} className={inputCls} required>
            <option value="">选择…</option>
            {runtimes?.map((rt) => (
              <option key={rt.id} value={rt.id}>{rt.name}</option>
            ))}
          </select>
        </Field>

        <Field label="System Prompt">
          <textarea value={systemPrompt} onChange={(e) => setSystemPrompt(e.target.value)} className={`${inputCls} font-mono`} rows={3} />
        </Field>

        <Field label="Skills（平台技能库）" hint="勾选的 skill 会下发到该 agent 所在机器，run 时按原名装入工作目录的项目级 skills">
          <div className="space-y-2">
            <div className="flex items-center gap-2">
              <input
                value={skillQuery}
                onChange={(e) => setSkillQuery(e.target.value)}
                className={`${inputCls} flex-1 !py-1.5 text-xs`}
                placeholder="搜索技能名称 / ID…"
                aria-label="搜索技能"
              />
              <span className="shrink-0 whitespace-nowrap text-[11px] text-ink-3 tabular-nums">
                已选 {skills.length}
                {skillLib ? ` / 共 ${skillLib.length}` : ""}
              </span>
            </div>
            {/* 可拖拽高度的列表（右下角 resize-y）：min 12rem，最高 60vh；
                Dialog 内容本身可滚动，不会把标题/底部按钮挤出屏幕。 */}
            <div className="min-h-48 max-h-[60vh] resize-y overflow-y-auto rounded-lg border border-line divide-y divide-line">
              {visibleSkills.length > 0 ? (
                visibleSkills.map((sk) => (
                  <label key={sk.id} className="flex items-center gap-2 px-3 py-2 text-sm cursor-pointer hover:bg-surface-0">
                    <input
                      type="checkbox"
                      checked={skills.includes(sk.id)}
                      onChange={(e) =>
                        setSkills((prev) =>
                          e.target.checked ? [...prev, sk.id] : prev.filter((x) => x !== sk.id)
                        )
                      }
                    />
                    <span className="font-medium">{sk.name}</span>
                    {sk.description && <span className="text-xs text-ink-3">— {sk.description}</span>}
                  </label>
                ))
              ) : skillLib && skillLib.length > 0 ? (
                <p className="px-3 py-2 text-xs text-ink-3">没有匹配的技能</p>
              ) : (
                <p className="px-3 py-2 text-xs text-ink-3">暂无技能——在「Skills」页面上传 skill 包后可在此勾选</p>
              )}
            </div>
          </div>
        </Field>

        <Field label="Model" hint="留空用 runtime 默认">
          <select
            value={customModel ? CUSTOM_MODEL : model}
            onChange={(e) => {
              const v = e.target.value;
              if (v === CUSTOM_MODEL) {
                setCustomModel(true);
                // 回到上次手输的值；首次进入自定义时 customDraft 为空。
                setModel(customDraft);
              } else {
                if (customModel) setCustomDraft(model);
                setCustomModel(false);
                setModel(v);
              }
            }}
            className={inputCls}
          >
            <option value="">runtime 默认（留空）</option>
            {COMMON_MODELS.map((m) => (
              <option key={m} value={m}>{m}</option>
            ))}
            <option value={CUSTOM_MODEL}>自定义…</option>
          </select>
          {customModel && (
            <input
              value={model}
              onChange={(e) => {
                setModel(e.target.value);
                setCustomDraft(e.target.value);
              }}
              className={`${inputCls} mt-1.5`}
              placeholder="输入模型 ID，如 claude-opus-4-1"
              aria-label="自定义模型 ID"
            />
          )}
        </Field>

        <details className="text-xs">
          <summary className="cursor-pointer text-ink-3 hover:text-ink-2">高级设置</summary>
          <div className="mt-2 space-y-4">
            <Field
              label="同时执行的任务数"
              hint="同一个 agent 同时能跑几个任务。保持 1 = 一次只干一个活（推荐）；多个 goal 都派给它且想并行时再调大，注意模型 API 可能限流。不同 agent 之间天然并行，无需调这个。"
            >
              <input value={maxConcurrent} onChange={(e) => setMaxConcurrent(e.target.value)} className={inputCls} type="number" min="1" />
            </Field>

            <Field label="环境变量" hint="运行时注入给 agent 进程（覆盖同名平台变量）">
              <KVRows rows={envRows} onChange={setEnvRows} keyPlaceholder="KEY" valuePlaceholder="VALUE" />
            </Field>

            <Field label="额外 MCP 服务器" hint="给这个 agent 挂它自己的工具（浏览器、数据库等）；平台的 workspace 工具始终自带">
              <div className="space-y-2">
                {mcpRows.map((m, i) => {
                  // Display type: the wire uses "" for stdio — both "" and
                  // the legacy "stdio" string render as the stdio option.
                  const t = m.type === "http" || m.type === "sse" ? m.type : "stdio";
                  return (
                    <div key={i} className="border border-line rounded-lg p-2 space-y-2">
                      <div className="flex gap-2">
                        <input
                          value={m.name}
                          onChange={(e) => setMcpRows(mcpRows.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))}
                          className={`${inputCls} flex-1 min-w-0`}
                          placeholder="名称（如 browser）"
                        />
                        {/* inputCls 自带 w-full，与固定宽度类冲突——用容器定宽 */}
                        <div className="w-32 shrink-0">
                          <select
                            value={t}
                            onChange={(e) => setMcpRows(mcpRows.map((x, j) => (j === i ? { ...x, type: e.target.value } : x)))}
                            className={inputCls}
                          >
                            <option value="http">HTTP</option>
                            <option value="sse">SSE</option>
                            <option value="stdio">stdio 命令</option>
                          </select>
                        </div>
                        <button
                          type="button"
                          onClick={() => setMcpRows(mcpRows.filter((_, j) => j !== i))}
                          className="text-ink-3 hover:text-red-400 shrink-0"
                          title="删除"
                        >✕</button>
                      </div>
                      {t === "stdio" ? (
                        <div className="space-y-2">
                          <div className="flex gap-2">
                            <input
                              value={m.command ?? ""}
                              onChange={(e) => setMcpRows(mcpRows.map((x, j) => (j === i ? { ...x, command: e.target.value } : x)))}
                              className={`${inputCls} flex-1 min-w-0 font-mono`}
                              placeholder="命令（如 npx）"
                            />
                            <input
                              value={(m.args ?? []).join(" ")}
                              onChange={(e) => setMcpRows(mcpRows.map((x, j) => (j === i ? { ...x, args: e.target.value.split(/\s+/).filter(Boolean) } : x)))}
                              className={`${inputCls} flex-1 min-w-0 font-mono`}
                              placeholder="参数（空格分隔）"
                            />
                          </div>
                          <details>
                            <summary className="cursor-pointer text-ink-3 hover:text-ink-2 text-[11px]">环境变量</summary>
                            <div className="mt-1">
                              <KVRows
                                rows={m.env ?? []}
                                onChange={(rows) => setMcpRows(mcpRows.map((x, j) => (j === i ? { ...x, env: rows } : x)))}
                                keyPlaceholder="KEY"
                                valuePlaceholder="VALUE"
                              />
                            </div>
                          </details>
                        </div>
                      ) : (
                        <div className="space-y-2">
                          <input
                            value={m.url ?? ""}
                            onChange={(e) => setMcpRows(mcpRows.map((x, j) => (j === i ? { ...x, url: e.target.value } : x)))}
                            className={`${inputCls} w-full min-w-0 font-mono`}
                            placeholder="http://localhost:9222/mcp"
                          />
                          <details>
                            <summary className="cursor-pointer text-ink-3 hover:text-ink-2 text-[11px]">请求头</summary>
                            <div className="mt-1">
                              <KVRows
                                rows={m.headers ?? []}
                                onChange={(rows) => setMcpRows(mcpRows.map((x, j) => (j === i ? { ...x, headers: rows } : x)))}
                                keyPlaceholder="Header"
                                valuePlaceholder="值"
                              />
                            </div>
                          </details>
                        </div>
                      )}
                    </div>
                  );
                })}
                <button type="button" className="text-accent hover:text-accent" onClick={() => setMcpRows([...mcpRows, { name: "", type: "http" }])}>
                  + 添加服务器
                </button>
              </div>
            </Field>
          </div>
        </details>

        {(create.isError || update.isError) && (
          <p className="text-sm text-red-400">{String(create.error ?? update.error)}</p>
        )}
      </form>
    </Dialog>
  );
}
