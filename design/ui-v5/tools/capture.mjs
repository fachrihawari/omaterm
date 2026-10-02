// R0 capture harness: offline reference copy + screenshots + computed styles.
// Usage: node capture.mjs
// Outputs: ../capture.html, ../captures/*.png, ../computed-styles.json,
//          ../icon-dom.json
import { readFileSync, writeFileSync, mkdirSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import puppeteer from 'puppeteer-core';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const VIEWPORTS = [
  { name: '1440x900', width: 1440, height: 900, dpr: 1 },
  { name: '1024x768', width: 1024, height: 768, dpr: 1 },
  { name: '800x600', width: 800, height: 600, dpr: 1 },
];
const BASE_SELECTORS = [
  '#projectsSidebar', '#projectsResizer', '#inspector', '#inspectorResizer',
  '.top-tab.active', '.terminal-pane.active', '.pane-toolbar',
  '.right-tab.active', '#toast', 'header', 'footer',
  '.project-item.active', '#diffStageBtn', '#commitBtn',
];
const PROPS = [
  'display', 'position', 'width', 'height', 'padding-top', 'padding-right',
  'padding-bottom', 'padding-left', 'margin-top', 'margin-right',
  'margin-bottom', 'margin-left', 'border-top-width', 'border-right-width',
  'border-bottom-width', 'border-left-width', 'border-top-left-radius',
  'background-color', 'color', 'font-family', 'font-size', 'font-weight',
  'line-height', 'letter-spacing', 'opacity', 'box-shadow', 'gap',
  'grid-template-columns', 'overflow-x', 'overflow-y',
];

function makeCaptureCopy() {
  let html = readFileSync(join(root, 'reference.html'), 'utf8');
  html = html.replace(
    'https://cdn.tailwindcss.com', './vendor/tailwind-cdn.js');
  html = html.replace(
    'https://unpkg.com/lucide@latest', './vendor/lucide-1.50.0.min.js');
  writeFileSync(join(root, 'capture.html'), html);
}

async function styleFor(page, selector, pseudo = null) {
  return page.evaluate(({ selector, pseudo, props }) => {
    const el = document.querySelector(selector);
    if (!el) return { selector, found: false };
    const cs = getComputedStyle(el, pseudo);
    const rect = el.getBoundingClientRect();
    const out = { selector, found: true, pseudo,
      rect: { x: rect.x, y: rect.y, w: rect.width, h: rect.height } };
    for (const p of props) out[p] = cs.getPropertyValue(p);
    return out;
  }, { selector, pseudo, props: PROPS });
}

async function main() {
  makeCaptureCopy();
  mkdirSync(join(root, 'captures'), { recursive: true });
  const browser = await puppeteer.launch({
    executablePath: '/usr/bin/chromium',
    args: ['--no-sandbox', '--force-device-scale-factor=1'],
  });
  const manifest = { viewports: [], states: {}, fonts: null, scrollbar: null,
    icons: {}, errors: [] };
  try {
    for (const vp of VIEWPORTS) {
      const page = await browser.newPage();
      await page.setViewport({ width: vp.width, height: vp.height,
        deviceScaleFactor: vp.dpr });
      const errors = [];
      page.on('pageerror', e => errors.push(String(e)));
      page.on('console', m => { if (m.type() === 'error') errors.push(m.text()); });
      await page.goto('file://' + join(root, 'capture.html'),
        { waitUntil: 'networkidle0', timeout: 60000 });
      await page.waitForFunction(
        () => document.querySelector('svg.lucide') !== null,
        { timeout: 30000 });
      await new Promise(r => setTimeout(r, 800));

      manifest.viewports.push({ ...vp });
      manifest.errors.push(...errors);

      if (vp.name === '1440x900') {
        // Fonts actually resolved.
        manifest.fonts = await page.evaluate(() => ({
          ui: getComputedStyle(document.body).fontFamily,
          mono: getComputedStyle(document.querySelector('#view-terminal')).fontFamily,
          loaded: [...document.fonts].map(f => `${f.family} ${f.status}`),
        }));
        manifest.scrollbar = await page.evaluate(() => ({
          thin: getComputedStyle(document.scrollingElement).scrollbarWidth,
        }));
        // Lucide DOM for every distinct icon name (1.50.0 generated output).
        manifest.icons = await page.evaluate(() => {
          const out = {};
          for (const el of document.querySelectorAll('svg.lucide')) {
            const cls = [...el.classList].find(c => c.startsWith('lucide-') && c !== 'lucide');
            if (cls && !out[cls]) out[cls] = el.outerHTML.slice(0, 2000);
          }
          return out;
        });
        const iconNames = await page.evaluate(() =>
          [...new Set([...document.querySelectorAll('i[data-lucide]')]
            .map(e => e.getAttribute('data-lucide')))].sort());
        manifest.iconNames = iconNames;

        const shot = async (state, actions = []) => {
          for (const a of actions) await a();
          await new Promise(r => setTimeout(r, 400));
          await page.screenshot({ path: join(root, 'captures', `${state}.png`) });
          const styles = [];
          for (const s of BASE_SELECTORS) styles.push(await styleFor(page, s));
          styles.push(await styleFor(page, '.right-tab.active', '::after'));
          manifest.states[state] = { viewport: vp.name, styles };
        };

        await shot('s-default');
        await shot('s-projects-hidden',
          [() => page.click('#toggleProjectsTop')]);
        await shot('s-projects-shown',
          [() => page.click('#showProjectsTop')]);
        await shot('s-inspector-files',
          [() => page.click('button[data-right="files"]')]);
        await shot('s-inspector-git',
          [() => page.click('button[data-right="git"]')]);
        await shot('s-diff-split',
          [() => page.click('button[data-view="diff"]')]);
        await shot('s-diff-inline',
          [() => page.click('#inlineDiffBtn')]);
        await shot('s-editor',
          [() => page.click('button[data-view="editor"]')]);
        // Hover states: pane toolbar + git actions + project card.
        await page.click('button[data-view="terminal"]');
        await page.hover('#pane1');
        await new Promise(r => setTimeout(r, 300));
        manifest.states['s-hover-pane-toolbar'] = { viewport: vp.name, styles: [
          await styleFor(page, '#pane1 .pane-toolbar'),
          await styleFor(page, '#pane1'),
        ]};
        await page.screenshot({ path: join(root, 'captures',
          's-hover-pane-toolbar.png') });
      } else {
        await page.screenshot({ path: join(root, 'captures',
          `s-default-${vp.name}.png`) });
      }
      await page.close();
    }
  } finally {
    await browser.close();
  }
  writeFileSync(join(root, 'computed-styles.json'),
    JSON.stringify(manifest, null, 1));
  const states = Object.keys(manifest.states);
  console.log(`states: ${states.join(', ')}`);
  console.log(`icons: ${(manifest.iconNames || []).join(', ')}`);
  console.log(`errors: ${manifest.errors.length}`);
}

main().catch(e => { console.error(e); process.exit(1); });
