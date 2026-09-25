// Dedicated synthetic DB, actual worker/OpenAI and Firefox. No merchant purchases.
const { firefox } = require('playwright');
const fs = require('node:fs/promises');
const assert = require('node:assert/strict');
const { randomUUID } = require('node:crypto');
const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:18086';
const out = process.env.E2E_SCREENSHOT_DIR || '/tmp/vitlane-step4-review';
if (process.env.E2E_LIVE_AI !== '1' || !['localhost','127.0.0.1'].includes(new URL(base).hostname)) throw Error('Requires E2E_LIVE_AI=1 and a dedicated local synthetic review server.');
(async () => {
  const browser = await firefox.launch(); const c = await browser.newContext({ viewport: {width:1440,height:1000} }); const page = await c.newPage(); const errors = [];
  page.on('pageerror', e => errors.push(e.message)); page.setDefaultTimeout(20000);
  try {
    await fs.mkdir(out,{recursive:true});
    assert.equal((await c.request.post(base+'/api/v1/dev/auth/session',{data:{profileKey:'empty-user'}})).status(),201);
    async function prefs(locale) { const p=await (await c.request.get(base+'/api/v1/me/preferences')).json(); const r=await c.request.patch(base+'/api/v1/me/preferences',{data:{schemaVersion:'vitlane.user-preferences.v1',expectedVersion:p.preferences.version,uiLocale:locale,preferredCurrency:'USD',researchCountry:'US'}}); assert.equal(r.status(),200); }
    await prefs('ko-KR');
    let id = process.env.E2E_REUSE_ID;
    if (!id) {
      const cap=await(await c.request.get(base+'/api/v1/managed-runner/capability')).json(); assert(cap.enabled);
      const r=await c.request.post(base+'/api/v1/shopping-plans',{headers:{'Idempotency-Key':randomUUID()},data:{originalIntent:'클래식 디자인과 부드러운 필기감이 좋은 만년필',planningMode:'SINGLE',executionMode:'EXPERIMENT',budget:{schemaVersion:'vitlane.curation-budget.v1',currency:'USD',totalAmount:null,allocationMode:'EQUAL'},location:{country:'US',city:''},category:'',allowedItems:[],blockedItems:[],referenceUrl:'',urlMode:'NONE',agentMode:'MANAGED',modelKey:cap.defaultModelKey}});
      const body=await r.json(); assert.equal(r.status(),201,JSON.stringify(body)); id=body.curation.id;
      await fs.writeFile(out+'/curation-id',id);
      // Change presentation while work is queued/running: generation must keep Korean.
      await prefs('en-US');
    }
    const work=async()=>await(await c.request.get(base+`/api/v1/curations/${id}/workspace`)).json();
    async function done(minRound=1, readWork=work) { let w; for(let i=0;i<120;i++){ w=await readWork(); const round=w.research?.groups?.[0]?.round; if(i%10===0)console.log('progress',w.targets?.length,round?.status,w.intelligence?.map(j=>[j.status,j.failureCode])); if(w.targets?.length && !w.activeWork && round?.roundNumber>=minRound && ['RESULTS_READY','NO_RESULTS','FAILED'].includes(round.status))return w; await page.waitForTimeout(1500); } await fs.writeFile(out+'/timeout-workspace.json',JSON.stringify(w,null,2)); throw Error('research timed out'); }
    let w=await done(); await fs.writeFile(out+'/initial-workspace.json',JSON.stringify(w,null,2));
    const target=w.targets[0].id, cp=base+`/api/v1/curations/${id}/targets/${target}/criteria`;
    const readCriteria=async()=>await(await c.request.get(cp)).json(); let criteria=await readCriteria();
    const all=w.catalogResearch?.pools?.flatMap(p=>[...p.products,...p.hiddenProducts]) || [];
    assert(['RESULTS_READY','NO_RESULTS'].includes(w.research.groups[0].round.status),JSON.stringify(w.intelligence)); assert(all.length>0 && all.length<=16); assert.equal(w.catalogResearch.schemaVersion,'vitlane.catalog-research-workspace.v4');
    for(const p of all){const a=p.axisAssessment;assert(a);assert.equal(a.contentLocale,'ko-KR');assert.equal(a.weights.reduce((x,y)=>x+y,0),100);assert.equal(a.scores.length,a.criteria.axes.length);assert.equal(a.totalScore,Math.round(a.scores.reduce((v,s,i)=>v+s.scorePercent*a.weights[i],0)/100));assert(/[가-힣]/.test(p.intentPoint));}
    console.log('PASS initial assessment and request locale',all.length,criteria.axes.map(a=>a.label));
    const frozen=JSON.stringify(all.map(p=>({id:p.candidateId,assessment:p.axisAssessment,intent:p.intentPoint,features:p.features,specifications:p.specifications})));
    const body={schemaVersion:'vitlane.criteria-command.v1',expectedCriteriaVersion:criteria.version,expectedCurationVersion:w.curation.version,idempotencyKey:randomUUID(),criteria:{...criteria,axes:[...criteria.axes.filter(a=>a.label!=='휴대성'),{axisId:'e2e-portability',label:'휴대성',definition:'휴대하기 쉬운 크기와 무게',importance:3,usesPrice:false,usesVisualEvidence:false,origin:'USER_EDIT'}]}};
    let r=await c.request.put(cp,{data:body}); assert.equal(r.status(),200,await r.text()); const saved=await r.json();
    r=await c.request.put(cp,{data:body});assert.equal(r.status(),200);assert.deepEqual(await r.json(),saved);
    r=await c.request.put(cp,{data:{...body,idempotencyKey:randomUUID()}});assert.equal(r.status(),409);
    await prefs('en-US'); await page.goto(base+'/curations/'+id);await page.getByLabel('Sort',{exact:true}).waitFor();
    assert.equal(await page.getByRole('button',{name:'Find more candidates',exact:true}).count(),0);
    await page.getByRole('button',{name:'Sort',exact:true}).click(); await page.getByRole('menuitemradio',{name:'휴대성',exact:true}).click(); assert(all.every(p=>!p.axisAssessment.scores.some(s=>s.axisId===saved.axes.at(-1).axisId)));
    assert.equal((await c.request.post(base+`/api/v1/curations/${id}/targets/${target}/catalog-research/expansions`,{data:{}})).status(),410);
    console.log('PASS criteria CAS/replay, missing axis and Expand removal');
    w=await work();const group=w.research.groups[0],action=randomUUID(); const roundNumber=group.round.roundNumber;
    r=await c.request.post(base+`/api/v1/shopping-sessions/${group.session.id}/research-again`,{headers:{'Idempotency-Key':action},data:{schemaVersion:'vitlane.research-again.v2',curationId:id,targetId:target,curationActionId:action,feedback:'',expectedCurationVersion:w.curation.version,expectedSessionVersion:group.session.version,expectedCriteriaVersion:saved.version}});
    assert.equal(r.status(),201,await r.text());
    w=await done(roundNumber+1);await fs.writeFile(out+'/after-research.json',JSON.stringify(w,null,2));assert.notEqual(w.research.groups[0].round.status,'FAILED',JSON.stringify(w.intelligence));
    const after=w.catalogResearch.pools.flatMap(p=>[...p.products,...p.hiddenProducts]);const ids=new Set(all.map(p=>p.candidateId));
    assert.equal(JSON.stringify(after.filter(p=>ids.has(p.candidateId)).map(p=>({id:p.candidateId,assessment:p.axisAssessment,intent:p.intentPoint,features:p.features,specifications:p.specifications}))),frozen);
    for(const p of after.filter(p=>!ids.has(p.candidateId)))assert.equal(p.axisAssessment.contentLocale,'en-US');
    console.log('PASS APPEND immutability and new locale',after.length,w.catalogResearch.pools[0].discoveryOutcome);
    // The next no-feedback run reuses query seeds; duplicate-only discovery skips evaluation.
    const cachedGroup=w.research.groups[0], cachedAction=randomUUID(), cachedCriteria=await readCriteria();
    r=await c.request.post(base+`/api/v1/shopping-sessions/${cachedGroup.session.id}/research-again`,{headers:{'Idempotency-Key':cachedAction},data:{schemaVersion:'vitlane.research-again.v2',curationId:id,targetId:target,curationActionId:cachedAction,feedback:'',expectedCurationVersion:w.curation.version,expectedSessionVersion:cachedGroup.session.version,expectedCriteriaVersion:cachedCriteria.version}});assert.equal(r.status(),201,await r.text());
    w=await done(cachedGroup.round.roundNumber+1); assert.notEqual(w.research.groups[0].round.status,'FAILED'); await fs.writeFile(out+'/cached-research.json',JSON.stringify(w,null,2));
    console.log('PASS cached no-feedback research',w.catalogResearch.pools[0].discoveryOutcome);

    // Feedback refines discovery; current settings and persisted assessments stay frozen.
    const beforeFeedback=await readCriteria(), feedbackGroup=w.research.groups[0], feedbackAction=randomUUID();
    r=await c.request.post(base+`/api/v1/shopping-sessions/${feedbackGroup.session.id}/research-again`,{headers:{'Idempotency-Key':feedbackAction},data:{schemaVersion:'vitlane.research-again.v2',curationId:id,targetId:target,curationActionId:feedbackAction,feedback:'기존 축은 이름과 정의까지 그대로 유지하고 내구성(Durability) 축을 중요도 4로 추가해 주세요.',expectedCurationVersion:w.curation.version,expectedSessionVersion:feedbackGroup.session.version,expectedCriteriaVersion:beforeFeedback.version}});assert.equal(r.status(),201,await r.text());
    w=await done(feedbackGroup.round.roundNumber+1);assert.notEqual(w.research.groups[0].round.status,'FAILED',JSON.stringify(w.intelligence));
    const patched=await readCriteria(); assert.deepEqual(patched,beforeFeedback);
    const feedbackProducts=w.catalogResearch.pools.flatMap(p=>[...p.products,...p.hiddenProducts]);
    assert.equal(JSON.stringify(feedbackProducts.filter(p=>ids.has(p.candidateId)).map(p=>({id:p.candidateId,assessment:p.axisAssessment,intent:p.intentPoint,features:p.features,specifications:p.specifications}))),frozen);
    await fs.writeFile(out+'/feedback-criteria.json',JSON.stringify(patched,null,2));console.log('PASS feedback preserves criteria and frozen old assessments');

    for(const locale of ['ko-KR','en-US']) { await prefs(locale); await page.goto(base+'/curations/'+id);await page.getByLabel(locale==='ko-KR'?'정렬':'Sort',{exact:true}).waitFor();await page.waitForTimeout(500);await page.setViewportSize({width:1440,height:1000});await page.locator('.catalog-ui-target__identity').first().evaluate(e=>e.scrollIntoView({block:'start'}));await page.waitForTimeout(300);
      const corners=await page.locator('.curation-candidate-card').first().evaluate(card=>{const c=card.getBoundingClientRect(),a=card.querySelector('.vt-candidate-card__icon-actions').getBoundingClientRect();return {top:a.top-c.top,right:c.right-a.right}});assert(corners.top<20&&corners.right<20,JSON.stringify(corners));
      await page.screenshot({path:out+'/'+locale+'-desktop.png',fullPage:true});
      for(const zoom of [1,2]) { await page.setViewportSize({width:320,height:900});await page.evaluate(z=>document.documentElement.style.fontSize=`${16*z}px`,zoom);await page.getByRole('button',{name:locale==='ko-KR'?'비교 기준 추가':'Add criterion',exact:true}).click();await page.waitForTimeout(200);const size=await page.evaluate(()=>({width:document.documentElement.scrollWidth,viewport:innerWidth}));assert(size.width<=size.viewport+1,JSON.stringify(size));await page.screenshot({path:out+'/'+locale+'-320-'+zoom+'x.png',fullPage:true});await page.locator('.research-criteria__editor').getByRole('button',{name:locale==='ko-KR'?'취소':'Cancel',exact:true}).click(); }
      await page.evaluate(()=>document.documentElement.style.fontSize='16px'); }
    // A fresh English request must actually produce English candidates, not merely
    // pass a vacuous check when rediscovery returns no new products.
    await prefs('en-US');const englishCap=await(await c.request.get(base+'/api/v1/managed-runner/capability')).json();
    r=await c.request.post(base+'/api/v1/shopping-plans',{headers:{'Idempotency-Key':randomUUID()},data:{originalIntent:'클래식 디자인과 부드러운 필기감이 좋은 만년필',planningMode:'SINGLE',executionMode:'EXPERIMENT',budget:{schemaVersion:'vitlane.curation-budget.v1',currency:'USD',totalAmount:null,allocationMode:'EQUAL'},location:{country:'US',city:''},category:'',allowedItems:[],blockedItems:[],referenceUrl:'',urlMode:'NONE',agentMode:'MANAGED',modelKey:englishCap.defaultModelKey}});
    assert.equal(r.status(),201,await r.text());const englishId=(await r.json()).curation.id;
    const english=await done(1,async()=>await(await c.request.get(base+`/api/v1/curations/${englishId}/workspace`)).json());
    assert.equal(english.research.groups[0].round.status,'RESULTS_READY',JSON.stringify(english.intelligence));
    const englishProducts=english.catalogResearch.pools.flatMap(p=>p.products);assert(englishProducts.length>0);
    for(const p of englishProducts){assert.equal(p.axisAssessment.contentLocale,'en-US');assert(!/[가-힣]/.test(p.intentPoint));assert(p.axisAssessment.scores.every(s=>!/[가-힣]/.test(s.explanation)));}
    await fs.writeFile(out+'/english-workspace.json',JSON.stringify(english,null,2));console.log('PASS English generation with Korean user input',englishProducts.length);
    assert.deepEqual(errors,[]); await fs.writeFile(out+'/result.json',JSON.stringify({id,url:base+'/curations/'+id,initialCount:all.length,finalCount:after.length,errors,checks:['live_ai','weights_100','locale_snapshot','criteria_CAS_replay','missing_axis','expand_410','append_immutable','ko_en_320_200','feedback_preserves_settings','english_new_candidates','corner_reactions']},null,2));console.log('PASS',base+'/curations/'+id);
  } catch(e) { await page.screenshot({path:out+"/failure.png",fullPage:true}); await fs.writeFile(out+"/failure.json",JSON.stringify({url:page.url(),body:await page.locator("body").innerText(),errors},null,2));throw e; } finally {await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
