"use client";

import { useEffect, useRef, useState } from "react";
import Link from "next/link";
import { Check, Info, MessageSquare, RotateCcw } from "lucide-react";
import { Button, PageBody, PageHeader, SectionCard } from "@/components/ui";
import { cn } from "@/lib/utils";

// 设置页只放“本机浏览器”的东西：外观与本机缓存。
// 平台级参数按用途归到各自的页面——飞书连接/摘要时间在「IM」，webhook secret 在
// 「项目仓库」。以前把这三样混在一页，用户看不出“平台设置”到底管什么。

// ── 外观：双主题切换 ──
// 持久化在 localStorage，layout 的首屏脚本在渲染前读回（无闪烁）。
const THEMES = [
  {
    id: "light",
    name: "Studio Paper",
    zh: "浅色 · 简约纸面",
    desc: "Codex 风格：暖白底、近黑正文、发丝级分隔线。",
  },
  {
    id: "dark",
    name: "Terminal Studio",
    zh: "深色 · 终端工作室",
    desc: "Dark IDE：蓝黑面板、等宽读数、run-green 状态点。",
  },
] as const;

type ThemeId = (typeof THEMES)[number]["id"];

function currentTheme(): ThemeId {
  if (typeof window === "undefined") return "light";
  const t = document.documentElement.dataset.theme;
  return t === "dark" ? "dark" : "light";
}

function applyTheme(t: ThemeId) {
  document.documentElement.dataset.theme = t;
  try {
    localStorage.setItem("agentwork-theme", t);
  } catch {
    /* 隐私模式等：主题仍对本页生效，只是不持久化 */
  }
}

// 主题卡：整卡可点，键盘可达（radiogroup + 方向键），点完焦点跟着走。
// 之前鼠标点上去没有指针光标、也没有焦点环，看起来像"没反应"。
function ThemeCard({
  theme,
  selected,
  onSelect,
  buttonRef,
  tabIndex,
  onKeyDown,
}: {
  theme: (typeof THEMES)[number];
  selected: boolean;
  onSelect: () => void;
  buttonRef: (el: HTMLButtonElement | null) => void;
  tabIndex: number;
  onKeyDown: (e: React.KeyboardEvent<HTMLButtonElement>) => void;
}) {
  const light = theme.id === "light";
  return (
    <button
      ref={buttonRef}
      type="button"
      role="radio"
      aria-checked={selected}
      tabIndex={tabIndex}
      onClick={onSelect}
      onKeyDown={onKeyDown}
      className={cn(
        "group cursor-pointer rounded-lg border p-3.5 text-left outline-none transition-colors duration-150",
        "focus-visible:ring-2 focus-visible:ring-accent/40",
        selected
          ? "border-accent bg-accent/[0.04]"
          : "border-line bg-surface-1 hover:border-line-strong hover:bg-surface-2"
      )}
    >
      {/* mini 预览：底色取自两套主题的实际值，一眼看出差别 */}
      <div
        className={`rounded-md border p-2.5 ${
          light ? "border-zinc-200 bg-white" : "border-[#2b3854] bg-[#131c2e]"
        }`}
      >
        <div className="mb-2 flex items-center gap-1.5">
          <span className="h-1.5 w-1.5 rounded-full bg-accent" />
          <span className={`h-1.5 w-10 rounded-full ${light ? "bg-zinc-300" : "bg-[#3d4f72]"}`} />
        </div>
        <div className={`rounded p-2 ${light ? "bg-zinc-50" : "bg-[#1a2539]"}`}>
          <div className="mb-1.5 flex items-center justify-between">
            <span className={`h-1.5 w-12 rounded-full ${light ? "bg-zinc-400" : "bg-[#8091b0]"}`} />
            <span className="h-1.5 w-1.5 rounded-full bg-green-400" />
          </div>
          <span className={`block h-1.5 w-8 rounded-full ${light ? "bg-zinc-300" : "bg-[#3d4f72]"}`} />
        </div>
      </div>

      <div className="mt-3 flex items-start justify-between gap-2">
        <div className="min-w-0">
          <p className="text-sm font-medium text-ink">{theme.zh}</p>
          <p className="mt-0.5 font-mono text-[10px] text-ink-3">{theme.name}</p>
        </div>
        {selected && (
          <span className="flex h-5 w-5 shrink-0 items-center justify-center rounded-full bg-accent text-accent-ink">
            <Check className="h-3 w-3" />
          </span>
        )}
      </div>
      <p className="mt-1.5 text-xs leading-relaxed text-ink-3">{theme.desc}</p>
    </button>
  );
}

export default function SettingsPage() {
  // 主题是纯客户端状态：初值从首屏脚本写好的 dataset 读回。
  const [theme, setTheme] = useState<ThemeId>("light");
  useEffect(() => {
    setTheme(currentTheme());
  }, []);

  const refs = useRef<(HTMLButtonElement | null)[]>([]);
  const pickTheme = (t: ThemeId, idx: number) => {
    applyTheme(t);
    setTheme(t);
    refs.current[idx]?.focus();
  };

  // 方向键在两张卡之间移动并立即选中（radiogroup 的标准交互）。
  const onCardKeyDown = (e: React.KeyboardEvent<HTMLButtonElement>, idx: number) => {
    const dir =
      e.key === "ArrowRight" || e.key === "ArrowDown"
        ? 1
        : e.key === "ArrowLeft" || e.key === "ArrowUp"
          ? -1
          : 0;
    if (!dir) return;
    e.preventDefault();
    const next = (idx + dir + THEMES.length) % THEMES.length;
    pickTheme(THEMES[next].id, next);
  };

  const [note, setNote] = useState("");
  const resetLocal = () => {
    try {
      localStorage.removeItem("agentwork-chat-history");
      localStorage.removeItem("agentwork-goals-view");
    } catch {}
    applyTheme("light");
    setTheme("light");
    setNote("已清空本机偏好与对话记录");
    window.setTimeout(() => setNote(""), 2500);
  };

  return (
    <PageBody>
      <PageHeader
        title="设置"
        desc="外观与本机数据。平台级参数按用途分散：推送在「IM」，webhook secret 在「项目仓库」。"
      />

      <div className="grid gap-5 lg:grid-cols-[minmax(0,1fr)_320px]">
        <div className="min-w-0 space-y-5">
          <SectionCard title="外观" desc="界面主题，点选即切换并记住">
            <div role="radiogroup" aria-label="界面主题" className="grid gap-3 p-4 sm:grid-cols-2">
              {THEMES.map((t, idx) => (
                <ThemeCard
                  key={t.id}
                  theme={t}
                  selected={theme === t.id}
                  onSelect={() => pickTheme(t.id, idx)}
                  buttonRef={(el) => {
                    refs.current[idx] = el;
                  }}
                  tabIndex={theme === t.id ? 0 : -1}
                  onKeyDown={(e) => onCardKeyDown(e, idx)}
                />
              ))}
            </div>
          </SectionCard>

          <SectionCard title="本机数据" desc="只影响这台机器的浏览器，不动服务端">
            <div className="flex flex-wrap items-center gap-3 p-4">
              <Button variant="outline" onClick={resetLocal} className="gap-1.5">
                <RotateCcw className="h-3.5 w-3.5" /> 清空偏好与对话记录
              </Button>
              <span className="text-[11px] text-ink-3">
                包括：主题、任务页视图、与管家的本地对话记录（CLI 侧的历史不受影响）。
              </span>
              {note && <p className="w-full text-xs text-emerald-400">{note}</p>}
            </div>
          </SectionCard>
        </div>

        <aside className="space-y-4">
          <SectionCard title="现在的样子">
            <dl className="divide-y divide-line">
              <div className="flex items-center justify-between px-4 py-2.5">
                <dt className="text-xs text-ink-3">界面主题</dt>
                <dd className="font-mono text-xs text-ink">
                  {theme === "light" ? "Studio Paper" : "Terminal Studio"}
                </dd>
              </div>
              <div className="flex items-center justify-between px-4 py-2.5">
                <dt className="text-xs text-ink-3">保存位置</dt>
                <dd className="font-mono text-[11px] text-ink-3">localStorage</dd>
              </div>
              <div className="flex items-center justify-between px-4 py-2.5">
                <dt className="text-xs text-ink-3">推送 / 签名</dt>
                <dd className="flex items-center gap-2 text-[11px] text-ink-3">
                  <Link href="/im" className="text-accent hover:underline">
                    IM
                  </Link>
                  <Link href="/domains" className="text-accent hover:underline">
                    项目仓库
                  </Link>
                </dd>
              </div>
            </dl>
          </SectionCard>

          <Link
            href="/im"
            className="flex items-start gap-2.5 rounded-lg border border-line bg-surface-0 px-4 py-3 transition-colors hover:border-accent/40"
          >
            <MessageSquare className="mt-0.5 h-3.5 w-3.5 shrink-0 text-ink-3" />
            <span className="min-w-0">
              <span className="block text-xs font-medium text-ink">飞书通知搬到了 IM</span>
              <span className="mt-0.5 block text-[11px] leading-relaxed text-ink-3">
                扫码连接、每日摘要时间都在那一页。
              </span>
            </span>
          </Link>

          <div className="flex gap-2.5 rounded-lg border border-line bg-surface-0 px-4 py-3">
            <Info className="mt-0.5 h-3.5 w-3.5 shrink-0 text-ink-3" />
            <p className="text-[11px] leading-relaxed text-ink-3">
              主题只影响这台机器的浏览器；推送与 webhook 属于平台级参数，对所有连接的 web 端生效。
            </p>
          </div>
        </aside>
      </div>
    </PageBody>
  );
}