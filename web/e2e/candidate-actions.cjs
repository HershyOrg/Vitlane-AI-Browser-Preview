// Playwright Firefox + real components, local HTTP fixtures. DB guards have separate integration tests.
const { firefox } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const fixture = require('../../shared/openapi/fixtures/curation-step2.v1.json');
(async () => {
  const { createServer } = await import('vite');
  const root = path.resolve(__dirname, '..');
  const vite = await createServer({ root, configFile: path.join(root, 'vite.config.ts'), logLevel: 'error', server: { host: '127.0.0.1', port: 0, fs: { allow: [root, await fs.realpath(path.join(root, 'node_modules'))] } } });
  await vite.listen();
  const base = vite.resolvedUrls.local[0];
  const browser = await firefox.launch();
  const out = process.env.E2E_SCREENSHOT_DIR || '/tmp/vitlane-candidate-actions';
  const results = [];
  try {
    await fs.mkdir(out, { recursive: true });
    // ADR-0082: the Pick mark takes the palette's comparison color. Korean runs on Ink
    // (achromatic, so the mark may share the title color), English on Maple.
    for (const locale of ['ko-KR', 'en-US']) for (const theme of ['light', 'dark']) for (const size of [{ width: 1440, zoom: 1 }, { width: 320, zoom: 1 }, { width: 320, zoom: 2 }]) {
      const accent = locale === 'ko-KR' ? 'neutral' : 'maple';
      const context = await browser.newContext({ viewport: { width: size.width, height: 1000 } });
      await context.addInitScript(({ locale, theme, accent }) => { localStorage.setItem('vitlane.locale.v1', locale); document.cookie = `vt_locale_choice=${locale}; Path=/`; localStorage.setItem('vitlane.appearance.v2', JSON.stringify({ theme, accent })); }, { locale, theme, accent });
      let saved = { pinned: false, sentiment: 'NONE', version: 0 }; const writes = []; const errors = [];
      await context.route('**/api/v1/**', async route => {
        const request = route.request(); const p = new URL(request.url()).pathname;
        if (p.endsWith('/external-product/reaction')) {
          const body = request.postDataJSON(); assert.equal(body.expectedVersion, saved.version); assert.deepEqual(body.productRef, fixture.unknownObservation.productRef); assert.equal(body.variantId, undefined);
          saved = { pinned: body.pinned, sentiment: body.sentiment, version: saved.version + 1 }; writes.push(body);
          return route.fulfill({ json: { schemaVersion: 'vitlane.product-reaction.v1', reaction: saved } });
        }
        assert(p.endsWith('/external-product'), `Unexpected provider/Cart call: ${p}`);
        return route.fulfill({ json: { reactionAllowed: true, reaction: saved, purchaseFeedback: { version: 0, records: [] } } });
      });
      const page = await context.newPage(); page.on('pageerror', e => errors.push(e.message));
      await page.goto(base + 'e2e/fixtures/candidate-actions.html');
      const badge = page.locator('.candidate-ranking-badge.is-pick'); await badge.waitFor();
      await page.waitForFunction(theme => document.documentElement.dataset.theme === theme, theme);
      await page.evaluate(zoom => document.documentElement.style.fontSize = `${16 * zoom}px`, size.zoom);
      assert.equal(await page.evaluate(() => document.documentElement.classList.contains('dark')), theme === 'dark');
      assert.equal((await badge.textContent()).trim(), 'Pick'); assert.equal(await badge.locator('.vt-brand-mark__symbol').count(), 1);
      const mark = await badge.evaluate(e => ({ background: getComputedStyle(e, '::before').backgroundColor, blur: getComputedStyle(e, '::before').backdropFilter, color: getComputedStyle(e).color }));
      assert.notEqual(mark.background, 'rgba(0, 0, 0, 0)'); assert.match(mark.blur, /blur/);
      // The Pick mark keeps the comparison color; the score numerator reads like the title.
      const titleColor = await page.locator('.vt-candidate-card__title').evaluate(e => getComputedStyle(e).color);
      assert.equal(await page.locator('.candidate-comparison__value').evaluate(e => getComputedStyle(e).color), titleColor);
      assert.equal(await page.evaluate(() => document.documentElement.dataset.accent), accent);
      const channels = mark.color.match(/\d+/g).map(Number);
      if (accent === 'neutral') assert(Math.max(...channels) - Math.min(...channels) <= 16, `Ink comparison must be achromatic: ${mark.color}`);
      else assert.notEqual(mark.color, titleColor);
      if (size.width === 320) {
        const menu = page.locator('.shell-mobile-sidebar-trigger'); await menu.waitFor();
        const style = await menu.evaluate(e => ({ background: getComputedStyle(e).backgroundColor, canvas: getComputedStyle(document.body).backgroundColor, border: getComputedStyle(e).borderTopWidth, shadow: getComputedStyle(e).boxShadow }));
        assert.notEqual(style.background, 'rgba(0, 0, 0, 0)'); assert.equal(style.background, style.canvas); assert.equal(style.border, '0px'); assert.equal(style.shadow, 'none');
      }
      if (size.width === 320 && size.zoom === 1) await page.screenshot({ path: `${out}/${locale}-${theme}-card.png` });
      const open = async () => { await page.locator('.vt-candidate-card__hit-area').click(); await page.locator('.candidate-detail').waitFor(); await page.waitForFunction(() => document.querySelector('.candidate-detail').getAnimations().every(animation => animation.playState === 'finished')); };
      await open();
      const action = name => page.locator(`[data-reaction="${name}"]`);
      for (const name of ['pin', 'like', 'dislike']) {
        await action(name).click(); await page.mouse.move(0, 0);
        await page.waitForFunction(name => document.querySelector(`[data-reaction="${name}"]`)?.getAttribute('aria-pressed') === 'true', name);
        await page.waitForFunction(name => getComputedStyle(document.querySelector(`[data-reaction="${name}"]`)).backgroundColor === 'rgba(0, 0, 0, 0)', name);
        const s = await action(name).evaluate(e => ({ background: getComputedStyle(e).backgroundColor, color: getComputedStyle(e).color, iconColor: getComputedStyle(e.querySelector('svg')).color, fill: getComputedStyle(e.querySelector('svg')).fill }));
        assert.equal(s.background, 'rgba(0, 0, 0, 0)'); assert.equal(s.fill, s.iconColor);
        // The pin takes the comparison color, which is the text color on Ink; like/dislike keep their tone colors.
        if (!(accent === 'neutral' && name === 'pin')) assert.notEqual(s.iconColor, s.color);
      }
      assert.equal(await action('like').getAttribute('aria-pressed'), 'false');
      assert.equal(saved.pinned, true); assert.equal(saved.sentiment, 'DISLIKE');
      await page.keyboard.press('Escape'); await page.reload(); await badge.waitFor();
      await page.evaluate(zoom => document.documentElement.style.fontSize = `${16 * zoom}px`, size.zoom);
      assert.equal(await page.evaluate(() => getComputedStyle(document.documentElement).fontSize), `${16 * size.zoom}px`);
      await open();
      assert.equal(await action('pin').getAttribute('aria-pressed'), 'true'); assert.equal(await action('dislike').getAttribute('aria-pressed'), 'true');
      const box = await page.locator('.candidate-detail').evaluate(e => { const b = e.getBoundingClientRect(); return { x: b.x, right: b.right, top: b.top, radius: getComputedStyle(e).borderTopLeftRadius, overflow: e.querySelector('.candidate-detail__content').scrollWidth - e.querySelector('.candidate-detail__content').clientWidth }; });
      assert(box.x >= 0 && box.right <= size.width + 1 && box.overflow < 2, JSON.stringify(box));
      if (size.width === 320) { assert(box.top >= 90, JSON.stringify(box)); assert.notEqual(box.radius, '0px'); }
      await page.screenshot({ path: `${out}/${locale}-${theme}-${size.width}-${size.zoom}x.png` });
      await action('dislike').click(); await action('pin').click(); assert.equal(saved.sentiment, 'NONE'); assert.equal(saved.pinned, false);
      assert.equal(writes.length, 5); assert.deepEqual(errors, []);
      results.push({ locale, theme, ...size, box, mark }); await context.close();
      console.log('PASS', locale, theme, size.width, size.zoom);
    }
    await fs.writeFile(out + '/result.json', JSON.stringify({ scope: 'Local Firefox fixtures; production not exercised', results }, null, 2));
  } finally { await browser.close(); await vite.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
