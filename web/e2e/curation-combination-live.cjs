// Read-only Cart-action smoke against the local review server. No model request or Cart write.
const { firefox } = require("playwright");
const assert = require("node:assert/strict"), fs = require("node:fs/promises");
const base=process.env.E2E_BASE_URL||"http://127.0.0.1:18100";
const id=process.env.E2E_CURATION_ID||"b1b45a50-9e1b-4b5c-97db-e7f26e6fe3a8";
const out="/tmp/vitlane-step8/cart-action";
if(process.env.LIVE_COMBINATION!=="1"||!["127.0.0.1","localhost"].includes(new URL(base).hostname))throw Error("Local opt-in required");
(async()=>{
 const browser=await firefox.launch(),context=await browser.newContext({viewport:{width:1440,height:1000}});
 const api=async(method,path,data,status=200)=>{const r=await context.request[method](base+path,{data});assert.equal(r.status(),status,await r.text());return r.json();};
 let originalPrefs;
 const setLocale=async uiLocale=>{const p=(await api("get","/api/v1/me/preferences")).preferences;if(p.uiLocale!==uiLocale)await api("patch","/api/v1/me/preferences",{schemaVersion:"vitlane.user-preferences.v1",expectedVersion:p.version,uiLocale,preferredCurrency:p.preferredCurrency,researchCountry:p.researchCountry});};
 try{
  await fs.mkdir(out,{recursive:true});
  await api("post","/api/v1/dev/auth/session",{profileKey:"empty-user"},201);
  originalPrefs=(await api("get","/api/v1/me/preferences")).preferences;
  const path="/api/v1/curations/"+id,before=await api("get",path+"/cart");
  const workspace=await api("get",path+"/workspace");
  assert.equal(workspace.catalogResearch.configurations.length,0,"This read-only case expects an unconfigured review curation");
  const page=await context.newPage(),errors=[],writes=[],evidence=[],hydrationErrors=[];
  page.on("response",async r=>{if(r.url().includes("/hydrations")&&!r.ok()){const data=await r.json().catch(()=>({}));hydrationErrors.push({status:r.status(),reason:data.error?.reasonCode});}});
  page.on("pageerror",e=>errors.push(e.message));
  page.on("request",r=>{if(r.method()!=="GET"&&/\/(threads|combination-cart|representative-cart|cart)$/.test(new URL(r.url()).pathname))writes.push(r.url());});
  for(const locale of ["ko-KR","en-US"]){
   await setLocale(locale);await page.goto(base+"/curations/"+id);
   const entry=page.locator(".curation-results__combination").last();await entry.waitFor();
   await page.waitForFunction(()=>[...document.querySelectorAll("[data-result-target]")].some(row=>!/상품 정보를 불러오는 중|Loading product details/.test(row.textContent)),null,{timeout:60000});
   assert.equal(await page.getByRole("button",{name:locale==="ko-KR"?"조합 추천":"Recommend a combination",exact:true}).count(),0);
   assert.equal(await entry.innerText(),locale==="ko-KR"?"조합을 Cart에 추가":"Add combination to Cart");
   for(const width of [1440,320])for(const scale of [1,2]){
    await page.setViewportSize({width,height:1000});await page.waitForTimeout(300);
    const close=page.locator("[data-mobile-sidebar-close]");if(await close.isVisible())await close.click();
    await page.evaluate(s=>document.documentElement.style.fontSize=(s*100)+"%",scale);
    await entry.scrollIntoViewIfNeeded();await entry.click();
    const toast=page.locator(".curation-cart-toast");await toast.waitFor();
    assert.match(await toast.innerText(),locale==="ko-KR"?/옵션 미확정/:/variant not selected/);
    const g=await entry.evaluate(el=>{const n=el.nextElementSibling,a=el.getBoundingClientRect(),b=n.getBoundingClientRect(),s=getComputedStyle(el);return {next:n.className,bottom:a.bottom,top:b.top,left:a.left,right:a.right,font:s.fontSize,otherFont:getComputedStyle(n).fontSize,color:s.color,scroll:el.scrollWidth,client:el.clientWidth,page:document.documentElement.scrollWidth};});
    assert(g.next.includes("curation-results__all"));assert(g.bottom<=g.top+1);assert.equal(g.font,g.otherFont);assert(g.left>=-1&&g.right<=width+1&&g.scroll<=g.client+1&&g.page<=width+1);
    const t=await toast.boundingBox();assert(t.x>=-1&&t.x+t.width<=width+1);
    assert(t.y+t.height>900,"Toast stays at the bottom");
    assert.equal(await toast.evaluate(el=>getComputedStyle(el).animationName),"curation-toast-enter");
    await page.screenshot({path:out+"/"+locale+"-"+width+"-"+scale+".png"});
    evidence.push({locale,width,scale,...g});
    await toast.getByRole("button",{name:locale==="ko-KR"?"알림 닫기":"Dismiss notification"}).click();
   }
  }
  assert.deepEqual(await api("get",path+"/cart"),before);assert.deepEqual(errors,[]);assert.deepEqual(writes,[]);
  await fs.writeFile(out+"/result.json",JSON.stringify({id,evidence,errors,writes,hydrationErrors},null,2));
  console.log("HYDRATION_OBSERVATIONS",JSON.stringify(hydrationErrors));
  console.log("CART_ACTION_LIVE_PASS: 8 locale/viewport/zoom cases; button replaced; unconfirmed variants skipped; no model/research/Cart write");
 }finally{if(originalPrefs)await setLocale(originalPrefs.uiLocale);await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
