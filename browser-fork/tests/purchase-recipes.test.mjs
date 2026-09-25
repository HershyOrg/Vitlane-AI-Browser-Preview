import assert from 'node:assert/strict';
import test from 'node:test';
import {
  PURCHASE_PREPARATION_EFFECT,
  PURCHASE_PREPARATION_MODE,
  PURCHASE_PREPARATION_STAGE,
  PURCHASE_RECIPE_REGISTRY,
  PurchaseRecipeError,
  COUPANG_PURCHASE_ADAPTER_ID,
  COUPANG_PURCHASE_STEP_ID,
  buildPurchasePreparationPlan,
  computePurchaseApprovalDigest,
  normalizePurchasePreparationPlanInput,
  resolvePurchasePreparationStep,
} from '../server/purchase-recipes.mjs';

const noOptions = { kind: 'none' };

function item(number, options = noOptions) {
  const productId = String(8800000000 + number);
  const itemId = String(25000000000 + number);
  const vendorItemId = String(92000000000 + number);
  return {
    query: `테스트 상품 ${number}`,
    productUrl: `https://www.coupang.com/vp/products/${productId}?itemId=${itemId}&vendorItemId=${vendorItemId}&sourceType=search`,
    offerIdentity: { productId, itemId, vendorItemId },
    quantity: number,
    unitPriceCeilingKrw: 10_000,
    linePriceCeilingKrw: 10_000 * number,
    options,
  };
}

function input(mode = 'single', items = [item(1)]) {
  const unsigned = {
    merchantId: 'COUPANG',
    recipeVersion: '1',
    mode,
    approval: {
      approvalId: 'approval_test_1',
      currency: 'KRW',
      revision: 3,
      expiresAt: new Date(Date.now() + 5 * 60_000).toISOString(),
      totalPriceCeilingKrw: 1_000_000,
    },
    items,
  };
  return {
    ...unsigned,
    approval: { ...unsigned.approval, approvalDigest: computePurchaseApprovalDigest(unsigned) },
  };
}

function observation({
  origin = 'https://www.coupang.com',
  pathname = '/vp/products/8800000001',
  pageState = 'product',
  optionState = 'not_required',
  semanticElements = [],
  offer = {
    offerIdentity: { productId: '8800000001', itemId: '25000000001', vendorItemId: '92000000001' },
    quantity: 1,
    unitPriceKrw: 9_000,
  },
  cartLines = [],
} = {}) {
  return { origin, pathname, pageState, optionState, semanticElements, offer, cartLines };
}

function element(candidateRef, role, name, overrides = {}) {
  return { candidateRef, role, name, visible: true, enabled: true, ...overrides };
}

function cartLine(source, selected = true, overrides = {}) {
  return {
    lineRef: `line_${source.offerIdentity.itemId}`,
    offerIdentity: source.offerIdentity,
    quantity: source.quantity,
    unitPriceKrw: source.unitPriceCeilingKrw - 1,
    selected,
    ...overrides,
  };
}

test('Coupang recipe v1 exposes exact hosts and a no-final-commitment boundary', () => {
  const recipe = PURCHASE_RECIPE_REGISTRY.COUPANG['1'];
  assert.equal(recipe.recipeId, 'coupang.purchase-preparation@1');
  assert.deepEqual(recipe.allowedOrigins, [
    'https://coupang.com',
    'https://www.coupang.com',
    'https://cart.coupang.com',
  ]);
  assert.equal(recipe.terminalBoundary.finalCommitmentAllowed, false);
  assert.equal(Object.isFrozen(recipe), true);
});

test('approval snapshot normalization strips extras and fixes route authorization fields', () => {
  const source = { ...input(), ignored: 'never signed' };
  const normalized = normalizePurchasePreparationPlanInput(source);
  assert.deepEqual(Object.keys(normalized), ['merchantId', 'recipeVersion', 'mode', 'approval', 'items']);
  assert.equal('ignored' in normalized, false);
  assert.equal(normalized.approval.currency, 'KRW');
  assert.equal(normalized.items[0].offerIdentity.vendorItemId, '92000000001');
  assert.equal(normalized.items[0].unitPriceCeilingKrw, 10_000);
  assert.equal(computePurchaseApprovalDigest(source), source.approval.approvalDigest);
  const changed = { ...source, items: [{ ...source.items[0], quantity: 2, linePriceCeilingKrw: 20_000 }] };
  assert.throws(() => buildPurchasePreparationPlan(changed), (error) => error.code === 'APPROVAL_DIGEST_MISMATCH');
});

test('single-item route uses Buy now and stops at checkout-ready handoff', () => {
  const plan = buildPurchasePreparationPlan(input());
  assert.deepEqual(plan.steps.map((step) => step.stage), [
    'search', 'product', 'options', 'set_quantity', 'buy_now', 'checkout_ready',
  ]);
  assert.equal(plan.steps.some((step) => step.stage === 'add_to_cart'), false);
  assert.equal(plan.finalCommitmentAllowed, false);
  assert.equal(plan.steps.at(-1).action.effect, 'handoff');
  assert.equal(Object.isFrozen(plan.steps), true);
});

test('multi-item route adds every item, then opens cart and advances to checkout-ready', () => {
  const plan = buildPurchasePreparationPlan(input(PURCHASE_PREPARATION_MODE.MULTI, [item(1), item(2)]));
  assert.deepEqual(plan.steps.map((step) => step.stage), [
    'search', 'product', 'options', 'set_quantity', 'add_to_cart',
    'search', 'product', 'options', 'set_quantity', 'add_to_cart',
    'cart', 'cart', 'checkout_ready',
  ]);
  assert.deepEqual(plan.steps.slice(-3).map((step) => step.action.effect), ['navigate', 'activate', 'handoff']);
  assert.equal(plan.steps.some((step) => step.stage === 'buy_now'), false);
});

test('search navigation is deterministic and preserves exact transactional offer identity', () => {
  const planInput = input();
  const plan = buildPurchasePreparationPlan(planInput);
  assert.equal(plan.steps[0].action.targetUrl, 'https://www.coupang.com/np/search?q=%ED%85%8C%EC%8A%A4%ED%8A%B8+%EC%83%81%ED%92%88+1');
  assert.equal(plan.steps[1].action.targetUrl,
    'https://www.coupang.com/vp/products/8800000001?itemId=25000000001&vendorItemId=92000000001');
  assert.deepEqual({
    approval: plan.steps[1].action.bindings.approval,
    productId: plan.steps[1].action.bindings.productId,
    itemId: plan.steps[1].action.bindings.itemId,
    vendorItemId: plan.steps[1].action.bindings.vendorItemId,
    quantity: plan.steps[1].action.bindings.quantity,
    unitPriceCeilingKrw: plan.steps[1].action.bindings.unitPriceCeilingKrw,
    linePriceCeilingKrw: plan.steps[1].action.bindings.linePriceCeilingKrw,
  }, {
    approval: planInput.approval,
    productId: '8800000001', itemId: '25000000001', vendorItemId: '92000000001', quantity: 1,
    unitPriceCeilingKrw: 10_000, linePriceCeilingKrw: 10_000,
  });
  const resolved = resolvePurchasePreparationStep({ ...planInput, cursor: 0 });
  assert.equal(resolved.status, 'ready');
  assert.equal(resolved.action.effect, PURCHASE_PREPARATION_EFFECT.NAVIGATE);
});

test('product navigation requires a fresh matching Coupang search observation', () => {
  const valid = resolvePurchasePreparationStep({
    ...input(),
    cursor: 1,
    observation: observation({ pathname: '/np/search', pageState: 'search', optionState: 'unknown' }),
  });
  assert.equal(valid.status, 'ready');
  assert.equal(valid.action.kind, PURCHASE_PREPARATION_STAGE.PRODUCT);

  const foreign = resolvePurchasePreparationStep({
    ...input(),
    cursor: 1,
    observation: observation({ origin: 'https://evil.example', pathname: '/np/search', pageState: 'search', optionState: 'unknown' }),
  });
  assert.equal(foreign.status, 'blocked');
  assert.equal(foreign.reasonCode, 'HOST_NOT_ALLOWED');
  assert.equal(foreign.action, null);
});

test('explicit no-option state is verified and unknown option state fails closed', () => {
  const ready = resolvePurchasePreparationStep({ ...input(), cursor: 2, observation: observation() });
  assert.equal(ready.status, 'ready');
  assert.equal(ready.action.effect, 'verify');

  const unknown = resolvePurchasePreparationStep({
    ...input(), cursor: 2, observation: observation({ optionState: 'unknown' }),
  });
  assert.equal(unknown.status, 'blocked');
  assert.equal(unknown.reasonCode, 'OPTIONS_NOT_READY');
  assert.equal(unknown.action, null);
});

test('option choices use role, accessible name, and group name without CSS selectors', () => {
  const choiceInput = input('single', [item(1, {
    kind: 'choices',
    choices: [{ groupName: '색상', valueName: '블랙' }, { groupName: '사이즈', valueName: '270' }],
  })]);
  const plan = buildPurchasePreparationPlan(choiceInput);
  assert.deepEqual(plan.steps.map((step) => step.stage), [
    'search', 'product', 'options', 'options', 'options', 'set_quantity', 'buy_now', 'checkout_ready',
  ]);
  const resolved = resolvePurchasePreparationStep({
    ...choiceInput,
    cursor: 2,
    observation: observation({
      optionState: 'required',
      semanticElements: [element('option_color_black', 'option', '블랙', { groupName: '색상' })],
    }),
  });
  assert.equal(resolved.status, 'ready');
  assert.equal(resolved.action.bindings.candidateRef, 'option_color_black');
  assert.equal(resolved.action.adapterId, COUPANG_PURCHASE_ADAPTER_ID);
  assert.equal(resolved.action.stepId, COUPANG_PURCHASE_STEP_ID.SELECT_OPTION);
  assert.equal('selector' in resolved.action.locator, false);

  assert.throws(() => buildPurchasePreparationPlan(input('single', [item(1, {
    kind: 'choices',
    choices: [{ groupName: '색상', valueName: '블랙' }, { groupName: '색상', valueName: '화이트' }],
  })])), (error) => error.code === 'INVALID_RECIPE_INPUT');
});

test('quantity step adjusts one semantic control at a time and verifies the approved quantity', () => {
  const target = item(2);
  const planInput = input('single', [target]);
  const baseObservation = {
    pathname: '/vp/products/8800000002',
    offer: { offerIdentity: target.offerIdentity, quantity: 1, unitPriceKrw: 9_000 },
  };
  const adjust = resolvePurchasePreparationStep({
    ...planInput,
    cursor: 3,
    observation: observation({
      ...baseObservation,
      semanticElements: [element('quantity_plus', 'button', '수량 늘리기')],
    }),
  });
  assert.equal(adjust.status, 'ready');
  assert.equal(adjust.stepComplete, false);
  assert.equal(adjust.action.stepId, 'set_quantity');
  assert.equal(adjust.action.bindings.candidateRef, 'quantity_plus');
  assert.equal(adjust.action.bindings.direction, 'increase');

  const verified = resolvePurchasePreparationStep({
    ...planInput,
    cursor: 3,
    observation: observation({
      ...baseObservation,
      offer: { ...baseObservation.offer, quantity: 2 },
    }),
  });
  assert.equal(verified.stepComplete, true);
  assert.equal(verified.action.effect, 'verify');
});

test('Buy now resolves one available semantic control and blocks missing, disabled, or ambiguous controls', () => {
  const cursor = 4;
  const planInput = input();
  const ready = resolvePurchasePreparationStep({
    ...planInput, cursor, observation: observation({ semanticElements: [element('buy_1', 'button', '바로구매')] }),
  });
  assert.equal(ready.status, 'ready');
  assert.equal(ready.action.bindings.candidateRef, 'buy_1');
  assert.deepEqual(ready.action.bindings.approval, planInput.approval);
  assert.deepEqual({
    productId: ready.action.bindings.productId,
    itemId: ready.action.bindings.itemId,
    vendorItemId: ready.action.bindings.vendorItemId,
    quantity: ready.action.bindings.quantity,
    unitPriceCeilingKrw: ready.action.bindings.unitPriceCeilingKrw,
    linePriceCeilingKrw: ready.action.bindings.linePriceCeilingKrw,
  }, {
    productId: '8800000001', itemId: '25000000001', vendorItemId: '92000000001', quantity: 1,
    unitPriceCeilingKrw: 10_000, linePriceCeilingKrw: 10_000,
  });

  const missing = resolvePurchasePreparationStep({ ...input(), cursor, observation: observation() });
  assert.equal(missing.reasonCode, 'CONTROL_NOT_FOUND');

  const disabled = resolvePurchasePreparationStep({
    ...input(), cursor, observation: observation({ semanticElements: [element('buy_1', 'button', '바로구매', { enabled: false })] }),
  });
  assert.equal(disabled.reasonCode, 'CONTROL_UNAVAILABLE');

  const ambiguous = resolvePurchasePreparationStep({
    ...input(), cursor, observation: observation({
      semanticElements: [element('buy_1', 'button', '바로구매'), element('buy_2', 'link', '바로 구매')],
    }),
  });
  assert.equal(ambiguous.reasonCode, 'CONTROL_AMBIGUOUS');
});

test('multi-item cart checkout binds only the approved selected offer lines', () => {
  const sources = [item(1), item(2)];
  const multi = input('multi', sources);
  const plan = buildPurchasePreparationPlan(multi);
  const cartCheckoutCursor = plan.steps.findIndex((step) => step.stepId === 'cart.checkout');
  const ready = resolvePurchasePreparationStep({
    ...multi,
    cursor: cartCheckoutCursor,
    observation: observation({
      origin: 'https://cart.coupang.com',
      pathname: '/cartView.pang',
      pageState: 'cart',
      optionState: 'unknown',
      semanticElements: [element('cart_buy', 'button', '2개 상품 구매하기')],
      cartLines: sources.map((source) => cartLine(source)),
    }),
  });
  assert.equal(ready.status, 'ready');
  assert.equal(ready.action.bindings.candidateRef, 'cart_buy');
  assert.deepEqual(ready.action.bindings.approvedLines, sources.map((source) => ({
    ...source.offerIdentity,
    quantity: source.quantity,
    unitPriceCeilingKrw: source.unitPriceCeilingKrw,
    linePriceCeilingKrw: source.linePriceCeilingKrw,
  })));

  const wrongPath = resolvePurchasePreparationStep({
    ...multi,
    cursor: cartCheckoutCursor,
    observation: observation({
      origin: 'https://cart.coupang.com',
      pathname: '/unknown',
      pageState: 'cart',
      optionState: 'unknown',
      semanticElements: [element('cart_buy', 'button', '구매하기')],
      cartLines: sources.map((source) => cartLine(source)),
    }),
  });
  assert.equal(wrongPath.reasonCode, 'URL_STATE_MISMATCH');

  const unrelated = item(3);
  const unsafeSelection = resolvePurchasePreparationStep({
    ...multi,
    cursor: cartCheckoutCursor,
    observation: observation({
      origin: 'https://cart.coupang.com',
      pathname: '/cartView.pang',
      pageState: 'cart',
      optionState: 'unknown',
      semanticElements: [element('cart_buy', 'button', '구매하기(3)')],
      cartLines: [...sources.map((source) => cartLine(source)), cartLine(unrelated)],
    }),
  });
  assert.equal(unsafeSelection.reasonCode, 'CART_SELECTION_MISMATCH');
  assert.equal(unsafeSelection.action, null);

  const duplicateSelection = resolvePurchasePreparationStep({
    ...multi,
    cursor: cartCheckoutCursor,
    observation: observation({
      origin: 'https://cart.coupang.com',
      pathname: '/cartView.pang',
      pageState: 'cart',
      optionState: 'unknown',
      semanticElements: [element('cart_buy', 'button', '구매하기(2)')],
      cartLines: [
        cartLine(sources[0], true, { lineRef: 'line_a' }),
        cartLine(sources[0], true, { lineRef: 'line_b' }),
      ],
    }),
  });
  assert.equal(duplicateSelection.reasonCode, 'CART_SELECTION_MISMATCH');
  assert.equal(duplicateSelection.action, null);
});

test('checkout-ready is a local non-mutating handoff that never observes checkout DOM', () => {
  const planInput = input();
  const handoff = resolvePurchasePreparationStep({
    ...planInput,
    cursor: buildPurchasePreparationPlan(planInput).steps.length - 1,
  });
  assert.equal(handoff.status, 'handoff');
  assert.equal(handoff.stage, 'checkout_ready');
  assert.equal(handoff.reasonCode, 'CHECKOUT_READY_USER_HANDOFF');
  assert.equal(handoff.action.effect, 'handoff');

  const actionKinds = new Set(Object.values(PURCHASE_PREPARATION_STAGE));
  for (const forbidden of ['payment', 'pay', 'place_order', 'confirm_order', 'confirm_purchase']) {
    assert.equal(actionKinds.has(forbidden), false);
  }
});

test('unknown DOM state, login redirects, and malformed observations fail closed', () => {
  const cursor = 4;
  const unknown = resolvePurchasePreparationStep({
    ...input(), cursor, observation: observation({ pageState: 'unknown' }),
  });
  assert.equal(unknown.reasonCode, 'UNKNOWN_PAGE_STATE');

  const login = resolvePurchasePreparationStep({
    ...input(), cursor, observation: observation({ origin: 'https://login.coupang.com', pathname: '/login/login.pang' }),
  });
  assert.equal(login.reasonCode, 'HOST_NOT_ALLOWED');

  const malformed = resolvePurchasePreparationStep({
    ...input(), cursor, observation: { ...observation(), pathname: '/vp/products/1?secret=value' },
  });
  assert.equal(malformed.reasonCode, 'INVALID_OBSERVATION');
});

test('unknown recipes, invalid branching, unsafe URLs, and sensitive queries are rejected', () => {
  const valid = input();
  for (const buildValue of [
    () => ({ ...valid, merchantId: 'UNKNOWN' }),
    () => ({ ...valid, recipeVersion: '2' }),
    () => ({ ...valid, items: [item(1), item(2)] }),
    () => ({ ...valid, mode: 'multi' }),
    () => ({ ...valid, items: [{ ...item(1), productUrl: 'https://evil.example/vp/products/1' }] }),
    () => ({ ...valid, items: [{ ...item(1), quantity: 0 }] }),
    () => ({ ...valid, items: [{ ...item(1), unitPriceCeilingKrw: 20_000, linePriceCeilingKrw: 10_000 }] }),
    () => ({ ...valid, items: [{ ...item(1), offerIdentity: { ...item(1).offerIdentity, itemId: '999' } }] }),
    () => ({ ...valid, items: [{ ...item(1), query: 'person@example.com' }] }),
    () => ({ ...valid, approval: { ...valid.approval, approvalDigest: 'sha256:bad' } }),
    () => ({ ...valid, approval: { ...valid.approval, expiresAt: new Date(Date.now() + 11 * 60_000).toISOString() } }),
  ]) {
    assert.throws(() => buildPurchasePreparationPlan(buildValue()), PurchaseRecipeError);
  }
});
