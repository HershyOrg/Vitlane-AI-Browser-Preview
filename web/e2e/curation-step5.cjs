// Synthetic local data only. Exercises real HTTP, PostgreSQL, worker and Firefox.
// Seeded messages test capability lifetime; they are not model-quality evidence.
const {firefox}=require('playwright');
const {randomUUID}=require('node:crypto');
const {execFileSync}=require('node:child_process');
const fs=require('node:fs/promises');
const assert=require('node:assert/strict');
const base=process.env.E2E_BASE_URL||'http://127.0.0.1:18115';
const database=process.env.E2E_DATABASE||'vitlane_curation_step5_finalreview';
const out=process.env.E2E_SCREENSHOT_DIR||'/tmp/vitlane-step5-e2e';
if(!['127.0.0.1','localhost'].includes(new URL(base).hostname)||!/^vitlane_curation_step5_[a-z]+$/.test(database))throw Error('Requires isolated local Step 5 fixture database');
function sql(query){return execFileSync('docker',['exec','-i','vitlane-curation-step4-review-pg','psql','-U','vitlane','-d',database,'-At','-v','ON_ERROR_STOP=1'],{input:query,encoding:'utf8'}).trim();}
(async()=>{
 const browser=await firefox.launch();const ctx=await browser.newContext({viewport:{width:1440,height:1000}});const page=await ctx.newPage();const errors=[];page.on('pageerror',e=>errors.push(e.message));page.setDefaultTimeout(15000);
 try{
  await fs.mkdir(out,{recursive:true});assert.equal((await ctx.request.post(base+'/api/v1/dev/auth/session',{data:{profileKey:'empty-user'}})).status(),201);
  async function locale(value){const r=await ctx.request.get(base+'/api/v1/me/preferences');const p=(await r.json()).preferences;assert.equal((await ctx.request.patch(base+'/api/v1/me/preferences',{data:{schemaVersion:'vitlane.user-preferences.v1',expectedVersion:p.version,uiLocale:value,preferredCurrency:'USD',researchCountry:'US'}})).status(),200);}
  await locale('ko-KR');const cap=await(await ctx.request.get(base+'/api/v1/managed-runner/capability')).json();
  const start=await ctx.request.post(base+'/api/v1/shopping-plans',{headers:{'Idempotency-Key':randomUUID()},data:{originalIntent:'A fountain pen for daily writing, portability and a smooth nib',planningMode:'SINGLE',executionMode:'EXPERIMENT',budget:{schemaVersion:'vitlane.curation-budget.v1',currency:'USD',totalAmount:null,allocationMode:'EQUAL'},location:{country:'US',city:''},category:'',allowedItems:[],blockedItems:[],referenceUrl:'',urlMode:'NONE',agentMode:'MANAGED',modelKey:cap.defaultModelKey}});
  const created=await start.json();assert.equal(start.status(),201,JSON.stringify(created));const id=created.curation.id;await fs.writeFile(out+'/curation-id',id);
  const read=async()=>{const r=await ctx.request.get(base+`/api/v1/curations/${id}/workspace`);assert.equal(r.status(),200,await r.text());return r.json();};
  async function done(){let w;for(let n=0;n<150;n++){w=await read();if(!w.activeWork&&!w.conversation?.unfinished&&w.targets.length)return w;if(n%20===0)console.log('waiting',w.intelligence?.map(j=>j.status),w.conversation?.unfinished);await page.waitForTimeout(500);}await fs.writeFile(out+'/timeout.json',JSON.stringify(w,null,2));throw Error('work timeout');}
  let w=await done();assert(w.conversation);assert(w.conversation.messages.some(m=>m.kind==='RESULT'));const target=w.targets[0].id;
  const initialProducts=w.catalogResearch.pools.flatMap(p=>[...p.products,...p.hiddenProducts]);const frozen=initialProducts.map(p=>({id:p.candidateId,assessment:p.axisAssessment}));assert(initialProducts.length>0);
  const post=async(data)=>ctx.request.post(base+`/api/v1/curations/${id}/conversation-requests`,{headers:{'Idempotency-Key':data.clientRequestId},data});
  async function noAction(){w=await done();const before=w.intelligence.length;const response=await post({schemaVersion:'vitlane.curation-conversation-request.v1',mode:'AUTO',request:'고마워',clientRequestId:randomUUID(),expectedCurationVersion:w.curation.version,expectedConversationVersion:w.conversation.version});assert.equal(response.status(),202,await response.text());assert.equal((await response.json()).status,'NO_ACTION');w=await done();assert.equal(w.intelligence.length,before);return w;}
  async function seed(){await noAction();const rid=w.conversation.requests.at(-1).id;const mid=randomUUID(),eid=randomUUID();const group=w.research.groups.find(g=>g.session.planTargetId===target);const criteria=await(await ctx.request.get(base+`/api/v1/curations/${id}/targets/${target}/criteria`)).json();const uid=w.curation.userId;
   const payload=JSON.stringify({kind:'RESEARCH_AGAIN',targetId:target,sessionId:group.session.id,sessionVersion:group.session.version,criteriaVersion:criteria.version,feedback:''});
   sql(`INSERT INTO curation_follow_ups(id,user_id,curation_id,response_id,kind,status,content,payload,fingerprint) VALUES('${mid}','${uid}','${id}','${rid}','PROPOSAL','PENDING','{"code":"LOW_AXIS_FIT","body":"현재 기준으로 더 적합한 후보를 찾아볼까요?","locale":"ko-KR"}','${payload}',curation_follow_up_context('${id}','${target}')); INSERT INTO curation_follow_ups(id,user_id,curation_id,response_id,kind,status,content) VALUES('${eid}','${uid}','${id}','${rid}','ERROR','PENDING','{"code":"RESEARCH_FAILED","targetTitle":"만년필"}');`);
   w=await read();return {proposal:w.conversation.messages.find(m=>m.id===mid),error:w.conversation.messages.find(m=>m.id===eid)};
  }
  const respond=async(message,response,key=randomUUID(),extra={})=>ctx.request.post(base+`/api/v1/curations/${id}/follow-ups/${message.id}/responses`,{headers:{'Idempotency-Key':key},data:{response,expectedVersion:message.version,clientRequestId:key,...extra}});
  let seeded=await seed();await page.goto(base+'/curations/'+id);await page.locator(`[data-message-id="${seeded.proposal.id}"]`).waitFor();
  const mid=seeded.proposal.id;const article=page.locator(`[data-message-id="${mid}"]`);assert.equal(await article.getByRole('button').count(),2);
  const resultMessage=w.conversation.messages.find(m=>m.kind==='RESULT'&&m.content.code==='RESEARCH_COMPLETED'&&m.content.added>0);assert(resultMessage);
  const resultBubble=page.locator(`[data-message-id="${resultMessage.id}"]`);
  assert.equal(await resultBubble.locator('.curation-conversation-bubble.is-vitlane header strong').innerText(),'Vitlane');
  assert.equal(await resultBubble.locator('p').innerText(),`현재 기준으로 새 후보 ${resultMessage.content.added}개를 찾았어요.`);
  assert.equal(await resultBubble.getByRole('button').count(),0);
  assert(await resultBubble.evaluate(e=>Boolean(e.compareDocumentPosition(document.querySelector('[data-live-artifact]'))&Node.DOCUMENT_POSITION_FOLLOWING)));
  assert.equal(await article.locator('.curation-conversation-bubble.is-vitlane header strong').innerText(),'Vitlane');
  assert.equal(await article.locator('time').getAttribute('datetime'),seeded.proposal.createdAt);
  assert.equal(await article.locator('.curation-conversation-bubble button').count(),2);
  assert.equal(await page.locator('.catalog-ui-target__research-again').count(),0);assert.equal(await page.locator('.catalog-ui-target__identity .catalog-ui-target__collapse').count(),w.targets.length);
  await page.locator('.catalog-ui-target__collapse').first().click();await page.getByRole('button',{name:'조사 방식: Auto. 다른 방식 선택',exact:true}).click();assert.equal(await page.getByRole('option').filter({hasText:'재조사 ·'}).count(),0);await page.getByRole('option').filter({has:page.locator('strong',{hasText:/^재조사$/})}).click();await page.getByRole('option').filter({hasText:'재조사 ·'}).first().click();
  assert.equal((await read()).conversation.messages.find(m=>m.id===mid).status,'PENDING');await page.screenshot({path:out+'/ko-desktop-pending.png',fullPage:true});
  let acceptedBody;page.on('request',r=>{if(r.url().includes(`/follow-ups/${mid}/responses`))acceptedBody=r.postDataJSON();});
  const accepted=page.waitForResponse(r=>r.url().includes(`/follow-ups/${mid}/responses`));await article.getByRole('button',{name:'수락',exact:true}).click();const acceptance=await accepted;assert.equal(acceptance.status(),200,await acceptance.text());w=await done();assert.equal(w.conversation.messages.find(m=>m.id===mid).status,'ACCEPTED');assert.equal(w.conversation.messages.find(m=>m.id===seeded.error.id).status,'SUPERSEDED');
  assert.equal((await respond(seeded.proposal,'ACCEPT')).status(),409);
  assert.equal((await respond(seeded.proposal,'ACCEPT',acceptedBody.clientRequestId)).status(),200);
  assert.equal((await respond(seeded.proposal,'ACCEPT',randomUUID(),{targetId:'override'})).status(),400);
  const after=w.catalogResearch.pools.flatMap(p=>[...p.products,...p.hiddenProducts]);for(const p of frozen)assert.deepEqual(after.find(x=>x.candidateId===p.id)?.axisAssessment,p.assessment);console.log('PASS exact button acceptance, replay, all pending superseded, candidate preservation');
  seeded=await seed();assert.equal((await respond(seeded.proposal,'DISMISS')).status(),200);w=await read();assert.equal(w.conversation.messages.find(m=>m.id===seeded.error.id).status,'PENDING');assert.equal((await respond(seeded.error,'ACKNOWLEDGE')).status(),200);console.log('PASS dismiss and acknowledge are local to one message');
  seeded=await seed();const criteriaURL=base+`/api/v1/curations/${id}/targets/${target}/criteria`;const criteria=await(await ctx.request.get(criteriaURL)).json();const changed=structuredClone(criteria);changed.axes[0].importance=changed.axes[0].importance===5?4:5;
  const patch=await ctx.request.put(criteriaURL,{data:{schemaVersion:'vitlane.criteria-command.v1',expectedCriteriaVersion:criteria.version,expectedCurationVersion:w.curation.version,idempotencyKey:randomUUID(),criteria:changed}});assert.equal(patch.status(),200,await patch.text());assert.equal((await respond(seeded.proposal,'ACCEPT')).status(),409);console.log('PASS related criteria invalidates old proposal');
  seeded=await seed();w=await read();const manual={schemaVersion:'vitlane.curation-conversation-request.v1',mode:'RESEARCH_AGAIN',request:'',targetId:target,expectedCurationVersion:w.curation.version,expectedConversationVersion:w.conversation.version,clientRequestId:randomUUID()};const race=await Promise.all([respond(seeded.proposal,'ACCEPT'),post(manual)]);assert.equal(race.filter(r=>r.status()===200||r.status()===202).length,1,race.map(r=>r.status()).join(','));assert.equal(race.filter(r=>r.status()===409).length,1);await done();console.log('PASS acceptance versus new request admits one action');
  seeded=await seed();
  for(const language of ['ko-KR','en-US']) for(const theme of ['light','dark']) {
   await locale(language);await page.goto(base+'/curations/'+id);
   await page.locator(`[data-message-id="${seeded.proposal.id}"]`).waitFor();
   for(const zoom of [1,2]) {
    await page.setViewportSize({width:320,height:900});
    await page.evaluate(({zoom,theme})=>{document.documentElement.style.fontSize=`${16*zoom}px`;document.documentElement.dataset.theme=theme;document.documentElement.classList.toggle('dark',theme==='dark');},{zoom,theme});
    const actions=page.locator(`[data-message-id="${seeded.proposal.id}"] .curation-follow-up__actions`);
    await actions.evaluate(n=>n.scrollIntoView({block:"end"}));await page.waitForTimeout(200);
    const layout=await actions.evaluate(el=>{
     const dock=document.querySelector('.catalog-ui-composer-dock');const style=getComputedStyle(dock);
     const bubble=el.closest('.curation-conversation-bubble');const messageStyle=getComputedStyle(bubble);
     return {width:document.documentElement.scrollWidth,viewport:innerWidth,blur:style.backdropFilter,
      background:style.backgroundColor,dockTop:dock.getBoundingClientRect().top,
      bubble:{...bubble.getBoundingClientRect().toJSON(),border:messageStyle.borderTopWidth,radius:messageStyle.borderTopLeftRadius,background:messageStyle.backgroundColor,overflow:bubble.scrollWidth-bubble.clientWidth},
      buttons:[...el.querySelectorAll('button')].map(b=>({...b.getBoundingClientRect().toJSON(),disabled:b.disabled}))};
    });
    // The composer is the canvas colour of the theme behind one line, never glass over the conversation (ADR-0086).
    assert(layout.width<=layout.viewport+1,JSON.stringify(layout));assert.equal(layout.blur,'none');
    assert(!/rgba\([^)]*,\s*0?\.\d+\)|\/\s*0?\.\d+\s*\)/.test(layout.background),`the composer must be opaque: ${layout.background}`);
    assert(layout.buttons.every(b=>!b.disabled&&b.top>=0&&b.bottom<=layout.dockTop+1&&b.left>=0&&b.right<=layout.viewport+1),JSON.stringify(layout));
    assert(parseFloat(layout.bubble.border)>0&&parseFloat(layout.bubble.radius)>0&&layout.bubble.overflow<=1,JSON.stringify(layout));
    assert(layout.buttons.every(b=>b.top>=layout.bubble.top&&b.bottom<=layout.bubble.bottom&&b.height>=24*zoom&&b.height<=28*zoom),JSON.stringify(layout));
    assert.notEqual(layout.bubble.background,await resultBubble.locator('.curation-conversation-bubble').evaluate(e=>getComputedStyle(e).backgroundColor));
    assert.equal(await resultBubble.locator('p').innerText(),language==='ko-KR'?`현재 기준으로 새 후보 ${resultMessage.content.added}개를 찾았어요.`:`I found ${resultMessage.content.added} new candidates using the current criteria.`);
    await page.screenshot({path:out+`/${language}-${theme}-320-${zoom}x.png`,fullPage:true});
   }
  }
  console.log('PASS opaque composer and reachable proposal buttons in both locales/themes at 320px and 200%');
  await page.reload();await page.locator(`[data-message-id="${seeded.proposal.id}"]`).getByRole('button',{name:'Accept',exact:true}).waitFor();assert.equal(await page.locator(`[data-message-id="${seeded.proposal.id}"]`).getByRole('button',{name:'Accept',exact:true}).count(),1);assert.equal((await read()).conversation.messages.find(m=>m.id===seeded.proposal.id).content.body,seeded.proposal.content.body);assert.deepEqual(errors,[]);
  if(process.env.E2E_VERIFY_RETRY==='true'){
   // Deliberate failure injection on this test's own terminal Job, never production data.
   await noAction();const failed=w.intelligence.find(j=>j.targetKind==='RESEARCH_ROUND');assert(failed);
   sql(`UPDATE intelligence_jobs SET status='FAILED',failure_code='PROVIDER_UNAVAILABLE',retryable=true,attempt_count=1 WHERE id='${failed.jobId}';`);
   const errorId=randomUUID(),responseId=w.conversation.requests.at(-1).id;
   sql(`INSERT INTO curation_follow_ups(id,user_id,curation_id,response_id,kind,status,content) VALUES('${errorId}','${w.curation.userId}','${id}','${responseId}','ERROR','PENDING','{"code":"RESEARCH_FAILED","jobId":"${failed.jobId}","retryable":true}');`);
   await page.goto(base+'/curations/'+id);await page.locator(`[data-message-id="${errorId}"]`).getByRole('button',{name:'OK',exact:true}).click();
   await page.getByRole('button',{name:'Research mode: Auto. Choose another mode',exact:true}).click();
   await page.getByRole('option').filter({hasText:'Retry failed research'}).click();
   await page.getByRole('option').filter({hasText:'Research request'}).first().click();
   const retried=page.waitForResponse(r=>r.url().endsWith('/conversation-requests'));
   await page.locator('.catalog-ui-focus-composer__send').click();
   const response=await retried;assert.equal(response.status(),202,await response.text());
   const replayBody=JSON.parse(response.request().postData());assert.equal((await post(replayBody)).status(),202);
   w=await done();assert.equal(w.conversation.messages.find(m=>m.id===errorId).status,'ACKNOWLEDGED');
   assert.equal(w.intelligence.filter(j=>j.jobId===failed.jobId).length,1);assert.equal(w.conversation.requests.filter(r=>r.mode==='RETRY').length,1);
   console.log('PASS acknowledged failure remains retryable from composer, replay keeps one Job');
  }
  await fs.writeFile(out+'/result.json',JSON.stringify({curationId:id,tests:'PASS',source:'synthetic message fixtures, real server/DB/worker, fixture catalog; see launcher for AI mode',browser:'Firefox'},null,2));console.log('PASS reload, Korean/English, 320px and 200% text');
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
