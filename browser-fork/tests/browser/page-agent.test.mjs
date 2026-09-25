import test, { before, after } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { chromium } from 'playwright';
import { validateObservation } from '../../server/protocol.mjs';
import {
  buildPurchasePreparationPlan,
  computePurchaseApprovalDigest,
} from '../../server/purchase-recipes.mjs';

const source = await readFile(new URL('../../agent/page-agent.js', import.meta.url), 'utf8');
let browser;

before(async () => {
  // Playwright uses a separate temporary profile; no personal browser data is used.
  browser = await chromium.launch({ channel: process.env.PLAYWRIGHT_CHANNEL || undefined, headless: true });
});
after(async () => { await browser?.close(); });

function fixture(path) {
  if (path === '/sensitive') return `<!doctype html><html><head><title>Account</title></head><body>
    <h1>PRIVATE ACCOUNT NAME</h1>
    <label>비밀번호 <input type="password" value="PASSWORD_SECRET"></label>
    <label>카드 번호 <input autocomplete="cc-number" value="CARD_SECRET"></label>
    <a href="/details">도움말</a>
  </body></html>`;
  if (path === '/payment') return `<!doctype html><html><head><title>Order</title></head><body>
    <form method="post" action="/checkout"><button>계속</button></form>
  </body></html>`;
  if (path === '/details') return '<!doctype html><html><head><title>Details</title></head><body><h1>가벼운 운동화 상세</h1></body></html>';
  if (path === '/results') return '<!doctype html><html><head><title>Results</title></head><body><h1>검색 결과</h1></body></html>';
  if (path === '/product') return `<!doctype html><html><head><title>Product</title></head><body>
    <h1>공개 러닝화</h1><p>무게 220g, 공개 상품 설명</p>
    <button>Buy now</button><a href="/checkout">Checkout</a>
    <iframe src="https://widgets.other.example/reviews"></iframe>
    <a href="/details">공개 상세 보기</a>
  </body></html>`;
  if (path === '/session') return `<!doctype html><html><head><title>Dashboard</title></head><body>
    <h1>Welcome</h1><p>PRIVATE ORDER CONTENT</p><button>Log out</button>
  </body></html>`;
  if (path === '/account') return '<!doctype html><html><head><title>My Account</title></head><body><h1>My Account</h1><p>PRIVATE ACCOUNT CONTENT</p></body></html>';
  if (path === '/cart') return '<!doctype html><html><head><title>Basket</title></head><body><h1>PRIVATE CART CONTENT</h1></body></html>';
  if (path === '/signed-in-product') return `<!doctype html><html><head><title>Public Running Shoe</title></head><body>
    <header class="site-header"><nav><a href="/details">Store home</a></nav>
      <section class="account-menu"><span>signed-user@example.com</span><a href="/account">My account</a><button>Log out</button></section>
    </header>
    <main itemscope itemtype="https://schema.org/Product"><h1>공개 러닝화</h1><p>무게 220g, 로그인 후에도 공개되는 상품 설명</p>
      <a href="/details">공개 상세 보기</a><button>Add to cart</button></main>
  </body></html>`;
  return `<!doctype html><html><head><meta name="viewport" content="width=device-width,initial-scale=1"><title>Vitlane fixture</title></head><body>
    <h1>상품 검색</h1>
    <form method="get" action="/results"><label>검색어 <input type="search" name="q" id="q"></label><button>검색</button></form>
    <input type="hidden" value="HIDDEN_SECRET"><div hidden>HIDDEN_TEXT</div>
    <a id="details" href="/details" target="_blank">상품 상세</a>
    <a href="https://other.example.com/details">다른 사이트</a>
    <a href="javascript:alert(1)">위험한 링크</a>
    <a href="http://127.0.0.1/private">PRIVATE NETWORK TEXT</a>
    <form method="post" action="/review"><p>PRIVATE FORM SUMMARY</p><button>Review</button></form>
    <button id="generic">일반 동작</button>
    <script>window.__laneAgent={snapshot:()=>({visibleText:'ATTACK'})}; window.sent=0; document.querySelector('#q').addEventListener('input',()=>window.sent++);</script>
    <div style="height:2000px">아래로 스크롤</div>
  </body></html>`;
}

async function pageFor(t, path = '/') {
  const context = await browser.newContext({ viewport: { width: 412, height: 915 }, isMobile: true });
  await context.route('https://shop.example.com/**', async (route) => {
    const url = new URL(route.request().url());
    await route.fulfill({ status: 200, contentType: 'text/html; charset=utf-8', body: fixture(url.pathname) });
  });
  t.after(() => context.close());
  const page = await context.newPage();
  await page.goto(`https://shop.example.com${path}`);
  return page;
}

async function syntheticCoupangPage(t, url, body) {
  const context = await browser.newContext({ viewport: { width: 412, height: 915 }, isMobile: true });
  await context.route('**/*', async (route) => {
    if (route.request().isNavigationRequest() && route.request().url() === url) {
      await route.fulfill({ status: 200, contentType: 'text/html; charset=utf-8', body });
      return;
    }
    await route.abort('blockedbyclient');
  });
  t.after(() => context.close());
  const page = await context.newPage();
  await page.goto(url);
  return page;
}

function coupangItem(number, {
  quantity = 1,
  unitPriceCeilingKrw = 10_000,
  options = { kind: 'none' },
} = {}) {
  const productId = String(8800000000 + number);
  const itemId = String(25000000000 + number);
  const vendorItemId = String(92000000000 + number);
  return {
    query: `synthetic product ${number}`,
    productUrl: `https://www.coupang.com/vp/products/${productId}?itemId=${itemId}&vendorItemId=${vendorItemId}`,
    offerIdentity: { productId, itemId, vendorItemId },
    quantity,
    unitPriceCeilingKrw,
    linePriceCeilingKrw: unitPriceCeilingKrw * quantity,
    options,
  };
}

function coupangPlan(mode, items) {
  const unsigned = {
    merchantId: 'COUPANG',
    recipeVersion: '1',
    mode,
    approval: {
      approvalId: `approval_browser_${mode}`,
      currency: 'KRW',
      revision: 1,
      expiresAt: new Date(Date.now() + 5 * 60_000).toISOString(),
      totalPriceCeilingKrw: items.reduce((sum, item) => sum + item.linePriceCeilingKrw, 0),
    },
    items,
  };
  return buildPurchasePreparationPlan({
    ...unsigned,
    approval: { ...unsigned.approval, approvalDigest: computePurchaseApprovalDigest(unsigned) },
  });
}

function coupangAction(plan, stepId, url, targetLineIndex) {
  const step = plan.steps.find((candidate) => candidate.action.stepId === stepId &&
    (targetLineIndex === undefined || candidate.action.bindings.targetLineIndex === targetLineIndex));
  assert.ok(step, `missing ${stepId} recipe step`);
  const pageUrl = new URL(url);
  return {
    kind: 'run_preparation_step',
    adapterId: step.action.adapterId,
    recipeVersion: step.action.recipeVersion,
    stepId: step.action.stepId,
    bindings: {
      ...step.action.bindings,
      expectedOrigin: pageUrl.origin,
      expectedPath: pageUrl.pathname,
    },
    reason: 'synthetic approved Coupang browser step',
  };
}

function coupangMetadata(page, epoch = 1) {
  const url = new URL(page.url());
  return {
    ...metadata(epoch),
    topOrigin: url.origin,
    frameOrigin: url.origin,
    pathname: url.pathname,
    queryOrFragmentPresent: Boolean(url.search || url.hash),
  };
}

function coupangProductFixture({
  unitPriceKrw = 9_000,
  quantity = 1,
  options = false,
  duplicateOption = false,
  buyButtons = 1,
  addButtons = 1,
} = {}) {
  const optionControl = options ? `
    <label for="color">색상</label>
    <select id="color"><option value="">선택</option><option value="black">블랙</option><option value="navy">네이비</option></select>
    ${duplicateOption ? '<label for="color-copy">색상</label><select id="color-copy"><option value="">선택</option><option value="black-copy">블랙</option></select>' : ''}` : '';
  return `<!doctype html><html><head><title>Synthetic Coupang product</title></head><body>
    <main itemscope itemtype="https://schema.org/Product">
      <h1>합성 쿠팡 상품</h1>
      ${optionControl}
      <strong data-vitlane-unit-price-krw="${unitPriceKrw}">${unitPriceKrw.toLocaleString('en-US')}원</strong>
      <label for="quantity">수량</label><input id="quantity" name="quantity" type="number" min="1" value="${quantity}">
      ${Array.from({ length: buyButtons }, (_, index) => `<button type="button" class="buy-now">${index % 2 ? '바로 구매' : '바로구매'}</button>`).join('')}
      ${Array.from({ length: addButtons }, () => '<button type="button" class="add-cart">장바구니 담기</button>').join('')}
      <button type="button" id="final-payment">결제하기</button>
    </main>
    <script>
      window.__coupangEvents = { buy: 0, add: 0, payment: 0, quantity: 0 };
      document.querySelectorAll('.buy-now').forEach((button) => button.addEventListener('click', () => window.__coupangEvents.buy++));
      document.querySelectorAll('.add-cart').forEach((button) => button.addEventListener('click', () => window.__coupangEvents.add++));
      document.querySelector('#final-payment').addEventListener('click', () => window.__coupangEvents.payment++);
      document.querySelector('#quantity').addEventListener('change', () => window.__coupangEvents.quantity++);
    </script>
  </body></html>`;
}

function coupangCartFixture(items, {
  extraSelectedItems = [],
  checkoutButtons = ['2개 상품 구매하기'],
} = {}) {
  const row = (item) => `<article data-vitlane-cart-line>
    <input type="checkbox" aria-label="상품 선택" checked>
    <a href="${item.productUrl}">합성 상품 ${item.offerIdentity.productId}</a>
    <span data-vitlane-quantity="${item.quantity}">${item.quantity}</span>
    <strong data-vitlane-unit-price-krw="${item.unitPriceCeilingKrw - 1}">${(item.unitPriceCeilingKrw - 1).toLocaleString('en-US')}원</strong>
  </article>`;
  return `<!doctype html><html><head><title>Synthetic cart</title></head><body>
    <main data-vitlane-cart>
      <h1>검토할 상품</h1>
      ${[...items, ...extraSelectedItems].map(row).join('')}
      ${checkoutButtons.map((name) => `<button type="button" class="start-checkout">${name}</button>`).join('')}
      <button type="button" id="final-payment">결제하기</button>
    </main>
    <script>
      window.__coupangEvents = { checkout: 0, payment: 0 };
      document.querySelectorAll('.start-checkout').forEach((button) => button.addEventListener('click', () => window.__coupangEvents.checkout++));
      document.querySelector('#final-payment').addEventListener('click', () => window.__coupangEvents.payment++);
    </script>
  </body></html>`;
}

async function agent(page) {
  const cdp = await page.context().newCDPSession(page);
  const { frameTree } = await cdp.send('Page.getFrameTree');
  const { executionContextId } = await cdp.send('Page.createIsolatedWorld', {
    frameId: frameTree.frame.id,
    worldName: 'vitlane-protocol-v1-test',
    grantUniveralAccess: false,
  });
  async function evaluate(expression) {
    const result = await cdp.send('Runtime.evaluate', { expression, contextId: executionContextId, returnByValue: true });
    if (result.exceptionDetails) throw new Error(result.exceptionDetails.exception?.description || 'Script failed');
    return result.result.value;
  }
  await evaluate(source);
  return {
    snapshot: (metadata) => evaluate(`__laneAgent.snapshot(${JSON.stringify(metadata)})`),
    inspect: (command) => evaluate(`__laneAgent.inspect(${JSON.stringify(command)})`),
    execute: (command) => evaluate(`__laneAgent.execute(${JSON.stringify(command)})`),
  };
}

function metadata(epoch = 1) {
  return {
    tabId: 'tab_fixture',
    frameId: 'frame_main',
    documentEpoch: epoch,
    topOrigin: 'https://shop.example.com',
    frameOrigin: 'https://shop.example.com',
    pathname: '/',
    queryOrFragmentPresent: false,
    foreground: true,
  };
}

function metadataFor(page, epoch = 1) {
  const url = new URL(page.url());
  return {
    ...metadata(epoch),
    topOrigin: url.origin,
    frameOrigin: url.origin,
    pathname: url.pathname,
    queryOrFragmentPresent: Boolean(url.search || url.hash),
  };
}

function pageIdentity(observation) {
  return {
    tabId: observation.nativeMetadata.tabId,
    frameId: observation.nativeMetadata.frameId,
    documentEpoch: observation.nativeMetadata.documentEpoch,
    observationId: observation.observationId,
    topOrigin: observation.nativeMetadata.topOrigin,
    frameOrigin: observation.nativeMetadata.frameOrigin,
    pathname: observation.nativeMetadata.pathname,
    queryOrFragmentPresent: observation.nativeMetadata.queryOrFragmentPresent,
  };
}

function command(observation, action, overrides = {}) {
  return {
    protocolVersion: 1,
    commandId: 'cmd_fixture_1',
    runId: 'run_fixture',
    deviceId: 'device_fixture',
    profileRef: 'profile_fixture',
    sequence: 1,
    leaseEpoch: 1,
    controlGeneration: 0,
    expiresAt: new Date(Date.now() + 30_000).toISOString(),
    actionHash: 'a'.repeat(64),
    serverPermit: `hmac-sha256:${'A'.repeat(43)}`,
    action: { ...action, page: pageIdentity(observation) },
    ...overrides,
  };
}

test('observation is structured, ignores page-world overrides, and exposes only bounded safe candidates', async (t) => {
  const page = await pageFor(t);
  const a = await agent(page);
  const observation = await a.snapshot(metadataFor(page));
  assert.equal(observation.nativeMetadata.topOrigin, 'https://shop.example.com');
  assert.equal(observation.privacy.inputValuesOmitted, true);
  assert.equal(observation.privacy.screenshotIncluded, false);
  assert.equal(observation.privacy.collectionStatus, 'sanitized');
  assert.match(observation.untrustedPageData.visibleText, /상품 검색/);
  const serialized = JSON.stringify(observation);
  for (const forbidden of [
    'ATTACK', 'HIDDEN_SECRET', 'HIDDEN_TEXT', 'javascript:', '127.0.0.1',
    'other.example.com', 'PRIVATE NETWORK TEXT', 'PRIVATE FORM SUMMARY',
  ]) {
    assert.equal(serialized.includes(forbidden), false, forbidden);
  }
  assert.deepEqual(observation.untrustedPageData.candidates.map((item) => item.kind).sort(), ['public_search', 'safe_link']);
  assert.deepEqual(observation.privacy.handoffReasonCodes, []);
  assert.ok(observation.privacy.excludedBoundaryCodes.includes('UNSUPPORTED_INTERACTION'));
  assert.ok(observation.privacy.excludedBoundaryCodes.includes('PRIVATE_NETWORK_BLOCKED'));
});

test('password, card, and payment-semantic pages block collection and require handoff', async (t) => {
  for (const path of ['/sensitive', '/payment']) {
    const page = await pageFor(t, path);
    const a = await agent(page);
    const observation = await a.snapshot(metadataFor(page));
    assert.equal(observation.privacy.collectionStatus, 'handoff_required', path);
    assert.equal(observation.untrustedPageData.visibleText, '');
    assert.deepEqual(observation.untrustedPageData.candidates, []);
    const serialized = JSON.stringify(observation);
    for (const secret of ['PRIVATE ACCOUNT NAME', 'PASSWORD_SECRET', 'CARD_SECRET']) assert.equal(serialized.includes(secret), false);
  }
});

test('product copy remains observable while buy controls and cross-origin frames are omitted', async (t) => {
  const page = await pageFor(t, '/product');
  const a = await agent(page);
  const observation = await a.snapshot(metadataFor(page));
  assert.equal(observation.privacy.collectionStatus, 'sanitized');
  assert.match(observation.untrustedPageData.visibleText, /공개 상품 설명/);
  assert.equal(observation.untrustedPageData.visibleText.includes('Buy now'), false);
  assert.equal(observation.untrustedPageData.candidates.some((item) => item.href?.includes('checkout')), false);
  assert.deepEqual(observation.privacy.handoffReasonCodes, []);
  assert.ok(observation.privacy.excludedBoundaryCodes.includes('PAYMENT_OR_COMMITMENT'));
  assert.ok(observation.privacy.excludedBoundaryCodes.includes('CROSS_ORIGIN_FRAME'));
  assert.deepEqual(validateObservation(observation), observation);
});

test('a signed-in public product remains observable with private account chrome removed', async (t) => {
  const page = await pageFor(t, '/signed-in-product');
  const a = await agent(page);
  const observation = await a.snapshot(metadataFor(page));
  assert.equal(observation.privacy.collectionStatus, 'sanitized');
  assert.match(observation.untrustedPageData.visibleText, /로그인 후에도 공개되는 상품 설명/);
  assert.equal(observation.untrustedPageData.pageTypeHint, 'product');
  assert.equal(observation.untrustedPageData.candidates.some((item) => item.name === '공개 상세 보기'), true);
  const serialized = JSON.stringify(observation);
  for (const privateValue of ['signed-user@example.com', 'My account', 'Log out', 'Store home']) {
    assert.equal(serialized.includes(privateValue), false, privateValue);
  }
  assert.ok(observation.privacy.excludedBoundaryCodes.includes('UNSUPPORTED_INTERACTION'));
  assert.ok(observation.privacy.excludedBoundaryCodes.includes('PAYMENT_OR_COMMITMENT'));
  assert.deepEqual(observation.privacy.handoffReasonCodes, []);
  assert.deepEqual(validateObservation(observation), observation);
});

test('private account, session, and cart surfaces fail closed without page content', async (t) => {
  for (const [path, secret] of [
    ['/account', 'PRIVATE ACCOUNT CONTENT'],
    ['/session', 'PRIVATE ORDER CONTENT'],
    ['/cart', 'PRIVATE CART CONTENT'],
  ]) {
    const page = await pageFor(t, path);
    const a = await agent(page);
    const observation = await a.snapshot(metadataFor(page));
    assert.equal(observation.privacy.collectionStatus, 'handoff_required', path);
    assert.equal(observation.untrustedPageData.visibleText, '');
    assert.deepEqual(observation.untrustedPageData.candidates, []);
    assert.equal(JSON.stringify(observation).includes(secret), false);
  }
});

test('the only input mutation is a public GET search preparation; it never submits and cannot replay', async (t) => {
  const page = await pageFor(t);
  const a = await agent(page);
  const observation = await a.snapshot(metadataFor(page));
  const search = observation.untrustedPageData.candidates.find((item) => item.kind === 'public_search');
  const cmd = command(observation, {
    kind: 'run_preparation_step', adapterId: 'builtin.public-search', recipeVersion: '1', stepId: 'prepare_query',
    bindings: { candidateRef: search.candidateRef, query: '가벼운 운동화' }, reason: '공개 검색어 준비',
  });
  assert.equal((await a.inspect(cmd)).effect, 'public_search_prepare');
  assert.deepEqual(await a.execute(cmd), { commandId: 'cmd_fixture_1', status: 'applied', code: 'PUBLIC_SEARCH_PREPARED' });
  assert.equal(await page.locator('#q').inputValue(), '가벼운 운동화');
  assert.equal(await page.evaluate(() => window.sent), 0);
  assert.equal(new URL(page.url()).pathname, '/');
  assert.deepEqual(await a.execute(cmd), { commandId: 'cmd_fixture_1', status: 'rejected', code: 'REPLAY_CONFLICT' });
  const afterPreparation = await a.snapshot(metadataFor(page, 2));
  assert.equal(afterPreparation.privacy.collectionStatus, 'handoff_required');
  assert.equal(JSON.stringify(afterPreparation).includes('가벼운 운동화'), false);
});

test('sensitive public-search text, generic click/fill/eval, and changed targets are denied locally', async (t) => {
  const page = await pageFor(t);
  const a = await agent(page);
  let observation = await a.snapshot(metadataFor(page));
  const search = observation.untrustedPageData.candidates.find((item) => item.kind === 'public_search');
  const secret = command(observation, {
    kind: 'run_preparation_step', adapterId: 'builtin.public-search', recipeVersion: '1', stepId: 'prepare_query',
    bindings: { candidateRef: search.candidateRef, query: 'person@example.com' }, reason: 'bad',
  });
  assert.equal((await a.execute(secret)).code, 'POLICY_DENIED');
  for (const action of [
    { kind: 'click', candidateRef: search.candidateRef },
    { kind: 'fill', candidateRef: search.candidateRef, value: 'x' },
    { kind: 'evaluate', script: 'document.cookie' },
  ]) assert.equal((await a.inspect(command(observation, action))).code, 'POLICY_DENIED');

  observation = await a.snapshot(metadataFor(page, 2));
  const link = observation.untrustedPageData.candidates.find((item) => item.kind === 'safe_link');
  await page.locator('#details').evaluate((element) => element.setAttribute('href', '/changed'));
  assert.equal((await a.inspect(command(observation, { kind: 'open_candidate', candidateRef: link.candidateRef, reason: 'open' }))).code, 'REOBSERVE_REQUIRED');
});

test('safe-link action hands control to the user and never starts navigation', async (t) => {
  const page = await pageFor(t);
  const a = await agent(page);
  const observation = await a.snapshot(metadataFor(page));
  const link = observation.untrustedPageData.candidates.find((item) => item.kind === 'safe_link');
  const cmd = command(observation, { kind: 'open_candidate', candidateRef: link.candidateRef, reason: '상세 확인' });
  const check = await a.inspect(cmd);
  assert.equal(check.targetOrigin, 'https://shop.example.com');
  assert.equal(check.targetUrl, 'https://shop.example.com/details');
  const before = page.url();
  const result = await a.execute(cmd);
  assert.deepEqual(result, { commandId: 'cmd_fixture_1', status: 'handoff', code: 'NAVIGATION_REQUIRES_USER' });
  assert.equal(page.url(), before);
  assert.equal(page.context().pages().length, 1);
});

test('page identity, expiry, and single-use state make stale commands deterministic rejections', async (t) => {
  const page = await pageFor(t);
  const a = await agent(page);
  const observation = await a.snapshot(metadataFor(page));
  const scroll = { kind: 'scroll', direction: 'down', reason: '더 보기' };
  const expired = command(observation, scroll, { expiresAt: new Date(Date.now() - 1).toISOString() });
  assert.equal((await a.execute(expired)).code, 'COMMAND_EXPIRED');
  const wrongPage = command(observation, scroll);
  wrongPage.action.page.documentEpoch++;
  assert.equal((await a.execute(wrongPage)).code, 'REOBSERVE_REQUIRED');
  const valid = command(observation, scroll);
  assert.equal((await a.execute(valid)).code, 'VIEWPORT_SCROLLED');
  assert.ok(await page.evaluate(() => scrollY) > 0);
  assert.equal((await a.execute(valid)).code, 'REPLAY_CONFLICT');
});

test('request_human returns a handoff result and performs no page mutation', async (t) => {
  const page = await pageFor(t, '/sensitive');
  const a = await agent(page);
  const observation = await a.snapshot(metadataFor(page));
  const cmd = command(observation, {
    kind: 'request_human', reasonCode: 'AUTHENTICATION_REQUIRED', message: '로그인은 직접 진행해 주세요.', reason: '민감 입력 보호',
  });
  assert.deepEqual(await a.execute(cmd), { commandId: 'cmd_fixture_1', status: 'handoff', code: 'AUTHENTICATION_REQUIRED' });
  assert.equal(new URL(page.url()).pathname, '/sensitive');
});

test('approved Coupang product steps select the exact option, verify price, set quantity, and activate only Buy now', async (t) => {
  const item = coupangItem(1, {
    quantity: 2,
    options: { kind: 'choices', choices: [{ groupName: '색상', valueName: '블랙' }] },
  });
  const plan = coupangPlan('single', [item]);
  const page = await syntheticCoupangPage(t, item.productUrl, coupangProductFixture({
    quantity: 1,
    options: true,
  }));
  const a = await agent(page);

  let observation = await a.snapshot(coupangMetadata(page));
  const select = command(observation, coupangAction(plan, 'select_option', item.productUrl, 0));
  assert.deepEqual(await a.execute(select), {
    commandId: 'cmd_fixture_1', status: 'applied', code: 'COUPANG_OPTION_SELECTED',
  });
  assert.equal(await page.locator('#color').inputValue(), 'black');

  observation = await a.snapshot(coupangMetadata(page));
  const verify = command(observation, coupangAction(plan, 'verify_options', item.productUrl, 0));
  assert.deepEqual(await a.execute(verify), {
    commandId: 'cmd_fixture_1', status: 'applied', code: 'COUPANG_OPTIONS_VERIFIED',
  });

  observation = await a.snapshot(coupangMetadata(page));
  const quantity = command(observation, coupangAction(plan, 'set_quantity', item.productUrl, 0));
  assert.deepEqual(await a.execute(quantity), {
    commandId: 'cmd_fixture_1', status: 'applied', code: 'COUPANG_QUANTITY_SET',
  });
  assert.equal(await page.locator('#quantity').inputValue(), '2');
  assert.equal(await page.evaluate(() => window.__coupangEvents.quantity), 1);

  observation = await a.snapshot(coupangMetadata(page));
  const buyNow = command(observation, coupangAction(plan, 'buy_now', item.productUrl, 0));
  assert.deepEqual(await a.execute(buyNow), {
    commandId: 'cmd_fixture_1', status: 'handoff', code: 'COUPANG_BUY_NOW_ACTIVATED',
  });
  assert.deepEqual(await page.evaluate(() => window.__coupangEvents), {
    buy: 1, add: 0, payment: 0, quantity: 1,
  });
});

test('approved Coupang multi-item route activates Add to cart instead of Buy now or payment', async (t) => {
  const items = [coupangItem(1), coupangItem(2)];
  const plan = coupangPlan('multi', items);
  const page = await syntheticCoupangPage(t, items[0].productUrl, coupangProductFixture());
  const a = await agent(page);
  const observation = await a.snapshot(coupangMetadata(page));
  const addToCart = command(observation, coupangAction(plan, 'add_to_cart', items[0].productUrl, 0));

  assert.deepEqual(await a.execute(addToCart), {
    commandId: 'cmd_fixture_1', status: 'applied', code: 'COUPANG_ADD_TO_CART_ACTIVATED',
  });
  assert.deepEqual(await page.evaluate(() => window.__coupangEvents), {
    buy: 0, add: 1, payment: 0, quantity: 0,
  });
});

test('approved Coupang cart step requires the exact selected lines and stops at checkout review', async (t) => {
  const items = [coupangItem(1), coupangItem(2, { quantity: 2 })];
  const plan = coupangPlan('multi', items);
  const cartUrl = 'https://cart.coupang.com/cartView.pang';
  const page = await syntheticCoupangPage(t, cartUrl, coupangCartFixture(items));
  const a = await agent(page);
  const observation = await a.snapshot(coupangMetadata(page));
  const startCheckout = command(observation, coupangAction(plan, 'start_checkout', cartUrl));

  assert.deepEqual(await a.execute(startCheckout), {
    commandId: 'cmd_fixture_1', status: 'handoff', code: 'COUPANG_CHECKOUT_REVIEW_ACTIVATED',
  });
  assert.deepEqual(await page.evaluate(() => window.__coupangEvents), { checkout: 1, payment: 0 });
  assert.equal(page.url(), cartUrl);
});

test('Coupang product controls reject option and Buy now ambiguity plus signed-binding tampering', async (t) => {
  const optionItem = coupangItem(1, {
    options: { kind: 'choices', choices: [{ groupName: '색상', valueName: '블랙' }] },
  });
  const optionPlan = coupangPlan('single', [optionItem]);
  const optionPage = await syntheticCoupangPage(t, optionItem.productUrl, coupangProductFixture({
    options: true,
    duplicateOption: true,
  }));
  const optionAgent = await agent(optionPage);
  const optionObservation = await optionAgent.snapshot(coupangMetadata(optionPage));
  const ambiguousOption = command(optionObservation,
    coupangAction(optionPlan, 'select_option', optionItem.productUrl, 0));
  assert.equal((await optionAgent.inspect(ambiguousOption)).code, 'CONTROL_AMBIGUOUS');
  assert.equal((await optionAgent.execute(ambiguousOption)).code, 'CONTROL_AMBIGUOUS');
  assert.equal(await optionPage.locator('#color').inputValue(), '');
  assert.equal(await optionPage.locator('#color-copy').inputValue(), '');

  const buyItem = coupangItem(2);
  const buyPlan = coupangPlan('single', [buyItem]);
  const buyPage = await syntheticCoupangPage(t, buyItem.productUrl, coupangProductFixture({ buyButtons: 2 }));
  const buyAgent = await agent(buyPage);
  let buyObservation = await buyAgent.snapshot(coupangMetadata(buyPage));
  const ambiguousBuy = command(buyObservation, coupangAction(buyPlan, 'buy_now', buyItem.productUrl, 0));
  assert.equal((await buyAgent.execute(ambiguousBuy)).code, 'CONTROL_AMBIGUOUS');

  buyObservation = await buyAgent.snapshot(coupangMetadata(buyPage));
  const tamperedAction = structuredClone(coupangAction(buyPlan, 'buy_now', buyItem.productUrl, 0));
  tamperedAction.bindings.quantity = 2;
  const tampered = command(buyObservation, tamperedAction);
  assert.equal((await buyAgent.execute(tampered)).code, 'APPROVAL_PAGE_MISMATCH');

  buyObservation = await buyAgent.snapshot(coupangMetadata(buyPage));
  const finalPaymentAction = structuredClone(coupangAction(buyPlan, 'buy_now', buyItem.productUrl, 0));
  finalPaymentAction.stepId = 'pay_now';
  const finalPayment = command(buyObservation, finalPaymentAction);
  assert.equal((await buyAgent.execute(finalPayment)).code, 'POLICY_DENIED');
  assert.deepEqual(await buyPage.evaluate(() => window.__coupangEvents), {
    buy: 0, add: 0, payment: 0, quantity: 0,
  });

  const duplicateIdentityUrl = `${buyItem.productUrl}&itemId=99999`;
  const duplicateIdentityPage = await syntheticCoupangPage(
    t,
    duplicateIdentityUrl,
    coupangProductFixture(),
  );
  const duplicateIdentityAgent = await agent(duplicateIdentityPage);
  const duplicateIdentityObservation = await duplicateIdentityAgent.snapshot(
    coupangMetadata(duplicateIdentityPage),
  );
  const duplicateIdentity = command(
    duplicateIdentityObservation,
    coupangAction(buyPlan, 'buy_now', duplicateIdentityUrl, 0),
  );
  assert.equal((await duplicateIdentityAgent.execute(duplicateIdentity)).code, 'APPROVAL_PAGE_MISMATCH');
  assert.equal(await duplicateIdentityPage.evaluate(() => window.__coupangEvents.buy), 0);

  const undeclaredItem = coupangItem(4);
  const undeclaredPlan = coupangPlan('single', [undeclaredItem]);
  const undeclaredPage = await syntheticCoupangPage(t, undeclaredItem.productUrl,
    coupangProductFixture({ options: true }));
  const undeclaredAgent = await agent(undeclaredPage);
  const undeclaredObservation = await undeclaredAgent.snapshot(coupangMetadata(undeclaredPage));
  const undeclaredBuy = command(undeclaredObservation,
    coupangAction(undeclaredPlan, 'buy_now', undeclaredItem.productUrl, 0));
  assert.equal((await undeclaredAgent.execute(undeclaredBuy)).code, 'PRODUCT_STATE_MISMATCH');
  assert.equal(await undeclaredPage.evaluate(() => window.__coupangEvents.buy), 0);
});

test('Coupang cart rejects extra selected lines, ambiguous checkout controls, and final-payment substitution', async (t) => {
  const items = [coupangItem(1), coupangItem(2)];
  const plan = coupangPlan('multi', items);
  const cartUrl = 'https://cart.coupang.com/cartView.pang';

  const tamperedPage = await syntheticCoupangPage(t, cartUrl, coupangCartFixture(items, {
    extraSelectedItems: [coupangItem(3)],
  }));
  const tamperedAgent = await agent(tamperedPage);
  const tamperedObservation = await tamperedAgent.snapshot(coupangMetadata(tamperedPage));
  const tamperedCart = command(tamperedObservation, coupangAction(plan, 'start_checkout', cartUrl));
  assert.equal((await tamperedAgent.execute(tamperedCart)).code, 'CART_SELECTION_MISMATCH');
  assert.deepEqual(await tamperedPage.evaluate(() => window.__coupangEvents), { checkout: 0, payment: 0 });

  const ambiguousPage = await syntheticCoupangPage(t, cartUrl, coupangCartFixture(items, {
    checkoutButtons: ['2개 상품 구매하기', '선택상품 구매하기'],
  }));
  const ambiguousAgent = await agent(ambiguousPage);
  const ambiguousObservation = await ambiguousAgent.snapshot(coupangMetadata(ambiguousPage));
  const ambiguousCart = command(ambiguousObservation, coupangAction(plan, 'start_checkout', cartUrl));
  assert.equal((await ambiguousAgent.execute(ambiguousCart)).code, 'CONTROL_AMBIGUOUS');
  assert.deepEqual(await ambiguousPage.evaluate(() => window.__coupangEvents), { checkout: 0, payment: 0 });

  const paymentOnlyPage = await syntheticCoupangPage(t, cartUrl, coupangCartFixture(items, {
    checkoutButtons: [],
  }));
  const paymentOnlyAgent = await agent(paymentOnlyPage);
  const paymentOnlyObservation = await paymentOnlyAgent.snapshot(coupangMetadata(paymentOnlyPage));
  const paymentOnlyCart = command(paymentOnlyObservation, coupangAction(plan, 'start_checkout', cartUrl));
  assert.equal((await paymentOnlyAgent.execute(paymentOnlyCart)).code, 'CONTROL_NOT_FOUND');
  assert.deepEqual(await paymentOnlyPage.evaluate(() => window.__coupangEvents), { checkout: 0, payment: 0 });
});
