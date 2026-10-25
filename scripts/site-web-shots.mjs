// Renders the website's web access pictures: the browser page at desktop
// and phone sizes, showing sample sessions of an omaterm of its own.
//
//   node scripts/site-web-shots.mjs
//
// It builds omaterm, runs it with config, state and runtime folders of its
// own and web access on port 7398 (your omaterm is never touched), and
// drives headless Chromium (on the PATH as chromium, or $CHROMIUM) through
// the DevTools protocol. The pictures go to site/assets/web-*.png.

import { execFileSync, spawn } from "node:child_process";
import { mkdtempSync, mkdirSync, writeFileSync, rmSync, chmodSync, symlinkSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const repo = resolve(import.meta.dirname, "..");
const out = join(repo, "site", "assets");
const port = 7398;
const debugPort = 9337;
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

const tmp = mkdtempSync(join(tmpdir(), "omaterm-web-shots-"));
const env = {
  ...process.env,
  XDG_CONFIG_HOME: join(tmp, "config"),
  XDG_STATE_HOME: join(tmp, "state"),
  XDG_RUNTIME_DIR: join(tmp, "run"),
};
for (const k of ["OMATERM_PANE", "OMATERM_SESSION", "OMATERM_SOCKET"]) delete env[k];
mkdirSync(env.XDG_RUNTIME_DIR);
chmodSync(env.XDG_RUNTIME_DIR, 0o700);

const bin = join(tmp, "omaterm");
const omaterm = (...args) => execFileSync(bin, args, { env, encoding: "utf8" }).trim();

// What the sample sessions show. They use the 16 ANSI colors.
const screens = {
  agent: "\x1b[1;36m❯\x1b[0m add a light variant to the theme loader\n\n" +
    "\x1b[32m●\x1b[0m Read \x1b[1minternal/theme/omarchy.go\x1b[0m\n" +
    "\x1b[32m●\x1b[0m Read \x1b[1mthemewatch.go\x1b[0m\n" +
    "\x1b[32m●\x1b[0m Parse the light flag from colors.toml\n" +
    "  and flip the wash when it is set.\n\n" +
    "\x1b[33m╭─ Edit internal/theme/omarchy.go ───────\x1b[0m\n" +
    "\x1b[33m│\x1b[0m \x1b[2m41\x1b[0m  func Parse(path string) Source {\n" +
    "\x1b[33m│\x1b[0m \x1b[31m42 -    bg := c.Background\x1b[0m\n" +
    "\x1b[33m│\x1b[0m \x1b[32m42 +    bg, light := c.Background,\x1b[0m\n" +
    "\x1b[33m│\x1b[0m \x1b[32m43 +        c.Light\x1b[0m\n" +
    "\x1b[33m╰────────────────────────────────────────\x1b[0m\n\n" +
    " \x1b[1mAllow this edit?\x1b[0m\n" +
    " \x1b[1;36m❯ 1. Yes\x1b[0m\n" +
    "   2. Yes, for this session\n" +
    "   3. No, and tell it what to do instead\n",
  dev: "\x1b[2m$\x1b[0m bun run dev\n\n" +
    "  \x1b[1;32mVITE\x1b[0m \x1b[32mv6.2.0\x1b[0m  ready in \x1b[1m312\x1b[0m ms\n\n" +
    "  \x1b[32m➜\x1b[0m  \x1b[1mLocal\x1b[0m:   \x1b[36mhttp://localhost:\x1b[1m5173\x1b[0;36m/\x1b[0m\n\n" +
    "\x1b[2m12:04:31\x1b[0m \x1b[1;36m[vite]\x1b[0m \x1b[32mhmr update\x1b[0m \x1b[2m/src/App.tsx\x1b[0m\n" +
    "\x1b[2m12:04:58\x1b[0m \x1b[1;36m[vite]\x1b[0m \x1b[32mhmr update\x1b[0m \x1b[2m/src/theme.css\x1b[0m\n",
  done: "\x1b[1;36m❯\x1b[0m make the theme follow Omarchy theme switches\n\n" +
    "\x1b[32m●\x1b[0m Update \x1b[1minternal/theme/omarchy.go\x1b[0m  \x1b[32m+42\x1b[0m \x1b[31m-7\x1b[0m\n" +
    "\x1b[32m●\x1b[0m Update \x1b[1mthemewatch.go\x1b[0m  \x1b[32m+31\x1b[0m\n" +
    "\x1b[32m●\x1b[0m Bash   go test ./internal/theme/...\n" +
    "  \x1b[2m└\x1b[0m \x1b[32mok\x1b[0m  omaterm/internal/theme  0.214s\n\n" +
    "Done. omaterm now follows Omarchy theme switches.\n\n\x1b[1;36m❯\x1b[0m ",
  tests: "\x1b[1;34m~/Projects/api\x1b[0m \x1b[32m❯\x1b[0m go test ./...\n" +
    "\x1b[32mok\x1b[0m  \tapi/handlers\t0.412s\n" +
    "\x1b[32mok\x1b[0m  \tapi/store\t1.083s\n",
};

// session starts a sample session: it shows its screen, reports a status
// as an agent's hook would, then waits as program, which names the pane.
function session(dir, name, screen, program, report) {
  const file = join(tmp, name + ".txt");
  writeFileSync(file, screen);
  const prog = join(tmp, "bin", program);
  mkdirSync(join(tmp, "bin"), { recursive: true });
  try { symlinkSync("/bin/cat", prog); } catch {}
  const rep = report ? `${bin} report ${report}; ` : "";
  return omaterm("new", "-n", name, "-d", dir, "--", "sh", "-c", `cat '${file}'; ${rep}exec '${prog}' >/dev/null`);
}

let chrome = null;
try {
  execFileSync("go", ["build", "-o", bin, "."], { cwd: repo, stdio: "inherit" });
  omaterm("web", "on", "--port", String(port));

  const omadir = join(tmp, "Projects", "omaterm");
  const apidir = join(tmp, "Projects", "api");
  mkdirSync(omadir, { recursive: true });
  mkdirSync(apidir, { recursive: true });
  const dev = session(omadir, "theme loader", screens.agent, "claude", `blocked "Allow this edit?"`);
  session(omadir, "vite", screens.dev, "bun", "working");
  session(omadir, "omarchy themes", screens.done, "codex", `done "Follows Omarchy theme switches"`);
  session(apidir, "tests", screens.tests, "go", `done "Tests passed"`);
  const pane = JSON.parse(omaterm("ls", "--json")).find((s) => s.id === dev).focus;
  await sleep(1500);

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
  ws.onmessage = (e) => { const m = JSON.parse(e.data); waits[m.id]?.(m.result); delete waits[m.id]; };
  const cdp = (method, params = {}) => new Promise((r) => { waits[++id] = r; ws.send(JSON.stringify({ id, method, params })); });
  const size = async (width, height, scale, mobile) => {
    await cdp("Emulation.setDeviceMetricsOverride", { width, height, deviceScaleFactor: scale, mobile });
    await cdp("Emulation.setTouchEmulationEnabled", { enabled: mobile, maxTouchPoints: mobile ? 5 : 1 });
  };
  const go = async (url, wait = 2500) => { await cdp("Page.navigate", { url }); await sleep(wait); };
  const shot = async (name) => {
    const png = await cdp("Page.captureScreenshot", {});
    writeFileSync(join(out, name + ".png"), Buffer.from(png.data, "base64"));
    console.log("wrote site/assets/" + name + ".png");
  };

  await size(1440, 900, 2, false);
  await go(`${base}/#code=${code}`);
  await go(`${base}/#/pane/${pane}`);
  await shot("web-desktop");
  await size(390, 844, 3, true);
  await go(`${base}/#`);
  await cdp("Page.reload", {});
  await sleep(2500);
  await shot("web-phone-list");
  await go(`${base}/#/pane/${pane}`);
  await shot("web-phone");
} finally {
  chrome?.kill();
  try { omaterm("daemon", "stop"); } catch {}
  rmSync(tmp, { recursive: true, force: true });
}
