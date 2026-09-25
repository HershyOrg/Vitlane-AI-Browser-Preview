// Captures the curation grid after a purchase check and the account list row it
// produced, counting per-card state reads throughout.
const { firefox } = require('playwright');
const base = 'http://127.0.0.1:18082';
const id = process.env.CURATION_ID;
const out = process.env.OUT;
const stateRe = /\/catalog-research\/candidates\/[^/]+\/(external-product|amazon\/state)$/;
(async () => {
  const browser = await firefox.launch();
  const context = await browser.newContext({ locale: 'ko-KR', viewport: { width: 1280, height: 900 } });
  const page = await context.newPage();
  page.setDefaultTimeout(60000);
  let stateGETs = 0; const workspaceGETs = [];
  page.on('request', (r) => {
    const p = new URL(r.url()).pathname;
    if (r.method() === 'GET' && stateRe.test(p)) stateGETs++;
    if (r.method() === 'GET' && /\/workspace$/.test(p)) workspaceGETs.push(p);
  });
  if ((await context.request.post(base + '/api/v1/dev/auth/session', { data: { profileKey: 'multi-product' } })).status() !== 201) throw new Error('dev session');
  await page.goto(`${base}/curations/${id}`);
  await page.locator('.curation-candidate-card[data-source="ELEVENST"]').first().waitFor();
  await page.waitForTimeout(2500);
  await page.getByRole('button', { name: '구매 체크', exact: true }).first().click();
  await page.getByRole('alertdialog').getByRole('button', { name: '확인', exact: true }).click();
  await page.getByRole('button', { name: '체크 취소', exact: true }).first().waitFor();
  await page.waitForTimeout(2500);
  await page.screenshot({ path: `${out}/01-card-checked.png`, fullPage: false });
  const list = await (await context.request.get(base + '/api/v1/account/purchase-checks')).json();
  await page.goto(`${base}/account?view=purchased`);
  await page.waitForTimeout(2500);
  await page.screenshot({ path: `${out}/02-account-purchased.png`, fullPage: false });
  console.log(JSON.stringify({ stateGETs, workspaceReads: workspaceGETs.length, listRows: (list.purchaseChecks || []).length, firstRow: (list.purchaseChecks || [])[0] }, null, 1));
  await browser.close();
})().catch((e) => { console.error(e); process.exitCode = 1; });
