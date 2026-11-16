import { useState } from "react";
import { Bot, FolderOpen, LoaderCircle, Minus, Plus, RefreshCw, Route, Search, SquareTerminal, X, Zap } from "lucide-react";
import { api } from "../lib/api";
import { cn } from "../lib/format";
import { useOpsAgent } from "../lib/agent";
import { toast, useApp } from "../lib/store";
import { openFilesWindow, useTermUI } from "../lib/term";
import { Button, Empty, IconButton, Kbd, StatusDot } from "../components/ui";
import { ServerAvatar } from "../components/widgets";
import { XTermView } from "../components/XTermView";
import { QuickPanel } from "../components/QuickCommands";
import { OpsPanel } from "../components/OpsPanel";

function QuickConnect({ onPick }: { onPick: (id: string) => void }) {
  const { servers, status } = useApp();
  const [q, setQ] = useState("");
  const list = servers.filter((s) => (s.name + s.host + s.group).toLowerCase().includes(q.toLowerCase()));
  return (
    <div className="w-[300px] max-w-[calc(100vw-32px)]">
      <div className="flex items-center gap-2 border-b border-line px-3 py-2">
        <Search size={14} className="text-muted" />
        <input autoFocus value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索服务器" className="h-6 min-w-0 flex-1 bg-transparent text-[13px] outline-none placeholder:text-muted" />
      </div>
      <div className="max-h-[min(320px,60vh)] overflow-y-auto p-1">
        {list.length === 0 && <div className="py-6 text-center text-[12.5px] text-muted">没有服务器</div>}
        {list.map((s) => (
          <button key={s.id} type="button" onClick={() => onPick(s.id)} className="flex h-10 w-full items-center gap-2.5 rounded-lg px-2 text-left hover:bg-panel3">
            <StatusDot status={status[s.id]?.status} />
            <span className="truncate text-[13px]">{s.name}</span>
            <span className="ml-auto truncate font-mono text-[11.5px] text-muted">{s.host}</span>
          </button>
        ))}
      </div>
    </div>
  );
}

export default function Terminals() {
  const { terms, activeTerm, setActiveTerm, closeTab, openTerminal, reconnectTab, servers, status, settings, loadSettings } = useApp();
  const { side, toggleSide, setSide } = useTermUI();
  const [picker, setPicker] = useState(false);
  const active = terms.find((t) => t.key === activeTerm) ?? terms[0];
  // 每台服务器上「AI 运维」对话的状态，显示在终端标签上
  const aiState = useOpsAgent((s) => {
    const out: Record<string, "running" | "waiting"> = {};
    for (const x of s.sessions) {
      if (!x.serverId || !s.running[x.id]) continue;
      out[x.serverId] = (s.waiting[x.id] ?? 0) > 0 ? "waiting" : (out[x.serverId] ?? "running");
    }
    return JSON.stringify(out);
  });
  const ai = JSON.parse(aiState) as Record<string, "running" | "waiting">;

  const changeFont = async (delta: number) => {
    if (!settings) return;
    const next = { ...settings, terminal: { ...settings.terminal, fontSize: Math.min(28, Math.max(10, settings.terminal.fontSize + delta)) } };
    try {
      await api.SaveSettings(next, "", false);
      await loadSettings();
    } catch (e) {
      toast.error("保存失败", e);
    }
  };

  if (terms.length === 0) {
    return (
      <div className="h-full overflow-y-auto">
        <div className="mx-auto max-w-[920px] px-4 pt-8 pb-6 @2xl:px-7 @2xl:pt-14">
          <Empty
            icon={SquareTerminal}
            title="打开一个终端"
            description={
              <span>
                选择下面的服务器开始会话，或在任意页面按 <Kbd>Ctrl</Kbd> <Kbd>K</Kbd> 搜索并连接。支持多标签、右键菜单、查找，旁边可以直接让 AI 帮你操作这台服务器。
              </span>
            }
          />
          <div className="grid grid-cols-[repeat(auto-fill,minmax(min(260px,100%),1fr))] gap-3">
            {servers.map((s) => (
              <button
                key={s.id}
                type="button"
                onClick={() => openTerminal(s.id)}
                className="card flex items-center gap-3 p-3.5 text-left transition-colors hover:border-accent/40"
              >
                <ServerAvatar name={s.name} size={36} />
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate text-[13.5px] font-medium">{s.name}</span>
                    <StatusDot status={status[s.id]?.status} />
                  </div>
                  <div className="truncate font-mono text-[11.5px] text-muted">
                    {s.username}@{s.host}:{s.port}
                  </div>
                </div>
                <SquareTerminal size={16} className="shrink-0 text-muted" />
              </button>
            ))}
          </div>
        </div>
      </div>
    );
  }

  // 工具栏按钮：窗口较窄时只显示图标
  const label = (text: string) => <span className="hidden @[720px]/term:inline">{text}</span>;

  return (
    <div className="@container/term flex h-full flex-col">
      <div className="flex h-[42px] shrink-0 items-center gap-1 border-b border-line bg-bg2 px-2">
        <div className="flex min-w-0 items-center gap-1 overflow-x-auto">
          {terms.map((t) => {
            const isActive = t.key === active?.key;
            const aiMark = ai[t.serverId];
            return (
              <div
                key={t.key}
                onClick={() => setActiveTerm(t.key)}
                onMouseDown={(e) => {
                  if (e.button === 1) {
                    e.preventDefault();
                    closeTab(t.key);
                  }
                }}
                className={cn(
                  "group flex h-[30px] max-w-[220px] shrink-0 items-center gap-2 rounded-lg border pr-1 pl-2.5 text-[12.5px] transition-colors",
                  isActive ? "border-line2 bg-panel text-fg shadow-sm" : "border-transparent text-muted hover:bg-panel2 hover:text-fg2",
                )}
              >
                {t.state === "connecting" ? (
                  <LoaderCircle size={12} className="shrink-0 animate-spin text-accent" />
                ) : (
                  <span className={cn("h-2 w-2 shrink-0 rounded-full", t.state === "open" ? "bg-ok" : "bg-muted/60")} />
                )}
                <span className="truncate">{t.title}</span>
                {aiMark && (
                  <span title={aiMark === "waiting" ? "AI 在等你批准一个操作" : "AI 正在这台服务器上处理"} className={cn("flex shrink-0 items-center", aiMark === "waiting" ? "text-warn" : "animate-pulse text-accent")}>
                    <Bot size={12} />
                  </span>
                )}
                <button
                  type="button"
                  onClick={(e) => {
                    e.stopPropagation();
                    closeTab(t.key);
                  }}
                  className="flex h-5 w-5 shrink-0 items-center justify-center rounded-md text-muted opacity-60 hover:bg-panel3 hover:text-fg group-hover:opacity-100"
                >
                  <X size={12} />
                </button>
              </div>
            );
          })}
        </div>
        {/* 新建按钮放在滚动容器之外：容器的 overflow 会把下拉列表裁掉 */}
        <div className="flex min-w-[28px] flex-1 items-center">
          <div className="relative">
            <IconButton icon={Plus} size="sm" tip="新建终端" tipPos="bottom" onClick={() => setPicker((v) => !v)} />
            {picker && (
              <>
                <div className="fixed inset-0 z-20" onClick={() => setPicker(false)} />
                <div className="absolute top-full left-0 z-30 mt-1 animate-pop rounded-xl border border-line2 bg-panel2 shadow-2xl">
                  <QuickConnect
                    onPick={(id) => {
                      setPicker(false);
                      openTerminal(id);
                    }}
                  />
                </div>
              </>
            )}
          </div>
        </div>
        <div className="flex shrink-0 items-center gap-0.5 pl-1">
          {active?.state === "closed" && (
            <>
              <Button size="sm" variant="soft" icon={RefreshCw} onClick={() => reconnectTab(active.key)} title="重新连接">
                {label("重新连接")}
              </Button>
              <Button size="sm" variant="ghost" icon={Route} onClick={() => useApp.getState().openRoute(active.serverId)} title="直连不通时，换一条线路连接：经跳板机，或经本地 / HTTP / SOCKS5 代理">
                {label("换线路")}
              </Button>
            </>
          )}
          <Button size="sm" variant="ghost" icon={FolderOpen} onClick={() => active && void openFilesWindow(active.serverId)} title="在独立窗口里打开这台服务器的文件管理（上传、下载、在线编辑，支持拖拽上传）">
            {label("文件")}
          </Button>
          <Button size="sm" variant={side === "ai" ? "soft" : "ghost"} icon={Bot} onClick={() => toggleSide("ai")} title="和 AI 对话，让它帮你操作当前这台服务器">
            {label("AI 运维")}
          </Button>
          <Button size="sm" variant={side === "quick" ? "soft" : "ghost"} icon={Zap} onClick={() => toggleSide("quick")} title="显示 / 收起快捷命令面板">
            {label("快捷命令")}
          </Button>
          <div className="hidden items-center @[560px]/term:flex">
            <span className="mx-1 h-4 w-px bg-line2" />
            <IconButton icon={Minus} size="sm" tip="缩小字号" tipPos="bottom" onClick={() => changeFont(-1)} />
            <span className="w-7 text-center text-[11.5px] text-muted">{settings?.terminal.fontSize ?? 14}</span>
            <IconButton icon={Plus} size="sm" tip="放大字号" tipPos="bottom" onClick={() => changeFont(1)} />
          </div>
        </div>
      </div>
      <div className="relative flex min-h-0 flex-1">
        <div className="relative min-h-0 min-w-0 flex-1">
          {terms.map((t) => (
            <div key={t.key} className={cn("absolute inset-0", t.key === active?.key ? "visible" : "invisible")}>
              <XTermView tab={t} active={t.key === active?.key} />
            </div>
          ))}
        </div>
        {/* 右侧面板：窗口够宽时并排显示，窗口较窄时盖在终端上面 */}
        {side && (
          <div className="absolute inset-y-0 right-0 z-10 flex max-w-full justify-end shadow-2xl @max-[560px]/term:left-0 @[820px]/term:static @[820px]/term:z-auto @[820px]/term:shadow-none">
            {side === "quick" ? <QuickPanel onClose={() => setSide(null)} /> : <OpsPanel tab={active} onClose={() => setSide(null)} />}
          </div>
        )}
      </div>
    </div>
  );
}
