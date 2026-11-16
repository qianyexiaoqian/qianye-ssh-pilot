import { useEffect, useRef, useState } from "react";
import { CircleCheck, CircleX, LoaderCircle, Play, Square, Zap } from "lucide-react";
import { api, on, type RunResult } from "../lib/api";
import { cn, formatMs, stripAnsi } from "../lib/format";
import { toast } from "../lib/store";
import { Badge, Button, Field, Modal } from "./ui";
import { CodeEditor, OutputBox, ServerPicker } from "./widgets";

export interface RunState {
  runId: string | null;
  outputs: Record<string, string>;
  results: Record<string, RunResult>;
  running: boolean;
}

type RunEvent = { kind: "output" | "server" | "done"; e: any };

/** 订阅一次批量执行的实时输出。调用 begin() 后、拿到 runId 之前到达的事件会先缓存再回放。 */
export function useRunStream() {
  const [state, setState] = useState<RunState>({ runId: null, outputs: {}, results: {}, running: false });
  const runRef = useRef<string | null>(null);
  const pendingRef = useRef(false);
  const bufRef = useRef<RunEvent[]>([]);
  const apply = ({ kind, e }: RunEvent) => {
    if (kind === "output") setState((s) => ({ ...s, outputs: { ...s.outputs, [e.serverId]: (s.outputs[e.serverId] ?? "") + e.data } }));
    else if (kind === "server") setState((s) => ({ ...s, results: { ...s.results, [e.result.serverId]: e.result as RunResult } }));
    else setState((s) => ({ ...s, running: false }));
  };
  useEffect(() => {
    const handle = (kind: RunEvent["kind"]) => (e: { runId: string }) => {
      if (runRef.current && e.runId === runRef.current) apply({ kind, e });
      else if (pendingRef.current) bufRef.current.push({ kind, e });
    };
    const offs = [on("run:output", handle("output")), on("run:server_done", handle("server")), on("run:done", handle("done"))];
    return () => offs.forEach((f) => f());
  }, []);
  const begin = () => {
    runRef.current = null;
    pendingRef.current = true;
    bufRef.current = [];
    setState({ runId: null, outputs: {}, results: {}, running: true });
  };
  const start = (runId: string) => {
    runRef.current = runId;
    pendingRef.current = false;
    setState((s) => ({ ...s, runId, running: true }));
    const buffered = bufRef.current.filter((x) => x.e.runId === runId);
    bufRef.current = [];
    buffered.forEach(apply);
  };
  const fail = () => {
    pendingRef.current = false;
    setState((s) => ({ ...s, running: false }));
  };
  const reset = () => {
    runRef.current = null;
    pendingRef.current = false;
    setState({ runId: null, outputs: {}, results: {}, running: false });
  };
  return { state, begin, start, fail, reset };
}

export function RunOutputs({ serverIds, state, names, className }: { serverIds: string[]; state: RunState; names: Record<string, string>; className?: string }) {
  const [active, setActive] = useState<string | null>(null);
  const current = active && serverIds.includes(active) ? active : serverIds[0];
  if (!state.runId || serverIds.length === 0) return null;
  return (
    <div className={cn("flex min-h-0 flex-col", className)}>
      <div className="mb-2 flex flex-wrap gap-1.5">
        {serverIds.map((id) => {
          const r = state.results[id];
          const ok = r && !r.error && r.exitCode === 0;
          return (
            <button
              key={id}
              type="button"
              onClick={() => setActive(id)}
              className={cn(
                "inline-flex h-7 items-center gap-1.5 rounded-lg border px-2.5 text-[12px] transition-colors",
                current === id ? "border-accent/50 bg-accent/10 text-fg" : "border-line text-fg2 hover:bg-panel2",
              )}
            >
              {!r ? <LoaderCircle size={12} className="animate-spin text-accent" /> : ok ? <CircleCheck size={12} className="text-ok" /> : <CircleX size={12} className="text-bad" />}
              {names[id] ?? id}
              {r && <span className="text-muted">{formatMs(r.durationMs)}</span>}
            </button>
          );
        })}
      </div>
      {current && (
        <>
          {state.results[current] && (
            <div className="mb-1.5 flex items-center gap-2 text-[12px]">
              {state.results[current].error ? (
                <Badge tone="bad" icon={CircleX}>
                  {state.results[current].error}
                </Badge>
              ) : (
                <Badge tone={state.results[current].exitCode === 0 ? "ok" : "bad"} icon={state.results[current].exitCode === 0 ? CircleCheck : CircleX}>
                  退出码 {state.results[current].exitCode}
                </Badge>
              )}
            </div>
          )}
          <OutputBox className="min-h-[160px] flex-1" text={stripAnsi(state.outputs[current] ?? state.results[current]?.output ?? "")} empty={state.results[current] ? "（无输出）" : "等待输出…"} />
        </>
      )}
    </div>
  );
}

/** 快速执行：选择服务器并运行一段命令（来自 AI 回复中的代码块或巡检建议） */
export function QuickRunModal({ open, onClose, command, serverIds = [], title = "执行命令" }: { open: boolean; onClose: () => void; command: string; serverIds?: string[]; title?: string }) {
  const [code, setCode] = useState(command);
  const [targets, setTargets] = useState<string[]>(serverIds);
  const { state, begin, start, fail, reset } = useRunStream();
  const [names, setNames] = useState<Record<string, string>>({});
  useEffect(() => {
    if (open) {
      setCode(command);
      setTargets(serverIds);
      reset();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, command]);
  const run = async () => {
    if (!targets.length) return toast.warn("请选择服务器");
    try {
      const servers = await api.ListServers();
      setNames(Object.fromEntries(servers.map((s) => [s.id, s.name])));
      begin();
      const id = await api.RunScript({ content: code, interpreter: "bash", serverIds: targets, title: code.split("\n")[0].slice(0, 40) });
      start(id);
    } catch (e) {
      fail();
      toast.error("执行失败", e);
    }
  };
  return (
    <Modal
      open={open}
      onClose={onClose}
      title={title}
      icon={Zap}
      size="xl"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            关闭
          </Button>
          {state.running ? (
            <Button variant="danger" icon={Square} onClick={() => state.runId && api.CancelRun(state.runId)}>
              取消执行
            </Button>
          ) : (
            <Button variant="primary" icon={Play} onClick={run} disabled={!code.trim()}>
              执行
            </Button>
          )}
        </>
      }
    >
      <div className="grid grid-cols-[1fr_300px] gap-4">
        <div className="flex min-w-0 flex-col gap-3">
          <Field label="命令">
            <CodeEditor value={code} onChange={setCode} minHeight={150} />
          </Field>
          <RunOutputs serverIds={state.runId ? targets : []} state={state} names={names} className="min-h-[220px]" />
        </div>
        <Field label="目标服务器">
          <ServerPicker value={targets} onChange={setTargets} maxHeight={360} />
        </Field>
      </div>
    </Modal>
  );
}

export function OpResultList({ results }: { results: { serverName: string; ok: boolean; message: string }[] }) {
  return (
    <div className="space-y-1.5">
      {results.map((r, i) => (
        <div key={i} className={cn("flex items-start gap-2.5 rounded-lg border px-3 py-2", r.ok ? "border-ok/25 bg-ok/6" : "border-bad/25 bg-bad/6")}>
          {r.ok ? <CircleCheck size={15} className="mt-0.5 shrink-0 text-ok" /> : <CircleX size={15} className="mt-0.5 shrink-0 text-bad" />}
          <div className="min-w-0 flex-1">
            <div className="text-[12.5px] font-medium text-fg">{r.serverName || "操作"}</div>
            <div className="mt-0.5 text-[12px] break-words whitespace-pre-wrap text-fg2">{r.message}</div>
          </div>
        </div>
      ))}
    </div>
  );
}
