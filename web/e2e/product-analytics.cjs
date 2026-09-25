// Firefox/WebKit. Google endpoints are intercepted; no real analytics traffic.
const assert = require("node:assert/strict");
const fs = require("node:fs");
const { firefox, webkit } = require("playwright");
const base = process.env.E2E_BASE_URL || "http://127.0.0.1:18087";
const evidence = process.env.ANALYTICS_EVIDENCE_DIR || "/tmp/vitlane-product-analytics";
const engine = process.env.ANALYTICS_BROWSER === "webkit" ? webkit : firefox;
(async () => {
  fs.mkdirSync(evidence, { recursive: true });
  const browser = await engine.launch({ headless: true });
  const results = [];
  try {
    for (const locale of ["ko-KR", "en-US"]) for (const width of [1440, 320]) {
      const context = await browser.newContext({ locale, viewport: { width, height:900 } });
      await context.addCookies([{name:"vt_locale_choice",value:locale,url:base}]);
      const external = [], errors = [];
      await context.route("**/api/v1/analytics/config", route => route.fulfill({json:{schemaVersion:"vitlane.analytics-config.v1",mode:"ga4",measurementId:"G-TEST1234",release:"e2e"}}));
      // Account Last Select takes precedence over the browser cookie. Pin the
      // account locale in this presentation matrix without changing fixture data.
      await context.route("**/api/v1/me/preferences", async route => {
        if (route.request().method() !== "GET") return route.continue();
        const response = await route.fetch(); const value = await response.json();
        assert(response.ok() && value.preferences && value.effective, "real account preference response");
        await route.fulfill({response,json:{...value,preferences:{...value.preferences,uiLocale:locale},effective:{...value.effective,uiLocale:locale}}});
      });
      await context.route("**/api/v1/me", async route => {
        const response = await route.fetch(); const value = await response.json();
        if(value.user) value.user.analyticsUserId = "a".repeat(64);
        await route.fulfill({response,json:value});
      });
      await context.route("https://www.googletagmanager.com/**", async route => {
        external.push({kind:"script"});
        await route.fulfill({contentType:"text/javascript",body:`
          const process = args => {
            if (args[0] === "get") args[3](args[2] === "client_id" ? "123.456" : "123456");
            if (args[0] === "event" && !window["ga-disable-G-TEST1234"]) fetch("https://www.google-analytics.com/g/collect", {method:"POST",body:JSON.stringify({name:args[1],params:args[2]})});
          };
          for (const args of window.dataLayer) process(args);
          window.gtag = function(){ process(arguments); };
        `});
      });
      await context.route("https://*.google-analytics.com/**", async route => {external.push({kind:"event",payload:JSON.parse(route.request().postData()||"{}")});await route.fulfill({status:204,headers:{"Access-Control-Allow-Origin":"*"}});});
      await context.route(/cloudflareinsights\.com/, route => {external.push({kind:"legacy"});return route.abort();});
      // Dedicated local review user, no production account or payment.
      const login = await context.request.post(base+"/api/v1/dev/auth/session",{data:{profileKey:"multi-product"}});
      if (!login.ok()) { // Use the stable non-operator fixture if profile names evolve.
        const fallback=await context.request.post(base+"/api/v1/dev/auth/session",{data:{userId:"e5000000-0000-4000-8000-000000000001"}});
        assert(fallback.ok());
      }
      const page = await context.newPage();
      page.on("pageerror", error => errors.push(error.message));
      await page.goto(base+"/curations/e5100000-0000-4000-8000-000000000002?email=private@example.com",{waitUntil:"domcontentloaded"});
      const refuse=locale==="ko-KR"?"동의하지 않기":"Decline";
      const allow=locale==="ko-KR"?"허용하기":"Allow";
      const settings=locale==="ko-KR"?"정보 제공 설정":"Usage information preferences";
      await page.getByRole("button",{name:refuse,exact:true}).waitFor();
      await page.locator(".shell-sidebar-profile__trigger").waitFor({state:"attached"});
      await page.locator("textarea").first().waitFor();
      await page.evaluate(() => document.fonts.ready);
      await page.waitForFunction(expected => document.documentElement.lang === expected, locale === "ko-KR" ? "ko" : "en");
      assert.equal(external.length,0,"no analytics before consent");
      const allowButton = page.getByRole("button",{name:allow,exact:true});
      assert(await allowButton.evaluate(button => button.classList.contains("vt-button--primary")),"allow uses brand emphasis");
      assert.notEqual(await allowButton.evaluate(button => getComputedStyle(button).backgroundColor),
        await page.getByRole("button",{name:refuse,exact:true}).evaluate(button => getComputedStyle(button).backgroundColor));
      assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
      if (width === 1440) assert.equal(await page.locator(".analytics-notice").evaluate(node=>getComputedStyle(node).fontSize),"16px","desktop body text is readable");
      await page.screenshot({path:evidence+"/"+locale+"-"+width+"-consent-default.png",fullPage:true});
      await page.evaluate(()=>{document.documentElement.style.fontSize="200%";});
      assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false,"200% text fits");
      const noticeBounds = await page.locator(".analytics-notice").boundingBox();
      const copyBounds = await page.locator(".analytics-notice__copy").boundingBox();
      assert(copyBounds.y >= noticeBounds.y && copyBounds.y + copyBounds.height <= noticeBounds.y + noticeBounds.height,"scrolling copy stays inside the banner");
      if (width === 320) {
        const actionsBounds = await page.locator(".analytics-notice__actions").boundingBox();
        assert(copyBounds.y + copyBounds.height <= actionsBounds.y,"mobile text never overlaps the choices");
      }
      const close = locale === "ko-KR" ? "닫기" : "Close";
      for (const name of [refuse,allow,close]) {
        const bounds = await page.getByRole("button",{name,exact:true}).boundingBox();
        assert(bounds.y >= noticeBounds.y && bounds.y + bounds.height <= noticeBounds.y + noticeBounds.height + 1,"both choices remain fully visible at 200%");
      }
      await page.screenshot({path:evidence+"/"+locale+"-"+width+"-consent.png",fullPage:true});
      await page.getByRole("button",{name:close,exact:true}).click();
      assert.equal(await page.locator(".analytics-notice").count(),0,"X dismisses the first notice");
      assert.equal((await context.cookies()).find(cookie=>cookie.name==="vt_analytics"),undefined,"X never decides consent");
      assert.equal(external.length,0,"X sends no analytics");
      const reopen = page.locator(".shell-sidebar-profile__trigger");
      if (!(await reopen.isVisible())) await page.locator(".shell-mobile-sidebar-trigger").click();
      await reopen.click();
      await page.getByRole("menuitem",{name:settings,exact:true}).click();
      await page.getByRole("button",{name:refuse,exact:true}).click();
      assert.equal(external.length,0,"refusal must send no ping");
      assert((await context.request.get(base+"/api/v1/curations")).ok(),"refused user can use service");
      await page.evaluate(()=>{document.documentElement.style.fontSize="";});
      // Open preferences from the existing user menu (mobile sidebar first).
      const trigger=page.locator(".shell-sidebar-profile__trigger");
      if (!(await trigger.isVisible())) await page.locator(".shell-mobile-sidebar-trigger").click();
      await trigger.click();
      await page.getByRole("menuitem",{name:settings,exact:true}).click();
      const draft = page.locator("textarea").first();
      await draft.fill("analytics preference must preserve this draft");
      await page.getByRole("button",{name:allow,exact:true}).click();
      await page.waitForFunction(()=>!!window.gtag);
      for (let i=0;i<30&&!external.some(e=>e.kind==="event"&&e.payload.name==="page_view");i++) await page.waitForTimeout(100);
      assert.equal(await draft.inputValue(), "analytics preference must preserve this draft", "consent must not remount the workspace");
      assert(external.some(e=>e.kind==="script"),"GA loads after opt-in");
      assert(external.some(e=>e.kind==="event"&&e.payload.name==="page_view"), JSON.stringify({external,errors,queued:await page.evaluate(()=>JSON.stringify(window.dataLayer))}));
      assert(!JSON.stringify(external).match(/private@example|e5100000|\?email/),"no raw identifiers or query");
      if (!(await trigger.isVisible())) await page.locator(".shell-mobile-sidebar-trigger").click();
      await trigger.click();await page.getByRole("menuitem",{name:settings,exact:true}).click();
      await page.getByRole("button",{name:refuse,exact:true}).click();
      const before=external.length;
      const nextPage = await context.newPage();
      nextPage.on("pageerror", error => errors.push(error.message));
      await nextPage.goto(page.url(), {waitUntil:"domcontentloaded"});
      const nextTrigger = nextPage.locator(".shell-sidebar-profile__trigger");
      await nextTrigger.waitFor({state:"attached"});
      if (!(await nextTrigger.isVisible())) await nextPage.locator(".shell-mobile-sidebar-trigger").click();
      await nextTrigger.click();
      // This entry appears only after analytics config has loaded. Background
      // polling and unrelated images must not define analytics readiness.
      await nextPage.getByRole("menuitem",{name:settings,exact:true}).waitFor();
      assert.equal(external.length,before,"new page after withdrawal must not load analytics");
      assert.equal(await nextPage.locator(".analytics-notice").count(),0,"no repeat notice within 24 hours");
      // Simulate the next visit after 24 hours without changing the saved refusal.
      await context.addCookies([{name:"vt_analytics_notice",value:"v1."+String(Date.now()-24*60*60*1000-1000),url:base}]);
      const tomorrowPage = await context.newPage();
      tomorrowPage.on("pageerror", error => errors.push(error.message));
      await tomorrowPage.goto(page.url(),{waitUntil:"domcontentloaded"});
      await tomorrowPage.getByRole("button",{name:refuse,exact:true}).waitFor();
      assert.equal((await context.cookies()).find(cookie=>cookie.name==="vt_analytics")?.value,"v1.denied");
      assert.equal(external.length,before,"daily reminder never grants consent or sends analytics");
      await tomorrowPage.getByRole("button",{name:close,exact:true}).click();
      assert.equal((await context.cookies()).find(cookie=>cookie.name==="vt_analytics")?.value,"v1.denied","X preserves existing refusal");
      assert.equal(await tomorrowPage.locator(".analytics-notice").count(),0);
      assert.equal(errors.length,0,JSON.stringify(errors));
      results.push({locale,width,zoom:"200%",consent:"PASS",dailyReminder:"PASS",allowEmphasis:"PASS",businessAccess:"PASS",privacy:"PASS"});
      await context.close();
    }
    fs.writeFileSync(evidence+"/results-"+(process.env.ANALYTICS_BROWSER||"firefox")+".json",JSON.stringify(results,null,2));
    console.log(JSON.stringify(results));
  } finally { await browser.close(); }
})().catch(error=>{console.error(error);process.exitCode=1;});
