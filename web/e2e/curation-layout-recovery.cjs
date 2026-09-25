// Full authenticated route with deterministic API fixtures. No provider calls.
const assert = require('node:assert/strict');
const path = require('node:path');
const fs = require('node:fs/promises');
const { firefox } = require('playwright');
const { openResults, closeResults } = require('./support/open-results.cjs');
const id = 'e5100000-0000-4000-8000-000000000002';
const timestamp = '2026-09-11T00:00:00Z';
const scope = { country: 'US', allowedItems: [], blockedItems: [], urlMode: 'NONE' };

function fixture(count = 6, targetCount = 1) {
  const targets = Array.from({ length: targetCount }, (_, i) => ({
    id: `target-${i}`, planId: 'plan', curationId: id, userId: 'fixture',
    title: `Headphones ${i + 1}`, normalizedIntent: 'wireless headphones', category: 'Audio',
    allocatedBudget: { amount: '200', currency: 'USD' }, researchScope: scope,
    orderIndex: i, targetHashSchema: 'vitlane.plan-target.v1', version: 1,
    createdAt: timestamp, updatedAt: timestamp,
  }));
  const pools = targets.map(target => ({
    targetId: target.id, version: 1, expandOrdinal: 0, latestMode: 'APPEND',
    sourceCoverage: [{ source: 'SHOPIFY', status: 'SUCCEEDED', candidateCount: count }],
    products: Array.from({ length: count }, (_, i) => ({
      candidateId: `${target.id}-candidate-${i}`, source: 'SHOPIFY',
      title: `Wireless headphones ${i + 1} with active noise cancellation`,
      description: 'Synthetic catalog fixture', intentPoint: 'Matches the requested battery life and fit.',
      features: ['Wireless connection'], specifications: ['USB-C'], categories: ['Audio'],
      priceMinimumMinor: 9900, priceMaximumMinor: 9900, currency: 'USD',
      locator: { kind: 'PRODUCT_URL', productUrl: `https://shop.example/products/${i}` },
      previewVariant: { id: `gid://shopify/ProductVariant/${i}`, title: 'Black',
        priceMinor: 9900, currency: 'USD', available: true, sellerName: 'Fixture seller', sellerDomain: 'shop.example' },
    })), hiddenProducts: [], messages: [],
  }));
  return {
    plan: { id: 'plan', userId: 'fixture', originalIntent: 'Compare headphones', planningMode: 'SINGLE',
      executionMode: 'EXPERIMENT', totalBudget: { amount: '200', currency: 'USD' },
      locationContext: { country: 'US' }, researchScope: scope, createdAt: timestamp },
    curation: { id, shoppingPlanId: 'plan', userId: 'fixture', phase: 'CURATING', version: 2,
      createdAt: timestamp, updatedAt: timestamp }, targets,
    research: { groups: targets.map(t => ({ session: { id: `session-${t.id}`, planTargetId: t.id, status: 'REVIEWING', version: 1 }, candidates: [] })) },
    cart: { selections: [] }, availableActions: [], timeline: [], latestArtifact: 'CURATION_BOARD',
    intelligence: [], coverage: 'NONE',
    catalogResearch: { schemaVersion: 'vitlane.catalog-research-workspace.v3', pools, configurations: [], interactions: [], messages: [] },
  };
}
function setWork(state, status) {
  state.intelligence = [{ jobId: 'job', actionId: 'initial-action', targetKind: state.curation.phase === 'PLANNING' ? 'PLANNING_TASK' : 'RESEARCH_ROUND',
    targetId: 'planning-task', provider: 'MANAGED', status, retryable: false, attempt: 1, steps: [] }];
  state.curation.version++;
  if (status === 'RUNNING') state.activeWork = { workTargetId: 'planning-task', label: 'Research in progress', status: 'RUNNING' };
  else delete state.activeWork;
}
async function installAPI(page, state, unexpected, commands, cancelOutcome = 'CANCELLED', budgetEnabled = false) {
  let thread;
  await page.route('**/api/**', async route => {
    const request = route.request(), pathname = new URL(request.url()).pathname;
    if (!pathname.startsWith('/api/')) return route.continue();
    if (pathname === '/api/v1/analytics/config' && request.method() === 'GET') {
      return route.fulfill({ json: { schemaVersion: 'vitlane.analytics-config.v1', mode: 'disabled', measurementId: '', release: 'fixture' } });
    }
    if (pathname === '/api/v1/curations/product-notices/sync' && request.method() === 'POST') {
      return route.fulfill({ json: { schemaVersion: 'vitlane.curation-notices.v1', curationIds: [] } });
    }
    if (pathname === `/api/v1/curations/${id}/background-research` && request.method() === 'GET') {
      return route.fulfill({ json: { schemaVersion: 'vitlane.background-research.v1', subscriptions: [], findings: [] } });
    }
    let json;
    if (pathname === '/api/v1/me') json = { user: { id: 'fixture', email: 'fixture@vitlane.example', displayName: 'Fixture', marketingAdmin: false, phase5Operator: false } };
    else if (pathname === '/api/v1/me/preferences') {
      const locale = await page.evaluate(() => localStorage.getItem('vitlane.locale.v2') || 'ko-KR');
      const preferences = { schemaVersion: 'vitlane.user-preferences.v1', version: 0, uiLocale: locale, researchCountry: 'US', preferredCurrency: 'USD' };
      json = { preferences, effective: preferences };
    }
    else if (pathname.endsWith('/threads') && request.method()==='GET') json = {schemaVersion:'vitlane.curation-thread.v2',controlMode:{mode:'AUTO',version:1},threads:thread?[thread]:[]};
    else if (pathname.endsWith('/threads')) {
      const body=request.postDataJSON();commands.push({pathname,body});setWork(state,'RUNNING');
      thread={schemaVersion:'vitlane.curation-thread.v2',id:body.clientRequestId,curationId:id,mode:'AUTO',origin:'REQUEST',request:body.request,expectedCurationVersion:body.expectedCurationVersion,revision:1,status:'INTERPRETING',targetLabels:{},actions:[{id:'auto-action',type:'AUTO_START',status:'RUNNING',jobs:[],effects:[],decisions:[]}],createdAt:timestamp,updatedAt:timestamp};json=thread;
    }
    else if (pathname.endsWith('/criteria') && request.method() === 'GET') json = null;
    else if (pathname.endsWith('/budget')) json = { schemaVersion: 'vitlane.curation-budget.v1', version: 0, researchVersion: 0, enabled: budgetEnabled, currency: 'USD', totalAmount: budgetEnabled ? (state.targets.length * 200).toFixed(2) : null, allocations: state.targets.map(t=>({targetId:t.id,quantity:1,amount:budgetEnabled ? '200.00' : null})) };
    else if (pathname.endsWith('/research-settings')) json = { schemaVersion: 'vitlane.research-settings.v1', country: 'US', version: 0 };
    else if (pathname === '/api/v1/managed-runner/usage') json = {
      enabled: true, usageDate: '2026-09-11', userSpentMicros: 0, userLimitMicros: 100000,
      userExhausted: false, serverExhausted: false, serverLimitMicros: 8000000,
    };
    else if (pathname.endsWith('/exchange-rate')) json = { schemaVersion: 'vitlane.exchange-rate.v1', status: 'UNAVAILABLE' };
    else if (pathname === '/api/v1/curations' && request.method() === 'GET') json = { schemaVersion: 'vitlane.curation-list.v2', curations: [] };
    else if (pathname === '/api/v1/support/summary') json = { schemaVersion: 'vitlane.support-summary.v1', unread: 0 };
    else if (pathname === '/api/v1/auth/capabilities') json = { googleEnabled: true, localReviewEnabled: false, localReviewSeeded: false, localReviewProfiles: [] };
    else if (pathname.endsWith('/workspace')) json = state;
    else if (pathname.endsWith('/cart') && request.method() === 'GET') json = { schemaVersion: 'vitlane.cart-view.v2', curationId: id, version: 0, country: 'US', currency: 'USD', items: [] };
    else if (pathname.endsWith('/catalog-research/hydrations')) {
      const body = request.postDataJSON();
      json = { ...state.catalogResearch, schemaVersion: 'vitlane.catalog-research-hydration.v1',
        pools: state.catalogResearch.pools.filter(p => p.targetId === body.targetId) };
    } else if (pathname.endsWith('/cancel')) {
      commands.push({ pathname }); setWork(state, cancelOutcome); json = { cancelledJobs: cancelOutcome === 'CANCELLED' ? 1 : 0 };
    } else if (pathname.endsWith('/conversation-requests')) {
      commands.push({ pathname, body: request.postDataJSON() }); setWork(state, 'RUNNING');
      json = { status: 'EXECUTED', decision: 'ADD_TARGET', source: 'DETERMINISTIC', reasonCode: 'NO_EXISTING_TARGET', replay: false };
    } else { unexpected.push(`${request.method()} ${pathname}`); return route.abort(); }
    await route.fulfill({ status: (pathname.endsWith('/conversation-requests') || pathname.endsWith('/threads') && request.method()==='POST') ? 202 : 200, json });
  });
  await page.route(/https:\/\/(?!127\.0\.0\.1|localhost)/, route => {
    unexpected.push('External network request'); return route.abort();
  });
}
async function waitLayout(page) {
  await page.waitForFunction(() => {
    const dock = document.querySelector('.catalog-ui-composer-dock');
    const shell = dock?.closest('.curation-workspace__dock-shell');
    return dock && shell && getComputedStyle(shell).position === 'sticky';
  });
  await page.evaluate(() => document.fonts.ready);
  await page.waitForTimeout(120);
}
async function dockAtEveryScroll(page, width, height) {
  const measurements = [];
  for (const fraction of [0, 0.5, 1]) {
    await page.locator('.shell-product-body').evaluate((node, f) => { node.scrollTop = node.scrollHeight * f; }, fraction);
    await page.waitForTimeout(40);
    const m = await page.locator('.catalog-ui-composer-dock').evaluate(node => {
      const r = node.getBoundingClientRect(), button = node.querySelector('[type="submit"]').getBoundingClientRect();
      const shell = node.closest('.curation-workspace__dock-shell');
      return {
        position: getComputedStyle(shell).position,
        dockPosition: getComputedStyle(node).position,
        x: r.x, right: r.right, top: r.top, bottom: r.bottom,
        buttonTop: button.top, buttonBottom: button.bottom,
        pageWidth: document.documentElement.scrollWidth,
        coordinates: [
          node.style.left, node.style.width, node.style.bottom,
          node.style.getPropertyValue('--composer-left'),
          node.style.getPropertyValue('--composer-width'),
          node.style.getPropertyValue('--composer-bottom'),
        ].join(''),
      };
    });
    assert.equal(m.position, 'sticky');
    assert.notEqual(m.dockPosition, 'fixed');
    assert.equal(m.coordinates, '', 'composer must not depend on runtime viewport coordinates');
    assert(m.x >= -1 && m.right <= width + 1 && m.top >= 0 && m.bottom <= height + 1, JSON.stringify(m));
    assert(m.buttonTop >= 0 && m.buttonBottom <= height + 1, JSON.stringify(m));
    assert(m.pageWidth <= width + 1, JSON.stringify(m));
    measurements.push(m);
  }
  assert(Math.abs(measurements[0].top - measurements[2].top) < 1, 'composer moved during scroll');
  return measurements[0];
}
async function run() {
  const { createServer } = await import('vite');
  const root = path.resolve(__dirname, '..');
  const vite = await createServer({ root, configFile: path.join(root, 'vite.config.ts'), clearScreen: false,
    logLevel: 'error', server: { host: '127.0.0.1', port: 0, strictPort: false, fs: { allow: [root, await fs.realpath(path.join(root, 'node_modules'))] } } });
  await vite.listen();
  const base = vite.resolvedUrls.local[0].replace(/\/$/, '');
  const browser = await firefox.launch();
  const results = [], unexpected = [], errors = [];
  if (process.env.E2E_EVIDENCE_DIR) await fs.mkdir(process.env.E2E_EVIDENCE_DIR, {recursive:true});
  // `columns` is the grid of the product group's sheet: about 50rem on a desktop (three 15rem cards),
  // the full width on a phone (two cards scaled down together, ADR-0076), fewer when text is enlarged.
  const cases = [
    ...[1, 4, 6, 8, 9].map(count => ({ width: 1440, height: 720, count, columns: 3 })),
    { width: 1280, height: 900, count: 6, columns: 3 },
    { width: 1280, height: 720, count: 6, columns: 3 },
    { width: 1920, height: 1080, count: 6, columns: 3 },
    { width: 1152, height: 720, count: 7, columns: 3 },
    { width: 900, height: 720, count: 5, columns: 3 },
    { width: 390, height: 844, count: 3, columns: 2, compact: true, phone: true },
    { width: 320, height: 720, count: 3, columns: 2, compact: true, phone: true },
    { width: 1440, height: 720, count: 9, columns: 3, targetCount: 2 },
    { width: 1280, height: 900, count: 6, columns: 2, scale: 200 },
    { width: 320, height: 720, count: 3, columns: 1, scale: 200, phone: true },
  ];
  try {
    for (const locale of ['en-US', 'ko-KR']) for (const theme of ['light', 'dark']) for (const spec of cases) {
      const context = await browser.newContext({ viewport: { width: spec.width, height: spec.height }, reducedMotion: 'reduce' });
      await context.addInitScript(({ locale, theme }) => {
        localStorage.setItem('vitlane.locale.v2', locale);
        localStorage.setItem('vitlane.appearance.v2', JSON.stringify({ theme, accent: 'blue' }));
      }, { locale, theme });
      const page = await context.newPage(); page.setDefaultTimeout(30000);
      page.on('pageerror', e => errors.push(e.message));
      const budgetPopoverCase = locale === 'ko-KR' && theme === 'light' && spec.width === 1440 && spec.height === 720 && spec.count === 6 && !spec.scale && !spec.targetCount;
      await installAPI(page, fixture(spec.count, spec.targetCount ?? 1), unexpected, [], 'CANCELLED', budgetPopoverCase);
      await page.goto(`${base}/curations/${id}`, { waitUntil: 'networkidle' });
      if (spec.scale) await page.evaluate(scale => { document.documentElement.style.fontSize = `${16 * scale / 100}px`; }, spec.scale);
      try { await waitLayout(page); } catch (error) { console.error({spec, unexpected, errors, body: (await page.locator('body').innerText()).slice(-4000)}); throw error; }
      // The conversation: one reading column, a result per product group, and no board inside it.
      const targetCount = spec.targetCount ?? 1;
      const conversation = await page.evaluate(() => {
        const main = document.querySelector('.curation-workspace__main'), style = getComputedStyle(main), box = main.getBoundingClientRect(), area = main.parentElement.getBoundingClientRect();
        const rem = parseFloat(getComputedStyle(document.documentElement).fontSize);
        return { rem, column: box.width - parseFloat(style.paddingLeft) - parseFloat(style.paddingRight), left: box.left - area.left, right: area.right - box.right,
          layout: document.querySelector('.curation-results__list')?.getAttribute('data-layout'), results: document.querySelectorAll('[data-result-target]').length,
          boards: document.querySelectorAll('.catalog-ui-candidate-grid, .catalog-ui-target').length };
      });
      assert.equal(conversation.results, targetCount, JSON.stringify({ spec, conversation }));
      assert.equal(conversation.layout, targetCount === 1 ? 'card' : 'row', JSON.stringify({ spec, conversation }));
      assert.equal(conversation.boards, 0, 'the conversation never holds a product group\'s board');
      // The reading column is 57.6rem (ADR-0089, 20% wider than ADR-0086's 48rem).
      assert(conversation.column <= 57.6 * conversation.rem + 1, JSON.stringify({ spec, conversation }));
      assert(Math.abs(conversation.left - conversation.right) <= 1, `the reading column is centred: ${JSON.stringify({ spec, conversation })}`);
      const composer = await dockAtEveryScroll(page, spec.width, spec.height);
      const lastBottom = await page.locator('[data-result-target]').last().evaluate(n => n.getBoundingClientRect().bottom);
      assert(lastBottom <= composer.top + 1, JSON.stringify({ spec, lastBottom, composer }));
      assert.equal(await page.locator('.catalog-ui-focus-composer').count(), 1);
      assert.equal(await page.getByRole('button', { name: locale === 'ko-KR' ? '새 응답' : 'New response', exact: true }).count(), 0);
      assert.equal(await page.locator('.curation-budget__segment').first().isDisabled(), !budgetPopoverCase);
      if (budgetPopoverCase) {
        const trigger = page.locator('.curation-budget__segment').first();
        const triggerBox = await trigger.boundingBox();
        await trigger.click();
        const popover = page.locator('.budget-popover');
        await popover.waitFor();
        const [popoverBox, arrowBox] = await Promise.all([
          popover.boundingBox(),
          popover.locator('.budget-popover__arrow').boundingBox(),
        ]);
        assert(triggerBox && popoverBox && arrowBox, 'Target budget popover geometry is incomplete');
        assert(popoverBox.y + popoverBox.height <= triggerBox.y + 1,
          JSON.stringify({ triggerBox, popoverBox }));
        const arrowCenter = arrowBox.x + arrowBox.width / 2;
        assert(arrowCenter >= triggerBox.x && arrowCenter <= triggerBox.x + triggerBox.width,
          JSON.stringify({ triggerBox, arrowBox }));
        await page.keyboard.press('Escape');
        await popover.waitFor({ state: 'hidden' });
      }
      if (spec.width === 1920) {
        await page.locator('.shell-product-sidebar__toggle').click();
        await page.waitForTimeout(40);
        const hit = await page.locator('.shell-sidebar-profile__trigger').evaluate(trigger => {
          const rect = trigger.getBoundingClientRect();
          const x = rect.left + rect.width / 2;
          const y = rect.top + rect.height / 2;
          const target = document.elementFromPoint(x, y);
          const sidebar = trigger.closest('.shell-product-sidebar');
          const composerShell = document.querySelector('.curation-workspace__dock-shell');
          return {
            x,
            y,
            hitsProfile: target === trigger || trigger.contains(target),
            sidebarLayer: Number.parseInt(getComputedStyle(sidebar).zIndex, 10),
            composerLayer: Number.parseInt(getComputedStyle(composerShell).zIndex, 10),
          };
        });
        assert(hit.hitsProfile, `profile must own its hit target during the sidebar transition: ${JSON.stringify(hit)}`);
        assert(hit.sidebarLayer > hit.composerLayer, `navigation layer must outrank composer: ${JSON.stringify(hit)}`);
        await page.mouse.click(hit.x, hit.y);
        const profileMenu = page.locator('.shell-sidebar-profile__menu');
        await profileMenu.waitFor();
        assert.equal(await page.locator('.budget-popover').count(), 0,
          'profile click must not pass through to the curation budget control');
        await page.keyboard.press('Escape');
        await profileMenu.waitFor({ state: 'hidden' });
        await page.waitForTimeout(160);
        for (const selector of ['.curation-composer-anchor', '.shell-product-sidebar__brand-trigger']) {
          if (selector.includes('brand-trigger')) {
            await page.locator(selector).click();
            await page.waitForTimeout(200);
          }
          const alignment = await page.locator('.curation-composer-anchor').evaluate(anchor => ({
            anchor: anchor.getBoundingClientRect().left,
            dock: anchor.querySelector('.catalog-ui-composer-dock').getBoundingClientRect().left,
          }));
          assert(Math.abs(alignment.anchor - alignment.dock) < 1,
            `composer must follow sidebar changes even at capped content width: ${JSON.stringify(alignment)}`);
        }
      }
      // The product group's sheet: beside the conversation on a desktop, a bottom sheet on a phone, and a grid that only flows down.
      await openResults(page);
      await page.waitForTimeout(260);
      const sheet = await page.evaluate(() => {
        const panel = document.querySelector('.curation-target-sheet'), body = panel.querySelector('.curation-target-sheet__body'), grid = panel.querySelector('.catalog-ui-candidate-grid');
        const box = panel.getBoundingClientRect(), rem = parseFloat(getComputedStyle(document.documentElement).fontSize);
        const cells = [...grid.querySelectorAll('.catalog-ui-candidate-cell')].map(cell => { const r = cell.getBoundingClientRect(); return { x: r.x, y: r.y, width: r.width }; });
        const first = grid.querySelector('.catalog-ui-candidate-cell'), card = first.firstElementChild;
        return { rem, top: box.top, bottom: box.bottom, left: box.left, right: box.right, width: box.width, viewport: { width: innerWidth, height: innerHeight },
          columns: Number(grid.getAttribute('data-column-count')), compact: grid.getAttribute('data-compact'), cells,
          zoom: Number(getComputedStyle(card).zoom), cell: first.getBoundingClientRect().width, gridWidth: grid.clientWidth,
          sideways: Math.max(body.scrollWidth - body.clientWidth, grid.scrollWidth - grid.clientWidth), scrollsDown: getComputedStyle(body).overflowY,
          tabs: panel.querySelectorAll('[role="tab"]').length, pageWidth: document.documentElement.scrollWidth };
      });
      assert.equal(sheet.columns, spec.columns, JSON.stringify({ spec, sheet }));
      assert.equal(sheet.compact, spec.compact ? 'true' : null, JSON.stringify({ spec, sheet }));
      if (spec.compact) {
        // The whole card shrinks: the cell is half the grid and the card keeps the 15rem layout inside it.
        assert(sheet.zoom > 0.5 && sheet.zoom < 1, JSON.stringify({ spec, sheet }));
        assert(Math.abs(sheet.cell * 2 + 8 - sheet.gridWidth) <= 2, JSON.stringify({ spec, sheet }));
      }
      assert.equal(sheet.cells.length, spec.count, JSON.stringify({ spec, sheet }));
      for (let i = 0; i < Math.min(sheet.columns, spec.count); i++) assert(Math.abs(sheet.cells[i].y - sheet.cells[0].y) < 1, 'first row order');
      const rows = Math.ceil(spec.count / sheet.columns);
      if (rows > 1) {
        assert(sheet.cells[sheet.columns].y > sheet.cells[0].y, 'the next row follows the full first row');
        assert(Math.abs(sheet.cells[sheet.columns].x - sheet.cells[0].x) < 1, 'the next row starts at the left');
      }
      assert(sheet.sideways <= 1 && sheet.scrollsDown === 'auto', `the sheet scrolls down, never sideways: ${JSON.stringify({ spec, sheet })}`);
      assert(sheet.pageWidth <= spec.width + 1, JSON.stringify({ spec, sheet }));
      if (spec.phone) {
        assert(Math.abs(sheet.width - spec.width) <= 1 && Math.abs(sheet.bottom - spec.height) <= 1, JSON.stringify({ spec, sheet }));
        assert(Math.abs(sheet.top - spec.height * 0.06) <= 2, `a phone sheet is 94dvh, taller than a product's details: ${JSON.stringify({ spec, sheet })}`);
      } else {
        assert(Math.abs(sheet.right - spec.width) <= 1 && Math.abs(sheet.top) <= 1 && Math.abs(sheet.bottom - spec.height) <= 1, JSON.stringify({ spec, sheet }));
        assert(sheet.width <= 50 * sheet.rem + 1 && sheet.left >= 0, JSON.stringify({ spec, sheet }));
      }
      // Several product groups: the tab row is the index of every candidate — "All" first, then one tab per group.
      assert.equal(sheet.tabs, targetCount > 1 ? targetCount + 1 : 0, JSON.stringify({ spec, sheet }));
      if (targetCount > 1) {
        await page.locator('.curation-target-sheet [role="tab"]').nth(2).click();
        await page.waitForFunction(count => document.querySelectorAll('.curation-target-sheet .catalog-ui-candidate-cell').length === count, spec.count);
      }
      if (process.env.E2E_EVIDENCE_DIR && locale === 'ko-KR' && theme === 'light' && spec.count === 6 && !spec.scale) {
        await page.screenshot({ path: path.join(process.env.E2E_EVIDENCE_DIR, `sheet-${spec.width}.png`) });
      }
      await closeResults(page);
      results.push({ ...spec, locale, theme, rows, composerHeight: spec.height - composer.top });
      if (process.env.E2E_EVIDENCE_DIR && locale === 'ko-KR' && theme === 'light' && spec.count === 6 && !spec.scale) {
        await fs.mkdir(process.env.E2E_EVIDENCE_DIR, { recursive: true });
        await page.screenshot({ path: path.join(process.env.E2E_EVIDENCE_DIR, `layout-${spec.width}.png`) });
      }
      await context.close();
    }
    for (const locale of ['en-US', 'ko-KR']) for (const size of [{width:1280,zoom:1},{width:320,zoom:1},{width:320,zoom:2}]) {
      const context = await browser.newContext({ viewport: { width: size.width, height: 720 }, reducedMotion: 'reduce' });
      await context.addInitScript(locale => localStorage.setItem('vitlane.locale.v2', locale), locale);
      const page = await context.newPage(); page.setDefaultTimeout(30000);
      page.on('pageerror', e => errors.push(e.message));
      const state = fixture(0, 0); state.curation.phase = 'PLANNING'; state.latestArtifact = 'TARGET_LIST'; setWork(state, 'RUNNING');
      state.intelligence[0].steps = [{kind:'INTERPRETING',status:'RUNNING',startedAt:timestamp}];
      const commands = []; await installAPI(page, state, unexpected, commands);
      await page.goto(`${base}/curations/${id}`, { waitUntil: 'networkidle' }); await waitLayout(page);
      await page.evaluate(zoom => document.documentElement.style.fontSize = `${16*zoom}px`, size.zoom);
      await waitLayout(page);
      const progress = await page.locator('.curation-composer-progress').evaluate(node => {
        const box = node.getBoundingClientRect(), composer = document.querySelector('.catalog-ui-focus-composer').getBoundingClientRect();
        return {top:box.top,bottom:box.bottom,composerTop:composer.top,inDock:!!node.closest('.catalog-ui-composer-dock')};
      });
      assert(progress.inDock && progress.top>=0 && progress.bottom<=progress.composerTop+1 && progress.composerTop-progress.bottom<=16*size.zoom, JSON.stringify(progress));
      await dockAtEveryScroll(page, size.width, 720);
      assert(await page.locator('.catalog-ui-focus-composer textarea').isDisabled());
      assert.equal(await page.locator('.curation-working-bar').count(), 0);
      assert.equal(await page.locator('.curation-live-artifact__header:visible').count(), 0);
      assert.equal(await page.locator('.curation-cancel-action').count(), 1);
      assert.equal(await page.locator('.curation-live-artifact').evaluate(n => getComputedStyle(n).borderTopWidth), '0px');
      const spinner = page.locator('.curation-step-spinner');
      await spinner.waitFor();
      assert.equal(await spinner.evaluate(n => getComputedStyle(n).animationName), 'none');
      await page.emulateMedia({reducedMotion:'no-preference'});
      const beforeRotation = await spinner.evaluate(n => getComputedStyle(n).transform);
      await page.waitForTimeout(150);
      assert.notEqual(await spinner.evaluate(n => getComputedStyle(n).transform), beforeRotation);
      if (process.env.E2E_EVIDENCE_DIR) await page.screenshot({path:path.join(process.env.E2E_EVIDENCE_DIR, `planning-${locale}-${size.width}-${size.zoom}x.png`)});

      await page.locator('.curation-cancel-action').click();
      await page.waitForFunction(() => !document.querySelector('.catalog-ui-focus-composer textarea')?.disabled);
      assert.equal(await page.locator('.curation-artifact-empty--recovery').count(), 0);
      assert.equal(await page.locator('.catalog-ui-focus-composer').count(), 1);
      assert.equal(await page.locator('.curation-live-artifact__header:visible').count(), 0);
      await dockAtEveryScroll(page, size.width, 720);
      await page.locator('.catalog-ui-focus-composer textarea').fill('Find wireless headphones again');
      await page.locator('.catalog-ui-focus-composer [type="submit"]').click();
      await page.waitForFunction(() => document.querySelector('.curation-thread') && document.querySelector('.catalog-ui-focus-composer textarea')?.disabled);
      assert.equal(commands.filter(c => c.pathname.endsWith('/threads')).length, 1);
      assert.equal(commands[1].body.request, 'Find wireless headphones again');
      assert.equal(commands[1].body.expectedCurationVersion, 4);
      await context.close();
    }
    for (const outcome of ['CANCELLED', 'SUCCEEDED']) {
      const context = await browser.newContext({ viewport: { width: 1280, height: 900 }, reducedMotion: 'reduce' });
      const page = await context.newPage();
      page.on('pageerror', e => errors.push(e.message));
      const state = fixture(6); setWork(state, 'RUNNING');
      const commands = []; await installAPI(page, state, unexpected, commands, outcome);
      await page.goto(`${base}/curations/${id}`, { waitUntil: 'networkidle' });
      await waitLayout(page);
      await openResults(page);
      const before = await page.locator('.catalog-ui-candidate-cell').allTextContents();
      assert.equal(before.length, 6);
      await closeResults(page);
      await page.locator('.curation-cancel-action').click();
      // The curating composer stays enabled while the Server works, so an
      // enabled textarea proves nothing here; the cancel control leaving the
      // dock is the signal that the workspace re-read after the cancel landed.
      await page.locator('.curation-cancel-action').waitFor({ state: 'detached' });
      await page.waitForFunction(() => !document.querySelector('.catalog-ui-focus-composer textarea')?.disabled);
      await openResults(page);
      assert.deepEqual(await page.locator('.catalog-ui-candidate-cell').allTextContents(), before,
        'cancel or a completed-before-cancel race must preserve all existing Candidates');
      await closeResults(page);
      assert.equal(commands.length, 1);
      assert.equal(await page.locator('.curation-cancel-action').count(), 0);
      assert.equal(await page.locator('.catalog-ui-focus-composer').count(), 1);
      await context.close();
    }
    assert.deepEqual(unexpected, []); assert.deepEqual(errors, []);
    if (process.env.E2E_EVIDENCE_DIR) await fs.writeFile(path.join(process.env.E2E_EVIDENCE_DIR, 'matrix.json'), JSON.stringify({ mode: 'ISOLATED_ROUTE_FIXTURE', results, cancellationLocales: 2, cancellationLayouts: 3, retainedCandidateOutcomes: ["CANCELLED", "SUCCEEDED"], unexpected, errors }, null, 2));
    console.log(`PASS ${results.length} route layout cases and 6 cancellation/resubmit flows plus 2 retained-result outcomes; no live provider calls`);
  } finally { await browser.close(); await vite.close(); }
}
run().catch(error => { console.error(error); process.exitCode = 1; });
