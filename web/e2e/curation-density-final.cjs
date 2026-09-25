// Read-only UI verification against a seeded local review curation.
// E2E_BASE_URL / E2E_CURATION_ID select the existing fixture; no research or cart writes.
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const { firefox, chromium } = require('playwright');
const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:18120';
const curation = process.env.E2E_CURATION_ID || '20e6101b-77a3-445c-8de3-4e0016597478';
const output = process.env.E2E_SCREENSHOT_DIR || '/tmp/vitlane-density-review';
const engine = process.env.E2E_BROWSER === 'chromium' ? chromium : firefox;

(async () => {
  assert.equal(new URL(base).hostname, '127.0.0.1');
  await fs.mkdir(output, { recursive: true });
  const browser = await engine.launch();
  const results = [];
  try {
    for (const locale of ['ko-KR', 'en-US']) {
      const context = await browser.newContext({ viewport: { width: 1365, height: 960 } });
      assert.equal((await context.request.post(`${base}/api/v1/dev/auth/session`, { data: { profileKey: 'empty-user' } })).status(), 201);
      // Change presentation in this browser only, preserving account preferences.
      await context.route('**/api/v1/me/preferences', async route => {
        assert.equal(route.request().method(), 'GET');
        const response = await route.fetch(), json = await response.json();
        json.effective.uiLocale = locale;
        await route.fulfill({ response, json });
      });
      const page = await context.newPage(), errors = [];
      page.on('pageerror', e => errors.push(e.message));
      await page.goto(`${base}/curations/${curation}`);
      await page.locator('.curation-candidate-card').first().waitFor();
      await page.waitForFunction(lang => document.documentElement.lang === lang, locale === 'ko-KR' ? 'ko' : 'en');
      const rail = page.locator('.phase8-candidate-rail__viewport').first();
      await page.locator('.phase8-target__header').first().evaluate(e => e.scrollIntoView({ block: 'start' }));
      const card = page.locator('.curation-candidate-card').first();
      const geometry = await card.evaluate(e => {
        const q = s => e.querySelector(s), rect = s => q(s).getBoundingClientRect().toJSON();
        return { height: e.getBoundingClientRect().height, title: rect('.vt-candidate-card__title'), titleStyle: getComputedStyle(q('.vt-candidate-card__title')).whiteSpace,
          ellipsis: getComputedStyle(q('.vt-candidate-card__title')).textOverflow, sourceWeight: getComputedStyle(q('.candidate-source-badge')).fontWeight,
          price: rect('.vt-candidate-card__price'), score: rect('.candidate-comparison__score'), axis: rect('.candidate-comparison__axis') };
      });
      assert.equal(geometry.titleStyle, 'nowrap'); assert.equal(geometry.ellipsis, 'ellipsis'); assert.equal(geometry.sourceWeight, '400');
      assert(geometry.score.top < geometry.price.bottom && geometry.axis.top >= geometry.price.bottom);
      const spacing = await page.evaluate(() => {
        const style = s => getComputedStyle(document.querySelector(s));
        return { target: style('.phase8-target__header').marginBottom, tags: style('.research-criteria').marginBottom,
          sort: style('.research-comparison-controls').marginBottom, controls: style('.phase8-candidate-rail__controls').position };
      });
      assert(Math.abs(parseFloat(spacing.target) - 5.6) < .1); assert(Math.abs(parseFloat(spacing.tags) - 2.4) < .1);
      assert(Math.abs(parseFloat(spacing.sort) - 8 / 3) < .1); assert.equal(spacing.controls, 'absolute');
      await page.screenshot({ path: `${output}/${locale}-cards.png` });
      // A genuine drag scrolls; releasing it must not open the underlying card.
      const box = await rail.boundingBox();
      await page.mouse.move(box.x + Math.min(650, box.width - 60), box.y + 70);
      await page.mouse.down();
      await page.mouse.move(box.x + 150, box.y + 72, { steps: 16 });
      await page.mouse.up();
      await page.waitForTimeout(200);
      assert(await rail.evaluate(e => e.scrollLeft) > 100, 'mouse drag scrolls the candidate rail');
      assert.equal(await page.getByRole('dialog').count(), 0, 'drag does not open a card');
      await rail.focus(); await page.keyboard.press('Home');
      assert(await rail.evaluate(e => e.scrollLeft) < 2);
      await page.locator('.phase8-candidate-rail__controls button').last().click();
      await page.waitForFunction(() => document.querySelector('.phase8-candidate-rail__viewport').scrollLeft > 10, undefined, { timeout: 5000 });
      // Home should run after the smooth button scroll has settled.
      await page.waitForTimeout(500);
      await rail.focus(); await page.keyboard.press('Home');
      await card.locator('.vt-candidate-card__hit-area').click();
      const modal = page.locator('.candidate-detail'); await modal.waitFor();
      assert.equal(await modal.locator('meter').count(), 0);
      assert.equal(await modal.locator('.candidate-detail__section .vt-disclosure__trigger').count(), 5);
      assert.equal(await modal.getByText(/옵션 다시 불러오기|Reload options|가중치|Weight \d|These scores include|추정을 포함/).count(), 0);
      const media = await modal.locator('.catalog-ui-candidate-modal__media').boundingBox();
      assert(Math.abs(media.width - media.height) < 1 && media.width > 230 && media.width < 255);
      const title = await modal.locator('h2').boundingBox(); assert(Math.abs(title.y - media.y) < 12);
      const cartColor = await modal.locator('.candidate-detail__cart-action').evaluate(e => getComputedStyle(e).backgroundColor);
      assert.equal(cartColor, 'rgb(80, 103, 216)');
      const accordions = modal.locator('.vt-disclosure__trigger');
      assert.equal(await modal.locator('.vt-disclosure__trigger[aria-expanded="false"]').count(), 0);
      await accordions.nth(2).click(); assert.equal(await accordions.nth(2).getAttribute('aria-expanded'), 'false');
      await accordions.nth(2).click(); assert.equal(await accordions.nth(2).getAttribute('aria-expanded'), 'true');
      await modal.locator('.candidate-detail__content').evaluate(e => e.scrollTop = 0);
      await page.screenshot({ path: `${output}/${locale}-detail.png` });
      const layouts = [];
      for (const theme of ['light', 'dark']) for (const width of [800, 390, 320]) for (const zoom of [1, 2]) {
        await page.setViewportSize({ width, height: 900 });
        await page.evaluate(t => document.documentElement.classList.toggle('dark', t === 'dark'), theme);
        await page.evaluate(z => document.documentElement.style.fontSize = `${16 * z}px`, zoom);
        await page.waitForTimeout(220);
        const sheet = await modal.evaluate(e => {
          const s = getComputedStyle(e), r = e.getBoundingClientRect();
          return { top: r.top, bottom: r.bottom, height: r.height, radius: s.borderTopLeftRadius, overflow: e.scrollWidth - e.clientWidth,
            contentOverflow: e.querySelector('.candidate-detail__content').scrollWidth - e.querySelector('.candidate-detail__content').clientWidth,
            backdrop: getComputedStyle(e.parentElement).backdropFilter };
        });
        assert(Math.abs(sheet.top - 90) < 2 && Math.abs(sheet.bottom - 900) < 2); assert(parseFloat(sheet.radius) > 0);
        assert(sheet.overflow <= 1 && sheet.contentOverflow <= 1, JSON.stringify({ width, zoom, sheet })); assert.equal(sheet.backdrop, 'none', 'a sheet appears without a scrim or blur (ADR-0086)');
        await modal.locator('.candidate-detail__content').evaluate(e => e.scrollTop = 0);
        await page.screenshot({ path: `${output}/${locale}-${theme}-${width}-${zoom}x.png` });
        const cart = modal.locator('.candidate-detail__cart-action');
        await cart.scrollIntoViewIfNeeded();
        const cartBox = await cart.boundingBox(); assert(cartBox.y >= 90 && cartBox.y + cartBox.height <= 901);
        layouts.push({ theme, width, zoom, ...sheet });
      }
      // Closing the dialog restores focus; tab traversal never escapes it.
      const firstControl = modal.locator('button:not(:disabled),a[href],input:not(:disabled),[tabindex="0"]').first();
      await firstControl.focus(); await page.keyboard.press('Shift+Tab');
      assert(await modal.evaluate(e => e.contains(document.activeElement)));
      await page.keyboard.press('Tab'); assert(await firstControl.evaluate(e => document.activeElement === e));
      await page.setViewportSize({ width: 1365, height: 480 });
      await page.evaluate(() => document.documentElement.style.fontSize = '16px');
      const shortSheet = await modal.boundingBox();
      assert(Math.abs(shortSheet.y - 48) < 2 && Math.abs(shortSheet.height - 432) < 2);
      await page.keyboard.press('Escape'); await modal.waitFor({ state: 'hidden' });
      assert(await card.locator('.vt-candidate-card__hit-area').evaluate(e => document.activeElement === e), 'focus returns to the card');
      await page.setViewportSize({ width: 320, height: 900 });
      const hamburger = page.locator('.shell-mobile-sidebar-trigger');
      const hamburgerStyle = await hamburger.evaluate(e => ({ border: getComputedStyle(e).borderTopWidth, shadow: getComputedStyle(e).boxShadow, background: getComputedStyle(e).backgroundColor }));
      assert.equal(hamburgerStyle.border, '0px'); assert.equal(hamburgerStyle.shadow, 'none'); assert.notEqual(hamburgerStyle.background, 'rgba(0, 0, 0, 0)');
      assert.deepEqual(errors, []);
      results.push({ locale, geometry, spacing, layouts, hamburgerStyle });
      await context.close();
    }
    await fs.writeFile(`${output}/results.json`, JSON.stringify({ status: 'PASS', results }, null, 2));
    console.log(JSON.stringify({ status: 'PASS', locales: results.length, sheetLayouts: results.reduce((n, r) => n + r.layouts.length, 0), output }));
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
