// One-off: rasterize the SVG brand mark to PNG manifest icons.
// Chromium renders the SVG; screenshots at exact pixel sizes become the PNGs.
// Run from apps/web: node scripts/icons.mjs
import { chromium } from 'playwright';
import { fileURLToPath } from 'node:url';
import path from 'node:path';

const here = path.dirname(fileURLToPath(import.meta.url));
const svg = `file://${here}/../public/icon-512.svg`;

const browser = await chromium.launch();
try {
  for (const size of [192, 512, 180]) {
    // Top-level navigation: a file:// SVG document scales to the viewport,
    // unlike a file:// <img> inside about:blank, which Chromium blocks.
    const page = await browser.newPage({ viewport: { width: size, height: size } });
    await page.goto(svg);
    await page.waitForTimeout(300);
    const out =
      size === 180
        ? `${here}/../public/apple-touch-icon.png`
        : `${here}/../public/icon-${size}.png`;
    await page.screenshot({ path: out });
    console.log('saved', out);
    await page.close();
  }
} finally {
  await browser.close();
}
