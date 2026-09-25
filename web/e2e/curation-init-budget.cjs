// Live-AI policy and compact composer regression on the synthetic review server.
const { firefox } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const { randomUUID } = require('node:crypto');
const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:18083';
const out = process.env.E2E_SCREENSHOT_DIR || '/tmp/vitlane-init-budget-review';
if (process.env.E2E_LIVE_AI !== '1' || !['localhost','127.0.0.1'].includes(new URL(base).hostname)) throw Error('Requires explicit live-AI opt-in and the dedicated localhost server.');
(async()=>{
 await fs.mkdir(out,{recursive:true});
 const browser=await firefox.launch();const c=await browser.newContext({viewport:{width:1440,height:1000}});const page=await c.newPage();page.setDefaultTimeout(20000);const errors=[];page.on('pageerror',e=>errors.push(e.message));
 try {
  assert.equal((await c.request.post(base+'/api/v1/dev/auth/session',{data:{profileKey:'empty-user'}})).status(),201);
  async function prefs(uiLocale){const p=await(await c.request.get(base+'/api/v1/me/preferences')).json();assert.equal((await c.request.patch(base+'/api/v1/me/preferences',{data:{schemaVersion:'vitlane.user-preferences.v1',expectedVersion:p.preferences.version,uiLocale,preferredCurrency:'USD',researchCountry:'US'}})).status(),200);}
  await prefs('ko-KR');
  async function ready(id){for(let i=0;i<110;i++){const w=await(await c.request.get(base+`/api/v1/curations/${id}/workspace`)).json();if(w.targets?.length&&w.curation.phase==='CURATING'&&w.research.groups.length===w.targets.length&&w.research.groups.every(g=>['RESULTS_READY','NO_RESULTS','FAILED'].includes(g.round?.status))&&!w.intelligence?.some(j=>['RUNNING','PENDING'].includes(j.status)))return w;if(w.intelligence?.some(j=>j.status==='FAILED'))throw Error(JSON.stringify(w.intelligence.map(j=>({status:j.status,failureCode:j.failureCode}))));await page.waitForTimeout(1000);}throw Error('AI planning did not settle: '+id);}
  async function budget(id){return (await c.request.get(base+`/api/v1/curations/${id}/budget`)).json();}
  const cap=await(await c.request.get(base+'/api/v1/managed-runner/capability')).json();assert(cap.enabled);
  const cases=[];
  async function create(name,text,setting){const r=await c.request.post(base+'/api/v1/shopping-plans',{headers:{'Idempotency-Key':randomUUID()},data:{originalIntent:text,planningMode:'AUTO',executionMode:'EXPERIMENT',budget:{schemaVersion:'vitlane.curation-budget.v1',currency:'USD',allocationMode:'AUTO',...setting},location:{country:'US',city:''},category:'',allowedItems:[],blockedItems:[],minPrice:null,maxPrice:null,referenceUrl:'',urlMode:'NONE',agentMode:'MANAGED',modelKey:'gpt-5.6-luna'}});assert.equal(r.status(),201,await r.text());const id=(await r.json()).curation.id;const w=await ready(id),b=await budget(id);cases.push({name,id,budget:b});console.log('AI case',name,id,b.enabled,b.totalAmount);return{id,w,b};}
  // A real home submission sends untouched AUTO; text supplies denomination and quantity.
  await page.goto(base+'/');await page.getByRole('button',{name:'예산 설정',exact:true}).waitFor();
  assert.equal(await page.locator('[aria-controls="shell-intent-settings"]').count(),0);assert.equal(await page.getByRole('button',{name:'단일 상품',exact:true}).count(),0);
  await page.screenshot({path:out+'/home-ko.png',fullPage:true});
  const submitted=page.waitForRequest(r=>r.url().endsWith('/api/v1/shopping-plans')&&r.method()==='POST');
  await page.locator('#curation-intent').fill('개당 5만원 이하 만년필 2개를 찾아줘');await page.getByRole('button',{name:'상품 찾기 시작',exact:true}).click();assert.equal((await submitted).postDataJSON().budget.inputMode,'AUTO');
  await page.waitForURL(/\/curations\//);const firstID=page.url().split('/').pop();let first=await ready(firstID);const inferred=await budget(firstID);assert.equal(inferred.totalAmount,'100000');assert.equal(inferred.currency,'KRW');assert.equal(inferred.allocations[0].quantity,2);cases.push({name:'home per-unit inference',id:firstID,budget:inferred});console.log('PASS home Init inference and quantity');
  await page.reload();await page.locator('.curation-budget__segment').first().waitFor();await page.waitForTimeout(700);
  const rows=await page.evaluate(()=>{const rect=s=>document.querySelector(s).getBoundingClientRect();return{budget:rect('.curation-budget__heading button').top,research:rect('.curation-composer-settings-trigger').top,bar:rect('.curation-budget__bar').top+rect('.curation-budget__bar').height/2,cart:rect('.catalog-ui-draft-trigger').top+rect('.catalog-ui-draft-trigger').height/2,plus:!!document.querySelector('.catalog-ui-focus-composer__plus')}});assert(Math.abs(rows.budget-rows.research)<3,JSON.stringify(rows));assert(Math.abs(rows.bar-rows.cart)<3,JSON.stringify(rows));assert.equal(rows.plus,false);await page.screenshot({path:out+'/workspace-ko.png',fullPage:true});
  // Curation budget-only natural language leaves the ledger, membership and jobs unchanged.
  first=await(await c.request.get(base+`/api/v1/curations/${firstID}/workspace`)).json();
  await page.getByLabel('조사 요청',{exact:true}).fill('만년필 예산을 100만원으로 올려줘');await page.getByRole('button',{name:'조사 요청 보내기',exact:true}).click();await page.getByText('예산은 입력창 위 예산 버튼에서 변경해 주세요.',{exact:true}).waitFor({timeout:60000});assert.deepEqual(await budget(firstID),inferred);const after=await(await c.request.get(base+`/api/v1/curations/${firstID}/workspace`)).json();assert.equal(after.curation.version,first.curation.version);assert.deepEqual(after.targets,first.targets);assert.equal(after.intelligence.length,first.intelligence.length);console.log('PASS curation budget-only request is ignored');
  const explicit=await create('explicit amount wins','총 10만원으로 만년필 2개 찾아줘',{inputMode:'EXPLICIT',totalAmount:'80.00'});assert.equal(explicit.b.totalAmount,'80.00');assert.equal(explicit.b.currency,'USD');assert.equal(explicit.b.allocations[0].quantity,2);
  const none=await create('explicit no limit wins','총 10만원 안에서 만년필 찾아줘',{inputMode:'EXPLICIT',totalAmount:null});assert.equal(none.b.enabled,false);
  const observation=await create('observed price is not a budget','정가가 10만원인 만년필을 찾아줘',{inputMode:'AUTO',totalAmount:null});assert.equal(observation.b.enabled,false);
  for(const locale of ['ko-KR','en-US']){
   await prefs(locale);await page.goto(base+'/');await page.getByRole('button',{name:locale==='ko-KR'?'예산 설정':'Budget settings',exact:true}).waitFor();await page.setViewportSize({width:320,height:900});await page.evaluate(()=>document.documentElement.style.fontSize='32px');await page.waitForTimeout(600);await page.screenshot({path:out+`/home-${locale}-320-200.png`,fullPage:true});
   for(const label of locale==='ko-KR'?['예산 설정','조사·보기 설정']:['Budget settings','Research and display settings']){await page.getByRole('button',{name:label,exact:true}).click();await page.getByRole('dialog').waitFor();await page.waitForTimeout(200);await page.screenshot({path:out+`/${locale}-${label.startsWith('예산')||label.startsWith('Budget')?'budget':'research'}-320-200.png`,fullPage:true});const fit=await page.getByRole('dialog').evaluate(d=>({left:d.getBoundingClientRect().left,right:d.getBoundingClientRect().right,scroll:d.scrollWidth,width:d.clientWidth,page:document.documentElement.scrollWidth,viewport:innerWidth}));assert(fit.left>=0&&fit.right<=321&&fit.scroll<=fit.width+1&&fit.page<=fit.viewport+1,JSON.stringify(fit));await page.keyboard.press('Escape');}
   await page.evaluate(()=>document.documentElement.style.fontSize='16px');await page.setViewportSize({width:1440,height:1000});
  }
  await prefs('ko-KR');assert.deepEqual(errors,[]);await fs.writeFile(out+'/result.json',JSON.stringify({cases,rows,errors},null,2));console.log('PASS Init precedence, curation guard, compact layout, ko/en 320px 200%');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
