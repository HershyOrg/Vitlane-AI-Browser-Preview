// Opt-in live smoke: existing server credentials stay on the server.
// LIVE_RESPONSE_PRICES=1 E2E_BASE_URL=http://127.0.0.1:18100 node e2e/curation-response-prices-live.cjs
const { firefox } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const { randomUUID } = require('node:crypto');
const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:18100';
const out = process.env.E2E_SCREENSHOT_DIR || '/tmp/vitlane-response-prices-live';
if (process.env.LIVE_RESPONSE_PRICES !== '1' || !['127.0.0.1', 'localhost'].includes(new URL(base).hostname)) throw Error('Explicit opt-in and local review server required');
(async () => {
  const browser = await firefox.launch();
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const page = await context.newPage();
    const errors = []; page.on('pageerror', e => errors.push(e.message));
    const api = async (method, path, data, expected = 200, headers = {}) => {
      const r = await context.request[method](base + path, {data, headers});
      assert.equal(r.status(), expected, await r.text()); return r.json();
    };
    await fs.mkdir(out, {recursive: true});
    await api('post', '/api/v1/dev/auth/session', {profileKey: 'empty-user'}, 201);
    const prefs = await api('get', '/api/v1/me/preferences');
    await api('patch', '/api/v1/me/preferences', {schemaVersion:'vitlane.user-preferences.v1', expectedVersion:prefs.preferences.version, uiLocale:'ko-KR', preferredCurrency:'USD', researchCountry:'US'});
    const cap = await api('get', '/api/v1/managed-runner/capability');
    const created = process.env.E2E_CURATION_ID ? {curation:{id:process.env.E2E_CURATION_ID}} : await api('post', '/api/v1/shopping-plans', {
      originalIntent:'Shopify에서 Lamy Safari 만년필을 찾아줘. 가격과 입문자 사용 편의성을 비교해줘.',
      controlMode:'AUTO', planningMode:'AUTO', executionMode:'EXPERIMENT',
      budget:{schemaVersion:'vitlane.curation-budget.v1', inputMode:'EXPLICIT', currency:'USD', totalAmount:'60', allocationMode:'EQUAL'},
      location:{country:'US', city:''}, category:'', allowedItems:[], blockedItems:[], referenceUrl:'', urlMode:'NONE', agentMode:'MANAGED', modelKey:cap.defaultModelKey
    },201,{'Idempotency-Key':randomUUID()});
    const id = created.curation.id, path = '/api/v1/curations/' + id;
    await fs.writeFile(out+'/curation-id.txt',id);
    console.log('Live curation',id,'model',cap.defaultModelKey);
    const threads=async()=>(await api('get',path+'/threads')).threads;
    const wait=async(threadID)=>{
      for(let n=0;n<300;n++){
        const ts=await threads(), t=threadID?ts.find(t=>t.id===threadID):ts[0];
        if(t && ['SUCCEEDED','FAILED','CANCELLED','WAITING_SELECTION'].includes(t.status)) return t;
        await new Promise(r=>setTimeout(r,1000));
      }throw Error('Thread timed out');
    };
    const initial=process.env.E2E_CURATION_ID ? (await threads()).find(t=>t.actions.some(a=>a.response?.kind==="COMMENT")) : await wait();
    await fs.writeFile(out+'/initial.json',JSON.stringify(initial,null,2));
    console.log('Research',initial.status,initial.actions.map(a=>a.type+':'+a.status).join(' > '));
    assert.equal(initial.status,'SUCCEEDED');
    const comment=initial.actions.find(a=>a.response?.kind==='COMMENT')?.response;
    assert(comment?.references.length, 'Expected researched products and a linked comment');
    const before=await api('get',path+'/workspace');
    const budgetBefore=await api('get',path+'/budget');
    await page.goto(base+'/curations/'+id);
    const input=page.getByRole('textbox',{name:'조사 요청',exact:true});
    await input.waitFor();
    let answered;
    if (process.env.E2E_VALIDATE_EXISTING === "1") { answered=(await threads()).find(t=>t.actions.some(a=>a.response?.kind==="ANSWER")); } else {
    const previous=new Set((await threads()).map(t=>t.id));
    await input.fill('방금 찾은 상품들의 조회된 가격과 통화를 알려줘. 최저가나 옵션별 가격 범위라면 구분해서 알려줘. 새로 조사하거나 설정을 바꾸지는 마.');
    await page.getByRole('button',{name:'조사 요청 보내기',exact:true}).click();
    let next;
    for(let n=0;n<60 && !next;n++){next=(await threads()).find(t=>!previous.has(t.id));if(!next) await new Promise(r=>setTimeout(r,500));}
    assert(next,'question accepted');
    answered=await wait(next.id);
    }
    const answer=answered.actions.find(a=>a.response?.kind==='ANSWER')?.response;
    await fs.writeFile(out+'/answer.json',JSON.stringify(answered,null,2));
    console.log('Question',answered.status,answer?.body);
    assert.equal(answered.status,'SUCCEEDED');assert(answer?.references.length);
    assert(answered.actions.every(a=>['AUTO_START','RESPONSE'].includes(a.type)),'question must not execute work');
    assert(!/PRODUCT_RANGE|SELECTED_VARIANT|minimumMinor|estimatedMinorByCurrency/.test(answer.body), 'internal price types must stay out of prose');
    assert(/[0-9]/.test(answer.body) && /USD|달러|\$/.test(answer.body),'answer uses USD listing prices');
    const after=await api('get',path+'/workspace');
    assert.deepEqual(after.targets,before.targets);
    assert.equal((await api('get',path+'/budget')).version,budgetBefore.version);
    const hydration = page.waitForResponse(r => r.url().includes('/catalog-research/hydrations') && r.request().method() === 'POST');
    await page.reload();
    await hydration;
    await page.locator('[data-response-kind="ANSWER"]').last().waitFor();
    assert.equal(await page.locator('.curation-response__caption').count(),(await threads()).filter(t=>t.actions.some(a=>a.response)).length);
    assert((await page.locator('.curation-response__caption').last().textContent()).includes('가격은 조회된 판매 페이지 기준'));
    await page.screenshot({path:out+'/prices-ko.png',fullPage:true});
    await page.locator('[data-response-kind="ANSWER"]').last().locator('button').first().click();
    await page.locator('.catalog-ui-candidate-modal, .candidate-detail-dialog').first().waitFor();
    await page.screenshot({path:out+'/linked-product.png',fullPage:true});
    assert.deepEqual(errors,[]);
    await fs.writeFile(out+'/result.json',JSON.stringify({url:base+'/curations/'+id,model:answer.modelKey,comment:comment.body,answer:answer.body,questionOnly:true,budgetUnchanged:true,errors},null,2));
    console.log('PASS live Shopify → response prices → linked product; question leaves targets and budget unchanged');
  } finally { await browser.close(); }
})().catch(e=>{console.error(e);process.exitCode=1});
