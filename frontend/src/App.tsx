import { useEffect, useState } from "react";
import { on, type AgentEvent, type AutoSyncResult, type LockState, type Sample, type ServerStatus, type Transfer } from "./lib/api";
import { toast, useApp } from "./lib/store";
import { dispatchAgentEvent, useAgent } from "./lib/agent";
import { initFileDrop } from "./lib/drop";
import { initWindowChrome } from "./lib/chrome";
import { useMonitor } from "./lib/monitor";
import { useQuick } from "./lib/quick";
import { useTransfers } from "./lib/files";
import { CommandPalette, ConfirmHost, FilesTitleBar, Sidebar, TitleBar, Toaster } from "./components/shell";
import { Button, Callout } from "./components/ui";
import { ErrorBoundary } from "./components/ErrorBoundary";
import { QuickHost } from "./components/QuickCommands";
import { LockScreen } from "./components/LockScreen";
import { RouteDialog } from "./components/RouteDialog";
import Dashboard from "./pages/Dashboard";
import Monitor from "./pages/Monitor";
import Files from "./pages/Files";
import FilesLauncher from "./pages/FilesLauncher";
import Servers from "./pages/Servers";
import Terminals from "./pages/Terminals";
import Keys from "./pages/Keys";
import Scripts from "./pages/Scripts";
import Certs from "./pages/Certs";
import Services from "./pages/Services";
import Inspect from "./pages/Inspect";
import Assistant from "./pages/Assistant";
import Brain from "./pages/Brain";
import SettingsPage from "./pages/Settings";

export default function App() {
  const { theme, info, lock, loadInfo, loadLock } = useApp();

  useEffect(() => {
    document.documentElement.dataset.theme = theme;
  }, [theme]);
  useEffect(() => {
    void loadInfo();
    void loadLock();
    const offLock = on<LockState>("lock:changed", (l) => l && useApp.getState().setLock(l));
    // 主窗口和文件管理窗口共用界面偏好：在一个窗口里切换主题，其他窗口跟着切换
    const onStorage = (e: StorageEvent) => {
      if (e.key === "theme" && (e.newValue === "dark" || e.newValue === "light")) useApp.setState({ theme: e.newValue });
    };
    window.addEventListener("storage", onStorage);
    return () => {
      offLock();
      window.removeEventListener("storage", onStorage);
    };
  }, [loadInfo, loadLock]);

  useEffect(() => {
    if (info) initWindowChrome(info.nativeResize);
  }, [info]);

  // 先确定当前窗口是主窗口还是某台服务器的文件管理窗口，再渲染对应的界面
  if (!info || !lock) return <div className="h-full bg-bg" />;
  // 设置了启动密码、还没有输入：只显示解锁页，主界面等解锁后再加载
  if (lock.sealed) return <LockScreen />;
  return (
    <>
      {info.window === "files" ? <FilesApp serverId={info.windowServer} /> : <MainApp />}
      {/* 运行中锁定：主界面留在下面（终端继续运行），由锁屏盖住 */}
      {lock.locked && <LockScreen />}
    </>
  );
}

/** 文件管理独立窗口：只有标题栏和一台服务器的文件管理 */
function FilesApp({ serverId }: { serverId: string }) {
  const { servers, info, loadServers, loadSettings } = useApp();
  const [ready, setReady] = useState(false);

  useEffect(() => {
    loadServers()
      .catch(() => {})
      .finally(() => setReady(true));
    void loadSettings();
    void useTransfers.getState().load();
    initFileDrop(() => {});
    const offs = [
      on<Transfer>("xfer:update", (t) => useTransfers.getState().apply(t)),
      // 主窗口修改了服务器配置
      on("store:reloaded", () => void loadServers()),
    ];
    return () => offs.forEach((f) => f());
  }, [loadServers, loadSettings]);

  return (
    <div className="flex h-full flex-col">
      <FilesTitleBar server={servers.find((s) => s.id === serverId)} />
      <main className="@container relative min-h-0 flex-1 bg-bg">
        {info?.initError ? (
          <div className="p-4">
            <Callout tone="bad" title="无法读取数据">
              {info.initError}
            </Callout>
          </div>
        ) : (
          ready && (
            <ErrorBoundary>
              <Files serverId={serverId} />
            </ErrorBoundary>
          )
        )}
      </main>
      <Toaster />
      <ConfirmHost />
    </div>
  );
}

function MainApp() {
  const { page, info, go, loadServers, loadKeys, loadSettings, loadRoutes, setStatus } = useApp();
  const [palette, setPalette] = useState(false);
  // 这次启动时刚把旧版本的数据搬到了程序目录：提示一次
  const [migrationSeen, setMigrationSeen] = useState(false);

  useEffect(() => {
    void loadServers();
    void loadKeys();
    void loadSettings();
    void loadRoutes();
    void useAgent.getState().loadSessions();
    void useQuick.getState().load();
    initFileDrop(() => toast.info("文件可以拖到这两个地方", "拖进 AI 助手的对话里，交给 AI 处理（阅读、传到服务器上部署）；或者打开服务器的文件管理窗口（终端工具栏的「文件」），拖进去直接上传。"));
    const offs = [
      on<Record<string, ServerStatus>>("servers:status", (s) => setStatus(s ?? {})),
      // 连上服务器后识别出了系统名称等
      on("servers:changed", () => void loadServers()),
      // 临时线路变化；带着服务器 ID 表示它的连接都断开了，临时线路自动取消
      on<string>("routes:changed", (cleared) => {
        void loadRoutes();
        const name = cleared && useApp.getState().servers.find((s) => s.id === cleared)?.name;
        if (name) toast.info(`${name} 的临时线路已取消`, "通过它打开的连接都已断开，之后恢复使用服务器保存的线路。");
      }),
      on<AgentEvent>("agent:event", dispatchAgentEvent),
      on<Sample>("monitor:sample", (s) => useMonitor.getState().push(s)),
      on<{ count: number; score: number; titles: string[] }>("inspect:alert", (a) => toast.warn(`自动巡检发现 ${a.count} 个新的严重问题`, (a.titles ?? []).join("；"))),
      on<AutoSyncResult>("cert:autosync", (r) => {
        const title = `证书「${r.certName}」自动同步`;
        if (r.ok) toast.ok(title, r.message);
        else toast.warn(title, r.message);
      }),
    ];
    const onKey = (e: KeyboardEvent) => {
      if (useApp.getState().lock?.locked) return;
      if ((e.ctrlKey || e.metaKey) && !e.shiftKey && !e.altKey && e.key.toLowerCase() === "k") {
        e.preventDefault();
        setPalette((v) => !v);
      }
    };
    window.addEventListener("keydown", onKey);
    return () => {
      offs.forEach((f) => f());
      window.removeEventListener("keydown", onKey);
    };
  }, [loadServers, loadKeys, loadSettings, loadRoutes, setStatus]);

  return (
    <div className="flex h-full flex-col">
      <TitleBar onSearch={() => setPalette(true)} />
      <div className="flex min-h-0 flex-1">
        <Sidebar />
        <main className="@container relative flex min-w-0 flex-1 flex-col overflow-hidden bg-bg">
          {(info?.initError || (info?.secretErrors ?? 0) > 0) && (
            <div className="px-4 pt-3 @2xl:px-6">
              {info?.initError ? (
                <Callout tone="bad" title="数据目录无法使用，已切换到临时目录">
                  {info.initError}
                </Callout>
              ) : (
                <Callout tone="warn" title={`有 ${info?.secretErrors} 项加密数据无法解密`}>
                  敏感数据使用 Windows 账户加密，从其他电脑复制的数据需要通过「设置 → 数据与备份」导入加密备份来迁移。
                </Callout>
              )}
            </div>
          )}
          {info?.previousDataDir && !migrationSeen && (
            <div className="px-4 pt-3 @2xl:px-6">
              <Callout
                tone="warn"
                title="这里是一个全新的数据目录，之前的数据没有跟过来"
                action={
                  <Button size="xs" variant="soft" className="shrink-0" onClick={() => setMigrationSeen(true)}>
                    知道了
                  </Button>
                }
              >
                程序换了位置，但 data 文件夹还留在原处：<span className="selectable font-mono">{info.previousDataDir}</span>。想接着用之前的数据，请先关掉程序，把那个 data 文件夹复制到现在程序所在的目录里（覆盖刚生成的空 data 文件夹）。
              </Callout>
            </div>
          )}
          {info?.justMigrated && !migrationSeen && (
            <div className="px-4 pt-3 @2xl:px-6">
              <Callout
                tone="ok"
                title="数据已经搬到程序所在的目录"
                action={
                  <div className="flex shrink-0 flex-wrap justify-end gap-1.5">
                    <Button
                      size="xs"
                      variant="ghost"
                      onClick={() => {
                        go("settings", { tab: "data" });
                        setMigrationSeen(true);
                      }}
                    >
                      查看
                    </Button>
                    <Button size="xs" variant="soft" onClick={() => setMigrationSeen(true)}>
                      知道了
                    </Button>
                  </div>
                }
              >
                从这个版本起，数据默认保存在程序目录下的 data 文件夹。原来的数据已经从 <span className="selectable font-mono">{info.migratedFrom}</span> 复制过来，旧目录里的数据原样保留，确认一切正常后可以自己删除。以后移动程序时，请连同 data 文件夹一起移动。
              </Callout>
            </div>
          )}
          <div className="relative min-h-0 flex-1">
            <div className={page === "terminal" ? "h-full" : "hidden"}>
              <ErrorBoundary>
                <Terminals />
              </ErrorBoundary>
            </div>
            <div className={page === "assistant" ? "h-full" : "hidden"}>
              <ErrorBoundary>
                <Assistant />
              </ErrorBoundary>
            </div>
            <ErrorBoundary resetKey={page}>
              {page === "dashboard" && <Dashboard />}
              {page === "monitor" && <Monitor />}
              {page === "servers" && <Servers />}
              {page === "files" && <FilesLauncher />}
              {page === "keys" && <Keys />}
              {page === "scripts" && <Scripts />}
              {page === "certs" && <Certs />}
              {page === "services" && <Services />}
              {page === "inspect" && <Inspect />}
              {page === "brain" && <Brain />}
              {page === "settings" && <SettingsPage />}
            </ErrorBoundary>
          </div>
        </main>
      </div>
      <Toaster />
      <QuickHost />
      <RouteDialog />
      <ConfirmHost />
      <CommandPalette open={palette} onClose={() => setPalette(false)} />
    </div>
  );
}
