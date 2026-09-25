// Review-server smoke: start with an empty browser, then sign in through the visible UI.
// No injected cookies/API login and no profile reset.
const assert = require("node:assert/strict");
const { firefox } = require("playwright");
const base = process.env.E2E_BASE_URL || "http://127.0.0.1:18100";
const id = process.env.E2E_CURATION_ID || "b1b45a50-9e1b-4b5c-97db-e7f26e6fe3a8";
(async () => {
 const browser = await firefox.launch();
 try {
  const context = await browser.newContext({viewport:{width:1440,height:1000}});
  const page = await context.newPage(), writes = [], errors = [];
  page.on("request", r => {if(r.method()!=="GET")writes.push(new URL(r.url()).pathname);});
  page.on("pageerror", e => errors.push(e.message));
  await page.goto(base+"/login?returnTo="+encodeURIComponent("/curations/"+id));
  const profile=page.locator(".catalog-ui-login__profile").filter({has:page.getByRole("heading",{name:/빈 일반 사용자|Empty customer|Empty user/})});
  await profile.waitFor({state:"attached"});
  if(!await profile.isVisible()) await page.getByText(/로컬 TEST 계정 선택|Choose a local TEST account/,{exact:true}).click();
  const response=page.waitForResponse(r=>r.url().endsWith("/api/v1/dev/auth/session")&&r.request().method()==="POST");
  await profile.getByRole("button",{name:/이 프로필로 시작|Use this profile/}).click();
  const signedIn=await response;assert.equal(signedIn.status(),201);
  assert.equal(signedIn.request().postDataJSON().profileKey,"empty-user");
  await page.waitForURL(url=>!url.pathname.startsWith("/login"));
  await page.goto(base+"/curations/"+id);
  await page.locator("[data-result-target]").first().waitFor({timeout:30000});
  const workspace=await context.request.get(base+"/api/v1/curations/"+id+"/workspace");
  assert.equal(workspace.status(),200);
  assert.equal((await workspace.json()).curation.id,id);
  assert.equal(writes.some(p=>p.endsWith("/reset")),false);
  assert.deepEqual(errors,[]);
  await page.screenshot({path:"/tmp/vitlane-step8/review-login-restored.png"});
  console.log("REVIEW_LOGIN_PASS: fresh browser -> visible local TEST profile -> existing curation; no reset");
 }finally{await browser.close();}
})().catch(e=>{console.error(e);process.exitCode=1;});
