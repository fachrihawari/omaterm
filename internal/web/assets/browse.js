// A session's files and git changes, to read in its card in place of the
// terminal, which keeps running behind them. Nothing here changes a
// file: the desktop app, or the shell in the pane, does that.
//
//   import { mountBrowse } from "./browse.js";
//   const b = mountBrowse(el, { id, api, h });
//   b.show("files" | "file" | "changes" | "diff", path);
//   b.turnEnded();   // the session stopped working: changes may be new
//
// Its views are links (#/pane/<id>/files/<dir>, …/file/<path>,
// …/changes, …/changes/<path>), so the phone's back goes back a view.

const FOLDER = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M20 20a2 2 0 0 0 2-2V8a2 2 0 0 0-2-2h-7.9a2 2 0 0 1-1.69-.9L9.6 3.9A2 2 0 0 0 7.93 3H4a2 2 0 0 0-2 2v13a2 2 0 0 0 2 2Z"/></svg>';
const FILE = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M15 2H6a2 2 0 0 0-2 2v16a2 2 0 0 0 2 2h12a2 2 0 0 0 2-2V7Z"/><path d="M14 2v4a2 2 0 0 0 2 2h4"/></svg>';
const REFRESH = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round"><path d="M3 12a9 9 0 0 1 9-9 9.75 9.75 0 0 1 6.74 2.74L21 8"/><path d="M21 3v5h-5"/><path d="M21 12a9 9 0 0 1-9 9 9.75 9.75 0 0 1-6.74-2.74L3 16"/><path d="M8 16H3v5"/></svg>';

const LETTER = { added: "A", modified: "M", deleted: "D", renamed: "R", type: "T" };

// size says a file's size as the desktop's status bar does.
function size(n) {
  if (n >= 1 << 20) return (n / (1 << 20)).toFixed(1).replace(/\.0$/, "") + " MB";
  if (n >= 1 << 10) return Math.round(n / (1 << 10)) + " KB";
  return n + " B";
}

// parsePatch reads git diff's text into its lines: hunk headers, and
// added, removed and kept lines with their old and new numbers.
export function parsePatch(text) {
  const out = [];
  let o = 0, n = 0, inHunk = false;
  for (const line of text.split("\n")) {
    const m = line.match(/^@@ -(\d+)(?:,\d+)? \+(\d+)(?:,\d+)? @@(.*)$/);
    if (m) {
      o = Number(m[1]);
      n = Number(m[2]);
      inHunk = true;
      out.push({ kind: "hunk", text: m[3].trim() });
      continue;
    }
    if (line.startsWith("diff --git")) { inHunk = false; continue; }
    if (!inHunk) {
      if (line.startsWith("Binary files")) out.push({ kind: "binary" });
      continue;
    }
    switch (line[0]) {
      case "+": out.push({ kind: "add", n: n++, text: line.slice(1) }); break;
      case "-": out.push({ kind: "del", o: o++, text: line.slice(1) }); break;
      case " ": out.push({ kind: "ctx", o: o++, n: n++, text: line.slice(1) }); break;
      case "\\": out.push({ kind: "note", text: line.slice(2) }); break;
    }
  }
  return out;
}

export function mountBrowse(el, { id, api, h }) {
  const base = `/api/panes/${encodeURIComponent(id)}`;
  const link = (kind, path = "") => `#/pane/${encodeURIComponent(id)}/${kind}` + (path ? "/" + encodeURIComponent(path) : "");
  const q = (path) => "?path=" + encodeURIComponent(path || "");
  const svg = (markup, cls = "") => h("span", { class: `ic ${cls}`, html: markup });
  let shown = { kind: "", path: "" };
  let ticket = 0;
  let rootName = "Files"; // the folder's name, once a listing told it

  // crumbs are the way up from path: the view's name, then each folder.
  const crumbs = (first, kind, path, last = true) => {
    const parts = path ? path.split("/") : [];
    const out = [h("a", { class: "bv-crumb", href: link(kind) }, first)];
    parts.forEach((p, i) => {
      out.push(h("span", { class: "bv-sep" }, "/"));
      const to = parts.slice(0, i + 1).join("/");
      out.push(i === parts.length - 1 && last
        ? h("span", { class: "bv-crumb here" }, p)
        : h("a", { class: "bv-crumb", href: link("files", to) }, p));
    });
    return h("div", { class: "bv-crumbs" }, out);
  };

  // counts are a file's or the changes' added and removed lines, the
  // nought left out.
  const counts = (added, removed) => [
    added ? h("span", { class: "bv-add" }, "+" + added) : null,
    added && removed ? " " : null,
    removed ? h("span", { class: "bv-del" }, "−" + removed) : null,
  ];
  // changedPath is a changed file's path: its folder faint, its name.
  const changedPath = (path) => {
    const slash = path.lastIndexOf("/");
    return h("div", { class: "bv-crumbs" },
      h("a", { class: "bv-crumb", href: link("changes") }, "Changes"),
      h("span", { class: "bv-sep" }, "/"),
      h("span", { class: "bv-crumb" },
        slash > 0 ? path.slice(0, slash + 1) : null,
        h("span", { class: "here" }, path.slice(slash + 1)),
      ),
    );
  };

  const bar = (left, ...right) => h("div", { class: "bv-bar" }, left, h("span", { class: "sp" }), ...right);
  const refreshBtn = () => h("button", {
    class: "ib bv-refresh", title: "Refresh", "aria-label": "Refresh",
    onclick: () => load(shown.kind, shown.path),
  }, svg(REFRESH));
  const note = (text) => h("p", { class: "bv-note" }, text);

  // load fetches the view's data and draws it, unless another view was
  // asked for meanwhile.
  async function load(kind, path) {
    const mine = ++ticket;
    const draw = (...nodes) => { if (mine === ticket) el.replaceChildren(...nodes); };
    if (!el.firstChild || el.dataset.kind !== kind) draw(note("Loading…"));
    el.dataset.kind = kind;
    try {
      switch (kind) {
        case "files": return draw(...files(await api(base + "/files" + q(path)), path));
        case "file": return draw(...file(await api(base + "/file" + q(path)), path));
        case "changes": return draw(...changes(await api(base + "/changes")));
        case "diff": return draw(...diff(await api(base + "/diff" + q(path)), path));
      }
    } catch (err) {
      if (err.message === "not paired") return;
      const [name, to] = kind === "files" || kind === "file" ? [rootName, "files"] : ["Changes", "changes"];
      draw(bar(crumbs(name, to, "", false), refreshBtn()), note(err.message));
    }
  }

  function files(d, path) {
    rootName = d.root || rootName;
    const rows = d.entries.map((e) => h("a", {
      class: "row bv-row" + (e.muted ? " muted" : ""),
      href: link(e.dir ? "files" : "file", path ? path + "/" + e.name : e.name),
    },
      h("span", { class: "pi" }, svg(e.dir ? FOLDER : FILE, e.dir ? "dir" : "")),
      h("span", { class: "nm" }, e.name),
      e.dir ? null : h("span", { class: "tm" }, size(e.size)),
    ));
    return [
      bar(crumbs(rootName, "files", path, true), refreshBtn()),
      h("div", { class: "bv-list" },
        rows.length ? rows : note("This folder is empty."),
        d.more ? note("Showing the first " + rows.length + " entries.") : null,
      ),
    ];
  }

  function file(f, path) {
    const top = bar(crumbs(rootName, "files", path), h("span", { class: "bv-meta" }, size(f.size)));
    switch (f.kind) {
      case "image":
        return [top, h("div", { class: "bv-image" }, h("img", { src: base + "/raw" + q(path), alt: path }))];
      case "binary":
        return [top, note("A binary file: there is no text to show.")];
      case "large":
        return [top, note("Too large to show here.")];
    }
    const lines = f.text.endsWith("\n") ? f.text.slice(0, -1).split("\n") : f.text.split("\n");
    const nums = lines.map((_, i) => i + 1).join("\n");
    return [top, h("div", { class: "bv-code" },
      h("pre", { class: "gut", "aria-hidden": "true" }, nums),
      h("pre", { class: "txt" }, lines.join("\n")),
    )];
  }

  function changes(c) {
    const added = c.files.reduce((n, f) => n + f.added, 0);
    const removed = c.files.reduce((n, f) => n + f.removed, 0);
    const since = c.base ? "since " + c.base : "not committed";
    const summary = c.files.length
      ? h("span", { class: "bv-sum" }, `${c.files.length} ${c.files.length === 1 ? "file" : "files"} · `,
        counts(added, removed), added || removed ? " · " : "", since)
      : h("span", { class: "bv-sum" }, "Changes " + since);
    const rows = c.files.map((f) => {
      const slash = f.path.lastIndexOf("/");
      return h("a", { class: "row bv-row", href: link("changes", f.path) },
        h("span", { class: `bv-st ${f.status}`, title: f.status }, LETTER[f.status] || "?"),
        h("span", { class: "mid" },
          h("span", { class: "nm" }, f.path.slice(slash + 1)),
          slash > 0 ? h("span", { class: "sub" }, f.path.slice(0, slash)) : null,
        ),
        h("span", { class: "tm" }, f.binary ? "binary" : counts(f.added, f.removed)),
      );
    });
    return [
      bar(summary, refreshBtn()),
      h("div", { class: "bv-list" }, rows.length ? rows : note(c.base ? "No changes since " + c.base + "." : "Nothing that isn't committed.")),
    ];
  }

  function diff(d, path) {
    const top = bar(changedPath(path), refreshBtn());
    if (d.large) return [top, note("This diff is too large to show here.")];
    const lines = parsePatch(d.text || "");
    if (lines.some((l) => l.kind === "binary")) return [top, note("A binary file changed.")];
    if (!lines.length) return [top, note("No changes in this file.")];
    return [top, h("div", { class: "bv-diff" }, lines.map((l) =>
      l.kind === "hunk"
        ? h("div", { class: "dl hunk" }, h("span", { class: "o" }), h("span", { class: "n" }), h("span", { class: "t" }, l.text || "⋯"))
        : h("div", { class: "dl " + l.kind },
          h("span", { class: "o" }, l.o ?? ""),
          h("span", { class: "n" }, l.n ?? ""),
          h("span", { class: "t" }, l.text || " "),
        ),
    ))];
  }

  return {
    // show draws a view, fetched again each time it shows: what it was
    // stays until the new one comes, and where it was scrolled with it.
    show(kind, path = "") {
      if (shown.kind !== kind || shown.path !== path) el.scrollTop = 0;
      shown = { kind, path };
      load(kind, path);
    },
    turnEnded() {
      if (shown.kind === "changes" || shown.kind === "diff") load(shown.kind, shown.path);
    },
  };
}
