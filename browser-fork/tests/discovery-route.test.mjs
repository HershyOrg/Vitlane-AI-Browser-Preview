import test from 'node:test';
import assert from 'node:assert/strict';
import { once } from 'node:events';
import { createBridge } from '../server/app.mjs';
import { DiscoveryError } from '../server/discovery.mjs';
import { step } from './fixtures.mjs';

const token = 'bridge-token-for-tests-0123456789abcdef';
const headers = { authorization: `Bearer ${token}`, 'content-type': 'application/json' };

async function listen(server, t) {
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => { server.closeAllConnections(); server.close(); });
  return `http://127.0.0.1:${server.address().port}`;
}

function request(source = 'ELEVENST') {
  return { query: '무선 헤드폰', source, limit: 2, offset: 0 };
}

test('discover shares bridge authentication and rejects browser-origin callers before discovery', async (t) => {
  let calls = 0;
  const base = await listen(createBridge({
    token,
    provider: async () => ({ kind: 'finish', message: 'done', reason: 'done' }),
    discovery: async () => { calls++; return []; },
  }), t);
  assert.equal((await fetch(`${base}/v1/discover`, {
    method: 'POST', headers: { 'content-type': 'application/json' }, body: JSON.stringify(request()),
  })).status, 401);
  assert.equal((await fetch(`${base}/v1/discover`, {
    method: 'POST', headers: { ...headers, Origin: 'https://evil.invalid' }, body: JSON.stringify(request()),
  })).status, 403);
  assert.equal(calls, 0);
});

test('discover returns the exact deterministic batch shape after canonical sanitization', async (t) => {
  let seen;
  const base = await listen(createBridge({
    token,
    provider: async () => ({ kind: 'finish', message: 'done', reason: 'done' }),
    discovery: async (input) => {
      seen = input;
      return [
        {
          source: 'UNTRUSTED',
          url: 'https://www.11st.co.kr/products/12345?tracking=removed',
          title: '  무선\u0000 헤드폰  ',
          context: `무료 배송 ₩ 32,900 ${'공개 설명 '.repeat(180)}`,
          priceText: 'not a price',
        },
        { url: 'https://www.11st.co.kr/products/12345', title: 'duplicate', context: '' },
        { url: 'https://private.invalid/products/44444', title: 'invalid', context: '' },
        { url: 'https://www.11st.co.kr/products/67890', title: '두 번째', context: '가격 미표시' },
      ];
    },
  }), t);
  const response = await fetch(`${base}/v1/discover`, {
    method: 'POST', headers, body: JSON.stringify({ query: '  무선   헤드폰 ', source: 'ELEVENST', limit: 2, offset: 0 }),
  });
  assert.equal(response.status, 200);
  assert.deepEqual(seen, { query: '무선 헤드폰', source: 'ELEVENST', limit: 2, offset: 0 });
  const body = await response.json();
  assert.deepEqual(body, {
    protocolVersion: 1,
    results: [
      {
        source: 'ELEVENST',
        url: 'https://www.11st.co.kr/products/12345',
        title: '무선 헤드폰',
        context: body.results[0].context,
        priceText: '₩32,900',
      },
      {
        source: 'ELEVENST',
        url: 'https://www.11st.co.kr/products/67890',
        title: '두 번째', context: '가격 미표시',
      },
    ],
    coverage: { source: 'ELEVENST', status: 'SUCCEEDED', count: 2 },
  });
  assert.ok(Array.from(body.results[0].context).length <= 1000);
});

test('accepted empty and failed discovery both return a stable 200 coverage response', async (t) => {
  const emptyBase = await listen(createBridge({
    token,
    provider: async () => ({}),
    discovery: async () => [],
  }), t);
  const empty = await fetch(`${emptyBase}/v1/discover`, {
    method: 'POST', headers, body: JSON.stringify(request('KURLY')),
  });
  assert.equal(empty.status, 200);
  assert.deepEqual(await empty.json(), {
    protocolVersion: 1,
    results: [],
    coverage: { source: 'KURLY', status: 'EMPTY', count: 0 },
  });

  const failedBase = await listen(createBridge({
    token,
    provider: async () => ({}),
    discovery: async () => { throw new Error('secret upstream URL and query'); },
  }), t);
  const failed = await fetch(`${failedBase}/v1/discover`, {
    method: 'POST', headers, body: JSON.stringify(request('LOTTEON')),
  });
  assert.equal(failed.status, 200);
  const text = await failed.text();
  assert.equal(text.includes('secret'), false);
  assert.equal(text.includes('무선'), false);
  assert.deepEqual(JSON.parse(text), {
    protocolVersion: 1,
    results: [],
    coverage: { source: 'LOTTEON', status: 'FAILED', reasonCode: 'DISCOVERY_FAILED', count: 0 },
  });
});

test('known network policy failures expose only a safe reason code', async (t) => {
  const base = await listen(createBridge({
    token,
    provider: async () => ({}),
    discovery: async () => { throw new DiscoveryError('PRIVATE_NETWORK_BLOCKED'); },
  }), t);
  const response = await fetch(`${base}/v1/discover`, {
    method: 'POST', headers, body: JSON.stringify(request('DAISOMALL')),
  });
  assert.equal(response.status, 200);
  assert.deepEqual((await response.json()).coverage, {
    source: 'DAISOMALL', status: 'FAILED', reasonCode: 'PRIVATE_NETWORK_BLOCKED', count: 0,
  });
});

test('invalid discovery requests fail before the injected discoverer runs', async (t) => {
  let calls = 0;
  const base = await listen(createBridge({
    token,
    provider: async () => ({}),
    discovery: async () => { calls++; return []; },
  }), t);
  const cases = [
    [{ query: 'shoe', source: 'UNKNOWN', limit: 1, offset: 0 }, 'DISCOVERY_SOURCE_UNSUPPORTED'],
    [{ query: 'shoe', source: 'MUSINSA', limit: 0, offset: 0 }, 'DISCOVERY_LIMIT_INVALID'],
    [{ query: 'shoe', source: 'MUSINSA', limit: 1, offset: -1 }, 'DISCOVERY_OFFSET_INVALID'],
    [{ query: 'person@example.com', source: 'MUSINSA', limit: 1, offset: 0 }, 'DISCOVERY_QUERY_INVALID'],
    [{ query: 'shoe', source: 'MUSINSA', limit: 1, offset: 0, secret: 'x' }, 'DISCOVERY_REQUEST_INVALID'],
  ];
  for (const [body, code] of cases) {
    const response = await fetch(`${base}/v1/discover`, {
      method: 'POST', headers, body: JSON.stringify(body),
    });
    assert.equal(response.status, 400);
    assert.equal((await response.json()).code, code);
  }
  assert.equal(calls, 0);
});

test('discover and model steps consume the same concurrency budget and disconnect cancels discovery', async (t) => {
  let signal;
  const base = await listen(createBridge({
    token,
    maxConcurrent: 1,
    provider: async () => ({ kind: 'finish', message: 'done', reason: 'done' }),
    discovery: (_request, discoverySignal) => {
      signal = discoverySignal;
      return new Promise((resolve, reject) => discoverySignal.addEventListener('abort', () => reject(new Error('cancelled')), { once: true }));
    },
  }), t);
  const controller = new AbortController();
  const first = fetch(`${base}/v1/discover`, {
    method: 'POST', headers, body: JSON.stringify(request()), signal: controller.signal,
  }).catch(() => null);
  while (!signal) await new Promise((resolve) => setTimeout(resolve, 5));
  const second = await fetch(`${base}/v1/step`, {
    method: 'POST', headers, body: JSON.stringify(step()),
  });
  assert.equal(second.status, 429);
  controller.abort();
  await first;
  for (let index = 0; !signal.aborted && index < 30; index++) {
    await new Promise((resolve) => setTimeout(resolve, 10));
  }
  assert.equal(signal.aborted, true);
});
