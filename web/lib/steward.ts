import { listAgents, listMachines, listRuntimes } from "./api";

// 管家连不上时，把「为什么」查到具体一层：机器离线 / 没绑运行时 / CLI 起不来。
// 只说「连接已关闭」等于把排查成本丢给用户，这里按 machine → runtime 逐层落。
export async function describeStewardFailure(stewardId: string | null): Promise<string> {
  try {
    const agents = await listAgents();
    const st = agents.find((a) => a.id === stewardId) ?? agents.find((a) => a.type === "steward");
    if (!st) return "还没有管家 agent。管家只会自动绑定 hwcloud CLI；可以在「对话」页或「智能体」页手动创建一个。";
    if (!st.runtime_id) return "管家还没绑定运行时——去「智能体」页给它挑一台机器上的运行时。";
    const rts = await listRuntimes();
    const rt = rts.find((r) => r.id === st.runtime_id);
    if (!rt) return "管家绑定的运行时已经不存在了——去「智能体」页重新选一个。";
    const machines = await listMachines();
    const m = machines.find((x) => x.id === rt.machine_id);
    if (!m) return "管家绑定的机器已经不在了——去「智能体」页重新选一个运行时。";
    if (m.status !== "connected") {
      return `管家挂在「${m.name}」上，但这台机器现在离线——在那台机器上重新跑 agentwork connect 再试。`;
    }
    return `管家在「${m.name}」上用 ${rt.name} 启动，但没连上——确认那台机器的 agentwork connect 还在跑、装好了这个 CLI，且没有别的会话正占着它。`;
  } catch {
    return "";
  }
}
