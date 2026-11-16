import { useMemo, useRef, useState } from "react";
import { cn } from "../lib/format";

export interface ChartSeries {
  label: string;
  /** CSS 颜色，按固定顺序取 var(--c-series-N) */
  color: string;
  values: number[];
}

export const SERIES_COLORS = ["var(--c-series-1)", "var(--c-series-2)"];

function pad2(n: number) {
  return String(n).padStart(2, "0");
}

function clock(ts: number) {
  const d = new Date(ts);
  return `${pad2(d.getHours())}:${pad2(d.getMinutes())}:${pad2(d.getSeconds())}`;
}

/** 把最大值向上取到 1 / 2 / 5 × 10ⁿ（或 1024 进制下的整数倍），让纵轴刻度是整齐的数 */
export function niceMax(v: number, base: 1000 | 1024 = 1000, floor = 1): number {
  const x = Math.max(v, floor);
  if (base === 1024) {
    let unit = 1;
    while (unit * 1024 <= x) unit *= 1024;
    for (const m of [1, 2, 5, 10, 20, 50, 100, 200, 500, 1024]) if (m * unit >= x) return m * unit;
    return 1024 * unit;
  }
  const p = Math.pow(10, Math.floor(Math.log10(x)));
  for (const m of [1, 2, 5, 10]) if (m * p >= x) return m * p;
  return 10 * p;
}

/**
 * 实时折线图：横轴是时间，显示最近 spanMs 毫秒，最新的采样贴着右边。
 * 相邻采样间隔超过 gapMs（离开页面、暂停、连接中断）时折线断开，不会把不连续的数据连成一条线。
 * 单系列时带浅色面积；多系列时显示图例（同时充当当前值的直接标注）。悬停显示十字线与数值。
 */
export function LiveChart({
  times,
  series,
  spanMs,
  gapMs,
  max,
  height = 96,
  format,
  compact,
  className,
}: {
  times: number[];
  series: ChartSeries[];
  /** 横轴覆盖的时间范围 */
  spanMs: number;
  /** 相邻两点超过这个间隔就断开 */
  gapMs: number;
  /** 纵轴上限；不传则根据窗口内的最大值自动取整 */
  max?: number;
  height?: number;
  format: (v: number) => string;
  /** 紧凑模式：不画网格与纵轴标注（卡片里的迷你图） */
  compact?: boolean;
  className?: string;
}) {
  const boxRef = useRef<HTMLDivElement>(null);
  const [hoverAt, setHoverAt] = useState<number | null>(null);
  const end = times.length ? times[times.length - 1] : 0;
  // 窗口内第一个采样的下标
  const first = useMemo(() => {
    let i = times.length;
    while (i > 0 && end - times[i - 1] <= spanMs) i--;
    return i;
  }, [times, end, spanMs]);
  const count = times.length - first;
  const xOf = (t: number) => 100 - ((end - t) / spanMs) * 100;

  const yMax = useMemo(() => {
    if (max !== undefined) return max;
    let m = 0;
    for (const s of series) for (let i = first; i < s.values.length; i++) m = Math.max(m, s.values[i] ?? 0);
    return m;
  }, [series, max, first]);
  // 纵向留出余量，线不会贴住上下边
  const yOf = (v: number) => 98 - (Math.min(Math.max(v, 0), yMax || 1) / (yMax || 1)) * 94;

  // 每个系列拆成若干连续的线段；面积图逐段闭合到基线
  const paths = useMemo(
    () =>
      series.map((s) => {
        let line = "";
        let area = "";
        let segStart = -1;
        const close = (last: number) => {
          if (segStart >= 0 && last > segStart) area += `L${xOf(times[last]).toFixed(2)},98L${xOf(times[segStart]).toFixed(2)},98Z`;
        };
        for (let i = first; i < times.length; i++) {
          const p = `${xOf(times[i]).toFixed(2)},${yOf(s.values[i] ?? 0).toFixed(2)}`;
          if (i === first || times[i] - times[i - 1] > gapMs) {
            close(i - 1);
            line += `M${p}`;
            area += `M${p}`;
            segStart = i;
          } else {
            line += `L${p}`;
            area += `L${p}`;
          }
        }
        close(times.length - 1);
        return { line, area };
      }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [series, times, first, yMax, spanMs, gapMs],
  );

  // 悬停：取时间上离鼠标最近的采样
  const hover = useMemo(() => {
    if (hoverAt === null || count < 1) return null;
    const t = end - ((100 - hoverAt) / 100) * spanMs;
    let best = first;
    for (let i = first; i < times.length; i++) if (Math.abs(times[i] - t) < Math.abs(times[best] - t)) best = i;
    return Math.abs(times[best] - t) <= Math.max(gapMs, spanMs / 20) ? best : null;
  }, [hoverAt, times, first, count, end, spanMs, gapMs]);

  const onMove = (e: React.MouseEvent) => {
    const box = boxRef.current?.getBoundingClientRect();
    if (box && box.width > 0) setHoverAt(((e.clientX - box.left) / box.width) * 100);
  };

  const hx = hover !== null ? xOf(times[hover]) : 0;
  return (
    <div className={className}>
      {series.length >= 2 && (
        <div className="mb-1.5 flex flex-wrap items-center gap-x-4 gap-y-1 text-[12px]">
          {series.map((s) => (
            <span key={s.label} className="inline-flex items-center gap-1.5 text-fg2">
              <span className="h-2 w-2 shrink-0 rounded-[3px]" style={{ background: s.color }} />
              {s.label}
              <span className="font-medium text-fg" style={{ fontVariantNumeric: "tabular-nums" }}>
                {s.values.length ? format(s.values[s.values.length - 1]) : "-"}
              </span>
            </span>
          ))}
        </div>
      )}
      <div ref={boxRef} className="relative" style={{ height }} onMouseMove={onMove} onMouseLeave={() => setHoverAt(null)}>
        <svg className="absolute inset-0 h-full w-full" viewBox="0 0 100 100" preserveAspectRatio="none" aria-hidden>
          {!compact &&
            [4, 51, 98].map((y) => (
              <line key={y} x1="0" x2="100" y1={y} y2={y} stroke="var(--c-line)" strokeWidth="1" vectorEffect="non-scaling-stroke" strokeDasharray={y === 98 ? undefined : "3 4"} />
            ))}
          {series.length === 1 && count > 1 && <path d={paths[0].area} fill={series[0].color} opacity="0.12" />}
          {count > 1 &&
            series.map((s, i) => <path key={s.label} d={paths[i].line} fill="none" stroke={s.color} strokeWidth="2" strokeLinejoin="round" strokeLinecap="round" vectorEffect="non-scaling-stroke" />)}
        </svg>
        {!compact && count > 1 && (
          <>
            <span className="pointer-events-none absolute top-0 left-0 rounded bg-panel/80 px-1 text-[10.5px] leading-[14px] text-muted">{format(yMax)}</span>
            <span className="pointer-events-none absolute bottom-0 left-0 rounded bg-panel/80 px-1 text-[10.5px] leading-[14px] text-muted">{format(0)}</span>
          </>
        )}
        {count <= 1 && <div className="absolute inset-0 flex items-center justify-center text-[11.5px] text-muted">{compact ? "" : "正在等待数据…"}</div>}
        {hover !== null && count > 1 && (
          <>
            <div className="pointer-events-none absolute top-0 bottom-0 w-px bg-line2" style={{ left: `${hx}%` }} />
            {series.map((s) => (
              <span
                key={s.label}
                className="pointer-events-none absolute h-2.5 w-2.5 -translate-x-1/2 -translate-y-1/2 rounded-full ring-2 ring-panel"
                style={{ left: `${hx}%`, top: `${yOf(s.values[hover] ?? 0)}%`, background: s.color }}
              />
            ))}
            <div
              className={cn("pointer-events-none absolute z-10 rounded-lg border border-line2 bg-panel3 px-2 py-1.5 text-[11.5px] whitespace-nowrap shadow-xl", compact ? "top-full mt-1" : "top-1")}
              style={hx > 55 ? { right: `${100 - hx + 3}%` } : { left: `${hx + 3}%` }}
            >
              <div className="text-muted" style={{ fontVariantNumeric: "tabular-nums" }}>
                {clock(times[hover])}
              </div>
              {series.map((s) => (
                <div key={s.label} className="mt-0.5 flex items-center gap-1.5 text-fg2">
                  <span className="h-2 w-2 shrink-0 rounded-[3px]" style={{ background: s.color }} />
                  {s.label}
                  <span className="ml-auto pl-3 font-medium text-fg" style={{ fontVariantNumeric: "tabular-nums" }}>
                    {format(s.values[hover] ?? 0)}
                  </span>
                </div>
              ))}
            </div>
          </>
        )}
      </div>
    </div>
  );
}
