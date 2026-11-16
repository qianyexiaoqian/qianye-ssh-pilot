// 驱动验收构建（wails build -tags qa）的窗口：通过程序里的本机测试接口调用 DevTools 协议，不需要移动鼠标。
//
// 准备：
//   wails build -tags qa -o QianYeSSHPilot-qa.exe
//   用一个专门的数据目录启动：set SSHPILOT_DATA_DIR=<测试数据目录>，再运行 build\bin\QianYeSSHPilot-qa.exe
//   程序会把每个窗口的测试端口写到 <测试数据目录>\qa\<main|files>-<进程号>.port
//
// 用法（QA_DIR 指向上面的 qa 目录）：
//   node qa.mjs list
//   node qa.mjs <main|files> eval <js 文件>              在窗口里执行脚本并打印返回值
//   node qa.mjs <main|files> shot <out.png>              截图
//   node qa.mjs <main|files> drop <x> <y> <本地路径...>  模拟把文件 / 文件夹拖到窗口的 (x, y) 处
//       （环境变量 DRAG_SHOT=<png> 可以保存鼠标悬停、还没松开时的截图）
import fs from "node:fs";
import path from "node:path";

const DIR = process.env.QA_DIR;
if (!DIR) {
  console.log("请先设置环境变量 QA_DIR（测试数据目录下的 qa 文件夹）");
  process.exit(1);
}

function ports() {
  if (!fs.existsSync(DIR)) return [];
  return fs
    .readdirSync(DIR)
    .filter((f) => f.endsWith(".port"))
    .map((f) => {
      const [kind, pid] = f.replace(".port", "").split("-");
      return { kind, pid: Number(pid), port: Number(fs.readFileSync(path.join(DIR, f), "utf8")), mtime: fs.statSync(path.join(DIR, f)).mtimeMs };
    })
    .sort((a, b) => b.mtime - a.mtime);
}

async function cdp(port, method, params = {}) {
  const r = await fetch(`http://127.0.0.1:${port}/cdp`, { method: "POST", body: JSON.stringify({ method, params }) });
  const j = await r.json();
  if (j.error) throw new Error(j.error);
  return j.result;
}

async function alive(p) {
  try {
    await cdp(p.port, "Runtime.evaluate", { expression: "1" });
    return true;
  } catch {
    return false;
  }
}

// 同一类窗口有多个时，取最近打开的那个
async function pick(kind) {
  for (const p of ports()) if (p.kind === kind && (await alive(p))) return p.port;
  throw new Error(`没有找到正在运行的 ${kind} 窗口`);
}

async function evaluate(port, expression) {
  const r = await cdp(port, "Runtime.evaluate", { expression, awaitPromise: true, returnByValue: true });
  if (r.exceptionDetails) throw new Error(r.exceptionDetails.exception?.description || r.exceptionDetails.text);
  return r.result.value;
}

const [a0, cmd, ...args] = process.argv.slice(2);
if (a0 === "list") {
  for (const p of ports()) console.log(p.kind, "pid", p.pid, "port", p.port, (await alive(p)) ? "alive" : "dead");
} else if (cmd === "eval") {
  const out = await evaluate(await pick(a0), fs.readFileSync(args[0], "utf8"));
  console.log(typeof out === "string" ? out : JSON.stringify(out, null, 1));
} else if (cmd === "shot") {
  const r = await cdp(await pick(a0), "Page.captureScreenshot", { format: "png" });
  fs.writeFileSync(args[0], Buffer.from(r.data, "base64"));
  console.log("saved", args[0]);
} else if (cmd === "drop") {
  const port = await pick(a0);
  const x = Number(args[0]), y = Number(args[1]);
  const data = { items: [], files: args.slice(2), dragOperationsMask: 1 };
  for (const type of ["dragEnter", "dragOver", "dragOver"]) {
    await cdp(port, "Input.dispatchDragEvent", { type, x, y, data });
    await new Promise((r) => setTimeout(r, 200));
  }
  if (process.env.DRAG_SHOT) {
    const r = await cdp(port, "Page.captureScreenshot", { format: "png" });
    fs.writeFileSync(process.env.DRAG_SHOT, Buffer.from(r.data, "base64"));
  }
  await cdp(port, "Input.dispatchDragEvent", { type: "drop", x, y, data });
  console.log("dropped", data.files.length, "item(s) at", x, y);
} else {
  console.log("用法：node qa.mjs list | <main|files> eval <js> | shot <png> | drop <x> <y> <路径...>");
}
process.exit(0);
