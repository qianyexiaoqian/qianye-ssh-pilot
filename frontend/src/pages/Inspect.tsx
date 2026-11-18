import { useEffect, useRef, useState } from "react";
import { Bot, CircleCheck, CircleX, Clock, Cpu, HardDrive, MemoryStick, Repeat, Sparkles, Stethoscope, Trash2, WifiOff } from "lucide-react";
import { api, on, type Finding, type Report } from "../lib/api";
import { cn, formatDuration, timeAgo } from "../lib/format";
import { toast, useApp } from "../lib/store";
import { Badge, Button, Card, Empty, Meter, PageHeader, Segmented, SectionTitle, Select } from "../components/ui";
import { ServerPicker, useScrub } from "../components/widgets";
import { healthLabel, runFinding, severityMeta } from "./Dashboard";

const AUTO_OPTIONS = [
  { mins: 0, label: "关闭" },
  { mins: 30, label: "每 30 分钟" },
  { mins: 60, label: "每小时" },
  { mins: 360, label: "每 6 小时" },
  { mins: 1440, label: "每天" },
];

export default function Inspect() {
  const { params, go, settings, loadSettings } = useApp();
  const scrub = useScrub();
  const auto = settings?.general.inspectInterval ?? 0;
  const setAuto = async (mins: number) => {
    try {
      await api.SetInspectInterval(mins);
      await loadSettings();
      toast.ok(mins ? "已开启自动巡检" : "已关闭自动巡检", mins ? "到时间会自动巡检全部服务器，发现新的严重问题时提醒你" : undefined);
    } catch (e) {
      toast.error("保存失败", e);
    }
  };
  const [report, setReport] = useState<Report | null>(null);
  const [scope, setScope] = useState<"all" | "pick">("all");
  const [targets, setTargets] = useState<string[]>([]);
  const [running, setRunning] = useState(false);
  const [progress, setProgress] = useState<{ done: number; total: number; server: string } | null>(null);
  const [sev, setSev] = useState<"all" | "critical" | "warning" | "info">("all");
  const handled = useRef<Record<string, boolean>>({});

  useEffect(() => {
    api.GetLastInspection().then((r) => r && setReport(r));
    const offs = [
      on<{ done: number; total: number; server: string }>("inspect:progress", (p) => setProgress(p)),
      on<Report>("inspect:done", (r) => setReport(r)),
    ];
    return () => offs.forEach((f) => f());
  }, []);

  const run = async () => {
    setRunning(true);
    setProgress(null);
    try {
      const r = await api.RunInspection(scope === "all" ? [] : targets);
      setReport(r);
      toast.ok(`巡检完成，健康分 ${r.score}`, `${r.critical} 个严重、${r.warning} 个警告`);
    } catch (e) {
      toast.error("巡检失败", e);
    } finally {
      setRunning(false);
      setProgress(null);
    }
  };
  const analyze = async () => {
    try {
      const prompt = await api.InspectionPrompt();
      go("assistant", { prompt, newSession: true });
    } catch (e) {
      toast.error("无法分析", e);
    }
  };
  useEffect(() => {
    if (params.run && !handled.current["run" + params.run]) {
      handled.current["run" + params.run] = true;
      void run();
    }
    if (params.analyze && !handled.current["an" + params.analyze]) {
      handled.current["an" + params.analyze] = true;
      void analyze();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params]);

  const health = report ? healthLabel(report.score) : null;
  const findings = (report?.findings ?? []).filter((f) => sev === "all" || f.severity === sev);
  const ask = (f: Finding) =>
    go("assistant", {
      newSession: true,
      prompt: `巡检发现了一个问题，请帮我分析原因并给出处理建议（需要时直接用工具诊断，只读命令优先）：\n\n- 级别：${severityMeta[f.severity]?.label ?? f.severity}\n- 服务器：${f.serverName || "（全局）"}\n- 问题：${f.title}\n- 详情：${f.detail || "无"}\n- 规则建议：${f.suggestion || "无"}`,
    });

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title="智能巡检"
        icon={Stethoscope}
        description="检查磁盘、内存、负载、失败服务、证书到期与同步状态；结合 AI 记忆给出运维建议"
        actions={
          <>
            {report && (
              <Button icon={Trash2} variant="ghost" onClick={async () => { await api.ClearInspection(); setReport(null); }}>
                清除报告
              </Button>
            )}
            {report && (
              <Button icon={Sparkles} variant="soft" onClick={analyze}>
                AI 深度分析
              </Button>
            )}
            <Button variant="primary" icon={Stethoscope} loading={running} onClick={run}>
              {running ? "巡检中…" : "开始巡检"}
            </Button>
          </>
        }
      />
      <div className="min-h-0 flex-1 overflow-y-auto px-4 @2xl:px-7 pb-8">
        <div className="grid grid-cols-1 gap-5 @4xl:grid-cols-[340px_1fr]">
          <div className="min-w-0 space-y-4">
            <Card className="p-5">
              <SectionTitle
                action={
                  auto > 0 ? (
                    <Badge tone="accent" icon={Repeat}>
                      已开启
                    </Badge>
                  ) : undefined
                }
              >
                自动巡检
              </SectionTitle>
              <Select value={String(auto)} onChange={(e) => setAuto(Number(e.target.value))}>
                {AUTO_OPTIONS.map((o) => (
                  <option key={o.mins} value={o.mins}>
                    {o.label}
                  </option>
                ))}
                {!AUTO_OPTIONS.some((o) => o.mins === auto) && <option value={auto}>每 {auto} 分钟</option>}
              </Select>
              <p className="mt-2 text-[12px] leading-relaxed text-muted">按设定的间隔自动巡检全部服务器并更新健康分；出现新的严重问题时弹出提醒。需要保持 QianYe SSH Pilot 运行。</p>
            </Card>
            <Card className="p-5">
              <SectionTitle>巡检范围</SectionTitle>
              <Segmented
                value={scope}
                onChange={setScope}
                options={[
                  { value: "all", label: "全部服务器" },
                  { value: "pick", label: "选择服务器" },
                ]}
              />
              {scope === "pick" && <ServerPicker className="mt-3" value={targets} onChange={setTargets} maxHeight={240} />}
              {running && (
                <div className="mt-4">
                  <div className="mb-1.5 flex justify-between text-[12px] text-muted">
                    <span className="truncate">{progress ? `已完成 ${progress.server}` : "正在连接服务器…"}</span>
                    <span>{progress ? `${progress.done}/${progress.total}` : ""}</span>
                  </div>
                  <Meter tone="accent" value={progress ? (progress.done / Math.max(progress.total, 1)) * 100 : 3} />
                </div>
              )}
            </Card>
            {report && health && (
              <Card className="relative overflow-hidden p-5">
                <div className="pointer-events-none absolute -top-20 -right-20 h-56 w-56 rounded-full bg-accent/10 blur-3xl" />
                <div className="text-[12.5px] text-muted">健康分</div>
                <div className="mt-1 flex items-end gap-3">
                  <span className="text-[64px] leading-none font-semibold tracking-tight">{report.score}</span>
                  <span className={cn("mb-2 inline-flex items-center gap-1.5 text-[13.5px] font-medium", `text-${health.tone}`)}>
                    <health.Icon size={16} />
                    {health.label}
                  </span>
                </div>
                <div className="mt-4 space-y-2">
                  {(["critical", "warning", "info"] as const).map((k) => {
                    const m = severityMeta[k];
                    const n = k === "critical" ? report.critical : k === "warning" ? report.warning : report.info;
                    return (
                      <div key={k} className="flex items-center justify-between text-[13px]">
                        <span className={cn("inline-flex items-center gap-1.5", `text-${m.tone}`)}>
                          <m.Icon size={14} />
                          {m.label}
                        </span>
                        <span className="font-semibold">{n}</span>
                      </div>
                    );
                  })}
                </div>
                <div className="mt-4 flex items-center gap-1.5 border-t border-line pt-3 text-[12px] text-muted">
                  <Clock size={12} /> {timeAgo(report.finishedAt)} · {report.servers.length} 台服务器
                </div>
              </Card>
            )}
          </div>

          <div className="min-w-0 space-y-5">
            {!report ? (
              <Card>
                <Empty
                  icon={Stethoscope}
                  title="还没有巡检报告"
                  description="巡检会并发采集每台服务器的系统指标，结合证书库检查到期与同步状态，生成按严重程度排序的问题清单和处理建议。"
                  action={
                    <Button variant="primary" icon={Stethoscope} loading={running} onClick={run}>
                      开始巡检
                    </Button>
                  }
                />
              </Card>
            ) : (
              <>
                <div>
                  <div className="mb-3 flex items-center justify-between">
                    <h3 className="text-[14px] font-semibold">发现的问题</h3>
                    <Segmented
                      size="sm"
                      value={sev}
                      onChange={setSev}
                      options={[
                        { value: "all", label: `全部 ${report.findings.length}` },
                        { value: "critical", label: `严重 ${report.critical}` },
                        { value: "warning", label: `警告 ${report.warning}` },
                        { value: "info", label: `提示 ${report.info}` },
                      ]}
                    />
                  </div>
                  {findings.length === 0 ? (
                    <Card className="flex items-center gap-2 px-5 py-6 text-[13px] text-ok">
                      <CircleCheck size={16} /> {report.findings.length === 0 ? "一切正常，没有发现问题" : "该级别下没有问题"}
                    </Card>
                  ) : (
                    <div className="space-y-2.5">
                      {findings.map((f) => {
                        const m = severityMeta[f.severity] ?? severityMeta.info;
                        return (
                          <Card key={f.id} className={cn("border-l-[3px] p-4", f.severity === "critical" ? "border-l-bad" : f.severity === "warning" ? "border-l-warn" : "border-l-info")}>
                            <div className="flex items-start gap-3">
                              <div className="min-w-0 flex-1">
                                <div className="flex flex-wrap items-center gap-2">
                                  <Badge tone={m.tone} icon={m.Icon}>
                                    {m.label}
                                  </Badge>
                                  <Badge>{f.category}</Badge>
                                  {f.serverName && <span className="text-[12px] text-muted">{f.serverName}</span>}
                                </div>
                                <div className="mt-1.5 text-[13.5px] font-medium text-fg">{scrub(f.title)}</div>
                                {f.detail && <div className="mt-0.5 text-[12.5px] break-all text-fg2">{scrub(f.detail)}</div>}
                                {f.suggestion && <div className="mt-1.5 text-[12.5px] text-muted">建议：{f.suggestion}</div>}
                              </div>
                              <div className="flex shrink-0 gap-1.5">
                                <Button size="sm" variant="ghost" icon={Bot} onClick={() => ask(f)}>
                                  问 AI
                                </Button>
                                {f.action && (
                                  <Button size="sm" variant="soft" onClick={() => runFinding(f, go)}>
                                    {f.action.label}
                                  </Button>
                                )}
                              </div>
                            </div>
                          </Card>
                        );
                      })}
                    </div>
                  )}
                </div>

                <div>
                  <h3 className="mb-3 text-[14px] font-semibold">服务器指标</h3>
                  <Card className="overflow-x-auto">
                    <table className="w-full min-w-[620px] text-[12.5px]">
                      <thead>
                        <tr className="border-b border-line text-left text-[12px] text-muted">
                          <th className="px-4 py-2.5 font-medium">服务器</th>
                          <th className="w-[140px] px-4 py-2.5 font-medium">
                            <span className="inline-flex items-center gap-1">
                              <Cpu size={12} /> CPU
                            </span>
                          </th>
                          <th className="w-[140px] px-4 py-2.5 font-medium">
                            <span className="inline-flex items-center gap-1">
                              <MemoryStick size={12} /> 内存
                            </span>
                          </th>
                          <th className="w-[140px] px-4 py-2.5 font-medium">
                            <span className="inline-flex items-center gap-1">
                              <HardDrive size={12} /> 磁盘(最高)
                            </span>
                          </th>
                          <th className="px-4 py-2.5 font-medium">负载</th>
                          <th className="px-4 py-2.5 font-medium">运行</th>
                        </tr>
                      </thead>
                      <tbody>
                        {report.servers.map((s) => {
                          const in_ = s.info;
                          if (!s.online || !in_) {
                            return (
                              <tr key={s.serverId} className="border-b border-line last:border-0">
                                <td className="px-4 py-3 font-medium">{s.serverName}</td>
                                <td colSpan={5} className="px-4 py-3 text-bad">
                                  <span className="inline-flex items-center gap-1.5">
                                    <WifiOff size={13} /> {scrub(s.error) || "无法连接"}
                                  </span>
                                </td>
                              </tr>
                            );
                          }
                          const mem = in_.memTotal ? ((in_.memTotal - in_.memAvailable) / in_.memTotal) * 100 : 0;
                          const disk = Math.max(0, ...in_.disks.map((d) => d.usePct));
                          const cell = (v: number) => (
                            <div>
                              <div className="mb-1 text-fg2" style={{ fontVariantNumeric: "tabular-nums" }}>
                                {v.toFixed(0)}%
                              </div>
                              <Meter value={v} height={4} />
                            </div>
                          );
                          return (
                            <tr key={s.serverId} className="border-b border-line last:border-0">
                              <td className="px-4 py-3">
                                <div className="font-medium">{s.serverName}</div>
                                <div className="truncate text-[11.5px] text-muted">{in_.os}</div>
                              </td>
                              <td className="px-4 py-3">{cell(in_.cpuUsage)}</td>
                              <td className="px-4 py-3">{cell(mem)}</td>
                              <td className="px-4 py-3">{cell(disk)}</td>
                              <td className="px-4 py-3 font-mono text-[12px] text-fg2">
                                {in_.load.map((l) => l.toFixed(2)).join(" ")}
                                <div className="font-sans text-[11px] text-muted">{in_.cpus} 核</div>
                              </td>
                              <td className="px-4 py-3 text-fg2">
                                {formatDuration(in_.uptime)}
                                {in_.failedUnits.length > 0 && (
                                  <div className="inline-flex items-center gap-1 text-[11.5px] text-warn">
                                    <CircleX size={11} /> {in_.failedUnits.length} 个失败服务
                                  </div>
                                )}
                              </td>
                            </tr>
                          );
                        })}
                      </tbody>
                    </table>
                  </Card>
                </div>
              </>
            )}
          </div>
        </div>
      </div>
    </div>
  );
}
