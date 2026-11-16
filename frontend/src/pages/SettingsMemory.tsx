import { useEffect, useState } from "react";
import { BrainCircuit, CircleCheck, Eye, EyeOff, Info, Plug, RefreshCw, TriangleAlert } from "lucide-react";
import { api, on, type EmbedTest, type IndexStatus, type Settings } from "../lib/api";
import { embedPresetOf, embedPresets } from "../lib/embed";
import { cn } from "../lib/format";
import { toast, useApp } from "../lib/store";
import { Badge, Button, Callout, Card, Field, IconButton, Input, Meter, Select, Switch } from "../components/ui";

/** 订阅长期记忆的检索方式与索引进度 */
export function useMemoryIndex() {
  const [status, setStatus] = useState<IndexStatus | null>(null);
  useEffect(() => {
    const load = () => api.GetMemoryIndex().then(setStatus);
    void load();
    const off = on<IndexStatus>("memory:index", setStatus);
    return off;
  }, []);
  return { status, reload: () => api.GetMemoryIndex().then(setStatus) };
}

export function MemoryModeBadge({ status }: { status: IndexStatus | null }) {
  if (!status) return null;
  return status.mode === "semantic" ? (
    <Badge tone="accent" icon={BrainCircuit} title="按意思匹配：换一种说法也能找到">
      语义向量 · {status.model}
      {status.dims > 0 && ` · ${status.dims} 维`}
    </Badge>
  ) : (
    <Badge title="只匹配字面相同的词；在「设置 → 长期记忆」配置向量模型后可以按意思匹配">关键词匹配</Badge>
  );
}

interface Props {
  s: Settings;
  set: (fn: (x: Settings) => void) => void;
  embedKey: string;
  setEmbedKey: (v: string) => void;
  clearEmbedKey: boolean;
  setClearEmbedKey: (v: boolean) => void;
  dirty: boolean;
  /** 保存全部设置，成功返回 true */
  save: () => Promise<boolean>;
}

/** 设置页的「长期记忆」：向量模型（单独配置的 OpenAI 兼容接口）、自动召回和索引状态 */
export function MemorySettings({ s, set, embedKey, setEmbedKey, clearEmbedKey, setClearEmbedKey, dirty, save }: Props) {
  const { status, reload } = useMemoryIndex();
  // 已保存的接口地址：换到另一个服务商时，原来保存的 Key 不应该发给新的服务商
  const savedUrl = useApp((st) => st.settings?.embed.baseUrl ?? "");
  const [showKey, setShowKey] = useState(false);
  const [testing, setTesting] = useState(false);
  const [result, setResult] = useState<{ ok: true; test: EmbedTest } | { ok: false; text: string } | null>(null);
  const e = s.embed;
  const preset = embedPresetOf(e.preset);
  const model = preset.models.find((m) => m.id === e.model);
  const hasKey = e.hasApiKey && !clearEmbedKey;
  const configured = !!e.baseUrl.trim() && !!e.model.trim() && (hasKey || !!embedKey.trim() || !!preset.local || /\/\/(localhost|127\.0\.0\.1)/.test(e.baseUrl));

  const test = async () => {
    if (dirty && !(await save())) return;
    setTesting(true);
    setResult(null);
    try {
      setResult({ ok: true, test: await api.TestEmbedding() });
    } catch (err) {
      setResult({ ok: false, text: String(err) });
    } finally {
      setTesting(false);
      void reload();
    }
  };
  const rebuild = async () => {
    try {
      await api.ReindexMemory();
      toast.info("正在重建索引", "已有的长期记忆会重新计算向量，期间仍然可以正常使用");
      void reload();
    } catch (err) {
      toast.error("无法重建索引", err);
    }
  };

  const semantic = status?.mode === "semantic";
  const pending = semantic && status.indexed < status.total;
  return (
    <>
      <Card className="p-5">
        <div className="flex flex-wrap items-center gap-x-3 gap-y-1.5">
          <div className="text-[13.5px] font-semibold">长期记忆</div>
          <MemoryModeBadge status={status} />
          {status && (
            <span className="text-[12px] text-muted">
              记忆 {status.facts} 条 · 往期对话 {status.episodes} 条
            </span>
          )}
          {semantic && (
            <Button size="sm" variant="ghost" icon={RefreshCw} className="ml-auto" disabled={status.indexing} onClick={rebuild}>
              重建索引
            </Button>
          )}
        </div>
        <p className="mt-2 text-[12px] leading-relaxed text-muted">
          长期记忆没有容量上限：AI 把具体到某台服务器的事实存在这里，每一轮对话的“问题 → 操作 → 结论”也会自动存档。你提问时，程序按相关度找出几条附给 AI，它就能想起“上次是怎么解决的”。
          配置向量模型后按意思匹配——问“磁盘满了”能找到“/data 占用过高”；不配置时使用内置的关键词匹配，只能找到字面相同的词，不需要联网。
        </p>
        {semantic && (pending || status.indexing) && (
          <div className="mt-3">
            <div className="mb-1 flex justify-between text-[12px] text-muted">
              <span>{status.indexing ? "正在建立索引…" : "有内容还没有建立索引"}</span>
              <span>
                {status.indexed} / {status.total}
              </span>
            </div>
            <Meter value={(status.indexed / Math.max(status.total, 1)) * 100} />
          </div>
        )}
        {status?.error && (
          <Callout tone="warn" title="向量接口暂时用不了，已经自动改用关键词匹配" className="mt-3">
            {status.error}
          </Callout>
        )}
      </Card>

      <Card className="p-5">
        <div className="mb-1 text-[13.5px] font-semibold">向量模型</div>
        <p className="mb-3 text-[12px] leading-relaxed text-muted">与对话用的 AI 模型分开配置，接口是 OpenAI 兼容的 /embeddings。向量模型很便宜：只在保存记忆和每次提问时各调用一次，记忆的内容和你提问的文字会发给这个接口用来计算向量（带有密码、密钥的消息不会发）。</p>
        <div className="grid grid-cols-2 gap-2.5 @2xl:grid-cols-4">
          {embedPresets.map((p) => (
            <button
              key={p.id}
              type="button"
              onClick={() => {
                set((x) => {
                  x.embed.preset = p.id;
                  x.embed.baseUrl = p.baseUrl;
                  x.embed.model = p.models[0]?.id ?? "";
                  x.embed.dimensions = 0;
                });
                if (e.hasApiKey) setClearEmbedKey(p.baseUrl !== savedUrl);
              }}
              className={cn("relative rounded-xl border px-3 py-3 text-left transition-all", e.preset === p.id ? "border-accent/60 bg-accent/10 shadow-[var(--glow)]" : "border-line2 hover:bg-panel2")}
            >
              <div className="text-[13px] font-semibold">{p.name}</div>
              <div className="mt-0.5 text-[11px] text-muted">{p.note}</div>
              {e.preset === p.id && <CircleCheck size={14} className="absolute top-2.5 right-2.5 text-accent" />}
            </button>
          ))}
        </div>
      </Card>

      <Card className="space-y-4 p-5">
        <Field label="接口地址（Base URL）" hint={preset.urlHint ?? "OpenAI 兼容接口地址，会自动补全 /embeddings"}>
          <Input mono value={e.baseUrl} onChange={(ev) => set((x) => (x.embed.baseUrl = ev.target.value))} placeholder="https://..." />
        </Field>
        <Field
          label="API Key"
          hint={
            hasKey
              ? "已保存（使用 Windows 账户加密），留空表示不修改"
              : preset.local
                ? "本地服务无需填写"
                : e.hasApiKey && e.baseUrl !== savedUrl
                  ? "换了服务商：原来保存的 Key 不会发给新的服务商，请填写新的 Key"
                  : "仅保存在本机；与对话模型的 Key 互不影响"
          }
          action={
            hasKey && (
              <button type="button" className="text-[12px] text-bad hover:underline" onClick={() => setClearEmbedKey(true)}>
                清除已保存的 Key
              </button>
            )
          }
        >
          <Input
            mono
            type={showKey ? "text" : "password"}
            value={embedKey}
            onChange={(ev) => setEmbedKey(ev.target.value)}
            placeholder={hasKey ? "••••••••••••（已保存）" : "sk-..."}
            right={<IconButton size="sm" icon={showKey ? EyeOff : Eye} onClick={() => setShowKey((v) => !v)} />}
          />
        </Field>
        <div className="grid grid-cols-1 gap-4 @xl:grid-cols-2">
          <Field label="模型" hint={preset.modelHint ?? "可从列表选择或直接输入模型名称"}>
            <Input
              mono
              value={e.model}
              onChange={(ev) =>
                set((x) => {
                  x.embed.model = ev.target.value;
                  // 换了模型后，原来选的维度不一定还能用
                  const next = preset.models.find((m) => m.id === ev.target.value);
                  if (x.embed.dimensions && !next?.dims?.includes(x.embed.dimensions)) x.embed.dimensions = 0;
                })
              }
              list="embed-model-list"
              placeholder="模型名称"
            />
            <datalist id="embed-model-list">
              {preset.models.map((m) => (
                <option key={m.id} value={m.id} />
              ))}
            </datalist>
          </Field>
          <Field label="向量维度" hint="维度越高越精细，占用的磁盘也越多；一般保持模型默认即可。修改模型或维度后索引会自动重建">
            {model?.dims ? (
              <Select value={String(e.dimensions || 0)} onChange={(ev) => set((x) => (x.embed.dimensions = Number(ev.target.value)))}>
                <option value="0">模型默认</option>
                {model.dims.map((d) => (
                  <option key={d} value={d}>
                    {d}
                  </option>
                ))}
              </Select>
            ) : (
              <Input mono type="number" value={e.dimensions || ""} onChange={(ev) => set((x) => (x.embed.dimensions = Math.max(0, Number(ev.target.value) || 0)))} placeholder="模型默认" />
            )}
          </Field>
        </div>
        <div className="flex flex-wrap items-center gap-x-3 gap-y-2 border-t border-line pt-4">
          <Button icon={Plug} loading={testing} disabled={!configured} onClick={test}>
            {dirty ? "保存并测试" : "测试连接"}
          </Button>
          {!configured && <span className="text-[12.5px] text-muted">填写 API Key 后启用；没有填写时使用内置的关键词匹配</span>}
          {result &&
            (result.ok ? (
              <span className={cn("inline-flex min-w-0 items-center gap-1.5 text-[12.5px]", result.test.warning ? "text-warn" : "text-ok")}>
                {result.test.warning ? <TriangleAlert size={14} className="shrink-0" /> : <CircleCheck size={14} className="shrink-0" />}
                <span>
                  {result.test.warning ||
                    `${result.test.model} · ${result.test.dims} 维 · ${result.test.latencyMs} ms · 区分度：相关 ${result.test.related.toFixed(2)} / 无关 ${result.test.unrelated.toFixed(2)}`}
                </span>
              </span>
            ) : (
              <span className="inline-flex min-w-0 items-center gap-1.5 text-[12.5px] text-bad">
                <Info size={14} className="shrink-0" />
                <span className="break-all">{result.text}</span>
              </span>
            ))}
        </div>
      </Card>

      <Card className="space-y-4 p-5">
        <div className="flex items-center justify-between gap-4">
          <div>
            <div className="text-[13px] font-medium">提问时自动带上相关的记忆</div>
            <div className="text-[12px] text-muted">每次提问前，找出相关的长期记忆和往期对话要点附给 AI；对话里可以点开「已参考 N 条长期记忆」查看具体是哪几条</div>
          </div>
          <Switch checked={!e.recallOff} onChange={(v) => set((x) => (x.embed.recallOff = !v))} />
        </div>
        {!e.recallOff && (
          <div className="grid grid-cols-1 gap-4 @xl:grid-cols-2">
            <Field label="每次最多带上（条）" hint="带得越多越不容易漏，但也更占用对话的上下文">
              <Input mono type="number" value={e.recallCount} onChange={(ev) => set((x) => (x.embed.recallCount = Number(ev.target.value)))} />
            </Field>
            <Field
              label="相关度下限"
              hint={`留空表示自动：不同模型的相似度分布不一样，程序会先量出这个模型的分布再定下限${status && status.threshold > 0 ? `（当前为 ${status.threshold.toFixed(2)}）` : ""}`}
            >
              <Input mono type="number" step="0.05" min="0" max="0.99" value={e.minScore || ""} onChange={(ev) => set((x) => (x.embed.minScore = Math.max(0, Number(ev.target.value) || 0)))} placeholder="自动" />
            </Field>
          </div>
        )}
      </Card>
    </>
  );
}
