const { firefox } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:15173';
const entry = path.resolve(__dirname, '../.amazon-step1-e2e.tsx');
const ref = (asin = 'B012345678') => ({ source: 'AMAZON', marketplace: 'US', asin });
const product = (asin = 'B012345678') => ({ candidateId: 'anchor-card', source: 'AMAZON', title: 'Wireless Headset', description: '', categories: [], features: [], specifications: [], priceMinimumMinor: 12999, priceMaximumMinor: 12999, currency: 'USD', hydration: { status: 'READY', retryable: false }, variantObservation: { observationId: 'observed', variantRef: ref(asin), price: { kind: 'OBSERVED', amountMinor: 12999, currency: 'USD' }, seller: { kind: 'UNKNOWN' }, availability: 'UNKNOWN', deliveryEligibility: 'UNCONFIRMED', purchaseRoute: 'EXTERNAL', productUrl: `https://www.amazon.com/dp/${asin}`, observedAt: '2026-09-10T00:00:00Z', refreshAfter: '2026-09-10T00:15:00Z' } });
async function run() {
 await fs.writeFile(entry, `import React, {useEffect,useState} from 'react'; import {createRoot} from 'react-dom/client'; import {LocaleProvider} from './src/shared/i18n'; import {AmazonCandidate} from './src/products/curation/iface/AmazonCandidate'; import {saveCatalogInteraction} from './src/products/curation/research/infra/liveCatalogReviewApi'; import {AmazonAPIUsage} from './src/products/curation/research/iface/AmazonAPIUsage'; import './src/styles.css'; import './src/products/curation/iface/catalog-curation-research.css'; function App(){const [interactions,setInteractions]=useState({});useEffect(()=>{fetch('/api/v1/curations/curation/catalog-research').then(r=>r.json()).then(v=>setInteractions(v.interactions))},[]); const values=Object.values(interactions);return <LocaleProvider><main><AmazonCandidate product={${JSON.stringify(product())}} curationId="curation" targetId="target" targetTitle="Headphones" interactions={interactions} signals={{pinned:values.some(v=>v.pinned),liked:values.some(v=>v.sentiment==='LIKE'),disliked:values.some(v=>v.sentiment==='DISLIKE')}} onInteraction={async(key,value,likedSnapshot,relationToken)=>{const [candidateId,variantId]=key.split('::');await saveCatalogInteraction({curationId:'curation',candidateId,variantId,...value,likedSnapshot,relationToken});setInteractions(old=>({...old,[key]:value}))}}/><AmazonAPIUsage/></main></LocaleProvider>};createRoot(document.getElementById('root')!).render(<App/>);`);
 await fs.writeFile(entry.replace(".tsx", ".html"), '<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="/.amazon-step1-e2e.tsx"></script>');
 const browser = await firefox.launch({ headless: true });
 try { for (const locale of ['en-US', 'ko-KR']) {
  let interactions = {};
  let selected = ref(), version = 0, recordVersion = 0, checked = false, providerCalls = 0, quotaFail = false;
  const context = await browser.newContext({ locale: 'ko-KR', viewport: { width: 320, height: 880 } });
  await context.addInitScript(locale => localStorage.setItem('vitlane.locale.v1', locale), locale);
  const page = await context.newPage(), errors = []; page.on('pageerror', e => errors.push(e.message));
  await page.route('**/__amazon-step1', route => route.fulfill({ contentType: 'text/html', body: '<!doctype html><meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="/.amazon-step1-e2e.tsx"></script>' }));
  await page.route('**/api/v1/**', async route => {
   const req = route.request(), url = req.url(); let body;
   assert(!/cart|order|checkout/.test(url), `Unexpected checkout: ${url}`);
   if (url.includes('/admin/catalog-sources/amazon/usage')) {
    if (quotaFail) return route.fulfill({ status: 503, json: { error: { code: 'UNAVAILABLE' } } });
    body = { source: 'AMAZON', enabled: true, quota: { limit: 100, used: 8, remaining: 92, resetAt: '2026-10-10T00:00:00Z', observedAt: '2026-09-10T00:00:00Z' }, estimatedRemaining: 91, attemptsSinceObservation: 1, requests24h: 3, succeeded24h: 2, failures24h: [{ reasonCode: 'AMAZON_SCHEMA_MISMATCH', count: 1 }], lastFailureCode: 'AMAZON_SCHEMA_MISMATCH' };
   } else if (url.endsWith('/catalog-research')) body = { interactions };
   else if (url.endsWith('/interaction')) { const value = req.postDataJSON(); const asin = decodeURIComponent(url.split('/variants/')[1].split('/')[0]); interactions['anchor-card::'+asin] = {pinned:value.pinned,sentiment:value.sentiment}; return route.fulfill({status:204}); }
   else if (url.endsWith('/state')) body = { variantRef: selected, configurationVersion: version, purchaseFeedback: { version: recordVersion, records: recordVersion ? [{ candidateId: 'anchor-card', variantRef: selected, checked, version: recordVersion, recordedAt: '2026-09-10T00:00:00Z', evidence: 'SELF_REPORTED' }] : [] } };
   else if (url.endsWith('/variant-pages')) { providerCalls++; body = { source: 'AMAZON', candidateId: 'anchor-card', relationToken: 'a'.repeat(64), observedAt: '2026-09-10T00:00:00Z', rows: [{ variantId: 'B987654321', title: 'Blue', selectedOptions: [{ name: 'Color', value: 'Blue' }], available: true, priceUnknown: true }], pagination: { pageSize: 20, hasNext: false } }; }
   else if (url.endsWith('/resolve')) { providerCalls++; assert.equal(req.postDataJSON().variantRef.asin, 'B987654321'); body = { product: product('B987654321') }; }
   else if (url.endsWith('/configuration')) { assert.equal(req.postDataJSON().expectedVersion, version); selected = ref(req.postDataJSON().variantId); version++; return route.fulfill({ status: 204 }); }
   else if (url.endsWith('/purchase-check')) { const data = req.postDataJSON(); assert.equal(data.variantRef.asin, selected.asin); assert.equal(data.expectedVersion, recordVersion); if (data.checked) assert.equal(data.snapshot?.productTitle, 'Wireless Headset'); else assert.equal(data.snapshot, undefined); checked = data.checked; recordVersion++; body = {version:recordVersion,records:[{candidateId:'anchor-card',variantRef:selected,checked,version:recordVersion,recordedAt:'2026-09-10T00:00:00Z',evidence:'SELF_REPORTED'}]}; }
   else if (url.endsWith('/hydrations')) { providerCalls++; body = { pools: [{ targetId: 'target', products: [product(selected.asin)], hiddenProducts: [], messages: [] }], configurations: [], interactions: [], messages: [] }; }
   else throw new Error(`Unexpected API: ${url}`);
   return route.fulfill({ status: 200, json: body });
  });
  await page.goto(`${base}/.amazon-step1-e2e.html`); const ko = locale === 'ko-KR';
  const button = (en, kr) => page.getByRole('button', { name: ko ? kr : en, exact: true });
  await button('External purchase', '외부 구매').waitFor();
  assert.equal(await page.getByRole('button', { name: /Add to cart|장바구니 담기/ }).count(), 0);
  assert.equal(await page.getByText('OPENWEBNINJA').count(), 0);
  await button('View options for Wireless Headset', 'Wireless Headset 옵션 보기').click();
  await button('Like', '좋아요').click(); await page.waitForFunction(()=>document.querySelector('.candidate-detail button[aria-pressed="true"]'));
  await button('Pin','Pin').click(); await button('Unpin','Pin 해제').waitFor();
  await page.getByRole('radio',{name:/^Blue/}).click(); assert.equal(selected.asin, 'B012345678'); await button('Save option','옵션 저장').click();
  await page.locator('.candidate-detail').waitFor({ state: 'hidden' }); assert.equal(selected.asin, 'B987654321'); assert.equal(providerCalls, 2);
  await button('Purchase check', '구매 체크').click(); await page.getByRole('alertdialog').getByRole('button', { name: ko ? '확인' : 'Confirm', exact: true }).click(); await button('Undo', '체크 취소').waitFor();
  await page.locator('.vt-candidate-card__signal.is-purchased').waitFor();
  await page.reload(); await page.locator('.vt-candidate-card__signal.is-purchased').waitFor(); await button('Undo', '체크 취소').click(); await button('Purchase check', '구매 체크').waitFor();
  await page.locator('.vt-candidate-card__signal.is-purchased').waitFor({ state: 'hidden' });
  assert.equal(interactions['anchor-card::B012345678'].sentiment, 'LIKE');
  await page.locator('.vt-candidate-card__signal.is-pin').waitFor();
  await page.locator('.vt-candidate-card__signal.is-like').waitFor();
  assert.equal(providerCalls, 3, 'only selected-option hydration on reload requires another provider call');
  await page.getByText('92 / 100', { exact: true }).waitFor(); quotaFail = true;
  await button('Refresh Amazon usage', 'Amazon 사용량 새로고침').click(); await page.getByRole('alert').waitFor(); await page.getByText('92 / 100', { exact: true }).waitFor();
  await page.evaluate(() => { document.documentElement.style.fontSize = '200%'; });
  await page.screenshot({ path: `/tmp/vitlane-amazon-step1-${locale}.png`, fullPage: true });
  const oversized = await page.evaluate(() => [...document.querySelectorAll('main *')].filter(el => el.getBoundingClientRect().right > innerWidth + 2).slice(0,5).map(el => ({tag:el.tagName,cls:el.className,right:el.getBoundingClientRect().right})));
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth + 2), false, JSON.stringify(oversized));
  await button('View options for Wireless Headset', 'Wireless Headset 옵션 보기').click(); await page.locator('.candidate-detail').waitFor(); await button('Dislike','싫어요').click(); await page.waitForFunction(()=>[...document.querySelectorAll('.candidate-detail button')].some(b=>/Dislike|싫어요/.test(b.textContent)&&b.getAttribute('aria-pressed')==='true')); assert.equal(interactions['anchor-card::B987654321'].sentiment,'DISLIKE'); assert.equal(interactions['anchor-card::B012345678'].sentiment,'LIKE'); await page.keyboard.press('Escape'); await page.locator('.candidate-detail').waitFor({ state: 'hidden' });
  await page.screenshot({ path: `/tmp/vitlane-amazon-step1-${locale}.png`, fullPage: true }); assert.deepEqual(errors, []); await context.close();
 }
 console.log('PASS: common detail, explicit draft/save, shared pin/like/dislike, exact Variant reactions and reload, explicit options, stable anchor, exact ASIN, external actions, check/undo/reload, quota failure retention, en/ko, Firefox 320px/200%, Escape');
 } finally { await browser.close(); await fs.rm(entry, { force: true }); await fs.rm(entry.replace(".tsx", ".html"), {force:true}); }
}
run().catch(e => { console.error(e); process.exitCode = 1; });
