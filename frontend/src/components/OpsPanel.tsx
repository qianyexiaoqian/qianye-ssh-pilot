import { useEffect, useRef, useState } from "react";
import { Activity, Bot, Cpu, FileSearch, HardDrive, History, MessageSquarePlus, PanelRightOpen, ScrollText, Settings, SquareTerminal, TextSelect, X } from "lucide-react";
import type { Attachment } from "../lib/api";
import { attachPaths, mergeAttachments } from "../lib/attach";
import { cn, timeAgo } from "../lib/format";
import { useAgent, useOpsAgent } from "../lib/agent";
import { toast, useApp, type TermTab } from "../lib/store";
import { useTermUI } from "../lib/term";
import { DropHint, useAttachDrop } from "./Attachments";
import { ChatThread, Composer, useStickToBottom } from "./Chat";
import { readTermText, typeIntoTerm } from "./XTermView";
import { Button, IconButton, Menu } from "./ui";
import { useAddr } from "./widgets";

// 每台服务器上次使用的对话：切换终端标签时回到各自的对话
const SESSIONS_KEY = "ops-sessions";
function loadMap(): Record<string, string> {
  try {
    return JSON.parse(localStorage.getItem(SESSIONS_KEY) || "{}");
  } catch {
    return {};
  }
}
function remember(serverId: string, sessionId: string | null) {
  const m = loadMap();
  if (sessionId) m[serverId] = sessionId;
  else delete m[serverId];
  localStorage.setItem(SESSIONS_KEY, JSON.stringify(m));
}

/** 面板的最小宽度 */
export const OPS_MIN_W = 320;
const MIN_W = OPS_MIN_W;
const MAX_W = 760;

const prompts = [
  { icon: Activity, title: "检查健康状况", text: "检查这台服务器现在的健康状况（负载、内存、磁盘、失败的服务），有问题的地方告诉我怎么处理。" },
  { icon: FileSearch, title: "分析终端里的输出", text: "帮我看看终端里最近的输出有没有问题，下一步该怎么做。", attachRecent: true },
  { icon: Cpu, title: "谁在占用资源", text: "找出这台服务器上占用 CPU 和内存最多的进程，判断是否正常。" },
  { icon: HardDrive, title: "磁盘空间", text: "看看磁盘空间够不够用，哪些目录占用最大，哪些可以安全清理。" },
  { icon: ScrollText, title: "最近的错误日志", text: "查看系统和主要服务最近的错误日志，总结需要关注的问题。" },
];

function lineCount(text: string) {
  return text.split("\n").length;
}

/**
 * 终端右侧的「AI 运维」面板：直接和 AI 对话，让它操作当前终端所在的服务器。
 * 对话绑定到服务器（后端在系统提示词里说明默认操作这台服务器），每台服务器各有自己的对话。
 *
 * maxWidth：和终端并排显示时面板最多能有多宽（要给终端留出最小宽度）；盖在终端上面时不限制。
 */
export function OpsPanel({ tab, onClose, maxWidth }: { tab: TermTab | undefined; onClose: () => void; maxWidth?: number }) {
  const { servers, settings, go } = useApp();
  const addr = useAddr();
  const ops = useOpsAgent();
  const { attach, setAttach } = useTermUI();
  const [input, setInput] = useState("");
  const [atts, setAtts] = useState<Attachment[]>([]);
  const attsRef = useRef(atts);
  attsRef.current = atts;
  const drop = useAttachDrop<HTMLElement>((paths) => void attachPaths(paths).then((added) => added.length && setAtts(mergeAttachments(attsRef.current, added))));
  const [width, setWidth] = useState(() => Math.min(MAX_W, Math.max(MIN_W, Number(localStorage.getItem("ops-width")) || 420)));
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const serverId = tab?.serverId ?? "";
  const server = servers.find((s) => s.id === serverId);
  const running = ops.currentId ? !!ops.running[ops.currentId] : false;
  const mine = ops.sessions.filter((s) => s.serverId === serverId);
  const { ref: scrollRef, onScroll, stick } = useStickToBottom([ops.messages, ops.live, ops.pending]);
  const aiReady = !!settings && (settings.ai.hasApiKey || /localhost|127\.0\.0\.1/.test(settings.ai.baseUrl));

  // 切换到另一台服务器的终端时，换成那台服务器上次的对话
  const sessionsLoaded = ops.sessions.length > 0;
  useEffect(() => {
    if (!serverId) return;
    const want = loadMap()[serverId];
    const st = useOpsAgent.getState();
    const valid = want && st.sessions.some((s) => s.id === want && s.serverId === serverId) ? want : null;
    const curOK = st.currentId && st.sessions.some((s) => s.id === st.currentId && s.serverId === serverId);
    if (valid) {
      if (st.currentId !== valid) void st.open(valid);
    } else if (!curOK && st.currentId) void st.open(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [serverId, sessionsLoaded]);

  // 从右键菜单带着终端内容过来时，把光标放进输入框
  useEffect(() => {
    if (attach) inputRef.current?.focus();
  }, [attach]);

  const send = async (text?: string, extra?: string) => {
    const t = (text ?? input).trim();
    const files = atts;
    if ((!t && files.length === 0) || running || !serverId) return;
    const term = extra ?? attach;
    const fence = "```";
    const body = term ? `${t}\n\n终端里的内容：\n${fence}\n${term}\n${fence}` : t;
    setInput("");
    setAttach("");
    setAtts([]);
    stick.current = true;
    const ok = await ops.send(
      body,
      serverId,
      files.map((a) => a.id),
    );
    const id = useOpsAgent.getState().currentId;
    if (id) remember(serverId, id);
    if (!ok) {
      setInput(t);
      setAttach(term);
      setAtts(files);
    }
  };

  const attachFromTerm = () => {
    if (!tab) return;
    const text = readTermText(tab.key, 80);
    if (!text) return toast.warn("终端里还没有内容");
    setAttach(text.length > 8000 ? text.slice(-8000) : text);
    inputRef.current?.focus();
  };

  const fill = (code: string) => {
    if (!tab || !typeIntoTerm(tab.key, code)) toast.warn("终端还没有连接");
  };

  // 拖动左边缘调整宽度
  const startResize = (e: React.MouseEvent) => {
    e.preventDefault();
    const startX = e.clientX;
    let w = width;
    const limit = Math.min(MAX_W, maxWidth ?? MAX_W);
    const startW = Math.min(width, limit);
    const move = (ev: MouseEvent) => {
      w = Math.min(limit, Math.max(MIN_W, startW + startX - ev.clientX));
      setWidth(w);
    };
    const up = () => {
      window.removeEventListener("mousemove", move);
      window.removeEventListener("mouseup", up);
      document.body.style.cursor = "";
      localStorage.setItem("ops-width", String(Math.round(w)));
    };
    document.body.style.cursor = "col-resize";
    window.addEventListener("mousemove", move);
    window.addEventListener("mouseup", up);
  };

  const empty = ops.messages.length === 0 && !running && !ops.loading;

  return (
    <aside ref={drop.ref} className="relative flex h-full max-w-full shrink-0 flex-col border-l border-line bg-bg2 @max-[560px]/term:!w-full" style={{ width: Math.max(MIN_W, Math.min(width, maxWidth ?? width)) }}>
      <DropHint show={drop.over && !!tab} compact />
      <div onMouseDown={startResize} className="absolute inset-y-0 -left-1 z-10 w-2 cursor-col-resize" title="拖动调整宽度" />
      <div className="flex h-10 shrink-0 items-center gap-1 border-b border-line pr-1.5 pl-3">
        <Bot size={14} className="shrink-0 text-accent" />
        <span className="shrink-0 text-[12.5px] font-semibold">AI 运维</span>
        {server && (
          <span className="ml-1 inline-flex min-w-0 items-center gap-1 rounded-md border border-line px-1.5 py-0.5 text-[11.5px] text-fg2" title={`AI 默认操作这台服务器：${addr(server, { port: false })}`}>
            <SquareTerminal size={11} className="shrink-0 text-muted" />
            <span className="truncate">{server.name}</span>
          </span>
        )}
        <div className="flex-1" />
        <IconButton
          icon={MessageSquarePlus}
          size="sm"
          title="新对话"
          disabled={empty && !ops.currentId}
          onClick={() => {
            remember(serverId, null);
            void ops.open(null);
          }}
        />
        <Menu
          align="right"
          trigger={<IconButton icon={History} size="sm" title="这台服务器的历史对话" />}
          items={
            mine.length
              ? mine.slice(0, 12).map((s) => ({
                  label: `${s.title || "新对话"} · ${timeAgo(s.updatedAt)}`,
                  onClick: () => {
                    remember(serverId, s.id);
                    void ops.open(s.id);
                  },
                }))
              : [{ label: "还没有历史对话", onClick: () => {}, disabled: true }]
          }
        />
        <IconButton
          icon={PanelRightOpen}
          size="sm"
          title="在「AI 助手」页面打开这个对话"
          disabled={!ops.currentId}
          onClick={() => {
            if (!ops.currentId) return;
            void useAgent.getState().open(ops.currentId);
            go("assistant");
          }}
        />
        <IconButton icon={X} size="sm" title="收起面板" onClick={onClose} />
      </div>

      {!tab ? (
        <div className="flex flex-1 items-center justify-center px-6 text-center text-[12.5px] text-muted">打开一个终端后，就可以让 AI 帮你操作那台服务器</div>
      ) : (
        <>
          <div ref={scrollRef} onScroll={onScroll} className="min-h-0 flex-1 overflow-y-auto">
            {empty ? (
              <div className="px-3 pt-5 pb-3">
                <div className="px-1 text-[13.5px] font-semibold">
                  想在 <span className="grad-text">{server?.name ?? "这台服务器"}</span> 上做什么？
                </div>
                <p className="mt-1.5 px-1 text-[12px] leading-relaxed text-muted">直接说要做的事，我会在这台服务器上查看和处理。只读的命令直接执行，有修改的操作会先问你。</p>
                {!aiReady && (
                  <div className="mt-3 flex items-center gap-2 rounded-xl border border-warn/35 bg-warn/8 px-3 py-2 text-[12px] text-fg2">
                    <span className="min-w-0 flex-1">还没有填写 AI 的 API Key（默认使用 DeepSeek 官方接口）。</span>
                    <Button size="xs" variant="soft" icon={Settings} onClick={() => go("settings", { tab: "ai" })}>
                      去设置
                    </Button>
                  </div>
                )}
                <div className="mt-3 space-y-1.5">
                  {prompts.map((p) => (
                    <button
                      key={p.title}
                      type="button"
                      onClick={() => {
                        if (!p.attachRecent) return void send(p.text);
                        const text = tab ? readTermText(tab.key, 80) : "";
                        if (!text) return void toast.warn("终端里还没有可分析的内容");
                        void send(p.text, text.length > 8000 ? text.slice(-8000) : text);
                      }}
                      className="flex w-full items-center gap-2.5 rounded-xl border border-line bg-panel px-3 py-2 text-left transition-colors hover:border-accent/40"
                    >
                      <p.icon size={14} className="shrink-0 text-accent" />
                      <span className="min-w-0 flex-1 truncate text-[12.5px] text-fg2">{p.title}</span>
                    </button>
                  ))}
                </div>
              </div>
            ) : (
              <div className="space-y-4 px-3 py-4">
                <ChatThread store={useOpsAgent} compact onRun={fill} runLabel="填入终端" />
              </div>
            )}
          </div>
          <div className="shrink-0 px-2.5 pb-2.5">
            <Composer
              compact
              inputRef={inputRef}
              value={input}
              onChange={setInput}
              onSend={() => void send()}
              onStop={() => ops.stop()}
              running={running}
              stopping={!!ops.currentId && !!ops.stopping[ops.currentId]}
              placeholder={running ? "AI 正在处理…" : `让 AI 操作 ${server?.name ?? "这台服务器"}，例如：nginx 为什么起不来`}
              attachments={atts}
              onAttachments={setAtts}
              above={
                attach ? (
                  <div className="mx-1 mt-1 mb-0.5 flex items-center gap-1.5 rounded-lg border border-line bg-bg2 px-2 py-1 text-[11.5px] text-fg2">
                    <TextSelect size={12} className="shrink-0 text-accent" />
                    <span className="min-w-0 flex-1 truncate" title={attach}>
                      附带终端内容 · {lineCount(attach)} 行
                    </span>
                    <button type="button" className="text-muted hover:text-fg" title="不附带" onClick={() => setAttach("")}>
                      <X size={12} />
                    </button>
                  </div>
                ) : undefined
              }
              hint={
                attach ? (
                  "Enter 发送"
                ) : (
                  <button type="button" className={cn("inline-flex items-center gap-1 hover:text-fg2")} onClick={attachFromTerm} title="把终端里选中的内容（没有选中则取最近的输出）一起发给 AI">
                    <TextSelect size={11} />
                    附带终端内容
                  </button>
                )
              }
            />
          </div>
        </>
      )}
    </aside>
  );
}
