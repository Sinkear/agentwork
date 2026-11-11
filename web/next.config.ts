import type { NextConfig } from "next";

// 浏览器只打同源的 Next 服务：/backend/* 由 Next 服务端转发给本机 daemon
// （默认 47813，可用 AGENTWORK_DAEMON_PORT 在构建时覆盖），反代 Web 端口
// 出去时用户无需直连 daemon（CORS 也随之无关）。/ws 也走同源：Next 的
// rewrite 代理（dev/多数 prod 版本）转发 WebSocket upgrade；若部署形态
// 不支持，则在反代层加一条 /ws → daemon/ws（需 Upgrade/Connection 透传）。
const daemonPort = process.env.AGENTWORK_DAEMON_PORT || "47813";
const daemonBase = `http://localhost:${daemonPort}`;

const nextConfig: NextConfig = {
  // 开发时允许用 127.0.0.1 访问：Next 16 默认禁止跨源开发资源请求，
  // 否则页面能出 SSR 骨架但客户端 JS 全被拦，表现为永远「加载中…」。
  allowedDevOrigins: ["127.0.0.1", "localhost"],
  output: "standalone",
  // 锁定 standalone 产物根目录：不设的话 Turbopack 会按仓库路径嵌套
  // （.next/standalone/<user>/<path>/web/...），打包脚本没法用固定路径取产物。
  turbopack: { root: __dirname },
  async rewrites() {
    return [
      { source: "/ws", destination: `${daemonBase}/ws` },
      { source: "/backend/:path*", destination: `${daemonBase}/:path*` },
      // webhook：宿主平台（GitHub/GitCode）直接 POST 到公开域名时，把
      // /webhooks/* 透传给 daemon——这样仓库里填的 URL 就是站点地址，
      // 不必暴露 daemon 端口。反代层若已单独转发，此条无害（同源直达）。
      { source: "/webhooks/:path*", destination: `${daemonBase}/webhooks/:path*` },
    ];
  },
};

export default nextConfig;
