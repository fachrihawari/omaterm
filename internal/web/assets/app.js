// omaterm web access: the sessions in a sidebar, and a pane as a live
// terminal beside them, or alone on a narrow screen. The pane's frames are
// the attach socket's: a kind byte, a big-endian uint32 length, the
// payload ('o' output, 'z' resized, 'c' closed; the page sends 'i' input
// and 'r' resize).

const app = document.getElementById("app");
const enc = new TextEncoder();

const AGENTS = { claude: "Claude Code", codex: "Codex", opencode: "OpenCode", pi: "Pi", gemini: "Gemini", amp: "Amp" };
const THEME = {
  background: "#0d1117", foreground: "#e6edf3", cursor: "#e6edf3", cursorAccent: "#0d1117",
  selectionBackground: "#264f78",
  black: "#484f58", red: "#ff7b72", green: "#3fb950", yellow: "#d29922", blue: "#58a6ff", magenta: "#bc8cff", cyan: "#39c5cf", white: "#b1bac4",
  brightBlack: "#6e7681", brightRed: "#ffa198", brightGreen: "#56d364", brightYellow: "#e3b341", brightBlue: "#79c0ff", brightMagenta: "#d2a8ff", brightCyan: "#56d4dd", brightWhite: "#ffffff",
};

let state = null;       // the last state: projects and sessions
let events = null;      // its WebSocket
let eventsRetry = 500;
let offline = false;
let paired = false;     // the shell shows, not the pair page
let term = null;        // the open terminal: its pane id, refresh and teardown
let shell = null;       // the shell's parts: sidebar list, content, footer

// h makes an element. Text is always set as text: names and messages
// come from programs.
function h(tag, props = {}, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k === "class") el.className = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (v === true) el.setAttribute(k, "");
    else if (v !== false && v != null) el.setAttribute(k, v);
  }
  for (const c of children.flat()) {
    if (c == null || c === false) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

async function api(path, body) {
  const res = await fetch(path, body === undefined ? {} : {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify(body),
  });
  if (res.status === 401) {
    showPair();
    throw new Error("not paired");
  }
  if (!res.ok) throw new Error((await res.text()).trim() || res.statusText);
  return res.json();
}

// --- pairing --------------------------------------------------------------

function showPair(message = "") {
  paired = false;
  shell = null;
  closeTerm();
  if (events) { events.onclose = null; events.close(); events = null; }
  document.title = "omaterm";
  const input = h("input", {
    name: "code", placeholder: "XXXX-XXXX", maxlength: "9", autocomplete: "one-time-code",
    autocapitalize: "characters", spellcheck: "false", "aria-label": "Pairing code",
  });
  const error = h("p", { class: "error", role: "alert" }, message);
  const button = h("button", { class: "primary", type: "submit" }, "Pair this browser");
  const form = h("form", {
    onsubmit: async (e) => {
      e.preventDefault();
      button.disabled = true;
      error.textContent = "";
      try {
        await pair(input.value);
        start();
      } catch (err) {
        error.textContent = err.message;
        button.disabled = false;
      }
    },
  },
    h("img", { src: "icon.svg", alt: "" }),
    h("h2", {}, "Pair this browser"),
    h("p", {}, "On your computer, run ", h("code", {}, "omaterm web pair"), " or choose ", h("b", {}, "Web Access: Pair a Device"), " in omaterm, then enter the code."),
    input, button, error,
  );
  app.replaceChildren(h("div", { class: "pair" }, form));
  input.focus();
}

async function pair(code) {
  const res = await fetch("/api/pair", {
    method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ code: code.trim() }),
  });
  if (!res.ok) throw new Error((await res.text()).trim() || "Pairing failed");
}

// --- state ----------------------------------------------------------------

function connectEvents() {
  const ws = new WebSocket(wsURL("/api/events"));
  events = ws;
  ws.onopen = () => { eventsRetry = 500; };
  ws.onmessage = (e) => {
    state = JSON.parse(e.data);
    offline = false;
    render();
  };
  ws.onclose = (e) => {
    if (events !== ws) return;
    events = null;
    if (e.code === 4401) { showPair("This browser was signed out."); return; }
    offline = true;
    render();
    setTimeout(async () => {
      // Signed out meanwhile? api() shows the pair page.
      try { state = await api("/api/state"); offline = false; connectEvents(); render(); } catch { if (paired) connectEventsLater(); }
    }, eventsRetry);
    eventsRetry = Math.min(eventsRetry * 2, 8000);
  };
}

function connectEventsLater() {
  setTimeout(() => { if (!events && paired) connectEvents(); }, eventsRetry);
}

function wsURL(path) {
  return (location.protocol === "https:" ? "wss://" : "ws://") + location.host + path;
}

function waitingCount() {
  return state ? state.sessions.filter((s) => s.lead && s.lead.status === "waiting").length : 0;
}

function render() {
  if (!paired || !shell) return;
  const waiting = waitingCount();
  document.title = waiting ? `(${waiting}) omaterm` : "omaterm";
  renderSidebar();
  if (term) term.refresh();
}

// paneByID finds a pane and its session.
function paneByID(id) {
  for (const s of state?.sessions ?? []) {
    for (const p of s.panes ?? []) if (p.id === id) return { session: s, pane: p };
  }
  return null;
}

// label names a pane by what runs in it; a shell's title is mostly its
// prompt's user, host and folder, which say little here.
function label(p) {
  if (p.url) return p.title || p.url;
  if (p.agent) return AGENTS[p.agent] || p.agent;
  return p.process || p.shell || "Terminal";
}

// status reads a pane's status for people: its class and words.
function status(p) {
  switch (p.status) {
    case "working": return { cls: "working", text: "Working" };
    case "waiting": return { cls: "waiting", text: "Needs you" };
    case "error": return { cls: "error", text: "Failed" };
    case "exited": return { cls: "", text: "Exited" };
  }
  if (p.agent) return { cls: "done", text: "Ready" };
  return { cls: "", text: p.process ? "Running" : "Idle" };
}

function ago(ms) {
  const s = Math.max(0, (Date.now() - ms) / 1000);
  if (s < 60) return "now";
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

// terminals are a session's panes that are terminals, not pages.
function terminals(s) {
  return (s.panes ?? []).filter((p) => !p.url);
}

// --- the shell: sidebar and content ------------------------------------------

function showShell() {
  const list = h("nav", { class: "side-list", "aria-label": "Sessions" });
  const foot = h("div", { class: "side-foot" });
  const waiting = h("span", { class: "chip waiting", hidden: true });
  const content = h("main", { class: "content" });
  const sidebar = h("aside", { class: "sidebar" },
    h("header", { class: "side-head" },
      h("img", { class: "logo", src: "icon.svg", alt: "" }),
      h("h1", {}, "omaterm"),
      waiting,
    ),
    list,
    foot,
  );
  const root = h("div", { class: "shell" }, sidebar, content);
  app.replaceChildren(root);
  shell = { root, list, foot, waiting, content };
}

function renderSidebar() {
  const { list, foot, waiting } = shell;
  const n = waitingCount();
  waiting.hidden = !n;
  waiting.textContent = `${n} waiting`;
  const open = term?.id;
  const items = [];
  if (!state) items.push(h("p", { class: "empty" }, "Loading…"));
  else if (!state.projects.length) items.push(h("p", { class: "empty" }, "No projects yet. Open a folder in omaterm to start."));
  for (const proj of state?.projects ?? []) {
    const sessions = state.sessions.filter((s) => s.project === proj.id && terminals(s).length);
    items.push(h("section", { class: "project" },
      h("div", { class: "project-head" },
        h("h2", { title: proj.dir }, proj.name),
        h("button", { class: "new", title: `New session in ${proj.name}`, "aria-label": `New session in ${proj.name}`, onclick: () => newSession(proj.id) }, "+"),
      ),
      sessions.length ? sessions.map((s) => sessionRow(s, proj, open)) : h("p", { class: "none" }, "No sessions"),
    ));
  }
  list.replaceChildren(...items);
  foot.replaceChildren(...[
    offline && h("span", { class: "offline" }, "Reconnecting…"),
    h("button", {
      class: "link", title: "Sign out this browser",
      onclick: async () => { if (confirm("Sign out this browser?")) { await api("/api/logout", {}); showPair(); } },
    }, "Sign out"),
  ].filter(Boolean));
}

// where is a pane's folder, as seen from its project's.
function where(dir, proj) {
  if (!dir || dir === proj.dir) return "";
  if (dir.startsWith(proj.dir + "/")) return dir.slice(proj.dir.length + 1);
  return dir.split("/").slice(-2).join("/");
}

function sessionRow(s, proj, open) {
  const panes = terminals(s);
  const lead = s.lead && s.lead.id && !s.lead.url ? s.lead : panes[0];
  const st = status(lead);
  const sub = lead.status === "waiting" && lead.message ? lead.message : [label(lead), where(lead.dir, proj)].filter(Boolean).join(" · ");
  const here = panes.some((p) => p.id === open);
  return h("div", { class: "session-group" },
    h("a", {
      class: "session" + (here ? " current" : ""), href: "#/pane/" + encodeURIComponent(here ? open : lead.id),
      "aria-current": here ? "page" : false,
    },
      h("span", { class: `dot ${st.cls}` }),
      h("span", { class: "body" },
        h("span", { class: "top" },
          h("span", { class: "name" }, s.name || label(lead)),
          lead.unread ? h("span", { class: "unread", title: "Unread" }) : null,
          h("span", { class: `status ${st.cls}` }, st.text + (lead.since ? ` · ${ago(lead.since)}` : "")),
        ),
        sub ? h("span", { class: "sub" }, sub) : null,
      ),
    ),
    panes.length > 1 ? h("div", { class: "panes" }, panes.map((p) =>
      h("a", { class: p.id === open ? "current" : "", href: "#/pane/" + encodeURIComponent(p.id) },
        h("span", { class: `dot ${status(p).cls}` }), label(p)))) : null,
  );
}

async function newSession(project) {
  try {
    const made = await api("/api/sessions", { project });
    location.hash = "#/pane/" + encodeURIComponent(made.pane);
  } catch (err) {
    if (err.message !== "not paired") alert(err.message);
  }
}

function showEmpty() {
  closeTerm();
  shell.root.dataset.view = "list";
  shell.content.replaceChildren(h("div", { class: "pick" },
    h("img", { src: "icon.svg", alt: "" }),
    h("p", {}, "Pick a session to open its terminal."),
  ));
}

// --- terminal ---------------------------------------------------------------

function frame(kind, payload) {
  const b = new Uint8Array(5 + payload.length);
  b[0] = kind.charCodeAt(0);
  new DataView(b.buffer).setUint32(1, payload.length);
  b.set(payload, 5);
  return b;
}

function uint16s(...v) {
  const b = new Uint8Array(v.length * 2);
  const dv = new DataView(b.buffer);
  v.forEach((x, i) => dv.setUint16(i * 2, Math.max(0, Math.min(Math.round(x), 0xffff))));
  return b;
}

function closeTerm() {
  if (term) { term.close(); term = null; }
}

const touch = matchMedia("(pointer: coarse)").matches;

// showTerm opens a pane in the content, sized to fill it: the pane takes
// this screen's size while it shows here, and the window's back after.
function showTerm(id) {
  if (term?.id === id) return;
  closeTerm();
  shell.root.dataset.view = "term";
  const title = h("h1", {}, "Terminal");
  const chip = h("span", { class: "chip" });
  const tabs = h("div", { class: "tabs" });
  const host = h("div", { class: "term-host" });
  const note = h("div", { class: "term-note", hidden: true });
  let ctrl = false;
  const ctrlBtn = h("button", { "aria-pressed": "false" }, "Ctrl");

  const xt = new Terminal({
    fontFamily: getComputedStyle(document.body).getPropertyValue("--mono"), fontSize: touch ? 12 : 13,
    theme: THEME, scrollback: 10000, cursorBlink: true, macOptionIsMeta: true, allowProposedApi: false,
  });
  const fit = new FitAddon.FitAddon();
  xt.loadAddon(fit);

  let ws = null, closed = false, retry = 500, gone = false, sized = false;

  const send = (bytes) => { if (ws && ws.readyState === 1) ws.send(frame("i", bytes)); };
  const sendText = (s) => send(enc.encode(s));
  const setNote = (text) => { note.hidden = !text; note.textContent = text || ""; };

  // fill sizes the terminal, and the pane, to the room it has.
  const fill = () => {
    const dims = fit.proposeDimensions();
    if (!dims || !dims.cols || !dims.rows) return;
    if (dims.cols !== xt.cols || dims.rows !== xt.rows) xt.resize(dims.cols, dims.rows);
    if (!ws || ws.readyState !== 1) return;
    const cell = xt._core?._renderService?.dimensions?.css?.cell;
    ws.send(frame("r", uint16s(dims.cols, dims.rows, cell?.width ?? 0, cell?.height ?? 0)));
  };

  const connect = () => {
    const sock = new WebSocket(wsURL(`/api/panes/${encodeURIComponent(id)}/attach`));
    sock.binaryType = "arraybuffer";
    ws = sock;
    let welcomed = false;
    sock.onmessage = (e) => {
      if (typeof e.data === "string") {
        const w = JSON.parse(e.data);
        xt.reset();
        xt.resize(w.cols, w.rows);
        welcomed = true;
        sized = false;
        retry = 500;
        setNote("");
        return;
      }
      const b = new Uint8Array(e.data);
      const kind = String.fromCharCode(b[0]);
      const payload = b.subarray(5);
      // The first frame shows the pane at its size; then it takes ours.
      if (kind === "o") xt.write(payload, sized ? undefined : () => { if (!sized) { sized = true; fill(); } });
      else if (kind === "z") {
        const dv = new DataView(payload.buffer, payload.byteOffset);
        const cols = dv.getUint16(0), rows = dv.getUint16(2);
        if (cols !== xt.cols || rows !== xt.rows) xt.resize(cols, rows);
      } else if (kind === "c") {
        gone = true;
        setNote("This session has ended.");
      }
    };
    sock.onclose = (e) => {
      if (ws !== sock || closed) return;
      ws = null;
      if (e.code === 4401) { showPair("This browser was signed out."); return; }
      if (e.code === 4404 || gone) { setNote(gone ? "This session has ended." : "This session is gone."); return; }
      setNote(welcomed ? "Reconnecting…" : "Connecting…");
      setTimeout(async () => {
        if (closed) return;
        try { await api("/api/state"); } catch (err) { if (err.message === "not paired") return; }
        connect();
      }, retry);
      retry = Math.min(retry * 2, 8000);
    };
  };

  xt.onData((d) => {
    if (ctrl && d.length === 1) {
      const c = d.toUpperCase().charCodeAt(0);
      if (c >= 64 && c < 96) d = String.fromCharCode(c & 0x1f);
      ctrl = false;
      ctrlBtn.setAttribute("aria-pressed", "false");
    }
    sendText(d);
  });
  xt.onBinary((d) => send(Uint8Array.from(d, (c) => c.charCodeAt(0) & 0xff)));

  const arrow = (k) => () => sendText((xt.modes.applicationCursorKeysMode ? "\x1bO" : "\x1b[") + k);
  const key = (text, fn, aria) => h("button", { "aria-label": aria, onclick: (e) => { e.preventDefault(); fn(); xt.focus(); } }, text);
  ctrlBtn.addEventListener("click", (e) => {
    e.preventDefault();
    ctrl = !ctrl;
    ctrlBtn.setAttribute("aria-pressed", String(ctrl));
    xt.focus();
  });
  const keys = touch ? h("div", { class: "keys" },
    key("Esc", () => sendText("\x1b")), key("Tab", () => sendText("\t")), ctrlBtn,
    key("^C", () => sendText("\x03"), "Control C"),
    key("↑", arrow("A"), "Up"), key("↓", arrow("B"), "Down"), key("←", arrow("D"), "Left"), key("→", arrow("C"), "Right"),
    key("⏎", () => sendText("\r"), "Enter"),
  ) : null;

  const header = h("header", { class: "term-head" },
    h("a", { class: "back", href: "#", "aria-label": "Sessions" }, "←"),
    h("div", { class: "titles" }, h("div", { class: "title-row" }, title, chip), tabs),
  );
  shell.content.replaceChildren(h("div", { class: "term-view" }, header, h("div", { class: "term-wrap" }, host), note, keys));
  xt.open(host);

  let resizeTimer = 0;
  const observer = new ResizeObserver(() => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(fill, 120);
  });
  observer.observe(host);
  const onVisible = () => { if (document.visibilityState === "visible" && !ws && !closed && !gone) connect(); };
  document.addEventListener("visibilitychange", onVisible);

  const refresh = () => {
    const f = paneByID(id);
    if (!f) return;
    const panes = terminals(f.session);
    title.textContent = f.session.name || label(f.pane);
    const st = status(f.pane);
    chip.className = `chip ${st.cls}`;
    chip.textContent = st.text;
    tabs.hidden = panes.length < 2;
    tabs.replaceChildren(...(panes.length < 2 ? [] : panes.map((p) =>
      h("a", { class: p.id === id ? "current" : "", href: "#/pane/" + encodeURIComponent(p.id) }, label(p)))));
    document.title = (waitingCount() ? `(${waitingCount()}) ` : "") + title.textContent + " · omaterm";
  };

  term = {
    id,
    refresh,
    close() {
      closed = true;
      observer.disconnect();
      clearTimeout(resizeTimer);
      document.removeEventListener("visibilitychange", onVisible);
      if (ws) { ws.onclose = null; ws.close(); }
      xt.dispose();
    },
  };
  refresh();
  renderSidebar();
  connect();
  if (!touch) xt.focus();
}

// --- routes -----------------------------------------------------------------

function route() {
  if (!paired) return;
  const m = location.hash.match(/^#\/pane\/(.+)$/);
  if (m) showTerm(decodeURIComponent(m[1]));
  else { showEmpty(); renderSidebar(); }
}

async function start() {
  try {
    state = await api("/api/state");
  } catch (err) {
    if (err.message !== "not paired") app.replaceChildren(h("p", { class: "empty" }, "omaterm did not answer: " + err.message));
    return;
  }
  paired = true;
  if (location.hash.startsWith("#code=")) history.replaceState(null, "", location.pathname);
  showShell();
  if (!events) connectEvents();
  route();
  render();
}

addEventListener("hashchange", route);

// A pairing link carries its code after the #, which never reaches a
// server's logs.
(async () => {
  const m = location.hash.match(/^#code=([0-9A-Za-z-]+)$/);
  if (m) {
    history.replaceState(null, "", location.pathname);
    try { await pair(m[1]); } catch (err) { showPair(err.message); return; }
  }
  start();
})();
