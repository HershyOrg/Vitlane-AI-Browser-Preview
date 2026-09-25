// Opt-in: a single initial shopping request must produce a combination without another user command.
const {request,firefox}=require("playwright"),assert=require("node:assert/strict"),fs=require("node:fs/promises"),crypto=require("node:crypto");
const base="http://127.0.0.1:18100",out="/tmp/vitlane-step8/initial-combination";
if(process.env.LIVE_INITIAL_COMBINATION!=="1")throw Error("Explicit local opt-in required");
(async()=>{
 const api=await request.newContext({baseURL:base});
 await api.post("/api/v1/dev/auth/session",{data:{profileKey:"empty-user"}});
 await fs.mkdir(out,{recursive:true});
 const r=await api.post("/api/v1/shopping-plans",{headers:{"Idempotency-Key":crypto.randomUUID()},data:{
  originalIntent:"For everyday writing, recommend one fountain pen and one bottle of fountain-pen ink that work well together. Two product groups, total budget 50 USD.",
  planningMode:"AUTO",controlMode:"AUTO",agentMode:"MANAGED",modelKey:"gpt-5.6-luna",
  executionMode:"EXPERIMENT",totalBudget:{amount:"50",currency:"USD"},budget:{schemaVersion:"vitlane.curation-budget.v1",currency:"USD",totalAmount:"50",allocationMode:"AUTO",inputMode:"EXPLICIT"},
  location:{country:"US",city:"Seattle"},category:"",allowedItems:[],blockedItems:[],referenceUrl:"",urlMode:"NONE",minPrice:null,maxPrice:null
 }});
 assert.equal(r.status(),201,await r.text());
 const created=await r.json(),id=created.curation.id,path="/api/v1/curations/"+id;
 await fs.writeFile(out+"/curation-id",id);console.log("INITIAL_CREATED",id);
 let completed;
 for(let i=0;i<100;i++){
  const threads=await(await api.get(path+"/threads")).json();
  const reply=threads.threads?.flatMap(t=>t.actions?.map(a=>({thread:t,response:a.response}))??[]).find(x=>x.response);
  if(reply){completed=reply;break;}
  if(threads.threads?.some(t=>t.status==="FAILED"))throw Error("Initial flow failed: "+threads.threads.flatMap(t=>t.actions??[]).flatMap(a=>a.jobs??[]).filter(j=>j.status==="FAILED").map(j=>j.reasonCode).join(","));
  if(i%10===0)console.log("INITIAL_WAIT",i*3,"seconds");
  await new Promise(r=>setTimeout(r,3000));
 }
 assert(completed,"initial response must finish");
 assert(completed.response.combination,"initial response itself must be a combination");
 const combo=completed.response.combination;
 for(const item of combo.items)assert(completed.response.body.includes("[["+item.ref+"]]"),"body recommends every initial representative");
 const browser=await firefox.launch();
 try{
  const context=await browser.newContext({storageState:await api.storageState(),viewport:{width:1440,height:1000}});
  const page=await context.newPage();await page.goto(base+"/curations/"+id);
  for(const item of combo.items)await page.locator('.curation-results [data-representative="'+item.candidateId+'"]').waitFor({timeout:60000});
  assert.equal(await page.getByRole("button",{name:"조합 추천",exact:true}).count(),0);
  await page.locator(".curation-results__all").first().click();await page.locator(".curation-recommended-combination").waitFor();
  await page.screenshot({path:out+"/initial.png"});
 }finally{await browser.close();}
 await fs.writeFile(out+"/result.json",JSON.stringify({id,threadId:completed.thread.id,model:completed.response.modelKey,body:completed.response.body,combination:combo},null,2));
 console.log("INITIAL_COMBINATION_PASS",id,"one initial request; automatic combination reply; representatives match; sidebar section");
 await api.dispose();
})().catch(e=>{console.error(e);process.exitCode=1;});
