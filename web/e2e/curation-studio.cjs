const { firefox } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const base = process.env.CURATION_STUDIO_URL || 'http://127.0.0.1:5194';
const output = process.env.CURATION_STUDIO_EVIDENCE || '/tmp/vitlane-curation-studio-evidence';

(async () => {
  await fs.mkdir(output, { recursive: true });
  const browser = await firefox.launch({ headless: true });
  const results = [];
  const failures = [];
  try {
    for (const locale of ['en-US', 'ko-KR']) {
      const context = await browser.newContext({ viewport: { width: 1440, height: 1300 }, locale, reducedMotion: 'reduce' });
      const page = await context.newPage();
      const errors = [];
      const api = [];
      page.on('pageerror', error => errors.push(error.message));
      page.on('request', request => { if (new URL(request.url()).pathname.startsWith('/api/')) api.push(request.url()); });
      await page.goto(`${base}/curation-studio.html?locale=${locale}`, { waitUntil: 'networkidle' });
      await page.evaluate(() => document.fonts.ready);
      const en = locale === 'en-US';
      const name = (english, korean) => en ? english : korean;
      const button = (english, korean) => page.getByRole('button', { name: name(english, korean), exact: true });
      const cardIDs = () => page.locator('[data-candidate]').evaluateAll(nodes => nodes.map(n => n.dataset.candidate));
      assert.equal(await page.locator('html').getAttribute('lang'), en ? 'en' : 'ko');
      assert.deepEqual(await cardIDs(), ['pilot', 'lamy', 'pelikan']);
      assert.equal(await page.locator('#vitlane-product-sidebar').count(), 1);
      assert.equal(await page.locator('.studio-sidebar').count(), 0);
      const header = await page.locator('.studio-target-heading').boundingBox();
      const tip = await page.locator('.studio-tip').boundingBox();
      assert.ok(tip.y >= header.y + header.height);
      const card = await page.locator('[data-candidate]').first().boundingBox();
      assert.ok(card.width >= 224 && card.width <= 256);
      const originalCopy = await page.locator('[data-user-content]').first().textContent();
      const before = await page.locator('[data-candidate="pilot"] .studio-rationale').textContent();
      await button('Writing feel 5/5', '필기감 5/5').click();
      await page.getByRole('radio', { name: name('Level 1', '1단계'), exact: true }).click();
      await page.keyboard.press('Escape');
      assert.equal(await page.locator('[data-candidate="pilot"] .studio-rationale').textContent(), before);
      await page.getByRole('radio', { name: name('Lowest price', '낮은 가격'), exact: true }).click();
      assert.equal((await cardIDs())[0], 'lamy');
      assert.equal(await page.getByRole('radio', { name: name('Best score', '높은 점수'), exact: true }).count(), 0);
      await page.getByRole('radio', { name: name('AI pick', 'AI 추천'), exact: true }).click();
      assert.equal((await cardIDs())[0], 'pilot');
      await button('Price and score filters', '가격·점수 필터').click();
      await page.getByLabel(name('Minimum price · USD', '최소 가격 · USD'), { exact: true }).fill('200');
      await page.getByLabel(name('Maximum price · USD', '최대 가격 · USD'), { exact: true }).fill('250');
      await page.keyboard.press('Escape');
      assert.deepEqual(await cardIDs(), ['lamy']);
      await button('Price and score filters', '가격·점수 필터').click();
      await page.getByLabel(name('Minimum price · USD', '최소 가격 · USD'), { exact: true }).fill('500');
      assert.deepEqual(await cardIDs(), ['lamy'], 'An invalid range keeps the last valid view');
      assert.equal(await page.getByRole('alert').count(), 1);
      await button('Clear filters', '필터 해제').click();
      await page.keyboard.press('Escape');
      await button('Hidden 1', '숨김 1').click();
      assert.equal((await cardIDs()).length, 4);
      await button('Restore Pilot Custom 74', '숨김 해제 Pilot Custom 74').click();
      assert.equal(await page.locator('[data-candidate]').count(), 4);
      await button('Hide Pilot Custom 74', '숨기기 Pilot Custom 74').click();
      await button('Hidden 1', '숨김 1').click();
      await button('Collapse candidates', '상품 접기').click();
      assert.equal(await page.locator('[data-candidate]').count(), 0);
      assert.equal(await page.locator('.studio-mission-badge').count(), 2);
      await button('Expand candidates', '상품 펼치기').click();
      await button('Research again', '재조사').click();
      assert.equal(await button('Find more', '후보 더 찾기').isDisabled(), true);
      await button('Cancel', '취소').click();
      assert.equal((await cardIDs()).length, 3);
      await button('Set a price watch', '가격 조사 설정').click();
      await page.getByLabel(name('Tell me at this price or below · USD', '이 가격 이하일 때 알려주기 · USD')).fill('290');
      await page.getByLabel(name('Minimum price · USD', '최소 가격 · USD'), { exact: true }).fill('300');
      assert.equal(await button('Start watching', '조사 등록').isDisabled(), true);
      await page.getByLabel(name('Minimum price · USD', '최소 가격 · USD'), { exact: true }).fill('200');
      await button('Start watching', '조사 등록').click();
      await page.getByRole('heading', { name: name('Research details', '조사 상세'), exact: true }).waitFor();
      assert.equal(await page.locator('.studio-mission-badge').count(), 3);
      await button('Extend 7 days', '7일 연장').click();
      await button('Found', '완료').click();
      assert.equal(await page.locator('.studio-detail-status').textContent(), name('Found a match', '결과 도착'));
      assert.equal(await page.locator('.studio-result-card').count(), 1);
      assert.ok((await page.locator('.studio-result-card').textContent()).includes('$290'));
      await page.screenshot({ path: path.join(output, `result-${locale}.png`) });
      await button('Review candidate', '후보 확인').click();
      assert.equal(await page.locator('.studio-product-detail').count(), 1);
      // Result observations do not overwrite the catalog fixture price.
      assert.ok((await page.locator('.studio-product-detail .studio-price').textContent()).includes('$336'));
      await page.keyboard.press('Escape');
      await button('Open research workspace', '조사 작업 열기').click();
      await page.getByRole('button', { name: /Remind me later|정한 시점에 알려주기/ }).click();
      assert.equal(await page.getByLabel(name('Minimum price · USD', '최소 가격 · USD'), { exact: true }).count(), 0);
      assert.equal(await page.getByLabel(name('Remind me at', '알림 시각'), { exact: true }).count(), 1);
      await page.screenshot({ path: path.join(output, `reminder-${locale}.png`) });
      await button('Start watching', '조사 등록').click();
      await page.getByRole('heading', { name: name('Research details', '조사 상세'), exact: true }).waitFor();
      assert.equal(await page.locator('.studio-detail-status').textContent(), name('Scheduled', '예약됨'));
      await button('Preview reminder', '알림 도착 체험').click();
      assert.equal(await page.locator('.studio-detail-status').textContent(), name('Reminder arrived', '알림 시점 도착'));
      assert.ok(!(await page.locator('.studio-result-card').textContent()).includes('USD'));
      await page.keyboard.press('Escape');
      await button('View Pilot Custom 823', '상세 보기 Pilot Custom 823').click();
      await button('Add to shortlist', '선택 목록에 담기').click();
      await page.keyboard.press('Escape');
      assert.equal(await page.locator('[data-candidate="pilot"]').evaluate(n => n.classList.contains('is-selected')), true);
      await button('Open research workspace', '조사 작업 열기').click();
      assert.deepEqual(await page.locator('.studio-work-section h3').allTextContents(), [name('Your research', '등록한 작업'), name('Create research', '작업 만들기'), name('Do it now', '지금 한 번')]);
      assert.equal(await page.locator('.studio-ended').getAttribute('data-state'), 'closed');
      assert.equal(await page.locator('.studio-job-rows').first().getByRole('button').count(), 2);
      await page.locator('.studio-ended .vt-disclosure__trigger').click();
      assert.equal(await page.locator('.studio-ended .studio-job-row').count(), 2);
      await page.locator('.studio-ended .vt-disclosure__trigger').click();
      await page.screenshot({ path: path.join(output, `workspace-${locale}.png`) });
      // Radix owns focus trapping and returns focus to the explicit opener.
      for (let i = 0; i < 15; i++) {
        await page.keyboard.press('Tab');
        assert.equal(await page.evaluate(() => !!document.activeElement.closest('[role="dialog"]')), true);
      }
      await page.keyboard.press('Escape');
      assert.equal(await button('Open research workspace', '조사 작업 열기').evaluate(n => n === document.activeElement), true);
      // Switching locale keeps product/user content and local effects intact.
      await button('Switch to Korean', '영어로 전환').click();
      assert.equal(await page.locator('[data-user-content]').first().textContent(), originalCopy);
      assert.equal(await page.locator('[data-candidate="pilot"]').evaluate(n => n.classList.contains('is-selected')), true);
      // Reload deliberately resets this isolated fixture.
      await page.reload({ waitUntil: 'networkidle' });
      assert.equal(await page.locator('.studio-mission-badge').count(), 2);
      await page.screenshot({ path: path.join(output, `page-${locale}.png`), fullPage: true });
      for (const width of [1440, 768, 390, 320]) {
        for (const zoom of [100, 200]) {
          await page.setViewportSize({ width, height: width === 1440 ? 1300 : 900 });
          await page.evaluate(zoom => document.documentElement.style.fontSize = `${zoom}%`, zoom);
          await page.evaluate(() => { document.activeElement?.blur(); document.querySelector('.studio-main').scrollTo({ top: 0, behavior: 'instant' }); });
          await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
          const layout = await page.evaluate(() => ({
            width: innerWidth, scroll: document.documentElement.scrollWidth,
            h1: document.querySelectorAll('h1').length,
            composerPosition: getComputedStyle(document.querySelector('.studio-composer-area')).position,
            content: {width: document.querySelector('.studio-main').clientWidth, scroll: document.querySelector('.studio-main').scrollWidth},
            names: [...document.querySelectorAll('button')].filter(n => !n.textContent.trim() && !n.getAttribute('aria-label')).length,
            images: [...document.images].filter(n => !n.complete || n.naturalWidth === 0).length,
          }));
          if (width <= 640) assert.equal(layout.composerPosition, 'static', 'Mobile composer must not obscure enlarged candidate content');
          if (layout.scroll > width || layout.content.scroll > layout.content.width + 1 || layout.names || layout.images || layout.h1 !== 1) failures.push({ locale, width, zoom, layout });
          if (width === 390 && zoom === 100 || width === 320 && zoom === 200) await page.screenshot({ path: path.join(output, `page-${locale}-${width}-${zoom}.png`), fullPage: true });
          if (width === 390 && zoom === 100) {
            await page.evaluate(() => document.querySelector('.studio-main').scrollTo({ top: 0, behavior: 'instant' }));
            await page.screenshot({ path: path.join(output, `viewport-${locale}-mobile.png`) });
          }
          await button('Open research workspace', '조사 작업 열기').click();
          await page.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))));
          const dialog = await page.getByRole('dialog').evaluate(n => ({ width: n.clientWidth, scroll: n.scrollWidth, right: n.getBoundingClientRect().right }));
          if (dialog.scroll > dialog.width + 1 || dialog.right > width + 1 || dialog.right < width - 1 && width <= 640) failures.push({ locale, width, zoom, dialog });
          await page.keyboard.press('Escape');
          results.push({ locale, width, textZoom: zoom, layout, dialog });
        }
      }
      assert.deepEqual(api, [], 'The prototype must not call product APIs');
      assert.deepEqual(errors, [], 'No uncaught errors');
      await context.close();
    }
    await fs.writeFile(path.join(output, 'manifest.json'), JSON.stringify({ browser: 'Firefox', scenarios: 'sort/filter/hide/restore/collapse/priority/foreground-cancel/create/extend/complete/result-to-candidate/reminder/ended-disclosure/shortlist/locale/focus/reload/no-api', results, failures }, null, 2));
    console.log(JSON.stringify({ matrices: results.length, failures, evidence: output }, null, 2));
    assert.equal(failures.length, 0);
  } finally { await browser.close(); }
})().catch(error => { console.error(error); process.exitCode = 1; });
