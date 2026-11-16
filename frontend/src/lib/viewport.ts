import { create } from "zustand";

// 窗口可以随意缩放，界面按窗口宽度分三档调整布局：
//   wide   侧栏按用户的选择展开或收起
//   mid    侧栏自动收成图标栏
//   narrow 侧栏隐藏，点标题栏左侧的按钮展开
export type Tier = "wide" | "mid" | "narrow";

const tierOf = (w: number): Tier => (w >= 1100 ? "wide" : w >= 760 ? "mid" : "narrow");

interface ViewportState {
  width: number;
  height: number;
  tier: Tier;
  /** 窄窗口下导航抽屉是否展开 */
  navOpen: boolean;
  setNavOpen: (v: boolean) => void;
}

export const useViewport = create<ViewportState>((set) => ({
  width: window.innerWidth,
  height: window.innerHeight,
  tier: tierOf(window.innerWidth),
  navOpen: false,
  setNavOpen: (navOpen) => set({ navOpen }),
}));

window.addEventListener("resize", () => {
  const tier = tierOf(window.innerWidth);
  const st = useViewport.getState();
  if (st.width === window.innerWidth && st.height === window.innerHeight) return;
  useViewport.setState({ width: window.innerWidth, height: window.innerHeight, tier, navOpen: tier === "narrow" ? st.navOpen : false });
});
