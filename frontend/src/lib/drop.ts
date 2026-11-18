import { runtime } from "./api";

// 从资源管理器拖进窗口的文件：Wails 解析出本地路径后回调（x、y 是松开鼠标的位置）。
export type DropHandler = (x: number, y: number, paths: string[]) => void;

let handler: DropHandler | null = null;

/** 当前界面接收拖入的文件（文件管理窗口用它上传）；传 null 取消 */
export function setDropHandler(h: DropHandler | null) {
  handler = h;
}

// 接收区：页面里的一块区域（例如 AI 对话）。文件落在哪个区域里，就交给哪个区域处理。
interface DropZone {
  el: HTMLElement;
  onPaths: (paths: string[]) => void;
}
const zones = new Set<DropZone>();

/** 把一个元素登记为接收区，返回取消登记的函数 */
export function addDropZone(el: HTMLElement, onPaths: (paths: string[]) => void): () => void {
  const zone = { el, onPaths };
  zones.add(zone);
  return () => void zones.delete(zone);
}

function zoneAt(x: number, y: number): DropZone | undefined {
  const target = document.elementFromPoint(x, y);
  if (!target) return undefined;
  // 接收区互相嵌套时取最里面的那个
  let best: DropZone | undefined;
  for (const z of zones) if (z.el.contains(target) && (!best || best.el.contains(z.el))) best = z;
  return best;
}

/** 应用启动时调用一次。没有界面接收时交给 fallback（例如提示用户去哪里上传） */
export function initFileDrop(fallback: (paths: string[]) => void) {
  runtime.OnFileDrop((x, y, paths) => {
    const list = (paths ?? []).filter(Boolean);
    if (!list.length) return;
    const zone = zoneAt(x, y);
    if (zone) zone.onPaths(list);
    else if (handler) handler(x, y, list);
    else fallback(list);
  }, false);
}

/** 拖动的是不是来自系统的文件（而不是页面里的文字、链接） */
export function isFileDrag(e: DragEvent): boolean {
  return !!e.dataTransfer && Array.from(e.dataTransfer.types).includes("Files");
}
