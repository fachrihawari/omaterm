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
// view is saved there as a PNG. A diff steps to the next changed file,
// wraps, and asks about a line in Compose. Output shows a session's text,
// its links and finds in it, and a process that crashed opens its last
// lines. With WEB_CHECK_THEME=<file>, a theme as the app sends it
// (web.setTheme; a light one, say), the page takes that look.
//
// So is the answer bar: a fake agent asks permission as Claude Code does,
// the bar offers its options, a tap types the digit, a tap on a screen
// that changed since is refused, and Stop sends Esc while it works.
//
// So are starting work from the phone: '+' opens the New Agent sheet,
// which starts a shell session and opens it; and the compose box's 📎
// sends a picture, whose path on the computer is pasted on its own.
//
// And what makes it a phone's app: the manifest, the service worker and
// the icons are served as their types, the worker runs, and the list's
// bell opens the notifications sheet, which can turn them on (a real
// subscription needs a push service, so none is made).

import { execFileSync, spawn } from "node:child_process";
import { createHash } from "node:crypto";
import { mkdtempSync, mkdirSync, writeFileSync, readFileSync, rmSync, chmodSync, existsSync } from "node:fs";
import { createServer } from "node:net";
import { deflateSync } from "node:zlib";
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
if (process.env.WEB_CHECK_THEME) writeFileSync(join(env.XDG_CONFIG_HOME, name, "web-theme.json"), readFileSync(process.env.WEB_CHECK_THEME));
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

// png is a w×h picture as a PNG, bands of color standing in for a
// screenshot.
const png = (w, h) => {
  const crcTable = Array.from({ length: 256 }, (_, n) => {
    for (let k = 0; k < 8; k++) n = n & 1 ? 0xedb88320 ^ (n >>> 1) : n >>> 1;
    return n >>> 0;
  });
  const crc = (b) => (b.reduce((c, x) => crcTable[(c ^ x) & 0xff] ^ (c >>> 8), 0xffffffff) ^ 0xffffffff) >>> 0;
  const chunk = (type, data) => {
    const body = Buffer.concat([Buffer.from(type, "latin1"), data]);
    const out = Buffer.alloc(body.length + 8);
    out.writeUInt32BE(data.length, 0);
    body.copy(out, 4);
    out.writeUInt32BE(crc(body), body.length + 4);
    return out;
  };
  const rows = Buffer.alloc((w * 3 + 1) * h);
  for (let y = 0; y < h; y++) {
    for (let x = 0; x < w; x++) {
      const at = y * (w * 3 + 1) + 1 + x * 3;
      const band = Math.floor(y / (h / 4));
      rows[at] = [47, 99, 233, 30][band] ?? 30;
      rows[at + 1] = [129, 213, 198, 30][band] ?? 30;
      rows[at + 2] = [247, 141, 109, 30][band] ?? 30;
      if (x > w * 0.1 && x < w * (0.3 + 0.15 * band) && y % (h / 4) > 10 && y % (h / 4) < 22) rows.fill(255, at, at + 3);
    }
  }
  const ihdr = Buffer.alloc(13);
  ihdr.writeUInt32BE(w, 0);
  ihdr.writeUInt32BE(h, 4);
  ihdr[8] = 8;
  ihdr[9] = 2;
  return Buffer.concat([Buffer.from([0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a]),
    chunk("IHDR", ihdr), chunk("IDAT", deflateSync(rows)), chunk("IEND", Buffer.alloc(0))]);
};
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
  // what follows what it had read before; want, when a function, tells it
  // once the action is done.
  let seen = "";
  const check = async (name, want, action) => {
    try {
      await action();
      if (typeof want === "function") want = await want();
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
  const diffOf = (path) => `decodeURIComponent(location.hash).endsWith("/changes/${path}") && document.querySelector(".bv-diff")`;
  await check("changes: ‹ › step to the other changed file and back", "", async () => {
    const [there, back] = await evaluate(`Boolean(document.querySelector('.bv-step[aria-label="Next file"]'))`) ? ["Next", "Previous"] : ["Previous", "Next"];
    await tap(`.bv-step[aria-label="${there} file"]`);
    await waitFor("notes.txt's diff did not show", `${diffOf("notes.txt")} && [...document.querySelectorAll(".dl.add .t")].some((e) => e.textContent === "new")`);
    if (await evaluate(`Boolean(document.querySelector('.bv-step[aria-label="${there} file"]'))`)) throw new Error(`the end of the list has a ${there}`);
    await tap(`.bv-step[aria-label="${back} file"]`);
    await waitFor("main.go's diff did not come back", diffOf("src/main.go"));
  });
  await check("changes: a diff wraps until its button turns that off, which is kept", "", async () => {
    const wraps = () => evaluate(`[document.querySelector(".bv-diff").classList.contains("wrap"), localStorage.getItem("omaterm.diff.wrap")]`);
    if (!(await wraps())[0]) throw new Error("the diff does not wrap");
    await tap(".bv-wrap");
    const [on, kept] = await wraps();
    if (on || kept !== "0") throw new Error(`after the tap, wrap ${on}, kept ${kept}`);
    await tap(".bv-wrap");
  });
  await check("changes: a tap on a line's number asks about it in Compose, sending nothing", "", async () => {
    await tap(".dl.add .n");
    const want = 'src/main.go:4 — println("hello from the phone")\n';
    await waitFor("Compose did not open with the line", `document.querySelector(".compose-box")?.value === ${JSON.stringify(want)} && !document.querySelector(".pane").dataset.view`);
    await sleep(400);
    await shot("diff-ask");
    await evaluate(`(() => { const b = document.querySelector(".compose-box"); b.value = ""; b.dispatchEvent(new Event("input")); })()`);
    await tap(".compose-close");
    await evaluate("history.back()");
    await waitFor("back did not go back to the diff", diffOf("src/main.go"));
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

  // Answering: an agent (goose, by its script's name, which omaterm reads
  // no screen of) asks permission as Claude Code draws it, and logs what
  // it reads as the probe does. "r" redraws the prompt for another
  // command, "1" answers it and starts work, Esc ends that.
  const asker = join(tmp, "goose.mjs");
  writeFileSync(asker, `
import { appendFileSync } from "node:fs";
const osc = (s) => process.stdout.write("\\x1b]7501;" + s + "\\x1b\\\\");
const ask = (dir) => {
  process.stdout.write("\\x1b[2J\\x1b[H⏺ I'll clean up.\\r\\n\\r\\n Bash command\\r\\n\\r\\n   rm -rf " + dir + "\\r\\n   Remove the " + dir +
    " folder\\r\\n\\r\\n Do you want to proceed?\\r\\n ❯ 1. Yes\\r\\n   2. Yes, and don't ask again for rm commands\\r\\n" +
    "   3. No, and tell Claude what to do differently (esc)\\r\\n");
  osc("state=blocked:kind=permission:msg=" + Buffer.from("Claude needs your permission to use Bash").toString("base64"));
};
process.stdin.setRawMode(true);
process.stdin.on("data", (b) => {
  appendFileSync(${JSON.stringify(logFile)}, b.toString("hex"));
  const s = b.toString();
  if (s === "r") ask("dist");
  if (s === "1") { process.stdout.write("\\x1b[2J\\x1b[H⏺ Removing build…\\r\\n"); osc("state=working"); }
  if (s === "\\x1b") { process.stdout.write("Interrupted\\r\\n"); osc("state=done"); }
});
ask("build");
setInterval(() => {}, 1 << 30);
`);
  // Beside it, one session that failed after it began to wait, and one
  // that finished: the inbox's.
  const failText = "The tests failed: 3 of 120 in internal/web, as the fixture's daemon did not start on a free port in time";
  const b64 = (s) => Buffer.from(s).toString("base64");
  const paneOf = (sid) => JSON.parse(omaterm("ls", "--json")).find((s) => s.id === sid).focus;
  const askPane = paneOf(omaterm("new", "-n", "asker", "-d", dir, "--", process.execPath, asker));
  await sleep(1200);
  omaterm("new", "-n", "failer", "-d", dir, "--", "sh", "-c", `printf '\\033]7501;state=error:msg=${b64(failText)}\\033\\\\'; exec cat`);
  const finPane = paneOf(omaterm("new", "-n", "finisher", "-d", dir, "--", "sh", "-c",
    `printf '\\033]7501;state=working\\033\\\\'; sleep 0.3; printf '\\033]7501;state=done:msg=${b64("Tests pass")}\\033\\\\'; exec cat`));
  const lead = (name) => `fetch("/api/state").then((r) => r.json()).then((st) => st.sessions.find((s) => s.name === ${JSON.stringify(name)})?.lead)`;

  await check("inbox: Needs you holds the waiting and failed sessions, longest waiting first", "", async () => {
    await evaluate(`location.hash = "#"`);
    await waitFor("no Needs you", `document.querySelectorAll(".needs .row").length === 2`);
    const names = await evaluate(`[...document.querySelectorAll(".needs .row .nm")].map((e) => e.textContent).join()`);
    if (names !== "asker,failer") throw new Error(`Needs you holds ${names}`);
  });
  await check("inbox: the done count is the finished session only", "", async () => {
    await waitFor("finisher is not done", `${lead("finisher")}.then((l) => l?.status === "idle" && l.unread)`);
    const chip = `document.querySelector('.side-f .chipc[title="Done sessions"]')?.textContent.trim()`;
    await waitFor("no done chip", `Boolean(${chip})`);
    const n = await evaluate(chip);
    if (n !== "1") throw new Error(`the done chip says ${n}`);
  });
  await check("inbox: a row's line, tapped, shows whole, and opens nothing", "", async () => {
    await tap(".needs .row .sub", failText);
    await waitFor("the line is still cut", `document.querySelector(".needs .row .sub.whole")?.textContent === ${JSON.stringify(failText)}`);
    if (await evaluate("location.hash")) throw new Error("the tap opened the session");
    await shot("inbox");
  });
  await check("inbox: the done chip opens the finished session, which it then sees", "", async () => {
    await tap('.side-f .chipc[title="Done sessions"]');
    await waitFor("it opened another", `location.hash === "#/pane/${finPane}"`);
    await waitFor("still unread", `${lead("finisher")}.then((l) => !l.unread)`);
    await evaluate(`location.hash = "#"`);
    await waitFor("the done chip stays", `!document.querySelector('.side-f .chipc[title="Done sessions"]')`);
  });

  const answerText = `[...document.querySelectorAll(".answer:not([hidden]) .answer-opt")].map((b) => b.textContent)`;
  await check("answer bar: the whole question and the screen's options", "", async () => {
    if (!(await evaluate(`${lead("asker")}.then((l) => l.unread)`))) throw new Error("asker is not unread");
    await tap(".needs .row .nm", "asker");
    await waitFor("no answer bar", `${answerText}.length === 3`);
    const q = await evaluate(`document.querySelector(".answer-q").textContent`);
    if (q !== "Claude needs your permission to use Bash") throw new Error(`the question is ${JSON.stringify(q)}`);
    const opts = await evaluate(`JSON.stringify(${answerText})`);
    if (opts !== JSON.stringify(["1Yes", "2Yes, and don't ask again for rm commands", "3No, and tell Claude what to do differently"])) {
      throw new Error(`the options are ${opts}`);
    }
    await sleep(600);
    await shot("answer");
  });
  await check("inbox: opening a session on the phone sees it", "", async () => {
    await waitFor("asker is still unread", `${lead("asker")}.then((l) => !l.unread)`);
  });
  await check("answer bar: a tap on a screen that changed since is refused", hex("r"), async () => {
    await key("r", "KeyR", "r");
    // Tapped as the new prompt shows, before the bar reads it again.
    const said = await evaluate(`(async () => {
      for (let i = 0; i < 300 && !document.querySelector(".xterm-rows").textContent.includes("rm -rf dist"); i++) {
        await new Promise((r) => setTimeout(r, 5));
      }
      document.querySelector(".answer-opt").click();
      return document.querySelector(".answer-note").textContent;
    })()`);
    if (!said.startsWith("The screen changed")) throw new Error(`the bar says ${JSON.stringify(said)}`);
  });
  await check("answer bar: a tap types the option's digit", hex("1"), async () => {
    await sleep(700);
    await tap(".answer-opt");
  });
  await check("answer bar: gone while the agent works, and Stop sends Esc", hex("\x1b"), async () => {
    await waitFor("no Stop", `!document.querySelector(".answer-stop").hidden && !document.querySelector(".answer:not([hidden])")`);
    await sleep(600);
    await shot("stop");
    await tap(".answer-stop");
  });
  await check("answer bar: none once the agent is done", "", async () => {
    await waitFor("the Stop stays", `document.querySelector(".answer-stop").hidden`);
  });
  await evaluate(`location.hash = "#/pane/${pane}"`);
  await sleep(1500);

  await check("a touch screen 860 px wide or more keeps the key bar and Compose, but no back", "", async () => {
    await cdp("Emulation.setDeviceMetricsOverride", { width: 1024, height: 768, deviceScaleFactor: 2, mobile: false });
    await sleep(600);
    const shown = (sel) => `getComputedStyle(document.querySelector(${JSON.stringify(sel)})).display !== "none"`;
    const got = await evaluate(`[${shown(".term-bottom")}, ${shown(".btn-compose")}, ${shown(".back-btn")}].join()`);
    await shot("tablet");
    await cdp("Emulation.setDeviceMetricsOverride", { width: 390, height: 844, deviceScaleFactor: 2, mobile: true });
    await sleep(600);
    if (got !== "true,true,false") throw new Error(`key bar, Compose, back: ${got}`);
  });

  // Output reads a session of a project of its own that printed lines,
  // and a process of it that crashed: its definition trusted as omaterm
  // trusts one, by the hash of what it runs (procs.Def.Variant).
  const procDir = join(tmp, "Projects", "procs");
  mkdirSync(procDir, { recursive: true });
  const crash = "echo listening on http://localhost:3999; echo Error: boom; exit 1";
  writeFileSync(join(procDir, "omaterm.yml"), `processes:\n  crash:\n    command: ${JSON.stringify(crash)}\n`);
  const variant = createHash("sha256").update(JSON.stringify({ command: crash, dir: "", env: null, port: 0, restart: "never", watch: null })).digest("hex");
  writeFileSync(join(env.XDG_CONFIG_HOME, name, "trust.json"), JSON.stringify({ version: 1, projects: { [procDir]: { [variant]: new Date().toISOString() } } }));
  const printed = "build started\nsee https://example.com/docs?a=1. for more, not file:///etc/passwd\n" +
    "local http://localhost:3000/ http://127.0.0.1:8080 http://[::1]:5173 https://shop.localhost\nerror: one\nerror: two\nerror: three\n";
  const outSid = omaterm("new", "-n", "build-logs", "-d", procDir, "--", "sh", "-c", `printf '${printed}'; exec sleep 600`);
  const outPane = JSON.parse(omaterm("ls", "--json")).find((s) => s.id === outSid).focus;
  const marked = `[...document.querySelectorAll(".bv-out mark.on")].map((m) => m.textContent).join("")`;

  await check("output: a session's text, its web links, and nothing else linked, the loopback ones neither", "", async () => {
    // Its terminal opened first starts it, as the list's row does.
    await evaluate(`location.hash = "#/pane/${outPane}"`);
    await waitFor("its terminal did not open", `document.querySelector(".xterm-rows")?.textContent.includes("error: three")`);
    await evaluate(`location.hash = "#/pane/${outPane}/output"`);
    await waitFor("the output did not show", `document.querySelector(".bv-out")?.textContent.includes("error: three")`);
    const links = await evaluate(`JSON.stringify([...document.querySelectorAll(".bv-out a")].map((a) => [a.getAttribute("href"), a.textContent, a.target, a.rel]))`);
    if (links !== JSON.stringify([["https://example.com/docs?a=1", "https://example.com/docs?a=1", "_blank", "noopener noreferrer"]])) throw new Error(`the links are ${links}`);
    await waitFor("the session's name did not show", `document.querySelector(".grip .t").textContent === "build-logs"`);
    await shot("output");
  });
  await check("output: the switch leaves a name of ten letters whole, at 360 px too", "", async () => {
    for (const width of [360, 390]) {
      await cdp("Emulation.setDeviceMetricsOverride", { width, height: 844, deviceScaleFactor: 2, mobile: true });
      await sleep(300);
      if (width === 360) await shot("output-360");
      if (await evaluate(`(() => { const t = document.querySelector(".grip .t"); return t.scrollWidth > t.clientWidth; })()`)) {
        throw new Error(`at ${width} px the name is cut`);
      }
    }
  });
  await check("output: Find counts the hits, its case ignored, and jumps between them", "", async () => {
    await tap('.bv-btn[aria-label="Find"]');
    await evaluate(`document.querySelector(".bv-find").focus()`);
    await cdp("Input.insertText", { text: "ERROR" });
    const at = (n) => waitFor(`not at hit ${n}`, `document.querySelector(".bv-count").textContent === "${n}/3" && document.querySelector('.bv-out mark.on[data-hit="${n - 1}"]') && ${marked} === "error"`);
    await at(1);
    await tap('.bv-btn[aria-label="Next"]');
    await at(2);
    await shot("output-find");
    await tap('.bv-btn[aria-label="Previous"]');
    await at(1);
    await tap('.bv-btn[aria-label="Previous"]');
    await at(3);
    await tap(".bv-done");
    await waitFor("the hits stayed marked", `!document.querySelector(".bv-out mark") && document.querySelector(".bv-findbar").hidden`);
  });
  await check("output: a process that crashed opens its last lines, and starts nothing", "", async () => {
    const inProcs = (...args) => execFileSync(bin, args, { env, cwd: procDir, encoding: "utf8" }).trim();
    for (let i = 0; ; i++) {
      try { inProcs("start", "crash"); break; } catch (err) { if (i > 50) throw err; await sleep(200); }
    }
    let crashPane = "";
    for (let i = 0; i < 50 && !crashPane; i++) {
      crashPane = await evaluate(`fetch("/api/state").then((r) => r.json()).then((st) => {
        const s = st.sessions.find((s) => s.process === "crash");
        return s?.proc?.status === "failed" ? s.panes[0].id : "";
      })`);
      if (!crashPane) await sleep(200);
    }
    if (!crashPane) throw new Error("the process did not crash: " + inProcs("ps"));
    await evaluate(`location.hash = "#/pane/${crashPane}"`);
    await waitFor("its output did not open", `location.hash.endsWith("/output") && document.querySelector(".bv-out")?.textContent.includes("Error: boom")`);
    const sum = await evaluate(`document.querySelector(".bv-sum").textContent`);
    if (!sum.startsWith("crash isn't running")) throw new Error(`the bar says ${sum}`);
    await shot("output-stopped");
    if (!/crash\s+failed/.test(inProcs("ps"))) throw new Error("reading it started it: " + inProcs("ps"));
    const read = inProcs("read", "crash");
    if (!read.includes("Error: boom")) throw new Error(`omaterm read crash says ${JSON.stringify(read)}`);
  });
  await check("back to the first session's terminal", "", async () => {
    await evaluate(`location.hash = "#/pane/${pane}"`);
    await waitFor("its terminal did not come back", `!document.querySelector(".pane").dataset.view && !document.querySelector(".btn-compose").hidden`);
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

  // New Agent: '+' opens the sheet, which starts a shell in the project
  // and opens it. No agent is started: a real one would run.
  await check("new agent: the sheet starts a shell session, and opens it", "", async () => {
    const before = JSON.parse(omaterm("ls", "--json")).length;
    await evaluate(`location.hash = "#/"`);
    await waitFor("the list did not show", `Boolean(document.querySelector(".proj .add"))`);
    await tap(".proj .add");
    await waitFor("the sheet's agents did not load", `document.querySelectorAll(".na-chip").length === 6`);
    await sleep(400);
    await shot("new-agent");
    await tap(".na-chip", "Shell only");
    await waitFor("a shell still asks for a first task", `document.querySelector("#na-task").closest(".na-field").hidden`);
    await tap(".na-start");
    await waitFor("no new session opened", `/^#\\/pane\\//.test(location.hash) && !location.hash.endsWith(${JSON.stringify(pane)})`);
    const n = JSON.parse(omaterm("ls", "--json")).length;
    if (n !== before + 1) throw new Error(`${n} sessions, after ${before}`);
  });
  await check("new agent: an agent in a worktree of its own, with a first task, as the sheet shows it", "", async () => {
    await evaluate(`location.hash = "#/"`);
    await waitFor("the list did not show", `Boolean(document.querySelector(".proj .add"))`);
    await tap(".proj .add");
    await waitFor("the sheet's agents did not load", `document.querySelectorAll(".na-chip").length === 6`);
    await tap(".na-chip", "Claude Code");
    await tap(".na-switch");
    await waitFor("no branch to name", `!document.querySelector("#na-branch").closest(".na-sub").hidden`);
    await evaluate(`document.querySelector("#na-branch").focus()`);
    await cdp("Input.insertText", { text: "fix-login" });
    await evaluate(`document.querySelector("#na-task").focus()`);
    await cdp("Input.insertText", { text: "The login button does nothing on a phone: find out why and fix it." });
    const hint = await evaluate(`document.querySelector(".na-sub .na-hint").textContent`);
    if (hint !== "A new branch from main, in check/.worktrees/fix-login.") throw new Error(`the hint says ${hint}`);
    await evaluate(`document.activeElement.blur()`);
    await sleep(300);
    await shot("new-agent-worktree");
    await tap(".na-cancel");
    await waitFor("the sheet did not close", `!document.querySelector(".na")`);
  });

  // A photo: the compose box's 📎 sends a picture; the path the computer
  // keeps it at is pasted, on its own, without Enter; Send then presses
  // Enter, and the thumbnail goes.
  const picture = join(tmp, "screenshot.png");
  writeFileSync(picture, png(1170, 760));
  await check("photo: the path it is kept at is pasted, without Enter", async () =>
    hex(await evaluate(`document.querySelector(".compose-shots img")?.title || ""`)), async () => {
    await evaluate(`location.hash = ${JSON.stringify("#/pane/" + pane)}`);
    await waitFor("the terminal did not open", `Boolean(document.querySelector(".btn-compose:not([hidden])"))`);
    await sleep(500);
    await tap(".btn-compose");
    await waitFor("no 📎 in the compose box", `Boolean(document.querySelector(".compose-attach input"))`);
    const { result } = await cdp("Runtime.evaluate", { expression: `document.querySelector(".compose-attach input")` });
    await cdp("DOM.setFileInputFiles", { files: [picture], objectId: result.objectId });
    await waitFor("no thumbnail: " + await evaluate(`document.querySelector(".compose-note").textContent`),
      `Boolean(document.querySelector(".compose-shots img"))`);
    const path = await evaluate(`document.querySelector(".compose-shots img").title`);
    if (!/[\\/]uploads[\\/]phone-[0-9a-f]{16}\.jpg$/.test(path)) throw new Error(`kept at ${path}`);
    await evaluate(`document.activeElement.blur()`);
    await sleep(300);
    await shot("photo");
  });
  await check("photo: Send presses Enter, and the thumbnail goes", hex("\r"), async () => {
    await tap(".compose-send");
    await waitFor("the thumbnail stays", `!document.querySelector(".compose-shots img")`);
  });

  await check("notifications: the manifest, worker and icons are served as their types", "", async () => {
    for (const [path, type] of [["/manifest.json", "application/json"], ["/sw.js", "text/javascript"],
      ["/icon-180.png", "image/png"], ["/icon-192.png", "image/png"], ["/icon-512.png", "image/png"]]) {
      const res = await fetch(base + path);
      const got = res.headers.get("content-type") || "";
      if (!res.ok || !got.startsWith(type)) throw new Error(`${path} is ${res.status} ${got}`);
    }
    const linked = await evaluate(`[...document.querySelectorAll('link[rel="manifest"], link[rel="apple-touch-icon"]')].length`);
    if (linked !== 2) throw new Error(`the page links ${linked} of the manifest and the touch icon`);
  });
  await check("notifications: the worker runs, and the list's bell opens its sheet", "", async () => {
    await waitFor("the service worker isn't running", `navigator.serviceWorker.getRegistration().then((r) => Boolean(r?.active))`);
    await evaluate(`location.hash = ""`);
    await waitFor("no bell in the list's footer", `Boolean(document.querySelector(".side-f .bell")?.getClientRects().length)`);
    await shot("push-list");
    await tap(".side-f .bell");
    await waitFor("the bell opened no sheet", `Boolean(document.querySelector(".push-sheet .push-on"))`);
    const st = await evaluate(`[".push-on", ".push-words", ".push-programs"].map((s) => document.querySelector(s))
      .map((e) => e.disabled + "/" + e.checked).concat(document.querySelector(".push-note").hidden)`);
    if (st.join() !== "false/false,true/false,true/false,true") {
      throw new Error(`notify, words and programs disabled/checked, then the note hidden: ${st}`);
    }
    await sleep(300);
    await shot("push-sheet");
    await tap(".push-close");
    await waitFor("the sheet stayed", `!document.querySelector(".push-sheet")`);
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
