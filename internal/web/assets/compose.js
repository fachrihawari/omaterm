// The compose box for the web terminal: a multi-line text box for writing
// a prompt with the phone keyboard, where autocorrect is welcome, then
// sending it as a paste.
//
//   import { mountCompose } from "./compose.js";
//   const compose = mountCompose(el, { sendPaste, sendKey });
//   compose.open(); compose.close(); compose.destroy();
//
// sendPaste and sendKey return false when they could not send; the text then
// stays in the box, with a note. Send pastes the text (sendPaste) and then presses Enter (sendKey), so a
// prompt is submitted once; Insert pastes it without Enter, to edit it in
// the terminal. An unsent draft is kept in localStorage under
// omaterm.compose; without storage the box works and forgets.

const STORE = "omaterm.compose";

function load() {
  try {
    return localStorage.getItem(STORE) || "";
  } catch {
    return "";
  }
}

function save(text) {
  try {
    localStorage.setItem(STORE, text);
  } catch {
    // A private window, or no storage: the draft lives this time only.
  }
}

export function mountCompose(el, options = {}) {
  const { sendPaste, sendKey } = options;
  const ac = new AbortController();
  const on = (target, type, fn, opts) => target.addEventListener(type, fn, { ...opts, signal: ac.signal });

  const root = document.createElement("div");
  root.className = "compose";
  root.hidden = true;
  root.setAttribute("role", "dialog");
  root.setAttribute("aria-modal", "true");
  root.setAttribute("aria-label", "Compose a prompt");

  const backdrop = document.createElement("div");
  backdrop.className = "compose-backdrop";

  const panel = document.createElement("div");
  panel.className = "compose-panel";

  const head = document.createElement("div");
  head.className = "compose-head";
  const title = document.createElement("span");
  title.textContent = "Compose";
  const closeButton = document.createElement("button");
  closeButton.type = "button";
  closeButton.className = "compose-close";
  closeButton.textContent = "×";
  closeButton.title = "Close";
  closeButton.setAttribute("aria-label", "Close");
  head.append(title, closeButton);

  const box = document.createElement("textarea");
  box.className = "compose-box";
  box.rows = 4;
  box.placeholder = "Write a prompt…";
  box.value = load();

  const foot = document.createElement("div");
  foot.className = "compose-foot";
  const insert = document.createElement("button");
  insert.type = "button";
  insert.className = "compose-insert";
  insert.textContent = "Insert";
  insert.title = "Paste without Enter";
  const send = document.createElement("button");
  send.type = "button";
  send.className = "compose-send";
  send.textContent = "Send ⏎";
  const note = document.createElement("span");
  note.className = "compose-note";
  note.setAttribute("role", "alert");
  note.hidden = true;
  foot.append(note, insert, send);

  panel.append(head, box, foot);
  root.append(backdrop, panel);
  el.append(root);

  let lastFocus = null;

  function open() {
    note.hidden = true;
    lastFocus = document.activeElement;
    root.hidden = false;
    box.focus();
    const end = box.value.length;
    try {
      box.setSelectionRange(end, end);
    } catch {
      // A browser that does not let a textarea be selected, still types.
    }
  }

  function close() {
    root.hidden = true;
    save(box.value);
    if (lastFocus && typeof lastFocus.focus === "function") {
      try {
        lastFocus.focus();
      } catch {
        // The terminal went away meanwhile.
      }
    }
  }

  function clear() {
    box.value = "";
    save("");
  }

  // submit sends the text, and Enter after it. A send that reports it did
  // not go (false: the terminal's socket is down) keeps the text and the box,
  // so nothing the user wrote is lost.
  function submit(withEnter) {
    const text = box.value;
    let sent = true;
    if (text.trim() !== "" && typeof sendPaste === "function") sent = sendPaste(text) !== false;
    if (sent && withEnter && typeof sendKey === "function") sent = sendKey("enter") !== false;
    if (!sent) {
      note.textContent = "Not connected: not sent";
      note.hidden = false;
      return;
    }
    clear();
    close();
  }

  on(closeButton, "click", close);
  on(backdrop, "click", close);
  on(insert, "click", () => submit(false));
  on(send, "click", () => submit(true));
  on(box, "input", () => {
    note.hidden = true;
    save(box.value);
  });
  on(box, "keydown", (e) => {
    if (e.key === "Escape") {
      e.preventDefault();
      close();
    } else if (e.key === "Enter" && (e.ctrlKey || e.metaKey)) {
      e.preventDefault();
      submit(true);
    }
  });

  return {
    open,
    close,
    destroy() {
      save(box.value);
      ac.abort();
      root.remove();
    },
  };
}
