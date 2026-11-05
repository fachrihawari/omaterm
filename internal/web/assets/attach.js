// A photo or screenshot for the agent, from the compose box: the 📎 opens
// the phone's camera or photos; the picture is scaled down to 2000 px at
// most and sent as a JPEG to the computer, which keeps it, without its
// EXIF, and answers its path. The path is pasted into the pane on its
// own, without Enter, as Claude Code and Codex take a pasted picture's
// path; a thumbnail over the box shows what went, until Send.
//
//   import { mountAttach } from "./attach.js";
//   const a = mountAttach({ id, sendPaste, say, signal });
//   foot.prepend(a.button); box.before(a.strip); a.clear();
//
// sendPaste returns false when it could not paste; say puts a line in the
// compose box's note.

// MAX is the longest side sent; THUMB the thumbnail's.
const MAX = 2000;
const THUMB = 160;

const CLIP = '<svg viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true"><path d="m21.44 11.05-9.19 9.19a6 6 0 0 1-8.49-8.49l8.57-8.57A4 4 0 1 1 18 8.84l-8.59 8.57a2 2 0 0 1-2.83-2.83l8.49-8.48"/></svg>';

// styles loads the stylesheet, once.
function styles() {
  if (document.querySelector('link[href="attach.css"]')) return;
  const l = document.createElement("link");
  l.rel = "stylesheet";
  l.href = "attach.css";
  document.head.append(l);
}

// shrink draws the picture at MAX px at most, on white (a JPEG has no
// clear), and makes its JPEG and its thumbnail, a data: URL, which the
// page's policy lets an <img> show.
async function shrink(file) {
  let bmp;
  try {
    bmp = await createImageBitmap(file);
  } catch {
    throw new Error("That picture can't be read here");
  }
  const draw = (side, type, quality) => {
    const scale = Math.min(1, side / Math.max(bmp.width, bmp.height));
    const c = document.createElement("canvas");
    c.width = Math.max(1, Math.round(bmp.width * scale));
    c.height = Math.max(1, Math.round(bmp.height * scale));
    const ctx = c.getContext("2d");
    ctx.fillStyle = "#fff";
    ctx.fillRect(0, 0, c.width, c.height);
    ctx.drawImage(bmp, 0, 0, c.width, c.height);
    return type ? new Promise((done) => c.toBlob(done, type, quality)) : c.toDataURL("image/jpeg", quality);
  };
  try {
    const blob = await draw(MAX, "image/jpeg", 0.88);
    if (!blob) throw new Error("The picture couldn't be made smaller");
    return { blob, thumb: draw(THUMB, "", 0.7) };
  } finally {
    bmp.close?.();
  }
}

export function mountAttach({ id, sendPaste, say, signal }) {
  styles();
  const button = document.createElement("label");
  button.className = "compose-attach";
  button.title = "Send a photo or screenshot";
  button.innerHTML = CLIP;
  // The input is the button's own, so a tap opens the picker as the phone
  // does it; seen by keyboards and screen readers, not by eyes.
  const input = document.createElement("input");
  input.type = "file";
  input.accept = "image/*";
  input.setAttribute("aria-label", "Send a photo or screenshot");
  button.append(input);

  const strip = document.createElement("div");
  strip.className = "compose-shots";
  strip.hidden = true;
  strip.setAttribute("aria-label", "Photos pasted into the prompt");

  let busy = false;
  const setBusy = (on) => {
    busy = on;
    button.classList.toggle("busy", on);
    input.disabled = on;
  };

  const pasted = document.createElement("span");
  pasted.textContent = "Pasted into the prompt";

  function add(thumb, path) {
    const img = document.createElement("img");
    img.src = thumb;
    img.alt = "A photo, pasted";
    img.title = path;
    strip.append(img, pasted);
    strip.hidden = false;
  }

  input.addEventListener("change", async () => {
    const file = input.files?.[0];
    input.value = "";
    if (!file || busy) return;
    setBusy(true);
    say("");
    try {
      const { blob, thumb } = await shrink(file);
      const res = await fetch(`/api/panes/${encodeURIComponent(id)}/upload`, {
        method: "POST", headers: { "Content-Type": "image/jpeg" }, body: blob, signal,
      });
      if (!res.ok) throw new Error((await res.text()).trim() || res.statusText);
      const { path } = await res.json();
      if (typeof path !== "string" || !path) throw new Error("The computer didn't keep it");
      if (sendPaste(path) === false) throw new Error("Not connected: the photo wasn't pasted");
      add(thumb, path);
    } catch (err) {
      if (err?.name !== "AbortError") say(err?.message || "The photo wasn't sent");
    } finally {
      setBusy(false);
    }
  }, { signal });

  return {
    button,
    strip,
    clear() {
      strip.replaceChildren();
      strip.hidden = true;
    },
  };
}
