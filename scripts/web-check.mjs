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
//
// Voice input is checked too, with Chromium's fake microphone and a fake
// whisper-server on the PATH: the mic records, the text lands in the
// compose box and nothing is sent; Send while recording waits for the
// words and sends them; turned off, the mic is gone.
//
// So are the session's Files and Changes views: the project is a git
// repository with a change, which the page lists and shows, and the
// terminal still takes keys after them. With WEB_CHECK_SHOTS=<dir>, each
// view is saved there as a PNG.

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
for (const k of ["OMATERM_PANE", "OMATERM_SESSION", "OMATERM_SOCKET"]) delete env[k];
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

// Voice input is on, with a model and a whisper-server that answers with
// the language it was asked for and the WAV's sample rate.
const name = env.OMATERM_DEV === "1" ? "omaterm-dev" : "omaterm";
const settingsFile = join(env.XDG_CONFIG_HOME, name, "settings.json");
const setVoice = (enabled) => {
  mkdirSync(join(env.XDG_CONFIG_HOME, name), { recursive: true });
  writeFileSync(settingsFile, JSON.stringify({ voice: { enabled, model: "tiny", language: "id" } }));
};
setVoice(true);
mkdirSync(join(env.XDG_STATE_HOME, name, "voice"), { recursive: true });
writeFileSync(join(env.XDG_STATE_HOME, name, "voice", "ggml-tiny.bin"), "not a model");
const fakeBin = join(tmp, "fakebin");
mkdirSync(fakeBin);
writeFileSync(join(fakeBin, "whisper-server"), `#!${process.execPath}
const { createServer } = require("node:http");
const port = process.argv[process.argv.indexOf("--port") + 1];
createServer((req, res) => {
  const chunks = [];
  req.on("data", (c) => chunks.push(c));
  req.on("end", () => {
    const body = Buffer.concat(chunks);
    const at = body.indexOf("RIFF");
    const rate = at < 0 ? 0 : body.readUInt32LE(at + 24);
    const samples = at < 0 ? 0 : body.readUInt32LE(at + 40) / 2;
    const lang = /name="language"\\r\\n\\r\\n([^\\r]*)/.exec(body.toString("latin1"))?.[1];
    res.setHeader("Content-Type", "application/json");
    res.end(JSON.stringify({ text: \` hello from the phone (\${lang}, \${rate} Hz, \${samples > 0 ? "sound" : "silence"})\\n\` }));
  });
}).listen(port, "127.0.0.1");
`, { mode: 0o755 });
env.PATH = `${fakeBin}:${env.PATH}`;

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
  mkdirSync(join(dir, "src"), { recursive: true });
  const git = (...args) => execFileSync("git", ["-c", "user.name=t", "-c", "user.email=t@t", "-c", "init.defaultBranch=main",
    "-c", "commit.gpgsign=false", ...args], { cwd: dir, stdio: "ignore" });
  writeFileSync(join(dir, "README.md"), "# check\n\nA project for the web check.\n");
  writeFileSync(join(dir, "src", "main.go"), "package main\n\nfunc main() {\n\tprintln(\"hello\")\n}\n");
  writeFileSync(join(dir, "logo.svg"), '<svg xmlns="http://www.w3.org/2000/svg" width="64" height="64"><circle cx="32" cy="32" r="28" fill="#2f81f7"/></svg>');
  git("init", "-q");
  git("add", "-A");
  git("commit", "-q", "-m", "first");
  writeFileSync(join(dir, "src", "main.go"), "package main\n\nfunc main() {\n\tprintln(\"hello from the phone\")\n}\n");
  writeFileSync(join(dir, "notes.txt"), "new\n");
  const sid = omaterm("new", "-n", "probe", "-d", dir, "--", process.execPath, probe);
  const pane = JSON.parse(omaterm("ls", "--json")).find((s) => s.id === sid).focus;
  await sleep(1000);

  const code = omaterm("web", "pair").match(/code=([A-Z0-9-]+)/)[1];
  const base = `http://127.0.0.1:${port}`;

  chrome = spawn(process.env.CHROMIUM || "chromium", ["--headless=new", `--remote-debugging-port=${debugPort}`,
    `--user-data-dir=${join(tmp, "chrome")}`, "--use-fake-ui-for-media-stream", "--use-fake-device-for-media-stream",
    "--autoplay-policy=no-user-gesture-required", "about:blank"], { stdio: "ignore" });
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
    if (!at) throw new Error(`nothing to tap: ${selector} ${text} (at ${await evaluate("location.hash")})`);
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

  // waitBox waits for the compose box to hold text matching want.
  const waitBox = async (want) => {
    let v = "";
    for (let i = 0; i < 80; i++) {
      v = await evaluate(`document.querySelector(".compose-box").value`);
      if (want.test(v)) return v;
      await sleep(100);
    }
    throw new Error(`the compose box has ${JSON.stringify(v)}`);
  };
  // speak records about a second and a half with the mic.
  const speak = async () => {
    if (await evaluate(`document.querySelector(".compose-mic").hidden`)) throw new Error("no mic in the compose box");
    await tap(".compose-mic");
    await sleep(1500);
    if (!(await evaluate(`document.querySelector(".compose-mic").classList.contains("recording")`))) {
      throw new Error("not recording: " + (await evaluate(`document.querySelector(".compose-note").textContent`)));
    }
    await tap(".compose-mic");
  };

  // waitFor waits for an expression in the page to be true.
  const waitFor = async (what, expression) => {
    for (let i = 0; i < 50; i++) {
      if (await evaluate(expression)) return;
      await sleep(100);
    }
    throw new Error(what);
  };
  const shotDir = process.env.WEB_CHECK_SHOTS;
  const shot = async (name) => {
    if (!shotDir) return;
    mkdirSync(shotDir, { recursive: true });
    const { data } = await cdp("Page.captureScreenshot", { format: "png" });
    writeFileSync(join(shotDir, `web-${name}.png`), Buffer.from(data, "base64"));
  };
  const viewText = `(document.querySelector(".pane-view")?.textContent || "")`;

  await check("files: the session's folder lists, folders first", "", async () => {
    await tap(".btn-browse");
    await waitFor("no files listed", `${viewText}.includes("README.md")`);
    const first = await evaluate(`document.querySelector(".pane-view .bv-row .nm").textContent`);
    if (first !== "src") throw new Error(`the first entry is ${first}, not src`);
    await shot("files");
  });
  await check("files: a folder opens, then a file shows its text", "", async () => {
    await tap(".pane-view .bv-row", "src");
    await waitFor("src did not open", `${viewText}.includes("main.go")`);
    await tap(".pane-view .bv-row .nm", "main.go");
    await waitFor("main.go's text did not show", `document.querySelector(".bv-code .txt")?.textContent.includes("hello from the phone")`);
    await shot("file");
  });
  await check("changes: the changed and new files, with their counts", "", async () => {
    await tap(".bv-seg a", "Changes");
    await waitFor("no changes listed", `${viewText}.includes("main.go") && ${viewText}.includes("notes.txt")`);
    const sum = await evaluate(`document.querySelector(".bv-sum").textContent`);
    if (!/^2 files · \+2 −1 · not committed$/.test(sum)) throw new Error(`the summary says ${sum}`);
    await shot("changes");
  });
  await check("changes: a file's diff", "", async () => {
    await tap(".pane-view .bv-row .nm", "main.go");
    await waitFor("the diff did not show", `[...document.querySelectorAll(".dl.add .t")].some((e) => e.textContent.includes("hello from the phone"))`);
    await shot("diff");
  });
  await check("back to the terminal, keys still reach it", hex("z"), async () => {
    await tap(".btn-close");
    await waitFor("the terminal did not come back", `!document.querySelector(".pane").dataset.view`);
    await key("z", "KeyZ", "z");
  });
  await check("the button opens the view last seen", "", async () => {
    const to = await evaluate(`document.querySelector(".btn-browse").getAttribute("href")`);
    if (!to.endsWith("/changes")) throw new Error(`it opens ${to}`);
  });

  await check("voice: the mic writes into the box in the default language, sending nothing", "", async () => {
    await tap(".btn-compose");
    await sleep(800);
    const chip = await evaluate(`document.querySelector(".compose-lang span").textContent`);
    if (chip !== "ID") throw new Error(`the language chip says ${chip}, not ID`);
    await speak();
    await waitBox(/^hello from the phone \(id, 16000 Hz, sound\)$/);
  });
  await check("voice: the chip's language, added after what the box had", "", async () => {
    await evaluate(`(() => { const s = document.querySelector(".compose-lang select"); s.value = "en"; s.dispatchEvent(new Event("change")); })()`);
    await speak();
    await waitBox(/^hello from the phone \(id, 16000 Hz, sound\) hello from the phone \(en, 16000 Hz, sound\)$/);
  });
  await check("voice: Send then sends what was written down", hex("hello from the phone (id, 16000 Hz, sound) hello from the phone (en, 16000 Hz, sound)\r"), () => tap(".compose-send"));
  await check("voice: Send while recording sends what was said, once written down", hex("hello from the phone (en, 16000 Hz, sound)\r"), async () => {
    await tap(".btn-compose");
    await sleep(800);
    await tap(".compose-mic");
    await sleep(1500);
    await tap(".compose-send");
    await sleep(1500);
  });
  await check("voice: off on the computer, the mic and chip are gone", "", async () => {
    setVoice(false);
    await tap(".btn-compose");
    await sleep(800);
    const shown = await evaluate(`!document.querySelector(".compose-mic").hidden || !document.querySelector(".compose-lang").hidden`);
    if (shown) throw new Error("the mic still shows");
    await tap(".compose-close");
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
