// Candidate grid regression: the authenticated route fixture from
// curation-layout-recovery.cjs, with controlled hydration and modal reads.
const id = 'e5100000-0000-4000-8000-000000000002';
const timestamp = '2026-09-11T00:00:00Z';
const scope = { country: 'US', allowedItems: [], blockedItems: [], urlMode: 'NONE' };

function fixture(count = 6, targetCount = 1) {
  const targets = Array.from({ length: targetCount }, (_, i) => ({
    id: `target-${i}`, planId: 'plan', curationId: id, userId: 'fixture',
    title: `Headphones ${i + 1}`, normalizedIntent: 'wireless headphones', category: 'Audio',
    allocatedBudget: { amount: '200', currency: 'USD' }, researchScope: scope,
    orderIndex: i, targetHashSchema: 'vitlane.plan-target.v1', version: 1,
    createdAt: timestamp, updatedAt: timestamp,
  }));
  const pools = targets.map(target => ({
    targetId: target.id, version: 1, expandOrdinal: 0, latestMode: 'APPEND',
    sourceCoverage: [{ source: 'SHOPIFY', status: 'SUCCEEDED', candidateCount: count }],
    products: Array.from({ length: count }, (_, i) => ({
      candidateId: `${target.id}-candidate-${i}`, source: 'SHOPIFY',
      title: `Wireless headphones ${i + 1} with active noise cancellation`,
      description: 'Synthetic catalog fixture', intentPoint: 'Matches the requested battery life and fit.',
      features: ['Wireless connection'], specifications: ['USB-C'], categories: ['Audio'],
      priceMinimumMinor: 9900, priceMaximumMinor: 9900, currency: 'USD',
      locator: { kind: 'PRODUCT_URL', productUrl: `https://shop.example/products/${i}` },
      previewVariant: { variantId: `gid://shopify/ProductVariant/${i}`, title: 'Black', selectedOptions: [],
        priceMinor: 9900, currency: 'USD', available: true, sellerName: 'Fixture seller', sellerDomain: 'shop.example' },
    })), hiddenProducts: [], messages: [],
  }));
  return {
    plan: { id: 'plan', userId: 'fixture', originalIntent: 'Compare headphones', planningMode: 'SINGLE',
      executionMode: 'EXPERIMENT', totalBudget: { amount: '200', currency: 'USD' },
      locationContext: { country: 'US' }, researchScope: scope, createdAt: timestamp },
    curation: { id, shoppingPlanId: 'plan', userId: 'fixture', phase: 'CURATING', version: 2,
      createdAt: timestamp, updatedAt: timestamp }, targets,
    research: { groups: targets.map(t => ({ session: { id: `session-${t.id}`, planTargetId: t.id, status: 'REVIEWING', version: 1 }, candidates: [] })) },
    cart: { selections: [] }, availableActions: [], timeline: [], latestArtifact: 'CURATION_BOARD',
    intelligence: [], coverage: 'NONE',
    conversation: { schemaVersion: 'vitlane.curation-conversation.v1', version: 0, unfinished: true, messages: [], requests: [] },
    catalogResearch: { schemaVersion: 'vitlane.catalog-research-workspace.v3', pools, configurations: [], interactions: [], messages: [] },
  };
}
async function installAPI(page, state, unexpected, hydration) {
  await page.route('**/api/**', async route => {
    const request = route.request(), pathname = new URL(request.url()).pathname;
    if (!pathname.startsWith('/api/')) return route.continue();
    if (pathname === '/api/v1/analytics/config' && request.method() === 'GET') {
      return route.fulfill({ json: { schemaVersion: 'vitlane.analytics-config.v1', mode: 'disabled', measurementId: '', release: 'fixture' } });
    }
    if (pathname === '/api/v1/curations/product-notices/sync' && request.method() === 'POST') {
      return route.fulfill({ json: { schemaVersion: 'vitlane.curation-notices.v1', curationIds: [] } });
    }
    if (pathname === `/api/v1/curations/${id}/background-research` && request.method() === 'GET') {
      return route.fulfill({ json: { schemaVersion: 'vitlane.background-research.v1', subscriptions: [], findings: [] } });
    }
    let json;
    if (pathname === '/api/v1/me') json = { user: { id: 'fixture', email: 'fixture@vitlane.example', displayName: 'Fixture', marketingAdmin: false, phase5Operator: false } };
    else if (pathname === '/api/v1/me/preferences') {
      const locale = await page.evaluate(() => localStorage.getItem('vitlane.locale.v2') || 'ko-KR');
      const preferences = { schemaVersion: 'vitlane.user-preferences.v1', version: 0, uiLocale: locale, researchCountry: 'US', preferredCurrency: 'USD' };
      json = { preferences, effective: preferences };
    }
    else if (pathname.endsWith('/threads') && request.method() === 'GET') json = { schemaVersion: 'vitlane.curation-thread.v2', controlMode: { mode: 'AUTO', version: 1 }, threads: [] };
    else if (pathname.endsWith('/criteria') && request.method() === 'GET') json = null;
    else if (pathname.endsWith('/budget')) json = { schemaVersion: 'vitlane.curation-budget.v1', version: 0, researchVersion: 0, enabled: false, currency: 'USD', totalAmount: null, allocations: state.targets.map(t=>({targetId:t.id,quantity:1,amount:null})) };
    else if (pathname.endsWith('/research-settings')) json = { schemaVersion: 'vitlane.research-settings.v1', country: 'US', version: 0 };
    else if (pathname.endsWith('/exchange-rate')) json = { schemaVersion: 'vitlane.exchange-rate.v1', status: 'UNAVAILABLE' };
    else if (pathname === '/api/v1/curations' && request.method() === 'GET') json = { schemaVersion: 'vitlane.curation-list.v2', curations: [] };
    else if (pathname === '/api/v1/support/summary') json = { schemaVersion: 'vitlane.support-summary.v1', unread: 0 };
    else if (pathname === '/api/v1/auth/capabilities') json = { googleEnabled: true, localReviewEnabled: false, localReviewSeeded: false, localReviewProfiles: [] };
    else if (pathname.endsWith('/workspace')) json = state;
    else if (pathname.endsWith('/cart') && request.method() === 'GET') json = { schemaVersion: 'vitlane.cart-view.v2', curationId: id, version: 0, country: 'US', currency: 'USD', items: [] };
    else if (pathname.endsWith('/catalog-research/hydrations')) {
      const body = request.postDataJSON();
      const catalog = hydration ? await hydration() : state.catalogResearch;
      json = { ...catalog, schemaVersion: 'vitlane.catalog-research-hydration.v1',
        pools: catalog.pools.filter(p => p.targetId === body.targetId) };
    } else if (pathname.endsWith('/variant-pages')) {
      const candidateId = pathname.split('/').at(-2);
      const product = state.catalogResearch.pools.flatMap(p => p.products).find(p => p.candidateId === candidateId);
      json = { schemaVersion: 'vitlane.catalog-variant-page.v1', source: 'SHOPIFY', candidateId,
        productTitle: product.title, merchantDomain: 'shop.example', rows: [product.previewVariant],
        pagination: { pageSize: 20, hasPrevious: false, hasNext: false }, observedAt: timestamp,
        metrics: { durationMilliseconds: 0, shopifyCalls: 0, rateRemaining: 0 } };
    } else { unexpected.push(`${request.method()} ${pathname}`); return route.abort(); }
    await route.fulfill({ status: 200, json });
  });
  await page.route(/https:\/\/(?!127\.0\.0\.1|localhost)/, route => {
    unexpected.push('External network request'); return route.abort();
  });
}

module.exports = { id, fixture, installAPI };
