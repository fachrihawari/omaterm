// Extracts the lucide icons the app uses from the npm package's icon
// node data into standalone SVG files in icons/, embedded by icons.go.
// Run: bun run scripts/extract-icons.ts
import { mkdirSync, writeFileSync } from "fs";

// Renamed lucide icons keep their mock name on the right.
const icons: Record<string, string> = {
  bell: "bell",
  "chevron-down": "chevron-down",
  "chevron-right": "chevron-right",
  "columns-2": "columns-2",
  ellipsis: "ellipsis",
  "external-link": "external-link",
  "file-code-2": "file-code",
  "file-text": "file-text",
  folder: "folder",
  "folder-git-2": "folder-git-2",
  "folder-open": "folder-open",
  "folder-plus": "folder-plus",
  "git-branch": "git-branch",
  "git-compare-arrows": "git-compare-arrows",
  "globe-2": "globe",
  info: "info",
  minus: "minus",
  "more-horizontal": "ellipsis",
  "open-in-new": "external-link",
  "panel-left": "panel-left",
  "panel-left-close": "panel-left-close",
  "panel-right": "panel-right",
  plus: "plus",
  radio: "radio",
  "refresh-cw": "refresh-cw",
  "rotate-cw": "rotate-cw",
  "rows-2": "rows-2",
  search: "search",
  "split-square-vertical": "split",
  "trash-2": "trash",
  "undo-2": "undo-2",
  x: "x",
  terminal: "terminal",
  "folder-search": "folder-search",
};
const names = Object.entries(icons);

mkdirSync("icons", { recursive: true });

for (const [name, source] of names) {
  const mod = await import(`../node_modules/lucide/dist/esm/icons/${source}.mjs`);
  const nodes: [string, Record<string, string>][] = mod.default;
  const inner = nodes
    .map(([tag, attrs]) => {
      const a = Object.entries(attrs)
        .map(([k, v]) => `${k}="${v}"`)
        .join(" ");
      return `<${tag} ${a}/>`;
    })
    .join("");
  const svg =
    `<svg xmlns="http://www.w3.org/2000/svg" width="24" height="24" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">${inner}</svg>\n`;
  writeFileSync(`icons/${name}.svg`, svg);
}
console.log(`wrote ${names.length} icons`);
