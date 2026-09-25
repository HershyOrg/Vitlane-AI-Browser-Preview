import net from 'node:net';
import { lookup as dnsLookup } from 'node:dns/promises';
import { chromium } from 'playwright';
import { RequestError, isPublicHostname } from './protocol.mjs';

const MAX_QUERY_BYTES = 300;
const MAX_QUERY_CODE_POINTS = 300;
const MAX_TITLE_LENGTH = 300;
const MAX_CONTEXT_LENGTH = 1000;
const MAX_PRICE_TEXT_LENGTH = 64;
const MAX_ANCHORS = 400;
const MAX_OFFSET = 400;
// Stay below the Go adapter's 20-second request budget so an empty or failed
// browser route still leaves time for its one planned provider fallback.
const DEFAULT_TIMEOUT_MS = 15_000;
const DEFAULT_READINESS_TIMEOUT_MS = 5_000;
const DEFAULT_READINESS_POLL_MS = 200;

const numericProduct = '[1-9][0-9]{0,19}';

function exactHostMatches(hostname, hosts) {
  const host = hostname.toLowerCase().replace(/\.$/, '');
  return hosts.includes(host);
}

function canonicalPathProduct(raw, hosts, pattern, canonical) {
  let parsed;
  try { parsed = new URL(raw); } catch { return null; }
  if (parsed.protocol !== 'https:' || parsed.username || parsed.password || parsed.port || parsed.hash ||
      !exactHostMatches(parsed.hostname, hosts)) return null;
  const match = parsed.pathname.match(pattern);
  if (!match) return null;
  return canonical(match[1]);
}

const merchantRegistry = Object.freeze({
  COUPANG: Object.freeze({
    searchHosts: Object.freeze(['www.coupang.com']),
    searchPaths: Object.freeze(['/np/search']),
    productHosts: Object.freeze(['coupang.com', 'www.coupang.com']),
    searchUrl(query) {
      const url = new URL('https://www.coupang.com/np/search');
      url.searchParams.set('q', query);
      return url.href;
    },
    canonicalProductUrl(raw) {
      return canonicalPathProduct(raw, this.productHosts, new RegExp(`^/vp/products/(${numericProduct})/?$`),
        (id) => `https://www.coupang.com/vp/products/${id}`);
    },
  }),
  ELEVENST: Object.freeze({
    searchHosts: Object.freeze(['search.11st.co.kr']),
    searchPaths: Object.freeze(['/Search.tmall', '/pc/total-search']),
    optionalSearchParameters: Object.freeze({ tabId: Object.freeze(['TOTAL_SEARCH']) }),
    productHosts: Object.freeze(['11st.co.kr', 'www.11st.co.kr']),
    searchUrl(query) {
      const url = new URL('https://search.11st.co.kr/Search.tmall');
      url.searchParams.set('kwd', query);
      return url.href;
    },
    canonicalProductUrl(raw) {
      let parsed;
      try { parsed = new URL(raw); } catch { return null; }
      if ([...parsed.searchParams.keys()].some((key) => key.toLowerCase() === 'method')) return null;
      return canonicalPathProduct(raw, this.productHosts, new RegExp(`^/products/(${numericProduct})/?$`),
        (id) => `https://www.11st.co.kr/products/${id}`);
    },
  }),
  MUSINSA: Object.freeze({
    searchHosts: Object.freeze(['www.musinsa.com']),
    searchPaths: Object.freeze(['/search/goods']),
    optionalSearchParameters: Object.freeze({ gf: Object.freeze(['A']) }),
    productHosts: Object.freeze(['musinsa.com', 'www.musinsa.com']),
    searchUrl(query) {
      const url = new URL('https://www.musinsa.com/search/goods');
      url.searchParams.set('keyword', query);
      return url.href;
    },
    canonicalProductUrl(raw) {
      return canonicalPathProduct(raw, this.productHosts, new RegExp(`^/(?:products|app/goods)/(${numericProduct})/?$`),
        (id) => `https://www.musinsa.com/products/${id}`);
    },
  }),
  KURLY: Object.freeze({
    searchHosts: Object.freeze(['www.kurly.com']),
    searchPaths: Object.freeze(['/search']),
    productHosts: Object.freeze(['kurly.com', 'www.kurly.com']),
    searchUrl(query) {
      const url = new URL('https://www.kurly.com/search');
      url.searchParams.set('sword', query);
      return url.href;
    },
    canonicalProductUrl(raw) {
      return canonicalPathProduct(raw, this.productHosts, new RegExp(`^/goods/(${numericProduct})/?$`),
        (id) => `https://www.kurly.com/goods/${id}`);
    },
  }),
  LOTTEON: Object.freeze({
    searchHosts: Object.freeze(['www.lotteon.com']),
    searchPaths: Object.freeze(['/csearch/search/search']),
    optionalSearchParameters: Object.freeze({ sort: Object.freeze(['ranking']) }),
    productHosts: Object.freeze(['lotteon.com', 'www.lotteon.com']),
    searchUrl(query) {
      const url = new URL('https://www.lotteon.com/csearch/search/search');
      url.searchParams.set('render', 'search');
      url.searchParams.set('platform', 'pc');
      url.searchParams.set('q', query);
      return url.href;
    },
    canonicalProductUrl(raw) {
      return canonicalPathProduct(raw, this.productHosts, /^\/p\/product\/(L[OM][0-9]{6,14})\/?$/,
        (id) => `https://www.lotteon.com/p/product/${id}`);
    },
  }),
  DAISOMALL: Object.freeze({
    searchHosts: Object.freeze(['www.daisomall.co.kr']),
    searchPaths: Object.freeze(['/ds/dst/SCR_DST_0015']),
    productHosts: Object.freeze(['daisomall.co.kr', 'www.daisomall.co.kr']),
    searchUrl(query) {
      const url = new URL('https://www.daisomall.co.kr/ds/dst/SCR_DST_0015');
      url.searchParams.set('searchTerm', query);
      return url.href;
    },
    canonicalProductUrl(raw) {
      let parsed;
      try { parsed = new URL(raw); } catch { return null; }
      if (parsed.protocol !== 'https:' || parsed.username || parsed.password || parsed.port || parsed.hash ||
          !exactHostMatches(parsed.hostname, this.productHosts) ||
          parsed.pathname !== '/pd/pdr/SCR_PDR_0001') return null;
      const ids = [...parsed.searchParams]
        .filter(([key]) => key.toLowerCase() === 'pdno')
        .map(([, value]) => value);
      if (ids.length !== 1) return null;
      const id = ids[0];
      if (!/^[0-9]{5,12}$/.test(id)) return null;
      return `https://www.daisomall.co.kr/pd/pdr/SCR_PDR_0001?pdNo=${id}`;
    },
  }),
  AUCTION: Object.freeze({
    searchHosts: Object.freeze(['www.auction.co.kr']),
    searchPaths: Object.freeze(['/n/search']),
    productHosts: Object.freeze(['auction.co.kr', 'www.auction.co.kr', 'itempage3.auction.co.kr']),
    searchUrl(query) {
      const url = new URL('https://www.auction.co.kr/n/search');
      url.searchParams.set('keyword', query);
      return url.href;
    },
    canonicalProductUrl(raw) {
      let parsed;
      try { parsed = new URL(raw); } catch { return null; }
      // Auction currently publishes HTTP product hrefs from its HTTPS search
      // page. Treat the href only as an identity carrier and always rebuild an
      // HTTPS canonical destination; no HTTP navigation is permitted.
      if (!['http:', 'https:'].includes(parsed.protocol) || parsed.username || parsed.password ||
          parsed.port || parsed.hash || !exactHostMatches(parsed.hostname, this.productHosts) ||
          !/^\/DetailView\.aspx$/i.test(parsed.pathname)) return null;
      const ids = [...parsed.searchParams]
        .filter(([key]) => key.toLowerCase() === 'itemno')
        .map(([, value]) => value);
      if (ids.length !== 1 || !/^[A-Z][0-9]{6,12}$/.test(ids[0])) return null;
      return `https://itempage3.auction.co.kr/DetailView.aspx?itemno=${ids[0]}`;
    },
  }),
  OHOUSE: Object.freeze({
    searchHosts: Object.freeze(['ohou.se', 'www.ohou.se']),
    searchPaths: Object.freeze(['/search/index']),
    productHosts: Object.freeze(['ohou.se', 'www.ohou.se', 'store.ohou.se']),
    searchUrl(query) {
      const url = new URL('https://ohou.se/search/index');
      url.searchParams.set('query', query);
      return url.href;
    },
    canonicalProductUrl(raw) {
      let parsed;
      try { parsed = new URL(raw); } catch { return null; }
      if (parsed.protocol !== 'https:' || parsed.username || parsed.password || parsed.port || parsed.hash ||
          !exactHostMatches(parsed.hostname, this.productHosts)) return null;
      const host = parsed.hostname.toLowerCase().replace(/\.$/, '');
      const pattern = host === 'store.ohou.se'
        ? new RegExp(`^/goods/(${numericProduct})/?$`)
        : new RegExp(`^/productions/(${numericProduct})(?:/selling)?/?$`);
      const match = parsed.pathname.match(pattern);
      if (!match) return null;
      return `https://ohou.se/productions/${match[1]}/selling`;
    },
  }),
});

export const SUPPORTED_MERCHANT_SOURCES = Object.freeze(Object.keys(merchantRegistry));

export class DiscoveryError extends Error {
  constructor(code) {
    super(code);
    this.code = /^[A-Z][A-Z0-9_]{2,79}$/.test(code) ? code : 'DISCOVERY_FAILED';
  }
}

function boundedText(value, maximum, { required = false } = {}) {
  if (typeof value !== 'string') return required ? null : '';
  const normalized = value.normalize('NFKC')
    .replace(/[\p{Cc}\p{Cf}]+/gu, ' ')
    .replace(/\s+/gu, ' ')
    .trim();
  if (required && !normalized) return null;
  return Array.from(normalized).slice(0, maximum).join('');
}

function boundedTitle(value) {
  const title = boundedText(value, MAX_TITLE_LENGTH, { required: true });
  if (!title) return null;
  const withoutNavigationLabel = title.replace(/\s*상품\s*상세(?:로)?\s*이동\s*$/u, '').trim();
  return withoutNavigationLabel || title;
}

function likelySensitiveQuery(value) {
  if (/\b(?:password|passwd|passcode|otp|one[- ]?time|cvv|cvc|card\s*number|access[_ -]?token|api[_ -]?key|secret)\b/i.test(value)) return true;
  if (/(?:비밀번호|인증번호|일회용\s*코드|카드\s*번호|보안\s*코드|주민등록)/u.test(value)) return true;
  if (/\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/i.test(value)) return true;
  if (/\b(?:https?|ftp):\/\//i.test(value)) return true;
  return value.replace(/[^0-9]/g, '').length >= 11;
}

export function validateDiscoveryRequest(input) {
  if (input === null || typeof input !== 'object' || Array.isArray(input)) {
    throw new RequestError('Discovery request is required', 400, 'DISCOVERY_REQUEST_INVALID');
  }
  const keys = Object.keys(input).sort();
  if (keys.length !== 4 || keys[0] !== 'limit' || keys[1] !== 'offset' ||
      keys[2] !== 'query' || keys[3] !== 'source') {
    throw new RequestError('Discovery request fields are invalid', 400, 'DISCOVERY_REQUEST_INVALID');
  }
  const query = boundedText(input.query, MAX_QUERY_CODE_POINTS, { required: true });
  if (!query || Buffer.byteLength(input.query, 'utf8') > MAX_QUERY_BYTES ||
      Array.from(input.query).length > MAX_QUERY_CODE_POINTS || likelySensitiveQuery(query)) {
    throw new RequestError('Discovery query is invalid', 400, 'DISCOVERY_QUERY_INVALID');
  }
  if (!Object.hasOwn(merchantRegistry, input.source)) {
    throw new RequestError('Discovery source is unsupported', 400, 'DISCOVERY_SOURCE_UNSUPPORTED');
  }
  if (!Number.isSafeInteger(input.limit) || input.limit < 1 || input.limit > 20) {
    throw new RequestError('Discovery limit is invalid', 400, 'DISCOVERY_LIMIT_INVALID');
  }
  if (!Number.isSafeInteger(input.offset) || input.offset < 0 || input.offset > MAX_OFFSET) {
    throw new RequestError('Discovery offset is invalid', 400, 'DISCOVERY_OFFSET_INVALID');
  }
  return { query, source: input.source, limit: input.limit, offset: input.offset };
}

const priceAmount = '(?:[0-9]{1,3}(?:,[0-9]{3})+|[0-9]{1,12})';
const priceTokenPattern = new RegExp(`(?<![0-9,])(?:₩\\s*${priceAmount}(?:\\s*원)?|${priceAmount}\\s*원)(?![0-9,])`, 'u');

export function extractPriceText(value) {
  const text = boundedText(value, MAX_CONTEXT_LENGTH);
  const match = text.match(priceTokenPattern)?.[0];
  if (!match) return undefined;
  return Array.from(match.replace(/\s+/gu, '')).slice(0, MAX_PRICE_TEXT_LENGTH).join('');
}

export function normalizeDiscoveryResults(request, rawResults) {
  if (!Array.isArray(rawResults)) throw new DiscoveryError('DISCOVERY_RESULT_INVALID');
  const merchant = merchantRegistry[request.source];
  const indexes = new Map();
  const candidates = [];
  for (const raw of rawResults) {
    if (raw === null || typeof raw !== 'object' || Array.isArray(raw)) continue;
    const url = merchant.canonicalProductUrl(raw.url);
    const title = boundedTitle(raw.title);
    if (!url || !title) continue;
    const context = boundedText(raw.context, MAX_CONTEXT_LENGTH);
    const priceText = extractPriceText(raw.priceText) ?? extractPriceText(context);
    const result = { source: request.source, url, title, context };
    if (priceText) result.priceText = priceText;
    const existing = indexes.get(url);
    if (existing === undefined) {
      indexes.set(url, candidates.length);
      candidates.push(result);
      continue;
    }
    const score = (candidate) => {
      const generic = /^(?:상품\s*상세(?:로)?\s*이동|상품\s*상세|product\s*details?)$/iu.test(candidate.title);
      return Array.from(candidate.title).length * 20 + Math.min(Array.from(candidate.context).length, 400) +
        (candidate.priceText ? 50 : 0) - (generic ? 1_000 : 0);
    };
    if (score(result) > score(candidates[existing])) candidates[existing] = result;
  }
  return candidates.slice(request.offset, request.offset + request.limit);
}

export function discoveryResponse(source, results) {
  return {
    protocolVersion: 1,
    results,
    coverage: { source, status: results.length > 0 ? 'SUCCEEDED' : 'EMPTY', count: results.length },
  };
}

const safeFailureCodes = new Set([
  'DISCOVERY_BROWSER_UNAVAILABLE',
  'DISCOVERY_CANCELLED',
  'DISCOVERY_DNS_FAILED',
  'DISCOVERY_FAILED',
  'DISCOVERY_NAVIGATION_BLOCKED',
  'DISCOVERY_RESULT_INVALID',
  'DISCOVERY_TIMEOUT',
  'DISCOVERY_UPSTREAM_FAILED',
  'PRIVATE_NETWORK_BLOCKED',
]);

export function discoveryFailureResponse(source, error) {
  const proposed = error instanceof DiscoveryError ? error.code : 'DISCOVERY_FAILED';
  const reasonCode = safeFailureCodes.has(proposed) ? proposed : 'DISCOVERY_FAILED';
  return {
    protocolVersion: 1,
    results: [],
    coverage: { source, status: 'FAILED', reasonCode, count: 0 },
  };
}

const deniedIpv4 = new net.BlockList();
for (const [address, prefix] of [
  ['0.0.0.0', 8], ['10.0.0.0', 8], ['100.64.0.0', 10], ['127.0.0.0', 8],
  ['169.254.0.0', 16], ['172.16.0.0', 12], ['192.0.0.0', 24], ['192.0.2.0', 24],
  ['192.88.99.0', 24], ['192.168.0.0', 16], ['198.18.0.0', 15],
  ['198.51.100.0', 24], ['203.0.113.0', 24], ['224.0.0.0', 4], ['240.0.0.0', 4],
]) deniedIpv4.addSubnet(address, prefix, 'ipv4');
const deniedIpv6 = new net.BlockList();
deniedIpv6.addAddress('::', 'ipv6');
deniedIpv6.addAddress('::1', 'ipv6');
for (const [address, prefix] of [
  ['::', 96], ['::ffff:0:0', 96], ['64:ff9b::', 96], ['64:ff9b:1::', 48], ['100::', 64],
  ['2001:2::', 48], ['2001:10::', 28], ['2001:db8::', 32], ['fc00::', 7],
  ['fe80::', 10], ['fec0::', 10], ['ff00::', 8],
]) deniedIpv6.addSubnet(address, prefix, 'ipv6');

export function isPublicIpAddress(address) {
  const family = net.isIP(address);
  if (family === 4) return !deniedIpv4.check(address, 'ipv4');
  if (family === 6) return !deniedIpv6.check(address, 'ipv6');
  return false;
}

export async function assertPublicResolvedUrl(raw, { lookup = dnsLookup, cache = new Map() } = {}) {
  let url;
  try { url = new URL(raw); } catch { throw new DiscoveryError('DISCOVERY_NAVIGATION_BLOCKED'); }
  if (url.protocol !== 'https:' || url.username || url.password || url.port || !isPublicHostname(url.hostname)) {
    throw new DiscoveryError('DISCOVERY_NAVIGATION_BLOCKED');
  }
  const literalFamily = net.isIP(url.hostname);
  if (literalFamily) {
    if (!isPublicIpAddress(url.hostname)) throw new DiscoveryError('PRIVATE_NETWORK_BLOCKED');
    return url;
  }
  const host = url.hostname.toLowerCase().replace(/\.$/, '');
  let pending = cache.get(host);
  if (!pending) {
    pending = Promise.resolve().then(() => lookup(host, { all: true, verbatim: true }));
    cache.set(host, pending);
  }
  let records;
  try { records = await pending; }
  catch { throw new DiscoveryError('DISCOVERY_DNS_FAILED'); }
  const list = Array.isArray(records) ? records : [records];
  if (list.length === 0 || list.some((record) => !record || !isPublicIpAddress(record.address))) {
    throw new DiscoveryError('PRIVATE_NETWORK_BLOCKED');
  }
  return url;
}

function rawAnchorProjection(anchors, maximum) {
  return anchors.slice(0, maximum).map((anchor) => {
    const image = anchor.querySelector('img');
    const container = anchor.closest('article, li, [data-testid*="product" i], [class*="product" i], [class*="goods" i]') ||
      anchor.parentElement;
    const clean = (value) => (value || '').replace(/\s+/gu, ' ').trim();
    const titleCandidates = [
      ...anchor.querySelectorAll('h1, h2, h3, h4, [itemprop="name"], [data-testid*="name" i], [data-testid*="title" i], [class*="name" i], [class*="title" i], span, p'),
    ].filter((node) => node.children.length === 0).map((node) => clean(node.textContent));
    titleCandidates.push(clean(anchor.getAttribute('aria-label')), clean(anchor.getAttribute('title')),
      clean(image?.getAttribute('alt')), clean(anchor.textContent));
    const titleScore = (value) => {
      const length = Array.from(value).length;
      if (length < 4 || length > 180) return -10_000;
      if (/^(?:담기|배송|샛별배송|상품\s*상세(?:로)?\s*이동|product\s*details?)$/iu.test(value)) return -9_000;
      let score = length;
      if (/(?:[0-9][0-9,]*\s*원|₩|[0-9]+%)/u.test(value)) score -= 500;
      if (/(?:쿠폰|첫구매\s*최대혜택가|리뷰|별점)/u.test(value)) score -= 250;
      return score;
    };
    const title = titleCandidates.reduce((best, value) => titleScore(value) > titleScore(best) ? value : best, '');
    const priceNode = container?.querySelector(
      '[class*="sales-price" i], [class*="sale-price" i], [class*="sale_price" i], [data-testid*="sale-price" i], [data-testid*="price" i]',
    );
    return {
      url: anchor.href || '',
      title,
      context: container?.innerText || anchor.textContent || '',
      priceText: priceNode?.textContent || '',
    };
  });
}

function sameSearchParameters(actual, expected, optional = {}) {
  const expectedEntries = [...expected.searchParams];
  if (!expectedEntries.every(([key, value]) => {
    const values = actual.searchParams.getAll(key);
    return values.length === 1 && values[0] === value;
  })) return false;
  for (const [key, value] of actual.searchParams) {
    if (expected.searchParams.has(key)) continue;
    const allowedValues = optional[key];
    if (!Array.isArray(allowedValues) || !allowedValues.includes(value) ||
        actual.searchParams.getAll(key).length !== 1) return false;
  }
  return true;
}

function allowedSearchNavigation(merchant, raw, expected) {
  let url;
  try { url = new URL(raw); } catch { return false; }
  return url.protocol === 'https:' && !url.username && !url.password && !url.port && !url.hash &&
    exactHostMatches(url.hostname, merchant.searchHosts) && merchant.searchPaths.includes(url.pathname) &&
    sameSearchParameters(url, expected, merchant.optionalSearchParameters);
}

function isPrimaryDocumentRequest(networkRequest, primaryPage) {
  if (typeof networkRequest.frame !== 'function') return true;
  try {
    const frame = networkRequest.frame();
    return frame.page() === primaryPage && frame.parentFrame() === null;
  } catch {
    return false;
  }
}

async function continueWithoutReferer(route, networkRequest) {
  let headers;
  try {
    if (typeof networkRequest.allHeaders === 'function') headers = await networkRequest.allHeaders();
    else if (typeof networkRequest.headers === 'function') headers = networkRequest.headers();
  } catch {
    headers = undefined;
  }
  if (!headers) return route.continue();
  const sanitized = { ...headers };
  for (const key of Object.keys(sanitized)) {
    if (key.toLowerCase() === 'referer') delete sanitized[key];
  }
  return route.continue({ headers: sanitized });
}

async function waitForProductAnchors({ page, merchant, controller, timeoutMs, pollMs, policyFailure }) {
  const deadline = Date.now() + timeoutMs;
  let raw = [];
  do {
    if (controller.signal.aborted) return raw;
    const failure = policyFailure();
    if (failure) throw failure;
    raw = await page.locator('a[href]').evaluateAll(rawAnchorProjection, MAX_ANCHORS);
    if (raw.some((candidate) => merchant.canonicalProductUrl(candidate?.url))) return raw;
    const remaining = deadline - Date.now();
    if (remaining <= 0) return raw;
    await new Promise((resolve) => setTimeout(resolve, Math.min(pollMs, remaining)));
  } while (true);
}

export function createPlaywrightDiscovery({
  browserType = chromium,
  lookup = dnsLookup,
  timeoutMs = DEFAULT_TIMEOUT_MS,
  readinessTimeoutMs = DEFAULT_READINESS_TIMEOUT_MS,
  readinessPollMs = DEFAULT_READINESS_POLL_MS,
  channel = process.env.PLAYWRIGHT_CHANNEL?.trim() || undefined,
} = {}) {
  if (!browserType || typeof browserType.launch !== 'function' || typeof lookup !== 'function' ||
      !Number.isSafeInteger(timeoutMs) || timeoutMs < 1_000 || timeoutMs > 60_000 ||
      !Number.isSafeInteger(readinessTimeoutMs) || readinessTimeoutMs < 0 || readinessTimeoutMs > 10_000 ||
      !Number.isSafeInteger(readinessPollMs) || readinessPollMs < 10 || readinessPollMs > 1_000 ||
      (channel !== undefined && (typeof channel !== 'string' || !/^[A-Za-z0-9._-]{1,40}$/.test(channel)))) {
    throw new TypeError('Invalid discovery configuration');
  }
  return async function discover(request, outerSignal) {
    const merchant = merchantRegistry[request.source];
    if (!merchant) throw new DiscoveryError('DISCOVERY_FAILED');
    const controller = new AbortController();
    let timedOut = false;
    let browser;
    let context;
    let primaryPage;
    let policyFailure;
    const close = () => {
      void context?.close().catch(() => {});
      void browser?.close().catch(() => {});
    };
    const onOuterAbort = () => controller.abort();
    if (outerSignal?.aborted) controller.abort();
    else outerSignal?.addEventListener('abort', onOuterAbort, { once: true });
    const timer = setTimeout(() => {
      timedOut = true;
      controller.abort();
    }, timeoutMs);
    controller.signal.addEventListener('abort', close, { once: true });
    try {
      if (controller.signal.aborted) throw new DiscoveryError('DISCOVERY_CANCELLED');
      try {
        const launchOptions = {
          headless: true,
          args: [
            '--disable-background-networking', '--disable-component-update', '--disable-default-apps',
            '--disable-quic', '--disable-sync', '--metrics-recording-only', '--no-first-run',
          ],
        };
        if (channel) launchOptions.channel = channel;
        browser = await browserType.launch(launchOptions);
      } catch {
        if (controller.signal.aborted) throw new DiscoveryError(timedOut ? 'DISCOVERY_TIMEOUT' : 'DISCOVERY_CANCELLED');
        throw new DiscoveryError('DISCOVERY_BROWSER_UNAVAILABLE');
      }
      if (controller.signal.aborted) throw new DiscoveryError(timedOut ? 'DISCOVERY_TIMEOUT' : 'DISCOVERY_CANCELLED');
      context = await browser.newContext({
        acceptDownloads: false,
        serviceWorkers: 'block',
        locale: 'ko-KR',
        viewport: { width: 1280, height: 900 },
      });
      const dnsCache = new Map();
      const searchUrl = merchant.searchUrl(request.query);
      const expectedSearchUrl = new URL(searchUrl);
      await context.route('**/*', async (route) => {
        const networkRequest = route.request();
        if (networkRequest.method() !== 'GET') {
          await route.abort('blockedbyclient').catch(() => {});
          return;
        }
        if (['image', 'media', 'font'].includes(networkRequest.resourceType())) {
          await route.abort('blockedbyclient').catch(() => {});
          return;
        }
        try {
          const url = await assertPublicResolvedUrl(networkRequest.url(), { lookup, cache: dnsCache });
          if (networkRequest.resourceType() === 'document') {
            if (!isPrimaryDocumentRequest(networkRequest, primaryPage)) {
              await route.abort('blockedbyclient').catch(() => {});
              return;
            }
            if (!allowedSearchNavigation(merchant, url, expectedSearchUrl)) {
              throw new DiscoveryError('DISCOVERY_NAVIGATION_BLOCKED');
            }
          }
          await continueWithoutReferer(route, networkRequest);
        } catch (error) {
          policyFailure ??= error instanceof DiscoveryError ? error : new DiscoveryError('DISCOVERY_NAVIGATION_BLOCKED');
          await route.abort('blockedbyclient').catch(() => {});
        }
      });
      if (typeof context.routeWebSocket === 'function') {
        await context.routeWebSocket('**/*', (socket) => socket.close());
      }
      context.on('page', (page) => {
        if (primaryPage && page !== primaryPage) void page.close().catch(() => {});
      });
      primaryPage = await context.newPage();
      primaryPage.setDefaultNavigationTimeout(timeoutMs);
      primaryPage.setDefaultTimeout(Math.min(timeoutMs, 5_000));
      primaryPage.on('popup', (popup) => void popup.close().catch(() => {}));
      primaryPage.on('download', (download) => void download.cancel().catch(() => {}));
      primaryPage.on('dialog', (dialog) => void dialog.dismiss().catch(() => {}));
      await assertPublicResolvedUrl(searchUrl, { lookup, cache: dnsCache });
      let response;
      try {
        response = await primaryPage.goto(searchUrl, { waitUntil: 'domcontentloaded', timeout: timeoutMs });
      } catch {
        if (policyFailure) throw policyFailure;
        if (controller.signal.aborted) throw new DiscoveryError(timedOut ? 'DISCOVERY_TIMEOUT' : 'DISCOVERY_CANCELLED');
        throw new DiscoveryError('DISCOVERY_UPSTREAM_FAILED');
      }
      if (policyFailure) throw policyFailure;
      if (!response || response.status() < 200 || response.status() >= 400) {
        throw new DiscoveryError('DISCOVERY_UPSTREAM_FAILED');
      }
      if (!allowedSearchNavigation(merchant, primaryPage.url(), expectedSearchUrl)) {
        throw new DiscoveryError('DISCOVERY_NAVIGATION_BLOCKED');
      }
      const raw = await waitForProductAnchors({
        page: primaryPage,
        merchant,
        controller,
        timeoutMs: Math.min(readinessTimeoutMs, timeoutMs),
        pollMs: readinessPollMs,
        policyFailure: () => policyFailure,
      });
      if (controller.signal.aborted) throw new DiscoveryError(timedOut ? 'DISCOVERY_TIMEOUT' : 'DISCOVERY_CANCELLED');
      return raw;
    } catch (error) {
      if (error instanceof DiscoveryError) throw error;
      if (controller.signal.aborted) throw new DiscoveryError(timedOut ? 'DISCOVERY_TIMEOUT' : 'DISCOVERY_CANCELLED');
      throw new DiscoveryError('DISCOVERY_FAILED');
    } finally {
      clearTimeout(timer);
      outerSignal?.removeEventListener('abort', onOuterAbort);
      await context?.close().catch(() => {});
      await browser?.close().catch(() => {});
    }
  };
}

export const discoverMerchantProducts = createPlaywrightDiscovery();
