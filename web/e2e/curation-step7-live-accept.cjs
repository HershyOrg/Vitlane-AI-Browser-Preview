const {firefox}=require('playwright');const fs=require('fs/promises');const assert=require('node:assert/strict');const {execFileSync}=require('node:child_process');
const base='http://127.0.0.1:18117',cid='e5700000-0000-4000-8000-000000000002',out='/tmp/vitlane-step7-evidence';
function sql(query){return execFileSync('docker',['exec','vitlane-local-review-postgres','psql','-v','ON_ERROR_STOP=1','-U','vitlane','-d','vitlane_curation_step7','-c',query],{encoding:'utf8'});}
(async()=>{const b=await firefox.launch();const c=await b.newContext({storageState:'/tmp/vitlane-step7-session.json',viewport:{width:1440,height:1000}});const p=await c.newPage();p.setDefaultTimeout(20000);const errors=[];p.on('pageerror',e=>errors.push(e.message));try{
const pref=await(await c.request.get(base+'/api/v1/me/preferences')).json();
assert.equal((await c.request.patch(base+'/api/v1/me/preferences',{data:{schemaVersion:'vitlane.user-preferences.v1',expectedVersion:pref.preferences.version,uiLocale:'ko-KR',preferredCurrency:'KRW',researchCountry:'KR'}})).status(),200);
await p.goto(base+'/curations/'+cid);await p.getByRole('button',{name:'수락',exact:true}).waitFor();
await p.screenshot({path:out+'/proposal-ko.png',fullPage:true});
const accept=p.waitForResponse(r=>r.url().includes('/follow-ups/')&&r.request().method()==='POST');
await p.getByRole('button',{name:'수락',exact:true}).click();const accepted=await accept;console.log('ACCEPT',accepted.status(),await accepted.text());assert.equal(accepted.status(),200);
await p.getByRole('button',{name:'지켜보는 조건 1개',exact:true}).waitFor();
await p.getByRole('button',{name:'지켜보는 조건 1개',exact:true}).click();await p.screenshot({path:out+'/subscription-ko.png'});await p.keyboard.press('Escape');
await p.goto(base+'/'); // Let this curation receive its first product while not visible.
console.log(sql("DELETE FROM research_feed_products; UPDATE research_feed_leases SET next_at=now() WHERE name IN ('TELEGRAM_JIRUM','classify');"));
console.log('PASS accepted exact subscription; waiting for live feed, classification and match');
await fs.writeFile(out+'/accepted.json',JSON.stringify({cid,errors}));assert.deepEqual(errors,[]);
}finally{await b.close()}})().catch(e=>{console.error(e);process.exit(1)});
