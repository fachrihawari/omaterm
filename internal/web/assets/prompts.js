// The compose box's prompts from before and for later.
//
//   import { mountPrompts } from "./prompts.js";
//   const prompts = mountPrompts({ h, head, panel, box, foot, insert, send, id, say, signedOut, onQueued });
//   prompts.remember(text); prompts.later(); await prompts.queue(text);
//   prompts.update(pane); prompts.opened(); prompts.destroy();
//
// Recent lists the last 10 prompts sent from this phone, kept in
// localStorage under omaterm.compose.recent: a tap puts one back in the
// box, which is also how a prompt sent into a socket that had died comes
// back. Signing out wipes them (app.js).
//
// For an agent's pane, When free lines the prompt up instead: omaterm
// types it into the agent's prompt once the agent is free, so the phone
// can go back in a pocket. The box lists those still waiting, each with
// Cancel, and onQueued tells how many, for the header to show.

const RECENT = "omaterm.compose.recent";
const KEEP = 10;
const MAX_TEXT = 64 << 10;

// Its look, beside the compose box's in keys.css.
if (!document.querySelector('link[href="prompts.css"]')) {
  const link = document.createElement("link");
  link.rel = "stylesheet";
  link.href = "prompts.css";
  document.head.append(link);
}

function loadRecent() {
  try {
    const list = JSON.parse(localStorage.getItem(RECENT) || "[]");
    return Array.isArray(list) ? list.filter((p) => typeof p?.text === "string" && typeof p.at === "number").slice(0, KEEP) : [];
  } catch {
    return [];
  }
}

function saveRecent(list) {
  try {
    localStorage.setItem(RECENT, JSON.stringify(list));
  } catch {
    // No storage, or full: Recent lasts this time only.
  }
}

function ago(ms) {
  const s = Math.max(0, (Date.now() - ms) / 1000);
  if (s < 60) return "now";
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

// firstLine is a prompt as a row shows it.
const firstLine = (text) => text.trim().split("\n")[0];

export function mountPrompts(options) {
  const { h, head, panel, box, foot, insert, send, id, say } = options;
  const base = `/api/panes/${encodeURIComponent(id)}/queue`;
  const sendLabel = send.textContent;

  // call asks the server; a 401 is a browser signed out.
  async function call(method, path, body) {
    const res = await fetch(path, body === undefined ? { method } : {
      method, headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
    });
    if (res.status === 401) {
      options.signedOut?.();
      throw new Error("not paired");
    }
    if (!res.ok) throw new Error((await res.text()).trim() || res.statusText);
    return res.json();
  }

  // --- Recent ---------------------------------------------------------------

  const recentBtn = h("button", { type: "button", class: "compose-recent-btn", "aria-expanded": "false", hidden: true }, "Recent");
  const recentList = h("div", { class: "compose-recent", hidden: true });
  head.insertBefore(recentBtn, head.lastChild);

  function showRecent(open) {
    const list = loadRecent();
    recentBtn.hidden = !list.length;
    open = open && list.length > 0;
    recentBtn.setAttribute("aria-expanded", String(open));
    recentList.hidden = !open;
    if (!open) return;
    recentList.replaceChildren(...list.map((p) => h("button", {
      type: "button", class: "compose-recent-item", title: p.text,
      onclick: () => putBack(p.text),
    }, h("span", { class: "t" }, firstLine(p.text)), h("span", { class: "ago" }, ago(p.at)))));
  }

  // putBack puts a prompt in the box: in place of nothing, else where the
  // cursor is, so nothing written is lost.
  function putBack(text) {
    showRecent(false);
    box.focus();
    if (box.value.trim() === "") box.value = text;
    else box.setRangeText(text, box.selectionStart, box.selectionEnd, "end");
    box.dispatchEvent(new Event("input")); // the draft keeps it
  }

  function remember(text) {
    if (text.trim() === "" || text.length > MAX_TEXT) return;
    const list = loadRecent().filter((p) => p.text !== text);
    list.unshift({ text, at: Date.now() });
    saveRecent(list.slice(0, KEEP));
    recentBtn.hidden = false;
  }

  recentBtn.addEventListener("click", () => showRecent(recentList.hidden));

  // --- When free ------------------------------------------------------------

  const laterBtn = h("button", {
    type: "button", class: "compose-later", "aria-pressed": "false", hidden: true,
    title: "Send it once the agent is free",
  }, "When free");
  foot.insertBefore(laterBtn, insert);
  const queuedList = h("div", { class: "compose-queued", hidden: true });
  panel.insertBefore(recentList, box);
  panel.insertBefore(queuedList, box);

  let agent = false, inbox = -1, waiting = [], alive = true;

  function setLater(on) {
    laterBtn.setAttribute("aria-pressed", String(on));
    insert.hidden = on; // the agent gets it with Enter, as Send
    send.textContent = on ? "Send later" : sendLabel;
  }
  laterBtn.addEventListener("click", () => setLater(laterBtn.getAttribute("aria-pressed") !== "true"));

  function showQueued() {
    options.onQueued?.(waiting.length);
    queuedList.hidden = !waiting.length;
    if (!waiting.length) return;
    queuedList.replaceChildren(
      h("div", { class: "lbl" }, waiting.length === 1 ? "Sends once the agent is free" : `${waiting.length} send once the agent is free`),
      ...waiting.map((q) => h("div", { class: "compose-queued-item" },
        h("span", { class: "t", title: q.text }, firstLine(q.text)),
        h("button", { type: "button", "aria-label": "Cancel: " + firstLine(q.text), onclick: () => cancel(q.id) }, "Cancel"),
      )),
    );
  }

  async function load() {
    try {
      const list = await call("GET", base);
      if (alive) { waiting = Array.isArray(list) ? list : []; showQueued(); }
    } catch {
      // Kept as it was: the next change asks again.
    }
  }

  async function cancel(msg) {
    say("");
    try {
      await call("DELETE", `${base}/${encodeURIComponent(msg)}`);
    } catch (err) {
      if (err.message !== "not paired") say(err.message);
    }
    await load();
  }

  // queue lines the text up for the agent; it tells why it couldn't, ""
  // when it did.
  async function queue(text) {
    try {
      const q = await call("POST", base, { text });
      remember(text);
      waiting = [...waiting.filter((w) => w.id !== q.id), q];
      showQueued();
      return "";
    } catch (err) {
      return err.message === "not paired" ? "Signed out: not sent" : err.message;
    }
  }

  return {
    remember,
    queue,
    later: () => !laterBtn.hidden && laterBtn.getAttribute("aria-pressed") === "true",
    // update follows the pane: an agent's is offered When free, and what
    // waits for it is asked for again as its inbox changes.
    update(pane) {
      agent = Boolean(pane?.agent) && pane.status !== "exited";
      laterBtn.hidden = !agent;
      if (!agent) setLater(false);
      const n = pane?.inbox || 0;
      if (n === inbox) return;
      inbox = n;
      if (n) load();
      else if (waiting.length) { waiting = []; showQueued(); }
    },
    // opened readies the box as it opens: Send sends now, unless asked.
    opened() {
      setLater(false);
      showRecent(false);
    },
    destroy() {
      alive = false;
    },
  };
}
