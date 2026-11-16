import { useEffect, useState } from "react";
import { Globe, Laptop, Plug, Radar, Route, SquareTerminal, Waypoints } from "lucide-react";
import { api, type ProxyConfig, type ProxyInput, type RouteInput, type Server, type TestResult } from "../lib/api";
import { toast, useApp } from "../lib/store";
import { Button, Callout, Field, Input, Modal, Segmented, Select, Switch } from "./ui";

// ---- 代理表单（服务器设置和线路对话框共用） ----

export interface ProxyForm {
  type: "local" | "http" | "socks5";
  host: string;
  port: string;
  username: string;
  password: string;
  /** 已经保存着这个代理的密码，界面上没有重新填写时沿用 */
  saved: boolean;
}

export const emptyProxy: ProxyForm = { type: "local", host: "", port: "", username: "", password: "", saved: false };

export function proxyFormOf(p?: ProxyConfig | null, password?: string): ProxyForm {
  if (!p || !p.type) return { ...emptyProxy };
  return {
    type: p.type as ProxyForm["type"],
    host: p.type === "local" ? "" : p.host,
    port: p.port ? String(p.port) : "",
    username: p.username ?? "",
    password: password ?? "",
    saved: !!p.hasPassword && !password,
  };
}

export function proxyInputOf(f: ProxyForm): ProxyInput {
  return { type: f.type, host: f.host.trim(), port: Number(f.port) || 0, username: f.username.trim(), password: f.password, keepPassword: f.saved && !f.password };
}

export const noProxy: ProxyInput = { type: "", host: "", port: 0, username: "", password: "", keepPassword: false };

export function proxyText(p?: ProxyConfig | null): string {
  if (!p) return "";
  const kind = p.type === "local" ? "本地代理" : p.type === "socks5" ? "SOCKS5 代理" : "HTTP 代理";
  return `${kind} ${p.host}:${p.port}`;
}

export function ProxyFields({ value, onChange }: { value: ProxyForm; onChange: (v: ProxyForm) => void }) {
  const [detecting, setDetecting] = useState(false);
  const [found, setFound] = useState("");
  const set = (patch: Partial<ProxyForm>) => onChange({ ...value, ...patch });
  // 地址里带着端口（例如粘贴了 socks5://10.0.0.2:1080）时，拆到两个输入框里
  const splitHost = (text: string) => {
    const m = /^(?:[a-z0-9]+:\/\/)?(\[[^\]]+\]|[^\s/:]+):(\d{1,5})\/?$/i.exec(text.trim());
    if (m) set({ host: m[1].replace(/^\[|\]$/g, ""), port: m[2] });
    return !!m;
  };
  const detect = async (silent: boolean) => {
    setDetecting(true);
    try {
      const r = await api.DetectLocalProxy();
      if (r.found) {
        onChange({ ...value, type: "local", port: String(r.port) });
        setFound(`找到了：127.0.0.1:${r.port}（${r.type === "socks5" ? "SOCKS5" : "HTTP"}，来自${r.source}）`);
      } else {
        setFound(silent ? "" : "没有找到正在运行的代理软件。请确认它已经启动，或者手动填写它的端口。");
      }
    } catch {
      setFound("");
    } finally {
      setDetecting(false);
    }
  };
  // 选了本地代理但还没有端口：自动找一次
  useEffect(() => {
    if (value.type === "local" && !value.port) void detect(true);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [value.type]);

  return (
    <div className="space-y-3">
      <Segmented
        className="w-full [&>button]:flex-1 [&>button]:justify-center"
        value={value.type}
        onChange={(v) => {
          setFound("");
          // 本机代理的端口和远程代理的端口不是一回事，在两者之间切换时不沿用
          const crossed = (v === "local") !== (value.type === "local");
          onChange({ ...value, type: v, host: v === "local" ? "" : value.host, port: crossed ? "" : value.port, username: v === "local" ? "" : value.username, password: v === "local" ? "" : value.password });
        }}
        options={[
          { value: "local", label: "本地代理", icon: Laptop },
          { value: "http", label: "HTTP", icon: Globe },
          { value: "socks5", label: "SOCKS5", icon: Waypoints },
        ]}
      />
      {value.type === "local" ? (
        <Field label="本机代理端口" hint={found || "这台电脑上运行的代理软件（Clash、v2rayN 等）监听的端口，HTTP 还是 SOCKS5 会自动识别。"}>
          <div className="flex gap-2">
            <div className="min-w-0 flex-1">
              <Input mono type="number" value={value.port} onChange={(e) => set({ port: e.target.value })} placeholder="例如 7890" />
            </div>
            <Button icon={Radar} loading={detecting} onClick={() => void detect(false)}>
              自动查找
            </Button>
          </div>
        </Field>
      ) : (
        <>
          <div className="grid grid-cols-[1fr_110px] gap-3">
            <Field label="代理地址">
              <Input
                mono
                value={value.host}
                onChange={(e) => set({ host: e.target.value })}
                onBlur={(e) => splitHost(e.target.value)}
                onPaste={(e) => {
                  if (splitHost(e.clipboardData.getData("text"))) e.preventDefault();
                }}
                placeholder={value.type === "http" ? "例如 proxy.example.com" : "例如 10.0.0.2"}
              />
            </Field>
            <Field label="端口">
              <Input mono type="number" value={value.port} onChange={(e) => set({ port: e.target.value })} placeholder={value.type === "http" ? "8080" : "1080"} />
            </Field>
          </div>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            <Field label="用户名（可选）">
              <Input mono value={value.username} onChange={(e) => set({ username: e.target.value, saved: false })} placeholder="代理不需要认证就留空" autoComplete="off" />
            </Field>
            <Field label="密码（可选）">
              <Input mono value={value.password} onChange={(e) => set({ password: e.target.value })} placeholder={value.saved ? "已保存，留空表示不修改" : ""} autoComplete="off" />
            </Field>
          </div>
        </>
      )}
    </div>
  );
}

// ---- 线路对话框 ----

function savedRouteText(s: Server, servers: Server[]): string {
  if (s.jumpServerId) return `经跳板机 ${servers.find((x) => x.id === s.jumpServerId)?.name ?? "（已删除）"}`;
  if (s.proxy) return `经${proxyText(s.proxy)}`;
  return "直连";
}

/** 为一台服务器选择连接线路：直连不通时临时走跳板机或代理，也可以保存为固定线路。由 useApp().openRoute 打开。 */
export function RouteDialog() {
  const { routeFor, openRoute, servers, status, routes, settings, loadRoutes, loadServers, loadSettings, openTerminal, reconnectTab, setActiveTerm, go } = useApp();
  const server = servers.find((s) => s.id === routeFor);
  const [mode, setMode] = useState<RouteInput["mode"]>("jump");
  const [jumpId, setJumpId] = useState("");
  const [proxy, setProxy] = useState<ProxyForm>(emptyProxy);
  const [remember, setRemember] = useState(false);
  const [busy, setBusy] = useState<"" | "test" | "apply" | "connect">("");
  const [result, setResult] = useState<TestResult | null>(null);

  const others = servers.filter((s) => s.id !== routeFor);
  const temp = routeFor ? routes[routeFor] : undefined;

  useEffect(() => {
    if (!server) return;
    setResult(null);
    setRemember(false);
    setBusy("");
    // 依次参考：正在用的临时线路、服务器保存的线路、上次用过的代理
    const cur = temp;
    const online = others.filter((s) => status[s.id]?.status === "online");
    setJumpId(cur?.jumpServerId || server.jumpServerId || online[0]?.id || others[0]?.id || "");
    setProxy(proxyFormOf(cur?.proxy ?? server.proxy ?? settings?.general.lastProxy));
    if (cur) setMode(cur.mode as RouteInput["mode"]);
    else if (server.proxy && !server.jumpServerId) setMode("proxy");
    else setMode(others.length > 0 ? "jump" : "proxy");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [routeFor]);

  if (!server) return null;
  const st = status[server.id];
  const hasSaved = !!(server.jumpServerId || server.proxy);
  const input = (): RouteInput => ({ mode, jumpServerId: mode === "jump" ? jumpId : "", proxy: mode === "proxy" ? proxyInputOf(proxy) : noProxy });
  const close = () => openRoute(null);

  const test = async () => {
    setBusy("test");
    setResult(null);
    try {
      setResult(await api.TestRoute(server.id, input()));
      void loadServers();
    } catch (e) {
      toast.error("测试失败", e);
    } finally {
      setBusy("");
    }
  };
  const apply = async (connect: boolean) => {
    setBusy(connect ? "connect" : "apply");
    try {
      if (remember) {
        await api.SaveServerRoute(server.id, input());
        toast.ok("线路已保存", `${server.name} 以后都这样连接`);
      } else {
        const r = await api.SetTempRoute(server.id, input());
        toast.ok(`${server.name} 已切换到临时线路`, `${r.label}。通过它打开的终端和文件管理窗口全部关闭后自动取消。`);
      }
      await Promise.all([loadRoutes(), loadServers(), loadSettings()]);
      if (connect) {
        // 这台服务器有连接失败的终端标签时直接让它重连，否则新开一个
        const closed = useApp.getState().terms.find((t) => t.serverId === server.id && t.state === "closed");
        if (closed) {
          reconnectTab(closed.key);
          setActiveTerm(closed.key);
          go("terminal");
        } else {
          openTerminal(server.id);
        }
      }
      close();
    } catch (e) {
      toast.error("设置线路失败", e);
    } finally {
      setBusy("");
    }
  };
  const cancelTemp = async () => {
    await api.ClearTempRoute(server.id);
    await loadRoutes();
    toast.ok("已取消临时线路", `${server.name} 恢复为${savedRouteText(server, servers)}`);
    close();
  };

  return (
    <Modal
      open
      onClose={close}
      title={`连接线路 · ${server.name}`}
      description={`${server.username}@${server.host}:${server.port}　直连不通时，可以换一条线路连接这台服务器。`}
      icon={Route}
      size="md"
      footer={
        <>
          <Button icon={Plug} loading={busy === "test"} disabled={!!busy} onClick={test} className="mr-auto">
            测试
          </Button>
          <Button loading={busy === "apply"} disabled={!!busy} onClick={() => void apply(false)}>
            {remember ? "保存" : "仅切换线路"}
          </Button>
          <Button variant="primary" icon={SquareTerminal} loading={busy === "connect"} disabled={!!busy} onClick={() => void apply(true)}>
            {remember ? "保存并打开终端" : "连接终端"}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        {temp ? (
          <Callout
            tone="warn"
            title={`正在使用临时线路：${temp.label}`}
            action={
              <Button size="sm" variant="soft" className="shrink-0" onClick={() => void cancelTemp()}>
                取消临时线路
              </Button>
            }
          >
            通过它打开的终端和文件管理窗口全部关闭后会自动取消；保存的线路是「{savedRouteText(server, servers)}」。
          </Callout>
        ) : (
          <div className="rounded-xl border border-line bg-bg2/50 px-3.5 py-2.5 text-[12.5px] text-fg2">
            当前线路：{savedRouteText(server, servers)}
            {st?.status === "offline" && <span className="text-bad">　·　不可达</span>}
            {st?.status === "online" && <span className="text-ok">　·　{st.latency} ms</span>}
          </div>
        )}

        <Segmented
          className="w-full [&>button]:flex-1 [&>button]:justify-center"
          value={mode}
          onChange={(v) => {
            setMode(v);
            setResult(null);
          }}
          options={[
            { value: "jump", label: "经跳板机", icon: Route },
            { value: "proxy", label: "经代理", icon: Globe },
            ...(hasSaved || temp || remember ? [{ value: "direct" as const, label: "直连", icon: Plug }] : []),
          ]}
        />

        {mode === "jump" &&
          (others.length === 0 ? (
            <Callout tone="info" title="还没有可以当跳板机的服务器">
              跳板机是另一台已经添加到这里、并且能访问 {server.host} 的服务器。先添加它，或者改用代理。
            </Callout>
          ) : (
            <Field label="跳板机" hint={`先登录跳板机，再从它连到 ${server.host}:${server.port}（相当于 ssh -J）。跳板机需要能访问这台服务器。`}>
              <Select
                value={jumpId}
                onChange={(e) => {
                  setJumpId(e.target.value);
                  setResult(null);
                }}
              >
                {others.map((s) => (
                  <option key={s.id} value={s.id}>
                    {status[s.id]?.status === "online" ? "● " : status[s.id]?.status === "offline" ? "○ " : ""}
                    {s.name}（{s.host}）{status[s.id]?.status === "offline" ? " · 不可达" : ""}
                  </option>
                ))}
              </Select>
            </Field>
          ))}
        {mode === "proxy" && (
          <ProxyFields
            value={proxy}
            onChange={(v) => {
              setProxy(v);
              setResult(null);
            }}
          />
        )}
        {mode === "direct" && <div className="text-[12.5px] leading-relaxed text-muted">不经过跳板机和代理，直接连接 {server.host}:{server.port}。</div>}

        <div className="flex items-center justify-between gap-4 rounded-xl border border-line bg-bg2/40 px-3.5 py-3">
          <div>
            <div className="text-[13px] font-medium">记住这条线路</div>
            <div className="text-[12px] leading-relaxed text-muted">{remember ? "保存到服务器设置，以后都这样连接。" : "只用于这次连接：终端和文件管理窗口全部关闭后自动取消，服务器的设置不变。"}</div>
          </div>
          <Switch checked={remember} onChange={setRemember} />
        </div>

        {result && (
          <Callout tone={result.ok ? "ok" : "bad"} title={result.ok ? result.message : "这条线路连不上"}>
            {result.ok ? result.os && <div>系统：{result.os}</div> : result.message}
          </Callout>
        )}
      </div>
    </Modal>
  );
}
