// Tests the web page's reading of a waiting agent's prompt (prompt.js),
// which the answer bar turns into buttons, on screens as Claude Code,
// Codex and OpenCode draw them.
//
//   node --test scripts/web-prompt-test.mjs

import { test } from "node:test";
import assert from "node:assert/strict";
import { parsePrompt, screenLines } from "../internal/web/assets/prompt.js";

// buffer is an xterm buffer of rows, 52 columns wide, the screen being
// the last rows of them; a row given as [text] is one the terminal
// wrapped onto, the row before it full.
function buffer(lines, rows = lines.length) {
  const all = lines.map((l) => (Array.isArray(l) ? { text: l[0], wrapped: true } : { text: l, wrapped: false }));
  while (all.length < rows) all.push({ text: "", wrapped: false });
  return {
    rows,
    buf: {
      baseY: all.length - rows,
      getLine: (y) => all[y] && {
        isWrapped: all[y].wrapped,
        translateToString: (trim) => (trim ? all[y].text.trimEnd() : all[y].text.padEnd(52)),
      },
    },
  };
}

const read = (lines, rows) => {
  const { buf, rows: r } = buffer(lines, rows);
  return parsePrompt(screenLines(buf, r));
};
const opts = (p) => p.options.map((o) => `${o.selected ? ">" : " "}${o.n} ${o.label}`);

const claude = [
  "⏺ I'll remove the build folder.",
  "",
  "╭────────────────────────────────────────────────╮",
  "│ Bash command                                   │",
  "│                                                │",
  "│   rm -rf build                                 │",
  "│   Remove the build folder                      │",
  "│                                                │",
  "│ Do you want to proceed?                        │",
  "│ ❯ 1. Yes                                       │",
  "│   2. Yes, and don't ask again for rm commands  │",
  "│      in /home/me/shop                          │",
  "│   3. No, and tell Claude what to do            │",
  "│      differently (esc)                         │",
  "╰────────────────────────────────────────────────╯",
];

test("Claude Code's permission prompt, in its box, wrapped at a phone's width", () => {
  const p = read(claude, 40);
  assert.equal(p.kind, "numbered");
  assert.deepEqual(opts(p), [">1 Yes", " 2 Yes, and don't ask again for rm commands", " 3 No, and tell Claude what to do"]);
  assert.match(p.key, /rm -rf build/);
  assert.match(p.key, /Do you want to proceed\?/);
});

test("Claude Code's prompt without a box, with its hint under it", () => {
  const p = read([
    "────────────────────────────────────────────────────",
    " Bash command",
    "   git push",
    " Do you want to proceed?",
    " ❯ 1. Yes",
    "   2. Yes, and don't ask again for git push commands",
    "   3. No, and tell Claude what to do differently (esc)",
    "",
    " Esc to cancel · Tab to add additional instructions",
  ]);
  assert.deepEqual(opts(p), [">1 Yes", " 2 Yes, and don't ask again for git push commands", " 3 No, and tell Claude what to do differently"]);
});

test("Codex's approval prompt", () => {
  const p = read([
    "",
    "  Would you like to run the following command?",
    "",
    "  Reason: this is a test reason",
    "",
    "  $ echo hello world",
    "",
    "",
    "› 1. Yes, proceed (y)",
    "  2. Yes, and don't ask again for commands that start with `echo hello world` (p)",
    "  3. No, and tell Codex what to do differently (esc)",
    "",
    "  Press enter to confirm or esc to cancel",
  ]);
  assert.equal(p.kind, "numbered");
  assert.deepEqual(opts(p), [">1 Yes, proceed", " 2 Yes, and don't ask again for commands that start with `echo hello world`", " 3 No, and tell Codex what to do differently"]);
});

test("a line the terminal wrapped is one option", () => {
  const p = read([
    "Do you want to proceed?",
    "❯ 1. Yes",
    "  2. Yes, and don't ask again for the npm run comman",
    ["ds in /home/me/shop"],
    "  3. No",
  ]);
  assert.deepEqual(opts(p), [">1 Yes", " 2 Yes, and don't ask again for the npm run commands in /home/me/shop", " 3 No"]);
});

test("a question's options with descriptions under them", () => {
  const p = read([
    "Which database?",
    "❯ 1. Postgres",
    "     The one the other services use",
    "  2. SQLite",
    "     A file, no server",
    "  3. Type something.",
    "Enter to select · ↑/↓ to navigate · Esc to cancel",
  ]);
  assert.deepEqual(opts(p), [">1 Postgres", " 2 SQLite", " 3 Type something."]);
});

test("a numbered list far up the screen is no menu", () => {
  const lines = ["Steps:", "1. Build it", "2. Test it", "3. Ship it"];
  for (let i = 0; i < 12; i++) lines.push(`output line ${i}`);
  lines.push("> waiting for you");
  assert.equal(read(lines).kind, "");
});

test("numbers out of order are no menu, nor is one option alone", () => {
  assert.equal(read(["1. One", "3. Three"]).kind, "");
  assert.equal(read(["Pick:", "❯ 1. Only this"]).kind, "");
});

test("the last menu wins, a restart at 1 starting anew", () => {
  const p = read(["1. Old", "2. Older", "", "Again?", "❯ 1. Yes", "  2. No"]);
  assert.deepEqual(opts(p), [">1 Yes", " 2 No"]);
});

test("OpenCode's row of buttons takes the arrows", () => {
  const p = read([
    "┃ △ Permission required: bash",
    "┃ $ rm -rf build",
    "┃",
    "┃  Allow once   Always allow   Reject",
  ]);
  assert.equal(p.kind, "arrows");
  assert.deepEqual(p.options, []);
});

test("no prompt is none", () => {
  const p = read(["$ ls", "README.md  main.go", "$"]);
  assert.equal(p.kind, "");
});

test("the key tells a new prompt from the last, not from output far above", () => {
  const prompt = (cmd) => ["Bash command", `  ${cmd}`, "Do you want to proceed?", "❯ 1. Yes", "  2. No"];
  const a = read(["old output A", ...Array(10).fill("x"), ...prompt("rm a")]);
  const b = read(["old output B", ...Array(10).fill("x"), ...prompt("rm a")]);
  const c = read(["old output A", ...Array(10).fill("x"), ...prompt("rm b")]);
  assert.equal(a.key, b.key);
  assert.notEqual(a.key, c.key);
});

test("the screen is read from its last text, below it blank", () => {
  const p = read(["Proceed?", "❯ 1. Yes", "  2. No"], 30);
  assert.equal(p.kind, "numbered");
});
