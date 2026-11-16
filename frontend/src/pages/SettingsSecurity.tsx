import { useEffect, useState } from "react";
import { FolderOpen, KeyRound, Lock, LockKeyhole, ShieldCheck } from "lucide-react";
import { api } from "../lib/api";
import { toast, useApp } from "../lib/store";
import { Badge, Button, Callout, Card, Field, Input, Modal, Select } from "../components/ui";

const autoLockChoices = [0, 1, 5, 10, 30, 60];

/** 设置、修改或取消启动密码的对话框 */
function PasswordDialog({ kind, onClose }: { kind: "set" | "change" | "remove" | null; onClose: () => void }) {
  const setLock = useApp((s) => s.setLock);
  const [old, setOld] = useState("");
  const [pw, setPw] = useState("");
  const [again, setAgain] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    setOld("");
    setPw("");
    setAgain("");
    setError("");
  }, [kind]);
  if (!kind) return null;

  const needNew = kind !== "remove";
  const problem = !needNew ? "" : pw && [...pw].length < 4 ? "至少 4 位" : again && pw !== again ? "两次输入的密码不一样" : "";
  const ready = (kind === "set" || old) && (!needNew || (pw && again && !problem));
  const submit = async () => {
    if (!ready || busy) return;
    setBusy(true);
    setError("");
    try {
      if (kind === "remove") {
        setLock(await api.RemoveLockPassword(old));
        toast.ok("已取消启动密码", "以后打开程序不再需要输入密码。");
      } else {
        setLock(await api.SetLockPassword(kind === "set" ? "" : old, pw));
        toast.ok(kind === "set" ? "启动密码已设置" : "启动密码已修改", kind === "set" ? "下次打开程序时需要输入它。" : undefined);
      }
      onClose();
    } catch (e) {
      setError(String(e));
    } finally {
      setBusy(false);
    }
  };
  const onEnter = (e: React.KeyboardEvent) => {
    if (e.key === "Enter") void submit();
  };

  return (
    <Modal
      open
      onClose={onClose}
      title={kind === "set" ? "设置启动密码" : kind === "change" ? "修改启动密码" : "取消启动密码"}
      description={kind === "remove" ? "取消后，打开程序不再需要密码，已保存的密码和密钥恢复为只由 Windows 账户加密。" : "打开程序时需要输入它；已保存的密码和密钥也会用它再加密一层。"}
      icon={LockKeyhole}
      size="sm"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button variant={kind === "remove" ? "danger" : "primary"} loading={busy} disabled={!ready} onClick={() => void submit()}>
            {kind === "set" ? "设置" : kind === "change" ? "修改" : "取消启动密码"}
          </Button>
        </>
      }
    >
      <div className="space-y-3.5">
        {kind !== "set" && (
          <Field label="当前的启动密码">
            <Input type="password" value={old} onChange={(e) => setOld(e.target.value)} onKeyDown={onEnter} autoFocus autoComplete="off" />
          </Field>
        )}
        {needNew && (
          <>
            <Field label={kind === "set" ? "启动密码" : "新的启动密码"}>
              <Input type="password" value={pw} onChange={(e) => setPw(e.target.value)} onKeyDown={onEnter} autoFocus={kind === "set"} placeholder="至少 4 位" autoComplete="new-password" />
            </Field>
            <Field label="再输入一次" error={problem || undefined}>
              <Input type="password" value={again} onChange={(e) => setAgain(e.target.value)} onKeyDown={onEnter} autoComplete="new-password" />
            </Field>
          </>
        )}
        {kind === "set" && (
          <Callout tone="warn" title="请记牢这个密码，忘记了没有办法找回">
            忘记后只能重置，重置会清空所有已保存的服务器密码、私钥和 API Key。建议设置前先到「数据与备份」导出一份加密备份。
          </Callout>
        )}
        {error && <div className="text-[12.5px] text-bad">{error}</div>}
      </div>
    </Modal>
  );
}

export function SecuritySettings() {
  const { lock, setLock, loadLock } = useApp();
  const [dialog, setDialog] = useState<"set" | "change" | "remove" | null>(null);

  useEffect(() => {
    void loadLock();
  }, [loadLock]);
  const enabled = !!lock?.enabled;

  return (
    <>
      <Card className="p-5">
        <div className="flex items-start gap-3">
          <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-accent/12 text-accent">
            <Lock size={18} />
          </div>
          <div className="min-w-0 flex-1">
            <div className="flex flex-wrap items-center gap-2">
              <span className="text-[13.5px] font-semibold">启动密码</span>
              {enabled ? (
                <Badge tone="ok" icon={ShieldCheck}>
                  已开启
                </Badge>
              ) : (
                <Badge>未设置</Badge>
              )}
            </div>
            <p className="mt-1 text-[12px] leading-relaxed text-muted">
              {enabled ? "打开程序时需要输入启动密码。也可以随时点标题栏右上角的锁把界面锁上。" : "设置后，打开程序时要先输入密码，别人用你的电脑也打不开它、看不到服务器的密码。"}
            </p>
            <div className="mt-3 flex flex-wrap gap-2">
              {enabled ? (
                <>
                  <Button icon={KeyRound} onClick={() => setDialog("change")}>
                    修改密码
                  </Button>
                  <Button
                    icon={Lock}
                    onClick={() =>
                      api
                        .LockNow()
                        .then(setLock)
                        .catch((e) => toast.error("无法锁定", e))
                    }
                  >
                    立即锁定
                  </Button>
                  <Button variant="ghost" className="text-bad hover:text-bad" onClick={() => setDialog("remove")}>
                    取消启动密码
                  </Button>
                </>
              ) : (
                <Button variant="primary" icon={LockKeyhole} onClick={() => setDialog("set")}>
                  设置启动密码
                </Button>
              )}
            </div>
            {enabled && (
              <Field label="空闲自动锁定" hint="电脑这么久没有键盘和鼠标操作后自动锁上界面。锁定只是挡住界面，正在运行的终端和任务不会中断。" className="mt-4 max-w-[320px]">
                <Select
                  value={String(lock?.autoLock ?? 0)}
                  onChange={(e) =>
                    api
                      .SetAutoLock(Number(e.target.value))
                      .then(setLock)
                      .catch((err) => toast.error("保存失败", err))
                  }
                >
                  {[...new Set([...autoLockChoices, lock?.autoLock ?? 0])]
                    .sort((a, b) => a - b)
                    .map((n) => (
                      <option key={n} value={n}>
                        {n === 0 ? "不自动锁定" : `${n} 分钟`}
                      </option>
                    ))}
                </Select>
              </Field>
            )}
          </div>
        </div>
      </Card>

      <Card className="p-5">
        <div className="text-[13.5px] font-semibold">启动密码保护什么</div>
        <ul className="mt-2 list-disc space-y-1.5 pl-5 text-[12.5px] leading-relaxed text-fg2">
          <li>
            <b className="font-semibold text-fg">已保存的密码和密钥</b>：服务器密码、sudo 密码、私钥、证书私钥、API Key 会用启动密码再加密一层。别人即使拿到 data 文件夹、甚至登录了你的 Windows 账户，没有启动密码也解不开。
          </li>
          <li>
            <b className="font-semibold text-fg">程序界面</b>：没有输入密码时什么都看不到，程序也不会连接任何服务器。
          </li>
          <li>
            <b className="font-semibold text-fg">不加密的部分</b>：服务器列表（地址、用户名）、脚本、对话记录和 AI 的记忆只是被锁屏挡住，文件本身没有加密。
          </li>
          <li>
            <b className="font-semibold text-fg">忘记密码</b>：无法找回，只能在锁屏上重置，重置会清空上面那些密码和密钥。
          </li>
          <li>设置启动密码后，不要再用 1.4 及更早的版本打开这份数据：旧版本不认识启动密码，会用解不开的密文去登录服务器。</li>
        </ul>
        {lock?.legacyDir && (
          <Callout
            tone="warn"
            className="mt-3"
            title="旧版本的数据目录里还有一份没有被启动密码保护的数据"
            action={
              <Button size="sm" variant="soft" icon={FolderOpen} className="shrink-0" onClick={() => api.OpenDir("legacy").catch((e) => toast.error("打不开目录", e))}>
                打开
              </Button>
            }
          >
            <span className="selectable font-mono">{lock.legacyDir}</span>
            ：升级到 1.4 时数据是复制过来的，旧目录里那一份还在，里面的密码只由 Windows 账户加密。确认现在一切正常后，建议把它删掉。
          </Callout>
        )}
      </Card>
      <PasswordDialog kind={dialog} onClose={() => setDialog(null)} />
    </>
  );
}
