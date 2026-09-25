import type { StyleProp, ViewStyle } from 'react-native';

export const VITLANE_BROWSER_PROTOCOL_VERSION = 1 as const;

export type BrowserControlMode = 'agent' | 'paused' | 'user' | 'stopped';
export type BrowserSecurityState = 'secure' | 'insecure' | 'internal' | 'unknown';

export type BrowserCapabilities = {
  adapter: 'ios-webkit';
  adapterVersion: string;
  observationMode: 'dom-projection';
  isolatedContentWorld: true;
  crossOriginFrames: false;
  screenshots: false;
  arbitraryJavaScript: false;
  cookieExport: false;
  /** Merchant cookies stay in the app-owned browser profile and are never exported. */
  sessionPersistence: 'app-scoped';
  /** Login, product review and user checkout use the same mounted browser profile. */
  sameSessionCheckout: true;
  openCandidateAutomation: false;
  supportedActions: ReadonlyArray<BrowserProposedAction['kind']>;
};

export type BrowserPageIdentity = {
  tabId: string;
  frameId: string;
  documentEpoch: number;
  observationId: string;
  topOrigin: string;
  frameOrigin: string;
  pathname: string;
  queryOrFragmentPresent: boolean;
};

export type BrowserSafeLinkCandidate = {
  candidateRef: string;
  nodeRef: string;
  kind: 'safe_link';
  role: string;
  name: string;
  href: string;
};

export type BrowserPublicSearchCandidate = {
  candidateRef: string;
  nodeRef: string;
  kind: 'public_search';
  role: 'searchbox';
  name: string;
};

export type SanitizedObservation = {
  observationId: string;
  nativeMetadata: {
    tabId: string;
    frameId: string;
    documentEpoch: number;
    topOrigin: string;
    frameOrigin: string;
    pathname: string;
    queryOrFragmentPresent: boolean;
    foreground: true;
  };
  untrustedPageData: {
    pageTypeHint: 'public' | 'search' | 'product' | 'listing' | 'unknown';
    /** Page-authored hint inferred without exposing account labels or values. */
    sessionStateHint: 'authenticated' | 'anonymous' | 'unknown';
    title: string;
    visibleText: string;
    candidates: Array<BrowserSafeLinkCandidate | BrowserPublicSearchCandidate>;
  };
  privacy: {
    inputValuesOmitted: true;
    secretsOmitted: true;
    screenshotIncluded: false;
    urlQueryAndFragmentOmitted: true;
    collectionStatus: 'sanitized' | 'handoff_required';
    handoffReasonCodes: Array<
      | 'AUTHENTICATION_REQUIRED'
      | 'SENSITIVE_INPUT_REQUIRED'
      | 'FORM_SUBMISSION_REQUIRED'
      | 'PAYMENT_OR_COMMITMENT'
      | 'UNSUPPORTED_INTERACTION'
      | 'CROSS_ORIGIN_FRAME'
      | 'PRIVATE_NETWORK_BLOCKED'
    >;
    excludedBoundaryCodes: Array<
      | 'FORM_SUBMISSION_REQUIRED'
      | 'PAYMENT_OR_COMMITMENT'
      | 'UNSUPPORTED_INTERACTION'
      | 'CROSS_ORIGIN_FRAME'
      | 'PRIVATE_NETWORK_BLOCKED'
    >;
  };
};

type BrowserActionBase = {
  page: BrowserPageIdentity;
  reason: string;
};

export type CoupangPreparationApproval = {
  approvalId: string;
  approvalDigest: string;
  currency: 'KRW';
  revision: number;
  expiresAt: string;
  totalPriceCeilingKrw: number;
};

export type CoupangApprovedLine = {
  productId: string;
  itemId: string;
  vendorItemId: string;
  quantity: number;
  unitPriceCeilingKrw: number;
  linePriceCeilingKrw: number;
};

export type CoupangApprovedPlanItem = {
  query: string;
  offerIdentity: Pick<CoupangApprovedLine, 'productId' | 'itemId' | 'vendorItemId'>;
  quantity: number;
  unitPriceCeilingKrw: number;
  linePriceCeilingKrw: number;
  productUrl: string;
  options:
    | { kind: 'none' }
    | { kind: 'choices'; choices: Array<{ groupName: string; valueName: string }> };
};

export type CoupangApprovedPreparationSnapshot = {
  merchantId: 'COUPANG';
  recipeVersion: '1';
  mode: 'single' | 'multi';
  route: 'single_buy_now' | 'multi_cart_checkout';
  approval: CoupangPreparationApproval;
  items: CoupangApprovedPlanItem[];
};

type CoupangProductPreparationBindings = CoupangApprovedLine & {
  approvedPreparation: CoupangApprovedPreparationSnapshot;
  approval: CoupangPreparationApproval;
  targetLineIndex: number;
  expectedOrigin: 'https://coupang.com' | 'https://www.coupang.com';
  expectedPath: string;
};

type CoupangOptionPreparationBindings = CoupangProductPreparationBindings & {
  targetOptionIndex: number;
  groupName: string;
  valueName: string;
};

type CoupangCartPreparationBindings = {
  approvedPreparation: CoupangApprovedPreparationSnapshot;
  approval: CoupangPreparationApproval;
  approvedLines: CoupangApprovedLine[];
  expectedOrigin: 'https://cart.coupang.com';
  expectedPath: string;
};

export type BrowserProposedAction =
  | (BrowserActionBase & { kind: 'inspect_page' })
  | (BrowserActionBase & { kind: 'open_candidate'; candidateRef: string })
  | (BrowserActionBase & { kind: 'scroll'; direction: 'up' | 'down' })
  | (BrowserActionBase & {
      kind: 'run_preparation_step';
      adapterId: 'builtin.public-search';
      recipeVersion: '1';
      stepId: 'prepare_query';
      bindings: { candidateRef: string; query: string };
    })
  | (BrowserActionBase & {
      kind: 'run_preparation_step';
      adapterId: 'builtin.coupang.purchase-preparation';
      recipeVersion: '1';
      stepId: 'select_option';
      bindings: CoupangOptionPreparationBindings;
    })
  | (BrowserActionBase & {
      kind: 'run_preparation_step';
      adapterId: 'builtin.coupang.purchase-preparation';
      recipeVersion: '1';
      stepId: 'verify_options' | 'set_quantity' | 'buy_now' | 'add_to_cart';
      bindings: CoupangProductPreparationBindings;
    })
  | (BrowserActionBase & {
      kind: 'run_preparation_step';
      adapterId: 'builtin.coupang.purchase-preparation';
      recipeVersion: '1';
      stepId: 'start_checkout';
      bindings: CoupangCartPreparationBindings;
    })
  | (BrowserActionBase & {
      kind: 'request_human';
      reasonCode:
        | 'AUTHENTICATION_REQUIRED'
        | 'SENSITIVE_INPUT_REQUIRED'
        | 'FORM_SUBMISSION_REQUIRED'
        | 'PAYMENT_OR_COMMITMENT'
        | 'UNSUPPORTED_INTERACTION'
        | 'CROSS_ORIGIN_FRAME'
        | 'PRIVATE_NETWORK_BLOCKED'
        | 'PAGE_CHANGED'
        | 'USER_DECISION_REQUIRED';
      message: string;
    })
  | (BrowserActionBase & { kind: 'finish'; message: string });

export type BrowserAuthorizedCommand = {
  protocolVersion: typeof VITLANE_BROWSER_PROTOCOL_VERSION;
  commandId: string;
  runId: string;
  deviceId: string;
  profileRef: string;
  sequence: number;
  leaseEpoch: number;
  controlGeneration: number;
  expiresAt: string;
  actionHash: string;
  action: BrowserProposedAction;
  serverPermit: string;
};

export type BrowserActionStatus = 'applied' | 'rejected' | 'handoff' | 'outcome_unknown';

export type BrowserActionResult = {
  commandId: string;
  status: BrowserActionStatus;
  code: string;
  nextObservationId?: string;
  evidenceRef?: string;
};

export type BrowserControlSnapshot = {
  mode: BrowserControlMode;
  runId: string | null;
  controlGeneration: number;
  foreground: boolean;
  pageReady: boolean;
  capabilities: BrowserCapabilities;
};

export type BrowserRunBinding = {
  runId: string;
  deviceId: string;
  profileRef: string;
  leaseEpoch: number;
};

export type BrowserPageIdentityEvent = {
  page: {
    tabId: string;
    frameId: string;
    documentEpoch: number;
    observationId: null;
    url: string;
    origin: string;
    title: string;
    foreground: boolean;
  };
  securityState: BrowserSecurityState;
};

export type BrowserControlEvent = BrowserControlSnapshot & {
  reason: string;
};

export type BrowserHandoffReason =
  | 'login'
  | 'verification'
  | 'payment'
  | 'final_action'
  | 'sensitive_page'
  | 'unsupported_page'
  | 'external_app'
  | 'unknown_result';

export type BrowserHandoffEvent = {
  reason: BrowserHandoffReason;
  message: string;
  origin: string;
  controlGeneration: number;
};

export type BrowserErrorEvent = {
  code: string;
  message: string;
  recoverable: boolean;
};

type NativeEvent<T> = (event: { nativeEvent: T }) => void;

export type VitlaneBrowserViewProps = {
  /** Initial top-level page. Only http(s) navigation is accepted. */
  url?: string;
  testID?: string;
  style?: StyleProp<ViewStyle>;
  onPageIdentity?: NativeEvent<BrowserPageIdentityEvent>;
  onControlStateChange?: NativeEvent<BrowserControlEvent>;
  onObservation?: NativeEvent<SanitizedObservation>;
  onActionResult?: NativeEvent<BrowserActionResult>;
  onHandoff?: NativeEvent<BrowserHandoffEvent>;
  onNavigationError?: NativeEvent<BrowserErrorEvent>;
};

export type VitlaneBrowserRef = {
  beginRun(binding: BrowserRunBinding): Promise<BrowserControlSnapshot>;
  observe(): Promise<SanitizedObservation>;
  execute(command: BrowserAuthorizedCommand): Promise<BrowserActionResult>;
  stopAgent(reason?: string): Promise<BrowserControlSnapshot>;
  takeOver(): Promise<BrowserControlSnapshot>;
  resumeAgent(): Promise<BrowserControlSnapshot>;
  goBack(): Promise<void>;
  reload(): Promise<void>;
};

export type BrowserCommandValidation =
  | { ok: true }
  | { ok: false; code: string; message: string };

const IDENTIFIER_PATTERN = /^[A-Za-z0-9][A-Za-z0-9._:-]{0,127}$/;
const COMMAND_KEYS = [
  'protocolVersion',
  'commandId',
  'runId',
  'deviceId',
  'profileRef',
  'sequence',
  'leaseEpoch',
  'controlGeneration',
  'expiresAt',
  'actionHash',
  'action',
  'serverPermit',
] as const;
const PAGE_KEYS = [
  'tabId',
  'frameId',
  'documentEpoch',
  'observationId',
  'topOrigin',
  'frameOrigin',
  'pathname',
  'queryOrFragmentPresent',
] as const;

function hasExactKeys(value: object, expected: ReadonlyArray<string>): boolean {
  const actual = Object.keys(value).sort();
  const sortedExpected = [...expected].sort();
  return actual.length === sortedExpected.length && actual.every((key, index) => key === sortedExpected[index]);
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return value !== null && typeof value === 'object' && !Array.isArray(value);
}

function boundedString(value: unknown, minimum: number, maximum: number): value is string {
  return (
    typeof value === 'string' &&
    value.length >= minimum &&
    value.length <= maximum &&
    !/[\u0000-\u0008\u000b\u000c\u000e-\u001f]/.test(value)
  );
}

const COUPANG_ID_PATTERN = /^[1-9][0-9]{0,19}$/;
const COUPANG_PRODUCT_PATH_PATTERN = /^\/vp\/products\/[1-9][0-9]*\/?$/;
const COUPANG_PRODUCT_ORIGINS = new Set(['https://coupang.com', 'https://www.coupang.com']);

function boundedInteger(value: unknown, minimum: number, maximum: number): value is number {
  return Number.isSafeInteger(value) && (value as number) >= minimum && (value as number) <= maximum;
}

function isCoupangApproval(value: unknown, now: number): value is CoupangPreparationApproval {
  if (!isRecord(value) || !hasExactKeys(value, [
    'approvalId', 'approvalDigest', 'currency', 'revision', 'expiresAt', 'totalPriceCeilingKrw',
  ])) return false;
  const expiresAt = typeof value.expiresAt === 'string' ? Date.parse(value.expiresAt) : Number.NaN;
  return typeof value.approvalId === 'string' && IDENTIFIER_PATTERN.test(value.approvalId) &&
    typeof value.approvalDigest === 'string' && /^sha256:[a-f0-9]{64}$/.test(value.approvalDigest) &&
    value.currency === 'KRW' && boundedInteger(value.revision, 1, Number.MAX_SAFE_INTEGER) &&
    typeof value.expiresAt === 'string' && Number.isFinite(expiresAt) &&
    new Date(expiresAt).toISOString() === value.expiresAt && expiresAt > now && expiresAt - now <= 10 * 60_000 &&
    boundedInteger(value.totalPriceCeilingKrw, 1, 1_000_000_000_000);
}

function isCoupangLine(value: unknown): value is CoupangApprovedLine {
  return isRecord(value) && hasExactKeys(value, [
    'productId', 'itemId', 'vendorItemId', 'quantity', 'unitPriceCeilingKrw', 'linePriceCeilingKrw',
  ]) && [value.productId, value.itemId, value.vendorItemId].every(
    part => typeof part === 'string' && COUPANG_ID_PATTERN.test(part),
  ) && boundedInteger(value.quantity, 1, 99) &&
    boundedInteger(value.unitPriceCeilingKrw, 1, 1_000_000_000_000) &&
    boundedInteger(value.linePriceCeilingKrw, 1, 1_000_000_000_000) &&
    value.linePriceCeilingKrw >= value.unitPriceCeilingKrw * value.quantity;
}

function isCoupangPlanItem(value: unknown): value is CoupangApprovedPlanItem {
  if (!isRecord(value) || !hasExactKeys(value, [
    'query', 'offerIdentity', 'quantity', 'unitPriceCeilingKrw', 'linePriceCeilingKrw', 'productUrl', 'options',
  ]) || !boundedString(value.query, 1, 160) || !isRecord(value.offerIdentity) ||
      !hasExactKeys(value.offerIdentity, ['productId', 'itemId', 'vendorItemId'])) return false;
  const line = {
    ...value.offerIdentity,
    quantity: value.quantity,
    unitPriceCeilingKrw: value.unitPriceCeilingKrw,
    linePriceCeilingKrw: value.linePriceCeilingKrw,
  };
  if (!isCoupangLine(line) || typeof value.productUrl !== 'string') return false;
  try {
    const url = new URL(value.productUrl);
    if (!COUPANG_PRODUCT_ORIGINS.has(url.origin) || !COUPANG_PRODUCT_PATH_PATTERN.test(url.pathname) ||
        url.pathname.replace(/\/$/, '').split('/').pop() !== line.productId ||
        url.searchParams.get('itemId') !== line.itemId || url.searchParams.get('vendorItemId') !== line.vendorItemId) return false;
  } catch { return false; }
  if (!isRecord(value.options)) return false;
  if (value.options.kind === 'none') return hasExactKeys(value.options, ['kind']);
  if (value.options.kind !== 'choices' || !hasExactKeys(value.options, ['kind', 'choices']) ||
      !Array.isArray(value.options.choices) || value.options.choices.length < 1 || value.options.choices.length > 10) return false;
  if (!value.options.choices.every(choice => isRecord(choice) && hasExactKeys(choice, ['groupName', 'valueName']) &&
    boundedString(choice.groupName, 1, 120) && boundedString(choice.valueName, 1, 120))) return false;
  return new Set(value.options.choices.map(choice => choice.groupName)).size === value.options.choices.length;
}

function isCoupangSnapshot(
  value: unknown,
  actionApproval: unknown,
  now: number,
): value is CoupangApprovedPreparationSnapshot {
  if (!isRecord(value) || !hasExactKeys(value, [
    'merchantId', 'recipeVersion', 'mode', 'route', 'approval', 'items',
  ]) || value.merchantId !== 'COUPANG' || value.recipeVersion !== '1' ||
      !['single', 'multi'].includes(String(value.mode)) ||
      value.route !== (value.mode === 'single' ? 'single_buy_now' : 'multi_cart_checkout') ||
      !isCoupangApproval(value.approval, now) || JSON.stringify(value.approval) !== JSON.stringify(actionApproval) ||
      !Array.isArray(value.items) || value.items.length < 1 || value.items.length > 20 ||
      (value.mode === 'single' ? value.items.length !== 1 : value.items.length < 2) ||
      !value.items.every(isCoupangPlanItem)) return false;
  const keys = value.items.map(item => `${item.offerIdentity.productId}:${item.offerIdentity.itemId}:${item.offerIdentity.vendorItemId}`);
  const total = value.items.reduce((sum, item) => sum + item.linePriceCeilingKrw, 0);
  return new Set(keys).size === keys.length && total <= value.approval.totalPriceCeilingKrw;
}

function validateCoupangPreparationAction(action: Extract<BrowserProposedAction, {
  kind: 'run_preparation_step'; adapterId: 'builtin.coupang.purchase-preparation';
}>, now: number): boolean {
  const rawBindings: unknown = action.bindings;
  if (action.recipeVersion !== '1' || !isRecord(rawBindings)) return false;
  const bindings: Record<string, unknown> = rawBindings;
  const productKeys = [
    'approvedPreparation', 'approval', 'targetLineIndex', 'productId', 'itemId', 'vendorItemId',
    'quantity', 'unitPriceCeilingKrw', 'linePriceCeilingKrw', 'expectedOrigin', 'expectedPath',
  ];
  if (action.stepId === 'start_checkout') {
    if (!hasExactKeys(bindings, ['approvedPreparation', 'approval', 'approvedLines', 'expectedOrigin', 'expectedPath']) ||
        !isCoupangApproval(bindings.approval, now) ||
        !isCoupangSnapshot(bindings.approvedPreparation, bindings.approval, now) ||
        bindings.approvedPreparation.mode !== 'multi' || !Array.isArray(bindings.approvedLines) ||
        bindings.approvedLines.length !== bindings.approvedPreparation.items.length ||
        !bindings.approvedLines.every(isCoupangLine) ||
        bindings.expectedOrigin !== 'https://cart.coupang.com' ||
        bindings.expectedPath !== '/cartView.pang') return false;
    const expected = bindings.approvedPreparation.items.map(item => ({
      ...item.offerIdentity, quantity: item.quantity,
      unitPriceCeilingKrw: item.unitPriceCeilingKrw, linePriceCeilingKrw: item.linePriceCeilingKrw,
    }));
    return JSON.stringify(bindings.approvedLines) === JSON.stringify(expected);
  }
  const expectedKeys = action.stepId === 'select_option'
    ? [...productKeys, 'targetOptionIndex', 'groupName', 'valueName']
    : productKeys;
  if (!hasExactKeys(bindings, expectedKeys) || !isCoupangApproval(bindings.approval, now) ||
      !isCoupangSnapshot(bindings.approvedPreparation, bindings.approval, now) ||
      !boundedInteger(bindings.targetLineIndex, 0, bindings.approvedPreparation.items.length - 1) ||
      !isCoupangLine({
        productId: bindings.productId, itemId: bindings.itemId, vendorItemId: bindings.vendorItemId,
        quantity: bindings.quantity, unitPriceCeilingKrw: bindings.unitPriceCeilingKrw,
        linePriceCeilingKrw: bindings.linePriceCeilingKrw,
      }) || !COUPANG_PRODUCT_ORIGINS.has(String(bindings.expectedOrigin)) ||
      typeof bindings.expectedPath !== 'string' || !COUPANG_PRODUCT_PATH_PATTERN.test(bindings.expectedPath)) return false;
  const item = bindings.approvedPreparation.items[bindings.targetLineIndex];
  if (!item) return false;
  const expected = {
    ...item.offerIdentity, quantity: item.quantity,
    unitPriceCeilingKrw: item.unitPriceCeilingKrw, linePriceCeilingKrw: item.linePriceCeilingKrw,
  };
  if (JSON.stringify(expected) !== JSON.stringify({
    productId: bindings.productId, itemId: bindings.itemId, vendorItemId: bindings.vendorItemId,
    quantity: bindings.quantity, unitPriceCeilingKrw: bindings.unitPriceCeilingKrw,
    linePriceCeilingKrw: bindings.linePriceCeilingKrw,
  }) || bindings.expectedPath.replace(/\/$/, '').split('/').pop() !== bindings.productId ||
      (action.stepId === 'buy_now' && bindings.approvedPreparation.mode !== 'single') ||
      (action.stepId === 'add_to_cart' && bindings.approvedPreparation.mode !== 'multi')) return false;
  return action.stepId !== 'select_option' || (
    boundedInteger(bindings.targetOptionIndex, 0, 9) && boundedString(bindings.groupName, 1, 120) &&
    boundedString(bindings.valueName, 1, 120) && item.options.kind === 'choices' &&
    JSON.stringify(item.options.choices[bindings.targetOptionIndex]) === JSON.stringify({
      groupName: bindings.groupName, valueName: bindings.valueName,
    })
  );
}

export function isLikelySensitivePublicSearchQuery(value: string): boolean {
  const text = value.normalize('NFKC');
  if (
    /\b(?:password|passwd|passcode|otp|one[- ]?time|cvv|cvc|card\s*number|access[_ -]?token|api[_ -]?key|secret)\b/i.test(
      text,
    ) ||
    /(?:비밀번호|인증번호|일회용\s*코드|카드\s*번호|보안\s*코드|주민등록)/.test(text) ||
    /\b[A-Z0-9._%+-]+@[A-Z0-9.-]+\.[A-Z]{2,}\b/i.test(text) ||
    /https?:\/\//i.test(text)
  ) {
    return true;
  }
  return text.replace(/[^0-9]/g, '').length >= 11;
}

export function validateBrowserCommand(
  command: BrowserAuthorizedCommand,
  now = Date.now(),
): BrowserCommandValidation {
  if (!command || typeof command !== 'object' || !hasExactKeys(command, COMMAND_KEYS)) {
    return { ok: false, code: 'invalid_envelope', message: 'The command envelope contains unexpected fields.' };
  }
  if (command.protocolVersion !== VITLANE_BROWSER_PROTOCOL_VERSION) {
    return { ok: false, code: 'protocol_mismatch', message: 'Unsupported browser protocol.' };
  }
  if (
    ![command.commandId, command.runId, command.deviceId, command.profileRef].every(value =>
      IDENTIFIER_PATTERN.test(value),
    ) ||
    !Number.isSafeInteger(command.sequence) ||
    command.sequence < 1 ||
    !Number.isSafeInteger(command.leaseEpoch) ||
    command.leaseEpoch < 1
  ) {
    return { ok: false, code: 'invalid_envelope', message: 'The command envelope is incomplete.' };
  }
  if (!Number.isSafeInteger(command.controlGeneration) || command.controlGeneration < 0) {
    return { ok: false, code: 'invalid_generation', message: 'The control generation is invalid.' };
  }
  const expiresAt = Date.parse(command.expiresAt);
  if (
    !/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3}Z$/.test(command.expiresAt) ||
    !Number.isFinite(expiresAt) ||
    new Date(expiresAt).toISOString() !== command.expiresAt ||
    expiresAt <= now ||
    expiresAt - now > 60_000
  ) {
    return { ok: false, code: 'expired', message: 'The command expiry is invalid.' };
  }
  if (!command.action || typeof command.action !== 'object') {
    return { ok: false, code: 'policy_denied', message: 'The action is invalid.' };
  }
  const action = command.action;
  const page = action.page;
  if (
    !page ||
    typeof page !== 'object' ||
    !hasExactKeys(page, PAGE_KEYS) ||
    ![page.tabId, page.frameId, page.observationId].every(value => IDENTIFIER_PATTERN.test(value)) ||
    !Number.isSafeInteger(page.documentEpoch) ||
    page.documentEpoch < 0 ||
    !page.topOrigin ||
    page.frameOrigin !== page.topOrigin ||
    typeof page.pathname !== 'string' ||
    !/^\/[^?#]{0,1023}$/.test(page.pathname) ||
    typeof page.queryOrFragmentPresent !== 'boolean'
  ) {
    return { ok: false, code: 'invalid_page', message: 'A fresh main-frame observation is required.' };
  }
  if (!/^[a-f0-9]{64}$/.test(command.actionHash)) {
    return { ok: false, code: 'invalid_action_hash', message: 'The action hash is invalid.' };
  }
  if (!/^hmac-sha256:[A-Za-z0-9_-]{43}$/.test(command.serverPermit)) {
    return { ok: false, code: 'invalid_server_permit', message: 'The server permit is invalid.' };
  }
  if (!boundedString(action.reason, 0, 500)) {
    return { ok: false, code: 'policy_denied', message: 'The action reason is invalid.' };
  }
  switch (action.kind) {
    case 'inspect_page':
      if (!hasExactKeys(action, ['kind', 'page', 'reason'])) {
        return { ok: false, code: 'policy_denied', message: 'The inspect action is invalid.' };
      }
      break;
    case 'open_candidate':
      if (
        !hasExactKeys(action, ['kind', 'page', 'candidateRef', 'reason']) ||
        !IDENTIFIER_PATTERN.test(action.candidateRef)
      ) {
        return { ok: false, code: 'policy_denied', message: 'The link action is invalid.' };
      }
      break;
    case 'scroll':
      if (!hasExactKeys(action, ['kind', 'page', 'direction', 'reason']) || !['up', 'down'].includes(action.direction)) {
        return { ok: false, code: 'policy_denied', message: 'The scroll action is invalid.' };
      }
      break;
    case 'run_preparation_step': {
      const bindings: unknown = action.bindings;
      if (!hasExactKeys(action, ['kind', 'page', 'adapterId', 'recipeVersion', 'stepId', 'bindings', 'reason'])) {
        return { ok: false, code: 'policy_denied', message: 'The preparation action contains unexpected fields.' };
      }
      if (action.adapterId === 'builtin.coupang.purchase-preparation') {
        if (!validateCoupangPreparationAction(action, now)) {
          return { ok: false, code: 'policy_denied', message: 'The merchant preparation approval is invalid.' };
        }
        break;
      }
      if (action.adapterId !== 'builtin.public-search' || action.recipeVersion !== '1' ||
          action.stepId !== 'prepare_query' || !isRecord(bindings) ||
          !hasExactKeys(bindings, ['candidateRef', 'query']) || !boundedString(bindings.candidateRef, 1, 128) ||
          !IDENTIFIER_PATTERN.test(bindings.candidateRef) || !boundedString(bindings.query, 1, 160) ||
          bindings.query !== bindings.query.trim() || isLikelySensitivePublicSearchQuery(bindings.query)) {
        return { ok: false, code: 'policy_denied', message: 'The public search preparation is invalid.' };
      }
      break;
    }
    case 'request_human': {
      const allowedReasons = new Set([
        'AUTHENTICATION_REQUIRED',
        'SENSITIVE_INPUT_REQUIRED',
        'FORM_SUBMISSION_REQUIRED',
        'PAYMENT_OR_COMMITMENT',
        'UNSUPPORTED_INTERACTION',
        'CROSS_ORIGIN_FRAME',
        'PRIVATE_NETWORK_BLOCKED',
        'PAGE_CHANGED',
        'USER_DECISION_REQUIRED',
      ]);
      if (
        !hasExactKeys(action, ['kind', 'page', 'reasonCode', 'message', 'reason']) ||
        !allowedReasons.has(action.reasonCode) ||
        !boundedString(action.message, 1, 1000)
      ) {
        return { ok: false, code: 'policy_denied', message: 'The handoff action is invalid.' };
      }
      break;
    }
    case 'finish':
      if (
        !hasExactKeys(action, ['kind', 'page', 'message', 'reason']) ||
        !boundedString(action.message, 1, 4000)
      ) {
        return { ok: false, code: 'policy_denied', message: 'The finish action is invalid.' };
      }
      break;
    default:
      return { ok: false, code: 'policy_denied', message: 'This action is not allowlisted.' };
  }
  return { ok: true };
}
