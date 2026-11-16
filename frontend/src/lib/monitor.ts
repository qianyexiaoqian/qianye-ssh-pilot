import { useEffect, useState } from "react";
import { create } from "zustand";
import { api, type Sample } from "./api";

/** 每台服务器保留的采样点数量 */
export const MAX_POINTS = 120;

interface MonitorState {
  /** 每台服务器最近的正常采样（按时间升序） */
  series: Record<string, Sample[]>;
  /** 最近一次出错的状态；收到正常采样后清除 */
  errors: Record<string, Sample | undefined>;
  push: (s: Sample) => void;
  drop: (id: string) => void;
}

export const useMonitor = create<MonitorState>((set, get) => ({
  series: {},
  errors: {},
  push: (s) => {
    if (!s?.serverId) return;
    if (s.error) {
      set({ errors: { ...get().errors, [s.serverId]: s } });
      return;
    }
    const prev = get().series[s.serverId] ?? [];
    if (prev.length && prev[prev.length - 1].time >= s.time) return;
    const next = prev.length >= MAX_POINTS ? [...prev.slice(prev.length - MAX_POINTS + 1), s] : [...prev, s];
    const errors = get().errors;
    set({ series: { ...get().series, [s.serverId]: next }, errors: errors[s.serverId] ? { ...errors, [s.serverId]: undefined } : errors });
  },
  drop: (id) => {
    const series = { ...get().series };
    const errors = { ...get().errors };
    delete series[id];
    delete errors[id];
    set({ series, errors });
  },
}));

const none: Sample[] = [];

/** 某台服务器的采样序列（只在这台服务器有新采样时触发重渲染） */
export function useSeries(id: string | undefined): Sample[] {
  return useMonitor((s) => (id ? s.series[id] : undefined)) ?? none;
}

export function useMonitorError(id: string | undefined): Sample | undefined {
  return useMonitor((s) => (id ? s.errors[id] : undefined));
}

// 每次页面加载生成一个标识，避免多个窗口（开发模式下的浏览器与桌面窗口）互相覆盖订阅
const clientId = Math.random().toString(36).slice(2, 8);
// 调用序号：后端并发处理调用，靠序号丢弃乱序到达的旧请求
let watchSeq = 0;

/**
 * 声明当前界面需要实时监控的服务器。界面卸载或列表变化时自动取消；
 * 后端会为取消的服务器保留几秒采样会话，页面间切换不会反复重连。
 * 订阅每 20 秒续订一次，界面异常退出后由后端自动回收。
 */
export function useMonitorWatch(name: string, ids: string[], enabled = true) {
  const sig = enabled ? [...new Set(ids)].sort().join(",") : "";
  useEffect(() => {
    if (!sig) return;
    const key = `${clientId}:${name}`;
    const list = sig.split(",");
    const send = () => void api.MonitorWatch(key, list, ++watchSeq);
    send();
    const timer = setInterval(send, 20000);
    return () => {
      clearInterval(timer);
      void api.MonitorWatch(key, [], ++watchSeq);
    };
  }, [name, sig]);
}

/** 每隔 ms 毫秒触发一次重渲染，用于判断采样是否过期 */
export function useNow(ms = 1000): number {
  const [now, setNow] = useState(Date.now());
  useEffect(() => {
    const t = setInterval(() => setNow(Date.now()), ms);
    return () => clearInterval(t);
  }, [ms]);
  return now;
}

/** 采样在多久之内算「实时」：三个采样周期，至少 10 秒 */
export function freshWindow(intervalSec: number): number {
  return Math.max(intervalSec * 3, 10) * 1000;
}

/** 图表的时间窗口：显示最近 points 个采样周期；相邻采样超过 2.5 个周期视为中断 */
export function chartWindow(intervalSec: number, points = 60): { spanMs: number; gapMs: number } {
  return { spanMs: points * intervalSec * 1000, gapMs: intervalSec * 2500 + 2000 };
}

export type LiveState = "live" | "connecting" | "error" | "paused";

/** 综合最近的采样与错误，判断一台服务器的监控状态 */
export function liveState(last: Sample | undefined, err: Sample | undefined, now: number, intervalSec: number): LiveState {
  if (err && (!last || err.time >= last.time)) return err.stopped ? "paused" : "error";
  if (last && now - last.time <= freshWindow(intervalSec)) return "live";
  return "connecting";
}
