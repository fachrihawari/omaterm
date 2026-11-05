// Notifications on this phone: a bell in the list's footer opens a sheet
// that turns Web Push on and off (push.go, sw.js), lets the agent's words
// show and programs' own notifications come, and says why it can't when it can't. It also badges the
// home-screen app's icon with the sessions waiting.
//
//   import { mountPush } from "./push.js";
//   const push = mountPush({ api, h });
//   foot.append(push.el); push.badge(waiting); push.refresh(); await push.forget();

const ID = /^[A-Za-z0-9_-]{1,64}$/;

const S = (d) => `<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${d}</svg>`;
const BELL = S('<path d="M6 8a6 6 0 0 1 12 0c0 7 3 9 3 9H3s3-2 3-9"/><path d="M10.3 21a1.94 1.94 0 0 0 3.4 0"/>');
const BELL_OFF = S('<path d="M8.7 3A6 6 0 0 1 18 8a21.3 21.3 0 0 0 .6 5M17 17H3s3-2 3-9a4.67 4.67 0 0 1 .3-1.7"/><path d="M10.3 21a1.94 1.94 0 0 0 3.4 0M2 2l20 20"/>');
const CLOSE = S('<path d="M18 6 6 18M6 6l12 12"/>');

// What this browser can do. Push needs a secure page (https, or this
// computer's own address) with a service worker; iOS gives it only to a
// Home Screen app.
const secure = window.isSecureContext && "serviceWorker" in navigator;
const can = secure && "PushManager" in window && "Notification" in window;
const ios = /iPad|iPhone|iPod/.test(navigator.userAgent) || (navigator.platform === "MacIntel" && navigator.maxTouchPoints > 1);

// why is what stops notifications here, "" when nothing does.
function why() {
  if (!window.isSecureContext) {
    return "Notifications need a secure (https) address: open omaterm through Tailscale or Cloudflare to turn them on.";
  }
  if (!can && ios) {
    return "On iPhone, notifications come only to omaterm on the Home Screen. Tap Share, then Add to Home Screen. " +
      "Open it from there and pair again, with a code from “omaterm web pair” typed in any shell pane.";
  }
  if (!can) return "This browser can't show notifications from a web page.";
  if (Notification.permission === "denied") {
    return "Notifications are blocked for this site: allow them in the browser's settings, then turn them on here.";
  }
  return "";
}

// bytes reads a base64url key, as PushManager.subscribe takes it.
function bytes(b64) {
  const s = atob(b64.replace(/-/g, "+").replace(/_/g, "/"));
  return Uint8Array.from(s, (c) => c.charCodeAt(0));
}

function sameKey(buf, b64) {
  if (!buf) return false;
  const a = new Uint8Array(buf), b = bytes(b64);
  return a.length === b.length && a.every((x, i) => x === b[i]);
}

export function mountPush({ api, h }) {
  let st = { on: false, words: false, programs: false };
  let note = "";
  let busy = false;
  let sheet = null;
  let shownBadge = -1;

  const bell = h("button", { class: "ib bell", type: "button", "aria-haspopup": "dialog", onclick: () => open() });

  if (secure) {
    navigator.serviceWorker.register("sw.js").catch(() => {});
    // A notification tapped while the page is open: the worker asks it to
    // show the session, never a page of the notification's choosing.
    navigator.serviceWorker.addEventListener("message", (e) => {
      if (e.data?.type !== "open") return;
      const pane = e.data.pane;
      location.hash = typeof pane === "string" && ID.test(pane) ? "#/pane/" + encodeURIComponent(pane) : "";
    });
  }

  // ready is the service worker's registration, once it runs; a worker
  // that can't start fails it rather than leaving it waiting.
  const ready = () => Promise.race([navigator.serviceWorker.ready,
    new Promise((_, no) => setTimeout(() => no(new Error("the service worker didn't start")), 10000))]);
  const subscription = async () => (await ready()).pushManager.getSubscription();
  const drop = () => fetch("/api/push", { method: "DELETE" });

  // refresh asks omaterm whether this device is notified, and keeps it
  // true to the browser: a subscription renewed is handed over, one gone
  // (site data cleared, permission taken back) turns them off.
  async function refresh() {
    try {
      st = await api("/api/push/key");
      if (st.on && can) {
        const sub = Notification.permission === "granted" ? await subscription() : null;
        if (!sub) {
          st.on = false;
          drop();
        } else if (sub.endpoint !== st.endpoint) {
          Object.assign(st, await api("/api/push", { subscription: sub.toJSON() }), { endpoint: sub.endpoint });
        }
      }
    } catch {
      // Not now: the bell says off.
    }
    draw();
  }

  async function turnOn() {
    // Asked in the tap itself, before anything waits: iOS asks only then.
    const asked = Notification.requestPermission();
    setBusy(true);
    try {
      if ((await asked) !== "granted") {
        note = why() || "Notifications weren't allowed.";
        return;
      }
      if (!st.key) st = await api("/api/push/key");
      const reg = await ready();
      let sub = await reg.pushManager.getSubscription();
      if (sub && !sameKey(sub.options.applicationServerKey, st.key)) {
        await sub.unsubscribe();
        sub = null;
      }
      sub ??= await reg.pushManager.subscribe({ userVisibleOnly: true, applicationServerKey: bytes(st.key) });
      Object.assign(st, await api("/api/push", { subscription: sub.toJSON() }), { endpoint: sub.endpoint });
      note = "";
    } catch (err) {
      note = "Couldn't turn notifications on: " + err.message;
    } finally {
      setBusy(false);
    }
  }

  async function turnOff() {
    setBusy(true);
    try {
      await (await subscription())?.unsubscribe();
      const res = await drop();
      if (!res.ok) throw new Error((await res.text()).trim() || res.statusText);
      st.on = false;
      note = "";
    } catch (err) {
      note = "Couldn't turn notifications off: " + err.message;
    } finally {
      setBusy(false);
    }
  }

  // setOption changes what comes: { words } or { programs }.
  async function setOption(option) {
    setBusy(true);
    try {
      const sub = await subscription();
      if (!sub) throw new Error("this browser has no subscription");
      Object.assign(st, await api("/api/push", { subscription: sub.toJSON(), ...option }));
    } catch (err) {
      note = "Couldn't change that: " + err.message;
    } finally {
      setBusy(false);
    }
  }

  function setBusy(b) {
    busy = b;
    draw();
  }

  // --- the sheet ---------------------------------------------------------

  const toggle = (label, detail, cls) => {
    const input = h("input", { type: "checkbox", role: "switch", class: "push-switch " + cls });
    const row = h("label", { class: "push-row" },
      h("span", { class: "push-text" }, h("b", {}, label), h("small", {}, detail)),
      input,
    );
    return { row, input };
  };

  function open() {
    if (sheet) return;
    const notify = toggle("Notify this phone",
      "When a session needs you, fails or finishes. Not while you're using the computer.", "push-on");
    const words = toggle("Show what the agent says",
      "Off, only the session and what happened show, as “Needs permission”. On, the agent's words show too, lock screen included.", "push-words");
    notify.input.addEventListener("change", () => (notify.input.checked ? turnOn() : turnOff()));
    const programs = toggle("Programs' own notifications",
      "What a program in a session sends itself, as a build that finished. Off, only omaterm's own come.", "push-programs");
    words.input.addEventListener("change", () => setOption({ words: words.input.checked }));
    programs.input.addEventListener("change", () => setOption({ programs: programs.input.checked }));
    const msg = h("p", { class: "push-note", role: "status" });
    const close = h("button", { class: "push-close ib", type: "button", "aria-label": "Close", html: CLOSE, onclick: () => shut() });
    const panel = h("div", { class: "push-panel" },
      h("div", { class: "push-head" }, h("h2", { id: "push-title" }, "Notifications"), close),
      notify.row, words.row, programs.row, msg,
    );
    const el = h("div", { class: "push-sheet", role: "dialog", "aria-modal": "true", "aria-labelledby": "push-title" },
      h("div", { class: "push-backdrop", onclick: () => shut() }),
      panel,
    );
    const onKey = (e) => { if (e.key === "Escape") shut(); };
    document.addEventListener("keydown", onKey);
    sheet = { el, notify: notify.input, words: words.input, programs: programs.input, msg, onKey };
    if (!note) note = why();
    document.body.append(el);
    draw();
    close.focus();
    refresh();
  }

  function shut() {
    if (!sheet) return;
    const { el, onKey } = sheet;
    document.removeEventListener("keydown", onKey);
    sheet = null;
    note = "";
    el.classList.add("leaving");
    setTimeout(() => el.remove(), 160);
    bell.focus();
  }

  function draw() {
    const on = st.on && can;
    bell.classList.toggle("on", on);
    bell.innerHTML = on || can ? BELL : BELL_OFF;
    bell.title = on ? "Notifications: on" : can ? "Notifications: off" : "Notifications: not here";
    bell.setAttribute("aria-label", bell.title);
    if (!sheet) return;
    // Busy, the switch stays as tapped until the answer.
    if (!busy) {
      sheet.notify.checked = on;
      sheet.words.checked = on && Boolean(st.words);
      sheet.programs.checked = on && Boolean(st.programs);
    }
    sheet.notify.disabled = !can || busy;
    sheet.words.disabled = !on || busy;
    sheet.programs.disabled = !on || busy;
    sheet.msg.textContent = note;
    sheet.msg.hidden = !note;
  }

  document.head.append(h("link", { rel: "stylesheet", href: "push.css" }));
  draw();
  if (can) refresh();

  return {
    el: bell,
    // refresh asks omaterm again, as after pairing anew.
    refresh: () => can && refresh(),
    // badge shows how many sessions wait on the home-screen app's icon.
    badge(n) {
      if (n === shownBadge || !navigator.setAppBadge) return;
      shownBadge = n;
      (n ? navigator.setAppBadge(n) : navigator.clearAppBadge()).catch(() => {});
    },
    // forget turns this browser's notifications off, as it signs out.
    async forget() {
      if (!secure) return;
      try {
        const reg = await navigator.serviceWorker.getRegistration();
        await (await reg?.pushManager?.getSubscription())?.unsubscribe();
        await drop();
      } catch {
        // Signing out drops the subscription with the device anyway.
      }
    },
  };
}
