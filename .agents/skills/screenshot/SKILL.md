---
name: screenshot
description: >
  Capture screenshots on this Omarchy (Hyprland/Wayland) machine, for bug
  reports, UI review, or attaching visual evidence. Use when asked to take a
  screenshot, grab the screen, capture a window or region, or record what the
  app looks like. Covers omarchy capture, grim/slurp, window geometry, HiDPI
  scaling, driving the UI with wtype/wclick, and the failure modes that make
  captures hang.
---

# Screenshots on Omarchy

Omarchy ships a capture stack: `omarchy capture screenshot` wraps `grim`
(capture) and `slurp` (region picker), with `tensaku-edit` as the annotation
editor. This machine runs Omarchy 4.0.6 on Hyprland 0.56 with a 1.25-scale
display.

Read `/usr/share/omarchy/default/agents/skills/omarchy/capture.md` for the
upstream reference. The rules below are what matters when an agent is the one
capturing.

## Check the display is awake first

This is the most common failure. If the display is in DPMS-off, `grim` blocks
forever waiting for a frame — there is no built-in timeout.

```bash
hyprctl monitors -j | jq -r '.[] | "\(.name) dpms=\(.dpmsStatus)"'
```

If `dpms=false`, wake it before capturing:

```bash
hyprctl eval 'hl.dsp.dpms({ action = "on" })'
```

On Hyprland 0.56 the dispatcher syntax is Lua. `hyprctl dispatch dpms on`
fails with a parse error, so do not use it. Always pass `timeout` to `grim` as
a backstop: a capture that hangs should fail loudly, not stall the session.

## Prefer a non-interactive mode

`slurp` needs a human to drag a region. Modes `smart`, `region`, and `windows`
all open it and will hang an agent. Use only these:

```bash
# Full screen, straight to disk. Prints the saved path.
omarchy capture screenshot fullscreen save

# Send the shot somewhere predictable instead of ~/Pictures
OMARCHY_SCREENSHOT_DIR=/tmp/opencode/shots omarchy capture screenshot fullscreen save
```

The destination resolves as `OMARCHY_SCREENSHOT_DIR`, then `XDG_PICTURES_DIR`,
then `~/Pictures`. With `save` the file is written and its path printed;
without a processing argument the default is `slurp`, which copies to the
clipboard and opens the editor — not what you want from an agent.

`omarchy capture screenshot windows save` picks a window interactively, so it
is equally unusable here. Capture a specific window by geometry instead.

## Capture one window

Hyprland reports logical coordinates, so read them from the client and hand
them to `grim`:

```bash
geo=$(hyprctl clients -j | jq -r '.[] | select(.class == "oterm")
  | "\(.at[0]),\(.at[1]) \(.size[0])x\(.size[1])"')
timeout 15 grim -g "$geo" /tmp/opencode/window.png
```

Pick the window by `.class` (app id) or `.title`. `hyprctl clients -j` lists
every mapped window with `at`, `size`, `class`, and `title`; check the list
before filtering, since the class is not always the app's name.

## Expect physical pixels, not logical ones

`grim` geometry is in logical coordinates but the PNG comes back in physical
pixels. On this 1.25-scale display a `100x100` region yields a `125x125` image,
and the full `1536x960` logical screen yields `1920x1200`. Do not "fix" this
by dividing — it is correct. Only account for it when a consumer needs exact
pixel dimensions.

## When the region is the point

For a crop of a known element, capture the whole window and crop afterwards
with ImageMagick, which is more reliable than asking a human to drag a box:

```bash
convert window.png -crop 340x120+1100+80 +repage -resize 250% detail.png
```

`+repage` matters: without it the crop keeps its original canvas offset and
later tools misplace it.

## Clicking the app before capturing

Many shots are only meaningful after driving the UI into a state — opening a
panel, switching tabs, expanding a row. `wtype` covers typing and key
chords, and `wclick` covers the pointer:

```bash
wtype -M ctrl -M shift -k p -m shift -m ctrl   # command palette
wclick click 1365 94                            # absolute logical coords
wclick move 1365 94                             # move without clicking
```

`wclick` is a wrapper around `wlrctl`, which drives the
`zwlr_virtual_pointer_v1` protocol. That matters: it needs no `uinput`
device and no root, unlike `ydotool`, whose daemon fails with
`failed to open uinput device: Permission denied` unless the user is in the
`input` group.

Two gotchas when picking coordinates:

- **`wclick` takes absolute logical screen coordinates**, not window-relative
  ones. Add the window's `at` offset from `hyprctl clients -j`. A click meant
  for a tab inside a window at `5,31` must include that `5,31`.
- **Convert from the screenshot before clicking.** The PNG is in physical
  pixels (1.25× here), so an element at `x=1700` in the image sits at
  `1700 / 1.25 = 1360` logical, plus the window offset.

Verify each click landed before capturing — read the PNG back and confirm the
state changed. A click that silently misses looks exactly like a UI bug.

## Verify, then clean up

Read the PNG back and look at it before reporting success — a black or
wallpaper-only image usually means the wrong window or a sleeping display.
Delete temporary captures when done; do not leave files in `~/Pictures`.

To share a capture, use `omarchy share file <path>`; shrink large ones first
with `omarchy transcode <input> png <resolution>`.
