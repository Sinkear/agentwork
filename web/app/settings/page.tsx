"use client";

import { useEffect, useState } from "react";
import {
  useImStatus,
  useConnectFeishu,
  useDisconnectFeishu,
  usePlatformSettings,
  useSavePlatformSettings,
} from "@/lib/queries";
import { Button, PageHeader } from "@/components/ui";

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

// 主题预览缩略：一块 mini 版面（导航条 + 卡片 + 状态点），点整卡切换。
function ThemeCard({ theme, selected, onSelect }: { theme: (typeof THEMES)[number]; selected: boolean; onSelect: () => void }) {
  const light = theme.id === "light";
  return (
    <button
      onClick={onSelect}
      aria-pressed={selected}
      className={`text-left rounded-xl border p-4 transition-colors duration-150 ${
        selected
          ? "border-accent ring-2 ring-accent/25"
          : "border-line hover:border-line-strong"
      }`}
    >
      {/* mini 预览：底色随主题实际值走（bg-surface-* 本身就是主题变量） */}
      <div className={`rounded-lg border p-2.5 ${light ? "bg-white border-zinc-200" : "bg-[#131c2e] border-[#2b3854]"}`}>
        <div className="flex items-center gap-1.5 mb-2">
          <span className="h-1.5 w-1.5 rounded-full bg-accent" />
          <span className={`h-1.5 w-10 rounded-full ${light ? "bg-zinc-300" : "bg-[#3d4f72]"}`} />
        </div>
        <div className={`rounded-md p-2 ${light ? "bg-zinc-50" : "bg-[#1a2539]"}`}>
          <div className="flex items-center justify-between mb-1.5">
            <span className={`h-1.5 w-12 rounded-full ${light ? "bg-zinc-400" : "bg-[#8091b0]"}`} />
            <span className="h-1.5 w-1.5 rounded-full bg-green-400" />
          </div>
          <span className={`block h-1.5 w-8 rounded-full ${light ? "bg-zinc-300" : "bg-[#3d4f72]"}`} />
        </div>
      </div>
      <p className="mt-3 text-sm font-medium text-ink">
        {theme.zh}
        <span className="ml-1.5 font-mono text-[10px] text-ink-3">{theme.name}</span>
      </p>
      <p className="mt-0.5 text-xs text-ink-3 leading-relaxed">{theme.desc}</p>
      {selected && <p className="mt-1.5 text-xs font-medium text-accent">当前使用</p>}
    </button>
  );
}

// Settings: the IM connect module + platform-wide settings (daily digest,
// webhook secret). The intake parser is the system steward (auto-seeded).
// The owner connects Feishu by scanning a QR code (SDK one-click app
// registration) — after that, tasks can be created from Feishu or the Web
// assistant dialog, and approvals answered from the notification cards.
export default function SettingsPage() {
  const { data: im, isLoading } = useImStatus();
  const connect = useConnectFeishu();
  const disconnect = useDisconnectFeishu();

  // 主题是纯客户端状态：初值从首屏脚本写好的 dataset 读回，点选后同步更新，
  // 卡片高亮才会立刻跟着走（不依赖任何一次查询刷新）。
  const [theme, setTheme] = useState<ThemeId>("light");
  useEffect(() => {
    setTheme(currentTheme());
  }, []);
  const pickTheme = (t: ThemeId) => {
    applyTheme(t);
    setTheme(t);
  };

  const status = im?.status ?? "idle";

  return (
    <div className="p-8">
      <PageHeader title="Settings" desc="通知、平台参数与外观。"/>

      <div className="max-w-xl space-y-8">
        {/* 外观 */}
        <section>
          <h2 className="text-lg font-medium mb-2">外观</h2>
          <p className="text-sm text-ink-3 mb-4">选择界面主题，立即生效并记住。</p>
          <div className="grid grid-cols-2 gap-3">
            {THEMES.map((t) => (
              <ThemeCard
                key={t.id}
                theme={t}
                selected={theme === t.id}
                onSelect={() => pickTheme(t.id)}
              />
            ))}
          </div>
        </section>

        {/* 飞书通知 */}
        <section>
          <h2 className="text-lg font-medium mb-2">飞书通知（IM 连接）</h2>
        <p className="text-sm text-ink-3 mb-4">
          里程碑事件（待审批 / 完成 / 合入 / 失败）会推送到飞书。扫码连接，之后你只需要在审批卡点出现。
        </p>

        {isLoading ? (
          <p className="text-ink-3">加载中…</p>
        ) : status === "connected" ? (
          <div className="rounded-lg border border-emerald-500/30 bg-emerald-500/10 p-4 space-y-2">
            <p className="text-emerald-300 font-medium">已连接飞书</p>
            <p className="text-xs text-emerald-300">
              应用 {im?.app_id ? `（${im.app_id.slice(0, 8)}…）` : ""} · 推送目标 {im?.receive_id?.slice(0, 12) || "未捕获"}
            </p>
            <div className="pt-2">
              <Button variant="danger" onClick={() => disconnect.mutate()} disabled={disconnect.isPending}>
                {disconnect.isPending ? "断开中…" : "断开连接"}
              </Button>
            </div>
            {/* 断开失败必须显示——静默等于「点了没反应」。 */}
            {disconnect.isError && (
              <p className="text-xs text-red-400">断开失败：{String(disconnect.error)}</p>
            )}
          </div>
        ) : status === "reconnecting" ? (
          <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 p-4 space-y-2">
            <p className="text-amber-300 font-medium">连接中断，正在自动重连…</p>
            <p className="text-xs text-amber-300">网络恢复后会自动恢复，无需操作。</p>
          </div>
        ) : status === "waiting_qr" || status === "waiting_message" ? (
          <div className="rounded-lg border border-blue-500/30 bg-blue-500/10 p-4 space-y-3">
            <p className="text-blue-300 font-medium">
              {status === "waiting_qr" ? "用飞书扫码完成连接" : "已扫码——给机器人发条消息完成连接"}
            </p>
            {im?.qr?.img_base64 ? (
              <img
                src={`data:image/png;base64,${im.qr.img_base64}`}
                alt="飞书连接二维码"
                className="w-56 h-56 bg-surface-1 p-2 rounded"
              />
            ) : (
              <p className="text-sm text-blue-400">二维码生成中…</p>
            )}
            <ol className="text-xs text-blue-300 space-y-1 list-decimal list-inside">
              <li>用飞书 App 扫码（或打开链接 {im?.qr?.url ? "在飞书里打开" : "…"}）</li>
              <li>授权后会自动创建应用并建立长连接</li>
              <li>给机器人发任意一条消息（如"hi"）——推送目标即被捕获</li>
            </ol>
            {status === "waiting_message" && (
              <p className="text-sm text-blue-300 font-medium">等待你在飞书里给机器人发消息…</p>
            )}
          </div>
        ) : status === "failed" ? (
          <div className="rounded-lg border border-red-500/30 bg-red-500/10 p-4 space-y-2">
            <p className="text-red-300 font-medium">连接失败</p>
            <p className="text-xs text-red-300 break-all">{im?.error || "未知错误"}</p>
            <Button onClick={() => connect.mutate()} disabled={connect.isPending}>
              {connect.isPending ? "重试中…" : "重新连接"}
            </Button>
            {/* 重试失败同样要显示——此前 connect.isError 只在 idle 分支渲染。 */}
            {connect.isError && <p className="text-sm text-red-400">{String(connect.error)}</p>}
          </div>
        ) : (
          <div className="rounded-lg border border-line p-4 space-y-3">
            <p className="text-sm text-ink-2">尚未连接飞书。连接后，任务完成、卡点待审、合入结果都会推到这里。</p>
            <Button onClick={() => connect.mutate()} disabled={connect.isPending}>
              {connect.isPending ? "连接中…" : "连接飞书"}
            </Button>
            {connect.isError && <p className="text-sm text-red-400">{String(connect.error)}</p>}
          </div>
        )}
        </section>
      </div>

      <PlatformSettingsSection />
    </div>
  );
}

// PlatformSettingsSection configures the platform-wide settings: the daily
// digest time and the webhook secret. The intake parser agent is the
// system-internal steward (auto-seeded, no configuration needed).
function PlatformSettingsSection() {
  const { data: settings, isLoading } = usePlatformSettings();
  const save = useSavePlatformSettings();
  const [digestTime, setDigestTime] = useState<string | null>(null);
  const [webhookSecret, setWebhookSecret] = useState<string | null>(null);

  const current = (k: string, fallback: string) =>
    k === "digest_time"
      ? (digestTime ?? settings?.digest_time ?? "")
      : (webhookSecret ?? settings?.webhook_secret ?? "");

  return (
    <div className="max-w-xl mt-10">
      <h2 className="text-lg font-medium mb-1">平台设置</h2>
      <p className="text-sm text-ink-3 mb-4">
        飞书和 Web 助手的自然语言指令由系统管家 agent 自动解析，无需手动配置。
      </p>

      {isLoading ? (
        <p className="text-ink-3">加载中…</p>
      ) : (
        <div className="space-y-4">
          <div>
            <label className="block text-sm text-ink-2 mb-1">每日摘要时间（HH:MM，本地）</label>
            <input
              type="text"
              className="w-full rounded border border-line-strong px-2 py-1.5 text-sm"
              placeholder="09:00"
              value={current("digest_time", "")}
              onChange={(e) => setDigestTime(e.target.value)}
            />
          </div>
          <div>
            <label className="block text-sm text-ink-2 mb-1">平台 webhook secret（可选，M4-B，GitHub/GitCode 共用）</label>
            <input
              type="password"
              className="w-full rounded border border-line-strong px-2 py-1.5 text-sm"
              placeholder="配了才有实时 issue 触发（不配则 5 分钟轮询兜底）"
              value={current("webhook_secret", "")}
              onChange={(e) => setWebhookSecret(e.target.value)}
            />
          </div>
          <Button
            onClick={() =>
              save.mutate({
                intake_agent: "",
                digest_time: current("digest_time", ""),
                webhook_secret: current("webhook_secret", ""),
              })
            }
            disabled={save.isPending || (!digestTime && !webhookSecret)}
          >
            {save.isPending ? "保存中…" : "保存"}
          </Button>
          {save.isError && <p className="text-sm text-red-400">{String(save.error)}</p>}
          {save.isSuccess && <p className="text-sm text-emerald-400">已保存</p>}
        </div>
      )}
    </div>
  );
}
