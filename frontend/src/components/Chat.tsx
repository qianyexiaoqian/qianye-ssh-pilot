import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import {
  Activity,
  ArrowUp,
  BookOpen,
  Bot,
  Brain,
  ChevronDown,
  ChevronRight,
  CircleCheck,
  CircleSlash,
  CircleX,
  FileCode,
  FilePlus,
  FileText,
  History,
  Layers,
  LoaderCircle,
  Paperclip,
  Play,
  Rocket,
  Save,
  Search,
  Server,
  ShieldAlert,
  ShieldCheck,
  Sparkles,
  Square,
  SquareTerminal,
  Stethoscope,
  TriangleAlert,
  Upload,
  User,
  Wrench,
} from "lucide-react";
import type { AgentMessage, ApprovalRequest, Attachment, RecallItem, ToolCall } from "../lib/api";
import { attachPasted, discardAttachments, mergeAttachments, pickAttachments } from "../lib/attach";
import { cn, formatDate, stripAnsi } from "../lib/format";
import type { AgentStore } from "../lib/agent";
import { AttachmentList } from "./Attachments";
import { Badge, Button, Spinner } from "./ui";
import { CodeBlock, Markdown, OutputBox } from "./widgets";

// 对话界面的公共部分：AI 助手页面和终端里的「AI 运维」面板共用。

const toolMeta: Record<string, { icon: typeof Bot; label: string }> = {
  run_command: { icon: SquareTerminal, label: "执行命令" },
  read_file: { icon: FileText, label: "读取文件" },
  write_file: { icon: FilePlus, label: "写入文件" },
  read_terminal: { icon: SquareTerminal, label: "查看终端内容" },
  server_status: { icon: Activity, label: "采集服务器概况" },
  service_control: { icon: Layers, label: "服务管理" },
  list_servers: { icon: Server, label: "查看服务器" },
  list_certificates: { icon: ShieldCheck, label: "查看证书" },
  deploy_certificate: { icon: Rocket, label: "部署证书" },
  save_deploy_target: { icon: ShieldCheck, label: "设置部署目标" },
  list_scripts: { icon: FileCode, label: "查看脚本库" },
  run_script: { icon: Play, label: "执行脚本" },
  save_script: { icon: Save, label: "保存脚本" },
  run_inspection: { icon: Stethoscope, label: "智能巡检" },
  memory: { icon: Brain, label: "更新记忆" },
  skills_list: { icon: BookOpen, label: "查看技能库" },
  skill_view: { icon: BookOpen, label: "阅读技能" },
  skill_manage: { icon: Sparkles, label: "沉淀技能" },
  session_search: { icon: Search, label: "检索历史对话" },
  recall: { icon: History, label: "检索长期记忆" },
  read_attachment: { icon: Paperclip, label: "阅读附件" },
  upload_attachment: { icon: Upload, label: "上传到服务器" },
};

const memoryTarget: Record<string, string> = { memory: "核心记忆", user: "用户画像", longterm: "长期记忆" };
const memoryAction: Record<string, string> = { add: "新增", patch: "修改", remove: "删除", archive: "移到长期记忆" };

function parseArgs(s: string): Record<string, any> {
  try {
    return JSON.parse(s || "{}");
  } catch {
    return {};
  }
}

/** names 是这个对话里附件的 ID → 文件名，工具参数里填的是 ID */
function toolSummary(call: ToolCall, names: Record<string, string> = {}): string {
  const a = parseArgs(call.arguments);
  const servers = Array.isArray(a.servers) ? a.servers.join("、") : a.server ?? "";
  const at = (text: string) => (servers ? `${servers} · ${text}` : text);
  switch (call.name) {
    case "run_command":
      return at(String(a.command ?? "").split("\n")[0]);
    case "read_file":
    case "write_file":
      return at(a.path ?? "");
    case "service_control":
      return at(`${a.action} ${a.service}`);
    case "read_terminal":
      return servers || "当前终端";
    case "server_status":
      return servers || "当前服务器";
    case "run_inspection":
      return servers || "全部服务器";
    case "deploy_certificate":
      return a.certificate ?? "";
    case "save_deploy_target":
      return `${a.certificate ?? ""}${a.action === "remove" ? " · 删除" : a.cert_path ? ` → ${servers ? servers + ":" : ""}${a.cert_path}` : ""}${a.deploy ? " · 立即部署" : ""}`;
    case "run_script":
      return `${a.script ?? ""} → ${servers}`;
    case "save_script":
      return a.name ?? "";
    case "memory":
      return at(`${memoryTarget[a.target as string] ?? a.target} · ${memoryAction[a.action as string] ?? a.action}`);
    case "recall":
      return at(a.query ?? "全部记忆");
    case "skill_view":
    case "skill_manage":
      return `${a.name ?? ""}${call.name === "skill_manage" ? ` · ${a.action}` : ""}`;
    case "session_search":
      return a.query ?? "";
    case "list_servers":
    case "list_scripts":
      return a.keyword ?? "";
    case "read_attachment":
      return names[a.attachment] ?? a.attachment ?? "";
    case "upload_attachment":
      return at(`${names[a.attachment] ?? a.attachment ?? ""} → ${a.path ?? ""}`);
  }
  return "";
}

function ToolCard({ call, result, running, names }: { call: ToolCall; result?: AgentMessage; running: boolean; names?: Record<string, string> }) {
  const [open, setOpen] = useState(false);
  const meta = toolMeta[call.name] ?? { icon: Wrench, label: call.name };
  const args = parseArgs(call.arguments);
  const rmeta = (result?.meta ?? {}) as Record<string, any>;
  const denied = rmeta.kind === "denied";
  const status = !result ? (running ? "running" : "pending") : denied ? "denied" : result.isError ? "error" : "ok";
  const learning = ["memory", "skill_manage", "save_script"].includes(call.name);
  return (
    <div className={cn("overflow-hidden rounded-xl border transition-colors", learning ? "border-accent/30 bg-accent/5" : "border-line2 bg-panel")}>
      <button type="button" onClick={() => setOpen((v) => !v)} className="flex h-10 w-full items-center gap-2.5 px-3 text-left hover:bg-panel2/60">
        <meta.icon size={14} className={cn("shrink-0", learning ? "text-accent" : "text-muted")} />
        <span className="shrink-0 text-[12.5px] font-medium text-fg2">{meta.label}</span>
        <span className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-muted">{toolSummary(call, names)}</span>
        {status === "running" && <LoaderCircle size={14} className="shrink-0 animate-spin text-accent" />}
        {status === "pending" && <span className="shrink-0 text-[11.5px] text-muted">未完成</span>}
        {status === "ok" && <CircleCheck size={14} className="shrink-0 text-ok" />}
        {status === "error" && <CircleX size={14} className="shrink-0 text-bad" />}
        {status === "denied" && (
          <span className="inline-flex shrink-0 items-center gap-1 text-[11.5px] text-warn">
            <CircleSlash size={12} /> 已拒绝
          </span>
        )}
        {open ? <ChevronDown size={14} className="shrink-0 text-muted" /> : <ChevronRight size={14} className="shrink-0 text-muted" />}
      </button>
      {open && (
        <div className="space-y-2.5 border-t border-line px-3 py-3">
          {typeof args.command === "string" && <CodeBlock code={args.command} lang="bash" />}
          {call.name === "write_file" && typeof args.content === "string" && <OutputBox text={args.content.slice(0, 3000)} className="max-h-[200px]" />}
          {typeof args.command !== "string" && call.name !== "write_file" && Object.keys(args).length > 0 && <OutputBox text={JSON.stringify(args, null, 2)} className="max-h-[160px]" />}
          {result &&
            (rmeta.kind === "command" && Array.isArray(rmeta.results) ? (
              <div className="space-y-2">
                {rmeta.results.map((r: any) => (
                  <div key={r.serverId}>
                    <div className="mb-1 flex items-center gap-2 text-[12px]">
                      {!r.error && r.exitCode === 0 ? <CircleCheck size={12} className="text-ok" /> : <CircleX size={12} className="text-bad" />}
                      <span className="font-medium text-fg2">{r.serverName}</span>
                      <span className="text-muted">{r.error ? r.error : `退出码 ${r.exitCode}`}</span>
                    </div>
                    <OutputBox text={stripAnsi(r.output)} className="max-h-[260px]" />
                  </div>
                ))}
              </div>
            ) : (
              <OutputBox text={result.content} className={cn("max-h-[300px]", result.isError && "text-[#ff9aa6]")} />
            ))}
        </div>
      )}
    </div>
  );
}

function ApprovalCard({ req, onResolve, compact }: { req: ApprovalRequest; onResolve: (id: string, approved: boolean, all?: boolean) => void; compact?: boolean }) {
  const danger = req.risk === "danger";
  const Icon = danger ? ShieldAlert : TriangleAlert;
  return (
    <div className={cn("animate-slide-up rounded-2xl border shadow-lg", compact ? "p-3" : "p-4", danger ? "border-bad/40 bg-bad/6" : "border-warn/35 bg-warn/6")}>
      <div className="flex items-start gap-3">
        {!compact && (
          <div className={cn("flex h-9 w-9 shrink-0 items-center justify-center rounded-xl", danger ? "bg-bad/15 text-bad" : "bg-warn/15 text-warn")}>
            <Icon size={18} />
          </div>
        )}
        <div className="min-w-0 flex-1">
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-[13.5px] font-semibold">{danger ? "高危操作，需要你确认" : "需要你的批准"}</span>
            <Badge tone={danger ? "bad" : "warn"} icon={Icon}>
              {danger ? "高危" : "变更"}
            </Badge>
          </div>
          <div className="mt-0.5 text-[12.5px] text-fg2">
            {req.title}
            {req.reason && <span className="text-muted"> · {req.reason}</span>}
          </div>
          {(req.servers ?? []).length > 0 && (
            <div className="mt-2 flex flex-wrap gap-1">
              {req.servers.map((s) => (
                <Badge key={s} icon={Server}>
                  {s}
                </Badge>
              ))}
            </div>
          )}
          {req.detail && <pre className="mt-2.5 max-h-[220px] overflow-auto rounded-lg border border-line bg-[#090a0e] px-3 py-2 font-mono text-[12px] whitespace-pre-wrap text-[#d9dcea]">{req.detail}</pre>}
          <div className="mt-3 flex flex-wrap gap-2">
            <Button size="sm" variant={danger ? "danger" : "primary"} onClick={() => onResolve(req.id, true)}>
              允许执行
            </Button>
            {!danger && (
              <Button size="sm" variant="soft" onClick={() => onResolve(req.id, true, true)}>
                本会话全部允许
              </Button>
            )}
            <Button size="sm" variant="ghost" onClick={() => onResolve(req.id, false)}>
              拒绝
            </Button>
          </div>
        </div>
      </div>
    </div>
  );
}

function Thinking({ text, live }: { text: string; live?: boolean }) {
  const [open, setOpen] = useState(!!live);
  useEffect(() => {
    if (live) setOpen(true);
  }, [live]);
  if (!text.trim()) return null;
  return (
    <div className="mb-2">
      <button type="button" onClick={() => setOpen((v) => !v)} className="inline-flex items-center gap-1.5 text-[12px] text-muted hover:text-fg2">
        {live ? <LoaderCircle size={12} className="animate-spin" /> : <Sparkles size={12} />}
        思考过程
        {open ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
      </button>
      {open && <div className="mt-1.5 border-l-2 border-line2 pl-3 text-[12.5px] leading-relaxed whitespace-pre-wrap text-muted">{text}</div>}
    </div>
  );
}

const recallKind: Record<string, { label: string; icon: typeof Bot }> = {
  fact: { label: "记忆", icon: Brain },
  episode: { label: "往期对话", icon: History },
  skill: { label: "相关技能", icon: BookOpen },
};

/** 用户消息下面的小标签：这次提问时程序自动附给 AI 的长期记忆，点开可以看到具体是哪几条 */
function RecallChip({ items }: { items?: RecallItem[] }) {
  const [open, setOpen] = useState(false);
  if (!items?.length) return null;
  return (
    <div className="flex max-w-full flex-col items-end gap-1.5">
      <button type="button" onClick={() => setOpen((v) => !v)} className="inline-flex items-center gap-1.5 rounded-full border border-accent/25 bg-accent/6 px-2.5 py-1 text-[11.5px] text-fg2 transition-colors hover:border-accent/45 hover:text-fg">
        <Brain size={12} className="text-accent" />
        已参考 {items.length} 条长期记忆
        {open ? <ChevronDown size={12} /> : <ChevronRight size={12} />}
      </button>
      {open && (
        <div className="w-full space-y-1.5 rounded-xl border border-line2 bg-panel p-2.5 text-left">
          {items.map((it, i) => {
            const k = recallKind[it.kind] ?? recallKind.fact;
            return (
              <div key={i} className="rounded-lg border border-line bg-bg2/60 px-2.5 py-2">
                <div className="mb-1 flex flex-wrap items-center gap-1.5 text-[11.5px] text-muted">
                  <Badge tone="accent" icon={k.icon}>
                    {k.label}
                  </Badge>
                  {(it.servers ?? []).map((s) => (
                    <Badge key={s} icon={Server}>
                      {s}
                    </Badge>
                  ))}
                  {it.kind === "episode" && (
                    <span className="truncate">
                      {it.title ? `《${it.title}》` : ""}
                      {it.time ? ` ${formatDate(it.time)}` : ""}
                    </span>
                  )}
                  <span className="ml-auto shrink-0">相关度 {it.score.toFixed(2)}</span>
                </div>
                <div className="selectable text-[12.5px] leading-relaxed break-words whitespace-pre-wrap text-fg2">{it.text}</div>
              </div>
            );
          })}
        </div>
      )}
    </div>
  );
}

function AssistantAvatar() {
  return (
    <div className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-xl grad-accent text-white shadow-[var(--glow)]">
      <Bot size={16} />
    </div>
  );
}

/**
 * 一个会话的消息列表：用户与 AI 的消息、工具调用、正在生成的内容和等待批准的操作。
 * compact 用于较窄的面板：不显示头像，间距更小。
 */
export function ChatThread({ store, compact, onRun, runLabel }: { store: AgentStore; compact?: boolean; onRun?: (code: string) => void; runLabel?: string }) {
  const ag = store();
  const running = ag.currentId ? !!ag.running[ag.currentId] : false;
  // 把工具结果挂到对应的工具调用上
  const results = useMemo(() => {
    const m: Record<string, AgentMessage> = {};
    for (const x of ag.messages) if (x.role === "tool" && x.toolCallId) m[x.toolCallId] = x;
    return m;
  }, [ag.messages]);
  const attachNames = useMemo(() => {
    const m: Record<string, string> = {};
    for (const x of ag.messages) for (const a of (x.attachments ?? []) as Attachment[]) m[a.id] = a.name;
    return m;
  }, [ag.messages]);

  return (
    <>
      {ag.loading && ag.messages.length === 0 && (
        <div className="flex justify-center py-10">
          <Spinner />
        </div>
      )}
      {ag.messages.map((m) => {
        if (m.role === "tool") return null;
        if ((m.role as string) === "learn")
          return (
            <div key={m.id} className="flex justify-center">
              <div className="inline-flex max-w-[90%] items-center gap-2 rounded-full border border-accent/25 bg-accent/8 px-3.5 py-1.5 text-[12px]">
                <Brain size={13} className="shrink-0 text-accent" />
                <span className="truncate font-medium text-fg2">{m.content}</span>
                {m.display && !compact && <span className="truncate text-muted">· {m.display}</span>}
              </div>
            </div>
          );
        if (m.kind === "notice")
          return (
            <div key={m.id} className="flex justify-center">
              <span className="rounded-full border border-line bg-panel px-3 py-1 text-center text-[12px] text-muted">{m.content}</span>
            </div>
          );
        if (m.role === "user") {
          const atts = (m.attachments ?? []) as Attachment[];
          // 只发了附件、没有写字的消息不显示空气泡
          const text = m.display || (atts.length ? "" : m.content);
          return (
            <div key={m.id} className="flex justify-end gap-3">
              <div className={cn("flex min-w-0 flex-col items-end gap-1.5", compact ? "max-w-[92%]" : "max-w-[78%]")}>
                <AttachmentList items={atts} sessionId={ag.currentId} align="end" />
                {text && (
                  <div className={cn("selectable max-w-full rounded-2xl rounded-tr-md bg-accent/14 leading-relaxed break-words whitespace-pre-wrap text-fg", compact ? "px-3 py-2 text-[13px]" : "px-4 py-2.5 text-[13.5px]")}>{text}</div>
                )}
                <RecallChip items={((m.meta ?? {}) as { recall?: RecallItem[] }).recall} />
              </div>
              {!compact && (
                <div className="mt-0.5 flex h-8 w-8 shrink-0 items-center justify-center rounded-xl bg-panel3 text-fg2">
                  <User size={15} />
                </div>
              )}
            </div>
          );
        }
        return (
          <div key={m.id} className="flex gap-3">
            {!compact && <AssistantAvatar />}
            <div className="min-w-0 flex-1 space-y-2">
              {m.thinking && <Thinking text={m.thinking} />}
              {m.content && <Markdown text={m.content} onRun={onRun} runLabel={runLabel} />}
              {(m.toolCalls ?? []).map((c) => (
                <ToolCard key={c.id} call={c} result={results[c.id]} running={running && ag.live.activeTool === c.id} names={attachNames} />
              ))}
            </div>
          </div>
        );
      })}
      {running && (ag.live.text || ag.live.thinking || ag.pending.length === 0) && (
        <div className="flex gap-3">
          {!compact && <AssistantAvatar />}
          <div className="min-w-0 flex-1">
            {ag.live.thinking && <Thinking text={ag.live.thinking} live={!ag.live.text} />}
            {ag.live.text ? (
              <Markdown text={ag.live.text + " ▍"} />
            ) : (
              !ag.live.thinking &&
              ag.pending.length === 0 && (
                <div className="flex h-8 items-center gap-1.5">
                  {[0, 1, 2].map((i) => (
                    <span key={i} className="h-1.5 w-1.5 animate-bounce rounded-full bg-accent/70" style={{ animationDelay: `${i * 120}ms` }} />
                  ))}
                </div>
              )
            )}
          </div>
        </div>
      )}
      {ag.pending.map((p) => (
        <ApprovalCard key={p.id} req={p} onResolve={ag.resolve} compact={compact} />
      ))}
    </>
  );
}

/** 让滚动容器在有新内容时保持在底部；用户往上翻看时不打扰 */
export function useStickToBottom(deps: unknown[]) {
  const ref = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  useEffect(() => {
    const el = ref.current;
    if (el && stick.current) el.scrollTop = el.scrollHeight;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, deps);
  const onScroll = (e: React.UIEvent<HTMLDivElement>) => {
    const el = e.currentTarget;
    stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 140;
  };
  return { ref, onScroll, stick };
}

export function Composer({
  value,
  onChange,
  onSend,
  onStop,
  running,
  placeholder,
  hint,
  compact,
  above,
  inputRef,
  attachments,
  onAttachments,
}: {
  value: string;
  onChange: (v: string) => void;
  onSend: () => void;
  onStop: () => void;
  running: boolean;
  placeholder: string;
  hint?: ReactNode;
  compact?: boolean;
  /** 显示在输入框上方的内容（例如随消息附带的终端输出） */
  above?: ReactNode;
  inputRef?: React.RefObject<HTMLTextAreaElement | null>;
  /** 准备随消息发送的图片和文件；传了 onAttachments 才显示添加附件的入口 */
  attachments?: Attachment[];
  onAttachments?: (next: Attachment[]) => void;
}) {
  const own = useRef<HTMLTextAreaElement>(null);
  const ref = inputRef ?? own;
  const list = attachments ?? [];
  // 添加附件要等后端处理完：期间用户可能又加了别的，合并时以最新的列表为准
  const latest = useRef(list);
  latest.current = list;
  const [picking, setPicking] = useState(false);
  const add = (added: Attachment[]) => {
    if (added.length) onAttachments?.(mergeAttachments(latest.current, added));
  };
  const pick = async () => {
    setPicking(true);
    add(await pickAttachments());
    setPicking(false);
    ref.current?.focus();
  };
  const canSend = !!value.trim() || list.length > 0;
  // 内容变化（包括发送后清空）时按内容调整高度
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    el.style.height = "auto";
    el.style.height = Math.min(el.scrollHeight, compact ? 140 : 200) + "px";
  }, [value, compact, ref]);
  return (
    <div className={cn("rounded-2xl border bg-panel shadow-lg transition-colors", compact ? "p-1.5" : "p-2", "border-line2 focus-within:border-accent/50 focus-within:shadow-[var(--glow)]")}>
      {above}
      {list.length > 0 && (
        <div className={cn(compact ? "px-1.5 pt-2 pb-1" : "px-2 pt-2 pb-1")}>
          <AttachmentList
            items={list}
            sessionId={null}
            onRemove={(a) => {
              discardAttachments([a]);
              onAttachments?.(latest.current.filter((x) => x.id !== a.id));
            }}
          />
        </div>
      )}
      <textarea
        ref={ref}
        value={value}
        onChange={(e) => onChange(e.target.value)}
        onPaste={(e) => {
          // 粘贴截图或复制的文件：加为附件。剪贴板里同时有文字时（例如从表格里复制）按文字粘贴
          const files = Array.from(e.clipboardData.files);
          if (!onAttachments || !files.length || e.clipboardData.getData("text/plain")) return;
          e.preventDefault();
          void attachPasted(files).then(add);
        }}
        onKeyDown={(e) => {
          if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
            e.preventDefault();
            onSend();
          }
        }}
        rows={1}
        placeholder={placeholder}
        className={cn("block w-full resize-none bg-transparent leading-relaxed outline-none placeholder:text-muted", compact ? "max-h-[140px] min-h-[36px] px-2 py-1.5 text-[13px]" : "max-h-[200px] min-h-[44px] px-2.5 py-2 text-[13.5px]")}
      />
      <div className="flex items-center justify-between gap-2 px-1">
        <div className="flex min-w-0 items-center gap-1">
          {onAttachments && (
            <button
              type="button"
              onClick={() => void pick()}
              disabled={picking}
              title="添加图片或文件：也可以直接粘贴截图，或把文件拖进对话"
              data-attach-button
              className={cn("flex shrink-0 items-center justify-center rounded-lg text-muted transition-colors hover:bg-panel3 hover:text-fg", compact ? "h-7 w-7" : "h-8 w-8")}
            >
              {picking ? <LoaderCircle size={14} className="animate-spin" /> : <Paperclip size={compact ? 14 : 15} />}
            </button>
          )}
          <span className="min-w-0 truncate text-[11.5px] text-muted">{hint}</span>
        </div>
        {running ? (
          <Button size="sm" variant="danger" icon={Square} onClick={onStop}>
            停止
          </Button>
        ) : (
          <button type="button" disabled={!canSend} onClick={onSend} title="发送（Enter）" className={cn("flex shrink-0 items-center justify-center rounded-xl grad-accent text-white transition-opacity disabled:opacity-35", compact ? "h-7 w-7" : "h-8 w-8")}>
            <ArrowUp size={compact ? 15 : 16} />
          </button>
        )}
      </div>
    </div>
  );
}
