/** @jest-environment node */
/// <reference types="node" />

import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { JSDOM } from 'jsdom';

const PAGE_AGENT_SOURCE = readFileSync(
  resolve(__dirname, '../ios/Resources/VitlanePageAgent.js'),
  'utf8',
);

type PageAgent = {
  setControlGeneration(generation: number): boolean;
  observe(generation: number): Record<string, unknown>;
  prepareSearch(args: Record<string, unknown>): Record<string, unknown>;
  executeCoupangPreparation(args: Record<string, unknown>): Record<string, unknown>;
  scroll(args: Record<string, unknown>): Record<string, unknown>;
  executeRecipe?: unknown;
  openCandidate?: unknown;
};

function page(html: string, url = 'https://shop.example.com/products/trail-boot') {
  const dom = new JSDOM(html, { url, runScripts: 'outside-only', pretendToBeVisual: true });
  const rectangle = {
    x: 0,
    y: 0,
    top: 0,
    left: 0,
    right: 100,
    bottom: 24,
    width: 100,
    height: 24,
    toJSON: () => ({}),
  };
  Object.defineProperty(dom.window.HTMLElement.prototype, 'getBoundingClientRect', {
    configurable: true,
    value: () => rectangle,
  });
  Object.defineProperty(dom.window, 'scrollBy', {
    configurable: true,
    value: jest.fn(),
  });
  dom.window.eval(PAGE_AGENT_SOURCE);
  const agent = (dom.window as unknown as { __vitlaneAgent: PageAgent }).__vitlaneAgent;
  expect(agent.setControlGeneration(7)).toBe(true);
  return { dom, agent };
}

describe('VitlanePageAgent privacy boundary', () => {
  it('does not treat an anonymous login link as an authenticated session', () => {
    const { agent } = page(`
      <title>Trail Shop</title>
      <main>
        <h1>Summer trail gear</h1>
        <p>Waterproof boots for day hikes.</p>
        <a href="/login">Log in</a>
        <a href="/products/trail-boot">Trail boot details</a>
      </main>
    `);

    const observation = agent.observe(7) as any;
    expect(observation.sensitivePage).toBe(false);
    expect(observation.visibleText).toContain('Waterproof boots');
    expect(observation.visibleText).not.toContain('Log in');
    expect(observation.sessionStateHint).toBe('anonymous');
    expect(observation.candidates).toHaveLength(1);
    expect(observation.candidates[0]).toMatchObject({ kind: 'safe_link', name: 'Trail boot details' });
    expect(observation.excludedBoundaryCodes).toContain('UNSUPPORTED_INTERACTION');
  });

  it('returns an empty observation for authenticated account signals', () => {
    const { agent } = page(`
      <title>Store</title>
      <main>
        <h1>My Account</h1>
        <p>Order 123 belongs to person@example.com.</p>
        <a href="/logout">Sign out</a>
      </main>
    `, 'https://shop.example.com/home');

    const observation = agent.observe(7) as any;
    expect(observation).toMatchObject({
      sensitivePage: true,
      blockedReason: 'authenticated',
      visibleText: '',
      candidates: [],
    });
  });

  it('observes a signed-in public product while excluding account and logout chrome', () => {
    const { agent } = page(`
      <title>Trail Boot</title>
      <header class="site-header account-menu">
        <p>Signed in as person@example.com</p>
        <a href="/orders">Order history</a>
        <button aria-label="Log out">Log out</button>
      </header>
      <main>
        <h1>Trail Boot</h1>
        <p>Waterproof shell with a grippy sole.</p>
        <a href="/products/trail-boot/details">Product details</a>
      </main>
    `);

    const observation = agent.observe(7) as any;
    expect(observation.sensitivePage).toBe(false);
    expect(observation.sessionStateHint).toBe('authenticated');
    expect(observation.visibleText).toContain('Waterproof shell');
    expect(observation.visibleText).not.toContain('person@example.com');
    expect(observation.visibleText).not.toContain('Order history');
    expect(observation.visibleText).not.toContain('Log out');
    expect(observation.candidates).toEqual([
      expect.objectContaining({ kind: 'safe_link', name: 'Product details' }),
    ]);
    expect(JSON.stringify(observation.candidates)).not.toContain('/orders');
    expect(observation.excludedBoundaryCodes).toContain('UNSUPPORTED_INTERACTION');
  });

  it.each([
    ['account', 'https://shop.example.com/account', '<h1>Welcome back</h1>'],
    ['session', 'https://shop.example.com/session', '<h1>Active devices</h1>'],
    ['cart', 'https://shop.example.com/cart', '<h1>Review selected items</h1>'],
  ])('returns an empty observation for private %s pages', (_kind, url, content) => {
    const { agent } = page(`
      <title>Store</title>
      <main>
        ${content}
        <p>Private order details for person@example.com.</p>
      </main>
    `, url);

    const observation = agent.observe(7) as any;
    expect(observation).toMatchObject({
      sensitivePage: true,
      blockedReason: 'sensitive_page',
      visibleText: '',
      candidates: [],
    });
    expect(JSON.stringify(observation)).not.toContain('person@example.com');
  });

  it('omits commitment controls and cross-origin frames without hiding public product copy', () => {
    const { agent } = page(`
      <title>Trail Boot</title>
      <main>
        <h1>Trail Boot</h1>
        <p>Waterproof shell with a grippy sole.</p>
        <p>Verification code: 123456</p>
        <button>Buy now</button>
        <iframe src="https://payments.example/offer"></iframe>
      </main>
    `);

    const observation = agent.observe(7) as any;
    expect(observation.sensitivePage).toBe(false);
    expect(observation.visibleText).toContain('Waterproof shell');
    expect(observation.visibleText).not.toContain('Buy now');
    expect(observation.visibleText).not.toContain('123456');
    expect(observation.excludedBoundaryCodes).toEqual(
      expect.arrayContaining(['PAYMENT_OR_COMMITMENT', 'CROSS_ORIGIN_FRAME']),
    );
  });

  it('stages a public query without focus, events, or submission and then requires re-observation', () => {
    const { dom, agent } = page(`
      <title>Catalog</title>
      <form method="get" action="/search">
        <input id="query" type="search" placeholder="Search products" />
        <button type="submit">Search</button>
      </form>
      <p>Public catalog copy.</p>
    `, 'https://shop.example.com/catalog');
    const input = dom.window.document.querySelector<HTMLInputElement>('#query')!;
    const form = input.form!;
    const events = { focus: 0, input: 0, change: 0, submit: 0 };
    input.addEventListener('focus', () => events.focus++);
    input.addEventListener('input', () => events.input++);
    input.addEventListener('change', () => events.change++);
    form.addEventListener('submit', event => {
      events.submit++;
      event.preventDefault();
    });

    const observation = agent.observe(7) as any;
    const candidate = observation.candidates.find((item: any) => item.kind === 'public_search');
    expect(candidate).toBeDefined();
    const args = {
      generation: 7,
      observationId: observation.observationId,
      candidateRef: candidate.candidateRef,
      query: 'waterproof hiking shoes',
    };
    expect(agent.prepareSearch(args)).toEqual({ applied: true, code: 'PUBLIC_SEARCH_PREPARED' });
    expect(input.value).toBe('waterproof hiking shoes');
    expect(events).toEqual({ focus: 0, input: 0, change: 0, submit: 0 });
    expect(() => agent.prepareSearch(args)).toThrow('replay_conflict');

    const nextObservation = agent.observe(7) as any;
    expect(nextObservation.sensitivePage).toBe(true);
    expect(nextObservation.visibleText).toBe('');
    expect(JSON.stringify(nextObservation)).not.toContain('waterproof hiking shoes');
  });

  it('rechecks the stored candidate fingerprint and exposes no generic recipe executor', () => {
    const { dom, agent } = page(`
      <form method="get" action="/search">
        <input id="query" type="search" placeholder="Search products" />
      </form>
    `, 'https://shop.example.com/catalog');
    const observation = agent.observe(7) as any;
    const candidate = observation.candidates[0];
    dom.window.document.querySelector('#query')!.setAttribute('placeholder', 'Changed after observation');

    expect(() =>
      agent.prepareSearch({
        generation: 7,
        observationId: observation.observationId,
        candidateRef: candidate.candidateRef,
        query: 'trail shoes',
      }),
    ).toThrow('target_changed');
    expect(agent.executeRecipe).toBeUndefined();
    expect(agent.openCandidate).toBeUndefined();
  });

  it('sets an approved Coupang quantity and requires a new observation before another step', () => {
    const { dom, agent } = page(`
      <title>쿠팡 상품</title>
      <main>
        <h1>승인된 상품</h1>
        <div class="total-price"><strong>12,000원</strong></div>
        <form>
          <input type="number" aria-label="수량" value="1" />
          <button type="button">장바구니 담기</button>
          <button type="button">바로구매</button>
        </form>
      </main>
    `, 'https://www.coupang.com/vp/products/12345?itemId=23456&vendorItemId=34567');
    const observation = agent.observe(7) as any;
    expect(observation).toMatchObject({ sensitivePage: false, recipeSurface: 'coupang_product' });
    expect(observation.visibleText).not.toContain('바로구매');

    const args = {
      generation: 7,
      observationId: observation.observationId,
      stepId: 'set_quantity',
      expectedOrigin: 'https://www.coupang.com',
      expectedPath: '/vp/products/12345',
      productId: '12345',
      itemId: '23456',
      vendorItemId: '34567',
      quantity: 2,
      unitPriceCeilingKrw: 12000,
      linePriceCeilingKrw: 24000,
    };
    expect(agent.executeCoupangPreparation(args)).toEqual({ applied: true, code: 'COUPANG_QUANTITY_SET' });
    expect(dom.window.document.querySelector<HTMLInputElement>('input[aria-label="수량"]')?.value).toBe('2');
    expect(() => agent.executeCoupangPreparation(args)).toThrow('replay_conflict');
  });

  it('selects only one exact option inside the approved Korean option group', () => {
    const { dom, agent } = page(`
      <title>쿠팡 상품</title>
      <main>
        <h1>승인된 상품</h1>
        <div class="total-price">18,000원</div>
        <fieldset>
          <legend>색상</legend>
          <label><input id="black" type="radio" name="color" />블랙</label>
          <label><input id="white" type="radio" name="color" />화이트</label>
        </fieldset>
        <input type="number" aria-label="수량" value="1" />
        <button type="button">바로구매</button>
      </main>
    `, 'https://www.coupang.com/vp/products/12345?itemId=23456&vendorItemId=34567');
    const radio = dom.window.document.querySelector<HTMLInputElement>('#black')!;
    radio.addEventListener('click', () => {
      radio.checked = true;
      radio.setAttribute('aria-checked', 'true');
    });
    const observation = agent.observe(7) as any;
    expect(agent.executeCoupangPreparation({
      generation: 7,
      observationId: observation.observationId,
      stepId: 'select_option',
      expectedOrigin: 'https://www.coupang.com',
      expectedPath: '/vp/products/12345',
      productId: '12345',
      itemId: '23456',
      vendorItemId: '34567',
      quantity: 1,
      unitPriceCeilingKrw: 18000,
      linePriceCeilingKrw: 18000,
      groupName: '색상',
      valueName: '블랙',
    })).toEqual({ applied: true, code: 'COUPANG_OPTION_SELECTED' });
    expect(radio.checked).toBe(true);
  });

  it('revalidates offer identity, quantity, KRW ceiling, and exact Buy now control before activation', () => {
    const { dom, agent } = page(`
      <title>쿠팡 상품</title>
      <main>
        <h1>승인된 상품</h1>
        <div class="total-price">9,900원</div>
        <input type="number" aria-label="수량" value="1" />
        <button id="buy" type="button">바로구매</button>
        <button type="button">결제하기</button>
      </main>
    `, 'https://www.coupang.com/vp/products/12345?itemId=23456&vendorItemId=34567');
    const clicked = jest.fn();
    dom.window.document.querySelector('#buy')?.addEventListener('click', clicked);
    const observation = agent.observe(7) as any;
    expect(agent.executeCoupangPreparation({
      generation: 7,
      observationId: observation.observationId,
      stepId: 'buy_now',
      approvedPreparation: { items: [{ options: { kind: 'none' } }] },
      targetLineIndex: 0,
      expectedOrigin: 'https://www.coupang.com',
      expectedPath: '/vp/products/12345',
      productId: '12345',
      itemId: '23456',
      vendorItemId: '34567',
      quantity: 1,
      unitPriceCeilingKrw: 10000,
      linePriceCeilingKrw: 10000,
    })).toEqual({ applied: true, code: 'COUPANG_BUY_NOW_TRIGGERED' });
    expect(clicked).toHaveBeenCalledTimes(1);
  });

  it('rejects a Coupang action when the price exceeds the approved ceiling', () => {
    const { agent } = page(`
      <title>쿠팡 상품</title>
      <main>
        <h1>승인된 상품</h1>
        <div class="total-price">19,900원</div>
        <input type="number" aria-label="수량" value="1" />
        <button type="button">장바구니 담기</button>
      </main>
    `, 'https://www.coupang.com/vp/products/12345?itemId=23456&vendorItemId=34567');
    const observation = agent.observe(7) as any;
    expect(() => agent.executeCoupangPreparation({
      generation: 7,
      observationId: observation.observationId,
      stepId: 'add_to_cart',
      approvedPreparation: { items: [{ options: { kind: 'none' } }] },
      targetLineIndex: 0,
      expectedOrigin: 'https://www.coupang.com',
      expectedPath: '/vp/products/12345',
      productId: '12345',
      itemId: '23456',
      vendorItemId: '34567',
      quantity: 1,
      unitPriceCeilingKrw: 10000,
      linePriceCeilingKrw: 10000,
    })).toThrow('price_ceiling_exceeded');
  });

  it('rejects duplicate Coupang offer identity query parameters', () => {
    const { dom, agent } = page(`
      <title>쿠팡 상품</title>
      <main>
        <h1>승인된 상품</h1>
        <div class="total-price">9,900원</div>
        <input type="number" aria-label="수량" value="1" />
        <button id="buy" type="button">바로구매</button>
      </main>
    `, 'https://www.coupang.com/vp/products/12345?itemId=23456&itemId=99999&vendorItemId=34567');
    const clicked = jest.fn();
    dom.window.document.querySelector('#buy')?.addEventListener('click', clicked);
    const observation = agent.observe(7) as any;
    expect(() => agent.executeCoupangPreparation({
      generation: 7,
      observationId: observation.observationId,
      stepId: 'buy_now',
      approvedPreparation: { items: [{ options: { kind: 'none' } }] },
      targetLineIndex: 0,
      expectedOrigin: 'https://www.coupang.com',
      expectedPath: '/vp/products/12345',
      productId: '12345',
      itemId: '23456',
      vendorItemId: '34567',
      quantity: 1,
      unitPriceCeilingKrw: 10000,
      linePriceCeilingKrw: 10000,
    })).toThrow('offer_identity_mismatch');
    expect(clicked).not.toHaveBeenCalled();
  });

  it('starts checkout only when the selected Coupang cart exactly matches approved lines', () => {
    const { dom, agent } = page(`
      <title>장바구니 | 쿠팡</title>
      <main>
        <h1>장바구니</h1>
        <article class="cart-item">
          <input type="checkbox" checked />
          <a href="https://www.coupang.com/vp/products/12345?itemId=23456&amp;vendorItemId=34567">승인된 상품</a>
          <input type="number" aria-label="수량" value="2" />
          <strong data-vitlane-unit-price-krw="9900">9,900원</strong>
        </article>
        <button id="checkout" type="button">1개 상품 구매하기</button>
      </main>
    `, 'https://cart.coupang.com/cartView.pang');
    const clicked = jest.fn();
    dom.window.document.querySelector('#checkout')?.addEventListener('click', clicked);
    const observation = agent.observe(7) as any;
    expect(observation).toMatchObject({
      sensitivePage: false,
      recipeSurface: 'coupang_cart',
      visibleText: '',
      candidates: [],
    });
    expect(agent.executeCoupangPreparation({
      generation: 7,
      observationId: observation.observationId,
      stepId: 'start_checkout',
      expectedOrigin: 'https://cart.coupang.com',
      expectedPath: '/cartView.pang',
      approval: { totalPriceCeilingKrw: 20000 },
      approvedLines: [{
        productId: '12345',
        itemId: '23456',
        vendorItemId: '34567',
        quantity: 2,
        unitPriceCeilingKrw: 9900,
        linePriceCeilingKrw: 20000,
      }],
    })).toEqual({ applied: true, code: 'COUPANG_CHECKOUT_TRIGGERED' });
    expect(clicked).toHaveBeenCalledTimes(1);
  });

  it('rejects ambiguous Coupang cart prices instead of accepting a smaller coupon amount', () => {
    const { agent } = page(`
      <title>장바구니 | 쿠팡</title>
      <main>
        <h1>장바구니</h1>
        <article class="cart-item">
          <input type="checkbox" checked />
          <a href="https://www.coupang.com/vp/products/12345?itemId=23456&amp;vendorItemId=34567">승인된 상품</a>
          <input type="number" aria-label="수량" value="2" />
          <strong data-vitlane-unit-price-krw="14900">14,900원</strong>
          <span data-testid="coupon-price">1,000원</span>
        </article>
        <button type="button">1개 상품 구매하기</button>
      </main>
    `, 'https://cart.coupang.com/cartView.pang');
    const observation = agent.observe(7) as any;
    expect(() => agent.executeCoupangPreparation({
      generation: 7,
      observationId: observation.observationId,
      stepId: 'start_checkout',
      expectedOrigin: 'https://cart.coupang.com',
      expectedPath: '/cartView.pang',
      approval: { totalPriceCeilingKrw: 20000 },
      approvedLines: [{
        productId: '12345',
        itemId: '23456',
        vendorItemId: '34567',
        quantity: 2,
        unitPriceCeilingKrw: 9900,
        linePriceCeilingKrw: 20000,
      }],
    })).toThrow('cart_line_unverified');
  });

  it('hands off an order/payment page instead of treating checkout as a cart review', () => {
    const { agent } = page(`
      <title>주문/결제 | 쿠팡</title>
      <main><h1>주문/결제</h1><button>결제하기</button></main>
    `, 'https://checkout.coupang.com/');
    expect(agent.observe(7)).toMatchObject({
      sensitivePage: true,
      blockedReason: 'payment',
      visibleText: '',
      candidates: [],
    });
  });
});
