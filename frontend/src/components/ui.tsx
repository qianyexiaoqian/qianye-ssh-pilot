import {
  forwardRef,
  isValidElement,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ButtonHTMLAttributes,
  type ComponentType,
  type InputHTMLAttributes,
  type ReactNode,
  type SelectHTMLAttributes,
  type TextareaHTMLAttributes,
} from "react";
import { createPortal } from "react-dom";
import { Check, ChevronDown, CircleAlert, CircleCheck, Copy, Eye, EyeOff, LoaderCircle, TriangleAlert, X } from "lucide-react";
import { cn } from "../lib/format";
import { runtime } from "../lib/api";

type IconType = ComponentType<{ size?: number; className?: string; strokeWidth?: number }>;

// ---- Button ----

type ButtonVariant = "primary" | "secondary" | "ghost" | "danger" | "soft" | "outline";
type ButtonSize = "xs" | "sm" | "md" | "lg";

export interface ButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  variant?: ButtonVariant;
  size?: ButtonSize;
  icon?: IconType;
  iconRight?: IconType;
  loading?: boolean;
}

const btnVariant: Record<ButtonVariant, string> = {
  primary: "grad-accent text-white shadow-[var(--glow)] hover:brightness-110 active:brightness-95",
  secondary: "bg-panel2 border border-line2 text-fg hover:bg-panel3",
  outline: "border border-line2 text-fg2 hover:text-fg hover:bg-panel2",
  ghost: "text-fg2 hover:bg-panel2 hover:text-fg",
  danger: "bg-bad text-white hover:brightness-110",
  soft: "bg-accent/12 text-accent hover:bg-accent/20",
};

const btnSize: Record<ButtonSize, string> = {
  xs: "h-6 px-2 text-[12px] gap-1 rounded-md",
  sm: "h-7 px-2.5 text-[12.5px] gap-1.5 rounded-lg",
  md: "h-[34px] px-3.5 text-[13px] gap-2 rounded-[10px]",
  lg: "h-10 px-5 text-[14px] gap-2 rounded-xl",
};

const iconSize: Record<ButtonSize, number> = { xs: 13, sm: 14, md: 15, lg: 16 };

export const Button = forwardRef<HTMLButtonElement, ButtonProps>(function Button(
  { variant = "secondary", size = "md", icon: Icon, iconRight: IconRight, loading, disabled, className, children, type = "button", ...rest },
  ref,
) {
  return (
    <button
      ref={ref}
      type={type}
      disabled={disabled || loading}
      className={cn(
        "inline-flex shrink-0 items-center justify-center font-medium whitespace-nowrap transition-all duration-150 select-none",
        "focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent/50 disabled:pointer-events-none disabled:opacity-45",
        btnVariant[variant],
        btnSize[size],
        className,
      )}
      {...rest}
    >
      {loading ? <LoaderCircle size={iconSize[size]} className="animate-spin" /> : Icon ? <Icon size={iconSize[size]} /> : null}
      {children}
      {IconRight && !loading ? <IconRight size={iconSize[size]} /> : null}
    </button>
  );
});

export interface IconButtonProps extends ButtonHTMLAttributes<HTMLButtonElement> {
  icon: IconType;
  tip?: string;
  tipPos?: "top" | "bottom" | "right";
  size?: "sm" | "md" | "lg";
  active?: boolean;
  tone?: "default" | "danger";
  loading?: boolean;
}

export function IconButton({ icon: Icon, tip, tipPos, size = "md", active, tone = "default", loading, className, disabled, ...rest }: IconButtonProps) {
  const dim = size === "sm" ? "h-7 w-7 rounded-lg" : size === "lg" ? "h-10 w-10 rounded-xl" : "h-8 w-8 rounded-[9px]";
  return (
    <button
      type="button"
      data-tip={tip}
      data-tip-pos={tipPos}
      disabled={disabled || loading}
      className={cn(
        "inline-flex shrink-0 items-center justify-center transition-colors disabled:opacity-40 disabled:pointer-events-none",
        dim,
        active ? "bg-accent/15 text-accent" : tone === "danger" ? "text-muted hover:bg-bad/12 hover:text-bad" : "text-muted hover:bg-panel3 hover:text-fg",
        className,
      )}
      {...rest}
    >
      {loading ? <LoaderCircle size={size === "sm" ? 14 : 16} className="animate-spin" /> : <Icon size={size === "sm" ? 14 : size === "lg" ? 18 : 16} />}
    </button>
  );
}

// ---- 表单 ----

const fieldBase =
  "w-full rounded-[10px] bg-bg2 border border-line2 text-[13px] text-fg placeholder:text-muted/70 outline-none transition " +
  "focus:border-accent/60 focus:ring-[3px] focus:ring-accent/15 disabled:opacity-60";

export interface InputProps extends InputHTMLAttributes<HTMLInputElement> {
  icon?: IconType;
  right?: ReactNode;
  mono?: boolean;
}

export const Input = forwardRef<HTMLInputElement, InputProps>(function Input({ icon: Icon, right, mono, className, ...rest }, ref) {
  return (
    <div className="relative">
      {Icon && <Icon size={15} className="pointer-events-none absolute top-1/2 left-3 -translate-y-1/2 text-muted" />}
      <input
        ref={ref}
        spellCheck={false}
        className={cn(fieldBase, "h-[34px] px-3", Icon && "pl-9", !!right && "pr-10", mono && "font-mono text-[12.5px]", className)}
        {...rest}
      />
      {right && <div className="absolute top-1/2 right-1.5 -translate-y-1/2">{right}</div>}
    </div>
  );
});

/** 密码输入框：内容默认遮住，点眼睛才显示；copyTip 不为空时，里面有内容就可以直接复制（不用先显示出来） */
export function PasswordInput({ copyTip, className, ...rest }: Omit<InputProps, "type" | "right" | "mono"> & { copyTip?: string }) {
  const [show, setShow] = useState(false);
  const value = typeof rest.value === "string" ? rest.value : "";
  const canCopy = !!copyTip && !!value;
  return (
    <Input
      autoComplete="off"
      {...rest}
      mono={show}
      type={show ? "text" : "password"}
      className={cn(canCopy && "pr-[68px]", className)}
      right={
        <div className="flex items-center">
          <IconButton size="sm" icon={show ? EyeOff : Eye} tip={show ? "遮住" : "显示"} onClick={() => setShow((v) => !v)} />
          {canCopy && <CopyButton text={value} tip={copyTip} />}
        </div>
      }
    />
  );
}

export interface TextareaProps extends TextareaHTMLAttributes<HTMLTextAreaElement> {
  mono?: boolean;
}

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaProps>(function Textarea({ mono, className, ...rest }, ref) {
  return (
    <textarea
      ref={ref}
      spellCheck={false}
      className={cn(fieldBase, "min-h-[84px] resize-y px-3 py-2 leading-relaxed", mono && "font-mono text-[12px]", className)}
      {...rest}
    />
  );
});

export function Select({ className, children, ...rest }: SelectHTMLAttributes<HTMLSelectElement>) {
  return (
    <div className="relative">
      <select className={cn(fieldBase, "h-[34px] appearance-none pr-8 pl-3", className)} {...rest}>
        {children}
      </select>
      <ChevronDown size={14} className="pointer-events-none absolute top-1/2 right-2.5 -translate-y-1/2 text-muted" />
    </div>
  );
}

export function Field({
  label,
  hint,
  error,
  required,
  children,
  className,
  action,
}: {
  label?: ReactNode;
  hint?: ReactNode;
  error?: string;
  required?: boolean;
  children: ReactNode;
  className?: string;
  action?: ReactNode;
}) {
  // 分段选择器等按钮类控件不能放在 <label> 里，否则点击标题文字会误触第一个选项
  const Tag = isValidElement(children) && children.type === Segmented ? "div" : "label";
  return (
    <Tag className={cn("block", className)}>
      {(label || action) && (
        <div className="mb-1.5 flex items-center justify-between gap-2">
          <span className="text-[12.5px] font-medium text-fg2">
            {label}
            {required && <span className="ml-0.5 text-bad">*</span>}
          </span>
          {action}
        </div>
      )}
      {children}
      {error ? <div className="mt-1 text-[12px] text-bad">{error}</div> : hint ? <div className="mt-1 text-[12px] leading-relaxed text-muted">{hint}</div> : null}
    </Tag>
  );
}

export function Switch({ checked, onChange, disabled, size = "md" }: { checked: boolean; onChange: (v: boolean) => void; disabled?: boolean; size?: "sm" | "md" }) {
  const w = size === "sm" ? "w-8 h-[18px]" : "w-10 h-[22px]";
  const knob = size === "sm" ? "h-3.5 w-3.5" : "h-[18px] w-[18px]";
  const shift = size === "sm" ? "translate-x-[14px]" : "translate-x-[18px]";
  return (
    <button
      type="button"
      role="switch"
      aria-checked={checked}
      disabled={disabled}
      onClick={() => onChange(!checked)}
      className={cn("relative inline-flex shrink-0 items-center rounded-full p-0.5 transition-colors disabled:opacity-50", w, checked ? "grad-accent" : "bg-panel3 border border-line2")}
    >
      <span className={cn("rounded-full bg-white shadow transition-transform", knob, checked ? shift : "translate-x-0")} />
    </button>
  );
}

export interface SegOption<T extends string> {
  value: T;
  label: ReactNode;
  icon?: IconType;
}

export function Segmented<T extends string>({ options, value, onChange, size = "md", className }: { options: SegOption<T>[]; value: T; onChange: (v: T) => void; size?: "sm" | "md"; className?: string }) {
  return (
    <div className={cn("inline-flex max-w-full overflow-x-auto rounded-[10px] border border-line bg-bg2 p-0.5", className)}>
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          onClick={() => onChange(o.value)}
          className={cn(
            "inline-flex shrink-0 items-center gap-1.5 rounded-lg font-medium whitespace-nowrap transition-all",
            size === "sm" ? "h-6 px-2.5 text-[12px]" : "h-[28px] px-3 text-[12.5px]",
            value === o.value ? "bg-panel3 text-fg shadow-sm" : "text-muted hover:text-fg",
          )}
        >
          {o.icon && <o.icon size={13} />}
          {o.label}
        </button>
      ))}
    </div>
  );
}

// ---- 展示 ----

export type Tone = "neutral" | "accent" | "ok" | "warn" | "serious" | "bad" | "info";

const badgeTone: Record<Tone, string> = {
  neutral: "bg-panel3 text-fg2 border-line",
  accent: "bg-accent/12 text-accent border-accent/25",
  ok: "bg-ok/12 text-ok border-ok/25",
  warn: "bg-warn/12 text-warn border-warn/25",
  serious: "bg-serious/12 text-serious border-serious/25",
  bad: "bg-bad/12 text-bad border-bad/25",
  info: "bg-info/12 text-info border-info/25",
};

export function Badge({ tone = "neutral", icon: Icon, children, className, title }: { tone?: Tone; icon?: IconType; children: ReactNode; className?: string; title?: string }) {
  return (
    <span title={title} className={cn("inline-flex h-[22px] shrink-0 items-center gap-1 rounded-md border px-1.5 text-[11.5px] font-medium whitespace-nowrap", badgeTone[tone], className)}>
      {Icon && <Icon size={12} />}
      {children}
    </span>
  );
}

export function StatusDot({ status, className }: { status?: string; className?: string }) {
  const color = status === "online" ? "bg-ok" : status === "offline" ? "bg-bad" : "bg-muted/60";
  return (
    <span className={cn("relative inline-flex h-2 w-2 shrink-0", className)}>
      {status === "online" && <span className="absolute inset-0 animate-ping rounded-full bg-ok opacity-40" />}
      <span className={cn("relative inline-flex h-2 w-2 rounded-full", color)} />
    </span>
  );
}

export function Card({ className, children, onClick }: { className?: string; children: ReactNode; onClick?: () => void }) {
  return (
    <div className={cn("card", className)} onClick={onClick}>
      {children}
    </div>
  );
}

export function Spinner({ size = 16, className }: { size?: number; className?: string }) {
  return <LoaderCircle size={size} className={cn("animate-spin text-muted", className)} />;
}

export function Empty({ icon: Icon, title, description, action, className }: { icon?: IconType; title: string; description?: ReactNode; action?: ReactNode; className?: string }) {
  return (
    <div className={cn("flex flex-col items-center justify-center px-6 py-14 text-center", className)}>
      {Icon && (
        <div className="mb-4 flex h-14 w-14 items-center justify-center rounded-2xl border border-line bg-panel2 text-muted">
          <Icon size={24} strokeWidth={1.6} />
        </div>
      )}
      <div className="text-[14.5px] font-semibold text-fg">{title}</div>
      {description && <div className="mt-1.5 max-w-sm text-[13px] leading-relaxed text-muted">{description}</div>}
      {action && <div className="mt-5">{action}</div>}
    </div>
  );
}

/** 用量条：填充色表示严重程度（accent → warn → bad），轨道使用同色系的浅色 */
export function Meter({ value, tone, className, height = 6, title }: { value: number; tone?: "accent" | "ok" | "warn" | "bad"; className?: string; height?: number; title?: string }) {
  const v = Math.max(0, Math.min(100, value || 0));
  const t = tone ?? (v >= 95 ? "bad" : v >= 85 ? "warn" : "accent");
  const fill = { accent: "bg-accent", ok: "bg-ok", warn: "bg-warn", bad: "bg-bad" }[t];
  const track = { accent: "bg-accent/15", ok: "bg-ok/15", warn: "bg-warn/18", bad: "bg-bad/18" }[t];
  return (
    <div title={title} className={cn("w-full overflow-hidden rounded-full", track, className)} style={{ height }}>
      <div className={cn("h-full rounded-full transition-[width] duration-500", fill)} style={{ width: `${v}%` }} />
    </div>
  );
}

export function Kbd({ children }: { children: ReactNode }) {
  return <kbd className="inline-flex h-5 min-w-5 items-center justify-center rounded border border-line2 bg-panel2 px-1 font-mono text-[10.5px] text-muted">{children}</kbd>;
}

export function CopyButton({ text, tip = "复制", size = "sm", className }: { text: string; tip?: string; size?: "sm" | "md"; className?: string }) {
  const [done, setDone] = useState(false);
  return (
    <IconButton
      icon={done ? Check : Copy}
      tip={done ? "已复制" : tip}
      size={size}
      className={cn(done && "text-ok", className)}
      onClick={(e) => {
        e.stopPropagation();
        runtime.ClipboardSetText(text);
        setDone(true);
        setTimeout(() => setDone(false), 1400);
      }}
    />
  );
}

export function PageHeader({ title, description, icon: Icon, actions }: { title: string; description?: ReactNode; icon?: IconType; actions?: ReactNode }) {
  return (
    <div className="flex flex-wrap items-end justify-between gap-x-4 gap-y-3 px-4 pt-4 pb-4 @2xl:px-7 @2xl:pt-6 @2xl:pb-5">
      <div className="flex min-w-0 items-center gap-3.5">
        {Icon && (
          <div className="flex h-10 w-10 shrink-0 items-center justify-center rounded-xl border border-line2 bg-panel2 text-accent">
            <Icon size={19} />
          </div>
        )}
        <div className="min-w-0">
          <h1 className="text-[19px] font-semibold tracking-tight text-fg">{title}</h1>
          {description && <p className="mt-0.5 truncate text-[12.5px] text-muted">{description}</p>}
        </div>
      </div>
      {actions && <div className="flex max-w-full min-w-0 flex-wrap items-center gap-2">{actions}</div>}
    </div>
  );
}

export function SectionTitle({ children, action, className }: { children: ReactNode; action?: ReactNode; className?: string }) {
  return (
    <div className={cn("mb-3 flex items-center justify-between", className)}>
      <h3 className="text-[13px] font-semibold text-fg">{children}</h3>
      {action}
    </div>
  );
}

export interface TabItem<T extends string> {
  key: T;
  label: ReactNode;
  icon?: IconType;
  count?: number;
}

export function Tabs<T extends string>({ tabs, value, onChange, className }: { tabs: TabItem<T>[]; value: T; onChange: (v: T) => void; className?: string }) {
  return (
    <div className={cn("flex items-center gap-1 overflow-x-auto overflow-y-hidden border-b border-line", className)}>
      {tabs.map((t) => (
        <button
          key={t.key}
          type="button"
          onClick={() => onChange(t.key)}
          className={cn(
            "relative inline-flex h-9 shrink-0 items-center gap-1.5 border-b-2 px-3 text-[13px] font-medium whitespace-nowrap transition-colors",
            value === t.key ? "border-accent text-fg" : "border-transparent text-muted hover:text-fg",
          )}
        >
          {t.icon && <t.icon size={14} />}
          {t.label}
          {t.count !== undefined && <span className={cn("rounded-md px-1.5 text-[11px]", value === t.key ? "bg-accent/15 text-accent" : "bg-panel3 text-muted")}>{t.count}</span>}
        </button>
      ))}
    </div>
  );
}

// ---- 浮层 ----

function useEscape(open: boolean, onClose: () => void) {
  useEffect(() => {
    if (!open) return;
    const h = (e: KeyboardEvent) => {
      if (e.key === "Escape") onClose();
    };
    window.addEventListener("keydown", h);
    return () => window.removeEventListener("keydown", h);
  }, [open, onClose]);
}

export function Modal({
  open,
  onClose,
  title,
  description,
  icon: Icon,
  size = "md",
  children,
  footer,
  bodyClassName,
}: {
  open: boolean;
  onClose: () => void;
  title: ReactNode;
  description?: ReactNode;
  icon?: IconType;
  size?: "sm" | "md" | "lg" | "xl" | "2xl";
  children: ReactNode;
  footer?: ReactNode;
  bodyClassName?: string;
}) {
  useEscape(open, onClose);
  if (!open) return null;
  const width = { sm: "max-w-[420px]", md: "max-w-[560px]", lg: "max-w-[720px]", xl: "max-w-[920px]", "2xl": "max-w-[1120px]" }[size];
  return createPortal(
    <div className="fixed inset-0 z-50 flex items-center justify-center p-3 sm:p-6">
      <div className="absolute inset-0 animate-fade-in bg-black/55 backdrop-blur-[3px]" onMouseDown={onClose} />
      <div className={cn("card relative flex max-h-full w-full animate-pop flex-col overflow-hidden !rounded-[18px] border-line2", width)}>
        <div className="flex items-start gap-3 border-b border-line px-5 py-4">
          {Icon && (
            <div className="mt-0.5 flex h-9 w-9 shrink-0 items-center justify-center rounded-xl bg-accent/12 text-accent">
              <Icon size={18} />
            </div>
          )}
          <div className="min-w-0 flex-1">
            <div className="text-[15px] font-semibold text-fg">{title}</div>
            {description && <div className="mt-0.5 text-[12.5px] leading-relaxed text-muted">{description}</div>}
          </div>
          <IconButton icon={X} size="sm" onClick={onClose} />
        </div>
        <div className={cn("min-h-0 flex-1 overflow-y-auto px-5 py-4", bodyClassName)}>{children}</div>
        {footer && <div className="flex flex-wrap items-center justify-end gap-2 border-t border-line bg-bg2/40 px-5 py-3">{footer}</div>}
      </div>
    </div>,
    document.body,
  );
}

export function Drawer({ open, onClose, title, subtitle, width = 560, children, actions }: { open: boolean; onClose: () => void; title: ReactNode; subtitle?: ReactNode; width?: number; children: ReactNode; actions?: ReactNode }) {
  useEscape(open, onClose);
  if (!open) return null;
  return createPortal(
    <div className="fixed inset-0 z-40 flex justify-end">
      <div className="absolute inset-0 animate-fade-in bg-black/40" onMouseDown={onClose} />
      <div className="relative flex h-full max-w-full animate-slide-left flex-col border-l border-line2 bg-panel shadow-2xl" style={{ width }}>
        <div className="flex items-start gap-3 border-b border-line px-5 pt-11 pb-4">
          <div className="min-w-0 flex-1">
            <div className="truncate text-[15px] font-semibold">{title}</div>
            {subtitle && <div className="mt-0.5 truncate text-[12.5px] text-muted">{subtitle}</div>}
          </div>
          {actions}
          <IconButton icon={X} size="sm" onClick={onClose} />
        </div>
        <div className="min-h-0 flex-1 overflow-y-auto">{children}</div>
      </div>
    </div>,
    document.body,
  );
}

export interface MenuItem {
  label: string;
  icon?: IconType;
  onClick: () => void;
  danger?: boolean;
  disabled?: boolean;
  divider?: boolean;
}

export function Menu({ trigger, items, align = "right" }: { trigger: ReactNode; items: MenuItem[]; align?: "left" | "right" }) {
  const [open, setOpen] = useState(false);
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!open) return;
    const h = (e: MouseEvent) => {
      if (ref.current && !ref.current.contains(e.target as Node)) setOpen(false);
    };
    window.addEventListener("mousedown", h);
    return () => window.removeEventListener("mousedown", h);
  }, [open]);
  return (
    <div ref={ref} className="relative" onClick={(e) => e.stopPropagation()}>
      <div onClick={() => setOpen((v) => !v)}>{trigger}</div>
      {open && (
        <div className={cn("absolute top-full z-30 mt-1 max-h-[min(360px,70vh)] max-w-[min(320px,calc(100vw-24px))] min-w-[168px] animate-pop overflow-y-auto rounded-xl border border-line2 bg-panel2 p-1 shadow-2xl", align === "right" ? "right-0" : "left-0")}>
          {items.map((it, i) =>
            it.divider ? (
              <div key={i} className="my-1 h-px bg-line" />
            ) : (
              <button
                key={i}
                type="button"
                disabled={it.disabled}
                onClick={() => {
                  setOpen(false);
                  it.onClick();
                }}
                className={cn(
                  "flex h-8 w-full items-center gap-2.5 rounded-lg px-2.5 text-left text-[13px] transition-colors disabled:opacity-40",
                  it.danger ? "text-bad hover:bg-bad/12" : "text-fg2 hover:bg-panel3 hover:text-fg",
                )}
              >
                {it.icon && <it.icon size={14} className="shrink-0" />}
                <span className="truncate">{it.label}</span>
              </button>
            ),
          )}
        </div>
      )}
    </div>
  );
}

export function TagInput({ value, onChange, placeholder, suggestions = [] }: { value: string[]; onChange: (v: string[]) => void; placeholder?: string; suggestions?: string[] }) {
  const [text, setText] = useState("");
  const add = (t: string) => {
    const v = t.trim().replace(/,$/, "");
    if (v && !value.includes(v)) onChange([...value, v]);
    setText("");
  };
  const left = suggestions.filter((s) => !value.includes(s)).slice(0, 8);
  return (
    <div>
      <div className={cn(fieldBase, "flex min-h-[34px] flex-wrap items-center gap-1.5 px-2 py-1")}>
        {value.map((t) => (
          <span key={t} className="inline-flex h-6 items-center gap-1 rounded-md bg-accent/12 pr-1 pl-2 text-[12px] text-accent">
            {t}
            <button type="button" className="rounded p-0.5 hover:bg-accent/20" onClick={() => onChange(value.filter((x) => x !== t))}>
              <X size={11} />
            </button>
          </span>
        ))}
        <input
          value={text}
          onChange={(e) => (e.target.value.endsWith(",") || e.target.value.endsWith("，") ? add(e.target.value.slice(0, -1)) : setText(e.target.value))}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              add(text);
            } else if (e.key === "Backspace" && !text && value.length) onChange(value.slice(0, -1));
          }}
          onBlur={() => text && add(text)}
          placeholder={value.length ? "" : placeholder}
          className="h-6 min-w-[80px] flex-1 bg-transparent text-[13px] outline-none placeholder:text-muted/70"
        />
      </div>
      {left.length > 0 && (
        <div className="mt-1.5 flex flex-wrap gap-1">
          {left.map((s) => (
            <button key={s} type="button" onClick={() => add(s)} className="h-5 rounded-md border border-line px-1.5 text-[11px] text-muted hover:border-accent/40 hover:text-accent">
              + {s}
            </button>
          ))}
        </div>
      )}
    </div>
  );
}

/** 提示条：始终图标 + 文字，状态色不单独承载含义 */
export function Callout({ tone = "info", title, children, className, action }: { tone?: "info" | "warn" | "bad" | "ok"; title?: ReactNode; children?: ReactNode; className?: string; action?: ReactNode }) {
  const Icon = tone === "ok" ? CircleCheck : tone === "info" ? CircleAlert : TriangleAlert;
  const cls = {
    info: "border-info/25 bg-info/8 text-info",
    warn: "border-warn/30 bg-warn/8 text-warn",
    bad: "border-bad/30 bg-bad/8 text-bad",
    ok: "border-ok/30 bg-ok/8 text-ok",
  }[tone];
  return (
    <div className={cn("flex items-start gap-2.5 rounded-xl border px-3.5 py-2.5", cls, className)}>
      <Icon size={16} className="mt-0.5 shrink-0" />
      <div className="min-w-0 flex-1 text-[12.5px] leading-relaxed">
        {title && <div className="font-semibold">{title}</div>}
        {children && <div className="text-fg2">{children}</div>}
      </div>
      {action}
    </div>
  );
}

// ---- 不会被滚动容器裁剪的弹出菜单 ----

export interface PopItem {
  label: string;
  icon: IconType;
  onClick: () => void;
  danger?: boolean;
  disabled?: boolean;
  divider?: boolean;
  /** 显示在右侧的提示（例如快捷键） */
  hint?: string;
}

export function PopMenu({ at, items, onClose }: { at: { x: number; y: number }; items: PopItem[]; onClose: () => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const [pos, setPos] = useState(at);
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    // 贴近窗口边缘时向内收，保证整个菜单可见
    setPos({ x: Math.max(8, Math.min(at.x, window.innerWidth - el.offsetWidth - 8)), y: Math.max(8, Math.min(at.y, window.innerHeight - el.offsetHeight - 8)) });
  }, [at]);
  useEffect(() => {
    const key = (e: KeyboardEvent) => e.key === "Escape" && onClose();
    window.addEventListener("keydown", key);
    return () => window.removeEventListener("keydown", key);
  }, [onClose]);
  return createPortal(
    <>
      <div className="fixed inset-0 z-[60]" onMouseDown={onClose} onContextMenu={(e) => (e.preventDefault(), onClose())} />
      <div ref={ref} className="fixed z-[61] min-w-[176px] animate-pop rounded-xl border border-line2 bg-panel2 p-1 shadow-2xl" style={{ left: pos.x, top: pos.y }}>
        {items.map((it, i) =>
          it.divider ? (
            <div key={i} className="my-1 h-px bg-line" />
          ) : (
            <button
              key={i}
              type="button"
              disabled={it.disabled}
              onClick={() => {
                onClose();
                it.onClick();
              }}
              className={cn(
                "flex h-8 w-full items-center gap-2.5 rounded-lg px-2.5 text-left text-[13px] transition-colors disabled:pointer-events-none disabled:opacity-40",
                it.danger ? "text-bad hover:bg-bad/12" : "text-fg2 hover:bg-panel3 hover:text-fg",
              )}
            >
              <it.icon size={14} className="shrink-0" />
              <span className="flex-1 whitespace-nowrap">{it.label}</span>
              {it.hint && <span className="pl-4 text-[11.5px] whitespace-nowrap text-muted">{it.hint}</span>}
            </button>
          ),
        )}
      </div>
    </>,
    document.body,
  );
}
