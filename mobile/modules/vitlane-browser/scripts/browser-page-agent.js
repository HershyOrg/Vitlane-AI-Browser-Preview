// This code runs in Android WebView's page world, not an isolated Chromium world.
// Treat all returned content as untrusted. Native code owns navigation and approval.
(() => {
  const key = '__vitlaneWebViewAgentV1';
  if (Object.prototype.hasOwnProperty.call(window, key)) return window[key];
  const clean = (value, limit = 200) => String(value || '').replace(/[\u0000-\u001f\u007f]/g, ' ')
    .replace(/\s+/g, ' ').trim().slice(0, limit);
  const redact = (value, limit = 200) => clean(value, limit * 2)
    .replace(/\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/gi, '[이메일 숨김]')
    .replace(/\b(?:\+?82[- .]?)?0?1[016789][- .]?\d{3,4}[- .]?\d{4}\b/g, '[전화번호 숨김]')
    .replace(/\b\d{6}[- ]?[1-8]\d{6}\b/g, '[개인정보 숨김]')
    .replace(/\b(?:\d[ -]?){13,19}\b/g, '[긴 번호 숨김]')
    .replace(/\bsk-[A-Za-z0-9_-]{12,}\b/g, '[키 숨김]').slice(0, limit);
  const prefix = 'e' + Math.random().toString(36).slice(2, 11) + '_';
  const ids = new WeakMap();
  let serial = 0;
  let records = new Map();
  let observedUrl = '';
  const excludedSelector = 'script,style,noscript,template,iframe,svg,canvas,input,textarea,select,option,'
    + '[hidden],[aria-hidden="true"],[inert],[contenteditable="true"]';
  const accountSelector = '[class*="account" i],[id*="account" i],[class*="profile" i],'
    + '[id*="profile" i],[class*="user-info" i],[class*="userInfo"],[data-user-name],[data-customer-name],'
    + '[class*="address" i],[class*="recipient" i],[class*="review-author" i]';

  function visible(element) {
    if (!element || !element.isConnected || element.closest('[hidden],[aria-hidden="true"],[inert]')) return false;
    const style = getComputedStyle(element);
    return style.display !== 'none' && style.visibility !== 'hidden' && style.visibility !== 'collapse'
      && style.opacity !== '0' && element.getClientRects().length > 0;
  }
  function labelText(element) {
    if (!element || element.matches('input,textarea,select,script,style,template,[hidden],[aria-hidden="true"]')) return '';
    const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
    const pieces = [];
    let node, count = 0;
    while ((node = walker.nextNode()) && count++ < 100) {
      if (!node.parentElement?.closest('input,textarea,select,script,style,template,[hidden],[aria-hidden="true"]')) pieces.push(node.textContent);
    }
    return clean(pieces.join(' '), 200);
  }
  function name(element) {
    const labelled = (element.getAttribute('aria-labelledby') || '').split(/\s+/).filter(Boolean)
      .map(id => labelText(document.getElementById(id))).join(' ');
    const labels = Array.from(element.labels || []).map(labelText).join(' ');
    return clean(element.getAttribute('aria-label') || labelled || labels || labelText(element)
      || element.getAttribute('placeholder') || element.getAttribute('title')
      || (element.matches('input[type="submit"],input[type="button"]') ? element.getAttribute('value') : '') || '', 200);
  }
  function url(value) {
    try {
      const parsed = new URL(value, location.href);
      if (!['http:', 'https:'].includes(parsed.protocol) || parsed.username || parsed.password) return null;
      const host = parsed.hostname.toLowerCase().replace(/\.$/, '');
      if (!host.includes('.') || host.includes(':') || /(?:^|\.)(?:localhost|local|internal|lan|home\.arpa|test|invalid)$/.test(host)) return null;
      if (/^[0-9.]+$/.test(host)) {
        const [a, b] = host.split('.').map(Number);
        if (a === 0 || a === 10 || a === 127 || a >= 224 || (a === 100 && b >= 64 && b <= 127)
          || (a === 169 && b === 254) || (a === 172 && b >= 16 && b <= 31)
          || (a === 192 && (b === 0 || b === 168)) || (a === 198 && (b === 18 || b === 19 || b === 51))
          || (a === 203 && b === 0)) return null;
      }
      return parsed;
    } catch { return null; }
  }
  function privateUrl(value) {
    const parsed = url(value);
    if (!parsed) return true;
    let path = parsed.pathname;
    try { path = decodeURIComponent(path); } catch { /* use undecoded path */ }
    return /^(?:login|signin|auth|accounts?|my|mypage|checkout|cart|payment|payments|wallet)\./i.test(parsed.hostname)
      || /(?:^|\/)(?:login|signin|sign-in|logout|auth|oauth|account|myaccount|mypage|profile|orders?|checkout|payment|payments|wallet|addresses|address|cart|basket|session)(?:\/|$|\.(?:html?|php|aspx?|jsp|pang)$)/i.test(path)
      || /(?:^|\/)(?:cartView|orderForm|orderSummary|paymentForm|orderConfirm)(?:\.|\/|$)/i.test(path);
  }
  function finalControl(element) {
    const label = name(element).normalize('NFKC');
    const hints = clean([element.id, element.getAttribute('name'), element.getAttribute('data-action')].join(' '), 300);
    return /(?:결제\s*하기|결제\s*확정|주문\s*하기|주문\s*확정|구매\s*확정|예약\s*확정|구독\s*시작|구독\s*확정|취소\s*확정)/i.test(label)
      || /\b(?:place\s+(?:my\s+|your\s+)?order|submit\s+order|confirm\s+(?:order|purchase|payment|booking|cancellation)|complete\s+(?:purchase|payment|order)|pay\s*(?:now|securely)?|subscribe\s+now|book\s+now)\b/i.test(label)
      || /(?:final[-_]?order|place[-_]?order|submit[-_]?payment|confirm[-_]?payment|pay[-_]?now)/i.test(hints);
  }
  function sideEffectLink(element) {
    const destination = url(element.href);
    if (!destination) return true;
    if (/(?:\b(?:add\s+to\s+(?:cart|bag|basket)|buy\s*now|remove|delete|unsubscribe|redeem|claim|follow|like|cancel\s+(?:order|booking|subscription))\b|장바구니\s*담기|바로\s*구매|삭제|구독\s*해지|팔로우|좋아요|주문\s*취소)/i.test(name(element))) return true;
    let path = destination.pathname;
    try { path = decodeURIComponent(path); } catch { /* use undecoded path */ }
    if (/(?:^|\/)(?:add[-_]?to[-_]?(?:cart|bag|basket)|buy[-_]?now|add|remove|delete|unsubscribe|cancel)(?:[\/._-]|$)/i.test(path)) return true;
    for (const [key, value] of destination.searchParams) {
      if (/^(?:add|remove|delete|buy|cart|checkout|purchase|subscribe|unsubscribe)$/i.test(key)) return true;
      if (/^(?:action|cmd|command|operation|mode|task|do)$/i.test(key)
        && /(?:add|remove|delete|buy|cart|checkout|purchase|subscribe|cancel|order)/i.test(value)) return true;
    }
    return false;
  }
  function accountRoots() {
    const roots = [...document.querySelectorAll(accountSelector)];
    for (const element of document.querySelectorAll('a[href],button,[role="button"]')) {
      if (!visible(element)) continue;
      const label = name(element);
      if (/^(?:log\s*out|sign\s*out|로그아웃)$/i.test(label)
        || /(?:^|\/)logout(?:\/|\?|$)/i.test(element.getAttribute('href') || '')) {
        const container = element.closest('header,[role="banner"],[class*="header" i],nav,[role="navigation"]');
        roots.push(container && container !== document.body ? container : element);
      } else if (/^(?:my\s+account|my\s+orders|account|profile|로그인|내\s*계정|마이페이지|주문\s*내역)$/i.test(label)) {
        roots.push(element);
      }
    }
    return roots.filter(root => root !== document.body && root !== document.documentElement);
  }
  function excluded(element, roots) {
    return roots.some(root => root === element || root.contains(element));
  }
  function sensitiveReason() {
    if (privateUrl(location.href)) return '로그인·계정·장바구니·결제 화면은 직접 조작해 주세요.';
    for (const element of document.querySelectorAll('input,textarea,select')) {
      if (!visible(element) || element.type === 'hidden') continue;
      const hints = [element.type, element.autocomplete, element.name, element.id, name(element)].join(' ');
      if (element.type === 'password' || /(?:password|passwd|passcode|one-time-code|(?:^|[\s_-])otp(?:$|[\s_-])|cc-number|cc-csc|cc-exp|card[-_ ]?number|cardholder|cvv|cvc|비밀번호|인증번호|카드\s*번호|보안\s*코드)/i.test(hints)) {
        return '비밀번호·인증·결제 정보를 입력하는 화면은 직접 조작해 주세요.';
      }
    }
    const headings = [...document.querySelectorAll('h1,[role="dialog"] h2,[role="dialog"] [role="heading"]')]
      .filter(visible).map(element => clean(element.innerText, 120));
    if (headings.some(value => /^(?:checkout|order summary|confirm (?:your )?order|payment details|my account|주문서|주문[ /·]결제|결제 정보|주문 내역|배송지 정보)$/i.test(value))) {
      return '개인 주문·결제 화면은 직접 조작해 주세요.';
    }
    return '';
  }
  function isSearchField(element) {
    if (!element.matches('input,textarea') || element.disabled || element.readOnly
      || !['text', 'search', 'textarea'].includes((element.type || '').toLowerCase())) return false;
    const hints = [element.name, element.id, element.getAttribute('placeholder'), element.getAttribute('aria-label')].join(' ');
    return element.type === 'search' || element.getAttribute('role') === 'searchbox'
      || element.form?.getAttribute('role') === 'search'
      || /(?:\b(?:q|query|keyword|search|searchTerm|searchKeyword|searchQuery|searchWord|srchWord)\b|검색)/i.test(hints);
  }
  function searchForm(field, submitter = null) {
    if (!isSearchField(field)) return false;
    const form = field.form;
    if (!form) return field.type === 'search' || field.getAttribute('role') === 'searchbox';
    const method = submitter?.getAttribute('formmethod') || form.getAttribute('method') || 'get';
    const action = url(submitter?.getAttribute('formaction') || form.getAttribute('action') || location.href);
    const target = submitter?.getAttribute('formtarget') || form.getAttribute('target') || '';
    if (method.toLowerCase() !== 'get' || !action || action.origin !== location.origin
      || privateUrl(action.href) || !['', '_self'].includes(target.toLowerCase())) return false;
    const editable = [...form.elements].filter(item => !item.disabled && item.matches('textarea,select,input:not([type="hidden"]):not([type="submit"]):not([type="button"]):not([type="reset"])'));
    return editable.length === 1 && editable[0] === field;
  }
  function searchFieldForButton(element) {
    if (!element.form || !(element.matches('button') && element.type === 'submit'
      || element.matches('input[type="submit"]'))) return null;
    const fields = [...element.form.elements].filter(isSearchField);
    return fields.length === 1 && searchForm(fields[0], element) ? fields[0] : null;
  }
  function descriptor(element) {
    if (!visible(element) || element.disabled || element.getAttribute('aria-disabled') === 'true'
      || finalControl(element)) return null;
    const tag = element.tagName.toLowerCase();
    const role = element.getAttribute('role') || (tag === 'a' ? 'link' : element.matches('button,input[type="button"],input[type="submit"]') ? 'button' : tag);
    const item = { tag, role: clean(role, 40), label: redact(name(element), 200) };
    if (tag === 'a') {
      const destination = url(element.href);
      if (!destination || privateUrl(destination.href) || element.hasAttribute('download')) return null;
      item.href = destination.href;
    } else if (element.matches('input,textarea')) {
      item.inputType = element.type;
      if (isSearchField(element) && searchForm(element)) item.search = true;
      else if (!element.matches('input[type="button"],input[type="submit"],input[type="checkbox"],input[type="radio"]')) return null;
    }
    if (searchFieldForButton(element)) item.search = true;
    return item.label || item.search ? item : null;
  }
  function fingerprint(element) {
    // Input content remains only in this local comparison, never in an observation.
    const form = element.form;
    return JSON.stringify([element.tagName, element.getAttribute('role'), name(element), element.getAttribute('href'),
      element.getAttribute('type'), element.getAttribute('name'), element.getAttribute('formaction'),
      element.getAttribute('formmethod'), element.getAttribute('formtarget'), element.getAttribute('target'),
      element.getAttribute('onclick'), element.getAttribute('onchange'), element.getAttribute('oninput'),
      element.disabled, element.readOnly, element.getAttribute('aria-disabled'), element.checked,
      isSearchField(element) ? element.value : null,
      form?.getAttribute('action'), form?.getAttribute('method'), form?.getAttribute('target')]);
  }
  function observe() {
    records = new Map();
    observedUrl = location.href;
    const reason = sensitiveReason();
    if (reason) return { url: observedUrl, title: '', text: '', elements: [], sensitive: true, reason };
    const roots = accountRoots();
    const elements = [];
    const controls = [...document.querySelectorAll('a[href],button,input,textarea,[role="button"],[role="link"],[role="searchbox"]')];
    const inViewport = element => {
      const rectangle = element.getBoundingClientRect();
      return rectangle.bottom > 0 && rectangle.top < innerHeight && rectangle.right > 0 && rectangle.left < innerWidth;
    };
    // Scrolling must make newly visible controls available even on long navigation-heavy pages.
    controls.sort((left, right) => Number(inViewport(right)) - Number(inViewport(left)));
    for (const element of controls) {
      if (elements.length >= 60) break;
      if (excluded(element, roots)) continue;
      const item = descriptor(element);
      if (!item) continue;
      if (!ids.has(element)) ids.set(element, prefix + (++serial));
      item.id = ids.get(element);
      records.set(item.id, { element, fingerprint: fingerprint(element) });
      elements.push(item);
    }
    const walker = document.createTreeWalker(document.body || document.documentElement, NodeFilter.SHOW_TEXT);
    const text = [];
    let length = 0, visited = 0, node;
    while ((node = walker.nextNode()) && length < 10000 && visited++ < 20000) {
      const parent = node.parentElement;
      if (!parent || parent.closest(excludedSelector) || excluded(parent, roots) || !visible(parent)) continue;
      const part = redact(node.textContent, Math.min(10000 - length, 2000));
      if (part) { text.push(part); length += part.length + 1; }
    }
    return { url: observedUrl, title: redact(document.title, 300), text: text.join(' ').slice(0, 10000), elements, sensitive: false };
  }
  function refuse(message) { return { ok: false, message }; }
  function action(command, expectedUrl) {
    if (!command || typeof command !== 'object' || Array.isArray(command)) return refuse('잘못된 동작입니다.');
    if (typeof expectedUrl !== 'string' || location.href !== expectedUrl || observedUrl !== expectedUrl) {
      return refuse('페이지 주소가 변경되었습니다. 다시 확인해 주세요.');
    }
    const reason = sensitiveReason();
    if (reason) { records.clear(); return refuse(reason); }
    if (command.type === 'inspect') return { ok: true };
    if (command.type === 'scroll') {
      if (!['up', 'down'].includes(command.direction)) return refuse('스크롤 방향을 확인하세요.');
      records.clear();
      window.scrollBy({ top: (command.direction === 'down' ? 1 : -1) * Math.max(200, innerHeight * 0.7), behavior: 'instant' });
      return { ok: true };
    }
    const target = records.get(command.targetId);
    if (!target || !target.element.isConnected || target.element.ownerDocument !== document
      || !visible(target.element) || target.fingerprint !== fingerprint(target.element)
      || excluded(target.element, accountRoots())) return refuse('대상이 변경되었습니다. 페이지를 다시 확인해 주세요.');
    const element = target.element;
    const item = descriptor(element);
    if (!item || finalControl(element)) return refuse('이 항목은 직접 조작해 주세요.');
    if (command.type === 'type') {
      if (!item.search || !isSearchField(element) || !searchForm(element)) return refuse('공개 검색창만 입력할 수 있습니다.');
      if (typeof command.text !== 'string' || command.text.trim().length === 0 || command.text.length > 300
        || /[\u0000-\u001f\u007f]/.test(command.text)
        || /(?:\b(?:password|passwd|otp|cvv|cvc|api[_ -]?key|secret)\b|\bsk-[A-Za-z0-9_-]{8,}|비밀번호|인증번호|카드\s*번호|주민등록|https?:\/\/|\S+@\S+\.\S+)/i.test(command.text)
        || command.text.replace(/\D/g, '').length >= 11) return refuse('개인정보가 없는 짧은 검색어만 입력해 주세요.');
      if (command.submit !== undefined && typeof command.submit !== 'boolean') return refuse('잘못된 검색 제출 설정입니다.');
      records.clear();
      const prototype = element.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
      const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set;
      if (!setter) return refuse('검색창을 입력할 수 없습니다.');
      setter.call(element, command.text.trim());
      element.dispatchEvent(new Event('input', { bubbles: true }));
      element.dispatchEvent(new Event('change', { bubbles: true }));
      if (command.submit === true) {
        if (location.href !== expectedUrl) return { ok: true, navigating: true };
        if (!element.isConnected || sensitiveReason() || !searchForm(element)
          || element.value !== command.text.trim()) return refuse('검색창이 변경되었습니다. 직접 확인해 주세요.');
        if (element.form) HTMLFormElement.prototype.requestSubmit.call(element.form);
        else {
          element.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', keyCode: 13, which: 13, bubbles: true, cancelable: true }));
          element.dispatchEvent(new KeyboardEvent('keyup', { key: 'Enter', code: 'Enter', keyCode: 13, which: 13, bubbles: true }));
        }
        return { ok: true, navigating: true };
      }
      return { ok: true };
    }
    if (command.type !== 'click') return refuse('지원하지 않는 동작입니다.');
    const searchField = searchFieldForButton(element);
    if ((!item.href && !searchField || item.href && sideEffectLink(element))
      && command.approved !== true) return refuse('이 동작은 앱에서 먼저 승인해 주세요.');
    if (element.form && !searchField && privateUrl(element.form.action || location.href)
      && !/^(?:buy\s*now|바로\s*구매)$/i.test(name(element))) return refuse('주문·결제 양식은 직접 조작해 주세요.');
    records.clear();
    if (item.href) {
      if (!url(element.href) || privateUrl(element.href)) return refuse('이 링크는 직접 열어 주세요.');
      element.click();
      return { ok: true, navigating: true };
    }
    if (searchField) {
      if (!searchForm(searchField, element)) return refuse('검색 양식이 변경되었습니다.');
      HTMLFormElement.prototype.requestSubmit.call(element.form, element);
    } else element.click();
    return { ok: true, navigating: true };
  }
  const api = Object.freeze({ observe, action });
  Object.defineProperty(window, key, { value: api, configurable: false, writable: false, enumerable: false });
  return api;
})()
