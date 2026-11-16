// 布局检查：在页面里执行，找出被裁掉或需要横向滚动才能看到的内容。
(() => {
  const wait = (ms) => new Promise((r) => setTimeout(r, ms));
  const cls = (el) => (el.className && el.className.baseVal !== undefined ? el.className.baseVal : el.className || "").toString();
  const desc = (el) => {
    const c = cls(el).split(/\s+/).filter(Boolean).slice(0, 4).join(".");
    return el.tagName.toLowerCase() + (c ? "." + c : "") + ' "' + (el.innerText || "").trim().slice(0, 22).replace(/\n/g, " ") + '"';
  };
  const skip = (el) => !!el.closest(".xterm, .cm-editor, svg, [data-audit-skip]");
  window.__audit = () => {
    const bad = [];
    for (const el of document.querySelectorAll("body *")) {
      if (skip(el) || el.clientWidth < 2 || el.clientHeight < 2) continue;
      const cs = getComputedStyle(el);
      if (cs.display === "none" || cs.visibility === "hidden") continue;
      const c = cls(el);
      const ox = cs.overflowX, oy = cs.overflowY;
      const dx = el.scrollWidth - el.clientWidth, dy = el.scrollHeight - el.clientHeight;
      if (dx > 1) {
        if (ox === "auto" || ox === "scroll") {
          // 允许明确声明可以横向滚动的地方（标签栏、表格、代码块）
          if (!/overflow-x-auto|overflow-auto/.test(c) && el.tagName !== "PRE") bad.push(`HSCROLL +${dx} ${desc(el)}`);
        } else if ((ox === "hidden" || ox === "clip") && cs.textOverflow !== "ellipsis" && !/line-clamp|truncate/.test(c)) {
          // 忽略纯装饰（模糊光斑等绝对定位元素）造成的溢出
          const real = [...el.querySelectorAll("*")].some((k) => {
            if (skip(k)) return false;
            const ks = getComputedStyle(k);
            if (ks.position === "absolute" || ks.position === "fixed" || ks.display === "none") return false;
            const r = k.getBoundingClientRect(), pr = el.getBoundingClientRect();
            return r.width > 1 && r.right > pr.right + 1.5 && !k.closest(".truncate") && getComputedStyle(k.parentElement).textOverflow !== "ellipsis";
          });
          if (real) bad.push(`CLIPX +${dx} ${desc(el)}`);
        }
      }
      if (dy > 1 && (oy === "hidden" || oy === "clip") && !/line-clamp|truncate/.test(c) && cs.textOverflow !== "ellipsis") {
        const pr = el.getBoundingClientRect();
        const real = [...el.children].some((k) => {
          const ks = getComputedStyle(k);
          if (ks.position === "absolute" || ks.position === "fixed" || ks.display === "none") return false;
          const r = k.getBoundingClientRect();
          return r.height > 1 && r.bottom > pr.bottom + 1.5;
        });
        if (real) bad.push(`CLIPY +${dy} ${desc(el)}`);
      }
    }
    // 没有被任何容器接住、直接超出窗口的元素
    for (const el of document.querySelectorAll("header *, main > *, aside *")) {
      if (skip(el)) continue;
      const r = el.getBoundingClientRect();
      if (r.width > 1 && r.height > 1 && (r.right > innerWidth + 1 || r.left < -1) && getComputedStyle(el).position !== "fixed") {
        let p = el.parentElement, ok = false;
        for (; p; p = p.parentElement) {
          const o = getComputedStyle(p).overflowX;
          if (o === "auto" || o === "scroll") { ok = true; break; }
        }
        if (!ok) bad.push(`OUT ${Math.round(r.left)}..${Math.round(r.right)} ${desc(el)}`);
      }
    }
    return [...new Set(bad)].slice(0, 12);
  };
  const nav = async (label) => {
    // 窄窗口下导航在抽屉里：先点标题栏的菜单按钮
    let btn = [...document.querySelectorAll("aside button")].find((b) => b.innerText.trim().split("\n")[0] === label || b.dataset.tip === label);
    if (!btn) {
      document.querySelector('header button[title="导航菜单"]')?.click();
      await wait(250);
      btn = [...document.querySelectorAll("aside button")].find((b) => b.innerText.trim().split("\n")[0] === label || b.dataset.tip === label);
    }
    btn?.click();
    await wait(450);
    return !!btn;
  };
  window.__navTo = nav;
  window.__auditAll = async (pages) => {
    const out = { size: innerWidth + "x" + innerHeight };
    for (const p of pages) {
      const ok = await nav(p);
      await wait(350);
      const r = window.__audit();
      if (!ok) out[p] = ["NAV FAILED"];
      else if (r.length) out[p] = r;
    }
    return out;
  };
  window.__wait = wait;
  window.__nav = nav;
  window.__pages = ["仪表盘", "实时监控", "服务器", "终端", "文件管理", "密钥管理", "证书中心", "服务管理", "脚本库", "智能巡检", "AI 助手", "记忆与技能", "设置"];
  const d2 = (el) => el.tagName.toLowerCase() + "." + cls(el).split(/\s+/).filter(Boolean).slice(0, 6).join(".") + ' "' + (el.innerText || "").trim().slice(0, 18).replace(/\n/g, " ") + '"';
  window.__why = () => {
    const out = [];
    for (const el of document.querySelectorAll("body, body *")) {
      const cs = getComputedStyle(el);
      if (el.clientWidth < 2 || el.scrollWidth - el.clientWidth <= 1) continue;
      if (!["auto", "scroll", "hidden", "clip"].includes(cs.overflowX)) continue;
      if (/overflow-x-auto/.test(cls(el)) || cs.textOverflow === "ellipsis" || /truncate|line-clamp/.test(cls(el))) continue;
      const pr = el.getBoundingClientRect();
      const kids = [...el.querySelectorAll("*")].filter((k) => {
        const r = k.getBoundingClientRect();
        return r.width > 1 && r.right > pr.right + 1 && getComputedStyle(k).position !== "absolute" && !k.closest(".xterm, .cm-editor");
      });
      if (!kids.length) continue;
      const leaves = kids.filter((k) => !kids.some((o) => o !== k && k.contains(o))).slice(0, 3);
      out.push({ box: d2(el), over: leaves.map((k) => d2(k) + " w=" + Math.round(k.getBoundingClientRect().width) + " right+" + Math.round(k.getBoundingClientRect().right - pr.right)) });
    }
    return out.slice(0, 4);
  };
  window.__auditWhy = async (pages) => {
    // 后台标签页里改变窗口大小后不一定会收到 resize 事件，这里补发一次
    window.dispatchEvent(new Event("resize"));
    await wait(250);
    const out = { size: innerWidth + "x" + innerHeight, mainW: Math.round(document.querySelector("main")?.getBoundingClientRect().width || 0) };
    for (const p of pages || window.__pages) {
      const ok = await nav(p);
      await wait(400);
      const r = window.__audit();
      if (!ok) out[p] = ["NAV FAILED"];
      else if (r.length) out[p] = { flags: r, why: window.__why() };
    }
    return out;
  };
  return "audit ready";
})();
