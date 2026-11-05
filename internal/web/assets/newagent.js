// The New Agent sheet: '+' asks which agent to start in a project, in a
// worktree of its own or not, and the first task to give it, as the
// window's New Agent dialog does. The page names the agent, never a
// command; the computer starts it and types the task in once the agent
// is at its prompt.
//
//   import { openNewAgent } from "./newagent.js";
//   openNewAgent({ project, projects, api, h, open });
//
// open(pane) shows the session made. A worktree slow to make is answered
// 202 with its branch: the sheet waits for its session to show in the
// state, then opens it. The agent and the worktree switch last chosen are
// kept in localStorage, as the window keeps them.

const STORE = "omaterm.newagent";
// waitLimit is how long the sheet waits for a slow worktree's session.
const waitLimit = 6 * 60 * 1000;

let sheet = null;

function stored() {
  try {
    return JSON.parse(localStorage.getItem(STORE)) || {};
  } catch {
    return {};
  }
}

function keep(v) {
  try {
    localStorage.setItem(STORE, JSON.stringify(v));
  } catch {
    // No storage: the choice lasts this time only.
  }
}

// styles loads the sheet's stylesheet, once, and resolves when it has.
function styles() {
  if (document.querySelector('link[href="newagent.css"]')) return Promise.resolve();
  return new Promise((done) => {
    const l = document.createElement("link");
    l.rel = "stylesheet";
    l.href = "newagent.css";
    l.onload = l.onerror = done;
    document.head.append(l);
  });
}

const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

export async function openNewAgent({ project, projects, api, h, open }) {
  await styles();
  sheet?.close(true);
  const was = stored();
  const ac = new AbortController();
  // busy while the computer makes the session; waiting, for a slow
  // worktree's session to show, which closing gives up on.
  let busy = false;
  let waiting = false;
  let opts = { agents: [], git: false, branch: "" };
  let agent = was.agent || "";
  let lastFocus = document.activeElement;

  const close = (now = false) => {
    if (busy && !now) return;
    ac.abort();
    sheet = null;
    if (now || matchMedia("(prefers-reduced-motion: reduce)").matches) {
      root.remove();
    } else {
      root.classList.add("leaving");
      setTimeout(() => root.remove(), 160);
    }
    try { lastFocus?.focus?.(); } catch { /* it went away meanwhile */ }
  };

  const note = h("p", { class: "na-note", role: "alert", hidden: true });
  const say = (text, bad = true) => {
    note.textContent = text || "";
    note.hidden = !text;
    note.classList.toggle("bad", bad);
  };

  const projPick = h("select", { class: "na-input", id: "na-project", "aria-label": "Project" },
    ...projects.map((p) => h("option", { value: p.id, selected: p.id === project }, p.name)));
  const chips = h("div", { class: "na-chips", role: "radiogroup", "aria-label": "Agent" });
  const agentHint = h("p", { class: "na-hint", hidden: true });

  const wtSwitch = h("input", { type: "checkbox", role: "switch", class: "na-switch", id: "na-wt" });
  wtSwitch.checked = Boolean(was.worktree);
  const branch = h("input", {
    class: "na-input", id: "na-branch", placeholder: "fix-login", autocomplete: "off", autocapitalize: "off",
    autocorrect: "off", spellcheck: "false", enterkeyhint: "next", maxlength: "100", "aria-label": "Branch",
  });
  const wtHint = h("p", { class: "na-hint" });
  const wtField = h("div", { class: "na-sub" }, branch, wtHint);

  const task = h("textarea", {
    class: "na-input na-task", id: "na-task", rows: "3", maxlength: "16000",
    placeholder: "What should it do?", "aria-label": "First task",
  });
  const taskField = h("div", { class: "na-field" },
    h("label", { class: "na-label", for: "na-task" }, "First task"),
    task,
    h("p", { class: "na-hint" }, "Typed in once the agent is ready. Leave it empty to start it idle."),
  );

  const start = h("button", { type: "submit", class: "na-start" }, "Start");
  const cancel = h("button", { type: "button", class: "na-cancel", onclick: () => close() }, "Cancel");
  const form = h("form", { class: "na-panel" },
    h("div", { class: "na-head" },
      h("span", {}, "New Agent"),
      h("button", { type: "button", class: "na-close", title: "Close", "aria-label": "Close", onclick: () => close() }, "×"),
    ),
    h("div", { class: "na-body" },
      projects.length > 1
        ? h("div", { class: "na-field" }, h("label", { class: "na-label", for: "na-project" }, "Project"), projPick)
        : null,
      h("div", { class: "na-field" }, h("span", { class: "na-label" }, "Agent"), chips, agentHint),
      h("div", { class: "na-field" },
        h("label", { class: "na-row", for: "na-wt" }, h("span", { class: "na-label" }, "Own worktree"), wtSwitch),
        wtField,
      ),
      taskField,
      note,
    ),
    h("div", { class: "na-foot" }, cancel, start),
  );
  const root = h("div", { class: "na", role: "dialog", "aria-modal": "true", "aria-label": "New Agent" },
    h("div", { class: "na-backdrop", onclick: () => close() }),
    form,
  );

  const projName = () => projects.find((p) => p.id === projPick.value)?.name || "";

  // show draws what the choices make of the sheet.
  function show() {
    const shell = agent === "shell";
    for (const c of chips.children) {
      const on = c.dataset.agent === agent;
      c.classList.toggle("on", on);
      c.setAttribute("aria-checked", String(on));
      c.tabIndex = on ? 0 : -1;
    }
    const a = opts.agents.find((x) => x.name === agent);
    agentHint.hidden = !a || a.found;
    if (a && !a.found) agentHint.textContent = `${a.name} isn't on the computer's PATH: it starts if your login shell finds it.`;
    wtSwitch.disabled = !opts.git || busy || waiting;
    if (!opts.git) wtSwitch.checked = false;
    wtField.hidden = !wtSwitch.checked;
    const b = branch.value.trim() || "<branch>";
    wtHint.textContent = opts.git
      ? `A new branch from ${opts.branch || "the default branch"}, in ${projName()}/.worktrees/${b}.`
      : "";
    if (!opts.git) {
      wtField.hidden = false;
      branch.hidden = true;
      wtHint.textContent = `${projName()} isn't in a git repository, which a worktree needs.`;
    } else {
      branch.hidden = false;
    }
    taskField.hidden = shell;
    start.disabled = busy || waiting || !agent;
    start.textContent = busy || waiting ? "Starting…" : "Start";
    for (const el of [projPick, branch, task]) el.disabled = busy || waiting;
    cancel.disabled = busy;
  }

  // load asks the computer what the project offers: its agents, and
  // whether it can have a worktree.
  async function load() {
    try {
      opts = await api("/api/new?project=" + encodeURIComponent(projPick.value));
    } catch (err) {
      if (err.message === "not paired") { close(true); return; }
      say(err.message);
      return;
    }
    if (!opts.agents.some((a) => a.name === agent)) {
      agent = (opts.agents.find((a) => a.found && a.name !== "shell") || opts.agents.at(-1))?.name || "";
    }
    chips.replaceChildren(...opts.agents.map((a) => h("button", {
      type: "button", role: "radio", class: "na-chip" + (a.found ? "" : " dim"), "data-agent": a.name,
      title: a.found ? a.label : `${a.label}: not on the computer's PATH`,
      onclick: () => { agent = a.name; say(""); show(); },
    }, a.label)));
    show();
  }

  // waitFor opens the session a slow worktree makes, once it shows.
  async function waitFor(proj, name) {
    const until = Date.now() + waitLimit;
    while (Date.now() < until && !ac.signal.aborted) {
      await sleep(2000);
      let st;
      try { st = await api("/api/state"); } catch { continue; }
      const s = (st.sessions || []).find((x) => x.project === proj && x.worktree?.branch === name);
      const pane = s && (s.lead?.id || (s.panes || []).find((p) => !p.url)?.id);
      if (pane) return pane;
    }
    return "";
  }

  async function submit(e) {
    e.preventDefault();
    if (busy || waiting || !agent) return;
    const body = { project: projPick.value, agent };
    if (wtSwitch.checked) {
      const b = branch.value.trim();
      if (!b) {
        say("Name the branch");
        branch.focus();
        return;
      }
      body.worktree = { branch: b };
    }
    if (agent !== "shell" && task.value.trim()) body.task = task.value;
    busy = true;
    say("");
    show();
    let made;
    try {
      made = await api("/api/sessions", body);
    } catch (err) {
      busy = false;
      if (err.message === "not paired") { close(true); return; }
      say(err.message);
      show();
      return;
    }
    keep({ agent, worktree: wtSwitch.checked });
    busy = false;
    let pane = made.pane;
    if (!pane && made.branch) {
      waiting = true;
      say(`Making the worktree for ${made.branch}: the session opens once it's ready.`, false);
      show();
      pane = await waitFor(body.project, made.branch);
      waiting = false;
      if (ac.signal.aborted) return;
      if (!pane) {
        say(`It's taking long: ${made.branch} shows in the list once it's made.`, false);
        show();
        return;
      }
    }
    close(true);
    open(pane);
    if (made.taskError) alert("The session started, but its first task wasn't queued: " + made.taskError);
  }

  const on = (el, type, fn) => el.addEventListener(type, fn, { signal: ac.signal });
  on(form, "submit", submit);
  on(projPick, "change", () => { say(""); load(); });
  on(wtSwitch, "change", () => {
    show();
    if (wtSwitch.checked) branch.focus();
  });
  on(branch, "input", show);
  on(chips, "keydown", (e) => {
    // Arrows move between the agents, as in a radio group.
    const step = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
    if (!step) return;
    e.preventDefault();
    const list = opts.agents.map((a) => a.name);
    agent = list[(list.indexOf(agent) + step + list.length) % list.length];
    show();
    chips.querySelector(".on")?.focus();
  });
  on(document, "keydown", (e) => { if (e.key === "Escape") close(); });

  sheet = { close };
  document.body.append(root);
  show();
  await load();
  (chips.querySelector(".on") || start).focus({ preventScroll: true });
}
