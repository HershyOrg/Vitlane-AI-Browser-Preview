(() => {
  'use strict';

  if (globalThis.__vitlaneAgent?.version === 1) return;

  const state = {
    generation: 0,
    observationId: null,
    candidates: new Map(),
    recipeSurface: null,
    recipeFingerprint: null,
    used: false,
  };

  const MAX_TEXT = 180;
  const FINAL_ACTION = /(?:add\s*to\s*(?:cart|bag|basket)|buy\s*now|checkout|place\s*order|purchase|pay(?:ment)?|book\s*now|reserve|subscribe|cancel\s*(?:order|booking|subscription)|refund|결제|구매|장바구니\s*담기|주문\s*확정|예약\s*확정|구독|환불|취소\s*확정)/i;
  const UNSUPPORTED_LINK = /(?:account|profile|orders?|settings|wallet|login|sign\s*in|auth|wishlist|remove|delete|logout|log\s*out|sign\s*out|unsubscribe|deactivate|follow|like|vote|redeem|claim|계정|프로필|주문\s*내역|설정|지갑|로그인|로그아웃|탈퇴|삭제|좋아요|팔로우)/i;
  const SENSITIVE_AUTOCOMPLETE = /(?:password|one-time-code|cc-|transaction-|webauthn)/i;
  const SENSITIVE_NAME = /(?:passw|passwd|otp|one.?time|verification|card|cvc|cvv|security.?code|resident|ssn|주민|비밀번호|인증번호|카드)/i;
  const COUPANG_PRODUCT_PATH = /^\/vp\/products\/([1-9][0-9]*)\/?$/;
  const COUPANG_PRODUCT_ORIGINS = new Set(['https://coupang.com', 'https://www.coupang.com']);
  const COUPANG_CART_PATHS = new Set([
    'https://cart.coupang.com/cartView.pang',
  ]);
  const COUPANG_STEPS = new Set([
    'select_option',
    'set_quantity',
    'verify_options',
    'add_to_cart',
    'buy_now',
    'start_checkout',
  ]);
  const FINAL_COMMITMENT = /^(?:결제하기|주문하기|주문\s*확정|결제\s*및\s*주문|구매\s*확정)$/;

  function randomId(prefix) {
    if (globalThis.crypto?.randomUUID) return `${prefix}_${globalThis.crypto.randomUUID()}`;
    const bytes = new Uint32Array(4);
    globalThis.crypto?.getRandomValues?.(bytes);
    return `${prefix}_${Array.from(bytes).map(value => value.toString(16)).join('')}_${Date.now()}`;
  }

  function normalize(value) {
    return String(value ?? '').replace(/\s+/g, ' ').trim();
  }

  function redact(value) {
    let text = normalize(value).slice(0, MAX_TEXT);
    text = text.replace(/[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}/gi, '[redacted]');
    text = text.replace(/(?:\+?\d[\d .-]{7,}\d)/g, '[redacted]');
    text = text.replace(/(?:\d[ -]*?){13,19}/g, '[redacted]');
    text = text.replace(/(?:otp|verification\s*code|one[- ]?time\s*code|인증번호|일회용\s*코드)\s*[:#=-]?\s*\d{4,8}/gi, '[redacted]');
    text = text.replace(/(?:api[_ -]?key|access[_ -]?token|secret|password|비밀번호)\s*[:=]\s*\S+/gi, '[redacted]');
    return text;
  }

  function fingerprint(parts) {
    const input = parts.join('\u001f');
    let hash = 2166136261;
    for (let index = 0; index < input.length; index += 1) {
      hash ^= input.charCodeAt(index);
      hash = Math.imul(hash, 16777619);
    }
    return `fp_${(hash >>> 0).toString(16).padStart(8, '0')}`;
  }

  function roleFor(element) {
    const explicit = normalize(element.getAttribute('role'));
    if (explicit) return explicit;
    const tag = element.tagName.toLowerCase();
    if (tag === 'a') return 'link';
    if (tag === 'button') return 'button';
    if (tag === 'select') return 'combobox';
    if (tag === 'input') return 'textbox';
    if (/^h[1-6]$/.test(tag)) return 'heading';
    return 'generic';
  }

  function labelFor(element) {
    const labelledBy = normalize(element.getAttribute('aria-labelledby'));
    if (labelledBy) {
      const labels = labelledBy
        .split(/\s+/)
        .map(id => document.getElementById(id))
        .filter(Boolean)
        .map(node => node.textContent);
      const joined = redact(labels.join(' '));
      if (joined) return joined;
    }
    const aria = redact(element.getAttribute('aria-label'));
    if (aria) return aria;
    if (element.labels?.length) {
      const labels = redact(Array.from(element.labels).map(node => node.textContent).join(' '));
      if (labels) return labels;
    }
    const placeholder = redact(element.getAttribute('placeholder'));
    if (placeholder) return placeholder;
    return redact(element.innerText || element.textContent);
  }

  function isVisible(element) {
    const style = getComputedStyle(element);
    if (style.display === 'none' || style.visibility === 'hidden' || (style.opacity !== '' && Number(style.opacity) === 0)) return false;
    const rect = element.getBoundingClientRect();
    return rect.width > 0 && rect.height > 0;
  }

  function exactText(element) {
    return normalize(labelFor(element) || element.innerText || element.textContent).normalize('NFKC');
  }

  function isEnabled(element) {
    return !element.disabled && element.getAttribute('aria-disabled') !== 'true';
  }

  function coupangSurface() {
    if (COUPANG_PRODUCT_ORIGINS.has(location.origin) && COUPANG_PRODUCT_PATH.test(location.pathname)) {
      return 'coupang_product';
    }
    const cartURL = `${location.origin}${location.pathname || '/'}`;
    if (COUPANG_CART_PATHS.has(cartURL)) return 'coupang_cart';
    return null;
  }

  function visibleSemanticElements() {
    return Array.from(document.querySelectorAll(
      'button,a[href],input,select,option,[role="button"],[role="radio"],[role="option"],[role="heading"],h1,h2,h3',
    )).filter(isVisible);
  }

  function isCoupangCartReview() {
    if (coupangSurface() !== 'coupang_cart') return false;
    if (document.querySelector('input[type="password"],input[autocomplete="one-time-code"],input[autocomplete^="cc-"]')) {
      return false;
    }
    const headings = visibleSemanticElements()
      .filter(element => roleFor(element) === 'heading')
      .map(exactText);
    if (headings.some(name => /^(?:주문\/결제|주문서|결제)$/.test(name))) return false;
    const pageText = normalize(`${document.title} ${headings.join(' ')}`).normalize('NFKC');
    return /(?:^|\s)장바구니(?:\s|$)/.test(pageText) || /장바구니/.test(pageText);
  }

  function coupangRecipeFingerprint(surface) {
    const identities = visibleSemanticElements().slice(0, 500).map(element => [
      element.tagName.toLowerCase(),
      roleFor(element),
      exactText(element),
      normalize(element.getAttribute('name')),
      normalize(element.getAttribute('value')),
      normalize(element.getAttribute('href')),
      normalize(element.getAttribute('aria-checked')),
      element.disabled ? 'disabled' : 'enabled',
    ].join('\u001e'));
    return fingerprint([surface, location.origin, location.pathname, location.search, ...identities]);
  }

  function isSensitiveControl(element) {
    if (!(element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement)) return false;
    const type = normalize(element.getAttribute('type')).toLowerCase();
    const autocomplete = normalize(element.getAttribute('autocomplete'));
    const identity = [
      element.getAttribute('name'),
      element.getAttribute('id'),
      element.getAttribute('placeholder'),
      element.getAttribute('aria-label'),
    ].map(normalize).join(' ');
    return type === 'password' || SENSITIVE_AUTOCOMPLETE.test(autocomplete) || SENSITIVE_NAME.test(identity);
  }

  function sensitivePageReason() {
    const surface = coupangSurface();
    if (surface === 'coupang_product') return null;
    if (surface === 'coupang_cart' && isCoupangCartReview()) return null;
    if (location.origin === 'https://checkout.coupang.com') return 'payment';
    if (surface === 'coupang_cart') return 'sensitive_page';
    const path = location.pathname.toLowerCase();
    if (/(?:login|signin|sign-in|auth|password)/.test(path)) return 'login';
    if (/(?:otp|captcha|challenge|verify|verification)/.test(path)) return 'verification';
    if (/(?:checkout|payment|billing|card|confirm-(?:order|booking)|order-confirm)/.test(path)) return 'payment';
    if (/(?:^|\/)(?:cart|basket|bag)(?:\/|$)/.test(path)) return 'sensitive_page';
    if (/(?:account|profile|orders?|purchases?|addresses|wallet|subscriptions?|session)/.test(path)) return 'sensitive_page';
    if (document.querySelector('input[type="password"]')) return 'login';
    if (document.querySelector('input[autocomplete="one-time-code"]')) return 'verification';
    if (document.querySelector('input[autocomplete^="cc-"]')) return 'payment';
    const headings = Array.from(document.querySelectorAll('h1,[role="heading"][aria-level="1"]'))
      .filter(isVisible).slice(0, 4).map(labelFor).join(' ');
    const privateHeading = /(?:my\s+account|account|profile|orders?|order\s+history|shopping\s+cart|your\s+cart|wallet|내\s*계정|마이페이지|프로필|주문\s*내역|장바구니)/i.test(`${document.title} ${headings}`);
    if (privateHeading) return 'authenticated';
    return null;
  }

  function authenticatedChromeRoots() {
    const roots = [];
    for (const element of document.querySelectorAll('a[href],button,[role="button"]')) {
      if (!isVisible(element) || !/(?:log\s*out|sign\s*out|로그아웃|(?:^|\/)logout(?:\/|$))/i.test(
        `${labelFor(element)} ${element.getAttribute('href') || ''}`
      )) continue;
      const container = element.closest(
        'header,nav,[role="banner"],[role="navigation"],[class*="header" i],[class*="account" i],[class*="profile" i]',
      );
      roots.push(container && container !== document.body && container !== document.documentElement
        ? container
        : element);
    }
    return [...new Set(roots)];
  }

  function sessionStateHint(authenticatedRoots) {
    // This is a privacy-safe, untrusted page hint. Never expose the account
    // label itself; only report that visible logout chrome was excluded.
    if (authenticatedRoots.length > 0) return 'authenticated';
    const signInControl = Array.from(document.querySelectorAll('a[href],button,[role="button"]'))
      .some(element => isVisible(element) && /(?:sign\s*in|log\s*in|로그인|(?:^|\/)(?:login|signin|sign-in)(?:\/|$))/i.test(
        `${labelFor(element)} ${element.getAttribute('href') || ''}`
      ));
    return signInControl ? 'anonymous' : 'unknown';
  }

  function hasUserInputValue(element) {
    if (!(element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement)) return false;
    const type = normalize(element.getAttribute('type')).toLowerCase();
    if (['button', 'submit', 'reset', 'checkbox', 'radio', 'image', 'range', 'color', 'hidden'].includes(type)) return false;
    return element.value !== '';
  }

  function likelySensitiveQuery(value) {
    const text = String(value ?? '').normalize('NFKC');
    if (/\b(?:password|passwd|passcode|otp|one[- ]?time|cvv|cvc|card\s*number|access[_ -]?token|api[_ -]?key|secret)\b/i.test(text)) return true;
    if (/(?:비밀번호|인증번호|일회용\s*코드|카드\s*번호|보안\s*코드|주민등록)/.test(text)) return true;
    if (/\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/i.test(text) || /https?:\/\//i.test(text)) return true;
    return text.replace(/[^0-9]/g, '').length >= 11;
  }

  function publicSameOriginHref(element) {
    if (!(element instanceof HTMLAnchorElement)) return null;
    if (element.hasAttribute('download') || element.hasAttribute('onclick')) return null;
    try {
      const url = new URL(element.href, location.href);
      if (!['https:', 'http:'].includes(url.protocol) || url.origin !== location.origin) return null;
      if (url.username || url.password || url.port || url.search || url.hash) return null;
      return url.href;
    } catch {
      return null;
    }
  }

  function isPublicSearch(element) {
    if (!(element instanceof HTMLInputElement)) return false;
    const role = normalize(element.getAttribute('role')).toLowerCase();
    const type = normalize(element.getAttribute('type') || 'text').toLowerCase();
    const identity = [
      element.getAttribute('name'),
      element.getAttribute('id'),
      element.getAttribute('placeholder'),
      element.getAttribute('aria-label'),
    ].map(normalize).join(' ');
    if (isSensitiveControl(element) || element.disabled || element.readOnly || element.value !== '') return false;
    if (!(role === 'searchbox' || type === 'search')) return false;
    const form = element.form;
    if (!form || normalize(form.method || 'get').toLowerCase() !== 'get') return false;
    try {
      if (new URL(form.action || location.href, location.href).origin !== location.origin) return false;
    } catch {
      return false;
    }
    const editable = Array.from(form.querySelectorAll('input, textarea, select')).filter(item =>
      !['hidden', 'submit', 'button', 'reset', 'image'].includes(normalize(item.getAttribute('type')).toLowerCase())
    );
    return editable.length === 1 && editable[0] === element && !/(?:password|otp|card|비밀번호|인증번호|카드)/i.test(identity);
  }

  function pageTypeHint() {
    const hints = `${location.pathname} ${document.title}`.toLowerCase();
    if (/(?:search|검색)/.test(hints)) return 'search';
    if (/(?:product|item|상품)/.test(hints)) return 'product';
    if (/(?:catalog|category|listing|목록)/.test(hints)) return 'listing';
    return 'unknown';
  }

  function hasFinalMeaning(element) {
    const meaning = [
      element.innerText,
      element.textContent,
      element.getAttribute('aria-label'),
      element.getAttribute('title'),
      element.getAttribute('href'),
      element.closest('form')?.getAttribute('action'),
    ].map(normalize).join(' ');
    return FINAL_ACTION.test(meaning);
  }

  function observe(expectedGeneration) {
    if (expectedGeneration !== state.generation) throw new Error('generation_mismatch');
    const reason = sensitivePageReason();
    state.observationId = randomId('obs');
    state.candidates = new Map();
    state.recipeSurface = null;
    state.recipeFingerprint = null;
    state.used = false;

    if (reason) {
      return {
        observationId: state.observationId,
        title: '',
        sensitivePage: true,
        blockedReason: reason,
        candidates: [],
        visibleText: '',
        pageTypeHint: 'unknown',
        sessionStateHint: 'unknown',
        excludedBoundaryCodes: [],
      };
    }

    const recipeSurface = coupangSurface();
    if (recipeSurface === 'coupang_cart') {
      if (!isCoupangCartReview()) return blockedObservation('payment');
      state.recipeSurface = recipeSurface;
      state.recipeFingerprint = coupangRecipeFingerprint(recipeSurface);
      return {
        observationId: state.observationId,
        title: '',
        sensitivePage: false,
        blockedReason: null,
        candidates: [],
        visibleText: '',
        pageTypeHint: 'unknown',
        sessionStateHint: 'unknown',
        recipeSurface,
        excludedBoundaryCodes: ['PAYMENT_OR_COMMITMENT'],
      };
    }

    const excludedBoundaryCodes = new Set();
    const excludedRoots = [];
    const authenticatedRoots = authenticatedChromeRoots();
    const observedSessionState = sessionStateHint(authenticatedRoots);
    for (const root of authenticatedRoots) {
      excludedBoundaryCodes.add('UNSUPPORTED_INTERACTION');
      excludedRoots.push(root);
    }
    for (const element of document.querySelectorAll('input, textarea, select, button, [role="button"], form, iframe, [contenteditable], [role="textbox"]')) {
      if (!isVisible(element)) continue;
      if (excludedRoots.some(root => root === element || root.contains(element))) continue;
      if (element instanceof HTMLIFrameElement) {
        try {
          if (new URL(element.src || location.href, location.href).origin !== location.origin) {
            excludedBoundaryCodes.add('CROSS_ORIGIN_FRAME');
            excludedRoots.push(element);
          }
        } catch {
          excludedBoundaryCodes.add('CROSS_ORIGIN_FRAME');
          excludedRoots.push(element);
        }
        continue;
      }
      if (recipeSurface === 'coupang_product' && (
        element.matches('form,button,[role="button"],input,textarea,select,[role="radio"],[role="option"]')
      )) {
        if (hasFinalMeaning(element)) excludedBoundaryCodes.add('PAYMENT_OR_COMMITMENT');
        else excludedBoundaryCodes.add('UNSUPPORTED_INTERACTION');
        excludedRoots.push(element);
        continue;
      }
      if (element.matches('[contenteditable]:not([contenteditable="false"]), [role="textbox"]')) {
        return blockedObservation('sensitive_page');
      }
      if (isSensitiveControl(element) || hasUserInputValue(element)) return blockedObservation('sensitive_page');
      if ((element instanceof HTMLInputElement || element instanceof HTMLTextAreaElement || element instanceof HTMLSelectElement) &&
          !isPublicSearch(element) && normalize(element.getAttribute('type')).toLowerCase() !== 'hidden') {
        return blockedObservation('form');
      }
      if (hasFinalMeaning(element)) {
        excludedBoundaryCodes.add('PAYMENT_OR_COMMITMENT');
        excludedRoots.push(element);
      } else if (element.matches('button, [role="button"]')) {
        excludedBoundaryCodes.add('UNSUPPORTED_INTERACTION');
        excludedRoots.push(element);
      } else if (element instanceof HTMLFormElement && !element.querySelector('input[type="search"], [role="searchbox"]')) {
        excludedBoundaryCodes.add('FORM_SUBMISSION_REQUIRED');
        excludedRoots.push(element);
      }
    }

    const candidateElements = document.querySelectorAll('a[href], input[type="search"], [role="searchbox"]');
    const observedCandidates = [];
    for (let index = 0; index < Math.min(candidateElements.length, 1200) && observedCandidates.length < 120; index += 1) {
      const element = candidateElements[index];
      if (!isVisible(element) || isSensitiveControl(element)) continue;
      if (excludedRoots.some(root => root === element || root.contains(element))) continue;
      if (element.closest('form')?.querySelector('input[type="password"], input[autocomplete="one-time-code"], input[autocomplete^="cc-"]')) continue;
      const role = roleFor(element);
      const label = labelFor(element);
      const text = role === 'textbox' || role === 'combobox' ? '' : redact(element.innerText || element.textContent);
      if (!label && !text) continue;
      const nodeFingerprint = fingerprint([
        element.tagName.toLowerCase(),
        role,
        label,
        text,
        normalize(element.getAttribute('type')),
        normalize(element.getAttribute('href')),
      ]);
      const nodeRef = `${state.observationId}:node_${observedCandidates.length + 1}`;

      const safeHref = publicSameOriginHref(element);
      if (element instanceof HTMLAnchorElement && hasFinalMeaning(element)) {
        excludedBoundaryCodes.add('PAYMENT_OR_COMMITMENT');
        excludedRoots.push(element);
        continue;
      }
      if (safeHref) {
        const linkMeaning = `${label || text} ${new URL(safeHref).pathname}`;
        if (UNSUPPORTED_LINK.test(linkMeaning)) {
          excludedBoundaryCodes.add('UNSUPPORTED_INTERACTION');
          excludedRoots.push(element);
          continue;
        }
        const candidateRef = `candidate_${observedCandidates.length}_${nodeFingerprint.slice(3)}`;
        const candidate = { candidateRef, nodeRef, kind: 'safe_link', role, name: label || text, href: safeHref };
        observedCandidates.push(candidate);
        state.candidates.set(candidateRef, { element, fingerprint: nodeFingerprint, kind: 'safe_link' });
      } else if (element instanceof HTMLAnchorElement) {
        excludedBoundaryCodes.add('UNSUPPORTED_INTERACTION');
        excludedRoots.push(element);
      } else if (isPublicSearch(element)) {
        const candidateRef = `candidate_${observedCandidates.length}_${nodeFingerprint.slice(3)}`;
        const candidate = { candidateRef, nodeRef, kind: 'public_search', role: 'searchbox', name: label || 'Search' };
        observedCandidates.push(candidate);
        state.candidates.set(candidateRef, { element, fingerprint: nodeFingerprint, kind: 'public_search' });
      }
    }
    let visibleText = '';
    const walker = document.createTreeWalker(document.body || document.documentElement, NodeFilter.SHOW_TEXT);
    let textNode;
    let visited = 0;
    while ((textNode = walker.nextNode()) && visibleText.length < 10000 && visited < 5000) {
      visited += 1;
      const parent = textNode.parentElement;
      if (!parent || parent.closest('script, style, noscript, input, textarea, select, [contenteditable]') ||
          excludedRoots.some(root => root === parent || root.contains(parent)) || !isVisible(parent)) continue;
      const part = redact(textNode.textContent);
      if (part) visibleText += `${part}\n`;
    }
    if (recipeSurface === 'coupang_product') {
      state.recipeSurface = recipeSurface;
      state.recipeFingerprint = coupangRecipeFingerprint(recipeSurface);
    }
    return {
      observationId: state.observationId,
      title: redact(document.title),
      sensitivePage: false,
      candidates: observedCandidates,
      visibleText,
      pageTypeHint: pageTypeHint(),
      sessionStateHint: observedSessionState,
      recipeSurface,
      excludedBoundaryCodes: Array.from(excludedBoundaryCodes).sort(),
    };
  }

  function blockedObservation(reason) {
    state.observationId = randomId('obs');
    state.candidates = new Map();
    state.recipeSurface = null;
    state.recipeFingerprint = null;
    state.used = false;
    return {
      observationId: state.observationId,
      title: '',
      sensitivePage: true,
      blockedReason: reason,
      candidates: [],
      visibleText: '',
      pageTypeHint: 'unknown',
      sessionStateHint: 'unknown',
      excludedBoundaryCodes: [],
    };
  }

  function candidateFor(args, expectedKind) {
    if (args.generation !== state.generation) throw new Error('generation_mismatch');
    if (!state.observationId || args.observationId !== state.observationId) throw new Error('stale_observation');
    if (state.used) throw new Error('replay_conflict');
    if (sensitivePageReason()) throw new Error('sensitive_page');
    const candidate = state.candidates.get(args.candidateRef);
    if (!candidate || candidate.kind !== expectedKind || !candidate.element.isConnected) {
      throw new Error('target_not_observed');
    }
    const element = candidate.element;
    const currentFingerprint = fingerprint([
      element.tagName.toLowerCase(),
      roleFor(element),
      labelFor(element),
      roleFor(element) === 'textbox' || roleFor(element) === 'combobox'
        ? ''
        : redact(element.innerText || element.textContent),
      normalize(element.getAttribute('type')),
      normalize(element.getAttribute('href')),
    ]);
    if (currentFingerprint !== candidate.fingerprint) throw new Error('target_changed');
    return candidate.element;
  }

  function prepareSearch(args) {
    const element = candidateFor(args, 'public_search');
    if (!isPublicSearch(element) || isSensitiveControl(element) || typeof args.query !== 'string' ||
        !args.query || args.query.length > 160 || args.query !== args.query.trim() || likelySensitiveQuery(args.query)) {
      throw new Error('sensitive_input_forbidden');
    }
    state.used = true;
    // Search preparation only stages the already-authorized value. Deliberately
    // avoid focus/input/change/submit because page handlers can have side effects.
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
    if (!setter) throw new Error('input_unsupported');
    setter.call(element, args.query);
    if (element.value !== args.query) {
      return { applied: false, outcomeUnknown: true, code: 'PREPARATION_OUTCOME_UNKNOWN' };
    }
    return { applied: true, code: 'PUBLIC_SEARCH_PREPARED' };
  }

  function scroll(args) {
    if (args.generation !== state.generation) throw new Error('generation_mismatch');
    if (!state.observationId || args.observationId !== state.observationId) throw new Error('stale_observation');
    if (state.used) throw new Error('replay_conflict');
    if (sensitivePageReason()) throw new Error('sensitive_page');
    state.used = true;
    const distance = Math.max(240, Math.floor(innerHeight * 0.72));
    globalThis.scrollBy({ top: args.direction === 'up' ? -distance : distance, behavior: 'smooth' });
    return { applied: true, code: 'VIEWPORT_SCROLLED' };
  }

  function requireRecipeFreshness(args, expectedSurface) {
    if (args.generation !== state.generation) throw new Error('generation_mismatch');
    if (!state.observationId || args.observationId !== state.observationId) throw new Error('stale_observation');
    if (state.used) throw new Error('replay_conflict');
    if (!COUPANG_STEPS.has(args.stepId)) throw new Error('recipe_step_denied');
    if (state.recipeSurface !== expectedSurface || coupangSurface() !== expectedSurface) throw new Error('recipe_surface_mismatch');
    if (location.origin !== args.expectedOrigin || location.pathname !== args.expectedPath) throw new Error('recipe_url_mismatch');
    if (state.recipeFingerprint !== coupangRecipeFingerprint(expectedSurface)) throw new Error('target_changed');
  }

  function requireProductIdentity(args) {
    const match = location.pathname.match(COUPANG_PRODUCT_PATH);
    if (!match || match[1] !== args.productId) throw new Error('offer_identity_mismatch');
    const params = new URLSearchParams(location.search);
    const itemIds = params.getAll('itemId');
    const vendorItemIds = params.getAll('vendorItemId');
    if (itemIds.length !== 1 || vendorItemIds.length !== 1 ||
        itemIds[0] !== args.itemId || vendorItemIds[0] !== args.vendorItemId) {
      throw new Error('offer_identity_mismatch');
    }
  }

  function exactControls(names, roles) {
    const accepted = new Set(names.map(value => normalize(value).normalize('NFKC')));
    return visibleSemanticElements().filter(element =>
      roles.has(roleFor(element)) && isEnabled(element) && accepted.has(exactText(element))
    );
  }

  function uniqueControl(names, roles) {
    const controls = exactControls(names, roles);
    if (controls.length !== 1) throw new Error(controls.length ? 'target_ambiguous' : 'target_not_observed');
    if (FINAL_COMMITMENT.test(exactText(controls[0]))) throw new Error('final_action_forbidden');
    return controls[0];
  }

  function groupNameFor(element) {
    const labelledBy = normalize(element.getAttribute('aria-labelledby'));
    if (labelledBy) {
      const label = labelledBy.split(/\s+/).map(id => document.getElementById(id)?.textContent || '').join(' ');
      if (normalize(label)) return normalize(label).normalize('NFKC');
    }
    const group = element.closest('fieldset,[role="group"],[role="radiogroup"],[data-option-group]');
    if (!group) return '';
    const label = group.querySelector('legend,[data-option-group-label],[role="heading"]');
    return normalize(label?.textContent || group.getAttribute('aria-label')).normalize('NFKC');
  }

  function selectExactOption(args) {
    const expectedValue = normalize(args.valueName).normalize('NFKC');
    const expectedGroup = normalize(args.groupName).normalize('NFKC');
    const matches = [];
    for (const element of document.querySelectorAll('select,input[type="radio"],button,[role="radio"],[role="option"],[role="button"]')) {
      if (!isVisible(element) || !isEnabled(element)) continue;
      if (element instanceof HTMLSelectElement) {
        const group = normalize(labelFor(element)).normalize('NFKC');
        const option = Array.from(element.options).filter(item => normalize(item.textContent).normalize('NFKC') === expectedValue);
        if (group === expectedGroup && option.length === 1 && !option[0].disabled) matches.push({ element, option: option[0] });
        continue;
      }
      const value = exactText(element);
      if (value === expectedValue && groupNameFor(element) === expectedGroup) matches.push({ element, option: null });
    }
    if (matches.length !== 1) throw new Error(matches.length ? 'target_ambiguous' : 'target_not_observed');
    state.used = true;
    const { element, option } = matches[0];
    if (element instanceof HTMLSelectElement) {
      const setter = Object.getOwnPropertyDescriptor(HTMLSelectElement.prototype, 'value')?.set;
      if (!setter) throw new Error('input_unsupported');
      setter.call(element, option.value);
      element.dispatchEvent(new Event('input', { bubbles: true }));
      element.dispatchEvent(new Event('change', { bubbles: true }));
      if (normalize(element.selectedOptions[0]?.textContent).normalize('NFKC') !== expectedValue) {
        return { applied: false, outcomeUnknown: true, code: 'COUPANG_OPTION_OUTCOME_UNKNOWN' };
      }
    } else {
      element.click();
      const selected = element.checked === true || element.getAttribute('aria-checked') === 'true' ||
        element.getAttribute('aria-selected') === 'true' || element.classList.contains('selected');
      if (!selected) return { applied: false, outcomeUnknown: true, code: 'COUPANG_OPTION_OUTCOME_UNKNOWN' };
    }
    return { applied: true, code: 'COUPANG_OPTION_SELECTED' };
  }

  function quantityInput() {
    const controls = Array.from(document.querySelectorAll('input[type="number"],input[inputmode="numeric"],input[name*="quantity" i],input[id*="quantity" i]'))
      .filter(element => isVisible(element) && isEnabled(element) && /^(?:수량|상품\s*수량)$/.test(exactText(element)));
    if (controls.length !== 1) throw new Error(controls.length ? 'target_ambiguous' : 'target_not_observed');
    return controls[0];
  }

  function setExactQuantity(args) {
    const input = quantityInput();
    const desired = String(args.quantity);
    state.used = true;
    const setter = Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value')?.set;
    if (!setter) throw new Error('input_unsupported');
    setter.call(input, desired);
    input.dispatchEvent(new Event('input', { bubbles: true }));
    input.dispatchEvent(new Event('change', { bubbles: true }));
    if (input.value !== desired) return { applied: false, outcomeUnknown: true, code: 'COUPANG_QUANTITY_OUTCOME_UNKNOWN' };
    return { applied: true, code: 'COUPANG_QUANTITY_SET' };
  }

  function parseWon(value, allowPlain = false) {
    const source = String(value || '').trim();
    const matches = source.match(/(?:₩\s*[1-9][0-9,]{0,15}|[1-9][0-9,]{0,15}\s*원)/g) || [];
    if (allowPlain && /^[1-9][0-9,]{0,15}$/.test(source)) matches.push(source);
    return [...new Set(matches.map(item => Number(item.replace(/[^0-9]/g, '')))
      .filter(item => Number.isSafeInteger(item) && item > 0))];
  }

  function verifiedProductPrice(args) {
    const priceNodes = Array.from(document.querySelectorAll(
      '[itemprop="price"],meta[property="product:price:amount"],[data-testid*="price" i],.total-price,.price-value,[class*="price-value" i]',
    ));
    const prices = priceNodes.flatMap(node => {
      const content = node.getAttribute('content') || node.getAttribute('value') || node.textContent || '';
      return parseWon(content, true);
    });
    const observed = [...new Set(prices)];
    if (observed.length !== 1) throw new Error(observed.length ? 'price_ambiguous' : 'price_not_verified');
    const unitPrice = observed[0];
    if (unitPrice > args.unitPriceCeilingKrw) throw new Error('price_ceiling_exceeded');
    if (unitPrice * args.quantity > args.linePriceCeilingKrw) throw new Error('price_ceiling_exceeded');
    return unitPrice;
  }

  function approvedOptionStateMatches(args) {
    const options = args.approvedPreparation?.items?.[args.targetLineIndex]?.options;
    if (!options || typeof options !== 'object') return false;
    const visibleOptions = Array.from(document.querySelectorAll(
      'select,input[type="radio"],[role="radio"],[role="option"],[data-option-group] button,[data-option-group] [role="button"]',
    )).filter(element => isVisible(element) && isEnabled(element));
    if (options.kind === 'none') return visibleOptions.length === 0;
    if (options.kind !== 'choices' || !Array.isArray(options.choices)) return false;
    const observedGroups = new Set(visibleOptions.map(element => element instanceof HTMLSelectElement
      ? normalize(labelFor(element)).normalize('NFKC')
      : groupNameFor(element)));
    const approvedGroups = new Set(options.choices.map(choice => normalize(choice.groupName).normalize('NFKC')));
    if (observedGroups.has('') || observedGroups.size !== approvedGroups.size ||
        [...observedGroups].some(group => !approvedGroups.has(group))) return false;
    return options.choices.every(choice => {
      const expectedGroup = normalize(choice.groupName).normalize('NFKC');
      const expectedValue = normalize(choice.valueName).normalize('NFKC');
      const matches = visibleOptions.filter(element => {
        if (element instanceof HTMLSelectElement) {
          return normalize(labelFor(element)).normalize('NFKC') === expectedGroup &&
            normalize(element.selectedOptions?.[0]?.textContent).normalize('NFKC') === expectedValue;
        }
        const selected = element.checked === true || element.getAttribute('aria-checked') === 'true' ||
          element.getAttribute('aria-selected') === 'true' || element.classList.contains('selected');
        return selected && groupNameFor(element) === expectedGroup && exactText(element) === expectedValue;
      });
      return matches.length === 1;
    });
  }

  function pricingAbsent(prices) {
    return prices.length ? 'price_ceiling_exceeded' : 'price_not_verified';
  }

  function verifyProductReady(args) {
    if (!approvedOptionStateMatches(args)) throw new Error('options_not_ready');
    verifiedProductPrice(args);
    const input = quantityInput();
    if (input.value !== String(args.quantity)) throw new Error('quantity_mismatch');
    const actions = [
      ...exactControls(['장바구니 담기'], new Set(['button'])),
      ...exactControls(['바로구매', '바로 구매'], new Set(['button', 'link'])),
    ];
    if (!actions.length) throw new Error('options_not_ready');
    return { applied: true, code: 'COUPANG_OPTIONS_VERIFIED' };
  }

  function offerIdentityFromURL(href) {
    try {
      const url = new URL(href, location.href);
      const match = url.pathname.match(COUPANG_PRODUCT_PATH);
      const itemIds = url.searchParams.getAll('itemId');
      const vendorItemIds = url.searchParams.getAll('vendorItemId');
      if (!match || itemIds.length !== 1 || vendorItemIds.length !== 1 ||
          !itemIds[0] || !vendorItemIds[0]) return null;
      return { productId: match[1], itemId: itemIds[0], vendorItemId: vendorItemIds[0] };
    } catch {
      return null;
    }
  }

  function cartLineFor(anchor) {
    return anchor.closest('[data-vendor-item-id],li,tr,article,[class*="cart-product" i],[class*="cart-item" i]');
  }

  function cartUnitPrices(root) {
    const priceNodes = Array.from(root.querySelectorAll(
      '[data-vitlane-unit-price-krw],[itemprop="price"],meta[property="product:price:amount"],' +
      '[data-testid*="price" i],.prod-sale-price .total-price,.total-price > strong,.price-value,' +
      '[class*="price-value" i]',
    )).filter(node => node.tagName === 'META' || isVisible(node));
    return [...new Set(priceNodes.flatMap(node => {
      const explicit = node.getAttribute('data-vitlane-unit-price-krw') ||
        node.getAttribute('content') || node.getAttribute('value');
      return parseWon(explicit || node.textContent, Boolean(explicit));
    }))];
  }

  function readCartLines() {
    const lines = [];
    const seen = new Set();
    for (const anchor of document.querySelectorAll('a[href*="/vp/products/"]')) {
      const identity = offerIdentityFromURL(anchor.href);
      const root = identity && cartLineFor(anchor);
      if (!identity || !root || !isVisible(root)) continue;
      const key = `${identity.productId}:${identity.itemId}:${identity.vendorItemId}`;
      if (seen.has(key)) continue;
      const checkbox = root.querySelector('input[type="checkbox"]');
      if (!checkbox || checkbox.checked !== true) continue;
      const quantity = root.querySelector('input[type="number"],input[name*="quantity" i],input[id*="quantity" i]');
      const amount = Number(quantity?.value);
      const prices = cartUnitPrices(root);
      // A cart row can contain list prices, coupons, rewards, or delivery copy.
      // Require one structured, unambiguous unit price; merchant DOM changes
      // fail closed until the versioned recipe is re-verified.
      if (!Number.isSafeInteger(amount) || amount < 1 || amount > 99 || prices.length !== 1) {
        throw new Error('cart_line_unverified');
      }
      seen.add(key);
      lines.push({ ...identity, quantity: amount, unitPrice: prices[0] });
    }
    return lines;
  }

  function verifyApprovedCartLines(args) {
    const totalPriceCeilingKrw = args.approval?.totalPriceCeilingKrw;
    if (!Number.isSafeInteger(totalPriceCeilingKrw) || totalPriceCeilingKrw < 1) {
      throw new Error('approval_invalid');
    }
    const expected = new Map(args.approvedLines.map(line => [
      `${line.productId}:${line.itemId}:${line.vendorItemId}`,
      line,
    ]));
    const actual = readCartLines();
    if (actual.length !== expected.size) throw new Error('cart_selection_mismatch');
    let verifiedTotal = 0;
    for (const line of actual) {
      const approved = expected.get(`${line.productId}:${line.itemId}:${line.vendorItemId}`);
      if (!approved || approved.quantity !== line.quantity) throw new Error('cart_selection_mismatch');
      const observedLinePrice = line.unitPrice * line.quantity;
      if (!Number.isSafeInteger(observedLinePrice) ||
          line.unitPrice > approved.unitPriceCeilingKrw ||
          observedLinePrice > approved.linePriceCeilingKrw) {
        throw new Error('price_ceiling_exceeded');
      }
      verifiedTotal += observedLinePrice;
    }
    if (!Number.isSafeInteger(verifiedTotal) || verifiedTotal > totalPriceCeilingKrw) {
      throw new Error('price_ceiling_exceeded');
    }
  }

  function activateProductAction(args, names, code) {
    verifyProductReady(args);
    const control = uniqueControl(names, new Set(['button', 'link']));
    state.used = true;
    control.click();
    return { applied: true, code };
  }

  function executeCoupangPreparation(args) {
    const surface = args.stepId === 'start_checkout' ? 'coupang_cart' : 'coupang_product';
    requireRecipeFreshness(args, surface);
    if (surface === 'coupang_product') requireProductIdentity(args);
    switch (args.stepId) {
      case 'select_option': return selectExactOption(args);
      case 'set_quantity': return setExactQuantity(args);
      case 'verify_options':
        state.used = true;
        return verifyProductReady(args);
      case 'add_to_cart':
        return activateProductAction(args, ['장바구니 담기'], 'COUPANG_ADD_TO_CART_TRIGGERED');
      case 'buy_now':
        return activateProductAction(args, ['바로구매', '바로 구매'], 'COUPANG_BUY_NOW_TRIGGERED');
      case 'start_checkout': {
        if (!isCoupangCartReview()) throw new Error('recipe_surface_mismatch');
        verifyApprovedCartLines(args);
        const controls = visibleSemanticElements().filter(element => {
          if (!new Set(['button', 'link']).has(roleFor(element)) || !isEnabled(element)) return false;
          const name = exactText(element);
          return ['구매하기', '선택상품 구매하기', '선택 상품 구매하기'].includes(name) ||
            /^(?:[1-9][0-9]*개 상품 구매하기|구매하기\s*\([1-9][0-9]*(?:개)?\))$/.test(name);
        });
        if (controls.length !== 1) throw new Error(controls.length ? 'target_ambiguous' : 'target_not_observed');
        const control = controls[0];
        state.used = true;
        control.click();
        return { applied: true, code: 'COUPANG_CHECKOUT_TRIGGERED' };
      }
      default: throw new Error('recipe_step_denied');
    }
  }

  globalThis.__vitlaneAgent = Object.freeze({
    version: 1,
    setControlGeneration(generation) {
      state.generation = generation;
      state.observationId = null;
      state.candidates = new Map();
      state.recipeSurface = null;
      state.recipeFingerprint = null;
      state.used = false;
      return true;
    },
    observe,
    prepareSearch,
    executeCoupangPreparation,
    scroll,
  });
})();
