// Second pass on the same local curation: a follow-up report, an ADD_TARGET
// request, then a request the stub cannot route (two researchable targets)
// so the real WAITING_SELECTION sheet is captured on desktop and at 320px.
const { firefox } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:18083';
const out = process.env.E2E_OUTPUT_DIR || '/tmp/vitlane-thread-ui';
if (!['127.0.0.1', 'localhost'].includes(new URL(base).hostname)) throw Error('Local review only');
(async () => {
  const id = (await fs.readFile(out + '/curation-id.txt', 'utf8')).trim();
  const browser = await firefox.launch();
  const c = await browser.newContext({ locale: 'ko-KR', viewport: { width: 1280, height: 900 } });
  const page = await c.newPage(); page.setDefaultTimeout(20000);
  const errors = []; page.on('pageerror', e => errors.push(e.message));
  const get = async p => { const r = await c.request.get(base + p); assert.equal(r.status(), 200, await r.text()); return r.json(); };
  const observed = {}; const shots = [];
  const shot = async name => { await page.screenshot({ path: `${out}/${name}.png` }); shots.push(name); };
  const root = `/api/v1/curations/${id}`;
  const threadOf = async text => (await get(root + '/threads')).threads.find(t => t.request === text);
  const active = s => ['INTERPRETING', 'RUNNING', 'WAITING_SELECTION'].includes(s);
  const submit = async text => { const box = page.locator('.catalog-ui-focus-composer textarea'); await page.waitForFunction(() => !document.querySelector('.catalog-ui-focus-composer textarea')?.disabled); await box.fill(text); await page.locator('.catalog-ui-focus-composer [type="submit"]').click(); };
  const settle = async text => { let t; for (let n = 0; n < 240; n++) { t = await threadOf(text); if (t && !active(t.status)) return t; await page.waitForTimeout(500); } throw Error('thread timeout: ' + text); };
  const reportFor = async text => { const r = page.locator('.curation-thread-report[data-thread-outcome]').filter({ has: page.locator('.curation-thread__request p', { hasText: new RegExp('^' + text + '$') }) }).last(); await r.waitFor(); return r; };
  try {
    assert.equal((await c.request.post(base + '/api/v1/dev/auth/session', { data: { profileKey: 'empty-user' } })).status(), 201);
    await page.goto(base + '/curations/' + id); await page.locator('.catalog-ui-focus-composer').waitFor();
    // 1. The earlier follow-up ended: its report sits after the first one, in time order.
    const follow = await reportFor('더 저렴한 걸로 다시 찾아줘');
    observed.followReport = { outcome: await follow.getAttribute('data-thread-outcome'), text: await follow.innerText() };
    const reports = page.locator('.curation-thread-report[data-thread-outcome]');
    observed.reportOrder = await reports.evaluateAll(list => list.map(el => el.querySelector('.curation-thread__request p')?.textContent));
    await follow.scrollIntoViewIfNeeded(); await shot('follow-report');
    // 2. Add a second product so the next unclear request needs a choice.
    await submit('파란 잉크도 추가해줘');
    const added = await settle('파란 잉크도 추가해줘');
    observed.addTarget = { status: added.status, reasonCode: added.reasonCode || '', actions: added.actions.map(a => `${a.type}:${a.status}`) };
    await page.waitForTimeout(2500);
    const addReport = await reportFor('파란 잉크도 추가해줘');
    observed.addReport = { outcome: await addReport.getAttribute('data-thread-outcome'), text: await addReport.innerText() };
    await addReport.scrollIntoViewIfNeeded(); await shot('add-report');
    // 3. Unclear repeat: the stub asks which product; the sheet opens itself.
    await submit('다시 찾아줘');
    let t; for (let n = 0; n < 240; n++) { t = await threadOf('다시 찾아줘'); if (t && t.status === 'WAITING_SELECTION') break; if (t && !active(t.status)) break; await page.waitForTimeout(500); }
    assert.equal(t.status, 'WAITING_SELECTION', JSON.stringify(t));
    await page.waitForTimeout(2500);
    const question = page.locator('.curation-thread__question'); await question.waitFor();
    observed.selection = { bar: await page.locator('.curation-thread__toggle').innerText(), expanded: await page.locator('.curation-thread__toggle').getAttribute('aria-expanded'), options: await question.getByRole('button').count(), composerLocked: await page.locator('.catalog-ui-focus-composer textarea').isDisabled(), stopButtons: await page.getByRole('button', { name: /^(중단|요청 취소|이 행동 중단|취소)$/ }).count(), tailRequest: await page.locator('.catalog-ui-conversation-latest .curation-thread__request').count() };
    await shot('selection-desktop');
    await page.setViewportSize({ width: 320, height: 720 }); await page.waitForTimeout(400);
    observed.selection320 = { scrollOk: await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), sheetVisible: await question.isVisible() };
    await shot('selection-320');
    await page.setViewportSize({ width: 1280, height: 900 }); await page.waitForTimeout(400);
    await question.getByRole('button').first().click();
    const resumed = await settle('다시 찾아줘');
    observed.resumed = { status: resumed.status, reasonCode: resumed.reasonCode || '', actions: resumed.actions.map(a => `${a.type}:${a.status}`) };
    await page.waitForTimeout(2500);
    const finalReport = await reportFor('다시 찾아줘');
    observed.finalReport = { outcome: await finalReport.getAttribute('data-thread-outcome'), text: await finalReport.innerText(), barCount: await page.locator('.curation-thread').count() };
    await finalReport.scrollIntoViewIfNeeded(); await shot('selection-report');
    observed.errors = errors;
    await fs.writeFile(out + '/result-2.json', JSON.stringify({ base, curationId: id, observed, shots }, null, 2));
    console.log(JSON.stringify(observed, null, 2));
    assert.deepEqual(errors, []);
    console.log('PASS selection flow on local stack');
  } catch (e) {
    await fs.writeFile(out + '/failure-2.json', JSON.stringify({ url: page.url(), observed, errors, body: await page.locator('body').innerText().catch(() => '') }, null, 2));
    await page.screenshot({ path: out + '/failure-2.png', fullPage: true }).catch(() => {});
    throw e;
  } finally { await browser.close(); }
})().catch(e => { console.error(e); process.exitCode = 1; });
