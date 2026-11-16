import { useEffect, useState } from "react";
import { Bot, BrainCircuit, CircleCheck, Database, Download, Eye, EyeOff, FolderOpen, Info, Monitor, Plug, RotateCcw, Save, Settings as SettingsIcon, Shield, SquareTerminal, Upload, Zap } from "lucide-react";
import { api, type Settings } from "../lib/api";
import { cn } from "../lib/format";
import { comboOf, DEFAULT_KEYS, hotkeyProblem, type HotkeyAction } from "../lib/hotkey";
import { confirm, toast, useApp } from "../lib/store";
import { Badge, Button, Callout, Card, Field, IconButton, Input, Modal, PageHeader, Segmented, Switch, Tabs } from "../components/ui";
import { AUTHOR, Logo, PRODUCT } from "../components/shell";
import { termThemes } from "../components/XTermView";
import { INTERVALS } from "./Monitor";
import { MemorySettings } from "./SettingsMemory";

interface Preset {
  id: string;
  name: string;
  provider: "anthropic" | "openai";
  baseUrl: string;
  models: string[];
  note: string;
}

const presets: Preset[] = [
  { id: "deepseek", name: "DeepSeek", provider: "openai", baseUrl: "https://api.deepseek.com", models: ["deepseek-v4-pro", "deepseek-flash"], note: "DeepSeek 官方接口（默认）" },
  { id: "anthropic", name: "Claude", provider: "anthropic", baseUrl: "", models: ["claude-opus-5-5", "claude-sonnet-5-5", "claude-haiku-5-5", "claude-fable-5-1"], note: "Anthropic 官方 API" },
  { id: "qwen", name: "通义千问", provider: "openai", baseUrl: "https://dashscope.aliyuncs.com/compatible-mode/v1", models: ["qwen-max", "qwen-plus", "qwen-turbo"], note: "阿里云百炼兼容模式" },
  { id: "kimi", name: "Kimi", provider: "openai", baseUrl: "https://api.moonshot.cn/v1", models: ["kimi-latest", "moonshot-v1-32k"], note: "Moonshot AI" },
  { id: "glm", name: "智谱 GLM", provider: "openai", baseUrl: "https://open.bigmodel.cn/api/paas/v4", models: ["glm-4-plus", "glm-4-air"], note: "智谱开放平台" },
  { id: "openai", name: "OpenAI", provider: "openai", baseUrl: "https://api.openai.com/v1", models: [], note: "OpenAI 官方" },
  { id: "ollama", name: "Ollama", provider: "openai", baseUrl: "http://localhost:11434/v1", models: ["qwen2.5:14b", "llama3.1:8b"], note: "本地模型，无需 API Key" },
  { id: "custom", name: "自定义", provider: "openai", baseUrl: "", models: [], note: "任意 OpenAI 兼容接口" },
];

type Tab = "ai" | "memory" | "agent" | "terminal" | "general" | "data" | "about";

const hotkeys: { field: keyof typeof DEFAULT_KEYS; action: HotkeyAction; label: string; desc: string }[] = [
  { field: "keyCopy", action: "copy", label: "复制", desc: "复制选中的文字" },
  { field: "keyPaste", action: "paste", label: "粘贴", desc: "把剪贴板的内容粘贴到终端" },
  { field: "keyFind", action: "find", label: "查找", desc: "在终端的输出里查找文字" },
];

/** 点一下，然后直接按下想用的组合键 */
function HotkeyInput({ value, action, others, onChange }: { value: string; action: HotkeyAction; others: string[]; onChange: (v: string) => void }) {
  const [recording, setRecording] = useState(false);
  const [problem, setProblem] = useState("");
  return (
    <div className="flex flex-col items-end gap-1">
      <button
        type="button"
        onClick={() => {
          setRecording(true);
          setProblem("");
        }}
        onBlur={() => setRecording(false)}
        onKeyDown={(e) => {
          if (!recording) return;
          e.preventDefault();
          e.stopPropagation();
          if (e.key === "Escape") return setRecording(false);
          const combo = comboOf(e.nativeEvent);
          if (!combo) return;
          const bad = hotkeyProblem(combo, action) ?? (others.includes(combo) ? "已经被另一个操作占用" : null);
          if (bad) return setProblem(`${combo}：${bad}`);
          setProblem("");
          setRecording(false);
          onChange(combo);
        }}
        className={cn(
          "flex h-[34px] min-w-[150px] items-center justify-center rounded-[10px] border px-3 font-mono text-[12.5px] transition-colors outline-none",
          recording ? "border-accent/70 bg-accent/10 text-accent shadow-[var(--glow)]" : "border-line2 bg-bg2 text-fg hover:border-accent/40",
        )}
      >
        {recording ? "请按下组合键…" : value}
      </button>
      {problem && <span className="max-w-[260px] text-right text-[11.5px] text-bad">{problem}</span>}
    </div>
  );
}

export default function SettingsPage() {
  const { params, loadSettings, info, theme, setTheme, settings: savedSettings } = useApp();
  const [tab, setTab] = useState<Tab>((params.tab as Tab) || "ai");
  const [s, setS] = useState<Settings | null>(null);
  const [apiKey, setApiKey] = useState("");
  const [showKey, setShowKey] = useState(false);
  const [clearKey, setClearKey] = useState(false);
  // 向量模型的 API Key 与对话模型的分开保存
  const [embedKey, setEmbedKey] = useState("");
  const [clearEmbedKey, setClearEmbedKey] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [saving, setSaving] = useState(false);
  const [testing, setTesting] = useState(false);
  const [testResult, setTestResult] = useState<{ ok: boolean; text: string } | null>(null);
  const [backup, setBackup] = useState<"export" | "import" | null>(null);

  useEffect(() => {
    api.GetSettings().then(setS);
  }, []);
  useEffect(() => {
    if (params.tab) setTab(params.tab as Tab);
  }, [params.tab]);

  if (!s) return null;
  const set = (fn: (x: Settings) => void) => {
    const next = structuredClone(s);
    fn(next);
    setS(next);
    setDirty(true);
  };
  const preset = presets.find((p) => p.id === s.ai.preset) ?? presets[presets.length - 1];

  const save = async () => {
    setSaving(true);
    try {
      let saved = await api.SaveSettings(s, apiKey, clearKey);
      if (embedKey.trim() || clearEmbedKey) {
        await api.SaveEmbedKey(embedKey, clearEmbedKey);
        saved = await api.GetSettings();
      }
      setS(saved);
      setApiKey("");
      setClearKey(false);
      setEmbedKey("");
      setClearEmbedKey(false);
      setDirty(false);
      await loadSettings();
      toast.ok("设置已保存");
      return true;
    } catch (e) {
      toast.error("保存失败", e);
      return false;
    } finally {
      setSaving(false);
    }
  };
  const test = async () => {
    if (dirty && !(await save())) return;
    setTesting(true);
    setTestResult(null);
    try {
      setTestResult({ ok: true, text: await api.TestAI() });
    } catch (e) {
      setTestResult({ ok: false, text: String(e) });
    } finally {
      setTesting(false);
    }
  };

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title="设置"
        icon={SettingsIcon}
        description="AI 模型、长期记忆、智能体行为、终端外观与数据备份"
        actions={
          <Button variant={dirty ? "primary" : "secondary"} icon={Save} loading={saving} disabled={!dirty} onClick={save}>
            保存设置
          </Button>
        }
      />
      <Tabs
        className="mx-4 @2xl:mx-7"
        value={tab}
        onChange={setTab}
        tabs={[
          { key: "ai", label: "AI 模型", icon: Bot },
          { key: "memory", label: "长期记忆", icon: BrainCircuit },
          { key: "agent", label: "智能体", icon: Zap },
          { key: "terminal", label: "终端", icon: SquareTerminal },
          { key: "general", label: "通用", icon: Monitor },
          { key: "data", label: "数据与备份", icon: Database },
          { key: "about", label: "关于", icon: Info },
        ]}
      />
      <div className="min-h-0 flex-1 overflow-y-auto px-4 @2xl:px-7 py-6">
        <div className="max-w-[860px] space-y-5">
          {tab === "ai" && (
            <>
              <Card className="p-5">
                <div className="mb-3 text-[13.5px] font-semibold">模型服务商</div>
                <div className="grid grid-cols-2 gap-2.5 @2xl:grid-cols-4">
                  {presets.map((p) => (
                    <button
                      key={p.id}
                      type="button"
                      onClick={() => {
                        set((x) => {
                          x.ai.preset = p.id;
                          x.ai.provider = p.provider;
                          x.ai.baseUrl = p.baseUrl;
                          if (p.models.length) x.ai.model = p.models[0];
                          else if (p.id !== preset.id) x.ai.model = "";
                        });
                        // 换到另一个服务商时，原来保存的 Key 不应该发给新的服务商
                        if (s.ai.hasApiKey) setClearKey(p.provider !== savedSettings?.ai.provider || p.baseUrl !== savedSettings?.ai.baseUrl);
                      }}
                      className={cn("relative rounded-xl border px-3 py-3 text-left transition-all", s.ai.preset === p.id ? "border-accent/60 bg-accent/10 shadow-[var(--glow)]" : "border-line2 hover:bg-panel2")}
                    >
                      <div className="text-[13px] font-semibold">{p.name}</div>
                      <div className="mt-0.5 text-[11px] text-muted">{p.note}</div>
                      {s.ai.preset === p.id && <CircleCheck size={14} className="absolute top-2.5 right-2.5 text-accent" />}
                    </button>
                  ))}
                </div>
              </Card>
              <Card className="space-y-4 p-5">
                <Field label="接口地址（Base URL）" hint={s.ai.provider === "anthropic" ? "留空使用 Anthropic 官方 API；使用代理网关时填写其地址" : "OpenAI 兼容接口地址，会自动补全 /chat/completions"}>
                  <Input mono value={s.ai.baseUrl} onChange={(e) => set((x) => (x.ai.baseUrl = e.target.value))} placeholder={s.ai.provider === "anthropic" ? "https://api.anthropic.com" : "https://..."} />
                </Field>
                <Field
                  label="API Key"
                  hint={
                    s.ai.hasApiKey && !clearKey
                      ? "已保存（使用 Windows 账户加密），留空表示不修改"
                      : preset.id === "ollama"
                        ? "本地 Ollama 无需填写"
                        : s.ai.hasApiKey && s.ai.baseUrl !== savedSettings?.ai.baseUrl
                          ? "换了服务商：原来保存的 Key 不会发给新的服务商，请填写新的 Key"
                          : "仅保存在本机"
                  }
                  action={
                    s.ai.hasApiKey &&
                    !clearKey && (
                      <button type="button" className="text-[12px] text-bad hover:underline" onClick={() => { setClearKey(true); setDirty(true); }}>
                        清除已保存的 Key
                      </button>
                    )
                  }
                >
                  <Input
                    mono
                    type={showKey ? "text" : "password"}
                    value={apiKey}
                    onChange={(e) => {
                      setApiKey(e.target.value);
                      setDirty(true);
                    }}
                    placeholder={s.ai.hasApiKey && !clearKey ? "••••••••••••（已保存）" : "sk-..."}
                    right={<IconButton size="sm" icon={showKey ? EyeOff : Eye} onClick={() => setShowKey((v) => !v)} />}
                  />
                </Field>
                <div className="grid grid-cols-1 gap-4 @xl:grid-cols-2">
                  <Field label="模型" hint="可从列表选择或直接输入模型名称">
                    <Input mono value={s.ai.model} onChange={(e) => set((x) => (x.ai.model = e.target.value))} list="model-list" placeholder="模型名称" />
                    <datalist id="model-list">
                      {preset.models.map((m) => (
                        <option key={m} value={m} />
                      ))}
                    </datalist>
                  </Field>
                  {s.ai.provider === "anthropic" ? (
                    <Field label="推理强度（effort）" hint="越高越深入但更慢、更贵；日常运维 medium 即可">
                      <Segmented
                        value={s.ai.effort as "low" | "medium" | "high" | "xhigh" | "max"}
                        onChange={(v) => set((x) => (x.ai.effort = v))}
                        options={[
                          { value: "low", label: "low" },
                          { value: "medium", label: "medium" },
                          { value: "high", label: "high" },
                          { value: "xhigh", label: "xhigh" },
                        ]}
                      />
                    </Field>
                  ) : (
                    <Field label="上下文预算（字符）" hint="超出时自动丢弃最早的对话轮次">
                      <Input mono type="number" value={s.ai.contextChars} onChange={(e) => set((x) => (x.ai.contextChars = Number(e.target.value)))} />
                    </Field>
                  )}
                </div>
                {s.ai.provider === "anthropic" && (
                  <Field label="单次最大输出 tokens" hint="包含思考过程，默认 64000">
                    <Input mono type="number" value={s.ai.maxTokens} onChange={(e) => set((x) => (x.ai.maxTokens = Number(e.target.value)))} className="w-[200px]" />
                  </Field>
                )}
                <div className="flex items-center gap-3 border-t border-line pt-4">
                  <Button icon={Plug} loading={testing} onClick={test}>
                    {dirty ? "保存并测试" : "测试连接"}
                  </Button>
                  {testResult && (
                    <span className={cn("inline-flex min-w-0 items-center gap-1.5 text-[12.5px]", testResult.ok ? "text-ok" : "text-bad")}>
                      {testResult.ok ? <CircleCheck size={14} /> : <Info size={14} />}
                      <span className="truncate">{testResult.text}</span>
                    </span>
                  )}
                </div>
              </Card>
              {s.ai.provider === "anthropic" && (
                <Callout tone="info" title="Claude 集成说明">
                  使用官方 Anthropic Go SDK：流式输出、自适应思考（显示思考摘要）、提示词缓存；直连官方 API 时启用服务端安全回退（fallbacks: default）与工具参数流式传输。
                </Callout>
              )}
            </>
          )}

          {tab === "memory" && (
            <MemorySettings
              s={s}
              set={set}
              embedKey={embedKey}
              setEmbedKey={(v) => {
                setEmbedKey(v);
                setDirty(true);
              }}
              clearEmbedKey={clearEmbedKey}
              setClearEmbedKey={(v) => {
                setClearEmbedKey(v);
                setDirty(true);
              }}
              dirty={dirty}
              save={save}
            />
          )}

          {tab === "agent" && (
            <>
              <Card className="p-5">
                <div className="mb-3 text-[13.5px] font-semibold">命令审批</div>
                <div className="space-y-2">
                  {[
                    { v: "safe", title: "只读命令自动执行（推荐）", desc: "查看状态、日志、配置等只读命令直接执行；任何修改都需要你批准" },
                    { v: "always", title: "每次都询问", desc: "AI 的每条命令都需要你批准，最安全" },
                    { v: "auto", title: "全自动", desc: "除高危命令（删除系统目录、关机、停止 SSH 等）外全部自动执行，请谨慎使用" },
                  ].map((o) => (
                    <button
                      key={o.v}
                      type="button"
                      onClick={() => set((x) => (x.ai.approvalMode = o.v))}
                      className={cn("flex w-full items-start gap-3 rounded-xl border px-4 py-3 text-left transition-colors", s.ai.approvalMode === o.v ? "border-accent/60 bg-accent/8" : "border-line2 hover:bg-panel2")}
                    >
                      <span className={cn("mt-1 flex h-4 w-4 shrink-0 items-center justify-center rounded-full border", s.ai.approvalMode === o.v ? "border-accent" : "border-line2")}>
                        {s.ai.approvalMode === o.v && <span className="h-2 w-2 rounded-full bg-accent" />}
                      </span>
                      <div>
                        <div className="text-[13px] font-medium">{o.title}</div>
                        <div className="mt-0.5 text-[12px] text-muted">{o.desc}</div>
                      </div>
                    </button>
                  ))}
                </div>
              </Card>
              <Card className="space-y-4 p-5">
                <div className="text-[13.5px] font-semibold">自我学习</div>
                <div className="flex items-center justify-between gap-4">
                  <div>
                    <div className="text-[13px] font-medium">自动学习复盘</div>
                    <div className="text-[12px] text-muted">一轮对话中工具调用次数达到阈值后，后台让 AI 回顾对话并沉淀记忆与技能。AI 沉淀的技能超过 90 天没有用到会自动归档，可以在「记忆与技能」里恢复或固定</div>
                  </div>
                  <Switch checked={s.ai.autoLearn} onChange={(v) => set((x) => (x.ai.autoLearn = v))} />
                </div>
                <div className="grid grid-cols-1 gap-4 @xl:grid-cols-3">
                  <Field label="复盘阈值（工具调用次数）">
                    <Input mono type="number" value={s.ai.reviewThreshold} onChange={(e) => set((x) => (x.ai.reviewThreshold = Number(e.target.value)))} />
                  </Field>
                  <Field label="记忆提醒间隔（轮）" hint="0 表示关闭">
                    <Input mono type="number" value={s.ai.nudgeInterval} onChange={(e) => set((x) => (x.ai.nudgeInterval = Number(e.target.value)))} />
                  </Field>
                  <Field label="单轮最大步骤">
                    <Input mono type="number" value={s.ai.maxSteps} onChange={(e) => set((x) => (x.ai.maxSteps = Number(e.target.value)))} />
                  </Field>
                </div>
              </Card>
            </>
          )}

          {tab === "terminal" && (
            <Card className="space-y-4 p-5">
              <div className="grid grid-cols-1 gap-4 @xl:grid-cols-2">
                <Field label="字号">
                  <Input mono type="number" value={s.terminal.fontSize} onChange={(e) => set((x) => (x.terminal.fontSize = Number(e.target.value)))} />
                </Field>
                <Field label="回滚行数">
                  <Input mono type="number" value={s.terminal.scrollback} onChange={(e) => set((x) => (x.terminal.scrollback = Number(e.target.value)))} />
                </Field>
              </div>
              <Field label="字体" hint="按顺序回退；中文建议保留 Microsoft YaHei UI">
                <Input mono value={s.terminal.fontFamily} onChange={(e) => set((x) => (x.terminal.fontFamily = e.target.value))} />
              </Field>
              <Field label="配色">
                <div className="grid grid-cols-1 gap-2.5 @xl:grid-cols-3">
                  {Object.entries(termThemes).map(([k, t]) => (
                    <button key={k} type="button" onClick={() => set((x) => (x.terminal.theme = k))} className={cn("overflow-hidden rounded-xl border text-left transition-all", s.terminal.theme === k ? "border-accent/70 shadow-[var(--glow)]" : "border-line2")}>
                      <div className="px-3 py-2.5 font-mono text-[11.5px] leading-relaxed" style={{ background: t.background, color: t.foreground }}>
                        <span style={{ color: t.green }}>root@web-01</span>:<span style={{ color: t.blue }}>~</span># ls
                        <br />
                        <span style={{ color: t.cyan }}>nginx</span> <span style={{ color: t.yellow }}>app.log</span> <span style={{ color: t.red }}>error</span>
                      </div>
                      <div className="border-t border-line bg-panel px-3 py-1.5 text-[12px]">{t.label}</div>
                    </button>
                  ))}
                </div>
              </Field>
              <div className="flex items-center justify-between">
                <span className="text-[13px]">光标闪烁</span>
                <Switch checked={s.terminal.cursorBlink} onChange={(v) => set((x) => (x.terminal.cursorBlink = v))} />
              </div>
              <div className="flex items-center justify-between gap-4">
                <div>
                  <div className="text-[13px]">选中即复制</div>
                  <div className="text-[12px] text-muted">用鼠标选中文字后自动复制到剪贴板</div>
                </div>
                <Switch checked={s.terminal.copyOnSelect} onChange={(v) => set((x) => (x.terminal.copyOnSelect = v))} />
              </div>
            </Card>
          )}
          {tab === "terminal" && (
            <Card className="space-y-4 p-5">
              <div className="flex items-start justify-between gap-4">
                <div>
                  <div className="text-[13.5px] font-semibold">快捷键</div>
                  <div className="mt-1 text-[12px] leading-relaxed text-muted">点一下右边的按键框，然后按下想用的组合键。在终端里点右键也可以打开包含这些操作的菜单。</div>
                </div>
                <Button
                  size="sm"
                  variant="ghost"
                  icon={RotateCcw}
                  disabled={hotkeys.every((h) => s.terminal[h.field] === DEFAULT_KEYS[h.field])}
                  onClick={() => set((x) => hotkeys.forEach((h) => (x.terminal[h.field] = DEFAULT_KEYS[h.field])))}
                >
                  恢复默认
                </Button>
              </div>
              {hotkeys.map((h) => (
                <div key={h.field} className="flex items-center justify-between gap-4">
                  <div className="min-w-0">
                    <div className="text-[13px]">{h.label}</div>
                    <div className="text-[12px] text-muted">{h.desc}</div>
                  </div>
                  <HotkeyInput value={s.terminal[h.field]} action={h.action} others={hotkeys.filter((o) => o.field !== h.field).map((o) => s.terminal[o.field])} onChange={(v) => set((x) => (x.terminal[h.field] = v))} />
                </div>
              ))}
              <div className="flex items-center justify-between gap-4 border-t border-line pt-4">
                <div>
                  <div className="text-[13px]">同时使用 Ctrl+V 粘贴、Ctrl+C 复制</div>
                  <div className="text-[12px] text-muted">Windows 的习惯按键：Ctrl+V 粘贴；选中了文字时 Ctrl+C 是复制，没有选中时照常中断程序。关闭后这两个按键原样发给终端（例如 vim 里的 Ctrl+V）。</div>
                </div>
                <Switch checked={!s.terminal.plainKeysOff} onChange={(v) => set((x) => (x.terminal.plainKeysOff = !v))} />
              </div>
            </Card>
          )}

          {tab === "general" && (
            <>
              <Card className="space-y-4 p-5">
                <div className="flex items-start justify-between gap-6">
                  <div>
                    <div className="text-[13.5px] font-semibold">实时监控</div>
                    <div className="mt-1 text-[12px] leading-relaxed text-muted">
                      只监控打开了终端的服务器：在实时监控页、仪表盘和服务器详情里实时显示它们的 CPU、内存、磁盘和网络，不会为了监控去连接其他服务器。每台服务器只占用一个 SSH 会话，采样只读取 /proc，对服务器几乎没有负担；离开这些页面后自动停止。
                    </div>
                  </div>
                  <Switch checked={!s.general.monitorOff} onChange={(v) => set((x) => (x.general.monitorOff = !v))} />
                </div>
                {!s.general.monitorOff && (
                  <Field label="刷新间隔" hint="间隔越短越接近实时；服务器很多或网络较慢时可以调大一些">
                    <Segmented value={String(s.general.monitorInterval)} onChange={(v) => set((x) => (x.general.monitorInterval = Number(v)))} options={INTERVALS.map((n) => ({ value: String(n), label: `${n} 秒` }))} />
                  </Field>
                )}
              </Card>
              <Card className="space-y-4 p-5">
                <Field label="界面主题">
                  <Segmented
                    value={theme}
                    onChange={(v) => setTheme(v)}
                    options={[
                      { value: "dark", label: "深色" },
                      { value: "light", label: "浅色" },
                    ]}
                  />
                </Field>
                <div className="grid grid-cols-1 gap-4 @xl:grid-cols-2">
                  <Field label="在线检测间隔（秒）" hint="多久检测一次服务器是否可达">
                    <Input mono type="number" value={s.general.statusInterval} onChange={(e) => set((x) => (x.general.statusInterval = Number(e.target.value)))} />
                  </Field>
                  <Field label="批量操作并发数">
                    <Input mono type="number" value={s.general.concurrency} onChange={(e) => set((x) => (x.general.concurrency = Number(e.target.value)))} />
                  </Field>
                  <Field label="脚本默认超时（秒）">
                    <Input mono type="number" value={s.general.commandTimeout} onChange={(e) => set((x) => (x.general.commandTimeout = Number(e.target.value)))} />
                  </Field>
                  <Field label="证书到期提醒（天）">
                    <Input mono type="number" value={s.general.certWarnDays} onChange={(e) => set((x) => (x.general.certWarnDays = Number(e.target.value)))} />
                  </Field>
                </div>
              </Card>
            </>
          )}

          {tab === "data" && (
            <>
              <Card className="p-5">
                <div className="text-[13.5px] font-semibold">数据目录</div>
                <div className="mt-2 flex items-center gap-2">
                  <code className="selectable min-w-0 flex-1 truncate rounded-lg border border-line bg-bg2 px-3 py-2 font-mono text-[12px] text-fg2" title={info?.dataDir}>
                    {info?.dataDir}
                  </code>
                  <Button icon={FolderOpen} onClick={() => api.OpenDir("data").catch((e) => toast.error("打不开目录", e))}>
                    打开
                  </Button>
                </div>
                <p className="mt-2 text-[12px] leading-relaxed text-muted">
                  {info?.dataMode === "program"
                    ? "全部数据都保存在程序所在目录下的 data 文件夹里：服务器、密钥、证书、脚本、设置、对话记录、AI 的记忆与技能。移动程序时请连同 data 文件夹一起移动。"
                    : info?.dataMode === "appdata"
                      ? "程序所在的目录不能写入（例如放在 Program Files 里），数据保存在当前用户的 AppData 目录。把程序移到一个可以写入的文件夹后，数据会放到程序目录下的 data 文件夹。"
                      : "数据目录由环境变量 SSHPILOT_DATA_DIR 指定。"}
                  密码、私钥和 API Key 使用 Windows DPAPI 加密，只有这台电脑上的当前 Windows 用户可以解密——换电脑或换账户时，请用下面的加密备份来迁移，直接拷贝 data 文件夹带不走这些密钥。
                </p>
                {info?.migratedFrom && (
                  <div className="mt-3 flex flex-wrap items-center gap-x-3 gap-y-2 rounded-lg border border-line bg-bg2/60 px-3 py-2 text-[12px] text-muted">
                    <span className="min-w-0 flex-1">
                      数据是从旧版本的目录 <code className="selectable font-mono text-fg2">{info.migratedFrom}</code> 复制过来的，旧目录里的数据原样保留。确认新版本一切正常后，可以自己删除它。
                    </span>
                    <Button size="sm" variant="ghost" icon={FolderOpen} onClick={() => api.OpenDir("legacy").catch((e) => toast.error("打不开目录", e))}>
                      打开旧目录
                    </Button>
                  </div>
                )}
              </Card>
              <Card className="p-5">
                <div className="flex items-start gap-3">
                  <Shield size={18} className="mt-0.5 shrink-0 text-accent" />
                  <div className="min-w-0 flex-1">
                    <div className="text-[13.5px] font-semibold">加密备份</div>
                    <p className="mt-1 text-[12px] leading-relaxed text-muted">导出全部服务器、密钥、证书、脚本、设置以及 AI 的记忆（包括长期记忆）与技能，使用你设置的密码加密（scrypt + AES-256-GCM），可用于迁移到其他电脑。</p>
                    <div className="mt-3 text-[12px] text-muted">备份默认保存到</div>
                    <div className="mt-1 flex items-center gap-2">
                      <code className="selectable min-w-0 flex-1 truncate rounded-lg border border-line bg-bg2 px-3 py-2 font-mono text-[12px] text-fg2" title={info?.backupDir}>
                        {info?.backupDir}
                      </code>
                      <Button icon={FolderOpen} onClick={() => api.OpenDir("backup").catch((e) => toast.error("打不开目录", e))}>
                        打开
                      </Button>
                    </div>
                    <div className="mt-3 flex flex-wrap gap-2">
                      <Button icon={Download} onClick={() => setBackup("export")}>
                        导出备份
                      </Button>
                      <Button icon={Upload} onClick={() => setBackup("import")}>
                        从备份恢复
                      </Button>
                    </div>
                  </div>
                </div>
              </Card>
            </>
          )}

          {tab === "about" && (
            <Card className="p-6">
              <div className="flex items-center gap-4">
                <Logo size={56} />
                <div className="min-w-0">
                  <div className="text-[20px] font-semibold">{info?.product || PRODUCT}</div>
                  <div className="text-[12.5px] text-muted">
                    SSH 服务器 · 证书 · 智能运维管家 · v{info?.version} · {info?.platform}
                  </div>
                  <div className="mt-0.5 text-[12.5px] text-fg2">作者：{info?.author || AUTHOR}</div>
                </div>
              </div>
              <div className="mt-5 grid grid-cols-1 gap-3 @xl:grid-cols-2 text-[12.5px] text-fg2">
                {[
                  ["服务器与终端", "自定义端口、私钥/密码、跳板机、sudo、多标签终端"],
                  ["密钥管理", "生成/导入私钥，公钥部署与回收，authorized_keys 对账，密钥轮换"],
                  ["证书中心", "导入/拉取证书，一键同步多台服务器，备份、检测、重载、回滚"],
                  ["服务与脚本", "systemd 服务管理，脚本库批量执行，实时输出与历史"],
                  ["AI 运维助手", "终端里直接对话操作服务器，命令审批与风险分级，DeepSeek、Claude 等模型"],
                  ["自我学习", "核心记忆 + 不限容量的长期记忆（向量检索、自动召回）、技能库、后台复盘"],
                ].map(([t, d]) => (
                  <div key={t} className="rounded-xl border border-line bg-bg2/50 p-3">
                    <div className="font-medium text-fg">{t}</div>
                    <div className="mt-0.5 text-muted">{d}</div>
                  </div>
                ))}
              </div>
              <div className="mt-4 flex gap-2">
                <Badge>Go + Wails</Badge>
                <Badge>React 19</Badge>
                <Badge>xterm.js</Badge>
                <Badge tone="accent">DeepSeek / Claude</Badge>
              </div>
            </Card>
          )}
        </div>
      </div>
      <BackupModal mode={backup} onClose={() => setBackup(null)} />
    </div>
  );
}

function BackupModal({ mode, onClose }: { mode: "export" | "import" | null; onClose: () => void }) {
  const { loadServers, loadKeys, loadSettings } = useApp();
  const [pw, setPw] = useState("");
  const [pw2, setPw2] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    setPw("");
    setPw2("");
  }, [mode]);
  const exporting = mode === "export";
  return (
    <Modal
      open={!!mode}
      onClose={onClose}
      title={exporting ? "导出加密备份" : "从备份恢复"}
      icon={exporting ? Download : Upload}
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button
            variant={exporting ? "primary" : "danger"}
            loading={busy}
            disabled={pw.length < 6 || (exporting && pw !== pw2)}
            onClick={async () => {
              if (!exporting && !(await confirm({ title: "恢复备份会覆盖当前全部数据，确定继续？", danger: true, confirmText: "覆盖并恢复" }))) return;
              setBusy(true);
              try {
                if (exporting) {
                  const p = await api.ExportBackup(pw);
                  if (p) toast.ok("备份已导出", p);
                } else {
                  const ok = await api.ImportBackup(pw);
                  if (ok) {
                    toast.ok("数据已恢复");
                    await Promise.all([loadServers(), loadKeys(), loadSettings()]);
                  }
                }
                onClose();
              } catch (e) {
                toast.error(exporting ? "导出失败" : "恢复失败", e);
              } finally {
                setBusy(false);
              }
            }}
          >
            {exporting ? "选择保存位置" : "选择备份文件"}
          </Button>
        </>
      }
    >
      <div className="space-y-3">
        <Field label="备份密码" hint="至少 6 位，忘记后无法恢复">
          <Input type="password" value={pw} onChange={(e) => setPw(e.target.value)} autoFocus />
        </Field>
        {exporting && (
          <Field label="确认密码" error={pw2 && pw !== pw2 ? "两次输入不一致" : undefined}>
            <Input type="password" value={pw2} onChange={(e) => setPw2(e.target.value)} />
          </Field>
        )}
      </div>
    </Modal>
  );
}

