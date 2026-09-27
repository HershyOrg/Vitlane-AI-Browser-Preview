// Uses an installed Playwright/Chromium; all HTTP requests are fulfilled by local fixtures.
// VITLANE_PLAYWRIGHT_MODULE may point at an existing cached Playwright installation.
import assert from 'node:assert/strict';
import { readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { dirname, resolve } from 'node:path';
import { fileURLToPath } from 'node:url';

const require = createRequire(import.meta.url);
const { chromium } = require(process.env.VITLANE_PLAYWRIGHT_MODULE || 'playwright');
const directory = dirname(fileURLToPath(import.meta.url));
const source = readFileSync(resolve(directory, 'browser-page-agent.js'), 'utf8');
const origin = 'https://browser-fixture.example.com';
const productPng = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAUAAAADICAIAAAAWZq/8AAABvElEQVR42u3TQQ0AMAgAsTFlGME+OhDBi6SVcMlFVj/gpi8BGBgwMGBgMDBgYMDAgIHBwICBAQODgQEDAwYGDAwGBgwMGBgwMBgYMDBgYDAwYGDAwICBwcCAgQEDAwYGAwMGBgwMBgYMDBgYMDAYGDAwYGAwMGBgwMCAgcHAgIEBAwMGBgMDBgYMDAYGDAwYGDAwGBgwMGBgwMBgYMDAgIHBwICBAQMDBgYDAwYGDAwYGAwMGBgwMBgYMDBgYMDAYGDAwICBwcCAgQEDAwYGAwMGBgwMGBgMDBgYMDAYGDAwYGDAwGBgwMCAgQEDg4EBAwMGBgMDBgYMDBgYDAwYGDAwYGAwMGBgwMBgYMDAgIEBA4OBAQMDBgYDAwYGDAwYGAwMGBgwMGBgMDBgYMDAYGDAwICBAQODgQEDAwYGDAwGBgwMGBgMDBgYMDBgYDAwYGDAwGBgwMCAgQEDg4EBAwMGBgwMBgYMDBgYDAwYGDAwYGAwMGBgwMCAgcHAgIEBA4OBAQMDBgYMDAYGDAwYGDAwGBgwMGBgMDBgYMDAgIHBwICBAQODgQEDAwYGDAwGBgwMGBgwMBgYMDCwMdS4AxM7RBiMAAAAAElFTkSuQmCC', 'base64');

// Small fixture-only CommonJS loader for the React packages already installed in mobile.
// It runs the real controlled-input implementation without downloading a CDN runtime.
const moduleSources = {
  react: readFileSync(resolve(dirname(require.resolve('react')), 'cjs/react.production.js'), 'utf8'),
  'react-dom': readFileSync(resolve(dirname(require.resolve('react-dom')), 'cjs/react-dom.production.js'), 'utf8'),
  'react-dom/client': readFileSync(resolve(dirname(require.resolve('react-dom/client')), 'cjs/react-dom-client.production.js'), 'utf8'),
  scheduler: readFileSync(resolve(dirname(require.resolve('scheduler')), 'cjs/scheduler.production.js'), 'utf8'),
};
const reactBundle = `(() => {
  const factories = {${Object.entries(moduleSources).map(([id, contents]) => `${JSON.stringify(id)}: function(module,exports,require){${contents}\n}`).join(',')}};
  const cache = {};
  function require(id) { if (!cache[id]) { cache[id] = {exports:{}}; factories[id](cache[id],cache[id].exports,require); } return cache[id].exports; }
  const React = require('react');
  function Fixture() {
    const [draft,setDraft] = React.useState('');
    return React.createElement('section', {},
      React.createElement('label', {}, 'Public draft', React.createElement('textarea', {value:draft,onChange:event=>setDraft(event.target.value)})),
      React.createElement('label', {}, 'Locked draft', React.createElement('textarea', {value:'',onChange:()=>{}})),
      React.createElement('output', {'aria-label':'Draft preview'}, draft));
  }
  function HumanFixture() {
    const [recipient,setRecipient] = React.useState('');
    const [phone,setPhone] = React.useState('');
    const [email,setEmail] = React.useState('');
    return React.createElement('form', {action:'/next',onSubmit:event=>{event.preventDefault();window.fixtureSubmits=(window.fixtureSubmits||0)+1}},
      React.createElement('label', {}, 'Recipient name', React.createElement('input', {name:'recipient',value:recipient,onChange:event=>setRecipient(event.target.value)})),
      React.createElement('label', {}, 'Phone', React.createElement('input', {type:'tel',name:'phone',value:phone,onChange:event=>setPhone(event.target.value)})),
      React.createElement('label', {}, 'Email', React.createElement('input', {type:'email',name:'email',value:email,onChange:event=>setEmail(event.target.value)})),
      React.createElement('label', {}, 'Locked email', React.createElement('input', {type:'email',name:'locked-email',value:'',onChange:()=>{}})),
      React.createElement('output', {}, [recipient,phone,email].join(' / ')),
      React.createElement('button', {}, 'Submit'));
  }
  require('react-dom/client').createRoot(document.getElementById('react-root')).render(React.createElement(location.pathname==='/human-fields'?HumanFixture:Fixture));
})();`;

const shell = body => `<!doctype html><html><head><style>
  body{font:16px sans-serif;padding:20px} main{max-width:700px} input,textarea,select,button{margin:8px;padding:8px}
  label{display:block} #pane{height:150px;overflow-y:auto;border:1px solid #888} #pane p{height:45px}
  #overlay{position:fixed;inset:0;background:#fffa;z-index:999}
  .filters-toggle,.sort-toggle,.facet-title{cursor:pointer}.catalog-filters-drawer{position:fixed;inset:5%;background:white;z-index:1000;padding:20px}
  .modal[aria-hidden="true"]{display:none}
</style></head><body>${body}</body></html>`;
const pages = {
  '/catalog': shell(`<main><h1>Trail catalog</h1><article><h2>Trail Alpha</h2><img src="/trail-alpha.png" alt="Blue Trail Alpha shoes" width="320" height="200"><p>$90 · blue</p></article><form action="/search"><label>Search<input name="q" type="search"></label><button>Search catalog</button></form>
    <button id="more">Load more</button><section id="results"></section></main>
    <script>document.getElementById('more').onclick=()=>{const r=document.getElementById('results');r.setAttribute('aria-busy','true');setTimeout(()=>{r.innerHTML='<a target="_blank" href="/detail">Trail boots</a>';r.setAttribute('aria-busy','false')},250)};</script>`),
  '/search': shell('<main><h1>Search results</h1><a href="/detail">Trail boots</a></main>'),
  '/cards': shell(`<main><article id="alpha"><h2>Trail Alpha</h2><p>$90 · blue</p><button>Compare</button></article>
    <article id="beta"><h2>Trail Beta</h2><p>$120 · red</p><button>Compare</button></article></main>
    <script>for(const card of document.querySelectorAll('article'))card.querySelector('button').onclick=()=>document.body.dataset.choice=card.id;</script>`),
  '/modal': shell(`<main><h1>Catalog</h1><button id="background">Continue in background</button></main>
    <div role="dialog" aria-modal="true" aria-label="Delivery location"><p>Select a delivery location</p><button id="close">Close</button></div>
    <script>document.getElementById('close').onclick=event=>event.currentTarget.closest('[role=dialog]').remove()</script>`),
  '/modal-open': shell(`<main><h1>Ivy bomber jacket</h1><button id="size-guide">Size guide</button></main>
    <div id="guide" class="product-size-drawer" aria-label="Size guide" hidden style="position:fixed;inset:10%;background:white;z-index:1000"><p>Body measurements and size conversion</p><button id="guide-close">Close guide</button></div>
    <script>document.getElementById('size-guide').onclick=()=>document.getElementById('guide').hidden=false;
      document.getElementById('guide-close').onclick=()=>document.getElementById('guide').hidden=true;</script>`),
  '/filters': shell(`<main><h1>Man Outerwear Jackets</h1><div class="catalog-toolbar"><div class="filters-toggle">Filters</div><div class="sort-toggle">Sort By</div></div>
    <article><h2>Ivy Bomber Jacket</h2><p>Cashmere</p></article></main>
    <aside class="catalog-filters-drawer" aria-label="Product filters" hidden><button id="filter-close">Close</button>
      <section class="facet"><div class="facet-title">Material</div><div id="material-options" hidden>
        <label for="cashmere-choice">Cashmere<input id="cashmere-choice" type="checkbox" hidden></label>
        <label for="linen-choice">Linen<input id="linen-choice" type="checkbox" hidden></label></div></section>
      <button id="apply-filters">View results</button></aside>
    <script>document.querySelector('.filters-toggle').onclick=()=>document.querySelector('.catalog-filters-drawer').hidden=false;
      document.querySelector('.facet-title').onclick=()=>document.getElementById('material-options').hidden=false;
      document.getElementById('filter-close').onclick=()=>document.querySelector('.catalog-filters-drawer').hidden=true;</script>`),
  '/loro-filters': shell(`<main><h1>Outerwear Jackets</h1><button type="button" class="filters-button" data-toggle="modal" data-target="#refinements-modal">Filters</button>
    <section id="results"><article><h2>Ivy Bomber Jacket</h2><p>Cashmere</p></article></section></main>
    <div class="modal modal-right" id="refinements-modal" role="dialog" aria-hidden="true" style="position:fixed;inset:4%;background:white;z-index:1000">
      <button type="button" class="close" data-dismiss="modal" aria-label="Close">Close</button><h4>Filter</h4>
      <div class="refinements accordion" id="refinements-parent">
        <div class="card algolia-custom-refinement-eShopMaterial">
          <div class="card-header" id="heading-algolia-custom-refinement-eShopMaterial">
            <button type="button" class="toggle-element refinement-title title-algolia-custom-refinement-eShopMaterial" aria-expanded="false" aria-controls="algolia-custom-refinement-eShopMaterial-body">Materials</button>
          </div>
          <div id="algolia-custom-refinement-eShopMaterial-body" class="collapse collapse-product-details" aria-labelledby="heading-algolia-custom-refinement-eShopMaterial" hidden>
            <div id="algolia-custom-refinement-eShopMaterial"><a class="d-block refinement-link eShopMaterial" href="?refinementList%5BeShopMaterial%5D%5B0%5D=Cashmere" data-value="Cashmere">Cashmere</a>
              <a class="d-block refinement-link eShopMaterial" href="?refinementList%5BeShopMaterial%5D%5B0%5D=Linen" data-value="Linen">Linen</a></div>
          </div>
        </div>
      </div>
      <div class="modal-footer show-results-button-container"><button id="loro-view-results" type="button" data-dismiss="modal" aria-label="Close"><span id="button-stats">View 28 Products</span></button></div>
    </div>
    <script>const modal=document.getElementById('refinements-modal'),body=document.getElementById('algolia-custom-refinement-eShopMaterial-body'),title=document.querySelector('.refinement-title');
      document.querySelector('.filters-button').onclick=()=>{modal.setAttribute('aria-hidden','false')};
      title.onclick=()=>{const open=body.hidden;body.hidden=!open;title.setAttribute('aria-expanded',String(open))};
      for(const link of document.querySelectorAll('.refinement-link'))link.onclick=event=>{event.preventDefault();link.classList.toggle('active');document.querySelector('#results p').textContent=link.classList.contains('active')?'Cashmere selected':'Cashmere'};
      document.querySelector('.close').onclick=document.getElementById('loro-view-results').onclick=()=>modal.setAttribute('aria-hidden','true');</script>`),
  '/nested': shell(`<main><div style="height:400px"></div><section role="region" aria-label="Models" style="height:150px;overflow-y:auto">${Array.from({length:7},(_,i)=>`<p style="height:40px"><a href="/item/${i}">Item ${i}</a></p>`).join('')}</section></main>`),
  '/human-fields': shell(`<main><h1>Contact details</h1><div id="react-root"></div><section id="private-controls"><label>Password<input type="password"></label><label>Email verification<input autocomplete="one-time-code"></label><label>Card name<input autocomplete="cc-name"></label></section></main><script src="/react-fixture.js"></script>`),
  '/checkout': shell(`<main><h1>Checkout</h1><p>Review the delivery options</p><label>Shipping address<input name="shipping-address"></label><button>Add delivery note</button></main>`),
  '/payment': shell(`<main><h1>Payment</h1><label>Card number<input autocomplete="cc-number"></label><button>Pay now</button></main>`),
  '/saved-payment': shell(`<main><h1>Payment</h1><p>Saved card ending 4242</p><button>Pay now</button><button>Change card</button></main>`),
  '/detail': shell(`<main><h1>Trail boots</h1><div id="react-root"></div>
    <label>Size<select><option value="small">Small</option><option value="large">Large</option></select></label>
    <label><input type="checkbox">Waterproof</label>
    <section id="pane" role="region" aria-label="More models">${Array.from({length:14},(_,i)=>`<p><a href="/detail?model=${i}">Model ${i}</a></p>`).join('')}</section>
    <button id="expand">Expand specifications</button><div id="specs"></div>
    </main><script src="/react-fixture.js"></script>
    <script>document.getElementById('expand').onclick=()=>document.getElementById('specs').textContent='Water resistance 20000 mm';</script>`),
};

const browser = await chromium.launch({ headless: true });
let assertions = 0;
function check(value, message) { assert.ok(value, message); assertions++; }
try {
  const context = await browser.newContext({ viewport: { width: 900, height: 1000 } });
  await context.route('**/*', route => {
    const url = new URL(route.request().url());
    if (url.origin !== origin) return route.abort();
    if (url.pathname === '/trail-alpha.png') return route.fulfill({ status: 200, contentType: 'image/png', body: productPng });
    const body = url.pathname === '/react-fixture.js' ? reactBundle : pages[url.pathname];
    return body ? route.fulfill({ status: 200, contentType: url.pathname.endsWith('.js') ? 'application/javascript' : 'text/html', body }) : route.abort();
  });
  await context.addInitScript({ content: source });
  const page = await context.newPage();
  const observe = () => page.evaluate(() => window.__vitlaneWebViewAgentV1.observe());
  const preview = (snapshot, command) => page.evaluate(({ snapshot, command }) => window.__vitlaneWebViewAgentV1.preview(command, snapshot.url, snapshot.documentId), {snapshot,command});
  const act = (snapshot, command) => page.evaluate(({ snapshot, command }) => window.__vitlaneWebViewAgentV1.action(command, snapshot.url, snapshot.documentId), {snapshot,command});
  const target = (snapshot,label) => { const item=snapshot.elements.find(item=>item.label===label); assert.ok(item,`Missing target: ${label}`); return item.id; };

  await page.goto(origin + '/catalog');
  let snapshot = await observe();
  check(snapshot.images.length === 1 && snapshot.images[0].src === origin + '/trail-alpha.png'
    && snapshot.images[0].alt === 'Blue Trail Alpha shoes' && snapshot.images[0].context.includes('Trail Alpha'),
    'Public product image retains its exact browser source and local card context');
  const firstImageId = snapshot.images[0].id;
  check((await observe()).images[0].id === firstImageId, 'Image identity stays stable within the current document');
  const firstDocument = snapshot.documentId;
  let result = await act(snapshot,{type:'click',targetId:target(snapshot,'Load more'),approved:true});
  check(result.ok, 'SPA load action dispatched');
  snapshot = await observe();
  check(snapshot.readiness.pending, 'Delayed SPA is busy before its results exist');
  await page.getByRole('link',{name:'Trail boots'}).waitFor();
  snapshot = await observe();
  check(!snapshot.readiness.pending && snapshot.documentId === firstDocument && snapshot.revision > 0, 'SPA settled within the same document');
  await Promise.all([page.waitForURL(origin+'/detail'), act(snapshot,{type:'click',targetId:target(snapshot,'Trail boots')})]);
  check(context.pages().length === 1, '_blank link navigated the existing page');
  await page.getByLabel('Public draft').waitFor();
  snapshot = await observe();
  check(snapshot.documentId !== firstDocument, 'Multipage navigation created fresh document identity');
  result = await act(snapshot,{type:'type',targetId:target(snapshot,'Public draft'),text:'Compare these hiking boots.',approved:true});
  check(result.ok && result.verified, 'React controlled input accepted native setter/input events');
  snapshot = await observe();
  check(snapshot.elements.find(item=>item.label==='Public draft').valueMatchesLastInput, 'Next observation retains factual input readback');
  check(await page.locator('output').textContent() === 'Compare these hiking boots.', 'Real React state and preview updated');
  result = await act(snapshot,{type:'type',targetId:target(snapshot,'Locked draft'),text:'Rejected draft',approved:true});
  check(!result.ok && result.code === 'NO_EFFECT', 'React controlled rejection reported as failure');

  snapshot = await observe();
  result = await act(snapshot,{type:'select',targetId:target(snapshot,'Size'),value:'large',approved:true});
  check(result.ok && result.verified && await page.getByLabel('Size').inputValue()==='large', 'Select changed and read back');
  snapshot = await observe();
  result = await act(snapshot,{type:'check',targetId:target(snapshot,'Waterproof'),checked:true,approved:true});
  check(result.ok && result.verified && await page.getByLabel('Waterproof').isChecked(), 'Checkbox changed and read back');
  snapshot = await observe();
  result = await act(snapshot,{type:'scroll',targetId:target(snapshot,'More models'),direction:'down'});
  check(result.ok && result.changed && await page.locator('#pane').evaluate(element=>element.scrollTop)>0, 'Nested scroll moved its own container');
  snapshot = await observe();
  const expandId = target(snapshot,'Expand specifications');
  result = await preview(snapshot,{type:'click',targetId:expandId});
  check(result.ok && result.highlighted && Number.isFinite(result.centerX) && Number.isFinite(result.centerY)
    && result.viewportWidth > 0 && result.viewportHeight > 0
    && await page.locator('div[aria-hidden="true"]').count() === 1,
    'Visible agent target preview returns private native coordinates and highlights the exact grounded element without executing it');
  check(await page.locator('#specs').textContent()==='', 'Target preview has no page side effect');
  await page.evaluate(()=>{const overlay=document.createElement('div');overlay.id='overlay';document.body.append(overlay)});
  result = await act(snapshot,{type:'click',targetId:expandId,approved:true});
  check(!result.ok && result.code==='OBSCURED', 'Actual Chromium hit-testing refused an overlay');
  check(await page.locator('#specs').textContent()==='', 'Overlay did not allow synthetic click-through');
  await page.evaluate(()=>document.getElementById('overlay').remove());

  await page.goto(origin+'/modal');
  snapshot=await observe();
  check(snapshot.interrupts.length===1 && snapshot.interrupts[0].blocking
    && snapshot.interrupts[0].text.includes('Select a delivery location'),
    'Visible modal is exposed as a structured blocking interrupt');
  const closeId=target(snapshot,'Close');
  check(snapshot.elements.find(item=>item.id===closeId).interrupt && snapshot.interrupts[0].dismissIds.includes(closeId),
    'Modal controls are explicitly scoped to the interrupt action space');
  result=await act(snapshot,{type:'click',targetId:closeId,approved:true});
  check(result.ok, 'Observed nonessential dialog can be dismissed through its grounded close control');

  await page.goto(origin+'/modal-open');
  snapshot=await observe();
  const sizeGuideId=target(snapshot,'Size guide');
  check(snapshot.interrupts.length===0, 'Hidden product detail dialog is absent before its opener is activated');
  result=await act(snapshot,{type:'click',targetId:sizeGuideId,approved:true});
  check(result.ok, 'Same-document size guide activation dispatched');
  snapshot=await observe();
  check(snapshot.interrupts.length===1 && snapshot.interrupts[0].triggerId===sizeGuideId
    && snapshot.interrupts[0].triggerLabel==='Size guide',
    'New same-page modal records the exact opener as its causal postcondition');
  check(snapshot.interrupts[0].actionIds.includes(target(snapshot,'Close guide')),
    'New modal exposes its own controls for the next browser action');

  await page.goto(origin+'/filters');
  snapshot=await observe();
  const filtersId=target(snapshot,'Filters');
  check(snapshot.elements.find(item=>item.id===filtersId).role==='button'
    && snapshot.elements.find(item=>item.label==='Sort By').role==='button',
    'Nonsemantic catalog Filters and Sort By triggers become grounded buttons');
  result=await act(snapshot,{type:'click',targetId:filtersId,approved:true});
  check(result.ok, 'Nonsemantic Filters trigger dispatched');
  snapshot=await observe();
  check(snapshot.interrupts.length===1 && snapshot.interrupts[0].triggerId===filtersId
    && snapshot.activation.triggerId===filtersId && snapshot.activation.newActionIds.includes(target(snapshot,'Material')),
    'Filter drawer is tied to its trigger and prioritizes its newly visible actions');
  const materialId=target(snapshot,'Material');
  result=await act(snapshot,{type:'click',targetId:materialId,approved:true});
  check(result.ok, 'Custom facet accordion trigger dispatched');
  snapshot=await observe();
  const cashmere=snapshot.elements.find(item=>item.label==='Cashmere');
  check(cashmere && cashmere.role==='checkbox' && cashmere.group.includes('Material')
    && snapshot.activation.triggerId===materialId && snapshot.activation.newActionIds.includes(cashmere.id),
    'Hidden checkbox label becomes a grouped facet choice after its accordion opens');
  result=await act(snapshot,{type:'check',targetId:cashmere.id,checked:true,approved:true});
  check(result.ok && result.verified && await page.locator('#cashmere-choice').isChecked(),
    'Visible facet label toggles and verifies its hidden native checkbox');

  await page.goto(origin+'/loro-filters');
  snapshot=await observe();
  const loroFiltersId=target(snapshot,'Filters');
  result=await act(snapshot,{type:'click',targetId:loroFiltersId,approved:true});
  check(result.ok && result.navigating===false, 'Loro Piana modal trigger is treated as a same-page activation');
  snapshot=await observe();
  check(snapshot.interrupts.length===1 && snapshot.interrupts[0].triggerId===loroFiltersId,
    'Loro Piana refinement modal is causally tied to Filters');
  const materialsId=target(snapshot,'Materials');
  result=await act(snapshot,{type:'click',targetId:materialsId,approved:true});
  check(result.ok && result.navigating===false, 'Loro Piana refinement accordion opens without a navigation wait');
  snapshot=await observe();
  const loroCashmere=snapshot.elements.find(item=>item.label==='Cashmere');
  check(loroCashmere && loroCashmere.role==='link' && loroCashmere.selected===false
    && loroCashmere.group.includes('Materials'),
    'Algolia link facet exposes its group and initial selected state: '+JSON.stringify(loroCashmere));
  result=await act(snapshot,{type:'click',targetId:loroCashmere.id,approved:true});
  check(result.ok && result.navigating===false && result.effect==='activated',
    'preventDefault Algolia facet click does not pretend to navigate');
  snapshot=await observe();
  check(snapshot.elements.find(item=>item.label==='Cashmere').selected===true,
    'Algolia active class verifies the selected filter option');
  const viewResultsId=target(snapshot,'View 28 Products');
  result=await act(snapshot,{type:'click',targetId:viewResultsId,approved:true});
  check(result.ok && result.navigating===false, 'View results closes the filter without a navigation wait');
  snapshot=await observe();
  check(snapshot.interrupts.length===0 && snapshot.deactivation.triggerId===viewResultsId
    && snapshot.deactivation.effect==='interrupt_closed',
    'Closing the Loro Piana filter is a verified causal postcondition');

  await page.goto(origin+'/catalog');
  snapshot = await observe();
  await Promise.all([page.waitForURL(url=>url.pathname==='/search'),act(snapshot,{type:'type',targetId:target(snapshot,'Search'),text:'waterproof boots',submit:true})]);
  check(new URL(page.url()).searchParams.get('q')==='waterproof boots', 'Search GET form completed multipage navigation');
  snapshot=await observe();
  check(snapshot.text.includes('Search results'), 'New page is readable after submission');

  await page.goto(origin+'/cards');
  snapshot=await observe();
  const comparisons=snapshot.elements.filter(item=>item.label==='Compare');
  check(comparisons.length===2 && comparisons[0].group==='Trail Alpha' && comparisons[1].group==='Trail Beta', 'Repeated buttons preserve both candidates with their own product identity');
  check(comparisons[1].context.includes('$120') && !comparisons[1].context.includes('$90'), 'Price context stays within its own card');
  result=await act(snapshot,{type:'click',targetId:comparisons[1].id,approved:true});
  check(result.ok && await page.evaluate(()=>document.body.dataset.choice)==='beta', 'Grounded ID clicked the requested product card');
  snapshot=await observe();
  await page.locator('#alpha h2').evaluate(element=>element.textContent='Trail Gamma');
  result=await act(snapshot,{type:'click',targetId:comparisons[0].id,approved:true});
  check(!result.ok && result.code==='STALE_TARGET', 'Recycled button identity was invalidated when its product changed');

  await page.goto(origin+'/nested');
  snapshot=await observe();
  const pane=snapshot.elements.find(item=>item.label==='Models');
  result=await act(snapshot,{type:'scroll',targetId:pane.id,direction:'down'});
  const afterScroll=await observe();
  check(result.ok && snapshot.viewport.y===afterScroll.viewport.y && snapshot.text===afterScroll.text, 'Nested scrolling may preserve window position and readable text');
  check(pane.scrollTop===0 && afterScroll.elements.find(item=>item.id===pane.id).scrollTop>0, 'Pane scroll offset provides an explicit observable postcondition');

  const humanFields = () => page.evaluate(() => window.__vitlaneWebViewAgentV1.humanFields());
  const manualState = () => page.evaluate(() => window.__vitlaneWebViewAgentV1.manualState());
  const fillHuman = (snapshot, values) => page.evaluate(({ snapshot, values }) => window.__vitlaneWebViewAgentV1.fillHumanFields({values},snapshot.url,snapshot.documentId),{snapshot,values});
  await page.goto(origin+'/human-fields');
  await page.getByLabel('Recipient name',{exact:true}).waitFor();
  let humanSnapshot = await humanFields();
  check(!humanSnapshot.blocked && humanSnapshot.fields.length===4 && humanSnapshot.fields.every(field=>!/(password|verification|card)/i.test(field.label)), 'Local assistance lists safe contact fields while excluding password, OTP and card data');
  await page.reload();
  await page.getByLabel('Recipient name',{exact:true}).waitFor();
  result = await fillHuman(humanSnapshot,[{id:humanSnapshot.fields[0].id,value:'Mina Person'}]);
  check(!result.ok && result.code==='STALE_DOCUMENT', 'Native answer cannot cross a same-URL document reload');
  humanSnapshot = await humanFields();
  await page.getByLabel('Recipient name',{exact:true}).evaluate(input=>input.form.action='/changed-destination');
  result = await fillHuman(humanSnapshot,[{id:humanSnapshot.fields[0].id,value:'Mina Person'}]);
  check(!result.ok && result.code==='STALE_FIELD' && await page.getByLabel('Recipient name',{exact:true}).inputValue()==='', 'Changed form destination invalidates a pending human answer');
  await page.locator('#private-controls').evaluate(element=>element.remove());
  check(!(await observe()).sensitive, 'Fixture becomes public when credential controls are removed before assistance');
  humanSnapshot = await humanFields();
  const answers=['Mina Person','+82 10 1234 5678','mina@example.com'];
  result = await fillHuman(humanSnapshot,humanSnapshot.fields.slice(0,3).map((field,i)=>({id:field.id,value:answers[i]})));
  check(result.ok && result.filled===3 && await page.getByLabel('Phone',{exact:true}).inputValue()===answers[1], 'Human phone/email answers update actual React controlled inputs');
  check((await page.locator('output').textContent()).includes(answers[0]) && !(await page.evaluate(()=>window.fixtureSubmits)), 'React state mirrors the answer locally without helper form submission');
  snapshot = await observe();
  check(!snapshot.sensitive && snapshot.text.includes('[로컬 개인정보 숨김]') && answers.every(answer=>!JSON.stringify({snapshot,result}).includes(answer)), 'Mirrored PII is redacted while the agent can continue reading the checkout page');
  humanSnapshot = await humanFields();
  const locked = humanSnapshot.fields.find(field=>field.label==='Locked email');
  result = await fillHuman(humanSnapshot,[{id:locked.id,value:'locked@example.com'}]);
  check(!result.ok && result.code==='NO_EFFECT' && !JSON.stringify(result).includes('locked@example.com'), 'Actual React controlled rejection reports failure without echoing a private answer');
  await page.goto(origin+'/catalog');
  check(!(await observe()).sensitive && (await observe()).text.includes('Trail catalog'), 'A new public document resets the local privacy latch');
  check((await observe()).screenshotSafe, 'Public catalog and empty search box are eligible for a native screenshot');
  await page.goto(origin+'/checkout');
  snapshot=await observe();
  check(!snapshot.sensitive && snapshot.personalFields.some(field=>field.kind==='address') && snapshot.elements.some(item=>item.label==='Add delivery note'), 'Checkout remains observable while personal fields are exposed only as value-free kinds');
  check(!(await manualState()).secretRequired, 'Ordinary checkout fields do not force a browser handoff');
  await page.goto(origin+'/payment');
  snapshot=await observe();
  check(snapshot.sensitive && (await manualState()).secretRequired, 'Card entry is detected as a local manual step');
  await page.goto(origin+'/saved-payment');
  snapshot=await observe();
  check(!snapshot.sensitive && snapshot.paymentRequired && !snapshot.elements.some(item=>item.label==='Pay now'), 'A saved-card payment is handed off without exposing its final charge control to AI actions');
  await page.goto(origin+'/catalog');
  await page.evaluate(()=>{const header=document.createElement('header');header.className='account-menu';header.textContent='Mina Person account';document.body.prepend(header)});
  snapshot=await observe();
  check(!snapshot.screenshotSafe && snapshot.localPreviewSafe && snapshot.images.length === 1 && !snapshot.text.includes('Mina Person'), 'A rendered account header stays out of model text without hiding local product media');
  await page.locator('header.account-menu').evaluate(element=>element.remove());
  await page.evaluate(()=>{const input=document.createElement('input');input.id='private-fixture';input.name='recipient';input.value='Mina Person';document.querySelector('main').append(input)});
  check(!(await observe()).screenshotSafe, 'A filled shipping field prevents raw pixel capture');
  await page.locator('#private-fixture').evaluate(element=>element.remove());
  await page.evaluate(()=>{const frame=document.createElement('iframe');frame.id='opaque-fixture';frame.src='about:blank';document.querySelector('main').append(frame)});
  check(!(await observe()).screenshotSafe, 'An opaque iframe prevents raw pixel capture');
  await page.locator('#opaque-fixture').evaluate(element=>element.remove());
  await page.evaluate(()=>{const label=document.createElement('p');label.id='private-fixture';label.setAttribute('aria-hidden','true');label.textContent='mina@example.com';document.querySelector('main').append(label)});
  check(!(await observe()).screenshotSafe, 'Aria-hidden PII is still rendered and cannot be treated as pixel-hidden');
  await page.locator('#private-fixture').evaluate(element=>element.remove());
  await page.evaluate(()=>{const input=document.createElement('input');input.type='password';document.querySelector('main').append(input)});
  snapshot=await observe();
  check(snapshot.sensitive && !snapshot.screenshotSafe && !snapshot.localPreviewSafe && snapshot.images.length === 0, 'Credential pages always block local previews and model-visible media');
  console.log(`Chromium local fixture integration: ${assertions} assertions passed (React ${require('react/package.json').version}).`);
} finally {
  await browser.close();
}
