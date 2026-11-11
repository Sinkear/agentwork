"use client";

import { useState } from "react";
import {
  Bell,
  CheckCircle2,
  GitMerge,
  Info,
  MessageSquare,
  ScanLine,
  Send,
  ShieldAlert,
} from "lucide-react";
import {
  useConnectFeishu,
  useDisconnectFeishu,
  useImStatus,
  usePlatformSettings,
  useSavePlatformSettings,
} from "@/lib/queries";
import { Button, PageBody, PageHeader, SectionCard, inputCls } from "@/components/ui";
import { cn } from "@/lib/utils";

// 飞书连接状态 → 文案与圆点色。与设置页原来的 themeLabel 一致，只是语义搬到这里。
function imStateOf(status?: string): { text: string; dot: string } {
  switch (status) {
    case "connected":
      return { text: "已连接", dot: "bg-accent" };
    case "waiting_qr":
      return { text: "等待扫码", dot: "bg-amber-400" };
    case "waiting_message":
      return { text: "等待消息", dot: "bg-amber-400" };
    case "reconnecting":
      return { text: "重连中", dot: "bg-amber-400" };
    case "failed":
      return { text: "连接失败", dot: "bg-red-400" };
    default:
      return { text: "未连接", dot: "bg-zinc-400" };
  }
}

export default function ImPage() {
  const { data: im } = useImStatus();
  const imState = imStateOf(im?.status);

  return (
    <PageBody>
      <PageHeader
        title="IM"
        desc="把任务进展推到飞书：任务完成、卡点待审、合入结果都从这里出。"
        action={
          <span className="inline-flex items-center gap-1.5 rounded-md border border-line bg-surface-1 px-2.5 py-1 text-xs text-ink-2">
            <span className={cn("h-1.5 w-1.5 rounded-full", imState.dot)} />
            {imState.text}
          </span>
        }
      />

      <div className="grid gap-6 lg:grid-cols-[minmax(0,1fr)_300px]">
        <div className="min-w-0 space-y-6">
          <FeishuConnectSection />
          <DailyDigestSection />
        </div>

        <aside className="space-y-4">
          <SectionCard title="会推送什么">
            <ul className="divide-y divide-line">
              <PushRow
                icon={<CheckCircle2 className="h-3.5 w-3.5" />}
                label="任务完成"
                desc="跑完并验收通过时推一条，带变更摘要与耗时。"
              />
              <PushRow
                icon={<ShieldAlert className="h-3.5 w-3.5" />}
                label="卡点待审"
                desc="需要人拍板时立刻推送，避免卡在没人看的地方。"
              />
              <PushRow
                icon={<GitMerge className="h-3.5 w-3.5" />}
                label="合入结果"
                desc="合入成功、冲突或被拒都推，附分支与提交号。"
              />
              <PushRow
                icon={<Bell className="h-3.5 w-3.5" />}
                label="每日摘要"
                desc="每天固定时间把当天进展汇总成一条消息。"
              />
            </ul>
          </SectionCard>

          <SectionCard title="连接方式">
            <div className="p-4">
              <ol className="space-y-3">
                <ConnectStep
                  n="1"
                  icon={<ScanLine className="h-3.5 w-3.5" />}
                  label="扫码"
                  desc="用飞书 App 扫二维码并完成授权。"
                />
                <ConnectStep
                  n="2"
                  icon={<MessageSquare className="h-3.5 w-3.5" />}
                  label="自动建应用"
                  desc="授权后平台自动创建应用并建立长连接。"
                />
                <ConnectStep
                  n="3"
                  icon={<Send className="h-3.5 w-3.5" />}
                  label="捕获推送目标"
                  desc="给机器人发任意一条消息（如 hi），推送目标即被捕获。"
                />
              </ol>
            </div>
            <div className="flex gap-2.5 border-t border-line px-4 py-3">
              <Info className="mt-0.5 h-3.5 w-3.5 shrink-0 text-ink-3" />
              <p className="text-[11px] leading-relaxed text-ink-3">
                未连接时平台仍可用，只是不推送。
              </p>
            </div>
          </SectionCard>
        </aside>
      </div>
    </PageBody>
  );
}

function PushRow({
  icon,
  label,
  desc,
}: {
  icon: React.ReactNode;
  label: string;
  desc: string;
}) {
  return (
    <li className="flex items-start gap-2.5 px-4 py-2.5">
      <span className="mt-0.5 shrink-0 text-ink-3">{icon}</span>
      <div className="min-w-0">
        <p className="text-xs font-medium text-ink">{label}</p>
        <p className="mt-0.5 text-[11px] leading-relaxed text-ink-3">{desc}</p>
      </div>
    </li>
  );
}

function ConnectStep({
  n,
  icon,
  label,
  desc,
}: {
  n: string;
  icon: React.ReactNode;
  label: string;
  desc: string;
}) {
  return (
    <li className="flex items-start gap-2.5">
      <span className="mt-0.5 flex h-5 w-5 shrink-0 items-center justify-center rounded-md border border-line bg-surface-2 font-mono text-[10px] text-ink-3">
        {n}
      </span>
      <div className="min-w-0">
        <p className="flex items-center gap-1.5 text-xs font-medium text-ink">
          <span className="text-ink-3">{icon}</span>
          {label}
        </p>
        <p className="mt-0.5 text-[11px] leading-relaxed text-ink-3">{desc}</p>
      </div>
    </li>
  );
}

// 飞书连接：状态由服务端推进（扫码 → 等待消息 → 已连接），每 3s 轮询一次。
function FeishuConnectSection() {
  const { data: im, isLoading } = useImStatus();
  const connect = useConnectFeishu();
  const disconnect = useDisconnectFeishu();
  const status = im?.status ?? "idle";

  return (
    <SectionCard title="飞书" desc="扫码连接后走长连接推送，不需要开公网回调">
      <div className="p-4">
        {isLoading ? (
          <p className="text-sm text-ink-3">加载中…</p>
        ) : status === "connected" ? (
          <div className="space-y-3 rounded-md border border-emerald-500/30 bg-emerald-500/10 p-4">
            <div className="flex items-center gap-2">
              <CheckCircle2 className="h-4 w-4 text-emerald-300" />
              <p className="font-medium text-emerald-300">已连接飞书</p>
            </div>
            <dl className="space-y-1">
              <div className="flex items-baseline justify-between gap-3">
                <dt className="text-xs text-emerald-300">应用</dt>
                <dd className="font-mono text-xs text-emerald-300">
                  {im?.app_id ? `${im.app_id.slice(0, 8)}…` : "未捕获"}
                </dd>
              </div>
              <div className="flex items-baseline justify-between gap-3">
                <dt className="text-xs text-emerald-300">推送目标</dt>
                <dd className="truncate font-mono text-xs text-emerald-300">
                  {im?.receive_id?.slice(0, 12) || "未捕获"}
                </dd>
              </div>
            </dl>
            <div className="pt-1">
              <Button
                type="button"
                variant="danger"
                onClick={() => disconnect.mutate()}
                disabled={disconnect.isPending}
              >
                {disconnect.isPending ? "断开中…" : "断开连接"}
              </Button>
            </div>
            {/* 断开失败必须显示——静默等于「点了没反应」。 */}
            {disconnect.isError && (
              <p className="text-xs text-red-400">断开失败：{String(disconnect.error)}</p>
            )}
          </div>
        ) : status === "reconnecting" ? (
          <div className="space-y-2 rounded-md border border-amber-500/30 bg-amber-500/10 p-4">
            <p className="font-medium text-amber-300">连接中断，正在自动重连…</p>
            <p className="text-xs text-amber-300">网络恢复后会自动恢复，无需操作。</p>
          </div>
        ) : status === "waiting_qr" || status === "waiting_message" ? (
          <div className="space-y-3 rounded-md border border-blue-500/30 bg-blue-500/10 p-4">
            <p className="font-medium text-blue-300">
              {status === "waiting_qr"
                ? "用飞书扫码完成连接"
                : "已扫码——给机器人发条消息完成连接"}
            </p>
            {im?.qr?.img_base64 ? (
              // eslint-disable-next-line @next/next/no-img-element
              <img
                src={`data:image/png;base64,${im.qr.img_base64}`}
                alt="飞书连接二维码"
                className="h-56 w-56 rounded bg-surface-1 p-2"
              />
            ) : (
              <p className="text-sm text-blue-400">二维码生成中…</p>
            )}
            <ol className="list-inside list-decimal space-y-1 text-xs text-blue-300">
              <li>用飞书 App 扫码（或打开链接 {im?.qr?.url ? "在飞书里打开" : "…"}）</li>
              <li>授权后会自动创建应用并建立长连接</li>
              <li>给机器人发任意一条消息（如“hi”）——推送目标即被捕获</li>
            </ol>
            {status === "waiting_message" && (
              <p className="text-sm font-medium text-blue-300">等待你在飞书里给机器人发消息…</p>
            )}
          </div>
        ) : status === "failed" ? (
          <div className="space-y-2 rounded-md border border-red-500/30 bg-red-500/10 p-4">
            <p className="font-medium text-red-300">连接失败</p>
            <p className="break-all text-xs text-red-300">{im?.error || "未知错误"}</p>
            <div className="pt-1">
              <Button
                type="button"
                onClick={() => connect.mutate()}
                disabled={connect.isPending}
              >
                {connect.isPending ? "重试中…" : "重新连接"}
              </Button>
            </div>
            {connect.isError && <p className="text-sm text-red-400">{String(connect.error)}</p>}
          </div>
        ) : (
          <div className="space-y-3 rounded-md border border-line bg-surface-0 p-4">
            <p className="text-sm text-ink-2">
              尚未连接飞书。连接后，任务完成、卡点待审、合入结果都会推到这里。
            </p>
            <Button type="button" onClick={() => connect.mutate()} disabled={connect.isPending}>
              {connect.isPending ? "连接中…" : "连接飞书"}
            </Button>
            {connect.isError && <p className="text-sm text-red-400">{String(connect.error)}</p>}
          </div>
        )}
      </div>
    </SectionCard>
  );
}

// 每日摘要时间。平台设置是整体覆盖：保存时必须回传 intake_agent 与 webhook_secret，
// 否则会把已有的平台 secret 清掉（webhook secret 的录入入口在项目仓库页）。
function DailyDigestSection() {
  const { data: settings, isLoading } = usePlatformSettings();
  const save = useSavePlatformSettings();
  const [digestTime, setDigestTime] = useState<string | null>(null);

  const value = digestTime ?? settings?.digest_time ?? "";

  const saveDigest = () => {
    save.mutate({
      intake_agent: "",
      digest_time: value,
      webhook_secret: settings?.webhook_secret ?? "",
    });
  };

  return (
    <SectionCard title="每日摘要推送" desc="到点把当天任务进展汇总成一条飞书消息">
      <div className="p-4">
        {isLoading ? (
          <p className="text-sm text-ink-3">加载中…</p>
        ) : (
          <form
            onSubmit={(e) => {
              e.preventDefault();
              saveDigest();
            }}
            className="space-y-3"
          >
            <label className="block">
              <span className="text-xs font-medium text-ink-2">推送时间（HH:MM，本地）</span>
              <input
                type="text"
                inputMode="numeric"
                className={`${inputCls} mt-1 max-w-[140px] font-mono`}
                placeholder="09:00"
                value={value}
                onChange={(e) => setDigestTime(e.target.value)}
              />
              <span className="mt-1 block text-[11px] text-ink-3">
                留空则按默认 09:00 推送；只填时间，不填日期。
              </span>
            </label>

            <div className="flex items-center gap-3">
              <Button
                type="button"
                onClick={saveDigest}
                disabled={save.isPending || digestTime === null}
              >
                {save.isPending ? "保存中…" : "保存"}
              </Button>
              {save.isError && <p className="text-sm text-red-400">{String(save.error)}</p>}
              {save.isSuccess && <p className="text-sm text-emerald-400">已保存</p>}
            </div>
          </form>
        )}
      </div>
    </SectionCard>
  );
}