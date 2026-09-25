// Firefox component integration: real React/Vite rendering with controlled API observations.
const assert = require("node:assert/strict");
const fs = require("node:fs/promises");
const path = require("node:path");
const { spawn } = require("node:child_process");
const { firefox } = require("playwright");
const root = path.resolve(__dirname,"..");
const fixture = path.join(root,".process-e2e.html");
const base = "http://127.0.0.1:18137";
(async()=>{
 let server,browser;
 try {
  await fs.writeFile(fixture,`<!doctype html><html><head><meta name="viewport" content="width=device-width, initial-scale=1"></head><body><main id="root"></main><script type="module">
  import React from 'react';
  import {createRoot} from 'react-dom/client';
  import {LocaleProvider} from '/src/shared/i18n/index.ts';
  import {OrderProcessProgress} from '/src/products/ordering/iface/OrderProcessProgress.tsx';
  import '/src/styles.css';
  createRoot(document.getElementById('root')).render(React.createElement(LocaleProvider,null,React.createElement(OrderProcessProgress,{orderId:'order-test',shops:[{id:'mo-test',shopDomain:'example.test'}]})));
  </script></body></html>`);
  server=spawn(process.execPath,[path.join(root,"node_modules/vite/bin/vite.js"),"--host","127.0.0.1","--port","18137","--strictPort"],{cwd:root,stdio:"ignore"});
  for(let n=0;n<100;n++){try{if((await fetch(base)).ok)break;}catch{}await new Promise(r=>setTimeout(r,100));}
  browser=await firefox.launch({headless:true});
  for(const locale of ["en-US","ko-KR"]){
   const context=await browser.newContext({viewport:{width:320,height:800}});
   await context.addCookies([{name:"vt_locale_choice",value:locale,url:base}]);
   const page=await context.newPage();const errors=[];page.on("pageerror",e=>errors.push(e.message));let calls=0;
   await page.route("**/api/v1/agencyOrder/order-test/process-requests",route=>{
    calls++;
    const stages=[
     ["PURCHASE","WAITING","MONEY_GATE_CLOSED"],
     ["PURCHASE","WAITING","PAYMENT_OUTCOME_UNKNOWN"],
     ["PURCHASE","WAITING","MERCHANT_RESULT_REQUIRED"],
     ["PURCHASE","COMPLETED","MERCHANT_PLACED"],
     ["CANCEL","WAITING","CANCELLATION_CONFIRMED_REFUND_PENDING"],
     ["CANCEL","COMPLETED","CANCELLED_DELIVERY_REQUIRES_REVIEW"],
    ];
    const [kind,outcome,reasonCode]=stages[Math.min(calls-1,stages.length-1)];
    const receipt={schemaVersion:"vitlane.order-process-receipt.v1",agencyOrderId:"order-test",requestId:kind==="CANCEL"?"cancel-test":"request-test",flowId:kind==="CANCEL"?"cancel-flow":"flow-test",merchantOrderId:"mo-test",kind,outcome,guidance:{reasonCode,customerAction:reasonCode==="CANCELLED_DELIVERY_REQUIRES_REVIEW"?"CONTACT_SUPPORT":"WAIT",operatorAction:"WAIT"}};
    return route.fulfill({json:{schemaVersion:"vitlane.order-process-requests.v1",requests:[receipt,{...receipt,requestId:"alias"}]}});
   });
   await page.goto(base+"/.process-e2e.html");
   await page.getByText(locale==="en-US"?"Payment processing is paused":"결제 처리가 일시 중지되었습니다",{exact:false}).waitFor();
   await page.getByText(locale==="en-US"?"The payment result is being confirmed":"결제 결과를 확인하고 있습니다",{exact:false}).waitFor();
   await page.evaluate(()=>{document.documentElement.style.fontSize="200%"});
   await page.getByText(locale==="en-US"?"The operator is confirming":"담당자가 판매처 구매 결과를 확인",{exact:false}).waitFor({timeout:12000});
   assert.equal(await page.locator("li").count(),1,"alias receipts share one visible flow");
   await page.getByText(locale==="en-US"?"The merchant purchase result is confirmed":"판매처 구매 결과가 확인되었습니다",{exact:false}).waitFor({timeout:12000});
   await page.getByText(locale==="en-US"?"Payment return is still pending":"결제금 반환은 아직 진행 중입니다",{exact:false}).waitFor({timeout:12000});
   await page.getByText(locale==="en-US"?"Payment was returned, but delivery was reported":"결제금이 반환되었으나 배송이 보고되었습니다",{exact:false}).waitFor({timeout:12000});
   assert.ok(calls>=6,"progress must refresh after admission");
   assert.equal(await page.locator("body").innerText().then(s=>s.includes("ACTIVATING_FUNDING")),false);
   assert.deepEqual(errors,[]);
   const width=await page.evaluate(()=>({scroll:document.documentElement.scrollWidth,client:document.documentElement.clientWidth}));assert.ok(width.scroll<=width.client+1,JSON.stringify(width));
   console.log(`${locale}: paused → unknown → merchant result waiting → confirmed → refund waiting → late delivery guidance; 320px and 200% text passed`);
   await context.close();
  }
 } finally {if(browser)await browser.close();if(server)server.kill();await fs.rm(fixture,{force:true});}
})().catch(e=>{console.error(e);process.exitCode=1});
