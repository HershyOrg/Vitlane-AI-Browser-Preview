// Real components, synthetic HTTP responses; no provider or production writes.
const { firefox } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');
(async () => {
 const { createServer } = await import('vite');
 const root = path.resolve(__dirname, '..');
 const vite = await createServer({ root, configFile: path.join(root,'vite.config.ts'), logLevel:'error', server:{host:'127.0.0.1',port:0,fs:{allow:[root,await fs.realpath(path.join(root,'node_modules'))]}} });
 await vite.listen(); const browser = await firefox.launch();
 const out = process.env.E2E_SCREENSHOT_DIR || '/tmp/vitlane-curation-readability'; const results=[];
 try {
  await fs.mkdir(out,{recursive:true});
  for (const locale of ['ko-KR','en-US']) for (const theme of ['light','dark']) for (const size of [{width:1440,zoom:1},{width:320,zoom:1},{width:320,zoom:2}]) {
   const context=await browser.newContext({viewport:{width:size.width,height:900}});
   await context.addInitScript(({locale,theme})=>{document.cookie=`vt_locale_choice=${locale}; Path=/`;localStorage.setItem('vitlane.locale.v2',locale);localStorage.setItem('vitlane.appearance.v2',JSON.stringify({theme,accent:'neutral'}));},{locale,theme});
   const page=await context.newPage(); const errors=[]; page.on('pageerror',e=>errors.push(e.message));
   await page.route('**/api/**',route=>{
    if (!new URL(route.request().url()).pathname.startsWith('/api/')) return route.continue();
    assert(new URL(route.request().url()).pathname.endsWith('/budget'), route.request().url());
    return route.fulfill({json:{schemaVersion:'vitlane.curation-budget.v1',version:1,researchVersion:1,enabled:true,currency:'KRW',totalAmount:'200000',allocations:[{targetId:'target',quantity:1,amount:'200000'}]}});
   });
   await page.goto(vite.resolvedUrls.local[0]+'e2e/fixtures/curation-readability.html');
   await page.locator('.budget-delta__amount').waitFor();
   await page.evaluate(zoom=>document.documentElement.style.fontSize=`${16*zoom}px`,size.zoom);
   await page.evaluate(()=>document.fonts.ready);
   const category=page.locator('.catalog-ui-target__vertical');
   assert.equal(await category.textContent(),locale==='ko-KR'?'전자제품':'Electronics');
   assert.equal(await category.count(),1);
   const categoryBox=await category.boundingBox(); assert(categoryBox.x>=0&&categoryBox.x+categoryBox.width<=size.width+1);
   const snapshot=await page.evaluate(()=>{
    const css=e=>({font:parseFloat(getComputedStyle(e).fontSize),background:getComputedStyle(e).backgroundColor,border:getComputedStyle(e).borderTopWidth,borderColor:getComputedStyle(e).borderTopColor});
    const amount=document.querySelector('.budget-delta__amount'); const r=document.createRange(); r.selectNodeContents(amount);
    const badge=document.querySelector('.candidate-ranking-badge');
    const price=document.querySelector('.vt-candidate-card__price-row');
    return {pageWidth:document.documentElement.scrollWidth,overflow:[...document.querySelectorAll('main *')].filter(e=>e.getBoundingClientRect().right>innerWidth+1).map(e=>({class:e.className,width:e.getBoundingClientRect().width})),amount:{...css(amount),text:amount.textContent,lines:r.getClientRects().length,whiteSpace:getComputedStyle(amount).whiteSpace,right:amount.getBoundingClientRect().right},priceRight:price.getBoundingClientRect().right,
     fonts:[...document.querySelectorAll('.research-axis-trigger[data-importance],.research-sort__trigger,.candidate-comparison__axis')].map(e=>css(e).font),
     pick:{background:getComputedStyle(badge,'::before').backgroundColor,blur:getComputedStyle(badge,'::before').backdropFilter,radius:getComputedStyle(badge).borderRadius},
     actions:[...document.querySelectorAll('.curation-follow-up__actions button,.curation-cancel-action')].map(css)};
   });
   assert.equal(snapshot.amount.lines,1,JSON.stringify(snapshot));
   assert.equal(snapshot.amount.whiteSpace,'nowrap'); assert.match(snapshot.amount.text,/−.*102,000/);
   assert(snapshot.amount.right<=snapshot.priceRight+1,JSON.stringify(snapshot));
   assert.equal(snapshot.fonts.length,3); assert(snapshot.fonts.every(n=>n>=16*size.zoom));
   assert(snapshot.actions.every(s=>s.font>=14*size.zoom && (s.background!=='rgba(0, 0, 0, 0)' || (parseFloat(s.border)>0&&s.borderColor!=='rgba(0, 0, 0, 0)'))),JSON.stringify(snapshot.actions));
   assert.match(snapshot.pick.blur,/blur/); assert.notEqual(snapshot.pick.background,'rgba(0, 0, 0, 0)'); assert.notEqual(snapshot.pick.radius,'0px');
   assert(snapshot.pageWidth<=size.width+1,JSON.stringify(snapshot)); assert.deepEqual(errors,[]);
   await page.screenshot({path:`${out}/${locale}-${theme}-${size.width}-${size.zoom}x.png`,fullPage:true});
   results.push({locale,theme,...size,...snapshot}); await context.close();
  }
  await fs.writeFile(out+'/result.json',JSON.stringify({scope:'Local Firefox only',results},null,2)); console.log('PASS readability: 12 locale/theme/viewport cases');
 } finally {await browser.close();await vite.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
