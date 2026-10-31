// The key bar and compose modules on their own, logging what they would
// send. Open keys-demo.html in a browser.

import { mountKeyBar } from "./keys.js";
import { mountCompose } from "./compose.js";

const log = document.getElementById("log");
const mods = document.getElementById("mods");
const appCursor = document.getElementById("appcursor");

function add(text, cls) {
  const line = document.createElement("div");
  if (cls) line.className = cls;
  line.textContent = text;
  log.append(line);
  log.scrollTop = log.scrollHeight;
}

const sendKey = (name) => add("key    " + name);
const sendText = (text) => add("paste  " + JSON.stringify(text), "paste");

mountKeyBar(document.getElementById("bar"), {
  sendKey,
  sendText,
  appCursor: () => appCursor.checked,
  onModifiers: (m) => {
    const on = [];
    if (m.ctrl) on.push(m.ctrlLocked ? "Ctrl locked" : "Ctrl");
    if (m.alt) on.push(m.altLocked ? "Alt locked" : "Alt");
    mods.textContent = on.length ? on.join(" · ") : "";
  },
});

const compose = mountCompose(document.getElementById("compose"), { sendPaste: sendText, sendKey });
document.getElementById("compose-open").addEventListener("click", compose.open);
