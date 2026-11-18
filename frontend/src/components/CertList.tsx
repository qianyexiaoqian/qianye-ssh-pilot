import { useEffect, useState } from "react";
import { Check, ChevronRight, FolderMinus, Pencil, Server as ServerIcon } from "lucide-react";
import { api, type CertView } from "../lib/api";
import { certTone, cn } from "../lib/format";
import { SortOverlay, useSortable } from "../lib/sortable";
import { toast } from "../lib/store";
import { Button, Field, Input, Meter, Modal } from "./ui";

// 证书中心左侧的证书列表。
// 证书可以放进分组：分组默认折叠，点标题展开；没有分组的证书直接列在下面。
// 分组和证书都可以按住拖动调整先后顺序；把证书拖到另一个分组的标题上松手，就把它移进那个分组。

/** 拖动目标身上的属性：值是分组名，空字符串表示「未分组」 */
const DROP = "data-cert-drop";

type ItemMode = "normal" | "slot" | "ghost";

function CertRow({ c, active, mode = "normal", onSelect, sort }: { c: CertView; active: boolean; mode?: ItemMode; onSelect?: () => void; sort?: object }) {
  const tone = certTone(c.days);
  const synced = c.targets.filter((t) => t.synced).length;
  const total = Math.max(1, (c.notAfter - c.notBefore) / 86400000);
  return (
    <button
      type="button"
      {...sort}
      onClick={onSelect}
      data-cert-row={c.id}
      className={cn(
        "block w-full rounded-xl px-3 py-2.5 text-left transition-colors",
        active ? "bg-accent/12" : "hover:bg-panel2",
        mode === "slot" && "bg-transparent outline-1 -outline-offset-1 outline-accent/45 outline-dashed hover:bg-transparent [&>*]:invisible",
        mode === "ghost" && "h-full border border-line2 bg-panel",
      )}
    >
      <div className="flex items-center justify-between gap-2">
        <span className="truncate text-[13px] font-medium text-fg">{c.name}</span>
        <span className={cn("shrink-0 text-[11.5px]", `text-${tone}`)}>{c.days < 0 ? "已过期" : `${c.days} 天`}</span>
      </div>
      <div className="mt-0.5 truncate text-[11.5px] text-muted">
        {/* 改过名称的证书，这一行显示它的域名，免得认不出是哪张 */}
        {c.domains.length > 1 ? `${c.domains[0]} 等 ${c.domains.length} 个域名` : c.domains[0] && c.name !== c.domains[0] ? c.domains[0] : c.issuer}
      </div>
      <Meter className="mt-2" height={4} value={(Math.max(c.days, 0) / total) * 100} tone={tone} />
      <div className="mt-1.5 flex items-center gap-1 text-[11px] text-muted">
        <ServerIcon size={11} />
        {c.targets.length === 0 ? "未配置部署目标" : synced === c.targets.length ? <span className="text-ok">{c.targets.length} 个目标已同步</span> : <span className="text-warn">{c.targets.length - synced}/{c.targets.length} 个目标待同步</span>}
      </div>
    </button>
  );
}

/** 一组证书（一个分组里的，或者没有分组的）：可以按住上下拖动 */
function CertRows({
  list,
  sel,
  onSelect,
  onReorder,
  onMove,
  onHover,
}: {
  list: CertView[];
  sel: string | null;
  onSelect: (id: string) => void;
  onReorder: (ids: string[]) => void;
  /** 被拖到了另一个分组的标题上 */
  onMove?: (id: string, group: string) => void;
  onHover?: (group: string | null) => void;
}) {
  const sortable = useSortable({ ids: list.map((c) => c.id), axis: "y", onReorder, dropAttr: onMove ? DROP : undefined, onDropTo: onMove, onHover });
  const byId = new Map(list.map((c) => [c.id, c]));
  return (
    <div className="relative space-y-1">
      {sortable.order.map((id) => {
        const c = byId.get(id);
        return c ? <CertRow key={id} c={c} active={sel === id} mode={sortable.draggingId === id ? "slot" : "normal"} onSelect={() => onSelect(id)} sort={sortable.itemProps(id)} /> : null;
      })}
      <SortOverlay sortable={sortable} className="sort-flat">
        {(id) => {
          const c = byId.get(id);
          return c ? <CertRow c={c} active={false} mode="ghost" /> : null;
        }}
      </SortOverlay>
    </div>
  );
}

export function CertList({
  certs,
  groups,
  sel,
  q,
  reveal,
  onSelect,
  onCerts,
  onGroups,
  onEditGroup,
  reload,
}: {
  certs: CertView[];
  /** 分组的先后顺序 */
  groups: string[];
  sel: string | null;
  /** 搜索的关键字：不为空时直接列出匹配的证书，不分组 */
  q: string;
  /** 需要让它显示出来的证书（从别处跳转过来查看时）：它所在的分组会自动展开 */
  reveal?: string | null;
  onSelect: (id: string) => void;
  /** 顺序调整后立刻按新的顺序显示 */
  onCerts: (next: CertView[]) => void;
  onGroups: (next: string[]) => void;
  onEditGroup: (group: string) => void;
  reload: () => Promise<unknown>;
}) {
  // 分组默认折叠
  const [open, setOpen] = useState<Record<string, boolean>>({});
  // 拖着证书经过的分组（高亮它的标题）；null 表示没有
  const [hover, setHover] = useState<string | null>(null);
  useEffect(() => {
    const g = reveal ? certs.find((c) => c.id === reveal)?.group : "";
    if (g) setOpen((o) => (o[g] ? o : { ...o, [g]: true }));
    // 只在要显示的证书变化时展开：之后用户可以自己再折叠起来
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [reveal, certs.length]);

  const failed = (what: string) => (e: unknown) => {
    toast.error(what, e);
    void reload();
  };
  // 在一个分组里拖动：只调整这几张证书之间的顺序，它们占着的位置不变
  const reorder = (ids: string[]) => {
    const moved = new Set(ids);
    const next = ids.map((id) => certs.find((c) => c.id === id)).filter((c): c is CertView => !!c);
    let i = 0;
    onCerts(certs.map((c) => (moved.has(c.id) ? next[i++] : c)));
    api.ReorderCerts(ids).catch(failed("调整顺序失败"));
  };
  const reorderGroups = (names: string[]) => {
    onGroups(names);
    api.ReorderCertGroups(names).catch(failed("调整顺序失败"));
  };
  const move = (id: string, group: string) => {
    const c = certs.find((x) => x.id === id);
    if (!c || c.group === group) return;
    onCerts(certs.map((x) => (x.id === id ? { ...x, group } : x)));
    if (group) setOpen((o) => ({ ...o, [group]: true }));
    api
      .SetCertGroup([id], group)
      .then(() => {
        toast.ok(group ? `已移到分组「${group}」` : "已移出分组", c.name);
        return reload();
      })
      .catch(failed("移动失败"));
  };
  const groupSort = useSortable({ ids: groups, axis: "y", onReorder: reorderGroups });

  const kw = q.trim().toLowerCase();
  if (kw) {
    const found = certs.filter((c) => (c.name + " " + c.domains.join(" ") + " " + c.issuer + " " + c.group).toLowerCase().includes(kw));
    if (found.length === 0) return <div className="px-3 py-8 text-center text-[12.5px] text-muted">没有匹配的证书</div>;
    return (
      <div className="space-y-1">
        {found.map((c) => (
          <CertRow key={c.id} c={c} active={sel === c.id} onSelect={() => onSelect(c.id)} />
        ))}
      </div>
    );
  }
  // 还没有任何分组：就是一个可以拖动的列表
  if (groups.length === 0) return <CertRows list={certs} sel={sel} onSelect={onSelect} onReorder={reorder} />;

  const header = (g: string, mode: ItemMode) => {
    const list = certs.filter((c) => c.group === g);
    const isOpen = !!open[g];
    const days = list.length ? Math.min(...list.map((c) => c.days)) : 0;
    const waiting = list.reduce((n, c) => n + c.targets.filter((t) => !t.synced).length, 0);
    return (
      <div
        {...{ [DROP]: g }}
        data-cert-group={g}
        data-open={isOpen}
        onClick={() => setOpen((o) => ({ ...o, [g]: !isOpen }))}
        title={mode === "normal" ? "点击展开或收起；按住可以拖动调整分组的顺序" : undefined}
        className={cn(
          "group/hd flex h-9 items-center gap-1.5 rounded-lg pr-1.5 pl-2 text-[12.5px] transition-colors hover:bg-panel2",
          hover === g && "bg-accent/15 ring-1 ring-accent/50",
          mode === "ghost" && "border border-line2 bg-panel",
        )}
      >
        <ChevronRight size={14} className={cn("shrink-0 text-muted transition-transform", isOpen && mode !== "ghost" && "rotate-90")} />
        <span className="truncate font-semibold text-fg2">{g}</span>
        {/* 折叠着的时候，提示当前查看的证书在这个分组里 */}
        {!isOpen && list.some((c) => c.id === sel) && <span className="h-1.5 w-1.5 shrink-0 rounded-full bg-accent" title="正在查看的证书在这个分组里" />}
        <span className="ml-auto flex shrink-0 items-center gap-2 pl-2 text-[11.5px]">
          {waiting > 0 && <span className="text-warn">{waiting} 个待同步</span>}
          {list.length > 0 && <span className={`text-${certTone(days)}`}>{days < 0 ? "有过期" : `${days} 天`}</span>}
          <span className="min-w-[14px] text-right text-muted tabular-nums">{list.length}</span>
          <button
            type="button"
            title="修改分组"
            onClick={(e) => {
              e.stopPropagation();
              onEditGroup(g);
            }}
            className="flex h-6 w-6 items-center justify-center rounded-md text-muted opacity-0 transition-opacity group-hover/hd:opacity-100 hover:bg-panel3 hover:text-fg"
          >
            <Pencil size={12} />
          </button>
        </span>
      </div>
    );
  };
  const loose = certs.filter((c) => !c.group);
  return (
    <div className="space-y-1">
      <div className="relative space-y-1">
        {groupSort.order.map((g) => {
          const lifted = groupSort.draggingId === g;
          return (
            <div key={g} {...groupSort.itemProps(g)} className={cn("rounded-xl", lifted && "outline-1 -outline-offset-1 outline-accent/45 outline-dashed [&>*]:invisible")}>
              {header(g, "normal")}
              {open[g] && (
                <div className="mt-1 mb-2 ml-3 border-l border-line pl-1.5">
                  <CertRows list={certs.filter((c) => c.group === g)} sel={sel} onSelect={onSelect} onReorder={reorder} onMove={move} onHover={setHover} />
                </div>
              )}
            </div>
          );
        })}
        <SortOverlay sortable={groupSort} className="sort-flat">
          {(g) => header(g, "ghost")}
        </SortOverlay>
      </div>
      <div
        {...{ [DROP]: "" }}
        className={cn("flex h-8 items-center gap-1.5 rounded-lg px-2 pt-1 text-[11.5px] font-semibold tracking-wide text-muted transition-colors", hover === "" && "bg-accent/15 ring-1 ring-accent/50")}
      >
        <span>未分组</span>
        <span className="ml-auto font-normal tabular-nums">{loose.length}</span>
      </div>
      {loose.length === 0 ? (
        <div className="px-3 pb-2 text-[11.5px] leading-relaxed text-muted/80">证书都在分组里了。把证书拖到这里可以移出分组。</div>
      ) : (
        <CertRows list={loose} sel={sel} onSelect={onSelect} onReorder={reorder} onMove={move} onHover={setHover} />
      )}
    </div>
  );
}

/** 新建或修改一个分组：起名字，勾选放进这个分组的证书 */
export function CertGroupModal({
  open,
  group,
  certs,
  groups,
  preselect,
  onClose,
  onSaved,
}: {
  open: boolean;
  /** 要修改的分组；空字符串表示新建 */
  group: string;
  certs: CertView[];
  groups: string[];
  /** 新建时先勾选上的证书 */
  preselect?: string | null;
  onClose: () => void;
  onSaved: (name: string) => void;
}) {
  const [name, setName] = useState("");
  const [picked, setPicked] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (!open) return;
    setName(group);
    setPicked(group ? certs.filter((c) => c.group === group).map((c) => c.id) : preselect ? [preselect] : []);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, group]);

  const trimmed = name.trim();
  const taken = trimmed !== group && groups.includes(trimmed);
  const save = async (ids: string[]) => {
    setBusy(true);
    try {
      await api.SaveCertGroup(group, ids.length ? trimmed : group, ids);
      onSaved(ids.length ? trimmed : "");
      onClose();
    } catch (e) {
      toast.error("保存失败", e);
    } finally {
      setBusy(false);
    }
  };
  const toggle = (id: string) => setPicked((p) => (p.includes(id) ? p.filter((x) => x !== id) : [...p, id]));
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={group ? `修改分组 · ${group}` : "新建分组"}
      description="分组只是为了在证书中心里好找，不影响证书和它的部署。"
      icon={Pencil}
      size="sm"
      footer={
        <>
          {group && (
            <Button icon={FolderMinus} className="mr-auto" disabled={busy} onClick={() => void save([])} title="分组里的证书变回未分组，证书本身不受影响">
              解散分组
            </Button>
          )}
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button variant="primary" loading={busy} disabled={!trimmed || taken || picked.length === 0} onClick={() => void save(picked)}>
            保存
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <Field label="分组名称" hint={taken ? <span className="text-bad">已经有一个叫「{trimmed}」的分组了</span> : undefined}>
          <Input autoFocus value={name} onChange={(e) => setName(e.target.value)} placeholder="例如：生产 / 测试 / 某个项目" maxLength={30} data-group-name />
        </Field>
        <Field label={`放进这个分组的证书（已选 ${picked.length} 张）`} hint={picked.length === 0 ? "至少选一张证书" : undefined}>
          <div className="max-h-[min(300px,40vh)] space-y-0.5 overflow-y-auto rounded-xl border border-line2 bg-bg2 p-1">
            {certs.map((c) => {
              const on = picked.includes(c.id);
              return (
                <button key={c.id} type="button" onClick={() => toggle(c.id)} data-group-pick={c.id} className={cn("flex w-full items-center gap-2.5 rounded-lg px-2 py-1.5 text-left transition-colors", on ? "bg-accent/10" : "hover:bg-panel2")}>
                  <span className={cn("flex h-4 w-4 shrink-0 items-center justify-center rounded-[5px] border transition-colors", on ? "border-accent bg-accent text-white" : "border-line2")}>{on && <Check size={11} strokeWidth={3} />}</span>
                  <span className="min-w-0 flex-1">
                    <span className="block truncate text-[13px] text-fg">{c.name}</span>
                    <span className="block truncate text-[11.5px] text-muted">{c.name !== c.domains[0] ? c.domains[0] : c.issuer}</span>
                  </span>
                  {c.group && c.group !== group && <span className="shrink-0 rounded-md bg-panel3 px-1.5 py-0.5 text-[11px] text-muted">现在在「{c.group}」</span>}
                </button>
              );
            })}
          </div>
        </Field>
      </div>
    </Modal>
  );
}
