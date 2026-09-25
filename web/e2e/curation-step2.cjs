const { firefox } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const fixture = require('../../shared/openapi/fixtures/curation-step2.v1.json');
const { openResults, closeResults } = require('./support/open-results.cjs');
let base = process.env.E2E_BASE_URL;
const screenshots = process.env.E2E_SCREENSHOT_DIR || '/tmp/vitlane-step2-browser';
async function run() {
 let vite;
 if (!base) {
  const { createServer } = await import('vite');
  const root = path.resolve(__dirname, '..');
  vite = await createServer({ root, configFile: path.join(root, 'vite.config.ts'), logLevel: 'error', server: { host: '127.0.0.1', port: 0 } });
  await vite.listen();
  base = vite.resolvedUrls.local[0].replace(/\/$/, '');
 }
 const browser = await firefox.launch({ headless: true });
 const evidence = [];
 try {
  await fs.mkdir(screenshots, { recursive: true });
  for (const locale of ['ko-KR', 'en-US']) {
   const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
   let prefs = { ...fixture.firstPreferences.effective, uiLocale: locale };
   let settings = { schemaVersion: 'vitlane.research-settings.v1', version: 0, country: 'KR' };
   const records = new Map(); const writes = []; const requests = []; const errors = [];
   let control = { enabled: true, version: 1, updatedAt: new Date().toISOString() };
   const usage = () => ({ schemaVersion: 'vitlane.catalog-api-usage.v4', id: 'OWN_PRODUCT', apiProvider: 'OpenWebNinja', apiProduct: 'Real-Time Product Search v2', quotaScope: 'realtime_product_search', configured: true, control, localRequestsPerMinute: 20, localDailyLimit: 30, requests24h: 4, notFound24h: 0, failures24h: [{ reasonCode: 'CATALOG_TIMEOUT', count: 1 }], estimatedRemaining: 93, quota: { limit: 100, remaining: 96, observedAt: new Date().toISOString(), resetAt: '2026-10-01T00:00:00Z' } });
   await context.route('**/api/v1/**', async route => {
    const req = route.request(); const url = new URL(req.url()); const p = url.pathname; requests.push({ path: p, method: req.method() });
    let data;
    if (req.method() !== 'GET') writes.push({ path: p, method: req.method(), body: req.postDataJSON() });
    if (p === '/api/v1/me') data = { user: { id: 'alice', status: 'ACTIVE', roles: ['USER'] } };
    else if (p.endsWith('/me/preferences')) { if (req.method() === 'PATCH') { const { schemaVersion, expectedVersion, ...selected } = req.postDataJSON(); if (expectedVersion !== prefs.version) return route.fulfill({ status: 409, json: { error: { code: 'PREFERENCE_VERSION_CONFLICT', message: 'Fixture preference version changed' } } }); prefs = { ...prefs, ...selected, version: prefs.version + 1 }; } data = { preferences: prefs, effective: prefs }; }
    else if (p.endsWith('/research-settings')) { if (req.method() === 'PATCH') { const b = req.postDataJSON(); assert.equal(b.expectedVersion, settings.version); settings = { ...settings, country: b.country, version: settings.version + 1 }; prefs = { ...prefs, researchCountry: b.country, version: prefs.version + 1 }; } data = settings; }
    else if (p.endsWith('/exchange-rate')) data = { schemaVersion: 'vitlane.exchange-rate.v1', status: 'STALE', rate: { base: 'USD', quote: 'KRW', rate: '1340.18', asOf: new Date(Date.now() - 86400000).toISOString().slice(0, 10), observedAt: new Date().toISOString(), source: 'https://frankfurter.dev/' } };
    else if (p.endsWith('/budget')) data = { schemaVersion: 'vitlane.curation-budget.v1', version: 0, researchVersion: 0, enabled: false, currency: 'KRW', totalAmount: null, allocations: [{ targetId:'target-1', quantity:1, amount:null }] };
    else if (p.endsWith('/criteria') && req.method() === 'GET') data = null;
    else if (p.endsWith('/cart')) data = { schemaVersion: 'vitlane.cart-view.v2', curationId: 'curation-1', version: 0, country: 'US', currency: 'USD', items: [] };
    else if (p.endsWith('/external-product/purchase-check')) { const b = req.postDataJSON(); const old = records.get(b.productRef.productId); assert.equal(b.expectedVersion, old?.version ?? 0); records.set(b.productRef.productId, { candidateId: p.match(/candidates\/([^/]+)\//)?.[1] ?? 'candidate-0', productRef: b.productRef, checked: b.checked, version: (old?.version ?? 0) + 1, evidence: 'SELF_REPORTED', recordedAt: new Date().toISOString() }); data = { schemaVersion: 'vitlane.external-purchase-feedback.v4', version: writes.length, records: [...records.values()] }; }
    else if (p.endsWith('/external-product')) data = { schemaVersion: 'vitlane.external-product-state.v1', purchaseFeedback: { schemaVersion: 'vitlane.external-purchase-feedback.v4', version: 0, records: [...records.values()] } };
    else if (p.endsWith('/admin/catalog-apis')) data = { schemaVersion: 'vitlane.catalog-api-list.v1', apis: [usage()] };
    else if (p.endsWith('/OWN_PRODUCT/control')) { const b = req.postDataJSON(); assert.equal(b.expectedVersion, control.version); control = { ...control, enabled: b.enabled, version: control.version + 1 }; data = usage(); }
    else if (p.endsWith('/OWN_PRODUCT/usage')) data = usage();
    else if (p.includes('managed-runner')) data = { enabled: true, serverExhausted: false, defaultModelKey: 'gpt-5.6-luna', models: [{ key: 'gpt-5.6-luna', label: 'Luna' }] };
    else throw new Error(`Unexpected API ${req.method()} ${p}`);
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify(data) });
   });
   const page = await context.newPage(); page.setDefaultTimeout(12000); page.on('pageerror', e => errors.push(e.message));
   await page.goto(`${base}/e2e/fixtures/curation-step2.html`);
   await page.getByText(fixture.observation.title, { exact: true }).first().waitFor();
   const offBudgetSegments = page.locator('.curation-budget__bar.is-unlimited .curation-budget__segment');
   assert.ok(await offBudgetSegments.count() > 0);
   assert.equal(await offBudgetSegments.evaluateAll(segments => segments.every(segment => segment.disabled)), true);
   assert.equal(await page.locator('.curation-budget__total-trigger').isDisabled(), false);
   await offBudgetSegments.first().evaluate(segment => segment.click());
   assert.equal(await page.locator('.budget-popover').count(), 0);
   const settingsTrigger = page.getByRole('button', { name: locale === 'ko-KR' ? '조사·보기 설정' : 'Research and display settings', exact: true });
   await settingsTrigger.waitFor();
   assert.equal(await page.locator('.catalog-ui-focus-composer__context').getByRole('button', { name: locale === 'ko-KR' ? '조사·보기 설정' : 'Research and display settings' }).count(), 1);
   await settingsTrigger.click();
   const country = page.getByLabel(locale === 'ko-KR' ? '다음 조사 국가' : 'Country for the next research', { exact: true });
   await country.selectOption('US');
   const display = page.getByLabel(locale === 'ko-KR' ? '보기 통화' : 'Display currency', { exact: true });
   await display.selectOption('USD');
   await page.keyboard.press('Escape');
   await page.getByText(locale === 'ko-KR' ? /약 130\.48\$/ : /Approx\. 130\.48\$/).first().waitFor();
   assert.equal(await page.getByText(fixture.observation.title, { exact: true }).count() > 0, true);
   assert.equal(writes.filter(w => w.path.endsWith('/research-settings')).length, 1);
   assert.equal(writes.some(w => /rounds|jobs|search$|cart$/.test(w.path)), false);
   assert.equal(requests.some(r => r.path.includes('hydration') || r.path.includes('variants')), false, 'product-only card must not trigger Shopify/Variant lookup');
   // The conversation shows the product group's representative; its cards live in the group's sheet.
   await openResults(page);
   await page.getByRole('button', { name: locale === 'ko-KR' ? '구매 체크' : 'Purchase check', exact: true }).first().click();
   const purchaseCheckDialog = page.getByRole('alertdialog');
   await purchaseCheckDialog.getByRole('button', { name: locale === 'ko-KR' ? '확인' : 'Confirm', exact: true }).click();
   await page.getByRole('button', { name: locale === 'ko-KR' ? '체크 취소' : 'Undo', exact: true }).first().waitFor();
   await page.getByRole('button', { name: locale === 'ko-KR' ? `${fixture.observation.title} 상세 보기` : `View ${fixture.observation.title}`, exact: true }).click();
   const dialog = page.locator('.catalog-ui-candidate-modal'); await dialog.waitFor();
   assert.equal(await dialog.getByText('GOOGLE_SHOPPING', { exact: true }).count(), 1);
   assert.equal(await dialog.getByText('8825648110', { exact: true }).count(), 1);
   assert.equal(await dialog.getByRole('button', { name: locale === 'ko-KR' ? '옵션 저장' : 'Save option', exact: true }).count(), 0);
   assert.equal(await dialog.getByRole('button', { name: /장바구니|Add to cart/ }).count(), 0);
   await page.screenshot({ path: path.join(screenshots, `${locale}-detail-desktop.png`), fullPage: true });
   await page.keyboard.press('Escape');
   // Every registry mall the Round can admit renders with its own label, its
   // price meaning and its own detail provenance, in both locales.
   const mallLabels = { ZIGZAG: ['Zigzag', '지그재그'], KURLY: ['Kurly', '컬리'], DAISOMALL: ['Daiso Mall', '다이소몰'], MUSINSA: ['Musinsa', '무신사'] };
   for (const observation of fixture.mallObservations) {
    const source = observation.productRef.source;
    const label = mallLabels[source][locale === 'ko-KR' ? 1 : 0];
    const card = page.locator(`.curation-candidate-card[data-source="${source}"]`);
    assert.equal(await card.count(), 1, `${source} card missing`);
    assert.equal(await card.locator('.candidate-source-badge').first().textContent(), label);
    // Prices render in the display currency the user picked, so the card is
    // checked for price meaning: an unknown price says so and never shows 0.
    const unavailable = locale === 'ko-KR' ? '가격 미확인' : 'Price unavailable';
    assert.equal(await card.getByText(unavailable, { exact: true }).count() > 0, observation.price.kind !== 'OBSERVED', `${source} price meaning`);
   }
   // Each mall uses the neutral merchant badge beside its name: the badge is loaded, 16px and
   // decorative, and the name is the same plain text for every mall in both themes — no mall colours.
   const badgeLooks = async locator => locator.evaluate(badge => {
    const logo = badge.querySelector('img.curation-mall-logo');
    return { look: `${getComputedStyle(badge).color} ${getComputedStyle(badge).fontWeight}`, logo: logo && { mall: logo.dataset.mall, alt: logo.getAttribute('alt'), width: Math.round(logo.getBoundingClientRect().width), loaded: logo.complete && logo.naturalWidth > 0 } };
   });
   let plainName;
   for (const dark of [false, true]) {
    await page.evaluate(on => document.documentElement.classList.toggle('dark', on), dark);
    const looks = new Set();
    for (const source of [fixture.observation, fixture.unknownObservation, ...fixture.mallObservations].map(observation => observation.productRef.source)) {
     const seen = await badgeLooks(page.locator(`.curation-candidate-card[data-source="${source}"] .candidate-source-badge`).first());
     assert.deepEqual(seen.logo, { mall: source, alt: '', width: 16, loaded: true }, `${source} ${dark ? 'dark' : 'light'} mall logo`);
     looks.add(seen.look);
    }
    assert.equal(looks.size, 1, `every mall name reads in one plain colour (${dark ? 'dark' : 'light'}): ${[...looks].join(' | ')}`);
    assert.ok([...looks][0].endsWith(' 400'), 'mall names are regular weight');
    if (!dark) plainName = [...looks][0];
   }
   await page.evaluate(() => document.documentElement.classList.remove('dark'));
   const mallCard = page.locator('.curation-candidate-card[data-source="KURLY"]');
   await mallCard.getByRole('button', { name: locale === 'ko-KR' ? '구매 체크' : 'Purchase check', exact: true }).click();
   await page.getByRole('alertdialog').getByRole('button', { name: locale === 'ko-KR' ? '확인' : 'Confirm', exact: true }).click();
   await mallCard.getByRole('button', { name: locale === 'ko-KR' ? '체크 취소' : 'Undo', exact: true }).waitFor();
   assert.equal(writes.filter(w => w.path.endsWith('/purchase-check') && w.body.productRef.source === 'KURLY').length, 1);
   await mallCard.getByRole('button', { name: locale === 'ko-KR' ? `${fixture.mallObservations[1].title} 상세 보기` : `View ${fixture.mallObservations[1].title}`, exact: true }).click();
   const mallDialog = page.locator('.catalog-ui-candidate-modal'); await mallDialog.waitFor();
   assert.equal(await mallDialog.getByText('KURLY_SEARCH', { exact: true }).count(), 1);
   const identity = await badgeLooks(mallDialog.locator('.catalog-ui-candidate-modal__identity > .candidate-source-badge'));
   assert.deepEqual([identity.logo?.mall, identity.logo?.loaded, identity.look], ['KURLY', true, plainName], 'the details name the mall by its logo and plain text');
   assert.equal(await mallDialog.getByText('5063110', { exact: true }).count(), 1);
   await page.keyboard.press('Escape');
   await mallDialog.waitFor({ state: 'detached' });
   // Narrow and enlarged: first the sheet itself, then the conversation and the composer without it.
   await page.setViewportSize({ width: 320, height: 900 });
   const sheetFit = () => ({ viewport: innerWidth, page: document.documentElement.scrollWidth, sheet: Math.round(document.querySelector('.curation-target-sheet__body').scrollWidth - document.querySelector('.curation-target-sheet__body').clientWidth) });
   for (const zoom of [1, 2]) {
    await page.evaluate(zoom => document.documentElement.style.fontSize = `${16 * zoom}px`, zoom);
    // The grid re-measures itself after the font size changes and the web font may still be arriving; a loaded CI
    // runner takes longer than a fixed pause. Wait for the settled layout (bounded), then hold it to the same rule.
    await page.evaluate(() => document.fonts.ready);
    // A column change starts the existing 180ms FLIP animation in a later layout effect.
    // One fitting frame can precede that animation. Require continuously fitting, idle geometry.
    await page.evaluate(() => { window.__sheetFitSince = undefined; });
    await page.waitForFunction(() => {
     const body = document.querySelector('.curation-target-sheet__body');
     const fits = document.documentElement.scrollWidth <= innerWidth + 1 && body.scrollWidth - body.clientWidth <= 1;
     const moving = body.getAnimations({ subtree: true }).some(animation => animation.playState === 'running');
     if (!fits || moving) { window.__sheetFitSince = undefined; return false; }
     window.__sheetFitSince ??= performance.now();
     return performance.now() - window.__sheetFitSince >= 250;
    }, null, { timeout: 3000 });
    const sheetWidth = await page.evaluate(sheetFit);
    assert.ok(sheetWidth.page <= sheetWidth.viewport + 1 && sheetWidth.sheet <= 1, `sheet overflow ${locale}/${zoom}: ${JSON.stringify(sheetWidth)} ${JSON.stringify(await page.evaluate(() => { const limit = innerWidth + 1; return [...document.querySelectorAll('body *')].filter(e => e.getBoundingClientRect().right > limit).slice(0, 8).map(e => ({ tag: e.tagName, text: (e.textContent || '').slice(0, 40), cls: String(e.className).slice(-50), right: Math.round(e.getBoundingClientRect().right), width: Math.round(e.getBoundingClientRect().width) })); }))}`);
    await page.screenshot({ path: path.join(screenshots, `${locale}-sheet-320-${zoom}x.png`) });
   }
   await page.evaluate(() => { document.documentElement.style.fontSize = ''; });
   await closeResults(page);
   for (const zoom of [1, 2]) {
    await page.setViewportSize({ width: 320, height: 900 });
    await page.evaluate(zoom => document.documentElement.style.fontSize = `${16 * zoom}px`, zoom);
    await settingsTrigger.click();
    await country.waitFor();
    await page.screenshot({ path: path.join(screenshots, `${locale}-320-${zoom}x.png`), fullPage: true });
    const width = await page.evaluate(() => ({ viewport: innerWidth, page: document.documentElement.scrollWidth }));
    assert.ok(width.page <= width.viewport + 1, `page overflow ${locale}/${zoom}: ${JSON.stringify(width)} ${JSON.stringify(await page.evaluate(() => [...document.querySelectorAll("body *")].filter(e => e.getBoundingClientRect().right > innerWidth + 1 && !e.closest(".phase8-product-rail")).map(e => ({ tag: e.tagName, cls: e.className, width: e.getBoundingClientRect().width, text: e.textContent.slice(0, 70) })).slice(0, 24)))}`);
    await page.keyboard.press('Escape');
   }
   await page.goto(`${base}/e2e/fixtures/curation-step2.html?mode=plan`);
   await page.getByRole('button', { name: locale === 'ko-KR' ? '조사·보기 설정' : 'Research and display settings', exact:true }).first().click();
   await page.locator('#shipping-country').waitFor();
   assert.equal(await page.locator('#shipping-country').inputValue(), 'US');
   assert.equal(await page.getByLabel(locale === 'ko-KR' ? '표시 통화' : 'Display currency', {exact:true}).inputValue(), 'USD');
   await page.goto(`${base}/e2e/fixtures/curation-step2.html?mode=operator`);
   const toggle = page.getByRole('switch'); await toggle.waitFor(); await toggle.click();
   await page.waitForFunction(() => document.querySelector('[role="switch"]')?.getAttribute('aria-checked') === 'false');
   assert.equal(writes.at(-1).body.schemaVersion, 'vitlane.catalog-api-control.v1');
   assert.equal(errors.length, 0, errors.join('\n'));
   evidence.push({ locale, checks: ['DB-shaped workspace observation', 'disabled No-limit budget segments', 'next-country CAS only', 'display currency only', 'no Variant/Shopify calls', 'product purchase check', 'provenance detail', 'registered mall text colors in light and dark', '320px at 100/200 percent text', 'Last Select seed', 'operator Off'], writes: writes.map(w => ({ path: w.path, method: w.method, body: w.body })) });
   await context.close();
  }
  await fs.writeFile(path.join(screenshots, 'result.json'), JSON.stringify({ scope: 'Firefox real components with contract fixtures; no live backend or model in this browser suite', evidence }, null, 2));
  console.log('Step 2 Firefox EN/KR matrix: PASS');
 } finally { await browser.close(); await vite?.close(); }
}
run().catch(e => { console.error(e); process.exitCode = 1; });
