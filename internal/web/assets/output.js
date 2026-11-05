// A pane's output as text, to read, select, copy and search, which the
// terminal's can't be: the browse view's Output, read from
// /api/panes/<id>/text. A process that stopped shows the last lines the
// computer kept of its run.
//
//   import { mountOutput } from "./output.js";
//   const out = mountOutput({ h, svg, bar, refreshBtn, note });
//   el.replaceChildren(...out.view(await api(base + "/text")));
//
// Its text is set as text, never as HTML. Only http and https addresses
// become links, which show the whole address and open in a new tab; not
// the computer's own (localhost, 127.x, [::1]), which on a phone would
// open the phone.

const SEARCH = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>';
const COPY = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><rect width="14" height="14" x="8" y="8" rx="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>';
const UP = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m18 15-6-6-6 6"/></svg>';
const DOWN = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="m6 9 6 6 6-6"/></svg>';

// maxHits caps the hits marked: past it, the count says so.
const maxHits = 1000;

// loopback tells a host of the computer itself: localhost and its
// subdomains (Dev URLs among them), 127.x, ::1 and the unspecified ones.
function loopback(host) {
  host = host.replace(/\.$/, "");
  return host === "localhost" || host.endsWith(".localhost") || /^127\./.test(host) ||
    host === "0.0.0.0" || host === "[::1]" || host === "[::]" || host.startsWith("[::ffff:7f");
}

// links finds the http and https addresses in text, without the
// punctuation a sentence puts after one, nor a bracket it doesn't open,
// and not the loopback ones.
export function links(text) {
  const out = [];
  const count = (s, c) => s.split(c).length - 1;
  for (const m of text.matchAll(/\bhttps?:\/\/[^\s<>"'`]+/g)) {
    let url = m[0];
    for (;;) {
      const last = url.at(-1);
      const open = { ")": "(", "]": "[", "}": "{" }[last];
      if (".,;:!?*".includes(last) || (open && count(url, open) < count(url, last))) url = url.slice(0, -1);
      else break;
    }
    try {
      const u = new URL(url);
      if ((u.protocol !== "http:" && u.protocol !== "https:") || loopback(u.hostname)) continue;
    } catch {
      continue;
    }
    out.push({ start: m.index, end: m.index + url.length, url });
  }
  return out;
}

// hits finds query in text, its case ignored, as given: no pattern.
export function hits(text, query) {
  if (!query) return [];
  const re = new RegExp(query.replace(/[.*+?^${}()|[\]\\]/g, "\\$&"), "gi");
  const out = [];
  for (const m of text.matchAll(re)) {
    out.push({ start: m.index, end: m.index + m[0].length });
    if (out.length > maxHits) break;
  }
  return out;
}

// copyText puts text on the clipboard: navigator.clipboard where the page
// is a secure context, else the older copy command, which works over
// plain http too. It tells whether it did.
async function copyText(text) {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    const ta = document.createElement("textarea");
    ta.value = text;
    ta.setAttribute("readonly", "");
    ta.style.cssText = "position:fixed;top:0;left:0;opacity:0;";
    document.body.append(ta);
    ta.select();
    let ok = false;
    try {
      ok = document.execCommand("copy");
    } catch {
      // Neither: the text can still be selected by hand.
    }
    ta.remove();
    return ok;
  }
}

export function mountOutput({ h, svg, bar, refreshBtn, note }) {
  let text = "";
  let pre = null;
  let found = [];
  let at = -1;
  let query = "";
  let finding = false;
  let typing = 0;

  const count = h("span", { class: "bv-meta bv-count", "aria-live": "polite" });
  const input = h("input", {
    class: "bv-find", type: "search", placeholder: "Find", "aria-label": "Find in the output",
    enterkeyhint: "search", autocapitalize: "off", autocorrect: "off", spellcheck: "false",
    oninput: () => {
      clearTimeout(typing);
      typing = setTimeout(() => search(input.value, 0), 150);
    },
    onkeydown: (e) => {
      if (e.key === "Enter") {
        e.preventDefault();
        clearTimeout(typing);
        if (input.value !== query) search(input.value, 0);
        else step(e.shiftKey ? -1 : 1);
      } else if (e.key === "Escape") {
        e.preventDefault();
        closeFind();
      }
    },
  });
  const iconBtn = (markup, label, onclick, cls = "") => h("button", {
    class: "ib bv-btn " + cls, type: "button", title: label, "aria-label": label, onclick,
  }, svg(markup));
  const prevBtn = iconBtn(UP, "Previous", () => step(-1));
  const nextBtn = iconBtn(DOWN, "Next", () => step(1));
  const doneBtn = h("button", { class: "bv-done", type: "button", onclick: () => closeFind() }, "Done");
  const findBar = h("div", { class: "bv-bar bv-findbar", hidden: true }, input, count, prevBtn, nextBtn, doneBtn);
  const copyBtn = h("button", { class: "bv-copy", type: "button", title: "Copy all the output", onclick: copy }, svg(COPY), h("span", {}, "Copy all"));

  async function copy() {
    const ok = await copyText(text);
    copyBtn.lastChild.textContent = ok ? "Copied" : "Not copied";
    copyBtn.classList.toggle("done", ok);
    setTimeout(() => {
      copyBtn.lastChild.textContent = "Copy all";
      copyBtn.classList.remove("done");
    }, 1500);
  }

  // paint sets the text in pre, its links and its hits cut out of it.
  function paint() {
    if (!pre) return;
    const ls = links(text);
    const cuts = new Set([0, text.length]);
    for (const r of [...ls, ...found.slice(0, maxHits)]) cuts.add(r.start).add(r.end);
    const points = [...cuts].sort((a, b) => a - b);
    // A fragment, not a spread: a long log can have more pieces than a
    // call takes arguments.
    const nodes = document.createDocumentFragment();
    let li = 0, hi = 0, a = null;
    for (let i = 0; i + 1 < points.length; i++) {
      const s = points[i], e = points[i + 1];
      while (li < ls.length && ls[li].end <= s) li++;
      while (hi < found.length && found[hi].end <= s) hi++;
      const link = li < ls.length && ls[li].start <= s ? ls[li] : null;
      const hit = hi < maxHits && hi < found.length && found[hi].start <= s ? hi : -1;
      let piece = document.createTextNode(text.slice(s, e));
      if (hit >= 0) piece = h("mark", { class: "bv-hit", "data-hit": hit }, piece);
      if (!link) {
        a = null;
        nodes.append(piece);
        continue;
      }
      if (!a || a.dataset.start !== String(link.start)) {
        a = h("a", { href: link.url, target: "_blank", rel: "noopener noreferrer", "data-start": link.start });
        nodes.append(a);
      }
      a.append(piece);
    }
    pre.replaceChildren(nodes);
  }

  // search marks query's hits and goes to the one at index.
  function search(q, index) {
    query = q;
    found = hits(text, q);
    at = found.length ? Math.min(index, Math.min(found.length, maxHits) - 1) : -1;
    paint();
    show();
  }

  // step goes to the next hit (1) or the one before (-1), round.
  function step(by) {
    const n = Math.min(found.length, maxHits);
    if (!n) return;
    at = (at + by + n) % n;
    show();
  }

  // show lights the current hit, scrolls it to the middle and counts.
  function show() {
    const n = Math.min(found.length, maxHits);
    count.textContent = !query ? "" : n ? `${at + 1}/${n}${found.length > maxHits ? "+" : ""}` : "0/0";
    prevBtn.disabled = nextBtn.disabled = n < 2;
    if (!pre) return;
    for (const m of pre.querySelectorAll("mark.on")) m.classList.remove("on");
    const marks = pre.querySelectorAll(`mark[data-hit="${at}"]`);
    for (const m of marks) m.classList.add("on");
    marks[0]?.scrollIntoView({ block: "center" });
  }

  function openFind() {
    finding = true;
    findBar.hidden = false;
    input.focus();
    input.select();
  }

  function closeFind() {
    finding = false;
    findBar.hidden = true;
    input.value = "";
    if (query) search("", 0);
  }

  return {
    // busy tells that a new view would undo what the user is doing:
    // typing a find, or selecting text.
    busy() {
      const sel = getSelection();
      return document.activeElement === input || Boolean(pre && sel && !sel.isCollapsed && pre.contains(sel.anchorNode));
    },
    // view draws the text a read of it answered, the find there was
    // still found in it.
    view(t) {
      text = t.text || "";
      const lines = text ? text.replace(/\n$/, "").split("\n").length : 0;
      const what = t.stopped
        ? `${t.stopped} isn't running · ${t.more ? "its last 1 MB" : "its last lines"}`
        : t.more ? `The last 1 MB · ${lines} lines` : `${lines} ${lines === 1 ? "line" : "lines"}`;
      const sum = h("span", { class: "bv-sum" }, what);
      if (!text.trim()) {
        pre = null;
        return [bar(sum, refreshBtn()), note(t.stopped ? `Nothing was kept of ${t.stopped}'s last run.` : "Nothing in this terminal yet.")];
      }
      const findBtn = iconBtn(SEARCH, "Find", () => (finding ? input.focus() : openFind()));
      pre = h("pre", { class: "bv-out" });
      found = hits(text, query);
      at = found.length ? Math.min(Math.max(at, 0), Math.min(found.length, maxHits) - 1) : -1;
      paint();
      queueMicrotask(() => { if (query) show(); });
      // The bar and the find under it stay at the top as the text scrolls.
      return [h("div", { class: "bv-head" }, bar(sum, findBtn, copyBtn, refreshBtn()), findBar), pre];
    },
  };
}
