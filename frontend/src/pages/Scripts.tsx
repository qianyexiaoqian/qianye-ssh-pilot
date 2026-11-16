import { useEffect, useMemo, useState } from "react";
import { Bot, CircleCheck, CircleX, Copy, FileCode, History, Lock, Play, Plus, Save, Search, Square, Trash2, X, Zap } from "lucide-react";
import { api, type RunRecord, type Script } from "../lib/api";
import { cn, formatMs, stripAnsi, timeAgo } from "../lib/format";
import { confirm, toast, useApp } from "../lib/store";
import { Badge, Button, Callout, Empty, Field, IconButton, Input, Modal, Select, Switch, Tabs } from "../components/ui";
import { CodeEditor, OutputBox, ServerPicker } from "../components/widgets";
import { RunOutputs, useRunStream } from "../components/QuickRun";

const ADHOC = "__adhoc";

function blank(): Script {
  return { id: "", name: "新脚本", description: "", category: "自定义", interpreter: "bash", content: "#!/bin/bash\n# 参数通过环境变量读取，例如 $DOMAIN\n\n", params: [], tags: [], readOnly: false, builtin: false, createdBy: "user", createdAt: 0, updatedAt: 0 } as Script;
}

export default function Scripts() {
  const { servers, params } = useApp();
  const [scripts, setScripts] = useState<Script[]>([]);
  const [selId, setSelId] = useState<string>(ADHOC);
  const [draft, setDraft] = useState<Script>(() => ({ ...blank(), name: "临时命令", content: "" }));
  const [dirty, setDirty] = useState(false);
  const [q, setQ] = useState("");
  const [cat, setCat] = useState("全部");
  const [targets, setTargets] = useState<string[]>([]);
  const [values, setValues] = useState<Record<string, string>>({});
  const [panel, setPanel] = useState<"run" | "history">("run");
  const [saving, setSaving] = useState(false);
  const { state, begin, start, fail, reset } = useRunStream();
  const [runTargets, setRunTargets] = useState<string[]>([]);

  const load = () => api.ListScripts().then((s) => setScripts(s ?? []));
  useEffect(() => {
    void load();
  }, []);

  const select = (s: Script | null) => {
    if (!s) {
      setSelId(ADHOC);
      setDraft({ ...blank(), name: "临时命令", content: "" });
    } else {
      setSelId(s.id || "__new");
      setDraft({ ...s, params: [...(s.params ?? [])] });
      setValues(Object.fromEntries((s.params ?? []).map((p) => [p.name, p.default])));
    }
    setDirty(false);
  };

  // 从其他页面跳转（巡检建议、仪表盘）
  useEffect(() => {
    if (params.tab === "history") setPanel("history");
    if (params.scriptId && scripts.length) {
      const s = scripts.find((x) => x.id === params.scriptId);
      if (s) {
        select(s);
        setValues({ ...Object.fromEntries((s.params ?? []).map((p) => [p.name, p.default])), ...(params.params ?? {}) });
        setPanel("run");
      }
    }
    if (params.servers) setTargets(params.servers);
    if (params.content) {
      select(null);
      setDraft((d) => ({ ...d, content: params.content }));
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [params, scripts.length]);

  const categories = useMemo(() => ["全部", ...new Set(scripts.map((s) => s.category))], [scripts]);
  const list = scripts.filter((s) => (cat === "全部" || s.category === cat) && (s.name + s.description).toLowerCase().includes(q.toLowerCase()));
  const isAdhoc = selId === ADHOC;
  const readOnly = draft.builtin;
  const names = useMemo(() => Object.fromEntries(servers.map((s) => [s.id, s.name])), [servers]);

  const update = (patch: Partial<Script>) => {
    setDraft((d) => ({ ...d, ...patch }) as Script);
    setDirty(true);
  };
  const save = async () => {
    setSaving(true);
    try {
      const saved = await api.SaveScript(draft);
      toast.ok("脚本已保存");
      await load();
      select(saved);
    } catch (e) {
      toast.error("保存失败", e);
    } finally {
      setSaving(false);
    }
  };
  const run = async () => {
    if (!targets.length) return toast.warn("请先选择目标服务器");
    if (!isAdhoc && !draft.readOnly) {
      const ok = await confirm({ title: `在 ${targets.length} 台服务器上执行「${draft.name}」？`, message: "该脚本可能修改系统状态。", confirmText: "执行" });
      if (!ok) return;
    }
    try {
      const req = isAdhoc || dirty || !draft.id ? { content: draft.content, interpreter: draft.interpreter, serverIds: targets, params: values, title: isAdhoc ? "" : draft.name } : { scriptId: draft.id, serverIds: targets, params: values };
      setRunTargets(targets);
      setPanel("run");
      begin();
      start(await api.RunScript(req));
    } catch (e) {
      fail();
      toast.error("无法执行", e);
    }
  };

  return (
    <div className="flex h-full flex-col overflow-y-auto @3xl:flex-row @3xl:overflow-hidden">
      {/* 列表 */}
      <div className="flex max-h-[min(240px,45vh)] w-full shrink-0 flex-col border-b border-line bg-bg2/40 @3xl:max-h-none @3xl:w-[280px] @3xl:border-r @3xl:border-b-0">
        <div className="space-y-2.5 p-4 pb-3">
          <div className="flex items-center justify-between">
            <h1 className="text-[16px] font-semibold">脚本库</h1>
            <IconButton icon={Plus} tip="新建脚本" onClick={() => select(blank())} />
          </div>
          <Input icon={Search} value={q} onChange={(e) => setQ(e.target.value)} placeholder="搜索脚本" />
          <div className="flex flex-wrap gap-1">
            {categories.map((c) => (
              <button key={c} type="button" onClick={() => setCat(c)} className={cn("h-6 rounded-md px-2 text-[11.5px] transition-colors", cat === c ? "bg-accent/15 text-accent" : "text-muted hover:bg-panel2 hover:text-fg2")}>
                {c}
              </button>
            ))}
          </div>
        </div>
        <div className="min-h-0 flex-1 space-y-0.5 overflow-y-auto px-2 pb-4">
          <button type="button" onClick={() => select(null)} className={cn("flex w-full items-center gap-2.5 rounded-[10px] px-2.5 py-2.5 text-left transition-colors", isAdhoc ? "bg-accent/12" : "hover:bg-panel2")}>
            <div className="flex h-8 w-8 items-center justify-center rounded-lg grad-accent text-white">
              <Zap size={15} />
            </div>
            <div>
              <div className="text-[13px] font-medium">临时命令</div>
              <div className="text-[11.5px] text-muted">在多台服务器上批量执行</div>
            </div>
          </button>
          <div className="my-2 h-px bg-line" />
          {list.map((s) => (
            <button key={s.id} type="button" onClick={() => select(s)} className={cn("w-full rounded-[10px] px-2.5 py-2 text-left transition-colors", selId === s.id ? "bg-accent/12" : "hover:bg-panel2")}>
              <div className="flex items-center gap-1.5">
                <span className="truncate text-[13px] font-medium text-fg">{s.name}</span>
                {s.createdBy === "agent" && <Bot size={12} className="shrink-0 text-accent" />}
                {s.builtin && <Lock size={11} className="shrink-0 text-muted" />}
              </div>
              <div className="mt-0.5 flex min-w-0 items-center gap-1.5 text-[11px] whitespace-nowrap">
                <span className="shrink-0 text-muted">{s.category}</span>
                {s.readOnly && <span className="shrink-0 text-ok">· 只读</span>}
                <span className="min-w-0 truncate text-muted/80">· {s.description}</span>
              </div>
            </button>
          ))}
        </div>
      </div>

      <div className="flex min-w-0 shrink-0 flex-col @3xl:min-h-0 @3xl:flex-1 @3xl:shrink @6xl:flex-row">
      {/* 编辑器 */}
      <div className="@container flex min-w-0 flex-col @3xl:min-h-0 @3xl:flex-1">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-b border-line px-4 py-3 @2xl:px-6 @2xl:py-4">
          <div className="min-w-0 flex-1">
            {isAdhoc ? (
              <>
                <div className="text-[16px] font-semibold">临时命令</div>
                <div className="text-[12px] text-muted">输入命令或脚本，选择服务器后执行；可保存为脚本以便复用</div>
              </>
            ) : (
              <>
                <input
                  value={draft.name}
                  readOnly={readOnly}
                  onChange={(e) => update({ name: e.target.value })}
                  className="w-full bg-transparent text-[16px] font-semibold outline-none"
                />
                <input
                  value={draft.description}
                  readOnly={readOnly}
                  onChange={(e) => update({ description: e.target.value })}
                  placeholder="添加描述…"
                  className="mt-0.5 w-full bg-transparent text-[12.5px] text-muted outline-none placeholder:text-muted/60"
                />
              </>
            )}
          </div>
          <div className="flex shrink-0 items-center gap-2">
            {draft.builtin && <Badge icon={Lock}>内置</Badge>}
            {draft.createdBy === "agent" && (
              <Badge tone="accent" icon={Bot}>
                AI 创建
              </Badge>
            )}
            {isAdhoc ? (
              <Button
                icon={Save}
                disabled={!draft.content.trim()}
                onClick={() => {
                  setSelId("__new");
                  setDraft((d) => ({ ...d, name: "新脚本" }) as Script);
                  setDirty(true);
                }}
              >
                保存为脚本
              </Button>
            ) : readOnly ? (
              <Button
                icon={Copy}
                onClick={async () => {
                  try {
                    const s = await api.DuplicateScript(draft.id);
                    await load();
                    select(s);
                    toast.ok("已复制为新脚本");
                  } catch (e) {
                    toast.error("复制失败", e);
                  }
                }}
              >
                复制后编辑
              </Button>
            ) : (
              <>
                {draft.id && (
                  <IconButton
                    icon={Trash2}
                    tone="danger"
                    tip="删除"
                    onClick={async () => {
                      if (!(await confirm({ title: `删除脚本「${draft.name}」？`, danger: true, confirmText: "删除" }))) return;
                      await api.DeleteScript(draft.id);
                      await load();
                      select(null);
                    }}
                  />
                )}
                <Button variant={dirty ? "primary" : "secondary"} icon={Save} loading={saving} onClick={save}>
                  保存
                </Button>
              </>
            )}
          </div>
        </div>
        <div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-4 py-4 @2xl:px-6 @2xl:py-5">
          {readOnly && <Callout tone="info">内置脚本不可修改。可以直接执行，或复制为自己的脚本后编辑。</Callout>}
          {!isAdhoc && (
            <div className="grid grid-cols-1 items-end gap-3 @xl:grid-cols-[1fr_1fr_auto]">
              <Field label="分类">
                <Input value={draft.category} readOnly={readOnly} onChange={(e) => update({ category: e.target.value })} list="script-cats" />
                <datalist id="script-cats">
                  {["系统", "磁盘", "网络", "证书", "服务", "容器", "安全", "自定义"].map((c) => (
                    <option key={c} value={c} />
                  ))}
                </datalist>
              </Field>
              <Field label="解释器">
                <Select value={draft.interpreter} disabled={readOnly} onChange={(e) => update({ interpreter: e.target.value })}>
                  <option value="bash">bash</option>
                  <option value="sh">sh</option>
                  <option value="python3">python3</option>
                </Select>
              </Field>
              <label className="flex h-[34px] items-center gap-2 text-[12.5px] text-fg2">
                <Switch size="sm" checked={draft.readOnly} disabled={readOnly} onChange={(v) => update({ readOnly: v })} />
                只读脚本
              </label>
            </div>
          )}
          <Field label={isAdhoc ? "命令" : "脚本内容"} hint={!isAdhoc ? "参数以环境变量形式传入脚本；只读脚本由 AI 调用时无需审批" : "多行命令会作为一个 bash 脚本执行；非 root 用户启用 sudo 时自动提权"}>
            <CodeEditor value={draft.content} onChange={(v) => update({ content: v })} readOnly={readOnly} language={draft.interpreter} minHeight={isAdhoc ? 220 : 320} />
          </Field>
          {!isAdhoc && (
            <div>
              <div className="mb-2 flex items-center justify-between">
                <span className="text-[12.5px] font-medium text-fg2">参数</span>
                {!readOnly && (
                  <Button size="xs" variant="ghost" icon={Plus} onClick={() => update({ params: [...(draft.params ?? []), { name: "", label: "", default: "", required: false }] as Script["params"] })}>
                    添加参数
                  </Button>
                )}
              </div>
              {(draft.params ?? []).length === 0 ? (
                <div className="rounded-xl border border-dashed border-line2 px-4 py-3 text-[12px] text-muted">没有参数</div>
              ) : (
                <div className="space-y-2 overflow-x-auto pb-1">
                  {(draft.params ?? []).map((p, i) => (
                    <div key={i} className="grid min-w-[480px] grid-cols-[140px_1fr_1fr_auto_auto] items-center gap-2">
                      <Input mono placeholder="变量名" value={p.name} readOnly={readOnly} onChange={(e) => update({ params: draft.params.map((x, j) => (j === i ? { ...x, name: e.target.value.toUpperCase() } : x)) as Script["params"] })} />
                      <Input placeholder="显示名称" value={p.label} readOnly={readOnly} onChange={(e) => update({ params: draft.params.map((x, j) => (j === i ? { ...x, label: e.target.value } : x)) as Script["params"] })} />
                      <Input placeholder="默认值" value={p.default} readOnly={readOnly} onChange={(e) => update({ params: draft.params.map((x, j) => (j === i ? { ...x, default: e.target.value } : x)) as Script["params"] })} />
                      <label className="flex items-center gap-1.5 text-[12px] text-muted">
                        <Switch size="sm" checked={p.required} disabled={readOnly} onChange={(v) => update({ params: draft.params.map((x, j) => (j === i ? { ...x, required: v } : x)) as Script["params"] })} />
                        必填
                      </label>
                      {!readOnly && <IconButton icon={X} size="sm" onClick={() => update({ params: draft.params.filter((_, j) => j !== i) as Script["params"] })} />}
                    </div>
                  ))}
                </div>
              )}
            </div>
          )}
        </div>
      </div>

      {/* 执行面板 */}
      <div className="flex w-full shrink-0 flex-col border-t border-line bg-bg2/30 @3xl:h-[46%] @6xl:h-auto @6xl:w-[400px] @6xl:border-t-0 @6xl:border-l">
        <Tabs
          className="px-4 pt-2"
          value={panel}
          onChange={setPanel}
          tabs={[
            { key: "run", label: "执行", icon: Play },
            { key: "history", label: "执行历史", icon: History },
          ]}
        />
        {panel === "run" ? (
          <div className="flex min-h-0 flex-1 flex-col gap-4 overflow-y-auto p-4 *:shrink-0">
            <ServerPicker value={targets} onChange={setTargets} maxHeight={220} />
            {!isAdhoc && (draft.params ?? []).filter((p) => p.name).length > 0 && (
              <div className="space-y-2.5">
                {draft.params
                  .filter((p) => p.name)
                  .map((p) => (
                    <Field key={p.name} label={p.label || p.name} required={p.required}>
                      <Input mono value={values[p.name] ?? ""} placeholder={p.default} onChange={(e) => setValues((v) => ({ ...v, [p.name]: e.target.value }))} />
                    </Field>
                  ))}
              </div>
            )}
            {state.running ? (
              <Button variant="danger" icon={Square} onClick={() => state.runId && api.CancelRun(state.runId)}>
                取消执行
              </Button>
            ) : (
              <Button variant="primary" size="lg" icon={Play} onClick={run} disabled={!draft.content.trim()}>
                在 {targets.length} 台服务器上执行
              </Button>
            )}
            {state.runId ? (
              <RunOutputs serverIds={runTargets} state={state} names={names} className="min-h-[260px] flex-1" />
            ) : (
              <div className="rounded-xl border border-dashed border-line2 px-4 py-8 text-center text-[12.5px] text-muted">执行输出会实时显示在这里</div>
            )}
            {state.runId && !state.running && (
              <Button size="sm" variant="ghost" onClick={reset}>
                清空输出
              </Button>
            )}
          </div>
        ) : (
          <RunHistory />
        )}
      </div>
      </div>
    </div>
  );
}

function RunHistory() {
  const [runs, setRuns] = useState<RunRecord[]>([]);
  const [open, setOpen] = useState<RunRecord | null>(null);
  useEffect(() => {
    api.ListRuns(200).then((r) => setRuns(r ?? []));
  }, []);
  const kindLabel: Record<string, string> = { script: "脚本", command: "命令", cert: "证书", service: "服务", agent: "AI", key: "密钥" };
  return (
    <div className="min-h-0 flex-1 overflow-y-auto p-3">
      {runs.length === 0 && <Empty icon={History} title="还没有执行记录" />}
      {runs.map((r) => (
        <button key={r.id} type="button" onClick={async () => setOpen(await api.GetRun(r.id))} className="flex w-full items-center gap-2.5 rounded-[10px] px-2.5 py-2 text-left hover:bg-panel2">
          {r.status === "success" ? <CircleCheck size={15} className="shrink-0 text-ok" /> : <CircleX size={15} className={cn("shrink-0", r.status === "partial" ? "text-warn" : "text-bad")} />}
          <div className="min-w-0 flex-1">
            <div className="truncate text-[12.5px] text-fg">{r.title}</div>
            <div className="text-[11px] text-muted">
              {kindLabel[r.kind] ?? r.kind} · {timeAgo(r.startedAt)}
              {r.finishedAt > 0 && ` · ${formatMs(r.finishedAt - r.startedAt)}`}
            </div>
          </div>
        </button>
      ))}
      <Modal open={!!open} onClose={() => setOpen(null)} title={open?.title ?? ""} icon={FileCode} size="xl" description={open ? `${timeAgo(open.startedAt)} · ${open.results?.length ?? 0} 台服务器` : ""}>
        {open && (
          <div className="space-y-4">
            {open.command && <OutputBox text={open.command} className="max-h-[160px]" />}
            {(open.results ?? []).map((res) => (
              <div key={res.serverId}>
                <div className="mb-1.5 flex items-center gap-2 text-[13px]">
                  {!res.error && res.exitCode === 0 ? <CircleCheck size={14} className="text-ok" /> : <CircleX size={14} className="text-bad" />}
                  <span className="font-medium">{res.serverName}</span>
                  <span className="text-[12px] text-muted">
                    {res.error ? res.error : `退出码 ${res.exitCode}`} · {formatMs(res.durationMs)}
                  </span>
                </div>
                <OutputBox text={stripAnsi(res.output)} className="max-h-[260px]" />
              </div>
            ))}
          </div>
        )}
      </Modal>
    </div>
  );
}
