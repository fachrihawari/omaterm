// omaterm web access: projects and sessions in a sidebar, and a live terminal
// card beside them, or full screen on a phone.

const app = document.getElementById("app");
const enc = new TextEncoder();

const AGENTS = { claude: "Claude Code", codex: "Codex", opencode: "OpenCode", pi: "Pi", gemini: "Gemini", amp: "Amp" };

// Default dark theme tokens until /api/theme loads.
const DEFAULT_THEME = {
  dark: true,
  tokens: {
    wash: "#0c0f14", sidebar: "#0c0f14", card: "#11161c", raised: "#21262c",
    popover: "#21262c", input: "#1b2026", hover: "rgba(215, 221, 229, 0.0588)",
    rowSelected: "rgba(215, 221, 229, 0.102)", chip: "rgba(215, 221, 229, 0.0706)",
    border: "rgba(215, 221, 229, 0.0784)", borderStrong: "rgba(215, 221, 229, 0.141)",
    text: "#d7dde5", textStrong: "#ffffff", textSoft: "#b9bfc7", textMuted: "#8c9199",
    textFaint: "#6a7076", accent: "#2f81f7", accentText: "#000000", selection: "#1d3a5f",
    ok: "#63d58d", warn: "#e9c66d", bad: "#ff6f6f", info: "#5aa9ff",
    attention: "#e9c66d", attentionSoft: "rgba(233, 198, 109, 0.141)", done: "#d7dde5",
    scrim: "rgba(0, 0, 0, 0.349)",
  },
  terminal: {
    foreground: "#d7dde5", background: "#11161c", cursor: "#ffffff", selection: "#1d3a5f",
    ansi: [
      "#11161c", "#ff6f6f", "#63d58d", "#e9c66d", "#5aa9ff", "#c792ea", "#61d8df", "#d7dde5",
      "#7f8a99", "#ff8585", "#b9f263", "#f3a85f", "#7cbcff", "#d7a8f0", "#86e3e8", "#ffffff",
    ],
  },
};

let currentTheme = DEFAULT_THEME;
let currentThemeStamp = "";

let state = null;       // last state: projects and sessions
let events = null;      // events WebSocket
let eventsRetry = 500;
let offline = false;
let paired = false;     // shell shows, not pair page
let term = null;        // open terminal: its pane id, refresh and teardown
let shell = null;       // shell DOM elements
const touch = matchMedia("(pointer: coarse)").matches || "ontouchstart" in window;

// Dynamic modules loaded asynchronously
const keysPromise = import("./keys.js").catch(() => null);
const composePromise = import("./compose.js").catch(() => null);

// SVG dictionary
const S = (d, extra = "") => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" ${extra}>${d}</svg>`;
const IC = {
  plus: S('<path d="M12 5v14M5 12h14"/>'),
  chev: S('<path d="M6 9l6 6 6-6"/>'),
  back: S('<path d="M19 12H5M12 19l-7-7 7-7"/>'),
  branch: S('<circle cx="6" cy="5" r="2"/><circle cx="6" cy="19" r="2"/><circle cx="18" cy="7" r="2"/><path d="M6 7v10M18 9c0 5-6 4-11 8"/>'),
  compose: S('<path d="M11 4H4a2 2 0 0 0-2 2v14a2 2 0 0 0 2 2h14a2 2 0 0 0 2-2v-7"/><path d="M18.5 2.5a2.121 2.121 0 0 1 3 3L12 15l-4 1 1-4 9.5-9.5z"/>'),
  signout: S('<path d="M9 21H5a2 2 0 0 1-2-2V5a2 2 0 0 1 2-2h4M16 17l5-5-5-5M21 12H9"/>'),
  term: S('<path d="M4 17l6-5-6-5M12 19h8"/>'),
  claude: `<svg viewBox="0 0 24 24" fill="currentColor"><path d="M12 2l1.6 6.2L19 5l-3.2 5.4L22 12l-6.2 1.6L19 19l-5.4-3.2L12 22l-1.6-6.2L5 19l3.2-5.4L2 12l6.2-1.6L5 5l5.4 3.2z"/></svg>`,
  codex: S('<path d="M12 2l8.5 5v10L12 22l-8.5-5V7z"/><path d="M8 10l3 2-3 2M13 15h3"/>'),
  bun: S('<path d="M3 14c0-4.5 4-8 9-8s9 3.5 9 8c0 2.8-4 5-9 5s-9-2.2-9-5z"/><path d="M9 13h.01M15 13h.01M10.5 15.5c.8.6 2.2.6 3 0"/>'),
  go: `<svg viewBox="0 0 24 24" fill="currentColor"><text x="12" y="16.5" font-size="11" font-weight="800" text-anchor="middle" font-family="Inter, sans-serif">go</text></svg>`,
};

const ST = {
  working: `<svg viewBox="0 0 16 16"><circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-opacity=".15" stroke-width="1.7"/><g class="spin"><path d="M8 2a6 6 0 0 1 5.6 3.9" fill="none" stroke="currentColor" stroke-width="1.7" stroke-linecap="round"/></g></svg>`,
  progress: `<svg viewBox="0 0 16 16"><circle cx="8" cy="8" r="6" fill="none" stroke="currentColor" stroke-opacity=".15" stroke-width="1.7"/><path d="M8 2a6 6 0 0 1 3.5 10.9" fill="none" stroke="var(--accent)" stroke-width="1.7" stroke-linecap="round"/></svg>`,
  waiting: `<svg viewBox="0 0 16 16"><circle cx="8" cy="8" r="6" fill="none" stroke="var(--attention)" stroke-width="1.7"/><circle class="pulse" cx="8" cy="8" r="2.5" fill="var(--attention)"/></svg>`,
  failed: `<svg viewBox="0 0 16 16"><circle cx="8" cy="8" r="7" fill="var(--bad)"/><path d="M8 4.5v4.2" stroke="var(--card, #fff)" stroke-width="1.8" stroke-linecap="round"/><circle cx="8" cy="11.3" r="1" fill="var(--card, #fff)"/></svg>`,
  done: `<svg viewBox="0 0 16 16"><circle cx="8" cy="8" r="7" fill="var(--done)"/><path d="M5 8.2l2 2 4-4.2" fill="none" stroke="var(--card, #fff)" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/></svg>`,
  doneseen: `<svg viewBox="0 0 16 16"><path d="M5 8.2l2 2 4-4.2" fill="none" stroke="var(--text-faint)" stroke-width="1.7" stroke-linecap="round" stroke-linejoin="round"/></svg>`,
  exited: `<svg viewBox="0 0 16 16"><circle cx="8" cy="8" r="6" fill="none" stroke="var(--text-faint)" stroke-width="1.5"/><path d="M6 6l4 4M10 6l-4 4" stroke="var(--text-faint)" stroke-width="1.5" stroke-linecap="round"/></svg>`,
};

// h creates an element. Text is always created via text nodes.
function h(tag, props = {}, ...children) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(props)) {
    if (k === "class") el.className = v;
    else if (k === "html") el.innerHTML = v;
    else if (k.startsWith("on")) el.addEventListener(k.slice(2), v);
    else if (v === true) el.setAttribute(k, "");
    else if (v !== false && v != null) el.setAttribute(k, v);
  }
  for (const c of children.flat()) {
    if (c == null || c === false || c === true) continue;
    el.append(c instanceof Node ? c : document.createTextNode(String(c)));
  }
  return el;
}

function icon(name, cls = "") {
  return h("span", { class: `ic ${cls}`, html: IC[name] || "" });
}

function statusIcon(stateName, cls = "") {
  if (!stateName || stateName === "none") return null;
  return h("span", { class: `st ${cls}`, html: ST[stateName] || "" });
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

// --- theme -----------------------------------------------------------------

function applyTheme(t) {
  if (!t || !t.tokens) return;
  currentTheme = t;
  currentThemeStamp = t.stamp || "";
  const root = document.documentElement;

  for (const [k, v] of Object.entries(t.tokens)) {
    const kebab = k.replace(/([A-Z])/g, "-$1").toLowerCase();
    root.style.setProperty(`--${kebab}`, v);
  }
  root.style.colorScheme = t.dark ? "dark" : "light";
  document.body.dataset.theme = t.dark ? "dark" : "light";

  const metaTheme = document.querySelector('meta[name="theme-color"]');
  if (metaTheme) {
    metaTheme.content = t.tokens.wash || t.tokens.sidebar || (t.dark ? "#0c0f14" : "#ededee");
  }

  if (term?.xt) {
    term.xt.options.theme = termColors(t);
  }
  try { localStorage.setItem("omaterm.theme", JSON.stringify(t)); } catch {}
}

// The page starts in the theme it last had, so the pairing page and the
// first paint already wear it; the daemon's answer replaces it.
try { applyTheme(JSON.parse(localStorage.getItem("omaterm.theme"))); } catch {}

function termColors(t) {
  const term = t?.terminal;
  if (!term) return {};
  const ansi = term.ansi || [];
  return {
    foreground: term.foreground,
    background: term.background,
    cursor: term.cursor || term.foreground,
    cursorAccent: term.background,
    selectionBackground: term.selection,
    black: ansi[0], red: ansi[1], green: ansi[2], yellow: ansi[3],
    blue: ansi[4], magenta: ansi[5], cyan: ansi[6], white: ansi[7],
    brightBlack: ansi[8], brightRed: ansi[9], brightGreen: ansi[10], brightYellow: ansi[11],
    brightBlue: ansi[12], brightMagenta: ansi[13], brightCyan: ansi[14], brightWhite: ansi[15],
  };
}

async function fetchTheme() {
  try {
    const res = await fetch("/api/theme");
    if (!res.ok) return;
    const t = await res.json();
    applyTheme(t);
  } catch {}
}

// --- pairing ---------------------------------------------------------------

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
    h("img", { src: "icon.svg", alt: "omaterm" }),
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

// --- state & events --------------------------------------------------------

function connectEvents() {
  const ws = new WebSocket(wsURL("/api/events"));
  events = ws;
  ws.onopen = () => { eventsRetry = 500; };
  ws.onmessage = (e) => {
    const msg = JSON.parse(e.data);
    if (msg.type !== "state") return;
    state = msg.state;
    offline = false;
    if (msg.theme && msg.theme !== currentThemeStamp) {
      fetchTheme();
    }
    render();
  };
  ws.onclose = (e) => {
    if (events !== ws) return;
    events = null;
    if (e.code === 4401) { showPair("This browser was signed out."); return; }
    offline = true;
    render();
    setTimeout(async () => {
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

function workingCount() {
  return state ? state.sessions.filter((s) => s.lead && s.lead.status === "working").length : 0;
}

function doneCount() {
  return state ? state.sessions.filter((s) => s.lead && (s.lead.unread || s.lead.status === "done")).length : 0;
}

function render() {
  if (!paired || !shell) return;
  const waiting = waitingCount();
  document.title = waiting ? `(${waiting}) omaterm` : "omaterm";
  renderSidebar();
  renderPills();
  if (term) term.refresh();
}

function paneByID(id) {
  for (const s of state?.sessions ?? []) {
    for (const p of s.panes ?? []) if (p.id === id) return { session: s, pane: p };
  }
  return null;
}

function label(p) {
  if (!p) return "Terminal";
  if (p.url) return p.title || p.url;
  if (p.agent) return AGENTS[p.agent] || p.agent;
  return p.process || p.shell || "Terminal";
}

function programType(p) {
  if (!p) return "term";
  const str = (p.agent || p.process || p.runtime || p.shell || "").toLowerCase();
  if (str.includes("claude")) return "claude";
  if (str.includes("codex")) return "codex";
  if (str.includes("bun") || str.includes("vite")) return "bun";
  if (str.includes("go")) return "go";
  return "term";
}

function statusState(p) {
  if (!p) return "none";
  switch (p.status) {
    case "working": return p.progress >= 0 ? "progress" : "working";
    case "waiting": return "waiting";
    case "failed":
    case "error": return "failed";
    case "exited": return "exited";
  }
  if (p.unread) return "done";
  if (p.message || p.summary || (p.records && p.records.length > 0)) return "doneseen";
  return "none";
}

function processState(p) {
  if (!p) return "none";
  const st = p.status;
  if (st === "running" || st === "Running") return "running";
  if (st === "starting" || st === "Starting" || st === "working") return "working";
  if (st === "failed" || st === "Failed" || st === "error") return "failed";
  if (st === "exited" || st === "Exited") return "exited";
  if (st === "untrusted" || st === "waiting") return "waiting";
  return "none";
}

function statusChip(p) {
  if (!p) return null;
  switch (p.status) {
    case "waiting": {
      let text = p.message;
      if (!text) {
        switch (p.kind) {
          case "permission": text = "Needs permission"; break;
          case "question": text = "Has a question"; break;
          case "auth": text = "Needs sign-in"; break;
          default: text = "Needs you"; break;
        }
      }
      return h("div", { class: "schip att" }, text);
    }
    case "working": {
      let text = p.progress >= 0 ? `${p.progress}%` : (p.message || (p.since ? ago(p.since) : "Working"));
      return h("div", { class: "schip" }, text);
    }
    case "failed":
    case "error": {
      return h("div", { class: "schip bad" }, p.message || "Failed");
    }
    case "exited": {
      const text = p.exitCode != null && p.exitCode !== 0 ? `Exited ${p.exitCode}` : "Exited";
      return h("div", { class: "schip" }, text);
    }
    default: {
      if (p.message) return h("div", { class: "schip" }, p.message);
      return null;
    }
  }
}

function ago(ms) {
  const s = Math.max(0, (Date.now() - ms) / 1000);
  if (s < 60) return "now";
  if (s < 3600) return `${Math.floor(s / 60)}m`;
  if (s < 86400) return `${Math.floor(s / 3600)}h`;
  return `${Math.floor(s / 86400)}d`;
}

function shortDir(dir, projDir) {
  if (!dir) return "";
  if (projDir && dir === projDir) return "";
  if (projDir && dir.startsWith(projDir + "/")) return dir.slice(projDir.length + 1);
  return dir.replace(/^\/home\/[^/]+/, "~");
}

function terminals(s) {
  return (s.panes ?? []).filter((p) => !p.url);
}

// --- shell layout ----------------------------------------------------------

function showShell() {
  const list = h("div", { class: "side-list", role: "navigation", "aria-label": "Sessions" });
  const foot = h("div", { class: "side-f" });
  const waitingBadge = h("span", { class: "chipc att", hidden: true });

  const pills = h("div", { class: "pills" });
  const content = h("div", { class: "content" });
  const mainView = h("main", { class: "main-view" }, pills, content);

  const sidebar = h("aside", { class: "side" },
    h("header", { class: "side-h" },
      h("img", { class: "logo", src: "icon.svg", alt: "omaterm" }),
      h("span", { class: "lbl" }, "Projects"),
      waitingBadge,
    ),
    list,
    foot,
  );

  const root = h("div", { class: "shell" }, sidebar, mainView);
  app.replaceChildren(root);
  shell = { root, list, foot, waitingBadge, pills, content };
}

function renderPills() {
  if (!shell?.pills || !state) return;
  const open = term?.id;
  const cur = paneByID(open);
  const activeProjId = cur?.session.project || state.projects[0]?.id;
  const projSessions = (state.sessions || []).filter((s) => s.project === activeProjId && terminals(s).length);

  const tabs = [];
  for (const s of projSessions) {
    const panes = terminals(s);
    const lead = (s.lead && s.lead.id) ? s.lead : panes[0];
    const here = panes.some((p) => p.id === open);
    const st = statusState(lead);

    tabs.push(h("a", {
      class: "pill-tab" + (here ? " on" : ""),
      href: "#/pane/" + encodeURIComponent(here ? open : lead.id),
    },
      statusIcon(st, "s11"),
      s.name || label(lead),
    ));
  }

  if (activeProjId) {
    tabs.push(h("button", {
      class: "ib plus", title: "New session in project", "aria-label": "New session",
      onclick: () => newSession(activeProjId),
    }, icon("plus")));
  }

  shell.pills.replaceChildren(...tabs);
}

function renderSidebar() {
  const { list, foot, waitingBadge } = shell;
  const nWaiting = waitingCount();
  const nWorking = workingCount();
  const nDone = doneCount();

  waitingBadge.hidden = !nWaiting;
  if (nWaiting) {
    waitingBadge.replaceChildren(statusIcon("waiting", "s12"), ` ${nWaiting}`);
  }

  const open = term?.id;
  const items = [];

  if (!state) {
    items.push(h("p", { class: "empty" }, "Loading…"));
  } else if (!state.projects.length) {
    items.push(h("p", { class: "empty" }, "No projects yet. Open a folder in omaterm to start."));
  }

  for (const proj of state?.projects ?? []) {
    const allSessions = (state.sessions || []).filter((s) => s.project === proj.id && terminals(s).length);
    const standardSessions = allSessions.filter((s) => !s.process);
    const processSessions = allSessions.filter((s) => Boolean(s.process));

    const projHead = h("div", { class: "proj", title: proj.dir },
      h("span", { class: "chev" }, icon("chev")),
      h("span", { class: "name" }, proj.name),
      h("button", {
        class: "ib add", title: `New session in ${proj.name}`, "aria-label": `New session in ${proj.name}`,
        onclick: (e) => { e.stopPropagation(); newSession(proj.id); },
      }, icon("plus")),
    );

    const sessionElements = standardSessions.map((s) => sessionRow(s, proj, open));
    const processElements = [];

    if (processSessions.length > 0) {
      processElements.push(h("div", { class: "proj-sect" }, "Processes"));
      for (const ps of processSessions) {
        processElements.push(processRow(ps, proj, open));
      }
    }

    items.push(h("section", { class: "project-group" },
      projHead,
      (standardSessions.length || processSessions.length)
        ? [...sessionElements, ...processElements]
        : h("p", { class: "none" }, "No sessions"),
    ));
  }

  list.replaceChildren(...items);

  // Footer chips & sign out
  const footChips = [];
  if (nWaiting > 0) {
    footChips.push(h("div", {
      class: "chipc att", title: "Go to session that needs you",
      onclick: () => jumpToSession((s) => s.lead?.status === "waiting"),
    }, statusIcon("waiting", "s12"), ` ${nWaiting}`));
  }
  if (nWorking > 0) {
    footChips.push(h("div", {
      class: "chipc", title: "Working sessions",
      onclick: () => jumpToSession((s) => s.lead?.status === "working"),
    }, statusIcon("working", "s12"), ` ${nWorking}`));
  }
  if (nDone > 0) {
    footChips.push(h("div", {
      class: "chipc", title: "Done sessions",
      onclick: () => jumpToSession((s) => s.lead?.unread || s.lead?.status === "done"),
    }, statusIcon("done", "s12"), ` ${nDone}`));
  }

  const footElements = [...footChips];
  if (offline) {
    footElements.push(h("span", { class: "offline" }, "Reconnecting…"));
  }
  footElements.push(h("button", {
    class: "link", title: "Sign out this browser",
    onclick: async () => { if (confirm("Sign out this browser?")) { await api("/api/logout", {}); showPair(); } },
  }, icon("signout", "s12"), "Sign out"));

  foot.replaceChildren(...footElements);
}

function jumpToSession(filterFn) {
  if (!state) return;
  const target = state.sessions.find(filterFn);
  if (target) {
    const pane = target.lead?.id || terminals(target)[0]?.id;
    if (pane) location.hash = "#/pane/" + encodeURIComponent(pane);
  }
}

function sessionRow(s, proj, open) {
  const panes = terminals(s);
  const lead = (s.lead && s.lead.id && !s.lead.url) ? s.lead : panes[0];
  const st = statusState(lead);
  const prog = programType(lead);
  const here = panes.some((p) => p.id === open);

  const branch = s.worktree?.branch || (lead.dir && lead.dir !== proj.dir ? shortDir(lead.dir, proj.dir) : "");
  const sub = lead.summary || lead.message || "";

  return h("div", {
    class: "row" + (here ? " sel" : "") + (lead.unread ? " unread" : "") + (lead.status === "exited" ? " exited" : ""),
    onclick: () => { location.hash = "#/pane/" + encodeURIComponent(here ? open : lead.id); },
  },
    h("span", { class: "pi" }, icon(prog)),
    h("div", { class: "mid" },
      h("div", { class: "top" },
        h("span", { class: "nm" }, s.name || label(lead)),
        branch ? h("span", { class: "br" }, branch) : null,
      ),
      sub ? h("span", { class: "sub" }, sub) : null,
    ),
    lead.since ? h("span", { class: "tm" }, ago(lead.since)) : null,
    statusIcon(st, "s14"),
  );
}

// linkHost is the host of a process's link, as the pill shows it.
function linkHost(link) {
  try { return new URL(link).host; } catch { return link; }
}

function processRow(s, proj, open) {
  const panes = terminals(s);
  const lead = (s.lead && s.lead.id) ? s.lead : panes[0];
  const here = panes.some((p) => p.id === open);
  const proc = s.proc;
  const pst = processState(proc ? { status: proc.status } : lead);

  const link = proc?.share || proc?.link || "";
  const port = proc?.port || lead.port;
  let pill = null;
  if (link) {
    pill = h("a", {
      class: "pill mono link", href: link, target: "_blank", rel: "noopener noreferrer",
      title: link, onclick: (e) => e.stopPropagation(),
    }, linkHost(link));
  } else if (port) {
    pill = h("span", { class: "pill mono" }, `:${port}`);
  }

  let statusEl = null;
  if (pst === "running") {
    statusEl = h("span", { class: "dotg" });
  } else {
    statusEl = statusIcon(pst, "s13") || h("span", { class: "dotg", style: "background: var(--text-faint)" });
  }

  return h("div", {
    class: "row" + (here ? " sel" : ""),
    onclick: () => { location.hash = "#/pane/" + encodeURIComponent(here ? open : lead.id); },
  },
    statusEl,
    h("div", { class: "mid" },
      h("div", { class: "top" },
        h("span", { class: "nm" }, s.name || s.process),
      ),
      proc?.message ? h("span", { class: "sub" }, proc.message) : null,
    ),
    pill,
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
  shell.content.replaceChildren(h("div", { class: "pane" },
    h("div", { class: "pick" },
      h("img", { src: "icon.svg", alt: "omaterm" }),
      h("h2", {}, "omaterm"),
      h("p", {}, "Pick a session from the sidebar to open its terminal."),
    ),
  ));
  renderPills();
}

// --- terminal card ---------------------------------------------------------

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

function touchScroll(xt, host) {
  const screen = host.querySelector(".xterm-screen");
  if (!screen) return;
  const row = () => screen.clientHeight / xt.rows || 16;
  const own = () => xt.modes.mouseTrackingMode === "none" && xt.buffer.active.type === "normal";
  let x = 0, y = 0, samples = [], part = 0, owed = 0, pulled = 0, v = 0, glideAt = 0, wheelAt = 0, rFrame = 0;

  const wheel = () => {
    screen.dispatchEvent(new WheelEvent("wheel", {
      deltaY: Math.sign(owed), deltaMode: WheelEvent.DOM_DELTA_LINE, clientX: x, clientY: y, bubbles: true, cancelable: true,
    }));
  };

  const stretch = (ease) => {
    const max = row() * 4, d = Math.abs(pulled) * 0.5;
    const off = Math.sign(pulled) * max * (1 - 1 / (d / max + 1));
    screen.style.transition = ease ? "transform 0.3s cubic-bezier(0.2, 0.8, 0.3, 1)" : "none";
    screen.style.transform = off ? `translateY(${-off}px)` : "";
  };
  const springBack = () => { pulled = 0; stretch(true); };

  const move = (px) => {
    if (!own()) {
      part += px / row();
      const n = Math.trunc(part);
      part -= n;
      owed = Math.max(-60, Math.min(60, owed + n));
      return true;
    }
    const buf = xt.buffer.active;
    if (pulled) {
      const p = pulled + px;
      if (Math.sign(p) === Math.sign(pulled)) { pulled = p; stretch(false); return true; }
      pulled = 0;
      stretch(false);
      px = p;
    }
    if ((px < 0 && buf.viewportY === 0) || (px > 0 && buf.viewportY >= buf.baseY)) {
      pulled += px;
      stretch(false);
      return false;
    }
    part += px / row();
    const n = Math.trunc(part);
    part -= n;
    if (n) xt.scrollLines(n);
    return true;
  };

  const tick = (now) => {
    rFrame = 0;
    if (!host.isConnected) return;
    if (v) {
      const dt = Math.min(now - glideAt, 50);
      glideAt = now;
      if (!move(v * dt)) {
        pulled = Math.sign(v) * Math.min(Math.abs(v) * 60, row() * 6);
        v = 0;
        stretch(false);
        requestAnimationFrame(springBack);
      } else {
        v *= Math.pow(0.996, dt);
        if (Math.abs(v) < 0.02) v = 0;
      }
    }
    if (Math.abs(owed) >= 1) {
      const paced = xt.modes.mouseTrackingMode !== "none";
      if (!paced) { wheel(); owed -= Math.sign(owed); }
      else if (Math.abs(owed) >= 4 && now - wheelAt >= 15) { wheel(); owed -= 2 * Math.sign(owed); wheelAt = now; }
      else if (now - wheelAt >= 45) { wheel(); owed -= Math.sign(owed); wheelAt = now; }
    }
    if (v || Math.abs(owed) >= 1) rFrame = requestAnimationFrame(tick);
  };
  const run = () => { if (!rFrame) rFrame = requestAnimationFrame(tick); };

  host.addEventListener("touchstart", (e) => {
    const t = e.touches[0];
    x = t.clientX; y = t.clientY;
    samples = [{ t: e.timeStamp, y }];
    v = 0; part = 0;
    if (pulled) { pulled = 0; stretch(false); }
  }, { passive: true, capture: true });

  host.addEventListener("touchmove", (e) => {
    const t = e.touches[0];
    e.preventDefault(); e.stopPropagation();
    const dy = y - t.clientY;
    x = t.clientX; y = t.clientY;
    samples.push({ t: e.timeStamp, y });
    while (samples.length > 2 && e.timeStamp - samples[0].t > 100) samples.shift();
    move(dy);
    run();
  }, { passive: false, capture: true });

  const end = (e) => {
    if (pulled) { springBack(); return; }
    const first = samples[0], last = samples[samples.length - 1];
    if (!first || e.timeStamp - last.t > 60 || last.t - first.t < 10) return;
    v = (first.y - last.y) / (last.t - first.t);
    if (Math.abs(v) < 0.2) { v = 0; return; }
    glideAt = performance.now();
    run();
  };
  host.addEventListener("touchend", end, { capture: true });
  host.addEventListener("touchcancel", end, { capture: true });
}

function phoneKeyboard(xt, host) {
  const ta = xt.textarea;
  if (!ta) return;
  let last = "", before = null;
  const ours = (e) => e.target === ta;
  host.addEventListener("keydown", (e) => {
    if (!ours(e)) return;
    last = ta.value;
    if (e.keyCode === 229) e.stopPropagation();
  }, true);
  for (const type of ["compositionstart", "compositionupdate", "compositionend"]) {
    host.addEventListener(type, (e) => { if (ours(e)) e.stopPropagation(); }, true);
  }
  host.addEventListener("beforeinput", (e) => { if (ours(e)) before = ta.value; }, true);
  host.addEventListener("input", (e) => {
    if (!ours(e)) return;
    e.stopPropagation();
    const was = before ?? last, now = ta.value;
    before = null;
    last = now;
    if (e.inputType === "insertFromPaste" || e.inputType === "insertFromDrop") return;
    let i = 0;
    while (i < was.length && i < now.length && was[i] === now[i]) i++;
    if (i > 0 && /[\ud800-\udbff]/.test(was[i - 1])) i--;
    const out = "\x7f".repeat([...was.slice(i)].length) + now.slice(i).replace(/\n/g, "\r");
    if (out) xt.input(out);
    if (now.includes("\n") || (now.length > 200 && now.endsWith(" "))) { ta.value = ""; last = ""; }
  }, true);
}

// showTerm renders a pane as a card on wash.
function showTerm(id) {
  if (term?.id === id) return;
  closeTerm();
  shell.root.dataset.view = "term";

  const f = paneByID(id);
  const proj = state?.projects.find((p) => p.id === f?.session.project);

  const backBtn = h("a", { class: "back-btn", href: "#", "aria-label": "Back to session list" }, icon("back", "s16"));
  const gripLogo = h("span", { class: "logo" });
  const gripTitle = h("span", { class: "t" }, f?.session.name || label(f?.pane));
  const gripCwd = h("span", { class: "cwd" }, shortDir(f?.pane.dir, proj?.dir));
  const grip = h("div", { class: "grip" }, gripLogo, gripTitle, gripCwd);

  const branchChip = h("div", { class: "gchip", hidden: true });
  const tabsRow = h("div", { class: "card-tabs", hidden: true });
  const statusChipEl = h("div", { class: "schip-wrap" });
  const modChip = h("div", { class: "schip mods", hidden: true });

  let composeBox = null;
  const composeBtn = h("button", {
    class: "ib btn-compose", hidden: true, title: "Compose prompt", "aria-label": "Compose prompt",
    onclick: () => {
      composeBox?.open();
    },
  }, icon("compose", "s14"));

  const header = h("header", { class: "ph" },
    backBtn, grip, branchChip, tabsRow,
    h("span", { class: "sp" }),
    modChip,
    statusChipEl,
    composeBtn,
  );

  const host = h("div", { class: "term-host" });
  const wrap = h("div", { class: "term-wrap" }, host);
  const note = h("div", { class: "term-note", hidden: true });

  const composeSlot = h("div", { class: "compose-slot" });
  const keybarSlot = h("div", { class: "keybar-slot" });
  const bottomBar = h("div", { class: "term-bottom" }, composeSlot, keybarSlot);

  const card = h("div", { class: "pane" }, header, wrap, note, bottomBar);
  shell.content.replaceChildren(card);

  const xt = new Terminal({
    fontFamily: getComputedStyle(document.documentElement).getPropertyValue("--mono") || "JetBrains Mono, monospace",
    fontSize: touch ? 12 : 13,
    theme: termColors(currentTheme),
    scrollback: 10000,
    cursorBlink: true,
    macOptionIsMeta: true,
    allowProposedApi: false,
  });
  const fit = new FitAddon.FitAddon();
  xt.loadAddon(fit);

  let ws = null, closed = false, retry = 500, gone = false, sized = false;

  const send = (bytes) => { if (ws && ws.readyState === 1) ws.send(frame("i", bytes)); };
  const sendText = (s) => send(enc.encode(s));
  const sendKey = (k) => { if (ws && ws.readyState === 1) ws.send(frame("k", enc.encode(k))); };
  const sendPaste = (p) => { if (ws && ws.readyState === 1) ws.send(frame("p", enc.encode(p))); };
  const appCursor = () => Boolean(xt.modes?.applicationCursorKeysMode);

  const setNote = (text) => { note.hidden = !text; note.textContent = text || ""; };

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
      if (e.code === 4410) { setNote(e.reason); return; }
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

  let ctrl = false;
  let keyBar = null;
  xt.onData((d) => {
    if (keyBar && d.length === 1) {
      const m = keyBar.takeModifiers();
      if (m.ctrl) {
        const c = d.toUpperCase().charCodeAt(0);
        if (c >= 64 && c < 96) d = String.fromCharCode(c & 0x1f);
      }
      if (m.alt) d = "\x1b" + d;
    } else if (ctrl && d.length === 1) {
      const c = d.toUpperCase().charCodeAt(0);
      if (c >= 64 && c < 96) d = String.fromCharCode(c & 0x1f);
      ctrl = false;
      const ctrlBtn = keybarSlot.querySelector('button[data-key="ctrl"]');
      if (ctrlBtn) ctrlBtn.setAttribute("aria-pressed", "false");
    }
    sendText(d);
  });
  xt.onBinary((d) => send(Uint8Array.from(d, (c) => c.charCodeAt(0) & 0xff)));

  // Fallback key bar for touch
  function mountFallbackKeys() {
    keybarSlot.replaceChildren();
    const arrow = (k) => () => sendText((xt.modes.applicationCursorKeysMode ? "\x1bO" : "\x1b[") + k);
    const key = (labelStr, fn, aria, dKey) => h("button", {
      "aria-label": aria || labelStr,
      "data-key": dKey || "",
      onclick: (e) => { e.preventDefault(); fn(); xt.focus(); },
    }, labelStr);

    const ctrlBtn = h("button", {
      "data-key": "ctrl", "aria-pressed": "false", "aria-label": "Control",
      onclick: (e) => {
        e.preventDefault();
        ctrl = !ctrl;
        ctrlBtn.setAttribute("aria-pressed", String(ctrl));
        xt.focus();
      },
    }, "Ctrl");

    const fallbackBar = h("div", { class: "keys" },
      key("Esc", () => sendText("\x1b")),
      key("Tab", () => sendText("\t")),
      ctrlBtn,
      key("^C", () => sendText("\x03"), "Control C"),
      key("↑", arrow("A"), "Up"), key("↓", arrow("B"), "Down"), key("←", arrow("D"), "Left"), key("→", arrow("C"), "Right"),
      key("⏎", () => sendText("\r"), "Enter"),
    );
    keybarSlot.append(fallbackBar);
  }

  mountFallbackKeys();

  // Mount external modules if available
  keysPromise.then((mod) => {
    if (mod?.mountKeyBar && keybarSlot.isConnected) {
      keybarSlot.replaceChildren();
      keyBar = mod.mountKeyBar(keybarSlot, {
        sendKey, sendText, appCursor,
        onModifiers: (m) => {
          const on = [];
          if (m.ctrl) on.push(m.ctrlLocked ? "Ctrl locked" : "Ctrl");
          if (m.alt) on.push(m.altLocked ? "Alt locked" : "Alt");
          modChip.hidden = !on.length;
          modChip.textContent = on.join(" · ");
        },
      });
    }
  });

  composePromise.then((mod) => {
    if (mod?.mountCompose && composeSlot.isConnected) {
      composeSlot.replaceChildren();
      composeBox = mod.mountCompose(composeSlot, { sendPaste, sendKey });
      composeBtn.hidden = false;
    }
  });

  xt.open(host);

  if (touch) {
    touchScroll(xt, host);
    phoneKeyboard(xt, host);
  }

  let resizeTimer = 0;
  const observer = new ResizeObserver(() => {
    clearTimeout(resizeTimer);
    resizeTimer = setTimeout(fill, 100);
  });
  observer.observe(host);

  const onVisible = () => { if (document.visibilityState === "visible" && !ws && !closed && !gone) connect(); };
  document.addEventListener("visibilitychange", onVisible);

  const refresh = () => {
    const cur = paneByID(id);
    if (!cur) return;
    const curPanes = terminals(cur.session);
    const curProj = state?.projects.find((p) => p.id === cur.session.project);

    gripTitle.textContent = cur.session.name || label(cur.pane);
    gripCwd.textContent = shortDir(cur.pane.dir, curProj?.dir);

    const st = statusState(cur.pane);
    const prog = programType(cur.pane);
    gripLogo.replaceChildren(statusIcon(st, "s13") || icon(prog, "s13"));

    const branch = cur.session.worktree?.branch || (cur.pane.dir && cur.pane.dir !== curProj?.dir ? shortDir(cur.pane.dir, curProj?.dir) : "");
    if (branch) {
      branchChip.hidden = false;
      branchChip.replaceChildren(icon("branch", "s11"), h("span", {}, branch));
    } else {
      branchChip.hidden = true;
    }

    if (curPanes.length > 1) {
      tabsRow.hidden = false;
      tabsRow.replaceChildren(...curPanes.map((p) =>
        h("a", {
          class: "card-tab" + (p.id === id ? " active" : ""),
          href: "#/pane/" + encodeURIComponent(p.id),
        }, label(p)),
      ));
    } else {
      tabsRow.hidden = true;
    }

    const sc = statusChip(cur.pane);
    statusChipEl.replaceChildren(sc ? sc : "");

    const waiting = waitingCount();
    document.title = (waiting ? `(${waiting}) ` : "") + gripTitle.textContent + " · omaterm";
  };

  term = {
    id,
    xt,
    refresh,
    close() {
      closed = true;
      observer.disconnect();
      clearTimeout(resizeTimer);
      document.removeEventListener("visibilitychange", onVisible);
      keyBar?.destroy();
      composeBox?.destroy();
      if (ws) { ws.onclose = null; ws.close(); }
      xt.dispose();
    },
  };

  refresh();
  renderSidebar();
  renderPills();
  connect();
  if (!touch) xt.focus();
}

// --- routes & start --------------------------------------------------------

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
  await fetchTheme();
  if (location.hash.startsWith("#code=")) history.replaceState(null, "", location.pathname);
  showShell();
  if (!events) connectEvents();
  route();
  render();
}

addEventListener("hashchange", route);

(async () => {
  const m = location.hash.match(/^#code=([0-9A-Za-z-]+)$/);
  if (m) {
    history.replaceState(null, "", location.pathname);
    try { await pair(m[1]); } catch (err) { showPair(err.message); return; }
  }
  start();
})();
