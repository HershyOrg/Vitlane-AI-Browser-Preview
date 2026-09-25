// Read-only local regression; empty TargetSet responses model the initial loading window.
const { firefox } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:18083';
const id = process.env.E2E_CURATION_ID;
const out = process.env.E2E_SCREENSHOT_DIR || '/tmp/vitlane-final-polish';
if (!id || !['127.0.0.1', 'localhost'].includes(new URL(base).hostname)) throw Error('Use a dedicated local synthetic Curation.');
(async () => {
 const browser = await firefox.launch();
 const context = await browser.newContext({ locale: 'ko-KR', viewport: { width: 1440, height: 1000 } });
 const page = await context.newPage(); const errors = []; page.on('pageerror', e => errors.push(e.message));
 try {
  await fs.mkdir(out, { recursive: true });
  assert.equal((await context.request.post(base + '/api/v1/dev/auth/session', { data: { profileKey: 'multi-product' } })).status(), 201);
  await page.goto(base + '/'); const title = page.locator('.init-request-form__title'); await title.waitFor();
  assert(['무엇을 찾고 있나요?', 'What are you looking for?'].includes(await title.innerText()));
  assert.equal(await title.evaluate(e => getComputedStyle(e).fontWeight), '400');
  await page.screenshot({ path: out + '/home.png' });
  for (const label of ['예산 설정','조사·보기 설정']) {
   await page.getByRole('button',{name:label,exact:true}).click();const popup=page.locator('.init-settings');await popup.waitFor();
   assert.equal(await page.locator('[data-slot="dialog-overlay"]').count(),0);
   const y=await popup.locator('.init-settings__row label').evaluateAll(rows=>rows.map(r=>r.getBoundingClientRect().y));assert.equal(y.length,2);assert(Math.abs(y[0]-y[1])<1);
   if(label==='예산 설정'){assert.equal(await popup.locator('select').count(),1);await popup.getByRole('button',{name:'제한 없음',exact:true}).waitFor();}
   await page.screenshot({path:out+(label==='예산 설정'?'/init-budget.png':'/init-region.png')});await page.keyboard.press('Escape');
  }

  const path = `/api/v1/curations/${id}/workspace`;
  const response = await context.request.get(base + path); assert.equal(response.status(), 200);
  const workspace = await response.json(); workspace.targets = []; workspace.research.groups = [];
  if (workspace.catalogResearch) workspace.catalogResearch.pools = [];
  await page.route(base + path, route => route.fulfill({ json: workspace }));
  await page.route(base + `/api/v1/curations/${id}/budget`, route => route.fulfill({ json: { schemaVersion: 'vitlane.curation-budget.v1', version: 0, researchVersion: 0, enabled: false, currency: 'KRW', totalAmount: null, allocations: [] } }));
  await page.goto(base + '/curations/' + id);
  const bar = page.locator('.curation-budget__bar'); await bar.waitFor();
  for (const width of [1440, 320]) {
   await page.setViewportSize({ width, height: 1000 }); await page.waitForTimeout(200);
   const geometry = await bar.evaluate(e => { const b=e.getBoundingClientRect(), s=e.querySelector('button').getBoundingClientRect(), t=e.querySelector('span').getBoundingClientRect(); return { bar:b.width,segment:s.width,text:t.width,centerDelta:Math.abs(t.x+t.width/2-b.x-b.width/2) }; });
   assert(geometry.segment >= geometry.bar - 3 && geometry.text > 20 && geometry.centerDelta < 2, JSON.stringify(geometry));
   await page.screenshot({ path: out + `/empty-bar-${width}.png` });
  }
  await page.unrouteAll(); await page.setViewportSize({ width:1440,height:1000 }); await page.reload();
  const targets = page.locator('.catalog-ui-curation__targets > .catalog-ui-target'); await targets.last().waitFor();
  assert(await targets.count() >= 2);
  assert.equal(await targets.first().evaluate(e=>getComputedStyle(e).borderBottomWidth),'1px');
  assert.equal(await targets.last().evaluate(e=>getComputedStyle(e).borderBottomWidth),'0px');
  assert.equal(await page.locator('.catalog-ui-composer-dock').evaluate(e=>getComputedStyle(e).borderTopWidth),'1px');
  await page.screenshot({ path:out+'/last-target.png' }); assert.deepEqual(errors,[]);
  await fs.writeFile(out+'/result.json',JSON.stringify({checks:['question mark and regular heading','anchored Init settings with fields on one row and explicit No limit','empty TargetSet bar fills width at desktop/mobile','only intermediate Target dividers','fixed composer top rule preserved'],errors},null,2));
  console.log('PASS heading, empty allocation bar, last Target divider; no server mutations');
 } finally { await browser.close(); }
})().catch(e=>{console.error(e);process.exitCode=1;});
