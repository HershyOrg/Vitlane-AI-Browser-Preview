import test from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import { createCommandIssuer, signAuthorizedCommand, verifyAuthorizedCommand } from '../server/permit.mjs';
import { actionFromApprovedPreparation, sha256, validateStep } from '../server/protocol.mjs';
import {
  buildPurchasePreparationPlan,
  computePurchaseApprovalDigest,
  PURCHASE_PREPARATION_EFFECT,
} from '../server/purchase-recipes.mjs';
import { finishProposal, observation, step } from './fixtures.mjs';

const permitKey = 'test-only-permit-key-32-bytes-minimum-value';
const golden = JSON.parse(await readFile(new URL('../protocol/fixtures/golden-authorized-command.v1.json', import.meta.url), 'utf8'));

function approvedPlan({ approvalId = 'approval_permit_1', mode = 'single', revision = 1 } = {}) {
  const items = [{
    query: '승인된 첫 번째 상품',
    productUrl: 'https://www.coupang.com/vp/products/12345?itemId=23456&vendorItemId=34567',
    offerIdentity: { productId: '12345', itemId: '23456', vendorItemId: '34567' },
    quantity: 1,
    unitPriceCeilingKrw: 15_000,
    linePriceCeilingKrw: 15_000,
    options: { kind: 'none' },
  }];
  if (mode === 'multi') {
    items.push({
      query: '승인된 두 번째 상품',
      productUrl: 'https://www.coupang.com/vp/products/45678?itemId=56789&vendorItemId=67890',
      offerIdentity: { productId: '45678', itemId: '56789', vendorItemId: '67890' },
      quantity: 2,
      unitPriceCeilingKrw: 10_000,
      linePriceCeilingKrw: 20_000,
      options: { kind: 'none' },
    });
  }
  const unsigned = {
    merchantId: 'COUPANG',
    recipeVersion: '1',
    mode,
    approval: {
      approvalId,
      currency: 'KRW',
      revision,
      expiresAt: new Date(Date.now() + 5 * 60_000).toISOString(),
      totalPriceCeilingKrw: mode === 'single' ? 15_000 : 35_000,
    },
    items,
  };
  return {
    ...unsigned,
    approval: { ...unsigned.approval, approvalDigest: computePurchaseApprovalDigest(unsigned) },
  };
}

function approvedStep(planInput, cursor, {
  sequence = 1,
  leaseEpoch = 1,
  controlGeneration = 0,
  runId = 'run_purchase_permit',
} = {}) {
  const plan = buildPurchasePreparationPlan(planInput);
  const routeStep = plan.steps[cursor];
  assert.ok(routeStep, `missing purchase route cursor ${cursor}`);
  const lineIndex = routeStep.action.bindings.targetLineIndex ?? 0;
  const item = planInput.items[lineIndex];
  const cart = routeStep.action.stepId === 'start_checkout';
  const origin = cart ? 'https://cart.coupang.com' : 'https://www.coupang.com';
  const pathname = cart ? '/cartView.pang' : `/vp/products/${item.offerIdentity.productId}`;
  return validateStep(step({
    observation: observation({
      nativeMetadata: {
        tabId: 'tab_purchase',
        frameId: 'frame_main',
        documentEpoch: 7,
        topOrigin: origin,
        frameOrigin: origin,
        pathname,
        queryOrFragmentPresent: false,
        foreground: true,
      },
      untrustedPageData: {
        pageTypeHint: cart ? 'unknown' : 'product',
        title: '',
        visibleText: '',
        candidates: [],
      },
      privacy: {
        inputValuesOmitted: true,
        secretsOmitted: true,
        screenshotIncluded: false,
        urlQueryAndFragmentOmitted: true,
        collectionStatus: 'sanitized',
        handoffReasonCodes: [],
        excludedBoundaryCodes: ['PAYMENT_OR_COMMITMENT'],
      },
    }),
    commandContext: {
      ...step().commandContext,
      runId,
      sequence,
      leaseEpoch,
      controlGeneration,
    },
    approvedPreparation: {
      merchantId: planInput.merchantId,
      recipeVersion: planInput.recipeVersion,
      mode: planInput.mode,
      approval: planInput.approval,
      items: planInput.items,
      cursor,
      expectedPage: { origin, pathname },
    },
  }));
}

function authorizeApproved(issuer, validated) {
  return issuer.authorize(validated, async () => actionFromApprovedPreparation(validated));
}

function commandIds(prefix) {
  let next = 0;
  return () => `${prefix}_${++next}`;
}

test('golden protocol-v1 command fixes canonical action hash and HMAC permit bytes', () => {
  const signed = signAuthorizedCommand({
    commandId: golden.commandId,
    runId: golden.runId,
    deviceId: golden.deviceId,
    profileRef: golden.profileRef,
    sequence: golden.sequence,
    leaseEpoch: golden.leaseEpoch,
    controlGeneration: golden.controlGeneration,
    expiresAt: golden.expiresAt,
    action: golden.action,
  }, permitKey);
  assert.deepEqual(signed, golden);
  assert.equal(golden.actionHash, sha256(golden.action));
  assert.deepEqual(verifyAuthorizedCommand(golden, {
    permitKey,
    now: () => Date.parse('2030-01-01T00:00:00.000Z'),
    expected: { runId: 'run_001', deviceId: 'device_001', profileRef: 'profile_001', sequence: 1, leaseEpoch: 1, controlGeneration: 0 },
  }), golden);
});

test('verification rejects action tampering, permit tampering, expiry, and local binding mismatch', () => {
  const now = () => Date.parse('2030-01-01T00:00:00.000Z');
  assert.throws(() => verifyAuthorizedCommand({ ...golden, action: { ...golden.action, direction: 'up' } }, { permitKey, now }), (error) => error.code === 'ACTION_HASH_MISMATCH');
  assert.throws(() => verifyAuthorizedCommand({ ...golden, serverPermit: `${golden.serverPermit.slice(0, -1)}A` }, { permitKey, now }), (error) => error.code === 'INVALID_SERVER_PERMIT');
  assert.throws(() => verifyAuthorizedCommand({ ...golden, serverPermit: `${golden.serverPermit}!!` }, { permitKey, now }), (error) => error.code === 'INVALID_SERVER_PERMIT');
  assert.throws(() => verifyAuthorizedCommand(golden, { permitKey, now: () => Date.parse(golden.expiresAt) }), (error) => error.code === 'COMMAND_EXPIRED');
  assert.throws(() => verifyAuthorizedCommand(golden, { permitKey, now, expected: { controlGeneration: 1 } }), (error) => error.code === 'COMMAND_BINDING_MISMATCH');
});

test('issuer creates deterministic short-lived commands and returns exact replay for identical sequence input', async () => {
  let calls = 0;
  const issuer = createCommandIssuer({
    permitKey,
    now: () => Date.parse('2029-12-31T23:59:00.000Z'),
    commandId: () => 'cmd_deterministic_001',
    ttlMs: 10_000,
  });
  const validated = validateStep(step());
  const first = await issuer.authorize(validated, async () => { calls++; return finishProposal; });
  const replay = await issuer.authorize(validated, async () => { calls++; return { ...finishProposal, message: 'different' }; });
  assert.strictEqual(replay, first);
  assert.equal(calls, 1);
  assert.equal(first.expiresAt, '2029-12-31T23:59:10.000Z');
  assert.equal(first.sequence, 1);
  issuer.verify(first, { now: () => Date.parse('2029-12-31T23:59:01.000Z'), expected: validated.commandContext });
});

test('issuer coalesces concurrent identical requests before invoking the model', async () => {
  let calls = 0;
  let release;
  const gate = new Promise((resolve) => { release = resolve; });
  const issuer = createCommandIssuer({ permitKey, now: () => 1_000_000, commandId: () => 'cmd_concurrent', ttlMs: 10_000 });
  const validated = validateStep(step());
  const propose = async () => { calls++; await gate; return finishProposal; };
  const one = issuer.authorize(validated, propose);
  const two = issuer.authorize(validated, propose);
  release();
  const [a, b] = await Promise.all([one, two]);
  assert.deepEqual(a, b);
  assert.equal(calls, 1);
});

test('issuer rejects changed replay payloads, gaps, stale generation, stale lease, and changed binding', async () => {
  const issuer = createCommandIssuer({ permitKey, now: () => 1_000_000, commandId: () => 'cmd_1', ttlMs: 10_000 });
  const first = validateStep(step());
  await issuer.authorize(first, async () => finishProposal);

  const changedReplay = validateStep(step({ goal: '다른 목표' }));
  await assert.rejects(() => issuer.authorize(changedReplay, async () => finishProposal), (error) => error.code === 'REPLAY_CONFLICT');

  const gap = validateStep(step({ commandContext: { ...step().commandContext, sequence: 3 } }));
  await assert.rejects(() => issuer.authorize(gap, async () => finishProposal), (error) => error.code === 'OUT_OF_SEQUENCE');

  const second = validateStep(step({ commandContext: { ...step().commandContext, sequence: 2, controlGeneration: 2 } }));
  await issuer.authorize(second, async () => finishProposal);
  await assert.rejects(() => issuer.authorize(first, async () => finishProposal), (error) => error.code === 'STALE_GENERATION');
  const staleGeneration = validateStep(step({ commandContext: { ...step().commandContext, sequence: 3, controlGeneration: 1 } }));
  await assert.rejects(() => issuer.authorize(staleGeneration, async () => finishProposal), (error) => error.code === 'STALE_GENERATION');

  // validateStep itself refuses epoch zero before the issuer sees it.
  assert.throws(() => validateStep(step({ commandContext: { ...step().commandContext, sequence: 3, leaseEpoch: 0 } })));
});

test('a higher lease starts at sequence one and preserves device/profile binding', async () => {
  const ids = ['cmd_lease_1', 'cmd_lease_2'];
  const issuer = createCommandIssuer({ permitKey, now: () => 1_000_000, commandId: () => ids.shift(), ttlMs: 10_000 });
  await issuer.authorize(validateStep(step()), async () => finishProposal);
  const nextLeaseContext = { ...step().commandContext, leaseEpoch: 2, sequence: 1, controlGeneration: 1 };
  const next = await issuer.authorize(validateStep(step({ commandContext: nextLeaseContext })), async () => finishProposal);
  assert.equal(next.leaseEpoch, 2);
  const staleLease = { ...step().commandContext, leaseEpoch: 1, sequence: 2, controlGeneration: 1 };
  await assert.rejects(() => issuer.authorize(validateStep(step({ commandContext: staleLease })), async () => finishProposal), (error) => error.code === 'STALE_LEASE');
  const wrongDevice = { ...nextLeaseContext, leaseEpoch: 3, deviceId: 'other_device' };
  await assert.rejects(() => issuer.authorize(validateStep(step({ commandContext: wrongDevice })), async () => finishProposal), (error) => error.code === 'COMMAND_BINDING_MISMATCH');
});

test('issuer pins purchase approval fields and exact executable cursor progression', async () => {
  const issuer = createCommandIssuer({
    permitKey,
    now: () => 1_000_000,
    commandId: commandIds('cmd_purchase'),
    ttlMs: 10_000,
  });
  const approved = approvedPlan();
  const changedDigest = approvedPlan({ revision: 2 });
  const changedApproval = approvedPlan({ approvalId: 'approval_permit_changed' });

  const first = approvedStep(approved, 2);
  const firstCommand = await authorizeApproved(issuer, first);
  assert.strictEqual(await authorizeApproved(issuer, first), firstCommand);

  await assert.rejects(
    () => authorizeApproved(issuer, approvedStep(changedDigest, 3, { sequence: 2 })),
    (error) => error.code === 'APPROVAL_BINDING_MISMATCH',
  );
  await assert.rejects(
    () => authorizeApproved(issuer, approvedStep(approved, 4, { sequence: 2 })),
    (error) => error.code === 'APPROVAL_CURSOR_MISMATCH',
  );
  await authorizeApproved(issuer, approvedStep(approved, 3, { sequence: 2 }));

  await assert.rejects(
    () => authorizeApproved(issuer, approvedStep(changedApproval, 4, { sequence: 3 })),
    (error) => error.code === 'APPROVAL_BINDING_MISMATCH',
  );
  await authorizeApproved(issuer, approvedStep(approved, 4, { sequence: 3 }));
  await assert.rejects(
    () => authorizeApproved(issuer, approvedStep(approved, 4, { sequence: 4 })),
    (error) => error.code === 'APPROVAL_CURSOR_MISMATCH',
  );
});

test('issuer derives multi-item cursor progression while skipping navigation steps', async () => {
  const issuer = createCommandIssuer({
    permitKey,
    now: () => 1_000_000,
    commandId: commandIds('cmd_multi'),
    ttlMs: 10_000,
  });
  const approved = approvedPlan({ mode: 'multi' });
  const plan = buildPurchasePreparationPlan(approved);
  const executableCursors = plan.steps.flatMap((routeStep, cursor) =>
    [PURCHASE_PREPARATION_EFFECT.ACTIVATE, PURCHASE_PREPARATION_EFFECT.VERIFY]
      .includes(routeStep.action.effect) ? [cursor] : []);
  assert.deepEqual(executableCursors, [2, 3, 4, 7, 8, 9, 11]);

  for (const [index, cursor] of executableCursors.entries()) {
    await authorizeApproved(issuer, approvedStep(approved, cursor, { sequence: index + 1 }));
  }
  await assert.rejects(
    () => authorizeApproved(issuer, approvedStep(approved, 11, { sequence: 8 })),
    (error) => error.code === 'APPROVAL_CURSOR_MISMATCH',
  );
});

test('non-purchase commands preserve purchase authority without advancing it', async () => {
  const issuer = createCommandIssuer({
    permitKey,
    now: () => 1_000_000,
    commandId: commandIds('cmd_interleaved'),
    ttlMs: 10_000,
  });
  const approved = approvedPlan();
  await authorizeApproved(issuer, approvedStep(approved, 2));
  const generic = validateStep(step({
    commandContext: { ...step().commandContext, runId: 'run_purchase_permit', sequence: 2 },
  }));
  await issuer.authorize(generic, async () => finishProposal);

  await assert.rejects(
    () => authorizeApproved(issuer, approvedStep(approved, 4, { sequence: 3 })),
    (error) => error.code === 'APPROVAL_CURSOR_MISMATCH',
  );
  await authorizeApproved(issuer, approvedStep(approved, 3, { sequence: 3 }));
});

test('a higher lease resets purchase authority and requires a fresh route start', async () => {
  const issuer = createCommandIssuer({
    permitKey,
    now: () => 1_000_000,
    commandId: commandIds('cmd_purchase_lease'),
    ttlMs: 10_000,
  });
  const firstApproval = approvedPlan({ approvalId: 'approval_first_lease' });
  const secondApproval = approvedPlan({ approvalId: 'approval_second_lease' });
  await authorizeApproved(issuer, approvedStep(firstApproval, 2));

  await assert.rejects(
    () => authorizeApproved(issuer, approvedStep(secondApproval, 3, {
      leaseEpoch: 2, sequence: 1, controlGeneration: 1,
    })),
    (error) => error.code === 'APPROVAL_CURSOR_MISMATCH',
  );
  await authorizeApproved(issuer, approvedStep(secondApproval, 2, {
    leaseEpoch: 2, sequence: 1, controlGeneration: 1,
  }));
  await assert.rejects(
    () => authorizeApproved(issuer, approvedStep(firstApproval, 3, {
      leaseEpoch: 2, sequence: 2, controlGeneration: 1,
    })),
    (error) => error.code === 'APPROVAL_BINDING_MISMATCH',
  );
  await authorizeApproved(issuer, approvedStep(secondApproval, 3, {
    leaseEpoch: 2, sequence: 2, controlGeneration: 1,
  }));
});

test('failed proposal does not pin or advance purchase cursor', async () => {
  const issuer = createCommandIssuer({
    permitKey,
    now: () => 1_000_000,
    commandId: commandIds('cmd_failed_purchase'),
    ttlMs: 10_000,
  });
  const first = approvedStep(approvedPlan(), 2);
  await assert.rejects(
    () => issuer.authorize(first, async () => { throw new Error('provider failed'); }),
    /provider failed/,
  );
  await authorizeApproved(issuer, first);
});
