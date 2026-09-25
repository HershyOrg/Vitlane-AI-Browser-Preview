import test, { before, after } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { chromium } from 'playwright';

const moduleRoot = new URL('../../../mobile/modules/vitlane-browser/', import.meta.url);
const source = (await readFile(new URL('scripts/browser-page-agent.js', moduleRoot), 'utf8')).replace(/\r\n/g, '\n');
const java = await readFile(new URL('android/src/main/java/com/vitlane/browser/BrowserPageScript.java', moduleRoot), 'utf8');
const embedded = [...java.matchAll(/\.append\("([A-Za-z0-9+/=]+)"\)/g)].map(match => match[1]).join('');
let browser;
before(async () => {
  browser = await chromium.launch({ channel: process.env.PLAYWRIGHT_CHANNEL || undefined, headless: true });
});
after(async () => { await browser?.close(); });

async function fixture(t, body, path = '/products/shoes') {
  const context = await browser.newContext({ viewport: { width: 420, height: 860 } });
  const requests = [];
  await context.route('**/*', async route => {
    requests.push(route.request().url());
    await route.fulfill({ status: 200, contentType: 'text/html; charset=utf-8',
      body: `<!doctype html><html><head><title>Public running shoe</title></head><body>${body}</body></html>` });
  });
  t.after(() => context.close());
  const page = await context.newPage();
  await page.goto('https://shop.vitlane-fixture.com' + path);
  return { page, requests };
}
const observe = page => page.evaluate(`(${source}).observe()`);
const act = (page, action, url = page.url()) => page.evaluate(`(${source}).action(${JSON.stringify(action)}, ${JSON.stringify(url)})`);
const target = (observation, label) => observation.elements.find(element => element.label === label);

test('WebView generated Java embeds the tested source exactly', () => {
  assert.equal(Buffer.from(embedded, 'base64').toString('utf8'), source);
});

test('public product observation omits account chrome, values, hidden text and obvious PII', async t => {
  const { page } = await fixture(t, `
    <header><p>PRIVATE CUSTOMER NAME</p><span>account@example.com</span><button>Log out</button></header>
    <main><h1>Running shoe</h1><p>220g, 39,000원</p><p>contact@example.com / 010-1234-5678</p>
    <a href="/products/next">Next shoe</a><button>Add to cart</button></main>
    <input type="hidden" value="HIDDEN_VALUE"><div hidden>HIDDEN_TEXT</div>
    <input type="email" value="PRIVATE_INPUT_VALUE"><textarea>PRIVATE_TEXTAREA_VALUE</textarea>
    <script>localStorage.setItem('test-secret','STORAGE_SECRET');document.cookie='test=COOKIE_SECRET';</script>`);
  const observation = await observe(page);
  assert.equal(observation.sensitive, false);
  assert.match(observation.text, /Running shoe/);
  assert.ok(target(observation, 'Add to cart'));
  const encoded = JSON.stringify(observation);
  for (const secret of ['PRIVATE CUSTOMER', 'account@example', 'HIDDEN_VALUE', 'HIDDEN_TEXT',
    'PRIVATE_INPUT_VALUE', 'PRIVATE_TEXTAREA_VALUE', 'STORAGE_SECRET', 'COOKIE_SECRET', 'contact@example', '010-1234-5678']) {
    assert.ok(!encoded.includes(secret), secret);
  }
});

test('search input dispatches input/change and submits a public same-origin GET form', async t => {
  const { page, requests } = await fixture(t, `
    <form method="get" action="/results"><label>Search<input type="search" name="q"></label><button>Search now</button></form>`);
  const observation = await observe(page);
  const field = observation.elements.find(element => element.tag === 'input');
  assert.equal(field.search, true);
  await page.evaluate(() => {
    const input = document.querySelector('input');
    input.addEventListener('input', () => document.documentElement.dataset.inputEvent = 'yes');
    input.addEventListener('change', () => document.documentElement.dataset.changeEvent = 'yes');
    input.form.addEventListener('submit', () => {
      if (document.documentElement.dataset.inputEvent !== 'yes' || document.documentElement.dataset.changeEvent !== 'yes') throw Error('missing events');
    });
  });
  const [result] = await Promise.all([
    act(page, { type: 'type', targetId: field.id, text: 'running shoes', submit: true }),
    page.waitForURL('**/results?q=running+shoes'),
  ]);
  assert.equal(result.ok, true);
  assert.equal(result.navigating, true);
  assert.ok(requests.some(request => request.endsWith('/results?q=running+shoes')));
});

test('public links navigate and stable IDs are replaced on a new document', async t => {
  const { page } = await fixture(t, '<a href="/products/next">Next shoe</a>');
  const first = await observe(page);
  const second = await observe(page);
  assert.equal(first.elements[0].id, second.elements[0].id);
  const [result] = await Promise.all([
    act(page, { type: 'click', targetId: first.elements[0].id }),
    page.waitForURL('**/products/next'),
  ]);
  assert.equal(result.ok, true);
  assert.equal(result.navigating, true);
  assert.notEqual((await observe(page)).elements[0].id, first.elements[0].id);
});

test('search buttons submit without side-effect approval and SPA search receives Enter', async t => {
  const { page } = await fixture(t, '<form action="/results"><input type="search" name="q" value="shoe"><button>Search now</button></form>');
  const observation = await observe(page);
  const button = target(observation, 'Search now');
  assert.equal(button.search, true);
  const [result] = await Promise.all([
    act(page, { type: 'click', targetId: button.id }),
    page.waitForURL('**/results?q=shoe'),
  ]);
  assert.equal(result.ok, true);
  const spa = await fixture(t, '<input type="search" placeholder="Search"><script>document.querySelector("input").addEventListener("keydown", event => { if (event.keyCode === 13) window.searched = event.target.value; });</script>');
  const spaObservation = await observe(spa.page);
  assert.equal((await act(spa.page, { type: 'type', targetId: spaObservation.elements[0].id, text: 'running shoes', submit: true })).ok, true);
  assert.equal(await spa.page.evaluate(() => window.searched), 'running shoes');
});

test('side-effect buttons require native approval and cannot be blindly replayed', async t => {
  const { page } = await fixture(t, '<button onclick="window.added=(window.added||0)+1">Add to cart</button>');
  const observation = await observe(page);
  const action = { type: 'click', targetId: observation.elements[0].id };
  assert.equal((await act(page, action)).ok, false);
  assert.equal(await page.evaluate(() => window.added || 0), 0);
  assert.equal((await act(page, { ...action, approved: true })).ok, true);
  assert.equal(await page.evaluate(() => window.added), 1);
  assert.equal((await act(page, { ...action, approved: true })).ok, false);
  assert.equal(await page.evaluate(() => window.added), 1);
});

test('side-effect links require native approval by label, path or query', async t => {
  const { page } = await fixture(t, '<a href="/products/next?action=add_to_cart">Continue</a><a href="/remove-item/123">Continue removal</a><a href="/products/next">Buy now</a>');
  const observation = await observe(page);
  for (const element of observation.elements) {
    assert.equal((await act(page, { type: 'click', targetId: element.id })).ok, false, element.href);
  }
  const approved = observation.elements[0];
  const [result] = await Promise.all([
    act(page, { type: 'click', targetId: approved.id, approved: true }),
    page.waitForURL('**/products/next?action=add_to_cart'),
  ]);
  assert.equal(result.ok, true);
});

test('final order/payment controls are excluded and a changed control is rejected', async t => {
  const { page } = await fixture(t, '<button>Pay now</button><button>Place order</button><button>결제하기</button><button id="buy">Buy now</button>');
  const observation = await observe(page);
  assert.deepEqual(observation.elements.map(element => element.label), ['Buy now']);
  await page.locator('#buy').evaluate(element => { element.textContent = '주문하기'; });
  assert.equal((await act(page, { type: 'click', targetId: observation.elements[0].id, approved: true })).ok, false);
});

test('login/account/cart/checkout URLs suppress the whole observation', async t => {
  for (const path of ['/login', '/account', '/cart', '/checkout', '/order/orderForm.pang']) {
    const { page } = await fixture(t, '<h1>PRIVATE NAME</h1><a href="/products/x">PRIVATE ORDER</a>', path);
    const observation = await observe(page);
    assert.equal(observation.sensitive, true, path);
    assert.equal(observation.title, '');
    assert.equal(observation.text, '');
    assert.deepEqual(observation.elements, []);
    assert.ok(!JSON.stringify(observation).includes('PRIVATE'));
  }
});

test('visible password/card/OTP forms suppress data while hidden login panels do not block a product', async t => {
  for (const field of ['<input type="password">', '<input autocomplete="cc-number">', '<input autocomplete="one-time-code">']) {
    const { page } = await fixture(t, '<p>PRIVATE USER NAME</p>' + field);
    const observation = await observe(page);
    assert.equal(observation.sensitive, true, field);
    assert.equal(observation.text, '');
    assert.deepEqual(observation.elements, []);
  }
  const { page } = await fixture(t, '<h1>Credit card wallet</h1><div hidden><input type="password"></div><button>Buy now</button>');
  assert.equal((await observe(page)).sensitive, false);
});

test('URL, label, href, replaced nodes and edited search text invalidate prior observations', async t => {
  const { page } = await fixture(t, '<a id="link" href="/products/next">Next shoe</a><input id="q" type="search" placeholder="Search">');
  let observation = await observe(page);
  await page.evaluate(() => history.pushState({}, '', '/products/changed'));
  assert.equal((await act(page, { type: 'click', targetId: target(observation, 'Next shoe').id }, observation.url)).ok, false);
  observation = await observe(page);
  await page.locator('#link').evaluate(element => element.setAttribute('href', '/products/other'));
  assert.equal((await act(page, { type: 'click', targetId: target(observation, 'Next shoe').id })).ok, false);
  observation = await observe(page);
  await page.locator('#link').evaluate(element => { element.outerHTML = element.outerHTML; });
  assert.equal((await act(page, { type: 'click', targetId: target(observation, 'Next shoe').id })).ok, false);
  observation = await observe(page);
  const search = observation.elements.find(element => element.search);
  await page.locator('#q').fill('user changed this');
  assert.equal((await act(page, { type: 'type', targetId: search.id, text: 'overwrite', submit: true })).ok, false);
});

test('POST, cross-origin and checkout forms are not automatic search targets', async t => {
  const { page } = await fixture(t, `
    <form method="post" action="/results"><input type="search" placeholder="POST search"><button>Post</button></form>
    <form action="https://other.vitlane-fixture.com/search"><input type="search" placeholder="Other search"><button>Other</button></form>
    <form action="/checkout"><input type="search" placeholder="Checkout search"><button>Checkout</button></form>`);
  const observation = await observe(page);
  assert.equal(observation.sensitive, false);
  assert.ok(observation.elements.every(element => !element.search));
  assert.equal((await act(page, { type: 'type', targetId: 'unknown', text: 'test', submit: true })).ok, false);
});

test('search text is not returned and sensitive text is not injected', async t => {
  const { page } = await fixture(t, '<input type="search" placeholder="Search" value="EXISTING_SEARCH_VALUE">');
  const observation = await observe(page);
  assert.ok(!JSON.stringify(observation).includes('EXISTING_SEARCH_VALUE'));
  assert.equal((await act(page, { type: 'type', targetId: observation.elements[0].id, text: 'person@example.com', submit: false })).ok, false);
  assert.equal((await act(page, { type: 'type', targetId: observation.elements[0].id, text: 'running shoes', submit: false })).ok, true);
  assert.equal(await page.locator('input').inputValue(), 'running shoes');
  const textarea = await fixture(t, '<form><label>Search<textarea role="searchbox" name="q">PRIVATE_DEFAULT_VALUE</textarea></label></form>');
  const textareaObservation = await observe(textarea.page);
  assert.ok(!JSON.stringify(textareaObservation).includes('PRIVATE_DEFAULT_VALUE'));
  assert.equal(textareaObservation.elements[0].label, 'Search');
});

test('observations are bounded and scrolling invalidates old target references', async t => {
  const { page } = await fixture(t, '<p>' + 'public product '.repeat(1500) + '</p>'
    + Array.from({ length: 85 }, (_, index) => `<button>Option ${index}</button>`).join('') + '<div style="height:2000px"></div>');
  const observation = await observe(page);
  assert.ok(observation.text.length <= 10000);
  assert.equal(observation.elements.length, 60);
  assert.equal((await act(page, { type: 'scroll', direction: 'down' })).ok, true);
  assert.equal((await act(page, { type: 'click', targetId: observation.elements[0].id, approved: true })).ok, false);
});

test('scrolling reveals below-fold targets beyond the first sixty DOM controls', async t => {
  const { page } = await fixture(t, '<div>' + Array.from({ length: 75 }, (_, index) => `<button style="display:block;height:30px">Top ${index}</button>`).join('')
    + '</div><button id="buy">Buy now</button>');
  assert.equal(target(await observe(page), 'Buy now'), undefined);
  await page.locator('#buy').scrollIntoViewIfNeeded();
  assert.ok(target(await observe(page), 'Buy now'));
});
