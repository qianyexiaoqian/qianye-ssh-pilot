import { create, type StoreApi, type UseBoundStore } from "zustand";
import { api, errText, type AgentEvent, type AgentMessage, type ApprovalRequest, type SessionSummary } from "./api";
import { toast } from "./store";

export interface Live {
  text: string;
  thinking: string;
  /** 正在执行的那一次工具调用，以及它开始的时间 */
  activeTool?: string;
  activeSince?: number;
}

const emptyLive: Live = { text: "", thinking: "" };

export interface AgentState {
  sessions: SessionSummary[];
  currentId: string | null;
  messages: AgentMessage[];
  pending: ApprovalRequest[];
  running: Record<string, boolean>;
  /** 已经点了「停止」、还在等这一轮收尾的会话 */
  stopping: Record<string, boolean>;
  reviewing: Record<string, boolean>;
  /** 每个会话里等待用户批准的操作数量（包括没有打开的会话） */
  waiting: Record<string, number>;
  live: Live;
  approveAll: boolean;
  tokens: { input: number; output: number };
  loading: boolean;

  loadSessions: () => Promise<void>;
  open: (id: string | null) => Promise<void>;
  /** serverId 不为空时，新对话绑定到这台服务器（终端里的「AI 运维」） */
  create: (serverId?: string) => Promise<string | null>;
  /** attachments 是随消息发送的附件 ID（图片和文件） */
  send: (text: string, serverId?: string, attachments?: string[]) => Promise<boolean>;
  stop: () => void;
  remove: (id: string) => Promise<void>;
  rename: (id: string, title: string) => Promise<void>;
  resolve: (approvalId: string, approved: boolean, all?: boolean) => Promise<void>;
  review: () => Promise<void>;
  handle: (e: AgentEvent) => void;
}

export type AgentStore = UseBoundStore<StoreApi<AgentState>>;

const learnLabel: Record<string, string> = { memory: "记忆", user: "用户画像", longterm: "长期记忆", skill: "技能", script: "脚本库" };
const actionLabel: Record<string, string> = { add: "新增", patch: "更新", remove: "删除", create: "新建", edit: "更新", delete: "删除", archive: "归档", restore: "恢复" };
const sourceLabel: Record<string, string> = { review: "后台复盘", curator: "自动整理" };

// 两个界面各自查看自己的会话：AI 助手页面（primary）和终端里的「AI 运维」面板。
// 会话列表是共用的；提示消息只由 primary 弹出，避免同一件事提示两次。
const stores: AgentStore[] = [];
const shareSessions = (fn: (list: SessionSummary[]) => SessionSummary[]) => stores.forEach((s) => s.setState({ sessions: fn(s.getState().sessions) }));

function createAgentStore(primary: boolean): AgentStore {
  const store: AgentStore = create<AgentState>((set, get) => ({
    sessions: [],
    currentId: null,
    messages: [],
    pending: [],
    running: {},
    stopping: {},
    reviewing: {},
    waiting: {},
    live: emptyLive,
    approveAll: false,
    tokens: { input: 0, output: 0 },
    loading: false,

    loadSessions: async () => {
      try {
        const sessions = (await api.AgentListSessions()) ?? [];
        for (const s of stores) {
          const running: Record<string, boolean> = { ...s.getState().running };
          for (const x of sessions) if (x.running) running[x.id] = true;
          s.setState({ sessions, running });
        }
      } catch (e) {
        console.error(e);
      }
    },

    open: async (id) => {
      if (!id) {
        set({ currentId: null, messages: [], pending: [], live: emptyLive, approveAll: false, tokens: { input: 0, output: 0 } });
        return;
      }
      // 重新加载同一会话时保留界面上的学习提示（它们不保存在会话记录中）
      const learnChips = get().currentId === id ? get().messages.filter((m) => (m.role as string) === "learn") : [];
      if (get().currentId !== id) set({ messages: [], pending: [], live: emptyLive });
      set({ currentId: id, loading: true });
      try {
        const v = await api.AgentGetSession(id);
        if (get().currentId !== id) return;
        const merged = [...(v.messages ?? []), ...learnChips].sort((a, b) => a.createdAt - b.createdAt);
        set({
          messages: merged,
          pending: v.pending ?? [],
          approveAll: v.approveAll,
          tokens: { input: v.inputTokens, output: v.outputTokens },
          running: { ...get().running, [id]: v.running },
          waiting: { ...get().waiting, [id]: (v.pending ?? []).length },
          // 切走再回来时会话可能还在执行某一步：把“正在执行”的状态接上，而不是显示成没做完
          live: v.running && v.activeTool ? { ...emptyLive, activeTool: v.activeTool, activeSince: v.activeSince || Date.now() } : emptyLive,
          loading: false,
        });
      } catch (e) {
        set({ loading: false });
        toast.error("打开会话失败", e);
      }
    },

    create: async (serverId = "") => {
      try {
        const s = await api.AgentNewSession(serverId);
        shareSessions((list) => [s, ...list.filter((x) => x.id !== s.id)]);
        await get().open(s.id);
        return s.id;
      } catch (e) {
        toast.error("创建会话失败", e);
        return null;
      }
    },

    send: async (text, serverId = "", attachments = []) => {
      let id = get().currentId;
      if (!id) id = await get().create(serverId);
      if (!id) return false;
      try {
        set({ running: { ...get().running, [id]: true }, live: emptyLive });
        await api.AgentSend(id, text, attachments);
        return true;
      } catch (e) {
        set({ running: { ...get().running, [id]: false } });
        toast.error("发送失败", e);
        return false;
      }
    },

    stop: () => {
      const id = get().currentId;
      if (!id) return;
      set({ stopping: { ...get().stopping, [id]: true } });
      api.AgentStop(id);
    },

    remove: async (id) => {
      try {
        await api.AgentDeleteSession(id);
        shareSessions((list) => list.filter((s) => s.id !== id));
        for (const s of stores) if (s.getState().currentId === id) await s.getState().open(null);
      } catch (e) {
        toast.error("删除失败", e);
      }
    },

    rename: async (id, title) => {
      try {
        await api.AgentRenameSession(id, title);
        shareSessions((list) => list.map((s) => (s.id === id ? { ...s, title } : s)) as SessionSummary[]);
      } catch (e) {
        toast.error("重命名失败", e);
      }
    },

    resolve: async (approvalId, approved, all = false) => {
      try {
        await api.AgentResolveApproval(approvalId, approved, all);
        if (all && approved) set({ approveAll: true });
      } catch (e) {
        toast.error("操作失败", errText(e));
      }
      set({ pending: get().pending.filter((p) => p.id !== approvalId) });
    },

    review: async () => {
      const id = get().currentId;
      if (!id) return;
      try {
        await api.AgentReview(id);
        set({ reviewing: { ...get().reviewing, [id]: true } });
        toast.info("已开始学习复盘", "AI 正在回顾本次会话，提炼记忆与技能");
      } catch (e) {
        toast.error("无法开始复盘", e);
      }
    },

    handle: (e) => {
      const st = get();
      const sid = e.sessionId;
      const current = sid === st.currentId;
      switch (e.type) {
        case "done":
          set({ running: { ...st.running, [sid]: false }, stopping: { ...st.stopping, [sid]: false }, waiting: { ...st.waiting, [sid]: 0 } });
          if (current) {
            set({ live: emptyLive });
            void get().open(sid);
          }
          if (primary) void get().loadSessions();
          return;
        case "title":
          set({ sessions: st.sessions.map((s) => (s.id === sid ? { ...s, title: e.text ?? s.title } : s)) as SessionSummary[] });
          return;
        case "review_start":
          set({ reviewing: { ...st.reviewing, [sid]: true } });
          return;
        case "review_done": {
          set({ reviewing: { ...st.reviewing, [sid]: false } });
          if (!primary) return;
          const n = Number(e.status || 0);
          if (e.error) toast.warn("学习复盘失败", e.error);
          else if (n > 0) toast.ok(`学习复盘完成：沉淀了 ${n} 条知识`, e.text);
          return;
        }
        case "learning": {
          const l = e.learn;
          if (!l) return;
          // 把核心记忆移到长期记忆：说“归档到长期记忆”比“归档了长期记忆”更清楚
          const what = l.kind === "longterm" && l.action === "archive" ? "把一条核心记忆移到了长期记忆" : `${actionLabel[l.action] ?? "更新"}了${learnLabel[l.kind] ?? l.kind}${l.kind === "skill" || l.kind === "script" ? "：" + l.target : ""}`;
          const title = `${sourceLabel[l.source] ?? "AI "}${what}`;
          if (primary) toast.info(title, l.summary);
          if (current) {
            const pseudo = { id: "learn-" + l.time + Math.random(), role: "learn", content: title, display: l.summary, createdAt: l.time } as unknown as AgentMessage;
            set({ messages: [...get().messages, pseudo] });
          }
          return;
        }
        case "error":
          // 同一个会话同时开在两个界面时只提示一次
          if (current && (primary || stores[0].getState().currentId !== sid)) toast.error(primary ? "AI 助手出错" : "AI 运维出错", e.error);
          return;
        case "user":
          set({ running: { ...st.running, [sid]: true } });
          break;
        case "approval":
          set({ waiting: { ...st.waiting, [sid]: (st.waiting[sid] ?? 0) + 1 } });
          break;
        case "approval_resolved":
          set({ waiting: { ...st.waiting, [sid]: Math.max(0, (st.waiting[sid] ?? 0) - 1) } });
          break;
      }
      if (!current) return;
      const push = (m?: AgentMessage) => {
        if (!m) return;
        const msgs = get().messages;
        if (msgs.some((x) => x.id === m.id)) return;
        set({ messages: [...msgs, m] });
      };
      switch (e.type) {
        case "user":
          push(e.message);
          break;
        case "step":
          set({ live: emptyLive });
          break;
        case "text":
          set({ live: { ...get().live, text: get().live.text + (e.text ?? "") } });
          break;
        case "thinking":
          set({ live: { ...get().live, thinking: get().live.thinking + (e.text ?? "") } });
          break;
        case "reset":
          set({ live: { ...get().live, text: "" } });
          break;
        case "assistant":
          push(e.message);
          set({ live: emptyLive });
          break;
        case "tool_start":
          set({ live: { ...get().live, activeTool: e.toolCall?.id, activeSince: Date.now() } });
          break;
        case "tool_end":
          push(e.message);
          set({ live: { ...get().live, activeTool: undefined, activeSince: undefined } });
          break;
        case "notice":
          push(e.message);
          break;
        case "recall": {
          // 程序为刚发出的这条消息找到了相关的长期记忆：把记录补到消息上
          const m = e.message;
          if (m) set({ messages: get().messages.map((x) => (x.id === m.id ? m : x)) });
          break;
        }
        case "approval":
          if (e.approval) set({ pending: [...get().pending.filter((p) => p.id !== e.approval!.id), e.approval] });
          break;
        case "approval_resolved":
          if (e.approval) set({ pending: get().pending.filter((p) => p.id !== e.approval!.id) });
          break;
      }
    },
  }));
  stores.push(store);
  return store;
}

/** AI 助手页面 */
export const useAgent = createAgentStore(true);
/** 终端里的「AI 运维」面板：与 AI 助手页面各看各的会话 */
export const useOpsAgent = createAgentStore(false);

/** 把后端推送的事件分发给两个界面 */
export function dispatchAgentEvent(e: AgentEvent) {
  stores.forEach((s) => s.getState().handle(e));
}
