import { create } from "zustand";
import { api, type Transfer } from "./api";
import { formatBytes } from "./format";
import { toast } from "./store";

const active = (t: Transfer | undefined) => !!t && (t.status === "running" || t.status === "preparing");

interface TransferState {
  /** 传输任务，最新的在前 */
  list: Transfer[];
  /** 每完成一次上传加一，文件列表据此刷新 */
  uploadsDone: number;
  /** 最近一次完成的上传（用于判断是否需要刷新当前目录） */
  lastUpload: Transfer | null;
  load: () => Promise<void>;
  apply: (t: Transfer) => void;
  clear: () => Promise<void>;
}

export const useTransfers = create<TransferState>((set, get) => ({
  list: [],
  uploadsDone: 0,
  lastUpload: null,
  load: async () => {
    try {
      set({ list: (await api.FilesTransfers()) ?? [] });
    } catch (e) {
      console.error(e);
    }
  },
  apply: (t) => {
    const list = get().list;
    const prev = list.find((x) => x.id === t.id);
    set({ list: prev ? list.map((x) => (x.id === t.id ? t : x)) : [t, ...list] });
    if (!active(prev) && prev) return;
    if (active(t)) return;
    // 刚刚结束：提示结果
    const what = t.kind === "upload" ? "上传" : "下载";
    if (t.status === "done") {
      const note = t.skipped ? `，跳过了 ${t.skipped} 个符号链接或特殊文件` : "";
      toast.ok(`${what}完成`, `${t.title} · ${t.files} 个文件 · ${formatBytes(t.total)}${note}`);
      if (t.kind === "upload") set({ uploadsDone: get().uploadsDone + 1, lastUpload: t });
    } else if (t.status === "failed") toast.error(`${what}失败：${t.title}`, t.error);
    else if (t.status === "cancelled") toast.info(`已取消${what}`, t.title);
  },
  clear: async () => {
    await api.FilesClearTransfers();
    await get().load();
  },
}));

const binaryExt = new Set(
  "zip gz tgz bz2 xz zst 7z rar tar jar war iso img bin exe dll so o a deb rpm apk png jpg jpeg gif webp ico bmp tiff pdf mp3 mp4 mkv avi mov flac wav woff woff2 ttf otf eot db sqlite sqlite3 pyc class dat pack idx".split(" "),
);

export function extOf(name: string): string {
  const i = name.lastIndexOf(".");
  return i > 0 ? name.slice(i + 1).toLowerCase() : "";
}

/** 按扩展名判断是否明显是二进制文件（压缩包、图片、可执行文件等），这类文件直接走下载 */
export function looksBinary(name: string): boolean {
  return binaryExt.has(extOf(name));
}

export type FileKind = "folder" | "link" | "archive" | "image" | "code" | "text" | "binary";

export function kindOf(name: string, isDir: boolean, isLink: boolean): FileKind {
  if (isDir) return "folder";
  const ext = extOf(name);
  if ("zip gz tgz bz2 xz zst 7z rar tar jar war iso deb rpm".split(" ").includes(ext)) return "archive";
  if ("png jpg jpeg gif webp ico bmp svg tiff".split(" ").includes(ext)) return "image";
  if ("sh bash zsh py js ts go rs c h cpp java php rb pl lua sql json yaml yml toml xml html css conf cnf ini service timer".split(" ").includes(ext)) return "code";
  if (binaryExt.has(ext)) return "binary";
  return isLink ? "link" : "text";
}

/** 根据文件名选择编辑器的语法高亮 */
export function languageOf(path: string): string {
  const name = path.slice(path.lastIndexOf("/") + 1).toLowerCase();
  const ext = extOf(name);
  if (name === "dockerfile" || name.startsWith("dockerfile.") || ext === "dockerfile") return "dockerfile";
  if (["sh", "bash", "zsh", "bashrc", "profile"].includes(ext) || [".bashrc", ".profile", ".bash_profile", ".zshrc"].includes(name)) return "bash";
  if (ext === "py") return "python";
  if (["yml", "yaml"].includes(ext)) return "yaml";
  if (ext === "json") return "json";
  if (["js", "mjs", "cjs", "ts"].includes(ext)) return "javascript";
  if (ext === "toml") return "toml";
  if (["xml", "html", "htm", "svg"].includes(ext)) return "xml";
  if (ext === "sql") return "sql";
  if (ext === "conf" && /nginx|sites-|conf\.d/.test(path.toLowerCase())) return "nginx";
  if (["conf", "cnf", "ini", "env", "properties", "service", "timer", "socket", "mount", "target", "cfg"].includes(ext) || name === ".env") return "properties";
  return "text";
}

/** 常用位置：点一下直接跳转 */
export const COMMON_DIRS = [
  { label: "主目录", path: "~" },
  { label: "根目录 /", path: "/" },
  { label: "/etc（系统配置）", path: "/etc" },
  { label: "/etc/nginx", path: "/etc/nginx" },
  { label: "/var/log（日志）", path: "/var/log" },
  { label: "/var/www（网站）", path: "/var/www" },
  { label: "/opt", path: "/opt" },
  { label: "/tmp", path: "/tmp" },
];

/** 把八进制权限拆成 9 个读 / 写 / 执行位（忽略最高位的特殊权限） */
export function permBits(perm: string): boolean[] {
  const digits = perm.slice(-3).padStart(3, "0");
  const out: boolean[] = [];
  for (const d of digits) {
    const n = Number(d) || 0;
    out.push((n & 4) !== 0, (n & 2) !== 0, (n & 1) !== 0);
  }
  return out;
}

export function bitsToPerm(bits: boolean[], special = ""): string {
  let out = special;
  for (let i = 0; i < 9; i += 3) out += String((bits[i] ? 4 : 0) + (bits[i + 1] ? 2 : 0) + (bits[i + 2] ? 1 : 0));
  return out;
}
