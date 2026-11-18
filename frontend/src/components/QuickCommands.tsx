import { useEffect, useMemo, useRef, useState } from "react";
import { ArrowDown, ArrowUp, ChevronRight, Copy, CornerDownLeft, Ellipsis, Layers, Pencil, Plus, Search, ShieldAlert, Trash2, X, Zap } from "lucide-react";
import { runtime, type QuickCommand } from "../lib/api";
import { cn } from "../lib/format";
import { fillCommand, looksRisky, placeholders, useQuick } from "../lib/quick";
import { toast, useApp } from "../lib/store";
import { Button, Field, IconButton, Input, Modal, PopMenu, Switch, Textarea, type PopItem } from "./ui";
import { QuickRunModal } from "./QuickRun";

const UNGROUPED = "未分组";

// ---- 终端右侧的快捷命令面板 ----

export function QuickPanel({ onClose }: { onClose: () => void }) {
  const { commands, loaded, load, use, edit, remove, move, broadcast, setBroadcast } = useQuick();
  const terms = useApp((s) => s.terms);
  const [q, setQ] = useState("");
  // 分组默认折叠，点开哪个看哪个；面板每次打开都从折叠开始
  const [expanded, setExpanded] = useState<Record<string, boolean>>({});
  const [menu, setMenu] = useState<{ cmd: QuickCommand; x: number; y: number } | null>(null);

  useEffect(() => {
    if (!loaded) void load();
  }, [loaded, load]);

  // 刚添加的命令：展开它所在的分组，让用户看到它
  const known = useRef<Set<string> | null>(null);
  useEffect(() => {
    if (!loaded) return;
    const prev = known.current;
    known.current = new Set(commands.map((c) => c.id));
    const fresh = prev ? commands.filter((c) => !prev.has(c.id)) : [];
    if (fresh.length) setExpanded((e) => ({ ...e, ...Object.fromEntries(fresh.map((c) => [c.group || UNGROUPED, true])) }));
  }, [commands, loaded]);

  const openCount = terms.filter((t) => t.state === "open").length;
  const groups = useMemo(() => {
    const kw = q.trim().toLowerCase();
    const list = kw ? commands.filter((c) => [c.name, c.command, c.group, c.description].join(" ").toLowerCase().includes(kw)) : commands;
    const map = new Map<string, QuickCommand[]>();
    for (const c of list) {
      const g = c.group || UNGROUPED;
      if (!map.has(g)) map.set(g, []);
      map.get(g)!.push(c);
    }
    return [...map.entries()];
  }, [commands, q]);

  const menuItems = (c: QuickCommand): PopItem[] => {
    const peers = commands.filter((x) => x.group === c.group);
    const idx = peers.findIndex((x) => x.id === c.id);
    return [
      { label: "编辑", icon: Pencil, onClick: () => edit(c) },
      { label: "在多台服务器执行…", icon: Layers, onClick: () => void use(c, "batch") },
      {
        label: "复制命令",
        icon: Copy,
        onClick: () => {
          runtime.ClipboardSetText(c.command);
          toast.ok("已复制");
        },
      },
      { label: "", icon: X, onClick: () => {}, divider: true },
      { label: "上移", icon: ArrowUp, disabled: idx <= 0, onClick: () => void move(c.id, -1) },
      { label: "下移", icon: ArrowDown, disabled: idx < 0 || idx >= peers.length - 1, onClick: () => void move(c.id, 1) },
      { label: "", icon: X, onClick: () => {}, divider: true },
      { label: "删除", icon: Trash2, danger: true, onClick: () => void remove(c) },
    ];
  };

  return (
    <aside className="flex h-full w-[272px] shrink-0 flex-col border-l border-line bg-bg2">
      <div className="flex h-10 shrink-0 items-center gap-2 border-b border-line pr-1.5 pl-3">
        <Zap size={14} className="text-accent" />
        <span className="flex-1 text-[12.5px] font-semibold">快捷命令</span>
        <IconButton icon={Plus} size="sm" title="添加快捷命令" onClick={() => edit({})} />
        <IconButton icon={X} size="sm" title="收起面板" onClick={onClose} />
      </div>
      <div className="flex shrink-0 items-center gap-2 border-b border-line px-3 py-1.5">
        <Search size={13} className="shrink-0 text-muted" />
        <input value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索命令" spellCheck={false} className="h-6 min-w-0 flex-1 bg-transparent text-[12.5px] outline-none placeholder:text-muted" />
        {q && (
          <button type="button" className="text-muted hover:text-fg" onClick={() => setQ("")}>
            <X size={12} />
          </button>
        )}
      </div>
      <div className="min-h-0 flex-1 overflow-y-auto px-1.5 py-1.5">
        {loaded && commands.length === 0 && (
          <div className="px-3 py-8 text-center">
            <div className="text-[12.5px] text-muted">还没有快捷命令</div>
            <Button size="sm" variant="soft" icon={Plus} className="mt-3" onClick={() => edit({})}>
              添加第一条
            </Button>
          </div>
        )}
        {commands.length > 0 && groups.length === 0 && <div className="px-3 py-8 text-center text-[12.5px] text-muted">没有匹配的命令</div>}
        {groups.map(([g, list]) => {
          // 搜索时全部展开；只有一个分组时没有折叠的必要，默认展开
          const closed = !q && !(expanded[g] ?? groups.length === 1);
          return (
            <div key={g} className="mb-1" data-quick-group={g} data-open={!closed}>
              <button
                type="button"
                onClick={() => setExpanded((e) => ({ ...e, [g]: closed }))}
                className="flex h-7 w-full items-center gap-1 rounded-md px-1.5 text-left text-[11.5px] font-semibold tracking-wide text-muted hover:text-fg2"
              >
                <ChevronRight size={12} className={cn("transition-transform", !closed && "rotate-90")} />
                <span className="flex-1 truncate">{g}</span>
                <span className="font-normal">{list.length}</span>
              </button>
              {!closed &&
                list.map((c) => (
                  <div
                    key={c.id}
                    role="button"
                    tabIndex={0}
                    title={c.description ? `${c.description}\n${c.command}` : c.command}
                    onClick={() => void use(c, "terminal")}
                    onKeyDown={(e) => e.key === "Enter" && void use(c, "terminal")}
                    onContextMenu={(e) => {
                      e.preventDefault();
                      setMenu({ cmd: c, x: e.clientX, y: e.clientY });
                    }}
                    className="group relative flex cursor-default flex-col rounded-lg px-2.5 py-1.5 outline-none transition-colors hover:bg-panel2 focus-visible:bg-panel2"
                  >
                    <div className="flex items-center gap-1.5 pr-6">
                      <span className="truncate text-[12.5px] font-medium text-fg">{c.name}</span>
                      {c.confirm && <ShieldAlert size={11} className="shrink-0 text-warn" />}
                      {!c.autoEnter && <span className="shrink-0 rounded border border-line px-1 text-[10px] leading-[14px] text-muted">不回车</span>}
                    </div>
                    <div className="truncate pr-6 font-mono text-[11px] text-muted">{c.command.split("\n")[0]}</div>
                    <button
                      type="button"
                      title="更多操作"
                      onClick={(e) => {
                        e.stopPropagation();
                        const r = e.currentTarget.getBoundingClientRect();
                        setMenu({ cmd: c, x: r.right - 176, y: r.bottom + 4 });
                      }}
                      className="absolute top-1/2 right-1.5 flex h-6 w-6 -translate-y-1/2 items-center justify-center rounded-md text-muted opacity-0 transition-opacity hover:bg-panel3 hover:text-fg group-hover:opacity-100 focus-visible:opacity-100"
                    >
                      <Ellipsis size={14} />
                    </button>
                  </div>
                ))}
            </div>
          );
        })}
      </div>
      <label className="flex shrink-0 items-center gap-2.5 border-t border-line px-3 py-2.5">
        <Switch size="sm" checked={broadcast} onChange={setBroadcast} />
        <span className="min-w-0 flex-1">
          <span className="block text-[12px] text-fg2">同时发送到全部终端</span>
          <span className="block truncate text-[11px] text-muted">{broadcast ? `将发送到 ${openCount} 个已连接的终端` : "点击命令只发送到当前终端"}</span>
        </span>
      </label>
      {menu && <PopMenu at={{ x: menu.x, y: menu.y }} items={menuItems(menu.cmd)} onClose={() => setMenu(null)} />}
    </aside>
  );
}

// ---- 新建 / 编辑 ----

function QuickEditor() {
  const { editing, closeEdit, save, remove, commands } = useQuick();
  const [f, setF] = useState<Partial<QuickCommand>>({});
  const [touchedConfirm, setTouchedConfirm] = useState(false);
  const [saving, setSaving] = useState(false);
  const open = editing !== null;
  const isNew = !editing?.id;

  useEffect(() => {
    if (!editing) return;
    const cmd = editing.command ?? "";
    setF({ name: "", group: "", description: "", autoEnter: true, ...editing, command: cmd, confirm: editing.confirm ?? (!editing.id && looksRisky(cmd)) });
    setTouchedConfirm(false);
  }, [editing]);

  const groups = useMemo(() => [...new Set(commands.map((c) => c.group).filter(Boolean))], [commands]);
  const phs = placeholders(f.command ?? "");
  const risky = looksRisky(f.command ?? "");
  const setCommand = (command: string) =>
    setF((x) => ({
      ...x,
      command,
      // 新建时根据命令内容自动建议「执行前确认」，用户手动改过后不再干预
      confirm: isNew && !touchedConfirm ? looksRisky(command) : x.confirm,
    }));

  const submit = async () => {
    setSaving(true);
    const saved = await save(f);
    setSaving(false);
    if (saved) {
      toast.ok(isNew ? "快捷命令已添加" : "已保存");
      closeEdit();
    }
  };

  return (
    <Modal
      open={open}
      onClose={closeEdit}
      title={isNew ? "添加快捷命令" : "编辑快捷命令"}
      description="保存后在终端右侧的面板里点一下即可发送，也可以在多台服务器上批量执行。"
      icon={Zap}
      footer={
        <>
          {!isNew && (
            <Button
              variant="ghost"
              icon={Trash2}
              className="mr-auto text-bad hover:bg-bad/12 hover:text-bad"
              onClick={async () => {
                const cur = commands.find((c) => c.id === editing?.id);
                if (cur && (await remove(cur))) closeEdit();
              }}
            >
              删除
            </Button>
          )}
          <Button variant="ghost" onClick={closeEdit}>
            取消
          </Button>
          <Button variant="primary" loading={saving} disabled={!(f.command ?? "").trim()} onClick={submit}>
            保存
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Field label="命令" required hint={<span>需要临时填写的部分写成 <code className="font-mono text-fg2">{"{{名称}}"}</code> 或 <code className="font-mono text-fg2">{"{{名称:默认值}}"}</code>，例如 <code className="font-mono text-fg2">{"systemctl restart {{服务名:nginx}}"}</code>，使用时会先让你填写。</span>}>
          <Textarea mono autoFocus rows={3} value={f.command ?? ""} onChange={(e) => setCommand(e.target.value)} placeholder="例如 docker ps -a" className="min-h-[72px]" />
        </Field>
        {phs.length > 0 && (
          <div className="flex flex-wrap items-center gap-1.5 text-[12px] text-muted">
            使用时填写：
            {phs.map((p) => (
              <span key={p.name} className="inline-flex h-[22px] items-center rounded-md border border-accent/25 bg-accent/12 px-1.5 text-[11.5px] text-accent">
                {p.name}
                {p.def && <span className="ml-1 text-muted">默认 {p.def}</span>}
              </span>
            ))}
          </div>
        )}
        <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
          <Field label="名称" hint="留空则使用命令的第一行">
            <Input value={f.name ?? ""} onChange={(e) => setF((x) => ({ ...x, name: e.target.value }))} placeholder="例如 查看全部容器" />
          </Field>
          <Field label="分组" hint="相同分组的命令会放在一起">
            <Input value={f.group ?? ""} onChange={(e) => setF((x) => ({ ...x, group: e.target.value }))} placeholder="例如 Docker" list="quick-groups" />
            <datalist id="quick-groups">
              {groups.map((g) => (
                <option key={g} value={g} />
              ))}
            </datalist>
          </Field>
        </div>
        <Field label="说明（可选）">
          <Input value={f.description ?? ""} onChange={(e) => setF((x) => ({ ...x, description: e.target.value }))} placeholder="这条命令是做什么的" />
        </Field>
        <div className="divide-y divide-line rounded-xl border border-line2 bg-bg2/50">
          <div className="flex items-center gap-3 px-3.5 py-2.5">
            <CornerDownLeft size={15} className="shrink-0 text-muted" />
            <div className="min-w-0 flex-1">
              <div className="text-[12.5px] font-medium text-fg">发送后自动回车执行</div>
              <div className="text-[12px] text-muted">关闭后只把命令填到终端里，你可以修改后再自己回车</div>
            </div>
            <Switch checked={!!f.autoEnter} onChange={(v) => setF((x) => ({ ...x, autoEnter: v }))} />
          </div>
          <div className="flex items-center gap-3 px-3.5 py-2.5">
            <ShieldAlert size={15} className={cn("shrink-0", f.confirm ? "text-warn" : "text-muted")} />
            <div className="min-w-0 flex-1">
              <div className="text-[12.5px] font-medium text-fg">执行前先确认</div>
              <div className="text-[12px] text-muted">{risky && !f.confirm ? "这条命令看起来会重启、停止或删除内容，建议开启" : "适合重启、删除这类有风险的命令，避免误点"}</div>
            </div>
            <Switch
              checked={!!f.confirm}
              onChange={(v) => {
                setTouchedConfirm(true);
                setF((x) => ({ ...x, confirm: v }));
              }}
            />
          </div>
        </div>
      </div>
    </Modal>
  );
}

// ---- 填写占位符 ----

function QuickFill() {
  const { filling, cancelFill, dispatch } = useQuick();
  const [values, setValues] = useState<Record<string, string>>({});
  const phs = useMemo(() => (filling ? placeholders(filling.cmd.command) : []), [filling]);
  useEffect(() => {
    setValues(Object.fromEntries(phs.map((p) => [p.name, p.def])));
  }, [phs]);
  if (!filling) return null;
  const text = fillCommand(filling.cmd.command, values);
  const missing = phs.filter((p) => !(values[p.name] ?? "").trim());
  const submit = () => {
    if (missing.length) return toast.warn(`请填写「${missing[0].name}」`);
    void dispatch(filling.cmd, text, filling.target);
  };
  return (
    <Modal
      open
      onClose={cancelFill}
      title={filling.cmd.name}
      description={filling.cmd.description || "填写下面的内容后发送"}
      icon={Zap}
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={cancelFill}>
            取消
          </Button>
          <Button variant="primary" onClick={submit}>
            {filling.target === "batch" ? "下一步：选择服务器" : filling.cmd.autoEnter ? "执行" : "发送到终端"}
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        {phs.map((p, i) => (
          <Field key={p.name} label={p.name}>
            <Input mono autoFocus={i === 0} value={values[p.name] ?? ""} onChange={(e) => setValues((v) => ({ ...v, [p.name]: e.target.value }))} onKeyDown={(e) => e.key === "Enter" && submit()} placeholder={p.def || p.name} />
          </Field>
        ))}
        <div>
          <div className="mb-1.5 text-[12px] text-muted">将要发送的命令</div>
          <pre className="selectable overflow-x-auto rounded-xl border border-line bg-bg2 px-3 py-2 font-mono text-[12px] leading-relaxed whitespace-pre-wrap text-fg2">{text}</pre>
        </div>
      </div>
    </Modal>
  );
}

/** 全局挂载一次：编辑器、占位符填写、批量执行弹窗 */
export function QuickHost() {
  const { batch, closeBatch } = useQuick();
  return (
    <>
      <QuickEditor />
      <QuickFill />
      <QuickRunModal open={batch !== null} onClose={closeBatch} command={batch?.command ?? ""} title={batch ? `批量执行 · ${batch.title}` : "批量执行"} />
    </>
  );
}
