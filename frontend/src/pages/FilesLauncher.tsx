import { useEffect, useState } from "react";
import { AppWindow, FolderOpen, Search, Server as ServerIcon } from "lucide-react";
import { api, on } from "../lib/api";
import { useApp } from "../lib/store";
import { openFilesWindow } from "../lib/term";
import { Badge, Button, Card, Empty, Input, PageHeader, StatusDot } from "../components/ui";
import { ServerAvatar } from "../components/widgets";

/** 主窗口里的「文件管理」：选择服务器，在独立窗口里打开它的文件管理 */
export default function FilesLauncher() {
  const { servers, status, go } = useApp();
  const [open, setOpen] = useState<string[]>([]);
  const [q, setQ] = useState("");

  useEffect(() => {
    api
      .FilesWindows()
      .then((ids: string[]) => setOpen(ids ?? []))
      .catch(() => {});
    return on<string[]>("fileswin:changed", (ids) => setOpen(ids ?? []));
  }, []);

  const kw = q.trim().toLowerCase();
  const list = servers.filter((s) => !kw || [s.name, s.host, s.group, ...(s.tags ?? [])].join(" ").toLowerCase().includes(kw));

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title="文件管理"
        icon={FolderOpen}
        description="每台服务器的文件管理在独立窗口里打开：上传、下载、在线编辑，文件和文件夹可以直接拖进去"
        actions={servers.length > 6 ? <Input icon={Search} placeholder="搜索服务器" value={q} onChange={(e) => setQ(e.target.value)} className="w-[200px]" /> : undefined}
      />
      <div className="min-h-0 flex-1 overflow-y-auto px-4 pb-6 @2xl:px-7">
        {servers.length === 0 ? (
          <Card>
            <Empty
              icon={ServerIcon}
              title="还没有服务器"
              description="添加服务器后，就可以像用资源管理器一样管理服务器上的文件。"
              action={
                <Button variant="primary" onClick={() => go("servers", { add: Date.now() })}>
                  添加服务器
                </Button>
              }
            />
          </Card>
        ) : list.length === 0 ? (
          <Empty icon={Search} title="没有匹配的服务器" />
        ) : (
          <div className="grid grid-cols-[repeat(auto-fill,minmax(min(280px,100%),1fr))] gap-3">
            {list.map((s) => {
              const opened = open.includes(s.id);
              return (
                <button
                  key={s.id}
                  type="button"
                  onClick={() => void openFilesWindow(s.id)}
                  title={opened ? "窗口已经打开，点击切换到那个窗口" : "在独立窗口里打开这台服务器的文件管理"}
                  className="card flex items-center gap-3 p-3.5 text-left transition-colors hover:border-accent/40"
                >
                  <ServerAvatar name={s.name} size={36} />
                  <div className="min-w-0 flex-1">
                    <div className="flex items-center gap-2">
                      <span className="truncate text-[13.5px] font-medium">{s.name}</span>
                      <StatusDot status={status[s.id]?.status} />
                    </div>
                    <div className="truncate font-mono text-[11.5px] text-muted">
                      {s.username}@{s.host}:{s.port}
                    </div>
                  </div>
                  {opened ? (
                    <Badge tone="accent" icon={AppWindow} className="shrink-0">
                      已打开
                    </Badge>
                  ) : (
                    <FolderOpen size={16} className="shrink-0 text-muted" />
                  )}
                </button>
              );
            })}
          </div>
        )}
      </div>
    </div>
  );
}
