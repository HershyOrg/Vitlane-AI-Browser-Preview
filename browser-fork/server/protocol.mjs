import net from 'node:net';
import { createHash } from 'node:crypto';
import {
  buildPurchasePreparationPlan,
  COUPANG_PURCHASE_ADAPTER_ID,
  COUPANG_PURCHASE_STEP_ID,
  normalizePurchasePreparationPlanInput,
  PurchaseRecipeError,
} from './purchase-recipes.mjs';

export class RequestError extends Error {
  constructor(message, status = 400, code = 'INVALID_REQUEST') {
    super(message);
    this.status = status;
    this.code = code;
  }
}

const record = (value) => value !== null && typeof value === 'object' && !Array.isArray(value);

function string(value, name, max = 4000) {
  if (typeof value !== 'string' || !value.trim() || value.length > max || /[\u0000-\u0008\u000b\u000c\u000e-\u001f]/.test(value)) {
    throw new RequestError(`Invalid ${name}`);
  }
  return value.trim();
}

function identifier(value, name, max = 128) {
  const result = string(value, name, max);
  if (!/^[A-Za-z0-9][A-Za-z0-9._:-]*$/.test(result)) throw new RequestError(`Invalid ${name}`);
  return result;
}

function integer(value, name, { min = 0, max = Number.MAX_SAFE_INTEGER } = {}) {
  if (!Number.isSafeInteger(value) || value < min || value > max) throw new RequestError(`Invalid ${name}`);
  return value;
}

function pathname(value, name = 'pathname') {
  const result = string(value, name, 1024);
  if (!result.startsWith('/') || result.includes('?') || result.includes('#')) {
    throw new RequestError(`Invalid ${name}`);
  }
  return result;
}

function exactKeys(value, keys, name) {
  if (!record(value) || Object.keys(value).length !== keys.length || keys.some((key) => !(key in value))) {
    throw new RequestError(`Invalid ${name}`);
  }
}

function privateIpv4(hostname) {
  const parts = hostname.split('.').map(Number);
  if (parts.length !== 4 || parts.some((part) => !Number.isInteger(part) || part < 0 || part > 255)) return true;
  const [a, b] = parts;
  return a === 0 || a === 10 || a === 127 || a >= 224 ||
    (a === 100 && b >= 64 && b <= 127) ||
    (a === 169 && b === 254) ||
    (a === 172 && b >= 16 && b <= 31) ||
    (a === 192 && (b === 0 || b === 168)) ||
    (a === 198 && (b === 18 || b === 19)) ||
    (a === 198 && b === 51) ||
    (a === 203 && b === 0);
}

function privateIpv6(hostname) {
  const host = hostname.replace(/^\[|\]$/g, '').toLowerCase();
  // Leading :: includes loopback, unspecified, IPv4-compatible and
  // IPv4-mapped forms. v1 rejects that whole ambiguous range.
  if (host.startsWith('::')) return true;
  if (/^(?:0|fc|fd|fe[89ab]|ff)/.test(host)) return true;
  if (/^2001:(?:2:|10:|db8:)/.test(host)) return true;
  const mapped = host.match(/^(?:::ffff:)?(\d+\.\d+\.\d+\.\d+)$/);
  return Boolean(mapped && privateIpv4(mapped[1]));
}

export function isPublicHostname(hostname) {
  const host = String(hostname).replace(/^\[|\]$/g, '').toLowerCase().replace(/\.$/, '');
  if (!host || host === 'localhost' || !host.includes('.') ||
      /(?:^|\.)(?:localhost|local|internal|lan|home\.arpa|test|invalid|example)$/.test(host)) return false;
  const family = net.isIP(host);
  if (family === 4) return !privateIpv4(host);
  if (family === 6) return !privateIpv6(host);
  return true;
}

/**
 * Normalizes a URL that may be opened by the local browser. It deliberately
 * rejects credentials, non-web schemes, private/reserved hosts, non-default
 * ports, and (by default) query/fragment data that could carry secrets.
 */
export function publicWebUrl(value, { allowQuery = false } = {}) {
  string(value, 'URL', 4096);
  let url;
  try { url = new URL(value); } catch { throw new RequestError('Invalid URL', 400, 'URL_POLICY_DENIED'); }
  if (!['https:', 'http:'].includes(url.protocol) || url.username || url.password || url.port ||
      !isPublicHostname(url.hostname) || (!allowQuery && (url.search || url.hash))) {
    throw new RequestError('URL is outside the public web policy', 400, 'URL_POLICY_DENIED');
  }
  return url.href;
}

export function publicOrigin(value, name = 'origin') {
  const origin = string(value, name, 512);
  let parsed;
  try { parsed = new URL(origin); } catch { throw new RequestError(`Invalid ${name}`); }
  if ((origin !== parsed.origin && origin !== `${parsed.origin}/`) || parsed.origin === 'null') {
    throw new RequestError(`Invalid ${name}`);
  }
  publicWebUrl(`${parsed.origin}/`);
  return parsed.origin;
}

function likelySensitiveText(value) {
  const text = value.normalize('NFKC');
  if (/\b(?:password|passwd|passcode|otp|one[- ]?time|cvv|cvc|card\s*number|access[_ -]?token|api[_ -]?key|secret)\b/i.test(text)) return true;
  if (/(?:비밀번호|인증번호|일회용\s*코드|카드\s*번호|보안\s*코드|주민등록)/.test(text)) return true;
  if (/\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/i.test(text)) return true;
  const digits = text.replace(/[^0-9]/g, '');
  return digits.length >= 11;
}

function pageIdentity(observation) {
  const metadata = observation.nativeMetadata;
  return {
    tabId: metadata.tabId,
    frameId: metadata.frameId,
    documentEpoch: metadata.documentEpoch,
    observationId: observation.observationId,
    topOrigin: metadata.topOrigin,
    frameOrigin: metadata.frameOrigin,
    pathname: metadata.pathname,
    queryOrFragmentPresent: metadata.queryOrFragmentPresent,
  };
}

const PAGE_TYPES = new Set(['public', 'search', 'product', 'listing', 'unknown']);
const BOUNDARY_CODES = new Set([
  'AUTHENTICATION_REQUIRED',
  'SENSITIVE_INPUT_REQUIRED',
  'FORM_SUBMISSION_REQUIRED',
  'PAYMENT_OR_COMMITMENT',
  'UNSUPPORTED_INTERACTION',
  'CROSS_ORIGIN_FRAME',
  'PRIVATE_NETWORK_BLOCKED',
]);
const EXCLUDABLE_BOUNDARY_CODES = new Set([
  'FORM_SUBMISSION_REQUIRED',
  'PAYMENT_OR_COMMITMENT',
  'UNSUPPORTED_INTERACTION',
  'CROSS_ORIGIN_FRAME',
  'PRIVATE_NETWORK_BLOCKED',
]);

export function validateObservation(input) {
  if (!record(input)) throw new RequestError('Observation is required');
  const observationId = identifier(input.observationId, 'observationId', 128);
  const native = input.nativeMetadata;
  if (!record(native)) throw new RequestError('nativeMetadata is required');
  exactKeys(native, [
    'tabId', 'frameId', 'documentEpoch', 'topOrigin', 'frameOrigin', 'pathname',
    'queryOrFragmentPresent', 'foreground',
  ], 'nativeMetadata');
  if (typeof native.queryOrFragmentPresent !== 'boolean') {
    throw new RequestError('Invalid nativeMetadata.queryOrFragmentPresent');
  }
  const nativeMetadata = {
    tabId: identifier(native.tabId, 'nativeMetadata.tabId', 128),
    frameId: identifier(native.frameId, 'nativeMetadata.frameId', 128),
    documentEpoch: integer(native.documentEpoch, 'nativeMetadata.documentEpoch'),
    topOrigin: publicOrigin(native.topOrigin, 'nativeMetadata.topOrigin'),
    frameOrigin: publicOrigin(native.frameOrigin, 'nativeMetadata.frameOrigin'),
    pathname: pathname(native.pathname, 'nativeMetadata.pathname'),
    queryOrFragmentPresent: native.queryOrFragmentPresent,
    foreground: native.foreground === true,
  };
  if (!nativeMetadata.foreground) {
    throw new RequestError('Browser must be foreground', 409, 'NOT_FOREGROUND');
  }
  if (nativeMetadata.topOrigin !== nativeMetadata.frameOrigin) {
    throw new RequestError('Cross-origin frames require human control', 409, 'HUMAN_REQUIRED');
  }

  const data = input.untrustedPageData;
  if (!record(data) || typeof data.title !== 'string' || data.title.length > 300 ||
      typeof data.visibleText !== 'string' || data.visibleText.length > 10000 ||
      !PAGE_TYPES.has(data.pageTypeHint) || !Array.isArray(data.candidates) || data.candidates.length > 120) {
    throw new RequestError('Invalid untrustedPageData');
  }
  const candidateRefs = new Set();
  const nodeRefs = new Set();
  const candidates = data.candidates.map((candidate) => {
    if (!record(candidate)) throw new RequestError('Invalid candidate');
    const kind = candidate.kind;
    if (!['safe_link', 'public_search'].includes(kind)) throw new RequestError('Unsupported candidate kind');
    const normalized = {
      candidateRef: identifier(candidate.candidateRef, 'candidateRef', 80),
      nodeRef: identifier(candidate.nodeRef, 'nodeRef', 80),
      kind,
      role: string(candidate.role, 'candidate.role', 40),
      name: typeof candidate.name === 'string' ? candidate.name.slice(0, 200) : '',
    };
    if (candidateRefs.has(normalized.candidateRef) || nodeRefs.has(normalized.nodeRef)) {
      throw new RequestError('Duplicate candidate reference');
    }
    candidateRefs.add(normalized.candidateRef);
    nodeRefs.add(normalized.nodeRef);
    if (kind === 'safe_link') {
      normalized.href = publicWebUrl(candidate.href);
      if (new URL(normalized.href).origin !== nativeMetadata.topOrigin) {
        throw new RequestError('Cross-origin candidates require human control', 409, 'HUMAN_REQUIRED');
      }
    }
    else if ('href' in candidate) throw new RequestError('Search candidates cannot contain href');
    return normalized;
  });

  const privacy = input.privacy;
  if (!record(privacy) || privacy.inputValuesOmitted !== true || privacy.secretsOmitted !== true ||
      privacy.screenshotIncluded !== false || privacy.urlQueryAndFragmentOmitted !== true ||
      !['sanitized', 'handoff_required'].includes(privacy.collectionStatus) ||
      !Array.isArray(privacy.handoffReasonCodes) || privacy.handoffReasonCodes.length > 12 ||
      !Array.isArray(privacy.excludedBoundaryCodes) || privacy.excludedBoundaryCodes.length > 12) {
    throw new RequestError('Invalid privacy declaration');
  }
  const handoffReasonCodes = [...new Set(privacy.handoffReasonCodes.map((code) => {
    if (!BOUNDARY_CODES.has(code)) throw new RequestError('Invalid handoff reason');
    return code;
  }))];
  const excludedBoundaryCodes = [...new Set(privacy.excludedBoundaryCodes.map((code) => {
    if (!EXCLUDABLE_BOUNDARY_CODES.has(code)) throw new RequestError('Invalid excluded boundary');
    return code;
  }))];
  if (privacy.collectionStatus === 'handoff_required' &&
      (data.pageTypeHint !== 'unknown' || data.title !== '' || data.visibleText !== '' ||
        candidates.length !== 0 || handoffReasonCodes.length === 0 ||
        excludedBoundaryCodes.length !== 0)) {
    throw new RequestError('Blocked observations must not contain page data');
  }
  if (privacy.collectionStatus === 'sanitized' && handoffReasonCodes.length !== 0) {
    throw new RequestError('Sanitized observations cannot carry handoff reasons');
  }
  const originBoundary = new Map([
    ['https://checkout.coupang.com', 'PAYMENT_OR_COMMITMENT'],
    ['https://login.coupang.com', 'AUTHENTICATION_REQUIRED'],
  ]).get(nativeMetadata.topOrigin);
  if (originBoundary && (privacy.collectionStatus !== 'handoff_required' ||
      !handoffReasonCodes.includes(originBoundary))) {
    throw new RequestError('This merchant origin requires human control', 409, 'HUMAN_REQUIRED');
  }

  return {
    observationId,
    nativeMetadata,
    untrustedPageData: {
      pageTypeHint: data.pageTypeHint,
      title: data.title,
      visibleText: data.visibleText,
      candidates,
    },
    privacy: {
      inputValuesOmitted: true,
      secretsOmitted: true,
      screenshotIncluded: false,
      urlQueryAndFragmentOmitted: true,
      collectionStatus: privacy.collectionStatus,
      handoffReasonCodes,
      excludedBoundaryCodes,
    },
  };
}

function validateCommandContext(input) {
  if (!record(input)) throw new RequestError('commandContext is required');
  return {
    runId: identifier(input.runId, 'commandContext.runId'),
    deviceId: identifier(input.deviceId, 'commandContext.deviceId'),
    profileRef: identifier(input.profileRef, 'commandContext.profileRef'),
    sequence: integer(input.sequence, 'commandContext.sequence', { min: 1 }),
    leaseEpoch: integer(input.leaseEpoch, 'commandContext.leaseEpoch', { min: 1 }),
    controlGeneration: integer(input.controlGeneration, 'commandContext.controlGeneration'),
  };
}

export function canonicalize(value) {
  if (value === null || typeof value === 'boolean' || typeof value === 'string') return JSON.stringify(value);
  if (typeof value === 'number' && Number.isFinite(value)) return JSON.stringify(value);
  if (Array.isArray(value)) return `[${value.map(canonicalize).join(',')}]`;
  if (record(value)) {
    return `{${Object.keys(value).sort().map((key) => `${JSON.stringify(key)}:${canonicalize(value[key])}`).join(',')}}`;
  }
  throw new TypeError('Value is not canonically serializable');
}

export function sha256(value) {
  return createHash('sha256').update(typeof value === 'string' ? value : canonicalize(value)).digest('hex');
}

export function validateStep(input) {
  if (!record(input) || input.protocolVersion !== 1) throw new RequestError('protocolVersion 1 is required');
  const goal = string(input.goal, 'goal', 4000);
  const observation = validateObservation(input.observation);
  const commandContext = validateCommandContext(input.commandContext);
  if (!Array.isArray(input.history) || input.history.length > 20) throw new RequestError('Invalid history');
  const history = input.history.map((entry) => {
    if (!record(entry)) throw new RequestError('Invalid history entry');
    const actionKind = identifier(entry.actionKind, 'history.actionKind', 64);
    if (!['inspect_page', 'open_candidate', 'scroll', 'run_preparation_step', 'request_human', 'finish'].includes(actionKind)) {
      throw new RequestError('Invalid history action');
    }
    const status = identifier(entry.status, 'history.status', 32);
    if (!['rejected', 'applied', 'outcome_unknown', 'handoff'].includes(status)) throw new RequestError('Invalid history status');
    return {
      sequence: integer(entry.sequence, 'history.sequence', { min: 1 }),
      actionKind,
      status,
      code: identifier(entry.code, 'history.code', 80),
    };
  });
  const result = { protocolVersion: 1, goal, observation, history, commandContext };
  if (input.approvedPreparation !== undefined) {
    result.approvedPreparation = validateApprovedPreparation(input.approvedPreparation, observation);
  }
  result.requestHash = sha256(result);
  return result;
}

export function toModelInput(step) {
  return {
    protocolVersion: 1,
    goal: step.goal,
    observation: step.observation,
    history: step.history,
  };
}

const COUPANG_EXECUTABLE_STEPS = new Set([
  COUPANG_PURCHASE_STEP_ID.SELECT_OPTION,
  COUPANG_PURCHASE_STEP_ID.VERIFY_OPTIONS,
  COUPANG_PURCHASE_STEP_ID.SET_QUANTITY,
  COUPANG_PURCHASE_STEP_ID.BUY_NOW,
  COUPANG_PURCHASE_STEP_ID.ADD_TO_CART,
  COUPANG_PURCHASE_STEP_ID.START_CHECKOUT,
]);

const COUPANG_PRODUCT_STEPS = new Set([
  COUPANG_PURCHASE_STEP_ID.SELECT_OPTION,
  COUPANG_PURCHASE_STEP_ID.VERIFY_OPTIONS,
  COUPANG_PURCHASE_STEP_ID.SET_QUANTITY,
  COUPANG_PURCHASE_STEP_ID.BUY_NOW,
  COUPANG_PURCHASE_STEP_ID.ADD_TO_CART,
]);

function expectedMerchantPage(raw, action, observation) {
  exactKeys(raw, ['origin', 'pathname'], 'approvedPreparation.expectedPage');
  const origin = publicOrigin(raw.origin, 'approvedPreparation.expectedPage.origin');
  const expectedPathname = pathname(raw.pathname, 'approvedPreparation.expectedPage.pathname');
  if (origin !== observation.nativeMetadata.topOrigin || origin !== observation.nativeMetadata.frameOrigin ||
      expectedPathname !== observation.nativeMetadata.pathname) {
    throw new RequestError('Approved preparation is not bound to the observed page', 400, 'APPROVAL_PAGE_MISMATCH');
  }
  if (COUPANG_PRODUCT_STEPS.has(action.stepId)) {
    const expectedPath = `/vp/products/${action.bindings.productId}`;
    if (!['https://coupang.com', 'https://www.coupang.com'].includes(origin) ||
        (expectedPathname !== expectedPath && expectedPathname !== `${expectedPath}/`)) {
      throw new RequestError('Approved product path does not match the offer', 400, 'APPROVAL_PAGE_MISMATCH');
    }
  } else if (action.stepId === COUPANG_PURCHASE_STEP_ID.START_CHECKOUT) {
    const allowed = origin === 'https://cart.coupang.com' &&
      expectedPathname === '/cartView.pang' &&
      observation.nativeMetadata.queryOrFragmentPresent === false;
    if (!allowed) throw new RequestError('Approved cart path is not supported', 400, 'APPROVAL_PAGE_MISMATCH');
  }
  return { origin, pathname: expectedPathname };
}

/**
 * Validates a native-created, separately user-approved recipe cursor. The
 * model never receives this value and cannot mint purchase authority.
 */
export function validateApprovedPreparation(input, observation) {
  exactKeys(input, [
    'merchantId', 'recipeVersion', 'mode', 'approval', 'items', 'cursor', 'expectedPage',
  ], 'approvedPreparation');
  const cursor = integer(input.cursor, 'approvedPreparation.cursor', { min: 0, max: 200 });
  let plan;
  try {
    const normalizedInput = normalizePurchasePreparationPlanInput({
      merchantId: input.merchantId,
      recipeVersion: input.recipeVersion,
      mode: input.mode,
      approval: input.approval,
      items: input.items,
    });
    plan = buildPurchasePreparationPlan(normalizedInput);
  } catch (error) {
    if (error instanceof PurchaseRecipeError) {
      throw new RequestError(error.message, 400, error.code);
    }
    throw error;
  }
  const step = plan.steps[cursor];
  if (!step || step.action.adapterId !== COUPANG_PURCHASE_ADAPTER_ID ||
      !COUPANG_EXECUTABLE_STEPS.has(step.action.stepId) ||
      !['activate', 'verify'].includes(step.action.effect)) {
    throw new RequestError('Approved cursor is not an executable merchant preparation step', 400, 'APPROVAL_STEP_DENIED');
  }
  const expectedPage = expectedMerchantPage(input.expectedPage, step.action, observation);
  return {
    cursor,
    merchantId: plan.merchantId,
    recipeVersion: plan.recipeVersion,
    mode: plan.mode,
    expectedPage,
    action: {
      adapterId: step.action.adapterId,
      recipeVersion: step.action.recipeVersion,
      stepId: step.action.stepId,
      bindings: { ...step.action.bindings, expectedOrigin: expectedPage.origin, expectedPath: expectedPage.pathname },
    },
  };
}

/** Builds the exact signed action from native approval, without model input. */
export function actionFromApprovedPreparation(step) {
  const approved = step.approvedPreparation;
  if (!approved) throw new RequestError('Approved preparation is required', 400, 'APPROVAL_REQUIRED');
  return {
    kind: 'run_preparation_step',
    page: pageIdentity(step.observation),
    adapterId: approved.action.adapterId,
    recipeVersion: approved.action.recipeVersion,
    stepId: approved.action.stepId,
    bindings: approved.action.bindings,
    reason: 'Explicit native user approval for a bounded Coupang preparation step',
  };
}

const HUMAN_REASON_CODES = new Set([
  ...BOUNDARY_CODES,
  'PAGE_CHANGED',
  'USER_DECISION_REQUIRED',
]);

export function validateProposedAction(raw, observation) {
  if (!record(raw)) throw new RequestError('Model returned an invalid action', 502, 'INVALID_MODEL_ACTION');
  const kind = string(raw.kind, 'action.kind', 40);
  const reason = string(raw.reason, 'action.reason', 500);
  const page = pageIdentity(observation);
  const handoffRequired = observation.privacy.collectionStatus === 'handoff_required';
  if (handoffRequired && kind !== 'request_human') {
    throw new RequestError('Sensitive page requires human control', 502, 'MODEL_POLICY_DENIED');
  }
  switch (kind) {
    case 'inspect_page':
      return { kind, page, reason };
    case 'open_candidate': {
      const candidateRef = identifier(raw.candidateRef, 'candidateRef', 80);
      const candidate = observation.untrustedPageData.candidates.find((item) => item.candidateRef === candidateRef);
      if (!candidate || candidate.kind !== 'safe_link') {
        throw new RequestError('Model selected an unavailable safe link', 502, 'INVALID_MODEL_ACTION');
      }
      const href = publicWebUrl(candidate.href);
      if (new URL(href).origin !== observation.nativeMetadata.topOrigin) {
        throw new RequestError('Cross-origin navigation requires human control', 502, 'MODEL_POLICY_DENIED');
      }
      return { kind, page, candidateRef, reason };
    }
    case 'scroll':
      if (!['up', 'down'].includes(raw.direction)) {
        throw new RequestError('Model returned an invalid scroll direction', 502, 'INVALID_MODEL_ACTION');
      }
      return { kind, page, direction: raw.direction, reason };
    case 'run_preparation_step': {
      if (raw.adapterId !== 'builtin.public-search' || raw.recipeVersion !== '1' || raw.stepId !== 'prepare_query') {
        throw new RequestError('Only the built-in public search preparation recipe is allowed', 502, 'MODEL_POLICY_DENIED');
      }
      const candidateRef = identifier(raw.candidateRef, 'candidateRef', 80);
      const candidate = observation.untrustedPageData.candidates.find((item) => item.candidateRef === candidateRef);
      if (!candidate || candidate.kind !== 'public_search') {
        throw new RequestError('Model selected an unavailable public search field', 502, 'INVALID_MODEL_ACTION');
      }
      const query = string(raw.query, 'query', 160);
      if (likelySensitiveText(query) || /https?:\/\//i.test(query)) {
        throw new RequestError('Public search query may contain sensitive data', 502, 'MODEL_POLICY_DENIED');
      }
      return {
        kind,
        page,
        adapterId: 'builtin.public-search',
        recipeVersion: '1',
        stepId: 'prepare_query',
        bindings: { candidateRef, query },
        reason,
      };
    }
    case 'request_human': {
      const reasonCode = identifier(raw.reasonCode, 'reasonCode', 80);
      if (!HUMAN_REASON_CODES.has(reasonCode)) {
        throw new RequestError('Invalid human handoff reason', 502, 'INVALID_MODEL_ACTION');
      }
      return { kind, page, reasonCode, message: string(raw.message, 'message', 1000), reason };
    }
    case 'finish':
      return { kind, page, message: string(raw.message, 'message', 4000), reason };
    default:
      throw new RequestError('Unsupported model action', 502, 'INVALID_MODEL_ACTION');
  }
}

/** Revalidates a provider adapter's output at the bridge policy boundary. */
export function validateProviderAction(raw, observation) {
  if (!record(raw)) throw new RequestError('Provider returned an invalid action', 502, 'INVALID_MODEL_ACTION');
  if (raw.kind === 'run_preparation_step' && record(raw.bindings)) {
    return validateProposedAction({
      kind: raw.kind,
      adapterId: raw.adapterId,
      recipeVersion: raw.recipeVersion,
      stepId: raw.stepId,
      candidateRef: raw.bindings.candidateRef,
      query: raw.bindings.query,
      reason: raw.reason,
    }, observation);
  }
  // Any provider-supplied page identity is intentionally discarded. The
  // trusted identity is always injected from the validated observation.
  return validateProposedAction(raw, observation);
}
