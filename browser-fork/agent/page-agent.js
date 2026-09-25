// Runs in a Chromium isolated world. The native host must verify the command's
// HMAC permit, lease, sequence and control generation before calling execute.
// The Android host keeps the current regular Chromium profile so a user can
// sign in and continue in the same browser session. Cookies and storage are
// never exported by this agent. Private account/session/cart/payment pages fail
// closed, while account/profile chrome is removed from public product pages.
(() => {
  if (globalThis.__laneAgent) return;

  let state = null;
  const clean = (value, max) => String(value ?? '').replace(/\s+/g, ' ').trim().slice(0, max);
  const record = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);
  const id = (value) => typeof value === 'string' && /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/.test(value);
  const randomId = (prefix) => `${prefix}_${Array.from(crypto.getRandomValues(new Uint8Array(16)),
    (byte) => byte.toString(16).padStart(2, '0')).join('')}`;

  function visible(element) {
    const style = getComputedStyle(element);
    return element.getClientRects().length > 0 && style.visibility !== 'hidden' && style.display !== 'none' &&
      !element.closest('[aria-hidden="true"],[inert]');
  }

  function privateIpv4(host) {
    const parts = host.split('.').map(Number);
    if (parts.length !== 4 || parts.some((part) => !Number.isInteger(part) || part < 0 || part > 255)) return true;
    const [a, b] = parts;
    return a === 0 || a === 10 || a === 127 || a >= 224 ||
      (a === 100 && b >= 64 && b <= 127) || (a === 169 && b === 254) ||
      (a === 172 && b >= 16 && b <= 31) || (a === 192 && (b === 0 || b === 168)) ||
      (a === 198 && (b === 18 || b === 19 || b === 51)) || (a === 203 && b === 0);
  }

  function publicHost(hostname) {
    const host = hostname.replace(/^\[|\]$/g, '').toLowerCase().replace(/\.$/, '');
    if (!host || host === 'localhost' || /(?:^|\.)(?:localhost|local|internal|lan|home\.arpa|test|invalid|example)$/.test(host)) return false;
    if (/^\d+(?:\.\d+){3}$/.test(host)) return !privateIpv4(host);
    if (host.includes(':')) {
      if (host === '::' || host === '::1' || host.startsWith('::') || host.startsWith('fc') ||
          host.startsWith('fd') || /^fe[89ab]/.test(host)) return false;
      return true;
    }
    return host.includes('.');
  }

  function publicUrl(value, { allowQuery = false } = {}) {
    let url;
    try { url = new URL(value, location.href); } catch { throw new Error('url_policy_denied'); }
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.port ||
        !publicHost(url.hostname) || (!allowQuery && (url.search || url.hash))) {
      throw new Error('url_policy_denied');
    }
    return url.href;
  }

  function publicOrigin(value) {
    let url;
    try { url = new URL(value); } catch { throw new Error('url_policy_denied'); }
    if (value !== url.origin && value !== `${url.origin}/`) throw new Error('url_policy_denied');
    publicUrl(`${url.origin}/`);
    return url.origin;
  }

  function accessibleName(element) {
    const labelledBy = (element.getAttribute('aria-labelledby') || '').split(/\s+/).filter(Boolean)
      .map((labelId) => document.getElementById(labelId)?.textContent || '').join(' ');
    const labels = Array.from(element.labels || []).map((label) => label.innerText).join(' ');
    return clean(element.getAttribute('aria-label') || labelledBy || labels || element.innerText ||
      (element.matches?.('input[type="button"],input[type="submit"]') ? element.value : '') ||
      element.placeholder || element.getAttribute('title') || '', 200);
  }

  function sensitiveInput(element) {
    const hints = [element.type, element.autocomplete, element.name, element.id, accessibleName(element)].join(' ');
    return /password|passwd|passcode|(?:^|\s)file(?:\s|$)|one-time-code|otp|cc-|credit.?card|card.?number|cvv|cvc|secret|token|비밀번호|인증번호|카드.?번호|보안.?코드/i.test(hints);
  }

  function hasUserInputValue(element) {
    if (!['INPUT', 'TEXTAREA'].includes(element.tagName)) return false;
    if (element.tagName === 'INPUT' && ['button', 'submit', 'reset', 'checkbox', 'radio', 'image', 'range', 'color', 'hidden'].includes(element.type)) return false;
    return element.value !== '';
  }

  function paymentMeaning(value) {
    return /(?:\b(?:add\s*to\s*(?:cart|bag|basket)|buy\s*now|checkout|place\s*order|purchase|pay(?:ment)?|book\s*now|reserve|subscribe|cancel\s*(?:order|booking|subscription)|refund)\b|결제|구매|장바구니\s*담기|주문\s*확정|예약\s*확정|구독|환불|취소\s*확정)/i.test(value);
  }

  function unsupportedLinkMeaning(value) {
    return /(?:\b(?:account|profile|orders?|settings|wallet|login|sign\s*in|auth|add\s*to\s*wishlist|remove|delete|logout|log\s*out|sign\s*out|unsubscribe|deactivate|follow|like|vote|redeem|claim)\b|계정|프로필|주문\s*내역|설정|지갑|로그인|로그아웃|탈퇴|삭제|구독\s*해지|좋아요|팔로우)/i.test(value);
  }

  function pathBoundary(pathname) {
    let value = pathname;
    try { value = decodeURIComponent(pathname); } catch { /* keep encoded path */ }
    if (/(?:^|\/)(?:checkout|cart|basket|payment|pay|subscribe|booking|reservation)(?:\/|$)/i.test(value)) return 'PAYMENT_OR_COMMITMENT';
    if (/(?:^|\/)(?:account|profile|orders?|settings|wallet|login|signin|auth|logout|addresses|session)(?:\/|$)/i.test(value)) return 'SENSITIVE_INPUT_REQUIRED';
    return null;
  }

  function privateSurfaceSignal() {
    const headings = Array.from(document.querySelectorAll('h1,[role="heading"][aria-level="1"]'))
      .filter(visible).slice(0, 4).map(accessibleName).join(' ');
    const chrome = `${document.title || ''} ${headings}`;
    return /(?:\b(?:my\s+account|account|profile|orders?|order\s+history|shopping\s+cart|your\s+cart|wallet)\b|내\s*계정|마이페이지|프로필|주문\s*내역|장바구니)/i.test(chrome);
  }

  function authenticatedChromeRoots() {
    const roots = [];
    for (const element of document.querySelectorAll('a[href],button,[role="button"]')) {
      if (!visible(element) || !/(?:\b(?:log\s*out|sign\s*out)\b|로그아웃|(?:^|\/)logout(?:\/|$))/i.test(
        `${accessibleName(element)} ${element.getAttribute('href') || ''}`)) continue;
      const container = element.closest('header,[role="banner"],[class*="header" i]') || element.closest(
        'nav,[role="navigation"],[class*="account" i],[class*="profile" i]',
      );
      roots.push(container && container !== document.body && container !== document.documentElement
        ? container
        : element);
    }
    return [...new Set(roots)];
  }

  function likelySensitiveQuery(value) {
    const text = String(value).normalize('NFKC');
    if (/\b(?:password|passwd|passcode|otp|one[- ]?time|cvv|cvc|card\s*number|access[_ -]?token|api[_ -]?key|secret)\b/i.test(text)) return true;
    if (/(?:비밀번호|인증번호|일회용\s*코드|카드\s*번호|보안\s*코드|주민등록)/.test(text)) return true;
    if (/\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/i.test(text)) return true;
    return text.replace(/[^0-9]/g, '').length >= 11 || /https?:\/\//i.test(text);
  }

  const COUPANG_ADAPTER = 'builtin.coupang.purchase-preparation';
  const COUPANG_MUTATION_STEPS = new Set([
    'select_option', 'verify_options', 'set_quantity', 'buy_now', 'add_to_cart', 'start_checkout',
  ]);
  const COUPANG_PRODUCT_ORIGINS = new Set(['https://coupang.com', 'https://www.coupang.com']);
  const COUPANG_CART_PAGES = new Set([
    'https://cart.coupang.com/cartView.pang',
  ]);
  const FINAL_COMMITMENT_NAME = /^(?:결제하기|주문하기|주문\s*확정|구매\s*확정|place\s+order|pay(?:\s+now)?)$/i;

  function exactRecord(value, keys) {
    return record(value) && Object.keys(value).length === keys.length && keys.every((key) => key in value);
  }

  function boundedWon(value) {
    return Number.isSafeInteger(value) && value >= 1 && value <= 1_000_000_000_000;
  }

  function approvedLine(value) {
    if (!exactRecord(value, [
      'productId', 'itemId', 'vendorItemId', 'quantity', 'unitPriceCeilingKrw', 'linePriceCeilingKrw',
    ])) return null;
    if (![value.productId, value.itemId, value.vendorItemId].every((part) =>
      typeof part === 'string' && /^[1-9][0-9]{0,19}$/.test(part))) return null;
    if (!Number.isSafeInteger(value.quantity) || value.quantity < 1 || value.quantity > 99 ||
        !boundedWon(value.unitPriceCeilingKrw) || !boundedWon(value.linePriceCeilingKrw) ||
        value.unitPriceCeilingKrw * value.quantity > value.linePriceCeilingKrw) return null;
    return { ...value };
  }

  function approval(value) {
    if (!exactRecord(value, [
      'approvalId', 'approvalDigest', 'currency', 'revision', 'expiresAt', 'totalPriceCeilingKrw',
    ]) || !id(value.approvalId) || !/^sha256:[a-f0-9]{64}$/.test(value.approvalDigest) ||
        value.currency !== 'KRW' || !Number.isSafeInteger(value.revision) || value.revision < 1 ||
        !boundedWon(value.totalPriceCeilingKrw)) return null;
    const expiry = Date.parse(value.expiresAt);
    if (!Number.isFinite(expiry) || new Date(expiry).toISOString() !== value.expiresAt ||
        expiry <= Date.now() || expiry - Date.now() > 10 * 60_000) return null;
    return { ...value };
  }

  function sameJson(left, right) {
    return JSON.stringify(left) === JSON.stringify(right);
  }

  function approvedPlan(value, actionApproval) {
    if (!exactRecord(value, ['merchantId', 'recipeVersion', 'mode', 'route', 'approval', 'items']) ||
        value.merchantId !== 'COUPANG' || value.recipeVersion !== '1' ||
        !['single', 'multi'].includes(value.mode) ||
        value.route !== (value.mode === 'single' ? 'single_buy_now' : 'multi_cart_checkout') ||
        !sameJson(value.approval, actionApproval) || !Array.isArray(value.items) ||
        value.items.length < 1 || value.items.length > 20 ||
        (value.mode === 'single' ? value.items.length !== 1 : value.items.length < 2)) return null;
    const lines = [];
    for (const item of value.items) {
      if (!exactRecord(item, [
        'query', 'offerIdentity', 'quantity', 'unitPriceCeilingKrw', 'linePriceCeilingKrw', 'productUrl', 'options',
      ]) || !record(item.offerIdentity)) return null;
      const line = approvedLine({
        ...item.offerIdentity,
        quantity: item.quantity,
        unitPriceCeilingKrw: item.unitPriceCeilingKrw,
        linePriceCeilingKrw: item.linePriceCeilingKrw,
      });
      if (!line || typeof item.productUrl !== 'string' || typeof item.query !== 'string' || !record(item.options)) return null;
      lines.push(line);
    }
    if (new Set(lines.map(lineKey)).size !== lines.length) return null;
    return { mode: value.mode, route: value.route, lines };
  }

  function normalizedAccessibleName(element) {
    return accessibleName(element).normalize('NFKC').replace(/\s+/g, ' ').trim();
  }

  function semanticRole(element) {
    const role = (element.getAttribute('role') || '').toLowerCase();
    if (role) return role;
    if (element.tagName === 'A') return 'link';
    if (element.matches('button,input[type="button"],input[type="submit"]')) return 'button';
    return '';
  }

  function enabled(element) {
    return !element.disabled && element.getAttribute('aria-disabled') !== 'true';
  }

  function wonFromText(value) {
    const text = clean(value, 120).normalize('NFKC');
    const match = text.match(/(?:^|[^0-9])([1-9][0-9]{0,2}(?:,[0-9]{3})*|[1-9][0-9]{0,12})(?:\s*원|\s*$)/);
    if (!match) return null;
    const amount = Number(match[1].replace(/,/g, ''));
    return boundedWon(amount) ? amount : null;
  }

  function exactUnitPrice(scope) {
    const values = new Set();
    const selectors = [
      '[data-vitlane-unit-price-krw]',
      'meta[property="product:price:amount"]',
      '[itemprop="price"]',
      '.prod-sale-price .total-price',
      '.total-price > strong',
    ];
    for (const element of scope.querySelectorAll(selectors.join(','))) {
      if (element.tagName !== 'META' && !visible(element)) continue;
      const raw = element.getAttribute('data-vitlane-unit-price-krw') || element.getAttribute('content') ||
        element.getAttribute('value') || element.textContent;
      const amount = /^\s*[1-9][0-9]{0,12}\s*$/.test(String(raw)) ? Number(String(raw).trim()) : wonFromText(raw);
      if (boundedWon(amount)) values.add(amount);
    }
    return values.size === 1 ? [...values][0] : null;
  }

  function quantityIn(scope, expected) {
    const fields = Array.from(scope.querySelectorAll(
      'input[type="number"],input[name*="quantity" i],select[name*="quantity" i],[data-vitlane-quantity]',
    )).filter((element) => visible(element));
    const values = new Set(fields.map((element) => Number(
      element.getAttribute('data-vitlane-quantity') || element.value || element.getAttribute('value'),
    )).filter((value) => Number.isSafeInteger(value) && value >= 1 && value <= 99));
    if (values.size === 0) return expected === 1;
    return values.size === 1 && values.has(expected);
  }

  function unresolvedOptions(scope) {
    return Array.from(scope.querySelectorAll('select')).some((element) => visible(element) && enabled(element) &&
      (!element.value || /^(?:선택|옵션\s*선택|choose|select)$/i.test(clean(element.selectedOptions?.[0]?.textContent, 80))));
  }

  function approvedOptionsMatch(scope, options) {
    if (!record(options)) return false;
    if (options.kind === 'none') {
      return !Array.from(scope.querySelectorAll(
        'select,input[type="radio"],[role="radio"],[role="option"],[data-option-group] button,[data-option-group] [role="button"]',
      )).some((element) => visible(element) && enabled(element));
    }
    if (options.kind !== 'choices' || !Array.isArray(options.choices) || unresolvedOptions(scope)) return false;
    const optionControls = Array.from(scope.querySelectorAll(
      'select,input[type="radio"],[role="radio"],[role="option"],[data-option-group] button,[data-option-group] [role="button"]',
    )).filter((element) => visible(element) && enabled(element));
    const groupNameOf = (element) => {
      if (element instanceof HTMLSelectElement) return normalizedAccessibleName(element);
      const group = element.closest('fieldset,[role="group"],[role="radiogroup"],[data-option-group]');
      const label = group?.querySelector('legend,[data-option-group-label],[role="heading"]');
      return clean(label?.textContent || group?.getAttribute('aria-label') || '', 120)
        .normalize('NFKC').replace(/\s+/g, ' ').trim();
    };
    const observedGroups = new Set(optionControls.map(groupNameOf));
    const approvedGroups = new Set(options.choices.map(({ groupName }) =>
      String(groupName).normalize('NFKC').replace(/\s+/g, ' ').trim()));
    if (observedGroups.has('') || observedGroups.size !== approvedGroups.size ||
        [...observedGroups].some((group) => !approvedGroups.has(group))) return false;
    return options.choices.every(({ groupName, valueName }) => {
      const expectedGroup = String(groupName).normalize('NFKC').replace(/\s+/g, ' ').trim();
      const expectedValue = String(valueName).normalize('NFKC').replace(/\s+/g, ' ').trim();
      const selected = [];
      for (const element of scope.querySelectorAll(
        'select,input[type="radio"],button,[role="radio"],[role="option"],[role="button"]',
      )) {
        if (!visible(element) || !enabled(element)) continue;
        if (element instanceof HTMLSelectElement) {
          const chosen = clean(element.selectedOptions?.[0]?.textContent, 200).normalize('NFKC').replace(/\s+/g, ' ').trim();
          if (normalizedAccessibleName(element) === expectedGroup && chosen === expectedValue) selected.push(element);
          continue;
        }
        const group = element.closest('fieldset,[role="group"],[role="radiogroup"],[data-option-group]');
        const label = group?.querySelector('legend,[data-option-group-label],[role="heading"]');
        const actualGroup = clean(label?.textContent || group?.getAttribute('aria-label') || '', 120)
          .normalize('NFKC').replace(/\s+/g, ' ').trim();
        const isSelected = element.checked === true || element.getAttribute('aria-checked') === 'true' ||
          element.getAttribute('aria-selected') === 'true' || element.classList.contains('selected');
        if (actualGroup === expectedGroup && normalizedAccessibleName(element) === expectedValue && isSelected) {
          selected.push(element);
        }
      }
      return selected.length === 1;
    });
  }

  function exactInteractive(scope, names, patterns = []) {
    const candidates = Array.from(scope.querySelectorAll(
      'button,a[href],[role="button"],[role="link"],input[type="button"],input[type="submit"]',
    )).filter((element) => visible(element) && enabled(element) && ['button', 'link'].includes(semanticRole(element)));
    return candidates.filter((element) => {
      const name = normalizedAccessibleName(element);
      return !FINAL_COMMITMENT_NAME.test(name) && (names.includes(name) || patterns.some((pattern) => pattern.test(name)));
    });
  }

  function productLocationMatches(line, bindings) {
    if (!COUPANG_PRODUCT_ORIGINS.has(location.origin) || location.origin !== bindings.expectedOrigin ||
        location.pathname !== bindings.expectedPath) return false;
    if (!new RegExp(`^/vp/products/${line.productId}/?$`).test(location.pathname)) return false;
    const params = new URL(location.href).searchParams;
    const itemIds = params.getAll('itemId');
    const vendorItemIds = params.getAll('vendorItemId');
    return itemIds.length === 1 && vendorItemIds.length === 1 &&
      itemIds[0] === line.itemId && vendorItemIds[0] === line.vendorItemId;
  }

  function productPreparation(bindings, stepId) {
    const baseKeys = [
      'approvedPreparation', 'approval', 'targetLineIndex', 'productId', 'itemId', 'vendorItemId',
      'quantity', 'unitPriceCeilingKrw', 'linePriceCeilingKrw', 'expectedOrigin', 'expectedPath',
    ];
    const required = stepId === 'select_option'
      ? [...baseKeys, 'targetOptionIndex', 'groupName', 'valueName']
      : baseKeys;
    if (!exactRecord(bindings, required)) return { code: 'POLICY_DENIED' };
    const authorized = approval(bindings.approval);
    const line = approvedLine({
      productId: bindings.productId,
      itemId: bindings.itemId,
      vendorItemId: bindings.vendorItemId,
      quantity: bindings.quantity,
      unitPriceCeilingKrw: bindings.unitPriceCeilingKrw,
      linePriceCeilingKrw: bindings.linePriceCeilingKrw,
    });
    const plan = authorized && approvedPlan(bindings.approvedPreparation, bindings.approval);
    if (!authorized || !line || !plan || !Number.isSafeInteger(bindings.targetLineIndex) ||
        bindings.targetLineIndex < 0 || bindings.targetLineIndex >= plan.lines.length ||
        !sameJson(plan.lines[bindings.targetLineIndex], line) ||
        (stepId === 'buy_now' && plan.mode !== 'single') ||
        (stepId === 'add_to_cart' && plan.mode !== 'multi') ||
        !productLocationMatches(line, bindings)) return { code: 'APPROVAL_PAGE_MISMATCH' };
    const scope = document.querySelector('main,[itemtype*="Product"],#contents');
    if (!scope || !visible(scope)) return { code: 'PRODUCT_STATE_MISMATCH' };
    const approvedItem = bindings.approvedPreparation.items[bindings.targetLineIndex];

    if (stepId === 'select_option') {
      if (!Number.isSafeInteger(bindings.targetOptionIndex) || bindings.targetOptionIndex < 0 ||
          typeof bindings.groupName !== 'string' || typeof bindings.valueName !== 'string') {
        return { code: 'POLICY_DENIED' };
      }
      const expectedGroup = bindings.groupName.normalize('NFKC').replace(/\s+/g, ' ').trim();
      const expectedValue = bindings.valueName.normalize('NFKC').replace(/\s+/g, ' ').trim();
      const matches = [];
      for (const element of scope.querySelectorAll(
        'select,input[type="radio"],button,[role="radio"],[role="option"],[role="button"]',
      )) {
        if (!visible(element) || !enabled(element)) continue;
        if (element instanceof HTMLSelectElement) {
          const group = normalizedAccessibleName(element);
          const options = Array.from(element.options).filter((option) =>
            clean(option.textContent, 200).normalize('NFKC').replace(/\s+/g, ' ').trim() === expectedValue && !option.disabled);
          if (group === expectedGroup && options.length === 1) matches.push({ target: element, option: options[0] });
          continue;
        }
        const group = element.closest('fieldset,[role="group"],[role="radiogroup"],[data-option-group]');
        const label = group?.querySelector('legend,[data-option-group-label],[role="heading"]');
        const groupName = clean(label?.textContent || group?.getAttribute('aria-label') || '', 120)
          .normalize('NFKC').replace(/\s+/g, ' ').trim();
        if (normalizedAccessibleName(element) === expectedValue && groupName === expectedGroup) {
          matches.push({ target: element, option: null });
        }
      }
      if (matches.length !== 1) return { code: matches.length ? 'CONTROL_AMBIGUOUS' : 'CONTROL_NOT_FOUND' };
      return { ...matches[0], authorized, lines: [line], code: 'READY' };
    }

    if (stepId === 'set_quantity') {
      const fields = Array.from(scope.querySelectorAll(
        'input[type="number"],input[inputmode="numeric"],input[name*="quantity" i],input[id*="quantity" i]',
      )).filter((element) => visible(element) && enabled(element));
      if (fields.length !== 1) return { code: fields.length ? 'CONTROL_AMBIGUOUS' : 'CONTROL_NOT_FOUND' };
      return { target: fields[0], desiredQuantity: line.quantity, authorized, lines: [line], code: 'READY' };
    }

    if (stepId === 'verify_options') {
      if (!approvedOptionsMatch(scope, approvedItem.options)) return { code: 'PRODUCT_STATE_MISMATCH' };
      const price = exactUnitPrice(scope);
      if (!price || price > line.unitPriceCeilingKrw || price * line.quantity > line.linePriceCeilingKrw ||
          price * line.quantity > authorized.totalPriceCeilingKrw) return { code: 'PRICE_CEILING_EXCEEDED' };
      return { verifyOnly: true, authorized, lines: [line], price, code: 'READY' };
    }

    if (!approvedOptionsMatch(scope, approvedItem.options) || !quantityIn(scope, line.quantity)) {
      return { code: 'PRODUCT_STATE_MISMATCH' };
    }
    const price = exactUnitPrice(scope);
    if (!price || price > line.unitPriceCeilingKrw || price * line.quantity > line.linePriceCeilingKrw ||
        price * line.quantity > authorized.totalPriceCeilingKrw) return { code: 'PRICE_CEILING_EXCEEDED' };
    const names = stepId === 'buy_now' ? ['바로구매', '바로 구매'] : ['장바구니 담기'];
    const matches = exactInteractive(scope, names);
    if (matches.length !== 1) return { code: matches.length ? 'CONTROL_AMBIGUOUS' : 'CONTROL_NOT_FOUND' };
    return { target: matches[0], authorized, lines: [line], price, code: 'READY' };
  }

  function identityFromProductLink(element) {
    let url;
    try { url = new URL(element.href, location.href); } catch { return null; }
    const match = url.pathname.match(/^\/vp\/products\/([1-9][0-9]*)\/?$/);
    const itemIds = url.searchParams.getAll('itemId');
    const vendorItemIds = url.searchParams.getAll('vendorItemId');
    if (!match || itemIds.length !== 1 || vendorItemIds.length !== 1 ||
        !/^[1-9][0-9]{0,19}$/.test(itemIds[0]) || !/^[1-9][0-9]{0,19}$/.test(vendorItemIds[0])) return null;
    return { productId: match[1], itemId: itemIds[0], vendorItemId: vendorItemIds[0] };
  }

  function cartLineRoot(link) {
    return link.closest('[data-vitlane-cart-line],li,article,tr,[role="row"],[class*="cart-unit-item" i],[class*="cart-item" i]');
  }

  function lineKey(line) {
    return `${line.productId}:${line.itemId}:${line.vendorItemId}`;
  }

  function selectedCartLines(scope) {
    const found = new Map();
    const seenRoots = new Set();
    let malformedSelectedLine = false;
    for (const link of scope.querySelectorAll('a[href*="/vp/products/"]')) {
      const root = cartLineRoot(link);
      if (!root || seenRoots.has(root)) continue;
      seenRoots.add(root);
      const checkboxes = Array.from(root.querySelectorAll('input[type="checkbox"],[role="checkbox"]')).filter(visible);
      const selected = checkboxes.some((box) => box.checked === true || box.getAttribute('aria-checked') === 'true');
      if (!selected) continue;
      const identity = identityFromProductLink(link);
      if (!identity) { malformedSelectedLine = true; continue; }
      const quantityFields = Array.from(root.querySelectorAll(
        'input[type="number"],input[name*="quantity" i],select[name*="quantity" i],[data-vitlane-quantity]',
      )).filter(visible);
      const quantities = new Set(quantityFields.map((field) => Number(
        field.getAttribute('data-vitlane-quantity') || field.value || field.getAttribute('value'),
      )).filter((value) => Number.isSafeInteger(value) && value >= 1 && value <= 99));
      const quantity = quantities.size === 1 ? [...quantities][0] : null;
      const unitPrice = exactUnitPrice(root);
      if (!quantity || !unitPrice || found.has(lineKey(identity))) malformedSelectedLine = true;
      else found.set(lineKey(identity), { root, identity, quantity, unitPrice });
    }
    return { found, malformedSelectedLine };
  }

  function cartPreparation(bindings) {
    if (!exactRecord(bindings, ['approvedPreparation', 'approval', 'approvedLines', 'expectedOrigin', 'expectedPath']) ||
        !Array.isArray(bindings.approvedLines) || bindings.approvedLines.length < 2 || bindings.approvedLines.length > 20) {
      return { code: 'POLICY_DENIED' };
    }
    const authorized = approval(bindings.approval);
    const lines = bindings.approvedLines.map(approvedLine);
    const plan = authorized && approvedPlan(bindings.approvedPreparation, bindings.approval);
    if (!authorized || !plan || plan.mode !== 'multi' || lines.some((line) => !line) ||
        !sameJson(plan.lines, lines) || new Set(lines.map(lineKey)).size !== lines.length ||
        location.origin !== bindings.expectedOrigin || location.pathname !== bindings.expectedPath ||
        !COUPANG_CART_PAGES.has(`${location.origin}${location.pathname}`)) return { code: 'APPROVAL_PAGE_MISMATCH' };
    const scope = Array.from(document.querySelectorAll('main,[data-vitlane-cart],#cartContent,[class*="cart" i]'))
      .find((candidate) => visible(candidate) && candidate.querySelector('a[href*="/vp/products/"]'));
    if (!scope || !visible(scope)) return { code: 'CART_STATE_MISMATCH' };
    const observed = selectedCartLines(scope);
    if (observed.malformedSelectedLine || observed.found.size !== lines.length) return { code: 'CART_SELECTION_MISMATCH' };
    let total = 0;
    const roots = [];
    for (const line of lines) {
      const current = observed.found.get(lineKey(line));
      if (!current || current.quantity !== line.quantity || current.unitPrice > line.unitPriceCeilingKrw ||
          current.unitPrice * current.quantity > line.linePriceCeilingKrw) return { code: 'CART_SELECTION_MISMATCH' };
      total += current.unitPrice * current.quantity;
      roots.push(current.root);
    }
    if (total > authorized.totalPriceCeilingKrw) return { code: 'PRICE_CEILING_EXCEEDED' };
    const matches = exactInteractive(scope, ['구매하기', '선택상품 구매하기', '선택 상품 구매하기'], [
      /^[1-9][0-9]*개 상품 구매하기$/,
      /^구매하기\s*\([1-9][0-9]*(?:개)?\)$/,
    ]).filter((target) => roots.every((root) => scope.contains(root)) && scope.contains(target));
    if (matches.length !== 1) return { code: matches.length ? 'CONTROL_AMBIGUOUS' : 'CONTROL_NOT_FOUND' };
    return { target: matches[0], authorized, lines, price: total, code: 'READY' };
  }

  function coupangPreparation(action) {
    if (action.adapterId !== COUPANG_ADAPTER || action.recipeVersion !== '1' ||
        !COUPANG_MUTATION_STEPS.has(action.stepId) || !record(action.bindings)) return { code: 'POLICY_DENIED' };
    return action.stepId === 'start_checkout'
      ? cartPreparation(action.bindings)
      : productPreparation(action.bindings, action.stepId);
  }

  function nativeMetadata(raw) {
    if (!record(raw) || !id(raw.tabId) || !id(raw.frameId) || !Number.isSafeInteger(raw.documentEpoch) ||
        raw.documentEpoch < 0 || raw.foreground !== true || typeof raw.topOrigin !== 'string' ||
        typeof raw.frameOrigin !== 'string' || typeof raw.pathname !== 'string' ||
        typeof raw.queryOrFragmentPresent !== 'boolean' || self !== top) throw new Error('invalid_native_metadata');
    let topOrigin;
    let frameOrigin;
    try {
      topOrigin = publicOrigin(raw.topOrigin);
      frameOrigin = publicOrigin(raw.frameOrigin);
    } catch { throw new Error('invalid_native_metadata'); }
    if (topOrigin !== location.origin || frameOrigin !== location.origin || topOrigin !== frameOrigin ||
        raw.pathname !== location.pathname ||
        raw.queryOrFragmentPresent !== Boolean(location.search || location.hash)) {
      throw new Error('native_origin_mismatch');
    }
    return {
      tabId: raw.tabId,
      frameId: raw.frameId,
      documentEpoch: raw.documentEpoch,
      topOrigin,
      frameOrigin,
      pathname: raw.pathname,
      queryOrFragmentPresent: raw.queryOrFragmentPresent,
      foreground: true,
    };
  }

  function safeSearch(element) {
    if (element.tagName !== 'INPUT' || !visible(element) || element.disabled || element.readOnly || element.value !== '') return false;
    if (!(element.type === 'search' || element.getAttribute('role') === 'searchbox')) return false;
    if (sensitiveInput(element)) return false;
    const form = element.form;
    if (!form || String(form.method || 'get').toLowerCase() !== 'get') return false;
    try {
      if (new URL(publicUrl(form.action || location.href)).origin !== location.origin) return false;
    } catch { return false; }
    const editable = Array.from(form.querySelectorAll('input,textarea,select')).filter((item) =>
      !['hidden', 'submit', 'button', 'reset', 'image'].includes(item.type));
    return editable.length === 1 && editable[0] === element;
  }

  function candidateInfo(element, index) {
    const candidateRef = `candidate_${index}`;
    const nodeRef = `node_${index}`;
    if (element.tagName === 'A') {
      if (element.hasAttribute('download') || element.hasAttribute('onclick')) return null;
      const name = accessibleName(element);
      const href = publicUrl(element.href);
      if (new URL(href).origin !== location.origin) return { omitReason: 'UNSUPPORTED_INTERACTION' };
      const boundary = pathBoundary(new URL(href).pathname);
      if (boundary) return { omitReason: boundary === 'PAYMENT_OR_COMMITMENT' ? boundary : 'UNSUPPORTED_INTERACTION' };
      if (paymentMeaning(`${name} ${new URL(href).pathname}`)) return { omitReason: 'PAYMENT_OR_COMMITMENT' };
      if (unsupportedLinkMeaning(`${name} ${new URL(href).pathname}`)) return { omitReason: 'UNSUPPORTED_INTERACTION' };
      return { candidateRef, nodeRef, kind: 'safe_link', role: 'link', name, href };
    }
    if (safeSearch(element)) {
      return { candidateRef, nodeRef, kind: 'public_search', role: 'searchbox', name: accessibleName(element) || 'Search' };
    }
    return null;
  }

  function currentCandidate(element, previous) {
    try {
      const described = candidateInfo(element, Number(previous.candidateRef.slice('candidate_'.length)));
      return described && !described.omitReason ? described : null;
    } catch { return null; }
  }

  function snapshot(rawMetadata) {
    const metadata = nativeMetadata(rawMetadata);
    const handoffReasons = new Set();
    const excludedReasons = new Set();
    const excludedRoots = [];
    let blockCollection = false;
    const currentPathBoundary = pathBoundary(location.pathname);
    if (currentPathBoundary) {
      handoffReasons.add(currentPathBoundary);
      blockCollection = true;
    }
    if (privateSurfaceSignal()) {
      handoffReasons.add('SENSITIVE_INPUT_REQUIRED');
      blockCollection = true;
    }

    for (const root of authenticatedChromeRoots()) {
      excludedReasons.add('UNSUPPORTED_INTERACTION');
      excludedRoots.push(root);
    }

    for (const element of document.querySelectorAll('input,textarea,select,button,[role="button"],form,iframe,[contenteditable],[role="textbox"]')) {
      if (!visible(element)) continue;
      if (excludedRoots.some((root) => root === element || root.contains(element))) continue;
      if (element.tagName === 'IFRAME') {
        try {
          if (new URL(element.src || location.href, location.href).origin !== location.origin) {
            excludedReasons.add('CROSS_ORIGIN_FRAME');
            excludedRoots.push(element);
          }
        } catch {
          excludedReasons.add('CROSS_ORIGIN_FRAME');
          excludedRoots.push(element);
        }
        continue;
      }
      if (element.matches('[contenteditable]:not([contenteditable="false"]),[role="textbox"]')) {
        handoffReasons.add('SENSITIVE_INPUT_REQUIRED');
        blockCollection = true;
      }
      if (['INPUT', 'TEXTAREA', 'SELECT'].includes(element.tagName) && sensitiveInput(element)) {
        handoffReasons.add(/password|passcode|one-time|otp|비밀번호|인증번호/i.test([
          element.type, element.autocomplete, element.name, element.id, accessibleName(element),
        ].join(' ')) ? 'AUTHENTICATION_REQUIRED' : 'SENSITIVE_INPUT_REQUIRED');
        blockCollection = true;
      } else if (hasUserInputValue(element)) {
        handoffReasons.add('SENSITIVE_INPUT_REQUIRED');
        blockCollection = true;
      } else if (['INPUT', 'TEXTAREA', 'SELECT'].includes(element.tagName) &&
          !(element.tagName === 'INPUT' && ['hidden', 'button', 'submit', 'reset', 'image'].includes(element.type)) &&
          !safeSearch(element)) {
        handoffReasons.add('FORM_SUBMISSION_REQUIRED');
        blockCollection = true;
      }
      const meaning = `${accessibleName(element)} ${element.getAttribute('action') || ''}`;
      if (paymentMeaning(meaning)) {
        excludedReasons.add('PAYMENT_OR_COMMITMENT');
        excludedRoots.push(element);
      } else if (element.matches('button,[role="button"]')) {
        excludedReasons.add('UNSUPPORTED_INTERACTION');
        excludedRoots.push(element);
      } else if (element.tagName === 'FORM' && !element.querySelector('input[type="search"],[role="searchbox"]')) {
        excludedReasons.add('FORM_SUBMISSION_REQUIRED');
        excludedRoots.push(element);
      }
    }

    const refs = new Map();
    const candidates = [];
    if (!blockCollection) {
      const elements = document.querySelectorAll('a[href],input[type="search"],[role="searchbox"]');
      for (let i = 0; i < Math.min(elements.length, 1200) && candidates.length < 120; i++) {
        const element = elements[i];
        if (!visible(element)) continue;
        if (excludedRoots.some((root) => root === element || root.contains(element))) continue;
        let info;
        try { info = candidateInfo(element, candidates.length + 1); }
        catch {
          if (element.tagName === 'A') {
            excludedReasons.add('PRIVATE_NETWORK_BLOCKED');
            excludedRoots.push(element);
          }
          continue;
        }
        if (info?.omitReason) {
          excludedReasons.add(info.omitReason);
          excludedRoots.push(element);
          continue;
        }
        if (!info) continue;
        refs.set(info.candidateRef, { element, info, fingerprint: JSON.stringify(info) });
        candidates.push(info);
      }
    }

    let visibleText = '';
    if (!blockCollection) {
      const walker = document.createTreeWalker(document.body || document.documentElement, NodeFilter.SHOW_TEXT);
      let node;
      let visited = 0;
      while ((node = walker.nextNode()) && visibleText.length < 10000 && visited++ < 5000) {
        const parent = node.parentElement;
        if (!parent || parent.closest('script,style,noscript,input,textarea,select,[contenteditable]') ||
            excludedRoots.some((root) => root === parent || root.contains(parent)) || !visible(parent)) continue;
        const part = clean(node.textContent, 600);
        if (part) visibleText += `${part}\n`;
      }
    }

    if (blockCollection) {
      refs.clear();
      candidates.length = 0;
      visibleText = '';
    }
    const observationId = randomId('obs');
    const pageTypeHint = blockCollection ? 'unknown' :
      candidates.some((candidate) => candidate.kind === 'public_search') ? 'search' :
        document.querySelector('[itemtype*="Product"],meta[property="product:price:amount"]') ? 'product' :
          candidates.length > 8 ? 'listing' : 'public';
    const observation = {
      observationId,
      nativeMetadata: metadata,
      untrustedPageData: {
        pageTypeHint,
        title: blockCollection ? '' : clean(document.title, 300),
        visibleText: visibleText.slice(0, 10000),
        candidates,
      },
      privacy: {
        inputValuesOmitted: true,
        secretsOmitted: true,
        screenshotIncluded: false,
        urlQueryAndFragmentOmitted: true,
        collectionStatus: blockCollection ? 'handoff_required' : 'sanitized',
        handoffReasonCodes: blockCollection ? Array.from(handoffReasons).sort() : [],
        excludedBoundaryCodes: blockCollection ? [] : Array.from(excludedReasons).sort(),
      },
    };
    state = {
      observationId,
      metadata,
      refs,
      locationHref: location.href,
      used: false,
    };
    return observation;
  }

  function rejected(commandId, code) {
    return { allowed: false, commandId: id(commandId) ? commandId : 'invalid_command', code };
  }

  function inspect(command) {
    if (!record(command) || command.protocolVersion !== 1 || !id(command.commandId) || !id(command.runId) ||
        !id(command.deviceId) || !id(command.profileRef) || !Number.isSafeInteger(command.sequence) || command.sequence < 1 ||
        !Number.isSafeInteger(command.leaseEpoch) || command.leaseEpoch < 1 ||
        !Number.isSafeInteger(command.controlGeneration) || command.controlGeneration < 0 || !record(command.action) ||
        typeof command.actionHash !== 'string' || !/^[a-f0-9]{64}$/.test(command.actionHash) ||
        typeof command.serverPermit !== 'string' || !/^hmac-sha256:[A-Za-z0-9_-]{43}$/.test(command.serverPermit)) {
      return rejected(command?.commandId, 'INVALID_COMMAND');
    }
    if (!state || state.used) return rejected(command.commandId, 'REPLAY_CONFLICT');
    const expiration = Date.parse(command.expiresAt);
    const now = Date.now();
    if (typeof command.expiresAt !== 'string' || !Number.isFinite(expiration) ||
        new Date(expiration).toISOString() !== command.expiresAt) return rejected(command.commandId, 'INVALID_COMMAND');
    if (expiration <= now) return rejected(command.commandId, 'COMMAND_EXPIRED');
    if (expiration - now > 60_000) return rejected(command.commandId, 'INVALID_COMMAND');
    const action = command.action;
    const page = action.page;
    if (!record(page) || page.observationId !== state.observationId || page.tabId !== state.metadata.tabId ||
        page.frameId !== state.metadata.frameId || page.documentEpoch !== state.metadata.documentEpoch ||
        page.topOrigin !== state.metadata.topOrigin || page.frameOrigin !== state.metadata.frameOrigin ||
        page.pathname !== state.metadata.pathname ||
        page.queryOrFragmentPresent !== state.metadata.queryOrFragmentPresent ||
        state.locationHref !== location.href || location.origin !== state.metadata.topOrigin) {
      return rejected(command.commandId, 'REOBSERVE_REQUIRED');
    }
    switch (action.kind) {
      case 'inspect_page':
        return { allowed: true, commandId: command.commandId, effect: 'read', label: 'Inspect page' };
      case 'request_human':
        return typeof action.reasonCode === 'string' && typeof action.message === 'string' ?
          { allowed: true, commandId: command.commandId, effect: 'handoff', label: clean(action.message, 200) } :
          rejected(command.commandId, 'POLICY_DENIED');
      case 'finish':
        return typeof action.message === 'string' ?
          { allowed: true, commandId: command.commandId, effect: 'finish', label: clean(action.message, 200) } :
          rejected(command.commandId, 'POLICY_DENIED');
      case 'scroll':
        return ['up', 'down'].includes(action.direction) ?
          { allowed: true, commandId: command.commandId, effect: 'viewport', label: action.direction } :
          rejected(command.commandId, 'POLICY_DENIED');
      case 'open_candidate': {
        const target = state.refs.get(action.candidateRef);
        if (!target || target.info.kind !== 'safe_link' || !target.element.isConnected || !visible(target.element)) {
          return rejected(command.commandId, 'REOBSERVE_REQUIRED');
        }
        const current = currentCandidate(target.element, target.info);
        if (!current || JSON.stringify(current) !== target.fingerprint) return rejected(command.commandId, 'REOBSERVE_REQUIRED');
        try { publicUrl(current.href); } catch { return rejected(command.commandId, 'POLICY_DENIED'); }
        return {
          allowed: true,
          commandId: command.commandId,
          effect: 'handoff_navigation',
          label: current.name || 'Link',
          targetOrigin: new URL(current.href).origin,
          targetUrl: current.href,
        };
      }
      case 'run_preparation_step': {
        if (action.adapterId === 'builtin.public-search') {
          if (action.recipeVersion !== '1' || action.stepId !== 'prepare_query' || !record(action.bindings)) {
            return rejected(command.commandId, 'POLICY_DENIED');
          }
          const { candidateRef, query } = action.bindings;
          const target = state.refs.get(candidateRef);
          if (!target || target.info.kind !== 'public_search' || !target.element.isConnected || !safeSearch(target.element) ||
              typeof query !== 'string' || !query.trim() || query.length > 160 || query !== query.trim() || likelySensitiveQuery(query)) {
            return rejected(command.commandId, 'POLICY_DENIED');
          }
          const current = currentCandidate(target.element, target.info);
          if (!current || JSON.stringify(current) !== target.fingerprint) return rejected(command.commandId, 'REOBSERVE_REQUIRED');
          return { allowed: true, commandId: command.commandId, effect: 'public_search_prepare', label: current.name || 'Search' };
        }
        const prepared = coupangPreparation(action);
        if (!prepared.target && !prepared.verifyOnly) return rejected(command.commandId, prepared.code);
        return {
          allowed: true,
          commandId: command.commandId,
          effect: `coupang_${action.stepId}`,
          label: action.stepId,
        };
      }
      default:
        return rejected(command.commandId, 'POLICY_DENIED');
    }
  }

  function execute(command) {
    const check = inspect(command);
    if (!check.allowed) return { commandId: check.commandId, status: 'rejected', code: check.code };
    const action = command.action;
    const target = action.kind === 'open_candidate' ? state.refs.get(action.candidateRef) :
      action.kind === 'run_preparation_step' && action.adapterId === 'builtin.public-search'
        ? state.refs.get(action.bindings.candidateRef)
        : null;
    state.used = true;
    try {
      switch (action.kind) {
        case 'request_human':
          return { commandId: command.commandId, status: 'handoff', code: action.reasonCode };
        case 'finish':
          return { commandId: command.commandId, status: 'applied', code: 'AI_RESPONSE_RECORDED' };
        case 'inspect_page':
          return { commandId: command.commandId, status: 'applied', code: 'OBSERVATION_REQUIRED' };
        case 'scroll':
          scrollBy({ top: innerHeight * 0.8 * (action.direction === 'up' ? -1 : 1), behavior: 'instant' });
          return { commandId: command.commandId, status: 'applied', code: 'VIEWPORT_SCROLLED' };
        case 'open_candidate':
          // Automatic navigation remains disabled until the native fork owns a
          // redirect- and DNS-aware NavigationThrottle/network policy. Even a
          // same-origin href can redirect to a private or commitment surface.
          publicUrl(target.info.href);
          return { commandId: command.commandId, status: 'handoff', code: 'NAVIGATION_REQUIRES_USER' };
        case 'run_preparation_step': {
          if (action.adapterId === 'builtin.public-search') {
            const input = target.element;
            // Deliberately avoid focus/input/change/submit events. A page can
            // attach arbitrary side effects to those events; v1 only stages the
            // public query value for the user to review and submit directly.
            Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value').set.call(input, action.bindings.query);
            if (input.value !== action.bindings.query) {
              return { commandId: command.commandId, status: 'outcome_unknown', code: 'PREPARATION_OUTCOME_UNKNOWN' };
            }
            return { commandId: command.commandId, status: 'applied', code: 'PUBLIC_SEARCH_PREPARED' };
          }
          const prepared = coupangPreparation(action);
          if (prepared.verifyOnly) {
            return { commandId: command.commandId, status: 'applied', code: 'COUPANG_OPTIONS_VERIFIED' };
          }
          if (!prepared.target || !prepared.target.isConnected || !visible(prepared.target) || !enabled(prepared.target)) {
            return { commandId: command.commandId, status: 'rejected', code: prepared.code || 'REOBSERVE_REQUIRED' };
          }
          if (action.stepId === 'set_quantity') {
            const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
            if (!setter) return { commandId: command.commandId, status: 'rejected', code: 'INPUT_UNSUPPORTED' };
            setter.call(prepared.target, String(prepared.desiredQuantity));
            prepared.target.dispatchEvent(new Event('input', { bubbles: true }));
            prepared.target.dispatchEvent(new Event('change', { bubbles: true }));
            return prepared.target.value === String(prepared.desiredQuantity)
              ? { commandId: command.commandId, status: 'applied', code: 'COUPANG_QUANTITY_SET' }
              : { commandId: command.commandId, status: 'outcome_unknown', code: 'COUPANG_QUANTITY_OUTCOME_UNKNOWN' };
          }
          if (action.stepId === 'select_option') {
            if (prepared.target instanceof HTMLSelectElement) {
              const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set;
              if (!setter || !prepared.option) {
                return { commandId: command.commandId, status: 'rejected', code: 'INPUT_UNSUPPORTED' };
              }
              setter.call(prepared.target, prepared.option.value);
              prepared.target.dispatchEvent(new Event('input', { bubbles: true }));
              prepared.target.dispatchEvent(new Event('change', { bubbles: true }));
              const selected = clean(prepared.target.selectedOptions?.[0]?.textContent, 200)
                .normalize('NFKC').replace(/\s+/g, ' ').trim();
              return selected === action.bindings.valueName.normalize('NFKC').replace(/\s+/g, ' ').trim()
                ? { commandId: command.commandId, status: 'applied', code: 'COUPANG_OPTION_SELECTED' }
                : { commandId: command.commandId, status: 'outcome_unknown', code: 'COUPANG_OPTION_OUTCOME_UNKNOWN' };
            }
            prepared.target.click();
            const selected = prepared.target.checked === true || prepared.target.getAttribute('aria-checked') === 'true' ||
              prepared.target.getAttribute('aria-selected') === 'true' || prepared.target.classList.contains('selected');
            return selected
              ? { commandId: command.commandId, status: 'applied', code: 'COUPANG_OPTION_SELECTED' }
              : { commandId: command.commandId, status: 'outcome_unknown', code: 'COUPANG_OPTION_OUTCOME_UNKNOWN' };
          }
          let activationCount = 0;
          const markActivation = (event) => {
            if (event.target === prepared.target || prepared.target.contains(event.target)) activationCount++;
          };
          document.addEventListener('click', markActivation, true);
          prepared.target.click();
          document.removeEventListener('click', markActivation, true);
          if (activationCount !== 1) {
            return { commandId: command.commandId, status: 'outcome_unknown', code: 'MERCHANT_ACTIVATION_UNKNOWN' };
          }
          const code = action.stepId === 'add_to_cart' ? 'COUPANG_ADD_TO_CART_ACTIVATED' :
            action.stepId === 'buy_now' ? 'COUPANG_BUY_NOW_ACTIVATED' : 'COUPANG_CHECKOUT_REVIEW_ACTIVATED';
          // Activation is the last automated boundary. Navigation, login, and
          // final order/payment confirmation continue under direct user control.
          return {
            commandId: command.commandId,
            status: action.stepId === 'add_to_cart' ? 'applied' : 'handoff',
            code,
          };
        }
        default:
          return { commandId: command.commandId, status: 'rejected', code: 'POLICY_DENIED' };
      }
    } catch {
      return { commandId: command.commandId, status: 'outcome_unknown', code: 'EXECUTION_OUTCOME_UNKNOWN' };
    }
  }

  globalThis.__laneAgent = Object.freeze({ snapshot, inspect, execute });
})();
