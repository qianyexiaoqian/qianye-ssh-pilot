import { useEffect, useMemo, useState } from "react";
import {
  Activity,
  ArrowUpRight,
  Check,
  ChevronRight,
  Bot,
  Brain,
  CircleAlert,
  CircleCheck,
  CircleX,
  Clock,
  FileCode,
  History,
  KeyRound,
  Plus,
  Server,
  ShieldCheck,
  Sparkles,
  Stethoscope,
  TriangleAlert,
  Wifi,
  WifiOff,
  X,
} from "lucide-react";
import { api, type Dashboard as DashboardData, type Finding, type Server as ServerData, type ServerStatus } from "../lib/api";
import { certTone, cn, formatDate, timeAgo } from "../lib/format";
import { liveState, useMonitorError, useMonitorWatch, useNow, useSeries } from "../lib/monitor";
import { useOpenServerIds } from "../lib/term";
import { useApp } from "../lib/store";
import { Badge, Button, Callout, Card, Empty, Meter, SectionTitle, Spinner, StatusDot } from "../components/ui";
import { ServerAvatar } from "../components/widgets";

function greeting() {
  const h = new Date().getHours();
  if (h < 6) return "夜深了";
  if (h < 12) return "早上好";
  if (h < 14) return "中午好";
  if (h < 18) return "下午好";
  return "晚上好";
}

export function healthLabel(score: number): { label: string; tone: "ok" | "warn" | "bad"; Icon: typeof CircleCheck } {
  if (score >= 85) return { label: "运行良好", tone: "ok", Icon: CircleCheck };
  if (score >= 60) return { label: "需要关注", tone: "warn", Icon: TriangleAlert };
  return { label: "存在风险", tone: "bad", Icon: CircleX };
}

export const severityMeta: Record<string, { label: string; tone: "bad" | "warn" | "info"; Icon: typeof CircleX }> = {
  critical: { label: "严重", tone: "bad", Icon: CircleX },
  warning: { label: "警告", tone: "warn", Icon: TriangleAlert },
  info: { label: "提示", tone: "info", Icon: CircleAlert },
};

function StatTile({ icon: Icon, label, value, sub, onClick, accent }: { icon: typeof Server; label: string; value: React.ReactNode; sub?: React.ReactNode; onClick?: () => void; accent?: string }) {
  return (
    <Card className="group relative overflow-hidden p-4 transition-colors hover:border-line2" onClick={onClick}>
      <div className="flex items-center justify-between">
        <span className="text-[12.5px] text-muted">{label}</span>
        <div className={cn("flex h-8 w-8 items-center justify-center rounded-lg bg-panel3", accent ?? "text-fg2")}>
          <Icon size={16} />
        </div>
      </div>
      <div className="mt-2 text-[26px] leading-tight font-semibold tracking-tight text-fg">{value}</div>
      {sub && <div className="mt-1 text-[12px] text-muted">{sub}</div>}
      {onClick && <ArrowUpRight size={14} className="absolute right-3 bottom-3 text-muted opacity-0 transition-opacity group-hover:opacity-100" />}
    </Card>
  );
}

export function runFinding(f: Finding, go: ReturnType<typeof useApp.getState>["go"]) {
  const a = f.action;
  if (!a) return;
  switch (a.type) {
    case "script":
      go("scripts", { scriptId: a.scriptId, servers: f.serverId ? [f.serverId] : [], params: a.params ?? {}, autorun: false });
      break;
    case "deploy":
      go("certs", { certId: a.certId });
      break;
    case "ai":
      go("assistant", { prompt: a.prompt });
      break;
  }
}

/** 仪表盘里的一行服务器：有实时采样时显示 CPU 与内存，否则显示连接延迟 */
function ServerRow({ server, st, watched, interval, now, onOpen }: { server: ServerData; st?: ServerStatus; watched: boolean; interval: number; now: number; onOpen: () => void }) {
  const series = useSeries(server.id);
  const err = useMonitorError(server.id);
  const last = series[series.length - 1];
  const live = watched && liveState(last, err, now, interval) === "live";
  return (
    <button type="button" onClick={onOpen} className="group flex items-center gap-3 rounded-xl border border-line bg-bg2/50 px-3 py-2.5 text-left transition-colors hover:border-line2 hover:bg-panel2">
      <ServerAvatar name={server.name} size={32} />
      <div className="min-w-0 flex-1">
        <div className="flex items-center gap-2">
          <span className="truncate text-[13px] font-medium">{server.name}</span>
          <StatusDot status={st?.status} />
        </div>
        <div className="truncate font-mono text-[11.5px] text-muted">{server.host}</div>
      </div>
      {live && last ? (
        <div className="flex shrink-0 gap-3" style={{ fontVariantNumeric: "tabular-nums" }}>
          {(
            [
              ["CPU", last.cpu],
              ["内存", last.memPct],
            ] as const
          ).map(([label, v]) => (
            <div key={label} className="w-[62px]">
              <div className="mb-1 flex items-baseline justify-between text-[11px]">
                <span className="text-muted">{label}</span>
                <span className={cn("font-medium", v >= 95 ? "text-bad" : v >= 85 ? "text-warn" : "text-fg2")}>{Math.round(v)}%</span>
              </div>
              <Meter value={v} height={4} />
            </div>
          ))}
        </div>
      ) : (
        <div className="shrink-0 text-right">
          <div className={cn("text-[12px]", st?.status === "online" ? "text-fg2" : "text-bad")}>{st?.status === "online" ? `${st.latency} ms` : st?.status === "offline" ? "离线" : "检测中"}</div>
          <div className="text-[11px] text-muted">{server.group || "未分组"}</div>
        </div>
      )}
    </button>
  );
}

interface Step {
  key: string;
  title: string;
  hint: string;
  done: boolean;
  run: () => void;
}

/** 新手引导：列出还没完成的几件事，全部完成或手动关闭后不再显示 */
function Onboarding({ steps, onDismiss }: { steps: Step[]; onDismiss: () => void }) {
  const done = steps.filter((s) => s.done).length;
  const next = steps.find((s) => !s.done);
  return (
    <Card className="relative mb-5 overflow-hidden p-5">
      <div className="pointer-events-none absolute -top-20 -left-10 h-48 w-72 rounded-full bg-accent/10 blur-3xl" />
      <div className="mb-3.5 flex items-center gap-3">
        <div className="flex h-9 w-9 items-center justify-center rounded-xl grad-accent text-white">
          <Sparkles size={17} />
        </div>
        <div className="min-w-0 flex-1">
          <div className="text-[14px] font-semibold">快速上手</div>
          <div className="text-[12px] text-muted">
            跟着做完这几步就能用起来 · 已完成 {done} / {steps.length}
          </div>
        </div>
        <div className="w-[160px]">
          <Meter value={(done / steps.length) * 100} tone="accent" />
        </div>
        <button type="button" onClick={onDismiss} title="不再显示" className="flex h-7 w-7 items-center justify-center rounded-lg text-muted hover:bg-panel3 hover:text-fg">
          <X size={14} />
        </button>
      </div>
      <div className="grid grid-cols-2 gap-2.5 @2xl:grid-cols-3 @5xl:grid-cols-5">
        {steps.map((s, i) => {
          const current = s === next;
          return (
            <button
              key={s.key}
              type="button"
              onClick={s.run}
              className={cn(
                "group flex flex-col rounded-xl border px-3 py-2.5 text-left transition-colors",
                s.done ? "border-line bg-bg2/40" : current ? "border-accent/50 bg-accent/10 hover:bg-accent/15" : "border-line2 hover:bg-panel2",
              )}
            >
              <div className="flex items-center gap-2">
                <span
                  className={cn(
                    "flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-[11px] font-semibold",
                    s.done ? "bg-ok/15 text-ok" : current ? "bg-accent text-white" : "border border-line2 text-muted",
                  )}
                >
                  {s.done ? <Check size={12} strokeWidth={3} /> : i + 1}
                </span>
                <span className={cn("truncate text-[12.5px] font-medium", s.done ? "text-muted" : "text-fg")}>{s.title}</span>
                {!s.done && <ChevronRight size={13} className="ml-auto shrink-0 text-muted transition-transform group-hover:translate-x-0.5" />}
              </div>
              <div className="mt-1.5 line-clamp-2 text-[11.5px] leading-relaxed text-muted">{s.done ? "已完成" : s.hint}</div>
            </button>
          );
        })}
      </div>
    </Card>
  );
}

export default function Dashboard() {
  const { go, servers, status, openTerminal, info, settings } = useApp();
  const [d, setD] = useState<DashboardData | null>(null);
  const [guideOff, setGuideOff] = useState(localStorage.getItem("onboarding") === "dismissed");
  const now = useNow(1000);
  const interval = settings?.general.monitorInterval ?? 3;
  // 仪表盘上显示的服务器里，打开了终端的才显示实时的 CPU 和内存
  const shown = useMemo(() => servers.slice(0, 8), [servers]);
  const openIds = useOpenServerIds();
  const liveIds = useMemo(() => shown.filter((s) => openIds.includes(s.id)).map((s) => s.id), [shown, openIds]);
  useMonitorWatch("dashboard", liveIds, !(settings?.general.monitorOff ?? true));
  const load = () =>
    api
      .GetDashboard()
      .then(setD)
      .catch(() => {});
  useEffect(() => {
    void load();
    const t = setInterval(load, 15000);
    return () => clearInterval(t);
  }, []);

  if (!d) {
    return (
      <div className="flex h-full items-center justify-center">
        <Spinner size={22} />
      </div>
    );
  }
  const online = servers.filter((s) => status[s.id]?.status === "online").length;
  const offline = servers.filter((s) => status[s.id]?.status === "offline").length;
  const report = d.lastReport;
  const health = report ? healthLabel(report.score) : null;
  const warnDays = 30;
  const steps: Step[] = [
    { key: "server", title: "添加服务器", hint: "填写地址和登录方式，支持直接粘贴 ssh 命令", done: d.serverCount > 0, run: () => go("servers", { add: Date.now() }) },
    { key: "key", title: "准备 SSH 密钥", hint: "生成一把密钥，比密码登录更安全", done: d.keyCount > 0, run: () => go("keys", { generate: Date.now() }) },
    { key: "inspect", title: "做一次巡检", hint: "一键检查磁盘、内存、服务和证书", done: !!report, run: () => go("inspect", { run: Date.now() }) },
    { key: "cert", title: "导入证书", hint: "之后可一键同步到多台服务器", done: d.certCount > 0, run: () => go("certs", { import: Date.now() }) },
    { key: "ai", title: "配置 AI 助手", hint: "填入 API Key，让 AI 帮你排查问题", done: d.aiConfigured, run: () => go("settings", { tab: "ai" }) },
  ];
  const showGuide = !guideOff && steps.some((s) => !s.done);

  return (
    <div className="h-full overflow-y-auto">
      <div className="mx-auto max-w-[1480px] px-4 @2xl:px-7 pt-6 pb-10">
        {/* 问候 */}
        <div className="mb-6 flex flex-wrap items-end justify-between gap-3">
          <div className="min-w-0">
            <h1 className="text-[22px] font-semibold tracking-tight">
              {greeting()}，<span className="grad-text">欢迎回来</span>
            </h1>
            <p className="mt-1 text-[12.5px] text-muted">
              {formatDate(Date.now())} · 管理 {d.serverCount} 台服务器、{d.certCount} 张证书 · v{info?.version}
            </p>
          </div>
          <div className="flex flex-wrap gap-2">
            <Button icon={Plus} onClick={() => go("servers", { add: Date.now() })}>
              添加服务器
            </Button>
            <Button variant="primary" icon={Stethoscope} onClick={() => go("inspect", { run: Date.now() })}>
              智能巡检
            </Button>
          </div>
        </div>

        {showGuide && (
          <Onboarding
            steps={steps}
            onDismiss={() => {
              localStorage.setItem("onboarding", "dismissed");
              setGuideOff(true);
            }}
          />
        )}

        {!d.aiConfigured && !showGuide && (
          <Callout
            tone="info"
            className="mb-5"
            title="配置 AI 模型，启用智能运维助手"
            action={
              <Button size="sm" variant="soft" onClick={() => go("settings", { tab: "ai" })}>
                去配置
              </Button>
            }
          >
            支持 Claude（推荐）以及 DeepSeek、通义千问、Kimi 等 OpenAI 兼容接口。助手会在使用中积累记忆与技能，越用越懂你的环境。
          </Callout>
        )}

        {/* KPI */}
        <div className="grid grid-cols-2 gap-3 @4xl:grid-cols-4 @4xl:gap-4">
          <StatTile
            icon={Server}
            label="服务器"
            accent="text-accent"
            value={
              <span>
                {online}
                <span className="text-[16px] font-normal text-muted"> / {d.serverCount} 在线</span>
              </span>
            }
            sub={
              offline > 0 ? (
                <span className="inline-flex items-center gap-1 text-bad">
                  <WifiOff size={12} /> {offline} 台离线
                </span>
              ) : d.serverCount ? (
                <span className="inline-flex items-center gap-1">
                  <Wifi size={12} /> 全部可达
                </span>
              ) : (
                "还没有服务器"
              )
            }
            onClick={() => go("servers")}
          />
          <StatTile
            icon={ShieldCheck}
            label="TLS 证书"
            accent="text-cyan"
            value={d.certCount}
            sub={
              d.certExpired + d.certExpiring > 0 ? (
                <span className="inline-flex items-center gap-1 text-warn">
                  <TriangleAlert size={12} />
                  {d.certExpired > 0 && `${d.certExpired} 张已过期 `}
                  {d.certExpiring > 0 && `${d.certExpiring} 张即将到期`}
                </span>
              ) : (
                "没有即将到期的证书"
              )
            }
            onClick={() => go("certs")}
          />
          <StatTile icon={KeyRound} label="SSH 密钥" value={d.keyCount} sub={`${d.publicKeyCount} 个公钥 · ${d.bindingCount} 处授权`} onClick={() => go("keys")} />
          <StatTile icon={Brain} label="AI 技能库" accent="text-accent" value={d.skillCount} sub={`${d.sessionCount} 次对话 · 长期记忆 ${d.longMemory.facts} 条`} onClick={() => go("brain")} />
        </div>

        <div className="mt-4 grid grid-cols-1 gap-4 @4xl:grid-cols-3">
          {/* 健康度 */}
          <Card className="relative overflow-hidden p-5">
            <div className="pointer-events-none absolute -top-16 -right-16 h-48 w-48 rounded-full bg-accent/10 blur-3xl" />
            <SectionTitle
              action={
                report && (
                  <span className="inline-flex items-center gap-1 text-[12px] text-muted">
                    <Clock size={12} />
                    {timeAgo(report.finishedAt)}
                  </span>
                )
              }
            >
              健康度
            </SectionTitle>
            {report && health ? (
              <>
                <div className="flex items-end gap-3">
                  <div className="text-[56px] leading-none font-semibold tracking-tight">{report.score}</div>
                  <div className={cn("mb-1.5 inline-flex items-center gap-1.5 text-[13px] font-medium", `text-${health.tone}`)}>
                    <health.Icon size={15} />
                    {health.label}
                  </div>
                </div>
                <div className="mt-4 grid grid-cols-3 gap-2">
                  {(["critical", "warning", "info"] as const).map((k) => {
                    const m = severityMeta[k];
                    const n = k === "critical" ? report.critical : k === "warning" ? report.warning : report.info;
                    return (
                      <div key={k} className="rounded-xl border border-line bg-bg2/60 px-3 py-2">
                        <div className={cn("flex items-center gap-1 text-[11.5px]", `text-${m.tone}`)}>
                          <m.Icon size={12} />
                          {m.label}
                        </div>
                        <div className="mt-0.5 text-[18px] font-semibold">{n}</div>
                      </div>
                    );
                  })}
                </div>
                <div className="mt-4 flex gap-2">
                  <Button size="sm" icon={Stethoscope} onClick={() => go("inspect", { run: Date.now() })}>
                    重新巡检
                  </Button>
                  <Button size="sm" variant="soft" icon={Sparkles} onClick={() => go("inspect", { analyze: Date.now() })}>
                    AI 分析
                  </Button>
                </div>
              </>
            ) : (
              <div className="flex flex-col items-start py-2">
                <div className="text-[13px] leading-relaxed text-muted">还没有巡检记录。巡检会检查磁盘、内存、负载、失败服务以及证书到期与同步状态，并给出运维建议。</div>
                <Button className="mt-4" variant="primary" size="sm" icon={Stethoscope} onClick={() => go("inspect", { run: Date.now() })}>
                  开始第一次巡检
                </Button>
              </div>
            )}
          </Card>

          {/* 服务器状态 */}
          <Card className="p-5 @4xl:col-span-2">
            <SectionTitle
              action={
                <div className="flex items-center gap-1">
                  {liveIds.length > 0 && !settings?.general.monitorOff && (
                    <span className="mr-1 inline-flex items-center gap-1.5 text-[12px] text-muted">
                      <Activity size={12} className="text-ok" />
                      每 {interval} 秒刷新
                    </span>
                  )}
                  <Button size="xs" variant="ghost" onClick={() => go("monitor")}>
                    实时监控
                  </Button>
                  <Button size="xs" variant="ghost" onClick={() => go("servers")}>
                    全部
                  </Button>
                </div>
              }
            >
              服务器状态
            </SectionTitle>
            {servers.length === 0 ? (
              <Empty icon={Server} title="还没有服务器" description="添加服务器后即可使用终端、脚本、证书同步和 AI 运维。" action={<Button variant="primary" icon={Plus} onClick={() => go("servers", { add: Date.now() })}>添加服务器</Button>} className="py-6" />
            ) : (
              <div className="grid grid-cols-1 gap-2 @2xl:grid-cols-2">
                {shown.map((s) => (
                  <ServerRow key={s.id} server={s} st={status[s.id]} watched={liveIds.includes(s.id) && !settings?.general.monitorOff} interval={interval} now={now} onOpen={() => openTerminal(s.id)} />
                ))}
              </div>
            )}
          </Card>
        </div>

        <div className="mt-4 grid grid-cols-1 gap-4 @4xl:grid-cols-3">
          {/* 运维建议 */}
          <Card className="p-5 @4xl:col-span-2">
            <SectionTitle
              action={
                report && (
                  <Button size="xs" variant="ghost" onClick={() => go("inspect")}>
                    查看报告
                  </Button>
                )
              }
            >
              运维建议
            </SectionTitle>
            {!report ? (
              <div className="py-6 text-center text-[13px] text-muted">完成一次巡检后，这里会列出需要处理的问题和建议操作</div>
            ) : report.findings.length === 0 ? (
              <div className="flex items-center gap-2 py-6 text-[13px] text-ok">
                <CircleCheck size={16} /> 没有发现问题，继续保持
              </div>
            ) : (
              <div className="space-y-2">
                {report.findings.slice(0, 6).map((f) => {
                  const m = severityMeta[f.severity] ?? severityMeta.info;
                  return (
                    <div key={f.id} className="flex items-center gap-3 rounded-xl border border-line bg-bg2/50 px-3.5 py-2.5">
                      <Badge tone={m.tone} icon={m.Icon}>
                        {m.label}
                      </Badge>
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-[13px] text-fg">
                          {f.serverName && <span className="text-muted">[{f.serverName}] </span>}
                          {f.title}
                        </div>
                        {f.suggestion && <div className="truncate text-[12px] text-muted">{f.suggestion}</div>}
                      </div>
                      {f.action && (
                        <Button size="xs" variant="soft" onClick={() => runFinding(f, go)}>
                          {f.action.label}
                        </Button>
                      )}
                    </div>
                  );
                })}
              </div>
            )}
          </Card>

          {/* 证书到期 */}
          <Card className="p-5">
            <SectionTitle
              action={
                <Button size="xs" variant="ghost" onClick={() => go("certs")}>
                  管理
                </Button>
              }
            >
              证书到期
            </SectionTitle>
            {d.certs.length === 0 ? (
              <div className="py-6 text-center text-[13px] text-muted">还没有导入证书</div>
            ) : (
              <div className="space-y-3.5">
                {d.certs.slice(0, 6).map((c) => {
                  const tone = certTone(c.days, warnDays);
                  const Icon = tone === "ok" ? CircleCheck : tone === "warn" ? TriangleAlert : CircleX;
                  return (
                    <button key={c.id} type="button" className="block w-full text-left" onClick={() => go("certs", { certId: c.id })}>
                      <div className="mb-1.5 flex items-center justify-between gap-2">
                        <span className="truncate text-[12.5px] text-fg">{c.name}</span>
                        <span className={cn("inline-flex shrink-0 items-center gap-1 text-[12px]", `text-${tone}`)}>
                          <Icon size={12} />
                          {c.days < 0 ? `已过期 ${-c.days} 天` : `剩 ${c.days} 天`}
                        </span>
                      </div>
                      <Meter value={Math.min(100, (Math.max(c.days, 0) / 90) * 100)} tone={tone} title={`到期 ${formatDate(c.notAfter)}`} />
                      <div className="mt-1 text-[11px] text-muted">
                        {c.targets} 个部署目标
                        {c.outOfSync > 0 && <span className="text-warn"> · {c.outOfSync} 个待同步</span>}
                        {c.failed > 0 && <span className="text-bad"> · {c.failed} 个失败</span>}
                      </div>
                    </button>
                  );
                })}
              </div>
            )}
          </Card>
        </div>

        <div className="mt-4 grid grid-cols-1 gap-4 @4xl:grid-cols-3">
          {/* 最近操作 */}
          <Card className="p-5 @4xl:col-span-2">
            <SectionTitle
              action={
                <Button size="xs" variant="ghost" onClick={() => go("scripts", { tab: "history" })}>
                  历史
                </Button>
              }
            >
              最近操作
            </SectionTitle>
            {d.recentRuns.length === 0 ? (
              <div className="py-6 text-center text-[13px] text-muted">执行脚本、部署证书、操作服务后会显示在这里</div>
            ) : (
              <div className="divide-y divide-line">
                {d.recentRuns.map((r) => {
                  const ok = r.status === "success";
                  const Icon = r.kind === "cert" ? ShieldCheck : r.kind === "agent" ? Bot : r.kind === "service" ? History : FileCode;
                  return (
                    <div key={r.id} className="flex items-center gap-3 py-2.5">
                      <div className="flex h-8 w-8 items-center justify-center rounded-lg bg-panel3 text-fg2">
                        <Icon size={15} />
                      </div>
                      <div className="min-w-0 flex-1">
                        <div className="truncate text-[13px]">{r.title}</div>
                        <div className="text-[11.5px] text-muted">{timeAgo(r.startedAt)}</div>
                      </div>
                      <Badge tone={ok ? "ok" : r.status === "partial" ? "warn" : r.status === "running" ? "accent" : "bad"} icon={ok ? CircleCheck : r.status === "running" ? Clock : TriangleAlert}>
                        {{ success: "成功", partial: "部分失败", failed: "失败", cancelled: "已取消", running: "执行中" }[r.status] ?? r.status}
                      </Badge>
                    </div>
                  );
                })}
              </div>
            )}
          </Card>

          {/* AI 学习 */}
          <Card className="p-5">
            <SectionTitle
              action={
                <Button size="xs" variant="ghost" onClick={() => go("brain")}>
                  详情
                </Button>
              }
            >
              AI 学习动态
            </SectionTitle>
            <div className="space-y-3">
              {[d.memory, d.userMemory].map((m) => {
                const pct = (m.used / Math.max(m.limit, 1)) * 100;
                return (
                  <div key={m.target}>
                    <div className="mb-1 flex justify-between text-[12px]">
                      <span className="text-fg2">{m.target === "memory" ? "核心记忆 · 环境" : "核心记忆 · 用户画像"}</span>
                      <span className="text-muted">
                        {m.entries.length} 条 · {Math.round(pct)}%
                      </span>
                    </div>
                    <Meter value={pct} tone={pct >= 90 ? "warn" : "accent"} />
                  </div>
                );
              })}
              <div className="flex justify-between text-[12px]">
                <span className="text-fg2">长期记忆</span>
                <span className="text-muted">
                  {d.longMemory.facts} 条 · 往期对话 {d.longMemory.episodes} 段 · {d.longMemory.mode === "semantic" ? "语义向量" : "关键词匹配"}
                </span>
              </div>
            </div>
            <div className="mt-4 space-y-2">
              {d.recentLearning.length === 0 ? (
                <div className="text-[12.5px] text-muted">与 AI 助手对话后，它会把学到的环境知识和操作经验沉淀下来</div>
              ) : (
                d.recentLearning.slice(0, 4).map((l, i) => (
                  <div key={i} className="flex items-start gap-2 text-[12px]">
                    <Brain size={13} className="mt-0.5 shrink-0 text-accent" />
                    <span className="line-clamp-2 text-fg2">
                      <span className="text-muted">{timeAgo(l.time)} · </span>
                      {l.kind === "skill" ? `技能 ${l.target}：` : l.kind === "script" ? `脚本 ${l.target}：` : ""}
                      {l.summary}
                    </span>
                  </div>
                ))
              )}
            </div>
          </Card>
        </div>
      </div>
    </div>
  );
}
