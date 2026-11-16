import { create } from "zustand";
import { api, type QuickCommand } from "./api";
import { confirm, toast, useApp, type TermTab } from "./store";
import { focusTerm } from "../components/XTermView";

export interface Placeholder {
  name: string;
  def: string;
}

const phRe = /\{\{\s*([^{}:]+?)\s*(?::([^{}]*))?\}\}/g;

/** 命令中的占位符：{{名称}} 或 {{名称:默认值}}，同名只出现一次 */
export function placeholders(cmd: string): Placeholder[] {
  const out: Placeholder[] = [];
  for (const m of cmd.matchAll(phRe)) {
    const name = m[1].trim();
    const found = out.find((p) => p.name === name);
    if (!found) out.push({ name, def: m[2] ?? "" });
    else if (!found.def && m[2]) found.def = m[2];
  }
  return out;
}

export function fillCommand(cmd: string, values: Record<string, string>): string {
  return cmd.replace(phRe, (_all, name: string, def?: string) => values[name.trim()] ?? def ?? "");
}

const riskyRe = /\b(rm\s+-[a-z]*[rf]|reboot|shutdown|poweroff|halt|mkfs|dd\s+if=|kill(all)?\b|systemctl\s+(restart|stop|disable|mask)|service\s+\S+\s+(restart|stop)|docker\s+(rm|stop|kill|restart|system\s+prune)|drop\s+(table|database)|truncate|iptables\s+-F|ufw\s+(disable|reset))/i;

/** 命令看起来有风险（重启、删除、停止服务等）时，建议执行前确认 */
export function looksRisky(cmd: string): boolean {
  return riskyRe.test(cmd);
}

export type QuickTarget = "terminal" | "batch";

interface QuickState {
  commands: QuickCommand[];
  loaded: boolean;
  load: () => Promise<void>;
  save: (q: Partial<QuickCommand>) => Promise<QuickCommand | null>;
  remove: (q: QuickCommand) => Promise<boolean>;
  move: (id: string, delta: number) => Promise<void>;

  /** 正在编辑的命令（null 表示编辑器关闭；没有 id 表示新建） */
  editing: Partial<QuickCommand> | null;
  edit: (q?: Partial<QuickCommand>) => void;
  closeEdit: () => void;

  /** 等待填写占位符的命令 */
  filling: { cmd: QuickCommand; target: QuickTarget } | null;
  cancelFill: () => void;
  /** 批量执行弹窗 */
  batch: { command: string; title: string } | null;
  closeBatch: () => void;
  /** 同时发送到全部已连接的终端 */
  broadcast: boolean;
  setBroadcast: (v: boolean) => void;

  use: (cmd: QuickCommand, target?: QuickTarget) => Promise<void>;
  dispatch: (cmd: QuickCommand, text: string, target: QuickTarget) => Promise<void>;
}

function openTabs(): TermTab[] {
  return useApp.getState().terms.filter((t) => t.state === "open" && t.termId);
}

function activeTab(): TermTab | undefined {
  const { terms, activeTerm } = useApp.getState();
  const t = terms.find((x) => x.key === activeTerm) ?? terms[0];
  return t && t.state === "open" && t.termId ? t : undefined;
}

export const useQuick = create<QuickState>((set, get) => ({
  commands: [],
  loaded: false,
  load: async () => {
    try {
      set({ commands: (await api.ListQuickCommands()) ?? [], loaded: true });
    } catch (e) {
      console.error(e);
    }
  },
  save: async (q) => {
    try {
      const saved = await api.SaveQuickCommand({ id: "", name: "", command: "", group: "", description: "", autoEnter: true, confirm: false, createdAt: 0, updatedAt: 0, ...q });
      await get().load();
      return saved;
    } catch (e) {
      toast.error("保存失败", e);
      return null;
    }
  },
  remove: async (q) => {
    if (!(await confirm({ title: `删除快捷命令「${q.name}」？`, danger: true, confirmText: "删除" }))) return false;
    try {
      await api.DeleteQuickCommand(q.id);
      await get().load();
      return true;
    } catch (e) {
      toast.error("删除失败", e);
      return false;
    }
  },
  move: async (id, delta) => {
    const list = get().commands;
    const cur = list.find((c) => c.id === id);
    if (!cur) return;
    // 只在同一分组内移动
    const peers = list.filter((c) => c.group === cur.group);
    const i = peers.findIndex((c) => c.id === id);
    const j = i + delta;
    if (j < 0 || j >= peers.length) return;
    const ids = list.map((c) => c.id);
    const a = ids.indexOf(id);
    const b = ids.indexOf(peers[j].id);
    [ids[a], ids[b]] = [ids[b], ids[a]];
    try {
      await api.ReorderQuickCommands(ids);
      await get().load();
    } catch (e) {
      toast.error("排序失败", e);
    }
  },

  editing: null,
  edit: (q = {}) => set({ editing: q }),
  closeEdit: () => set({ editing: null }),

  filling: null,
  cancelFill: () => set({ filling: null }),
  batch: null,
  closeBatch: () => set({ batch: null }),
  broadcast: false,
  setBroadcast: (v) => set({ broadcast: v }),

  use: async (cmd, target) => {
    const t: QuickTarget = target ?? (activeTab() ? "terminal" : "batch");
    if (placeholders(cmd.command).length > 0) {
      set({ filling: { cmd, target: t } });
      return;
    }
    await get().dispatch(cmd, cmd.command, t);
  },

  dispatch: async (cmd, text, target) => {
    set({ filling: null });
    if (target === "batch") {
      set({ batch: { command: text, title: cmd.name } });
      return;
    }
    const active = activeTab();
    const tabs = get().broadcast ? openTabs() : active ? [active] : [];
    if (tabs.length === 0) {
      // 没有已连接的终端：改为选择服务器批量执行
      set({ batch: { command: text, title: cmd.name } });
      return;
    }
    if (cmd.confirm) {
      const where = tabs.map((x) => x.title).join("、");
      if (!(await confirm({ title: `执行「${cmd.name}」？`, message: `${text}\n\n将发送到：${where}`, confirmText: cmd.autoEnter ? "执行" : "发送" }))) return;
    }
    const data = text.replace(/\r?\n/g, "\r") + (cmd.autoEnter ? "\r" : "");
    let failed = 0;
    for (const tab of tabs) {
      try {
        await api.TerminalWrite(tab.termId, data);
      } catch {
        failed++;
      }
    }
    if (failed) toast.warn(`${failed} 个终端发送失败`, "连接可能已断开，请重新连接后再试");
    const app = useApp.getState();
    if (app.page !== "terminal") app.go("terminal");
    const focus = active ?? tabs[0];
    setTimeout(() => focusTerm(focus.key), 30);
  },
}));
