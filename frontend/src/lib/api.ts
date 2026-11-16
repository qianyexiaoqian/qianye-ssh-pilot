import * as App from "../../wailsjs/go/main/App";
import * as RT from "../../wailsjs/runtime/runtime";
import type { main, store, agent, ops, certs, sshx } from "../../wailsjs/go/models";

// 生成的绑定要求传入类实例；这里放宽参数类型，保留返回值类型。
type Loose<T> = {
  [K in keyof T]: T[K] extends (...args: infer A) => infer R ? (...args: { [I in keyof A]: any }) => R : never;
};

export const api = App as unknown as Loose<typeof App>;
export const runtime = RT;

// 生成的模型是带 convertValues 方法的类；界面中只把它们当作纯数据使用。
type Plain<T> = T extends (infer U)[] ? Plain<U>[] : T extends object ? { [K in keyof T as T[K] extends (...a: any[]) => any ? never : K]: Plain<T[K]> } : T;

export type Server = Plain<store.Server>;
export type Settings = Plain<store.Settings>;
export type Script = Plain<store.Script>;
export type ScriptParam = Plain<store.ScriptParam>;
export type RunRecord = Plain<store.RunRecord>;
export type RunResult = Plain<store.RunResult>;
export type DeployTarget = Plain<store.DeployTarget>;
export type PublicKey = Plain<store.PublicKey>;
export type KeyView = Plain<main.KeyView>;
export type PublicKeyView = Plain<main.PublicKeyView>;
export type AuthorizedKeyView = Plain<main.AuthorizedKeyView>;
export type OpResult = Plain<main.OpResult>;
export type TestResult = Plain<main.TestResult>;
export type Dashboard = Plain<main.Dashboard>;
export type CertView = Plain<main.CertView>;
export type TargetView = Plain<main.TargetView>;
export type DiscoveredView = Plain<main.DiscoveredView>;
export type SessionView = Plain<main.SessionView>;
export type AppInfo = Plain<main.AppInfo>;
export type CertParsed = Plain<certs.Parsed>;
export type TLSInfo = Plain<certs.TLSInfo>;
export type ServerInfo = Plain<ops.ServerInfo>;
export type ServiceInfo = Plain<ops.ServiceInfo>;
export type Report = Plain<ops.Report>;
export type Finding = Plain<ops.Finding>;
export type AgentMessage = Plain<agent.Message>;
export type ToolCall = Plain<agent.ToolCall>;
export type ApprovalRequest = Plain<agent.ApprovalRequest>;
export type SessionSummary = Plain<agent.SessionSummary>;
export type MemoryView = Plain<agent.MemoryView>;
export type SkillMeta = Plain<agent.SkillMeta>;
export type Skill = Plain<agent.Skill>;
export type LearnEvent = Plain<agent.LearnEvent>;
export type QuickCommand = Plain<store.QuickCommand>;
export type Sample = Plain<ops.Sample>;
export type AutoSyncResult = Plain<main.AutoSyncResult>;
export type FileEntry = Plain<sshx.FileEntry>;
export type FileListing = Plain<main.FileListing>;
export type FileText = Plain<main.FileText>;
export type Transfer = Plain<main.Transfer>;
export type LongItem = Plain<main.LongItem>;
export type IndexStatus = Plain<agent.IndexStatus>;
export type EmbedTest = Plain<main.EmbedTest>;
export type LockState = Plain<main.LockState>;
export type RouteView = Plain<main.RouteView>;
export type ServerSecrets = Plain<main.ServerSecrets>;
export type LocalProxy = Plain<main.LocalProxy>;
export type ProxyConfig = Plain<store.ProxyConfig>;

/** 界面填写的代理；keepPassword 表示沿用已经保存的代理密码 */
export interface ProxyInput {
  type: "" | "local" | "http" | "socks5";
  host: string;
  port: number;
  username: string;
  password: string;
  keepPassword: boolean;
}

/** 连接线路：直连、经跳板机或经代理 */
export interface RouteInput {
  mode: "direct" | "jump" | "proxy";
  jumpServerId: string;
  proxy: ProxyInput;
}

/** 随用户消息保存的一条召回记录：提问时程序自动附给 AI 的长期记忆、往期对话要点或技能提示 */
export interface RecallItem {
  kind: "fact" | "episode" | "skill";
  text: string;
  title?: string;
  servers?: string[];
  time?: number;
  score: number;
}

/** 证书部署进度（通过 deploy:* 事件推送） */
export interface DeployStep {
  targetId: string;
  step: string;
  label: string;
  status: "running" | "ok" | "failed" | "skipped" | "warning";
  message: string;
  time: number;
}

export interface DeployResult {
  targetId: string;
  serverId: string;
  serverName: string;
  success: boolean;
  message: string;
  fingerprint: string;
  rolledBack: boolean;
  tlsStatus: string;
  steps: DeployStep[];
  durationMs: number;
}

export interface ServerStatus {
  status: "online" | "offline" | "unknown";
  latency: number;
  checkedAt: number;
  error: string;
}

export interface AgentEvent {
  sessionId: string;
  type: string;
  text?: string;
  message?: AgentMessage;
  toolCall?: ToolCall;
  approval?: ApprovalRequest;
  learn?: LearnEvent;
  error?: string;
  status?: string;
}

/** 把 Wails 返回的错误转换成可读文本 */
export function errText(e: unknown): string {
  if (!e) return "未知错误";
  if (typeof e === "string") return e;
  if (e instanceof Error) return e.message;
  try {
    return JSON.stringify(e);
  } catch {
    return String(e);
  }
}

export function on<T = any>(event: string, cb: (data: T) => void): () => void {
  return RT.EventsOn(event, cb as (...data: any) => void);
}
