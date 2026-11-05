// Reads the question a waiting agent shows at the bottom of its screen,
// for the answer bar (answer.js) to offer as buttons. It reads only; it
// knows no DOM, so node can test it (scripts/web-prompt-test.mjs).
//
//   import { screenLines, parsePrompt } from "./prompt.js";
//   const p = parsePrompt(screenLines(xt.buffer.active, xt.rows));
//   // p.kind: "numbered", "arrows" or "" (none found)
//   // p.options: [{ n: 1, label: "Yes", selected: true }, …]
//   // p.key: what the prompt's lines say, to tell it changed
//
// The shapes it knows: Claude Code's and Codex's numbered menus
// ("❯ 1. Yes", "› 1. Yes, proceed (y)"), whose digits answer, and
// OpenCode's row of buttons ("Allow once  Always allow  Reject"), which
// ← → move along and Enter takes.

// SCAN is how many lines up from the screen's last text are read.
const SCAN = 30;
// NEAR is how close to that last text a menu's last option must be.
const NEAR = 8;
// GAP is how many lines (a description, a wrapped label) may come
// between one option and the next.
const GAP = 4;
// ABOVE is how many lines of text over a menu are its question, which
// tells one prompt from the next with the same options.
const ABOVE = 6;
const MAX_LABEL = 120;

// A menu's option: a marker for the chosen one, a digit, a dot.
const OPTION = /^\s*([❯›>▶►▸»*]\s*)?([1-9])[.)]\s+(\S.*)$/;
// A box's sides, as some agents draw around a prompt.
const SIDES = /^\s*[│┃║▌]|[│┃║]\s*$/g;
// A shortcut a label ends with: "(y)", "(esc)".
const SHORTCUT = /\s+\((?:esc|enter|tab|[a-z0-9])\)$/i;

// screenLines are the screen's lines from SCAN up from its last text
// down to it, a line the terminal wrapped joined to the one it goes on.
// buf is an xterm buffer (xt.buffer.active).
export function screenLines(buf, rows) {
  const top = buf.baseY;
  let end = top + rows - 1;
  while (end > top && !(buf.getLine(end)?.translateToString(true).trim())) end--;
  const out = [];
  for (let y = Math.max(0, end - SCAN + 1); y <= end; y++) {
    const line = buf.getLine(y);
    if (!line) continue;
    const text = line.translateToString(false);
    if (line.isWrapped && out.length) out[out.length - 1] += text;
    else out.push(text);
  }
  return out.map((s) => s.trimEnd());
}

// parsePrompt finds the prompt in lines: the last numbered menu near the
// bottom, 1 to n in order, of two options or more; else OpenCode's
// buttons; else none.
export function parsePrompt(lines) {
  const clean = lines.map((s) => s.replace(SIDES, "").trimEnd());
  let menu = null, run = null, gap = 0;
  clean.forEach((text, i) => {
    const m = OPTION.exec(text);
    if (m) {
      const n = Number(m[2]);
      const opt = { n, label: label(m[3]), selected: Boolean(m[1]) };
      if (run && n === run.options.length + 1 && gap <= GAP) {
        run.options.push(opt);
        run.last = i;
      } else if (n === 1) {
        run = { options: [opt], first: i, last: i };
      } else {
        run = null;
      }
      gap = 0;
      if (run && run.options.length >= 2) menu = run;
      return;
    }
    if (run && ++gap > GAP) run = null;
  });
  if (menu && clean.length - 1 - menu.last <= NEAR) {
    const above = clean.slice(0, menu.first).filter((s) => s.trim()).slice(-ABOVE);
    return { kind: "numbered", options: menu.options, key: [...above, ...clean.slice(menu.first, menu.last + 1)].join("\n") };
  }
  const tail = clean.filter((s) => s.trim()).slice(-NEAR);
  const kind = tail.some((s) => s.includes("Allow once") && s.includes("Reject")) ? "arrows" : "";
  return { kind, options: [], key: tail.join("\n") };
}

function label(text) {
  const s = text.replace(SHORTCUT, "").trim();
  return s.length > MAX_LABEL ? s.slice(0, MAX_LABEL - 1) + "…" : s;
}
