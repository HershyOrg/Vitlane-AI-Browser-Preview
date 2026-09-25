const {firefox}=require('playwright');
const fs=require('node:fs/promises'),path=require('node:path'),assert=require('node:assert/strict');
const base=process.env.E2E_BASE_URL||'http://127.0.0.1:4178';
const entry=path.resolve(__dirname,'../.amazon-control-e2e.tsx');
const out=path.resolve(__dirname,'../../docs/design/evidence/amazon-operator-control');
const fixture=`import React from 'react';import {createRoot} from 'react-dom/client';import {LocaleProvider} from './src/shared/i18n';import {AmazonAPIUsage} from './src/products/curation/research/iface/AmazonAPIUsage';import {CurationSourceNotices} from './src/products/curation/iface/CurationDismissibleNotice';import './src/styles.css';import './src/product-shell.css';import './src/catalog-surfaces.css';createRoot(document.getElementById('root')).render(<LocaleProvider><main style={{padding:16,maxWidth:700,margin:'auto'}}><AmazonAPIUsage/><CurationSourceNotices scopeId="quota" coverage={[{source:'AMAZON',status:'SKIPPED',reasonCode:'AMAZON_QUOTA_EXHAUSTED',candidateCount:0}]}/></main></LocaleProvider>);`;
(async()=>{
 await fs.mkdir(out,{recursive:true});await fs.writeFile(entry,fixture);await fs.writeFile(entry.replace('.tsx','.html'),'<meta name="viewport" content="width=device-width, initial-scale=1"><div id="root"></div><script type="module" src="/.amazon-control-e2e.tsx"></script>');
 const b=await firefox.launch();const results=[];try{
 for(const locale of ['en-US','ko-KR'])for(const theme of ['light','dark']){
 const c=await b.newContext({viewport:{width:1280,height:1000}});await c.addInitScript(l=>localStorage.setItem('vitlane.locale.v1',l),locale);const p=await c.newPage();const errors=[];p.on('pageerror',e=>errors.push(e.message));
 let enabled=true,version=1;const writes=[];
 const usage=()=>({schemaVersion:'vitlane.catalog-api-usage.v3',source:'AMAZON',configured:true,enabled,mode:enabled?'LIVE':'DISABLED',control:{enabled,version,updatedAt:'2026-09-11T00:00:00Z'},quota:{limit:100,used:100,remaining:0,observedAt:'2026-09-11T00:00:00Z',resetAt:'2026-10-01T00:00:00Z'},estimatedRemaining:0,attemptsSinceObservation:0,requests24h:100,succeeded24h:100,failures24h:[{reasonCode:'AMAZON_QUOTA_EXHAUSTED',count:1}]});
 await p.route('**/api/**',async r=>{const u=new URL(r.request().url());if(!u.pathname.startsWith('/api/'))return r.continue();if(u.pathname.endsWith('/amazon/usage'))return r.fulfill({json:usage()});if(u.pathname.endsWith('/amazon/control')&&r.request().method()==='PUT'){const v=r.request().postDataJSON();assert.equal(v.expectedVersion,version);enabled=v.enabled;version++;writes.push(v);return r.fulfill({json:usage()})}throw Error('Unexpected request '+u.pathname)});
 await p.goto(base+'/.amazon-control-e2e.html',{waitUntil:'networkidle'});await p.evaluate(t=>document.documentElement.classList.toggle('dark',t==='dark'),theme);
 const control=p.getByRole('switch');await control.waitFor();assert.equal(await control.getAttribute('aria-checked'),'true');assert.equal(await p.locator('.curation-dismissible-notice').count(),0);
 for(const width of [1280,320])for(const scale of [100,200]){
 await p.setViewportSize({width,height:1000});await p.evaluate(s=>document.documentElement.style.fontSize=16*s/100+'px',scale);
 assert(await p.evaluate(()=>document.documentElement.scrollWidth<=innerWidth+1));assert(await control.evaluate(e=>e.getBoundingClientRect().height>=44));
 await control.focus();await p.keyboard.press('Space');await p.waitForFunction(()=>document.querySelector('[role=switch]').getAttribute('aria-checked')==='false');
 await p.reload({waitUntil:'networkidle'});await p.evaluate(({theme,scale})=>{document.documentElement.classList.toggle('dark',theme==='dark');document.documentElement.style.fontSize=16*scale/100+'px'}, {theme,scale});
 assert.equal(await control.getAttribute('aria-checked'),'false');await control.focus();await p.keyboard.press('Space');await p.waitForFunction(()=>document.querySelector('[role=switch]').getAttribute('aria-checked')==='true');
 if(locale==='ko-KR'&&scale===100)await p.screenshot({path:path.join(out,`${theme}-${width}.png`),fullPage:true});
 results.push({locale,theme,width,scale});
 }
 assert.equal(errors.length,0,JSON.stringify(errors));assert.equal(writes.length,8);await c.close();
 }
 await fs.writeFile(path.join(out,'matrix.json'),JSON.stringify({results,mode:'ISOLATED_UI_FIXTURE',liveProviderCalls:0},null,2));console.log('PASS '+results.length+' locale/theme/viewport/text-size cases; switch keyboard/save/reload and no customer quota banner');
 }finally{await b.close();await fs.rm(entry,{force:true});await fs.rm(entry.replace('.tsx','.html'),{force:true})}
})().catch(e=>{console.error(e);process.exitCode=1});
