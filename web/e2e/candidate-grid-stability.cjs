// Real authenticated Curation route, synthetic API only. Firefox owns layout,
// scroll anchoring, focus restoration and the actual mouse/key/wheel events.
//
// A product group's candidates live in its sheet as a grid that flows down (ADR-0086). What must
// stay still is the reader's place in that sheet: late hydration and a poll reorder cards under
// them, a product's details open and close on top of the sheet, and none of it may move the list.
const assert = require('node:assert/strict');
const path = require('node:path');
const fs = require('node:fs/promises');
const os = require('node:os');
const { firefox } = require('playwright');
const { id, fixture, installAPI } = require('./fixtures/candidate-grid-stability.cjs');
const { openResults, closeResults } = require('./support/open-results.cjs');
const bodySelector = '.curation-target-sheet__body';
const gridSelector = '.curation-target-sheet .catalog-ui-candidate-grid';
const cellSelector = '.catalog-ui-candidate-cell';
const pause = page => page.waitForTimeout(350); // Covers the 180ms order animation plus layout/paint.

async function assertPosition(page, expected, message) {
  await pause(page);
  const actual = await page.locator(bodySelector).evaluate(n => n.scrollTop);
  assert(Math.abs(actual - expected) <= 1, `${message}: expected ${expected}, got ${actual}`);
}

async function interactions(page, state, locale) {
  const body = page.locator(bodySelector);
  const grid = page.locator(gridSelector);
  // The grid never scrolls sideways; the sheet scrolls down, by wheel and by keyboard focus alike.
  assert(await grid.evaluate(n => n.scrollWidth <= n.clientWidth + 1), 'the grid must not overflow sideways');
  const box = await body.boundingBox();
  await page.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await page.mouse.wheel(0, 420);
  await pause(page);
  const scrolled = await body.evaluate(n => n.scrollTop);
  assert(scrolled > 100, `the wheel scrolls the sheet: ${scrolled}`);

  // A product's details open on top of the sheet; closing them returns to the same place and the same card.
  const index = await body.evaluate(n => { const v = n.getBoundingClientRect(); return [...n.querySelectorAll('.vt-candidate-card__hit-area')].findIndex(card => { const r = card.getBoundingClientRect(); return r.top >= v.top && r.bottom <= v.bottom; }); });
  const hit = body.locator('.vt-candidate-card__hit-area').nth(index < 0 ? 0 : index);
  if (index < 0) await hit.scrollIntoViewIfNeeded();
  await pause(page);
  const beforeModal = await body.evaluate(n => n.scrollTop);
  await hit.click();
  await page.locator('.candidate-detail').waitFor();
  assert.equal(await page.locator('.curation-target-sheet').count(), 1, 'the sheet stays behind the details');
  await page.keyboard.press('Escape');
  await page.locator('.candidate-detail').waitFor({ state: 'detached' });
  assert.equal(await page.locator('.curation-target-sheet').count(), 1, 'Escape closes the top layer only');
  await assertPosition(page, beforeModal, 'closing the details preserves the place in the sheet');
  assert(await hit.evaluate(n => n === document.activeElement), 'focus returns to the card that opened the details');

  // A poll updates the same mounted grid, changes order and appends a result.
  await grid.evaluate(n => { n.dataset.refreshProbe = 'same-grid'; });
  const userPosition = await body.evaluate(n => n.scrollTop);
  const pool = state.catalogResearch.pools[0];
  state.curation.version++;
  pool.version++;
  pool.products.forEach((p, i) => { p.axisAssessment.totalScore = 90 - i; });
  const extra = structuredClone(pool.products[0]);
  extra.candidateId = 'appended-candidate'; extra.title = 'New headphones / 새 후보';
  extra.axisAssessment.totalScore = 100;
  pool.products.push(extra);
  await page.waitForFunction(({ selector, count }) => document.querySelector(selector)?.querySelectorAll('.catalog-ui-candidate-cell').length === count,
    { selector: gridSelector, count: pool.products.length });
  await assertPosition(page, userPosition, 'poll/reorder/append preserves the place in the sheet');
  assert.equal(await grid.getAttribute('data-refresh-probe'), 'same-grid', 'refresh retains the mounted grid');
  assert((await grid.locator(cellSelector).first().getAttribute('data-candidate-key')).includes('appended-candidate'));
  // The conversation follows: nothing was opened since the poll except the earlier details, which the reader chose.
  await closeResults(page);
  assert.equal(await page.locator('.curation-target-sheet').count(), 0);

  // A fresh route load starts with the conversation only; the sheet opens at its top again.
  await page.reload({ waitUntil: 'networkidle' });
  assert.equal(await page.locator('.curation-target-sheet').count(), 0, 'a reload never restores an open sheet');
  await openResults(page);
  await assertPosition(page, 0, 'a fresh sheet starts at the top');
  assert.equal(await page.locator('html').getAttribute('lang'), locale === 'ko-KR' ? 'ko' : 'en');
}

async function run() {
  const { createServer } = await import('vite');
  const root = path.resolve(__dirname, '..');
  // Dependencies can be symlinked to another checkout. Isolate Vite's cache and
  // allow its resolved module directory so fonts load in WSL as well as CI.
  const temp = await fs.mkdtemp(path.join(os.tmpdir(), 'vitlane-grid-'));
  const cacheDir = path.join(temp, 'node_modules', '.vite');
  const modules = await fs.realpath(path.join(root, 'node_modules'));
  const vite = await createServer({ root, cacheDir, configFile: path.join(root, 'vite.config.ts'),
    logLevel: 'error', server: { host: '127.0.0.1', port: 0, fs: { allow: [path.dirname(root), modules, cacheDir] } } });
  await vite.listen();
  const browser = await firefox.launch();
  const unexpected = [], errors = [], results = [];
  const cases = [
    { width: 1440, scale: 1, targets: 1 },
    { width: 900, scale: 1, targets: 1 },
    { width: 320, scale: 1, targets: 1 },
    { width: 1440, scale: 2, targets: 1 },
    { width: 320, scale: 2, targets: 1 },
    { width: 1440, scale: 1, targets: 2 },
  ];
  try {
    for (const locale of ['ko-KR', 'en-US']) for (const reducedMotion of ['no-preference', 'reduce']) for (const spec of cases) {
      const context = await browser.newContext({ viewport: { width: spec.width, height: 1000 }, reducedMotion });
      await context.addInitScript(({ locale, scale }) => {
        localStorage.setItem('vitlane.locale.v2', locale);
        window.sheetSamples = [];
        function sample() {
          if (document.documentElement) document.documentElement.style.fontSize = `${16 * scale}px`;
          const body = document.querySelector('.curation-target-sheet__body'), grid = body?.querySelector('.catalog-ui-candidate-grid');
          if (body && grid) {
            const state = { top: body.scrollTop, left: body.scrollLeft, max: body.scrollHeight - body.clientHeight, columns: grid.dataset.columnCount };
            if (JSON.stringify(window.sheetSamples.at(-1)) !== JSON.stringify(state)) window.sheetSamples.push(state);
          }
          requestAnimationFrame(sample);
        }
        requestAnimationFrame(sample);
      }, { locale, scale: spec.scale });
      const page = await context.newPage(); page.setDefaultTimeout(10000);
      page.setDefaultNavigationTimeout(60000);
      page.on('pageerror', e => errors.push(e.message));
      page.on('response', response => { if (response.status() >= 400) errors.push(`${response.status()} ${response.url()}`); });
      const state = fixture(12, spec.targets);
      let releaseHydration;
      const pendingHydration = new Promise(resolve => { releaseHydration = resolve; });
      await installAPI(page, state, unexpected, async () => { await pendingHydration; return state.catalogResearch; });
      await page.goto(`${vite.resolvedUrls.local[0]}curations/${id}`, { waitUntil: 'domcontentloaded' });
      await page.locator('[data-result-target]').first().waitFor();
      assert.equal(await page.locator('[data-result-target]').count(), spec.targets);
      await openResults(page);
      await page.locator(gridSelector).waitFor();
      await page.evaluate(() => document.fonts.ready);
      await pause(page);
      assert((await page.evaluate(() => window.sheetSamples)).every(s => s.top === 0), 'a new sheet starts at the top');
      // Delayed display hydration reverses Pick order after the first layout. Cards move to their new
      // places; the sheet must not follow any of them (scroll anchoring, focus, the order animation).
      for (const pool of state.catalogResearch.pools) pool.products.forEach((p, i) => { p.axisAssessment = {
        schemaVersion: 'vitlane.axis-assessment.v1', totalScore: i + 1, totalBasisPoints: (i + 1) * 100,
        criteria: { axes: [] }, scores: [], weights: [],
      }; });
      releaseHydration();
      await page.waitForFunction(selector => document.querySelector(selector)?.firstElementChild?.dataset.candidateKey.includes('candidate-11'), gridSelector);
      await pause(page);
      const samples = await page.evaluate(() => window.sheetSamples);
      assert(samples.every(s => Math.abs(s.top) <= 1), `entry/hydration auto-jump ${JSON.stringify({ locale, reducedMotion, spec, samples })}`);
      // Cards travel to their new places during the order animation; the sheet itself never moves sideways,
      // and once the cards have settled nothing is wider than the grid.
      assert(samples.every(s => s.left === 0), `the sheet moved sideways ${JSON.stringify({ locale, reducedMotion, spec, samples })}`);
      assert(await page.locator(gridSelector).evaluate(n => n.scrollWidth <= n.clientWidth + 1), 'the settled grid must not overflow sideways');
      assert(samples.at(-1).max > 0, 'fixture must exercise a sheet that scrolls');
      // The conversation shows the new leader once it is known.
      assert((await page.locator('[data-result-target]').first().getAttribute('data-representative')).includes('candidate-11'));
      if (spec.targets === 1 && spec.scale === 1 && spec.width !== 900) await interactions(page, state, locale);
      results.push({ locale, reducedMotion, ...spec });
      console.log(`PASS grid ${locale} ${reducedMotion} ${spec.width}px ${spec.scale * 100}% ${spec.targets} target(s)`);
      await context.close();
    }
    assert.deepEqual(unexpected, []); assert.deepEqual(errors, []);
    console.log(`PASS ${results.length} Firefox sheet entry/hydration cases, 8 scroll/details/refresh flows; no external calls`);
  } finally { await browser.close(); await vite.close(); await fs.rm(temp, { recursive: true, force: true }); }
}
run().catch(error => { console.error(error); process.exitCode = 1; });
