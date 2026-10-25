// omaterm web access: the sessions, and a pane as a live terminal. The
// pane's frames are the attach socket's: a kind byte, a big-endian
// uint32 length, the payload ('o' output, 'z' resized, 'c' closed; the
// page sends 'i' input and 'r' resize).

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
let view = null;        // "pair", "list" or "term"
let term = null;        // the open terminal's teardown and refresh

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
  view = "pair";
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
    render();
  };
  ws.onclose = (e) => {
    if (events !== ws) return;
    events = null;
    if (e.code === 4401) { showPair("This browser was signed out."); return; }
    render(true);
    setTimeout(async () => {
      // Signed out meanwhile? api() shows the pair page.
      try { state = await api("/api/state"); connectEvents(); render(); } catch { if (view !== "pair") connectEventsLater(); }
    }, eventsRetry);
    eventsRetry = Math.min(eventsRetry * 2, 8000);
  };
}

function connectEventsLater() {
  setTimeout(() => { if (!events && view !== "pair") connectEvents(); }, eventsRetry);
}

function wsURL(path) {
  return (location.protocol === "https:" ? "wss://" : "ws://") + location.host + path;
}

function render(offline = false) {
  const waiting = state ? state.sessions.filter((s) => s.lead && s.lead.status === "waiting").length : 0;
  document.title = waiting ? `(${waiting}) omaterm` : "omaterm";
  if (view === "list") renderList(offline);
  if (view === "term" && term) term.refresh();
}

// paneByID finds a pane and its session.
function paneByID(id) {
  for (const s of state?.sessions ?? []) {
    for (const p of s.panes ?? []) if (p.id === id) return { session: s, pane: p };
  }
  return null;
}

function label(p) {
  if (p.url) return p.title || p.url;
  if (p.agent) return AGENTS[p.agent] || p.agent;
  return p.process || p.title || p.shell || "Terminal";
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

// --- session list -----------------------------------------------------------

function showList() {
  view = "list";
  closeTerm();
  render();
}

function renderList(offline) {
  const waiting = state ? state.sessions.filter((s) => s.lead && s.lead.status === "waiting").length : 0;
  const header = h("header", { class: "bar" },
    h("img", { class: "logo", src: "icon.svg", alt: "" }),
    h("h1", {}, "omaterm"),
    waiting ? h("span", { class: "chip waiting" }, `${waiting} waiting`) : null,
    h("button", {
      class: "icon-btn", title: "Sign out this browser",
      onclick: async () => { if (confirm("Sign out this browser?")) { await api("/api/logout", {}); showPair(); } },
    }, "Sign out"),
  );
  const body = h("main", { class: "list" });
  if (!state) {
    body.append(h("p", { class: "empty" }, "Loading…"));
  } else if (!state.projects.length) {
    body.append(h("p", { class: "empty" }, "No projects yet. Open a folder in omaterm to start."));
  }
  for (const proj of state?.projects ?? []) {
    const sessions = state.sessions.filter((s) => s.project === proj.id);
    body.append(h("section", { class: "project" },
      h("div", { class: "project-head" },
        h("h2", { title: proj.dir }, proj.name),
        h("button", { class: "new", onclick: () => newSession(proj.id) }, "+ New session"),
      ),
      sessions.length ? sessions.map((s) => sessionCard(s, proj)) : h("p", { class: "empty" }, "No sessions."),
    ));
  }
  app.replaceChildren(...[header, offline && h("div", { class: "offline" }, "Reconnecting…"), body].filter(Boolean));
}

// where is a pane's folder, as seen from its project's.
function where(dir, proj) {
  if (!dir) return "";
  if (dir === proj.dir) return "";
  if (dir.startsWith(proj.dir + "/")) return dir.slice(proj.dir.length + 1);
  return dir.split("/").slice(-2).join("/");
}

function sessionCard(s, proj) {
  const lead = s.lead && s.lead.id ? s.lead : s.panes[0];
  if (!lead) return null;
  const st = status(lead);
  const sub = lead.status === "waiting" && lead.message ? lead.message : [label(lead), where(lead.dir, proj)].filter(Boolean).join(" · ");
  const panes = (s.panes ?? []).filter((p) => !p.url);
  return h("div", {
    class: "session", role: "button", tabindex: "0",
    onclick: () => openPane(lead.id),
    onkeydown: (e) => { if (e.key === "Enter") openPane(lead.id); },
  },
    h("div", { class: "top" },
      h("span", { class: `dot ${st.cls}` }),
      h("span", { class: "name" }, s.name || label(lead)),
      lead.unread ? h("span", { class: "unread", title: "Unread" }) : null,
      h("span", { class: `status ${st.cls}` }, st.text + (lead.since ? ` · ${ago(lead.since)}` : "")),
    ),
    h("div", { class: "sub" }, sub),
    panes.length > 1 ? h("div", { class: "panes" }, panes.map((p) =>
      h("button", { onclick: (e) => { e.stopPropagation(); openPane(p.id); } }, label(p)))) : null,
  );
}

async function newSession(project) {
  try {
    const made = await api("/api/sessions", { project });
    openPane(made.pane);
  } catch (err) {
    if (err.message !== "not paired") alert(err.message);
  }
}

function openPane(id) {
  location.hash = "#/pane/" + encodeURIComponent(id);
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

function showTerm(id) {
  view = "term";
  closeTerm();
  const found = paneByID(id);
  const title = h("h1", {}, found ? (found.session.name || label(found.pane)) : "Terminal");
  const chip = h("span", { class: "chip" });
  const fitBtn = h("button", { class: "icon-btn", "aria-pressed": "false", title: "Size the pane to this screen" }, "Fit");
  const wrap = h("div", { class: "term-wrap" });
  const note = h("div", { class: "term-note", hidden: true });
  let ctrl = false;
  const ctrlBtn = h("button", { "aria-pressed": "false" }, "Ctrl");
  const compose = h("input", { placeholder: "Type a line…", enterkeyhint: "send", autocomplete: "off", autocapitalize: "off", spellcheck: "false" });

  const xt = new Terminal({
    cols: 80, rows: 24, fontFamily: getComputedStyle(document.body).getPropertyValue("--mono"), fontSize: 13,
    theme: THEME, scrollback: 10000, cursorBlink: true, macOptionIsMeta: true, allowProposedApi: false,
  });
  const fit = new FitAddon.FitAddon();
  xt.loadAddon(fit);

  let ws = null, size = { cols: 80, rows: 24 }, fitting = false, closed = false, retry = 500, gone = false;

  const send = (bytes) => { if (ws && ws.readyState === 1) ws.send(frame("i", bytes)); };
  const sendText = (s) => send(enc.encode(s));
  const setNote = (text) => { note.hidden = !text; note.textContent = text || ""; };

  // Following the pane's size, the font shrinks until its width fits the
  // screen; past the smallest, the terminal scrolls sideways.
  const scaleToWidth = () => {
    if (fitting) return;
    for (let i = 0; i < 3; i++) {
      const dims = fit.proposeDimensions();
      if (!dims || !dims.cols) return;
      const want = xt.options.fontSize * dims.cols / size.cols;
      const next = Math.max(7, Math.min(14, Math.floor(want * 2) / 2));
      if (next === xt.options.fontSize) return;
      xt.options.fontSize = next;
    }
  };

  // Fitting sizes the pane itself to this screen, for every client: the
  // one that resizes last wins.
  const fitToScreen = () => {
    xt.options.fontSize = matchMedia("(pointer: fine)").matches ? 13 : 12;
    const dims = fit.proposeDimensions();
    if (!dims || !ws || ws.readyState !== 1) return;
    const fs = xt.options.fontSize;
    ws.send(frame("r", uint16s(dims.cols, dims.rows, fs * 0.6, fs * 1.2)));
  };

  const connect = () => {
    const sock = new WebSocket(wsURL(`/api/panes/${encodeURIComponent(id)}/attach`));
    sock.binaryType = "arraybuffer";
    ws = sock;
    let welcomed = false;
    sock.onmessage = (e) => {
      if (typeof e.data === "string") {
        const w = JSON.parse(e.data);
        size = { cols: w.cols, rows: w.rows };
        xt.reset();
        xt.resize(w.cols, w.rows);
        welcomed = true;
        retry = 500;
        setNote("");
        if (fitting) fitToScreen(); else scaleToWidth();
        return;
      }
      const b = new Uint8Array(e.data);
      const kind = String.fromCharCode(b[0]);
      const payload = b.subarray(5);
      if (kind === "o") xt.write(payload);
      else if (kind === "z") {
        const dv = new DataView(payload.buffer, payload.byteOffset);
        size = { cols: dv.getUint16(0), rows: dv.getUint16(2) };
        xt.resize(size.cols, size.rows);
        if (!fitting) scaleToWidth();
      } else if (kind === "c") {
        gone = true;
        setNote("This pane has closed.");
      }
    };
    sock.onclose = (e) => {
      if (ws !== sock || closed) return;
      ws = null;
      if (e.code === 4401) { showPair("This browser was signed out."); return; }
      if (e.code === 4404 || gone) { setNote("This pane has closed."); return; }
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
  const key = (text, fn) => h("button", { onclick: (e) => { e.preventDefault(); fn(); xt.focus(); } }, text);
  ctrlBtn.addEventListener("click", (e) => {
    e.preventDefault();
    ctrl = !ctrl;
    ctrlBtn.setAttribute("aria-pressed", String(ctrl));
    xt.focus();
  });
  const keys = h("div", { class: "keys" },
    key("Esc", () => sendText("\x1b")), key("Tab", () => sendText("\t")), ctrlBtn,
    key("^C", () => sendText("\x03")),
    key("↑", arrow("A")), key("↓", arrow("B")), key("←", arrow("D")), key("→", arrow("C")),
    key("⏎", () => sendText("\r")),
  );
  const sendLine = () => {
    const text = compose.value;
    compose.value = "";
    if (text) sendText(xt.modes.bracketedPasteMode ? `\x1b[200~${text}\x1b[201~` : text);
    setTimeout(() => sendText("\r"), text ? 60 : 0);
  };
  const composeBar = h("form", { class: "compose", onsubmit: (e) => { e.preventDefault(); sendLine(); } },
    compose, h("button", { type: "submit" }, "Send"));

  fitBtn.addEventListener("click", () => {
    fitting = !fitting;
    fitBtn.setAttribute("aria-pressed", String(fitting));
    if (fitting) fitToScreen(); else scaleToWidth();
  });

  const header = h("header", { class: "bar" },
    h("button", { class: "icon-btn", title: "Back", onclick: () => history.length > 1 ? history.back() : (location.hash = "") }, "←"),
    title, chip, fitBtn,
  );
  app.replaceChildren(h("div", { class: "term-view" }, header, wrap, note, keys, composeBar));
  xt.open(wrap);

  let resizeTimer = 0;
  const onResize = () => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(() => (fitting ? fitToScreen() : scaleToWidth()), 150);
  };
  const onVisible = () => { if (document.visibilityState === "visible" && !ws && !closed && !gone) connect(); };
  addEventListener("resize", onResize);
  document.addEventListener("visibilitychange", onVisible);

  const refresh = () => {
    const f = paneByID(id);
    if (!f) return;
    title.textContent = f.session.name || label(f.pane);
    const st = status(f.pane);
    chip.className = `chip ${st.cls === "waiting" ? "waiting" : ""}`;
    chip.textContent = st.text;
  };
  refresh();
  connect();
  xt.focus();

  term = {
    refresh,
    close() {
      closed = true;
      removeEventListener("resize", onResize);
      document.removeEventListener("visibilitychange", onVisible);
      if (ws) { ws.onclose = null; ws.close(); }
      xt.dispose();
    },
  };
}

// --- routes -----------------------------------------------------------------

function route() {
  if (view === "pair") return;
  const m = location.hash.match(/^#\/pane\/(.+)$/);
  if (m) showTerm(decodeURIComponent(m[1]));
  else showList();
}

async function start() {
  try {
    state = await api("/api/state");
  } catch (err) {
    if (err.message !== "not paired") app.replaceChildren(h("p", { class: "empty" }, "omaterm did not answer: " + err.message));
    return;
  }
  view = "list";
  if (!events) connectEvents();
  if (location.hash.startsWith("#code=")) history.replaceState(null, "", location.pathname);
  route();
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
