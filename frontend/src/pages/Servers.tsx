import { useEffect, useMemo, useRef, useState } from "react";
import {
  Activity,
  ArrowDown,
  ArrowUp,
  CircleCheck,
  CircleX,
  Copy,
  Cpu,
  Ellipsis,
  Eye,
  EyeOff,
  Fingerprint,
  FolderOpen,
  HardDrive,
  KeyRound,
  LayoutGrid,
  List,
  Lock,
  MemoryStick,
  Pencil,
  Plug,
  Plus,
  RefreshCw,
  Route,
  Search,
  Server as ServerIcon,
  ShieldAlert,
  ShieldCheck,
  SquareTerminal,
  Trash2,
  TriangleAlert,
  Upload,
  Zap,
} from "lucide-react";
import { api, runtime, type AuthorizedKeyView, type PublicKeyView, type Server, type ServerInfo, type TestResult } from "../lib/api";
import { cn, formatBytes, formatDuration, formatRate, parseSSHTarget, timeAgo, usageTone } from "../lib/format";
import { chartWindow, liveState, useMonitorError, useMonitorWatch, useNow, useSeries } from "../lib/monitor";
import { openFilesWindow, useOpenServerIds } from "../lib/term";
import { confirm, toast, useApp } from "../lib/store";
import {
  Badge,
  Button,
  Callout,
  Card,
  CopyButton,
  Drawer,
  Empty,
  Field,
  IconButton,
  Input,
  Menu,
  Meter,
  Modal,
  PageHeader,
  Segmented,
  Select,
  Spinner,
  StatusDot,
  Switch,
  Tabs,
  TagInput,
  Textarea,
} from "../components/ui";
import { KV, ServerAvatar } from "../components/widgets";
import { OpResultList } from "../components/QuickRun";
import { LiveChart, niceMax, SERIES_COLORS } from "../components/charts";

export default function Servers() {
  const { servers, status, loadServers, params, openTerminal } = useApp();
  const [q, setQ] = useState("");
  const [group, setGroup] = useState("all");
  const [view, setView] = useState<"grid" | "list">((localStorage.getItem("servers-view") as "grid" | "list") || "grid");
  const [editing, setEditing] = useState<Server | null | undefined>(undefined);
  const [detail, setDetail] = useState<Server | null>(null);
  const [testing, setTesting] = useState<Record<string, boolean>>({});

  useEffect(() => {
    void loadServers();
  }, [loadServers]);
  useEffect(() => {
    if (params.add) setEditing(null);
  }, [params.add]);
  // 从实时监控等页面跳转过来时直接打开详情
  const openedFor = useRef<unknown>(null);
  useEffect(() => {
    const key = params.at ?? params.detail;
    if (!params.detail || openedFor.current === key) return;
    const s = servers.find((x) => x.id === params.detail);
    if (s) {
      openedFor.current = key;
      setDetail(s);
    }
  }, [params.detail, params.at, servers]);

  const groups = useMemo(() => [...new Set(servers.map((s) => s.group).filter(Boolean))], [servers]);
  const list = servers.filter((s) => {
    if (group !== "all" && (s.group || "") !== (group === "__none" ? "" : group)) return false;
    const kw = q.trim().toLowerCase();
    return !kw || [s.name, s.host, s.group, s.os, s.notes, ...(s.tags ?? [])].join(" ").toLowerCase().includes(kw);
  });
  const online = servers.filter((s) => status[s.id]?.status === "online").length;

  const test = async (s: Server) => {
    setTesting((t) => ({ ...t, [s.id]: true }));
    try {
      const r = await api.TestServer(s.id);
      if (r.ok) toast.ok(`${s.name} ${r.message}`, r.os);
      else toast.error(`${s.name} 连接失败`, r.message);
      void loadServers();
    } finally {
      setTesting((t) => ({ ...t, [s.id]: false }));
    }
  };
  const remove = async (s: Server) => {
    if (!(await confirm({ title: `删除服务器「${s.name}」？`, message: "将同时删除该服务器的证书部署目标和公钥绑定记录，服务器本身不受影响。", danger: true, confirmText: "删除" }))) return;
    try {
      await api.DeleteServer(s.id);
      toast.ok("已删除");
      void loadServers();
    } catch (e) {
      toast.error("删除失败", e);
    }
  };
  const menu = (s: Server) => [
    { label: "编辑", icon: Pencil, onClick: () => setEditing(s) },
    { label: "文件管理", icon: FolderOpen, onClick: () => void openFilesWindow(s.id) },
    { label: "测试连接", icon: Plug, onClick: () => test(s) },
    { label: "复制地址", icon: Copy, onClick: () => runtime.ClipboardSetText(`${s.username}@${s.host} -p ${s.port}`) },
    { label: "", onClick: () => {}, divider: true },
    { label: "删除", icon: Trash2, danger: true, onClick: () => remove(s) },
  ];

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title="服务器"
        icon={ServerIcon}
        description={`共 ${servers.length} 台 · 在线 ${online} 台`}
        actions={
          <>
            <Input icon={Search} placeholder="搜索名称、地址、标签" value={q} onChange={(e) => setQ(e.target.value)} className="w-[240px] max-w-full" />
            <Select value={group} onChange={(e) => setGroup(e.target.value)} className="w-[130px]">
              <option value="all">全部分组</option>
              {groups.map((g) => (
                <option key={g} value={g}>
                  {g}
                </option>
              ))}
              <option value="__none">未分组</option>
            </Select>
            <Segmented
              value={view}
              onChange={(v) => {
                setView(v);
                localStorage.setItem("servers-view", v);
              }}
              options={[
                { value: "grid", label: "", icon: LayoutGrid },
                { value: "list", label: "", icon: List },
              ]}
            />
            <IconButton icon={RefreshCw} tip="刷新状态" onClick={() => api.RefreshStatus()} />
            <Button variant="primary" icon={Plus} onClick={() => setEditing(null)}>
              添加服务器
            </Button>
          </>
        }
      />
      <div className="min-h-0 flex-1 overflow-y-auto px-4 @2xl:px-7 pb-8">
        {servers.length === 0 ? (
          <Card className="mt-2">
            <Empty
              icon={ServerIcon}
              title="添加你的第一台服务器"
              description="支持私钥或密码登录、自定义端口和跳板机。私钥会使用 Windows 账户加密保存在本机。"
              action={
                <Button variant="primary" icon={Plus} onClick={() => setEditing(null)}>
                  添加服务器
                </Button>
              }
            />
          </Card>
        ) : list.length === 0 ? (
          <Empty icon={Search} title="没有匹配的服务器" />
        ) : view === "grid" ? (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(min(310px,100%),1fr))] gap-4">
            {list.map((s) => {
              const st = status[s.id];
              return (
                <Card key={s.id} className="group flex flex-col p-4 transition-all hover:border-line2 hover:shadow-lg">
                  <div className="flex items-start gap-3">
                    <ServerAvatar name={s.name} size={40} />
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="truncate text-[14px] font-semibold">{s.name}</span>
                        <StatusDot status={st?.status} />
                      </div>
                      <div className="mt-0.5 truncate font-mono text-[12px] text-muted">
                        {s.username}@{s.host}:{s.port}
                      </div>
                    </div>
                    <Menu trigger={<IconButton icon={Ellipsis} size="sm" />} items={menu(s)} />
                  </div>
                  <div className="mt-3 flex min-h-[22px] flex-wrap gap-1.5">
                    {s.group && <Badge tone="accent">{s.group}</Badge>}
                    <Badge icon={s.authType === "key" ? KeyRound : Lock}>{s.authType === "key" ? "密钥" : "密码"}</Badge>
                    {s.useSudo && <Badge tone="info">sudo</Badge>}
                    {s.jumpServerId && <Badge icon={Route}>跳板机</Badge>}
                    {(s.tags ?? []).slice(0, 3).map((t) => (
                      <Badge key={t}>{t}</Badge>
                    ))}
                  </div>
                  <div className="mt-3 flex items-center justify-between text-[12px] text-muted">
                    <span className="truncate">{s.os || "系统未知"}</span>
                    <span className={cn("shrink-0", st?.status === "offline" && "text-bad")}>{st?.status === "online" ? `${st.latency} ms` : st?.status === "offline" ? "不可达" : "检测中"}</span>
                  </div>
                  <div className="mt-4 flex gap-2 border-t border-line pt-3">
                    <Button size="sm" variant="primary" icon={SquareTerminal} className="flex-1" onClick={() => openTerminal(s.id)}>
                      终端
                    </Button>
                    <Button size="sm" icon={Activity} className="flex-1" onClick={() => setDetail(s)}>
                      详情
                    </Button>
                    <IconButton icon={Plug} tip="测试连接" loading={testing[s.id]} onClick={() => test(s)} />
                  </div>
                </Card>
              );
            })}
          </div>
        ) : (
          <Card className="overflow-x-auto">
            <table className="w-full min-w-[760px] text-[13px]">
              <thead>
                <tr className="border-b border-line text-left text-[12px] text-muted">
                  <th className="px-4 py-2.5 font-medium">名称</th>
                  <th className="px-4 py-2.5 font-medium">地址</th>
                  <th className="px-4 py-2.5 font-medium">分组 / 标签</th>
                  <th className="px-4 py-2.5 font-medium">系统</th>
                  <th className="px-4 py-2.5 font-medium">状态</th>
                  <th className="w-[160px] px-4 py-2.5" />
                </tr>
              </thead>
              <tbody>
                {list.map((s) => {
                  const st = status[s.id];
                  return (
                    <tr key={s.id} className="border-b border-line last:border-0 hover:bg-panel2/60">
                      <td className="px-4 py-2.5">
                        <div className="flex items-center gap-2.5">
                          <ServerAvatar name={s.name} size={28} />
                          <span className="font-medium">{s.name}</span>
                        </div>
                      </td>
                      <td className="px-4 py-2.5 font-mono text-[12px] text-fg2">
                        {s.username}@{s.host}:{s.port}
                      </td>
                      <td className="px-4 py-2.5">
                        <div className="flex flex-wrap gap-1">
                          {s.group && <Badge tone="accent">{s.group}</Badge>}
                          {(s.tags ?? []).slice(0, 2).map((t) => (
                            <Badge key={t}>{t}</Badge>
                          ))}
                        </div>
                      </td>
                      <td className="max-w-[200px] truncate px-4 py-2.5 text-fg2">{s.os || "-"}</td>
                      <td className="px-4 py-2.5">
                        <span className="inline-flex items-center gap-2">
                          <StatusDot status={st?.status} />
                          <span className="text-fg2">{st?.status === "online" ? `${st.latency} ms` : st?.status === "offline" ? "离线" : "-"}</span>
                        </span>
                      </td>
                      <td className="px-4 py-2.5">
                        <div className="flex justify-end gap-1">
                          <IconButton icon={SquareTerminal} tip="终端" onClick={() => openTerminal(s.id)} />
                          <IconButton icon={Activity} tip="详情" onClick={() => setDetail(s)} />
                          <IconButton icon={Plug} tip="测试" loading={testing[s.id]} onClick={() => test(s)} />
                          <Menu trigger={<IconButton icon={Ellipsis} />} items={menu(s)} />
                        </div>
                      </td>
                    </tr>
                  );
                })}
              </tbody>
            </table>
          </Card>
        )}
      </div>
      <ServerForm open={editing !== undefined} server={editing ?? null} onClose={() => setEditing(undefined)} />
      <ServerDetail server={detail} onClose={() => setDetail(null)} onEdit={(s) => setEditing(s)} />
    </div>
  );
}

// ---- 添加 / 编辑 ----

interface FormState {
  name: string;
  host: string;
  port: number;
  username: string;
  authType: string;
  keyId: string;
  password: string;
  group: string;
  tags: string[];
  jumpServerId: string;
  useSudo: boolean;
  sudoPassword: string;
  notes: string;
}

function ServerForm({ open, server, onClose }: { open: boolean; server: Server | null; onClose: () => void }) {
  const { keys, servers, loadServers, loadKeys, go } = useApp();
  const empty: FormState = { name: "", host: "", port: 22, username: "root", authType: keys.length ? "key" : "password", keyId: keys[0]?.id ?? "", password: "", group: "", tags: [], jumpServerId: "", useSudo: false, sudoPassword: "", notes: "" };
  const [f, setF] = useState<FormState>(empty);
  const [showPw, setShowPw] = useState(false);
  const [advanced, setAdvanced] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [result, setResult] = useState<TestResult | null>(null);

  useEffect(() => {
    if (!open) return;
    void loadKeys();
    setResult(null);
    setShowPw(false);
    if (server) {
      setF({
        name: server.name,
        host: server.host,
        port: server.port,
        username: server.username,
        authType: server.authType,
        keyId: server.keyId || keys[0]?.id || "",
        password: "",
        group: server.group,
        tags: server.tags ?? [],
        jumpServerId: server.jumpServerId,
        useSudo: server.useSudo,
        sudoPassword: "",
        notes: server.notes,
      });
      setAdvanced(!!(server.jumpServerId || server.useSudo || server.notes));
    } else {
      setF(empty);
      setAdvanced(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, server]);

  const set = <K extends keyof FormState>(k: K, v: FormState[K]) => setF((x) => ({ ...x, [k]: v }));
  const input = () => ({ ...f, id: server?.id ?? "", port: Number(f.port) || 22 });
  // 粘贴 ssh 命令或 user@host:port 时自动拆分到各个输入框
  const applyTarget = (text: string, always: boolean) => {
    const p = parseSSHTarget(text);
    if (!p || !(always || p.user || p.port)) return false;
    setF((x) => ({ ...x, host: p.host, username: p.user ?? x.username, port: p.port ?? x.port }));
    const parts = [`地址 ${p.host}`, p.user && `用户 ${p.user}`, p.port && `端口 ${p.port}`].filter(Boolean);
    if (p.user || p.port) toast.info("已自动识别", parts.join(" · "));
    return true;
  };
  const groups = [...new Set(servers.map((s) => s.group).filter(Boolean))];
  const tagSuggestions = [...new Set(servers.flatMap((s) => s.tags ?? []))];

  const doTest = async () => {
    setTesting(true);
    setResult(null);
    try {
      setResult(await api.TestServerInput(input()));
    } catch (e) {
      toast.error("测试失败", e);
    } finally {
      setTesting(false);
    }
  };
  const save = async () => {
    setSaving(true);
    try {
      await api.SaveServer(input());
      toast.ok(server ? "已保存" : "服务器已添加");
      await loadServers();
      onClose();
    } catch (e) {
      toast.error("保存失败", e);
    } finally {
      setSaving(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={server ? `编辑服务器 · ${server.name}` : "添加服务器"}
      description="默认用户为 root、端口 22；建议使用密钥登录。"
      icon={ServerIcon}
      size="lg"
      footer={
        <>
          <Button icon={Plug} loading={testing} onClick={doTest} disabled={!f.host} className="mr-auto">
            测试连接
          </Button>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button variant="primary" loading={saving} onClick={save} disabled={!f.host}>
            保存
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_1.4fr_110px]">
          <Field label="名称">
            <Input value={f.name} onChange={(e) => set("name", e.target.value)} placeholder="例如 web-01" autoFocus />
          </Field>
          <Field label="主机地址" required>
            <Input
              mono
              value={f.host}
              onChange={(e) => set("host", e.target.value)}
              placeholder="IP、域名，或直接粘贴 ssh 命令"
              onPaste={(e) => {
                const text = e.clipboardData.getData("text");
                if (applyTarget(text, /^\s*ssh\s/i.test(text))) e.preventDefault();
              }}
              onBlur={() => applyTarget(f.host, false)}
            />
          </Field>
          <Field label="SSH 端口">
            <Input mono type="number" value={f.port} onChange={(e) => set("port", Number(e.target.value))} />
          </Field>
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="用户名">
            <Input mono value={f.username} onChange={(e) => set("username", e.target.value)} placeholder="root" />
          </Field>
          <Field label="认证方式">
            <Segmented
              className="w-full [&>button]:flex-1 [&>button]:justify-center"
              value={f.authType as "key" | "password"}
              onChange={(v) => set("authType", v)}
              options={[
                { value: "key", label: "私钥", icon: KeyRound },
                { value: "password", label: "密码", icon: Lock },
              ]}
            />
          </Field>
        </div>
        {f.authType === "key" ? (
          <Field
            label="登录私钥"
            action={
              <button type="button" className="text-[12px] text-accent hover:underline" onClick={() => go("keys", { generate: Date.now() })}>
                导入 / 生成私钥
              </button>
            }
            hint={keys.length === 0 ? "还没有私钥，请先到「密钥管理」导入或生成。生成后可一键把公钥部署到服务器。" : undefined}
          >
            <Select value={f.keyId} onChange={(e) => set("keyId", e.target.value)}>
              {keys.length === 0 && <option value="">（没有可用私钥）</option>}
              {keys.map((k) => (
                <option key={k.id} value={k.id}>
                  {k.name} · {k.type} · {k.fingerprint.slice(7, 23)}…
                </option>
              ))}
            </Select>
          </Field>
        ) : (
          <Field label="登录密码" hint={server?.hasPassword ? "已保存密码，留空表示不修改" : "密码使用 Windows 账户加密保存"}>
            <Input
              type={showPw ? "text" : "password"}
              value={f.password}
              onChange={(e) => set("password", e.target.value)}
              placeholder={server?.hasPassword ? "••••••••（不修改）" : "输入密码"}
              right={<IconButton size="sm" icon={showPw ? EyeOff : Eye} onClick={() => setShowPw((v) => !v)} />}
            />
          </Field>
        )}
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="分组">
            <Input value={f.group} onChange={(e) => set("group", e.target.value)} placeholder="例如 生产 / 测试" list="server-groups" />
            <datalist id="server-groups">
              {groups.map((g) => (
                <option key={g} value={g} />
              ))}
            </datalist>
          </Field>
          <Field label="标签">
            <TagInput value={f.tags} onChange={(v) => set("tags", v)} placeholder="回车添加，例如 nginx" suggestions={tagSuggestions} />
          </Field>
        </div>

        <button type="button" onClick={() => setAdvanced((v) => !v)} className="text-[12.5px] font-medium text-accent hover:underline">
          {advanced ? "收起高级选项" : "高级选项：跳板机、sudo、备注"}
        </button>
        {advanced && (
          <div className="space-y-4 rounded-xl border border-line bg-bg2/40 p-4">
            <Field label="跳板机" hint="通过另一台已添加的服务器中转连接（ProxyJump）">
              <Select value={f.jumpServerId} onChange={(e) => set("jumpServerId", e.target.value)}>
                <option value="">不使用跳板机</option>
                {servers
                  .filter((s) => s.id !== server?.id)
                  .map((s) => (
                    <option key={s.id} value={s.id}>
                      {s.name}（{s.host}）
                    </option>
                  ))}
              </Select>
            </Field>
            <div className="flex items-center justify-between gap-4">
              <div>
                <div className="text-[13px] font-medium">使用 sudo 执行特权操作</div>
                <div className="text-[12px] text-muted">非 root 用户执行脚本、部署证书、管理服务时自动通过 sudo 提权</div>
              </div>
              <Switch checked={f.useSudo} onChange={(v) => set("useSudo", v)} />
            </div>
            {f.useSudo && (
              <Field label="sudo 密码" hint={server?.hasSudoPassword ? "已保存，留空表示不修改" : "留空时使用登录密码，或要求 sudoers 配置 NOPASSWD"}>
                <Input type="password" value={f.sudoPassword} onChange={(e) => set("sudoPassword", e.target.value)} placeholder={server?.hasSudoPassword ? "••••••••（不修改）" : "可选"} />
              </Field>
            )}
            <Field label="备注" hint="备注会提供给 AI 助手作为环境信息">
              <Textarea value={f.notes} onChange={(e) => set("notes", e.target.value)} placeholder="例如：生产 Nginx 入口，证书目录 /etc/nginx/ssl" className="min-h-[64px]" />
            </Field>
          </div>
        )}

        {result && (
          <Callout tone={result.ok ? (result.sudoChecked && !result.sudoOk ? "warn" : "ok") : "bad"} title={result.ok ? result.message : "连接失败"}>
            {result.ok ? (
              <div className="space-y-0.5">
                {result.os && <div>系统：{result.os}</div>}
                {result.hostKeyNew && <div className="font-mono text-[11.5px]">新主机密钥：{result.hostKeyFingerprint}</div>}
              </div>
            ) : (
              result.message
            )}
          </Callout>
        )}
      </div>
    </Modal>
  );
}

// ---- 详情抽屉 ----

function Gauge({ label, value, sub, icon: Icon }: { label: string; value: number; sub: string; icon: typeof Cpu }) {
  const tone = usageTone(value);
  return (
    <div className="rounded-xl border border-line bg-bg2/50 p-3">
      <div className="flex items-center justify-between text-[12px] text-muted">
        <span className="inline-flex items-center gap-1.5">
          <Icon size={13} />
          {label}
        </span>
        {tone !== "accent" && <ShieldAlert size={13} className={tone === "bad" ? "text-bad" : "text-warn"} />}
      </div>
      <div className="mt-1.5 text-[20px] font-semibold">{value.toFixed(0)}%</div>
      <Meter value={value} className="mt-2" />
      <div className="mt-1.5 truncate text-[11.5px] text-muted">{sub}</div>
    </div>
  );
}

function ServerDetail({ server, onClose, onEdit }: { server: Server | null; onClose: () => void; onEdit: (s: Server) => void }) {
  const { status, openTerminal, loadServers, settings } = useApp();
  const [tab, setTab] = useState<"info" | "keys" | "host">("info");
  const [info, setInfo] = useState<ServerInfo | null>(null);
  const [infoErr, setInfoErr] = useState("");
  const [loading, setLoading] = useState(false);
  const [authKeys, setAuthKeys] = useState<AuthorizedKeyView[] | null>(null);
  const [keysErr, setKeysErr] = useState("");
  const [deploy, setDeploy] = useState(false);
  const [upgrade, setUpgrade] = useState(false);

  // 实时监控：详情打开且停留在「系统概况」时采样
  const openIds = useOpenServerIds();
  const termOpen = !!server && openIds.includes(server.id);
  const monitorOn = !(settings?.general.monitorOff ?? true) && termOpen;
  const interval = settings?.general.monitorInterval ?? 3;
  useMonitorWatch("drawer", server && tab === "info" ? [server.id] : [], monitorOn);
  const series = useSeries(server?.id);
  const liveErr = useMonitorError(server?.id);
  const now = useNow(1000);
  const lastSample = series[series.length - 1];
  const state = liveState(lastSample, liveErr, now, interval);
  const live = monitorOn && state === "live" ? lastSample : undefined;
  const chart = useMemo(
    () => ({
      times: series.map((s) => s.time),
      cpu: series.map((s) => s.cpu),
      mem: series.map((s) => s.memPct),
      rx: series.map((s) => s.netRx),
      tx: series.map((s) => s.netTx),
      hasNet: series.some((s) => s.hasNet),
    }),
    [series],
  );

  const loadInfo = async (s: Server, silent = false) => {
    if (!silent) {
      setLoading(true);
      setInfoErr("");
    }
    try {
      setInfo(await api.GetServerInfo(s.id));
      setInfoErr("");
    } catch (e) {
      if (!silent) setInfoErr(String(e));
    } finally {
      if (!silent) setLoading(false);
    }
  };
  const loadKeys = async (s: Server) => {
    setAuthKeys(null);
    setKeysErr("");
    try {
      setAuthKeys(await api.ScanAuthorizedKeys(s.id));
    } catch (e) {
      setKeysErr(String(e));
      setAuthKeys([]);
    }
  };
  useEffect(() => {
    if (!server) return;
    setTab("info");
    setInfo(null);
    setAuthKeys(null);
    void loadInfo(server);
  }, [server]);
  useEffect(() => {
    if (server && tab === "keys" && authKeys === null) void loadKeys(server);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [tab, server]);
  // 进程、失败服务、分区这些变化较慢的信息，每 30 秒在后台刷新一次
  useEffect(() => {
    if (!server || tab !== "info" || !monitorOn) return;
    const t = setInterval(() => void loadInfo(server, true), 30000);
    return () => clearInterval(t);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [server, tab, monitorOn]);

  if (!server) return null;
  const st = status[server.id];
  const rootDisk = info?.disks.reduce((a, d) => (d.usePct > (a?.usePct ?? -1) ? d : a), undefined as ServerInfo["disks"][number] | undefined);

  return (
    <Drawer
      open={!!server}
      onClose={onClose}
      width={620}
      title={
        <span className="flex items-center gap-2.5">
          <ServerAvatar name={server.name} size={28} />
          {server.name}
          <StatusDot status={st?.status} />
        </span>
      }
      subtitle={`${server.username}@${server.host}:${server.port}${server.os ? " · " + server.os : ""}`}
      actions={
        <div className="flex gap-1.5">
          <Button size="sm" variant="primary" icon={SquareTerminal} onClick={() => openTerminal(server.id)}>
            终端
          </Button>
          <Button size="sm" icon={Pencil} onClick={() => onEdit(server)}>
            编辑
          </Button>
        </div>
      }
    >
      <Tabs
        className="px-5"
        value={tab}
        onChange={setTab}
        tabs={[
          { key: "info", label: "系统概况", icon: Activity },
          { key: "keys", label: "授权公钥", icon: KeyRound },
          { key: "host", label: "连接信息", icon: Fingerprint },
        ]}
      />
      <div className="p-5">
        {tab === "info" &&
          (loading && !info ? (
            <div className="flex items-center justify-center gap-2 py-16 text-[13px] text-muted">
              <Spinner /> 正在采集系统信息…
            </div>
          ) : infoErr ? (
            <Callout tone="bad" title="无法采集系统信息" action={<Button size="sm" onClick={() => loadInfo(server)}>重试</Button>}>
              {infoErr}
            </Callout>
          ) : info ? (
            <div className="space-y-4">
              <div className="grid grid-cols-3 gap-3">
                <Gauge label="CPU" icon={Cpu} value={live?.cpu ?? info.cpuUsage} sub={`${info.cpus} 核 · 负载 ${(live?.load ?? info.load).map((l) => l.toFixed(2)).join(" ")}`} />
                <Gauge
                  label="内存"
                  icon={MemoryStick}
                  value={live?.memPct ?? (info.memTotal ? ((info.memTotal - info.memAvailable) / info.memTotal) * 100 : 0)}
                  sub={live ? `${formatBytes(live.memUsed)} / ${formatBytes(live.memTotal)}` : `${formatBytes(info.memTotal - info.memAvailable)} / ${formatBytes(info.memTotal)}`}
                />
                <Gauge label={`磁盘 ${rootDisk?.mount ?? ""}`} icon={HardDrive} value={rootDisk?.usePct ?? 0} sub={rootDisk ? `${formatBytes(rootDisk.used)} / ${formatBytes(rootDisk.size)}` : "-"} />
              </div>
              {monitorOn && (
                <Card className="p-4">
                  <div className="mb-3 flex items-center gap-2">
                    <span className="text-[12.5px] font-medium">实时趋势</span>
                    {state === "live" ? (
                      <Badge tone="ok" icon={Activity}>
                        每 {interval} 秒刷新
                      </Badge>
                    ) : state === "connecting" ? (
                      <Badge>正在连接…</Badge>
                    ) : (
                      <Badge tone="warn" icon={TriangleAlert}>
                        {state === "paused" ? "已暂停" : "采集失败，稍后自动重试"}
                      </Badge>
                    )}
                    {live && live.ioWait >= 10 && (
                      <Badge tone="warn" icon={TriangleAlert} title="CPU 有较多时间在等待磁盘读写，可能是磁盘 IO 瓶颈">
                        IO 等待 {live.ioWait.toFixed(0)}%
                      </Badge>
                    )}
                  </div>
                  {liveErr && state !== "live" && state !== "connecting" ? (
                    <Callout
                      tone="warn"
                      action={
                        liveErr.stopped ? (
                          <Button size="sm" onClick={() => api.MonitorRetry(server.id)}>
                            重试
                          </Button>
                        ) : undefined
                      }
                    >
                      {liveErr.error}
                    </Callout>
                  ) : (
                    <>
                      <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
                        <div>
                          <div className="mb-1 text-[11.5px] text-muted">CPU 使用率</div>
                          <LiveChart times={chart.times} series={[{ label: "CPU", color: SERIES_COLORS[0], values: chart.cpu }]} max={100} format={(v) => `${Math.round(v)}%`} height={92} {...chartWindow(interval)} />
                        </div>
                        <div>
                          <div className="mb-1 text-[11.5px] text-muted">内存使用率</div>
                          <LiveChart times={chart.times} series={[{ label: "内存", color: SERIES_COLORS[0], values: chart.mem }]} max={100} format={(v) => `${Math.round(v)}%`} height={92} {...chartWindow(interval)} />
                        </div>
                      </div>
                      {chart.hasNet && (
                        <div className="mt-4">
                          <div className="mb-1 flex items-center gap-1 text-[11.5px] text-muted">
                            网络速率
                            <ArrowDown size={11} className="ml-1" />
                            <ArrowUp size={11} />
                          </div>
                          <LiveChart
                            times={chart.times}
                            series={[
                              { label: "下行", color: SERIES_COLORS[0], values: chart.rx },
                              { label: "上行", color: SERIES_COLORS[1], values: chart.tx },
                            ]}
                            max={niceMax(Math.max(...chart.rx.slice(-60), ...chart.tx.slice(-60), 0), 1024, 10 * 1024)}
                            format={formatRate}
                            height={92}
                            {...chartWindow(interval)}
                          />
                        </div>
                      )}
                    </>
                  )}
                </Card>
              )}
              {info.failedUnits.length > 0 && (
                <Callout tone="warn" title={`${info.failedUnits.length} 个服务处于失败状态`}>
                  {info.failedUnits.join("、")}
                </Callout>
              )}
              <Card className="p-4">
                <KV k="主机名" v={info.hostname} />
                <KV k="系统" v={info.os} />
                <KV k="内核" v={`${info.kernel} (${info.arch})`} mono />
                <KV k="CPU" v={info.cpuModel || "-"} />
                <KV k="运行时长" v={formatDuration(info.uptime)} />
                <KV k="IP 地址" v={info.ips.join("  ") || "-"} mono />
                <KV k="进程 / 登录用户" v={`${info.procs} / ${info.users}`} />
                {info.swapTotal > 0 && <KV k="Swap" v={`${formatBytes(info.swapTotal - info.swapFree)} / ${formatBytes(info.swapTotal)}`} />}
              </Card>
              {info.disks.length > 1 && (
                <Card className="p-4">
                  <div className="mb-2 text-[12.5px] font-medium">磁盘分区</div>
                  <div className="space-y-2.5">
                    {info.disks.map((d) => (
                      <div key={d.mount}>
                        <div className="mb-1 flex justify-between text-[12px]">
                          <span className="font-mono text-fg2">{d.mount}</span>
                          <span className="text-muted">
                            {formatBytes(d.used)} / {formatBytes(d.size)} · {d.usePct.toFixed(0)}%
                          </span>
                        </div>
                        <Meter value={d.usePct} />
                      </div>
                    ))}
                  </div>
                </Card>
              )}
              {info.topProcs.length > 0 && (
                <Card className="p-4">
                  <div className="mb-2 text-[12.5px] font-medium">CPU 占用最高的进程</div>
                  <table className="w-full text-[12px]">
                    <tbody>
                      {info.topProcs.map((p) => (
                        <tr key={p.pid} className="border-b border-line last:border-0">
                          <td className="py-1.5 font-mono text-muted">{p.pid}</td>
                          <td className="py-1.5 text-fg2">{p.command}</td>
                          <td className="py-1.5 text-right" style={{ fontVariantNumeric: "tabular-nums" }}>
                            {p.cpu.toFixed(1)}%
                          </td>
                          <td className="py-1.5 text-right text-muted" style={{ fontVariantNumeric: "tabular-nums" }}>
                            内存 {p.mem.toFixed(1)}%
                          </td>
                        </tr>
                      ))}
                    </tbody>
                  </table>
                </Card>
              )}
              <div className="flex items-center justify-between text-[12px] text-muted">
                <span>
                  {monitorOn ? "CPU、内存、网络实时更新；其余信息" : ""}采集于 {timeAgo(info.collectedAt)}
                  {!termOpen && !settings?.general.monitorOff && " · 打开这台服务器的终端后显示实时趋势"}
                </span>
                <Button size="sm" variant="ghost" icon={RefreshCw} loading={loading} onClick={() => loadInfo(server)}>
                  刷新
                </Button>
              </div>
            </div>
          ) : null)}

        {tab === "keys" && (
          <div className="space-y-3">
            <div className="flex items-center justify-between">
              <div className="text-[12.5px] text-muted">
                {server.username} 用户的 ~/.ssh/authorized_keys
              </div>
              <div className="flex gap-1.5">
                <Button size="sm" icon={RefreshCw} onClick={() => loadKeys(server)}>
                  刷新
                </Button>
                <Button size="sm" variant="primary" icon={Upload} onClick={() => setDeploy(true)}>
                  部署公钥
                </Button>
              </div>
            </div>
            {keysErr && <Callout tone="bad">{keysErr}</Callout>}
            {authKeys === null ? (
              <div className="flex items-center justify-center gap-2 py-12 text-[13px] text-muted">
                <Spinner /> 正在读取…
              </div>
            ) : authKeys.length === 0 && !keysErr ? (
              <Empty icon={KeyRound} title="没有授权的公钥" description="可以把本地密钥的公钥部署到这台服务器，之后即可使用密钥登录。" />
            ) : (
              authKeys.map((k) => (
                <Card key={k.blob} className="p-3.5">
                  <div className="flex items-start gap-3">
                    <div className={cn("flex h-8 w-8 shrink-0 items-center justify-center rounded-lg", k.publicKeyId ? "bg-accent/12 text-accent" : "bg-warn/12 text-warn")}>
                      <KeyRound size={15} />
                    </div>
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-1.5">
                        <span className="truncate text-[13px] font-medium">{k.publicKeyName || k.comment || "（无注释）"}</span>
                        <Badge>{k.type}</Badge>
                        {k.isLoginKey && <Badge tone="ok" icon={CircleCheck}>当前登录密钥</Badge>}
                        {k.publicKeyId ? <Badge tone="accent">已在公钥库</Badge> : <Badge tone="warn" icon={ShieldAlert}>未知来源</Badge>}
                      </div>
                      <div className="mt-1 truncate font-mono text-[11.5px] text-muted">{k.fingerprint}</div>
                      {k.options && <div className="mt-0.5 truncate font-mono text-[11px] text-muted">选项：{k.options}</div>}
                    </div>
                    <div className="flex gap-1">
                      {!k.publicKeyId && (
                        <IconButton
                          icon={Plus}
                          tip="加入公钥库"
                          onClick={async () => {
                            try {
                              await api.ImportPublicKey(k.comment, k.line);
                              toast.ok("已加入公钥库");
                              void loadKeys(server);
                            } catch (e) {
                              toast.error("导入失败", e);
                            }
                          }}
                        />
                      )}
                      <IconButton
                        icon={Trash2}
                        tone="danger"
                        tip={k.isLoginKey ? "当前登录密钥不能删除" : "从服务器移除"}
                        disabled={k.isLoginKey}
                        onClick={async () => {
                          if (!(await confirm({ title: "从 authorized_keys 移除该公钥？", message: `${k.fingerprint}\n移除后持有对应私钥的人将无法登录 ${server.name}。`, danger: true, confirmText: "移除" }))) return;
                          try {
                            await api.RemoveAuthorizedKey(server.id, k.blob);
                            toast.ok("已移除");
                            void loadKeys(server);
                          } catch (e) {
                            toast.error("移除失败", e);
                          }
                        }}
                      />
                    </div>
                  </div>
                </Card>
              ))
            )}
          </div>
        )}

        {tab === "host" && (
          <div className="space-y-4">
            {server.authType === "password" && (
              <Callout
                tone="info"
                title="这台服务器还在用密码登录"
                action={
                  <Button size="sm" variant="soft" icon={ShieldCheck} onClick={() => setUpgrade(true)}>
                    一键改用密钥
                  </Button>
                }
              >
                密钥登录更安全，也不用担心密码泄露。会先验证新密钥能登录，再切换，失败不会改动任何配置。
              </Callout>
            )}
            <Card className="p-4">
              <KV k="地址" v={`${server.host}:${server.port}`} mono />
              <KV k="用户" v={server.username} mono />
              <KV k="认证" v={server.authType === "key" ? "私钥" : "密码"} />
              <KV k="sudo" v={server.useSudo ? "启用" : "未启用"} />
              <KV k="最近检测" v={st?.checkedAt ? `${timeAgo(st.checkedAt)}${st.status === "online" ? ` · ${st.latency} ms` : ""}` : "-"} />
              {st?.error && <KV k="错误" v={st.error} />}
            </Card>
            <Card className="p-4">
              <div className="mb-2 flex items-center gap-2 text-[13px] font-medium">
                <Fingerprint size={15} className="text-accent" /> 主机密钥
              </div>
              {server.hostKeyFingerprint ? (
                <>
                  <div className="flex items-center gap-2 rounded-lg bg-bg2 px-3 py-2 font-mono text-[12px] text-fg2">
                    <span className="truncate">{server.hostKeyFingerprint}</span>
                    <CopyButton text={server.hostKeyFingerprint} />
                  </div>
                  <p className="mt-2 text-[12px] leading-relaxed text-muted">首次连接时自动记录（TOFU）。之后若服务器返回不同的主机密钥，连接会被拒绝以防止中间人攻击。服务器重装系统后可以重新信任。</p>
                  <Button
                    size="sm"
                    className="mt-3"
                    icon={RefreshCw}
                    onClick={async () => {
                      if (!(await confirm({ title: "信任新的主机密钥？", message: "仅在确认服务器重装或更换过 SSH 主机密钥时执行。下次连接会记录新的密钥。", confirmText: "清除并重新信任" }))) return;
                      await api.TrustHostKey(server.id);
                      toast.ok("已清除，下次连接时重新记录");
                      void loadServers();
                    }}
                  >
                    重新信任主机密钥
                  </Button>
                </>
              ) : (
                <div className="text-[12.5px] text-muted">尚未连接过，首次连接时自动记录。</div>
              )}
            </Card>
            {server.notes && (
              <Card className="p-4">
                <div className="mb-1 text-[12.5px] font-medium">备注</div>
                <div className="text-[12.5px] whitespace-pre-wrap text-fg2">{server.notes}</div>
              </Card>
            )}
          </div>
        )}
      </div>
      <DeployKeyModal open={deploy} server={server} onClose={() => setDeploy(false)} onDone={() => loadKeys(server)} />
      <KeyUpgradeModal open={upgrade} server={server} onClose={() => setUpgrade(false)} onDone={onClose} />
    </Drawer>
  );
}

/** 把密码登录的服务器一键切换为密钥登录：生成（或选择）密钥 → 部署公钥 → 验证 → 切换 */
function KeyUpgradeModal({ open, server, onClose, onDone }: { open: boolean; server: Server; onClose: () => void; onDone: () => void }) {
  const { keys, loadKeys, loadServers } = useApp();
  const [keyId, setKeyId] = useState("");
  const [running, setRunning] = useState(false);
  const [result, setResult] = useState<{ ok: boolean; message: string } | null>(null);
  useEffect(() => {
    if (!open) return;
    setKeyId("");
    setResult(null);
    void loadKeys();
  }, [open, loadKeys]);

  const run = async () => {
    setRunning(true);
    setResult(null);
    try {
      let id = keyId;
      if (!id) {
        const k = await api.GenerateKey({ name: `${server.name}-key`, kind: "ed25519", bits: 0, comment: "", passphrase: "" });
        id = k.id;
        await loadKeys();
      }
      const res = await api.RotateKey(id, [server.id], false);
      const r = res[0];
      setResult({ ok: !!r?.ok, message: r?.message ?? "没有返回结果" });
      if (r?.ok) {
        await loadServers();
        toast.ok(`${server.name} 已改用密钥登录`);
      }
    } catch (e) {
      setResult({ ok: false, message: String(e) });
    } finally {
      setRunning(false);
    }
  };

  return (
    <Modal
      open={open}
      onClose={onClose}
      title={`改用密钥登录 · ${server.name}`}
      icon={ShieldCheck}
      size="sm"
      footer={
        result?.ok ? (
          <Button
            variant="primary"
            onClick={() => {
              onClose();
              onDone();
            }}
          >
            完成
          </Button>
        ) : (
          <>
            <Button variant="ghost" onClick={onClose}>
              取消
            </Button>
            <Button variant="primary" loading={running} onClick={run}>
              开始切换
            </Button>
          </>
        )
      }
    >
      <div className="space-y-4">
        <Field label="使用哪把密钥">
          <Select value={keyId} onChange={(e) => setKeyId(e.target.value)} disabled={running || !!result?.ok}>
            <option value="">新生成一把（推荐）</option>
            {keys.map((k) => (
              <option key={k.id} value={k.id}>
                {k.name} · {k.type}
              </option>
            ))}
          </Select>
        </Field>
        <ol className="space-y-1.5 text-[12.5px] leading-relaxed text-fg2">
          <li>1. 把公钥加入服务器的 authorized_keys</li>
          <li>2. 用新密钥试着登录一次，确认可用</li>
          <li>3. 确认无误后，把这台服务器改为密钥登录</li>
        </ol>
        <p className="text-[12px] leading-relaxed text-muted">不会修改服务器的 SSH 配置，原来的密码仍然可以登录。任何一步失败都会保持现状。</p>
        {result && (
          <Callout tone={result.ok ? "ok" : "bad"} title={result.ok ? "切换成功" : "没有切换"}>
            {result.message}
          </Callout>
        )}
      </div>
    </Modal>
  );
}

function DeployKeyModal({ open, server, onClose, onDone }: { open: boolean; server: Server; onClose: () => void; onDone: () => void }) {
  const [pubs, setPubs] = useState<PublicKeyView[]>([]);
  const [sel, setSel] = useState("");
  const [busy, setBusy] = useState(false);
  const [res, setRes] = useState<{ serverName: string; ok: boolean; message: string }[] | null>(null);
  useEffect(() => {
    if (!open) return;
    setRes(null);
    api.ListPublicKeys().then((p) => {
      setPubs(p ?? []);
      setSel(p?.[0]?.id ?? "");
    });
  }, [open]);
  return (
    <Modal
      open={open}
      onClose={onClose}
      title="部署公钥到服务器"
      description={`追加到 ${server.name} 的 authorized_keys，已存在时自动跳过`}
      icon={Zap}
      size="md"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            关闭
          </Button>
          <Button
            variant="primary"
            loading={busy}
            disabled={!sel}
            onClick={async () => {
              setBusy(true);
              try {
                setRes(await api.BindPublicKey(sel, [server.id]));
                onDone();
              } finally {
                setBusy(false);
              }
            }}
          >
            部署
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <Field label="选择公钥">
          <Select value={sel} onChange={(e) => setSel(e.target.value)}>
            {pubs.length === 0 && <option value="">公钥库为空</option>}
            {pubs.map((p) => (
              <option key={p.id} value={p.id}>
                {p.name} · {p.type}
              </option>
            ))}
          </Select>
        </Field>
        {res && <OpResultList results={res} />}
        {res && res.some((r) => r.ok) && (
          <div className="flex items-center gap-1.5 text-[12px] text-muted">
            <CircleX size={12} className="hidden" />
            部署成功后，可在服务器设置中把认证方式切换为该私钥。
          </div>
        )}
      </div>
    </Modal>
  );
}
