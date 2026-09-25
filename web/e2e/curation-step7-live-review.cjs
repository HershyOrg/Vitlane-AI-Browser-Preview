const {firefox}=require('playwright');const fs=require('node:fs/promises');const assert=require('node:assert/strict');
const base=process.env.E2E_BASE_URL||'http://127.0.0.1:18117',cid='e5700000-0000-4000-8000-000000000002',out='/tmp/vitlane-step7-evidence';
if(!['localhost','127.0.0.1'].includes(new URL(base).hostname))throw Error('Local review only');
(async()=>{const browser=await firefox.launch();const context=await browser.newContext({storageState:'/tmp/vitlane-step7-session.json',viewport:{width:1440,height:1000}});const page=await context.newPage();page.setDefaultTimeout(20000);const errors=[];page.on('pageerror',e=>errors.push(e.message));try{
await page.goto(base+'/');await page.locator('.curation-product-notice').first().waitFor();await page.screenshot({path:out+'/sidebar-new-product.png'});console.log('PASS sidebar notice');
const read=async()=>await(await context.request.get(base+'/api/v1/curations/'+cid+'/background-research')).json();
let view=await read();const finding=view.findings.find(f=>f.status==='NEW'&&f.product.productRef);assert(finding,'real original product expected');
await page.goto(base+'/curations/'+cid);await page.getByRole('button',{name:new RegExp('지켜보는 조건')}).waitFor();
await page.locator('.curation-background__controls button').first().click();
await page.locator(`[data-finding-id="${finding.id}"]`).click();
await page.getByRole('dialog').waitFor();await page.screenshot({path:out+'/finding-before-import-ko.png'});
const responsePromise=page.waitForResponse(r=>r.url().endsWith('/findings/'+finding.id+'/candidates'),{timeout:70000});
await page.getByRole('button',{name:'후보 추가',exact:true}).click();const response=await responsePromise;const result=await response.json();console.log('import',response.status(),result);assert.equal(response.status(),200);
await page.locator('.curation-background__products').waitFor();await page.keyboard.press('Escape');await page.getByRole('dialog').waitFor({state:'hidden'});view=await read();assert.equal(view.findings.find(f=>f.id===finding.id).status,'ADDED');
const replay=await context.request.post(base+'/api/v1/curations/'+cid+'/findings/'+finding.id+'/candidates',{data:{}});assert.equal(replay.status(),200);assert.equal((await replay.json()).candidateId,result.candidateId);
console.log('PASS real AI candidate assessment and replay');
const w=await(await context.request.get(base+'/api/v1/curations/'+cid+'/workspace')).json();await fs.writeFile(out+'/workspace-after-import.json',JSON.stringify(w,null,2));
for(const locale of ['ko-KR','en-US']){
 const pref=await(await context.request.get(base+'/api/v1/me/preferences')).json();
 assert.equal((await context.request.patch(base+'/api/v1/me/preferences',{data:{schemaVersion:'vitlane.user-preferences.v1',expectedVersion:pref.preferences.version,uiLocale:locale,preferredCurrency:'KRW',researchCountry:'KR'}})).status(),200);
 await page.setViewportSize({width:1440,height:1000});await page.goto(base+'/curations/'+cid);await page.locator('.curation-background__controls').waitFor();
 await page.screenshot({path:out+'/'+locale+'-desktop.png',fullPage:true});
 for(const scale of [1,2]){
  await page.setViewportSize({width:320,height:900});await page.evaluate(z=>{document.documentElement.style.fontSize=16*z+'px';window.dispatchEvent(new Event('resize'));},scale);
  await page.locator('.curation-background__controls button').first().click();await page.locator('.curation-background__product').first().click();await page.getByRole('dialog').waitFor();
  await page.screenshot({path:out+'/'+locale+'-320-'+scale+'x-finding.png',fullPage:true});
  const size=await page.evaluate(()=>{const d=document.querySelector('[role="dialog"]');return {page:document.documentElement.scrollWidth,viewport:innerWidth,dialog:d.getBoundingClientRect().toJSON(),scroll:d.scrollWidth}});
  assert(size.page<=321&&size.dialog.x>=-1&&size.dialog.right<=321&&size.scroll<=size.dialog.width+1,JSON.stringify(size));
  await page.keyboard.press('Tab');assert(await page.getByRole('dialog').evaluate(el=>el.contains(document.activeElement)));
  await page.keyboard.press('Escape');await page.getByRole('dialog').waitFor({state:'hidden'});
 }
 await page.evaluate(()=>document.documentElement.style.fontSize='16px');
}
assert.deepEqual(errors,[]);await fs.writeFile(out+'/live-review-result.json',JSON.stringify({curationId:cid,candidateId:result.candidateId,realFinding:finding.product.title,findings:view.findings.length,checks:['notice','entry','real_AI_import','replay','ko-KR','en-US','320px','200%','focus'],errors},null,2));
console.log('PASS live review, locales, mobile, zoom, focus');
}finally{await browser.close()}})().catch(e=>{console.error(e);process.exit(1)});
