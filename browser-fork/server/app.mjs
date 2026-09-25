import http from 'node:http';
import { timingSafeEqual } from 'node:crypto';
import {
  actionFromApprovedPreparation,
  RequestError,
  validateProviderAction,
  validateStep,
} from './protocol.mjs';
import { createCommandIssuer } from './permit.mjs';
import {
  discoverMerchantProducts,
  discoveryFailureResponse,
  discoveryResponse,
  normalizeDiscoveryResults,
  validateDiscoveryRequest,
} from './discovery.mjs';

export function createBridge({
  token,
  permitKey = token,
  provider,
  issuer,
  discovery = discoverMerchantProducts,
  maxConcurrent = 2,
}) {
  if (typeof token !== 'string' || Buffer.byteLength(token) < 32) throw new Error('BRIDGE_TOKEN must have at least 32 bytes');
  if (typeof provider !== 'function') throw new Error('provider is required');
  if (typeof discovery !== 'function') throw new Error('discovery is required');
  if (!Number.isSafeInteger(maxConcurrent) || maxConcurrent < 1 || maxConcurrent > 32) throw new Error('Invalid maxConcurrent');
  const commandIssuer = issuer ?? createCommandIssuer({ permitKey });
  const expected = Buffer.from(`Bearer ${token}`);
  let active = 0;
  const server = http.createServer(async (req, res) => {
    const reply = (status, body) => {
      if (res.destroyed || res.writableEnded) return;
      res.writeHead(status, {
        'Content-Type': 'application/json; charset=utf-8',
        'Cache-Control': 'no-store',
        'X-Content-Type-Options': 'nosniff',
      });
      res.end(JSON.stringify(body));
    };
    // A website must never call the native bridge with the user's credential.
    if (req.headers.origin || req.headers['sec-fetch-site']) {
      return reply(403, { error: 'Native clients only', code: 'NATIVE_CLIENT_REQUIRED' });
    }
    const given = Buffer.from(req.headers.authorization ?? '');
    if (given.length !== expected.length || !timingSafeEqual(given, expected)) {
      return reply(401, { error: 'Unauthorized', code: 'UNAUTHORIZED' });
    }
    if (req.method === 'GET' && req.url === '/health') {
      return reply(200, { status: 'ok', protocolVersion: 1 });
    }
    const isStep = req.method === 'POST' && req.url === '/v1/step';
    const isDiscovery = req.method === 'POST' && req.url === '/v1/discover';
    if (!isStep && !isDiscovery) {
      return reply(404, { error: 'Not found', code: 'NOT_FOUND' });
    }
    if (!req.headers['content-type']?.startsWith('application/json')) {
      return reply(415, { error: 'Expected application/json', code: 'CONTENT_TYPE_REQUIRED' });
    }
    if (active >= maxConcurrent) {
      return reply(429, { error: 'Bridge is busy; try again', code: 'BRIDGE_BUSY' });
    }
    active++;
    const controller = new AbortController();
    const timer = setTimeout(() => controller.abort(), 50_000);
    res.on('close', () => controller.abort());
    try {
      let length = 0;
      const chunks = [];
      for await (const chunk of req) {
        length += chunk.length;
        if (length > 262_144) throw new RequestError('Request too large', 413, 'REQUEST_TOO_LARGE');
        chunks.push(chunk);
      }
      let data;
      try { data = JSON.parse(Buffer.concat(chunks).toString('utf8')); }
      catch { throw new RequestError('Invalid JSON', 400, 'INVALID_JSON'); }
      if (isDiscovery) {
        const request = validateDiscoveryRequest(data);
        let response;
        try {
          const rawResults = await discovery(request, controller.signal);
          response = discoveryResponse(request.source, normalizeDiscoveryResults(request, rawResults));
        } catch (error) {
          response = discoveryFailureResponse(request.source, error);
        }
        reply(200, response);
      } else {
        const step = validateStep(data);
        const authorizedCommand = await commandIssuer.authorize(step, async () =>
          step.approvedPreparation
            ? actionFromApprovedPreparation(step)
            : validateProviderAction(await provider(step, controller.signal), step.observation));
        reply(200, { authorizedCommand });
      }
    } catch (error) {
      reply(error instanceof RequestError ? error.status : 502, {
        error: error instanceof RequestError ? error.message : 'AI request failed or timed out',
        code: error instanceof RequestError ? error.code : 'AI_REQUEST_FAILED',
      });
    } finally {
      active--;
      clearTimeout(timer);
    }
  });
  server.requestTimeout = 15_000;
  server.headersTimeout = 10_000;
  return server;
}
