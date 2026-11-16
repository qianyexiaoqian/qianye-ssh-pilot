import { useCallback, useEffect, useRef, useState } from "react";
import { Terminal, type ITheme } from "@xterm/xterm";
import { FitAddon } from "@xterm/addon-fit";
import { SearchAddon, type ISearchOptions } from "@xterm/addon-search";
import { WebLinksAddon } from "@xterm/addon-web-links";
import { CaseSensitive, ChevronDown, ChevronUp, ClipboardPaste, Copy, Eraser, Search, Sparkles, TextSelect, X } from "lucide-react";
import { api, errText, on, runtime } from "../lib/api";
import { cn } from "../lib/format";
import { comboOf, isPlainCtrl } from "../lib/hotkey";
import { toast, useApp, type TermTab } from "../lib/store";
import { useTermUI } from "../lib/term";
import { PopMenu, type PopItem } from "./ui";

export const termThemes: Record<string, ITheme & { label: string }> = {
  pilot: {
    label: "Pilot 暗夜",
    background: "#0c0d13",
    foreground: "#d9dcea",
    cursor: "#8b7bff",
    cursorAccent: "#0c0d13",
    selectionBackground: "#7c6cff55",
    black: "#1c1f2b",
    red: "#ff6b81",
    green: "#5be49b",
    yellow: "#f9d47a",
    blue: "#7aa2ff",
    magenta: "#c99bff",
    cyan: "#4fd6e8",
    white: "#d9dcea",
    brightBlack: "#5b6178",
    brightRed: "#ff8a9b",
    brightGreen: "#86efb5",
    brightYellow: "#fde59a",
    brightBlue: "#a1bdff",
    brightMagenta: "#dcbcff",
    brightCyan: "#86e8f4",
    brightWhite: "#ffffff",
  },
  midnight: {
    label: "午夜蓝",
    background: "#0b1220",
    foreground: "#cbd5e1",
    cursor: "#38bdf8",
    selectionBackground: "#38bdf855",
    black: "#1e293b",
    red: "#f87171",
    green: "#4ade80",
    yellow: "#facc15",
    blue: "#60a5fa",
    magenta: "#c084fc",
    cyan: "#22d3ee",
    white: "#e2e8f0",
    brightBlack: "#475569",
    brightRed: "#fca5a5",
    brightGreen: "#86efac",
    brightYellow: "#fde047",
    brightBlue: "#93c5fd",
    brightMagenta: "#d8b4fe",
    brightCyan: "#67e8f9",
    brightWhite: "#f8fafc",
  },
  paper: {
    label: "纸白",
    background: "#fbfbfd",
    foreground: "#1f2937",
    cursor: "#6553f5",
    selectionBackground: "#6553f533",
    black: "#1f2937",
    red: "#dc2626",
    green: "#15803d",
    yellow: "#a16207",
    blue: "#2563eb",
    magenta: "#9333ea",
    cyan: "#0e7490",
    white: "#d1d5db",
    brightBlack: "#6b7280",
    brightRed: "#ef4444",
    brightGreen: "#16a34a",
    brightYellow: "#ca8a04",
    brightBlue: "#3b82f6",
    brightMagenta: "#a855f7",
    brightCyan: "#0891b2",
    brightWhite: "#111827",
  },
};

// 已打开的终端实例（按标签 key），供快捷命令面板聚焦、读取屏幕内容
const liveTerms = new Map<string, Terminal>();

export function focusTerm(key: string) {
  liveTerms.get(key)?.focus();
}

/** 读取终端里选中的文本；没有选中时取光标之前的最近若干行 */
export function readTermText(key: string, maxLines = 80): string {
  const term = liveTerms.get(key);
  if (!term) return "";
  const sel = term.getSelection();
  if (sel.trim()) return sel.trim();
  const buf = term.buffer.active;
  const end = buf.baseY + buf.cursorY;
  const lines: string[] = [];
  for (let i = Math.max(0, end - maxLines * 2); i <= end; i++) {
    const line = buf.getLine(i);
    if (!line) continue;
    const text = line.translateToString(true);
    // 自动折行的内容接回上一行
    if (line.isWrapped && lines.length) lines[lines.length - 1] += text;
    else lines.push(text);
  }
  return lines.slice(-maxLines).join("\n").replace(/\n{3,}/g, "\n\n").trim();
}

/** 把一段文字填进终端的输入行（不回车），并让终端获得焦点 */
export function typeIntoTerm(key: string, text: string): boolean {
  const term = liveTerms.get(key);
  if (!term) return false;
  term.paste(text);
  term.focus();
  return true;
}

function b64ToBytes(b64: string): Uint8Array {
  const bin = atob(b64);
  const out = new Uint8Array(bin.length);
  for (let i = 0; i < bin.length; i++) out[i] = bin.charCodeAt(i);
  return out;
}

// 查找时的高亮颜色（当前匹配用强调色，其余匹配用半透明底色）
const searchDecorations: ISearchOptions["decorations"] = {
  matchBackground: "#5b4bd6",
  matchBorder: "#7c6cff",
  matchOverviewRuler: "#7c6cff",
  activeMatchBackground: "#f9d47a",
  activeMatchBorder: "#f9d47a",
  activeMatchColorOverviewRuler: "#f9d47a",
};

function FindBar({ search, initial, onClose }: { search: SearchAddon; initial: string; onClose: () => void }) {
  const [q, setQ] = useState(initial);
  const [caseSensitive, setCaseSensitive] = useState(false);
  const [result, setResult] = useState<{ index: number; count: number } | null>(null);
  const inputRef = useRef<HTMLInputElement>(null);
  const opts = useCallback((incremental = false): ISearchOptions => ({ caseSensitive, incremental, decorations: searchDecorations }), [caseSensitive]);

  useEffect(() => {
    inputRef.current?.select();
    const d = search.onDidChangeResults((r) => setResult({ index: r.resultIndex, count: r.resultCount }));
    return () => {
      d.dispose();
      search.clearDecorations();
    };
  }, [search]);
  // 输入或切换大小写后从当前位置重新查找
  const lastCase = useRef(caseSensitive);
  useEffect(() => {
    if (!q) {
      search.clearDecorations();
      setResult(null);
      return;
    }
    // 查找选项变了而关键词没变时，插件不会重新标记匹配项，需要先清掉旧的
    if (lastCase.current !== caseSensitive) {
      lastCase.current = caseSensitive;
      search.clearDecorations();
    }
    search.findNext(q, opts(true));
  }, [q, opts, search, caseSensitive]);

  const step = (dir: 1 | -1) => {
    if (!q) return;
    if (dir === 1) search.findNext(q, opts());
    else search.findPrevious(q, opts());
  };
  return (
    <div className="absolute top-2 right-4 z-10 flex h-9 max-w-[calc(100%-24px)] animate-pop items-center gap-1 rounded-xl border border-line2 bg-panel2 pr-1 pl-2.5 shadow-2xl" data-find-bar>
      <Search size={13} className="shrink-0 text-muted" />
      <input
        ref={inputRef}
        autoFocus
        value={q}
        onChange={(e) => setQ(e.target.value)}
        onKeyDown={(e) => {
          if (e.key === "Enter") {
            e.preventDefault();
            step(e.shiftKey ? -1 : 1);
          } else if (e.key === "Escape") {
            e.preventDefault();
            onClose();
          }
        }}
        placeholder="在终端里查找"
        className="h-7 w-[180px] min-w-0 bg-transparent text-[12.5px] text-fg outline-none placeholder:text-muted"
      />
      <span className="w-[52px] shrink-0 text-center text-[11.5px] text-muted" style={{ fontVariantNumeric: "tabular-nums" }}>
        {!q ? "" : !result || result.count === 0 ? "无结果" : result.index < 0 ? `共 ${result.count}` : `${result.index + 1} / ${result.count}`}
      </span>
      <button type="button" title="区分大小写" onClick={() => setCaseSensitive((v) => !v)} className={cn("flex h-7 w-7 shrink-0 items-center justify-center rounded-lg", caseSensitive ? "bg-accent/15 text-accent" : "text-muted hover:bg-panel3 hover:text-fg")}>
        <CaseSensitive size={15} />
      </button>
      <button type="button" title="上一个（Shift+Enter）" onClick={() => step(-1)} className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-muted hover:bg-panel3 hover:text-fg">
        <ChevronUp size={15} />
      </button>
      <button type="button" title="下一个（Enter）" onClick={() => step(1)} className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-muted hover:bg-panel3 hover:text-fg">
        <ChevronDown size={15} />
      </button>
      <button type="button" title="关闭（Esc）" onClick={onClose} className="flex h-7 w-7 shrink-0 items-center justify-center rounded-lg text-muted hover:bg-panel3 hover:text-fg">
        <X size={14} />
      </button>
    </div>
  );
}

export function XTermView({ tab, active }: { tab: TermTab; active: boolean }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const termRef = useRef<Terminal | null>(null);
  const fitRef = useRef<FitAddon | null>(null);
  const searchRef = useRef<SearchAddon | null>(null);
  const idRef = useRef<string | null>(null);
  const settings = useApp((s) => s.settings?.terminal);
  const patchTerm = useApp((s) => s.patchTerm);
  const [menu, setMenu] = useState<{ x: number; y: number; hasSel: boolean } | null>(null);
  const [find, setFind] = useState<{ initial: string } | null>(null);
  // 终端实例里的事件处理函数通过这个引用调用最新的操作
  const actions = useRef<{ copy: () => boolean; paste: () => void; openFind: () => void }>({ copy: () => false, paste: () => {}, openFind: () => {} });

  actions.current = {
    copy: () => {
      const sel = termRef.current?.getSelection();
      if (sel) runtime.ClipboardSetText(sel);
      return !!sel;
    },
    paste: () => {
      void runtime.ClipboardGetText().then((text) => {
        // 交给 xterm 粘贴：统一换行，并在 Shell 开启了“括号粘贴”时避免多行内容被立刻执行
        if (text && idRef.current) termRef.current?.paste(text);
      });
    },
    openFind: () => {
      const sel = termRef.current?.getSelection() ?? "";
      setFind({ initial: sel && !sel.includes("\n") ? sel.trim() : "" });
    },
  };
  const closeFind = () => {
    setFind(null);
    termRef.current?.focus();
  };

  // 创建终端并连接（seq 变化时重新连接）
  useEffect(() => {
    const host = hostRef.current;
    if (!host) return;
    const theme = termThemes[settings?.theme ?? "pilot"] ?? termThemes.pilot;
    const term = new Terminal({
      fontFamily: settings?.fontFamily || 'Consolas, "Microsoft YaHei UI", monospace',
      fontSize: settings?.fontSize ?? 14,
      lineHeight: 1.18,
      cursorBlink: settings?.cursorBlink ?? true,
      cursorStyle: "bar",
      scrollback: settings?.scrollback ?? 5000,
      theme,
      allowProposedApi: true,
      macOptionIsMeta: true,
      rightClickSelectsWord: false,
    });
    const fit = new FitAddon();
    const search = new SearchAddon();
    term.loadAddon(fit);
    term.loadAddon(search);
    term.loadAddon(new WebLinksAddon((_e, uri) => runtime.BrowserOpenURL(uri)));
    term.open(host);
    termRef.current = term;
    fitRef.current = fit;
    searchRef.current = search;
    liveTerms.set(tab.key, term);
    try {
      fit.fit();
    } catch {
      /* 容器尚未显示 */
    }

    // 快捷键在按下时读取最新的设置，改了设置不用重连终端
    term.attachCustomKeyEventHandler((e) => {
      if (e.type !== "keydown") return true;
      const combo = comboOf(e);
      if (!combo) return true;
      const cfg = useApp.getState().settings?.terminal;
      const handled = () => {
        e.preventDefault();
        return false;
      };
      if (combo === (cfg?.keyCopy || "Ctrl+Shift+C")) {
        if (actions.current.copy()) {
          // Ctrl+字母 这类按键复制后取消选中，下一次按下照常发给终端（例如 Ctrl+C 中断）
          if (isPlainCtrl(combo)) term.clearSelection();
          return handled();
        }
        // 没有选中内容：Ctrl+字母 照常发给终端，其余组合键不做任何事（避免 Ctrl+Shift+C 被当成 Ctrl+C 中断程序）
        return isPlainCtrl(combo) ? true : handled();
      }
      if (combo === (cfg?.keyPaste || "Ctrl+Shift+V")) {
        actions.current.paste();
        return handled();
      }
      if (combo === (cfg?.keyFind || "Ctrl+Shift+F")) {
        actions.current.openFind();
        return handled();
      }
      if (!cfg?.plainKeysOff) {
        // Windows 习惯：Ctrl+V 粘贴；选中了文字时 Ctrl+C 是复制
        if (combo === "Ctrl+V") {
          actions.current.paste();
          return handled();
        }
        if (combo === "Ctrl+C" && term.hasSelection()) {
          actions.current.copy();
          term.clearSelection();
          return handled();
        }
      }
      return true;
    });
    const selDisp = term.onSelectionChange(() => {
      if (useApp.getState().settings?.terminal.copyOnSelect && term.hasSelection()) actions.current.copy();
    });
    const onContext = (e: MouseEvent) => {
      e.preventDefault();
      setMenu({ x: e.clientX, y: e.clientY, hasSel: term.hasSelection() });
    };
    host.addEventListener("contextmenu", onContext);

    let disposed = false;
    const offs: Array<() => void> = [];
    term.writeln(`\x1b[38;5;141m●\x1b[0m 正在连接 \x1b[1m${tab.title}\x1b[0m …`);
    api
      .OpenTerminal(tab.serverId, term.cols, term.rows)
      .then((id: string) => {
        if (disposed) {
          api.CloseTerminal(id);
          return;
        }
        idRef.current = id;
        // 与之前的写入保持顺序地清屏（reset 会跳过尚未处理的写入队列）
        term.write("\x1b[2J\x1b[3J\x1b[H");
        offs.push(on<string>("term:data:" + id, (b64) => term.write(b64ToBytes(b64))));
        offs.push(
          on<string>("term:exit:" + id, (reason) => {
            term.writeln(`\r\n\x1b[38;5;244m── ${reason} ──\x1b[0m`);
            idRef.current = null;
            patchTerm(tab.key, { state: "closed", message: reason, termId: undefined });
          }),
        );
        patchTerm(tab.key, { state: "open", termId: id, message: undefined });
        // 订阅完成后通知后端开始推送（期间的输出已在后端缓存）
        api.TerminalReady(id);
        api.TerminalResize(id, term.cols, term.rows);
        term.focus();
      })
      .catch((e: unknown) => {
        const msg = errText(e);
        term.writeln(`\r\n\x1b[31m✗ 连接失败：${msg}\x1b[0m`);
        patchTerm(tab.key, { state: "closed", message: msg });
      });
    const dataDisp = term.onData((d) => {
      if (idRef.current) api.TerminalWrite(idRef.current, d);
    });
    const resizeDisp = term.onResize(({ cols, rows }) => {
      if (idRef.current) api.TerminalResize(idRef.current, cols, rows);
    });
    let raf = 0;
    const ro = new ResizeObserver(() => {
      cancelAnimationFrame(raf);
      raf = requestAnimationFrame(() => {
        if (host.offsetWidth > 0 && host.offsetHeight > 0) {
          try {
            fit.fit();
          } catch {
            /* ignore */
          }
        }
      });
    });
    ro.observe(host);

    return () => {
      disposed = true;
      ro.disconnect();
      cancelAnimationFrame(raf);
      host.removeEventListener("contextmenu", onContext);
      offs.forEach((f) => f());
      dataDisp.dispose();
      resizeDisp.dispose();
      selDisp.dispose();
      if (idRef.current) api.CloseTerminal(idRef.current);
      idRef.current = null;
      if (liveTerms.get(tab.key) === term) liveTerms.delete(tab.key);
      term.dispose();
      termRef.current = null;
      searchRef.current = null;
      setFind(null);
      setMenu(null);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab.seq, tab.serverId]);

  // 设置变化时更新外观
  useEffect(() => {
    const term = termRef.current;
    if (!term || !settings) return;
    term.options.fontSize = settings.fontSize;
    term.options.fontFamily = settings.fontFamily;
    term.options.cursorBlink = settings.cursorBlink;
    term.options.theme = termThemes[settings.theme] ?? termThemes.pilot;
    try {
      fitRef.current?.fit();
    } catch {
      /* ignore */
    }
  }, [settings]);

  useEffect(() => {
    if (!active) return;
    const t = setTimeout(() => {
      try {
        fitRef.current?.fit();
      } catch {
        /* ignore */
      }
      termRef.current?.focus();
    }, 30);
    return () => clearTimeout(t);
  }, [active]);

  const menuItems = (): PopItem[] => {
    const term = termRef.current;
    const sep: PopItem = { label: "", icon: X, onClick: () => {}, divider: true };
    return [
      {
        label: "复制",
        icon: Copy,
        hint: settings?.keyCopy,
        disabled: !menu?.hasSel,
        onClick: () => {
          if (actions.current.copy()) toast.ok("已复制");
        },
      },
      { label: "粘贴", icon: ClipboardPaste, hint: settings?.keyPaste, disabled: tab.state !== "open", onClick: () => actions.current.paste() },
      { label: "全选", icon: TextSelect, onClick: () => term?.selectAll() },
      sep,
      { label: "查找…", icon: Search, hint: settings?.keyFind, onClick: () => actions.current.openFind() },
      { label: "清空屏幕", icon: Eraser, onClick: () => term?.clear() },
      sep,
      {
        label: menu?.hasSel ? "让 AI 分析选中的内容" : "让 AI 分析最近的输出",
        icon: Sparkles,
        onClick: () => {
          const text = readTermText(tab.key, 80);
          if (!text) return void toast.warn("终端里还没有可分析的内容");
          useTermUI.getState().askAI(text);
        },
      },
    ];
  };

  const bg = (termThemes[settings?.theme ?? "pilot"] ?? termThemes.pilot).background;
  return (
    <div className="relative h-full w-full" style={{ background: bg }}>
      <div ref={hostRef} className="xterm-host h-full w-full" />
      {find && searchRef.current && <FindBar search={searchRef.current} initial={find.initial} onClose={closeFind} />}
      {menu && (
        <PopMenu
          at={{ x: menu.x, y: menu.y }}
          items={menuItems()}
          onClose={() => {
            setMenu(null);
            // 菜单关闭后把焦点还给终端（「查找」会自己把焦点移到查找框）
            setTimeout(() => !document.querySelector("[data-find-bar]") && termRef.current?.focus(), 0);
          }}
        />
      )}
    </div>
  );
}
