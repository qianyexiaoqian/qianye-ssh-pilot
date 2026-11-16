export function cn(...parts: Array<string | false | null | undefined | 0>): string {
  return parts.filter(Boolean).join(" ");
}

export function formatBytes(n: number | undefined, digits = 1): string {
  if (!n || n < 0) return "0 B";
  const units = ["B", "KB", "MB", "GB", "TB", "PB"];
  let i = 0;
  let v = n;
  while (v >= 1024 && i < units.length - 1) {
    v /= 1024;
    i++;
  }
  return `${v.toFixed(i === 0 ? 0 : digits)} ${units[i]}`;
}

export function formatDuration(sec: number): string {
  if (!sec || sec < 0) return "-";
  const d = Math.floor(sec / 86400);
  const h = Math.floor((sec % 86400) / 3600);
  const m = Math.floor((sec % 3600) / 60);
  if (d > 0) return `${d} 天 ${h} 小时`;
  if (h > 0) return `${h} 小时 ${m} 分`;
  return `${m} 分钟`;
}

export function formatMs(ms: number): string {
  if (ms < 1000) return `${ms} ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)} s`;
  return `${Math.floor(ms / 60000)} 分 ${Math.round((ms % 60000) / 1000)} 秒`;
}

export function timeAgo(ts: number): string {
  if (!ts) return "-";
  const diff = Date.now() - ts;
  if (diff < 0) return "刚刚";
  const s = Math.floor(diff / 1000);
  if (s < 45) return "刚刚";
  const m = Math.floor(s / 60);
  if (m < 60) return `${m} 分钟前`;
  const h = Math.floor(m / 60);
  if (h < 24) return `${h} 小时前`;
  const d = Math.floor(h / 24);
  if (d < 30) return `${d} 天前`;
  return formatDate(ts);
}

export function formatDate(ts: number, withTime = false): string {
  if (!ts) return "-";
  const d = new Date(ts);
  const p = (n: number) => String(n).padStart(2, "0");
  const date = `${d.getFullYear()}-${p(d.getMonth() + 1)}-${p(d.getDate())}`;
  return withTime ? `${date} ${p(d.getHours())}:${p(d.getMinutes())}` : date;
}

export function daysUntil(ts: number): number {
  return Math.floor((ts - Date.now()) / 86400000);
}

/** 证书剩余天数对应的状态：critical ≤7 天或已过期，warning ≤ warnDays */
export function certTone(days: number, warnDays = 30): "ok" | "warn" | "bad" {
  if (days <= 7) return "bad";
  if (days <= warnDays) return "warn";
  return "ok";
}

/** 资源使用率对应的状态 */
export function usageTone(pct: number): "accent" | "warn" | "bad" {
  if (pct >= 95) return "bad";
  if (pct >= 85) return "warn";
  return "accent";
}

export function compact(n: number): string {
  if (n >= 1_000_000) return `${(n / 1_000_000).toFixed(1)}M`;
  if (n >= 10_000) return `${(n / 1000).toFixed(1)}K`;
  return n.toLocaleString();
}

const ansiRe = /\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b\][^\x07]*(\x07|\x1b\\)|\r(?!\n)/g;

/** 去掉 ANSI 转义序列（脚本输出在普通文本框中显示时使用） */
export function stripAnsi(s: string): string {
  return s.replace(ansiRe, "");
}

export function initials(name: string): string {
  const t = name.trim();
  if (!t) return "?";
  const ascii = t.replace(/[^A-Za-z0-9]/g, "");
  if (ascii.length >= 2) return ascii.slice(0, 2).toUpperCase();
  return t.slice(0, 1).toUpperCase();
}

/** 根据字符串生成稳定的色相（用于服务器头像） */
export function hueOf(s: string): number {
  let h = 0;
  for (let i = 0; i < s.length; i++) h = (h * 31 + s.charCodeAt(i)) % 360;
  return h;
}

/** 网络速率（字节/秒） */
export function formatRate(bps: number | undefined): string {
  if (!bps || bps < 1) return "0 B/s";
  return `${formatBytes(bps, bps >= 1024 * 1024 ? 1 : 0)}/s`;
}

/** 负载相对 CPU 核数的状态 */
export function loadTone(load: number, cpus: number): "accent" | "warn" | "bad" {
  const per = load / Math.max(cpus, 1);
  if (per >= 2) return "bad";
  if (per >= 1) return "warn";
  return "accent";
}

const sshValueFlags = ["-i", "-o", "-J", "-F", "-L", "-R", "-D", "-b", "-c", "-e", "-m", "-W", "-w", "-E", "-S", "-I", "-B", "-Q"];

/** 从粘贴的内容里识别 SSH 目标：ssh -p 2222 root@1.2.3.4、root@host:2222、host:22、[::1]:22 等 */
export function parseSSHTarget(text: string): { host: string; user?: string; port?: number } | null {
  let t = text.trim();
  if (!t || /[\r\n]/.test(t)) return null;
  let port: number | undefined;
  let user: string | undefined;
  if (/^ssh\s/i.test(t)) {
    const tokens = t.split(/\s+/).slice(1);
    let target = "";
    for (let i = 0; i < tokens.length; i++) {
      const tk = tokens[i];
      if (tk === "-p") port = Number(tokens[++i]);
      else if (/^-p\d+$/.test(tk)) port = Number(tk.slice(2));
      else if (tk === "-l") user = tokens[++i];
      else if (sshValueFlags.includes(tk)) i++;
      else if (tk.startsWith("-")) continue;
      else if (!target) target = tk;
    }
    t = target;
  }
  t = t.replace(/^ssh:\/\//i, "").replace(/\/$/, "");
  const at = t.lastIndexOf("@");
  if (at >= 0) {
    user = t.slice(0, at) || user;
    t = t.slice(at + 1);
  }
  let m = /^\[([^\]]+)\](?::(\d+))?$/.exec(t);
  if (m) {
    t = m[1];
    if (m[2]) port = Number(m[2]);
  } else if ((m = /^([^:]+):(\d+)$/.exec(t))) {
    t = m[1];
    port = Number(m[2]);
  }
  if (!t || /\s/.test(t)) return null;
  return { host: t, user, port: port && port > 0 && port < 65536 ? port : undefined };
}
