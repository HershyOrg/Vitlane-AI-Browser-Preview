// Dedicated local DB; live AI, synthetic merchant products. No purchases.
const {firefox}=require('playwright');
const assert=require('node:assert/strict');
const fs=require('node:fs/promises');
const {randomUUID}=require('node:crypto');
const base=process.env.E2E_BASE_URL||'http://127.0.0.1:18087';
const out=process.env.E2E_SCREENSHOT_DIR||'/tmp/vitlane-auto-threads';
if(process.env.E2E_LIVE_AI!=='1'||!['localhost','127.0.0.1'].includes(new URL(base).hostname))throw Error('Requires explicit local live AI test configuration');
(async()=>{
 const browser=await firefox.launch();const context=await browser.newContext({viewport:{width:1440,height:1000}});const page=await context.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));
 await fs.mkdir(out,{recursive:true});
 try{
  const api=async(method,path,data,expected=200,headers={})=>{const r=await context.request[method](base+path,{data,headers});assert.equal(r.status(),expected,await r.text());return await r.json()};
  await api('post','/api/v1/dev/auth/session',{profileKey:'empty-user'},201);
  const prefs=async(locale)=>{let p=await api('get','/api/v1/me/preferences');await api('patch','/api/v1/me/preferences',{schemaVersion:'vitlane.user-preferences.v1',expectedVersion:p.preferences.version,uiLocale:locale,preferredCurrency:'USD',researchCountry:'US'})};
  await prefs('ko-KR');
  const cap=await api('get','/api/v1/managed-runner/capability');assert(cap.enabled);
  let id=process.env.E2E_REUSE_ID;
  if(!id){const created=await api('post','/api/v1/shopping-plans',{originalIntent:'클래식 디자인과 필기감이 좋은 만년필, 20만원 언더',controlMode:'AUTO',planningMode:'AUTO',executionMode:'EXPERIMENT',budget:{schemaVersion:'vitlane.curation-budget.v1',inputMode:'EXPLICIT',currency:'USD',totalAmount:null,allocationMode:'EQUAL'},location:{country:'US',city:''},category:'',allowedItems:[],blockedItems:[],referenceUrl:'',urlMode:'NONE',agentMode:'MANAGED',modelKey:cap.defaultModelKey},201,{'Idempotency-Key':randomUUID()});id=created.curation.id;await fs.writeFile(out+'/curation-id.txt',id);}
  const root=`/api/v1/curations/${id}`;
  const threads=async()=> (await api('get',root+'/threads')).threads;
  const wait=async(threadID)=>{for(let n=0;n<180;n++){const all=await threads();const t=threadID?all.find(t=>t.id===threadID):all[0];if(t&&['SUCCEEDED','FAILED','CANCELLED','WAITING_SELECTION'].includes(t.status)){await fs.writeFile(out+'/last-thread.json',JSON.stringify(t,null,2));return t;}await page.waitForTimeout(1000);}throw Error('thread timeout')};
  const initial=await wait();assert.equal(initial.status,'SUCCEEDED',JSON.stringify(initial));
  let budget=await api('get',root+'/budget');assert.equal(budget.currency,'KRW');assert.equal(budget.totalAmount,'200000');assert(initial.decisions.some(d=>d.kind==='BUDGET'&&['ENABLE','SET_TOTAL'].includes(d.result)));assert(initial.steps.some(s=>s.effects.some(e=>e.kind==='BUDGET_CHANGED')));
  assert.equal((await api('get',root+'/research-settings')).country,'US');console.log('PASS initial cap, one thread, decisions, result receipts, manual country');
  const submit=async(text)=>{const w=await api('get',root+'/workspace'),key=randomUUID();return api('post',root+'/threads',{clientRequestId:key,request:text,expectedCurationVersion:w.curation.version},202,{'Idempotency-Key':key})};
  const change=await submit('예산을 15만원 이하로 바꿔줘. 조사는 하지 마.');
  // The accepted request immediately excludes conflicting mutations.
  let mode=await api('get',root+'/control-mode');const blocked=await context.request.put(base+root+'/control-mode',{data:{mode:'MANUAL',version:mode.version}});assert.equal(blocked.status(),409);
  const changed=await wait(change.id);assert.equal(changed.status,'SUCCEEDED',JSON.stringify(changed));assert.equal((await api('get',root+'/budget')).totalAmount,'150000');assert(changed.steps.every(s=>s.kind==='BUDGET'));const conversation=(await api('get',root+'/workspace')).conversation;if(conversation){assert.equal(conversation.requests.filter(r=>r.id===changed.id).length,1);assert.equal(conversation.messages.filter(m=>m.responseId===changed.id).length,0);}console.log('PASS natural budget command and immediate mutation lock');
  const repeat=await submit('Try again');const repeated=await wait(repeat.id);assert.equal(repeated.status,'SUCCEEDED',JSON.stringify(repeated));assert(repeated.decisions.some(d=>d.kind==='TARGET'&&d.source==='DETERMINISTIC'));assert.equal((await api('get',root+'/budget')).totalAmount,'150000');console.log('PASS sole-target deterministic repeat, preserved budget');
  const cancelled=await submit('예산 10만원 이하로 다시 찾아줘');await api('post',root+`/threads/${cancelled.id}/cancel`,{});assert.equal((await wait(cancelled.id)).status,'CANCELLED');assert.equal((await api('get',root+'/budget')).totalAmount,'150000');console.log('PASS interpretation cancellation');
  for(const locale of ['ko-KR','en-US']){await prefs(locale);await page.goto(base+'/curations/'+id);await page.getByRole('textbox',{name:locale==='ko-KR'?'조사 요청':'Research request',exact:true}).waitFor();await page.waitForTimeout(1500);await page.setViewportSize({width:1440,height:1000});await page.screenshot({path:out+'/'+locale+'-desktop.png',fullPage:true});
   for(const zoom of [1,2]){await page.setViewportSize({width:320,height:900});await page.evaluate(z=>document.documentElement.style.fontSize=`${16*z}px`,zoom);await page.waitForTimeout(200);const size=await page.evaluate(()=>({w:document.documentElement.scrollWidth,v:innerWidth}));assert(size.w<=size.v+1,JSON.stringify(size));await page.screenshot({path:out+'/'+locale+'-320-'+zoom+'x.png',fullPage:true});}
   await page.evaluate(()=>document.documentElement.style.fontSize='16px');}
  assert.deepEqual(errors,[]);await fs.writeFile(out+'/result.json',JSON.stringify({id,checks:['initial cap','budget override','country preserved','immediate thread lock','deterministic repeat','cancel','ko-en','320px','200%'],errors},null,2));console.log('PASS',id);
 }catch(e){await fs.writeFile(out+'/failure.json',JSON.stringify({url:page.url(),body:await page.locator('body').innerText(),errors},null,2));await page.screenshot({path:out+'/failure.png',fullPage:true});throw e;}finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1});
