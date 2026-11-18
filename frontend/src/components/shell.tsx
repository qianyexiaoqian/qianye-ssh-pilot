import { useEffect, useMemo, useRef, useState } from "react";
import {
  Activity,
  Bot,
  Brain,
  ChevronsUpDown,
  CircleCheck,
  CircleX,
  FileCode,
  FolderOpen,
  Info,
  KeyRound,
  LayoutDashboard,
  Layers,
  Lock,
  Maximize2,
  Minimize2,
  Menu as MenuIcon,
  Minus,
  Moon,
  PanelLeft,
  Search,
  Server,
  Settings,
  ShieldCheck,
  SquareTerminal,
  Stethoscope,
  Sun,
  TriangleAlert,
  X,
  Zap,
  type LucideIcon,
} from "lucide-react";
import { api, runtime } from "../lib/api";
import { cn } from "../lib/format";
import { useApp, useConfirm, useToasts, type Page } from "../lib/store";
import { useAgent } from "../lib/agent";
import { useQuick } from "../lib/quick";
import { useViewport } from "../lib/viewport";
import { useWindowState } from "../lib/chrome";
import type { Server as ServerModel } from "../lib/api";
import { Button, Kbd, StatusDot } from "./ui";

export const PRODUCT = "QianYe SSH Pilot";
export const AUTHOR = "Qian Ye";

export function Logo({ size = 26 }: { size?: number }) {
  return (
    <svg width={size} height={size} viewBox="0 0 64 64" className="shrink-0">
      <defs>
        <linearGradient id="lg-a" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#9a8bff" />
          <stop offset="0.55" stopColor="#6a5af9" />
          <stop offset="1" stopColor="#22c7e6" />
        </linearGradient>
      </defs>
      <rect x="2" y="2" width="60" height="60" rx="16" fill="url(#lg-a)" />
      <rect x="2.5" y="2.5" width="59" height="59" rx="15.5" fill="none" stroke="white" strokeOpacity="0.25" />
      <path d="M17 22 L29 32 L17 42" fill="none" stroke="white" strokeWidth="5.5" strokeLinecap="round" strokeLinejoin="round" />
      <rect x="32" y="39" width="16" height="5.5" rx="2.75" fill="white" />
      <circle cx="47" cy="20" r="4" fill="white" fillOpacity="0.9" />
    </svg>
  );
}

interface NavItem {
  page: Page;
  label: string;
  icon: LucideIcon;
}

const nav: { title: string; items: NavItem[] }[] = [
  {
    title: "概览",
    items: [
      { page: "dashboard", label: "仪表盘", icon: LayoutDashboard },
      { page: "monitor", label: "实时监控", icon: Activity },
    ],
  },
  {
    title: "基础设施",
    items: [
      { page: "servers", label: "服务器", icon: Server },
      { page: "terminal", label: "终端", icon: SquareTerminal },
      { page: "files", label: "文件管理", icon: FolderOpen },
      { page: "keys", label: "密钥管理", icon: KeyRound },
      { page: "certs", label: "证书中心", icon: ShieldCheck },
      { page: "services", label: "服务管理", icon: Layers },
    ],
  },
  {
    title: "自动化",
    items: [
      { page: "scripts", label: "脚本库", icon: FileCode },
      { page: "inspect", label: "智能巡检", icon: Stethoscope },
    ],
  },
  {
    title: "智能体",
    items: [
      { page: "assistant", label: "AI 助手", icon: Bot },
      { page: "brain", label: "记忆与技能", icon: Brain },
    ],
  },
];

export const pageTitles: Record<Page, string> = {
  dashboard: "仪表盘",
  monitor: "实时监控",
  servers: "服务器",
  terminal: "终端",
  files: "文件管理",
  keys: "密钥管理",
  certs: "证书中心",
  services: "服务管理",
  scripts: "脚本库",
  inspect: "智能巡检",
  assistant: "AI 助手",
  brain: "记忆与技能",
  settings: "设置",
};

export function Sidebar() {
  const { page, go, terms, sidebarCollapsed, settings } = useApp();
  const { tier, height, navOpen, setNavOpen } = useViewport();
  const running = useAgent((s) => Object.values(s.running).some(Boolean));
  // 窗口较窄时自动收成图标栏；更窄时隐藏，从标题栏的按钮展开成抽屉
  const drawer = tier === "narrow";
  const collapsed = drawer ? false : tier === "mid" ? true : sidebarCollapsed;
  const item = (it: NavItem) => {
    const active = page === it.page;
    const badge = it.page === "terminal" && terms.length > 0 ? terms.length : undefined;
    return (
      <button
        key={it.page}
        type="button"
        onClick={() => {
          setNavOpen(false);
          go(it.page);
        }}
        data-tip={collapsed ? it.label : undefined}
        data-tip-pos="right"
        className={cn(
          "group relative flex h-9 w-full items-center gap-3 rounded-[10px] text-[13px] font-medium transition-all",
          collapsed ? "justify-center px-0" : "px-3",
          active ? "bg-accent/12 text-fg" : "text-fg2/80 hover:bg-panel2 hover:text-fg",
        )}
      >
        {active && <span className="absolute top-2 bottom-2 left-0 w-[3px] rounded-full grad-accent" />}
        <it.icon size={17} className={cn("shrink-0 transition-colors", active ? "text-accent" : "text-muted group-hover:text-fg2")} />
        {!collapsed && <span className="truncate">{it.label}</span>}
        {!collapsed && badge !== undefined && <span className="ml-auto rounded-md bg-panel3 px-1.5 text-[11px] text-fg2">{badge}</span>}
        {it.page === "assistant" && running && <span className={cn("h-2 w-2 animate-pulse rounded-full bg-accent", collapsed ? "absolute top-1.5 right-2" : "ml-auto")} />}
      </button>
    );
  };
  const model = settings?.ai.model ?? "";
  if (drawer && !navOpen) return null;
  return (
    <>
      {drawer && <div className="fixed inset-0 top-[40px] z-40 animate-fade-in bg-black/45" onMouseDown={() => setNavOpen(false)} />}
      <aside
        className={cn(
          "flex shrink-0 flex-col border-r border-line bg-bg2 transition-[width] duration-200",
          collapsed ? "w-[64px]" : "w-[216px]",
          drawer ? "fixed top-[40px] bottom-0 left-0 z-40 shadow-2xl" : "h-full",
        )}
      >
      <nav className="min-h-0 flex-1 space-y-5 overflow-y-auto px-2.5 pt-4 pb-2">
        {nav.map((g) => (
          <div key={g.title}>
            {!collapsed && <div className="mb-1.5 px-3 text-[11px] font-semibold tracking-wider text-muted/70">{g.title}</div>}
            <div className="space-y-0.5">{g.items.map(item)}</div>
          </div>
        ))}
      </nav>
      <div className="shrink-0 space-y-1 border-t border-line p-2.5">
        {!collapsed && height >= 600 && (
          <button
            type="button"
            onClick={() => {
              setNavOpen(false);
              go("settings", { tab: "ai" });
            }}
            className="mb-1 flex w-full items-center gap-2.5 rounded-[10px] border border-line bg-panel px-2.5 py-2 text-left transition-colors hover:border-line2"
          >
            <div className="flex h-7 w-7 items-center justify-center rounded-lg grad-accent text-white">
              <Bot size={14} />
            </div>
            <div className="min-w-0 flex-1">
              <div className="text-[11px] text-muted">AI 模型</div>
              <div className="truncate font-mono text-[11.5px] text-fg2">{model || "未配置"}</div>
            </div>
            <ChevronsUpDown size={13} className="text-muted" />
          </button>
        )}
        {item({ page: "settings", label: "设置", icon: Settings })}
      </div>
      </aside>
    </>
  );
}

function ThemeButton() {
  const { theme, setTheme } = useApp();
  return (
    <button
      type="button"
      onClick={() => setTheme(theme === "dark" ? "light" : "dark")}
      className="mr-1 flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-muted hover:bg-panel2 hover:text-fg"
      data-tip={theme === "dark" ? "浅色模式" : "深色模式"}
      data-tip-pos="bottom"
    >
      {theme === "dark" ? <Sun size={15} /> : <Moon size={15} />}
    </button>
  );
}

/** 设置了启动密码时显示：点一下把界面锁上 */
function LockButton() {
  const lock = useApp((s) => s.lock);
  const setLock = useApp((s) => s.setLock);
  if (!lock?.enabled) return null;
  return (
    <button
      type="button"
      onClick={() =>
        api
          .LockNow()
          .then(setLock)
          .catch(() => {})
      }
      className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-muted hover:bg-panel2 hover:text-fg"
      data-tip="锁定"
      data-tip-pos="bottom"
    >
      <Lock size={15} />
    </button>
  );
}

export function WindowButtons() {
  const maximised = useWindowState((s) => s.maximised);
  return (
    <>
      <WinButton onClick={() => runtime.WindowMinimise()} icon={<Minus size={15} />} title="最小化" />
      <WinButton onClick={() => runtime.WindowToggleMaximise()} icon={maximised ? <Minimize2 size={13} /> : <Maximize2 size={13} />} title={maximised ? "向下还原（还原后可以拖动窗口边缘调整大小）" : "最大化"} />
      <WinButton onClick={() => runtime.Quit()} icon={<X size={16} />} title="关闭" danger />
    </>
  );
}

export function TitleBar({ onSearch }: { onSearch: () => void }) {
  const { page, toggleSidebar, sidebarCollapsed } = useApp();
  const { tier, width, navOpen, setNavOpen } = useViewport();
  // 侧栏展开时，标题栏左侧与侧栏同宽，让页面标题和内容左边缘对齐
  const aligned = tier === "wide" && !sidebarCollapsed;
  return (
    <header className="drag flex h-[40px] shrink-0 items-center border-b border-line bg-bg2" onDoubleClick={() => runtime.WindowToggleMaximise()}>
      <div className={cn("flex shrink-0 items-center gap-2.5 pl-3.5", aligned ? "w-[216px]" : "pr-1")}>
        <Logo size={22} />
        {width >= 560 && <span className="text-[13px] font-semibold tracking-tight whitespace-nowrap">{PRODUCT}</span>}
      </div>
      <div className="no-drag flex min-w-0 items-center gap-1 pl-1" onDoubleClick={(e) => e.stopPropagation()}>
        {tier === "narrow" ? (
          <button type="button" onClick={() => setNavOpen(!navOpen)} className={cn("flex h-7 w-7 shrink-0 items-center justify-center rounded-lg hover:bg-panel2 hover:text-fg", navOpen ? "bg-panel2 text-fg" : "text-muted")} title="导航菜单">
            <MenuIcon size={15} />
          </button>
        ) : (
          tier === "wide" && (
            <button type="button" onClick={toggleSidebar} className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-muted hover:bg-panel2 hover:text-fg" data-tip="折叠侧栏" data-tip-pos="bottom">
              <PanelLeft size={15} />
            </button>
          )
        )}
        <span className="truncate pl-1 text-[12.5px] text-muted">{pageTitles[page]}</span>
      </div>
      <div className="flex min-w-0 flex-1 justify-center px-2">
        {tier !== "narrow" && (
          <button
            type="button"
            onClick={onSearch}
            onDoubleClick={(e) => e.stopPropagation()}
            className="no-drag flex h-[26px] w-full max-w-[360px] min-w-0 items-center gap-2 rounded-lg border border-line bg-panel px-2.5 text-[12px] text-muted transition-colors hover:border-line2 hover:text-fg2"
          >
            <Search size={13} className="shrink-0" />
            <span className="min-w-0 flex-1 truncate text-left">搜索服务器、页面或操作…</span>
            <Kbd>Ctrl</Kbd>
            <Kbd>K</Kbd>
          </button>
        )}
      </div>
      <div className="no-drag flex shrink-0 items-center" onDoubleClick={(e) => e.stopPropagation()}>
        {tier === "narrow" && (
          <button type="button" onClick={onSearch} className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-muted hover:bg-panel2 hover:text-fg" title="搜索（Ctrl+K）">
            <Search size={15} />
          </button>
        )}
        <LockButton />
        <ThemeButton />
        <WindowButtons />
      </div>
    </header>
  );
}

/** 文件管理独立窗口的标题栏 */
export function FilesTitleBar({ server }: { server: ServerModel | undefined }) {
  const width = useViewport((s) => s.width);
  return (
    <header className="drag flex h-[40px] shrink-0 items-center border-b border-line bg-bg2" onDoubleClick={() => runtime.WindowToggleMaximise()}>
      <div className="flex min-w-0 flex-1 items-center gap-2.5 pl-3.5">
        <Logo size={22} />
        <span className="shrink-0 text-[13px] font-semibold tracking-tight">文件管理</span>
        {server && (
          <span className="flex min-w-0 items-center gap-2 text-[12.5px]">
            <span className="h-3.5 w-px shrink-0 bg-line2" />
            <span className="truncate font-medium text-fg2">{server.name}</span>
            {width >= 620 && (
              <span className="truncate font-mono text-[11.5px] text-muted" title={`以 ${server.username} 的身份访问`}>
                {server.username}@{server.host}
              </span>
            )}
          </span>
        )}
      </div>
      <div className="no-drag flex shrink-0 items-center" onDoubleClick={(e) => e.stopPropagation()}>
        <ThemeButton />
        <WindowButtons />
      </div>
    </header>
  );
}

function WinButton({ onClick, icon, danger, title }: { onClick: () => void; icon: React.ReactNode; danger?: boolean; title: string }) {
  return (
    <button type="button" onClick={onClick} title={title} className={cn("flex h-[40px] w-[46px] shrink-0 items-center justify-center text-fg2 transition-colors", danger ? "hover:bg-[#e81123] hover:text-white" : "hover:bg-panel3")}>
      {icon}
    </button>
  );
}

export function Toaster() {
  const { toasts, dismiss } = useToasts();
  return (
    <div className="pointer-events-none fixed right-3 bottom-3 z-[70] flex w-[min(360px,calc(100vw-24px))] flex-col gap-2 sm:right-5 sm:bottom-5">
      {toasts.map((t) => {
        const Icon = t.tone === "ok" ? CircleCheck : t.tone === "bad" ? CircleX : t.tone === "warn" ? TriangleAlert : Info;
        const color = { ok: "text-ok", bad: "text-bad", warn: "text-warn", info: "text-info" }[t.tone];
        return (
          <div key={t.id} className="pointer-events-auto flex animate-slide-up items-start gap-3 rounded-xl border border-line2 bg-panel2 px-3.5 py-3 shadow-2xl">
            <Icon size={17} className={cn("mt-0.5 shrink-0", color)} />
            <div className="min-w-0 flex-1">
              <div className="text-[13px] font-medium text-fg">{t.title}</div>
              {t.message && <div className="mt-0.5 line-clamp-4 text-[12px] leading-relaxed break-words text-muted">{t.message}</div>}
            </div>
            <button type="button" className="text-muted hover:text-fg" onClick={() => dismiss(t.id)}>
              <X size={14} />
            </button>
          </div>
        );
      })}
    </div>
  );
}

export function ConfirmHost() {
  const { current, close } = useConfirm();
  useEffect(() => {
    if (!current) return;
    // 对话框里的按钮不自动获得焦点：弹出对话框的那次回车（例如在输入框里按回车提交）
    // 还没有松开，焦点落在「确定」按钮上会被这次按键直接触发。这里统一由下面的监听处理键盘操作。
    (document.activeElement as HTMLElement | null)?.blur?.();
    const h = (e: KeyboardEvent) => {
      // 忽略弹出之前就按下、以及按住不放产生的重复按键，必须是一次新的按键才算确认
      if (e.repeat || e.timeStamp - current.at < 250) return;
      if (e.key === "Escape") close(false);
      if (e.key === "Enter") {
        e.preventDefault();
        close(true);
      }
    };
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, [current, close]);
  if (!current) return null;
  return (
    <div className="fixed inset-0 z-[80] flex items-center justify-center p-3 sm:p-6">
      <div className="absolute inset-0 animate-fade-in bg-black/55 backdrop-blur-[3px]" onMouseDown={() => close(false)} />
      <div className="card relative max-h-full w-full max-w-[420px] animate-pop overflow-y-auto !rounded-[18px] border-line2 p-5">
        <div className="flex items-start gap-3">
          <div className={cn("flex h-9 w-9 shrink-0 items-center justify-center rounded-xl", current.danger ? "bg-bad/12 text-bad" : "bg-accent/12 text-accent")}>
            {current.danger ? <TriangleAlert size={18} /> : <Info size={18} />}
          </div>
          <div className="min-w-0 flex-1">
            <div className="text-[15px] font-semibold">{current.title}</div>
            {current.message && <div className="mt-1.5 text-[13px] leading-relaxed whitespace-pre-line text-muted">{current.message}</div>}
          </div>
        </div>
        <div className="mt-5 flex justify-end gap-2">
          <Button variant="ghost" onClick={() => close(false)}>
            取消
          </Button>
          <Button variant={current.danger ? "danger" : "primary"} onClick={() => close(true)}>
            {current.confirmText ?? "确定"}
          </Button>
        </div>
      </div>
    </div>
  );
}

interface PaletteItem {
  id: string;
  label: string;
  hint?: string;
  icon: LucideIcon;
  run: () => void;
  status?: string;
}

export function CommandPalette({ open, onClose }: { open: boolean; onClose: () => void }) {
  const { servers, status, go, openTerminal } = useApp();
  const quick = useQuick((s) => s.commands);
  const [q, setQ] = useState("");
  const [sel, setSel] = useState(0);
  const inputRef = useRef<HTMLInputElement>(null);
  useEffect(() => {
    if (open) {
      setQ("");
      setSel(0);
      setTimeout(() => inputRef.current?.focus(), 10);
    }
  }, [open]);
  const items = useMemo<PaletteItem[]>(() => {
    const pages: PaletteItem[] = (Object.keys(pageTitles) as Page[]).map((p) => ({
      id: "page-" + p,
      label: "前往 · " + pageTitles[p],
      icon: LayoutDashboard,
      run: () => go(p),
    }));
    const conn: PaletteItem[] = servers.map((s) => ({
      id: "srv-" + s.id,
      label: s.name,
      hint: `${s.username}@${s.host}:${s.port}  ·  打开终端`,
      icon: SquareTerminal,
      status: status[s.id]?.status,
      run: () => openTerminal(s.id),
    }));
    const actions: PaletteItem[] = [
      { id: "act-ai", label: "新建 AI 对话", icon: Bot, run: () => go("assistant", { new: Date.now() }) },
      { id: "act-inspect", label: "开始智能巡检", icon: Stethoscope, run: () => go("inspect", { run: Date.now() }) },
      { id: "act-add", label: "添加服务器", icon: Server, run: () => go("servers", { add: Date.now() }) },
      { id: "act-cert", label: "导入证书", icon: ShieldCheck, run: () => go("certs", { import: Date.now() }) },
      { id: "act-quick", label: "添加快捷命令", icon: Zap, run: () => useQuick.getState().edit({}) },
    ];
    // 快捷命令：有已连接的终端时发送到当前终端，否则选择服务器批量执行
    const cmds: PaletteItem[] = quick.map((c) => ({
      id: "quick-" + c.id,
      label: "快捷命令 · " + c.name,
      hint: c.command.split("\n")[0],
      icon: Zap,
      run: () => void useQuick.getState().use(c),
    }));
    const kw = q.trim().toLowerCase();
    // 没有输入时不列出快捷命令，避免把服务器和页面挤到后面
    if (!kw) return [...conn, ...actions, ...pages].slice(0, 40);
    return [...conn, ...actions, ...pages, ...cmds].filter((i) => (i.label + " " + (i.hint ?? "")).toLowerCase().includes(kw)).slice(0, 40);
  }, [q, servers, status, go, openTerminal, quick]);
  if (!open) return null;
  const run = (i: PaletteItem) => {
    onClose();
    i.run();
  };
  return (
    <div className="fixed inset-0 z-[75] flex justify-center px-3 pt-[min(12vh,96px)] pb-3">
      <div className="absolute inset-0 animate-fade-in bg-black/50 backdrop-blur-[2px]" onMouseDown={onClose} />
      <div className="card relative flex h-fit max-h-full w-full max-w-[600px] animate-pop flex-col overflow-hidden !rounded-[18px] border-line2">
        <div className="flex shrink-0 items-center gap-3 border-b border-line px-4">
          <Search size={17} className="text-muted" />
          <input
            ref={inputRef}
            value={q}
            onChange={(e) => {
              setQ(e.target.value);
              setSel(0);
            }}
            onKeyDown={(e) => {
              if (e.key === "Escape") onClose();
              if (e.key === "ArrowDown") {
                e.preventDefault();
                setSel((v) => Math.min(v + 1, items.length - 1));
              }
              if (e.key === "ArrowUp") {
                e.preventDefault();
                setSel((v) => Math.max(v - 1, 0));
              }
              if (e.key === "Enter" && items[sel]) run(items[sel]);
            }}
            placeholder="输入服务器名称快速连接，或搜索页面与操作"
            className="h-[52px] min-w-0 flex-1 bg-transparent text-[14px] outline-none placeholder:text-muted"
          />
          <Kbd>Esc</Kbd>
        </div>
        <div className="max-h-[420px] min-h-0 overflow-y-auto p-1.5">
          {items.length === 0 && <div className="py-10 text-center text-[13px] text-muted">没有匹配的结果</div>}
          {items.map((i, idx) => (
            <button
              key={i.id}
              type="button"
              onMouseEnter={() => setSel(idx)}
              onClick={() => run(i)}
              className={cn("flex h-11 w-full items-center gap-3 rounded-[10px] px-3 text-left", idx === sel ? "bg-accent/12" : "")}
            >
              <i.icon size={16} className={idx === sel ? "text-accent" : "text-muted"} />
              <span className="shrink-0 truncate text-[13px] text-fg" style={{ maxWidth: "70%" }}>
                {i.label}
              </span>
              {i.status && <StatusDot status={i.status} />}
              {i.hint && <span className="ml-auto min-w-0 truncate font-mono text-[11.5px] text-muted">{i.hint}</span>}
            </button>
          ))}
        </div>
      </div>
    </div>
  );
}

