// Real local HTTP/DB/worker. Product observations are fixtures; AI mode is explicit.
const { firefox } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:18099';
const mode = process.env.E2E_AI_MODE;
const countries = (process.env.E2E_COUNTRIES || 'KR,US').split(',');
const profile = process.env.E2E_PROFILE || 'empty-user';
const out = process.env.E2E_SCREENSHOT_DIR || '/tmp/vitlane-axis-recovery';
if (process.env.E2E_SYNTHETIC !== '1' || !['stub', 'live'].includes(mode) || !['localhost', '127.0.0.1'].includes(new URL(base).hostname) || countries.some(c => !['KR', 'US'].includes(c))) {
  throw Error('Requires an isolated local fixture server, E2E_SYNTHETIC=1 and E2E_AI_MODE=stub|live');
}
(async () => {
  const browser = await firefox.launch();
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  const page = await context.newPage();
  const errors = [];
  page.on('pageerror', e => errors.push(e.message));
  page.setDefaultTimeout(20000);
  const get = async path => {
    const response = await context.request.get(base + path);
    assert.equal(response.status(), 200, path);
    return response.json();
  };
  let originalPreferences;
  try {
    await fs.mkdir(out, { recursive: true });
    assert.equal((await context.request.post(base + '/api/v1/dev/auth/session', { data: { profileKey: profile } })).status(), 201);
    originalPreferences = (await get('/api/v1/me/preferences')).preferences;
    const results = [];
    for (const country of countries) {
      const ko = country === 'KR';
      const current = (await get('/api/v1/me/preferences')).preferences;
      const locale = ko ? 'ko-KR' : 'en-US';
      if (current.uiLocale !== locale) {
        assert.equal((await context.request.patch(base + '/api/v1/me/preferences', { data: { schemaVersion: 'vitlane.user-preferences.v1', expectedVersion: current.version, uiLocale: locale } })).status(), 200);
      }
      await page.goto(base + '/');
      await page.locator('#curation-intent').waitFor();
      await page.getByRole('button', { name: ko ? '조사·보기 설정' : 'Research and display settings', exact: true }).click();
      const countrySelect = page.getByLabel(ko ? '조사 국가' : 'Research country', { exact: true });
      // KR must work with the actual default; the test must not silently force US.
      if (ko) assert.equal(await countrySelect.inputValue(), 'KR');
      else await countrySelect.selectOption('US');
      await page.keyboard.press('Escape');
      await page.locator('#curation-intent').fill(ko
        ? '라미 사파리 만년필을 부드러운 필기감, 클래식한 디자인, 휴대성 세 가지 기준으로 찾아줘.'
        : 'Find a fountain pen with smooth writing, classic design and portability.');
      const [response] = await Promise.all([
        page.waitForResponse(r => r.url().endsWith('/api/v1/shopping-plans') && r.request().method() === 'POST'),
        page.getByRole('button', { name: ko ? '상품 찾기 시작' : 'Start product search', exact: true }).click(),
      ]);
      assert.equal(response.status(), 201, await response.text());
      assert.equal(response.request().postDataJSON().location.country, country);
      const id = (await response.json()).curation.id;
      const read = () => get(`/api/v1/curations/${id}/workspace`);
      let workspace;
      for (let n = 0; n < 120; n++) {
        workspace = await read();
        const groups = workspace.research?.groups || [];
        if (!workspace.activeWork && groups.length && groups.every(g => ['RESULTS_READY', 'NO_RESULTS', 'FAILED'].includes(g.round?.status))) break;
        if (n % 15 === 0) console.log('Waiting', country, workspace.intelligence?.map(j => ({ status: j.status, failure: j.failureCode })));
        await page.waitForTimeout(1500);
      }
      assert(!workspace.activeWork, 'research timed out');
      assert(workspace.research.groups.every(g => g.round.status !== 'FAILED'), JSON.stringify(workspace.intelligence.map(j => ({ status: j.status, failure: j.failureCode }))));
      assert(workspace.intelligence.every(j => j.status !== 'FAILED'), 'A failed job must not be hidden by a later successful round: ' + JSON.stringify(workspace.intelligence.map(j => ({ status: j.status, failure: j.failureCode }))));
      const products = workspace.catalogResearch.pools.flatMap(pool => pool.products);
      assert(products.length > 0, `${country}: no candidates`);
      for (const product of products) {
        const assessment = product.axisAssessment;
        assert(assessment, 'assessment is missing');
        assert.equal(assessment.contentLocale, locale);
        assert(assessment.criteria.axes.length >= (mode === 'live' ? 2 : 1));
        if (mode === 'live') assert(assessment.criteria.axes.every(a => a.axisId !== 'stub-general'));
        assert.equal(assessment.weights.reduce((sum, weight) => sum + weight, 0), 100);
        assert.equal(assessment.scores.length, assessment.criteria.axes.length);
        assert.equal(assessment.totalScore, Math.round(assessment.scores.reduce((sum, score, i) => sum + score.scorePercent * assessment.weights[i], 0) / 100));
        assert(assessment.scores.every(s => s.explanation.trim() && s.scorePercent >= 0 && s.scorePercent <= 100));
      }
      const saved = JSON.stringify(products.map(p => ({ id: p.candidateId, assessment: p.axisAssessment })));
      await page.goto(base + '/curations/' + id);
      const cards = page.locator('.curation-candidate-card');
      await cards.first().waitFor();
      assert.equal(await cards.locator('.candidate-axis-details').count(), 0);
      const sort = page.getByRole('button', { name: ko ? '정렬' : 'Sort', exact: true }).first();
      await sort.click();
      const axis = products[0].axisAssessment.criteria.axes[0];
      await page.getByRole('menuitemradio', { name: axis.label, exact: true }).click();
      await cards.first().locator('.vt-candidate-card__hit-area').click();
      const detail = page.locator('.candidate-detail');
      await detail.waitFor();
      assert(await detail.locator('.candidate-axis-details__score').count() >= (mode === 'live' ? 2 : 1));
      assert.equal(await detail.locator('meter').count(), 0);
      assert.equal(await detail.locator('.vt-disclosure').count(), 5);
      await detail.locator('.candidate-axis-details').scrollIntoViewIfNeeded();
      await page.screenshot({ path: `${out}/${mode}-${country}-assessment.png` });
      await page.keyboard.press('Escape');
      await detail.waitFor({ state: 'hidden' });
      await page.setViewportSize({ width: 320, height: 900 });
      await page.evaluate(() => { document.documentElement.style.fontSize = '32px'; });
      assert(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1));
      await page.screenshot({ path: `${out}/${mode}-${country}-mobile.png` });
      await page.setViewportSize({ width: 1440, height: 1000 });
      const after = await read();
      assert.equal(JSON.stringify(after.catalogResearch.pools.flatMap(pool => pool.products).map(p => ({ id: p.candidateId, assessment: p.axisAssessment }))), saved);
      await fs.writeFile(`${out}/${mode}-${country}-workspace.json`, JSON.stringify(workspace, null, 2));
      results.push({ country, id, url: `${base}/curations/${id}`, count: products.length, axes: products[0].axisAssessment.criteria.axes.map(a => a.label), scores: products.map(p => p.axisAssessment.totalScore) });
      console.log('PASS', country, results.at(-1));
    }
    assert.deepEqual(errors, []);
    await fs.writeFile(`${out}/${mode}-result.json`, JSON.stringify({ aiMode: mode, products: 'saved observations', results, errors }, null, 2));
  } catch (error) {
    await page.screenshot({ path: `${out}/${mode}-failure.png`, fullPage: true });
    throw error;
  } finally {
    if (originalPreferences) {
      const current = (await get('/api/v1/me/preferences')).preferences;
      if (current.uiLocale !== originalPreferences.uiLocale || current.researchCountry !== originalPreferences.researchCountry) {
        assert.equal((await context.request.patch(base + '/api/v1/me/preferences', { data: { schemaVersion: 'vitlane.user-preferences.v1', expectedVersion: current.version, uiLocale: originalPreferences.uiLocale, researchCountry: originalPreferences.researchCountry } })).status(), 200);
      }
    }
    await browser.close();
  }
})().catch(error => { console.error(error.message); process.exitCode = 1; });
