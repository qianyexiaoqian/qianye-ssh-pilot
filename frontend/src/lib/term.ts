import { useEffect, useState } from "react";
import { create } from "zustand";
import { api } from "./api";
import { toast, useApp } from "./store";

export type TermSide = "ai" | "quick" | null;

interface TermUIState {
  /** 终端右侧打开的面板 */
  side: TermSide;
  setSide: (s: TermSide) => void;
  toggleSide: (s: Exclude<TermSide, null>) => void;
  /** 准备随下一条消息发给 AI 的终端内容 */
  attach: string;
  setAttach: (text: string) => void;
  /** 打开「AI 运维」面板并附带一段终端内容 */
  askAI: (text: string) => void;
}

const initialSide = (): TermSide => {
  const v = localStorage.getItem("term-side");
  if (v === "ai" || v === "quick") return v;
  if (v === "none") return null;
  // 旧版本只有快捷命令面板的开关
  return localStorage.getItem("quick-panel") === "closed" ? null : "quick";
};

export const useTermUI = create<TermUIState>((set, get) => ({
  side: initialSide(),
  setSide: (side) => {
    localStorage.setItem("term-side", side ?? "none");
    set({ side });
  },
  toggleSide: (s) => get().setSide(get().side === s ? null : s),
  attach: "",
  setAttach: (attach) => set({ attach }),
  askAI: (text) => {
    get().setSide("ai");
    set({ attach: text.length > 8000 ? text.slice(-8000) : text });
  },
}));

/** 当前打开了终端（已连接或正在连接）的服务器 ID，去重后排序 */
export function useOpenServerIds(): string[] {
  const sig = useApp((s) => [...new Set(s.terms.filter((t) => t.state !== "closed").map((t) => t.serverId))].sort().join(","));
  const [ids, setIds] = useState<string[]>(() => (sig ? sig.split(",") : []));
  useEffect(() => setIds(sig ? sig.split(",") : []), [sig]);
  return ids;
}

/** 打开某台服务器的文件管理独立窗口 */
export async function openFilesWindow(serverId: string) {
  try {
    await api.OpenFilesWindow(serverId);
  } catch (e) {
    toast.error("无法打开文件管理窗口", e);
  }
}
