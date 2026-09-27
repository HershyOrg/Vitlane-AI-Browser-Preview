/** @jest-environment node */
/// <reference types="node" />

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { JSDOM } from 'jsdom';

const source = readFileSync(resolve(__dirname, '../scripts/browser-page-agent.js'), 'utf8');
type PageAgent = {
  observe(): any;
  action(action: Record<string, unknown>, expectedUrl: string, expectedDocumentId: string): any;
  humanFields(): any;
  fillHumanFields(command: Record<string, unknown>, expectedUrl: string, expectedDocumentId: string): any;
  secretFields(): any;
  fillSecretFields(command: Record<string, unknown>, expectedUrl: string, expectedDocumentId: string): any;
};

function page(html: string) {
  const dom = new JSDOM(html, { url: 'https://shop.example.com/catalog', runScripts: 'outside-only', pretendToBeVisual: true });
  const { window } = dom;
  const rectangle = { x: 10, y: 10, top: 10, left: 10, right: 210, bottom: 40, width: 200, height: 30, toJSON: () => ({}) };
  Object.defineProperty(window.HTMLElement.prototype, 'getBoundingClientRect', { configurable: true, value: () => rectangle });
  Object.defineProperty(window.HTMLElement.prototype, 'getClientRects', { configurable: true, value: () => [rectangle] });
  Object.defineProperty(window.HTMLElement.prototype, 'scrollIntoView', { configurable: true, value: jest.fn() });
  Object.defineProperty(window, 'scrollBy', { configurable: true, value: jest.fn() });
  const agent = window.eval(source) as PageAgent;
  const observe = () => agent.observe();
  const act = (action: Record<string, unknown>, snapshot = observe()) => agent.action(action, window.location.href, snapshot.documentId);
  const target = (label: string, snapshot = observe()) => snapshot.elements.find((element: any) => element.label === label);
  return { dom, window, agent, observe, act, target };
}

describe('Android native-only human form assistance', () => {
  const fields = '<form action="/shipping"><label>Recipient name<input name="recipient" value="Existing Person"></label><label>Address<textarea name="address">Existing address</textarea></label><label>Phone<input type="tel" name="phone"></label><label>Email<input type="email" name="email"></label><button>Submit</button></form>';
  const fill = (agent: PageAgent, snapshot: any, values: any[]) => agent.fillHumanFields({ values }, snapshot.url, snapshot.documentId);

  it('lists only bounded basic personal fields without values even beside password/card inputs', () => {
    const { agent, observe } = page(fields + '<label>Password<input type="password"></label><input autocomplete="one-time-code"><input name="card-name" autocomplete="cc-name"><input name="email-verification"><input name="phone-cvv"><input name="api-key-name"><input name="name" hidden><input name="name" disabled><input name="name" readonly>');
    const snapshot = agent.humanFields();
    expect(snapshot.blocked).toBe(false);
    expect(snapshot.fields.map((field: any) => field.kind)).toEqual(['recipient', 'address', 'phone', 'email']);
    expect(snapshot.fields.every((field: any) => Object.keys(field).sort().join(',') === 'id,inputType,kind,label')).toBe(true);
    expect(snapshot.fields.every((field: any) => /^he[^\s]+/.test(field.id))).toBe(true);
    expect(agent.humanFields().fields.map((field: any) => field.id)).toEqual(snapshot.fields.map((field: any) => field.id));
    expect(JSON.stringify(snapshot)).not.toContain('Existing Person');
    expect(JSON.stringify(snapshot)).not.toContain('Existing address');
    expect(observe()).toMatchObject({ sensitive: true, text: '', html: '', elements: [] });
  });

  it('caps descriptors at eight and blocks pages without supported fields', () => {
    expect(page('<input type="password"><input name="q">').agent.humanFields()).toMatchObject({ blocked: true, fields: [] });
    expect(page(Array.from({ length: 12 }, (_, i) => `<label>Name ${i}<input name="name-${i}"></label>`).join('')).agent.humanFields().fields).toHaveLength(8);
  });

  it('excludes disguised authentication/payment fields with camel-case or localized hints', () => {
    const inputs = ['phoneOtp', 'emailOneTimeCode', 'recipientCcName', 'emailSmsCode', 'nameSecurityAnswer', 'recipientIban', 'namePin', '전화 확인번호', '이메일 일회용 번호'];
    expect(page(inputs.map(name => `<label>Name<input name="${name}"></label>`).join('')).agent.humanFields()).toMatchObject({ blocked: true, fields: [] });
  });

  it('recognizes postal codes and honors the site maximum length, including zero', () => {
    const { agent, window } = page('<label>Postal code<input autocomplete="postal-code" maxlength="0"></label>');
    const snapshot = agent.humanFields();
    expect(snapshot.fields[0].kind).toBe('postcode');
    expect(fill(agent, snapshot, [{ id: snapshot.fields[0].id, value: '12345' }]).code).toBe('INVALID_FIELDS');
    expect(window.document.querySelector('input')!.value).toBe('');
  });

  it('fills legitimate phone/email locally, dispatches input/change without submit, and hides mirrored personal text', () => {
    const { agent, window, observe } = page(fields + '<output></output>');
    const snapshot = agent.humanFields();
    const answers = ['Mina Person', '123 Example Road\nUnit 2', '+82 10 1234 5678', 'mina@example.com'];
    const inputs = jest.fn(), changes = jest.fn(), submissions = jest.fn();
    window.document.addEventListener('input', event => { inputs(); window.document.querySelector('output')!.textContent += (event.target as HTMLInputElement).value; });
    window.document.addEventListener('change', changes);
    window.document.querySelector('form')!.addEventListener('submit', submissions);
    const result = fill(agent, snapshot, snapshot.fields.map((field: any, i: number) => ({ id: field.id, value: answers[i] })));
    expect(result).toEqual({ ok: true, filled: 4, failed: 0 });
    expect(Array.from(window.document.querySelectorAll('input,textarea')).map(input => (input as HTMLInputElement).value)).toEqual(answers);
    expect(inputs).toHaveBeenCalledTimes(4); expect(changes).toHaveBeenCalledTimes(4); expect(submissions).not.toHaveBeenCalled();
    expect(observe()).toMatchObject({ sensitive: false });
    for (const answer of answers) expect(JSON.stringify({ result, observation: observe(), localFields: agent.humanFields() })).not.toContain(answer);
    expect(agent.action({ type: 'scroll', direction: 'down' }, snapshot.url, snapshot.documentId).ok).toBe(true);
  });

  it('rejects an answer after same-document navigation or with the wrong document identity', () => {
    const { agent, window } = page(fields);
    const snapshot = agent.humanFields(), values = [{ id: snapshot.fields[0].id, value: 'New Person' }];
    expect(agent.fillHumanFields({ values }, snapshot.url, 'other-document').code).toBe('STALE_DOCUMENT');
    window.history.pushState({}, '', '/other-page');
    expect(fill(agent, snapshot, values).code).toBe('STALE_DOCUMENT');
    expect(window.document.querySelector('input')!.value).toBe('Existing Person');
  });

  it.each(['label', 'type', 'form-action', 'base-url', 'form-owner', 'replacement', 'hidden', 'readonly', 'user-value'])(
    'rejects a stale field changed by %s before applying any answer', change => {
      const { agent, window } = page(fields);
      const snapshot = agent.humanFields();
      const input = window.document.querySelector('input')!;
      if (change === 'label') input.parentElement!.firstChild!.textContent = 'Other recipient name';
      if (change === 'type') input.type = 'password';
      if (change === 'form-action') input.form!.action = '/another-destination';
      if (change === 'base-url') { const base = window.document.createElement('base'); base.href = 'https://different.example.com/'; window.document.head.append(base); }
      if (change === 'form-owner') { const form = window.document.createElement('form'); window.document.body.append(form); form.append(input); }
      if (change === 'replacement') input.replaceWith(input.cloneNode(true));
      if (change === 'hidden') input.hidden = true;
      if (change === 'readonly') input.readOnly = true;
      if (change === 'user-value') input.value = 'User intervened';
      const result = fill(agent, snapshot, [{ id: snapshot.fields[1].id, value: 'New address' }, { id: snapshot.fields[0].id, value: 'New Person' }]);
      expect(result).toMatchObject({ ok: false, filled: 0, failed: 2, code: 'STALE_FIELD' });
      expect(window.document.querySelector('textarea')!.value).toBe('Existing address');
    });

  it('rechecks later fields after a site event handler replaces them', () => {
    const { agent, window, observe } = page(fields);
    const snapshot = agent.humanFields();
    window.document.querySelector('input')!.addEventListener('input', () => window.document.querySelector('textarea')!.replaceWith(window.document.createElement('textarea')));
    expect(fill(agent, snapshot, [{ id: snapshot.fields[0].id, value: 'New Person' }, { id: snapshot.fields[1].id, value: 'New address' }])).toEqual({ ok: false, filled: 1, failed: 1, code: 'STALE_FIELD' });
    expect(window.document.querySelector('textarea')!.value).toBe('');
    expect(observe().sensitive).toBe(false);
  });

  it('reports site input rejection without echoing the proposed answer', () => {
    const { agent, window } = page(fields);
    const snapshot = agent.humanFields();
    window.document.querySelector('input')!.addEventListener('input', event => { (event.target as HTMLInputElement).value = ''; });
    expect(fill(agent, snapshot, [{ id: snapshot.fields[0].id, value: 'New Person' }])).toEqual({ ok: false, filled: 0, failed: 1, code: 'NO_EFFECT' });
  });

  it.each(['unknown', 'duplicate', 'overlong', 'newline', 'extra-key'])('rejects %s answers atomically', invalid => {
    const { agent, window } = page(fields);
    const snapshot = agent.humanFields();
    const values: any[] = [{ id: snapshot.fields[0].id, value: 'New Person' }];
    if (invalid === 'unknown') values.push({ id: 'unknown', value: 'Other' });
    if (invalid === 'duplicate') values.push(values[0]);
    if (invalid === 'overlong') values.push({ id: snapshot.fields[2].id, value: '1'.repeat(41) });
    if (invalid === 'newline') values[0].value = 'New\nPerson';
    if (invalid === 'extra-key') values[0].submit = true;
    expect(fill(agent, snapshot, values).ok).toBe(false);
    expect(window.document.querySelector('input')!.value).toBe('Existing Person');
  });
});

describe('Android device-vault secret channel', () => {
  const secureForm = `<form action="/login">
    <label>Password<input type="password" name="password"></label>
    <label>Verification code<input autocomplete="one-time-code"></label>
    <label>Card number<input autocomplete="cc-number"></label>
    <label>Expiry<input autocomplete="cc-exp"></label>
    <label>CVC<input autocomplete="cc-csc"></label>
    <label>CAPTCHA<input name="captcha"></label><button>Continue</button></form>`;
  const fill = (agent: PageAgent, snapshot: any, values: any[]) =>
    agent.fillSecretFields({ values }, snapshot.url, snapshot.documentId);

  it('returns only native field descriptors and never current values or CAPTCHA', () => {
    const { agent, observe } = page(secureForm.replace('name="password"', 'name="password" value="site-existing-secret"'));
    const snapshot = agent.secretFields();
    expect(snapshot.fields.map((field: any) => field.kind)).toEqual(['password', 'otp', 'card_number', 'card_expiry', 'card_cvc']);
    expect(snapshot.fields.every((field: any) => Object.keys(field).sort().join(',') === 'id,inputType,kind,label')).toBe(true);
    expect(JSON.stringify(snapshot)).not.toContain('site-existing-secret');
    expect(JSON.stringify(snapshot)).not.toContain('captcha');
    expect(observe()).toMatchObject({ sensitive: true, title: '', text: '', html: '', elements: [] });
  });

  it('fills secrets locally without submission and never echoes them in results or observations', () => {
    const { agent, window, observe } = page(secureForm);
    const snapshot = agent.secretFields();
    const secrets = ['correct horse battery staple', '123456', '4111111111111111', '12/30', '123'];
    const submissions = jest.fn(), inputs = jest.fn();
    window.document.querySelector('form')!.addEventListener('submit', submissions);
    window.document.addEventListener('input', inputs);
    const result = fill(agent, snapshot, snapshot.fields.map((field: any, i: number) => ({ id: field.id, value: secrets[i] })));
    expect(result).toEqual({ ok: true, filled: 5, failed: 0 });
    expect(inputs).toHaveBeenCalledTimes(5);
    expect(submissions).not.toHaveBeenCalled();
    expect(Array.from(window.document.querySelectorAll('input')).slice(0, 5).map(input => (input as HTMLInputElement).value)).toEqual(secrets);
    const boundary = JSON.stringify({ result, observation: observe(), descriptors: agent.secretFields() });
    for (const secret of secrets) expect(boundary).not.toContain(secret);
  });

  it('rejects invalid or stale batches atomically', () => {
    const { agent, window } = page(secureForm);
    const snapshot = agent.secretFields();
    expect(fill(agent, snapshot, [{ id: snapshot.fields[2].id, value: '4111111111111112' }])).toMatchObject({ ok: false, code: 'INVALID_FIELDS', filled: 0 });
    expect((window.document.querySelector('[autocomplete="cc-number"]') as HTMLInputElement).value).toBe('');
    const password = window.document.querySelector('input[type="password"]')!;
    password.setAttribute('aria-label', 'Changed password field');
    expect(fill(agent, snapshot, [{ id: snapshot.fields[0].id, value: 'local-only-password' }])).toMatchObject({ ok: false, code: 'STALE_FIELD', filled: 0 });
    expect((password as HTMLInputElement).value).toBe('');
  });
});

describe('Android screenshot privacy eligibility', () => {
  it('allows public product cards and ordinary search text without adding raw values', () => {
    const { observe } = page('<main><form><label>Search<input type="search" name="q" value="trail boots"></label></form><article><h2>Trail Alpha</h2><p>$90 · blue</p><a href="/details">Details</a></article></main>');
    const snapshot = observe();
    expect(snapshot.screenshotSafe).toBe(true);
    expect(snapshot.sensitive).toBe(false);
    expect(JSON.stringify(snapshot)).not.toContain('trail boots');
  });

  it('blocks visible account pixels even when semantic observation omits the account header', () => {
    const { observe } = page('<header class="account-menu">Mina Person account</header><main><h1>Public catalog</h1></main>');
    const snapshot = observe();
    expect(snapshot).toMatchObject({ sensitive: false, screenshotSafe: false });
    expect(JSON.stringify(snapshot)).not.toContain('Mina Person');
  });

  it.each([
    '<label>Recipient<input name="recipient" value="Mina Person"></label>',
    '<label>Public draft<textarea>Private personal draft</textarea></label>',
    '<label>Option<select><option selected>Stored personal option</option></select></label>',
    '<div contenteditable="true">Private editable content</div>',
    '<label>Search<input type="search" name="q" value="mina@example.com"></label>',
  ])('blocks private or filled non-search controls from screenshots: %s', control => {
    expect(page('<main><h1>Catalog</h1>' + control + '</main>').observe().screenshotSafe).toBe(false);
  });

  it.each(['<input type="password">', '<iframe src="https://other.example.com"></iframe>', '<video></video>', '<canvas></canvas>', '<object></object>', '<embed>'])(
    'blocks credential or opaque page pixels: %s', content => {
      expect(page('<main><h1>Catalog</h1>' + content + '</main>').observe().screenshotSafe).toBe(false);
    });

  it('does not confuse aria-hidden or inert with actual pixel invisibility', () => {
    expect(page('<main><h1>Catalog</h1><p aria-hidden="true">mina@example.com</p></main>').observe().screenshotSafe).toBe(false);
    expect(page('<main><h1>Catalog</h1><div inert><input value="Private draft"></div></main>').observe().screenshotSafe).toBe(false);
    expect(page('<header aria-hidden="true">Mina Person<button>Logout</button></header><main>Catalog</main>').observe().screenshotSafe).toBe(false);
    expect(page('<main><h1>Catalog</h1><p style="display:none">mina@example.com</p><iframe style="display:none"></iframe></main>').observe().screenshotSafe).toBe(true);
  });

  it('checks raw viewport text before redaction, including email split between inline nodes', () => {
    expect(page('<main>Contact <span>mina</span><span>@</span><span>example.com</span></main>').observe().screenshotSafe).toBe(false);
    const snapshot = page('<main>Contact mina@example.com or 010-1234-5678</main>').observe();
    expect(snapshot.screenshotSafe).toBe(false);
    expect(snapshot.text).not.toContain('mina@example.com');
    expect(snapshot.text).not.toContain('010-1234-5678');
  });

  it('allows content that is fully outside the captured viewport', () => {
    const { observe, window } = page('<main><h1>Catalog</h1><div class="account-menu">Private member area</div><iframe></iframe></main>');
    for (const element of window.document.querySelectorAll('.account-menu,iframe')) Object.defineProperty(element, 'getBoundingClientRect', {
      value: () => ({ x: 0, y: 5000, top: 5000, left: 0, right: 200, bottom: 5200, width: 200, height: 200 }),
    });
    expect(observe().screenshotSafe).toBe(true);
  });
});

describe('Android page agent semantic observations', () => {
  it('recognizes only exact modal close buttons without navigation or submission', () => {
    const { observe } = page('<main><button>Close</button><div role="dialog"><button type="button">닫기</button><div role="button">×</div><button>Cancel</button><button>Accept cookies</button><form><button>Dismiss</button><button type="reset">창 닫기</button><button type="button">✕</button></form><a role="button" href="/next">Close</a></div></main>');
    const elements = observe().elements;
    expect(elements.filter((item: any) => item.dismiss).map((item: any) => item.label)).toEqual(['닫기', '×', '✕']);
    expect(elements.filter((item: any) => ['Close', 'Cancel', 'Accept cookies', 'Dismiss', '창 닫기'].includes(item.label)).every((item: any) => !item.dismiss)).toBe(true);
  });

  it('invalidates a previously recognized close button when it leaves its dialog', () => {
    const { observe, act, window } = page('<main><div role="dialog"><button type="button">Close</button></div></main>');
    const snapshot = observe(), close = snapshot.elements.find((item: any) => item.label === 'Close' && item.role === 'button');
    expect(close.dismiss).toBe(true);
    const button = window.document.querySelector('button')!, clicked = jest.fn();
    button.addEventListener('click', clicked);
    window.document.querySelector('main')!.append(button);
    expect(act({ type: 'click', targetId: close.id, approved: true }, snapshot).code).toBe('STALE_TARGET');
    expect(clicked).not.toHaveBeenCalled();
  });

  it('grounds repeated buttons in their own product card and price', () => {
    const { observe } = page('<main><article><h2>Trail Alpha</h2><p>$90 · blue</p><button>Compare</button></article><article><h2>Trail Beta</h2><p>$120 · red</p><button>Compare</button></article></main>');
    const buttons = observe().elements.filter((item: any) => item.label === 'Compare');
    expect(buttons).toHaveLength(2);
    expect(buttons[0]).toMatchObject({ group: 'Trail Alpha', context: 'Trail Alpha $90 · blue' });
    expect(buttons[1]).toMatchObject({ group: 'Trail Beta', context: 'Trail Beta $120 · red' });
  });

  it('preserves table-row identity and anonymous card headings', () => {
    const { observe } = page('<main><table><tr><th>Harbor room</th><td>2 guests</td><td>$80</td><td><button>Select</button></td></tr><tr><th>Garden room</th><td>4 guests</td><td>$150</td><td><button>Select</button></td></tr></table><div><h3>Canvas boot</h3><p>Size 42</p><div><button>Details</button></div></div></main>');
    const snapshot = observe();
    const selects = snapshot.elements.filter((item: any) => item.label === 'Select');
    expect(selects[0]).toMatchObject({ group: 'Harbor room', context: 'Harbor room 2 guests $80' });
    expect(selects[1].group).toBe('Garden room');
    expect(snapshot.elements.find((item: any) => item.label === 'Details')).toMatchObject({ group: 'Canvas boot', context: 'Canvas boot Size 42' });
  });

  it('relates a nested form to its owning product instead of an action-wrapper class', () => {
    const { observe } = page('<main><div class="product-card"><h2>Trail Alpha</h2><form aria-label="Options"><label>Size<select><option>Small</option></select></label><div class="card-actions"><button>Apply</button></div></form></div></main>');
    expect(observe().elements.find((item: any) => item.label === 'Apply')).toMatchObject({ group: 'Trail Alpha / Options', context: 'Size' });
  });

  it('bounds relational context and omits private, hidden, editable and neighboring-card data', () => {
    const { observe } = page(`<main><article><h2>Public boot</h2><div class="account-menu">Private member identity</div><textarea>Private draft</textarea><div contenteditable="true">Private editing content</div><input value="Private field value"><p hidden>Hidden account note</p><p>Contact person@example.com</p><p>${'Public description '.repeat(80)}</p><button>Compare</button></article><article><h2>Unrelated room</h2><p>Other-card facts</p></article></main>`);
    const target = observe().elements.find((item: any) => item.label === 'Compare');
    expect(target.group).toBe('Public boot');
    expect(target.context.length).toBeLessThanOrEqual(500);
    expect(target.context).toContain('[이메일 숨김]');
    for (const value of ['Private', 'Hidden account', 'person@example.com', 'Unrelated room', 'Other-card facts']) expect(target.context).not.toContain(value);
  });

  it('reports current headings, form purpose, option and checkbox state without field values', () => {
    const { observe } = page(`<main><h1>Trail boots</h1><form aria-label="Filters" action="/catalog">
      <label>Size<select><option value="small">Small</option><option value="large" selected>Large</option></select></label>
      <label><input type="checkbox" checked>Waterproof</label>
      <label>Notes<textarea>Private draft</textarea></label></form></main>`);
    const snapshot = observe();
    expect(snapshot.headings).toEqual([{ level: 1, text: 'Trail boots' }]);
    expect(snapshot.forms).toEqual([{ label: 'Filters', method: 'get', action: 'https://shop.example.com/catalog' }]);
    expect(snapshot.elements.find((item: any) => item.label === 'Size')).toMatchObject({ role: 'combobox', options: [
      { value: 'small', label: 'Small', selected: false, disabled: false },
      { value: 'large', label: 'Large', selected: true, disabled: false },
    ] });
    expect(snapshot.elements.find((item: any) => item.label === 'Waterproof')).toMatchObject({ checked: true, role: 'checkbox' });
    expect(snapshot.elements.find((item: any) => item.label === 'Notes')).toMatchObject({ editable: true, hasValue: true });
    expect(JSON.stringify(snapshot)).not.toContain('Private draft');
  });

  it('serializes a bounded HTML sketch with relation structure and omits scripts, arbitrary attributes, tokens and account areas', () => {
    const { observe } = page(`<main><h1>Boots</h1><table><tr><th>Price</th><td>$99</td></tr></table>
      <a href="/boots" onclick="secretOperation()" data-token="hidden-api-secret">Details</a>
      <a href="/boots?token=private-session-token">Secret link</a>
      <a href="/search?q=person%40example.com">Encoded contact link</a>
      <script>window.secret = 'script-private-secret'</script><input type="hidden" value="hidden-secret">
      <div class="account-menu">Alice's account data</div><p hidden>Hidden details</p></main>`);
    const snapshot = observe();
    expect(snapshot.html).toContain('<table><tbody><tr><th>Price</th><td>$99</td></tr></tbody></table>');
    expect(snapshot.html).toContain('href="https://shop.example.com/boots"');
    for (const secret of ['secretOperation', 'hidden-api-secret', 'private-session-token', 'person%40example.com', 'script-private-secret', 'hidden-secret', "Alice's", 'Hidden details']) {
      expect(JSON.stringify(snapshot)).not.toContain(secret);
    }
  });

  it('detects SPA changes while keeping the document and unchanged element identity stable', () => {
    const { observe, window } = page('<main><h1>Results</h1><button>Filter</button></main>');
    const before = observe();
    expect(observe().changes.domChanged).toBe(false);
    window.document.querySelector('main')!.appendChild(window.document.createElement('p')).textContent = 'New result';
    const after = observe();
    expect(after.documentId).toBe(before.documentId);
    expect(after.elements[0].id).toBe(before.elements[0].id);
    expect(after.revision).toBeGreaterThan(before.revision);
    expect(after.changes.domChanged).toBe(true);
    expect(after.text).toContain('New result');
    expect(after.readiness.domQuietMs).toBeLessThan(100);
  });

  it('detects visible loading state and includes dialog status outside the main element', () => {
    const { observe } = page('<main><h1>Results</h1><div aria-busy="true">Loading</div></main><div role="status">Saved successfully</div>');
    expect(observe().readiness.pending).toBe(true);
    expect(observe().text).toContain('Saved successfully');
  });

  it('prioritizes newly visible controls and text on a long page', () => {
    const { window, observe } = page(`<main>${Array.from({ length: 100 }, (_, i) => `<p><a href="/item/${i}">Item ${i}</a>${'description '.repeat(30)}</p>`).join('')}</main>`);
    for (const element of window.document.querySelectorAll('p,a')) {
      const current = element.textContent!.includes('Item 99');
      const top = current ? 10 : -500;
      Object.defineProperty(element, 'getBoundingClientRect', { value: () => ({ x: 0, y: top, top, left: 0, right: 100, bottom: top + 20, width: 100, height: 20 }) });
    }
    const snapshot = observe();
    expect(snapshot.elements).toHaveLength(80);
    expect(snapshot.elements[0].label).toBe('Item 99');
    expect(snapshot.text.startsWith('Item 99')).toBe(true);
    expect(snapshot.text.length).toBeLessThanOrEqual(10000);
    expect(snapshot.html.length).toBeLessThanOrEqual(7000);
  });

  it.each(['<input type="password">', '<input autocomplete="one-time-code">', '<input autocomplete="cc-number">'])('stops observation at sensitive credential or payment controls: %s', input => {
    const { observe } = page(`<main><h1>Protected</h1>${input}<p>Secret content</p></main>`);
    expect(observe()).toMatchObject({ sensitive: true, text: '', html: '', elements: [] });
  });

  it('excludes private identity fields while preserving a public message input', () => {
    const { observe } = page('<label>Email<input type="email" value="person@example.com"></label><label>Phone<input name="phone"></label><label>Message<textarea></textarea></label>');
    expect(observe().elements.map((item: any) => item.label)).toEqual(['Message']);
    expect(JSON.stringify(observe())).not.toContain('person@example.com');
  });
});

describe('Android page agent guarded actions and readback', () => {
  it('rejects a reused button node when its owning product changes between observe and execute', () => {
    const { agent, observe, window } = page('<article><h2>Trail Alpha</h2><p>$90</p><button>Compare</button></article>');
    const snapshot = observe();
    const button = window.document.querySelector('button');
    window.document.querySelector('h2')!.textContent = 'Trail Beta';
    expect(window.document.querySelector('button')).toBe(button);
    expect(agent.action({ type: 'click', targetId: snapshot.elements[0].id, approved: true }, window.location.href, snapshot.documentId)).toMatchObject({ ok: false, code: 'STALE_TARGET' });
  });

  it('rechecks changed price context but does not invalidate a button for unrelated sibling-card updates', () => {
    const { agent, observe, window } = page('<main><article><h2>Trail Alpha</h2><p id="price">$90</p><button>Compare</button></article><article><h2>Trail Beta</h2><p id="other">$120</p><button>Compare</button></article></main>');
    let snapshot = observe();
    window.document.getElementById('other')!.textContent = '$100';
    expect(agent.action({ type: 'click', targetId: snapshot.elements[0].id, approved: true }, window.location.href, snapshot.documentId).ok).toBe(true);
    snapshot = observe();
    window.document.getElementById('price')!.textContent = '$140';
    expect(agent.action({ type: 'click', targetId: snapshot.elements[0].id, approved: true }, window.location.href, snapshot.documentId)).toMatchObject({ ok: false, code: 'STALE_TARGET' });
  });

  it('requires native approval for public form text and observes only local value-match verification', () => {
    const { observe, act, target, window } = page('<label>Message<textarea></textarea></label>');
    const field = target('Message');
    expect(act({ type: 'type', targetId: field.id, text: 'Please compare the blue and red models.' })).toMatchObject({ ok: false, code: 'APPROVAL_REQUIRED' });
    expect(act({ type: 'type', targetId: field.id, text: 'Please compare the blue and red models.', approved: true })).toMatchObject({ ok: true, verified: true, changed: true });
    expect(window.document.querySelector('textarea')!.value).toBe('Please compare the blue and red models.');
    const snapshot = observe();
    expect(snapshot.elements[0].valueMatchesLastInput).toBe(true);
    expect(JSON.stringify(snapshot)).not.toContain('Please compare');
  });

  it('allows public search text without approval but prevents auto-submit of general forms', () => {
    const { act, target } = page('<input type="search" aria-label="Search"><textarea aria-label="Message"></textarea>');
    expect(act({ type: 'type', targetId: target('Search').id, text: 'waterproof boots' })).toMatchObject({ ok: true, verified: true });
    expect(act({ type: 'type', targetId: target('Message').id, text: 'Draft', submit: true, approved: true })).toMatchObject({ ok: false });
  });

  it('does not mistake a hotel search field for a telephone field', () => {
    const { act, target } = page('<input type="search" aria-label="Hotel search">');
    expect(act({ type: 'type', targetId: target('Hotel search').id, text: 'Seoul' }).ok).toBe(true);
  });

  it.each(['person@example.com', 'sk-abcdefghijklmnop', '010-1234-5678'])('refuses sensitive input even with approval: %s', text => {
    const { act, target } = page('<textarea aria-label="Message"></textarea>');
    expect(act({ type: 'type', targetId: target('Message').id, text, approved: true }).ok).toBe(false);
  });

  it('selects an explicitly requested option after approval and reports the new state', () => {
    const { act, target, observe } = page('<select aria-label="Size"><option value="s">Small</option><option value="l">Large</option></select>');
    const id = target('Size').id;
    expect(act({ type: 'select', targetId: id, value: 'l' })).toMatchObject({ ok: false, code: 'APPROVAL_REQUIRED' });
    expect(act({ type: 'select', targetId: id, value: 'l', approved: true })).toMatchObject({ ok: true, verified: true, changed: true });
    expect(observe().elements[0].options[1].selected).toBe(true);
  });

  it('checks and unchecks state idempotently and does not reverse an already correct checkbox', () => {
    const { act, target } = page('<label><input type="checkbox">Waterproof</label>');
    const id = target('Waterproof').id;
    expect(act({ type: 'check', targetId: id, checked: true, approved: true })).toMatchObject({ ok: true, changed: true, verified: true });
    expect(act({ type: 'check', targetId: id, checked: true, approved: true })).toMatchObject({ ok: true, changed: false, verified: true });
    expect(act({ type: 'check', targetId: id, checked: false, approved: true })).toMatchObject({ ok: true, changed: true, verified: true });
  });

  it('detects input rejection by the page instead of reporting a successful change', () => {
    const { act, target, window } = page('<textarea aria-label="Message"></textarea>');
    window.document.querySelector('textarea')!.addEventListener('input', event => { (event.target as HTMLTextAreaElement).value = ''; });
    expect(act({ type: 'type', targetId: target('Message').id, text: 'Draft', approved: true })).toMatchObject({ ok: false, code: 'NO_EFFECT', retryable: true });
  });

  it('verifies custom switch states and rejects a click that does not update them', () => {
    const { act, target, window } = page('<div role="switch" aria-label="Waterproof" aria-checked="false"></div>');
    expect(act({ type: 'check', targetId: target('Waterproof').id, checked: true, approved: true })).toMatchObject({ ok: false, code: 'NO_EFFECT' });
    window.document.querySelector('[role="switch"]')!.addEventListener('click', event => (event.target as HTMLElement).setAttribute('aria-checked', 'true'));
    expect(act({ type: 'check', targetId: target('Waterproof').id, checked: true, approved: true })).toMatchObject({ ok: true, verified: true });
  });

  it('detects selection rejection by the page', () => {
    const { act, target, window } = page('<select aria-label="Size"><option value="s">Small</option><option value="l">Large</option></select>');
    window.document.querySelector('select')!.addEventListener('change', event => { (event.target as HTMLSelectElement).value = 's'; });
    expect(act({ type: 'select', targetId: target('Size').id, value: 'l', approved: true })).toMatchObject({ ok: false, code: 'NO_EFFECT' });
  });

  it('refuses a disabled option and ambiguous option labels', () => {
    const { act, target } = page('<select aria-label="Size"><option value="s">Small</option><option value="l" disabled>Large</option><option value="x">Same</option><option value="y">Same</option></select>');
    for (const value of ['l', 'Same']) expect(act({ type: 'select', targetId: target('Size').id, value, approved: true }).ok).toBe(false);
  });

  it('invalidates a replaced target rather than clicking its replacement', () => {
    const { agent, observe, window } = page('<button>Expand</button>');
    const snapshot = observe();
    window.document.querySelector('button')!.outerHTML = '<button>Expand</button>';
    expect(agent.action({ type: 'click', targetId: snapshot.elements[0].id, approved: true }, window.location.href, snapshot.documentId)).toMatchObject({ ok: false, code: 'STALE_TARGET', retryable: true });
  });

  it('detects changed option lists and changed user values before executing an old action', () => {
    const { agent, observe, window } = page('<select aria-label="Size"><option value="s">Small</option></select><textarea aria-label="Message"></textarea>');
    const snapshot = observe();
    window.document.querySelector('select')!.appendChild(window.document.createElement('option')).textContent = 'Large';
    window.document.querySelector('textarea')!.value = 'User draft';
    for (const item of snapshot.elements) expect(agent.action({ type: item.tag === 'select' ? 'select' : 'type', targetId: item.id, value: 's', text: 'Draft', approved: true }, window.location.href, snapshot.documentId).code).toBe('STALE_TARGET');
  });

  it('rejects old document identity even when URL remains the same', () => {
    const { agent, observe, window } = page('<button>Expand</button>');
    const snapshot = observe();
    expect(agent.action({ type: 'click', targetId: snapshot.elements[0].id, approved: true }, window.location.href, 'different-document')).toMatchObject({ ok: false, code: 'STALE_DOCUMENT' });
  });

  it('refuses overlay-covered controls instead of bypassing the overlay with click()', () => {
    const { act, target, window } = page('<button>Expand</button><div role="dialog">Modal overlay</div>');
    const click = jest.fn();
    window.document.querySelector('button')!.addEventListener('click', click);
    Object.defineProperty(window.document, 'elementFromPoint', { value: () => window.document.querySelector('[role="dialog"]') });
    expect(act({ type: 'click', targetId: target('Expand').id, approved: true })).toMatchObject({ ok: false, code: 'OBSCURED' });
    expect(click).not.toHaveBeenCalled();
  });

  it('does not click a target that moved while a decision was pending', () => {
    const { agent, observe, window } = page('<button>Expand</button>');
    const snapshot = observe();
    Object.defineProperty(window.document.querySelector('button'), 'getBoundingClientRect', { value: () => ({ x: 10, y: 200, top: 200, left: 10, right: 210, bottom: 230, width: 200, height: 30 }) });
    expect(agent.action({ type: 'click', targetId: snapshot.elements[0].id, approved: true }, window.location.href, snapshot.documentId)).toMatchObject({ ok: false, code: 'NOT_READY' });
  });

  it('reports disabled controls but prevents executing them', () => {
    const { act, target } = page('<fieldset disabled><button>Apply</button></fieldset>');
    expect(target('Apply').disabled).toBe(true);
    expect(act({ type: 'click', targetId: target('Apply').id, approved: true })).toMatchObject({ ok: false, code: 'NOT_READY' });
  });

  it('opens target blank links in the current WebView and restores the original DOM attribute', () => {
    const { act, target, window } = page('<a href="/boots" target="_blank">Boots</a>');
    let duringClick: string | null = null;
    const anchor = window.document.querySelector('a')!;
    anchor.addEventListener('click', event => { event.preventDefault(); duringClick = anchor.getAttribute('target'); });
    expect(act({ type: 'click', targetId: target('Boots').id })).toMatchObject({
      ok: true,
      navigating: false,
      effect: 'activated',
    });
    expect(duringClick).toBe('_self');
    expect(anchor.target).toBe('_blank');
  });

  it('overrides an inherited base target while activating a link', () => {
    const { act, target, window } = page('<base target="_blank"><a href="/boots">Boots</a>');
    const anchor = window.document.querySelector('a')!;
    let duringClick: string | null = null;
    anchor.addEventListener('click', event => { event.preventDefault(); duringClick = anchor.getAttribute('target'); });
    expect(act({ type: 'click', targetId: target('Boots').id }).ok).toBe(true);
    expect(duringClick).toBe('_self');
    expect(anchor.hasAttribute('target')).toBe(false);
  });

  it('scrolls an explicit nested pane and allows focused inspection on the next observation', () => {
    const { window, target, act, observe } = page('<section aria-label="Results" style="overflow-y:auto"><p>Nested result details</p></section>');
    const section = window.document.querySelector('section')!;
    Object.defineProperty(section, 'clientHeight', { value: 200 });
    Object.defineProperty(section, 'scrollHeight', { value: 1200 });
    const id = target('Results').id;
    expect(target('Results')).toMatchObject({ scrollTop: 0, scrollHeight: 1200, clientHeight: 200 });
    expect(act({ type: 'scroll', targetId: id, direction: 'down' })).toMatchObject({ ok: true, changed: true });
    expect(section.scrollTop).toBe(140);
    expect(target('Results').scrollTop).toBe(140);
    expect(act({ type: 'inspect', targetId: id }).ok).toBe(true);
    expect(observe().detail.text).toContain('Nested result details');
  });

  it('never exposes final payment/order controls as action candidates', () => {
    const { observe } = page('<button>Pay now</button><button>주문 확정</button><button>Compare products</button>');
    const snapshot = observe();
    expect(snapshot.paymentRequired).toBe(true);
    expect(snapshot.elements.map((item: any) => item.label)).toEqual(['주문 확정', 'Compare products']);
  });
});
