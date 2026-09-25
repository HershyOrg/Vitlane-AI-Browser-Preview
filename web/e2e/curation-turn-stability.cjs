// Request turns hold still (ADR-0089) on a local review stack: stub model, stub Korean catalogs, a dedicated
// local DB. No live AI, no purchase. Run scripts/local-review.sh first, then
//   E2E_BASE_URL=http://127.0.0.1:<port> node e2e/curation-turn-stability.cjs
// Every 300ms while a request runs it records where the composer, the request bar and each turn stand, and
// fails if any of them moves: the composer and the bar keep their place and size, a turn keeps its place in
// the conversation, and a turn's rows are inside it from the start. A second request researches a product
// group again, so the first turn must keep its rows (D2) while the new turn gets its own.
const { firefox } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const { randomUUID } = require('node:crypto');
const base = (process.env.E2E_BASE_URL || 'http://127.0.0.1:18080').replace(/\/$/, '');
const out = process.env.E2E_SCREENSHOT_DIR || '/tmp/vitlane-turn-stability';
if (!['localhost', '127.0.0.1'].includes(new URL(base).hostname)) throw Error('Runs only against a local review stack');

const only = process.env.E2E_VIEWPORTS ? process.env.E2E_VIEWPORTS.split(',') : undefined;
const viewports = [
  { name: 'desktop', width: 1440, height: 1000 },
  { name: 'phone', width: 390, height: 844, phone: true },
].filter(viewport => !only || only.includes(viewport.name));

// Where everything stands, in the conversation's own coordinates for the turns (they scroll) and in the
// viewport's for the composer and the bar (they do not).
const probe = page => page.evaluate(() => {
  const scroller = document.querySelector('.shell-product-body');
  const scrollTop = scroller ? scroller.scrollTop : 0;
  const box = node => node ? node.getBoundingClientRect() : undefined;
  const dock = box(document.querySelector('.curation-composer-anchor > .catalog-ui-composer-dock'));
  const bar = box(document.querySelector('.catalog-ui-composer-dock > .curation-thread'));
  const turns = [...document.querySelectorAll('.curation-transcript > li.curation-transcript__row--thread')].map(li => {
    const id = li.querySelector('[data-thread-id]')?.getAttribute('data-thread-id');
    const rows = [...li.querySelectorAll('[data-result-target], [data-planning-target]')].map(row => row.getAttribute('data-result-target') ?? row.getAttribute('data-planning-target'));
    return { id, top: Math.round(li.getBoundingClientRect().top + scrollTop), rows };
  });
  const outside = [...document.querySelectorAll('[data-result-target], [data-planning-target]')].filter(row => !row.closest('li.curation-transcript__row--thread')).length;
  return { dock: dock && [Math.round(dock.top), Math.round(dock.height)], bar: bar && Math.round(bar.bottom), turns, outside };
});

const spread = values => values.length ? Math.max(...values) - Math.min(...values) : 0;

(async () => {
  const browser = await firefox.launch();
  await fs.mkdir(out, { recursive: true });
  try {
    for (const viewport of viewports) {
      const context = await browser.newContext({ viewport: { width: viewport.width, height: viewport.height }, hasTouch: Boolean(viewport.phone), locale: 'ko-KR' });
      const page = await context.newPage();
      const errors = []; page.on('pageerror', error => errors.push(error.message));
      const api = async (method, path, data, expected = 200, headers = {}) => { const r = await context.request[method](base + path, { data, headers }); assert.equal(r.status(), expected, await r.text()); return r.json(); };
      await api('post', '/api/v1/dev/auth/session', { profileKey: 'empty-user' }, 201);
      const preferences = await api('get', '/api/v1/me/preferences');
      await api('patch', '/api/v1/me/preferences', { schemaVersion: 'vitlane.user-preferences.v1', expectedVersion: preferences.preferences.version, uiLocale: 'ko-KR', preferredCurrency: 'KRW', researchCountry: 'KR' });
      const capability = await api('get', '/api/v1/managed-runner/capability');
      const created = await api('post', '/api/v1/shopping-plans', { originalIntent: '필기감 좋은 만년필, 출퇴근용 무선 이어폰', controlMode: 'AUTO', planningMode: 'AUTO', executionMode: 'EXPERIMENT', budget: { schemaVersion: 'vitlane.curation-budget.v1', inputMode: 'EXPLICIT', currency: 'KRW', totalAmount: null, allocationMode: 'EQUAL' }, location: { country: 'KR', city: '' }, category: '', allowedItems: [], blockedItems: [], referenceUrl: '', urlMode: 'NONE', agentMode: 'MANAGED', modelKey: capability.defaultModelKey }, 201, { 'Idempotency-Key': randomUUID() });
      const id = created.curation.id;
      const threads = async () => (await api('get', `/api/v1/curations/${id}/threads`)).threads;
      const settled = status => ['SUCCEEDED', 'FAILED', 'CANCELLED'].includes(status);
      const label = `${viewport.name}`;

      // Record until the request has ended and the page has had time to show it.
      const record = async (until) => {
        const frames = []; let endedAt;
        for (let n = 0; n < 400; n++) {
          frames.push(await probe(page));
          if (endedAt === undefined && await until()) endedAt = frames.length;
          if (endedAt !== undefined && frames.length - endedAt >= 8) return frames;
          await page.waitForTimeout(300);
        }
        throw Error(`${label}: the request did not end`);
      };
      const assertStill = (frames, what) => {
        const docked = frames.filter(frame => frame.dock);
        assert(docked.length > 5, `${label} ${what}: the composer was recorded`);
        assert(spread(docked.map(frame => frame.dock[0])) <= 1 && spread(docked.map(frame => frame.dock[1])) <= 1, `${label} ${what}: the composer keeps its place and size ${JSON.stringify(docked.map(frame => frame.dock))}`);
        const barred = frames.filter(frame => frame.bar !== undefined && frame.bar !== null);
        assert(spread(barred.map(frame => frame.bar)) <= 1, `${label} ${what}: the request bar keeps its place ${JSON.stringify(barred.map(frame => frame.bar))}`);
        for (const frame of barred) assert(frame.bar <= frame.dock[0], `${label} ${what}: the bar stands above the composer`);
        const ids = [...new Set(frames.flatMap(frame => frame.turns.map(turn => turn.id)))];
        for (const turnId of ids) {
          const tops = frames.flatMap(frame => frame.turns.filter(turn => turn.id === turnId).map(turn => turn.top));
          assert(spread(tops) <= 1, `${label} ${what}: turn ${turnId} keeps its place ${JSON.stringify(tops)}`);
        }
        const withTurn = frames.filter(frame => frame.turns.length > 0);
        assert(withTurn.every(frame => frame.outside === 0), `${label} ${what}: every row stands inside its request's turn ${JSON.stringify(withTurn.map(frame => frame.outside))}`);
        return ids;
      };

      // 1. The first request, from the plan to the reply.
      await page.goto(`${base}/curations/${id}`);
      await page.locator('.curation-composer-anchor > .catalog-ui-composer-dock').waitFor();
      const first = await record(async () => settled((await threads())[0]?.status));
      const firstIds = assertStill(first, 'first request');
      assert.equal(firstIds.length, 1, `${label}: one request is one turn`);
      const firstId = firstIds[0];
      assert(first.some(frame => frame.turns[0]?.rows.length > 0 && frame.bar), `${label}: the rows are in the turn while the request still runs`);
      await page.screenshot({ path: `${out}/${label}-01-first-turn.png` });

      const reading = await page.evaluate(firstId => {
        const turn = document.querySelector(`[data-thread-id="${firstId}"]`);
        const request = turn.querySelector('.curation-thread__request').getBoundingClientRect(), reply = turn.querySelector('.curation-thread-report__reply').getBoundingClientRect();
        // A stub research can come back empty for one group; measure a row that shows a product.
        const row = turn.querySelector('button.curation-result__row'); if (!row) return { empty: true };
        const name = row.querySelector('.curation-result__name').getBoundingClientRect(), logo = row.querySelector('.curation-mall-logo');
        const main = document.querySelector('.curation-workspace__main'), style = getComputedStyle(main);
        return {
          inTurn: Math.round(reply.top - request.bottom),
          text: getComputedStyle(turn.querySelector('.curation-response__text, .curation-thread-report__headline')).fontSize,
          column: Math.round(main.getBoundingClientRect().width - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight)),
          rowHeight: Math.round(row.getBoundingClientRect().height),
          logo: logo && { gap: Math.round(logo.getBoundingClientRect().left - name.right), size: Math.round(logo.getBoundingClientRect().width), alt: logo.getAttribute('alt') },
          score: row.querySelectorAll('.candidate-comparison__score, .curation-result__unscored').length,
          list: Math.round(row.closest('li').querySelector('.curation-result__list').getBoundingClientRect().width),
          // The way into the list starts where the product's button ends: no strip between them belongs to neither.
          listGap: Math.round(row.closest('li').querySelector('.curation-result__list').getBoundingClientRect().left - row.getBoundingClientRect().right),
        };
      }, firstId);
      console.log(label, 'reading', JSON.stringify(reading));
      assert(!reading.empty, `${label}: the first request found at least one product (stub catalogs)`);
      assert(Math.abs(reading.inTurn - 32) <= 1, `${label}: the reply stands 32px under its request`);
      assert.equal(reading.text, '17.6px', `${label}: the conversation reads 10% larger`);
      if (!viewport.phone) assert.equal(reading.column, 922, 'the reading column is 57.6rem');
      assert(reading.rowHeight >= 74, `${label}: a row is at least 4.625rem tall`);
      assert.deepEqual([reading.logo?.gap, reading.logo?.size, Boolean(reading.logo?.alt), reading.score], [8, 16, true, 0], `${label}: the mall's logo right after the name, and no score in a row`);
      assert(reading.list >= (viewport.phone ? 96 : 192), `${label}: the way into the list is a wide target (${reading.list}px)`);
      assert.equal(reading.listGap, 0, `${label}: the list target starts where the product's button ends`);

      // 2. A second request that asks which product group to research again. While the question waits, the
      //    conversation keeps room for the open bar: its last words can still be read above it.
      const box = page.getByRole('textbox', { name: '조사 요청', exact: true });
      await box.fill('다시 찾아줘'); await box.press('Enter');
      await page.locator('.curation-thread__question').waitFor({ timeout: 60000 });
      await page.waitForTimeout(600);
      const room = await page.evaluate(firstId => {
        const scroller = document.querySelector('.shell-product-body'); scroller.scrollTop = scroller.scrollHeight;
        const foot = document.querySelector(`[data-thread-id="${firstId}"] .curation-response__foot`).getBoundingClientRect();
        return { foot: Math.round(foot.bottom), bar: Math.round(document.querySelector('.catalog-ui-composer-dock > .curation-thread').getBoundingClientRect().top) };
      }, firstId);
      assert(room.foot <= room.bar, `${label}: the last words of the conversation scroll clear of the open bar ${JSON.stringify(room)}`);
      await page.screenshot({ path: `${out}/${label}-02-question.png` });
      const option = page.locator('.curation-thread__question button').first();
      const chosen = (await option.textContent()).trim();
      await option.click();
      const second = await record(async () => { const all = await threads(); return all.length > 1 && all.every(thread => settled(thread.status)); });
      const ids = assertStill(second, 'second request');
      assert.equal(ids.length, 2, `${label}: the second request is a second turn`);
      const last = second.at(-1);
      const [earlier, later] = [last.turns.find(turn => turn.id === firstId), last.turns.find(turn => turn.id !== firstId)];
      assert.equal(earlier.rows.length, 2, `${label}: the first turn keeps both of its rows after one group is researched again (D2)`);
      assert.equal(later.rows.length, 1, `${label}: the new turn holds the group it researched (${chosen})`);
      assert(earlier.rows.includes(later.rows[0]), `${label}: the same product group stands in both turns`);
      const between = await page.evaluate(() => {
        const turns = [...document.querySelectorAll('.curation-transcript > li.curation-transcript__row--thread')];
        let deepest = { bottom: -Infinity };
        for (const node of turns[0].querySelectorAll('*')) { const r = node.getBoundingClientRect(); if (r.width > 0 && r.height > 0 && !node.closest('.vt-visually-hidden') && r.bottom > deepest.bottom) deepest = { bottom: r.bottom, element: `${node.tagName.toLowerCase()}.${String(node.className).split(' ').join('.')}` }; }
        return { gap: Math.round(turns[1].querySelector('.curation-thread__request').getBoundingClientRect().top - deepest.bottom), deepest: deepest.element };
      });
      assert(Math.abs(between.gap - 48) <= 1, `${label}: turns stand 48px apart ${JSON.stringify(between)}`);
      // The composer's shade is there only while the conversation passes under it: none at the end, back when scrolled up.
      const shade = async top => page.evaluate(async top => {
        const scroller = document.querySelector('.shell-product-body'); scroller.scrollTop = top === 'end' ? scroller.scrollHeight : top;
        await new Promise(resolve => setTimeout(resolve, 400)); // past the shade's fade
        return { scrollable: scroller.scrollHeight - scroller.clientHeight > 40, shadow: getComputedStyle(document.querySelector('.curation-composer-anchor > .catalog-ui-composer-dock')).boxShadow };
      }, top);
      const atEnd = await shade('end');
      assert.equal(atEnd.shadow, 'none', `${label}: no shade over the composer at the end of the conversation`);
      if (atEnd.scrollable) assert.notEqual((await shade(0)).shadow, 'none', `${label}: the shade returns while the conversation passes under the composer`);
      await page.screenshot({ path: `${out}/${label}-03-second-turn.png` });

      // 3. A product's details: one inset for everything, icons on the same lines.
      await page.evaluate(() => { document.querySelector('.shell-product-body').scrollTop = 0; });
      if (!viewport.phone) {
        await page.locator('.curation-result__row').first().click();
        await page.locator('.catalog-ui-candidate-modal').waitFor(); await page.waitForTimeout(700);
        const details = await page.evaluate(() => {
          const modal = document.querySelector('.catalog-ui-candidate-modal'), frame = modal.getBoundingClientRect();
          const textLeft = selector => { const node = modal.querySelector(selector); if (!node) return null; const range = document.createRange(); range.selectNodeContents(node); const rect = range.getClientRects()[0]; return rect ? Math.round(rect.left - frame.left) : null; };
          const iconRight = selector => { const icon = modal.querySelector(selector); return icon ? Math.round(frame.right - icon.getBoundingClientRect().right) : null; };
          return {
            // On a desktop the product's name stands beside its picture, so the picture marks the header's start.
            lefts: [Math.round(modal.querySelector('.catalog-ui-candidate-modal__media').getBoundingClientRect().left - frame.left), textLeft('.candidate-detail__note'), textLeft('.candidate-detail__section .vt-disclosure__trigger > span'), textLeft('.candidate-detail__section .vt-disclosure__content p, .candidate-detail__section .vt-disclosure__content .candidate-axis-details__label'), textLeft('.catalog-ui-candidate-modal__footer span')].filter(value => value !== null),
            rights: [iconRight('.catalog-ui-candidate-modal__header > .vt-button svg'), iconRight('.candidate-detail__section .vt-disclosure__trigger svg')].filter(value => value !== null),
            sizes: [getComputedStyle(modal.querySelector('.catalog-ui-candidate-modal__identity h2')).fontSize, getComputedStyle(modal.querySelector('.candidate-detail__section .vt-disclosure__trigger > span')).fontSize, getComputedStyle(modal.querySelector('.candidate-detail__section .vt-disclosure__content')).fontSize],
          };
        });
        console.log(label, 'details', JSON.stringify(details));
        assert(details.lefts.length >= 4 && spread(details.lefts) <= 1, `${label}: the details start on one line ${JSON.stringify(details.lefts)}`);
        assert(details.rights.length === 2 && spread(details.rights) <= 1, `${label}: the close and section icons end on one line ${JSON.stringify(details.rights)}`);
        assert.deepEqual(details.sizes, ['22px', '17.6px', '17.6px'], `${label}: the details read larger, a heading never smaller than its body`);
        await page.screenshot({ path: `${out}/${label}-04-details.png` });
        await page.keyboard.press('Escape'); await page.waitForTimeout(400);
      } else {
        // 4. Two sheets deep on a phone: the one behind recedes under its own shade, and nothing blurs the page.
        await page.locator('.curation-result--row .curation-result__list').first().click();
        await page.locator('.curation-target-sheet').waitFor(); await page.waitForTimeout(700);
        await page.locator('.curation-target-sheet .curation-candidate-card').first().click({ position: { x: 40, y: 40 } });
        await page.locator('.catalog-ui-candidate-modal').waitFor(); await page.waitForTimeout(900);
        const deep = await page.evaluate(() => ({
          recede: getComputedStyle(document.querySelector('.curation-target-sheet'), '::after').opacity,
          front: getComputedStyle(document.querySelector('.catalog-ui-candidate-modal')).boxShadow !== 'none',
          blurredLayers: [...document.querySelectorAll('body *')].filter(node => { const r = node.getBoundingClientRect(), filter = getComputedStyle(node).backdropFilter; return filter && filter !== 'none' && r.width >= innerWidth - 1 && r.height >= innerHeight - 1; }).length,
        }));
        console.log(label, 'two sheets', JSON.stringify(deep));
        assert.deepEqual(deep, { recede: '1', front: true, blurredLayers: 0 }, `${label}: the sheet behind recedes and the page is never blurred`);
        await page.screenshot({ path: `${out}/${label}-04-two-sheets.png` });
      }
      assert.deepEqual(errors, [], `${label}: no page errors`);
      console.log(label, 'ok', id);
      await context.close();
    }
  } finally {
    await browser.close();
  }
})().catch(error => { console.error(error); process.exit(1); });
