// Real UI + HTTP client against deterministic API fixtures. No provider calls.
const assert=require("node:assert/strict"),fs=require("node:fs/promises");
const {firefox}=require("playwright"),{fixture,install}=require("./curation-row-regression.cjs");
(async()=>{
 const {preview}=await import("vite"),path=require("node:path"),root=path.resolve(__dirname,"..");
 const server=await preview({root,configFile:false,logLevel:"error",build:{outDir:path.join(root,"dist")},preview:{host:"127.0.0.1",port:0}});
 const browser=await firefox.launch();
 try {
  for(const locale of ["ko-KR","en-US"]){
   const state=fixture(2,false,true),pools=state.workspace.catalogResearch.pools;
   for(const pool of pools)for(const product of pool.products){product.source="SHOPIFY";delete product.variantObservation;}
   const [a,c]=[pools[0].products[1],pools[0].products[2]],b=pools[1].products[1];
   const variants=new Map(pools.flatMap(p=>p.products).map(p=>[p.candidateId,{variantId:p.candidateId+"-v",title:"Default",priceMinor:2000,currency:"USD",available:true,selectedOptions:[],mediaUrl:p.mediaUrl}]));
   state.workspace.catalogResearch.configurations=pools.flatMap(p=>p.products).map(p=>({candidateId:p.candidateId,version:1,observedAt:"2026-09-22T00:00:00Z",variant:variants.get(p.candidateId)}));
   // The initial research response already contains the combination.
   const reply=state.threads.pop().actions[0],initial=state.threads[0];
   reply.threadId=initial.id;reply.sequence=1;reply.instruction="COMMENT";
   initial.actions.push(reply);
   const plan=reply.response.combination;
   plan.items=[a,b].map((p,i)=>({targetId:pools[i].targetId,candidateId:p.candidateId,checkoutEligible:true,quantity:1,variantId:variants.get(p.candidateId).variantId,configurationVersion:1}));
   plan.compatibility="UNVERIFIED";
   const context=await browser.newContext({viewport:{width:1440,height:1000}});
   const page=await context.newPage(),writes=[],unexpected=[],errors=[],commands=[];page.on("pageerror",e=>errors.push(e.message));
   await install(page,state,locale,writes,unexpected);
   let saved={schemaVersion:"vitlane.cart-view.v2",curationId:state.workspace.curation.id,version:0,country:"US",currency:"USD",items:[]};
   await page.route("**/cart",r=>r.fulfill({json:saved}));
   await page.route("**/variant-pages",r=>{const id=decodeURIComponent(new URL(r.request().url()).pathname.split("/").at(-2));return r.fulfill({json:{schemaVersion:"vitlane.variant-page.v2",source:"SHOPIFY",candidateId:id,productTitle:"Product",merchantDomain:"shop.example",rows:[variants.get(id)],pagination:{pageSize:20,hasNext:false,hasPrevious:false},observedAt:"2026-09-22T00:00:00Z",metrics:{}}});});
   let failNext=0;
   await page.route("**/representative-cart",async r=>{
    // A refused add is the one case the combination action still speaks about.
    if(failNext>0){failNext--;return r.fulfill({status:409,json:{error:{code:"CONFLICT",reasonCode:"COMBINATION_CART_LIMIT",retryable:false,message:"Cart limit"}}});}
    const body=r.request().postDataJSON();commands.push(body);
    assert.equal(body.schemaVersion,"vitlane.representative-cart-command.v1");
    assert.equal(body.expectedVersion,saved.version);
    assert(body.items.every(i=>!Object.hasOwn(i,"productImageUrl")),"display images are not command fields");
    saved={...saved,version:saved.version+1,items:[...saved.items,...body.items.map(i=>({...i,addedAt:"2026-09-22T00:00:00Z"}))]};
    await r.fulfill({json:saved});
   });
   await page.goto(server.resolvedUrls.local[0]+"curations/"+state.workspace.curation.id);
   const main=page.locator(".curation-results[data-results-holder]").first(),entry=main.locator(".curation-results__combination");
   await entry.waitFor();
   await page.waitForFunction(()=>{const el=document.querySelector(".curation-response__paragraph + .curation-response__paragraph");return el&&parseFloat(getComputedStyle(el).marginBlockStart)>0;});
   const paragraphGap=await page.locator(".curation-response__paragraph + .curation-response__paragraph").first().evaluate(el=>({gap:parseFloat(getComputedStyle(el).marginBlockStart),line:parseFloat(getComputedStyle(el).lineHeight)}));
   assert(paragraphGap.gap>0&&paragraphGap.gap<paragraphGap.line,"paragraphs retain a compact gap below a full blank line: "+JSON.stringify(paragraphGap));
   const representatives=()=>main.locator("[data-representative]").evaluateAll(rows=>rows.map(r=>r.dataset.representative));
   await page.waitForFunction(ids=>ids.every(id=>document.querySelector('.curation-results [data-representative="'+id+'"]')),[a.candidateId,b.candidateId]);
   assert.deepEqual(await representatives(),[a.candidateId,b.candidateId],"AI combination initializes representatives, not first individual candidates");
   assert.match(await main.innerText(),locale==="ko-KR"?/조합 어울림/:/Combination fit/);
   // Adding says nothing (owner 2026-09-23); a refused add still does, in the bottom-right toast.
   const toast=page.locator(".curation-cart-toast");
   failNext=1;await entry.click();await toast.waitFor();
   assert.match(await toast.innerText(),locale==="ko-KR"?/조합을 추가하지 못했습니다/:/Could not add the combination/);
   assert.equal(await toast.evaluate(el=>getComputedStyle(el).animationName),"curation-toast-enter");
   assert.notEqual(await toast.evaluate(el=>getComputedStyle(el).boxShadow),"none","Toast keeps its shade");
   const rect=await page.locator(".curation-cart-toast-viewport").boundingBox();assert(rect.y+rect.height>950&&rect.x>900,"bottom-right toast");
   await page.waitForTimeout(2400);assert(await toast.isVisible(),"Toast remains visible before three seconds");
   await toast.waitFor({state:"detached",timeout:1600});
   await page.emulateMedia({reducedMotion:"reduce"});failNext=1;await entry.click();await toast.waitFor();
   assert.equal(await toast.evaluate(el=>getComputedStyle(el).animationName),"none");
   await toast.getByRole("button").click();await toast.waitFor({state:"detached"});await page.emulateMedia({reducedMotion:"no-preference"});
   await Promise.all([page.waitForResponse(r=>r.url().endsWith("/representative-cart")&&r.ok()),entry.click()]);await page.waitForTimeout(300);
   assert.deepEqual(commands[0].items.map(i=>i.candidateId),[a.candidateId,b.candidateId]);
   assert.equal(await toast.count(),0,"a successful add shows no notice");
   await main.locator(".curation-results__all").click();
   const section=page.locator(".curation-recommended-combination");await section.waitFor();
   assert.match(await section.innerText(),locale==="ko-KR"?/추천 조합/:/Recommended combination/);
   assert.deepEqual(await section.locator("[data-representative]").evaluateAll(rows=>rows.map(r=>r.dataset.representative)),[a.candidateId,b.candidateId]);
   await fs.mkdir("/tmp/vitlane-step8/current-representatives",{recursive:true});
   for(const width of [1440,320])for(const scale of [1,2]){
    await page.setViewportSize({width,height:1000});await page.evaluate(s=>document.documentElement.style.fontSize=(s*100)+"%",scale);
    await page.waitForTimeout(250);
    const geometry=await section.evaluate(el=>{const r=el.getBoundingClientRect();return {left:r.left,right:r.right,scroll:el.scrollWidth,client:el.clientWidth};});
    assert(geometry.left>=-1&&geometry.right<=width+1&&geometry.scroll<=geometry.client+1,"recommended section fits "+locale+" "+width+" "+scale);
    const textWidths=await section.locator(".curation-result__text").evaluateAll(els=>els.map(el=>el.getBoundingClientRect().width));
    assert(textWidths.every(w=>w>=140),"summary text remains readable "+locale+" "+width+" "+scale);
    await page.screenshot({path:"/tmp/vitlane-step8/current-representatives/sidebar-"+locale+"-"+width+"-"+scale+".png"});
   }
   await page.setViewportSize({width:1440,height:1000});await page.evaluate(()=>document.documentElement.style.fontSize="100%");await page.waitForTimeout(250);
   await page.locator('[data-sheet-tab="'+pools[0].targetId+'"]').click();
   const sort=page.getByRole("button",{name:locale==="ko-KR"?"정렬":"Sort",exact:true});await sort.click();
   assert.equal(await page.getByRole("menuitemradio",{name:locale==="ko-KR"?"조합 어울림":"Combination fit",exact:true}).count(),1);
   assert.equal(await page.getByRole("menuitemradio",{name:"Vitlane Pick",exact:true}).count(),1);
   await page.keyboard.press("Escape");
   const candidate=page.locator('[data-candidate-id="'+c.candidateId+'"]').first();
   await candidate.locator(".vt-candidate-card__hit-area").click();
   await page.locator(".catalog-ui-candidate-modal").waitFor();
   await page.locator(".catalog-ui-candidate-modal").getByRole("button",{name:locale==="ko-KR"?"상품 상세 닫기":"Close product details",exact:true}).click();await page.locator(".catalog-ui-candidate-modal").waitFor({state:"detached"});
   await page.locator(".curation-target-sheet [data-sheet-close]").click();await page.locator(".curation-target-sheet").waitFor({state:"detached"});
   assert.deepEqual(await representatives(),[c.candidateId,b.candidateId],"viewing C replaces A even though A is already in Cart");
   await Promise.all([page.waitForResponse(r=>r.url().endsWith("/representative-cart")&&r.ok()),entry.click()]);await page.waitForTimeout(300);
   assert.deepEqual(commands[1].items.map(i=>i.candidateId),[c.candidateId],"C added and already-carted B not duplicated");
   assert.equal(await toast.count(),0,"adding says nothing, even with a product already in the Cart");
   assert.deepEqual(saved.items.map(i=>i.candidateId),[a.candidateId,b.candidateId,c.candidateId]);
   assert.deepEqual(writes,[],"no extra model request");assert.deepEqual(unexpected,[]);assert.deepEqual(errors,[]);
   await page.waitForTimeout(250);await fs.mkdir("/tmp/vitlane-step8/current-representatives",{recursive:true});await page.screenshot({path:"/tmp/vitlane-step8/current-representatives/"+locale+".png"});
   await page.reload();await entry.waitFor();
   assert.deepEqual(await representatives(),[c.candidateId,b.candidateId],"representative choice survives reload");
   // Everything shown is already in the Cart: another press sends nothing and says nothing.
   await entry.click();await page.waitForTimeout(400);
   assert.equal(commands.length,2,"no command for products already in the Cart");assert.equal(await toast.count(),0);
   await context.close();
   // A stored multi-target reply remains history after removing all but one Target.
   const single=fixture(2,false,true);single.targets.pop();single.workspace.catalogResearch.pools.pop();
   const singleContext=await browser.newContext({viewport:{width:1440,height:1000}});
   const singlePage=await singleContext.newPage(),singleWrites=[],singleUnexpected=[];
   await install(singlePage,single,locale,singleWrites,singleUnexpected);
   await singlePage.goto(server.resolvedUrls.local[0]+"curations/"+single.workspace.curation.id);
   const singleResult=singlePage.locator(".curation-results [data-representative]");
   await singleResult.waitFor();
   assert.equal(await singlePage.locator(".curation-results__combination").count(),0,"single Target has no combination Cart action");
   assert(!(await singleResult.innerText()).includes(locale==="ko-KR"?"조합 어울림":"Combination fit"),"old multi-target recommendation cannot choose a single Target");
   await singleResult.locator(".curation-result__more").click();
   await singlePage.getByRole("button",{name:locale==="ko-KR"?"정렬":"Sort",exact:true}).click();
   assert.equal(await singlePage.getByRole("menuitemradio",{name:locale==="ko-KR"?"조합 어울림":"Combination fit",exact:true}).count(),0);
   assert.equal(await singlePage.getByRole("menuitemradio",{name:"Vitlane Pick",exact:true}).count(),1);
   assert.deepEqual(singleWrites,[]);assert.deepEqual(singleUnexpected,[]);
   await singleContext.close();
  }
  console.log("CURRENT_REPRESENTATIVES_PASS: KO/EN initial A+B; distinct fit/Pick; recommended section; Cart A+B; view C; Cart C+B; B not duplicated; reload; single Target after removal uses Pick; bottom-right animated Toast with three-second dismissal; no model request");
 }finally{await browser.close();server.httpServer.closeAllConnections?.();await new Promise(r=>server.httpServer.close(r));}
})().catch(e=>{console.error(e);process.exitCode=1;});
