// Compare the unchanged product presentation against a separately built main ref.
// E2E_ROW_BASELINE_DIST=<main web/dist> node e2e/curation-row-regression.cjs
// The baseline must include ADR-0089 (2026-09-23): it changed the Row itself, so ab658cdc no longer matches.
// Both builds receive the same synthetic API responses. No server or provider writes.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises"), path = require("node:path");
const { firefox } = require("playwright");
const id = "e5100000-0000-4000-8000-000000000002", timestamp = "2026-09-21T00:00:00Z";
const out = process.env.E2E_SCREENSHOT_DIR || "/tmp/vitlane-row-regression";
function fixture(targetCount, split, combination = false) {
 const scope = {country:"US",allowedItems:[],blockedItems:[],urlMode:"NONE"};
 const targets = Array.from({length:targetCount},(_,i)=>({
  id:"target-"+i,planId:"plan",curationId:id,userId:"fixture",title:i===0?"Fountain pen":"Blue writing ink",
  normalizedIntent:"Daily writing",category:"Stationery",allocatedBudget:{amount:"50",currency:"USD"},
  researchScope:scope,orderIndex:i,targetHashSchema:"vitlane.plan-target.v1",version:1,createdAt:timestamp,updatedAt:timestamp
 }));
 const pools=targets.map((target,n)=>({targetId:target.id,version:1,expandOrdinal:0,latestMode:"APPEND",sourceCoverage:[],messages:[],hiddenProducts:[],
  products:Array.from({length:3},(_,i)=>({candidateId:target.id+"-candidate-"+i,source:"AMAZON",
   title:(n===0?"Everyday Fountain Pen ":"Blue Bottled Writing Ink ")+(i+1),description:"Fixture product for layout regression.",
   intentPoint:"Comfortable for everyday writing.",features:[],specifications:[],categories:["Stationery"],
   priceMinimumMinor:2000+i*500,priceMaximumMinor:2000+i*500,currency:"USD",
   productUrl:"https://shop.example/product/"+n+"/"+i,
   locator:{kind:"PRODUCT_URL",productUrl:"https://shop.example/product/"+n+"/"+i},
   hydration:{status:"READY"},
   variantObservation:{observationId:"observation-"+i,variantRef:{asin:"B000000001",country:"US"},price:{kind:"OBSERVED",amountMinor:2000+i*500,currency:"USD"},availability:"UNKNOWN",deliveryEligibility:"UNCONFIRMED",seller:{kind:"UNKNOWN"},purchaseRoute:"EXTERNAL",productUrl:"https://www.amazon.com/dp/B000000001",observedAt:timestamp,refreshAfter:"2099-01-01T00:00:00Z"},
   mediaUrl:"data:image/svg+xml,"+encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" width="80" height="80"><rect width="80" height="80" fill="#edf2f6"/><rect x="30" y="10" width="20" height="60" rx="4" fill="#285880"/></svg>'),
  }))}));
 const job=t=>({jobId:"job-"+t.id,actionId:"research",kind:"RESEARCH_ROUND",targetId:t.id,status:"SUCCEEDED",effects:[{kind:"CANDIDATES_ADDED",targetId:t.id,count:3}]});
 const makeThread=(name,ts,jobs)=>({schemaVersion:"vitlane.curation-thread.v2",id:name,curationId:id,mode:"AUTO",origin:"REQUEST",request:"Compare everyday writing products.",revision:2,status:"SUCCEEDED",createdAt:ts,updatedAt:ts,targetLabels:Object.fromEntries(targets.map(t=>[t.id,t.title])),
  actions:[{id:name+"-research",threadId:name,sequence:0,type:"START_RESEARCH",status:"SUCCEEDED",jobs,effects:[],decisionIds:[],decisions:[],answers:[]}]});
 const threads=split?targets.map((t,i)=>makeThread("research-"+i,"2026-09-21T00:0"+i+":00Z",[job(t)])):[makeThread("research",timestamp,targets.map(job))];
 const combo=makeThread("combination","2026-09-21T00:05:00Z",[]);
 combo.request="";
 combo.actions=[{id:"reply",threadId:combo.id,sequence:0,type:"RESPONSE",instruction:"COMBINATION",status:"SUCCEEDED",jobs:[],effects:[],decisionIds:[],decisions:[],answers:[],
  response:{schemaVersion:"vitlane.thread-response.v2",kind:"COMMENT",body:"These products work well for daily writing.",locale:"en-US",references:[],createdAt:combo.createdAt,
   combination:{items:combination?targets.map(t=>({targetId:t.id,candidateId:t.id+"-candidate-0",checkoutEligible:false,quantity:1})):[],criteriaVersions:Object.fromEntries(targets.map(t=>[t.id,1])),reasons:["Comfortable for daily use."],tips:[],cautions:[],budgetAdvice:"You can keep the remaining budget."}}}];
 threads.push(combo);
 return {targets,threads,workspace:{plan:{id:"plan",userId:"fixture",originalIntent:"Compare writing products",planningMode:"SINGLE",executionMode:"EXPERIMENT",totalBudget:{amount:"100",currency:"USD"},locationContext:{country:"US"},researchScope:scope,createdAt:timestamp},
 curation:{id,shoppingPlanId:"plan",userId:"fixture",phase:"CURATING",version:2,createdAt:timestamp,updatedAt:timestamp},targets,
 research:{groups:targets.map(t=>({session:{id:"session-"+t.id,planTargetId:t.id,status:"REVIEWING",version:1},candidates:[]}))},
 cart:{selections:[]},availableActions:[],timeline:[],latestArtifact:"CURATION_BOARD",intelligence:[],coverage:"NONE",
 catalogResearch:{schemaVersion:"vitlane.catalog-research-workspace.v3",pools,configurations:[],interactions:[],messages:[]}}};
}
async function install(page,state,locale,writes,unexpected){
 await page.route("**/api/**",async route=>{
  const req=route.request(),p=new URL(req.url()).pathname;let json,status=200;
  if(!p.startsWith("/api/"))return route.continue();
  if(p==="/api/v1/analytics/config"&&req.method()==="GET") return route.fulfill({json:{schemaVersion:"vitlane.analytics-config.v1",mode:"disabled",measurementId:"",release:"fixture"}});
  if(p==="/api/v1/me") json={user:{id:"fixture",email:"fixture@vitlane.example",displayName:"Fixture",marketingAdmin:false,phase5Operator:false}};
  else if(p==="/api/v1/me/preferences"){const pref={schemaVersion:"vitlane.user-preferences.v1",version:1,uiLocale:locale,researchCountry:"US",preferredCurrency:"USD"};json={preferences:pref,effective:pref};}
  else if(p.endsWith("/threads")&&req.method()==="GET")json={schemaVersion:"vitlane.curation-thread.v2",controlMode:{mode:"AUTO",version:1},threads:state.threads};
  else if(p.endsWith("/threads")&&req.method()==="POST"){writes.push(req.postDataJSON());json={...state.threads.at(-1),id:"new-combination",request:"",revision:1,status:"RUNNING"};status=202;}
  else if(p.endsWith("/amazon/state"))json={schemaVersion:"vitlane.amazon-candidate-state.v3",configurationVersion:0,variantRef:{asin:"B000000001",country:"US"},purchaseFeedback:{schemaVersion:"vitlane.external-purchase-feedback.v3",version:0,records:[]}};
  else if(p.endsWith("/criteria"))json=null;
  else if(p.endsWith("/budget"))json=state.budget||{schemaVersion:"vitlane.curation-budget.v1",version:0,researchVersion:0,enabled:false,currency:"USD",totalAmount:null,allocations:state.targets.map(t=>({targetId:t.id,quantity:1,amount:null}))};
  else if(p.endsWith("/research-settings"))json={schemaVersion:"vitlane.research-settings.v1",country:"US",version:0};
  else if(p==="/api/v1/managed-runner/usage")json={enabled:true,usageDate:"2026-09-21",userSpentMicros:0,userLimitMicros:100000,userExhausted:false,serverExhausted:false,serverLimitMicros:8000000};
  else if(p.endsWith("/exchange-rate"))json={schemaVersion:"vitlane.exchange-rate.v1",status:"UNAVAILABLE"};
  else if(p==="/api/v1/curations")json={schemaVersion:"vitlane.curation-list.v2",curations:[]};
  else if(p==="/api/v1/support/summary")json={schemaVersion:"vitlane.support-summary.v1",unread:0};
  else if(p==="/api/v1/auth/capabilities")json={googleEnabled:true,localReviewEnabled:false,localReviewSeeded:false,localReviewProfiles:[]};
  else if(p.endsWith("/background-research"))json={schemaVersion:"vitlane.background-research.v1",subscriptions:[],findings:[]};
  else if(p==="/api/v1/curations/product-notices/sync")json={schemaVersion:"vitlane.curation-notice-sync.v1",curationIds:[]};
  else if(p.endsWith("/workspace"))json=state.workspace;
  else if(p.endsWith("/cart"))json={schemaVersion:"vitlane.cart-view.v2",curationId:id,version:0,country:"US",currency:"USD",items:[]};
  else if(p.endsWith("/catalog-research/hydrations"))json={...state.workspace.catalogResearch,schemaVersion:"vitlane.catalog-research-hydration.v1"};
  else {unexpected.push(req.method()+" "+p);return route.abort();}
  if(req.method()!=="GET"&&!p.endsWith("/threads")&&!p.endsWith("/catalog-research/hydrations")&&!p.endsWith("/product-notices/sync"))unexpected.push(req.method()+" "+p);
  await route.fulfill({status,json});
 });
 await page.route("https://**",route=>{unexpected.push("External network");return route.abort();});
}
async function metrics(page){
 return page.locator("[data-result-target]").evaluateAll(rows=>rows.map(row=>{
  const box=row.getBoundingClientRect();
  const elements=[row,...row.querySelectorAll("*")];
  const styleKeys=["display","gridTemplateColumns","gap","padding","margin","fontFamily","fontSize","fontWeight","lineHeight","color","whiteSpace","flexWrap","borderRadius"];
  const rounded=value=>Math.round(value*100)/100;
  return {holder:row.closest("[data-results-holder]").dataset.resultsHolder,html:row.outerHTML,rect:{width:rounded(box.width),height:rounded(box.height)},
   elements:elements.map(el=>{const r=el.getBoundingClientRect(),s=getComputedStyle(el);return {tag:el.tagName,class:el.className,rect:{x:r.width||r.height?rounded(r.left-box.left):0,y:r.width||r.height?rounded(r.top-box.top):0,width:rounded(r.width),height:rounded(r.height)},style:Object.fromEntries(styleKeys.map(k=>[k,s[k]]))};})};
 }));
}
module.exports={fixture,install};
if(require.main===module)(async()=>{
 assert(process.env.E2E_ROW_BASELINE_DIST,"A separately built main baseline is required");
 const {preview}=await import("vite"),root=path.resolve(__dirname,"..");
 const servers=[],browser=await firefox.launch(),evidence=[];
 const pixelPage=await browser.newPage();
 await fs.mkdir(out,{recursive:true});
 try{
  for(const dist of [path.resolve(process.env.E2E_ROW_BASELINE_DIST),path.join(root,"dist")]){
   const server=await preview({root,configFile:false,logLevel:"error",build:{outDir:dist},preview:{host:"127.0.0.1",port:0}});
   servers.push(server);
  }
  for(const scenario of [{name:"rows",count:2,split:false},{name:"split-cards",count:2,split:true},{name:"single-card",count:1,split:false}]){
   for(const locale of ["ko-KR","en-US"])for(const width of [1440,320])for(const scale of [1,2]){
    const results=[];
    for(let variant=0;variant<2;variant++){
     const context=await browser.newContext({viewport:{width,height:1000}});
     const page=await context.newPage(),writes=[],unexpected=[],errors=[];
     page.on("pageerror",e=>errors.push(e.message));
     await install(page,fixture(scenario.count,scenario.split),locale,writes,unexpected);
     await page.goto(servers[variant].resolvedUrls.local[0]+"curations/"+id);
     await page.locator("[data-result-target]").first().waitFor();
     await page.waitForFunction(() => [...document.querySelectorAll("[data-result-target]")].every(row => /Everyday Fountain Pen|Blue Bottled Writing Ink/.test(row.textContent)));
     // Exclude unrelated sticky composer occlusion from the product-only screenshot.
     await page.addStyleTag({content:".curation-workspace__dock-shell { visibility: hidden; }"});
     await page.evaluate(s=>{document.documentElement.style.fontSize=(s*100)+"%";},scale);
     await page.evaluate(()=>document.fonts.ready);
     await page.waitForTimeout(350);
     const list=page.locator(".curation-results__list").first();
     await list.scrollIntoViewIfNeeded();await page.waitForTimeout(100);
     const measured=await metrics(page);
     const key=scenario.name+"-"+locale+"-"+width+"-"+scale;
     // Element screenshots also compare the original Row pixels, not just DOM/overflow.
     // Rasterize at the same integer origin: different transcript scroll offsets can
     // otherwise change Firefox text antialiasing although the measured Row is identical.
     const originalStyle=await list.evaluate(el=>{const original=el.getAttribute("style");const r=el.getBoundingClientRect();Object.assign(el.style,{position:"fixed",top:"0px",left:"0px",width:r.width+"px",zIndex:"1000",backgroundColor:getComputedStyle(document.querySelector(".curation-workspace")).backgroundColor});return original;});
     const clip=await list.boundingBox();
     const shot=await page.screenshot({path:out+"/"+key+"-"+(variant?"fixed":"main")+".png",clip:{...clip,width:Math.floor(clip.width),height:Math.floor(clip.height)},animations:"disabled"});
     await list.evaluate((el,value)=>{if(value===null)el.removeAttribute("style");else el.setAttribute("style",value);},originalStyle);
     assert.equal(await page.locator(".curation-result__list-label, .curation-target-sheet__group-title small").count(),0);
     if(variant===1&&scenario.name==="rows"&&width===1440&&scale===1){
      const detail=page.locator(".catalog-ui-candidate-modal"),sheet=page.locator(".curation-target-sheet");
      await page.locator(".curation-result__row").first().click();await detail.waitFor();assert.equal(await sheet.count(),0);
      await page.keyboard.press("Escape");await detail.waitFor({state:"detached"});
      await page.locator(".curation-result__list").first().click();await sheet.waitFor();
      await page.keyboard.press("Escape");await sheet.waitFor({state:"detached"});
      await page.locator(".curation-results__all").first().click();await sheet.waitFor();
      assert.equal(await sheet.getAttribute("data-target-sheet"),"*all");
      assert.equal(await sheet.locator(".curation-target-sheet__group-title small").count(),0);
      await page.keyboard.press("Escape");await sheet.waitFor({state:"detached"});
      // Every product here is an external-store listing: nothing can go into the Vitlane Cart, so the combination
      // offers no Cart action at all (owner 2026-09-23 — no "nothing was added" notice).
      assert.equal(await page.locator(".curation-results__combination").count(),0);
      assert.equal(writes.length,0);assert.equal(await page.locator(".curation-cart-toast").count(),0);
      assert.equal(await page.getByRole("button",{name:locale==="ko-KR"?"조합 추천":"Recommend combination",exact:true}).count(),0);
     }
     assert.deepEqual(unexpected,[]);assert.deepEqual(errors,[]);
     results.push({measured,shot});await context.close();
    }
    assert.deepEqual(results[1].measured,results[0].measured,scenario.name+" "+locale+" "+width+" "+scale+": original Row DOM, styles, dimensions and ownership");
    const pixels=await pixelPage.evaluate(async urls=>{
     const read=async url=>{const image=new Image();image.src=url;await image.decode();const canvas=document.createElement("canvas");canvas.width=image.width;canvas.height=image.height;const c=canvas.getContext("2d");c.drawImage(image,0,0);return {width:image.width,height:image.height,data:c.getImageData(0,0,image.width,image.height).data};};
     const [a,b]=await Promise.all(urls.map(read));let changed=0,maxDelta=0;
     if(a.width!==b.width||a.height!==b.height)return {dimensions:[a.width,a.height,b.width,b.height]};
     for(let i=0;i<a.data.length;i+=4){let different=false;for(let j=0;j<4;j++){const delta=Math.abs(a.data[i+j]-b.data[i+j]);maxDelta=Math.max(maxDelta,delta);different ||= delta>0;}if(different)changed++;}
     return {changed,maxDelta,total:a.width*a.height};
    },results.map(r=>"data:image/png;base64,"+r.shot.toString("base64")));
    assert.equal(pixels.changed,0,scenario.name+" "+locale+" "+width+" "+scale+": original product pixels "+JSON.stringify(pixels));
    evidence.push({...scenario,locale,width,scale,domStylesAndGeometry:"identical",pixels:"identical"});
   }
  }
  await fs.writeFile(out+"/result.json",JSON.stringify(evidence,null,2));
  console.log("ROW_REGRESSION_PASS",evidence.length,"same-data main/fixed comparisons; Row, split/single cards, detail, lists, Cart action skips external items without writes");
 }finally{await browser.close();for(const server of servers)await new Promise(resolve=>server.httpServer.close(resolve));}
})().catch(e=>{console.error(e);process.exitCode=1;});
