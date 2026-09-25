const {firefox}=require('playwright');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const path=require('node:path');
async function run(){
 const {createServer}=await import('vite');
 const vite=await createServer({root:path.resolve(__dirname,'..'),configFile:path.resolve(__dirname,'../vite.config.ts'),logLevel:'error',server:{host:'127.0.0.1',port:0}});
 await vite.listen();const base=vite.resolvedUrls.local[0];
 const browser=await firefox.launch();const errors=[];const evidence='/tmp/api-progress-ui';await fs.mkdir(evidence,{recursive:true});
 try{for(const locale of ['ko-KR','en-US'])for(const theme of ['dark','light']){
  const page=await browser.newPage({locale:'ko-KR',viewport:{width:1440,height:1024},reducedMotion:'no-preference'});page.on('pageerror',e=>errors.push(e.message));
  await page.goto(base+'e2e/fixtures/api-progress-review.html');await page.getByRole('button',{name:locale==='ko-KR'?'KO':'EN',exact:true}).click();
  await page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme);
  assert.equal(await page.locator('.catalog-api-group').count(),4);assert.equal(await page.getByRole('switch').count(),6);
  const row=page.locator('.catalog-api-row').filter({has:page.getByText('Real-Time Web Search',{exact:true})});
  await row.locator('[aria-expanded]').click();assert(await row.locator('.catalog-api-row__details').isVisible());
  const toggle=row.getByRole('switch');assert.equal(await toggle.getAttribute('aria-checked'),'false');await toggle.click();assert.equal(await toggle.getAttribute('aria-checked'),'true');
  await row.getByRole('button',{name:locale==='ko-KR'?'사용량 새로고침':'Refresh usage',exact:true}).click();
  const writes=await page.evaluate(()=>window.reviewWrites);assert.equal(writes.length,1);assert.equal(writes[0].body.expectedVersion,1);
  assert.equal(writes[0].body.schemaVersion,'vitlane.catalog-api-control.v1');
  await page.screenshot({path:`${evidence}/operator-${locale}-${theme}.png`,fullPage:true});
  for(const scale of [1,2]){await page.setViewportSize({width:320,height:900});await page.evaluate(scale=>document.documentElement.style.fontSize=`${16*scale}px`,scale);assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));await page.screenshot({path:`${evidence}/operator-${locale}-${theme}-320-${scale}.png`,fullPage:true});}
  await page.goto(base+'e2e/fixtures/api-progress-review.html?mode=progress');await page.setViewportSize({width:1440,height:1024});await page.getByRole('button',{name:locale==='ko-KR'?'KO':'EN',exact:true}).click();
  await page.evaluate(theme=>document.documentElement.classList.toggle('dark',theme==='dark'),theme);
  const spinner=page.locator('.curation-step-spinner');await spinner.waitFor();assert.equal(await spinner.count(),1);
  const before=await spinner.evaluate(n=>getComputedStyle(n).transform);await page.waitForTimeout(150);assert.notEqual(await spinner.evaluate(n=>getComputedStyle(n).transform),before);
  assert.equal(await page.locator('.curation-working-bar, .curation-running-summary, .curation-running-details').count(),0);
  await page.screenshot({path:`${evidence}/progress-${locale}-${theme}.png`});
  for(const scale of [1,2]){await page.setViewportSize({width:320,height:900});await page.evaluate(scale=>document.documentElement.style.fontSize=`${16*scale}px`,scale);await page.waitForTimeout(180);assert(await page.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1),JSON.stringify(await page.evaluate(()=>[...document.querySelectorAll('body *')].filter(e=>e.getBoundingClientRect().right>innerWidth+1).map(e=>({tag:e.tagName,cls:e.className,text:e.textContent?.slice(0,70)})))));assert(await page.locator('.curation-cancel-action').isVisible());}
  await page.emulateMedia({reducedMotion:'reduce'});assert.equal(await spinner.evaluate(n=>getComputedStyle(n).animationName),'none');
  await page.locator('.curation-cancel-action').click();assert.equal(await spinner.count(),0);
  await page.close();
 }
 assert.deepEqual(errors,[]);console.log('PASS API grouping, toggle/CAS, refresh, inline steps, rotation/reduced motion, cancel, EN/KO light/dark 320px/200%; fixture only');
 }finally{await browser.close();await vite.close();}
}run().catch(e=>{console.error(e);process.exitCode=1});
