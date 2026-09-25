// Drives the local review stack (stub model + stub catalogs) through a real
// Auto request and records how the request Thread is shown before, during and
// after, on desktop and at 320px. Local only; no external calls, no purchases.
const { firefox } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const { randomUUID } = require('node:crypto');
const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:18082';
const out = process.env.E2E_OUTPUT_DIR || '/tmp/vitlane-thread-ui';
if (!['127.0.0.1', 'localhost'].includes(new URL(base).hostname)) throw Error('Local review only');
(async () => {
  await fs.mkdir(out, { recursive: true });
  const browser = await firefox.launch();
  const c = await browser.newContext({ viewport: { width: 1280, height: 900 } });
  const page = await c.newPage(); page.setDefaultTimeout(20000);
  const errors = []; page.on('pageerror', e => errors.push(e.message));
  const get = async p => { const r = await c.request.get(base + p); assert.equal(r.status(), 200, await r.text()); return r.json(); };
  const observed = {}; const shots = [];
  const shot = async (name, opts = {}) => { const file = `${out}/${name}.png`; await page.screenshot({ path: file, fullPage: false, ...opts }); shots.push(name); };
  try {
    assert.equal((await c.request.post(base + '/api/v1/dev/auth/session', { data: { profileKey: 'empty-user' } })).status(), 201);
    const p = await get('/api/v1/me/preferences');
    assert.equal((await c.request.patch(base + '/api/v1/me/preferences', { data: { schemaVersion: 'vitlane.user-preferences.v1', expectedVersion: p.preferences.version, uiLocale: 'ko-KR', preferredCurrency: 'KRW', researchCountry: 'KR' } })).status(), 200);

    // 1. Before: home composer only, then a real Auto request.
    await page.goto(base + '/'); await page.locator('#curation-intent').waitFor();
    await page.locator('#curation-intent').fill('30만원 이내로 파란색 만년필 한 자루를 조사해줘');
    await page.locator('#curation-intent').press('Enter');
    await page.waitForURL(/\/curations\//); const id = new URL(page.url()).pathname.split('/').pop();
    await fs.writeFile(out + '/curation-id.txt', id);
    const root = `/api/v1/curations/${id}`;
    // 2. During: keep one screenshot per Thread status the UI shows.
    let thread; const seen = new Set();
    for (let n = 0; n < 240; n++) {
      const all = (await get(root + '/threads')).threads; thread = all[0];
      if (thread && !seen.has(thread.status)) {
        seen.add(thread.status);
        if (['INTERPRETING', 'RUNNING', 'WAITING_SELECTION'].includes(thread.status)) {
          await page.waitForTimeout(400);
          const bar = page.locator('.curation-thread');
          if (await bar.count()) { observed[thread.status] = { bar: await bar.innerText(), history: await page.locator('.curation-thread-history').count(), stopButtons: await page.getByRole('button', { name: /^(중단|요청 취소|이 행동 중단|취소)$/ }).count() }; await shot(`during-${thread.status.toLowerCase()}`); }
        }
      }
      if (thread && !['INTERPRETING', 'RUNNING', 'WAITING_SELECTION'].includes(thread.status)) break;
      await page.waitForTimeout(500);
    }
    assert.ok(thread, 'thread created');
    await fs.writeFile(out + '/initial-thread.json', JSON.stringify(thread, null, 2));
    await page.waitForTimeout(1500);
    // 3. After: no bar, one report in the conversation.
    await page.waitForFunction(() => document.querySelector('.curation-thread-report[data-thread-outcome]') && !document.querySelector('.curation-thread'));
    const report = page.locator('.curation-thread-report').last();
    observed.after = { outcome: await report.getAttribute('data-thread-outcome'), text: await report.innerText(), threadStatus: thread.status, reasonCode: thread.reasonCode || '', barCount: await page.locator('.curation-thread').count(), historyCount: await page.locator('.curation-thread-history').count(), vitlaneBubblesInReport: await report.locator('.is-vitlane').count(), disclosureBoxes: await report.locator('.vt-disclosure').count() };
    await report.scrollIntoViewIfNeeded(); await shot('after-report');
    await report.getByRole('button', { name: '자세히', exact: true }).click(); await page.waitForTimeout(300);
    observed.details = { actionRows: await report.locator('[data-action-id]').count(), text: await report.locator('.curation-thread-report__details').innerText() };
    await shot('after-report-details');
    await report.getByRole('button', { name: '자세히', exact: true }).click();

    // 4. A follow-up request: the stub asks which product when the reference is unclear.
    const composer = page.locator('.catalog-ui-focus-composer textarea');
    if (await composer.isEnabled()) {
      await composer.fill('더 저렴한 걸로 다시 찾아줘');
      await page.locator('.catalog-ui-focus-composer [type="submit"]').click();
      let follow;
      for (let n = 0; n < 240; n++) {
        const all = (await get(root + '/threads')).threads; follow = all.find(t => t.request === '더 저렴한 걸로 다시 찾아줘');
        if (follow && !seen.has('follow:' + follow.status)) {
          seen.add('follow:' + follow.status);
          if (['INTERPRETING', 'RUNNING', 'WAITING_SELECTION'].includes(follow.status)) {
            await page.waitForTimeout(400);
            if (await page.locator('.curation-thread').count()) {
              observed['follow-' + follow.status] = { bar: await page.locator('.curation-thread').innerText(), tailRequest: await page.locator('.catalog-ui-conversation-latest .curation-thread__request').count(), expanded: await page.locator('.curation-thread__toggle').getAttribute('aria-expanded') };
              await shot(`follow-${follow.status.toLowerCase()}`);
              if (follow.status === 'WAITING_SELECTION') {
                await page.setViewportSize({ width: 320, height: 720 }); await page.waitForTimeout(300);
                observed.mobileSelection = { scrollOk: await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1) };
                await shot('follow-selection-320');
                await page.setViewportSize({ width: 1280, height: 900 }); await page.waitForTimeout(300);
                const group = page.locator('.curation-thread__question');
                await group.getByRole('button').first().click();
              }
            }
          }
        }
        if (follow && !['INTERPRETING', 'RUNNING', 'WAITING_SELECTION'].includes(follow.status)) break;
        await page.waitForTimeout(500);
      }
      if (follow) { await fs.writeFile(out + '/follow-thread.json', JSON.stringify(follow, null, 2)); await page.waitForTimeout(1500); const reports = page.locator('.curation-thread-report[data-thread-outcome]'); observed.followAfter = { count: await reports.count(), last: await reports.last().innerText(), lastOutcome: await reports.last().getAttribute('data-thread-outcome'), orderOk: (await reports.first().innerText()).includes('만년필') }; await reports.last().scrollIntoViewIfNeeded(); await shot('follow-report'); }
    }
    // 5. 320px report layout.
    await page.setViewportSize({ width: 320, height: 720 }); await page.waitForTimeout(300);
    await page.locator('.curation-thread-report').last().scrollIntoViewIfNeeded();
    observed.mobileReport = { scrollOk: await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1) };
    await shot('after-report-320');
    observed.errors = errors;
    await fs.writeFile(out + '/result.json', JSON.stringify({ base, curationId: id, observed, shots }, null, 2));
    console.log(JSON.stringify(observed, null, 2));
    assert.deepEqual(errors, []);
    console.log('PASS thread UI on local stack');
  } catch (e) {
    await fs.writeFile(out + '/failure.json', JSON.stringify({ url: page.url(), observed, errors, body: await page.locator('body').innerText().catch(() => '') }, null, 2));
    await page.screenshot({ path: out + '/failure.png', fullPage: true }).catch(() => {});
    throw e;
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
