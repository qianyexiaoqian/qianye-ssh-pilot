import { useEffect, useLayoutEffect, useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode, type RefObject } from "react";
import { createPortal } from "react-dom";
import { cn } from "./format";

// 拖动排序：按住一项不放（长按）把它提起来，拖到新的位置松手。
// 服务器卡片（网格）、终端标签（横排）、证书中心的分组和证书（竖排）共用。
//
// 做法：被拖的那一项留在原地当占位（由调用方画成虚框），另外画一份跟着指针走的「浮起来的副本」；
// 指针每移动一下，就看副本的中心落在了谁的位置上，把占位挪过去，其余各项用一小段动画滑到新位置。
// 判断落点用的是布局位置（offsetLeft / offsetTop），不受正在播放的滑动动画影响，所以不会来回抖。

/** 鼠标按住后移动超过这么多像素就立即开始拖动，不必等长按的时间 */
const MOVE_START = 6;
/** 指针离滚动区域的边缘这么近时自动滚动 */
const EDGE = 48;
const SCROLL_STEP = 14;
const EASE = "cubic-bezier(0.2, 0.8, 0.2, 1)";

interface DragState {
  id: string;
  w: number;
  h: number;
  /** 按下的位置相对这一项左上角的偏移 */
  offX: number;
  offY: number;
  /** 按下时这一项在窗口里的位置 */
  left: number;
  top: number;
  /** 松手之后副本要滑回去的位置 */
  drop?: { x: number; y: number };
}

export interface SortableOptions {
  /** 当前的顺序 */
  ids: string[];
  /** 松手时顺序有变化才会调用。调用方应当立刻（同步）按新的顺序显示，再去保存 */
  onReorder: (ids: string[]) => void;
  disabled?: boolean;
  /** grid：卡片网格，上下左右都能拖；x：横排的标签，只能左右拖；y：竖排的列表，只能上下拖 */
  axis?: Axis;
  /** 按住多久算长按（毫秒） */
  delay?: number;
  /**
   * 可以把一项拖到列表之外的某个目标上松手（例如把证书拖到另一个分组的标题上）：
   * dropAttr 是这些目标身上的属性名，属性的值是目标的标识。拖到目标上时不调整顺序，松手后调用 onDropTo。
   */
  dropAttr?: string;
  onDropTo?: (id: string, target: string) => void;
  /** 拖动过程中指针移到了哪个目标上（离开时为 null），用来给目标加上高亮 */
  onHover?: (target: string | null) => void;
}

type Axis = "grid" | "x" | "y";

export interface Sortable {
  /** 应当按这个顺序渲染：拖动过程中是临时的顺序 */
  order: string[];
  /** 正在被拖动的那一项（它留在原地当占位） */
  draggingId: string | null;
  /** 展开到每一项的最外层元素上；它们共同的父元素要有 position: relative */
  itemProps: (id: string) => { ref: (el: HTMLElement | null) => void; onPointerDown: (e: ReactPointerEvent<HTMLElement>) => void; "data-sort-id": string };
  /** 交给 SortOverlay */
  drag: DragState | null;
  overlayRef: RefObject<HTMLDivElement | null>;
  overlayPos: () => { x: number; y: number };
}

function scrollParent(el: HTMLElement, axis: Axis): HTMLElement | null {
  for (let p: HTMLElement | null = el.parentElement; p && p !== document.body; p = p.parentElement) {
    const style = getComputedStyle(p);
    const overflow = axis === "x" ? style.overflowX : style.overflowY;
    const room = axis === "x" ? p.scrollWidth > p.clientWidth : p.scrollHeight > p.clientHeight;
    if (room && (overflow === "auto" || overflow === "scroll")) return p;
  }
  return null;
}

export function useSortable({ ids, onReorder, disabled = false, axis = "grid", delay = 220, dropAttr, onDropTo, onHover }: SortableOptions): Sortable {
  const [drag, setDrag] = useState<DragState | null>(null);
  const [working, setWorking] = useState<string[] | null>(null);
  const els = useRef(new Map<string, HTMLElement>());
  const overlayRef = useRef<HTMLDivElement | null>(null);
  // 指针事件和动画帧里要读到最新的值，放在 ref 里而不是闭包里
  const live = useRef({
    ids,
    onReorder,
    axis,
    dropAttr,
    onDropTo,
    onHover,
    /** 指针正停在哪个目标上（见 dropAttr） */
    hover: null as string | null,
    press: null as { id: string; x: number; y: number; mouse: boolean; timer: number } | null,
    drag: null as DragState | null,
    working: null as string[] | null,
    x: 0,
    y: 0,
    raf: 0,
    dropTimer: 0,
    scroller: null as HTMLElement | null,
    flip: null as Map<string, DOMRect> | null,
    off: [] as (() => void)[],
  });
  live.current.ids = ids;
  live.current.onReorder = onReorder;
  live.current.axis = axis;
  live.current.dropAttr = dropAttr;
  live.current.onDropTo = onDropTo;
  live.current.onHover = onHover;

  const overlayPos = () => {
    const L = live.current;
    const d = L.drag;
    if (!d) return { x: 0, y: 0 };
    return { x: L.axis === "y" ? d.left : L.x - d.offX, y: L.axis === "x" ? d.top : L.y - d.offY };
  };

  /** 指针是不是停在列表之外的某个目标上 */
  const setHover = (target: string | null) => {
    const L = live.current;
    if (L.hover === target) return;
    L.hover = target;
    // 停在目标上时让副本变淡：它比目标（一行标题）大得多，不然会把目标的高亮整个挡住
    if (overlayRef.current) overlayRef.current.style.opacity = target === null ? "" : "0.5";
    L.onHover?.(target);
  };
  const findTarget = () => {
    const L = live.current;
    if (!L.dropAttr) return null;
    const el = document.elementFromPoint(L.x, L.y)?.closest(`[${L.dropAttr}]`);
    return el ? el.getAttribute(L.dropAttr) : null;
  };

  const snapshot = () => {
    live.current.flip = new Map([...els.current].map(([id, el]) => [id, el.getBoundingClientRect()]));
  };

  /** 副本的中心落在了谁的位置上，就把占位挪到那里 */
  const retarget = () => {
    const L = live.current;
    const d = L.drag;
    const order = L.working;
    const self = d && els.current.get(d.id);
    const parent = self?.offsetParent as HTMLElement | null;
    if (!d || !order || !self || !parent) return;
    const pr = parent.getBoundingClientRect();
    const at = overlayPos();
    const cx = at.x + d.w / 2 - pr.left - parent.clientLeft + parent.scrollLeft;
    const cy = at.y + d.h / 2 - pr.top - parent.clientTop + parent.scrollTop;
    // 竖排的列表按指针的位置算：各项高矮不一（展开的分组很高），副本的中心离指针可能很远
    const py = L.y - pr.top - parent.clientTop + parent.scrollTop;
    const from = order.indexOf(d.id);
    let to = from;
    for (let i = 0; i < order.length && to === from; i++) {
      const el = i === from ? undefined : els.current.get(order[i]);
      if (!el) continue;
      if (L.axis === "y") {
        if (py < el.offsetTop || py >= el.offsetTop + el.offsetHeight) continue;
        const mid = el.offsetTop + el.offsetHeight / 2;
        if (i > from ? py > mid : py < mid) to = i;
        continue;
      }
      if (cx < el.offsetLeft || cx >= el.offsetLeft + el.offsetWidth) continue;
      if (L.axis === "x") {
        // 标签宽窄不一：要越过对方的中线才换位置，否则换完马上又会被判回去
        const mid = el.offsetLeft + el.offsetWidth / 2;
        if (i > from ? cx > mid : cx < mid) to = i;
      } else if (cy >= el.offsetTop && cy < el.offsetTop + el.offsetHeight) to = i;
    }
    if (to === from) return;
    const next = order.slice();
    next.splice(from, 1);
    next.splice(to, 0, d.id);
    snapshot();
    L.working = next;
    setWorking(next);
  };

  const place = () => {
    const el = overlayRef.current;
    if (!el || !live.current.drag) return;
    const at = overlayPos();
    el.style.transform = `translate3d(${at.x}px, ${at.y}px, 0)`;
  };

  const tick = () => {
    const L = live.current;
    if (!L.drag) return;
    const sc = L.scroller;
    if (sc) {
      const r = sc.getBoundingClientRect();
      const before = L.axis === "x" ? sc.scrollLeft : sc.scrollTop;
      if (L.axis === "x") {
        if (L.x < r.left + EDGE) sc.scrollLeft -= SCROLL_STEP;
        else if (L.x > r.right - EDGE) sc.scrollLeft += SCROLL_STEP;
      } else if (L.y < r.top + EDGE) sc.scrollTop -= SCROLL_STEP;
      else if (L.y > r.bottom - EDGE) sc.scrollTop += SCROLL_STEP;
      if ((L.axis === "x" ? sc.scrollLeft : sc.scrollTop) !== before) retarget();
    }
    L.raf = requestAnimationFrame(tick);
  };

  /** 松开所有监听；不改变显示 */
  const release = () => {
    const L = live.current;
    if (L.press) window.clearTimeout(L.press.timer);
    L.press = null;
    cancelAnimationFrame(L.raf);
    L.off.forEach((fn) => fn());
    L.off = [];
    document.body.classList.remove("sorting");
  };

  const begin = () => {
    const L = live.current;
    const p = L.press;
    const el = p && els.current.get(p.id);
    if (!p || !el || L.drag) return;
    window.clearTimeout(p.timer);
    window.clearTimeout(L.dropTimer);
    const r = el.getBoundingClientRect();
    const d: DragState = { id: p.id, w: r.width, h: r.height, offX: p.x - r.left, offY: p.y - r.top, left: r.left, top: r.top };
    L.drag = d;
    L.working = L.ids.slice();
    L.scroller = scrollParent(el, L.axis);
    document.body.classList.add("sorting");
    window.getSelection()?.removeAllRanges();
    setWorking(L.working);
    setDrag(d);
    L.raf = requestAnimationFrame(tick);
  };

  const finish = (commit: boolean) => {
    const L = live.current;
    const d = L.drag;
    const order = L.working;
    const target = L.hover;
    release();
    L.drag = null;
    L.working = null;
    setHover(null);
    if (!d) return;
    // 拖完松手不算一次点击：否则会触发按下的那个位置上的操作（例如复制名称）
    const swallow = (ev: MouseEvent) => {
      ev.stopPropagation();
      ev.preventDefault();
    };
    window.addEventListener("click", swallow, true);
    window.setTimeout(() => window.removeEventListener("click", swallow, true), 0);
    const clear = () => {
      setDrag(null);
      setWorking(null);
    };
    if (!commit || !order) {
      // 取消：各项滑回原来的位置
      snapshot();
      clear();
      return;
    }
    if (target !== null) {
      // 拖到了列表之外的目标上：顺序不变，交给调用方处理
      snapshot();
      clear();
      L.onDropTo?.(d.id, target);
      return;
    }
    if (order.join("\n") !== L.ids.join("\n")) L.onReorder(order);
    // 让浮着的副本滑进占位，再收起来
    const slot = els.current.get(d.id);
    slot?.getAnimations().forEach((a) => a.cancel());
    const r = slot?.getBoundingClientRect();
    if (!r) return clear();
    setDrag({ ...d, drop: { x: r.left, y: r.top } });
    L.dropTimer = window.setTimeout(clear, 180);
  };

  const onPointerDown = (id: string) => (e: ReactPointerEvent<HTMLElement>) => {
    const L = live.current;
    if (disabled || e.button !== 0 || L.press || L.drag) return;
    const item = e.currentTarget;
    const target = e.target as HTMLElement;
    // 从弹出菜单里冒泡上来的事件（菜单画在别处）不算；按在按钮、输入框上是在操作它们，不是要拖动。
    // 带 data-drag-ok 的例外（点一下复制的名称和地址）：它们占着卡片上最顺手的位置，按住照样可以拖
    if (!item.contains(target)) return;
    const control = target.closest("button, a, input, textarea, select, [data-no-drag]");
    if (control && control !== item && item.contains(control) && !control.hasAttribute("data-drag-ok")) return;
    L.x = e.clientX;
    L.y = e.clientY;
    L.press = { id, x: e.clientX, y: e.clientY, mouse: e.pointerType === "mouse", timer: window.setTimeout(begin, delay) };
    const move = (ev: PointerEvent) => {
      L.x = ev.clientX;
      L.y = ev.clientY;
      if (L.drag) {
        ev.preventDefault();
        place();
        const over = findTarget();
        setHover(over);
        if (over === null) retarget();
      } else if (L.press && Math.hypot(ev.clientX - L.press.x, ev.clientY - L.press.y) > MOVE_START) {
        // 鼠标：按住一拖就开始。触摸屏：按住不动才是拖动，直接滑是在滚动页面
        if (L.press.mouse) begin();
        else release();
      }
    };
    const up = () => (L.drag ? finish(true) : release());
    const cancel = () => (L.drag ? finish(false) : release());
    const key = (ev: KeyboardEvent) => {
      if (ev.key !== "Escape" || !L.drag) return;
      ev.preventDefault();
      ev.stopPropagation();
      finish(false);
    };
    window.addEventListener("pointermove", move);
    window.addEventListener("pointerup", up);
    window.addEventListener("pointercancel", cancel);
    window.addEventListener("blur", cancel);
    window.addEventListener("keydown", key, true);
    L.off = [
      () => window.removeEventListener("pointermove", move),
      () => window.removeEventListener("pointerup", up),
      () => window.removeEventListener("pointercancel", cancel),
      () => window.removeEventListener("blur", cancel),
      () => window.removeEventListener("keydown", key, true),
    ];
  };

  // 顺序变了：其余各项从原来的位置滑到新的位置
  useLayoutEffect(() => {
    const prev = live.current.flip;
    live.current.flip = null;
    if (!prev) return;
    for (const [id, el] of els.current) {
      const was = prev.get(id);
      if (!was) continue;
      el.getAnimations().forEach((a) => a.cancel());
      const now = el.getBoundingClientRect();
      const dx = was.left - now.left;
      const dy = was.top - now.top;
      if (Math.abs(dx) < 1 && Math.abs(dy) < 1) continue;
      el.animate([{ transform: `translate(${dx}px, ${dy}px)` }, { transform: "none" }], { duration: 200, easing: EASE });
    }
  }, [working]);

  useEffect(
    () => () => {
      release();
      window.clearTimeout(live.current.dropTimer);
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  );

  // 拖动过程中列表本身变了（有项目被删除或新增）：临时顺序里去掉不存在的，补上新来的
  const order = working ? [...working.filter((id) => ids.includes(id)), ...ids.filter((id) => !working.includes(id))] : ids;

  return {
    order,
    draggingId: drag?.id ?? null,
    itemProps: (id) => ({
      ref: (el) => {
        if (el) els.current.set(id, el);
        else els.current.delete(id);
      },
      onPointerDown: onPointerDown(id),
      "data-sort-id": id,
    }),
    drag,
    overlayRef,
    overlayPos,
  };
}

/** 跟着指针走的那一份副本；children 画出这一项的样子 */
export function SortOverlay({ sortable, children, className }: { sortable: Sortable; children: (id: string) => ReactNode; className?: string }) {
  const d = sortable.drag;
  if (!d) return null;
  const at = d.drop ?? sortable.overlayPos();
  return createPortal(
    <div
      ref={sortable.overlayRef}
      className={cn("sort-ghost pointer-events-none fixed top-0 left-0 z-[80]", className)}
      style={{ width: d.w, height: d.h, transform: `translate3d(${at.x}px, ${at.y}px, 0)`, transition: d.drop ? `transform 170ms ${EASE}` : undefined }}
    >
      {children(d.id)}
    </div>,
    document.body,
  );
}
