// The key bar for the web terminal: a scrollable row of touch-friendly
// keys, and a sheet to choose and order them.
//
//   import { mountKeyBar } from "./keys.js";
//   const bar = mountKeyBar(el, { sendKey, sendText, appCursor, onModifiers });
//   bar.destroy();
//
// sendKey(name) sends a key by name to the daemon ("esc", "shift+tab",
// "up", "ctrl+enter"), which encodes it as the program asked keys to be
// sent. sendText(text) sends text as typed input, as if the user typed it, not
// as a paste: a newline in it goes as it is (a line feed).
//
// Ctrl and Alt are sticky: one tap applies them to the next key, or to the
// next character the user types in the terminal, and then releases; a
// second tap locks them until a third. Before the page sends a character
// the user typed, it calls takeModifiers(), which returns {ctrl, alt}
// (true when that modifier applies) and releases the one-shot ones; locked
// modifiers stay on. onModifiers, when given, is called with
// {ctrl, alt, ctrlLocked, altLocked} whenever that state changes, so the
// page can show it beside the terminal.
//
// appCursor tells that the terminal is in application cursor mode (a
// boolean, or a function returning one): the arrow keys then go as
// "app+up" and so on, which the daemon sends as ESC O A rather than
// ESC [ A. The keys and their order live in localStorage under
// omaterm.keybar; without storage the bar works and forgets.

const STORE = "omaterm.keybar";

// DEFAULTS are the keys the bar starts with.
const DEFAULTS = [
  { key: "esc", label: "Esc" },
  { key: "tab", label: "Tab" },
  { key: "shift+tab", label: "⇧Tab" },
  { mod: "ctrl", label: "Ctrl" },
  { mod: "alt", label: "Alt" },
  { key: "up", label: "↑" },
  { key: "down", label: "↓" },
  { key: "left", label: "←" },
  { key: "right", label: "→" },
  { key: "ctrl+c", label: "⌃C" },
  { key: "ctrl+enter", label: "⌃⏎" },
  { key: "enter", label: "⏎" },
];

// CATALOGUE is what the edit sheet offers besides the defaults.
const CATALOGUE = [
  { key: "home", label: "Home" },
  { key: "end", label: "End" },
  { key: "pageup", label: "PgUp" },
  { key: "pagedown", label: "PgDn" },
  { key: "insert", label: "Ins" },
  { key: "delete", label: "Del" },
  { key: "f1", label: "F1" },
  { key: "f2", label: "F2" },
  { key: "f3", label: "F3" },
  { key: "f4", label: "F4" },
  { key: "f5", label: "F5" },
  { key: "f6", label: "F6" },
  { key: "f7", label: "F7" },
  { key: "f8", label: "F8" },
  { key: "f9", label: "F9" },
  { key: "f10", label: "F10" },
  { key: "f11", label: "F11" },
  { key: "f12", label: "F12" },
  { key: "ctrl+d", label: "⌃D" },
  { key: "ctrl+z", label: "⌃Z" },
  { key: "ctrl+l", label: "⌃L" },
  { key: "ctrl+r", label: "⌃R" },
  { key: "shift+enter", label: "⇧⏎" },
  { key: "alt+enter", label: "Alt+⏎" },
  { key: "space", label: "Space" },
];

// LABELS name the keys a name alone would not show well.
const LABELS = {
  esc: "Esc",
  tab: "Tab",
  "shift+tab": "⇧Tab",
  enter: "⏎",
  "ctrl+enter": "⌃⏎",
  "shift+enter": "⇧⏎",
  "alt+enter": "Alt+⏎",
  "ctrl+c": "⌃C",
  up: "↑",
  down: "↓",
  left: "←",
  right: "→",
  space: "Space",
  backspace: "⌫",
  delete: "Del",
  insert: "Ins",
  home: "Home",
  end: "End",
  pageup: "PgUp",
  pagedown: "PgDn",
};

const ARROWS = new Set(["up", "down", "left", "right"]);

// labelFor is what a key shows on the bar.
function labelFor(item) {
  if (item.label) return item.label;
  if (item.text != null) return "Text";
  if (LABELS[item.key]) return LABELS[item.key];
  const m = /^(ctrl|alt)\+(.+)$/.exec(item.key || "");
  if (m) return (m[1] === "ctrl" ? "⌃" : "Alt+") + m[2].toUpperCase();
  return item.key || "?";
}

// isItem accepts a stored item: a key, a modifier or a text snippet.
function isItem(v) {
  return !!v && typeof v === "object" &&
    (typeof v.key === "string" || v.mod === "ctrl" || v.mod === "alt" || typeof v.text === "string");
}

// load returns the saved keys, or null when there are none or storage
// cannot be read.
function load() {
  try {
    const raw = localStorage.getItem(STORE);
    if (!raw) return null;
    const v = JSON.parse(raw);
    if (!Array.isArray(v)) return null;
    const items = v.filter(isItem).map((x) => ({ ...x }));
    return items.length ? items : null;
  } catch {
    return null;
  }
}

function save(items) {
  try {
    localStorage.setItem(STORE, JSON.stringify(items));
  } catch {
    // A private window, or no storage: the bar still works this time.
  }
}

function clone(item) {
  return { ...item };
}

export function mountKeyBar(el, options = {}) {
  const { sendKey, sendText, onModifiers } = options;
  const appCursor = options.appCursor;
  let items = load() || DEFAULTS.map(clone);

  const mods = { ctrl: false, alt: false, ctrlLocked: false, altLocked: false };
  const ac = new AbortController();
  const on = (target, type, fn, opts) => target.addEventListener(type, fn, { ...opts, signal: ac.signal });

  // The bar.
  const root = document.createElement("div");
  root.className = "keybar";
  root.setAttribute("role", "toolbar");
  root.setAttribute("aria-label", "Terminal keys");
  const row = document.createElement("div");
  row.className = "keybar-row";
  const end = document.createElement("div");
  end.className = "keybar-end";
  root.append(row, end);
  el.append(root);
  // Tapping a key must not take the focus from the terminal.
  on(root, "pointerdown", (e) => e.preventDefault());

  function cursorIsApp() {
    try {
      return typeof appCursor === "function" ? !!appCursor() : !!appCursor;
    } catch {
      return false;
    }
  }

  function notify() {
    if (typeof onModifiers !== "function") return;
    try {
      onModifiers({ ctrl: mods.ctrl, alt: mods.alt, ctrlLocked: mods.ctrlLocked, altLocked: mods.altLocked });
    } catch {
      // A page's callback that fails is no reason to stop the bar.
    }
  }

  function renderMods() {
    for (const b of root.querySelectorAll(".keybar-mod")) {
      const which = b.dataset.mod;
      const active = !!mods[which];
      const locked = !!mods[which + "Locked"];
      b.classList.toggle("is-on", active);
      b.classList.toggle("is-locked", locked);
      b.setAttribute("aria-pressed", active ? "true" : "false");
      b.title = which === "ctrl"
        ? (locked ? "Ctrl is locked: tap to release" : "Ctrl for the next key")
        : (locked ? "Alt is locked: tap to release" : "Alt for the next key");
    }
  }

  function tapMod(which) {
    const locked = which + "Locked";
    if (mods[which] && !mods[locked]) {
      mods[locked] = true; // a second tap locks
    } else if (mods[locked]) {
      mods[which] = false; // a third releases
      mods[locked] = false;
    } else {
      mods[which] = true; // one-shot
    }
    renderMods();
    notify();
  }

  // release clears the modifiers that were for one key only.
  function release() {
    if (mods.ctrl && !mods.ctrlLocked) mods.ctrl = false;
    if (mods.alt && !mods.altLocked) mods.alt = false;
    renderMods();
    notify();
  }

  // takeModifiers is the page's: it takes the modifiers that apply to a
  // typed character, and releases the one-shot ones.
  function takeModifiers() {
    const taken = { ctrl: mods.ctrl, alt: mods.alt };
    if (mods.ctrl && !mods.ctrlLocked) mods.ctrl = false;
    if (mods.alt && !mods.altLocked) mods.alt = false;
    renderMods();
    notify();
    return taken;
  }

  function press(item) {
    if (item.text != null) {
      if (typeof sendText === "function") sendText(item.text);
      release();
      return;
    }
    if (!item.key) return;
    let name = item.key;
    if (ARROWS.has(name) && cursorIsApp()) name = "app+" + name;
    if (mods.ctrl && !name.startsWith("ctrl+")) name = "ctrl+" + name;
    if (mods.alt && !name.startsWith("alt+")) name = "alt+" + name;
    if (typeof sendKey === "function") sendKey(name);
    release();
  }

  function keyButton(item) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "keybar-key";
    b.textContent = labelFor(item);
    b.setAttribute("aria-label", item.text != null ? "Text: " + item.text : (item.key || ""));
    if (item.text != null) b.title = item.text;
    on(b, "click", () => press(item));
    return b;
  }

  function modButton(which) {
    const b = document.createElement("button");
    b.type = "button";
    b.className = "keybar-key keybar-mod";
    b.dataset.mod = which;
    b.textContent = which === "ctrl" ? "Ctrl" : "Alt";
    on(b, "click", () => tapMod(which));
    return b;
  }

  function renderRow() {
    row.replaceChildren();
    for (const item of items) {
      if (item.mod === "ctrl" || item.mod === "alt") row.append(modButton(item.mod));
      else row.append(keyButton(item));
    }
    const edit = document.createElement("button");
    edit.type = "button";
    edit.className = "keybar-key keybar-edit";
    edit.textContent = "Edit";
    edit.title = "Choose and order the keys";
    on(edit, "click", openSheet);
    end.replaceChildren(edit);
    renderMods();
  }

  // The edit sheet, a modal over the page.
  let sheet = null;
  let lastFocus = null;

  function openSheet() {
    if (sheet) return;
    lastFocus = document.activeElement;
    sheet = document.createElement("div");
    sheet.className = "keybar-sheet";
    sheet.setAttribute("role", "dialog");
    sheet.setAttribute("aria-modal", "true");
    sheet.setAttribute("aria-label", "Edit keys");

    const panel = document.createElement("div");
    panel.className = "keybar-panel";
    sheet.append(panel);

    const head = document.createElement("div");
    head.className = "keybar-head";
    const title = document.createElement("h2");
    title.textContent = "Keys";
    const done = document.createElement("button");
    done.type = "button";
    done.className = "keybar-done";
    done.textContent = "Done";
    head.append(title, done);
    panel.append(head);

    // The rest scrolls under the head, so Done stays in reach.
    const body = document.createElement("div");
    body.className = "keybar-body";
    panel.append(body);

    const hint = document.createElement("p");
    hint.className = "keybar-hint";
    hint.textContent = "Tap a key to add it. Ctrl and Alt on the bar apply to the next key.";
    body.append(hint);

    const list = document.createElement("div");
    list.className = "keybar-list";
    body.append(list);

    const catalogue = document.createElement("div");
    catalogue.className = "keybar-catalogue";
    body.append(catalogue);

    const form = document.createElement("form");
    form.className = "keybar-text";
    const input = document.createElement("input");
    input.type = "text";
    input.placeholder = "A snippet of text…";
    input.setAttribute("aria-label", "Text to paste");
    const add = document.createElement("button");
    add.type = "submit";
    add.textContent = "Add text";
    form.append(input, add);
    body.append(form);

    const foot = document.createElement("div");
    foot.className = "keybar-foot";
    const reset = document.createElement("button");
    reset.type = "button";
    reset.className = "keybar-reset";
    reset.textContent = "Reset";
    foot.append(reset);
    body.append(foot);

    function renderSheet() {
      list.replaceChildren();
      items.forEach((item, i) => {
        const line = document.createElement("div");
        line.className = "keybar-item";
        const name = document.createElement("span");
        name.className = "keybar-item-name";
        name.textContent = labelFor(item);
        line.append(name);
        const up = document.createElement("button");
        up.type = "button";
        up.textContent = "↑";
        up.title = "Move up";
        up.disabled = i === 0;
        on(up, "click", () => move(i, -1));
        const down = document.createElement("button");
        down.type = "button";
        down.textContent = "↓";
        down.title = "Move down";
        down.disabled = i === items.length - 1;
        on(down, "click", () => move(i, 1));
        const remove = document.createElement("button");
        remove.type = "button";
        remove.textContent = "×";
        remove.title = "Remove";
        on(remove, "click", () => {
          items.splice(i, 1);
          save(items);
          renderRow();
          renderSheet();
        });
        line.append(up, down, remove);
        list.append(line);
      });

      catalogue.replaceChildren();
      for (const entry of CATALOGUE) {
        const b = document.createElement("button");
        b.type = "button";
        b.textContent = entry.label;
        on(b, "click", () => {
          items.push(clone(entry));
          save(items);
          renderRow();
          renderSheet();
        });
        catalogue.append(b);
      }
    }

    function move(i, by) {
      const j = i + by;
      if (j < 0 || j >= items.length) return;
      [items[i], items[j]] = [items[j], items[i]];
      save(items);
      renderRow();
      renderSheet();
    }

    on(done, "click", closeSheet);
    on(sheet, "click", (e) => {
      if (e.target === sheet) closeSheet();
    });
    on(document, "keydown", (e) => {
      if (e.key === "Escape") {
        e.preventDefault();
        closeSheet();
      }
    });
    on(reset, "click", () => {
      items = DEFAULTS.map(clone);
      save(items);
      renderRow();
      renderSheet();
    });
    on(form, "submit", (e) => {
      e.preventDefault();
      const text = input.value.trim();
      if (!text) return;
      items.push({ text, label: text.split("\n")[0].slice(0, 16) || "Text" });
      input.value = "";
      save(items);
      renderRow();
      renderSheet();
    });

    document.body.append(sheet);
    renderSheet();
    done.focus();
  }

  function closeSheet() {
    if (!sheet) return;
    sheet.remove();
    sheet = null;
    if (lastFocus && typeof lastFocus.focus === "function") {
      try {
        lastFocus.focus();
      } catch {
        // The terminal went away meanwhile.
      }
    }
  }

  renderRow();

  return {
    destroy() {
      closeSheet();
      ac.abort();
      root.remove();
    },
    takeModifiers,
  };
}
