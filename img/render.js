// Renders each src/*.html diagram to ../<name>.png at 2x, after web fonts load.
//   node render.js [name...]
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');
(async () => {
  const src = path.join(__dirname, 'src');
  const names = process.argv.slice(2).length ? process.argv.slice(2)
    : fs.readdirSync(src).filter(f => f.endsWith('.html')).map(f => f.slice(0, -5));
  const browser = await chromium.launch();
  const page = await browser.newPage({ deviceScaleFactor: 2, viewport: { width: 1500, height: 1000 } });
  for (const n of names) {
    await page.goto('file://' + path.join(src, n + '.html'));
    await page.evaluate(() => document.fonts.ready);
    const ok = await page.evaluate(() => document.fonts.check('16px Roboto'));
    if (!ok) throw new Error(n + ': Roboto did not load; refusing to render with a fallback font');
    await page.locator('svg').first().screenshot({ path: path.join(__dirname, n + '.png') });
    console.log('rendered', n);
  }
  await browser.close();
})().catch(e => { console.error(e.message); process.exit(1); });
