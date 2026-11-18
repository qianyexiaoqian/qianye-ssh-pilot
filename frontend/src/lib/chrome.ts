import { create } from "zustand";
import { runtime } from "./api";

// 无边框窗口的缩放和最大化状态。
//
// 窗口左、右、下三边的缩放由系统处理（后端把系统的缩放边框还给了窗口，见 winframe_windows.go）；
// 上边没有系统边框，由 Wails 注入的脚本处理：鼠标停在窗口最上面几个像素时显示缩放指针，按下后开始缩放。
// 这里做三件事：
//   1. 记录窗口是否最大化（标题栏按钮据此显示「最大化」或「向下还原」）；
//   2. 把 Wails 的缩放范围收到上边：其余三边已经由系统接管，窗口内侧不再占用位置（否则贴着右边缘的滚动条很难拖动）；
//   3. 最大化时关掉缩放：这时窗口不能缩放，顶上那几个像素应该用来拖动窗口。
// 缩放指针的显示见 index.css 里 html[style*="-resize"] 那条规则。

export const useWindowState = create<{ maximised: boolean }>(() => ({ maximised: false }));

interface WailsFlags {
  resizeEdge?: string;
  defaultCursor?: string | null;
}

let started = false;

/** 应用启动时调用一次；native 表示左、右、下三边由系统处理缩放 */
export function initWindowChrome(native: boolean) {
  if (started) return;
  started = true;
  const refresh = () =>
    void runtime
      .WindowIsMaximised()
      .then((maximised) => useWindowState.setState({ maximised }))
      .catch(() => {});
  refresh();
  window.addEventListener("resize", refresh);
  // Wails 的监听器先注册、先执行：它判断出鼠标在哪条边上，这里再决定要不要保留
  window.addEventListener("mousemove", () => {
    const flags = (window as unknown as { wails?: { flags?: WailsFlags } }).wails?.flags;
    const edge = flags?.resizeEdge;
    if (!flags || !edge) return;
    const top = edge === "n-resize" || edge === "nw-resize" || edge === "ne-resize";
    if (useWindowState.getState().maximised || (native && !top)) {
      flags.resizeEdge = undefined;
      document.documentElement.style.cursor = flags.defaultCursor ?? "";
    }
  });
}
