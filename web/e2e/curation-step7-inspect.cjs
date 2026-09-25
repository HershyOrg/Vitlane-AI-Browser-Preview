const {firefox}=require('playwright');const fs=require('fs/promises');
(async()=>{const browser=await firefox.launch();const context=await browser.newContext({viewport:{width:1440,height:1000}});const p=await context.newPage();const base='http://127.0.0.1:18117';
p.on('pageerror',e=>console.log('PAGE ERROR',e.message));
const r=await context.request.post(base+'/api/v1/dev/auth/session',{data:{profileKey:'multi-product'}});console.log('login',r.status());
await p.goto(base+'/curations/e5700000-0000-4000-8000-000000000002');await p.waitForTimeout(2500);
await fs.mkdir('/tmp/vitlane-step7-evidence',{recursive:true});await p.screenshot({path:'/tmp/vitlane-step7-evidence/proposal-desktop.png',fullPage:true});
console.log((await p.locator('main').first().innerText()).slice(-6500));await context.storageState({path:'/tmp/vitlane-step7-session.json'});await browser.close()})().catch(e=>{console.error(e);process.exit(1)});
