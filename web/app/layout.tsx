import type { Metadata } from "next";
import { IBM_Plex_Sans, JetBrains_Mono } from "next/font/google";
import "./globals.css";
import { Providers } from "@/components/providers";
import { Nav } from "@/components/nav";
import { ErrorBoundary } from "@/components/error-boundary";
import { ChatAssistant } from "@/components/chat-assistant";

const plexSans = IBM_Plex_Sans({
  variable: "--font-plex-sans",
  subsets: ["latin"],
  weight: ["400", "500", "600", "700"],
});

const jetbrainsMono = JetBrains_Mono({
  variable: "--font-jetbrains-mono",
  subsets: ["latin"],
});

export const metadata: Metadata = {
  title: "agentwork",
  description: "Task-driven multi-runtime agent management",
};

// 首屏防闪烁：在 body 渲染前读回用户主题（localStorage）。
// 默认浅色 Studio Paper；深色 Terminal Studio 为可选项。
const THEME_BOOT = `(function(){try{var t=localStorage.getItem('agentwork-theme');if(t!=='dark'&&t!=='light')t='light';document.documentElement.dataset.theme=t;}catch(e){document.documentElement.dataset.theme='light';}})()`;

export default function RootLayout({
  children,
}: Readonly<{
  children: React.ReactNode;
}>) {
  return (
    <html
      lang="en"
      className={`${plexSans.variable} ${jetbrainsMono.variable} h-full antialiased`}
      data-theme="light"
      suppressHydrationWarning
    >
      <head>
        <script dangerouslySetInnerHTML={{ __html: THEME_BOOT }} />
      </head>
      <body className="min-h-full flex flex-row bg-surface-0">
        <Providers>
          <Nav />
          <main className="flex-1 overflow-auto">
            <ErrorBoundary>{children}</ErrorBoundary>
          </main>
          <ChatAssistant />
        </Providers>
      </body>
    </html>
  );
}
