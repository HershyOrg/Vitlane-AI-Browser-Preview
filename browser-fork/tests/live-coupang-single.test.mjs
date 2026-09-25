import assert from 'node:assert/strict';
import test from 'node:test';

import {
  buildSingleApproval,
  LiveCoupangError,
  parseCoupangProductUrl,
  parseOptionLines,
} from '../scripts/lib/live-coupang-single.mjs';
import {
  buildPurchasePreparationPlan,
  verifyPurchaseApprovalDigest,
} from '../server/purchase-recipes.mjs';

const PRODUCT_URL = 'https://www.coupang.com/vp/products/8825648110' +
  '?itemId=25717201283&vendorItemId=92706038164';

function code(error, expected) {
  return error instanceof LiveCoupangError && error.code === expected;
}

test('live URL parser preserves only the exact Coupang offer identity', () => {
  const parsed = parseCoupangProductUrl(`${PRODUCT_URL}&sourceType=search&traceId=public`);
  assert.equal(parsed.productUrl, PRODUCT_URL);
  assert.deepEqual(parsed.offerIdentity, {
    productId: '8825648110',
    itemId: '25717201283',
    vendorItemId: '92706038164',
  });
  assert.equal(Object.isFrozen(parsed), true);
  assert.equal(Object.isFrozen(parsed.offerIdentity), true);
});

test('live URL parser rejects missing, duplicate, or non-Coupang offer identity', () => {
  for (const value of [
    'https://www.coupang.com/vp/products/8825648110',
    `${PRODUCT_URL}&itemId=999`,
    `${PRODUCT_URL}&vendorItemId=999`,
    `${PRODUCT_URL}#checkout`,
    PRODUCT_URL.replace('www.coupang.com', 'login.coupang.com'),
    PRODUCT_URL.replace('/vp/products/', '/np/products/'),
    PRODUCT_URL.replace('https://', 'http://'),
  ]) {
    assert.throws(() => parseCoupangProductUrl(value), LiveCoupangError, value);
  }
});

test('option lines become one exact value per group', () => {
  assert.deepEqual(parseOptionLines(''), { kind: 'none' });
  assert.deepEqual(parseOptionLines(' 색상 = 블랙\n사이즈=270 '), {
    kind: 'choices',
    choices: [
      { groupName: '색상', valueName: '블랙' },
      { groupName: '사이즈', valueName: '270' },
    ],
  });
  assert.throws(() => parseOptionLines('색상=블랙\n색상=네이비'), (error) => code(error, 'OPTION_INPUT_INVALID'));
  assert.throws(() => parseOptionLines('색상=블랙=대형'), (error) => code(error, 'OPTION_INPUT_INVALID'));
});

test('single approval binds identity, options, quantity, ceilings, expiry, and digest', () => {
  const before = Date.now();
  const approved = buildSingleApproval({
    productUrl: `${PRODUCT_URL}&sourceType=search`,
    quantity: 2,
    unitPriceCeilingKrw: 12_000,
    totalPriceCeilingKrw: 24_000,
    options: parseOptionLines('색상=블랙'),
    approvalId: 'approval_mac_test',
  });
  const plan = buildPurchasePreparationPlan(approved);
  assert.equal(verifyPurchaseApprovalDigest(approved), true);
  assert.equal(plan.mode, 'single');
  assert.equal(plan.route, 'single_buy_now');
  assert.equal(plan.items.length, 1);
  assert.equal(plan.items[0].productUrl, PRODUCT_URL);
  assert.equal(plan.items[0].quantity, 2);
  assert.equal(plan.items[0].unitPriceCeilingKrw, 12_000);
  assert.equal(plan.items[0].linePriceCeilingKrw, 24_000);
  assert.deepEqual(plan.items[0].options, {
    kind: 'choices',
    choices: [{ groupName: '색상', valueName: '블랙' }],
  });
  const expiry = Date.parse(approved.approval.expiresAt);
  assert.ok(expiry > before);
  assert.ok(expiry <= before + 5 * 60_000 + 100);
  assert.match(approved.approval.approvalDigest, /^sha256:[a-f0-9]{64}$/);
  assert.equal(Object.isFrozen(approved), true);
  assert.equal(Object.isFrozen(approved.items[0]), true);
});

test('single approval rejects a total ceiling below quantity times the unit ceiling', () => {
  assert.throws(() => buildSingleApproval({
    productUrl: PRODUCT_URL,
    quantity: 2,
    unitPriceCeilingKrw: 12_000,
    totalPriceCeilingKrw: 23_999,
    options: { kind: 'none' },
  }), (error) => code(error, 'PRICE_CEILING_INVALID'));
});

test('single approval requires explicit normalized option intent', () => {
  assert.throws(() => buildSingleApproval({
    productUrl: PRODUCT_URL,
    quantity: 1,
    unitPriceCeilingKrw: 12_000,
    totalPriceCeilingKrw: 12_000,
  }), (error) => code(error, 'OPTION_INPUT_INVALID'));
});
