import { memo, useMemo, useState } from "react";
import { Activity, ArrowDown, ArrowUp, CircleCheck, Cpu, HardDrive, LoaderCircle, MemoryStick, Pause, Play, Plus, RefreshCw, Server as ServerIcon, SquareTerminal, TriangleAlert, WifiOff } from "lucide-react";
import { api, type Sample, type Server } from "../lib/api";
import { cn, formatBytes, formatRate, usageTone } from "../lib/format";
import { chartWindow, liveState, useMonitor, useMonitorError, useMonitorWatch, useNow, useSeries, type LiveState } from "../lib/monitor";
import { toast, useApp } from "../lib/store";
import { useOpenServerIds } from "../lib/term";
import { Badge, Button, Callout, Card, Empty, IconButton, Meter, PageHeader, Segmented, Select } from "../components/ui";
import { ServerAvatar } from "../components/widgets";
import { LiveChart, SERIES_COLORS } from "../components/charts";

export const INTERVALS = [1, 2, 3, 5, 10, 30];

const pct = (v: number) => `${Math.round(v)}%`;

/** 一台服务器是否需要关注：CPU、内存、磁盘任一项 ≥ 85%，或负载超过核数 */
export function needsAttention(s: Sample | undefined): boolean {
  if (!s) return false;
  return s.cpu >= 85 || s.memPct >= 85 || s.diskPct >= 85 || (s.load?.[0] ?? 0) >= Math.max(s.cpus, 1);
}

function StateBadge({ state, online, userPaused }: { state: LiveState; online: boolean; userPaused: boolean }) {
  if (!online)
    return (
      <Badge tone="bad" icon={WifiOff}>
        不可达
      </Badge>
    );
  // 用户自己点了暂停：只是说明状态，不是告警
  if (userPaused) return <Badge icon={Pause}>已暂停</Badge>;
  if (state === "live")
    return (
      <Badge tone="ok" icon={Activity}>
        实时
      </Badge>
    );
  if (state === "connecting")
    return (
      <Badge icon={LoaderCircle} className="[&>svg]:animate-spin">
        连接中
      </Badge>
    );
  return (
    <Badge tone="warn" icon={TriangleAlert}>
      {state === "paused" ? "已暂停" : "采集失败"}
    </Badge>
  );
}

function Stat({ icon: Icon, label, value, sub, times, values, interval }: { icon: typeof Cpu; label: string; value: number | undefined; sub: string; times: number[]; values: number[]; interval: number }) {
  const tone = usageTone(value ?? 0);
  return (
    <div className="min-w-0 rounded-xl border border-line bg-bg2/50 px-3 pt-2.5 pb-2">
      <div className="flex items-center justify-between text-[11.5px] text-muted">
        <span className="inline-flex items-center gap-1.5">
          <Icon size={12} />
          {label}
        </span>
        {tone !== "accent" && (
          <span className={cn("inline-flex items-center gap-1", tone === "bad" ? "text-bad" : "text-warn")}>
            <TriangleAlert size={11} />
            {tone === "bad" ? "过高" : "偏高"}
          </span>
        )}
      </div>
      <div className="mt-0.5 text-[20px] leading-tight font-semibold" style={{ fontVariantNumeric: "tabular-nums" }}>
        {value === undefined ? <span className="text-muted">–</span> : pct(value)}
      </div>
      <LiveChart compact className="mt-1" height={34} times={times} series={[{ label, color: SERIES_COLORS[0], values }]} max={100} format={pct} {...chartWindow(interval, 40)} />
      <div className="mt-1 truncate text-[11px] text-muted">{sub}</div>
    </div>
  );
}

const ServerCard = memo(function ServerCard({ server, online, interval, watching, now }: { server: Server; online: boolean; interval: number; watching: boolean; now: number }) {
  const { openTerminal, go } = useApp();
  const series = useSeries(server.id);
  const err = useMonitorError(server.id);
  const last = series[series.length - 1];
  const state = liveState(last, err, now, interval);
  const live = online && watching && state === "live";
  const times = useMemo(() => series.map((s) => s.time), [series]);
  const cpu = useMemo(() => series.map((s) => s.cpu), [series]);
  const mem = useMemo(() => series.map((s) => s.memPct), [series]);
  const shown = online ? last : undefined;

  return (
    <Card className={cn("flex flex-col p-4 transition-colors hover:border-line2", !online && "opacity-70")}>
      <div className="flex items-center gap-3">
        <ServerAvatar name={server.name} size={34} />
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <span className="truncate text-[13.5px] font-semibold">{server.name}</span>
            {live && needsAttention(last) && <TriangleAlert size={13} className="shrink-0 text-warn" />}
          </div>
          <div className="truncate font-mono text-[11.5px] text-muted">
            {server.username}@{server.host}
          </div>
        </div>
        <StateBadge state={state} online={online} userPaused={!watching} />
        <div className="flex">
          <IconButton icon={SquareTerminal} size="sm" title="打开终端" onClick={() => openTerminal(server.id)} />
          <IconButton icon={Activity} size="sm" title="查看详情" onClick={() => go("servers", { detail: server.id, at: Date.now() })} />
        </div>
      </div>

      {online && watching && err && state !== "live" && state !== "connecting" ? (
        <div className="mt-3 flex flex-1 flex-col justify-center rounded-xl border border-warn/30 bg-warn/8 px-3 py-2.5">
          <div className="line-clamp-3 text-[12px] leading-relaxed text-fg2">{err.error}</div>
          {err.stopped && (
            <div className="mt-2 flex items-center gap-2">
              <Button size="xs" icon={RefreshCw} onClick={() => api.MonitorRetry(server.id)}>
                重试
              </Button>
              <span className="text-[11.5px] text-muted">为避免服务器封禁登录，不会自动重试</span>
            </div>
          )}
        </div>
      ) : (
        <>
          <div className={cn("mt-3 grid grid-cols-2 gap-2.5 transition-opacity", !live && "opacity-60")}>
            <Stat icon={Cpu} label="CPU" interval={interval} value={shown?.cpu} times={times} values={cpu} sub={shown ? `${shown.cpus || "-"} 核 · 负载 ${(shown.load ?? []).map((l) => l.toFixed(2)).join(" ")}` : online ? "等待数据" : "服务器不可达"} />
            <Stat icon={MemoryStick} label="内存" interval={interval} value={shown?.memPct} times={times} values={mem} sub={shown ? `${formatBytes(shown.memUsed)} / ${formatBytes(shown.memTotal)}` : online ? "等待数据" : "服务器不可达"} />
          </div>
          <div className={cn("mt-3 flex items-center gap-3 text-[11.5px] text-muted transition-opacity", !live && "opacity-60")}>
            <span className="inline-flex shrink-0 items-center gap-1.5">
              <HardDrive size={12} />
              磁盘 <span className="font-mono text-fg2">{shown?.diskMount || "/"}</span>
            </span>
            <Meter value={shown?.diskPct ?? 0} className="min-w-0 flex-1" height={5} />
            <span className="w-8 shrink-0 text-right text-fg2" style={{ fontVariantNumeric: "tabular-nums" }}>
              {shown?.diskSize ? pct(shown.diskPct) : "–"}
            </span>
            {shown?.hasNet && (
              <span className="inline-flex shrink-0 items-center gap-2 border-l border-line pl-3" style={{ fontVariantNumeric: "tabular-nums" }}>
                <span className="inline-flex items-center gap-0.5" title="下行速率">
                  <ArrowDown size={11} />
                  {formatRate(shown.netRx)}
                </span>
                <span className="inline-flex items-center gap-0.5" title="上行速率">
                  <ArrowUp size={11} />
                  {formatRate(shown.netTx)}
                </span>
              </span>
            )}
          </div>
        </>
      )}
    </Card>
  );
});

function Tile({ label, value, sub, icon: Icon, tone }: { label: string; value: React.ReactNode; sub: React.ReactNode; icon: typeof Cpu; tone?: "ok" | "warn" }) {
  return (
    <Card className="p-4">
      <div className="flex items-center justify-between">
        <span className="text-[12.5px] text-muted">{label}</span>
        <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-panel3 text-fg2">
          <Icon size={16} />
        </div>
      </div>
      <div className="mt-2 text-[26px] leading-tight font-semibold tracking-tight" style={{ fontVariantNumeric: "tabular-nums" }}>
        {value}
      </div>
      <div className={cn("mt-1 inline-flex items-center gap-1 text-[12px]", tone === "warn" ? "text-warn" : tone === "ok" ? "text-ok" : "text-muted")}>{sub}</div>
    </Card>
  );
}

export default function Monitor() {
  const { servers, settings, loadSettings, go } = useApp();
  const [group, setGroup] = useState("all");
  const [sort, setSort] = useState<"default" | "cpu" | "mem">("default");
  const [paused, setPaused] = useState(false);
  const [saving, setSaving] = useState(false);
  const now = useNow(1000);
  const interval = settings?.general.monitorInterval ?? 3;
  const off = settings?.general.monitorOff ?? false;
  const watching = !paused && !off;

  // 只监控打开了终端的服务器：不会为了监控去连接其他服务器
  const openIds = useOpenServerIds();
  const opened = useMemo(() => servers.filter((s) => openIds.includes(s.id)), [servers, openIds]);
  const groups = useMemo(() => [...new Set(opened.map((s) => s.group).filter(Boolean))], [opened]);
  const list = useMemo(() => opened.filter((s) => group === "all" || (s.group || "") === (group === "__none" ? "" : group)), [opened, group]);
  const watchIds = useMemo(() => list.map((s) => s.id), [list]);
  useMonitorWatch("monitor-page", watchIds, watching);

  // 汇总与排序每秒按最新采样计算一次（不订阅整个采样表，避免每个采样都重渲染整页）
  const { latest, ordered, liveCount, avgCpu, avgMem, attention } = useMemo(() => {
    const { series, errors } = useMonitor.getState();
    const latest: Record<string, Sample | undefined> = {};
    let liveCount = 0;
    let cpu = 0;
    let mem = 0;
    const attention: string[] = [];
    for (const s of list) {
      const arr = series[s.id];
      const last = arr?.[arr.length - 1];
      if (!watching || liveState(last, errors[s.id], now, interval) !== "live") continue;
      latest[s.id] = last;
      liveCount++;
      cpu += last!.cpu;
      mem += last!.memPct;
      if (needsAttention(last)) attention.push(s.name);
    }
    const ordered = [...list];
    if (sort !== "default") {
      const key = (s: Server) => (sort === "cpu" ? latest[s.id]?.cpu : latest[s.id]?.memPct) ?? -1;
      ordered.sort((a, b) => key(b) - key(a));
    }
    return { latest, ordered, liveCount, avgCpu: liveCount ? cpu / liveCount : 0, avgMem: liveCount ? mem / liveCount : 0, attention };
  }, [list, now, interval, sort, watching]);
  void latest;

  // 没有实时数据时汇总卡片下方的说明
  const idle = off ? "实时监控已关闭" : paused ? "已暂停" : "等待数据";

  const setMonitor = async (sec: number, disabled: boolean) => {
    setSaving(true);
    try {
      await api.SetMonitor(sec, disabled);
      await loadSettings();
    } catch (e) {
      toast.error("保存失败", e);
    } finally {
      setSaving(false);
    }
  };

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title="实时监控"
        icon={Activity}
        description={off ? "实时监控已关闭" : paused ? "已暂停刷新" : opened.length === 0 ? "只监控打开了终端的服务器" : `每 ${interval} 秒刷新一次 · 正在监控 ${liveCount} / ${list.length} 台打开了终端的服务器`}
        actions={
          <>
            {groups.length > 0 && (
              <Select value={group} onChange={(e) => setGroup(e.target.value)} className="w-[124px]">
                <option value="all">全部分组</option>
                {groups.map((g) => (
                  <option key={g} value={g}>
                    {g}
                  </option>
                ))}
                <option value="__none">未分组</option>
              </Select>
            )}
            <Select value={sort} onChange={(e) => setSort(e.target.value as typeof sort)} className="w-[132px]">
              <option value="default">默认顺序</option>
              <option value="cpu">按 CPU 排序</option>
              <option value="mem">按内存排序</option>
            </Select>
            <div className="flex max-w-full min-w-0 items-center gap-2">
              <span className="shrink-0 text-[12px] text-muted">刷新间隔</span>
              <Segmented
                className={cn(saving && "pointer-events-none opacity-60")}
                value={String(interval)}
                onChange={(v) => setMonitor(Number(v), off)}
                options={INTERVALS.map((n) => ({ value: String(n), label: `${n}s` }))}
              />
            </div>
            <Button icon={paused ? Play : Pause} variant={paused ? "primary" : "secondary"} disabled={off} onClick={() => setPaused((v) => !v)}>
              {paused ? "继续" : "暂停"}
            </Button>
          </>
        }
      />
      <div className="min-h-0 flex-1 overflow-y-auto px-4 @2xl:px-7 pb-8">
        {off && (
          <Callout
            tone="info"
            className="mb-4"
            title="实时监控已关闭"
            action={
              <Button size="sm" variant="soft" loading={saving} onClick={() => setMonitor(interval, false)}>
                开启
              </Button>
            }
          >
            开启后，打开了终端的服务器会在这里实时显示 CPU、内存、磁盘和网络。每台服务器只占用一个 SSH 会话，采样只读取 /proc，对服务器几乎没有负担。
          </Callout>
        )}
        {servers.length === 0 ? (
          <Card className="mt-2">
            <Empty
              icon={ServerIcon}
              title="还没有服务器"
              description="添加服务器并打开它的终端后，这里会实时显示 CPU、内存、磁盘和网络。"
              action={
                <Button variant="primary" icon={Plus} onClick={() => go("servers", { add: Date.now() })}>
                  添加服务器
                </Button>
              }
            />
          </Card>
        ) : opened.length === 0 ? (
          <Card className="mt-2">
            <Empty
              icon={SquareTerminal}
              title="还没有打开终端的服务器"
              description="实时监控只监控已经打开终端（SSH 已连接）的服务器，不会去连接其他服务器。打开一台服务器的终端，它就会出现在这里。"
              action={
                <Button variant="primary" icon={SquareTerminal} onClick={() => go("terminal")}>
                  打开终端
                </Button>
              }
            />
          </Card>
        ) : (
          <>
            <div className="mb-4 grid grid-cols-2 gap-3 @4xl:grid-cols-4 @4xl:gap-4">
              <Tile
                icon={ServerIcon}
                label="监控中"
                value={
                  <span>
                    {liveCount}
                    <span className="text-[16px] font-normal text-muted"> / {list.length} 台</span>
                  </span>
                }
                sub={servers.length > opened.length ? `另外 ${servers.length - opened.length} 台没有打开终端` : "所有服务器都打开了终端"}
              />
              <Tile icon={Cpu} label="平均 CPU" value={liveCount ? pct(avgCpu) : "–"} sub={liveCount ? `${liveCount} 台服务器的平均值` : idle} />
              <Tile icon={MemoryStick} label="平均内存" value={liveCount ? pct(avgMem) : "–"} sub={liveCount ? `${liveCount} 台服务器的平均值` : idle} />
              <Tile
                icon={TriangleAlert}
                label="需要关注"
                value={attention.length}
                tone={attention.length ? "warn" : liveCount ? "ok" : undefined}
                sub={
                  attention.length ? (
                    <>
                      <TriangleAlert size={12} />
                      <span className="truncate">{attention.slice(0, 3).join("、")}</span>
                    </>
                  ) : liveCount ? (
                    <>
                      <CircleCheck size={12} />
                      资源使用都在正常范围
                    </>
                  ) : (
                    idle
                  )
                }
              />
            </div>
            {list.length === 0 ? (
              <Empty icon={ServerIcon} title="这个分组里没有服务器" />
            ) : (
              <div className="grid grid-cols-[repeat(auto-fill,minmax(min(360px,100%),1fr))] gap-4">
                {ordered.map((s) => (
                  <ServerCard key={s.id} server={s} online interval={interval} watching={watching} now={now} />
                ))}
              </div>
            )}
          </>
        )}
      </div>
    </div>
  );
}
