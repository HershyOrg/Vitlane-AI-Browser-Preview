import test from 'node:test';
import assert from 'node:assert/strict';
import { once } from 'node:events';
import http from 'node:http';
import { createBridge } from '../server/app.mjs';
import { verifyAuthorizedCommand } from '../server/permit.mjs';
import { createProvider, SYSTEM_PROMPT } from '../server/provider.mjs';
import { validateStep } from '../server/protocol.mjs';
import { step } from './fixtures.mjs';

const token = 'bridge-token-for-tests-0123456789abcdef';
const headers = { authorization: `Bearer ${token}`, 'content-type': 'application/json' };

async function listen(server, t) {
  server.listen(0, '127.0.0.1');
  await once(server, 'listening');
  t.after(() => { server.closeAllConnections(); server.close(); });
  return `http://127.0.0.1:${server.address().port}`;
}

test('health and model access require native credential; website origins are rejected', async (t) => {
  let calls = 0;
  const base = await listen(createBridge({ token, provider: async () => { calls++; return {}; } }), t);
  assert.equal((await fetch(`${base}/health`)).status, 401);
  const health = await fetch(`${base}/health`, { headers });
  assert.equal(health.status, 200);
  assert.deepEqual(await health.json(), { status: 'ok', protocolVersion: 1 });
  assert.equal((await fetch(`${base}/v1/step`, {
    method: 'POST', headers: { ...headers, Origin: 'https://evil.example.com' }, body: JSON.stringify(step()),
  })).status, 403);
  assert.equal((await fetch(`${base}/v1/step`, { method: 'POST', headers, body: '{}' })).status, 400);
  assert.equal(calls, 0);
});

test('full bridge call returns a signed AuthorizedCommand while model sees no device/profile binding', async (t) => {
  let seen;
  let calls = 0;
  const model = http.createServer(async (req, res) => {
    calls++;
    assert.equal(req.url, '/v1/chat/completions');
    assert.equal(req.headers.authorization, 'Bearer model-key');
    const chunks = [];
    for await (const chunk of req) chunks.push(chunk);
    seen = JSON.parse(Buffer.concat(chunks).toString());
    res.setHeader('Content-Type', 'application/json');
    res.end(JSON.stringify({ choices: [{ message: { content: JSON.stringify({
      kind: 'open_candidate', candidateRef: 'candidate_1', reason: '공개 상품 링크 확인',
    }) } }] }));
  });
  const modelBase = await listen(model, t);
  const provider = createProvider({ baseUrl: `${modelBase}/v1`, apiKey: 'model-key', model: 'test-model' });
  const base = await listen(createBridge({ token, provider }), t);
  const body = JSON.stringify(step());
  const response = await fetch(`${base}/v1/step`, { method: 'POST', headers, body });
  assert.equal(response.status, 200);
  const { authorizedCommand } = await response.json();
  assert.equal(authorizedCommand.action.kind, 'open_candidate');
  assert.equal(authorizedCommand.action.page.observationId, 'obs_001');
  assert.match(authorizedCommand.actionHash, /^[a-f0-9]{64}$/);
  assert.match(authorizedCommand.serverPermit, /^hmac-sha256:/);
  verifyAuthorizedCommand(authorizedCommand, { permitKey: token, expected: step().commandContext });
  assert.equal(seen.model, 'test-model');
  assert.equal(seen.messages[0].content, SYSTEM_PROMPT);
  assert.equal(seen.messages[0].content.includes('document.cookie'), false);
  const modelInput = JSON.parse(seen.messages[1].content);
  assert.equal(modelInput.goal, step().goal);
  assert.equal('commandContext' in modelInput, false);
  assert.equal(JSON.stringify(modelInput).includes('device_001'), false);

  const replay = await fetch(`${base}/v1/step`, { method: 'POST', headers, body });
  assert.equal(replay.status, 200);
  assert.deepEqual((await replay.json()).authorizedCommand, authorizedCommand);
  assert.equal(calls, 1);
});

test('bridge accepts sanitized product text with excluded commitment and frame boundaries', async (t) => {
  let seen;
  const base = await listen(createBridge({
    token,
    provider: async (validated) => {
      seen = validated.observation;
      return { kind: 'finish', message: '상품 정보를 확인했습니다.', reason: '공개 정보 확인' };
    },
  }), t);
  const productObservation = {
    ...step().observation,
    untrustedPageData: {
      pageTypeHint: 'product',
      title: '공개 상품',
      visibleText: '공개 상품 설명',
      candidates: [{
        candidateRef: 'candidate_1', nodeRef: 'node_1', kind: 'safe_link', role: 'link',
        name: '상세 정보', href: 'https://shop.example.com/products/shoe/details',
      }],
    },
    privacy: {
      ...step().observation.privacy,
      handoffReasonCodes: [],
      excludedBoundaryCodes: ['PAYMENT_OR_COMMITMENT', 'CROSS_ORIGIN_FRAME'],
    },
  };
  const request = step({
    observation: productObservation,
    commandContext: { ...step().commandContext, runId: 'run_product_boundaries' },
  });
  const response = await fetch(`${base}/v1/step`, {
    method: 'POST', headers, body: JSON.stringify(request),
  });
  assert.equal(response.status, 200);
  assert.equal(seen.untrustedPageData.visibleText, '공개 상품 설명');
  assert.deepEqual(seen.privacy.handoffReasonCodes, []);
  assert.deepEqual(seen.privacy.excludedBoundaryCodes,
    ['PAYMENT_OR_COMMITMENT', 'CROSS_ORIGIN_FRAME']);
});

test('same run sequence with changed observation or goal is a replay conflict', async (t) => {
  const base = await listen(createBridge({ token, provider: async () => ({ kind: 'finish', message: '완료', reason: '확인' }) }), t);
  assert.equal((await fetch(`${base}/v1/step`, { method: 'POST', headers, body: JSON.stringify(step()) })).status, 200);
  const changed = step({ goal: '다른 목표' });
  const response = await fetch(`${base}/v1/step`, { method: 'POST', headers, body: JSON.stringify(changed) });
  assert.equal(response.status, 409);
  assert.equal((await response.json()).code, 'REPLAY_CONFLICT');
});

test('bridge policy revalidates custom provider output before signing it', async (t) => {
  const base = await listen(createBridge({ token, provider: async () => ({
    kind: 'evaluate', script: 'document.cookie', reason: 'unsafe',
  }) }), t);
  const response = await fetch(`${base}/v1/step`, { method: 'POST', headers, body: JSON.stringify(step()) });
  assert.equal(response.status, 502);
  assert.equal((await response.json()).code, 'INVALID_MODEL_ACTION');
});

test('oversized request is rejected without provider calls', async (t) => {
  let calls = 0;
  const base = await listen(createBridge({ token, provider: async () => { calls++; return {}; } }), t);
  const response = await fetch(`${base}/v1/step`, { method: 'POST', headers, body: JSON.stringify({ text: 'x'.repeat(270000) }) });
  assert.equal(response.status, 413);
  assert.equal((await response.json()).code, 'REQUEST_TOO_LARGE');
  assert.equal(calls, 0);
});

test('unexpected provider errors do not expose keys or payloads', async (t) => {
  const base = await listen(createBridge({ token, provider: async () => { throw new Error('SECRET API KEY user payload'); } }), t);
  const response = await fetch(`${base}/v1/step`, { method: 'POST', headers, body: JSON.stringify(step()) });
  assert.equal(response.status, 502);
  const text = await response.text();
  assert.equal(text.includes('SECRET'), false);
  assert.match(text, /AI_REQUEST_FAILED/);
});

test('concurrency is bounded and disconnect cancels model work', async (t) => {
  let signal;
  const base = await listen(createBridge({ token, maxConcurrent: 1, provider: (_step, providerSignal) => {
    signal = providerSignal;
    return new Promise((resolve, reject) => providerSignal.addEventListener('abort', () => reject(new Error('cancelled')), { once: true }));
  } }), t);
  const controller = new AbortController();
  const first = fetch(`${base}/v1/step`, { method: 'POST', headers, body: JSON.stringify(step()), signal: controller.signal }).catch(() => null);
  while (!signal) await new Promise((resolve) => setTimeout(resolve, 5));
  const secondStep = step({ commandContext: { ...step().commandContext, runId: 'run_002' } });
  assert.equal((await fetch(`${base}/v1/step`, { method: 'POST', headers, body: JSON.stringify(secondStep) })).status, 429);
  controller.abort();
  await first;
  for (let i = 0; !signal.aborted && i < 30; i++) await new Promise((resolve) => setTimeout(resolve, 10));
  assert.equal(signal.aborted, true);
});

test('provider rejects remote plaintext endpoints, redirects, malformed JSON, and forbidden model actions', async () => {
  assert.throws(() => createProvider({ baseUrl: 'http://remote.example.com/v1', apiKey: 'k', model: 'm' }));
  const makeProvider = (content) => createProvider({
    baseUrl: 'https://model.example.com/v1', apiKey: 'k', model: 'm',
    fetchImpl: async (_url, init) => {
      assert.equal(init.redirect, 'error');
      return new Response(JSON.stringify({ choices: [{ message: { content } }] }));
    },
  });
  await assert.rejects(() => makeProvider('not json')(step(), new AbortController().signal), /valid JSON/);
  await assert.rejects(() => makeProvider(JSON.stringify({ kind: 'evaluate', script: 'document.cookie', reason: 'read' }))(
    // createProvider normally receives the validated form from createBridge.
    validateStep(step()), new AbortController().signal,
  ), /Unsupported model action/);
});
