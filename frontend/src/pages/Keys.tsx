import { useEffect, useState } from "react";
import {
  CircleCheck,
  Download,
  Ellipsis,
  FileKey,
  FolderOpen,
  KeyRound,
  Lock,
  Pencil,
  Repeat,
  Server as ServerIcon,
  Sparkles,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import { api, type KeyView, type OpResult, type PublicKeyView } from "../lib/api";
import { cn, timeAgo } from "../lib/format";
import { confirm, toast, useApp } from "../lib/store";
import { Badge, Button, Callout, Card, CopyButton, Empty, Field, IconButton, Input, Menu, Modal, PageHeader, Segmented, Switch, Tabs, Textarea } from "../components/ui";
import { ServerPicker } from "../components/widgets";
import { OpResultList } from "../components/QuickRun";

export default function Keys() {
  const { keys, loadKeys, servers, params } = useApp();
  const [tab, setTab] = useState<"private" | "public">("private");
  const [pubs, setPubs] = useState<PublicKeyView[]>([]);
  const [gen, setGen] = useState(false);
  const [imp, setImp] = useState(false);
  const [impPub, setImpPub] = useState(false);
  const [bind, setBind] = useState<{ pubId: string; name: string } | null>(null);
  const [rotate, setRotate] = useState<KeyView | null>(null);

  const loadPubs = () => api.ListPublicKeys().then((p) => setPubs(p ?? []));
  const reload = () => {
    void loadKeys();
    void loadPubs();
  };
  useEffect(reload, []);
  useEffect(() => {
    if (params.generate) setGen(true);
  }, [params.generate]);

  const serverName = (id: string) => servers.find((s) => s.id === id)?.name ?? id;

  const removeKey = async (k: KeyView) => {
    if (!(await confirm({ title: `删除私钥「${k.name}」？`, message: "删除后无法恢复（建议先导出备份）。已部署到服务器上的公钥不会被移除。", danger: true, confirmText: "删除" }))) return;
    try {
      await api.DeleteKey(k.id);
      toast.ok("已删除");
      reload();
    } catch (e) {
      toast.error("无法删除", e);
    }
  };
  const rename = async (id: string, old: string, isPub = false) => {
    const name = window.prompt("新名称", old);
    if (!name || name === old) return;
    try {
      if (isPub) await api.RenamePublicKey(id, name);
      else await api.RenameKey(id, name);
      reload();
    } catch (e) {
      toast.error("重命名失败", e);
    }
  };
  const unbind = async (p: PublicKeyView, serverId: string) => {
    if (!(await confirm({ title: `从 ${serverName(serverId)} 移除公钥「${p.name}」？`, message: "将从该服务器的 authorized_keys 中删除这一行。", danger: true, confirmText: "移除" }))) return;
    const res = await api.UnbindPublicKey(p.id, [serverId]);
    if (res[0]?.ok) toast.ok("已移除", res[0].message);
    else toast.error("移除失败", res[0]?.message);
    void loadPubs();
  };

  return (
    <div className="flex h-full flex-col">
      <PageHeader
        title="密钥管理"
        icon={KeyRound}
        description="私钥使用 Windows 账户加密保存；公钥可以一键部署到服务器的 authorized_keys 并跟踪绑定关系"
        actions={
          <>
            <Button icon={Upload} onClick={() => setImpPub(true)}>
              导入公钥
            </Button>
            <Button icon={FileKey} onClick={() => setImp(true)}>
              导入私钥
            </Button>
            <Button variant="primary" icon={Sparkles} onClick={() => setGen(true)}>
              生成密钥
            </Button>
          </>
        }
      />
      <Tabs
        className="mx-4 @2xl:mx-7"
        value={tab}
        onChange={setTab}
        tabs={[
          { key: "private", label: "私钥", icon: Lock, count: keys.length },
          { key: "public", label: "公钥与授权", icon: KeyRound, count: pubs.length },
        ]}
      />
      <div className="min-h-0 flex-1 overflow-y-auto px-4 @2xl:px-7 pt-5 pb-8">
        {tab === "private" &&
          (keys.length === 0 ? (
            <Card>
              <Empty
                icon={KeyRound}
                title="还没有私钥"
                description="生成一对新的 ED25519 密钥（推荐），或导入已有的 OpenSSH / PEM 私钥。"
                action={
                  <div className="flex gap-2">
                    <Button icon={FileKey} onClick={() => setImp(true)}>
                      导入私钥
                    </Button>
                    <Button variant="primary" icon={Sparkles} onClick={() => setGen(true)}>
                      生成密钥
                    </Button>
                  </div>
                }
              />
            </Card>
          ) : (
            <div className="grid grid-cols-[repeat(auto-fill,minmax(min(420px,100%),1fr))] gap-4">
              {keys.map((k) => (
                <Card key={k.id} className="p-4">
                  <div className="flex items-start gap-3">
                    <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl grad-accent text-white">
                      <KeyRound size={18} />
                    </div>
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="truncate text-[14px] font-semibold">{k.name}</span>
                        <Badge tone="accent">{k.type}</Badge>
                        {k.encrypted && (
                          <Badge icon={Lock} title="私钥带密码短语">
                            已加密
                          </Badge>
                        )}
                      </div>
                      <div className="mt-1 flex items-center gap-1 font-mono text-[11.5px] text-muted">
                        <span className="truncate">{k.fingerprint}</span>
                      </div>
                    </div>
                    <Menu
                      trigger={<IconButton icon={Ellipsis} size="sm" />}
                      items={[
                        { label: "重命名", icon: Pencil, onClick: () => rename(k.id, k.name) },
                        {
                          label: "导出私钥",
                          icon: Download,
                          onClick: async () => {
                            try {
                              const p = await api.ExportPrivateKey(k.id);
                              if (p) toast.ok("已导出", p);
                            } catch (e) {
                              toast.error("导出失败", e);
                            }
                          },
                        },
                        { label: "", onClick: () => {}, divider: true },
                        { label: "删除", icon: Trash2, danger: true, onClick: () => removeKey(k) },
                      ]}
                    />
                  </div>
                  <div className="mt-3 flex items-center gap-2 rounded-lg border border-line bg-bg2 px-3 py-2">
                    <span className="min-w-0 flex-1 truncate font-mono text-[11.5px] text-fg2">{k.publicKey}</span>
                    <CopyButton text={k.publicKey} tip="复制公钥" />
                  </div>
                  <div className="mt-3 flex items-center justify-between gap-2">
                    <div className="flex min-w-0 flex-wrap items-center gap-1.5 text-[12px] text-muted">
                      <ServerIcon size={13} />
                      {k.usedBy.length === 0 ? (
                        "未用于登录"
                      ) : (
                        <>
                          登录：
                          {k.usedBy.slice(0, 3).map((id) => (
                            <Badge key={id}>{serverName(id)}</Badge>
                          ))}
                          {k.usedBy.length > 3 && <span>+{k.usedBy.length - 3}</span>}
                        </>
                      )}
                    </div>
                    <div className="flex shrink-0 gap-1.5">
                      <Button size="sm" icon={Repeat} onClick={() => setRotate(k)}>
                        切换登录
                      </Button>
                      <Button size="sm" variant="soft" icon={Upload} onClick={() => k.publicKeyId && setBind({ pubId: k.publicKeyId, name: k.name })}>
                        部署公钥
                      </Button>
                    </div>
                  </div>
                  <div className="mt-2 text-[11px] text-muted">创建于 {timeAgo(k.createdAt)}</div>
                </Card>
              ))}
            </div>
          ))}

        {tab === "public" &&
          (pubs.length === 0 ? (
            <Card>
              <Empty icon={KeyRound} title="公钥库为空" description="生成或导入私钥时会自动加入对应公钥；也可以导入同事、CI 等其他人的公钥统一部署。" action={<Button icon={Upload} onClick={() => setImpPub(true)}>导入公钥</Button>} />
            </Card>
          ) : (
            <div className="space-y-3">
              {pubs.map((p) => (
                <Card key={p.id} className="p-4">
                  <div className="flex items-start gap-3">
                    <div className={cn("flex h-9 w-9 shrink-0 items-center justify-center rounded-xl", p.privateKeyId ? "bg-accent/12 text-accent" : "bg-info/12 text-info")}>
                      <KeyRound size={16} />
                    </div>
                    <div className="min-w-0 flex-1">
                      <div className="flex items-center gap-2">
                        <span className="truncate text-[13.5px] font-semibold">{p.name}</span>
                        <Badge>{p.type}</Badge>
                        <Badge tone={p.privateKeyId ? "accent" : "info"}>{p.privateKeyId ? "本地私钥" : "外部导入"}</Badge>
                      </div>
                      <div className="mt-1 flex items-center gap-1">
                        <span className="truncate font-mono text-[11.5px] text-muted">{p.fingerprint}</span>
                        <CopyButton text={p.key} tip="复制公钥" />
                      </div>
                    </div>
                    <div className="flex shrink-0 gap-1">
                      <Button size="sm" variant="soft" icon={Upload} onClick={() => setBind({ pubId: p.id, name: p.name })}>
                        部署到服务器
                      </Button>
                      <Menu
                        trigger={<IconButton icon={Ellipsis} size="sm" />}
                        items={[
                          { label: "重命名", icon: Pencil, onClick: () => rename(p.id, p.name, true) },
                          {
                            label: "删除",
                            icon: Trash2,
                            danger: true,
                            disabled: !!p.privateKeyId,
                            onClick: async () => {
                              if (!(await confirm({ title: `删除公钥「${p.name}」？`, message: "只删除本地记录，不会修改服务器上的 authorized_keys。", danger: true }))) return;
                              try {
                                await api.DeletePublicKey(p.id);
                                void loadPubs();
                              } catch (e) {
                                toast.error("删除失败", e);
                              }
                            },
                          },
                        ]}
                      />
                    </div>
                  </div>
                  <div className="mt-3 flex flex-wrap items-center gap-1.5 border-t border-line pt-3">
                    <span className="mr-1 text-[12px] text-muted">已授权服务器</span>
                    {p.bindings.length === 0 && <span className="text-[12px] text-muted/70">（尚未部署）</span>}
                    {p.bindings.map((b) => (
                      <span key={b.serverId} className="inline-flex h-6 items-center gap-1 rounded-md border border-ok/25 bg-ok/8 pr-1 pl-2 text-[12px] text-fg2">
                        <CircleCheck size={11} className="text-ok" />
                        {b.serverName}
                        <button type="button" data-tip="解绑并从服务器移除" className="rounded p-0.5 text-muted hover:bg-bad/15 hover:text-bad" onClick={() => unbind(p, b.serverId)}>
                          <X size={11} />
                        </button>
                      </span>
                    ))}
                  </div>
                </Card>
              ))}
            </div>
          ))}
      </div>

      <GenerateModal
        open={gen}
        onClose={() => setGen(false)}
        onDone={(k) => {
          reload();
          if (k.publicKeyId) setBind({ pubId: k.publicKeyId, name: k.name });
        }}
      />
      <ImportKeyModal open={imp} onClose={() => setImp(false)} onDone={reload} />
      <ImportPubModal open={impPub} onClose={() => setImpPub(false)} onDone={loadPubs} />
      <BindModal target={bind} onClose={() => setBind(null)} onDone={loadPubs} />
      <RotateModal keyView={rotate} onClose={() => setRotate(null)} onDone={reload} />
    </div>
  );
}

function GenerateModal({ open, onClose, onDone }: { open: boolean; onClose: () => void; onDone: (k: KeyView) => void }) {
  const [name, setName] = useState("");
  const [kind, setKind] = useState<"ed25519" | "rsa" | "ecdsa">("ed25519");
  const [comment, setComment] = useState("");
  const [passphrase, setPassphrase] = useState("");
  const [busy, setBusy] = useState(false);
  const [created, setCreated] = useState<KeyView | null>(null);
  useEffect(() => {
    if (open) {
      setName("");
      setComment("");
      setPassphrase("");
      setCreated(null);
      setKind("ed25519");
    }
  }, [open]);
  const submit = async () => {
    setBusy(true);
    try {
      const k = await api.GenerateKey({ name, kind, bits: kind === "rsa" ? 4096 : 0, comment, passphrase });
      setCreated(k);
      toast.ok("密钥已生成", k.fingerprint);
    } catch (e) {
      toast.error("生成失败", e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      open={open}
      onClose={onClose}
      title="生成 SSH 密钥对"
      icon={Sparkles}
      description="私钥加密保存在本机，公钥可以部署到任意服务器"
      footer={
        created ? (
          <>
            <Button variant="ghost" onClick={onClose}>
              完成
            </Button>
            <Button
              variant="primary"
              icon={Upload}
              onClick={() => {
                onDone(created);
                onClose();
              }}
            >
              部署到服务器
            </Button>
          </>
        ) : (
          <>
            <Button variant="ghost" onClick={onClose}>
              取消
            </Button>
            <Button variant="primary" loading={busy} onClick={submit}>
              生成
            </Button>
          </>
        )
      }
    >
      {created ? (
        <div className="space-y-3">
          <Callout tone="ok" title={`「${created.name}」已生成`}>
            {created.type} · {created.fingerprint}
          </Callout>
          <Field label="公钥（authorized_keys 格式）">
            <div className="flex items-start gap-2 rounded-xl border border-line2 bg-bg2 p-3">
              <code className="min-w-0 flex-1 font-mono text-[11.5px] break-all text-fg2">{created.publicKey}</code>
              <CopyButton text={created.publicKey} />
            </div>
          </Field>
        </div>
      ) : (
        <div className="space-y-4">
          <Field label="名称">
            <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 ops-2026" autoFocus />
          </Field>
          <Field label="算法">
            <Segmented
              value={kind}
              onChange={setKind}
              options={[
                { value: "ed25519", label: "ED25519（推荐）" },
                { value: "rsa", label: "RSA 4096" },
                { value: "ecdsa", label: "ECDSA P-256" },
              ]}
            />
          </Field>
          <Field label="注释" hint="会写在公钥末尾，便于在 authorized_keys 中识别">
            <Input value={comment} onChange={(e) => setComment(e.target.value)} placeholder="默认 sshpilot-名称" />
          </Field>
          <Field label="密码短语（可选）" hint="为私钥文件再加一层保护；会加密保存以便自动连接">
            <Input type="password" value={passphrase} onChange={(e) => setPassphrase(e.target.value)} />
          </Field>
        </div>
      )}
    </Modal>
  );
}

function ImportKeyModal({ open, onClose, onDone }: { open: boolean; onClose: () => void; onDone: () => void }) {
  const [name, setName] = useState("");
  const [pem, setPem] = useState("");
  const [passphrase, setPassphrase] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (open) {
      setName("");
      setPem("");
      setPassphrase("");
    }
  }, [open]);
  const pick = async () => {
    try {
      const f = await api.PickTextFile("选择私钥文件");
      if (f) {
        setPem(f.content);
        if (!name) setName(f.name.replace(/\.(pem|key)$/i, ""));
      }
    } catch (e) {
      toast.error("读取文件失败", e);
    }
  };
  const submit = async () => {
    setBusy(true);
    try {
      const k = await api.ImportKey({ name, pem, passphrase });
      toast.ok("私钥已导入", `${k.type} · ${k.fingerprint}`);
      onDone();
      onClose();
    } catch (e) {
      toast.error("导入失败", e);
    } finally {
      setBusy(false);
    }
  };
  return (
    <Modal
      open={open}
      onClose={onClose}
      title="导入私钥"
      icon={FileKey}
      description="支持 OpenSSH、PKCS#1、PKCS#8 格式（RSA / ECDSA / ED25519）"
      size="lg"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button variant="primary" loading={busy} onClick={submit} disabled={!pem.trim()}>
            导入
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Field label="名称">
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 id_ed25519" />
        </Field>
        <Field
          label="私钥内容"
          action={
            <Button size="xs" variant="ghost" icon={FolderOpen} onClick={pick}>
              从文件选择
            </Button>
          }
        >
          <Textarea mono value={pem} onChange={(e) => setPem(e.target.value)} placeholder="-----BEGIN OPENSSH PRIVATE KEY-----" className="min-h-[180px]" />
        </Field>
        <Field label="密码短语" hint="私钥加密时填写">
          <Input type="password" value={passphrase} onChange={(e) => setPassphrase(e.target.value)} />
        </Field>
      </div>
    </Modal>
  );
}

function ImportPubModal({ open, onClose, onDone }: { open: boolean; onClose: () => void; onDone: () => void }) {
  const [name, setName] = useState("");
  const [line, setLine] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (open) {
      setName("");
      setLine("");
    }
  }, [open]);
  return (
    <Modal
      open={open}
      onClose={onClose}
      title="导入公钥"
      icon={Upload}
      description="例如同事、CI/CD 或堡垒机的公钥，导入后可统一部署和回收"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            取消
          </Button>
          <Button
            variant="primary"
            loading={busy}
            disabled={!line.trim()}
            onClick={async () => {
              setBusy(true);
              try {
                await api.ImportPublicKey(name, line);
                toast.ok("公钥已导入");
                onDone();
                onClose();
              } catch (e) {
                toast.error("导入失败", e);
              } finally {
                setBusy(false);
              }
            }}
          >
            导入
          </Button>
        </>
      }
    >
      <div className="space-y-4">
        <Field label="名称" hint="留空时使用公钥注释">
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="例如 张三 / gitlab-runner" />
        </Field>
        <Field
          label="公钥"
          action={
            <Button
              size="xs"
              variant="ghost"
              icon={FolderOpen}
              onClick={async () => {
                const f = await api.PickTextFile("选择公钥文件");
                if (f) setLine(f.content.trim());
              }}
            >
              从文件选择
            </Button>
          }
        >
          <Textarea mono value={line} onChange={(e) => setLine(e.target.value)} placeholder="ssh-ed25519 AAAAC3Nza... user@host" />
        </Field>
      </div>
    </Modal>
  );
}

function BindModal({ target, onClose, onDone }: { target: { pubId: string; name: string } | null; onClose: () => void; onDone: () => void }) {
  const [sel, setSel] = useState<string[]>([]);
  const [busy, setBusy] = useState(false);
  const [res, setRes] = useState<OpResult[] | null>(null);
  useEffect(() => {
    if (target) {
      setSel([]);
      setRes(null);
    }
  }, [target]);
  return (
    <Modal
      open={!!target}
      onClose={onClose}
      title={`部署公钥「${target?.name ?? ""}」`}
      icon={Upload}
      description="追加到所选服务器登录用户的 ~/.ssh/authorized_keys，自动修正目录权限，已存在时跳过"
      size="lg"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            关闭
          </Button>
          <Button
            variant="primary"
            loading={busy}
            disabled={sel.length === 0}
            onClick={async () => {
              if (!target) return;
              setBusy(true);
              try {
                const r = await api.BindPublicKey(target.pubId, sel);
                setRes(r);
                const ok = r.filter((x) => x.ok).length;
                toast[ok === r.length ? "ok" : "warn"](`部署完成：${ok}/${r.length} 台成功`);
                onDone();
              } finally {
                setBusy(false);
              }
            }}
          >
            部署到 {sel.length} 台服务器
          </Button>
        </>
      }
    >
      {res ? <OpResultList results={res} /> : <ServerPicker value={sel} onChange={setSel} maxHeight={360} />}
    </Modal>
  );
}

function RotateModal({ keyView, onClose, onDone }: { keyView: KeyView | null; onClose: () => void; onDone: () => void }) {
  const { servers, loadServers } = useApp();
  const [sel, setSel] = useState<string[]>([]);
  const [removeOld, setRemoveOld] = useState(true);
  const [busy, setBusy] = useState(false);
  const [res, setRes] = useState<OpResult[] | null>(null);
  useEffect(() => {
    if (keyView) {
      setSel([]);
      setRes(null);
    }
  }, [keyView]);
  const exclude = keyView ? servers.filter((s) => s.authType === "key" && s.keyId === keyView.id).map((s) => s.id) : [];
  return (
    <Modal
      open={!!keyView}
      onClose={onClose}
      title={`切换登录密钥为「${keyView?.name ?? ""}」`}
      icon={Repeat}
      description="依次执行：部署新公钥 → 用新密钥验证登录 → 切换服务器配置 → （可选）移除旧公钥。验证失败时保持原配置不变。"
      size="lg"
      footer={
        <>
          <Button variant="ghost" onClick={onClose}>
            关闭
          </Button>
          <Button
            variant="primary"
            loading={busy}
            disabled={sel.length === 0 || !!res}
            onClick={async () => {
              if (!keyView) return;
              setBusy(true);
              try {
                setRes(await api.RotateKey(keyView.id, sel, removeOld));
                onDone();
                void loadServers();
              } finally {
                setBusy(false);
              }
            }}
          >
            开始切换
          </Button>
        </>
      }
    >
      {res ? (
        <OpResultList results={res} />
      ) : (
        <div className="space-y-4">
          <ServerPicker value={sel} onChange={setSel} exclude={exclude} maxHeight={300} />
          <div className="flex items-center justify-between rounded-xl border border-line bg-bg2/40 px-4 py-3">
            <div>
              <div className="text-[13px] font-medium">切换成功后移除旧的登录公钥</div>
              <div className="text-[12px] text-muted">仅对原先使用密钥登录的服务器生效；密码登录的服务器不受影响</div>
            </div>
            <Switch checked={removeOld} onChange={setRemoveOld} />
          </div>
        </div>
      )}
    </Modal>
  );
}

