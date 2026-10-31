// Checks, in a real browser, that the web page's keys reach the program.
//
//   node scripts/web-check.mjs
//
// It builds omaterm, runs it with config, state and runtime folders of its
// own and web access on a free port (your omaterm is never touched), starts
// a session whose program logs the raw bytes it reads, pairs headless
// Chromium (on the PATH as chromium, or $CHROMIUM) at phone size with touch
// on, taps the key bar's keys and the compose box's Send, and compares the
// bytes the program read with what each key should send. It prints one line
// per check and exits 1, saying which bytes arrived, when one is wrong.

import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, chmodSync, existsSync } from "node:fs";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const repo = resolve(import.meta.dirname, "..");
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const freePort = () => new Promise((res, rej) => {
  const s = createServer();
  s.on("error", rej);
  s.listen(0, "127.0.0.1", () => { const { port } = s.address(); s.close(() => res(port)); });
});

const tmp = mkdtempSync(join(tmpdir(), "omaterm-web-check-"));
const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(tmp, "config"),
  // macOS ignores XDG_CONFIG_HOME; this keeps its real config out of reach.
  OMATERM_CONFIG_HOME: join(tmp, "config"),
  XDG_STATE_HOME: join(tmp, "state"),
  XDG_RUNTIME_DIR: join(tmp, "run"),
};
for (const k of ["OMATERM_PANE", "OMATERM_SESSION", "OMATERM_SOCKET", "OMATERM_DAEMON_SOCKET"]) delete env[k];
mkdirSync(env.XDG_RUNTIME_DIR);
chmodSync(env.XDG_RUNTIME_DIR, 0o700);

const bin = join(tmp, "omaterm");
const omaterm = (...args) => execFileSync(bin, args, { env, encoding: "utf8" }).trim();

// The program under test puts the terminal in raw mode, as an editor does,
// and appends every chunk it reads to a file as hex.
const logFile = join(tmp, "bytes.hex");
const probe = join(tmp, "probe.mjs");
writeFileSync(probe, `
import { appendFileSync } from "node:fs";
process.stdin.setRawMode(true);
process.stdin.on("data", (b) => appendFileSync(${JSON.stringify(logFile)}, b.toString("hex")));
appendFileSync(${JSON.stringify(logFile)}, "");
setInterval(() => {}, 1 << 30);
`);

const hex = (s) => Buffer.from(s, "latin1").toString("hex");
const got = () => (existsSync(logFile) ? readFileSync(logFile, "utf8") : "");
const show = (h) => (h ? JSON.stringify(Buffer.from(h, "hex").toString("latin1")) : "nothing");

let chrome = null;
let failed = 0;
try {
  execFileSync("go", ["build", "-o", bin, "."], { cwd: repo, stdio: "inherit" });
  const port = await freePort();
  const debugPort = await freePort();
  omaterm("web", "on", "--port", String(port));

  const dir = join(tmp, "Projects", "check");
  mkdirSync(dir, { recursive: true });
  const sid = omaterm("new", "-n", "probe", "-d", dir, "--", process.execPath, probe);
  const pane = JSON.parse(omaterm("ls", "--json")).find((s) => s.id === sid).focus;
  await sleep(1000);

  const code = omaterm("web", "pair").match(/code=([A-Z0-9-]+)/)[1];
  const base = `http://127.0.0.1:${port}`;

  chrome = spawn(process.env.CHROMIUM || "chromium", ["--headless=new", `--remote-debugging-port=${debugPort}`,
    `--user-data-dir=${join(tmp, "chrome")}`, "about:blank"], { stdio: "ignore" });
  let targets;
  for (let i = 0; i < 50 && !targets; i++) {
    try { targets = await (await fetch(`http://127.0.0.1:${debugPort}/json`)).json(); } catch { await sleep(200); }
  }
  const ws = new WebSocket(targets.find((t) => t.type === "page").webSocketDebuggerUrl);
  await new Promise((r) => (ws.onopen = r));
  let id = 0;
  const waits = {};
  ws.onmessage = (e) => { const m = JSON.parse(e.data); waits[m.id]?.(m); delete waits[m.id]; };
  const cdp = (method, params = {}) => new Promise((r) => { waits[++id] = r; ws.send(JSON.stringify({ id, method, params })); }).then((m) => m.result);
  const evaluate = async (expression) => (await cdp("Runtime.evaluate", { expression, returnByValue: true, awaitPromise: true })).result.value;

  await cdp("Emulation.setDeviceMetricsOverride", { width: 390, height: 844, deviceScaleFactor: 2, mobile: true });
  await cdp("Emulation.setTouchEmulationEnabled", { enabled: true, maxTouchPoints: 5 });
  await cdp("Page.navigate", { url: `${base}/#code=${code}` });
  await sleep(2000);
  await cdp("Page.navigate", { url: `${base}/#/pane/${pane}` });
  await sleep(2500);

  // tap touches the middle of the first element matching a selector whose
  // text is text (any, when it is empty), scrolling it into view.
  const tap = async (selector, text = "") => {
    const at = await evaluate(`(() => {
      const el = [...document.querySelectorAll(${JSON.stringify(selector)})]
        .find((e) => !${JSON.stringify(text)} || e.textContent.trim() === ${JSON.stringify(text)});
      if (!el) return null;
      el.scrollIntoView({ inline: "center", block: "nearest" });
      const r = el.getBoundingClientRect();
      return { x: r.x + r.width / 2, y: r.y + r.height / 2 };
    })()`);
    if (!at) throw new Error(`nothing to tap: ${selector} ${text}`);
    await cdp("Input.dispatchTouchEvent", { type: "touchStart", touchPoints: [at] });
    await cdp("Input.dispatchTouchEvent", { type: "touchEnd", touchPoints: [] });
  };
  // key types a character as a hardware keyboard would, in the terminal.
  const key = async (k, code, text) => {
    await evaluate(`document.querySelector(".xterm-helper-textarea").focus()`);
    await cdp("Input.dispatchKeyEvent", { type: "keyDown", key: k, code, text, windowsVirtualKeyCode: k.toUpperCase().charCodeAt(0) });
    await cdp("Input.dispatchKeyEvent", { type: "keyUp", key: k, code });
  };

  // check runs an action and waits for the program to have read exactly
  // what follows what it had read before.
  let seen = "";
  const check = async (name, want, action) => {
    try {
      await action();
    } catch (err) {
      failed++;
      console.log(`FAIL ${name}: ${err.message}`);
      return;
    }
    let now = "";
    for (let i = 0; i < 30; i++) {
      now = got().slice(seen.length);
      if (now.length >= want.length) break;
      await sleep(100);
    }
    await sleep(150);
    now = got().slice(seen.length);
    seen = got();
    if (now === want) console.log(`ok   ${name}: ${show(now)}`);
    else { failed++; console.log(`FAIL ${name}: want ${show(want)}, got ${show(now)}`); }
  };

  const tapKey = (label) => tap(".keybar-key", label);

  await check("Esc", hex("\x1b"), () => tapKey("Esc"));
  await check("Shift+Tab", hex("\x1b[Z"), () => tapKey("⇧Tab"));
  await check("Ctrl+C", hex("\x03"), () => tapKey("⌃C"));
  await check("Ctrl+Enter", hex("\n"), () => tapKey("⌃⏎"));
  await check("sticky Ctrl, then L typed", hex("\x0c"), async () => {
    await tapKey("Ctrl");
    await key("l", "KeyL", "l");
  });
  await check("typed l after Ctrl is released", hex("l"), () => key("l", "KeyL", "l"));
  await check("sticky Alt, then x typed", hex("\x1bx"), async () => {
    await tapKey("Alt");
    await key("x", "KeyX", "x");
  });

  await check("compose Send pastes the text and presses Enter", hex("hello from compose\r"), async () => {
    await tap(".btn-compose");
    await sleep(400);
    await evaluate(`document.querySelector(".compose-box").focus()`);
    await cdp("Input.insertText", { text: "hello from compose" });
    await tap(".compose-send");
  });
  await check("compose Insert pastes without Enter", hex("just text"), async () => {
    await tap(".btn-compose");
    await sleep(400);
    await evaluate(`document.querySelector(".compose-box").focus()`);
    await cdp("Input.insertText", { text: "just text" });
    await tap(".compose-insert");
  });
} catch (err) {
  failed++;
  console.log("FAIL " + (err?.stack || err));
} finally {
  chrome?.kill();
  try { omaterm("daemon", "stop"); } catch {}
  rmSync(tmp, { recursive: true, force: true });
}

if (failed) {
  console.log(`${failed} check${failed > 1 ? "s" : ""} failed`);
  process.exit(1);
}
console.log("all checks passed");
