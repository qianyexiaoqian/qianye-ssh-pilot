// 终端快捷键：写成 "Ctrl+Shift+C" 的形式保存在设置里。

export const DEFAULT_KEYS = { keyCopy: "Ctrl+Shift+C", keyPaste: "Ctrl+Shift+V", keyFind: "Ctrl+Shift+F" } as const;

/** 按键名称：字母和数字按物理键位取（不受 Shift、输入法影响），其余用 key 的名字 */
function keyName(e: KeyboardEvent): string {
  if (/^Key[A-Z]$/.test(e.code)) return e.code.slice(3);
  if (/^Digit\d$/.test(e.code)) return e.code.slice(5);
  if (e.key === " ") return "Space";
  return e.key.length === 1 ? e.key.toUpperCase() : e.key;
}

const MODIFIERS = new Set(["Control", "Shift", "Alt", "Meta", "AltGraph", "CapsLock", "Process", "Dead", "Unidentified"]);

/** 把一次按键整理成快捷键写法；只按了修饰键时返回 null */
export function comboOf(e: KeyboardEvent): string | null {
  if (MODIFIERS.has(e.key)) return null;
  return (e.ctrlKey ? "Ctrl+" : "") + (e.altKey ? "Alt+" : "") + (e.shiftKey ? "Shift+" : "") + keyName(e);
}

export type HotkeyAction = "copy" | "paste" | "find";

/**
 * 检查快捷键能不能用：必须带 Ctrl 或 Alt，或者是功能键 / Insert，否则会占用正常输入。
 * 复制可以设成 Ctrl+C：它只在选中了文字时才复制，没有选中时照常发送中断信号。
 */
export function hotkeyProblem(combo: string, action: HotkeyAction): string | null {
  const parts = combo.split("+");
  const key = parts[parts.length - 1];
  const special = key === "Insert" || /^F\d{1,2}$/.test(key);
  if (!parts.includes("Ctrl") && !parts.includes("Alt") && !special) return "需要带 Ctrl 或 Alt（或者使用 F1–F12、Insert）";
  if (["Enter", "Tab", "Escape", "Backspace", "Space", "ArrowUp", "ArrowDown", "ArrowLeft", "ArrowRight"].includes(key)) return "这个按键在终端里有固定用途";
  if (action !== "copy" && isPlainCtrl(combo)) return `${combo} 在终端里另有用途（例如 Ctrl+C 中断、Ctrl+R 搜索历史），建议加上 Shift`;
  return null;
}

/** Ctrl+字母：终端里用来发送控制字符的按键 */
export function isPlainCtrl(combo: string): boolean {
  return /^Ctrl\+[A-Z]$/.test(combo);
}

export function matchHotkey(e: KeyboardEvent, combo: string | undefined): boolean {
  return !!combo && comboOf(e) === combo;
}
