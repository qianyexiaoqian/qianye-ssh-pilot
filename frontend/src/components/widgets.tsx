import { useMemo, useState, type ReactNode } from "react";
import CodeMirror from "@uiw/react-codemirror";
import { StreamLanguage, HighlightStyle, syntaxHighlighting } from "@codemirror/language";
import { shell } from "@codemirror/legacy-modes/mode/shell";
import { python } from "@codemirror/legacy-modes/mode/python";
import { nginx } from "@codemirror/legacy-modes/mode/nginx";
import { yaml } from "@codemirror/legacy-modes/mode/yaml";
import { javascript, json } from "@codemirror/legacy-modes/mode/javascript";
import { toml } from "@codemirror/legacy-modes/mode/toml";
import { properties } from "@codemirror/legacy-modes/mode/properties";
import { dockerFile } from "@codemirror/legacy-modes/mode/dockerfile";
import { xml } from "@codemirror/legacy-modes/mode/xml";
import { standardSQL } from "@codemirror/legacy-modes/mode/sql";
import { EditorView } from "@codemirror/view";
import { tags as t } from "@lezer/highlight";
import ReactMarkdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { Check, Copy, Play, Search, Server as ServerIcon, Zap } from "lucide-react";
import { runtime, type Server } from "../lib/api";
import { cn, hueOf, initials } from "../lib/format";
import { useQuick } from "../lib/quick";
import { toast, useApp } from "../lib/store";
import { CopyButton, StatusDot } from "./ui";

// ---- 服务器头像 ----

export function ServerAvatar({ name, size = 32, className }: { name: string; size?: number; className?: string }) {
  const h = hueOf(name);
  return (
    <div
      className={cn("flex shrink-0 items-center justify-center rounded-[10px] font-semibold text-white", className)}
      style={{
        width: size,
        height: size,
        fontSize: size * 0.36,
        background: `linear-gradient(135deg, hsl(${h} 70% 58%), hsl(${(h + 40) % 360} 72% 46%))`,
      }}
    >
      {initials(name)}
    </div>
  );
}

// ---- 服务器地址（隐私模式下遮住） ----

/** 隐私模式下代替连接地址显示的内容：长度固定，看不出原来的地址有多长 */
export const ADDR_MASK = "••••••••••";

type AddrOf = Pick<Server, "username" | "host" | "port">;
interface AddrParts {
  /** 带不带「用户@」，默认带 */
  user?: boolean;
  /** 带不带「:端口」，默认带 */
  port?: boolean;
}

export function serverAddr(s: AddrOf, privacy: boolean, { user = true, port = true }: AddrParts = {}) {
  if (privacy) return ADDR_MASK;
  return `${user ? s.username + "@" : ""}${s.host}${port ? ":" + s.port : ""}`;
}

/** 返回一个函数，把服务器写成「用户@主机:端口」；隐私模式打开时返回的是遮住的文字 */
export function useAddr() {
  const privacy = useApp((s) => s.privacy);
  return (s: AddrOf, parts?: AddrParts) => serverAddr(s, privacy, parts);
}

/** 隐私模式下把一段文字（IP、主机名等）遮住 */
export function useMask() {
  const privacy = useApp((s) => s.privacy);
  return (text: string) => (privacy && text ? ADDR_MASK : text);
}

/** 隐私模式下把一段文字（报错信息、巡检结果等）里出现的服务器地址遮住 */
export function useScrub() {
  const privacy = useApp((s) => s.privacy);
  const servers = useApp((s) => s.servers);
  return (text: string) => {
    if (!privacy || !text) return text;
    // 长的先换，免得一个地址是另一个地址的一部分时换出半截；太短的不换，避免误伤普通文字
    const hosts = [...new Set(servers.map((s) => s.host))].filter((h) => h.length >= 4).sort((a, b) => b.length - a.length);
    return hosts.reduce((out, h) => out.split(h).join(ADDR_MASK), text);
  };
}

/**
 * 点一下就复制的文字：鼠标移上去时末尾出现复制图标。value 是复制的内容，children 是显示的内容（两者可以不同）。
 * secret 表示复制的内容不该显示出来（隐私模式下的地址），提示里就不带上它。
 */
export function CopyText({ value, what, tip, secret, className, children }: { value: string; what: string; tip?: string; secret?: boolean; className?: string; children: ReactNode }) {
  const [done, setDone] = useState(false);
  return (
    <button
      type="button"
      data-drag-ok
      title={tip ?? `点击复制${what}`}
      onClick={(e) => {
        e.stopPropagation();
        runtime.ClipboardSetText(value);
        toast.ok(`已复制${what}`, secret ? undefined : value);
        setDone(true);
        setTimeout(() => setDone(false), 1400);
      }}
      className={cn("group/copy inline-flex max-w-full min-w-0 items-center gap-1 text-left transition-colors hover:text-accent", className)}
    >
      <span className="truncate">{children}</span>
      {done ? <Check size={12} className="shrink-0 text-ok" /> : <Copy size={12} className="shrink-0 opacity-0 transition-opacity group-hover/copy:opacity-80" />}
    </button>
  );
}

/** 服务器的连接地址。copy 为 true 时点一下复制主机（IP 或域名，不带用户名和端口） */
export function ServerAddr({ server, user, port, copy, className }: { server: AddrOf; copy?: boolean; className?: string } & AddrParts) {
  const privacy = useApp((s) => s.privacy);
  const text = serverAddr(server, privacy, { user, port });
  if (!copy) return <span className={className}>{text}</span>;
  return (
    <CopyText value={server.host} what=" IP / 域名" tip="点击复制 IP / 域名（不带端口）" secret={privacy} className={className}>
      {text}
    </CopyText>
  );
}

// ---- 服务器选择器（多选 / 单选） ----

export function ServerPicker({
  value,
  onChange,
  single,
  className,
  maxHeight = 280,
  exclude = [],
}: {
  value: string[];
  onChange: (ids: string[]) => void;
  single?: boolean;
  className?: string;
  maxHeight?: number;
  exclude?: string[];
}) {
  const { servers, status } = useApp();
  const [q, setQ] = useState("");
  const [group, setGroup] = useState("");
  const groups = useMemo(() => [...new Set(servers.map((s) => s.group).filter(Boolean))], [servers]);
  const list = servers.filter((s) => {
    if (exclude.includes(s.id)) return false;
    if (group && s.group !== group) return false;
    const kw = q.trim().toLowerCase();
    return !kw || [s.name, s.host, s.group, ...(s.tags ?? [])].join(" ").toLowerCase().includes(kw);
  });
  const allSelected = list.length > 0 && list.every((s) => value.includes(s.id));
  const toggle = (id: string) => {
    if (single) return onChange([id]);
    onChange(value.includes(id) ? value.filter((x) => x !== id) : [...value, id]);
  };
  return (
    <div className={cn("overflow-hidden rounded-xl border border-line2 bg-bg2", className)}>
      <div className="flex items-center gap-2 border-b border-line px-2.5 py-2">
        <Search size={14} className="text-muted" />
        <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索服务器" className="h-6 min-w-0 flex-1 bg-transparent text-[12.5px] outline-none placeholder:text-muted" />
        {groups.length > 0 && (
          <select value={group} onChange={(e) => setGroup(e.target.value)} className="h-6 rounded-md border border-line bg-panel px-1.5 text-[12px] text-fg2 outline-none">
            <option value="">全部分组</option>
            {groups.map((g) => (
              <option key={g} value={g}>
                {g}
              </option>
            ))}
          </select>
        )}
        {!single && (
          <button
            type="button"
            onClick={() => onChange(allSelected ? value.filter((id) => !list.some((s) => s.id === id)) : [...new Set([...value, ...list.map((s) => s.id)])])}
            className="h-6 rounded-md px-2 text-[12px] text-accent hover:bg-accent/10"
          >
            {allSelected ? "取消全选" : "全选"}
          </button>
        )}
      </div>
      <div className="overflow-y-auto p-1" style={{ maxHeight }}>
        {list.length === 0 && (
          <div className="flex flex-col items-center gap-2 py-8 text-[12.5px] text-muted">
            <ServerIcon size={18} />
            {servers.length === 0 ? "还没有添加服务器" : "没有匹配的服务器"}
          </div>
        )}
        {list.map((s) => {
          const on = value.includes(s.id);
          return (
            <button
              key={s.id}
              type="button"
              onClick={() => toggle(s.id)}
              className={cn("flex h-10 w-full items-center gap-2.5 rounded-lg px-2 text-left transition-colors", on ? "bg-accent/10" : "hover:bg-panel2")}
            >
              <span className={cn("flex h-4 w-4 shrink-0 items-center justify-center border transition-colors", single ? "rounded-full" : "rounded-[5px]", on ? "border-accent bg-accent text-white" : "border-line2")}>
                {on && <Check size={11} strokeWidth={3} />}
              </span>
              <StatusDot status={status[s.id]?.status} />
              <span className="truncate text-[13px] text-fg">{s.name}</span>
              <ServerAddr server={s} port={false} className="ml-auto truncate font-mono text-[11.5px] text-muted" />
            </button>
          );
        })}
      </div>
      {!single && (
        <div className="border-t border-line px-3 py-1.5 text-[11.5px] text-muted">
          已选择 <span className="font-medium text-fg2">{value.length}</span> / {servers.length} 台
        </div>
      )}
    </div>
  );
}

// ---- 代码编辑器 ----

const highlight = HighlightStyle.define([
  { tag: t.comment, color: "#6b7389", fontStyle: "italic" },
  { tag: [t.keyword, t.controlKeyword, t.operatorKeyword], color: "#b69cff" },
  { tag: [t.string, t.special(t.string)], color: "#7ee0a1" },
  { tag: [t.number, t.bool, t.atom], color: "#f6b26b" },
  { tag: [t.variableName, t.special(t.variableName)], color: "#7dd3fc" },
  { tag: [t.function(t.variableName), t.definition(t.variableName)], color: "#93c5fd" },
  { tag: [t.typeName, t.className], color: "#22d3ee" },
  { tag: t.operator, color: "#c4c9d8" },
  { tag: t.meta, color: "#f472b6" },
]);

const editorTheme = EditorView.theme(
  {
    "&": { backgroundColor: "transparent", color: "var(--c-fg)" },
    ".cm-content": { caretColor: "var(--c-accent)", padding: "10px 0" },
    ".cm-gutters": { backgroundColor: "transparent", color: "var(--c-muted)", border: "none", paddingLeft: "6px" },
    ".cm-activeLine": { backgroundColor: "color-mix(in oklab, var(--c-fg) 4%, transparent)" },
    ".cm-activeLineGutter": { backgroundColor: "transparent", color: "var(--c-fg2)" },
    ".cm-selectionBackground, &.cm-focused .cm-selectionBackground, ::selection": { backgroundColor: "color-mix(in oklab, var(--c-accent) 30%, transparent) !important" },
    ".cm-cursor": { borderLeftColor: "var(--c-accent)" },
  },
  { dark: true },
);

const editorModes = { bash: shell, sh: shell, python3: python, python, nginx, yaml, json, javascript, toml, properties, dockerfile: dockerFile, xml, sql: standardSQL };

/** language 为 "text" 时不做语法高亮；未知的语言按 shell 处理 */
export function CodeEditor({ value, onChange, language = "bash", readOnly, className, minHeight = 240 }: { value: string; onChange?: (v: string) => void; language?: string; readOnly?: boolean; className?: string; minHeight?: number }) {
  const extensions = useMemo(() => {
    const base = [syntaxHighlighting(highlight), editorTheme, EditorView.lineWrapping];
    if (language === "text") return base;
    return [StreamLanguage.define(editorModes[language as keyof typeof editorModes] ?? shell), ...base];
  }, [language]);
  return (
    <div className={cn("cm-host overflow-hidden rounded-xl border border-line2 bg-bg2", className)} style={{ minHeight }}>
      <CodeMirror
        value={value}
        onChange={onChange}
        readOnly={readOnly}
        extensions={extensions}
        theme="none"
        height="100%"
        style={{ height: "100%", minHeight }}
        basicSetup={{ foldGutter: false, highlightActiveLine: !readOnly, autocompletion: false, searchKeymap: true }}
      />
    </div>
  );
}

// ---- Markdown ----

export function CodeBlock({ code, lang, onRun, runLabel = "执行" }: { code: string; lang?: string; onRun?: (code: string) => void; runLabel?: string }) {
  const runnable = onRun && (!lang || ["bash", "sh", "shell", "console", "zsh"].includes(lang));
  return (
    <div className="@container/code group/code my-2.5 overflow-hidden rounded-xl border border-line2 bg-bg2">
      <div className="flex h-8 items-center justify-between border-b border-line px-3">
        <span className="min-w-0 truncate font-mono text-[11px] text-muted">{lang || "text"}</span>
        <div className="flex shrink-0 items-center gap-0.5 opacity-70 transition-opacity group-hover/code:opacity-100">
          {runnable && (
            <button type="button" onClick={() => useQuick.getState().edit({ command: code })} title="保存为快捷命令，之后在终端里一键发送" className="flex h-6 items-center gap-1 rounded-md px-2 text-[11.5px] whitespace-nowrap text-fg2 hover:bg-panel3">
              <Zap size={11} />
              <span className="hidden @[340px]/code:inline">存为快捷命令</span>
            </button>
          )}
          {runnable && (
            <button type="button" onClick={() => onRun!(code)} className="flex h-6 items-center gap-1 rounded-md px-2 text-[11.5px] whitespace-nowrap text-accent hover:bg-accent/12">
              <Play size={11} />
              {runLabel}
            </button>
          )}
          <CopyButton text={code} size="sm" className="!h-6 !w-6" />
        </div>
      </div>
      <pre className="overflow-x-auto px-3.5 py-2.5 font-mono text-[12.5px] leading-relaxed text-fg2">
        <code>{code}</code>
      </pre>
    </div>
  );
}

export function Markdown({ text, onRun, runLabel, className }: { text: string; onRun?: (code: string) => void; runLabel?: string; className?: string }) {
  return (
    <div className={cn("prose-chat", className)}>
      <ReactMarkdown
        remarkPlugins={[remarkGfm]}
        components={{
          pre: ({ children }) => <>{children}</>,
          code: ({ className, children }) => {
            const m = /language-(\w+)/.exec(className || "");
            const content = String(children ?? "");
            const isBlock = !!m || content.includes("\n");
            if (!isBlock) return <code>{children}</code>;
            return <CodeBlock code={content.replace(/\n$/, "")} lang={m?.[1]} onRun={onRun} runLabel={runLabel} />;
          },
          a: ({ href, children }) => (
            <a href={href} target="_blank" rel="noreferrer">
              {children}
            </a>
          ),
        }}
      >
        {text}
      </ReactMarkdown>
    </div>
  );
}

// ---- 输出面板 ----

export function OutputBox({ text, className, empty = "（无输出）" }: { text: string; className?: string; empty?: ReactNode }) {
  return (
    <pre className={cn("selectable overflow-auto rounded-xl border border-line bg-[#090a0e] px-3.5 py-3 font-mono text-[12px] leading-relaxed whitespace-pre-wrap text-[#c9cede]", className)}>
      {text || <span className="text-muted">{empty}</span>}
    </pre>
  );
}

export function KV({ k, v, mono }: { k: ReactNode; v: ReactNode; mono?: boolean }) {
  return (
    <div className="flex items-start justify-between gap-4 py-1.5 text-[12.5px]">
      <span className="shrink-0 text-muted">{k}</span>
      <span className={cn("min-w-0 text-right break-all text-fg2", mono && "font-mono text-[12px]")}>{v}</span>
    </div>
  );
}
