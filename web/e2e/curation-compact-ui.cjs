// Presentation matrix only. Provider/controller persistence is covered by
// amazon-source.cjs and the Research component integration tests.
// Purchase controls follow the approved Step 2 32px minimum; other targets retain 44px.
const {firefox}=require('playwright');
const fs=require('node:fs/promises'),path=require('node:path'),assert=require('node:assert/strict');
const base=process.env.E2E_BASE_URL||'http://127.0.0.1:4178';
const entry=path.resolve(__dirname,'../.compact-ui-e2e.tsx');
const out=process.env.E2E_EVIDENCE_DIR||path.resolve(__dirname,'../../docs/design/evidence/curation-secondary-outline');
const fixture=`import React,{useState} from 'react';import {createRoot} from 'react-dom/client';
import {LocaleProvider,useLocale} from './src/shared/i18n';
import {CurationCandidateCard} from './src/products/curation/iface/CurationCandidateCard';
import {CandidateDetailDialog} from './src/products/curation/iface/CandidateDetailDialog';
import {CurationAgentRunningPanel} from './src/products/curation/iface/CurationAgentWork';
import {CurationConversationBubble} from './src/products/curation/iface/CurationWorkspace';
import {CurationSourceNotices} from './src/products/curation/iface/CurationDismissibleNotice';
import 'pretendard/dist/web/variable/pretendardvariable-dynamic-subset.css';import '@fontsource/ibm-plex-mono/latin-400.css';import './src/styles.css';import './src/product-shell.css';import './src/order-operations.css';import './src/catalog-surfaces.css';import './src/account-surfaces.css';import './src/products/curation/iface/catalog-curation-research.css';
const job={jobId:'fixture-job',actionId:'fixture-action',targetKind:'RESEARCH_ROUND',targetId:'round',provider:'MANAGED',status:'RUNNING',retryable:false,attempt:2,steps:[{id:'s1',kind:'INTERPRETING',status:'SUCCEEDED',startedAt:'2026-09-10T00:00:00Z'},{id:'s2',kind:'RANKING',status:'RUNNING',startedAt:'2026-09-10T00:00:01Z'}]};
const options=[{id:'black',title:'Headphones',attributes:[{name:'Color',value:'Jet Black'}],price:{kind:'OBSERVED',amountMinor:9999,currency:'USD'},availability:'AVAILABLE',selectable:true},{id:'blue',title:'Headphones',attributes:[{name:'Color',value:'Blue'}],price:{kind:'UNKNOWN'},availability:'UNKNOWN',selectable:true}];
function App(){const {l,locale}=useLocale();const [source,setSource]=useState(null),[selected,setSelected]=useState(options[0]),[working,setWorking]=useState(true);const [interaction,setInteraction]=useState({pinned:true,sentiment:'LIKE'});
const candidate=(s)=>({id:s,source:s,title:'Wireless Headphones with Active Noise Cancellation and Long Battery Life',sellerName:'Fixture seller',price:options[0].price,selectedVariant:options[0],recommendation:'Original catalog observation. Full text is preserved here.',features:['Wireless connection'],specifications:['USB-C'],disclosures:[],purchaseRoute:s==='AMAZON'?'EXTERNAL':'VITLANE_CHECKOUT',observedAt:'2026-09-10T00:00:00Z'});
return <main className="catalog-ui-curation" style={{padding:16,maxWidth:1200,margin:'auto'}}><CurationConversationBubble locale={locale} message={{id:'user',role:'USER',title:'Me',body:'전자기기를 더 비교해 줘. Please compare the battery life and fit.',createdAt:'2026-09-10T00:00:00Z',state:'ACTIVE',turnId:'turn'}}/>
{working&&<CurationAgentRunningPanel job={job} onWorkChanged={()=>setWorking(false)}/>}
<CurationSourceNotices scopeId="fixture-round" coverage={[{source:"AMAZON",status:"FAILED",reasonCode:"TIMEOUT",candidateCount:0}]}/><div className="phase8-candidate-rail__viewport"><div className="catalog-ui-candidate-grid" style={{display:'grid',gridAutoFlow:'column',gridAutoColumns:254,gridTemplateRows:'auto',overflowX:'auto',gap:16,paddingBlock:16}}>{['SHOPIFY','AMAZON'].map(s=><CurationCandidateCard key={s} candidate={candidate(s)} signals={{pinned:true,liked:true}} onOpen={()=>{setSelected(options[0]);setSource(s)}} primaryAction={{label:s==='AMAZON'?l('External purchase','외부 구매'):l('Add to cart','장바구니 담기')}} secondaryAction={s==='AMAZON'?{label:l('Purchase check','구매 체크')}:undefined}/>)}</div></div>
{source&&<CandidateDetailDialog candidate={candidate(source)} selected={selected} options={options} interaction={interaction} onReaction={v=>setInteraction(old=>({...old,...v}))} onSelect={id=>setSelected(options.find(o=>o.id===id))} onSave={()=>setSource(null)} onReload={()=>{}} pageNumber={1} purchase={source==='AMAZON'?{kind:'EXTERNAL',href:'https://www.amazon.com/dp/B012345678',checked:false,disabled:false,onCheck:()=>{}}:{kind:'CART',label:l('Add to cart','장바구니 담기'),disabled:false,onAction:()=>{}}} onClose={()=>setSource(null)}/>}</main>};
createRoot(document.getElementById('root')).render(<LocaleProvider><App/></LocaleProvider>);`;
function luminance(rgb){const values=rgb.match(/[\d.]+/g).slice(0,3).map(Number).map(v=>v/255).map(v=>v<=0.04045?v/12.92:((v+0.055)/1.055)**2.4);return values[0]*0.2126+values[1]*0.7152+values[2]*0.0722;}
function contrast(a,b){const x=luminance(a),y=luminance(b);return (Math.max(x,y)+0.05)/(Math.min(x,y)+0.05);}
async function checkSecondary(button, page) {
 const check = async () => {
  const style = await button.evaluate(el => {
   const style = getComputedStyle(el);
   const canvas=document.createElement('canvas');canvas.width=canvas.height=1;const ctx=canvas.getContext('2d');
   ctx.fillStyle=style.color;ctx.fillRect(0,0,1,1);const rgb=[...ctx.getImageData(0,0,1,1).data].slice(0,3);
   let parent=el.parentElement;while(parent&&getComputedStyle(parent).backgroundColor==='rgba(0, 0, 0, 0)')parent=parent.parentElement;
   return {background:style.backgroundColor,color:style.color,rgb,surface:getComputedStyle(parent).backgroundColor,border:style.borderTopColor,borderWidth:parseFloat(style.borderTopWidth),borderStyle:style.borderTopStyle};
  });
  assert.equal(style.background,'rgba(0, 0, 0, 0)',JSON.stringify(style));
  assert.equal(style.color,style.border,JSON.stringify(style));
  assert(style.borderWidth>=1&&style.borderStyle==='solid',JSON.stringify(style));
  assert(style.rgb[2]-style.rgb[0]>=50&&style.rgb[2]-style.rgb[1]>=25,'Secondary must be visibly blue: '+JSON.stringify(style));
  assert(contrast('rgb('+style.rgb.join(',')+')',style.surface)>=4.5,JSON.stringify(style));
 };
 await check();
 await button.hover();
 await page.waitForTimeout(180);
 await check();
 await button.focus();
 await check();
 const previous=await page.evaluate(()=>{const root=document.documentElement;const previous=root.dataset.accent;root.dataset.accent='neutral';return previous});
 await page.waitForTimeout(180);
 await check();
 await page.evaluate(previous=>document.documentElement.dataset.accent=previous,previous);
}
async function checkTertiary(button, page) {
 assert(await button.evaluate(el=>el.classList.contains('vt-button--tertiary')));
 const style = await button.evaluate(el => {
  const style=getComputedStyle(el);
  const parent=getComputedStyle(el.closest('.vt-candidate-card, .candidate-detail'));
  return {background:style.backgroundColor,color:style.color,surface:parent.backgroundColor};
 });
 assert.notEqual(style.background,'rgba(0, 0, 0, 0)');
 const rgb=style.background.match(/[\d.]+/g).slice(0,3).map(Number);
 assert(Math.max(...rgb)-Math.min(...rgb)<=10,JSON.stringify(style));
 assert(contrast(style.color,style.background)>=4.5,JSON.stringify(style));
 await button.hover();
 await page.waitForTimeout(180);
 const hovered=await button.evaluate(el=>({border:getComputedStyle(el).borderTopColor,background:getComputedStyle(el).backgroundColor}));
 assert.notEqual(hovered.border,'rgba(0, 0, 0, 0)');
 assert.equal(hovered.background,style.background);
}
async function run(){await fs.mkdir(out,{recursive:true});await fs.writeFile(entry,fixture);await fs.writeFile(entry.replace('.tsx','.html'),'<meta name="viewport" content="width=device-width,initial-scale=1"><div id="root"></div><script type="module" src="/.compact-ui-e2e.tsx"></script>');
const b=await firefox.launch();const results=[],errors=[],requests=[],progress=[];try{
 for(const locale of ['en-US','ko-KR'])for(const theme of ['light','dark']){
  const c=await b.newContext({viewport:{width:1440,height:1050},reducedMotion:'reduce'});await c.addInitScript(l=>localStorage.setItem('vitlane.locale.v2',l),locale);const p=await c.newPage();p.setDefaultTimeout(10000);p.on('pageerror',e=>errors.push(e.message));
  await p.route('**/api/**',r=>{const u=new URL(r.request().url());if(!u.pathname.startsWith('/api/'))return r.continue();requests.push({path:u.pathname,method:r.request().method()});if(u.pathname.includes('cancel'))return r.fulfill({json:{cancelledJobs:1}});return r.abort()});
  await p.goto(base+'/.compact-ui-e2e.html',{waitUntil:'networkidle'});await p.locator('.curation-step-activity').waitFor().catch(async error=>{console.error({errors,body:(await p.locator('body').innerText()).slice(0,1500)});throw error});await p.evaluate(theme=>{document.documentElement.classList.toggle('dark',theme==='dark');document.documentElement.dataset.accent='blue'},theme);
  const summary=p.locator('.curation-step-activity');assert(await p.locator('.curation-step-list').isVisible());assert.equal(await summary.locator('[aria-expanded]').count(),0);
  progress.push({locale,theme,...await p.locator('.curation-action-activity').evaluate(e=>({height:e.getBoundingClientRect().height,background:getComputedStyle(e).backgroundColor}))});
  assert.equal(await summary.locator('[role="status"]').count(),1);assert.equal(await summary.locator('[role="status"]').getAttribute('aria-atomic'),'true');
  if(locale==='ko-KR'){await p.screenshot({path:path.join(out,'fixture-'+theme+'.png')});await p.locator('.curation-action-activity').screenshot({path:path.join(out,'progress-'+theme+'.png')});}
  for(const width of [1440,768,390,320])for(const scale of [100,200]){
   await p.setViewportSize({width,height:1050});await p.evaluate(scale=>document.documentElement.style.fontSize=16*scale/100+'px',scale);await p.waitForTimeout(180);
   const pageWidth=await p.evaluate(()=>({width:innerWidth,scroll:document.documentElement.scrollWidth}));assert(pageWidth.scroll<=width+1,JSON.stringify({locale,theme,width,scale,pageWidth,overflow:await p.evaluate(()=>[...document.querySelectorAll('body *')].filter(e=>e.getBoundingClientRect().right>innerWidth+1).slice(0,12).map(e=>({tag:e.tagName,cls:e.className,width:e.getBoundingClientRect().width,display:getComputedStyle(e).display,min:getComputedStyle(e).minWidth}))) }));
   const bannerMetrics=await p.locator('.curation-dismissible-notice').evaluate(el=>{const b=el.querySelector('.vt-notice'),x=el.querySelector('button'),m=el.querySelector('.vt-notice__body'),r=el.getBoundingClientRect(),xr=x.getBoundingClientRect();return {border:getComputedStyle(b).borderLeftWidth,left:r.left,right:r.right,xLeft:xr.left,xRight:xr.right,xTop:xr.top,top:r.top,xWidth:xr.width,xHeight:xr.height,messageFirstLineCenter:m.getBoundingClientRect().top+parseFloat(getComputedStyle(el.querySelector('.vt-notice__message')).lineHeight)/2,messageRight:m.getBoundingClientRect().right,background:getComputedStyle(b).backgroundColor}});
   assert.equal(bannerMetrics.border,'0px');assert(bannerMetrics.xLeft>=bannerMetrics.messageRight&&bannerMetrics.xRight<=bannerMetrics.right&&bannerMetrics.xTop>=bannerMetrics.top&&Math.abs(bannerMetrics.xTop+bannerMetrics.xHeight/2-bannerMetrics.messageFirstLineCenter)<=1);assert(bannerMetrics.xWidth>=44&&bannerMetrics.xHeight>=44);assert.notEqual(bannerMetrics.background,'rgba(0, 0, 0, 0)');
   for(const source of ['SHOPIFY','AMAZON']){
    const card=p.locator('.curation-candidate-card[data-source="'+source+'"]');
    const cardMetrics=await card.evaluate(el=>({height:el.getBoundingClientRect().height,sourceStyle:{color:(()=>{const canvas=document.createElement('canvas');canvas.width=canvas.height=1;const ctx=canvas.getContext('2d');ctx.fillStyle=getComputedStyle(el.querySelector('.candidate-source-badge')).color;ctx.fillRect(0,0,1,1);return 'rgb('+[...ctx.getImageData(0,0,1,1).data].slice(0,3).join(',')+')'})(),background:getComputedStyle(el.querySelector('.candidate-source-badge')).backgroundColor,surface:(()=>{let node=el.querySelector('.candidate-source-badge');while(node){const value=getComputedStyle(node).backgroundColor;if(value!=='rgba(0, 0, 0, 0)'&&value!=='transparent')return value;node=node.parentElement}throw Error('Source background unresolved')})()},buttons:[...el.querySelectorAll('button')].map(b=>({width:b.getBoundingClientRect().width,height:b.getBoundingClientRect().height,compact:!!b.closest('.vt-candidate-card__primary-action')}))}));
    assert.equal(cardMetrics.sourceStyle.background,'rgba(0, 0, 0, 0)');const sourceContrast=contrast(cardMetrics.sourceStyle.color,cardMetrics.sourceStyle.surface);assert(sourceContrast>=4.5,JSON.stringify({source,theme,sourceContrast}));assert(cardMetrics.buttons.every(b=>b.width>=43&&b.height>=(b.compact?32:43)),JSON.stringify({locale,theme,width,scale,source,cardMetrics}));
    const copy=await card.evaluate(el=>{const title=el.querySelector('.vt-candidate-card__title'),seller=el.querySelector('.vt-candidate-card__meta span'),reason=el.querySelector('.vt-candidate-card__evidence');return {titleWeight:getComputedStyle(title).fontWeight,titleSize:parseFloat(getComputedStyle(title).fontSize),sellerSize:parseFloat(getComputedStyle(seller).fontSize),sellerTop:seller.getBoundingClientRect().top,titleTop:title.getBoundingClientRect().top,reasonHeight:reason.getBoundingClientRect().height,lineHeight:parseFloat(getComputedStyle(reason).lineHeight)}});
    assert.equal(copy.titleWeight,'400');assert(copy.sellerSize<copy.titleSize&&copy.sellerTop<copy.titleTop);assert(copy.reasonHeight>0&&copy.reasonHeight<=copy.lineHeight+1);
    assert(await card.locator('.vt-candidate-card__primary-action > button').first().evaluate((e,kind)=>e.classList.contains('vt-button--'+kind),source==='SHOPIFY'?'primary':'secondary'));
    if(source==='AMAZON'){await checkSecondary(card.locator('.vt-candidate-card__primary-action > button').first(),p);await checkTertiary(card.getByRole('button',{name:locale==='ko-KR'?'구매 체크':'Purchase check',exact:true}),p);}
    const opener=card.locator('.vt-candidate-card__hit-area');await opener.focus();await p.keyboard.press('Enter');const modal=p.locator('.candidate-detail');await modal.waitFor();
    const measure=()=>modal.evaluate(el=>({width:el.clientWidth,scroll:el.scrollWidth,left:el.getBoundingClientRect().left,right:el.getBoundingClientRect().right,overflow:[...el.querySelectorAll('*')].filter(e=>e.getBoundingClientRect().right>el.getBoundingClientRect().right+1).slice(0,10).map(e=>({tag:e.tagName,cls:e.className,text:e.textContent.slice(0,70),width:e.getBoundingClientRect().width})),buttons:[...el.querySelectorAll('button')].filter(b=>b.getClientRects().length).map(b=>({text:b.textContent,width:b.getBoundingClientRect().width,height:b.getBoundingClientRect().height,compact:!!b.closest('.catalog-ui-candidate-modal__footer-actions')}))}));
    const m=await measure();assert(m.scroll<=m.width+1&&m.left>=-1&&m.right<=width+1,JSON.stringify({locale,theme,width,scale,source,m}));assert(m.buttons.every(b=>b.width>=43&&b.height>=(b.compact?32:43)),JSON.stringify(m.buttons));
    assert.equal(await modal.locator('.vt-disclosure__trigger[aria-expanded="true"]').count(),4);
    const notes=modal.locator('.vt-disclosure__trigger').first();assert(Number(await notes.locator('span').evaluate(e=>getComputedStyle(e).fontWeight))>=700);assert(await modal.getByText('Original catalog observation. Full text is preserved here.').isVisible());await notes.click();assert(!(await modal.getByText('Original catalog observation. Full text is preserved here.').isVisible()));await notes.click();
    assert(await modal.getByText('Fixture seller',{exact:true}).isVisible());const expanded=await measure();assert(expanded.scroll<=expanded.width+1);
    const reactionColors=await modal.locator('[data-reaction] svg').evaluateAll(elements=>elements.map(e=>getComputedStyle(e).color));assert.equal(new Set(reactionColors).size,3);
    const purchaseButton=modal.getByRole('button',{name:source==='AMAZON'?(locale==='ko-KR'?'외부 구매':'External purchase'):(locale==='ko-KR'?'장바구니 담기':'Add to cart'),exact:true});assert(await purchaseButton.evaluate((e,kind)=>e.classList.contains('vt-button--'+kind),source==='SHOPIFY'?'primary':'secondary'));
    if(source==='AMAZON'){await checkSecondary(purchaseButton,p);await checkTertiary(modal.getByRole('button',{name:locale==='ko-KR'?'구매 체크':'Purchase check',exact:true}),p);}
    const close=modal.getByRole('button',{name:locale==='ko-KR'?'상품 상세 닫기':'Close product details'});await close.focus();await p.keyboard.press('Shift+Tab');assert(await p.evaluate(()=>!!document.activeElement.closest('[role="dialog"]')));await p.keyboard.press('Tab');assert(await close.evaluate(e=>e===document.activeElement));
    await modal.locator('[role="radio"]').first().focus();await p.keyboard.press('ArrowDown');assert.equal(await modal.locator('[role="radio"]').nth(1).getAttribute('aria-checked'),'true');
    if(locale==='ko-KR'&&width===320&&scale===200){await modal.locator('.catalog-ui-candidate-modal__footer').scrollIntoViewIfNeeded();await modal.screenshot({path:path.join(out,`mobile-${theme}-${source}.png`)});}
    if(locale==='ko-KR'&&width===1440&&scale===100)await modal.screenshot({path:path.join(out,`detail-${theme}-${source}.png`)});
    await p.keyboard.press('Escape');assert.equal(await modal.count(),0);assert(await opener.evaluate(e=>e===document.activeElement));results.push({locale,theme,width,scale,source,cardHeight:cardMetrics.height,sourceStyle:cardMetrics.sourceStyle,sourceContrast});
   }
  }
  await p.locator('.curation-dismissible-notice__close').click();assert.equal(await p.locator('.curation-dismissible-notice').count(),0);
  await p.locator('.curation-cancel-action').click();await summary.waitFor({state:'hidden'});await c.close();
 }
 assert.equal(errors.length,0,JSON.stringify(errors));assert.equal(requests.length,4);assert(requests.every(r=>r.method==='POST'&&r.path.includes('cancel')));await fs.writeFile(path.join(out,'matrix.json'),JSON.stringify({results,progress,errors,requests,mode:'ISOLATED_PRESENTATION_FIXTURE'},null,2));console.log('PASS '+results.length+' source/locale/theme/viewport/text-size checks; source/reaction colors, purchase emphasis with transparent Secondary normal/hover/focus and neutral Tertiary, seller/title/reason, expanded details, dismissible banner alignment, focus and zero live API calls');
 }finally{await b.close();await fs.rm(entry,{force:true});await fs.rm(entry.replace('.tsx','.html'),{force:true})}}
run().catch(e=>{console.error(e);process.exitCode=1});
