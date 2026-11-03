"use client";

import Link from "next/link";
import { ArrowUpRight, Bot, ListChecks, Server, ScrollText } from "lucide-react";
import { PageBody, PageHeader, SectionCard } from "@/components/ui";
import { StewardChat } from "@/components/steward-chat";

// 「对话」页：管家聊天占主栏，右侧一条常驻的“能做什么 / 去哪儿看结果”侧栏。
// 侧栏不是装饰——管家改完东西之后，人需要知道去哪验证（任务、机器、日志）。
const CAN_DO = [
  "创建任务：说清目标、执行者、验收标准",
  "查看与推进任务：谁卡在待审批、哪次执行失败",
  "改平台配置：项目仓库绑定、定时任务、技能",
  "排查问题：读 daemon 日志、看某次 run 的输出",
];

const QUICK_LINKS = [
  { href: "/goals", label: "任务看板", desc: "看管家刚建的任务落在哪一列", icon: ListChecks },
  { href: "/agents", label: "智能体", desc: "看谁在跑、跑的是什么模型", icon: Bot },
  { href: "/machines", label: "机器", desc: "看执行机器是否在线", icon: Server },
  { href: "/logs", label: "日志", desc: "管家每一步的原始记录", icon: ScrollText },
];

export default function ChatPage() {
  return (
    <PageBody full>
      <PageHeader
        title="对话"
        desc="用自然语言指挥管家：建任务、查状态、改配置，它通过 agentwork CLI 直接改这个平台。"
      />
      <div className="flex min-h-0 flex-1 gap-4">
        <StewardChat className="min-w-0 flex-1" />
        <aside className="hidden w-[300px] shrink-0 flex-col gap-4 overflow-y-auto lg:flex">
          <SectionCard title="管家能做什么" className="text-sm">
            <ul className="space-y-2 p-4">
              {CAN_DO.map((t) => (
                <li key={t} className="flex gap-2 text-xs leading-relaxed text-ink-2">
                  <span className="mt-1.5 h-1 w-1 shrink-0 rounded-full bg-accent" />
                  {t}
                </li>
              ))}
            </ul>
          </SectionCard>

          <SectionCard title="做完去哪看" desc="顺手验证结果">
            <div className="divide-y divide-line">
              {QUICK_LINKS.map((l) => {
                const Icon = l.icon;
                return (
                  <Link
                    key={l.href}
                    href={l.href}
                    className="group flex items-start gap-2.5 px-4 py-2.5 transition-colors hover:bg-surface-2"
                  >
                    <Icon className="mt-0.5 h-4 w-4 shrink-0 text-ink-3 group-hover:text-accent" />
                    <span className="min-w-0">
                      <span className="flex items-center gap-1 text-xs font-medium text-ink">
                        {l.label}
                        <ArrowUpRight className="h-3 w-3 text-ink-3 opacity-0 transition-opacity group-hover:opacity-100" />
                      </span>
                      <span className="mt-0.5 block text-[11px] leading-snug text-ink-3">{l.desc}</span>
                    </span>
                  </Link>
                );
              })}
            </div>
          </SectionCard>

          <div className="rounded-lg border border-line bg-surface-0 px-4 py-3 text-[11px] leading-relaxed text-ink-3">
            会话记录存在本机浏览器（最近 100 条），CLI 侧的会话也会保留；换机器打开就是新的一段对话。
          </div>
        </aside>
      </div>
    </PageBody>
  );
}