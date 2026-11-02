"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { Target, Bot, Users, Terminal, Clock, Boxes, Settings, ScrollText, Monitor, Sparkles, LayoutTemplate } from "lucide-react";
import { cn } from "@/lib/utils";

// 导航分组：同一层级的实体归到一组，扫读时先看组名再看条目。
// 组顺序即产品的心智模型——编排什么 → 跑在哪里 → 用什么资源 → 系统层。
const NAV_GROUPS: { title: string; items: { href: string; label: string; icon: typeof Target }[] }[] = [
  {
    title: "编排",
    items: [
      { href: "/goals", label: "Goal", icon: Target },
      { href: "/agents", label: "Agent", icon: Bot },
      { href: "/squads", label: "Squad", icon: Users },
    ],
  },
  {
    title: "基础设施",
    items: [
      { href: "/runtimes", label: "Runtime", icon: Terminal },
      { href: "/machines", label: "Machine", icon: Monitor },
      { href: "/skills", label: "Skills", icon: Sparkles },
    ],
  },
  {
    title: "资源",
    items: [
      { href: "/domains", label: "Project", icon: Boxes },
      { href: "/templates", label: "Templates", icon: LayoutTemplate },
      { href: "/schedules", label: "Schedule", icon: Clock },
    ],
  },
  {
    title: "系统",
    items: [
      { href: "/logs", label: "Logs", icon: ScrollText },
      { href: "/settings", label: "Settings", icon: Settings },
    ],
  },
];

export function Nav() {
  const pathname = usePathname();
  return (
    <nav className="w-56 shrink-0 border-r border-line bg-surface-1/60 backdrop-blur flex flex-col p-3 overflow-y-auto">
      {/* Logo：等宽字体 + 绿色状态点，终端产品名而不是渐变字。 */}
      <div className="flex items-center gap-2 px-3 pt-2 pb-4">
        <span className="h-2 w-2 rounded-full bg-accent shrink-0" />
        <span className="font-mono text-sm font-semibold tracking-tight text-ink">agentwork</span>
      </div>
      {NAV_GROUPS.map((group) => (
        <div key={group.title} className="mb-3">
          <div className="px-3 pb-1 font-mono text-[10px] uppercase tracking-[0.12em] text-ink-3 select-none">
            {group.title}
          </div>
          <div className="flex flex-col gap-0.5">
            {group.items.map((item) => {
              const active = pathname.startsWith(item.href);
              const Icon = item.icon;
              return (
                <Link
                  key={item.href}
                  href={item.href}
                  className={cn(
                    "group relative px-3 py-1.5 rounded-md text-sm transition-colors duration-150 flex items-center gap-2.5",
                    active
                      ? "bg-surface-3 text-ink"
                      : "text-ink-2 hover:bg-surface-2 hover:text-ink"
                  )}
                >
                  {/* active 指示：左侧一道 2px 绿条，比整块染色更 IDE。 */}
                  {active && <span className="absolute left-0 top-1.5 bottom-1.5 w-0.5 rounded-full bg-accent" />}
                  <Icon className={cn("h-4 w-4 shrink-0", active ? "text-accent" : "text-ink-3 group-hover:text-ink-2")} />
                  {item.label}
                </Link>
              );
            })}
          </div>
        </div>
      ))}
    </nav>
  );
}
