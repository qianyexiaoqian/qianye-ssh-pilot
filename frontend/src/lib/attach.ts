import { api, type AttachResult, type Attachment } from "./api";
import { toast } from "./store";

// 随消息发给 AI 的图片和文件。添加后先由后端保管（图片会处理成合适的大小），发送时只传附件的 ID。

/** 一条消息最多带的附件数量（与后端一致） */
export const MAX_ATTACHMENTS = 10;
/** 粘贴进来的内容要整个经过界面传给后端，太大的请用户拖进来或点按钮选择 */
const MAX_PASTED = 25 * 1024 * 1024;

/** 界面显示图片用的地址。sessionId 为 null 表示还没有发出去的附件 */
export function attachmentURL(sessionId: string | null, a: Attachment): string {
  return `/attachment/${sessionId ?? "draft"}/${a.id}`;
}

function report(res: AttachResult | null | undefined): Attachment[] {
  const errors = res?.errors ?? [];
  if (errors.length) toast.warn(errors.length === 1 ? "有一个文件没能添加" : `有 ${errors.length} 个文件没能添加`, errors.join("\n"));
  return res?.added ?? [];
}

/** 打开系统的文件选择框 */
export async function pickAttachments(): Promise<Attachment[]> {
  try {
    return report(await api.AgentPickFiles());
  } catch (e) {
    toast.error("无法添加文件", e);
    return [];
  }
}

/** 添加拖进窗口的文件 */
export async function attachPaths(paths: string[]): Promise<Attachment[]> {
  try {
    return report(await api.AgentAttachPaths(paths));
  } catch (e) {
    toast.error("无法添加文件", e);
    return [];
  }
}

function readBase64(file: File): Promise<string> {
  return new Promise((resolve, reject) => {
    const r = new FileReader();
    r.onload = () => resolve(String(r.result).replace(/^data:[^,]*,/, ""));
    r.onerror = () => reject(r.error);
    r.readAsDataURL(file);
  });
}

/** 浏览器把粘贴进来的截图都叫 image.png：换成带时间的名字，几张截图才分得开 */
function pastedName(f: File): string {
  if (f.name !== "image.png" || !f.type.startsWith("image/")) return f.name;
  const d = new Date();
  const p = (n: number) => String(n).padStart(2, "0");
  return `截图 ${p(d.getHours())}-${p(d.getMinutes())}-${p(d.getSeconds())}.png`;
}

/** 添加粘贴进来的截图、图片或文件 */
export async function attachPasted(files: File[]): Promise<Attachment[]> {
  const out: Attachment[] = [];
  for (const f of files) {
    if (f.size > MAX_PASTED) {
      toast.warn(`「${f.name}」太大，不能用粘贴的方式添加`, "请把文件拖进对话，或点输入框左下角的回形针按钮选择。");
      continue;
    }
    try {
      out.push(await api.AgentAttachData(pastedName(f), await readBase64(f)));
    } catch (e) {
      toast.error(`「${f.name}」没能添加`, e);
    }
  }
  return out;
}

/** 丢弃还没有发出去的附件（后端删除保存的那一份） */
export function discardAttachments(list: Attachment[]) {
  for (const a of list) void api.AgentDiscardAttachment(a.id).catch(() => {});
}

/** 把新添加的附件接在已有的后面；超过上限的部分丢弃并提示 */
export function mergeAttachments(current: Attachment[], added: Attachment[]): Attachment[] {
  const room = Math.max(0, MAX_ATTACHMENTS - current.length);
  if (added.length > room) {
    discardAttachments(added.slice(room));
    toast.warn(`一条消息最多带 ${MAX_ATTACHMENTS} 个附件`, "多出来的没有添加，可以分几条消息发送。");
  }
  return [...current, ...added.slice(0, room)];
}
