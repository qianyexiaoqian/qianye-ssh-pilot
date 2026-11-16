import { useCallback, useEffect, useRef, useState } from "react";
import { BrainCircuit, Clock, History, Pencil, Plus, Search, Server, Target, Trash2 } from "lucide-react";
import { api, type IndexStatus, type LongItem } from "../lib/api";
import { formatDate, timeAgo } from "../lib/format";
import { confirm, toast, useApp } from "../lib/store";
import { MemoryModeBadge } from "../pages/SettingsMemory";
import { Badge, Button, Card, Empty, Field, IconButton, Input, Modal, Segmented, Spinner, Textarea } from "./ui";
import { ServerPicker } from "./widgets";

const MAX_CHARS = 600;
const PAGE = 60;
const sourceLabel: Record<string, string> = { agent: "AI 在对话中记录", review: "后台复盘", user: "手动添加", archive: "由核心记忆移入" };

type Kind = "fact" | "episode";

/** 「记忆与技能」页里的长期记忆：不限容量、按相关度检索的事实，以及自动存档的往期对话要点 */
export function LongMemoryPanel({ status, onChanged }: { status: IndexStatus | null; onChanged: () => void }) {
  const go = useApp((s) => s.go);
  const [kind, setKind] = useState<Kind>("fact");
  const [query, setQuery] = useState("");
  const [items, setItems] = useState<LongItem[] | null>(null);
  const [shown, setShown] = useState(PAGE);
  const [editing, setEditing] = useState<LongItem | "new" | null>(null);
  const seq = useRef(0);

  const load = useCallback(async (k: Kind, q: string) => {
    const mine = ++seq.current;
    try {
      const list = (await api.ListLongMemory(k, q)) ?? [];
      // 输入得快时，只采用最后一次搜索的结果
      if (mine === seq.current) setItems(list);
    } catch (e) {
      if (mine === seq.current) setItems([]);
      toast.error("读取长期记忆失败", e);
    }
  }, []);
  useEffect(() => {
    setShown(PAGE);
    const t = setTimeout(() => void load(kind, query), query ? 300 : 0);
    return () => clearTimeout(t);
  }, [kind, query, load]);

  const changed = () => {
    void load(kind, query);
    onChanged();
  };
  const remove = async (it: LongItem) => {
    if (!(await confirm({ title: it.kind === "episode" ? "删除这段往期对话的要点？" : "删除这条长期记忆？", message: it.text, danger: true, confirmText: "删除" }))) return;
    try {
      await api.DeleteLongMemory(it.id);
      changed();
    } catch (e) {
      toast.error("删除失败", e);
    }
  };

  const searching = !!query.trim();
  return (
    <Card className="p-5">
      <div className="flex flex-wrap items-center gap-x-3 gap-y-2">
        <h3 className="inline-flex items-center gap-1.5 text-[13.5px] font-semibold">
          <BrainCircuit size={15} className="text-accent" /> 长期记忆
        </h3>
        <MemoryModeBadge status={status} />
        {status?.mode === "lexical" && (
          <button type="button" className="text-[12px] text-accent hover:underline" onClick={() => go("settings", { tab: "memory" })}>
            配置向量模型，按意思匹配
          </button>
        )}
        <Button size="sm" variant="soft" icon={Plus} className="ml-auto" onClick={() => setEditing("new")}>
          添加记忆
        </Button>
      </div>
      <p className="mt-2 text-[12px] leading-relaxed text-muted">
        没有容量上限，平时不占用对话的上下文：提问时程序自动找出相关的几条附给 AI。具体到某台服务器的事实放在这里，并标明是哪台服务器；每一轮对话的“问题 → 操作 → 结论”也会自动存档。
      </p>
      <div className="mt-3 flex flex-wrap items-center gap-2">
        <Segmented
          value={kind}
          onChange={(v) => {
            setItems(null);
            setKind(v);
          }}
          options={[
            { value: "fact", label: `记忆${status ? ` ${status.facts}` : ""}`, icon: BrainCircuit },
            { value: "episode", label: `往期对话${status ? ` ${status.episodes}` : ""}`, icon: History },
          ]}
        />
        <div className="min-w-[180px] flex-1">
          <Input icon={Search} value={query} onChange={(e) => setQuery(e.target.value)} placeholder={kind === "fact" ? "搜索记忆，例如：nginx 证书放在哪" : "搜索往期对话，例如：上次 502 是怎么解决的"} />
        </div>
      </div>

      <div className="mt-3 space-y-2">
        {items === null && (
          <div className="flex justify-center py-8">
            <Spinner />
          </div>
        )}
        {items?.length === 0 &&
          (searching ? (
            <div className="py-8 text-center text-[12.5px] text-muted">没有找到相关的内容{status?.mode === "lexical" && "（当前是关键词匹配，需要包含相同的词）"}</div>
          ) : kind === "fact" ? (
            <Empty icon={BrainCircuit} title="还没有长期记忆" description="AI 在对话中了解到某台服务器的角色、路径、已知问题时会记在这里；你也可以自己添加。" className="py-8" />
          ) : (
            <Empty icon={History} title="还没有往期对话的要点" description="和 AI 完成一次有结论的对话后，这里会自动出现它的“问题 → 操作 → 结论”。" className="py-8" />
          ))}
        {(items ?? []).slice(0, shown).map((it) => (
          <div key={it.id} className="group flex items-start gap-3 rounded-xl border border-line bg-bg2/50 px-3.5 py-2.5">
            <div className="min-w-0 flex-1">
              {it.kind === "episode" && (
                <div className="mb-1 flex flex-wrap items-center gap-x-2 text-[12px]">
                  <span className="truncate font-medium text-fg">{it.title || "对话"}</span>
                  <span className="text-muted">{formatDate(it.createdAt, true)}</span>
                </div>
              )}
              <div className="selectable text-[13px] leading-relaxed break-words whitespace-pre-wrap text-fg">{it.text}</div>
              <div className="mt-1.5 flex flex-wrap items-center gap-x-2.5 gap-y-1 text-[11.5px] text-muted">
                {it.serverNames.length > 0 ? (
                  it.serverNames.map((n) => (
                    <Badge key={n} icon={Server}>
                      {n}
                    </Badge>
                  ))
                ) : (
                  it.kind === "fact" && <Badge>通用</Badge>
                )}
                {searching && (
                  <span className="inline-flex items-center gap-1 text-accent">
                    <Target size={11} />
                    相关度 {it.score.toFixed(2)}
                  </span>
                )}
                {it.kind === "fact" && <span>{sourceLabel[it.source] ?? it.source}</span>}
                {it.kind === "fact" && (
                  <span className="inline-flex items-center gap-1">
                    <Clock size={11} />
                    {timeAgo(it.updatedAt)}
                  </span>
                )}
                <span>{it.hits > 0 ? `被想起 ${it.hits} 次` : "还没有被用到"}</span>
              </div>
            </div>
            <div className="flex shrink-0 gap-0.5 opacity-0 transition-opacity group-hover:opacity-100 focus-within:opacity-100">
              {it.kind === "fact" && <IconButton icon={Pencil} size="sm" tip="编辑" onClick={() => setEditing(it)} />}
              <IconButton icon={Trash2} size="sm" tone="danger" tip="删除" onClick={() => void remove(it)} />
            </div>
          </div>
        ))}
        {(items?.length ?? 0) > shown && (
          <div className="flex justify-center pt-1">
            <Button size="sm" variant="ghost" onClick={() => setShown((n) => n + PAGE)}>
              再显示 {Math.min(PAGE, (items?.length ?? 0) - shown)} 条（共 {items?.length} 条）
            </Button>
          </div>
        )}
      </div>
      <LongMemoryEditor target={editing} onClose={() => setEditing(null)} onSaved={changed} />
    </Card>
  );
}

function LongMemoryEditor({ target, onClose, onSaved }: { target: LongItem | "new" | null; onClose: () => void; onSaved: () => void }) {
  const [text, setText] = useState("");
  const [servers, setServers] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const isNew = target === "new";
  useEffect(() => {
    if (!target) return;
    setText(isNew ? "" : target.text);
    setServers(isNew ? [] : target.servers);
  }, [target, isNew]);
  const length = [...text.trim()].length;
  return (
    <Modal
      open={!!target}
      onClose={onClose}
      title={isNew ? "添加长期记忆" : "编辑长期记忆"}
      description="写成今后仍然成立的一两句话；不要写密码和密钥"
      icon={BrainCircuit}
      size="md"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button
            variant="primary"
            loading={busy}
            disabled={!length || length > MAX_CHARS}
            onClick={async () => {
              if (!target) return;
              setBusy(true);
              try {
                await api.SaveLongMemory(isNew ? "" : target.id, text, servers);
                toast.ok(isNew ? "已添加到长期记忆" : "长期记忆已更新");
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
        <Field label="内容" hint={`${length} / ${MAX_CHARS} 字`} error={length > MAX_CHARS ? `超过 ${MAX_CHARS} 字了：请拆成几条，或只保留结论` : undefined}>
          <Textarea autoFocus value={text} onChange={(e) => setText(e.target.value)} placeholder="例如：Nginx 证书统一放在 /etc/nginx/ssl，由 certbot 续期，续期后需要 reload nginx" className="min-h-[110px]" />
        </Field>
        <Field label="这条记忆是关于哪些服务器的" hint="在这些服务器的终端里提问、或者问题里提到它们时，这条记忆会优先被想起。对所有服务器都适用的知识不用选。">
          <ServerPicker value={servers} onChange={setServers} maxHeight={200} />
        </Field>
      </div>
    </Modal>
  );
}
