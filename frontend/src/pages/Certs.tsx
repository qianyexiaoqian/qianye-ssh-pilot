import { useEffect, useMemo, useRef, useState } from "react";
import {
  CircleCheck,
  CircleDashed,
  CircleX,
  Clock,
  CloudUpload,
  Download,
  Ellipsis,
  FolderOpen,
  FolderPlus,
  Globe,
  History,
  LoaderCircle,
  Pencil,
  Plus,
  Radar,
  RefreshCw,
  Repeat,
  Rocket,
  Search,
  Server as ServerIcon,
  ShieldCheck,
  Trash2,
  TriangleAlert,
} from "lucide-react";
import { api, on, type CertParsed, type CertView, type DeployResult, type DeployStep, type DiscoveredView, type TargetView, type TLSInfo } from "../lib/api";
import { certTone, cn, formatDate, timeAgo } from "../lib/format";
import { confirm, toast, useApp } from "../lib/store";
import { Badge, Button, Callout, Card, CopyButton, Empty, Field, IconButton, Input, Menu, Meter, Modal, Segmented, Select, Spinner, Switch, TagInput, Textarea } from "../components/ui";
import { KV, ServerPicker, useMask } from "../components/widgets";
import { CertGroupModal, CertList } from "../components/CertList";

function DaysBadge({ days }: { days: number }) {
  const tone = certTone(days);
  const Icon = tone === "ok" ? CircleCheck : tone === "warn" ? TriangleAlert : CircleX;
  return (
    <Badge tone={tone} icon={Icon}>
      {days < 0 ? `已过期 ${-days} 天` : `剩 ${days} 天`}
    </Badge>
  );
}

export default function Certs() {
  const { params } = useApp();
  const [certs, setCerts] = useState<CertView[]>([]);
  // 分组的先后顺序（只包含里面有证书的分组）
  const [groups, setGroups] = useState<string[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [sel, setSel] = useState<string | null>(null);
  const [q, setQ] = useState("");
  const [importMode, setImportMode] = useState<{ replace?: CertView } | null>(null);
  // 正在新建（空字符串）或修改的分组；null 表示对话框没有打开
  const [groupEdit, setGroupEdit] = useState<string | null>(null);
  // 从别处跳转过来要看的证书，或者刚刚放进某个分组的证书：它所在的分组自动展开
  const [reveal, setReveal] = useState<string | null>(null);

  const load = async () => {
    const [list, names] = await Promise.all([api.ListCerts(), api.ListCertGroups()]);
    setCerts(list ?? []);
    setGroups(names ?? []);
    setLoaded(true);
    setSel((cur) => (cur && (list ?? []).some((c) => c.id === cur) ? cur : (list ?? [])[0]?.id ?? null));
    return list ?? [];
  };
  useEffect(() => {
    void load();
    // AI 助手导入、更新或改名了证书：这一页开着的话跟着刷新
    return on("certs:changed", () => void load());
  }, []);
  useEffect(() => {
    if (params.certId) {
      setSel(params.certId);
      setReveal(params.certId);
    }
    if (params.import) setImportMode({});
  }, [params]);

  const cur = certs.find((c) => c.id === sel) ?? null;

  return (
    <div className="flex h-full flex-col overflow-y-auto @3xl:flex-row @3xl:overflow-hidden">
      <div className="flex max-h-[min(240px,45vh)] w-full shrink-0 flex-col border-b border-line bg-bg2/40 @3xl:max-h-none @3xl:w-[330px] @3xl:border-r @3xl:border-b-0">
        <div className="space-y-2.5 p-4 pb-3">
          <div className="flex items-center justify-between">
            <h1 className="text-[16px] font-semibold">证书中心</h1>
            <div className="flex items-center gap-1">
              {certs.length > 0 && <IconButton icon={FolderPlus} tip="新建分组" tipPos="bottom" onClick={() => setGroupEdit("")} data-new-group />}
              <Button size="sm" variant="primary" icon={Plus} onClick={() => setImportMode({})}>
                导入证书
              </Button>
            </div>
          </div>
          <Input icon={Search} value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索名称、域名、分组" />
        </div>
        <div className="min-h-0 flex-1 space-y-1 overflow-y-auto px-2 pb-4" data-cert-list>
          {loaded && certs.length === 0 && <div className="px-3 py-8 text-center text-[12.5px] text-muted">还没有证书</div>}
          <CertList
            certs={certs}
            groups={groups}
            sel={sel}
            q={q}
            reveal={reveal}
            onSelect={setSel}
            onCerts={setCerts}
            onGroups={setGroups}
            onEditGroup={setGroupEdit}
            reload={load}
          />
        </div>
      </div>
      <div className="@container min-w-0 shrink-0 @3xl:min-h-0 @3xl:flex-1 @3xl:shrink @3xl:overflow-y-auto">
        {cur ? (
          <CertDetail
            cert={cur}
            groups={groups}
            reload={load}
            onReplace={() => setImportMode({ replace: cur })}
            onGrouped={() => setReveal(cur.id)}
          />
        ) : (
          loaded && (
            <div className="mx-auto max-w-[640px] pt-16">
              <Empty
                icon={ShieldCheck}
                title="集中管理 TLS 证书"
                description="导入证书（或从 certbot 所在服务器拉取），配置部署目标后即可一键同步到多台服务器：自动备份、校验、检测配置、重载服务，失败自动回滚。"
                action={
                  <Button variant="primary" icon={Plus} onClick={() => setImportMode({})}>
                    导入第一张证书
                  </Button>
                }
              />
            </div>
          )
        )}
      </div>
      <CertGroupModal
        open={groupEdit !== null}
        group={groupEdit ?? ""}
        certs={certs}
        groups={groups}
        // 新建分组时先勾上正在看的那张证书；它已经在别的分组里就不勾，免得不小心把它挪走
        preselect={cur && !cur.group ? cur.id : null}
        onClose={() => setGroupEdit(null)}
        onSaved={async (name) => {
          const list = await load();
          // 新建或修改之后把这个分组展开，看得到里面的证书
          const first = name ? list.find((c) => c.group === name) : undefined;
          if (first) setReveal(first.id);
        }}
      />
      <ImportCertModal
        open={!!importMode}
        replace={importMode?.replace}
        groups={groups}
        onClose={() => setImportMode(null)}
        onSaved={async (c, replaced) => {
          await load();
          setSel(c.id);
          setReveal(c.id);
          if (replaced && c.targets.length > 0) {
            const go = await confirm({ title: "证书已更新，立即同步？", message: `将新证书部署到 ${c.targets.length} 个部署目标（自动备份并在失败时回滚）。`, confirmText: "立即同步" });
            if (go) window.dispatchEvent(new CustomEvent("cert-deploy", { detail: { certId: c.id } }));
          }
        }}
      />
    </div>
  );
}

function CertDetail({ cert, groups, reload, onReplace, onGrouped }: { cert: CertView; groups: string[]; reload: () => Promise<CertView[]>; onReplace: () => void; onGrouped: () => void }) {
  const [target, setTarget] = useState<TargetView | null | undefined>(undefined);
  const [deploy, setDeploy] = useState<{ targetIds: string[] } | null>(null);
  const [discover, setDiscover] = useState(false);
  const [refreshing, setRefreshing] = useState(false);
  const [syncing, setSyncing] = useState(false);
  const [editing, setEditing] = useState<{ name: string; notes: string; group: string } | null>(null);
  const edit = () => setEditing({ name: cert.name, notes: cert.notes, group: cert.group });
  const [savingMeta, setSavingMeta] = useState(false);
  const mask = useMask();
  const saveMeta = async () => {
    if (!editing) return;
    setSavingMeta(true);
    try {
      await api.UpdateCertMeta(cert.id, editing.name, editing.notes);
      const moved = editing.group.trim() !== cert.group;
      if (moved) await api.SetCertGroup([cert.id], editing.group);
      setEditing(null);
      await reload();
      if (moved) onGrouped();
    } catch (e) {
      toast.error("保存失败", e);
    } finally {
      setSavingMeta(false);
    }
  };

  useEffect(() => {
    const h = (e: Event) => {
      const d = (e as CustomEvent).detail;
      if (d?.certId === cert.id) setDeploy({ targetIds: [] });
    };
    window.addEventListener("cert-deploy", h);
    return () => window.removeEventListener("cert-deploy", h);
  }, [cert.id]);

  const total = Math.max(1, Math.round((cert.notAfter - cert.notBefore) / 86400000));
  const tone = certTone(cert.days);
  const refresh = async () => {
    setRefreshing(true);
    try {
      const r = await api.RefreshCert(cert.id);
      await reload();
      if (r.changed) {
        toast.ok("证书已更新", r.message);
        if (cert.targets.length && (await confirm({ title: "检测到新证书，立即同步？", message: r.message, confirmText: "立即同步" }))) setDeploy({ targetIds: [] });
      } else toast.info(r.message);
    } catch (e) {
      toast.error("刷新失败", e);
    } finally {
      setRefreshing(false);
    }
  };

  // 检查来源服务器：有新证书就拉取，并同步到还没更新的部署目标
  const syncNow = async () => {
    setSyncing(true);
    try {
      const r = await api.SyncCertNow(cert.id);
      await reload();
      if (r.ok) toast.ok(r.changed ? "证书已更新并同步" : "检查完成", r.message);
      else toast.warn("检查完成，有需要处理的问题", r.message);
    } catch (e) {
      toast.error("检查失败", e);
    } finally {
      setSyncing(false);
    }
  };
  const toggleAuto = async (on: boolean) => {
    if (
      on &&
      !(await confirm({
        title: "开启自动同步？",
        message: `每 6 小时检查一次来源服务器「${cert.sourceServerName}」。证书续期后会自动拉取，并同步到还没有更新的部署目标：检测配置、重载服务，失败自动回滚。\n\n需要保持 QianYe SSH Pilot 运行。开启后会立即检查一次。`,
        confirmText: "开启",
      }))
    )
      return;
    try {
      await api.SetCertAutoSync(cert.id, on);
      await reload();
      if (on) await syncNow();
      else toast.info("已关闭自动同步");
    } catch (e) {
      toast.error("操作失败", e);
    }
  };

  return (
    <div className="mx-auto max-w-[1100px] px-4 py-5 @2xl:px-7 @2xl:py-6">
      <div className="flex flex-wrap items-start gap-x-4 gap-y-3">
        <div className={cn("flex h-12 w-12 shrink-0 items-center justify-center rounded-2xl", tone === "ok" ? "bg-ok/12 text-ok" : tone === "warn" ? "bg-warn/12 text-warn" : "bg-bad/12 text-bad")}>
          <ShieldCheck size={22} />
        </div>
        <div className="min-w-0 flex-1">
          <div className="group/name flex items-center gap-2">
            <h2 className="truncate text-[19px] font-semibold" title="双击修改名称" onDoubleClick={edit}>
              {cert.name}
            </h2>
            <IconButton icon={Pencil} size="sm" tip="修改名称、分组" className="opacity-60 group-hover/name:opacity-100" onClick={edit} data-cert-rename />
            <DaysBadge days={cert.days} />
          </div>
          <div className="mt-1.5 flex flex-wrap gap-1.5">
            {cert.domains.map((d) => (
              <Badge key={d} icon={Globe}>
                {d}
              </Badge>
            ))}
          </div>
        </div>
        <div className="flex flex-wrap gap-2">
          {cert.source && (
            <Button icon={RefreshCw} loading={refreshing} onClick={refresh}>
              从源刷新
            </Button>
          )}
          <Button icon={CloudUpload} onClick={onReplace}>
            更新证书
          </Button>
          <Button variant="primary" icon={Rocket} disabled={cert.targets.length === 0} onClick={() => setDeploy({ targetIds: [] })}>
            一键同步
          </Button>
          <Menu
            trigger={<IconButton icon={Ellipsis} />}
            items={[
              { label: "修改名称、分组和备注", icon: Pencil, onClick: edit },
              {
                label: "导出证书文件",
                icon: Download,
                onClick: async () => {
                  try {
                    const p = await api.ExportCert(cert.id);
                    if (p) toast.ok("已导出", p);
                  } catch (e) {
                    toast.error("导出失败", e);
                  }
                },
              },
              { label: "", onClick: () => {}, divider: true },
              {
                label: "删除证书",
                icon: Trash2,
                danger: true,
                onClick: async () => {
                  if (!(await confirm({ title: `删除证书「${cert.name}」？`, message: "将同时删除它的部署目标配置，服务器上的证书文件不受影响。", danger: true, confirmText: "删除" }))) return;
                  await api.DeleteCert(cert.id);
                  await reload();
                },
              },
            ]}
          />
        </div>
      </div>

      <div className="mt-6 grid grid-cols-1 gap-4 @2xl:grid-cols-3">
        <Card className="p-4">
          <div className="text-[12px] text-muted">有效期</div>
          <div className="mt-1 text-[15px] font-semibold">
            {formatDate(cert.notBefore)} → {formatDate(cert.notAfter)}
          </div>
          <Meter className="mt-3" value={(Math.max(cert.days, 0) / total) * 100} tone={tone} title={`共 ${total} 天`} />
          <div className="mt-1.5 text-[11.5px] text-muted">
            总有效期 {total} 天 · 更新于 {timeAgo(cert.updatedAt)}
          </div>
        </Card>
        <Card className="p-4">
          <KV k="签发者" v={cert.issuer} />
          <KV k="密钥类型" v={cert.keyType} />
          <KV k="证书链" v={cert.chainCount > 0 ? `包含 ${cert.chainCount} 张中间证书` : <span className="text-warn">无中间证书</span>} />
        </Card>
        <Card className="p-4">
          <div className="flex items-center justify-between text-[12px] text-muted">
            SHA-256 指纹 <CopyButton text={cert.fingerprint} />
          </div>
          <div className="mt-1 font-mono text-[11px] leading-relaxed break-all text-fg2">{cert.fingerprint}</div>
          {cert.source && (
            <div className="mt-2 truncate text-[11.5px] text-muted" title={cert.source.certPath}>
              来源：{cert.sourceServerName} · {cert.source.certPath}
            </div>
          )}
        </Card>
      </div>
      {cert.source ? (
        <Card className={cn("mt-4 flex flex-wrap items-center gap-x-3.5 gap-y-2 px-4 py-3", cert.autoSync && "border-accent/30")}>
          <div className={cn("flex h-9 w-9 shrink-0 items-center justify-center rounded-xl", cert.autoSync ? "bg-accent/12 text-accent" : "bg-panel3 text-muted")}>
            <Repeat size={17} />
          </div>
          <div className="min-w-[min(200px,100%)] flex-1">
            <div className="flex items-center gap-2 text-[13px] font-medium">
              自动同步
              {cert.autoSync ? <Badge tone="accent">已开启 · 每 6 小时检查</Badge> : <Badge>未开启</Badge>}
            </div>
            <div className="mt-0.5 truncate text-[12px] text-muted" title={cert.autoSyncMessage}>
              {cert.autoSyncAt ? (
                <span className="inline-flex items-center gap-1">
                  {cert.autoSyncOk ? <CircleCheck size={12} className="shrink-0 text-ok" /> : <TriangleAlert size={12} className="shrink-0 text-warn" />}
                  {timeAgo(cert.autoSyncAt)}检查：{cert.autoSyncMessage}
                </span>
              ) : (
                `「${cert.sourceServerName}」上的证书续期后，自动拉取并同步到下面的部署目标，不用再手动操作`
              )}
            </div>
          </div>
          <Button size="sm" icon={RefreshCw} loading={syncing} onClick={syncNow} title="立即检查来源服务器，有新证书就拉取并同步">
            立即检查并同步
          </Button>
          <Switch checked={cert.autoSync} disabled={syncing} onChange={toggleAuto} />
        </Card>
      ) : (
        <div className="mt-3 flex items-center gap-2 px-1 text-[12px] text-muted">
          <Repeat size={13} className="shrink-0" />
          想让证书续期后自动同步？点击「更新证书」并选择「从服务器拉取」（例如运行 certbot 的服务器），记录来源后即可开启自动同步。
        </div>
      )}
      {cert.notes && <div className="mt-3 rounded-xl border border-line bg-panel px-4 py-2.5 text-[12.5px] whitespace-pre-wrap text-fg2">{cert.notes}</div>}

      {/* 部署目标 */}
      <div className="mt-7 mb-3 flex flex-wrap items-center justify-between gap-x-4 gap-y-2">
        <div className="min-w-0">
          <h3 className="text-[14px] font-semibold">部署目标</h3>
          <p className="text-[12px] text-muted">证书会被写入这些服务器上的指定路径，并在部署后执行配置检测与服务重载</p>
        </div>
        <div className="flex flex-wrap gap-2">
          <Button icon={Radar} onClick={() => setDiscover(true)}>
            扫描服务器
          </Button>
          <Button variant="soft" icon={Plus} onClick={() => setTarget(null)}>
            添加部署目标
          </Button>
        </div>
      </div>
      {cert.targets.length === 0 ? (
        <Card>
          <Empty icon={ServerIcon} title="还没有部署目标" description="可以手动添加（内置 Nginx / Apache / HAProxy 预设），或扫描服务器自动发现 Web 服务正在使用的证书路径。" className="py-10" />
        </Card>
      ) : (
        <div className="space-y-2.5">
          {cert.targets.map((t) => {
            const state = t.synced ? "synced" : t.lastStatus === "failed" ? "failed" : t.lastFingerprint ? "outdated" : "pending";
            const meta = {
              synced: { label: "已同步", tone: "ok" as const, Icon: CircleCheck },
              failed: { label: "上次失败", tone: "bad" as const, Icon: CircleX },
              outdated: { label: "版本落后", tone: "warn" as const, Icon: TriangleAlert },
              pending: { label: "未部署", tone: "neutral" as const, Icon: CircleDashed },
            }[state];
            return (
              <Card key={t.id} className="p-4">
                <div className="flex items-start gap-4">
                  <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-panel3 text-fg2">
                    <ServerIcon size={16} />
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-2">
                      <span className="text-[13.5px] font-semibold">{t.serverName || "（服务器已删除）"}</span>
                      <span className="font-mono text-[11.5px] text-muted">{mask(t.serverHost)}</span>
                      <Badge tone="accent">{presetLabel[t.preset as Preset] ?? "自定义"}</Badge>
                      <Badge tone={meta.tone} icon={meta.Icon}>
                        {meta.label}
                      </Badge>
                    </div>
                    <div className="mt-2 grid grid-cols-1 gap-x-6 @xl:grid-cols-2 gap-y-1 font-mono text-[11.5px] text-fg2">
                      <span className="truncate">
                        <span className="text-muted">证书 </span>
                        {t.certPath}
                      </span>
                      {t.keyPath && (
                        <span className="truncate">
                          <span className="text-muted">私钥 </span>
                          {t.keyPath}
                        </span>
                      )}
                      {t.testCommand && (
                        <span className="truncate">
                          <span className="text-muted">检测 </span>
                          {t.testCommand}
                        </span>
                      )}
                      {t.services.length > 0 && t.serviceAction !== "none" && (
                        <span className="truncate">
                          <span className="text-muted">{t.serviceAction} </span>
                          {t.services.join(", ")}
                        </span>
                      )}
                    </div>
                    {t.lastDeployAt > 0 && (
                      <div className={cn("mt-2 flex items-center gap-1.5 text-[11.5px]", t.lastStatus === "failed" ? "text-bad" : "text-muted")}>
                        <Clock size={11} />
                        {timeAgo(t.lastDeployAt)} · {t.lastMessage}
                      </div>
                    )}
                  </div>
                  <div className="flex shrink-0 gap-1">
                    <Button size="sm" variant={t.synced ? "secondary" : "soft"} icon={Rocket} onClick={() => setDeploy({ targetIds: [t.id] })}>
                      部署
                    </Button>
                    <IconButton icon={Pencil} tip="编辑" onClick={() => setTarget(t)} />
                    <IconButton
                      icon={Trash2}
                      tone="danger"
                      tip="删除"
                      onClick={async () => {
                        if (!(await confirm({ title: "删除该部署目标？", message: "只删除配置，不会修改服务器上的文件。", danger: true }))) return;
                        await api.DeleteTarget(t.id);
                        await reload();
                      }}
                    />
                  </div>
                </div>
              </Card>
            );
          })}
        </div>
      )}

      <div className="mt-7 grid grid-cols-1 gap-4 @2xl:grid-cols-2">
        <TLSCheck cert={cert} />
        <Card className="p-4">
          <div className="mb-2 flex items-center gap-2 text-[13px] font-semibold">
            <History size={15} className="text-accent" /> 历史版本
          </div>
          {cert.history.length === 0 ? (
            <div className="text-[12.5px] text-muted">替换（续期）证书后，旧版本记录会显示在这里</div>
          ) : (
            <div className="space-y-1.5">
              {cert.history.map((h) => (
                <div key={h.fingerprint + h.replacedAt} className="flex items-center justify-between gap-3 text-[12px]">
                  <span className="truncate font-mono text-muted">{h.fingerprint.slice(0, 29)}…</span>
                  <span className="shrink-0 text-fg2">
                    到期 {formatDate(h.notAfter)} · {timeAgo(h.replacedAt)}替换
                  </span>
                </div>
              ))}
            </div>
          )}
        </Card>
      </div>

      <TargetModal open={target !== undefined} cert={cert} target={target ?? null} onClose={() => setTarget(undefined)} onSaved={reload} />
      <DeployModal open={!!deploy} cert={cert} targetIds={deploy?.targetIds ?? []} onClose={() => setDeploy(null)} onDone={reload} />
      <DiscoverModal open={discover} cert={cert} onClose={() => setDiscover(false)} onChanged={reload} />
      <Modal
        open={editing !== null}
        onClose={() => setEditing(null)}
        title="修改名称、分组和备注"
        description="名称和分组只影响证书在证书中心里怎样显示，不影响证书本身和部署。"
        icon={Pencil}
        size="sm"
        footer={
          <>
            <Button variant="ghost" onClick={() => setEditing(null)}>
              取消
            </Button>
            <Button variant="primary" loading={savingMeta} disabled={!editing?.name.trim()} onClick={saveMeta}>
              保存
            </Button>
          </>
        }
      >
        <div className="space-y-3">
          <Field label="名称" hint={cert.domains.length ? `域名：${cert.domains.slice(0, 3).join("、")}${cert.domains.length > 3 ? " 等" : ""}` : undefined}>
            <Input
              autoFocus
              value={editing?.name ?? ""}
              onChange={(e) => setEditing((m) => m && { ...m, name: e.target.value })}
              onKeyDown={(e) => {
                if (e.key === "Enter" && editing?.name.trim()) void saveMeta();
              }}
              placeholder="例如：主站证书"
              maxLength={80}
              data-cert-name
            />
          </Field>
          <Field label="分组" hint="可以选已有的分组，也可以直接输入一个新的；留空表示不分组">
            <Input value={editing?.group ?? ""} onChange={(e) => setEditing((m) => m && { ...m, group: e.target.value })} placeholder="不分组" list="cert-groups" maxLength={30} data-cert-group-input />
            <datalist id="cert-groups">
              {groups.map((g) => (
                <option key={g} value={g} />
              ))}
            </datalist>
          </Field>
          <Field label="备注">
            <Textarea value={editing?.notes ?? ""} onChange={(e) => setEditing((m) => m && { ...m, notes: e.target.value })} placeholder="例如：续期方式、负责人、用途" />
          </Field>
        </div>
      </Modal>
    </div>
  );
}

// ---- 导入 / 更新 ----

function ParsedPreview({ p }: { p: CertParsed }) {
  return (
    <div className="rounded-xl border border-line2 bg-bg2/60 p-3.5">
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[13px] font-semibold">{p.domains[0]}</span>
        {p.domains.length > 1 && <span className="text-[12px] text-muted">等 {p.domains.length} 个域名</span>}
        <DaysBadge days={Math.floor((p.notAfter - Date.now()) / 86400000)} />
        {p.hasKey ? (
          p.keyMatches ? (
            <Badge tone="ok" icon={CircleCheck}>
              私钥匹配
            </Badge>
          ) : (
            <Badge tone="bad" icon={CircleX}>
              私钥不匹配
            </Badge>
          )
        ) : (
          <Badge tone="warn" icon={TriangleAlert}>
            未提供私钥
          </Badge>
        )}
      </div>
      <div className="mt-2 grid grid-cols-1 gap-x-6 @xl:grid-cols-2 text-[12px]">
        <KV k="签发者" v={p.issuer} />
        <KV k="有效期至" v={formatDate(p.notAfter, true)} />
        <KV k="密钥类型" v={p.keyType} />
        <KV k="中间证书" v={`${p.chainCount} 张`} />
      </div>
      {(p.warnings ?? []).map((w) => (
        <div key={w} className="mt-1 flex items-center gap-1.5 text-[12px] text-warn">
          <TriangleAlert size={12} /> {w}
        </div>
      ))}
    </div>
  );
}

function ImportCertModal({ open, replace, groups, onClose, onSaved }: { open: boolean; replace?: CertView; groups: string[]; onClose: () => void; onSaved: (c: CertView, replaced: boolean) => void }) {
  const [mode, setMode] = useState<"paste" | "pull">("paste");
  const [name, setName] = useState("");
  const [group, setGroup] = useState("");
  const [certPem, setCertPem] = useState("");
  const [keyPem, setKeyPem] = useState("");
  const [preview, setPreview] = useState<CertParsed | null>(null);
  const [previewErr, setPreviewErr] = useState("");
  const [server, setServer] = useState<string[]>([]);
  const [paths, setPaths] = useState({ cert: "", key: "", chain: "" });
  const [domain, setDomain] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) return;
    setMode(replace?.source ? "pull" : "paste");
    setName("");
    setGroup("");
    setCertPem("");
    setKeyPem("");
    setPreview(null);
    setPreviewErr("");
    setServer(replace?.source ? [replace.source.serverId] : []);
    setPaths(replace?.source ? { cert: replace.source.certPath, key: replace.source.keyPath, chain: replace.source.chainPath } : { cert: "", key: "", chain: "" });
    setDomain(replace?.domains?.[0]?.replace("*.", "") ?? "");
  }, [open, replace]);

  useEffect(() => {
    if (!certPem.trim()) {
      setPreview(null);
      setPreviewErr("");
      return;
    }
    const t = setTimeout(() => {
      api
        .PreviewCert(certPem, keyPem, "")
        .then((p) => {
          setPreview(p);
          setPreviewErr("");
        })
        .catch((e) => {
          setPreview(null);
          setPreviewErr(String(e));
        });
    }, 300);
    return () => clearTimeout(t);
  }, [certPem, keyPem]);

  const pick = async (which: "cert" | "key") => {
    const f = await api.PickTextFile(which === "cert" ? "选择证书文件（fullchain.pem / .crt）" : "选择私钥文件（privkey.pem / .key）");
    if (!f) return;
    if (which === "cert") setCertPem(f.content);
    else setKeyPem(f.content);
  };
  const submit = async () => {
    setBusy(true);
    try {
      const c =
        mode === "paste"
          ? await api.ImportCert({ id: replace?.id ?? "", name, certPem, keyPem, chainPem: "", notes: "", group })
          : await api.PullCert({ certId: replace?.id ?? "", serverId: server[0], certPath: paths.cert, keyPath: paths.key, chainPath: paths.chain, name, group });
      toast.ok(replace ? "证书已更新" : "证书已导入", `${c.domains.join(", ")} · 有效期至 ${formatDate(c.notAfter)}`);
      onClose();
      onSaved(c, !!replace);
    } catch (e) {
      toast.error(replace ? "更新失败" : "导入失败", e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={replace ? `更新证书 · ${replace.name}` : "导入证书"}
      description={replace ? "替换为续期后的新证书，部署目标保持不变；若未提供私钥将尝试沿用原私钥" : "支持粘贴 PEM、选择文件，或直接从服务器（例如运行 certbot 的机器）拉取"}
      icon={ShieldCheck}
      size="lg"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button variant="primary" loading={busy} onClick={submit} disabled={mode === "paste" ? !certPem.trim() || (!!preview && preview.hasKey && !preview.keyMatches) : !server[0] || !paths.cert}>
            {replace ? "更新证书" : "导入"}
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Segmented
          value={mode}
          onChange={setMode}
          options={[
            { value: "paste", label: "粘贴 / 选择文件", icon: FolderOpen },
            { value: "pull", label: "从服务器拉取", icon: Download },
          ]}
        />
        {!replace && (
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1.6fr_1fr]">
            <Field label="名称" hint="留空时使用证书主域名">
              <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 example.com 通配符证书" />
            </Field>
            <Field label="分组" hint="可以不填">
              <Input value={group} onChange={(e) => setGroup(e.target.value)} placeholder="不分组" list="cert-groups-import" maxLength={30} />
              <datalist id="cert-groups-import">
                {groups.map((g) => (
                  <option key={g} value={g} />
                ))}
              </datalist>
            </Field>
          </div>
        )}
        {mode === "paste" ? (
          <>
            <Field
              label="证书（建议 fullchain，包含中间证书）"
              action={
                <Button size="xs" variant="ghost" icon={FolderOpen} onClick={() => pick("cert")}>
                  选择文件
                </Button>
              }
            >
              <Textarea mono value={certPem} onChange={(e) => setCertPem(e.target.value)} placeholder="-----BEGIN CERTIFICATE-----" className="min-h-[130px]" />
            </Field>
            <Field
              label="私钥"
              action={
                <Button size="xs" variant="ghost" icon={FolderOpen} onClick={() => pick("key")}>
                  选择文件
                </Button>
              }
              hint="也可以把证书和私钥一起粘贴到上面的输入框"
            >
              <Textarea mono value={keyPem} onChange={(e) => setKeyPem(e.target.value)} placeholder="-----BEGIN PRIVATE KEY-----" className="min-h-[100px]" />
            </Field>
            {previewErr && <Callout tone="bad">{previewErr}</Callout>}
            {preview && <ParsedPreview p={preview} />}
          </>
        ) : (
          <>
            <Field label="来源服务器">
              <ServerPicker single value={server} onChange={setServer} maxHeight={180} />
            </Field>
            <div className="flex items-end gap-2">
              <Field label="Let's Encrypt 快捷填写" className="flex-1">
                <Input value={domain} onChange={(e) => setDomain(e.target.value)} placeholder="域名，例如 example.com" />
              </Field>
              <Button onClick={() => setPaths({ cert: `/etc/letsencrypt/live/${domain}/fullchain.pem`, key: `/etc/letsencrypt/live/${domain}/privkey.pem`, chain: "" })} disabled={!domain}>
                填入 certbot 路径
              </Button>
            </div>
            <Field label="证书路径" required>
              <Input mono value={paths.cert} onChange={(e) => setPaths({ ...paths, cert: e.target.value })} placeholder="/etc/letsencrypt/live/example.com/fullchain.pem" />
            </Field>
            <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
              <Field label="私钥路径">
                <Input mono value={paths.key} onChange={(e) => setPaths({ ...paths, key: e.target.value })} placeholder="/etc/letsencrypt/live/example.com/privkey.pem" />
              </Field>
              <Field label="证书链路径（可选）">
                <Input mono value={paths.chain} onChange={(e) => setPaths({ ...paths, chain: e.target.value })} placeholder="证书文件不含中间证书时填写" />
              </Field>
            </div>
            <Callout tone="info">会记录来源，之后在证书详情中点击「从源刷新」即可获取续期后的新证书并一键同步到其他服务器。</Callout>
          </>
        )}
      </div>
    </Modal>
  );
}

// ---- 部署目标 ----

type Preset = "nginx" | "apache" | "haproxy" | "xboard" | "custom";

/** 「扫描服务器」找到的证书是从哪里发现的 */
const sourceLabel: Record<string, string> = { nginx: "Nginx", apache: "Apache", haproxy: "HAProxy", xboard: "XBoard 节点", "1panel": "1Panel", letsencrypt: "Let's Encrypt" };

const presetLabel: Record<Preset, string> = { nginx: "Nginx", apache: "Apache", haproxy: "HAProxy", xboard: "XBoard 节点", custom: "自定义" };
const presetNote: Record<Preset, string> = { nginx: "reload nginx", apache: "reload apache2", haproxy: "合并 PEM", xboard: "restart xboard-node", custom: "手动配置" };

// 各个预设的常用路径和部署后的操作。AI 添加部署目标时用的是同一组预设（agent_certs.go 的 targetPresets），两边要保持一致。
function presetValues(preset: Preset, domain: string) {
  const d = (domain || "site").replace("*.", "wildcard.");
  switch (preset) {
    case "nginx":
      return { certPath: `/etc/nginx/ssl/${d}.crt`, keyPath: `/etc/nginx/ssl/${d}.key`, chainPath: "", certFormat: "fullchain", testCommand: "nginx -t", services: ["nginx"], serviceAction: "reload", verifyPort: 443 };
    case "apache":
      return { certPath: `/etc/ssl/certs/${d}.crt`, keyPath: `/etc/ssl/private/${d}.key`, chainPath: "", certFormat: "fullchain", testCommand: "apachectl configtest", services: ["apache2"], serviceAction: "reload", verifyPort: 443 };
    case "haproxy":
      return { certPath: `/etc/haproxy/certs/${d}.pem`, keyPath: "", chainPath: "", certFormat: "combined", testCommand: "haproxy -c -f /etc/haproxy/haproxy.cfg", services: ["haproxy"], serviceAction: "reload", verifyPort: 443 };
    case "xboard":
      // xboard-node 没有 reload，换证书后要重启；证书放在哪里由节点 config.yml 里的 cert_file / key_file 决定，这里给的只是常见的位置。
      // 节点监听的端口各不相同，默认不做线上验证
      return { certPath: "/etc/xboard-node/fullchain.pem", keyPath: "/etc/xboard-node/privkey.pem", chainPath: "", certFormat: "fullchain", testCommand: "", services: ["xboard-node"], serviceAction: "restart", verifyPort: 0 };
    default:
      return { certPath: "", keyPath: "", chainPath: "", certFormat: "fullchain", testCommand: "", services: [] as string[], serviceAction: "none" };
  }
}

function TargetModal({ open, cert, target, onClose, onSaved }: { open: boolean; cert: CertView; target: TargetView | null; onClose: () => void; onSaved: () => void }) {
  const domain = cert.domains[0] ?? "";
  const init = () => ({
    id: "",
    certId: cert.id,
    serverId: "",
    preset: "nginx" as Preset,
    certMode: "644",
    keyMode: "600",
    owner: "",
    backup: true,
    postCommand: "",
    verifyPort: 443,
    verifyDomain: "",
    ...presetValues("nginx", domain),
  });
  const [t, setT] = useState(init);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (!open) return;
    if (target) setT({ ...init(), ...target, preset: (target.preset || "custom") as Preset, services: target.services ?? [] });
    else setT(init());
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, target]);
  const set = (patch: Partial<ReturnType<typeof init>>) => setT((x) => ({ ...x, ...patch }));
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={target ? "编辑部署目标" : "添加部署目标"}
      icon={ServerIcon}
      size="lg"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button
            variant="primary"
            loading={busy}
            disabled={!t.serverId || !t.certPath}
            onClick={async () => {
              setBusy(true);
              try {
                await api.SaveTarget(t);
                toast.ok("部署目标已保存");
                onSaved();
                onClose();
              } catch (e) {
                toast.error("保存失败", e);
              } finally {
                setBusy(false);
              }
            }}
          >
            保存
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="服务器" required>
            <ServerPicker single value={t.serverId ? [t.serverId] : []} onChange={(v) => set({ serverId: v[0] ?? "" })} maxHeight={150} />
          </Field>
          <Field label="预设" hint={t.preset === "xboard" ? "证书路径要和节点 config.yml 里的 cert_file / key_file 一致；不确定时用「扫描服务器」自动读取" : "选择后自动填写常用路径与命令，可再修改"}>
            <div className="grid grid-cols-2 gap-2">
              {(["nginx", "apache", "haproxy", "xboard", "custom"] as Preset[]).map((p) => (
                <button
                  key={p}
                  type="button"
                  onClick={() => set({ preset: p, ...presetValues(p, domain) })}
                  className={cn("h-14 rounded-xl border text-[13px] font-medium transition-colors", p === "custom" && "col-span-2", t.preset === p ? "border-accent/60 bg-accent/10 text-fg" : "border-line2 text-fg2 hover:bg-panel2")}
                >
                  {presetLabel[p]}
                  <div className="truncate px-1 text-[11px] font-normal text-muted">{presetNote[p]}</div>
                </button>
              ))}
            </div>
          </Field>
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_150px]">
          <Field label="证书路径" required>
            <Input mono value={t.certPath} onChange={(e) => set({ certPath: e.target.value })} />
          </Field>
          <Field label="证书格式">
            <Select value={t.certFormat} onChange={(e) => set({ certFormat: e.target.value })}>
              <option value="fullchain">完整链 fullchain</option>
              <option value="leaf">仅叶子证书</option>
              <option value="combined">证书+私钥合并</option>
            </Select>
          </Field>
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label={t.certFormat === "combined" ? "私钥路径（可选）" : "私钥路径"} required={t.certFormat !== "combined"}>
            <Input mono value={t.keyPath} onChange={(e) => set({ keyPath: e.target.value })} />
          </Field>
          <Field label="证书链路径（可选）" hint="Apache 2.2 等需要单独链文件时填写">
            <Input mono value={t.chainPath} onChange={(e) => set({ chainPath: e.target.value })} />
          </Field>
        </div>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-3">
          <Field label="证书权限">
            <Input mono value={t.certMode} onChange={(e) => set({ certMode: e.target.value })} />
          </Field>
          <Field label="私钥权限">
            <Input mono value={t.keyMode} onChange={(e) => set({ keyMode: e.target.value })} />
          </Field>
          <Field label="属主（可选）">
            <Input mono value={t.owner} onChange={(e) => set({ owner: e.target.value })} placeholder="root:root" />
          </Field>
        </div>
        <Field label="配置检测命令" hint="部署后执行，失败时自动回滚，不会重载服务">
          <Input mono value={t.testCommand} onChange={(e) => set({ testCommand: e.target.value })} placeholder="nginx -t" />
        </Field>
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-[1fr_150px]">
          <Field label="重载的服务">
            <TagInput value={t.services} onChange={(v) => set({ services: v })} placeholder="回车添加，例如 nginx" suggestions={["nginx", "apache2", "httpd", "haproxy", "openresty", "caddy", "postfix", "dovecot"]} />
          </Field>
          <Field label="服务操作">
            <Select value={t.serviceAction} onChange={(e) => set({ serviceAction: e.target.value })}>
              <option value="reload">reload（平滑）</option>
              <option value="restart">restart</option>
              <option value="none">不操作</option>
            </Select>
          </Field>
        </div>
        <Field label="后置命令（可选）" hint="例如通知、刷新 CDN；失败只提示不回滚">
          <Input mono value={t.postCommand} onChange={(e) => set({ postCommand: e.target.value })} />
        </Field>
        <div className="grid grid-cols-1 items-end gap-3 sm:grid-cols-[120px_1fr_auto]">
          <Field label="验证端口">
            <Input mono type="number" value={t.verifyPort} onChange={(e) => set({ verifyPort: Number(e.target.value) })} />
          </Field>
          <Field label="验证域名（SNI）" hint="部署后从本机连接服务器检查是否返回新证书，0 表示跳过">
            <Input mono value={t.verifyDomain} onChange={(e) => set({ verifyDomain: e.target.value })} placeholder={domain.replace("*.", "www.")} />
          </Field>
          <label className="mb-[22px] flex h-[34px] items-center gap-2 text-[12.5px] text-fg2">
            <Switch size="sm" checked={t.backup} onChange={(v) => set({ backup: v })} />
            部署前备份
          </label>
        </div>
      </div>
    </Modal>
  );
}

// ---- 部署进度 ----

function DeployModal({ open, cert, targetIds, onClose, onDone }: { open: boolean; cert: CertView; targetIds: string[]; onClose: () => void; onDone: () => void }) {
  const [steps, setSteps] = useState<Record<string, DeployStep[]>>({});
  const [results, setResults] = useState<Record<string, DeployResult>>({});
  const [running, setRunning] = useState(false);
  const runRef = useRef<string | null>(null);
  const targets = useMemo(() => cert.targets.filter((t) => targetIds.length === 0 || targetIds.includes(t.id)), [cert, targetIds]);

  useEffect(() => {
    if (!open) return;
    setSteps({});
    setResults({});
    setRunning(true);
    runRef.current = null;
    // 拿到部署 ID 之前到达的事件先缓存，之后按 ID 回放
    const buffer: Array<() => void> = [];
    const handle =
      <T extends { runId: string }>(apply: (e: T) => void) =>
      (e: T) => {
        if (runRef.current === null) buffer.push(() => e.runId === runRef.current && apply(e));
        else if (e.runId === runRef.current) apply(e);
      };
    const offs = [
      on<{ runId: string; step: DeployStep }>(
        "deploy:step",
        handle((e) =>
          setSteps((s) => {
            const list = [...(s[e.step.targetId] ?? [])];
            const i = list.findIndex((x) => x.step === e.step.step && x.label === e.step.label);
            if (i >= 0) list[i] = e.step;
            else list.push(e.step);
            return { ...s, [e.step.targetId]: list };
          }),
        ),
      ),
      on<{ runId: string; result: DeployResult }>(
        "deploy:target_done",
        handle((e) => setResults((r) => ({ ...r, [e.result.targetId]: e.result }))),
      ),
      on<{ runId: string; error?: string; results: DeployResult[] }>(
        "deploy:done",
        handle((e) => {
          setRunning(false);
          const ok = (e.results ?? []).filter((r) => r.success).length;
          if (e.error) toast.error("部署失败", e.error);
          else toast[ok === (e.results ?? []).length ? "ok" : "warn"](`部署完成：${ok}/${(e.results ?? []).length} 个目标成功`);
          onDone();
        }),
      ),
    ];
    api
      .DeployCert(cert.id, targetIds)
      .then((id: string) => {
        runRef.current = id;
        buffer.splice(0).forEach((f) => f());
      })
      .catch((e: unknown) => {
        setRunning(false);
        toast.error("无法开始部署", e);
      });
    return () => offs.forEach((f) => f());
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const done = Object.keys(results).length;
  return (
    <Modal
      open={open}
      onClose={() => !running && onClose()}
      title={`同步证书 · ${cert.name}`}
      description={running ? `正在部署 ${done}/${targets.length}…` : `完成 ${done}/${targets.length}`}
      icon={Rocket}
      size="lg"
      footer={
        <Button variant={running ? "secondary" : "primary"} disabled={running} onClick={onClose}>
          {running ? "部署中…" : "完成"}
        </Button>
      }
    >
      <Meter value={targets.length ? (done / targets.length) * 100 : 0} tone={running ? "accent" : Object.values(results).every((r) => r.success) ? "ok" : "warn"} className="mb-4" />
      <div className="space-y-3">
        {targets.map((t) => {
          const r = results[t.id];
          const list = steps[t.id] ?? [];
          return (
            <div key={t.id} className={cn("rounded-xl border p-3.5", r ? (r.success ? "border-ok/30 bg-ok/5" : "border-bad/30 bg-bad/5") : "border-line2 bg-bg2/40")}>
              <div className="flex items-center gap-2">
                {!r ? <LoaderCircle size={15} className="animate-spin text-accent" /> : r.success ? <CircleCheck size={15} className="text-ok" /> : <CircleX size={15} className="text-bad" />}
                <span className="text-[13px] font-semibold">{t.serverName}</span>
                <span className="truncate font-mono text-[11.5px] text-muted">{t.certPath}</span>
                {r && <span className={cn("ml-auto shrink-0 text-[12px]", r.success ? "text-ok" : "text-bad")}>{r.message}</span>}
              </div>
              {list.length > 0 && (
                <div className="mt-2.5 space-y-1 border-l border-line2 pl-3.5">
                  {list.map((s, i) => {
                    const Icon = s.status === "ok" ? CircleCheck : s.status === "failed" ? CircleX : s.status === "warning" ? TriangleAlert : s.status === "skipped" ? CircleDashed : LoaderCircle;
                    const color = ({ ok: "text-ok", failed: "text-bad", warning: "text-warn", skipped: "text-muted", running: "text-accent" } as Record<string, string>)[s.status] ?? "text-muted";
                    return (
                      <div key={i} className="text-[12px]">
                        <div className="flex items-center gap-1.5">
                          <Icon size={12} className={cn(color, s.status === "running" && "animate-spin")} />
                          <span className="text-fg2">{s.label}</span>
                        </div>
                        {s.message && s.status !== "running" && <div className="mt-0.5 ml-[18px] font-mono text-[11px] whitespace-pre-wrap text-muted">{s.message}</div>}
                      </div>
                    );
                  })}
                </div>
              )}
            </div>
          );
        })}
      </div>
    </Modal>
  );
}

// ---- 扫描发现 ----

function DiscoverModal({ open, cert, onClose, onChanged }: { open: boolean; cert: CertView; onClose: () => void; onChanged: () => void }) {
  const [server, setServer] = useState<string[]>([]);
  const [found, setFound] = useState<DiscoveredView[] | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (open) {
      setFound(null);
      setServer([]);
    }
  }, [open]);
  const scan = async () => {
    setBusy(true);
    try {
      setFound(await api.DiscoverCerts(server[0]));
    } catch (e) {
      toast.error("扫描失败", e);
    } finally {
      setBusy(false);
    }
  };
  const addTarget = async (d: DiscoveredView) => {
    const preset: Preset = d.source === "nginx" || d.source === "apache" || d.source === "haproxy" || d.source === "xboard" ? d.source : "custom";
    const base = presetValues(preset, cert.domains[0] ?? "");
    try {
      await api.SaveTarget({
        id: "",
        certId: cert.id,
        serverId: server[0],
        preset,
        ...base,
        certPath: d.certPath,
        keyPath: preset === "haproxy" ? "" : d.keyPath,
        certFormat: preset === "haproxy" ? "combined" : "fullchain",
        certMode: "644",
        keyMode: "600",
        owner: "",
        backup: true,
        postCommand: "",
        verifyPort: preset === "xboard" ? 0 : 443,
        verifyDomain: "",
      });
      toast.ok("已添加部署目标", d.certPath);
      onChanged();
      void scan();
    } catch (e) {
      toast.error("添加失败", e);
    }
  };
  return (
    <Modal open={open} onClose={onClose} title="扫描服务器上的证书" icon={Radar} size="xl" description="读取 nginx -T、Apache、HAProxy、XBoard 节点（xboard-node）的配置、1Panel 的网站证书目录与 /etc/letsencrypt/live，找出正在使用的证书文件">
      <div className="grid grid-cols-1 gap-4 md:grid-cols-[280px_1fr]">
        <div className="space-y-3">
          <ServerPicker single value={server} onChange={setServer} maxHeight={300} />
          <Button variant="primary" className="w-full" icon={Radar} loading={busy} disabled={!server[0]} onClick={scan}>
            开始扫描
          </Button>
        </div>
        <div className="min-h-[300px]">
          {found === null ? (
            <div className="flex h-full items-center justify-center text-[12.5px] text-muted">{busy ? <Spinner /> : "选择服务器后开始扫描"}</div>
          ) : found.length === 0 ? (
            <Empty icon={Search} title="没有发现证书配置" description="该服务器上没有找到 Nginx / Apache / HAProxy / XBoard 节点 / 1Panel 的证书配置。可以手动添加部署目标。" />
          ) : (
            <div className="space-y-2">
              {found.map((d) => (
                <div key={d.certPath} className="rounded-xl border border-line2 bg-bg2/50 p-3">
                  <div className="flex items-center gap-2">
                    <Badge tone="accent">{sourceLabel[d.source] ?? d.source}</Badge>
                    <span className="truncate font-mono text-[12px] text-fg">{d.certPath}</span>
                  </div>
                  {d.keyPath && d.keyPath !== d.certPath && <div className="mt-1 truncate font-mono text-[11.5px] text-muted">私钥 {d.keyPath}</div>}
                  <div className="mt-2 flex flex-wrap items-center gap-2 text-[12px]">
                    {d.error ? (
                      <span className="text-bad">{d.error}</span>
                    ) : (
                      <>
                        <span className="text-fg2">{d.domains.slice(0, 3).join(", ")}</span>
                        <DaysBadge days={Math.floor((d.notAfter - Date.now()) / 86400000)} />
                        {d.matchCertId ? (
                          <Badge tone={d.sameVersion ? "ok" : "warn"} icon={d.sameVersion ? CircleCheck : TriangleAlert}>
                            {d.matchCertId === cert.id ? "当前证书" : d.matchCertName}
                            {d.sameVersion ? " · 同版本" : " · 旧版本"}
                          </Badge>
                        ) : (
                          <Badge>不在证书库</Badge>
                        )}
                      </>
                    )}
                    <div className="ml-auto">
                      {d.hasTarget ? (
                        <Badge tone="ok" icon={CircleCheck}>
                          已是部署目标
                        </Badge>
                      ) : (
                        <Button size="xs" variant="soft" icon={Plus} onClick={() => addTarget(d)}>
                          添加为本证书的部署目标
                        </Button>
                      )}
                    </div>
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </div>
    </Modal>
  );
}

// ---- 线上检测 ----

function TLSCheck({ cert }: { cert: CertView }) {
  const { servers } = useApp();
  const firstTarget = cert.targets[0];
  const [host, setHost] = useState("");
  const [port, setPort] = useState(443);
  const [sni, setSni] = useState("");
  const [info, setInfo] = useState<TLSInfo | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setInfo(null);
    setHost(firstTarget ? servers.find((s) => s.id === firstTarget.serverId)?.host ?? "" : (cert.domains[0] ?? "").replace("*.", "www."));
    setSni((cert.domains[0] ?? "").replace("*.", "www."));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cert.id]);
  return (
    <Card className="p-4">
      <div className="mb-3 flex items-center gap-2 text-[13px] font-semibold">
        <Globe size={15} className="text-accent" /> 线上检测
      </div>
      <div className="grid grid-cols-[1fr_76px] gap-2">
        <Input mono value={host} onChange={(e) => setHost(e.target.value)} placeholder="主机或 IP" />
        <Input mono type="number" value={port} onChange={(e) => setPort(Number(e.target.value))} />
      </div>
      <div className="mt-2 flex gap-2">
        <Input mono value={sni} onChange={(e) => setSni(e.target.value)} placeholder="SNI 域名" className="flex-1" />
        <Button
          loading={busy}
          disabled={!host}
          onClick={async () => {
            setBusy(true);
            try {
              setInfo(await api.CheckTLS(host, port, sni));
            } catch (e) {
              setInfo(null);
              toast.error("检测失败", e);
            } finally {
              setBusy(false);
            }
          }}
        >
          检测
        </Button>
      </div>
      {info && (
        <div className="mt-3 space-y-1.5 text-[12px]">
          <div className="flex flex-wrap items-center gap-1.5">
            {info.fingerprint === cert.fingerprint ? (
              <Badge tone="ok" icon={CircleCheck}>
                与当前证书一致
              </Badge>
            ) : (
              <Badge tone="warn" icon={TriangleAlert}>
                与当前证书不同
              </Badge>
            )}
            {info.trusted ? (
              <Badge tone="ok" icon={CircleCheck}>
                浏览器信任
              </Badge>
            ) : (
              <Badge tone="bad" icon={CircleX} title={info.verifyError}>
                校验失败
              </Badge>
            )}
            <Badge>{info.protocol}</Badge>
            <span className="text-muted">{info.latencyMs} ms</span>
          </div>
          <KV k="线上证书" v={info.domains.slice(0, 3).join(", ")} />
          <KV k="到期" v={formatDate(info.notAfter, true)} />
          {!info.trusted && info.verifyError && <div className="text-[11.5px] break-all text-bad">{info.verifyError}</div>}
        </div>
      )}
    </Card>
  );
}
