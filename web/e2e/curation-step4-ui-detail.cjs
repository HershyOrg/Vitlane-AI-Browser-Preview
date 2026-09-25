// Actual local API + dedicated synthetic DB. Does not call a live AI provider.
const { firefox } = require('playwright');
const assert = require('node:assert/strict');
const fs = require('node:fs/promises');
const base = process.env.E2E_BASE_URL || 'http://127.0.0.1:18094';
const id = process.env.E2E_CURATION_ID;
const out = process.env.E2E_SCREENSHOT_DIR || '/tmp/vitlane-step4-ui-detail';
if (!id || process.env.E2E_SYNTHETIC !== '1' || !['localhost','127.0.0.1'].includes(new URL(base).hostname)) throw Error('Requires E2E_CURATION_ID, E2E_SYNTHETIC=1 and an isolated local review DB.');
(async()=>{
  const browser=await firefox.launch();const c=await browser.newContext();const p=await c.newPage();const errors=[];p.on('pageerror',e=>errors.push(e.message));p.setDefaultTimeout(12000);
  const get=async path=>{const r=await c.request.get(base+path);assert.equal(r.status(),200);return r.json();};
  const work=()=>get(`/api/v1/curations/${id}/workspace`);
  const frozen=w=>JSON.stringify(w.catalogResearch.pools.flatMap(pool=>[...pool.products,...pool.hiddenProducts]).map(v=>({id:v.candidateId,assessment:v.axisAssessment,intent:v.intentPoint})));
  try {
    await fs.mkdir(out,{recursive:true});assert.equal((await c.request.post(base+'/api/v1/dev/auth/session',{data:{profileKey:'empty-user'}})).status(),201);
    const initial=await work(), target=initial.targets[0].id, cp=`/api/v1/curations/${id}/targets/${target}/criteria`, before=await get(cp), original=frozen(initial);
    const preference=await get('/api/v1/me/preferences');
    async function prefs(locale){const v=await get('/api/v1/me/preferences');assert.equal((await c.request.patch(base+'/api/v1/me/preferences',{data:{schemaVersion:'vitlane.user-preferences.v1',expectedVersion:v.preferences.version,uiLocale:locale}})).status(),200);}
    await prefs('en-US');await p.goto(base+'/curations/'+id);await p.locator('.research-axis-trigger').first().waitFor();
    // Cancel has no write; explicit importance, add and remove round-trip through API.
    await p.locator('.research-axis-trigger').first().click();await p.keyboard.press('Escape');await p.locator('.research-criteria__popover').waitFor({state:'hidden'});assert.deepEqual(await get(cp),before);
    const weight=before.axes[0].importance===5?4:5;
    await p.locator('.research-axis-trigger').first().click();await p.getByRole('group',{name:'Importance',exact:true}).getByRole('button',{name:String(weight),exact:true}).click();
    await p.getByRole('button',{name:'Save',exact:true}).click();await p.locator('.research-criteria__editor').waitFor({state:'hidden'});assert.equal((await get(cp)).axes[0].importance,weight);
    await p.getByRole('button',{name:'Add criterion',exact:true}).click();await p.getByLabel('Criterion',{exact:true}).fill('Portability');await p.getByRole('button',{name:'Save',exact:true}).click();await p.locator('.research-criteria__editor').waitFor({state:'hidden'});
    assert.equal((await get(cp)).axes.length,before.axes.length+1);
    await p.getByRole('button',{name:/Edit Portability/}).click();await p.getByRole('button',{name:'Remove',exact:true}).click();await p.locator('.research-criteria__editor').waitFor({state:'hidden'});assert.equal((await get(cp)).axes.length,before.axes.length);
    await p.locator('.research-axis-trigger').first().click();await p.getByRole('group',{name:'Importance',exact:true}).getByRole('button',{name:String(before.axes[0].importance),exact:true}).click();await p.getByRole('button',{name:'Save',exact:true}).click();await p.locator('.research-criteria__editor').waitFor({state:'hidden'});
    assert.deepEqual((await get(cp)).axes,before.axes);assert.equal(frozen(await work()),original);
    console.log('PASS explicit tag importance/add/remove, Escape and immutable candidates');
    const results=[];
    for(const locale of ['ko-KR','en-US']) for(const theme of ['light','dark']) for(const size of [{width:1440,zoom:1},{width:320,zoom:1},{width:320,zoom:2}]){
      const accent=size.width===1440?'neutral':'blue';
      await prefs(locale);await p.setViewportSize({width:size.width,height:1000});await p.addInitScript(({theme,accent})=>localStorage.setItem('vitlane.appearance.v2',JSON.stringify({theme,accent})),{theme,accent});
      await p.goto(base+'/curations/'+id);await p.locator('.candidate-comparison__score').first().waitFor();await p.evaluate(z=>{document.documentElement.style.fontSize=`${16*z}px`;window.dispatchEvent(new Event('resize'));},size.zoom);
      const ko=locale==='ko-KR', sort=p.getByRole('button',{name:ko?'정렬':'Sort',exact:true});
      assert.equal(await sort.locator('svg.lucide-arrow-up-down').count(),1);
      await sort.click();await p.getByRole('menuitemradio',{name:ko?'낮은 가격순':'Lowest price',exact:true}).click();assert((await sort.innerText()).includes(ko?'낮은 가격순':'Lowest price'));
      await sort.click();await p.getByRole('menuitemradio',{name:ko?'높은 가격순':'Highest price',exact:true}).click();assert((await sort.innerText()).includes(ko?'높은 가격순':'Highest price'));
      await sort.click();await p.getByRole('menuitemradio',{name:before.axes[0].label,exact:true}).click();
      await sort.click();await p.getByRole('menuitemradio',{name:'Vitlane Pick',exact:true}).click();
      assert.equal(await p.locator('.research-comparison-controls input, .research-comparison-controls select, .curation-candidate-card details, .curation-candidate-card .candidate-axis-details').count(),0);
      const card=p.locator('.curation-candidate-card').first();
      assert(/\/100/.test(await card.locator('.candidate-comparison__score').innerText()));
      const colors=await card.evaluate(e=>({axis:getComputedStyle(e.querySelector('.candidate-comparison__axis')).color,score:getComputedStyle(e.querySelector('.candidate-comparison__value')).color,title:getComputedStyle(e.querySelector('.vt-candidate-card__title')).color}));assert.equal(colors.score,colors.title);if(accent!=='neutral')assert.notEqual(colors.axis,colors.title); // Ink comparison is achromatic (ADR-0082)
      assert.equal(await card.locator('.vt-candidate-card__icon-actions').count(),0);
      assert.equal(await sort.evaluate(e=>getComputedStyle(e).outlineStyle),'none');
      assert.equal(await p.locator('.catalog-ui-composer-dock').evaluate(e=>getComputedStyle(e).backdropFilter),'none','the composer is the canvas colour behind one line, not glass (ADR-0086)');
      const evidence=await card.locator('.vt-candidate-card__evidence').evaluate(e=>({clamp:getComputedStyle(e).webkitLineClamp,overflow:getComputedStyle(e).overflow,orient:getComputedStyle(e).webkitBoxOrient}));assert.deepEqual(evidence,{clamp:'2',overflow:'hidden',orient:'vertical'});
      const badge=await card.locator('.candidate-ranking-badge').evaluate(el=>({text:el.textContent,weight:getComputedStyle(el).fontWeight,pseudo:getComputedStyle(el,'::before').content,logo:!!el.querySelector('.vt-brand-mark__symbol')}));assert(!/공동|tied/i.test(badge.text));assert(Number(badge.weight)>=600);assert.equal(badge.pseudo,'none');assert(badge.logo);assert.equal(badge.text.trim(),'Pick');
      await p.getByRole('button',{name:ko?'비교 기준 추가':'Add criterion',exact:true}).click();
      const pop=await p.locator('.research-criteria__popover').evaluate(e=>e.getBoundingClientRect().toJSON());assert(pop.x>=0&&pop.right<=size.width+1,JSON.stringify(pop));await p.keyboard.press('Escape');await p.locator('.research-criteria__popover').waitFor({state:'hidden'});
      await p.locator('.catalog-ui-target__identity').first().evaluate(e=>e.scrollIntoView({block:'start'}));
      if(theme==='light')await p.screenshot({path:`${out}/${locale}-${size.width}-${size.zoom}x-curation.png`});
      const hit=card.locator('.vt-candidate-card__hit-area');await hit.click();const modal=p.locator('.candidate-detail');await modal.waitFor();
      const scores=modal.locator('.candidate-axis-details__score');const expected=initial.catalogResearch.pools[0].products[0].axisAssessment.scores;
      assert.deepEqual(await scores.allTextContents(),expected.map(score=>`${score.scorePercent}/100`));assert.equal(await modal.locator('meter').count(),0);assert.equal(await modal.locator('.vt-disclosure').count(),5);
      const box=await modal.evaluate(e=>{const m=e.querySelector('.catalog-ui-candidate-modal__media').getBoundingClientRect(),d=e.getBoundingClientRect(),scroll=e.querySelector('.candidate-detail__content');return {width:d.width,x:d.x,right:d.right,media:m.width,square:Math.abs(m.width-m.height),overflow:scroll.scrollWidth-scroll.clientWidth,footerBottom:e.querySelector('.catalog-ui-candidate-modal__footer').getBoundingClientRect().bottom};});
      assert(box.x>=0&&box.right<=size.width+1,JSON.stringify(box));assert(box.square<1&&box.media>(size.width===1440?200:100),JSON.stringify(box));assert(box.overflow<2,JSON.stringify(box));assert(box.footerBottom<=1001,JSON.stringify(box));
      if(theme==='light')await p.screenshot({path:`${out}/${locale}-${size.width}-${size.zoom}x-detail.png`});
      await p.keyboard.press('Tab');assert(await modal.evaluate(e=>e.contains(document.activeElement)));
      const notice=modal.locator('.vt-notice__body').first();if(await notice.count())assert(await notice.evaluate(e=>e.clientWidth)>size.width/3);
      await modal.locator('.candidate-axis-details').scrollIntoViewIfNeeded();
      if(theme==='light'&&size.width===1440)await p.screenshot({path:`${out}/${locale}-assessment.png`});
      await p.keyboard.press('Escape');await modal.waitFor({state:'hidden'});assert(await hit.evaluate(e=>e===document.activeElement));
      const pageWidth=await p.evaluate(()=>document.documentElement.scrollWidth);assert(pageWidth<=size.width+1,`${pageWidth}/${size.width}`);
      results.push({locale,theme,...size,box,badge});console.log('PASS',locale,theme,size.width,size.zoom);
    }
    await prefs(preference.preferences.uiLocale);assert.equal(frozen(await work()),original);assert.deepEqual(errors,[]);
    await fs.writeFile(out+'/ui-result.json',JSON.stringify({id,results,errors,checks:['tag_CRUD','cancel','sort','accent_logo_pick','modal_axes','immutable_assessment','focus','responsive']},null,2));
  } catch(e){await p.screenshot({path:out+'/ui-failure.png'});await fs.writeFile(out+'/ui-failure.txt',await p.locator('body').innerText());throw e;}finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
