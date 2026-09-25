const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const { chromium } = require('playwright');
const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:18087';
const id = process.env.E2E_CURATION_ID || '20e6101b-77a3-445c-8de3-4e0016597478';
const output = process.env.E2E_SCREENSHOT_DIR || '/tmp/vitlane-density-polish';
(async () => {
  assert.equal(new URL(base).hostname, '127.0.0.1');
  await fs.mkdir(output, { recursive: true });
  const browser = await chromium.launch(), results = [];
  try {
    for (const locale of ['ko-KR', 'en-US']) {
      const context = await browser.newContext({ viewport: { width: 1365, height: 960 } });
      assert.equal((await context.request.post(`${base}/api/v1/dev/auth/session`, { data: { profileKey: 'empty-user' } })).status(), 201);
      await context.route('**/api/v1/me/preferences', async route => {
        assert.equal(route.request().method(), 'GET');
        const response = await route.fetch(), json = await response.json(); json.effective.uiLocale = locale;
        await route.fulfill({ response, json });
      });
      // A browser-only projection exercises every color without editing saved axes.
      await context.route('**/targets/*/criteria', async route => {
        assert.equal(route.request().method(), 'GET');
        const response = await route.fetch(), json = await response.json();
        json.axes = [1,2,3,4,5].map(importance => ({ ...json.axes[0], axisId: `visual-${importance}`, label: `Axis ${importance}`, importance }));
        await route.fulfill({ response, json });
      });
      const page = await context.newPage(); const errors = []; page.on('pageerror', e => errors.push(e.message));
      await page.goto(`${base}/curations/${id}`);
      await page.locator('.research-axis-trigger[data-importance="5"]').waitFor();
      for (const theme of ['light','dark']) {
        await page.evaluate(t => document.documentElement.classList.toggle('dark', t === 'dark'), theme);
        await page.waitForTimeout(300);
        const colors = await page.evaluate(() => {
          const rgb = color => { const c = document.createElement('canvas').getContext('2d'); c.fillStyle = color; c.fillRect(0,0,1,1); return [...c.getImageData(0,0,1,1).data].slice(0,3); };
          return { axes: [...document.querySelectorAll('.research-axis-trigger[data-importance]')].map(e => rgb(getComputedStyle(e).color)),
            sort: rgb(getComputedStyle(document.querySelector('.research-sort__trigger')).color), body: rgb(getComputedStyle(document.body).color) };
        });
        const brightness = c => c.reduce((sum, value) => sum + value, 0);
        assert.deepEqual(colors.axes[0], colors.axes[1]); assert.deepEqual(colors.axes[0], colors.sort);
        if (theme === 'light') {
          assert.deepEqual(colors.axes[4], [80,103,216]); assert(brightness(colors.axes[4]) > brightness(colors.axes[3]));
          assert(brightness(colors.axes[3]) > brightness(colors.axes[2])); assert(brightness(colors.sort) < 150);
        } else {
          assert(brightness(colors.axes[4]) < brightness(colors.axes[3]), JSON.stringify(colors)); assert(brightness(colors.axes[3]) < brightness(colors.axes[2]), JSON.stringify(colors));
          assert(brightness(colors.sort) > 700);
        }
        await page.locator('.phase8-target__header').first().evaluate(e => e.scrollIntoView({ block: 'start' }));
        await page.screenshot({ path: `${output}/${locale}-${theme}-axes.png` });
        const amazon = page.locator('.curation-candidate-card[data-source="AMAZON"]').first();
        await amazon.locator('.vt-candidate-card__hit-area').click();
        const modal = page.locator('.candidate-detail'); await modal.waitFor();
        const option = modal.locator('.catalog-ui-variant-row[aria-checked="true"]'); await option.waitFor(); await option.focus();
        const focus = await option.evaluate(e => ({ border: getComputedStyle(e).borderTopWidth, shadow: getComputedStyle(e).boxShadow, outline: getComputedStyle(e).outlineStyle,
          background: getComputedStyle(e).backgroundColor, base: getComputedStyle(e.closest('.candidate-detail')).backgroundColor }));
        assert.equal(focus.border, '0px'); assert.equal(focus.shadow, 'none'); assert.equal(focus.outline, 'none'); assert.notEqual(focus.background, focus.base);
        for (const width of [1365,800,320]) for (const zoom of [1,2]) {
          await page.setViewportSize({ width, height: 960 }); await page.evaluate(z => document.documentElement.style.fontSize = `${16*z}px`, zoom);
          const layout = await modal.locator('.catalog-ui-candidate-modal__footer-actions').evaluate(e => {
            const r = n => n.getBoundingClientRect().toJSON();
            return { save: r(e.querySelector('.candidate-detail__save-action')), buttons: [...e.querySelectorAll('.candidate-detail__external-actions button')].map(r), overflow: e.scrollWidth - e.clientWidth };
          });
          assert(Math.abs(layout.buttons[0].top - layout.buttons[1].top) < 1);
          assert(layout.buttons[0].left < layout.buttons[1].left && layout.save.bottom <= layout.buttons[0].top);
          assert(layout.buttons[0].right <= layout.buttons[1].left && layout.overflow <= 1);
          await modal.locator('.candidate-detail__external-actions').scrollIntoViewIfNeeded();
          const bounds = await modal.locator('.candidate-detail__external-actions').boundingBox(); assert(bounds.y >= 0 && bounds.y + bounds.height <= 961);
          await page.screenshot({ path: `${output}/${locale}-${theme}-${width}-${zoom}x-actions.png` });
          results.push({ locale, theme, width, zoom, colors, focus, layout });
        }
        await page.keyboard.press('Escape'); await modal.waitFor({ state: 'hidden' });
        await page.setViewportSize({ width: 1365, height: 960 }); await page.evaluate(() => document.documentElement.style.fontSize = '16px');
      }
      assert.deepEqual(errors, []); await context.close();
    }
    await fs.writeFile(`${output}/results.json`, JSON.stringify({ status: 'PASS', results }, null, 2));
    console.log(JSON.stringify({ status: 'PASS', layouts: results.length, output }));
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
