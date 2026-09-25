import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import {
  actionFromApprovedPreparation,
  canonicalize,
  isPublicHostname,
  publicWebUrl,
  sha256,
  toModelInput,
  validateObservation,
  validateProposedAction,
  validateStep,
} from '../server/protocol.mjs';
import { buildPurchasePreparationPlan, computePurchaseApprovalDigest } from '../server/purchase-recipes.mjs';
import { discoveryResponse, normalizeDiscoveryResults, validateDiscoveryRequest } from '../server/discovery.mjs';
import { observation, step } from './fixtures.mjs';

const schema = JSON.parse(await readFile(new URL('../protocol/v1.schema.json', import.meta.url), 'utf8'));
const goldenStep = JSON.parse(await readFile(new URL('../protocol/fixtures/golden-step-request.v1.json', import.meta.url), 'utf8'));
const goldenDiscoverRequest = JSON.parse(await readFile(new URL('../protocol/fixtures/golden-discover-request.v1.json', import.meta.url), 'utf8'));
const goldenDiscoverResponse = JSON.parse(await readFile(new URL('../protocol/fixtures/golden-discover-response.v1.json', import.meta.url), 'utf8'));

function approvedCoupangPreparation(cursor = 4) {
  const unsigned = {
    merchantId: 'COUPANG',
    recipeVersion: '1',
    mode: 'single',
    approval: {
      approvalId: 'approval_protocol_1',
      currency: 'KRW',
      revision: 1,
      expiresAt: new Date(Date.now() + 5 * 60_000).toISOString(),
      totalPriceCeilingKrw: 15_000,
    },
    items: [{
      query: '승인된 테스트 상품',
      productUrl: 'https://www.coupang.com/vp/products/12345?itemId=23456&vendorItemId=34567',
      offerIdentity: { productId: '12345', itemId: '23456', vendorItemId: '34567' },
      quantity: 1,
      unitPriceCeilingKrw: 15_000,
      linePriceCeilingKrw: 15_000,
      options: { kind: 'none' },
    }],
  };
  return {
    ...unsigned,
    approval: { ...unsigned.approval, approvalDigest: computePurchaseApprovalDigest(unsigned) },
    cursor,
    expectedPage: { origin: 'https://www.coupang.com', pathname: '/vp/products/12345' },
  };
}

function approvedCoupangMultiPreparation() {
  const items = [
    {
      query: '승인된 테스트 상품 1',
      productUrl: 'https://www.coupang.com/vp/products/12345?itemId=23456&vendorItemId=34567',
      offerIdentity: { productId: '12345', itemId: '23456', vendorItemId: '34567' },
      quantity: 1,
      unitPriceCeilingKrw: 15_000,
      linePriceCeilingKrw: 15_000,
      options: { kind: 'none' },
    },
    {
      query: '승인된 테스트 상품 2',
      productUrl: 'https://www.coupang.com/vp/products/45678?itemId=56789&vendorItemId=67890',
      offerIdentity: { productId: '45678', itemId: '56789', vendorItemId: '67890' },
      quantity: 2,
      unitPriceCeilingKrw: 10_000,
      linePriceCeilingKrw: 20_000,
      options: { kind: 'none' },
    },
  ];
  const unsigned = {
    merchantId: 'COUPANG',
    recipeVersion: '1',
    mode: 'multi',
    approval: {
      approvalId: 'approval_protocol_multi_1',
      currency: 'KRW',
      revision: 1,
      expiresAt: new Date(Date.now() + 5 * 60_000).toISOString(),
      totalPriceCeilingKrw: 35_000,
    },
    items,
  };
  const approved = {
    ...unsigned,
    approval: { ...unsigned.approval, approvalDigest: computePurchaseApprovalDigest(unsigned) },
  };
  const plan = buildPurchasePreparationPlan(approved);
  return {
    ...approved,
    cursor: plan.steps.findIndex((entry) => entry.action.stepId === 'start_checkout'),
    expectedPage: { origin: 'https://cart.coupang.com', pathname: '/cartView.pang' },
  };
}

function coupangStep(cursor = 4) {
  return step({
    observation: observation({
      nativeMetadata: {
        tabId: 'tab_001', frameId: 'frame_main', documentEpoch: 7,
        topOrigin: 'https://www.coupang.com', frameOrigin: 'https://www.coupang.com', foreground: true,
        pathname: '/vp/products/12345', queryOrFragmentPresent: true,
      },
      untrustedPageData: {
        pageTypeHint: 'product', title: '승인된 상품', visibleText: '공개 상품 설명', candidates: [],
      },
      privacy: {
        inputValuesOmitted: true, secretsOmitted: true, screenshotIncluded: false,
        urlQueryAndFragmentOmitted: true, collectionStatus: 'sanitized', handoffReasonCodes: [],
        excludedBoundaryCodes: ['PAYMENT_OR_COMMITMENT'],
      },
    }),
    approvedPreparation: approvedCoupangPreparation(cursor),
  });
}

test('protocol-v1 schema exposes exactly the bounded action surface', () => {
  const kinds = [...new Set(schema.$defs.ProposedAction.oneOf.map((entry) => entry.properties.kind.const))].sort();
  assert.deepEqual(kinds, ['finish', 'inspect_page', 'open_candidate', 'request_human', 'run_preparation_step', 'scroll']);
  const preparationAdapters = schema.$defs.ProposedAction.oneOf
    .filter((entry) => entry.properties.kind.const === 'run_preparation_step')
    .map((entry) => entry.properties.adapterId.const)
    .sort();
  assert.deepEqual(preparationAdapters, ['builtin.coupang.purchase-preparation', 'builtin.public-search']);
  assert.equal(schema.$defs.AuthorizedCommand.additionalProperties, false);
  assert.equal(schema.$defs.SanitizedObservation.properties.privacy.additionalProperties, false);
  assert.deepEqual(schema.$defs.MerchantSource.enum,
    ['COUPANG', 'ELEVENST', 'MUSINSA', 'KURLY', 'LOTTEON', 'DAISOMALL', 'AUCTION', 'OHOUSE']);
  assert.equal(schema.$defs.DiscoverRequest.additionalProperties, false);
  assert.equal(schema.$defs.DiscoverRequest.properties.query.maxLength, 300);
  assert.equal(schema.$defs.DiscoverRequest.properties.offset.maximum, 400);
  assert.equal(schema.$defs.DiscoveryResult.properties.context.maxLength, 1000);
  assert.equal(schema.$defs.DiscoveryResult.properties.priceText.maxLength, 64);
});

test('golden discovery fixtures match strict runtime normalization', () => {
  const request = validateDiscoveryRequest(goldenDiscoverRequest);
  const raw = goldenDiscoverResponse.results.map(({ url, title, context, priceText }) => ({
    url, title, context, priceText,
  }));
  assert.deepEqual(discoveryResponse(request.source, normalizeDiscoveryResults(request, raw)),
    goldenDiscoverResponse);
});

test('golden StepRequest passes the runtime validator unchanged apart from requestHash', () => {
  const normalized = validateStep(goldenStep);
  const { requestHash, ...wire } = normalized;
  assert.match(requestHash, /^[a-f0-9]{64}$/);
  assert.deepEqual(wire, goldenStep);
});

test('native-approved Coupang steps bypass the model but remain digest, cursor, and page bound', () => {
  for (const [cursor, stepId] of [[2, 'verify_options'], [3, 'set_quantity'], [4, 'buy_now']]) {
    const normalized = validateStep(coupangStep(cursor));
    assert.equal(normalized.approvedPreparation.action.stepId, stepId);
    assert.equal('approvedPreparation' in toModelInput(normalized), false);
    const action = actionFromApprovedPreparation(normalized);
    assert.equal(action.adapterId, 'builtin.coupang.purchase-preparation');
    assert.equal(action.stepId, stepId);
    assert.equal(action.bindings.expectedOrigin, 'https://www.coupang.com');
    assert.equal(action.bindings.productId, '12345');
  }

  const tampered = coupangStep();
  tampered.approvedPreparation.items[0].query = '승인 뒤 바뀐 상품명';
  assert.throws(() => validateStep(tampered), (error) => error.code === 'APPROVAL_DIGEST_MISMATCH');

  const wrongPage = coupangStep();
  wrongPage.approvedPreparation.expectedPage.pathname = '/vp/products/99999';
  assert.throws(() => validateStep(wrongPage), (error) => error.code === 'APPROVAL_PAGE_MISMATCH');
});

test('public URL policy rejects schemes, credentials, query data, ports, and private or reserved hosts', () => {
  const denied = [
    'javascript:alert(1)',
    'file:///etc/passwd',
    'chrome://settings',
    'intent://open',
    'https://user:pass@shop.example.com/path',
    'https://shop.example.com/path?token=secret',
    'https://shop.example.com/path#private',
    'https://shop.example.com:8443/path',
    'http://localhost/path',
    'http://127.0.0.1/path',
    'http://2130706433/path',
    'http://0x7f000001/path',
    'http://0177.0.0.1/path',
    'http://10.0.0.1/path',
    'http://172.16.0.1/path',
    'http://192.168.1.1/path',
    'http://169.254.169.254/latest/meta-data',
    'http://[::1]/path',
    'http://[::ffff:7f00:1]/path',
    'http://[::ffff:192.168.1.1]/path',
    'http://[fd00::1]/path',
    'http://[2001:db8::1]/path',
    'https://printer.local/path',
    'https://intranet/path',
  ];
  for (const url of denied) assert.throws(() => publicWebUrl(url), undefined, url);
  assert.equal(publicWebUrl('https://shop.example.com/products/shoe'), 'https://shop.example.com/products/shoe');
  assert.equal(isPublicHostname('8.8.8.8'), true);
  assert.equal(isPublicHostname('192.168.1.4'), false);
});

test('observation separates native metadata, untrusted page data, and enforceable privacy declarations', () => {
  const raw = observation();
  raw.cookie = 'COOKIE_SECRET';
  raw.untrustedPageData.rawHtml = '<input value="PRIVATE">';
  raw.untrustedPageData.candidates[1].value = 'PRIVATE_VALUE';
  const normalized = validateObservation(raw);
  assert.deepEqual(Object.keys(normalized), ['observationId', 'nativeMetadata', 'untrustedPageData', 'privacy']);
  assert.equal(normalized.nativeMetadata.topOrigin, 'https://shop.example.com');
  assert.equal(JSON.stringify(normalized).includes('COOKIE_SECRET'), false);
  assert.equal(JSON.stringify(normalized).includes('PRIVATE_VALUE'), false);
  assert.equal(JSON.stringify(normalized).includes('rawHtml'), false);
  assert.throws(() => validateObservation(observation({
    nativeMetadata: { ...observation().nativeMetadata, topOrigin: 'https://shop.example.com/private/path' },
  })));
  const crossOrigin = observation();
  crossOrigin.untrustedPageData.candidates[0].href = 'https://other.example.com/products/shoe';
  assert.throws(() => validateObservation(crossOrigin), (error) => error.code === 'HUMAN_REQUIRED');
});

test('blocked observations carry only handoff metadata and no page content', () => {
  const blocked = observation({
    untrustedPageData: { pageTypeHint: 'unknown', title: '', visibleText: '', candidates: [] },
    privacy: {
      inputValuesOmitted: true,
      secretsOmitted: true,
      screenshotIncluded: false,
      urlQueryAndFragmentOmitted: true,
      collectionStatus: 'handoff_required',
      handoffReasonCodes: ['AUTHENTICATION_REQUIRED'],
      excludedBoundaryCodes: [],
    },
  });
  assert.equal(validateObservation(blocked).privacy.collectionStatus, 'handoff_required');
  blocked.untrustedPageData.visibleText = 'password value';
  assert.throws(() => validateObservation(blocked), /must not contain page data/);
  blocked.untrustedPageData.visibleText = '';
  blocked.untrustedPageData.title = 'Private account';
  assert.throws(() => validateObservation(blocked), /must not contain page data/);
});

test('Coupang checkout origin is always a payment handoff and never a sanitized model surface', () => {
  const checkoutMetadata = {
    ...observation().nativeMetadata,
    topOrigin: 'https://checkout.coupang.com',
    frameOrigin: 'https://checkout.coupang.com',
    pathname: '/checkout/order',
    queryOrFragmentPresent: false,
  };
  const emptyPage = { pageTypeHint: 'unknown', title: '', visibleText: '', candidates: [] };
  assert.throws(() => validateObservation(observation({
    nativeMetadata: checkoutMetadata,
    untrustedPageData: emptyPage,
    privacy: {
      inputValuesOmitted: true, secretsOmitted: true, screenshotIncluded: false,
      urlQueryAndFragmentOmitted: true, collectionStatus: 'sanitized',
      handoffReasonCodes: [], excludedBoundaryCodes: ['PAYMENT_OR_COMMITMENT'],
    },
  })), (error) => error.code === 'HUMAN_REQUIRED');

  const blocked = validateObservation(observation({
    nativeMetadata: checkoutMetadata,
    untrustedPageData: emptyPage,
    privacy: {
      inputValuesOmitted: true, secretsOmitted: true, screenshotIncluded: false,
      urlQueryAndFragmentOmitted: true, collectionStatus: 'handoff_required',
      handoffReasonCodes: ['PAYMENT_OR_COMMITMENT'], excludedBoundaryCodes: [],
    },
  }));
  const action = validateProposedAction({
    kind: 'request_human', reasonCode: 'PAYMENT_OR_COMMITMENT',
    message: '주문 내용과 결제를 직접 확인해 주세요.', reason: '결제 화면 인계',
  }, blocked);
  assert.equal(action.kind, 'request_human');
  assert.equal(action.page.pathname, '/checkout/order');
});

test('approved cart checkout requires the exact native cart path without query or fragment', () => {
  const cartObservation = observation({
    nativeMetadata: {
      ...observation().nativeMetadata,
      topOrigin: 'https://cart.coupang.com',
      frameOrigin: 'https://cart.coupang.com',
      pathname: '/cartView.pang',
      queryOrFragmentPresent: true,
    },
    untrustedPageData: {
      pageTypeHint: 'public', title: '장바구니', visibleText: '선택 상품', candidates: [],
    },
  });
  assert.throws(() => validateStep(step({
    observation: cartObservation,
    approvedPreparation: approvedCoupangMultiPreparation(),
  })), (error) => error.code === 'APPROVAL_PAGE_MISMATCH');
});

test('sanitized observations carry only explicit subtree-exclusion attestations', () => {
  const raw = observation();
  raw.privacy = {
    ...raw.privacy,
    handoffReasonCodes: [],
    excludedBoundaryCodes: ['PAYMENT_OR_COMMITMENT', 'CROSS_ORIGIN_FRAME'],
  };
  const normalized = validateObservation(raw);
  assert.equal(normalized.privacy.collectionStatus, 'sanitized');
  assert.deepEqual(normalized.privacy.handoffReasonCodes, []);
  assert.deepEqual(normalized.privacy.excludedBoundaryCodes,
    ['PAYMENT_OR_COMMITMENT', 'CROSS_ORIGIN_FRAME']);
  assert.throws(() => validateObservation(observation({
    privacy: { ...raw.privacy, handoffReasonCodes: ['PAYMENT_OR_COMMITMENT'] },
  })), /cannot carry handoff reasons/);
  assert.throws(() => validateObservation(observation({
    privacy: { ...raw.privacy, excludedBoundaryCodes: ['AUTHENTICATION_REQUIRED'] },
  })), /Invalid excluded boundary/);
});

test('step validation bounds structured history and keeps run binding out of model input', () => {
  const normalized = validateStep(step({
    history: [{ sequence: 1, actionKind: 'scroll', status: 'applied', code: 'VIEWPORT_SCROLLED' }],
  }));
  assert.match(normalized.requestHash, /^[a-f0-9]{64}$/);
  const modelInput = toModelInput(normalized);
  assert.equal('commandContext' in modelInput, false);
  assert.equal(JSON.stringify(modelInput).includes('device_001'), false);
  assert.throws(() => validateStep(step({ history: Array(21).fill({ sequence: 1, actionKind: 'scroll', status: 'applied', code: 'OK' }) })));
  assert.throws(() => validateStep(step({ commandContext: { ...step().commandContext, sequence: 0 } })));
});

test('model can select only safe links, scroll, inspect, handoff, finish, or the exact public-search recipe', () => {
  const obs = validateObservation(observation());
  const open = validateProposedAction({ kind: 'open_candidate', candidateRef: 'candidate_1', reason: '상품 확인' }, obs);
  assert.equal(open.page.observationId, 'obs_001');
  assert.equal(open.page.topOrigin, 'https://shop.example.com');
  assert.deepEqual(validateProposedAction({ kind: 'scroll', direction: 'down', reason: '더 보기' }, obs).direction, 'down');
  const prepare = validateProposedAction({
    kind: 'run_preparation_step', adapterId: 'builtin.public-search', recipeVersion: '1', stepId: 'prepare_query',
    candidateRef: 'candidate_2', query: '가벼운 러닝화', reason: '공개 검색어 준비',
  }, obs);
  assert.deepEqual(prepare.bindings, { candidateRef: 'candidate_2', query: '가벼운 러닝화' });
  for (const action of [
    { kind: 'evaluate', script: 'document.cookie', reason: 'read' },
    { kind: 'click', candidateRef: 'candidate_1', reason: 'click' },
    { kind: 'fill', candidateRef: 'candidate_2', value: 'x', reason: 'fill' },
    { kind: 'navigate', url: 'https://shop.example.com', reason: 'go' },
    { kind: 'run_preparation_step', adapterId: 'remote-code', recipeVersion: '1', stepId: 'eval', candidateRef: 'candidate_2', query: 'shoe', reason: 'run' },
  ]) assert.throws(() => validateProposedAction(action, obs));
});

test('public search rejects invented refs and likely secrets or personal identifiers', () => {
  const obs = validateObservation(observation());
  const base = { kind: 'run_preparation_step', adapterId: 'builtin.public-search', recipeVersion: '1', stepId: 'prepare_query', candidateRef: 'candidate_2', reason: '검색 준비' };
  for (const query of ['person@example.com', '010-1234-5678', 'card number 4111111111111111', 'https://private.example/path', 'OTP 123456']) {
    assert.throws(() => validateProposedAction({ ...base, query }, obs), undefined, query);
  }
  assert.throws(() => validateProposedAction({ ...base, candidateRef: 'candidate_999', query: '운동화' }, obs));
  assert.throws(() => validateProposedAction({ kind: 'open_candidate', candidateRef: 'candidate_2', reason: 'wrong kind' }, obs));
});

test('handoff-required observation deterministically rejects every model action except request_human', () => {
  const obs = validateObservation(observation({
    untrustedPageData: { pageTypeHint: 'unknown', title: '', visibleText: '', candidates: [] },
    privacy: {
      inputValuesOmitted: true, secretsOmitted: true, screenshotIncluded: false, urlQueryAndFragmentOmitted: true,
      collectionStatus: 'handoff_required', handoffReasonCodes: ['PAYMENT_OR_COMMITMENT'], excludedBoundaryCodes: [],
    },
  }));
  assert.throws(() => validateProposedAction({ kind: 'finish', message: 'done', reason: 'done' }, obs), /requires human/);
  const handoff = validateProposedAction({
    kind: 'request_human', reasonCode: 'PAYMENT_OR_COMMITMENT', message: '최종 결제는 직접 진행해 주세요.', reason: '사용자 확인 필요',
  }, obs);
  assert.equal(handoff.kind, 'request_human');
  assert.equal(handoff.page.observationId, 'obs_001');
});

test('canonical JSON and SHA-256 are deterministic across object key order', () => {
  assert.equal(canonicalize({ b: 2, a: { d: 4, c: 3 } }), '{"a":{"c":3,"d":4},"b":2}');
  assert.equal(sha256({ b: 2, a: 1 }), sha256({ a: 1, b: 2 }));
});
