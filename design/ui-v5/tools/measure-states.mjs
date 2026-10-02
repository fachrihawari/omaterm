// R0 per-state component measurement. Usage: node measure-states.mjs
// Activates each reference view, then measures its components.
// Outputs: ../component-measures-states.json
import { writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import puppeteer from 'puppeteer-core';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const PROPS = ['display', 'width', 'height', 'padding-top', 'padding-right',
  'padding-bottom', 'padding-left', 'margin-top', 'margin-right',
  'margin-bottom', 'margin-left', 'border-top-width', 'border-right-width',
  'border-bottom-width', 'border-left-width', 'border-top-left-radius',
  'border-bottom-left-radius', 'background-color', 'color', 'font-family',
  'font-size', 'font-weight', 'line-height', 'letter-spacing', 'opacity',
  'box-shadow', 'gap', 'grid-template-columns'];

const STATES = {
  git: {
    actions: ['button[data-right="git"]'],
    selectors: ['.git-file', '.git-file .text-\\[11px\\]', '#stagedList',
      '#changesList', '#commitBtn', 'textarea', '#diffStageBtn'],
  },
  files: {
    actions: ['button[data-right="files"]'],
    selectors: ['.tree-row', '.tree-row.active', '#right-files input',
      '#right-files .rounded-md'],
  },
  diffSplit: {
    actions: ['button[data-view="diff"]'],
    selectors: ['.line', '.ln', '.diff-add', '.diff-del', '#splitDiffBtn',
      '#inlineDiffBtn', '#diffScopeLabel', '#diffDiscardBtn'],
  },
  diffInline: {
    actions: ['button[data-view="diff"]', '#inlineDiffBtn'],
    selectors: ['#inlineDiff .grid', '#inlineDiff .ln'],
  },
  editor: {
    actions: ['button[data-view="editor"]'],
    selectors: ['#view-editor .line', '#view-editor .ln'],
  },
  terminalPanes: {
    actions: ['button[data-view="terminal"]'],
    selectors: ['.pane-toolbar', '.pane-toolbar > div', '.split-handle-v',
      '#pane2 .h-8', '#pane3 .h-7', '.terminal-cursor'],
  },
};

async function main() {
  const browser = await puppeteer.launch({
    executablePath: '/usr/bin/chromium', args: ['--no-sandbox'],
  });
  const out = {};
  try {
    const page = await browser.newPage();
    await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
    await page.goto('file://' + join(root, 'capture.html'),
      { waitUntil: 'networkidle0', timeout: 60000 });
    await page.waitForFunction(
      () => document.querySelector('svg.lucide') !== null, { timeout: 30000 });
    await new Promise(r => setTimeout(r, 800));
    for (const [name, spec] of Object.entries(STATES)) {
      await page.reload({ waitUntil: 'networkidle0' });
      await page.waitForFunction(
        () => document.querySelector('svg.lucide') !== null, { timeout: 30000 });
      await new Promise(r => setTimeout(r, 500));
      for (const sel of spec.actions) {
        await page.click(sel);
        await new Promise(r => setTimeout(r, 300));
      }
      out[name] = await page.evaluate(({ selectors, props }) => {
        const res = {};
        const els = (sel) => [...document.querySelectorAll(sel)].slice(0, 3);
        for (const sel of selectors) {
          const found = els(sel);
          if (!found.length) { res[sel] = { found: false }; continue; }
          res[sel] = found.map(el => {
            const cs = getComputedStyle(el);
            const rect = el.getBoundingClientRect();
            const entry = { rect: { x: +rect.x.toFixed(2), y: +rect.y.toFixed(2),
              w: +rect.width.toFixed(2), h: +rect.height.toFixed(2) } };
            for (const p of props) entry[p] = cs.getPropertyValue(p);
            return entry;
          });
        }
        return res;
      }, { selectors: spec.selectors, props: PROPS });
    }
    await page.close();
  } finally {
    await browser.close();
  }
  writeFileSync(join(root, 'component-measures-states.json'), JSON.stringify(out, null, 1));
  console.log('states measured:', Object.keys(out).join(', '));
}

main().catch(e => { console.error(e); process.exit(1); });
