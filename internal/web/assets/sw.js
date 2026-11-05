// omaterm's service worker: it shows the notifications omaterm pushes
// (push.go) and, tapped, opens the session one is about.
//
// It has no fetch handler and keeps nothing in Cache Storage, and must
// not: what the page reads (files, diffs, a terminal's text) never lands
// on the phone, and a worker left on an old address serves nothing stale.

// ID is what a pane's id, or a notification's tag, may be: anything else
// in a push is ignored, never followed.
const ID = /^[A-Za-z0-9_-]{1,64}$/;

self.addEventListener("install", () => self.skipWaiting());
self.addEventListener("activate", (e) => e.waitUntil(self.clients.claim()));

// Every push shows: Safari drops a subscription whose pushes don't.
self.addEventListener("push", (e) => {
  let d = {};
  try { d = e.data?.json() ?? {}; } catch {}
  const pane = typeof d.pane === "string" && ID.test(d.pane) ? d.pane : "";
  const shown = self.registration.showNotification(String(d.title || "omaterm").slice(0, 120), {
    body: String(d.body || "").slice(0, 240),
    // A session's newer notification takes its older one's place, and
    // still sounds.
    tag: typeof d.tag === "string" && ID.test(d.tag) ? d.tag : "omaterm",
    renotify: true,
    icon: "icon-192.png",
    data: { pane },
  });
  e.waitUntil(Promise.all([shown, badge(d.waiting)]));
});

// badge puts the count of sessions waiting on the home-screen app's icon.
function badge(n) {
  if (!Number.isInteger(n) || n < 0 || !navigator.setAppBadge) return null;
  return (n ? navigator.setAppBadge(n) : navigator.clearAppBadge()).catch(() => {});
}

// A tap opens the session: in the page already open, else a new one.
self.addEventListener("notificationclick", (e) => {
  e.notification.close();
  const pane = e.notification.data?.pane || "";
  const hash = ID.test(pane) ? "#/pane/" + encodeURIComponent(pane) : "";
  e.waitUntil((async () => {
    const pages = await self.clients.matchAll({ type: "window", includeUncontrolled: true });
    const page = pages.find((c) => new URL(c.url).origin === self.location.origin);
    if (page) {
      page.postMessage({ type: "open", pane: hash ? pane : "" });
      return page.focus();
    }
    return self.clients.openWindow("/" + hash);
  })());
});

// A push service that renews a subscription tells the worker, which hands
// the new one to omaterm (Firefox; others renew while the page is open).
self.addEventListener("pushsubscriptionchange", (e) => {
  e.waitUntil((async () => {
    const sub = e.newSubscription || await self.registration.pushManager.subscribe(e.oldSubscription.options);
    await fetch("/api/push", {
      method: "POST", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ subscription: sub.toJSON() }),
    });
  })().catch(() => {}));
});
