import test from 'node:test';
import assert from 'node:assert/strict';
import {
  DiscoveryError,
  SUPPORTED_MERCHANT_SOURCES,
  assertPublicResolvedUrl,
  createPlaywrightDiscovery,
  extractPriceText,
  isPublicIpAddress,
  normalizeDiscoveryResults,
  validateDiscoveryRequest,
} from '../server/discovery.mjs';

test('discovery request has an exact bounded public-search shape', () => {
  assert.deepEqual(validateDiscoveryRequest({
    query: '  러닝화   방수  ', source: 'MUSINSA', limit: 4, offset: 0,
  }), { query: '러닝화 방수', source: 'MUSINSA', limit: 4, offset: 0 });
  for (const input of [
    { query: 'shoe', source: 'UNKNOWN', limit: 1, offset: 0 },
    { query: 'shoe', source: 'KURLY', limit: 0, offset: 0 },
    { query: 'shoe', source: 'KURLY', limit: 21, offset: 0 },
    { query: 'shoe', source: 'KURLY', limit: 1, offset: -1 },
    { query: 'shoe', source: 'KURLY', limit: 1, offset: 401 },
    { query: 'person@example.com', source: 'KURLY', limit: 1, offset: 0 },
    { query: 'https://merchant.example/item', source: 'KURLY', limit: 1, offset: 0 },
    { query: 'shoe', source: 'KURLY', limit: 1, offset: 0, browserToken: 'secret' },
    { query: 'a'.repeat(301), source: 'KURLY', limit: 1, offset: 0 },
    { query: '가'.repeat(101), source: 'KURLY', limit: 1, offset: 0 },
  ]) assert.throws(() => validateDiscoveryRequest(input));
  assert.deepEqual(SUPPORTED_MERCHANT_SOURCES,
    ['COUPANG', 'ELEVENST', 'MUSINSA', 'KURLY', 'LOTTEON', 'DAISOMALL', 'AUCTION', 'OHOUSE']);
});

test('result normalization canonicalizes registered products, deduplicates, bounds copy, and extracts one won token', () => {
  const request = validateDiscoveryRequest({ query: 'headphones', source: 'ELEVENST', limit: 2, offset: 0 });
  const context = `  공개 상품\n₩ 12,900 원  ${'설명 '.repeat(400)}`;
  const results = normalizeDiscoveryResults(request, [
    { url: 'https://www.11st.co.kr/products/6848013817?tracking=ignored', title: '  무선\u0000 헤드폰  ', context },
    { url: 'https://www.11st.co.kr/products/6848013817', title: '상품 상세로 이동', context: '20,000원' },
    { url: 'https://www.11st.co.kr/products/11111?method=getQnaList', title: 'Q&A', context: 'not a product' },
    { url: 'https://evil.invalid/products/22222', title: 'wrong host', context: '' },
    { url: 'https://offers.11st.co.kr/products/22222', title: 'unregistered subdomain', context: '' },
    { url: 'https://www.11st.co.kr/products/22222', title: '두 번째 상품', context: '판매가 25000원' },
    { url: 'https://www.11st.co.kr/products/33333', title: 'limit excludes this', context: '' },
  ]);
  assert.equal(results.length, 2);
  assert.deepEqual(results[0], {
    source: 'ELEVENST',
    url: 'https://www.11st.co.kr/products/6848013817',
    title: '무선 헤드폰',
    context: results[0].context,
    priceText: '₩12,900원',
  });
  assert.ok(Array.from(results[0].context).length <= 1000);
  assert.equal(results[1].url, 'https://www.11st.co.kr/products/22222');
  assert.equal(results[1].priceText, '25000원');
  assert.equal(extractPriceText('정가 19,000원 할인 15,000원'), '19,000원');

  const next = normalizeDiscoveryResults({ ...request, limit: 1, offset: 1 }, [
    { url: 'https://www.11st.co.kr/products/11111', title: '첫 상품', context: '' },
    { url: 'https://www.11st.co.kr/products/22222', title: '다음 상품', context: '25,000원' },
  ]);
  assert.equal(next.length, 1);
  assert.equal(next[0].url, 'https://www.11st.co.kr/products/22222');

  const preferred = normalizeDiscoveryResults({ ...request, limit: 1 }, [
    { url: 'https://www.11st.co.kr/products/44444', title: '상품 상세로 이동', context: '' },
    { url: 'https://www.11st.co.kr/products/44444', title: '쿠션 러닝화 화이트 상품상세로 이동', context: '판매가 39,900원' },
  ]);
  assert.equal(preferred[0].title, '쿠션 러닝화 화이트');
});

test('query-identity merchant URLs keep only the canonical product identifier', () => {
  const request = validateDiscoveryRequest({ query: '수납함', source: 'DAISOMALL', limit: 1, offset: 0 });
  assert.deepEqual(normalizeDiscoveryResults(request, [{
    url: 'https://www.daisomall.co.kr/pd/pdr/SCR_PDR_0001?campaign=bad&PdNo=1058641#ignored',
    title: '수납함', context: '3,000원',
  }]), []);
  assert.deepEqual(normalizeDiscoveryResults(request, [{
    url: 'https://www.daisomall.co.kr/pd/pdr/SCR_PDR_0001?campaign=ignored&PdNo=1058641',
    title: '수납함', context: '3,000원',
  }]), [{
    source: 'DAISOMALL',
    url: 'https://www.daisomall.co.kr/pd/pdr/SCR_PDR_0001?pdNo=1058641',
    title: '수납함', context: '3,000원', priceText: '3,000원',
  }]);
  assert.deepEqual(normalizeDiscoveryResults(request, [{
    url: 'https://www.daisomall.co.kr/pd/pdr/SCR_PDR_0001?pdNo=1058641&PDNO=1058642',
    title: 'conflicting identity', context: '3,000원',
  }]), []);
});

test('expanded retail sources accept only audited product identities and rebuild canonical URLs', () => {
  for (const [source, raw, canonical] of [
    ['COUPANG', 'https://www.coupang.com/vp/products/8825648110?itemId=25717201283&vendorItemId=92706038164',
      'https://www.coupang.com/vp/products/8825648110'],
    ['OHOUSE', 'https://store.ohou.se/goods/102652?affect_type=Search',
      'https://ohou.se/productions/102652/selling'],
  ]) {
    const request = validateDiscoveryRequest({ query: '상품', source, limit: 1, offset: 0 });
    assert.deepEqual(normalizeDiscoveryResults(request, [{
      url: raw, title: '검증된 상품', context: '판매가 39,900원',
    }]), [{
      source, url: canonical, title: '검증된 상품', context: '판매가 39,900원', priceText: '39,900원',
    }], source);
    for (const rejected of [
      raw.replace('https://', 'http://'),
      raw.replace(new URL(raw).hostname, `${new URL(raw).hostname}.evil.test`),
      raw.replace(/([1-9][0-9]{4,})/, '0$1'),
    ]) {
      assert.deepEqual(normalizeDiscoveryResults(request, [{
        url: rejected, title: '거절될 상품', context: '',
      }]), [], `${source}:${rejected}`);
    }
  }
});

test('Auction upgrades its audited HTTP result identity to one exact HTTPS product URL', () => {
  const request = validateDiscoveryRequest({ query: '헤드폰', source: 'AUCTION', limit: 1, offset: 0 });
  assert.deepEqual(normalizeDiscoveryResults(request, [{
    url: 'http://itempage3.auction.co.kr/DetailView.aspx?itemno=D762102946&frm=search',
    title: '무선 헤드폰', context: '59,900원',
  }]), [{
    source: 'AUCTION',
    url: 'https://itempage3.auction.co.kr/DetailView.aspx?itemno=D762102946',
    title: '무선 헤드폰', context: '59,900원', priceText: '59,900원',
  }]);
  for (const url of [
    'http://itempage3.auction.co.kr.evil.test/DetailView.aspx?itemno=D762102946',
    'ftp://itempage3.auction.co.kr/DetailView.aspx?itemno=D762102946',
    'http://user@itempage3.auction.co.kr/DetailView.aspx?itemno=D762102946',
    'http://itempage3.auction.co.kr:8080/DetailView.aspx?itemno=D762102946',
    'http://itempage3.auction.co.kr/DetailView.aspx?itemno=D762102946#reviews',
    'http://itempage3.auction.co.kr/DetailView.aspx?itemno=d762102946',
    'http://itempage3.auction.co.kr/DetailView.aspx?itemno=D762102946&ITEMNO=A1234567',
    'http://itempage3.auction.co.kr/Other.aspx?itemno=D762102946',
  ]) assert.deepEqual(normalizeDiscoveryResults(request, [{ url, title: '거절', context: '' }]), [], url);
});

test('Ohouse does not mix its search-card and canonical product path hosts', () => {
  const request = validateDiscoveryRequest({ query: '의자', source: 'OHOUSE', limit: 1, offset: 0 });
  for (const url of [
    'https://store.ohou.se/productions/102652/selling',
    'https://ohou.se/goods/102652',
    'https://store.ohou.se/goods/102652#reviews',
  ]) assert.deepEqual(normalizeDiscoveryResults(request, [{ url, title: '거절', context: '' }]), [], url);
});

test('DNS policy rejects private, reserved, mixed, and unresolved destinations without network access', async () => {
  for (const address of ['127.0.0.1', '10.0.0.2', '169.254.169.254', '192.0.2.1', '::1', 'fc00::1', '2001:db8::1']) {
    assert.equal(isPublicIpAddress(address), false, address);
  }
  assert.equal(isPublicIpAddress('93.184.216.34'), true);
  assert.equal(isPublicIpAddress('2606:2800:220:1:248:1893:25c8:1946'), true);

  const publicLookup = async () => [{ address: '93.184.216.34', family: 4 }];
  const mixedLookup = async () => [
    { address: '93.184.216.34', family: 4 },
    { address: '127.0.0.1', family: 4 },
  ];
  await assert.doesNotReject(() => assertPublicResolvedUrl('https://shop.real-domain.com/search', { lookup: publicLookup }));
  await assert.rejects(
    () => assertPublicResolvedUrl('https://shop.real-domain.com/search', { lookup: mixedLookup }),
    (error) => error instanceof DiscoveryError && error.code === 'PRIVATE_NETWORK_BLOCKED',
  );
  await assert.rejects(
    () => assertPublicResolvedUrl('https://127.0.0.1/search', { lookup: publicLookup }),
    (error) => error instanceof DiscoveryError && error.code === 'DISCOVERY_NAVIGATION_BLOCKED',
  );
  await assert.rejects(
    () => assertPublicResolvedUrl('https://shop.real-domain.com/search', { lookup: async () => { throw new Error('dns detail'); } }),
    (error) => error instanceof DiscoveryError && error.code === 'DISCOVERY_DNS_FAILED',
  );
});

function fakeBrowserType(rawResults, { finalUrl } = {}) {
  const state = {
    launches: 0, contexts: 0, contextCloses: 0, browserCloses: 0,
    continued: [], aborted: [], websocketCloses: 0, popupCloses: 0, downloadCancels: 0,
    contextOptions: [], launchOptions: [], gotoUrls: [], evaluationCalls: 0,
  };
  return {
    state,
    async launch(options) {
      state.launches++;
      state.launchOptions.push(options);
      let routeHandler;
      let contextPageHandler;
      const pageHandlers = new Map();
      let currentUrl = 'about:blank';
      const page = {
        setDefaultNavigationTimeout() {},
        setDefaultTimeout() {},
        on(name, handler) { pageHandlers.set(name, handler); },
        url() { return currentUrl; },
        locator() {
          return { evaluateAll: async () => {
            state.evaluationCalls++;
            return typeof rawResults === 'function' ? rawResults(state.evaluationCalls) : rawResults;
          } };
        },
        async goto(url) {
          currentUrl = url;
          state.gotoUrls.push(url);
          const requests = [
            { method: 'GET', resourceType: 'document', url },
            { method: 'POST', resourceType: 'fetch', url: 'https://www.musinsa.com/analytics' },
            { method: 'GET', resourceType: 'image', url: 'https://cdn.real-domain.com/image.jpg' },
            { method: 'GET', resourceType: 'script', url: 'https://cdn.real-domain.com/app.js' },
          ];
          for (const value of requests) {
            await routeHandler({
              request: () => ({
                method: () => value.method,
                resourceType: () => value.resourceType,
                url: () => value.url,
                headers: () => ({ accept: '*/*', referer: `${url}#private-fragment` }),
              }),
              continue: async (options) => state.continued.push({ ...value, options }),
              abort: async () => state.aborted.push(value),
            });
          }
          contextPageHandler?.({ close: async () => { state.popupCloses++; } });
          pageHandlers.get('popup')?.({ close: async () => { state.popupCloses++; } });
          pageHandlers.get('download')?.({ cancel: async () => { state.downloadCancels++; } });
          if (finalUrl) currentUrl = typeof finalUrl === 'function' ? finalUrl(url) : finalUrl;
          return { status: () => 200 };
        },
      };
      const context = {
        async route(_pattern, handler) { routeHandler = handler; },
        async routeWebSocket(_pattern, handler) {
          handler({ close: () => { state.websocketCloses++; } });
        },
        on(name, handler) { if (name === 'page') contextPageHandler = handler; },
        async newPage() { return page; },
        async close() { state.contextCloses++; },
      };
      return {
        async newContext(options) {
          state.contexts++;
          state.contextOptions.push(options);
          return context;
        },
        async close() { state.browserCloses++; },
      };
    },
  };
}

test('Playwright discovery uses a fresh ephemeral context, permits only GET, and blocks sockets/downloads/popups', async () => {
  const browserType = fakeBrowserType([{
    url: 'https://www.musinsa.com/products/12345', title: '운동화', context: '판매가 59,000원',
  }]);
  const lookup = async () => [{ address: '93.184.216.34', family: 4 }];
  const discover = createPlaywrightDiscovery({ browserType, lookup, timeoutMs: 5_000 });
  const request = validateDiscoveryRequest({ query: 'shoe', source: 'MUSINSA', limit: 1, offset: 0 });
  for (let i = 0; i < 2; i++) {
    const raw = await discover(request, new AbortController().signal);
    assert.equal(raw[0].title, '운동화');
  }
  assert.equal(browserType.state.launches, 2);
  assert.equal(browserType.state.contexts, 2);
  assert.equal(browserType.state.contextCloses, 2);
  assert.equal(browserType.state.browserCloses, 2);
  assert.equal(browserType.state.continued.every((item) => item.method === 'GET'), true);
  assert.equal(browserType.state.continued.every((item) => !Object.hasOwn(item.options.headers, 'referer')), true);
  assert.equal(browserType.state.aborted.some((item) => item.method === 'POST'), true);
  assert.equal(browserType.state.aborted.some((item) => item.resourceType === 'image'), true);
  assert.equal(browserType.state.websocketCloses, 2);
  assert.equal(browserType.state.popupCloses, 4);
  assert.equal(browserType.state.downloadCancels, 2);
  for (const options of browserType.state.contextOptions) {
    assert.equal(options.acceptDownloads, false);
    assert.equal(options.serviceWorkers, 'block');
  }
});

test('fixed merchant registry builds only the reviewed search entries', async () => {
  const query = '수납함 & 10%';
  const cases = [
    ['COUPANG', 'https://www.coupang.com', '/np/search', 'q', {}, 'https://www.coupang.com/vp/products/12345'],
    ['ELEVENST', 'https://search.11st.co.kr', '/Search.tmall', 'kwd', {}, 'https://www.11st.co.kr/products/12345'],
    ['MUSINSA', 'https://www.musinsa.com', '/search/goods', 'keyword', {}, 'https://www.musinsa.com/products/12345'],
    ['KURLY', 'https://www.kurly.com', '/search', 'sword', {}, 'https://www.kurly.com/goods/12345'],
    ['LOTTEON', 'https://www.lotteon.com', '/csearch/search/search', 'q', { render: 'search', platform: 'pc' }, 'https://www.lotteon.com/p/product/LO123456'],
    ['DAISOMALL', 'https://www.daisomall.co.kr', '/ds/dst/SCR_DST_0015', 'searchTerm', {}, 'https://www.daisomall.co.kr/pd/pdr/SCR_PDR_0001?pdNo=12345'],
    ['AUCTION', 'https://www.auction.co.kr', '/n/search', 'keyword', {}, 'http://itempage3.auction.co.kr/DetailView.aspx?itemno=D1234567'],
    ['OHOUSE', 'https://ohou.se', '/search/index', 'query', {}, 'https://store.ohou.se/goods/12345'],
  ];
  for (const [source, origin, pathname, queryKey, fixed, productUrl] of cases) {
    const browserType = fakeBrowserType([{ url: productUrl, title: '상품', context: '1,000원' }]);
    const discover = createPlaywrightDiscovery({
      browserType,
      lookup: async () => [{ address: '93.184.216.34', family: 4 }],
      timeoutMs: 5_000,
      readinessTimeoutMs: 0,
      channel: 'chrome',
    });
    await discover(validateDiscoveryRequest({ query, source, limit: 1, offset: 0 }), new AbortController().signal);
    const actual = new URL(browserType.state.gotoUrls[0]);
    assert.equal(actual.origin, origin, source);
    assert.equal(actual.pathname, pathname, source);
    assert.equal(actual.searchParams.get(queryKey), query, source);
    for (const [key, value] of Object.entries(fixed)) assert.equal(actual.searchParams.get(key), value, `${source}:${key}`);
    assert.equal(actual.searchParams.size, 1 + Object.keys(fixed).length, source);
    assert.equal(browserType.state.launchOptions[0].channel, 'chrome');
  }
});

test('merchant-owned fixed search state is accepted but unknown query expansion is blocked', async () => {
  for (const [source, extra, productUrl] of [
    ['ELEVENST', ['tabId', 'TOTAL_SEARCH'], 'https://www.11st.co.kr/products/12345'],
    ['MUSINSA', ['gf', 'A'], 'https://www.musinsa.com/products/12345'],
    ['LOTTEON', ['sort', 'ranking'], 'https://www.lotteon.com/p/product/LO123456'],
  ]) {
    const browserType = fakeBrowserType([{ url: productUrl, title: '상품', context: '1,000원' }], {
      finalUrl: (raw) => { const url = new URL(raw); url.searchParams.set(...extra); return url.href; },
    });
    const discover = createPlaywrightDiscovery({
      browserType,
      lookup: async () => [{ address: '93.184.216.34', family: 4 }],
      timeoutMs: 5_000,
      readinessTimeoutMs: 0,
    });
    await assert.doesNotReject(() => discover(
      validateDiscoveryRequest({ query: '상품', source, limit: 1, offset: 0 }),
      new AbortController().signal,
    ));
  }

  const browserType = fakeBrowserType([], {
    finalUrl: (raw) => { const url = new URL(raw); url.searchParams.set('redirect', 'https://evil.invalid'); return url.href; },
  });
  const discover = createPlaywrightDiscovery({
    browserType,
    lookup: async () => [{ address: '93.184.216.34', family: 4 }],
    timeoutMs: 5_000,
    readinessTimeoutMs: 0,
  });
  await assert.rejects(
    () => discover(validateDiscoveryRequest({ query: '상품', source: 'MUSINSA', limit: 1, offset: 0 }), new AbortController().signal),
    (error) => error instanceof DiscoveryError && error.code === 'DISCOVERY_NAVIGATION_BLOCKED',
  );
});

test('CSR discovery waits a bounded interval for a registered product anchor', async () => {
  const browserType = fakeBrowserType((call) => call < 3 ? [] : [{
    url: 'https://www.kurly.com/goods/12345', title: '늦게 렌더된 상품', context: '8,900원',
  }]);
  const discover = createPlaywrightDiscovery({
    browserType,
    lookup: async () => [{ address: '93.184.216.34', family: 4 }],
    timeoutMs: 5_000,
    readinessTimeoutMs: 200,
    readinessPollMs: 10,
    channel: undefined,
  });
  const results = await discover(
    validateDiscoveryRequest({ query: '사과', source: 'KURLY', limit: 1, offset: 0 }),
    new AbortController().signal,
  );
  assert.equal(browserType.state.evaluationCalls, 3);
  assert.equal(results[0].title, '늦게 렌더된 상품');
  assert.equal('channel' in browserType.state.launchOptions[0], false);
});
