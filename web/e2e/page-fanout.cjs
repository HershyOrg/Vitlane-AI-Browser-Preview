// Counts per-card state GETs a real curation page sends on load, after one purchase check,
// after one Korean product reaction and after the undo. Local server speaks HTTP/1.1.
const { firefox } = require('playwright');
const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:18082';
const id = process.env.CURATION_ID;
const stateRe = /\/catalog-research\/candidates\/[^/]+\/(external-product|amazon\/state)$/;
(async () => {
  const browser = await firefox.launch();
  const context = await browser.newContext({ locale: 'ko-KR', viewport: { width: 1440, height: 1000 } });
  const page = await context.newPage();
  page.setDefaultTimeout(60000);
  const errors = [];
  page.on('pageerror', (e) => errors.push(e.message));
  if ((await context.request.post(base + '/api/v1/dev/auth/session', { data: { profileKey: 'multi-product' } })).status() !== 201) throw new Error('dev session');
  let events = []; let inflight = 0; let lastActivity = Date.now();
  const starts = new Map();
  page.on('request', (r) => { if (r.method() === 'GET' && stateRe.test(new URL(r.url()).pathname)) { starts.set(r, Date.now()); inflight++; lastActivity = Date.now(); } });
  const done = (failed) => (r) => { if (!starts.has(r)) return; events.push({ start: starts.get(r), end: Date.now(), failed }); starts.delete(r); inflight--; lastActivity = Date.now(); };
  page.on('requestfinished', done(false));
  page.on('requestfailed', done(true));
  const mark = () => { lastActivity = Date.now(); return Date.now(); };
  const settle = async (quiet = 2000) => { while (inflight > 0 || Date.now() - lastActivity < quiet) await page.waitForTimeout(100); };
  const pct = (xs, p) => xs.length ? xs[Math.min(xs.length - 1, Math.floor(xs.length * p))] : 0;
  const result = {};
  const record = (phase, t0) => {
    const d = events.map((e) => e.end - e.start).sort((a, b) => a - b);
    result[phase] = { stateGETs: events.length, failed: events.filter((e) => e.failed).length, settleMs: events.length ? Math.max(...events.map((e) => e.end)) - t0 : 0, p50Ms: pct(d, 0.5), p95Ms: pct(d, 0.95) };
    events = [];
  };
  try {
    const cardSel = '.curation-candidate-card[data-source="ELEVENST"], .curation-candidate-card[data-source="COUPANG"], .curation-candidate-card[data-source="AMAZON"]';
    const t0 = Date.now();
    await page.goto(`${base}/curations/${id}`);
    await page.locator(cardSel).first().waitFor();
    await settle();
    result.externalCards = await page.locator(cardSel).count();
    record('load', t0);

    const t1 = mark();
    await page.getByRole('button', { name: '구매 체크', exact: true }).first().click();
    await page.getByRole('alertdialog').getByRole('button', { name: '확인', exact: true }).click();
    await page.getByRole('button', { name: '체크 취소', exact: true }).first().waitFor();
    await settle();
    record('purchaseCheck', t1);

    await page.locator('.curation-candidate-card[data-source="ELEVENST"] .vt-candidate-card__hit-area').first().click();
    const like = page.locator('[data-reaction="like"]');
    await like.waitFor();
    const t2 = mark();
    await like.click();
    await page.locator('[data-reaction="like"][aria-pressed="true"]').waitFor();
    await settle();
    record('koreanReaction', t2);
    // restore the reaction so later runs start from the same state
    mark(); await like.click(); await page.locator('[data-reaction="like"][aria-pressed="false"]').waitFor(); await settle(); events = [];
    await page.keyboard.press('Escape');

    const t3 = mark();
    await page.getByRole('button', { name: '체크 취소', exact: true }).first().click();
    await page.getByRole('button', { name: '구매 체크', exact: true }).first().waitFor();
    await settle();
    record('undo', t3);
    result.pageErrors = errors.length;
    console.log(JSON.stringify(result));
  } finally {
    await browser.close();
  }
})().catch((e) => { console.error(e); process.exitCode = 1; });
