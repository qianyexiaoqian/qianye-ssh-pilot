import { useEffect, useState } from "react";
import { Archive, ArchiveRestore, BookOpen, Bot, Brain as BrainIcon, BrainCircuit, Check, ChevronDown, ChevronRight, Clock, Eye, FileCode, Lock, Pencil, Pin, PinOff, Plus, Sparkles, Trash2, User, X } from "lucide-react";
import { api, type LearnEvent, type MemoryView, type Skill, type SkillMeta } from "../lib/api";
import { cn, timeAgo } from "../lib/format";
import { confirm, toast } from "../lib/store";
import { LongMemoryPanel } from "../components/LongMemory";
import { Badge, Button, Card, Empty, Field, IconButton, Input, Meter, Modal, PageHeader, Segmented, Textarea } from "../components/ui";
import { Markdown } from "../components/widgets";
import { MemoryModeBadge, useMemoryIndex } from "./SettingsMemory";

const sourceLabel: Record<string, string> = { agent: "对话中", review: "后台复盘", user: "手动编辑", curator: "自动整理" };
const kindMeta: Record<string, { label: string; icon: typeof BrainIcon }> = {
  memory: { label: "核心记忆", icon: BrainIcon },
  user: { label: "用户画像", icon: User },
  longterm: { label: "长期记忆", icon: BrainCircuit },
  skill: { label: "技能", icon: BookOpen },
  script: { label: "脚本", icon: FileCode },
};
const actionLabel: Record<string, string> = { add: "新增", patch: "修改", remove: "删除", create: "创建", edit: "更新", delete: "删除", archive: "归档", restore: "恢复" };
const createdByLabel: Record<string, string> = { builtin: "内置", agent: "AI 沉淀", review: "复盘沉淀", user: "手动" };

export default function Brain() {
  const [mem, setMem] = useState<MemoryView[]>([]);
  const [target, setTarget] = useState<"memory" | "user">("memory");
  const [skills, setSkills] = useState<SkillMeta[]>([]);
  const [archived, setArchived] = useState<SkillMeta[]>([]);
  const [showArchived, setShowArchived] = useState(false);
  const [log, setLog] = useState<LearnEvent[]>([]);
  const [editing, setEditing] = useState<{ index: number; text: string } | null>(null);
  const [skill, setSkill] = useState<{ name: string; isNew?: boolean } | null>(null);
  const { status, reload: reloadIndex } = useMemoryIndex();

  const load = () => {
    api.GetMemory().then((m) => setMem(m ?? []));
    api.ListSkills().then((s) => setSkills(s ?? []));
    api.ListArchivedSkills().then((s) => setArchived(s ?? []));
    api.GetLearningLog(80).then((l) => setLog(l ?? []));
    void reloadIndex();
  };
  useEffect(load, []);

  const view = mem.find((m) => m.target === target);
  const saveEntries = async (entries: string[]) => {
    try {
      await api.SaveMemory(target, entries);
      setEditing(null);
      load();
    } catch (e) {
      toast.error("保存失败", e);
    }
  };
  const totalUses = skills.reduce((a, s) => a + s.uses, 0);
  const act = async (what: string, fn: () => Promise<unknown>) => {
    try {
      await fn();
      load();
    } catch (e) {
      toast.error(what + "失败", e);
    }
  };

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title="记忆与技能"
        icon={BrainIcon}
        description="AI 越用越了解你的环境：核心记忆常驻在每次对话里，长期记忆不限容量、按需想起，技能沉淀可复用的操作流程"
        actions={
          <Button variant="primary" icon={Plus} onClick={() => setSkill({ name: "", isNew: true })}>
            新建技能
          </Button>
        }
      />
      <div className="min-h-0 flex-1 overflow-y-auto px-4 @2xl:px-7 pb-8">
        <div className="grid grid-cols-1 gap-4 @2xl:grid-cols-2 @5xl:grid-cols-4">
          {mem.map((m) => {
            const pct = (m.used / Math.max(m.limit, 1)) * 100;
            return (
              <Card key={m.target} className="p-4">
                <div className="flex items-center justify-between gap-2 text-[12.5px] text-muted">
                  <span className="inline-flex min-w-0 items-center gap-1.5">
                    {m.target === "memory" ? <BrainIcon size={14} className="shrink-0" /> : <User size={14} className="shrink-0" />}
                    <span className="truncate">{m.target === "memory" ? "核心记忆 · 环境" : "核心记忆 · 用户画像"}</span>
                  </span>
                  <span className="shrink-0">
                    {m.used} / {m.limit} 字
                  </span>
                </div>
                <div className="mt-1.5 text-[24px] font-semibold">{m.entries.length} 条</div>
                <Meter className="mt-2" value={pct} tone={pct >= 90 ? "warn" : "accent"} />
                <div className="mt-1.5 text-[11.5px] text-muted">{pct >= 90 ? "接近上限，AI 会把不常用的移到长期记忆" : "每次对话都会带上，所以容量很小"}</div>
              </Card>
            );
          })}
          <Card className="p-4">
            <div className="flex items-center justify-between gap-2 text-[12.5px] text-muted">
              <span className="inline-flex items-center gap-1.5">
                <BrainCircuit size={14} /> 长期记忆
              </span>
              <span>不限容量</span>
            </div>
            <div className="mt-1.5 text-[24px] font-semibold">{status ? status.facts : "-"} 条</div>
            <div className="mt-2 flex flex-wrap gap-1.5">
              <MemoryModeBadge status={status} />
            </div>
            <div className="mt-1.5 text-[11.5px] text-muted">另有 {status?.episodes ?? 0} 段往期对话的要点，提问时按相关度想起</div>
          </Card>
          <Card className="p-4">
            <div className="flex items-center justify-between text-[12.5px] text-muted">
              <span className="inline-flex items-center gap-1.5">
                <BookOpen size={14} /> 技能库
              </span>
              <span>累计调用 {totalUses} 次</span>
            </div>
            <div className="mt-1.5 text-[24px] font-semibold">{skills.length} 个</div>
            <div className="mt-2 flex flex-wrap gap-1.5">
              <Badge icon={Lock}>内置 {skills.filter((s) => s.createdBy === "builtin").length}</Badge>
              <Badge tone="accent" icon={Bot}>
                AI 沉淀 {skills.filter((s) => s.createdBy === "agent" || s.createdBy === "review").length}
              </Badge>
              <Badge icon={User}>手动 {skills.filter((s) => s.createdBy === "user").length}</Badge>
            </div>
          </Card>
        </div>

        <div className="mt-5">
          <LongMemoryPanel status={status} onChanged={load} />
        </div>

        <div className="mt-5 grid grid-cols-1 gap-5 @4xl:grid-cols-[1.1fr_1fr]">
          {/* 核心记忆 */}
          <Card className="flex flex-col p-5">
            <div className="mb-3 flex flex-wrap items-center justify-between gap-2">
              <h3 className="text-[13.5px] font-semibold">核心记忆</h3>
              <div className="flex flex-wrap items-center gap-2">
                <Segmented
                  value={target}
                  onChange={(v) => {
                    setTarget(v);
                    setEditing(null);
                  }}
                  options={[
                    { value: "memory", label: "环境", icon: BrainIcon },
                    { value: "user", label: "用户画像", icon: User },
                  ]}
                />
                <Button size="sm" variant="soft" icon={Plus} onClick={() => setEditing({ index: -1, text: "" })}>
                  添加条目
                </Button>
              </div>
            </div>
            <p className="mb-3 text-[12px] text-muted">
              {target === "memory" ? "整个环境通用、几乎每次对话都用得上的约定。具体到某台服务器的事实请放到上面的长期记忆。" : "你的偏好、习惯，以及对助手工作方式的要求。"}新内容会在下一个对话中生效。
            </p>
            <div className="space-y-2">
              {editing?.index === -1 && (
                <div className="rounded-xl border border-accent/40 bg-bg2 p-2">
                  <Textarea autoFocus value={editing.text} onChange={(e) => setEditing({ ...editing, text: e.target.value })} placeholder={target === "memory" ? "例如：生产环境统一用 Nginx + systemd，改配置前先备份" : "例如：先给结论再给细节；重启生产服务前要先问我"} className="min-h-[70px] border-0 bg-transparent focus:ring-0" />
                  <div className="flex justify-end gap-1.5">
                    <Button size="xs" variant="ghost" onClick={() => setEditing(null)}>
                      取消
                    </Button>
                    <Button size="xs" variant="primary" disabled={!editing.text.trim()} onClick={() => saveEntries([...(view?.entries ?? []), editing.text])}>
                      保存
                    </Button>
                  </div>
                </div>
              )}
              {(view?.entries ?? []).length === 0 && editing?.index !== -1 && <Empty icon={BrainIcon} title="还没有核心记忆" description="与 AI 助手对话时，它会主动记录值得长期保存的信息。" className="py-8" />}
              {(view?.entries ?? []).map((e, i) =>
                editing?.index === i ? (
                  <div key={i} className="rounded-xl border border-accent/40 bg-bg2 p-2">
                    <Textarea autoFocus value={editing.text} onChange={(ev) => setEditing({ index: i, text: ev.target.value })} className="min-h-[70px] border-0 bg-transparent focus:ring-0" />
                    <div className="flex justify-end gap-1.5">
                      <Button size="xs" variant="ghost" icon={X} onClick={() => setEditing(null)}>
                        取消
                      </Button>
                      <Button size="xs" variant="primary" icon={Check} onClick={() => saveEntries(view!.entries.map((x, j) => (j === i ? editing.text : x)))}>
                        保存
                      </Button>
                    </div>
                  </div>
                ) : (
                  <div key={i} className="group flex items-start gap-3 rounded-xl border border-line bg-bg2/50 px-3.5 py-2.5">
                    <span className="mt-0.5 font-mono text-[11px] text-muted">{String(i + 1).padStart(2, "0")}</span>
                    <div className="selectable min-w-0 flex-1 text-[13px] leading-relaxed break-words whitespace-pre-wrap text-fg">{e}</div>
                    <div className="flex shrink-0 gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
                      <IconButton icon={Pencil} size="sm" onClick={() => setEditing({ index: i, text: e })} />
                      <IconButton
                        icon={Trash2}
                        size="sm"
                        tone="danger"
                        onClick={async () => {
                          if (await confirm({ title: "删除这条记忆？", message: e, danger: true, confirmText: "删除" })) void saveEntries(view!.entries.filter((_, j) => j !== i));
                        }}
                      />
                    </div>
                  </div>
                ),
              )}
            </div>
          </Card>

          {/* 技能 */}
          <Card className="p-5">
            <div className="mb-3 flex flex-wrap items-center justify-between gap-x-3 gap-y-1">
              <h3 className="text-[13.5px] font-semibold">技能库</h3>
              <span className="text-[12px] text-muted">SKILL.md · 兼容 agentskills.io</span>
            </div>
            <div className="space-y-2">
              {skills.map((s) => (
                <button key={s.name} type="button" onClick={() => setSkill({ name: s.name })} className="flex w-full items-start gap-3 rounded-xl border border-line bg-bg2/50 px-3.5 py-3 text-left transition-colors hover:border-line2 hover:bg-panel2">
                  <div className={cn("flex h-8 w-8 shrink-0 items-center justify-center rounded-lg", s.createdBy === "builtin" ? "bg-panel3 text-fg2" : "bg-accent/12 text-accent")}>
                    {s.createdBy === "builtin" ? <BookOpen size={15} /> : <Sparkles size={15} />}
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="flex flex-wrap items-center gap-x-2 gap-y-1">
                      <span className="truncate font-mono text-[12.5px] font-medium text-fg">{s.name}</span>
                      <span className="text-[11px] text-muted">v{s.version}</span>
                      {s.pinned && (
                        <Badge tone="accent" icon={Pin} title="已固定：不会被自动归档，AI 也不能删除它">
                          已固定
                        </Badge>
                      )}
                      {s.stale && !s.pinned && (
                        <Badge icon={Clock} title={s.createdBy === "agent" || s.createdBy === "review" ? "超过 30 天没有用到；满 90 天会自动归档（可以恢复）。想一直保留就把它固定" : "超过 30 天没有用到"}>
                          久未使用
                        </Badge>
                      )}
                    </div>
                    <div className="mt-0.5 line-clamp-2 text-[12px] text-muted">{s.description}</div>
                  </div>
                  <div className="shrink-0 text-right text-[11px] text-muted">
                    <div>{createdByLabel[s.createdBy] ?? s.createdBy}</div>
                    <div className="mt-0.5">使用 {s.uses} 次</div>
                  </div>
                </button>
              ))}
            </div>
            {archived.length > 0 && (
              <div className="mt-3 border-t border-line pt-3">
                <button type="button" onClick={() => setShowArchived((v) => !v)} className="inline-flex items-center gap-1.5 text-[12.5px] text-fg2 hover:text-fg">
                  {showArchived ? <ChevronDown size={14} /> : <ChevronRight size={14} />}
                  <Archive size={13} />
                  已归档 {archived.length} 个
                </button>
                {showArchived && (
                  <div className="mt-2 space-y-2">
                    <p className="text-[12px] text-muted">归档的技能不再出现在 AI 的技能索引里，随时可以恢复。</p>
                    {archived.map((s) => (
                      <div key={s.name} className="flex items-center gap-3 rounded-xl border border-line bg-bg2/40 px-3.5 py-2.5">
                        <div className="min-w-0 flex-1">
                          <div className="truncate font-mono text-[12.5px] text-fg2">{s.name}</div>
                          <div className="truncate text-[12px] text-muted">
                            {s.description} · 归档于 {timeAgo(s.archivedAt)}
                          </div>
                        </div>
                        <Button size="xs" variant="soft" icon={ArchiveRestore} onClick={() => act("恢复", () => api.RestoreSkill(s.name))}>
                          恢复
                        </Button>
                        <IconButton
                          icon={Trash2}
                          size="sm"
                          tone="danger"
                          tip="彻底删除"
                          onClick={async () => {
                            if (await confirm({ title: `彻底删除已归档的技能 ${s.name}？`, message: "删除后不能恢复。", danger: true, confirmText: "删除" })) void act("删除", () => api.DeleteArchivedSkill(s.name));
                          }}
                        />
                      </div>
                    ))}
                  </div>
                )}
              </div>
            )}
          </Card>
        </div>

        {/* 学习日志 */}
        <Card className="mt-5 p-5">
          <h3 className="mb-3 text-[13.5px] font-semibold">学习日志</h3>
          {log.length === 0 ? (
            <div className="py-6 text-center text-[12.5px] text-muted">还没有学习记录</div>
          ) : (
            <div className="relative space-y-3 pl-5">
              <div className="absolute top-1 bottom-1 left-[7px] w-px bg-line2" />
              {log.map((l, i) => {
                const k = kindMeta[l.kind] ?? kindMeta.memory;
                return (
                  <div key={i} className="relative">
                    <div className="absolute top-1 -left-5 flex h-[15px] w-[15px] items-center justify-center rounded-full border border-accent/40 bg-panel">
                      <span className="h-1.5 w-1.5 rounded-full bg-accent" />
                    </div>
                    <div className="flex flex-wrap items-center gap-2 text-[12px]">
                      <Badge tone="accent" icon={k.icon}>
                        {k.label}
                      </Badge>
                      <span className="text-fg2">
                        {l.kind === "longterm" && l.action === "archive" ? "由核心记忆移入" : (actionLabel[l.action] ?? l.action)}
                        {(l.kind === "skill" || l.kind === "script") && ` · ${l.target}`}
                      </span>
                      <span className="inline-flex items-center gap-1 text-muted">
                        <Clock size={11} />
                        {timeAgo(l.time)} · {sourceLabel[l.source] ?? l.source}
                      </span>
                    </div>
                    {l.summary && <div className="mt-1 text-[12.5px] break-words text-muted">{l.summary}</div>}
                  </div>
                );
              })}
            </div>
          )}
        </Card>
      </div>
      <SkillModal target={skill} onClose={() => setSkill(null)} onChanged={load} />
    </div>
  );
}

const skillTemplate = `---
name: my-skill
description: 一句话说明这个技能解决什么问题
---

# 技能标题

## 适用场景

## 步骤
1.

## 注意事项

## 验证
`;

function SkillModal({ target, onClose, onChanged }: { target: { name: string; isNew?: boolean } | null; onClose: () => void; onChanged: () => void }) {
  const [skill, setSkill] = useState<Skill | null>(null);
  const [mode, setMode] = useState<"view" | "edit">("view");
  const [name, setName] = useState("");
  const [content, setContent] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (!target) return;
    setSkill(null);
    if (target.isNew) {
      setMode("edit");
      setName("");
      setContent(skillTemplate);
    } else {
      setMode("view");
      setName(target.name);
      api.GetSkill(target.name).then((s) => {
        setSkill(s);
        setContent(s.content);
      });
    }
  }, [target]);
  const run = async (what: string, fn: () => Promise<unknown>, close = true) => {
    try {
      await fn();
      onChanged();
      if (close) onClose();
    } catch (e) {
      toast.error(what + "失败", e);
    }
  };
  return (
    <Modal
      open={!!target}
      onClose={onClose}
      title={target?.isNew ? "新建技能" : target?.name ?? ""}
      description={skill ? `${skill.description} · v${skill.version} · 更新于 ${skill.updatedAt || "-"}` : "技能是可复用的操作流程，AI 会在相关任务中按需读取"}
      icon={BookOpen}
      size="xl"
      footer={
        <>
          {!target?.isNew && (
            <div className="mr-auto flex flex-wrap items-center gap-1">
              <Button
                variant="ghost"
                icon={Trash2}
                className="text-bad"
                onClick={async () => {
                  if (!target || !(await confirm({ title: `删除技能 ${target.name}？`, danger: true, confirmText: "删除" }))) return;
                  void run("删除", () => api.DeleteSkill(target.name));
                }}
              >
                删除
              </Button>
              <Button variant="ghost" icon={Archive} onClick={() => target && void run("归档", () => api.ArchiveSkill(target.name))}>
                归档
              </Button>
              {skill && (
                <Button
                  variant="ghost"
                  icon={skill.pinned ? PinOff : Pin}
                  onClick={() =>
                    void run(
                      "操作",
                      async () => {
                        await api.PinSkill(skill.name, !skill.pinned);
                        setSkill({ ...skill, pinned: !skill.pinned });
                        toast.ok(skill.pinned ? "已取消固定" : "已固定", skill.pinned ? undefined : "固定的技能不会被自动归档，AI 也不能删除它");
                      },
                      false,
                    )
                  }
                >
                  {skill.pinned ? "取消固定" : "固定"}
                </Button>
              )}
            </div>
          )}
          {mode === "view" ? (
            <Button icon={Pencil} onClick={() => setMode("edit")}>
              编辑
            </Button>
          ) : (
            <>
              {!target?.isNew && (
                <Button variant="ghost" icon={Eye} onClick={() => setMode("view")}>
                  预览
                </Button>
              )}
              <Button
                variant="primary"
                loading={busy}
                onClick={async () => {
                  setBusy(true);
                  try {
                    await api.SaveSkill(target?.isNew ? name : target!.name, content);
                    toast.ok("技能已保存");
                    onChanged();
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
          )}
        </>
      }
    >
      {mode === "edit" ? (
        <div className="space-y-3">
          {target?.isNew && (
            <Field label="技能名" hint="小写字母、数字和连字符，例如 mysql-slow-query">
              <Input mono value={name} onChange={(e) => setName(e.target.value)} />
            </Field>
          )}
          <Textarea mono value={content} onChange={(e) => setContent(e.target.value)} className="min-h-[420px]" />
        </div>
      ) : skill ? (
        <Markdown text={skill.body} />
      ) : (
        <div className="py-10 text-center text-muted">加载中…</div>
      )}
    </Modal>
  );
}
