import { useEffect, useMemo, useState } from "react";
import { CircleCheck, CircleDashed, CircleX, FileText, Layers, Play, Power, RefreshCw, RotateCw, Search, Square, Star, Zap } from "lucide-react";
import { api, type OpResult, type ServiceInfo } from "../lib/api";
import { cn } from "../lib/format";
import { confirm, toast, useApp } from "../lib/store";
import { Badge, Button, Empty, Field, IconButton, Input, Modal, Segmented, Select, Spinner, StatusDot } from "../components/ui";
import { OutputBox, ServerAvatar, ServerPicker } from "../components/widgets";
import { OpResultList } from "../components/QuickRun";

const COMMON = ["nginx", "apache2", "httpd", "haproxy", "openresty", "caddy", "docker", "containerd", "mysql", "mysqld", "mariadb", "postgresql", "redis", "redis-server", "mongod", "php-fpm", "php8.1-fpm", "php8.2-fpm", "php8.3-fpm", "sshd", "ssh", "cron", "crond", "firewalld", "ufw", "fail2ban", "supervisor", "elasticsearch", "rabbitmq-server", "kubelet", "nfs-server", "postfix"];

function stateMeta(s: ServiceInfo) {
  if (s.active === "active") return { label: s.sub === "running" ? "运行中" : s.sub, tone: "ok" as const, Icon: CircleCheck };
  if (s.active === "failed") return { label: "失败", tone: "bad" as const, Icon: CircleX };
  if (s.active === "activating" || s.active === "reloading") return { label: "启动中", tone: "info" as const, Icon: RefreshCw };
  return { label: "已停止", tone: "neutral" as const, Icon: CircleDashed };
}

export default function Services() {
  const { servers, status } = useApp();
  const [srv, setSrv] = useState<string | null>(null);
  const [list, setList] = useState<ServiceInfo[] | null>(null);
  const [err, setErr] = useState("");
  const [q, setQ] = useState("");
  const [filter, setFilter] = useState<"common" | "running" | "failed" | "all">("common");
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const [text, setText] = useState<{ title: string; body: string } | null>(null);
  const [batch, setBatch] = useState(false);

  useEffect(() => {
    if (!srv && servers.length) setSrv(servers[0].id);
  }, [servers, srv]);

  const load = async (id: string) => {
    setList(null);
    setErr("");
    try {
      setList(await api.ListServices(id));
    } catch (e) {
      setErr(String(e));
      setList([]);
    }
  };
  useEffect(() => {
    if (srv) void load(srv);
  }, [srv]);

  const shown = useMemo(() => {
    let l = list ?? [];
    if (filter === "common") l = l.filter((s) => COMMON.includes(s.name) || s.active === "failed");
    if (filter === "running") l = l.filter((s) => s.active === "active");
    if (filter === "failed") l = l.filter((s) => s.active === "failed");
    const kw = q.trim().toLowerCase();
    if (kw) l = (list ?? []).filter((s) => (s.name + s.description).toLowerCase().includes(kw));
    return l;
  }, [list, filter, q]);

  const act = async (s: ServiceInfo, action: string) => {
    if (!srv) return;
    if (["stop", "restart"].includes(action) && !(await confirm({ title: `${action === "stop" ? "停止" : "重启"}服务 ${s.name}？`, message: "可能会短暂中断该服务。", danger: action === "stop", confirmText: "确定" }))) return;
    const key = s.name + action;
    setBusy((b) => ({ ...b, [key]: true }));
    try {
      const r = await api.ServiceAction([srv], s.name, action);
      if (r[0]?.ok) toast.ok(`${s.name} ${action} 成功`, r[0].message);
      else toast.error(`${s.name} ${action} 失败`, r[0]?.message);
      void load(srv);
    } finally {
      setBusy((b) => ({ ...b, [key]: false }));
    }
  };
  const show = async (s: ServiceInfo, kind: "status" | "logs") => {
    if (!srv) return;
    setText({ title: `${s.name} · ${kind === "status" ? "状态" : "日志"}`, body: "" });
    try {
      const body = kind === "status" ? await api.ServiceStatus(srv, s.name) : await api.ServiceLogs(srv, s.name, 200);
      setText({ title: `${s.name} · ${kind === "status" ? "状态" : "最近 200 行日志"}`, body });
    } catch (e) {
      setText({ title: s.name, body: String(e) });
    }
  };

  const current = servers.find((s) => s.id === srv);
  return (
    <div className="flex h-full flex-col overflow-y-auto @3xl:flex-row @3xl:overflow-hidden">
      <div className="flex max-h-[min(240px,45vh)] w-full shrink-0 flex-col border-b border-line bg-bg2/40 @3xl:max-h-none @3xl:w-[260px] @3xl:border-r @3xl:border-b-0">
        <div className="flex items-center justify-between p-4 pb-3">
          <h1 className="text-[16px] font-semibold">服务管理</h1>
          <Button size="sm" variant="soft" icon={Zap} onClick={() => setBatch(true)}>
            批量
          </Button>
        </div>
        <div className="min-h-0 flex-1 space-y-0.5 overflow-y-auto px-2 pb-4">
          {servers.length === 0 && <div className="px-3 py-6 text-center text-[12.5px] text-muted">还没有服务器</div>}
          {servers.map((s) => (
            <button key={s.id} type="button" onClick={() => setSrv(s.id)} className={cn("flex w-full items-center gap-2.5 rounded-[10px] px-2.5 py-2 text-left transition-colors", srv === s.id ? "bg-accent/12" : "hover:bg-panel2")}>
              <ServerAvatar name={s.name} size={28} />
              <div className="min-w-0 flex-1">
                <div className="truncate text-[13px] font-medium">{s.name}</div>
                <div className="truncate font-mono text-[11px] text-muted">{s.host}</div>
              </div>
              <StatusDot status={status[s.id]?.status} />
            </button>
          ))}
        </div>
      </div>
      <div className="flex min-w-0 shrink-0 flex-col @3xl:min-h-0 @3xl:flex-1 @3xl:shrink">
        {current ? (
          <>
            <div className="flex flex-wrap items-center gap-x-3 gap-y-2 px-4 pt-4 pb-3 @2xl:px-6 @2xl:pt-5 @2xl:pb-4">
              <div className="min-w-0 flex-1">
                <div className="text-[17px] font-semibold">{current.name}</div>
                <div className="text-[12px] text-muted">
                  {list ? `共 ${list.length} 个服务 · 运行 ${list.filter((s) => s.active === "active").length} · 失败 ${list.filter((s) => s.active === "failed").length}` : "加载中…"}
                </div>
              </div>
              <Segmented
                value={filter}
                onChange={setFilter}
                options={[
                  { value: "common", label: "常用", icon: Star },
                  { value: "running", label: "运行中" },
                  { value: "failed", label: "失败" },
                  { value: "all", label: "全部" },
                ]}
              />
              <Input icon={Search} value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索服务" className="w-[200px] max-w-full" />
              <IconButton icon={RefreshCw} tip="刷新" onClick={() => srv && load(srv)} />
            </div>
            <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-6 @2xl:px-6">
              {err && <div className="mb-3 rounded-xl border border-bad/30 bg-bad/6 px-4 py-3 text-[12.5px] text-bad">{err}</div>}
              {list === null ? (
                <div className="flex items-center justify-center gap-2 py-20 text-[13px] text-muted">
                  <Spinner /> 正在读取服务列表…
                </div>
              ) : shown.length === 0 ? (
                <Empty icon={Layers} title={filter === "common" ? "没有识别到常用服务" : "没有匹配的服务"} description={filter === "common" ? "切换到「全部」查看所有 systemd 服务" : undefined} />
              ) : (
                <div className="card overflow-hidden">
                  {shown.map((s) => {
                    const m = stateMeta(s);
                    return (
                      <div key={s.name} className="flex items-center gap-4 border-b border-line px-4 py-3 last:border-0 hover:bg-panel2/50">
                        <div className="min-w-0 flex-1">
                          <div className="flex items-center gap-2">
                            <span className="font-mono text-[13px] font-medium text-fg">{s.name}</span>
                            <Badge tone={m.tone} icon={m.Icon}>
                              {m.label}
                            </Badge>
                            {s.enabled && <Badge>{s.enabled === "enabled" ? "开机启动" : s.enabled}</Badge>}
                          </div>
                          {s.description && <div className="mt-0.5 truncate text-[12px] text-muted">{s.description}</div>}
                        </div>
                        <div className="flex shrink-0 items-center gap-1">
                          {s.active === "active" ? (
                            <>
                              <Button size="sm" icon={RotateCw} loading={busy[s.name + "reload"]} onClick={() => act(s, "reload")}>
                                重载
                              </Button>
                              <Button size="sm" icon={RefreshCw} loading={busy[s.name + "restart"]} onClick={() => act(s, "restart")}>
                                重启
                              </Button>
                              <IconButton icon={Square} tone="danger" tip="停止" loading={busy[s.name + "stop"]} onClick={() => act(s, "stop")} />
                            </>
                          ) : (
                            <Button size="sm" variant="soft" icon={Play} loading={busy[s.name + "start"]} onClick={() => act(s, "start")}>
                              启动
                            </Button>
                          )}
                          <IconButton icon={Power} tip="状态详情" onClick={() => show(s, "status")} />
                          <IconButton icon={FileText} tip="查看日志" onClick={() => show(s, "logs")} />
                        </div>
                      </div>
                    );
                  })}
                </div>
              )}
            </div>
          </>
        ) : (
          <Empty icon={Layers} title="选择一台服务器" description="查看并管理 systemd 服务：启动、停止、重启、重载，查看状态与日志" className="pt-24" />
        )}
      </div>
      <Modal open={!!text} onClose={() => setText(null)} title={text?.title ?? ""} icon={FileText} size="xl">
        {text && (text.body ? <OutputBox text={text.body} className="max-h-[60vh]" /> : <div className="flex justify-center py-10"><Spinner /></div>)}
      </Modal>
      <BatchModal open={batch} onClose={() => setBatch(false)} />
    </div>
  );
}

function BatchModal({ open, onClose }: { open: boolean; onClose: () => void }) {
  const [service, setService] = useState("nginx");
  const [action, setAction] = useState("reload");
  const [targets, setTargets] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [res, setRes] = useState<OpResult[] | null>(null);
  useEffect(() => {
    if (open) setRes(null);
  }, [open]);
  return (
    <Modal
      open={open}
      onClose={onClose}
      title="批量操作服务"
      icon={Zap}
      size="lg"
      description="对多台服务器上的同一服务执行操作，例如统一重载 nginx"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            关闭
          </Button>
          <Button
            variant="primary"
            loading={busy}
            disabled={!service || targets.length === 0}
            onClick={async () => {
              if (["stop", "restart"].includes(action) && !(await confirm({ title: `在 ${targets.length} 台服务器上 ${action} ${service}？`, danger: action === "stop", confirmText: "执行" }))) return;
              setBusy(true);
              try {
                setRes(await api.ServiceAction(targets, service, action));
              } finally {
                setBusy(false);
              }
            }}
          >
            执行
          </Button>
        </>
      }
    >
      {res ? (
        <OpResultList results={res} />
      ) : (
        <div className="space-y-4">
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_160px]">
            <Field label="服务名">
              <Input mono value={service} onChange={(e) => setService(e.target.value)} list="svc-common" />
              <datalist id="svc-common">
                {COMMON.map((c) => (
                  <option key={c} value={c} />
                ))}
              </datalist>
            </Field>
            <Field label="操作">
              <Select value={action} onChange={(e) => setAction(e.target.value)}>
                <option value="reload">重载 reload</option>
                <option value="restart">重启 restart</option>
                <option value="start">启动 start</option>
                <option value="stop">停止 stop</option>
                <option value="enable">开机启动 enable</option>
                <option value="disable">取消开机启动 disable</option>
              </Select>
            </Field>
          </div>
          <ServerPicker value={targets} onChange={setTargets} maxHeight={300} />
        </div>
      )}
    </Modal>
  );
}
