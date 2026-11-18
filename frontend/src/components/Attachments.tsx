import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { File as FileIcon, FileArchive, FileText, ImageOff, Paperclip, X } from "lucide-react";
import type { Attachment } from "../lib/api";
import { attachmentURL } from "../lib/attach";
import { addDropZone, isFileDrag } from "../lib/drop";
import { cn, formatBytes } from "../lib/format";

// 随消息发给 AI 的图片和文件在界面上的样子：输入框里等待发送的、对话里已经发出的。

const archiveRe = /\.(zip|rar|7z|tar|gz|tgz|bz2|xz|zst|jar|war|deb|rpm|apk|iso|img)$/i;

function iconOf(a: Attachment) {
  return a.text ? FileText : archiveRe.test(a.name) ? FileArchive : FileIcon;
}

/** 点开图片看大图；点空白处或按 Esc 关闭。标题栏留在外面，窗口仍然可以拖动 */
export function ImageViewer({ src, name, onClose }: { src: string; name: string; onClose: () => void }) {
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== "Escape") return;
      e.stopPropagation();
      onClose();
    };
    window.addEventListener("keydown", onKey, true);
    return () => window.removeEventListener("keydown", onKey, true);
  }, [onClose]);
  return createPortal(
    <div className="fixed inset-x-0 top-[40px] bottom-0 z-[65] flex animate-fade-in items-center justify-center bg-black/80 p-4 sm:p-8" onClick={onClose} data-image-viewer>
      <img src={src} alt={name} className="max-h-full max-w-full rounded-lg object-contain shadow-2xl" onClick={(e) => e.stopPropagation()} />
      <button type="button" onClick={onClose} title="关闭（Esc）" className="absolute top-3 right-3 flex h-9 w-9 items-center justify-center rounded-full bg-white/12 text-white hover:bg-white/25">
        <X size={18} />
      </button>
      <div className="absolute bottom-3 left-1/2 max-w-[80%] -translate-x-1/2 truncate rounded-full bg-black/65 px-3 py-1 text-[12px] text-white/85">{name}</div>
    </div>,
    document.body,
  );
}

function RemoveButton({ onClick }: { onClick: () => void }) {
  return (
    <button
      type="button"
      title="移除"
      onClick={(e) => {
        e.stopPropagation();
        onClick();
      }}
      className="absolute -top-1.5 -right-1.5 flex h-[18px] w-[18px] items-center justify-center rounded-full border border-line2 bg-panel3 text-fg2 shadow-md hover:bg-bad hover:text-white"
    >
      <X size={11} />
    </button>
  );
}

const FULL_MAX_H = 240;

/** shape：thumb 是输入框里的小方块；tile 是对话里图片较多时的方块；full 按原来的比例显示，宽度不超过 max、高度不超过 FULL_MAX_H */
function Picture({ a, src, shape, max, onOpen }: { a: Attachment; src: string; shape: "thumb" | "tile" | "full"; max: number; onOpen: () => void }) {
  const draft = shape === "thumb";
  const [broken, setBroken] = useState(false);
  if (broken)
    return (
      <div className={cn("flex items-center justify-center gap-1.5 rounded-xl border border-line2 bg-panel2 text-[11.5px] text-muted", draft ? "h-[52px] w-[52px]" : "h-[52px] px-3")} title={`${a.name}：图片已经找不到了`}>
        <ImageOff size={14} className="shrink-0" />
        {!draft && <span className="max-w-[160px] truncate">{a.name}</span>}
      </div>
    );
  return (
    <img
      src={src}
      alt={a.name}
      title={`${a.name}（点击查看大图）`}
      loading="lazy"
      draggable={false}
      onClick={onOpen}
      onError={() => setBroken(true)}
      className={cn("block cursor-zoom-in rounded-xl border border-line2 bg-panel2 object-cover", shape === "thumb" ? "h-[52px] w-[52px]" : shape === "tile" ? "h-[120px] w-[120px]" : "h-auto max-h-[240px] max-w-full")}
      // 按原来的比例占位（又宽又扁的按宽度限制，又窄又高的按高度限制），图片加载出来时内容不会跳动
      style={shape !== "full" || !a.width || !a.height ? undefined : { aspectRatio: `${a.width} / ${a.height}`, width: Math.min(a.width, max, Math.round((FULL_MAX_H * a.width) / a.height)) }}
    />
  );
}

/**
 * 一组附件：图片显示缩略图（点击看大图），文件显示名称和大小。
 * sessionId 为 null 表示还没有发出去（输入框里），图片显示成小方块；onRemove 不为空时每一项带移除按钮。
 */
export function AttachmentList({ items, sessionId, onRemove, align = "start" }: { items: Attachment[]; sessionId: string | null; onRemove?: (a: Attachment) => void; align?: "start" | "end" }) {
  const [view, setView] = useState<Attachment | null>(null);
  if (!items.length) return null;
  const draft = sessionId === null;
  const images = items.filter((a) => a.kind === "image");
  // 输入框里按添加的顺序排成一行；对话里图片一行、文件一行，一两张图片显示得大一些，更多时排成方块
  const rows = draft ? [items] : [images, items.filter((a) => a.kind !== "image")].filter((r) => r.length > 0);
  const shape = draft ? "thumb" : images.length > 2 ? "tile" : "full";
  return (
    <div className={cn("flex max-w-full flex-col gap-2", align === "end" ? "items-end" : "items-start")} data-attachments>
      {rows.map((row, i) => (
        <div key={i} className={cn("flex max-w-full flex-wrap items-start gap-2", align === "end" && "justify-end")}>
          {row.map((a) => {
            const Icon = iconOf(a);
            return (
              <div key={a.id} className="relative max-w-full" data-attachment={a.kind}>
                {a.kind === "image" ? (
                  <Picture a={a} src={attachmentURL(sessionId, a)} shape={shape} max={images.length > 1 ? 240 : 320} onOpen={() => setView(a)} />
                ) : (
                  <div className="flex h-[52px] max-w-[240px] items-center gap-2.5 rounded-xl border border-line2 bg-panel2 pr-3.5 pl-2.5" title={a.path || a.name}>
                    <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-lg bg-accent/12 text-accent">
                      <Icon size={15} />
                    </div>
                    <div className="min-w-0 text-left">
                      <div className="truncate text-[12.5px] leading-tight text-fg">{a.name}</div>
                      <div className="mt-0.5 text-[11px] leading-tight text-muted">{formatBytes(a.size)}</div>
                    </div>
                  </div>
                )}
                {onRemove && <RemoveButton onClick={() => onRemove(a)} />}
              </div>
            );
          })}
        </div>
      ))}
      {view && <ImageViewer src={attachmentURL(sessionId, view)} name={view.name} onClose={() => setView(null)} />}
    </div>
  );
}

/**
 * 把一块区域变成附件的接收区：从资源管理器拖文件进来，松开后回调文件的本地路径。
 * 返回的 ref 挂在区域的元素上（需要 position: relative），over 为 true 时显示 DropHint。
 */
export function useAttachDrop<T extends HTMLElement>(onPaths: (paths: string[]) => void) {
  const ref = useRef<T>(null);
  const [over, setOver] = useState(false);
  const cb = useRef(onPaths);
  cb.current = onPaths;
  useEffect(() => {
    const el = ref.current;
    if (!el) return;
    let depth = 0;
    const enter = (e: DragEvent) => {
      if (!isFileDrag(e)) return;
      depth++;
      setOver(true);
    };
    const leave = (e: DragEvent) => {
      if (!isFileDrag(e)) return;
      depth = Math.max(0, depth - 1);
      if (depth === 0) setOver(false);
    };
    const end = () => {
      depth = 0;
      setOver(false);
    };
    el.addEventListener("dragenter", enter);
    el.addEventListener("dragleave", leave);
    // 在别处松开也要收起提示
    window.addEventListener("drop", end);
    const off = addDropZone(el, (paths) => {
      end();
      cb.current(paths);
    });
    return () => {
      el.removeEventListener("dragenter", enter);
      el.removeEventListener("dragleave", leave);
      window.removeEventListener("drop", end);
      off();
    };
  }, []);
  return { ref, over };
}

/** 文件拖到接收区上方时的提示 */
export function DropHint({ show, compact }: { show: boolean; compact?: boolean }) {
  if (!show) return null;
  return (
    <div className="pointer-events-none absolute inset-2 z-30 flex items-center justify-center rounded-2xl border-2 border-dashed border-accent/60 bg-bg/80 backdrop-blur-[2px]" data-drop-hint>
      <div className={cn("flex items-center gap-2 rounded-full bg-accent/15 font-medium text-accent", compact ? "px-3 py-1.5 text-[12.5px]" : "px-4 py-2 text-[13.5px]")}>
        <Paperclip size={compact ? 14 : 16} />
        松开鼠标，把文件交给 AI
      </div>
    </div>
  );
}
