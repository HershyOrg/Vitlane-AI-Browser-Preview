import { randomBytes } from 'node:crypto';
import { readFile } from 'node:fs/promises';
import assert from 'node:assert/strict';
import test, { after, before } from 'node:test';

import { chromium } from 'playwright';

import {
  buildSingleApproval,
  executeApprovedSingle,
  LiveCoupangError,
  parseOptionLines,
} from '../../scripts/lib/live-coupang-single.mjs';

const PAGE_AGENT_SOURCE = await readFile(new URL('../../agent/page-agent.js', import.meta.url), 'utf8');
const PRODUCT_URL = 'https://www.coupang.com/vp/products/8825648110' +
  '?itemId=25717201283&vendorItemId=92706038164';
let browser;

before(async () => {
  browser = await chromium.launch({ channel: process.env.PLAYWRIGHT_CHANNEL || undefined, headless: true });
});

after(async () => { await browser?.close(); });

function productFixture({ buyButtons = 1 } = {}) {
  return `<!doctype html><html><head><title>합성 쿠팡 상품</title></head><body>
    <main itemscope itemtype="https://schema.org/Product">
      <h1>합성 상품</h1>
      <label for="color">색상</label>
      <select id="color"><option value="">선택</option><option value="black">블랙</option></select>
      <strong data-vitlane-unit-price-krw="11900">11,900원</strong>
      <label for="quantity">수량</label>
      <input id="quantity" name="quantity" type="number" min="1" value="1">
      ${Array.from({ length: buyButtons }, () => '<button type="button" class="buy">바로구매</button>').join('')}
      <button type="button" id="payment">결제하기</button>
    </main>
    <script>
      window.__liveEvents = { buy: 0, payment: 0, quantity: 0, option: 0 };
      document.querySelectorAll('.buy').forEach((button) => button.addEventListener('click', () => window.__liveEvents.buy++));
      document.querySelector('#payment').addEventListener('click', () => window.__liveEvents.payment++);
      document.querySelector('#quantity').addEventListener('change', () => window.__liveEvents.quantity++);
      document.querySelector('#color').addEventListener('change', () => window.__liveEvents.option++);
    </script>
  </body></html>`;
}

async function pageFor(t, fixture = productFixture()) {
  const context = await browser.newContext({ locale: 'ko-KR', viewport: { width: 1280, height: 900 } });
  t.after(() => context.close());
  await context.route('**/*', async (route) => {
    if (route.request().isNavigationRequest() && new URL(route.request().url()).origin === 'https://www.coupang.com') {
      await route.fulfill({ status: 200, contentType: 'text/html; charset=utf-8', body: fixture });
      return;
    }
    await route.abort('blockedbyclient');
  });
  const page = await context.newPage();
  await page.goto(PRODUCT_URL);
  return page;
}

function approval() {
  return buildSingleApproval({
    productUrl: PRODUCT_URL,
    quantity: 2,
    unitPriceCeilingKrw: 12_000,
    totalPriceCeilingKrw: 24_000,
    options: parseOptionLines('색상=블랙'),
    approvalId: 'approval_mac_browser',
  });
}

test('Mac live core executes the signed fixed sequence and clicks Buy now once', async (t) => {
  const page = await pageFor(t);
  const steps = [];
  const result = await executeApprovedSingle({
    page,
    approvedInput: approval(),
    permitKey: randomBytes(32),
    pageAgentSource: PAGE_AGENT_SOURCE,
    onStep: async (step) => steps.push(step),
  });

  assert.deepEqual(steps, [
    { stepId: 'select_option', status: 'applied', code: 'COUPANG_OPTION_SELECTED' },
    { stepId: 'verify_options', status: 'applied', code: 'COUPANG_OPTIONS_VERIFIED' },
    { stepId: 'set_quantity', status: 'applied', code: 'COUPANG_QUANTITY_SET' },
    { stepId: 'buy_now', status: 'handoff', code: 'COUPANG_BUY_NOW_ACTIVATED' },
  ]);
  assert.equal(result.status, 'handoff');
  assert.equal(result.buyNowAttempted, true);
  assert.deepEqual(await page.evaluate(() => window.__liveEvents), {
    buy: 1,
    payment: 0,
    quantity: 1,
    option: 1,
  });
});

test('Mac live core fails closed when the real page has ambiguous Buy now controls', async (t) => {
  const page = await pageFor(t, productFixture({ buyButtons: 2 }));
  await assert.rejects(executeApprovedSingle({
    page,
    approvedInput: approval(),
    permitKey: randomBytes(32),
    pageAgentSource: PAGE_AGENT_SOURCE,
  }), (error) => error instanceof LiveCoupangError && error.code === 'CONTROL_AMBIGUOUS');
  assert.deepEqual(await page.evaluate(() => window.__liveEvents), {
    buy: 0,
    payment: 0,
    quantity: 1,
    option: 1,
  });
});

test('Mac live core rechecks the exact approved tab URL before every step', async (t) => {
  const page = await pageFor(t);
  await assert.rejects(executeApprovedSingle({
    page,
    approvedInput: approval(),
    permitKey: randomBytes(32),
    pageAgentSource: PAGE_AGENT_SOURCE,
    onStep: async ({ stepId }) => {
      if (stepId === 'select_option') {
        await page.evaluate(() => history.replaceState(null, '', `${location.pathname}?itemId=25717201283&vendorItemId=92706038164&changed=1`));
      }
    },
  }), (error) => error instanceof LiveCoupangError && error.code === 'APPROVAL_PAGE_MISMATCH');
  assert.deepEqual(await page.evaluate(() => window.__liveEvents), {
    buy: 0,
    payment: 0,
    quantity: 0,
    option: 1,
  });
});
