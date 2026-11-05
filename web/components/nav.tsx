"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import {
  Target,
  MessageSquare,
  Plus,
  Bot,
  Users,
  Terminal,
  Monitor,
  Sparkles,
  Boxes,
  LayoutTemplate,
  Clock,
  ScrollText,
  Settings,
  MessagesSquare,
  LayoutDashboard,
} from "lucide-react";
import { cn } from "@/lib/utils";

// 导航按“我现在要做什么”排序，而不是按代码里的资源类型排：
//   工作台（每天用）→ 组织（谁在干活）→ 运行环境（在哪跑）→ 资源（用什么）。
// 「系统」不进主列表——日志和设置是低频、排障时才来的地方，压到左下角的角标上，
// 主列表就只留下日常要点的东西。
type NavItem = {
  href: string;
  label: string;
  icon: typeof Target;
  newTask?: boolean;
};

export const NAV_GROUPS: { title: string; items: NavItem[] }[] = [
  {
    title: "工作台",
    items: [
      { href: "/", label: "总览", icon: LayoutDashboard },
      { href: "/goals", label: "任务", icon: Target },
      { href: "/chat", label: "对话", icon: MessageSquare },
      { href: "/goals/new", label: "新建任务", icon: Plus, newTask: true },
      { href: "/schedules", label: "定时任务", icon: Clock },
    ],
  },
  {
    title: "组织",
    items: [
      { href: "/agents", label: "智能体", icon: Bot },
      { href: "/squads", label: "小队", icon: Users },
      { href: "/im", label: "IM", icon: MessagesSquare },
    ],
  },
  {
    title: "运行环境",
    items: [
      { href: "/runtimes", label: "运行时", icon: Terminal },
      { href: "/machines", label: "机器", icon: Monitor },
    ],
  },
  {
    title: "资源",
    items: [
      { href: "/domains", label: "项目仓库", icon: Boxes },
      { href: "/templates", label: "模板", icon: LayoutTemplate },
      { href: "/skills", label: "技能", icon: Sparkles },
    ],
  },
];

// 左下角角标：低频入口，做成小方块而不是和主列表一样的长条，
// 视觉上退到角落，但仍保持 44px 的可点面积与中文标签。
const SYSTEM_ITEMS: NavItem[] = [
  { href: "/logs", label: "日志", icon: ScrollText },
  { href: "/settings", label: "设置", icon: Settings },
];

export function Nav() {
  const pathname = usePathname();

  // 「新建任务」是 /goals 的子路径，但它是独立页面：同时亮会让人以为是同一件事。
  const isActive = (item: NavItem) => {
    // "/" 必须精确匹配：startsWith("/") 会让所有路由都亮。
    if (item.href === "/") return pathname === "/";
    if (item.newTask) return pathname === "/goals/new";
    if (item.href === "/goals") {
      return pathname === "/goals" || (pathname.startsWith("/goals/") && pathname !== "/goals/new");
    }
    return pathname.startsWith(item.href);
  };

  return (
    <nav className="sticky top-0 flex h-screen w-56 shrink-0 flex-col border-r border-line bg-surface-1/60 p-3 backdrop-blur">
      {/* Logo：等宽字体 + 绿色状态点，终端产品名而不是渐变字。点它回总览。 */}
      <Link
        href="/"
        title="返回总览"
        className="flex items-center gap-2 rounded-md px-3 pb-4 pt-2 transition-colors hover:bg-surface-2/60 focus-visible:outline-none focus-visible:ring-1 focus-visible:ring-accent"
      >
        <span className="h-2 w-2 shrink-0 rounded-full bg-accent" />
        <span className="font-mono text-sm font-semibold tracking-tight text-ink">agentwork</span>
      </Link>

      <div className="min-h-0 flex-1 overflow-y-auto">
      {NAV_GROUPS.map((group) => (
        <div key={group.title} className="mb-3">
          <div className="select-none px-3 pb-1 text-[11px] font-medium text-ink-3">{group.title}</div>
          <div className="flex flex-col gap-0.5">
            {group.items.map((item) => {
              const active = isActive(item);
              const Icon = item.icon;
              return (
                <Link
                  key={item.href}
                  href={item.href}
                  className={cn(
                    "group relative flex items-center gap-2.5 rounded-md px-3 py-1.5 text-sm transition-colors duration-150",
                    item.newTask && "mt-0.5 text-accent-dim hover:bg-accent/10 hover:text-accent",
                    // 停在这一页时也给个底：否则从侧栏看不出“我现在在新建任务”。
                    item.newTask && active && "bg-accent/10 text-accent",
                    !item.newTask && active && "bg-surface-3 text-ink",
                    !item.newTask && !active && "text-ink-2 hover:bg-surface-2 hover:text-ink"
                  )}
                >
                  {/* active 指示：左侧一道 2px 绿条，比整块染色更 IDE。 */}
                  {active && !item.newTask && (
                    <span className="absolute left-0 top-1.5 bottom-1.5 w-0.5 rounded-full bg-accent" />
                  )}
                  <Icon
                    className={cn(
                      "h-4 w-4 shrink-0",
                      item.newTask
                        ? "text-accent/80 group-hover:text-accent"
                        : active
                          ? "text-accent"
                          : "text-ink-3 group-hover:text-ink-2"
                    )}
                  />
                  {item.label}
                </Link>
              );
            })}
          </div>
        </div>
      ))}
      </div>

      {/* 左下角角标：日志 / 设置。钉在底部，不随中间分组滚动消失。 */}
      <div className="shrink-0 border-t border-line px-1 pt-2 mt-2">
        <div className="grid grid-cols-2 gap-1.5">
          {SYSTEM_ITEMS.map((item) => {
            const active = isActive(item);
            const Icon = item.icon;
            return (
              <Link
                key={item.href}
                href={item.href}
                title={item.label}
                className={cn(
                  "flex flex-col items-center gap-1 rounded-md py-2 text-[11px] transition-colors duration-150",
                  active
                    ? "bg-surface-3 text-ink"
                    : "text-ink-3 hover:bg-surface-2 hover:text-ink-2"
                )}
              >
                <Icon className={cn("h-4 w-4", active && "text-accent")} />
                {item.label}
              </Link>
            );
          })}
        </div>
      </div>
    </nav>
  );
}