import { useEffect, useRef, useState } from "react";
import { Activity, Bot, Brain, FileText, KeyRound, MessageSquarePlus, PanelLeft, Pencil, ShieldCheck, SquareTerminal, Stethoscope, Trash2 } from "lucide-react";
import { cn, compact, timeAgo } from "../lib/format";
import { useAgent } from "../lib/agent";
import { confirm, useApp } from "../lib/store";
import { Badge, Button, IconButton } from "../components/ui";
import { ChatThread, Composer, useStickToBottom } from "../components/Chat";
import { QuickRunModal } from "../components/QuickRun";
import { Logo } from "../components/shell";

const quickPrompts = [
  { icon: Stethoscope, title: "全面巡检", text: "巡检所有服务器，按优先级给出需要处理的问题和运维建议。" },
  { icon: ShieldCheck, title: "证书检查", text: "检查证书库中哪些证书快到期了，以及每个部署目标的同步状态，给出处理建议。" },
  { icon: Activity, title: "磁盘排查", text: "看看哪台服务器磁盘占用最高，找出占用最大的目录并给出安全的清理建议。" },
  { icon: FileText, title: "日志分析", text: "分析 Nginx 最近的错误日志，总结主要问题和可能原因。" },
  { icon: KeyRound, title: "SSH 安全", text: "检查所有服务器的 SSH 安全配置和最近的暴力破解情况，给出加固建议。" },
  { icon: Brain, title: "你了解什么", text: "总结一下你目前记住的关于我的服务器环境和我的偏好的信息。" },
];

export default function Assistant() {
  const { params, settings, servers, go } = useApp();
  const ag = useAgent();
  const [input, setInput] = useState("");
  const [runCmd, setRunCmd] = useState<string | null>(null);
  const [showList, setShowList] = useState(false);
  const handled = useRef<Set<unknown>>(new Set());
  const running = ag.currentId ? !!ag.running[ag.currentId] : false;
  const reviewing = ag.currentId ? !!ag.reviewing[ag.currentId] : false;
  const { ref: scrollRef, onScroll, stick } = useStickToBottom([ag.messages, ag.live, ag.pending]);

  // 首次进入时打开最近的会话
  useEffect(() => {
    if (!ag.currentId && ag.sessions.length > 0 && !params.prompt && !params.new) void ag.open(ag.sessions[0].id);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [ag.sessions.length]);

  // 来自其他页面的提问
  useEffect(() => {
    const key = params.prompt ?? params.new;
    if (!key || handled.current.has(key)) return;
    handled.current.add(key);
    (async () => {
      if (params.newSession || params.new || !ag.currentId) await ag.create();
      if (params.prompt) await ag.send(params.prompt);
    })();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params]);

  const send = async (text?: string) => {
    const t = (text ?? input).trim();
    if (!t || running) return;
    setInput("");
    stick.current = true;
    const ok = await ag.send(t);
    if (!ok) setInput(t);
  };

  const cur = ag.sessions.find((s) => s.id === ag.currentId);
  const serverName = (id: string) => servers.find((s) => s.id === id)?.name;
  const empty = ag.messages.length === 0 && !running && !ag.loading;

  return (
    <div className="@container/ai relative flex h-full">
      {/* 会话列表：窗口较窄时收起，点标题栏左侧的按钮展开 */}
      {showList && <div className="absolute inset-0 z-10 bg-black/40 @[760px]/ai:hidden" onClick={() => setShowList(false)} />}
      <div className={cn("absolute inset-y-0 left-0 z-20 w-[250px] max-w-[80%] shrink-0 flex-col border-r border-line bg-bg2 shadow-2xl @[760px]/ai:static @[760px]/ai:flex @[760px]/ai:bg-bg2/40 @[760px]/ai:shadow-none", showList ? "flex" : "hidden")}>
        <div className="p-3">
          <Button
            variant="primary"
            className="w-full"
            icon={MessageSquarePlus}
            onClick={() => {
              setShowList(false);
              void ag.create();
            }}
          >
            新对话
          </Button>
        </div>
        <div className="min-h-0 flex-1 space-y-0.5 overflow-y-auto px-2 pb-3">
          {ag.sessions.length === 0 && <div className="px-3 py-6 text-center text-[12px] text-muted">还没有对话</div>}
          {ag.sessions.map((s) => (
            <div
              key={s.id}
              onClick={() => {
                setShowList(false);
                void ag.open(s.id);
              }}
              className={cn("group relative cursor-default rounded-[10px] px-3 py-2 transition-colors", s.id === ag.currentId ? "bg-accent/12" : "hover:bg-panel2")}
            >
              <div className="flex items-center gap-1.5">
                {ag.running[s.id] && <span className="h-1.5 w-1.5 shrink-0 animate-pulse rounded-full bg-accent" />}
                <span className="truncate text-[13px] text-fg">{s.title || "新对话"}</span>
              </div>
              <div className="mt-0.5 flex items-center gap-1.5 text-[11px] text-muted">
                <span className="shrink-0">{timeAgo(s.updatedAt)}</span>
                {s.serverId && (
                  <span className="inline-flex min-w-0 items-center gap-0.5" title="从这台服务器的终端里发起的对话">
                    <SquareTerminal size={10} className="shrink-0" />
                    <span className="truncate">{serverName(s.serverId) ?? "已删除的服务器"}</span>
                  </span>
                )}
                {s.learned > 0 && (
                  <span className="inline-flex shrink-0 items-center gap-0.5 text-accent">
                    <Brain size={10} /> {s.learned}
                  </span>
                )}
              </div>
              <button
                type="button"
                className="absolute top-2 right-2 hidden h-6 w-6 items-center justify-center rounded-md text-muted group-hover:flex hover:bg-bad/15 hover:text-bad"
                onClick={async (e) => {
                  e.stopPropagation();
                  if (await confirm({ title: "删除这个对话？", danger: true, confirmText: "删除" })) void ag.remove(s.id);
                }}
              >
                <Trash2 size={12} />
              </button>
            </div>
          ))}
        </div>
      </div>

      {/* 对话 */}
      <div className="flex min-w-0 flex-1 flex-col">
        <div className="flex h-[52px] shrink-0 items-center gap-2 border-b border-line px-3 @[760px]/ai:gap-3 @[760px]/ai:px-5">
          <IconButton icon={PanelLeft} size="sm" title="对话列表" className="@[760px]/ai:hidden" onClick={() => setShowList((v) => !v)} />
          <div className="min-w-0 flex-1">
            <div className="flex items-center gap-2">
              <span className="truncate text-[14px] font-semibold">{cur?.title || "AI 运维助手"}</span>
              {cur?.serverId && (
                <Badge icon={SquareTerminal} className="hidden shrink-0 @[560px]/ai:inline-flex" title="这个对话默认操作这台服务器">
                  {serverName(cur.serverId) ?? "已删除的服务器"}
                </Badge>
              )}
              {cur && (
                <IconButton
                  icon={Pencil}
                  size="sm"
                  tip="重命名"
                  onClick={() => {
                    const t = window.prompt("对话标题", cur.title);
                    if (t) void ag.rename(cur.id, t);
                  }}
                />
              )}
            </div>
          </div>
          {ag.approveAll && (
            <Badge tone="warn" className="hidden shrink-0 @[640px]/ai:inline-flex">
              本会话已全部允许变更
            </Badge>
          )}
          {ag.tokens.input > 0 && (
            <span className="hidden shrink-0 text-[11.5px] text-muted @[900px]/ai:inline">
              {compact(ag.tokens.input)} 输入 · {compact(ag.tokens.output)} 输出 tokens
            </span>
          )}
          {ag.currentId && (
            <Button size="sm" className="shrink-0" icon={reviewing ? undefined : Brain} loading={reviewing} disabled={reviewing || running || ag.messages.length === 0} onClick={() => ag.review()}>
              {reviewing ? "复盘中" : "学习复盘"}
            </Button>
          )}
          <button
            type="button"
            onClick={() => go("settings", { tab: "ai" })}
            className="hidden h-7 max-w-[200px] shrink-0 items-center gap-1.5 rounded-lg border border-line px-2 font-mono text-[11.5px] text-fg2 hover:border-line2 @[560px]/ai:flex"
          >
            <Bot size={12} className="shrink-0 text-accent" />
            <span className="truncate">{settings?.ai.model || "未配置模型"}</span>
          </button>
        </div>

        <div ref={scrollRef} onScroll={onScroll} className="min-h-0 flex-1 overflow-y-auto">
          {empty ? (
            <div className="mx-auto flex max-w-[760px] flex-col items-center px-4 pt-[6vh] pb-10 @[560px]/ai:px-6 @[560px]/ai:pt-[9vh]">
              <Logo size={56} />
              <h2 className="mt-5 text-center text-[22px] font-semibold tracking-tight">
                你好，我是你的<span className="grad-text">运维助手</span>
              </h2>
              <p className="mt-2 max-w-[560px] text-center text-[13px] leading-relaxed text-muted">我可以直接在你的服务器上排查问题、执行命令、管理服务和同步证书。只读命令会直接执行，有修改的操作会先征得你的同意。我会记住你的环境和偏好，并把解决问题的经验沉淀为技能，越用越顺手。</p>
              <div className="mt-8 grid w-full grid-cols-1 gap-3 @[560px]/ai:grid-cols-2">
                {quickPrompts.map((p) => (
                  <button key={p.title} type="button" onClick={() => send(p.text)} className="card group flex items-start gap-3 p-3.5 text-left transition-all hover:border-accent/40 hover:shadow-[var(--glow)]">
                    <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-accent/12 text-accent">
                      <p.icon size={15} />
                    </div>
                    <div className="min-w-0">
                      <div className="text-[13px] font-medium">{p.title}</div>
                      <div className="mt-0.5 line-clamp-2 text-[12px] text-muted">{p.text}</div>
                    </div>
                  </button>
                ))}
              </div>
            </div>
          ) : (
            <div className="mx-auto max-w-[880px] space-y-5 px-4 py-5 @[560px]/ai:px-6 @[560px]/ai:py-6">
              <ChatThread store={useAgent} onRun={(c) => setRunCmd(c)} />
            </div>
          )}
        </div>

        {/* 输入框 */}
        <div className="shrink-0 px-3 pb-3 @[560px]/ai:px-6 @[560px]/ai:pb-5">
          <div className="mx-auto max-w-[880px]">
            <Composer
              value={input}
              onChange={setInput}
              onSend={() => void send()}
              onStop={() => ag.stop()}
              running={running}
              placeholder={running ? "AI 正在处理…" : "描述你想做的事，例如：检查 web-01 为什么 502"}
              hint="Enter 发送 · Shift+Enter 换行 · 修改类操作会先征求你的同意"
            />
          </div>
        </div>
      </div>
      <QuickRunModal open={runCmd !== null} onClose={() => setRunCmd(null)} command={runCmd ?? ""} title="执行 AI 建议的命令" />
    </div>
  );
}
