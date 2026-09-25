import { randomBytes, randomUUID } from 'node:crypto';

import { createCommandIssuer, verifyAuthorizedCommand } from '../../server/permit.mjs';
import {
  actionFromApprovedPreparation,
  validateStep,
} from '../../server/protocol.mjs';
import {
  buildPurchasePreparationPlan,
  computePurchaseApprovalDigest,
  PURCHASE_PREPARATION_EFFECT,
  PURCHASE_PREPARATION_MODE,
} from '../../server/purchase-recipes.mjs';

const COUPANG_PRODUCT_ORIGINS = new Set([
  'https://coupang.com',
  'https://www.coupang.com',
]);
const COUPANG_PRODUCT_PATH = /^\/vp\/products\/([1-9][0-9]{0,19})\/?$/;
const COUPANG_ID = /^[1-9][0-9]{0,19}$/;
const APPROVAL_TTL_MS = 5 * 60_000;
const EXPECTED_RESULT = Object.freeze({
  select_option: Object.freeze({ status: 'applied', code: 'COUPANG_OPTION_SELECTED' }),
  verify_options: Object.freeze({ status: 'applied', code: 'COUPANG_OPTIONS_VERIFIED' }),
  set_quantity: Object.freeze({ status: 'applied', code: 'COUPANG_QUANTITY_SET' }),
  buy_now: Object.freeze({ status: 'handoff', code: 'COUPANG_BUY_NOW_ACTIVATED' }),
});

export class LiveCoupangError extends Error {
  constructor(message, code = 'LIVE_COUPANG_FAILED', details = {}) {
    super(message);
    this.name = 'LiveCoupangError';
    this.code = code;
    Object.assign(this, details);
  }
}

function positiveInteger(value, name, { min = 1, max = Number.MAX_SAFE_INTEGER } = {}) {
  const parsed = typeof value === 'string' && /^[0-9]+$/.test(value) ? Number(value) : value;
  if (!Number.isSafeInteger(parsed) || parsed < min || parsed > max) {
    throw new LiveCoupangError(`${name} must be an integer from ${min} to ${max}`, 'INVALID_APPROVAL_INPUT');
  }
  return parsed;
}

function exactIdentityParameters(url) {
  const itemIds = url.searchParams.getAll('itemId');
  const vendorItemIds = url.searchParams.getAll('vendorItemId');
  if (itemIds.length !== 1 || vendorItemIds.length !== 1 ||
      !COUPANG_ID.test(itemIds[0]) || !COUPANG_ID.test(vendorItemIds[0])) {
    throw new LiveCoupangError(
      'The product URL must contain exactly one itemId and vendorItemId.',
      'PRODUCT_URL_IDENTITY_INVALID',
    );
  }
  return { itemId: itemIds[0], vendorItemId: vendorItemIds[0] };
}

/**
 * Accepts only a concrete Coupang offer URL. Tracking parameters are ignored,
 * while duplicate identity parameters, fragments, credentials, and alternate
 * hosts fail closed.
 */
export function parseCoupangProductUrl(value) {
  let url;
  try {
    url = new URL(value);
  } catch {
    throw new LiveCoupangError('Open a concrete Coupang product URL.', 'PRODUCT_URL_INVALID');
  }
  const pathMatch = url.pathname.match(COUPANG_PRODUCT_PATH);
  if (url.protocol !== 'https:' || url.username || url.password || url.port || url.hash ||
      !COUPANG_PRODUCT_ORIGINS.has(url.origin) || !pathMatch) {
    throw new LiveCoupangError('Open a supported Coupang product URL.', 'PRODUCT_URL_DENIED');
  }
  const { itemId, vendorItemId } = exactIdentityParameters(url);
  const productId = pathMatch[1];
  const canonical = new URL(`/vp/products/${productId}`, 'https://www.coupang.com');
  canonical.searchParams.set('itemId', itemId);
  canonical.searchParams.set('vendorItemId', vendorItemId);
  return Object.freeze({
    productUrl: canonical.href,
    offerIdentity: Object.freeze({ productId, itemId, vendorItemId }),
  });
}

/** Parse optional `group=value` lines from the local control form. */
export function parseOptionLines(value) {
  const lines = Array.isArray(value) ? value : String(value ?? '').split(/\r?\n/);
  const choices = [];
  const groups = new Set();
  for (const raw of lines) {
    const line = String(raw).normalize('NFKC').trim();
    if (!line) continue;
    const separator = line.indexOf('=');
    if (separator < 1 || separator !== line.lastIndexOf('=')) {
      throw new LiveCoupangError('Each option must use one group=value line.', 'OPTION_INPUT_INVALID');
    }
    const groupName = line.slice(0, separator).trim();
    const valueName = line.slice(separator + 1).trim();
    if (!groupName || !valueName || groupName.length > 120 || valueName.length > 120 || groups.has(groupName)) {
      throw new LiveCoupangError('Each option group needs one non-empty approved value.', 'OPTION_INPUT_INVALID');
    }
    groups.add(groupName);
    choices.push({ groupName, valueName });
  }
  if (choices.length > 10) {
    throw new LiveCoupangError('At most ten option groups are supported.', 'OPTION_INPUT_INVALID');
  }
  return choices.length ? { kind: 'choices', choices } : { kind: 'none' };
}

function frozenClone(value) {
  if (value && typeof value === 'object' && !Object.isFrozen(value)) {
    Object.freeze(value);
    for (const child of Object.values(value)) frozenClone(child);
  }
  return value;
}

/**
 * Creates the exact short-lived snapshot that the local approval screen shows.
 * The returned object is immutable and is passed unchanged to execution.
 */
export function buildSingleApproval({
  productUrl,
  quantity,
  unitPriceCeilingKrw,
  totalPriceCeilingKrw,
  options,
  now = Date.now(),
  approvalId = `approval_mac_${randomUUID()}`,
} = {}) {
  const parsed = parseCoupangProductUrl(productUrl);
  const approvedQuantity = positiveInteger(quantity, 'quantity', { max: 99 });
  const unitCeiling = positiveInteger(unitPriceCeilingKrw, 'unitPriceCeilingKrw', {
    max: 1_000_000_000_000,
  });
  const totalCeiling = positiveInteger(totalPriceCeilingKrw, 'totalPriceCeilingKrw', {
    max: 1_000_000_000_000,
  });
  if (!Number.isSafeInteger(now) || now <= 0 || totalCeiling < unitCeiling * approvedQuantity) {
    throw new LiveCoupangError(
      'The total price ceiling must cover the unit ceiling times quantity.',
      'PRICE_CEILING_INVALID',
    );
  }
  const normalizedOptions = options?.kind === 'choices'
    ? parseOptionLines(options.choices?.map((choice) => `${choice?.groupName ?? ''}=${choice?.valueName ?? ''}`))
    : options?.kind === 'none' ? { kind: 'none' } : (() => {
      throw new LiveCoupangError('Options must explicitly be none or approved choices.', 'OPTION_INPUT_INVALID');
    })();
  const item = {
    query: '쿠팡 상품',
    productUrl: parsed.productUrl,
    offerIdentity: { ...parsed.offerIdentity },
    quantity: approvedQuantity,
    unitPriceCeilingKrw: unitCeiling,
    linePriceCeilingKrw: totalCeiling,
    options: normalizedOptions,
  };
  const unsigned = {
    merchantId: 'COUPANG',
    recipeVersion: '1',
    mode: PURCHASE_PREPARATION_MODE.SINGLE,
    approval: {
      approvalId,
      currency: 'KRW',
      revision: 1,
      expiresAt: new Date(now + APPROVAL_TTL_MS).toISOString(),
      totalPriceCeilingKrw: totalCeiling,
    },
    items: [item],
  };
  const approvedInput = {
    ...unsigned,
    approval: {
      ...unsigned.approval,
      approvalDigest: computePurchaseApprovalDigest(unsigned),
    },
  };
  // Validate every bound field now, before the UI treats this as approvable.
  buildPurchasePreparationPlan(approvedInput);
  return frozenClone(approvedInput);
}

function pageIdentityMetadata(url, ids, documentEpoch) {
  const parsed = new URL(url);
  return {
    tabId: ids.tabId,
    frameId: ids.frameId,
    documentEpoch,
    topOrigin: parsed.origin,
    frameOrigin: parsed.origin,
    pathname: parsed.pathname,
    queryOrFragmentPresent: Boolean(parsed.search || parsed.hash),
    foreground: true,
  };
}

function evaluationValue(result, operation) {
  if (result?.exceptionDetails || !result?.result) {
    throw new LiveCoupangError(`The isolated page agent could not ${operation}.`, 'PAGE_AGENT_FAILED');
  }
  if (result.result.type === 'undefined') return undefined;
  if (!('value' in result.result)) {
    throw new LiveCoupangError(`The isolated page agent could not ${operation}.`, 'PAGE_AGENT_FAILED');
  }
  return result.result.value;
}

/** Creates the same isolated-world page-agent boundary used by browser tests. */
export async function createIsolatedAgent(page, source) {
  if (!page?.context || typeof page.context !== 'function' || typeof page.url !== 'function' ||
      typeof source !== 'string' || !source.trim()) {
    throw new LiveCoupangError('A live Playwright page and bundled page-agent source are required.', 'PAGE_AGENT_INVALID');
  }
  const session = await page.context().newCDPSession(page);
  let detached = false;
  try {
    const [{ frameTree }, { targetInfo }] = await Promise.all([
      session.send('Page.getFrameTree'),
      session.send('Target.getTargetInfo'),
    ]);
    if (!frameTree?.frame?.id || !targetInfo?.targetId) {
      throw new LiveCoupangError('The active Chrome target is unavailable.', 'PAGE_TARGET_INVALID');
    }
    const { executionContextId } = await session.send('Page.createIsolatedWorld', {
      frameId: frameTree.frame.id,
      worldName: 'vitlane-live-coupang-v1',
      grantUniveralAccess: false,
    });
    const evaluate = async (expression, operation) => {
      let result;
      try {
        result = await session.send('Runtime.evaluate', {
          expression,
          contextId: executionContextId,
          returnByValue: true,
          awaitPromise: true,
          userGesture: false,
        });
      } catch (cause) {
        throw new LiveCoupangError(
          `The isolated page agent could not ${operation}.`,
          'PAGE_AGENT_FAILED',
          { cause },
        );
      }
      return evaluationValue(result, operation);
    };
    await evaluate(source, 'initialize');
    const ids = {
      tabId: `tab_${targetInfo.targetId}`.slice(0, 128),
      frameId: `frame_${frameTree.frame.id}`.slice(0, 128),
    };
    return Object.freeze({
      metadata: (url, documentEpoch = 1) => pageIdentityMetadata(url, ids, documentEpoch),
      snapshot: (metadata) => evaluate(
        `globalThis.__laneAgent.snapshot(${JSON.stringify(metadata)})`,
        'create a sanitized observation',
      ),
      inspect: (command) => evaluate(
        `globalThis.__laneAgent.inspect(${JSON.stringify(command)})`,
        'inspect an approved command',
      ),
      execute: (command) => evaluate(
        `globalThis.__laneAgent.execute(${JSON.stringify(command)})`,
        'execute an approved command',
      ),
      detach: async () => {
        if (detached) return;
        detached = true;
        try { await session.detach(); } catch { /* the Buy now navigation may already destroy the target */ }
      },
    });
  } catch (error) {
    try { await session.detach(); } catch { /* target may already be gone */ }
    throw error;
  }
}

function sameOffer(left, right) {
  return left.productId === right.productId && left.itemId === right.itemId &&
    left.vendorItemId === right.vendorItemId;
}

function assertPinnedProductUrl(currentUrl, pinnedUrl, approvedItem) {
  if (currentUrl !== pinnedUrl) {
    throw new LiveCoupangError('The product tab changed after approval.', 'APPROVAL_PAGE_MISMATCH');
  }
  const parsed = parseCoupangProductUrl(currentUrl);
  if (parsed.productUrl !== approvedItem.productUrl || !sameOffer(parsed.offerIdentity, approvedItem.offerIdentity)) {
    throw new LiveCoupangError('The product offer no longer matches the approval.', 'APPROVAL_PAGE_MISMATCH');
  }
  return parsed;
}

function executableCursors(plan) {
  return plan.steps
    .map((step, cursor) => [step, cursor])
    .filter(([step]) => [PURCHASE_PREPARATION_EFFECT.ACTIVATE, PURCHASE_PREPARATION_EFFECT.VERIFY]
      .includes(step.action.effect));
}

/**
 * Executes the already-displayed single-item approval. There is no retry path:
 * once Buy now is attempted, any lost result remains outcome-unknown and the
 * controller detaches without looking at the destination page.
 */
export async function executeApprovedSingle({
  page,
  approvedInput,
  permitKey = randomBytes(32),
  pageAgentSource,
  onStep = async () => {},
} = {}) {
  const plan = buildPurchasePreparationPlan(approvedInput);
  if (plan.mode !== PURCHASE_PREPARATION_MODE.SINGLE || plan.items.length !== 1) {
    throw new LiveCoupangError('The Mac live harness accepts exactly one approved item.', 'SINGLE_ITEM_REQUIRED');
  }
  const steps = executableCursors(plan);
  if (steps.filter(([step]) => step.action.stepId === 'buy_now').length !== 1 ||
      steps.some(([step]) => !Object.hasOwn(EXPECTED_RESULT, step.action.stepId))) {
    throw new LiveCoupangError('The approved route is not the fixed single-item recipe.', 'RECIPE_SEQUENCE_INVALID');
  }
  const pinnedUrl = page.url();
  const parsed = assertPinnedProductUrl(pinnedUrl, pinnedUrl, plan.items[0]);
  const agent = await createIsolatedAgent(page, pageAgentSource);
  const issuer = createCommandIssuer({ permitKey });
  const runId = `run_mac_${randomUUID()}`;
  const contextBase = {
    runId,
    deviceId: 'mac_local_chrome',
    profileRef: 'coupang_mac_profile_v1',
    leaseEpoch: 1,
    controlGeneration: 0,
  };
  const history = [];
  let sequence = 1;
  let buyNowAttempted = false;
  try {
    for (const [recipeStep, cursor] of steps) {
      assertPinnedProductUrl(page.url(), pinnedUrl, plan.items[0]);
      const metadata = agent.metadata(pinnedUrl, 1);
      const observation = await agent.snapshot(metadata);
      const commandContext = { ...contextBase, sequence };
      const step = validateStep({
        protocolVersion: 1,
        goal: 'Prepare this exact approved Coupang item and hand control back at order review.',
        observation,
        history,
        commandContext,
        approvedPreparation: {
          merchantId: approvedInput.merchantId,
          recipeVersion: approvedInput.recipeVersion,
          mode: approvedInput.mode,
          approval: approvedInput.approval,
          items: approvedInput.items,
          cursor,
          expectedPage: {
            origin: new URL(pinnedUrl).origin,
            pathname: new URL(pinnedUrl).pathname,
          },
        },
      });
      const command = await issuer.authorize(step, async () => actionFromApprovedPreparation(step));
      const verified = verifyAuthorizedCommand(command, {
        permitKey,
        expected: commandContext,
      });
      const inspection = await agent.inspect(verified);
      if (inspection?.allowed !== true) {
        throw new LiveCoupangError(
          `The ${recipeStep.action.stepId} step was denied.`,
          inspection?.code || 'PREPARATION_STEP_DENIED',
        );
      }
      if (recipeStep.action.stepId === 'buy_now') {
        if (buyNowAttempted) {
          throw new LiveCoupangError('Buy now was already attempted.', 'BUY_NOW_REPLAY_DENIED', {
            buyNowAttempted: true,
          });
        }
        // Set this before the CDP call. A navigation can destroy the execution
        // context after the click, so an unavailable result must never retry.
        buyNowAttempted = true;
      }
      let result;
      try {
        result = await agent.execute(verified);
      } catch (error) {
        if (buyNowAttempted) {
          await onStep({ stepId: 'buy_now', status: 'outcome_unknown', code: 'BUY_NOW_OUTCOME_UNKNOWN' });
          throw new LiveCoupangError(
            'Buy now may have been activated. Review the visible browser and do not retry this approval.',
            'BUY_NOW_OUTCOME_UNKNOWN',
            { buyNowAttempted: true, cause: error },
          );
        }
        throw error;
      }
      const expected = EXPECTED_RESULT[recipeStep.action.stepId];
      if (result?.status !== expected.status || result?.code !== expected.code) {
        throw new LiveCoupangError(
          `The ${recipeStep.action.stepId} result was not exact.`,
          result?.code || 'PREPARATION_OUTCOME_UNKNOWN',
          { buyNowAttempted },
        );
      }
      const event = Object.freeze({
        stepId: recipeStep.action.stepId,
        status: result.status,
        code: result.code,
      });
      await onStep(event);
      history.push({
        sequence,
        actionKind: verified.action.kind,
        status: result.status,
        code: result.code,
      });
      sequence += 1;
      if (recipeStep.action.stepId === 'buy_now') {
        return Object.freeze({
          status: 'handoff',
          code: 'COUPANG_BUY_NOW_ACTIVATED',
          approvalId: approvedInput.approval.approvalId,
          offerIdentity: parsed.offerIdentity,
          buyNowAttempted: true,
        });
      }
    }
    throw new LiveCoupangError('The fixed route did not reach Buy now.', 'RECIPE_SEQUENCE_INVALID');
  } finally {
    // No page URL, DOM, or result is read after the Buy now attempt.
    await agent.detach();
  }
}
