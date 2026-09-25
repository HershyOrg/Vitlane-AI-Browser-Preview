const { firefox } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const path = require('node:path');

// Actual Home form, providers and usePlanFlow; HTTP is a synthetic contract.
// No account, model or catalog provider is called.
async function run() {
 const { createServer } = await import('vite');
 const root = path.resolve(__dirname, '..');
 const vite = await createServer({ root, configFile:path.join(root,'vite.config.ts'), logLevel:'error', server:{host:'127.0.0.1',port:0,fs:{allow:[root,await fs.realpath(path.join(root,'node_modules'))]}} });
 await vite.listen();
 const browser = await firefox.launch({headless:true});
 try {
  for (const locale of ['ko-KR','en-US']) for (const width of [1280,320]) for (const zoom of [1,2]) {
   const context=await browser.newContext({viewport:{width,height:900}}), page=await context.newPage();
   const errors=[],writes=[];
   let prefs={schemaVersion:'vitlane.user-preferences.v1',version:0,uiLocale:locale,preferredCurrency:'KRW',researchCountry:'US'};
   let releasePatch, holdPatch=false, failPatch=false;
   page.on('pageerror',error=>errors.push(error.message));
   await context.route('**/api/v1/**',async route=>{
    const request=route.request(), pathname=new URL(request.url()).pathname, method=request.method();
    const body=method==='GET'?undefined:request.postDataJSON();
    if(body) writes.push({pathname,body});
    let json;
    if(pathname==='/api/v1/me') json={user:{id:'autosave-fixture',status:'ACTIVE',roles:['USER']}};
    else if(pathname==='/api/v1/me/preferences') {
     if(method==='PATCH') {
      assert.equal(body.expectedVersion,prefs.version);
      if(holdPatch) await new Promise(resolve=>{releasePatch=resolve;});
      if(failPatch) {failPatch=false; return route.fulfill({status:503,json:{error:{code:'UNAVAILABLE',message:'Unavailable'}}});}
      const {schemaVersion,expectedVersion,...patch}=body;
      prefs={...prefs,...patch,version:prefs.version+1};
     }
     json={preferences:prefs,effective:prefs};
    } else if(pathname==='/api/v1/managed-runner/capability') json={enabled:true,serverExhausted:false,defaultModelKey:'gpt-5.6-luna',models:[{key:'gpt-5.6-luna',label:'Luna'}]};
    else if(pathname==='/api/v1/curations'&&method==='GET') json={schemaVersion:'vitlane.curation-list.v2',curations:[]};
    else if(pathname==='/api/v1/shopping-plans') {
     json={plan:{id:'plan-fixture',location:body.location},curation:{id:'curation-fixture'}};
    } else throw new Error(`Unexpected API ${method} ${pathname}`);
    await route.fulfill({json});
   });
   const ko=locale==='ko-KR';
   const countrySettings=page.getByRole('button',{name:ko?'조사·보기 설정':'Research and display settings',exact:true});
   const budgetSettings=page.getByRole('button',{name:ko?'예산 설정':'Budget settings',exact:true});
   const display=page.getByLabel(ko?'표시 통화':'Display currency',{exact:true});
   const autoSwitch=page.getByRole('switch',{name:ko?'자동 큐레이션':'Automatic curation',exact:true});
   const amount=page.getByLabel(ko?'총 예산':'Total budget',{exact:true});
   const intent=page.locator('#curation-intent'),send=page.getByRole('button',{name:ko?'상품 찾기 시작':'Start product search',exact:true});
   const ready=()=>page.waitForFunction(()=>document.querySelector('#preferences')?.textContent.includes('"ready":true'));
   const close=async()=>{await page.keyboard.press('Escape');await page.locator('.init-settings').waitFor({state:'hidden'});};
   const geometry=async()=>{
    const box=await page.locator('.init-settings').boundingBox();
    assert(box.x>=-1 && box.x+box.width<=width+1,JSON.stringify({width,zoom,box}));
    assert.equal(await page.locator('.init-settings').getByRole('button',{name:/^(Save|Cancel|저장|취소)$/}).count(),0);
    assert(await page.evaluate(()=>document.documentElement.scrollWidth<=window.innerWidth+1));
   };
   await page.goto(`${vite.resolvedUrls.local[0]}e2e/fixtures/init-research-country.html`);await ready();
   await page.evaluate(zoom=>{document.documentElement.style.fontSize=`${16*zoom}px`;},zoom);
   await intent.fill('fountain pen');await countrySettings.click();await geometry();
   await page.locator('#shipping-country').selectOption('KR');assert.match(await countrySettings.innerText(),/KR · KRW/);
   await page.waitForFunction(()=>!document.querySelector('#shipping-country').disabled);assert.equal(prefs.researchCountry,'KR');await close();
   await budgetSettings.click();assert.equal(await autoSwitch.getAttribute('aria-checked'),'true');assert.equal(await amount.count(),0);await autoSwitch.click();await amount.fill('200000');await geometry();await close();
   assert.match(await budgetSettings.innerText(),/200,000/);
   await budgetSettings.click();await amount.fill('abc');await close();assert.equal(await send.isDisabled(),true);
   await page.locator('form').evaluate(form=>form.requestSubmit());assert.equal(writes.length,1);
   await budgetSettings.click();await amount.fill('200000');await close();
   await countrySettings.click();holdPatch=true;await display.selectOption('USD').catch(async error=>{console.error({locale,width,zoom,body:await page.locator('body').innerText()});throw error;});
   await page.waitForFunction(()=>document.querySelector('#curation-intent').disabled);
   await close();assert.equal(await send.isDisabled(),true);assert.equal(await intent.inputValue(),'fountain pen');
   while(!releasePatch) await new Promise(resolve=>setTimeout(resolve,10));
   holdPatch=false;releasePatch();
   await page.waitForFunction(()=>!document.querySelector('#curation-intent').disabled);
   assert.deepEqual(writes.map(w=>w.body),[{schemaVersion:'vitlane.user-preferences.v1',expectedVersion:0,researchCountry:'KR'},{schemaVersion:'vitlane.user-preferences.v1',expectedVersion:1,preferredCurrency:'USD'}]);
   assert.match(await countrySettings.innerText(),/KR · USD/);
   await page.reload();await ready();await page.evaluate(zoom=>{document.documentElement.style.fontSize=`${16*zoom}px`;},zoom);
   assert.match(await countrySettings.innerText(),/KR · USD/);
   await budgetSettings.click();assert.equal(await autoSwitch.getAttribute('aria-checked'),'true');assert.equal(await amount.count(),0);await autoSwitch.click();assert.equal(await amount.inputValue(),'');await close();

   if(width===1280 && zoom===1) {
    await countrySettings.click();failPatch=true;await display.selectOption('KRW');
    await page.getByRole('button',{name:ko?'다시 시도':'Retry',exact:true}).waitFor();await close();
    await intent.fill('fountain pen');assert.equal(await send.isDisabled(),true);
    await page.getByRole('button',{name:ko?'다시 시도':'Retry',exact:true}).click();
    await page.waitForFunction(()=>!document.querySelector('#curation-intent').disabled);
    assert.equal(await send.isDisabled(),false);assert.equal(prefs.preferredCurrency,'KRW');
   }
   await countrySettings.click();await page.locator('#shipping-country').selectOption('US');await page.waitForFunction(()=>!document.querySelector('#shipping-country').disabled);await close();
   await budgetSettings.click();await page.locator('#curation-currency').selectOption('USD');await amount.fill('100.25');await close();
   await intent.fill('fountain pen');await intent.press('Shift+Enter');
   assert.equal(writes.filter(w=>w.pathname==='/api/v1/shopping-plans').length,0);
   await intent.press('Enter');await page.waitForFunction(()=>document.querySelector('#plan-result').textContent.includes('plan-fixture'));
   const submitted=writes.filter(w=>w.pathname==='/api/v1/shopping-plans');assert.equal(submitted.length,1);
   assert.equal(submitted[0].body.controlMode,'MANUAL');assert.equal(submitted[0].body.location.country,'US');assert.equal(submitted[0].body.budget.totalAmount,'100.25');assert.equal(submitted[0].body.budget.currency,'USD');
   assert.equal(prefs.preferredCurrency,width===1280 && zoom===1?'KRW':'USD','budget submission must not overwrite display currency');
   assert.deepEqual(errors,[]);
   if(width===320 && zoom===2) await page.screenshot({path:`/tmp/vitlane-home-autosave-${locale}.png`,fullPage:true});
   console.log(`PASS ${locale} ${width}px ${zoom*100}%: immediate settings, delayed save, reload boundary and actual request`);
   await context.close();
  }
 } finally {await browser.close();await vite.close();}
}
run().catch(error=>{console.error(error);process.exitCode=1;});
