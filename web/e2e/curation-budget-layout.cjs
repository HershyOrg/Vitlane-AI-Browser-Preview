// Read-only budget projection checks on existing synthetic review Curations.
const { firefox }=require('playwright');const fs=require('node:fs/promises');const assert=require('node:assert/strict');
const base=process.env.E2E_BASE_URL||'http://127.0.0.1:18083';const out=process.env.E2E_SCREENSHOT_DIR||'/tmp/vitlane-budget-layout';
const enabledID=process.env.E2E_BUDGET_ID,noneID=process.env.E2E_NO_BUDGET_ID;
if(!enabledID||!noneID||!['localhost','127.0.0.1'].includes(new URL(base).hostname))throw Error('Supply existing synthetic enabled/disabled Curation IDs on localhost.');
(async()=>{await fs.mkdir(out,{recursive:true});const browser=await firefox.launch();const c=await browser.newContext({viewport:{width:1440,height:1000}});const page=await c.newPage();page.setDefaultTimeout(15000);const errors=[];page.on('pageerror',e=>errors.push(e.message));try{
assert.equal((await c.request.post(base+'/api/v1/dev/auth/session',{data:{profileKey:'empty-user'}})).status(),201);
async function prefs(locale){const p=await(await c.request.get(base+'/api/v1/me/preferences')).json();assert.equal((await c.request.patch(base+'/api/v1/me/preferences',{data:{schemaVersion:'vitlane.user-preferences.v1',expectedVersion:p.preferences.version,uiLocale:locale,preferredCurrency:'USD'}})).status(),200);}
async function budget(id){const r=await c.request.get(base+`/api/v1/curations/${id}/budget`);assert.equal(r.status(),200);return r.json();}
const original={ [enabledID]:await budget(enabledID),[noneID]:await budget(noneID) };assert(original[enabledID].enabled);assert(!original[noneID].enabled);
for(const locale of ['ko-KR','en-US']){
 await prefs(locale);await page.setViewportSize({width:1440,height:1000});await page.goto(base+'/');await page.locator('#curation-intent').waitFor();await page.waitForTimeout(400);
 assert.equal(await page.locator('.vt-page-header h1').textContent(),locale==='ko-KR'?'어떤 상품을 찾고 있나요?':'What product are you looking for?');assert.equal(await page.locator('.vt-page-header__eyebrow, .vt-page-header__summary').count(),0);
 assert.equal(await page.locator('.vt-page-header').evaluate(el=>getComputedStyle(el).borderBottomWidth),'0px');
 assert.equal(await page.locator('.shell-intent-assurance').count(),0);const home=await page.locator('.init-composer').boundingBox();assert(home.y+home.height<950,JSON.stringify(home));await page.screenshot({path:out+`/home-${locale}.png`});
 for(const [id,label] of [[enabledID,'allocated'],[noneID,'unlimited']]){
  await page.goto(base+'/curations/'+id);await page.locator('.curation-budget__segment:not([disabled])').first().waitFor();await page.waitForTimeout(400);
  const text=await page.locator('.curation-budget__segment').allTextContents();assert(text.every(t=>label==='unlimited'?t.trim()===(locale==='ko-KR'?'제한없음':'No limit'):/\$[\d,]+/.test(t)),JSON.stringify(text));
  const style=await page.evaluate(()=>{const b=document.querySelector('.curation-budget__bar'),cs=getComputedStyle(b),c=getComputedStyle(document.querySelector('.catalog-ui-focus-composer__context'));return{height:b.getBoundingClientRect().height,border:cs.borderTopWidth,radius:cs.borderRadius,background:cs.backgroundColor,contextBorder:c.borderBottomWidth,contextBackground:c.backgroundColor};});assert(style.height<=26,JSON.stringify(style));assert.equal(style.border,'1px');assert(parseFloat(style.radius)>=100);assert.equal(style.background,'rgba(0, 0, 0, 0)');assert.equal(style.contextBorder,'0px');assert.equal(style.contextBackground,'rgba(0, 0, 0, 0)');
  await page.screenshot({path:out+`/${locale}-${label}-desktop.png`});
  for(const zoom of [1,2]){
   await page.setViewportSize({width:320,height:900});await page.evaluate(z=>{document.documentElement.style.fontSize=`${16*z}px`;window.dispatchEvent(new Event('resize'));},zoom);await page.waitForTimeout(650);
   const fit=await page.evaluate(()=>{const b=document.querySelector('.curation-budget__bar');return{page:document.documentElement.scrollWidth,viewport:innerWidth,scroll:b.scrollWidth,width:b.clientWidth};});assert(fit.page<=fit.viewport+1&&fit.scroll<=fit.width+1,JSON.stringify(fit));
   const token=await page.locator('.catalog-ui-focus-token').boundingBox(),send=await page.locator('.catalog-ui-focus-composer__send').boundingBox();assert(token.x+token.width<=send.x,JSON.stringify({token,send}));
   await page.screenshot({path:out+`/${locale}-${label}-320-${zoom}.png`});
   for(let i=0;i<await page.locator('.curation-budget__segment').count();i++){
    await page.locator('.curation-budget__segment').nth(i).click();await page.getByRole('dialog').waitFor();await page.waitForTimeout(180);await page.keyboard.press('Escape');await page.getByRole('dialog').waitFor({state:'hidden'});await page.waitForTimeout(180);
   }
  }
  assert.deepEqual(await budget(id),original[id]);await page.evaluate(()=>document.documentElement.style.fontSize='16px');await page.setViewportSize({width:1440,height:1000});
 }
 await page.goto(base+'/');await page.locator('#curation-intent').waitFor();await page.setViewportSize({width:320,height:900});await page.evaluate(()=>document.documentElement.style.fontSize='32px');await page.waitForTimeout(650);
 const heading=await page.locator('.vt-page-header h1').boundingBox(),header=await page.locator('.vt-page-header').boundingBox();assert(heading.y>=header.y&&heading.y+heading.height<=header.y+header.height,JSON.stringify({heading,header}));await page.screenshot({path:out+`/home-${locale}-320-200.png`});
 const button=page.getByRole('button',{name:locale==='ko-KR'?'예산 설정':'Budget settings',exact:true});await button.click();await page.getByRole('dialog').waitFor();await page.screenshot({path:out+`/init-budget-${locale}-320-200.png`});await page.keyboard.press('Escape');
 await page.evaluate(()=>document.documentElement.style.fontSize='16px');
}
await prefs('ko-KR');assert.deepEqual(errors,[]);await fs.writeFile(out+'/result.json',JSON.stringify({enabledID,noneID,checks:['amount labels','unlimited value','thin filled pill','margin separation','home offset','ko/en','320px 200%','all segments clickable','projection leaves ledger unchanged'],errors},null,2));console.log('PASS budget pill, composer spacing, home offset, ko/en mobile and segment clicks');
}finally{await browser.close();}})().catch(e=>{console.error(e);process.exitCode=1;});
