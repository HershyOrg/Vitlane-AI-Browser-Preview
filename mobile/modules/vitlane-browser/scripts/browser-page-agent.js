// This code runs in Android WebView's page world, not an isolated Chromium world.
// Treat all returned content as untrusted. Native code owns navigation and approval.
(() => {
  const key = '__vitlaneWebViewAgentV1';
  if (Object.prototype.hasOwnProperty.call(window, key)) return window[key];
  const clean = (value, limit = 200) => String(value || '').replace(/[\u0000-\u001f\u007f]/g, ' ')
    .replace(/\s+/g, ' ').trim().slice(0, limit);
  const localPrivateValues = new Set();
  const redact = (value, limit = 200) => {
    let result = clean(value, Math.max(limit * 2, 1000));
    for (const privateValue of localPrivateValues) if (privateValue.length >= 2) {
      result = result.split(privateValue).join('[로컬 개인정보 숨김]');
    }
    return result.replace(/\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/gi, '[이메일 숨김]')
    .replace(/\b(?:\+?82[- .]?)?0?1[016789][- .]?\d{3,4}[- .]?\d{4}\b/g, '[전화번호 숨김]')
    .replace(/\b\d{6}[- ]?[1-8]\d{6}\b/g, '[개인정보 숨김]')
    .replace(/\b(?:\d[ -]?){13,19}\b/g, '[긴 번호 숨김]')
    .replace(/\bsk-[A-Za-z0-9_-]{12,}\b/g, '[키 숨김]').slice(0, limit);
  };
  const prefix = 'e' + Math.random().toString(36).slice(2, 11) + '_';
  const ids = new WeakMap();
  let serial = 0;
  const imageIds = new WeakMap();
  let imageSerial = 0;
  let records = new Map();
  let observedUrl = '';
  const documentId = 'd' + Math.random().toString(36).slice(2, 13);
  let revision = 0, previousRevision = -1, lastMutation = Date.now();
  let inspection = null, actionPreview = null, lastActivation = null;
  const semanticInterruptSelector = 'dialog[open],[role="alertdialog"],[aria-modal="true"],[role="dialog"]';
  const authoredInterruptSelector = '[class*="modal" i],[id*="modal" i],[class*="popup" i],[id*="popup" i],'
    + '[class*="drawer" i],[id*="drawer" i],[class*="sheet" i],[data-modal],[data-testid*="modal" i]';
  const interruptSelector = semanticInterruptSelector + ',' + authoredInterruptSelector;
  const explicitControlSelector = 'a[href],button,input,textarea,select,summary,[role="button"],[role="link"],'
    + '[role="searchbox"],[role="checkbox"],[role="switch"],[role="radio"],[role="tab"],[role="option"],'
    + '[role="combobox"],[role="listbox"],[role="menuitem"],[role="region"],[role="dialog"],[tabindex]';
  const authoredValues = new WeakMap();
  const humanIds = new WeakMap();
  let humanSerial = 0, humanObservedUrl = '';
  let humanRecords = new Map();
  const secretIds = new WeakMap();
  let secretSerial = 0, secretObservedUrl = '';
  let secretRecords = new Map();
  const mutations = new MutationObserver(changes => {
    if (changes.length) { revision++; lastMutation = Date.now(); }
  });
  mutations.observe(document, { subtree: true, childList: true, characterData: true, attributes: true,
    attributeFilter: ['class', 'style', 'hidden', 'disabled', 'readonly', 'aria-busy', 'aria-expanded',
      'aria-checked', 'aria-selected', 'aria-disabled', 'aria-hidden', 'href', 'role', 'value'] });
  function flushMutations() {
    if (mutations.takeRecords().length) { revision++; lastMutation = Date.now(); }
  }
  function inViewport(element) {
    const r = element.getBoundingClientRect();
    return r.bottom > 0 && r.top < innerHeight && r.right > 0 && r.left < innerWidth;
  }
  function disabled(element) {
    return !!element.disabled || element.matches(':disabled') || !!element.closest('[aria-disabled="true"]');
  }
  function scrollable(element) {
    return element.scrollHeight > element.clientHeight + 8 && element.clientHeight > 40
      && /auto|scroll/.test(getComputedStyle(element).overflowY);
  }
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
  function labelText(element, limit = 200) {
    if (!element || element.matches('input,textarea,select,script,style,template,[hidden],[aria-hidden="true"]')) return '';
    const walker = document.createTreeWalker(element, NodeFilter.SHOW_TEXT);
    const pieces = [];
    let node, count = 0;
    while ((node = walker.nextNode()) && count++ < 100) {
      if (!node.parentElement?.closest('input,textarea,select,script,style,template,[hidden],[aria-hidden="true"]')) pieces.push(node.textContent);
    }
    return clean(pieces.join(' '), limit);
  }
  function filterResultsControl(element) {
    if (!element?.matches?.('button,[role="button"]')) return false;
    const hints = clean([element.id, element.className, element.getAttribute('data-testid'),
      element.parentElement?.id, element.parentElement?.className].join(' '), 500);
    return /(?:show[-_ ]?results?|view[-_ ]?results?|apply[-_ ]?filters?|filter[-_ ]?results?)/i.test(hints)
      || !!element.querySelector('[id*="button-stats" i],[class*="result-count" i]');
  }
  function name(element) {
    const labelled = (element.getAttribute('aria-labelledby') || '').split(/\s+/).filter(Boolean)
      .map(id => labelText(document.getElementById(id))).join(' ');
    const labels = Array.from(element.labels || []).map(label => labelText(label)).join(' ');
    const rendered = labelText(element);
    // Loro Piana's Algolia drawer labels its dynamic "View N Products" button as aria-label="Close".
    // The rendered result action is the useful operation; treating it as a generic dismiss loses
    // the selected-filter postcondition and causes the agent to reopen the drawer.
    const filterResultLabel = filterResultsControl(element) ? rendered || 'View results' : '';
    return clean(filterResultLabel || element.getAttribute('aria-label') || labelled || labels || rendered
      || element.getAttribute('placeholder') || element.getAttribute('title')
      || (element.matches('input[type="submit"],input[type="button"]') ? element.getAttribute('value') : '')
      || (element.matches('a,button,[role="button"]') ? element.querySelector('img[alt]')?.getAttribute('alt') : '') || '', 200);
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
    return /(?:결제\s*(?:하기|확정|완료)|구매\s*확정)/i.test(label)
      || /\b(?:confirm\s+(?:purchase|payment)|complete\s+(?:purchase|payment)|pay\s*(?:now|securely)?)\b/i.test(label)
      || /(?:submit[-_]?payment|confirm[-_]?payment|pay[-_]?now)/i.test(hints);
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
  function accountRoots(visibility = visible, labelFor = name) {
    const roots = [...document.querySelectorAll(accountSelector)];
    for (const element of document.querySelectorAll('a[href],button,[role="button"]')) {
      if (!visibility(element)) continue;
      const label = labelFor(element);
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
    for (const element of document.querySelectorAll('input,textarea,select')) {
      if (!visible(element) || element.type === 'hidden') continue;
      const hints = [element.type, element.autocomplete, element.name, element.id, name(element)].join(' ');
      if (element.type === 'password' || /(?:password|passwd|passcode|one-time-code|(?:^|[\s_-])otp(?:$|[\s_-])|cc-number|cc-csc|cc-exp|card[-_ ]?number|cardholder|cvv|cvc|비밀번호|인증번호|카드\s*번호|보안\s*코드)/i.test(hints)) {
        return '비밀번호·인증·결제 정보를 입력하는 화면은 직접 조작해 주세요.';
      }
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
  function sensitiveField(element) {
    const hints = [element.type, element.autocomplete, element.name, element.id, name(element)].join(' ');
    return /(?:password|passwd|passcode|one-time-code|(?:^|[\s_-])otp(?:$|[\s_-])|cc-|card[-_ ]?number|cardholder|cvv|cvc|api[-_ ]?key|secret|token|e-?mail|phone|mobile|(?:^|[\s_-])tel(?:ephone)?(?:$|[\s_-])|address|recipient|birth|username|given-name|family-name|full-name|비밀번호|인증번호|카드|주민등록|이메일|전화|연락처|주소|수령인|생년|성명)/i.test(hints);
  }
  function editableField(element) {
    return element.matches('input,textarea') && !disabled(element) && !element.readOnly
      && element.getAttribute('aria-readonly') !== 'true'
      && ['text', 'search', 'textarea', 'number', 'date', 'month', 'time', 'week', 'datetime-local'].includes(element.type)
      && !sensitiveField(element);
  }
  function publicText(value, limit) {
    return typeof value === 'string' && value.length <= limit
      && !/[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/.test(value)
      && !/(?:\b(?:password|passwd|otp|cvv|cvc|api[_ -]?key|secret)\b|\bsk-[A-Za-z0-9_-]{8,}|비밀번호|인증번호|카드\s*번호|주민등록|\S+@\S+\.\S+)/i.test(value)
      && !/(?:\d[ -]?){11,}/.test(value);
  }
  function safeUrl(value) {
    const parsed = url(value);
    if (!parsed) return '';
    if ([...parsed.searchParams].some(([key]) => /(?:token|secret|password|auth|session|api[-_]?key|email|phone)/i.test(key))) return '';
    let decoded;
    try { decoded = decodeURIComponent(parsed.pathname + parsed.search + parsed.hash); } catch { return ''; }
    if (redact(decoded, 4096) !== clean(decoded, 4096)
      || /(?:token|secret|password|auth|session|api[-_]?key)=/i.test(parsed.hash)) return '';
    return redact(parsed.href, 2000) === parsed.href ? parsed.href : '';
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
  const contextBoundary = 'tr,[role="row"],li,[role="listitem"],article,[itemscope],fieldset,form,section,[role="group"],[role="region"],'
    + '[class*="facet" i],[class*="refinement" i],[class*="filter-group" i],[data-testid*="facet" i]';
  function cardLike(element) {
    // Class names help locate a container; their arbitrary values never leave the page.
    return /(?:^|[\s_-])(?:card|tile|result|item|product|room|offer|listing)(?:$|[\s_-])/i.test(element.className || '')
      && !/(?:^|[\s_-])(?:grid|list|container|wrapper|actions?|controls|buttons)(?:$|[\s_-])/i.test(element.className || '');
  }
  function relationText(root, target, roots, limit) {
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    const parts = [];
    let visited = 0, length = 0, node;
    while ((node = walker.nextNode()) && visited++ < 400 && length < limit) {
      const parent = node.parentElement;
      if (!parent || target?.contains(parent) || parent.closest(excludedSelector)
        || parent.closest('button,[role="button"]') || excluded(parent, roots) || !visible(parent)) continue;
      const text = redact(node.textContent, limit - length);
      if (text) { parts.push(text); length += text.length + 1; }
    }
    return parts.join(' ').slice(0, limit);
  }
  function relationTitle(container, roots) {
    const labelled = (container.getAttribute('aria-labelledby') || '').split(/\s+/).filter(Boolean)
      .map(id => document.getElementById(id)).filter(node => node && visible(node) && !excluded(node, roots))
      .map(node => relationText(node, null, roots, 200)).join(' ');
    const titleSelector = container.matches('tr,[role="row"]')
      ? 'th,[role="rowheader"],td,[role="cell"]'
      : 'legend,h1,h2,h3,h4,h5,h6,[role="heading"],[class*="refinement-title" i],[class*="facet-title" i]';
    const title = [...container.querySelectorAll(titleSelector)].find(node => {
      if (!visible(node) || excluded(node, roots)) return false;
      if (node.matches('[class*="refinement-title" i],[class*="facet-title" i]')) return true;
      // A category/list heading must not borrow another nested product card's heading.
      for (let owner = node.parentElement; owner && owner !== container; owner = owner.parentElement) {
        if (owner.matches(contextBoundary) || cardLike(owner)) return false;
      }
      return true;
    });
    const titleText = title && title.matches('[class*="refinement-title" i],[class*="facet-title" i]')
      ? labelText(title, 200) : title ? relationText(title, null, roots, 200) : '';
    return redact(container.getAttribute('aria-label') || labelled || titleText, 200);
  }
  function relation(element, roots) {
    let container = null;
    for (let parent = element.parentElement, depth = 0; parent && depth++ < 8; parent = parent.parentElement) {
      if (parent.matches('body,html,main,[role="main"]')) break;
      if (excluded(parent, roots)) return { group: '', context: '' };
      if (parent.matches(contextBoundary) || cardLike(parent)) { container = parent; break; }
      // Common anonymous div cards still have an identity heading outside their action row.
      if ([...parent.children].some(child => child.matches('h1,h2,h3,h4,h5,h6,[role="heading"]'))) {
        container = parent; break;
      }
    }
    if (!container) return { group: '', context: '' };
    const context = relationText(container, element, roots, 500);
    const title = relationTitle(container, roots);
    let group = title || context;
    if (!title || container.matches('form,fieldset,[role="group"]')) {
      for (let parent = container.parentElement, depth = 0; parent && depth++ < 5; parent = parent.parentElement) {
        if (parent.matches('body,html,main,[role="main"]') || excluded(parent, roots)) break;
        if (!parent.matches(contextBoundary) && !cardLike(parent)) continue;
        const outer = relationTitle(parent, roots);
        if (outer && outer !== title) { group = outer + (title ? ' / ' + title : ''); break; }
      }
    }
    group = redact(group, 200);
    return { group, context };
  }
  function dismissControl(element) {
    if (!element.matches('button,[role="button"]') || element.hasAttribute('href') || element.closest('a[href]')
      || !/^(?:close|dismiss|닫기|창\s*닫기|×|✕)$/i.test(name(element).normalize('NFKC'))) return false;
    const modal = interruptRoot(element);
    if (!modal || !visible(modal)) return false;
    // A close-looking submit/reset button can still change a form or finalize an order.
    return !(element.form && element.matches('button,input') && element.type !== 'button');
  }
  function interruptSurface(element) {
    if (!element || !visible(element)) return false;
    if (element.matches(semanticInterruptSelector)) return true;
    if (!element.matches(authoredInterruptSelector)
      || element.matches('button,a,input,textarea,select,option')) return false;
    const bounds = element.getBoundingClientRect(), style = getComputedStyle(element);
    return style.position === 'fixed' || bounds.width * bounds.height >= innerWidth * innerHeight * 0.08;
  }
  function interruptRoot(element) {
    for (let root = element; root; root = root.parentElement) {
      if (root.matches?.(interruptSelector) && interruptSurface(root)) return root;
    }
    return null;
  }
  function labelChoice(element) {
    if (!element?.matches?.('label')) return null;
    const target = element.htmlFor ? document.getElementById(element.htmlFor) : element.querySelector('input');
    return target?.matches?.('input[type="checkbox"],input[type="radio"]') ? target : null;
  }
  function facetChoiceState(element) {
    if (!element?.matches?.('a[href],button,[role="option"],[role="checkbox"],[role="radio"]')) return null;
    const ownHints = clean([element.id, element.className, element.getAttribute('data-testid'),
      element.getAttribute('data-value')].join(' '), 500);
    const container = element.closest('[class*="facet" i],[class*="refinement" i],[class*="filter-option" i],'
      + '[data-testid*="facet" i],[data-testid*="filter" i],#refinements-parent');
    if (!/(?:refinement|facet|filter)[-_ ]?(?:link|option|value|choice|item)/i.test(ownHints)
      && !(container && element.matches('[role="option"],[role="checkbox"],[role="radio"]'))) return null;
    if (/(?:refinement|facet|filter)[-_ ]?(?:title|toggle|trigger|header)/i.test(ownHints)) return null;
    if (element.hasAttribute('aria-selected')) return element.getAttribute('aria-selected') === 'true';
    if (element.hasAttribute('aria-pressed')) return element.getAttribute('aria-pressed') === 'true';
    if (element.hasAttribute('aria-checked')) return element.getAttribute('aria-checked') === 'true';
    return /(?:^|\s)(?:active|selected|is-selected|checked|chosen)(?:\s|$)/i.test(element.className || '');
  }
  function implicitButton(element) {
    if (!element?.matches?.('div,span,p,h2,h3,h4,h5,h6') || !visible(element)
      || element.matches(explicitControlSelector)) return false;
    const label = name(element);
    if (!label || label.length > 80 || element.querySelector(explicitControlSelector)) return false;
    if ([...element.children].some(child => visible(child) && name(child) === label)) return false;
    const hints = clean([element.id, element.className, element.getAttribute('data-testid'),
      element.getAttribute('data-action')].join(' '), 400);
    const exactCatalogControl = /^(?:filters?|sort\s+by|filter\s+by|필터|정렬|색상|컬러|소재|재질|사이즈|크기|카테고리|categories|category|color|colou?r|materials?|sizes?)$/i.test(label);
    const hinted = /(?:filter|sort|facet|refinement|accordion|toggle|trigger)/i.test(hints);
    return (exactCatalogControl && (hinted || getComputedStyle(element).cursor === 'pointer'
      || typeof element.onclick === 'function')) || (hinted && getComputedStyle(element).cursor === 'pointer');
  }
  function controlCandidates(includeScrollPanes = true) {
    const controls = [...document.querySelectorAll(explicitControlSelector)];
    for (const label of [...document.querySelectorAll('label')].slice(0, 1000)) {
      if (labelChoice(label) && !controls.includes(label)) controls.push(label);
    }
    for (const candidate of [...document.querySelectorAll('div,span,p,h2,h3,h4,h5,h6')].slice(0, 3000)) {
      if (implicitButton(candidate) && !controls.includes(candidate)) controls.push(candidate);
    }
    if (includeScrollPanes) for (const candidate of [...document.querySelectorAll('main,section,div,ul')].slice(0, 2000)) {
      if (scrollable(candidate) && !controls.includes(candidate)) controls.push(candidate);
    }
    return controls;
  }
  function descriptor(element, roots = accountRoots()) {
    if (!visible(element) || finalControl(element) || element.matches('input[type="hidden"]')) return null;
    const tag = element.tagName.toLowerCase();
    const choice = labelChoice(element);
    const role = element.getAttribute('role') || (choice ? choice.type
      : implicitButton(element) ? 'button' : tag === 'a' ? 'link'
      : element.matches('input[type="checkbox"]') ? 'checkbox'
      : element.matches('input[type="radio"]') ? 'radio'
      : tag === 'select' ? 'combobox' : tag === 'summary' ? 'button'
      : element.matches('button,input[type="button"],input[type="submit"]') ? 'button'
      : element.matches('input,textarea') ? 'textbox' : tag);
    const item = { tag, role: clean(role, 40), label: redact(name(element), 200), inViewport: inViewport(element),
      disabled: choice ? disabled(choice) : disabled(element) };
    const r = element.getBoundingClientRect();
    item.bounds = { x: Math.round(r.x), y: Math.round(r.y), width: Math.round(r.width), height: Math.round(r.height) };
    if (tag === 'a') {
      item.href = safeUrl(element.href);
      if (!item.href || element.hasAttribute('download')) return null;
    } else if (element.matches('input,textarea,select')) {
      if (sensitiveField(element)) return null;
      item.inputType = element.type;
      if (isSearchField(element) && searchForm(element)) item.search = true;
      if (editableField(element)) item.editable = true;
      else if (tag !== 'select' && !element.matches('input[type="button"],input[type="submit"],input[type="checkbox"],input[type="radio"]')) return null;
      // Never transmit pre-filled or user-entered values. Agent-authored values can be verified locally.
      if (authoredValues.has(element)) item.valueMatchesLastInput = authoredValues.get(element) === element.value;
      item.hasValue = !!element.value;
    }
    if (tag === 'select') {
      item.multiple = element.multiple;
      item.options = [...element.options].slice(0, 50).map(option => ({
        value: redact(option.value, 200), label: redact(option.label || option.textContent, 200),
        selected: option.selected, disabled: disabled(option),
      })).filter(option => publicText(option.value, 200) && publicText(option.label, 200));
    }
    if (choice) { item.inputType = choice.type; item.checked = !!choice.checked; }
    else if (element.matches('input[type="checkbox"],input[type="radio"]')) item.checked = !!element.checked;
    else if (element.hasAttribute('aria-checked')) item.checked = element.getAttribute('aria-checked') === 'true';
    for (const state of ['expanded', 'selected', 'pressed']) {
      if (element.hasAttribute('aria-' + state)) item[state] = element.getAttribute('aria-' + state) === 'true';
    }
    const facetState = facetChoiceState(element);
    if (facetState !== null) item.selected = facetState;
    if (scrollable(element)) {
      item.scrollable = true;
      for (const metric of ['scrollTop', 'scrollHeight', 'clientHeight']) {
        const value = element[metric];
        if (typeof value === 'number' && Number.isFinite(value)) item[metric] = Math.max(0, Math.min(1e9, value));
      }
    }
    if (searchFieldForButton(element)) item.search = true;
    if (dismissControl(element)) item.dismiss = true;
    if (interruptRoot(element)) item.interrupt = true;
    if (!item.label && item.scrollable) item.label = 'Scrollable content';
    const related = relation(element, roots);
    if (related.group) item.group = related.group;
    if (related.context) item.context = related.context;
    return item.label || item.search ? item : null;
  }
  function fingerprint(element, roots = accountRoots(), related = relation(element, roots)) {
    // Input content remains only in this local comparison, never in an observation.
    const form = element.form, choice = labelChoice(element);
    return JSON.stringify([element.tagName, element.getAttribute('role'), name(element), element.getAttribute('href'),
      element.getAttribute('type'), element.getAttribute('name'), element.getAttribute('formaction'),
      element.getAttribute('formmethod'), element.getAttribute('formtarget'), element.getAttribute('target'),
      element.getAttribute('onclick'), element.getAttribute('onchange'), element.getAttribute('oninput'),
      element.disabled, element.readOnly, element.getAttribute('aria-disabled'), element.checked,
      choice?.disabled, choice?.checked,
      element.matches('input,textarea,select') ? element.value : null,
      element.matches('select') ? [...element.options].map(option => [option.value, option.label, option.disabled, option.selected]) : null,
      element.getAttribute('aria-checked'), element.getAttribute('aria-expanded'),
      facetChoiceState(element),
      related.group || '', related.context || '', dismissControl(element),
      form?.getAttribute('action'), form?.getAttribute('method'), form?.getAttribute('target')]);
  }
  function screenshotVisible(element) {
    // aria-hidden/inert hide semantics, not pixels. A native WebView capture still draws them.
    if (!element?.isConnected || !inViewport(element) || !element.getClientRects().length) return false;
    const style = getComputedStyle(element);
    if (style.visibility === 'hidden' || style.visibility === 'collapse') return false;
    for (let node = element; node; node = node.parentElement) {
      const current = node === element ? style : getComputedStyle(node);
      if (current.display === 'none' || current.opacity === '0') return false;
    }
    return true;
  }
  function screenshotSafe(roots) {
    const visiblePixels = new WeakMap();
    const shown = element => {
      if (!visiblePixels.has(element)) visiblePixels.set(element, screenshotVisible(element));
      return visiblePixels.get(element);
    };
    if (roots.some(shown) || accountRoots(shown, element => name(element) || clean(element.textContent, 200)).some(shown)) return false;
    if ([...document.querySelectorAll('iframe,video,canvas,object,embed')].some(shown)) return false;
    const controls = document.querySelectorAll('input,textarea,select,[contenteditable]');
    if (controls.length > 1000) return false;
    for (const element of controls) {
      if (!shown(element)) continue;
      if (sensitiveField(element)) return false;
      if (element.matches('[contenteditable]:not([contenteditable="false"])')) {
        if (clean(element.innerText || element.textContent, 1)) return false;
      } else {
        const value = String(element.value || '');
        // Search text can be public, but an email/phone entered into search must not be captured.
        if (value.length > 2000 || redact(value, 2000) !== clean(value, 2000)) return false;
        if (value && !isSearchField(element)
          && !element.matches('input[type="button"],input[type="submit"],input[type="reset"],input[type="checkbox"],input[type="radio"],input[type="range"],input[type="color"]')) return false;
      }
    }
    // Inspect raw rendered text before ordinary observation redaction/account exclusion. Scan
    // limits fail closed; nothing from this local comparison is returned to the model or UI.
    const walker = document.createTreeWalker(document.body || document.documentElement, NodeFilter.SHOW_TEXT);
    const pieces = [];
    let node, visited = 0, length = 0;
    while ((node = walker.nextNode())) {
      if (++visited > 6000) return false;
      const parent = node.parentElement;
      if (!parent || parent.closest('script,style,noscript,template,input,textarea,select') || !shown(parent)) continue;
      const value = node.textContent || '';
      length += value.length;
      if (length > 30000) return false;
      pieces.push(value);
    }
    const raw = pieces.join('');
    return redact(raw, 30000) === clean(raw, 30000);
  }
  function textContent(root, roots, limit = 10000) {
    const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
    const foreground = [], background = [];
    let foregroundLength = 0, backgroundLength = 0, visited = 0, node;
    while ((node = walker.nextNode()) && visited++ < 20000) {
      const parent = node.parentElement;
      if (!parent || parent.closest(excludedSelector) || excluded(parent, roots) || !visible(parent)) continue;
      const viewport = inViewport(parent);
      const length = viewport ? foregroundLength : backgroundLength;
      if (length >= limit) continue;
      const part = redact(node.textContent, Math.min(limit - length, 2000));
      if (part) {
        if (viewport) { foreground.push(part); foregroundLength += part.length + 1; }
        else { background.push(part); backgroundLength += part.length + 1; }
      }
    }
    return foreground.concat(background).join(' ').slice(0, limit);
  }
  // Reconstruct an allowlisted HTML sketch. Never send outerHTML, scripts, event handlers,
  // hidden fields, cookies, arbitrary data-* attributes or pre-filled form values to a model.
  function semanticHtml(root, roots, limit = 7000) {
    const escape = value => String(value).replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]);
    let remaining = limit, visited = 0;
    function visit(node, depth) {
      if (remaining <= 0 || depth > 30 || visited++ > 2500) return '';
      if (node.nodeType === Node.TEXT_NODE) {
        const value = escape(redact(node.textContent, Math.min(remaining, 600)));
        const output = value.slice(0, remaining);
        remaining -= output.length;
        return output;
      }
      if (node.nodeType !== Node.ELEMENT_NODE || !visible(node) || excluded(node, roots)
        || node.matches(excludedSelector) || node.matches('input,textarea,select,option')
        || node.matches(accountSelector)) return '';
      const rawTag = node.tagName.toLowerCase();
      const tag = /^(?:main|article|section|nav|header|footer|aside|div|p|h[1-6]|ul|ol|li|dl|dt|dd|table|thead|tbody|tr|th|td|a|button|label|form|details|summary|span|strong|em)$/.test(rawTag) ? rawTag : 'span';
      const attributes = [];
      if (ids.has(node)) attributes.push('ref="' + escape(ids.get(node)) + '"');
      const role = node.getAttribute('role');
      if (role && /^[a-z -]{1,40}$/.test(role)) attributes.push('role="' + escape(role) + '"');
      if (rawTag === 'a') {
        const href = safeUrl(node.href);
        if (!href) return '';
        attributes.push('href="' + escape(href) + '"');
      }
      const open = '<' + tag + (attributes.length ? ' ' + attributes.join(' ') : '') + '>';
      const close = '</' + tag + '>';
      if (remaining < open.length + close.length) return '';
      remaining -= open.length + close.length;
      return open + [...node.childNodes].map(child => visit(child, depth + 1)).join('') + close;
    }
    return visit(root, 0).slice(0, limit);
  }
  function pageImages(main, roots) {
    const seen = new Set(), candidates = [];
    for (const element of [...document.images].slice(0, 800)) {
      if (!visible(element) || excluded(element, roots) || !main.contains(element)) continue;
      const bounds = element.getBoundingClientRect();
      const width = Math.round(element.naturalWidth || bounds.width), height = Math.round(element.naturalHeight || bounds.height);
      if (width < 120 || height < 80 || width / height > 6 || height / width > 6) continue;
      const src = safeUrl(element.currentSrc || element.src);
      if (!src || seen.has(src)) continue;
      const alt = redact(element.getAttribute('alt') || element.getAttribute('aria-label') || element.getAttribute('title') || '', 200);
      if (/^(?:logo|icon|avatar|profile|spacer|pixel|광고|로고|아이콘|프로필)(?:\s|$)/i.test(alt)) continue;
      const related = relation(element, roots);
      if (!imageIds.has(element)) imageIds.set(element, 'i' + prefix + (++imageSerial));
      seen.add(src);
      candidates.push({ id: imageIds.get(element), src, alt, context: redact(related.group || related.context, 300),
        width, height, score: (inViewport(element) ? 1e9 : 0) + Math.min(width * height, 1e8) + (alt ? 1e7 : 0) });
    }
    candidates.sort((left, right) => right.score - left.score);
    return candidates.slice(0, 12).map(({ score, ...image }) => image);
  }
  function observe() {
    flushMutations();
    const before = previousRevision;
    previousRevision = revision;
    const resources = performance.getEntriesByType ? performance.getEntriesByType('resource') : [];
    const lastResource = resources.reduce((latest, item) => Math.max(latest,
      Number(item.responseEnd || item.duration || item.startTime || 0)), 0);
    const resourceQuietMs = Math.max(0, performance.now() - lastResource);
    const base = { url: location.href, documentId, revision,
      changes: { sinceRevision: before, domChanged: before !== revision },
      readiness: { readyState: document.readyState, domQuietMs: Math.max(0, Date.now() - lastMutation),
        resourceQuietMs, resourceCount: resources.length,
        pending: [...document.querySelectorAll('[aria-busy="true"],progress')].some(visible) },
      viewport: { x: Math.round(scrollX), y: Math.round(scrollY), width: innerWidth, height: innerHeight,
        scrollHeight: Math.max(document.documentElement.scrollHeight, document.body?.scrollHeight || 0) } };
    records = new Map();
    observedUrl = location.href;
    const reason = sensitiveReason();
    if (reason) return { ...base, title: '', text: '', html: '', headings: [], forms: [], elements: [], images: [],
      sensitive: true, screenshotSafe: false, localPreviewSafe: false, reason };
    const roots = accountRoots();
    const elements = [];
    const controls = controlCandidates();
    // Dialog/drawer controls must win the bounded action budget even when the site appends the
    // overlay after a large product grid in DOM order.
    controls.sort((left, right) => Number(!!interruptRoot(right)) - Number(!!interruptRoot(left))
      || Number(inViewport(right)) - Number(inViewport(left)));
    for (const element of controls) {
      if (elements.length >= 80) break;
      if (excluded(element, roots)) continue;
      const item = descriptor(element, roots);
      if (!item) continue;
      if (!ids.has(element)) ids.set(element, prefix + (++serial));
      item.id = ids.get(element);
      records.set(item.id, { element, fingerprint: fingerprint(element, roots, item), pageX: item.bounds.x + scrollX, pageY: item.bounds.y + scrollY });
      elements.push(item);
    }
    const interrupts = [];
    const seenInterrupts = new Set();
    for (const root of [...document.querySelectorAll(interruptSelector)]) {
      if (interrupts.length >= 6 || seenInterrupts.has(root) || !interruptSurface(root) || excluded(root, roots)) continue;
      // Frameworks commonly nest .modal-content inside .modal. Expose the innermost visible surface once.
      if ([...root.querySelectorAll(interruptSelector)].some(child => child !== root && interruptSurface(child))) continue;
      seenInterrupts.add(root);
      const style = getComputedStyle(root), bounds = root.getBoundingClientRect();
      const blocking = root.matches('dialog[open],[role="alertdialog"],[aria-modal="true"],' + authoredInterruptSelector)
        || style.position === 'fixed' || bounds.width * bounds.height >= innerWidth * innerHeight * 0.35;
      const actionIds = elements.filter(item => {
        const record = records.get(item.id);
        return record && root.contains(record.element) && ['button','link','checkbox','radio','combobox','textbox','option','menuitem'].includes(item.role);
      }).slice(0, 80).map(item => item.id);
      const dismissIds = elements.filter(item => item.dismiss && actionIds.includes(item.id)).map(item => item.id);
      const interrupt = { type: root.matches('[role="alertdialog"]') ? 'alertdialog' : 'dialog',
        label: redact(name(root), 200), text: textContent(root, roots, 900), blocking, actionIds, dismissIds };
      if (lastActivation && Date.now() - lastActivation.at <= 15000
        && (!lastActivation.root || lastActivation.root === root)
        && !lastActivation.existing.has(root)) {
        lastActivation.root = root;
        interrupt.triggerId = lastActivation.targetId;
        interrupt.triggerLabel = lastActivation.label;
      }
      interrupts.push(interrupt);
    }
    const main = [...document.querySelectorAll('main,[role="main"],article')].find(element => visible(element) && !excluded(element, roots)) || document.body || document.documentElement;
    const notices = [...document.querySelectorAll('[role="dialog"],[role="alert"],[role="status"]')]
      .filter(element => visible(element) && !excluded(element, roots) && !main.contains(element)).slice(0, 8)
      .map(element => textContent(element, roots, 600)).join(' ');
    const text = (notices + ' ' + textContent(main, roots, 10000)).trim().slice(0, 10000);
    const visibleSections = scrollY > innerHeight / 2 ? [...main.querySelectorAll('article,section,li,tr')]
      .filter(element => visible(element) && inViewport(element) && !excluded(element, roots)).slice(0, 5) : [];
    const html = visibleSections.length ? visibleSections.map(element => semanticHtml(element, roots, 1400)).join('') : semanticHtml(main, roots);
    const headings = [...document.querySelectorAll('h1,h2,h3,h4,h5,h6,[role="heading"]')]
      .filter(element => visible(element) && !excluded(element, roots)).slice(0, 30)
      .map(element => ({ level: Number(element.getAttribute('aria-level') || element.tagName.slice(1)) || 2, text: redact(labelText(element), 200) }));
    const forms = [...document.forms].filter(element => visible(element) && !excluded(element, roots)).slice(0, 10)
      .map(element => ({ label: redact(name(element), 200), method: clean(element.method, 10), action: safeUrl(element.action || location.href) }));
    const paymentRequired = controls.some(element => visible(element) && !excluded(element, roots) && finalControl(element));
    const personalFields = [];
    for (const field of [...document.querySelectorAll('input,textarea')].slice(0, 500)) {
      const kind = humanKind(field);
      if (kind && !field.value && !personalFields.some(item => item.kind === kind)) {
        personalFields.push({ kind, label: redact(name(field), 120) });
      }
      if (personalFields.length >= 6) break;
    }
    const pixelsSafe = screenshotSafe(roots);
    // Image URLs stay in the native process and image IDs/metadata are the only values exposed to
    // the planner. Account chrome, an iframe, or a filled ordinary field therefore must not hide
    // otherwise public product media from the user's local chat preview.
    const images = pageImages(main, roots);
    const result = { ...base, title: redact(document.title, 300), text, html, headings, forms, elements, interrupts,
      images, personalFields, paymentRequired, sensitive: false, screenshotSafe: pixelsSafe,
      localPreviewSafe: true };
    if (lastActivation && Date.now() - lastActivation.at <= 15000) {
      const newActionIds = elements.filter(item => {
        const record = records.get(item.id);
        return record && item.inViewport && !lastActivation.existingControls.has(record.element)
          && ['button','link','checkbox','radio','switch','combobox','option','menuitem'].includes(item.role);
      }).slice(0, 16).map(item => item.id);
      if (newActionIds.length) result.activation = { triggerId: lastActivation.targetId,
        triggerLabel: lastActivation.label, newActionIds };
      if (lastActivation.sourceInterrupt && !interruptSurface(lastActivation.sourceInterrupt)) {
        result.deactivation = { triggerId: lastActivation.targetId,
          triggerLabel: lastActivation.label, effect: 'interrupt_closed' };
      }
    }
    if (inspection?.isConnected && !excluded(inspection, roots) && visible(inspection)) {
      result.detail = { text: textContent(inspection, roots, 6000), html: semanticHtml(inspection, roots, 6000) };
    }
    inspection = null;
    return result;
  }
  function refuse(message, code = 'UNSUPPORTED', retryable = false) { return { ok: false, message, code, retryable }; }

  // Local human assistance only. These descriptors and answers must never enter model observations,
  // task memory or history. Native UI owns collecting the user's answer and invoking this helper.
  function humanKind(element) {
    if (!element.matches('input,textarea') || !visible(element) || disabled(element) || element.readOnly
      || element.getAttribute('aria-readonly') === 'true'
      || !['text', 'textarea', 'email', 'tel', 'number'].includes(element.type)) return '';
    const hints = [element.type, element.autocomplete, element.name, element.id, name(element),
      element.getAttribute('placeholder')].join(' ').normalize('NFKC').replace(/([a-z0-9])([A-Z])/g, '$1 $2');
    if (/(?:password|passwd|passcode|(?:^|[\s_-])pwd(?:$|[\s_-])|one[-_ ]?time|otp|captcha|verification|verify|(?:sms|email|phone|auth|security|access|confirmation|login)[-_ ]?(?:code|pin)|security[-_ ]?(?:answer|question)|cc[-_ ]|card|cvv|cvc|pin[-_ ]?code|(?:^|[\s_-])pin(?:$|[\s_-])|2fa|mfa|secret|token|api[-_ ]?key|bank|routing|iban|swift|account[-_ ]?number|(?:^|[\s_-])ssn(?:$|[\s_-])|passport|username|user[-_ ]?id|비밀번호|인증|보안|일회용|확인\s*(?:코드|번호)|카드|계좌|주민|여권|로그인\s*아이디)/i.test(hints)) return '';
    if (/(?:postal[-_ ]?code|post[-_ ]?code|zip[-_ ]?code|(?:^|[\s_-])zip(?:$|[\s_-])|우편\s*번호)/i.test(hints)) return 'postcode';
    if (element.type === 'email' || /(?:e[-_ ]?mail|이메일|전자\s*우편)/i.test(hints)) return 'email';
    if (element.type === 'tel' || /(?:phone|telephone|mobile|(?:^|[\s_-])tel(?:$|[\s_-])|전화|연락처|휴대폰|휴대전화)/i.test(hints)) return 'phone';
    if (/(?:address|street|city|province|country|state[-_ ]?name|주소|도로명|상세주소|시군구)/i.test(hints)) return 'address';
    if (/(?:recipient|receiver|consignee|수령인|받는\s*(?:분|사람)|수취인)/i.test(hints)) return 'recipient';
    if (/(?:given[-_ ]?name|family[-_ ]?name|first[-_ ]?name|last[-_ ]?name|full[-_ ]?name|(?:^|[\s_-])name(?:$|[\s_-])|성명|이름)/i.test(hints)) return 'name';
    return '';
  }
  function humanFingerprint(element) {
    // Current values are compared locally to avoid overwriting a user's intervening edit.
    // Neither this fingerprint nor any value is returned by humanFields/fillHumanFields.
    const form = element.form;
    return JSON.stringify([element.tagName, element.type, element.name, element.id, name(element),
      element.autocomplete, element.getAttribute('placeholder'), element.getAttribute('aria-label'),
      element.getAttribute('aria-labelledby'), element.getAttribute('aria-readonly'),
      element.maxLength, element.getAttribute('pattern'), element.disabled, element.readOnly, element.value,
      form?.id, form?.getAttribute('action'), form?.action, form?.getAttribute('method'), form?.getAttribute('target')]);
  }
  function humanFields() {
    humanRecords = new Map();
    humanObservedUrl = location.href;
    const base = { url: location.href, documentId, fields: [], blocked: true };
    const page = url(location.href);
    if (!page || page.protocol !== 'https:') return base;
    for (const element of [...document.querySelectorAll('input,textarea')].slice(0, 500)) {
      if (base.fields.length >= 8) break;
      const kind = humanKind(element);
      if (!kind) continue;
      if (!humanIds.has(element)) humanIds.set(element, 'h' + prefix + (++humanSerial));
      const id = humanIds.get(element);
      humanRecords.set(id, { element, kind, fingerprint: humanFingerprint(element), form: element.form });
      const label = redact(name(element), 120) || ({ name: '이름', recipient: '수령인', address: '주소', postcode: '우편번호', phone: '전화번호', email: '이메일' })[kind];
      base.fields.push({ id, label, inputType: element.type, kind });
    }
    base.blocked = base.fields.length === 0;
    return base;
  }
  function fillHumanFields(command, expectedUrl, expectedDocumentId) {
    const fail = (code, count = 0, total = 0) => ({ ok: false, filled: count, failed: total - count, code });
    if (typeof expectedUrl !== 'string' || !expectedUrl || location.href !== expectedUrl
      || humanObservedUrl !== expectedUrl || expectedDocumentId !== documentId) return fail('STALE_DOCUMENT');
    const page = url(location.href);
    if (!page || page.protocol !== 'https:') return fail('UNSUPPORTED');
    if (!command || typeof command !== 'object' || Array.isArray(command)
      || Object.keys(command).some(key => key !== 'values') || !Array.isArray(command.values)
      || command.values.length < 1 || command.values.length > 8) return fail('INVALID_FIELDS');
    const plans = [], seen = new Set();
    const stillCurrent = target => target.element.isConnected && target.element.ownerDocument === document
      && target.element.form === target.form && humanKind(target.element) === target.kind
      && humanFingerprint(target.element) === target.fingerprint;
    // Validate the whole batch before writing any field. After each site's event handler runs,
    // recheck the remaining targets to avoid following a stale answer into a replaced form.
    for (const field of command.values) {
      if (!field || typeof field !== 'object' || Array.isArray(field)
        || Object.keys(field).some(key => !['id', 'value'].includes(key))
        || typeof field.id !== 'string' || seen.has(field.id) || typeof field.value !== 'string') return fail('INVALID_FIELDS', 0, command.values.length);
      seen.add(field.id);
      const target = humanRecords.get(field.id);
      if (!target || !stillCurrent(target)) return fail('STALE_FIELD', 0, command.values.length);
      const limits = { name: 160, recipient: 160, address: 500, postcode: 32, phone: 40, email: 254 };
      const limit = target.element.hasAttribute('maxlength') && target.element.maxLength >= 0
        ? Math.min(limits[target.kind], target.element.maxLength) : limits[target.kind];
      if (field.value.length > limit || /[\u0000-\u0008\u000b\u000c\u000e-\u001f\u007f]/.test(field.value)
        || (target.element.tagName !== 'TEXTAREA' && /[\r\n]/.test(field.value))) return fail('INVALID_FIELDS', 0, command.values.length);
      plans.push({ target, value: field.value });
    }
    humanRecords.clear();
    let filled = 0;
    for (const plan of plans) {
      if (location.href !== expectedUrl || !stillCurrent(plan.target)) return fail('STALE_FIELD', filled, plans.length);
      const element = plan.target.element;
      const prototype = element.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
      const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set;
      if (!setter) return fail('UNSUPPORTED', filled, plans.length);
      localPrivateValues.add(plan.value);
      records.clear();
      inspection = null;
      try {
        setter.call(element, plan.value);
        element.dispatchEvent(new Event('input', { bubbles: true }));
        element.dispatchEvent(new Event('change', { bubbles: true }));
      } catch { return fail('NO_EFFECT', filled, plans.length); }
      if (!element.isConnected || element.value !== plan.value) return fail('NO_EFFECT', filled, plans.length);
      filled++;
    }
    return { ok: true, filled, failed: 0 };
  }

  // Device-vault assistance only. Field descriptors stay in the native process and secret values
  // travel directly from the encrypted vault/native secure dialog to the matching page controls.
  // Neither method is used by observe(), planner history, task memory or model requests.
  function secretKind(element) {
    if (!element.matches('input') || !visible(element) || disabled(element) || element.readOnly
      || element.getAttribute('aria-readonly') === 'true' || element.type === 'hidden') return '';
    const hints = [element.type, element.autocomplete, element.name, element.id, name(element),
      element.getAttribute('placeholder')].join(' ').normalize('NFKC').replace(/([a-z0-9])([A-Z])/g, '$1 $2');
    if (/(?:captcha|recaptcha|hcaptcha|turnstile|보안\s*문자|자동\s*입력\s*방지)/i.test(hints)) return '';
    if (element.type === 'password' || /(?:password|passwd|passcode|(?:^|[\s_-])pwd(?:$|[\s_-])|비밀번호)/i.test(hints)) return 'password';
    if (/(?:one[-_ ]?time[-_ ]?(?:code|password)|one-time-code|(?:^|[\s_-])otp(?:$|[\s_-])|(?:sms|email|phone|auth|verification|confirmation)[-_ ]?(?:code|pin)|인증\s*(?:번호|코드)|일회용)/i.test(hints)) return 'otp';
    if (/(?:cc[-_ ]?number|card[-_ ]?number|credit[-_ ]?card|카드\s*번호)/i.test(hints)) return 'card_number';
    if (/(?:cc[-_ ]?exp[-_ ]?month|card[-_ ]?(?:exp|expiry|expiration)[-_ ]?month|유효\s*기간\s*월)/i.test(hints)) return 'card_exp_month';
    if (/(?:cc[-_ ]?exp[-_ ]?year|card[-_ ]?(?:exp|expiry|expiration)[-_ ]?year|유효\s*기간\s*년)/i.test(hints)) return 'card_exp_year';
    if (/(?:cc[-_ ]?exp|card[-_ ]?(?:exp|expiry|expiration)|유효\s*기간)/i.test(hints)) return 'card_expiry';
    if (/(?:cc[-_ ]?(?:csc|cvc)|cvv|cvc|card[-_ ]?(?:security|verification)[-_ ]?code|보안\s*코드)/i.test(hints)) return 'card_cvc';
    return '';
  }
  function secretFields() {
    secretRecords = new Map();
    secretObservedUrl = location.href;
    const base = { url: location.href, documentId, fields: [], blocked: true };
    const page = url(location.href);
    if (!page || page.protocol !== 'https:') return base;
    for (const element of [...document.querySelectorAll('input')].slice(0, 500)) {
      if (base.fields.length >= 8) break;
      const kind = secretKind(element);
      if (!kind) continue;
      if (!secretIds.has(element)) secretIds.set(element, 's' + prefix + (++secretSerial));
      const id = secretIds.get(element);
      secretRecords.set(id, { element, kind, fingerprint: humanFingerprint(element), form: element.form });
      const fallback = { password: '비밀번호', otp: '인증번호', card_number: '카드번호',
        card_expiry: '카드 유효기간', card_exp_month: '카드 유효기간 월',
        card_exp_year: '카드 유효기간 연도', card_cvc: '카드 보안코드' }[kind];
      base.fields.push({ id, label: redact(name(element), 120) || fallback, inputType: element.type, kind });
    }
    base.blocked = base.fields.length === 0;
    return base;
  }
  function validSecretValue(kind, value) {
    if (typeof value !== 'string' || !value || value.length > 512
      || /[\u0000-\u001f\u007f]/.test(value)) return false;
    if (kind === 'password') return value.length >= 4;
    const compact = value.replace(/[ -]/g, '');
    if (kind === 'otp') return /^[0-9]{4,12}$/.test(compact);
    if (kind === 'card_number') {
      if (!/^[0-9]{13,19}$/.test(compact)) return false;
      let sum = 0, twice = false;
      for (let i = compact.length - 1; i >= 0; i--) {
        let digit = Number(compact[i]);
        if (twice && (digit *= 2) > 9) digit -= 9;
        sum += digit; twice = !twice;
      }
      return sum % 10 === 0;
    }
    if (kind === 'card_expiry') return /^(?:0[1-9]|1[0-2])(?:[/ -]?)(?:[0-9]{2}|20[0-9]{2})$/.test(value.trim());
    if (kind === 'card_exp_month') return /^(?:0?[1-9]|1[0-2])$/.test(value.trim());
    if (kind === 'card_exp_year') return /^(?:[0-9]{2}|20[0-9]{2})$/.test(value.trim());
    return kind === 'card_cvc' && /^[0-9]{3,4}$/.test(value.trim());
  }
  function fillSecretFields(command, expectedUrl, expectedDocumentId) {
    const fail = (code, count = 0, total = 0) => ({ ok: false, filled: count, failed: total - count, code });
    if (typeof expectedUrl !== 'string' || !expectedUrl || location.href !== expectedUrl
      || secretObservedUrl !== expectedUrl || expectedDocumentId !== documentId) return fail('STALE_DOCUMENT');
    const page = url(location.href);
    if (!page || page.protocol !== 'https:') return fail('UNSUPPORTED');
    if (!command || typeof command !== 'object' || Array.isArray(command)
      || Object.keys(command).some(key => key !== 'values') || !Array.isArray(command.values)
      || command.values.length < 1 || command.values.length > 8) return fail('INVALID_FIELDS');
    const plans = [], seen = new Set();
    const stillCurrent = target => target.element.isConnected && target.element.ownerDocument === document
      && target.element.form === target.form && secretKind(target.element) === target.kind
      && humanFingerprint(target.element) === target.fingerprint;
    for (const field of command.values) {
      if (!field || typeof field !== 'object' || Array.isArray(field)
        || Object.keys(field).some(key => !['id', 'value'].includes(key))
        || typeof field.id !== 'string' || seen.has(field.id) || typeof field.value !== 'string') return fail('INVALID_FIELDS', 0, command.values.length);
      seen.add(field.id);
      const target = secretRecords.get(field.id);
      if (!target || !stillCurrent(target)) return fail('STALE_FIELD', 0, command.values.length);
      const elementLimit = target.element.hasAttribute('maxlength') && target.element.maxLength >= 0
        ? Math.min(512, target.element.maxLength) : 512;
      if (field.value.length > elementLimit || !validSecretValue(target.kind, field.value)) return fail('INVALID_FIELDS', 0, command.values.length);
      plans.push({ target, value: field.value });
    }
    secretRecords.clear();
    let filled = 0;
    for (const plan of plans) {
      if (location.href !== expectedUrl || !stillCurrent(plan.target)) return fail('STALE_FIELD', filled, plans.length);
      const element = plan.target.element;
      const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
      if (!setter) return fail('UNSUPPORTED', filled, plans.length);
      localPrivateValues.add(plan.value);
      records.clear();
      inspection = null;
      try {
        setter.call(element, plan.value);
        element.dispatchEvent(new Event('input', { bubbles: true }));
        element.dispatchEvent(new Event('change', { bubbles: true }));
      } catch { return fail('NO_EFFECT', filled, plans.length); }
      if (!element.isConnected || element.value !== plan.value) return fail('NO_EFFECT', filled, plans.length);
      filled++;
    }
    return { ok: true, filled, failed: 0 };
  }
  function preview(command, expectedUrl, expectedDocumentId) {
    if (actionPreview?.isConnected) actionPreview.remove();
    actionPreview = null;
    if (!command || typeof command !== 'object' || Array.isArray(command)
      || typeof expectedUrl !== 'string' || location.href !== expectedUrl || observedUrl !== expectedUrl
      || expectedDocumentId && expectedDocumentId !== documentId) return refuse('페이지가 변경되었습니다.', 'STALE_DOCUMENT', true);
    if (!command.targetId) return { ok: true, highlighted: false };
    const target = records.get(command.targetId);
    if (!target || !target.element.isConnected || target.element.ownerDocument !== document
      || !visible(target.element) || target.fingerprint !== fingerprint(target.element)
      || excluded(target.element, accountRoots())) return refuse('대상이 변경되었습니다.', 'STALE_TARGET', true);
    const element = target.element;
    if (!inViewport(element)) element.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' });
    const bounds = element.getBoundingClientRect();
    const centerX = (Math.max(0, bounds.left) + Math.min(innerWidth, bounds.right)) / 2;
    const centerY = (Math.max(0, bounds.top) + Math.min(innerHeight, bounds.bottom)) / 2;
    const hit = typeof document.elementFromPoint === 'function' ? document.elementFromPoint(centerX, centerY) : element;
    if (!Number.isFinite(centerX) || !Number.isFinite(centerY) || bounds.width <= 0 || bounds.height <= 0
      || !hit || !(hit === element || element.contains(hit))) {
      return refuse('다른 창이 대상을 가리고 있습니다.', 'OBSCURED', true);
    }
    if (command.type === 'click') {
      const item = descriptor(element);
      if (!item || finalControl(element)) return refuse('이 항목은 직접 조작해 주세요.');
      // A native Android tap does not enter action(), so capture the same pre-activation state
      // here. The next observation can then attribute a new dialog/drawer to this exact target.
      lastActivation = { targetId: command.targetId, label: redact(item.label, 200), at: Date.now(),
        existing: new Set([...document.querySelectorAll(interruptSelector)].filter(interruptSurface)),
        existingControls: new Set(controlCandidates(false).filter(visible)), root: null,
        sourceInterrupt: interruptRoot(element) };
    }
    const marker = document.createElement('div');
    marker.setAttribute('aria-hidden', 'true');
    marker.style.cssText = `position:fixed;pointer-events:none;z-index:2147483647;left:${Math.max(0, bounds.left - 4)}px;top:${Math.max(0, bounds.top - 4)}px;width:${Math.max(8, bounds.width + 8)}px;height:${Math.max(8, bounds.height + 8)}px;border:3px solid #405de6;border-radius:10px;box-sizing:border-box;box-shadow:0 0 0 4px rgba(64,93,230,.18);`;
    (document.documentElement || document.body).append(marker);
    actionPreview = marker;
    setTimeout(() => { if (marker.isConnected) marker.remove(); if (actionPreview === marker) actionPreview = null; }, 1400);
    // Coordinates stay inside the native preview callback. They are never included in model-visible
    // observations and let Android perform one verified touch fallback on the same current target.
    return { ok: true, highlighted: true, centerX, centerY,
      viewportWidth: innerWidth, viewportHeight: innerHeight };
  }
  function action(command, expectedUrl, expectedDocumentId) {
    if (!command || typeof command !== 'object' || Array.isArray(command)) return refuse('잘못된 동작입니다.');
    if (typeof expectedUrl !== 'string' || location.href !== expectedUrl || observedUrl !== expectedUrl) {
      return refuse('페이지 주소가 변경되었습니다. 다시 확인해 주세요.', 'STALE_DOCUMENT', true);
    }
    if (expectedDocumentId && expectedDocumentId !== documentId) return refuse('새 문서가 열렸습니다. 다시 확인합니다.', 'STALE_DOCUMENT', true);
    const reason = sensitiveReason();
    if (reason) { records.clear(); return refuse(reason); }
    if (command.type !== 'click') lastActivation = null;
    if (command.type === 'inspect' && !command.targetId) return { ok: true, effect: 'observed' };
    if (command.type === 'scroll' && !command.targetId) {
      if (!['up', 'down'].includes(command.direction)) return refuse('스크롤 방향을 확인하세요.');
      const before = scrollY;
      records.clear();
      window.scrollBy({ top: (command.direction === 'down' ? 1 : -1) * Math.max(200, innerHeight * 0.7), behavior: 'instant' });
      return { ok: true, effect: 'scrolled', changed: scrollY !== before };
    }
    const target = records.get(command.targetId);
    if (!target || !target.element.isConnected || target.element.ownerDocument !== document
      || !visible(target.element) || target.fingerprint !== fingerprint(target.element)
      || excluded(target.element, accountRoots())) return refuse('대상이 변경되었습니다. 페이지를 다시 확인해 주세요.', 'STALE_TARGET', true);
    const element = target.element;
    const item = descriptor(element);
    if (!item || finalControl(element)) return refuse('이 항목은 직접 조작해 주세요.');
    if (command.type === 'inspect') { inspection = element; return { ok: true, effect: 'observed' }; }
    if (command.type === 'scroll') {
      if (!item.scrollable || !['up', 'down'].includes(command.direction)) return refuse('스크롤 가능한 영역이 아닙니다.');
      const before = element.scrollTop;
      element.scrollTop += (command.direction === 'down' ? 1 : -1) * Math.max(80, element.clientHeight * 0.7);
      records.clear();
      return { ok: true, effect: 'scrolled', changed: before !== element.scrollTop };
    }
    if (disabled(element)) return refuse('아직 활성화되지 않은 항목입니다.', 'NOT_READY', true);
    const currentBounds = element.getBoundingClientRect();
    if (Math.abs(currentBounds.x + scrollX - target.pageX) > 4 || Math.abs(currentBounds.y + scrollY - target.pageY) > 4) {
      return refuse('대상 위치가 바뀌었습니다. 다시 확인합니다.', 'NOT_READY', true);
    }
    // Scroll and check the actual hit target before invoking a click. Overlayed targets
    // must be observed again; calling HTMLElement.click() alone would bypass overlays.
    if (['click', 'check'].includes(command.type)) {
      if (!inViewport(element)) element.scrollIntoView({ block: 'center', inline: 'nearest', behavior: 'instant' });
      const r = element.getBoundingClientRect();
      const x = (Math.max(0, r.left) + Math.min(innerWidth, r.right)) / 2;
      const y = (Math.max(0, r.top) + Math.min(innerHeight, r.bottom)) / 2;
      const hit = typeof document.elementFromPoint === 'function' ? document.elementFromPoint(x, y) : element;
      if (!hit || !(hit === element || element.contains(hit))) return refuse('다른 창이 대상을 가리고 있습니다.', 'OBSCURED', true);
    }
    if (command.type === 'select') {
      if (element.tagName !== 'SELECT' || element.multiple || typeof command.value !== 'string') return refuse('단일 선택 항목을 확인하세요.');
      if (command.approved !== true) return refuse('검증된 네이티브 동작만 선택할 수 있습니다.', 'APPROVAL_REQUIRED');
      const options = [...element.options].filter(option => !disabled(option)
        && (option.value === command.value || clean(option.label || option.textContent, 200) === command.value));
      if (options.length !== 1 || !publicText(command.value, 200)) return refuse('선택지를 다시 확인해 주세요.', 'STALE_TARGET', true);
      const before = element.value;
      Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value').set.call(element, options[0].value);
      element.dispatchEvent(new Event('input', { bubbles: true }));
      element.dispatchEvent(new Event('change', { bubbles: true }));
      records.clear();
      if (element.value !== options[0].value) return refuse('사이트가 선택을 반영하지 않았습니다.', 'NO_EFFECT', true);
      return { ok: true, effect: 'selected', changed: element.value !== before, verified: true };
    }
    if (command.type === 'check') {
      const choice = labelChoice(element);
      const checkTarget = choice || element;
      if (!checkTarget.matches('input[type="checkbox"],input[type="radio"],[role="checkbox"],[role="switch"],[role="radio"]')
        || typeof command.checked !== 'boolean') return refuse('선택 상태를 확인하세요.');
      if (command.approved !== true) return refuse('검증된 네이티브 동작만 선택할 수 있습니다.', 'APPROVAL_REQUIRED');
      if ((checkTarget.type === 'radio' || item.role === 'radio') && !command.checked) return refuse('라디오는 다른 선택지를 선택해 주세요.');
      if (item.checked === command.checked) return { ok: true, effect: 'checked', changed: false, verified: true };
      element.click();
      records.clear();
      const checked = checkTarget.matches('input') ? checkTarget.checked : checkTarget.getAttribute('aria-checked') === 'true';
      if (checked !== command.checked) return refuse('사이트가 선택 상태를 반영하지 않았습니다.', 'NO_EFFECT', true);
      return { ok: true, effect: 'checked', changed: checked !== item.checked, verified: true };
    }
    if (command.type === 'type') {
      if (!editableField(element)) return refuse('개인정보가 없는 공개 입력란만 입력할 수 있습니다.');
      if (!item.search && command.approved !== true) return refuse('검증된 네이티브 동작만 입력할 수 있습니다.', 'APPROVAL_REQUIRED');
      if (!publicText(command.text, item.search ? 300 : 2000)) return refuse('개인정보가 없는 텍스트만 입력해 주세요.');
      if (command.submit !== undefined && typeof command.submit !== 'boolean') return refuse('잘못된 제출 설정입니다.');
      if (command.submit === true && (!item.search || !searchForm(element))) return refuse('일반 입력 후에는 제출 버튼을 별도로 확인해 주세요.');
      records.clear();
      const prototype = element.tagName === 'TEXTAREA' ? HTMLTextAreaElement.prototype : HTMLInputElement.prototype;
      const setter = Object.getOwnPropertyDescriptor(prototype, 'value')?.set;
      if (!setter) return refuse('검색창을 입력할 수 없습니다.');
      const before = element.value;
      setter.call(element, command.text);
      authoredValues.set(element, command.text);
      element.dispatchEvent(new Event('input', { bubbles: true }));
      element.dispatchEvent(new Event('change', { bubbles: true }));
      if (command.submit === true) {
        if (location.href !== expectedUrl) return { ok: true, navigating: true };
        if (!element.isConnected || sensitiveReason() || !searchForm(element)
          || element.value !== command.text) return refuse('검색창이 변경되었습니다. 직접 확인해 주세요.');
        if (element.form) HTMLFormElement.prototype.requestSubmit.call(element.form);
        else {
          element.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', code: 'Enter', keyCode: 13, which: 13, bubbles: true, cancelable: true }));
          element.dispatchEvent(new KeyboardEvent('keyup', { key: 'Enter', code: 'Enter', keyCode: 13, which: 13, bubbles: true }));
        }
        return { ok: true, navigating: true };
      }
      if (element.value !== command.text) return refuse('사이트가 입력을 반영하지 않았습니다.', 'NO_EFFECT', true);
      return { ok: true, effect: 'typed', changed: before !== element.value, verified: true };
    }
    if (command.type !== 'click') return refuse('지원하지 않는 동작입니다.');
    const searchField = searchFieldForButton(element);
    if ((!item.href && !searchField || item.href && sideEffectLink(element))
      && command.approved !== true) return refuse('검증된 네이티브 동작만 실행할 수 있습니다.');
    // Capture the visible dialog roots immediately before dispatch. A dialog that becomes visible
    // after this exact activation is an observable click postcondition even when the URL and the
    // opener's aria state do not change (common for size guides and product quick-view sheets).
    lastActivation = { targetId: command.targetId, label: redact(item.label, 200), at: Date.now(),
      existing: new Set([...document.querySelectorAll(interruptSelector)].filter(interruptSurface)),
      existingControls: new Set(controlCandidates(false).filter(visible)), root: null,
      sourceInterrupt: interruptRoot(element) };
    records.clear();
    if (item.href) {
      if (!url(element.href)) return refuse('이 링크는 열 수 없습니다.');
      const target = element.getAttribute('target');
      if (target?.toLowerCase() !== '_self') element.setAttribute('target', '_self');
      let dispatchedEvent = null;
      const rememberEvent = event => { dispatchedEvent = event; };
      element.addEventListener('click', rememberEvent, { capture: true, once: true });
      try { element.click(); } finally {
        element.removeEventListener('click', rememberEvent, true);
        if (target === null) element.removeAttribute('target');
        else if (target.toLowerCase() !== '_self') element.setAttribute('target', target);
      }
      // Algolia and similar facet links keep an href for routing/accessibility but prevent the
      // default navigation and update results in-place. Preserve the dispatched Event reference
      // until propagation completes so delegated preventDefault() handlers are also observed.
      return { ok: true, navigating: !(dispatchedEvent && dispatchedEvent.defaultPrevented),
        effect: dispatchedEvent && dispatchedEvent.defaultPrevented ? 'activated' : 'navigation_requested' };
    }
    if (searchField) {
      if (!searchForm(searchField, element)) return refuse('검색 양식이 변경되었습니다.');
      HTMLFormElement.prototype.requestSubmit.call(element.form, element);
    } else element.click();
    return { ok: true, navigating: !!searchField, effect: searchField ? 'navigation_requested' : 'activated' };
  }
  function manualState() {
    const secretRequired = [...document.querySelectorAll('input,textarea')].some(element => {
      if (!visible(element) || element.type === 'hidden') return false;
      const hints = [element.type, element.autocomplete, element.name, element.id, name(element)].join(' ');
      return element.type === 'password' || /(?:one-time-code|(?:^|[\s_-])otp(?:$|[\s_-])|captcha|verification|cc-number|cc-csc|cc-exp|card[-_ ]?number|cvv|cvc|비밀번호|인증번호|카드\s*번호|보안\s*코드)/i.test(hints);
    });
    return { url: location.href, documentId, secretRequired,
      ready: document.readyState !== 'loading' && ![...document.querySelectorAll('[aria-busy="true"],progress')].some(visible) };
  }
  const api = Object.freeze({ observe, preview, action, humanFields, fillHumanFields, secretFields, fillSecretFields, manualState });
  Object.defineProperty(window, key, { value: api, configurable: false, writable: false, enumerable: false });
  return api;
})()
