// R0 detailed measurement: component-level rects + styles + icon path diff.
// Usage: node measure.mjs
// Outputs: ../component-measures.json, ../icon-diff.json
import { readFileSync, writeFileSync } from 'node:fs';
import { dirname, join } from 'node:path';
import { fileURLToPath } from 'node:url';
import puppeteer from 'puppeteer-core';

const root = join(dirname(fileURLToPath(import.meta.url)), '..');
const PROPS = ['display', 'width', 'height', 'padding-top', 'padding-right',
  'padding-bottom', 'padding-left', 'margin-top', 'margin-right',
  'margin-bottom', 'margin-left', 'border-top-width', 'border-right-width',
  'border-bottom-width', 'border-left-width', 'border-top-left-radius',
  'border-top-right-radius', 'background-color', 'color', 'font-family',
  'font-size', 'font-weight', 'line-height', 'letter-spacing', 'opacity',
  'box-shadow', 'gap', 'grid-template-columns'];

const TARGETS = [
  '.project-item.active', '.project-item.active .project-name',
  '.project-item.active .project-meta', '.project-item.active .project-path',
  '#toggleProjectsTop', '#toggleProjectsTop svg',
  '.top-tab', '.top-tab.active', '.right-tab.active',
  '#inspector .pill', '#right-info .border',
  '.git-file', '.tree-row', '.tree-row.active',
  '.terminal-pane .h-8', '.terminal-pane .h-7',
  '.pane-toolbar > div', '.pane-toolbar button',
  '#toast', 'footer', 'footer button',
  '#diffStageBtn', '#splitDiffBtn', '#commitBtn',
  '.line', '.ln', '.diff-add', '.diff-del',
  '#inspectorResizer', '.kbd',
];

async function main() {
  const browser = await puppeteer.launch({
    executablePath: '/usr/bin/chromium', args: ['--no-sandbox'],
  });
  const out = { targets: {}, icons: {} };
  try {
    const page = await browser.newPage();
    await page.setViewport({ width: 1440, height: 900, deviceScaleFactor: 1 });
    await page.goto('file://' + join(root, 'capture.html'),
      { waitUntil: 'networkidle0', timeout: 60000 });
    await page.waitForFunction(
      () => document.querySelector('svg.lucide') !== null, { timeout: 30000 });
    await new Promise(r => setTimeout(r, 800));

    out.targets = await page.evaluate(({ targets, props }) => {
      const res = {};
      for (const sel of targets) {
        const el = document.querySelector(sel);
        if (!el) { res[sel] = { found: false }; continue; }
        const cs = getComputedStyle(el);
        const rect = el.getBoundingClientRect();
        const entry = { found: true,
          rect: { x: +rect.x.toFixed(2), y: +rect.y.toFixed(2),
            w: +rect.width.toFixed(2), h: +rect.height.toFixed(2) } };
        for (const p of props) entry[p] = cs.getPropertyValue(p);
        if (sel === '.right-tab.active') {
          const after = getComputedStyle(el, '::after');
          entry['::after'] = {
            left: after.getPropertyValue('left'),
            right: after.getPropertyValue('right'),
            bottom: after.getPropertyValue('bottom'),
            height: after.getPropertyValue('height'),
            background: after.getPropertyValue('background-color'),
          };
        }
        res[sel] = entry;
      }
      return res;
    }, { targets: TARGETS, props: PROPS });

    // Inner path data for every rendered lucide SVG (1.50.0).
    out.icons = await page.evaluate(() => {
      const res = {};
      for (const el of document.querySelectorAll('svg.lucide')) {
        const cls = [...el.classList].find(c => c.startsWith('lucide-') && c !== 'lucide');
        if (!cls || res[cls]) continue;
        const paths = [...el.querySelectorAll('path,circle,rect,line,polyline,polygon')]
          .map(n => n.outerHTML);
        res[cls] = {
          viewBox: el.getAttribute('viewBox'),
          fill: el.getAttribute('fill'),
          stroke: el.getAttribute('stroke'),
          strokeWidth: el.getAttribute('stroke-width'),
          children: paths,
        };
      }
      return res;
    });

    // Hover snapshot: project card + staged git row + top tab.
    await page.hover('.project-item:not(.active)');
    await new Promise(r => setTimeout(r, 300));
    out.hover = await page.evaluate(({ props }) => {
      const res = {};
      for (const sel of ['.project-item:not(.active)', '.top-tab:not(.active)']) {
        const el = document.querySelector(sel);
        if (!el) { res[sel] = { found: false }; continue; }
        const cs = getComputedStyle(el);
        const entry = { found: true };
        for (const p of props) entry[p] = cs.getPropertyValue(p);
        res[sel] = entry;
      }
      return res;
    }, { props: PROPS });
    await page.screenshot({ path: join(root, 'captures', 's-hover-card.png') });
    await page.close();
  } finally {
    await browser.close();
  }
  writeFileSync(join(root, 'component-measures.json'), JSON.stringify(out, null, 1));

  // Diff 1.50.0 rendered paths vs vendored 1.49.0 files.
  const { execSync } = await import('node:child_process');
  const names = JSON.parse(readFileSync(join(root, 'computed-styles.json'), 'utf8').iconNames || '[]');
  void execSync;
  const diff = {};
  const { readFileSync: rf } = await import('node:fs');
  for (const name of names) {
    try {
      const vendored = rf(
        `/home/fachri/Projects/personal/omaterm/apps/omaterm/assets/icons/${name}.svg`, 'utf8');
      diff[name] = { vendored: true };
    } catch {
      diff[name] = { vendored: false };
    }
  }
  // Compare inner children for vendored names present in render output.
  const mismatched = [];
  for (const [cls, info] of Object.entries(out.icons)) {
    const name = cls.replace(/^lucide-/, '');
    try {
      const v = rf(
        `/home/fachri/Projects/personal/omaterm/apps/omaterm/assets/icons/${name}.svg`, 'utf8');
      const vInner = [...v.matchAll(/<(path|circle|rect|line|polyline|polygon)[^>]*\/?>/g)]
        .map(m => m[0]).join('');
      const rInner = info.children.join('');
      const norm = s => s.replace(/\s+/g, ' ').replace(/"\/>/g, '" />');
      if (norm(vInner) !== norm(rInner)) mismatched.push(name);
    } catch { /* not vendored; recorded above */ }
  }
  writeFileSync(join(root, 'icon-diff.json'),
    JSON.stringify({ usedNames: names, vendoredMap: diff, mismatched150vs149: mismatched }, null, 1));
  console.log('mismatched 1.50-render vs 1.49-vendored:', JSON.stringify(mismatched));
  console.log('targets measured:', Object.keys(out.targets).length);
}

main().catch(e => { console.error(e); process.exit(1); });
