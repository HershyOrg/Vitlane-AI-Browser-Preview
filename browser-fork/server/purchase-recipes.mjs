import { createHash } from 'node:crypto';

/**
 * Deterministic merchant purchase-preparation recipes.
 *
 * This module deliberately stops at a checkout-ready handoff. It does not
 * expose a payment, place-order, or order-confirmation action. The native
 * executor must still bind every semantic element reference to a fresh page
 * observation immediately before executing an action.
 */

export class PurchaseRecipeError extends Error {
  constructor(message, code = 'INVALID_RECIPE_INPUT') {
    super(message);
    this.name = 'PurchaseRecipeError';
    this.code = code;
  }
}

export const PURCHASE_PREPARATION_STAGE = Object.freeze({
  SEARCH: 'search',
  PRODUCT: 'product',
  OPTIONS: 'options',
  SET_QUANTITY: 'set_quantity',
  ADD_TO_CART: 'add_to_cart',
  BUY_NOW: 'buy_now',
  CART: 'cart',
  CHECKOUT_READY: 'checkout_ready',
});

export const PURCHASE_PREPARATION_EFFECT = Object.freeze({
  NAVIGATE: 'navigate',
  ACTIVATE: 'activate',
  VERIFY: 'verify',
  HANDOFF: 'handoff',
});

export const PURCHASE_PREPARATION_MODE = Object.freeze({
  SINGLE: 'single',
  MULTI: 'multi',
});

export const COUPANG_PURCHASE_ADAPTER_ID = 'builtin.coupang.purchase-preparation';
export const PURCHASE_APPROVAL_MAX_TTL_MS = 10 * 60 * 1000;

export const COUPANG_PURCHASE_STEP_ID = Object.freeze({
  SEARCH: 'search',
  PRODUCT: 'product',
  SELECT_OPTION: 'select_option',
  VERIFY_OPTIONS: 'verify_options',
  SET_QUANTITY: 'set_quantity',
  ADD_TO_CART: 'add_to_cart',
  BUY_NOW: 'buy_now',
  OPEN_CART: 'open_cart',
  START_CHECKOUT: 'start_checkout',
  CHECKOUT_READY: 'checkout_ready',
});

const PAGE_STATE = Object.freeze({
  SEARCH: 'search',
  PRODUCT: 'product',
  CART: 'cart',
  CHECKOUT_READY: 'checkout_ready',
  UNKNOWN: 'unknown',
});

const OPTION_STATE = Object.freeze({
  NOT_REQUIRED: 'not_required',
  REQUIRED: 'required',
  PARTIALLY_RESOLVED: 'partially_resolved',
  RESOLVED: 'resolved',
  UNKNOWN: 'unknown',
});

const SEMANTIC_ROLES = new Set(['button', 'heading', 'link', 'option', 'radio']);
const PAGE_STATES = new Set(Object.values(PAGE_STATE));
const OPTION_STATES = new Set(Object.values(OPTION_STATE));
const IDENTIFIER = /^[A-Za-z0-9][A-Za-z0-9._:-]*$/;
const COUPANG_PRODUCT_PATH = /^\/vp\/products\/[1-9][0-9]*\/?$/;
const COUPANG_ID = /^[1-9][0-9]{0,19}$/;

const deepFreeze = (value) => {
  if (value && typeof value === 'object' && !Object.isFrozen(value)) {
    Object.freeze(value);
    for (const child of Object.values(value)) deepFreeze(child);
  }
  return value;
};

function canonicalize(value) {
  if (value === null || typeof value === 'boolean' || typeof value === 'string') return JSON.stringify(value);
  if (typeof value === 'number' && Number.isFinite(value)) return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(canonicalize).join(',')}]`;
  if (record(value)) {
    return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${canonicalize(value[key])}`).join(',')}}`;
  }
  throw new TypeError('Value is not canonically serializable');
}

function sha256(value) {
  return createHash('sha256').update(typeof value === 'string' ? value : canonicalize(value)).digest('hex');
}

const coupangRecipeV1 = deepFreeze({
  merchantId: 'COUPANG',
  recipeVersion: '1',
  recipeId: 'coupang.purchase-preparation@1',
  allowedOrigins: [
    'https://coupang.com',
    'https://www.coupang.com',
    'https://cart.coupang.com',
  ],
  pages: {
    search: {
      origins: ['https://coupang.com', 'https://www.coupang.com'],
      pathPatterns: [/^\/np\/search\/?$/],
    },
    product: {
      origins: ['https://coupang.com', 'https://www.coupang.com'],
      pathPatterns: [COUPANG_PRODUCT_PATH],
    },
    cart: {
      originPaths: [
        { origin: 'https://cart.coupang.com', pathPatterns: [/^\/cartView\.pang$/] },
      ],
    },
  },
  navigation: {
    searchOrigin: 'https://www.coupang.com',
    searchPath: '/np/search',
    searchParameter: 'q',
    productOrigin: 'https://www.coupang.com',
    cartUrl: 'https://cart.coupang.com/cartView.pang',
  },
  locators: {
    addToCart: {
      strategy: 'role_and_accessible_name',
      roles: ['button'],
      exactNames: ['장바구니 담기'],
    },
    buyNow: {
      strategy: 'role_and_accessible_name',
      roles: ['button', 'link'],
      exactNames: ['바로구매', '바로 구매'],
    },
    cartCheckout: {
      strategy: 'role_and_accessible_name',
      roles: ['button', 'link'],
      exactNames: ['구매하기', '선택상품 구매하기', '선택 상품 구매하기'],
      namePatterns: [
        /^[1-9][0-9]*개 상품 구매하기$/,
        /^구매하기\s*\([1-9][0-9]*(?:개)?\)$/,
      ],
    },
    quantityIncrease: {
      strategy: 'role_and_accessible_name',
      roles: ['button'],
      exactNames: ['수량 늘리기', '수량 증가', '증가', '+'],
    },
    quantityDecrease: {
      strategy: 'role_and_accessible_name',
      roles: ['button'],
      exactNames: ['수량 줄이기', '수량 감소', '감소', '-'],
    },
  },
  terminalBoundary: {
    stage: PURCHASE_PREPARATION_STAGE.CHECKOUT_READY,
    effect: PURCHASE_PREPARATION_EFFECT.HANDOFF,
    finalCommitmentAllowed: false,
    forbiddenActionKinds: ['payment', 'pay', 'place_order', 'confirm_order', 'confirm_purchase'],
  },
});

export const PURCHASE_RECIPE_REGISTRY = deepFreeze({
  COUPANG: { '1': coupangRecipeV1 },
});

function record(value) {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function boundedText(value, name, max = 300) {
  if (typeof value !== 'string') throw new PurchaseRecipeError(`${name} must be a string`);
  const normalized = value.normalize('NFKC').replace(/\s+/g, ' ').trim();
  if (!normalized || normalized.length > max || /[\u0000-\u001f\u007f]/.test(normalized)) {
    throw new PurchaseRecipeError(`Invalid ${name}`);
  }
  return normalized;
}

function identifier(value, name) {
  const result = boundedText(value, name, 100);
  if (!IDENTIFIER.test(result)) throw new PurchaseRecipeError(`Invalid ${name}`);
  return result;
}

function likelySensitive(value) {
  const text = value.normalize('NFKC');
  if (/https?:\/\/|\b(?:password|passwd|passcode|otp|cvv|cvc|token|secret)\b/i.test(text)) return true;
  if (/(?:비밀번호|인증번호|일회용\s*코드|카드\s*번호|보안\s*코드|주민등록)/.test(text)) return true;
  if (/\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/i.test(text)) return true;
  return text.replace(/[^0-9]/g, '').length >= 11;
}

function searchQuery(value) {
  const query = boundedText(value, 'item.query', 160);
  if (likelySensitive(query)) throw new PurchaseRecipeError('Search query violates public-search policy', 'QUERY_POLICY_DENIED');
  return query;
}

function coupangId(value, name) {
  const result = boundedText(value, name, 20);
  if (!COUPANG_ID.test(result)) throw new PurchaseRecipeError(`Invalid ${name}`, 'OFFER_IDENTITY_INVALID');
  return result;
}

function normalizeOfferIdentity(value, name = 'item.offerIdentity') {
  if (!record(value)) throw new PurchaseRecipeError(`${name} is required`, 'OFFER_IDENTITY_INVALID');
  return {
    productId: coupangId(value.productId, `${name}.productId`),
    itemId: coupangId(value.itemId, `${name}.itemId`),
    vendorItemId: coupangId(value.vendorItemId, `${name}.vendorItemId`),
  };
}

function quantity(value, name = 'item.quantity') {
  if (!Number.isSafeInteger(value) || value < 1 || value > 99) {
    throw new PurchaseRecipeError(`${name} must be an integer from 1 to 99`, 'QUANTITY_INVALID');
  }
  return value;
}

function won(value, name) {
  if (!Number.isSafeInteger(value) || value < 1 || value > 1_000_000_000_000) {
    throw new PurchaseRecipeError(`${name} must be a positive bounded KRW integer`, 'PRICE_CEILING_INVALID');
  }
  return value;
}

function normalizeApproval(value, { requireDigest = true } = {}) {
  if (!record(value)) throw new PurchaseRecipeError('approval is required', 'APPROVAL_INVALID');
  const approvalId = identifier(value.approvalId, 'approval.approvalId');
  let approvalDigest;
  if (value.approvalDigest !== undefined) {
    approvalDigest = boundedText(value.approvalDigest, 'approval.approvalDigest', 71);
    if (!/^sha256:[a-f0-9]{64}$/.test(approvalDigest)) {
      throw new PurchaseRecipeError('Invalid approval.approvalDigest', 'APPROVAL_INVALID');
    }
  } else if (requireDigest) {
    throw new PurchaseRecipeError('approval.approvalDigest is required', 'APPROVAL_INVALID');
  }
  if (value.currency !== 'KRW') throw new PurchaseRecipeError('Approval currency must be KRW', 'APPROVAL_INVALID');
  if (!Number.isSafeInteger(value.revision) || value.revision < 1) {
    throw new PurchaseRecipeError('Invalid approval.revision', 'APPROVAL_INVALID');
  }
  const expiry = typeof value.expiresAt === 'string' ? new Date(value.expiresAt) : new Date(Number.NaN);
  if (!Number.isFinite(expiry.getTime())) throw new PurchaseRecipeError('Invalid approval.expiresAt', 'APPROVAL_INVALID');
  const remainingMs = expiry.getTime() - Date.now();
  if (remainingMs <= 0 || remainingMs > PURCHASE_APPROVAL_MAX_TTL_MS) {
    throw new PurchaseRecipeError('Approval must expire within 10 minutes', 'APPROVAL_EXPIRED_OR_TOO_LONG');
  }
  return {
    approvalId,
    ...(approvalDigest ? { approvalDigest } : {}),
    currency: 'KRW',
    revision: value.revision,
    expiresAt: expiry.toISOString(),
    totalPriceCeilingKrw: won(value.totalPriceCeilingKrw, 'approval.totalPriceCeilingKrw'),
  };
}

function offerKey(identity) {
  return `${identity.productId}:${identity.itemId}:${identity.vendorItemId}`;
}

function offerBinding(item) {
  return {
    ...item.offerIdentity,
    quantity: item.quantity,
    unitPriceCeilingKrw: item.unitPriceCeilingKrw,
    linePriceCeilingKrw: item.linePriceCeilingKrw,
  };
}

function approvalBinding(approval) {
  return { ...approval };
}

function normalizeProductUrl(recipe, value, offerIdentity) {
  const raw = boundedText(value, 'item.productUrl', 4096);
  let parsed;
  try { parsed = new URL(raw); }
  catch { throw new PurchaseRecipeError('Invalid item.productUrl', 'PRODUCT_URL_DENIED'); }
  if (parsed.protocol !== 'https:' || parsed.username || parsed.password || parsed.port || parsed.hash ||
      !recipe.pages.product.origins.includes(parsed.origin) || !COUPANG_PRODUCT_PATH.test(parsed.pathname)) {
    throw new PurchaseRecipeError('Product URL is outside the recipe', 'PRODUCT_URL_DENIED');
  }
  const pathProductId = parsed.pathname.match(COUPANG_PRODUCT_PATH)?.[0].split('/').filter(Boolean).at(-1);
  if (pathProductId !== offerIdentity.productId || parsed.searchParams.get('itemId') !== offerIdentity.itemId ||
      parsed.searchParams.get('vendorItemId') !== offerIdentity.vendorItemId) {
    throw new PurchaseRecipeError('Product URL does not match the approved offer identity', 'OFFER_IDENTITY_MISMATCH');
  }
  const target = new URL(`/vp/products/${offerIdentity.productId}`, recipe.navigation.productOrigin);
  target.searchParams.set('itemId', offerIdentity.itemId);
  target.searchParams.set('vendorItemId', offerIdentity.vendorItemId);
  return target.href;
}

function normalizeOptions(value) {
  if (!record(value) || !['none', 'choices'].includes(value.kind)) {
    throw new PurchaseRecipeError('item.options must explicitly declare none or choices');
  }
  if (value.kind === 'none') {
    if ('choices' in value) throw new PurchaseRecipeError('none options cannot contain choices');
    return { kind: 'none' };
  }
  if (!Array.isArray(value.choices) || value.choices.length < 1 || value.choices.length > 10) {
    throw new PurchaseRecipeError('Choice options require 1-10 choices');
  }
  const seen = new Set();
  const seenGroups = new Set();
  const choices = value.choices.map((choice, index) => {
    if (!record(choice)) throw new PurchaseRecipeError(`Invalid item.options.choices[${index}]`);
    const groupName = boundedText(choice.groupName, `item.options.choices[${index}].groupName`, 120);
    const valueName = boundedText(choice.valueName, `item.options.choices[${index}].valueName`, 120);
    if (likelySensitive(groupName) || likelySensitive(valueName)) {
      throw new PurchaseRecipeError('Option choice violates public-data policy', 'OPTION_POLICY_DENIED');
    }
    const key = `${groupName}\u0000${valueName}`;
    if (seen.has(key) || seenGroups.has(groupName)) {
      throw new PurchaseRecipeError('Each option group may have only one approved value');
    }
    seen.add(key);
    seenGroups.add(groupName);
    return { groupName, valueName };
  });
  return { kind: 'choices', choices };
}

function getRecipe(merchantId, recipeVersion) {
  const merchant = identifier(merchantId, 'merchantId');
  const version = identifier(recipeVersion, 'recipeVersion');
  const recipe = PURCHASE_RECIPE_REGISTRY[merchant]?.[version];
  if (!recipe) throw new PurchaseRecipeError('Unknown merchant recipe', 'RECIPE_NOT_FOUND');
  return recipe;
}

function approvalDigestMaterial(normalized) {
  const { approvalDigest: _approvalDigest, ...approval } = normalized.approval;
  return {
    merchantId: normalized.recipe.merchantId,
    recipeVersion: normalized.recipe.recipeVersion,
    mode: normalized.mode,
    route: normalized.mode === PURCHASE_PREPARATION_MODE.SINGLE ? 'single_buy_now' : 'multi_cart_checkout',
    approval,
    items: normalized.items,
  };
}

function normalizePlanInput(input, { verifyDigest = true } = {}) {
  if (!record(input)) throw new PurchaseRecipeError('Plan input is required');
  const recipe = getRecipe(input.merchantId, input.recipeVersion);
  const approval = normalizeApproval(input.approval, { requireDigest: verifyDigest });
  if (!Object.values(PURCHASE_PREPARATION_MODE).includes(input.mode)) {
    throw new PurchaseRecipeError('Invalid purchase-preparation mode');
  }
  if (!Array.isArray(input.items) || input.items.length < 1 || input.items.length > 20) {
    throw new PurchaseRecipeError('Plan requires 1-20 items');
  }
  if (input.mode === PURCHASE_PREPARATION_MODE.SINGLE && input.items.length !== 1) {
    throw new PurchaseRecipeError('Single-item mode requires exactly one item');
  }
  if (input.mode === PURCHASE_PREPARATION_MODE.MULTI && input.items.length < 2) {
    throw new PurchaseRecipeError('Multi-item mode requires at least two items');
  }
  const items = input.items.map((item, index) => {
    if (!record(item)) throw new PurchaseRecipeError(`Invalid items[${index}]`);
    const offerIdentity = normalizeOfferIdentity(item.offerIdentity, `items[${index}].offerIdentity`);
    const normalizedQuantity = quantity(item.quantity, `items[${index}].quantity`);
    const unitPriceCeilingKrw = won(item.unitPriceCeilingKrw, `items[${index}].unitPriceCeilingKrw`);
    const linePriceCeilingKrw = won(item.linePriceCeilingKrw, `items[${index}].linePriceCeilingKrw`);
    if (linePriceCeilingKrw < unitPriceCeilingKrw * normalizedQuantity) {
      throw new PurchaseRecipeError('Line ceiling is below unit ceiling times quantity', 'PRICE_CEILING_INVALID');
    }
    return {
      query: searchQuery(item.query),
      offerIdentity,
      quantity: normalizedQuantity,
      unitPriceCeilingKrw,
      linePriceCeilingKrw,
      productUrl: normalizeProductUrl(recipe, item.productUrl, offerIdentity),
      options: normalizeOptions(item.options),
    };
  });
  if (new Set(items.map((item) => offerKey(item.offerIdentity))).size !== items.length) {
    throw new PurchaseRecipeError('Duplicate offer identity in plan', 'OFFER_IDENTITY_INVALID');
  }
  if (items.reduce((sum, item) => sum + item.linePriceCeilingKrw, 0) > approval.totalPriceCeilingKrw) {
    throw new PurchaseRecipeError('Total ceiling is below approved line ceilings', 'PRICE_CEILING_INVALID');
  }
  const normalized = { recipe, approval, mode: input.mode, items };
  if (verifyDigest) {
    const expected = `sha256:${sha256(approvalDigestMaterial(normalized))}`;
    if (approval.approvalDigest !== expected) {
      throw new PurchaseRecipeError('Approval digest does not match normalized plan', 'APPROVAL_DIGEST_MISMATCH');
    }
  }
  return normalized;
}

export function purchaseApprovalDigestMaterial(input) {
  return deepFreeze(approvalDigestMaterial(normalizePlanInput(input, { verifyDigest: false })));
}

export function canonicalPurchaseApprovalDigestMaterial(input) {
  return canonicalize(purchaseApprovalDigestMaterial(input));
}

export function computePurchaseApprovalDigest(input) {
  return `sha256:${sha256(purchaseApprovalDigestMaterial(input))}`;
}

export function verifyPurchaseApprovalDigest(input) {
  normalizePlanInput(input, { verifyDigest: true });
  return true;
}

/** Returns the exact extra-key-stripped snapshot that an approval digest covers. */
export function normalizePurchasePreparationPlanInput(input) {
  const { recipe, approval, mode, items } = normalizePlanInput(input);
  return deepFreeze({
    merchantId: recipe.merchantId,
    recipeVersion: recipe.recipeVersion,
    mode,
    approval,
    items,
  });
}

function searchUrl(recipe, query) {
  const url = new URL(recipe.navigation.searchPath, recipe.navigation.searchOrigin);
  url.searchParams.set(recipe.navigation.searchParameter, query);
  return url.href;
}

function semanticLocator(roles, exactNames, exactGroupNames = undefined) {
  return {
    strategy: 'role_and_accessible_name',
    roles,
    exactNames,
    ...(exactGroupNames ? { exactGroupNames } : {}),
  };
}

function recipeAction(recipe, kind, effect, stepId, bindings, extra = {}) {
  return {
    kind,
    effect,
    adapterId: COUPANG_PURCHASE_ADAPTER_ID,
    recipeVersion: recipe.recipeVersion,
    stepId,
    bindings,
    ...extra,
  };
}

function itemSteps(recipe, approvedPreparation, item, itemIndex, terminalAction) {
  const prefix = `item_${itemIndex + 1}`;
  const authorization = approvalBinding(approvedPreparation.approval);
  const planBinding = { approvedPreparation, approval: authorization, targetLineIndex: itemIndex };
  const steps = [
    {
      stepId: `${prefix}.search`,
      stage: PURCHASE_PREPARATION_STAGE.SEARCH,
      expectedPageState: null,
      action: recipeAction(recipe, PURCHASE_PREPARATION_STAGE.SEARCH, PURCHASE_PREPARATION_EFFECT.NAVIGATE,
        COUPANG_PURCHASE_STEP_ID.SEARCH, { ...planBinding, query: item.query }, { targetUrl: searchUrl(recipe, item.query) }),
    },
    {
      stepId: `${prefix}.product`,
      stage: PURCHASE_PREPARATION_STAGE.PRODUCT,
      expectedPageState: PAGE_STATE.SEARCH,
      action: recipeAction(recipe, PURCHASE_PREPARATION_STAGE.PRODUCT, PURCHASE_PREPARATION_EFFECT.NAVIGATE,
        COUPANG_PURCHASE_STEP_ID.PRODUCT, { ...planBinding, ...offerBinding(item) }, { targetUrl: item.productUrl }),
    },
  ];

  if (item.options.kind === 'choices') {
    item.options.choices.forEach((choice, optionIndex) => {
      steps.push({
        stepId: `${prefix}.options.${optionIndex + 1}`,
        stage: PURCHASE_PREPARATION_STAGE.OPTIONS,
        expectedPageState: PAGE_STATE.PRODUCT,
        expectedOffer: offerBinding(item),
        expectedOptionStates: [OPTION_STATE.REQUIRED, OPTION_STATE.PARTIALLY_RESOLVED],
        action: recipeAction(recipe, PURCHASE_PREPARATION_STAGE.OPTIONS, PURCHASE_PREPARATION_EFFECT.ACTIVATE,
          COUPANG_PURCHASE_STEP_ID.SELECT_OPTION,
          { ...planBinding, ...offerBinding(item), targetOptionIndex: optionIndex, groupName: choice.groupName, valueName: choice.valueName },
          { locator: semanticLocator(['option', 'radio', 'button'], [choice.valueName], [choice.groupName]) }),
      });
    });
    steps.push({
      stepId: `${prefix}.options.verify`,
      stage: PURCHASE_PREPARATION_STAGE.OPTIONS,
      expectedPageState: PAGE_STATE.PRODUCT,
      expectedOffer: offerBinding(item),
      expectedOptionStates: [OPTION_STATE.RESOLVED],
      action: recipeAction(recipe, PURCHASE_PREPARATION_STAGE.OPTIONS, PURCHASE_PREPARATION_EFFECT.VERIFY,
        COUPANG_PURCHASE_STEP_ID.VERIFY_OPTIONS, { ...planBinding, ...offerBinding(item) }),
    });
  } else {
    steps.push({
      stepId: `${prefix}.options.verify`,
      stage: PURCHASE_PREPARATION_STAGE.OPTIONS,
      expectedPageState: PAGE_STATE.PRODUCT,
      expectedOffer: offerBinding(item),
      expectedOptionStates: [OPTION_STATE.NOT_REQUIRED],
      action: recipeAction(recipe, PURCHASE_PREPARATION_STAGE.OPTIONS, PURCHASE_PREPARATION_EFFECT.VERIFY,
        COUPANG_PURCHASE_STEP_ID.VERIFY_OPTIONS, { ...planBinding, ...offerBinding(item) }),
    });
  }

  steps.push({
    stepId: `${prefix}.quantity`,
    stage: PURCHASE_PREPARATION_STAGE.SET_QUANTITY,
    expectedPageState: PAGE_STATE.PRODUCT,
    expectedOffer: offerBinding(item),
    action: recipeAction(recipe, PURCHASE_PREPARATION_STAGE.SET_QUANTITY, PURCHASE_PREPARATION_EFFECT.ACTIVATE,
      COUPANG_PURCHASE_STEP_ID.SET_QUANTITY,
      { ...planBinding, ...offerBinding(item) },
      { locators: { increase: recipe.locators.quantityIncrease, decrease: recipe.locators.quantityDecrease } }),
  });

  steps.push({
    stepId: `${prefix}.${terminalAction}`,
    stage: terminalAction,
    expectedPageState: PAGE_STATE.PRODUCT,
    expectedOffer: offerBinding(item),
    requireApprovedQuantity: true,
    expectedOptionStates: [OPTION_STATE.NOT_REQUIRED, OPTION_STATE.RESOLVED],
    action: recipeAction(recipe, terminalAction, PURCHASE_PREPARATION_EFFECT.ACTIVATE,
      terminalAction === PURCHASE_PREPARATION_STAGE.BUY_NOW ? COUPANG_PURCHASE_STEP_ID.BUY_NOW : COUPANG_PURCHASE_STEP_ID.ADD_TO_CART,
      { ...planBinding, ...offerBinding(item) },
      { locator: terminalAction === PURCHASE_PREPARATION_STAGE.BUY_NOW ? recipe.locators.buyNow : recipe.locators.addToCart }),
  });
  return steps;
}

/**
 * Build an immutable route. A single-item plan uses Buy now; a multi-item plan
 * adds each item to the cart, then opens the cart and advances only to the
 * checkout-ready page.
 */
export function buildPurchasePreparationPlan(input) {
  const { recipe, approval, mode, items } = normalizePlanInput(input);
  const route = mode === PURCHASE_PREPARATION_MODE.SINGLE ? 'single_buy_now' : 'multi_cart_checkout';
  const approvedPreparation = deepFreeze({
    merchantId: recipe.merchantId,
    recipeVersion: recipe.recipeVersion,
    mode,
    route,
    approval,
    items,
  });
  const approvedLines = items.map(offerBinding);
  const terminalAction = mode === PURCHASE_PREPARATION_MODE.SINGLE
    ? PURCHASE_PREPARATION_STAGE.BUY_NOW
    : PURCHASE_PREPARATION_STAGE.ADD_TO_CART;
  const steps = items.flatMap((item, index) => itemSteps(recipe, approvedPreparation, item, index, terminalAction));
  if (mode === PURCHASE_PREPARATION_MODE.MULTI) {
    steps.push(
      {
        stepId: 'cart.open',
        stage: PURCHASE_PREPARATION_STAGE.CART,
        expectedPageState: PAGE_STATE.PRODUCT,
        action: recipeAction(recipe, PURCHASE_PREPARATION_STAGE.CART, PURCHASE_PREPARATION_EFFECT.NAVIGATE,
          COUPANG_PURCHASE_STEP_ID.OPEN_CART, { approvedPreparation, approval: approvalBinding(approval), approvedLines }, { targetUrl: recipe.navigation.cartUrl }),
      },
      {
        stepId: 'cart.checkout',
        stage: PURCHASE_PREPARATION_STAGE.CART,
        expectedPageState: PAGE_STATE.CART,
        expectedApprovedLines: approvedLines,
        action: recipeAction(recipe, PURCHASE_PREPARATION_STAGE.CART, PURCHASE_PREPARATION_EFFECT.ACTIVATE,
          COUPANG_PURCHASE_STEP_ID.START_CHECKOUT, { approvedPreparation, approval: approvalBinding(approval), approvedLines }, { locator: recipe.locators.cartCheckout }),
      },
    );
  }
  steps.push({
    stepId: 'checkout.ready',
    stage: PURCHASE_PREPARATION_STAGE.CHECKOUT_READY,
    // This is a local terminal marker. The checkout host is never observed;
    // buy-now/start-checkout activation immediately transfers control.
    expectedPageState: null,
    action: recipeAction(recipe, PURCHASE_PREPARATION_STAGE.CHECKOUT_READY, PURCHASE_PREPARATION_EFFECT.HANDOFF,
      COUPANG_PURCHASE_STEP_ID.CHECKOUT_READY, { approvedPreparation, approval: approvalBinding(approval), approvedLines }, { reasonCode: 'CHECKOUT_READY_USER_HANDOFF' }),
  });
  return deepFreeze({
    planVersion: 1,
    merchantId: recipe.merchantId,
    recipeVersion: recipe.recipeVersion,
    recipeId: recipe.recipeId,
    route,
    approvedPreparation,
    approval,
    mode,
    itemCount: items.length,
    items,
    finalCommitmentAllowed: false,
    steps,
  });
}

function normalizeObservation(input) {
  if (!record(input)) throw new PurchaseRecipeError('Observation is required', 'INVALID_OBSERVATION');
  const origin = boundedText(input.origin, 'observation.origin', 512);
  let parsedOrigin;
  try { parsedOrigin = new URL(origin); }
  catch { throw new PurchaseRecipeError('Invalid observation.origin', 'INVALID_OBSERVATION'); }
  if (parsedOrigin.origin !== origin || parsedOrigin.protocol !== 'https:' || parsedOrigin.username || parsedOrigin.password || parsedOrigin.port) {
    throw new PurchaseRecipeError('Invalid observation.origin', 'INVALID_OBSERVATION');
  }
  const pathname = boundedText(input.pathname, 'observation.pathname', 1024);
  if (!pathname.startsWith('/') || pathname.includes('?') || pathname.includes('#')) {
    throw new PurchaseRecipeError('Invalid observation.pathname', 'INVALID_OBSERVATION');
  }
  if (!PAGE_STATES.has(input.pageState)) throw new PurchaseRecipeError('Invalid observation.pageState', 'INVALID_OBSERVATION');
  if (!OPTION_STATES.has(input.optionState)) throw new PurchaseRecipeError('Invalid observation.optionState', 'INVALID_OBSERVATION');
  if (!Array.isArray(input.semanticElements) || input.semanticElements.length > 200) {
    throw new PurchaseRecipeError('Invalid observation.semanticElements', 'INVALID_OBSERVATION');
  }
  const refs = new Set();
  const semanticElements = input.semanticElements.map((element, index) => {
    if (!record(element) || typeof element.visible !== 'boolean' || typeof element.enabled !== 'boolean') {
      throw new PurchaseRecipeError(`Invalid semanticElements[${index}]`, 'INVALID_OBSERVATION');
    }
    const candidateRef = identifier(element.candidateRef, `semanticElements[${index}].candidateRef`);
    if (refs.has(candidateRef)) throw new PurchaseRecipeError('Duplicate semantic element reference', 'INVALID_OBSERVATION');
    refs.add(candidateRef);
    const role = boundedText(element.role, `semanticElements[${index}].role`, 40).toLowerCase();
    if (!SEMANTIC_ROLES.has(role)) throw new PurchaseRecipeError('Unsupported semantic role', 'INVALID_OBSERVATION');
    return {
      candidateRef,
      role,
      name: boundedText(element.name, `semanticElements[${index}].name`, 200),
      ...(element.groupName === undefined ? {} : { groupName: boundedText(element.groupName, `semanticElements[${index}].groupName`, 120) }),
      visible: element.visible,
      enabled: element.enabled,
    };
  });
  let offer = null;
  if (input.offer !== null && input.offer !== undefined) {
    if (!record(input.offer)) throw new PurchaseRecipeError('Invalid observation.offer', 'INVALID_OBSERVATION');
    offer = {
      offerIdentity: normalizeOfferIdentity(input.offer.offerIdentity, 'observation.offer.offerIdentity'),
      quantity: quantity(input.offer.quantity, 'observation.offer.quantity'),
      unitPriceKrw: won(input.offer.unitPriceKrw, 'observation.offer.unitPriceKrw'),
    };
  }
  const inputCartLines = input.cartLines ?? [];
  if (!Array.isArray(inputCartLines) || inputCartLines.length > 100) {
    throw new PurchaseRecipeError('Invalid observation.cartLines', 'INVALID_OBSERVATION');
  }
  const lineRefs = new Set();
  const cartLines = inputCartLines.map((line, index) => {
    if (!record(line) || typeof line.selected !== 'boolean') {
      throw new PurchaseRecipeError(`Invalid cartLines[${index}]`, 'INVALID_OBSERVATION');
    }
    const lineRef = identifier(line.lineRef, `cartLines[${index}].lineRef`);
    if (lineRefs.has(lineRef)) throw new PurchaseRecipeError('Duplicate cart line reference', 'INVALID_OBSERVATION');
    lineRefs.add(lineRef);
    return {
      lineRef,
      offerIdentity: normalizeOfferIdentity(line.offerIdentity, `cartLines[${index}].offerIdentity`),
      quantity: quantity(line.quantity, `cartLines[${index}].quantity`),
      unitPriceKrw: won(line.unitPriceKrw, `cartLines[${index}].unitPriceKrw`),
      selected: line.selected,
    };
  });
  return { origin, pathname, pageState: input.pageState, optionState: input.optionState, semanticElements, offer, cartLines };
}

function matchesPageRule(recipe, pageState, observation) {
  const rule = recipe.pages[pageState];
  if (!rule) return false;
  let urlMatches;
  if (rule.originPaths) {
    urlMatches = rule.originPaths.some((entry) => entry.origin === observation.origin && entry.pathPatterns.some((pattern) => pattern.test(observation.pathname)));
  } else {
    urlMatches = rule.origins.includes(observation.origin) &&
      (!rule.pathPatterns || rule.pathPatterns.some((pattern) => pattern.test(observation.pathname)));
  }
  if (!urlMatches) return false;
  if (rule.requiredLocator) {
    return observation.semanticElements.some((element) => element.visible && locatorMatches(rule.requiredLocator, element));
  }
  return true;
}

function normalizedName(value) {
  return value.normalize('NFKC').replace(/\s+/g, ' ').trim();
}

function locatorMatches(locator, element) {
  if (locator.strategy !== 'role_and_accessible_name' || !locator.roles.includes(element.role)) return false;
  const name = normalizedName(element.name);
  const exact = locator.exactNames?.some((candidate) => normalizedName(candidate) === name) ?? false;
  const pattern = locator.namePatterns?.some((candidate) => candidate.test(name)) ?? false;
  if (!exact && !pattern) return false;
  if (locator.exactGroupNames) {
    if (!element.groupName) return false;
    const groupName = normalizedName(element.groupName);
    if (!locator.exactGroupNames.some((candidate) => normalizedName(candidate) === groupName)) return false;
  }
  return true;
}

function blocked(step, reasonCode) {
  return deepFreeze({ status: 'blocked', stage: step.stage, stepId: step.stepId, reasonCode, action: null });
}

function offerStateMatches(expected, observed, pathname, requireApprovedQuantity) {
  if (!observed || offerKey(expected) !== offerKey(observed.offerIdentity) ||
      (requireApprovedQuantity && expected.quantity !== observed.quantity)) return false;
  const pathProductId = pathname.match(COUPANG_PRODUCT_PATH)?.[0].split('/').filter(Boolean).at(-1);
  return pathProductId === expected.productId && observed.unitPriceKrw <= expected.unitPriceCeilingKrw &&
    observed.unitPriceKrw * expected.quantity <= expected.linePriceCeilingKrw;
}

function cartSelectionMatches(expectedLines, observedLines, totalPriceCeilingKrw) {
  const expected = new Map(expectedLines.map((line) => [offerKey(line), line.quantity]));
  const ceilings = new Map(expectedLines.map((line) => [offerKey(line), line]));
  const selected = observedLines.filter((line) => line.selected);
  if (expected.size !== expectedLines.length || selected.length !== expected.size) return false;
  const selectedKeys = new Set();
  let total = 0;
  for (const line of selected) {
    const key = offerKey(line.offerIdentity);
    if (selectedKeys.has(key)) return false;
    selectedKeys.add(key);
    const approved = ceilings.get(key);
    if (!approved || approved.quantity !== line.quantity || line.unitPriceKrw > approved.unitPriceCeilingKrw ||
        line.unitPriceKrw * line.quantity > approved.linePriceCeilingKrw) return false;
    total += line.unitPriceKrw * line.quantity;
  }
  return selectedKeys.size === expected.size &&
    [...expected.keys()].every((key) => selectedKeys.has(key)) &&
    total <= totalPriceCeilingKrw;
}

/**
 * Resolve one route step against a fresh native-origin and semantic-DOM
 * observation. Unknown, mismatched, unavailable, or ambiguous states return a
 * blocked result and never fall back to a generic click.
 */
export function resolvePurchasePreparationStep(input) {
  if (!record(input) || !Number.isSafeInteger(input.cursor) || input.cursor < 0) {
    throw new PurchaseRecipeError('Invalid route cursor');
  }
  const plan = buildPurchasePreparationPlan(input);
  if (input.cursor >= plan.steps.length) throw new PurchaseRecipeError('Route cursor is outside the plan');
  const recipe = getRecipe(plan.merchantId, plan.recipeVersion);
  const step = plan.steps[input.cursor];

  if (step.expectedPageState === null) {
    if (step.action.effect === PURCHASE_PREPARATION_EFFECT.HANDOFF) {
      return deepFreeze({
        status: 'handoff',
        stage: step.stage,
        stepId: step.stepId,
        reasonCode: step.action.reasonCode,
        action: step.action,
      });
    }
    return deepFreeze({ status: 'ready', stage: step.stage, stepId: step.stepId, action: step.action });
  }

  let observation;
  try { observation = normalizeObservation(input.observation); }
  catch (error) {
    if (error instanceof PurchaseRecipeError) return blocked(step, 'INVALID_OBSERVATION');
    throw error;
  }
  if (!recipe.allowedOrigins.includes(observation.origin)) return blocked(step, 'HOST_NOT_ALLOWED');
  if (observation.pageState === PAGE_STATE.UNKNOWN) return blocked(step, 'UNKNOWN_PAGE_STATE');
  if (observation.pageState !== step.expectedPageState) return blocked(step, 'PAGE_STATE_MISMATCH');
  if (!matchesPageRule(recipe, observation.pageState, observation)) return blocked(step, 'URL_STATE_MISMATCH');
  if (step.expectedOptionStates && !step.expectedOptionStates.includes(observation.optionState)) {
    return blocked(step, 'OPTIONS_NOT_READY');
  }
  if (step.expectedOffer && !offerStateMatches(step.expectedOffer, observation.offer, observation.pathname,
    step.requireApprovedQuantity === true)) {
    return blocked(step, 'OFFER_STATE_MISMATCH');
  }
  if (step.expectedApprovedLines && !cartSelectionMatches(step.expectedApprovedLines, observation.cartLines,
    step.action.bindings.approval.totalPriceCeilingKrw)) {
    return blocked(step, 'CART_SELECTION_MISMATCH');
  }

  if (step.action.effect === PURCHASE_PREPARATION_EFFECT.HANDOFF) {
    return deepFreeze({ status: 'handoff', stage: step.stage, stepId: step.stepId, reasonCode: step.action.reasonCode, action: step.action });
  }
  if (step.action.effect === PURCHASE_PREPARATION_EFFECT.VERIFY || step.action.effect === PURCHASE_PREPARATION_EFFECT.NAVIGATE) {
    return deepFreeze({ status: 'ready', stage: step.stage, stepId: step.stepId, action: step.action });
  }

  if (step.action.stepId === COUPANG_PURCHASE_STEP_ID.SET_QUANTITY) {
    const currentQuantity = observation.offer.quantity;
    const targetQuantity = step.expectedOffer.quantity;
    if (currentQuantity === targetQuantity) {
      const { locators: _locators, ...verifiedAction } = step.action;
      return deepFreeze({
        status: 'ready',
        stage: step.stage,
        stepId: step.stepId,
        stepComplete: true,
        action: {
          ...verifiedAction,
          effect: PURCHASE_PREPARATION_EFFECT.VERIFY,
          bindings: { ...verifiedAction.bindings, currentQuantity },
        },
      });
    }
    const direction = currentQuantity < targetQuantity ? 'increase' : 'decrease';
    const locator = step.action.locators[direction];
    const matchingQuantityControls = observation.semanticElements.filter((element) => locatorMatches(locator, element));
    const availableQuantityControls = matchingQuantityControls.filter((element) => element.visible && element.enabled);
    if (availableQuantityControls.length === 0) {
      return blocked(step, matchingQuantityControls.length > 0 ? 'CONTROL_UNAVAILABLE' : 'CONTROL_NOT_FOUND');
    }
    if (availableQuantityControls.length !== 1) return blocked(step, 'CONTROL_AMBIGUOUS');
    return deepFreeze({
      status: 'ready',
      stage: step.stage,
      stepId: step.stepId,
      stepComplete: false,
      action: {
        ...step.action,
        locator,
        bindings: {
          ...step.action.bindings,
          candidateRef: availableQuantityControls[0].candidateRef,
          currentQuantity,
          direction,
        },
      },
    });
  }

  const matching = observation.semanticElements.filter((element) => locatorMatches(step.action.locator, element));
  const available = matching.filter((element) => element.visible && element.enabled);
  if (available.length === 0) return blocked(step, matching.length > 0 ? 'CONTROL_UNAVAILABLE' : 'CONTROL_NOT_FOUND');
  if (available.length !== 1) return blocked(step, 'CONTROL_AMBIGUOUS');
  return deepFreeze({
    status: 'ready',
    stage: step.stage,
    stepId: step.stepId,
    action: {
      ...step.action,
      bindings: { ...step.action.bindings, candidateRef: available[0].candidateRef },
    },
  });
}
