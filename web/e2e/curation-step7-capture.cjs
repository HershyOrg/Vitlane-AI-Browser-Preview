// Local UI regression: actual saved discoveries + the ordinary research review fixture.
// No paid AI calls or mutations to discoveries/subscriptions.
const { firefox } = require('playwright');
const fs = require('node:fs/promises');
const assert = require('node:assert/strict');
const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:18117';
const cid = 'e5700000-0000-4000-8000-000000000002';
const out = '/tmp/vitlane-step7-evidence';
if (!['localhost', '127.0.0.1'].includes(new URL(base).hostname)) throw Error('Local review only');
(async () => {
  const browser = await firefox.launch();
  const context = await browser.newContext({ storageState: '/tmp/vitlane-step7-session.json', viewport: { width: 1440, height: 1400 } });
  const page = await context.newPage();
  const errors = []; page.on('pageerror', e => errors.push(e.message));
  const matrix = [];
  const prefs = async locale => {
    const pref = await (await context.request.get(base + '/api/v1/me/preferences')).json();
    const r = await context.request.patch(base + '/api/v1/me/preferences', { data: { schemaVersion: 'vitlane.user-preferences.v1', expectedVersion: pref.preferences.version, uiLocale: locale, preferredCurrency: 'KRW', researchCountry: 'KR' } });
    assert.equal(r.status(), 200);
  };
  const capture = name => page.screenshot({ path: out + '/' + name + '.png', fullPage: true });
  const sheetFits = async () => {
    await page.locator('[role="dialog"]').evaluate(async el => { await Promise.all(el.getAnimations().map(animation => animation.finished.catch(() => {}))); });
    await page.waitForFunction(() => { const box = document.querySelector('[role="dialog"]')?.getBoundingClientRect(); return box && box.left >= -1 && box.right <= innerWidth + 1; });
    const result = await page.evaluate(() => {
      const dialog = document.querySelector('[role="dialog"]'); const box = dialog.getBoundingClientRect();
      const body = dialog.querySelector('.curation-target-sheet__body');
      return { width: innerWidth, page: document.documentElement.scrollWidth, left: box.left, right: box.right, top: box.top, bottom: box.bottom, viewportHeight: innerHeight, scroll: body.scrollWidth, client: body.clientWidth };
    });
    assert(result.page <= result.width + 1 && result.left >= -1 && result.right <= result.width + 1 && result.top >= -1 && result.bottom <= result.viewportHeight + 1 && result.scroll <= result.client + 1, JSON.stringify(result));
  };
  try {
    await fs.mkdir(out, { recursive: true });
    for (const locale of ['en-US', 'ko-KR']) {
      await prefs(locale); await page.setViewportSize({ width: 1440, height: 1400 });
      await page.goto(base + '/curations/' + cid);
      await page.locator('.curation-response__results [data-results-holder]').first().waitFor();
      await page.locator('[data-discovery-response]').first().waitFor();
      const controls = page.locator('.curation-background__controls');
      assert.equal(await controls.locator('button').count(), 2);
      assert.equal(await page.locator('.curation-workspace__main .curation-background__product').count(), 0);
      assert.equal(await page.locator('[data-result-target]').count(), 3);
      assert.equal(await page.locator('[data-response-kind="COMMENT"]').count(), 1);
      assert.equal(await page.locator('.curation-discovery-card').count(), 3);
      await capture(locale + '-conversation-mini-cards');
      // Already-added background product and ordinary candidates share the existing comparison sheet.
      await page.locator('.curation-results__all').click();
      const candidates = page.locator('.curation-target-sheet'); await candidates.waitFor(); await sheetFits();
      await candidates.getByText('실리콘 조리도구 5종 세트', { exact: true }).first().waitFor();
      assert((await candidates.innerText()).includes('바겐슈타이거'));
      assert.equal(await candidates.locator('.vt-candidate-card').count(), 5);
      await capture(locale + '-combined-candidates');
      await page.keyboard.press('Escape'); await candidates.waitFor({ state: 'hidden' });
      for (const [width, zoom] of [[1440, 1], [320, 1], [320, 2]]) {
        await page.setViewportSize({ width, height: width === 1440 ? 1400 : 900 });
        await page.evaluate(z => document.documentElement.style.fontSize = 16 * z + 'px', zoom);

        // Every discovery stays in one compact row; narrow screens scroll that row, never the page.
        const mini = await page.locator('.curation-discovery-cards').evaluateAll(rows => rows.map(row => ({
          page: document.documentElement.scrollWidth, viewport: innerWidth, width: row.clientWidth,
          cards: [...row.querySelectorAll('.curation-discovery-card')].map(card => { const box = card.getBoundingClientRect(); return { top: box.top, width: box.width, height: box.height, scroll: card.scrollWidth, client: card.clientWidth }; })
        })));
        for (const row of mini) { assert(row.page <= row.viewport + 1); assert(row.cards.every(card => Math.abs(card.top - row.cards[0].top) < 2 && card.width <= row.width + 1 && card.scroll <= card.client + 1), JSON.stringify(row)); }
        const card = page.locator('.curation-discovery-card').first();
        await card.click(); await page.locator('.curation-background__detail').waitFor(); await sheetFits();
        const actions = page.locator('.curation-background__actions');
        const link = actions.locator('a.vt-button--primary');
        assert.equal(await link.innerText(), locale === 'ko-KR' ? '링크 가기' : 'Open link');
        assert.equal(await link.getAttribute('target'), '_blank');
        assert.equal(await actions.locator('button.vt-button--secondary').innerText(), locale === 'ko-KR' ? '후보 추가' : 'Add to candidates');
        await link.scrollIntoViewIfNeeded();
        assert(await link.evaluate(el => { const box = el.getBoundingClientRect(); return box.top >= 0 && box.bottom <= innerHeight; }));
        await capture(locale + '-' + width + '-' + zoom + 'x-link-actions');
        await page.keyboard.press('Escape'); await page.getByRole('dialog').waitFor({ state: 'hidden' });
        assert(await card.evaluate(el => el === document.activeElement));
        await controls.locator('button').first().click();
        const dialog = page.getByRole('dialog'); await dialog.waitFor(); await page.waitForTimeout(250);
        await sheetFits();
        const rows = await dialog.locator('.curation-background__product').evaluateAll(items => items.map(el => {
          const r = el.getBoundingClientRect(), text = el.querySelector('strong').getBoundingClientRect();
          return { top: r.top, bottom: r.bottom, height: r.height, scroll: el.scrollHeight, client: el.clientHeight, textLeft: text.left, textRight: text.right, left: r.left, right: r.right, textWidth: text.width, display: getComputedStyle(el).display };
        }));
        assert(rows.length >= 3); assert(rows.every(r => r.display === 'grid' && r.height >= 65 && r.scroll <= r.client + 1 && r.textLeft >= r.left && r.textRight <= r.right), JSON.stringify(rows));
        if (width === 320) assert(rows.every(r => r.textWidth >= 180), JSON.stringify(rows));
        for (let i = 1; i < rows.length; i++) assert(rows[i].top >= rows[i - 1].bottom);
        await capture(locale + '-' + width + '-' + zoom + 'x-discoveries-revised');
        await dialog.locator('.curation-background__product').first().click();
        await page.locator('.curation-background__detail').waitFor(); assert.equal(await page.getByRole('dialog').count(), 1);
        await sheetFits();
        assert(await page.locator('[data-background-back]').evaluate(el => el === document.activeElement));
        await capture(locale + '-' + width + '-' + zoom + 'x-detail-revised');
        await page.locator('[data-background-back]').click(); await dialog.locator('.curation-background__product').first().waitFor();
        assert(await dialog.locator('.curation-background__product').first().evaluate(el => el === document.activeElement));
        await page.keyboard.press('Escape'); await page.getByRole('dialog').waitFor({ state: 'hidden' });
        assert(await controls.locator('button').first().evaluate(el => el === document.activeElement));
        await controls.locator('button').nth(1).click(); await page.getByRole('dialog').waitFor(); await sheetFits();
        await capture(locale + '-' + width + '-' + zoom + 'x-conditions-revised');
        await page.keyboard.press('Escape'); await page.getByRole('dialog').waitFor({ state: 'hidden' });
        matrix.push({ locale, width, zoom, rowCount: rows.length });
      }
      await page.evaluate(() => document.documentElement.style.fontSize = '16px');
    }
    assert.deepEqual(errors, []);
    await fs.writeFile(out + '/ui-revision-result.json', JSON.stringify({ matrix, errors, ordinaryCandidates: 4, addedLiveDiscovery: 1, discoveryDialogs: 1 }, null, 2));
    console.log('PASS mini cards, primary link/secondary add, combined candidates; ko/en desktop/320px/200%, focus and Escape');
  } finally { await prefs('ko-KR'); await browser.close(); }
})().catch(e => { console.error(e); process.exit(1); });
