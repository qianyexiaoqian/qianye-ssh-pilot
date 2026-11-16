import { useEffect, useRef, useState } from "react";
import { createPortal } from "react-dom";
import { ArrowLeft, Eye, EyeOff, Lock, TriangleAlert } from "lucide-react";
import { api, errText, runtime } from "../lib/api";
import { toast, useApp } from "../lib/store";
import { Button, IconButton, Input } from "./ui";
import { Logo, PRODUCT, WindowButtons } from "./shell";

/**
 * 锁屏：设置了启动密码后，启动时和运行中锁定时盖住整个窗口。
 * 运行中锁定时主界面还在下面（终端继续运行），这里负责让它既看不见也操作不了。
 */
export function LockScreen() {
  const lock = useApp((s) => s.lock);
  const setLock = useApp((s) => s.setLock);
  const loadLock = useApp((s) => s.loadLock);
  const [pw, setPw] = useState("");
  const [show, setShow] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  const [forgot, setForgot] = useState(false);
  const [sure, setSure] = useState(false);
  const [now, setNow] = useState(Date.now());
  const layer = useRef<HTMLDivElement>(null);
  const input = useRef<HTMLInputElement>(null);
  const submitRef = useRef<() => void>(() => {});

  const wait = Math.max(0, Math.ceil(((lock?.retryAt ?? 0) - now) / 1000));
  const missing = !!lock?.missing;

  const submit = async () => {
    if (busy || !pw || wait > 0 || forgot || missing) return;
    setBusy(true);
    setError("");
    try {
      setLock(await api.Unlock(pw));
    } catch (e) {
      setError(errText(e));
      setPw("");
      await loadLock();
      setNow(Date.now());
    } finally {
      setBusy(false);
    }
  };
  submitRef.current = () => void submit();

  const reset = async () => {
    setBusy(true);
    try {
      const r = await api.ResetLock();
      const parts = [`清空了 ${r.wiped} 项已保存的密码和密钥`];
      if (r.servers > 0) parts.push(`${r.servers} 台服务器需要重新填写密码或选择私钥`);
      toast.warn("启动密码已重置", parts.join("，") + "。");
      setLock(r.state);
    } catch (e) {
      setError(errText(e));
    } finally {
      setBusy(false);
    }
  };

  // 输错之后、等待结束之后，光标回到输入框，可以直接再输
  useEffect(() => {
    if (!busy && wait <= 0 && !forgot) input.current?.focus();
  }, [busy, wait > 0, forgot]);

  // 等待重试的倒计时
  useEffect(() => {
    if (wait <= 0) return;
    const t = setInterval(() => setNow(Date.now()), 500);
    return () => clearInterval(t);
  }, [wait > 0]);

  useEffect(() => {
    // 下面的主界面不能再获得焦点、响应键盘
    const others = Array.from(document.body.children).filter((el) => el !== layer.current);
    others.forEach((el) => el.setAttribute("inert", ""));
    input.current?.focus();
    const onKey = (e: KeyboardEvent) => {
      // 主界面的全局快捷键（Ctrl+K 等）在锁定时不生效
      e.stopImmediatePropagation();
      if (e.key === "Enter" && document.activeElement === input.current) {
        e.preventDefault();
        submitRef.current();
      }
      if (e.key === "Tab" && layer.current) {
        // 焦点只在锁屏里循环
        const items = Array.from(layer.current.querySelectorAll<HTMLElement>("input:not([disabled]), button:not([disabled])"));
        if (items.length === 0) return;
        e.preventDefault();
        const i = items.indexOf(document.activeElement as HTMLElement);
        items[(i + (e.shiftKey ? -1 : 1) + items.length) % items.length].focus();
      }
    };
    window.addEventListener("keydown", onKey, true);
    return () => {
      others.forEach((el) => el.removeAttribute("inert"));
      window.removeEventListener("keydown", onKey, true);
    };
  }, []);

  const resetPanel = (
    <div className="mt-5 text-left">
      <div className="rounded-xl border border-warn/30 bg-warn/8 px-3.5 py-3 text-[12.5px] leading-relaxed text-fg2">
        <div className="mb-1 flex items-center gap-1.5 font-semibold text-warn">
          <TriangleAlert size={15} />
          {missing ? "解锁所需的文件不见了" : "启动密码没有找回的办法"}
        </div>
        {missing && <p className="mb-1.5">数据目录里的 lock.json 被删除或损坏了。如果你有它的备份，关闭程序、把它放回数据目录再打开即可；否则只能重置。</p>}
        <p>重置会：</p>
        <ul className="mt-1 list-disc space-y-0.5 pl-5">
          <li>清空所有已保存的服务器密码、sudo 密码、私钥、证书私钥和 API Key（它们是用启动密码加密的，没有密码解不开）；</li>
          <li>保留服务器列表、分组、脚本、对话记录和 AI 的记忆；</li>
          <li>取消启动密码。</li>
        </ul>
        <p className="mt-1.5 text-muted">重置前的数据会在数据目录里留一份副本（store.json.before-reset 和 lock.json.before-reset），以后想起密码还能换回去。</p>
      </div>
      <label className="mt-3 flex cursor-pointer items-start gap-2 text-[12.5px] text-fg2">
        <input type="checkbox" className="mt-0.5 accent-accent" checked={sure} onChange={(e) => setSure(e.target.checked)} />
        <span>我明白这些密码和私钥清空后需要重新填写</span>
      </label>
      {error && <div className="mt-2 text-[12.5px] text-bad">{error}</div>}
      <div className="mt-4 flex gap-2">
        {!missing && (
          <Button
            icon={ArrowLeft}
            className="flex-1"
            onClick={() => {
              setForgot(false);
              setSure(false);
              setError("");
            }}
          >
            返回
          </Button>
        )}
        <Button variant="danger" className="flex-1" disabled={!sure} loading={busy} onClick={reset}>
          清空并重置
        </Button>
      </div>
    </div>
  );

  return createPortal(
    <div ref={layer} data-lock-screen className="fixed inset-0 z-[200] flex flex-col bg-bg">
      <header className="drag flex h-[40px] shrink-0 items-center" onDoubleClick={() => runtime.WindowToggleMaximise()}>
        <div className="flex min-w-0 flex-1 items-center gap-2.5 pl-3.5">
          <Logo size={22} />
          <span className="truncate text-[13px] font-semibold tracking-tight">{PRODUCT}</span>
        </div>
        <div className="no-drag flex shrink-0 items-center" onDoubleClick={(e) => e.stopPropagation()}>
          <WindowButtons />
        </div>
      </header>
      {/* 卡片用 m-auto 居中：窗口很矮、内容放不下时可以从头滚动，不会被裁掉上半部分 */}
      <div className="relative flex min-h-0 flex-1 overflow-y-auto p-4">
        <div className="pointer-events-none absolute top-1/2 left-1/2 h-[420px] w-[420px] -translate-x-1/2 -translate-y-1/2 rounded-full bg-accent/10 blur-[110px]" />
        <div className="card relative m-auto w-full max-w-[380px] !rounded-[20px] border-line2 p-5 text-center sm:p-7">
          <div className="mx-auto flex h-14 w-14 items-center justify-center rounded-2xl grad-accent text-white shadow-[var(--glow)]">
            <Lock size={26} />
          </div>
          <div className="mt-4 text-[17px] font-semibold">{forgot || missing ? "重置启动密码" : "已锁定"}</div>
          {forgot || missing ? (
            resetPanel
          ) : (
            <>
              <div className="mt-1 text-[12.5px] text-muted">{lock?.sealed ? "输入启动密码后才能查看和连接服务器" : "输入启动密码继续使用，正在运行的终端和任务没有中断"}</div>
              <div className="mt-5 text-left">
                <Input
                  ref={input}
                  type={show ? "text" : "password"}
                  value={pw}
                  onChange={(e) => {
                    setPw(e.target.value);
                    setError("");
                  }}
                  placeholder={wait > 0 ? `请等待 ${wait} 秒` : "启动密码"}
                  disabled={busy || wait > 0}
                  autoComplete="off"
                  className="h-10 text-[14px]"
                  right={<IconButton size="sm" icon={show ? EyeOff : Eye} onClick={() => setShow((v) => !v)} />}
                />
                <div className="mt-1.5 min-h-[18px] text-[12.5px] text-bad">{wait > 0 ? `输错次数过多，请在 ${wait} 秒后再试` : error}</div>
              </div>
              <Button variant="primary" size="lg" className="mt-2 w-full" loading={busy} disabled={!pw || wait > 0} onClick={() => void submit()}>
                解锁
              </Button>
              <button
                type="button"
                className="mt-4 text-[12.5px] text-muted hover:text-fg2 hover:underline"
                onClick={() => {
                  setForgot(true);
                  setError("");
                }}
              >
                忘记密码？
              </button>
            </>
          )}
        </div>
      </div>
    </div>,
    document.body,
  );
}
