// Posts a thread to Threads from a file of posts: the first is the thread's
// root, and each one after replies to the one before, as a thread is made.
//
//   node scripts/threads-post.mjs check   <posts.json>
//   node scripts/threads-post.mjs upload  <posts.json> --gh <owner/repo> [--tag threads-media]
//   node scripts/threads-post.mjs post    <posts.json> [--base-url <url>] [--from N] [--at "2026-11-04 19:30"]
//   node scripts/threads-post.mjs limit
//   node scripts/threads-post.mjs refresh-token
//   node scripts/threads-post.mjs delete  <post-id>
//
// A posts file:
//
//   { "topic": "AI", "posts": [
//       { "text": "…", "video": "~/Videos/omaterm-launch-tall.mp4" },
//       { "text": "…" } ] }
//
// check posts nothing: it measures each text as Threads does (500 at most,
// an emoji counting its UTF-8 bytes; 5 links at most), and each video
// against Threads' rules, with ffprobe.
//
// Threads fetches a video from a public URL, never from this machine.
// upload puts the videos up as assets of a GitHub release (gh must be
// logged in), in a repository of their own rather than omaterm's, whose
// releases feed the app's updates. --base-url says the videos are already
// served there, by their file names.
//
// post publishes. It keeps what it published in <posts.json>.state.json, so
// a run that failed half way goes on from there rather than posting twice;
// --from N starts at post N (1-based). --at waits until then: Threads has
// no scheduling of its own. Nothing is ever deleted unless asked.
//
// The account, once, by hand (it needs your login):
//   1. developers.facebook.com: create an app with the "Access the Threads
//      API" use case, with threads_basic and threads_content_publish.
//   2. App roles: add your Threads account as a Threads tester, and accept
//      it in Threads (Settings, Account, Website permissions, Invites).
//   3. Get a token for it (the use case's "Generate access token"), and
//      swap it for one that lasts 60 days:
//      curl "https://graph.threads.com/access_token?grant_type=th_exchange_token&client_secret=APP_SECRET&access_token=SHORT_TOKEN"
//   4. Your user id: curl "https://graph.threads.com/v1.0/me?fields=id,username&access_token=TOKEN"
//   5. Put both in ~/.config/omaterm-threads/credentials (chmod 600):
//        THREADS_USER_ID=…
//        THREADS_ACCESS_TOKEN=…
//      or in the environment. refresh-token renews the token (allowed once
//      it is a day old; it lapses after 60 days without).

import { execFileSync } from "node:child_process";
import { existsSync, openSync, readSync, closeSync, readFileSync, statSync, writeFileSync, chmodSync } from "node:fs";
import { homedir } from "node:os";
import { basename, join, resolve } from "node:path";

const api = process.env.THREADS_API || "https://graph.threads.com";
const credsFile = join(homedir(), ".config", "omaterm-threads", "credentials");
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

function die(msg) {
  console.error("threads-post: " + msg);
  process.exit(1);
}

function args(argv) {
  const pos = [], flags = {};
  for (let i = 0; i < argv.length; i++) {
    if (argv[i].startsWith("--")) {
      flags[argv[i].slice(2)] = argv[i + 1] && !argv[i + 1].startsWith("--") ? argv[++i] : true;
    } else {
      pos.push(argv[i]);
    }
  }
  return { pos, flags };
}

const home = (p) => (p.startsWith("~/") ? join(homedir(), p.slice(2)) : p);

function load(file) {
  const doc = JSON.parse(readFileSync(file, "utf8"));
  if (!Array.isArray(doc.posts) || doc.posts.length === 0) die(file + ": no posts");
  doc.posts.forEach((p) => p.video && (p.video = resolve(home(p.video))));
  return doc;
}

const stateFile = (file) => file + ".state.json";

function loadState(file) {
  return existsSync(stateFile(file)) ? JSON.parse(readFileSync(stateFile(file), "utf8")) : { media: {}, posts: [] };
}

function saveState(file, st) {
  writeFileSync(stateFile(file), JSON.stringify(st, null, 2) + "\n");
}

// length is a text's length as Threads counts it: an emoji by its UTF-8
// bytes, any other character as one.
function length(text) {
  let n = 0;
  for (const ch of text) {
    n += /\p{Extended_Pictographic}|‍|️/u.test(ch) ? Buffer.byteLength(ch) : 1;
  }
  return n;
}

// links are the distinct links in a text, with a scheme or without.
function links(text) {
  const found = text.match(/\b(?:https?:\/\/)?(?:[a-z0-9-]+\.)+[a-z]{2,}(?:\/[^\s]*)?/gi) || [];
  return new Set(found.map((l) => l.replace(/^https?:\/\//, "").replace(/[.,)]+$/, "")));
}

// moovFirst tells whether an MP4's index comes before its data, as
// Threads wants: walks the top-level boxes until one of the two.
function moovFirst(path) {
  const fd = openSync(path, "r");
  try {
    const head = Buffer.alloc(16);
    let at = 0;
    const size = statSync(path).size;
    while (at < size) {
      if (readSync(fd, head, 0, 16, at) < 8) return false;
      let len = head.readUInt32BE(0);
      const type = head.toString("latin1", 4, 8);
      if (type === "moov") return true;
      if (type === "mdat") return false;
      if (len === 1) len = Number(head.readBigUInt64BE(8));
      if (len < 8) return false;
      at += len;
    }
    return false;
  } finally {
    closeSync(fd);
  }
}

function checkVideo(path) {
  const problems = [];
  if (!existsSync(path)) return ["missing: " + path];
  const out = JSON.parse(execFileSync("ffprobe", ["-v", "error", "-print_format", "json", "-show_format", "-show_streams", path], { encoding: "utf8" }));
  const v = out.streams.find((s) => s.codec_type === "video");
  if (!v) return ["no video stream"];
  const [num, den] = v.r_frame_rate.split("/").map(Number);
  const fps = num / (den || 1);
  const dur = Number(out.format.duration);
  if (!["h264", "hevc"].includes(v.codec_name)) problems.push("codec " + v.codec_name + ", not H.264 or HEVC");
  if (v.pix_fmt !== "yuv420p") problems.push("pixels " + v.pix_fmt + ", not yuv420p");
  if (fps < 23 || fps > 60) problems.push(fps.toFixed(1) + " fps, not 23 to 60");
  if (v.width > 1920) problems.push(v.width + " wide, more than 1920");
  if (!(dur > 0 && dur <= 300)) problems.push(dur + " s, not up to 300");
  if (Number(out.format.size) > 1 << 30) problems.push("more than 1 GB");
  const a = out.streams.find((s) => s.codec_type === "audio");
  if (a && (a.codec_name !== "aac" || Number(a.sample_rate) > 48000)) problems.push("audio not AAC at 48 kHz or less");
  if (!moovFirst(path)) problems.push("its index (moov) isn't first: encode with -movflags +faststart");
  return problems;
}

function check(file) {
  const doc = load(file);
  let bad = 0;
  if (doc.topic !== undefined && (doc.topic.length < 1 || doc.topic.length > 50 || /[.&]/.test(doc.topic))) {
    console.log(`topic "${doc.topic}": 1 to 50 characters, no . or &`);
    bad++;
  }
  doc.posts.forEach((p, i) => {
    const problems = [];
    const n = length(p.text || "");
    if (!p.text && !p.video) problems.push("no text and no video");
    if (n > 500) problems.push(`${n} characters, more than 500`);
    if (links(p.text || "").size > 5) problems.push("more than 5 links");
    if (p.video) problems.push(...checkVideo(p.video).map((m) => basename(p.video) + ": " + m));
    console.log(`${i + 1}/ ${String(n).padStart(3)} chars${p.video ? "  + " + basename(p.video) : ""}${problems.length ? "\n    " + problems.join("\n    ") : ""}`);
    bad += problems.length;
  });
  if (bad) die(`${bad} problem(s)`);
  console.log("ok");
}

function upload(file, flags) {
  const repo = flags.gh;
  if (!repo || repo === true) die("upload needs --gh <owner/repo>, a repository of its own for the videos");
  const tag = flags.tag || "threads-media";
  const doc = load(file);
  const st = loadState(file);
  const videos = [...new Set(doc.posts.filter((p) => p.video).map((p) => p.video))];
  try {
    execFileSync("gh", ["release", "view", tag, "--repo", repo], { stdio: "ignore" });
  } catch {
    execFileSync("gh", ["release", "create", tag, "--repo", repo, "--prerelease", "--title", "Threads media",
      "--notes", "Videos the Threads posts link to."], { stdio: "inherit" });
  }
  execFileSync("gh", ["release", "upload", tag, "--repo", repo, "--clobber", ...videos], { stdio: "inherit" });
  for (const v of videos) {
    const url = `https://github.com/${repo}/releases/download/${tag}/${basename(v)}`;
    reachable(url);
    st.media[v] = url;
  }
  saveState(file, st);
  console.log(`${videos.length} video(s) up; their URLs are in ${stateFile(file)}`);
}

// reachable dies unless url, redirects followed, answers 200 with a video.
function reachable(url) {
  const out = execFileSync("curl", ["-sIL", url], { encoding: "utf8" });
  const blocks = out.trim().split(/\r?\n\r?\n/);
  const last = blocks[blocks.length - 1];
  const status = (last.match(/^HTTP\/\S+ (\d+)/) || [])[1];
  const type = ((last.match(/^content-type:\s*(.+)$/im) || [])[1] || "").trim();
  if (status !== "200") die(`${url} answers ${status || "nothing"}`);
  if (!/^video\/|^application\/octet-stream/.test(type)) die(`${url} is ${type || "of no type"}, not a video`);
}

function creds() {
  const env = {};
  if (existsSync(credsFile)) {
    if ((statSync(credsFile).mode & 0o077) !== 0) chmodSync(credsFile, 0o600);
    for (const line of readFileSync(credsFile, "utf8").split("\n")) {
      const m = line.match(/^\s*([A-Z_]+)\s*=\s*(.*?)\s*$/);
      if (m) env[m[1]] = m[2];
    }
  }
  const user = process.env.THREADS_USER_ID || env.THREADS_USER_ID;
  const token = process.env.THREADS_ACCESS_TOKEN || env.THREADS_ACCESS_TOKEN;
  if (!user || !token) die(`no THREADS_USER_ID and THREADS_ACCESS_TOKEN, in the environment or ${credsFile}`);
  return { user, token };
}

// call is a Threads API request: a POST's params, its token too, go as a
// form; a GET's in the URL, as Threads takes them.
async function call(method, path, params, token) {
  const body = new URLSearchParams({ ...params, access_token: token });
  const url = method === "GET" ? `${api}${path}?${body}` : `${api}${path}`;
  const res = await fetch(url, method === "GET" ? {} : { method, body });
  const data = await res.json().catch(() => ({}));
  if (!res.ok || data.error) {
    const e = data.error || {};
    throw new Error(`${method} ${path}: ${e.message || res.status}${e.error_user_msg ? " (" + e.error_user_msg + ")" : ""}`);
  }
  return data;
}

// ready waits for a container to be processed: every 10 s, for 5 minutes.
async function ready(id, token) {
  for (let i = 0; i < 30; i++) {
    const s = await call("GET", `/v1.0/${id}`, { fields: "status,error_message" }, token);
    if (s.status === "FINISHED") return;
    if (s.status === "ERROR" || s.status === "EXPIRED") throw new Error(`container ${id}: ${s.status} ${s.error_message || ""}`);
    await sleep(10_000);
  }
  throw new Error(`container ${id} not ready after 5 minutes`);
}

async function waitUntil(at) {
  const when = new Date(at.replace(" ", "T"));
  if (isNaN(when)) die(`--at "${at}": give a time as 2026-11-04 19:30`);
  const ms = when - Date.now();
  if (ms > 0) {
    console.log(`waiting until ${when.toLocaleString()}`);
    await sleep(ms);
  }
}

async function post(file, flags) {
  const doc = load(file);
  const st = loadState(file);
  const { user, token } = creds();
  // The videos' public URLs: uploaded, or under --base-url.
  for (const p of doc.posts.filter((p) => p.video)) {
    if (flags["base-url"]) st.media[p.video] = flags["base-url"].replace(/\/$/, "") + "/" + basename(p.video);
    if (!st.media[p.video]) die(`${basename(p.video)} has no public URL: run upload, or give --base-url`);
    reachable(st.media[p.video]);
  }
  saveState(file, st);
  const from = Number(flags.from || 1) - 1;
  if (flags.at) await waitUntil(flags.at);
  for (let i = from; i < doc.posts.length; i++) {
    if (st.posts[i]?.id) {
      console.log(`${i + 1}/ already up: ${st.posts[i].permalink || st.posts[i].id}`);
      continue;
    }
    const p = doc.posts[i];
    const params = { text: p.text || "" };
    if (p.video) Object.assign(params, { media_type: "VIDEO", video_url: st.media[p.video] });
    else params.media_type = "TEXT";
    // The root takes the topic; each one after replies to the one before.
    if (i === 0 && doc.topic) params.topic_tag = doc.topic;
    if (i > 0) {
      const prev = st.posts[i - 1]?.id;
      if (!prev) die(`post ${i} isn't up, so ${i + 1} has nothing to reply to`);
      params.reply_to_id = prev;
    }
    const { id: container } = await call("POST", `/v1.0/${user}/threads`, params, token);
    await (p.video ? ready(container, token) : sleep(3000));
    const { id } = await call("POST", `/v1.0/${user}/threads_publish`, { creation_id: container }, token);
    const { permalink } = await call("GET", `/v1.0/${id}`, { fields: "permalink" }, token).catch(() => ({}));
    st.posts[i] = { id, permalink };
    saveState(file, st);
    console.log(`${i + 1}/ up: ${permalink || id}`);
  }
  console.log("done: " + (st.posts[0]?.permalink || st.posts[0]?.id));
}

async function main() {
  const [cmd, ...rest] = process.argv.slice(2);
  const { pos, flags } = args(rest);
  switch (cmd) {
    case "check":
      return check(pos[0] || die("check <posts.json>"));
    case "upload":
      return upload(pos[0] || die("upload <posts.json> --gh <owner/repo>"), flags);
    case "post":
      return post(pos[0] || die("post <posts.json>"), flags);
    case "limit": {
      const { user, token } = creds();
      const r = await call("GET", `/v1.0/${user}/threads_publishing_limit`, { fields: "quota_usage,config,reply_quota_usage,reply_config" }, token);
      return console.log(JSON.stringify(r.data?.[0] || r, null, 2));
    }
    case "refresh-token": {
      const { token } = creds();
      const r = await call("GET", "/refresh_access_token", { grant_type: "th_refresh_token" }, token);
      const days = Math.round(r.expires_in / 86400);
      if (existsSync(credsFile) && !process.env.THREADS_ACCESS_TOKEN) {
        const text = readFileSync(credsFile, "utf8").replace(/^(\s*THREADS_ACCESS_TOKEN\s*=).*$/m, "$1" + r.access_token);
        writeFileSync(credsFile, text, { mode: 0o600 });
        return console.log(`renewed in ${credsFile}, for ${days} days`);
      }
      return console.log(`renewed, for ${days} days; put it in THREADS_ACCESS_TOKEN:\n${r.access_token}`);
    }
    case "delete": {
      const { token } = creds();
      await call("DELETE", `/v1.0/${pos[0] || die("delete <post-id>")}`, {}, token);
      return console.log("deleted " + pos[0]);
    }
    default:
      die("check | upload | post | limit | refresh-token | delete; the top of this file says how");
  }
}

main().catch((e) => die(e.message));
