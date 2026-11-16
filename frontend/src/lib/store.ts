import { create } from "zustand";
import { api, errText, type KeyView, type LockState, type RouteView, type Server, type ServerStatus, type Settings, type AppInfo } from "./api";

export type Page =
  | "dashboard"
  | "monitor"
  | "servers"
  | "terminal"
  | "files"
  | "keys"
  | "scripts"
  | "certs"
  | "services"
  | "inspect"
  | "assistant"
  | "brain"
  | "settings";

export interface TermTab {
  key: string;
  serverId: string;
  title: string;
  termId?: string;
  state: "connecting" | "open" | "closed";
  message?: string;
  seq: number;
}

interface AppState {
  page: Page;
  params: Record<string, any>;
  go: (page: Page, params?: Record<string, any>) => void;

  info: AppInfo | null;
  servers: Server[];
  status: Record<string, ServerStatus>;
  keys: KeyView[];
  settings: Settings | null;
  theme: "dark" | "light";
  sidebarCollapsed: boolean;
  /** 启动密码的状态；null 表示还没有读到 */
  lock: LockState | null;
  /** 正在生效的临时线路，按服务器 ID 索引 */
  routes: Record<string, RouteView>;
  /** 正在为哪台服务器选择连接线路（打开线路对话框） */
  routeFor: string | null;

  loadLock: () => Promise<void>;
  setLock: (l: LockState) => void;
  loadRoutes: () => Promise<void>;
  openRoute: (serverId: string | null) => void;
  loadInfo: () => Promise<void>;
  loadServers: () => Promise<void>;
  loadKeys: () => Promise<void>;
  loadSettings: () => Promise<void>;
  setStatus: (s: Record<string, ServerStatus>) => void;
  setTheme: (t: "dark" | "light") => void;
  toggleSidebar: () => void;

  terms: TermTab[];
  activeTerm: string | null;
  openTerminal: (serverId: string) => void;
  closeTab: (key: string) => void;
  setActiveTerm: (key: string) => void;
  patchTerm: (key: string, patch: Partial<TermTab>) => void;
  reconnectTab: (key: string) => void;
}

let tabSeq = 0;

export const useApp = create<AppState>((set, get) => ({
  page: "dashboard",
  params: {},
  go: (page, params = {}) => set({ page, params }),

  info: null,
  servers: [],
  status: {},
  keys: [],
  settings: null,
  theme: (localStorage.getItem("theme") as "dark" | "light") || "dark",
  sidebarCollapsed: localStorage.getItem("sidebar") === "collapsed",
  lock: null,
  routes: {},
  routeFor: null,

  loadLock: async () => {
    try {
      set({ lock: await api.GetLockState() });
    } catch (e) {
      console.error(errText(e));
    }
  },
  setLock: (l) => set({ lock: l }),
  loadRoutes: async () => {
    const list = (await api.ListTempRoutes()) ?? [];
    set({ routes: Object.fromEntries(list.map((r) => [r.serverId, r])) });
  },
  openRoute: (serverId) => set({ routeFor: serverId }),
  loadInfo: async () => {
    try {
      set({ info: await api.GetAppInfo() });
    } catch (e) {
      console.error(errText(e));
    }
  },
  loadServers: async () => {
    const servers = (await api.ListServers()) ?? [];
    const status = { ...get().status };
    for (const s of servers) {
      if (!status[s.id] && s.lastStatus) {
        status[s.id] = { status: s.lastStatus as ServerStatus["status"], latency: s.lastLatency, checkedAt: s.lastCheckAt, error: s.lastError };
      }
    }
    set({ servers, status });
  },
  loadKeys: async () => set({ keys: (await api.ListKeys()) ?? [] }),
  loadSettings: async () => {
    const settings = await api.GetSettings();
    set({ settings });
  },
  setStatus: (s) => set({ status: s }),
  setTheme: (t) => {
    localStorage.setItem("theme", t);
    document.documentElement.dataset.theme = t;
    set({ theme: t });
  },
  toggleSidebar: () => {
    const v = !get().sidebarCollapsed;
    localStorage.setItem("sidebar", v ? "collapsed" : "expanded");
    set({ sidebarCollapsed: v });
  },

  terms: [],
  activeTerm: null,
  openTerminal: (serverId) => {
    const srv = get().servers.find((s) => s.id === serverId);
    const key = `tab-${++tabSeq}`;
    const sameCount = get().terms.filter((t) => t.serverId === serverId).length;
    const title = (srv?.name ?? "终端") + (sameCount ? ` (${sameCount + 1})` : "");
    set((st) => ({
      terms: [...st.terms, { key, serverId, title, state: "connecting", seq: 0 }],
      activeTerm: key,
      page: "terminal",
    }));
  },
  closeTab: (key) =>
    set((st) => {
      const idx = st.terms.findIndex((t) => t.key === key);
      const terms = st.terms.filter((t) => t.key !== key);
      let active = st.activeTerm;
      if (active === key) active = terms[Math.max(0, idx - 1)]?.key ?? null;
      return { terms, activeTerm: active };
    }),
  setActiveTerm: (key) => set({ activeTerm: key }),
  patchTerm: (key, patch) => set((st) => ({ terms: st.terms.map((t) => (t.key === key ? { ...t, ...patch } : t)) })),
  reconnectTab: (key) =>
    set((st) => ({ terms: st.terms.map((t) => (t.key === key ? { ...t, state: "connecting", termId: undefined, message: undefined, seq: t.seq + 1 } : t)) })),
}));

// ---- 全局提示 ----

export interface Toast {
  id: number;
  tone: "ok" | "bad" | "warn" | "info";
  title: string;
  message?: string;
}

interface ToastState {
  toasts: Toast[];
  push: (t: Omit<Toast, "id">, ttl?: number) => void;
  dismiss: (id: number) => void;
}

let toastSeq = 0;
export const useToasts = create<ToastState>((set, get) => ({
  toasts: [],
  push: (t, ttl = 4200) => {
    const id = ++toastSeq;
    set({ toasts: [...get().toasts.slice(-4), { ...t, id }] });
    setTimeout(() => get().dismiss(id), ttl);
  },
  dismiss: (id) => set({ toasts: get().toasts.filter((x) => x.id !== id) }),
}));

export const toast = {
  ok: (title: string, message?: string) => useToasts.getState().push({ tone: "ok", title, message }),
  info: (title: string, message?: string) => useToasts.getState().push({ tone: "info", title, message }),
  warn: (title: string, message?: string) => useToasts.getState().push({ tone: "warn", title, message }, 6000),
  error: (title: string, e?: unknown) =>
    useToasts.getState().push({ tone: "bad", title, message: e === undefined ? undefined : errText(e) }, 7000),
};

// ---- 确认对话框 ----

export interface ConfirmOptions {
  title: string;
  message?: string;
  confirmText?: string;
  danger?: boolean;
}

interface ConfirmState {
  /** at：对话框弹出的时刻（performance.now），用于忽略弹出之前就已按下的按键 */
  current: (ConfirmOptions & { resolve: (v: boolean) => void; at: number }) | null;
  ask: (o: ConfirmOptions) => Promise<boolean>;
  close: (v: boolean) => void;
}

export const useConfirm = create<ConfirmState>((set, get) => ({
  current: null,
  ask: (o) => new Promise<boolean>((resolve) => set({ current: { ...o, resolve, at: performance.now() } })),
  close: (v) => {
    get().current?.resolve(v);
    set({ current: null });
  },
}));

export const confirm = (o: ConfirmOptions) => useConfirm.getState().ask(o);
