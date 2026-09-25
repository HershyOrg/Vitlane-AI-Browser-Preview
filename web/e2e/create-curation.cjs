const { firefox } = require('playwright');
const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:18082';
(async () => {
  const browser = await firefox.launch();
  const context = await browser.newContext({ locale: 'ko-KR', viewport: { width: 1440, height: 1000 } });
  const page = await context.newPage();
  page.setDefaultTimeout(30000);
  try {
    if ((await context.request.post(base + '/api/v1/dev/auth/session', { data: { profileKey: 'multi-product' } })).status() !== 201) throw new Error('dev session');
    await page.goto(base + '/');
    await page.locator('#curation-intent').fill('라미 사파리 만년필');
    await page.locator('.shell-intent-composer__submit').click();
    await page.waitForURL(/\/curations\//);
    const id = page.url().split('/').pop();
    let workspace;
    for (let i = 0; i < 80; i++) {
      workspace = await (await context.request.get(base + `/api/v1/curations/${id}/workspace`)).json();
      if (workspace.catalogResearch?.pools?.some((p) => p.products?.length)) break;
      await page.waitForTimeout(1500);
    }
    const pool = workspace.catalogResearch.pools.find((p) => p.products?.length);
    console.log(JSON.stringify({ id, targetId: pool.targetId, products: pool.products.map((p) => ({ candidateId: p.candidateId, source: p.source })) }));
  } finally {
    await browser.close();
  }
})().catch((e) => { console.error(e); process.exitCode = 1; });
