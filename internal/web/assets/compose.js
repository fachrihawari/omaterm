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
//
// With voice input on (Settings › Voice on the computer), a mic records a
// prompt and the computer writes it down into the box, to fix before Send:
// it is never sent by itself. The language chip beside it picks the
// language, kept under omaterm.voice.lang. Off, neither shows.

const STORE = "omaterm.compose";
const LANG_STORE = "omaterm.voice.lang";

const MIC = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="M12 19v3m7-12v2a7 7 0 0 1-14 0v-2"/><rect width="6" height="13" x="9" y="2" rx="3"/></svg>';
const STOP = '<svg viewBox="0 0 24 24" fill="currentColor" aria-hidden="true"><rect x="6" y="6" width="12" height="12" rx="2"/></svg>';

// voice.js loads the first time the box opens, as keys.js does.
let voiceModule = null;
const loadVoice = () => (voiceModule ??= import("./voice.js").catch(() => null));

function stored(key) {
  try {
    return localStorage.getItem(key) || "";
  } catch {
    return "";
  }
}

function store(key, value) {
  try {
    localStorage.setItem(key, value);
  } catch {
    // No storage: the choice lasts this time only.
  }
}

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

  const mic = document.createElement("button");
  mic.type = "button";
  mic.className = "compose-mic";
  mic.hidden = true;
  mic.innerHTML = MIC;
  mic.title = "Speak a prompt";
  mic.setAttribute("aria-label", "Speak a prompt");
  const micTime = document.createElement("span");
  micTime.className = "compose-mic-time";
  mic.append(micTime);
  // The chip shows the language's code; the select over it, unseen, opens
  // the phone's own picker with the names.
  const lang = document.createElement("label");
  lang.className = "compose-lang";
  lang.hidden = true;
  const langCode = document.createElement("span");
  const langPick = document.createElement("select");
  langPick.setAttribute("aria-label", "Language");
  lang.append(langCode, langPick);
  foot.append(mic, lang, note, insert, send);

  panel.append(head, box, foot);
  root.append(backdrop, panel);
  el.append(root);

  let lastFocus = null;
  let leaving = 0;

  // voice is what the computer said of voice input, and why the page can't
  // record ("" when it can); rec is the recording in progress.
  let voice = { enabled: false };
  let voiceMod = null;
  let cannot = "";
  let rec = null;
  let ticking = 0;
  let busy = false;
  let starting = false;

  function say(text) {
    note.textContent = text;
    note.hidden = !text;
  }

  // refreshVoice asks whether voice input is on, each time the box opens:
  // it is turned on and off on the computer.
  async function refreshVoice() {
    const mod = await loadVoice();
    if (!mod) return;
    voiceMod = mod;
    voice = await mod.voiceStatus();
    cannot = mod.canRecord();
    mic.hidden = lang.hidden = !voice.enabled;
    if (!voice.enabled) {
      stopRecording(false);
      return;
    }
    mic.classList.toggle("dim", !voice.ready || cannot !== "");
    const langs = voice.languages || [];
    let want = stored(LANG_STORE) || voice.language || "auto";
    if (!langs.some((l) => l.code === want)) want = voice.language || "auto";
    langPick.replaceChildren(...langs.map((l) => new Option(l.name, l.code, false, l.code === want)));
    langPick.value = want;
    langCode.textContent = langTag(want);
  }

  function showMic() {
    mic.classList.toggle("recording", rec !== null);
    mic.classList.toggle("busy", busy);
    mic.disabled = busy;
    mic.innerHTML = rec ? STOP : MIC;
    mic.append(micTime);
    micTime.textContent = "";
    const label = rec ? "Stop recording" : busy ? "Writing it down…" : "Speak a prompt";
    mic.title = label;
    mic.setAttribute("aria-label", label);
  }

  function tick() {
    if (!rec) return;
    const s = Math.floor(rec.seconds());
    micTime.textContent = `${Math.floor(s / 60)}:${String(s % 60).padStart(2, "0")}`;
  }

  // startRecording starts the microphone; called straight from the tap,
  // with no await before record, which iOS needs to let audio play.
  async function startRecording() {
    say("");
    starting = true;
    try {
      rec = await voiceMod.record({ onLimit: () => stopRecording(true) });
    } catch (err) {
      rec = null;
      say(err?.name === "NotAllowedError" ? "Microphone access was refused" : "The microphone didn't start");
      return;
    } finally {
      starting = false;
    }
    // Closed while the microphone was asked for.
    if (root.hidden || root.classList.contains("leaving")) {
      stopRecording(false);
      return;
    }
    showMic();
    ticking = setInterval(tick, 250);
  }

  // stopRecording ends the recording; keep sends it to be written down,
  // and the text joins what the box has.
  async function stopRecording(keep) {
    if (!rec) return;
    const r = rec;
    rec = null;
    clearInterval(ticking);
    if (!keep) {
      r.cancel();
      showMic();
      return;
    }
    busy = true;
    showMic();
    try {
      const text = (await voiceMod.transcribe(await r.stop(), langPick.value)).trim();
      if (text) {
        const v = box.value;
        box.value = v + (v && !/\s$/.test(v) ? " " : "") + text;
        save(box.value);
      } else {
        say("Nothing was heard");
      }
    } catch (err) {
      say(err?.message || "Not written down");
    } finally {
      busy = false;
      showMic();
    }
  }

  function open() {
    clearTimeout(leaving);
    root.classList.remove("leaving");
    note.hidden = true;
    refreshVoice();
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

  // close slides the sheet away, then hides it; with less motion asked
  // for, it hides at once. A recording in progress is dropped.
  function close() {
    stopRecording(false);
    if (matchMedia("(prefers-reduced-motion: reduce)").matches) {
      root.hidden = true;
    } else if (!root.hidden && !root.classList.contains("leaving")) {
      root.classList.add("leaving");
      leaving = setTimeout(() => {
        root.hidden = true;
        root.classList.remove("leaving");
      }, 160);
    }
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

  on(mic, "click", () => {
    if (busy || starting) return;
    if (rec) {
      stopRecording(true);
    } else if (!voice.ready || cannot || !voiceMod) {
      say(cannot || voice.reason || "Voice input isn't ready");
    } else {
      startRecording();
    }
  });
  on(langPick, "change", () => {
    store(LANG_STORE, langPick.value);
    langCode.textContent = langTag(langPick.value);
  });
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
      stopRecording(false);
      save(box.value);
      ac.abort();
      root.remove();
    },
  };
}

// langTag is a language setting as the chip shows it: ID, ID+EN (one of
// two, which whisper tells), or Auto.
function langTag(code) {
  return code === "auto" ? "Auto" : code.toUpperCase().replaceAll(",", "+");
}
