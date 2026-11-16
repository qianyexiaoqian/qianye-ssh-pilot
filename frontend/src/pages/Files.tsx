import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import {
  ArrowDown,
  ArrowUp,
  ChevronDown,
  ChevronUp,
  CircleCheck,
  CircleX,
  Copy,
  CornerLeftUp,
  Download,
  Ellipsis,
  File as FileIcon,
  FileArchive,
  FileCode,
  FileImage,
  FilePlus,
  FileText as FileTextIcon,
  Folder,
  FolderOpen,
  FolderPlus,
  FolderUp,
  House,
  Link2,
  LoaderCircle,
  LockKeyhole,
  MapPin,
  Pencil,
  RefreshCw,
  Save,
  Search,
  ShieldAlert,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import { api, errText, on, runtime, type FileEntry, type FileListing, type FileText, type Transfer } from "../lib/api";
import { isFileDrag, setDropHandler } from "../lib/drop";
import { bitsToPerm, COMMON_DIRS, kindOf, languageOf, looksBinary, permBits, useTransfers } from "../lib/files";
import { cn, formatBytes, formatDate, formatRate } from "../lib/format";
import { confirm, toast, useApp } from "../lib/store";
import { useViewport } from "../lib/viewport";
import { Badge, Button, Callout, Card, Field, IconButton, Input, Menu, Meter, Modal, PopMenu, Spinner, Switch, type PopItem } from "../components/ui";
import { CodeEditor } from "../components/widgets";

const TEXT_LIMIT = 2 * 1024 * 1024;
const MAX_ROWS = 2000;

// 每台服务器上次浏览到的目录（再次打开文件管理窗口时回到原处）
const lastPathKey = (serverId: string) => "files-path:" + serverId;

type SortKey = "name" | "size" | "time";

/** 所有者显示为「用户:组」；用户与组同名时只显示一次，查不到名称时显示数字 ID */
function ownerText(e: FileEntry): string {
  const user = e.owner || (e.uid >= 0 ? String(e.uid) : "");
  const group = e.group || (e.gid >= 0 ? String(e.gid) : "");
  if (!user && !group) return "-";
  return e.owner && e.owner === e.group ? user : `${user}:${group}`;
}

function EntryIcon({ e }: { e: FileEntry }) {
  const kind = kindOf(e.name, e.isDir, e.isLink);
  const Icon = { folder: Folder, link: Link2, archive: FileArchive, image: FileImage, code: FileCode, text: FileTextIcon, binary: FileIcon }[kind];
  return <Icon size={16} className={cn("shrink-0", kind === "folder" ? "text-accent" : "text-muted")} />;
}

// ---- 输入名称（新建 / 重命名） ----

function NameModal({ title, label, initial, confirmText, onClose, onSubmit }: { title: string; label: string; initial: string; confirmText: string; onClose: () => void; onSubmit: (name: string) => Promise<boolean> }) {
  const [name, setName] = useState(initial);
  const [busy, setBusy] = useState(false);
  const submit = async () => {
    if (!name.trim() || busy) return;
    setBusy(true);
    const ok = await onSubmit(name.trim());
    setBusy(false);
    if (ok) onClose();
  };
  return (
    <Modal
      open
      onClose={onClose}
      title={title}
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button variant="primary" loading={busy} disabled={!name.trim()} onClick={submit}>
            {confirmText}
          </Button>
        </>
      }
    >
      <Field label={label}>
        <Input mono autoFocus value={name} onChange={(e) => setName(e.target.value)} onKeyDown={(e) => e.key === "Enter" && submit()} onFocus={(e) => e.target.select()} />
      </Field>
    </Modal>
  );
}

// ---- 修改权限 ----

function ChmodModal({ entry, onClose, onSubmit }: { entry: FileEntry; onClose: () => void; onSubmit: (perm: string) => Promise<boolean> }) {
  const special = entry.perm.length > 3 ? entry.perm.slice(0, entry.perm.length - 3) : "";
  const [bits, setBits] = useState(() => permBits(entry.perm));
  const [busy, setBusy] = useState(false);
  const perm = bitsToPerm(bits, special);
  const rows = ["所有者", "同组用户", "其他人"];
  const cols = ["读取", "写入", entry.isDir ? "进入" : "执行"];
  return (
    <Modal
      open
      onClose={onClose}
      title={`修改权限 · ${entry.name}`}
      icon={LockKeyhole}
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button
            variant="primary"
            loading={busy}
            disabled={perm === entry.perm}
            onClick={async () => {
              setBusy(true);
              const ok = await onSubmit(perm);
              setBusy(false);
              if (ok) onClose();
            }}
          >
            应用
          </Button>
        </>
      }
    >
      <table className="w-full text-[13px]">
        <thead>
          <tr className="text-[12px] text-muted">
            <th className="pb-2 text-left font-medium" />
            {cols.map((c) => (
              <th key={c} className="pb-2 font-medium">
                {c}
              </th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map((r, i) => (
            <tr key={r} className="border-t border-line">
              <td className="py-2.5 text-fg2">{r}</td>
              {cols.map((c, j) => (
                <td key={c} className="py-2.5 text-center">
                  <input type="checkbox" className="h-4 w-4 accent-[var(--c-accent)]" checked={bits[i * 3 + j]} onChange={(e) => setBits((b) => b.map((v, k) => (k === i * 3 + j ? e.target.checked : v)))} />
                </td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
      <div className="mt-4 flex items-center justify-between rounded-xl border border-line bg-bg2/50 px-3.5 py-2.5 text-[12.5px]">
        <span className="text-muted">权限数字</span>
        <span className="font-mono text-fg">
          {entry.perm} → <span className={perm === entry.perm ? "" : "font-semibold text-accent"}>{perm}</span>
        </span>
      </div>
      <p className="mt-3 text-[12px] leading-relaxed text-muted">常见取值：普通文件 644，脚本 755，私钥和密码文件 600，文件夹 755。</p>
    </Modal>
  );
}

// ---- 在线编辑 ----

function EditorModal({ serverId, file, onClose, onSaved }: { serverId: string; file: FileText; onClose: () => void; onSaved: () => void }) {
  const [content, setContent] = useState(file.content);
  const [saved, setSaved] = useState(file.content);
  const [saving, setSaving] = useState(false);
  const dirty = content !== saved;
  const name = file.path.slice(file.path.lastIndexOf("/") + 1);
  // 编辑区的高度跟着窗口走：小窗口里不撑出对话框，大窗口里多显示几行
  const editorHeight = useViewport((s) => Math.max(140, Math.min(s.height - 250, 760)));

  const save = useCallback(async () => {
    if (saving) return;
    setSaving(true);
    try {
      await api.FilesWrite(serverId, file.path, content);
      setSaved(content);
      toast.ok("已保存", file.path);
      onSaved();
    } catch (e) {
      toast.error("保存失败", e);
    } finally {
      setSaving(false);
    }
  }, [saving, serverId, file.path, content, onSaved]);

  const close = async () => {
    if (dirty && !(await confirm({ title: "放弃未保存的修改？", message: `「${name}」有修改还没有保存。`, confirmText: "放弃修改", danger: true }))) return;
    onClose();
  };
  // Ctrl+S 保存
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === "s") {
        e.preventDefault();
        if (content !== saved) void save();
      }
    };
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, [content, saved, save]);

  return (
    <Modal
      open
      onClose={close}
      title={
        <span className="flex items-center gap-2">
          {name}
          {dirty && <Badge tone="warn">未保存</Badge>}
          {file.viaSudo && (
            <Badge tone="info" icon={ShieldAlert} title="登录用户没有权限直接读写这个文件，已通过 sudo 访问">
              通过 sudo 读写
            </Badge>
          )}
        </span>
      }
      description={<span className="font-mono text-[12px]">{file.path}</span>}
      icon={Pencil}
      size="2xl"
      bodyClassName="!p-0"
      footer={
        <>
          <span className="mr-auto text-[12px] text-muted">
            {formatBytes(new Blob([content]).size)} · 权限 {file.perm || "-"} · Ctrl+S 保存
          </span>
          <Button variant="ghost" onClick={close}>
            关闭
          </Button>
          <Button variant="primary" icon={Save} loading={saving} disabled={!dirty} onClick={save}>
            保存
          </Button>
        </>
      }
    >
      <CodeEditor value={content} onChange={setContent} language={languageOf(file.path)} minHeight={editorHeight} className="!rounded-none !border-0" />
    </Modal>
  );
}

// ---- 传输任务 ----

function TransferRow({ t }: { t: Transfer }) {
  const running = t.status === "running" || t.status === "preparing";
  const pct = t.total > 0 ? (t.done / t.total) * 100 : t.status === "done" ? 100 : 0;
  const Icon = t.kind === "upload" ? ArrowUp : ArrowDown;
  const eta = running && t.speed > 0 && t.total > t.done ? Math.ceil((t.total - t.done) / t.speed) : 0;
  return (
    <div className="flex items-center gap-3 px-4 py-2.5">
      <div className={cn("flex h-8 w-8 shrink-0 items-center justify-center rounded-lg", running ? "bg-accent/12 text-accent" : "bg-panel3 text-muted")}>
        <Icon size={15} />
      </div>
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2 text-[12.5px]">
          <span className="truncate font-medium text-fg">{t.title}</span>
          <span className="shrink-0 text-muted">
            {t.kind === "upload" ? "上传到" : "下载自"} {t.serverName}
          </span>
        </div>
        {running ? (
          <>
            <Meter className="mt-1.5" value={pct} tone="accent" height={5} />
            <div className="mt-1 flex justify-between text-[11.5px] text-muted" style={{ fontVariantNumeric: "tabular-nums" }}>
              <span className="truncate">
                {t.status === "preparing" ? "正在准备…" : `${formatBytes(t.done)} / ${formatBytes(t.total)} · 文件 ${t.filesDone} / ${t.files}`}
              </span>
              <span className="shrink-0 pl-3">
                {formatRate(t.speed)}
                {eta > 0 && ` · 约 ${eta >= 60 ? `${Math.ceil(eta / 60)} 分钟` : `${eta} 秒`}`}
              </span>
            </div>
          </>
        ) : (
          <div className="mt-0.5 truncate text-[11.5px] text-muted" title={t.error || t.target}>
            {t.status === "failed"
              ? t.error
              : t.status === "cancelled"
                ? `已取消，停在 ${formatBytes(t.done)} / ${formatBytes(t.total)}，没有保留未传完的文件`
                : `${t.files} 个文件 · ${formatBytes(t.total)} · ${t.kind === "upload" ? "到 " : "保存在 "}${t.target}`}
          </div>
        )}
      </div>
      {running ? (
        <Button size="xs" onClick={() => api.FilesCancel(t.id)}>
          取消
        </Button>
      ) : (
        <>
          {t.status === "done" ? (
            <Badge tone="ok" icon={CircleCheck}>
              完成
            </Badge>
          ) : t.status === "failed" ? (
            <Badge tone="bad" icon={CircleX}>
              失败
            </Badge>
          ) : (
            <Badge>已取消</Badge>
          )}
          {t.kind === "download" && t.status === "done" && (
            <Button size="xs" icon={FolderOpen} onClick={() => api.RevealLocal(t.target).catch((e: unknown) => toast.error("无法打开文件夹", e))}>
              打开文件夹
            </Button>
          )}
        </>
      )}
    </div>
  );
}

function TransferPanel() {
  const { list, clear } = useTransfers();
  const [open, setOpen] = useState(true);
  if (list.length === 0) return null;
  const running = list.filter((t) => t.status === "running" || t.status === "preparing").length;
  return (
    <div className="shrink-0 border-t border-line bg-bg2">
      <div className="flex h-9 items-center gap-2 px-4">
        <button type="button" onClick={() => setOpen((v) => !v)} className="flex flex-1 items-center gap-2 text-left text-[12.5px] font-medium text-fg2 hover:text-fg">
          {open ? <ChevronDown size={14} /> : <ChevronUp size={14} />}
          传输任务
          {running > 0 ? (
            <span className="inline-flex items-center gap-1.5 font-normal text-accent">
              <LoaderCircle size={12} className="animate-spin" />
              {running} 个进行中
            </span>
          ) : (
            <span className="font-normal text-muted">共 {list.length} 个，全部结束</span>
          )}
        </button>
        {list.length > running && (
          <Button size="xs" variant="ghost" onClick={() => void clear()}>
            清除已结束的
          </Button>
        )}
      </div>
      {open && <div className="max-h-[196px] divide-y divide-line overflow-y-auto border-t border-line">{list.map((t) => <TransferRow key={t.id} t={t} />)}</div>}
    </div>
  );
}

// ---- 页面 ----

/** 一台服务器的文件管理（显示在独立窗口里） */
export default function Files({ serverId }: { serverId: string }) {
  const { servers } = useApp();
  const [listing, setListing] = useState<FileListing | null>(null);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const [pathInput, setPathInput] = useState("");
  const [showHidden, setShowHidden] = useState(localStorage.getItem("files-hidden") === "1");
  const [filter, setFilter] = useState("");
  const [sort, setSort] = useState<{ key: SortKey; dir: 1 | -1 }>({ key: "name", dir: 1 });
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [menu, setMenu] = useState<{ entry: FileEntry; x: number; y: number } | null>(null);
  const [naming, setNaming] = useState<{ kind: "mkdir" | "create" | "rename"; entry?: FileEntry } | null>(null);
  const [chmod, setChmod] = useState<FileEntry | null>(null);
  const [editing, setEditing] = useState<FileText | null>(null);
  const [opening, setOpening] = useState("");
  const [drag, setDrag] = useState<{ dir: string } | null>(null);
  const [parentGone, setParentGone] = useState(false);
  const seq = useRef(0);
  const server = servers.find((s) => s.id === serverId);
  const cwd = listing?.path ?? "";
  const cwdRef = useRef(cwd);
  cwdRef.current = cwd;

  const load = useCallback(
    async (dir: string, quiet = false) => {
      if (!serverId) return;
      const my = ++seq.current;
      if (!quiet) setLoading(true);
      setError("");
      try {
        const l = await api.FilesList(serverId, dir);
        if (my !== seq.current) return;
        setListing(l);
        setPathInput(l.path);
        localStorage.setItem(lastPathKey(serverId), l.path);
        if (!quiet) {
          setSelected(new Set());
          setFilter("");
        }
      } catch (e) {
        if (my !== seq.current) return;
        if (!quiet) setError(errText(e));
      } finally {
        if (my === seq.current) setLoading(false);
      }
    },
    [serverId],
  );

  useEffect(() => {
    if (!serverId) return;
    setListing(null);
    void load(localStorage.getItem(lastPathKey(serverId)) ?? "");
  }, [serverId, load]);
  // 主窗口关闭后，本窗口等传输结束再关闭
  useEffect(() => on("parent:exit", () => setParentGone(true)), []);
  // 上传到当前目录的任务完成后刷新列表
  const { uploadsDone, lastUpload } = useTransfers();
  useEffect(() => {
    if (lastUpload && lastUpload.serverId === serverId && lastUpload.target === cwd) void load(cwd, true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [uploadsDone]);

  const rows = useMemo(() => {
    const kw = filter.trim().toLowerCase();
    const list = (listing?.entries ?? []).filter((e) => (showHidden || !e.name.startsWith(".")) && (!kw || e.name.toLowerCase().includes(kw)));
    const cmp = (a: FileEntry, b: FileEntry) => {
      if (a.isDir !== b.isDir) return a.isDir ? -1 : 1;
      const v = sort.key === "size" ? a.size - b.size : sort.key === "time" ? a.modTime - b.modTime : a.name.localeCompare(b.name, undefined, { numeric: true, sensitivity: "base" });
      return v * sort.dir;
    };
    return [...list].sort(cmp);
  }, [listing, showHidden, filter, sort]);
  const hiddenCount = useMemo(() => (listing?.entries ?? []).filter((e) => e.name.startsWith(".")).length, [listing]);
  const shown = rows.slice(0, MAX_ROWS);
  const picked = rows.filter((e) => selected.has(e.path));
  const allPicked = shown.length > 0 && shown.every((e) => selected.has(e.path));

  const toggle = (p: string) =>
    setSelected((s) => {
      const n = new Set(s);
      if (n.has(p)) n.delete(p);
      else n.add(p);
      return n;
    });
  const sortBy = (key: SortKey) => setSort((s) => (s.key === key ? { key, dir: s.dir === 1 ? -1 : 1 } : { key, dir: key === "name" ? 1 : -1 }));

  // ---- 操作 ----

  const download = async (paths: string[]) => {
    if (!paths.length) return;
    try {
      const dir = await api.FilesPickSaveDir();
      if (!dir) return;
      await api.FilesDownload(serverId, paths, dir);
      setSelected(new Set());
    } catch (e) {
      toast.error("无法开始下载", e);
    }
  };
  // 把本地的文件、文件夹上传到 dir。文件夹连同里面的内容一起上传，服务器上没有的文件夹会自动创建。
  const startUpload = useCallback(
    async (paths: string[], dir: string) => {
      if (!paths.length || !dir) return;
      try {
        const res = await api.FilesUpload(serverId, dir, paths, false);
        if (res.conflicts?.length) {
          const names = res.conflicts.slice(0, 6).join("、") + (res.conflicts.length > 6 ? ` 等 ${res.conflicts.length} 项` : "");
          if (!(await confirm({ title: `${dir} 里已经有同名的项目`, message: `${names}\n\n继续上传会覆盖同名文件（文件夹会合并）。`, confirmText: "覆盖并上传", danger: true }))) return;
          await api.FilesUpload(serverId, dir, paths, true);
        }
      } catch (e) {
        toast.error("无法开始上传", e);
      }
    },
    [serverId],
  );
  const upload = async (folder: boolean) => {
    try {
      const paths = await api.FilesPickUpload(folder);
      if (paths?.length) await startUpload(paths, cwd);
    } catch (e) {
      toast.error("无法开始上传", e);
    }
  };

  // 拖拽上传：把文件或文件夹从资源管理器拖进窗口。拖到某个文件夹那一行上就传进那个文件夹，否则传到当前目录。
  useEffect(() => {
    const dirAt = (el: Element | null) => el?.closest?.("[data-drop-dir]")?.getAttribute("data-drop-dir") || cwdRef.current;
    let depth = 0;
    const enter = (e: DragEvent) => {
      if (!isFileDrag(e)) return;
      depth++;
      setDrag({ dir: dirAt(e.target as Element | null) });
    };
    const over = (e: DragEvent) => {
      if (!isFileDrag(e)) return;
      const dir = dirAt(e.target as Element | null);
      setDrag((d) => (d?.dir === dir ? d : { dir }));
    };
    const leave = (e: DragEvent) => {
      if (!isFileDrag(e)) return;
      depth = Math.max(0, depth - 1);
      if (depth === 0) setDrag(null);
    };
    const end = () => {
      depth = 0;
      setDrag(null);
    };
    window.addEventListener("dragenter", enter);
    window.addEventListener("dragover", over);
    window.addEventListener("dragleave", leave);
    window.addEventListener("drop", end);
    setDropHandler((x, y, paths) => {
      end();
      if (!cwdRef.current) return void toast.warn("目录还没有打开，稍后再拖一次");
      void startUpload(paths, dirAt(document.elementFromPoint(x, y)));
    });
    return () => {
      window.removeEventListener("dragenter", enter);
      window.removeEventListener("dragover", over);
      window.removeEventListener("dragleave", leave);
      window.removeEventListener("drop", end);
      setDropHandler(null);
    };
  }, [startUpload]);
  const remove = async (entries: FileEntry[]) => {
    if (!entries.length) return;
    const dirs = entries.filter((e) => e.isDir && !e.isLink).length;
    const names = entries
      .slice(0, 6)
      .map((e) => e.name)
      .join("、");
    const title = entries.length === 1 ? `删除「${entries[0].name}」？` : `删除选中的 ${entries.length} 项？`;
    const message = `${entries.length > 1 ? names + (entries.length > 6 ? " 等" : "") + "\n\n" : ""}${dirs ? "文件夹里的所有内容会一起删除。" : ""}删除后无法恢复。`;
    if (!(await confirm({ title, message, danger: true, confirmText: "删除" }))) return;
    try {
      await api.FilesDelete(
        serverId,
        entries.map((e) => e.path),
      );
      toast.ok(entries.length === 1 ? "已删除" : `已删除 ${entries.length} 项`);
    } catch (e) {
      toast.error("删除失败", e);
    }
    setSelected(new Set());
    void load(cwd, true);
  };
  const openEntry = async (e: FileEntry) => {
    if (e.isDir) return void load(e.path);
    if (looksBinary(e.name) || e.size > TEXT_LIMIT) {
      const why = e.size > TEXT_LIMIT ? `有 ${formatBytes(e.size)}，超过在线查看的上限` : "不是文本文件";
      if (await confirm({ title: `下载「${e.name}」？`, message: `这个文件${why}，不能在线查看，可以下载到本地后打开。`, confirmText: "下载" })) void download([e.path]);
      return;
    }
    setOpening(e.path);
    try {
      setEditing(await api.FilesRead(serverId, e.path));
    } catch (err) {
      toast.error(`无法打开 ${e.name}`, err);
    } finally {
      setOpening("");
    }
  };
  const submitName = async (name: string) => {
    if (!naming) return false;
    try {
      if (naming.kind === "mkdir") await api.FilesMkdir(serverId, cwd, name);
      else if (naming.kind === "create") await api.FilesCreate(serverId, cwd, name);
      else if (naming.entry) await api.FilesRename(serverId, naming.entry.path, name);
      void load(cwd, true);
      return true;
    } catch (e) {
      toast.error(naming.kind === "rename" ? "重命名失败" : "新建失败", e);
      return false;
    }
  };
  const menuItems = (e: FileEntry): PopItem[] => [
    { label: e.isDir ? "打开" : "查看 / 编辑", icon: e.isDir ? FolderOpen : Pencil, onClick: () => void openEntry(e) },
    { label: "下载", icon: Download, onClick: () => void download([e.path]) },
    { label: "", icon: X, onClick: () => {}, divider: true },
    { label: "重命名", icon: Pencil, onClick: () => setNaming({ kind: "rename", entry: e }) },
    { label: "修改权限", icon: LockKeyhole, onClick: () => setChmod(e) },
    {
      label: "复制路径",
      icon: Copy,
      onClick: () => {
        runtime.ClipboardSetText(e.path);
        toast.ok("已复制路径", e.path);
      },
    },
    { label: "", icon: X, onClick: () => {}, divider: true },
    { label: "删除", icon: Trash2, danger: true, onClick: () => void remove([e]) },
  ];

  if (!server) {
    return <div className="flex h-full items-center justify-center px-6 text-center text-[13px] text-muted">这台服务器已经被删除，可以关闭这个窗口。</div>;
  }

  // 可点击排序的表头（普通函数而不是组件，避免每次渲染都重新挂载）
  const sortHead = (k: SortKey, label: string, className?: string) => (
    <th className={cn("px-3 py-2 font-medium", className)}>
      <button type="button" onClick={() => sortBy(k)} className={cn("inline-flex items-center gap-1 hover:text-fg", sort.key === k && "text-fg2")}>
        {label}
        {sort.key === k && (sort.dir === 1 ? <ArrowUp size={11} /> : <ArrowDown size={11} />)}
      </button>
    </th>
  );

  return (
    <div className="@container/files flex h-full flex-col">
      {parentGone && (
        <div className="px-3 pt-3 @[640px]/files:px-5">
          <Callout tone="info" title="主窗口已经关闭">
            传输任务完成后，这个窗口会自动关闭。
          </Callout>
        </div>
      )}
      {/* 路径栏 */}
      <div className="flex shrink-0 items-center gap-1.5 px-3 pt-3 pb-2.5 @[640px]/files:gap-2 @[640px]/files:px-5">
        <IconButton icon={CornerLeftUp} title="上一级" disabled={!listing?.parent || loading} onClick={() => listing?.parent && load(listing.parent)} />
        <IconButton icon={House} title="主目录" disabled={loading} onClick={() => load("~")} />
        <IconButton icon={RefreshCw} title="刷新" loading={loading} onClick={() => load(cwd || "")} />
        <form
          className="min-w-0 flex-1"
          onSubmit={(e) => {
            e.preventDefault();
            void load(pathInput.trim() || "~");
          }}
        >
          <Input mono value={pathInput} onChange={(e) => setPathInput(e.target.value)} placeholder="输入路径后回车，例如 /etc/nginx" />
        </form>
        <Menu
          align="right"
          trigger={
            <Button icon={MapPin} iconRight={ChevronDown} title="常用位置">
              <span className="hidden @[640px]/files:inline">常用位置</span>
            </Button>
          }
          items={COMMON_DIRS.map((d) => ({ label: d.label, onClick: () => void load(d.path) }))}
        />
      </div>

      {/* 工具栏 */}
      <div className="flex shrink-0 flex-wrap items-center gap-x-2 gap-y-1.5 px-3 pb-2.5 @[640px]/files:px-5">
        {picked.length > 0 ? (
          <>
            <span className="text-[12.5px] text-fg2">
              已选择 <span className="font-semibold text-fg">{picked.length}</span> 项
            </span>
            <Button size="sm" variant="primary" icon={Download} onClick={() => download(picked.map((e) => e.path))}>
              下载
            </Button>
            <Button size="sm" icon={Trash2} className="text-bad hover:text-bad" onClick={() => remove(picked)}>
              删除
            </Button>
            <Button size="sm" variant="ghost" onClick={() => setSelected(new Set())}>
              取消选择
            </Button>
          </>
        ) : (
          <>
            <Button size="sm" variant="primary" icon={Upload} disabled={!listing} onClick={() => upload(false)}>
              上传文件
            </Button>
            <Button size="sm" icon={FolderUp} disabled={!listing} onClick={() => upload(true)}>
              上传文件夹
            </Button>
            <span className="mx-1 hidden h-4 w-px bg-line2 @[520px]/files:block" />
            <Button size="sm" icon={FolderPlus} disabled={!listing} onClick={() => setNaming({ kind: "mkdir" })}>
              新建文件夹
            </Button>
            <Button size="sm" icon={FilePlus} disabled={!listing} onClick={() => setNaming({ kind: "create" })}>
              新建文件
            </Button>
          </>
        )}
        <div className="flex-1" />
        <label className="flex shrink-0 items-center gap-2 text-[12px] text-muted">
          <Switch size="sm" checked={showHidden} onChange={(v) => (localStorage.setItem("files-hidden", v ? "1" : "0"), setShowHidden(v))} />
          显示隐藏文件{!showHidden && hiddenCount > 0 ? `（${hiddenCount}）` : ""}
        </label>
        <Input icon={Search} placeholder="筛选文件名" value={filter} onChange={(e) => setFilter(e.target.value)} className="!h-7 w-[160px] text-[12.5px]" />
      </div>

      {/* 文件列表 */}
      <div className="relative flex min-h-0 flex-1 flex-col">
        {drag && (
          <div className="pointer-events-none absolute inset-x-0 bottom-4 z-[3] flex justify-center px-4">
            <span className="inline-flex h-9 max-w-full animate-pop items-center gap-2 rounded-full border border-accent/50 bg-panel2 px-3.5 text-[12.5px] text-fg shadow-2xl">
              <Upload size={13} className="shrink-0 text-accent" />
              <span className="truncate">
                松开鼠标，上传到 <span className="font-mono text-accent">{drag.dir || "当前目录"}</span>
              </span>
            </span>
          </div>
        )}
      <div className="min-h-0 flex-1 overflow-y-auto px-3 pb-4 @[640px]/files:px-5">
        {error ? (
          <Callout
            tone="bad"
            title="无法打开这个目录"
            action={
              <div className="flex shrink-0 gap-1.5">
                <Button size="sm" onClick={() => load(pathInput.trim() || "~")}>
                  重试
                </Button>
                <Button size="sm" onClick={() => load(listing?.path || "~")}>
                  {listing ? "返回" : "回到主目录"}
                </Button>
              </div>
            }
          >
            {error}
          </Callout>
        ) : !listing ? (
          <div className="flex items-center justify-center gap-2 py-24 text-[13px] text-muted">
            <Spinner /> 正在连接并读取目录…
          </div>
        ) : (
          <Card className={cn("overflow-hidden transition-[opacity,border-color]", loading && "opacity-60", drag && "!border-accent/70")}>
            <table className="w-full table-fixed text-[13px]">
              <thead className="sticky top-0 z-[1] bg-panel">
                <tr className="border-b border-line text-left text-[12px] text-muted">
                  <th className="w-10 py-2 pl-4">
                    <input
                      type="checkbox"
                      className="h-3.5 w-3.5 accent-[var(--c-accent)]"
                      checked={allPicked}
                      onChange={(e) => setSelected(e.target.checked ? new Set(shown.map((x) => x.path)) : new Set())}
                    />
                  </th>
                  {sortHead("name", "名称")}
                  {sortHead("size", "大小", "w-[88px] text-right")}
                  <th className="hidden w-[112px] px-3 py-2 font-medium @[860px]/files:table-cell">权限</th>
                  <th className="hidden w-[130px] px-3 py-2 font-medium @[980px]/files:table-cell">所有者</th>
                  {sortHead("time", "修改时间", "hidden w-[150px] @[640px]/files:table-cell")}
                  <th className="w-11" />
                </tr>
              </thead>
              <tbody>
                {shown.map((e) => {
                  const on = selected.has(e.path);
                  return (
                    <tr
                      key={e.path}
                      onContextMenu={(ev) => {
                        ev.preventDefault();
                        setMenu({ entry: e, x: ev.clientX, y: ev.clientY });
                      }}
                      onDoubleClick={() => void openEntry(e)}
                      data-drop-dir={e.isDir ? e.path : undefined}
                      className={cn("group border-b border-line last:border-0", drag && e.isDir && drag.dir === e.path ? "bg-accent/20" : on ? "bg-accent/8" : "hover:bg-panel2/60")}
                    >
                      <td className="py-1.5 pl-4">
                        <input type="checkbox" className="h-3.5 w-3.5 accent-[var(--c-accent)]" checked={on} onChange={() => toggle(e.path)} />
                      </td>
                      <td className="px-3 py-1.5">
                        <button type="button" onClick={() => void openEntry(e)} className="flex max-w-full items-center gap-2.5 text-left hover:text-accent">
                          {opening === e.path ? <LoaderCircle size={16} className="shrink-0 animate-spin text-accent" /> : <EntryIcon e={e} />}
                          <span className={cn("truncate", e.isDir && "font-medium")}>{e.name}</span>
                          {e.isLink && <span className="truncate font-mono text-[11.5px] text-muted">→ {e.linkTarget || "（失效的链接）"}</span>}
                        </button>
                      </td>
                      <td className="px-3 py-1.5 text-right text-[12px] text-fg2" style={{ fontVariantNumeric: "tabular-nums" }}>
                        {e.isDir ? "–" : formatBytes(e.size)}
                      </td>
                      <td className="hidden px-3 py-1.5 font-mono text-[11.5px] text-muted @[860px]/files:table-cell" title={`权限数字 ${e.perm}`}>
                        {e.mode}
                      </td>
                      <td className="hidden truncate px-3 py-1.5 text-[12px] text-muted @[980px]/files:table-cell">{ownerText(e)}</td>
                      <td className="hidden truncate px-3 py-1.5 text-[12px] text-muted @[640px]/files:table-cell" style={{ fontVariantNumeric: "tabular-nums" }}>
                        {formatDate(e.modTime, true)}
                      </td>
                      <td className="pr-2">
                        <button
                          type="button"
                          title="更多操作"
                          onClick={(ev) => {
                            const r = ev.currentTarget.getBoundingClientRect();
                            setMenu({ entry: e, x: r.right - 176, y: r.bottom + 4 });
                          }}
                          className="flex h-7 w-7 items-center justify-center rounded-lg text-muted opacity-0 transition-opacity hover:bg-panel3 hover:text-fg group-hover:opacity-100 focus-visible:opacity-100"
                        >
                          <Ellipsis size={15} />
                        </button>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
            {rows.length === 0 && (
              <div className="py-14 text-center text-[13px] text-muted">
                {filter ? "没有匹配的文件" : listing.entries.length > 0 ? `这里只有 ${hiddenCount} 个隐藏文件，打开右上角的「显示隐藏文件」可以看到` : "这个文件夹是空的，把文件或文件夹拖到这里就能上传"}
              </div>
            )}
            {rows.length > MAX_ROWS && (
              <div className="border-t border-line px-4 py-2 text-[12px] text-muted">
                这个目录有 {rows.length} 项，只显示前 {MAX_ROWS} 项。用右上角的筛选框输入文件名可以找到其余的。
              </div>
            )}
          </Card>
        )}
        {listing && !error && (
          <div className="mt-2 px-1 text-[12px] text-muted">
            {rows.filter((e) => e.isDir).length} 个文件夹，{rows.filter((e) => !e.isDir).length} 个文件 · 点击名称打开，右键查看更多操作 · 把文件或文件夹拖进窗口即可上传
          </div>
        )}
      </div>

      </div>

      <TransferPanel />

      {menu && <PopMenu at={{ x: menu.x, y: menu.y }} items={menuItems(menu.entry)} onClose={() => setMenu(null)} />}
      {naming && (
        <NameModal
          title={naming.kind === "mkdir" ? "新建文件夹" : naming.kind === "create" ? "新建文件" : `重命名「${naming.entry?.name}」`}
          label={naming.kind === "rename" ? "新名称" : `在 ${cwd} 下创建`}
          initial={naming.entry?.name ?? ""}
          confirmText={naming.kind === "rename" ? "重命名" : "创建"}
          onClose={() => setNaming(null)}
          onSubmit={submitName}
        />
      )}
      {chmod && (
        <ChmodModal
          entry={chmod}
          onClose={() => setChmod(null)}
          onSubmit={async (perm) => {
            try {
              await api.FilesChmod(serverId, chmod.path, perm);
              toast.ok("权限已修改", `${chmod.name} → ${perm}`);
              void load(cwd, true);
              return true;
            } catch (e) {
              toast.error("修改权限失败", e);
              return false;
            }
          }}
        />
      )}
      {editing && <EditorModal serverId={serverId} file={editing} onClose={() => setEditing(null)} onSaved={() => void load(cwd, true)} />}
    </div>
  );
}
