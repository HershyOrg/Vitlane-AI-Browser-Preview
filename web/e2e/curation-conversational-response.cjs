// Conversational response (ADR-0086) on a local review stack: stub model, stub Korean catalogs,
// a dedicated local DB. No live AI, no purchase. Run scripts/local-review.sh first.
const { firefox } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const { randomUUID } = require('node:crypto');
const base = (process.env.E2E_BASE_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const out = process.env.E2E_SCREENSHOT_DIR || '/tmp/vitlane-conversational-response';
if (!['localhost', '127.0.0.1'].includes(new URL(base).hostname)) throw Error('Runs only against a local review stack');
(async () => {
  const browser = await firefox.launch();
  const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  const page = await context.newPage(); global.__page = page;
  const errors = []; page.on('pageerror', e => errors.push(e.message));
  await fs.mkdir(out, { recursive: true });
  const api = async (method, path, data, expected = 200, headers = {}) => { const r = await context.request[method](base + path, { data, headers }); assert.equal(r.status(), expected, await r.text()); return r.json(); };
  await api('post', '/api/v1/dev/auth/session', { profileKey: 'empty-user' }, 201);
  const prefs = async (locale) => { const p = await api('get', '/api/v1/me/preferences'); await api('patch', '/api/v1/me/preferences', { schemaVersion: 'vitlane.user-preferences.v1', expectedVersion: p.preferences.version, uiLocale: locale, preferredCurrency: 'KRW', researchCountry: 'KR' }); };
  await prefs('ko-KR');
  const cap = await api('get', '/api/v1/managed-runner/capability');
  const created = await api('post', '/api/v1/shopping-plans', { originalIntent: '필기감 좋은 만년필, 출퇴근용 무선 이어폰', controlMode: 'AUTO', planningMode: 'AUTO', executionMode: 'EXPERIMENT', budget: { schemaVersion: 'vitlane.curation-budget.v1', inputMode: 'EXPLICIT', currency: 'KRW', totalAmount: null, allocationMode: 'EQUAL' }, location: { country: 'KR', city: '' }, category: '', allowedItems: [], blockedItems: [], referenceUrl: '', urlMode: 'NONE', agentMode: 'MANAGED', modelKey: cap.defaultModelKey }, 201, { 'Idempotency-Key': randomUUID() });
  const id = created.curation.id;
  await fs.writeFile(out + '/curation-id.txt', id);
  const root = `/api/v1/curations/${id}`;
  const threads = async () => (await api('get', root + '/threads')).threads;
  const wait = async (threadID) => { for (let n = 0; n < 240; n++) { const all = await threads(); const t = threadID ? all.find(t => t.id === threadID) : all[0]; if (t && ['SUCCEEDED', 'FAILED', 'CANCELLED', 'WAITING_SELECTION'].includes(t.status)) return t; await page.waitForTimeout(1000); } throw Error('thread timeout'); };
  const describe = t => `${t.status} ${t.actions.map(a => `${a.type}${a.instruction && a.type === 'RESPONSE' ? '(' + a.instruction + ')' : ''}:${a.status}${a.reasonCode ? ':' + a.reasonCode : ''}`).join(' > ')}`;
  const shot = async (target, name) => { await target.waitForTimeout(450); await target.screenshot({ path: `${out}/${name}.png` }); };
  const sheet = page.locator('.curation-target-sheet'), details = page.locator('.catalog-ui-candidate-modal');
  const shown = () => page.locator('[data-result-target]').first().evaluate(n => [n.querySelector('.curation-result__why')?.textContent, n.querySelector('.curation-result__name')?.textContent]);

  // 1. The research ends with one written reply.
  const first = await wait();
  console.log('first', describe(first));
  const reply = first.actions.find(a => a.type === 'RESPONSE');
  assert.equal(first.status, 'SUCCEEDED'); assert(reply?.response, 'the first research must end with a written reply');
  console.log('reply', `${reply.response.kind} | ${reply.response.body} | refs=${reply.response.references.map(r => r.ref + '→' + r.title).join(', ')}`);

  // 2. The conversation: a reading column, body text without a sender line, one row per product group with two places
  //    to click (the product, and the way into its list), and under them, on the left, the facts and then the record.
  await page.goto(base + '/curations/' + id);
  await page.getByRole('textbox', { name: '조사 요청', exact: true }).waitFor(); await page.waitForTimeout(2500);
  const conversation = await page.evaluate(() => {
    const main = document.querySelector('.curation-workspace__main'), style = getComputedStyle(main), box = main.getBoundingClientRect(), area = main.parentElement.getBoundingClientRect();
    const foot = document.querySelector('.curation-response__foot'), facts = foot?.querySelector('.curation-result__facts')?.getBoundingClientRect(), record = foot?.querySelector('.curation-response__link')?.getBoundingClientRect();
    const dock = document.querySelector('.catalog-ui-composer-dock'), dockStyle = getComputedStyle(dock);
    return { column: Math.round(box.width - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight)), left: Math.round(box.left - area.left), right: Math.round(area.right - box.right),
      rows: document.querySelectorAll('[data-result-target]').length, layout: document.querySelector('.curation-results__list')?.getAttribute('data-layout'), held: document.querySelectorAll('.curation-response__results [data-results-holder]').length,
      senderLines: document.querySelectorAll('.curation-response header, .curation-response svg.vt-brand-mark, .curation-thread__request header').length,
      boards: document.querySelectorAll('.catalog-ui-target, .catalog-ui-candidate-grid, .curation-target-sheet').length,
      footStacked: facts && record ? Math.abs(facts.left - record.left) <= 1 && record.top >= facts.bottom - 1 && record.right < box.left + box.width / 2 : false,
      listAreas: [...document.querySelectorAll('.curation-result__list')].map(n => [n.querySelectorAll('.curation-result__stack-item').length, n.querySelector('.curation-result__list-count')?.textContent ?? '', n.getAttribute('aria-label')]),
      all: (() => { const link = document.querySelector('.curation-results__all'), rows = [...document.querySelectorAll('[data-result-target]')], last = rows.at(-1).getBoundingClientRect(), r = link?.getBoundingClientRect(); return link ? { text: link.textContent, underLastRow: r.top >= last.bottom - 1, onTheRight: Math.abs(r.right - last.right) <= 2, aboveFoot: r.bottom <= document.querySelector('.curation-response__foot').getBoundingClientRect().top + 1, small: parseFloat(getComputedStyle(link).fontSize) < parseFloat(getComputedStyle(document.querySelector('.curation-result__name')).fontSize), accent: getComputedStyle(link).color === getComputedStyle(document.querySelector('.curation-result__why')).color } : null; })(),
      dock: { blur: dockStyle.backdropFilter, sameAsPage: dockStyle.backgroundColor === getComputedStyle(document.querySelector('.curation-workspace')).backgroundColor, divider: parseFloat(dockStyle.borderTopWidth) > 0, allCandidates: /전체 후보/.test(dock.textContent) },
      facts: foot?.querySelector('.curation-result__facts')?.textContent, reply: document.querySelector('[data-response-kind="COMMENT"]')?.textContent };
  });
  console.log('conversation', JSON.stringify(conversation));
  // 57.6rem at 16px (ADR-0089).
  assert.equal(conversation.column, 922); assert(Math.abs(conversation.left - conversation.right) <= 1, 'the reading column is centred');
  assert.deepEqual([conversation.rows, conversation.layout, conversation.held], [2, 'row', 1]);
  assert.equal(conversation.senderLines, 0, 'no logo or name on a reply, and the bubble carries the words only');
  assert.equal(conversation.boards, 0, 'the conversation holds no board'); assert(conversation.footStacked, 'the facts sit under the reply on the left and the record right under them');
  // The way into a group's list is a stack of the candidates behind the one shown; a group with one candidate has nothing behind it.
  assert.deepEqual(conversation.listAreas, [[1, '+1', '필기감 좋은 만년필 후보 2개 펼치기'], [0, '', '출퇴근용 무선 이어폰 후보 1개 펼치기']]);
  assert.deepEqual(conversation.all, { text: '전체 후보 보기', underLastRow: true, onTheRight: true, aboveFoot: true, small: true, accent: true }, 'every candidate at once: small accent words under the last row, on the right');
  assert.deepEqual(conversation.dock, { blur: 'none', sameAsPage: true, divider: true, allCandidates: false }, 'the composer is an opaque slab of the page colour behind a divider, with no all-candidates entry');
  assert(!conversation.reply.includes('[11번가]'), 'product names read short inside the sentence');
  assert.deepEqual(await shown(), ['Pick', '[11번가] [정품] 라미 사파리 만년필']);
  await shot(page, '01-conversation');
  const surface = selector => page.evaluate(selector => { const panel = document.querySelector(selector), box = panel.getBoundingClientRect(), layer = getComputedStyle(panel.parentElement); return { left: Math.round(box.left), right: Math.round(innerWidth - box.right), width: Math.round(box.width), height: Math.round(box.height), scrim: panel.parentElement.classList.contains('vt-scrim'), blur: layer.backdropFilter, paint: layer.backgroundColor }; }, selector);
  const clear = view => assert.deepEqual([view.scrim, view.blur, view.paint], [false, 'none', 'rgba(0, 0, 0, 0)'], 'a sheet appears over the page without a scrim');

  // 3. The product in a row opens that product's details at once: a side sheet, and no list in between.
  await page.locator('.curation-result__row').first().click(); await details.waitFor(); await page.waitForTimeout(350);
  const detailsView = await surface('.catalog-ui-candidate-modal');
  console.log('details', JSON.stringify(detailsView));
  assert.equal(await sheet.count(), 0, 'the list is not opened on the way to a product');
  assert.deepEqual([detailsView.right, detailsView.height], [0, 1000]); assert(detailsView.width < 800, 'the details are the narrower of the two sheets'); clear(detailsView);
  await shot(page, '02-details-side-sheet');
  await page.keyboard.press('Escape'); await details.waitFor({ state: 'detached' });
  assert.deepEqual(await shown(), ['Pick', '[11번가] [정품] 라미 사파리 만년필'], 'viewing the Pick keeps it the Pick');

  // 4. The right edge of a row opens the product group's list: a side sheet with tabs and a grid.
  await page.locator('.curation-result__list').first().click(); await sheet.waitFor(); await page.waitForTimeout(300);
  const sheetView = { ...await surface('.curation-target-sheet'), ...await page.evaluate(() => { const panel = document.querySelector('.curation-target-sheet'); return { columns: panel.querySelector('.catalog-ui-candidate-grid')?.getAttribute('data-column-count'), tabs: [...panel.querySelectorAll('[role="tab"]')].map(tab => tab.textContent), cards: panel.querySelectorAll('.vt-candidate-card').length, remove: panel.querySelector('.curation-target-sheet__remove')?.textContent }; }) };
  console.log('sheet', JSON.stringify(sheetView));
  assert.deepEqual([sheetView.right, sheetView.width, sheetView.height, sheetView.columns], [0, 800, 1000, '3']); assert.deepEqual(sheetView.tabs, ['전체3', '필기감 좋은 만년필2', '출퇴근용 무선 이어폰1']); assert.equal(sheetView.remove, '상품군 빼기'); clear(sheetView);
  await shot(page, '03-list-sheet');
  await sheet.locator('[role="tab"]').nth(2).click(); await page.waitForTimeout(400); await shot(page, '04-list-second-group');
  await sheet.locator('[role="tab"]').nth(1).click(); await page.waitForTimeout(300);

  // 5. A card's details open over the list and leave its edge in view; Escape closes one layer at a time, a drag by the
  //    side grabber closes a sheet with the mouse, and the last product opened stands for its group.
  await sheet.locator('.vt-candidate-card__hit-area').nth(1).click(); await details.waitFor(); await page.waitForTimeout(350);
  const stacked = await surface('.catalog-ui-candidate-modal');
  assert(stacked.left - sheetView.left >= 16, 'the list stays visible beside the details'); await shot(page, '05-details-over-list');
  await page.keyboard.press('Escape'); await details.waitFor({ state: 'detached' }); assert.equal(await sheet.count(), 1, 'Escape closes the details only');
  const grabber = await sheet.locator('.curation-side-grabber').boundingBox();
  await page.mouse.move(grabber.x + grabber.width / 2, grabber.y + grabber.height / 2); await page.mouse.down();
  await page.mouse.move(grabber.x + 140, grabber.y + grabber.height / 2, { steps: 6 }); await page.mouse.move(grabber.x + 460, grabber.y + grabber.height / 2, { steps: 8 }); await page.mouse.up();
  await sheet.waitFor({ state: 'detached' });
  assert.deepEqual(await shown(), ['마지막으로 본 상품', '[11번가] 라미 사파리 만년필']); await shot(page, '06-last-viewed');
  // A sort chosen afterwards hands the row to that sort's leader. The x closes the sheet.
  await page.locator('.curation-result__list').first().click(); await sheet.waitFor();
  await sheet.locator('.research-sort__trigger').click(); await page.getByRole('menuitemradio', { name: '낮은 가격순' }).click(); await page.waitForTimeout(300);
  await sheet.getByRole('button', { name: '닫기', exact: true }).click(); await sheet.waitFor({ state: 'detached' });
  assert.equal((await shown())[0], '최저가'); await shot(page, '07-sorted');
  // Taking a product group out lives at the end of its list, behind a confirmation.
  await page.locator('.curation-result__list').first().click(); await sheet.waitFor();
  await sheet.locator('.curation-target-sheet__remove').click(); await page.locator('[role="alertdialog"]').waitFor(); await shot(page, '08-remove-confirm');
  await page.getByRole('button', { name: '취소', exact: true }).click(); await page.locator('[role="alertdialog"]').waitFor({ state: 'detached' });
  assert.equal(await sheet.count(), 1); await page.keyboard.press('Escape'); await sheet.waitFor({ state: 'detached' });

  // 6. The facts, the record, and a product name inside the reply, which opens that product without its list.
  await page.locator('.curation-result__facts').first().click(); await page.waitForTimeout(300); await shot(page, '09-facts');
  await page.keyboard.press('Escape'); await page.locator('.curation-result__facts-popover').waitFor({ state: 'detached' });
  await page.getByRole('button', { name: '처리 내역', exact: true }).first().click(); await page.waitForTimeout(300); await shot(page, '10-record');
  assert.equal(await page.locator('.curation-response__record [data-action-id]').count(), 4);
  // A popover owns Escape until its 100 ms exit has finished; a person never presses the next key that fast.
  await page.keyboard.press('Escape'); await page.locator('.curation-response__record').waitFor({ state: 'detached' });
  await page.locator('.curation-response__text .curation-response__ref').first().click(); await details.waitFor();
  assert.equal(await sheet.count(), 0, 'a product named in the reply opens by itself');
  await page.keyboard.press('Escape'); await details.waitFor({ state: 'detached' });
  // "전체 후보 보기" opens the same sheet on its first tab: every group's candidates under a heading per group.
  assert.equal(await page.locator('.catalog-ui-composer-dock').getByRole('button', { name: /전체 후보/ }).count(), 0, 'the composer has no all-candidates entry');
  await page.locator('.curation-results__all').first().click(); await sheet.waitFor(); await page.waitForTimeout(300);
  const overview = await page.evaluate(() => { const panel = document.querySelector('.curation-target-sheet'); return { active: panel.getAttribute('data-target-sheet'), selected: [...panel.querySelectorAll('[role="tab"]')].map(tab => tab.getAttribute('aria-selected')), groups: [...panel.querySelectorAll('.curation-target-sheet__group-title')].map(n => n.textContent), cards: panel.querySelectorAll('.vt-candidate-card').length, tools: panel.querySelectorAll('.research-sort__trigger, .curation-target-sheet__remove').length }; });
  console.log('all tab', JSON.stringify(overview));
  assert.deepEqual(overview, { active: '*all', selected: ['true', 'false', 'false'], groups: ['필기감 좋은 만년필기준·정렬', '출퇴근용 무선 이어폰기준·정렬'], cards: 3, tools: 0 });
  await shot(page, '10b-all-tab');
  await sheet.locator('.curation-target-sheet__group-title').nth(1).click(); await page.waitForTimeout(300);
  assert.equal(await sheet.locator('[role="tab"][aria-selected="true"]').textContent(), '출퇴근용 무선 이어폰1', 'a group heading goes to that group\'s own tab');
  await sheet.getByRole('button', { name: '닫기', exact: true }).click(); await sheet.waitFor({ state: 'detached' });

  // 7. A question: answered from saved facts, nothing changes.
  const before = (await threads()).length; const budgetBefore = (await api('get', root + '/budget')).version;
  await page.getByRole('textbox', { name: '조사 요청', exact: true }).fill('둘 중 뭐가 더 가성비가 좋아?'); await page.getByRole('button', { name: '조사 요청 보내기' }).click();
  for (let n = 0; n < 120 && (await threads()).length === before; n++) await page.waitForTimeout(500);
  const answered = await wait((await threads())[0].id);
  console.log('question', describe(answered));
  const answer = answered.actions.find(a => a.type === 'RESPONSE');
  assert.equal(answered.actions.length, 2); assert.equal(answer?.response?.kind, 'ANSWER'); assert.equal((await api('get', root + '/budget')).version, budgetBefore);
  await page.waitForTimeout(2500); await shot(page, '11-answer');
  assert.equal(await page.locator('.curation-response__caption').count(), 2); assert.equal(await page.locator('.curation-response__results [data-results-holder]').count(), 1, 'the results stay with the response that found them');

  // 8. English; then a narrow window with a mouse, where both sheets rise from the bottom and their x must work.
  await prefs('en-US'); await page.reload(); await page.getByRole('textbox', { name: 'Research request', exact: true }).waitFor(); await page.waitForTimeout(2500); await shot(page, '12-en');
  assert.deepEqual([await page.locator('.curation-result__list').first().getAttribute('aria-label'), await page.locator('.curation-results__all').first().textContent()], ['Open all 2 candidates of 필기감 좋은 만년필', 'View all candidates']);
  await prefs('ko-KR'); await page.setViewportSize({ width: 430, height: 900 }); await page.reload(); await page.getByRole('textbox', { name: '조사 요청', exact: true }).waitFor(); await page.waitForTimeout(2500);
  await page.locator('.curation-result__list').first().click(); await sheet.waitFor(); await page.waitForTimeout(300);
  const narrow = await surface('.curation-target-sheet'); assert.equal(narrow.width, 430); assert(narrow.height < 900, 'the list is a bottom sheet here'); clear(narrow);
  await sheet.locator('.vt-candidate-card__hit-area').first().click(); await details.waitFor(); await page.waitForTimeout(350);
  await details.getByRole('button', { name: '상품 상세 닫기' }).click(); await details.waitFor({ state: 'detached' });
  await sheet.getByRole('button', { name: '닫기', exact: true }).click(); await sheet.waitFor({ state: 'detached' });
  // The header drags the bottom sheet away with the mouse, too.
  await page.locator('.curation-result__list').first().click(); await sheet.waitFor(); await page.waitForTimeout(300);
  const grab = await sheet.locator('.vt-sheet-grabber').boundingBox();
  await page.mouse.move(grab.x + grab.width / 2, grab.y + grab.height / 2); await page.mouse.down();
  await page.mouse.move(grab.x + grab.width / 2, grab.y + 160, { steps: 6 }); await page.mouse.move(grab.x + grab.width / 2, grab.y + 520, { steps: 8 }); await page.mouse.up();
  await sheet.waitFor({ state: 'detached' });
  await page.setViewportSize({ width: 1440, height: 1000 });

  // 9. A phone: bottom sheets, two deep; a row's product opens alone; both x buttons answer a tap.
  const phone = await browser.newContext({ viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, hasTouch: true, storageState: await context.storageState() });
  const mobile = await phone.newPage(); mobile.on('pageerror', e => errors.push('mobile: ' + e.message));
  const mobileSheet = mobile.locator('.curation-target-sheet'), mobileDetails = mobile.locator('.catalog-ui-candidate-modal');
  await mobile.goto(base + '/curations/' + id); await mobile.getByRole('textbox', { name: '조사 요청', exact: true }).waitFor(); await mobile.waitForTimeout(2500);
  await shot(mobile, '13-phone');
  await mobile.locator('.curation-result__row').first().tap(); await mobileDetails.waitFor(); assert.equal(await mobileSheet.count(), 0, 'on a phone, too, a product opens without its list');
  await mobileDetails.getByRole('button', { name: '상품 상세 닫기' }).tap(); await mobileDetails.waitFor({ state: 'detached' });
  await mobile.locator('.curation-result__list').first().tap(); await mobileSheet.waitFor(); await shot(mobile, '14-phone-list');
  await mobileSheet.locator('.vt-candidate-card__hit-area').first().tap(); await mobileDetails.waitFor(); await mobile.waitForTimeout(500); await shot(mobile, '15-phone-two-sheets');
  const layers = await mobile.evaluate(() => { const a = document.querySelector('.curation-target-sheet').getBoundingClientRect(), b = document.querySelector('.catalog-ui-candidate-modal').getBoundingClientRect(); return { sheetTop: Math.round(a.top), sheetWidth: Math.round(a.width), detailsTop: Math.round(b.top), detailsWidth: Math.round(b.width), overflow: document.documentElement.scrollWidth - innerWidth, scrims: document.querySelectorAll('.curation-target-sheet__backdrop.vt-scrim, .candidate-detail-backdrop.vt-scrim').length }; });
  console.log('phone layers', JSON.stringify(layers));
  assert(layers.detailsTop - layers.sheetTop >= 24, 'the product group\'s sheet stays visible above the details'); assert(layers.sheetWidth < layers.detailsWidth, 'the sheet steps back behind the details'); assert(layers.overflow <= 1); assert.equal(layers.scrims, 0);
  await mobileDetails.getByRole('button', { name: '상품 상세 닫기' }).tap(); await mobileDetails.waitFor({ state: 'detached' });
  await mobileSheet.getByRole('button', { name: '닫기', exact: true }).tap(); await mobileSheet.waitFor({ state: 'detached' });
  await phone.close();

  // 10. A US request with a two-word and a one-word product name. Both used to fail with
  //     CATALOG_SEARCH_PLAN_COMPILE_INVALID before any Shopify request was sent (ADR-0087). Vitlane saves no display
  //     facts of a Shopify product, so the reply's references arrive without a title and the page names them from
  //     what it has just read; the row opens that product's details, again alone.
  const usPrefs = await api('get', '/api/v1/me/preferences');
  await api('patch', '/api/v1/me/preferences', { schemaVersion: 'vitlane.user-preferences.v1', expectedVersion: usPrefs.preferences.version, uiLocale: 'ko-KR', preferredCurrency: 'USD', researchCountry: 'US' });
  const us = (await api('post', '/api/v1/shopping-plans', { originalIntent: 'camping chair, sunscreen', controlMode: 'AUTO', planningMode: 'AUTO', executionMode: 'EXPERIMENT', budget: { schemaVersion: 'vitlane.curation-budget.v1', inputMode: 'EXPLICIT', currency: 'USD', totalAmount: '100', allocationMode: 'EQUAL' }, location: { country: 'US', city: '' }, category: '', allowedItems: [], blockedItems: [], referenceUrl: '', urlMode: 'NONE', agentMode: 'MANAGED', modelKey: cap.defaultModelKey }, 201, { 'Idempotency-Key': randomUUID() })).curation.id;
  let usThread; for (let n = 0; n < 240; n++) { usThread = (await api('get', `/api/v1/curations/${us}/threads`)).threads[0]; if (usThread && ['SUCCEEDED', 'FAILED', 'CANCELLED'].includes(usThread.status)) break; await page.waitForTimeout(1000); }
  console.log('us', describe(usThread));
  const usReply = usThread.actions.find(a => a.type === 'RESPONSE')?.response;
  assert.equal(usThread.status, 'SUCCEEDED'); assert(usReply?.references.length > 0 && usReply.references.every(r => r.title === ''), 'a Shopify reference carries no saved title');
  await page.goto(base + '/curations/' + us); await page.getByRole('textbox', { name: '조사 요청', exact: true }).waitFor();
  const named = page.locator('.curation-response__text .curation-response__ref').first();
  await page.waitForFunction(() => { const link = document.querySelector('.curation-response__text .curation-response__ref'); return link && link.textContent && link.textContent !== '이 상품'; }, null, { timeout: 20000 });
  const usView = { link: await named.textContent(), full: await named.getAttribute('aria-label'), rows: await page.locator('[data-result-target]').evaluateAll(rows => rows.map(li => [li.querySelector('.curation-result__why')?.textContent, li.querySelector('.curation-result__name')?.textContent, li.querySelector('.candidate-comparison__value')?.textContent])) };
  console.log('us view', JSON.stringify(usView));
  assert(usView.full && usView.full.length > 0 && usView.rows.every(row => row[0] === 'Pick' && row[1]), 'every row shows its Pick by name');
  // The synthetic catalog answers every phrase with the same four products, so the two rows may show the same Pick;
  // within a group the products differ in name, price and score, which is what telling a swapped representative apart needs.
  await shot(page, '16-us-conversation');
  await page.locator('.curation-result__row').first().click(); await details.waitFor(); assert.equal(await sheet.count(), 0);
  assert.equal(await details.locator('h2').first().textContent(), usView.rows[0][1]); await shot(page, '17-us-details');
  await page.keyboard.press('Escape'); await details.waitFor({ state: 'detached' });
  // Another product viewed from the list takes the row; the lowest-price sort takes it back for its leader.
  await page.locator('.curation-result__list').first().click(); await sheet.waitFor(); await page.waitForTimeout(300);
  const cards = await sheet.locator('.vt-candidate-card__title').allTextContents(); const other = cards.findIndex(title => title !== usView.rows[0][1]);
  await sheet.locator('.vt-candidate-card__hit-area').nth(other).click(); await details.waitFor(); await page.keyboard.press('Escape'); await details.waitFor({ state: 'detached' });
  await sheet.getByRole('button', { name: '닫기', exact: true }).click(); await sheet.waitFor({ state: 'detached' });
  assert.deepEqual(await shown(), ['마지막으로 본 상품', cards[other]]); await shot(page, '18-us-last-viewed');
  assert.notEqual(cards[other], usView.rows[0][1], 'the products of a group differ, so the swap shows');
  await prefs('ko-KR');

  // 11. Those Shopify calls are in the operator's API ledger, under Shopify's own name.
  const operator = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
  const operatorSession = await operator.request.post(base + '/api/v1/dev/auth/session', { data: { profileKey: 'empty-operator' } }); assert.equal(operatorSession.status(), 201);
  const usage = await (await operator.request.get(base + '/api/v1/admin/catalog-apis')).json();
  const shopify = usage.apis.find(row => row.id === 'SHOPIFY_UCP');
  console.log('shopify ledger', JSON.stringify({ provider: shopify?.apiProvider, requests24h: shopify?.requests24h, enabled: shopify?.control?.enabled, failures24h: shopify?.failures24h }));
  assert(shopify && shopify.apiProvider === 'Shopify' && shopify.control.enabled && shopify.requests24h >= 3, 'the search and the price lookups of this run are counted');
  await operator.close();
  console.log('errors', JSON.stringify(errors)); assert.deepEqual(errors, []);
  await fs.writeFile(out + '/result.json', JSON.stringify({ id, checks: ['reply written once', 'reading column 48rem', 'no sender line', 'rows with a product and a thumbnail stack into the list', 'all-candidates link and the All tab', 'facts then record under the reply on the left', 'opaque composer without an all-candidates entry', 'row opens details alone as a side sheet', 'list side sheet with tabs and a grid', 'no scrim on either sheet', 'details over the list, Escape order, mouse drag', 'last viewed and sort change the representative', 'remove behind a confirmation', 'record popover', 'product name opens details alone', 'question answered without changes', 'en-US', 'narrow window: x and drag with a mouse', 'phone sheets two deep, x by tap, no overflow', 'US: one- and two-word product names researched, untitled Shopify references named by the page, swap visible', 'Shopify calls in the operator ledger'], conversation, details: detailsView, sheet: sheetView, layers, us: usView, errors }, null, 2));
  console.log('Conversational response Firefox E2E: PASS', id);
  await browser.close();
})().catch(async e => {
  console.error('Conversational response Firefox E2E: FAIL'); console.error(e); process.exitCode = 1;
  try {
    const page = global.__page;
    console.error('layers at failure', JSON.stringify(await page.evaluate(() => ({ dialogs: [...document.querySelectorAll('[role="dialog"],[role="alertdialog"]')].map(d => `${d.className.slice(0, 40)}|${d.querySelector('h2')?.textContent?.slice(0, 40)}`), poppers: document.querySelectorAll('[data-radix-popper-content-wrapper]').length, active: `${document.activeElement?.tagName}.${String(document.activeElement?.className).slice(0, 40)}` }))));
    await page.screenshot({ path: `${out}/failure.png` });
  } catch { /* the page may already be gone */ }
  process.exit(1);
});
