// The answer bar for the web terminal: while the open pane's agent waits
// for the user, a strip over the key bar shows its whole question and a
// button for each answer its screen offers (prompt.js reads them). While
// it works there is nothing over the terminal: the key bar's Esc
// interrupts it.
//
//   import { mountAnswer } from "./answer.js";
//   const bar = mountAnswer(el, { h, xt, sendText, sendKey });
//   bar.update(pane);   // the open pane's info, at each state
//   bar.destroy();
//
// el holds the strip. sendText(text)
// types text and sendKey(name) presses a key ("esc", "enter", "left");
// each returns false when it could not send. A button types what the user
// would: an option's digit, or a key. Nothing is sent by itself. A tap
// reads the screen again first, and refuses when the prompt it was drawn
// from changed or went: a late tap must not answer a newer question. For
// the same reason buttons that just came take no tap for SETTLE ms, and
// none do for REST ms after one was taken.
//
// The question and the labels are the agent's words, and the screen's:
// they go in as text, never as markup.

import { parsePrompt, screenLines } from "./prompt.js";

const SETTLE = 500;
const REST = 1500;
// SCAN_AFTER is how long output is let settle before the screen is read
// again.
const SCAN_AFTER = 150;

const WAITING = { permission: "Needs permission", question: "Has a question", auth: "Needs sign-in" };

export function mountAnswer(el, { h, xt, sendText, sendKey }) {
  const root = h("div", { class: "answer", role: "group", "aria-label": "Answer the agent", hidden: true });
  el.append(root);
  // Tapping a button must not take the focus, or bring up the keyboard.
  root.addEventListener("pointerdown", (e) => e.preventDefault());

  let pane = null;
  let shown = null; // the prompt the strip shows: its key and message
  let held = false; // the buttons take no tap: settling or resting
  let holdTimer = 0, scanTimer = 0;
  let note = null;

  const mode = () => (pane?.status === "waiting" ? "waiting" : "");
  const read = () => parsePrompt(screenLines(xt.buffer.active, xt.rows));
  const keyOf = (p) => `${pane?.since}\n${p.kind}\n${p.key}`;

  const hold = (ms) => {
    held = true;
    root.classList.add("held");
    clearTimeout(holdTimer);
    holdTimer = setTimeout(() => {
      held = false;
      root.classList.remove("held");
    }, ms);
  };
  const say = (text) => { if (note) { note.textContent = text; note.hidden = !text; } };

  // answer sends what a button stands for, if the prompt is the one the
  // button was drawn from.
  const answer = (send) => () => {
    if (held) return;
    if (mode() !== "waiting" || keyOf(read()) !== shown?.key) {
      draw(true);
      say("The screen changed: look again, then tap.");
      return;
    }
    if (!send()) { say("Not connected: try again in a moment."); return; }
    say("");
    hold(REST);
  };

  const key = (label, aria, send, cls = "") => h("button", {
    class: "answer-key" + cls, type: "button", "aria-label": aria, onclick: answer(send),
  }, label);

  // draw shows the strip for the pane as it is; force draws it even when
  // the prompt looks the same.
  function draw(force = false) {
    const m = mode();
    if (m !== "waiting") {
      shown = null;
      root.hidden = true;
      root.replaceChildren();
      return;
    }

    const p = read();
    const k = keyOf(p);
    const message = pane.message || WAITING[pane.kind] || "Needs you";
    if (!force && shown?.key === k && shown.message === message) return;
    const fresh = shown?.key !== k;
    shown = { key: k, message };

    const parts = [h("p", { class: "answer-q" }, message)];
    if (p.kind === "numbered") {
      parts.push(h("div", { class: "answer-opts" }, p.options.map((o) => h("button", {
        class: "answer-opt" + (o.selected ? " sel" : ""), type: "button", "aria-label": `${o.n}: ${o.label}`,
        onclick: answer(() => sendText(String(o.n))),
      }, h("span", { class: "answer-n" }, String(o.n)), h("span", { class: "answer-l" }, o.label)))));
    }
    const keys = [];
    if (p.kind === "arrows") {
      keys.push(key("←", "Left", () => sendKey("left")), key("→", "Right", () => sendKey("right")));
    } else if (p.kind !== "numbered") {
      for (const n of ["1", "2", "3"]) keys.push(key(n, n, () => sendText(n)));
    }
    keys.push(key("Esc", "Escape", () => sendKey("esc")), key("⏎", "Enter", () => sendKey("enter"), " enter"));
    note = h("span", { class: "answer-note", role: "status", hidden: true });
    parts.push(h("div", { class: "answer-row" }, keys, note));

    root.hidden = false;
    root.replaceChildren(...parts);
    if (fresh) hold(SETTLE);
  }

  // The screen is read again as output settles, while the agent waits.
  const written = xt.onWriteParsed(() => {
    if (mode() !== "waiting") return;
    clearTimeout(scanTimer);
    scanTimer = setTimeout(() => draw(), SCAN_AFTER);
  });

  return {
    update(p) {
      pane = p;
      draw();
    },
    destroy() {
      clearTimeout(holdTimer);
      clearTimeout(scanTimer);
      written.dispose();
      root.remove();
    },
  };
}
