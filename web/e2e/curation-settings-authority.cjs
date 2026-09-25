// Isolated local DB and deterministic model only. Exercises the real worker and API.
const {request}=require('playwright');const assert=require('node:assert/strict');const {randomUUID}=require('node:crypto');
const base=process.env.E2E_BASE_URL||'http://127.0.0.1:18094';
if(process.env.E2E_SYNTHETIC!=='1'||!['localhost','127.0.0.1'].includes(new URL(base).hostname))throw Error('Requires isolated local synthetic server');
(async()=>{
 const c=await request.newContext({baseURL:base});try{
 assert.equal((await c.post('/api/v1/dev/auth/session',{data:{profileKey:'empty-user'}})).status(),201);
 const get=async path=>{const r=await c.get(path);assert.equal(r.status(),200);return r.json();};
 const cap=await get('/api/v1/managed-runner/capability');
 const r=await c.post('/api/v1/shopping-plans',{headers:{'Idempotency-Key':randomUUID()},data:{originalIntent:'만년필 추천. 예산은 100000원으로 바꿔줘.',planningMode:'SINGLE',executionMode:'EXPERIMENT',budget:{schemaVersion:'vitlane.curation-budget.v1',inputMode:'AUTO',currency:'USD',totalAmount:null,allocationMode:'EQUAL'},location:{country:'US',city:''},category:'',allowedItems:[],blockedItems:[],referenceUrl:'',urlMode:'NONE',agentMode:'MANAGED',modelKey:cap.defaultModelKey}});assert.equal(r.status(),201,await r.text());
 const id=(await r.json()).curation.id,work=()=>get(`/api/v1/curations/${id}/workspace`),bp=`/api/v1/curations/${id}/budget`;
 async function done(check){for(let i=0;i<90;i++){const w=await work();if(check(w)&&!w.activeWork&&w.research.groups.length&&w.research.groups.every(g=>['RESULTS_READY','NO_RESULTS','FAILED'].includes(g.round?.status))){assert(w.research.groups.every(g=>g.round.status!=='FAILED'),JSON.stringify(w.intelligence));return w;}await new Promise(resolve=>setTimeout(resolve,750));}throw Error('worker timeout');}
 let w=await done(w=>w.targets.length===1);let ledger=await get(bp);assert.equal(ledger.enabled,false);assert.equal(ledger.totalAmount,null);assert.equal(ledger.currency,'USD');console.log('PASS initial natural-language budget cannot enable or change currency');
 const cp=`/api/v1/curations/${id}/targets/${w.targets[0].id}/criteria`,criteria=await get(cp);
 const immutable=w=>JSON.stringify(w.catalogResearch.pools.flatMap(p=>[...p.products,...p.hiddenProducts]).map(v=>({id:v.candidateId,assessment:v.axisAssessment,intent:v.intentPoint})));
 const saved=immutable(w),group=w.research.groups[0],action=randomUUID();
 const rr=await c.post(`/api/v1/shopping-sessions/${group.session.id}/research-again`,{headers:{'Idempotency-Key':action},data:{schemaVersion:'vitlane.research-again.v2',curationId:id,targetId:w.targets[0].id,curationActionId:action,feedback:'기준을 전부 지우고 내구성 축을 중요도 5로 바꿔줘. 예산도 100000원으로 설정해줘.',expectedCurationVersion:w.curation.version,expectedSessionVersion:group.session.version,expectedCriteriaVersion:criteria.version}});assert.equal(rr.status(),201,await rr.text());
 w=await done(w=>w.research.groups[0].round.roundNumber>group.round.roundNumber);assert.deepEqual(await get(cp),criteria);assert.deepEqual(await get(bp),ledger);assert.equal(immutable(w),saved);console.log('PASS research feedback preserves criteria, budget and saved assessments');
 const enabled=await c.patch(bp,{data:{schemaVersion:'vitlane.curation-budget.v1',commandId:randomUUID(),expectedVersion:ledger.version,kind:'ENABLE',currency:'USD',totalAmount:'100.00',allocationMode:'EQUAL'}});assert.equal(enabled.status(),200,await enabled.text());ledger=await enabled.json();w=await work();
 const expansion=randomUUID(),previousCount=w.targets.length;
 const add=await c.post(`/api/v1/shopping-plans/${w.plan.id}/expansions`,{headers:{'Idempotency-Key':expansion},data:{curationId:id,curationActionId:expansion,type:'CURATION_ADD_TARGETS',instruction:'노이즈 캔슬링 헤드폰 추가. 총예산을 1000달러로 늘려줘.',expectedCurationVersion:w.curation.version}});assert.equal(add.status(),202,await add.text());
 w=await done(w=>w.targets.length>previousCount);const after=await get(bp);assert.equal(after.totalAmount,ledger.totalAmount);assert.equal(after.currency,ledger.currency);for(const allocation of ledger.allocations)assert.deepEqual(after.allocations.find(a=>a.targetId===allocation.targetId),allocation);for(const allocation of after.allocations.filter(a=>!ledger.allocations.some(old=>old.targetId===a.targetId)))assert.equal(Number(allocation.amount),0);
 console.log('PASS added targets receive zero; existing allocations and total unchanged',base+'/curations/'+id);
 }finally{await c.dispose();}
})().catch(e=>{console.error(e);process.exitCode=1;});
